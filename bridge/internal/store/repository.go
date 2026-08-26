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

func constraintError(operation string, err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "unique constraint"),
		strings.Contains(message, "primary key constraint"),
		strings.Contains(message, "foreign key constraint"):
		return fmt.Errorf("%w: %s: %v", domain.ErrConflict, operation, err)
	default:
		return fmt.Errorf("%s: %w", operation, err)
	}
}

func (s *Store) CreateWorkspace(ctx context.Context, workspace domain.Workspace) error {
	if workspace.Status == "" {
		workspace.Status = domain.WorkspaceActive
	}
	if workspace.CreatedAt.IsZero() {
		workspace.CreatedAt = s.now()
	}
	if workspace.UpdatedAt.IsZero() {
		workspace.UpdatedAt = workspace.CreatedAt
	}
	if err := workspace.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspaces
		(id, slug, name, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		workspace.ID, workspace.Slug, workspace.Name, workspace.Status,
		workspace.CreatedAt.Format(time.RFC3339Nano), workspace.UpdatedAt.Format(time.RFC3339Nano))
	return constraintError("create workspace", err)
}

func (s *Store) GetWorkspace(ctx context.Context, workspaceID domain.ID) (domain.Workspace, error) {
	var w domain.Workspace
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, slug, name, status, created_at, updated_at
		FROM workspaces WHERE id = ?`, workspaceID).
		Scan(&w.ID, &w.Slug, &w.Name, &w.Status, &created, &updated)
	if err == sql.ErrNoRows {
		return domain.Workspace{}, fmt.Errorf("%w: workspace %s", domain.ErrNotFound, workspaceID)
	}
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("get workspace: %w", err)
	}
	w.CreatedAt, err = decodeTime(created)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("decode workspace created_at: %w", err)
	}
	w.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("decode workspace updated_at: %w", err)
	}
	return w, nil
}

func (s *Store) CreateAccount(ctx context.Context, account domain.Account) error {
	if account.Status == "" {
		account.Status = domain.AccountActive
	}
	if account.CreatedAt.IsZero() {
		account.CreatedAt = s.now()
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = account.CreatedAt
	}
	if err := requireAccount(account); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO accounts
		(id, email, display_name, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		account.ID, account.Email, account.DisplayName, account.Status,
		account.CreatedAt.Format(time.RFC3339Nano), account.UpdatedAt.Format(time.RFC3339Nano))
	return constraintError("create account", err)
}

func requireAccount(account domain.Account) error {
	if account.ID.Empty() {
		return fmt.Errorf("%w: account id is required", domain.ErrInvalid)
	}
	if strings.TrimSpace(account.Email) == "" {
		return fmt.Errorf("%w: account email is required", domain.ErrInvalid)
	}
	if strings.TrimSpace(account.DisplayName) == "" {
		return fmt.Errorf("%w: account display_name is required", domain.ErrInvalid)
	}
	if account.Status == "" {
		return fmt.Errorf("%w: account status is required", domain.ErrInvalid)
	}
	return nil
}

func (s *Store) CreateConnection(ctx context.Context, connection domain.Connection) error {
	if connection.Status == "" {
		connection.Status = domain.ConnectionConfigured
	}
	if connection.ConfiguredAt.IsZero() {
		connection.ConfiguredAt = s.now()
	}
	if err := connection.Validate(); err != nil {
		return err
	}
	for _, endpoint := range []struct {
		table string
		id    domain.ID
		name  string
	}{
		{table: "gitlab_instances", id: connection.SourceGitLabProject.InstanceID, name: "GitLab instance"},
		{table: "multica_instances", id: connection.TargetMulticaProject.InstanceID, name: "Multica instance"},
	} {
		var endpointWorkspace string
		err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM `+endpoint.table+` WHERE id = ?`, endpoint.id).Scan(&endpointWorkspace)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: %s %s", domain.ErrNotFound, endpoint.name, endpoint.id)
		}
		if err != nil {
			return fmt.Errorf("check %s workspace: %w", endpoint.name, err)
		}
		if domain.ID(endpointWorkspace) != connection.WorkspaceID {
			return fmt.Errorf("%w: %s belongs to another workspace", domain.ErrForbidden, endpoint.name)
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO connections (
		id, workspace_id, name,
		source_gitlab_instance_id, source_project_external_id, source_project_path,
		source_project_web_url, source_project_ssh_url, source_project_https_url,
		target_multica_instance_id, target_project_external_id, target_project_name,
		target_project_web_url, status, configured_at, ready_at, disabled_at, created_by
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		connection.ID, connection.WorkspaceID, connection.Name,
		connection.SourceGitLabProject.InstanceID, connection.SourceGitLabProject.ExternalID,
		connection.SourceGitLabProject.FullPath, connection.SourceGitLabProject.WebURL,
		connection.SourceGitLabProject.SSHURL, connection.SourceGitLabProject.HTTPSURL,
		connection.TargetMulticaProject.InstanceID, connection.TargetMulticaProject.ExternalID,
		connection.TargetMulticaProject.Name, connection.TargetMulticaProject.WebURL,
		connection.Status, connection.ConfiguredAt.Format(time.RFC3339Nano),
		formatOptionalTime(connection.ReadyAt), formatOptionalTime(connection.DisabledAt), nullID(connection.CreatedBy))
	return constraintError("create connection", err)
}

func nullID(id domain.ID) any {
	if id.Empty() {
		return nil
	}
	return id
}

func formatOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Format(time.RFC3339Nano)
}

func (s *Store) GetConnection(ctx context.Context, workspaceID, connectionID domain.ID) (domain.Connection, error) {
	var c domain.Connection
	var configured string
	var ready, disabled sql.NullString
	var createdBy sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT
		id, workspace_id, name,
		source_gitlab_instance_id, source_project_external_id, source_project_path,
		source_project_web_url, source_project_ssh_url, source_project_https_url,
		target_multica_instance_id, target_project_external_id, target_project_name,
		target_project_web_url, status, configured_at, ready_at, disabled_at, created_by
		FROM connections WHERE workspace_id = ? AND id = ?`, workspaceID, connectionID).Scan(
		&c.ID, &c.WorkspaceID, &c.Name,
		&c.SourceGitLabProject.InstanceID, &c.SourceGitLabProject.ExternalID,
		&c.SourceGitLabProject.FullPath, &c.SourceGitLabProject.WebURL,
		&c.SourceGitLabProject.SSHURL, &c.SourceGitLabProject.HTTPSURL,
		&c.TargetMulticaProject.InstanceID, &c.TargetMulticaProject.ExternalID,
		&c.TargetMulticaProject.Name, &c.TargetMulticaProject.WebURL,
		&c.Status, &configured, &ready, &disabled, &createdBy)
	if err == sql.ErrNoRows {
		// Deliberately hide whether the ID exists in another Workspace.
		return domain.Connection{}, fmt.Errorf("%w: connection %s", domain.ErrNotFound, connectionID)
	}
	if err != nil {
		return domain.Connection{}, fmt.Errorf("get connection: %w", err)
	}
	if createdBy.Valid {
		c.CreatedBy = domain.ID(createdBy.String)
	}
	c.ConfiguredAt, err = decodeTime(configured)
	if err != nil {
		return domain.Connection{}, fmt.Errorf("decode connection configured_at: %w", err)
	}
	c.ReadyAt, err = decodeOptionalTime(ready)
	if err != nil {
		return domain.Connection{}, fmt.Errorf("decode connection ready_at: %w", err)
	}
	c.DisabledAt, err = decodeOptionalTime(disabled)
	if err != nil {
		return domain.Connection{}, fmt.Errorf("decode connection disabled_at: %w", err)
	}
	return c, nil
}

func (s *Store) CreateFlow(ctx context.Context, flow domain.Flow) error {
	if flow.Status == "" {
		flow.Status = domain.FlowDraft
	}
	if flow.UpdatedAt.IsZero() {
		flow.UpdatedAt = s.now()
	}
	if flow.ID.Empty() {
		return fmt.Errorf("%w: flow id is required", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(flow.WorkspaceID); err != nil {
		return err
	}
	if flow.ConnectionID.Empty() {
		return fmt.Errorf("%w: flow connection_id is required", domain.ErrInvalid)
	}
	if strings.TrimSpace(flow.Name) == "" {
		return fmt.Errorf("%w: flow name is required", domain.ErrInvalid)
	}
	// SQLite's single-column foreign key cannot express the workspace/connection
	// pair, so enforce the pair explicitly before mutation.
	var connectionWorkspace string
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM connections WHERE id = ?`, flow.ConnectionID).Scan(&connectionWorkspace); err == sql.ErrNoRows {
		return fmt.Errorf("%w: connection %s", domain.ErrNotFound, flow.ConnectionID)
	} else if err != nil {
		return fmt.Errorf("check flow connection: %w", err)
	} else if domain.ID(connectionWorkspace) != flow.WorkspaceID {
		return fmt.Errorf("%w: flow connection belongs to another workspace", domain.ErrForbidden)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO flows
		(id, workspace_id, connection_id, name, description, status, active_version, created_by, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		flow.ID, flow.WorkspaceID, flow.ConnectionID, flow.Name, flow.Description,
		flow.Status, flow.ActiveVersion, nullID(flow.CreatedBy), flow.UpdatedAt.Format(time.RFC3339Nano))
	return constraintError("create flow", err)
}

func (s *Store) GetFlow(ctx context.Context, workspaceID, flowID domain.ID) (domain.Flow, error) {
	var flow domain.Flow
	var updated string
	var createdBy sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, workspace_id, connection_id, name, description, status, active_version, created_by, updated_at
		FROM flows WHERE workspace_id = ? AND id = ?`, workspaceID, flowID).Scan(
		&flow.ID, &flow.WorkspaceID, &flow.ConnectionID, &flow.Name, &flow.Description, &flow.Status,
		&flow.ActiveVersion, &createdBy, &updated)
	if err == sql.ErrNoRows {
		return domain.Flow{}, fmt.Errorf("%w: flow %s", domain.ErrNotFound, flowID)
	}
	if err != nil {
		return domain.Flow{}, fmt.Errorf("get flow: %w", err)
	}
	if createdBy.Valid {
		flow.CreatedBy = domain.ID(createdBy.String)
	}
	flow.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return domain.Flow{}, fmt.Errorf("decode flow updated_at: %w", err)
	}
	return flow, nil
}

func (s *Store) ListFlows(ctx context.Context, workspaceID, connectionID domain.ID) ([]domain.Flow, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, workspace_id, connection_id, name, description, status, active_version, created_by, updated_at
		FROM flows WHERE workspace_id = ? AND connection_id = ? ORDER BY name, id`, workspaceID, connectionID)
	if err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	defer rows.Close()
	var result []domain.Flow
	for rows.Next() {
		var flow domain.Flow
		var createdBy sql.NullString
		var updated string
		if err := rows.Scan(&flow.ID, &flow.WorkspaceID, &flow.ConnectionID, &flow.Name, &flow.Description, &flow.Status, &flow.ActiveVersion, &createdBy, &updated); err != nil {
			return nil, err
		}
		if createdBy.Valid {
			flow.CreatedBy = domain.ID(createdBy.String)
		}
		flow.UpdatedAt, err = decodeTime(updated)
		if err != nil {
			return nil, err
		}
		result = append(result, flow)
	}
	return result, rows.Err()
}

func (s *Store) SaveFlowVersion(ctx context.Context, version domain.FlowVersion) error {
	if version.ID.Empty() {
		return fmt.Errorf("%w: flow version id is required", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(version.WorkspaceID); err != nil {
		return err
	}
	if version.FlowID.Empty() {
		return fmt.Errorf("%w: flow version flow_id is required", domain.ErrInvalid)
	}
	if version.Version <= 0 {
		return fmt.Errorf("%w: flow version must be positive", domain.ErrInvalid)
	}
	if version.Status == "" {
		version.Status = domain.FlowDraft
	}
	if err := version.Graph.ValidateShape(); err != nil {
		return err
	}
	graphJSON, err := json.Marshal(version.Graph)
	if err != nil {
		return fmt.Errorf("marshal flow graph: %w", err)
	}
	planJSON, err := marshalJSON(version.CompiledPlan, "{}")
	if err != nil {
		return fmt.Errorf("marshal compiled plan: %w", err)
	}
	behaviorsJSON, err := marshalJSON(version.BehaviorRefs, "[]")
	if err != nil {
		return fmt.Errorf("marshal behavior refs: %w", err)
	}
	modelsJSON, err := marshalJSON(version.ModelRefs, "[]")
	if err != nil {
		return fmt.Errorf("marshal model refs: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save flow version: %w", err)
	}
	defer tx.Rollback()
	var flowWorkspace string
	if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM flows WHERE id = ?`, version.FlowID).Scan(&flowWorkspace); err == sql.ErrNoRows {
		return fmt.Errorf("%w: flow %s", domain.ErrNotFound, version.FlowID)
	} else if err != nil {
		return fmt.Errorf("check flow workspace: %w", err)
	} else if domain.ID(flowWorkspace) != version.WorkspaceID {
		return fmt.Errorf("%w: flow belongs to another workspace", domain.ErrForbidden)
	}

	var existing struct {
		ID, Status, Graph, Plan, Behaviors, Models string
	}
	err = tx.QueryRowContext(ctx, `SELECT id, status, graph_json, compiled_plan_json,
		behavior_refs_json, model_refs_json FROM flow_versions
		WHERE workspace_id = ? AND flow_id = ? AND version = ?`,
		version.WorkspaceID, version.FlowID, version.Version).Scan(
		&existing.ID, &existing.Status, &existing.Graph, &existing.Plan, &existing.Behaviors, &existing.Models)
	if err == nil {
		if existing.Graph != string(graphJSON) || existing.Plan != planJSON || existing.Behaviors != behaviorsJSON || existing.Models != modelsJSON || existing.Status != string(version.Status) {
			return fmt.Errorf("%w: flow %s version %d", domain.ErrImmutable, version.FlowID, version.Version)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit idempotent flow version: %w", err)
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("inspect flow version: %w", err)
	}

	if version.Status == domain.FlowPublished {
		// Only one published version is active.  Prior versions remain available
		// as immutable history and move to archived status when a new one wins.
		if _, err := tx.ExecContext(ctx, `UPDATE flow_versions SET status = 'archived'
			WHERE workspace_id = ? AND flow_id = ? AND status = 'published'`, version.WorkspaceID, version.FlowID); err != nil {
			return fmt.Errorf("archive previous flow version: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO flow_versions (
		id, workspace_id, flow_id, version, status, graph_json, compiled_plan_json,
		behavior_refs_json, model_refs_json, published_at, published_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		version.ID, version.WorkspaceID, version.FlowID, version.Version, version.Status,
		string(graphJSON), planJSON, behaviorsJSON, modelsJSON,
		formatOptionalTime(version.PublishedAt), nullID(version.PublishedBy))
	if err != nil {
		return constraintError("save flow version", err)
	}
	if version.Status == domain.FlowPublished {
		if _, err := tx.ExecContext(ctx, `UPDATE flows SET status = 'published', active_version = ?, updated_at = ?
			WHERE workspace_id = ? AND id = ?`, version.Version, nowText(), version.WorkspaceID, version.FlowID); err != nil {
			return fmt.Errorf("activate flow version: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit flow version: %w", err)
	}
	return nil
}

func (s *Store) GetFlowVersion(ctx context.Context, workspaceID, flowID domain.ID, versionNumber int) (domain.FlowVersion, error) {
	var v domain.FlowVersion
	var status, graphJSON, planJSON, behaviorsJSON, modelsJSON string
	var publishedAt sql.NullString
	var publishedBy sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, workspace_id, flow_id, version, status,
		graph_json, compiled_plan_json, behavior_refs_json, model_refs_json, published_at, published_by
		FROM flow_versions WHERE workspace_id = ? AND flow_id = ? AND version = ?`,
		workspaceID, flowID, versionNumber).Scan(
		&v.ID, &v.WorkspaceID, &v.FlowID, &v.Version, &status, &graphJSON,
		&planJSON, &behaviorsJSON, &modelsJSON, &publishedAt, &publishedBy)
	if err == sql.ErrNoRows {
		return domain.FlowVersion{}, fmt.Errorf("%w: flow %s version %d", domain.ErrNotFound, flowID, versionNumber)
	}
	if err != nil {
		return domain.FlowVersion{}, fmt.Errorf("get flow version: %w", err)
	}
	v.Status = domain.FlowStatus(status)
	if publishedBy.Valid {
		v.PublishedBy = domain.ID(publishedBy.String)
	}
	if err := json.Unmarshal([]byte(graphJSON), &v.Graph); err != nil {
		return domain.FlowVersion{}, fmt.Errorf("decode flow graph: %w", err)
	}
	if err := json.Unmarshal([]byte(planJSON), &v.CompiledPlan); err != nil {
		return domain.FlowVersion{}, fmt.Errorf("decode compiled plan: %w", err)
	}
	if err := json.Unmarshal([]byte(behaviorsJSON), &v.BehaviorRefs); err != nil {
		return domain.FlowVersion{}, fmt.Errorf("decode behavior refs: %w", err)
	}
	if err := json.Unmarshal([]byte(modelsJSON), &v.ModelRefs); err != nil {
		return domain.FlowVersion{}, fmt.Errorf("decode model refs: %w", err)
	}
	v.PublishedAt, err = decodeOptionalTime(publishedAt)
	if err != nil {
		return domain.FlowVersion{}, fmt.Errorf("decode flow published_at: %w", err)
	}
	return v, nil
}

func (s *Store) NextFlowVersion(ctx context.Context, workspaceID, flowID domain.ID) (int, error) {
	var next int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM flow_versions WHERE workspace_id = ? AND flow_id = ?`, workspaceID, flowID).Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("next flow version: %w", err)
	}
	return next, nil
}

func (s *Store) UpdateFlowStatus(ctx context.Context, workspaceID, flowID domain.ID, status domain.FlowStatus) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE flows SET status = ?, updated_at = ? WHERE workspace_id = ? AND id = ?`, status, nowText(), workspaceID, flowID)
	if err != nil {
		return fmt.Errorf("update flow status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: flow %s", domain.ErrNotFound, flowID)
	}
	return nil
}

func requireWorkspaceID(workspaceID domain.ID) error {
	if workspaceID.Empty() {
		return fmt.Errorf("%w: workspace_id is required", domain.ErrInvalid)
	}
	return nil
}
