# SpecWire — 交接文档

> 仓库：`/Users/ww/playground/specwire`
> 日期：2026-08-18
> 状态：初始落地中，方案已定，待实施

## 一句话定位

**SpecWire**：spec 状态以 git 提交为唯一事实源，经 webhook 事件通道接到执行编排器（Multica）——"规格有线，状态即提交"。

## 核心架构

```
┌─────────────────────────────────────────────────────────┐
│ 本地 spec 闭环（dsh-spec-loop / OpenSpec）                │
│   /spec new → approve → implement → verify → archive     │
│   每次状态迁移 = 一次 git 提交                              │
└──────────────────────────┬──────────────────────────────┘
                           │ push
                           ▼
                  git 托管（事实源仓库）
                  openspec/changes/<id>/  ← 唯一权威
                           │ push / merge webhook
                           ▼
                  Multica 自动化（Webhook 触发器）
                  Runbook：检出仓库 → 读新增 change → 执行
                           │
                           ▼
              agent 执行 → 产物回写 git（勾 tasks + commit + MR）
```

**三条原则**
1. **git 是唯一事实源**：spec 文档、状态、执行痕迹全部以提交历史承载；无轮询、无双状态机、无同步桥
2. **状态即提交**：spec 每次状态迁移（含 approve / verify）都产生一次 git 提交，webhook 才能跟随生命周期
3. **webhook 是事件广播**：git → Multica 单向事件通道；Multica 侧产物（commit / MR）自然落回 git

## 完整事件流（逐段核对）

```
① /spec new → 生成 change 目录 + commit + push
        │
② GitLab push webhook → POST → Multica 自动化 Webhook URL
        │                          │
        ▼                          ▼
   Multica 建卡 + 派 agent  ←  Runbook：检出仓库，读 openspec/changes/ 新增 change
        │
③ agent 执行：读 proposal/tasks → 改代码 → 勾 tasks.md → commit + push
        │
④ MR 合并（交付）→ GitLab merge webhook → Multica（可选：更新卡/触发验收）
        │
⑤ 本地 /spec verify + archive → commit + push
        │
⑥ archive 移动目录 + push → webhook → Multica 卡 done
```

### 逐段覆盖核对

| 环节 | 覆盖方式 | 状态 |
|------|---------|------|
| ① spec 提交进 git | 本地 dsh-spec-loop + git | ✅ 已有 |
| ② push → Multica 建卡派活 | GitLab webhook → Multica 自动化 Webhook | ✅ 足够 |
| ③ agent 执行 + 状态回写 git | agent 在检出仓库里 commit（Multica 本来就在 worktree 干活） | ✅ 足够 |
| ④ 交付（MR） | GitLab MR/merge webhook → 同一个 Multica 自动化（事件过滤区分 push/merge） | ✅ 足够 |
| ⑤ verify/archive 本地动作 | 本地 /spec 命令 | ✅ 本地闭环，不需要 webhook |
| ⑥ 归档 → 卡 done | archive commit → push → webhook | ✅ 足够 |

**结论：GitLab webhook + Multica webhook 作为事件通道是足够的**——两个方向都通：git 侧任何状态变化（提交/合并）都能推给 Multica，Multica 侧的产物（agent 的 commit/MR）自然落回 git。

### 缺口 A：spec 状态迁移必须"提交化"，否则 Multica 看不到

Webhook 只认 git 事件。但 spec 的很多状态迁移（approve、verify）目前不产生 git 提交——它们只是本地命令。如果不约定"每个状态迁移都 commit"，Multica 就收不到通知：

| spec 状态迁移 | 现状 | 需要约定 |
|--------------|------|---------|
| proposed（/spec new） | commit ✅ | 已满足 |
| approved（/spec approve） | ❌ 不 commit | 约定：approve 后更新 change 状态并 commit → 触发 webhook → Multica 卡 backlog→todo |
| implementing（agent 开工） | agent commit ✅ | 已满足（agent 勾 tasks 即提交） |
| verified（/spec verify） | ❌ 不 commit | 约定：verify 通过后 commit → 卡 in_review→done |
| archived（/spec archive） | commit ✅ | 已满足（目录移动 + 提交） |

**核心原则：把"spec 状态机"翻译成"git 提交序列"**——每一次状态迁移 = 一次提交 = 一个 webhook 事件。做到这一点，Multica 就能完整跟随 spec 生命周期。

### 缺口 B：agent 执行中的"中间状态"回写

agent 开工时（in_progress）和交付时（in_review）也需要 commit 到 git（比如勾选 tasks.md、提交状态注释），否则 Multica 看板上有状态、git 里没有对应痕迹，两边会漂。这靠 **Runbook 约定**：告诉 agent"每次状态变化都要 commit + push"。

### Runner 的角色：可选，不是支撑件

Runner 在方案四里没有不可替代的作用：

| 用途 | 需要吗 |
|------|--------|
| push 后自动校验（npm test、openspec validate） | 可选增强——CI 闸门，防止坏提交进事实源 |
| 触发 Multica | ❌ 不需要——webhook 已覆盖 |
| 验收自动化 | 可选——spec verify 是本地 LLM judge，不依赖 CI |

**建议**：第一阶段不装 Runner，只用 webhook 通道跑通闭环；等稳定后如果需要"提交质量闸门"再加 CI。

## 状态映射（spec ↔ git 提交 ↔ Multica）

| spec 状态 | git 中的表达 | Multica 卡片 |
|-----------|-------------|-------------|
| proposed | `/spec new` 生成 change 目录 + commit | backlog（建卡） |
| approved | approve 后更新状态 + commit | todo |
| implementing | agent 开工勾 tasks.md + commit | in_progress |
| implemented | agent 交付（tasks 全勾 + MR 合并） | in_review |
| verified | `/spec verify` 通过 + commit | done（待归档） |
| archived | archive 移动目录 + commit | done（归档） |

**关键约定**：approve 和 verify 当前不产生提交，必须补"状态文件 + commit"（dsh-spec-loop 侧加钩子，或人工约定每次迁移提交）。

## 组件清单与选型

| 组件 | 选型 | 状态 |
|------|------|------|
| 本地 spec 闭环 | dsh-spec-loop（`/spec` 命令族） | 已有 |
| git 事实源仓库 | GitLab CE（自托管）或 GitHub 私有仓库 | 待定 |
| 事件通道 | GitLab push/merge webhook → Multica 自动化 Webhook URL | 两端均免费层支持 |
| 执行编排 | Multica（自动化 Webhook 触发器 + Runbook + agent） | 已有 |
| CI Runner | **第一阶段不用**（可选增强：提交质量闸门） | 待定 |

### 选型要点（已调研确认）
- GitLab CE（Free 层）：项目级 Webhook、Runner 均支持（官方文档标注 Tier: Free）；Docker 部署（~3GB 镜像、4GB+ 内存、arm64 支持）；轻量替代 Gitea
- Multica：自动化（Autopilot）原生支持 Webhook 触发器，识别 `X-GitHub-Event` / `X-Gitlab-Event` 请求头；支持事件过滤、Idempotency-Key 去重、"创建 issue"输出模式
- **Multica 的 GitLab issue/MR 集成 PR 未合入**——不依赖它，webhook 通道已足够

## 实施清单

1. **git 托管就位**：选定 GitLab CE（Docker）或 GitHub 私有仓库，建 openspec 根结构并推送
2. **Multica 自动化**：Webhook 触发器 + "创建 issue"输出模式 + Runbook（检出仓库、扫描 openspec/changes/ 新增 change、按 proposal/tasks 执行、状态变化即 commit + push）+ 事件过滤（push 到 main）
3. **git 托管侧 webhook**：push / merge 事件 → Multica 给的 URL
4. **"状态即提交"落地**：approve / verify 迁移补提交钩子
5. **跑通首个闭环**：`/spec new` → commit → webhook → Multica 建卡派 agent → agent 执行 → 勾 tasks + commit + MR → verify → archive
6. **（可选）Runner**：push 后自动 `npm test` / `openspec validate` 作为事实源闸门

## 已知风险与边界

- **状态迁移遗漏**：approve / verify 不提交则 Multica 收不到事件——"状态即提交"是硬约定
- **并发冲突**：不同 change 是独立目录天然不冲突；同 change 多 agent 用 worktree/branch 隔离；archive 时 specs/ 合并冲突是特性
- **webhook 只做触发**：payload 不含 proposal 内容，agent 执行时自行从仓库读取（git 是事实源）
- **Multica 运行时离线**：任务在队列等待（绑定发起时的运行时）；"仅运行"模式会跳过
- **GitLab 集成未合入**：webhook 通道不依赖它；若后续需要 GitLab issue 原生同步，等官方合入再评估
