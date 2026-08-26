package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"specwire/bridge/internal/domain"
)

func TestCreateFlowExecutionAndEnqueueRollsBackTogether(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspaceID := domain.ID("workspace-runtime-atomic")
	testWorkspace(t, s, workspaceID)
	testEndpoints(t, s, workspaceID, "gitlab-runtime-atomic", "multica-runtime-atomic", "runtime-atomic")
	connection := testConnection(workspaceID, "connection-runtime-atomic", "gitlab-runtime-atomic", "multica-runtime-atomic", "source", "target")
	if err := s.CreateConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	flowRecord := domain.Flow{ID: "flow-runtime-atomic", WorkspaceID: workspaceID, ConnectionID: connection.ID, Name: "Runtime atomic", Status: domain.FlowDraft}
	if err := s.CreateFlow(ctx, flowRecord); err != nil {
		t.Fatal(err)
	}
	version := domain.FlowVersion{ID: "version-runtime-atomic", WorkspaceID: workspaceID, FlowID: flowRecord.ID, Version: 1, Status: domain.FlowDraft, Graph: testGraph()}
	if err := s.SaveFlowVersion(ctx, version); err != nil {
		t.Fatal(err)
	}
	conflictingJob := domain.Job{ID: "job-runtime-atomic-conflict", WorkspaceID: workspaceID, Kind: "test", Payload: map[string]any{"reserved": true}}
	if err := s.EnqueueJob(ctx, conflictingJob); err != nil {
		t.Fatal(err)
	}
	execution := domain.FlowExecution{
		ID:             "execution-runtime-atomic",
		WorkspaceID:    workspaceID,
		ConnectionID:   connection.ID,
		FlowID:         flowRecord.ID,
		FlowVersionID:  version.ID,
		FlowVersion:    version.Version,
		EventID:        "event-runtime-atomic",
		DeliveryID:     "delivery-runtime-atomic",
		IdempotencyKey: "idempotency-runtime-atomic",
		CorrelationID:  "correlation-runtime-atomic",
		Status:         domain.ExecutionQueued,
	}
	err := s.CreateFlowExecutionAndEnqueue(ctx, execution, domain.Job{ID: conflictingJob.ID, WorkspaceID: workspaceID, Kind: "flow.execute"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("atomic create error = %v, want conflict", err)
	}
	if _, err := s.GetFlowExecution(ctx, workspaceID, execution.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("execution after rolled-back enqueue = %v, want not found", err)
	}
	claimedConflict, err := s.ClaimNextJob(ctx, "atomic-conflict-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteJob(ctx, workspaceID, claimedConflict.ID, "atomic-conflict-worker"); err != nil {
		t.Fatal(err)
	}

	successJob := domain.Job{ID: "job-runtime-atomic-success", WorkspaceID: workspaceID, Kind: "flow.execute", Payload: map[string]any{"execution_id": execution.ID}}
	if err := s.CreateFlowExecutionAndEnqueue(ctx, execution, successJob); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNextJob(ctx, "atomic-test-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != successJob.ID || claimed.Payload["execution_id"] != string(execution.ID) {
		t.Fatalf("claimed job = %+v", claimed)
	}
}

func TestRequeueFlowExecutionRollsBackAndAllowsOneConcurrentRetry(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspaceID := domain.ID("workspace-requeue-atomic")
	testWorkspace(t, s, workspaceID)
	testEndpoints(t, s, workspaceID, "gitlab-requeue-atomic", "multica-requeue-atomic", "requeue-atomic")
	connection := testConnection(workspaceID, "connection-requeue-atomic", "gitlab-requeue-atomic", "multica-requeue-atomic", "source", "target")
	if err := s.CreateConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	flowRecord := domain.Flow{ID: "flow-requeue-atomic", WorkspaceID: workspaceID, ConnectionID: connection.ID, Name: "Requeue atomic", Status: domain.FlowDraft}
	if err := s.CreateFlow(ctx, flowRecord); err != nil {
		t.Fatal(err)
	}
	version := domain.FlowVersion{ID: "version-requeue-atomic", WorkspaceID: workspaceID, FlowID: flowRecord.ID, Version: 1, Status: domain.FlowDraft, Graph: testGraph()}
	if err := s.SaveFlowVersion(ctx, version); err != nil {
		t.Fatal(err)
	}
	execution := domain.FlowExecution{
		ID:             "execution-requeue-atomic",
		WorkspaceID:    workspaceID,
		ConnectionID:   connection.ID,
		FlowID:         flowRecord.ID,
		FlowVersionID:  version.ID,
		FlowVersion:    version.Version,
		EventID:        "event-requeue-atomic",
		DeliveryID:     "delivery-requeue-atomic",
		IdempotencyKey: "idempotency-requeue-atomic",
		CorrelationID:  "correlation-requeue-atomic",
		Status:         domain.ExecutionFailed,
		ErrorCategory:  "provider",
		ErrorMessage:   "temporary failure",
	}
	if err := s.CreateFlowExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	conflictingJob := domain.Job{ID: "job-requeue-atomic-conflict", WorkspaceID: workspaceID, Kind: "test", Payload: map[string]any{"reserved": true}}
	if err := s.EnqueueJob(ctx, conflictingJob); err != nil {
		t.Fatal(err)
	}
	queued := execution
	queued.Status = domain.ExecutionQueued
	queued.ErrorCategory = ""
	queued.ErrorMessage = ""
	err := s.RequeueFlowExecution(ctx, queued, domain.Job{ID: conflictingJob.ID, WorkspaceID: workspaceID, Kind: "flow.retry"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("atomic requeue error = %v, want conflict", err)
	}
	afterRollback, err := s.GetFlowExecution(ctx, workspaceID, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRollback.Status != domain.ExecutionFailed || afterRollback.ErrorCategory != "provider" {
		t.Fatalf("execution after rolled-back requeue = %+v", afterRollback)
	}
	claimedConflict, err := s.ClaimNextJob(ctx, "requeue-conflict-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteJob(ctx, workspaceID, claimedConflict.ID, "requeue-conflict-worker"); err != nil {
		t.Fatal(err)
	}

	if err := s.RequeueFlowExecution(ctx, queued, domain.Job{ID: "job-requeue-atomic-success", WorkspaceID: workspaceID, Kind: "flow.retry", Payload: map[string]any{"execution_id": execution.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RequeueFlowExecution(ctx, queued, domain.Job{ID: "job-requeue-atomic-loser", WorkspaceID: workspaceID, Kind: "flow.retry"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second retry error = %v, want conflict", err)
	}
	final, err := s.GetFlowExecution(ctx, workspaceID, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != domain.ExecutionQueued || final.ErrorCategory != "" || final.ErrorMessage != "" {
		t.Fatalf("requeued execution = %+v", final)
	}
	claimed, err := s.ClaimNextJob(ctx, "requeue-test-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != "job-requeue-atomic-success" {
		t.Fatalf("claimed retry job = %+v", claimed)
	}
}
