package controlplane

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/security"
	"specwire/bridge/internal/store"
)

type fakeGroupProbe struct {
	results []domain.CapabilityResult
	err     error
	seen    [][]byte
}

func (f *fakeGroupProbe) ProbeGitLabGroup(_ context.Context, _ domain.GitLabInstance, _ domain.GitLabGroupBinding, material []byte) ([]domain.CapabilityResult, error) {
	f.seen = append(f.seen, append([]byte(nil), material...))
	return f.results, f.err
}

func (f *fakeGroupProbe) ProbeGitLabCredential(_ context.Context, _ domain.GitLabInstance, material []byte) ([]domain.CapabilityResult, error) {
	f.seen = append(f.seen, append([]byte(nil), material...))
	return f.results, f.err
}

func newCredentialServiceFixture(t *testing.T, probe *fakeGroupProbe) (*CredentialService, *store.Store, domain.GitLabInstance, domain.GitLabGroupBinding) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "credentials.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.CreateWorkspace(ctx, domain.Workspace{ID: "workspace-credentials", Slug: "credentials", Name: "Credentials", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	instance := domain.GitLabInstance{ID: "gitlab-credentials", WorkspaceID: "workspace-credentials", Name: "GitLab", BaseURL: "https://gitlab.example.test"}
	if err := s.CreateGitLabInstance(ctx, instance); err != nil {
		t.Fatal(err)
	}
	binding := domain.GitLabGroupBinding{ID: "group-binding", WorkspaceID: "workspace-credentials", GitLabInstanceID: instance.ID, ExternalGroupID: "group-42", FullPath: "platform", InheritSubgroups: true}
	if err := s.CreateGitLabGroupBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	key := [32]byte{}
	for i := range key {
		key[i] = byte(i + 1)
	}
	vault, err := security.NewVault(s, key[:])
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewCredentialService(s, vault, probe)
	if err != nil {
		t.Fatal(err)
	}
	return service, s, instance, binding
}

func TestGroupCredentialBindAndSafeRotation(t *testing.T) {
	probe := &fakeGroupProbe{results: []domain.CapabilityResult{{Capability: "gitlab.projects.read", Available: true}, {Capability: "gitlab.hooks.manage", Available: true}}}
	service, s, instance, binding := newCredentialServiceFixture(t, probe)
	ctx := context.Background()
	oldRef := domain.SecretRef{ID: "secret-old", WorkspaceID: binding.WorkspaceID, Alias: "group-token-v1", Kind: domain.SecretGroupCredential}
	profile, err := service.BindGroupCredential(ctx, instance, binding, "credential-profile", "group-token-v1", domain.CredentialGroupAccessToken, oldRef, []byte("old-secret-material"), []string{"gitlab.projects.read"})
	if err != nil {
		t.Fatalf("bind credential: %v", err)
	}
	if profile.SecretRef.ID != oldRef.ID || len(profile.Capabilities) != 2 {
		t.Fatalf("profile = %+v", profile)
	}
	saved, err := s.GetGitLabGroupBinding(ctx, binding.WorkspaceID, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.CredentialProfileID != profile.ID || saved.CredentialRef == nil || saved.CredentialRef.ID != oldRef.ID || !saved.InheritSubgroups {
		t.Fatalf("saved Group binding = %+v", saved)
	}
	newRef := domain.SecretRef{ID: "secret-new", WorkspaceID: binding.WorkspaceID, Alias: "group-token-v2", Kind: domain.SecretGroupCredential}
	rotated, err := service.RotateGroupCredential(ctx, instance, saved, newRef, []byte("new-secret-material"), []string{"gitlab.projects.read"})
	if err != nil {
		t.Fatalf("rotate credential: %v", err)
	}
	if rotated.SecretRef.ID != newRef.ID || rotated.Alias != newRef.Alias {
		t.Fatalf("rotated profile = %+v", rotated)
	}
	updated, err := s.GetGitLabGroupBinding(ctx, binding.WorkspaceID, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CredentialRef == nil || updated.CredentialRef.ID != newRef.ID {
		t.Fatalf("binding still points to old secret: %+v", updated)
	}
	if len(probe.seen) != 2 || string(probe.seen[0]) != "old-secret-material" || string(probe.seen[1]) != "new-secret-material" {
		t.Fatalf("probe materials = %q", probe.seen)
	}
	for _, material := range probe.seen {
		if string(material) == "" {
			t.Fatal("probe received cleared material")
		}
	}
}

func TestGroupCredentialCapabilityFailureDoesNotBind(t *testing.T) {
	probe := &fakeGroupProbe{results: []domain.CapabilityResult{{Capability: "gitlab.projects.read", Available: false, Reason: "403"}}}
	service, s, instance, binding := newCredentialServiceFixture(t, probe)
	ref := domain.SecretRef{ID: "secret-denied", WorkspaceID: binding.WorkspaceID, Alias: "denied", Kind: domain.SecretGroupCredential}
	_, err := service.BindGroupCredential(context.Background(), instance, binding, "profile-denied", "denied", domain.CredentialPAT, ref, []byte("private"), []string{"gitlab.projects.read"})
	if !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("capability error = %v", err)
	}
	saved, err := s.GetGitLabGroupBinding(context.Background(), binding.WorkspaceID, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.CredentialRef != nil || !saved.CredentialProfileID.Empty() {
		t.Fatalf("denied credential was bound: %+v", saved)
	}
}

func TestGroupCredentialTransientFailureIsRetryable(t *testing.T) {
	probe := &fakeGroupProbe{err: &ProviderTransientError{Err: errors.New("temporary GitLab outage")}}
	service, _, instance, binding := newCredentialServiceFixture(t, probe)
	ref := domain.SecretRef{ID: "secret-transient", WorkspaceID: binding.WorkspaceID, Alias: "transient", Kind: domain.SecretGroupCredential}
	_, err := service.BindGroupCredential(context.Background(), instance, binding, "profile-transient", "transient", domain.CredentialPAT, ref, []byte("private"), nil)
	if !errors.Is(err, ErrProviderTransient) {
		t.Fatalf("transient error = %v", err)
	}
}

func TestGitLabInstanceCredentialIsPersistedForInitialDiscovery(t *testing.T) {
	probe := &fakeGroupProbe{results: []domain.CapabilityResult{{Capability: "gitlab.groups.read", Available: true}}}
	service, s, instance, _ := newCredentialServiceFixture(t, probe)
	ctx := context.Background()
	ref := domain.SecretRef{ID: "secret-instance", WorkspaceID: instance.WorkspaceID, Alias: "instance-discovery", Kind: domain.SecretGroupCredential}
	attached, results, err := service.BindGitLabInstanceCredential(ctx, instance, ref.Alias, domain.CredentialPAT, ref, []byte("instance-secret"), []string{"gitlab.groups.read"})
	if err != nil {
		t.Fatalf("bind instance credential: %v", err)
	}
	if attached != ref || len(results) != 1 || len(probe.seen) != 1 || string(probe.seen[0]) != "instance-secret" {
		t.Fatalf("instance credential result = %+v, results=%+v, probe=%q", attached, results, probe.seen)
	}
	loaded, err := s.GetGitLabInstance(ctx, instance.WorkspaceID, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CredentialRef == nil || *loaded.CredentialRef != ref {
		t.Fatalf("persisted instance credential = %+v", loaded.CredentialRef)
	}
	material, err := service.vault.Resolve(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if string(material) != "instance-secret" {
		t.Fatalf("resolved material = %q", material)
	}
	clear(material)
}

func TestGitLabInstanceCredentialCapabilityFailureDoesNotAttach(t *testing.T) {
	probe := &fakeGroupProbe{results: []domain.CapabilityResult{{Capability: "gitlab.groups.read", Available: false, Reason: "forbidden"}}}
	service, s, instance, _ := newCredentialServiceFixture(t, probe)
	ref := domain.SecretRef{ID: "secret-instance-denied", WorkspaceID: instance.WorkspaceID, Alias: "instance-denied", Kind: domain.SecretGroupCredential}
	if _, _, err := service.BindGitLabInstanceCredential(context.Background(), instance, ref.Alias, domain.CredentialPAT, ref, []byte("instance-secret"), []string{"gitlab.groups.read"}); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("capability error = %v", err)
	}
	loaded, err := s.GetGitLabInstance(context.Background(), instance.WorkspaceID, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CredentialRef != nil {
		t.Fatalf("denied instance credential was attached: %+v", loaded.CredentialRef)
	}
}
