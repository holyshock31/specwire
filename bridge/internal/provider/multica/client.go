package multica

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
)

const maxResponseBytes = 4 << 20

// CommandResult is deliberately small: the adapter needs stdout/stderr and a
// local request identity, but it never exposes command arguments containing
// credentials to callers.
type CommandResult struct {
	Stdout    []byte
	Stderr    []byte
	RequestID string
}

type Runner interface {
	Run(context.Context, []string, io.Reader) (CommandResult, error)
}

type CommandError struct {
	ExitCode int
	Stderr   string
	Err      error
}

func (e *CommandError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("multica exited with code %d: %v", e.ExitCode, e.Err)
	}
	return fmt.Sprintf("multica exited with code %d: %v: %s", e.ExitCode, e.Err, truncate(e.Stderr, 300))
}

func (e *CommandError) Unwrap() error { return e.Err }

type OSRunner struct {
	Binary string
}

func (r OSRunner) Run(ctx context.Context, args []string, stdin io.Reader) (CommandResult, error) {
	binary := strings.TrimSpace(r.Binary)
	if binary == "" {
		binary = "multica"
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdin = stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedBuffer{Buffer: &stdout, Limit: maxResponseBytes}
	cmd.Stderr = &limitedBuffer{Buffer: &stderr, Limit: maxResponseBytes}
	if err := cmd.Start(); err != nil {
		return CommandResult{}, &CommandError{ExitCode: -1, Err: err}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), RequestID: domain.NewID().String()}, commandError(err, stderr.String())
		}
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), RequestID: domain.NewID().String()}, &CommandError{ExitCode: -1, Stderr: stderr.String(), Err: ctx.Err()}
	}
	return CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), RequestID: domain.NewID().String()}, nil
}

type limitedBuffer struct {
	*bytes.Buffer
	Limit int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	remaining := b.Limit - b.Len()
	if remaining <= 0 {
		return len(value), nil
	}
	if len(value) > remaining {
		value = value[:remaining]
	}
	_, _ = b.Buffer.Write(value)
	return len(value), nil
}

type Client struct {
	runner  Runner
	profile string
	timeout time.Duration
}

type Options struct {
	Runner  Runner
	Profile string
	Timeout time.Duration
	Binary  string
}

func NewClient(options Options) *Client {
	if options.Runner == nil {
		options.Runner = OSRunner{Binary: options.Binary}
	}
	if strings.TrimSpace(options.Profile) == "" {
		options.Profile = "specwire-local"
	}
	if options.Timeout <= 0 {
		options.Timeout = 30 * time.Second
	}
	return &Client{runner: options.Runner, profile: strings.TrimSpace(options.Profile), timeout: options.Timeout}
}

func (c *Client) ListWorkspaces(ctx context.Context, instance domain.MulticaInstance, query string, credential *provider.Credential) ([]provider.MulticaWorkspace, error) {
	_ = credential
	result, err := c.run(ctx, instance, "", "workspace", "list", "--output", "json")
	if err != nil {
		return nil, err
	}
	items, err := objectList(result.Stdout, "workspaces", "items")
	if err != nil {
		return nil, c.invalid("list workspaces", result, err)
	}
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]provider.MulticaWorkspace, 0, len(items))
	for _, item := range items {
		workspace := provider.MulticaWorkspace{InstanceID: instance.ID, ExternalID: firstMapString(item, "id", "uuid", "workspace_id"), Name: firstMapString(item, "name", "title")}
		if query != "" && !strings.Contains(strings.ToLower(workspace.Name), query) && !strings.Contains(strings.ToLower(workspace.ExternalID), query) {
			continue
		}
		if workspace.ExternalID != "" {
			out = append(out, workspace)
		}
	}
	return out, nil
}

func (c *Client) ListProjects(ctx context.Context, instance domain.MulticaInstance, workspace provider.MulticaWorkspace, query string, credential *provider.Credential) ([]provider.MulticaProject, error) {
	_ = credential
	result, err := c.run(ctx, instance, workspace.ExternalID, "project", "list", "--output", "json")
	if err != nil {
		return nil, err
	}
	items, err := objectList(result.Stdout, "projects", "items")
	if err != nil {
		return nil, c.invalid("list projects", result, err)
	}
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]provider.MulticaProject, 0, len(items))
	for _, item := range items {
		project := provider.MulticaProject{InstanceID: instance.ID, WorkspaceID: workspace.ExternalID, ExternalID: firstMapString(item, "id", "uuid", "project_id"), Title: firstMapString(item, "title", "name"), WebURL: firstMapString(item, "web_url", "url")}
		if query != "" && !strings.Contains(strings.ToLower(project.Title), query) && !strings.Contains(strings.ToLower(project.ExternalID), query) {
			continue
		}
		if project.ExternalID != "" {
			out = append(out, project)
		}
	}
	return out, nil
}

func (c *Client) CreateProject(ctx context.Context, instance domain.MulticaInstance, input provider.CreateProjectInput, credential *provider.Credential) (provider.MulticaProject, error) {
	_ = credential
	if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.WorkspaceID) == "" {
		return provider.MulticaProject{}, fmt.Errorf("%w: Multica project title and workspace are required", domain.ErrInvalid)
	}
	args := []string{"project", "create", "--title", input.Title, "--output", "json"}
	if strings.TrimSpace(input.Description) != "" {
		args = append(args, "--description", input.Description)
	}
	result, err := c.run(ctx, instance, input.WorkspaceID, args...)
	if err != nil {
		return provider.MulticaProject{}, err
	}
	item, err := objectValue(result.Stdout, "project")
	if err != nil {
		return provider.MulticaProject{}, c.invalid("create project", result, err)
	}
	project := mapProject(instance.ID, input.WorkspaceID, item)
	if project.ExternalID == "" {
		return provider.MulticaProject{}, c.invalid("create project", result, errors.New("response did not contain project ID"))
	}
	if project.Title == "" {
		project.Title = input.Title
	}
	return project, nil
}

func (c *Client) EnsureWorkspaceRepository(ctx context.Context, instance domain.MulticaInstance, workspace provider.MulticaWorkspace, project provider.GitLabProject, cloneURL string, credential *provider.Credential) (provider.ResourceResult, error) {
	_ = credential
	cloneURL = strings.TrimSpace(cloneURL)
	if cloneURL == "" {
		return provider.ResourceResult{}, fmt.Errorf("%w: repository clone URL is required", domain.ErrInvalid)
	}
	result, err := c.run(ctx, instance, workspace.ExternalID, "repo", "list", "--output", "json")
	if err != nil {
		return provider.ResourceResult{}, err
	}
	items, err := objectList(result.Stdout, "repositories", "repos", "items")
	if err != nil {
		return provider.ResourceResult{}, c.invalid("list workspace repositories", result, err)
	}
	for _, item := range items {
		if sameURL(firstMapString(item, "url", "clone_url", "repository_url"), cloneURL) {
			return provider.ResourceResult{Kind: domain.ResourceWorkspaceRepository, ExternalID: firstMapString(item, "id", "uuid", "url"), Adopted: true, Ownership: domain.OwnershipAdopted, RequestID: result.RequestID, Snapshot: map[string]any{"clone_url": cloneURL, "project": project.FullPath}}, nil
		}
	}
	added, err := c.run(ctx, instance, workspace.ExternalID, "repo", "add", cloneURL, "--output", "json")
	if err != nil {
		return provider.ResourceResult{}, err
	}
	item, parseErr := objectValue(added.Stdout, "repository", "repo")
	if parseErr != nil {
		// Some CLI versions return a bare confirmation string.  The URL is still
		// a stable external identity for the workspace registry.
		item = map[string]any{}
	}
	return provider.ResourceResult{Kind: domain.ResourceWorkspaceRepository, ExternalID: firstNonEmpty(firstMapString(item, "id", "uuid", "url"), cloneURL), Created: true, Ownership: domain.OwnershipManaged, RequestID: added.RequestID, Snapshot: map[string]any{"clone_url": cloneURL, "project": project.FullPath}}, nil
}

func (c *Client) EnsureProjectResource(ctx context.Context, instance domain.MulticaInstance, target provider.MulticaProject, project provider.GitLabProject, cloneURL string, credential *provider.Credential) (provider.ResourceResult, error) {
	_ = credential
	cloneURL = strings.TrimSpace(cloneURL)
	if target.ExternalID == "" || cloneURL == "" {
		return provider.ResourceResult{}, fmt.Errorf("%w: Multica project and clone URL are required", domain.ErrInvalid)
	}
	result, err := c.run(ctx, instance, target.WorkspaceID, "project", "resource", "list", target.ExternalID, "--output", "json")
	if err != nil {
		return provider.ResourceResult{}, err
	}
	items, err := objectList(result.Stdout, "resources", "items")
	if err != nil {
		return provider.ResourceResult{}, c.invalid("list project resources", result, err)
	}
	for _, item := range items {
		if sameURL(firstMapString(item, "url", "clone_url", "repository_url"), cloneURL) {
			return provider.ResourceResult{Kind: domain.ResourceProject, ExternalID: firstMapString(item, "id", "uuid", "url"), Adopted: true, Ownership: domain.OwnershipAdopted, RequestID: result.RequestID, Snapshot: map[string]any{"clone_url": cloneURL, "project": project.FullPath}}, nil
		}
	}
	added, err := c.run(ctx, instance, target.WorkspaceID, "project", "resource", "add", target.ExternalID, "--type", "gitlab_repo", "--url", cloneURL, "--label", "GitLab: "+project.FullPath, "--output", "json")
	if err != nil {
		return provider.ResourceResult{}, err
	}
	item, parseErr := objectValue(added.Stdout, "resource", "item")
	if parseErr != nil {
		item = map[string]any{}
	}
	return provider.ResourceResult{Kind: domain.ResourceProject, ExternalID: firstNonEmpty(firstMapString(item, "id", "uuid", "url"), cloneURL), Created: true, Ownership: domain.OwnershipManaged, RequestID: added.RequestID, Snapshot: map[string]any{"clone_url": cloneURL, "project": project.FullPath, "type": "gitlab_repo"}}, nil
}

func (c *Client) CreateIssue(ctx context.Context, instance domain.MulticaInstance, input provider.IssueInput, credential *provider.Credential) (provider.IssueResult, error) {
	_ = credential
	if input.ProjectID == "" || strings.TrimSpace(input.Title) == "" {
		return provider.IssueResult{}, fmt.Errorf("%w: issue project and title are required", domain.ErrInvalid)
	}
	status := firstNonEmpty(input.Status, "backlog")
	args := []string{"issue", "create", "--project", input.ProjectID, "--title", input.Title, "--status", status, "--description-stdin", "--output", "json"}
	if input.Assignee != "" {
		args = append(args, "--assignee", input.Assignee)
	}
	if input.IdempotencyKey != "" {
		args = append(args, "--metadata", "specwire_idempotency_key="+input.IdempotencyKey)
	}
	result, err := c.runWithInput(ctx, instance, "", strings.NewReader(input.Description), args...)
	if err != nil {
		return provider.IssueResult{}, err
	}
	item, err := objectValue(result.Stdout, "issue")
	if err != nil {
		return provider.IssueResult{}, c.invalid("create issue", result, err)
	}
	issueID := firstMapString(item, "id", "uuid", "issue_id")
	if issueID == "" {
		return provider.IssueResult{}, c.invalid("create issue", result, errors.New("response did not contain issue ID"))
	}
	return provider.IssueResult{IssueID: issueID, ProjectID: firstNonEmpty(firstMapString(item, "project_id"), input.ProjectID), Created: true, RequestID: result.RequestID}, nil
}

func (c *Client) SetIssueStatus(ctx context.Context, instance domain.MulticaInstance, issueID, status string, credential *provider.Credential) (provider.IssueStatusResult, error) {
	_ = credential
	if issueID == "" || strings.TrimSpace(status) == "" {
		return provider.IssueStatusResult{}, fmt.Errorf("%w: issue ID and status are required", domain.ErrInvalid)
	}
	result, err := c.run(ctx, instance, "", "issue", "status", issueID, status, "--output", "json")
	if err != nil {
		return provider.IssueStatusResult{}, err
	}
	return provider.IssueStatusResult{IssueID: issueID, Status: status, RequestID: result.RequestID}, nil
}

func (c *Client) ProbeReadiness(ctx context.Context, instance domain.MulticaInstance) (provider.ReadinessResult, error) {
	result, err := c.run(ctx, instance, "", "workspace", "list", "--output", "json")
	if err != nil {
		return provider.ReadinessResult{}, err
	}
	if _, err := objectList(result.Stdout, "workspaces", "items"); err != nil {
		return provider.ReadinessResult{}, c.invalid("probe readiness", result, err)
	}
	return provider.ReadinessResult{Ready: true, RequestID: result.RequestID}, nil
}

func (c *Client) run(ctx context.Context, instance domain.MulticaInstance, workspaceID string, args ...string) (CommandResult, error) {
	return c.runWithInput(ctx, instance, workspaceID, nil, args...)
}

func (c *Client) runWithInput(ctx context.Context, instance domain.MulticaInstance, workspaceID string, stdin io.Reader, args ...string) (CommandResult, error) {
	if c.runner == nil {
		return CommandResult{}, &provider.ProviderError{Provider: domain.ProviderMultica, Operation: strings.Join(args, " "), Category: provider.ErrorUnauthorized, Err: provider.ErrNotConfigured}
	}
	commandContext := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		commandContext, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	base := []string{"--profile", c.profile}
	if strings.TrimSpace(instance.BaseURL) != "" {
		base = append(base, "--server-url", strings.TrimSpace(instance.BaseURL))
	}
	if strings.TrimSpace(workspaceID) != "" {
		base = append(base, "--workspace-id", workspaceID)
	}
	base = append(base, args...)
	result, err := c.runner.Run(commandContext, base, stdin)
	if err == nil {
		return result, nil
	}
	category := provider.ErrorInvalidResponse
	if errors.Is(commandContext.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		category = provider.ErrorTimeout
	} else if commandErr := new(CommandError); errors.As(err, &commandErr) {
		stderr := strings.ToLower(commandErr.Stderr)
		switch {
		case strings.Contains(stderr, "unauthorized"), strings.Contains(stderr, "not authenticated"), strings.Contains(stderr, "login"):
			category = provider.ErrorUnauthorized
		case strings.Contains(stderr, "forbidden"), strings.Contains(stderr, "permission"):
			category = provider.ErrorForbidden
		case strings.Contains(stderr, "not found"):
			category = provider.ErrorNotFound
		case strings.Contains(stderr, "already exists"), strings.Contains(stderr, "duplicate"):
			category = provider.ErrorConflict
		case strings.Contains(stderr, "timeout"), strings.Contains(stderr, "temporarily"):
			category = provider.ErrorTransient
		}
	}
	return result, &provider.ProviderError{Provider: domain.ProviderMultica, Operation: strings.Join(args, " "), Category: category, RequestID: result.RequestID, Err: err}
}

func (c *Client) invalid(operation string, result CommandResult, err error) error {
	return &provider.ProviderError{Provider: domain.ProviderMultica, Operation: operation, Category: provider.ErrorInvalidResponse, RequestID: result.RequestID, Err: err}
}

func objectList(data []byte, keys ...string) ([]map[string]any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if list, ok := value.([]any); ok {
		return objects(list)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("expected JSON array or object")
	}
	for _, key := range keys {
		if candidate, ok := object[key]; ok {
			if list, ok := candidate.([]any); ok {
				return objects(list)
			}
		}
	}
	// A single object is a useful response for create, but not for list.  Keep
	// the error explicit so a changed CLI contract cannot silently look empty.
	return nil, errors.New("JSON response did not contain an array")
}

func objectValue(data []byte, keys ...string) (map[string]any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range keys {
			if nested, ok := object[key].(map[string]any); ok {
				return nested, nil
			}
		}
		return object, nil
	}
	return nil, errors.New("JSON response did not contain an object")
}

func objects(items []any) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("JSON array contained a non-object")
		}
		result = append(result, object)
	}
	return result, nil
}

func mapProject(instanceID domain.ID, workspaceID string, item map[string]any) provider.MulticaProject {
	return provider.MulticaProject{InstanceID: instanceID, WorkspaceID: workspaceID, ExternalID: firstMapString(item, "id", "uuid", "project_id"), Title: firstMapString(item, "title", "name"), WebURL: firstMapString(item, "web_url", "url")}
}

func firstMapString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return strings.TrimSpace(typed)
				}
			case json.Number:
				return typed.String()
			case float64:
				return strconv.FormatInt(int64(typed), 10)
			}
		}
	}
	return ""
}

func sameURL(left, right string) bool {
	return strings.TrimRight(strings.TrimSpace(left), "/") == strings.TrimRight(strings.TrimSpace(right), "/")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func commandError(err error, stderr string) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &CommandError{ExitCode: exitErr.ExitCode(), Stderr: stderr, Err: err}
	}
	return &CommandError{ExitCode: -1, Stderr: stderr, Err: err}
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
