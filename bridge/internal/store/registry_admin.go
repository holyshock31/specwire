package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"specwire/bridge/internal/domain"
)

func (s *Store) RegisterConnectorType(ctx context.Context, workspaceID domain.ID, item domain.ConnectorType) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	if item.Key == "" || item.Version == "" || item.DisplayName == "" || item.Provider == "" {
		return fmt.Errorf("%w: connector type metadata is incomplete", domain.ErrInvalid)
	}
	definition, err := json.Marshal(item)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = ensureConnectorType(ctx, tx, workspaceID, item, string(definition))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RegisterConnectorBehavior(ctx context.Context, workspaceID domain.ID, item domain.ConnectorBehavior) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	if item.Key == "" || item.Version == "" || item.ConnectorTypeKey == "" || item.ConnectorTypeVersion == "" {
		return fmt.Errorf("%w: connector behavior metadata is incomplete", domain.ErrInvalid)
	}
	definition, err := json.Marshal(item)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var typeID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM connector_types WHERE workspace_id = ? AND key = ? AND version = ?`, workspaceID, item.ConnectorTypeKey, item.ConnectorTypeVersion).Scan(&typeID); err == sql.ErrNoRows {
		return fmt.Errorf("%w: connector type is not registered", domain.ErrInvalid)
	} else if err != nil {
		return err
	}
	if err := ensureConnectorBehavior(ctx, tx, workspaceID, domain.ID(typeID), item, string(definition)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RegisterDataModel(ctx context.Context, workspaceID domain.ID, item domain.DataModelDefinition) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	if item.Key == "" || item.Version == "" || item.Schema == nil {
		return fmt.Errorf("%w: data model definition is incomplete", domain.ErrInvalid)
	}
	definition, err := json.Marshal(item)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureDataModel(ctx, tx, workspaceID, item, string(definition)); err != nil {
		return err
	}
	return tx.Commit()
}
