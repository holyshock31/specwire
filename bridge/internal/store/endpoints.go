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
	var instanceWorkspace string
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM gitlab_instances WHERE id = ?`, binding.GitLabInstanceID).Scan(&instanceWorkspace); err == sql.ErrNoRows {
		return fmt.Errorf("%w: GitLab instance %s", domain.ErrNotFound, binding.GitLabInstanceID)
	} else if err != nil {
		return fmt.Errorf("check GitLab group instance: %w", err)
	} else if domain.ID(instanceWorkspace) != binding.WorkspaceID {
		return fmt.Errorf("%w: GitLab group instance belongs to another workspace", domain.ErrForbidden)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO gitlab_group_bindings
		(id, workspace_id, gitlab_instance_id, external_group_id, full_path, credential_ref_id, credential_profile_id, inherit_subgroups, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, binding.ID, binding.WorkspaceID, binding.GitLabInstanceID,
		binding.ExternalGroupID, binding.FullPath, secretRefID(binding.CredentialRef), nullID(binding.CredentialProfileID), boolInt(binding.InheritSubgroups), binding.Status)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin disable connection: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE connections SET status = ?, disabled_at = ?
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
	if _, err := tx.ExecContext(ctx, `UPDATE hook_routes SET status = 'disabled'
		WHERE workspace_id = ? AND connection_id = ?`, workspaceID, connectionID); err != nil {
		return fmt.Errorf("disable connection routes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit disable connection: %w", err)
	}
	return nil
}

func (s *Store) GetGitLabInstance(ctx context.Context, workspaceID, instanceID domain.ID) (domain.GitLabInstance, error) {
	var instance domain.GitLabInstance
	var external, credential sql.NullString
	var credentialAlias, credentialKind sql.NullString
	var capabilities string
	err := s.db.QueryRowContext(ctx, `SELECT i.id, i.workspace_id, i.name, i.base_url,
		i.external_id, i.credential_ref_id, i.status, i.capabilities_json, s.alias, s.kind FROM gitlab_instances i
		LEFT JOIN secrets s ON s.ref_id = i.credential_ref_id AND s.workspace_id = i.workspace_id
		WHERE i.workspace_id = ? AND i.id = ?`, workspaceID, instanceID).Scan(
		&instance.ID, &instance.WorkspaceID, &instance.Name, &instance.BaseURL,
		&external, &credential, &instance.Status, &capabilities, &credentialAlias, &credentialKind)
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
		instance.CredentialRef = &domain.SecretRef{ID: domain.ID(credential.String), WorkspaceID: workspaceID, Alias: credentialAlias.String, Kind: domain.SecretKind(credentialKind.String)}
	}
	if err := json.Unmarshal([]byte(capabilities), &instance.Capabilities); err != nil {
		return domain.GitLabInstance{}, fmt.Errorf("decode GitLab capabilities: %w", err)
	}
	return instance, nil
}

func (s *Store) GetMulticaInstance(ctx context.Context, workspaceID, instanceID domain.ID) (domain.MulticaInstance, error) {
	var instance domain.MulticaInstance
	var external, credential sql.NullString
	var credentialAlias, credentialKind sql.NullString
	var capabilities string
	err := s.db.QueryRowContext(ctx, `SELECT i.id, i.workspace_id, i.name, i.base_url,
		i.external_id, i.management_credential_ref_id, i.status, i.capabilities_json, s.alias, s.kind FROM multica_instances i
		LEFT JOIN secrets s ON s.ref_id = i.management_credential_ref_id AND s.workspace_id = i.workspace_id
		WHERE i.workspace_id = ? AND i.id = ?`, workspaceID, instanceID).Scan(
		&instance.ID, &instance.WorkspaceID, &instance.Name, &instance.BaseURL,
		&external, &credential, &instance.Status, &capabilities, &credentialAlias, &credentialKind)
	if err == sql.ErrNoRows {
		return domain.MulticaInstance{}, fmt.Errorf("%w: Multica instance %s", domain.ErrNotFound, instanceID)
	}
	if err != nil {
		return domain.MulticaInstance{}, fmt.Errorf("get Multica instance: %w", err)
	}
	if external.Valid {
		instance.ExternalID = external.String
	}
	if credential.Valid && credential.String != "" {
		instance.ManagementCredentialRef = &domain.SecretRef{ID: domain.ID(credential.String), WorkspaceID: workspaceID, Alias: credentialAlias.String, Kind: domain.SecretKind(credentialKind.String)}
	}
	if err := json.Unmarshal([]byte(capabilities), &instance.Capabilities); err != nil {
		return domain.MulticaInstance{}, fmt.Errorf("decode Multica capabilities: %w", err)
	}
	return instance, nil
}

func isNoRows(err error) bool { return err == sql.ErrNoRows }

func (s *Store) ListGitLabInstances(ctx context.Context, workspaceID domain.ID) ([]domain.GitLabInstance, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, i.name, i.base_url, i.external_id, i.credential_ref_id, i.status, i.capabilities_json, s.alias, s.kind
		FROM gitlab_instances i LEFT JOIN secrets s ON s.ref_id = i.credential_ref_id AND s.workspace_id = i.workspace_id
		WHERE i.workspace_id = ? ORDER BY i.name, i.id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list GitLab instances: %w", err)
	}
	defer rows.Close()
	var out []domain.GitLabInstance
	for rows.Next() {
		var item domain.GitLabInstance
		var external, credential sql.NullString
		var capabilities string
		var credentialAlias, credentialKind sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &item.BaseURL, &external, &credential, &item.Status, &capabilities, &credentialAlias, &credentialKind); err != nil {
			return nil, err
		}
		item.WorkspaceID = workspaceID
		if external.Valid {
			item.ExternalID = external.String
		}
		if credential.Valid && credential.String != "" {
			item.CredentialRef = &domain.SecretRef{ID: domain.ID(credential.String), WorkspaceID: workspaceID, Alias: credentialAlias.String, Kind: domain.SecretKind(credentialKind.String)}
		}
		if err := json.Unmarshal([]byte(capabilities), &item.Capabilities); err != nil {
			return nil, fmt.Errorf("decode GitLab capabilities: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListMulticaInstances(ctx context.Context, workspaceID domain.ID) ([]domain.MulticaInstance, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, i.name, i.base_url, i.external_id, i.management_credential_ref_id, i.status, i.capabilities_json, s.alias, s.kind
		FROM multica_instances i LEFT JOIN secrets s ON s.ref_id = i.management_credential_ref_id AND s.workspace_id = i.workspace_id
		WHERE i.workspace_id = ? ORDER BY i.name, i.id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list Multica instances: %w", err)
	}
	defer rows.Close()
	var out []domain.MulticaInstance
	for rows.Next() {
		var item domain.MulticaInstance
		var external, credential sql.NullString
		var capabilities string
		var credentialAlias, credentialKind sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &item.BaseURL, &external, &credential, &item.Status, &capabilities, &credentialAlias, &credentialKind); err != nil {
			return nil, err
		}
		item.WorkspaceID = workspaceID
		if external.Valid {
			item.ExternalID = external.String
		}
		if credential.Valid && credential.String != "" {
			item.ManagementCredentialRef = &domain.SecretRef{ID: domain.ID(credential.String), WorkspaceID: workspaceID, Alias: credentialAlias.String, Kind: domain.SecretKind(credentialKind.String)}
		}
		if err := json.Unmarshal([]byte(capabilities), &item.Capabilities); err != nil {
			return nil, fmt.Errorf("decode Multica capabilities: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) DisableGitLabInstance(ctx context.Context, workspaceID, instanceID domain.ID) error {
	return s.disableEndpoint(ctx, "gitlab_instances", workspaceID, instanceID)
}

func (s *Store) DisableMulticaInstance(ctx context.Context, workspaceID, instanceID domain.ID) error {
	return s.disableEndpoint(ctx, "multica_instances", workspaceID, instanceID)
}

func (s *Store) UpdateGitLabCapabilities(ctx context.Context, workspaceID, instanceID domain.ID, capabilities []string) error {
	return s.updateEndpointCapabilities(ctx, "gitlab_instances", workspaceID, instanceID, capabilities)
}

func (s *Store) UpdateMulticaCapabilities(ctx context.Context, workspaceID, instanceID domain.ID, capabilities []string) error {
	return s.updateEndpointCapabilities(ctx, "multica_instances", workspaceID, instanceID, capabilities)
}

func (s *Store) updateEndpointCapabilities(ctx context.Context, table string, workspaceID, instanceID domain.ID, capabilities []string) error {
	if table != "gitlab_instances" && table != "multica_instances" {
		return fmt.Errorf("%w: unsupported endpoint table", domain.ErrInvalid)
	}
	encoded, err := marshalJSON(capabilities, "[]")
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE `+table+` SET capabilities_json = ? WHERE workspace_id = ? AND id = ?`, encoded, workspaceID, instanceID)
	if err != nil {
		return fmt.Errorf("update endpoint capabilities: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: endpoint %s", domain.ErrNotFound, instanceID)
	}
	return nil
}

func (s *Store) disableEndpoint(ctx context.Context, table string, workspaceID, instanceID domain.ID) error {
	if table != "gitlab_instances" && table != "multica_instances" {
		return fmt.Errorf("%w: unsupported endpoint table", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE `+table+` SET status = 'disabled' WHERE workspace_id = ? AND id = ?`, workspaceID, instanceID)
	if err != nil {
		return fmt.Errorf("disable endpoint: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: endpoint %s", domain.ErrNotFound, instanceID)
	}
	return nil
}

func (s *Store) GetGitLabGroupBinding(ctx context.Context, workspaceID, bindingID domain.ID) (domain.GitLabGroupBinding, error) {
	var binding domain.GitLabGroupBinding
	var credential, profile sql.NullString
	var credentialAlias, credentialKind sql.NullString
	var inherit int
	err := s.db.QueryRowContext(ctx, `SELECT b.id, b.gitlab_instance_id, b.external_group_id, b.full_path,
		b.credential_ref_id, b.credential_profile_id, b.inherit_subgroups, b.status, s.alias, s.kind
		FROM gitlab_group_bindings b
		LEFT JOIN secrets s ON s.ref_id = b.credential_ref_id AND s.workspace_id = b.workspace_id
		WHERE b.workspace_id = ? AND b.id = ?`, workspaceID, bindingID).Scan(&binding.ID, &binding.GitLabInstanceID,
		&binding.ExternalGroupID, &binding.FullPath, &credential, &profile, &inherit, &binding.Status, &credentialAlias, &credentialKind)
	if err == sql.ErrNoRows {
		return domain.GitLabGroupBinding{}, fmt.Errorf("%w: GitLab group binding %s", domain.ErrNotFound, bindingID)
	}
	if err != nil {
		return domain.GitLabGroupBinding{}, fmt.Errorf("get GitLab group binding: %w", err)
	}
	binding.WorkspaceID = workspaceID
	binding.InheritSubgroups = inherit != 0
	if profile.Valid {
		binding.CredentialProfileID = domain.ID(profile.String)
	}
	if credential.Valid && credential.String != "" {
		binding.CredentialRef = &domain.SecretRef{ID: domain.ID(credential.String), WorkspaceID: workspaceID, Alias: credentialAlias.String, Kind: domain.SecretKind(credentialKind.String)}
	}
	return binding, nil
}

// GetGitLabGroupBindingByGroup resolves a configured Group without exposing
// another Workspace's binding.  It is used by route reconciliation and the
// runtime credential seam.
func (s *Store) GetGitLabGroupBindingByGroup(ctx context.Context, workspaceID, instanceID domain.ID, externalGroupID string) (domain.GitLabGroupBinding, error) {
	var bindingID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM gitlab_group_bindings
		WHERE workspace_id = ? AND gitlab_instance_id = ? AND external_group_id = ?`, workspaceID, instanceID, externalGroupID).Scan(&bindingID)
	if err == sql.ErrNoRows {
		return domain.GitLabGroupBinding{}, fmt.Errorf("%w: GitLab Group %s", domain.ErrNotFound, externalGroupID)
	}
	if err != nil {
		return domain.GitLabGroupBinding{}, fmt.Errorf("find GitLab Group binding: %w", err)
	}
	return s.GetGitLabGroupBinding(ctx, workspaceID, domain.ID(bindingID))
}

func (s *Store) ListGitLabGroupBindings(ctx context.Context, workspaceID, instanceID domain.ID) ([]domain.GitLabGroupBinding, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM gitlab_group_bindings
		WHERE workspace_id = ? AND gitlab_instance_id = ? ORDER BY full_path, id`, workspaceID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list GitLab Group bindings: %w", err)
	}
	defer rows.Close()
	var result []domain.GitLabGroupBinding
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		item, err := s.GetGitLabGroupBinding(ctx, workspaceID, domain.ID(id))
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdateGitLabGroupCredential(ctx context.Context, workspaceID, bindingID, profileID domain.ID, ref *domain.SecretRef) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	if profileID.Empty() || ref == nil {
		return fmt.Errorf("%w: credential profile and secret reference are required", domain.ErrInvalid)
	}
	if ref.WorkspaceID != workspaceID {
		return fmt.Errorf("%w: credential belongs to another workspace", domain.ErrForbidden)
	}
	var profileWorkspace string
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM credential_profiles WHERE id = ?`, profileID).Scan(&profileWorkspace); err == sql.ErrNoRows {
		return fmt.Errorf("%w: credential profile %s", domain.ErrNotFound, profileID)
	} else if err != nil {
		return fmt.Errorf("check credential profile: %w", err)
	} else if domain.ID(profileWorkspace) != workspaceID {
		return fmt.Errorf("%w: credential profile belongs to another workspace", domain.ErrForbidden)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE gitlab_group_bindings SET credential_ref_id = ?, credential_profile_id = ?
		WHERE workspace_id = ? AND id = ?`, ref.ID, profileID, workspaceID, bindingID)
	if err != nil {
		return constraintError("update GitLab group credential", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: GitLab group binding %s", domain.ErrNotFound, bindingID)
	}
	return nil
}
