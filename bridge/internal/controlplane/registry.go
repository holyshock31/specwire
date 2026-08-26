package controlplane

import (
	"context"
	"fmt"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/registry"
)

type RegistryStore interface {
	RegisterConnectorType(context.Context, domain.ID, domain.ConnectorType) error
	RegisterConnectorBehavior(context.Context, domain.ID, domain.ConnectorBehavior) error
	RegisterDataModel(context.Context, domain.ID, domain.DataModelDefinition) error
	ListConnectorBehaviors(context.Context, domain.ID) ([]domain.ConnectorBehavior, error)
	ListDataModels(context.Context, domain.ID) ([]domain.DataModelDefinition, error)
}

type RegistryService struct {
	store    RegistryStore
	adapters registry.AdapterCatalog
}

func NewRegistryService(store RegistryStore, adapters registry.AdapterCatalog) (*RegistryService, error) {
	if store == nil || adapters == nil {
		return nil, fmt.Errorf("%w: registry service dependencies are required", domain.ErrInvalid)
	}
	return &RegistryService{store: store, adapters: adapters}, nil
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
	item.WorkspaceID = workspaceID
	return s.store.RegisterDataModel(ctx, workspaceID, item)
}
