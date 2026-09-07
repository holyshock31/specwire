# SpecWire Integration Platform Architecture

**Status**: current

**Last reconciled**: 2026-09-07 from `specwire-integration-mvp`

## Purpose

This document describes the current structural architecture of SpecWire after the Integration MVP. Accepted trade-offs and their rationale remain in the ADRs; observable behavior remains in `openspec/specs/behavior/`.

## System boundary

SpecWire is the integration boundary between GitLab change lifecycle events and Multica execution projections. It does not author OpenSpec content, operate local Git worktrees, run implementation Agents, or decide review and merge outcomes. Client-side SpecWire Skills publish lifecycle signals that the runtime consumes through GitLab.

The product is one deployable Bridge with two collaborating internal layers:

```text
Control plane
  Workspace / account / membership / role
  provider endpoint profiles / credential references
  Connection / onboarding / resources / shared Hook
  ConnectorBehavior / DataModel registry
  Flow drafts / templates / published versions / audit

Flow runtime
  webhook verification / event acceptance
  Connection and route resolution
  durable jobs / FlowVersion execution / node checkpoints
  provider adapters / correlation / retry / replay / reconciliation
```

These are internal module boundaries, not independently deployed services.

## Ownership hierarchy

Workspace is the isolation boundary. Provider endpoint profiles, credentials, Connections, resources, Flows, executions, and audit records belong to one Workspace.

A Connection is the instance-aware binding of one GitLab source project to one Multica target project for the current version. It owns the selected endpoint/project identities, managed or adopted resources, shared Hook, authorization scope, and multiple Integration Flows. Human-readable paths are diagnostic snapshots; stable relations include Workspace, connector-instance, and provider external IDs.

Project and resource onboarding is a control-plane operation. It may create or adopt the Multica project, workspace repository entry, project resource, GitLab lifecycle labels, and shared Hook metadata. It is not represented as repeatedly executable canvas nodes.

## Flow and connector model

The execution model is:

```text
ConnectorType → ConnectorBehavior → ConnectorNode
                       ↓
                adapter operation

ConnectorNode → typed port / DataModel → GenericNode → ConnectorNode
```

- `ConnectorType` groups a provider family such as GitLab or Multica.
- `ConnectorBehavior` is a versioned input or output capability with parameter schema, port contracts, required capabilities, and one allowlisted pre-deployed adapter operation.
- `ConnectorNode` is one use of a behavior plus parameter bindings inside a FlowVersion; it is not a reusable endpoint or credential instance.
- `DataModel` is an independent, declarative, immutable versioned contract carried by ports and edges.
- Generic processing is restricted to Parse/Normalize, Mapping/Template, and Condition/Filter. Arbitrary code, loops, waits, subflows, and uploaded adapters are outside the current runtime.

A draft may be incomplete. Publishing validates topology, parameters, authorization, adapter availability, and model compatibility, then creates an immutable FlowVersion and activates its route. Executions remain pinned to the versions selected at start.

## Ingress and lifecycle templates

Compatible input Flows for one source project share a managed GitLab Hook. The Hook authenticates and accepts provider events; persisted routes select every matching published Flow independently.

The built-in lifecycle consists of three templates:

```text
change Issue opened
  → Publish Change
  → create correlated Multica projection

archived trailer pushed on main
  → Complete Archive
  → set projection done and close linked Issue

specwire::abandoned newly added to the existing change Issue
  → Abandon Change
  → set projection cancelled, record reason, and close linked Issue
```

Completion and cancellation are mutually exclusive terminal outcomes. Neither creates a new projection. Bridge-generated notes or close updates must not re-enter the abandon route.

## Persistence and reliability

The MVP uses one Bridge process and one SQLite database with versioned migrations and a database-backed durable job queue. Event acceptance, selected FlowVersion, execution identity, and runnable job are persisted before asynchronous provider work begins.

Delivery is at-least-once. External actions use platform-generated idempotency keys or provider reconciliation. Node checkpoints retain attempts and redacted snapshots. Known safe failures can retry from a checkpoint; uncertain provider outcomes enter an indeterminate or reconciliation-required state before another side effect is allowed.

Correlation includes Workspace, Connection, source identity, Flow/behavior identity, publication or delivery identity, and target action identity. Terminal projection state is durable so delayed or replayed events cannot resurrect `done` or `cancelled` work.

## Security boundaries

Login identity, Workspace authorization, provider credentials, and Multica runtime checkout credentials are separate concerns:

- local or external identity providers identify SpecWire accounts;
- Workspace membership and the fixed `admin`, scoped `operator`, and `viewer` roles authorize product actions;
- endpoint and Group credential references authorize provider capabilities;
- Multica runtime or Agent `glab` credentials remain outside the SpecWire control plane.

Secrets are stored and transported as references or redacted aliases. They must not appear in Flow definitions, execution snapshots, audit output, or browser responses. Provider adapters use bounded calls and safe argument arrays; the registry cannot upload executable connector code.

## Related decisions

- [ADR 0001: SpecWire owns the integration boundary](adr/0001-specwire-owns-integration-boundary.md)
- [ADR 0006: Workspace-scoped control plane](adr/0006-workspace-scoped-control-plane.md)
- [ADR 0007: Connection-scoped visual Integration Flows](adr/0007-connection-scoped-visual-integration-flows.md)
