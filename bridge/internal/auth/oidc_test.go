package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"

	"specwire/bridge/internal/domain"
)

type fakeOIDCExchanger struct{}

func (fakeOIDCExchanger) Exchange(context.Context, string, string, string, string) (OIDCClaims, error) {
	return OIDCClaims{Subject: "oidc-subject-1", Email: "person@example.com", DisplayName: "OIDC Person"}, nil
}

func TestOIDCPKCEStateAndPendingFirstLogin(t *testing.T) {
	s := newAuthStore(t)
	ctx := context.Background()
	if err := s.CreateWorkspace(ctx, domain.Workspace{ID: "workspace-oidc", Slug: "oidc", Name: "OIDC", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateIdentityProvider(ctx, domain.IdentityProvider{ID: "idp-oidc", WorkspaceID: "workspace-oidc", Kind: "oidc", Name: "Corporate", IssuerURL: "https://issuer.example", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte("OIDC state encryption key"))
	p, err := NewOIDCProvider(s, OIDCConfig{AuthorizationURL: "https://issuer.example/authorize", ClientID: "client-1", RedirectURI: "https://specwire.example/auth/callback"}, key[:])
	if err != nil {
		t.Fatal(err)
	}
	request, err := p.Begin(ctx, "idp-oidc")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	parsed, err := url.Parse(request.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for _, required := range []string{"state", "nonce", "code_challenge", "code_challenge_method", "client_id", "redirect_uri"} {
		if query.Get(required) == "" {
			t.Fatalf("OIDC query missing %s: %s", required, request.URL)
		}
	}
	if query.Get("code_challenge_method") != "S256" {
		t.Fatal("PKCE method is not S256")
	}
	if query.Get("state") != request.State {
		t.Fatal("returned state differs from URL state")
	}
	result, err := p.Complete(ctx, fakeOIDCExchanger{}, "authorization-code", request.State)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !result.Pending || result.Account.Status != domain.AccountPending {
		t.Fatalf("first external login should be pending: %+v", result)
	}
	if result.Account.Email != "person@example.com" {
		t.Fatalf("pending email = %q", result.Account.Email)
	}
	if _, err := p.Complete(ctx, fakeOIDCExchanger{}, "authorization-code", request.State); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reused state = %v, want forbidden", err)
	}

	// The verifier is stored encrypted; the callback request only carries the
	// state and the code, never a verifier or secret in the URL.
	if strings.Contains(request.URL, "code_verifier") || strings.Contains(request.URL, base64.RawURLEncoding.EncodeToString(key[:])) {
		t.Fatal("OIDC authorization URL leaked private state material")
	}
}
