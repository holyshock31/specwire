package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"specwire/bridge/internal/domain"
)

func (s *Store) UpsertMulticaWorkspace(ctx context.Context, workspace domain.MulticaWorkspaceRef) (domain.MulticaWorkspaceRef, error) {
	if err := validateProviderRef(workspace.WorkspaceID, workspace.InstanceID, workspace.ExternalID, workspace.Name, "Multica workspace"); err != nil {
		return domain.MulticaWorkspaceRef{}, err
	}
	if workspace.ID.Empty() {
		workspace.ID = domain.NewID()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO multica_workspaces
		(id, workspace_id, multica_instance_id, external_id, name)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, multica_instance_id, external_id) DO UPDATE SET name = excluded.name`,
		workspace.ID, workspace.WorkspaceID, workspace.InstanceID, workspace.ExternalID, workspace.Name)
	if err != nil {
		return domain.MulticaWorkspaceRef{}, constraintError("upsert Multica workspace", err)
	}
	return s.GetMulticaWorkspace(ctx, workspace.WorkspaceID, workspace.InstanceID, workspace.ExternalID)
}

func (s *Store) GetMulticaWorkspace(ctx context.Context, workspaceID, instanceID domain.ID, externalID string) (domain.MulticaWorkspaceRef, error) {
	var item domain.MulticaWorkspaceRef
	err := s.db.QueryRowContext(ctx, `SELECT id, workspace_id, multica_instance_id, external_id, name
		FROM multica_workspaces WHERE workspace_id = ? AND multica_instance_id = ? AND external_id = ?`, workspaceID, instanceID, externalID).
		Scan(&item.ID, &item.WorkspaceID, &item.InstanceID, &item.ExternalID, &item.Name)
	if err == sql.ErrNoRows {
		return domain.MulticaWorkspaceRef{}, fmt.Errorf("%w: Multica workspace %s", domain.ErrNotFound, externalID)
	}
	if err != nil {
		return domain.MulticaWorkspaceRef{}, fmt.Errorf("get Multica workspace: %w", err)
	}
	return item, nil
}

func (s *Store) ListMulticaWorkspaces(ctx context.Context, workspaceID, instanceID domain.ID) ([]domain.MulticaWorkspaceRef, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, workspace_id, multica_instance_id, external_id, name
		FROM multica_workspaces WHERE workspace_id = ? AND multica_instance_id = ? ORDER BY name, external_id`, workspaceID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list Multica workspaces: %w", err)
	}
	defer rows.Close()
	var result []domain.MulticaWorkspaceRef
	for rows.Next() {
		var item domain.MulticaWorkspaceRef
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.InstanceID, &item.ExternalID, &item.Name); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpsertMulticaProject(ctx context.Context, project domain.MulticaProjectRef) (domain.MulticaProjectRef, error) {
	if err := validateProviderRef(project.WorkspaceID, project.InstanceID, project.ExternalID, project.Title, "Multica project"); err != nil {
		return domain.MulticaProjectRef{}, err
	}
	if project.MulticaWorkspaceID.Empty() {
		return domain.MulticaProjectRef{}, fmt.Errorf("%w: Multica project workspace is required", domain.ErrInvalid)
	}
	if project.ID.Empty() {
		project.ID = domain.NewID()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO multica_projects
		(id, workspace_id, multica_instance_id, multica_workspace_id, external_id, title)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, multica_instance_id, external_id) DO UPDATE SET
		multica_workspace_id = excluded.multica_workspace_id, title = excluded.title`,
		project.ID, project.WorkspaceID, project.InstanceID, project.MulticaWorkspaceID, project.ExternalID, project.Title)
	if err != nil {
		return domain.MulticaProjectRef{}, constraintError("upsert Multica project", err)
	}
	return s.GetMulticaProject(ctx, project.WorkspaceID, project.InstanceID, project.ExternalID)
}

func (s *Store) GetMulticaProject(ctx context.Context, workspaceID, instanceID domain.ID, externalID string) (domain.MulticaProjectRef, error) {
	var item domain.MulticaProjectRef
	err := s.db.QueryRowContext(ctx, `SELECT id, workspace_id, multica_instance_id, multica_workspace_id, external_id, title
		FROM multica_projects WHERE workspace_id = ? AND multica_instance_id = ? AND external_id = ?`, workspaceID, instanceID, externalID).
		Scan(&item.ID, &item.WorkspaceID, &item.InstanceID, &item.MulticaWorkspaceID, &item.ExternalID, &item.Title)
	if err == sql.ErrNoRows {
		return domain.MulticaProjectRef{}, fmt.Errorf("%w: Multica project %s", domain.ErrNotFound, externalID)
	}
	if err != nil {
		return domain.MulticaProjectRef{}, fmt.Errorf("get Multica project: %w", err)
	}
	return item, nil
}

func (s *Store) ListMulticaProjects(ctx context.Context, workspaceID, instanceID, multicaWorkspaceID domain.ID) ([]domain.MulticaProjectRef, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, workspace_id, multica_instance_id, multica_workspace_id, external_id, title
		FROM multica_projects WHERE workspace_id = ? AND multica_instance_id = ? AND multica_workspace_id = ? ORDER BY title, external_id`, workspaceID, instanceID, multicaWorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("list Multica projects: %w", err)
	}
	defer rows.Close()
	var result []domain.MulticaProjectRef
	for rows.Next() {
		var item domain.MulticaProjectRef
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.InstanceID, &item.MulticaWorkspaceID, &item.ExternalID, &item.Title); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func validateProviderRef(workspaceID, instanceID domain.ID, externalID, name, kind string) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	if instanceID.Empty() || strings.TrimSpace(externalID) == "" || strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: %s instance, external ID and name are required", domain.ErrInvalid, kind)
	}
	return nil
}
