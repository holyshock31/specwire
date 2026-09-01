ALTER TABLE flow_executions ADD COLUMN attention_status TEXT NOT NULL DEFAULT 'none';
ALTER TABLE flow_executions ADD COLUMN attention_actor_account_id TEXT;
ALTER TABLE flow_executions ADD COLUMN attention_updated_at TEXT;

UPDATE flow_executions
SET attention_status = 'open'
WHERE status IN ('failed', 'indeterminate', 'reconciliation-required');

CREATE INDEX IF NOT EXISTS flow_executions_attention_idx
  ON flow_executions (workspace_id, attention_status, updated_at);
