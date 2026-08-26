CREATE TABLE IF NOT EXISTS workspaces (
  id         TEXT PRIMARY KEY,
  slug       TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  status     TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS accounts (
  id           TEXT PRIMARY KEY,
  email        TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  status       TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS identity_providers (
  id                   TEXT PRIMARY KEY,
  workspace_id         TEXT NOT NULL REFERENCES workspaces(id),
  kind                 TEXT NOT NULL,
  name                 TEXT NOT NULL,
  issuer_url           TEXT,
  client_id            TEXT,
  client_secret_ref_id TEXT,
  enabled              INTEGER NOT NULL DEFAULT 1,
  UNIQUE (workspace_id, name)
);

CREATE TABLE IF NOT EXISTS workspace_memberships (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  account_id   TEXT NOT NULL REFERENCES accounts(id),
  status       TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  UNIQUE (workspace_id, account_id)
);

CREATE TABLE IF NOT EXISTS scoped_role_bindings (
  id            TEXT PRIMARY KEY,
  workspace_id  TEXT NOT NULL REFERENCES workspaces(id),
  membership_id TEXT NOT NULL REFERENCES workspace_memberships(id),
  role          TEXT NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
  scope_type    TEXT NOT NULL,
  scope_id      TEXT,
  UNIQUE (workspace_id, membership_id, role, scope_type, scope_id)
);

CREATE TABLE IF NOT EXISTS gitlab_instances (
  id            TEXT PRIMARY KEY,
  workspace_id  TEXT NOT NULL REFERENCES workspaces(id),
  name          TEXT NOT NULL,
  base_url      TEXT NOT NULL,
  external_id   TEXT,
  credential_ref_id TEXT,
  status        TEXT NOT NULL,
  capabilities_json TEXT NOT NULL DEFAULT '[]',
  UNIQUE (workspace_id, name),
  UNIQUE (workspace_id, external_id)
);

CREATE TABLE IF NOT EXISTS gitlab_group_bindings (
  id                TEXT PRIMARY KEY,
  workspace_id      TEXT NOT NULL REFERENCES workspaces(id),
  gitlab_instance_id TEXT NOT NULL REFERENCES gitlab_instances(id),
  external_group_id  TEXT NOT NULL,
  full_path          TEXT NOT NULL,
  credential_ref_id  TEXT,
  inherit_subgroups  INTEGER NOT NULL DEFAULT 1,
  status             TEXT NOT NULL,
  UNIQUE (workspace_id, gitlab_instance_id, external_group_id),
  UNIQUE (workspace_id, gitlab_instance_id, full_path)
);

CREATE TABLE IF NOT EXISTS multica_instances (
  id                         TEXT PRIMARY KEY,
  workspace_id               TEXT NOT NULL REFERENCES workspaces(id),
  name                       TEXT NOT NULL,
  base_url                   TEXT NOT NULL,
  external_id                TEXT,
  management_credential_ref_id TEXT,
  status                     TEXT NOT NULL,
  capabilities_json          TEXT NOT NULL DEFAULT '[]',
  UNIQUE (workspace_id, name),
  UNIQUE (workspace_id, external_id)
);

CREATE TABLE IF NOT EXISTS multica_workspaces (
  id                  TEXT PRIMARY KEY,
  workspace_id        TEXT NOT NULL REFERENCES workspaces(id),
  multica_instance_id TEXT NOT NULL REFERENCES multica_instances(id),
  external_id         TEXT NOT NULL,
  name                TEXT NOT NULL,
  UNIQUE (workspace_id, multica_instance_id, external_id)
);

CREATE TABLE IF NOT EXISTS multica_projects (
  id                    TEXT PRIMARY KEY,
  workspace_id          TEXT NOT NULL REFERENCES workspaces(id),
  multica_instance_id   TEXT NOT NULL REFERENCES multica_instances(id),
  multica_workspace_id  TEXT NOT NULL REFERENCES multica_workspaces(id),
  external_id           TEXT NOT NULL,
  title                 TEXT NOT NULL,
  UNIQUE (workspace_id, multica_instance_id, external_id)
);

CREATE TABLE IF NOT EXISTS connections (
  id                         TEXT PRIMARY KEY,
  workspace_id               TEXT NOT NULL REFERENCES workspaces(id),
  name                       TEXT NOT NULL,
  source_gitlab_instance_id  TEXT NOT NULL REFERENCES gitlab_instances(id),
  source_project_external_id TEXT NOT NULL,
  source_project_path        TEXT NOT NULL DEFAULT '',
  source_project_web_url     TEXT NOT NULL DEFAULT '',
  source_project_ssh_url     TEXT NOT NULL DEFAULT '',
  source_project_https_url   TEXT NOT NULL DEFAULT '',
  target_multica_instance_id TEXT NOT NULL REFERENCES multica_instances(id),
  target_project_external_id TEXT NOT NULL,
  target_project_name        TEXT NOT NULL DEFAULT '',
  target_project_web_url     TEXT NOT NULL DEFAULT '',
  status                     TEXT NOT NULL,
  configured_at              TEXT NOT NULL,
  ready_at                   TEXT,
  disabled_at                TEXT,
  created_by                 TEXT REFERENCES accounts(id)
);

CREATE UNIQUE INDEX IF NOT EXISTS connections_active_source_uq
  ON connections (workspace_id, source_gitlab_instance_id, source_project_external_id)
  WHERE status <> 'disabled';

CREATE UNIQUE INDEX IF NOT EXISTS connections_active_target_uq
  ON connections (workspace_id, target_multica_instance_id, target_project_external_id)
  WHERE status <> 'disabled';

CREATE TABLE IF NOT EXISTS managed_resources (
  id             TEXT PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id),
  connection_id  TEXT NOT NULL REFERENCES connections(id),
  kind           TEXT NOT NULL,
  provider       TEXT NOT NULL,
  instance_id    TEXT NOT NULL,
  external_id    TEXT NOT NULL,
  ownership      TEXT NOT NULL CHECK (ownership IN ('managed', 'adopted', 'external')),
  management_mark TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL,
  snapshot_json  TEXT NOT NULL DEFAULT '{}',
  UNIQUE (workspace_id, connection_id, kind, instance_id, external_id)
);

CREATE TABLE IF NOT EXISTS hooks (
  id             TEXT PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id),
  connection_id  TEXT NOT NULL REFERENCES connections(id),
  provider       TEXT NOT NULL,
  instance_id    TEXT NOT NULL,
  external_id    TEXT NOT NULL,
  signing_ref_id TEXT,
  status         TEXT NOT NULL,
  UNIQUE (workspace_id, instance_id, external_id)
);

CREATE TABLE IF NOT EXISTS hook_routes (
  id                TEXT PRIMARY KEY,
  workspace_id      TEXT NOT NULL REFERENCES workspaces(id),
  connection_id     TEXT NOT NULL REFERENCES connections(id),
  source_instance_id TEXT NOT NULL,
  source_project_external_id TEXT NOT NULL,
  behavior_key      TEXT NOT NULL,
  behavior_version  TEXT NOT NULL,
  flow_id           TEXT NOT NULL,
  flow_version      INTEGER NOT NULL,
  event_filter_json TEXT NOT NULL DEFAULT '{}',
  hook_id           TEXT NOT NULL REFERENCES hooks(id),
  status            TEXT NOT NULL,
  UNIQUE (workspace_id, connection_id, behavior_key, behavior_version, flow_id, flow_version)
);

CREATE TABLE IF NOT EXISTS connector_types (
  id             TEXT PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id),
  key            TEXT NOT NULL,
  version        TEXT NOT NULL,
  display_name   TEXT NOT NULL,
  provider       TEXT NOT NULL,
  status         TEXT NOT NULL,
  definition_json TEXT NOT NULL,
  UNIQUE (workspace_id, key, version)
);

CREATE TABLE IF NOT EXISTS connector_behaviors (
  id                    TEXT PRIMARY KEY,
  workspace_id          TEXT NOT NULL REFERENCES workspaces(id),
  connector_type_id     TEXT NOT NULL REFERENCES connector_types(id),
  connector_type_key    TEXT NOT NULL,
  connector_type_version TEXT NOT NULL,
  key                   TEXT NOT NULL,
  version               TEXT NOT NULL,
  display_name          TEXT NOT NULL,
  direction             TEXT NOT NULL CHECK (direction IN ('input', 'output')),
  adapter_operation     TEXT NOT NULL,
  status                TEXT NOT NULL,
  definition_json       TEXT NOT NULL,
  UNIQUE (workspace_id, key, version)
);

CREATE TABLE IF NOT EXISTS data_models (
  id              TEXT PRIMARY KEY,
  workspace_id    TEXT NOT NULL REFERENCES workspaces(id),
  key             TEXT NOT NULL,
  version         TEXT NOT NULL,
  display_name    TEXT NOT NULL,
  status          TEXT NOT NULL,
  definition_json TEXT NOT NULL,
  UNIQUE (workspace_id, key, version)
);

CREATE TABLE IF NOT EXISTS flows (
  id             TEXT PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id),
  connection_id  TEXT NOT NULL REFERENCES connections(id),
  name           TEXT NOT NULL,
  description    TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL,
  active_version INTEGER NOT NULL DEFAULT 0,
  created_by     TEXT REFERENCES accounts(id),
  updated_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS flow_templates (
  id              TEXT PRIMARY KEY,
  workspace_id    TEXT NOT NULL REFERENCES workspaces(id),
  key             TEXT NOT NULL,
  version         TEXT NOT NULL,
  name            TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  graph_json      TEXT NOT NULL,
  status          TEXT NOT NULL,
  UNIQUE (workspace_id, key, version)
);

CREATE TABLE IF NOT EXISTS flow_versions (
  id              TEXT PRIMARY KEY,
  workspace_id    TEXT NOT NULL REFERENCES workspaces(id),
  flow_id         TEXT NOT NULL REFERENCES flows(id),
  version         INTEGER NOT NULL,
  status          TEXT NOT NULL,
  graph_json      TEXT NOT NULL,
  compiled_plan_json TEXT NOT NULL DEFAULT '{}',
  behavior_refs_json TEXT NOT NULL DEFAULT '[]',
  model_refs_json    TEXT NOT NULL DEFAULT '[]',
  published_at    TEXT,
  published_by    TEXT REFERENCES accounts(id),
  UNIQUE (workspace_id, flow_id, version)
);

CREATE UNIQUE INDEX IF NOT EXISTS flow_versions_one_active
  ON flow_versions (workspace_id, flow_id)
  WHERE status = 'published';

CREATE TABLE IF NOT EXISTS flow_executions (
  id                   TEXT PRIMARY KEY,
  workspace_id         TEXT NOT NULL REFERENCES workspaces(id),
  connection_id        TEXT NOT NULL REFERENCES connections(id),
  flow_id              TEXT NOT NULL REFERENCES flows(id),
  flow_version_id      TEXT NOT NULL REFERENCES flow_versions(id),
  flow_version         INTEGER NOT NULL,
  event_id             TEXT NOT NULL,
  delivery_id          TEXT NOT NULL DEFAULT '',
  idempotency_key      TEXT NOT NULL,
  correlation_id       TEXT NOT NULL,
  status               TEXT NOT NULL,
  current_node_id      TEXT NOT NULL DEFAULT '',
  provider_request_ids_json TEXT NOT NULL DEFAULT '[]',
  error_category       TEXT NOT NULL DEFAULT '',
  created_at           TEXT NOT NULL,
  updated_at           TEXT NOT NULL,
  UNIQUE (workspace_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS node_executions (
  id                   TEXT PRIMARY KEY,
  workspace_id         TEXT NOT NULL REFERENCES workspaces(id),
  execution_id         TEXT NOT NULL REFERENCES flow_executions(id),
  node_id              TEXT NOT NULL,
  status               TEXT NOT NULL,
  attempt              INTEGER NOT NULL DEFAULT 0,
  input_snapshot_json  TEXT NOT NULL DEFAULT '{}',
  output_snapshot_json TEXT NOT NULL DEFAULT '{}',
  error_category       TEXT NOT NULL DEFAULT '',
  provider_request_id  TEXT NOT NULL DEFAULT '',
  UNIQUE (workspace_id, execution_id, node_id, attempt)
);

CREATE TABLE IF NOT EXISTS audit_events (
  id               TEXT PRIMARY KEY,
  workspace_id     TEXT NOT NULL REFERENCES workspaces(id),
  actor_account_id TEXT REFERENCES accounts(id),
  action           TEXT NOT NULL,
  entity_type      TEXT NOT NULL,
  entity_id        TEXT NOT NULL,
  payload_json     TEXT NOT NULL DEFAULT '{}',
  created_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS correlations (
  id                    TEXT PRIMARY KEY,
  workspace_id          TEXT NOT NULL REFERENCES workspaces(id),
  connection_id         TEXT NOT NULL REFERENCES connections(id),
  source_identity       TEXT NOT NULL,
  publication_identity  TEXT NOT NULL,
  target_identity       TEXT NOT NULL DEFAULT '',
  flow_execution_id     TEXT REFERENCES flow_executions(id),
  provider_request_id   TEXT NOT NULL DEFAULT '',
  created_at            TEXT NOT NULL,
  UNIQUE (workspace_id, connection_id, source_identity, publication_identity)
);

CREATE TABLE IF NOT EXISTS idempotency_keys (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  scope        TEXT NOT NULL,
  key          TEXT NOT NULL,
  claimed_at   TEXT NOT NULL,
  expires_at   TEXT,
  UNIQUE (workspace_id, scope, key)
);

CREATE TABLE IF NOT EXISTS secrets (
  ref_id       TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  alias        TEXT NOT NULL,
  kind         TEXT NOT NULL,
  nonce        BLOB NOT NULL,
  ciphertext   BLOB NOT NULL,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE (workspace_id, alias)
);

CREATE TABLE IF NOT EXISTS jobs (
  id             TEXT PRIMARY KEY,
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id),
  kind           TEXT NOT NULL,
  payload_json   TEXT NOT NULL,
  available_at   TEXT NOT NULL,
  lease_until    TEXT,
  leased_by      TEXT NOT NULL DEFAULT '',
  attempt_count  INTEGER NOT NULL DEFAULT 0,
  status         TEXT NOT NULL,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS jobs_claim_idx
  ON jobs (status, available_at, lease_until);
