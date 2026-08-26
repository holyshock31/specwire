# Domain documentation

This is a single-domain repository. `CONTEXT.md` is the Agent entry point; the published domain context lives under `openspec/specs/domain/`. Do not introduce a `CONTEXT-MAP.md` unless the repository becomes genuinely multi-package or adopts that layout explicitly.

- `openspec/specs/domain/` is the canonical source for published system boundaries, vocabulary, and cross-component context.
- `openspec/specs/architecture/adr/` is the canonical home for accepted architectural decisions; current core architecture lives under `openspec/specs/architecture/`.
- `openspec/specs/behavior/` contains current behavior contracts; `domain/`, `architecture/`, and `experience/` contain their corresponding published knowledge.
- `openspec/drafts/` contains pre-Change exploration; `openspec/changes/<change-id>/` contains the complete proposed change and implementation tasks; completed changes move to `openspec/changes/archive/` as historical records.
- `docs/agents/` contains repository and Agent operating procedures, not product drafts or current behavior contracts.

Update the published domain context when shared domain language or boundaries change. Add an ADR under the architecture area for a durable architectural decision. Use OpenSpec for a behavior or workflow change; do not duplicate those contracts in agent instructions.
