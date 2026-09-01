package domain

import "time"

type ExternalIdentity struct {
	ID                 ID        `json:"id"`
	AccountID          ID        `json:"account_id"`
	IdentityProviderID ID        `json:"identity_provider_id"`
	Subject            string    `json:"subject"`
	EmailSnapshot      string    `json:"email_snapshot,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

type Session struct {
	ID            ID         `json:"id"`
	AccountID     ID         `json:"account_id"`
	TokenHash     string     `json:"-"`
	CSRFTokenHash string     `json:"-"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}

type AuthorizationContext struct {
	AccountID   ID     `json:"account_id"`
	SessionID   ID     `json:"session_id"`
	WorkspaceID ID     `json:"workspace_id"`
	Role        Role   `json:"role"`
	ScopeType   string `json:"scope_type,omitempty"`
	ScopeID     ID     `json:"scope_id,omitempty"`
}

type SessionCredentials struct {
	SessionID ID        `json:"session_id"`
	Token     string    `json:"-"`
	CSRFToken string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
}
