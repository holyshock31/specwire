# reset-specwire-v2-contract

## Why

The current behavior contracts still mix the retired v1 `proposal-ready` Push Hook, the v2 GitLab Issue publication model, and the client-side workflow performed by SpecWire Skills. This creates competing interpretations of SpecWire's boundary and makes the early project difficult to evolve safely.

The project is still early enough to establish one clean v2 contract now, before more clients and behavior depend on the old wording. The accepted boundary and architecture decisions already provide the target: SpecWire integrates GitLab with Multica, while OpenSpec authoring, repository operations, Agent execution, and review/merge workflow remain outside the runtime.

## What Changes

- **BREAKING** Make a GitLab Issue carrying the `change` label the only publication entry point for a new execution projection.
- Retain the `archived` Push Hook only as the completion signal that closes the projection and linked publication Issue; it must never publish new work.
- Rewrite the `bridge` contract around the current v2 event, projection, correlation, idempotency, recovery, and Multica adapter behavior.
- Rewrite the `workflow` contract to cover only the SpecWire-visible integration lifecycle; remove local branch, OpenSpec authoring, Agent execution, MR review, and merge responsibilities from it.
- Remove v1 publication semantics from the current contracts, tests, and authoring context. Historical change artifacts may retain their historical v1 description.
- Make the SpecWire/SpecWire Skills boundary explicit: Skills produce or consume the integration protocol but do not define Bridge behavior.
- Align `openspec/config.yaml` and derived documentation with the v2-only contract without making them additional sources of truth.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `bridge`: replace the mixed v1/v2 publication contract with the v2 Issue publication and archive-completion contract.
- `workflow`: narrow the capability to the integration lifecycle owned by SpecWire and remove client workflow behavior.

## Impact

- **Bridge**: handler routing, v1 proposal-ingestion code and tests, event parsing, configuration context, and contract tests are affected. The current Multica adapter remains the supported target.
- **SpecWire Skills**: the independently managed client must continue to publish the GitLab Issue protocol and consume the projection lifecycle; its local Git and Agent steps remain outside this repository's runtime contract.
- **OpenSpec**: the three current specs are audited together; `admin` remains the control-plane contract unless the audit finds a concrete requirement mismatch.
- **GitLab/Multica**: no new external platform capability is introduced. The existing v2 Issue Hook, projection correlation, and archive closure are made canonical.
