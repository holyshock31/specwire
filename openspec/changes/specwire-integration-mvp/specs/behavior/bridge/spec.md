## MODIFIED Requirements

### Requirement: Bridge 校验并过滤 GitLab 事件

Bridge MUST verify the GitLab webhook signature before producing any side effect and MUST resolve the source project through the registered Workspace/endpoint route and the provider's external project ID. It MUST route a valid event only to enabled published input Flows whose ConnectorBehavior, Connection, and event filters match. For the built-in lifecycle templates, an Issue Hook with action `open` and the `change` label is the publication event, while an `archived` Push Hook on the target ref is the completion event. Other hook kinds, refs, actions, labels, deleted branches, malformed payloads, or unmatched routes MUST produce no Flow side effect and MUST remain observable as ignored or skipped.

#### Scenario: 非法签名请求被拒绝

- **WHEN** a webhook signature is missing, expired, or does not match any secret authorized for the resolved route
- **THEN** Bridge returns HTTP 401 and produces no FlowExecution or external side effect

#### Scenario: 有效事件按已发布 Flow 路由

- **WHEN** a valid GitLab event matches a source project and an enabled published input ConnectorBehavior
- **THEN** Bridge durably accepts the event for each matching Flow and does not invoke a Flow draft or disabled version

#### Scenario: 非目标项目事件被忽略

- **WHEN** a valid webhook resolves to a project with no authorized active Connection
- **THEN** Bridge records an ignored or routing-failure result and creates no FlowExecution or external side effect

#### Scenario: 多项目各自 token 均可验签

- **WHEN** two authorized source projects use different managed signing secrets
- **THEN** each valid event is verified against the secret authorized for its resolved route and is routed only within that route's Workspace and Connection scope

#### Scenario: 旧单值配置继续可用

- **WHEN** a deployment is still in the bounded legacy-import window and supplies one legacy signing secret
- **THEN** the compatibility reader may verify the event for the imported route, while the persistent route model remains the authoritative target after migration

#### Scenario: 任一 token 不匹配时拒绝

- **WHEN** the signature matches none of the secrets authorized for the resolved route
- **THEN** Bridge returns HTTP 401 and creates no FlowExecution or external side effect

#### Scenario: v1 proposal push 不再发布任务

- **WHEN** a main push contains a legacy `proposal-ready` or other proposal publication trailer
- **THEN** no published Flow treats it as a new publication unless an explicitly registered input behavior defines that event, and the built-in lifecycle templates ignore it

#### Scenario: 非归档 push 不触发 SpecWire 副作用

- **WHEN** a push is not on the configured target ref, deletes the ref, has no `archived` event, or contains an unknown event
- **THEN** the built-in completion Flow records ignored or skipped and creates or changes no execution projection

#### Scenario: 未匹配事件被跳过

- **WHEN** a valid event has no matching published Flow or fails a configured event filter
- **THEN** Bridge records an ignored or skipped result, produces no external action, and does not ask GitLab to redeliver a permanently ineligible event

#### Scenario: 归档 Push 不创建新投影

- **WHEN** an archived Push Hook reaches the target ref
- **THEN** the matching completion Flow may complete an existing projection, but Bridge does not create a new development projection from the Push event

### Requirement: 判重基于稳定键，保证幂等

For every externally side-effecting Flow behavior, SpecWire MUST derive an idempotency key that includes the Workspace, Connection, source endpoint/project identity, Flow behavior identity, immutable publication or provider delivery identity, and target action identity as applicable. Persistent uniqueness MUST ensure that a duplicate delivery or concurrent execution produces at most one business result for the same Flow action. Different matching Flows MAY execute independently and MUST have separate idempotency scopes. A `processing`, `created`, `completed`, or safely reconciled record MUST prevent an unsafe duplicate; an `error` record MAY be retried, and an indeterminate record MUST be reconciled before another side effect.

#### Scenario: 同一 Flow 的 Issue 重放不重复建卡

- **WHEN** the same immutable `change` Issue publication is delivered again to the same Connection and publish Flow
- **THEN** the Flow reports duplicate or already applied and Multica contains only one execution projection for that Flow action

#### Scenario: 两条匹配 Flow 各自幂等

- **WHEN** one event matches two enabled published Flows
- **THEN** each Flow may create its own intended result, while replaying either Flow's event does not duplicate that Flow's result

#### Scenario: 并发执行只产生一个动作结果

- **WHEN** concurrent deliveries claim the same Flow action idempotency key
- **THEN** exactly one execution performs the side effect or reconciles it, and the others observe the durable result

#### Scenario: 并发投递只创建一个投影

- **WHEN** concurrent deliveries carry the same immutable publication and target the same publication Flow
- **THEN** at most one Multica execution projection is created and all other deliveries reuse or observe its durable result

#### Scenario: 同一 Issue 事件重放不重复建卡

- **WHEN** the same valid publication Issue event is delivered again to the same publish Flow
- **THEN** the existing execution result and correlation are reused and no second Multica projection is created

#### Scenario: 同事件重放不重复建卡

- **WHEN** the same provider delivery is retried after the webhook response or executor restart
- **THEN** the action idempotency key prevents a duplicate projection

#### Scenario: 项目映射变更后重放建到新项目

- **WHEN** an administrator explicitly rebinds a source project to a different target and requests a new replay
- **THEN** the new Connection/action scope may produce a new target result, while a replay in the original scope remains duplicate

#### Scenario: 外部创建失败可重试

- **WHEN** a Multica output behavior fails before its result is known to be applied
- **THEN** the node records a retryable error or reconciliation requirement and a later authorized operation can resume it without losing the original idempotency key

#### Scenario: 已发布版本不被动态改写

- **WHEN** an administrator edits a Flow or publishes a new ConnectorBehavior/DataModel version
- **THEN** existing FlowExecutions retain their recorded versions and are not silently reinterpreted

#### Scenario: 不同冻结版本保持不可变

- **WHEN** a later publication carries a different frozen `branch_head_sha` for an existing change
- **THEN** Bridge does not retarget the existing projection; the client must publish a new `change_id` or explicitly use a new Flow replay policy

### Requirement: 项目映射将 GitLab 项目归属到 Multica project

The persistent control plane MUST resolve an event to an active Connection using Workspace, provider endpoint identity, and the GitLab external numeric project ID. A Connection MUST bind one GitLab project to one Multica project in v1. Human-readable paths and URLs are diagnostic snapshots only. Legacy `SPECWIRE_PROJECT_MAP` or a process-global default MAY be imported during migration but MUST NOT be used to silently route a resolved event to an unrelated project after the persistent model is authoritative.

#### Scenario: 相同路径在不同 endpoint 下不混淆

- **WHEN** two Workspaces or endpoint profiles contain GitLab projects with the same full path
- **THEN** Bridge resolves each event to the Connection identified by its Workspace, endpoint, and external project ID rather than by path alone

#### Scenario: 缺少 Connection 不回退建卡

- **WHEN** a valid event has no enabled Connection for its resolved source project
- **THEN** Bridge records an actionable routing diagnostic and creates no projection in a default or unrelated Multica project

#### Scenario: 一对一冲突阻止路由

- **WHEN** a project would resolve to multiple active target Connections in the same Workspace
- **THEN** the route is blocked as a conflict until an administrator resolves the ownership ambiguity

#### Scenario: 映射加载失败阻止启动

- **WHEN** a migrated or configured Connection has an incomplete endpoint/project identity or an ambiguous target
- **THEN** the affected route is not activated and the control plane reports the invalid mapping

#### Scenario: allowlist 项目缺映射被拒绝

- **WHEN** a legacy allowlist project has no imported or configured Connection
- **THEN** Bridge does not fall back to a default target and records an actionable routing error

### Requirement: 建卡携带完整上下文

For the built-in publication Flow, the Multica output behavior MUST create an Execution Projection containing the repository, `change_id`, source `branch`, frozen `branch_head_sha`, target branch metadata, initial status, and optional assignee from the normalized publication model. The output mapping MUST be validated against the target behavior input model. Bridge MUST NOT clone or interpret OpenSpec files and MUST NOT depend on client-side Skill instructions to define the data contract.

#### Scenario: 合法 change Issue 建卡携带冻结上下文

- **WHEN** an opened Issue has the `change` label and valid `change_id`, `branch`, and `branch_head_sha` values
- **THEN** the publication Flow creates a Multica projection whose context contains those values and the configured target branch

#### Scenario: 发布者指定直通状态和分配

- **WHEN** the normalized publication model contains an allowed direct status or assignee
- **THEN** the Multica output behavior applies that status or assignment according to its declared mapping

#### Scenario: 非法模型输入不建卡

- **WHEN** normalization or output validation finds a missing required field or unsupported status
- **THEN** the Flow records a non-retryable validation error and creates no Multica projection

#### Scenario: 非法状态不建卡

- **WHEN** the publication model contains a status outside the target behavior's allowed values
- **THEN** the output behavior records a validation error and creates no projection

#### Scenario: 建卡描述可被 Agent 直接执行

- **WHEN** the built-in publication Flow creates an execution projection
- **THEN** the projection contains repository, change ID, source branch, frozen SHA, and target branch metadata needed by the client Agent without reading OpenSpec content through Bridge

### Requirement: 归档事件自动完成投影闭环

The built-in archive Flow MUST use an `archived` event on the target main ref as a completion signal, not as a publication entry point. It MUST locate the correlated publication and Execution Projection for the source Connection and `change_id`, mark the projection complete, and close linked GitLab publication Issues when the configured GitLab API capability is available. Failure to close an Issue MUST remain recoverable and MUST NOT undo a successful Multica completion. If no correlation exists, the Flow MUST record an actionable diagnostic and perform no unrelated side effect.

#### Scenario: 归档完成对应投影

- **WHEN** an `archived` Push Hook reaches the target ref for a published change
- **THEN** the archive Flow marks the correlated Multica projection done and creates no new projection

#### Scenario: 归档关闭关联 Issue

- **WHEN** the correlated publication has linked open GitLab Issues and the Connection has the required API capability
- **THEN** the Flow attempts to close all linked Issues after the projection completion is durable

#### Scenario: 关闭 Issue 失败不回滚投影

- **WHEN** the Multica projection is marked done but a linked GitLab Issue close call fails
- **THEN** the projection remains done, the failure is recorded as recoverable, and a later retry or repair can close the Issue

#### Scenario: 无关联记录不产生无关副作用

- **WHEN** an archived event has no linked publication or created projection
- **THEN** the Flow records an actionable diagnostic and does not complete an unrelated projection or create a new one

#### Scenario: 归档后实现投影自动完成

- **WHEN** an archived Push Hook reaches the target ref for a published change
- **THEN** the completion Flow marks the correlated Multica projection done and creates no new development projection

#### Scenario: 归档后实现卡自动 done

- **WHEN** the archive completion action finds the created execution card
- **THEN** it applies the target `done` state once and records the result for replay

#### Scenario: 归档关闭关联 GitLab Issue

- **WHEN** the completed publication has linked open GitLab Issues and the Connection has the required API capability
- **THEN** the archive Flow attempts to close all linked Issues after projection completion is durable

#### Scenario: 未配置 GitLab token 时降级

- **WHEN** archive completion has no configured GitLab Issue-close capability
- **THEN** Multica completion still proceeds, the missing closure is observable, and a later repair may close the Issues

#### Scenario: 无关联记录的旧 change 归档不报错

- **WHEN** an archived event refers to a historical change with no current correlation
- **THEN** the completion Flow records an actionable diagnostic, returns without an unrelated side effect, and does not fail the webhook process

### Requirement: 配置通过环境变量注入，secret 不入仓库

The runtime MUST no longer treat process-level project maps, webhook secrets, or provider credentials as the authoritative Connection configuration. Deployment environment variables MAY provide bootstrap values, the public Hook base URL, compatibility settings, and secret-manager references; actual Workspace-scoped endpoint, credential, Connection, and Flow state MUST be loaded from the persistent control plane after bootstrap. Signing material and provider credentials MUST never be written to source control, Flow definitions, execution snapshots, or unredacted logs.

#### Scenario: 缺少持久化 Connection 不启动隐式全局路由

- **WHEN** no migrated or configured Connection matches a source event
- **THEN** Bridge reports the routing failure and does not fall back to a global project mapping

#### Scenario: bootstrap 配置只用于建立初始状态

- **WHEN** a deployment supplies legacy environment mappings during migration
- **THEN** SpecWire imports or adopts them into a Workspace-scoped persistent model and records the compatibility path rather than treating the environment mapping as a second live source of truth

#### Scenario: secret 不出现在 Flow 执行记录

- **WHEN** a connector behavior uses a webhook or provider credential
- **THEN** execution and audit views show only a redacted alias or reference and never the secret value

#### Scenario: 缺少必填配置启动失败

- **WHEN** bootstrap configuration required to establish the initial persistent control plane is missing or invalid
- **THEN** the deployment refuses to activate the affected route rather than silently running with incomplete routing configuration

#### Scenario: 归档 API 配置缺失可观测降级

- **WHEN** the archive Flow lacks the optional GitLab API capability for closing the publication Issue
- **THEN** Multica completion proceeds and the skipped Issue closure is recorded as a recoverable warning

### Requirement: Bridge 处理 GitLab change Issue 发布

Bridge MUST treat a matching published input Flow with a GitLab Issue Hook object kind `issue`, action `open`, the `change` label, and a complete publication description as the supported new-publication protocol for the built-in template. The description MUST contain `change_id`, `branch`, and `branch_head_sha`; malformed or incomplete descriptions MUST end in an observable validation result without a Multica side effect. Other input behaviors MAY define their own provider event contract through the ConnectorBehavior registry.

#### Scenario: 合法 change Issue 进入发布 Flow

- **WHEN** a valid `change` Issue arrives for an active Connection with the built-in publication Flow published
- **THEN** Bridge creates one FlowExecution and the Flow can create one correlated Execution Projection

#### Scenario: 非 change Issue 不触发建卡

- **WHEN** an Issue Hook lacks the `change` label or its action is not `open`
- **THEN** the matching publication Flow records skipped and creates no Execution Projection

#### Scenario: 描述缺少发布字段被忽略

- **WHEN** a `change` Issue is missing `change_id`, `branch`, or `branch_head_sha`
- **THEN** the Flow records a validation failure or ignored result and creates no projection or correlation record

#### Scenario: 合法 change Issue 创建执行投影

- **WHEN** a valid `change` Issue arrives for an active Connection with the built-in publication Flow published
- **THEN** Bridge creates one FlowExecution and the Flow creates one correlated Execution Projection

### Requirement: Bridge 保存发布 Issue 与 change 的关联

After the publication Flow creates or safely reconciles a projection, Bridge MUST persist the Workspace, Connection, provider endpoint/project identity, FlowVersion, GitLab Issue IID, `change_id`, source branch, frozen SHA, and target projection correlation. Replaying the same Issue MUST preserve one correlation per intended Flow action. The correlation MUST be sufficient for the archive Flow to close the correct publication Issue without reading OpenSpec content.

#### Scenario: 发布成功后记录关联

- **WHEN** the Multica projection action succeeds for a valid publication Issue
- **THEN** the correlation record is durable with the source/target IDs and FlowVersion before the execution is reported successful

#### Scenario: 关联记录重放幂等

- **WHEN** the same Issue Hook is delivered again to the same publication Flow
- **THEN** the existing correlation is reused and no duplicate projection or correlation is created
