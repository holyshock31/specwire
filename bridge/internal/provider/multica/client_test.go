package multica

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
)

type scriptedRunner struct {
	calls  [][]string
	inputs []string
}

func (r *scriptedRunner) Run(_ context.Context, args []string, stdin io.Reader) (CommandResult, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if stdin != nil {
		body, _ := io.ReadAll(stdin)
		r.inputs = append(r.inputs, string(body))
	} else {
		r.inputs = append(r.inputs, "")
	}
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "workspace list"):
		return CommandResult{Stdout: []byte(`[{"id":"workspace-1","name":"Team"}]`), RequestID: "req-workspace"}, nil
	case strings.Contains(joined, "project list"):
		return CommandResult{Stdout: []byte(`[]`), RequestID: "req-project-list"}, nil
	case strings.Contains(joined, "project create"):
		return CommandResult{Stdout: []byte(`{"id":"project-1","title":"platform/webdeck"}`), RequestID: "req-project-create"}, nil
	case strings.Contains(joined, "repo list"):
		return CommandResult{Stdout: []byte(`[]`), RequestID: "req-repo-list"}, nil
	case strings.Contains(joined, "repo add"):
		return CommandResult{Stdout: []byte(`{"id":"repo-1","url":"git@gitlab.example:platform/webdeck.git"}`), RequestID: "req-repo-add"}, nil
	case strings.Contains(joined, "project resource list"):
		return CommandResult{Stdout: []byte(`[]`), RequestID: "req-resource-list"}, nil
	case strings.Contains(joined, "project resource add"):
		return CommandResult{Stdout: []byte(`{"id":"resource-1","url":"git@gitlab.example:platform/webdeck.git"}`), RequestID: "req-resource-add"}, nil
	case strings.Contains(joined, "issue create"):
		return CommandResult{Stdout: []byte(`{"id":"issue-1","project_id":"project-1"}`), RequestID: "req-issue-create"}, nil
	case strings.Contains(joined, "issue status"):
		return CommandResult{Stdout: []byte(`{"id":"issue-1","status":"done"}`), RequestID: "req-issue-status"}, nil
	default:
		return CommandResult{Stdout: []byte(`[]`), RequestID: "req-default"}, nil
	}
}

func TestClientUsesSafeCLIContractForOnboardingAndIssueActions(t *testing.T) {
	runner := &scriptedRunner{}
	client := NewClient(Options{Runner: runner, Profile: "specwire-local", Timeout: time.Second})
	instance := domain.MulticaInstance{ID: "multica-1", BaseURL: "http://multica.example.test"}
	ctx := context.Background()

	workspaces, err := client.ListWorkspaces(ctx, instance, "team", nil)
	if err != nil || len(workspaces) != 1 || workspaces[0].ExternalID != "workspace-1" {
		t.Fatalf("workspaces = %+v, %v", workspaces, err)
	}
	projects, err := client.ListProjects(ctx, instance, workspaces[0], "", nil)
	if err != nil || projects != nil && len(projects) != 0 {
		t.Fatalf("projects = %+v, %v", projects, err)
	}
	created, err := client.CreateProject(ctx, instance, provider.CreateProjectInput{WorkspaceID: "workspace-1", Title: "platform/webdeck", IdempotencyKey: "operation-1:project"}, nil)
	if err != nil || created.ExternalID != "project-1" || created.Title != "platform/webdeck" {
		t.Fatalf("created project = %+v, %v", created, err)
	}
	gitlabProject := provider.GitLabProject{FullPath: "platform/webdeck"}
	repo, err := client.EnsureWorkspaceRepository(ctx, instance, workspaces[0], gitlabProject, "git@gitlab.example:platform/webdeck.git", nil)
	if err != nil || !repo.Created || repo.Ownership != domain.OwnershipManaged {
		t.Fatalf("repo result = %+v, %v", repo, err)
	}
	resource, err := client.EnsureProjectResource(ctx, instance, created, gitlabProject, "git@gitlab.example:platform/webdeck.git", nil)
	if err != nil || !resource.Created || resource.Kind != domain.ResourceProject {
		t.Fatalf("project resource = %+v, %v", resource, err)
	}
	issue, err := client.CreateIssue(ctx, instance, provider.IssueInput{ProjectID: created.ExternalID, Title: "[SpecWire] CH-1", Description: "body", IdempotencyKey: "execution-1"}, nil)
	if err != nil || issue.IssueID != "issue-1" {
		t.Fatalf("issue = %+v, %v", issue, err)
	}
	if len(runner.inputs) == 0 || runner.inputs[len(runner.inputs)-1] != "body" {
		t.Fatalf("issue stdin = %q", runner.inputs[len(runner.inputs)-1])
	}
	status, err := client.SetIssueStatus(ctx, instance, issue.IssueID, "done", nil)
	if err != nil || status.Status != "done" {
		t.Fatalf("status = %+v, %v", status, err)
	}
	ready, err := client.ProbeReadiness(ctx, instance)
	if err != nil || !ready.Ready {
		t.Fatalf("readiness = %+v, %v", ready, err)
	}
	for _, call := range runner.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "secret") || strings.Contains(joined, "token") {
			t.Fatalf("secret-like argument leaked into CLI call: %v", call)
		}
		if len(call) < 2 || call[0] != "--profile" || call[1] != "specwire-local" {
			t.Fatalf("global CLI flags missing: %v", call)
		}
	}
}

func TestClientAdoptsExistingRepositoryAndProjectResource(t *testing.T) {
	runner := &scriptedRunnerWithExisting{}
	client := NewClient(Options{Runner: runner, Timeout: time.Second})
	instance := domain.MulticaInstance{ID: "multica-1"}
	workspace := provider.MulticaWorkspace{InstanceID: instance.ID, ExternalID: "workspace-1"}
	project := provider.GitLabProject{FullPath: "platform/webdeck"}
	target := provider.MulticaProject{InstanceID: instance.ID, WorkspaceID: workspace.ExternalID, ExternalID: "project-1", Title: "WebDeck"}
	repo, err := client.EnsureWorkspaceRepository(context.Background(), instance, workspace, project, "https://gitlab.example/platform/webdeck.git", nil)
	if err != nil || !repo.Adopted || repo.Ownership != domain.OwnershipAdopted {
		t.Fatalf("adopted repo = %+v, %v", repo, err)
	}
	resource, err := client.EnsureProjectResource(context.Background(), instance, target, project, "https://gitlab.example/platform/webdeck.git", nil)
	if err != nil || !resource.Adopted || resource.Ownership != domain.OwnershipAdopted {
		t.Fatalf("adopted resource = %+v, %v", resource, err)
	}
	if runner.addCalls != 0 {
		t.Fatalf("existing resources were added %d times", runner.addCalls)
	}
}

type scriptedRunnerWithExisting struct{ addCalls int }

func (r *scriptedRunnerWithExisting) Run(_ context.Context, args []string, _ io.Reader) (CommandResult, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "repo list"):
		return CommandResult{Stdout: []byte(`[{"id":"repo-1","url":"https://gitlab.example/platform/webdeck.git"}]`), RequestID: "repo-list"}, nil
	case strings.Contains(joined, "project resource list"):
		return CommandResult{Stdout: []byte(`[{"id":"resource-1","url":"https://gitlab.example/platform/webdeck.git"}]`), RequestID: "resource-list"}, nil
	case strings.Contains(joined, "repo add"), strings.Contains(joined, "project resource add"):
		r.addCalls++
	}
	return CommandResult{Stdout: []byte(`[]`), RequestID: "request"}, nil
}
