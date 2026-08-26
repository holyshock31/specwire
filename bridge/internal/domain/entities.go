package domain

import (
	"fmt"
	"strings"
	"time"
)

type WorkspaceStatus string

const (
	WorkspaceActive    WorkspaceStatus = "active"
	WorkspaceSuspended WorkspaceStatus = "suspended"
)

type AccountStatus string

const (
	AccountPending  AccountStatus = "pending"
	AccountActive   AccountStatus = "active"
	AccountDisabled AccountStatus = "disabled"
)

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

type ProviderKind string

const (
	ProviderGitLab  ProviderKind = "gitlab"
	ProviderMultica ProviderKind = "multica"
)

type EndpointStatus string

const (
	EndpointActive   EndpointStatus = "active"
	EndpointDisabled EndpointStatus = "disabled"
)

type ConnectionStatus string

const (
	ConnectionConfigured  ConnectionStatus = "configured"
	ConnectionReady       ConnectionStatus = "ready"
	ConnectionReadyFailed ConnectionStatus = "ready_failed"
	ConnectionDisabled    ConnectionStatus = "disabled"
)

type Ownership string

const (
	OwnershipManaged  Ownership = "managed"
	OwnershipAdopted  Ownership = "adopted"
	OwnershipExternal Ownership = "external"
)

type ResourceKind string

const (
	ResourceWorkspaceRepository ResourceKind = "workspace_repository"
	ResourceProject             ResourceKind = "project"
	ResourceLabel               ResourceKind = "label"
	ResourceHook                ResourceKind = "hook"
	ResourceGitLabProject       ResourceKind = "gitlab_project"
)

type HookStatus string

const (
	HookPlanned  HookStatus = "planned"
	HookActive   HookStatus = "active"
	HookDisabled HookStatus = "disabled"
	HookError    HookStatus = "error"
)

type ConnectorDirection string

const (
	DirectionInput  ConnectorDirection = "input"
	DirectionOutput ConnectorDirection = "output"
)

type ConnectorStatus string

const (
	DefinitionDraft      ConnectorStatus = "draft"
	DefinitionPublished  ConnectorStatus = "published"
	DefinitionDeprecated ConnectorStatus = "deprecated"
	DefinitionDisabled   ConnectorStatus = "disabled"
)

type FlowStatus string

const (
	FlowDraft     FlowStatus = "draft"
	FlowPublished FlowStatus = "published"
	FlowPaused    FlowStatus = "paused"
	FlowArchived  FlowStatus = "archived"
)

type ExecutionStatus string

const (
	ExecutionQueued               ExecutionStatus = "queued"
	ExecutionRunning              ExecutionStatus = "running"
	ExecutionSucceeded            ExecutionStatus = "succeeded"
	ExecutionFailed               ExecutionStatus = "failed"
	ExecutionSkipped              ExecutionStatus = "skipped"
	ExecutionIndeterminate        ExecutionStatus = "indeterminate"
	ExecutionReconciliationNeeded ExecutionStatus = "reconciliation-required"
)

type NodeExecutionStatus string

const (
	NodeQueued        NodeExecutionStatus = "queued"
	NodeRunning       NodeExecutionStatus = "running"
	NodeSucceeded     NodeExecutionStatus = "succeeded"
	NodeFailed        NodeExecutionStatus = "failed"
	NodeSkipped       NodeExecutionStatus = "skipped"
	NodeIndeterminate NodeExecutionStatus = "indeterminate"
)

type SecretKind string

const (
	SecretLoginProvider     SecretKind = "login_provider"
	SecretGroupCredential   SecretKind = "group_credential"
	SecretMulticaCredential SecretKind = "multica_control_plane"
	SecretHookSigning       SecretKind = "hook_signing"
	SecretGeneric           SecretKind = "generic"
)

// Workspace is the isolation boundary for all control-plane resources.
type Workspace struct {
	ID        ID              `json:"id"`
	Slug      string          `json:"slug"`
	Name      string          `json:"name"`
	Status    WorkspaceStatus `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (w Workspace) Validate() error {
	if err := requireID("id", w.ID); err != nil {
		return err
	}
	if strings.TrimSpace(w.Slug) == "" {
		return fmt.Errorf("%w: slug is required", ErrInvalid)
	}
	if strings.TrimSpace(w.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if w.Status == "" {
		return fmt.Errorf("%w: status is required", ErrInvalid)
	}
	return nil
}

// Account is intentionally not Workspace-owned: one account may be a member
// of many Workspaces.  Membership is the explicit isolation edge.
type Account struct {
	ID          ID            `json:"id"`
	Email       string        `json:"email"`
	DisplayName string        `json:"display_name"`
	Status      AccountStatus `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

type IdentityProvider struct {
	ID              ID         `json:"id"`
	WorkspaceID     ID         `json:"workspace_id"`
	Kind            string     `json:"kind"`
	Name            string     `json:"name"`
	IssuerURL       string     `json:"issuer_url,omitempty"`
	ClientID        string     `json:"client_id,omitempty"`
	ClientSecretRef *SecretRef `json:"client_secret_ref,omitempty"`
	Enabled         bool       `json:"enabled"`
}

type WorkspaceMembership struct {
	ID          ID        `json:"id"`
	WorkspaceID ID        `json:"workspace_id"`
	AccountID   ID        `json:"account_id"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type ScopedRoleBinding struct {
	ID           ID     `json:"id"`
	WorkspaceID  ID     `json:"workspace_id"`
	MembershipID ID     `json:"membership_id"`
	Role         Role   `json:"role"`
	ScopeType    string `json:"scope_type"`
	ScopeID      ID     `json:"scope_id,omitempty"`
}

type GitLabInstance struct {
	ID            ID             `json:"id"`
	WorkspaceID   ID             `json:"workspace_id"`
	Name          string         `json:"name"`
	BaseURL       string         `json:"base_url"`
	ExternalID    string         `json:"external_id,omitempty"`
	CredentialRef *SecretRef     `json:"credential_ref,omitempty"`
	Status        EndpointStatus `json:"status"`
	Capabilities  []string       `json:"capabilities,omitempty"`
}

type GitLabGroupBinding struct {
	ID               ID             `json:"id"`
	WorkspaceID      ID             `json:"workspace_id"`
	GitLabInstanceID ID             `json:"gitlab_instance_id"`
	ExternalGroupID  string         `json:"external_group_id"`
	FullPath         string         `json:"full_path"`
	CredentialRef    *SecretRef     `json:"credential_ref,omitempty"`
	InheritSubgroups bool           `json:"inherit_subgroups"`
	Status           EndpointStatus `json:"status"`
}

type MulticaInstance struct {
	ID                      ID             `json:"id"`
	WorkspaceID             ID             `json:"workspace_id"`
	Name                    string         `json:"name"`
	BaseURL                 string         `json:"base_url"`
	ExternalID              string         `json:"external_id,omitempty"`
	ManagementCredentialRef *SecretRef     `json:"management_credential_ref,omitempty"`
	Status                  EndpointStatus `json:"status"`
	Capabilities            []string       `json:"capabilities,omitempty"`
}

type ProviderProjectRef struct {
	InstanceID ID     `json:"instance_id"`
	ExternalID string `json:"external_id"`
	FullPath   string `json:"full_path,omitempty"`
	Name       string `json:"name,omitempty"`
	WebURL     string `json:"web_url,omitempty"`
	SSHURL     string `json:"ssh_url,omitempty"`
	HTTPSURL   string `json:"https_url,omitempty"`
}

type Connection struct {
	ID                   ID                 `json:"id"`
	WorkspaceID          ID                 `json:"workspace_id"`
	Name                 string             `json:"name"`
	SourceGitLabProject  ProviderProjectRef `json:"source_gitlab_project"`
	TargetMulticaProject ProviderProjectRef `json:"target_multica_project"`
	Status               ConnectionStatus   `json:"status"`
	ConfiguredAt         time.Time          `json:"configured_at"`
	ReadyAt              *time.Time         `json:"ready_at,omitempty"`
	DisabledAt           *time.Time         `json:"disabled_at,omitempty"`
	CreatedBy            ID                 `json:"created_by"`
}

func (c Connection) Validate() error {
	if err := requireID("id", c.ID); err != nil {
		return err
	}
	if err := requireWorkspaceID(c.WorkspaceID); err != nil {
		return err
	}
	if err := requireID("source_gitlab_project.instance_id", c.SourceGitLabProject.InstanceID); err != nil {
		return err
	}
	if strings.TrimSpace(c.SourceGitLabProject.ExternalID) == "" {
		return fmt.Errorf("%w: source project external_id is required", ErrInvalid)
	}
	if err := requireID("target_multica_project.instance_id", c.TargetMulticaProject.InstanceID); err != nil {
		return err
	}
	if strings.TrimSpace(c.TargetMulticaProject.ExternalID) == "" {
		return fmt.Errorf("%w: target project external_id is required", ErrInvalid)
	}
	if c.Status == "" {
		return fmt.Errorf("%w: connection status is required", ErrInvalid)
	}
	return nil
}

type ManagedResource struct {
	ID             ID             `json:"id"`
	WorkspaceID    ID             `json:"workspace_id"`
	ConnectionID   ID             `json:"connection_id"`
	Kind           ResourceKind   `json:"kind"`
	Provider       ProviderKind   `json:"provider"`
	InstanceID     ID             `json:"instance_id"`
	ExternalID     string         `json:"external_id"`
	Ownership      Ownership      `json:"ownership"`
	ManagementMark string         `json:"management_mark,omitempty"`
	Status         string         `json:"status"`
	Snapshot       map[string]any `json:"snapshot,omitempty"`
}

type HookRoute struct {
	ID              ID                 `json:"id"`
	WorkspaceID     ID                 `json:"workspace_id"`
	ConnectionID    ID                 `json:"connection_id"`
	SourceProject   ProviderProjectRef `json:"source_project"`
	BehaviorKey     string             `json:"behavior_key"`
	BehaviorVersion string             `json:"behavior_version"`
	FlowID          ID                 `json:"flow_id"`
	FlowVersion     int                `json:"flow_version"`
	EventFilter     map[string]any     `json:"event_filter,omitempty"`
	HookRef         ID                 `json:"hook_ref"`
	Status          HookStatus         `json:"status"`
}

type ConnectorType struct {
	ID          ID              `json:"id"`
	WorkspaceID ID              `json:"workspace_id"`
	Key         string          `json:"key"`
	Version     string          `json:"version"`
	DisplayName string          `json:"display_name"`
	Provider    ProviderKind    `json:"provider"`
	Status      ConnectorStatus `json:"status"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
}

type ConnectorBehavior struct {
	ID                   ID                 `json:"id"`
	WorkspaceID          ID                 `json:"workspace_id"`
	ConnectorTypeKey     string             `json:"connector_type_key"`
	ConnectorTypeVersion string             `json:"connector_type_version"`
	Key                  string             `json:"key"`
	Version              string             `json:"version"`
	DisplayName          string             `json:"display_name"`
	Direction            ConnectorDirection `json:"direction"`
	ParameterSchema      map[string]any     `json:"parameter_schema,omitempty"`
	InputModelRef        string             `json:"input_model_ref,omitempty"`
	OutputModelRef       string             `json:"output_model_ref,omitempty"`
	ProviderEventSchema  map[string]any     `json:"provider_event_schema,omitempty"`
	RequiredCapabilities []string           `json:"required_capabilities,omitempty"`
	AdapterOperation     string             `json:"adapter_operation"`
	IdempotencyStrategy  string             `json:"idempotency_strategy"`
	Reconciliation       string             `json:"reconciliation,omitempty"`
	Status               ConnectorStatus    `json:"status"`
}

type DataModelDefinition struct {
	ID              ID                `json:"id"`
	WorkspaceID     ID                `json:"workspace_id"`
	Key             string            `json:"key"`
	Version         string            `json:"version"`
	DisplayName     string            `json:"display_name"`
	Description     string            `json:"description,omitempty"`
	Schema          map[string]any    `json:"schema"`
	RequiredFields  []string          `json:"required_fields,omitempty"`
	SemanticRoles   map[string]string `json:"semantic_roles,omitempty"`
	AllowExtensions bool              `json:"allow_extensions"`
	Status          ConnectorStatus   `json:"status"`
}

type Flow struct {
	ID            ID         `json:"id"`
	WorkspaceID   ID         `json:"workspace_id"`
	ConnectionID  ID         `json:"connection_id"`
	Name          string     `json:"name"`
	Description   string     `json:"description,omitempty"`
	Status        FlowStatus `json:"status"`
	ActiveVersion int        `json:"active_version,omitempty"`
	CreatedBy     ID         `json:"created_by"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type FlowTemplate struct {
	ID          ID              `json:"id"`
	WorkspaceID ID              `json:"workspace_id"`
	Key         string          `json:"key"`
	Version     string          `json:"version"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Graph       FlowGraph       `json:"graph"`
	Status      ConnectorStatus `json:"status"`
}

type FlowVersion struct {
	ID           ID             `json:"id"`
	WorkspaceID  ID             `json:"workspace_id"`
	FlowID       ID             `json:"flow_id"`
	Version      int            `json:"version"`
	Status       FlowStatus     `json:"status"`
	Graph        FlowGraph      `json:"graph"`
	CompiledPlan map[string]any `json:"compiled_plan,omitempty"`
	BehaviorRefs []string       `json:"behavior_refs,omitempty"`
	ModelRefs    []string       `json:"model_refs,omitempty"`
	PublishedAt  *time.Time     `json:"published_at,omitempty"`
	PublishedBy  ID             `json:"published_by,omitempty"`
}

type FlowExecution struct {
	ID                 ID              `json:"id"`
	WorkspaceID        ID              `json:"workspace_id"`
	ConnectionID       ID              `json:"connection_id"`
	FlowID             ID              `json:"flow_id"`
	FlowVersionID      ID              `json:"flow_version_id"`
	FlowVersion        int             `json:"flow_version"`
	EventID            ID              `json:"event_id"`
	DeliveryID         string          `json:"delivery_id,omitempty"`
	IdempotencyKey     string          `json:"idempotency_key"`
	CorrelationID      string          `json:"correlation_id"`
	Status             ExecutionStatus `json:"status"`
	CurrentNodeID      ID              `json:"current_node_id,omitempty"`
	ProviderRequestIDs []string        `json:"provider_request_ids,omitempty"`
	ErrorCategory      string          `json:"error_category,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type NodeExecution struct {
	ID                ID                  `json:"id"`
	WorkspaceID       ID                  `json:"workspace_id"`
	ExecutionID       ID                  `json:"execution_id"`
	NodeID            ID                  `json:"node_id"`
	Status            NodeExecutionStatus `json:"status"`
	Attempt           int                 `json:"attempt"`
	InputSnapshot     map[string]any      `json:"input_snapshot,omitempty"`
	OutputSnapshot    map[string]any      `json:"output_snapshot,omitempty"`
	ErrorCategory     string              `json:"error_category,omitempty"`
	ProviderRequestID string              `json:"provider_request_id,omitempty"`
}

type AuditEvent struct {
	ID             ID             `json:"id"`
	WorkspaceID    ID             `json:"workspace_id"`
	ActorAccountID ID             `json:"actor_account_id,omitempty"`
	Action         string         `json:"action"`
	EntityType     string         `json:"entity_type"`
	EntityID       ID             `json:"entity_id"`
	Payload        map[string]any `json:"payload,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type Correlation struct {
	ID                  ID        `json:"id"`
	WorkspaceID         ID        `json:"workspace_id"`
	ConnectionID        ID        `json:"connection_id"`
	SourceIdentity      string    `json:"source_identity"`
	PublicationIdentity string    `json:"publication_identity"`
	TargetIdentity      string    `json:"target_identity,omitempty"`
	FlowExecutionID     ID        `json:"flow_execution_id,omitempty"`
	ProviderRequestID   string    `json:"provider_request_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

type IdempotencyKey struct {
	ID          ID         `json:"id"`
	WorkspaceID ID         `json:"workspace_id"`
	Scope       string     `json:"scope"`
	Key         string     `json:"key"`
	ClaimedAt   time.Time  `json:"claimed_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type SecretRef struct {
	ID          ID         `json:"secret_ref"`
	WorkspaceID ID         `json:"workspace_id"`
	Alias       string     `json:"alias"`
	Kind        SecretKind `json:"kind"`
}

func (r SecretRef) Validate() error {
	if err := requireID("id", r.ID); err != nil {
		return err
	}
	if err := requireWorkspaceID(r.WorkspaceID); err != nil {
		return err
	}
	if strings.TrimSpace(r.Alias) == "" {
		return fmt.Errorf("%w: secret alias is required", ErrInvalid)
	}
	if r.Kind == "" {
		return fmt.Errorf("%w: secret kind is required", ErrInvalid)
	}
	return nil
}
