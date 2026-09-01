package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"specwire/bridge/internal/auth"
	"specwire/bridge/internal/controlplane"
	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
	gitlabprovider "specwire/bridge/internal/provider/gitlab"
	"specwire/bridge/internal/security"
	"specwire/bridge/internal/store"
)

type credentialFlowMulticaFake struct{}

func (credentialFlowMulticaFake) ListWorkspaces(context.Context, domain.MulticaInstance, string, *provider.Credential) ([]provider.MulticaWorkspace, error) {
	return nil, errors.New("not used by GitLab credential test")
}
func (credentialFlowMulticaFake) ListProjects(context.Context, domain.MulticaInstance, provider.MulticaWorkspace, string, *provider.Credential) ([]provider.MulticaProject, error) {
	return nil, errors.New("not used by GitLab credential test")
}
func (credentialFlowMulticaFake) CreateProject(context.Context, domain.MulticaInstance, provider.CreateProjectInput, *provider.Credential) (provider.MulticaProject, error) {
	return provider.MulticaProject{}, errors.New("not used by GitLab credential test")
}
func (credentialFlowMulticaFake) EnsureWorkspaceRepository(context.Context, domain.MulticaInstance, provider.MulticaWorkspace, provider.GitLabProject, string, *provider.Credential) (provider.ResourceResult, error) {
	return provider.ResourceResult{}, errors.New("not used by GitLab credential test")
}
func (credentialFlowMulticaFake) EnsureProjectResource(context.Context, domain.MulticaInstance, provider.MulticaProject, provider.GitLabProject, string, *provider.Credential) (provider.ResourceResult, error) {
	return provider.ResourceResult{}, errors.New("not used by GitLab credential test")
}
func (credentialFlowMulticaFake) CreateIssue(context.Context, domain.MulticaInstance, provider.IssueInput, *provider.Credential) (provider.IssueResult, error) {
	return provider.IssueResult{}, errors.New("not used by GitLab credential test")
}
func (credentialFlowMulticaFake) SetIssueStatus(context.Context, domain.MulticaInstance, string, string, *provider.Credential) (provider.IssueStatusResult, error) {
	return provider.IssueStatusResult{}, errors.New("not used by GitLab credential test")
}
func (credentialFlowMulticaFake) ProbeReadiness(context.Context, domain.MulticaInstance) (provider.ReadinessResult, error) {
	return provider.ReadinessResult{}, errors.New("not used by GitLab credential test")
}

func TestGitLabSelectorsUsePersistedCredentialWithoutProcessToken(t *testing.T) {
	t.Setenv("SPECWIRE_GITLAB_TOKEN", "process-token-must-not-be-used")
	var tokens []string
	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("PRIVATE-TOKEN"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "gitlab-request-1")
		switch r.URL.Path {
		case "/api/v4/groups":
			switch r.Header.Get("PRIVATE-TOKEN") {
			case "invalid-token":
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
			default:
				_, _ = w.Write([]byte(`[{"id":7,"full_path":"platform","name":"Platform"}]`))
			}
		case "/api/v4/groups/7/projects":
			if r.Header.Get("PRIVATE-TOKEN") == "forbidden-token" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"403 Forbidden"}`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":42,"path_with_namespace":"platform/webdeck","name":"WebDeck","web_url":"https://gitlab.example/platform/webdeck","ssh_url_to_repo":"git@gitlab.example:platform/webdeck.git","http_url_to_repo":"https://gitlab.example/platform/webdeck.git","namespace":{"id":7}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gitlabServer.Close()

	db, err := store.Open(t.TempDir() + "/provider-credentials.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	local, err := auth.NewLocalProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := security.NewVault(db, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	gitlab := gitlabprovider.NewClient(gitlabServer.Client())
	multica := credentialFlowMulticaFake{}
	probe, err := controlplane.NewProviderEndpointProbe(gitlab, multica, vault)
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := controlplane.NewEndpointService(db, probe)
	if err != nil {
		t.Fatal(err)
	}
	connections, err := controlplane.NewConnectionService(db, gitlab, multica, vault)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := controlplane.NewSelectionService(connections, db)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := controlplane.NewCredentialService(db, vault, probe)
	if err != nil {
		t.Fatal(err)
	}
	api, err := NewServer(local, db, db, endpoints)
	if err != nil {
		t.Fatal(err)
	}
	api.SetIntegrationServices(IntegrationServices{Store: db, Selection: selection, Connections: connections, Credentials: credentials})
	server := httptest.NewServer(api)
	defer server.Close()
	client := server.Client()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.Jar = jar

	status, body := jsonRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/bootstrap", `{"email":"admin@example.com","password":"correct horse battery staple","displayName":"Admin"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("bootstrap = %d %s", status, body)
	}
	var bootstrap struct {
		Workspace domain.Workspace `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(body), &bootstrap); err != nil {
		t.Fatal(err)
	}
	workspaceID := bootstrap.Workspace.ID
	if err := db.CreateGitLabInstance(context.Background(), domain.GitLabInstance{ID: "gitlab-persisted", WorkspaceID: workspaceID, Name: "GitLab", BaseURL: gitlabServer.URL}); err != nil {
		t.Fatal(err)
	}
	status, body = jsonRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", `{"email":"admin@example.com","password":"correct horse battery staple"}`, nil)
	if status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	var login struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal([]byte(body), &login); err != nil {
		t.Fatal(err)
	}
	base := server.URL + "/api/v1/workspaces/" + string(workspaceID)

	for attempt := 1; attempt <= 2; attempt++ {
		status, body = jsonRequest(t, client, http.MethodGet, base+"/gitlab-instances/gitlab-persisted/groups", "", nil)
		if status != http.StatusUnprocessableEntity || !strings.Contains(body, `"code":"provider_credential_required"`) || strings.Contains(body, "internal server error") {
			t.Fatalf("missing credential selector attempt %d = %d %s", attempt, status, body)
		}
	}

	csrf := map[string]string{csrfHeader: login.CSRF}
	status, body = jsonRequest(t, client, http.MethodPost, base+"/gitlab-instances/gitlab-persisted/credentials", `{"alias":"platform/discovery-v1","kind":"pat","secret":"persistent-token-v1"}`, csrf)
	if status != http.StatusCreated || strings.Contains(body, "persistent-token-v1") {
		t.Fatalf("bind instance credential = %d %s", status, body)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base+"/gitlab-instances/gitlab-persisted/groups", "", nil)
	if status != http.StatusOK || !strings.Contains(body, `"external_id":"7"`) {
		t.Fatalf("persisted credential group selector = %d %s", status, body)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base+"/gitlab-instances/gitlab-persisted/projects?group_id=7&group_path=platform", "", nil)
	if status != http.StatusOK || !strings.Contains(body, `"external_id":"42"`) {
		t.Fatalf("persisted credential project selector = %d %s", status, body)
	}

	status, body = jsonRequest(t, client, http.MethodPost, base+"/gitlab-instances/gitlab-persisted/credentials", `{"alias":"platform/discovery-v2","kind":"pat","secret":"persistent-token-v2"}`, csrf)
	if status != http.StatusCreated || strings.Contains(body, "persistent-token-v2") {
		t.Fatalf("rotate instance credential = %d %s", status, body)
	}
	status, _ = jsonRequest(t, client, http.MethodGet, base+"/gitlab-instances/gitlab-persisted/groups", "", nil)
	if status != http.StatusOK {
		t.Fatalf("selector after credential rotation = %d", status)
	}
	if len(tokens) < 3 || tokens[len(tokens)-1] != "persistent-token-v2" {
		t.Fatalf("GitLab request tokens = %q", tokens)
	}
	for _, token := range tokens {
		if token == "process-token-must-not-be-used" {
			t.Fatalf("process-level token was used: %q", tokens)
		}
	}

	status, body = jsonRequest(t, client, http.MethodPost, base+"/gitlab-instances/gitlab-persisted/credentials", `{"alias":"platform/invalid","kind":"pat","secret":"invalid-token"}`, csrf)
	if status != http.StatusUnauthorized || !strings.Contains(body, `"code":"provider_credential_rejected"`) || strings.Contains(body, "invalid-token") {
		t.Fatalf("invalid credential response = %d %s", status, body)
	}
	status, body = jsonRequest(t, client, http.MethodPost, base+"/gitlab-instances/gitlab-persisted/credentials", `{"alias":"platform/forbidden","kind":"pat","secret":"forbidden-token"}`, csrf)
	if status != http.StatusCreated {
		t.Fatalf("bind forbidden-test credential = %d %s", status, body)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base+"/gitlab-instances/gitlab-persisted/projects?group_id=7&group_path=platform", "", nil)
	if status != http.StatusForbidden || !strings.Contains(body, `"code":"provider_credential_forbidden"`) {
		t.Fatalf("forbidden project selector = %d %s", status, body)
	}
	if err := db.CreateGitLabGroupBinding(context.Background(), domain.GitLabGroupBinding{
		ID:               "group-binding-http-test",
		WorkspaceID:      workspaceID,
		GitLabInstanceID: "gitlab-persisted",
		ExternalGroupID:  "7",
		FullPath:         "platform",
		InheritSubgroups: true,
		Status:           domain.EndpointActive,
	}); err != nil {
		t.Fatal(err)
	}
	status, body = jsonRequest(t, client, http.MethodPost, base+"/gitlab-instances/gitlab-persisted/groups/7/credentials?group_path=platform", `{"alias":"platform/group-v1","kind":"pat","secret":"group-token"}`, csrf)
	if status != http.StatusCreated || strings.Contains(body, "group-token") {
		t.Fatalf("bind Group credential = %d %s", status, body)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base+"/gitlab-instances/gitlab-persisted/projects?group_id=7&group_path=platform", "", nil)
	if status != http.StatusOK || !strings.Contains(body, `"external_id":"42"`) {
		t.Fatalf("Group credential project selector = %d %s", status, body)
	}
	if tokens[len(tokens)-1] != "group-token" {
		t.Fatalf("Group project request token = %q, want group-token", tokens[len(tokens)-1])
	}
	if err := db.CreateMulticaInstance(context.Background(), domain.MulticaInstance{ID: "multica-persisted", WorkspaceID: workspaceID, Name: "Multica", BaseURL: "https://multica.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateConnection(context.Background(), domain.Connection{
		ID: "connection-bound-selector", WorkspaceID: workspaceID, Name: "platform/webdeck",
		SourceGitLabProject:  domain.ProviderProjectRef{InstanceID: "gitlab-persisted", GroupID: "7", ExternalID: "42", FullPath: "platform/webdeck"},
		TargetMulticaProject: domain.ProviderProjectRef{InstanceID: "multica-persisted", ExternalID: "target-42", Name: "WebDeck"},
		Status:               domain.ConnectionReady,
	}); err != nil {
		t.Fatal(err)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base+"/gitlab-instances/gitlab-persisted/projects?group_id=7&group_path=platform&exclude_bound=true", "", nil)
	if status != http.StatusOK || strings.Contains(body, `"external_id":"42"`) {
		t.Fatalf("bound project was not excluded: %d %s", status, body)
	}
}
