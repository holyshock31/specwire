package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"specwire/bridge/internal/auth"
	"specwire/bridge/internal/controlplane"
	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/provider"
	runtimenew "specwire/bridge/internal/runtime"
	"specwire/bridge/internal/security"
)

// IntegrationStore is the read/write seam used by the HTTP control plane.  It
// keeps the API independent from the SQLite implementation while allowing the
// constructor used by the legacy API to stay unchanged.
type IntegrationStore interface {
	ListConnections(context.Context, domain.ID) ([]domain.Connection, error)
	GetConnection(context.Context, domain.ID, domain.ID) (domain.Connection, error)
	DisableConnection(context.Context, domain.ID, domain.ID) error
	ListManagedResources(context.Context, domain.ID, domain.ID) ([]domain.ManagedResource, error)
	GetHookByProject(context.Context, domain.ID, domain.ID, string) (domain.Hook, error)
	ListHookRoutes(context.Context, domain.ID, domain.ID) ([]domain.HookRoute, error)
	CreateGitLabGroupBinding(context.Context, domain.GitLabGroupBinding) error
	GetGitLabGroupBinding(context.Context, domain.ID, domain.ID) (domain.GitLabGroupBinding, error)
	GetGitLabGroupBindingByGroup(context.Context, domain.ID, domain.ID, string) (domain.GitLabGroupBinding, error)
	GetCredentialProfile(context.Context, domain.ID, domain.ID) (domain.CredentialProfile, error)
	ListFlows(context.Context, domain.ID, domain.ID) ([]domain.Flow, error)
	GetFlow(context.Context, domain.ID, domain.ID) (domain.Flow, error)
	GetFlowDraft(context.Context, domain.ID, domain.ID) (domain.FlowGraph, error)
	ListFlowVersions(context.Context, domain.ID, domain.ID) ([]domain.FlowVersion, error)
	GetFlowVersion(context.Context, domain.ID, domain.ID, int) (domain.FlowVersion, error)
	ListFlowTemplates(context.Context, domain.ID) ([]domain.FlowTemplate, error)
	GetMulticaWorkspace(context.Context, domain.ID, domain.ID, string) (domain.MulticaWorkspaceRef, error)
	GetMulticaProject(context.Context, domain.ID, domain.ID, string) (domain.MulticaProjectRef, error)
	ListFlowExecutions(context.Context, domain.ID, domain.ID, domain.ID, int) ([]domain.FlowExecution, error)
	GetFlowExecution(context.Context, domain.ID, domain.ID) (domain.FlowExecution, error)
	ListNodeExecutions(context.Context, domain.ID, domain.ID) ([]domain.NodeExecution, error)
	GetInboundEvent(context.Context, domain.ID, domain.ID) (domain.InboundEvent, error)
	CreateFlowExecution(context.Context, domain.FlowExecution) error
	CreateFlowExecutionAndEnqueue(context.Context, domain.FlowExecution, domain.Job) error
	UpdateFlowExecution(context.Context, domain.FlowExecution) error
	RequeueFlowExecution(context.Context, domain.FlowExecution, domain.Job) error
	EnqueueJob(context.Context, domain.Job) error
	ListAuditEvents(context.Context, domain.ID, string, domain.ID, int) ([]domain.AuditEvent, error)
	CreateAuditEvent(context.Context, domain.AuditEvent) error
}

type IntegrationServices struct {
	Store       IntegrationStore
	Selection   *controlplane.SelectionService
	Connections *controlplane.ConnectionService
	Hooks       *controlplane.HookReconciler
	Credentials *controlplane.CredentialService
	Flows       *flow.Service
	Registry    *controlplane.RegistryService
	LiveTests   *runtimenew.LiveTestService
}

func (s *Server) handleIntegration(w http.ResponseWriter, r *http.Request, path string) bool {
	if s.integration == nil || !strings.HasPrefix(path, "workspaces/") {
		return false
	}
	parts := splitPath(strings.TrimPrefix(path, "workspaces/"))
	if !integrationPath(parts, r.Method) {
		return false
	}
	session, ok := s.session(w, r)
	if !ok {
		return true
	}
	switch parts[1] {
	case "gitlab-instances":
		if len(parts) >= 6 && parts[3] == "groups" && parts[5] == "credentials" {
			s.handleGroupCredentials(w, r, session, parts)
		} else {
			s.handleSelectors(w, r, session, parts)
		}
	case "multica-instances":
		s.handleSelectors(w, r, session, parts)
	case "connections":
		s.handleConnections(w, r, session, parts)
	case "flow-templates":
		s.handleFlowTemplates(w, r, session, parts)
	case "flows":
		s.handleFlows(w, r, session, parts)
	case "executions":
		s.handleExecutions(w, r, session, parts)
	case "registry":
		s.handleRegistry(w, r, session, parts)
	case "audit-events":
		s.handleAuditEvents(w, r, session, parts)
	}
	return true
}

func integrationPath(parts []string, method string) bool {
	if len(parts) < 2 || parts[0] == "" {
		return false
	}
	switch parts[1] {
	case "gitlab-instances":
		return len(parts) >= 4 && (parts[3] == "groups" || parts[3] == "projects")
	case "multica-instances":
		return len(parts) >= 4 && parts[3] == "workspaces"
	case "connections", "flow-templates", "flows", "executions", "registry", "audit-events":
		return true
	default:
		return false
	}
}

func (s *Server) integrationStore(w http.ResponseWriter) (IntegrationStore, bool) {
	if s.integration == nil || s.integration.Store == nil {
		writeError(w, fmt.Errorf("%w: integration control plane is not configured", domain.ErrInvalid))
		return nil, false
	}
	return s.integration.Store, true
}

func (s *Server) authorizeWorkspace(w http.ResponseWriter, r *http.Request, session domain.Session, workspaceID domain.ID, role domain.Role, mutation bool) bool {
	if mutation {
		return s.authorizeMutation(w, r, session, workspaceID, role, auth.Scope{Type: "workspace", ID: workspaceID})
	}
	return s.authorize(w, r, session, workspaceID, role, auth.Scope{Type: "workspace", ID: workspaceID})
}

func connectionScope(connection domain.Connection) auth.Scope {
	if strings.TrimSpace(connection.SourceGitLabProject.GroupID) != "" {
		return auth.Scope{Type: "gitlab_group", ID: domain.ID(connection.SourceGitLabProject.GroupID)}
	}
	return auth.Scope{Type: "connection", ID: connection.ID}
}

func (s *Server) authorizeConnection(w http.ResponseWriter, r *http.Request, session domain.Session, connection domain.Connection, role domain.Role, mutation bool) bool {
	if mutation {
		return s.authorizeMutation(w, r, session, connection.WorkspaceID, role, connectionScope(connection))
	}
	return s.authorize(w, r, session, connection.WorkspaceID, role, connectionScope(connection))
}

func (s *Server) audit(ctx context.Context, actor domain.ID, action, entityType string, entityID domain.ID, payload map[string]any) {
	if s.integration == nil || s.integration.Store == nil {
		return
	}
	_ = s.integration.Store.CreateAuditEvent(ctx, domain.AuditEvent{ID: domain.NewID(), WorkspaceID: workspaceFromPayload(payload), ActorAccountID: actor, Action: action, EntityType: entityType, EntityID: entityID, Payload: payload})
}

// workspaceFromPayload avoids making every small handler carry a second
// workspace argument into best-effort audit calls.  Callers that need an
// audit record should include workspace_id in the payload.
func workspaceFromPayload(payload map[string]any) domain.ID {
	if payload == nil {
		return ""
	}
	if value, ok := payload["workspace_id"]; ok {
		return domain.ID(strings.TrimSpace(fmt.Sprint(value)))
	}
	return ""
}

func (s *Server) visibleConnections(ctx context.Context, session domain.Session, workspaceID domain.ID, connections []domain.Connection) []domain.Connection {
	if auth.Authorize(ctx, s.authStore(), session.AccountID, workspaceID, domain.RoleViewer, auth.Scope{Type: "workspace", ID: workspaceID}) == nil {
		return connections
	}
	visible := make([]domain.Connection, 0, len(connections))
	for _, connection := range connections {
		if auth.Authorize(ctx, s.authStore(), session.AccountID, workspaceID, domain.RoleViewer, connectionScope(connection)) == nil {
			visible = append(visible, connection)
		}
	}
	return visible
}

func (s *Server) handleSelectors(w http.ResponseWriter, r *http.Request, session domain.Session, parts []string) {
	workspaceID := domain.ID(parts[0])
	if !s.authorizeWorkspace(w, r, session, workspaceID, domain.RoleViewer, false) {
		return
	}
	if s.integration.Selection == nil || s.integration.Store == nil || s.endpoints == nil {
		writeError(w, fmt.Errorf("%w: selector service is not configured", domain.ErrInvalid))
		return
	}
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	instanceID := domain.ID(parts[2])
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if parts[1] == "gitlab-instances" {
		instance, err := s.endpoints.GetGitLab(r.Context(), workspaceID, instanceID)
		if err != nil {
			writeError(w, err)
			return
		}
		switch parts[3] {
		case "groups":
			items, err := s.integration.Selection.GitLabGroups(r.Context(), instance, query, instance.CredentialRef)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, items)
		case "projects":
			groupID := strings.TrimSpace(r.URL.Query().Get("group_id"))
			if groupID == "" {
				writeError(w, fmt.Errorf("%w: group_id is required", domain.ErrInvalid))
				return
			}
			group := provider.GitLabGroup{InstanceID: instanceID, ExternalID: groupID, FullPath: strings.TrimSpace(r.URL.Query().Get("group_path")), Name: strings.TrimSpace(r.URL.Query().Get("group_name"))}
			credentialRef := instance.CredentialRef
			if binding, bindingErr := s.integration.Store.GetGitLabGroupBindingByGroup(r.Context(), workspaceID, instanceID, groupID); bindingErr == nil && binding.CredentialRef != nil {
				credentialRef = binding.CredentialRef
			} else if bindingErr != nil && !errors.Is(bindingErr, domain.ErrNotFound) {
				writeError(w, bindingErr)
				return
			}
			items, err := s.integration.Selection.GitLabProjects(r.Context(), instance, group, query, credentialRef)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, items)
		default:
			http.NotFound(w, r)
		}
		return
	}

	instance, err := s.endpoints.GetMultica(r.Context(), workspaceID, instanceID)
	if err != nil {
		writeError(w, err)
		return
	}
	if parts[3] != "workspaces" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 4 {
		items, err := s.integration.Selection.MulticaWorkspaces(r.Context(), instance, query, instance.ManagementCredentialRef)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
		return
	}
	if len(parts) == 6 && parts[5] == "projects" {
		workspaceRef, err := s.integration.Store.GetMulticaWorkspace(r.Context(), workspaceID, instanceID, parts[4])
		if err != nil {
			writeError(w, err)
			return
		}
		items, err := s.integration.Selection.MulticaProjects(r.Context(), instance, workspaceRef, query, instance.ManagementCredentialRef)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
		return
	}
	http.NotFound(w, r)
}

type groupCredentialRequest struct {
	Alias                string                       `json:"alias"`
	Kind                 domain.CredentialProfileKind `json:"kind"`
	Secret               string                       `json:"secret"`
	RequiredCapabilities []string                     `json:"required_capabilities,omitempty"`
}

func (s *Server) handleGroupCredentials(w http.ResponseWriter, r *http.Request, session domain.Session, parts []string) {
	if s.integration == nil || s.integration.Store == nil || s.integration.Credentials == nil || s.endpoints == nil {
		writeError(w, fmt.Errorf("%w: Group credential service is not configured", domain.ErrInvalid))
		return
	}
	if len(parts) < 6 || parts[3] != "groups" || parts[5] != "credentials" {
		http.NotFound(w, r)
		return
	}
	workspaceID := domain.ID(parts[0])
	instanceID := domain.ID(parts[2])
	groupExternalID := strings.TrimSpace(parts[4])
	if groupExternalID == "" {
		writeError(w, fmt.Errorf("%w: GitLab Group external ID is required", domain.ErrInvalid))
		return
	}
	instance, err := s.endpoints.GetGitLab(r.Context(), workspaceID, instanceID)
	if err != nil {
		writeError(w, err)
		return
	}
	binding, bindingErr := s.integration.Store.GetGitLabGroupBindingByGroup(r.Context(), workspaceID, instanceID, groupExternalID)
	if bindingErr != nil && !errors.Is(bindingErr, domain.ErrNotFound) {
		writeError(w, bindingErr)
		return
	}
	if r.Method == http.MethodGet {
		if len(parts) != 6 {
			http.NotFound(w, r)
			return
		}
		if bindingErr != nil {
			writeError(w, bindingErr)
			return
		}
		if !s.authorize(w, r, session, workspaceID, domain.RoleViewer, auth.Scope{Type: "gitlab_group", ID: domain.ID(groupExternalID)}) {
			return
		}
		var profile any
		if !binding.CredentialProfileID.Empty() {
			loaded, profileErr := s.integration.Store.GetCredentialProfile(r.Context(), workspaceID, binding.CredentialProfileID)
			if profileErr != nil && !errors.Is(profileErr, domain.ErrNotFound) {
				writeError(w, profileErr)
				return
			}
			if profileErr == nil {
				profile = loaded
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"binding": binding, "credential_profile": profile})
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if !s.authorizeMutation(w, r, session, workspaceID, domain.RoleOperator, auth.Scope{Type: "gitlab_group", ID: domain.ID(groupExternalID)}) {
		return
	}
	var request groupCredentialRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(parts) == 8 && parts[7] == "rotate" {
		if bindingErr != nil {
			writeError(w, bindingErr)
			return
		}
		if domain.ID(parts[6]) != binding.CredentialProfileID {
			writeError(w, fmt.Errorf("%w: credential profile is not bound to this Group", domain.ErrForbidden))
			return
		}
		profile, profileErr := s.integration.Store.GetCredentialProfile(r.Context(), workspaceID, binding.CredentialProfileID)
		if profileErr != nil {
			writeError(w, profileErr)
			return
		}
		if request.Alias == "" {
			request.Alias = profile.Alias
		}
		if len(request.RequiredCapabilities) == 0 {
			request.RequiredCapabilities = []string{"gitlab.projects.read"}
		}
		ref := domain.SecretRef{ID: domain.NewID(), WorkspaceID: workspaceID, Alias: strings.TrimSpace(request.Alias), Kind: domain.SecretGroupCredential}
		rotated, err := s.integration.Credentials.RotateGroupCredential(r.Context(), instance, binding, ref, []byte(request.Secret), request.RequiredCapabilities)
		if err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), session.AccountID, "credential.group.rotate", "credential_profile", rotated.ID, map[string]any{"workspace_id": workspaceID, "group_external_id": groupExternalID, "alias": rotated.Alias})
		writeJSON(w, http.StatusOK, rotated)
		return
	}
	if len(parts) != 6 {
		http.NotFound(w, r)
		return
	}
	if strings.TrimSpace(request.Alias) == "" || strings.TrimSpace(request.Secret) == "" {
		writeError(w, fmt.Errorf("%w: credential alias and secret are required", domain.ErrInvalid))
		return
	}
	if bindingErr != nil {
		binding = domain.GitLabGroupBinding{ID: domain.NewID(), WorkspaceID: workspaceID, GitLabInstanceID: instanceID, ExternalGroupID: groupExternalID, FullPath: firstNonEmpty(r.URL.Query().Get("group_path"), groupExternalID), InheritSubgroups: true, Status: domain.EndpointActive}
		if err := s.integration.Store.CreateGitLabGroupBinding(r.Context(), binding); err != nil && !errors.Is(err, domain.ErrConflict) {
			writeError(w, err)
			return
		}
		binding, err = s.integration.Store.GetGitLabGroupBindingByGroup(r.Context(), workspaceID, instanceID, groupExternalID)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	if request.Kind == "" {
		request.Kind = domain.CredentialGroupAccessToken
	}
	if len(request.RequiredCapabilities) == 0 {
		request.RequiredCapabilities = []string{"gitlab.projects.read"}
	}
	profileID := domain.NewID()
	ref := domain.SecretRef{ID: domain.NewID(), WorkspaceID: workspaceID, Alias: strings.TrimSpace(request.Alias), Kind: domain.SecretGroupCredential}
	profile, err := s.integration.Credentials.BindGroupCredential(r.Context(), instance, binding, profileID, request.Alias, request.Kind, ref, []byte(request.Secret), request.RequiredCapabilities)
	if err != nil {
		writeError(w, err)
		return
	}
	s.audit(r.Context(), session.AccountID, "credential.group.bind", "credential_profile", profile.ID, map[string]any{"workspace_id": workspaceID, "group_external_id": groupExternalID, "kind": profile.Kind, "alias": profile.Alias})
	writeJSON(w, http.StatusCreated, profile)
}

type onboardingRequest struct {
	OperationID               domain.ID `json:"operation_id,omitempty"`
	SourceGitLabInstanceID    domain.ID `json:"source_gitlab_instance_id"`
	SourceProjectExternalID   string    `json:"source_project_external_id"`
	GroupBindingID            domain.ID `json:"group_binding_id,omitempty"`
	GroupExternalID           string    `json:"group_external_id,omitempty"`
	GroupFullPath             string    `json:"group_full_path,omitempty"`
	TargetMulticaInstanceID   domain.ID `json:"target_multica_instance_id"`
	TargetWorkspaceExternalID string    `json:"target_workspace_external_id"`
	TargetProjectExternalID   string    `json:"target_project_external_id,omitempty"`
	TargetProjectTitle        string    `json:"target_project_title,omitempty"`
	CreateTargetProject       bool      `json:"create_target_project,omitempty"`
	PreferHTTPS               bool      `json:"prefer_https,omitempty"`
	DryRun                    bool      `json:"dry_run,omitempty"`
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request, session domain.Session, parts []string) {
	workspaceID := domain.ID(parts[0])
	store, ok := s.integrationStore(w)
	if !ok {
		return
	}
	if len(parts) == 2 {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		connections, err := store.ListConnections(r.Context(), workspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		visible := s.visibleConnections(r.Context(), session, workspaceID, connections)
		if len(visible) == 0 && len(connections) > 0 {
			writeError(w, domain.ErrForbidden)
			return
		}
		writeJSON(w, http.StatusOK, visible)
		return
	}
	if len(parts) == 3 && parts[2] == "onboard" {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var request onboardingRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		if !s.authorizeOnboarding(w, r, session, workspaceID, request.GroupExternalID) {
			return
		}
		if request.DryRun {
			s.writeOnboardingPlan(w, r, workspaceID, request)
			return
		}
		result, err := s.onboard(r.Context(), session, workspaceID, request)
		if err != nil {
			writeError(w, err)
			return
		}
		status := http.StatusCreated
		if result.Operation.Status == domain.OnboardingBlocked || result.Operation.Status == domain.OnboardingFailed {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, result)
		return
	}
	connection, err := store.GetConnection(r.Context(), workspaceID, domain.ID(parts[2]))
	if err != nil {
		writeError(w, err)
		return
	}
	if len(parts) == 3 {
		if r.Method != http.MethodGet || !s.authorizeConnection(w, r, session, connection, domain.RoleViewer, false) {
			return
		}
		s.writeConnectionDetail(w, r, store, connection)
		return
	}
	requiredRole := domain.RoleViewer
	if r.Method != http.MethodGet {
		requiredRole = domain.RoleOperator
	}
	if !s.authorizeConnection(w, r, session, connection, requiredRole, r.Method != http.MethodGet) {
		return
	}
	switch parts[3] {
	case "disable":
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := store.DisableConnection(r.Context(), workspaceID, connection.ID); err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), session.AccountID, "connection.disable", "connection", connection.ID, map[string]any{"workspace_id": workspaceID})
		w.WriteHeader(http.StatusNoContent)
	case "deprovision":
		plan := func() (controlplane.DeprovisionPlan, error) {
			current, err := store.GetConnection(r.Context(), workspaceID, connection.ID)
			if err != nil {
				return controlplane.DeprovisionPlan{}, err
			}
			resources, err := store.ListManagedResources(r.Context(), workspaceID, connection.ID)
			if err != nil {
				return controlplane.DeprovisionPlan{}, err
			}
			return controlplane.BuildDeprovisionPlan(current, resources), nil
		}
		if r.Method == http.MethodGet {
			value, err := plan()
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, value)
			return
		}
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Confirm bool `json:"confirm"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		if !request.Confirm {
			writeError(w, fmt.Errorf("%w: deprovision requires confirm=true", domain.ErrInvalid))
			return
		}
		// Unbinding is deliberately limited to the SpecWire control plane.  The
		// provider project, adopted resources, shared Hook and execution history
		// remain in place for explicit, separately reviewed cleanup.
		if err := store.DisableConnection(r.Context(), workspaceID, connection.ID); err != nil {
			writeError(w, err)
			return
		}
		value, err := plan()
		if err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), session.AccountID, "connection.deprovision.request", "connection", connection.ID, map[string]any{"workspace_id": workspaceID, "external_deletion_planned": false})
		writeJSON(w, http.StatusOK, map[string]any{"deprovision_requested": true, "plan": value})
	case "resources":
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		items, err := store.ListManagedResources(r.Context(), workspaceID, connection.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case "hooks":
		if len(parts) == 5 && parts[4] == "rotate" {
			if r.Method != http.MethodPost || s.integration.Hooks == nil || !s.checkCSRF(w, r, session) {
				if r.Method != http.MethodPost {
					http.NotFound(w, r)
				}
				return
			}
			rotated, err := s.integration.Hooks.RotateSigningToken(r.Context(), workspaceID, connection.ID)
			if err != nil {
				writeError(w, err)
				return
			}
			s.audit(r.Context(), session.AccountID, "hook.rotate", "hook", rotated.ID, map[string]any{"workspace_id": workspaceID, "connection_id": connection.ID})
			writeJSON(w, http.StatusOK, rotated)
			return
		}
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		hook, hookErr := store.GetHookByProject(r.Context(), workspaceID, connection.SourceGitLabProject.InstanceID, connection.SourceGitLabProject.ExternalID)
		if hookErr != nil && !errors.Is(hookErr, domain.ErrNotFound) {
			writeError(w, hookErr)
			return
		}
		routes, err := store.ListHookRoutes(r.Context(), workspaceID, connection.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"hook": optionalHook(hook, hookErr == nil), "routes": routes})
	case "flows":
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		items, err := store.ListFlows(r.Context(), workspaceID, connection.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) authorizeOnboarding(w http.ResponseWriter, r *http.Request, session domain.Session, workspaceID domain.ID, groupExternalID string) bool {
	if strings.TrimSpace(groupExternalID) != "" {
		if s.authorizeMutation(w, r, session, workspaceID, domain.RoleOperator, auth.Scope{Type: "gitlab_group", ID: domain.ID(groupExternalID)}) {
			return true
		}
		return false
	}
	return s.authorizeWorkspace(w, r, session, workspaceID, domain.RoleOperator, true)
}

func (s *Server) writeOnboardingPlan(w http.ResponseWriter, r *http.Request, workspaceID domain.ID, request onboardingRequest) {
	if s.endpoints == nil || s.integration == nil || s.integration.Connections == nil {
		writeError(w, fmt.Errorf("%w: onboarding dependencies are not configured", domain.ErrInvalid))
		return
	}
	source, err := s.endpoints.GetGitLab(r.Context(), workspaceID, request.SourceGitLabInstanceID)
	if err != nil {
		writeError(w, err)
		return
	}
	target, err := s.endpoints.GetMultica(r.Context(), workspaceID, request.TargetMulticaInstanceID)
	if err != nil {
		writeError(w, err)
		return
	}
	hookURL := s.integration.Connections.PublicHookURL()
	targetProject := request.TargetProjectExternalID
	if targetProject == "" && request.CreateTargetProject {
		targetProject = "<created-after-preview>"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dry_run": true,
		"source":  map[string]any{"instance_id": source.ID, "project_external_id": request.SourceProjectExternalID, "group_external_id": request.GroupExternalID},
		"target":  map[string]any{"instance_id": target.ID, "workspace_external_id": request.TargetWorkspaceExternalID, "project_external_id": targetProject, "project_title": request.TargetProjectTitle},
		"defaults": map[string]any{"label": "change", "clone_transport": func() string {
			if request.PreferHTTPS {
				return "https"
			}
			return "ssh"
		}(), "management_mark": "specwire-managed", "hook_url": hookURL, "hook_events": []string{"Issue Hook", "Push Hook"}},
		"resources": []string{string(domain.ResourceWorkspaceRepository), string(domain.ResourceProject), string(domain.ResourceLabel)},
		"hook":      map[string]any{"status": "planned", "activation": "first-published-input-flow"},
	})
}

func (s *Server) onboard(ctx context.Context, session domain.Session, workspaceID domain.ID, request onboardingRequest) (controlplane.OnboardingResult, error) {
	store := s.integration.Store
	if s.endpoints == nil || s.integration.Connections == nil {
		return controlplane.OnboardingResult{}, fmt.Errorf("%w: onboarding service is not configured", domain.ErrInvalid)
	}
	sourceInstance, err := s.endpoints.GetGitLab(ctx, workspaceID, request.SourceGitLabInstanceID)
	if err != nil {
		return controlplane.OnboardingResult{}, err
	}
	targetInstance, err := s.endpoints.GetMultica(ctx, workspaceID, request.TargetMulticaInstanceID)
	if err != nil {
		return controlplane.OnboardingResult{}, err
	}
	group, err := s.resolveGroupBinding(ctx, workspaceID, request, sourceInstance.ID)
	if err != nil {
		return controlplane.OnboardingResult{}, err
	}
	workspaceRef, err := store.GetMulticaWorkspace(ctx, workspaceID, targetInstance.ID, request.TargetWorkspaceExternalID)
	if err != nil {
		return controlplane.OnboardingResult{}, err
	}
	var targetProject *provider.MulticaProject
	if strings.TrimSpace(request.TargetProjectExternalID) != "" {
		project, err := store.GetMulticaProject(ctx, workspaceID, targetInstance.ID, request.TargetProjectExternalID)
		if err != nil {
			return controlplane.OnboardingResult{}, err
		}
		targetProject = &provider.MulticaProject{InstanceID: targetInstance.ID, ExternalID: project.ExternalID, Title: project.Title, WorkspaceID: workspaceRef.ExternalID}
	}
	result, err := s.integration.Connections.Onboard(ctx, controlplane.OnboardingRequest{
		OperationID: request.OperationID, ActorID: session.AccountID, WorkspaceID: workspaceID, SourceGitLabInstance: sourceInstance, SourceProjectExternalID: request.SourceProjectExternalID,
		Group: group, TargetMulticaInstance: targetInstance, TargetWorkspace: provider.MulticaWorkspace{InstanceID: targetInstance.ID, ExternalID: workspaceRef.ExternalID, Name: workspaceRef.Name}, TargetProject: targetProject,
		CreateTargetProject: request.CreateTargetProject, TargetProjectTitle: request.TargetProjectTitle, PreferHTTPS: request.PreferHTTPS,
	})
	if err == nil {
		s.audit(ctx, session.AccountID, "connection.onboard", "connection", result.Connection.ID, map[string]any{"workspace_id": workspaceID, "operation_id": result.Operation.ID, "ready": result.Ready})
	}
	return result, err
}

func (s *Server) resolveGroupBinding(ctx context.Context, workspaceID domain.ID, request onboardingRequest, instanceID domain.ID) (domain.GitLabGroupBinding, error) {
	store := s.integration.Store
	if !request.GroupBindingID.Empty() {
		return store.GetGitLabGroupBinding(ctx, workspaceID, request.GroupBindingID)
	}
	groupID := strings.TrimSpace(request.GroupExternalID)
	if groupID == "" {
		return domain.GitLabGroupBinding{}, fmt.Errorf("%w: group_external_id or group_binding_id is required", domain.ErrInvalid)
	}
	binding, err := store.GetGitLabGroupBindingByGroup(ctx, workspaceID, instanceID, groupID)
	if err == nil {
		return binding, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.GitLabGroupBinding{}, err
	}
	binding = domain.GitLabGroupBinding{ID: domain.NewID(), WorkspaceID: workspaceID, GitLabInstanceID: instanceID, ExternalGroupID: groupID, FullPath: firstNonEmpty(request.GroupFullPath, groupID), InheritSubgroups: true, Status: domain.EndpointActive}
	if err := store.CreateGitLabGroupBinding(ctx, binding); err != nil && !errors.Is(err, domain.ErrConflict) {
		return domain.GitLabGroupBinding{}, err
	}
	return store.GetGitLabGroupBindingByGroup(ctx, workspaceID, instanceID, groupID)
}

type connectionDetail struct {
	Connection domain.Connection        `json:"connection"`
	Resources  []domain.ManagedResource `json:"resources"`
	Hook       *domain.Hook             `json:"hook,omitempty"`
	Routes     []domain.HookRoute       `json:"routes"`
	Flows      []domain.Flow            `json:"flows"`
}

func (s *Server) writeConnectionDetail(w http.ResponseWriter, r *http.Request, store IntegrationStore, connection domain.Connection) {
	resources, err := store.ListManagedResources(r.Context(), connection.WorkspaceID, connection.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	routes, err := store.ListHookRoutes(r.Context(), connection.WorkspaceID, connection.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	flows, err := store.ListFlows(r.Context(), connection.WorkspaceID, connection.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	hook, hookErr := store.GetHookByProject(r.Context(), connection.WorkspaceID, connection.SourceGitLabProject.InstanceID, connection.SourceGitLabProject.ExternalID)
	if hookErr != nil && !errors.Is(hookErr, domain.ErrNotFound) {
		writeError(w, hookErr)
		return
	}
	var hookPtr *domain.Hook
	if hookErr == nil {
		hookPtr = &hook
	}
	writeJSON(w, http.StatusOK, connectionDetail{Connection: connection, Resources: resources, Hook: hookPtr, Routes: routes, Flows: flows})
}

func optionalHook(hook domain.Hook, present bool) any {
	if !present {
		return nil
	}
	return hook
}

func (s *Server) handleFlowTemplates(w http.ResponseWriter, r *http.Request, session domain.Session, parts []string) {
	if r.Method != http.MethodGet || len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	workspaceID := domain.ID(parts[0])
	if !s.authorizeWorkspace(w, r, session, workspaceID, domain.RoleViewer, false) {
		return
	}
	store, ok := s.integrationStore(w)
	if !ok {
		return
	}
	items, err := store.ListFlowTemplates(r.Context(), workspaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type createFlowRequest struct {
	ConnectionID    domain.ID `json:"connection_id"`
	TemplateKey     string    `json:"template_key,omitempty"`
	TemplateVersion string    `json:"template_version,omitempty"`
	Name            string    `json:"name"`
	Blank           bool      `json:"blank,omitempty"`
}

func (s *Server) handleFlows(w http.ResponseWriter, r *http.Request, session domain.Session, parts []string) {
	workspaceID := domain.ID(parts[0])
	store, ok := s.integrationStore(w)
	if !ok {
		return
	}
	if len(parts) == 2 {
		s.handleFlowCollection(w, r, session, workspaceID, store)
		return
	}
	flowID := domain.ID(parts[2])
	item, err := store.GetFlow(r.Context(), workspaceID, flowID)
	if err != nil {
		writeError(w, err)
		return
	}
	if !s.authorizeConnectionByID(w, r, session, store, workspaceID, item.ConnectionID, r.Method != http.MethodGet) {
		return
	}
	if len(parts) == 3 {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		draft, draftErr := store.GetFlowDraft(r.Context(), workspaceID, flowID)
		if draftErr != nil && !errors.Is(draftErr, domain.ErrNotFound) {
			writeError(w, draftErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"flow": item, "draft": func() any {
			if draftErr != nil {
				return nil
			}
			return draft
		}()})
		return
	}
	switch parts[3] {
	case "draft":
		s.handleFlowDraft(w, r, session, store, item)
	case "validate":
		s.handleFlowValidate(w, r, store, item)
	case "simulate":
		s.handleFlowSimulate(w, r, session, store, item)
	case "test":
		s.handleFlowLiveTest(w, r, session, item)
	case "publish":
		s.handleFlowPublish(w, r, session, item)
	case "pause":
		s.handleFlowPause(w, r, session, item)
	case "archive":
		s.handleFlowArchive(w, r, session, item)
	case "versions":
		s.handleFlowVersions(w, r, store, item, parts[4:])
	case "executions":
		s.handleFlowExecutions(w, r, store, item)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleFlowCollection(w http.ResponseWriter, r *http.Request, session domain.Session, workspaceID domain.ID, store IntegrationStore) {
	switch r.Method {
	case http.MethodGet:
		var connectionID domain.ID
		if value := strings.TrimSpace(r.URL.Query().Get("connection_id")); value != "" {
			connectionID = domain.ID(value)
		}
		if !connectionID.Empty() {
			connection, err := store.GetConnection(r.Context(), workspaceID, connectionID)
			if err != nil {
				writeError(w, err)
				return
			}
			if !s.authorizeConnection(w, r, session, connection, domain.RoleViewer, false) {
				return
			}
			items, err := store.ListFlows(r.Context(), workspaceID, connectionID)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, items)
			return
		}
		if !s.authorizeWorkspace(w, r, session, workspaceID, domain.RoleViewer, false) {
			return
		}
		connections, err := store.ListConnections(r.Context(), workspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		visible := s.visibleConnections(r.Context(), session, workspaceID, connections)
		allowed := make(map[domain.ID]struct{}, len(visible))
		for _, connection := range visible {
			allowed[connection.ID] = struct{}{}
		}
		items, err := store.ListFlows(r.Context(), workspaceID, connectionID)
		if err != nil {
			writeError(w, err)
			return
		}
		filtered := make([]domain.Flow, 0, len(items))
		for _, item := range items {
			if _, ok := allowed[item.ConnectionID]; ok {
				filtered = append(filtered, item)
			}
		}
		writeJSON(w, http.StatusOK, filtered)
	case http.MethodPost:
		var request createFlowRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		connection, err := store.GetConnection(r.Context(), workspaceID, request.ConnectionID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !s.authorizeConnection(w, r, session, connection, domain.RoleOperator, true) {
			return
		}
		if s.integration.Flows == nil {
			writeError(w, fmt.Errorf("%w: Flow service is not configured", domain.ErrInvalid))
			return
		}
		var item domain.Flow
		if request.Blank {
			item, err = s.integration.Flows.CreateBlank(r.Context(), workspaceID, connection.ID, session.AccountID, request.Name)
		} else {
			version := firstNonEmpty(request.TemplateVersion, "1.0.0")
			item, err = s.integration.Flows.CreateFromTemplate(r.Context(), workspaceID, connection.ID, session.AccountID, request.TemplateKey, version, request.Name)
		}
		if err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), session.AccountID, "flow.create", "flow", item.ID, map[string]any{"workspace_id": workspaceID, "connection_id": connection.ID})
		writeJSON(w, http.StatusCreated, item)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) authorizeConnectionByID(w http.ResponseWriter, r *http.Request, session domain.Session, store IntegrationStore, workspaceID, connectionID domain.ID, mutation bool) bool {
	connection, err := store.GetConnection(r.Context(), workspaceID, connectionID)
	if err != nil {
		writeError(w, err)
		return false
	}
	role := domain.RoleViewer
	if mutation {
		role = domain.RoleOperator
	}
	return s.authorizeConnection(w, r, session, connection, role, mutation)
}

func (s *Server) handleFlowDraft(w http.ResponseWriter, r *http.Request, session domain.Session, store IntegrationStore, item domain.Flow) {
	if r.Method == http.MethodGet {
		draft, err := store.GetFlowDraft(r.Context(), item.WorkspaceID, item.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, draft)
		return
	}
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	if s.integration.Flows == nil {
		writeError(w, fmt.Errorf("%w: Flow service is not configured", domain.ErrInvalid))
		return
	}
	if !s.checkCSRFForMutation(w, r, session) {
		return
	}
	var request struct {
		Graph domain.FlowGraph `json:"graph"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	validation, err := s.integration.Flows.SaveDraft(r.Context(), item.WorkspaceID, item.ID, request.Graph)
	if err != nil {
		writeError(w, err)
		return
	}
	s.audit(r.Context(), session.AccountID, "flow.draft.save", "flow", item.ID, map[string]any{"workspace_id": item.WorkspaceID, "valid": validation.Valid})
	writeJSON(w, http.StatusOK, map[string]any{"draft": request.Graph, "validation": validation})
}

func (s *Server) checkCSRFForMutation(w http.ResponseWriter, r *http.Request, session domain.Session) bool {
	return s.checkCSRF(w, r, session)
}

func (s *Server) handleFlowValidate(w http.ResponseWriter, r *http.Request, store IntegrationStore, item domain.Flow) {
	if r.Method != http.MethodPost || s.integration.Flows == nil {
		http.NotFound(w, r)
		return
	}
	validation, err := s.integration.Flows.ValidateDraft(r.Context(), item.WorkspaceID, item.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, validation)
}

func (s *Server) handleFlowSimulate(w http.ResponseWriter, r *http.Request, session domain.Session, store IntegrationStore, item domain.Flow) {
	if r.Method != http.MethodPost || s.integration.Flows == nil || !s.checkCSRF(w, r, session) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
		}
		return
	}
	var request struct {
		SampleEvent map[string]any `json:"sample_event"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	connection, err := store.GetConnection(r.Context(), item.WorkspaceID, item.ConnectionID)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := s.integration.Flows.Simulate(r.Context(), item.WorkspaceID, item.ID, request.SampleEvent, flow.RuntimeContext{
		WorkspaceID:   item.WorkspaceID,
		ConnectionID:  item.ConnectionID,
		SourceProject: connection.SourceGitLabProject.FullPath,
		TargetProject: connection.TargetMulticaProject.ExternalID,
		TargetRef:     "refs/heads/main",
	})
	if err != nil {
		writeError(w, err)
		return
	}
	for index := range result.Nodes {
		if result.Nodes[index].Input != nil {
			result.Nodes[index].Input, _ = security.RedactValue(result.Nodes[index].Input).(map[string]any)
		}
		if result.Nodes[index].Output != nil {
			result.Nodes[index].Output, _ = security.RedactValue(result.Nodes[index].Output).(map[string]any)
		}
	}
	s.audit(r.Context(), session.AccountID, "flow.simulate", "flow", item.ID, map[string]any{"workspace_id": item.WorkspaceID, "valid": result.Valid, "external_actions_suppressed": result.ExternalActionsSuppressed})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleFlowLiveTest(w http.ResponseWriter, r *http.Request, session domain.Session, item domain.Flow) {
	if r.Method != http.MethodPost || s.integration == nil || s.integration.LiveTests == nil || !s.checkCSRF(w, r, session) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
		}
		return
	}
	var request struct {
		SampleEvent        map[string]any `json:"sample_event,omitempty"`
		ConfirmSideEffects bool           `json:"confirm_side_effects"`
		FlowVersion        int            `json:"flow_version,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	execution, err := s.integration.LiveTests.Start(r.Context(), item.WorkspaceID, item.ID, runtimenew.LiveTestRequest{
		SampleEvent:        request.SampleEvent,
		ConfirmSideEffects: request.ConfirmSideEffects,
		FlowVersion:        request.FlowVersion,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	s.audit(r.Context(), session.AccountID, "flow.live_test", "flow_execution", execution.ID, map[string]any{
		"workspace_id": item.WorkspaceID, "flow_id": item.ID, "flow_version": execution.FlowVersion, "side_effects_confirmed": true,
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"execution": execution, "side_effects_confirmed": true})
}

func (s *Server) handleFlowPublish(w http.ResponseWriter, r *http.Request, session domain.Session, item domain.Flow) {
	if r.Method != http.MethodPost || s.integration.Flows == nil || !s.checkCSRF(w, r, session) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
		}
		return
	}
	version, validation, err := s.integration.Flows.Publish(r.Context(), item.WorkspaceID, item.ID, session.AccountID)
	if err != nil {
		if !validation.Valid {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Flow cannot be published", "validation": validation})
			return
		}
		writeError(w, err)
		return
	}
	s.audit(r.Context(), session.AccountID, "flow.publish", "flow", item.ID, map[string]any{"workspace_id": item.WorkspaceID, "version": version.Version})
	writeJSON(w, http.StatusOK, map[string]any{"version": version, "validation": validation})
}

func (s *Server) handleFlowPause(w http.ResponseWriter, r *http.Request, session domain.Session, item domain.Flow) {
	if r.Method != http.MethodPost || s.integration.Flows == nil || !s.checkCSRF(w, r, session) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
		}
		return
	}
	if err := s.integration.Flows.Pause(r.Context(), item.WorkspaceID, item.ID, item.ActiveVersion); err != nil {
		writeError(w, err)
		return
	}
	s.audit(r.Context(), session.AccountID, "flow.pause", "flow", item.ID, map[string]any{"workspace_id": item.WorkspaceID, "version": item.ActiveVersion})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFlowArchive(w http.ResponseWriter, r *http.Request, session domain.Session, item domain.Flow) {
	if r.Method != http.MethodPost || s.integration.Flows == nil || !s.checkCSRF(w, r, session) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
		}
		return
	}
	if err := s.integration.Flows.Archive(r.Context(), item.WorkspaceID, item.ID); err != nil {
		writeError(w, err)
		return
	}
	s.audit(r.Context(), session.AccountID, "flow.archive", "flow", item.ID, map[string]any{"workspace_id": item.WorkspaceID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFlowVersions(w http.ResponseWriter, r *http.Request, store IntegrationStore, item domain.Flow, rest []string) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if len(rest) == 0 {
		items, err := store.ListFlowVersions(r.Context(), item.WorkspaceID, item.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
		return
	}
	if len(rest) != 1 {
		http.NotFound(w, r)
		return
	}
	version, err := strconv.Atoi(rest[0])
	if err != nil || version <= 0 {
		writeError(w, fmt.Errorf("%w: version must be positive", domain.ErrInvalid))
		return
	}
	itemVersion, err := store.GetFlowVersion(r.Context(), item.WorkspaceID, item.ID, version)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, itemVersion)
}

func (s *Server) handleFlowExecutions(w http.ResponseWriter, r *http.Request, store IntegrationStore, item domain.Flow) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	limit := parseLimit(r)
	items, err := store.ListFlowExecutions(r.Context(), item.WorkspaceID, item.ConnectionID, item.ID, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleExecutions(w http.ResponseWriter, r *http.Request, session domain.Session, parts []string) {
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	store, ok := s.integrationStore(w)
	if !ok {
		return
	}
	workspaceID := domain.ID(parts[0])
	execution, err := store.GetFlowExecution(r.Context(), workspaceID, domain.ID(parts[2]))
	if err != nil {
		writeError(w, err)
		return
	}
	if !s.authorizeConnectionByID(w, r, session, store, workspaceID, execution.ConnectionID, r.Method != http.MethodGet) {
		return
	}
	if len(parts) == 3 {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		s.writeExecutionDetail(w, r, store, execution)
		return
	}
	switch parts[3] {
	case "retry":
		s.retryExecution(w, r, session, store, execution)
	case "replay":
		s.replayExecution(w, r, session, store, execution)
	default:
		http.NotFound(w, r)
	}
}

type executionDetail struct {
	Execution domain.FlowExecution   `json:"execution"`
	Event     domain.InboundEvent    `json:"event"`
	Nodes     []domain.NodeExecution `json:"nodes"`
}

func (s *Server) writeExecutionDetail(w http.ResponseWriter, r *http.Request, store IntegrationStore, execution domain.FlowExecution) {
	event, err := store.GetInboundEvent(r.Context(), execution.WorkspaceID, execution.EventID)
	if err != nil {
		writeError(w, err)
		return
	}
	nodes, err := store.ListNodeExecutions(r.Context(), execution.WorkspaceID, execution.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, executionDetail{Execution: execution, Event: event, Nodes: nodes})
}

func (s *Server) retryExecution(w http.ResponseWriter, r *http.Request, session domain.Session, store IntegrationStore, execution domain.FlowExecution) {
	if r.Method != http.MethodPost || !s.checkCSRF(w, r, session) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
		}
		return
	}
	if execution.Status != domain.ExecutionFailed && execution.Status != domain.ExecutionIndeterminate && execution.Status != domain.ExecutionReconciliationNeeded {
		writeError(w, fmt.Errorf("%w: only failed or reconciliation-required executions can be retried", domain.ErrConflict))
		return
	}
	execution.Status = domain.ExecutionQueued
	execution.ErrorCategory = ""
	execution.ErrorMessage = ""
	job := domain.Job{ID: domain.NewID(), WorkspaceID: execution.WorkspaceID, Kind: "flow.retry", Payload: map[string]any{"execution_id": execution.ID, "connection_id": execution.ConnectionID}}
	if err := store.RequeueFlowExecution(r.Context(), execution, job); err != nil {
		writeError(w, err)
		return
	}
	s.audit(r.Context(), session.AccountID, "execution.retry", "flow_execution", execution.ID, map[string]any{"workspace_id": execution.WorkspaceID, "job_id": job.ID})
	writeJSON(w, http.StatusAccepted, execution)
}

func (s *Server) replayExecution(w http.ResponseWriter, r *http.Request, session domain.Session, store IntegrationStore, execution domain.FlowExecution) {
	if r.Method != http.MethodPost || !s.checkCSRF(w, r, session) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
		}
		return
	}
	var request struct {
		FlowVersion        int  `json:"flow_version"`
		ConfirmSideEffects bool `json:"confirm_side_effects"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.FlowVersion <= 0 || !request.ConfirmSideEffects {
		writeError(w, fmt.Errorf("%w: replay requires a selected flow_version and confirm_side_effects=true", domain.ErrInvalid))
		return
	}
	version, err := store.GetFlowVersion(r.Context(), execution.WorkspaceID, execution.FlowID, request.FlowVersion)
	if err != nil {
		writeError(w, err)
		return
	}
	if version.Status != domain.FlowPublished {
		writeError(w, fmt.Errorf("%w: replay requires a published FlowVersion", domain.ErrConflict))
		return
	}
	event, err := store.GetInboundEvent(r.Context(), execution.WorkspaceID, execution.EventID)
	if err != nil {
		writeError(w, err)
		return
	}
	replayID := domain.NewID()
	replay := domain.FlowExecution{ID: replayID, WorkspaceID: execution.WorkspaceID, ConnectionID: execution.ConnectionID, FlowID: execution.FlowID, FlowVersionID: version.ID, FlowVersion: version.Version, EventID: event.ID, DeliveryID: event.DeliveryID + "#replay-" + string(replayID), IdempotencyKey: "replay:" + string(replayID), CorrelationID: "replay:" + string(replayID), Status: domain.ExecutionQueued}
	job := domain.Job{ID: domain.NewID(), WorkspaceID: replay.WorkspaceID, Kind: "flow.execute", Payload: map[string]any{"execution_id": replay.ID, "connection_id": replay.ConnectionID}}
	if err := store.CreateFlowExecutionAndEnqueue(r.Context(), replay, job); err != nil {
		writeError(w, err)
		return
	}
	s.audit(r.Context(), session.AccountID, "execution.replay", "flow_execution", replay.ID, map[string]any{"workspace_id": replay.WorkspaceID, "source_execution_id": execution.ID, "flow_version": version.Version, "side_effects_confirmed": true})
	writeJSON(w, http.StatusAccepted, replay)
}

func (s *Server) handleRegistry(w http.ResponseWriter, r *http.Request, session domain.Session, parts []string) {
	if (len(parts) != 3 && len(parts) != 4) || s.integration.Registry == nil {
		http.NotFound(w, r)
		return
	}
	workspaceID := domain.ID(parts[0])
	resource := parts[2]
	if r.Method == http.MethodGet {
		if len(parts) != 3 {
			http.NotFound(w, r)
			return
		}
		if !s.authorizeWorkspace(w, r, session, workspaceID, domain.RoleViewer, false) {
			return
		}
		var value any
		var err error
		switch resource {
		case "connector-types":
			value, err = s.integration.Registry.ListConnectorTypes(r.Context(), workspaceID)
		case "connector-behaviors":
			value, err = s.integration.Registry.ListConnectorBehaviors(r.Context(), workspaceID)
		case "data-models":
			value, err = s.integration.Registry.ListDataModels(r.Context(), workspaceID)
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
		return
	}
	if len(parts) == 4 {
		if r.Method != http.MethodPatch || !s.authorizeWorkspace(w, r, session, workspaceID, domain.RoleAdmin, true) {
			return
		}
		var request struct {
			Status domain.ConnectorStatus `json:"status"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		var err error
		itemID := domain.ID(parts[3])
		switch resource {
		case "connector-types":
			err = s.integration.Registry.SetConnectorTypeStatus(r.Context(), workspaceID, itemID, request.Status)
		case "connector-behaviors":
			err = s.integration.Registry.SetConnectorBehaviorStatus(r.Context(), workspaceID, itemID, request.Status)
		case "data-models":
			err = s.integration.Registry.SetDataModelStatus(r.Context(), workspaceID, itemID, request.Status)
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), session.AccountID, "registry."+resource+".status", resource, itemID, map[string]any{"workspace_id": workspaceID, "status": request.Status})
		writeJSON(w, http.StatusOK, map[string]any{"id": itemID, "status": request.Status})
		return
	}
	if r.Method != http.MethodPost || !s.authorizeWorkspace(w, r, session, workspaceID, domain.RoleAdmin, true) {
		return
	}
	var value any
	var entityID domain.ID
	switch resource {
	case "connector-types":
		var item domain.ConnectorType
		if !decodeJSON(w, r, &item) {
			return
		}
		if item.ID.Empty() {
			item.ID = domain.NewID()
		}
		if item.Status == "" {
			item.Status = domain.DefinitionDraft
		}
		if err := s.integration.Registry.RegisterConnectorType(r.Context(), workspaceID, item); err != nil {
			writeError(w, err)
			return
		}
		value = item
		entityID = item.ID
	case "connector-behaviors":
		var item domain.ConnectorBehavior
		if !decodeJSON(w, r, &item) {
			return
		}
		if item.ID.Empty() {
			item.ID = domain.NewID()
		}
		if item.Status == "" {
			item.Status = domain.DefinitionDraft
		}
		if err := s.integration.Registry.RegisterConnectorBehavior(r.Context(), workspaceID, item); err != nil {
			writeError(w, err)
			return
		}
		value = item
		entityID = item.ID
	case "data-models":
		var item domain.DataModelDefinition
		if !decodeJSON(w, r, &item) {
			return
		}
		if item.ID.Empty() {
			item.ID = domain.NewID()
		}
		if item.Status == "" {
			item.Status = domain.DefinitionDraft
		}
		if err := s.integration.Registry.RegisterDataModel(r.Context(), workspaceID, item); err != nil {
			writeError(w, err)
			return
		}
		value = item
		entityID = item.ID
	default:
		http.NotFound(w, r)
		return
	}
	s.audit(r.Context(), session.AccountID, "registry."+resource+".register", resource, entityID, map[string]any{"workspace_id": workspaceID})
	writeJSON(w, http.StatusCreated, value)
}

func (s *Server) handleAuditEvents(w http.ResponseWriter, r *http.Request, session domain.Session, parts []string) {
	if r.Method != http.MethodGet || len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	workspaceID := domain.ID(parts[0])
	if !s.authorizeWorkspace(w, r, session, workspaceID, domain.RoleViewer, false) {
		return
	}
	store, ok := s.integrationStore(w)
	if !ok {
		return
	}
	entityID := domain.ID(strings.TrimSpace(r.URL.Query().Get("entity_id")))
	items, err := store.ListAuditEvents(r.Context(), workspaceID, r.URL.Query().Get("entity_type"), entityID, parseLimit(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func parseLimit(r *http.Request) int {
	value, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if value <= 0 {
		return 50
	}
	if value > 500 {
		return 500
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
