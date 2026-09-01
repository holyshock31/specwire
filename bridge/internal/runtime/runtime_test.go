package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	noted    []string
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
func (f *runtimeGitLabFake) NoteIssue(_ context.Context, _ domain.GitLabInstance, _ provider.GitLabProject, iid int, body string, _ *provider.Credential) error {
	f.noted = append(f.noted, strconv.Itoa(iid)+":"+body)
	return nil
}
func (f *runtimeGitLabFake) CloseIssue(_ context.Context, _ domain.GitLabInstance, _ provider.GitLabProject, iid int, _ *provider.Credential) error {
	f.closed = append(f.closed, iid)
	return f.closeErr
}

type runtimeMulticaFake struct {
	created   []provider.IssueInput
	statuses  []string
	createErr error
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
	if f.createErr != nil {
		return provider.IssueResult{}, f.createErr
	}
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
	if len(multica.created) != 1 || multica.created[0].ProjectID != "target-1" || multica.created[0].Title != "[SpecWire] CHG-7" || !strings.Contains(multica.created[0].Description, "branch_head_sha: abc123") {
		t.Fatalf("created=%+v", multica.created)
	}
	audits, err := db.ListAuditEvents(ctx, workspaceID, "flow_execution", executions[0].ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	foundProviderCreate := false
	for _, audit := range audits {
		if audit.Action == "provider.multica.issue.create" && audit.Payload["outcome"] == "succeeded" {
			foundProviderCreate = true
		}
	}
	if !foundProviderCreate {
		t.Fatalf("provider side effect audit missing: %+v", audits)
	}
	correlation, err := db.GetCorrelation(ctx, workspaceID, "connection-runtime", item.ID, "101", "CHG-7")
	if err != nil || correlation.TargetIdentity != "issue-runtime" || correlation.SourceIssueIID != 7 {
		t.Fatalf("correlation=%+v err=%v", correlation, err)
	}
}

func TestLiveTestRequiresConfirmationAndQueuesRedactedExecution(t *testing.T) {
	db, catalog, _, _, _, workspaceID := openRuntime(t)
	ctx := context.Background()
	service, err := flow.NewService(db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SeedBuiltins(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	item, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplatePublishChange, "1.0.0", "Live test")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Publish(ctx, workspaceID, item.ID, ""); err != nil {
		t.Fatal(err)
	}
	liveTests, err := NewLiveTestService(db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := liveTests.Start(ctx, workspaceID, item.ID, LiveTestRequest{}); err == nil {
		t.Fatal("live test without confirmation must be rejected")
	}
	secretSample := map[string]any{
		"object_kind": "issue",
		"object_attributes": map[string]any{
			"iid":         9,
			"action":      "open",
			"description": "change_id: LIVE-9\nbranch: live-test\nbranch_head_sha: sha-9",
			"token":       "must-not-be-stored",
		},
		"token": "must-not-be-stored",
	}
	execution, err := liveTests.Start(ctx, workspaceID, item.ID, LiveTestRequest{SampleEvent: secretSample, ConfirmSideEffects: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(execution.DeliveryID, "live-test:") || execution.Status != domain.ExecutionQueued || execution.FlowVersion != 1 {
		t.Fatalf("live test execution = %+v", execution)
	}
	event, err := db.GetInboundEvent(ctx, workspaceID, execution.EventID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(event.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must-not-be-stored") {
		t.Fatalf("live test stored secret sample: %s", encoded)
	}
	jobs, err := db.ClaimNextJob(ctx, "live-test-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if jobs.Payload["execution_id"] != string(execution.ID) {
		t.Fatalf("live test job payload = %+v", jobs.Payload)
	}
}

func TestLiveTestCompleteArchiveUsesPushHookDefaultSample(t *testing.T) {
	db, catalog, _, _, _, workspaceID := openRuntime(t)
	ctx := context.Background()
	service, err := flow.NewService(db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SeedBuiltins(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	item, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplateCompleteArchive, "1.0.0", "Complete archive live test")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Publish(ctx, workspaceID, item.ID, ""); err != nil {
		t.Fatal(err)
	}
	liveTests, err := NewLiveTestService(db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := liveTests.Start(ctx, workspaceID, item.ID, LiveTestRequest{ConfirmSideEffects: true})
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != domain.ExecutionQueued || !strings.HasPrefix(execution.DeliveryID, "live-test:") {
		t.Fatalf("complete archive live test execution = %+v", execution)
	}
	event, err := db.GetInboundEvent(ctx, workspaceID, execution.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if event.Payload["object_kind"] != "push" || event.Payload["ref"] != "refs/heads/main" {
		t.Fatalf("default push sample = %+v", event.Payload)
	}
	if strings.TrimSpace(stringValue(event.Payload["after"])) == "" {
		t.Fatalf("default push sample has no commit SHA: %+v", event.Payload)
	}
	changeID := stringValue(event.Payload["change_id"])
	if !strings.HasPrefix(changeID, "LIVE-") {
		t.Fatalf("default push sample change_id = %q", changeID)
	}
	headCommit, ok := event.Payload["head_commit"].(map[string]any)
	if !ok || !strings.Contains(stringValue(headCommit["message"]), "SpecWire-Event: archived") || !strings.Contains(stringValue(headCommit["message"]), "SpecWire-Change: "+changeID) {
		t.Fatalf("default push sample archive trailer = %+v", event.Payload["head_commit"])
	}
	if stringValue(event.Payload["provider_delivery_id"]) != "live-test:"+string(execution.ID) {
		t.Fatalf("default push sample provider delivery = %q", event.Payload["provider_delivery_id"])
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

func TestIngressAndExecutorAbandonCancelsProjection(t *testing.T) {
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
	publication, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplatePublishChange, "1.0.0", "Publish abandoned change")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Publish(ctx, workspaceID, publication.ID, ""); err != nil {
		t.Fatal(err)
	}
	abandonFlow, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplateAbandonChange, "1.0.0", "Cancel abandoned projection")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Publish(ctx, workspaceID, abandonFlow.ID, ""); err != nil {
		t.Fatal(err)
	}
	archiveFlow, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplateCompleteArchive, "1.0.0", "Complete archive")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Publish(ctx, workspaceID, archiveFlow.ID, ""); err != nil {
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
	ingress, err := NewIngress(db, vault, catalog)
	if err != nil {
		t.Fatal(err)
	}
	post := func(id string, event string, body []byte) *httptest.ResponseRecorder {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		r := httptest.NewRequest(http.MethodPost, "/gitlab/specwire?instance_id=gitlab-runtime", strings.NewReader(string(body)))
		r.Header.Set("X-Gitlab-Event", event)
		r.Header.Set("webhook-id", id)
		r.Header.Set("webhook-timestamp", ts)
		r.Header.Set("webhook-signature", sign(secret, id, ts, body))
		response := httptest.NewRecorder()
		ingress.ServeHTTP(response, r)
		return response
	}
	publicationBody := []byte(`{"object_kind":"issue","object_attributes":{"iid":21,"action":"open","description":"change_id: CHG-ABANDON\nbranch: change/feat-CHG-ABANDON\nbranch_head_sha: abandon-sha\n","labels":[{"title":"change"}]},"project":{"id":101,"path_with_namespace":"platform/service"}}`)
	if response := post("delivery-pub-abandon", "Issue Hook", publicationBody); response.Code != http.StatusAccepted {
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

	abandonBody := []byte(`{"object_kind":"issue","object_attributes":{"iid":21,"action":"update","description":"change_id: CHG-ABANDON\nbranch: change/feat-CHG-ABANDON\nbranch_head_sha: abandon-sha\nSpecWire-Reason: change has no actual content\n","labels":[{"title":"change"},{"title":"specwire::abandoned"}]},"changes":{"labels":{"previous":[{"title":"change"}],"current":[{"title":"change"},{"title":"specwire::abandoned"}]}},"project":{"id":101,"path_with_namespace":"platform/service"}}`)
	if response := post("delivery-abandon", "Issue Hook", abandonBody); response.Code != http.StatusAccepted {
		t.Fatalf("abandon status=%d body=%s", response.Code, response.Body.String())
	}
	abandonExecutions, err := db.ListFlowExecutions(ctx, workspaceID, "connection-runtime", abandonFlow.ID, 10)
	if err != nil || len(abandonExecutions) != 1 {
		t.Fatalf("abandon executions=%d err=%v", len(abandonExecutions), err)
	}
	if err := executor.Execute(ctx, workspaceID, abandonExecutions[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(multica.statuses) != 1 || multica.statuses[0] != "issue-runtime:cancelled" {
		t.Fatalf("statuses after abandon=%v", multica.statuses)
	}
	if len(gitlab.noted) != 1 || !strings.Contains(gitlab.noted[0], "change has no actual content") {
		t.Fatalf("abandon notes=%v", gitlab.noted)
	}
	if len(gitlab.closed) != 1 || gitlab.closed[0] != 21 {
		t.Fatalf("closed issues after abandon=%v", gitlab.closed)
	}
	correlations, err := db.ListCorrelations(ctx, workspaceID, "connection-runtime", "101", "CHG-ABANDON")
	if err != nil || len(correlations) != 1 || correlations[0].LifecycleStatus != domain.ProjectionCancelled {
		t.Fatalf("cancelled correlations=%+v err=%v", correlations, err)
	}

	archiveBody := []byte(`{"ref":"refs/heads/main","after":"archive-after-abandon","head_commit":{"id":"archive-after-abandon","message":"archive\n\nSpecWire-Event: archived\nSpecWire-Change: CHG-ABANDON"},"commits":[],"project":{"id":101,"path_with_namespace":"platform/service"}}`)
	if response := post("delivery-archive-after-abandon", "Push Hook", archiveBody); response.Code != http.StatusAccepted {
		t.Fatalf("late archive status=%d body=%s", response.Code, response.Body.String())
	}
	lateExecutions, err := db.ListFlowExecutions(ctx, workspaceID, "connection-runtime", archiveFlow.ID, 10)
	if err != nil || len(lateExecutions) != 1 {
		t.Fatalf("late archive executions=%d err=%v", len(lateExecutions), err)
	}
	if err := executor.Execute(ctx, workspaceID, lateExecutions[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(multica.statuses) != 1 || len(gitlab.noted) != 1 || len(gitlab.closed) != 1 {
		t.Fatalf("terminal guard repeated provider effects: statuses=%v notes=%v closed=%v", multica.statuses, gitlab.noted, gitlab.closed)
	}
}

func TestIngressDeliversOneDeliveryToEveryMatchingPublishedFlow(t *testing.T) {
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
	first, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplatePublishChange, "1.0.0", "Publish one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplatePublishChange, "1.0.0", "Publish two")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Publish(ctx, workspaceID, first.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Publish(ctx, workspaceID, second.ID, ""); err != nil {
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
	body := []byte(`{"object_kind":"issue","object_attributes":{"iid":17,"action":"open","description":"change_id: CHG-17\nbranch: change/17\nbranch_head_sha: frozen-17\n","labels":[{"title":"change"}]},"project":{"id":101,"path_with_namespace":"platform/service"}}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "/gitlab/specwire?instance_id=gitlab-runtime", strings.NewReader(string(body)))
	request.Header.Set("X-Gitlab-Event", "Issue Hook")
	request.Header.Set("webhook-id", "delivery-multiple-flows")
	request.Header.Set("webhook-timestamp", timestamp)
	request.Header.Set("webhook-signature", sign(secret, "delivery-multiple-flows", timestamp, body))
	clearBytes(secret)
	response := httptest.NewRecorder()
	ingress, err := NewIngress(db, vault, catalog)
	if err != nil {
		t.Fatal(err)
	}
	ingress.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("ingress status=%d body=%s", response.Code, response.Body.String())
	}
	var result IngressResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Accepted != 2 || result.Duplicates != 0 {
		t.Fatalf("ingress result=%+v", result)
	}
	firstExecutions, err := db.ListFlowExecutions(ctx, workspaceID, "connection-runtime", first.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	secondExecutions, err := db.ListFlowExecutions(ctx, workspaceID, "connection-runtime", second.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstExecutions) != 1 || len(secondExecutions) != 1 {
		t.Fatalf("flow executions = first:%d second:%d", len(firstExecutions), len(secondExecutions))
	}
	executor, err := NewExecutor(db, gitlab, multica, vault, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Execute(ctx, workspaceID, firstExecutions[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := executor.Execute(ctx, workspaceID, secondExecutions[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(multica.created) != 2 {
		t.Fatalf("matching Flows must produce independent projections, got %d", len(multica.created))
	}
	firstCorrelation, err := db.GetCorrelation(ctx, workspaceID, "connection-runtime", first.ID, "101", "CHG-17")
	if err != nil || firstCorrelation.FlowID != first.ID {
		t.Fatalf("first Flow correlation=%+v err=%v", firstCorrelation, err)
	}
	secondCorrelation, err := db.GetCorrelation(ctx, workspaceID, "connection-runtime", second.ID, "101", "CHG-17")
	if err != nil || secondCorrelation.FlowID != second.ID {
		t.Fatalf("second Flow correlation=%+v err=%v", secondCorrelation, err)
	}
}

func TestArchiveBeforePublicationRequiresReconciliation(t *testing.T) {
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
	archive, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplateCompleteArchive, "1.0.0", "Archive first")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Publish(ctx, workspaceID, archive.ID, ""); err != nil {
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
	body := []byte(`{"ref":"refs/heads/main","after":"archive-sha","head_commit":{"id":"archive-sha","message":"archive\n\nSpecWire-Event: archived\nSpecWire-Change: MISSING-1"},"project":{"id":101,"path_with_namespace":"platform/service"}}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "/gitlab/specwire?instance_id=gitlab-runtime", strings.NewReader(string(body)))
	request.Header.Set("X-Gitlab-Event", "Push Hook")
	request.Header.Set("webhook-id", "delivery-archive-before-publication")
	request.Header.Set("webhook-timestamp", timestamp)
	request.Header.Set("webhook-signature", sign(secret, "delivery-archive-before-publication", timestamp, body))
	clearBytes(secret)
	response := httptest.NewRecorder()
	ingress, err := NewIngress(db, vault, catalog)
	if err != nil {
		t.Fatal(err)
	}
	ingress.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("ingress status=%d body=%s", response.Code, response.Body.String())
	}
	executions, err := db.ListFlowExecutions(ctx, workspaceID, "connection-runtime", archive.ID, 10)
	if err != nil || len(executions) != 1 {
		t.Fatalf("archive executions=%d err=%v", len(executions), err)
	}
	executor, err := NewExecutor(db, gitlab, multica, vault, catalog)
	if err != nil {
		t.Fatal(err)
	}
	executionErr := executor.Execute(ctx, workspaceID, executions[0].ID)
	if executionErr == nil || !errors.Is(executionErr, domain.ErrNotFound) {
		t.Fatalf("archive without correlation error=%v, want reconciliation/not-found", executionErr)
	}
	stored, err := db.GetFlowExecution(ctx, workspaceID, executions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.ExecutionReconciliationNeeded || stored.ErrorCategory != "reconciliation-required" {
		t.Fatalf("stored execution=%+v", stored)
	}
	nodes, err := db.ListNodeExecutions(ctx, workspaceID, executions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var completionNode *domain.NodeExecution
	for index := range nodes {
		if nodes[index].NodeID == "multica-complete" {
			completionNode = &nodes[index]
			break
		}
	}
	if completionNode == nil || completionNode.Status != domain.NodeIndeterminate {
		t.Fatalf("archive node checkpoints=%+v", nodes)
	}
	if len(multica.statuses) != 0 {
		t.Fatalf("missing correlation must not call Multica: %v", multica.statuses)
	}
}

func TestProviderTimeoutMarksExecutionIndeterminate(t *testing.T) {
	db, catalog, vault, gitlab, multica, workspaceID := openRuntime(t)
	ctx := context.Background()
	service, err := flow.NewService(db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SeedBuiltins(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	publication, err := service.CreateFromTemplate(ctx, workspaceID, "connection-runtime", "", flow.TemplatePublishChange, "1.0.0", "Timeout")
	if err != nil {
		t.Fatal(err)
	}
	version, _, err := service.Publish(ctx, workspaceID, publication.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	event := domain.InboundEvent{ID: "event-provider-timeout", WorkspaceID: workspaceID, ConnectionID: "connection-runtime", Provider: domain.ProviderGitLab, SourceInstanceID: "gitlab-runtime", SourceProjectExternalID: "101", BehaviorKey: "gitlab.issue-hook", BehaviorVersion: "1.0.0", DeliveryID: "delivery-provider-timeout", Payload: map[string]any{
		"object_kind":       "issue",
		"object_attributes": map[string]any{"iid": 19, "action": "open", "description": "change_id: TIMEOUT-19\nbranch: timeout\nbranch_head_sha: sha-timeout", "labels": []any{map[string]any{"title": "change"}}},
		"project":           map[string]any{"id": 101, "path_with_namespace": "platform/service"},
	}}
	execution := domain.FlowExecution{ID: "execution-provider-timeout", WorkspaceID: workspaceID, ConnectionID: "connection-runtime", FlowID: publication.ID, FlowVersionID: version.ID, FlowVersion: version.Version, DeliveryID: event.DeliveryID, IdempotencyKey: "idempotency-provider-timeout", CorrelationID: "correlation-provider-timeout", Status: domain.ExecutionQueued}
	job := domain.Job{ID: "job-provider-timeout", WorkspaceID: workspaceID, Kind: JobKindFlowExecute, Payload: map[string]any{"execution_id": execution.ID, "connection_id": execution.ConnectionID}}
	if _, created, err := db.AcceptInboundEvent(ctx, event, execution, job); err != nil || !created {
		t.Fatalf("accept timeout execution: created=%v err=%v", created, err)
	}
	multica.createErr = &provider.ProviderError{Provider: domain.ProviderMultica, Operation: "create issue", Category: provider.ErrorTimeout, Err: errors.New("provider deadline exceeded")}
	executor, err := NewExecutor(db, gitlab, multica, vault, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if executionErr := executor.Execute(ctx, workspaceID, execution.ID); executionErr == nil {
		t.Fatal("provider timeout must fail the execution")
	}
	stored, err := db.GetFlowExecution(ctx, workspaceID, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.ExecutionReconciliationNeeded || stored.ErrorCategory != string(provider.ErrorTimeout) {
		t.Fatalf("stored timeout execution=%+v", stored)
	}
	nodes, err := db.ListNodeExecutions(ctx, workspaceID, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	var outputNode *domain.NodeExecution
	for index := range nodes {
		if nodes[index].NodeID == "multica-create" {
			outputNode = &nodes[index]
			break
		}
	}
	if outputNode == nil || outputNode.Status != domain.NodeIndeterminate || outputNode.ErrorCategory != string(provider.ErrorTimeout) {
		t.Fatalf("timeout node checkpoints=%+v", nodes)
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
