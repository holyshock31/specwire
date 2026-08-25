# Domain documentation

This is a single-domain repository. Keep domain context in one root `CONTEXT.md`; do not introduce a `CONTEXT-MAP.md` unless the repository becomes genuinely multi-package or adopts that layout explicitly.

- `CONTEXT.md` is the canonical source for system boundaries, vocabulary, and cross-component context.
- `docs/adr/` is the canonical home for accepted architectural decisions.
- `openspec/specs/` contains the current behavior contracts.
- `openspec/changes/<change-id>/` contains active proposed deltas and their implementation tasks; completed changes move to `openspec/changes/archive/` as historical records.

Update `CONTEXT.md` when shared domain language or boundaries change. Add an ADR for a durable architectural decision. Use OpenSpec for a behavior or workflow change; do not duplicate those contracts in agent instructions.
