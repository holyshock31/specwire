package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"specwire/bridge/internal/domain"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidSession     = errors.New("invalid session")
	ErrCSRF               = errors.New("csrf validation failed")
)

type Store interface {
	BootstrapFirstAdmin(context.Context, domain.Account, string, domain.Workspace, domain.ID, domain.ID) error
	FindAccountByEmail(context.Context, string) (domain.Account, error)
	GetPasswordHash(context.Context, domain.ID) (string, error)
	CreateSession(context.Context, domain.Session) error
	GetSessionByTokenHash(context.Context, string) (domain.Session, error)
	RotateSessionCSRF(context.Context, domain.ID, string) error
	RevokeSession(context.Context, domain.ID) error
	ListRoleBindings(context.Context, domain.ID, domain.ID) ([]domain.ScopedRoleBinding, error)
}

type LocalProvider struct {
	store      Store
	sessionTTL time.Duration
	now        func() time.Time
}

func NewLocalProvider(store Store) (*LocalProvider, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: auth store is required", domain.ErrInvalid)
	}
	return &LocalProvider{store: store, sessionTTL: 12 * time.Hour, now: time.Now}, nil
}

func (p *LocalProvider) SetSessionTTL(ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("%w: session TTL must be positive", domain.ErrInvalid)
	}
	p.sessionTTL = ttl
	return nil
}

func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", fmt.Errorf("%w: password must contain at least 8 characters", domain.ErrInvalid)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (p *LocalProvider) BootstrapFirstAdmin(ctx context.Context, email, password, displayName string) (domain.Account, domain.Workspace, error) {
	email = NormalizeEmail(email)
	if email == "" || strings.TrimSpace(displayName) == "" {
		return domain.Account{}, domain.Workspace{}, fmt.Errorf("%w: administrator email and display name are required", domain.ErrInvalid)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return domain.Account{}, domain.Workspace{}, err
	}
	now := p.now().UTC()
	account := domain.Account{ID: domain.NewID(), Email: email, DisplayName: strings.TrimSpace(displayName), Status: domain.AccountActive, CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{ID: domain.NewID(), Slug: "default", Name: "Default Workspace", Status: domain.WorkspaceActive, CreatedAt: now, UpdatedAt: now}
	if reader, ok := p.store.(interface {
		GetWorkspaceBySlug(context.Context, string) (domain.Workspace, error)
	}); ok {
		if existing, err := reader.GetWorkspaceBySlug(ctx, workspace.Slug); err == nil {
			workspace = existing
		}
	}
	if err := p.store.BootstrapFirstAdmin(ctx, account, hash, workspace, domain.NewID(), domain.NewID()); err != nil {
		return domain.Account{}, domain.Workspace{}, err
	}
	return account, workspace, nil
}

func (p *LocalProvider) Login(ctx context.Context, email, password string) (domain.Account, domain.SessionCredentials, error) {
	account, err := p.store.FindAccountByEmail(ctx, NormalizeEmail(email))
	if err != nil || account.Status != domain.AccountActive {
		return domain.Account{}, domain.SessionCredentials{}, ErrInvalidCredentials
	}
	hash, err := p.store.GetPasswordHash(ctx, account.ID)
	if err != nil || !CheckPassword(hash, password) {
		return domain.Account{}, domain.SessionCredentials{}, ErrInvalidCredentials
	}
	token, err := randomToken()
	if err != nil {
		return domain.Account{}, domain.SessionCredentials{}, fmt.Errorf("create session token: %w", err)
	}
	csrf, err := randomToken()
	if err != nil {
		return domain.Account{}, domain.SessionCredentials{}, fmt.Errorf("create CSRF token: %w", err)
	}
	now := p.now().UTC()
	expires := now.Add(p.sessionTTL)
	session := domain.Session{ID: domain.NewID(), AccountID: account.ID, TokenHash: hashToken(token), CSRFTokenHash: hashToken(csrf), CreatedAt: now, ExpiresAt: expires}
	if err := p.store.CreateSession(ctx, session); err != nil {
		return domain.Account{}, domain.SessionCredentials{}, err
	}
	return account, domain.SessionCredentials{SessionID: session.ID, Token: token, CSRFToken: csrf, ExpiresAt: expires}, nil
}

func (p *LocalProvider) Authenticate(ctx context.Context, token string) (domain.Session, error) {
	if token == "" {
		return domain.Session{}, ErrInvalidSession
	}
	session, err := p.store.GetSessionByTokenHash(ctx, hashToken(token))
	if err != nil || session.RevokedAt != nil || !p.now().Before(session.ExpiresAt) {
		return domain.Session{}, ErrInvalidSession
	}
	return session, nil
}

func (p *LocalProvider) ValidateCSRF(session domain.Session, csrfToken string) error {
	if csrfToken == "" || session.CSRFTokenHash == "" {
		return ErrCSRF
	}
	want := hashToken(csrfToken)
	if subtle.ConstantTimeCompare([]byte(want), []byte(session.CSRFTokenHash)) != 1 {
		return ErrCSRF
	}
	return nil
}

// RefreshCSRF replaces the CSRF secret for an authenticated session. The raw
// token is returned only to the caller; the store keeps its hash.
func (p *LocalProvider) RefreshCSRF(ctx context.Context, session domain.Session) (string, error) {
	if session.ID.Empty() {
		return "", ErrInvalidSession
	}
	csrf, err := randomToken()
	if err != nil {
		return "", fmt.Errorf("create CSRF token: %w", err)
	}
	if err := p.store.RotateSessionCSRF(ctx, session.ID, hashToken(csrf)); err != nil {
		return "", err
	}
	return csrf, nil
}

func (p *LocalProvider) Logout(ctx context.Context, sessionID domain.ID) error {
	if sessionID.Empty() {
		return ErrInvalidSession
	}
	return p.store.RevokeSession(ctx, sessionID)
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
