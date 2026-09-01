package store

import (
	"context"
	"errors"
	"testing"

	"specwire/bridge/internal/domain"
)

func TestCorrelationLifecycleIsTerminalAndIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspaceID := domain.ID("workspace-correlation-lifecycle")
	testWorkspace(t, s, workspaceID)
	testEndpoints(t, s, workspaceID, "gitlab-correlation-lifecycle", "multica-correlation-lifecycle", "lifecycle")
	connection := testConnection(workspaceID, "connection-correlation-lifecycle", "gitlab-correlation-lifecycle", "multica-correlation-lifecycle", "source", "target")
	if err := s.CreateConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	flowRecord := domain.Flow{ID: "flow-correlation-lifecycle", WorkspaceID: workspaceID, ConnectionID: connection.ID, Name: "Lifecycle", Status: domain.FlowDraft}
	if err := s.CreateFlow(ctx, flowRecord); err != nil {
		t.Fatal(err)
	}
	correlation, err := s.UpsertCorrelation(ctx, domain.Correlation{
		ID:                  "correlation-lifecycle",
		WorkspaceID:         workspaceID,
		ConnectionID:        connection.ID,
		FlowID:              flowRecord.ID,
		SourceIdentity:      "source",
		SourceIssueIID:      7,
		PublicationIdentity: "CHG-LIFECYCLE",
		TargetIdentity:      "issue-lifecycle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if correlation.LifecycleStatus != domain.ProjectionActive {
		t.Fatalf("initial lifecycle status = %q", correlation.LifecycleStatus)
	}
	if err := s.MarkCorrelationLifecycle(ctx, workspaceID, correlation.ID, domain.ProjectionCancelled); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkCorrelationLifecycle(ctx, workspaceID, correlation.ID, domain.ProjectionCancelled); err != nil {
		t.Fatalf("same terminal transition should be idempotent: %v", err)
	}
	if err := s.MarkCorrelationLifecycle(ctx, workspaceID, correlation.ID, domain.ProjectionDone); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting terminal transition = %v, want ErrConflict", err)
	}
	stored, err := s.GetCorrelation(ctx, workspaceID, connection.ID, flowRecord.ID, "source", "CHG-LIFECYCLE")
	if err != nil {
		t.Fatal(err)
	}
	if stored.LifecycleStatus != domain.ProjectionCancelled {
		t.Fatalf("stored lifecycle status = %q", stored.LifecycleStatus)
	}
	if _, err := s.UpsertCorrelation(ctx, domain.Correlation{
		WorkspaceID:         workspaceID,
		ConnectionID:        connection.ID,
		FlowID:              flowRecord.ID,
		SourceIdentity:      "source",
		PublicationIdentity: "CHG-LIFECYCLE",
		TargetIdentity:      "issue-lifecycle",
	}); err != nil {
		t.Fatal(err)
	}
	stored, err = s.GetCorrelation(ctx, workspaceID, connection.ID, flowRecord.ID, "source", "CHG-LIFECYCLE")
	if err != nil {
		t.Fatal(err)
	}
	if stored.LifecycleStatus != domain.ProjectionCancelled {
		t.Fatalf("upsert resurrected lifecycle status = %q", stored.LifecycleStatus)
	}
}
