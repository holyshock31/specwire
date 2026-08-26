CREATE TABLE IF NOT EXISTS onboarding_operations (
  id             TEXT PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id),
  connection_id  TEXT REFERENCES connections(id),
  status         TEXT NOT NULL,
  request_json   TEXT NOT NULL,
  error_category TEXT NOT NULL DEFAULT '',
  error_message  TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS onboarding_checkpoints (
  id            TEXT PRIMARY KEY,
  workspace_id  TEXT NOT NULL REFERENCES workspaces(id),
  operation_id  TEXT NOT NULL REFERENCES onboarding_operations(id),
  step          TEXT NOT NULL,
  status        TEXT NOT NULL,
  provider_id   TEXT NOT NULL DEFAULT '',
  result_json   TEXT NOT NULL DEFAULT '{}',
  updated_at    TEXT NOT NULL,
  UNIQUE (workspace_id, operation_id, step)
);

CREATE INDEX IF NOT EXISTS onboarding_workspace_idx
  ON onboarding_operations(workspace_id, updated_at);
