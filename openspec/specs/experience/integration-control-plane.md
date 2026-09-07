# Integration Control Plane Experience

**Status**: current

**Last reconciled**: 2026-09-07 from `specwire-integration-mvp`

## Purpose

This document records the accepted product and interaction contract for configuring and operating a Workspace-scoped SpecWire integration. Historical image prototypes remain with their archived Change and are visual references rather than the current source of truth.

## Primary journey

The normal configuration sequence is:

```text
1. Configure provider endpoint profiles and credential references
2. Create a Connection by explicitly selecting source and target context
3. Review or execute the resource and shared-Hook plan
4. Create one or more Flows under that Connection
5. Edit and publish a Flow in the Connection-scoped Builder
6. Observe executions and use bounded recovery actions
```

Connection onboarding and Flow authoring are distinct. A Connection may be configured before any Flow exists. Publishing the first compatible input Flow activates its route and creates or adopts the shared Hook; saving a draft has no provider side effect.

## Navigation and object placement

The Workspace control plane exposes these areas:

```text
控制台
├── 概览
└── 告警
集成管理
├── 连接管理
├── GitLab 项目
├── Multica 项目
├── HOOK 事件
├── 令牌管理
├── 集成流
└── 执行记录
运营
├── 运行状态
├── 同步任务
└── 审计日志
配置
├── 实例配置
├── 集成能力
├── 全局配置
├── 环境变量
└── 权限管理
帮助
└── 对象关系
```

“实例配置” manages Workspace-owned GitLab and Multica endpoint profiles. “连接管理” owns source-to-target bindings and resource preparation. “集成流” is a Workspace-level Flow catalog, but Flow creation and editing remain scoped to one Connection. “集成能力” is the registry of Flow node capabilities and DataModels, not a list of endpoint instances or Connections.

GitLab and Multica project pages are read-only indexes of projects already represented by Connections. Adding a project happens through Connection onboarding because a project enters SpecWire together with its source/target relation and resource plan.

## Connection onboarding

Opening “新建 Connection” starts with no endpoint, Group, Workspace, or project preselected. Selectors load one level at a time:

```text
GitLab instance → Group → source project
Multica instance → Workspace → existing project or explicit create option
```

The optional “隐藏当前 Workspace 已绑定的项目” filter removes projects already owned by active Connections without weakening server-side conflict detection. Disabling a Connection makes those project identities selectable again.

Before saving, the user can inspect the endpoint and external IDs, project-creation defaults, lifecycle labels, clone URL, resource ownership, Hook plan, and capability/readiness results. Preview does not mutate providers. Automatic target-project creation must be explicitly selected when no existing target project is chosen.

## Connection detail and Flow catalog

Connection detail keeps source endpoint/project and target endpoint/Workspace/project visible as fixed context. It groups managed or adopted resources, shared Hook and routes, Flow collection, execution summary, and operational health under one aggregate.

One Connection can own multiple Flows. Flow cards show lifecycle state, active version, input/output behavior, DataModel path, and recent execution outcome. Workspace Flow Catalog rows must retain their owning Connection and source/target context; opening a row preserves that scope.

## Flow Builder

Builder never asks the author to remap provider instances or projects. The selected Connection is read-only scope, while the author selects or creates a Flow within it.

The Builder provides:

- a template or empty-draft starting point;
- a palette of published ConnectorBehaviors and the limited GenericNodes;
- a drag-and-drop canvas with typed ports and visible DataModel contracts;
- a node inspector generated from declared parameter schemas;
- draft save, validation, simulation, explicit live test, publish, pause, and archive actions according to role and lifecycle state.

Invalid drafts may be saved, but publication is blocked with node- or edge-level diagnostics. Templates are copied into independent drafts. Published versions remain immutable, and editing creates a new draft/version path rather than overwriting execution history.

## Execution and attention states

Execution views show the pinned FlowVersion, node attempts and checkpoints, redacted input/output snapshots, provider request or correlation IDs, errors, and retention information.

Execution outcome and operator attention are separate. A failed, indeterminate, or reconciliation-required execution can be `待关注` or `已知晓`; acknowledging it removes it from active alert counts without changing the underlying failure. “取消已知晓” restores attention. Retry continues the existing execution when safe, while replay creates a new execution pinned to an explicitly selected version and requires confirmation of possible external effects.

## Security and feedback

The UI displays secret aliases, capability results, and actionable repair guidance, never plaintext credentials. Missing or rejected provider credentials must produce a specific repair path rather than a generic internal-server error or a process-level credential fallback.

Destructive actions such as disabling a Connection, removing a managed resource, pausing a Flow, or performing a live external test require clear scope and confirmation. Historical executions, adopted resources, and unrelated provider objects remain visible and are not silently deleted.
