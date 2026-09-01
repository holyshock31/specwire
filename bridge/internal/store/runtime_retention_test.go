package store

import (
	"context"
	"testing"
	"time"

	"specwire/bridge/internal/domain"
)

func TestPurgeExpiredRuntimePayloadsKeepsRuntimeMetadata(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	workspaceID := domain.ID("workspace-retention")
	testWorkspace(t, s, workspaceID)
	testEndpoints(t, s, workspaceID, "gitlab-retention", "multica-retention", "retention")
	connection := testConnection(workspaceID, "connection-retention", "gitlab-retention", "multica-retention", "101", "target")
	if err := s.CreateConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveInboundEvent(ctx, domain.InboundEvent{
		ID:                      "event-retention",
		WorkspaceID:             workspaceID,
		ConnectionID:            connection.ID,
		Provider:                domain.ProviderGitLab,
		SourceInstanceID:        "gitlab-retention",
		SourceProjectExternalID: "101",
		BehaviorKey:             "gitlab.issue-hook",
		BehaviorVersion:         "1.0.0",
		DeliveryID:              "delivery-retention",
		Payload:                 map[string]any{"description": "sensitive but already redacted", "token": "[REDACTED]"},
		ReceivedAt:              now.Add(-2 * time.Hour),
		RetentionUntil:          timePtr(now.Add(-time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}

	flowRecord := domain.Flow{ID: "flow-retention", WorkspaceID: workspaceID, ConnectionID: connection.ID, Name: "Retention", Status: domain.FlowDraft}
	if err := s.CreateFlow(ctx, flowRecord); err != nil {
		t.Fatal(err)
	}
	version := domain.FlowVersion{ID: "version-retention", WorkspaceID: workspaceID, FlowID: flowRecord.ID, Version: 1, Status: domain.FlowDraft, Graph: testGraph()}
	if err := s.SaveFlowVersion(ctx, version); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFlowExecution(ctx, domain.FlowExecution{
		ID:             "execution-retention",
		WorkspaceID:    workspaceID,
		ConnectionID:   connection.ID,
		FlowID:         flowRecord.ID,
		FlowVersionID:  version.ID,
		FlowVersion:    version.Version,
		EventID:        "event-retention",
		DeliveryID:     "delivery-retention",
		IdempotencyKey: "idempotency-retention",
		CorrelationID:  "correlation-retention",
		Status:         domain.ExecutionFailed,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateNodeExecution(ctx, domain.NodeExecution{
		ID:             "node-retention",
		WorkspaceID:    workspaceID,
		ExecutionID:    "execution-retention",
		NodeID:         "normalize-node",
		Status:         domain.NodeFailed,
		Attempt:        1,
		InputSnapshot:  map[string]any{"token": "[REDACTED]"},
		OutputSnapshot: map[string]any{"description": "sensitive but redacted"},
		RetentionUntil: timePtr(now.Add(-time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}

	cleared, err := s.PurgeExpiredRuntimePayloads(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 2 {
		t.Fatalf("cleared = %d, want inbound event and node snapshot", cleared)
	}
	event, err := s.GetInboundEvent(ctx, workspaceID, "event-retention")
	if err != nil {
		t.Fatal(err)
	}
	if len(event.Payload) != 0 || event.PayloadHash == "" || event.RetentionUntil == nil {
		t.Fatalf("event metadata/payload = %+v", event)
	}
	nodes, err := s.ListNodeExecutions(ctx, workspaceID, "execution-retention")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || len(nodes[0].InputSnapshot) != 0 || len(nodes[0].OutputSnapshot) != 0 || nodes[0].RetentionUntil == nil {
		t.Fatalf("node metadata/payload = %+v", nodes)
	}
}

func TestPurgeRuntimePayloadsDoesNotClearUnexpiredRows(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	workspaceID := domain.ID("workspace-retention-future")
	testWorkspace(t, s, workspaceID)
	testEndpoints(t, s, workspaceID, "gitlab-retention-future", "multica-retention-future", "retention-future")
	connection := testConnection(workspaceID, "connection-retention-future", "gitlab-retention-future", "multica-retention-future", "202", "target")
	if err := s.CreateConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveInboundEvent(ctx, domain.InboundEvent{
		ID:                      "event-retention-future",
		WorkspaceID:             workspaceID,
		ConnectionID:            connection.ID,
		Provider:                domain.ProviderGitLab,
		SourceInstanceID:        "gitlab-retention-future",
		SourceProjectExternalID: "202",
		BehaviorKey:             "gitlab.issue-hook",
		BehaviorVersion:         "1.0.0",
		DeliveryID:              "delivery-retention-future",
		Payload:                 map[string]any{"keep": "this"},
		RetentionUntil:          timePtr(now.Add(time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	if cleared, err := s.PurgeExpiredRuntimePayloads(ctx, now); err != nil || cleared != 0 {
		t.Fatalf("cleared = %d, err = %v, want 0", cleared, err)
	}
	event, err := s.GetInboundEvent(ctx, workspaceID, "event-retention-future")
	if err != nil {
		t.Fatal(err)
	}
	if event.Payload["keep"] != "this" {
		t.Fatalf("unexpired payload = %+v", event.Payload)
	}
}

func timePtr(value time.Time) *time.Time { return &value }
