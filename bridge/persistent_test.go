package main

import (
	"context"
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/registry"
	securitynew "specwire/bridge/internal/security"
	foundationstore "specwire/bridge/internal/store"
)

func TestLegacyGitLabCredentialIsImportedOnlyByExplicitMigration(t *testing.T) {
	ctx := context.Background()
	store, err := foundationstore.Open(t.TempDir() + "/legacy-import.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	vault, err := securitynew.NewVault(store, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := registry.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	catalog := flow.NewCatalog(bundle.Behaviors, bundle.DataModels, []string{
		"gitlab.events.issue", "gitlab.events.push", "multica.issue.create", "multica.issue.status",
	})
	flows, err := flow.NewService(store, catalog)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		GitLabToken:     "legacy-token",
		GitLabURL:       "http://gitlab.example",
		AllowedProjects: map[string]bool{},
	}
	if err := importLegacyConfiguration(ctx, cfg, store, vault, nil, nil, flows, nil, bundle); err != nil {
		t.Fatalf("explicit legacy import: %v", err)
	}
	workspace, err := store.GetWorkspaceBySlug(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.GetGitLabInstance(ctx, workspace.ID, "gitlab-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if instance.CredentialRef == nil {
		t.Fatal("explicit legacy import did not persist a credential reference")
	}
	material, err := vault.Resolve(ctx, *instance.CredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	if string(material) != "legacy-token" {
		t.Fatalf("imported credential = %q, want legacy-token", material)
	}
	clearBytesLocal(material)

}

func TestLegacyGitLabImportAttachesCredentialToExistingEndpoint(t *testing.T) {
	ctx := context.Background()
	store, err := foundationstore.Open(t.TempDir() + "/existing-endpoint.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	vault, err := securitynew.NewVault(store, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{ID: domain.ID("workspace-existing"), Slug: "default", Name: "Default Workspace", Status: domain.WorkspaceActive}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateGitLabInstance(ctx, domain.GitLabInstance{
		ID: "gitlab-legacy", WorkspaceID: workspace.ID, Name: "GitLab (legacy)", BaseURL: "http://gitlab.example", ExternalID: "legacy", Status: domain.EndpointActive,
	}); err != nil {
		t.Fatal(err)
	}
	ref, err := ensureLegacyGitLabInstance(ctx, &Config{GitLabToken: "legacy-token", GitLabURL: "http://gitlab.example"}, store, vault, workspace.ID)
	if err != nil {
		t.Fatalf("ensure existing GitLab instance: %v", err)
	}
	if ref == nil {
		t.Fatal("existing endpoint did not receive imported credential")
	}
	loaded, err := store.GetGitLabInstance(ctx, workspace.ID, "gitlab-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CredentialRef == nil || loaded.CredentialRef.ID != ref.ID {
		t.Fatalf("credential ref = %+v, want %s", loaded.CredentialRef, ref.ID)
	}
	material, err := vault.Resolve(ctx, *loaded.CredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	if string(material) != "legacy-token" {
		t.Fatalf("imported credential = %q, want legacy-token", material)
	}
	clearBytesLocal(material)
}
