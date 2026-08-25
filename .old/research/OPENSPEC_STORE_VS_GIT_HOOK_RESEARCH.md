# OpenSpec Store 与 GitLab Push Webhook 方案调研

> 调研日期：2026-08-18  
> OpenSpec 基线：v1.9.0  
> Multica 核对基线：v0.4.26 及当前官方文档  
> 对照方案：[SPECWIRE_WORKFLOW_DECISION.md](./SPECWIRE_WORKFLOW_DECISION.md) 中的单仓库 SpecWire Workflow

## 1. 结论摘要

OpenSpec 确实存在正式命名为 **Stores** 的功能，但仍处于 beta。它的准确含义是：

> 一个独立的、只用于规划的 OpenSpec Git 仓库，加上本机按名称定位该仓库的注册机制。

它不是 OpenSpec 云服务、制品库、数据库、事件总线或任务系统，也不会自动执行 `clone`、`pull`、`push` 或 webhook。[OpenSpec Stores 用户指南](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/docs/stores-beta/user-guide.md#L1-L51)

因此，OpenSpec Store 与当前的“规格提交到 GitLab 后，由 GitLab push webhook 触发 Multica”不是替代关系，而是两个不同层次的能力：

```text
OpenSpec Store：决定 spec 放在哪里、如何从多个仓库引用
GitLab webhook：决定一个已发布 Git 版本如何触发外部工作流
```

二者可以组合，但 Store 不会替代 webhook。采用 Store 后，webhook 应配置在 Store 对应的 GitLab planning project 上，并且必须额外解决“这份 spec 应在哪个代码仓库实现”的映射问题。

对当前“单代码仓库、小团队 PoC”，建议继续把 `openspec/` 放在代码仓库内，保持现有 GitLab push webhook 方案。OpenSpec 官方也把仓库内规划视为默认方案，只在规划真正跨多个仓库或团队时建议使用 Store。[OpenSpec 团队工作流](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/docs/team-workflow.md#L66-L68)

另有一个会影响当前方案落地的重要事实：Multica v0.4.26 的 Autopilot `create_issue` 模式会把 issue 创建为 `todo` 并立即排队执行，不是 `backlog`。所以不能直接把 GitLab webhook 接到 `create_issue` Autopilot 来实现“先进入 backlog、人工批准后开工”；桥接逻辑必须显式创建 `status=backlog` 的 issue。[Multica v0.4.26 源码](https://github.com/multica-ai/multica/blob/v0.4.26/server/internal/service/autopilot.go#L614-L742)

## 2. OpenSpec Store 的准确含义

### 2.1 正式功能，但仍是 beta

OpenSpec v1.5.0 首次将 Stores 标为 very early beta；v1.9.0 的用户指南仍明确说明命令、参数、文件格式和 JSON 输出可能变化。[v1.5.0 Changelog](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/CHANGELOG.md#L406-L413)、[v1.9.0 Stores Guide](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/docs/stores-beta/user-guide.md#L1-L6)

一个 Store 的结构是：

```text
team-plans/
├── .openspec-store/
│   └── store.yaml              # Store 身份及可选 canonical remote
└── openspec/
    ├── config.yaml
    ├── specs/                  # 当前规格事实
    └── changes/                # 活跃 change
        └── archive/            # 已归档 change
```

创建和使用流程：

```bash
openspec store setup team-plans --path ~/openspec/team-plans \
  --remote git@gitlab.example.com:planning/team-plans.git

openspec new change add-login --store team-plans
openspec status --change add-login --store team-plans
openspec validate add-login --store team-plans

# 以下仍由人或 Skill 执行；--remote 只写 Store metadata，不会添加 Git origin
git -C ~/openspec/team-plans remote add origin \
  git@gitlab.example.com:planning/team-plans.git
git -C ~/openspec/team-plans push -u origin HEAD:main
```

其他机器需要自行 clone，再注册本地 checkout：

```bash
git clone git@gitlab.example.com:planning/team-plans.git ~/openspec/team-plans
openspec store register ~/openspec/team-plans
```

官方完整流程见 [Stores 用户指南](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/docs/stores-beta/user-guide.md#L53-L124)。

### 2.2 Store 不负责网络同步

`--remote` 只把 canonical clone URL 记录在 `.openspec-store/store.yaml`，用于提示其他人从哪里 clone；它不会启用同步。[OpenSpec CLI 文档](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/docs/cli.md#L339-L357)

当前实现对 Git 的写操作仅限 Store setup 时可选的 `git init` 和一次初始 commit；源码明确声明没有 clone、pull、push 或 sync。[Store Git 实现](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/src/core/store/git.ts#L11-L15)

仓库中还能看到带 `autoSync: true` 的旧设计示例，但该文件开头明确标为“historical beta direction”，不是当前产品依据，不应把它当成已经发布的 API 或能力。[历史方向说明](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/openspec/initiatives/context-store-and-initiatives/direction.md#L1-L17)、[历史 autoSync 示例](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/openspec/initiatives/context-store-and-initiatives/direction.md#L366-L381)

### 2.3 数据存放

| 数据 | 实际位置 | 是否共享 |
|---|---|---|
| 规格与 change | `<store>/openspec/` | 通过 Git commit/push 共享 |
| Store 身份 | `<store>/.openspec-store/store.yaml` | 提交到 Git |
| Store registry | `~/.local/share/openspec/stores/registry.yaml` | 仅当前机器 |
| Workset | `~/.local/share/openspec/worksets/` | 仅当前机器 |

Store metadata 和 registry 的当前数据结构见 [foundation.ts](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/src/core/store/foundation.ts#L28-L93)；默认全局数据目录见 [global-config.ts](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/src/core/global-config.ts#L77-L120)。

Registry 只是 `store id → 本地 checkout 路径/remote/branch` 的本机 YAML 索引，不是远端 registry 服务。当前实现还限制同一个 Store ID 在每台机器只注册一个 checkout。[registry.ts](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/src/core/store/registry.ts#L86-L118)

### 2.4 不要混淆的几个概念

| 术语 | 含义 |
|---|---|
| Store | 独立的 OpenSpec planning Git repo 及本机寻址机制 |
| Registry | Store 名称到本地 checkout 的本机索引 |
| Artifact | change 中的 `proposal.md`、`design.md`、`tasks.md`、delta specs 等普通文件 |
| Archive | 把 delta 合入主 `specs/`，再把 change 移到 `changes/archive/` |
| `/opsx:sync` | 把 delta 合入同一 OpenSpec root 的主 specs，但不归档；不是 Git/Store 网络同步 |

参考：[Change 与 artifact](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/docs/concepts.md#L182-L218)、[Archive](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/docs/concepts.md#L503-L557)、[`/opsx:sync`](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/docs/opsx.md#L218-L226)。

## 3. “Git hook”在当前方案中的准确含义

当前 ADR 所需的不是开发者电脑上的本地 Git hook，而是 **GitLab project push webhook**：GitLab 接受 push、更新远端 ref 后，由服务端向配置的 HTTP endpoint 发送事件。

| 机制 | 运行位置 | 是否适合作为批准发布事件 |
|---|---|---|
| 本地 `post-commit` | 开发者 checkout | 不适合；commit 可能从未 push，其他机器也未必安装相同 hook |
| 本地 `pre-push` | 开发者 checkout | 不适合充当远端事实；发生在 GitLab 接受 push 之前 |
| GitLab server hook | GitLab/Gitaly 服务端的自定义脚本 | 能做，但运维侵入较大，PoC 没必要 |
| GitLab project push webhook | GitLab 服务端发送 HTTP POST | 当前方案应使用的机制 |

Git 本地 hook 的调用阶段见 [Git 官方 githooks 文档](https://git-scm.com/docs/githooks)；GitLab server hook 是另一种管理员级扩展，见 [GitLab Server Hooks](https://docs.gitlab.com/administration/server_hooks/)。

GitLab push webhook payload 包含：

- `before`：push 前的 commit SHA；
- `after`：push 后的 commit SHA；
- `ref`：例如 `refs/heads/main`；
- `commits`：本次事件携带的 commit 摘要。

因此当前 ADR 使用 `after` 作为 `approved_commit_sha` 是合理的。[GitLab Push Events](https://docs.gitlab.com/user/project/integrations/webhook_events/#push-events)

GitLab project webhook 还可以按分支过滤；但一次 push 涉及的分支/tag 超过实例阈值时，整个 push 可能不产生 webhook，所以仍需要 schedule 扫描或人工重放作为补偿。[GitLab Webhooks](https://docs.gitlab.com/user/project/integrations/webhooks/#filter-push-events-by-branch)、[Push event limits](https://docs.gitlab.com/user/project/integrations/webhooks/#push-event-limits)

## 4. Store 与当前 GitLab webhook 逻辑的逐项区别

| 维度 | OpenSpec Store | 当前提交 GitLab + push webhook |
|---|---|---|
| 解决的问题 | spec 放在哪里、如何跨仓库引用 | 已发布版本如何触发外部工作流 |
| 运行形态 | 本地 CLI + 普通 Git repo | GitLab 服务端事件 + HTTP 接收端 |
| 权威数据 | Store repo 中的 OpenSpec 文件 | GitLab 远端 main 上的 commit |
| 版本标识 | 仍然是普通 Git commit SHA | webhook `after` SHA |
| 自动 push/pull | 没有 | 不负责；事件在 push 成功后产生 |
| 自动创建 backlog | 没有 | 由 Adapter/CI/Multica 集成实现 |
| 跨仓库规划 | 支持 Store pointer/reference | webhook 本身不知道 spec 的跨仓库语义 |
| 目标代码仓库 | Store 不选择，也不路由 | 同仓库时可由 webhook project 直接确定 |
| 批准语义 | 没有内建 `publish/approve` 状态 | SpecWire 可把受保护 main 上特定 commit 定义为批准 |
| 去重与重试 | 没有外部事件，自然也不提供 | 必须按业务稳定键去重，并处理漏事件/重放 |
| 审核 | 普通 Git branch/MR | 同样可使用 GitLab MR/保护分支 |
| 安全边界 | Git 仓库读写权限 | webhook secret、接收端权限、Multica 凭据 |

最核心的区别是：

```text
Store 是 storage/topology（存放与引用拓扑）
Webhook 是 event/integration（事件与系统集成）
```

## 5. 两种落地拓扑

### 5.1 当前单仓库方案：推荐用于 PoC

```text
application-repo
├── code
└── openspec/changes/<change-id>
        │
        ├─ validate + 人工确认
        └─ commit/push 到 GitLab main
                    │
                    ▼
             GitLab push webhook
                    │
                    ▼
          bridge 创建 Multica backlog
                    │
                    ▼
          人批准 → Agent 开发 → MR
```

优点：

- webhook 所属 GitLab project 就是目标代码仓库；
- 一个 SHA 同时锚定批准的 spec 和对应代码基线；
- spec、实现、CI、MR 权限都在一个 project 内；
- 无需维护 Store registry、额外 planning repo 或跨仓库映射。

这最符合当前 ADR 的单仓库、小团队前提。

### 5.2 Store + webhook：适合真正的跨仓库规划

```text
planning-store GitLab project
└── openspec/changes/<change-id>
        │
        └─ MR merge / push main
                    │
                    ▼
             GitLab push webhook
                    │
                    ▼
        SpecWire bridge 读取 routing metadata
              ┌─────┴─────┐
              ▼           ▼
        api-repo backlog  web-repo backlog
```

Store 只告诉 OpenSpec 规划 root 在哪里。官方明确说明：选择 Store 不会发现所有使用它的代码仓库，也不会把一份 Store task list 拆分或路由到多个代码仓库。[Store 多仓库限制](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/docs/stores-beta/user-guide.md#L192-L221)

OpenSpec 官方推荐的多组件方式是：

1. Store 保存共享的高层契约；
2. 每个代码仓库在 `openspec/config.yaml` 中把 Store 声明为只读 `references:`；
3. 每个代码仓库创建自己的本地 implementation change，独立 apply、MR 和 archive。

这比让一个 Store change 直接驱动多个代码仓库更接近 OpenSpec 原生模型。[Store references 工作流](https://github.com/Fission-AI/OpenSpec/blob/v1.9.0/docs/stores-beta/user-guide.md#L223-L258)

如果 SpecWire 仍决定从一个 Store change 直接创建实现 backlog，就必须自定义目标映射；那部分属于 SpecWire 集成逻辑，不是 OpenSpec Store 提供的能力。

## 6. 采用 Store 后事件数据必须变化

当前单仓库卡片可以使用：

```yaml
repository: group/application
change_id: add-login
approved_commit_sha: abc123
target_branch: main
```

因为同一个 repo/SHA 同时代表 spec 版本和代码基线。

一旦 spec 与代码分仓，至少应拆成：

```yaml
spec_repository: planning/team-plans
change_id: add-login
spec_commit_sha: abc123

target_repository: product/application
target_branch: main
target_base_sha: def456
```

理由：

- `spec_commit_sha` 只能定位 Store 中获批的规格；
- 它不能定位目标代码仓库的起始版本；
- webhook 的 GitLab project ID 是 planning project，不再天然等于实现 project；
- 同一 Store change 可能对应多个 target repository。

多目标时应改成显式列表：

```yaml
spec_repository: planning/team-plans
change_id: checkout-promo
spec_commit_sha: abc123

targets:
  - repository: product/checkout-api
    base_branch: main
    base_sha: def456
  - repository: product/checkout-web
    base_branch: main
    base_sha: 789abc
```

相应稳定键至少需要包含：

```text
<spec-project-id>:<change-id>:<spec-commit-sha>:<target-project-id>
```

这是基于两仓库版本模型得出的 SpecWire 设计推论，不是 OpenSpec 内建字段。

## 7. Multica 对当前 Hook 方案的实际影响

### 7.1 Generic Autopilot webhook 能接收事件，但不等于 backlog 闸门

Multica Autopilot webhook 可以接收 JSON，对 `Idempotency-Key` 去重，并把 payload 交给 Agent；`create_issue` 与 `run_only` 是两种输出模式。[Multica Autopilots](https://github.com/multica-ai/multica/blob/v0.4.26/apps/docs/content/docs/autopilots.zh.mdx#L8-L35)、[Webhook 行为](https://github.com/multica-ai/multica/blob/v0.4.26/apps/docs/content/docs/autopilots.zh.mdx#L59-L125)

但 v0.4.26 的 `create_issue` 实现：

1. 把新 issue 状态硬编码为 `todo`；
2. 设置 Agent/Squad assignee；
3. 随即 enqueue 执行任务。

来源：[Multica v0.4.26 `dispatchCreateIssue`](https://github.com/multica-ai/multica/blob/v0.4.26/server/internal/service/autopilot.go#L562-L742)。

这与 SpecWire 当前要求的“先建 backlog，不启动 Agent；人移出 backlog 才开工”不一致。

Multica 普通 issue 明确支持 `backlog`，且分配给 Agent 后仍要等它离开 backlog 才创建执行任务。[Multica Issues](https://github.com/multica-ai/multica/blob/v0.4.26/apps/docs/content/docs/issues.zh.mdx#L28-L55)

因此当前应选下列方式之一：

1. Webhook Adapter 或 GitLab CI 直接调用 Multica API/CLI，以 `status=backlog` 创建 issue；这是最直接的 PoC 方案。
2. Webhook 触发 `run_only` 控制型 Agent，由它查重并执行 `multica issue create --status backlog`；它会引入一个需要在线 runtime 的控制执行层。
3. 等待或扩展 Multica，让 Autopilot 支持配置初始 issue status；当前官方接口没有该字段。

### 7.2 Native GitLab VCS webhook 是另一条事件路径

Multica 自托管 GitLab 集成官方要求在 GitLab 启用 Merge Request events 和 Pipeline events，用于 MR 关联、CI 展示以及合并后的 issue 状态处理；它没有被描述为“监听 spec push 并创建 backlog”。[Multica GitLab VCS integration](https://github.com/multica-ai/multica/blob/v0.4.26/apps/docs/content/docs/vcs-integration.zh.mdx#L57-L85)

所以应继续区分：

```text
spec publication push
  → SpecWire bridge / Generic Autopilot webhook
  → 创建 backlog

implementation MR / pipeline
  → Multica native GitLab VCS webhook
  → 关联 MR、展示 CI、完成联动
```

### 7.3 直接连接仍有认证边界

Multica Generic Autopilot webhook 的 URL token 本身是调用凭据；配置额外签名密钥时，它校验 GitHub 风格的 `X-Hub-Signature-256`。[Multica webhook 源码](https://github.com/multica-ai/multica/blob/v0.4.26/server/internal/handler/autopilot_webhook.go#L236-L275)

GitLab project webhook 的传统 Secret token 放在 `X-Gitlab-Token`。因此不能假定 Multica Generic webhook 会按 GitLab secret 语义验证来源；若不接受仅依赖 bearer URL，应保留 Adapter 做验签和协议转换。[GitLab delivery headers](https://docs.gitlab.com/user/project/integrations/webhooks/#delivery-headers)

## 8. 当前建议

### 当前 PoC

继续采用：

```text
代码仓库内 OpenSpec
→ 人审核并发布到受保护 main
→ GitLab project push webhook
→ 很薄的 Adapter/CI bridge
→ 显式创建 Multica backlog issue
→ 人批准后执行
```

不要为了“让 Git 触发 Multica”而引入 Store，因为 Store 不提供触发能力；它只会在当前单仓库场景中增加一个 planning repo 和跨仓库版本映射。

### 将来满足以下条件再引入 Store

- 一份共享契约真正被多个代码仓库消费；
- 规划需要在代码仓库存在之前开始；
- 规划和实现需要不同权限、MR 或发布周期；
- 有独立团队长期维护共享 requirements/contracts。

届时推荐先采用“Store 保存上游契约、各代码仓库用 `references:` 引用并维护自己的 implementation change”的原生模式。GitLab webhook 保留在每个真正发布实现 change 的仓库；若一定从 Store 集中派发，则另外设计 target mapping、双 SHA、fan-out 和幂等规则。

## 9. 事实、推论与未知项

### 已核实事实

- Stores 是 OpenSpec v1.9.0 的正式 beta 功能。
- Store 是独立 planning Git repo，不是服务。
- OpenSpec 不会 clone、pull、push、自动 sync，也不发 webhook。
- Store registry/workset 是本机状态。
- Store 当前不会把任务路由到代码仓库。
- GitLab push webhook 可提供远端 `after` SHA。
- Multica v0.4.26 `create_issue` Autopilot 创建 `todo` 并立即 enqueue；普通 `backlog` issue 才具备人工开工闸门。

### 设计推论

- Store 与 GitLab webhook 正交，可以组合但互不替代。
- 单仓库 PoC 没有引入 Store 的必要。
- spec/code 分仓后必须分别记录 `spec_commit_sha` 和目标代码 `base_sha`。
- 一份 Store change 面向多个仓库时，稳定键必须包含 target project，或改用每仓库本地 implementation change。

### 尚需 PoC 验证或决策

- 发布信号最终采用受保护 main 的直接 push、spec MR merge，还是明确的 commit trailer。
- Bridge 采用 GitLab CI、无状态 Adapter，还是 `run_only` 控制 Agent。
- 自托管 Multica 实际部署版本是否仍保持 v0.4.26 的 Autopilot 初始状态行为；升级后需重新核对。
- 若未来采用 Store，target mapping 写在 change metadata、独立 manifest，还是由每仓库 implementation change 表达。
- 是否接受 Generic webhook URL token 作为唯一调用凭据；不接受时必须保留验签 Adapter。

## 10. 最终判断

一句话概括：

> **OpenSpec Store 是“独立 spec Git 仓库 + 本地寻址机制”；GitLab push webhook 是“已发布 Git 版本到 Multica 的事件桥”。Store 可以成为 webhook 的上游，但不能替代 webhook。**

对当前 SpecWire Workflow，维持单仓库方案最合适。真正需要先修正的不是是否使用 Store，而是 Multica backlog 的创建路径：不能直接依赖当前 `create_issue` Autopilot，必须显式创建 `status=backlog` 的 issue，才能保留人工开工闸门。
