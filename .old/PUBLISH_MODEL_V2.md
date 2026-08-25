# SpecWire 发布模型 v2：Issue 事件驱动的发布/开发 + Push 事件驱动的归档

> 状态：**设计草案（Phase 1）**，待评审
> 日期：2026-08-19
> 关联：[SPECWIRE_BRIDGE_DESIGN.md](./SPECWIRE_BRIDGE_DESIGN.md)（v1 push 模型，D1–D23）、[HANDOFF.md](./HANDOFF.md)

## 1. 背景与动机

v1（push 模型）要求规格直接提交 main 并携带 trailer——发布即 push main，带来两个问题：

1. **权限模型回到"谁能 push main"**：规格发布与代码写权限混在一起，团队化后难以分级。
2. **规格无评审载体**：change 直接进 main，评审只能事后（backlog 阶段）进行。

v2 把"发布动作"从 **main 上的 push** 改为 **GitLab Issue（tag: change）**：规格在 feat/fix 分支上开发与评审，main 保持干净；发布=创建带标记的 Issue，权限回归"谁能建 Issue/合并 MR"。

## 2. 事件模型总览：两套 Hook 分工

```text
┌─ Issue Hook（新增，发布/建卡）────────────────────────────┐
│  本地 skill → 创建 GitLab Issue（labels: [change]）       │
│  → GitLab → Bridge（X-Gitlab-Event: Issue Hook）          │
│  → 校验 tag=change + opened 事件                          │
│  → 解析 description（change_id / branch / head sha）      │
│  → 项目映射（D20）→ 建 Multica 卡（含分支信息）            │
└──────────────────────────────────────────────────────────┘

┌─ Push Hook（现有，归档/置 Done）──────────────────────────┐
│  人在 main 上 openspec archive → 提交（archived trailer）  │
│  → push main → Bridge（X-Gitlab-Event: Push Hook）        │
│  → 按 project+change_id 匹配                              │
│  → ① 关闭对应 GitLab Issue（v2 新增，GitLab API）         │
│  → ② 置 Multica 卡 done（D17 现有）                       │
└──────────────────────────────────────────────────────────┘
```

## 3. 完整生命周期

### 3.1 发布（本地 skill）

```
本地 skill（一条命令）：
  1. 创建 feat/fix 分支（git checkout -b feat/<change-id>）
  2. opsx:propose 生成 change（openspec/changes/<change-id>/）
  3. commit + push 分支
  4. 创建 GitLab Issue：
     - title: [change] <change-id>
     - labels: change
     - description: change_id / branch / branch_head_sha
```

### 3.2 建卡（Bridge，Issue Hook）

- 校验：`X-Gitlab-Event: Issue Hook`、事件 `opened`、`labels` 含 `change`
- 解析 description 的 `change_id` / `branch` / `branch_head_sha`
- 项目映射（复用 D20 `SPECWIRE_PROJECT_MAP`）
- 建 Multica 卡：title `[SpecWire] <change_id>`，description 含分支信息
- **记录关联**（新增 `issue_links` 表）：

```sql
CREATE TABLE IF NOT EXISTS issue_links (
  gitlab_project TEXT NOT NULL,   -- personal/webdeck
  issue_iid      INTEGER NOT NULL,
  change_id      TEXT NOT NULL,
  branch         TEXT NOT NULL,
  branch_head_sha TEXT,
  created_at     TEXT NOT NULL,
  PRIMARY KEY (gitlab_project, issue_iid)
);
```

### 3.3 开发（Agent）

- Agent 读卡：`branch` / `branch_head_sha` → checkout 该分支/SHA 开发（Instructions 第 1 条调整）
- 完成后 Draft MR → main（人 review → 转 ready → 合并）
- **change 文档随实现 MR 一起进 main**（用户决策）

### 3.4 归档（人，main 上）

```
git checkout main && git pull
openspec archive <change_id>        # 主 specs 自动合并
git add openspec && git commit -m "spec(...): archive completed change

SpecWire-Event: archived
SpecWire-Change: <change_id>"
git push origin main
```

Bridge（Push Hook archived）：
1. 按 project+change_id 查 `issue_links` → **关闭所有关联的未关闭 GitLab Issue**（GitLab API）
2. 按 project+change_id 置最新 created 的 Multica 卡 done（D17 现有）

## 4. 兼容与幂等

| 场景 | 行为 |
|---|---|
| v1 旧 change（无 issue_links 记录） | 归档时跳过关 Issue，仅置 done + warn 日志 |
| archived 重放 | 重复 close（GitLab 幂等）+ 重复置 done，无害 |
| GitLab API 失败 | 记 error，**不阻塞** Multica done；可人工关或重放 |
| `SPECWIRE_GITLAB_TOKEN` 未配置 | close 步骤跳过 + warn（向后兼容） |
| Issue Hook 重复投递 | `issue_links` 主键（project+iid）幂等 |

## 5. 新增配置

| 变量 | 必填 | 说明 |
|---|---|---|
| `SPECWIRE_GITLAB_TOKEN` | 否（缺省跳过关 Issue） | Project/Group Access Token，scope 最小 `issues`；经 `.env` 注入 |
| `SPECWIRE_GITLAB_URL` | 否 | 默认 `http://gitlab.specwire.local:8929`（specwire-net 内容器可达） |

## 6. 待决项（评审时敲定）

1. **分支基线**：建卡时冻结 `branch_head_sha`（Agent 开发该 SHA，规格再改=新发布）——推荐，与 D10 一致；或 Agent 动态跟分支头。
2. **关闭范围**：归档关闭该 change 的**全部**关联未关闭 Issue（推荐，语义完整）还是仅最新。
3. **Draft MR 约定**：Agent 交付 draft → 人 review 后转 ready；确认这个两段式。
4. **v1 push 模型去留**：发布侧完全迁移 v2（push 仅用于归档）；过渡期两套并存。

## 7. 实施阶段

| Phase | 内容 | 依赖 |
|---|---|---|
| 1（本文档） | v2 模型定稿 | 评审通过 |
| 2 | Bridge：`issue_links` 表 + Issue Hook 解析建卡 | 无 |
| 3 | Bridge：GitLab API client + 归档侧关 Issue | GitLab PAT |
| 4 | 本地发布 skill（分支+propose+push+建 issue）+ Instructions 调整 | Phase 2/3 |
