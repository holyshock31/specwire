package domain

import (
	"fmt"
	"strings"
)

type NodeKind string

const (
	NodeConnector NodeKind = "connector"
	NodeGeneric   NodeKind = "generic"
	NodeTerminal  NodeKind = "terminal"
)

type PortDirection string

const (
	PortInput  PortDirection = "input"
	PortOutput PortDirection = "output"
)

type Port struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Direction     PortDirection `json:"direction"`
	ModelRef      string        `json:"model_ref,omitempty"`
	SemanticRoles []string      `json:"semantic_roles,omitempty"`
	Required      bool          `json:"required"`
}

type ParameterBindingKind string

const (
	BindingFixed         ParameterBindingKind = "fixed"
	BindingConnectionRef ParameterBindingKind = "connection_ref"
	BindingResourceRef   ParameterBindingKind = "resource_ref"
	BindingSecretRef     ParameterBindingKind = "secret_ref"
	BindingRuntimeRef    ParameterBindingKind = "runtime_ref"
)

type ParameterBinding struct {
	Kind  ParameterBindingKind `json:"kind"`
	Value any                  `json:"value,omitempty"`
	Ref   string               `json:"ref,omitempty"`
}

type ConnectorNode struct {
	BehaviorKey       string                      `json:"behavior_key"`
	BehaviorVersion   string                      `json:"behavior_version"`
	ParameterBindings map[string]ParameterBinding `json:"parameter_bindings,omitempty"`
}

type GenericNode struct {
	Type              string                      `json:"type"`
	ParameterBindings map[string]ParameterBinding `json:"parameter_bindings,omitempty"`
}

type NodePosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type FlowNode struct {
	ID        ID             `json:"id"`
	Kind      NodeKind       `json:"kind"`
	Name      string         `json:"name,omitempty"`
	Position  NodePosition   `json:"position"`
	Inputs    []Port         `json:"inputs,omitempty"`
	Outputs   []Port         `json:"outputs,omitempty"`
	Connector *ConnectorNode `json:"connector,omitempty"`
	Generic   *GenericNode   `json:"generic,omitempty"`
	Config    map[string]any `json:"config,omitempty"`
}

type FlowEdge struct {
	ID         string `json:"id"`
	FromNodeID ID     `json:"from_node_id"`
	FromPortID string `json:"from_port_id"`
	ToNodeID   ID     `json:"to_node_id"`
	ToPortID   string `json:"to_port_id"`
}

type FlowTerminal struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Reason string `json:"reason,omitempty"`
}

type FlowGraph struct {
	Nodes     []FlowNode     `json:"nodes"`
	Edges     []FlowEdge     `json:"edges"`
	Terminals []FlowTerminal `json:"terminals,omitempty"`
}

// ValidateShape validates the graph document's local shape.  Completeness
// and registry-dependent compatibility are intentionally implemented by the
// flow module, so a draft may still be saved while it is incomplete.
func (g FlowGraph) ValidateShape() error {
	seenNodes := make(map[ID]struct{}, len(g.Nodes))
	for _, node := range g.Nodes {
		if err := requireID("node.id", node.ID); err != nil {
			return err
		}
		if _, ok := seenNodes[node.ID]; ok {
			return fmt.Errorf("%w: duplicate node %s", ErrInvalid, node.ID)
		}
		seenNodes[node.ID] = struct{}{}
		switch node.Kind {
		case NodeConnector:
			if node.Connector == nil || strings.TrimSpace(node.Connector.BehaviorKey) == "" || strings.TrimSpace(node.Connector.BehaviorVersion) == "" {
				return fmt.Errorf("%w: connector node %s needs behavior key and version", ErrInvalid, node.ID)
			}
		case NodeGeneric:
			if node.Generic == nil || strings.TrimSpace(node.Generic.Type) == "" {
				return fmt.Errorf("%w: generic node %s needs type", ErrInvalid, node.ID)
			}
		case NodeTerminal:
		default:
			return fmt.Errorf("%w: unsupported node kind %q", ErrInvalid, node.Kind)
		}
		ports := make(map[string]struct{}, len(node.Inputs)+len(node.Outputs))
		for _, port := range append(append([]Port(nil), node.Inputs...), node.Outputs...) {
			if strings.TrimSpace(port.ID) == "" {
				return fmt.Errorf("%w: node %s has port without id", ErrInvalid, node.ID)
			}
			if _, ok := ports[port.ID]; ok {
				return fmt.Errorf("%w: duplicate port %s on node %s", ErrInvalid, port.ID, node.ID)
			}
			ports[port.ID] = struct{}{}
		}
	}
	seenEdges := make(map[string]struct{}, len(g.Edges))
	for _, edge := range g.Edges {
		if strings.TrimSpace(edge.ID) == "" {
			return fmt.Errorf("%w: edge id is required", ErrInvalid)
		}
		if _, ok := seenEdges[edge.ID]; ok {
			return fmt.Errorf("%w: duplicate edge %s", ErrInvalid, edge.ID)
		}
		seenEdges[edge.ID] = struct{}{}
		if _, ok := seenNodes[edge.FromNodeID]; !ok {
			return fmt.Errorf("%w: edge %s source node missing", ErrInvalid, edge.ID)
		}
		if _, ok := seenNodes[edge.ToNodeID]; !ok {
			return fmt.Errorf("%w: edge %s target node missing", ErrInvalid, edge.ID)
		}
		if edge.FromNodeID == edge.ToNodeID {
			return fmt.Errorf("%w: self edge %s", ErrInvalid, edge.ID)
		}
	}
	return nil
}
