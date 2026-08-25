# v2-issue-publish-model

## Why

v1 发布模型要求规格直接提交 main（带 trailer 的 push），导致"谁能发布规格"与"谁能写代码"权限混在一起，且规格没有评审载体。v2 将发布动作改为创建 tag=change 的 GitLab Issue：规格在 feat/fix 分支上开发与评审，main 保持干净，权限回归 GitLab 原生模型。

## What Changes

- **发布载体切换**：发布动作从"main 上的 trailer push"变为"创建 GitLab Issue（labels: [change]）"；本地 skill 封装：建分支 → opsx:propose → commit/push → 创建 Issue（描述含 change_id / branch / branch_head_sha）
- **Bridge 新增 Issue Hook 处理**：`X-Gitlab-Event: Issue Hook`，校验 `opened` 事件 + `change` 标签，解析描述，按 D20 项目映射建 Multica 卡（描述含分支信息）
- **分支基线冻结**：建卡时记录 `branch_head_sha`，Agent checkout 到该冻结点开发（替代 v1 的 `approved_commit_sha` 语义）
- **归档闭环扩展**：`archived` push 事件除置 Multica 卡 done 外，按关联表关闭对应 GitLab Issue（新增 `issue_links` 表 + GitLab API client）
- **评审模型**：Agent 交付普通 MR（评审请求）→ 人 review → GitLab approval/合并权限把关；Draft MR 仅作可选展示工具
- **兼容**：v1 的 push 建卡路径保留并标记 deprecated；无关联记录的旧 change 归档仅置 done

## Capabilities

### New Capabilities

- `workflow`（`specs/workflow/spec.md` 已有现状需求；本次为**修改**，见下）

### Modified Capabilities

- `workflow`：增加 v2 发布/评审/归档闭环的需求（Issue 事件发布、分支基线、关闭 GitLab Issue）
- `bridge`：增加 Issue Hook 处理、issue_links 关联、GitLab API 关闭 Issue 的需求

## Impact

- **Bridge**（Go）：handler 增加 Issue Hook 分支；新增 `issue_links` 表（SQLite 迁移）；新增 GitLab API client（关闭 Issue）；配置新增 `SPECWIRE_GITLAB_TOKEN`/`SPECWIRE_GITLAB_URL`
- **本地 skill**（scripts/）：新增 v2 发布 skill（分支+propose+push+建 Issue）
- **Agent 侧**：Instructions 第 1 条与 specwire-workflow skill 调整为"按 issue 描述中的 branch_head_sha 开发"
- **Multica**：无平台改动（状态/分配为原生能力）
- **GitLab**：项目需配置 Issue Hook（tag=change 事件）+ 创建 Access Token（scope: issues）
