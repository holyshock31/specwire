package store

import (
	"context"
	"database/sql"
	"fmt"

	"specwire/bridge/internal/domain"
)

// GetConnectionStats returns the derived values needed by the Connection
// workbench. The query is intentionally Workspace-scoped and uses aggregate
// subqueries so list consumers do not need to load every Flow, resource, or
// execution just to render a summary row.
func (s *Store) GetConnectionStats(ctx context.Context, workspaceID, connectionID domain.ID) (domain.ConnectionStats, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return domain.ConnectionStats{}, err
	}
	if connectionID.Empty() {
		return domain.ConnectionStats{}, fmt.Errorf("%w: connection_id is required", domain.ErrInvalid)
	}
	var stats domain.ConnectionStats
	var latestExecutionStatus, latestExecutionAttentionStatus, latestExecutionAt sql.NullString
	var latestOnboardingStatus, latestOnboardingAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM flows f WHERE f.workspace_id = c.workspace_id AND f.connection_id = c.id),
		(SELECT COUNT(*) FROM managed_resources r WHERE r.workspace_id = c.workspace_id AND r.connection_id = c.id),
		(SELECT COUNT(*) FROM flow_executions e WHERE e.workspace_id = c.workspace_id AND e.connection_id = c.id),
		(SELECT COUNT(*) FROM flow_executions e WHERE e.workspace_id = c.workspace_id AND e.connection_id = c.id AND e.status = 'succeeded'),
		(SELECT COUNT(*) FROM flow_executions e WHERE e.workspace_id = c.workspace_id AND e.connection_id = c.id AND e.status IN ('failed', 'indeterminate', 'reconciliation-required') AND e.attention_status = 'open'),
		(SELECT e.status FROM flow_executions e WHERE e.workspace_id = c.workspace_id AND e.connection_id = c.id ORDER BY e.updated_at DESC, e.id DESC LIMIT 1),
		(SELECT e.attention_status FROM flow_executions e WHERE e.workspace_id = c.workspace_id AND e.connection_id = c.id ORDER BY e.updated_at DESC, e.id DESC LIMIT 1),
		(SELECT e.updated_at FROM flow_executions e WHERE e.workspace_id = c.workspace_id AND e.connection_id = c.id ORDER BY e.updated_at DESC, e.id DESC LIMIT 1),
		(SELECT o.status FROM onboarding_operations o WHERE o.workspace_id = c.workspace_id AND o.connection_id = c.id ORDER BY o.updated_at DESC, o.id DESC LIMIT 1),
		(SELECT o.updated_at FROM onboarding_operations o WHERE o.workspace_id = c.workspace_id AND o.connection_id = c.id ORDER BY o.updated_at DESC, o.id DESC LIMIT 1)
		FROM connections c WHERE c.workspace_id = ? AND c.id = ?`, workspaceID, connectionID).Scan(
		&stats.FlowCount,
		&stats.ResourceCount,
		&stats.ExecutionCount,
		&stats.SuccessfulExecutionCount,
		&stats.UnacknowledgedExecutionCount,
		&latestExecutionStatus,
		&latestExecutionAttentionStatus,
		&latestExecutionAt,
		&latestOnboardingStatus,
		&latestOnboardingAt,
	)
	if err == sql.ErrNoRows {
		return domain.ConnectionStats{}, fmt.Errorf("%w: connection %s", domain.ErrNotFound, connectionID)
	}
	if err != nil {
		return domain.ConnectionStats{}, fmt.Errorf("get connection stats: %w", err)
	}
	if latestExecutionStatus.Valid {
		stats.LatestExecutionStatus = domain.ExecutionStatus(latestExecutionStatus.String)
	}
	if latestExecutionAttentionStatus.Valid {
		stats.LatestExecutionAttentionStatus = domain.ExecutionAttentionStatus(latestExecutionAttentionStatus.String)
	}
	if latestExecutionAt.Valid {
		value, err := decodeTime(latestExecutionAt.String)
		if err != nil {
			return domain.ConnectionStats{}, fmt.Errorf("decode latest execution time: %w", err)
		}
		stats.LatestExecutionAt = &value
	}
	if latestOnboardingStatus.Valid {
		stats.LatestOnboardingStatus = domain.OnboardingStatus(latestOnboardingStatus.String)
	}
	if latestOnboardingAt.Valid {
		value, err := decodeTime(latestOnboardingAt.String)
		if err != nil {
			return domain.ConnectionStats{}, fmt.Errorf("decode latest onboarding time: %w", err)
		}
		stats.LatestOnboardingAt = &value
	}
	return stats, nil
}
