# Agent instructions

Read `CONTEXT.md` before making cross-component changes. Treat `openspec/specs/` as the current behavior contract, `openspec/changes/` as the change workflow, and `docs/adr/` as the record of accepted architectural decisions. Keep SpecWire's integration boundary separate from the workflow skills that act as its clients.

## Agent skills

- [Issue tracker](docs/agents/issue-tracker.md) — GitLab Issue and merge-request conventions.
- [Triage labels](docs/agents/triage-labels.md) — canonical triage labels and workflow-label boundary.
- [Domain documentation](docs/agents/domain.md) — where context, decisions, specs, and change artifacts belong.

## Document responsibilities and authority

Use one authoritative document set for each kind of information. Do not create a second behavior contract in a Skill, README, or design note.

- `openspec/specs/` is the current SpecWire behavior contract: integration events, accepted inputs and outputs, lifecycle, correlation, idempotency, recovery, and control-plane behavior.
- `openspec/changes/<change-id>/` is the proposal for future behavior. Its proposal, design, delta specs, and tasks are authoritative only for that change until it is implemented, merged, and archived into `openspec/specs/`.
- `CONTEXT.md` is the canonical source for domain vocabulary, system boundaries, and cross-component context. OpenSpec requirements should use its terms rather than redefining them.
- `docs/adr/` records accepted architectural decisions and their rationale. It explains why a boundary or tradeoff exists; it does not replace executable behavior requirements.
- `AGENTS.md` and `docs/agents/` define repository and agent operating procedures. They may point to the sources above but must not redefine SpecWire behavior.
- The separately managed SpecWire Skills repository documents client-side workflow commands and local repository operations. Skills consume the SpecWire contract; they are not part of the SpecWire runtime or its source of truth.
- `README.md` and other explanatory documents are derived documentation. Update them from the authoritative sources instead of treating them as requirements.

Matt Skills are an authoring workflow, not an additional specification system: use grilling/domain modeling to clarify `CONTEXT.md` and ADRs, use `to-spec` to capture a proposed OpenSpec change, and use implementation/ticket outputs only as consumers or planning artifacts. Never write a Matt-generated draft directly into `openspec/specs/` without review and acceptance.

When documents disagree, classify the disagreement first. For current SpecWire behavior, reconcile against `openspec/specs/`; for terminology, update `CONTEXT.md`; for architectural rationale, supersede or amend the relevant ADR. Do not silently resolve a conflict by copying one document over another. Keep `openspec/config.yaml` aligned with these rules, but treat it as authoring guidance rather than the behavior contract.
