ALTER TABLE hooks ADD COLUMN source_project_external_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS hooks_project_uq
  ON hooks (workspace_id, instance_id, source_project_external_id)
  WHERE source_project_external_id <> '';
