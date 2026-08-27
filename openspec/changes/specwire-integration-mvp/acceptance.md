# SpecWire Integration MVP 验收记录

日期：2026-08-27

本记录依据本 Change 的四组行为规约、`design.md` 和 `prototype/` 下的三张正式参考图编写。它是 Change 内的验收证据，不是 `openspec/specs/` 的已发布行为契约。

## 验收环境

- 分支：`change/feat-specwire-integration-mvp`
- Worktree：`/Volumes/d/playground/specwire-worktrees/specwire-integration-mvp`
- 浏览器验证：Chrome CDP browser automation，使用隔离临时 SQLite 数据库和 `127.0.0.1:8788`
- 现有服务 `127.0.0.1:8787`：只读打开和截图，未执行 mutation

## 用例矩阵

| 编号 | 来源 | 验收场景 | 结果 | 证据/说明 |
|---|---|---|---|---|
| A-01 | admin / local auth | 首次创建管理员，再登录并进入 Default Workspace | **通过（浏览器）** | 合法邮箱和 8 位以上密码：bootstrap `201`，login `200`，首页和 Workspace 可加载 |
| A-02 | admin / local auth | 非法首次安装输入被拒绝，并给出可理解反馈 | **部分通过** | `admin` + 6 位密码返回 `400 invalid request`；错误本身正确，但成功后旧错误文案仍残留 |
| A-03 | admin | 登录后添加 GitLab / Multica 实例 | **部分通过** | 重新登录后两个实例都能保存；Provider 列表刷新会立即触发发现请求 |
| A-04 | admin / security | 登录会话刷新后仍能执行 mutation | **失败（阻断）** | 刷新页面后 `auth/me` 只恢复账户，不恢复 CSRF token；添加实例返回 `403 csrf validation failed` |
| A-05 | admin / integration-flow | Onboarding 级联选择 GitLab instance → Group → project、Multica instance → Workspace → project | **未通过浏览器验收** | 页面有级联控件，但需要真实 provider 或可注入的浏览器 fake；当前隔离环境未执行外部发现 |
| A-06 | admin / integration-flow | Onboarding dry-run 展示 instance/external ID、资源、Hook 计划和默认值 | **后端通过，页面部分** | provider fake 和 API 测试覆盖；实际页面是 Connections 页内嵌表单 + JSON 预览，不是原型中的四步向导和右侧预览 |
| A-07 | admin / integration-flow | Onboarding 幂等创建/采用两个 Multica resource context，并支持部分失败恢复 | **后端通过** | `TestConnectionOnboardingCreatesProjectAndBothResourceContexts`、`TestConnectionOnboardingResumesAfterPartialResourceFailure` |
| A-08 | integration-flow | Connection 下创建多个 Flow，保存空白草稿，模板独立复制 | **后端通过，浏览器未完成** | Flow service/API 测试通过；隔离页面没有可用 Connection，无法进入完整 Builder 主路径 |
| A-09 | integration-flow / prototype | Builder 支持模板、拖拽 Connector/GenericNode、端口/DataModel、参数 Inspector、校验和发布 | **部分通过** | 代码和 Flow 测试覆盖；页面存在 Builder，但当前是全局导航下的简化画布，和正式 Builder 原型的上下文、布局、端口交互表达有明显差异 |
| A-10 | integration-flow | 样例模拟不调用外部动作，非法图阻止发布 | **后端通过，浏览器未完成** | `TestSimulatePublicationSuppressesExternalConnector`、`TestSimulateRejectsInvalidDraftBeforeExternalAction` |
| A-11 | bridge / runtime | 共享 Hook、多个匹配 Flow、异步执行、幂等、关联和归档闭环 | **后端通过** | runtime、controlplane、provider fake 测试通过 |
| A-12 | workflow / recovery | 执行详情、脱敏快照、Retry、Repair、Replay、版本固定 | **后端通过，浏览器未完成** | runtime/http API 测试通过；页面控件存在，但未完成带实际执行记录的浏览器验收 |
| A-13 | admin / security | Workspace、固定角色、Group scope、viewer 只读、跨 Workspace 探测 | **后端通过** | auth/http API 测试通过 |
| A-14 | migration / cutover | 旧配置导入、持久化路由成为唯一活动路由 | **自动化通过** | migration、cutover 和持久化测试通过；真实部署未验收 |
| A-15 | cutover | Compose 启动后真实 GitLab → Multica 发布/归档 E2E | **未执行** | 需要人工确认外部副作用、现有凭据和服务状态；未用测试结果冒充通过 |

## 原型对齐结论

当前实现不是三张正式原型的等价实现，主要差异如下：

1. `connection-onboarding.png` 是独立的四步 Connection Onboarding 页面，带右侧 Connection Preview、资源状态、Hook 计划和确认动作；实际实现把 onboarding 压缩成 Connections 页面中的一个内嵌卡片，预览是 JSON `<pre>`，信息层级和操作顺序不同。
2. `visual-direction-03-connection-detail-execution.png` 以 Connection Detail 为中心展示多个 Flow、Flow 摘要、执行列表和右侧执行详情；实际页面只有在已有 Connection 后才能进入对应区域，当前尚未完成带真实数据的浏览器验收，不能宣称与原型一致。
3. `flow-builder-publish-change.png` 是 Connection 上下文中的全屏 Builder，带节点库、画布工具栏、类型化端口、Inspector、模型契约和发布校验；实际页面虽然提供画布、节点库、Inspector 和 JSON 编辑器，但整体是同一静态页中的简化区域，交互层级、端口连线表达和视觉结构明显简化。
4. 新建的 persistent-only 数据库仍会由兼容导入逻辑生成 `GitLab (legacy)` 和 `Multica (legacy)` endpoint 记录；这会让“新方案首次进入”与原型中的已配置实例状态混在一起，需要在接受前明确清理或重新定义初始化行为。

## 阻断项

- **B-01：CSRF token 在页面刷新后丢失。** 这是实际功能阻断，不是视觉差异；用户重新打开页面后无法添加实例、创建 Connection、保存 Flow 等任何 mutation。
- **B-02：首次安装成功后错误文案不清除。** 会让用户误以为管理员创建失败。
- **B-03：浏览器验收缺少可重复的 provider fake 环境。** 后端 fake 已有，但不能直接驱动真实浏览器主路径；因此 Onboarding、Connection Detail、Builder 和 Execution Detail 尚不能作为“已验收”交付。

## 自动化验证

以下命令在本 Change worktree 已通过：

```text
go test ./...
go test -race ./...
go vet ./...
openspec validate --changes
docker compose build
```

自动化通过只证明后端/接口/构建层行为，不替代浏览器主路径和原型一致性验收。

## 验收结论

**当前 Change 不通过最终验收。** 后端核心纵向切片基本可用，但至少需要先修复 B-01，并补齐可重复的浏览器验收路径；随后再处理三张正式原型与实际页面的信息架构和视觉落差。未执行真实 GitLab/Multica E2E，也不应归档或同步本 Change 到 `openspec/specs/`。
