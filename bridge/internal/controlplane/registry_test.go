package controlplane

import (
	"context"
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/registry"
	"specwire/bridge/internal/store"
)

func TestRegistryCatalogIsWorkspaceScoped(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, id := range []domain.ID{"workspace-one", "workspace-two"} {
		if err := db.CreateWorkspace(ctx, domain.Workspace{ID: id, Slug: string(id), Name: string(id), Status: domain.WorkspaceActive}); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := registry.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.BootstrapRegistry(ctx, "workspace-one", bundle); err != nil {
		t.Fatal(err)
	}
	if err := db.BootstrapRegistry(ctx, "workspace-two", bundle); err != nil {
		t.Fatal(err)
	}
	service, err := NewRegistryService(db, registry.AdapterAllowlist{
		"gitlab.events.issue": true, "gitlab.events.push": true,
		"multica.issue.create": true, "multica.issue.status": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	custom := domain.DataModelDefinition{
		ID:  "custom-model",
		Key: "CustomEvent", Version: "v1", DisplayName: "Custom Event", Status: domain.DefinitionPublished,
		Schema:         map[string]any{"type": "object", "properties": map[string]any{"event_id": map[string]any{"type": "string"}}},
		RequiredFields: []string{"event_id"},
	}
	if err := service.RegisterDataModel(ctx, "workspace-one", custom); err != nil {
		t.Fatal(err)
	}
	one, err := service.CatalogForWorkspace(ctx, "workspace-one")
	if err != nil {
		t.Fatal(err)
	}
	two, err := service.CatalogForWorkspace(ctx, "workspace-two")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := one.Model("CustomEvent.v1"); !ok {
		t.Fatal("workspace-one catalog does not contain its registered model")
	}
	if _, ok := two.Model("CustomEvent.v1"); ok {
		t.Fatal("workspace-two catalog leaked workspace-one model")
	}
}
