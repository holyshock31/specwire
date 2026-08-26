package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"specwire/bridge/internal/domain"
)

func (s *Store) CreateGitLabInstance(ctx context.Context, instance domain.GitLabInstance) error {
	if instance.Status == "" {
		instance.Status = domain.EndpointActive
	}
	if instance.ID.Empty() {
		return fmt.Errorf("%w: GitLab instance id is required", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(instance.WorkspaceID); err != nil {
		return err
	}
	if strings.TrimSpace(instance.Name) == "" || strings.TrimSpace(instance.BaseURL) == "" {
		return fmt.Errorf("%w: GitLab instance name and base_url are required", domain.ErrInvalid)
	}
	capabilities, err := marshalJSON(instance.Capabilities, "[]")
	if err != nil {
		return fmt.Errorf("marshal GitLab capabilities: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO gitlab_instances
		(id, workspace_id, name, base_url, external_id, credential_ref_id, status, capabilities_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, instance.ID, instance.WorkspaceID, instance.Name,
		instance.BaseURL, nullString(instance.ExternalID), secretRefID(instance.CredentialRef),
		instance.Status, capabilities)
	return constraintError("create GitLab instance", err)
}

func (s *Store) CreateMulticaInstance(ctx context.Context, instance domain.MulticaInstance) error {
	if instance.Status == "" {
		instance.Status = domain.EndpointActive
	}
	if instance.ID.Empty() {
		return fmt.Errorf("%w: Multica instance id is required", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(instance.WorkspaceID); err != nil {
		return err
	}
	if strings.TrimSpace(instance.Name) == "" || strings.TrimSpace(instance.BaseURL) == "" {
		return fmt.Errorf("%w: Multica instance name and base_url are required", domain.ErrInvalid)
	}
	capabilities, err := marshalJSON(instance.Capabilities, "[]")
	if err != nil {
		return fmt.Errorf("marshal Multica capabilities: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO multica_instances
		(id, workspace_id, name, base_url, external_id, management_credential_ref_id, status, capabilities_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, instance.ID, instance.WorkspaceID, instance.Name,
		instance.BaseURL, nullString(instance.ExternalID), secretRefID(instance.ManagementCredentialRef),
		instance.Status, capabilities)
	return constraintError("create Multica instance", err)
}

func secretRefID(ref *domain.SecretRef) any {
	if ref == nil || ref.ID.Empty() {
		return nil
	}
	return ref.ID
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (s *Store) CreateGitLabGroupBinding(ctx context.Context, binding domain.GitLabGroupBinding) error {
	if binding.Status == "" {
		binding.Status = domain.EndpointActive
	}
	if binding.ID.Empty() {
		return fmt.Errorf("%w: GitLab group binding id is required", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(binding.WorkspaceID); err != nil {
		return err
	}
	if binding.GitLabInstanceID.Empty() || strings.TrimSpace(binding.ExternalGroupID) == "" || strings.TrimSpace(binding.FullPath) == "" {
		return fmt.Errorf("%w: GitLab group binding instance, external_group_id and full_path are required", domain.ErrInvalid)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO gitlab_group_bindings
		(id, workspace_id, gitlab_instance_id, external_group_id, full_path, credential_ref_id, inherit_subgroups, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, binding.ID, binding.WorkspaceID, binding.GitLabInstanceID,
		binding.ExternalGroupID, binding.FullPath, secretRefID(binding.CredentialRef), boolInt(binding.InheritSubgroups), binding.Status)
	return constraintError("create GitLab group binding", err)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) DisableConnection(ctx context.Context, workspaceID, connectionID domain.ID) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE connections SET status = ?, disabled_at = ?
		WHERE workspace_id = ? AND id = ?`, domain.ConnectionDisabled, nowText(), workspaceID, connectionID)
	if err != nil {
		return fmt.Errorf("disable connection: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("disable connection result: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: connection %s", domain.ErrNotFound, connectionID)
	}
	return nil
}

func (s *Store) GetGitLabInstance(ctx context.Context, workspaceID, instanceID domain.ID) (domain.GitLabInstance, error) {
	var instance domain.GitLabInstance
	var external, credential sql.NullString
	var capabilities string
	err := s.db.QueryRowContext(ctx, `SELECT id, workspace_id, name, base_url,
		external_id, credential_ref_id, status, capabilities_json FROM gitlab_instances
		WHERE workspace_id = ? AND id = ?`, workspaceID, instanceID).Scan(
		&instance.ID, &instance.WorkspaceID, &instance.Name, &instance.BaseURL,
		&external, &credential, &instance.Status, &capabilities)
	if err != nil {
		if isNoRows(err) {
			return domain.GitLabInstance{}, fmt.Errorf("%w: GitLab instance %s", domain.ErrNotFound, instanceID)
		}
		return domain.GitLabInstance{}, fmt.Errorf("get GitLab instance: %w", err)
	}
	if external.Valid {
		instance.ExternalID = external.String
	}
	if credential.Valid && credential.String != "" {
		instance.CredentialRef = &domain.SecretRef{ID: domain.ID(credential.String), WorkspaceID: workspaceID}
	}
	if err := json.Unmarshal([]byte(capabilities), &instance.Capabilities); err != nil {
		return domain.GitLabInstance{}, fmt.Errorf("decode GitLab capabilities: %w", err)
	}
	return instance, nil
}

func isNoRows(err error) bool { return err == sql.ErrNoRows }
