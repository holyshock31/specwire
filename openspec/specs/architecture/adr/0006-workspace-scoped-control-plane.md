# Workspace-scoped control plane with separate identity and connector credentials

**Status**: accepted

**Date**: 2026-08-26

**Supersedes**: the scope exclusions in [ADR 0002](0002-current-specwire-scope.md) that place general identity management and external project provisioning outside the SpecWire control plane. ADR 0002 remains authoritative for the GitLab-to-Multica integration boundary and the exclusion of client workflow/Agent responsibilities.

## Context

SpecWire started as a single-process GitLab-to-Multica Bridge whose project mapping, webhook lifecycle, and credentials were configured through environment variables. The product now needs multiple GitLab and Multica instances, Workspace isolation, account permissions, Group-scoped GitLab credentials, and an onboarding operation that can establish both Multica repository contexts for a project. Multica runtime checkout access is owned by the runtime environment and must not be confused with optional control-plane access used for provider administration.

Treating these concerns as global process configuration would make identical paths and numeric IDs ambiguous, mix login identity with provider access, and make it difficult to prevent one Workspace from using another Workspace's credentials or resources. Putting the workflow in Skills would also violate the existing integration-boundary decision.

## Decision

SpecWire owns a Workspace-scoped integration control plane with the following boundaries:

1. **Workspace is the isolation boundary.** Accounts may have memberships in multiple Workspaces, but connector instances, credentials, mappings, resources, operations, and audit records belong to exactly one Workspace.
2. **Login identity is separate from integration access.** Local login and external OAuth/OIDC identify SpecWire accounts. GitLab Group credentials and optional Multica management credentials are separate connector secrets and are never inferred from a user's login token.
3. **Provider access is capability-based, not user-membership-based.** Project discovery and onboarding are authorized by the Workspace role plus the configured Group credential and endpoint capability checks. SpecWire does not require the logged-in account to be impersonated as a GitLab or Multica member for each operation; a missing provider capability blocks the operation.
4. **Connector instances are explicit identity dimensions.** GitLab and Multica instances have stable internal IDs. A Multica instance may be registered in endpoint-only state; its optional management credential is required only by capabilities that need it. The same physical endpoint can be registered independently in multiple Workspaces with separate credential references.
5. **SpecWire may provision integration-side project context.** The control plane may create/select the Multica project and reconcile the GitLab label, webhook, Multica workspace repository entry, and Multica project resource. External project/resource ownership is tracked as managed or adopted, and destructive cleanup requires explicit confirmation.
6. **Runtime checkout remains outside SpecWire.** Multica/Agent runtime `glab` access is owned by the runtime environment. SpecWire may test readiness but does not require or store that credential as a prerequisite for configured state.
7. **The client workflow boundary remains unchanged.** OpenSpec authoring, local Git operations, Agent execution, MR review/merge, and archive Skills remain clients or execution-system responsibilities. The OpenSpec change is the behavior contract for the control plane; this ADR records the rationale and boundary only.

## Consequences

- Stable mappings and correlations must carry Workspace and connector-instance IDs rather than relying on paths or global defaults.
- The control plane needs account/session/membership data, secret references, provider adapters, durable onboarding checkpoints, and audit events. Multica management secret references are optional and capability-scoped; runtime checkout secrets are not stored here.
- Provider capability checks make Group/project discovery and onboarding testable without coupling every operation to the logged-in user's provider membership.
- A physical GitLab/Multica endpoint may have multiple Workspace registrations, which avoids implicit credential sharing but requires explicit configuration per Workspace.
- Existing `.env` mappings require a bounded import/compatibility path into a Default Workspace; the new persistent model becomes authoritative after migration.
- Multica resource capability differences are handled behind an adapter/capability probe instead of leaking provider-specific resource types into the Workspace model.
- This decision expands the control-plane scope but does not turn SpecWire into a workflow engine, project-management product, or runtime credential broker.

## Non-Consequences

This ADR does not define API payloads, onboarding states, Multica default values, hook event filters, resource URLs, role permissions, adapter contracts, or migration steps. Those are defined in `openspec/changes/specwire-integration-mvp/` until implemented and archived into the current behavior specs.
