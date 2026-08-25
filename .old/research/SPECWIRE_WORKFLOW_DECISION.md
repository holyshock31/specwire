# SpecWire 架构决策：采用 Workflow 方案

> 决策编号：ADR-001  
> 日期：2026-08-18  
> 状态：**已接受（Accepted）**  
> 当前阶段：单仓库、小团队 PoC  
> 当前实施基线：本文档  
> 暂缓方案：[idea.v3.md](./idea.v3.md) 与 [deprecated.IMPLEMENTATION_GUIDE.md](./deprecated.IMPLEMENTATION_GUIDE.md) 中描述的 Product 方案

## 1. 决策摘要

当前采用 **SpecWire Workflow**，暂不开发独立的 SpecWire 产品。

在当前阶段，SpecWire 被定义为：

> 一套连接 OpenSpec、GitLab 和 Multica 的工作流协议、Skill、Runbook 与集成配置。

现阶段直接复用现有产品：

- OpenSpec 负责规格 artifact、instructions、status、validate 与 archive。
- GitLab 负责 Git、受保护分支、MR、CI、审核和通知。
- Multica 负责 backlog、Agent 调度和执行状态。
- Skill 负责指导人和 Agent 按约定调用这些工具。

当前**不实施**：

- SpecWire Core 或 Lifecycle Policy 包。
- 独立 `specwire` CLI。
- `state.yaml`。
- generation 和 contract hash。
- 常驻 Reconciler 服务。
- SpecWire 数据库和管理界面。
- 对 OpenSpec validate、status、instructions 或 archive 的重新实现。

## 2. 决策背景

最初的 Product 方案希望把 spec 生命周期完整写入 Git，并通过显式状态机、generation、contract hash 和 Reconciler 获得自动一致性。该方案可以处理并发修订、过期 Agent 结果、重复 webhook、投影漂移和自动恢复，但需要长期维护新的 CLI、规则模块、Webhook Adapter、Reconciler 和 CI 门禁。

当前要验证的核心假设更简单：

1. 本地 Agent 能否基于 OpenSpec 生成合格规格。
2. 人能否检查后将规格发布到 GitLab。
3. GitLab 事件能否让 Multica 创建一张不立即执行的 backlog 卡片。
4. 人能否在 Multica 中批准开工，由 Agent 完成开发并创建 GitLab MR。
5. 人能否复验、合并实现并归档 OpenSpec change。

这些假设可以通过现有产品和少量集成配置验证。先开发完整控制层会把“验证工作流价值”和“建设新产品”耦合在一起，增加前置成本，也可能重复实现 OpenSpec 已有能力。

## 3. 两套方案

### 3.1 方案 A：SpecWire Workflow（已采用）

```text
本地 Agent + OpenSpec 写规格
  → 人检查规格
  → Publish Skill：validate + commit + push
  → GitLab main
  → 事件桥接或周期扫描
  → Multica 创建 backlog
  → 人将 backlog 移出并批准开工
  → Multica Agent 开发
  → GitLab branch + MR + CI
  → 人复验 MR
  → 合并实现
  → openspec archive
  → 提交归档结果
```

交付物是一套 Workflow Kit：

1. 发布规格的 Skill。
2. Git commit、分支和 MR 元数据约定。
3. Multica `create_issue` Autopilot 与 backlog 规则。
4. 实现 Agent Runbook。
5. 复验与归档 Skill。
6. GitLab 到 Multica 的事件桥接配置。
7. 漏事件的 schedule 补偿任务。

### 3.2 方案 B：SpecWire Product（暂缓）

```text
OpenSpec 生成规格
  → specwire finalize-new
  → specwire approve
  → state.yaml + generation + contract hash
  → Reconciler 创建或校准 Multica 卡片
  → Agent 按固定 generation 开发
  → CI 校验 generation/hash
  → MR
  → specwire verify
  → specwire archive
```

需要自研和维护：

- SpecWire CLI。
- 生命周期规则模块。
- `state.yaml` Schema。
- generation 与 contract hash。
- Git Publisher。
- TaskEnvelope。
- Webhook Adapter。
- Reconciler。
- CI Validator。
- 备份、监控、升级和凭据轮换机制。

## 4. 方案差异

| 维度 | Workflow（当前） | Product（暂缓） |
|---|---|---|
| SpecWire 定位 | 工作流协议与集成方案 | 独立软件产品 |
| 规格工具 | 直接使用 OpenSpec | 复用 OpenSpec，并增加控制层 |
| 新代码 | Skill、Runbook、CI 脚本或很薄的 Adapter | CLI、策略模块、Reconciler、Adapter、CI 校验 |
| 规格事实 | Git 中的 OpenSpec artifact | Git 中的 OpenSpec artifact + `state.yaml` |
| 人工批准 | 人把合格 change 发布到受保护 main | 显式 `specwire approve` 迁移 |
| 批准版本 | Git commit SHA | generation + contract hash + Git SHA |
| 执行闸门 | Multica backlog；人移出后开工 | Reconciler 根据持久状态派发 |
| Agent 输入 | issue 中记录批准 commit SHA | TaskEnvelope 记录 generation/hash |
| 重复事件 | 稳定键搜索；接受极低概率竞态 | 幂等计划和唯一约束 |
| 漏事件 | schedule 或人工补发 | Reconciler 自动补齐 |
| 开发中修改 spec | 人工取消旧任务并重新发布 | generation 自动使旧结果失效 |
| 过期结果 | 人工检查 commit SHA | CLI/CI 自动拒绝 |
| Multica 卡片丢失 | schedule 或人工重建 | Reconciler 自动重建投影 |
| 审计来源 | Git、MR、Multica 活动记录 | 额外的显式生命周期记录 |
| 运维负担 | 低 | 高 |
| 适用范围 | 单人、小团队、少量仓库、低并发 | 多团队、多仓库、高并发、强审计 |

## 5. 采用 Workflow 的理由

1. 当前目标是验证端到端工作流，而不是先证明自研控制层。
2. OpenSpec 已覆盖规格 artifact 的生成指导、状态检查、校验和归档。
3. Git commit SHA 已经可以作为不可变的批准版本标识。
4. GitLab 已覆盖分支、MR、CI、审核和通知。
5. Multica 已覆盖 backlog 和 Agent 调度；backlog 可以作为人工开工闸门。
6. 单仓库、小团队、单 change 低并发场景可以接受人工恢复。
7. Workflow 的约定可以保留升级到 Product 所需的稳定标识，不会形成不可逆路线。

## 6. 当前工作流契约

### 6.1 事实与投影

| 信息 | 当前权威 |
|---|---|
| 规格内容 | GitLab `main` 中的 OpenSpec artifact |
| 人工批准 | 人把通过校验的 change 发布到受保护 `main` 的动作 |
| 批准版本 | 发布 commit 的 Git SHA |
| 开发任务和运行状态 | Multica issue/run；属于操作投影 |
| 实现结果 | GitLab implementation branch、MR 和 CI |
| 完成规格 | 合并实现后产生的 OpenSpec archive commit |

Multica 卡片不是规格事实源。卡片丢失时，应根据 GitLab 中的已发布 change 重建，而不是反向修改 Git 以迁就卡片。

### 6.2 发布规格

Publish Skill 必须执行：

1. 检查当前仓库和目标 change。
2. 调用 OpenSpec status/instructions/validate。
3. 明确提示人确认“本次 push 即批准发布”。
4. 一次只发布一个 change。
5. 精确暂存该 change 及必要的 OpenSpec 文件，不使用 `git add -A`。
6. 提交并 push 到受保护 `main`。

建议 commit message：

```text
spec(add-user-login): publish proposal

SpecWire-Event: proposal-ready
SpecWire-Change: add-user-login
```

批准 commit SHA 不写入同一个 commit；它由 GitLab push event 的 `after` 字段提供。

### 6.3 Multica backlog

每张 backlog 卡必须至少记录：

```yaml
repository: <GitLab project path or project ID>
change_id: <OpenSpec change ID>
approved_commit_sha: <GitLab push after SHA>
target_branch: main
```

稳定键定义为：

```text
<GitLab project ID>:<change ID>:<approved commit SHA>
```

创建卡片前必须在目标 Multica project 中按稳定键查重。PoC 接受并发投递仍可能产生重复卡片；发现重复时由人关闭多余卡片。

卡片必须停留在 backlog，且不得因创建或预分配立即触发 Agent。人在 Multica 中明确将其移出 backlog 后，才表示批准开工。

### 6.4 Agent 实现与 MR

实现 Agent 必须：

1. 读取 issue 中的 repository、change ID 和 approved commit SHA。
2. 从批准 SHA 对应的规格读取实现要求。
3. 创建 `specwire/<change-id>` 分支，禁止直接推送 `main`。
4. 按 `tasks.md` 实现并运行项目测试。
5. 创建 GitLab MR，目标为 `main`。
6. 在 MR 描述中写入：

```text
SpecWire-Change: add-user-login
SpecWire-Approved-Commit: <sha>
```

### 6.5 复验、合并与归档

当前默认顺序：

1. 人在 MR 上复验实现与批准规格的一致性。
2. CI 和人工复验通过后合并实现 MR。
3. 从最新 `main` 运行 OpenSpec validate/archive。
4. 提交并推送 archive 结果。

建议 archive commit message：

```text
spec(add-user-login): archive completed change

SpecWire-Event: archived
SpecWire-Change: add-user-login
```

事件处理必须忽略 `SpecWire-Event: archived`，不得再次创建开发 backlog。

## 7. GitLab 与 Multica 的连接方式

目标语义是：proposal-ready commit 最终在 Multica 中产生一张 backlog 卡片。

当前不假定 GitLab push webhook 可以无适配地直接调用 Multica generic Autopilot webhook，因为两端的鉴权和事件入口可能不同。候选方式：

| 方式 | 新代码 | 特点 |
|---|---:|---|
| Multica schedule 扫描 GitLab | 无 | 最简单，但存在轮询延迟 |
| GitLab CI job 调用 Multica webhook | 少量脚本 | 需要 Runner；可使用 CI secret |
| 无状态 Webhook Adapter | 很少 | 实时；负责验签、转换和转发 |

**当前决策尚未选择实时桥接方式。** 无论选择哪种方式，都应保留 schedule 或人工重放作为漏事件补偿。

Multica 原生 GitLab VCS integration 继续负责 MR 和 Pipeline 事件关联；proposal push 到 backlog 是另一条事件路径，不应混为一个 webhook。

## 8. 已接受的限制与缓解措施

| 风险 | Workflow 阶段的处理方式 |
|---|---|
| webhook 重复 | 使用稳定键查重；重复卡片人工关闭 |
| webhook 丢失 | schedule 扫描或人工重放 |
| Agent 开发期间 spec 被修改 | 发布新版本，人工取消旧卡；旧批准版本仍由 SHA 标识 |
| Agent 使用错误规格版本 | issue 和 MR 强制记录 approved commit SHA |
| Agent 直接推 main | GitLab 受保护分支和独立 Agent 凭据阻止 |
| archive 再次触发开发 | 按 `SpecWire-Event: archived` 和路径过滤 |
| Multica 卡片被删除 | 从 GitLab 发布记录重建 |
| Skill 未按约定操作 | 人工审核、受保护分支、CI；暂不承诺程序级强制 |

Workflow 阶段不承诺严格 exactly-once，也不承诺完全自动恢复。

## 9. 待决事项

以下事项不影响采用 Workflow 的决策，留待 PoC 前后分别验证：

1. proposal push 使用 GitLab CI bridge、无状态 Adapter，还是先用 schedule。
2. Multica `create_issue` 模式能否直接创建 backlog 且不触发执行；需要实际联调确认。
3. 人的主要通知来源采用 GitLab MR 通知还是 Multica 通知。
4. archive 直接推送受保护 `main`，还是通过一个独立 archive MR。
5. 多个 change 同时发布时是否继续坚持“一次 commit 一个 change”。

## 10. 升级为 Product 的触发条件

出现以下任一类持续问题时，重新评估 Product 方案：

- 多个 Agent 经常并行处理同一个 change。
- 开发期间频繁修改已批准规格。
- 过期 Agent 结果或错误规格版本实际进入 MR。
- 重复卡片、漏事件或人工恢复已经形成显著成本。
- 需要强制记录批准人、批准时间和批准版本。
- 扩展到多个团队、多个 GitLab project 或大量 change。
- 需要自动重建 Multica 投影并保证最终收敛。
- Skill 和约定无法充分约束 Git 操作与状态迁移。

升级时优先增加解决真实问题的最小能力，不默认一次性建设完整 Product。例如先增加 CI stale-check，再考虑 generation；先增加 schedule 对账，再考虑常驻 Reconciler。

## 11. 决策后果

### 正面后果

- 更快验证端到端闭环是否真正有价值。
- 避免重复实现 OpenSpec 已有功能。
- 自研代码和长期维护面显著减少。
- Git commit SHA 提供了足够的初始版本锚点。
- 保留未来产品化所需的 change ID、稳定键和元数据。

### 负面后果

- 状态分散在 Git、GitLab 和 Multica，暂时没有单一生命周期状态文件。
- 一些恢复和冲突处理依赖人工。
- Skill 和 Runbook 主要是行为约束，不是安全强制。
- 在高并发或强审计场景下需要重新设计。

## 12. 对既有文档的影响

- [idea.v3.md](./idea.v3.md) 保留为 Product 方案的设计研究，不是当前实施基线。
- [deprecated.IMPLEMENTATION_GUIDE.md](./deprecated.IMPLEMENTATION_GUIDE.md) 保留为 Product 方案的历史实施手册，当前暂停执行。
- 本文档是当前决策与后续 PoC 的最高优先级依据；若旧文档与本文冲突，以本文为准。

