package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"specwire/bridge/internal/domain"
)

type OAuthStateRecord struct {
	ID                     domain.ID
	IdentityProviderID     domain.ID
	StateHash              string
	Nonce                  string
	CodeVerifierNonce      []byte
	CodeVerifierCiphertext []byte
	RedirectURI            string
	ExpiresAt              time.Time
}

func (s *Store) SaveOAuthState(ctx context.Context, state OAuthStateRecord) error {
	if state.ID.Empty() || state.IdentityProviderID.Empty() || state.StateHash == "" || state.Nonce == "" || state.RedirectURI == "" || state.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: incomplete OAuth state", domain.ErrInvalid)
	}
	if len(state.CodeVerifierNonce) == 0 || len(state.CodeVerifierCiphertext) == 0 {
		return fmt.Errorf("%w: OAuth code verifier must be encrypted", domain.ErrInvalid)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO oauth_authorization_states
		(id, identity_provider_id, state_hash, nonce, code_verifier_ciphertext,
		code_verifier_nonce, redirect_uri, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		state.ID, state.IdentityProviderID, state.StateHash, state.Nonce,
		state.CodeVerifierCiphertext, state.CodeVerifierNonce, state.RedirectURI,
		state.ExpiresAt.Format(time.RFC3339Nano))
	return constraintError("save OAuth state", err)
}

// ConsumeOAuthState atomically reads and consumes a state.  A state is
// single-use and expired states are indistinguishable from unknown states.
func (s *Store) ConsumeOAuthState(ctx context.Context, stateHash string, now time.Time) (OAuthStateRecord, error) {
	if stateHash == "" {
		return OAuthStateRecord{}, fmt.Errorf("%w: OAuth state is required", domain.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthStateRecord{}, fmt.Errorf("begin consume OAuth state: %w", err)
	}
	defer tx.Rollback()
	var record OAuthStateRecord
	var expires string
	err = tx.QueryRowContext(ctx, `SELECT id, identity_provider_id, state_hash, nonce,
		code_verifier_nonce, code_verifier_ciphertext, redirect_uri, expires_at
		FROM oauth_authorization_states WHERE state_hash = ? AND consumed_at IS NULL`, stateHash).Scan(
		&record.ID, &record.IdentityProviderID, &record.StateHash, &record.Nonce,
		&record.CodeVerifierNonce, &record.CodeVerifierCiphertext, &record.RedirectURI, &expires)
	if err == sql.ErrNoRows {
		return OAuthStateRecord{}, fmt.Errorf("%w: OAuth state", domain.ErrNotFound)
	}
	if err != nil {
		return OAuthStateRecord{}, fmt.Errorf("read OAuth state: %w", err)
	}
	record.ExpiresAt, err = decodeTime(expires)
	if err != nil {
		return OAuthStateRecord{}, fmt.Errorf("decode OAuth state expiry: %w", err)
	}
	if !now.Before(record.ExpiresAt) {
		return OAuthStateRecord{}, fmt.Errorf("%w: OAuth state expired", domain.ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_authorization_states SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`, now.UTC().Format(time.RFC3339Nano), record.ID); err != nil {
		return OAuthStateRecord{}, fmt.Errorf("consume OAuth state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return OAuthStateRecord{}, fmt.Errorf("commit OAuth state: %w", err)
	}
	return record, nil
}
