## ADDED Requirements

### Requirement: Workspace 身份和简单角色隔离控制面

SpecWire MUST provide a Workspace isolation boundary for accounts, connector configuration, credentials, Connections, Flows, executions, resources, and audit records. An account MAY belong to multiple Workspaces. The built-in local provider and configured external OAuth/OIDC providers MUST identify accounts without implicitly granting Workspace membership. The roles MUST be limited to `admin`, scoped `operator`, and `viewer`. Provider access MUST be evaluated from the selected endpoint, Workspace credential binding, and required capability; the logged-in account's GitLab or Multica membership is not an implicit prerequisite.

#### Scenario: 外部登录不自动获得 Workspace 权限

- **WHEN** an external OAuth/OIDC identity logs in for the first time without a Workspace membership
- **THEN** the account remains pending or read-only until an administrator grants membership

#### Scenario: Operator 只能管理授权范围

- **WHEN** a scoped operator manages a Connection or Flow for a GitLab Group outside the operator's binding
- **THEN** the mutation is rejected and no provider side effect occurs

#### Scenario: Viewer 不能发布 Flow

- **WHEN** a viewer attempts to edit credentials, mutate a Connection, or publish a Flow
- **THEN** the operation is rejected while read-only mappings, resources, executions, and redacted audit data remain visible

#### Scenario: 登录身份不冒充 provider 成员

- **WHEN** a user logs in through local login or external OAuth/OIDC and the Workspace has a configured Group credential
- **THEN** provider discovery and onboarding use the authorized Workspace credential and capability checks, while the login token is not stored or presented as a GitLab/Multica integration credential

### Requirement: Provider endpoint 和 Group credential 可管理

An administrator MUST be able to add, test, disable, and list multiple GitLab and Multica endpoint profiles within a Workspace. A GitLab Group credential MUST support the configured PAT or Group Access Token secret profile, subgroup inheritance, capability checks, safe rotation, and redacted display. A Multica endpoint MAY be registered without a management credential; such a credential is required only for an explicitly declared control-plane capability. Runtime `glab` checkout credentials MUST remain outside SpecWire. The same physical endpoint MAY be registered independently in another Workspace with different credentials and IDs.

#### Scenario: Workspace 添加多个 GitLab endpoint

- **WHEN** an administrator registers two GitLab endpoint profiles in the same Workspace
- **THEN** each profile has a distinct internal ID and projects with identical paths remain distinguishable by endpoint and external project ID

#### Scenario: Group credential 继承子组

- **WHEN** a Group credential is configured with subgroup inheritance
- **THEN** authorized project discovery includes inherited subgroup projects without copying the secret to every project

#### Scenario: 凭证轮换不泄露旧值

- **WHEN** an administrator rotates a Group or optional Multica control-plane credential
- **THEN** future operations use the new secret reference, while UI, API responses, logs, and audit events never reveal either secret value

#### Scenario: Multica endpoint 可无管理凭证注册

- **WHEN** an administrator registers a Multica endpoint but no control-plane capability requiring a management credential has been selected
- **THEN** the endpoint is persisted and can participate in project/workspace selection or readiness reporting without requesting runtime `glab` credentials or treating the endpoint as unusable

### Requirement: Connection onboarding 选择并准备源项目和目标项目

The admin surface MUST allow an authorized operator to select a GitLab endpoint, Group, and project through server-backed searchable selectors, then select a Multica endpoint, existing workspace, and existing project or request project creation. The server MUST recheck Workspace scope and the selected credential's current provider capability. An active Connection MUST retain endpoint IDs, external project/workspace IDs, and enforce one-to-one source-to-target ownership in v1. Unless overridden by an authorized operator, project creation MUST use the GitLab full path as title, leave optional description/icon/lead/date unset, and use the target defaults defined by the built-in models.

#### Scenario: 通过级联选择器创建 Connection

- **WHEN** an operator selects a GitLab endpoint, Group, project, Multica endpoint, workspace, and target project
- **THEN** the review shows all instance and external IDs and the operator can run a dry-run before mutation

#### Scenario: 创建 Multica project 使用默认值

- **WHEN** the operator chooses create instead of an existing Multica project
- **THEN** SpecWire uses the GitLab full path as the default title, leaves optional description/icon/lead/date fields unset, and requires explicit handling of a title conflict

#### Scenario: 一对一冲突可见且不覆盖

- **WHEN** the selected GitLab project is already bound or the selected Multica project is claimed by another active Connection in the Workspace
- **THEN** onboarding reports a conflict and does not silently reassign or overwrite either Connection

#### Scenario: provider capability 不足时阻止 onboarding

- **WHEN** the Workspace role is sufficient but the selected Group credential or optional Multica management capability cannot discover or mutate the selected provider resource
- **THEN** onboarding is blocked with an actionable capability error and does not claim the Connection as ready

### Requirement: Connection onboarding 幂等配置两个 Multica 资源上下文

Onboarding MUST be a durable, retryable operation that can add or adopt the same runtime clone URL in both the selected Multica workspace repository registry and the selected Multica project resources. Existing matching resources MUST be adopted rather than duplicated. The default clone URL MUST be the runtime-reachable SSH URL derived from the registered GitLab endpoint alias, with HTTPS as an explicit fallback. Managed resources MUST carry the `specwire-managed` ownership marker; adopted resources MUST retain adopted ownership. Onboarding MUST distinguish `configured` from optional `ready`, preserve partial progress, and require explicit confirmation for destructive deprovisioning.

#### Scenario: 两个资源位置都配置

- **WHEN** a Connection onboarding operation completes successfully
- **THEN** the GitLab repository is accepted or adopted in both Multica resource contexts and each result is recorded with ownership and external identifiers

#### Scenario: 网络失败后重试不重复创建

- **WHEN** onboarding fails after one resource context has succeeded and the operator retries
- **THEN** SpecWire resumes from the durable checkpoint, re-reads provider state, and does not create a duplicate project or resource

#### Scenario: Runtime glab 不属于配置凭证

- **WHEN** the optional runtime readiness probe cannot access the Multica Agent's `glab` configuration
- **THEN** the Connection may remain `configured` with a visible `ready` failure, and SpecWire does not request or store the runtime checkout credential

#### Scenario: 默认资源和 Hook 语义可见

- **WHEN** an operator reviews a dry-run with no overrides
- **THEN** the preview shows the `change` label, the shared Issue/Push Hook using the configured public ingress, the default SSH clone URL, the `specwire-managed` marker for resources SpecWire creates, and the target project title derived from the GitLab full path

## MODIFIED Requirements

### Requirement: 项目配置可视化管理

The admin surface MUST manage Workspace-scoped Connections rather than a process-global allowlist and path-only mapping. It MUST show the selected source endpoint/project and target endpoint/workspace/project, stable internal and external IDs, Connection status, Flow count, managed/adopted resources, and the latest onboarding or execution health. It MUST support creating, disabling, unbinding, and inspecting a Connection through server-backed selectors without exposing `.env` or secret values.

#### Scenario: 添加 Connection

- **WHEN** an authorized operator selects a GitLab endpoint, Group, project, Multica endpoint, workspace, and existing or newly created project
- **THEN** the admin surface creates a reviewable Connection onboarding operation with the source and target IDs and does not mutate providers before the operator confirms

#### Scenario: 添加项目

- **WHEN** an authorized operator selects a GitLab project and a Multica target while creating a Connection
- **THEN** the source project enters the Workspace-scoped Connection inventory with its target mapping, resource plan, and Flow configuration entry

#### Scenario: 移除项目

- **WHEN** an administrator disables or unbinds a Connection from a GitLab project
- **THEN** the active routing and mapping are removed without deleting the external GitLab/Multica projects, retained resources, or historical executions

#### Scenario: Connection 下管理 Flow

- **WHEN** an operator opens a configured Connection
- **THEN** the page lists its draft, published, paused, and archived Flows and allows the operator to open the Flow Builder within the Connection scope

#### Scenario: 禁用 Connection 停止新路由

- **WHEN** an administrator disables or unbinds a Connection
- **THEN** new event routing stops, historical FlowExecutions and external projects/resources remain visible, and no unrelated Connection is changed

#### Scenario: 跨 Workspace 查询被拒绝

- **WHEN** a client changes an ID in a Connection or Flow request to an object owned by another Workspace
- **THEN** the API returns an authorization or not-found result without revealing the other Workspace's existence or performing a mutation

### Requirement: Hook 生命周期自动化

The admin surface MUST show the shared Hook status for each source project and MUST reconcile one SpecWire-managed Hook for the compatible input behaviors of its active Connections. Publishing the first input Flow MAY create or adopt the Hook and register its route; publishing additional compatible Flows MUST add routes without duplicating the Hook. The surface MUST support signing-secret rotation, preserve unrelated provider Hooks, and display redacted provider results.

#### Scenario: 发布第一条输入 Flow 时创建 Hook

- **WHEN** an authorized operator publishes the first GitLab input Flow for a configured Connection
- **THEN** SpecWire creates or adopts one managed Issue/Push Hook as required by the selected behaviors and registers the Flow route

#### Scenario: 新增 Flow 复用 Hook

- **WHEN** a second Flow for the same source project and compatible event set is published
- **THEN** the admin surface shows the same managed Hook with an additional route and no duplicate Hook is created

#### Scenario: 项目无 hook 时创建

- **WHEN** a configured Connection publishes its first input Flow and no managed Hook exists for the source project
- **THEN** SpecWire creates or adopts the shared managed Hook and registers the published Flow route

#### Scenario: 轮换 Hook secret

- **WHEN** an administrator rotates the signing secret for a managed Hook
- **THEN** the provider Hook and SpecWire secret reference are updated atomically from the control-plane perspective, the old secret is no longer accepted, and the secret value is never displayed

#### Scenario: token 轮换

- **WHEN** an administrator rotates the signing token for a shared managed Hook
- **THEN** the Hook uses the new token, the old token is rejected, and the rotation result is recorded without exposing either token

### Requirement: 配置持久化与生效

Workspace, account, provider endpoint, credential reference, Connection, resource, Flow, route, execution, and audit changes MUST be persisted in the control-plane store with explicit Workspace ownership. The new persistent model MUST be authoritative after migration; legacy `.env` mappings MAY be read only through a bounded import or compatibility path and MUST NOT be silently written as a competing source of truth. Draft saves MUST not activate provider side effects; Flow publication and Connection onboarding MUST record actor, checkpoint, version, and redacted result.

#### Scenario: 旧配置导入 Default Workspace

- **WHEN** the deployment contains legacy project mappings and managed Hooks that can be identified
- **THEN** migration imports them into a Default Workspace with resolved endpoint/project IDs and adopts matching resources without creating duplicates

#### Scenario: 重启后持久化模型继续生效

- **WHEN** the service restarts after a Connection, Flow, or credential-reference change
- **THEN** the persisted Workspace-scoped state is restored and no stale process-global mapping silently overrides it

#### Scenario: 草稿保存不产生外部副作用

- **WHEN** an operator saves an incomplete Flow draft
- **THEN** only the draft and audit/checkpoint state change; no GitLab Hook, Multica project, resource, or execution is created

#### Scenario: 保存后重启生效

- **WHEN** the service restarts after a Connection, Flow, or credential-reference change
- **THEN** the persisted Workspace-scoped model is restored and published routes continue to use it without requiring a process-global mapping

#### Scenario: 非法配置被拒绝保存

- **WHEN** a submitted Connection, resource, Flow, or credential reference contains an invalid value or unauthorized resource
- **THEN** the mutation is rejected with an actionable error and no partial provider side effect is reported as successful
