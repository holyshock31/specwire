# SpecWire v3 实施手册

> **决策更新（2026-08-18）**：本手册对应的 Product 实施方案已暂缓，当前不得按本手册启动 CLI/Core/Reconciler 等产品化建设。当前采用 **SpecWire Workflow**，见 [SPECWIRE_WORKFLOW_DECISION.md](./SPECWIRE_WORKFLOW_DECISION.md)。本文件仅保留为未来升级参考。
>
> 日期：2026-08-18
> 状态：暂缓，仅作为 Product 方案参考
> 设计依据：[idea.v3.md](./idea.v3.md)
> 原则：严格按阶段门推进；前一阶段未验收，不启动下一阶段

![SpecWire v3 架构](../assets/specwire-v3-architecture.png)

## 1. 目标与范围

本手册把 SpecWire v3 从设计落实为一个最小可运行闭环：

```text
/spec new
  → GitLab main 中 proposed
  → Multica backlog 卡
  → /spec approve
  → approved generation 派给 Codex Agent
  → 实现分支 + implemented + MR
  → CI + 合并
  → /spec verify
  → Multica done
```

GitLab CE 和 Multica 只是基础设施。完整闭环还需要实现：

1. **Spec State 模块**：持久阶段、generation、契约 hash、Git commit/push。
2. **Reconciler 模块**：读取 GitLab、Multica 和 MR/run 观察，生成幂等操作计划。
3. **Webhook Adapter**：验证 GitLab 请求并转换为 Multica generic webhook 能校验的请求。
4. **dsh-spec-loop fork**：把 `/spec` 命令接入 Spec State。
5. **GitLab CI 门禁**：阻止 stale generation 或非法状态迁移进入 main。

## 2. 当前环境快照

以下是 2026-08-18 的本机检查结果，执行前应重新确认：

| 项目 | 当前状态 |
|---|---|
| 主机 | macOS 26.2，Apple Silicon ARM64 |
| 内存 | 16 GB |
| 工作区 | `/Volumes/d/playground/specwire` |
| 可用磁盘 | 约 1.5 TiB |
| Docker | 29.4.0，daemon 正常 |
| Docker Compose | v5.1.2 |
| Codex CLI | 0.147.0 |
| Multica CLI | 未安装 |
| glab | 未安装 |
| specwire Git 仓库 | 尚未初始化 |

GitLab 官方单机基线为 16 GB 内存。当前机器可做单人、低并发 PoC，但不宜同时高负载运行 GitLab、Multica、Runner 和多个 Agent。长期运行建议：

- GitLab CE：独立 Linux 主机，推荐 16 GB 以上；受限 PoC 可按官方低内存设置调优。
- Multica 服务：可与 GitLab 分机部署。
- Multica daemon 与 Codex：运行在开发者实际工作的机器上。
- GitLab、Multica、Webhook Adapter 和 daemon 必须通过稳定主机名互访。

参考：[GitLab 资源要求](https://docs.gitlab.com/install/requirements/)、[GitLab 低内存环境](https://docs.gitlab.com/omnibus/settings/memory_constrained_envs/)、[Multica 自托管](https://multica.ai/docs/self-host-quickstart)。

## 3. 阶段 0：冻结拓扑、版本与命名

### 3.1 需要先确定

| 配置项 | PoC 示例 | 约束 |
|---|---|---|
| GitLab URL | `http://gitlab.specwire.lan:8929` | 浏览器、Multica backend、daemon 均可访问 |
| Git SSH | `ssh://git@gitlab.specwire.lan:2424` | Agent 可推实现分支 |
| Multica App URL | `https://multica.specwire.lan` | 浏览器可访问 |
| Multica API/Public URL | `https://multica-api.specwire.lan` | GitLab webhook 和 daemon 可访问 |
| Adapter URL | `https://hooks.specwire.lan/gitlab/specwire` | 仅接收 GitLab webhook |
| 主分支 | `main` | 受保护 |
| 实现分支 | `specwire/<change-id>-g<generation>` | 确定性命名 |

跨机器部署时禁止使用 `localhost` 作为对外 URL。PoC 使用 HTTP 时只能放在受信局域网；跨主机或公开网络必须使用 TLS。

### 3.2 版本策略

- GitLab CE 使用固定 `<major>.<minor>.<patch>-ce.0` 镜像标签。
- Multica 固定发布 tag 或 `MULTICA_IMAGE_TAG`，不要让生产环境隐式跟随 `latest`。
- Node、GitLab Runner 和基础 CI image 也必须固定版本。
- 版本、镜像 digest、升级日期记录到运行日志。

### 3.3 阶段门

- 所有主机名已解析。
- 端口无冲突。
- GitLab 主机至少满足 PoC 资源预算。
- GitLab、Multica 和 Agent 的网络方向已经画清并通过连通性检查。

## 4. 阶段 1：部署 GitLab CE

### 4.1 创建独立基础设施目录

建议把运行数据放在源码仓库外：

```text
/Volumes/d/playground/specwire-infra/
  gitlab/
    compose.yaml
    .env
    config/
    logs/
    data/
```

`.env`：

```dotenv
GITLAB_VERSION=<固定版本号>
```

不要把 `.env`、root 密码或 token 提交到 specwire 仓库。

### 4.2 GitLab Compose

```yaml
services:
  gitlab:
    image: gitlab/gitlab-ce:${GITLAB_VERSION}-ce.0
    container_name: specwire-gitlab
    restart: unless-stopped
    hostname: gitlab.specwire.lan

    environment:
      GITLAB_OMNIBUS_CONFIG: |
        external_url 'http://gitlab.specwire.lan:8929'
        gitlab_rails['gitlab_shell_ssh_port'] = 2424

        # 仅用于单人低资源 PoC
        puma['worker_processes'] = 0
        sidekiq['concurrency'] = 10
        prometheus_monitoring['enable'] = false

    ports:
      - "8929:8929"
      - "2424:22"

    volumes:
      - ./config:/etc/gitlab
      - ./logs:/var/log/gitlab
      - ./data:/var/opt/gitlab

    shm_size: "256m"
```

生产环境应增加 TLS、邮件、备份和监控，并根据实际资源重新评估低内存配置。官方安装说明：[GitLab Docker](https://docs.gitlab.com/install/docker/installation/)。

### 4.3 启动与首次登录

```bash
cd /Volumes/d/playground/specwire-infra/gitlab
docker compose up -d
docker compose ps
docker logs -f specwire-gitlab
```

初始化完成后读取一次性 root 密码：

```bash
docker exec specwire-gitlab grep 'Password:' /etc/gitlab/initial_root_password
```

验证：

```bash
curl -fsS http://gitlab.specwire.lan:8929/-/readiness
```

### 4.4 创建 project 与权限

1. 创建日常 Maintainer 用户，不长期使用 root。
2. 创建 group 和 `specwire` project。
3. 设置默认分支为 `main`。
4. 保护 `main`：
   - 只允许人类 Maintainer 发布生命周期提交。
   - Agent 身份不得直接 push。
   - 合并必须通过 CI；PoC 阶段必须人工合并。
5. 允许 Agent 推送 `specwire/*` 分支并创建 MR。

### 4.5 凭据分离

| 凭据 | 最小权限 | 使用者 |
|---|---|---|
| Human/dsh SSH | push main | 人类运行 `/spec` |
| Agent Git SSH/token | push 非保护分支 | 实现 Agent |
| Agent glab token | 创建、读取 MR 所需 API 权限 | 实现 Agent |
| Multica VCS token | `read_api` | Multica 原生 VCS 集成 |
| Reconciler GitLab token | 读 repository、branch、MR、pipeline | Reconciler |
| GitLab webhook secret | 仅 webhook 验证 | Adapter 或 Multica VCS |

这些凭据不得混用，不得放进 prompt、issue 正文或 Git 仓库。

安装并配置 `glab`：

```bash
brew install glab
glab auth login --hostname gitlab.specwire.lan
```

### 4.6 阶段门

- 浏览器登录成功。
- SSH clone/push 成功。
- `glab` 能创建测试 MR。
- Agent 身份 push main 被拒绝。
- Maintainer 可以发布受控 main 提交。

## 5. 阶段 2：部署 Multica

### 5.1 启动自托管服务

```bash
cd /Volumes/d/playground/specwire-infra
git clone https://github.com/multica-ai/multica.git
cd multica
git checkout <固定发布标签>
make selfhost
```

`make selfhost` 会生成 `.env`、数据库密码、JWT secret、VCS 加密 key，并启动 PostgreSQL、backend 和 frontend。

验证：

```bash
docker compose -f docker-compose.selfhost.yml ps
curl -fsS http://localhost:8080/readyz
```

预期数据库和 migrations 均为 `ok`。

### 5.2 配置可访问 URL

同机本地测试可使用 `http://localhost:3000`。GitLab 与 Multica 跨主机时，在 Multica `.env` 设置：

```dotenv
FRONTEND_ORIGIN=https://multica.specwire.lan
MULTICA_APP_URL=https://multica.specwire.lan
MULTICA_PUBLIC_URL=https://multica-api.specwire.lan
MULTICA_VCS_INTEGRATION_ENABLED=true
MULTICA_VCS_SECRET_KEY=<由安装流程生成并安全保管>
```

Compose 默认只把 3000/8080 绑定到 loopback；跨主机必须通过支持 WebSocket 的 HTTPS reverse proxy 暴露，不能简单改成 `0.0.0.0` 后直接公开。

### 5.3 登录

打开 Multica App URL，输入邮箱获取验证码。未配置邮件时从 backend 日志读取：

```bash
docker compose -f docker-compose.selfhost.yml logs backend | grep "Verification code"
```

创建 workspace 和 project。

### 5.4 安装 CLI 与 daemon

```bash
brew install multica-ai/tap/multica
multica setup self-host
multica daemon status
```

跨主机：

```bash
multica setup self-host --server-url https://multica-api.specwire.lan --app-url https://multica.specwire.lan
```

daemon 继承启动用户的本机权限。生产环境应使用专用 Unix/macOS 用户、VM 或受限主机运行，不要让 Reconciler 使用个人全权限账户。

### 5.5 创建两个 Agent

| Agent | 工具 | 权限 |
|---|---|---|
| SpecWire Implementer | Codex | 读 main、推 `specwire/*`、建 MR；不能推 main |
| SpecWire Reconciler | Codex | 读 GitLab、写 Multica 卡片；不能 commit、push 或 merge |

先手工创建普通 issue，指派给 Implementer，确认：

- daemon 在线。
- Codex 能启动。
- issue timeline 收到 Agent 回复。
- Agent 能访问预期工作目录，但不能越权 push main。

### 5.6 阶段门

- `/readyz` 正常。
- daemon 显示 running、workspace 数量大于 0。
- 两个 Agent 均可被选中。
- 手工 issue 能完成一次最小 Codex run。

## 6. 阶段 3：连接 Multica 原生 GitLab VCS

这条通道只处理 MR 与 Pipeline，不负责 SpecWire main push：

```text
GitLab Merge Request / Pipeline
  → Multica native VCS webhook
  → MR/CI 关联到 MUL issue
```

### 6.1 Multica 侧

1. Settings → Integrations → Git providers。
2. 选择 GitLab。
3. 填写 GitLab instance URL。
4. 填写 `read_api` token。
5. 连接后复制 webhook URL 和只显示一次的 webhook secret。

### 6.2 GitLab 侧

Project → Settings → Webhooks：

- URL：Multica 生成的 VCS webhook URL。
- Secret token：Multica 生成的 secret。
- 开启 Merge request events。
- 开启 Pipeline events。

### 6.3 联调

1. 手工创建一张测试卡，例如 `MUL-123`。
2. 创建一个 branch 或 MR，标题/正文包含 `MUL-123`。
3. 验证 MR 出现在卡片侧栏。
4. 跑一次测试 pipeline，验证 CI 状态被镜像。
5. MR 中禁止使用 `Closes`、`Fixes`、`Resolves MUL-123`。

官方说明：[Multica self-hosted Git providers](https://multica.ai/docs/vcs-integration)。

### 6.4 阶段门

- 合法 webhook 返回成功。
- 错误 secret 被拒绝。
- MR 与 pipeline 能关联卡片。
- 普通合并不会绕过 SpecWire verify 语义提前结束 change。

## 7. 阶段 4：初始化 SpecWire 仓库

### 7.1 目标结构

```text
.specwire/
  config.yaml
openspec/
  changes/
    archive/
packages/
  specwire-core/
  specwire-reconciler/
  specwire-webhook/
vendor/
  dsh-spec-loop/
assets/
idea.v3.md
IMPLEMENTATION_GUIDE.md
```

### 7.2 初始化

```bash
cd /Volumes/d/playground/specwire
git init -b main
git remote add origin ssh://git@gitlab.specwire.lan:2424/<group>/specwire.git
mkdir -p .specwire openspec/changes/archive packages/specwire-core packages/specwire-reconciler packages/specwire-webhook
touch openspec/changes/archive/.gitkeep packages/specwire-core/.gitkeep packages/specwire-reconciler/.gitkeep packages/specwire-webhook/.gitkeep
```

`vendor/dsh-spec-loop/` 在导入实际 fork 时再创建，避免用空目录占位后影响 clone 或 subtree 操作。

生成一次性 repository UUID：

```bash
specwire_repo_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
printf '%s\n' "$specwire_repo_id"
```

`.specwire/config.yaml`：

```yaml
schema_version: 1
repository_id: "<上一步生成的 UUID>"
main_branch: main
remote: origin
multica_project: "<Multica project ID 或稳定名称>"
default_assignee: "<Implementer agent slug>"
push_mode: auto
```

首次提交只包含项目骨架、设计文档和图片：

```bash
git add .specwire openspec packages assets idea.v3.md IMPLEMENTATION_GUIDE.md
git commit -m "chore: bootstrap SpecWire v3"
git push -u origin main
```

### 7.3 阶段门

- GitLab main 能看到初始提交。
- repository_id 已冻结。
- 仓库不包含 token、密码或本机私密配置。
- Agent 能 clone，但不能 push main。

## 8. 阶段 5：实现 Spec State 模块

建议先实现独立 CLI/包，再接 dsh。不要把状态逻辑直接散落到各个 `/spec` 命令。

### 8.1 最小接口

```text
specwire inspect [<change-id>|--all] --json
specwire finalize-new <change-id> [--push]
specwire transition <change-id> approve|revise|retry|verify|archive [--push]
specwire finalize-implementation <change-id> --generation N
specwire validate-mr --base <ref> --head <ref>
```

### 8.2 内部职责

```text
packages/specwire-core/
  src/
    schema/
    state/
    contract-hash/
    transitions/
    git-publisher/
    cli/
  test/
    fixtures/
    integration/
```

模块必须独占：

- `state.yaml` schema 校验。
- 合法状态迁移。
- generation 单调递增。
- 契约文件规范化与 SHA-256。
- 当前分支、main 与 `origin/main` 一致性检查。
- 精确暂存目标 change；禁止 `git add -A`。
- commit message。
- 单 ref、非 force push。
- `NO_CHANGE`、`LOCAL_COMMITTED`、`PUSHED`、`REMOTE_PENDING` 结果。

### 8.3 必测场景

1. new finalize 只生成一次 proposed commit。
2. proposed → approved，generation 从 0 到 1。
3. retry 保留 hash、generation + 1。
4. revise 回到 proposed 并清空批准 hash。
5. 非法 phase 跳转被拒绝。
6. unrelated staged/unstaged 文件不进入提交。
7. push 失败保留一个本地 commit；重试不再 commit。
8. tasks 只改变 checkbox 时 hash 不变。
9. 修改 proposal、任务文本或 spec delta 时 hash 改变。
10. stale generation 的 implementation finalize 被拒绝。
11. archive 在一次提交中完成状态更新和目录移动。

### 8.4 接入 dsh-spec-loop fork

- `/spec new` 只启动生成；生成完成后调用 `finalize-new`。
- approve、revise、retry、verify、archive 全部调用统一 transition 接口。
- session projection 可以保留为 UI 缓存，但不得作为权限或阶段权威。
- 生成中断时允许显式重跑 finalize，不重复生成内容。

### 8.5 阶段门

- 单元测试通过。
- 临时 Git 仓库集成测试通过。
- 跨新会话仍能读取 approved。
- 破坏性和越权 Git 操作均被测试阻止。

## 9. 阶段 6：实现 Reconciler 模块

### 9.1 接口

```text
specwire reconcile --dry-run
specwire reconcile --apply
```

建议把计划与副作用分开：

```text
load snapshot
  → observe GitLab + Multica
  → plan deterministic actions
  → print/audit plan
  → apply through adapters
```

### 9.2 输入

- 远端 main 的 commit SHA。
- active 与 archive changes。
- GitLab branches、MR、pipeline。
- Multica issues、metadata、active runs。
- `.specwire/config.yaml`。

Reconciler 必须使用隔离 checkout/cache，不得复用 Implementer 正在修改的工作树。

### 9.3 稳定键

```text
specwire:<repository_id>:<change_id>
```

卡片标题严格为：

```text
[SpecWire:<repository-id>:<change-id>]
```

人类摘要放 description，不改稳定标题。搜索必须：

- 限定 Multica project。
- 包含 done/cancelled。
- 对完整标题或 `specwire.key` metadata 精确匹配。
- 并发 create 冲突时重新搜索并接管现有卡。

### 9.4 状态优先级

| 优先级 | Git 与外部观察 | Multica 目标状态 |
|---|---|---|
| 1 | verified 或 archived | done |
| 2 | proposed | backlog |
| 3 | implemented | in_review |
| 4 | approved + 同 generation MR + CI failed | blocked |
| 5 | approved + 同 generation open MR | in_review |
| 6 | approved + blocked/cancelled 且 generation 未变 | 保持 |
| 7 | approved + active run 或确定性 branch | in_progress |
| 8 | approved | todo；仅新 generation 派发 |

不同 generation 的 branch、MR、run 一律标记 stale，不参与当前状态判定。

### 9.5 可恢复派发

仅当 Git generation 大于卡片 metadata generation：

1. `--no-start` 归一为 backlog。
2. 写 generation 和 contract hash metadata。
3. `--no-start` 设置 assignee。
4. 最后 backlog → todo，产生唯一启动边沿。

同 generation 的状态修复全部使用 `--no-start`，不得通过周期调和暗中重跑。

### 9.6 先做手工验证

在接入 Autopilot 前，至少运行：

```bash
specwire reconcile --dry-run
specwire reconcile --apply
specwire reconcile --apply
specwire reconcile --apply
```

第二、三次应收敛为 no-op。

### 9.7 阶段门

- 重复运行不重复建卡或派发。
- proposed 永不启动 Agent。
- open MR 不会被周期调和回退为 todo。
- blocked/cancelled 同 generation 不会自动复活。
- Reconciler 没有 Git commit、push、merge 凭据。

## 10. 阶段 7：创建 run-only Autopilot

在 Multica UI 创建：

| 配置 | 值 |
|---|---|
| Name | `SpecWire Reconciler` |
| Agent | SpecWire Reconciler |
| Execution mode | `run_only` |
| Schedule | `*/15 * * * *` |
| Timezone | `Asia/Shanghai` |

推荐 Prompt：

```text
在隔离 checkout 中执行 specwire reconcile --apply。
不得修改、提交、push 或 merge Git。
不得创建稳定键以外的卡片。
输出 main commit、计划动作、执行结果和错误的 JSON 摘要。
任何凭据或 webhook URL 均不得输出到回复。
```

先点击 Run now，验证一次；再观察两个 schedule 周期。当前 Multica Autopilot 的 schedule 与 webhook 均可触发 run-only task，运行记录位于 Autopilot history。[官方说明](https://multica.ai/docs/autopilots)。

阶段门：

- 手动触发成功。
- schedule 成功。
- Reconciler 输出同一 schema 的结果。
- 运行失败可在 history 中发现。

## 11. 阶段 8：实现 Webhook Adapter

### 11.1 两条 webhook 必须分开

```text
MR / Pipeline
  → Multica native VCS webhook

main push
  → SpecWire Webhook Adapter
  → Multica generic Autopilot webhook
  → Reconciler
```

### 11.2 Adapter 接口

```text
POST /gitlab/specwire
```

运行时配置：

```dotenv
GITLAB_WEBHOOK_SECRET=<secret>
GITLAB_ALLOWED_PROJECT_IDS=<逗号分隔 ID>
MULTICA_AUTOPILOT_WEBHOOK_URL=<bearer URL>
MULTICA_AUTOPILOT_HMAC_SECRET=<内部 HMAC secret>
MAX_BODY_BYTES=262144
```

配置通过 secret store、Docker secret 或部署环境注入，不写进仓库。

### 11.3 请求处理顺序

1. 先按字节数限制读取 raw body。
2. 常量时间校验 `X-Gitlab-Token`，或校验部署所选的 GitLab signing token。
3. 解析并校验 project allowlist。
4. 只接受配置的 push/MR 事件；push 业务上只关心 main。
5. 保留 GitLab `Idempotency-Key`。
6. 对**实际转发的原始 body**生成 Multica 要求的 `X-Hub-Signature-256: sha256=<hex>`。
7. 转发到 Multica Autopilot webhook URL。
8. 返回 Multica admission 结果，不等待 Reconciler 完成。

不得记录完整 bearer URL、secret、Authorization header 或可能含敏感信息的原始 payload。

### 11.4 GitLab webhook

Project → Settings → Webhooks，新建第二条 webhook：

- URL：Adapter URL。
- Secret token：`GITLAB_WEBHOOK_SECRET`。
- 开启 Push events。
- 可过滤 main 以减少噪音。
- 可选开启 MR events，以缩短 in_review 校准延迟。

分支过滤不能修复 GitLab 因多 ref push 而根本未生成的事件，schedule 仍是必需兜底。

### 11.5 安全与幂等测试

1. 正确 secret → accepted。
2. 错误/缺失 secret → 401 或 403。
3. 非 allowlist project → 403。
4. 超限 payload → 413。
5. 同一 `Idempotency-Key` 重放三次 → 一个 Autopilot run。
6. 不同 delivery、同一仓库状态 → Reconciler 结果收敛。
7. Adapter/Multica 暂时离线 → 恢复后 schedule 补齐。

### 11.6 阶段门

- 外部 GitLab 无法直接访问 Multica 内部 generic webhook URL。
- Adapter 无数据库、无 Git 写权限、无 Multica issue 业务逻辑。
- 两条 webhook 的 secret、URL 和事件范围分别记录。

## 12. 阶段 9：GitLab CI 门禁

### 12.1 Runner

- 使用专用 Runner。
- Runner 不持有 main push 或生产部署凭据。
- 对 image、Runner 和执行器版本做固定。
- PoC 不需要 Docker-in-Docker。

### 12.2 最小 pipeline

```yaml
stages:
  - specwire
  - test

specwire_contract:
  stage: specwire
  image: node:<固定版本>
  script:
    - npm ci
    - npm run build
    - npm run specwire:validate-mr
  rules:
    - if: '$CI_PIPELINE_SOURCE == "merge_request_event"'

project_tests:
  stage: test
  image: node:<固定版本>
  script:
    - npm ci
    - npm test
```

具体命令应在包结构确定后替换，不保留不可执行占位符进入正式 main。

### 12.3 Validator 必须检查

- source branch = `specwire/<change-id>-g<N>`。
- base main phase = approved(N)。
- head phase = implemented(N)。
- generation 与批准 hash 未变。
- 当前文件重新计算 hash 通过。
- tasks 全勾。
- MR 未修改其他 change 的 `state.yaml`。
- stale generation 被拒绝。
- 项目测试通过。

### 12.4 合并策略

- required pipeline 必须通过。
- PoC 阶段人工合并。
- MR 不使用 closing keywords。
- 合并后立即触发调和，卡片应处于 in_review。
- 未完成完整 PoC 前不得启用 auto-merge。

## 13. 阶段 10：完整 PoC

选择一个只改极小文件的 change，例如 `specwire-poc-hello`。

### 13.1 正常路径

| 步骤 | 操作 | Git 预期 | Multica 预期 |
|---|---|---|---|
| 1 | `/spec new` + finalize | proposed，generation 0 | backlog |
| 2 | `/spec approve` | approved，generation 1 | todo → Agent run |
| 3 | Agent 开工 | main 不变；创建 `-g1` branch | in_progress |
| 4 | Agent finalize | branch 为 implemented(1) | in_progress |
| 5 | 创建 MR | main 仍 approved(1) | in_review |
| 6 | CI 通过并人工合并 | main 为 implemented(1) | in_review |
| 7 | `/spec verify` | verified(1) | done |
| 8 | `/spec archive` | archive 中 archived(1) | done |

### 13.2 故障注入

必须逐项验证：

1. 同一 webhook 重放三次。
2. 同一时刻并发触发三次。
3. approved(1) 执行中 revise/approve 得到 generation 2。
4. Agent 使用 generation 1 尝试 finalize。
5. MR 已打开时运行三次 schedule。
6. Agent 分支 push 触发 GitLab 事件。
7. Adapter 停止后 push main，再恢复 schedule。
8. Multica 停止后 push main，再恢复。
9. 多 ref push 没有 webhook。
10. 删除 Multica 卡片后执行全量调和。
11. push main 被拒绝或网络中断。
12. Git 工作树存在无关 staged/unstaged 修改。

结果必须满足 [idea.v3.md 的 PoC 验收标准](./idea.v3.md#poc-验收标准)。

### 13.3 Go / No-Go

只有以下全部成立才进入上线加固：

- 正常路径完整通过。
- 14 项设计验收全部通过。
- 没有重复卡片或重复副作用。
- stale generation 无法合并。
- done 只由 verified/archived 推导。
- 所有 secret 均可轮换且未进入 Git。

## 14. 阶段 11：上线加固

### 14.1 备份

- GitLab：配置、repositories、数据库、uploads 和 secrets。
- Multica：PostgreSQL、附件/持久卷、`.env` 中不可重建的 secret。
- Adapter：部署配置和 secret 标识，不把明文 secret 放入普通备份日志。
- 定期执行恢复演练，不能只验证备份命令退出码。

### 14.2 监控

- GitLab readiness 和磁盘。
- GitLab webhook 是否被临时/永久禁用。
- Multica `/readyz`。
- Multica Autopilot failed/skipped history。
- Reconciler 连续失败、运行时长和 no-op 比例。
- todo 长时间无 run、in_progress 无 branch、MR 长时间无 CI 等 stranded 状态。

### 14.3 升级

1. 阅读 GitLab 和 Multica 当前版本升级说明。
2. 备份。
3. 在副本/测试环境升级。
4. 固定新版本和 digest。
5. 验证数据库 migration。
6. 重跑最小 PoC。
7. 再升级生产。

### 14.4 回滚

- Spec State 和 Reconciler 发布必须可回滚到上一固定版本。
- schema 变更需要向后兼容读取或明确 migration。
- Adapter 新版本失败时，停用 push webhook；schedule 继续保证最终校准。
- 禁止通过 force push 回滚 Git 持久状态。

## 15. 执行记录模板

每个阶段追加一份记录：

```markdown
## YYYY-MM-DD 阶段 N

- 执行人/Agent：
- 输入版本：
- GitLab 版本：
- Multica 版本：
- SpecWire commit：
- 执行命令：
- 产物：
- 自动测试：
- 手工验收：
- 已知缺陷：
- 回滚点：
- 结论：PASS / FAIL / BLOCKED
```

## 16. 推荐的第一批实际任务

按以下顺序创建任务，不要并行跨阶段：

1. `infra/gitlab-compose`：生成 GitLab Compose、版本和健康检查说明。
2. `infra/multica-selfhost`：部署 Multica，验证 daemon + Codex。
3. `integration/multica-gitlab-vcs`：打通 MR/Pipeline 原生 webhook。
4. `core/state-schema`：实现 config/state schema 和解析器。
5. `core/transition-engine`：实现迁移与 generation。
6. `core/contract-hash`：实现规范化 hash。
7. `core/git-publisher`：实现精确 commit/push。
8. `integration/dsh-fork`：接入 `/spec`。
9. `reconciler/planner`：实现纯计划器。
10. `reconciler/adapters`：接 GitLab/Multica。
11. `webhook/adapter`：实现验证和转发。
12. `ci/specwire-validator`：实现 MR 门禁。
13. `poc/end-to-end`：执行完整 PoC 与故障注入。

第一项真正的工程实现应是 **Spec State schema + transition tests**；在 GitLab、Multica 各自独立验收前，不应开始自动化联调。
