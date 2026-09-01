# Agent 规约（中文草案）

> 本文件是仓库 Agent 规约的中文草案，供人工评审和微调。自动加载和权威版本仍是 `AGENTS.md`。

## 工作入口

跨组件变更开始前，先阅读 `CONTEXT.md`，再按照其中的指针阅读已发布领域上下文和当前契约。SpecWire 是 GitLab 与 Multica 之间的集成边界；本地 Workflow Skill、Agent 执行、代码仓库操作、MR 评审和合并不属于 SpecWire 运行时。

## 文档权威性

每类信息只保留一个权威来源，不在 Skill、README 或设计说明中创建第二套行为契约。

| 目录/文件 | 职责 |
|---|---|
| `openspec/drafts/` | 尚未形成正式 Change 的探索草稿 |
| `openspec/changes/<change-id>/` | 一次完整变更的设计、决策、原型、增量文档和任务 |
| `openspec/specs/behavior/` | 当前已接受、已实现的功能行为契约 |
| `openspec/specs/domain/` | 当前领域术语、概念和关系 |
| `openspec/specs/architecture/` | 当前架构设计和已接受 ADR |
| `openspec/specs/experience/` | 当前产品体验和交互契约 |
| `docs/agents/` | Agent 和仓库操作规程，不是产品设计文档 |
| `CONTEXT.md` | Agent 启动入口，指向 `openspec/specs/domain/`，不复制领域正文 |
| `openspec/config.yaml` | OpenSpec 的 schema、编写上下文、产物规则和操作提示，不是行为或架构的权威来源 |

`openspec/specs/` 是当前已发布知识库，不是历史材料的简单追加。归档 Change 时，要对新增、修改和删除内容进行合并、替代和冲突处理。

## 目标目录结构

```text
openspec/
├── config.yaml
├── drafts/
│   └── <draft-id>/
│       ├── exploration.md
│       ├── domain/
│       ├── architecture/
│       ├── prototype/
│       └── technical-design.md
├── changes/
│   ├── <change-id>/
│   │   ├── proposal.md
│   │   ├── design.md
│   │   ├── decisions.md
│   │   ├── prototype/
│   │   ├── technical-design.md
│   │   ├── specs/
│   │   │   ├── behavior/
│   │   │   ├── domain/
│   │   │   ├── architecture/
│   │   │   └── experience/
│   │   └── tasks.md
│   └── archive/
└── specs/
    ├── behavior/
    ├── domain/
    ├── architecture/
    │   └── adr/
    ├── experience/
    └── index.md
```

功能目录必须位于 `behavior/` 下，例如 `behavior/admin/`、`behavior/bridge/`、`behavior/workflow/` 和 `behavior/integration-flow/`。`changes/<change-id>/specs/` 与 `openspec/specs/` 使用相同的分类体系；前者保存增量内容，后者保存合并后的完整当前状态。

## 以 Change 为先的设计工作流

对于绿地项目、跨组件工作或领域模型发生实质性变化的工作：

1. 还没有明确范围时，将探索材料放入 `openspec/drafts/<draft-id>/`。
2. 范围形成后，建立或更新一个完整的 `openspec/changes/<change-id>/`，不要把同一件事拆到多个 Change。
3. 将整理后的 Grill 结论、接受/拒绝的方案、领域模型、拟议 ADR、产品原型、技术设计、delta specs 和任务放入该 Change。
4. 原始对话不是契约；必须把结论整理为决策、理由、影响和待解决问题。
5. Change 期间不直接修改 `openspec/specs/`。原型迭代放入 Change 的 `prototype/`；废弃方向也随 Change 保留。
6. 只有行为被实现并接受后，才把 Change 合并到对应的 `specs/behavior/`、`domain/`、`architecture/` 或 `experience/` 目录。

拟议架构决策在 Change 内维护；接受后放入 `openspec/specs/architecture/adr/`。稳定的领域术语和模型放入 `openspec/specs/domain/`。当前产品交互契约放入 `openspec/specs/experience/`，候选图片和探索过程仍留在 draft 或 Change 中。

## 归档规则

归档不是把文件原样复制到 `specs/`，而是：

- 将行为增量合并到 `specs/behavior/`；
- 将确认后的领域变化合并到 `specs/domain/`；
- 将确认后的架构设计和 ADR 合并到 `specs/architecture/`；
- 将确认后的交互规则合并到 `specs/experience/`；
- 删除或标记已经被新设计替代的旧内容；
- 将完整 Change（包括原型和设计历史）保留到 `openspec/changes/archive/`。

## Skill 使用规则

Matt Skills 和 OpenSpec Skills 是编写流程，不是第二套规格系统。Skill 的默认输出位置如果与本仓库的文档分层冲突，以本规约和 `docs/agents/domain.md` 为准。

`openspec/config.yaml` 只提供 OpenSpec 生成和操作时的上下文、规则与提示；它不能取代 `openspec/specs/` 中的已发布内容，也不能把草稿直接视为当前行为。

## 文档冲突处理

发现冲突时先判断信息类型，再修改对应权威来源：

- 功能行为 → `openspec/specs/behavior/`
- 领域术语和模型 → `openspec/specs/domain/`
- 架构设计和理由 → `openspec/specs/architecture/`
- 产品体验和交互 → `openspec/specs/experience/`

不要通过复制文档或静默覆盖来解决冲突。README 等说明性文档应从上述权威内容派生更新。
