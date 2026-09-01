package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"specwire/bridge/internal/domain"
)

func (s *Store) SaveFlowDraft(ctx context.Context, workspaceID, flowID domain.ID, graph domain.FlowGraph) error {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return err
	}
	if flowID.Empty() {
		return fmt.Errorf("%w: flow id is required", domain.ErrInvalid)
	}
	var flowWorkspace string
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM flows WHERE id = ?`, flowID).Scan(&flowWorkspace); err == sql.ErrNoRows {
		return fmt.Errorf("%w: flow %s", domain.ErrNotFound, flowID)
	} else if err != nil {
		return fmt.Errorf("check flow draft workspace: %w", err)
	} else if domain.ID(flowWorkspace) != workspaceID {
		return fmt.Errorf("%w: flow draft belongs to another workspace", domain.ErrForbidden)
	}
	if err := graph.ValidateShape(); err != nil {
		return err
	}
	encoded, err := json.Marshal(graph)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO flow_drafts(flow_id, workspace_id, graph_json, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(flow_id) DO UPDATE SET graph_json = excluded.graph_json, updated_at = excluded.updated_at
		WHERE flow_drafts.workspace_id = excluded.workspace_id`, flowID, workspaceID, string(encoded), time.Now().UTC().Format(time.RFC3339Nano))
	return constraintError("save flow draft", err)
}

func (s *Store) GetFlowDraft(ctx context.Context, workspaceID, flowID domain.ID) (domain.FlowGraph, error) {
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT graph_json FROM flow_drafts WHERE workspace_id = ? AND flow_id = ?`, workspaceID, flowID).Scan(&encoded)
	if err == sql.ErrNoRows {
		return domain.FlowGraph{}, fmt.Errorf("%w: flow draft %s", domain.ErrNotFound, flowID)
	}
	if err != nil {
		return domain.FlowGraph{}, fmt.Errorf("get flow draft: %w", err)
	}
	var graph domain.FlowGraph
	if err := json.Unmarshal([]byte(encoded), &graph); err != nil {
		return domain.FlowGraph{}, fmt.Errorf("decode flow draft: %w", err)
	}
	return graph, nil
}
