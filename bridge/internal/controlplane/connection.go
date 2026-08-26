package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/provider"
)

type ConnectionStore interface {
	CreateConnection(context.Context, domain.Connection) error
	GetConnection(context.Context, domain.ID, domain.ID) (domain.Connection, error)
	FindActiveConnectionBySource(context.Context, domain.ID, domain.ID, string) (domain.Connection, error)
	FindActiveConnectionByTarget(context.Context, domain.ID, domain.ID, string) (domain.Connection, error)
	UpdateConnectionStatus(context.Context, domain.ID, domain.ID, domain.ConnectionStatus, *time.Time) error
	EnsureManagedResource(context.Context, domain.ManagedResource) (domain.ManagedResource, error)
	ListManagedResources(context.Context, domain.ID, domain.ID) ([]domain.ManagedResource, error)
	CreateOnboardingOperation(context.Context, domain.OnboardingOperation) error
	GetOnboardingOperation(context.Context, domain.ID, domain.ID) (domain.OnboardingOperation, error)
	UpdateOnboardingOperation(context.Context, domain.ID, domain.ID, domain.ID, domain.OnboardingStatus, string, string) error
	UpsertOnboardingCheckpoint(context.Context, domain.OnboardingCheckpoint) error
	GetOnboardingCheckpoint(context.Context, domain.ID, domain.ID, string) (domain.OnboardingCheckpoint, error)
}

type CredentialResolver interface {
	Resolve(context.Context, domain.SecretRef) ([]byte, error)
}

type ConnectionService struct {
	store         ConnectionStore
	gitlab        provider.GitLab
	multica       provider.Multica
	vault         CredentialResolver
	publicHookURL string
	now           func() time.Time
}

func NewConnectionService(store ConnectionStore, gitlab provider.GitLab, multica provider.Multica, vault CredentialResolver) (*ConnectionService, error) {
	if store == nil || gitlab == nil || multica == nil {
		return nil, fmt.Errorf("%w: connection service dependencies are required", domain.ErrInvalid)
	}
	return &ConnectionService{store: store, gitlab: gitlab, multica: multica, vault: vault, publicHookURL: "http://host.docker.internal:8787/gitlab/specwire", now: time.Now}, nil
}

func (s *ConnectionService) SetPublicHookURL(value string) {
	if strings.TrimSpace(value) != "" {
		s.publicHookURL = strings.TrimSpace(value)
	}
}

func (s *ConnectionService) PublicHookURL() string { return s.publicHookURL }

func (s *ConnectionService) ListGitLabGroups(ctx context.Context, instance domain.GitLabInstance, query string, credentialRef *domain.SecretRef) ([]provider.GitLabGroup, error) {
	credential, cleanup, err := s.resolveCredential(ctx, credentialRef)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return s.gitlab.ListGroups(ctx, instance, query, credential)
}

func (s *ConnectionService) ListGitLabProjects(ctx context.Context, instance domain.GitLabInstance, group provider.GitLabGroup, query string, credentialRef *domain.SecretRef) ([]provider.GitLabProject, error) {
	credential, cleanup, err := s.resolveCredential(ctx, credentialRef)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return s.gitlab.ListProjects(ctx, instance, group, query, credential)
}

func (s *ConnectionService) ListMulticaWorkspaces(ctx context.Context, instance domain.MulticaInstance, query string, credentialRef *domain.SecretRef) ([]provider.MulticaWorkspace, error) {
	credential, cleanup, err := s.resolveCredential(ctx, credentialRef)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return s.multica.ListWorkspaces(ctx, instance, query, credential)
}

func (s *ConnectionService) ListMulticaProjects(ctx context.Context, instance domain.MulticaInstance, workspace provider.MulticaWorkspace, query string, credentialRef *domain.SecretRef) ([]provider.MulticaProject, error) {
	credential, cleanup, err := s.resolveCredential(ctx, credentialRef)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return s.multica.ListProjects(ctx, instance, workspace, query, credential)
}

type OnboardingRequest struct {
	OperationID             domain.ID
	ActorID                 domain.ID
	WorkspaceID             domain.ID
	SourceGitLabInstance    domain.GitLabInstance
	SourceProjectExternalID string
	Group                   domain.GitLabGroupBinding
	GitLabCredentialRef     *domain.SecretRef
	TargetMulticaInstance   domain.MulticaInstance
	TargetWorkspace         provider.MulticaWorkspace
	TargetProject           *provider.MulticaProject
	CreateTargetProject     bool
	TargetProjectTitle      string
	MulticaCredentialRef    *domain.SecretRef
	PreferSSH               bool
	// PreferHTTPS is an explicit opt-out from the SSH-first default.  The
	// boolean PreferSSH is retained for callers that already set it directly.
	PreferHTTPS   bool
	PublicHookURL string
}

type OnboardingResult struct {
	Operation  domain.OnboardingOperation
	Connection domain.Connection
	Resources  []domain.ManagedResource
	HookPlan   map[string]any
	Ready      bool
}

// ResourceDeprovisionCheck makes the destructive boundary explicit.  The MVP
// can identify resources that SpecWire created, but it intentionally does not
// delete provider objects as part of unbinding a Connection.
type ResourceDeprovisionCheck struct {
	Resource          domain.ManagedResource `json:"resource"`
	EligibleForManual bool                   `json:"eligible_for_manual_deprovision"`
	ExternalDeletion  bool                   `json:"external_deletion"`
	Action            string                 `json:"action"`
	Reason            string                 `json:"reason,omitempty"`
}

type DeprovisionPlan struct {
	WorkspaceID             domain.ID                  `json:"workspace_id"`
	ConnectionID            domain.ID                  `json:"connection_id"`
	Connection              domain.Connection          `json:"connection"`
	RequiresConfirmation    bool                       `json:"requires_confirmation"`
	ExternalDeletionPlanned bool                       `json:"external_deletion_planned"`
	HistoryRetained         bool                       `json:"history_retained"`
	Checks                  []ResourceDeprovisionCheck `json:"checks"`
}

// DeprovisionCheck is read-only.  It is used both for the preview and after a
// confirmed unbind so the API never implies that an external provider object
// was deleted when only the SpecWire binding was disabled.
func (s *ConnectionService) DeprovisionCheck(ctx context.Context, workspaceID, connectionID domain.ID) (DeprovisionPlan, error) {
	connection, err := s.store.GetConnection(ctx, workspaceID, connectionID)
	if err != nil {
		return DeprovisionPlan{}, err
	}
	resources, err := s.store.ListManagedResources(ctx, workspaceID, connectionID)
	if err != nil {
		return DeprovisionPlan{}, err
	}
	return BuildDeprovisionPlan(connection, resources), nil
}

// BuildDeprovisionPlan contains the policy-only part of DeprovisionCheck so
// transports can render a preview even when provider clients are not wired in
// (for example, a read-only API test or a control-plane maintenance process).
func BuildDeprovisionPlan(connection domain.Connection, resources []domain.ManagedResource) DeprovisionPlan {
	checks := make([]ResourceDeprovisionCheck, 0, len(resources))
	for _, resource := range resources {
		check := ResourceDeprovisionCheck{Resource: resource, ExternalDeletion: false, Action: "retain"}
		switch {
		case resource.Ownership == domain.OwnershipManaged && resource.ManagementMark == "specwire-managed":
			check.EligibleForManual = true
			check.Action = "manual-provider-deprovision-eligible"
			check.Reason = "SpecWire created this resource, but MVP unbind never deletes provider objects"
		case resource.Ownership == domain.OwnershipAdopted:
			check.Reason = "adopted provider resource is retained"
		default:
			check.Reason = "external or unmarked provider resource is retained"
		}
		checks = append(checks, check)
	}
	return DeprovisionPlan{
		WorkspaceID:             connection.WorkspaceID,
		ConnectionID:            connection.ID,
		Connection:              connection,
		RequiresConfirmation:    connection.Status != domain.ConnectionDisabled,
		ExternalDeletionPlanned: false,
		HistoryRetained:         true,
		Checks:                  checks,
	}
}

func (s *ConnectionService) Onboard(ctx context.Context, request OnboardingRequest) (OnboardingResult, error) {
	if err := request.validate(); err != nil {
		return OnboardingResult{}, err
	}
	operationID := request.OperationID
	if operationID.Empty() {
		operationID = domain.NewID()
	}
	operation := domain.OnboardingOperation{ID: operationID, WorkspaceID: request.WorkspaceID, Status: domain.OnboardingRunning, Request: requestSummary(request), CreatedAt: s.now().UTC(), UpdatedAt: s.now().UTC()}
	if err := s.store.CreateOnboardingOperation(ctx, operation); err != nil {
		if !errors.Is(err, domain.ErrConflict) {
			return OnboardingResult{}, err
		}
		existing, getErr := s.store.GetOnboardingOperation(ctx, request.WorkspaceID, operationID)
		if getErr != nil {
			return OnboardingResult{}, getErr
		}
		operation = existing
	}
	fail := func(err error, status domain.OnboardingStatus) (OnboardingResult, error) {
		category := "internal"
		var pe *provider.ProviderError
		if errors.As(err, &pe) {
			category = string(pe.Category)
		}
		message := safeMessage(err)
		_ = s.store.UpdateOnboardingOperation(ctx, request.WorkspaceID, operationID, operation.ConnectionID, status, category, message)
		operation.Status = status
		operation.ErrorCategory = category
		operation.ErrorMessage = message
		operation.UpdatedAt = s.now().UTC()
		return OnboardingResult{Operation: operation}, err
	}
	var connection domain.Connection
	var targetProject provider.MulticaProject
	var err error
	if !operation.ConnectionID.Empty() {
		connection, err = s.store.GetConnection(ctx, request.WorkspaceID, operation.ConnectionID)
		if err != nil {
			return fail(err, domain.OnboardingFailed)
		}
		targetProject = provider.MulticaProject{InstanceID: connection.TargetMulticaProject.InstanceID, ExternalID: connection.TargetMulticaProject.ExternalID, Title: connection.TargetMulticaProject.Name, WebURL: connection.TargetMulticaProject.WebURL, WorkspaceID: request.TargetWorkspace.ExternalID}
	} else {
		if _, ok, err := s.completedCheckpoint(ctx, request.WorkspaceID, operationID, "source_project"); err != nil {
			return fail(err, domain.OnboardingFailed)
		} else if !ok {
			if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "source_project", "succeeded", request.SourceProjectExternalID, map[string]any{"external_id": request.SourceProjectExternalID}); err != nil {
				return fail(err, domain.OnboardingFailed)
			}
		}
		existing, lookupErr := s.store.FindActiveConnectionBySource(ctx, request.WorkspaceID, request.SourceGitLabInstance.ID, request.SourceProjectExternalID)
		if lookupErr == nil {
			expectedTargetID := ""
			if request.TargetProject != nil {
				expectedTargetID = request.TargetProject.ExternalID
			} else if checkpoint, ok, checkpointErr := s.completedCheckpoint(ctx, request.WorkspaceID, operationID, "target_project"); checkpointErr != nil {
				return fail(checkpointErr, domain.OnboardingFailed)
			} else if ok {
				expectedTargetID = firstMapString(checkpoint.Result, checkpoint.ProviderID, "external_id")
			}
			if expectedTargetID == "" || existing.TargetMulticaProject.InstanceID != request.TargetMulticaInstance.ID || existing.TargetMulticaProject.ExternalID != expectedTargetID {
				return fail(fmt.Errorf("%w: source project is already bound to another target", domain.ErrConflict), domain.OnboardingBlocked)
			}
			connection = existing
			targetProject = provider.MulticaProject{InstanceID: existing.TargetMulticaProject.InstanceID, ExternalID: existing.TargetMulticaProject.ExternalID, Title: existing.TargetMulticaProject.Name, WebURL: existing.TargetMulticaProject.WebURL, WorkspaceID: request.TargetWorkspace.ExternalID}
			operation.ConnectionID = existing.ID
			if err := s.store.UpdateOnboardingOperation(ctx, request.WorkspaceID, operationID, existing.ID, domain.OnboardingRunning, "", ""); err != nil {
				return fail(err, domain.OnboardingFailed)
			}
			if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "connection", "succeeded", string(existing.ID), map[string]any{"connection_id": existing.ID, "adopted": true}); err != nil {
				return fail(err, domain.OnboardingFailed)
			}
		} else if !errors.Is(lookupErr, domain.ErrNotFound) {
			return fail(lookupErr, domain.OnboardingFailed)
		}
		if connection.ID.Empty() && request.TargetProject != nil {
			if _, err := s.store.FindActiveConnectionByTarget(ctx, request.WorkspaceID, request.TargetMulticaInstance.ID, request.TargetProject.ExternalID); err == nil {
				return fail(fmt.Errorf("%w: target project is already bound", domain.ErrConflict), domain.OnboardingBlocked)
			} else if !errors.Is(err, domain.ErrNotFound) {
				return fail(err, domain.OnboardingFailed)
			}
		}
		var sourceErr error
		sourceProject, sourceErr := s.loadSourceProject(ctx, request)
		if sourceErr != nil {
			return fail(sourceErr, domain.OnboardingFailed)
		}
		targetProject, err = s.ensureTargetProject(ctx, request, sourceProject, operationID)
		if err != nil {
			return fail(err, domain.OnboardingFailed)
		}
		if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "target_project", "succeeded", targetProject.ExternalID, map[string]any{
			"external_id": targetProject.ExternalID, "workspace_id": targetProject.WorkspaceID,
			"title": targetProject.Title, "web_url": targetProject.WebURL,
		}); err != nil {
			return fail(err, domain.OnboardingFailed)
		}
		if connection.ID.Empty() {
			connection = domain.Connection{ID: domain.NewID(), WorkspaceID: request.WorkspaceID, Name: sourceProject.FullPath, SourceGitLabProject: domain.ProviderProjectRef{InstanceID: sourceProject.InstanceID, ExternalID: sourceProject.ExternalID, GroupID: sourceProject.GroupID, FullPath: sourceProject.FullPath, Name: sourceProject.Name, WebURL: sourceProject.WebURL, SSHURL: sourceProject.SSHURL, HTTPSURL: sourceProject.HTTPSURL}, TargetMulticaProject: domain.ProviderProjectRef{InstanceID: targetProject.InstanceID, ExternalID: targetProject.ExternalID, Name: targetProject.Title, WebURL: targetProject.WebURL}, Status: domain.ConnectionConfigured, ConfiguredAt: s.now().UTC()}
			if err := s.store.CreateConnection(ctx, connection); err != nil {
				return fail(err, domain.OnboardingBlocked)
			}
			operation.ConnectionID = connection.ID
			if err := s.store.UpdateOnboardingOperation(ctx, request.WorkspaceID, operationID, connection.ID, domain.OnboardingRunning, "", ""); err != nil {
				return fail(err, domain.OnboardingFailed)
			}
			if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "connection", "succeeded", string(connection.ID), map[string]any{"connection_id": connection.ID}); err != nil {
				return fail(err, domain.OnboardingFailed)
			}
		}
	}

	sourceProject, err := s.loadSourceProject(ctx, request)
	if err != nil {
		return fail(err, domain.OnboardingFailed)
	}

	resources := make([]domain.ManagedResource, 0, 3)
	gitlabRef := requestGitLabCredentialRef(request)
	gitlabCredential, gitlabCleanup, err := s.resolveCredential(ctx, gitlabRef)
	if err != nil {
		return fail(err, domain.OnboardingBlocked)
	}
	defer gitlabCleanup()
	var label provider.LabelResult
	if checkpoint, ok, checkpointErr := s.completedCheckpoint(ctx, request.WorkspaceID, operationID, "gitlab_label"); checkpointErr != nil {
		return fail(checkpointErr, domain.OnboardingFailed)
	} else if ok {
		label = labelFromCheckpoint(checkpoint)
	} else {
		label, err = s.gitlab.EnsureLabel(ctx, request.SourceGitLabInstance, sourceProject, "change", gitlabCredential)
		if err != nil {
			return fail(err, domain.OnboardingFailed)
		}
		s.recordProviderEffect(ctx, request, "provider.gitlab.label.ensure", "connection", connection.ID, map[string]any{
			"provider": "gitlab", "operation": "ensure_label", "external_id": label.ExternalID, "request_id": label.RequestID, "created": label.Created, "adopted": label.Adopted,
		})
	}
	labelResource, err := s.store.EnsureManagedResource(ctx, domain.ManagedResource{ID: domain.NewID(), WorkspaceID: request.WorkspaceID, ConnectionID: connection.ID, Kind: domain.ResourceLabel, Provider: domain.ProviderGitLab, InstanceID: request.SourceGitLabInstance.ID, ExternalID: label.ExternalID, Ownership: ownershipFrom(label.Created, label.Adopted), ManagementMark: "specwire-managed", Status: "ready", Snapshot: map[string]any{"title": label.Title, "request_id": label.RequestID}})
	if err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	resources = append(resources, labelResource)
	if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "gitlab_label", "succeeded", label.ExternalID, map[string]any{"external_id": label.ExternalID, "title": label.Title, "created": label.Created, "adopted": label.Adopted, "request_id": label.RequestID}); err != nil {
		return fail(err, domain.OnboardingFailed)
	}

	cloneURL, err := provider.CanonicalCloneURL(sourceProject, request.PreferSSH || !request.PreferHTTPS)
	if err != nil {
		return fail(err, domain.OnboardingBlocked)
	}
	multicaRef := requestMulticaCredentialRef(request)
	multicaCredential, multicaCleanup, err := s.resolveCredential(ctx, multicaRef)
	if err != nil {
		return fail(err, domain.OnboardingBlocked)
	}
	defer multicaCleanup()
	var workspaceResource provider.ResourceResult
	if checkpoint, ok, checkpointErr := s.completedCheckpoint(ctx, request.WorkspaceID, operationID, "multica_workspace_repository"); checkpointErr != nil {
		return fail(checkpointErr, domain.OnboardingFailed)
	} else if ok {
		workspaceResource = resourceResultFromCheckpoint(checkpoint, domain.ResourceWorkspaceRepository)
	} else {
		workspaceResource, err = s.multica.EnsureWorkspaceRepository(ctx, request.TargetMulticaInstance, request.TargetWorkspace, sourceProject, cloneURL, multicaCredential)
		if err != nil {
			return fail(err, domain.OnboardingFailed)
		}
		s.recordProviderEffect(ctx, request, "provider.multica.workspace_repository.ensure", "connection", connection.ID, map[string]any{
			"provider": "multica", "operation": "ensure_workspace_repository", "external_id": workspaceResource.ExternalID, "request_id": workspaceResource.RequestID, "created": workspaceResource.Created, "adopted": workspaceResource.Adopted,
		})
	}
	resource, err := s.store.EnsureManagedResource(ctx, resourceFromResult(request.WorkspaceID, connection.ID, request.TargetMulticaInstance.ID, workspaceResource))
	if err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	resources = append(resources, resource)
	if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "multica_workspace_repository", "succeeded", workspaceResource.ExternalID, map[string]any{"external_id": workspaceResource.ExternalID, "ownership": workspaceResource.Ownership, "created": workspaceResource.Created, "adopted": workspaceResource.Adopted, "request_id": workspaceResource.RequestID, "snapshot": workspaceResource.Snapshot, "clone_url": cloneURL}); err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	var projectResource provider.ResourceResult
	if checkpoint, ok, checkpointErr := s.completedCheckpoint(ctx, request.WorkspaceID, operationID, "multica_project_resource"); checkpointErr != nil {
		return fail(checkpointErr, domain.OnboardingFailed)
	} else if ok {
		projectResource = resourceResultFromCheckpoint(checkpoint, domain.ResourceProject)
	} else {
		projectResource, err = s.multica.EnsureProjectResource(ctx, request.TargetMulticaInstance, targetProject, sourceProject, cloneURL, multicaCredential)
		if err != nil {
			return fail(err, domain.OnboardingFailed)
		}
		s.recordProviderEffect(ctx, request, "provider.multica.project_resource.ensure", "connection", connection.ID, map[string]any{
			"provider": "multica", "operation": "ensure_project_resource", "external_id": projectResource.ExternalID, "request_id": projectResource.RequestID, "created": projectResource.Created, "adopted": projectResource.Adopted,
		})
	}
	resource, err = s.store.EnsureManagedResource(ctx, resourceFromResult(request.WorkspaceID, connection.ID, request.TargetMulticaInstance.ID, projectResource))
	if err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	resources = append(resources, resource)
	if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "multica_project_resource", "succeeded", projectResource.ExternalID, map[string]any{"external_id": projectResource.ExternalID, "ownership": projectResource.Ownership, "created": projectResource.Created, "adopted": projectResource.Adopted, "request_id": projectResource.RequestID, "snapshot": projectResource.Snapshot, "clone_url": cloneURL}); err != nil {
		return fail(err, domain.OnboardingFailed)
	}

	hookURL := request.PublicHookURL
	if strings.TrimSpace(hookURL) == "" {
		hookURL = s.publicHookURL
	}
	hookPlan := map[string]any{"url": hookURL, "events": []string{"Issue Hook", "Push Hook"}, "status": "planned", "management": "created-on-first-published-input-flow"}
	if _, ok, err := s.completedCheckpoint(ctx, request.WorkspaceID, operationID, "hook_plan"); err != nil {
		return fail(err, domain.OnboardingFailed)
	} else if !ok {
		if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "hook_plan", "succeeded", "", hookPlan); err != nil {
			return fail(err, domain.OnboardingFailed)
		}
	}
	ready := false
	readiness, readinessErr := s.multica.ProbeReadiness(ctx, request.TargetMulticaInstance)
	if readinessErr == nil && readiness.Ready {
		ready = true
		now := s.now().UTC()
		_ = s.store.UpdateConnectionStatus(ctx, request.WorkspaceID, connection.ID, domain.ConnectionReady, &now)
		connection.Status = domain.ConnectionReady
		connection.ReadyAt = &now
	} else if readinessErr == nil {
		_ = s.store.UpdateConnectionStatus(ctx, request.WorkspaceID, connection.ID, domain.ConnectionReadyFailed, nil)
		connection.Status = domain.ConnectionReadyFailed
	}
	status := domain.OnboardingConfigured
	if ready {
		status = domain.OnboardingReady
	}
	if err := s.store.UpdateOnboardingOperation(ctx, request.WorkspaceID, operationID, connection.ID, status, "", ""); err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	operation.Status = status
	operation.ConnectionID = connection.ID
	operation.UpdatedAt = s.now().UTC()
	return OnboardingResult{Operation: operation, Connection: connection, Resources: resources, HookPlan: hookPlan, Ready: ready}, nil
}

func (r OnboardingRequest) validate() error {
	if r.WorkspaceID.Empty() || r.SourceGitLabInstance.ID.Empty() || r.TargetMulticaInstance.ID.Empty() || strings.TrimSpace(r.SourceProjectExternalID) == "" || strings.TrimSpace(r.TargetWorkspace.ExternalID) == "" {
		return fmt.Errorf("%w: onboarding workspace, endpoint, project and target workspace are required", domain.ErrInvalid)
	}
	if r.SourceGitLabInstance.WorkspaceID != r.WorkspaceID || r.TargetMulticaInstance.WorkspaceID != r.WorkspaceID {
		return fmt.Errorf("%w: onboarding endpoint belongs to another workspace", domain.ErrForbidden)
	}
	if r.TargetProject == nil && !r.CreateTargetProject {
		return fmt.Errorf("%w: select an existing target project or confirm project creation", domain.ErrInvalid)
	}
	if r.TargetProject != nil && r.TargetProject.InstanceID != r.TargetMulticaInstance.ID {
		return fmt.Errorf("%w: target project belongs to another Multica instance", domain.ErrForbidden)
	}
	return nil
}

func (s *ConnectionService) loadSourceProject(ctx context.Context, request OnboardingRequest) (provider.GitLabProject, error) {
	credential, cleanup, err := s.resolveCredential(ctx, requestGitLabCredentialRef(request))
	if err != nil {
		return provider.GitLabProject{}, err
	}
	defer cleanup()
	return s.gitlab.GetProject(ctx, request.SourceGitLabInstance, request.SourceProjectExternalID, credential)
}

func (s *ConnectionService) ensureTargetProject(ctx context.Context, request OnboardingRequest, source provider.GitLabProject, operationID domain.ID) (provider.MulticaProject, error) {
	if request.TargetProject != nil {
		return *request.TargetProject, nil
	}
	if checkpoint, ok, err := s.completedCheckpoint(ctx, request.WorkspaceID, operationID, "target_project"); err != nil {
		return provider.MulticaProject{}, err
	} else if ok {
		return provider.MulticaProject{
			InstanceID:  request.TargetMulticaInstance.ID,
			WorkspaceID: firstMapString(checkpoint.Result, "workspace_id"),
			ExternalID:  firstMapString(checkpoint.Result, checkpoint.ProviderID, "external_id"),
			Title:       firstMapString(checkpoint.Result, "title"),
			WebURL:      firstMapString(checkpoint.Result, "web_url"),
		}, nil
	}
	title := strings.TrimSpace(request.TargetProjectTitle)
	if title == "" {
		title = source.FullPath
	}
	credential, cleanup, err := s.resolveCredential(ctx, requestMulticaCredentialRef(request))
	if err != nil {
		return provider.MulticaProject{}, err
	}
	defer cleanup()
	projects, err := s.multica.ListProjects(ctx, request.TargetMulticaInstance, request.TargetWorkspace, title, credential)
	if err != nil {
		return provider.MulticaProject{}, err
	}
	for _, project := range projects {
		if strings.EqualFold(project.Title, title) {
			return provider.MulticaProject{}, fmt.Errorf("%w: Multica project title %q already exists; choose it explicitly", domain.ErrConflict, title)
		}
	}
	project, err := s.multica.CreateProject(ctx, request.TargetMulticaInstance, provider.CreateProjectInput{InstanceID: request.TargetMulticaInstance.ID, WorkspaceID: request.TargetWorkspace.ExternalID, Title: title, IdempotencyKey: string(operationID) + ":project"}, credential)
	if err != nil {
		return provider.MulticaProject{}, err
	}
	s.recordProviderEffect(ctx, request, "provider.multica.project.create", "multica_project", domain.ID(project.ExternalID), map[string]any{
		"provider": "multica", "operation": "create_project", "external_id": project.ExternalID, "workspace_id": project.WorkspaceID, "title": project.Title, "idempotency_key": string(operationID) + ":project",
	})
	return project, nil
}

func requestGitLabCredentialRef(request OnboardingRequest) *domain.SecretRef {
	if request.GitLabCredentialRef != nil {
		return request.GitLabCredentialRef
	}
	if request.Group.CredentialRef != nil {
		return request.Group.CredentialRef
	}
	return request.SourceGitLabInstance.CredentialRef
}

func requestMulticaCredentialRef(request OnboardingRequest) *domain.SecretRef {
	if request.MulticaCredentialRef != nil {
		return request.MulticaCredentialRef
	}
	return request.TargetMulticaInstance.ManagementCredentialRef
}

func (s *ConnectionService) resolveCredential(ctx context.Context, ref *domain.SecretRef) (*provider.Credential, func(), error) {
	if ref == nil {
		return nil, func() {}, nil
	}
	if s.vault == nil {
		return nil, func() {}, fmt.Errorf("%w: credential resolver is not configured", domain.ErrInvalid)
	}
	material, err := s.vault.Resolve(ctx, *ref)
	if err != nil {
		return nil, func() {}, err
	}
	credential := &provider.Credential{Ref: *ref, Material: material}
	return credential, func() { clearBytes(material) }, nil
}

func (s *ConnectionService) checkpoint(ctx context.Context, workspaceID, operationID domain.ID, step, status, providerID string, result map[string]any) error {
	return s.store.UpsertOnboardingCheckpoint(ctx, domain.OnboardingCheckpoint{ID: domain.NewID(), WorkspaceID: workspaceID, OperationID: operationID, Step: step, Status: status, ProviderID: providerID, Result: result, UpdatedAt: s.now().UTC()})
}

func (s *ConnectionService) completedCheckpoint(ctx context.Context, workspaceID, operationID domain.ID, step string) (domain.OnboardingCheckpoint, bool, error) {
	checkpoint, err := s.store.GetOnboardingCheckpoint(ctx, workspaceID, operationID, step)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.OnboardingCheckpoint{}, false, nil
	}
	if err != nil {
		return domain.OnboardingCheckpoint{}, false, err
	}
	return checkpoint, checkpoint.Status == "succeeded", nil
}

func labelFromCheckpoint(checkpoint domain.OnboardingCheckpoint) provider.LabelResult {
	return provider.LabelResult{
		ExternalID: firstMapString(checkpoint.Result, checkpoint.ProviderID, "external_id"),
		Title:      firstMapString(checkpoint.Result, "title"),
		Created:    boolValue(checkpoint.Result["created"]),
		Adopted:    boolValue(checkpoint.Result["adopted"]),
		RequestID:  firstMapString(checkpoint.Result, "request_id"),
	}
}

func resourceResultFromCheckpoint(checkpoint domain.OnboardingCheckpoint, kind domain.ResourceKind) provider.ResourceResult {
	return provider.ResourceResult{
		Kind:       kind,
		ExternalID: firstMapString(checkpoint.Result, checkpoint.ProviderID, "external_id"),
		Ownership:  domain.Ownership(firstMapString(checkpoint.Result, "ownership")),
		Created:    boolValue(checkpoint.Result["created"]),
		Adopted:    boolValue(checkpoint.Result["adopted"]),
		RequestID:  firstMapString(checkpoint.Result, "request_id"),
		Snapshot:   mapValue(checkpoint.Result["snapshot"]),
	}
}

func firstMapString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func boolValue(value any) bool {
	boolean, ok := value.(bool)
	return ok && boolean
}

func mapValue(value any) map[string]any {
	if value, ok := value.(map[string]any); ok {
		return value
	}
	return map[string]any{}
}

func requestSummary(request OnboardingRequest) map[string]any {
	return map[string]any{"source_instance_id": request.SourceGitLabInstance.ID, "source_project_external_id": request.SourceProjectExternalID, "target_instance_id": request.TargetMulticaInstance.ID, "target_workspace_external_id": request.TargetWorkspace.ExternalID, "target_project_external_id": func() string {
		if request.TargetProject != nil {
			return request.TargetProject.ExternalID
		}
		return ""
	}(), "create_target_project": request.CreateTargetProject, "prefer_ssh": request.PreferSSH || !request.PreferHTTPS, "public_hook_url": request.PublicHookURL}
}

func ownershipFrom(created, adopted bool) domain.Ownership {
	if adopted {
		return domain.OwnershipAdopted
	}
	if created {
		return domain.OwnershipManaged
	}
	return domain.OwnershipExternal
}

func resourceFromResult(workspaceID, connectionID, instanceID domain.ID, result provider.ResourceResult) domain.ManagedResource {
	return domain.ManagedResource{ID: domain.NewID(), WorkspaceID: workspaceID, ConnectionID: connectionID, Kind: result.Kind, Provider: domain.ProviderMultica, InstanceID: instanceID, ExternalID: result.ExternalID, Ownership: result.Ownership, ManagementMark: func() string {
		if result.Created {
			return "specwire-managed"
		}
		return ""
	}(), Status: "ready", Snapshot: result.Snapshot}
}

func safeMessage(err error) string {
	message := err.Error()
	if len(message) > 500 {
		return message[:500]
	}
	return message
}

func (s *ConnectionService) recordProviderEffect(ctx context.Context, request OnboardingRequest, action, entityType string, entityID domain.ID, payload map[string]any) {
	if entityID.Empty() {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["workspace_id"] = request.WorkspaceID
	recordAudit(ctx, s.store, domain.AuditEvent{ID: domain.NewID(), WorkspaceID: request.WorkspaceID, ActorAccountID: request.ActorID, Action: action, EntityType: entityType, EntityID: entityID, Payload: payload})
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
