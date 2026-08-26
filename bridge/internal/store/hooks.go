package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"specwire/bridge/internal/domain"
)

func (s *Store) UpsertHook(ctx context.Context, hook domain.Hook) (domain.Hook, error) {
	if hook.ID.Empty() {
		hook.ID = domain.NewID()
	}
	if err := requireWorkspaceID(hook.WorkspaceID); err != nil {
		return domain.Hook{}, err
	}
	if hook.ConnectionID.Empty() || hook.InstanceID.Empty() || strings.TrimSpace(hook.SourceProjectExternalID) == "" || strings.TrimSpace(hook.ExternalID) == "" {
		return domain.Hook{}, fmt.Errorf("%w: incomplete Hook identity", domain.ErrInvalid)
	}
	if hook.Provider == "" {
		hook.Provider = domain.ProviderGitLab
	}
	if hook.Status == "" {
		hook.Status = domain.HookActive
	}
	var connectionWorkspace string
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM connections WHERE id = ?`, hook.ConnectionID).Scan(&connectionWorkspace); err == sql.ErrNoRows {
		return domain.Hook{}, fmt.Errorf("%w: connection %s", domain.ErrNotFound, hook.ConnectionID)
	} else if err != nil {
		return domain.Hook{}, err
	} else if domain.ID(connectionWorkspace) != hook.WorkspaceID {
		return domain.Hook{}, fmt.Errorf("%w: Hook connection belongs to another workspace", domain.ErrForbidden)
	}
	if hook.SigningRef != nil && hook.SigningRef.WorkspaceID != hook.WorkspaceID {
		return domain.Hook{}, fmt.Errorf("%w: Hook signing secret belongs to another workspace", domain.ErrForbidden)
	}
	var existingID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM hooks WHERE workspace_id = ? AND instance_id = ? AND source_project_external_id = ?`, hook.WorkspaceID, hook.InstanceID, hook.SourceProjectExternalID).Scan(&existingID)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE hooks SET connection_id = ?, provider = ?, external_id = ?, signing_ref_id = ?, status = ? WHERE id = ? AND workspace_id = ?`, hook.ConnectionID, hook.Provider, hook.ExternalID, secretRefID(hook.SigningRef), hook.Status, existingID, hook.WorkspaceID)
		if err != nil {
			return domain.Hook{}, constraintError("update Hook", err)
		}
		return s.GetHook(ctx, hook.WorkspaceID, domain.ID(existingID))
	}
	if err != sql.ErrNoRows {
		return domain.Hook{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO hooks
		(id, workspace_id, connection_id, provider, instance_id, source_project_external_id, external_id, signing_ref_id, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, hook.ID, hook.WorkspaceID, hook.ConnectionID, hook.Provider,
		hook.InstanceID, hook.SourceProjectExternalID, hook.ExternalID, secretRefID(hook.SigningRef), hook.Status)
	if err != nil {
		return domain.Hook{}, constraintError("create Hook", err)
	}
	return s.GetHook(ctx, hook.WorkspaceID, hook.ID)
}

func (s *Store) GetHook(ctx context.Context, workspaceID, hookID domain.ID) (domain.Hook, error) {
	var hook domain.Hook
	var signingRef sql.NullString
	var signingAlias sql.NullString
	var signingKind sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT h.id, h.connection_id, h.provider, h.instance_id, h.source_project_external_id, h.external_id, h.signing_ref_id, h.status, s.alias, s.kind
		FROM hooks h LEFT JOIN secrets s ON s.ref_id = h.signing_ref_id AND s.workspace_id = h.workspace_id
		WHERE h.workspace_id = ? AND h.id = ?`, workspaceID, hookID).Scan(
		&hook.ID, &hook.ConnectionID, &hook.Provider, &hook.InstanceID, &hook.SourceProjectExternalID, &hook.ExternalID, &signingRef, &hook.Status, &signingAlias, &signingKind)
	if err == sql.ErrNoRows {
		return domain.Hook{}, fmt.Errorf("%w: Hook %s", domain.ErrNotFound, hookID)
	}
	if err != nil {
		return domain.Hook{}, fmt.Errorf("get Hook: %w", err)
	}
	hook.WorkspaceID = workspaceID
	if signingRef.Valid && signingRef.String != "" {
		hook.SigningRef = &domain.SecretRef{ID: domain.ID(signingRef.String), WorkspaceID: workspaceID, Alias: signingAlias.String, Kind: domain.SecretKind(signingKind.String)}
	}
	return hook, nil
}

func (s *Store) GetHookByProject(ctx context.Context, workspaceID, instanceID domain.ID, sourceProjectExternalID string) (domain.Hook, error) {
	var hookID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM hooks WHERE workspace_id = ? AND instance_id = ? AND source_project_external_id = ?`, workspaceID, instanceID, sourceProjectExternalID).Scan(&hookID)
	if err == sql.ErrNoRows {
		return domain.Hook{}, fmt.Errorf("%w: Hook for project %s", domain.ErrNotFound, sourceProjectExternalID)
	}
	if err != nil {
		return domain.Hook{}, err
	}
	return s.GetHook(ctx, workspaceID, domain.ID(hookID))
}

func (s *Store) UpsertHookRoute(ctx context.Context, route domain.HookRoute) (domain.HookRoute, error) {
	if route.ID.Empty() {
		route.ID = domain.NewID()
	}
	if err := requireWorkspaceID(route.WorkspaceID); err != nil {
		return domain.HookRoute{}, err
	}
	if route.ConnectionID.Empty() || route.HookRef.Empty() || route.FlowID.Empty() || route.FlowVersion <= 0 || strings.TrimSpace(route.BehaviorKey) == "" || strings.TrimSpace(route.BehaviorVersion) == "" || strings.TrimSpace(route.SourceProject.ExternalID) == "" {
		return domain.HookRoute{}, fmt.Errorf("%w: incomplete Hook route", domain.ErrInvalid)
	}
	if route.Status == "" {
		route.Status = domain.HookActive
	}
	filter, err := marshalJSON(route.EventFilter, "{}")
	if err != nil {
		return domain.HookRoute{}, err
	}
	var existingID string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM hook_routes WHERE workspace_id = ? AND connection_id = ? AND behavior_key = ? AND behavior_version = ? AND flow_id = ? AND flow_version = ?`, route.WorkspaceID, route.ConnectionID, route.BehaviorKey, route.BehaviorVersion, route.FlowID, route.FlowVersion).Scan(&existingID)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE hook_routes SET source_instance_id = ?, source_project_external_id = ?, event_filter_json = ?, hook_id = ?, status = ? WHERE id = ? AND workspace_id = ?`, route.SourceProject.InstanceID, route.SourceProject.ExternalID, filter, route.HookRef, route.Status, existingID, route.WorkspaceID)
		if err != nil {
			return domain.HookRoute{}, constraintError("update Hook route", err)
		}
		route.ID = domain.ID(existingID)
		return route, nil
	}
	if err != sql.ErrNoRows {
		return domain.HookRoute{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO hook_routes
		(id, workspace_id, connection_id, source_instance_id, source_project_external_id, behavior_key, behavior_version, flow_id, flow_version, event_filter_json, hook_id, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, route.ID, route.WorkspaceID, route.ConnectionID, route.SourceProject.InstanceID, route.SourceProject.ExternalID, route.BehaviorKey, route.BehaviorVersion, route.FlowID, route.FlowVersion, filter, route.HookRef, route.Status)
	if err != nil {
		return domain.HookRoute{}, constraintError("create Hook route", err)
	}
	return route, nil
}

func (s *Store) DisableHookRoutesForFlow(ctx context.Context, workspaceID, flowID domain.ID, flowVersion int) error {
	result, err := s.db.ExecContext(ctx, `UPDATE hook_routes SET status = 'disabled' WHERE workspace_id = ? AND flow_id = ? AND (? = 0 OR flow_version = ?)`, workspaceID, flowID, flowVersion, flowVersion)
	if err != nil {
		return fmt.Errorf("disable Hook routes: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return fmt.Errorf("%w: Hook route for flow %s", domain.ErrNotFound, flowID)
	}
	return nil
}

func (s *Store) ListHookRoutes(ctx context.Context, workspaceID, connectionID domain.ID) ([]domain.HookRoute, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, source_instance_id, source_project_external_id, behavior_key, behavior_version, flow_id, flow_version, event_filter_json, hook_id, status FROM hook_routes WHERE workspace_id = ? AND connection_id = ? ORDER BY flow_id, flow_version`, workspaceID, connectionID)
	if err != nil {
		return nil, fmt.Errorf("list Hook routes: %w", err)
	}
	defer rows.Close()
	var result []domain.HookRoute
	for rows.Next() {
		var route domain.HookRoute
		var sourceInstance, sourceProject, filter string
		if err := rows.Scan(&route.ID, &sourceInstance, &sourceProject, &route.BehaviorKey, &route.BehaviorVersion, &route.FlowID, &route.FlowVersion, &filter, &route.HookRef, &route.Status); err != nil {
			return nil, err
		}
		route.WorkspaceID, route.ConnectionID = workspaceID, connectionID
		route.SourceProject = domain.ProviderProjectRef{InstanceID: domain.ID(sourceInstance), ExternalID: sourceProject}
		if err := json.Unmarshal([]byte(filter), &route.EventFilter); err != nil {
			return nil, err
		}
		result = append(result, route)
	}
	return result, rows.Err()
}
