CREATE TABLE IF NOT EXISTS flow_drafts (
  flow_id       TEXT PRIMARY KEY REFERENCES flows(id),
  workspace_id  TEXT NOT NULL REFERENCES workspaces(id),
  graph_json    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
