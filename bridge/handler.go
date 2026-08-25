// SpecWire Bridge webhook 处理管线：
// 验签 → Issue 发布或 archived Push Hook 过滤 → project allowlist → 判重/归档 → multica CLI → 落库。
// 对应 SPECWIRE_BRIDGE_DESIGN.md §5。
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	maxBodyBytes = 1 << 20 // 1 MiB，防异常大 payload
	// maxWebhookAge 允许的 webhook-timestamp 最大回退窗口（防重放）。
	maxWebhookAge = 5 * time.Minute
)

// webhookHandler 承载 Bridge 处理管线。
type webhookHandler struct {
	cfgPtr *atomic.Pointer[Config] // 运行时可变配置（admin API copy-on-write 替换；每次请求 Load 取一致快照）
	store  *Store
	gitlab *gitlabClient // GitLab API 客户端（token/URL 启动时固定，admin 不修改）
}

// cfg 返回当前配置快照。
func (h *webhookHandler) cfg() *Config { return h.cfgPtr.Load() }

// gitlabPushPayload 是 GitLab Push Hook payload 的最小字段集（设计 §5）。
type gitlabPushPayload struct {
	Ref        string         `json:"ref"`
	After      string         `json:"after"`
	Project    gitlabProject  `json:"project"`
	HeadCommit *gitlabCommit  `json:"head_commit"`
	Commits    []gitlabCommit `json:"commits"`
}

type gitlabProject struct {
	PathWithNamespace string `json:"path_with_namespace"`
}

type gitlabCommit struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

// webhookResult 用于日志与响应正文。
type webhookResult string

const (
	resultIgnored   webhookResult = "ignored"
	resultDuplicate webhookResult = "duplicate"
	resultCreated   webhookResult = "created"
	resultError     webhookResult = "error"
)

// ServeHTTP 是 GitLab webhook 入口（POST /gitlab/specwire）。
func (h *webhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// deliveryKey 取自 Standard Webhooks 的 webhook-id（GitLab 19.1+ 每个请求都带）。
	deliveryKey := r.Header.Get("webhook-id")
	if deliveryKey == "" {
		deliveryKey = r.Header.Get("X-Gitlab-Delivery") // 兜底
	}

	// 1. 读取原始 body（验签需要 raw body，设计 §5.1）
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		slog.Error("read body failed", "delivery_key", deliveryKey, "error", err)
		h.respond(w, deliveryKey, "", "", "", resultIgnored, http.StatusOK, "read body failed")
		return
	}

	// 2. HMAC 验签（设计 §5.1 / D15，Standard Webhooks 规范）：多 token 任一匹配即通过
	if err := verifySignature(h.cfg().WebhookSecrets,
		r.Header.Get("webhook-id"),
		r.Header.Get("webhook-timestamp"),
		r.Header.Get("webhook-signature"),
		body, time.Now()); err != nil {
		slog.Warn("webhook unauthorized", "delivery_key", deliveryKey, "remote", r.RemoteAddr, "reason", err.Error())
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// 3. 事件类型分流：Issue Hook 发布；Push Hook 只负责归档完成
	switch r.Header.Get("X-Gitlab-Event") {
	case "Push Hook":
		h.handlePushHook(w, r, deliveryKey, body)
	case "Issue Hook":
		h.handleIssueHook(w, r, deliveryKey, body)
	default:
		h.respond(w, deliveryKey, "", "", "", resultIgnored, http.StatusOK, "not a push/issue hook")
	}
}

// handlePushHook 处理 Push Hook 归档事件；Push Hook 不再负责发布新任务。
func (h *webhookHandler) handlePushHook(w http.ResponseWriter, r *http.Request, deliveryKey string, body []byte) {
	// 4. 解析 payload
	var p gitlabPushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		// 非法 JSON：200 ignored（TC-BR-027）；GitLab 重试不会修复坏 payload，避免重试风暴
		slog.Error("invalid payload json", "delivery_key", deliveryKey, "error", err)
		h.respond(w, deliveryKey, "", "", "", resultIgnored, http.StatusOK, "invalid payload json")
		return
	}

	project := p.Project.PathWithNamespace

	// 5. project allowlist（设计 §5.3，SPECWIRE_ALLOWED_PROJECTS）
	if !h.cfg().AllowedProjects[project] {
		h.respond(w, deliveryKey, project, "", "", resultIgnored, http.StatusOK, "project not allowed")
		return
	}

	// 6. 分支过滤 + 删除分支（设计 §5.4）
	if p.Ref != h.cfg().RefFilter {
		h.respond(w, deliveryKey, project, "", "", resultIgnored, http.StatusOK, "ref not target")
		return
	}
	if strings.Trim(p.After, "0") == "" {
		h.respond(w, deliveryKey, project, "", "", resultIgnored, http.StatusOK, "branch deleted (zero after)")
		return
	}

	// 7. 只解析 archived trailer；普通提交和发布 trailer 都不产生副作用。
	events := parseArchiveTrailers(p)
	if len(events) == 0 {
		h.respond(w, deliveryKey, project, "", "", resultIgnored, http.StatusOK, "no specwire trailer")
		return
	}

	// 8. 逐事件完成投影；归档不创建新卡，因此响应保持 ignored 语义。
	for _, ev := range events {
		h.handleArchived(r, deliveryKey, project, ev)
	}
	h.respond(w, deliveryKey, project, "", "", resultIgnored, http.StatusOK, "archive event processed")
}

// handlePublication 处理一个 GitLab change Issue 发布。
// 稳定键判重 → 创建 Multica 投影 → 记录创建状态；Issue 关联由调用方幂等补齐。
func (h *webhookHandler) handlePublication(r *http.Request, deliveryKey, project string, pub publication) (webhookResult, string) {
	projectID := ""
	if len(h.cfg().ProjectMap) > 0 {
		projectID = h.cfg().ProjectMap[project]
		if projectID == "" {
			// 启动校验已保证 allowlist ⊆ map，此处为防御性检查
			slog.Error("no multica project mapping", "delivery_key", deliveryKey, "project", project)
			return resultError, "no multica project mapping"
		}
	} else {
		projectID = h.cfg().MulticaProjectID
	}

	stableKey := stableKeyOf(project, pub.ChangeID, pub.BranchHeadSHA, projectID)
	res, err := h.store.Claim(stableKey, deliveryKey, project, pub.ChangeID, pub.BranchHeadSHA)
	if err != nil {
		slog.Error("claim failed", "delivery_key", deliveryKey, "stable_key", stableKey, "error", err)
		return resultError, "store claim failed"
	}
	if res == Duplicate {
		return resultDuplicate, "duplicate stable key"
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg().CLITimeout)
	defer cancel()
	issueID, err := createBacklogIssue(ctx, h.cfg(), projectID, pub.ChangeID,
		buildDescription(project, pub.ChangeID, pub.Branch, pub.BranchHeadSHA, h.cfg().RefFilter),
		pub.Status, pub.Assignee)
	if err != nil {
		if merr := h.store.MarkError(stableKey, err.Error()); merr != nil {
			slog.Error("mark error failed", "stable_key", stableKey, "error", merr)
		}
		slog.Error("multica create failed", "delivery_key", deliveryKey, "stable_key", stableKey, "error", err)
		return resultError, "multica cli failed"
	}
	if err := h.store.MarkCreated(stableKey, issueID, projectID); err != nil {
		slog.Error("mark created failed", "stable_key", stableKey, "issue_id", issueID, "error", err)
	}
	slog.Info("issue created",
		"delivery_key", deliveryKey, "project", project, "multica_project_id", projectID,
		"change_id", pub.ChangeID, "branch", pub.Branch, "branch_head_sha", pub.BranchHeadSHA,
		"status", pub.Status, "assignee", pub.Assignee, "multica_issue_id", issueID)
	return resultCreated, "issue " + issueID
}

// gitlabIssuePayload 是 GitLab Issue Hook payload 的最小字段集（v2）。
type gitlabIssuePayload struct {
	ObjectKind       string                `json:"object_kind"`
	ObjectAttributes gitlabIssueAttributes `json:"object_attributes"`
	Project          gitlabProject         `json:"project"`
}

type gitlabIssueAttributes struct {
	IID         int                `json:"iid"`
	Action      string             `json:"action"`
	Labels      []gitlabIssueLabel `json:"labels"`
	Description string             `json:"description"`
}

type gitlabIssueLabel struct {
	Title string `json:"title"`
}

// handleIssueHook 处理 GitLab change Issue 发布：
// 仅处理 object_kind=issue、action=open、labels 含 change 的 Issue，
// 并解析 change_id/branch/branch_head_sha 及可选状态/分配字段。
func (h *webhookHandler) handleIssueHook(w http.ResponseWriter, r *http.Request, deliveryKey string, body []byte) {
	var p gitlabIssuePayload
	if err := json.Unmarshal(body, &p); err != nil {
		slog.Error("invalid issue hook payload", "delivery_key", deliveryKey, "error", err)
		h.respond(w, deliveryKey, "", "", "", resultIgnored, http.StatusOK, "invalid payload json")
		return
	}
	if p.ObjectKind != "issue" || p.ObjectAttributes.Action != "open" {
		h.respond(w, deliveryKey, "", "", "", resultIgnored, http.StatusOK, "not an issue open event")
		return
	}
	hasChangeTag := false
	for _, l := range p.ObjectAttributes.Labels {
		if l.Title == "change" {
			hasChangeTag = true
			break
		}
	}
	if !hasChangeTag {
		h.respond(w, deliveryKey, "", "", "", resultIgnored, http.StatusOK, "issue without change label")
		return
	}

	project := p.Project.PathWithNamespace
	if !h.cfg().AllowedProjects[project] {
		h.respond(w, deliveryKey, project, "", "", resultIgnored, http.StatusOK, "project not allowed")
		return
	}

	changeID, branch, headSHA, status, assignee, ok := parseIssueDescription(p.ObjectAttributes.Description)
	if !ok {
		h.respond(w, deliveryKey, project, "", "", resultIgnored, http.StatusOK, "issue description missing/invalid fields")
		return
	}

	pub := publication{ChangeID: changeID, Branch: branch, BranchHeadSHA: headSHA, Status: status, Assignee: assignee}
	res, detail := h.handlePublication(r, deliveryKey, project, pub)

	// 首次创建和稳定键重放都尝试补齐关联，允许修复外部建卡成功但关联落库失败的窗口。
	if res == resultCreated || res == resultDuplicate {
		if err := h.store.InsertIssueLink(project, p.ObjectAttributes.IID, changeID, branch, headSHA); err != nil {
			slog.Error("record issue link failed", "delivery_key", deliveryKey, "project", project,
				"issue_iid", p.ObjectAttributes.IID, "change_id", changeID, "error", err)
			h.respond(w, deliveryKey, project, changeID, headSHA, resultError, http.StatusBadGateway, "record issue link failed")
			return
		}
		slog.Info("issue link recorded",
			"delivery_key", deliveryKey, "project", project, "issue_iid", p.ObjectAttributes.IID,
			"change_id", changeID, "branch", branch, "branch_head_sha", headSHA)
	}

	switch res {
	case resultCreated:
		h.respond(w, deliveryKey, project, changeID, headSHA, resultCreated, http.StatusOK, detail)
	case resultDuplicate:
		h.respond(w, deliveryKey, project, changeID, headSHA, resultDuplicate, http.StatusOK, detail)
	default:
		h.respond(w, deliveryKey, project, changeID, headSHA, resultError, http.StatusBadGateway, detail)
	}
}

// parseIssueDescription 解析发布 Issue 描述中的字段行（v2）：
// change_id / branch / branch_head_sha 必填；SpecWire-Status / SpecWire-Assignee 可选（D23）。
// Status 非 backlog/todo 视为无效；缺必填字段视为无效。
func parseIssueDescription(desc string) (changeID, branch, headSHA, status, assignee string, ok bool) {
	for _, line := range strings.Split(desc, "\n") {
		line = strings.TrimSpace(line)
		if v, found := strings.CutPrefix(line, "change_id:"); found {
			changeID = strings.TrimSpace(v)
		}
		if v, found := strings.CutPrefix(line, "branch:"); found {
			branch = strings.TrimSpace(v)
		}
		if v, found := strings.CutPrefix(line, "branch_head_sha:"); found {
			headSHA = strings.TrimSpace(v)
		}
		if v, found := strings.CutPrefix(line, "SpecWire-Status:"); found {
			status = strings.TrimSpace(v)
		}
		if v, found := strings.CutPrefix(line, "SpecWire-Assignee:"); found {
			assignee = strings.TrimSpace(v)
		}
	}
	if changeID == "" || branch == "" || headSHA == "" {
		return "", "", "", "", "", false
	}
	if status != "" && status != "backlog" && status != "todo" {
		return "", "", "", "", "", false
	}
	return changeID, branch, headSHA, status, assignee, true
}

// handleArchived 处理单个归档事件（设计 §5.5 / D17 + v2）：
//   - 不创建开发 Backlog（契约：archived 不得触发开发）；
//   - 自动将该项目+change 最新创建且 state=created 的实现卡置为 Done，
//     使"规格归档"同时闭环"执行投影"，免去人工在 Multica 收尾；
//   - v2：按 issue_links 反查并关闭该 change 的全部关联 GitLab Issue
//     （token 未配置或 API 失败 → 降级，不阻塞 Multica 置 done）。
//
// 匹配规则：归档 push 的 after_sha 与建卡时不同，稳定键无法精确匹配，
// 只能按 project+change_id 匹配（见 Store.LatestCreatedIssue）。
func (h *webhookHandler) handleArchived(r *http.Request, deliveryKey, project string, ev specwireEvent) {
	issueID, err := h.store.LatestCreatedIssue(project, ev.ChangeID)
	if err != nil {
		slog.Error("archive lookup failed", "delivery_key", deliveryKey, "project", project, "change_id", ev.ChangeID, "error", err)
		return
	}
	if issueID == "" {
		slog.Info("archived event, no issue to close", "delivery_key", deliveryKey, "project", project, "change_id", ev.ChangeID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg().CLITimeout)
	defer cancel()
	if err := updateIssueStatus(ctx, h.cfg(), issueID, "done"); err != nil {
		// 失败只记日志：archived 是应忽略的事件，GitLab 重试无意义，留给人工处理
		slog.Error("archive: mark issue done failed", "delivery_key", deliveryKey, "issue_id", issueID, "error", err)
		return
	}
	slog.Info("archived: issue set to done",
		"delivery_key", deliveryKey, "project", project, "change_id", ev.ChangeID, "issue_id", issueID)

	// v2：关闭关联 GitLab Issue（降级语义：不阻塞上面已完成的置 done）
	h.closeLinkedGitLabIssues(r, deliveryKey, project, ev.ChangeID)
}

// closeLinkedGitLabIssues 按 issue_links 关闭该 change 的全部关联发布 Issue（v2）。
func (h *webhookHandler) closeLinkedGitLabIssues(r *http.Request, deliveryKey, project, changeID string) {
	links, err := h.store.ListIssueLinks(project, changeID)
	if err != nil {
		slog.Error("archive: list issue links failed", "delivery_key", deliveryKey, "project", project, "change_id", changeID, "error", err)
		return
	}
	if len(links) == 0 {
		slog.Warn("archived: no issue_links for change, skip closing gitlab issues",
			"delivery_key", deliveryKey, "project", project, "change_id", changeID)
		return
	}
	for _, l := range links {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		err := h.gitlab.CloseIssue(ctx, l.GitlabProject, l.IssueIID)
		cancel()
		if err != nil {
			if errors.Is(err, ErrGitLabNotConfigured) {
				slog.Warn("archived: gitlab token not configured, skip closing issue",
					"delivery_key", deliveryKey, "project", project, "issue_iid", l.IssueIID)
				return // 未配置则全部跳过
			}
			slog.Error("archived: close gitlab issue failed",
				"delivery_key", deliveryKey, "project", project, "issue_iid", l.IssueIID, "error", err)
			continue
		}
		slog.Info("archived: gitlab issue closed",
			"delivery_key", deliveryKey, "project", project, "issue_iid", l.IssueIID, "change_id", changeID)
	}
}

// verifySignature 按 Standard Webhooks 规范验证 HMAC-SHA256 签名（设计 §5.1 / D15）。
// GitLab 19.1+ 的签名消息串为 "{webhook-id}.{webhook-timestamp}.{raw_body}"，
// key 为 signing token 去掉 whsec_ 前缀后 base64 解码；签名格式 "v1,<base64>"，
// header 可能含多个空格分隔的签名（GitLab 目前只发一个）。
// 多 token：secrets 中任一 token 验签通过即放行（任一不匹配 → 401）。
func verifySignature(secrets []string, id, ts, sigHeader string, body []byte, now time.Time) error {
	if id == "" || ts == "" || sigHeader == "" {
		return fmt.Errorf("missing webhook-id/webhook-timestamp/webhook-signature headers")
	}
	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid webhook-timestamp %q", ts)
	}
	// 防重放：只接受当前时间 ±maxWebhookAge 内的请求
	if d := now.Sub(time.Unix(tsInt, 0)); d > maxWebhookAge || d < -maxWebhookAge {
		return fmt.Errorf("webhook-timestamp %s out of %s window", ts, maxWebhookAge)
	}
	msg := []byte(id + "." + ts + "." + string(body))
	for _, secret := range secrets {
		key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
		if err != nil {
			continue // 启动校验已保证合法；防御性跳过非法项
		}
		mac := hmac.New(sha256.New, key)
		mac.Write(msg)
		computed := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
		for _, sig := range strings.Split(sigHeader, " ") {
			if subtle.ConstantTimeCompare([]byte(sig), []byte(computed)) == 1 {
				return nil
			}
		}
	}
	return fmt.Errorf("signature mismatch")
}

// stableKeyOf 业务稳定键：<project path>:<change ID>:<branch head SHA>:<multica project ID>。
// D21：稳定键含目标 multica project，同一发布事件在不同目标 project 下产生不同键——
// 配置变更后重放会自然建到新 project（修复建错归属），同一配置下重放仍幂等。
func stableKeyOf(project, changeID, branchHeadSHA, multicaProjectID string) string {
	return project + ":" + changeID + ":" + branchHeadSHA + ":" + multicaProjectID
}

// publication 是 GitLab change Issue 携带的不可变发布元数据。
type publication struct {
	ChangeID      string
	Branch        string
	BranchHeadSHA string
	Status        string
	Assignee      string
}

// specwireEvent 是归档 Push Hook 中解析出的事件。
type specwireEvent struct {
	ChangeID string
	Event    string
}

// parseArchiveTrailers 从 push 的全部 commit 中收集 archived 事件：
//   - 合并 head_commit 与 commits（按 commit id 去重）；
//   - 从新到旧遍历，只接受 archived trailer；
//   - 同一 change_id 在同一 push 中出现多次 → 只保留最新一次。
func parseArchiveTrailers(p gitlabPushPayload) []specwireEvent {
	seen := map[string]bool{}
	var all []gitlabCommit
	if p.HeadCommit != nil && !seen[p.HeadCommit.ID] {
		seen[p.HeadCommit.ID] = true
		all = append(all, *p.HeadCommit)
	}
	for _, c := range p.Commits {
		if !seen[c.ID] {
			seen[c.ID] = true
			all = append(all, c)
		}
	}

	var events []specwireEvent
	latest := map[string]bool{}
	for i := len(all) - 1; i >= 0; i-- {
		id, ok := parseArchiveTrailer(all[i].Message)
		if !ok {
			continue
		}
		if latest[id] {
			continue // 该 change 已取到更新的提交
		}
		latest[id] = true
		events = append(events, specwireEvent{ChangeID: id, Event: "archived"})
	}
	return events
}

// parseArchiveTrailer 从单条 commit message 解析归档 trailer。
func parseArchiveTrailer(msg string) (changeID string, ok bool) {
	var event string
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		if v, found := strings.CutPrefix(line, "SpecWire-Event:"); found {
			event = strings.TrimSpace(v)
		}
		if v, found := strings.CutPrefix(line, "SpecWire-Change:"); found {
			changeID = strings.TrimSpace(v)
		}
	}
	return changeID, event == "archived" && changeID != ""
}

// buildDescription 生成 Multica Issue 描述，只携带 SpecWire 的发布元数据。
// 本地 Skill、Agent、MR 和 archive 命令不写入 Bridge 的运行时描述契约。
func buildDescription(project, changeID, branch, branchHeadSHA, refFilter string) string {
	target := strings.TrimPrefix(refFilter, "refs/heads/")
	return fmt.Sprintf(`[SpecWire Backlog] 由 GitLab change Issue 自动创建。

repository: %s
change_id: %s
branch: %s
branch_head_sha: %s
target_branch: %s
`, project, changeID, branch, branchHeadSHA, target)
}

// respond 写响应并记录结构化结果日志（设计 §8：一条请求一条结果日志，不记录 secret/payload 全文）。
func (h *webhookHandler) respond(w http.ResponseWriter, delivery, project, change, sha string, res webhookResult, status int, detail string) {
	attrs := []any{"delivery_key", delivery, "result", res, "http_status", status}
	if project != "" {
		attrs = append(attrs, "project", project)
	}
	if change != "" {
		attrs = append(attrs, "change_id", change)
	}
	if sha != "" {
		attrs = append(attrs, "after_sha", sha)
	}
	if detail != "" {
		attrs = append(attrs, "detail", detail)
	}
	if status >= 500 {
		slog.Error("webhook handled", attrs...)
	} else {
		slog.Info("webhook handled", attrs...)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write([]byte(string(res))); err != nil {
		slog.Warn("write response failed", "delivery_key", delivery, "error", err)
	}
}
