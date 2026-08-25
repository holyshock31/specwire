# Design: reset-specwire-v2-contract

## Context

The repository has three current capabilities under `openspec/specs/`, but the `workflow` and `bridge` contracts still describe the retired `proposal-ready` publication path alongside the v2 Issue path. The running Bridge also routes Push Hook publication trailers through the old creation path and builds projection descriptions around `approved_commit_sha` and client-side Agent instructions.

The accepted ADRs and `CONTEXT.md` establish the target boundary: GitLab owns the published change, Multica is a derived execution projection, v2 `change` Issues are the only new-publication entry point, and SpecWire Skills remain independent clients. The reset therefore has to align the behavior contract and the runtime implementation together. Historical change artifacts remain historical records and are not rewritten.

## Goals / Non-Goals

**Goals:**

- Make the current main specs describe one v2 integration contract.
- Make runtime routing, projection metadata, correlation, and archive completion agree with that contract.
- Remove new-work support for `proposal-ready` Push Hook while retaining `archived` Push Hook as the completion signal.
- Keep the Multica adapter current and narrow: resolve project, create projection, apply initial state/assignment, and complete projection.
- Make the document authority model executable for future agents and Skills.

**Non-Goals:**

- Adding another execution system or generalizing the current Multica implementation.
- Moving or deleting the separately managed SpecWire Skills installation.
- Rebuilding the Multica platform, adding notifications, or implementing general identity/project provisioning.
- Changing historical archived change artifacts merely to remove old terminology.

## Decisions

### 1. Use a single v2 event matrix

The Bridge owns only the following externally visible event boundary:

```text
GitLab Issue Hook
  object_kind=issue, action=open, label=change
        │
        ▼
  validate publication fields
  (change_id, branch, branch_head_sha)
        │
        ▼
  create one Multica projection + correlation

GitLab Push Hook on main
  SpecWire-Event: archived
        │
        ▼
  complete the correlated projection
  close linked publication Issues when configured
```

Pushes containing `proposal-ready`, ordinary branch pushes, MR activity, and local Skill commands are not publication inputs. They must not create a new projection.

### 2. Keep source, projection, and client responsibilities separate

- GitLab publication metadata is the source-side input; Multica state is a rebuildable projection.
- Bridge parses only the small protocol envelope and does not clone or interpret OpenSpec content.
- SpecWire Skills may author OpenSpec, create branches, push branches, run Agents, and manage MRs, but these steps are not Bridge behavior.
- `CONTEXT.md` owns vocabulary, ADRs own rationale, and OpenSpec main specs own observable behavior.

### 3. Freeze v2 publication metadata at the Issue event

The Issue description is parsed once for `change_id`, `branch`, and `branch_head_sha`. The projection description must carry the source branch and frozen SHA rather than the v1 `approved_commit_sha` field or instructions copied from a Skill. The stable key uses the GitLab project, change ID, frozen SHA, and resolved Multica project ID. A later frozen revision is a new publication and must not mutate an existing projection.

### 4. Make publication correlation durable

After successful projection creation, persist the GitLab project, Issue IID, change ID, branch, and frozen SHA. The correlation is the only data needed by archive completion to close the publication Issue; no OpenSpec checkout or Skill state is required. Correlation persistence is part of successful publication handling and must be retryable if it fails.

### 5. Remove v1 creation behavior, preserve data-safe completion

Delete the v1 proposal-ingestion branch, parser path, active fixtures, and tests that assert new cards from `proposal-ready`. Keep archive lookup tolerant of projections created before the reset so existing work can still complete, but do not accept a new v1 publication. This is compatibility for existing data, not support for the v1 protocol.

### 6. Treat configuration context as derived authoring guidance

Update `openspec/config.yaml` so newly generated artifacts describe Issue publication and archive completion, omit local Skill/Agent ownership, and use `change` as a label rather than a tag. The config remains guidance for authoring and validation; the main specs remain the behavior authority.

## Risks / Trade-offs

- **[Existing v1 clients]** → New v1 publication attempts will be ignored; the independent Skills client must use the v2 Issue protocol. Existing v1 projections remain completable through archive data already stored by Bridge.
- **[Projection description consumers]** → Agents or scripts reading `approved_commit_sha` may need the v2 `branch_head_sha` and branch fields. Update the client-side Skill contract separately and validate the complete v2 card description end to end.
- **[Partial correlation failure]** → A projection created before its Issue link is persisted can make archive closure incomplete. Treat correlation persistence as retryable publication work and add a test that proves replay repairs the link without creating a second projection.
- **[Spec/code migration window]** → During implementation, the delta describes the target while the main specs still describe current behavior. Do not archive the change until v1 routing is removed and the v2 tests pass.

## Migration Plan

1. Implement the Bridge and test changes from the delta while the change remains active.
2. Update `openspec/config.yaml` and active explanatory documents; leave historical change artifacts untouched.
3. Run unit, race, contract-validation, and available Compose end-to-end checks.
4. Promote the accepted delta into `openspec/specs/` through the normal OpenSpec archive path, then archive this change.

Rollback, if required before archive, is to stop applying this change and keep the current main specs/runtime together; do not partially promote only the documentation or only the handler changes.

## Open Questions

None. The remaining decisions are implementation and verification tasks, not unresolved contract choices.
