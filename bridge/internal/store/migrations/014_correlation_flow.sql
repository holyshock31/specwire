-- Correlations belong to an intended Flow action. Rebuild the table so the
-- original connection-wide uniqueness constraint cannot collapse projections
-- created by two matching Flows. Existing rows retain their Flow when it can
-- be resolved through the stored execution; legacy rows remain readable only
-- to migration tooling because they have no unambiguous Flow owner.
CREATE TABLE correlations_v2 (
  id                    TEXT PRIMARY KEY,
  workspace_id          TEXT NOT NULL REFERENCES workspaces(id),
  connection_id         TEXT NOT NULL REFERENCES connections(id),
  flow_id               TEXT REFERENCES flows(id),
  source_identity       TEXT NOT NULL,
  source_issue_iid      INTEGER NOT NULL DEFAULT 0,
  source_issue_iids_json TEXT NOT NULL DEFAULT '[]',
  publication_identity  TEXT NOT NULL,
  target_identity       TEXT NOT NULL DEFAULT '',
  flow_execution_id     TEXT REFERENCES flow_executions(id),
  provider_request_id   TEXT NOT NULL DEFAULT '',
  created_at            TEXT NOT NULL,
  UNIQUE (workspace_id, connection_id, flow_id, source_identity, publication_identity)
);

INSERT INTO correlations_v2
  (id, workspace_id, connection_id, flow_id, source_identity, source_issue_iid,
   source_issue_iids_json, publication_identity, target_identity,
   flow_execution_id, provider_request_id, created_at)
SELECT c.id, c.workspace_id, c.connection_id, e.flow_id, c.source_identity,
       c.source_issue_iid, c.source_issue_iids_json, c.publication_identity,
       c.target_identity, c.flow_execution_id, c.provider_request_id, c.created_at
FROM correlations c
LEFT JOIN flow_executions e ON e.id = c.flow_execution_id;

DROP TABLE correlations;
ALTER TABLE correlations_v2 RENAME TO correlations;
