## 1. Establish the v2 contract baseline

- [x] 1.1 Verify the delta specs against `CONTEXT.md` and ADR 0001–0005; remove any remaining requirement that assigns local repository, Agent, MR, or Skill behavior to SpecWire.
- [x] 1.2 Update `openspec/config.yaml` to describe Issue label `change` as the only publication input, `archived` Push Hook as completion only, and the SpecWire/Skills boundary.
- [x] 1.3 Search active (non-archived) docs, tests, fixtures, and comments for `proposal-ready`, `tag=change`, and v1 publication wording; remove or rewrite active references while preserving historical change artifacts.

## 2. Make Bridge routing v2-only

- [x] 2.1 Change Push Hook handling so only `SpecWire-Event: archived` on the configured main ref can produce completion side effects; `proposal-ready` and unknown publication trailers are ignored.
- [x] 2.2 Remove the v1 proposal-ingestion creation path and its active parser/description behavior from the Bridge runtime.
- [x] 2.3 Keep Issue Hook handling limited to `object_kind=issue`, action `open`, the `change` label, allowlisted project, and valid `change_id`/`branch`/`branch_head_sha` fields.
- [x] 2.4 Update Multica projection description generation to include repository, `change_id`, source branch, frozen `branch_head_sha`, target branch, initial status, and assignment context without copying Skill instructions or using `approved_commit_sha`.
- [x] 2.5 Preserve immutable publication idempotency: same project/change/frozen SHA/target project replays are duplicates; failed creation can retry; an active projection is never retargeted in place.
- [x] 2.6 Make Issue correlation persistence part of successful publication handling and ensure a replay can repair a missing correlation without creating another projection.
- [x] 2.7 Preserve archive completion semantics: mark the correlated Multica projection done, attempt linked GitLab Issue closure, and keep Multica completion when GitLab closure fails.

## 3. Remove v1 assertions and add v2 coverage

- [x] 3.1 Delete or rewrite handler tests, fixtures, and store assertions that require a new card from `proposal-ready`.
- [x] 3.2 Add tests for v2 Issue publication filtering, required fields, initial status/assignment, frozen projection context, and non-change Issue ignoring.
- [x] 3.3 Add tests for exact replay, concurrent replay, failed creation retry, correlation repair, changed-frozen-SHA non-retargeting, and command-injection-safe adapter arguments.
- [x] 3.4 Add tests proving archived Push Hook completes existing projections, closes linked Issues when configured, degrades observably without GitLab credentials, and never creates a new projection.

## 4. Validate and promote the contract

- [x] 4.1 Run `go test ./...` and `go test -race ./...` in `bridge/`.
- [x] 4.2 Run `openspec validate reset-specwire-v2-contract --strict` and resolve all structural or semantic validation failures.
- [x] 4.3 Rebuild and deploy the Bridge with `docker compose build && docker compose up -d`, then run the available v2 end-to-end flow: Skill/client publishes a `change` Issue → Multica projection is created → archive event completes the projection and closes the Issue.
- [x] 4.4 After implementation and verification, use the normal OpenSpec archive path to promote the delta into `openspec/specs/` and archive the change; verify the active specs contain no v1 publication contract.
