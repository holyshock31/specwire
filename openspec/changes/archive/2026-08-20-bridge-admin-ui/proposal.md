# bridge-admin-ui

## Why

Bridge 的配置（项目 allowlist、GitLab↔Multica 项目映射、webhook secret、GitLab token）目前全部靠手改 `.env` + 手工在 GitLab 页面配置 hook，重复、易错、无审计、无可视化。多项目场景下每项目需要独立 signing token（GitLab API 拒绝复用 token），手工维护成本线性增长。

## What Changes

- **Bridge 多 token 验签**：`SPECWIRE_WEBHOOK_SECRET` 升级为 `SPECWIRE_WEBHOOK_SECRETS`（逗号分隔，向后兼容单值）；verifySignature 遍历列表任一匹配即通过
- **配置管理 API**（Bridge 内嵌，`/admin` 路由）：项目增删、项目映射（D20）查看/编辑、token 管理、hook 状态
- **GitLab Hook 自动编排**：API 为项目创建/更新 webhook（push+issues 事件、独立 signing token）；token 由页面生成（`whsec_` + 32 字节）
- **配置持久化**：写入 `.env`（保留现有加载机制），变更后提示重启生效
- **管理页面**：简单 Web UI（项目列表、映射配置、hook 状态、token 轮换、Bridge 状态）

## Capabilities

### New Capabilities

- `admin`（`specs/admin/spec.md`）：管理界面与配置 API 的行为契约

### Modified Capabilities

- `bridge`（`specs/bridge/spec.md`）：多 token 验签需求（BREAKING：配置项从单值升级为列表，旧值兼容）

## Impact

- **Bridge**：config.go（多 secret 解析）、handler.go（验签遍历）、新增 admin.go（API handler）+ 静态页面；测试更新
- **配置**：`.env` 新增 `SPECWIRE_WEBHOOK_SECRETS`（兼容旧 `SPECWIRE_WEBHOOK_SECRET`）
- **GitLab**：hook 由页面自动管理（替代手工配置）；项目 Access Token 仍需手动创建（scope: api）用于 hook 编排
- **文档**：README 增加管理页面入口说明
