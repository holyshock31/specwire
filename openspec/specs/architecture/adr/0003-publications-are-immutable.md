# Publications are immutable and single-active per change

**Status**: accepted

SpecWire treats a published change revision as an immutable publication. Re-delivery of the same publication is idempotent; a different frozen revision is not an update to an existing execution task and must use a new `change_id` rather than silently retargeting work already in progress.

**Consequences**:

- The Bridge never moves an active task to a new branch head or specification revision.
- The normal lifecycle has one active publication and one execution projection per `change_id`.
- Revisions are explicit new changes, keeping archive and completion correlation unambiguous.
