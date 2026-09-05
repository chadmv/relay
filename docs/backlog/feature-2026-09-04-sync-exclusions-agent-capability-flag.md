---
title: An agent capability flag for sync exclusions, so a mixed-version fleet cannot silently ignore them
type: feature
status: open
created: 2026-09-04
priority: medium
source: Stage 2 of the sync-spec exclusion paths design; Stage 1 shipped as PR #203 and left this as a documented limitation
---

# An agent capability flag for sync exclusions, so a mixed-version fleet cannot silently ignore them

## Summary

Protobuf drops a field an agent's build does not know. An agent built before sync exclusions shipped
therefore receives an excluding spec, syncs the excluded path **in full**, and reports success -
with no error, no warning and no log line. The coordinator never asks whether an agent honours
exclusions and never detects the mismatch afterwards, so on a mixed-version fleet whether a task's
exclusions take effect depends on which agent it lands on.

## Context

Stage 1 (`feature-2026-09-04-implement-sync-spec-exclusion-paths`, closed, PR #203) shipped the
feature and stated this as a live limitation rather than fixing it. `README.md`'s exclusions section
carries the paragraph beginning "**Exclusions need every agent in the fleet on a build that supports
them, and nothing checks that.**", which names the mechanism, the observable and the operator action
("Upgrade every agent before using exclusions"). It was deliberately written to stand alone and to
promise no future fix, so it does not become false if this item never lands - which also means the
work itself is tracked nowhere else.

**The failure is silent in the dangerous direction.** The excluded subtree lands on disk on a machine
whose operator asked for it not to be, and the coordinator believes otherwise. There is a second
consequence beyond the disk: the coordinator computes the composite `SourceKey` while an old agent
registers the bare stream, so the warm-workspace bias can never match for an excluded task on that
agent, and every such task is a full cold sync of the whole stream, permanently.

**The precedent already exists and is the model to copy - with one deliberate difference.**
`supports_workspaces` is the same shape: an `optional bool` on `RegisterRequest`, a column on
`workers`, and a hard skip in `selectWorker`. Its persistence is COALESCE-to-previous
(`supports_workspaces = COALESCE(sqlc.narg(supports_workspaces)::bool, supports_workspaces)` in
`RegisterWorkerConnection`, and the same against `workers.supports_workspaces` in
`UpsertWorkerByHostname`), so an agent that stops reporting keeps its last value. **This flag must
NOT do that.** A downgrade to an older binary stops reporting precisely because the binary no longer
supports the feature, so preserving a stale TRUE is exactly wrong here - it must COALESCE to FALSE
and revert.

Verified against the tree on 2026-09-04: `RegisterRequest`'s highest field number is 12
(`supports_workspaces`), so 13 is free; the latest migration is `000023_task_preparing_status`, so
`000024` is free; and `selectWorker`'s existing capability filter is a hard `continue`
(`if sourceBearing && !w.SupportsWorkspaces { continue }`), not a lower score.

## Proposal

Sketch only; four pieces, and the third is the one that changes behaviour.

1. **Proto and schema.** `optional bool supports_sync_exclusions = 13` on `RegisterRequest`; a
   migration adding `workers.supports_sync_exclusions BOOLEAN NOT NULL DEFAULT FALSE`; the column
   written at all three registration statements in `internal/store/query/workers.sql`
   (`RegisterWorkerConnection`, `UpsertWorkerByHostname`, `InsertWorkerForAutoEnroll`).
   **COALESCE to FALSE, not to previous** - see Context.
2. **The agent reports it unconditionally.** `buildRegisterRequest` sets it to true because it
   describes the BUILD, not configuration - there is nothing for an operator to turn on. Plumb it at
   all three registration sites in `internal/worker/handler.go`, not two.
3. **`selectWorker` hard-skips.** An excluded task must not be scored on a worker without the
   capability; match the `SupportsWorkspaces` shape (`continue`), not a score penalty, so the task
   waits for a capable worker rather than landing on an incapable one at a lower rank. Decide what
   happens when NO worker is capable - the task pends, and whether that is visible enough is part of
   this item, not a detail.
4. **README.** Delete the version-skew paragraph and replace it with what the flag now guarantees.
   It is one paragraph so that this replaces exactly it.

## Acceptance / Done When

- An agent on a build without exclusion support is never assigned a task carrying an exclusion.
- A downgrade to such a build reverts the flag rather than preserving a stale TRUE, proven by a
  test that registers with the field present and then absent.
- The mutation that must be killed: making `selectWorker` score the incapable worker lower instead
  of skipping it.
- A task that can find no capable worker has an observable state, and README says what an operator
  sees.
- README's version-skew paragraph is gone, replaced rather than corrected.

## Related

- `proto/relayv1/relay.proto` (`RegisterRequest`), `internal/store/query/workers.sql`,
  `internal/worker/handler.go`, `internal/scheduler/dispatch.go` (`selectWorker`),
  `internal/agent/agent.go` (`buildRegisterRequest`), `README.md` (the exclusions section)
- [[feature-2026-09-04-implement-sync-spec-exclusion-paths]] - Stage 1, closed; its resolution
  records this as deferred
- `docs/superpowers/specs/2026-09-04-sync-spec-exclusion-paths.md` - the design, whose section 8
  permits the cut only in this form
- [[idea-2026-09-04-a-job-author-controls-how-many-p4-clients-each-agent-creates]] - the other
  open consequence of Stage 1
