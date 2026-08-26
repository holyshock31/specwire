# Agent instructions

Read `CONTEXT.md` before making cross-component changes, then follow its pointers to the published domain context and current contracts. Treat `openspec/drafts/` as pre-Change exploration, `openspec/changes/` as the complete change workflow, and `openspec/specs/` as the current published knowledge base. Keep SpecWire's integration boundary separate from the workflow skills that act as its clients.

## Agent skills

- [Issue tracker](docs/agents/issue-tracker.md) — GitLab Issue and merge-request conventions.
- [Triage labels](docs/agents/triage-labels.md) — canonical triage labels and workflow-label boundary.
- [Domain documentation](docs/agents/domain.md) — where context, decisions, specs, and change artifacts belong.

## Document responsibilities and authority

Use one authoritative document set for each kind of information. Do not create a second behavior contract in a Skill, README, or design note.

- `openspec/specs/` is the current SpecWire published knowledge base, split by type: `behavior/` contains observable behavior contracts, `domain/` contains vocabulary and models, `architecture/` contains current design and accepted ADRs, and `experience/` contains current product and interaction contracts.
- `openspec/drafts/<draft-id>/` contains exploration that has not yet become a coherent change: candidate domain concepts, proposed ADRs, prototypes, technical design, and roadmap material. It is not a behavior contract and does not directly authorize implementation.
- `openspec/changes/<change-id>/` is the working set for a coherent proposed change. For greenfield or cross-component work, it may contain synthesized decisions, product prototypes, technical design, proposed architectural decisions, proposal, delta specs, and tasks. Its `specs/` taxonomy mirrors the published `openspec/specs/` taxonomy for the categories it changes, but its documents describe deltas rather than the full current state.
- `CONTEXT.md` is the Agent entry point; the canonical published domain vocabulary, system boundaries, and cross-component context are in `openspec/specs/domain/`. OpenSpec requirements should use those terms rather than redefining them.
- `openspec/specs/architecture/adr/` records accepted architectural decisions and their rationale. It explains why a boundary or tradeoff exists; it does not replace executable behavior requirements.
- `AGENTS.md` and `docs/agents/` define repository and agent operating procedures. They may point to the sources above but must not redefine SpecWire behavior.
- The separately managed SpecWire Skills repository documents client-side workflow commands and local repository operations. Skills consume the SpecWire contract; they are not part of the SpecWire runtime or its source of truth.
- `README.md` and other explanatory documents are derived documentation. Update them from the authoritative sources instead of treating them as requirements.
- `openspec/config.yaml` controls OpenSpec authoring context, artifact-specific guidance, and apply/archive hints. It is authoring configuration, not the source of SpecWire behavior, domain terminology, or architecture.

## Change-first design workflow

For greenfield, cross-component, or materially re-modeled work:

- If the idea is still unshaped, record it in `openspec/drafts/<draft-id>/`. Once its scope is coherent, synthesize it into one `openspec/changes/<change-id>/` rather than continuing to split the design across documents.
- Record synthesized Grill results, accepted and rejected options, product prototypes, technical design, and proposed ADR rationale in the change. Preserve decisions as concise rationale and consequences; raw conversation transcripts are not the contract.
- Keep proposed domain, architecture, experience, and behavior changes in the change until they are accepted. A Change-specific prototype belongs in its `prototype/` directory; discarded directions remain with the change history.
- Keep proposed behavior and other published-state deltas in the change's matching `specs/` categories. Do not write directly to `openspec/specs/` during design.
- After implementation and acceptance, reconcile the change into the matching published categories: behavior → `specs/behavior/`, domain → `specs/domain/`, architecture/accepted ADR → `specs/architecture/`, and current product experience → `specs/experience/`. This is a merge/reconciliation, not an append-only copy.
- Retain the complete change, including prototypes and design history, under `openspec/changes/archive/`.
- Treat repository-local document rules as the routing authority when a generic Skill suggests a different output location. Skills provide authoring procedures; they do not create a second specification system.

For the detailed artifact-routing and archive rules, read [Domain documentation](docs/agents/domain.md) when creating, revising, or archiving a change.

Matt Skills are an authoring workflow, not an additional specification system: use grilling/domain modeling to clarify the active change first, use `to-spec` to synthesize its proposed OpenSpec behavior, and use implementation/ticket outputs only as consumers or planning artifacts. Never write a Matt-generated draft directly into `openspec/specs/` without review and acceptance.

When documents disagree, classify the disagreement first. For behavior, reconcile against `openspec/specs/behavior/`; for terminology, reconcile against `openspec/specs/domain/`; for architecture and rationale, supersede or amend the relevant document under `openspec/specs/architecture/`; for product experience, reconcile against `openspec/specs/experience/`. Do not silently resolve a conflict by copying one document over another. Keep `openspec/config.yaml` aligned with these rules, but treat it as authoring guidance rather than the published knowledge base.
