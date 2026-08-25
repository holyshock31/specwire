# workflow Specification

## Purpose

定义 SpecWire 的 GitLab 发布与 Multica 执行投影之间的集成生命周期契约，并明确客户端 Skills、执行系统与 SpecWire Bridge 的责任边界。

## Requirements

### Requirement: SpecWire 以 Git/OpenSpec 为规格事实源，Multica 为执行投影

GitLab remains the source system for the published change revision, while a Multica Issue/Run is a derived execution projection. SpecWire MUST preserve the publication's immutable metadata and correlation, but MUST NOT author, clone, or interpret OpenSpec content and MUST NOT treat Multica as a source of change requirements. Repository operations, Agent execution, review, and merge decisions remain client-side responsibilities.

#### Scenario: 发布元数据进入执行投影

- **WHEN** a valid GitLab `change` Issue publication is received
- **THEN** Bridge creates a derived Multica projection containing the publication metadata without copying or interpreting OpenSpec files

#### Scenario: 规格发布后创建未分配 Backlog 卡

- **WHEN** a valid `change` Issue is published without an explicit direct-execution status
- **THEN** SpecWire creates the derived projection in its default backlog state; any approval or assignment action remains outside SpecWire

#### Scenario: 人批准后 Agent 开工

- **WHEN** a human or client workflow changes the projection state to permit execution
- **THEN** the client Agent may begin its work, while SpecWire remains responsible only for the projection and its correlation

#### Scenario: 归档后投影自动闭环

- **WHEN** the published change emits an `archived` completion event
- **THEN** SpecWire completes the correlated projection and linked publication closure without interpreting the Agent's implementation

#### Scenario: 执行投影不是事实源

- **WHEN** a Multica projection title, status, or run state changes
- **THEN** the GitLab change content and publication revision remain authoritative and unchanged

### Requirement: Agent 实现基线为发布时刻的冻结点

SpecWire MUST freeze and carry the publication's `branch_head_sha` as correlation metadata. SpecWire MUST NOT follow a moving branch head or update an active projection in place. How an Agent checks out that frozen revision is owned by the separately managed SpecWire Skills/client layer.

#### Scenario: 分支推进不影响已发布投影

- **WHEN** the source branch advances after a `change` Issue is published
- **THEN** the existing projection retains the original `branch_head_sha` and is not retargeted

#### Scenario: 新版本显式新发布

- **WHEN** a different frozen revision needs execution
- **THEN** the client publishes a new `change_id` and SpecWire creates an independent publication/projection

#### Scenario: 开发期间规格被再次发布不影响进行中 Agent

- **WHEN** a new immutable publication is created while a client Agent is working on an existing projection
- **THEN** SpecWire leaves the existing projection's frozen metadata unchanged and creates an independent projection for the new publication

### Requirement: SpecWire 只处理发布与归档两个集成事件

SpecWire MUST expose a narrow lifecycle boundary: a GitLab `change` Issue opens a new execution projection, and an `archived` Push Hook on main completes the existing projection. Branch creation, OpenSpec proposal authoring, local commit/push, Agent execution, MR review, MR merge, and Skill distribution MUST remain outside this lifecycle contract.

#### Scenario: change Issue 是唯一新任务入口

- **WHEN** an allowlisted GitLab project opens a valid Issue with the `change` label
- **THEN** SpecWire handles it as a publication and projects it to the configured execution system

#### Scenario: archive 只负责完成

- **WHEN** an `archived` Push Hook arrives for a published change
- **THEN** SpecWire completes the correlated projection and does not create another task

#### Scenario: 普通分支活动不触发投影

- **WHEN** a feature branch is pushed or a merge request is reviewed or merged without an `archived` event
- **THEN** SpecWire does not create or change an execution projection as part of those client-side workflow actions

### Requirement: 发布协议与客户端 Skill 解耦

SpecWire Skills MAY create branches, author OpenSpec changes, push the publication branch, create the GitLab `change` Issue, and perform repository or Agent operations. Those Skills MUST consume the publication and archive protocol defined by SpecWire and MUST NOT be treated as the authority for Bridge behavior. A change to Skill workflow alone MUST NOT expand SpecWire runtime scope.

#### Scenario: Skill 发布协议字段稳定

- **WHEN** a Skill publishes a change
- **THEN** it supplies the GitLab Issue label and fields required by the SpecWire publication contract, while the internal Skill steps remain independently managed

#### Scenario: Skill 流程变化不改变 Bridge 契约

- **WHEN** the client changes its local branch or Agent orchestration steps without changing the publication protocol
- **THEN** SpecWire requires no runtime behavior change
