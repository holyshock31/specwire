# SpecWire owns the integration boundary, not workflow skills

**Status**: accepted

SpecWire is defined as the integration layer between GitLab and Multica-like execution systems. Local workflow skills, including initiate, review, merge, and archive, remain a separately managed client layer under SpecWire Skills. This keeps skill distribution and local repository operations independent from the Bridge contract, while allowing additional execution systems or clients to integrate through the same boundary.

**Consequences**:

- SpecWire must expose stable event, projection, and correlation semantics rather than depend on a particular skill implementation.
- Changes to local skills do not automatically expand SpecWire's runtime scope.
- Multica-specific behavior should remain behind an execution-system integration boundary as other targets are introduced.
