package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"specwire/bridge/internal/domain"
)

func (s *Store) CreateFlowTemplate(ctx context.Context, template domain.FlowTemplate) error {
	if template.ID.Empty() {
		template.ID = domain.NewID()
	}
	if err := requireWorkspaceID(template.WorkspaceID); err != nil {
		return err
	}
	if template.Key == "" || template.Version == "" || template.Name == "" {
		return fmt.Errorf("%w: incomplete flow template", domain.ErrInvalid)
	}
	if err := template.Graph.ValidateShape(); err != nil {
		return err
	}
	graph, err := json.Marshal(template.Graph)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO flow_templates(id, workspace_id, key, version, name, description, graph_json, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, template.ID, template.WorkspaceID, template.Key, template.Version, template.Name, template.Description, string(graph), template.Status)
	return constraintError("create flow template", err)
}

func (s *Store) GetFlowTemplate(ctx context.Context, workspaceID domain.ID, key, version string) (domain.FlowTemplate, error) {
	var template domain.FlowTemplate
	var graph string
	err := s.db.QueryRowContext(ctx, `SELECT id, key, version, name, description, graph_json, status FROM flow_templates
		WHERE workspace_id = ? AND key = ? AND version = ?`, workspaceID, key, version).Scan(&template.ID, &template.Key, &template.Version, &template.Name, &template.Description, &graph, &template.Status)
	if err == sql.ErrNoRows {
		return domain.FlowTemplate{}, fmt.Errorf("%w: flow template %s@%s", domain.ErrNotFound, key, version)
	}
	if err != nil {
		return domain.FlowTemplate{}, err
	}
	template.WorkspaceID = workspaceID
	if err := json.Unmarshal([]byte(graph), &template.Graph); err != nil {
		return domain.FlowTemplate{}, err
	}
	return template, nil
}

func (s *Store) ListFlowTemplates(ctx context.Context, workspaceID domain.ID) ([]domain.FlowTemplate, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, key, version, name, description, graph_json, status FROM flow_templates
		WHERE workspace_id = ? ORDER BY key, version`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FlowTemplate
	for rows.Next() {
		var item domain.FlowTemplate
		var graph string
		if err := rows.Scan(&item.ID, &item.Key, &item.Version, &item.Name, &item.Description, &graph, &item.Status); err != nil {
			return nil, err
		}
		item.WorkspaceID = workspaceID
		if err := json.Unmarshal([]byte(graph), &item.Graph); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SeedFlowTemplates(ctx context.Context, workspaceID domain.ID, templates []domain.FlowTemplate) error {
	for _, template := range templates {
		template.WorkspaceID = workspaceID
		if err := s.CreateFlowTemplate(ctx, template); err != nil {
			if !isConflict(err) {
				return err
			}
		}
	}
	return nil
}

func isConflict(err error) bool { return err != nil && errors.Is(err, domain.ErrConflict) }
