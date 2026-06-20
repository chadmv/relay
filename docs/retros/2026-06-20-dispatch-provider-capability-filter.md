---
date: 2026-06-20
topic: dispatch-provider-capability-filter
branch: claude/suspicious-beaver-5f66ef
range: efe6867..17e89b6 (impl), 8781c2c (integration-test fix), 73c4b28 (predicate unification)
pr: 2026-06-20 / dispatch-provider-capability-filter
merge: 2026-06-20 / dispatch-provider-capability-filter
---

# Session Retro: 2026-06-20 - Dispatch Provider-Capability Filter

**TL;DR:** Closed `bug-2026-06-19-dispatch-provider-capability-filter`, the
dispatch-side gap deferred by the predecessor nil-provider guard. Workers now
report a single `supports_workspaces` capability bit at registration; the
scheduler hard-skips providerless workers for source-bearing tasks rather than
merely scoring them lower, so such a task is held `pending` (not dispatched to
fail) until a capable worker connects. The high-value lessons were three: an
integration test that was green while certifying nothing, a three-way divergence
in how "source-bearing" was decided across the scheduler, and rolling-upgrade
safety treated as a first-class design constraint via `optional bool` + COALESCE
+ `DEFAULT TRUE`.

## What Was Built

A source-bearing task is now never *routed* to a worker that cannot manage a
workspace; the predecessor's runtime `PREPARE_FAILED` guard is retained only as
the backstop for the narrow registration-to-dispatch race.

- **Proto** (`proto/relayv1/relay.proto`) - `optional bool supports_workspaces`
  (field 12) on `RegisterRequest`. `optional` gives explicit presence so the
  server can distinguish "agent reported false" from "old agent omitted it."
- **Agent** (`internal/agent/agent.go`) - `buildRegisterRequest` sets the field
  to `proto.Bool(a.provider != nil)`, reusing the exact fact the runtime guard
  keys on so registration and the guard stay consistent by construction.
- **Migration 000017** - `workers.supports_workspaces BOOLEAN NOT NULL DEFAULT
  TRUE`, persisted on every connect via `RegisterWorkerConnection` (authoritative)
  and on insert/upsert via `UpsertWorkerByHostname`, both using
  `COALESCE(sqlc.narg('supports_workspaces')::bool, ...)` so a nil param (old
  agent) never overwrites a known value.
- **Scheduler** (`internal/scheduler/dispatch.go`) - `selectWorker` hard-skips
  providerless workers for source-bearing tasks before scoring, so the
  warm-affinity bonus can never resurrect one. A nil selection leaves the task
  `pending`; the existing dispatch cycle re-attempts it when a capable worker
  registers. One throttled log line per cycle surfaces the held condition,
  emitted only when no connected worker advertises a provider (so normal
  all-busy backpressure stays silent).

## Key Decisions

- **Capability lives on the `workers` DB row, not the in-memory Registry.**
  `selectWorker` already iterates value-copied `store.Worker` rows from
  `ListWorkers`; a plain bool column rides that read with zero new queries and
  satisfies the no-interior-pointers-across-locks invariant trivially.
- **No new task status.** A held source-bearing task stays `pending` -
  operationally identical to a task whose workers are all busy. Minting an
  `unschedulable` status would mean a migration, a back-to-pending transition
  with its own epoch-fence considerations, and UI work for no behavioral gain.
  Mirrors the predecessor's YAGNI call (`PREPARE_FAILED` -> existing `failed`).
- **Rolling-upgrade safety as a design constraint, not an afterthought** (see
  Instructive Events 3). The `optional bool` + COALESCE + `DEFAULT TRUE` trio was
  chosen specifically so a half-upgraded fleet cannot strand every source
  workload.

## Instructive Events

### 1. A test that passed for the wrong reason

The held-pending integration test seeded the task with `Requires: []byte("[]")`
(a JSON array). `LabelMatch` unmarshals `Requires` into a `map[string]string`,
which fails on an array, so the providerless worker was rejected at the **label**
gate - the capability filter under test was never reached. The test was green and
certified nothing. Phase 4 integration verification caught it; the fixture was
corrected to `{}` and the capability guard is now genuinely exercised.

**Lesson:** a passing integration test can mask the entire feature if the fixture
trips a gate earlier in the path than the one under test. Verify the assertion
fails when the feature is removed (red-when-reverted), and check fixture shapes
against *every* gate the path crosses, not just the gate you mean to test.

### 2. Three-way predicate divergence for one concept

"Source-bearing" was decided three different ways: `taskSrc != nil` in the
selectWorker filter, `Type != ""` in the held-count loop, and `Type ==
"perforce"` in the actual agent-side provider path - and a doc comment falsely
claimed two of them matched. The directions happen to agree today (Perforce is
the only provider), but it is a latent footgun: the moment a second provider type
lands, the three branches diverge silently. The two scheduler predicates were
unified behind one `taskIsSourceBearing` helper (now the single decision point
for both `selectWorker` and the held-pending count).

**Lesson:** when the same concept gates multiple branches, route them through one
predicate. A comment asserting "same condition as X" rots silently and is worse
than no comment, because it actively misleads the next reader.

### 3. Rolling-upgrade safety as a first-class design constraint

The load-bearing threat was a mixed fleet: server upgraded first, agents still on
the old binary. Proto3 decodes an absent plain `bool` as `false`, so an
absent-as-false design plus a false-defaulting column would have held *every*
source-bearing task across the entire fleet until all agents upgraded - a
self-inflicted outage for source workloads during the upgrade window. The fix was
a deliberate trio: `optional bool` (explicit presence; old agents send nothing),
`COALESCE(narg, existing)` (a nil param leaves the column untouched), and
`DEFAULT TRUE` (safe assumption: a worker can manage a workspace unless it
explicitly says it cannot, and it covers the first insert by an old agent).

**Lesson:** this is a reusable pattern for adding any agent-reported capability
bit to a fleet that upgrades in place - `optional` for presence, COALESCE to make
omission a no-op, and a safe column DEFAULT so the absence of the signal never
fences work off. Record it as the default approach for the next capability field.

## Files Most Touched

- `internal/scheduler/dispatch.go` - hard filter in `selectWorker`, the
  `taskIsSourceBearing` helper unifying both predicates, the per-cycle
  held-pending log.
- `internal/store/query/workers.sql` - COALESCE writes on
  `RegisterWorkerConnection` and `UpsertWorkerByHostname`.
- `internal/store/migrations/000017_workers_supports_workspaces.{up,down}.sql`.
- `proto/relayv1/relay.proto` - `optional bool supports_workspaces = 12`.
- `internal/agent/agent.go` - agent reports the bit from provider presence.
- `internal/worker/handler.go` - threads `reg.SupportsWorkspaces` through all
  three register paths.
- `internal/worker/handler_test.go` - integration coverage (capability persists;
  source task held pending; old-agent default-TRUE); the `Requires` fixture fix
  lives here.
- `internal/scheduler/select_worker_test.go` - unit coverage of the filter.
- `docs/backlog/closed/bug-2026-06-19-dispatch-provider-capability-filter.md` -
  closed.
</content>
</invoke>
