CREATE TABLE IF NOT EXISTS inbound_events (
  id                        TEXT PRIMARY KEY,
  workspace_id              TEXT NOT NULL REFERENCES workspaces(id),
  connection_id             TEXT NOT NULL REFERENCES connections(id),
  provider                  TEXT NOT NULL,
  source_instance_id        TEXT NOT NULL,
  source_project_external_id TEXT NOT NULL,
  behavior_key              TEXT NOT NULL,
  behavior_version          TEXT NOT NULL,
  delivery_id               TEXT NOT NULL,
  payload_json              TEXT NOT NULL,
  payload_hash              TEXT NOT NULL,
  received_at               TEXT NOT NULL,
  UNIQUE (workspace_id, source_instance_id, source_project_external_id, behavior_key, behavior_version, delivery_id)
);

CREATE INDEX IF NOT EXISTS inbound_events_route_idx
  ON inbound_events (workspace_id, source_instance_id, source_project_external_id, behavior_key, behavior_version);

CREATE INDEX IF NOT EXISTS flow_executions_connection_idx
  ON flow_executions (workspace_id, connection_id, created_at);
