package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/registry"
)

type RegistryCounts struct {
	ConnectorTypes int
	Behaviors      int
	DataModels     int
}

func (s *Store) BootstrapRegistry(ctx context.Context, workspaceID domain.ID, bundle registry.Bundle) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	if err := bundle.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin registry bootstrap: %w", err)
	}
	defer tx.Rollback()

	typeIDs := make(map[string]domain.ID, len(bundle.ConnectorTypes))
	for _, item := range bundle.ConnectorTypes {
		definition, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("marshal connector type %s: %w", item.Key, err)
		}
		id, err := ensureConnectorType(ctx, tx, workspaceID, item, string(definition))
		if err != nil {
			return err
		}
		typeIDs[registry.StableKey(item.Key, item.Version)] = id
	}
	for _, item := range bundle.Behaviors {
		typeID, ok := typeIDs[registry.StableKey(item.ConnectorTypeKey, item.ConnectorTypeVersion)]
		if !ok {
			return fmt.Errorf("%w: connector type %s not bootstrapped", domain.ErrInvalid, item.ConnectorTypeKey)
		}
		definition, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("marshal connector behavior %s: %w", item.Key, err)
		}
		if err := ensureConnectorBehavior(ctx, tx, workspaceID, typeID, item, string(definition)); err != nil {
			return err
		}
	}
	for _, item := range bundle.DataModels {
		definition, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("marshal data model %s: %w", item.Key, err)
		}
		if err := ensureDataModel(ctx, tx, workspaceID, item, string(definition)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit registry bootstrap: %w", err)
	}
	return nil
}

func ensureConnectorType(ctx context.Context, tx *sql.Tx, workspaceID domain.ID, item domain.ConnectorType, definition string) (domain.ID, error) {
	var id string
	var existing string
	err := tx.QueryRowContext(ctx, `SELECT id, definition_json FROM connector_types
		WHERE workspace_id = ? AND key = ? AND version = ?`, workspaceID, item.Key, item.Version).Scan(&id, &existing)
	if err == nil {
		if existing != definition {
			return "", fmt.Errorf("%w: connector type %s", domain.ErrImmutable, registry.StableKey(item.Key, item.Version))
		}
		return domain.ID(id), nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("inspect connector type %s: %w", item.Key, err)
	}
	newID := item.ID
	if newID.Empty() {
		newID = domain.NewID()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO connector_types
		(id, workspace_id, key, version, display_name, provider, status, definition_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, newID, workspaceID, item.Key, item.Version,
		item.DisplayName, item.Provider, item.Status, definition)
	if err != nil {
		return "", constraintError("bootstrap connector type", err)
	}
	return newID, nil
}

func ensureConnectorBehavior(ctx context.Context, tx *sql.Tx, workspaceID, typeID domain.ID, item domain.ConnectorBehavior, definition string) error {
	var id, existing string
	err := tx.QueryRowContext(ctx, `SELECT id, definition_json FROM connector_behaviors
		WHERE workspace_id = ? AND key = ? AND version = ?`, workspaceID, item.Key, item.Version).Scan(&id, &existing)
	if err == nil {
		if existing != definition {
			return fmt.Errorf("%w: connector behavior %s", domain.ErrImmutable, registry.StableKey(item.Key, item.Version))
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("inspect connector behavior %s: %w", item.Key, err)
	}
	newID := item.ID
	if newID.Empty() {
		newID = domain.NewID()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO connector_behaviors
		(id, workspace_id, connector_type_id, connector_type_key, connector_type_version,
		key, version, display_name, direction, adapter_operation, status, definition_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, newID, workspaceID, typeID,
		item.ConnectorTypeKey, item.ConnectorTypeVersion, item.Key, item.Version,
		item.DisplayName, item.Direction, item.AdapterOperation, item.Status, definition)
	if err != nil {
		return constraintError("bootstrap connector behavior", err)
	}
	return nil
}

func ensureDataModel(ctx context.Context, tx *sql.Tx, workspaceID domain.ID, item domain.DataModelDefinition, definition string) error {
	var id, existing string
	err := tx.QueryRowContext(ctx, `SELECT id, definition_json FROM data_models
		WHERE workspace_id = ? AND key = ? AND version = ?`, workspaceID, item.Key, item.Version).Scan(&id, &existing)
	if err == nil {
		if existing != definition {
			return fmt.Errorf("%w: data model %s", domain.ErrImmutable, registry.StableKey(item.Key, item.Version))
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("inspect data model %s: %w", item.Key, err)
	}
	newID := item.ID
	if newID.Empty() {
		newID = domain.NewID()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO data_models
		(id, workspace_id, key, version, display_name, status, definition_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, newID, workspaceID, item.Key, item.Version,
		item.DisplayName, item.Status, definition)
	if err != nil {
		return constraintError("bootstrap data model", err)
	}
	return nil
}

func (s *Store) RegistryCounts(ctx context.Context, workspaceID domain.ID) (RegistryCounts, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return RegistryCounts{}, err
	}
	var counts RegistryCounts
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM connector_types WHERE workspace_id = ?`, workspaceID).Scan(&counts.ConnectorTypes); err != nil {
		return RegistryCounts{}, fmt.Errorf("count connector types: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM connector_behaviors WHERE workspace_id = ?`, workspaceID).Scan(&counts.Behaviors); err != nil {
		return RegistryCounts{}, fmt.Errorf("count connector behaviors: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM data_models WHERE workspace_id = ?`, workspaceID).Scan(&counts.DataModels); err != nil {
		return RegistryCounts{}, fmt.Errorf("count data models: %w", err)
	}
	return counts, nil
}

func (s *Store) ListConnectorBehaviors(ctx context.Context, workspaceID domain.ID) ([]domain.ConnectorBehavior, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, definition_json FROM connector_behaviors
		WHERE workspace_id = ? ORDER BY key, version`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list connector behaviors: %w", err)
	}
	defer rows.Close()
	var out []domain.ConnectorBehavior
	for rows.Next() {
		var id, definition string
		if err := rows.Scan(&id, &definition); err != nil {
			return nil, fmt.Errorf("scan connector behavior: %w", err)
		}
		var item domain.ConnectorBehavior
		if err := json.Unmarshal([]byte(definition), &item); err != nil {
			return nil, fmt.Errorf("decode connector behavior: %w", err)
		}
		item.ID, item.WorkspaceID = domain.ID(id), workspaceID
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list connector behaviors: %w", err)
	}
	return out, nil
}

func (s *Store) ListDataModels(ctx context.Context, workspaceID domain.ID) ([]domain.DataModelDefinition, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, definition_json FROM data_models
		WHERE workspace_id = ? ORDER BY key, version`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list data models: %w", err)
	}
	defer rows.Close()
	var out []domain.DataModelDefinition
	for rows.Next() {
		var id, definition string
		if err := rows.Scan(&id, &definition); err != nil {
			return nil, fmt.Errorf("scan data model: %w", err)
		}
		var item domain.DataModelDefinition
		if err := json.Unmarshal([]byte(definition), &item); err != nil {
			return nil, fmt.Errorf("decode data model: %w", err)
		}
		item.ID, item.WorkspaceID = domain.ID(id), workspaceID
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list data models: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		return registry.StableKey(out[i].Key, out[i].Version) < registry.StableKey(out[j].Key, out[j].Version)
	})
	return out, nil
}
