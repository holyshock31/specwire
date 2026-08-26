CREATE TABLE IF NOT EXISTS credential_profiles (
  id             TEXT PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id),
  provider       TEXT NOT NULL,
  kind           TEXT NOT NULL CHECK (kind IN ('pat', 'group_access_token')),
  alias          TEXT NOT NULL,
  secret_ref_id  TEXT NOT NULL REFERENCES secrets(ref_id),
  status         TEXT NOT NULL,
  capabilities_json TEXT NOT NULL DEFAULT '[]',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  UNIQUE (workspace_id, alias)
);

ALTER TABLE gitlab_group_bindings ADD COLUMN credential_profile_id TEXT REFERENCES credential_profiles(id);

CREATE TABLE IF NOT EXISTS provider_capability_checks (
  id             TEXT PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id),
  provider       TEXT NOT NULL,
  instance_id    TEXT NOT NULL,
  resource_type  TEXT NOT NULL,
  resource_id    TEXT NOT NULL,
  capability     TEXT NOT NULL,
  available      INTEGER NOT NULL,
  reason         TEXT NOT NULL DEFAULT '',
  request_id     TEXT NOT NULL DEFAULT '',
  checked_at     TEXT NOT NULL,
  UNIQUE (workspace_id, instance_id, resource_type, resource_id, capability)
);
