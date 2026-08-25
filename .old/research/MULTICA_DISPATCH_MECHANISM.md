# Multica Issue 调度与 Squad 协作机制

> 日期：2026-08-18  
> 状态：研究结论，作为 SpecWire Workflow PoC 的实现约束  
> 本机 Multica 源码：`f320af4a5b60`  
> 本机 Multica CLI：`0.4.29`（commit `c670e0549`）  
> 适用范围：当前自托管版本与本机源码；Multica 后续升级时需要重新核对

## 1. 结论摘要

Multica 的调度模型不是“Workspace 中有 Agent 就自动抢单”，而是：

> **服务端显式路由 + Agent 自主执行。**

服务端根据 Issue 的 assignee、status 和 Runtime 状态决定是否创建 Agent Run；Agent 收到 Run 后，自主理解任务并决定如何完成。

核心结论：

1. Workspace 中存在 Agent，不代表未分配的 Issue 会自动有人处理。
2. Issue 必须明确分配给某个 Agent 或 Squad，才存在自动执行路径。
3. 分配给 Agent 时，任务直接进入该 Agent 的队列，不经过全局 Leader。
4. 分配给 Squad 时，只先唤醒 Squad Leader；Leader 再自主选择一个或多个成员。
5. Backlog 是创建、分配和状态晋升路径上的停车区，但不是所有交互的绝对隔离区。
6. 评论、`@mention` 等属于独立触发路径，在 Backlog 等状态下也可能唤醒 Agent。
7. Autopilot 是预配置规则，不是一个动态挑选空闲 Agent 的智能调度中心。

## 2. 核心对象及职责

| 对象 | 作用 |
|---|---|
| Workspace | Agent、Squad、Issue、Project 和权限的组织边界 |
| Issue | 工作载体，保存描述、状态、assignee、评论和执行记录 |
| Agent | 一个可被明确调度的智能体身份 |
| Runtime | Agent 实际执行所依赖的本地或远程运行环境，例如 Codex、Claude |
| Squad | 一组 Agent/人员以及一个固定 Leader 的协作单元 |
| Squad Leader | Squad 的协调 Agent，负责理解、拆解、委派和汇总，而非默认亲自实现 |
| Autopilot | 由手动、定时、Webhook 或 API 触发的预配置规则 |
| Run / Task | 一次具体的 Agent 执行实例 |

Multica 没有一个默认的 Workspace 全局 Leader。Mika、某个普通 Agent 或 Squad Leader 都不会自动扫描 Workspace 中的所有未分配 Issue。

## 3. Issue 的基本调度矩阵

| Assignee | Issue 状态/动作 | 结果 |
|---|---|---|
| 未分配 | 任意 | 不启动 Agent |
| 人类 Member | 任意 | 不自动启动 Agent |
| 指定 Agent | 创建为 Backlog | 只创建 Issue，不执行 |
| 指定 Agent | 创建为非 Backlog | 为该 Agent 创建 Run |
| 指定 Agent | 在 Backlog 中预分配 | 不执行 |
| 指定 Agent | 从 Backlog 移出 | 为该 Agent 创建 Run |
| 指定 Squad | 创建为 Backlog | 只创建 Issue，不唤醒 Leader |
| 指定 Squad | 创建为非 Backlog | 唤醒该 Squad 的固定 Leader |
| 指定 Squad | 从 Backlog 移出 | 唤醒该 Squad 的固定 Leader |

### 3.1 创建和改派

当前实现中，创建 Issue 或修改 assignee 时，Backlog 是主要停车状态：

- Backlog：不创建执行 Run。
- 非 Backlog：原则上可以触发分配对象。
- 未指定 assignee：不会因为 Workspace 中存在 Agent 而自动选择一个。
- assignee 是 Member：不会转化成 Agent 工作。

常规工作流应使用 Todo 表示准备执行。不要依赖 In Review、Blocked、Done 等状态来阻止“创建或改派”产生 Run；当前实现的特殊停车判断主要针对 Backlog。

### 3.2 从 Backlog 晋升

已经绑定 Agent 或 Squad 的 Issue 从 Backlog 移出时：

- 分配给 Agent：触发该 Agent。
- 分配给 Squad：触发 Squad Leader。
- 直接从 Backlog 改为 Done 或 Cancelled：不应启动实现 Run。

Todo → In Progress 等普通状态变化，不代表重新创建一轮执行；新 Run 主要由创建、改派、Backlog 晋升、评论或 mention 等事件触发。

### 3.3 评论和 @mention 是独立触发路径

Backlog 只阻止 Issue 创建、分配和晋升路径的自动开工，不阻止所有评论路由：

- 显式 `@mention` 某个 Agent，可以为该 Agent 创建任务。
- Agent 已经是 Issue assignee 时，普通评论可能通过 assignee fallback 再次触发它。
- 评论触发适用于包括 Backlog、In Review、Done 在内的多个状态。
- Squad 场景中，成员回复和大部分后续评论会重新唤醒 Leader。

因此，Backlog 更准确的含义是：

> “不要因为创建、预分配或状态晋升而立即开工”，而不是“任何操作都绝不触发 Agent”。

如果需要严格人工批准闸门，Backlog 阶段不应预分配 Agent/Squad，也应避免在批准前 `@mention` Agent。

### 3.4 重复任务抑制

Multica 会对同一个 Issue 与 Agent 的 pending task 做去重，避免同一触发被重复入队。但这不是通用业务幂等机制：

- 在父 Issue mention Agent，同时又创建 Todo 子 Issue 分配给同一 Agent，会形成两次不同任务。
- Webhook 重复创建两张不同 Issue，不会被 Agent task 去重自动解决。
- SpecWire 仍需使用稳定键对 GitLab 事件和 Multica Issue 做业务查重。

## 4. 直接分配给 Agent

直接 Agent 路径如下：

```text
创建或更新 Issue
  → 明确 assignee = Agent
  → 调度条件成立
  → 服务端写入 Agent task queue
  → 对应 Runtime/daemon 领取任务
  → Agent 自主读取上下文并执行
  → Agent 评论、提交代码或更新 Issue 状态
```

这条路径没有 Leader：

- 服务端确定“交给哪个 Agent”。
- Agent 自主确定“如何完成”。
- 不会自动咨询 Mika。
- 不会从 Workspace 的多个 Agent 中选择最空闲者。

适合：

- 边界清晰、单一能力即可完成的任务。
- 已经明确知道负责 Agent 的任务。
- 不需要跨前端、后端、测试等角色协调的任务。

## 5. 分配给 Squad

### 5.1 Squad 不会一次启动所有成员

将 Issue 分配给 Squad 后，Multica 首先只唤醒 Squad 的固定 Leader：

```text
Parent Issue 分配给 Squad
  → 唤醒 Squad Leader
  → Leader 阅读 Issue、Roster、成员 role/skills、Squad Instructions
  → Leader 决定下一步委派对象
```

Squad 本身不会：

- 自动运行所有 Agent。
- 自动提升并发度。
- 自动按机器负载挑选空闲 Agent。
- 自动把多个 Agent 合并成一个“超级 Agent”。

成员 role 和 skills 是 Leader 的判断上下文，不是硬编码调度规则。

### 5.2 Leader 的职责

Leader 每次运行都会收到系统维护的 Squad Operating Protocol，要求它：

1. 阅读父 Issue、验收标准和最新活动。
2. 根据成员 role、skills 和 Squad Instructions 判断合适成员。
3. 使用完整的 mention 语法委派工作。
4. 记录本轮 Squad evaluation。
5. 委派完成后立即停止，不继续亲自实现。
6. 在成员反馈后重新评估下一步。
7. 父 Issue 属于本 Squad 时，首次将其设为 In Progress。
8. 确认整体目标完成后，将父 Issue 设为 In Review。
9. Done 通常留给人类复验或现有集成。

### 5.3 多 Agent 并行与串行

Leader 可以在一条委派评论中 mention 一个或多个成员：

- mention 一个 Agent：创建一个成员 Run。
- mention 多个 Agent：分别创建多个 Run，可以形成并行工作。
- 有依赖关系时：先委派前置工作，成员反馈后 Leader 被重新唤醒，再委派下一阶段。

示例：

```text
复杂 Parent Issue
       ↓
   Squad Leader
       ↓ 拆解
  ┌────┴────┐
前端 Agent  后端 Agent       并行
  └────┬────┘
       ↓ Leader 被反馈重新唤醒
    测试 Agent               串行依赖
       ↓
Leader 汇总并转 In Review
```

是否真正拆成多个 Agent 不由服务器保证。Leader 可能：

- 判断一个 Agent 足以完成全部工作。
- 同时委派多个 Agent。
- 分阶段串行委派。
- 判断 Squad 缺少合适能力并向人类升级。

拆解质量取决于 Issue 质量、Leader 模型能力、成员角色描述、skills 和 Squad Instructions。

### 5.4 mention 与子 Issue 两种委派方式

Leader 有两种主要方式：

#### 方式 A：在父 Issue 中 @mention

优点：

- 快。
- 所有 Agent 共享父 Issue 上下文。
- 适合小规模协作或短任务。

缺点：

- 每个子工作的状态、依赖和验收不够清晰。
- 多 Agent 同时修改相同代码区域时，更难追踪冲突。

#### 方式 B：创建 Todo 子 Issue 并分配 Agent

```text
SPEC-10 父任务：用户认证
├── SPEC-11 后端接口 → Backend Agent
├── SPEC-12 登录页面 → Frontend Agent
└── SPEC-13 集成测试 → QA Agent，等待前两项
```

优点：

- 每项工作有独立状态、Run、验收和活动记录。
- 更容易表达并行关系和前后依赖。
- 适合复杂开发和需要审计的 SpecWire 任务。

限制：

- Multica 不是完整的依赖图工作流引擎；Leader 仍需理解和维护顺序。
- 子 Issue 完成后，平台负责唤醒 Leader 重新评估，但不会替 Leader 自动完成所有汇总判断。

同一份工作只能选择一种方式。不能既创建 Todo 子 Issue 分配给 Agent，又在父 Issue mention 同一个 Agent，否则会启动两个并行 Run。

## 6. Leader 的重新唤醒和父 Issue 生命周期

典型 Squad 生命周期：

```text
Backlog
  → 人工批准
Todo
  → Leader 首次运行
In Progress
  → Leader 委派成员
  → 成员工作和反馈
  → Leader 多次被重新唤醒并评估
In Review
  → 人工复验
Done
```

Leader 通常会在以下事件后重新唤醒：

- 被委派成员发表评论、进展或问题。
- 成员完成工作并推动 Issue。
- 子 Issue 或阶段性工作完成。
- 有人再次 mention Leader/Squad。

Multica 使用 pending task 去重和 self-trigger 抑制来减少 Leader 自己评论导致的循环，但 Leader 的判断仍然是模型行为，不是确定性状态机。

## 7. Autopilot 的定位

Autopilot 是预配置触发器与执行目标的组合，可以由以下来源启动：

- 手动触发。
- 定时计划。
- Webhook。
- API。

Autopilot 必须预先绑定目标：

- 目标为 Agent：直接让该 Agent 执行。
- 目标为 Squad：路由到该 Squad Leader。

Autopilot 不会动态扫描 Workspace 并挑选空闲 Agent。

当前 `create_issue` 模式有一个影响 SpecWire 的重要约束：

> 当前源码会创建 Todo Issue，并立即 enqueue 对应 Agent 或 Squad Leader。

因此，它不能直接满足“GitLab 发布规格后只创建 Backlog，等待人批准”的严格语义。`run_only` 模式则直接执行，不创建作为人工闸门的 backlog Issue。

SpecWire Workflow PoC 应使用 Adapter、CI job 或受控脚本显式调用 Issue 创建接口/CLI，并指定 `status=backlog`，而不是直接依赖当前 Autopilot `create_issue`。

## 8. Runtime 与执行条件

Agent 是身份，Runtime 是实际执行能力。正常运行至少要求：

- Agent 未归档。
- Agent 已绑定 Runtime。
- Runtime 对应 CLI 可以执行。
- 调用者有权触发目标 Agent。

当前源码区分：

- Direct Agent 的 Runtime 临时离线：任务设计上可以先入队，等机器上线后领取。
- 未绑定 Runtime、Agent 已归档或 Runtime 明确不可执行：不能正常开始。
- Squad 路径在触发 Leader 时要求 Leader 可用；Leader 离线时可能不会产生可等待的队列项，需要后续重新触发。

所以，“Workspace 中有 Agent”还不够；Issue 必须路由到一个具有可用执行环境的具体 Agent，或路由到一个具有可用 Leader 的 Squad。

## 9. 本机 Smoke Test 观察

测试 Issue：`WW1-1 [smoke] Codex runtime verification`。

实际发生了两个 Run：

1. 人直接向 `SpecWire Dev` 发出请求，第一个 Run 负责创建 Issue。
2. 第一个 Run 创建了一个 Todo Issue，并把 assignee 设为 `SpecWire Dev`。
3. Issue 创建事件满足自动调度条件，服务端又为同一个 Agent 创建第二个 Run。
4. 第二个 Run 回复 `MULTICA_SMOKE_OK`，并把 Issue 更新为 In Review。

时间：

- Issue 创建：2026-08-18 14:48:50 UTC。
- 第二个 Run 开始：14:48:53 UTC。
- 第二个 Run 完成：14:50:31 UTC。
- 从 Issue 发布到 Run 完成：1 分 41 秒。
- 从最初要求 Agent 创建 Issue 到全部完成：2 分 19 秒。

这次没有 Squad Leader，也没有 Autopilot。Run 中的 `delegated_from_task_id` 表示任务来源链，不代表经过 Leader。

另外观察到前端曾暂时仍显示 Todo 和 Agent Working，而 API 中 Issue 已经是 In Review、Run 已完成；刷新页面后恢复。因此 PoC 排障时应把 API/CLI 的 Issue、Run 和 Comment 数据作为执行事实，UI 作为展示层。

## 10. 对 SpecWire Workflow 的建议

### 10.1 严格人工批准版本

推荐流程：

```text
GitLab proposal-ready 事件
  → Adapter 验签、解析和稳定键查重
  → 创建未分配的 Multica Backlog
  → 人检查规格和任务元数据
  → 人在一次受控操作中设置 assignee，并移到 Todo
  → Direct Agent 或 Squad Leader 开始工作
```

Backlog 阶段不预分配 Agent/Squad，可以避免普通评论通过 assignee fallback 提前唤醒执行者。批准动作同时决定：

- 是否开工。
- 交给具体 Agent，还是交给 Squad。

### 10.2 简化版

PoC 也可以创建“已分配 Agent/Squad 的 Backlog”，因为创建本身不会执行。但必须接受以下约束：

- 批准前不要 mention Agent。
- 在 Backlog 上的普通评论也可能触发 assignee fallback。
- 这是一条团队约定，不是严格技术隔离。

### 10.3 任务复杂度选择

| 任务特征 | 推荐 assignee |
|---|---|
| 单一、边界清楚、责任人明确 | Direct Agent |
| 涉及多个能力，创建时无法确定具体执行者 | Squad |
| 可以并行拆成多个独立交付物 | Squad + 多个子 Issue |
| 有严格前后依赖 | Squad + 分阶段子 Issue |

### 10.4 避免额外 Agent Run

GitLab 事件桥接应直接通过 API/CLI 创建 Multica Issue，而不是先要求一个 Agent“帮忙创建 Issue”。后者会额外消耗一个创建者 Run，并可能紧接着触发真正执行 Run。

## 11. 当前机制边界

Multica 已经提供：

- 显式 assignee 路由。
- Backlog 停车。
- Direct Agent 执行。
- Squad Leader 协调。
- 多 Agent mention 和子 Issue 委派。
- 成员反馈后的 Leader 重新评估。
- Run 队列、去重和活动记录。

Multica 当前不等同于：

- 自动抢单市场。
- 基于负载的动态调度器。
- 确定性的任务分解器。
- 完整的 DAG/依赖工作流引擎。
- SpecWire 的规格事实源。
- GitLab proposal-ready 事件的业务幂等层。

SpecWire 仍需负责事件过滤、稳定键、批准语义、规格版本 SHA，以及 GitLab 到 Multica 的桥接约定。

## 12. 源码与文档依据

- Issue 是否产生 Run 的统一判断：`/Users/ww/github/multica/server/internal/service/issue_trigger.go`
- Issue Run 的 Agent/Squad 分流：`/Users/ww/github/multica/server/internal/handler/issue_trigger.go`
- Agent/Runtime readiness：`/Users/ww/github/multica/server/internal/service/agent_ready.go`
- Squad Leader 协议：`/Users/ww/github/multica/server/internal/handler/squad_briefing.go`
- Squad 用户文档：`/Users/ww/github/multica/apps/docs/content/docs/squads.mdx`
- Autopilot `create_issue` 调度：`/Users/ww/github/multica/server/internal/service/autopilot.go`
