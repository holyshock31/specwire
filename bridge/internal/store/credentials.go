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

func (s *Store) CreateCredentialProfile(ctx context.Context, profile domain.CredentialProfile) error {
	if profile.ID.Empty() || profile.SecretRef.ID.Empty() {
		return fmt.Errorf("%w: credential profile and secret reference IDs are required", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(profile.WorkspaceID); err != nil {
		return err
	}
	if profile.SecretRef.WorkspaceID != profile.WorkspaceID {
		return fmt.Errorf("%w: secret reference belongs to another workspace", domain.ErrForbidden)
	}
	if profile.Provider != domain.ProviderGitLab && profile.Provider != domain.ProviderMultica {
		return fmt.Errorf("%w: unsupported credential provider", domain.ErrInvalid)
	}
	if profile.Kind != domain.CredentialPAT && profile.Kind != domain.CredentialGroupAccessToken {
		return fmt.Errorf("%w: unsupported credential profile kind", domain.ErrInvalid)
	}
	if strings.TrimSpace(profile.Alias) == "" {
		return fmt.Errorf("%w: credential alias is required", domain.ErrInvalid)
	}
	if profile.Status == "" {
		profile.Status = domain.CredentialActive
	}
	capabilities, err := marshalJSON(profile.Capabilities, "[]")
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO credential_profiles
		(id, workspace_id, provider, kind, alias, secret_ref_id, status, capabilities_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, profile.ID, profile.WorkspaceID, profile.Provider, profile.Kind,
		profile.Alias, profile.SecretRef.ID, profile.Status, capabilities, nowText(), nowText())
	return constraintError("create credential profile", err)
}

func (s *Store) GetCredentialProfile(ctx context.Context, workspaceID, profileID domain.ID) (domain.CredentialProfile, error) {
	var profile domain.CredentialProfile
	var secretRefID string
	var capabilities string
	err := s.db.QueryRowContext(ctx, `SELECT id, provider, kind, alias, secret_ref_id, status, capabilities_json
		FROM credential_profiles WHERE workspace_id = ? AND id = ?`, workspaceID, profileID).Scan(&profile.ID, &profile.Provider,
		&profile.Kind, &profile.Alias, &secretRefID, &profile.Status, &capabilities)
	if err == sql.ErrNoRows {
		return domain.CredentialProfile{}, fmt.Errorf("%w: credential profile %s", domain.ErrNotFound, profileID)
	}
	if err != nil {
		return domain.CredentialProfile{}, fmt.Errorf("get credential profile: %w", err)
	}
	profile.WorkspaceID = workspaceID
	profile.SecretRef = domain.SecretRef{ID: domain.ID(secretRefID), WorkspaceID: workspaceID, Alias: profile.Alias, Kind: domain.SecretGroupCredential}
	if err := json.Unmarshal([]byte(capabilities), &profile.Capabilities); err != nil {
		return domain.CredentialProfile{}, fmt.Errorf("decode credential capabilities: %w", err)
	}
	return profile, nil
}

func (s *Store) RotateCredentialProfile(ctx context.Context, workspaceID, profileID domain.ID, ref domain.SecretRef) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if ref.WorkspaceID != workspaceID {
		return fmt.Errorf("%w: secret reference belongs to another workspace", domain.ErrForbidden)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE credential_profiles SET secret_ref_id = ?, updated_at = ?, status = 'active'
		WHERE workspace_id = ? AND id = ?`, ref.ID, nowText(), workspaceID, profileID)
	if err != nil {
		return constraintError("rotate credential profile", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: credential profile %s", domain.ErrNotFound, profileID)
	}
	return nil
}

func (s *Store) RecordCapabilityResults(ctx context.Context, workspaceID domain.ID, provider domain.ProviderKind, instanceID domain.ID, resourceType, resourceID string, results []domain.CapabilityResult) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin capability record: %w", err)
	}
	defer tx.Rollback()
	for _, result := range results {
		if strings.TrimSpace(result.Capability) == "" {
			return fmt.Errorf("%w: capability name is required", domain.ErrInvalid)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO provider_capability_checks
			(id, workspace_id, provider, instance_id, resource_type, resource_id, capability, available, reason, request_id, checked_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(workspace_id, instance_id, resource_type, resource_id, capability) DO UPDATE SET
			available = excluded.available, reason = excluded.reason, request_id = excluded.request_id, checked_at = excluded.checked_at`,
			domain.NewID(), workspaceID, provider, instanceID, resourceType, resourceID, result.Capability,
			boolInt(result.Available), result.Reason, result.RequestID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return constraintError("record capability result", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit capability record: %w", err)
	}
	return nil
}
