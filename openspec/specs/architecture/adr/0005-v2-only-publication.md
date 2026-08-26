# GitLab change Issues are the only publication entry point

**Status**: accepted

SpecWire will not retain the v1 `proposal-ready` Push Hook as a publication path. A GitLab Issue with the `change` label is the only supported way to publish a new execution task. The `archived` Push Hook remains because it is the v2 completion signal after a change has been merged and archived; it does not publish work.

**Consequences**:

- The v1 proposal-ingestion handler, contract, tests, and active documentation were removed by the archived `2026-08-25-reset-specwire-v2-contract` change.
- New clients and SpecWire Skills need only implement the v2 Issue publication protocol.
- The Bridge still needs the Push Hook receiver for archive events, but must not create tasks from proposal commits.
