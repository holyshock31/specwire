# workflow Specification（delta：v2 发布模型）

## ADDED Requirements

### Requirement: 发布动作由 GitLab Issue 事件承载

v2 发布动作 = 创建 GitLab Issue（labels 含 `change`），描述携带 `change_id`、`branch`、`branch_head_sha`。本地 skill 封装完整动作：创建 feat/fix 分支 → opsx:propose → commit/push 分支 → 创建 Issue。规格不再直接提交 main（v1 路径保留但标记 deprecated）。

#### Scenario: skill 发布后 Bridge 建卡

人执行本地发布 skill，GitLab Issue Hook 将事件送达 Bridge，Bridge 校验通过后按项目映射创建 Multica 卡，卡描述包含分支与冻结 SHA。

#### Scenario: 非 change 标签的 Issue 不触发建卡

GitLab Issue 无 `change` 标签（或事件非 opened），Bridge 忽略该事件，不建卡。

### Requirement: Agent 实现基线为建卡时的分支冻结 SHA

建卡时 Bridge 记录 `branch_head_sha` 并写入卡描述；Agent checkout 到该 SHA 开发（冻结语义，替代 v1 的 approved_commit_sha）。分支头后续前进不影响已建卡任务的基线。

#### Scenario: 分支推进不影响进行中任务

发布后分支继续提交，Agent 仍基于建卡时刻的冻结 SHA 开发，交付 MR 基于该基线。

### Requirement: 评审通过单层 MR 完成交付

Agent 完成实现后创建普通 MR（评审请求）并置 in_review；人打开 MR 评审，通过 GitLab approval 与合并权限把关后合并。Draft MR 仅作为实现中途展示的可选工具，不是交付流程的必需环节。

#### Scenario: Agent 交付普通 MR 等待评审

Agent 创建 MR（描述含 SpecWire-Change 与冻结 SHA），不自行合并、不自行转 ready；人评审后合并。

### Requirement: 归档同时关闭关联 GitLab Issue

`archived` push 事件到达时，Bridge 按 project+change_id 反查 `issue_links` 关联表，关闭该 change 的全部关联未关闭 GitLab Issue，并置 Multica 卡 done。无关联记录（v1 旧 change）时跳过关闭步骤（记 warn）。

#### Scenario: 归档后 Issue 与 Multica 卡同时闭环

人执行 archive 提交并 push，GitLab Issue 自动关闭、Multica 卡自动 done，无需人工操作任一投影。

#### Scenario: GitLab API 失败不阻塞投影闭环

关闭 Issue 的 API 调用失败时，Multica 卡仍置 done，错误记日志，可人工处理或重放。
