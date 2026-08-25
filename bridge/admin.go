// SpecWire Bridge 配置管理：/admin/ 管理页面（go:embed 静态页）+ /admin/api/* 变更 API。
// 原则（design.md §2/§3）：
//   - 页面只读快照 + 变更 API，不直接暴露 .env；
//   - 变更写运行时配置（copy-on-write 原子替换指针，webhook 侧无锁读快照）；
//   - POST /admin/api/apply 原子写回 env 文件（临时文件 + rename），页面提示重启生效（无热加载）；
//   - 安全：SPECWIRE_ADMIN_TOKEN 配置后要求 X-Admin-Token；未配置仅回环可访问。
package main

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed admin/static/index.html
var adminIndexHTML []byte

// adminState 是 admin API 的运行时状态：
//   - SecretByPath：GitLab 项目 → 专属 signing token 索引，供 token 轮换时定位并移除旧 token；
//   - RestartRequired：apply 后置 true，页面显示"重启生效"提示；仅内存态，
//     进程重启即自然复位（持久化会误导重启后的页面仍显示提示）。
type adminState struct {
	SecretByPath    map[string]string `json:"secret_by_path"`
	RestartRequired bool              `json:"-"`
}

// adminHandler 承载 /admin 路由。
type adminHandler struct {
	cfgPtr     *atomic.Pointer[Config]
	gitlab     *gitlabClient
	envPath    string // apply 写回的目标 env 文件（容器部署挂载卷内的 overlay）
	statePath  string // admin-state.json 路径（持久化 secretByPath）
	adminToken string // SPECWIRE_ADMIN_TOKEN；空 → 仅回环可访问

	mu sync.RWMutex // 序列化 admin 变更与状态读写
	st *adminState
}

// newAdminHandler 构造 admin handler 并加载持久化状态（文件缺失/损坏 → 空状态）。
func newAdminHandler(cfgPtr *atomic.Pointer[Config], gitlab *gitlabClient, adminToken, envPath, statePath string) *adminHandler {
	h := &adminHandler{
		cfgPtr:     cfgPtr,
		gitlab:     gitlab,
		envPath:    envPath,
		statePath:  statePath,
		adminToken: adminToken,
		st:         &adminState{SecretByPath: map[string]string{}},
	}
	h.loadState()
	// 剪除索引中已不在 secrets 列表里的旧 token（.env 手工改过/轮换后残留）
	h.mu.Lock()
	defer h.mu.Unlock()
	secrets := h.cfgPtr.Load().WebhookSecrets
	for k, v := range h.st.SecretByPath {
		if !slices.Contains(secrets, v) {
			delete(h.st.SecretByPath, k)
		}
	}
	return h
}

func (h *adminHandler) loadState() {
	b, err := os.ReadFile(h.statePath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("admin state load failed", "path", h.statePath, "error", err)
		}
		return
	}
	var st adminState
	if err := json.Unmarshal(b, &st); err != nil {
		slog.Warn("admin state parse failed", "path", h.statePath, "error", err)
		return
	}
	if st.SecretByPath == nil {
		st.SecretByPath = map[string]string{}
	}
	h.st = &st
}

// persistState 原子写回 admin-state.json（临时文件 + rename）。
func (h *adminHandler) persistState() error {
	b, err := json.MarshalIndent(h.st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal admin state: %w", err)
	}
	return atomicWriteFile(h.statePath, b, 0o600)
}

// atomicWriteFile 以临时文件 + rename 原子写文件（保留目录内其他文件与权限）。
func atomicWriteFile(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后为 no-op
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// ---------- 路由 ----------

// ServeHTTP 分发 /admin 下的静态页面与 API。
func (h *adminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin")
	switch {
	case path == "/" || path == "/index.html":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(adminIndexHTML)
	case strings.HasPrefix(path, "/api/"):
		if !h.authorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		h.routeAPI(w, r, strings.TrimPrefix(path, "/api"))
	default:
		http.NotFound(w, r)
	}
}

func (h *adminHandler) routeAPI(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case path == "/config" && r.Method == http.MethodGet:
		h.handleConfig(w, r)
	case path == "/projects" && r.Method == http.MethodPost:
		h.handleAddProject(w, r)
	case strings.HasPrefix(path, "/projects/") && r.Method == http.MethodDelete:
		h.handleRemoveProject(w, r, strings.TrimPrefix(path, "/projects/"))
	case strings.HasSuffix(path, "/rotate") && r.Method == http.MethodPost:
		h.handleRotateToken(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/hooks/"), "/rotate"))
	case strings.HasPrefix(path, "/hooks/") && r.Method == http.MethodPost:
		h.handleUpsertHook(w, r, strings.TrimPrefix(path, "/hooks/"))
	case path == "/apply" && r.Method == http.MethodPost:
		h.handleApply(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// authorized 校验 admin 访问：
//   - 配置了 SPECWIRE_ADMIN_TOKEN → 要求 X-Admin-Token 头（常数时间比较）；
//   - 未配置 → 仅回环地址可访问（PoC 默认本机可用；容器部署请配置 token）。
func (h *adminHandler) authorized(r *http.Request) bool {
	if h.adminToken != "" {
		got := r.Header.Get("X-Admin-Token")
		return subtle.ConstantTimeCompare([]byte(got), []byte(h.adminToken)) == 1
	}
	return isLoopbackAddr(r.RemoteAddr)
}

func isLoopbackAddr(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write json response failed", "error", err)
	}
}

// ---------- 配置快照（3.1） ----------

// hookStatus 是单项目 hook 状态（存在/缺失/查询失败）。
type hookStatus struct {
	Exists       bool   `json:"exists"`
	ID           int    `json:"id,omitempty"`
	URL          string `json:"url,omitempty"`
	PushEvents   bool   `json:"push_events,omitempty"`
	IssuesEvents bool   `json:"issues_events,omitempty"`
	Error        string `json:"error,omitempty"`
}

type projectSnapshot struct {
	Path             string      `json:"path"`
	MulticaProjectID string      `json:"multica_project_id"`
	HasSecret        bool        `json:"has_secret"`
	SecretSource     string      `json:"secret_source,omitempty"` // dedicated（项目专属）| shared（legacy 共享 secret）
	Hook             *hookStatus `json:"hook"`
}

type configSnapshot struct {
	AllowedProjects  []string          `json:"allowed_projects"`
	ProjectMap       map[string]string `json:"project_map"`
	DefaultMulticaID string            `json:"default_multica_project_id"`
	MulticaProjects  []multicaProject  `json:"multica_projects"`
	MulticaError     string            `json:"multica_error,omitempty"`
	Projects         []projectSnapshot `json:"projects"`
	GitlabConfigured bool              `json:"gitlab_configured"`
	GitlabURL        string            `json:"gitlab_url"`
	WebhookURL       string            `json:"webhook_url"`
	SecretCount      int               `json:"secret_count"`
	RestartRequired  bool              `json:"restart_required"`
}

// handleConfig 返回配置快照：项目/mapping/hook 状态/secret 是否存在（不含 token 明文）。
func (h *adminHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cfg := h.cfgPtr.Load()
	h.mu.RLock()
	defer h.mu.RUnlock()

	keys := sortedKeys(cfg.AllowedProjects)
	snap := configSnapshot{
		AllowedProjects:  keys,
		ProjectMap:       cfg.ProjectMap,
		DefaultMulticaID: cfg.MulticaProjectID,
		GitlabConfigured: h.gitlab.configured(),
		GitlabURL:        h.gitlab.baseURL,
		WebhookURL:       cfg.WebhookURL,
		SecretCount:      len(cfg.WebhookSecrets),
		RestartRequired:  h.st.RestartRequired,
	}

	// Multica 项目列表（添加项目表单下拉用；失败不阻塞快照）
	projects, err := listProjects(ctx, cfg)
	if err != nil {
		slog.Warn("admin config: list multica projects failed", "error", err)
		snap.MulticaError = err.Error()
	} else {
		snap.MulticaProjects = projects
	}

	// 每个项目的 hook 状态（GitLab API；未配置 → unknown）
	for _, p := range keys {
		ps := projectSnapshot{Path: p}
		if token := h.st.SecretByPath[p]; token != "" {
			ps.HasSecret = true
			ps.SecretSource = "dedicated"
		} else if len(cfg.WebhookSecrets) > 0 {
			ps.HasSecret = true
			ps.SecretSource = "shared" // legacy 共享 secret：未按项目归因，但可验签
		}
		if id, ok := cfg.ProjectMap[p]; ok {
			ps.MulticaProjectID = id
		} else {
			ps.MulticaProjectID = cfg.MulticaProjectID
		}
		if h.gitlab.configured() {
			hooks, err := h.gitlab.ListHooks(ctx, p)
			if err != nil {
				ps.Hook = &hookStatus{Exists: false, Error: err.Error()}
			} else if id := h.gitlab.FindHookByURL(hooks, cfg.WebhookURL); id > 0 {
				for _, hk := range hooks {
					if hk.ID == id {
						ps.Hook = &hookStatus{Exists: true, ID: hk.ID, URL: hk.URL, PushEvents: hk.PushEvents, IssuesEvents: hk.IssuesEvents}
						break
					}
				}
			} else {
				ps.Hook = &hookStatus{Exists: false}
			}
		} else {
			ps.Hook = &hookStatus{Exists: false, Error: "SPECWIRE_GITLAB_TOKEN 未配置"}
		}
		snap.Projects = append(snap.Projects, ps)
	}
	writeJSON(w, http.StatusOK, snap)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------- 项目增删（3.2） ----------

// addProjectRequest 是 POST /admin/api/projects 的请求体。
type addProjectRequest struct {
	GitlabPath     string `json:"gitlab_path"`
	MulticaProject string `json:"multica_project"` // Multica project title 或 UUID
}

// handleAddProject 添加项目：校验 GitLab 项目存在 + Multica project 存在 → 更新 allowlist 与映射。
// 映射回填：已存在但无映射的 allowlist 项目补默认项目 ID（保持其现有行为，且通过启动一致性校验）。
func (h *adminHandler) handleAddProject(w http.ResponseWriter, r *http.Request) {
	var req addProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	req.GitlabPath = strings.TrimSpace(req.GitlabPath)
	req.MulticaProject = strings.TrimSpace(req.MulticaProject)
	if req.GitlabPath == "" || req.MulticaProject == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "gitlab_path 与 multica_project 均必填"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cfg := h.cfgPtr.Load()
	if !h.gitlab.configured() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "SPECWIRE_GITLAB_TOKEN 未配置，无法校验 GitLab 项目"})
		return
	}
	exists, err := h.gitlab.ProjectExists(ctx, req.GitlabPath)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "校验 GitLab 项目失败: " + err.Error()})
		return
	}
	if !exists {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("GitLab 项目 %q 不存在", req.GitlabPath)})
		return
	}

	projects, err := listProjects(ctx, cfg)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "获取 Multica 项目列表失败: " + err.Error()})
		return
	}
	mapped, err := resolveProjectMap(map[string]string{req.GitlabPath: req.MulticaProject}, projects)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	multicaID := mapped[req.GitlabPath]
	var out map[string]string
	err = h.mutate(func(cfg *Config) error {
		if cfg.AllowedProjects[req.GitlabPath] {
			return fmt.Errorf("项目 %q 已在 allowlist 中", req.GitlabPath)
		}
		cfg.AllowedProjects[req.GitlabPath] = true
		// 映射回填：保持所有 allowlist 项目都有映射（启动校验要求）
		if out == nil {
			out = map[string]string{}
		}
		for p := range cfg.AllowedProjects {
			if v, ok := cfg.ProjectMap[p]; ok {
				out[p] = v
			} else {
				out[p] = cfg.MulticaProjectID
			}
		}
		out[req.GitlabPath] = multicaID
		cfg.ProjectMap = out
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	slog.Info("admin: project added", "project", req.GitlabPath, "multica_project_id", multicaID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "path": req.GitlabPath, "multica_project_id": multicaID,
	})
}

// handleRemoveProject 移除项目：allowlist + 映射 + 该项目的专属 token（若已知）一并删除。
// 已存在的历史卡不受影响；GitLab hook 保留但事件将因 allowlist 过滤被忽略。
func (h *adminHandler) handleRemoveProject(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project path required"})
		return
	}
	err := h.mutate(func(cfg *Config) error {
		if !cfg.AllowedProjects[path] {
			return fmt.Errorf("项目 %q 不在 allowlist 中", path)
		}
		delete(cfg.AllowedProjects, path)
		delete(cfg.ProjectMap, path)
		if token := h.st.SecretByPath[path]; token != "" {
			cfg.WebhookSecrets = slices.DeleteFunc(cfg.WebhookSecrets, func(s string) bool { return s == token })
			delete(h.st.SecretByPath, path)
		}
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err := h.persistState(); err != nil {
		slog.Error("admin: persist state failed", "error", err)
	}
	slog.Info("admin: project removed", "project", path)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path})
}

// ---------- hook 生命周期（3.3/3.4） ----------

// handleUpsertHook 创建/更新项目 hook：生成 token → GitLab API（存在则更新，缺失则创建）→ 记入内存 SECRETS。
func (h *adminHandler) handleUpsertHook(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project path required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cfg := h.cfgPtr.Load()
	if !cfg.AllowedProjects[path] {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("项目 %q 不在 allowlist 中", path)})
		return
	}
	if !h.gitlab.configured() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "SPECWIRE_GITLAB_TOKEN 未配置，无法编排 GitLab hook"})
		return
	}
	token, err := generateSecret()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	hooks, err := h.gitlab.ListHooks(ctx, path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "查询现有 hook 失败: " + err.Error()})
		return
	}
	hookID := h.gitlab.FindHookByURL(hooks, cfg.WebhookURL)
	if hookID > 0 {
		if err := h.gitlab.UpdateHook(ctx, path, hookID, cfg.WebhookURL, token, true, true); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "更新 GitLab hook 失败: " + err.Error()})
			return
		}
	} else {
		hookID, err = h.gitlab.CreateHook(ctx, path, cfg.WebhookURL, token, true, true)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "创建 GitLab hook 失败: " + err.Error()})
			return
		}
	}

	h.mu.Lock()
	cfg = h.cfgPtr.Load()
	if !slices.Contains(cfg.WebhookSecrets, token) {
		next := cloneConfig(cfg)
		next.WebhookSecrets = append(next.WebhookSecrets, token)
		h.cfgPtr.Store(next)
		cfg = next
	}
	h.st.SecretByPath[path] = token
	h.mu.Unlock()
	if err := h.persistState(); err != nil {
		slog.Error("admin: persist state failed", "error", err)
	}

	slog.Info("admin: hook upserted", "project", path, "hook_id", hookID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path, "hook_id": hookID})
}

// handleRotateToken 轮换项目 token：新 token → 更新 GitLab hook → 替换内存 SECRETS 中旧 token。
// 旧 token 通过 secretByPath 索引定位（页面创建的 hook 必有索引）；定位不到时旧 token 保留并告警。
func (h *adminHandler) handleRotateToken(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project path required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cfg := h.cfgPtr.Load()
	if !cfg.AllowedProjects[path] {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("项目 %q 不在 allowlist 中", path)})
		return
	}
	if !h.gitlab.configured() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "SPECWIRE_GITLAB_TOKEN 未配置，无法编排 GitLab hook"})
		return
	}
	token, err := generateSecret()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	hooks, err := h.gitlab.ListHooks(ctx, path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "查询现有 hook 失败: " + err.Error()})
		return
	}
	hookID := h.gitlab.FindHookByURL(hooks, cfg.WebhookURL)
	if hookID <= 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("项目 %q 还没有指向 %s 的 hook，请先创建", path, cfg.WebhookURL)})
		return
	}
	if err := h.gitlab.UpdateHook(ctx, path, hookID, cfg.WebhookURL, token, true, true); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "更新 GitLab hook 失败: " + err.Error()})
		return
	}

	warning := ""
	h.mu.Lock()
	old := h.st.SecretByPath[path]
	if old == "" && len(h.cfgPtr.Load().WebhookSecrets) == 1 {
		old = h.cfgPtr.Load().WebhookSecrets[0] // 单 token 旧配置：该 token 即本项目旧 token
	}
	next := cloneConfig(h.cfgPtr.Load())
	if old != "" {
		next.WebhookSecrets = slices.DeleteFunc(next.WebhookSecrets, func(s string) bool { return s == old })
	}
	if !slices.Contains(next.WebhookSecrets, token) {
		next.WebhookSecrets = append(next.WebhookSecrets, token)
	}
	h.cfgPtr.Store(next)
	h.st.SecretByPath[path] = token
	h.mu.Unlock()
	if old == "" {
		warning = "未能定位该项目的旧 token，旧 token 仍有效；建议移除项目后重新添加以重建专属 token"
	}
	if err := h.persistState(); err != nil {
		slog.Error("admin: persist state failed", "error", err)
	}

	slog.Info("admin: token rotated", "project", path, "hook_id", hookID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path, "hook_id": hookID, "warning": warning})
}

// ---------- 配置持久化（3.5） ----------

// handleApply 把当前运行时配置原子写回 env 文件（保留注释与其他键；SECRET 统一迁移为 SECRETS），
// 并置 restart_required 供页面提示 `docker compose up -d`。
func (h *adminHandler) handleApply(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	cfg := h.cfgPtr.Load()

	keys := sortedKeys(cfg.AllowedProjects)
	updates := map[string]string{
		"SPECWIRE_ALLOWED_PROJECTS":   strings.Join(keys, ","),
		"SPECWIRE_MULTICA_PROJECT_ID": cfg.MulticaProjectID,
		"SPECWIRE_WEBHOOK_SECRETS":    strings.Join(cfg.WebhookSecrets, ","),
	}
	var mapEntries []string
	for _, p := range keys {
		id, ok := cfg.ProjectMap[p]
		if !ok {
			id = cfg.MulticaProjectID
		}
		mapEntries = append(mapEntries, p+":"+id)
	}
	updates["SPECWIRE_PROJECT_MAP"] = strings.Join(mapEntries, ",")

	if err := rewriteEnvFile(h.envPath, updates, []string{"SPECWIRE_WEBHOOK_SECRET"}); err != nil {
		h.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "写回 env 文件失败: " + err.Error()})
		return
	}
	h.st.RestartRequired = true
	if err := h.persistState(); err != nil {
		slog.Error("admin: persist state failed", "error", err)
	}
	h.mu.Unlock()

	slog.Info("admin: config applied", "env_path", h.envPath, "projects", len(keys), "secrets", len(cfg.WebhookSecrets))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"restart_required": true,
		"message":          "配置已写入 " + h.envPath + "，执行 docker compose up -d 后生效",
	})
}

// envQuote 按需给值加双引号（值含逗号/引号/# 时）。
func envQuote(v string) string {
	if strings.ContainsAny(v, ",\"#") {
		return "\"" + strings.ReplaceAll(v, "\"", "\\\"") + "\""
	}
	return v
}

// rewriteEnvFile 重写 env 文件中的指定键（其余行与注释原样保留）：
//   - 命中 updates 的键原地替换（缺失则追加到文件末尾）；
//   - 命中 removes 的键整行注释掉（如旧 SPECWIRE_WEBHOOK_SECRET 迁移为 SECRETS）；
//   - 临时文件 + rename 原子写。
func rewriteEnvFile(path string, updates map[string]string, removes []string) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read env file: %w", err)
	}
	removeSet := map[string]bool{}
	for _, k := range removes {
		removeSet[k] = true
	}

	var out []string
	replaced := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			out = append(out, line)
			continue
		}
		k, _, ok := strings.Cut(trimmed, "=")
		if !ok {
			out = append(out, line)
			continue
		}
		k = strings.TrimSpace(k)
		if removeSet[k] {
			out = append(out, "# "+line+"  # admin apply: 已迁移到 SPECWIRE_WEBHOOK_SECRETS")
			continue
		}
		if v, ok := updates[k]; ok {
			out = append(out, k+"="+envQuote(v))
			replaced[k] = true
			continue
		}
		out = append(out, line)
	}
	updateKeys := mapsKeys(updates)
	sort.Strings(updateKeys)
	for _, k := range updateKeys {
		if !replaced[k] {
			out = append(out, k+"="+envQuote(updates[k]))
		}
	}
	return atomicWriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

func mapsKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---------- 运行时配置变更 ----------

// cloneConfig 深拷贝 Config 的可变字段（maps/slice），供 copy-on-write。
func cloneConfig(c *Config) *Config {
	cp := *c
	cp.AllowedProjects = map[string]bool{}
	for k, v := range c.AllowedProjects {
		cp.AllowedProjects[k] = v
	}
	cp.ProjectMap = map[string]string{}
	for k, v := range c.ProjectMap {
		cp.ProjectMap[k] = v
	}
	cp.WebhookSecrets = slices.Clone(c.WebhookSecrets)
	return &cp
}

// mutate 在锁内克隆当前配置、应用变更并原子替换指针；fn 返回错误则放弃变更。
func (h *adminHandler) mutate(fn func(*Config) error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	next := cloneConfig(h.cfgPtr.Load())
	if err := fn(next); err != nil {
		return err
	}
	h.cfgPtr.Store(next)
	return nil
}
