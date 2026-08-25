# Integration side effects use at-least-once delivery with replayable failures

**Status**: accepted

SpecWire treats GitLab delivery as at-least-once and makes projection operations idempotent. A partial failure is recorded and observable so it can be retried or manually replayed; SpecWire does not require exactly-once execution or compensate by rolling back an already successful external side effect.

**Consequences**:

- Duplicate webhook delivery must converge without duplicate execution projections.
- A successful Multica completion is not undone because closing the GitLab Issue fails.
- Recovery and reconciliation are part of the integration boundary, while the source change remains owned by GitLab.
