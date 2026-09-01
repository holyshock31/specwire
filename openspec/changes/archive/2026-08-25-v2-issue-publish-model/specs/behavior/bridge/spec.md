# bridge Specification（delta：v2 发布模型）

## ADDED Requirements

### Requirement: Bridge 处理 GitLab Issue Hook 事件

Bridge 接收 `X-Gitlab-Event: Issue Hook`，仅处理事件 `opened` 且 labels 含 `change` 的 Issue；解析描述中的 `change_id` / `branch` / `branch_head_sha`，按 `SPECWIRE_PROJECT_MAP` 映射到 Multica project 建卡。描述可携带 `SpecWire-Status: todo` / `SpecWire-Assignee: <name>` 字段（与 v1 trailer 同构，D23 语义延续）：Status 非法值（非 backlog/todo）→ 事件视为无效。不符合条件的事件返回 200 ignored。

#### Scenario: 合法 change Issue 建卡成功

Issue（tag=change，描述完整）创建事件到达，Bridge 在对应 Multica project 建卡，卡描述含分支与冻结 SHA。

#### Scenario: 描述缺字段被忽略

change Issue 描述缺少 change_id 或 branch 字段，Bridge 视为无效事件忽略，不建卡。

#### Scenario: 描述指定直通模式

Issue 描述含 `SpecWire-Status: todo`，Bridge 建卡状态为 todo（发布者已批准），Agent 无需等待人工批准。

#### Scenario: 描述指定分配

Issue 描述含 `SpecWire-Assignee: SpecWire Dev`，Bridge 建卡时预分配。

#### Scenario: 非法状态值被拒绝

Issue 描述含 `SpecWire-Status: nonsense`，Bridge 将该事件视为无效并忽略，不建卡。

### Requirement: 记录 GitLab Issue 与 change 的关联

Bridge 建卡时在 `issue_links` 表记录（gitlab_project, issue_iid, change_id, branch, branch_head_sha），主键 (gitlab_project, issue_iid) 保证 Issue Hook 重放幂等。归档时按 project+change_id 反查。

#### Scenario: Issue Hook 重放不重复建卡

GitLab 重发同一 Issue 事件，关联记录命中，Bridge 返回 duplicate，不重复建卡。

### Requirement: 归档时通过 GitLab API 关闭关联 Issue

配置 `SPECWIRE_GITLAB_TOKEN`（scope 至少 issues）与 `SPECWIRE_GITLAB_URL` 后，archived push 事件按 `issue_links` 反查并关闭该 change 的全部关联未关闭 Issue；token 未配置或 API 失败时跳过关闭（记 warn/error），不阻塞 Multica 卡置 done。

#### Scenario: 未配置 token 时归档降级

`SPECWIRE_GITLAB_TOKEN` 未设置，归档仅置 Multica 卡 done，关闭 Issue 步骤跳过并记 warn。

### Requirement: v1 push 建卡路径保留但标记 deprecated

`proposal-ready` trailer 的 push 建卡路径继续可用（过渡期兼容），日志标记 deprecated；新发布以 Issue 模型为主。归档路径（`archived` trailer）为两模型共用，不受影响。

#### Scenario: v1 发布仍可建卡

发布者沿用 v1 方式（main 上 proposal-ready push），Bridge 建卡成功并输出 deprecated 提示日志。
