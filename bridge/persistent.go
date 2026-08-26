package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"specwire/bridge/internal/auth"
	"specwire/bridge/internal/controlplane"
	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/httpapi"
	"specwire/bridge/internal/provider"
	gitlabprovider "specwire/bridge/internal/provider/gitlab"
	multicaprovider "specwire/bridge/internal/provider/multica"
	"specwire/bridge/internal/registry"
	runtimenew "specwire/bridge/internal/runtime"
	securitynew "specwire/bridge/internal/security"
	foundationstore "specwire/bridge/internal/store"
)

type persistentApplication struct {
	store   *foundationstore.Store
	api     *httpapi.Server
	ingress http.Handler
	worker  *runtimenew.Worker
}

func newPersistentApplication(cfg *Config) (*persistentApplication, error) {
	store, err := foundationstore.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = store.Close()
		}
	}()

	masterKey, err := loadSecretMasterKey(filepath.Join(filepath.Dir(cfg.DBPath), ".specwire-master.key"))
	if err != nil {
		return nil, err
	}
	vault, err := securitynew.NewVault(store, masterKey)
	clearBytesLocal(masterKey)
	if err != nil {
		return nil, err
	}

	gitlab := gitlabprovider.NewClient(&http.Client{Timeout: cfg.CLITimeout}, cfg.GitLabToken)
	multica := multicaprovider.NewClient(multicaprovider.Options{Profile: cfg.MulticaProfile, Timeout: cfg.CLITimeout})
	probe, err := controlplane.NewProviderEndpointProbe(gitlab, multica, vault)
	if err != nil {
		return nil, err
	}
	endpoints, err := controlplane.NewEndpointService(store, probe)
	if err != nil {
		return nil, err
	}
	bundle, err := registry.LoadBuiltins()
	if err != nil {
		return nil, err
	}
	allowlisted := []string{"gitlab.events.issue", "gitlab.events.push", "multica.issue.create", "multica.issue.status"}
	catalog := flow.NewCatalog(bundle.Behaviors, bundle.DataModels, allowlisted)
	hooks, err := controlplane.NewHookReconciler(store, gitlab, vault, catalog, cfg.WebhookURL)
	if err != nil {
		return nil, err
	}
	connections, err := controlplane.NewConnectionService(store, gitlab, multica, vault)
	if err != nil {
		return nil, err
	}
	connections.SetPublicHookURL(cfg.WebhookURL)
	selection, err := controlplane.NewSelectionService(connections, store)
	if err != nil {
		return nil, err
	}
	registryService, err := controlplane.NewRegistryService(store, registry.AdapterAllowlist{
		"gitlab.events.issue":  true,
		"gitlab.events.push":   true,
		"multica.issue.create": true,
		"multica.issue.status": true,
	})
	if err != nil {
		return nil, err
	}
	flows, err := flow.NewService(store, catalog)
	if err != nil {
		return nil, err
	}
	flows.SetRouteActivator(hooks)
	credentialResolver, err := runtimenew.NewStoredGitLabCredentialResolver(store, vault)
	if err != nil {
		return nil, err
	}
	credentialService, err := controlplane.NewCredentialService(store, vault, probe)
	if err != nil {
		return nil, err
	}
	executor, err := runtimenew.NewExecutor(store, gitlab, multica, vault, catalog, runtimenew.WithGitLabCredentialResolver(credentialResolver))
	if err != nil {
		return nil, err
	}
	ingress, err := runtimenew.NewIngress(store, vault, catalog)
	if err != nil {
		return nil, err
	}
	worker, err := runtimenew.NewWorker(store, executor, "bridge-"+domain.NewID().String())
	if err != nil {
		return nil, err
	}
	localAuth, err := auth.NewLocalProvider(store)
	if err != nil {
		return nil, err
	}
	api, err := httpapi.NewServer(localAuth, store, store, endpoints)
	if err != nil {
		return nil, err
	}
	api.SetIntegrationServices(httpapi.IntegrationServices{Store: store, Selection: selection, Connections: connections, Credentials: credentialService, Flows: flows, Registry: registryService})

	// The old .env path is still required during this change's compatibility
	// window.  Import it once per source project and make the persistent route
	// model authoritative afterwards.
	if err := importLegacyConfiguration(context.Background(), cfg, store, vault, gitlab, multica, flows, hooks, bundle); err != nil {
		return nil, err
	}
	closeOnError = false
	return &persistentApplication{store: store, api: api, ingress: ingress, worker: worker}, nil
}

func loadSecretMasterKey(path string) ([]byte, error) {
	if encoded := strings.TrimSpace(os.Getenv("SPECWIRE_SECRET_MASTER_KEY")); encoded != "" {
		if value, err := base64.StdEncoding.DecodeString(encoded); err == nil && len(value) == 32 {
			return value, nil
		}
		if value, err := hex.DecodeString(encoded); err == nil && len(value) == 32 {
			return value, nil
		}
		return nil, fmt.Errorf("SPECWIRE_SECRET_MASTER_KEY must be base64 or hex encoded 32 bytes")
	}
	if value, err := os.ReadFile(path); err == nil {
		if len(value) != 32 {
			return nil, fmt.Errorf("secret master key file %s must contain 32 bytes", path)
		}
		return value, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read secret master key file: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate secret master key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		clearBytesLocal(key)
		return nil, fmt.Errorf("create secret master key directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if _, writeErr := file.Write(key); writeErr != nil {
			_ = file.Close()
			_ = os.Remove(path)
			clearBytesLocal(key)
			return nil, fmt.Errorf("write secret master key: %w", writeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			clearBytesLocal(key)
			return nil, fmt.Errorf("close secret master key: %w", closeErr)
		}
		return key, nil
	}
	clearBytesLocal(key)
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create secret master key file: %w", err)
	}
	value, readErr := os.ReadFile(path)
	if readErr != nil || len(value) != 32 {
		return nil, fmt.Errorf("read concurrently created secret master key: %w", readErr)
	}
	return value, nil
}

func clearBytesLocal(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func importLegacyConfiguration(ctx context.Context, cfg *Config, store *foundationstore.Store, vault *securitynew.Vault, gitlab provider.GitLab, multica provider.Multica, flows *flow.Service, hooks *controlplane.HookReconciler, bundle registry.Bundle) error {
	workspace, err := store.GetWorkspaceBySlug(ctx, "default")
	if errors.Is(err, domain.ErrNotFound) {
		workspace = domain.Workspace{ID: domain.NewID(), Slug: "default", Name: "Default Workspace", Status: domain.WorkspaceActive}
		if err := store.CreateWorkspace(ctx, workspace); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := store.BootstrapRegistry(ctx, workspace.ID, bundle); err != nil {
		return err
	}
	if err := flows.SeedBuiltins(ctx, workspace.ID); err != nil {
		return err
	}

	gitlabRef, err := ensureLegacyGitLabInstance(ctx, cfg, store, vault, workspace.ID)
	if err != nil {
		return err
	}
	multicaInstance, err := ensureLegacyMulticaInstance(ctx, cfg, store)
	if err != nil {
		return err
	}
	if len(cfg.AllowedProjects) == 0 {
		return nil
	}
	paths := make([]string, 0, len(cfg.AllowedProjects))
	for path := range cfg.AllowedProjects {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := importLegacyProject(ctx, cfg, store, vault, gitlab, multica, flows, hooks, workspace, gitlabRef, multicaInstance, path); err != nil {
			// A provider being unavailable must not prevent the admin UI from
			// starting; the operation can be retried through onboarding later.
			slog.Warn("legacy project import skipped", "project", path, "error", err)
		}
	}
	return nil
}

func ensureLegacyGitLabInstance(ctx context.Context, cfg *Config, store *foundationstore.Store, vault *securitynew.Vault, workspaceID domain.ID) (*domain.SecretRef, error) {
	var ref *domain.SecretRef
	if strings.TrimSpace(cfg.GitLabToken) != "" {
		value := domain.SecretRef{ID: "secret-legacy-gitlab", WorkspaceID: workspaceID, Alias: "legacy/gitlab", Kind: domain.SecretGroupCredential}
		if err := vault.Put(ctx, value, []byte(cfg.GitLabToken)); err != nil {
			return nil, err
		}
		ref = &value
	}
	instance := domain.GitLabInstance{ID: "gitlab-legacy", WorkspaceID: workspaceID, Name: "GitLab (legacy)", BaseURL: cfg.GitLabURL, ExternalID: "legacy", CredentialRef: ref, Status: domain.EndpointActive}
	if err := store.CreateGitLabInstance(ctx, instance); err != nil && !errors.Is(err, domain.ErrConflict) {
		return nil, err
	}
	loaded, err := store.GetGitLabInstance(ctx, workspaceID, instance.ID)
	if err != nil {
		return nil, err
	}
	return loaded.CredentialRef, nil
}

func ensureLegacyMulticaInstance(ctx context.Context, cfg *Config, store *foundationstore.Store) (domain.MulticaInstance, error) {
	instance := domain.MulticaInstance{ID: "multica-legacy", WorkspaceID: "default", Name: "Multica (legacy)", BaseURL: getenv("MULTICA_SERVER_URL", "http://host.docker.internal:28080"), ExternalID: "legacy", Status: domain.EndpointActive}
	workspace, err := store.GetWorkspaceBySlug(ctx, "default")
	if err != nil {
		return domain.MulticaInstance{}, err
	}
	instance.WorkspaceID = workspace.ID
	if err := store.CreateMulticaInstance(ctx, instance); err != nil && !errors.Is(err, domain.ErrConflict) {
		return domain.MulticaInstance{}, err
	}
	return store.GetMulticaInstance(ctx, workspace.ID, instance.ID)
}

func importLegacyProject(ctx context.Context, cfg *Config, store *foundationstore.Store, vault *securitynew.Vault, gitlab provider.GitLab, multica provider.Multica, flows *flow.Service, hooks *controlplane.HookReconciler, workspace domain.Workspace, gitlabRef *domain.SecretRef, multicaInstance domain.MulticaInstance, path string) error {
	sourceInstance, err := store.GetGitLabInstance(ctx, workspace.ID, "gitlab-legacy")
	if err != nil {
		return err
	}
	sourceCredential, cleanup, err := legacyCredential(ctx, vault, gitlabRef)
	if err != nil {
		return err
	}
	defer cleanup()
	source, err := gitlab.GetProject(ctx, sourceInstance, path, sourceCredential)
	if err != nil {
		return err
	}
	groupID := strings.TrimSpace(source.GroupID)
	if groupID == "" {
		groupID = strings.SplitN(source.FullPath, "/", 2)[0]
	}
	if groupID == "" {
		return fmt.Errorf("GitLab project %s has no group identity", path)
	}
	group, err := store.GetGitLabGroupBindingByGroup(ctx, workspace.ID, sourceInstance.ID, groupID)
	if errors.Is(err, domain.ErrNotFound) {
		group = domain.GitLabGroupBinding{ID: domain.NewID(), WorkspaceID: workspace.ID, GitLabInstanceID: sourceInstance.ID, ExternalGroupID: groupID, FullPath: firstLegacyNonEmpty(strings.TrimSuffix(source.FullPath, "/"+filepath.Base(source.FullPath)), groupID), CredentialRef: gitlabRef, InheritSubgroups: true, Status: domain.EndpointActive}
		if err := store.CreateGitLabGroupBinding(ctx, group); err != nil && !errors.Is(err, domain.ErrConflict) {
			return err
		}
		group, err = store.GetGitLabGroupBindingByGroup(ctx, workspace.ID, sourceInstance.ID, groupID)
	}
	if err != nil {
		return err
	}
	target, targetWorkspace, err := resolveLegacyTarget(ctx, cfg, multica, multicaInstance, path)
	if err != nil {
		return err
	}
	connection, err := store.FindActiveConnectionBySource(ctx, workspace.ID, sourceInstance.ID, source.ExternalID)
	if errors.Is(err, domain.ErrNotFound) {
		service, serviceErr := controlplane.NewConnectionService(store, gitlab, multica, vault)
		if serviceErr != nil {
			return serviceErr
		}
		service.SetPublicHookURL(cfg.WebhookURL)
		result, onboardErr := service.Onboard(ctx, controlplane.OnboardingRequest{OperationID: legacyOperationID(workspace.ID, source.ExternalID), WorkspaceID: workspace.ID, SourceGitLabInstance: sourceInstance, SourceProjectExternalID: source.ExternalID, Group: group, GitLabCredentialRef: gitlabRef, TargetMulticaInstance: multicaInstance, TargetWorkspace: targetWorkspace, TargetProject: &target, MulticaCredentialRef: nil, PreferSSH: true})
		if onboardErr != nil {
			return onboardErr
		}
		connection = result.Connection
	} else if err != nil {
		return err
	}
	if err := ensureLegacyHook(ctx, cfg, store, vault, gitlab, sourceInstance, source, connection, gitlabRef); err != nil {
		return err
	}
	if err := ensureLegacyFlow(ctx, store, flows, workspace.ID, connection.ID, flow.TemplatePublishChange, "Publish Change"); err != nil {
		return err
	}
	if err := ensureLegacyFlow(ctx, store, flows, workspace.ID, connection.ID, flow.TemplateCompleteArchive, "Complete Archive"); err != nil {
		return err
	}
	_ = hooks
	return nil
}

func legacyCredential(ctx context.Context, vault *securitynew.Vault, ref *domain.SecretRef) (*provider.Credential, func(), error) {
	if ref == nil {
		return nil, func() {}, nil
	}
	material, err := vault.Resolve(ctx, *ref)
	if err != nil {
		return nil, func() {}, err
	}
	return &provider.Credential{Ref: *ref, Material: material}, func() { clearBytesLocal(material) }, nil
}

func resolveLegacyTarget(ctx context.Context, cfg *Config, multica provider.Multica, instance domain.MulticaInstance, sourcePath string) (provider.MulticaProject, provider.MulticaWorkspace, error) {
	targetID := cfg.MulticaProjectID
	if mapped := cfg.ProjectMap[sourcePath]; mapped != "" {
		targetID = mapped
	}
	if targetID == "" {
		return provider.MulticaProject{}, provider.MulticaWorkspace{}, fmt.Errorf("legacy project %s has no Multica target", sourcePath)
	}
	workspaces, err := multica.ListWorkspaces(ctx, instance, "", nil)
	if err != nil {
		return provider.MulticaProject{}, provider.MulticaWorkspace{}, err
	}
	for _, candidate := range workspaces {
		projects, listErr := multica.ListProjects(ctx, instance, candidate, "", nil)
		if listErr != nil {
			continue
		}
		for _, project := range projects {
			if project.ExternalID == targetID {
				return project, candidate, nil
			}
		}
	}
	if configuredWorkspace := strings.TrimSpace(os.Getenv("SPECWIRE_MULTICA_WORKSPACE_ID")); configuredWorkspace != "" {
		candidate := provider.MulticaWorkspace{InstanceID: instance.ID, ExternalID: configuredWorkspace, Name: firstLegacyNonEmpty(os.Getenv("SPECWIRE_MULTICA_WORKSPACE_NAME"), configuredWorkspace)}
		return provider.MulticaProject{InstanceID: instance.ID, WorkspaceID: configuredWorkspace, ExternalID: targetID, Title: sourcePath}, candidate, nil
	}
	return provider.MulticaProject{}, provider.MulticaWorkspace{}, fmt.Errorf("legacy Multica project %s was not found in a workspace", targetID)
}

func ensureLegacyHook(ctx context.Context, cfg *Config, store *foundationstore.Store, vault *securitynew.Vault, gitlab provider.GitLab, instance domain.GitLabInstance, source provider.GitLabProject, connection domain.Connection, credentialRef *domain.SecretRef) error {
	if len(cfg.WebhookSecrets) == 0 {
		return nil
	}
	token := []byte(cfg.WebhookSecrets[0])
	refID := "legacy-hook-" + shortHash(source.ExternalID)
	ref := domain.SecretRef{ID: domain.ID(refID), WorkspaceID: connection.WorkspaceID, Alias: "legacy/hook/" + source.ExternalID, Kind: domain.SecretHookSigning}
	if err := vault.Put(ctx, ref, token); err != nil {
		return err
	}
	credential, cleanup, err := legacyCredential(ctx, vault, credentialRef)
	if err != nil {
		return err
	}
	defer cleanup()
	hookResult, err := gitlab.EnsureHook(ctx, instance, source, provider.HookSpec{URL: hookURLWithInstance(cfg.WebhookURL, instance.ID), Events: []string{"Issue Hook", "Push Hook"}, SigningRef: ref, SigningToken: token, ManagementMark: "specwire-managed"}, credential)
	if err != nil {
		return err
	}
	hook, lookupErr := store.GetHookByProject(ctx, connection.WorkspaceID, instance.ID, source.ExternalID)
	if errors.Is(lookupErr, domain.ErrNotFound) {
		hook.ID = domain.NewID()
	} else if lookupErr != nil {
		return lookupErr
	}
	hook.WorkspaceID = connection.WorkspaceID
	hook.ConnectionID = connection.ID
	hook.Provider = domain.ProviderGitLab
	hook.InstanceID = instance.ID
	hook.SourceProjectExternalID = source.ExternalID
	hook.ExternalID = hookResult.ExternalID
	hook.SigningRef = &ref
	hook.Status = domain.HookActive
	stored, err := store.UpsertHook(ctx, hook)
	if err != nil {
		return err
	}
	_, err = store.EnsureManagedResource(ctx, domain.ManagedResource{ID: domain.NewID(), WorkspaceID: connection.WorkspaceID, ConnectionID: connection.ID, Kind: domain.ResourceHook, Provider: domain.ProviderGitLab, InstanceID: instance.ID, ExternalID: stored.ExternalID, Ownership: domain.OwnershipAdopted, ManagementMark: "specwire-managed", Status: string(domain.HookActive), Snapshot: map[string]any{"url": hookURLWithInstance(cfg.WebhookURL, instance.ID)}})
	return err
}

func ensureLegacyFlow(ctx context.Context, store *foundationstore.Store, flows *flow.Service, workspaceID, connectionID domain.ID, templateKey, name string) error {
	items, err := store.ListFlows(ctx, workspaceID, connectionID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Name != name {
			continue
		}
		if item.Status == domain.FlowPublished {
			return nil
		}
		_, _, err := flows.Publish(ctx, workspaceID, item.ID, "")
		return err
	}
	item, err := flows.CreateFromTemplate(ctx, workspaceID, connectionID, "", templateKey, "1.0.0", name)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil
		}
		return err
	}
	_, _, err = flows.Publish(ctx, workspaceID, item.ID, "")
	return err
}

func legacyOperationID(workspaceID domain.ID, sourceID string) domain.ID {
	return domain.ID("legacy-onboard-" + shortHash(string(workspaceID)+":"+sourceID))
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func hookURLWithInstance(base string, instanceID domain.ID) string {
	separator := "?"
	if strings.Contains(base, "?") {
		separator = "&"
	}
	return base + separator + "instance_id=" + string(instanceID)
}

func firstLegacyNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
