package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/provider"
	"specwire/bridge/internal/security"
)

const (
	defaultJobLease       = 2 * time.Minute
	defaultWorkerPoll     = 200 * time.Millisecond
	defaultWorkerBackoff  = 5 * time.Second
	defaultRetentionSweep = time.Hour
	maxAutomaticAttempts  = 5
)

// Execute runs one immutable FlowVersion.  Transformations are interpreted
// from the stored graph, while provider calls are restricted to the adapter
// interfaces supplied to the executor.
func (e *Executor) Execute(ctx context.Context, workspaceID, executionID domain.ID) error {
	return e.ExecuteInWorkspace(ctx, workspaceID, executionID)
}

func (e *Executor) ExecuteInWorkspace(ctx context.Context, workspaceID, executionID domain.ID) error {
	execution, err := e.store.GetFlowExecution(ctx, workspaceID, executionID)
	if err != nil {
		return err
	}
	if execution.Status == domain.ExecutionSucceeded || execution.Status == domain.ExecutionSkipped {
		return nil
	}
	event, err := e.store.GetInboundEvent(ctx, workspaceID, execution.EventID)
	if err != nil {
		return e.failExecution(ctx, execution, "storage", err)
	}
	version, err := e.store.GetFlowVersion(ctx, workspaceID, execution.FlowID, execution.FlowVersion)
	if err != nil {
		return e.failExecution(ctx, execution, "storage", err)
	}
	connection, err := e.store.GetConnection(ctx, workspaceID, execution.ConnectionID)
	if err != nil {
		return e.failExecution(ctx, execution, "storage", err)
	}
	catalog, err := e.catalogForWorkspace(ctx, workspaceID)
	if err != nil {
		return e.failExecution(ctx, execution, "registry", err)
	}

	nodes := make(map[domain.ID]domain.FlowNode, len(version.Graph.Nodes))
	for _, node := range version.Graph.Nodes {
		nodes[node.ID] = node
	}
	order, err := executionOrder(version.Graph)
	if err != nil {
		return e.failExecution(ctx, execution, "validation", err)
	}
	inputID := inputNodeID(version.Graph, catalog)
	if inputID.Empty() {
		return e.failExecution(ctx, execution, "validation", fmt.Errorf("%w: input ConnectorNode is missing", domain.ErrInvalid))
	}
	latest, err := latestNodeExecutions(ctx, e.store, workspaceID, execution.ID)
	if err != nil {
		return e.failExecution(ctx, execution, "storage", err)
	}

	if execution.Status != domain.ExecutionRunning {
		execution.Status = domain.ExecutionRunning
		execution.ErrorCategory = ""
		execution.ErrorMessage = ""
		if err := e.store.UpdateFlowExecution(ctx, execution); err != nil {
			return err
		}
	}

	inputs := map[domain.ID]map[string]any{inputID: {"event": event.Payload}}
	active := map[domain.ID]bool{inputID: true}
	ctxValue := flow.RuntimeContext{WorkspaceID: workspaceID, ConnectionID: connection.ID, FlowExecutionID: execution.ID, EventID: event.ID, SourceProject: connection.SourceGitLabProject.FullPath, TargetProject: connection.TargetMulticaProject.ExternalID, TargetRef: "refs/heads/main"}
	outputCount := 0
	for _, nodeID := range order {
		node := nodes[nodeID]
		if !active[nodeID] {
			continue
		}
		execution.CurrentNodeID = nodeID
		if err := e.store.UpdateFlowExecution(ctx, execution); err != nil {
			return err
		}
		input := firstNodeInput(node, inputs[nodeID])
		if existing, ok := latest[nodeID]; ok && (existing.Status == domain.NodeSucceeded || existing.Status == domain.NodeSkipped) && node.Kind == domain.NodeConnector && node.Connector != nil {
			// A successful output is retained as the checkpoint.  This matters
			// when a later repair only needs to re-run a safe bookkeeping step.
			if existing.Status == domain.NodeSkipped {
				continue
			}
			if value, ok := existing.OutputSnapshot["value"]; ok {
				propagate(version.Graph, nodeID, value, inputs, active)
			}
			outputCount++
			continue
		}

		attempt := 1
		if existing, ok := latest[nodeID]; ok {
			attempt = existing.Attempt + 1
		}
		retentionUntil := e.now().UTC().Add(e.retention)
		nodeExecution := domain.NodeExecution{ID: domain.NewID(), WorkspaceID: workspaceID, ExecutionID: execution.ID, NodeID: nodeID, Status: domain.NodeRunning, Attempt: attempt, InputSnapshot: redactedMap(input), RetentionUntil: &retentionUntil}
		if err := e.store.CreateNodeExecution(ctx, nodeExecution); err != nil {
			return e.failExecution(ctx, execution, "storage", err)
		}
		value, nodeErr := e.executeNode(ctx, node, input, event, execution, connection, ctxValue, catalog)
		if nodeErr != nil {
			category, status := classifyNodeError(nodeErr)
			nodeExecution.Status = status
			nodeExecution.ErrorCategory = category
			nodeExecution.ErrorMessage = safeError(nodeErr)
			if updateErr := e.store.UpdateNodeExecution(ctx, nodeExecution); updateErr != nil {
				return updateErr
			}
			if status == domain.NodeSkipped {
				execution.Status = domain.ExecutionSkipped
				execution.ErrorCategory = category
				execution.ErrorMessage = safeError(nodeErr)
				_ = e.store.UpdateFlowExecution(ctx, execution)
				return nil
			}
			execution.ErrorCategory = category
			execution.ErrorMessage = safeError(nodeErr)
			if status == domain.NodeIndeterminate {
				execution.Status = domain.ExecutionReconciliationNeeded
			} else {
				execution.Status = domain.ExecutionFailed
			}
			_ = e.store.UpdateFlowExecution(ctx, execution)
			return nodeErr
		}
		nodeExecution.Status = domain.NodeSucceeded
		nodeExecution.OutputSnapshot = redactedMap(map[string]any{"value": value})
		if result, ok := value.(map[string]any); ok {
			if requestID := stringValue(result["provider_request_id"]); requestID != "" {
				nodeExecution.ProviderRequestID = requestID
				execution.ProviderRequestIDs = appendUnique(execution.ProviderRequestIDs, requestID)
			}
		}
		if err := e.store.UpdateNodeExecution(ctx, nodeExecution); err != nil {
			return err
		}
		propagate(version.Graph, nodeID, value, inputs, active)
		if node.Kind == domain.NodeConnector && node.Connector != nil {
			behavior, ok := catalog.Behavior(node.Connector.BehaviorKey, node.Connector.BehaviorVersion)
			if ok && behavior.Direction == domain.DirectionOutput {
				outputCount++
			}
		}
	}
	if outputCount == 0 {
		return e.failExecution(ctx, execution, "validation", fmt.Errorf("%w: Flow did not reach an output ConnectorNode", domain.ErrInvalid))
	}
	execution.Status = domain.ExecutionSucceeded
	execution.ErrorCategory = ""
	execution.ErrorMessage = ""
	return e.store.UpdateFlowExecution(ctx, execution)
}

func (e *Executor) executeNode(ctx context.Context, node domain.FlowNode, input map[string]any, event domain.InboundEvent, execution domain.FlowExecution, connection domain.Connection, runtimeContext flow.RuntimeContext, catalog flow.Catalog) (any, error) {
	switch node.Kind {
	case domain.NodeConnector:
		if node.Connector == nil {
			return nil, fmt.Errorf("%w: connector node configuration is missing", domain.ErrInvalid)
		}
		behavior, ok := catalog.Behavior(node.Connector.BehaviorKey, node.Connector.BehaviorVersion)
		if !ok {
			return nil, fmt.Errorf("%w: connector behavior %s@%s is not registered", domain.ErrInvalid, node.Connector.BehaviorKey, node.Connector.BehaviorVersion)
		}
		if behavior.Direction == domain.DirectionInput {
			return input["event"], nil
		}
		return e.executeOutput(ctx, behavior, node, input, event, execution, connection, runtimeContext)
	case domain.NodeGeneric:
		if node.Generic == nil {
			return nil, fmt.Errorf("%w: generic node configuration is missing", domain.ErrInvalid)
		}
		switch node.Generic.Type {
		case flow.GenericParseNormalize:
			model := fixedString(node.Generic.ParameterBindings, "model")
			if model == "" {
				return nil, fmt.Errorf("%w: Parse/Normalize model is required", domain.ErrInvalid)
			}
			return flow.ParseNormalizeWithCatalog(input, model, runtimeContext, catalog)
		case flow.GenericMappingTemplate:
			model := fixedString(node.Generic.ParameterBindings, "model")
			if model == "" {
				return nil, fmt.Errorf("%w: Mapping/Template target model is required", domain.ErrInvalid)
			}
			mapping, err := mappingForNode(node, model)
			if err != nil {
				return nil, err
			}
			output, err := flow.ApplyMapping(input, mapping, runtimeContext)
			if err != nil {
				return nil, err
			}
			if (model == "MulticaCreateIssueInput.v1" || model == "MulticaCreateIssueInput@v1") && isBlank(output["description"]) {
				output["description"] = buildIssueDescription(connection, input)
			}
			if err := validateModelValue(catalog, model, output); err != nil {
				return nil, err
			}
			return output, nil
		case flow.GenericConditionFilter:
			filter, err := filterForNode(node)
			if err != nil {
				return nil, err
			}
			matched, err := flow.EvaluateFilter(input, filter)
			if err != nil {
				return nil, err
			}
			if !matched {
				return nil, &skipError{}
			}
			return input, nil
		default:
			return nil, fmt.Errorf("%w: unsupported generic node %s", domain.ErrInvalid, node.Generic.Type)
		}
	case domain.NodeTerminal:
		return input, &skipError{}
	default:
		return nil, fmt.Errorf("%w: unsupported node kind %s", domain.ErrInvalid, node.Kind)
	}
}

func (e *Executor) executeOutput(ctx context.Context, behavior domain.ConnectorBehavior, node domain.FlowNode, input map[string]any, event domain.InboundEvent, execution domain.FlowExecution, connection domain.Connection, runtimeContext flow.RuntimeContext) (map[string]any, error) {
	instance, err := e.store.GetMulticaInstance(ctx, connection.WorkspaceID, connection.TargetMulticaProject.InstanceID)
	if err != nil {
		return nil, err
	}
	projectID := connection.TargetMulticaProject.ExternalID
	if binding, ok := node.Connector.ParameterBindings["project"]; ok {
		resolved, err := resolveConnectorBinding(binding, connection, runtimeContext)
		if err != nil {
			return nil, err
		}
		if text := stringValue(resolved); text != "" {
			projectID = text
		}
	}
	switch behavior.Key {
	case "multica.create-issue":
		return e.createIssue(ctx, instance, projectID, input, execution, connection)
	case "multica.complete-issue":
		return e.completeIssue(ctx, instance, input, event, execution, connection)
	default:
		return nil, fmt.Errorf("%w: output behavior %s is not implemented", domain.ErrInvalid, behavior.Key)
	}
}

func (e *Executor) createIssue(ctx context.Context, instance domain.MulticaInstance, projectID string, input map[string]any, execution domain.FlowExecution, connection domain.Connection) (map[string]any, error) {
	changeID := stringValue(input["change_id"])
	if changeID == "" {
		return nil, fmt.Errorf("%w: Multica Create Issue requires change_id", domain.ErrInvalid)
	}
	if existing, err := e.store.GetCorrelation(ctx, connection.WorkspaceID, connection.ID, connection.SourceGitLabProject.ExternalID, changeID); err == nil {
		return map[string]any{
			"issue_id":            existing.TargetIdentity,
			"project_id":          projectID,
			"provider_request_id": existing.ProviderRequestID,
			"correlation_id":      execution.CorrelationID,
			"deduplicated":        true,
		}, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	title := firstNonEmpty(stringValue(input["title"]), "[SpecWire] "+changeID)
	status := firstNonEmpty(stringValue(input["status"]), "backlog")
	if status != "backlog" && status != "todo" {
		return nil, fmt.Errorf("%w: unsupported Multica status %q", domain.ErrInvalid, status)
	}
	description := firstNonEmpty(stringValue(input["description"]), buildIssueDescription(connection, input))
	result, err := e.multica.CreateIssue(ctx, instance, provider.IssueInput{ProjectID: projectID, Title: title, Description: description, Status: status, Assignee: stringValue(input["assignee"]), IdempotencyKey: execution.IdempotencyKey}, nil)
	if err != nil {
		e.recordProviderEffect(ctx, execution, "provider.multica.issue.create", "failed", "", projectID, err)
		return nil, err
	}
	e.recordProviderEffect(ctx, execution, "provider.multica.issue.create", "succeeded", result.RequestID, projectID, nil)
	if result.IssueID == "" {
		return nil, fmt.Errorf("%w: Multica adapter returned no issue ID", domain.ErrInvalid)
	}
	issueIID := intValue(input["issue_iid"])
	change := domain.Correlation{ID: domain.NewID(), WorkspaceID: connection.WorkspaceID, ConnectionID: connection.ID, SourceIdentity: connection.SourceGitLabProject.ExternalID, SourceIssueIID: issueIID, SourceIssueIIDs: []int{issueIID}, PublicationIdentity: changeID, TargetIdentity: result.IssueID, FlowExecutionID: execution.ID, ProviderRequestID: result.RequestID}
	if _, err := e.store.UpsertCorrelation(ctx, change); err != nil {
		return nil, err
	}
	return map[string]any{"issue_id": result.IssueID, "project_id": firstNonEmpty(result.ProjectID, projectID), "provider_request_id": result.RequestID, "correlation_id": execution.CorrelationID}, nil
}

func (e *Executor) completeIssue(ctx context.Context, instance domain.MulticaInstance, input map[string]any, event domain.InboundEvent, execution domain.FlowExecution, connection domain.Connection) (map[string]any, error) {
	changeID := stringValue(input["change_id"])
	if changeID == "" {
		return nil, fmt.Errorf("%w: Multica Complete Issue requires change_id", domain.ErrInvalid)
	}
	correlation, err := e.store.GetCorrelation(ctx, connection.WorkspaceID, connection.ID, connection.SourceGitLabProject.ExternalID, changeID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, &reconciliationError{err: fmt.Errorf("%w: correlated projection for %s is unavailable: %v", domain.ErrNotFound, changeID, err)}
		}
		return nil, err
	}
	status := firstNonEmpty(stringValue(input["desired_status"]), "done")
	if status != "done" {
		return nil, fmt.Errorf("%w: archive completion status must be done", domain.ErrInvalid)
	}
	result, err := e.multica.SetIssueStatus(ctx, instance, correlation.TargetIdentity, status, nil)
	if err != nil {
		e.recordProviderEffect(ctx, execution, "provider.multica.issue.status", "failed", "", correlation.TargetIdentity, err)
		return nil, err
	}
	e.recordProviderEffect(ctx, execution, "provider.multica.issue.status", "succeeded", result.RequestID, correlation.TargetIdentity, nil)
	out := map[string]any{"issue_id": correlation.TargetIdentity, "status": result.Status, "provider_request_id": result.RequestID, "correlation_id": execution.CorrelationID}
	issueIIDs := append([]int(nil), correlation.SourceIssueIIDs...)
	if len(issueIIDs) == 0 && correlation.SourceIssueIID > 0 {
		issueIIDs = []int{correlation.SourceIssueIID}
	}
	if e.credentials == nil || len(issueIIDs) == 0 {
		out["gitlab_issue_close"] = "skipped"
		return out, nil
	}
	gitlabInstance, err := e.store.GetGitLabInstance(ctx, connection.WorkspaceID, connection.SourceGitLabProject.InstanceID)
	if err != nil {
		return nil, err
	}
	credential, cleanup, err := e.credentials.ResolveForConnection(ctx, connection)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	project := provider.GitLabProject{InstanceID: connection.SourceGitLabProject.InstanceID, ExternalID: connection.SourceGitLabProject.ExternalID, FullPath: connection.SourceGitLabProject.FullPath, Name: connection.SourceGitLabProject.Name, WebURL: connection.SourceGitLabProject.WebURL, SSHURL: connection.SourceGitLabProject.SSHURL, HTTPSURL: connection.SourceGitLabProject.HTTPSURL}
	closed := 0
	for _, issueIID := range issueIIDs {
		if err := e.gitlab.CloseIssue(ctx, gitlabInstance, project, issueIID, credential); err != nil {
			e.recordProviderEffect(ctx, execution, "provider.gitlab.issue.close", "failed", "", strconv.Itoa(issueIID), err)
			return nil, &reconciliationError{err: fmt.Errorf("close GitLab publication Issue %d: %w", issueIID, err)}
		}
		e.recordProviderEffect(ctx, execution, "provider.gitlab.issue.close", "succeeded", "", strconv.Itoa(issueIID), nil)
		closed++
	}
	out["gitlab_issue_close"] = "closed"
	out["gitlab_issues_closed"] = closed
	_ = event
	return out, nil
}

type skipError struct{}

func (e *skipError) Error() string { return "condition did not match" }

type reconciliationError struct{ err error }

func (e *reconciliationError) Error() string { return e.err.Error() }
func (e *reconciliationError) Unwrap() error { return e.err }

func classifyNodeError(err error) (string, domain.NodeExecutionStatus) {
	var skip *skipError
	if errors.As(err, &skip) {
		return "skipped", domain.NodeSkipped
	}
	var reconciliation *reconciliationError
	if errors.As(err, &reconciliation) {
		return "reconciliation-required", domain.NodeIndeterminate
	}
	var providerError *provider.ProviderError
	if errors.As(err, &providerError) {
		if providerError.Category == provider.ErrorIndeterminate || providerError.Category == provider.ErrorTimeout {
			return string(providerError.Category), domain.NodeIndeterminate
		}
		return string(providerError.Category), domain.NodeFailed
	}
	if errors.Is(err, domain.ErrInvalid) {
		return "validation", domain.NodeFailed
	}
	if errors.Is(err, domain.ErrNotFound) {
		return "not-found", domain.NodeFailed
	}
	return "internal", domain.NodeFailed
}

func (e *Executor) failExecution(ctx context.Context, execution domain.FlowExecution, category string, err error) error {
	execution.Status = domain.ExecutionFailed
	execution.ErrorCategory = category
	execution.ErrorMessage = safeError(err)
	_ = e.store.UpdateFlowExecution(ctx, execution)
	return err
}

func executionOrder(graph domain.FlowGraph) ([]domain.ID, error) {
	indegree := map[domain.ID]int{}
	adjacency := map[domain.ID][]domain.ID{}
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

func firstNodeInput(node domain.FlowNode, inputs map[string]any) map[string]any {
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

func propagate(graph domain.FlowGraph, nodeID domain.ID, value any, inputs map[domain.ID]map[string]any, active map[domain.ID]bool) {
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

func latestNodeExecutions(ctx context.Context, store Store, workspaceID, executionID domain.ID) (map[domain.ID]domain.NodeExecution, error) {
	items, err := store.ListNodeExecutions(ctx, workspaceID, executionID)
	if err != nil {
		return nil, err
	}
	result := map[domain.ID]domain.NodeExecution{}
	for _, item := range items {
		if current, ok := result[item.NodeID]; !ok || item.Attempt > current.Attempt {
			result[item.NodeID] = item
		}
	}
	return result, nil
}

func mappingForNode(node domain.FlowNode, model string) (flow.MappingSpec, error) {
	if node.Config != nil {
		if raw, ok := node.Config["mapping"]; ok {
			return decodeMapping(raw)
		}
	}
	if binding, ok := node.Generic.ParameterBindings["mapping"]; ok && binding.Value != nil {
		return decodeMapping(binding.Value)
	}
	if mapping, ok := flow.DefaultMappingForModel(model); ok {
		return mapping, nil
	}
	return nil, fmt.Errorf("%w: no default mapping for model %s", domain.ErrInvalid, model)
}

func decodeMapping(raw any) (flow.MappingSpec, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid mapping definition", domain.ErrInvalid)
	}
	var mapping flow.MappingSpec
	if err := json.Unmarshal(encoded, &mapping); err != nil {
		return nil, fmt.Errorf("%w: invalid mapping definition", domain.ErrInvalid)
	}
	return mapping, nil
}

func filterForNode(node domain.FlowNode) (flow.Filter, error) {
	if node.Config == nil {
		return flow.Filter{}, fmt.Errorf("%w: Condition/Filter definition is required", domain.ErrInvalid)
	}
	raw, ok := node.Config["filter"]
	if !ok {
		return flow.Filter{}, fmt.Errorf("%w: Condition/Filter definition is required", domain.ErrInvalid)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return flow.Filter{}, fmt.Errorf("%w: invalid filter definition", domain.ErrInvalid)
	}
	var filter flow.Filter
	if err := json.Unmarshal(encoded, &filter); err != nil || strings.TrimSpace(filter.Op) == "" {
		return flow.Filter{}, fmt.Errorf("%w: invalid filter definition", domain.ErrInvalid)
	}
	return filter, nil
}

func fixedString(bindings map[string]domain.ParameterBinding, name string) string {
	binding, ok := bindings[name]
	if !ok || binding.Kind != domain.BindingFixed {
		return ""
	}
	return stringValue(binding.Value)
}

func resolveConnectorBinding(binding domain.ParameterBinding, connection domain.Connection, runtimeContext flow.RuntimeContext) (any, error) {
	switch binding.Kind {
	case domain.BindingConnectionRef:
		if binding.Ref == "$connection.target_project" {
			return connection.TargetMulticaProject.ExternalID, nil
		}
		if binding.Ref == "$connection.source_project" {
			return connection.SourceGitLabProject.ExternalID, nil
		}
		return nil, fmt.Errorf("%w: unsupported Connection reference %s", domain.ErrInvalid, binding.Ref)
	case domain.BindingFixed:
		return binding.Value, nil
	case domain.BindingRuntimeRef:
		if binding.Ref == "$runtime.flow_execution_id" {
			return runtimeContext.FlowExecutionID, nil
		}
		return nil, fmt.Errorf("%w: unsupported runtime reference %s", domain.ErrInvalid, binding.Ref)
	default:
		return nil, fmt.Errorf("%w: connector project binding must be a safe reference", domain.ErrInvalid)
	}
}

func validateModelValue(catalog flow.Catalog, model string, value map[string]any) error {
	return flow.ValidateModelValue(catalog, model, value)
}

func redactedMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	redacted, ok := security.RedactValue(value).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return redacted
}

func (e *Executor) recordProviderEffect(ctx context.Context, execution domain.FlowExecution, action, outcome, requestID, externalID string, effectErr error) {
	recorder, ok := e.store.(AuditRecorder)
	if !ok {
		return
	}
	payload := map[string]any{"workspace_id": execution.WorkspaceID, "flow_id": execution.FlowID, "flow_version": execution.FlowVersion, "provider_request_id": requestID, "external_id": externalID, "outcome": outcome}
	if effectErr != nil {
		payload["error"] = safeError(effectErr)
	}
	_ = recorder.CreateAuditEvent(ctx, domain.AuditEvent{ID: domain.NewID(), WorkspaceID: execution.WorkspaceID, Action: action, EntityType: "flow_execution", EntityID: execution.ID, Payload: payload})
}

func buildIssueDescription(connection domain.Connection, input map[string]any) string {
	return fmt.Sprintf("[SpecWire Backlog] 由 GitLab change Issue 自动创建。\n\nrepository: %s\nchange_id: %s\nbranch: %s\nbranch_head_sha: %s\ntarget_branch: %s\n", connection.SourceGitLabProject.FullPath, stringValue(input["change_id"]), stringValue(input["branch"]), stringValue(input["branch_head_sha"]), strings.TrimPrefix(firstNonEmpty(stringValue(input["target_ref"]), "refs/heads/main"), "refs/heads/"))
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isBlank(value any) bool { return value == nil || stringValue(value) == "" }

func safeError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 1000 {
		return message[:1000]
	}
	return message
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// Worker polls the durable queue with bounded concurrency.  SQLite's lease
// makes a process restart safe; the per-Connection mutex keeps related work
// ordered while allowing unrelated Connections to run concurrently.
type Worker struct {
	store          Store
	executor       *Executor
	workerID       string
	lease          time.Duration
	poll           time.Duration
	concurrency    int
	retentionSweep time.Duration
	locks          sync.Map
}

type WorkerOption func(*Worker)

func WithWorkerLease(value time.Duration) WorkerOption {
	return func(w *Worker) {
		if value > 0 {
			w.lease = value
		}
	}
}

func WithWorkerPoll(value time.Duration) WorkerOption {
	return func(w *Worker) {
		if value > 0 {
			w.poll = value
		}
	}
}

func WithWorkerConcurrency(value int) WorkerOption {
	return func(w *Worker) {
		if value > 0 {
			w.concurrency = value
		}
	}
}

func WithWorkerRetentionSweep(value time.Duration) WorkerOption {
	return func(w *Worker) {
		if value > 0 {
			w.retentionSweep = value
		}
	}
}

func NewWorker(store Store, executor *Executor, workerID string, options ...WorkerOption) (*Worker, error) {
	if store == nil || executor == nil || strings.TrimSpace(workerID) == "" {
		return nil, invalid("worker dependencies and worker ID are required")
	}
	worker := &Worker{store: store, executor: executor, workerID: workerID, lease: defaultJobLease, poll: defaultWorkerPoll, concurrency: 2, retentionSweep: defaultRetentionSweep}
	for _, option := range options {
		option(worker)
	}
	return worker, nil
}

func (w *Worker) Run(ctx context.Context) error {
	semaphore := make(chan struct{}, w.concurrency)
	var wait sync.WaitGroup
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	retentionTicker := time.NewTicker(w.retentionSweep)
	defer retentionTicker.Stop()
	defer wait.Wait()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-retentionTicker.C:
			if retention, ok := w.store.(RetentionStore); ok {
				if cleared, err := retention.PurgeExpiredRuntimePayloads(ctx, time.Now().UTC()); err != nil {
					// Retention is hygiene, not a reason to stop delivery. The
					// next sweep can retry after a transient database failure.
					slog.Warn("runtime retention sweep failed", "error", err)
				} else if cleared > 0 {
					slog.Info("runtime retention sweep cleared snapshots", "count", cleared)
				}
			}
		case <-ticker.C:
			select {
			case semaphore <- struct{}{}:
			default:
				continue
			}
			job, err := w.store.ClaimNextJob(ctx, w.workerID, w.lease)
			if err != nil {
				<-semaphore
				if errors.Is(err, domain.ErrNotFound) {
					continue
				}
				return err
			}
			wait.Add(1)
			go func(job domain.Job) {
				defer wait.Done()
				defer func() { <-semaphore }()
				w.runJob(ctx, job)
			}(job)
		}
	}
}

func (w *Worker) runJob(ctx context.Context, job domain.Job) {
	connectionID := stringValue(job.Payload["connection_id"])
	var lock *sync.Mutex
	if connectionID != "" {
		value, _ := w.locks.LoadOrStore(connectionID, &sync.Mutex{})
		lock = value.(*sync.Mutex)
		lock.Lock()
		defer lock.Unlock()
	}
	executionID := stringValue(job.Payload["execution_id"])
	if executionID == "" {
		_ = w.store.FailJob(ctx, job.WorkspaceID, job.ID, w.workerID, nil, "job payload has no execution_id")
		return
	}
	err := w.executor.ExecuteInWorkspace(ctx, job.WorkspaceID, domain.ID(executionID))
	if err == nil {
		_ = w.store.CompleteJob(ctx, job.WorkspaceID, job.ID, w.workerID)
		return
	}
	if isRetryableExecutionError(err) && job.AttemptCount < maxAutomaticAttempts {
		next := time.Now().UTC().Add(time.Duration(job.AttemptCount) * defaultWorkerBackoff)
		_ = w.store.FailJob(ctx, job.WorkspaceID, job.ID, w.workerID, &next, safeError(err))
		return
	}
	_ = w.store.FailJob(ctx, job.WorkspaceID, job.ID, w.workerID, nil, safeError(err))
}

func isRetryableExecutionError(err error) bool {
	var providerError *provider.ProviderError
	if errors.As(err, &providerError) {
		return providerError.Retryable()
	}
	var reconciliation *reconciliationError
	return errors.As(err, &reconciliation)
}
