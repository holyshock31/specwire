package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"specwire/bridge/internal/domain"
)

// ClaimIdempotency atomically records a platform idempotency key.  The
// returned bool is true only for the caller that inserted the key.
func (s *Store) ClaimIdempotency(ctx context.Context, workspaceID domain.ID, scope, key string, expiresAt *time.Time) (bool, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return false, err
	}
	if scope == "" || key == "" {
		return false, fmt.Errorf("%w: idempotency scope and key are required", domain.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin idempotency claim: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO idempotency_keys
		(id, workspace_id, scope, key, claimed_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		domain.NewID(), workspaceID, scope, key, nowText(), formatOptionalTime(expiresAt))
	if err != nil {
		return false, fmt.Errorf("claim idempotency key: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read idempotency claim result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit idempotency claim: %w", err)
	}
	return affected == 1, nil
}

func (s *Store) HasIdempotencyKey(ctx context.Context, workspaceID domain.ID, scope, key string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM idempotency_keys
		WHERE workspace_id = ? AND scope = ? AND key = ?`, workspaceID, scope, key).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check idempotency key: %w", err)
	}
	return count != 0, nil
}

type SecretRecord struct {
	Ref        domain.SecretRef
	Nonce      []byte
	Ciphertext []byte
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (s *Store) PutSecretRecord(ctx context.Context, ref domain.SecretRef, nonce, ciphertext []byte) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if len(nonce) == 0 || len(ciphertext) == 0 {
		return fmt.Errorf("%w: encrypted secret payload is required", domain.ErrInvalid)
	}
	now := nowText()
	_, err := s.db.ExecContext(ctx, `INSERT INTO secrets
		(ref_id, workspace_id, alias, kind, nonce, ciphertext, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(ref_id) DO UPDATE SET
		workspace_id = excluded.workspace_id,
		alias = excluded.alias,
		kind = excluded.kind,
		nonce = excluded.nonce,
		ciphertext = excluded.ciphertext,
		updated_at = excluded.updated_at`,
		ref.ID, ref.WorkspaceID, ref.Alias, ref.Kind, nonce, ciphertext, now, now)
	return constraintError("put secret record", err)
}

func (s *Store) GetSecretRecord(ctx context.Context, workspaceID, refID domain.ID) (SecretRecord, error) {
	var record SecretRecord
	var created, updated string
	var alias string
	var kind domain.SecretKind
	err := s.db.QueryRowContext(ctx, `SELECT ref_id, workspace_id, alias, kind,
		nonce, ciphertext, created_at, updated_at FROM secrets
		WHERE workspace_id = ? AND ref_id = ?`, workspaceID, refID).Scan(
		&record.Ref.ID, &record.Ref.WorkspaceID, &alias, &kind, &record.Nonce,
		&record.Ciphertext, &created, &updated)
	if err == sql.ErrNoRows {
		return SecretRecord{}, fmt.Errorf("%w: secret %s", domain.ErrNotFound, refID)
	}
	if err != nil {
		return SecretRecord{}, fmt.Errorf("get secret record: %w", err)
	}
	record.Ref.Alias = alias
	record.Ref.Kind = kind
	record.CreatedAt, err = decodeTime(created)
	if err != nil {
		return SecretRecord{}, fmt.Errorf("decode secret created_at: %w", err)
	}
	record.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return SecretRecord{}, fmt.Errorf("decode secret updated_at: %w", err)
	}
	return record, nil
}
