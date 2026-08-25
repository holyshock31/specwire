# Design: v2-issue-publish-model

## Context

现状：Bridge 只处理 Push Hook（v1，proposal-ready 建卡 / archived 置 done），稳定键四段式（D21），项目映射 D20，状态/分配由发布者 trailer 指定（D23）。Agent Instructions 第 1 条基于 `approved_commit_sha` checkout。发布工具为 `scripts/publish.sh`（main 直推）。

目标（见 proposal.md）：发布载体切换为 GitLab Issue（tag=change），规格留在分支，归档闭环扩展到关闭 GitLab Issue。

## Goals / Non-Goals

- Goals：Issue Hook 建卡（含分支信息与冻结 SHA）；`issue_links` 关联表；归档关闭 GitLab Issue；v1 兼容 deprecated。
- Non-Goals：不做规格分支与 main 的分支策略改造（change 随实现 MR 进 main）；不做 Multica 平台改动；不做通知系统。

## Design

### 1. Issue Hook 事件处理（Bridge）

```
X-Gitlab-Event: Issue Hook
  → 校验 object_kind=issue、event=opened、labels 含 change
  → 解析 description（change_id / branch / branch_head_sha；缺字段 → ignored）
  → 解析 SpecWire-Status / SpecWire-Assignee（D23 语义延续；Status 非 backlog/todo → ignored）
  → D20 项目映射 → 建 Multica 卡（描述含分支信息；状态/分配按解析结果）
  → 写 issue_links（主键 gitlab_project+issue_iid，重放幂等）
```

GitLab Issue Hook payload：`object_attributes.iid`、`object_attributes.labels`（name 数组）、`object_attributes.description`、`project.path_with_namespace`。校验失败/非目标事件一律 200 ignored（不重试语义与 push 一致）。description 字段解析复用 v1 trailer 的行解析逻辑（`SpecWire-Status:` / `SpecWire-Assignee:` 前缀匹配），保证两模型语义一致。

### 2. issue_links 表（SQLite 迁移）

```sql
CREATE TABLE IF NOT EXISTS issue_links (
  gitlab_project TEXT NOT NULL,
  issue_iid      INTEGER NOT NULL,
  change_id      TEXT NOT NULL,
  branch         TEXT NOT NULL,
  branch_head_sha TEXT,
  created_at     TEXT NOT NULL,
  PRIMARY KEY (gitlab_project, issue_iid)
);
```

与 `events` 表并列；迁移沿用 OpenStore 的 PRAGMA 检查模式。

### 3. 归档闭环扩展（GitLab API）

- 新增 `gitlab.go`：`CloseIssue(ctx, project, iid)` → `PUT /api/v4/projects/{url-encoded-path}/issues/{iid}?state_event=close`，Bearer token，进程组超时模式（同 multica.go）
- 归档流程：置 done（D17 现有）→ 反查 issue_links 关闭全部关联未关闭 Issue（循环 CloseIssue）
- 失败语义：关闭失败记 error 不阻塞 done；token 未配置跳过 + warn

### 4. 本地发布 skill（scripts/publish-v2.sh）

```
1. git checkout -b feat/<change-id>（基于最新 main）
2. opsx:propose <change-id>（生成骨架后由人填写）
3. git add 精确暂存 change 目录 → commit → push 分支
4. 创建 GitLab Issue（glab 或 API）：labels=[change]，描述含 change_id/branch/branch_head_sha
```

依赖：`glab` CLI 或直接 GitLab API（token 复用 SPECWIRE_GITLAB_TOKEN）。分支命名约定 `feat/<change-id>`。

### 5. Agent 侧调整

- Instructions 第 1 条：`multica repo checkout <repo> --ref <branch_head_sha>`（v2 卡）或 `--ref <approved_commit_sha>`（v1 卡，兼容）
- specwire-workflow skill：增加 v2 发布流程与分支基线说明

### 6. 配置

| 变量 | 必填 | 说明 |
|---|---|---|
| `SPECWIRE_GITLAB_TOKEN` | 否 | scope 至少 issues；缺省跳过关 Issue |
| `SPECWIRE_GITLAB_URL` | 否 | 默认 `http://gitlab.specwire.local:8929` |

## Migration / Compatibility

- `issue_links` 新表，CREATE IF NOT EXISTS，无破坏
- v1 push 建卡路径保留，建卡成功日志加 `deprecated` 标记
- 旧 change 无关联记录：归档跳过关 Issue + warn
- GitLab 侧新增：项目 Issue Hook 配置（tag 过滤可在 Bridge 侧做，Hook 全量投递）

## Open Questions

- Issue Hook 的 GitLab 投递（是否带 signing token header？签名验证与 push 一致需实测确认）
- skill 创建 Issue 用 glab 还是直接 API（取决于本机是否安装 glab）
