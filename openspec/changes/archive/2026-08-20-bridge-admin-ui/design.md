# Design: bridge-admin-ui

## Context

现状（见 proposal.md Why）：Bridge 配置靠手改 `.env` + GitLab 手工配置 hook；多项目需要多 signing token（GitLab API 拒绝跨 hook 复用，实测确认）；配置无可视化与审计。v2 发布模型已引入 Issue Hook 与 GitLab API 调用（gitlab.go 已有 HTTP 客户端模式）。

## Goals / Non-Goals

- Goals：多 token 验签；内嵌管理 API + 页面；hook 生命周期自动化（创建/轮换）；配置写回 `.env`。
- Non-Goals：不做配置热加载（保存后重启生效）；不做用户认证体系（本机 PoC，admin 路由仅监听本机或简单 token）；不做多租户。

## Design

### 1. 多 token 验签（config.go + handler.go）

```
SPECWIRE_WEBHOOK_SECRETS="whsec_A,whsec_B,..."   # 新配置（逗号分隔）
SPECWIRE_WEBHOOK_SECRET=whsec_X                  # 兼容：存在时并入列表（优先级低于 SECRETS）
Config.WebhookSecrets []string
verifySignature：遍历 secrets，任一与 header 签名匹配即通过
```

- 解析：`parseSecrets(s)`（逗号分隔 + trim，校验 whsec_ 前缀与 base64）
- 启动校验：列表至少一个有效；空列表 → 启动失败
- 测试：多 token 各自验签通过、任一不匹配 401、单值兼容

### 2. 配置管理 API（admin.go，`/admin/api/*`）

| 端点 | 方法 | 功能 |
|---|---|---|
| `/admin/api/config` | GET | 当前配置快照（项目 allowlist、映射、hook 状态、多 token 是否存在——**不返回 token 明文**） |
| `/admin/api/projects` | POST | 添加项目（gitlab path + multica project title/id）→ 校验（GitLab 项目存在、Multica project 存在）→ 更新 allowlist + 映射 |
| `/admin/api/projects/{path}` | DELETE | 移除项目（allowlist + 映射 + 可选删除 hook） |
| `/admin/api/hooks/{path}` | POST | 创建/更新项目 hook：生成 token → GitLab API 配置 → 写入 SECRETS |
| `/admin/api/hooks/{path}/rotate` | POST | token 轮换：新 token → 更新 hook + SECRETS |
| `/admin/api/apply` | POST | 将待保存变更写回 `.env`（原子替换目标键）+ 返回重启提示 |

- 安全：admin 路由绑定 `127.0.0.1` 额外端口或要求 `X-Admin-Token`（env `SPECWIRE_ADMIN_TOKEN`，PoC 默认本机可用）
- GitLab API 编排复用 gitlab.go 的 HTTP 模式（新增 CreateHook/UpdateHook/ListHooks）

### 3. token 生成与存储

- 生成：`whsec_` + `crypto/rand` 32 字节 → base64（与 GitLab 校验一致）
- 存储：追加进 `.env` 的 `SPECWIRE_WEBHOOK_SECRETS`（保留其余行与注释）；`/admin/api/apply` 做原子写（临时文件 + rename，沿用 settings 原子写入思路）

### 4. 管理页面（静态 HTML + fetch）

- 单页：项目表（路径/映射/hook 状态/操作按钮）、添加项目表单、配置总览、重启提示条
- 无构建链：`admin/static/index.html` 嵌入二进制（go:embed），零前端依赖

### 5. 配置结构与启动

- 启动时：LoadConfig 解析 SECRETS/SECRET → 校验 → 快照供 admin API 读取
- 页面"保存"流程：修改内存态 → apply 写 `.env` → 提示 `docker compose up -d`（recreate 生效）

## Migration / Compatibility

- `SPECWIRE_WEBHOOK_SECRET` 继续生效（并入列表）；新配置优先
- 现有 `.env` 无需迁移（页面首次打开时提示统一到 SECRETS 可选）
- 现有 hook 不受影响（页面接管后按项目重建/复用）

## Open Questions

- admin 页面是否需要简单认证（本机 PoC vs 未来暴露）——默认 `SPECWIRE_ADMIN_TOKEN` 可选，未配置仅限本机回环访问
- GitLab 项目 Access Token 的 scope 需求：hook 编排需要 `api`（或至少 webhook 管理 scope）——与归档用 token 可合并为同一个 PAT
