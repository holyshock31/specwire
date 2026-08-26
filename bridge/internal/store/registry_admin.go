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

func (s *Store) SetConnectorTypeStatus(ctx context.Context, workspaceID, itemID domain.ID, status domain.ConnectorStatus) error {
	return s.setRegistryStatus(ctx, "connector_types", workspaceID, itemID, status)
}

func (s *Store) SetConnectorBehaviorStatus(ctx context.Context, workspaceID, itemID domain.ID, status domain.ConnectorStatus) error {
	return s.setRegistryStatus(ctx, "connector_behaviors", workspaceID, itemID, status)
}

func (s *Store) SetDataModelStatus(ctx context.Context, workspaceID, itemID domain.ID, status domain.ConnectorStatus) error {
	return s.setRegistryStatus(ctx, "data_models", workspaceID, itemID, status)
}

func (s *Store) setRegistryStatus(ctx context.Context, table string, workspaceID, itemID domain.ID, status domain.ConnectorStatus) error {
	if table != "connector_types" && table != "connector_behaviors" && table != "data_models" {
		return fmt.Errorf("%w: unsupported registry table", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	if itemID.Empty() {
		return fmt.Errorf("%w: registry item id is required", domain.ErrInvalid)
	}
	if !validConnectorStatus(status) {
		return fmt.Errorf("%w: unsupported registry status %q", domain.ErrInvalid, status)
	}
	var definition string
	err := s.db.QueryRowContext(ctx, `SELECT definition_json FROM `+table+` WHERE workspace_id = ? AND id = ?`, workspaceID, itemID).Scan(&definition)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: registry item %s", domain.ErrNotFound, itemID)
	}
	if err != nil {
		return fmt.Errorf("get registry definition: %w", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(definition), &decoded); err != nil {
		return fmt.Errorf("decode registry definition: %w", err)
	}
	decoded["status"] = status
	updated, err := json.Marshal(decoded)
	if err != nil {
		return fmt.Errorf("encode registry definition: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE `+table+` SET status = ?, definition_json = ? WHERE workspace_id = ? AND id = ?`, status, string(updated), workspaceID, itemID)
	if err != nil {
		return constraintError("update registry status", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: registry item %s", domain.ErrNotFound, itemID)
	}
	return nil
}

func validConnectorStatus(status domain.ConnectorStatus) bool {
	switch status {
	case domain.DefinitionDraft, domain.DefinitionPublished, domain.DefinitionDeprecated, domain.DefinitionDisabled:
		return true
	default:
		return false
	}
}
