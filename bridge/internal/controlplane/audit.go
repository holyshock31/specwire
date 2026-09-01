package controlplane

import (
	"context"

	"specwire/bridge/internal/domain"
)

// auditRecorder is optional at the use-case seam so focused provider fakes do
// not need to implement the audit repository.  The production SQLite store
// implements it; audit failures are intentionally best-effort after an
// already completed provider call.
type auditRecorder interface {
	CreateAuditEvent(context.Context, domain.AuditEvent) error
}

func recordAudit(ctx context.Context, target any, event domain.AuditEvent) {
	recorder, ok := target.(auditRecorder)
	if !ok {
		return
	}
	_ = recorder.CreateAuditEvent(ctx, event)
}
