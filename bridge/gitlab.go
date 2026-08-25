// SpecWire Bridge → GitLab API 客户端：
//   - 归档时关闭关联发布 Issue（v2 发布模型）；
//   - 管理 API 的项目校验与 webhook 生命周期编排（CreateHook/UpdateHook/ListHooks/DeleteHook，bridge-admin-ui）。
//
// 统一走 PRIVATE-TOKEN header + 30s 超时 HTTP 客户端模式。
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrGitLabNotConfigured 表示未配置 GitLab API token（归档降级 / admin 返回提示）。
var ErrGitLabNotConfigured = fmt.Errorf("gitlab api not configured")

// gitlabClient 封装 GitLab API 调用。token/baseURL 在构造时固定（admin 不修改这两项）。
type gitlabClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// newGitlabClient 从配置构造客户端。
func newGitlabClient(cfg *Config) *gitlabClient {
	return &gitlabClient{
		baseURL: strings.TrimSuffix(cfg.GitLabURL, "/"),
		token:   cfg.GitLabToken,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// configured 返回是否配置了 API token。
func (c *gitlabClient) configured() bool { return c.token != "" }

// gitlabAPIError 是 GitLab API 非 2xx 响应的错误（含状态码，便于按 404 分支）。
type gitlabAPIError struct {
	Method string
	Path   string
	Status int
	Body   string
}

func (e *gitlabAPIError) Error() string {
	return fmt.Sprintf("gitlab api %s %s: unexpected status %d: %s", e.Method, e.Path, e.Status, truncateStr(e.Body, 300))
}

// do 发起一次 API 请求；2xx 返回响应体，其他状态返回 *gitlabAPIError。
func (c *gitlabClient) do(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("build gitlab request: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab api call %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read gitlab response: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return b, nil
	}
	return nil, &gitlabAPIError{Method: method, Path: path, Status: resp.StatusCode, Body: string(b)}
}

// escapeProject 转义 project path（如 personal/webdeck）用于 URL 路径。
// GitLab API 要求整段 %2F 编码（未编码的斜杠会被当成 URL 层级 → 404）。
func escapeProject(path string) string {
	return url.PathEscape(path)
}

// ProjectExists 检查 GitLab 项目是否存在（GET /projects/{path}，404 → 不存在）。
func (c *gitlabClient) ProjectExists(ctx context.Context, projectPath string) (bool, error) {
	_, err := c.do(ctx, http.MethodGet, "/api/v4/projects/"+escapeProject(projectPath), nil)
	if err != nil {
		var apiErr *gitlabAPIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// gitlabHook 是 GitLab project hook 的最小字段集。
type gitlabHook struct {
	ID           int    `json:"id"`
	URL          string `json:"url"`
	PushEvents   bool   `json:"push_events"`
	IssuesEvents bool   `json:"issues_events"`
}

// ListHooks 列出项目的全部 webhook。
func (c *gitlabClient) ListHooks(ctx context.Context, projectPath string) ([]gitlabHook, error) {
	b, err := c.do(ctx, http.MethodGet, "/api/v4/projects/"+escapeProject(projectPath)+"/hooks", nil)
	if err != nil {
		return nil, err
	}
	var out []gitlabHook
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parse gitlab hooks: %w", err)
	}
	return out, nil
}

// hookForm 构造 hook 参数（push/issues 事件 + signing token）。
func hookForm(hookURL, token string, pushEvents, issuesEvents bool) url.Values {
	f := url.Values{}
	f.Set("url", hookURL)
	f.Set("push_events", strconv.FormatBool(pushEvents))
	f.Set("issues_events", strconv.FormatBool(issuesEvents))
	if token != "" {
		f.Set("token", token)
	}
	return f
}

// CreateHook 创建项目 webhook（push+issues 事件、项目唯一 signing token），返回 hook ID。
func (c *gitlabClient) CreateHook(ctx context.Context, projectPath, hookURL, token string, pushEvents, issuesEvents bool) (int, error) {
	b, err := c.do(ctx, http.MethodPost, "/api/v4/projects/"+escapeProject(projectPath)+"/hooks",
		hookForm(hookURL, token, pushEvents, issuesEvents))
	if err != nil {
		return 0, err
	}
	var h gitlabHook
	if err := json.Unmarshal(b, &h); err != nil {
		return 0, fmt.Errorf("parse created gitlab hook: %w", err)
	}
	return h.ID, nil
}

// UpdateHook 更新项目 webhook（token 轮换/参数修正）。
func (c *gitlabClient) UpdateHook(ctx context.Context, projectPath string, hookID int, hookURL, token string, pushEvents, issuesEvents bool) error {
	path := fmt.Sprintf("/api/v4/projects/%s/hooks/%d", escapeProject(projectPath), hookID)
	_, err := c.do(ctx, http.MethodPut, path, hookForm(hookURL, token, pushEvents, issuesEvents))
	return err
}

// DeleteHook 删除项目 webhook（404 视为已删除，幂等）。
func (c *gitlabClient) DeleteHook(ctx context.Context, projectPath string, hookID int) error {
	path := fmt.Sprintf("/api/v4/projects/%s/hooks/%d", escapeProject(projectPath), hookID)
	_, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		var apiErr *gitlabAPIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil
		}
		return err
	}
	return nil
}

// FindHookByURL 在列表中查找指向指定 URL 的 hook；不存在返回 -1。
func (c *gitlabClient) FindHookByURL(hooks []gitlabHook, hookURL string) int {
	for _, h := range hooks {
		if h.URL == hookURL {
			return h.ID
		}
	}
	return -1
}

// CloseIssue 关闭指定项目的 GitLab Issue（PUT ...?state_event=close）。
// token 未配置时返回 ErrGitLabNotConfigured（调用方降级跳过）。
func (c *gitlabClient) CloseIssue(ctx context.Context, projectPath string, iid int) error {
	if !c.configured() {
		return fmt.Errorf("%w: SPECWIRE_GITLAB_TOKEN not set", ErrGitLabNotConfigured)
	}
	path := fmt.Sprintf("/api/v4/projects/%s/issues/%d?state_event=close", escapeProject(projectPath), iid)
	_, err := c.do(ctx, http.MethodPut, path, nil)
	return err
}

// generateSecret 生成项目专属 signing token：crypto/rand 32 字节 → base64，前缀 whsec_。
// 与 GitLab Standard Webhooks 校验格式一致（whsec_ + base64 key）。
func generateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(buf), nil
}
