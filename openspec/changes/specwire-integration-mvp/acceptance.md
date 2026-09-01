# SpecWire Integration MVP 验收记录

日期：2026-08-28

本记录依据本 Change 的四组行为规约、`design.md` 和 `prototype/` 下的三张正式参考图编写。它是 Change 内的验收证据，不是 `openspec/specs/` 的已发布行为契约。

## 验收环境

- 分支：`change/feat-specwire-integration-mvp`
- Worktree：`/Volumes/d/playground/specwire-worktrees/specwire-integration-mvp`
- 浏览器验证：`agent-browser` 隔离会话，应用 `127.0.0.1:8788`
- Provider fake：GitLab HTTP fixture `127.0.0.1:8791`；Multica 使用 `/tmp/specwire-integration-mvp-fakes/bin/multica` CLI fixture
- 数据库：临时 SQLite；浏览器路径未使用真实 Secret，也未调用真实 GitLab/Multica 外部服务
- `127.0.0.1:8787`：未作为本轮验收服务；本轮使用 worktree 的独立服务，避免与现有进程混淆

## 用例矩阵

| 编号 | 来源 | 验收场景 | 结果 | 证据/说明 |
|---|---|---|---|---|
| A-01 | admin / local auth | 首次创建管理员，再登录并进入 Default Workspace | **通过（浏览器）** | 合法邮箱和 8 位以上密码：bootstrap `201`，login `200`，首页和 Workspace 可加载 |
| A-02 | admin / local auth | 非法首次安装输入被拒绝，并给出可理解反馈 | **通过（浏览器）** | `admin` + 短密码返回 `400 invalid request`；错误文案清空后用合法密码创建成功 |
| A-03 | admin | 登录后添加 GitLab / Multica 实例 | **通过（浏览器）** | fake GitLab/Multica 实例保存成功，Provider 列表可刷新并驱动发现 |
| A-04 | admin / security | 登录会话刷新后仍能执行 mutation | **通过（浏览器）** | 刷新页面后 `auth/me` 恢复 CSRF token，随后 mutation 成功；HTTP 回归测试覆盖 CSRF cookie 缺失时的恢复 |
| A-05 | admin / integration-flow | Onboarding 级联选择 GitLab instance → Group → project、Multica instance → Workspace → project | **通过（浏览器）** | fake provider 返回 Group `platform · 7`、项目 `platform/webdeck · 101`、Workspace `Team Core · mc-ws-1`、目标项目 `WebDeck · mc-proj-1` |
| A-06 | admin / integration-flow | Onboarding dry-run 展示 instance/external ID、资源、Hook 计划和默认值 | **通过（浏览器 + API）** | 预览展示源/目标 ID、默认 label、SSH、Issue/Push Hook、workspace_repository/project/label 资源，且未创建外部资源 |
| A-07 | admin / integration-flow | Onboarding 幂等创建/采用两个 Multica resource context，并支持部分失败恢复 | **通过（浏览器 + 后端）** | 页面最终显示 `ready`、3 个 Managed Resources；`TestConnectionOnboardingCreatesProjectAndBothResourceContexts`、`TestConnectionOnboardingResumesAfterPartialResourceFailure` 通过 |
| A-08 | integration-flow | Connection 下创建多个 Flow，保存空白草稿，模板独立复制 | **通过（浏览器 + 后端）** | 同一 Connection 下创建 `Complete Archive` 与 `Publish Change` 两个独立 Flow；Connection 摘要分别展示版本、状态和执行数 |
| A-09 | integration-flow / prototype | Builder 支持模板、拖拽 Connector/GenericNode、端口/DataModel、参数 Inspector、校验和发布 | **通过（浏览器 + 后端）** | 模板卡片可选择；DOM DragEvent 实际落入 `Condition / Filter` 节点；画布显示 SVG 边、typed ports、DataModel；Inspector 可配置节点；兼容性和校验通过 |
| A-10 | integration-flow | 样例模拟不调用外部动作，非法图阻止发布 | **通过（浏览器 + 后端）** | Builder 的“样例模拟”显示 `外部动作已全部抑制`；Flow 校验和发布成功；非法图回归测试通过 |
| A-11 | bridge / runtime | 共享 Hook、多个匹配 Flow、异步执行、幂等、关联和归档闭环 | **通过（后端）** | runtime、controlplane、provider fake 测试通过；correlation 唯一性已包含 Flow 维度，多 Flow 投影互不串扰 |
| A-12 | workflow / recovery | 执行详情、脱敏快照、Retry、Repair、Replay、版本固定 | **通过（浏览器 + 后端）** | fake 真实连接测试执行成功；详情显示固定 FlowVersion、Correlation/Idempotency、Provider IDs、节点快照；失败状态显示 Retry，indeterminate 状态额外显示 Repair；成功状态仅显示 Replay |
| A-13 | admin / security | Workspace、固定角色、Group scope、viewer 只读、跨 Workspace 探测 | **通过（后端）** | auth/http API 测试通过 |
| A-14 | migration / cutover | 旧配置导入、持久化路由成为唯一活动路由 | **通过（自动化）** | migration、cutover 和持久化测试通过；persistent-only 新安装不再自动生成 legacy provider 实例 |
| A-15 | cutover | Compose 启动后真实 GitLab → Multica 发布/归档 E2E | **部分通过（发布已通过；归档未执行）** | 本轮已用 worktree Compose、真实 GitLab Hook 和真实 Multica CLI 完成发布投影；归档完成及 GitLab Issue 关闭仍未执行 |
| A-16 | admin / provider-auth | 不设置 `SPECWIRE_GITLAB_TOKEN`，仅使用持久化的 Workspace/Group Credential 发现 Group 和项目 | **通过（自动化/API）** | `TestGitLabSelectorsUsePersistedCredentialWithoutProcessToken` 设置进程级哨兵 token，但实例先无 credential，再绑定持久化 instance credential；Group/project 查询均成功，并进一步绑定 Group credential 验证项目查询可使用更窄的持久化 Group credential |
| A-17 | admin / provider-auth | 未配置 GitLab Credential 时查询 Group | **通过（自动化/API）** | 同一测试连续两次查询均返回 `422 provider_credential_required`，响应不含 `internal server error`，且未调用 provider |
| A-18 | admin / provider-auth | GitLab Credential 无效或无权访问目标 Group 时查询/创建 Connection | **通过（自动化/API）** | fake GitLab 无效 token 返回应用层 `401 provider_credential_rejected`；无权项目查询返回 `403 provider_credential_forbidden`；响应不泄露 token |
| A-19 | migration / provider-auth | 旧 `.env` 凭据显式导入后，运行时只使用持久化 Credential；未执行迁移时不读取环境变量作为 fallback | **通过（自动化）** | `TestLegacyGitLabCredentialIsImportedOnlyByExplicitMigration` 验证显式调用 legacy import 会落持久化 SecretRef；persistent-only 配置默认关闭导入，HTTP regression 验证未配置持久化 credential 时不会读进程 token |
| A-20 | admin / provider-auth | fake GitLab 记录 Group/project 请求使用已选择的 Credential，而不是进程级 token | **通过（自动化/API）** | fake GitLab 记录每个 `PRIVATE-TOKEN`；绑定 instance v1 后查询、轮换至 v2 后查询，再绑定 Group credential 覆盖项目查询，所有请求均使用对应持久化值且从未使用进程级哨兵 token |

### 测试状态说明

- A-01～A-15 的状态沿用本轮已有验收记录；其中 A-03/A-05 的“通过”只证明 fake provider 的正常 UI/API 路径，不证明请求使用了持久化 Credential。
- A-16～A-20 是本轮新增的 provider-auth 回归；它们使用临时 SQLite 和 fake GitLab，不包含真实 Secret 或真实外部副作用。
- A-03/A-05 的浏览器通过结果仍只证明正常 UI/API 路径；A-16/A-20 才是“无需 `SPECWIRE_GITLAB_TOKEN` 且请求使用持久化 Credential”的证据。

## 本轮修复

1. correlation projection 的唯一性和查找范围改为 Workspace + Connection + Flow + source identity + publication/delivery identity；归档不再错误地用归档 Flow 查找发布 Flow 的 correlation，而是使用持久化的 `change_id`/`correlation_id` 解析投影。
2. 新增 correlation Flow 维度迁移和回归测试，保留旧 correlation 行的兼容读取语义。
3. `SPECWIRE_PERSISTENT_ONLY=true` 时默认关闭 legacy `.env` 导入；旧运行模式仍保持兼容，显式 `SPECWIRE_LEGACY_IMPORT=true` 可启用导入。
4. persistent-only 启动及首次 bootstrap 会幂等 seed registry 与内置 Flow 模板，避免干净安装没有行为/模型/模板目录。
5. `OnboardingResult` 增加稳定的 JSON 字段名，修复前端读取 `operation/connection/resources/hook_plan/ready` 时出现 undefined 的问题。
6. 管理界面补齐 Connection onboarding/review、Connection 多 Flow 与执行摘要、模板选择、拖放式 typed-port Builder、schema-driven Inspector、执行详情和受控恢复动作；模板选择和执行表头与正式原型语义一致。
7. persistent-only 控制面移除 GitLab 进程级 token 槽；GitLab 实例支持在 Group 选择前绑定发现凭据，Group credential 可覆盖实例凭据；缺少、拒绝或无权凭据返回可行动 4xx，旧 `.env` 只在显式导入路径读取。

## 原型对齐结论

本轮已将三张正式原型的关键交互语义落到 MVP 页面：

1. `connection-onboarding.png` 的四步选择/预览/确认被表达为结构化 onboarding 区域，并展示 provider/external ID、资源计划、Hook 计划和默认值。当前仍是无第三方 UI 框架的 MVP 页面，未承诺像素级复刻。
2. `visual-direction-03-connection-detail-execution.png` 的 Connection 中心模型已落地：一个 Connection 可承载多个 Flow，详情展示共享 Hook、Managed Resources、每个 Flow 的状态/版本/执行摘要，并可进入执行详情。
3. `flow-builder-publish-change.png` 的 Builder 关键语义已落地：Connection 上下文、模板库、Connector/GenericNode palette、拖放节点、可见 typed ports/edges、DataModel 和参数 Inspector、校验/模拟/发布，以及执行入口。全 n8n 级能力仍不在 MVP 范围。

因此，页面与 Change 的主要交互设计已基本一致；A-16～A-20 的持久化凭据链路已通过自动化/API 回归，不能仅凭 fake provider 的 A-03/A-05 通过结果替代这些凭据证据。视觉精修和真实 provider 环境验收仍是另外的验收闸门。

## Goal re-verification（2026-08-29）

本轮在 worktree 的独立临时运行时重新核验了页面结构。为避免接触现有 8787 服务和用户数据，使用工作树数据库的临时副本与脱敏 fixture，服务监听 `127.0.0.1:8788`；不设置 `SPECWIRE_GITLAB_TOKEN`，也没有调用真实 Provider。

| 场景 | 结果 | 证据 |
|---|---|---|
| 登录后进入 Workspace、查看 Connection 列表和 onboarding | **通过** | `/tmp/specwire-integration-mvp-acceptance/connection-onboarding-final.png`；页面显示 Workspace、2 个 Connection、四步 onboarding 和实例/Group/项目字段。本轮临时运行未配置 provider credential，因此没有把空的 Group/project 下拉项冒充为实时发现通过；A-05 的 fake-provider 级联路径仍以既有记录为准 |
| Connection 详情表达完整源端/目标端对象链路 | **通过** | `/tmp/specwire-integration-mvp-acceptance/connection-detail-final.png`、`/tmp/specwire-integration-mvp-acceptance/connection-resources-shared-hook-detail.png`；显示 GitLab instance → Group → project、Multica instance → Workspace → project，以及 4 个 Managed Resources、共享 Hook 和 2 条 route |
| 一个 Connection 下查看多个 Flow | **通过** | `/tmp/specwire-integration-mvp-acceptance/connection-flows-final.png`；显示 `Complete Archive`、`Publish Change` 及各自的 Builder 入口 |
| 固定 Connection 上下文进入 Flow Builder | **通过** | `/tmp/specwire-integration-mvp-acceptance/flow-builder-final-2.png`；显示 Connection 选择器、Connection ID、模板、ConnectorBehavior/GenericNode palette、typed ports、DataModel 和 Inspector |
| 拖拽新增通用节点 | **通过** | `agent-browser drag` 及 DOM DragEvent smoke；新增 `Condition / Filter`，节点数从 4 增至 5，画布仍无重叠 |
| 执行记录和执行详情 | **通过** | `/tmp/specwire-integration-mvp-acceptance/connection-executions-final.png`、`/tmp/specwire-integration-mvp-acceptance/execution-detail-final.png`；显示 Flow/Connection、固定版本、节点 checkpoint、Provider request ID、Replay |

截图和本轮临时运行日志保存在 `/tmp/specwire-integration-mvp-acceptance/`。上述是 UI/本地 fixture 验收证据，不替代 A-15 的真实 GitLab/Multica 外部副作用验收，也不代表已完成 Change 的合并、归档或 specs 同步。

## 完整信息架构复验（2026-08-29）

本轮针对“原型左侧导航不得裁剪”的差异，在同一临时 worktree 服务上完成了菜单和页面复验。浏览器视口调整为 `1600×1200`，使用真实导航点击；每个入口都检查了 `.view.active h1`，确认不是只把菜单文字加回而没有对应页面。

| 导航分组 | 已验证入口 | 页面/数据证据 |
|---|---|---|
| 控制台 | 概览、告警 | Connection、Flow、执行关注项汇总；告警由 Connection/Hook/Execution 持久化状态推导 |
| 集成管理 | 连接管理、GitLab 项目、Multica 项目、HOOK 事件、令牌管理、流程编排、执行记录 | 连接工作台、源/目标项目索引、共享 Hook/事件摘要、脱敏 SecretRef、Flow Builder 和 Execution 上下文均有独立页面 |
| 运营 | 运行状态、同步任务、审计日志 | 组件/策略状态、全 FlowExecution 列表、Workspace 审计列表；同步任务可进入执行详情 |
| 配置 | 实例配置、集成能力、全局配置、环境变量、权限管理 | endpoint/registry 管理、Workspace 默认值、运行边界安全策略、当前 account/workspace binding 和固定角色 |

页面数据均来自当前 Workspace 的 Connection、Flow、Execution、Hook event、Group binding、audit 和 access-context API；令牌页面只显示 Alias/类型/能力，环境变量页面不回显值，Multica runtime 的 `glab` checkout credential 不进入控制面。完整导航因此是实现范围的一部分，三张正式原型图是 Connection 主路径视觉参考，而不是全量页面清单。

## 阻断项与修复状态

- **B-01：CSRF token 在页面刷新后丢失——已修复并回归通过。** 登录时写入非 HttpOnly 的 CSRF cookie；`auth/me` 校验并在缺失/失效时轮换 token，前端启动时恢复 `state.csrf`。
- **B-02：首次安装成功后错误文案不清除——已修复并回归通过。** bootstrap 开始时清空登录错误区域。
- **B-03：浏览器验收缺少可重复的 provider fake 环境——本轮已解除。** 使用临时 fake GitLab HTTP fixture 和 fake Multica CLI fixture 完成主路径；fixture 不进入仓库，也不包含真实 Secret。
- **B-04：控制面凭据来源和错误映射——已修复并回归通过。** persistent-only 请求只解析实例/Group 的持久化 `SecretRef`；缺少凭据为 `422 provider_credential_required`，provider 401/403 分别为 `401 provider_credential_rejected` / `403 provider_credential_forbidden`，且不返回明文 token。

## 自动化验证

以下命令在本 Change worktree 已通过（含本轮凭据链路修复）：

    go test ./...
    go test -race ./...
    go vet ./...
    node --check <(sed -n '/<script>/,/<\/script>/p' bridge/admin/static/integrations.html | sed '1d;$d')
    openspec validate --changes
    git diff --check
    docker compose build

浏览器主路径另外通过 `agent-browser` 在 fake provider 环境执行：bootstrap/login、provider instance、级联 onboarding、dry-run、保存 Connection、模板创建两个 Flow、拖放节点、Inspector、校验、模拟、发布、真实连接测试、执行详情和状态动作展示。

## 未完成的 Change 闸门

本 Goal 已完成关键验收差异修复，但这不等于允许归档 Change。以下事项仍需在后续人工闸门中单独完成或确认：

- OpenSpec tasks `5.8`、`7.5`、`8.4` 的正式浏览器/集成测试代码是否纳入仓库；本轮使用临时 fixture 和浏览器会话完成验收证据，未把 fixture 作为产品代码提交。
- OpenSpec task `8.6` 的真实 GitLab/Multica 外部 E2E 尚缺归档完成、Multica 状态更新和关联 GitLab Issue 关闭；发布投影部分已在本轮通过。
- OpenSpec task `8.7` 的人工验收、合并、归档和同步到 `openspec/specs/`。
- 真实 GitLab/Multica 外部 E2E 的归档闭环及正式浏览器 fixture 是否纳入仓库，仍需按 Change 既定人工闸门确认；发布投影和 A-16～A-20 的 fake-provider/API 验收已通过。

在这些闸门完成前，不应归档或同步本 Change 到 `openspec/specs/`。

## 配置入口 Goal 验收（2026-08-30）

本轮目标是补齐原型中缺失的两个真实管理入口，使用当前 worktree 的临时数据库副本和独立端口验收，不接触 `127.0.0.1:8787` 的运行数据。

| 编号 | 验收场景 | 结果 | 证据 |
|---|---|---|---|
| C-01 | 实例配置以列表页展示 GitLab/Multica 实例、内部 ID、Base URL、凭据引用/能力和状态 | **通过（浏览器 + 临时 API）** | `/tmp/specwire-config-goal.gTg2AA/endpoints-final.png`；DOM 显示列表、指标、内部 ID、URL、凭据/能力、状态和操作列 |
| C-02 | 管理员通过“添加实例”弹窗创建 GitLab 或 Multica endpoint，并在列表中看到新实例 | **通过（浏览器 + 临时 API）** | 添加 GitLab/Multica 弹窗提交均返回 201；列表出现 `Goal GitLab` 和 `Goal Multica` 及其稳定 ID |
| C-03 | 从实例列表执行能力测试和停用，停用后状态变为 disabled 且记录仍保留 | **通过（浏览器 + 临时 API）** | GitLab 测试在缺少持久化 credential 时返回可行动的 422；停用返回 204，列表保留该实例并显示 `disabled`，停用后测试/停用操作被禁用 |
| C-04 | 连接器目录展示 ConnectorType、ConnectorBehavior 版本、DataModel 以及服务端 allowlisted adapter | **通过（浏览器 + 临时 API）** | `/tmp/specwire-config-goal.gTg2AA/registry-final.png`；目录 DOM 和四组 registry GET 均成功，页面展示 types、behaviors、4 个 MVP models 及 adapter allowlist |
| C-05 | 管理员只能从 allowlisted adapter 中选择并注册 ConnectorBehavior，可保存草稿或直接发布 | **通过（浏览器 + 临时 API）** | 表单的 adapter 选项来自 `/registry/adapter-operations`；合法 `multica.issue.create` 注册返回 201，行为可发布并出现在目录 |
| C-06 | 已发布行为可停用/重新启用；新版本复制为独立版本，旧版本不被修改 | **通过（浏览器 + 临时 API）** | 已发布行为停用/启用 PATCH 均成功；“新版本”生成独立 `1.0.1` 草稿，旧 `1.0.0` 仍保持 published |
| C-07 | 新发布行为出现在 Flow Builder palette，实例选择器仍使用同一批 GitLab/Multica 实例 | **通过（浏览器 + 临时 API）** | palette 包含 `goal.output.correct@1.0.0`；停用 GitLab 实例不再进入 GitLab onboarding selector，活动 Multica 实例仍可选；完整 17 个导航入口均可访问 |

实现边界：本轮不引入 `ConnectorInstance`，不读取 `SPECWIRE_GITLAB_TOKEN` fallback，不上传或执行任意代码，不修改已发布 `openspec/specs/`，也不要求重启现有 8787 服务。

本轮实例能力测试使用临时地址 `http://127.0.0.1:1`，且没有配置真实 provider credential；因此 C-03 验证的是失败映射、可行动错误和停用生命周期，不把外部 provider 成功访问冒充为真实联通。真实 GitLab/Multica 成功 E2E 仍由 A-15、8.6 负责。

## 旧数据导入记录（2026-08-30）

通过显式 legacy import 将既有项目配置导入当前 worktree 的持久化数据库。旧配置中的 `specwire/specwire-poc` 已在 GitLab 中迁移为 `personal/specwire`，本次按当前 provider 实际项目路径归一化为 `personal/specwire → SpecWire PoC`，同时保留 `personal/webdeck → WebDeck`；未修改旧 `.env`。

- 目标数据库完整性检查通过；保留原有管理员、Default Workspace、内置目录和 Multica 项目记录。
- 导入结果：2 个 `ready` Connection、8 个 Managed Resource、2 个 GitLab Hook、4 个已发布内置 Flow、2 个 onboarding operation（均为 `ready`）和 3 个持久化 SecretRef。
- 导入过程只在一次性进程中读取旧 GitLab token，并将其写入目标 Workspace 的加密 SecretRef；正式 `8787` 容器恢复为 persistent-only，容器环境不包含 `SPECWIRE_GITLAB_TOKEN`，运行时也不将其作为 fallback。
- WebDeck/SpecWire 的已有 Multica 资源继续保留；由于历史资源使用的 clone URL 与当前 GitLab API 返回的 canonical SSH URL 不同，Multica 侧可能同时存在历史 URL 资源和本次导入的 canonical URL 资源，未自动删除任何外部资源。

## Connection 工作台目标复验（2026-08-30）

本轮在 /Volumes/d/playground/specwire-worktrees/specwire-integration-mvp 的 worktree 服务 127.0.0.1:8787 上复验 Connection 工作台。复用已有登录会话，只读取当前 Workspace 的持久化数据，不读取浏览器凭据，不调用 GitLab/Multica 外部写操作。

| 编号 | 验收场景 | 结果 | 证据 |
|---|---|---|---|
| G-01 | Connection 列表显示源项目、目标项目、Flow 数、资源数、状态和健康摘要 | **通过（浏览器 + API）** | 页面显示 2 条 Connection，均显示 2 个 Flow · 4 个资源、健康 · 连接；列表 API 由 Workspace-scoped 聚合读模型提供 flow_count、resource_count、health |
| G-02 | 无匹配筛选时，计数、左侧列表和右侧详情保持一致，不残留旧详情 | **通过（浏览器）** | 输入 no-such-connection 后显示 0 / 2 条、无匹配提示、右侧详情隐藏且无选中行；清除筛选后恢复 2 / 2 条 |
| G-03 | Connection 详情表达 GitLab instance → Group → project 与 Multica instance → Workspace → project，并保留内部 ID | **通过（浏览器）** | /tmp/specwire-connection-management-goal/02-connection-detail.png；实例名称不再显示 (legacy)/(internal)，端点显示为“内部部署端点”，实例/Group/Workspace/项目 ID 单独展示 |
| G-04 | 一个 Connection 下显示多个 Flow、版本、状态、输入/输出 ConnectorBehavior 和 DataModel 契约摘要 | **通过（浏览器）** | /tmp/specwire-connection-management-goal/04-flows-contracts.png；Complete Archive 和 Publish Change 均显示 Builder 入口、输入行为、输出行为和 DataModel 链路 |
| G-05 | 详情可查看 Managed Resources、共享 Hook、Hook routes 与执行记录入口 | **通过（浏览器）** | /tmp/specwire-connection-management-goal/03-resources-hook.png、05-executions.png；资源显示类型/Provider/实例 ID/外部 ID/所有权，Hook 显示来源项目和 2 条 route，执行页显示 0 条空态 |
| G-06 | Connection 列表聚合统计跨 Workspace 隔离，并正确统计 Flow、资源、执行和成功执行 | **通过（自动化）** | TestConnectionStatsAggregatesWorkspaceScopedRecords；空 Connection 与含 1 Flow、2 Resource、1 成功 Execution 的聚合结果均通过断言 |
| G-07 | 左侧导航/Connection rail 不产生横向溢出 | **通过（浏览器）** | /tmp/specwire-connection-management-goal/01-workbench.png、07-workbench-fresh.png；DOM 检查 body.scrollWidth == viewport width，.sidebar 与 .connection-rail 的 scrollWidth == clientWidth |
| G-08 | 未配置/真实 Provider 外部副作用路径的完整 E2E | **未执行（既有人工闸门）** | 本轮只验证当前已导入数据的控制面读路径；真实 GitLab/Multica E2E 仍由 A-15/8.6 负责，不以本轮页面读路径代替 |

本轮补齐了 Connection 列表专用的聚合读模型；它不改变 Connection、Flow、Hook、Managed Resource 或 Execution 的持久化语义。Flow 契约摘要由已保存的 Flow graph 读取 ConnectorBehavior 和 DataModel 引用。Flow Builder 是 Connection-scoped editor；Workspace 级“集成流”只提供 Flow Catalog，不把 Connection 详情变成完整编排画布，也不建立第二套 Flow 归属关系。

## 真实 GitLab → Multica 发布通路复验（2026-08-31）

本轮针对真实通路中发现的 Multica CLI 契约错误进行修复和复验。服务运行于 `/Volumes/d/playground/specwire-worktrees/specwire-integration-mvp/bridge` 的 worktree Compose，保持 `persistent-only`，容器未配置 `SPECWIRE_GITLAB_TOKEN`；GitLab 的控制面访问来自当前 Connection 绑定的持久化凭据，runtime 的 `glab` checkout 凭据仍不进入 SpecWire。

| 场景 | 结果 | 证据 |
|---|---|---|
| 已有 GitLab Issue Hook 进入 Publish Change Flow | **通过** | 已有 Hook delivery `81/82` 返回 `202`，对应 GitLab `personal/webdeck` 的 Issue `10/11`；未新增测试事件 |
| Multica Create Issue 使用受支持 CLI 参数 | **通过** | 移除不受支持的 `issue create --metadata` 参数；真实 Multica CLI `0.4.29` 接收创建请求 |
| 真实发布执行 `a1d50cb6-3ef5-49d7-a29d-c4826f9838c3` | **通过** | 重试后 `FlowExecution=succeeded`，4 个节点全部成功，Multica Issue `6df84afb-2e42-4243-86e0-0038779fedff`（`WW1-18`）存在，Correlation 已落盘 |
| 真实发布执行 `b258d9dd-dc33-4b9d-87b5-74d789fc99de` | **通过（含对账）** | 外部 Issue `9cf87491-99f8-4661-9867-c2b34e267bf6`（`WW1-19`）已存在但未关联时，重试先命中 Multica duplicate；新增按项目 + 标题 + 描述精确检索后安全采用，最终 `FlowExecution=succeeded`，Correlation 已落盘 |
| 重复保护 | **通过** | 两个 `change_id` 各自只有一个目标 Issue；重试保留原 FlowVersion、Connection、幂等键，并未使用 `--allow-duplicate` |

本轮同时补充了 Multica adapter 回归测试：验证 issue-create 不发送平台幂等字段，且在 provider duplicate/result-uncertain 错误后能够通过受限精确检索采用已有 Issue。归档 Flow、Multica 状态完成和关联 GitLab Issue 关闭尚未在真实环境执行，因此 A-15/任务 8.6 仍不能视为完整通过。

## Flow Builder Connection → Flow 入口复验（2026-08-31，部分入口已被本轮 IA 修正）

本轮修复 Builder 入口在只选择 Connection、未选择 Flow 时的断链问题。验证使用当前 worktree Compose 的 http://127.0.0.1:8787/admin/integrations 和已导入数据，没有创建、删除或修改任何 Connection/Flow。

| 场景 | 结果 | 证据 |
|---|---|---|
| 从导航直接进入“流程编排”并默认打开 Builder | **已被本轮修正，不再作为当前行为** | 历史实现曾由主导航直接进入 Builder；当前主导航改为“集成流”Flow Catalog，Builder 必须保留 Connection 上下文 |
| 在 Builder 中切换同一 Connection 的 Flow | **通过（浏览器 + DOM）** | 选择 22a4a141-70fb-4ef2-9824-36fe16d7d0af 后标题切换为 Publish Change，执行摘要固定为该 Flow，两个 Flow 选项仍保留 |
| 在 Builder 中切换 Connection | **通过（浏览器 + DOM）** | 切换到 64847e94-cdf9-4738-8bb0-a523e50903f5（personal/webdeck）后加载其两个 Flow，默认标题为 Complete Archive，画布内容可见 |
| 从 Connection 详情 →“集成流”→“打开 Builder”进入指定 Flow | **通过（浏览器 + DOM）** | personal/specwire 详情展示 Complete Archive、Publish Change 两个入口；点击第一个后进入 view-builder，Flow 下拉仍含两个选项并打开 Complete Archive |
| Connection 没有 Flow 时展示明确空态和创建动作 | **通过（代码路径审查）** | renderBuilderEmpty() 为无 Flow 分支提供“当前 Connection 暂无 Flow”、模板创建和空白 Flow 动作；当前两个真实 Connection 都已有 Flow，本轮未为验证该分支新增临时持久化数据 |
| 鼠标黑箭头/蓝色光晕是否属于页面功能 | **通过（源码范围确认）** | 页面没有固定光标/光晕 DOM 或样式；截图中的光晕属于浏览器自动化/截图指针叠加，不纳入产品实现 |

截图证据：

- /tmp/specwire-flow-builder-connection-flows.png：直接进入 Builder 后的 Connection/Flow 选择器和画布。
- /tmp/specwire-connection-flows-entry.png：Connection 详情的多个 Flow 与 Builder 入口。

## Flow Catalog 与 Connection-scoped Builder 复验（2026-08-31）

| 场景 | 结果 | 证据 |
|---|---|---|
| 主导航“集成流”展示当前 Workspace 的 Flow 总览，并支持按状态、Connection 和关键词筛选 | **通过（代码 + DOM 路径）** | `view-flows`、`flow-catalog-table`、状态/Connection 筛选器和 `renderFlowCatalog()` 已接入既有 Workspace-scoped `state.flows` 与执行聚合 |
| Flow 总览的每条记录显示所属 Connection、源/目标项目、实例/Workspace 上下文、版本/状态和执行摘要 | **通过（代码 + DOM 路径）** | Catalog 行由 `flowCatalogContext()` 从 Connection detail 读模型补齐源端实例/Group/项目与目标端实例/Workspace/项目，不创建新的持久化关系 |
| 从 Flow Catalog 打开 Builder 时保留所属 Connection，不能在 Builder 中重新选择项目或实例 | **通过（代码 + DOM 路径）** | Catalog action 调用 `openBuilderForConnection(connection_id, {flowID})`；`builder-connection` 仅保留为隐藏内部状态，页面展示只读 Connection scope |
| Builder 展示完整的 Connection、GitLab 源端和 Multica 目标端范围 | **通过（代码 + DOM 路径）** | `flowCatalogScopeMarkup()` 渲染 Connection ID、GitLab 实例/Group/项目 ID、Multica 实例/Workspace/项目 ID；创建/编辑提示明确映射由 Connection 固定 |
| 模板选中态文字保持可读，不再出现蓝底蓝字的低对比 | **通过（源码样式）** | `.template-card` 与 hover/selected 状态显式设置深色默认文字和蓝色选中文字 |
| 重新构建 worktree bridge 并提供最新页面 | **通过（运行态）** | `docker compose build bridge`、`docker compose up -d bridge` 成功；容器为 `Up`，启动日志为 `persistent-only cutover enabled` / `bridge listening`，`GET /admin/integrations` 返回 `200` |
| 登录后浏览器截图验证新的 Catalog 与 Builder 视觉布局 | **未执行（人工闸门）** | 当前自动化浏览器未持有登录会话；已完成静态 HTML/JS 路径检查，需登录后人工确认最终视觉密度和响应式布局 |

本轮执行的验证命令：

    git diff --check
    node --check <(sed -n '/<script>/,/<\/script>/p' admin/static/integrations.html | sed '1d;$d')
    go test ./...
    openspec validate --changes
    docker compose build bridge
    docker compose up -d bridge

容器重建后 specwire-bridge 为 Up，映射 0.0.0.0:8787->8787/tcp。

## 执行记录人工关注状态（2026-08-31）

本轮为执行结果补充独立的人工关注状态。`FlowExecution.status` 仍表示真实执行结果；`attention_status` 只表示该结果是否需要操作员继续关注。确认不会把失败改成成功，也不会删除历史记录；重试或修复重新排队时会清除关注状态，后续再次失败会重新打开。

| 编号 | 验收场景 | 结果 | 证据 |
|---|---|---|---|
| A-21 | 新建失败或待对账执行默认进入 `open`，Connection 聚合统计未确认数量 | **通过（自动化）** | `TestConnectionStatsAggregatesWorkspaceScopedRecords`、`TestRequeueFlowExecutionRollsBackAndAllowsOneConcurrentRetry`；失败执行读取为 `open`，Connection 的 `unacknowledged_execution_count` 为 1 |
| A-22 | 操作员确认已知晓后保留失败结果，并记录操作人和时间 | **通过（自动化）** | `TestIntegrationFlowAPIUsesConnectionScopeAndLifecycle`；`POST /executions/{id}/attention` 返回 `status=reconciliation-required` 与 `attention_status=acknowledged`，详情保留 actor/time |
| A-23 | 操作员可取消已知晓重新打开关注，且产生审计记录 | **通过（自动化）** | 同一 HTTP 集成测试验证 `acknowledged → open` 及 `execution.attention.update` 审计事件 |
| A-24 | 重试/修复清除关注状态；连接器健康与告警只统计仍为 `open` 的可行动失败 | **通过（自动化 + 页面代码路径）** | 存储回归测试验证重新排队后为 `none`；Connection 健康聚合按未确认计数优先；管理页只将 `open` 执行计入告警、摘要和待关注列 |
| A-25 | 非失败、非待对账执行不允许人工关注操作 | **通过（代码路径）** | API handler 对 queued、running、succeeded、skipped 返回冲突；其持久化状态规范化为 `none` |

本轮执行的验证命令：

    go test ./internal/domain ./internal/store ./internal/httpapi ./internal/runtime
    node --check <(sed -n '/<script>/,/<\/script>/p' admin/static/integrations.html | sed '1d;$d')
    git diff --check

尚未用浏览器重新截图验证本轮新增的“确认已知晓/取消已知晓”按钮；因此它不替代既有浏览器人工闸门。现有 `7.5`、`8.4`、`8.6`、`8.7` 等 Change 闸门仍按前文状态执行，不能因为本轮自动化通过而归档 Change。

## 新建 Connection 显式选择（2026-08-31）

本轮修正新建 Connection 表单：不再自动选择第一条实例、Group、项目或 Workspace；下级选项只在操作者显式选择上级对象后加载，目标项目仅在勾选自动创建时允许留空。

| 编号 | 验收场景 | 结果 | 证据 |
|---|---|---|---|
| A-26 | 打开新建 Connection 时所有连接上下文为空，不预填第一条记录 | **通过（代码路径）** | `fillEndpointSelectors` 只渲染占位项并保留空值；`resetOnboardingState` 清空所有级联选择和自动创建复选框 |
| A-27 | 未完成必填选择时点击预览或保存 | **通过（代码路径）** | `validateOnboardingSelection` 列出缺失的实例、Group、项目、Workspace 或目标项目，并在调用 onboarding API 前返回 |
| A-28 | 选择上级对象后逐级加载下级资源 | **通过（代码路径）** | `loadGroups`、`loadProjects`、`loadMulticaWorkspaces`、`loadMulticaProjects` 均先清空下级值，且不再自动调用下一级加载函数 |
| A-29 | 开启隐藏已绑定项目后，已被活动 Connection 占用的源/目标项目不出现在下拉列表 | **通过（自动化 + 代码路径）** | `exclude_bound=true` 由 GitLab/Multica 项目选择 API 在服务端按 Workspace、实例 ID 和外部项目 ID过滤；`TestSelectorProjectFiltersExcludeOnlyActiveWorkspaceBindings`、`TestGitLabSelectorsUsePersistedCredentialWithoutProcessToken` 覆盖活动绑定排除、停用绑定释放和 HTTP 选择器路径 |

尚未用浏览器重新截图验证空表单、级联选择和筛选开关的视觉状态；因此 A-26～A-29 的“通过”不替代浏览器人工闸门。

## 真实连接测试 Push Hook 修复（2026-08-31）

本轮修复了 `Complete Archive` 在 Builder 中点击“真实连接测试”必然返回 `400 invalid request` 的问题。原因是页面只提交副作用确认，而 `gitlab.push-hook` 没有适配的默认样例事件；后端现在为内置 Push Hook 生成符合 GitLab 归档事件和 `ArchiveCompletion.v1` 入口要求的一次性样例。调用仍创建正常的持久化执行，是否能完成 Multica 状态更新取决于当前 Connection 是否存在对应的 publication correlation；没有关联时进入可恢复的 reconciliation-required 是预期结果，不冒充成功。

| 编号 | 验收场景 | 结果 | 证据 |
|---|---|---|---|
| B-05 | `Complete Archive` 已发布 Flow 不带 `sample_event` 发起真实测试 | **通过（后端 + HTTP）** | `TestLiveTestCompleteArchiveUsesPushHookDefaultSample`、`TestIntegrationFlowAPIUsesConnectionScopeAndLifecycle`；请求返回 `202`，创建 `live-test:` 执行，事件包含 Push Hook 的 main ref、commit trailer、`change_id` 和 provider delivery ID |
| B-06 | 默认 Push Hook 样例进入脱敏持久化路径 | **通过（自动化）** | runtime live-test 回归检查从 InboundEvent 读取样例；已有敏感字段脱敏测试继续通过，默认样例不包含凭据 |
| B-07 | 既有 `Publish Change` 真实测试和确认闸门不回归 | **通过（HTTP）** | 同一 HTTP 集成测试验证未确认请求仍为 `400` 且返回 `detail`，确认请求仍为 `202` |
| B-08 | 页面展示 API 的可操作校验原因 | **通过（代码 + 脚本语法）** | HTTP 错误响应增加安全的 `detail` 字段，前端 `api()` 将其合并到错误文案；`node --check` 通过 |

本轮尚未把真实 Push Hook 样例用于真实 Multica 外部状态变更；当前默认样例使用唯一 `LIVE-*` change ID，若没有对应的 publication correlation，执行会按既有设计进入对账状态。真实 GitLab/Multica 归档闭环仍由任务 `8.6` 和人工闸门负责，不因本轮入队修复而标记完成。

## 集成能力台账重构（2026-08-31）

本轮将原“连接器目录”改为“集成能力”，解决 Provider 实例、Connection、ConnectorBehavior、DataModel 和 Adapter Operation 被平铺展示而无法理解的问题。页面默认以“连接器行为台账”为主视图；DataModel 和提供方类型分别位于独立页签。连接器行为关系详情明确展示 `ConnectorType → ConnectorBehavior → Provider event schema / DataModel → allowlisted adapter operation`，并注明实际实例、项目和凭据来自 Workspace / Connection，而不是行为记录自身。

| 编号 | 验收场景 | 结果 | 证据 |
|---|---|---|---|
| D-01 | 管理员从页面能明确知道该页管理的是 Flow 节点能力，不是 Provider 实例或 Connection | **通过（页面结构 + 文案）** | 页面标题改为“集成能力”，顶部边界说明和对象关系链区分实例配置、连接管理、连接器行为、DataModel 与运行适配器 |
| D-02 | 默认主视图按连接器行为展示每个可拖入 Flow 的节点能力 | **通过（页面结构）** | `连接器行为台账` 每行显示行为名称、所属提供方、入口/出口方向、端口契约、Flow 用途、版本状态和“查看关系” |
| D-03 | Provider event schema 与 canonical DataModel 不再共用模糊标签 | **通过（页面渲染逻辑）** | 行为契约按 `Provider event schema` / `DataModel` 区分；输入行为的 Provider 事件契约不被伪装成 DataModel 清单中的规范化模型 |
| D-04 | 管理员可查看行为与类型、契约、适配器及 Flow 使用方式的关系 | **通过（页面交互代码）** | 行为行的“查看关系”打开关系详情；详情注明 ConnectorNode 的 Flow 用法、Connection 提供的实例/项目/凭据及 adapter/capability 元数据 |
| D-05 | 管理员可以新建 DataModel 或从既有模型创建新版本，已发布版本不原地修改 | **通过（页面 + API + 既有 HTTP 回归）** | DataModel 页签提供新建/新版本表单，调用 Workspace-scoped `POST /registry/data-models`；新版本默认 draft 且 key 固定；`TestIntegrationFlowAPIUsesConnectionScopeAndLifecycle` 已验证注册接口 |
| D-06 | DataModel 可发布/停用，Schema、required fields、semantic roles 和扩展策略可查看 | **通过（页面 + API）** | DataModel 表格展示用途、必填字段、semantic roles/扩展策略和 Schema 折叠查看；操作调用 `PATCH /registry/data-models/:id` |
| D-07 | 既有 Flow Builder、连接器行为注册和 Workspace 隔离不回归 | **通过（脚本 + Go）** | `node --check`、相关 Registry/Flow/HTTP/Runtime 测试和全量测试通过；Builder 仍从同一行为注册表刷新 palette，后端 API 语义未变 |
| D-08 | worktree Bridge 能加载新的管理页面 | **通过（运行态）** | 重建 `specwire-bridge` 后容器为 `Up`，`GET /admin/integrations` 返回 `200`；登录后视觉截图仍需人工闸门确认 |

本轮执行的验证命令：

    node --check <(sed -n '/<script>/,/<\/script>/p' bridge/admin/static/integrations.html | sed '1d;$d')
    git diff --check
    go test ./internal/registry ./internal/controlplane ./internal/httpapi ./internal/flow ./internal/runtime
    go test ./...
    go test -race ./...
    go vet ./...
    openspec validate --changes
    docker compose build bridge
    docker compose up -d bridge

本轮未修改 `openspec/specs/`；页面语义和验收记录仍属于当前 Change，待 Change 整体接受后再按分类同步到 published specs。

## abandoned 生命周期与 Multica 终态（2026-09-02）

本轮修正废弃 change 的投影闭环。Multica CLI/API 的合法状态包含 `cancelled`，但当前 Multica 看板没有单独的 Cancelled 列；因此 Bridge 的事实状态仍写入 Multica `cancelled`，SpecWire 通过执行结果、Correlation 和 provider effect 提供可审计证据，不把取消伪装成 `done`，也不依赖本机 CLI 另起一条控制路径。

| 编号 | 验收场景 | 结果 | 证据 |
|---|---|---|---|
| L-01 | main 上的 `archived` Push 将活动投影转为 `done` | **通过（定向自动化）** | `TestIngressAndExecutorCompleteArchiveCorrelation`；Multica status 与 GitLab Issue close 断言通过 |
| L-02 | 已有 Change Issue 的 `Issue Hook update` 明确新增 `specwire::abandoned` 标签后转为 `cancelled` | **通过（定向自动化）** | `TestAbandonIssueRequiresControlledLabelTransition`、`TestIngressAndExecutorAbandonCancelsProjection`；状态、Correlation、GitLab note/close 均有断言 |
| L-03 | abandon 路由只接受精确标签 transition；标签已存在、没有 `changes.labels`、仅描述变更或其他 action 均不触发 | **通过（定向自动化）** | `internal/runtime/lifecycle_test.go` 覆盖新增标签、已有标签、无 label diff、无关更新和错误 action |
| L-04 | 取消后的投影不会被重复 abandon、Bridge 自己的后处理更新或较晚 archived 事件复活 | **通过（定向自动化）** | `TestIngressAndExecutorAbandonCancelsProjection` 断言终态保护以及不重复调用 Multica、GitLab note/close |
| L-05 | persistent-only 运行时使用 Connection 绑定的 Multica profile | **待运行态复验** | 代码路径由 `MulticaAdapter` 使用持久化实例/profile；需重建并重启 worktree Bridge 后用真实 `WW1-20` 做一次受控验证 |
| L-06 | Multica 看板展示取消状态 | **不属于 SpecWire 本 Change** | 当前 Multica 前端未提供 Cancelled 列；这不影响 API/CLI 的 `cancelled` 状态。若产品要求看板可见，需要在 Multica 产品/镜像侧另立变更 |

本轮只修改 worktree 中的 Bridge 和 Change 文档，没有重启当前运行容器；因此 `127.0.0.1:8787` 仍可能运行旧镜像，真实 `WW1-20` 的取消状态不会因本轮代码落盘自动改变。部署和真实 E2E 仍属于任务 `8.6`，需单独授权后执行。
