package flow

import (
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/registry"
)

func testSimulationCatalog(t *testing.T) Catalog {
	t.Helper()
	bundle, err := registry.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	return NewCatalog(bundle.Behaviors, bundle.DataModels, []string{
		"gitlab.events.issue", "gitlab.events.push", "multica.issue.create", "multica.issue.status",
	})
}

func TestSimulatePublicationSuppressesExternalConnector(t *testing.T) {
	catalog := testSimulationCatalog(t)
	result := Simulate(publicationGraph(), catalog, map[string]any{
		"object_kind": "issue",
		"object_attributes": map[string]any{
			"action":      "open",
			"iid":         7,
			"description": "change_id: CHG-7\nbranch: change/7\nbranch_head_sha: abc123\n",
			"labels":      []any{map[string]any{"title": "change"}},
		},
		"project": map[string]any{"path_with_namespace": "platform/service"},
	}, RuntimeContext{SourceProject: "platform/service", TargetProject: "target-1", TargetRef: "refs/heads/main"})

	if !result.Valid || !result.ExternalActionsSuppressed {
		t.Fatalf("simulation = %+v", result)
	}
	if len(result.Nodes) != 4 {
		t.Fatalf("nodes = %+v, want four trace entries", result.Nodes)
	}
	last := result.Nodes[len(result.Nodes)-1]
	if last.Status != "suppressed" || !last.SideEffectSuppressed {
		t.Fatalf("output trace = %+v", last)
	}
	if last.Input["change_id"] != "CHG-7" {
		t.Fatalf("output input = %+v", last.Input)
	}
}

func TestSimulateRejectsInvalidDraftBeforeExternalAction(t *testing.T) {
	catalog := testSimulationCatalog(t)
	graph := domain.FlowGraph{Nodes: []domain.FlowNode{{ID: "input", Kind: domain.NodeConnector, Connector: &domain.ConnectorNode{BehaviorKey: "gitlab.issue-hook", BehaviorVersion: "1.0.0"}}}}
	result := Simulate(graph, catalog, map[string]any{"object_kind": "issue"}, RuntimeContext{})
	if result.Valid || len(result.Diagnostics) == 0 {
		t.Fatalf("invalid simulation = %+v", result)
	}
	if len(result.Nodes) != 0 {
		t.Fatalf("invalid draft executed nodes: %+v", result.Nodes)
	}
}
