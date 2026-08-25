# SpecWire 后续要求（多项目与平台演进）

> 文档定位：本文是平台路线图、运维要求和历史验证记录，不是当前行为契约；当前行为以 `openspec/specs/` 为准。任何需要落地的条目都必须先建立并评审 OpenSpec change，再进入实现。
>
> 起草背景：2026-08-24，以 `personal/webdeck`（unify-topbar-app-icon）跑通发起 → 实现 → 评审 → 合并 → 归档全链路后的沉淀。本仓库的客户端流程由独立 SpecWire Skills 管理，Bridge 只消费其发布与归档协议。

## 0. 现状基准（截至 2026-08-25，需以运行环境复核）

- Bridge 已支持多项目：`SPECWIRE_ALLOWED_PROJECTS`（白名单）+ `SPECWIRE_PROJECT_MAP`（GitLab path → Multica project ID）+ `SPECWIRE_WEBHOOK_SECRETS`（多值轮换）；状态存 SQLite（`issue_links` / `events`，均带 project 字段）
- 当前凭证：`specwire-bridge` 组级 Access Token（`personal` 组，scope=api）——具体有效期和覆盖范围以 GitLab/API 实际查询为准
- 当前实测：`personal/webdeck → Multica 59f6006a-…`（WebDeck）；`specwire/specwire-poc → 3e7d61cd-…` 是历史 PoC 映射，不作为当前 GitLab 项目存在性的事实
- 已知缺口：归档时 Bridge 关 GitLab Issue 曾因 token 被轮换而 401（已修复：token 更新 + 手动补关 Issue #6）

## 1. 多项目接入要求（近期执行）

### 1.1 推荐形态：项目收敛到组 + 组级 token（零平台改造）

- 新纳管项目**优先建在 `personal` 组**（含子组）：现有 `specwire-bridge` 组级 token 天然覆盖，**无需新建 token**
- 若项目归属其他顶层组：为该组另建组级 token，**并**（受 Bridge 单 token 限制，见 2.1）评估升级或收敛
- 禁止用全局 PAT（最小权限原则）

### 1.2 新增项目标准 Runbook

1. GitLab：项目建到目标组（默认 `personal`）
2. Multica：创建对应项目，记录 project UUID
3. Bridge 配置（**走 admin UI 保存**，勿手改 env）：
   - `ALLOWED_PROJECTS` += `personal/<新项目>`
   - `PROJECT_MAP` += `personal/<新项目>:<multica 项目 id>`
   - 应用后 `docker compose up -d`
4. GitLab 新项目设置 → Webhooks → `http://gitlab.specwire.local:8787/gitlab/specwire` + 复用现有 `whsec_…`（内网可共用；需隔离时在 `SPECWIRE_WEBHOOK_SECRETS` 追加新值并双值并存 48h 轮换）
5. 验收：以 `[verify] hook token check`（Issue #4 类）或发一个 test change 验证 Bridge 建卡与归档关 Issue

### 1.3 移除项目

- 从 `ALLOWED_PROJECTS` / `PROJECT_MAP` 移除 → 应用 → 验证 `events` 不再新增该项目记录；GitLab webhook 删除

## 2. 平台演进要求（中期）

### 2.1 Bridge per-project 凭证（最高优先级演进）

现状：单一 `SPECWIRE_GITLAB_TOKEN` 全局共享 —— 跨多顶层组时与"最小权限"冲突。

- **要求**：`config` 支持按项目配置 GitLab 凭证（token / secret），存储进现有 SQLite（新增 config 表）或 `data/bridge.env` 扩展；admin UI 写回机制同步支持
- **验收标准**：同一 Bridge 实例可同时纳管 `personal` 组与另一个组，各用各自组级 token；验证单一项目 token 无法访问他项目数据
- 过渡期：若不得不同管多组，可临时全局 PAT，但必须记录例外并尽快迁移

### 2.2 可观测性

- 已有：`events` 审计表（含 project/change_id/delivery_key）、`SPECWIRE_LOG_LEVEL`
- 要求：归档/关闭 Issue 失败（如 401）时告警可感知（日志等级提升或指标）；`archived` 事件的幂等重放（同一 delivery 不重复建卡）

### 2.3 备份与运维基线

- **GitLab**：`gitlab-backup create` + `gitlab-ctl backup-etc`（勿裸 cp data 卷，WAL 一致性）；备份文件从 `/var/opt/gitlab/backups` 拷出
- **Bridge**：`sqlite3 data/specwire-bridge.db ".backup <路径>"`（含 WAL，勿直拷 .db-wal）；`.env`/`data/bridge.env` 随 `data/` 卷备份
- **token 轮换双端流程**：① GitLab 网页/API 建新 token ② Bridge 侧替换 `.env` 并 `docker compose up -d` ③ 本机 glab `auth login --hostname … --token <新>` ④ 双值 secret 同理（48h 并存后移除旧值）
- 记录：每次轮换在 `docs/` 追加"凭证轮换日志"（token 本体不入文档）

## 3. 安全与合规要求（持续）

- **最低权限**：组级 token 优于全局；项目级仅用于单项目场景
- **token 保密**：不得出现在聊天、终端历史、日志；已暴露的 token（本次会话出现过）**应立即视为已泄露并在 24h 内轮换**，本仓库文档不记录任何 token 值
- **webhook secret**：保持双值轮换习惯；每项目可独立 secret（见 2.1 演进后可落实）
- **管理面板**：`SPECWIRE_ADMIN_TOKEN` 限本机使用；admin API 不暴露对外

## 4. 平台自身流程要求（dogfood）

- Bridge / specwire 平台改动**必须走 openspec v2 发布模型**（本仓库 openspec/ 目录 + 四技能管线），不允许直接改 main
- 本要求文档本身纳入 openspec 变更管理（如前述升级项 2.1 立 `upgrade-bridge-per-project-credentials` 变更）

## 5. 待办映射（建议下一批变更）

| 编号 | 项 | 优先级 | 载体建议 |
|---|---|---|---|
| F-1 | Bridge per-project 凭证（2.1） | 高 | specwire 仓库 openspec change |
| F-2 | archived 幂等与失败告警（2.2） | 中 | 同上（可与 F-1 合并） |
| F-3 | 备份基线脚本化（2.3） | 中 | scripts/ 下 cron 脚本 + 文档 |
| F-4 | 凭证轮换 SOP 文档（2.3/3） | 中 | docs/ 独立 SOP |
| F-5 | 多项目管理 Runbook 自动化（1.2 脚本化） | 低 | skill（类似 init/review/merge/archive 的 specwire 侧 skill） |
