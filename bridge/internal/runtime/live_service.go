package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"specwire/bridge/internal/domain"
	"specwire/bridge/internal/flow"
	"specwire/bridge/internal/security"
)

// LiveTestRequest deliberately makes the side-effect boundary explicit.  A
// live test is not the same operation as draft simulation: it creates a real
// queued execution and may invoke the configured provider adapters.
type LiveTestRequest struct {
	SampleEvent        map[string]any
	ConfirmSideEffects bool
	FlowVersion        int
}

// LiveTestService creates a durable, one-off execution for an already
// published FlowVersion.  It does not execute the provider call itself; the
// normal Worker owns execution, retries, checkpoints and observability.
type LiveTestService struct {
	store     Store
	catalog   flow.Catalog
	resolver  flow.CatalogResolver
	now       func() time.Time
	retention time.Duration
}

type LiveTestOption func(*LiveTestService)

func WithLiveTestCatalogResolver(resolver flow.CatalogResolver) LiveTestOption {
	return func(service *LiveTestService) { service.resolver = resolver }
}

func WithLiveTestRetention(value time.Duration) LiveTestOption {
	return func(service *LiveTestService) {
		if value > 0 {
			service.retention = value
		}
	}
}

func NewLiveTestService(store Store, catalog flow.Catalog, options ...LiveTestOption) (*LiveTestService, error) {
	if store == nil {
		return nil, invalid("live test store is required")
	}
	service := &LiveTestService{store: store, catalog: catalog, now: time.Now, retention: defaultRetention}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

func (s *LiveTestService) catalogForWorkspace(ctx context.Context, workspaceID domain.ID) (flow.Catalog, error) {
	if s.resolver != nil {
		return s.resolver.CatalogForWorkspace(ctx, workspaceID)
	}
	return s.catalog, nil
}

// Start validates the active/pinned published version, creates a redacted
// provider event and queues the execution atomically.  The returned
// FlowExecution is the durable record, not a claim that the provider call has
// completed.
func (s *LiveTestService) Start(ctx context.Context, workspaceID, flowID domain.ID, request LiveTestRequest) (domain.FlowExecution, error) {
	if !request.ConfirmSideEffects {
		return domain.FlowExecution{}, fmt.Errorf("%w: live test requires confirm_side_effects=true", domain.ErrInvalid)
	}
	flowRecord, err := s.store.GetFlow(ctx, workspaceID, flowID)
	if err != nil {
		return domain.FlowExecution{}, err
	}
	if flowRecord.Status != domain.FlowPublished {
		return domain.FlowExecution{}, fmt.Errorf("%w: live test requires a published Flow", domain.ErrConflict)
	}
	versionNumber := request.FlowVersion
	if versionNumber <= 0 {
		versionNumber = flowRecord.ActiveVersion
	}
	if versionNumber <= 0 {
		return domain.FlowExecution{}, fmt.Errorf("%w: published Flow has no active version", domain.ErrConflict)
	}
	version, err := s.store.GetFlowVersion(ctx, workspaceID, flowID, versionNumber)
	if err != nil {
		return domain.FlowExecution{}, err
	}
	if version.Status != domain.FlowPublished {
		return domain.FlowExecution{}, fmt.Errorf("%w: live test requires a published FlowVersion", domain.ErrConflict)
	}
	connection, err := s.store.GetConnection(ctx, workspaceID, flowRecord.ConnectionID)
	if err != nil {
		return domain.FlowExecution{}, err
	}
	catalog, err := s.catalogForWorkspace(ctx, workspaceID)
	if err != nil {
		return domain.FlowExecution{}, err
	}
	inputNode, behavior, ok := liveTestInputNode(version.Graph, catalog)
	if !ok {
		return domain.FlowExecution{}, fmt.Errorf("%w: published Flow has no registered input ConnectorNode", domain.ErrInvalid)
	}

	identity := domain.NewID()
	payload, err := liveTestPayload(request.SampleEvent, behavior.Key, connection, identity)
	if err != nil {
		return domain.FlowExecution{}, err
	}
	// The event is a user-supplied sample, but it enters the same durable
	// payload path as a webhook and therefore receives the same redaction
	// boundary before it can be inspected through the API.
	redacted, ok := security.RedactValue(payload).(map[string]any)
	if !ok {
		return domain.FlowExecution{}, fmt.Errorf("%w: live test sample must be a JSON object", domain.ErrInvalid)
	}
	receivedAt := s.now().UTC()
	retentionUntil := receivedAt.Add(s.retention)
	deliveryID := "live-test:" + string(identity)
	event := domain.InboundEvent{
		ID:                      domain.NewID(),
		WorkspaceID:             workspaceID,
		ConnectionID:            connection.ID,
		Provider:                domain.ProviderGitLab,
		SourceInstanceID:        connection.SourceGitLabProject.InstanceID,
		SourceProjectExternalID: connection.SourceGitLabProject.ExternalID,
		BehaviorKey:             behavior.Key,
		BehaviorVersion:         behavior.Version,
		DeliveryID:              deliveryID,
		Payload:                 redacted,
		ReceivedAt:              receivedAt,
		RetentionUntil:          &retentionUntil,
	}
	execution := domain.FlowExecution{
		ID:             identity,
		WorkspaceID:    workspaceID,
		ConnectionID:   connection.ID,
		FlowID:         flowID,
		FlowVersionID:  version.ID,
		FlowVersion:    version.Version,
		EventID:        event.ID,
		DeliveryID:     deliveryID,
		IdempotencyKey: "live-test:" + string(identity),
		CorrelationID:  "live-test:" + string(identity),
		Status:         domain.ExecutionQueued,
		CurrentNodeID:  inputNode.ID,
	}
	job := domain.Job{ID: domain.NewID(), WorkspaceID: workspaceID, Kind: JobKindFlowExecute, Payload: map[string]any{
		"execution_id":  execution.ID,
		"connection_id": connection.ID,
	}}
	created, isNew, err := s.store.AcceptInboundEvent(ctx, event, execution, job)
	if err != nil {
		return domain.FlowExecution{}, err
	}
	if !isNew {
		return created, nil
	}
	return created, nil
}

func liveTestInputNode(graph domain.FlowGraph, catalog flow.Catalog) (domain.FlowNode, domain.ConnectorBehavior, bool) {
	for _, node := range graph.Nodes {
		if node.Kind != domain.NodeConnector || node.Connector == nil {
			continue
		}
		behavior, ok := catalog.Behavior(node.Connector.BehaviorKey, node.Connector.BehaviorVersion)
		if ok && behavior.Direction == domain.DirectionInput {
			return node, behavior, true
		}
	}
	return domain.FlowNode{}, domain.ConnectorBehavior{}, false
}

func liveTestPayload(sample map[string]any, behaviorKey string, connection domain.Connection, identity domain.ID) (map[string]any, error) {
	if len(sample) != 0 {
		return cloneMap(sample), nil
	}
	key := strings.ToLower(behaviorKey)
	if strings.Contains(key, "issue") {
		changeID := "LIVE-" + strings.ToUpper(shortIdentity(identity))
		return map[string]any{
			"object_kind": "issue",
			"object_attributes": map[string]any{
				"iid":         1,
				"action":      "open",
				"description": "change_id: " + changeID + "\nbranch: live-test\nbranch_head_sha: " + shortIdentity(identity),
				"labels":      []any{map[string]any{"title": "change"}},
			},
			"project": map[string]any{
				"id":                  connection.SourceGitLabProject.ExternalID,
				"path_with_namespace": connection.SourceGitLabProject.FullPath,
			},
		}, nil
	}
	if strings.Contains(key, "push") || strings.Contains(key, "archive") {
		changeID := "LIVE-" + strings.ToUpper(shortIdentity(identity))
		// Keep the default event close to the payload accepted from GitLab and
		// include the fields normally added by ingress. The archive parser uses
		// the enriched fields to identify the existing projection and provider
		// delivery, while the commit trailer makes the sample recognizable as an
		// archived Push Hook when it is inspected through the execution detail.
		return map[string]any{
			"object_kind": "push",
			"ref":         "refs/heads/main",
			"after":       shortIdentity(identity),
			"head_commit": map[string]any{
				"id":      shortIdentity(identity),
				"message": "SpecWire-Event: archived\nSpecWire-Change: " + changeID,
			},
			"commits": []any{},
			"project": map[string]any{
				"id":                  connection.SourceGitLabProject.ExternalID,
				"path_with_namespace": connection.SourceGitLabProject.FullPath,
			},
			"change_id":            changeID,
			"provider_delivery_id": "live-test:" + string(identity),
			"lifecycle_event":      "archived",
		}, nil
	}
	return nil, fmt.Errorf("%w: sample_event is required for input behavior %s", domain.ErrInvalid, behaviorKey)
}

func shortIdentity(identity domain.ID) string {
	value := strings.ReplaceAll(string(identity), "-", "")
	if len(value) > 12 {
		return value[:12]
	}
	return value
}
