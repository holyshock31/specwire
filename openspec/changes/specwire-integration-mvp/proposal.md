## Why

SpecWire 当前把 GitLab 项目映射、Webhook 生命周期、凭证和 Multica 目标配置在固定的 Bridge 路径与环境变量中，无法表达 Workspace 隔离、多个 provider endpoint、可复用的连接器行为和可调整的数据转换。与此同时，GitLab `change` Issue 发布与 `archived` Push Hook 完成投影的生命周期已经被验证，需要在保持投影闭环的前提下升级为可配置的 Integration Flow。

现在落地第一版统一模型，可以让管理员准备 Connection，让配置人员通过模板或拖拽节点建立 GitLab 到 Multica 的真实集成流；本地 Skills 和 Agent 仍然只是发布/执行流程的客户端，不进入 SpecWire 运行时。

## What Changes

- **BREAKING** 将第一版控制面从路径/环境变量映射升级为 Workspace-scoped Connection：Connection 绑定源项目、目标项目、资源、共享 Hook 与 Flow 集合。
- 保留 Workspace、账号、登录 provider、简单角色、GitLab/Multica endpoint 配置、Group-scoped credential、项目发现和 Multica 资源 onboarding；Multica endpoint 可先以无管理凭证状态注册，runtime 的 `glab` 凭证不进入 SpecWire；节点不再使用 `ConnectorInstance` 作为 Flow 内概念。
- 新增 `ConnectorType`、`ConnectorBehavior` 和 `ConnectorNode` 模型。一个连接器类型可以提供多个输入或输出行为；Flow 中的节点是选定行为及其参数绑定。管理员只能配置已部署并审核的 adapter，不能上传任意执行代码。
- 新增声明式、版本化的 DataModel registry。系统提供内置模型，管理员可增加模型版本；模型定义不散落在执行代码中。
- 新增可拖拽的 Flow Builder、FlowTemplate、FlowVersion 和 FlowExecution。草稿与发布分离，发布版本不可变，执行记录固定使用启动时版本。
- 第一版提供解析/标准化、映射/模板、条件/过滤三个通用中间节点；只允许一个输入连接器、无环 DAG 和互斥条件分支，不支持自定义代码、循环、等待、子流程、错误分支或通知节点。
- 将当前固定的 GitLab → Multica 行为迁移为内置模板：`Issue Hook → ChangePublication.v1 → Multica Create Issue`、`main archived Push Hook → ArchiveCompletion.v1 → Multica Complete Issue(done)`，以及受控的 `Issue Hook update + specwire::abandoned 标签新增 → ChangeLifecycle.v1 → Multica Complete Issue(cancelled)`；废弃不再使用 change 分支 Push trailer。
- 将 Bridge 改为共享 Hook 接收、按已发布 Flow 路由并异步执行；保留 at-least-once、幂等、关联、检查点、重试、重放、部分成功和不确定外部结果的恢复语义。
- 发布 Flow 时自动校验并 reconcile 已配置的 Hook；Connection onboarding 负责项目、Multica workspace/project、两个 GitLab 生命周期标签及其资源记录，不把资源创建做成画布节点。
- 提供模板创建、空白 Flow、样例事件模拟、一次性真实连接测试、执行历史和失败重试；敏感数据只保存有期限的脱敏快照。
- 旧的 `workspace-instance-onboarding` 规划被本 change supersede；主 `openspec/specs/` 下的 `behavior/`、`domain/`、`architecture/` 和 `experience/` 只在实现、验收和归档后合并，不在设计阶段直接改写。

## Capabilities

### New Capabilities

- `integration-flow`: ConnectorType/Behavior、ConnectorNode、DataModel、Flow、模板、版本、校验、发布和异步执行行为。

### Modified Capabilities

- `admin`: 将固定项目配置与 `.env` 持久化升级为 Workspace-scoped Connection、资源 onboarding、Flow 管理、模板、发布和执行观测。
- `bridge`: 从固定 GitLab 事件处理改为共享 Hook、Flow 路由和异步执行，同时保留 Issue 发布、archived Push 完成和受控 abandoned Issue 标签取消投影的内置模板语义。
- `workflow`: 保持 GitLab 仍是发布变更的事实源、Multica 是执行投影，Skills/Agent 仍在边界外；将“只处理固定事件路径”改为由已发布 Integration Flow 承载，第一版模板保持现有生命周期闭环。

## Impact

- **Control plane**：需要 Workspace、Connection、provider endpoint、凭证引用、资源、ConnectorType/Behavior registry、DataModel registry、Flow 定义和权限 API；Multica 管理凭证按能力可选，runtime checkout 凭证不属于控制面。
- **Bridge**：需要把同步的固定 handler 拆为事件入口、路由、队列/执行器、节点适配器和执行记录；所有副作用仍须使用安全参数数组、超时、幂等键和审计。
- **Admin UI**：从项目表单扩展为 Connection onboarding、模板选择、可视化画布、节点参数面板、模型/端口提示、发布校验和执行详情。
- **GitLab/Multica**：继续使用 GitLab Issue/Push Hook 和 Multica execution adapter；Hook 按源项目共享，多个匹配 Flow 独立执行。
- **Skills/Agent**：不新增运行时职责；既有 Skill 发布 Issue、归档变更和 Agent checkout/执行的边界保持不变。
- **Migration**：旧 `.env` 映射、Hook 和可识别资源需要导入 Default Workspace；当前固定路径最终由内置 Flow 模板替代。
- **Scope**：这是 SpecWire 第一版可配置集成平台，不是完整 n8n。任意代码、在线上传 connector adapter、循环/等待、通用通知、跨 Workspace Flow 导入和大规模 connector catalog 不在本 change 内。
