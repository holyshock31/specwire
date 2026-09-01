ALTER TABLE flow_executions ADD COLUMN error_message TEXT NOT NULL DEFAULT '';
ALTER TABLE node_executions ADD COLUMN error_message TEXT NOT NULL DEFAULT '';
