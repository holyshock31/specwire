package controlplane

import (
	"context"
	"fmt"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/registry"
)

type RegistryStore interface {
	RegisterConnectorType(context.Context, domain.ID, domain.ConnectorType) error
	RegisterConnectorBehavior(context.Context, domain.ID, domain.ConnectorBehavior) error
	RegisterDataModel(context.Context, domain.ID, domain.DataModelDefinition) error
	ListConnectorTypes(context.Context, domain.ID) ([]domain.ConnectorType, error)
	ListConnectorBehaviors(context.Context, domain.ID) ([]domain.ConnectorBehavior, error)
	ListDataModels(context.Context, domain.ID) ([]domain.DataModelDefinition, error)
	SetConnectorTypeStatus(context.Context, domain.ID, domain.ID, domain.ConnectorStatus) error
	SetConnectorBehaviorStatus(context.Context, domain.ID, domain.ID, domain.ConnectorStatus) error
	SetDataModelStatus(context.Context, domain.ID, domain.ID, domain.ConnectorStatus) error
}

type RegistryService struct {
	store     RegistryStore
	adapters  registry.AdapterCatalog
	allowlist []string
}

func NewRegistryService(store RegistryStore, adapters registry.AdapterCatalog) (*RegistryService, error) {
	if store == nil || adapters == nil {
		return nil, fmt.Errorf("%w: registry service dependencies are required", domain.ErrInvalid)
	}
	return &RegistryService{store: store, adapters: adapters, allowlist: adapterOperations(adapters)}, nil
}

// CatalogForWorkspace builds a fresh registry view from durable definitions.
// Registry definitions are Workspace-owned, so a single process-wide catalog
// must not be reused for every request.
func (s *RegistryService) CatalogForWorkspace(ctx context.Context, workspaceID domain.ID) (flow.Catalog, error) {
	behaviors, err := s.store.ListConnectorBehaviors(ctx, workspaceID)
	if err != nil {
		return flow.Catalog{}, err
	}
	models, err := s.store.ListDataModels(ctx, workspaceID)
	if err != nil {
		return flow.Catalog{}, err
	}
	return flow.NewCatalog(behaviors, models, s.allowlist), nil
}

func (s *RegistryService) BehaviorForWorkspace(ctx context.Context, workspaceID domain.ID, key, version string) (domain.ConnectorBehavior, bool, error) {
	catalog, err := s.CatalogForWorkspace(ctx, workspaceID)
	if err != nil {
		return domain.ConnectorBehavior{}, false, err
	}
	item, ok := catalog.Behavior(key, version)
	return item, ok, nil
}

func adapterOperations(adapters registry.AdapterCatalog) []string {
	if provider, ok := adapters.(interface{ Operations() []string }); ok {
		return append([]string(nil), provider.Operations()...)
	}
	return nil
}

func (s *RegistryService) RegisterConnectorType(ctx context.Context, workspaceID domain.ID, item domain.ConnectorType) error {
	item.WorkspaceID = workspaceID
	return s.store.RegisterConnectorType(ctx, workspaceID, item)
}

func (s *RegistryService) RegisterConnectorBehavior(ctx context.Context, workspaceID domain.ID, item domain.ConnectorBehavior) error {
	if err := registry.ValidateBehavior(item, s.adapters); err != nil {
		return err
	}
	item.WorkspaceID = workspaceID
	return s.store.RegisterConnectorBehavior(ctx, workspaceID, item)
}

func (s *RegistryService) RegisterDataModel(ctx context.Context, workspaceID domain.ID, item domain.DataModelDefinition) error {
	if err := registry.ValidateDataModel(item); err != nil {
		return err
	}
	item.WorkspaceID = workspaceID
	return s.store.RegisterDataModel(ctx, workspaceID, item)
}

func (s *RegistryService) ListConnectorTypes(ctx context.Context, workspaceID domain.ID) ([]domain.ConnectorType, error) {
	return s.store.ListConnectorTypes(ctx, workspaceID)
}

func (s *RegistryService) ListConnectorBehaviors(ctx context.Context, workspaceID domain.ID) ([]domain.ConnectorBehavior, error) {
	return s.store.ListConnectorBehaviors(ctx, workspaceID)
}

func (s *RegistryService) ListDataModels(ctx context.Context, workspaceID domain.ID) ([]domain.DataModelDefinition, error) {
	return s.store.ListDataModels(ctx, workspaceID)
}

// AllowlistedAdapterOperations exposes the deployed adapter catalog to the
// admin UI as metadata only. Registration still goes through
// ValidateBehavior, so the client cannot introduce an executable operation.
func (s *RegistryService) AllowlistedAdapterOperations() []string {
	return append([]string(nil), s.allowlist...)
}

func (s *RegistryService) SetConnectorTypeStatus(ctx context.Context, workspaceID, itemID domain.ID, status domain.ConnectorStatus) error {
	return s.store.SetConnectorTypeStatus(ctx, workspaceID, itemID, status)
}

func (s *RegistryService) SetConnectorBehaviorStatus(ctx context.Context, workspaceID, itemID domain.ID, status domain.ConnectorStatus) error {
	return s.store.SetConnectorBehaviorStatus(ctx, workspaceID, itemID, status)
}

func (s *RegistryService) SetDataModelStatus(ctx context.Context, workspaceID, itemID domain.ID, status domain.ConnectorStatus) error {
	return s.store.SetDataModelStatus(ctx, workspaceID, itemID, status)
}
