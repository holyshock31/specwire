package security

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/store"
)

type SecretBackend interface {
	PutSecretRecord(context.Context, domain.SecretRef, []byte, []byte) error
	GetSecretRecord(context.Context, domain.ID, domain.ID) (store.SecretRecord, error)
}

type Vault struct {
	backend SecretBackend
	block   cipher.AEAD
}

func NewVault(backend SecretBackend, masterKey []byte) (*Vault, error) {
	if backend == nil {
		return nil, fmt.Errorf("%w: secret backend is required", domain.ErrInvalid)
	}
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("%w: secret master key must be 32 bytes", domain.ErrInvalid)
	}
	block, err := aes.NewCipher(append([]byte(nil), masterKey...))
	if err != nil {
		return nil, fmt.Errorf("create secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create secret AEAD: %w", err)
	}
	return &Vault{backend: backend, block: aead}, nil
}

func (v *Vault) Put(ctx context.Context, ref domain.SecretRef, material []byte) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if len(material) == 0 {
		return fmt.Errorf("%w: secret material is required", domain.ErrInvalid)
	}
	nonce := make([]byte, v.block.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate secret nonce: %w", err)
	}
	ciphertext := v.block.Seal(nil, nonce, material, []byte(ref.WorkspaceID+":"+ref.ID))
	return v.backend.PutSecretRecord(ctx, ref, nonce, ciphertext)
}

func (v *Vault) Resolve(ctx context.Context, ref domain.SecretRef) ([]byte, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	record, err := v.backend.GetSecretRecord(ctx, ref.WorkspaceID, ref.ID)
	if err != nil {
		return nil, err
	}
	if record.Ref.Alias != ref.Alias || record.Ref.Kind != ref.Kind {
		return nil, fmt.Errorf("%w: secret reference metadata mismatch", domain.ErrForbidden)
	}
	plaintext, err := v.block.Open(nil, record.Nonce, record.Ciphertext, []byte(ref.WorkspaceID+":"+ref.ID))
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt secret", domain.ErrForbidden)
	}
	return plaintext, nil
}

var sensitiveKeys = map[string]struct{}{
	"token": {}, "access_token": {}, "refresh_token": {}, "private_token": {},
	"password": {}, "passwd": {}, "secret": {}, "client_secret": {},
	"authorization": {}, "authorization_code": {}, "webhook_secret": {},
	"signing_secret": {}, "signing_token": {}, "api_key": {}, "apikey": {},
}

func isSecretReferenceKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	return normalized == "secret_ref" || strings.HasSuffix(normalized, "_secret_ref") ||
		normalized == "credential_ref" || strings.HasSuffix(normalized, "_credential_ref") ||
		normalized == "secret_alias" || strings.HasSuffix(normalized, "_secret_alias")
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	if isSecretReferenceKey(normalized) {
		return false
	}
	if _, ok := sensitiveKeys[normalized]; ok {
		return true
	}
	for _, fragment := range []string{"token", "password", "passwd", "client_secret", "authorization", "signing_secret"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func RedactJSON(input []byte) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON for redaction: %w", err)
	}
	return json.Marshal(RedactValue(value))
}

func RedactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if isSensitiveKey(key) {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = RedactValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = RedactValue(child)
		}
		return out
	default:
		return value
	}
}
