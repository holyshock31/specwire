# SpecWire Workflow 研究与 PoC 交接

> 状态快照：2026-08-19（Asia/Shanghai），**M4 第一阶段联调完成**  
> 工作目录：`/Volumes/d/playground/specwire`  
> 当前阶段：研究、架构收敛和本机集成 PoC，不是 SpecWire 产品开发阶段。  
> 当前进展：SpecWire Bridge（Go）已实现并接入真实 GitLab webhook，验收矩阵 1–9 全绿；下一步是部署固化（launchd）。

## 0. 给新会话的工作约束

1. 先阅读本文，再阅读“文档地图”列出的当前文档。
2. 用户非常在意操作边界。除非用户明确要求“执行、修改、安装或部署”，默认只分析、检查和给步骤。
3. 不要擅自修改 Docker、Multica、GitLab、SSH、profile、仓库、Issue、MR 或本机文件。
4. 如需检查现状，优先使用只读命令；执行前说明要读什么。
5. 不要把 password、token、webhook secret、完整 bearer URL 或 SSH 私钥写入本文或普通日志。
6. 当前目录不是 Git repository；不要假定这里可以 commit。

## 1. 目标与当前产品判断

目标是验证一条以 Git 中的 OpenSpec 为事实源、以 Multica 为执行编排层的工作流：

```text
本地 Agent + OpenSpec 编写规格
  → 人审核并发布到 GitLab main
  → GitLab Push Webhook
  → 创建 Multica Backlog
  → 人批准执行（Todo + assignee）
  → Multica Agent 在独立 worktree/branch 开发
  → commit + push + GitLab MR
  → 人复验、合并、归档 OpenSpec
```

当前已经决定采用 **SpecWire Workflow**，暂不开发一个完整的 SpecWire Product。

当前含义：

- SpecWire 首先是一套 workflow、Git 约定、Skill 和很薄的集成桥。
- Git/OpenSpec 是规格事实源。
- Multica Issue/Run 是可重建的操作投影，不是规格事实源。
- 只有当重复事件、并发修订、状态漂移等问题在真实使用中持续出现，才考虑升级为完整 Product、独立 Core、Reconciler 或状态机服务。
- OpenSpec Store 目前不需要。当前是单代码仓库 PoC，把 `openspec/` 放在代码仓库中最简单；Store 与 webhook 正交，不能替代 webhook。

## 2. 当前推荐的实时桥接方式

### 2.1 结论

GitLab Project Push Webhook 不能直接执行本机 `.sh` 文件。它只会向 URL 发送 HTTP POST。

当前最适合 PoC 的方式是运行一个很小的常驻 HTTP 服务，可用脚本语言实现，暂称 **SpecWire Bridge**：

```text
GitLab Push Webhook
  → SpecWire Bridge
      - 验证 X-Gitlab-Token
      - 只接受 Push Hook
      - 校验 project allowlist
      - 过滤 refs/heads/main
      - 识别 proposal-ready 发布事件
      - 判重
      - 调用 Multica CLI
  → 创建未分配的 Multica Backlog Issue
```

它不是旧设计中的“只转发协议的无状态 Adapter”。一旦脚本负责业务稳定键判重并创建 Issue，它更准确的名字是 Bridge/Dispatcher。

**已实现（2026-08-19）**：Go 实现位于 `bridge/`，设计见 `SPECWIRE_BRIDGE_DESIGN.md`（定稿，决策 D1–D17）。关键实现点：Standard Webhooks HMAC-SHA256 验签（D15，替代 `X-Gitlab-Token`）、`.env` 配置加载（D16）、`archived` 事件自动置卡 Done（D17）。46 个单测含 `-race` 全绿。

### 2.2 为什么不直接使用 Multica Autopilot `create_issue`

已核实的当前 Multica 行为：

- Autopilot `create_issue` 创建的是 `todo`。
- 它会绑定 Agent/Squad，并立即 enqueue。
- 这不满足“先进入 Backlog，等待人批准才开工”的闸门语义。

备选的 `run_only` 控制 Agent 虽然可以调用 CLI 创建 Backlog，但会多引入一层在线 Runtime 和模型行为。对确定性的 PoC，Bridge 直接调用 CLI 更简单。

目标 CLI 语义为：

```bash
multica --profile specwire-local issue create \
  --project "$MULTICA_PROJECT_ID" \
  --status backlog \
  --title "[SpecWire] $CHANGE_ID" \
  --description-stdin \
  --output json
```

注意：

- 创建 Backlog 时不设置 `--assignee`。
- 不使用 `--allow-duplicate`。
- 实现时应通过 subprocess 参数数组调用 CLI，不要把 webhook 字段拼进 shell 字符串。
- Bridge 运行用户必须能读取 `specwire-local` profile，但不得把 profile token复制进仓库。

人工批准执行时，再完成以下语义：

```bash
multica --profile specwire-local issue update <issue-id> \
  --status todo \
  --assignee "SpecWire Dev"
```

以上是目标命令示例，不代表授权新会话立即执行。

### 2.3 两条 GitLab 事件路径不要混用

```text
规格发布 push
  → SpecWire Bridge
  → 创建 Multica Backlog

实现 MR / Pipeline
  → Multica native GitLab VCS webhook
  → MR 关联、CI 展示、合并联动
```

Multica Settings 中的 Repository 注册解决的是“Agent 到哪里 clone 和工作”；它不等于“Push 自动创建 Issue”。

## 3. 发布事件与幂等契约

### 3.1 当前发布提交约定

一次 publish 可以发布**一个或多个 change**（D19：每个 change 一个 commit，各自带 trailer，一次 push 推送）。单 change 建议 commit message：

```text
spec(add-user-login): publish proposal

SpecWire-Event: proposal-ready
SpecWire-Change: add-user-login
```

GitLab Push payload 中的 `after` 是本次 push 头 SHA；**每个 change 的批准版本 SHA 取各自 commit 的 id**（单 change 时等于 after）。

Backlog 至少记录：

```yaml
repository: <GitLab project path or project ID>
change_id: <OpenSpec change ID>
approved_commit_sha: <该 change 所在 commit SHA>
target_branch: main
```

当前文档定义的业务稳定键：

```text
<GitLab project path>:<change ID>:<approved commit SHA>
```

（实现 `stableKeyOf` 用 `path_with_namespace` 并含 multica project id（D21）；发布工具 `scripts/publish.sh` 自动写 trailer，支持多 change，可选 `--todo`/`--assignee` 指定建卡状态与分配（D23）：缺省 backlog 评审模式；`SpecWire-Status: todo` 直通开工；`SpecWire-Assignee: <name>` 预分配。稳定键不含状态/分配，幂等不受影响。）

### 3.2 两层判重

1. 投递判重：GitLab `Idempotency-Key`，用于识别同一次 webhook 的重试。
2. 业务判重：上述稳定键，避免同一个已批准版本创建多张 Multica Issue。

PoC 可选择：

- SQLite 唯一索引：确定性最好，能处理并发请求。
- 单进程串行队列 + 本地状态文件：实现更少，但故障恢复和原子性较弱。
- `multica issue search` 后再 create：最轻，但并发时存在 search-then-create 竞态。

如果选择 SQLite，建议至少记录：

```text
stable_key | delivery_key | state | multica_issue_id | created_at | last_error
```

处理返回语义：

- 非目标事件：`2xx ignored`。
- 已成功处理的重复事件：`2xx duplicate`，不再创建。
- Issue 创建成功并记录映射：`2xx`。
- Multica 临时失败：`5xx`，允许 GitLab 重试。

### 3.3 Payload 边界

GitLab Push payload 的 commit 明细可能被限制或截断。建议分阶段处理：

- 最小 PoC：依赖“一次 push 一个发布 commit”约定，从 payload 识别 trailer。
- 稳健版本：把 payload 只当唤醒信号，根据 `after` 从 GitLab API 或 Git 精确读取已发布 commit/spec。
- 后续仍应保留 schedule 或人工重放，用于弥补漏 webhook。

archive commit 不得再次创建开发 Backlog；**且会自动把对应实现卡置 Done（D17）**，例如：

```text
SpecWire-Event: archived
SpecWire-Change: add-user-login
```

匹配规则：按 `project + change_id` 取最新 `state=created` 的卡置 `done`（归档 push 的 after_sha 与建卡时必然不同，稳定键无法匹配，SHA 不参与归档匹配）。

## 4. 本机环境快照

以下状态在 2026-08-19 通过只读命令重新核实过；新会话开始工作前可再次只读核实。

### 4.1 Multica self-host

- Multica CLI：`0.4.29`，darwin/arm64。
- 本地前端：`http://127.0.0.1:23000`。
- 本地后端：`http://127.0.0.1:28080`。
- Docker services：
  - `multica-frontend-1`
  - `multica-backend-1`
  - `multica-postgres-1`
- Compose 工作目录：`/Users/ww/github/multica`。
- 当前服务由 `/Users/ww/github/multica/docker-compose.selfhost.yml` 创建。
- `docker-compose.yml` 是开发依赖入口，之前实际只启动 PostgreSQL；不要用它判断 self-host 前后端是否应该启动。

### 4.2 Multica profiles 与 Desktop 共存

当前 profile 目录：

```text
~/.multica/profiles/desktop-api.multica.ai/
~/.multica/profiles/specwire-local/
```

- `desktop-api.multica.ai` 对应现有 Multica Desktop/Cloud。
- `specwire-local` 对应 self-host。
- 两者可以共存，不需要卸载 Multica Desktop。
- 后续所有 self-host CLI 命令必须显式使用 `--profile specwire-local`，避免误操作默认或云端 profile。
- 不要读取或展示 profile 中的 token。

### 4.3 Multica workspace

- Workspace：`ww1`
- Workspace ID：`d388048a-4685-48a6-9acc-a7567097d11a`
- Project：`SpecWire PoC`
- Project ID：`3e7d61cd-900b-41a8-85f4-c97019e2020f`
- Repository：`git@gitlab-specwire-agent:specwire/specwire-poc.git`
- Agent：`SpecWire Dev`
- Agent ID：`ec71aaac-4ba4-4116-a8dc-920794509711`
- Agent 当前 `max_concurrent_tasks = 1`。
- Agent 当前绑定的是 `DeepSeek Harness`（DSH）Runtime，不是 Codex Runtime。

同一 daemon 还发现 Codex、Claude Code、Grok、Hermes、Oh-My-Pi、Pi 等在线 Runtime。这证明 Multica self-host 可以发现多种本机 Runtime，但不代表 `SpecWire Dev` 已切换到 Codex。

长期设计要求：SpecWire 不应强依赖 DSH。目标应是 Skill + CLI，可由 Codex、Claude Code 或其他兼容 Agent 使用。是否把当前 `SpecWire Dev` 改绑 Codex，需要用户明确决定，不能擅自修改。

### 4.4 GitLab CE

- Container：`specwire-gitlab`
- Image：`gitlab/gitlab-ce:19.1.2-ce.0`
- 状态：healthy。
- HTTP：`http://127.0.0.1:8929`
- SSH host port：`2424`
- GitLab external URL：`http://gitlab.specwire.test:8929`
- Compose：`/Users/ww/infra/specwire-infra/gitlab/compose.yaml`
- Group：`specwire`
- Project：`specwire/specwire-poc`
- Agent SSH alias：`gitlab-specwire-agent`

以下连接已验证：

```bash
git ls-remote git@gitlab-specwire-agent:specwire/specwire-poc.git
```

不要记录 SSH 私钥或 GitLab access token。

### 4.5 GitLab 容器访问本机 Bridge

如果 Bridge 运行在 Mac 主机，GitLab 容器中的 `127.0.0.1` 指向 GitLab 容器自己，不能作为 Bridge URL。

Docker Desktop for Mac 的候选地址：

```text
http://host.docker.internal:<bridge-port>/gitlab/specwire
```

同时需要核实：

- Bridge 不应只监听主机 `127.0.0.1`，否则容器可能无法访问。
- GitLab Admin 的 outbound request 设置是否允许访问本地网络/该 host 与端口。
- Bridge 仍需校验 webhook secret 和 project allowlist，不能因为是本机 PoC 就省略。

**已核实（M4，2026-08-19）**：webhook 已配置并工作——URL `http://host.docker.internal:8787/gitlab/specwire`，Signing token（whsec_）与 Bridge 的 `SPECWIRE_WEBHOOK_SECRET` 一致，GitLab 侧 outbound request 已放行；GitLab 投递历史显示 200。GitLab 19.1 的 Signing token（HMAC）为 GA 特性，无需 feature flag。

## 5. 已完成的验证

### 5.1 Runtime Smoke Test

Issue：`WW1-1 [smoke] Codex runtime verification`

实际结果：

- Agent 正常收到任务并回复 `MULTICA_SMOKE_OK`。
- 实际 Runtime 是 DSH。
- 从 Issue 创建到执行完成约 1 分 41 秒。
- API 已完成时，前端曾短暂仍显示 Todo/Working；刷新后恢复。因此排障应以 CLI/API 的 Issue、Run、Comment 为执行事实，UI 作为展示层。

### 5.2 真实 branch + commit + MR Smoke Test

Issue：`WW1-3`，当前状态 `in_review`。

已验证：

- Agent 在隔离工作目录 checkout repository。
- 创建了文件 `specwire-agent-smoke.md`。
- Branch：`agent/specwire-dev/c8ae87ed`
- Commit：`494dd55ad996a61a486c206041a25d039e137966`
- MR：`http://gitlab.specwire.test:8929/specwire/specwire-poc/-/merge_requests/1`
- 目标为 `main`。
- 没有直接 push main，也没有合并 MR。
- MR 标题和描述包含 `WW1-3`。

该 Run 的工作目录：

```text
/Users/ww/multica_workspaces_specwire-local/
  d388048a-4685-48a6-9acc-a7567097d11a/
  c8ae87ed/workdir
```

每个 Run 使用独立 task/workdir，因此多个 Agent 或任务不会共享同一个 checkout 目录。Git 层仍应使用独立分支处理并发。

在普通终端直接执行：

```bash
multica repo checkout ...
```

出现 `MULTICA_DAEMON_PORT not set` 是预期行为：该命令设计为在 daemon task 内运行，不是普通宿主终端 checkout 命令。

当前 `multica issue pull-requests WW1-3 --output json` 返回空数组，尽管 GitLab MR 已存在。这表示 native GitLab VCS 关联尚未验证成功，或 MR 没有被 Multica webhook 关联；这是后续独立检查项，不影响“Agent 能 push 并创建 MR”的结论。

### 5.3 其他 Issue

- `WW1-2` 是一次无关的 blocked 测试项，可忽略；不要未经授权删除。

### 5.4 端到端闭环联调（M4，2026-08-19）✅

真实环境跑通完整工作流：`add-user-login` change 从提案发布到归档闭环。

| 环节 | 事实 | 验收项 |
|---|---|---|
| proposal push（`ff1cc36`） | Bridge 创建 **WW1-4**（backlog、未分配、metadata 完整） | 3 ✅ |
| GitLab Resend 重放 | Bridge 返回 `duplicate`，仍只有一张 | 4/5 ✅ |
| Backlog 无 Run | status=backlog + 无 assignee | 6 ✅ |
| 人转 Todo + 分配 SpecWire Dev | Run `2ab82258` 开工，3 分钟完成 | 7 ✅ |
| Agent 交付 | 分支 `agent/specwire-dev/2ab82258`、commit `ba0f69e6`、**MR !2**，未 push main | 8 ✅ |
| 人合并 MR | main → `c645701f` | — |
| archive push（D17） | Bridge 自动将 WW1-4 置 **done**，无新卡 | 9 ✅ |

未做：验收 10（Multica 停服 5xx/恢复重放，破坏性测试，可选）。遗留：`multica issue pull-requests` 关联仍为空（native VCS webhook 路径未通，见 §9 #6）。

## 6. 已核实的 Multica 调度机制

1. Workspace 中“存在 Agent”不意味着任意 Issue 会自动被某个 Agent 抢走。
2. Direct Agent：Issue 必须明确路由/分配给该 Agent。
3. Todo + Agent assignee 会触发执行。
4. Backlog 通常不会因分配立即执行，但为了严格闸门，推荐 Backlog 阶段保持未分配。
5. 已分配 Backlog 上的评论或 mention 可能通过 assignee fallback 提前唤醒 Agent，因此不要把“已分配 Backlog”当作绝对隔离。
6. Squad 通过 Leader 接收任务。是否拆给多个 Agent由 Leader 决策，不是所有复杂任务都会自动并行。
7. Autopilot 是预配置触发器和执行目标，不是动态扫描 Workspace 后选择空闲 Agent 的调度中心。
8. Multica 的 pending task 去重不是 SpecWire 的业务幂等，不能防止 webhook 重复创建两张不同 Issue。

## 7. 当前文档地图与优先级

### 7.1 当前决策与事实

1. [`SPECWIRE_BRIDGE_DESIGN.md`](./SPECWIRE_BRIDGE_DESIGN.md)（**当前最高优先级文档**）
   - SpecWire Bridge 技术设计，**已定稿**（决策 D1–D17）。
   - 定义事件契约、验签（HMAC）、判重（SQLite）、archive→done 自动化、里程碑 M1–M5。
2. [`SPECWIRE_BRIDGE_TEST_CASES.md`](./SPECWIRE_BRIDGE_TEST_CASES.md)
   - 测试用例 TC-BR-001~036 与验收矩阵映射，46 个单测全绿。
3. [`research/SPECWIRE_WORKFLOW_DECISION.md`](./research/SPECWIRE_WORKFLOW_DECISION.md)（ADR-001）
   - 记录为什么采用 Workflow 而不是 Product。
   - 定义 publish commit、Backlog metadata、稳定键、Agent/MR 约定。
   - 其中“实时桥接方式”仍是待选表述，实际已按“直接 CLI Bridge”实施（见 Bridge 设计 D1–D17）。
4. [`research/OPENSPEC_STORE_VS_GIT_HOOK_RESEARCH.md`](./research/OPENSPEC_STORE_VS_GIT_HOOK_RESEARCH.md)
   - 明确 Store 不替代 webhook；明确 Autopilot `create_issue` 会创建 Todo 并立即执行。
5. [`research/MULTICA_DISPATCH_MECHANISM.md`](./research/MULTICA_DISPATCH_MECHANISM.md)
   - Agent、Runtime、Squad、Autopilot、Backlog/Todo 和 worktree 的已验证机制。

### 7.2 历史或已废弃材料

4. [`idea.v3.md`](./research/idea.v3.md)
   - 较完整但偏 Product/Reconciler 的设计。
   - 其中 Adapter 是“无状态验签后转发到 generic Autopilot”，不是当前直接 CLI Bridge 方向。
   - 可借用安全和可靠性约束，不要把整套架构当作当前 PoC 决策。

5. [`deprecated.IMPLEMENTATION_GUIDE.md`](./research/deprecated.IMPLEMENTATION_GUIDE.md)
   - 已明确重命名为 deprecated。
   - 描述的是旧版 `GitLab → 无状态 Adapter → generic Autopilot → Reconciler`。
   - 不应按此文件直接实施。

6. [`idea.md`](./research/idea.md)、[`idea.v2.md`](./research/idea.v2.md)
   - 历史演进材料，仅用于理解思路变化。

## 8. 下一里程碑：已全部完成（M1–M5）

Bridge 生命周期：设计定稿（M1）→ 骨架/SQLite（M2）→ 管线（M3）→ HMAC 验签（M3b）→ archive→done（M3c）→ 真实联调（M4）→ **Docker Compose 部署固化（M5）**。

### M5 部署固化（2026-08-19 完成）

- `bridge/Dockerfile`：多阶段构建（golang:1.26-alpine → alpine:3.22），镜像内置 multica CLI 0.4.29 linux/arm64（官方 release 解压即用）
- `bridge/compose.yaml`：`restart: unless-stopped`、`env_file: .env`、`ports 8787:8787`、`./data:/app/data`（SQLite 持久化，历史已迁移）、profile 只读挂载、`specwire-net` external 网络复用、日志 max-size 10m×3
- 关键覆盖：`MULTICA_SERVER_URL=http://host.docker.internal:28080`（profile config.json 写死 127.0.0.1，容器内必须覆盖）、`HOME=/multica`（CLI 找 profile 的位置）
- 生命周期：`docker compose build && docker compose up -d`（升级一条命令）；日志 `docker compose logs -f`
- 注意：allowlist 加项目 = 改 `.env` + `docker compose restart bridge`（D16 env_file 注入）

### 8.4 验收矩阵（已完成 1–9，剩 10）

1. 普通非 spec push → `ignored` ✅
2. proposal 未带 `proposal-ready` → `ignored` ✅
3. 合法 proposal-ready push → 恰好一张未分配 Backlog ✅
4. 同一个 delivery 重放三次 → 仍只有一张 Issue ✅
5. 不同 delivery、相同业务稳定键 → 仍只有一张 Issue ✅
6. Backlog 创建后没有 Agent Run ✅
7. 人将其改为 Todo 并分配 `SpecWire Dev` → Agent 开始工作 ✅
8. Agent 创建 branch、commit、MR，不直接 push main ✅
9. archive push → 不创建新的开发 Backlog，且自动置卡 Done（D17）✅
10. Multica 暂停时 Bridge 返回可重试失败；恢复后可重放成功 ⬜ 未做（破坏性测试，可选）

## 9. 尚未收敛的决定

已决（2026-08-19，见 Bridge 设计 D1–D17）：实现语言 Go；SQLite 判重；payload 读 trailer；宿主用户进程（launchd 固化待做）；同一 change 新 SHA 新建卡（D10）；HMAC 验签（D15）；`.env` 配置（D16）；archive→done 自动化（D17）。

仍开放：

1. 是否把“直接 CLI Bridge”正式写入 `research/SPECWIRE_WORKFLOW_DECISION.md`（ADR-001 §7 的 bridge 项仍是待选表述）。
2. 是否以及何时添加 schedule reconciliation 作为漏事件兜底。
3. Native GitLab VCS webhook 为什么没有把 MR 关联到 Issue（`multica issue pull-requests` 返回空，WW1-3 与 WW1-4 均如此）。
4. 是否把 `SpecWire Dev` 从 DSH Runtime 切换为 Codex Runtime，以验证“不依赖 DSH”的目标。
5. 人的最终通知来源选择 GitLab 还是 Multica。
6. 验收 10（Multica 停服 5xx/恢复重放）是否执行（破坏性测试）。

## 10. 新会话可先运行的只读检查

以下命令只用于核实状态，不会创建或修改资源：

```bash
docker ps --format '{{.Names}}\t{{.Status}}\t{{.Ports}}'

multica --version

find "$HOME/.multica/profiles" \
  -mindepth 1 -maxdepth 1 -type d -exec basename {} \;

multica --profile specwire-local workspace list --output json
multica --profile specwire-local agent list --output json
multica --profile specwire-local runtime list --output json
multica --profile specwire-local project list --output json
multica --profile specwire-local repo list --output json
multica --profile specwire-local issue list --output json

git ls-remote git@gitlab-specwire-agent:specwire/specwire-poc.git
```

不要把“只读检查可运行”理解成允许新会话直接开始部署或写 Bridge。后者需要用户明确授权。

## 11. 建议新会话的第一句话与第一个决策

建议新会话先向用户确认：

> 已读取交接。Bridge（Go）已实现并接入真实 GitLab webhook，验收 1–9 全绿；当前下一步是 M5：把 Bridge 固化为 launchd 常驻服务（编译二进制 + plist + 日志重定向）。我不改现有 GitLab/Multica 配置，除非你明确让我实施。
