package flow

import (
	"strings"
	"testing"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/registry"
)

func builtinCatalog(t *testing.T) Catalog {
	t.Helper()
	bundle, err := registry.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	return NewCatalog(bundle.Behaviors, bundle.DataModels, []string{"gitlab.events.issue", "gitlab.events.push", "multica.issue.create", "multica.issue.status"})
}

func TestBuiltinTemplatesCompile(t *testing.T) {
	catalog := builtinCatalog(t)
	for _, template := range BuiltinTemplates() {
		result := catalog.Validate(template.Graph, true)
		if !result.Valid {
			t.Fatalf("template %s invalid: %+v", template.Key, result.Diagnostics)
		}
		plan, err := Compile(template.Graph, catalog)
		if err != nil {
			t.Fatalf("compile %s: %v", template.Key, err)
		}
		if plan["plan_version"] != "1" {
			t.Fatalf("plan for %s = %#v", template.Key, plan)
		}
	}
}

func TestDraftMayBeIncompleteButPublishDiagnosticsAreSpecific(t *testing.T) {
	catalog := builtinCatalog(t)
	empty := catalog.Validate(domain.FlowGraph{}, false)
	if empty.Valid || !hasDiagnostic(empty, "input_count") {
		t.Fatalf("empty draft diagnostics = %+v", empty)
	}
	invalid := publicationGraph()
	invalid.Edges = append(invalid.Edges, domain.FlowEdge{ID: "cycle", FromNodeID: "multica-create", FromPortID: "out", ToNodeID: "parse-publication", ToPortID: "event"})
	result := catalog.Validate(invalid, true)
	if result.Valid || !hasDiagnostic(result, "cycle") {
		t.Fatalf("cycle diagnostics = %+v", result)
	}
	invalid.Nodes[2].Generic.ParameterBindings["script"] = domain.ParameterBinding{Kind: domain.BindingFixed, Value: "return eval(input)"}
	result = catalog.Validate(invalid, true)
	if result.Valid || !hasDiagnostic(result, "unsupported_node") && !hasDiagnostic(result, "expression_forbidden") && !hasDiagnostic(result, "no_code_parameter") {
		t.Fatalf("script diagnostics = %+v", result)
	}
}

func TestMappingAndFilterAreRestrictedDeclarativeOperations(t *testing.T) {
	input := map[string]any{"change_id": "CH-1", "nested": map[string]any{"title": "Hello"}}
	output, err := ApplyMapping(input, MappingSpec{"title": {Source: "$input.nested.title"}, "prefix": {Concat: []string{"literal:[SpecWire] ", "$input.change_id"}}, "fallback": {Source: "$input.missing", Default: "backlog"}}, RuntimeContext{})
	if err != nil {
		t.Fatal(err)
	}
	if output["title"] != "Hello" || output["prefix"] != "[SpecWire] CH-1" || output["fallback"] != "backlog" {
		t.Fatalf("mapping output = %#v", output)
	}
	if _, err := ApplyMapping(input, MappingSpec{"bad": {Source: "$input.x;exec()"}}, RuntimeContext{}); err == nil {
		t.Fatal("executable mapping accepted")
	}
	matched, err := EvaluateFilter(input, Filter{Op: "and", Children: []Filter{{Op: "exists", Field: "change_id"}, {Op: "contains", Field: "change_id", Value: "CH"}}})
	if err != nil || !matched {
		t.Fatalf("filter = %v, %v", matched, err)
	}
	if _, err := EvaluateFilter(input, Filter{Op: "execute", Field: "change_id"}); err == nil {
		t.Fatal("unsupported filter accepted")
	}
}

func TestRegisteredModelCanBeUsedByDeclarativeParseNormalize(t *testing.T) {
	model := domain.DataModelDefinition{
		Key: "CustomEvent", Version: "v1", DisplayName: "Custom Event", AllowExtensions: true,
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"event_id": map[string]any{"type": "string"},
			"attempt":  map[string]any{"type": "integer"},
		}}, RequiredFields: []string{"event_id"},
	}
	catalog := NewCatalog(nil, []domain.DataModelDefinition{model}, nil)
	output, err := ParseNormalizeWithCatalog(map[string]any{"event_id": "evt-1", "attempt": float64(2), "extra": "kept"}, "CustomEvent.v1", RuntimeContext{}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if output["event_id"] != "evt-1" || output["extensions"] == nil {
		t.Fatalf("custom model output = %#v", output)
	}
	if _, err := ParseNormalizeWithCatalog(map[string]any{"attempt": 1.5}, "CustomEvent.v1", RuntimeContext{}, catalog); err == nil {
		t.Fatal("missing required field or fractional integer was accepted")
	}
	if _, err := ParseNormalizeWithCatalog(map[string]any{"event_id": 42}, "CustomEvent.v1", RuntimeContext{}, catalog); err == nil {
		t.Fatal("wrong model field type was accepted")
	}
}

func hasDiagnostic(result ValidationResult, code string) bool {
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == code || strings.Contains(diagnostic.Message, code) {
			return true
		}
	}
	return false
}
