package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// glReq 记录一次 stub GitLab API 请求。
type glReq struct {
	Method   string
	Path     string // 解码后的路径（r.URL.Path）
	RawPath  string // 原始转义路径（r.URL.EscapedPath）
	RawQuery string
	Form     url.Values
}

// gitlabStub 是测试用 GitLab API stub：记录请求并按脚本返回。
type gitlabStub struct {
	srv *httptest.Server
	mu  sync.Mutex

	reqs       []glReq
	hooks      map[string][]gitlabHook // project path → hooks（测试可预置）
	missing    map[string]bool         // 项目不存在
	nextHookID int
}

func newGitlabStub(t *testing.T) *gitlabStub {
	s := &gitlabStub{hooks: map[string][]gitlabHook{}, missing: map[string]bool{}, nextHookID: 100}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *gitlabStub) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := glReq{Method: r.Method, Path: r.URL.Path, RawPath: r.URL.EscapedPath(), RawQuery: r.URL.RawQuery}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		if err := r.ParseForm(); err == nil {
			rec.Form = r.PostForm
		}
	}
	s.reqs = append(s.reqs, rec)

	rest := strings.TrimPrefix(r.URL.Path, "/api/v4/projects/")
	// project path 含 "/"，用 "/hooks" 定位 hook 子路径
	if hooksIdx := strings.Index(rest, "/hooks"); hooksIdx >= 0 {
		proj := rest[:hooksIdx]
		tail := rest[hooksIdx+len("/hooks"):]
		switch {
		case r.Method == http.MethodGet && tail == "":
			list := s.hooks[proj]
			if list == nil {
				list = []gitlabHook{}
			}
			writeJSON(w, http.StatusOK, list)
			return
		case r.Method == http.MethodPost && tail == "":
			h := gitlabHook{ID: s.nextHookID, URL: rec.Form.Get("url"), PushEvents: true, IssuesEvents: true}
			s.nextHookID++
			s.hooks[proj] = append(s.hooks[proj], h)
			writeJSON(w, http.StatusCreated, h)
			return
		case r.Method == http.MethodPut && strings.HasPrefix(tail, "/"):
			hookID := atoiOrZero(strings.TrimPrefix(tail, "/"))
			for i := range s.hooks[proj] {
				if s.hooks[proj][i].ID == hookID {
					s.hooks[proj][i].URL = rec.Form.Get("url")
					s.hooks[proj][i].PushEvents = true
					s.hooks[proj][i].IssuesEvents = true
					writeJSON(w, http.StatusOK, s.hooks[proj][i])
					return
				}
			}
			http.Error(w, "hook not found", http.StatusNotFound)
			return
		case r.Method == http.MethodDelete && strings.HasPrefix(tail, "/"):
			hookID := atoiOrZero(strings.TrimPrefix(tail, "/"))
			list := s.hooks[proj]
			for i := range list {
				if list[i].ID == hookID {
					s.hooks[proj] = append(list[:i], list[i+1:]...)
					break
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
		return
	}

	// 项目存在性 / Issue 关闭
	switch {
	case r.Method == http.MethodGet:
		if s.missing[rest] {
			http.Error(w, "404 Project Not Found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": 1, "path_with_namespace": rest})
	case r.Method == http.MethodPut && strings.Contains(rest, "/issues/"):
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func atoiOrZero(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func (s *gitlabStub) lastReq(t *testing.T) glReq {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reqs) == 0 {
		t.Fatal("no gitlab requests recorded")
	}
	return s.reqs[len(s.reqs)-1]
}

func (s *gitlabStub) reqCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

func testGitlabClient(stub *gitlabStub) *gitlabClient {
	return newGitlabClient(&Config{GitLabToken: "glpat-test", GitLabURL: stub.srv.URL})
}

// ---------- gitlabClient ----------

func TestGitlabCreateHook(t *testing.T) {
	stub := newGitlabStub(t)
	c := testGitlabClient(stub)
	ctx := context.Background()

	id, err := c.CreateHook(ctx, "personal/webdeck", "http://bridge/gitlab/specwire", "whsec_abc", true, true)
	if err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	if id != 100 {
		t.Errorf("hook id = %d, want 100", id)
	}
	req := stub.lastReq(t)
	if req.Method != http.MethodPost || req.Path != "/api/v4/projects/personal/webdeck/hooks" {
		t.Errorf("req = %s %s", req.Method, req.Path)
	}
	// GitLab 要求项目路径 %2F 编码（未编码斜杠 → 404）
	if !strings.Contains(req.RawPath, "personal%2Fwebdeck") {
		t.Errorf("raw path = %s, want %%2F-encoded project path", req.RawPath)
	}
	if got := req.Form.Get("url"); got != "http://bridge/gitlab/specwire" {
		t.Errorf("url = %q", got)
	}
	if got := req.Form.Get("token"); got != "whsec_abc" {
		t.Errorf("token = %q", got)
	}
	if req.Form.Get("push_events") != "true" || req.Form.Get("issues_events") != "true" {
		t.Errorf("events = push:%q issues:%q", req.Form.Get("push_events"), req.Form.Get("issues_events"))
	}
}

func TestGitlabUpdateHook(t *testing.T) {
	stub := newGitlabStub(t)
	stub.hooks["personal/webdeck"] = []gitlabHook{{ID: 5, URL: "http://old"}}
	c := testGitlabClient(stub)
	ctx := context.Background()

	if err := c.UpdateHook(ctx, "personal/webdeck", 5, "http://bridge/gitlab/specwire", "whsec_new", true, true); err != nil {
		t.Fatalf("UpdateHook: %v", err)
	}
	req := stub.lastReq(t)
	if req.Method != http.MethodPut || req.Path != "/api/v4/projects/personal/webdeck/hooks/5" {
		t.Errorf("req = %s %s", req.Method, req.Path)
	}
	if got := req.Form.Get("token"); got != "whsec_new" {
		t.Errorf("token = %q, want whsec_new", got)
	}
}

func TestGitlabListHooks(t *testing.T) {
	stub := newGitlabStub(t)
	stub.hooks["a/b"] = []gitlabHook{{ID: 1, URL: "http://x", PushEvents: true, IssuesEvents: false}}
	c := testGitlabClient(stub)

	hooks, err := c.ListHooks(context.Background(), "a/b")
	if err != nil {
		t.Fatalf("ListHooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].ID != 1 || hooks[0].PushEvents != true || hooks[0].IssuesEvents != false {
		t.Errorf("hooks = %+v", hooks)
	}
	if id := c.FindHookByURL(hooks, "http://x"); id != 1 {
		t.Errorf("FindHookByURL = %d, want 1", id)
	}
	if id := c.FindHookByURL(hooks, "http://nope"); id != -1 {
		t.Errorf("FindHookByURL missing = %d, want -1", id)
	}
}

func TestGitlabDeleteHook(t *testing.T) {
	stub := newGitlabStub(t)
	stub.hooks["a/b"] = []gitlabHook{{ID: 7, URL: "http://x"}}
	c := testGitlabClient(stub)

	if err := c.DeleteHook(context.Background(), "a/b", 7); err != nil {
		t.Fatalf("DeleteHook: %v", err)
	}
	req := stub.lastReq(t)
	if req.Method != http.MethodDelete || req.Path != "/api/v4/projects/a/b/hooks/7" {
		t.Errorf("req = %s %s", req.Method, req.Path)
	}
	// 删除不存在的 hook（404）→ 幂等成功
	if err := c.DeleteHook(context.Background(), "a/b", 999); err != nil {
		t.Errorf("DeleteHook missing: %v", err)
	}
}

func TestGitlabProjectExists(t *testing.T) {
	stub := newGitlabStub(t)
	c := testGitlabClient(stub)
	ctx := context.Background()

	ok, err := c.ProjectExists(ctx, "specwire/specwire-poc")
	if err != nil || !ok {
		t.Fatalf("ProjectExists(existing) = %v, %v; want true, nil", ok, err)
	}
	stub.missing["personal/nope"] = true
	ok, err = c.ProjectExists(ctx, "personal/nope")
	if err != nil || ok {
		t.Fatalf("ProjectExists(missing) = %v, %v; want false, nil", ok, err)
	}
}

func TestGitlabCloseIssue(t *testing.T) {
	stub := newGitlabStub(t)
	c := testGitlabClient(stub)
	if err := c.CloseIssue(context.Background(), "specwire/specwire-poc", 42); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	req := stub.lastReq(t)
	if req.Method != http.MethodPut || !strings.Contains(req.Path, "/issues/42") || !strings.Contains(req.RawQuery, "state_event=close") {
		t.Errorf("req = %s %s?%s", req.Method, req.Path, req.RawQuery)
	}

	// 未配置 token → ErrGitLabNotConfigured
	noToken := newGitlabClient(&Config{GitLabURL: stub.srv.URL})
	if err := noToken.CloseIssue(context.Background(), "x/y", 1); err == nil {
		t.Fatal("CloseIssue without token: want error, got nil")
	}
}

// ---------- token 生成（2.2） ----------

func TestGenerateSecret(t *testing.T) {
	a, err := generateSecret()
	if err != nil {
		t.Fatalf("generateSecret: %v", err)
	}
	b, err := generateSecret()
	if err != nil {
		t.Fatalf("generateSecret: %v", err)
	}
	if !strings.HasPrefix(a, "whsec_") {
		t.Errorf("secret %q: want whsec_ prefix", a)
	}
	if a == b {
		t.Errorf("two secrets identical: %q", a)
	}
	// 32 字节 → base64 长度 44
	payload := strings.TrimPrefix(a, "whsec_")
	if len(payload) != 44 {
		t.Errorf("payload length = %d, want 44 (32 bytes base64)", len(payload))
	}
	if _, err := parseSecrets(a); err != nil {
		t.Errorf("parseSecrets(generated) = %v", err)
	}
	var raw []byte
	if err := json.Unmarshal([]byte(`"`+payload+`"`), &raw); err != nil {
		t.Errorf("payload not valid base64 json: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("decoded payload = %d bytes, want 32", len(raw))
	}
}
