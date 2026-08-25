# SpecWire Integration Context

SpecWire is the integration boundary between GitLab's change lifecycle and Multica. Other execution systems are future integration targets, not current supported targets. It connects systems while keeping SpecWire Skills and the internal ownership of each system outside the SpecWire runtime boundary.

## Language

**SpecWire**:
The system-to-system integration layer that translates GitLab change events into execution-system projections and preserves the links needed for lifecycle closure.
_Avoid_: workflow product, agent runtime, project-management platform

**SpecWire Skill**:
An independently managed client-side automation capability that orchestrates repository, GitLab, or execution-system operations around SpecWire. SpecWire Skills are clients of SpecWire, not part of its runtime.
_Avoid_: SpecWire feature, Bridge behavior

**Execution System**:
A system that owns execution tasks and agent runs for a published change. Multica is the current execution system; other systems are future integration targets.
_Avoid_: SpecWire, source of truth

**Execution Projection**:
The derived representation of a published GitLab change in an execution system. It carries operational context and state but is not authoritative for the change content.
_Avoid_: specification, source change

**Execution Adapter**:
The narrow integration boundary through which SpecWire addresses an execution system: resolve its project, create a projection, apply initial assignment or state, and mark the projection complete.
_Avoid_: workflow engine, full Multica model

**Publication**:
An immutable handoff of one change revision from GitLab into SpecWire through a GitLab Issue carrying the `change` label. Re-delivery of the same publication is idempotent; changing the frozen revision is a different change and is not an in-place task update.
_Avoid_: mutable task update, live branch tracking

**Archive Event**:
The GitLab push event that signals a merged change has been archived on `main`. It closes the execution projection and its publication Issue; it is a completion signal, not a publication entry point.
_Avoid_: v1 publication, proposal event

**Projection Recovery**:
The process of observing, retrying, or manually replaying a failed integration side effect without changing the authoritative GitLab change.
_Avoid_: rollback of source change, exactly-once execution

**Control Plane**:
The operational surface for keeping SpecWire integrations configured and observable, including project mapping, webhook lifecycle, credentials, audit, and health. It does not own external project provisioning or general identity management.
_Avoid_: workflow UI, project-management system

## Reference scenario

The end-to-end scenario in the project diagram is a client workflow around SpecWire, not an expansion of the SpecWire runtime boundary:

1. A locally managed Skill starts a change in an agent session/worktree: it creates a `feat`/`fix` branch, runs `opsx:propose`, commits and pushes the branch, then opens a GitLab Issue with the `change` label and the `change_id`, `branch`, and `branch_head_sha` fields.
2. The GitLab Issue Hook delivers that publication to SpecWire. SpecWire validates the event, creates the Multica execution projection with the branch context, and applies the requested initial status or assignment. Other issue platforms are outside the current supported target.
3. Multica and the client Agent handle checkout, implementation, MR delivery, human review, and merge. These are client/execution-system responsibilities, not SpecWire Bridge behavior.
4. The archive Skill synchronizes the merged `main`, runs the repository's archive operation, and pushes the archive event. SpecWire receives the `archived` Push Hook, completes the Multica projection, and closes the linked GitLab publication Issue.

The diagram's `tag: change` wording should be read as: the GitLab Issue carries the `change` label. In the canonical GitLab protocol, this is an Issue label (`labels[].title == "change"`), not a Git tag or a generic webhook field. The diagram is a scenario reference and does not make the local Skills, Agent session, review, merge, notification, or archive command part of SpecWire's runtime contract.
