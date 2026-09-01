package security

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/store"
)

func TestVaultEncryptsAtRestAndResolvesByReference(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateWorkspace(context.Background(), domain.Workspace{ID: "workspace-secret", Slug: "secret", Name: "Secret", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte("test deployment master key"))
	vault, err := NewVault(s, key[:])
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.SecretRef{ID: "secret-ref", WorkspaceID: "workspace-secret", Alias: "gitlab-group-token", Kind: domain.SecretGroupCredential}
	material := []byte("glpat-super-secret-value")
	if err := vault.Put(context.Background(), ref, material); err != nil {
		t.Fatalf("Put: %v", err)
	}
	record, err := s.GetSecretRecord(context.Background(), ref.WorkspaceID, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(record.Nonce), string(material)) || strings.Contains(string(record.Ciphertext), string(material)) {
		t.Fatal("plaintext secret material is present in stored ciphertext")
	}
	resolved, err := vault.Resolve(context.Background(), ref)
	if err != nil || string(resolved) != string(material) {
		t.Fatalf("Resolve = %q, %v", resolved, err)
	}
	wrongKey := sha256.Sum256([]byte("different deployment master key"))
	wrongVault, err := NewVault(s, wrongKey[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongVault.Resolve(context.Background(), ref); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("wrong key error = %v, want forbidden", err)
	}
}

func TestRedactJSONPreservesReferencesAndRedactsNestedSecrets(t *testing.T) {
	input := []byte(`{"credential_ref":{"secret_ref":"secret-1","alias":"group-token"},"token":"do-not-log","nested":[{"password":"also-private","value":"safe"}],"connection_id":"conn-1"}`)
	redacted, err := RedactJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(redacted, &value); err != nil {
		t.Fatal(err)
	}
	if value["token"] != "[REDACTED]" {
		t.Fatalf("token = %#v", value["token"])
	}
	if value["connection_id"] != "conn-1" {
		t.Fatalf("connection id was changed: %#v", value["connection_id"])
	}
	refValue := value["credential_ref"].(map[string]any)
	if refValue["secret_ref"] != "secret-1" || refValue["alias"] != "group-token" {
		t.Fatalf("reference was not preserved: %#v", refValue)
	}
	nested := value["nested"].([]any)[0].(map[string]any)
	if nested["password"] != "[REDACTED]" || nested["value"] != "safe" {
		t.Fatalf("nested redaction = %#v", nested)
	}
}
