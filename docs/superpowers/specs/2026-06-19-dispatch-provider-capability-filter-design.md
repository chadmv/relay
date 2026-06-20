---
date: 2026-06-19
topic: dispatch-provider-capability-filter
type: design-spec
status: draft
backlog: bug-2026-06-19-dispatch-provider-capability-filter
predecessor: bug-2026-06-10-source-tasks-run-without-workspace (closed 2026-06-19)
---

# Dispatch Provider-Capability Filter

## Problem

`selectWorker` (`internal/scheduler/dispatch.go:130-202`) has no notion of which
workers can manage a workspace. A source-bearing task (one with a non-empty
`task.Source`, today always a Perforce stream) can be dispatched to a worker whose
agent has no workspace provider configured (`RELAY_WORKSPACE_ROOT` unset or its
Perforce preflight failed). Warm-workspace affinity is only a positive score bonus,
not a hard requirement, so dispatch never *avoids* a providerless worker.

The predecessor fix (`bug-2026-06-10`) added an agent-side guard: a source-bearing
task on a providerless worker now emits `TASK_STATUS_PREPARE_FAILED`, which the
server maps to a terminal `"failed"`. So the failure is loud rather than silent.
But the task can be re-dispatched to *another* providerless worker on each retry,
burning its entire retry budget without ever reaching a capable worker, and a task
with no `retries` configured fails permanently the first time it lands wrong even
though a capable worker may exist or may appear shortly.

This spec makes provider capability a hard scheduling constraint: a source-bearing
task is only ever dispatched to a worker that reports a workspace provider, and if
no such worker is currently available the task stays `pending` until one is.

## Scope (and non-scope)

In scope: report a single workspace-provider capability bit per worker, persist it,
filter on it in `selectWorker` for source-bearing tasks, and surface the "held, no
capable worker" condition in logs.

Explicitly NOT in scope (YAGNI - the acceptance criteria do not require any of it):

- A general capability/requirement matching system. Label matching already exists
  (`LabelMatch`); this is one fixed boolean, not a new label namespace or a
  capabilities map.
- A new task status (e.g. `unschedulable` / `held`). The task simply stays
  `pending`. Argued under Question 3.
- Per-provider-type capability (e.g. "supports perforce" vs "supports git"). There
  is exactly one provider type today (Perforce). A single `supports_workspaces`
  boolean is sufficient; richer typing is a future spec if a second provider lands.
- Any change to the agent-side runtime guard or the `PREPARE_FAILED -> failed`
  mapping. Those stay as the backstop for the race where a worker's provider
  disappears between registration and dispatch.

## Design Decisions

### Question 1: How does a worker report workspace-provider capability?

**Decision.** Add a boolean `supports_workspaces` to the `RegisterRequest` proto
message. The agent sets it at registration time from a fact it already holds:
`provider != nil` on the `*agent.Agent`. Persist it to a new
`workers.supports_workspaces` column (migration 000017) written on the same upsert
paths that already write hardware specs.

**Proto.** Add field 12 to `RegisterRequest`:

```proto
message RegisterRequest {
  // ... existing fields 1-11 ...
  bool supports_workspaces = 12;
}
```

Field 12 is the next free tag; the `oneof credential` occupies 9/10 and `inventory`
is 11. A proto3 `bool` defaults to `false` when absent, which matters for old
agents (Question 4) - so the *persisted default* must differ from the proto default;
see below.

**Agent.** `Agent.buildRegisterRequest` (`internal/agent/agent.go:241`) already
branches on `a.provider` for inventory (`if il, ok := a.provider.(source.InventoryLister)`).
Set `req.SupportsWorkspaces = a.provider != nil` there. This is the single source of
truth for "this agent can manage a workspace": `cmd/relay-agent/main.go:70-107`
leaves `provider` nil whenever `RELAY_WORKSPACE_ROOT` is unset *or* the Perforce
preflight fails, which is exactly the providerless condition the runtime guard keys
on. Reusing `provider != nil` keeps the registration claim and the runtime guard
consistent by construction.

**Persistence: DB column, not Registry-only.** `selectWorker` iterates
`workers []store.Worker` returned by `d.q.ListWorkers(ctx)` (`dispatch.go:74`),
which are DB rows. It never consults the in-memory `worker.Registry` (that holds
only `Sender` streams, not worker metadata). Putting the capability anywhere other
than the `workers` row would force `selectWorker` to grow a second lookup against a
different structure and cross a lock to read it. A column on `workers` rides the
existing `ListWorkers` read with zero new queries and is naturally a value field on
the copied `store.Worker`, so the no-interior-pointers-across-locks invariant is
satisfied trivially (there is no pointer and no lock - it is a plain column on a
value-copied row).

The column is written on the two upsert paths the agent already exercises:

- `UpsertWorkerByHostname` (`internal/store/query/workers.sql:53`) - used by both
  `enrollAndRegister` and `autoEnrollAndRegister`. Add `supports_workspaces` to the
  insert column list, the `VALUES`, the `ON CONFLICT DO UPDATE SET`, and the
  explicit `RETURNING` list. Add the param to `UpsertWorkerByHostnameParams` callers
  in `handler.go` (set from `reg.SupportsWorkspaces`).
- The `reconnectAndRegister` path does NOT call `UpsertWorkerByHostname` (it looks
  the worker up by agent-token hash, `handler.go:227`). A long-lived agent that
  toggles its provider config and reconnects would otherwise keep its stale
  capability. To keep the bit fresh on every connect, write it in
  `RegisterWorkerConnection` (`workers.sql:30`), which already runs on *every*
  `finishRegister` (`handler.go:294`). Add `supports_workspaces = $N` to that UPDATE
  and pass `reg.SupportsWorkspaces` from `finishRegister`. This makes
  `RegisterWorkerConnection` the single authoritative write of the capability on
  every connection, and lets the `UpsertWorkerByHostname` change be optional - but we
  keep both so a freshly-inserted worker row has a correct value before
  `RegisterWorkerConnection` runs. (Decision: write it in `RegisterWorkerConnection`
  as the authoritative path; the `UpsertWorkerByHostname` write is for insert-time
  correctness only. Both pull from the same `reg.SupportsWorkspaces`.)

`reg` is the `*relayv1.RegisterRequest` already threaded through every register
path, so no new plumbing is needed beyond the new proto field.

### Question 2: selectWorker as a hard filter

**Decision.** "Source-bearing" is determined exactly as the existing warm-scoring
code determines it: the task has a parseable non-empty `task.Source`. `selectWorker`
already parses this into `taskSrc *api.SourceSpec` at lines 145-152 (`taskSrc != nil`
iff `len(task.Source) > 0` and the JSON unmarshals). Reuse that variable - do not
add a second notion of "source-bearing."

Add a hard-skip guard inside the worker loop, after the existing eligibility checks
(status, disabled, reserved, labels, free slots) and **before** the warm-affinity
scoring block. The natural placement is immediately after the `free <= 0` check
(`dispatch.go:177-180`) and before `score := free`:

```
// Source-bearing tasks require a worker with a workspace provider. Skipping
// providerless workers here (rather than scoring them lower) is the hard
// requirement: a task whose Source is set must never be dispatched to a worker
// that will only PREPARE_FAILED it.
if taskSrc != nil && !w.SupportsWorkspaces {
    continue
}
```

Placing it after the cheap eligibility checks keeps the common non-source path on
its existing fast path. For a non-source task `taskSrc == nil`, the guard is a no-op
and every eligible worker remains a candidate exactly as today - this is what keeps
acceptance bullet 3 (non-source tasks unaffected) true by construction.

The guard sits before scoring, so a providerless worker is never even considered as
`best`, and the warm-affinity bonus (which only ever applies when `taskSrc != nil`)
can never resurrect one.

### Question 3: Unschedulable / hold path

**Decision.** No new mechanism and no new task status. When every worker is filtered
out, `selectWorker` returns `nil` (its `best` stays nil), and the existing dispatch
loop (`dispatch.go:120-127`) simply does not call `sendTask` for that task. The task
is never claimed, so it stays `pending` and is re-evaluated on the next dispatch
cycle - which fires on the 30s safety-net ticker, on `Trigger()`, and on NOTIFY when
a worker (re)connects via `finishRegister`'s `go h.triggerDispatch()`. So a task held
for lack of a capable worker is automatically re-attempted the moment a capable
worker registers. **This "stays pending" behavior already exists today** for any
task with no eligible worker (e.g. all workers busy or label-mismatched); the
capability filter is just one more reason `selectWorker` can return nil, and it
inherits the same safe hold semantics for free.

Why no new status: a `pending` task with no capable worker is operationally
identical to a `pending` task whose workers are all busy - both are "waiting for a
suitable worker." Introducing an `unschedulable` status would require a migration, a
state-machine transition back to `pending` when a capable worker appears (with its
own epoch-fence considerations), UI work, and risks a task getting stuck in a
terminal-looking state. The acceptance criterion is "stays pending (not failed) and
the condition is observable" - `pending` + a log line meets it without any of that
cost. This mirrors the predecessor's YAGNI call (map `PREPARE_FAILED` to existing
`failed` rather than mint a new status).

**Observability (the minimal addition).** "Stays pending" is silent today, so add a
log line. The dispatch loop must distinguish "no worker because all busy" (normal,
noisy, do not log) from "source-bearing task with zero *capable* workers" (the
condition we want observable). `selectWorker` already returns only `*store.Worker`;
to surface the reason without reworking its signature, have the dispatch loop log
when a source-bearing task got no worker AND no online/stale worker advertises
`supports_workspaces`:

- In `dispatch()`, after `ListWorkers`, compute once per cycle a cheap boolean
  `anyProviderWorker` = "exists a worker with status online/stale, not disabled, not
  reserved, with `SupportsWorkspaces`." (Reuse the same eligibility predicate as the
  loop, or a simplified online/stale + SupportsWorkspaces check - exact predicate
  decided at plan time; the cheap version is acceptable since this is advisory.)
- In the per-task loop, when `w == nil` and the task is source-bearing and
  `!anyProviderWorker`, log once: `dispatch: task %s requires a workspace provider
  but no capable worker is connected; holding pending`. Throttle to avoid log spam
  on the 30s ticker - log at most once per task ID per N minutes, or gate on a
  per-cycle "first such task only" flag. **Decision: log at most one such line per
  dispatch cycle** (not per task), naming the count of held source-bearing tasks,
  e.g. `dispatch: N source-bearing task(s) held pending; no connected worker has a
  workspace provider`. One line per cycle is enough to make the condition observable
  in logs and on the 30s ticker is at most 2 lines/minute - acceptable, no
  dedup-map state to maintain.

No SSE/event change is in scope. The task already publishes no event while pending;
adding a "held" event is a possible future enhancement (see Open Questions) but is
not required by the acceptance criteria.

### Question 4: Backward / rolling-upgrade compatibility

**Decision.** The `workers.supports_workspaces` column defaults to `TRUE`.

The threat is a rolling upgrade or mixed fleet: the server is upgraded first and
starts enforcing the filter while agents are still on the old binary. Old agents do
not send field 12, so proto3 decodes `reg.SupportsWorkspaces` as `false`. If we
treated absent-as-false and the column also defaulted false, **every** source-bearing
task would be held pending across the entire fleet until all agents are upgraded -
a self-inflicted outage for source workloads during the upgrade window.

Defaulting the column `TRUE` makes the safe assumption "a worker can manage a
workspace unless it explicitly says it cannot." Consequences:

- A pre-field agent that never sends the field: the column keeps its `TRUE` default
  on insert. But note `RegisterWorkerConnection` (Question 1) writes
  `reg.SupportsWorkspaces` on every connect, and an old agent sends `false` there.
  This is the rolling-upgrade hazard. **Mitigation: gate the write on a non-default
  signal.** Proto3 cannot distinguish "absent" from "explicit false" for a plain
  `bool`. To make old-agent reconnects safe we make the proto field
  `optional bool supports_workspaces` (proto3 explicit-presence). The agent always
  sets it (it always knows `provider != nil`), so new agents send an explicit
  true/false. Old agents send nothing -> server sees "not present" -> server does
  NOT overwrite the column (leaves the `TRUE` default / prior value). New agents send
  presence -> server writes the real value. This preserves the safe default for old
  agents while letting new agents report `false` accurately.

  Implementation note: `optional bool` in proto3 generates a `*bool` getter
  (`GetSupportsWorkspaces()` returns the value, `SupportsWorkspaces *bool` for
  presence). The server writes the column only when the pointer is non-nil; pass a
  nullable param to `RegisterWorkerConnection` / `UpsertWorkerByHostname` and have the
  SQL `COALESCE(sqlc.narg(supports_workspaces), supports_workspaces)` so a NULL param
  leaves the existing value untouched (and the column DEFAULT TRUE covers the very
  first insert by an old agent).

- A new agent with no provider sends explicit `false` -> column set false -> its
  source-bearing tasks are correctly held off it. Correct.

- A new agent with a provider sends explicit `true` -> column true. Correct.

This is the one genuinely load-bearing compat decision and is called out again under
Risks.

## Files Changed

Proto / generated:

- `proto/relayv1/relay.proto` - add `optional bool supports_workspaces = 12;` to
  `RegisterRequest`. Run `make generate` to regenerate `relay.pb.go` (do not hand-edit
  generated files; follow the CLAUDE.md LF/CRLF clean-up note after generation).

Agent:

- `internal/agent/agent.go` - in `buildRegisterRequest`, set
  `req.SupportsWorkspaces = proto.Bool(a.provider != nil)` (or equivalent for the
  optional field). No other agent change; the runtime guard stays as the backstop.

Store (SQL, then `make generate`):

- `internal/store/migrations/000017_workers_supports_workspaces.up.sql` -
  `ALTER TABLE workers ADD COLUMN supports_workspaces BOOLEAN NOT NULL DEFAULT TRUE;`
- `internal/store/migrations/000017_workers_supports_workspaces.down.sql` -
  `ALTER TABLE workers DROP COLUMN supports_workspaces;`
- `internal/store/query/workers.sql` -
  - `RegisterWorkerConnection`: add `supports_workspaces =
    COALESCE(sqlc.narg(supports_workspaces)::bool, supports_workspaces)` to the SET
    clause (authoritative per-connect write; NULL leaves value unchanged).
  - `UpsertWorkerByHostname`: add the column to insert list / VALUES /
    `ON CONFLICT DO UPDATE SET` (COALESCE same way) / explicit RETURNING list.
  - (Generated `*.sql.go`, `models.go` update via `make generate`; never hand-edit.)

Server:

- `internal/worker/handler.go` - pass `reg.GetSupportsWorkspaces()` presence through
  `RegisterWorkerConnectionParams` in `finishRegister`, and through
  `UpsertWorkerByHostnameParams` in `enrollAndRegister` / `autoEnrollAndRegister`.

Scheduler:

- `internal/scheduler/dispatch.go` -
  - `selectWorker`: add the `taskSrc != nil && !w.SupportsWorkspaces { continue }`
    guard before scoring.
  - `dispatch()`: compute `anyProviderWorker` once per cycle and emit the one
    held-pending log line per cycle when source-bearing tasks were filtered out.

Tests: see Success Criteria.

## Invariant Compliance

- **Single job-spec pipeline.** No new spec struct or task-creation path. "Source-
  bearing" reuses the existing `task.Source` JSON parsed into `api.SourceSpec`, the
  same type the warm-scoring path already uses. No `jobspec.TaskSpec` field is added.
- **No interior pointers across locks.** The capability is a plain `bool` column on
  the value-copied `store.Worker` row from `ListWorkers`. `selectWorker` reads
  `w.SupportsWorkspaces` off a row it already owns; it never touches `worker.Registry`
  or any locked structure for this. The Registry getters are unchanged.
- **Epoch fence.** This change touches no `tasks.status` or `task_logs` write. A held
  task is simply never claimed, so it stays `pending` with no status write at all -
  the epoch fence is not engaged because no assignment is created or ended. The
  `RegisterWorkerConnection` change adds one column to an UPDATE that already runs per
  connect; it does not alter `connection_epoch` semantics (it still bumps the epoch).
- **One bounded sender per gRPC stream.** No change to the send path; the capability
  is read-only scheduling metadata, kept entirely out of the hot write/send paths.
- **Single JSON entry point.** No HTTP body handling changes.
- **Identity-checked teardown.** Untouched.

## Success Criteria

Mapped to the three acceptance bullets, each with its test.

**Bullet 1: A source-bearing task is never dispatched to a worker without a
workspace provider.**

- Unit (`internal/scheduler/select_worker_test.go`, in-package, no Docker): given a
  source-bearing task and a single eligible online worker with
  `SupportsWorkspaces=false`, `selectWorker` returns `nil`. Given the same task and a
  worker with `SupportsWorkspaces=true`, it returns that worker. Given a mix
  (providerless + provider-capable), it returns the provider-capable one even when
  the providerless worker has more free slots. Extend `baseWorker`/`baseTask` helpers
  (add a source-bearing task helper and set the worker bool).
- Integration (`internal/worker/handler_test.go` or
  `internal/scheduler/*_integration_test.go`, Docker/Postgres): register a worker that
  reports `supports_workspaces=false`, submit a source-bearing job, run one dispatch
  cycle, assert the task is still `pending` and no `ClaimTaskForWorker` occurred for
  it (worker received no `DispatchTask`).

**Bullet 2: When no capable worker exists, the task stays pending (not failed) and
the condition is observable.**

- Unit: covered by the "returns nil" assertion above (task is never claimed ->
  remains pending).
- Integration: extend the bullet-1 integration test to assert the task status is
  `pending` (not `failed`) after the dispatch cycle, and capture log output to assert
  the held-pending line is emitted exactly once for the cycle. (If capturing the log
  is awkward, the observable assertion is the persisted `pending` status plus the
  unit-level guarantee that the log path is reached; decide at plan time whether to
  assert on captured logs.)

**Bullet 3: Non-source tasks are unaffected and still schedule on any eligible
worker.**

- Unit: a non-source task (`task.Source` empty) is dispatched to a worker with
  `SupportsWorkspaces=false` (the guard is a no-op). This is the regression guard
  proving the filter is scoped to source-bearing tasks only.
- Existing `select_worker_test.go` cases (stale/online eligible, offline excluded,
  label match, slot exhaustion) continue to pass unchanged - they use the default
  `SupportsWorkspaces` (false in zero-value `store.Worker`); since those tasks are
  non-source the guard does not fire, so they are unaffected. (Confirm the in-memory
  `baseWorker` zero value of `false` does not break existing non-source cases - it
  must not, because the guard only triggers for source-bearing tasks.)

Integration test for the registration->capability->dispatch round trip exercises:
agent reports the bit -> `RegisterWorkerConnection` persists it -> `ListWorkers`
returns it -> `selectWorker` honors it. This is the end-to-end proof the column,
proto field, and filter are wired together.

## Risks and Open Questions

- **Rolling-upgrade default (load-bearing).** Resolved as `DEFAULT TRUE` + `optional
  bool` so old agents do not strand the fleet's source tasks and new agents report
  accurately. Risk if we got presence-handling wrong: an old agent's plain `false`
  overwriting a `true` column would hold its source tasks. The `optional`/COALESCE
  design specifically prevents this. Verify at implementation that sqlc generates a
  nullable param for the COALESCE narg and that the agent always sets presence.
- **Stale capability between connects.** Capability is refreshed on every connect via
  `RegisterWorkerConnection`. If an agent's provider config changes *while connected*
  (preflight starts failing mid-session), the column is not updated until the next
  reconnect. The agent-side runtime guard (`PREPARE_FAILED -> failed`) remains the
  backstop for this window. Acceptable: this is a narrow race and the predecessor's
  guard already covers it; live re-advertisement of capability is out of scope.
- **Provider-capable worker that is busy.** A source-bearing task with capable
  workers that are all at capacity is held pending by the *existing* slot logic, and
  our per-cycle log only fires when `!anyProviderWorker`. So "all capable workers
  busy" stays silent (correct - that is normal backpressure, not an unschedulable
  condition).
- **Open: should a held source-bearing task surface in the API/UI?** Out of scope
  here; the acceptance criterion is met by `pending` + logs. If operators want a
  first-class "blocked: no capable worker" signal in the jobs/tasks API, that is a
  follow-up backlog item (a `blocked_reason` or an SSE "held" event), not required by
  this bug.
- **Open: log throttling granularity.** Decision is one line per dispatch cycle; if
  that proves too noisy or too coarse in practice, a per-task-ID dedup with a TTL is a
  cheap follow-up. Not building the dedup map now (YAGNI).

## Predecessor Linkage

This closes the dispatch-side gap explicitly deferred by
`bug-2026-06-10-source-tasks-run-without-workspace` (retro
`docs/retros/2026-06-19-nil-provider-source-guard.md`, "Known Limitations"). The
agent-side guard and `PREPARE_FAILED -> failed` mapping from that work are retained
as the runtime backstop; this spec adds the dispatch-time avoidance so the backstop
is rarely hit and a source task's retry budget is no longer burned bouncing between
providerless workers.
