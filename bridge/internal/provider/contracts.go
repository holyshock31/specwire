package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"specwire/bridge/internal/domain"
)

type ErrorCategory string

const (
	ErrorUnauthorized    ErrorCategory = "unauthorized"
	ErrorForbidden       ErrorCategory = "forbidden"
	ErrorNotFound        ErrorCategory = "not-found"
	ErrorConflict        ErrorCategory = "conflict"
	ErrorRateLimited     ErrorCategory = "rate-limited"
	ErrorTimeout         ErrorCategory = "timeout"
	ErrorInvalidResponse ErrorCategory = "invalid-response"
	ErrorIndeterminate   ErrorCategory = "indeterminate"
	ErrorTransient       ErrorCategory = "transient"
)

var ErrNotConfigured = errors.New("provider adapter not configured")

type ProviderError struct {
	Provider  domain.ProviderKind
	Operation string
	Category  ErrorCategory
	RequestID string
	Err       error
}

func (e *ProviderError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s %s: %s", e.Provider, e.Operation, e.Category)
	}
	return fmt.Sprintf("%s %s: %s: %v", e.Provider, e.Operation, e.Category, e.Err)
}
func (e *ProviderError) Unwrap() error { return e.Err }
func (e *ProviderError) Retryable() bool {
	return e.Category == ErrorTransient || e.Category == ErrorRateLimited || e.Category == ErrorTimeout
}

type Credential struct {
	Ref      domain.SecretRef
	Material []byte
}

func (c Credential) Validate() error {
	if err := c.Ref.Validate(); err != nil {
		return err
	}
	if len(c.Material) == 0 {
		return fmt.Errorf("%w: provider credential material is required", domain.ErrInvalid)
	}
	return nil
}

type GitLabGroup struct {
	InstanceID       domain.ID `json:"instance_id"`
	ExternalID       string    `json:"external_id"`
	FullPath         string    `json:"full_path"`
	Name             string    `json:"name"`
	ParentExternalID string    `json:"parent_external_id,omitempty"`
}

type GitLabProject struct {
	InstanceID domain.ID `json:"instance_id"`
	ExternalID string    `json:"external_id"`
	GroupID    string    `json:"group_id,omitempty"`
	FullPath   string    `json:"full_path"`
	Name       string    `json:"name"`
	WebURL     string    `json:"web_url"`
	SSHURL     string    `json:"ssh_url"`
	HTTPSURL   string    `json:"https_url"`
}

type LabelResult struct {
	ExternalID string `json:"external_id"`
	Title      string `json:"title"`
	Created    bool   `json:"created"`
	Adopted    bool   `json:"adopted"`
	RequestID  string `json:"request_id,omitempty"`
}

type HookSpec struct {
	URL        string
	Events     []string
	SigningRef domain.SecretRef
	// SigningToken is ephemeral material supplied only for the provider call;
	// it is never persisted as part of a HookSpec or flow definition.
	SigningToken   []byte `json:"-"`
	ManagementMark string
}

type HookResult struct {
	ExternalID string `json:"external_id"`
	Created    bool   `json:"created"`
	Adopted    bool   `json:"adopted"`
	RequestID  string `json:"request_id,omitempty"`
}

type MulticaWorkspace struct {
	InstanceID domain.ID `json:"instance_id"`
	ExternalID string    `json:"external_id"`
	Name       string    `json:"name"`
}

type MulticaProject struct {
	InstanceID  domain.ID `json:"instance_id"`
	WorkspaceID string    `json:"workspace_id"`
	ExternalID  string    `json:"external_id"`
	Title       string    `json:"title"`
	WebURL      string    `json:"web_url,omitempty"`
}

type ResourceResult struct {
	Kind       domain.ResourceKind `json:"kind"`
	ExternalID string              `json:"external_id"`
	Created    bool                `json:"created"`
	Adopted    bool                `json:"adopted"`
	Ownership  domain.Ownership    `json:"ownership"`
	RequestID  string              `json:"request_id,omitempty"`
	Snapshot   map[string]any      `json:"snapshot,omitempty"`
}

type CreateProjectInput struct {
	InstanceID     domain.ID
	WorkspaceID    string
	Title          string
	Description    string
	IdempotencyKey string
}

type IssueInput struct {
	ProjectID      string
	Title          string
	Description    string
	Status         string
	Assignee       string
	IdempotencyKey string
}

type IssueResult struct {
	IssueID   string
	ProjectID string
	Created   bool
	Adopted   bool
	RequestID string
}

type IssueStatusResult struct {
	IssueID   string
	Status    string
	RequestID string
}

type ReadinessResult struct {
	Ready     bool
	Reason    string
	RequestID string
}

type GitLab interface {
	ListGroups(context.Context, domain.GitLabInstance, string, *Credential) ([]GitLabGroup, error)
	ListProjects(context.Context, domain.GitLabInstance, GitLabGroup, string, *Credential) ([]GitLabProject, error)
	GetProject(context.Context, domain.GitLabInstance, string, *Credential) (GitLabProject, error)
	EnsureLabel(context.Context, domain.GitLabInstance, GitLabProject, string, *Credential) (LabelResult, error)
	EnsureHook(context.Context, domain.GitLabInstance, GitLabProject, HookSpec, *Credential) (HookResult, error)
	NoteIssue(context.Context, domain.GitLabInstance, GitLabProject, int, string, *Credential) error
	CloseIssue(context.Context, domain.GitLabInstance, GitLabProject, int, *Credential) error
}

type Multica interface {
	ListWorkspaces(context.Context, domain.MulticaInstance, string, *Credential) ([]MulticaWorkspace, error)
	ListProjects(context.Context, domain.MulticaInstance, MulticaWorkspace, string, *Credential) ([]MulticaProject, error)
	CreateProject(context.Context, domain.MulticaInstance, CreateProjectInput, *Credential) (MulticaProject, error)
	EnsureWorkspaceRepository(context.Context, domain.MulticaInstance, MulticaWorkspace, GitLabProject, string, *Credential) (ResourceResult, error)
	EnsureProjectResource(context.Context, domain.MulticaInstance, MulticaProject, GitLabProject, string, *Credential) (ResourceResult, error)
	CreateIssue(context.Context, domain.MulticaInstance, IssueInput, *Credential) (IssueResult, error)
	SetIssueStatus(context.Context, domain.MulticaInstance, string, string, *Credential) (IssueStatusResult, error)
	ProbeReadiness(context.Context, domain.MulticaInstance) (ReadinessResult, error)
}

func CanonicalCloneURL(project GitLabProject, preferSSH bool) (string, error) {
	if preferSSH && strings.TrimSpace(project.SSHURL) != "" {
		return project.SSHURL, nil
	}
	if strings.TrimSpace(project.HTTPSURL) != "" {
		return project.HTTPSURL, nil
	}
	if strings.TrimSpace(project.SSHURL) != "" {
		return project.SSHURL, nil
	}
	return "", fmt.Errorf("%w: GitLab project has no clone URL", domain.ErrInvalid)
}

func IsRetryable(err error) bool {
	var providerErr *ProviderError
	return errors.As(err, &providerErr) && providerErr.Retryable()
}
