# Connection-scoped visual Integration Flows

**Status**: accepted

**Date**: 2026-08-26

**Clarifies**: [ADR 0001](0001-specwire-owns-integration-boundary.md), [ADR 0002](0002-current-specwire-scope.md), [ADR 0006](0006-workspace-scoped-control-plane.md)

SpecWire needs to evolve from a fixed GitLab-to-Multica Bridge into a configurable integration surface while preserving the boundary around GitLab change publication, Multica execution projections, and independently managed Skills/Agents. We decide to make a Connection-scoped visual Integration Flow a first-class product capability: Connection owns the source/target binding, provider resources, shared Hook, and authorization scope; Flow owns the directed event-to-action behavior.

The Flow model uses `ConnectorType → ConnectorBehavior → ConnectorNode`, where one provider type can expose multiple input or output behaviors and a node is a selected behavior plus parameter bindings. DataModel definitions are independent, declarative, versioned contracts between provider event schemas, canonical SpecWire models, and target action inputs. Built-in lifecycle behavior is represented by templates rather than an unversioned special handler. Connector behaviors are registry metadata over pre-deployed, approved adapters; administrators can configure and version them, but cannot upload executable connector logic.

The first version is intentionally a constrained visual DAG: one input connector, registered connector and generic nodes, mutually exclusive condition branches, and connector outputs. It excludes arbitrary code, loops, waits, subflows, error branches, and a general-purpose n8n-style automation runtime. Published FlowVersions, ConnectorBehavior versions, and DataModel versions are immutable; executions are asynchronous, at-least-once, idempotent, checkpointed, retryable, and explicitly replayable without promising cross-system rollback.

## Considered Options

- **Keep fixed Bridge handlers**: smallest immediate implementation, but every new provider behavior or data transformation becomes bespoke code and the admin model cannot express it.
- **Build a full n8n-like workflow engine**: maximum flexibility, but changes SpecWire into a general iPaaS/runtime platform and introduces disproportionate complexity in code execution, loops, waits, credentials, compensation, and long-running execution.
- **Adopt a constrained visual Integration Flow**: provides the requested drag-and-drop authoring and a future extension seam while keeping provider behavior, data contracts, security, and lifecycle semantics bounded. This is the chosen option.
- **Let administrators upload or author arbitrary connector code**: rejected for the MVP because it would make credentials, sandboxing, review, and execution semantics part of every Flow definition. New executable connector capabilities require a separately deployed and approved adapter.

## Consequences

- `workspace-instance-onboarding` was superseded as a planning artifact; its useful Connection/control-plane assumptions were reconciled with the Flow model in `specwire-integration-mvp` before implementation.
- Project/resource onboarding remains a control-plane operation; publishing an input Flow may reconcile a shared Hook and register a route, but resource creation is not a canvas node.
- Existing GitLab Issue publication and archived Push completion become built-in Flow templates, preserving the current projection and closure semantics while moving execution through one runtime.
- The implementation needs a graph definition/validation layer, declarative ConnectorBehavior and DataModel registries, an asynchronous executor, version pinning, execution recovery, and a visual admin editor.
- Connector registry entries point only to deployed adapter operations; the admin surface can enable, configure, and version supported behavior metadata, while adding new executable provider capabilities remains a deployment concern.
- SpecWire remains an integration boundary, not the owner of OpenSpec content, local Git operations, Agent execution, MR review/merge, or SpecWire Skills.
- Future capabilities such as additional providers, fan-out, waits, subflows, or generic HTTP behaviors require separate behavior and architecture decisions; they are not implicit consequences of this ADR.
