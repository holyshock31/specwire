package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/registry"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "specwire.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func testWorkspace(t *testing.T, s *Store, id domain.ID) domain.Workspace {
	t.Helper()
	w := domain.Workspace{ID: id, Slug: string(id), Name: "Workspace " + string(id), Status: domain.WorkspaceActive}
	if err := s.CreateWorkspace(context.Background(), w); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	return w
}

func testEndpoints(t *testing.T, s *Store, workspaceID domain.ID, gitlabID, multicaID domain.ID, externalSuffix string) {
	t.Helper()
	if err := s.CreateGitLabInstance(context.Background(), domain.GitLabInstance{
		ID: gitlabID, WorkspaceID: workspaceID, Name: "GitLab " + externalSuffix,
		BaseURL: "https://gitlab.example.test", ExternalID: "gitlab-" + externalSuffix,
	}); err != nil {
		t.Fatalf("CreateGitLabInstance: %v", err)
	}
	if err := s.CreateMulticaInstance(context.Background(), domain.MulticaInstance{
		ID: multicaID, WorkspaceID: workspaceID, Name: "Multica " + externalSuffix,
		BaseURL: "https://multica.example.test", ExternalID: "multica-" + externalSuffix,
	}); err != nil {
		t.Fatalf("CreateMulticaInstance: %v", err)
	}
}

func testConnection(workspaceID, id, gitlabID, multicaID domain.ID, source, target string) domain.Connection {
	return domain.Connection{
		ID: id, WorkspaceID: workspaceID, Name: string(id),
		SourceGitLabProject:  domain.ProviderProjectRef{InstanceID: gitlabID, ExternalID: source, FullPath: "group/" + source},
		TargetMulticaProject: domain.ProviderProjectRef{InstanceID: multicaID, ExternalID: target, Name: target},
		Status:               domain.ConnectionConfigured,
	}
}

func TestMigrationsAreVersionedAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "specwire.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	var migrations int
	if err := first.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if migrations != 16 {
		t.Fatalf("migrations = %d, want 16", migrations)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()
	if err := second.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if migrations != 16 {
		t.Fatalf("migrations after reopen = %d, want 16", migrations)
	}
	var legacyConnectorTables int
	if err := second.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'connector_instances'`).Scan(&legacyConnectorTables); err != nil {
		t.Fatal(err)
	}
	if legacyConnectorTables != 0 {
		t.Fatal("Flow-level connector_instances table must not be part of the new schema")
	}
}

func TestMulticaProjectURLSnapshotRoundTrips(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace := testWorkspace(t, s, "workspace-multica-url")
	testEndpoints(t, s, workspace.ID, "gitlab-multica-url", "multica-url", "url")
	multicaWorkspace, err := s.UpsertMulticaWorkspace(ctx, domain.MulticaWorkspaceRef{
		ID: "multica-workspace-url", WorkspaceID: workspace.ID, InstanceID: "multica-url", ExternalID: "team-core", Name: "Team Core",
	})
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.UpsertMulticaProject(ctx, domain.MulticaProjectRef{
		ID: "multica-project-url", WorkspaceID: workspace.ID, InstanceID: "multica-url", MulticaWorkspaceID: multicaWorkspace.ID,
		ExternalID: "project-webdeck", Title: "platform/webdeck", WebURL: "https://multica.example/projects/project-webdeck",
	})
	if err != nil {
		t.Fatal(err)
	}
	if project.WebURL != "https://multica.example/projects/project-webdeck" {
		t.Fatalf("upserted project URL = %q", project.WebURL)
	}
	listed, err := s.ListMulticaProjects(ctx, workspace.ID, "multica-url", multicaWorkspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].WebURL != project.WebURL {
		t.Fatalf("listed project snapshot = %+v", listed)
	}
}

func TestActivateHookRouteRetiresPreviousFlowVersion(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace := testWorkspace(t, s, "workspace-route-version")
	testEndpoints(t, s, workspace.ID, "gitlab-route-version", "multica-route-version", "route")
	connection := testConnection(workspace.ID, "connection-route-version", "gitlab-route-version", "multica-route-version", "source-route", "target-route")
	if err := s.CreateConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertHook(ctx, domain.Hook{
		ID: "hook-route-version", WorkspaceID: workspace.ID, ConnectionID: connection.ID, Provider: domain.ProviderGitLab,
		InstanceID: "gitlab-route-version", SourceProjectExternalID: "source-route", ExternalID: "provider-hook-route-version", Status: domain.HookActive,
	}); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 2; version++ {
		if _, err := s.ActivateHookRoute(ctx, domain.HookRoute{
			ID: domain.ID(fmt.Sprintf("route-version-%d", version)), WorkspaceID: workspace.ID, ConnectionID: connection.ID,
			SourceProject: connection.SourceGitLabProject, BehaviorKey: "gitlab.issue-hook", BehaviorVersion: "1.0.0",
			FlowID: "flow-route-version", FlowVersion: version, HookRef: "hook-route-version", Status: domain.HookActive,
		}); err != nil {
			t.Fatalf("activate route v%d: %v", version, err)
		}
	}
	routes, err := s.ListHookRoutes(ctx, workspace.ID, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes))
	}
	for _, route := range routes {
		want := domain.HookDisabled
		if route.FlowVersion == 2 {
			want = domain.HookActive
		}
		if route.Status != want {
			t.Fatalf("route v%d status = %q, want %q", route.FlowVersion, route.Status, want)
		}
	}
}

func TestRegistryBootstrapIsIdempotentAndImmutable(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	testWorkspace(t, s, "workspace-registry")
	bundle, err := registry.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BootstrapRegistry(ctx, "workspace-registry", bundle); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := s.BootstrapRegistry(ctx, "workspace-registry", bundle); err != nil {
		t.Fatalf("repeat bootstrap: %v", err)
	}
	counts, err := s.RegistryCounts(ctx, "workspace-registry")
	if err != nil {
		t.Fatal(err)
	}
	if counts.ConnectorTypes != 2 || counts.Behaviors != 5 || counts.DataModels != 5 {
		t.Fatalf("registry counts = %+v", counts)
	}
	bundle.DataModels[0].DisplayName = "changed after publication"
	if err := s.BootstrapRegistry(ctx, "workspace-registry", bundle); !errors.Is(err, domain.ErrImmutable) {
		t.Fatalf("changed builtin bootstrap error = %v, want ErrImmutable", err)
	}
}

func TestWorkspaceScopedConnectionLookupAndActiveConflicts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	testWorkspace(t, s, "workspace-one")
	testWorkspace(t, s, "workspace-two")
	testEndpoints(t, s, "workspace-one", "gitlab-one", "multica-one", "one")
	testEndpoints(t, s, "workspace-two", "gitlab-two", "multica-two", "two")
	c1 := testConnection("workspace-one", "connection-one", "gitlab-one", "multica-one", "project-1", "target-1")
	if err := s.CreateConnection(ctx, c1); err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	if _, err := s.GetConnection(ctx, "workspace-two", c1.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-workspace lookup = %v, want ErrNotFound", err)
	}
	if err := s.CreateConnection(ctx, testConnection("workspace-one", "connection-two", "gitlab-one", "multica-one", "project-1", "target-2")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("source conflict = %v, want ErrConflict", err)
	}
	if err := s.CreateConnection(ctx, testConnection("workspace-one", "connection-three", "gitlab-one", "multica-one", "project-2", "target-1")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("target conflict = %v, want ErrConflict", err)
	}
	if err := s.DisableConnection(ctx, "workspace-one", c1.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateConnection(ctx, testConnection("workspace-one", "connection-two", "gitlab-one", "multica-one", "project-1", "target-2")); err != nil {
		t.Fatalf("disabled connection should release active source: %v", err)
	}
}

func TestConnectionStatsAggregatesWorkspaceScopedRecords(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace := testWorkspace(t, s, "workspace-connection-stats")
	testEndpoints(t, s, workspace.ID, "gitlab-connection-stats", "multica-connection-stats", "stats")
	connection := testConnection(workspace.ID, "connection-stats", "gitlab-connection-stats", "multica-connection-stats", "source-stats", "target-stats")
	if err := s.CreateConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}

	stats, err := s.GetConnectionStats(ctx, workspace.ID, connection.ID)
	if err != nil {
		t.Fatalf("empty connection stats: %v", err)
	}
	if stats.FlowCount != 0 || stats.ResourceCount != 0 || stats.ExecutionCount != 0 {
		t.Fatalf("empty connection stats = %+v", stats)
	}

	flow := domain.Flow{ID: "flow-connection-stats", WorkspaceID: workspace.ID, ConnectionID: connection.ID, Name: "Stats Flow", Status: domain.FlowPublished, ActiveVersion: 1}
	if err := s.CreateFlow(ctx, flow); err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	version := domain.FlowVersion{ID: "flow-version-connection-stats", WorkspaceID: workspace.ID, FlowID: flow.ID, Version: 1, Status: domain.FlowPublished, Graph: testGraph()}
	if err := s.SaveFlowVersion(ctx, version); err != nil {
		t.Fatalf("SaveFlowVersion: %v", err)
	}
	if _, err := s.EnsureManagedResource(ctx, domain.ManagedResource{
		ID: "resource-connection-stats", WorkspaceID: workspace.ID, ConnectionID: connection.ID,
		Kind: domain.ResourceProject, Provider: domain.ProviderGitLab, InstanceID: "gitlab-connection-stats",
		ExternalID: "source-stats", Ownership: domain.OwnershipManaged, Status: "ready",
	}); err != nil {
		t.Fatalf("EnsureManagedResource: %v", err)
	}
	if _, err := s.EnsureManagedResource(ctx, domain.ManagedResource{
		ID: "resource-connection-stats-target", WorkspaceID: workspace.ID, ConnectionID: connection.ID,
		Kind: domain.ResourceProject, Provider: domain.ProviderMultica, InstanceID: "multica-connection-stats",
		ExternalID: "target-stats", Ownership: domain.OwnershipAdopted, Status: "ready",
	}); err != nil {
		t.Fatalf("EnsureManagedResource target: %v", err)
	}
	if err := s.CreateFlowExecution(ctx, domain.FlowExecution{
		ID: "execution-connection-stats", WorkspaceID: workspace.ID, ConnectionID: connection.ID,
		FlowID: flow.ID, FlowVersionID: version.ID, FlowVersion: 1, EventID: "event-connection-stats",
		IdempotencyKey: "idempotency-connection-stats", CorrelationID: "correlation-connection-stats",
		Status: domain.ExecutionSucceeded,
	}); err != nil {
		t.Fatalf("CreateFlowExecution: %v", err)
	}
	failureTime := time.Now().UTC().Add(time.Second)
	if err := s.CreateFlowExecution(ctx, domain.FlowExecution{
		ID: "execution-connection-stats-failed", WorkspaceID: workspace.ID, ConnectionID: connection.ID,
		FlowID: flow.ID, FlowVersionID: version.ID, FlowVersion: 1, EventID: "event-connection-stats-failed",
		IdempotencyKey: "idempotency-connection-stats-failed", CorrelationID: "correlation-connection-stats-failed",
		Status: domain.ExecutionFailed, ErrorCategory: "provider", ErrorMessage: "temporary provider failure",
		CreatedAt: failureTime, UpdatedAt: failureTime,
	}); err != nil {
		t.Fatalf("Create failed FlowExecution: %v", err)
	}

	stats, err = s.GetConnectionStats(ctx, workspace.ID, connection.ID)
	if err != nil {
		t.Fatalf("populated connection stats: %v", err)
	}
	if stats.FlowCount != 1 || stats.ResourceCount != 2 || stats.ExecutionCount != 2 || stats.SuccessfulExecutionCount != 1 || stats.UnacknowledgedExecutionCount != 1 {
		t.Fatalf("populated connection stats = %+v", stats)
	}
	if stats.LatestExecutionStatus != domain.ExecutionFailed || stats.LatestExecutionAttentionStatus != domain.ExecutionAttentionOpen || stats.LatestExecutionAt == nil {
		t.Fatalf("latest execution stats = %+v", stats)
	}
	failed, err := s.GetFlowExecution(ctx, workspace.ID, "execution-connection-stats-failed")
	if err != nil {
		t.Fatalf("get failed execution: %v", err)
	}
	acknowledgedAt := time.Now().UTC()
	failed.AttentionStatus = domain.ExecutionAttentionAcknowledged
	failed.AttentionActorAccountID = "account-connection-stats"
	failed.AttentionUpdatedAt = &acknowledgedAt
	if err := s.UpdateFlowExecution(ctx, failed); err != nil {
		t.Fatalf("acknowledge failed execution: %v", err)
	}
	stats, err = s.GetConnectionStats(ctx, workspace.ID, connection.ID)
	if err != nil {
		t.Fatalf("connection stats after acknowledge: %v", err)
	}
	if stats.UnacknowledgedExecutionCount != 0 || stats.LatestExecutionStatus != domain.ExecutionFailed || stats.LatestExecutionAttentionStatus != domain.ExecutionAttentionAcknowledged {
		t.Fatalf("connection stats after acknowledge = %+v", stats)
	}
}

func TestProviderExternalIDsAreWorkspaceAndInstanceScoped(t *testing.T) {
	s := openTestStore(t)
	testWorkspace(t, s, "workspace-one")
	if err := s.CreateGitLabInstance(context.Background(), domain.GitLabInstance{ID: "gitlab-one", WorkspaceID: "workspace-one", Name: "one", BaseURL: "https://gitlab.example.test", ExternalID: "same-external"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGitLabInstance(context.Background(), domain.GitLabInstance{ID: "gitlab-two", WorkspaceID: "workspace-one", Name: "two", BaseURL: "https://gitlab.example.test", ExternalID: "same-external"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate provider external id = %v, want conflict", err)
	}
	testWorkspace(t, s, "workspace-two")
	if err := s.CreateGitLabInstance(context.Background(), domain.GitLabInstance{ID: "gitlab-three", WorkspaceID: "workspace-two", Name: "three", BaseURL: "https://gitlab.example.test", ExternalID: "same-external"}); err != nil {
		t.Fatalf("same external id in another workspace should be allowed: %v", err)
	}
}

func testGraph() domain.FlowGraph {
	return domain.FlowGraph{
		Nodes: []domain.FlowNode{
			{ID: "input-node", Kind: domain.NodeConnector, Name: "GitLab Issue", Connector: &domain.ConnectorNode{BehaviorKey: "gitlab.issue-hook", BehaviorVersion: "1.0.0"}, Outputs: []domain.Port{{ID: "input-out", Name: "event", Direction: domain.PortOutput, ModelRef: "provider:gitlab.issue.v1"}}},
			{ID: "normalize-node", Kind: domain.NodeGeneric, Name: "Normalize", Generic: &domain.GenericNode{Type: "parse-normalize", ParameterBindings: map[string]domain.ParameterBinding{"model": {Kind: domain.BindingFixed, Value: "ChangePublication.v1"}}}, Inputs: []domain.Port{{ID: "normalize-in", Direction: domain.PortInput}}, Outputs: []domain.Port{{ID: "normalize-out", Direction: domain.PortOutput, ModelRef: "ChangePublication.v1"}}},
		},
		Edges: []domain.FlowEdge{{ID: "edge-1", FromNodeID: "input-node", FromPortID: "input-out", ToNodeID: "normalize-node", ToPortID: "normalize-in"}},
	}
}

func TestFlowVersionIsImmutableAndRoundTripsGraph(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	testWorkspace(t, s, "workspace-flow")
	testEndpoints(t, s, "workspace-flow", "gitlab-flow", "multica-flow", "flow")
	connection := testConnection("workspace-flow", "connection-flow", "gitlab-flow", "multica-flow", "source", "target")
	if err := s.CreateConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	flow := domain.Flow{ID: "flow-one", WorkspaceID: "workspace-flow", ConnectionID: connection.ID, Name: "Publish", Status: domain.FlowDraft}
	if err := s.CreateFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	version := domain.FlowVersion{ID: "flow-version-one", WorkspaceID: flow.WorkspaceID, FlowID: flow.ID, Version: 1, Status: domain.FlowDraft, Graph: testGraph(), CompiledPlan: map[string]any{"plan_version": "1", "nodes": 2}, BehaviorRefs: []string{"gitlab.issue-hook@1.0.0"}, ModelRefs: []string{"ChangePublication.v1"}}
	if err := s.SaveFlowVersion(ctx, version); err != nil {
		t.Fatalf("save version: %v", err)
	}
	if err := s.SaveFlowVersion(ctx, version); err != nil {
		t.Fatalf("idempotent save version: %v", err)
	}
	reloaded, err := s.GetFlowVersion(ctx, flow.WorkspaceID, flow.ID, 1)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if len(reloaded.Graph.Nodes) != 2 || reloaded.Graph.Edges[0].FromNodeID != "input-node" || reloaded.CompiledPlan["plan_version"] != "1" {
		t.Fatalf("reloaded version lost graph/plan: %+v", reloaded)
	}
	version.BehaviorRefs = []string{"different@2.0.0"}
	if err := s.SaveFlowVersion(ctx, version); !errors.Is(err, domain.ErrImmutable) {
		t.Fatalf("changed version error = %v, want ErrImmutable", err)
	}
}

func TestFlowGraphPersistsConnectorNodeWithoutConnectorInstance(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	testWorkspace(t, s, "workspace-terminology")
	testEndpoints(t, s, "workspace-terminology", "gitlab-terminology", "multica-terminology", "terminology")
	connection := testConnection("workspace-terminology", "connection-terminology", "gitlab-terminology", "multica-terminology", "source", "target")
	if err := s.CreateConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	flow := domain.Flow{ID: "flow-terminology", WorkspaceID: connection.WorkspaceID, ConnectionID: connection.ID, Name: "Terminology", Status: domain.FlowDraft}
	if err := s.CreateFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	graph := domain.FlowGraph{Nodes: []domain.FlowNode{{ID: "connector-node", Kind: domain.NodeConnector, Connector: &domain.ConnectorNode{
		BehaviorKey: "gitlab.push-hook", BehaviorVersion: "1.0.0", ParameterBindings: map[string]domain.ParameterBinding{
			"project": {Kind: domain.BindingConnectionRef, Ref: "$connection.source_project"},
		},
	}}}}
	if err := s.SaveFlowVersion(ctx, domain.FlowVersion{ID: "flow-terminology-v1", WorkspaceID: flow.WorkspaceID, FlowID: flow.ID, Version: 1, Status: domain.FlowDraft, Graph: graph}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.GetFlowVersion(ctx, flow.WorkspaceID, flow.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Graph.Nodes[0].Connector == nil || reloaded.Graph.Nodes[0].Connector.BehaviorKey != "gitlab.push-hook" {
		t.Fatalf("connector node was not preserved: %+v", reloaded.Graph.Nodes[0])
	}
	encoded, err := json.Marshal(reloaded.Graph)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "ConnectorInstance") || strings.Contains(string(encoded), "connector_instance") {
		t.Fatalf("legacy ConnectorInstance terminology leaked into graph: %s", encoded)
	}
}

func TestConcurrentIdempotencyClaimHasSingleWinner(t *testing.T) {
	s := openTestStore(t)
	testWorkspace(t, s, "workspace-idempotency")
	ctx := context.Background()
	const workers = 24
	results := make(chan bool, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := s.ClaimIdempotency(ctx, "workspace-idempotency", "flow-execution", "delivery-1", nil)
			results <- claimed
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	winners := 0
	for claimed := range results {
		if claimed {
			winners++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent claim: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("idempotency winners = %d, want 1", winners)
	}
	has, err := s.HasIdempotencyKey(ctx, "workspace-idempotency", "flow-execution", "delivery-1")
	if err != nil || !has {
		t.Fatalf("stored idempotency key = %v, %v", has, err)
	}
}

func TestOpenMemoryStore(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	testWorkspace(t, s, "memory")
	if _, err := s.GetWorkspace(context.Background(), "memory"); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339Nano, nowText()); err != nil {
		t.Fatal(err)
	}
}

func TestConstraintErrorDoesNotHideInvalidInput(t *testing.T) {
	s := openTestStore(t)
	err := s.CreateWorkspace(context.Background(), domain.Workspace{ID: "", Slug: "", Name: ""})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid workspace error = %v", err)
	}
	if fmt.Sprint(err) == "" {
		t.Fatal("error should be descriptive")
	}
}
