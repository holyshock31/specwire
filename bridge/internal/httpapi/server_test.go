package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"specwire/bridge/internal/auth"
	"specwire/bridge/internal/controlplane"
	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/store"
)

type testProbe struct{}

func (testProbe) ProbeGitLab(context.Context, domain.GitLabInstance) ([]domain.CapabilityResult, error) {
	return nil, nil
}
func (testProbe) ProbeMultica(context.Context, domain.MulticaInstance) ([]domain.CapabilityResult, error) {
	return nil, nil
}

func TestAuthAndEndpointAPIUsesSessionCSRFAndWorkspaceAuthorization(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/httpapi.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	local, err := auth.NewLocalProvider(s)
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := controlplane.NewEndpointService(s, testProbe{})
	if err != nil {
		t.Fatal(err)
	}
	api, err := NewServer(local, s, s, endpoints)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api)
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar

	status, bootstrapBody := jsonRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/bootstrap", `{"email":"admin@example.com","password":"correct horse battery staple","displayName":"Admin"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("bootstrap status = %d", status)
	}
	var bootstrap struct {
		Workspace domain.Workspace `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(bootstrapBody), &bootstrap); err != nil {
		t.Fatal(err)
	}
	loginStatus, loginBody := jsonRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", `{"email":"admin@example.com","password":"correct horse battery staple"}`, nil)
	if loginStatus != http.StatusOK {
		t.Fatalf("login status = %d body=%s", loginStatus, loginBody)
	}
	var login struct {
		Account domain.Account `json:"account"`
		CSRF    string         `json:"csrf_token"`
	}
	if err := json.Unmarshal([]byte(loginBody), &login); err != nil {
		t.Fatal(err)
	}
	if login.CSRF == "" {
		t.Fatal("login did not return CSRF token")
	}
	if cookie := cookieValue(jar.Cookies(mustURL(t, server.URL)), csrfCookie); cookie == "" {
		t.Fatal("login did not set CSRF cookie")
	}

	path := server.URL + "/api/v1/workspaces/" + string(bootstrap.Workspace.ID) + "/gitlab-instances"
	status, _ = jsonRequest(t, client, http.MethodPost, path, `{"name":"GitLab","base_url":"https://gitlab.example.test"}`, map[string]string{csrfHeader: login.CSRF})
	if status != http.StatusCreated {
		t.Fatalf("create endpoint status = %d", status)
	}
	status, body := jsonRequest(t, client, http.MethodGet, path, "", nil)
	if status != http.StatusOK || !strings.Contains(body, "gitlab.example.test") {
		t.Fatalf("list endpoint = %d %s", status, body)
	}
	status, _ = jsonRequest(t, client, http.MethodPost, path, `{"name":"No CSRF","base_url":"https://gitlab.example.test"}`, nil)
	if status != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", status)
	}
	jar.SetCookies(mustURL(t, server.URL), []*http.Cookie{{Name: csrfCookie, Value: "", Path: "/", MaxAge: -1}})
	reloadedStatus, reloadedBody := jsonRequest(t, client, http.MethodGet, server.URL+"/api/v1/auth/me", "", nil)
	if reloadedStatus != http.StatusOK {
		t.Fatalf("auth/me after page reload = %d %s", reloadedStatus, reloadedBody)
	}
	var reloaded struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal([]byte(reloadedBody), &reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.CSRF == "" || cookieValue(jar.Cookies(mustURL(t, server.URL)), csrfCookie) != reloaded.CSRF {
		t.Fatalf("auth/me did not restore CSRF cookie: body=%q cookie=%q", reloaded.CSRF, cookieValue(jar.Cookies(mustURL(t, server.URL)), csrfCookie))
	}
	reloadedCreateStatus, _ := jsonRequest(t, client, http.MethodPost, path, `{"name":"After Reload","base_url":"https://gitlab.reload.test"}`, map[string]string{csrfHeader: reloaded.CSRF})
	if reloadedCreateStatus != http.StatusCreated {
		t.Fatalf("create endpoint after page reload status = %d", reloadedCreateStatus)
	}
	status, _ = jsonRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/logout", "{}", map[string]string{csrfHeader: login.CSRF})
	if status != http.StatusForbidden {
		t.Fatalf("logout with stale CSRF status = %d", status)
	}
	status, _ = jsonRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/logout", "{}", map[string]string{csrfHeader: reloaded.CSRF})
	if status != http.StatusNoContent {
		t.Fatalf("logout status = %d", status)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func cookieValue(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func jsonRequest(t *testing.T, client *http.Client, method, endpoint, body string, headers map[string]string) (int, string) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(responseBody)
}
