ALTER TABLE inbound_events ADD COLUMN retention_until TEXT;
ALTER TABLE node_executions ADD COLUMN retention_until TEXT;

CREATE INDEX IF NOT EXISTS inbound_events_retention_idx
  ON inbound_events (retention_until);

CREATE INDEX IF NOT EXISTS node_executions_retention_idx
  ON node_executions (retention_until);
