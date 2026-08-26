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

func TestAuthorizationDoesNotCrossWorkspaceOrInferProviderMembership(t *testing.T) {
	s := newAuthStore(t)
	ctx := context.Background()
	for _, workspace := range []domain.Workspace{
		{ID: "workspace-a", Slug: "workspace-a", Name: "Workspace A", Status: domain.WorkspaceActive},
		{ID: "workspace-b", Slug: "workspace-b", Name: "Workspace B", Status: domain.WorkspaceActive},
	} {
		if err := s.CreateWorkspace(ctx, workspace); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateAccount(ctx, domain.Account{ID: "account-shared", Email: "shared@example.com", DisplayName: "Shared", Status: domain.AccountActive}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMembership(ctx, domain.WorkspaceMembership{ID: "membership-a", WorkspaceID: "workspace-a", AccountID: "account-shared"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMembership(ctx, domain.WorkspaceMembership{ID: "membership-b", WorkspaceID: "workspace-b", AccountID: "account-shared"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRole(ctx, domain.ScopedRoleBinding{ID: "role-a", WorkspaceID: "workspace-a", MembershipID: "membership-a", Role: domain.RoleOperator, ScopeType: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRole(ctx, domain.ScopedRoleBinding{ID: "role-b", WorkspaceID: "workspace-b", MembershipID: "membership-b", Role: domain.RoleViewer, ScopeType: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if err := Authorize(ctx, s, "account-shared", "workspace-a", domain.RoleOperator, Scope{Type: "workspace", ID: "workspace-a"}); err != nil {
		t.Fatalf("workspace A authorization rejected: %v", err)
	}
	if err := Authorize(ctx, s, "account-shared", "workspace-b", domain.RoleOperator, Scope{Type: "workspace", ID: "workspace-b"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("workspace B role escalation = %v, want forbidden", err)
	}
	if err := Authorize(ctx, s, "account-shared", "workspace-missing", domain.RoleViewer, Scope{Type: "workspace", ID: "workspace-missing"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("unknown workspace probing = %v, want forbidden", err)
	}
	if err := s.CreateAccount(ctx, domain.Account{ID: "account-provider-scope", Email: "provider@example.com", DisplayName: "Provider Scope", Status: domain.AccountActive}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMembership(ctx, domain.WorkspaceMembership{ID: "membership-provider-scope", WorkspaceID: "workspace-a", AccountID: "account-provider-scope"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRole(ctx, domain.ScopedRoleBinding{ID: "role-provider-scope", WorkspaceID: "workspace-a", MembershipID: "membership-provider-scope", Role: domain.RoleViewer, ScopeType: "gitlab_group", ScopeID: "group-bound"}); err != nil {
		t.Fatal(err)
	}
	// Provider identity or login email is intentionally irrelevant to product
	// authorization; a Group-scoped role is required for this operation.
	if err := Authorize(ctx, s, "account-provider-scope", "workspace-a", domain.RoleViewer, Scope{Type: "gitlab_group", ID: "group-not-bound"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("unbound provider group access = %v, want forbidden", err)
	}
}

func TestViewerCannotMutateCredentialOrReplayScopes(t *testing.T) {
	s := newAuthStore(t)
	ctx := context.Background()
	if err := s.CreateWorkspace(ctx, domain.Workspace{ID: "workspace-viewer", Slug: "viewer", Name: "Viewer", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAccount(ctx, domain.Account{ID: "account-viewer", Email: "viewer@example.com", DisplayName: "Viewer", Status: domain.AccountActive}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMembership(ctx, domain.WorkspaceMembership{ID: "membership-viewer", WorkspaceID: "workspace-viewer", AccountID: "account-viewer"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRole(ctx, domain.ScopedRoleBinding{ID: "role-viewer", WorkspaceID: "workspace-viewer", MembershipID: "membership-viewer", Role: domain.RoleViewer, ScopeType: "workspace"}); err != nil {
		t.Fatal(err)
	}
	for _, required := range []domain.Role{domain.RoleAdmin, domain.RoleOperator} {
		if err := Authorize(ctx, s, "account-viewer", "workspace-viewer", required, Scope{Type: "workspace", ID: "workspace-viewer"}); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("viewer authorization for %s = %v, want forbidden", required, err)
		}
	}
	if err := Authorize(ctx, s, "account-viewer", "workspace-viewer", domain.RoleViewer, Scope{Type: "workspace", ID: "workspace-viewer"}); err != nil {
		t.Fatalf("viewer read authorization rejected: %v", err)
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
