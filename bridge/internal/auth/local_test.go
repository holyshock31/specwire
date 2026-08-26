package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/store"
)

func newAuthStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir() + "/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestLocalProviderBootstrapLoginCSRFAndLogout(t *testing.T) {
	s := newAuthStore(t)
	provider, err := NewLocalProvider(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.BootstrapFirstAdmin(context.Background(), "ADMIN@EXAMPLE.COM", "correct horse battery staple", "Administrator"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.BootstrapFirstAdmin(context.Background(), "second@example.com", "correct horse battery staple", "Second"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second bootstrap error = %v, want conflict", err)
	}
	account, credentials, err := provider.Login(context.Background(), " admin@example.com ", "correct horse battery staple")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if account.Email != "admin@example.com" || credentials.Token == "" || credentials.CSRFToken == "" {
		t.Fatalf("login result incomplete: %+v %+v", account, credentials)
	}
	session, err := provider.Authenticate(context.Background(), credentials.Token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if err := provider.ValidateCSRF(session, credentials.CSRFToken); err != nil {
		t.Fatalf("ValidateCSRF: %v", err)
	}
	if err := provider.ValidateCSRF(session, "wrong"); !errors.Is(err, ErrCSRF) {
		t.Fatalf("wrong CSRF = %v", err)
	}
	if _, _, err := provider.Login(context.Background(), "admin@example.com", "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password = %v", err)
	}
	if err := provider.Logout(context.Background(), credentials.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Authenticate(context.Background(), credentials.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("revoked session = %v", err)
	}
}

func TestAuthorizeFixedRolesAndScopes(t *testing.T) {
	s := newAuthStore(t)
	ctx := context.Background()
	if err := s.CreateWorkspace(ctx, domain.Workspace{ID: "workspace-authz", Slug: "authz", Name: "Authz", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAccount(ctx, domain.Account{ID: "account-operator", Email: "operator@example.com", DisplayName: "Operator", Status: domain.AccountActive}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMembership(ctx, domain.WorkspaceMembership{ID: "membership-operator", WorkspaceID: "workspace-authz", AccountID: "account-operator"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRole(ctx, domain.ScopedRoleBinding{ID: "role-operator", WorkspaceID: "workspace-authz", MembershipID: "membership-operator", Role: domain.RoleOperator, ScopeType: "gitlab_group", ScopeID: "group-a"}); err != nil {
		t.Fatal(err)
	}
	if err := Authorize(ctx, s, "account-operator", "workspace-authz", domain.RoleOperator, Scope{Type: "gitlab_group", ID: "group-a"}); err != nil {
		t.Fatalf("authorized group operation rejected: %v", err)
	}
	if err := Authorize(ctx, s, "account-operator", "workspace-authz", domain.RoleOperator, Scope{Type: "gitlab_group", ID: "group-b"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("out-of-scope operation = %v, want forbidden", err)
	}
	if err := Authorize(ctx, s, "account-operator", "workspace-authz", domain.RoleAdmin, Scope{Type: "workspace"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("operator escalation = %v, want forbidden", err)
	}
	if err := s.GrantRole(ctx, domain.ScopedRoleBinding{ID: "custom-role", WorkspaceID: "workspace-authz", MembershipID: "membership-operator", Role: "connector_admin", ScopeType: "workspace"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("custom role = %v, want invalid", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	s := newAuthStore(t)
	p, err := NewLocalProvider(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetSessionTTL(time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.BootstrapFirstAdmin(context.Background(), "admin@example.com", "correct horse battery staple", "Admin"); err != nil {
		t.Fatal(err)
	}
	_, credentials, err := p.Login(context.Background(), "admin@example.com", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := p.Authenticate(context.Background(), credentials.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired session = %v, want invalid session", err)
	}
}
