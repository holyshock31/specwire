package flow

import "specwire/bridge/internal/domain"

const (
	TemplatePublishChange   = "publish-change"
	TemplateCompleteArchive = "complete-archive"
	TemplateAbandonChange   = "abandon-change"
)

func BuiltinTemplates() []domain.FlowTemplate {
	return []domain.FlowTemplate{
		{Key: TemplatePublishChange, Version: "1.0.0", Name: "Publish Change", Description: "Project a GitLab change Issue into Multica.", Status: domain.DefinitionPublished, Graph: publicationGraph()},
		{Key: TemplateCompleteArchive, Version: "1.0.0", Name: "Complete Archive", Description: "Complete the correlated Multica projection after archive.", Status: domain.DefinitionPublished, Graph: archiveGraph()},
		{Key: TemplateAbandonChange, Version: "1.0.0", Name: "Abandon Change", Description: "Cancel the correlated Multica projection when the source Change Issue receives the specwire::abandoned label.", Status: domain.DefinitionPublished, Graph: abandonGraph()},
	}
}

func publicationGraph() domain.FlowGraph {
	return domain.FlowGraph{Nodes: []domain.FlowNode{
		{ID: "gitlab-issue-input", Kind: domain.NodeConnector, Name: "GitLab Issue Hook", Outputs: []domain.Port{{ID: "event", Name: "GitLab Issue Event", Direction: domain.PortOutput, ModelRef: "provider:gitlab.issue.v1"}}, Connector: &domain.ConnectorNode{BehaviorKey: "gitlab.issue-hook", BehaviorVersion: "1.0.0"}},
		{ID: "parse-publication", Kind: domain.NodeGeneric, Name: "Parse / Normalize", Inputs: []domain.Port{{ID: "event", Direction: domain.PortInput, ModelRef: "provider:gitlab.issue.v1", Required: true}}, Outputs: []domain.Port{{ID: "publication", Direction: domain.PortOutput, ModelRef: "ChangePublication.v1"}}, Generic: &domain.GenericNode{Type: GenericParseNormalize, ParameterBindings: map[string]domain.ParameterBinding{"model": {Kind: domain.BindingFixed, Value: "ChangePublication.v1"}}}},
		{ID: "map-issue", Kind: domain.NodeGeneric, Name: "Build Multica Input", Inputs: []domain.Port{{ID: "publication", Direction: domain.PortInput, ModelRef: "ChangePublication.v1", Required: true}}, Outputs: []domain.Port{{ID: "issue-input", Direction: domain.PortOutput, ModelRef: "MulticaCreateIssueInput.v1"}}, Generic: &domain.GenericNode{Type: GenericMappingTemplate, ParameterBindings: map[string]domain.ParameterBinding{"model": {Kind: domain.BindingFixed, Value: "MulticaCreateIssueInput.v1"}}}},
		{ID: "multica-create", Kind: domain.NodeConnector, Name: "Multica Create Issue", Inputs: []domain.Port{{ID: "issue-input", Direction: domain.PortInput, ModelRef: "MulticaCreateIssueInput.v1", Required: true}}, Connector: &domain.ConnectorNode{BehaviorKey: "multica.create-issue", BehaviorVersion: "1.0.0", ParameterBindings: map[string]domain.ParameterBinding{"project": {Kind: domain.BindingConnectionRef, Ref: "$connection.target_project"}}}},
	}, Edges: []domain.FlowEdge{{ID: "edge-issue-parse", FromNodeID: "gitlab-issue-input", FromPortID: "event", ToNodeID: "parse-publication", ToPortID: "event"}, {ID: "edge-parse-map", FromNodeID: "parse-publication", FromPortID: "publication", ToNodeID: "map-issue", ToPortID: "publication"}, {ID: "edge-map-create", FromNodeID: "map-issue", FromPortID: "issue-input", ToNodeID: "multica-create", ToPortID: "issue-input"}}}
}

func archiveGraph() domain.FlowGraph {
	return domain.FlowGraph{Nodes: []domain.FlowNode{
		{ID: "gitlab-push-input", Kind: domain.NodeConnector, Name: "GitLab Push Hook", Outputs: []domain.Port{{ID: "event", Name: "GitLab Push Event", Direction: domain.PortOutput, ModelRef: "provider:gitlab.push.v1"}}, Connector: &domain.ConnectorNode{BehaviorKey: "gitlab.push-hook", BehaviorVersion: "1.0.0"}},
		{ID: "parse-archive", Kind: domain.NodeGeneric, Name: "Parse / Normalize", Inputs: []domain.Port{{ID: "event", Direction: domain.PortInput, ModelRef: "provider:gitlab.push.v1", Required: true}}, Outputs: []domain.Port{{ID: "completion", Direction: domain.PortOutput, ModelRef: "ArchiveCompletion.v1"}}, Generic: &domain.GenericNode{Type: GenericParseNormalize, ParameterBindings: map[string]domain.ParameterBinding{"model": {Kind: domain.BindingFixed, Value: "ArchiveCompletion.v1"}}}},
		{ID: "map-complete", Kind: domain.NodeGeneric, Name: "Build Completion Input", Inputs: []domain.Port{{ID: "completion", Direction: domain.PortInput, ModelRef: "ArchiveCompletion.v1", Required: true}}, Outputs: []domain.Port{{ID: "complete-input", Direction: domain.PortOutput, ModelRef: "MulticaCompleteIssueInput.v1"}}, Generic: &domain.GenericNode{Type: GenericMappingTemplate, ParameterBindings: map[string]domain.ParameterBinding{"model": {Kind: domain.BindingFixed, Value: "MulticaCompleteIssueInput.v1"}}}},
		{ID: "multica-complete", Kind: domain.NodeConnector, Name: "Multica Complete Issue", Inputs: []domain.Port{{ID: "complete-input", Direction: domain.PortInput, ModelRef: "MulticaCompleteIssueInput.v1", Required: true}}, Connector: &domain.ConnectorNode{BehaviorKey: "multica.complete-issue", BehaviorVersion: "1.0.0", ParameterBindings: map[string]domain.ParameterBinding{"project": {Kind: domain.BindingConnectionRef, Ref: "$connection.target_project"}}}},
	}, Edges: []domain.FlowEdge{{ID: "edge-push-parse", FromNodeID: "gitlab-push-input", FromPortID: "event", ToNodeID: "parse-archive", ToPortID: "event"}, {ID: "edge-archive-map", FromNodeID: "parse-archive", FromPortID: "completion", ToNodeID: "map-complete", ToPortID: "completion"}, {ID: "edge-map-complete", FromNodeID: "map-complete", FromPortID: "complete-input", ToNodeID: "multica-complete", ToPortID: "complete-input"}}}
}

func abandonGraph() domain.FlowGraph {
	return domain.FlowGraph{Nodes: []domain.FlowNode{
		{ID: "gitlab-abandon-input", Kind: domain.NodeConnector, Name: "GitLab Abandon Label Hook", Outputs: []domain.Port{{ID: "event", Name: "GitLab Issue Event", Direction: domain.PortOutput, ModelRef: "provider:gitlab.issue.v1"}}, Connector: &domain.ConnectorNode{BehaviorKey: "gitlab.issue-abandon-hook", BehaviorVersion: "1.0.0"}},
		{ID: "parse-lifecycle", Kind: domain.NodeGeneric, Name: "Parse / Normalize", Inputs: []domain.Port{{ID: "event", Direction: domain.PortInput, ModelRef: "provider:gitlab.issue.v1", Required: true}}, Outputs: []domain.Port{{ID: "lifecycle", Direction: domain.PortOutput, ModelRef: "ChangeLifecycle.v1"}}, Generic: &domain.GenericNode{Type: GenericParseNormalize, ParameterBindings: map[string]domain.ParameterBinding{"model": {Kind: domain.BindingFixed, Value: "ChangeLifecycle.v1"}}}},
		{ID: "map-cancel", Kind: domain.NodeGeneric, Name: "Build Cancellation Input", Inputs: []domain.Port{{ID: "lifecycle", Direction: domain.PortInput, ModelRef: "ChangeLifecycle.v1", Required: true}}, Outputs: []domain.Port{{ID: "complete-input", Direction: domain.PortOutput, ModelRef: "MulticaCompleteIssueInput.v1"}}, Config: map[string]any{"mapping": MappingSpec{
			"correlation_id":   {Source: "$input.change_id"},
			"change_id":        {Source: "$input.change_id"},
			"desired_status":   {Constant: "cancelled"},
			"lifecycle_event":  {Constant: "abandoned"},
			"lifecycle_reason": {Source: "$input.lifecycle_reason"},
		}}, Generic: &domain.GenericNode{Type: GenericMappingTemplate, ParameterBindings: map[string]domain.ParameterBinding{"model": {Kind: domain.BindingFixed, Value: "MulticaCompleteIssueInput.v1"}}}},
		{ID: "multica-cancel", Kind: domain.NodeConnector, Name: "Multica Cancel Issue", Inputs: []domain.Port{{ID: "complete-input", Direction: domain.PortInput, ModelRef: "MulticaCompleteIssueInput.v1", Required: true}}, Connector: &domain.ConnectorNode{BehaviorKey: "multica.complete-issue", BehaviorVersion: "1.0.0", ParameterBindings: map[string]domain.ParameterBinding{"project": {Kind: domain.BindingConnectionRef, Ref: "$connection.target_project"}}}},
	}, Edges: []domain.FlowEdge{{ID: "edge-abandon-parse", FromNodeID: "gitlab-abandon-input", FromPortID: "event", ToNodeID: "parse-lifecycle", ToPortID: "event"}, {ID: "edge-lifecycle-map", FromNodeID: "parse-lifecycle", FromPortID: "lifecycle", ToNodeID: "map-cancel", ToPortID: "lifecycle"}, {ID: "edge-map-cancel", FromNodeID: "map-cancel", FromPortID: "complete-input", ToNodeID: "multica-cancel", ToPortID: "complete-input"}}}
}
