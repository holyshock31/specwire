# bridge Specification

## Purpose

定义 SpecWire Bridge（GitLab 事件桥接组件）的行为契约：事件校验与过滤、幂等判重、项目映射、发布上下文、归档投影闭环与配置安全。

## Requirements

### Requirement: Bridge 校验并过滤 GitLab 事件

Bridge MUST verify the GitLab webhook signature before producing any side effect. It MUST accept only allowlisted projects. For `Push Hook`, it MUST process the target ref only for `archived` completion events; a push containing a publication trailer MUST NOT create a new execution projection. For `Issue Hook`, it MUST process only an `issue` object with action `open` and the `change` label. Other hook kinds, refs, actions, labels, deleted branches, or malformed payloads MUST return an ignored result without creating or changing an execution projection.

#### Scenario: 非法签名请求被拒绝

- **WHEN** a webhook signature is missing, expired, or does not match any configured signing token
- **THEN** Bridge returns HTTP 401 and produces no side effect

#### Scenario: 非目标项目事件被忽略

- **WHEN** a valid webhook is sent for a project outside `SPECWIRE_ALLOWED_PROJECTS`
- **THEN** Bridge returns an ignored result and does not create or update a projection

#### Scenario: 多项目各自 token 均可验签

- **WHEN** allowlisted projects use different signing tokens configured in `SPECWIRE_WEBHOOK_SECRETS`
- **THEN** a valid event signed by any configured token is accepted for normal routing

#### Scenario: 旧单值配置继续可用

- **WHEN** only `SPECWIRE_WEBHOOK_SECRET` is configured
- **THEN** Bridge treats it as a single signing token and preserves webhook verification behavior

#### Scenario: 任一 token 不匹配时拒绝

- **WHEN** the webhook signature matches none of the configured signing tokens
- **THEN** Bridge returns HTTP 401 and produces no side effect

#### Scenario: v1 proposal push 不再发布任务

- **WHEN** a main push contains `proposal-ready` or any other publication trailer
- **THEN** Bridge ignores the publication event and does not create a Multica projection

#### Scenario: 非归档 push 不触发 SpecWire 副作用

- **WHEN** a push is not on the configured target ref, deletes the ref, has no `archived` event, or contains an unknown event
- **THEN** Bridge returns an ignored result and does not create or complete a projection

### Requirement: 判重基于稳定键，保证幂等

For a valid publication, the business stable key MUST include the GitLab project path, `change_id`, frozen `branch_head_sha`, and resolved Multica project ID. SQLite uniqueness MUST ensure that the same publication replay or concurrent delivery creates at most one execution projection. A `processing` or `created` record MUST be treated as duplicate; an `error` record MAY be retried. Bridge MUST NOT retarget an existing projection to a new frozen revision. A changed revision MUST be published as a new `change_id`.

#### Scenario: 同一 Issue 事件重放不重复建卡

- **WHEN** GitLab redelivers the same `change` Issue publication with the same project, change ID, frozen SHA, and target project
- **THEN** Bridge returns duplicate and Multica contains only one execution projection

#### Scenario: 同事件重放不重复建卡

- **WHEN** GitLab redelivers the same valid publication event
- **THEN** Bridge returns duplicate and does not create a second execution projection

#### Scenario: 项目映射变更后重放建到新项目

- **WHEN** the target mapping changes and the same publication is deliberately replayed against a different resolved Multica project
- **THEN** the new stable key permits a projection in the new target while a replay with the same target remains duplicate

#### Scenario: 并发投递只创建一个投影

- **WHEN** concurrent deliveries carry the same publication stable key
- **THEN** exactly one delivery claims creation and all other deliveries return duplicate

#### Scenario: 外部创建失败可重试

- **WHEN** Multica projection creation fails after the stable key is claimed
- **THEN** Bridge records an observable error state and a later delivery may retry the same stable key

#### Scenario: 已发布版本不被动态改写

- **WHEN** a later event carries a different frozen SHA for an existing change
- **THEN** Bridge does not update the existing projection or silently move its baseline; the caller must use a new `change_id`

### Requirement: 项目映射将 GitLab 项目归属到 Multica project

`SPECWIRE_PROJECT_MAP` MUST map each allowlisted GitLab project path to one Multica project title or ID. At startup, a title MUST resolve to exactly one Multica project ID; a missing or ambiguous mapping MUST prevent startup. If no project map is configured, the configured `SPECWIRE_MULTICA_PROJECT_ID` MAY serve as the default target. An allowlisted project without a valid mapping MUST NOT silently fall back to another project.

#### Scenario: 映射加载失败阻止启动

- **WHEN** a configured Multica project title does not exist or resolves to more than one project
- **THEN** Bridge refuses to start and reports the invalid mapping

#### Scenario: allowlist 项目缺映射被拒绝

- **WHEN** an allowlisted GitLab project has no resolved target project
- **THEN** Bridge rejects publication handling without creating a projection in a default or unrelated project

### Requirement: 建卡携带完整上下文

For a valid v2 publication, Bridge MUST create the Multica projection with the repository, `change_id`, source `branch`, frozen `branch_head_sha`, and target branch metadata. The projection MUST carry the initial status and optional assignee from `SpecWire-Status` and `SpecWire-Assignee`, defaulting to the review/backlog state. The context MUST be sufficient to identify the immutable publication, but Bridge MUST NOT clone or interpret OpenSpec files and MUST NOT depend on client-side Skill instructions to define its contract.

#### Scenario: 合法 change Issue 建卡携带冻结上下文

- **WHEN** an opened Issue has the `change` label and valid `change_id`, `branch`, and `branch_head_sha` fields
- **THEN** Bridge creates a Multica projection whose description contains those fields and the configured target branch

#### Scenario: 发布者指定直通状态和分配

- **WHEN** the Issue description contains `SpecWire-Status: todo` and/or `SpecWire-Assignee: <name>`
- **THEN** Bridge applies the requested initial status and assignment to the projection

#### Scenario: 非法状态不建卡

- **WHEN** the Issue description contains a status other than `backlog` or `todo`
- **THEN** Bridge ignores the event and creates no projection

#### Scenario: 建卡描述可被 Agent 直接执行

- **WHEN** a client Agent receives the created projection
- **THEN** the projection contains the repository, change ID, source branch, frozen SHA, and target branch needed by the client to perform its own checkout and delivery steps without additional Bridge-only context

### Requirement: 归档事件自动完成投影闭环

An `archived` event on the target main ref MUST NOT create a development projection. Bridge MUST locate the latest created projection for the GitLab project and `change_id`, mark that projection done, and close every linked GitLab publication Issue when the GitLab API is configured. Failure to close a GitLab Issue MUST remain observable and MUST NOT undo a successful Multica completion; the operation MUST be recoverable or manually replayable. If no projection or link exists, Bridge MUST perform no unrelated side effect and record an actionable diagnostic.

#### Scenario: 归档后实现投影自动完成

- **WHEN** an `archived` Push Hook reaches the target main ref for a published change
- **THEN** Bridge marks the corresponding Multica projection done and does not create a new projection

#### Scenario: 归档后实现卡自动 done

- **WHEN** an `archived` event identifies a published change with a created Multica projection
- **THEN** the corresponding execution card is marked done and no new card is created

#### Scenario: 归档关闭关联 GitLab Issue

- **WHEN** the change has linked publication Issues and GitLab API credentials are configured
- **THEN** Bridge attempts to close all linked open Issues after locating the projection

#### Scenario: 关闭 Issue 失败不回滚投影

- **WHEN** the Multica projection is marked done but closing a linked GitLab Issue fails
- **THEN** the projection remains done, the failure is logged as recoverable, and no source change is rolled back

#### Scenario: 未配置 GitLab token 时降级

- **WHEN** an archived event has a linked publication Issue but `SPECWIRE_GITLAB_TOKEN` is not configured
- **THEN** Bridge completes the Multica projection, records a warning, and leaves Issue closure for later recovery

#### Scenario: 无关联记录的旧 change 归档不报错

- **WHEN** an archived event has no linked publication Issue or no created projection
- **THEN** Bridge records an actionable diagnostic, returns without an unrelated side effect, and does not fail the webhook process

### Requirement: 配置通过环境变量注入，secret 不入仓库

Bridge MUST load runtime configuration from environment variables or the deployment `.env` file, with environment variables taking precedence. Signing tokens MUST use the configured `whsec_` format and valid encoded material. Required allowlist, signing-token, and default/ mapped Multica project configuration MUST fail startup when absent or invalid. GitLab API credentials and URL MAY be absent only when archive-side Issue closure is intentionally disabled; this degradation MUST be observable.

#### Scenario: 缺少必填配置启动失败

- **WHEN** required webhook, allowlist, or Multica target configuration is missing
- **THEN** Bridge refuses to start instead of silently running with an incomplete routing configuration

#### Scenario: 归档 API 配置缺失可观测降级

- **WHEN** `SPECWIRE_GITLAB_TOKEN` is absent during archive completion
- **THEN** Multica completion still proceeds and the skipped GitLab Issue closure is recorded as a warning

### Requirement: CLI 调用安全（参数数组 + 进程组超时）

The Multica execution adapter MUST invoke the CLI with an argument array and MUST NOT concatenate webhook-controlled values into a shell command. Each external call MUST have a bounded timeout that terminates the complete child process group and records a retryable error when the call fails.

#### Scenario: 恶意字段不产生命令注入

- **WHEN** a publication field contains shell metacharacters
- **THEN** the value is passed as a literal argument or input value and no shell command is executed

#### Scenario: CLI 超时可恢复

- **WHEN** a Multica CLI call exceeds the configured timeout
- **THEN** Bridge terminates the child process group, records an error, and returns a result that can be retried

#### Scenario: 恶意 change_id 不产生命令注入

- **WHEN** `change_id` contains shell metacharacters
- **THEN** the value is passed literally and no injected command is executed

#### Scenario: CLI 超时返回 502 可重试

- **WHEN** the Multica CLI exceeds `SPECWIRE_CLI_TIMEOUT`
- **THEN** Bridge returns a retryable gateway error after terminating the process group and records the failed stable key

### Requirement: Bridge 处理 GitLab change Issue 发布

Bridge MUST treat an Issue Hook with object kind `issue`, action `open`, the `change` label, and a complete publication description as the only supported new-publication protocol. The description MUST contain `change_id`, `branch`, and `branch_head_sha`; malformed or incomplete descriptions MUST be ignored without a Multica side effect.

#### Scenario: 合法 change Issue 创建执行投影

- **WHEN** a valid `change` Issue publication arrives for an allowlisted project
- **THEN** Bridge resolves the target Multica project, creates one projection, and records the GitLab Issue correlation

#### Scenario: 非 change Issue 不触发建卡

- **WHEN** an Issue Hook lacks the `change` label or its action is not `open`
- **THEN** Bridge returns ignored and does not create an execution projection

#### Scenario: 描述缺少发布字段被忽略

- **WHEN** a `change` Issue is missing `change_id`, `branch`, or `branch_head_sha`
- **THEN** Bridge returns ignored and does not create a projection or correlation record

### Requirement: Bridge 保存发布 Issue 与 change 的关联

After a publication projection is successfully created, Bridge MUST persist the GitLab project, Issue IID, `change_id`, source branch, and frozen SHA correlation. Replaying the same Issue MUST preserve one correlation and one projection. The correlation MUST be sufficient for an `archived` event to close the correct publication Issue without reading OpenSpec content.

#### Scenario: 发布成功后记录关联

- **WHEN** the Multica projection is created for a valid publication Issue
- **THEN** the correlation record is persisted before the delivery is considered successful

#### Scenario: 关联记录重放幂等

- **WHEN** the same Issue Hook is delivered again
- **THEN** the existing correlation is reused and no second projection or duplicate correlation is created
