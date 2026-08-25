# SpecWire v3 — 可实施设计

> **决策更新（2026-08-18）**：本文描述的 Product 方案已暂缓，保留作为未来升级备选，不是当前实施基线。当前采用 **SpecWire Workflow**，见 [SPECWIRE_WORKFLOW_DECISION.md](./SPECWIRE_WORKFLOW_DECISION.md)。如有冲突，以该决策记录为准。
>
> 仓库：`/Users/ww/playground/specwire`（= `/Volumes/d/playground/specwire`）
> 日期：2026-08-18
> 状态：历史备选 Product 方案，当前不实施
> 取代：`idea.v2.md`
> 依据：v1、v2、两轮评审，以及 GitLab、Multica、dsh-spec-loop 当前文档与源码核实结果

## 一句话定位

**SpecWire** 把 spec 的持久生命周期写入 Git，把 Multica 卡片和运行状态定义为可校准的操作投影，并以“webhook 优先、周期调和兜底”的方式驱动实现：

> 规格有线，阶段在 Git，执行可观察，投影可校准。

## 本版已定案

1. GitLab 远端 `main` 是 spec 持久阶段的唯一权威；不是所有运行状态都必须写入 Git。
2. 每个 change 使用独立 `state.yaml`，首期即包含 `generation` 和已批准契约哈希。
3. Agent 在实现分支写入 `implemented`；MR 合并后该状态原子进入 `main`。
4. `in_progress`、`in_review`、`blocked` 是操作状态，由调和器根据 Multica run、分支、MR 和 CI 推导或保留。
5. proposed 阶段即创建 backlog 卡，但绝不派发。
6. dsh-spec-loop 首期 fork 自维护；PoC 稳定后再决定是否提交上游。
7. GitLab 到 Multica generic webhook 之间增加无状态 Webhook Adapter，负责 GitLab 验签和协议适配；它不保存业务状态，不是同步桥。
8. 分布式执行采用至少一次语义，不宣称严格 exactly-once；依靠稳定键、generation、可恢复操作和确定性分支消除重复副作用。

## 核心架构

![SpecWire v3 架构](../assets/specwire-v3-architecture.png)

> 部署命令、配置、凭据划分、联调顺序和逐阶段验收见 [deprecated.IMPLEMENTATION_GUIDE.md](./deprecated.IMPLEMENTATION_GUIDE.md)。

文本拓扑：

```text
┌──────────────────────────────────────────────────────────────────┐
│ dsh-spec-loop fork                                                │
│ /spec new / approve / revise / retry / verify / archive           │
│                         │                                         │
│                         ▼                                         │
│ Spec State 模块                                                   │
│ 校验迁移 → 写 state.yaml → 精确暂存 → commit → push main           │
└─────────────────────────┬────────────────────────────────────────┘
                          ▼
                 GitLab CE：远端 main
        openspec/changes/<id>/ = 持久阶段唯一权威
                          │
            push / MR webhook + 定时触发
                          ▼
              无状态 Webhook Adapter
      校验 GitLab 请求 → 保留幂等键 → 转成 Multica 签名
                          │
                          ▼
              Multica run-only Autopilot
                          │
                          ▼
                    Reconciler 模块
   读 main 快照 + Multica run + GitLab branch/MR/CI → 生成并应用计划
                          │
             approved generation 尚未派发
                          ▼
                 Multica issue / Agent run
                          │
                          ▼
       specwire/<change-id>-g<generation> → commit → MR
                          │
             Agent 写 implemented，CI 校验
                          ▼
                    合并进入 main
                          │
                  /spec verify
                          ▼
                         done
```

## 权威关系与术语

### 持久阶段

`proposed → approved → implemented → verified → archived` 是 change 的持久阶段，只认远端 `main` 中的 `state.yaml`。

- 本地尚未 push 的提交是待发布状态，不能驱动远端执行。
- 实现分支上的 `implemented` 是候选状态，合并前不具备权威性。
- webhook payload 只负责提示“需要重新读取”，不传递权威阶段。

### 操作状态

`backlog → todo → in_progress → in_review → done` 以及 `blocked/cancelled` 是 Multica 中的操作状态。

- 它们服务于派发和可视化，不与 Git 竞争持久阶段权威。
- `in_progress` 需要 Multica run 或实现分支证据。
- `in_review` 需要开放 MR，或 `main` 已进入 `implemented`。
- 卡片被人工修改后，调和器可以按规则纠正。
- `blocked/cancelled` 在同一 generation 内保留，不自动重新派发；恢复执行必须显式 `/spec retry`。

因此本方案承诺的是**无双权威**，不是“系统中只有一套状态”。

## 仓库布局

```text
.specwire/
  config.yaml
openspec/
  changes/
    <change-id>/
      proposal.md
      tasks.md
      state.yaml
      specs/                 # 可选：spec delta
      verify.md              # verify 后可生成
    archive/
      <archive-name>/
        proposal.md
        tasks.md
        state.yaml
        ...
```

`.specwire/config.yaml` 至少包含：

```yaml
schema_version: 1
repository_id: "生成一次后不变的 UUID"
main_branch: main
remote: origin
multica_project: "目标 Multica project"
default_assignee: "目标 agent 或 assignee"
push_mode: auto
```

- `repository_id` 参与跨仓库稳定键，仓库改名不改变它。
- 凭据、token、webhook secret 不得写入此文件。
- fork 若代表独立工作流，必须生成新的 `repository_id`。

## change 状态文件

`openspec/changes/<change-id>/state.yaml`：

```yaml
schema_version: 1
change_id: add-example
phase: approved
generation: 3
approved_contract_sha256: "sha256:..."
```

字段约束：

- `change_id` 必须与目录名一致，并通过安全字符白名单校验。
- `phase` 只能取规定枚举值。
- `generation` 是已批准执行代次，单调递增，禁止回退。
- proposed 尚未批准时，`approved_contract_sha256` 为 `null`。
- approved、implemented、verified 必须保存同一 generation 对应的契约哈希。
- 首期不需要 `transition_id`；Git commit 已提供持久迁移标识。

### 契约哈希

批准时对以下文件按相对路径排序、统一换行后计算 SHA-256：

- `proposal.md`
- `tasks.md`，但只把 Markdown 列表项开头的 `- [x]`、`- [X]` 规范化为 `- [ ]`
- `specs/` 下的所有 spec delta

`state.yaml`、`verify.md` 和生成物不进入契约哈希。哈希输入必须包含明确的相对路径、长度和内容分隔，禁止直接拼接文件正文。这样 Agent 可以勾选任务，但不能悄悄修改任务文本或批准内容。

## 持久阶段迁移

| 事件 | 前置阶段 | 结果阶段 | generation / hash | 权威生效时刻 |
|---|---|---|---|---|
| `/spec new` 完成生成 | 不存在 | proposed | generation = 0，hash = null | finalize 提交并 push main 后 |
| `/spec approve` | proposed | approved | generation + 1，计算 hash | push main 后 |
| `/spec revise` | approved 或 implemented | proposed | generation 保留，清空 hash | push main 后 |
| `/spec retry` | approved | approved | generation + 1，hash 不变 | push main 后 |
| Agent finalize implementation | 分支基于 approved(N) | implemented(N) | generation、hash 不变 | 仅是候选 |
| MR merge | main 为 approved(N) | implemented(N) | 从 MR 原子进入 main | merge 后 |
| `/spec verify` 成功 | implemented | verified | generation、hash 不变 | push main 后 |
| `/spec archive` | verified | archived 并移动目录 | generation、hash 不变 | push main 后 |

补充规则：

- proposed 内部编辑不增加 generation，因为尚未派发。
- verify 失败不得迁移阶段；可以更新验证报告，然后选择 revise。
- Agent 失败但契约不变时，必须由人执行 `/spec retry` 创建新 generation；定时调和不得自动复活同一 generation。
- 首期不定义持久 `cancelled` 阶段。Multica cancelled 只暂停当前 generation；永久废弃 change 需要后续增加 Git 侧迁移，不能只改卡片。

## dsh-spec-loop 改造

当前 dsh-spec-loop@0.1.2 的批准状态是 session projection，`/spec approve` 不写状态文件；`/spec new` 又通过 `agent.steer` 异步启动生成并立即返回。因此不能把“命令函数返回成功”直接等价为“文件已经可以提交”。

首期 fork 新增一个深的 **Spec State 模块**，dsh 命令只通过其小接口操作状态：

```text
specwire inspect [<change-id>|--all] --json
specwire finalize-new <change-id> [--push]
specwire transition <change-id> approve|revise|retry|verify|archive [--push]
specwire finalize-implementation <change-id> --generation N
```

行为约束：

1. `/spec new` 仍负责启动生成；生成 Agent 在所有文件写完后调用 `finalize-new`。如果会话中断，用户可重跑 finalize，而不会重复生成。
2. approve、revise、retry、verify、archive 统一调用 `transition`，不能各自实现 Git 和迁移逻辑。
3. 所有读取和校验都可通过 `inspect --json` 测试；Multica runbook 也只使用这一读取接口。
4. 模块必须返回结构化结果：`NO_CHANGE`、`LOCAL_COMMITTED`、`PUSHED`、`REMOTE_PENDING` 或明确错误。

### Git 提交约束

- new、approve、revise、retry、verify、archive 只能在配置的 main 分支执行。
- 执行前 fetch，并要求本地 main 与 `origin/main` 一致；落后或分叉时停止。
- 只暂存当前 change 的白名单路径；禁止 `git add -A`，不得卷入用户已有修改。
- 目标路径存在预先未提交且来源不明的修改时停止；其他路径的脏文件可以保留。
- commit message 使用固定格式，例如 `spec(add-example): approved [g3]`。
- auto push 只允许 `HEAD:refs/heads/main` 单 ref、非 force push。
- push 失败时保留本地 commit，返回 `REMOTE_PENDING`；重试只 push 同一提交，不再产生一次迁移。
- archive 必须在一次提交中完成状态更新和目录移动。

## 分支、MR 与 CI

### 身份与权限

- main 为受保护分支。
- 人的 dsh 身份可发布生命周期提交到 main。
- Agent 身份只能推送 `specwire/*` 并创建 MR，禁止直接推 main。
- Multica VCS token、Agent Git 凭据、`glab` API token 分离，遵循最小权限。

### Agent 交付协议

1. 任务载荷必须包含 canonical key、change_id、generation、契约哈希和目标 main commit。
2. Agent fetch main 并验证 phase、generation、hash 与任务载荷完全一致。
3. 使用确定性分支 `specwire/<change-id>-g<generation>`；分支已存在时恢复，不另建随机分支。
4. Agent 只在该分支实现、测试和勾选 tasks。
5. 完成后调用 `finalize-implementation`，它验证 tasks 全勾、契约哈希未变，再把分支内状态改为 implemented。
6. 推送分支并用 `glab mr create` 建 MR；标题或正文包含稳定键、generation 和 `MUL-xxx`，禁止使用 Closes、Fixes、Resolves 等自动关闭关键字。
7. Agent 在重要步骤前和 finalize 前重新读取远端 main；若 phase 已不是 approved，或 generation/hash 已变化，必须停止并报告 `STALE_GENERATION`，不得 rebase 后强行交付。

### 合并门禁

CI 至少校验：

- MR source branch 符合确定性命名。
- base main 为 approved(N)，head 为 implemented(N)。
- base 与 head 的 generation、批准契约哈希一致。
- head 重新计算的契约哈希通过。
- tasks 全部勾选。
- MR 没有修改其他 change 的 `state.yaml`。
- 项目测试和必要静态检查通过。

PoC 阶段使用人工合并；CI 完成前不得启用自动合并。

## Reconciler 模块

Reconciler 是唯一允许把外部观察转换为 Multica 写操作的模块。webhook、定时任务和人工重放都只能调用同一个接口，不能各自维护规则。

### 输入

- 从远端 main 检出的 active changes 和 archive changes。
- GitLab 中确定性分支、开放/已合并 MR、必要 CI 状态。
- Multica 中目标 project 的 issue、metadata 和活动 run。
- `.specwire/config.yaml`。

### 输出

先生成可审计计划，再执行：

```text
ensure_issue
set_metadata
set_status
set_assignee
add_or_repair_link
mark_stale_execution
dispatch
no_op
```

提供 `--dry-run`；日志记录输入 main commit、计划、执行结果和错误，但不得提交或 push Git。

### 卡片稳定键

```text
specwire:<repository_id>:<change_id>
```

- 标题必须严格等于 `[SpecWire:<repository-id>:<change-id>]`，创建后不改名；人类可读摘要放在 description。这样 Multica 的 active-title duplicate guard 才能参与并发创建保护。
- metadata 至少保存 `specwire.key`、`specwire.generation`、`specwire.contract_sha256`。
- 搜索必须限定 Multica project、包含 closed issues，并对完整标题或 metadata 做精确匹配；不能直接采用模糊搜索的第一个结果。
- 无卡时先以 backlog 创建。并发创建发生 active duplicate 冲突时，重新搜索并接管现有卡，视为成功。
- 卡片进入 done 后仍复用原卡，不为新事件创建同名卡。

### 状态优先级

每次运行都从当前快照收敛，不根据“这次是哪一种 webhook”选择业务动作：

任何 branch、MR 或 run 只要携带的 generation 与 Git 当前 generation 不同，就先标记为 stale，且不得参与下面的状态判定。能安全停止的旧 run 可以停止；旧 MR 默认不自动关闭，但必须添加 stale 标记并阻止合并。

| 优先级 | 当前观察 | 目标操作状态 | 是否派发 |
|---|---|---|---|
| 1 | phase = verified 或 archived | done | 否 |
| 2 | phase = proposed | backlog；旧 run/MR 标记 stale | 否 |
| 3 | phase = implemented | in_review | 否 |
| 4 | approved 且同 generation 存在开放 MR、必需 CI 失败 | blocked | 否 |
| 5 | approved 且同 generation 存在开放 MR | in_review | 否 |
| 6 | approved 且卡为 blocked/cancelled、generation 未变化 | 保持原状 | 否 |
| 7 | approved 且同 generation 存在活动 run 或确定性分支 | in_progress | 否 |
| 8 | phase = approved | todo | 仅新 generation |

这套优先级保证：

- main 仍为 approved、但 MR 已打开时，定时调和不会把 in_review 回退为 todo。
- revise 回到 proposed 后，旧 run、分支或 MR 不会继续主导卡片状态；能自动停止时停止，否则标记 stale 并依靠 Agent/CI 的 generation 校验拒绝交付。

### 可恢复派发

当 Git generation 大于卡片 metadata generation 时：

1. 用 `--no-start` 把卡归一到 backlog。
2. 写入新 generation 和契约哈希 metadata。
3. 用 `--no-start` 设置 assignee。
4. 最后执行 backlog → todo，作为唯一启动边沿。

任一步失败，下次调和从现状继续。generation 相同且卡片已经 todo/assigned 时，不反复切换状态或重新指派。

同 generation 的状态纠偏一律使用 `--no-start`；只有 metadata generation 变大时才允许执行启动边沿。因此把误置 done 的 approved 卡纠正回 todo，不会暗中重跑旧任务，后续必须显式 retry。

系统只承诺至少一次派发：极窄的跨系统故障窗口仍可能重复启动。重复 Agent 必须因确定性分支和 generation 校验而恢复同一工作或安全退出。

### 可重建范围

- phase、目标卡片状态、稳定键和当前 MR/分支关联可以重建。
- 被删除卡片原有的 issue number、评论和历史 run 不能从 Git 完整恢复。
- 若卡片被删除，重建后可能得到新的 `MUL-xxx`；调和器应按确定性分支查找 MR 并修复链接。无法自动修改时必须报告，不得声称历史完全恢复。

## Webhook 与可靠性

### 触发方式

- main push：立即唤醒调和。
- MR open/update/merge：GitLab VCS 集成负责关联；Webhook Adapter 同时唤醒调和，以缩短 in_review 更新延迟。
- schedule：例如每 15 分钟全量调和 active 与 archive。
- 人工重放：只再次调用同一 Reconciler。

因此正确表述是 **webhook-first + periodic reconciliation**；schedule 本质上会周期读取 Git。

### 安全适配

当前 Multica generic webhook 不直接校验 GitLab 的 `X-Gitlab-Token`，而 native VCS webhook 是另一入口。生产路径固定为：

```text
GitLab
  └─ X-Gitlab-Token 或 GitLab signing token
       → Webhook Adapter：校验来源、project allowlist、payload 大小
          └─ X-Hub-Signature-256 + 原 Idempotency-Key
               → Multica generic Autopilot webhook
```

- Adapter 无数据库、无业务状态，只完成认证、限流、协议转换和转发。
- Multica webhook URL 只暴露在内部网络，不直接配置到不受信网络。
- 验签失败返回 401/403；payload 超限返回 413；合法请求快速转交 Multica。
- PoC 若暂时直连 bearer URL，必须限制在私网并标注为临时风险，不能声称已校验 `X-Gitlab-Token`。

### 已知平台边界

1. GitLab 默认一次 push 超过 `push_event_hooks_limit` 个 refs 时可能完全不产生 push webhook。分支过滤只能减少已产生事件的投递，不能恢复未生成事件。约定一次只 push 一个 ref，并由 schedule 兜底。
2. push payload 的详细 commits 数量有限；Reconciler 不解析提交列表和 `before..after`，只读取远端 main 当前快照。
3. GitLab 17.4+ 的 `Idempotency-Key` 在重试间保持稳定；Adapter 原样转发。缺少该头时，仍由业务调和幂等兜底。
4. GitLab webhook 连续失败会被临时乃至永久禁用；必须监控禁用状态，接收端快速响应。
5. Multica generic webhook 的 payload 上限纳入 Adapter 检查。

## 模块、外部系统与凭据

| 模块或工具 | 选择 | v3 要求 |
|---|---|---|
| dsh-spec-loop | fork 0.1.2 | 接入 Spec State 模块，不再依赖 session projection 作为权威 |
| Spec State 模块 | 新建本地 CLI/包 | 集中迁移、hash、Git commit/push；接口同时供 dsh、Agent、测试使用 |
| Git 托管 | GitLab CE 自托管 | main 保护、webhook、MR、CI |
| Webhook Adapter | 新建无状态适配器 | GitLab 验签 → Multica generic 签名；project allowlist |
| Reconciler 模块 | 单一 runbook/CLI | 计划与执行分离，支持 dry-run；Git 只读 |
| 执行编排 | Multica 自托管 | run-only Autopilot + schedule；issue metadata；Agent CLI |
| VCS 集成 | Multica GitLab integration | MR/CI 关联；不负责持久 phase |
| Reconciler GitLab 读取 | 独立只读凭据 | 读取 main、分支、MR、CI；不能 push 或合并 |
| Agent Git | SSH key 或 write_repository 凭据 | 只能推 specwire/* |
| Agent MR | `glab` + 独立 API token | 当前机器需安装并配置 host/auth |
| CI | GitLab Runner | 合并前验证 generation、hash、状态迁移、tasks 和项目测试 |

任何 token 都不得复用到权限更大的角色，也不得写入仓库或 runbook 文本。

## 实施路线

完整实施手册见 [deprecated.IMPLEMENTATION_GUIDE.md](./deprecated.IMPLEMENTATION_GUIDE.md)。总体阶段保持为：

| 阶段 | 主要产物 | 阶段门 |
|---|---|---|
| 0. 拓扑与版本冻结 | 主机、域名、端口、版本、资源和凭据清单 | 所有参与方可互访 |
| 1. GitLab CE | 远端 project、受保护 main、三类最小权限凭据 | clone/push/MR 均通过 |
| 2. Multica | 自托管服务、daemon、实现 Agent、Reconciler Agent | 手工 issue 能驱动 Codex |
| 3. VCS 联调 | GitLab MR/Pipeline → Multica 原生 VCS webhook | MR 与 CI 能关联卡片 |
| 4. 仓库初始化 | config、目录骨架、远端 main、repository_id | 首次 push 且无 secret |
| 5. Spec State | schema、CLI、Git 原子提交、dsh fork | 状态迁移测试全部通过 |
| 6. Reconciler | dry-run/apply、稳定键、状态优先级、恢复派发 | 重复运行结果收敛 |
| 7. Autopilot | run-only、手动触发和 schedule | 周期执行可观察 |
| 8. Webhook Adapter | main push webhook、安全适配 | 漏事件可补、非法请求被拒 |
| 9. CI | generation、hash、phase、tasks 和项目测试门禁 | stale MR 无法合并 |
| 10. PoC | 一个完整 change 闭环和故障注入 | 下述 PoC 验收全部通过 |
| 11. 上线加固 | 备份、监控、升级、回滚和凭据轮换 | 才可评估自动合并 |

## PoC 验收标准

1. `/spec new` 异步生成结束前不提交；finalize 后只产生一次 proposed 提交。
2. 新会话、dsh、Reconciler 和 Agent 读取同一个 state.yaml，均能识别 approved。
3. 仓库存在无关 staged/unstaged 修改时，生命周期提交不包含它们。
4. approved(N) 运行中执行 revise/approve 得到 N+1 后，旧 Agent 和旧 MR 均被拒绝。
5. 同一 webhook 重放三次、并发触发三次，仍只有一张精确匹配的卡；done 卡也不会被重复创建。
6. main 仍为 approved、开放 MR 已存在时，周期调和保持 in_review，不回退 todo。
7. blocked/cancelled 的同一 generation 不自动重启；`/spec retry` 后新 generation 只启动一次。
8. 非法 GitLab token、错误 Multica 内部签名和超限 payload 均被拒绝。
9. Multica 离线期间漏掉 webhook 后，上线后的 schedule 能从远端 main 补齐。
10. 多 ref push 没有 webhook 时，schedule 仍能补齐状态。
11. Agent 分支 push 不形成自触发循环；Reconciler 全程不 commit/push。
12. Agent 的 MR 合并后，main 原子进入 implemented；卡保持 in_review，只有 verify push 后才 done。
13. 删除卡片后能重建语义投影，并明确报告新 issue number 和无法恢复的历史。
14. push 失败会得到 REMOTE_PENDING；重试不会生成第二个 phase commit。

## 明确不承诺

- 不承诺跨 GitLab 与 Multica 的严格 exactly-once。
- 不把 Multica issue number、评论或 run 历史视为可由 Git 完整恢复的数据。
- 不把 webhook payload 当作状态增量日志。
- 不允许 Agent 直接修改 main 或跳过 generation/hash 校验。
- 不在 CI 完成前启用自动合并。
- 不在首期仅靠 Multica cancelled 表达永久废弃 change。

## 当前事实与参考

- 当前目录尚不是 Git 仓库，阶段 2 前不得声称已完成联调。
- dsh-spec-loop@0.1.2 当前状态来自 session projection，`/spec new` 为异步生成；首期 fork 是硬前置。
- GitLab webhook 行为与限制：[Webhook events](https://docs.gitlab.com/user/project/integrations/webhook_events/)、[Webhooks](https://docs.gitlab.com/user/project/integrations/webhooks/)
- Multica Autopilot：[官方文档](https://www.multica.ai/docs/zh/autopilots)
- Multica GitLab VCS 集成：[官方文档](https://www.multica.ai/docs/zh/vcs-integration)
- Multica generic webhook 当前验签与去重实现：[源码](https://github.com/multica-ai/multica/blob/main/server/internal/handler/autopilot_webhook.go)

## 最终实施判断

这条路线可行。真正需要实现和验证的核心不是 webhook 转发本身，而是三个深模块：

1. **Spec State**：独占持久迁移、契约 hash 和 Git 发布。
2. **Reconciler**：独占外部观察到操作计划的转换。
3. **Webhook Adapter**：独占 GitLab 到 Multica 的认证与协议适配。

三者接口保持小而明确，dsh、GitLab、Multica 和 Agent 都不再各自复制状态规则。完成阶段 0～10 的验收后，方案才从“设计可行”进入“PoC 已证实”；完成阶段 11 后才具备生产运行条件。
