package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"specwire/bridge/internal/domain"
)

const ErrNoJobText = "no runnable job"

func (s *Store) SaveInboundEvent(ctx context.Context, event domain.InboundEvent) (domain.InboundEvent, error) {
	if event.ID.Empty() {
		event.ID = domain.NewID()
	}
	if err := requireWorkspaceID(event.WorkspaceID); err != nil {
		return domain.InboundEvent{}, err
	}
	if event.ConnectionID.Empty() || event.SourceInstanceID.Empty() || strings.TrimSpace(event.SourceProjectExternalID) == "" || strings.TrimSpace(event.BehaviorKey) == "" || strings.TrimSpace(event.BehaviorVersion) == "" || strings.TrimSpace(event.DeliveryID) == "" {
		return domain.InboundEvent{}, fmt.Errorf("%w: incomplete inbound event identity", domain.ErrInvalid)
	}
	if event.Provider == "" {
		event.Provider = domain.ProviderGitLab
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = s.now()
	}
	payload, err := marshalJSON(event.Payload, "{}")
	if err != nil {
		return domain.InboundEvent{}, err
	}
	if event.PayloadHash == "" {
		sum := sha256.Sum256([]byte(payload))
		event.PayloadHash = hex.EncodeToString(sum[:])
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO inbound_events
		(id, workspace_id, connection_id, provider, source_instance_id, source_project_external_id, behavior_key, behavior_version, delivery_id, payload_json, payload_hash, received_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, source_instance_id, source_project_external_id, behavior_key, behavior_version, delivery_id) DO NOTHING`,
		event.ID, event.WorkspaceID, event.ConnectionID, event.Provider, event.SourceInstanceID, event.SourceProjectExternalID, event.BehaviorKey, event.BehaviorVersion, event.DeliveryID, payload, event.PayloadHash, event.ReceivedAt.Format(time.RFC3339Nano))
	if err != nil {
		return domain.InboundEvent{}, constraintError("save inbound event", err)
	}
	return s.GetInboundEventByDelivery(ctx, event.WorkspaceID, event.SourceInstanceID, event.SourceProjectExternalID, event.BehaviorKey, event.BehaviorVersion, event.DeliveryID)
}

func (s *Store) GetInboundEvent(ctx context.Context, workspaceID, eventID domain.ID) (domain.InboundEvent, error) {
	var event domain.InboundEvent
	var payload, received string
	err := s.db.QueryRowContext(ctx, `SELECT id, connection_id, provider, source_instance_id, source_project_external_id, behavior_key, behavior_version, delivery_id, payload_json, payload_hash, received_at
		FROM inbound_events WHERE workspace_id = ? AND id = ?`, workspaceID, eventID).Scan(
		&event.ID, &event.ConnectionID, &event.Provider, &event.SourceInstanceID, &event.SourceProjectExternalID, &event.BehaviorKey, &event.BehaviorVersion, &event.DeliveryID, &payload, &event.PayloadHash, &received)
	if err == sql.ErrNoRows {
		return domain.InboundEvent{}, fmt.Errorf("%w: inbound event %s", domain.ErrNotFound, eventID)
	}
	if err != nil {
		return domain.InboundEvent{}, fmt.Errorf("get inbound event: %w", err)
	}
	event.WorkspaceID = workspaceID
	if err := json.Unmarshal([]byte(payload), &event.Payload); err != nil {
		return domain.InboundEvent{}, fmt.Errorf("decode inbound event: %w", err)
	}
	event.ReceivedAt, err = decodeTime(received)
	if err != nil {
		return domain.InboundEvent{}, err
	}
	return event, nil
}

func (s *Store) GetInboundEventByDelivery(ctx context.Context, workspaceID, sourceInstanceID domain.ID, sourceProject, behaviorKey, behaviorVersion, deliveryID string) (domain.InboundEvent, error) {
	var eventID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM inbound_events WHERE workspace_id = ? AND source_instance_id = ? AND source_project_external_id = ? AND behavior_key = ? AND behavior_version = ? AND delivery_id = ?`, workspaceID, sourceInstanceID, sourceProject, behaviorKey, behaviorVersion, deliveryID).Scan(&eventID)
	if err == sql.ErrNoRows {
		return domain.InboundEvent{}, fmt.Errorf("%w: inbound event delivery %s", domain.ErrNotFound, deliveryID)
	}
	if err != nil {
		return domain.InboundEvent{}, err
	}
	return s.GetInboundEvent(ctx, workspaceID, domain.ID(eventID))
}

func (s *Store) CreateFlowExecution(ctx context.Context, execution domain.FlowExecution) error {
	if execution.ID.Empty() || execution.EventID.Empty() || execution.FlowVersionID.Empty() || execution.ConnectionID.Empty() || execution.FlowID.Empty() || strings.TrimSpace(execution.IdempotencyKey) == "" || strings.TrimSpace(execution.CorrelationID) == "" {
		return fmt.Errorf("%w: incomplete FlowExecution", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(execution.WorkspaceID); err != nil {
		return err
	}
	if execution.Status == "" {
		execution.Status = domain.ExecutionQueued
	}
	if execution.CreatedAt.IsZero() {
		execution.CreatedAt = s.now()
	}
	if execution.UpdatedAt.IsZero() {
		execution.UpdatedAt = execution.CreatedAt
	}
	providerIDs, err := marshalJSON(execution.ProviderRequestIDs, "[]")
	if err != nil {
		return err
	}
	var flowWorkspace, versionWorkspace string
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM flows WHERE id = ?`, execution.FlowID).Scan(&flowWorkspace); err == sql.ErrNoRows {
		return fmt.Errorf("%w: flow %s", domain.ErrNotFound, execution.FlowID)
	} else if err != nil {
		return err
	} else if domain.ID(flowWorkspace) != execution.WorkspaceID {
		return fmt.Errorf("%w: execution Flow belongs to another workspace", domain.ErrForbidden)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM flow_versions WHERE id = ? AND flow_id = ?`, execution.FlowVersionID, execution.FlowID).Scan(&versionWorkspace); err == sql.ErrNoRows {
		return fmt.Errorf("%w: FlowVersion %s", domain.ErrNotFound, execution.FlowVersionID)
	} else if err != nil {
		return err
	} else if domain.ID(versionWorkspace) != execution.WorkspaceID {
		return fmt.Errorf("%w: execution FlowVersion belongs to another workspace", domain.ErrForbidden)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO flow_executions
		(id, workspace_id, connection_id, flow_id, flow_version_id, flow_version, event_id, delivery_id, idempotency_key, correlation_id, status, current_node_id, provider_request_ids_json, error_category, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, execution.ID, execution.WorkspaceID, execution.ConnectionID, execution.FlowID, execution.FlowVersionID, execution.FlowVersion, execution.EventID, execution.DeliveryID, execution.IdempotencyKey, execution.CorrelationID, execution.Status, execution.CurrentNodeID, providerIDs, execution.ErrorCategory, execution.CreatedAt.Format(time.RFC3339Nano), execution.UpdatedAt.Format(time.RFC3339Nano))
	return constraintError("create FlowExecution", err)
}

func (s *Store) GetFlowExecution(ctx context.Context, workspaceID, executionID domain.ID) (domain.FlowExecution, error) {
	var execution domain.FlowExecution
	var providerIDs, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, connection_id, flow_id, flow_version_id, flow_version, event_id, delivery_id, idempotency_key, correlation_id, status, current_node_id, provider_request_ids_json, error_category, created_at, updated_at
		FROM flow_executions WHERE workspace_id = ? AND id = ?`, workspaceID, executionID).Scan(
		&execution.ID, &execution.ConnectionID, &execution.FlowID, &execution.FlowVersionID, &execution.FlowVersion, &execution.EventID, &execution.DeliveryID, &execution.IdempotencyKey, &execution.CorrelationID, &execution.Status, &execution.CurrentNodeID, &providerIDs, &execution.ErrorCategory, &created, &updated)
	if err == sql.ErrNoRows {
		return domain.FlowExecution{}, fmt.Errorf("%w: FlowExecution %s", domain.ErrNotFound, executionID)
	}
	if err != nil {
		return domain.FlowExecution{}, fmt.Errorf("get FlowExecution: %w", err)
	}
	execution.WorkspaceID = workspaceID
	if err := json.Unmarshal([]byte(providerIDs), &execution.ProviderRequestIDs); err != nil {
		return domain.FlowExecution{}, err
	}
	execution.CreatedAt, err = decodeTime(created)
	if err != nil {
		return domain.FlowExecution{}, err
	}
	execution.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return domain.FlowExecution{}, err
	}
	return execution, nil
}

func (s *Store) UpdateFlowExecution(ctx context.Context, execution domain.FlowExecution) error {
	if err := requireWorkspaceID(execution.WorkspaceID); err != nil {
		return err
	}
	providerIDs, err := marshalJSON(execution.ProviderRequestIDs, "[]")
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE flow_executions SET status = ?, current_node_id = ?, provider_request_ids_json = ?, error_category = ?, updated_at = ? WHERE workspace_id = ? AND id = ?`, execution.Status, execution.CurrentNodeID, providerIDs, execution.ErrorCategory, s.now().Format(time.RFC3339Nano), execution.WorkspaceID, execution.ID)
	if err != nil {
		return fmt.Errorf("update FlowExecution: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return fmt.Errorf("%w: FlowExecution %s", domain.ErrNotFound, execution.ID)
	}
	return nil
}

func (s *Store) ListFlowExecutions(ctx context.Context, workspaceID, connectionID, flowID domain.ID, limit int) ([]domain.FlowExecution, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	query := `SELECT id FROM flow_executions WHERE workspace_id = ?`
	args := []any{workspaceID}
	if !connectionID.Empty() {
		query += ` AND connection_id = ?`
		args = append(args, connectionID)
	}
	if !flowID.Empty() {
		query += ` AND flow_id = ?`
		args = append(args, flowID)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.FlowExecution
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		item, err := s.GetFlowExecution(ctx, workspaceID, domain.ID(id))
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CreateNodeExecution(ctx context.Context, node domain.NodeExecution) error {
	if node.ID.Empty() || node.ExecutionID.Empty() || node.NodeID.Empty() || node.Attempt < 0 {
		return fmt.Errorf("%w: incomplete NodeExecution", domain.ErrInvalid)
	}
	if err := requireWorkspaceID(node.WorkspaceID); err != nil {
		return err
	}
	if node.Status == "" {
		node.Status = domain.NodeQueued
	}
	input, err := marshalJSON(node.InputSnapshot, "{}")
	if err != nil {
		return err
	}
	output, err := marshalJSON(node.OutputSnapshot, "{}")
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO node_executions
		(id, workspace_id, execution_id, node_id, status, attempt, input_snapshot_json, output_snapshot_json, error_category, provider_request_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, node.ID, node.WorkspaceID, node.ExecutionID, node.NodeID, node.Status, node.Attempt, input, output, node.ErrorCategory, node.ProviderRequestID)
	return constraintError("create NodeExecution", err)
}

func (s *Store) UpdateNodeExecution(ctx context.Context, node domain.NodeExecution) error {
	input, err := marshalJSON(node.InputSnapshot, "{}")
	if err != nil {
		return err
	}
	output, err := marshalJSON(node.OutputSnapshot, "{}")
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE node_executions SET status = ?, input_snapshot_json = ?, output_snapshot_json = ?, error_category = ?, provider_request_id = ? WHERE workspace_id = ? AND id = ?`, node.Status, input, output, node.ErrorCategory, node.ProviderRequestID, node.WorkspaceID, node.ID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return fmt.Errorf("%w: NodeExecution %s", domain.ErrNotFound, node.ID)
	}
	return nil
}

func (s *Store) ListNodeExecutions(ctx context.Context, workspaceID, executionID domain.ID) ([]domain.NodeExecution, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, node_id, status, attempt, input_snapshot_json, output_snapshot_json, error_category, provider_request_id FROM node_executions WHERE workspace_id = ? AND execution_id = ? ORDER BY attempt, node_id`, workspaceID, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.NodeExecution
	for rows.Next() {
		var item domain.NodeExecution
		var input, output string
		if err := rows.Scan(&item.ID, &item.NodeID, &item.Status, &item.Attempt, &input, &output, &item.ErrorCategory, &item.ProviderRequestID); err != nil {
			return nil, err
		}
		item.WorkspaceID, item.ExecutionID = workspaceID, executionID
		if err := json.Unmarshal([]byte(input), &item.InputSnapshot); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(output), &item.OutputSnapshot); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) EnqueueJob(ctx context.Context, job domain.Job) error {
	if job.ID.Empty() {
		job.ID = domain.NewID()
	}
	if err := requireWorkspaceID(job.WorkspaceID); err != nil {
		return err
	}
	if strings.TrimSpace(job.Kind) == "" {
		return fmt.Errorf("%w: job kind is required", domain.ErrInvalid)
	}
	if job.Status == "" {
		job.Status = "queued"
	}
	if job.AvailableAt.IsZero() {
		job.AvailableAt = s.now()
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = job.AvailableAt
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = job.CreatedAt
	}
	payload, err := marshalJSON(job.Payload, "{}")
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO jobs
		(id, workspace_id, kind, payload_json, available_at, lease_until, leased_by, attempt_count, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, job.WorkspaceID, job.Kind, payload, job.AvailableAt.Format(time.RFC3339Nano), formatOptionalTime(job.LeaseUntil), job.LeasedBy, job.AttemptCount, job.Status, job.CreatedAt.Format(time.RFC3339Nano), job.UpdatedAt.Format(time.RFC3339Nano))
	return constraintError("enqueue job", err)
}

func (s *Store) ClaimNextJob(ctx context.Context, workerID string, lease time.Duration) (domain.Job, error) {
	if strings.TrimSpace(workerID) == "" || lease <= 0 {
		return domain.Job{}, fmt.Errorf("%w: worker ID and lease are required", domain.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Job{}, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM jobs
		WHERE (status = 'queued' AND available_at <= ?)
		   OR (status = 'running' AND lease_until IS NOT NULL AND lease_until <= ?)
		ORDER BY available_at, created_at, id LIMIT 1`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)).Scan(&id)
	if err == sql.ErrNoRows {
		return domain.Job{}, fmt.Errorf("%w: %s", domain.ErrNotFound, ErrNoJobText)
	}
	if err != nil {
		return domain.Job{}, err
	}
	leaseUntil := now.Add(lease)
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET status = 'running', leased_by = ?, lease_until = ?, attempt_count = attempt_count + 1, updated_at = ? WHERE id = ? AND ((status = 'queued' AND available_at <= ?) OR (status = 'running' AND lease_until IS NOT NULL AND lease_until <= ?))`, workerID, leaseUntil.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), id, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return domain.Job{}, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return domain.Job{}, err
		}
		return domain.Job{}, fmt.Errorf("%w: job was claimed by another worker", domain.ErrConflict)
	}
	job, err := scanJob(tx.QueryRowContext(ctx, `SELECT id, workspace_id, kind, payload_json, available_at, lease_until, leased_by, attempt_count, status, created_at, updated_at FROM jobs WHERE id = ?`, id))
	if err != nil {
		return domain.Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Job{}, err
	}
	return job, nil
}

func (s *Store) CompleteJob(ctx context.Context, workspaceID domain.ID, jobID, workerID domain.ID) error {
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'succeeded', lease_until = NULL, leased_by = '', updated_at = ? WHERE workspace_id = ? AND id = ? AND leased_by = ?`, s.now().Format(time.RFC3339Nano), workspaceID, jobID, workerID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return fmt.Errorf("%w: job %s", domain.ErrNotFound, jobID)
	}
	return nil
}

func (s *Store) FailJob(ctx context.Context, workspaceID, jobID domain.ID, workerID string, retryAt *time.Time, errorMessage string) error {
	status := "failed"
	available := s.now()
	if retryAt != nil {
		status = "queued"
		available = retryAt.UTC()
	}
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, available_at = ?, lease_until = NULL, leased_by = '', updated_at = ? WHERE workspace_id = ? AND id = ? AND leased_by = ?`, status, available.Format(time.RFC3339Nano), s.now().Format(time.RFC3339Nano), workspaceID, jobID, workerID)
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return fmt.Errorf("%w: job %s", domain.ErrNotFound, jobID)
	}
	_ = errorMessage // job errors are recorded on FlowExecution, not in payload/logs.
	return nil
}

func scanJob(row *sql.Row) (domain.Job, error) {
	var job domain.Job
	var payload, available, created, updated string
	var leaseUntil sql.NullString
	if err := row.Scan(&job.ID, &job.WorkspaceID, &job.Kind, &payload, &available, &leaseUntil, &job.LeasedBy, &job.AttemptCount, &job.Status, &created, &updated); err != nil {
		return domain.Job{}, err
	}
	if err := json.Unmarshal([]byte(payload), &job.Payload); err != nil {
		return domain.Job{}, err
	}
	var err error
	job.AvailableAt, err = decodeTime(available)
	if err != nil {
		return domain.Job{}, err
	}
	job.CreatedAt, err = decodeTime(created)
	if err != nil {
		return domain.Job{}, err
	}
	job.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return domain.Job{}, err
	}
	job.LeaseUntil, err = decodeOptionalTime(leaseUntil)
	return job, err
}

func (s *Store) UpsertCorrelation(ctx context.Context, correlation domain.Correlation) (domain.Correlation, error) {
	if correlation.ID.Empty() {
		correlation.ID = domain.NewID()
	}
	if correlation.WorkspaceID.Empty() || correlation.ConnectionID.Empty() || strings.TrimSpace(correlation.SourceIdentity) == "" || strings.TrimSpace(correlation.PublicationIdentity) == "" {
		return domain.Correlation{}, fmt.Errorf("%w: incomplete correlation", domain.ErrInvalid)
	}
	if correlation.CreatedAt.IsZero() {
		correlation.CreatedAt = s.now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO correlations
		(id, workspace_id, connection_id, source_identity, publication_identity, target_identity, flow_execution_id, provider_request_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, connection_id, source_identity, publication_identity) DO UPDATE SET target_identity = excluded.target_identity, flow_execution_id = excluded.flow_execution_id, provider_request_id = excluded.provider_request_id`, correlation.ID, correlation.WorkspaceID, correlation.ConnectionID, correlation.SourceIdentity, correlation.PublicationIdentity, correlation.TargetIdentity, nullID(correlation.FlowExecutionID), correlation.ProviderRequestID, correlation.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return domain.Correlation{}, constraintError("upsert correlation", err)
	}
	return s.GetCorrelation(ctx, correlation.WorkspaceID, correlation.ConnectionID, correlation.SourceIdentity, correlation.PublicationIdentity)
}

func (s *Store) GetCorrelation(ctx context.Context, workspaceID, connectionID domain.ID, sourceIdentity, publicationIdentity string) (domain.Correlation, error) {
	var item domain.Correlation
	var flowExecution sql.NullString
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id, source_identity, publication_identity, target_identity, flow_execution_id, provider_request_id, created_at FROM correlations WHERE workspace_id = ? AND connection_id = ? AND source_identity = ? AND publication_identity = ?`, workspaceID, connectionID, sourceIdentity, publicationIdentity).Scan(&item.ID, &item.SourceIdentity, &item.PublicationIdentity, &item.TargetIdentity, &flowExecution, &item.ProviderRequestID, &created)
	if err == sql.ErrNoRows {
		return domain.Correlation{}, fmt.Errorf("%w: correlation %s", domain.ErrNotFound, publicationIdentity)
	}
	if err != nil {
		return domain.Correlation{}, err
	}
	item.WorkspaceID, item.ConnectionID = workspaceID, connectionID
	if flowExecution.Valid {
		item.FlowExecutionID = domain.ID(flowExecution.String)
	}
	item.CreatedAt, err = decodeTime(created)
	return item, err
}

func (s *Store) ListActiveHookRoutesForEvent(ctx context.Context, workspaceID, sourceInstanceID domain.ID, sourceProject, behaviorKey, behaviorVersion string) ([]domain.HookRoute, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.connection_id, r.flow_id, r.flow_version, r.event_filter_json, r.hook_id, r.status FROM hook_routes r JOIN connections c ON c.id = r.connection_id AND c.workspace_id = r.workspace_id WHERE r.workspace_id = ? AND r.source_instance_id = ? AND r.source_project_external_id = ? AND r.behavior_key = ? AND r.behavior_version = ? AND r.status = 'active' AND c.status <> 'disabled' ORDER BY r.flow_id, r.flow_version`, workspaceID, sourceInstanceID, sourceProject, behaviorKey, behaviorVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.HookRoute
	for rows.Next() {
		var item domain.HookRoute
		var filter string
		if err := rows.Scan(&item.ID, &item.ConnectionID, &item.FlowID, &item.FlowVersion, &filter, &item.HookRef, &item.Status); err != nil {
			return nil, err
		}
		item.WorkspaceID = workspaceID
		item.SourceProject = domain.ProviderProjectRef{InstanceID: sourceInstanceID, ExternalID: sourceProject}
		if err := json.Unmarshal([]byte(filter), &item.EventFilter); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) DisableHookRoutesForConnection(ctx context.Context, workspaceID, connectionID domain.ID) error {
	_, err := s.db.ExecContext(ctx, `UPDATE hook_routes SET status = 'disabled' WHERE workspace_id = ? AND connection_id = ?`, workspaceID, connectionID)
	return err
}
