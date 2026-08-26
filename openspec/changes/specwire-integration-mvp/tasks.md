> 实施约定：本 Change 按 `design.md` 的 Foundation → Golden path → Authoring surface → Recovery and cutover 四个门槛推进。每个门槛先完成可运行的纵向切片和测试，再继续下一组；不要把所有任务当作一次性大提交。任务仍必须逐项实现并勾选，门槛说明不降低任何规约要求。

## 1. Domain and persistence foundation

- [x] 1.1 Define Workspace, Account, IdentityProvider, WorkspaceMembership, ScopedRoleBinding, GitLabInstance, GitLabGroupBinding, MulticaInstance, Connection, ManagedResource, HookRoute, ConnectorType, ConnectorBehavior, DataModelDefinition, Flow, FlowTemplate, FlowVersion, FlowExecution, NodeExecution, AuditEvent, and correlation models with explicit Workspace ownership.
- [x] 1.2 Replace the Flow-level `ConnectorInstance` vocabulary with ConnectorNode = behavior + parameter bindings while retaining GitLab/Multica endpoint profiles as control-plane records; add migration tests for the terminology and identity rules.
- [x] 1.3 Add database migrations, foreign keys, and uniqueness constraints for Workspace isolation, one-to-one active Connections, published Flow versions, provider external IDs, route ownership, idempotency keys, and managed-resource ownership.
- [x] 1.4 Add secret-reference storage and redaction boundaries for login provider secrets, Group credentials, optional Multica control-plane credentials, and Hook signing secrets; keep runtime checkout credentials outside the control plane and test that plaintext secret material cannot appear in API responses, Flow definitions, snapshots, or audit records.
- [x] 1.5 Implement declarative registry loading for built-in ConnectorType/Behavior and DataModel definitions, with stable keys, versions, schemas, semantic roles, and adapter-operation references; verify repeated bootstrap is idempotent.
- [x] 1.6 Implement versioned Flow graph persistence for nodes, ports, edges, behavior/model references, parameter bindings, filters, and compiled-plan metadata; add round-trip tests for drafts and published versions.
- [x] 1.7 Add unit tests for cross-Workspace query/mutation isolation, provider external-ID uniqueness, Connection conflicts, immutable version references, and concurrent idempotency claims.

## 2. Workspace identity and provider configuration

- [x] 2.1 Implement the built-in local provider, bootstrap of the first administrator and Default Workspace, secure session creation/logout, and redacted authentication errors.
- [x] 2.2 Implement external OAuth/OIDC authorization-code + PKCE login, `(identity_provider_id, subject)` identity linking, pending first-login state, and explicit Workspace membership grants.
- [x] 2.3 Implement `admin`, scoped `operator`, and `viewer` authorization checks for Connection, Flow, credential, provider, execution, replay, and audit operations; reject custom roles and `connector_admin` configuration.
- [x] 2.4 Add APIs for administrators to create, test, disable, and list Workspace-owned GitLab and Multica endpoint profiles with stable internal IDs, optional Multica management capability/credential references, and redacted credential references; do not model runtime `glab` checkout credentials.
- [x] 2.5 Add GitLab Group credential binding with PAT/Group Access Token profiles, subgroup inheritance, capability checks, safe rotation, and provider-fake tests for missing permissions and transient failures.
- [x] 2.6 Add authorization tests for multi-Workspace accounts, Group/subgroup scope, provider capability checks using configured credentials rather than login impersonation, viewer read-only behavior, credential access, replay authorization, and cross-Workspace ID probing.

## 3. Connection onboarding and shared provider resources

- [x] 3.1 Implement server-backed searchable selectors for GitLab endpoint → Group → project and Multica endpoint → workspace → project, returning stable internal and external IDs plus URL/clone snapshots and the selected provider capability state.
- [x] 3.2 Implement Connection creation with one source GitLab project and one target Multica project, existing-project selection or defaulted project creation using the GitLab full path title and unset optional fields, one-to-one conflict detection, and explicit dry-run output.
- [x] 3.3 Implement idempotent Multica project creation and add/adopt of the runtime clone URL in both the selected Multica workspace repository registry and project resources, using the `specwire-managed` marker for created resources and recording managed/adopted ownership separately.
- [x] 3.4 Implement capability probing and canonical clone-URL selection (instance host alias, SSH default, HTTPS fallback) without confusing optional Multica management access or runtime `glab` access with login credentials.
- [x] 3.5 Implement durable Connection onboarding checkpoints, `configured` versus `ready` status, retryable/blocked/conflict errors, partial-progress resume, and audit events for every provider side effect.
- [x] 3.6 Implement shared GitLab Hook reconciliation for compatible input behaviors: one managed Hook per source project, safe signing-token rotation, preservation of unrelated Hooks, and route registration only when an input Flow is published.
- [x] 3.7 Implement Connection disable/unbind and explicit managed-resource deprovision checks without deleting external projects, adopted resources, unrelated Hooks, or historical executions.
- [x] 3.8 Add provider-fake and integration tests for duplicate suppression, existing-resource adoption, project title conflicts, one-to-one mapping conflicts, Hook sharing, Hook rotation, and retry after partial onboarding failure.

## 4. Connector behavior and DataModel registry

- [x] 4.1 Implement the ConnectorType/ConnectorBehavior registry and adapter contract with direction, parameter schema, input/output model contracts, required capabilities, behavior version, idempotency strategy, reconciliation capability, and an allowlisted pre-deployed adapter operation.
- [x] 4.2 Implement administrator APIs and schema-driven forms for registering metadata over deployed adapters and enabling/disabling/versioning behaviors while preventing arbitrary script, HTTP definition, or executable upload.
- [x] 4.3 Implement the DataModel registry using declarative schema definitions, semantic roles, extension-field policy, published/deprecated lifecycle, and administrator model/version registration without in-place mutation; seed the four MVP models and their fixed defaults.
- [x] 4.4 Implement port compatibility validation for provider contracts, canonical models, required semantic roles, parameter schemas, and explicit Mapping/Template conversions.
- [x] 4.5 Implement the `Parse/Normalize` node with provider-event input, selected canonical DataModel output, required/type validation, and retained extension fields.
- [x] 4.6 Implement the `Mapping/Template` node with field selection, rename, default, constant, simple concatenation, and declared runtime-reference operations; reject arbitrary executable expressions.
- [x] 4.7 Implement the `Condition/Filter` node with field existence, equality/comparison, string predicates, Boolean `AND`/`OR`, mutually exclusive branches, and a `skipped` terminal result when no branch matches.
- [x] 4.8 Add unit and contract tests for built-in models/defaults, administrator-added model versions, incompatible ports, missing semantic roles, mapping validation, condition evaluation, deployed-adapter enforcement, and no-code enforcement.

## 5. Flow definition, templates, and publication lifecycle

- [x] 5.1 Implement Flow and FlowTemplate APIs scoped to a Connection, including blank drafts, template instantiation, independent cloning, node/edge updates, and server-side scope validation.
- [x] 5.2 Implement graph validation for exactly one input ConnectorNode, output ConnectorNode termination, acyclic topology, supported node types, mutually exclusive condition branches, compatible ports, complete parameters, and authorized resource references.
- [x] 5.3 Implement draft/published/paused/archived lifecycle, immutable FlowVersion creation, behavior/model version pinning, route activation on publish, and rollback by publishing a prior version as a new active version.
- [x] 5.4 Implement the Connection detail UI with Flow list, template gallery, version state, shared Hook/resource status, execution summary, and access-aware actions.
- [x] 5.5 Replace the static Flow configuration surface with a drag-and-drop canvas supporting the registered ConnectorBehavior and GenericNode palette, typed ports, edge validation, node selection, deletion, and graph persistence.
- [x] 5.6 Implement schema-driven node parameter panels for fixed values, Connection/resource references, credential aliases, event filters, and declared runtime-data references; never render secret values.
- [x] 5.7 Implement model/port visualization, validation diagnostics, invalid-draft saving, publish blocking, and clear distinction between provider event schema, canonical DataModel, and target action input.
- [ ] 5.8 Implement sample-event simulation with external actions suppressed and an explicitly confirmed live connection test; add browser-level coverage for template creation, editing, validation, and publish/pause flows.

## 6. Asynchronous Flow runtime and built-in lifecycle behaviors

- [x] 6.1 Refactor Bridge ingress into signature verification, provider-envelope parsing, Workspace/Connection route resolution, durable event acceptance, and asynchronous executor handoff without waiting for provider side effects in the webhook request.
- [x] 6.2 Implement route matching for source endpoint/project, ConnectorBehavior, event filters, Connection state, and active FlowVersion; deliver one event to every matching enabled Flow and reuse the shared Hook.
- [x] 6.3 Implement FlowExecution and NodeExecution checkpoints, read-only system context, node attempt tracking, per-Connection ordering where available, and bounded concurrency/backpressure defaults.
- [x] 6.4 Implement platform-managed idempotency and correlation keys containing Workspace, Connection, source identity, Flow/behavior identity, publication or delivery identity, and target action identity.
- [x] 6.5 Implement retryable, validation, skipped, indeterminate, and reconciliation-required states; retry safe failed nodes, preserve partial success, and avoid blind repetition after an uncertain external result.
- [x] 6.6 Register and execute the built-in `publish-change` template: GitLab Issue Hook → Parse/Normalize → `ChangePublication.v1` → Mapping/Template → Multica Create Issue, including immutable publication metadata and projection correlation.
- [x] 6.7 Register and execute the built-in `complete-archive` template: archived Push Hook → Parse/Normalize → `ArchiveCompletion.v1` → Mapping/Template → Multica Complete Issue, including durable projection completion and recoverable GitLab Issue closure.
- [x] 6.8 Preserve safe Multica adapter invocation with argument arrays, bounded process-group timeouts, CLI output validation, provider request IDs, and no shell interpolation; retain and extend existing security tests.
- [x] 6.9 Add runtime tests for duplicate deliveries, concurrent claims, multiple matching Flows, out-of-order archive events, missing correlations, immutable frozen SHAs, provider timeouts, indeterminate results, and retry/reconciliation behavior.

## 7. Execution observability, replay, and security

- [x] 7.1 Implement execution detail APIs and UI showing FlowVersion, node states, attempts, redacted input/output snapshots, provider correlation IDs, errors, and retention metadata.
- [x] 7.2 Implement bounded, configurable retention for raw-event summaries and node snapshots with secret/token/code redaction and tests for sensitive-field leakage.
- [x] 7.3 Implement authorized retry, repair, and replay operations; retry resumes the original execution when safe, while replay creates a new execution pinned to an explicitly selected version and requires side-effect confirmation.
- [x] 7.4 Implement audit events for Flow draft/publish/pause/replay, ConnectorBehavior/DataModel changes, route activation, provider side effects, resource adoption, retries, failures, and deprovision requests.
- [ ] 7.5 Add API, browser, and race tests for role enforcement, redaction, execution visibility, replay version pinning, idempotency, and concurrent operator actions.

## 8. Migration, cutover, and end-to-end verification

- [x] 8.1 Implement import of legacy `.env` allowlist/project mappings and signing secrets into a Default Workspace with resolved endpoint/project IDs, adopting identifiable Hooks and Multica resources without duplicates.
- [x] 8.2 Add a bounded compatibility reader and cutover flag, then route the existing fixed Issue/Push behavior through the two built-in Flow templates before removing the old direct business branches.
- [x] 8.3 Verify that the persistent Connection/route model becomes the only live routing source after cutover and that a missing Connection never falls back to a global project mapping.
- [ ] 8.4 Add API contract and browser tests for the complete admin/operator/viewer path: login → configure endpoint/credential → select projects → onboard resources → create/edit/publish Flow → inspect execution → retry/replay.
- [x] 8.5 Run the full unit/integration suite with `go test -race ./...`, including provider fakes, migration idempotency, secret redaction, duplicate delivery, and failure recovery assertions.
- [ ] 8.6 Rebuild and deploy with `docker compose build && docker compose up -d`, then run the real GitLab/Multica end-to-end flow for publication, Multica projection creation, archive completion, and linked GitLab Issue closure.
- [ ] 8.7 After implementation acceptance, review `openspec/specs/domain/` and `openspec/specs/architecture/adr/` for shipped vocabulary and rationale, then archive this change and merge its deltas into the matching `openspec/specs/behavior/`, `domain/`, `architecture/`, and `experience/` areas; do not publish before the behavior is implemented and accepted.
