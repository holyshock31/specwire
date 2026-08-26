package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/store"
)

type OIDCConfig struct {
	AuthorizationURL string
	ClientID         string
	RedirectURI      string
	Scopes           []string
}

type OIDCClaims struct {
	Subject     string
	Email       string
	DisplayName string
}

// OIDCExchanger is the provider seam.  The HTTP/token implementation belongs
// to an adapter; the auth package owns PKCE state, identity linking and
// membership semantics.
type OIDCExchanger interface {
	Exchange(context.Context, string, string, string, string) (OIDCClaims, error)
}

type OIDCStore interface {
	SaveOAuthState(context.Context, store.OAuthStateRecord) error
	ConsumeOAuthState(context.Context, string, time.Time) (store.OAuthStateRecord, error)
	FindAccountByIdentity(context.Context, domain.ID, string) (domain.Account, error)
	FindAccountByEmail(context.Context, string) (domain.Account, error)
	CreatePendingExternalAccount(context.Context, domain.Account, domain.ExternalIdentity) error
}

type AuthorizationRequest struct {
	URL   string
	State string
}

type ExternalLoginResult struct {
	Account domain.Account
	Pending bool
}

type OIDCProvider struct {
	store     OIDCStore
	config    OIDCConfig
	stateAEAD cipher.AEAD
	stateTTL  time.Duration
	now       func() time.Time
}

func NewOIDCProvider(store OIDCStore, config OIDCConfig, stateKey []byte) (*OIDCProvider, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: OIDC store is required", domain.ErrInvalid)
	}
	if strings.TrimSpace(config.AuthorizationURL) == "" || strings.TrimSpace(config.ClientID) == "" || strings.TrimSpace(config.RedirectURI) == "" {
		return nil, fmt.Errorf("%w: OIDC authorization URL, client ID and redirect URI are required", domain.ErrInvalid)
	}
	if len(stateKey) != 32 {
		return nil, fmt.Errorf("%w: OIDC state key must be 32 bytes", domain.ErrInvalid)
	}
	block, err := aes.NewCipher(append([]byte(nil), stateKey...))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(config.Scopes) == 0 {
		config.Scopes = []string{"openid", "profile", "email"}
	}
	return &OIDCProvider{store: store, config: config, stateAEAD: aead, stateTTL: 10 * time.Minute, now: time.Now}, nil
}

func (p *OIDCProvider) Begin(ctx context.Context, providerID domain.ID) (AuthorizationRequest, error) {
	if providerID.Empty() {
		return AuthorizationRequest{}, fmt.Errorf("%w: identity provider id is required", domain.ErrInvalid)
	}
	state, err := randomToken()
	if err != nil {
		return AuthorizationRequest{}, err
	}
	nonce, err := randomToken()
	if err != nil {
		return AuthorizationRequest{}, err
	}
	verifier, err := randomToken()
	if err != nil {
		return AuthorizationRequest{}, err
	}
	hash := hashToken(state)
	nonceBytes := make([]byte, p.stateAEAD.NonceSize())
	if _, err := randRead(nonceBytes); err != nil {
		return AuthorizationRequest{}, err
	}
	ciphertext := p.stateAEAD.Seal(nil, nonceBytes, []byte(verifier), []byte(hash))
	expires := p.now().UTC().Add(p.stateTTL)
	if err := p.store.SaveOAuthState(ctx, store.OAuthStateRecord{ID: domain.NewID(), IdentityProviderID: providerID, StateHash: hash, Nonce: nonce, CodeVerifierNonce: nonceBytes, CodeVerifierCiphertext: ciphertext, RedirectURI: p.config.RedirectURI, ExpiresAt: expires}); err != nil {
		return AuthorizationRequest{}, err
	}
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", p.config.ClientID)
	query.Set("redirect_uri", p.config.RedirectURI)
	query.Set("scope", strings.Join(p.config.Scopes, " "))
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", codeChallenge(verifier))
	query.Set("code_challenge_method", "S256")
	endpoint, err := url.Parse(p.config.AuthorizationURL)
	if err != nil {
		return AuthorizationRequest{}, fmt.Errorf("parse OIDC authorization URL: %w", err)
	}
	endpoint.RawQuery = query.Encode()
	return AuthorizationRequest{URL: endpoint.String(), State: state}, nil
}

func (p *OIDCProvider) Complete(ctx context.Context, exchanger OIDCExchanger, code, state string) (ExternalLoginResult, error) {
	if exchanger == nil || strings.TrimSpace(code) == "" || strings.TrimSpace(state) == "" {
		return ExternalLoginResult{}, fmt.Errorf("%w: OIDC callback is incomplete", domain.ErrInvalid)
	}
	record, err := p.store.ConsumeOAuthState(ctx, hashToken(state), p.now().UTC())
	if err != nil {
		return ExternalLoginResult{}, fmt.Errorf("%w: invalid OIDC callback state", domain.ErrForbidden)
	}
	verifier, err := p.stateAEAD.Open(nil, record.CodeVerifierNonce, record.CodeVerifierCiphertext, []byte(record.StateHash))
	if err != nil {
		return ExternalLoginResult{}, fmt.Errorf("%w: invalid OIDC callback state", domain.ErrForbidden)
	}
	claims, err := exchanger.Exchange(ctx, code, record.RedirectURI, string(verifier), record.Nonce)
	if err != nil {
		return ExternalLoginResult{}, fmt.Errorf("OIDC token exchange: %w", err)
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return ExternalLoginResult{}, fmt.Errorf("%w: OIDC subject is required", domain.ErrForbidden)
	}
	account, err := p.store.FindAccountByIdentity(ctx, record.IdentityProviderID, claims.Subject)
	if err == nil {
		return ExternalLoginResult{Account: account, Pending: account.Status != domain.AccountActive}, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return ExternalLoginResult{}, err
	}
	account = domain.Account{ID: domain.NewID(), Email: p.pendingEmail(ctx, record.IdentityProviderID, claims), DisplayName: firstNonEmpty(claims.DisplayName, claims.Subject), Status: domain.AccountPending, CreatedAt: p.now().UTC(), UpdatedAt: p.now().UTC()}
	identity := domain.ExternalIdentity{ID: domain.NewID(), AccountID: account.ID, IdentityProviderID: record.IdentityProviderID, Subject: claims.Subject, EmailSnapshot: NormalizeEmail(claims.Email), CreatedAt: account.CreatedAt}
	if err := p.store.CreatePendingExternalAccount(ctx, account, identity); err != nil {
		return ExternalLoginResult{}, err
	}
	return ExternalLoginResult{Account: account, Pending: true}, nil
}

func (p *OIDCProvider) pendingEmail(ctx context.Context, providerID domain.ID, claims OIDCClaims) string {
	email := NormalizeEmail(claims.Email)
	if email == "" {
		return syntheticEmail(providerID, claims.Subject)
	}
	if _, err := p.store.FindAccountByEmail(ctx, email); errors.Is(err, domain.ErrNotFound) {
		return email
	}
	return syntheticEmail(providerID, claims.Subject)
}

func syntheticEmail(providerID domain.ID, subject string) string {
	sum := sha256.Sum256([]byte(string(providerID) + ":" + subject))
	return "pending-" + hexShort(sum[:]) + "@accounts.invalid"
}

func hexShort(value []byte) string { return base64.RawURLEncoding.EncodeToString(value)[:16] }

func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Kept as a variable-sized helper so tests can replace it without changing
// the PKCE/state algorithm.
var randRead = func(dst []byte) (int, error) { return rand.Reader.Read(dst) }

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "External account"
}
