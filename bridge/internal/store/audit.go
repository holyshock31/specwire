package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"specwire/bridge/internal/domain"
)

// CreateAuditEvent persists a redacted control-plane or runtime audit record.
// Redaction is repeated at this boundary so a caller cannot accidentally make
// an otherwise safe API leak a credential through an audit payload.
func (s *Store) CreateAuditEvent(ctx context.Context, event domain.AuditEvent) error {
	if event.ID.Empty() {
		event.ID = domain.NewID()
	}
	if err := requireWorkspaceID(event.WorkspaceID); err != nil {
		return err
	}
	if strings.TrimSpace(event.Action) == "" || strings.TrimSpace(event.EntityType) == "" || event.EntityID.Empty() {
		return fmt.Errorf("%w: audit action, entity_type and entity_id are required", domain.ErrInvalid)
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = s.now()
	}
	if event.ActorAccountID != "" {
		var accountID string
		err := s.db.QueryRowContext(ctx, `SELECT a.id FROM accounts a
			JOIN workspace_memberships m ON m.account_id = a.id
			WHERE a.id = ? AND m.workspace_id = ? AND m.status = 'active'`, event.ActorAccountID, event.WorkspaceID).Scan(&accountID)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: audit actor is not a Workspace member", domain.ErrForbidden)
		}
		if err != nil {
			return fmt.Errorf("check audit actor: %w", err)
		}
	}
	payload, err := marshalJSON(redactAuditValue(event.Payload), "{}")
	if err != nil {
		return fmt.Errorf("marshal audit payload: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit_events
		(id, workspace_id, actor_account_id, action, entity_type, entity_id, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.WorkspaceID, nullID(event.ActorAccountID), event.Action,
		event.EntityType, event.EntityID, payload, event.CreatedAt.Format(time.RFC3339Nano))
	return constraintError("create audit event", err)
}

func (s *Store) ListAuditEvents(ctx context.Context, workspaceID domain.ID, entityType string, entityID domain.ID, limit int) ([]domain.AuditEvent, error) {
	if err := requireWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, actor_account_id, action, entity_type, entity_id, payload_json, created_at
		FROM audit_events WHERE workspace_id = ?`
	args := []any{workspaceID}
	if strings.TrimSpace(entityType) != "" {
		query += ` AND entity_type = ?`
		args = append(args, entityType)
	}
	if !entityID.Empty() {
		query += ` AND entity_id = ?`
		args = append(args, entityID)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	var result []domain.AuditEvent
	for rows.Next() {
		var item domain.AuditEvent
		var actor sql.NullString
		var payload, created string
		if err := rows.Scan(&item.ID, &actor, &item.Action, &item.EntityType, &item.EntityID, &payload, &created); err != nil {
			return nil, err
		}
		item.WorkspaceID = workspaceID
		if actor.Valid {
			item.ActorAccountID = domain.ID(actor.String)
		}
		if err := json.Unmarshal([]byte(payload), &item.Payload); err != nil {
			return nil, fmt.Errorf("decode audit payload: %w", err)
		}
		item.CreatedAt, err = decodeTime(created)
		if err != nil {
			return nil, fmt.Errorf("decode audit created_at: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

var auditSensitiveKeys = map[string]struct{}{
	"token": {}, "access_token": {}, "refresh_token": {}, "private_token": {},
	"password": {}, "passwd": {}, "secret": {}, "client_secret": {},
	"authorization": {}, "authorization_code": {}, "webhook_secret": {},
	"signing_secret": {}, "signing_token": {}, "api_key": {}, "apikey": {},
}

func redactAuditValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
			if _, sensitive := auditSensitiveKeys[normalized]; sensitive || strings.Contains(normalized, "token") || strings.Contains(normalized, "password") || strings.Contains(normalized, "client_secret") || strings.Contains(normalized, "authorization") || strings.Contains(normalized, "signing_secret") {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = redactAuditValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = redactAuditValue(child)
		}
		return out
	default:
		return value
	}
}
