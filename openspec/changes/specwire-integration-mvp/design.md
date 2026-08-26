## Context

本 change 是第一版可配置 Integration Flow 的统一设计，取代未实现的 `workspace-instance-onboarding` 规划。当前 Bridge 在单个 HTTP handler 中按 GitLab Issue/Push Hook 分支，直接通过 Multica CLI 执行固定的建卡和完成逻辑；admin 页面仍以进程级 `.env`、项目路径和静态映射为中心。详见 `proposal.md` 的动机与范围。

当前代码已经具备可以复用的安全和生命周期基础：原始 webhook 验签、GitLab `change` Issue 发布、`archived` Push 完成、稳定键判重、Issue 关联、CLI 参数数组和进程组超时。新设计把这些固定路径提取为连接器行为和 Flow 执行器，但不改变 GitLab/Multica/Skills/Agent 的责任边界。

## Goals / Non-Goals

**Goals:**

- 让一个 Workspace 内的 Connection 成为源项目、目标项目、资源、共享 Hook 和多个 Flow 的容器。
- 让管理员配置 ConnectorType/Behavior 和 DataModel，Flow 配置人员通过模板或画布建立可发布的有类型 DAG。
- 用同一个运行时承载内置 GitLab → Multica 生命周期模板和后续可配置 Flow。
- 保留现有发布投影的不可变上下文、 at-least-once、幂等、关联、归档闭环和可恢复失败语义。
- 使草稿、发布版本、执行记录、模型版本、节点版本和外部副作用都可审计和重放。

**Non-Goals:**

- 不实现任意代码节点、用户上传插件、循环、等待、子流程、错误分支、通知编排或通用 iPaaS 的全部节点目录。
- 不把 OpenSpec 内容、Git 操作、Agent 执行、MR review/merge 或 Skills 实现移入 SpecWire。
- 不把 Connection onboarding 的项目创建、Multica 两个资源上下文、标签和 Hook provisioning 变成普通 Flow 节点。
- 不追求跨 GitLab/Multica 的 exactly-once 或跨 provider 自动事务回滚。
- 不在本 change 中支持跨 Workspace Flow 导入、运行时 checkout 凭证托管或任意 provider 的自动发现。

## Decisions

### 1. Control plane 与 Flow runtime 分层

保留两个紧密协作但责任不同的层：

```text
Control plane
  Workspace / Account / Role
  GitLabInstance / MulticaInstance
  Group credential / resource references
  Connection / onboarding / shared Hook
  ConnectorType / Behavior registry
  DataModel registry
  Flow draft / published version / audit

Flow runtime
  webhook ingress
  route resolution
  FlowVersion execution
  node checkpoints
  provider adapters
  retry / replay / reconciliation
```

`GitLabInstance` 和 `MulticaInstance` 仍然是控制面上的 Workspace-owned endpoint/profile，用于 endpoint、外部 ID 和凭证边界；它们不是 Flow 内的 `ConnectorInstance`。Flow 内只保存 `ConnectorNode`，其内容是选择的 ConnectorBehavior 和参数绑定。GitLab 侧的项目发现和控制面操作使用 Workspace 绑定的 Group credential；MulticaInstance 可以先以 endpoint-only 状态注册，只有声明需要管理能力的操作才需要可选的 Multica control-plane credential。

`Connection` 是用户可见的源项目到目标项目的绑定，拥有资源 onboarding 结果、共享 Hook 和 Flow 集合。Flow 默认引用 `$connection.source_project`、`$connection.target_project` 以及 Connection 授权范围内的 credential/resource reference；不在每个节点中复制一套项目映射。

Connection onboarding 可以在没有任何 Flow 时完成。只有发布带 input ConnectorBehavior 的 Flow 时，控制面才注册该 Flow 的事件路由并 reconcile 共享 Hook。这样资源准备与业务事件处理不会互相伪装成同一种节点。

Multica runtime/Agent 使用的 `glab` checkout credential 永远由 runtime 环境管理，不由 SpecWire 申请、保存或注入。Connection 的 `configured` 状态不依赖该 credential；可选的 readiness probe 只报告 runtime 是否可用。若某个控制面 adapter 确实需要 Multica 管理 API，它必须声明对应 capability，缺少该 capability 时操作进入 blocked/ready-failed，而不是借用登录 OAuth token 或 runtime checkout credential。

**考虑过的方案：**

- 将 Connection 仅做成一次性的配置向导，不保存 Flow：无法表达一个 Connection 下的发布和归档等多个行为。
- 让每个 Flow 独立持有源/目标项目和凭证：会重复配置并削弱 Workspace/Connection 的授权边界。
- 把资源创建也做成节点：会把一次性控制面副作用混入可重复事件执行，难以定义幂等和权限。

### 2. ConnectorType、ConnectorBehavior 与 ConnectorNode

使用三层概念，但只把最后一层放进 Flow 图：

```text
ConnectorType       = provider boundary, e.g. GitLab / Multica
ConnectorBehavior   = one declared capability, e.g. Issue Hook / Create Issue
ConnectorNode        = selected behavior + parameter bindings in one FlowVersion
```

一个 ConnectorType 可以有多个行为。每个行为由管理员启用和版本化，并声明：

- `direction`: `input` 或 `output`；
- `parameter_schema`: 生成节点配置表单和校验；
- `input_model` / `output_model`: provider-side port contract；
- `required_capabilities`: 需要的 endpoint、Group credential 或资源能力；
- `adapter_operation` 与版本：指向已部署的安全执行适配器；
- `idempotency_strategy` 与 reconciliation 能力。

Flow 中选择行为后，行为固定到该节点的版本；运行时不能根据业务数据动态切换行为。需要条件选择时，使用多个行为节点和 Condition/Filter 分支。

连接器类型和行为的元数据可以由管理员管理，但执行行为必须来自已部署、审核并 allowlist 的 adapter operation。第一版的“注册”只允许把元数据绑定到已存在的 adapter，或启用/停用/发布其版本；不能在管理界面上传 connector、脚本、二进制或任意 HTTP 执行定义。以后若增加声明式 HTTP adapter，也必须是一个独立的受控 adapter 类型，而不是让 Flow 作者直接拼 HTTP 请求。新增可执行 provider capability 需要服务端部署和审核后才能被注册。

**考虑过的方案：**

- 一个 `GitLab` 节点通过运行时参数决定所有操作：输入/输出模型、权限和审计边界不明确。
- 每个 provider 操作都成为不可扩展的硬编码 handler：无法支持模板之外的配置 Flow。
- Flow 作者上传脚本：灵活但引入代码执行、凭证泄露和不可审计的运行时行为。

### 3. DataModel registry 与 provider contract

DataModel 是独立的数据契约，不是必须出现在画布上的节点。连接器边界使用 provider-specific contract，中间处理节点负责转换为 SpecWire canonical model，再转换为目标行为的 input model：

```text
GitLabEvent
  → Parse/Normalize
  → ChangePublication.v1
  → Mapping/Template
  → MulticaIssueInput
  → Multica Create Issue
```

`DataModelDefinition` 采用统一 registry，至少包含：

- 稳定 `key` 和不可变 `version`；
- JSON Schema 受限子集或等价结构定义；
- required/type/extension-field policy；
- platform semantic roles，如 `change_id`、`branch`、`branch_head_sha`；
- 展示标签、描述和兼容性元数据；
- `published`/`deprecated` 生命周期。

内置模型以版本化声明式定义随产品发布并初始化到 registry；管理员可以新增模型或新版本，不能原地修改已发布版本。运行时的 payload 是模型定义的一个值，不把所有 payload 永久当作独立的控制面资源存储；执行记录只保存受保留策略约束的脱敏快照和引用。

MVP 的内置模型和默认绑定先固定为以下契约：

| Model | Required fields / roles | Default or fixed mapping |
|---|---|---|
| `ChangePublication.v1` | `change_id`、`branch`、`branch_head_sha`；语义角色分别为 change identity、source branch、frozen revision | `source_project`、Issue IID/URL、`target_ref`、`status`、`assignee` 为可选上下文；`target_ref` 缺省为 `refs/heads/main`，`status` 缺省为 `backlog`，`assignee` 缺省为空 |
| `ArchiveCompletion.v1` | `change_id`、source project identity、target ref；另带 provider delivery identity | 只用于查找既有 projection；target ref 默认 `refs/heads/main`，不得创建新 projection |
| `MulticaCreateIssueInput.v1` | target project identity、title、description | target project 默认 `$connection.target_project`；title 默认 `[SpecWire] {change_id}`；status 默认 `backlog`；assignee 可选 |
| `MulticaCompleteIssueInput.v1` | correlated projection identity / `change_id`、desired status | desired status 固定为 `done`；只能使用已持久化 correlation，不根据 Push payload 猜测无关 issue |

Provider event schema 仍归 ConnectorBehavior 管理，不把 GitLab 原始 payload 的全部字段硬编码进 canonical DataModel。内置 GitLab Issue 行为固定匹配 `object_kind=issue`、`action=open` 和 Issue label `change`；内置 GitLab Push 行为固定匹配 `refs/heads/main` 上的 `archived` completion event。

Connection onboarding 的默认值也在 MVP 中固定：Multica project title 默认取 GitLab full path，description/icon/lead/date 不自动填写；GitLab `change` label 采用 create-or-adopt；Hook 默认同时订阅 Issue 和 Push 所需事件，回调地址取 Workspace/deployment 的 public ingress（本地 Compose 沿用 `http://host.docker.internal:8787/gitlab/specwire`）；资源默认使用 runtime 可达的 SSH clone URL，按配置的 instance host alias 生成，无法使用时才回退 HTTPS；新建或采用的资源标记为 `specwire-managed`，已存在资源保留 adopted ownership。除上述默认外，操作者必须填写 GitLab instance、Group、source project、Multica instance、target workspace，以及选择 target project 或确认创建。

核心语义角色由平台定义，模型可以携带扩展字段和自定义字段。GenericNode 通过模型和角色元数据工作，不把 provider 字段名散落在代码中。目标 ConnectorBehavior 声明它接受的 input model 和 required roles，发布校验负责发现不兼容连线。

**考虑过的方案：**

- 不抽象 DataModel，让每一对 connector 自定义转换：随着 provider 数量增加会形成组合爆炸。
- 只传递无类型 JSON：画布无法在发布前发现缺少字段，执行失败会推迟到外部副作用之后。
- 把所有模型写死在 Go/前端代码：无法由管理员增加新版本，也会让模型变更和程序发布强耦合。

### 4. GenericNode 只提供三个受控处理能力

第一版注册以下通用节点：

1. `Parse/Normalize`：将 provider event contract 转换成选定的 canonical DataModel；
2. `Mapping/Template`：字段选择、重命名、默认值、常量、简单拼接和声明式运行时引用；
3. `Condition/Filter`：存在性、相等/比较、字符串谓词和 Boolean `AND`/`OR`。

模型 required/type 校验是端口验证和节点执行的内建能力，不另做通用脚本节点。幂等、重试、关联 ID、审计、超时和凭证解析是平台运行时能力，也不作为普通节点暴露。

节点参数分为固定值、Workspace resource reference 和声明式 runtime-data reference。业务数据只能通过有类型端口传递；节点可以读取只读系统上下文，如 `workspace_id`、`connection_id`、`flow_execution_id`、`event_id` 和 Connection 源/目标引用，但不能修改全局上下文。

### 5. Provider access 与产品权限分离

产品权限由 Workspace membership 和 `admin`、scoped `operator`、`viewer` 决定；provider access 则由 endpoint、Group credential、可选 Multica management credential 和 capability probe 决定。选择项目、onboarding、发布 Flow 时，服务端重新检查当前 Workspace 授权范围及所需 provider capability。MVP 不要求把登录账号映射成 GitLab/Multica 的同名成员，也不把 OAuth/OIDC login token 当作 provider credential；如果配置的 Group credential 没有目标 Group/project 权限，操作必须失败并给出可修复诊断。

### 6. Flow 图、模板和版本

Flow 定义以版本化图文档保存，包含节点、端口、边、行为版本、模型引用、参数绑定和过滤器。FlowTemplate 是同一图格式的起始模板；从模板创建时复制成独立草稿，不和模板建立隐式同步关系。

Flow 生命周期为：

```text
draft → published → paused → archived
```

草稿可以不完整。发布前执行图校验：

- 恰好一个 input ConnectorNode；
- 每条可执行路径到达 output ConnectorNode 或显式 filtered terminal；
- 图无环；
- 条件分支互斥；
- 端口模型兼容；
- 必填参数和授权资源完整；
- ConnectorBehavior、DataModel 和 adapter 版本仍可用。

发布将图编译为不可变 execution plan，并创建新的 FlowVersion。每个 FlowExecution 记录所使用的 FlowVersion、行为版本和模型版本。旧版本只能通过重新发布成为新的 active version，不能原地修改。

运行时使用编译计划而不是直接解释任意前端图 JSON。这样可以在发布时一次性完成模型/权限/拓扑校验，也能让历史执行重放到确定的计划。

### 7. Shared Hook 与 Flow route

同一个源项目的兼容输入行为共享一个 SpecWire-managed Hook。Hook 只负责可靠接收和验证事件；Flow route 负责将事件匹配到 Connection、行为、事件过滤器和已发布 Flow。

路由注册包含：

- Workspace、Connection 和源项目外部身份；
- input ConnectorBehavior/version；
- provider event filter；
- active FlowVersion；
- 共享 Hook 引用。

同一事件匹配多条 enabled Flow 时，所有匹配 Flow 独立建立执行记录，不采用 first-match。每条 Flow 的幂等作用域独立，发布第二条 Flow 不创建第二个 Hook。

草稿保存不产生 provider side effect。发布输入 Flow 时，系统在激活 route 前完成 Hook 创建/领用、签名材料校验和 route reconcile；如果其中一步失败，Flow 保持未发布或进入可恢复错误状态。

### 8. FlowExecution 与可靠性

Bridge ingress 负责读取原始 body、验签、解析路由所需的最小 provider envelope，并把已接受事件和 FlowVersion 持久化后交给异步 executor。HTTP webhook 响应不等待完整 Multica side effect。

FlowExecution 至少包含：

- `event_id`、delivery identity、Connection、FlowVersion；
- 总体状态和当前 node；
- 每个节点的 checkpoint、attempt、输入/输出快照引用；
- provider request/correlation ID；
- retryable、validation、skipped、indeterminate 等错误分类；
- idempotency key 和 reconciliation result。

执行采用 at-least-once。外部行为必须提供 deterministic idempotency key 或查询/对账能力。已知失败从安全的失败节点恢复；外部调用结果不确定时先进入 `indeterminate`/reconciliation-required，不盲目重试。

同一 Connection/源项目尽量使用有序队列分区，但不能依赖 provider webhook 的全局顺序。归档 Flow 找不到发布投影时应等待、重试或报告可修复关联缺失，而不是完成无关投影。

条件分支一次只执行一条路径，第一版不做并行 fan-out、事务回滚或自动补偿。已成功的外部动作不会因后续失败被自动删除；部分成功必须在执行详情中可见。

### 9. Admin UI 与 API 组织

默认管理入口仍以 Connection 列表和资源/健康状态为主。进入某个 Connection 后提供 Flow 列表和 Flow Builder：

```text
Connection detail
├── Overview / resources / Hook
├── Flows
│   ├── template gallery
│   ├── canvas
│   └── version / publish state
└── Executions / audit
```

画布节点的参数表单由 ConnectorBehavior 或 GenericNode 的 parameter schema 驱动。画布显示端口模型、连接校验、必填项、scope 错误和发布阻塞原因；DataModel 默认以端口/边标识展示，并可以展开查看 schema/semantic roles。

API 分成三类：

1. **Control APIs**：Workspace、provider endpoint、credential reference、Connection、resource、Hook 和 route reconcile；
2. **Design APIs**：Connector registry、DataModel registry、Flow draft/version/template、validate/publish/pause；
3. **Runtime APIs**：ingress、execution detail、retry、replay、reconciliation 和 redacted audit。

所有 API 在服务端重新检查 Workspace 和 Group/project scope。凭证只以 alias/reference 出现，Flow 作者不能读取 secret。Flow 版本和执行计划必须在发布/执行之间保持不可变。

### 10. 迁移和当前行为替换

迁移先把旧 `.env` allowlist/project map、可识别 Hook 和 Multica 资源导入 Default Workspace，建立 endpoint/project/resource/Connection 记录，并采用 managed/adopted 规则防止重复。

随后把现有固定路径表达为两个内置 FlowTemplate：

```text
publish-change:
  GitLab Issue Hook → Parse/Normalize → ChangePublication.v1
  → Mapping/Template → Multica Create Issue

complete-archive:
  GitLab archived Push Hook → Parse/Normalize → ArchiveCompletion.v1
  → Mapping/Template → Multica Complete Issue
```

切换期间可以保留有限的兼容读取和单次回滚开关，但不允许新模型和旧 `SPECWIRE_PROJECT_MAP` 同时作为两个活动路由源。成功切换后，旧固定 handler 只保留为迁移参考并移除其直接业务分支。

如果新 Flow runtime 发布失败，Connection 的资源、Hook 和历史投影不回滚；可以暂停新路由，修复或重新发布版本，再对保留事件进行显式 replay。

## Implementation Blueprint

本节把前面的控制面、Flow 和可靠性决策落实为实现约束。它是本 Change 的实现级技术蓝图，不新增产品行为；实现过程中如果要改变这里的默认值，应先更新本 Change 的设计和任务依赖。

### 1. MVP 部署基线与持久化队列

MVP 按单部署、单 Bridge 进程、单 SQLite 数据库实现。Workspace 是逻辑隔离和授权边界，不承诺第一版通过多副本提供高可用；因此不引入 Redis、Kafka 或外部任务平台。SQLite 使用 WAL、busy timeout 和显式连接池配置，数据目录继续由 Compose volume 持久化。

所有数据库变更使用版本化 SQL migration，并由 `schema_migrations` 记录已应用版本。继续使用 `database/sql` 和显式 SQL，不引入 ORM；存储模块负责事务、约束和迁移，业务模块不直接拼接表结构或依赖 SQLite 特有的查询结果。

异步执行使用数据库内的 durable job queue：入站事件、FlowExecution 和待执行节点在同一事务中持久化；worker 通过租约字段（例如 `available_at`、`lease_until`、`attempt_count`）认领任务，进程重启后可重新领取过期租约。worker 使用有界并发；同一 Connection 的任务通过 key 分区或串行闸门保持可解释顺序，跨 Connection 可以并发。未来迁移到 PostgreSQL 或外部队列时，只替换存储/队列模块的实现，不让 HTTP、Flow 或 Provider 模块感知队列产品。

这项基线的边界是明确的：若部署目标变成多副本、跨节点 worker 或高吞吐生产环境，必须先新增架构决策，将 SQLite 队列替换为支持租约和并发消费的持久化方案；不能在当前实现上直接横向扩容。

### 2. Go 模块与深模块 seam

保留 `bridge/` 作为 Go module 和容器构建根，采用渐进式拆分，不一次性重写现有 handler。目标结构如下；目录名是实现建议，职责和接口边界比最终文件名更重要：

```text
bridge/
  main.go
  internal/
    domain/          # Workspace、Connection、Flow、Execution 等领域值和不变量
    store/           # migration、事务和 repository 实现
    controlplane/    # 账号、Workspace、provider profile、credential、Connection、resource、Hook 用例
    registry/        # ConnectorType/Behavior、DataModel 的声明式注册和版本生命周期
    flow/            # 图文档、端口兼容性、校验、编译和版本发布
    runtime/         # ingress、route、job、worker、checkpoint、retry、replay
    provider/
      gitlab/        # GitLab API 和 Hook/Group/project adapter
      multica/       # Multica CLI/API adapter 和 capability probe
    auth/            # local provider、session、OIDC/PKCE、授权上下文
    httpapi/         # REST transport、DTO、错误映射和鉴权 middleware
  admin/
    web/             # TypeScript 管理页面源码和构建产物入口
```

这些是内部模块而非对外微服务。HTTP 模块只依赖用例接口，Flow/runtime 不依赖 HTTP，业务模块不直接创建 `exec.Cmd` 或数据库连接。Provider adapter 是可替换的 seam：真实 GitLab/Multica 实现和 provider fake 共享同一个接口，测试通过接口验证副作用、错误分类和幂等行为。存储模块也提供面向用例的深接口，而不是把每张表的 CRUD 暴露给所有调用方。

### 3. 标识、数据库模型与事务边界

- 所有 SpecWire 实体使用内部 UUID 作为主键；provider 的项目、workspace、Hook、resource、issue ID 作为外部标识保存，并始终与对应的 Workspace 和 instance ID 一起解释。
- 所有控制面表带明确的 `workspace_id` 外键；查询和 mutation 先由授权上下文限定 Workspace，再执行业务条件，不能依赖调用方先传入的对象 ID。
- Connection 的一对一约束由数据库唯一约束和服务端冲突诊断共同保证：同一 Workspace 内，一个 GitLab source project 和一个 Multica target project 不能被两个 active Connection 占用；停用/解绑不删除外部对象或历史执行。
- provider 外部标识、Hook route、managed/adopted resource 和 idempotency key 使用带 instance/Workspace/Connection 的复合唯一约束，避免相同 path 或数值 ID 跨实例碰撞。
- Flow draft 保存图文档；发布时在事务内记录不可变 FlowVersion、行为/模型版本引用和编译计划摘要，再通过可重试的 route-reconcile 操作激活外部 Hook。外部 provider 调用不假装参与数据库事务，调用结果和 checkpoint 必须单独落盘。
- Connection onboarding 的每个 provider side effect 都有 operation/checkpoint 记录。项目创建、两个 Multica resource context、label 和 Hook 的步骤可以部分成功；重试先读取 provider 状态并采用或恢复，不靠回滚幻想保持原子性。
- 入站验签和最小 envelope 解析成功后，在一个事务中写入事件摘要、匹配的 FlowVersion 和可执行 job；HTTP 响应不等待 Multica side effect。原始 payload 和节点快照按 retention 保存并在写入前脱敏。

关键状态机仍以规约为准：Connection 区分 `configured` 与可选 `ready`；Flow 为 `draft → published → paused → archived`；FlowExecution/NodeExecution 使用 `queued`、`running`、成功、`failed`、`skipped`、`indeterminate` 和 `reconciliation-required` 等可观测结果。状态转换集中在 domain/runtime 模块，不散落在 handler 或 UI。

### 4. Provider adapter 合同与联调前置

Provider adapter 对上层提供少量面向用例的深接口，而不是把每条 CLI/API 命令透传到 UI：

- GitLab adapter：列出 endpoint 下的 Group/project、读取 project identity/clone URL、创建或采用 label、创建/更新/读取 Hook、关闭 Issue，并返回 capability 和 provider request identity。
- Multica adapter：列出 workspace/project、创建或采用 project、在 workspace repository registry 中添加/读取 repository、在 project resources 中添加/读取 GitLab resource、创建 Issue、更新 Issue status，并返回 capability、资源 ownership 和 provider request identity。
- 每个可能产生外部副作用的操作必须返回 confirmed、failed 或 indeterminate 结果，并声明其 idempotency/reconciliation 方法；错误统一映射为 unauthorized、forbidden、not-found、conflict、rate-limited、timeout、invalid-response 或 indeterminate 等类别。
- runtime 继续使用安全的参数数组调用 `multica` CLI，不允许 shell interpolation；CLI checkout credential 仍只属于 runtime 环境。可选的 Multica control-plane credential 只由声明了对应 capability 的 control-plane adapter 使用。

在实现资源 onboarding 和 output behavior 前，先用当前本地 Multica CLI 做一个 adapter contract spike，固定 `project list/create/resource`、`repo list/add`、`issue create/status` 的 flags、JSON 字段、workspace 选择方式和失败输出；provider fake 的 contract tests 必须覆盖这些字段。现有 `multica.go` 中“真实 CLI 输出尚未联调验证”的注释在该 spike 完成前不得被当成已验证接口。

### 5. 认证、密钥和授权上下文

第一条可运行路径先启用内置 local provider：密码使用成熟的慢哈希实现，登录后使用数据库 session 和 HttpOnly、SameSite cookie；所有 mutation API 通过同源校验和 CSRF 防护。Workspace membership 和三种固定角色在 middleware 之后、用例之内再次检查，避免仅依赖页面隐藏按钮。

外部 OAuth/OIDC 作为同一 Change 的后续实现，使用 authorization-code + PKCE 和 `(identity_provider_id, subject)` linking；首次登录只创建 pending account，不自动授予 Workspace。登录 token 不能作为 GitLab Group credential、Multica management credential 或 runtime `glab` credential。

MVP 的 secret store 使用由部署环境注入的 master key 对 SQLite 中的密文做 envelope encryption；业务记录只保存 `secret_ref`/alias，API、日志、Flow definition、execution snapshot 和 audit 永不返回明文。master key 轮换和外部 secret manager 作为后续可替换的 secret-store adapter，不把密钥值塞入 Flow JSON 或 provider profile。

### 6. 管理 UI 与 Flow Builder 技术路线

管理页面从当前的内嵌原生静态页面渐进迁移为 React + TypeScript + Vite；构建产物输出到 Go 服务可嵌入的静态目录，HTTP API 采用版本化的 `/api/v1` JSON contract。Builder 使用一个成熟的 DAG canvas library，但节点元数据、端口校验和发布规则全部由服务端返回和校验，前端画布不是业务规则的唯一实现。

页面实现顺序遵循原型的三张图：Connection onboarding → Connection detail/execution → Flow Builder。Onboarding 先完成实例/Group/project/resource 和 Hook plan 的可解释状态；Flow Builder 只编辑 Connection 内的 Flow draft；Execution detail 只展示已固定的 FlowVersion、节点 checkpoint、脱敏快照和恢复动作。未发布 Flow 不激活 Hook route，资源 onboarding 不作为画布节点。

### 7. 实施切片和验收门槛

不把 58 个任务当作一次性大提交，而在同一个 Change 内按以下门槛推进；任务完成仍以 `tasks.md` 的逐项复选框和测试证据为准：

1. **Foundation**：完成持久化 migration、核心领域不变量、storage seam、secret redaction 和声明式 registry 基础；可启动、可迁移、跨 Workspace 查询有测试。
2. **Golden path**：完成 local admin/Default Workspace、GitLab/Multica endpoint profile、Group credential、级联选择、Connection/resource onboarding、内置模型和 `publish-change`/`complete-archive` 模板，使用 provider fake 跑通 Hook route、异步执行、Multica 投影和执行详情。
3. **Authoring surface**：完成 Connection detail、template/blank Flow、拖拽画布、参数面板、端口/模型校验、draft/publish/pause 和模拟执行；浏览器测试覆盖原型中的主路径。
4. **Recovery and cutover**：完成重试、indeterminate reconciliation、replay、审计/retention、外部 OIDC、旧 `.env` 导入、兼容切换和真实 GitLab/Multica E2E；最后才移除旧固定 handler 分支。

每个门槛都必须保留 provider fake 测试；涉及部署的门槛还必须执行 `go test -race ./...` 和 `docker compose build && docker compose up -d`，并记录真实验证结果。任何未完成的门槛不能通过勾选后续任务来掩盖。

## Risks / Trade-offs

- **[Risk] 图编辑器容易把产品变成通用工作流平台** → 第一版只注册三个 GenericNode、有限 DAG 和四个 provider behaviors，发布校验拒绝未注册能力。
- **[Risk] DataModel registry 与 adapter 语义不一致** → ConnectorBehavior 声明 model/role contract，发布和执行前都做 schema/required-role 校验，模型版本不可变。
- **[Risk] 多 Flow 共用 Hook 造成重复业务动作** → 路由按 Flow 独立幂等，发布时提示潜在重叠，运行时不做隐式 first-match。
- **[Risk] 外部调用超时导致重复副作用** → 每个 output behavior 必须提供幂等策略或 reconciliation；不确定结果进入 indeterminate，而不是直接 retry。
- **[Risk] 当前同步 handler 与异步 executor 切换产生回归** → 先将现有固定发布/归档场景迁移成内置模板，使用 provider fakes、重复投递、超时和端到端测试覆盖切换。
- **[Risk] 旧环境配置与新持久化配置分裂** → 兼容读取只用于一次导入/明确迁移窗口，迁移后 persistent control plane 是唯一活动路由源。
- **[Risk] Flow/模型/行为版本过多增加运维成本** → 执行记录保存版本引用，UI 默认只展示 active/deprecated 状态，历史版本只读并可显式重新发布。
- **[Trade-off] 第一版不支持循环、等待和并行分支** → 降低状态机、补偿和资源竞争复杂度；通过多条 Flow 和平台级重试覆盖当前生命周期。
- **[Trade-off] Connection onboarding 与 Flow 分开** → 画布无法编排资源创建，但控制面权限、幂等和外部资源所有权更清楚。

## Migration Plan

1. 更新领域词汇：保留 `GitLabInstance`/`MulticaInstance` 作为控制面 endpoint profile，移除 Flow 内 `ConnectorInstance` 的含义；将用户可见 `IntegrationBinding` 收敛为 `Connection`。
2. 建立持久化 registry 和 Connection/onboarding 数据模型，导入旧 `.env` 配置，发现并采用现有 Hook/资源。
3. 建立 ConnectorType/Behavior、DataModel 和 FlowVersion 注册表，先注册 GitLab/Multica 的四个 MVP behaviors 及两个内置模板。
4. 增加共享 Hook route、异步 ingress/executor、checkpoint、幂等和 reconciliation，先以 provider fakes 验证。
5. 实现 Connection detail 内的 Flow Builder、模板创建、模型端口校验、模拟测试、发布和执行详情。
6. 在兼容窗口内将旧固定 Issue/Push handler 路由到两个内置 Flow；完成真实 GitLab/Multica 端到端验证后关闭旧业务分支。
7. 失败回滚只暂停新路由或重新激活上一个 FlowVersion，不删除已创建的项目、资源、Hook 或历史投影。
8. 实现验收后 archive 本 change，将接受的行为、领域、架构和体验内容分别合并到 `openspec/specs/behavior/`、`domain/`、`architecture/` 和 `experience/`；在此之前不直接修改主 specs。
