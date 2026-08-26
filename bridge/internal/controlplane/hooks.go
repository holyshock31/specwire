package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/provider"
)

type HookStore interface {
	GetFlow(context.Context, domain.ID, domain.ID) (domain.Flow, error)
	GetConnection(context.Context, domain.ID, domain.ID) (domain.Connection, error)
	GetGitLabInstance(context.Context, domain.ID, domain.ID) (domain.GitLabInstance, error)
	GetHookByProject(context.Context, domain.ID, domain.ID, string) (domain.Hook, error)
	UpsertHook(context.Context, domain.Hook) (domain.Hook, error)
	UpsertHookRoute(context.Context, domain.HookRoute) (domain.HookRoute, error)
	DisableHookRoutesForFlow(context.Context, domain.ID, domain.ID, int) error
	EnsureManagedResource(context.Context, domain.ManagedResource) (domain.ManagedResource, error)
}

type BehaviorResolver interface {
	Behavior(string, string) (domain.ConnectorBehavior, bool)
}

type HookSecretStore interface {
	CredentialResolver
	Put(context.Context, domain.SecretRef, []byte) error
}

type HookReconciler struct {
	store         HookStore
	gitlab        provider.GitLab
	vault         HookSecretStore
	behaviors     BehaviorResolver
	catalog       flow.CatalogResolver
	gitlabCredRef *domain.SecretRef
	hookURL       string
}

func NewHookReconciler(store HookStore, gitlab provider.GitLab, vault HookSecretStore, behaviors BehaviorResolver, hookURL string) (*HookReconciler, error) {
	if store == nil || gitlab == nil || behaviors == nil {
		return nil, fmt.Errorf("%w: Hook reconciler dependencies are required", domain.ErrInvalid)
	}
	if strings.TrimSpace(hookURL) == "" {
		return nil, fmt.Errorf("%w: Hook URL is required", domain.ErrInvalid)
	}
	return &HookReconciler{store: store, gitlab: gitlab, vault: vault, behaviors: behaviors, hookURL: strings.TrimSpace(hookURL)}, nil
}

func (r *HookReconciler) SetGitLabCredentialRef(ref *domain.SecretRef) { r.gitlabCredRef = ref }

func (r *HookReconciler) SetCatalogResolver(resolver flow.CatalogResolver) { r.catalog = resolver }

// ActivateInputFlow makes the provider Hook and the durable route ready as a
// single idempotent reconciliation operation.  One source project can have
// many routes, but only one Hook record/provider Hook is used.
func (r *HookReconciler) ActivateInputFlow(ctx context.Context, version domain.FlowVersion) error {
	flowRecord, err := r.store.GetFlow(ctx, version.WorkspaceID, version.FlowID)
	if err != nil {
		return err
	}
	connection, err := r.store.GetConnection(ctx, version.WorkspaceID, flowRecord.ConnectionID)
	if err != nil {
		return err
	}
	input, behavior, err := r.inputNode(ctx, version.WorkspaceID, version.Graph)
	if err != nil {
		return err
	}
	hook, err := r.ensureHook(ctx, connection, input, behavior)
	if err != nil {
		return err
	}
	route, err := r.store.UpsertHookRoute(ctx, domain.HookRoute{
		ID:              domain.NewID(),
		WorkspaceID:     version.WorkspaceID,
		ConnectionID:    connection.ID,
		SourceProject:   connection.SourceGitLabProject,
		BehaviorKey:     behavior.Key,
		BehaviorVersion: behavior.Version,
		FlowID:          version.FlowID,
		FlowVersion:     version.Version,
		HookRef:         hook.ID,
		Status:          domain.HookActive,
	})
	if err != nil {
		return err
	}
	_, err = r.store.EnsureManagedResource(ctx, domain.ManagedResource{
		ID:             domain.NewID(),
		WorkspaceID:    version.WorkspaceID,
		ConnectionID:   connection.ID,
		Kind:           domain.ResourceHook,
		Provider:       domain.ProviderGitLab,
		InstanceID:     connection.SourceGitLabProject.InstanceID,
		ExternalID:     hook.ExternalID,
		Ownership:      domain.OwnershipManaged,
		ManagementMark: "specwire-managed",
		Status:         string(route.Status),
		Snapshot:       map[string]any{"url": r.hookURLForInstance(connection.SourceGitLabProject.InstanceID), "route_id": route.ID, "flow_version": version.Version},
	})
	return err
}

func (r *HookReconciler) PauseInputFlow(ctx context.Context, version domain.FlowVersion) error {
	err := r.store.DisableHookRoutesForFlow(ctx, version.WorkspaceID, version.FlowID, version.Version)
	if err != nil && strings.Contains(err.Error(), domain.ErrNotFound.Error()) {
		return nil
	}
	return err
}

func (r *HookReconciler) inputNode(ctx context.Context, workspaceID domain.ID, graph domain.FlowGraph) (domain.FlowNode, domain.ConnectorBehavior, error) {
	catalog := r.behaviors
	if r.catalog != nil {
		resolved, err := r.catalog.CatalogForWorkspace(ctx, workspaceID)
		if err != nil {
			return domain.FlowNode{}, domain.ConnectorBehavior{}, err
		}
		catalog = resolved
	}
	for _, node := range graph.Nodes {
		if node.Kind != domain.NodeConnector || node.Connector == nil {
			continue
		}
		behavior, ok := catalog.Behavior(node.Connector.BehaviorKey, node.Connector.BehaviorVersion)
		if ok && behavior.Direction == domain.DirectionInput {
			return node, behavior, nil
		}
	}
	return domain.FlowNode{}, domain.ConnectorBehavior{}, fmt.Errorf("%w: published Flow has no input ConnectorNode", domain.ErrInvalid)
}

func (r *HookReconciler) ensureHook(ctx context.Context, connection domain.Connection, input domain.FlowNode, behavior domain.ConnectorBehavior) (domain.Hook, error) {
	gitlabInstance, err := r.store.GetGitLabInstance(ctx, connection.WorkspaceID, connection.SourceGitLabProject.InstanceID)
	if err != nil {
		return domain.Hook{}, err
	}
	hook, err := r.store.GetHookByProject(ctx, connection.WorkspaceID, connection.SourceGitLabProject.InstanceID, connection.SourceGitLabProject.ExternalID)
	if err != nil && !isNotFound(err) {
		return domain.Hook{}, err
	}
	var signingRef domain.SecretRef
	var signingToken []byte
	if err == nil && hook.SigningRef != nil {
		signingRef = *hook.SigningRef
		if r.vault == nil {
			return domain.Hook{}, fmt.Errorf("%w: Hook secret resolver is not configured", domain.ErrInvalid)
		}
		signingToken, err = r.vault.Resolve(ctx, signingRef)
		if err != nil {
			return domain.Hook{}, err
		}
	} else {
		signingToken, err = newSigningToken()
		if err != nil {
			return domain.Hook{}, err
		}
		signingRef = domain.SecretRef{ID: domain.NewID(), WorkspaceID: connection.WorkspaceID, Alias: "hook/" + string(connection.SourceGitLabProject.InstanceID) + "/" + connection.SourceGitLabProject.ExternalID, Kind: domain.SecretHookSigning}
		if r.vault == nil {
			return domain.Hook{}, fmt.Errorf("%w: Hook secret store is not configured", domain.ErrInvalid)
		}
		if err := r.vault.Put(ctx, signingRef, signingToken); err != nil {
			return domain.Hook{}, err
		}
	}
	defer clearBytes(signingToken)
	project := connection.SourceGitLabProject
	providerProject := provider.GitLabProject{InstanceID: project.InstanceID, ExternalID: project.ExternalID, FullPath: project.FullPath, Name: project.Name, WebURL: project.WebURL, SSHURL: project.SSHURL, HTTPSURL: project.HTTPSURL}
	gitlabCredential, cleanup, err := r.resolveGitLabCredential(ctx, connection)
	if err != nil {
		return domain.Hook{}, err
	}
	defer cleanup()
	hookResult, err := r.gitlab.EnsureHook(ctx, gitlabInstance, providerProject, provider.HookSpec{URL: r.hookURLForInstance(project.InstanceID), Events: eventsForBehavior(behavior, input), SigningRef: signingRef, SigningToken: signingToken, ManagementMark: "specwire-managed"}, gitlabCredential)
	if err != nil {
		return domain.Hook{}, err
	}
	recordAudit(ctx, r.store, domain.AuditEvent{ID: domain.NewID(), WorkspaceID: connection.WorkspaceID, Action: "provider.gitlab.hook.ensure", EntityType: "connection", EntityID: connection.ID, Payload: map[string]any{
		"workspace_id": connection.WorkspaceID, "provider": "gitlab", "operation": "ensure_hook", "external_id": hookResult.ExternalID, "request_id": hookResult.RequestID, "created": hookResult.Created, "adopted": hookResult.Adopted,
	}})
	hookID := hookResult.ExternalID
	if hookID == "" {
		return domain.Hook{}, fmt.Errorf("%w: GitLab Hook adapter returned no external ID", domain.ErrInvalid)
	}
	return r.store.UpsertHook(ctx, domain.Hook{ID: hook.ID, WorkspaceID: connection.WorkspaceID, ConnectionID: connection.ID, Provider: domain.ProviderGitLab, InstanceID: project.InstanceID, SourceProjectExternalID: project.ExternalID, ExternalID: hookID, SigningRef: &signingRef, Status: domain.HookActive})
}

func (r *HookReconciler) resolveGitLabCredential(ctx context.Context, connection domain.Connection) (*provider.Credential, func(), error) {
	ref := r.gitlabCredRef
	if ref == nil && connection.SourceGitLabProject.GroupID != "" {
		if groupStore, ok := r.store.(interface {
			GetGitLabGroupBindingByGroup(context.Context, domain.ID, domain.ID, string) (domain.GitLabGroupBinding, error)
		}); ok {
			binding, err := groupStore.GetGitLabGroupBindingByGroup(ctx, connection.WorkspaceID, connection.SourceGitLabProject.InstanceID, connection.SourceGitLabProject.GroupID)
			if err == nil {
				ref = binding.CredentialRef
			} else if !errors.Is(err, domain.ErrNotFound) {
				return nil, func() {}, err
			}
		}
	}
	if ref == nil {
		return nil, func() {}, nil
	}
	if r.vault == nil {
		return nil, func() {}, fmt.Errorf("%w: GitLab credential resolver is not configured", domain.ErrInvalid)
	}
	material, err := r.vault.Resolve(ctx, *ref)
	if err != nil {
		return nil, func() {}, err
	}
	return &provider.Credential{Ref: *ref, Material: material}, func() { clearBytes(material) }, nil
}

func (r *HookReconciler) hookURLForInstance(instanceID domain.ID) string {
	parsed, err := url.Parse(r.hookURL)
	if err != nil {
		return r.hookURL
	}
	query := parsed.Query()
	query.Set("instance_id", string(instanceID))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func eventsForBehavior(behavior domain.ConnectorBehavior, node domain.FlowNode) []string {
	key := strings.ToLower(behavior.Key + " " + node.Name)
	if strings.Contains(key, "issue") {
		return []string{"Issue Hook"}
	}
	if strings.Contains(key, "push") || strings.Contains(key, "archive") {
		return []string{"Push Hook"}
	}
	return []string{"Issue Hook", "Push Hook"}
}

func newSigningToken() ([]byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate Hook signing token: %w", err)
	}
	token := []byte("whsec_" + base64.StdEncoding.EncodeToString(raw))
	clearBytes(raw)
	return token, nil
}

func isNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }

var _ flow.RouteActivator = (*HookReconciler)(nil)
