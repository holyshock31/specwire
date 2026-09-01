# SpecWire

SpecWire 是 **GitLab 与 Multica 之间的集成边界**（PoC）。OpenSpec、Agent、MR 和本地仓库操作由独立客户端 Skills 管理；Multica Issue/Run 是执行投影。

## 文档入口（OpenSpec 结构化）

```
openspec/
  drafts/                       ← 尚未形成正式 Change 的探索材料
  changes/                      ← 一次完整变更的设计、delta specs 和任务
    archive/                    ← 已完成变更及其原型/设计历史
  specs/                        ← 当前已接受、已发布的完整知识
    behavior/                   ← 功能行为契约
    domain/                     ← 领域术语和模型
    architecture/               ← 架构设计与 ADR
    experience/                 ← 产品体验和交互契约
```

```bash
openspec list                 # 查看所有 changes
openspec view                 # 交互式 dashboard（specs + changes）
openspec show <change-id>
openspec validate             # 校验全部
```

## 组件

| 组件 | 位置 | 说明 |
|---|---|---|
| Bridge | `bridge/` | Go，Docker Compose 部署（`docker compose up -d`），GitLab webhook → Multica |
| 客户端 Skills | 独立 SpecWire Skills 管理 | 负责发起、执行、评审、合并、归档；消费 SpecWire 协议，不定义 Bridge 行为 |

## 文档职责

- 当前知识以 `openspec/specs/` 下对应类型目录为准；`openspec/changes/` 保存尚未完成的变更，完成后移入 `openspec/changes/archive/`。
- `CONTEXT.md` 是领域文档入口；领域术语以 `openspec/specs/domain/` 为准，架构理由以 `openspec/specs/architecture/adr/` 为准。
- 尚未形成 Change 的路线图和设计草稿放在 `openspec/drafts/`；其中需要落地的条目必须先整理成 OpenSpec change。
- `docs/agents/` 只保存 Agent 和仓库操作规程，不属于产品设计草稿。
- 客户端 Skills 文档只定义如何消费 SpecWire 协议，不定义 Bridge 行为。

## GitHub Release

GitLab 是源码入口，GitHub 通过 mirror 接收分支和 tag。普通 `main` push 或 Pull Request 会触发 CI；发布版本时创建并推送 `v*` tag，mirror 将 tag 同步到 GitHub 后，Release workflow 会运行 Bridge 测试并自动创建 GitHub Release。

```bash
git tag v0.1.0
git push origin v0.1.0
```

Release 使用 GitHub 自动生成修改说明，并提供该 tag 的源码归档；当前项目没有 npm/Electron 类分发制品。

## 管理页面（bridge-admin-ui）

Bridge 内嵌配置管理页面：**http://localhost:8787/admin**（容器部署时经宿主端口访问）。

- **项目表**：GitLab 项目 / Multica 映射 / hook 状态 / 专属 token；支持添加项目（校验 GitLab 与 Multica 均存在）、移除项目、创建 hook、轮换 token。
- **创建/更新 hook**：自动生成专属 signing token（`whsec_` + 32 字节）并通过 GitLab API 配置 push+issues 事件；token 立即进入运行时验签列表，旧 token 在轮换后失效。
- **保存配置**：`/admin/api/apply` 把当前配置（allowlist / 映射 / tokens）原子写回 env 文件（临时文件 + rename），页面提示 **`docker compose up -d`** 重启生效（无热加载）。
- **安全**：`SPECWIRE_ADMIN_TOKEN` 配置后所有 `/admin/api/*` 要求 `X-Admin-Token` 头（页面首次 401 时提示输入并保存在本机浏览器）；未配置时仅回环地址可访问。
- **配置项**：
  - `SPECWIRE_WEBHOOK_SECRETS`：逗号分隔的 signing token 列表（旧单值 `SPECWIRE_WEBHOOK_SECRET` 兼容并入，apply 时统一迁移为 SECRETS）
  - `SPECWIRE_WEBHOOK_URL`：GitLab webhook 回调地址（hook 编排用，默认 `http://host.docker.internal:8787/gitlab/specwire`）
  - `SPECWIRE_ADMIN_ENV_PATH`：apply 写回的目标 env 文件（容器部署指向挂载卷内 overlay，见 compose.yaml）

## 快速操作

```bash
# Bridge 生命周期（bridge/ 目录下）
docker compose logs -f        # 看日志
docker compose restart bridge # 改 .env 后重启
docker compose build && docker compose up -d   # 升级

# 管理页面
open http://localhost:8787/admin

# 查看当前 change 状态
openspec status --change <change-id>
```
