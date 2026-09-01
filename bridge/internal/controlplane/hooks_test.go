package controlplane

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/provider"
	"specwire/bridge/internal/registry"
	"specwire/bridge/internal/security"
	"specwire/bridge/internal/store"
)

type hookGitLabFake struct {
	ensureCalls  int
	lastSpec     provider.HookSpec
	ensureErrors []error
}

func (f *hookGitLabFake) ListGroups(context.Context, domain.GitLabInstance, string, *provider.Credential) ([]provider.GitLabGroup, error) {
	return nil, nil
}
func (f *hookGitLabFake) ListProjects(context.Context, domain.GitLabInstance, provider.GitLabGroup, string, *provider.Credential) ([]provider.GitLabProject, error) {
	return nil, nil
}
func (f *hookGitLabFake) GetProject(_ context.Context, instance domain.GitLabInstance, externalID string, _ *provider.Credential) (provider.GitLabProject, error) {
	return provider.GitLabProject{InstanceID: instance.ID, ExternalID: externalID, FullPath: "platform/service"}, nil
}
func (f *hookGitLabFake) EnsureLabel(context.Context, domain.GitLabInstance, provider.GitLabProject, string, *provider.Credential) (provider.LabelResult, error) {
	return provider.LabelResult{}, nil
}
func (f *hookGitLabFake) EnsureHook(_ context.Context, _ domain.GitLabInstance, _ provider.GitLabProject, spec provider.HookSpec, _ *provider.Credential) (provider.HookResult, error) {
	f.ensureCalls++
	if len(f.ensureErrors) > 0 {
		err := f.ensureErrors[0]
		f.ensureErrors = f.ensureErrors[1:]
		return provider.HookResult{}, err
	}
	f.lastSpec = spec
	return provider.HookResult{ExternalID: "hook-42", Adopted: f.ensureCalls > 1}, nil
}
func (f *hookGitLabFake) NoteIssue(context.Context, domain.GitLabInstance, provider.GitLabProject, int, string, *provider.Credential) error {
	return nil
}
func (f *hookGitLabFake) CloseIssue(context.Context, domain.GitLabInstance, provider.GitLabProject, int, *provider.Credential) error {
	return nil
}

func TestHookReconcilerSharesHookAcrossPublishedInputFlows(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/hooks.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	workspaceID := domain.ID("workspace-hooks")
	if err := s.CreateWorkspace(ctx, domain.Workspace{ID: workspaceID, Slug: "hooks", Name: "Hooks", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	gitlabInstance := domain.GitLabInstance{ID: "gitlab-hooks", WorkspaceID: workspaceID, Name: "GitLab", BaseURL: "https://gitlab.example.test"}
	if err := s.CreateGitLabInstance(ctx, gitlabInstance); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMulticaInstance(ctx, domain.MulticaInstance{ID: "multica-hooks", WorkspaceID: workspaceID, Name: "Multica", BaseURL: "https://multica.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateConnection(ctx, domain.Connection{ID: "connection-hooks", WorkspaceID: workspaceID, Name: "platform/service", SourceGitLabProject: domain.ProviderProjectRef{InstanceID: gitlabInstance.ID, ExternalID: "source-1", FullPath: "platform/service"}, TargetMulticaProject: domain.ProviderProjectRef{InstanceID: "multica-hooks", ExternalID: "target-1", Name: "Service"}, Status: domain.ConnectionConfigured}); err != nil {
		t.Fatal(err)
	}
	bundle, err := registry.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BootstrapRegistry(ctx, workspaceID, bundle); err != nil {
		t.Fatal(err)
	}
	catalog := flow.NewCatalog(bundle.Behaviors, bundle.DataModels, []string{"gitlab.events.issue", "gitlab.events.push", "multica.issue.create", "multica.issue.status"})
	key := sha256.Sum256([]byte("hook test master key"))
	vault, err := security.NewVault(s, key[:])
	if err != nil {
		t.Fatal(err)
	}
	gitlab := &hookGitLabFake{}
	reconciler, err := NewHookReconciler(s, gitlab, vault, catalog, "https://specwire.example/gitlab/specwire")
	if err != nil {
		t.Fatal(err)
	}
	flowService, err := flow.NewService(s, catalog)
	if err != nil {
		t.Fatal(err)
	}
	flowService.SetRouteActivator(reconciler)
	if err := flowService.SeedBuiltins(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	first, err := flowService.CreateFromTemplate(ctx, workspaceID, "connection-hooks", "", flow.TemplatePublishChange, "1.0.0", "Publish")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := flowService.Publish(ctx, workspaceID, first.ID, ""); err != nil {
		t.Fatal(err)
	}
	second, err := flowService.CreateFromTemplate(ctx, workspaceID, "connection-hooks", "", flow.TemplateCompleteArchive, "1.0.0", "Complete")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := flowService.Publish(ctx, workspaceID, second.ID, ""); err != nil {
		t.Fatal(err)
	}
	if gitlab.ensureCalls != 2 || len(gitlab.lastSpec.SigningToken) == 0 {
		t.Fatalf("Hook calls/token = %d/%q", gitlab.ensureCalls, gitlab.lastSpec.SigningToken)
	}
	if len(gitlab.lastSpec.Events) != 2 || gitlab.lastSpec.Events[0] != "Issue Hook" || gitlab.lastSpec.Events[1] != "Push Hook" {
		t.Fatalf("shared Hook events = %v, want Issue Hook and Push Hook", gitlab.lastSpec.Events)
	}
	hook, err := s.GetHookByProject(ctx, workspaceID, gitlabInstance.ID, "source-1")
	if err != nil {
		t.Fatal(err)
	}
	if hook.ExternalID != "hook-42" || hook.SigningRef == nil || hook.SigningRef.Alias == "" {
		t.Fatalf("stored Hook = %+v", hook)
	}
	oldSigningRef := *hook.SigningRef
	rotated, err := reconciler.RotateSigningToken(ctx, workspaceID, "connection-hooks")
	if err != nil {
		t.Fatalf("rotate Hook signing token: %v", err)
	}
	if rotated.SigningRef == nil || rotated.SigningRef.ID == oldSigningRef.ID {
		t.Fatalf("rotated Hook signing ref = %+v, old = %+v", rotated.SigningRef, oldSigningRef)
	}
	rotatedSecret, err := vault.Resolve(ctx, *rotated.SigningRef)
	if err != nil || len(rotatedSecret) == 0 {
		t.Fatalf("rotated Hook secret = %q, %v", rotatedSecret, err)
	}
	clearBytes(rotatedSecret)
	gitlab.ensureErrors = []error{errors.New("GitLab temporarily unavailable")}
	if _, err := reconciler.RotateSigningToken(ctx, workspaceID, "connection-hooks"); err == nil {
		t.Fatal("provider rotation failure must be returned")
	}
	afterFailedRotation, err := s.GetHookByProject(ctx, workspaceID, gitlabInstance.ID, "source-1")
	if err != nil {
		t.Fatal(err)
	}
	if afterFailedRotation.SigningRef == nil || afterFailedRotation.SigningRef.ID != rotated.SigningRef.ID {
		t.Fatalf("failed rotation changed durable signing ref: %+v", afterFailedRotation.SigningRef)
	}
	secret, err := vault.Resolve(ctx, *hook.SigningRef)
	if err != nil || len(secret) == 0 {
		t.Fatalf("stored Hook secret = %q, %v", secret, err)
	}
	clearBytes(secret)
	routes, err := s.ListHookRoutes(ctx, workspaceID, "connection-hooks")
	if err != nil || len(routes) != 2 {
		t.Fatalf("routes = %d, %v", len(routes), err)
	}
	if err := flowService.Pause(ctx, workspaceID, first.ID, 1); err != nil {
		t.Fatal(err)
	}
	routes, err = s.ListHookRoutes(ctx, workspaceID, "connection-hooks")
	if err != nil {
		t.Fatal(err)
	}
	paused, active := 0, 0
	for _, route := range routes {
		if route.FlowID == first.ID && route.Status == domain.HookDisabled {
			paused++
		}
		if route.FlowID == second.ID && route.Status == domain.HookActive {
			active++
		}
	}
	if paused != 1 || active != 1 {
		t.Fatalf("shared route statuses = %+v", routes)
	}
}
