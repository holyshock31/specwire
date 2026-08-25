# SpecWire Bridge 测试用例

> 状态：**已定稿（2026-08-19）**，配合 [SPECWIRE_BRIDGE_DESIGN.md](./SPECWIRE_BRIDGE_DESIGN.md) 使用
> 日期：2026-08-19
> 目标：覆盖 HANDOFF §8.4 验收矩阵第一阶段（前 7 项），并扩展边界用例
> 编号约定：`TC-BR-xxx`（Bridge），分组编号

## 1. 测试策略

三层，按顺序推进：

1. **单元测试（本机，M2/M3）**：`httptest` 构造 GitLab push 请求打 Bridge handler；Multica CLI 用 **fake 可执行脚本**注入（通过 `PATH` 前置指向 `scripts/fake-multica`）。fake 的行为由环境变量控制（成功 / 非零退出 / 超时），并把它收到的 argv 写到一个文件供断言——这样可以直接验证"参数数组、不经过 shell、不含 secret"。
2. **集成测试（后置，M4）**：真实 GitLab push + 真实 Multica，验收 1–7。需要用户授权配置 GitLab webhook。
3. **手工回归项**：无法自动化的（人批准开工、Agent 行为），记录操作步骤。

### 1.1 假 multica 契约

```bash
# scripts/fake-multica —— 测试用假 CLI
# 环境变量：
#   FAKE_MULTICA_EXIT_CODE  (默认 0)
#   FAKE_MULTICA_DELAY      (秒，模拟超时)
# 行为：
#   1. 把全部 argv 逐行写入 $FAKE_MULTICA_ARGV_FILE
#   2. 按 FAKE_MULTICA_EXIT_CODE 退出
#   3. 退出 0 时，stdout 输出符合真实 CLI 的 JSON（含 issue id）
```

## 2. 公共 Fixture

- 合法 signing token：`whsec_` + base64（测试值 `whsec_dGVzdC1zZWNyZXQ=`，即 `whsec_` + base64("test-secret")）
- 白名单：`SPECWIRE_ALLOWED_PROJECTS=specwire/specwire-poc`
- 合法 payload（`testdata/push_proposal.json`）：

```json
{
  "object_kind": "push",
  "ref": "refs/heads/main",
  "after": "494dd55ad996a61a486c206041a25d039e137966",
  "project": { "id": 1, "path_with_namespace": "specwire/specwire-poc" },
  "head_commit": {
    "id": "494dd55ad996a61a486c206041a25d039e137966",
    "message": "spec(add-user-login): publish proposal\n\nSpecWire-Event: proposal-ready\nSpecWire-Change: add-user-login\n"
  }
}
```

请求头（Standard Webhooks，M3b 起，signature 按 `{webhook-id}.{webhook-timestamp}.{raw_body}` 计算 HMAC-SHA256，key 为 token 去掉 `whsec_` 后 base64 解码，格式 `v1,<base64>`）：

```text
webhook-id: 550e8400-e29b-41d4-a716-446655440000
webhook-timestamp: <当前 Unix 秒>
webhook-signature: v1,<base64(HMAC-SHA256)>
X-Gitlab-Event: Push Hook
```

## 3. 用例清单

### 3.1 请求入口与验签

| ID | 场景 | 请求构造 | 期望 | 验收项 |
|---|---|---|---|---|
| TC-BR-001 | 合法请求 → 创建 Backlog | 完整合法 payload + 正确签名头 | `200`；fake argv 含 `--profile specwire-local issue create --project <id> --status backlog --title [SpecWire] add-user-login --description-stdin --output json`；SQLite 落 `state=created` 且 `multica_issue_id` 非空 | 3 |
| TC-BR-002 | 签名错误 | `webhook-signature: v1,AAAA` | `401`；fake 未被调用；无记录 | — |
| TC-BR-003 | 签名头缺失 | 去掉 `webhook-id`/`webhook-timestamp`/`webhook-signature` | `401`；fake 未被调用 | — |
| TC-BR-031 | 多签名列表含有效值 | `webhook-signature: v1,bad <正确签名> v1,future` | `200 created`（任一匹配即通过） | — |
| TC-BR-032 | 时间戳过期（防重放） | `webhook-timestamp` 为 10 分钟前（签名按该时间戳计算） | `401` | — |
| TC-BR-033 | 非法 token 格式（启动校验） | `SPECWIRE_WEBHOOK_SECRET` 非 `whsec_` 前缀或后缀非 base64 | `LoadConfig` 启动失败 | — |
| TC-BR-034 | `.env` 加载 | `./.env` 提供配置；环境变量已存在时不覆盖 | `LoadConfig` 正常；env 值优先 | — |
| TC-BR-004 | 非 Push Hook | `X-Gitlab-Event: Merge Request Hook` | `200 ignored`；fake 未被调用 | 1 |

### 3.2 过滤规则

| ID | 场景 | 请求构造 | 期望 | 验收项 |
|---|---|---|---|---|
| TC-BR-005 | 项目不在白名单 | `path_with_namespace: other/group/repo` | `200 ignored`；fake 未调用 | 1 |
| TC-BR-006 | 白名单追加后生效 | env 加 `,other/repo` 后重发 TC-BR-005 的 payload | `200`（创建）；验证 allowlist 可配置化 | — |
| TC-BR-007 | 非 main 分支 | `ref: refs/heads/feature/x` | `200 ignored` | 1 |
| TC-BR-008 | 删除分支 push | `after: 0000000000000000000000000000000000000000` | `200 ignored` | — |
| TC-BR-009 | 无 SpecWire trailer | 普通 commit message | `200 ignored` | 2 |
| TC-BR-010 | `archived` 事件（有对应实现卡） | 先 proposal 建卡，再发 archived（**不同 SHA**，模拟归档提交） | `200 ignored`；不建新卡；fake argv 断言 `issue status <卡id> done`（D17 自动置 Done） | 9 |
| TC-BR-040 | `SpecWire-Status: todo` | trailer 带 todo | argv 含 `--status todo` 且不含 `--assignee`（D23 直通模式） | 7 |
| TC-BR-041 | `SpecWire-Assignee: <name>` | trailer 带 assignee | argv 含 `--assignee SpecWire Dev` | 7 |
| TC-BR-042 | `SpecWire-Status` 非法值 | `SpecWire-Status: nonsense` | `200 ignored`；fake 未调用 | — |
| TC-BR-043 | todo 建卡后重放 | 同 payload 重发 | `duplicate`（状态不影响幂等） | 4 |
| TC-BR-035 | `archived` 事件（无对应卡） | `SpecWire-Change: never-created` | `200 ignored`；fake 未调用 | — |
| TC-BR-036 | 匹配规则：同 change 多版本卡 | 同 change 两个 SHA 各建一卡（D10），再发 archived | store 层断言：取**最新 created** 卡（error 状态不计、跨项目隔离） | — |
| TC-BR-011 | trailer 缺 `SpecWire-Change` | 只有 `SpecWire-Event: proposal-ready` | `200 ignored`；不创建 | 2 |
| TC-BR-012 | `head_commit` 为空 | 删掉 head_commit，commits 含带 trailer 的 commit | 从 `commits` 取到 trailer，`200` 创建 | — |
| TC-BR-013 | 多个 proposal commit（不同 change） | commits 含 2 个带 `proposal-ready`（不同 change） | **各建一张卡**（D19）；稳定键用各自 commit id；`200 created` | — |
| TC-BR-038 | 同一 change 同 push 多个 commit | 同 change 两个 proposal commit | **只建一张卡**，取最新 commit（稳定键含最新 commit id） | — |
| TC-BR-039 | 多个 archived commit（不同 change） | commits 含 2 个 `archived` | 各自置 Done；`200 ignored` | 9 |

### 3.3 判重与幂等（验收 4、5 是核心）

| ID | 场景 | 请求构造 | 期望 | 验收项 |
|---|---|---|---|---|
| TC-BR-014 | 同 delivery 重放 3 次 | 同 payload + 同 `X-Gitlab-Delivery` 连发 3 次 | 第 1 次 `200`；第 2、3 次 `200 duplicate`；仅 1 条 SQLite 记录；fake 只被调 1 次 | 4 |
| TC-BR-015 | 不同 delivery、同稳定键 | 同 payload、换 `X-Gitlab-Delivery` | 第 1 次 `200`，第 2 次 `200 duplicate` | 5 |
| TC-BR-016 | 并发同稳定键 | 10 个 goroutine 同时发相同 payload | 恰好 1 个 `200`，其余 `200 duplicate`；SQLite 仅 1 条；fake 只调 1 次 | 4/5 |
| TC-BR-017 | 失败后重试 | fake 先 `FAKE_MULTICA_EXIT_CODE=1` 发一次（`502`，state=error）；恢复 fake 后同稳定键重发 | `200` 创建成功，覆盖为 `created`；SQLite 仍 1 条 | 10（重试语义单测化） |
| TC-BR-018 | 不同 change_id 不误判 | 同 after_sha、不同 `SpecWire-Change` | 各创建 1 张，2 条记录 | 3（隔离性） |

### 3.4 CLI 调用与错误处理

| ID | 场景 | 请求构造 | 期望 | 验收项 |
|---|---|---|---|---|
| TC-BR-019 | CLI 非零退出 | `FAKE_MULTICA_EXIT_CODE=1` | `502`；SQLite `state=error` + `last_error` 非空 | 10 |
| TC-BR-020 | CLI 超时 | `FAKE_MULTICA_DELAY` 大于 `SPECWIRE_CLI_TIMEOUT` | `502`；`state=error` | 10 |
| TC-BR-021 | description 内容 | 捕获 fake 的 stdin 到文件断言 | 包含 `repository: specwire/specwire-poc`、`change_id: add-user-login`、`approved_commit_sha: 494dd55a...`、`target_branch: main` | 3（元数据完整性） |
| TC-BR-022 | title 断言 | 见 TC-BR-001 argv 文件 | `[SpecWire] add-user-login` | 3 |
| TC-BR-023 | 不经过 shell | argv 文件中任意字段含 `$(touch /tmp/pwned)`、`;`、`&` 等字符的 change_id | 按字面传递；`/tmp/pwned` 不存在；fake 收到的 argv 与该字段一致 | 安全 |
| TC-BR-024 | 未分配断言 | fake argv 文件 | argv **不含** `--assignee` / `--assignee-id` | 6（闸门前置） |
| TC-BR-025 | 无 `--allow-duplicate` | fake argv 文件 | argv 不含 `--allow-duplicate` | 幂等契约 |

### 3.5 配置与健壮性

| ID | 场景 | 构造 | 期望 |
|---|---|---|---|
| TC-BR-026 | 缺必填配置启动 | 不设 `SPECWIRE_WEBHOOK_SECRET` | 启动失败，明确报错 |
| TC-BR-027 | 非法 payload JSON | body 为 `not-json` | `200 ignored`（设计 §5.8 已定：重试不会修复坏 payload，避免重试风暴）；记错误日志；不 panic |
| TC-BR-028 | 空 body | — | 不 panic；按约定返回 |
| TC-BR-029 | SQLite 写失败 | DB 文件只读 | `500`；不创建 Issue（事务回滚） |
| TC-BR-030 | 日志无 secret | 全量日志扫描 | 不包含 `test-secret` / token 值 |

### 3.6 验收矩阵映射（供 M4 联调使用）

| HANDOFF 验收项 | 自动化用例 | 联调验证方式 |
|---|---|---|
| 1. 普通非 spec push → ignored | TC-BR-004/005/007/009 | 真实 push 普通 commit |
| 2. 无 proposal-ready → ignored | TC-BR-009/011 | 真实 push 无 trailer commit |
| 3. 合法 push → 恰好一张未分配 Backlog | TC-BR-001/021/022/024/025 | `multica issue list` 核对 |
| 4. 同 delivery 重放 3 次 → 一张 | TC-BR-014 | GitLab webhook 重放（或手工重发） |
| 5. 不同 delivery 同稳定键 → 一张 | TC-BR-015 | 手工重发同 payload |
| 6. Backlog 无 Agent Run | TC-BR-024 | `multica issue show <id>` 无 run |
| 7. 人转 Todo + 分配 → Agent 开工 | —（人工） | 手工操作 + `issue show` 观察 run |
| 8. Agent 只开 MR 不 push main | —（人工，已有 WW1-3 佐证） | 观察 MR |
| 9. archive push 不建卡，且自动置 Done | TC-BR-010/035/036 | 真实 archive push 后核对：无新卡 + issue 变 done |
| 10. Multica 暂停 → 5xx → 恢复重放 | TC-BR-017/019/020 | 停容器实测 |

## 4. 未覆盖 / 接受的限制

1. 不测真实 Multica 行为（单测用 fake），联调前不保证 CLI 输出 JSON 结构假设——M4 第一步先用真实 `multica issue create` 手工验证一次输出格式。
2. 不测 GitLab 自身的重试策略与 `X-Gitlab-Delivery` 复用行为——只测 Bridge 侧响应码。
3. `payload.commits` 截断风险不测（最小版接受，见设计 §6）。
4. 并发测试受 SQLite 行为影响，CI 与本机需用同一 `modernc.org/sqlite` 版本。

## 5. 执行方式

```bash
cd bridge
go test ./... -v          # 单元测试（含并发用例）
# 联调（M4，需授权）：
# 1. 配真实 GitLab webhook → host.docker.internal:8787
# 2. 按 §3.6 逐项执行
```
