# SpecWire Bridge 技术设计（PoC）

> 状态：**已定稿（2026-08-19）**，U1–U5 已全部确认（D10–D14）
> 日期：2026-08-19
> 依据：[HANDOFF.md](./HANDOFF.md) 第 8 节、[SPECWIRE_WORKFLOW_DECISION.md](./research/SPECWIRE_WORKFLOW_DECISION.md)（ADR-001）、[MULTICA_DISPATCH_MECHANISM.md](./research/MULTICA_DISPATCH_MECHANISM.md)
> 关联测试用例：[SPECWIRE_BRIDGE_TEST_CASES.md](./SPECWIRE_BRIDGE_TEST_CASES.md)

## 1. 目的与范围

### 1.1 目标

实现一个极小的常驻 HTTP 服务（**SpecWire Bridge**），把 GitLab 的规格发布事件桥接到 Multica：

```text
GitLab Push Webhook POST
  → Bridge（验签 → 过滤 → 解析 → 判重）
  → multica --profile specwire-local issue create --status backlog
  → 未分配的 Multica Backlog Issue
```

**本设计只覆盖验收矩阵第一阶段（前 7 项）**：

1. 普通非 spec push → ignored，无 Issue。
2. proposal 未带 `proposal-ready` → ignored。
3. 合法 proposal-ready push → 恰好一张未分配 Backlog。
4. 同一个 delivery 重放三次 → 仍只有一张 Issue。
5. 不同 delivery、相同业务稳定键 → 仍只有一张 Issue。
6. Backlog 创建后没有 Agent Run。
7. 人将其改为 Todo 并分配 `SpecWire Dev` → Agent 开始工作。

### 1.2 非目标（本阶段明确不做）

- 不读取 GitLab API（payload 即事实，trailer 从 commit message 解析）。
- 不做 schedule 补偿扫描、不做 Reconciler、不做 archive 闭环。
- 不做 MR/Pipeline 关联（那是 Multica native GitLab VCS webhook 的职责，另一条路径）。
- 不做 SpecWire Product / Core / 状态机。
- 不处理 `SpecWire-Event: archived` 之外的任何开发触发（archived 必须忽略）。
- 不引入 CI、镜像、Kubernetes。

## 2. 架构总览

```text
GitLab CE（容器内）
  │  POST http://host.docker.internal:8787/gitlab/specwire
  │  headers: X-Gitlab-Token, X-Gitlab-Event, X-Gitlab-Delivery
  ▼
┌─────────────────────────────────────────────────────────┐
│ SpecWire Bridge（Mac 宿主用户进程，Go 单二进制）          │
│                                                         │
│  1. 验签     X-Gitlab-Token == 环境变量 secret          │
│  2. 事件类型 X-Gitlab-Event == "Push Hook"              │
│  3. allowlist project.path_with_namespace 白名单        │
│  4. 分支     payload.ref == "refs/heads/main"           │
│  5. 解析     commit message 中的 SpecWire trailer      │
│  6. 判重     SQLite 唯一索引（业务稳定键）              │
│  7. 调用     os/exec 参数数组 → multica CLI            │
│  8. 落库     成功映射 / 失败状态                        │
└─────────────────────────────────────────────────────────┘
  │ subprocess（不经过 shell）
  ▼
multica --profile specwire-local issue create --status backlog
```

## 3. 决策记录

### 3.1 本次已定（用户已拍板或已核实）

| # | 决策 | 选择 | 依据 |
|---|---|---|---|
| D1 | 实现语言 | **Go**（本机 `go1.26.2 darwin/arm64` 已装） | 用户倾向；标准库覆盖 HTTP/HMAC/SQLite 全部需求；单二进制；`os/exec` 参数数组天然满足"不拼 shell 字符串" |
| D2 | project allowlist | **环境变量**，逗号分隔，后续可追加 | 用户拍板 |
| D3 | 判重存储 | **SQLite**（`modernc.org/sqlite`，纯 Go、无 CGO） | HANDOFF §3.2 推荐；唯一索引抗并发，确定性最好 |
| D4 | payload 解析 | **最小版：从 commit message 读 trailer**，不调 GitLab API | HANDOFF §3.3；契约"一次 push 一个发布 commit" |
| D5 | 部署形态 | ~~Mac 宿主用户进程~~ → **Docker Compose 容器**（`bridge/Dockerfile` + `compose.yaml`） | 用户确认（2026-08-19）；多阶段构建（golang→alpine），镜像内置 multica CLI（linux/arm64 官方 release）；`restart: unless-stopped` 自带崩溃重启；复用现有 `specwire-net` 网络 |
| D6 | 监听地址 | **`0.0.0.0:8787`**（默认，环境变量可覆盖） | GitLab 容器内 `127.0.0.1` 指向容器自身，必须用 `host.docker.internal` 访问宿主 |
| D7 | 创建语义 | `--status backlog`、**不传 `--assignee`**、不传 `--allow-duplicate` | HANDOFF §2.2；MULTICA_DISPATCH §10.1 严格闸门 |
| D8 | CLI 调用方式 | `os/exec` **参数数组**，禁止 shell 字符串拼接 | HANDOFF §2.2 明确约束 |
| D9 | 返回语义 | 见 §9：`2xx ignored / 2xx duplicate / 2xx / 5xx` | HANDOFF §3.2 |
| D10 | 同一 change 的新批准 SHA | **新建卡**（稳定键含 SHA，天然新版本卡；旧卡人工取消） | 用户确认（2026-08-19）；ADR-001 §8 缓解措施；"版本=任务身份"，不打断进行中 Run |
| D11 | allowlist 匹配字段 | `project.path_with_namespace`（如 `specwire/specwire-poc`） | 用户确认 |
| D12 | 监听端口 | `8787` | 用户确认 |
| D13 | 失败重试语义 | 相同稳定键、上次 `state=error` → 允许重试（覆盖旧记录） | 用户确认 |
| D14 | GitLab webhook 配置时机 | Bridge 单测/验收通过后再配置（后置） | 用户确认 |
| D15 | 请求验签机制 | **Standard Webhooks HMAC-SHA256 签名**（GitLab 19.1 GA 的 Signing token，`whsec_` 前缀） | 用户确认（2026-08-19）；替代已标记弃用的 `X-Gitlab-Token` 明文 token（GitLab deprecation #596848）；header：`webhook-id` / `webhook-timestamp` / `webhook-signature` |
| D16 | 配置注入 | 环境变量 + **可选 `./.env` 文件**（环境变量优先，`.env` 不覆盖；`.env` 必须进 `.gitignore`） | 用户确认（2026-08-19）；本地开发便利，部署仍走 env 注入 |
| D17 | archive → done 自动化 | `archived` 事件**按 project+change_id 匹配**（不含 SHA）最新 created 的实现卡，自动置 `done` | 用户确认（2026-08-19）；归档 push 的 after_sha 与建卡时必然不同，稳定键无法匹配；archived 是"change 状态变更"的信号载体，SHA 不参与匹配 |
| D18 | 部署形态升级 | **Docker Compose 容器**（`bridge/Dockerfile` + `compose.yaml`） | 用户确认（2026-08-19）；多阶段构建，镜像内置 multica CLI linux/arm64；`restart: unless-stopped`；复用 `specwire-net`；`MULTICA_SERVER_URL` 覆盖 profile 的 127.0.0.1；profile 只读挂载 |
| D19 | 多 change 发布 | **一次 push 可含多个 proposal/archived commit**，每个 change 独立建卡/归档；`approved_commit_sha` = 该 change 所在 commit 的 id（单 change 时等于 push after）；同 change 同 push 多 commit → 取最新 | 用户确认（2026-08-19）；`parseAllTrailers` 收集全部事件；响应汇总：任一失败 502（重试幂等收敛）、有创建 200、全重复 duplicate |
| D20 | GitLab 项目 → Multica project 映射 | **`SPECWIRE_PROJECT_MAP`**：逗号分隔 `gitlab path:multica title/ID`；启动时调 `project list` 把 title 解析为 UUID（歧义/缺失 → 启动失败）；allowlist 每项必须有映射；未配置时回退默认 `SPECWIRE_MULTICA_PROJECT_ID`（旧行为） | 用户确认（2026-08-19）；webdeck 卡曾错误归入 SpecWire PoC project——project 归属决定 Agent 执行时的仓库上下文（WebDeck project 已绑定 webdeck 的 gitlab repo）；CLI 只接受 UUID（实测），配置可写 title 提升可读性 |
| D21 | 幂等键含目标 project | **稳定键 = `<path>:<change>:<sha>:<multica project id>`**（第四段为目标 project） | 用户确认（2026-08-19）并否决了更复杂的"漂移检测"方案；同一发布事件在不同目标 project 下键不同——配置变更后重放自动建到新 project（修复建错归属），同一配置下重放仍幂等；旧格式键行残留无害（不再命中） |
| D22 | Agent 实现约定四层保障 | L1 Agent Instructions（平台注入，每次运行必读）→ L2 specwire-workflow skill（按需加载）→ L3 issue description 关键行（必读兜底）→ L4 人复验 MR | 用户确认（2026-08-19）；WW1-4 时 Instructions 为空、全靠 Agent 临场推断；instructions 经 `PUT /api/agents/{id}` 写入（613 字符，5 条硬规则）；skill 已创建并分配 `SpecWire Dev` |
| D23 | 发布者按次指定建卡状态/分配 | 新 trailer：`SpecWire-Status: todo`（默认 backlog）与 `SpecWire-Assignee: <name>`（可选）；publish.sh 加 `--todo`/`--assignee` 选项 | 用户确认（2026-08-19）；发布者按次选择评审模式或直通模式，Bridge 不写死；`SpecWire-Status` 非法值（非 backlog/todo）→ 该 commit 视为无效（ignored）；assignee 在 publish.sh 侧预校验（agent list）；todo 语义已同步 Instructions 第 7 条与 skill |

### 3.2 决策状态

U1–U5（原待决项）已于 2026-08-19 全部确认并入 §3.1（D10–D14），本设计**定稿**。仍开放的小项：

- TC-BR-027：非法 JSON payload → **`200 ignored` + 错误日志**（GitLab 重试不会修复坏 payload，避免重试风暴；与 §5.8 一致）。

## 4. 配置项（全部环境变量，不写入仓库）

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `SPECWIRE_ALLOWED_PROJECTS` | 是 | 无 | 逗号分隔的 GitLab `path_with_namespace` 白名单，如 `specwire/specwire-poc`；后续新增项目直接追加 |
| `SPECWIRE_WEBHOOK_SECRET` | 是 | 无 | Standard Webhooks **Signing token**（`whsec_` 开头，与 GitLab webhook 表单 Generate signing token 的值一致）；启动校验：`whsec_` 前缀 + 后缀可 base64 解码；不写仓库 |
| `SPECWIRE_MULTICA_PROFILE` | 否 | `specwire-local` | Multica CLI profile |
| `SPECWIRE_MULTICA_PROJECT_ID` | 是 | 无 | Multica Project ID：`3e7d61cd-900b-41a8-85f4-c97019e2020f` |
| `SPECWIRE_LISTEN_ADDR` | 否 | `0.0.0.0:8787` | 监听地址；**不要默认绑 `127.0.0.1`** |
| `SPECWIRE_DB_PATH` | 否 | `./specwire-bridge.db` | SQLite 文件路径 |
| `SPECWIRE_REF_FILTER` | 否 | `refs/heads/main` | 只处理的分支 ref |
| `SPECWIRE_CLI_TIMEOUT` | 否 | `30s` | multica CLI 调用超时 |
| `SPECWIRE_LOG_LEVEL` | 否 | `info` | `slog` 级别 |
| `SPECWIRE_PROJECT_MAP` | 否 | 无 | GitLab path → Multica project 映射（D20）：`gitlab/path:Multica title 或 UUID`，逗号分隔；配置后 allowlist 每项必须在此有映射，否则启动失败；未配置时回退 `SPECWIRE_MULTICA_PROJECT_ID` |

启动时校验：必填项缺失 → 启动失败并给出明确错误，不静默运行。

配置注入顺序（D16）：启动时先尝试加载 `./.env`（不存在则忽略），**已存在的环境变量优先**——`.env` 只提供默认值，真实部署用 env 注入时行为一致。`.env` 含 secret，必须在 `.gitignore` 中。`.env` 格式：`KEY=VALUE` 逐行，`#` 注释、空行忽略，值支持单/双引号包裹。

## 5. 请求处理管线（按序执行，任一步不通过即短路返回）

### 5.1 验签（Standard Webhooks HMAC-SHA256，D15）

GitLab 19.1+ 配置 **Signing token** 后，每个 webhook 请求带三个 header（遵循 [Standard Webhooks 规范](https://www.standardwebhooks.com/)）：

| Header | 内容 |
|---|---|
| `webhook-id` | 消息 ID（Bridge 的 delivery key 来源） |
| `webhook-timestamp` | Unix 秒 |
| `webhook-signature` | `v1,<base64>`，可能多个空格分隔 |

验证步骤：

1. 先读取**原始 body**（签名基于 raw body，必须先读再验）。
2. 构造消息串 `"{webhook-id}.{webhook-timestamp}.{raw_body}"`。
3. key = `SPECWIRE_WEBHOOK_SECRET` 去掉 `whsec_` 前缀后 **base64 解码**。
4. 计算 HMAC-SHA256 → base64 → 前缀 `v1,`。
5. 与 `webhook-signature` 中**每个**签名用 `crypto/subtle.ConstantTimeCompare` 比对（防时序攻击），任一匹配即通过。
6. `webhook-timestamp` 超出当前时间 ±5 分钟 → 401（防重放）。
7. 任一 header 缺失 / 比对失败 → **401**。

> 历史：原设计用 `X-Gitlab-Token` 明文 token，GitLab 已将其标记 "(not recommended)" 并计划移除（deprecation #596848）；M3 后按方案 B 切换为 HMAC 签名。

### 5.2 事件类型

- header `X-Gitlab-Event != "Push Hook"` → **200 ignored**（GitLab 可能会发 Test Hook 等，一律忽略且不重试）。

### 5.3 Project allowlist

- 解析 `payload.project.path_with_namespace`，不在 `SPECWIRE_ALLOWED_PROJECTS` 中 → **200 ignored**。
- 新增项目只需改环境变量，无需改代码。

### 5.4 分支过滤

- `payload.ref != SPECWIRE_REF_FILTER` → **200 ignored**。
- 删除分支 push（`payload.after` 为全零 `0000...`）→ **200 ignored**（无新 commit 可批准）。

### 5.5 Trailer 解析（最小版）

从 push 的 commit message 中读取 trailer：

```text
spec(add-user-login): publish proposal

SpecWire-Event: proposal-ready
SpecWire-Change: add-user-login
```

解析规则：

1. **收集全部事件**（D19）：合并 `head_commit` 与 `commits`（按 commit id 去重），从新到旧遍历，每个带完整 trailer 的 commit 生成一个事件；同一 `change_id` 在 push 内出现多次 → 只保留最新一次。
2. 每个 proposal-ready 事件独立建卡：稳定键 `project:change_id:<该 commit 的 id>`（D19；单 change 时 commit id 等于 push after，与旧契约一致）。
3. `SpecWire-Event` 取值：
   - `proposal-ready` → 建卡（见 §5.6/§5.7，逐事件循环）。
   - `archived` → **不创建开发 Backlog**，自动将该项目+change 最新 created 的实现卡置 `done`（D17）；无匹配卡则仅忽略。
   - 其他值 / 缺失 → 记日志忽略。
4. 必须同时存在 `SpecWire-Change` 且非空；缺失 → 该 commit 视为无 trailer。
5. **可选 trailer（D23）**：`SpecWire-Status: todo`（默认 backlog）与 `SpecWire-Assignee: <name>`；`SpecWire-Status` 非法值（非 backlog/todo）→ 该 commit 视为无 trailer（不静默建错状态）。
6. 响应汇总（D19）：任一建卡失败 → **502**（GitLab 重试，已成功者走 duplicate/error 重试分支幂等收敛）；有创建 → 200；全重复 → `200 duplicate`；无可处理事件 → ignored。

### 5.6 判重（SQLite）

业务稳定键（HANDOFF §3.1）：

```text
<GitLab project path>:<change ID>:<approved commit SHA>
```

例如：`specwire/specwire-poc:add-user-login:494dd55a...`

流程：

1. `INSERT OR IGNORE`（事务内）。
   - 插入成功 → 本次负责创建（见 5.7）。
   - 冲突（已存在）→ 查状态：
     - `state=created` → **200 duplicate**，不创建。
     - `state=error` 且按 D13 → 允许重试：更新该行（清空 `last_error`）后继续创建。
2. 创建完成后在同一事务中更新 `multica_issue_id` / `state` / `created_at`。

**并发说明**：SQLite 唯一索引保证并发重放只有一个请求插入成功；多请求竞争时失败者走 duplicate 分支。写操作使用单写事务 + `busy_timeout`（如 5s）。

### 5.7 调用 Multica CLI

```go
args := []string{
  "--profile", cfg.MulticaProfile,
  "issue", "create",
  "--project", cfg.MulticaProjectID,
  "--status", "backlog",
  "--title", "[SpecWire] " + changeID,
  "--description-stdin",
  "--output", "json",
}
```

- 通过 `os/exec` 参数数组执行，**绝不把 webhook 字段拼进 shell 字符串**（D8）。
- description 经 stdin 传入（`--description-stdin`），内容为 metadata + 指引：

```text
[SpecWire Backlog] 由 GitLab push 自动创建，等待人工批准开工。

repository: specwire/specwire-poc
change_id: <changeID>
approved_commit_sha: <afterSHA>
target_branch: main
```

- 超时 `SPECWIRE_CLI_TIMEOUT`（默认 30s）；超时按失败处理。
- 结果：
  - 退出码 0 → 解析 stdout JSON，取 issue id → 更新 SQLite → **200**。
  - 非零退出 / 超时 → 更新 SQLite `state=error` + `last_error` → **502**（GitLab 会按自身重试策略重发）。

### 5.8 响应

| 场景 | HTTP | 说明 |
|---|---|---|
| 验签失败（header 缺失 / 签名不匹配 / 时间戳过期） | 401 | 不重试；记 warn 日志含 reason |
| 非 Push Hook / 非白名单项目 / 非 main / 无 trailer / 删除分支 | 200 ignored | GitLab 视为成功，不再重试 |
| archived 事件 | 200 ignored | 不建开发 Backlog；自动置 Done（D17）：有匹配卡 → detail `issue <id> set done`；无卡 → `no issue to close`；置 Done 失败 → `mark done failed`（记日志，不返回 5xx，GitLab 重试无意义） |
| 非法 JSON payload | 200 ignored | 记错误日志；重试不会修复坏 payload（TC-BR-027） |
| 业务重复（稳定键已 created） | 200 duplicate | 不再创建 |
| 创建成功 | 200 | — |
| Multica CLI 失败 / 超时 | 502 | 可重试；重放时走 error→重试分支 |

## 6. GitLab Payload 边界（最小版接受的简化）

- `payload.commits` 可能被 GitLab 截断（默认只给部分 commit）；最小版依赖 `head_commit` 与"一次 push 一个发布 commit"契约，**接受截断风险**。
- 如果后续发现 trailer 漏读，升级方案（不在本阶段）：把 payload 当唤醒信号，按 `after` 调 GitLab API 精确读取 commit——需要 GitLab API token 与额外配置，届时再决策。

## 7. SQLite Schema

```sql
CREATE TABLE IF NOT EXISTS events (
  stable_key       TEXT PRIMARY KEY,   -- <project_path>:<change_id>:<after_sha>
  delivery_key     TEXT NOT NULL,      -- webhook-id（Standard Webhooks 消息 ID；审计用，不设唯一约束）
  state            TEXT NOT NULL,      -- created | processing | error（见下方说明）
  multica_issue_id TEXT,               -- 创建成功后的 Multica Issue ID
  project_path     TEXT NOT NULL,
  change_id        TEXT NOT NULL,
  after_sha        TEXT NOT NULL,
  created_at       TEXT NOT NULL,      -- UTC RFC3339
  last_error       TEXT
);
```

- 状态语义（实现细化，M2 已落地）：
  - `processing`：已认领、multica CLI 调用中。**并发重复投递遇到 processing 返回 Duplicate**（不重复创建）；若创建最终失败，认领请求自身返回 502 触发 GitLab 重试，重试到达时状态已变为 error → 走重试分支，最终一致。
  - `created`：创建成功，重复投递返回 Duplicate。
  - `error`：创建失败，允许同稳定键重试（覆盖旧记录）。
- `delivery_key` 只记录不判重：GitLab 重试是否复用同一 `webhook-id` 不保证，业务幂等一律以 `stable_key` 为准（HANDOFF §3.2 的两层判重中，PoC 以业务判重为硬约束）。
- 迁移：PoC 无迁移机制，schema 变更直接重建（本机数据可弃）。

## 8. 日志与可观测性

- 使用标准库 `log/slog`，输出到 stdout，JSON 格式。
- 每个请求关键字段：`delivery_key`（webhook-id）、`project_path`、`change_id`、`after_sha`、`result`（ignored/duplicate/created/error）、`http_status`。
- **禁止**记录：signing token（`SPECWIRE_WEBHOOK_SECRET`）、Multica token、payload 全文。
- 一个请求一条结果日志 + 错误时附 `last_error`（截断，如 500 字符）。

## 9. 安全约束

1. secret 仅环境变量注入，不落仓库、不进日志。
2. subprocess 参数数组，杜绝注入。
3. 监听 `0.0.0.0` 是容器可达所必需，但依赖 webhook secret 做唯一防线——GitLab 侧同时开启 outbound request 限制（仅允许 `host.docker.internal:8787`）。
4. Bridge 运行用户需要能读 `specwire-local` profile（即宿主用户本人），不把 profile token 复制进仓库。

## 10. 代码结构（建议）

```text
bridge/
  go.mod                        # module specwire/bridge
  main.go                       # 入口：加载配置、开 SQLite、起 HTTP
  config.go                     # 环境变量解析 + 校验
  handler.go                    # HTTP handler：验签 → 过滤 → 解析 → 判重 → 调用
  store.go                      # SQLite：Init / UpsertStableKey / MarkCreated / MarkError
  multica.go                    # CLI 调用（os/exec，返回 issue id 或 error）
  handler_test.go               # httptest 驱动的管线测试（配 fake multica）
  store_test.go                 # SQLite 判重与并发测试
  testdata/
    push_proposal.json          # GitLab push payload fixture
    push_archived.json
    push_no_trailer.json
    ...
  scripts/fake-multica          # 测试用假 CLI：记录 argv，按环境变量模拟成功/失败/超时
```

PoC 不做分层抽象（不引入接口/依赖注入），fake 通过"假 CLI 可执行文件 + `PATH` 前置"注入；如后续要扩展再重构。

## 11. 里程碑拆分

| 里程碑 | 内容 | 完成标志 |
|---|---|---|
| M1 | 本文档 + 测试用例定稿 | ✅ 已完成（2026-08-19，U1–U5 已确认） |
| M2 | Go 骨架 + 配置 + SQLite | ✅ 已完成（2026-08-19，15 个单测含并发 + `-race` 全绿） |
| M3 | 管线：验签/过滤/trailer/判重/CLI | ✅ 已完成（2026-08-19，38 个单测含 `-race` 全绿；覆盖 TC-BR-001~027） |
| M3b | 验签切换方案 B：HMAC-SHA256（D15/D16）+ `.env` 加载 | ✅ 已完成（2026-08-19，44 个单测含 `-race` 全绿；新增 TC-BR-031~033 及 config 用例） |
| M3c | archive → done 自动化（D17）：按 project+change_id 匹配置 Done | ✅ 已完成（2026-08-19，46 个单测含 `-race` 全绿；TC-BR-010 重写 + TC-BR-035/036 + TestLatestCreatedIssue） |
| M3d | 多 change 发布（D19）：parseAllTrailers + 循环建卡/归档 + 响应汇总 + publish.sh 多 change | ✅ 已完成（2026-08-19，48 个单测含 `-race` 全绿；TC-BR-037~039 + 归档循环） |
| M3e | project 映射（D20）：SPECWIRE_PROJECT_MAP + title→UUID resolve + allowlist 校验 | ✅ 已完成（2026-08-19，57 个单测含 `-race` 全绿；容器已部署，`project map loaded entries=2`） |
| M4 | 真实 GitLab webhook 配置与联调 | ✅ 已完成（2026-08-19，验收 1–9 全绿 + archive→done 自动化真实验证；验收 10 破坏性测试未做，可选） |
| M5 | 部署固化：Docker Compose 容器化 | ✅ 已完成（2026-08-19；Dockerfile 多阶段 + compose.yaml + 数据迁移 + 容器内 CLI 连通验证） |

## 12. 关联文档

- [SPECWIRE_BRIDGE_TEST_CASES.md](./SPECWIRE_BRIDGE_TEST_CASES.md) —— 测试用例与验收映射
- [HANDOFF.md](./HANDOFF.md) —— 交接与约束
- [SPECWIRE_WORKFLOW_DECISION.md](./research/SPECWIRE_WORKFLOW_DECISION.md) —— ADR-001，本文定稿后建议把"直接 CLI Bridge"回填为正式决策
