## MODIFIED Requirements

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
