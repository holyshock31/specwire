package flow

import (
	"context"
	"errors"
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/registry"
	"specwire/bridge/internal/store"
)

func openFlowService(t *testing.T) (*store.Store, *Service, domain.ID) {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/flow.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	workspaceID := domain.ID("workspace-flow-service")
	if err := db.CreateWorkspace(ctx, domain.Workspace{ID: workspaceID, Slug: "flow-service", Name: "Flow Service", Status: domain.WorkspaceActive}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateGitLabInstance(ctx, domain.GitLabInstance{ID: "gitlab-flow-service", WorkspaceID: workspaceID, Name: "GitLab", BaseURL: "https://gitlab.example.test", ExternalID: "gitlab"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateMulticaInstance(ctx, domain.MulticaInstance{ID: "multica-flow-service", WorkspaceID: workspaceID, Name: "Multica", BaseURL: "https://multica.example.test", ExternalID: "multica"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateConnection(ctx, domain.Connection{
		ID: "connection-flow-service", WorkspaceID: workspaceID, Name: "platform/service",
		SourceGitLabProject:  domain.ProviderProjectRef{InstanceID: "gitlab-flow-service", ExternalID: "platform/service", FullPath: "platform/service"},
		TargetMulticaProject: domain.ProviderProjectRef{InstanceID: "multica-flow-service", ExternalID: "project-1", Name: "Service"},
		Status:               domain.ConnectionReady,
	}); err != nil {
		t.Fatal(err)
	}
	bundle, err := registry.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.BootstrapRegistry(ctx, workspaceID, bundle); err != nil {
		t.Fatal(err)
	}
	catalog := NewCatalog(bundle.Behaviors, bundle.DataModels, []string{
		"gitlab.events.issue", "gitlab.events.push", "multica.issue.create", "multica.issue.status",
	})
	service, err := NewService(db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SeedBuiltins(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	return db, service, workspaceID
}

func TestFlowServiceTemplateDraftPublishAndImmutableHistory(t *testing.T) {
	db, service, workspaceID := openFlowService(t)
	ctx := context.Background()

	flow, err := service.CreateFromTemplate(ctx, workspaceID, "connection-flow-service", "", TemplatePublishChange, "1.0.0", "")
	if err != nil {
		t.Fatalf("create from template: %v", err)
	}
	draft, err := db.GetFlowDraft(ctx, workspaceID, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Nodes) != 4 || draft.Nodes[0].Connector == nil {
		t.Fatalf("template draft was not cloned: %+v", draft)
	}

	version, validation, err := service.Publish(ctx, workspaceID, flow.ID, "")
	if err != nil || !validation.Valid {
		t.Fatalf("publish template: version=%+v validation=%+v err=%v", version, validation, err)
	}
	if version.Version != 1 || version.Status != domain.FlowPublished || len(version.BehaviorRefs) != 2 {
		t.Fatalf("published version = %+v", version)
	}

	updated := cloneGraph(draft)
	updated.Nodes[1].Name = "Normalize publication"
	if _, err := service.SaveDraft(ctx, workspaceID, flow.ID, updated); err != nil {
		t.Fatal(err)
	}
	version2, validation, err := service.Publish(ctx, workspaceID, flow.ID, "")
	if err != nil || !validation.Valid {
		t.Fatalf("publish second version: version=%+v validation=%+v err=%v", version2, validation, err)
	}
	if version2.Version != 2 {
		t.Fatalf("second version number = %d, want 2", version2.Version)
	}
	old, err := db.GetFlowVersion(ctx, workspaceID, flow.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != domain.FlowArchived || old.Graph.Nodes[1].Name == version2.Graph.Nodes[1].Name {
		t.Fatalf("old version was changed instead of archived: %+v", old)
	}
	version1Mutation := version
	version1Mutation.Graph.Nodes[0].Name = "mutated history"
	if err := db.SaveFlowVersion(ctx, version1Mutation); !errors.Is(err, domain.ErrImmutable) {
		t.Fatalf("mutating published version should be immutable: %v", err)
	}
}

func TestFlowServiceSavesInvalidDraftButBlocksPublish(t *testing.T) {
	_, service, workspaceID := openFlowService(t)
	ctx := context.Background()
	created, err := service.CreateBlank(ctx, workspaceID, "connection-flow-service", "", "Empty")
	if err != nil {
		t.Fatal(err)
	}
	validation, err := service.ValidateDraft(ctx, workspaceID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if validation.Valid || !hasDiagnostic(validation, "input_count") {
		t.Fatalf("empty draft validation = %+v", validation)
	}
	if _, validation, err := service.Publish(ctx, workspaceID, created.ID, ""); err == nil || validation.Valid {
		t.Fatalf("invalid draft published: validation=%+v err=%v", validation, err)
	}
}

func TestFlowServiceTemplateInstantiationIsIndependent(t *testing.T) {
	db, service, workspaceID := openFlowService(t)
	ctx := context.Background()
	first, err := service.CreateFromTemplate(ctx, workspaceID, "connection-flow-service", "", TemplatePublishChange, "1.0.0", "First")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateFromTemplate(ctx, workspaceID, "connection-flow-service", "", TemplatePublishChange, "1.0.0", "Second")
	if err != nil {
		t.Fatal(err)
	}
	firstGraph, err := db.GetFlowDraft(ctx, workspaceID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstGraph.Nodes[0].Name = "Changed only in First"
	if _, err := service.SaveDraft(ctx, workspaceID, first.ID, firstGraph); err != nil {
		t.Fatal(err)
	}
	secondGraph, err := db.GetFlowDraft(ctx, workspaceID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondGraph.Nodes[0].Name == "Changed only in First" {
		t.Fatal("template instances share mutable graph state")
	}
}
