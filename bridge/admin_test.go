package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestAdmin 构造 admin handler：stub GitLab + fake multica + 临时 .env/admin-state。
func newTestAdmin(t *testing.T, stub *gitlabStub) (*adminHandler, *atomic.Pointer[Config], string) {
	t.Helper()
	dir := fakeMultica(t)
	t.Setenv("FAKE_MULTICA_ARGV_FILE", filepath.Join(dir, "argv"))
	t.Setenv("FAKE_MULTICA_STDIN_FILE", filepath.Join(dir, "stdin"))

	cfg := &Config{
		AllowedProjects:  map[string]bool{"specwire/specwire-poc": true},
		WebhookSecrets:   []string{tkSigningSecret},
		MulticaProfile:   "specwire-local",
		MulticaProjectID: tkProjectID,
		ProjectMap:       map[string]string{"specwire/specwire-poc": tkProjectID},
		GitLabToken:      "glpat-test",
		GitLabURL:        stub.srv.URL,
		WebhookURL:       "http://bridge/gitlab/specwire",
		RefFilter:        "refs/heads/main",
		CLITimeout:       5 * time.Second,
	}
	cfgPtr := &atomic.Pointer[Config]{}
	cfgPtr.Store(cfg)

	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	statePath := filepath.Join(tmp, "admin-state.json")

	h := newAdminHandler(cfgPtr, newGitlabClient(cfg), "", envPath, statePath)
	return h, cfgPtr, envPath
}

// adminRequest 向 admin handler 发请求；默认回环源地址（未配置 token 时允许）。
func adminRequest(t *testing.T, h *adminHandler, method, path, body, remote string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	if remote != "" {
		req.RemoteAddr = remote
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
}

const webdeck = "personal/webdeck"

// ---------- 3.1 config 快照 ----------

func TestAdminConfigSnapshot(t *testing.T) {
	stub := newGitlabStub(t)
	h, _, _ := newTestAdmin(t, stub)

	rec := adminRequest(t, h, http.MethodGet, "/admin/api/config", "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var snap configSnapshot
	decodeJSON(t, rec, &snap)
	if len(snap.AllowedProjects) != 1 || snap.AllowedProjects[0] != "specwire/specwire-poc" {
		t.Errorf("allowed_projects = %v", snap.AllowedProjects)
	}
	if snap.ProjectMap["specwire/specwire-poc"] != tkProjectID {
		t.Errorf("project_map = %v", snap.ProjectMap)
	}
	if snap.GitlabConfigured != true || snap.WebhookURL != "http://bridge/gitlab/specwire" {
		t.Errorf("gitlab = %+v", snap)
	}
	if snap.SecretCount != 1 {
		t.Errorf("secret_count = %d, want 1", snap.SecretCount)
	}
	if len(snap.MulticaProjects) != 2 {
		t.Errorf("multica_projects = %+v", snap.MulticaProjects)
	}
	if len(snap.Projects) != 1 {
		t.Fatalf("projects = %+v", snap.Projects)
	}
	p := snap.Projects[0]
	if p.Path != "specwire/specwire-poc" || p.MulticaProjectID != tkProjectID {
		t.Errorf("project = %+v", p)
	}
	if !p.HasSecret || p.SecretSource != "shared" {
		t.Errorf("has_secret=%v secret_source=%q, want true/shared (legacy shared secret)", p.HasSecret, p.SecretSource)
	}
	if p.Hook == nil || p.Hook.Exists {
		t.Errorf("hook = %+v, want exists=false (no hooks in stub)", p.Hook)
	}
	// 快照不得含 token 明文
	if strings.Contains(rec.Body.String(), tkSecret) || strings.Contains(rec.Body.String(), "whsec_") {
		t.Errorf("snapshot leaks secret: %s", rec.Body.String())
	}
}

func TestAdminConfigSnapshotHookStatus(t *testing.T) {
	stub := newGitlabStub(t)
	stub.hooks[webdeck] = []gitlabHook{{ID: 5, URL: "http://bridge/gitlab/specwire", PushEvents: true, IssuesEvents: true}}
	stub.hooks["specwire/specwire-poc"] = []gitlabHook{{ID: 9, URL: "http://other"}}
	h, cfgPtr, _ := newTestAdmin(t, stub)
	cfg := cfgPtr.Load()
	cfg.AllowedProjects[webdeck] = true
	cfg.ProjectMap[webdeck] = "59f6006a-4b60-4219-b736-e7c3d4befd19"

	rec := adminRequest(t, h, http.MethodGet, "/admin/api/config", "", "127.0.0.1:1", nil)
	var snap configSnapshot
	decodeJSON(t, rec, &snap)
	byPath := map[string]projectSnapshot{}
	for _, p := range snap.Projects {
		byPath[p.Path] = p
	}
	if w := byPath[webdeck]; w.Hook == nil || !w.Hook.Exists || w.Hook.ID != 5 {
		t.Errorf("webdeck hook = %+v, want exists id=5", w.Hook)
	}
	if s := byPath["specwire/specwire-poc"]; s.Hook == nil || s.Hook.Exists {
		t.Errorf("specwire hook = %+v, want exists=false (url mismatch)", s.Hook)
	}
}

// ---------- 3.6 admin 安全 ----------

func TestAdminAuthLoopbackOnly(t *testing.T) {
	stub := newGitlabStub(t)
	h, _, _ := newTestAdmin(t, stub)

	// 回环放行
	rec := adminRequest(t, h, http.MethodGet, "/admin/api/config", "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("loopback: status = %d, want 200", rec.Code)
	}
	// 非回环拒绝
	rec = adminRequest(t, h, http.MethodGet, "/admin/api/config", "", "10.0.0.5:1234", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("non-loopback: status = %d, want 401", rec.Code)
	}
	// 静态页面无需鉴权
	rec = adminRequest(t, h, http.MethodGet, "/admin/", "", "10.0.0.5:1234", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("page from non-loopback: status = %d, want 200", rec.Code)
	}
}

func TestAdminAuthTokenRequired(t *testing.T) {
	stub := newGitlabStub(t)
	h, _, _ := newTestAdmin(t, stub)
	h.adminToken = "secret-admin"

	// 无 token → 401（即使回环）
	rec := adminRequest(t, h, http.MethodGet, "/admin/api/config", "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", rec.Code)
	}
	// 错误 token → 401
	rec = adminRequest(t, h, http.MethodGet, "/admin/api/config", "", "127.0.0.1:1", map[string]string{"X-Admin-Token": "wrong"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", rec.Code)
	}
	// 正确 token → 200
	rec = adminRequest(t, h, http.MethodGet, "/admin/api/config", "", "127.0.0.1:1", map[string]string{"X-Admin-Token": "secret-admin"})
	if rec.Code != http.StatusOK {
		t.Errorf("right token: status = %d, want 200", rec.Code)
	}
}

// ---------- 3.2 项目增删 ----------

func TestAdminAddProject(t *testing.T) {
	stub := newGitlabStub(t)
	h, cfgPtr, _ := newTestAdmin(t, stub)

	body := `{"gitlab_path":"personal/webdeck","multica_project":"WebDeck"}`
	rec := adminRequest(t, h, http.MethodPost, "/admin/api/projects", body, "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var r map[string]any
	decodeJSON(t, rec, &r)
	if r["multica_project_id"] != "59f6006a-4b60-4219-b736-e7c3d4befd19" {
		t.Errorf("multica_project_id = %v", r["multica_project_id"])
	}

	cfg := cfgPtr.Load()
	if !cfg.AllowedProjects[webdeck] {
		t.Error("webdeck not in allowlist")
	}
	if cfg.ProjectMap[webdeck] != "59f6006a-4b60-4219-b736-e7c3d4befd19" {
		t.Errorf("webdeck map = %q", cfg.ProjectMap[webdeck])
	}
	// 回填：原有项目保持映射
	if cfg.ProjectMap["specwire/specwire-poc"] != tkProjectID {
		t.Errorf("specwire map = %q, want %q (backfill)", cfg.ProjectMap["specwire/specwire-poc"], tkProjectID)
	}
}

func TestAdminAddProjectRejects(t *testing.T) {
	stub := newGitlabStub(t)
	h, _, _ := newTestAdmin(t, stub)
	stub.missing["personal/nope"] = true

	cases := []struct {
		name string
		body string
	}{
		{"gitlab missing", `{"gitlab_path":"personal/nope","multica_project":"WebDeck"}`},
		{"multica missing", `{"gitlab_path":"personal/webdeck","multica_project":"不存在的项目"}`},
		{"empty fields", `{"gitlab_path":"","multica_project":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := adminRequest(t, h, http.MethodPost, "/admin/api/projects", tc.body, "127.0.0.1:1", nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAdminRemoveProject(t *testing.T) {
	stub := newGitlabStub(t)
	h, cfgPtr, _ := newTestAdmin(t, stub)
	cfg := cfgPtr.Load()
	cfg.AllowedProjects[webdeck] = true
	cfg.ProjectMap[webdeck] = "59f6006a-4b60-4219-b736-e7c3d4befd19"
	cfg.WebhookSecrets = append(cfg.WebhookSecrets, "whsec_webdeck-token")
	h.mu.Lock()
	h.st.SecretByPath[webdeck] = "whsec_webdeck-token"
	h.mu.Unlock()

	rec := adminRequest(t, h, http.MethodDelete, "/admin/api/projects/"+webdeck, "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cfg = cfgPtr.Load()
	if cfg.AllowedProjects[webdeck] {
		t.Error("webdeck still in allowlist")
	}
	if _, ok := cfg.ProjectMap[webdeck]; ok {
		t.Error("webdeck still in project map")
	}
	for _, s := range cfg.WebhookSecrets {
		if s == "whsec_webdeck-token" {
			t.Error("removed project token still in secrets")
		}
	}
	// 移除不存在的项目 → 404
	rec = adminRequest(t, h, http.MethodDelete, "/admin/api/projects/"+webdeck, "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("remove again: status = %d, want 404", rec.Code)
	}
}

// ---------- 3.3/3.4 hook 生命周期 ----------

func TestAdminUpsertHookCreates(t *testing.T) {
	stub := newGitlabStub(t)
	h, cfgPtr, _ := newTestAdmin(t, stub)
	cfg := cfgPtr.Load()
	cfg.AllowedProjects[webdeck] = true
	cfg.ProjectMap[webdeck] = "59f6006a-4b60-4219-b736-e7c3d4befd19"

	rec := adminRequest(t, h, http.MethodPost, "/admin/api/hooks/"+webdeck, "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var r map[string]any
	decodeJSON(t, rec, &r)
	if r["hook_id"] != float64(100) {
		t.Errorf("hook_id = %v, want 100", r["hook_id"])
	}
	// GitLab 收到 token（whsec_ 前缀）
	req := stub.lastReq(t)
	if req.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.Method)
	}
	token := req.Form.Get("token")
	if !strings.HasPrefix(token, "whsec_") || len(token) != len("whsec_")+44 {
		t.Errorf("token = %q, want whsec_ + 32-byte base64", token)
	}
	// 运行时 secrets 记录
	secrets := cfgPtr.Load().WebhookSecrets
	if !strings.Contains(strings.Join(secrets, ","), token) {
		t.Errorf("secrets %v missing new token", secrets)
	}
	// 状态索引 + 持久化
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.st.SecretByPath[webdeck] != token {
		t.Errorf("secret_by_path = %v", h.st.SecretByPath)
	}
}

func TestAdminUpsertHookUpdatesExisting(t *testing.T) {
	stub := newGitlabStub(t)
	stub.hooks[webdeck] = []gitlabHook{{ID: 7, URL: "http://bridge/gitlab/specwire"}}
	h, cfgPtr, _ := newTestAdmin(t, stub)
	cfg := cfgPtr.Load()
	cfg.AllowedProjects[webdeck] = true
	cfg.ProjectMap[webdeck] = "59f6006a-4b60-4219-b736-e7c3d4befd19"

	rec := adminRequest(t, h, http.MethodPost, "/admin/api/hooks/"+webdeck, "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var r map[string]any
	decodeJSON(t, rec, &r)
	if r["hook_id"] != float64(7) {
		t.Errorf("hook_id = %v, want 7 (update existing)", r["hook_id"])
	}
	req := stub.lastReq(t)
	if req.Method != http.MethodPut || req.Path != "/api/v4/projects/personal/webdeck/hooks/7" {
		t.Errorf("req = %s %s, want PUT .../hooks/7", req.Method, req.Path)
	}
	if stub.reqCount() != 2 { // GET hooks + PUT
		t.Errorf("request count = %d, want 2", stub.reqCount())
	}
}

func TestAdminRotateToken(t *testing.T) {
	stub := newGitlabStub(t)
	h, cfgPtr, _ := newTestAdmin(t, stub)
	cfg := cfgPtr.Load()
	cfg.AllowedProjects[webdeck] = true
	cfg.ProjectMap[webdeck] = "59f6006a-4b60-4219-b736-e7c3d4befd19"

	// 先创建 hook（建立旧 token 索引）
	rec := adminRequest(t, h, http.MethodPost, "/admin/api/hooks/"+webdeck, "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("create hook: status = %d", rec.Code)
	}
	oldToken := cfgPtr.Load().WebhookSecrets[len(cfgPtr.Load().WebhookSecrets)-1]

	// 轮换
	rec = adminRequest(t, h, http.MethodPost, "/admin/api/hooks/"+webdeck+"/rotate", "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: status = %d, body: %s", rec.Code, rec.Body.String())
	}
	req := stub.lastReq(t)
	if req.Method != http.MethodPut {
		t.Fatalf("rotate method = %s, want PUT", req.Method)
	}
	newToken := req.Form.Get("token")
	if newToken == "" || newToken == oldToken {
		t.Errorf("new token = %q, old = %q", newToken, oldToken)
	}

	secrets := cfgPtr.Load().WebhookSecrets
	for _, s := range secrets {
		if s == oldToken {
			t.Error("old token still in runtime secrets after rotate")
		}
	}
	if !strings.Contains(strings.Join(secrets, ","), newToken) {
		t.Error("new token missing from runtime secrets")
	}
	// 状态索引指向新 token
	h.mu.RLock()
	if h.st.SecretByPath[webdeck] != newToken {
		t.Errorf("secret_by_path = %q, want %q", h.st.SecretByPath[webdeck], newToken)
	}
	h.mu.RUnlock()
}

// 轮换时无 hook → 409。
func TestAdminRotateTokenNoHook(t *testing.T) {
	stub := newGitlabStub(t)
	h, cfgPtr, _ := newTestAdmin(t, stub)
	cfg := cfgPtr.Load()
	cfg.AllowedProjects[webdeck] = true
	cfg.ProjectMap[webdeck] = "59f6006a-4b60-4219-b736-e7c3d4befd19"

	rec := adminRequest(t, h, http.MethodPost, "/admin/api/hooks/"+webdeck+"/rotate", "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

// ---------- 3.5 apply 写回 ----------

func TestAdminApplyRewritesEnv(t *testing.T) {
	stub := newGitlabStub(t)
	h, cfgPtr, envPath := newTestAdmin(t, stub)

	// 预置 .env：注释 + 旧 SECRET + 无关键
	seed := "# SpecWire Bridge 配置\n\nSPECWIRE_WEBHOOK_SECRET=" + tkSigningSecret + "\n" +
		"SPECWIRE_MULTICA_PROJECT_ID=" + tkProjectID + "\n" +
		"SPECWIRE_LOG_LEVEL=info\n"
	if err := os.WriteFile(envPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := cfgPtr.Load()
	cfg.AllowedProjects[webdeck] = true
	cfg.ProjectMap[webdeck] = "59f6006a-4b60-4219-b736-e7c3d4befd19"
	cfg.WebhookSecrets = []string{tkSigningSecret, "whsec_new-token"}

	rec := adminRequest(t, h, http.MethodPost, "/admin/api/apply", "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var r map[string]any
	decodeJSON(t, rec, &r)
	if r["restart_required"] != true {
		t.Errorf("restart_required = %v", r["restart_required"])
	}

	b, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if !strings.Contains(content, "# SpecWire Bridge 配置") {
		t.Error("comment lost")
	}
	if !strings.Contains(content, "SPECWIRE_WEBHOOK_SECRETS=\"whsec_") {
		t.Errorf("SECRETS line missing/quoted: %s", content)
	}
	if strings.Contains(content, "\nSPECWIRE_WEBHOOK_SECRET=") {
		t.Errorf("legacy SECRET line not migrated: %s", content)
	}
	if !strings.Contains(content, "SPECWIRE_ALLOWED_PROJECTS=") ||
		!strings.Contains(content, "specwire/specwire-poc") ||
		!strings.Contains(content, "personal/webdeck") {
		t.Errorf("allowlist not rewritten: %s", content)
	}
	if !strings.Contains(content, "personal/webdeck:59f6006a-4b60-4219-b736-e7c3d4befd19") {
		t.Errorf("project map not rewritten: %s", content)
	}
	if !strings.Contains(content, "SPECWIRE_LOG_LEVEL=info") {
		t.Error("unrelated key lost")
	}
	// 无残留临时文件
	entries, _ := os.ReadDir(filepath.Dir(envPath))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	// apply 后快照 restart_required=true
	rec = adminRequest(t, h, http.MethodGet, "/admin/api/config", "", "127.0.0.1:1", nil)
	var snap configSnapshot
	decodeJSON(t, rec, &snap)
	if !snap.RestartRequired {
		t.Error("restart_required not set in snapshot")
	}
}

func TestAdminApplyAppendsMissingKeys(t *testing.T) {
	stub := newGitlabStub(t)
	h, _, envPath := newTestAdmin(t, stub)
	if err := os.WriteFile(envPath, []byte("SPECWIRE_LOG_LEVEL=debug\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := adminRequest(t, h, http.MethodPost, "/admin/api/apply", "", "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	b, _ := os.ReadFile(envPath)
	for _, key := range []string{"SPECWIRE_WEBHOOK_SECRETS", "SPECWIRE_ALLOWED_PROJECTS", "SPECWIRE_PROJECT_MAP", "SPECWIRE_MULTICA_PROJECT_ID"} {
		if !strings.Contains(string(b), key+"=") {
			t.Errorf("missing %s: %s", key, b)
		}
	}
}

// ---------- 4.1 静态页面 ----------

func TestAdminPageServed(t *testing.T) {
	stub := newGitlabStub(t)
	h, _, _ := newTestAdmin(t, stub)
	rec := adminRequest(t, h, http.MethodGet, "/admin/", "", "10.0.0.5:1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "SpecWire Bridge 管理") {
		t.Error("page body missing title")
	}
	if !strings.Contains(rec.Body.String(), "/admin/api/config") {
		t.Error("page body missing api reference")
	}
}
