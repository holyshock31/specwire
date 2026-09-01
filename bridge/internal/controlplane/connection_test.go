package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
	"specwire/bridge/internal/store"
)

func TestOnboardingResultUsesStableJSONFieldNames(t *testing.T) {
	value, err := json.Marshal(OnboardingResult{Operation: domain.OnboardingOperation{ID: "operation-json"}, Connection: domain.Connection{ID: "connection-json"}, Resources: []domain.ManagedResource{}, HookPlan: map[string]any{}, Ready: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"operation"`, `"connection"`, `"resources"`, `"hook_plan"`, `"ready"`} {
		if !strings.Contains(string(value), key) {
			t.Fatalf("JSON result missing %s: %s", key, value)
		}
	}
	for _, key := range []string{`"Operation"`, `"Connection"`, `"Resources"`, `"HookPlan"`, `"Ready"`} {
		if strings.Contains(string(value), key) {
			t.Fatalf("JSON result contains unstable field %s: %s", key, value)
		}
	}
}

type onboardingGitLabFake struct {
	project provider.GitLabProject
	labels  int
}

func (f *onboardingGitLabFake) ListGroups(context.Context, domain.GitLabInstance, string, *provider.Credential) ([]provider.GitLabGroup, error) {
	return nil, nil
}
func (f *onboardingGitLabFake) ListProjects(context.Context, domain.GitLabInstance, provider.GitLabGroup, string, *provider.Credential) ([]provider.GitLabProject, error) {
	return []provider.GitLabProject{f.project}, nil
}
func (f *onboardingGitLabFake) GetProject(context.Context, domain.GitLabInstance, string, *provider.Credential) (provider.GitLabProject, error) {
	return f.project, nil
}
func (f *onboardingGitLabFake) EnsureLabel(context.Context, domain.GitLabInstance, provider.GitLabProject, string, *provider.Credential) (provider.LabelResult, error) {
	f.labels++
	return provider.LabelResult{ExternalID: "label-" + strconv.Itoa(f.labels), Title: func() string {
		if f.labels%2 == 0 {
			return managedAbandonLabel
		}
		return "change"
	}(), Created: true}, nil
}
func (f *onboardingGitLabFake) EnsureHook(context.Context, domain.GitLabInstance, provider.GitLabProject, provider.HookSpec, *provider.Credential) (provider.HookResult, error) {
	return provider.HookResult{ExternalID: "hook-1", Created: true}, nil
}
func (f *onboardingGitLabFake) NoteIssue(context.Context, domain.GitLabInstance, provider.GitLabProject, int, string, *provider.Credential) error {
	return nil
}
func (f *onboardingGitLabFake) CloseIssue(context.Context, domain.GitLabInstance, provider.GitLabProject, int, *provider.Credential) error {
	return nil
}

type onboardingMulticaFake struct {
	projects              []provider.MulticaProject
	created               int
	workspaceResources    int
	projectResources      int
	lastCloneURL          string
	readiness             provider.ReadinessResult
	projectResourceErrors []error
}

func (f *onboardingMulticaFake) ListWorkspaces(context.Context, domain.MulticaInstance, string, *provider.Credential) ([]provider.MulticaWorkspace, error) {
	return []provider.MulticaWorkspace{{ExternalID: "workspace-1", Name: "Team"}}, nil
}
func (f *onboardingMulticaFake) ListProjects(context.Context, domain.MulticaInstance, provider.MulticaWorkspace, string, *provider.Credential) ([]provider.MulticaProject, error) {
	return f.projects, nil
}
func (f *onboardingMulticaFake) CreateProject(_ context.Context, instance domain.MulticaInstance, input provider.CreateProjectInput, _ *provider.Credential) (provider.MulticaProject, error) {
	f.created++
	project := provider.MulticaProject{InstanceID: instance.ID, WorkspaceID: input.WorkspaceID, ExternalID: "project-1", Title: input.Title, WebURL: "https://multica.example/projects/project-1"}
	f.projects = append(f.projects, project)
	return project, nil
}
func (f *onboardingMulticaFake) EnsureWorkspaceRepository(_ context.Context, _ domain.MulticaInstance, _ provider.MulticaWorkspace, project provider.GitLabProject, cloneURL string, _ *provider.Credential) (provider.ResourceResult, error) {
	f.workspaceResources++
	f.lastCloneURL = cloneURL
	return provider.ResourceResult{Kind: domain.ResourceWorkspaceRepository, ExternalID: "repo-resource-1", Created: f.workspaceResources == 1, Adopted: f.workspaceResources > 1, Ownership: func() domain.Ownership {
		if f.workspaceResources == 1 {
			return domain.OwnershipManaged
		}
		return domain.OwnershipAdopted
	}(), Snapshot: map[string]any{"path": project.FullPath}}, nil
}
func (f *onboardingMulticaFake) EnsureProjectResource(_ context.Context, _ domain.MulticaInstance, _ provider.MulticaProject, project provider.GitLabProject, cloneURL string, _ *provider.Credential) (provider.ResourceResult, error) {
	f.projectResources++
	if len(f.projectResourceErrors) > 0 {
		err := f.projectResourceErrors[0]
		f.projectResourceErrors = f.projectResourceErrors[1:]
		return provider.ResourceResult{}, err
	}
	f.lastCloneURL = cloneURL
	return provider.ResourceResult{Kind: domain.ResourceProject, ExternalID: "project-resource-1", Created: f.projectResources == 1, Adopted: f.projectResources > 1, Ownership: func() domain.Ownership {
		if f.projectResources == 1 {
			return domain.OwnershipManaged
		}
		return domain.OwnershipAdopted
	}(), Snapshot: map[string]any{"path": project.FullPath}}, nil
}
func (f *onboardingMulticaFake) CreateIssue(context.Context, domain.MulticaInstance, provider.IssueInput, *provider.Credential) (provider.IssueResult, error) {
	return provider.IssueResult{}, errors.New("not used")
}
func (f *onboardingMulticaFake) SetIssueStatus(context.Context, domain.MulticaInstance, string, string, *provider.Credential) (provider.IssueStatusResult, error) {
	return provider.IssueStatusResult{}, errors.New("not used")
}
func (f *onboardingMulticaFake) ProbeReadiness(context.Context, domain.MulticaInstance) (provider.ReadinessResult, error) {
	return f.readiness, nil
}

func TestConnectionOnboardingCreatesProjectAndBothResourceContexts(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/onboarding.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.CreateWorkspace(ctx, domain.Workspace{ID: "workspace-onboarding", Slug: "onboarding", Name: "Onboarding", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	gitlabInstance := domain.GitLabInstance{ID: "gitlab-onboarding", WorkspaceID: "workspace-onboarding", Name: "GitLab", BaseURL: "https://gitlab.example.test"}
	multicaInstance := domain.MulticaInstance{ID: "multica-onboarding", WorkspaceID: "workspace-onboarding", Name: "Multica", BaseURL: "https://multica.example.test"}
	if err := s.CreateGitLabInstance(ctx, gitlabInstance); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMulticaInstance(ctx, multicaInstance); err != nil {
		t.Fatal(err)
	}
	gitlab := &onboardingGitLabFake{project: provider.GitLabProject{InstanceID: gitlabInstance.ID, ExternalID: "gitlab-project-1", GroupID: "group-1", FullPath: "platform/webdeck", Name: "webdeck", WebURL: "https://gitlab.example/platform/webdeck", SSHURL: "git@gitlab.example:platform/webdeck.git", HTTPSURL: "https://gitlab.example/platform/webdeck.git"}}
	multica := &onboardingMulticaFake{readiness: provider.ReadinessResult{Ready: true}}
	service, err := NewConnectionService(s, gitlab, multica, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Onboard(ctx, OnboardingRequest{OperationID: "onboarding-operation", WorkspaceID: "workspace-onboarding", SourceGitLabInstance: gitlabInstance, SourceProjectExternalID: "gitlab-project-1", TargetMulticaInstance: multicaInstance, TargetWorkspace: provider.MulticaWorkspace{InstanceID: multicaInstance.ID, ExternalID: "workspace-1", Name: "Team"}, CreateTargetProject: true, PreferSSH: true, PublicHookURL: "https://specwire.example/hooks/gitlab"})
	if err != nil {
		t.Fatalf("Onboard: %v", err)
	}
	if result.Connection.Status != domain.ConnectionReady || !result.Ready {
		t.Fatalf("connection readiness = %+v ready=%v", result.Connection, result.Ready)
	}
	if result.Connection.TargetMulticaProject.Name != "platform/webdeck" {
		t.Fatalf("default target title = %q", result.Connection.TargetMulticaProject.Name)
	}
	if len(result.Resources) != 4 {
		t.Fatalf("resources = %d, want two labels + two resource contexts", len(result.Resources))
	}
	if multica.created != 1 || multica.workspaceResources != 1 || multica.projectResources != 1 {
		t.Fatalf("provider calls: project=%d workspace=%d project-resource=%d", multica.created, multica.workspaceResources, multica.projectResources)
	}
	if multica.lastCloneURL != gitlab.project.SSHURL {
		t.Fatalf("clone URL = %q, want SSH %q", multica.lastCloneURL, gitlab.project.SSHURL)
	}
	if result.HookPlan["status"] != "planned" || result.HookPlan["url"] != "https://specwire.example/hooks/gitlab" {
		t.Fatalf("hook plan = %#v", result.HookPlan)
	}
	operation, err := s.GetOnboardingOperation(ctx, "workspace-onboarding", "onboarding-operation")
	if err != nil {
		t.Fatal(err)
	}
	if operation.ConnectionID != result.Connection.ID || operation.Status != domain.OnboardingReady {
		t.Fatalf("persisted operation = %+v", operation)
	}
	resources, err := s.ListManagedResources(ctx, "workspace-onboarding", result.Connection.ID)
	if err != nil || len(resources) != 4 {
		t.Fatalf("persisted resources = %d, %v", len(resources), err)
	}
	audits, err := s.ListAuditEvents(ctx, "workspace-onboarding", "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	providerAuditActions := map[string]bool{}
	for _, audit := range audits {
		if strings.HasPrefix(audit.Action, "provider.") {
			providerAuditActions[audit.Action] = true
		}
	}
	for _, action := range []string{"provider.multica.project.create", "provider.gitlab.label.ensure", "provider.multica.workspace_repository.ensure", "provider.multica.project_resource.ensure"} {
		if !providerAuditActions[action] {
			t.Fatalf("missing provider audit action %q: %+v", action, providerAuditActions)
		}
	}
	second, err := service.Onboard(ctx, OnboardingRequest{OperationID: "onboarding-operation", WorkspaceID: "workspace-onboarding", SourceGitLabInstance: gitlabInstance, SourceProjectExternalID: "gitlab-project-1", TargetMulticaInstance: multicaInstance, TargetWorkspace: provider.MulticaWorkspace{InstanceID: multicaInstance.ID, ExternalID: "workspace-1", Name: "Team"}, CreateTargetProject: true, PreferSSH: true, PublicHookURL: "https://specwire.example/hooks/gitlab"})
	if err != nil {
		t.Fatalf("retry Onboard: %v", err)
	}
	if second.Connection.ID != result.Connection.ID || multica.created != 1 || len(second.Resources) != 4 {
		t.Fatalf("retry duplicated onboarding: connection=%s project_creates=%d resources=%d", second.Connection.ID, multica.created, len(second.Resources))
	}
	plan, err := service.DeprovisionCheck(ctx, "workspace-onboarding", result.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ExternalDeletionPlanned || !plan.HistoryRetained || len(plan.Checks) != 4 {
		t.Fatalf("deprovision plan = %+v", plan)
	}
	for _, check := range plan.Checks {
		if !check.EligibleForManual || check.ExternalDeletion || check.Action != "manual-provider-deprovision-eligible" {
			t.Fatalf("created resource check = %+v", check)
		}
	}
	if err := s.DisableConnection(ctx, "workspace-onboarding", result.Connection.ID); err != nil {
		t.Fatal(err)
	}
	afterDisable, err := service.DeprovisionCheck(ctx, "workspace-onboarding", result.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterDisable.Connection.Status != domain.ConnectionDisabled || afterDisable.RequiresConfirmation {
		t.Fatalf("disabled deprovision plan = %+v", afterDisable)
	}
}

func TestConnectionOnboardingConflictDoesNotCreateTarget(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/onboarding-conflict.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.CreateWorkspace(ctx, domain.Workspace{ID: "workspace-conflict", Slug: "conflict", Name: "Conflict", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	gitlabInstance := domain.GitLabInstance{ID: "gitlab-conflict", WorkspaceID: "workspace-conflict", Name: "GitLab", BaseURL: "https://gitlab.example.test"}
	multicaInstance := domain.MulticaInstance{ID: "multica-conflict", WorkspaceID: "workspace-conflict", Name: "Multica", BaseURL: "https://multica.example.test"}
	if err := s.CreateGitLabInstance(ctx, gitlabInstance); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMulticaInstance(ctx, multicaInstance); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateConnection(ctx, domain.Connection{ID: "existing-connection", WorkspaceID: "workspace-conflict", Name: "Existing", SourceGitLabProject: domain.ProviderProjectRef{InstanceID: gitlabInstance.ID, ExternalID: "same-source"}, TargetMulticaProject: domain.ProviderProjectRef{InstanceID: multicaInstance.ID, ExternalID: "target-existing"}, Status: domain.ConnectionConfigured}); err != nil {
		t.Fatal(err)
	}
	gitlab := &onboardingGitLabFake{project: provider.GitLabProject{InstanceID: gitlabInstance.ID, ExternalID: "same-source", FullPath: "platform/existing", SSHURL: "git@gitlab.example:platform/existing.git"}}
	multica := &onboardingMulticaFake{}
	service, err := NewConnectionService(s, gitlab, multica, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Onboard(ctx, OnboardingRequest{WorkspaceID: "workspace-conflict", SourceGitLabInstance: gitlabInstance, SourceProjectExternalID: "same-source", TargetMulticaInstance: multicaInstance, TargetWorkspace: provider.MulticaWorkspace{ExternalID: "workspace-1"}, CreateTargetProject: true})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflict error = %v, want ErrConflict", err)
	}
	if multica.created != 0 {
		t.Fatalf("target project was created despite conflict: %d", multica.created)
	}
}

func TestConnectionOnboardingResumesAfterPartialResourceFailure(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/onboarding-resume.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	workspaceID := domain.ID("workspace-resume")
	if err := s.CreateWorkspace(ctx, domain.Workspace{ID: workspaceID, Slug: "resume", Name: "Resume", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	gitlabInstance := domain.GitLabInstance{ID: "gitlab-resume", WorkspaceID: workspaceID, Name: "GitLab", BaseURL: "https://gitlab.example.test"}
	multicaInstance := domain.MulticaInstance{ID: "multica-resume", WorkspaceID: workspaceID, Name: "Multica", BaseURL: "https://multica.example.test"}
	if err := s.CreateGitLabInstance(ctx, gitlabInstance); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMulticaInstance(ctx, multicaInstance); err != nil {
		t.Fatal(err)
	}
	gitlab := &onboardingGitLabFake{project: provider.GitLabProject{InstanceID: gitlabInstance.ID, ExternalID: "source-resume", GroupID: "group-resume", FullPath: "platform/resume", Name: "resume", WebURL: "https://gitlab.example/platform/resume", SSHURL: "git@gitlab.example:platform/resume.git", HTTPSURL: "https://gitlab.example/platform/resume.git"}}
	multica := &onboardingMulticaFake{readiness: provider.ReadinessResult{Ready: true}, projectResourceErrors: []error{errors.New("project resource temporarily unavailable")}}
	service, err := NewConnectionService(s, gitlab, multica, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := OnboardingRequest{OperationID: "onboarding-resume-operation", WorkspaceID: workspaceID, SourceGitLabInstance: gitlabInstance, SourceProjectExternalID: "source-resume", TargetMulticaInstance: multicaInstance, TargetWorkspace: provider.MulticaWorkspace{InstanceID: multicaInstance.ID, ExternalID: "workspace-resume", Name: "Team"}, CreateTargetProject: true, PreferSSH: true}
	if _, err := service.Onboard(ctx, request); err == nil {
		t.Fatal("first onboarding should report the partial provider failure")
	}
	operation, err := s.GetOnboardingOperation(ctx, workspaceID, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != domain.OnboardingFailed || operation.ConnectionID.Empty() {
		t.Fatalf("partial operation = %+v", operation)
	}
	partial, err := s.ListManagedResources(ctx, workspaceID, operation.ConnectionID)
	if err != nil || len(partial) != 3 {
		t.Fatalf("partial resources = %d, %v; want two labels and workspace repository", len(partial), err)
	}

	result, err := service.Onboard(ctx, request)
	if err != nil {
		t.Fatalf("resume onboarding: %v", err)
	}
	if result.Connection.ID != operation.ConnectionID || result.Connection.Status != domain.ConnectionReady {
		t.Fatalf("resumed connection = %+v", result.Connection)
	}
	if multica.created != 1 || multica.workspaceResources != 1 || multica.projectResources != 2 {
		t.Fatalf("resume repeated provider effects: projects=%d workspace_resources=%d project_resources=%d", multica.created, multica.workspaceResources, multica.projectResources)
	}
	resources, err := s.ListManagedResources(ctx, workspaceID, result.Connection.ID)
	if err != nil || len(resources) != 4 {
		t.Fatalf("resumed resources = %d, %v; want two labels and two resource contexts", len(resources), err)
	}
}
