package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"specwire/bridge/internal/controlplane"
	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/provider"
	"specwire/bridge/internal/registry"
	"specwire/bridge/internal/security"
	"specwire/bridge/internal/store"
)

type runtimeGitLabFake struct {
	hookID   string
	ensure   int
	closed   []int
	closeErr error
}

type runtimeGitLabCredentials struct{}

func (runtimeGitLabCredentials) ResolveForConnection(_ context.Context, connection domain.Connection) (*provider.Credential, func(), error) {
	material := []byte("runtime-test-token")
	credential := &provider.Credential{
		Ref: domain.SecretRef{
			ID:          "credential-runtime",
			WorkspaceID: connection.WorkspaceID,
			Alias:       "runtime-test",
			Kind:        domain.SecretGroupCredential,
		},
		Material: material,
	}
	return credential, func() { clearBytes(material) }, nil
}

func (f *runtimeGitLabFake) ListGroups(context.Context, domain.GitLabInstance, string, *provider.Credential) ([]provider.GitLabGroup, error) {
	return nil, nil
}
func (f *runtimeGitLabFake) ListProjects(context.Context, domain.GitLabInstance, provider.GitLabGroup, string, *provider.Credential) ([]provider.GitLabProject, error) {
	return nil, nil
}
func (f *runtimeGitLabFake) GetProject(_ context.Context, instance domain.GitLabInstance, externalID string, _ *provider.Credential) (provider.GitLabProject, error) {
	return provider.GitLabProject{InstanceID: instance.ID, ExternalID: externalID, GroupID: "group-1", FullPath: "platform/service", Name: "service", WebURL: "https://gitlab.example/platform/service", SSHURL: "git@gitlab.example:platform/service.git", HTTPSURL: "https://gitlab.example/platform/service.git"}, nil
}
func (f *runtimeGitLabFake) EnsureLabel(context.Context, domain.GitLabInstance, provider.GitLabProject, string, *provider.Credential) (provider.LabelResult, error) {
	return provider.LabelResult{ExternalID: "change", Title: "change", Adopted: true}, nil
}
func (f *runtimeGitLabFake) EnsureHook(context.Context, domain.GitLabInstance, provider.GitLabProject, provider.HookSpec, *provider.Credential) (provider.HookResult, error) {
	f.ensure++
	if f.hookID == "" {
		f.hookID = "hook-runtime"
		return provider.HookResult{ExternalID: f.hookID, Created: true}, nil
	}
	return provider.HookResult{ExternalID: f.hookID, Adopted: true}, nil
}
func (f *runtimeGitLabFake) CloseIssue(_ context.Context, _ domain.GitLabInstance, _ provider.GitLabProject, iid int, _ *provider.Credential) error {
	f.closed = append(f.closed, iid)
	return f.closeErr
}

type runtimeMulticaFake struct {
	created  []provider.IssueInput
	statuses []string
}

func (f *runtimeMulticaFake) ListWorkspaces(context.Context, domain.MulticaInstance, string, *provider.Credential) ([]provider.MulticaWorkspace, error) {
	return nil, nil
}
func (f *runtimeMulticaFake) ListProjects(context.Context, domain.MulticaInstance, provider.MulticaWorkspace, string, *provider.Credential) ([]provider.MulticaProject, error) {
	return nil, nil
}
func (f *runtimeMulticaFake) CreateProject(context.Context, domain.MulticaInstance, provider.CreateProjectInput, *provider.Credential) (provider.MulticaProject, error) {
	return provider.MulticaProject{}, nil
}
func (f *runtimeMulticaFake) EnsureWorkspaceRepository(context.Context, domain.MulticaInstance, provider.MulticaWorkspace, provider.GitLabProject, string, *provider.Credential) (provider.ResourceResult, error) {
	return provider.ResourceResult{}, nil
}
func (f *runtimeMulticaFake) EnsureProjectResource(context.Context, domain.MulticaInstance, provider.MulticaProject, provider.GitLabProject, string, *provider.Credential) (provider.ResourceResult, error) {
	return provider.ResourceResult{}, nil
}
func (f *runtimeMulticaFake) CreateIssue(_ context.Context, _ domain.MulticaInstance, input provider.IssueInput, _ *provider.Credential) (provider.IssueResult, error) {
	f.created = append(f.created, input)
	return provider.IssueResult{IssueID: "issue-runtime", ProjectID: input.ProjectID, Created: true, RequestID: "multica-create-1"}, nil
}
func (f *runtimeMulticaFake) SetIssueStatus(_ context.Context, _ domain.MulticaInstance, issueID, status string, _ *provider.Credential) (provider.IssueStatusResult, error) {
	f.statuses = append(f.statuses, issueID+":"+status)
	return provider.IssueStatusResult{IssueID: issueID, Status: status, RequestID: "multica-status-1"}, nil
}
func (f *runtimeMulticaFake) ProbeReadiness(context.Context, domain.MulticaInstance) (provider.ReadinessResult, error) {
	return provider.ReadinessResult{Ready: true}, nil
}

func openRuntime(t *testing.T) (*store.Store, flow.Catalog, *security.Vault, *runtimeGitLabFake, *runtimeMulticaFake, domain.ID) {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/runtime.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	workspaceID := domain.ID("workspace-runtime")
	if err := db.CreateWorkspace(ctx, domain.Workspace{ID: workspaceID, Slug: "runtime", Name: "Runtime", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	gitlabInstance := domain.GitLabInstance{ID: "gitlab-runtime", WorkspaceID: workspaceID, Name: "GitLab", BaseURL: "https://gitlab.example"}
	if err := db.CreateGitLabInstance(ctx, gitlabInstance); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateMulticaInstance(ctx, domain.MulticaInstance{ID: "multica-runtime", WorkspaceID: workspaceID, Name: "Multica", BaseURL: "http://multica.example"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateConnection(ctx, domain.Connection{ID: "connection-runtime", WorkspaceID: workspaceID, Name: "platform/service", SourceGitLabProject: domain.ProviderProjectRef{InstanceID: gitlabInstance.ID, ExternalID: "101", GroupID: "group-1", FullPath: "platform/service", Name: "service", WebURL: "https://gitlab.example/platform/service", SSHURL: "git@gitlab.example:platform/service.git", HTTPSURL: "https://gitlab.example/platform/service.git"}, TargetMulticaProject: domain.ProviderProjectRef{InstanceID: "multica-runtime", ExternalID: "target-1", Name: "Service"}, Status: domain.ConnectionReady}); err != nil {
		t.Fatal(err)
	}
	bundle, err := registry.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.BootstrapRegistry(ctx, workspaceID, bundle); err != nil {
		t.Fatal(err)
	}
	catalog := flow.NewCatalog(bundle.Behaviors, bundle.DataModels, []string{"gitlab.events.issue", "gitlab.events.push", "multica.issue.create", "multica.issue.status"})
	key := sha256.Sum256([]byte("runtime master key"))
	vault, err := security.NewVault(db, key[:])
	if err != nil {
		t.Fatal(err)
	}
	return db, catalog, vault, &runtimeGitLabFake{}, &runtimeMulticaFake{}, workspaceID
}

func TestIngressAcceptsAndDeduplicatesPublication(t *testing.T) {
	db, catalog, vault, gitlab, multica, workspaceID := openRuntime(t)
	ctx := context.Background()
	reconciler, err := controlplane.NewHookReconciler(db, gitlab, vault, catalog, "https://specwire.example/gitlab/specwire?instance_id=gitlab-runtime")
	if err != nil {
		t.Fatal(err)
	}
	service, err := flow.NewService(db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	service.SetRouteActivator(reconciler)
	if err := service.SeedBuiltins(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	item, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplatePublishChange, "1.0.0", "Publish")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Publish(ctx, workspaceID, item.ID, ""); err != nil {
		t.Fatal(err)
	}
	hook, err := db.GetHookByProject(ctx, workspaceID, "gitlab-runtime", "101")
	if err != nil || hook.SigningRef == nil {
		t.Fatalf("hook = %+v, err=%v", hook, err)
	}
	secret, err := vault.Resolve(ctx, *hook.SigningRef)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"object_kind":"issue","object_attributes":{"iid":7,"action":"open","description":"change_id: CHG-7\nbranch: change/7\nbranch_head_sha: abc123\n","labels":[{"title":"change"}]},"project":{"id":101,"path_with_namespace":"platform/service","web_url":"https://gitlab.example/platform/service"}}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	id := "delivery-runtime-1"
	signature := sign(secret, id, ts, body)
	clearBytes(secret)
	ingress, err := NewIngress(db, vault, catalog)
	if err != nil {
		t.Fatal(err)
	}
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/gitlab/specwire?instance_id=gitlab-runtime", strings.NewReader(string(body)))
		r.Header.Set("X-Gitlab-Event", "Issue Hook")
		r.Header.Set("webhook-id", id)
		r.Header.Set("webhook-timestamp", ts)
		r.Header.Set("webhook-signature", signature)
		response := httptest.NewRecorder()
		ingress.ServeHTTP(response, r)
		return response
	}
	first := request()
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	var firstResult IngressResult
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	if firstResult.Accepted != 1 || firstResult.Duplicates != 0 {
		t.Fatalf("first result=%+v", firstResult)
	}
	second := request()
	var secondResult IngressResult
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatal(err)
	}
	if second.Code != http.StatusAccepted || secondResult.Duplicates != 1 || secondResult.Accepted != 0 {
		t.Fatalf("second result status=%d result=%+v", second.Code, secondResult)
	}
	if len(multica.created) != 0 {
		t.Fatal("ingress must not call Multica synchronously")
	}
	executions, err := db.ListFlowExecutions(ctx, workspaceID, "connection-runtime", item.ID, 10)
	if err != nil || len(executions) != 1 {
		t.Fatalf("executions=%d err=%v", len(executions), err)
	}
	executor, err := NewExecutor(db, gitlab, multica, vault, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Execute(ctx, workspaceID, executions[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(multica.created) != 1 || multica.created[0].ProjectID != "target-1" || multica.created[0].Title != "[SpecWire] CHG-7" {
		t.Fatalf("created=%+v", multica.created)
	}
	correlation, err := db.GetCorrelation(ctx, workspaceID, "connection-runtime", "101", "CHG-7")
	if err != nil || correlation.TargetIdentity != "issue-runtime" || correlation.SourceIssueIID != 7 {
		t.Fatalf("correlation=%+v err=%v", correlation, err)
	}
}

func TestIngressAndExecutorCompleteArchiveCorrelation(t *testing.T) {
	db, catalog, vault, gitlab, multica, workspaceID := openRuntime(t)
	ctx := context.Background()
	reconciler, err := controlplane.NewHookReconciler(db, gitlab, vault, catalog, "https://specwire.example/gitlab/specwire?instance_id=gitlab-runtime")
	if err != nil {
		t.Fatal(err)
	}
	service, err := flow.NewService(db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	service.SetRouteActivator(reconciler)
	if err := service.SeedBuiltins(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	publication, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplatePublishChange, "1.0.0", "Publish")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Publish(ctx, workspaceID, publication.ID, ""); err != nil {
		t.Fatal(err)
	}
	hook, err := db.GetHookByProject(ctx, workspaceID, "gitlab-runtime", "101")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := vault.Resolve(ctx, *hook.SigningRef)
	if err != nil {
		t.Fatal(err)
	}
	publicationBody := []byte(`{"object_kind":"issue","object_attributes":{"iid":8,"action":"open","description":"change_id: CHG-8\nbranch: change/8\nbranch_head_sha: def456\n","labels":[{"title":"change"}]},"project":{"id":101,"path_with_namespace":"platform/service"}}`)
	pubID, pubTS := "delivery-pub-8", strconv.FormatInt(time.Now().Unix(), 10)
	ingress, err := NewIngress(db, vault, catalog)
	if err != nil {
		t.Fatal(err)
	}
	post := func(id, ts string, body []byte, event string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/gitlab/specwire?instance_id=gitlab-runtime", strings.NewReader(string(body)))
		r.Header.Set("X-Gitlab-Event", event)
		r.Header.Set("webhook-id", id)
		r.Header.Set("webhook-timestamp", ts)
		r.Header.Set("webhook-signature", sign(secret, id, ts, body))
		response := httptest.NewRecorder()
		ingress.ServeHTTP(response, r)
		return response
	}
	if response := post(pubID, pubTS, publicationBody, "Issue Hook"); response.Code != http.StatusAccepted {
		t.Fatalf("publication status=%d body=%s", response.Code, response.Body.String())
	}
	publicationExecutions, err := db.ListFlowExecutions(ctx, workspaceID, "connection-runtime", publication.ID, 10)
	if err != nil || len(publicationExecutions) != 1 {
		t.Fatalf("publication executions=%d err=%v", len(publicationExecutions), err)
	}
	executor, err := NewExecutor(db, gitlab, multica, vault, catalog, WithGitLabCredentialResolver(runtimeGitLabCredentials{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Execute(ctx, workspaceID, publicationExecutions[0].ID); err != nil {
		t.Fatal(err)
	}
	archive, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplateCompleteArchive, "1.0.0", "Complete")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Publish(ctx, workspaceID, archive.ID, ""); err != nil {
		t.Fatal(err)
	}
	archiveBody := []byte(`{"ref":"refs/heads/main","after":"123456","head_commit":{"id":"commit-1","message":"archive\n\nSpecWire-Event: archived\nSpecWire-Change: CHG-8"},"commits":[],"project":{"id":101,"path_with_namespace":"platform/service"}}`)
	archiveID, archiveTS := "delivery-archive-8", strconv.FormatInt(time.Now().Unix(), 10)
	archiveResponse := post(archiveID, archiveTS, archiveBody, "Push Hook")
	if archiveResponse.Code != http.StatusAccepted {
		t.Fatalf("archive status=%d body=%s", archiveResponse.Code, archiveResponse.Body.String())
	}
	archiveExecutions, err := db.ListFlowExecutions(ctx, workspaceID, "connection-runtime", archive.ID, 10)
	if err != nil || len(archiveExecutions) != 1 {
		routes, _ := db.ListHookRoutesForProject(ctx, "gitlab-runtime", "101")
		t.Fatalf("archive executions=%d err=%v routes=%+v matches=%v ids=%v", len(archiveExecutions), err, routes, matchesBehavior(GitLabEnvelope{EventName: "Push Hook", Payload: func() map[string]any { var value map[string]any; _ = json.Unmarshal(archiveBody, &value); return value }()}, "gitlab.push-hook"), archiveChangeIDs(func() map[string]any { var value map[string]any; _ = json.Unmarshal(archiveBody, &value); return value }()))
	}
	if err := executor.Execute(ctx, workspaceID, archiveExecutions[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(multica.statuses) != 1 || multica.statuses[0] != "issue-runtime:done" {
		t.Fatalf("statuses=%v", multica.statuses)
	}
	if len(gitlab.closed) != 1 || gitlab.closed[0] != 8 {
		t.Fatalf("closed issues=%v", gitlab.closed)
	}
}

func sign(secret []byte, id, timestamp string, body []byte) string {
	key := secret
	if len(secret) > 6 && string(secret[:6]) == "whsec_" {
		decoded, _ := base64.StdEncoding.DecodeString(string(secret[6:]))
		key = decoded
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id + "." + timestamp + "." + string(body)))
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
