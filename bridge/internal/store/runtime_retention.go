package store

import (
	"context"
	"time"
)

// PurgeExpiredRuntimePayloads removes retained payload material while keeping
// the execution/event identity, hash, status and provider references available
// for audit and reconciliation.  Expiry is assigned by the runtime when the
// event or node checkpoint is written; a NULL expiry is left untouched so a
// caller cannot accidentally turn an unconfigured retention policy into data
// loss.
func (s *Store) PurgeExpiredRuntimePayloads(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		now = s.now()
	}
	cutoff := now.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `UPDATE inbound_events
		SET payload_json = '{}'
		WHERE retention_until IS NOT NULL AND retention_until <= ? AND payload_json <> '{}'`, cutoff)
	if err != nil {
		return 0, err
	}
	cleared, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	result, err = tx.ExecContext(ctx, `UPDATE node_executions
		SET input_snapshot_json = '{}', output_snapshot_json = '{}'
		WHERE retention_until IS NOT NULL AND retention_until <= ?
		  AND (input_snapshot_json <> '{}' OR output_snapshot_json <> '{}')`, cutoff)
	if err != nil {
		return 0, err
	}
	nodeCleared, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(cleared + nodeCleared), nil
}
