# Current SpecWire scope is GitLab to Multica

**Status**: accepted

The current SpecWire product scope is the integration boundary from GitLab to Multica. Other execution systems remain future adapter targets and do not expand the current acceptance scope. SpecWire owns the Bridge, execution projections, correlation, and integration control plane; OpenSpec authoring, repository operations, Agent execution, MR review or merge decisions, and SpecWire Skills remain outside the runtime.

**Consequences**:

- Current behavior may remain Multica-specific, but future target support must have a clear adapter seam.
- The adapter seam is intentionally narrow: resolve a target project, create an execution projection, apply initial state or assignment, and mark the projection complete.
- The SpecWire repository should document the integration contract rather than own or duplicate SpecWire Skills.
- A second execution-system implementation is a separate scope decision, not an implicit follow-up to the current Bridge.
- The control plane covers mapping, webhook lifecycle, credentials, audit, and health; external project provisioning, general identity management, backups, and notification orchestration remain outside this scope.
- Bridge validation covers event authenticity, project routing, labels, and field shape; it does not clone or interpret OpenSpec content, which remains authoritative in GitLab.
- The supported publication protocol is the GitLab `change` Issue; the legacy proposal push path is not part of the current scope.
