# SpecWire v2 — 交接文档（融合版）

> 仓库：`/Users/ww/playground/specwire`（= `/Volumes/d/playground/specwire`）
> 日期：2026-08-18
> 状态：方案已评审（DSH 分析两轮 + Codex 审核一轮，事实均已对照官方文档/源码核实），待实施
> 本版依据：idea.md v1 + 评审结论 + Multica 官方文档与开源源码核实结果

## 一句话定位

**SpecWire**：spec 状态以 git 提交为唯一事实源，状态迁移落盘为仓库内机器可读文件并提交，经 webhook 事件通道唤醒执行编排器（Multica）——"规格有线，状态在 Git，卡片可重建"。

## 核心架构

```
┌─────────────────────────────────────────────────────────┐
│ 本地 spec 闭环（dsh-spec-loop，需改造）                    │
│   /spec new → approve → implement → verify → archive     │
│   每次状态迁移 = 写状态文件 + git 提交（main 分支）          │
└──────────────────────────┬──────────────────────────────┘
                           │ push（main）
                           ▼
                  git 托管（GitLab CE 自托管，事实源）
                  openspec/changes/<id>/  ← 唯一权威
                  main = spec 权威；specwire/<id>/ 分支 = 实现
                           │ push / merge webhook（分支过滤 main）
                           ▼
          Multica（自托管）Autopilot：run-only reconcile 协调器
          Runbook：检出仓库 → 读 change 状态 → 与卡片对账
          （search-then-create；置状态；approved 才派实现任务）
                           │
                           ▼
              agent 执行（specwire/<id> 分支）→ 推分支 + 建 MR
              → VCS 集成关联卡片 + CI 镜像 → 合并 → verify → archive
```

## 四条原则（v2 修订）

1. **git 是唯一事实源**：spec 文档、状态、执行痕迹全部以提交历史承载；无轮询（对 git）、无双状态机、无同步桥
2. **状态在 git，提交形成版本**：spec 状态必须**落盘为仓库内机器可读文件**且每次迁移产生一次提交；webhook 才能跟随生命周期。注意："状态即提交"的前提是"状态先落盘"——没有文件变化，提交是空提交
3. **webhook 只负责唤醒，不是状态传输**：payload 内容不可信（截断/缺省），事件只回答"去查一下仓库"，一切状态以检出后的仓库为准
4. **Multica 是可重建投影**：卡片不是第二事实源，只是 git 状态的投影——卡丢了/坏了/漂了，跑一次 reconcile 即恢复。因此无需为卡片正确性做持久承诺

## 状态模型（P0，已核实）

### 现状（dsh-spec-loop@0.1.2，本机已安装于 `~/.dsh/profiles/web/node_modules/`）

- **状态保存在会话投影（session projection）中**，README 明确："approval state is per-session, folded from that session's command log — approving in one session does not approve in another"
- `/spec approve` 只做校验并返回成功，**不写状态文件、不提交 git**（源码 `cmdApprove` 核实）；new / archive 同样不自动 commit
- change 目录只有 proposal.md / tasks.md，纯 Markdown，**无任何机器可读状态字段**
- 后果：Multica 新会话无法继承本地批准状态——"agent 执行时自行从仓库读取"的假设在本地侧不成立。**这是当前最大 P0，必须先改**

### 目标状态模型

- 每个 change 在仓库内维护机器可读状态，`/spec` 命令读写并校验合法迁移，成功后自动 commit（+ 约定 push）
- 状态迁移表：

| 迁移 | 动作 | 提交到 |
|------|------|--------|
| proposed（/spec new） | 生成 change 目录（proposal.md/tasks.md）+ 写状态文件 + commit | main |
| approved（/spec approve） | 校验通过 → 更新状态文件 + commit | main |
| implementing（agent 开工） | agent 勾 tasks.md（进度痕迹，**不触发迁移**） | 实现分支 |
| implemented（MR 合并） | 合并进 main，tasks 全勾 + 状态更新 + commit | main |
| verified（/spec verify） | 本地校验通过 → 更新状态文件 + commit | main |
| archived（/spec archive） | 目录移动至归档 + commit | main |

- **防自触发循环的硬规则**：普通任务勾选、agent 实现提交**不触发生命周期迁移**；reconcile runbook 对 git 只读（绝不 commit/push）
- 决策点 D4（见文末）：状态载体选 proposal.md frontmatter 还是独立 state.yaml；revision/transition_id 是否第一期就加

## 事件与分支语义

- **main = spec 权威**：所有状态迁移（new/approve/verify/archive 的提交）都发生在 main
- **specwire/<change-id> 分支 = 代码实现**：agent 在分支上工作，中间提交不进 main → 不触发 webhook → 无自触发循环
- **MR 交付**：分支推送到 GitLab，建 MR（标题/正文含卡编号 MUL-xxx，**不用 Closes/Fixes 关闭关键字**——否则 VCS 集成会在合并时提前把卡置 done）
- **中间状态维护**：
  - `in_progress`：agent 开工时用 Multica CLI 自报（官方一等公民机制，文档明示 "the agent is expected to move the status... through the Multica CLI"；server 不会在任务启动/完成时自动翻转状态）
  - `in_review`：由 reconcile 规则推导（检测到该 change 存在 open MR 即置 in_review），或 agent CLI 置——决策点 D2（见文末）
- **backlog 语义**：Multica 官方机制——backlog 的 issue 不触发 run。proposed 的卡放 backlog = 天然"未批准不执行"闸门

## 事件 → 动作分派表（reconcile 语义，幂等）

单一 Autopilot，**run-only 模式**（不用 create-issue 模式：每次触发必建一张新卡，且有 60s 重复窗口——同一 change 的多次 push 会重复建卡）。Webhook + 定时双触发器，同一个 runbook。

| git 事件 | 触发 | runbook 动作 | 幂等保证 |
|---------|------|-------------|---------|
| proposed push（main） | webhook | `multica issue search <change-id>` → 无则 `issue create --status backlog`（标题含 change-id 作稳定键）；有则 no-op | search-then-create；投递级 Idempotency-Key 去重 |
| approved push（main） | webhook | 置 todo + `issue assign`（若未指派）→ agent 自动开工（todo+已指派即触发） | 幂等：重复 run 置同样状态 |
| agent 开工 / 交付 | —（不经 git） | agent CLI 自报 in_progress / in_review | 状态可重复写 |
| MR 创建 / 合并 | VCS 集成 | 自动关联卡片、镜像 CI（无 Closes → 合并不置 done） | 系统事件 |
| verify push（main） | webhook | 置 done | 幂等 |
| archive push（main） | webhook | no-op（卡已 done；archive 是 git 侧整理，不触发卡更新） | 幂等 |
| 任意时刻 | 定时（如每 15 分钟） | 全量对账：仓库状态 → 卡片状态，修复漏事件/离线/人工重放造成的漂移 | 纯函数 |

**卡片生命周期**（精简为单 done）：`backlog(proposed) → todo(approved) → in_progress → in_review → done(verified)`。archive 不映射卡片动作。

## Webhook 通道可靠性（已核实事实，写入设计约束）

1. **>3 refs 的 push 不发 webhook**：GitLab `push_event_hooks_limit` 默认 3，超限**整个 push 事件静默丢失**（无任何通知）。缓解：GitLab webhook 按分支过滤（只发 main）；约定"一次 push 只推一个 ref"；定时 reconcile 兜底
2. **payload 最多 20 个提交**：runbook 一律**忽略 payload 内容，以检出仓库的当前状态为准**（不解析 payload 文件列表、不依赖 before..after）
3. **投递级去重现成可用**：GitLab 17.4+ 每次投递带 `Idempotency-Key`（重试保持一致），Multica Autopilot generic provider 读取该头去重（源码 `extractDedupeKey` 核实）
4. **业务级幂等靠对账语义**：稳定键 = change_id（卡片标题），动作 = 幂等状态写入；重复投递/重放结果相同
5. **超时可能重复投递**（GitLab 官方文档提示）；**4 次连续失败临时禁用、40 次永久禁用** webhook → Multica 侧必须快速 200（其契约就是同步 admit + 异步 worker，天然匹配）
6. **payload ≤ 256 KiB**（Multica 侧限制）；小团队仓库基本不触及，知道边界即可
7. **Webhook URL 即凭据**：泄露可一键轮换；GitLab 侧建议配 Secret token（X-Gitlab-Token），Multica 校验不符返回 401

## 组件清单与选型（v2 更新）

| 组件 | 选型 | 状态 |
|------|------|------|
| 本地 spec 闭环 | dsh-spec-loop@0.1.2（`/spec` 命令族） | 已安装，**需改造：状态落盘 + 自动提交**（决策点 D5：fork 自维护 vs 上游 PR） |
| git 事实源仓库 | **GitLab CE 自托管（推荐）**；GitHub 私有仓库（备选） | 推荐理由：自托管 Multica 的 VCS 集成原生支持 GitLab |
| 事件通道 | GitLab push/MR webhook → Multica Autopilot webhook（push）+ VCS 集成（MR） | 两端能力已核实 |
| 执行编排 | **Multica 自托管**（Docker Compose，官方支持） | 自托管是 VCS 集成的前提 |
| VCS 集成 | Multica 自托管 GitLab 集成：MR 关联卡片、CI 镜像、合并转 Done | **已上线**（v1 中"PR 未合入"信息过时，删除该风险）；需 `MULTICA_VCS_INTEGRATION_ENABLED=true` + `MULTICA_VCS_SECRET_KEY` |
| agent 建 MR | daemon 主机自身 Git 凭据（SSH deploy key / credential helper token）+ `glab mr create` | 官方明确支持，零 Multica 配置 |
| CI Runner | 第二阶段：自动合并前的**必选**门禁（非可选增强） | openspec CLI 本机未装，需安装或封装 dsh-spec-loop 的 validateChange |

## 实施清单（6 阶段，状态模型先行）

1. **阶段 1 状态模型（P0）**：改造/fork dsh-spec-loop——new/approve/verify/archive 读写状态载体（D4 定案）+ 合法迁移校验 + 命令成功后自动 commit
2. **阶段 2 仓库就位**：specwire `git init` + openspec 根结构 + 首次 push（当前目录**尚不是 git 仓库**，文档中"✅ 足够"目前是设计判断，非联调结论）
3. **阶段 3 Multica 就位**：自托管部署；创建 run-only reconcile Autopilot（webhook + schedule 双触发器）+ Runbook（对账规则 + 只读 git + approved 才派活 + agent CLI 状态约定）
4. **阶段 4 GitLab CE + VCS 集成**：部署 GitLab CE、注册 push webhook（分支过滤 main、配 secret）、连接 VCS 集成（MR 事件）、agent 用 daemon 主机凭据建 MR
5. **阶段 5 PoC 验收**（见下）
6. **阶段 6 CI 门禁**：自动合并前必装（Runner + openspec validate 或封装校验器）

## PoC 验收标准

1. 同一 webhook 重放三次 → 仍只有一张卡（投递级 Idempotency-Key + search-then-create）
2. 跨会话能识别 approved（新会话 /spec list 与 Multica agent 会话读到同一状态）
3. 运行时离线后恢复：离线期间的 push，上线后定时 reconcile 补齐卡片
4. agent 自己的 push 不形成循环（分支 push 被过滤 + runbook 只读 git + 任务勾选不触发迁移）
5. 触发 >3 refs 的 push 后，定时 reconcile 能恢复丢失的事件
6. MR 合并后卡片停在 in_review（验证无 Closes 关键字配置生效），verify push 后才 done

## 已知风险与边界

- **状态落盘缺失（当前 P0）**：不完成阶段 1，整个方案不成立；"状态即提交"的前提是"状态先落盘"
- **>3 refs push 静默丢事件**：无通知、无重试，靠约定 + 定时 reconcile
- **并发冲突**：不同 change 独立目录天然不冲突；同 change 多 agent 用分支隔离；同 capability 并发归档可能冲突——archive 是本地人工步骤，冲突现场在人的终端，git 直接给合并提示，人工解决即可（低危，非"特性"）
- **运行时离线（run-only 模式）**：任务跳过不排队 → 定时 reconcile 兜底；这也是不用 create-issue 模式的代价（create-issue 会排队但会重复建卡，两害相权）
- **合并转 Done 误触发**：MR 中禁用 Closes/Fixes/Resolves MUL-xxx，否则 VCS 集成提前置 done，跳过 verify 语义
- **webhook 自动禁用**：4 次连续失败临时禁用（1 分钟起，最长 24h）、40 次永久禁用——Multica receiver 必须快速 200
- **Multica 卡片只是投影**：漂移、丢失均可由 reconcile 重建，不作为可靠性依赖

## 待确认决策点（讨论后定案，再更新本版）

- **D2（in_review 触发源）**：推荐 reconcile 规则推导（检测 open MR）优于 agent CLI 自觉，也比依赖 VCS 集成未承诺的行为可靠；in_progress 保持 agent CLI
- **D3（proposed 建卡时机）**：推荐 proposed push 即建卡置 backlog（官方"backlog 不触发 run"是天然闸门，看板审计完整；代价仅一次廉价空 reconcile run）；备选：approved 才建卡（看板空白、丢审计）
- **D4（状态载体）**：推荐给 proposal.md 加 YAML frontmatter（`status:` 字段）而非新增平行 state.yaml——少一个文件少一份双写一致性负担；revision/transition_id（漏事件检测/防重放）建议第二阶段再加，第一阶段只落 phase
- **D5（dsh-spec-loop 维护方式）**：fork 自维护 vs 给上游提 PR（涉及 /spec 命令族改造量与上游接受度）
