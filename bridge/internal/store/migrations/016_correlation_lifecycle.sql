ALTER TABLE correlations ADD COLUMN lifecycle_status TEXT NOT NULL DEFAULT 'active';

CREATE INDEX IF NOT EXISTS correlations_lifecycle_idx
  ON correlations (workspace_id, connection_id, source_identity, publication_identity, lifecycle_status);
