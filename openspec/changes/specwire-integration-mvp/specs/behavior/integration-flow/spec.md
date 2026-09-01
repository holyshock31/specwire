## Purpose

为 SpecWire 提供 Workspace 内可配置、可版本化、可观测的 Integration Flow，使外部连接器事件能够经过受约束的数据转换后产生可靠的执行系统投影。

## ADDED Requirements

### Requirement: Connection 是 Flow 的运行容器

SpecWire MUST represent an active Connection as the Workspace-scoped binding of exactly one source project and one target project, together with their provider endpoint references, managed or adopted resources, shared ingress hook, and published or draft Flows. A Flow MUST use the Connection's source and target by default. A Flow node MUST NOT silently introduce a different Workspace, source project, or target project.

#### Scenario: Connection 下创建多个生命周期 Flow

- **WHEN** an operator has configured a Connection between a GitLab project and a Multica project
- **THEN** the operator can create separate publish and archive Flows under that Connection, while SpecWire maintains the reserved abandon Flow for the controlled `specwire::abandoned` label route; each Flow resolves its default source and target from the Connection

#### Scenario: Flow 不能跨 Workspace 引用项目

- **WHEN** a Flow parameter attempts to reference a source project, target project, credential, or resource from another Workspace
- **THEN** validation rejects the draft for publication and no external side effect is performed

#### Scenario: 资源 onboarding 不成为画布节点

- **WHEN** a Connection is onboarded
- **THEN** project creation, repository-resource registration, label reconciliation, and Hook provisioning are recorded as Connection onboarding operations rather than ordinary Flow nodes

### Requirement: 管理员可配置连接器类型和行为

An administrator MUST be able to register, enable, disable, and version ConnectorType and ConnectorBehavior definitions within the supported adapter boundary. A ConnectorType MAY expose multiple behaviors. Each behavior MUST declare its direction, parameter schema, input and output model contracts, required capabilities, and adapter behavior version. A behavior MUST reference an already deployed and approved adapter operation; registering metadata MUST NOT install or execute uploaded code. A Flow author MUST select from registered behaviors and configure parameters, but MUST NOT upload arbitrary executable code as a node implementation.

#### Scenario: 一个 ConnectorType 暴露多个行为

- **WHEN** the administrator enables the GitLab connector type with Issue Hook and Push Hook behaviors
- **THEN** the Flow Builder displays both behaviors under GitLab and each behavior exposes its own direction, parameter form, and event contract

#### Scenario: 未绑定执行适配器的行为不可发布

- **WHEN** an administrator defines a behavior whose adapter binding, parameter schema, or model contract is incomplete
- **THEN** the behavior cannot be selected in a published Flow and the UI reports the missing capability

#### Scenario: Flow 作者不能上传代码节点

- **WHEN** a Flow author attempts to upload a script or executable as a connector or transform node
- **THEN** the operation is rejected and no executable content is stored as a Flow definition

#### Scenario: 未部署 adapter 的行为不能注册

- **WHEN** an administrator attempts to register or enable a ConnectorBehavior whose adapter operation is not deployed, approved, or allowlisted
- **THEN** the registry rejects or keeps the behavior disabled and reports the missing adapter capability without storing executable content

#### Scenario: 行为版本升级不改写已发布 Flow

- **WHEN** an administrator publishes a new version of a ConnectorBehavior
- **THEN** existing published FlowVersions continue to use the behavior version they were published with, and a Flow changes version only after an explicit upgrade and publish

### Requirement: Flow 支持受约束的可视化 DAG

The Flow Builder MUST allow an author to create a draft from a template or an empty canvas, place registered ConnectorNode and GenericNode definitions, configure their parameters, and connect compatible ports. A publishable Flow MUST have exactly one input ConnectorNode, every executable path MUST terminate at an output ConnectorNode or an explicit filtered terminal state, and the graph MUST be acyclic. The first version MUST NOT support loops, waits, subflows, error branches, or arbitrary script nodes. Condition branches MUST be mutually exclusive for one execution.

#### Scenario: 空白 Flow 可以保存为草稿

- **WHEN** an author creates an empty Flow and saves it without an input or output connector
- **THEN** SpecWire stores the incomplete graph as a draft and reports the missing publication requirements without activating it

#### Scenario: 缺少入口或出口不能发布

- **WHEN** an author tries to publish a Flow with no input connector, a path that does not reach an output connector, or an unconfigured required parameter
- **THEN** publication is rejected with node-level validation errors and no Hook route is activated

#### Scenario: 环路不能发布

- **WHEN** a draft contains a cycle or a wait/loop/subflow node not supported by the current registry
- **THEN** validation rejects publication and identifies the unsupported graph edge or node

#### Scenario: 条件分支一次只执行一条路径

- **WHEN** an event reaches a Condition/Filter node with several mutually exclusive branches
- **THEN** at most one matching branch executes for that FlowExecution, and a non-matching event ends as `skipped` without retry

### Requirement: DataModel 是节点之间的独立数据契约

SpecWire MUST maintain a declarative, versioned DataModel registry. A DataModel definition MUST describe its schema, required fields, type information, extension-field policy, and any platform semantic roles. Built-in models MUST be delivered as declarative registry definitions rather than scattered provider-specific code. The MVP registry MUST provide `ChangePublication.v1`, `ArchiveCompletion.v1`, `ChangeLifecycle.v1`, `MulticaCreateIssueInput.v1`, and `MulticaCompleteIssueInput.v1` with the required fields, roles, and defaults defined by the published change contract. An administrator MAY add a new model or model version, but MUST NOT mutate a published model version in place. A DataModel is a port or edge contract in a Flow and is not required to be a visible canvas node.

#### Scenario: 系统提供内置数据模型

- **WHEN** an administrator creates a Flow for the supported GitLab-to-Multica lifecycle
- **THEN** the model registry offers versioned built-in models such as `ChangePublication.v1`, `ArchiveCompletion.v1`, `ChangeLifecycle.v1`, and the target action input models

#### Scenario: 管理员新增模型版本

- **WHEN** an administrator registers `ChangePublication.v2` with a declarative schema and semantic role metadata
- **THEN** the registry exposes v2 for new or explicitly upgraded Flows while existing Flows remain bound to v1

#### Scenario: 不兼容的数据模型连线被拒绝

- **WHEN** an author connects a node output to an input whose model contract is incompatible and no Mapping/Template node resolves the difference
- **THEN** the editor marks the edge invalid and publication is rejected

#### Scenario: 扩展字段可被保留

- **WHEN** a provider event contains fields not declared as required fields in the selected DataModel
- **THEN** the normalized message may retain those extension fields while required-field and type validation remains enforced

#### Scenario: 内置模型默认值固定

- **WHEN** a built-in publication Flow omits optional target reference, status, assignee, or project title overrides
- **THEN** the Flow uses `refs/heads/main`, `backlog`, an empty assignee, `[SpecWire] {change_id}`, and `$connection.target_project` respectively, and the selected published model versions remain immutable

### Requirement: 解析、映射和条件节点提供有限的通用处理能力

The first version MUST provide the following GenericNode behaviors: Parse/Normalize, Mapping/Template, and Condition/Filter. Parse/Normalize MUST convert a provider event contract into a selected DataModel. Mapping/Template MUST support field selection, renaming, defaults, constants, simple concatenation, and declared runtime references. Condition/Filter MUST support field existence, equality, comparison, string predicates, and Boolean AND/OR. These nodes MUST NOT execute arbitrary code or silently access undeclared mutable state.

#### Scenario: GitLab 事件转换为标准模型

- **WHEN** a GitLab Issue connector emits a valid provider event and the Flow contains a Parse/Normalize node targeting `ChangePublication.v1`
- **THEN** the node emits a validated `ChangePublication.v1` value or a visible validation error without calling the target system

#### Scenario: 映射生成目标输入

- **WHEN** a Mapping/Template node maps `change_id`, `branch`, and `branch_head_sha` into a Multica action input
- **THEN** the output contains the configured values, defaults, and templates and is validated against the output behavior's input model

#### Scenario: 映射不能执行脚本

- **WHEN** a mapping configuration contains executable script syntax or an undeclared mutable variable reference
- **THEN** validation rejects the mapping and the Flow cannot be published

### Requirement: Flow 参数使用固定值、资源引用或运行时引用

ConnectorNode and GenericNode parameters MUST distinguish fixed values, Workspace-scoped resource references, and declared runtime-data references. Credential parameters MUST be represented as secret aliases or credential references and MUST never expose secret material in the Flow definition, API response, editor, execution snapshot, or audit event. Source and target project parameters SHOULD resolve to `$connection.source_project` and `$connection.target_project` unless an explicitly authorized behavior permits another resource within the same Connection scope.

#### Scenario: 节点参数引用 Connection 项目

- **WHEN** an author configures a Multica action without overriding its target project
- **THEN** the action resolves the target from the parent Connection and the Flow definition does not duplicate an unrelated project ID

#### Scenario: 凭证只显示安全别名

- **WHEN** an author configures a connector behavior requiring a credential
- **THEN** the editor displays an authorized credential alias and never returns the token or signing secret

#### Scenario: 非法跨域参数被拒绝

- **WHEN** a node parameter references a resource outside the author's Workspace or scope
- **THEN** save or publication is rejected with an authorization error

### Requirement: 模板、草稿和发布版本生命周期可追踪

A FlowTemplate MUST create an independent editable Flow draft. Updating a template MUST NOT overwrite an existing Flow. Publishing a valid draft MUST create an immutable FlowVersion. Every FlowExecution MUST record the FlowVersion, ConnectorBehavior versions, and DataModel versions used at execution start. Pausing or unpublishing a Flow MUST stop new routing without deleting historical executions or external resources. Reverting MUST publish a prior version as a new active version rather than mutating history.

#### Scenario: 从模板创建可修改 Flow

- **WHEN** an author selects the built-in GitLab publication template
- **THEN** SpecWire creates a draft graph that can be edited independently of the template and other Connections

#### Scenario: 发布后编辑产生新版本

- **WHEN** an author edits a published Flow and saves the changes
- **THEN** the current published FlowVersion remains active until the new draft is explicitly published, after which a new immutable version is activated

#### Scenario: 执行固定版本

- **WHEN** a FlowExecution starts and an administrator later publishes a new FlowVersion
- **THEN** the existing execution continues with its recorded version and the new version applies only according to the routing activation rule for later events

### Requirement: 发布 Flow 注册共享入口路由

Publishing an enabled Flow with an input ConnectorBehavior MUST register or reconcile the route needed by its parent Connection. A source project MUST use one SpecWire-managed shared Hook for compatible input behaviors rather than one Hook per Flow. A valid event MUST be delivered to every enabled published Flow whose source, behavior, and filter match; routing MUST NOT silently select only the first match. Saving a draft MUST have no provider-side side effect.

#### Scenario: 第一条输入 Flow 创建共享 Hook

- **WHEN** the first published GitLab input Flow is activated for a configured Connection
- **THEN** SpecWire creates or adopts one managed Hook for the source project and registers the Flow route

#### Scenario: 新 Flow 不重复创建 Hook

- **WHEN** another published Flow for the same source project and compatible Hook events is activated
- **THEN** SpecWire reuses the shared Hook and adds a route without creating a duplicate Hook

#### Scenario: 多条匹配 Flow 独立执行

- **WHEN** one GitLab event matches two enabled published Flows
- **THEN** both Flows receive independent FlowExecutions with independent idempotency scopes and execution records

### Requirement: FlowExecution 异步、可恢复且至少一次

After signature and route validation, SpecWire MUST be able to acknowledge an inbound event after durably accepting it for asynchronous execution rather than waiting for every external action. Delivery and execution MUST use at-least-once semantics. Each externally side-effecting behavior MUST have a platform-managed idempotency key or provider reconciliation strategy. A FlowExecution MUST record node checkpoints, attempts, state, correlation IDs, and retryable or indeterminate failures. A retry MUST resume from the failed node when safe; an external call with unknown outcome MUST NOT be blindly repeated.

#### Scenario: 入站事件先入队再执行

- **WHEN** a valid GitLab event matches a published Flow
- **THEN** SpecWire durably records the accepted event and FlowVersion, makes the event eligible for asynchronous execution, and does not require the webhook request to wait for the complete Multica action

#### Scenario: 重复事件不重复产生业务结果

- **WHEN** the same provider delivery or immutable publication is delivered more than once to the same Flow and action
- **THEN** the executions converge on one business result and later deliveries are reported as duplicate or already applied

#### Scenario: 外部调用结果不确定

- **WHEN** a Multica action times out after the provider may have accepted the request
- **THEN** the node enters an observable `indeterminate` or reconciliation-required state and SpecWire queries or repairs it before allowing a new side effect

#### Scenario: 同一 Connection 保持可解释顺序

- **WHEN** related events for the same Connection and source project are accepted concurrently
- **THEN** SpecWire provides per-Connection ordering where available, while correlation and retry handling remain correct if provider delivery order is not guaranteed

### Requirement: 测试、执行观测和重放不产生隐式副作用

The Flow Builder MUST support a simulation using a sample event with external action nodes mocked or suppressed, and MAY support an explicitly confirmed live connection test. Execution views MUST show the selected versions, node statuses, redacted input/output snapshots, errors, attempts, and correlation IDs for the configured retention period. Retry MUST continue the original execution when safe. Replay MUST create a new execution with an explicitly selected FlowVersion and an explicit side-effect confirmation.

#### Scenario: 模拟测试不创建 Multica 任务

- **WHEN** an author runs a simulation with a sample GitLab event
- **THEN** all node transformations and validation results are shown, external Multica actions are suppressed, and no external task is created

#### Scenario: 连接测试需要确认

- **WHEN** an authorized operator chooses a live connection test and confirms the side effect
- **THEN** SpecWire performs only the declared test action, marks it as a test execution, and records the actor and provider result

#### Scenario: 重放显式选择版本

- **WHEN** an operator replays a retained event
- **THEN** SpecWire creates a new execution pinned to the selected version and warns that the replay may produce an external side effect

#### Scenario: 快照不泄露 secret

- **WHEN** a user views an execution or audit record
- **THEN** credential values, OAuth tokens, signing secrets, and authorization codes are redacted or absent

### Requirement: 执行结果与人工关注状态分离

SpecWire MUST keep the execution outcome (`status`) separate from the operator attention state (`attention_status`). A failed, indeterminate, or reconciliation-required execution MUST default to `open` attention, while queued, running, succeeded, and skipped executions MUST have `none` attention. An authorized operator MAY acknowledge an actionable execution or reopen it for attention. Acknowledging MUST NOT change the execution outcome, delete the historical record, or imply that the external side effect succeeded. Alert counts and active execution attention summaries MUST include only actionable executions whose attention state is `open`; retry or repair MUST clear the attention state while the execution is queued, and a subsequent failure MUST reopen it. Every attention state mutation MUST record the actor and time in the execution record and an audit event.

#### Scenario: 已知晓不改写失败结果

- **WHEN** an authorized operator acknowledges a failed, indeterminate, or reconciliation-required execution
- **THEN** the execution remains in its original outcome status and history, its attention state becomes `acknowledged`, and it is removed from the active execution alert count

#### Scenario: 可以重新打开关注项

- **WHEN** an operator reopens an acknowledged actionable execution
- **THEN** its attention state becomes `open`, the original outcome remains unchanged, and it appears again in active execution alerts

#### Scenario: 重试清除旧关注状态

- **WHEN** an operator retries or repairs an actionable execution
- **THEN** the queued execution has `none` attention; if the attempt fails again, the new failure is stored as `open`

#### Scenario: 已成功记录不允许标记关注

- **WHEN** an operator attempts to acknowledge or reopen a queued, running, succeeded, or skipped execution
- **THEN** the mutation is rejected and the execution record remains unchanged
