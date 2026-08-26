package flow

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/registry"
)

const (
	GenericParseNormalize  = "parse-normalize"
	GenericMappingTemplate = "mapping-template"
	GenericConditionFilter = "condition-filter"
)

type Diagnostic struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ValidationResult struct {
	Valid       bool         `json:"valid"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type Catalog struct {
	Behaviors map[string]domain.ConnectorBehavior
	Models    map[string]domain.DataModelDefinition
	Adapters  map[string]bool
}

func NewCatalog(behaviors []domain.ConnectorBehavior, models []domain.DataModelDefinition, allowlistedAdapters []string) Catalog {
	catalog := Catalog{Behaviors: map[string]domain.ConnectorBehavior{}, Models: map[string]domain.DataModelDefinition{}, Adapters: map[string]bool{}}
	for _, item := range behaviors {
		catalog.Behaviors[registry.StableKey(item.Key, item.Version)] = item
	}
	for _, item := range models {
		catalog.Models[registry.StableKey(item.Key, item.Version)] = item
	}
	for _, operation := range allowlistedAdapters {
		catalog.Adapters[operation] = true
	}
	return catalog
}

func (c Catalog) Behavior(key, version string) (domain.ConnectorBehavior, bool) {
	item, ok := c.Behaviors[registry.StableKey(key, version)]
	return item, ok
}
func (c Catalog) Model(ref string) (domain.DataModelDefinition, bool) {
	if item, ok := c.Models[ref]; ok {
		return item, true
	}
	// Definitions use both key@version and the product-facing Key.vVersion
	// spelling.  Accept both at the seam but persist the original reference.
	for stable, item := range c.Models {
		if stable == ref || item.Key+"."+item.Version == ref || item.Key+"@"+item.Version == ref {
			return item, true
		}
	}
	return domain.DataModelDefinition{}, false
}

// ValidateModelValue validates a runtime value against a registered model
// definition.  Values arriving from JSON are normally float64 for numbers,
// so integer fields explicitly reject fractional values instead of silently
// truncating them.
func ValidateModelValue(c Catalog, model string, value map[string]any) error {
	definition, ok := c.Model(model)
	if !ok {
		return fmt.Errorf("%w: model %s is not registered", domain.ErrInvalid, model)
	}
	for _, field := range definition.RequiredFields {
		if blankValue(value[field]) {
			return fmt.Errorf("%w: model %s requires %s", domain.ErrInvalid, model, field)
		}
	}
	properties, _ := definition.Schema["properties"].(map[string]any)
	for field, raw := range properties {
		property, _ := raw.(map[string]any)
		actual, exists := value[field]
		if !exists || actual == nil {
			continue
		}
		switch propertyType(property["type"]) {
		case "string":
			if kind := reflect.ValueOf(actual).Kind(); kind != reflect.String {
				return fmt.Errorf("%w: model %s field %s must be a string", domain.ErrInvalid, model, field)
			}
		case "integer":
			if !integerValue(actual) {
				return fmt.Errorf("%w: model %s field %s must be an integer", domain.ErrInvalid, model, field)
			}
		case "number":
			if _, ok := modelNumberValue(actual); !ok {
				return fmt.Errorf("%w: model %s field %s must be a number", domain.ErrInvalid, model, field)
			}
		case "boolean":
			if _, ok := actual.(bool); !ok {
				return fmt.Errorf("%w: model %s field %s must be a boolean", domain.ErrInvalid, model, field)
			}
		}
		if constant, ok := property["const"]; ok && !modelValuesEqual(actual, constant) {
			return fmt.Errorf("%w: model %s field %s has an invalid constant", domain.ErrInvalid, model, field)
		}
	}
	return nil
}

func propertyType(value any) string {
	text, _ := value.(string)
	return text
}

func blankValue(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}

func integerValue(value any) bool {
	switch typed := value.(type) {
	case int:
		return true
	case int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return float32(math.Trunc(float64(typed))) == typed
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0) && math.Trunc(typed) == typed
	case json.Number:
		_, err := typed.Int64()
		return err == nil
	default:
		return false
	}
}

func modelNumberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case float32:
		return float64(typed), true
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func modelValuesEqual(left, right any) bool {
	if leftString, ok := left.(string); ok {
		if rightString, ok := right.(string); ok {
			return leftString == rightString
		}
	}
	return fmt.Sprint(left) == fmt.Sprint(right)
}

func (c Catalog) Validate(graph domain.FlowGraph, publish bool) ValidationResult {
	result := ValidationResult{Valid: true}
	add := func(path, code, message string) {
		result.Valid = false
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Path: path, Code: code, Message: message})
	}
	if err := graph.ValidateShape(); err != nil {
		add("graph", "invalid_shape", err.Error())
		return result
	}
	nodes := make(map[domain.ID]domain.FlowNode, len(graph.Nodes))
	inputCount := 0
	for i, node := range graph.Nodes {
		path := fmt.Sprintf("nodes[%d]", i)
		nodes[node.ID] = node
		switch node.Kind {
		case domain.NodeConnector:
			behavior, ok := c.Behavior(node.Connector.BehaviorKey, node.Connector.BehaviorVersion)
			if !ok {
				add(path, "behavior_not_found", "connector behavior is not registered")
				continue
			}
			if behavior.Status != "" && behavior.Status != domain.DefinitionPublished {
				add(path, "behavior_unavailable", "connector behavior is not published")
				continue
			}
			if !c.Adapters[behavior.AdapterOperation] {
				add(path, "adapter_not_allowlisted", "connector behavior does not reference an allowlisted deployed adapter")
			}
			if behavior.Direction == domain.DirectionInput {
				inputCount++
			}
			if behavior.Direction != domain.DirectionInput && behavior.Direction != domain.DirectionOutput {
				add(path, "invalid_direction", "connector behavior has an invalid direction")
			}
			validateBindings(path+".connector.parameter_bindings", node.Connector.ParameterBindings, add)
			validateParameterSchema(path+".connector.parameter_bindings", behavior.ParameterSchema, node.Connector.ParameterBindings, add)
			validateConnectorModelContract(path, node, behavior, add)
		case domain.NodeGeneric:
			if !supportedGeneric(node.Generic.Type) {
				add(path, "unsupported_node", "only Parse/Normalize, Mapping/Template, and Condition/Filter nodes are supported")
			}
			validateBindings(path+".generic.parameter_bindings", node.Generic.ParameterBindings, add)
			validateNoCodeParameters(path+".generic.parameter_bindings", node.Generic.ParameterBindings, add)
			validateNoCodeConfig(path+".config", node.Config, add)
			if node.Generic.Type == GenericConditionFilter && node.Config != nil {
				if exclusive, ok := node.Config["mutually_exclusive"].(bool); ok && !exclusive {
					add(path, "nonexclusive_condition", "Condition/Filter branches must be mutually exclusive")
				}
			}
		case domain.NodeTerminal:
			if node.Config != nil {
				if kind, ok := node.Config["kind"].(string); ok && kind == "error" {
					add(path, "unsupported_terminal", "error branches are not supported")
				}
			}
		}
		for portIndex, port := range append(append([]domain.Port(nil), node.Inputs...), node.Outputs...) {
			portPath := fmt.Sprintf("%s.ports[%d]", path, portIndex)
			if port.ModelRef != "" && !isProviderModel(port.ModelRef) {
				if _, ok := c.Model(port.ModelRef); !ok {
					add(portPath, "model_not_found", "port references an unregistered DataModel version")
				}
			}
		}
	}
	if inputCount != 1 {
		add("graph", "input_count", fmt.Sprintf("publishable Flow requires exactly one input ConnectorNode; found %d", inputCount))
	}
	if hasCycle(graph, nodes) {
		add("edges", "cycle", "Flow graph must be acyclic; loops and waits are not supported")
	}
	for i, edge := range graph.Edges {
		from, fromOK := nodes[edge.FromNodeID]
		to, toOK := nodes[edge.ToNodeID]
		if !fromOK || !toOK {
			continue
		}
		fromPort, fromOK := findPort(from, edge.FromPortID, domain.PortOutput)
		toPort, toOK := findPort(to, edge.ToPortID, domain.PortInput)
		if !fromOK || !toOK {
			add(fmt.Sprintf("edges[%d]", i), "port_not_found", "edge must connect an output port to an input port")
			continue
		}
		if fromPort.ModelRef != "" && toPort.ModelRef != "" && !modelsCompatible(fromPort.ModelRef, toPort.ModelRef, from) {
			add(fmt.Sprintf("edges[%d]", i), "incompatible_model", "port DataModels are incompatible; use Mapping/Template to declare the conversion")
		}
	}
	for _, node := range graph.Nodes {
		if node.Kind != domain.NodeGeneric {
			continue
		}
		for _, port := range node.Inputs {
			if !port.Required {
				continue
			}
			incoming := false
			for _, edge := range graph.Edges {
				if edge.ToNodeID == node.ID && edge.ToPortID == port.ID {
					incoming = true
					break
				}
			}
			if !incoming {
				add("node:"+string(node.ID), "required_input_missing", "required input port is not connected")
			}
		}
	}
	if publish {
		reachable := reachableFromInput(graph, nodes, c)
		for _, node := range graph.Nodes {
			if _, ok := reachable[node.ID]; !ok {
				continue
			}
			if hasOutgoing(graph, node.ID) {
				continue
			}
			if node.Kind == domain.NodeTerminal {
				continue
			}
			if node.Kind != domain.NodeConnector || node.Connector == nil {
				add("node:"+string(node.ID), "path_not_terminated", "every executable path must terminate at an output ConnectorNode or filtered terminal")
				continue
			}
			behavior, ok := c.Behavior(node.Connector.BehaviorKey, node.Connector.BehaviorVersion)
			if !ok || behavior.Direction != domain.DirectionOutput {
				add("node:"+string(node.ID), "output_required", "path terminator must be an output ConnectorNode")
			}
		}
	}
	sort.Slice(result.Diagnostics, func(i, j int) bool {
		if result.Diagnostics[i].Path == result.Diagnostics[j].Path {
			return result.Diagnostics[i].Code < result.Diagnostics[j].Code
		}
		return result.Diagnostics[i].Path < result.Diagnostics[j].Path
	})
	return result
}

func supportedGeneric(kind string) bool {
	return kind == GenericParseNormalize || kind == GenericMappingTemplate || kind == GenericConditionFilter
}

func validateBindings(path string, bindings map[string]domain.ParameterBinding, add func(string, string, string)) {
	for name, binding := range bindings {
		if binding.Kind == "" {
			add(path+"."+name, "binding_kind_missing", "parameter binding kind is required")
			continue
		}
		switch binding.Kind {
		case domain.BindingFixed:
			if binding.Value == nil {
				add(path+"."+name, "fixed_value_missing", "fixed parameter needs a value")
			}
		case domain.BindingConnectionRef, domain.BindingResourceRef, domain.BindingSecretRef, domain.BindingRuntimeRef:
			if strings.TrimSpace(binding.Ref) == "" {
				add(path+"."+name, "reference_missing", "reference binding needs a ref")
			}
			if binding.Value != nil {
				add(path+"."+name, "reference_value_forbidden", "reference binding cannot contain an inline value")
			}
		default:
			add(path+"."+name, "binding_kind_unsupported", "parameter binding kind is not supported")
		}
		if strings.ContainsAny(binding.Ref, ";{}()[]\n\t\r") {
			add(path+"."+name, "expression_forbidden", "arbitrary expressions are not allowed in parameter references")
		}
	}
}

func validateParameterSchema(path string, schema map[string]any, bindings map[string]domain.ParameterBinding, add func(string, string, string)) {
	if len(schema) == 0 {
		return
	}
	properties, _ := schema["properties"].(map[string]any)
	for name := range bindings {
		if len(properties) != 0 {
			if _, ok := properties[name]; !ok {
				add(path+"."+name, "parameter_not_declared", "parameter is not declared by the ConnectorBehavior schema")
				continue
			}
		}
		if property, ok := properties[name].(map[string]any); ok {
			if allowed, ok := stringList(property["binding_kinds"]); ok && len(allowed) != 0 {
				binding := bindings[name]
				if !containsString(allowed, string(binding.Kind)) {
					add(path+"."+name, "binding_kind_forbidden", "parameter binding kind is not allowed by the ConnectorBehavior schema")
				}
			}
		}
	}
	for _, required := range requiredParameterNames(schema) {
		binding, ok := bindings[required]
		if !ok || binding.Kind == "" || (binding.Kind == domain.BindingFixed && binding.Value == nil) || ((binding.Kind == domain.BindingConnectionRef || binding.Kind == domain.BindingResourceRef || binding.Kind == domain.BindingSecretRef || binding.Kind == domain.BindingRuntimeRef) && strings.TrimSpace(binding.Ref) == "") {
			add(path+"."+required, "required_parameter_missing", "required ConnectorBehavior parameter is not configured")
		}
	}
}

func requiredParameterNames(schema map[string]any) []string {
	values, ok := stringList(schema["required"])
	if !ok {
		return nil
	}
	return values
}

func stringList(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			return append([]string(nil), strings...), true
		}
		return nil, false
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result, true
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func validateConnectorModelContract(path string, node domain.FlowNode, behavior domain.ConnectorBehavior, add func(string, string, string)) {
	if behavior.Direction == domain.DirectionInput && behavior.OutputModelRef != "" {
		if !hasModelPort(node.Outputs, behavior.OutputModelRef, domain.PortOutput) {
			add(path+".outputs", "behavior_output_model_mismatch", "input ConnectorBehavior output port must expose the behavior output model")
		}
	}
	if behavior.Direction == domain.DirectionOutput && behavior.InputModelRef != "" {
		if !hasModelPort(node.Inputs, behavior.InputModelRef, domain.PortInput) {
			add(path+".inputs", "behavior_input_model_mismatch", "output ConnectorBehavior input port must accept the behavior input model")
		}
	}
}

func hasModelPort(ports []domain.Port, model string, direction domain.PortDirection) bool {
	for _, port := range ports {
		if port.Direction == direction && port.ModelRef == model {
			return true
		}
	}
	return false
}

func validateNoCodeParameters(path string, bindings map[string]domain.ParameterBinding, add func(string, string, string)) {
	for name, binding := range bindings {
		lowerName := strings.ToLower(strings.TrimSpace(name))
		if lowerName == "script" || lowerName == "code" || lowerName == "expression" || lowerName == "program" {
			add(path+"."+name, "no_code_parameter", "generic nodes cannot execute scripts or arbitrary expressions")
		}
		if binding.Kind == domain.BindingFixed {
			if value, ok := binding.Value.(string); ok && containsExecutableText(value) {
				add(path+"."+name, "no_code_parameter", "generic nodes cannot execute scripts or arbitrary expressions")
			}
		}
	}
}

func validateNoCodeConfig(path string, config map[string]any, add func(string, string, string)) {
	for name, value := range config {
		lowerName := strings.ToLower(strings.TrimSpace(name))
		if lowerName == "script" || lowerName == "code" || lowerName == "expression" || lowerName == "program" {
			add(path+"."+name, "no_code_parameter", "generic nodes cannot execute scripts or arbitrary expressions")
		}
		if text, ok := value.(string); ok && containsExecutableText(text) {
			add(path+"."+name, "no_code_parameter", "generic nodes cannot execute scripts or arbitrary expressions")
		}
	}
}

func containsExecutableText(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"eval(", "exec(", "javascript:", "python:", "shell:", "bash:"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isProviderModel(ref string) bool { return strings.HasPrefix(ref, "provider:") }

func findPort(node domain.FlowNode, id string, direction domain.PortDirection) (domain.Port, bool) {
	ports := node.Inputs
	if direction == domain.PortOutput {
		ports = node.Outputs
	}
	for _, port := range ports {
		if port.ID == id && port.Direction == direction {
			return port, true
		}
	}
	return domain.Port{}, false
}

func modelsCompatible(from, to string, fromNode domain.FlowNode) bool {
	if from == to {
		return true
	}
	return fromNode.Kind == domain.NodeGeneric && fromNode.Generic != nil && fromNode.Generic.Type == GenericMappingTemplate
}

func hasOutgoing(graph domain.FlowGraph, nodeID domain.ID) bool {
	for _, edge := range graph.Edges {
		if edge.FromNodeID == nodeID {
			return true
		}
	}
	return false
}

func hasCycle(graph domain.FlowGraph, nodes map[domain.ID]domain.FlowNode) bool {
	adj := map[domain.ID][]domain.ID{}
	for _, edge := range graph.Edges {
		adj[edge.FromNodeID] = append(adj[edge.FromNodeID], edge.ToNodeID)
	}
	state := map[domain.ID]int{}
	var visit func(domain.ID) bool
	visit = func(id domain.ID) bool {
		if state[id] == 1 {
			return true
		}
		if state[id] == 2 {
			return false
		}
		state[id] = 1
		for _, next := range adj[id] {
			if _, ok := nodes[next]; ok && visit(next) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for id := range nodes {
		if visit(id) {
			return true
		}
	}
	return false
}

func reachableFromInput(graph domain.FlowGraph, nodes map[domain.ID]domain.FlowNode, catalog Catalog) map[domain.ID]struct{} {
	seen := map[domain.ID]struct{}{}
	queue := []domain.ID{}
	for _, node := range graph.Nodes {
		if node.Kind == domain.NodeConnector && node.Connector != nil {
			if behavior, ok := catalog.Behavior(node.Connector.BehaviorKey, node.Connector.BehaviorVersion); ok && behavior.Direction == domain.DirectionInput {
				queue = append(queue, node.ID)
			}
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		for _, edge := range graph.Edges {
			if edge.FromNodeID == id {
				if _, ok := nodes[edge.ToNodeID]; ok {
					queue = append(queue, edge.ToNodeID)
				}
			}
		}
	}
	return seen
}
