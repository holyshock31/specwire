package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"specwire/bridge/internal/domain"
)

func (s *Store) FindActiveConnectionBySource(ctx context.Context, workspaceID, instanceID domain.ID, externalID string) (domain.Connection, error) {
	return s.findConnection(ctx, `source_gitlab_instance_id = ? AND source_project_external_id = ?`, workspaceID, instanceID, externalID)
}

func (s *Store) FindActiveConnectionByTarget(ctx context.Context, workspaceID, instanceID domain.ID, externalID string) (domain.Connection, error) {
	return s.findConnection(ctx, `target_multica_instance_id = ? AND target_project_external_id = ?`, workspaceID, instanceID, externalID)
}

func (s *Store) UpdateConnectionStatus(ctx context.Context, workspaceID, connectionID domain.ID, status domain.ConnectionStatus, readyAt *time.Time) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE connections SET status = ?, ready_at = ? WHERE workspace_id = ? AND id = ?`,
		status, formatOptionalTime(readyAt), workspaceID, connectionID)
	if err != nil {
		return fmt.Errorf("update connection status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: connection %s", domain.ErrNotFound, connectionID)
	}
	return nil
}

func (s *Store) findConnection(ctx context.Context, predicate string, workspaceID, instanceID domain.ID, externalID string) (domain.Connection, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM connections WHERE workspace_id = ? AND `+predicate+` AND status <> 'disabled' LIMIT 1`, workspaceID, instanceID, externalID).Scan(&id)
	if err == sql.ErrNoRows {
		return domain.Connection{}, fmt.Errorf("%w: active connection", domain.ErrNotFound)
	}
	if err != nil {
		return domain.Connection{}, fmt.Errorf("find active connection: %w", err)
	}
	return s.GetConnection(ctx, workspaceID, domain.ID(id))
}

func (s *Store) EnsureManagedResource(ctx context.Context, resource domain.ManagedResource) (domain.ManagedResource, error) {
	if resource.ID.Empty() {
		resource.ID = domain.NewID()
	}
	if err := requireWorkspaceID(resource.WorkspaceID); err != nil {
		return domain.ManagedResource{}, err
	}
	if resource.ConnectionID.Empty() || resource.InstanceID.Empty() || strings.TrimSpace(resource.ExternalID) == "" || resource.Kind == "" || resource.Provider == "" {
		return domain.ManagedResource{}, fmt.Errorf("%w: incomplete managed resource", domain.ErrInvalid)
	}
	var connectionWorkspace string
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM connections WHERE id = ?`, resource.ConnectionID).Scan(&connectionWorkspace); err == sql.ErrNoRows {
		return domain.ManagedResource{}, fmt.Errorf("%w: connection %s", domain.ErrNotFound, resource.ConnectionID)
	} else if err != nil {
		return domain.ManagedResource{}, err
	} else if domain.ID(connectionWorkspace) != resource.WorkspaceID {
		return domain.ManagedResource{}, fmt.Errorf("%w: resource connection belongs to another workspace", domain.ErrForbidden)
	}
	if resource.Ownership == "" {
		resource.Ownership = domain.OwnershipManaged
	}
	if resource.Status == "" {
		resource.Status = "ready"
	}
	snapshot, err := marshalJSON(resource.Snapshot, "{}")
	if err != nil {
		return domain.ManagedResource{}, err
	}
	var existing domain.ManagedResource
	var existingSnapshot string
	err = s.db.QueryRowContext(ctx, `SELECT id, kind, provider, instance_id, external_id, ownership, management_mark, status, snapshot_json
		FROM managed_resources WHERE workspace_id = ? AND connection_id = ? AND kind = ? AND instance_id = ? AND external_id = ?`,
		resource.WorkspaceID, resource.ConnectionID, resource.Kind, resource.InstanceID, resource.ExternalID).Scan(
		&existing.ID, &existing.Kind, &existing.Provider, &existing.InstanceID, &existing.ExternalID,
		&existing.Ownership, &existing.ManagementMark, &existing.Status, &existingSnapshot)
	if err == nil {
		existing.WorkspaceID, existing.ConnectionID = resource.WorkspaceID, resource.ConnectionID
		if err := json.Unmarshal([]byte(existingSnapshot), &existing.Snapshot); err != nil {
			return domain.ManagedResource{}, fmt.Errorf("decode managed resource snapshot: %w", err)
		}
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return domain.ManagedResource{}, fmt.Errorf("inspect managed resource: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO managed_resources
		(id, workspace_id, connection_id, kind, provider, instance_id, external_id, ownership, management_mark, status, snapshot_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, resource.ID, resource.WorkspaceID, resource.ConnectionID, resource.Kind,
		resource.Provider, resource.InstanceID, resource.ExternalID, resource.Ownership, resource.ManagementMark, resource.Status, snapshot)
	if err != nil {
		return domain.ManagedResource{}, constraintError("ensure managed resource", err)
	}
	return resource, nil
}

func (s *Store) ListManagedResources(ctx context.Context, workspaceID, connectionID domain.ID) ([]domain.ManagedResource, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, provider, instance_id, external_id, ownership, management_mark, status, snapshot_json
		FROM managed_resources WHERE workspace_id = ? AND connection_id = ? ORDER BY kind, external_id`, workspaceID, connectionID)
	if err != nil {
		return nil, fmt.Errorf("list managed resources: %w", err)
	}
	defer rows.Close()
	var out []domain.ManagedResource
	for rows.Next() {
		var item domain.ManagedResource
		var snapshot string
		if err := rows.Scan(&item.ID, &item.Kind, &item.Provider, &item.InstanceID, &item.ExternalID, &item.Ownership, &item.ManagementMark, &item.Status, &snapshot); err != nil {
			return nil, err
		}
		item.WorkspaceID, item.ConnectionID = workspaceID, connectionID
		if err := json.Unmarshal([]byte(snapshot), &item.Snapshot); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CreateOnboardingOperation(ctx context.Context, operation domain.OnboardingOperation) error {
	if operation.ID.Empty() {
		return fmt.Errorf("%w: onboarding operation id is required", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(operation.WorkspaceID); err != nil {
		return err
	}
	if operation.Status == "" {
		operation.Status = domain.OnboardingPending
	}
	if operation.CreatedAt.IsZero() {
		operation.CreatedAt = s.now()
	}
	if operation.UpdatedAt.IsZero() {
		operation.UpdatedAt = operation.CreatedAt
	}
	request, err := marshalJSON(operation.Request, "{}")
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO onboarding_operations
		(id, workspace_id, connection_id, status, request_json, error_category, error_message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, operation.ID, operation.WorkspaceID, nullID(operation.ConnectionID), operation.Status,
		request, operation.ErrorCategory, operation.ErrorMessage, operation.CreatedAt.Format(time.RFC3339Nano), operation.UpdatedAt.Format(time.RFC3339Nano))
	return constraintError("create onboarding operation", err)
}

func (s *Store) GetOnboardingOperation(ctx context.Context, workspaceID, operationID domain.ID) (domain.OnboardingOperation, error) {
	var operation domain.OnboardingOperation
	var connectionID sql.NullString
	var request, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, connection_id, status, request_json, error_category, error_message, created_at, updated_at
		FROM onboarding_operations WHERE workspace_id = ? AND id = ?`, workspaceID, operationID).Scan(&operation.ID, &connectionID, &operation.Status, &request, &operation.ErrorCategory, &operation.ErrorMessage, &created, &updated)
	if err == sql.ErrNoRows {
		return domain.OnboardingOperation{}, fmt.Errorf("%w: onboarding operation %s", domain.ErrNotFound, operationID)
	}
	if err != nil {
		return domain.OnboardingOperation{}, fmt.Errorf("get onboarding operation: %w", err)
	}
	operation.WorkspaceID = workspaceID
	if connectionID.Valid {
		operation.ConnectionID = domain.ID(connectionID.String)
	}
	if err := json.Unmarshal([]byte(request), &operation.Request); err != nil {
		return domain.OnboardingOperation{}, err
	}
	operation.CreatedAt, err = decodeTime(created)
	if err != nil {
		return domain.OnboardingOperation{}, err
	}
	operation.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return domain.OnboardingOperation{}, err
	}
	return operation, nil
}

func (s *Store) UpdateOnboardingOperation(ctx context.Context, workspaceID, operationID, connectionID domain.ID, status domain.OnboardingStatus, category, message string) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	if len(message) > 500 {
		message = message[:500]
	}
	result, err := s.db.ExecContext(ctx, `UPDATE onboarding_operations SET connection_id = ?, status = ?, error_category = ?, error_message = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?`, nullID(connectionID), status, category, message, nowText(), workspaceID, operationID)
	if err != nil {
		return fmt.Errorf("update onboarding operation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: onboarding operation %s", domain.ErrNotFound, operationID)
	}
	return nil
}

func (s *Store) UpsertOnboardingCheckpoint(ctx context.Context, checkpoint domain.OnboardingCheckpoint) error {
	if checkpoint.ID.Empty() {
		checkpoint.ID = domain.NewID()
	}
	if err := requireWorkspaceID(checkpoint.WorkspaceID); err != nil {
		return err
	}
	if checkpoint.OperationID.Empty() || strings.TrimSpace(checkpoint.Step) == "" || strings.TrimSpace(checkpoint.Status) == "" {
		return fmt.Errorf("%w: incomplete onboarding checkpoint", domain.ErrInvalid)
	}
	if checkpoint.UpdatedAt.IsZero() {
		checkpoint.UpdatedAt = s.now()
	}
	result, err := marshalJSON(checkpoint.Result, "{}")
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO onboarding_checkpoints
		(id, workspace_id, operation_id, step, status, provider_id, result_json, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, operation_id, step) DO UPDATE SET
		status = excluded.status, provider_id = excluded.provider_id, result_json = excluded.result_json, updated_at = excluded.updated_at`,
		checkpoint.ID, checkpoint.WorkspaceID, checkpoint.OperationID, checkpoint.Step, checkpoint.Status, checkpoint.ProviderID, result, checkpoint.UpdatedAt.Format(time.RFC3339Nano))
	return constraintError("upsert onboarding checkpoint", err)
}

func (s *Store) GetOnboardingCheckpoint(ctx context.Context, workspaceID, operationID domain.ID, step string) (domain.OnboardingCheckpoint, error) {
	var checkpoint domain.OnboardingCheckpoint
	var result, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, step, status, provider_id, result_json, updated_at FROM onboarding_checkpoints
		WHERE workspace_id = ? AND operation_id = ? AND step = ?`, workspaceID, operationID, step).Scan(&checkpoint.ID, &checkpoint.Step, &checkpoint.Status, &checkpoint.ProviderID, &result, &updated)
	if err == sql.ErrNoRows {
		return domain.OnboardingCheckpoint{}, fmt.Errorf("%w: onboarding checkpoint %s", domain.ErrNotFound, step)
	}
	if err != nil {
		return domain.OnboardingCheckpoint{}, err
	}
	checkpoint.WorkspaceID, checkpoint.OperationID = workspaceID, operationID
	if err := json.Unmarshal([]byte(result), &checkpoint.Result); err != nil {
		return domain.OnboardingCheckpoint{}, err
	}
	checkpoint.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return domain.OnboardingCheckpoint{}, err
	}
	return checkpoint, nil
}
