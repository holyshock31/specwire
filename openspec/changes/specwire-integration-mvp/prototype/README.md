# SpecWire 原型材料

这里保存本 Change 的候选原型和视觉参考，不是生产实现，也不是已经接受的产品体验契约。

## 当前材料

- `connection-onboarding.png`：Connection 创建向导的最终参考图，展示 GitLab 源项目、Multica 目标项目、资源准备、Hook 计划、预检，以及保存 Connection 后继续创建第一个 Flow。
- `visual-direction-03-connection-detail-execution.png`：最近一版的连接详情视觉方向，包含 Connection 下的 Flow 列表、Flow 执行记录和执行详情。它来自 Product Design 的原始生成产物。
- `flow-builder-publish-change.png`：`Publish Change` 的全屏 Flow Builder 原型，展示注册节点、类型化端口、DataModel、节点配置、发布前校验和发布动作。

三张图按同一条用户路径表达：`Connection Onboarding` → `Connection Detail / Execution` → `Flow Builder`。其中 Flow Builder 仍按同一条 canonical Change 链路表达：`GitLab Issue Hook` → `Parse / Normalize` → `Mapping / Template` → `Multica Create Issue`；数据契约为 `ChangePublication.v1` → `MulticaCreateIssueInput.v1`。`Complete Archive` 才使用 archived Push Hook。图中的 `v2.1.0-draft.3` 发布后创建下一版 `v2.2.0`，不覆盖已发布版本。

## 与规约的关系

这些图片是 `openspec/changes/specwire-integration-mvp/` 的候选视觉参考，不是生产实现，也不是已经接受的产品体验契约。`openspec/specs/` 仍然只记录已经接受并实现的行为；在本 Change 实现、验收并归档前，不要求主 specs 与图片中的新控制面和 Flow 模型一致。

## 页面范围

- Onboarding 图覆盖 Connection 的首次配置：选择 GitLab/Multica 实例、Group 与项目，选择或创建 Multica project，准备两个资源上下文，并执行预检。它表达 `Connection` 可以先完成配置；Hook 仅显示为计划状态，必须等第一个带 input ConnectorBehavior 的 Flow 发布后才创建或领用并注册 route。
- Connection Detail 图假设 Connection 已完成配置，聚焦 Connection 下的多个 Flow、Flow 摘要、执行记录和执行详情；它不重复展示实例选择、项目发现或资源 onboarding。
- Builder 图假设用户已进入一个 Connection，聚焦一个 `Publish Change` 草稿的节点编排、参数映射、类型校验和发布；模板选择、空白 Flow 初始态、Connection 资源管理和共享 Hook 管理属于其他页面或前置流程。
- 实例 ID、外部项目/Workspace ID、资源 ownership、Hook route 和 capability 检查仍以 Change 的 admin/integration-flow 规约为准，不因图片未展示而省略。

## 术语与状态约定

- Builder 中 GitLab Issue Hook 下的“事件契约”标签表示 `ConnectorBehavior` 的 provider 事件契约，不是 DataModel registry 中的模型。图片中若出现 `GitLabIssueEvent.v1` 这样的版本串，也只表示该 provider contract 的版本；DataModel 以端口/边契约展示，当前内置模型包括 `ChangePublication.v1`、`ArchiveCompletion.v1`、`MulticaCreateIssueInput.v1` 和 `MulticaCompleteIssueInput.v1`。
- 执行详情中的重试只对失败或不确定结果、且可以安全继续的执行启用；成功执行不提供可用的重试动作。重放始终需要选择 FlowVersion，并明确确认可能产生的外部副作用。
- Flow 内没有 `ConnectorInstance`；画布节点表示已注册的 ConnectorBehavior 或受控 GenericNode 加参数绑定，Connection 才拥有项目、资源、共享 Hook 和授权边界。

旧的 `onboarding` HTML throwaway 原型已删除；它基于旧的项目 onboarding 设计，不能代表当前以 Connection 和 Flow 为核心的方案。

## 取舍

本目录只保留每个页面的一张最终参考图。最近生成的其他 onboarding 方向属于候选，不作为 Change 资产入库；旧的连接管理图、拼接截图和被替换的中间版本也不恢复。生成工具缓存中的图片不具有项目文档的权威性。
