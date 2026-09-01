package flow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"specwire/bridge/internal/domain"
)

type RuntimeContext struct {
	WorkspaceID     domain.ID
	ConnectionID    domain.ID
	FlowExecutionID domain.ID
	EventID         domain.ID
	SourceProject   string
	TargetProject   string
	TargetRef       string
}

type MappingRule struct {
	Source   string   `json:"source,omitempty"`
	Constant any      `json:"constant,omitempty"`
	Default  any      `json:"default,omitempty"`
	Concat   []string `json:"concat,omitempty"`
}

type MappingSpec map[string]MappingRule

// DefaultMappingForModel is the small, declarative-by-contract mapping set
// used by the built-in templates.  Custom Mapping/Template nodes can
// provide their own MappingSpec in the graph; these defaults keep the bundled
// templates useful without embedding a provider call or executable script in
// the canvas.
func DefaultMappingForModel(model string) (MappingSpec, bool) {
	switch model {
	case "MulticaCreateIssueInput.v1", "MulticaCreateIssueInput@v1":
		return MappingSpec{
			"target_project":  {Source: "$connection.target_project"},
			"title":           {Concat: []string{"literal:[SpecWire] ", "$input.change_id"}},
			"description":     {Source: "$input.description", Default: ""},
			"status":          {Source: "$input.status", Default: "backlog"},
			"assignee":        {Source: "$input.assignee", Default: ""},
			"change_id":       {Source: "$input.change_id"},
			"branch":          {Source: "$input.branch"},
			"branch_head_sha": {Source: "$input.branch_head_sha"},
			"target_ref":      {Source: "$input.target_ref", Default: "refs/heads/main"},
			"issue_iid":       {Source: "$input.issue_iid"},
		}, true
	case "MulticaCompleteIssueInput.v1", "MulticaCompleteIssueInput@v1":
		return MappingSpec{
			// The archive event does not carry a target provider ID.  Keep the
			// stable change identity as the lookup selector; the output adapter
			// resolves the actual target from persisted correlations.
			"correlation_id": {Source: "$input.change_id"},
			"change_id":      {Source: "$input.change_id"},
			"desired_status": {Constant: "done"},
			"lifecycle_event": {Source: "$input.lifecycle_event"},
			"lifecycle_reason": {Source: "$input.lifecycle_reason"},
		}, true
	default:
		return nil, false
	}
}

func ParseNormalize(providerEvent map[string]any, model string, context RuntimeContext) (map[string]any, error) {
	return parseNormalize(providerEvent, model, context, Catalog{})
}

// ParseNormalizeWithCatalog extends the built-in parsers with the declarative
// registry seam.  An administrator-added model is intentionally treated as a
// shape contract: the provider adapter supplies the event object, and the
// model validator enforces its required fields and types.  Provider-specific
// semantic extraction remains an explicitly deployed adapter concern rather
// than user-supplied code.
func ParseNormalizeWithCatalog(providerEvent map[string]any, model string, context RuntimeContext, catalog Catalog) (map[string]any, error) {
	return parseNormalize(providerEvent, model, context, catalog)
}

func parseNormalize(providerEvent map[string]any, model string, context RuntimeContext, catalog Catalog) (map[string]any, error) {
	if providerEvent == nil {
		return nil, fmt.Errorf("%w: provider event is required", domain.ErrInvalid)
	}
	var output map[string]any
	switch model {
	case "ChangePublication.v1", "ChangePublication@v1":
		output = parsePublication(providerEvent, context)
	case "ArchiveCompletion.v1", "ArchiveCompletion@v1":
		output = parseArchiveCompletion(providerEvent, context)
	case "ChangeLifecycle.v1", "ChangeLifecycle@v1":
		output = parseChangeLifecycle(providerEvent, context)
	default:
		definition, ok := catalog.Model(model)
		if !ok {
			return nil, fmt.Errorf("%w: Parse/Normalize has no built-in parser for model %s", domain.ErrInvalid, model)
		}
		output = cloneObject(providerEvent)
		if definition.AllowExtensions {
			output["extensions"] = map[string]any{"provider_event": cloneObject(providerEvent)}
		}
	}
	if err := validateRequiredModel(output, model); err != nil {
		return nil, err
	}
	if _, ok := catalog.Model(model); ok {
		if err := ValidateModelValue(catalog, model, output); err != nil {
			return nil, err
		}
		if definition, ok := catalog.Model(model); ok && definition.AllowExtensions {
			if _, exists := output["extensions"]; !exists {
				output["extensions"] = map[string]any{"provider_event": cloneObject(providerEvent)}
			}
		}
	}
	return output, nil
}

func parsePublication(event map[string]any, context RuntimeContext) map[string]any {
	attributes := object(event["object_attributes"])
	description := stringValue(attributes["description"])
	fields := parseDescriptionFields(description)
	output := map[string]any{}
	output["change_id"] = firstValue(fields["change_id"], event["change_id"])
	output["branch"] = firstValue(fields["branch"], event["branch"])
	output["branch_head_sha"] = firstValue(fields["branch_head_sha"], event["branch_head_sha"])
	output["source_project"] = firstValue(context.SourceProject, stringValue(object(event["project"])["path_with_namespace"]), stringValue(event["source_project"]))
	output["description"] = description
	if iid := intValue(attributes["iid"]); iid != 0 {
		output["issue_iid"] = iid
	}
	if url := firstValue(stringValue(attributes["url"]), stringValue(attributes["web_url"]), stringValue(event["issue_url"])); url != "" {
		output["issue_url"] = url
	}
	output["target_ref"] = firstValue(fields["target_ref"], context.TargetRef, "refs/heads/main")
	output["status"] = firstValue(fields["status"], "backlog")
	output["assignee"] = firstValue(fields["assignee"], "")
	return output
}

func parseArchiveCompletion(event map[string]any, context RuntimeContext) map[string]any {
	output := map[string]any{}
	fields := parseDescriptionFields(stringValue(event["message"]))
	output["change_id"] = firstValue(stringValue(event["change_id"]), fields["change_id"])
	output["source_project"] = firstValue(context.SourceProject, stringValue(object(event["project"])["path_with_namespace"]), stringValue(event["source_project"]))
	output["target_ref"] = firstValue(stringValue(event["ref"]), context.TargetRef, "refs/heads/main")
	output["provider_delivery_id"] = firstValue(stringValue(event["provider_delivery_id"]), stringValue(event["delivery_id"]))
	if lifecycleEvent := stringValue(event["lifecycle_event"]); lifecycleEvent != "" {
		output["lifecycle_event"] = lifecycleEvent
	}
	if lifecycleReason := stringValue(event["lifecycle_reason"]); lifecycleReason != "" {
		output["lifecycle_reason"] = lifecycleReason
	}
	return output
}

func parseChangeLifecycle(event map[string]any, context RuntimeContext) map[string]any {
	attributes := object(event["object_attributes"])
	description := stringValue(attributes["description"])
	fields := parseDescriptionFields(description)
	output := map[string]any{}
	output["change_id"] = firstValue(fields["change_id"], stringValue(event["change_id"]))
	output["source_project"] = firstValue(context.SourceProject, stringValue(object(event["project"])["path_with_namespace"]), stringValue(event["source_project"]))
	output["target_ref"] = firstValue(stringValue(event["target_ref"]), context.TargetRef, "refs/heads/main")
	output["provider_delivery_id"] = firstValue(stringValue(event["provider_delivery_id"]), stringValue(event["delivery_id"]))
	output["lifecycle_event"] = firstValue(stringValue(event["lifecycle_event"]), "abandoned")
	output["lifecycle_reason"] = firstValue(
		stringValue(event["lifecycle_reason"]),
		fields["specwire_reason"],
		fields["specwire-reason"],
		fields["abandon_reason"],
		fields["abandon-reason"],
		fields["reason"],
		"GitLab Issue 添加 specwire::abandoned 标签",
	)
	if iid := intValue(attributes["iid"]); iid != 0 {
		output["issue_iid"] = iid
	}
	if url := firstValue(stringValue(attributes["url"]), stringValue(attributes["web_url"]), stringValue(event["issue_url"])); url != "" {
		output["issue_url"] = url
	}
	return output
}

func parseDescriptionFields(description string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			key, value, ok = strings.Cut(line, "=")
		}
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "change_id", "branch", "branch_head_sha", "target_ref", "status", "assignee",
			"lifecycle_reason", "specwire_reason", "specwire-reason", "abandon_reason", "abandon-reason", "reason":
			result[key] = value
		}
	}
	return result
}

func validateRequiredModel(value map[string]any, model string) error {
	required := map[string][]string{
		"ChangePublication.v1": {"change_id", "branch", "branch_head_sha"},
		"ChangePublication@v1": {"change_id", "branch", "branch_head_sha"},
		"ArchiveCompletion.v1": {"change_id", "source_project", "target_ref", "provider_delivery_id"},
		"ArchiveCompletion@v1": {"change_id", "source_project", "target_ref", "provider_delivery_id"},
		"ChangeLifecycle.v1":   {"change_id", "source_project", "target_ref", "provider_delivery_id", "lifecycle_event", "lifecycle_reason"},
		"ChangeLifecycle@v1":   {"change_id", "source_project", "target_ref", "provider_delivery_id", "lifecycle_event", "lifecycle_reason"},
	}[model]
	for _, field := range required {
		if strings.TrimSpace(stringValue(value[field])) == "" {
			return fmt.Errorf("%w: model %s requires %s", domain.ErrInvalid, model, field)
		}
	}
	return nil
}

func ApplyMapping(input map[string]any, spec MappingSpec, context RuntimeContext) (map[string]any, error) {
	if input == nil {
		input = map[string]any{}
	}
	output := map[string]any{}
	for target, rule := range spec {
		if rule.Constant != nil {
			output[target] = rule.Constant
			continue
		}
		if len(rule.Concat) != 0 {
			var b strings.Builder
			for _, part := range rule.Concat {
				value, err := resolveReference(input, part, context)
				if err != nil {
					return nil, err
				}
				b.WriteString(stringValue(value))
			}
			output[target] = b.String()
			continue
		}
		if rule.Source != "" {
			value, err := resolveReference(input, rule.Source, context)
			if err != nil {
				return nil, err
			}
			if isEmptyValue(value) && rule.Default != nil {
				value = rule.Default
			}
			output[target] = value
			continue
		}
		if rule.Default != nil {
			output[target] = rule.Default
			continue
		}
		return nil, fmt.Errorf("%w: mapping %s has no source, constant or default", domain.ErrInvalid, target)
	}
	return output, nil
}

func resolveReference(input map[string]any, ref string, context RuntimeContext) (any, error) {
	if strings.HasPrefix(ref, "literal:") {
		return strings.TrimPrefix(ref, "literal:"), nil
	}
	if strings.ContainsAny(ref, ";{}()[]`\n\t\r") {
		return nil, fmt.Errorf("%w: executable mapping reference is forbidden", domain.ErrInvalid)
	}
	if strings.HasPrefix(ref, "$connection.") {
		switch strings.TrimPrefix(ref, "$connection.") {
		case "source_project":
			return context.SourceProject, nil
		case "target_project":
			return context.TargetProject, nil
		case "target_ref":
			return firstValue(context.TargetRef, "refs/heads/main"), nil
		default:
			return nil, fmt.Errorf("%w: undeclared Connection reference %s", domain.ErrInvalid, ref)
		}
	}
	if strings.HasPrefix(ref, "$runtime.") {
		switch strings.TrimPrefix(ref, "$runtime.") {
		case "workspace_id":
			return context.WorkspaceID, nil
		case "connection_id":
			return context.ConnectionID, nil
		case "flow_execution_id":
			return context.FlowExecutionID, nil
		case "event_id":
			return context.EventID, nil
		default:
			return nil, fmt.Errorf("%w: undeclared runtime reference %s", domain.ErrInvalid, ref)
		}
	}
	ref = strings.TrimPrefix(ref, "$input.")
	return lookupPath(input, ref), nil
}

func lookupPath(input map[string]any, path string) any {
	current := any(input)
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[segment]
	}
	return current
}

type Filter struct {
	Op       string   `json:"op"`
	Field    string   `json:"field,omitempty"`
	Value    any      `json:"value,omitempty"`
	Children []Filter `json:"children,omitempty"`
}

// ValidateFilter checks the static, no-code filter vocabulary before a graph
// can be published.  EvaluateFilter repeats the operational checks at runtime
// because Flow versions are durable and must remain defensive when replayed.
func ValidateFilter(filter Filter) error {
	operation := strings.ToLower(strings.TrimSpace(filter.Op))
	switch operation {
	case "and", "or":
		if len(filter.Children) == 0 {
			return fmt.Errorf("%w: Boolean filter requires children", domain.ErrInvalid)
		}
		for _, child := range filter.Children {
			if err := ValidateFilter(child); err != nil {
				return err
			}
		}
	case "exists", "equals", "contains", "prefix", "suffix", "gt", "gte", "lt", "lte":
		if strings.TrimSpace(filter.Field) == "" {
			return fmt.Errorf("%w: filter field is required", domain.ErrInvalid)
		}
		if operation != "exists" && filter.Value == nil {
			return fmt.Errorf("%w: filter value is required", domain.ErrInvalid)
		}
		if (operation == "gt" || operation == "gte" || operation == "lt" || operation == "lte") && filter.Value != nil {
			if _, ok := numberValue(filter.Value); !ok {
				return fmt.Errorf("%w: comparison filter requires a numeric value", domain.ErrInvalid)
			}
		}
	default:
		return fmt.Errorf("%w: unsupported filter operator %s", domain.ErrInvalid, filter.Op)
	}
	return nil
}

func EvaluateFilter(input map[string]any, filter Filter) (bool, error) {
	operation := strings.ToLower(filter.Op)
	switch operation {
	case "and", "or":
		if len(filter.Children) == 0 {
			return false, fmt.Errorf("%w: Boolean filter requires children", domain.ErrInvalid)
		}
		result := operation == "and"
		for _, child := range filter.Children {
			value, err := EvaluateFilter(input, child)
			if err != nil {
				return false, err
			}
			if operation == "and" {
				result = result && value
			} else {
				result = result || value
			}
		}
		return result, nil
	case "exists":
		return lookupPath(input, filter.Field) != nil, nil
	case "equals":
		return valuesEqual(lookupPath(input, filter.Field), filter.Value), nil
	case "contains", "prefix", "suffix":
		actual := stringValue(lookupPath(input, filter.Field))
		expected := stringValue(filter.Value)
		switch operation {
		case "contains":
			return strings.Contains(actual, expected), nil
		case "prefix":
			return strings.HasPrefix(actual, expected), nil
		default:
			return strings.HasSuffix(actual, expected), nil
		}
	case "gt", "gte", "lt", "lte":
		actual, actualOK := numberValue(lookupPath(input, filter.Field))
		expected, expectedOK := numberValue(filter.Value)
		if !actualOK || !expectedOK {
			return false, fmt.Errorf("%w: comparison filter requires numbers", domain.ErrInvalid)
		}
		switch operation {
		case "gt":
			return actual > expected, nil
		case "gte":
			return actual >= expected, nil
		case "lt":
			return actual < expected, nil
		default:
			return actual <= expected, nil
		}
	default:
		return false, fmt.Errorf("%w: unsupported filter operator %s", domain.ErrInvalid, filter.Op)
	}
}

var numberPattern = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case jsonNumber:
		parsed, err := strconv.ParseFloat(string(typed), 64)
		return parsed, err == nil
	case string:
		if !numberPattern.MatchString(typed) {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(typed, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// jsonNumber avoids importing encoding/json into the node evaluator's public
// value path; callers decoding JSON can normalize numbers to float64 first.
type jsonNumber string

func valuesEqual(left, right any) bool { return stringValue(left) == stringValue(right) }
func isEmptyValue(value any) bool      { return value == nil || stringValue(value) == "" }
func object(value any) map[string]any {
	if value, ok := value.(map[string]any); ok {
		return value
	}
	return map[string]any{}
}

func cloneObject(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var copy map[string]any
	if err := json.Unmarshal(encoded, &copy); err != nil {
		return map[string]any{}
	}
	return copy
}
func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if stringValue, ok := value.(string); ok {
		return stringValue
	}
	return fmt.Sprint(value)
}
func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}
func firstValue(values ...any) string {
	for _, value := range values {
		if text := stringValue(value); strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}
