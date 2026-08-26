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
	PublicHookURL           string
}

type OnboardingResult struct {
	Operation  domain.OnboardingOperation
	Connection domain.Connection
	Resources  []domain.ManagedResource
	HookPlan   map[string]any
	Ready      bool
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
		_ = s.store.UpdateOnboardingOperation(ctx, request.WorkspaceID, operationID, "", status, category, safeMessage(err))
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
		if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "source_project", "succeeded", request.SourceProjectExternalID, map[string]any{"external_id": request.SourceProjectExternalID}); err != nil {
			return fail(err, domain.OnboardingFailed)
		}
		if _, err := s.store.FindActiveConnectionBySource(ctx, request.WorkspaceID, request.SourceGitLabInstance.ID, request.SourceProjectExternalID); err == nil {
			return fail(fmt.Errorf("%w: source project is already bound", domain.ErrConflict), domain.OnboardingBlocked)
		} else if !errors.Is(err, domain.ErrNotFound) {
			return fail(err, domain.OnboardingFailed)
		}
		if request.TargetProject != nil {
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
		connection = domain.Connection{ID: domain.NewID(), WorkspaceID: request.WorkspaceID, Name: sourceProject.FullPath, SourceGitLabProject: domain.ProviderProjectRef{InstanceID: sourceProject.InstanceID, ExternalID: sourceProject.ExternalID, FullPath: sourceProject.FullPath, Name: sourceProject.Name, WebURL: sourceProject.WebURL, SSHURL: sourceProject.SSHURL, HTTPSURL: sourceProject.HTTPSURL}, TargetMulticaProject: domain.ProviderProjectRef{InstanceID: targetProject.InstanceID, ExternalID: targetProject.ExternalID, Name: targetProject.Title, WebURL: targetProject.WebURL}, Status: domain.ConnectionConfigured, ConfiguredAt: s.now().UTC()}
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

	sourceProject, err := s.loadSourceProject(ctx, request)
	if err != nil {
		return fail(err, domain.OnboardingFailed)
	}

	resources := make([]domain.ManagedResource, 0, 3)
	gitlabCredential, gitlabCleanup, err := s.resolveCredential(ctx, request.GitLabCredentialRef)
	if err != nil {
		return fail(err, domain.OnboardingBlocked)
	}
	defer gitlabCleanup()
	label, err := s.gitlab.EnsureLabel(ctx, request.SourceGitLabInstance, sourceProject, "change", gitlabCredential)
	if err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	labelResource, err := s.store.EnsureManagedResource(ctx, domain.ManagedResource{ID: domain.NewID(), WorkspaceID: request.WorkspaceID, ConnectionID: connection.ID, Kind: domain.ResourceLabel, Provider: domain.ProviderGitLab, InstanceID: request.SourceGitLabInstance.ID, ExternalID: label.ExternalID, Ownership: ownershipFrom(label.Created, label.Adopted), ManagementMark: "specwire-managed", Status: "ready", Snapshot: map[string]any{"title": label.Title, "request_id": label.RequestID}})
	if err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	resources = append(resources, labelResource)
	if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "gitlab_label", "succeeded", label.ExternalID, map[string]any{"external_id": label.ExternalID, "created": label.Created, "adopted": label.Adopted}); err != nil {
		return fail(err, domain.OnboardingFailed)
	}

	cloneURL, err := provider.CanonicalCloneURL(sourceProject, request.PreferSSH)
	if err != nil {
		return fail(err, domain.OnboardingBlocked)
	}
	multicaCredential, multicaCleanup, err := s.resolveCredential(ctx, request.MulticaCredentialRef)
	if err != nil {
		return fail(err, domain.OnboardingBlocked)
	}
	defer multicaCleanup()
	workspaceResource, err := s.multica.EnsureWorkspaceRepository(ctx, request.TargetMulticaInstance, request.TargetWorkspace, sourceProject, cloneURL, multicaCredential)
	if err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	resource, err := s.store.EnsureManagedResource(ctx, resourceFromResult(request.WorkspaceID, connection.ID, request.TargetMulticaInstance.ID, workspaceResource))
	if err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	resources = append(resources, resource)
	if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "multica_workspace_repository", "succeeded", workspaceResource.ExternalID, map[string]any{"external_id": workspaceResource.ExternalID, "ownership": workspaceResource.Ownership, "clone_url": cloneURL}); err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	projectResource, err := s.multica.EnsureProjectResource(ctx, request.TargetMulticaInstance, targetProject, sourceProject, cloneURL, multicaCredential)
	if err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	resource, err = s.store.EnsureManagedResource(ctx, resourceFromResult(request.WorkspaceID, connection.ID, request.TargetMulticaInstance.ID, projectResource))
	if err != nil {
		return fail(err, domain.OnboardingFailed)
	}
	resources = append(resources, resource)
	if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "multica_project_resource", "succeeded", projectResource.ExternalID, map[string]any{"external_id": projectResource.ExternalID, "ownership": projectResource.Ownership, "clone_url": cloneURL}); err != nil {
		return fail(err, domain.OnboardingFailed)
	}

	hookURL := request.PublicHookURL
	if strings.TrimSpace(hookURL) == "" {
		hookURL = s.publicHookURL
	}
	hookPlan := map[string]any{"url": hookURL, "events": []string{"Issue Hook", "Push Hook"}, "status": "planned", "management": "created-on-first-published-input-flow"}
	if err := s.checkpoint(ctx, request.WorkspaceID, operationID, "hook_plan", "succeeded", "", hookPlan); err != nil {
		return fail(err, domain.OnboardingFailed)
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
	credential, cleanup, err := s.resolveCredential(ctx, request.GitLabCredentialRef)
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
	title := strings.TrimSpace(request.TargetProjectTitle)
	if title == "" {
		title = source.FullPath
	}
	credential, cleanup, err := s.resolveCredential(ctx, request.MulticaCredentialRef)
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
	return s.multica.CreateProject(ctx, request.TargetMulticaInstance, provider.CreateProjectInput{InstanceID: request.TargetMulticaInstance.ID, WorkspaceID: request.TargetWorkspace.ExternalID, Title: title, IdempotencyKey: string(operationID) + ":project"}, credential)
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

func requestSummary(request OnboardingRequest) map[string]any {
	return map[string]any{"source_instance_id": request.SourceGitLabInstance.ID, "source_project_external_id": request.SourceProjectExternalID, "target_instance_id": request.TargetMulticaInstance.ID, "target_workspace_external_id": request.TargetWorkspace.ExternalID, "target_project_external_id": func() string {
		if request.TargetProject != nil {
			return request.TargetProject.ExternalID
		}
		return ""
	}(), "create_target_project": request.CreateTargetProject, "prefer_ssh": request.PreferSSH, "public_hook_url": request.PublicHookURL}
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
func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
