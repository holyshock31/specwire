package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"specwire/bridge/internal/domain"
)

func (s *Store) CreateIdentityProvider(ctx context.Context, provider domain.IdentityProvider) error {
	if provider.ID.Empty() {
		return fmt.Errorf("%w: identity provider id is required", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(provider.WorkspaceID); err != nil {
		return err
	}
	if strings.TrimSpace(provider.Kind) == "" || strings.TrimSpace(provider.Name) == "" {
		return fmt.Errorf("%w: identity provider kind and name are required", domain.ErrInvalid)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO identity_providers
		(id, workspace_id, kind, name, issuer_url, client_id, client_secret_ref_id, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, provider.ID, provider.WorkspaceID, provider.Kind,
		provider.Name, nullString(provider.IssuerURL), nullString(provider.ClientID),
		secretRefID(provider.ClientSecretRef), boolInt(provider.Enabled))
	return constraintError("create identity provider", err)
}

func (s *Store) CreateMembership(ctx context.Context, membership domain.WorkspaceMembership) error {
	if membership.ID.Empty() || membership.AccountID.Empty() {
		return fmt.Errorf("%w: membership id and account_id are required", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(membership.WorkspaceID); err != nil {
		return err
	}
	if membership.Status == "" {
		membership.Status = "active"
	}
	if membership.CreatedAt.IsZero() {
		membership.CreatedAt = s.now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspace_memberships
		(id, workspace_id, account_id, status, created_at) VALUES (?, ?, ?, ?, ?)`,
		membership.ID, membership.WorkspaceID, membership.AccountID, membership.Status,
		membership.CreatedAt.Format(time.RFC3339Nano))
	return constraintError("create workspace membership", err)
}

func (s *Store) GrantRole(ctx context.Context, binding domain.ScopedRoleBinding) error {
	if binding.ID.Empty() || binding.MembershipID.Empty() {
		return fmt.Errorf("%w: role binding id and membership_id are required", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(binding.WorkspaceID); err != nil {
		return err
	}
	if binding.Role != domain.RoleAdmin && binding.Role != domain.RoleOperator && binding.Role != domain.RoleViewer {
		return fmt.Errorf("%w: unsupported role %q", domain.ErrInvalid, binding.Role)
	}
	if strings.TrimSpace(binding.ScopeType) == "" {
		return fmt.Errorf("%w: role binding scope_type is required", domain.ErrInvalid)
	}
	if binding.ScopeType == "workspace" {
		binding.ScopeID = ""
	}
	var membershipWorkspace string
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_memberships WHERE id = ?`, binding.MembershipID).Scan(&membershipWorkspace); err == sql.ErrNoRows {
		return fmt.Errorf("%w: membership %s", domain.ErrNotFound, binding.MembershipID)
	} else if err != nil {
		return fmt.Errorf("check role membership: %w", err)
	} else if domain.ID(membershipWorkspace) != binding.WorkspaceID {
		return fmt.Errorf("%w: role membership belongs to another workspace", domain.ErrForbidden)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO scoped_role_bindings
		(id, workspace_id, membership_id, role, scope_type, scope_id) VALUES (?, ?, ?, ?, ?, ?)`,
		binding.ID, binding.WorkspaceID, binding.MembershipID, binding.Role, binding.ScopeType, nullID(binding.ScopeID))
	return constraintError("grant role", err)
}

// BootstrapFirstAdmin creates the first local account, the Default Workspace,
// its membership and an admin binding in one transaction.  It refuses to
// overwrite an existing installation.
func (s *Store) BootstrapFirstAdmin(ctx context.Context, account domain.Account, passwordHash string, workspace domain.Workspace, membershipID, roleBindingID domain.ID) error {
	if err := requireAccount(account); err != nil {
		return err
	}
	if strings.TrimSpace(passwordHash) == "" {
		return fmt.Errorf("%w: password hash is required", domain.ErrInvalid)
	}
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
	if membershipID.Empty() || roleBindingID.Empty() {
		return fmt.Errorf("%w: bootstrap membership and role binding IDs are required", domain.ErrInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin first admin bootstrap: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&count); err != nil {
		return fmt.Errorf("count accounts: %w", err)
	}
	if count != 0 {
		return fmt.Errorf("%w: first administrator already exists", domain.ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO accounts
		(id, email, display_name, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		account.ID, account.Email, account.DisplayName, account.Status,
		account.CreatedAt.Format(time.RFC3339Nano), account.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return constraintError("bootstrap account", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_password_credentials(account_id, password_hash, updated_at) VALUES (?, ?, ?)`, account.ID, passwordHash, nowText()); err != nil {
		return constraintError("bootstrap password", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces
		(id, slug, name, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, workspace.ID,
		workspace.Slug, workspace.Name, workspace.Status, workspace.CreatedAt.Format(time.RFC3339Nano), workspace.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return constraintError("bootstrap workspace", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_memberships
		(id, workspace_id, account_id, status, created_at) VALUES (?, ?, ?, 'active', ?)`, membershipID, workspace.ID, account.ID, nowText()); err != nil {
		return constraintError("bootstrap membership", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scoped_role_bindings
		(id, workspace_id, membership_id, role, scope_type, scope_id) VALUES (?, ?, ?, 'admin', 'workspace', NULL)`, roleBindingID, workspace.ID, membershipID); err != nil {
		return constraintError("bootstrap admin role", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit first admin bootstrap: %w", err)
	}
	return nil
}

func (s *Store) FindAccountByEmail(ctx context.Context, email string) (domain.Account, error) {
	var account domain.Account
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, email, display_name, status, created_at, updated_at
		FROM accounts WHERE email = ?`, email).Scan(&account.ID, &account.Email, &account.DisplayName, &account.Status, &created, &updated)
	if err == sql.ErrNoRows {
		return domain.Account{}, fmt.Errorf("%w: account", domain.ErrNotFound)
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("find account by email: %w", err)
	}
	account.CreatedAt, err = decodeTime(created)
	if err != nil {
		return domain.Account{}, err
	}
	account.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

func (s *Store) GetAccount(ctx context.Context, accountID domain.ID) (domain.Account, error) {
	var account domain.Account
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, email, display_name, status, created_at, updated_at
		FROM accounts WHERE id = ?`, accountID).Scan(&account.ID, &account.Email, &account.DisplayName, &account.Status, &created, &updated)
	if err == sql.ErrNoRows {
		return domain.Account{}, fmt.Errorf("%w: account %s", domain.ErrNotFound, accountID)
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("get account: %w", err)
	}
	account.CreatedAt, err = decodeTime(created)
	if err != nil {
		return domain.Account{}, err
	}
	account.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

func (s *Store) GetPasswordHash(ctx context.Context, accountID domain.ID) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM account_password_credentials WHERE account_id = ?`, accountID).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("%w: password credential", domain.ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("get password hash: %w", err)
	}
	return hash, nil
}

func (s *Store) CreateSession(ctx context.Context, session domain.Session) error {
	if session.ID.Empty() || session.AccountID.Empty() || session.TokenHash == "" || session.CSRFTokenHash == "" {
		return fmt.Errorf("%w: incomplete session", domain.ErrInvalid)
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = s.now()
	}
	if session.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: session expiry is required", domain.ErrInvalid)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions
		(id, account_id, token_hash, csrf_token_hash, expires_at, created_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, session.ID, session.AccountID, session.TokenHash, session.CSRFTokenHash,
		session.ExpiresAt.Format(time.RFC3339Nano), session.CreatedAt.Format(time.RFC3339Nano), formatOptionalTime(session.RevokedAt))
	return constraintError("create session", err)
}

func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash string) (domain.Session, error) {
	var session domain.Session
	var expires, created string
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, account_id, token_hash, csrf_token_hash, expires_at, created_at, revoked_at
		FROM sessions WHERE token_hash = ?`, tokenHash).Scan(&session.ID, &session.AccountID, &session.TokenHash,
		&session.CSRFTokenHash, &expires, &created, &revoked)
	if err == sql.ErrNoRows {
		return domain.Session{}, fmt.Errorf("%w: session", domain.ErrNotFound)
	}
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}
	session.ExpiresAt, err = decodeTime(expires)
	if err != nil {
		return domain.Session{}, err
	}
	session.CreatedAt, err = decodeTime(created)
	if err != nil {
		return domain.Session{}, err
	}
	session.RevokedAt, err = decodeOptionalTime(revoked)
	if err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func (s *Store) RevokeSession(ctx context.Context, sessionID domain.ID) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, nowText(), sessionID)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (s *Store) CreateExternalIdentity(ctx context.Context, identity domain.ExternalIdentity) error {
	if identity.ID.Empty() || identity.AccountID.Empty() || identity.IdentityProviderID.Empty() || strings.TrimSpace(identity.Subject) == "" {
		return fmt.Errorf("%w: incomplete external identity", domain.ErrInvalid)
	}
	if identity.CreatedAt.IsZero() {
		identity.CreatedAt = s.now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO external_identities
		(id, account_id, identity_provider_id, subject, email_snapshot, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, identity.ID, identity.AccountID, identity.IdentityProviderID,
		identity.Subject, identity.EmailSnapshot, identity.CreatedAt.Format(time.RFC3339Nano))
	return constraintError("create external identity", err)
}

func (s *Store) CreatePendingExternalAccount(ctx context.Context, account domain.Account, identity domain.ExternalIdentity) error {
	if account.Status == "" {
		account.Status = domain.AccountPending
	}
	if account.Status != domain.AccountPending {
		return fmt.Errorf("%w: external first-login account must be pending", domain.ErrInvalid)
	}
	if err := requireAccount(account); err != nil {
		return err
	}
	if identity.AccountID != account.ID {
		return fmt.Errorf("%w: identity account mismatch", domain.ErrInvalid)
	}
	if identity.ID.Empty() || identity.IdentityProviderID.Empty() || strings.TrimSpace(identity.Subject) == "" {
		return fmt.Errorf("%w: incomplete external identity", domain.ErrInvalid)
	}
	if identity.CreatedAt.IsZero() {
		identity.CreatedAt = s.now()
	}
	if account.CreatedAt.IsZero() {
		account.CreatedAt = identity.CreatedAt
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = account.CreatedAt
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pending external account: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO accounts
		(id, email, display_name, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		account.ID, account.Email, account.DisplayName, account.Status,
		account.CreatedAt.Format(time.RFC3339Nano), account.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return constraintError("create pending external account", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO external_identities
		(id, account_id, identity_provider_id, subject, email_snapshot, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, identity.ID, identity.AccountID, identity.IdentityProviderID,
		identity.Subject, identity.EmailSnapshot, identity.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return constraintError("link pending external identity", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pending external account: %w", err)
	}
	return nil
}

func (s *Store) FindAccountByIdentity(ctx context.Context, providerID domain.ID, subject string) (domain.Account, error) {
	var account domain.Account
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT a.id, a.email, a.display_name, a.status, a.created_at, a.updated_at
		FROM accounts a JOIN external_identities i ON i.account_id = a.id
		WHERE i.identity_provider_id = ? AND i.subject = ?`, providerID, subject).
		Scan(&account.ID, &account.Email, &account.DisplayName, &account.Status, &created, &updated)
	if err == sql.ErrNoRows {
		return domain.Account{}, fmt.Errorf("%w: external identity", domain.ErrNotFound)
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("find account by identity: %w", err)
	}
	account.CreatedAt, err = decodeTime(created)
	if err != nil {
		return domain.Account{}, err
	}
	account.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

func (s *Store) ListRoleBindings(ctx context.Context, accountID, workspaceID domain.ID) ([]domain.ScopedRoleBinding, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.workspace_id, r.membership_id, r.role, r.scope_type, r.scope_id
		FROM scoped_role_bindings r JOIN workspace_memberships m ON m.id = r.membership_id
		WHERE r.workspace_id = ? AND m.workspace_id = ? AND m.account_id = ? AND m.status = 'active'
		ORDER BY r.role, r.scope_type, r.scope_id`, workspaceID, workspaceID, accountID)
	if err != nil {
		return nil, fmt.Errorf("list role bindings: %w", err)
	}
	defer rows.Close()
	var out []domain.ScopedRoleBinding
	for rows.Next() {
		var item domain.ScopedRoleBinding
		var scopeID sql.NullString
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.MembershipID, &item.Role, &item.ScopeType, &scopeID); err != nil {
			return nil, err
		}
		if scopeID.Valid {
			item.ScopeID = domain.ID(scopeID.String)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
