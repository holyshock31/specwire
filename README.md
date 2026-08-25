# SpecWire

SpecWire 是 **GitLab 与 Multica 之间的集成边界**（PoC）。OpenSpec、Agent、MR 和本地仓库操作由独立客户端 Skills 管理；Multica Issue/Run 是执行投影。

## 文档入口（OpenSpec 结构化）

```
openspec/
  specs/                        ← 主规格（当前已实现的行为契约）
    workflow/spec.md            ← SpecWire 可见的发布/完成集成生命周期
    bridge/spec.md              ← Bridge 组件：事件校验/判重/映射/闭环
    admin/spec.md               ← Bridge 控制面：项目映射、Hook、凭据、配置
  changes/                      ← 未完成变更提案（现状 → 目标的差异）
    archive/                    ← 已完成变更记录
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
| 旧文档 | `.old/` | 历史交接/设计文档归档（内容已被 openspec specs 取代） |

## 文档职责

- 当前行为以 `openspec/specs/` 为准；`openspec/changes/` 只描述尚未完成的变更，完成后移入 `openspec/changes/archive/`。
- `CONTEXT.md` 与 `docs/adr/` 分别维护领域边界/术语和已接受的架构理由。
- `docs/specwire-next-requirements.md` 是平台路线图与运维沉淀，不是当前行为契约；其中每项要落地的要求都必须先建立 OpenSpec change。
- 客户端 Skills 文档只定义如何消费 SpecWire 协议，不定义 Bridge 行为。

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
