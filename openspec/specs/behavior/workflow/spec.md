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

### Requirement: 发布协议与客户端 Skill 解耦

SpecWire Skills MAY create branches, author OpenSpec changes, push the publication branch, create the GitLab `change` Issue, and perform repository or Agent operations. Those Skills MUST consume the publication and archive protocol defined by SpecWire and MUST NOT be treated as the authority for Bridge behavior. A change to Skill workflow alone MUST NOT expand SpecWire runtime scope.

#### Scenario: Skill 发布协议字段稳定

- **WHEN** a Skill publishes a change
- **THEN** it supplies the GitLab Issue label and fields required by the SpecWire publication contract, while the internal Skill steps remain independently managed

#### Scenario: Skill 流程变化不改变 Bridge 契约

- **WHEN** the client changes its local branch or Agent orchestration steps without changing the publication protocol
- **THEN** SpecWire requires no runtime behavior change

### Requirement: SpecWire 只处理发布、归档与受控废弃三个集成事件

SpecWire MUST expose a narrow Integration Flow boundary for the current GitLab-to-Multica lifecycle: a published input ConnectorBehavior handles a GitLab `change` Issue as a new execution projection, an `archived` Push input ConnectorBehavior handles completion of an existing projection, and the reserved abandon input ConnectorBehavior handles an explicit `specwire::abandoned` label addition on an existing Change Issue to cancel its projection. These behaviors MUST be represented by published Flows and built-in templates rather than an unversioned hard-coded route. Branch creation, OpenSpec proposal authoring, local commit/push, Agent execution, MR review, MR merge, and Skill distribution MUST remain outside this lifecycle contract.

#### Scenario: change Issue 是内置发布 Flow 的入口

- **WHEN** an allowlisted GitLab project opens a valid Issue with the `change` label and the publication Flow is published for its Connection
- **THEN** SpecWire handles it through the Flow runtime and projects it to the configured execution system

#### Scenario: archive Flow 只负责完成

- **WHEN** an `archived` Push Hook arrives for a published change and the completion Flow matches
- **THEN** SpecWire completes the correlated projection and does not create another task

#### Scenario: 普通分支活动不触发投影

- **WHEN** a feature branch is pushed or a merge request is reviewed or merged without an `archived` completion event
- **THEN** no published lifecycle Flow creates or changes an execution projection as part of those client-side workflow actions

#### Scenario: 未发布 Flow 不接收生产事件

- **WHEN** a Connection has only a draft, paused, or archived Flow for an input behavior
- **THEN** the event is recorded as unmatched or skipped and no production external action is performed

#### Scenario: change Issue 是唯一新任务入口

- **WHEN** an allowlisted GitLab project opens a valid Issue with the `change` label and the built-in publication Flow is published
- **THEN** the Flow runtime handles it as the only built-in new-task publication entry point

#### Scenario: archive 只负责完成

- **WHEN** an `archived` Push Hook arrives for a published change and the completion Flow matches
- **THEN** the Flow completes the correlated projection and does not create another task

#### Scenario: abandoned 标签只负责取消

- **WHEN** an existing Change Issue receives a new `specwire::abandoned` label and the reserved abandon Flow matches its Issue update
- **THEN** the Flow cancels the correlated projection and does not create another task or mark it done
