package flow

import (
	"encoding/json"
	"fmt"
	"strings"

	"specwire/bridge/internal/domain"
)

// SimulationNode is an execution-free trace entry.  Output ConnectorNodes are
// deliberately marked suppressed; their adapter operations are never called.
type SimulationNode struct {
	ID                   domain.ID       `json:"id"`
	Name                 string          `json:"name,omitempty"`
	Kind                 domain.NodeKind `json:"kind"`
	Status               string          `json:"status"`
	Input                map[string]any  `json:"input,omitempty"`
	Output               map[string]any  `json:"output,omitempty"`
	Error                string          `json:"error,omitempty"`
	SideEffectSuppressed bool            `json:"side_effect_suppressed,omitempty"`
}

type SimulationResult struct {
	Valid                     bool             `json:"valid"`
	Diagnostics               []Diagnostic     `json:"diagnostics,omitempty"`
	Nodes                     []SimulationNode `json:"nodes"`
	ExternalActionsSuppressed bool             `json:"external_actions_suppressed"`
}

// Simulate evaluates the graph's transformation nodes against one sample
// provider event.  It shares the same catalog contracts as publication and
// stops at output connector nodes without invoking an adapter.
func Simulate(graph domain.FlowGraph, catalog Catalog, event map[string]any, context RuntimeContext) SimulationResult {
	validation := catalog.Validate(graph, true)
	result := SimulationResult{Valid: validation.Valid, Diagnostics: validation.Diagnostics, ExternalActionsSuppressed: true}
	if event == nil {
		result.Valid = false
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Path: "sample_event", Code: "required", Message: "sample event is required"})
		return result
	}
	if !validation.Valid {
		return result
	}
	order, err := simulationOrder(graph)
	if err != nil {
		result.Valid = false
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Path: "graph", Code: "invalid_graph", Message: err.Error()})
		return result
	}
	inputID := simulationInputID(graph, catalog)
	if inputID.Empty() {
		result.Valid = false
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Path: "graph", Code: "input_count", Message: "input ConnectorNode is required"})
		return result
	}
	inputs := map[domain.ID]map[string]any{inputID: {"event": event}}
	active := map[domain.ID]bool{inputID: true}
	nodes := make(map[domain.ID]domain.FlowNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes[node.ID] = node
	}
	for _, nodeID := range order {
		if !active[nodeID] {
			continue
		}
		node := nodes[nodeID]
		input := simulationNodeInput(node, inputs[nodeID])
		trace := SimulationNode{ID: node.ID, Name: node.Name, Kind: node.Kind, Status: "succeeded", Input: input}
		value, suppressed, nodeErr := simulateNode(node, input, catalog, context)
		if nodeErr != nil {
			trace.Status = "failed"
			trace.Error = nodeErr.Error()
			result.Valid = false
			result.Nodes = append(result.Nodes, trace)
			return result
		}
		trace.SideEffectSuppressed = suppressed
		if suppressed {
			trace.Status = "suppressed"
		}
		if value == nil && node.Kind == domain.NodeGeneric && node.Generic != nil && node.Generic.Type == GenericConditionFilter {
			trace.Status = "skipped"
			result.Nodes = append(result.Nodes, trace)
			continue
		}
		trace.Output = simulationValue(value)
		result.Nodes = append(result.Nodes, trace)
		for _, edge := range graph.Edges {
			if edge.FromNodeID != nodeID {
				continue
			}
			if inputs[edge.ToNodeID] == nil {
				inputs[edge.ToNodeID] = map[string]any{}
			}
			inputs[edge.ToNodeID][edge.ToPortID] = value
			active[edge.ToNodeID] = true
		}
	}
	return result
}

func simulateNode(node domain.FlowNode, input map[string]any, catalog Catalog, context RuntimeContext) (any, bool, error) {
	switch node.Kind {
	case domain.NodeConnector:
		if node.Connector == nil {
			return nil, false, fmt.Errorf("%w: connector node configuration is missing", domain.ErrInvalid)
		}
		behavior, ok := catalog.Behavior(node.Connector.BehaviorKey, node.Connector.BehaviorVersion)
		if !ok {
			return nil, false, fmt.Errorf("%w: connector behavior is not registered", domain.ErrInvalid)
		}
		if behavior.Direction == domain.DirectionInput {
			return input["event"], false, nil
		}
		return input, true, nil
	case domain.NodeGeneric:
		if node.Generic == nil {
			return nil, false, fmt.Errorf("%w: generic node configuration is missing", domain.ErrInvalid)
		}
		switch node.Generic.Type {
		case GenericParseNormalize:
			model := fixedModel(node.Generic.ParameterBindings, "model")
			if model == "" {
				return nil, false, fmt.Errorf("%w: Parse/Normalize model is required", domain.ErrInvalid)
			}
			output, err := ParseNormalizeWithCatalog(input, model, context, catalog)
			if err != nil {
				return nil, false, err
			}
			return output, false, nil
		case GenericMappingTemplate:
			model := fixedModel(node.Generic.ParameterBindings, "model")
			if model == "" {
				return nil, false, fmt.Errorf("%w: Mapping/Template target model is required", domain.ErrInvalid)
			}
			mapping, err := simulationMapping(node, model)
			if err != nil {
				return nil, false, err
			}
			output, err := ApplyMapping(input, mapping, context)
			if err != nil {
				return nil, false, err
			}
			if err := ValidateModelValue(catalog, model, output); err != nil {
				return nil, false, err
			}
			return output, false, nil
		case GenericConditionFilter:
			filter, err := simulationFilter(node)
			if err != nil {
				return nil, false, err
			}
			matched, err := EvaluateFilter(input, filter)
			if err != nil {
				return nil, false, err
			}
			if !matched {
				return nil, false, nil
			}
			return input, false, nil
		default:
			return nil, false, fmt.Errorf("%w: unsupported generic node %s", domain.ErrInvalid, node.Generic.Type)
		}
	case domain.NodeTerminal:
		return input, false, nil
	default:
		return nil, false, fmt.Errorf("%w: unsupported node kind %s", domain.ErrInvalid, node.Kind)
	}
}

func fixedModel(bindings map[string]domain.ParameterBinding, name string) string {
	binding, ok := bindings[name]
	if !ok || binding.Kind != domain.BindingFixed {
		return ""
	}
	value, _ := binding.Value.(string)
	return strings.TrimSpace(value)
}

func simulationMapping(node domain.FlowNode, model string) (MappingSpec, error) {
	if node.Config != nil {
		if raw, ok := node.Config["mapping"]; ok {
			encoded, err := json.Marshal(raw)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid mapping definition", domain.ErrInvalid)
			}
			var mapping MappingSpec
			if err := json.Unmarshal(encoded, &mapping); err != nil {
				return nil, fmt.Errorf("%w: invalid mapping definition", domain.ErrInvalid)
			}
			return mapping, nil
		}
	}
	if binding, ok := node.Generic.ParameterBindings["mapping"]; ok && binding.Value != nil {
		encoded, err := json.Marshal(binding.Value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid mapping definition", domain.ErrInvalid)
		}
		var mapping MappingSpec
		if err := json.Unmarshal(encoded, &mapping); err != nil {
			return nil, fmt.Errorf("%w: invalid mapping definition", domain.ErrInvalid)
		}
		return mapping, nil
	}
	if mapping, ok := DefaultMappingForModel(model); ok {
		return mapping, nil
	}
	return nil, fmt.Errorf("%w: no default mapping for model %s", domain.ErrInvalid, model)
}

func simulationFilter(node domain.FlowNode) (Filter, error) {
	if node.Config == nil {
		return Filter{}, fmt.Errorf("%w: Condition/Filter definition is required", domain.ErrInvalid)
	}
	encoded, err := json.Marshal(node.Config["filter"])
	if err != nil {
		return Filter{}, fmt.Errorf("%w: invalid filter definition", domain.ErrInvalid)
	}
	var filter Filter
	if err := json.Unmarshal(encoded, &filter); err != nil || strings.TrimSpace(filter.Op) == "" {
		return Filter{}, fmt.Errorf("%w: invalid filter definition", domain.ErrInvalid)
	}
	return filter, nil
}

func simulationNodeInput(node domain.FlowNode, inputs map[string]any) map[string]any {
	if inputs == nil {
		return map[string]any{}
	}
	if node.Kind == domain.NodeConnector {
		if value, ok := inputs["event"]; ok {
			if object, ok := value.(map[string]any); ok {
				return map[string]any{"event": object}
			}
		}
	}
	for _, port := range node.Inputs {
		if value, ok := inputs[port.ID]; ok {
			if object, ok := value.(map[string]any); ok {
				return object
			}
			return map[string]any{"value": value}
		}
	}
	for _, value := range inputs {
		if object, ok := value.(map[string]any); ok {
			return object
		}
	}
	return inputs
}

func simulationValue(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	if value == nil {
		return nil
	}
	return map[string]any{"value": value}
}

func simulationInputID(graph domain.FlowGraph, catalog Catalog) domain.ID {
	for _, node := range graph.Nodes {
		if node.Kind == domain.NodeConnector && node.Connector != nil {
			if behavior, ok := catalog.Behavior(node.Connector.BehaviorKey, node.Connector.BehaviorVersion); ok && behavior.Direction == domain.DirectionInput {
				return node.ID
			}
		}
	}
	return ""
}

func simulationOrder(graph domain.FlowGraph) ([]domain.ID, error) {
	indegree := make(map[domain.ID]int, len(graph.Nodes))
	adjacency := make(map[domain.ID][]domain.ID, len(graph.Nodes))
	for _, node := range graph.Nodes {
		indegree[node.ID] = 0
	}
	for _, edge := range graph.Edges {
		if _, ok := indegree[edge.FromNodeID]; !ok {
			return nil, fmt.Errorf("%w: edge source node is missing", domain.ErrInvalid)
		}
		if _, ok := indegree[edge.ToNodeID]; !ok {
			return nil, fmt.Errorf("%w: edge target node is missing", domain.ErrInvalid)
		}
		adjacency[edge.FromNodeID] = append(adjacency[edge.FromNodeID], edge.ToNodeID)
		indegree[edge.ToNodeID]++
	}
	queue := make([]domain.ID, 0, len(indegree))
	for _, node := range graph.Nodes {
		if indegree[node.ID] == 0 {
			queue = append(queue, node.ID)
		}
	}
	order := make([]domain.ID, 0, len(indegree))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)
		for _, next := range adjacency[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if len(order) != len(indegree) {
		return nil, fmt.Errorf("%w: Flow graph contains a cycle", domain.ErrInvalid)
	}
	return order, nil
}
