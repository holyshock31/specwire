package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"specwire/bridge/internal/auth"
	"specwire/bridge/internal/controlplane"
	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/registry"
	"specwire/bridge/internal/security"
	"specwire/bridge/internal/store"
)

type testGroupCredentialProbe struct{}

func (testGroupCredentialProbe) ProbeGitLabGroup(context.Context, domain.GitLabInstance, domain.GitLabGroupBinding, []byte) ([]domain.CapabilityResult, error) {
	return []domain.CapabilityResult{{Capability: "gitlab.groups.read", Available: true}, {Capability: "gitlab.projects.read", Available: true}}, nil
}

func TestIntegrationFlowAPIUsesConnectionScopeAndLifecycle(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/integration-api.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	local, err := auth.NewLocalProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := controlplane.NewEndpointService(db, testProbe{})
	if err != nil {
		t.Fatal(err)
	}
	api, err := NewServer(local, db, db, endpoints)
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := registry.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	catalog := flow.NewCatalog(bundle.Behaviors, bundle.DataModels, []string{
		"gitlab.events.issue", "gitlab.events.push", "multica.issue.create", "multica.issue.status",
	})
	flowService, err := flow.NewService(db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	registryService, err := controlplane.NewRegistryService(db, registry.AdapterAllowlist{
		"gitlab.events.issue": true, "gitlab.events.push": true,
		"multica.issue.create": true, "multica.issue.status": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	api.SetIntegrationServices(IntegrationServices{Store: db, Flows: flowService, Registry: registryService})

	server := httptest.NewServer(api)
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar

	status, body := jsonRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/bootstrap", `{"email":"admin@example.com","password":"correct horse battery staple","displayName":"Admin"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("bootstrap status = %d body=%s", status, body)
	}
	var bootstrap struct {
		Workspace domain.Workspace `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(body), &bootstrap); err != nil {
		t.Fatal(err)
	}
	workspaceID := bootstrap.Workspace.ID
	if workspaceID.Empty() {
		t.Fatal("bootstrap did not return workspace")
	}
	if err := db.BootstrapRegistry(context.Background(), workspaceID, bundle); err != nil {
		t.Fatal(err)
	}
	if err := flowService.SeedBuiltins(context.Background(), workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateGitLabInstance(context.Background(), domain.GitLabInstance{ID: "gitlab-api", WorkspaceID: workspaceID, Name: "GitLab", BaseURL: "https://gitlab.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateMulticaInstance(context.Background(), domain.MulticaInstance{ID: "multica-api", WorkspaceID: workspaceID, Name: "Multica", BaseURL: "https://multica.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateConnection(context.Background(), domain.Connection{
		ID: "connection-api", WorkspaceID: workspaceID, Name: "platform/webdeck",
		SourceGitLabProject:  domain.ProviderProjectRef{InstanceID: "gitlab-api", GroupID: "group-api", ExternalID: "project-api", FullPath: "platform/webdeck"},
		TargetMulticaProject: domain.ProviderProjectRef{InstanceID: "multica-api", ExternalID: "target-api", Name: "WebDeck"},
		Status:               domain.ConnectionReady,
	}); err != nil {
		t.Fatal(err)
	}

	status, body = jsonRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", `{"email":"admin@example.com","password":"correct horse battery staple"}`, nil)
	if status != http.StatusOK {
		t.Fatalf("login status = %d body=%s", status, body)
	}
	var login struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal([]byte(body), &login); err != nil {
		t.Fatal(err)
	}
	csrf := map[string]string{csrfHeader: login.CSRF}
	status, body = jsonRequest(t, client, http.MethodGet, server.URL+"/api/v1/auth/workspaces", "", nil)
	if status != http.StatusOK || !strings.Contains(body, string(workspaceID)) {
		t.Fatalf("list account Workspaces = %d %s", status, body)
	}
	base := server.URL + "/api/v1/workspaces/" + string(workspaceID)

	status, body = jsonRequest(t, client, http.MethodPost, base+"/flows", `{"connection_id":"connection-api","template_key":"publish-change","template_version":"1.0.0","name":"Publish Change"}`, csrf)
	if status != http.StatusCreated {
		t.Fatalf("create Flow status = %d body=%s", status, body)
	}
	var created domain.Flow
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}

	status, body = jsonRequest(t, client, http.MethodGet, base+"/flows", "", nil)
	if status != http.StatusOK || !strings.Contains(body, string(created.ID)) {
		t.Fatalf("list all Flows = %d %s", status, body)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base+"/flows?connection_id=connection-api", "", nil)
	if status != http.StatusOK || !strings.Contains(body, string(created.ID)) {
		t.Fatalf("list connection Flows = %d %s", status, body)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base+"/flows/"+string(created.ID), "", nil)
	if status != http.StatusOK || !strings.Contains(body, "Publish Change") || !strings.Contains(body, "parse-publication") {
		t.Fatalf("get Flow detail = %d %s", status, body)
	}

	status, body = jsonRequest(t, client, http.MethodPost, base+"/flows/"+string(created.ID)+"/publish", "{}", csrf)
	if status != http.StatusOK || !strings.Contains(body, `"version":`) {
		t.Fatalf("publish Flow = %d %s", status, body)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base+"/flows/"+string(created.ID)+"/versions", "", nil)
	if status != http.StatusOK || !strings.Contains(body, `"version":1`) {
		t.Fatalf("list Flow versions = %d %s", status, body)
	}
	status, _ = jsonRequest(t, client, http.MethodPost, base+"/flows/"+string(created.ID)+"/pause", "{}", csrf)
	if status != http.StatusNoContent {
		t.Fatalf("pause Flow = %d", status)
	}
	status, _ = jsonRequest(t, client, http.MethodPost, base+"/flows/"+string(created.ID)+"/archive", "{}", csrf)
	if status != http.StatusNoContent {
		t.Fatalf("archive Flow = %d", status)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base+"/flows/"+string(created.ID), "", nil)
	if status != http.StatusOK || !strings.Contains(body, `"status":"archived"`) {
		t.Fatalf("archived Flow detail = %d %s", status, body)
	}

	status, body = jsonRequest(t, client, http.MethodPost, base+"/registry/connector-types", `{"key":"custom","version":"1.0.0","display_name":"Custom","provider":"custom","status":"draft"}`, csrf)
	if status != http.StatusCreated {
		t.Fatalf("register connector type = %d %s", status, body)
	}
	var registered domain.ConnectorType
	if err := json.Unmarshal([]byte(body), &registered); err != nil {
		t.Fatal(err)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base+"/audit-events?entity_type=connector-types&entity_id="+string(registered.ID), "", nil)
	if status != http.StatusOK || !strings.Contains(body, "registry.connector-types.register") {
		t.Fatalf("registry audit = %d %s", status, body)
	}
	status, body = jsonRequest(t, client, http.MethodPatch, base+"/registry/connector-types/"+string(registered.ID), `{"status":"published"}`, csrf)
	if status != http.StatusOK || !strings.Contains(body, `"status":"published"`) {
		t.Fatalf("publish connector type = %d %s", status, body)
	}
}

func TestGroupCredentialAPIDoesNotExposeSecretAndSupportsRotation(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/credentials-api.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	local, err := auth.NewLocalProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := controlplane.NewEndpointService(db, testProbe{})
	if err != nil {
		t.Fatal(err)
	}
	api, err := NewServer(local, db, db, endpoints)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := security.NewVault(db, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := controlplane.NewCredentialService(db, vault, testGroupCredentialProbe{})
	if err != nil {
		t.Fatal(err)
	}
	api.SetIntegrationServices(IntegrationServices{Store: db, Credentials: credentials})
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
		t.Fatalf("bootstrap status = %d body=%s", status, body)
	}
	var bootstrap struct {
		Workspace domain.Workspace `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(body), &bootstrap); err != nil {
		t.Fatal(err)
	}
	workspaceID := bootstrap.Workspace.ID
	if err := db.CreateGitLabInstance(context.Background(), domain.GitLabInstance{ID: "gitlab-credentials-api", WorkspaceID: workspaceID, Name: "GitLab", BaseURL: "https://gitlab.example.test"}); err != nil {
		t.Fatal(err)
	}
	status, body = jsonRequest(t, client, http.MethodPost, server.URL+"/api/v1/auth/login", `{"email":"admin@example.com","password":"correct horse battery staple"}`, nil)
	if status != http.StatusOK {
		t.Fatalf("login status = %d body=%s", status, body)
	}
	var login struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal([]byte(body), &login); err != nil {
		t.Fatal(err)
	}
	base := server.URL + "/api/v1/workspaces/" + string(workspaceID) + "/gitlab-instances/gitlab-credentials-api/groups/group-1/credentials"
	headers := map[string]string{csrfHeader: login.CSRF}
	status, body = jsonRequest(t, client, http.MethodPost, base, `{"alias":"platform/group-token","kind":"group_access_token","secret":"super-secret-v1"}`, headers)
	if status != http.StatusCreated || strings.Contains(body, "super-secret-v1") {
		t.Fatalf("bind credential = %d %s", status, body)
	}
	var profile domain.CredentialProfile
	if err := json.Unmarshal([]byte(body), &profile); err != nil {
		t.Fatal(err)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base, "", nil)
	if status != http.StatusOK || strings.Contains(body, "super-secret-v1") || !strings.Contains(body, string(profile.ID)) {
		t.Fatalf("get credential = %d %s", status, body)
	}
	rotateURL := base + "/" + string(profile.ID) + "/rotate"
	status, body = jsonRequest(t, client, http.MethodPost, rotateURL, `{"secret":"super-secret-v2"}`, headers)
	if status != http.StatusOK || strings.Contains(body, "super-secret-v2") {
		t.Fatalf("rotate credential = %d %s", status, body)
	}
	status, body = jsonRequest(t, client, http.MethodGet, base, "", nil)
	if status != http.StatusOK || strings.Contains(body, "super-secret-v1") || strings.Contains(body, "super-secret-v2") {
		t.Fatalf("credential after rotation = %d %s", status, body)
	}
}
