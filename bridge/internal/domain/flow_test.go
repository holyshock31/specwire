package domain

import (
	"strings"
	"testing"
)

func TestNewIDIsUUIDv4(t *testing.T) {
	id := NewID()
	parts := strings.Split(string(id), "-")
	if len(parts) != 5 {
		t.Fatalf("id %q is not UUID-shaped", id)
	}
	if len(parts[2]) != 4 || parts[2][0] != '4' {
		t.Fatalf("id %q is not UUID v4", id)
	}
	if id == NewID() {
		t.Fatal("two generated IDs collided")
	}
}

func TestFlowGraphValidateShapeAllowsDraftButRejectsInvalidReferences(t *testing.T) {
	graph := FlowGraph{Nodes: []FlowNode{
		{ID: "input", Kind: NodeConnector, Connector: &ConnectorNode{BehaviorKey: "gitlab.issue-hook", BehaviorVersion: "1.0.0"}},
		{ID: "normalize", Kind: NodeGeneric, Generic: &GenericNode{Type: "parse-normalize"}},
	}, Edges: []FlowEdge{{ID: "edge-1", FromNodeID: "input", FromPortID: "out", ToNodeID: "normalize", ToPortID: "in"}}}
	if err := graph.ValidateShape(); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
	graph.Edges[0].ToNodeID = "missing"
	if err := graph.ValidateShape(); err == nil {
		t.Fatal("graph with missing edge target accepted")
	}
}
