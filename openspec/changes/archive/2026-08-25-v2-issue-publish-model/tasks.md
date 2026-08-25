# Tasks: v2-issue-publish-model

## 1. 前置验证（Open Questions）

- [x] 1.1 确认 GitLab Issue Hook 投递是否带 Standard Webhooks 签名 header（webhook-id/timestamp/signature）——实测：配置 Issue Hook 后触发一次事件，检查 Bridge 收到的 header；若与 push 一致则复用 verifySignature，否则调整验签分支
- [x] 1.2 确认本机是否有 `glab` CLI；无则 skill 直接调用 GitLab API（复用 SPECWIRE_GITLAB_TOKEN）

## 2. Bridge：Issue Hook 解析与建卡

- [x] 2.1 handler.go：新增 `gitlabIssuePayload` 结构体（object_kind/event/object_attributes{iid,labels,description}/project.path_with_namespace）
- [x] 2.2 ServeHTTP 增加 Issue Hook 分支：校验 object_kind=issue、event=opened、labels 含 change；非目标事件 200 ignored
- [x] 2.3 解析 description 的 change_id / branch / branch_head_sha；缺字段 → ignored；`SpecWire-Status` / `SpecWire-Assignee` 字段（与 v1 trailer 同构，D23）：Status 非 backlog/todo → ignored
- [x] 2.4 复用 handleProposal 的建卡流程（项目映射 D20、稳定键、description 生成），描述加入分支信息；状态/分配按解析结果（默认 backlog 未分配）
- [x] 2.5 建卡成功后写 issue_links（INSERT OR IGNORE，主键 gitlab_project+issue_iid）

## 3. Bridge：issue_links 表与迁移

- [x] 3.1 store.go：schema 增加 issue_links 表；OpenStore 迁移（PRAGMA 检查 + CREATE IF NOT EXISTS）
- [x] 3.2 store.go：InsertIssueLink / ListIssueLinks(project, changeID) / LinkExists 查询方法 + 单元测试

## 4. Bridge：归档关闭 GitLab Issue

- [x] 4.1 新增 gitlab.go：CloseIssue(ctx, project, iid)（PUT /api/v4/projects/{path}/issues/{iid}?state_event=close，Bearer token，进程组超时模式）
- [x] 4.2 config.go：SPECWIRE_GITLAB_TOKEN / SPECWIRE_GITLAB_URL（默认 http://gitlab.specwire.local:8929）
- [x] 4.3 handleArchived：置 done 后按 project+change_id 反查 issue_links，循环关闭全部关联未关闭 Issue；token 未配置跳过 + warn；API 失败记 error 不阻塞 done
- [x] 4.4 v1 建卡路径日志加 deprecated 标记

## 5. 测试

- [x] 5.1 handler_test：Issue Hook 建卡（合法/缺标签/缺字段/重放幂等）用例
- [x] 5.2 handler_test：archived 关闭 Issue（fake GitLab API 或 stub 注入）用例
- [x] 5.3 store_test：issue_links 插入/查询/幂等用例
- [x] 5.4 全量 go test -race 通过

## 6. 本地发布 skill

- [x] 6.1 scripts/publish-v2.sh：建分支 → opsx:propose → 精确暂存 commit → push 分支 → 创建 Issue（labels=[change]，描述含 change_id/branch/branch_head_sha）
- [x] 6.2 skill 支持 `--todo` / `--assignee <name>` 选项（写入 Issue description 的 SpecWire-Status / SpecWire-Assignee 字段，与 v1 一致）
- [x] 6.3 分支命名约定 feat/<change-id>；重复发布检查（分支已存在提示）

## 7. Agent 侧与文档

- [x] 7.1 Agent Instructions 第 1 条：优先 checkout 卡描述的 branch_head_sha（v2），兼容 approved_commit_sha（v1）
- [x] 7.2 specwire-workflow skill：增加 v2 发布流程与分支基线说明
- [x] 7.3 更新主 specs（archive 时自动合并 delta）与 HANDOFF 交接说明

## 8. 部署与联调

- [x] 8.1 重新 build 镜像 + 部署
- [x] 8.2 GitLab 配置：项目 Issue Hook（URL 同 push hook）+ Access Token（scope: issues）注入 .env
- [x] 8.3 端到端：skill 发布一个 change → 建卡（分支信息）→ Agent 开发 → MR → 合并 → archive → Issue 关闭 + 卡 done
