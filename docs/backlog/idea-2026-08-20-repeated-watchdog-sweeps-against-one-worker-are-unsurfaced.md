---
title: Repeated watchdog sweeps against one worker are unsurfaced, so a wedged worker becomes a silent sink for queued work
type: idea
status: open
created: 2026-08-20
updated: 2026-08-21
priority: medium
source: Phase 4 security lens of the 2026-08-20-coordinator-stale-task-watchdog slice; the diagnosability cost that slice accepted
---

# Repeated watchdog sweeps against one worker are unsurfaced, so a wedged worker becomes a silent sink for queued work

## Summary

The coordinator stale-task watchdog (`internal/scheduler/watchdog.go`) ends an overdue assignment by
stamping `timed_out`, which **frees the worker's slot** - `CountActiveTasksByAllWorkers` counts only
`status IN ('dispatched','running')`, so the moment the row goes terminal the dispatcher considers
that worker to have capacity again.

The coordinator has no way to **compel** the agent to stop. `Watchdog.sendCancel` calls
`Registry.SendCancel(workerID, taskID, false)` and **discards the return value**, deliberately: the
watchdog is registry-blind by design, the agent may be connected to a different replica, and
`CancelTask` is a message to an untrusted peer that is free to ignore it.

Put together, a wedged or hostile worker changes shape rather than getting better. Before the
watchdog it held a **fixed** set of tasks forever. Now it **drains** queued work at roughly
(slots / max-assignment) and fails each item, indefinitely. Neither behaviour is clearly worse than
the other - the second at least keeps the job status machine moving and lets an operator see failures
- but **nothing surfaces the pattern**, and the pattern is the actionable part.

**Repeated sweeps against the same `worker_id` are the tell that a worker should be disabled**, and
there is no counter, no metric and no aggregated log line that exposes it. `SweepOnce` logs one line
per swept task, which names the worker, but nothing aggregates by worker and nothing survives the
process. An operator has to read the raw log and notice a repeating UUID.

## Repro / Symptoms

1. Run an agent patched to accept dispatches, report `RUNNING`, and never report terminal (or simply
   ignore `CancelTask`). Give it a slot count of 4.
2. Submit a stream of tasks. Every ~`RELAY_TASK_MAX_ASSIGNMENT` (24h by default; set it to `1h` to
   observe in an afternoon), the watchdog sweeps that worker's four tasks, marks them `timed_out`,
   cascades their transitive dependents to `failed`, and frees four slots.
3. The dispatcher immediately hands the same worker four more tasks.
4. Observed: an unbounded number of jobs fail over time, attributable to one machine, and the only
   evidence is N lines per sweep in the server log with the same worker UUID in each. Nothing in
   `GET /v1/workers`, `GET /v1/workers/stats` or `GET /v1/workers/{id}/metrics` reflects it, and the
   worker's `last_seen_at` stays fresh because its stream is healthy.

Expected: something an operator can query that says "worker X has had 37 assignments swept in the
last 24 hours", which is a disable decision with one number behind it.

## Context

Found by the Phase 4 security lens of the watchdog slice, while pricing what that slice's fix does
**not** buy. The slice's own Known Limitations record the mechanism ("the freed slot is optimistic -
the coordinator releases the worker's slot while the subprocess may still be running, so a machine
with a wedged task can be handed more work"); this item is the observability half of that sentence.

**This is a sibling of two open items, not an amendment to either, and the split is deliberate.**

- [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]] is scoped to `handleTaskLog`'s
  `pgx.ErrNoRows` arm - a **chunk rejected by the fence** - and its acceptance criteria are about a
  rejection counter.
- [[idea-2026-08-15-ingest-log-suppression-is-uncounted]] is scoped to `ingestLogLimiter.allow`'s two
  `return false` paths - a **log line dropped** - across five kinds and three handlers.
- This one counts a **third noun** in a **fourth place**: an *assignment terminated by the
  coordinator*, on a periodic writer in `internal/scheduler`, not on the gRPC recv path at all.

All three are instances of the same shape - "the system now silently drops or kills something and
nobody can see it" - and all three want the same read surface. **Spec them in one sitting and ship
them separately**, exactly as the 2026-08-15 sibling already recommends for the first two. Folding
this into either would widen it from one arm in one handler to a different package, and this project
keeps finding items that are wrong about their own scope precisely because somebody grew one by
amendment.

## 2026-08-21: the read surface exists. This item is now the LAST slice of four, not the first.

`docs/superpowers/specs/2026-08-21-silent-drop-observability.md` specced all four items in one
sitting, and **slice 1 shipped the shared mechanism** (`docs/retros/2026-08-21-silent-drop-observability-slice1.md`).
The expensive shared part every version of this item deferred to is settled. What changed for this
item specifically:

**What now exists and must be extended, not reinvented:**

- **`GET /v1/server/counters`**, `auth(admin(...))`, in `internal/api/server_counters.go`. Admin-only
  deliberately: these numbers describe adversary activity and internal control state, so it is NOT
  modelled on `/v1/jobs/stats` or `/v1/workers/stats`, which are `auth`-only database censuses.
- **`api.CounterSources`** - a struct of nil-able per-subsystem source fields, set at the wiring
  boundary. A **nil field means the section is ABSENT from the payload, never zero-valued**: a section
  of zeros means "this control ran and stopped nothing", an absent section means "this build or this
  replica does not have that control wired". Do not collapse the two.
- **The counts/levels contract.** `counts` are monotonic since `started_at`; `levels` are current. A
  reporter may consult `counts` to decide whether to speak and may **never** consult `levels`. This
  item's aggregate sweep line is exempt from the comparison problem because it is driven by the sweep
  itself rather than by a counter-move test - but the monotonic-versus-current classification is part
  of the payload contract and applies here regardless.
- **Per replica, per process, zeroed by a restart**, with `started_at` always present. The watchdog is
  multi-replica-safe by first-write-wins, so a sweep of worker X may be counted on either replica. Say
  so in the field documentation.

**THE HARD CONSTRAINT, and it is the one thing here that will otherwise be rediscovered as a compile
error under time pressure: `internal/scheduler` ALREADY IMPORTS `internal/api`** (`scheduler/dispatch.go`,
`scheduler/source_proto.go`). So `internal/api` can **never** import `internal/scheduler`, and this
section therefore **cannot** follow slice 1's pattern of "the source interface returns the subsystem's
own type". The required shape is:

- declare the watchdog snapshot type (`WatchdogCounters` or similar) **inside `internal/api`**, next to
  the other response types;
- declare `type WatchdogSource interface { CounterSnapshot() WatchdogCounters }` **inside
  `internal/api`**;
- have `scheduler.Watchdog` return that type - legal, because scheduler already imports api.

`CounterSources` is a struct of independent fields precisely so each section can make that choice
separately. The note is already in `server_counters.go`'s doc comment; it is repeated here so this
item carries it.

**THE TYPED-NIL TRAP, which is not hypothetical for this section.** The watchdog is legitimately
disable-able (`RELAY_TASK_WATCHDOG_MARGIN=0`, `RELAY_TASK_MAX_ASSIGNMENT=0`), so
`var wd *scheduler.Watchdog; if enabled { wd = ... }; CounterSources{Watchdog: wd}` is the natural
shape **and it panics**: a typed nil pointer stored in an interface is not `== nil`, so the handler's
`src != nil` is true and the snapshot call dereferences a nil receiver - a goroutine stack trace to
the log per admin request, inside the feature whose subject is bounding log volume. Filter the typed
nil at the wiring boundary where the concrete type is still visible (`cmd/relay-server`'s
`buildHTTPServer` is the live example, guarded by
`TestBuildHTTPServer_TypedNilListenerLeavesTheSectionAbsent`). Do **not** instead make the snapshot
method nil-tolerant: returning a zero snapshot turns an unwired control into a section of zeros, which
is the one distinction this payload exists to preserve.

**THE EXEMPTION-PREDICATE RULE, and `swept_by_worker` was DELIBERATELY DE-AUTHORIZED.** Slice 1's spec
pre-blessed `watchdog.counts.swept_by_worker` in the payload's non-integer allow-list, against code
nobody had written. **That entry was removed during slice 1's review**, and the removal is the point:
pre-authorizing it reduced its only forcing function to a one-line edit with the justification already
supplied. A map keyed on server-resolved worker UUIDs may well still be the right answer here - but it
now costs a `counterPayloadExemption{why, typeOK, jsonOK}` argued **in the same commit that can be read
against the code**, including whether unbounded key cardinality is acceptable. The admin-authentication
argument (this route is not an attacker-writable site, so a worker UUID admissible HERE stays
inadmissible in any log line reachable from the gRPC recv path) is one input to that decision, not a
standing grant.

**And the residual that entry must be written knowing:** exemptions are shape-checked but
**NON-DESCENDING** - both payload walks stop at an exempted path once the predicate passes. That is
right for a scalar like `started_at`, whose predicate examines the whole value. It is **wrong for a
container**: a `jsonOK` that merely accepted `map[string]string` would leave every key and every value
uninspected, which is the total exemption the predicate mechanism replaced, re-entered through the
predicate. A `swept_by_worker` exemption must do the descending itself inside `typeOK`/`jsonOK` -
checking key shape, value shape and cardinality - or the walks must first be taught to recurse past it.
Slice 1 proved this is not theoretical: a `map[string]string` at an exempted path, with a
newline-injected RTL-override key and an IP-address value, passed both guards with zero failures.

**What the spec decided about this item's own design** (`spec` sections 3.1, 7.2 and 10.4), to be
verified against code rather than adopted, per this project's standing rule:

- **The item's "genuinely easier answer than the other two" framing is REFUTED in emphasis.** The
  premise (the event is already durable in `tasks`) is true and the conclusion does not follow. This is
  the **hardest** of the four: the only one whose correct-by-construction route is blocked and whose
  fallback needs the only unbounded-in-principle key in the cluster.
- **The DB-query route is rejected for now, with a revisit condition to record in source.** A windowed
  `COUNT(*)` is better on every axis except one, and that one is fatal: an **agent** writes `timed_out`
  itself (`handler.go` maps `TASK_STATUS_TIMED_OUT` straight through), and the two writers mean opposite
  things about the worker's health. Distinguishing them needs a new terminal status (which must then be
  threaded through every status allow-list, including the two that must be read backwards -
  `AppendTaskLog`'s first arm and `ListOverdueAssignedTasks` - plus `TestTasksStatusVocabularyIsExactly`)
  or a nullable `timed_out_by`-style column plus a migration on an epoch-fenced write path. **If such a
  column is ever added for another reason, revisit this** - write that in the comment where the counter
  lives.
- **The in-process per-worker map is capped**: `sweptByWorker map[string]uint64` at 256, first-come
  rather than top-K, with a `sweptOverflow` plain total for sweeps attributable to untracked workers and
  a `sweptTotal` that always counts every sweep so the two reconcile. Cumulative since process start, no
  rolling window. Read under the `Watchdog`'s own mutex and **copied out** - no interior pointer escapes
  the lock. Unbounded is not an option while
  [[bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded]] is open.
- **`bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick` folds into this slice.** Both items
  want a once-per-sweep aggregate line; shipping them separately produces two lines and a third item to
  reconcile them.

## Proposal

To be argued at spec time rather than adopted as written. **Superseded in part by the 2026-08-21
section above**; where the two disagree, the spec's reasoning is the later and better-evidenced one,
but verify it against the code rather than inheriting it.

- **Prefer the query to the counter, if the numbers reconcile.** A swept task is a durable row with a
  worker id and a `finished_at`; a windowed count over `tasks` needs no process state, survives
  restarts, and is correct across replicas. The catch to settle: a `timed_out` row written by the
  **agent** (`handleTaskStatus`) is indistinguishable in the table from one written by the
  **watchdog**, and they mean opposite things about the worker's health - the first is the agent
  behaving correctly. If the query route is taken, the two must be distinguishable, which probably
  means a column or a distinct status and is the reason this may not be as cheap as it looks.
  **(2026-08-21: rejected on exactly this, with a revisit condition. See above.)**
- **Otherwise, an in-process per-worker counter on the `Watchdog`**, flushed nowhere and read through
  the endpoint. Note that it is per replica and say so, since a fleet with two relay-servers splits
  its sweeps arbitrarily between them.
- **Aggregate the log line as well as, or instead of, counting.** One "watchdog: swept N tasks across
  M workers; worst: worker X with K" line per sweep is close to free and is the smallest thing that
  makes the pattern visible in an existing log pipeline. It also interacts with
  [[bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick]] - both are about what one sweep
  should say - so the two should be looked at together even though they are separate items.
- **Do not auto-disable a worker on a sweep count.** Tempting and wrong at this stage: the threshold
  is a product decision nobody has made, the failure mode of a wrong threshold is taking a healthy
  machine out of a fleet, and the existing `handleDisableWorker` path already gives an operator a
  one-call remedy once they can see the number. Surface first, automate later if asked.
- **Answer "per worker or global" once, for all three sibling items.** Per worker is the useful
  diagnostic and matches where `metrics.Store` already keys. **(2026-08-21: answered. This is the only
  one of the four with a per-worker key; the other three are process-wide or level-only. And
  `metrics.Store` is the WRONG home for any of them - `Append` no-ops for an untracked worker and
  `Clear` deletes the entry on teardown, so a counter there is destroyed by the disconnect that caused
  it. The `Metrics` WIRING pattern is the precedent; the type is not.)**

## Acceptance / Done When

- A repeated sweep against one worker is visible to an operator through an endpoint, not only by
  reading raw log lines, with at least a count over a stated window.
- A watchdog-written `timed_out` is distinguishable from an agent-written one, or the chosen design
  explains why conflating them is acceptable for this signal.
- Whatever is added is per worker, and its per-replica versus fleet-wide semantics are documented.
- No new lock, goroutine or round trip on the gRPC recv path (this item does not touch it, and the
  constraint is stated so a "unify all three counters" refactor cannot quietly violate it).
- The counters or query results cannot be read by an agent - server-side observability, never a
  response on the worker stream.
- The read surface is the one the two sibling items use, or the divergence is deliberate and written
  down. **(2026-08-21: it is `GET /v1/server/counters`. The divergence budget is spent.)**
- **(2026-08-21) The watchdog snapshot type is declared in `internal/api`**, because
  `internal/scheduler` imports `internal/api` and the reverse import is impossible.
- **(2026-08-21) A disabled watchdog leaves the section ABSENT and does not panic**, with the typed nil
  filtered at the wiring boundary rather than by making the snapshot method nil-tolerant.
- **(2026-08-21) Any non-integer field added to the payload - `swept_by_worker` included - ships with a
  `counterPayloadExemption` whose `typeOK`/`jsonOK` predicates descend into the container**, argued in
  the same commit. `swept_by_worker` carries no standing pre-authorization.
- **(2026-08-21) `bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick` is closed by the same
  slice**, with ONE aggregate line covering both the swept set and the failed-write set.

## Related

- Source: `internal/scheduler/watchdog.go` (`SweepOnce`'s per-task log line; `sendCancel`, which
  discards `SendCancel`'s error and says why), `internal/worker/registry.go` (`SendCancel`),
  `internal/store/query/tasks.sql` (`CountActiveTasksByAllWorkers`, which is what makes the slot free
  the moment the row goes terminal), `internal/api/workers.go` (`handleDisableWorker`, the existing
  operator remedy)
- **The read surface, shipped 2026-08-21**: `internal/api/server_counters.go` (the payload contract for
  all four sections, the import-direction note, the typed-nil note),
  `internal/api/server_counters_test.go` (`counterPayloadExemption` and the two payload walks),
  `internal/api/server.go` (the route), `cmd/relay-server/http_server.go` (`buildHTTPServer`, the wiring
  boundary)
- Siblings on the same shape, to be shipped separately:
  [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]],
  [[idea-2026-08-15-ingest-log-suppression-is-uncounted]]
- Adjacent, on what one sweep should say, and **to be folded into this slice**:
  [[bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick]]
- Why the per-worker map must be capped: [[bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded]]
- The joint spec and the slice that settled the mechanism:
  `docs/superpowers/specs/2026-08-21-silent-drop-observability.md` (sections 3.1, 7.2, 10.4),
  `docs/superpowers/plans/2026-08-21-silent-drop-observability-slice1.md` (R2, the import direction),
  `docs/retros/2026-08-21-silent-drop-observability-slice1.md`
- The slice that created this gap: `docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md`
  (section 11, "the freed slot is optimistic"),
  `docs/retros/2026-08-20-coordinator-stale-task-watchdog.md`
- The item the slice closed: [[bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task]]

## Notes

The rule worth recording, and it is the same one the ingest-limiter item recorded from the other
direction: **a mechanism that quietly cleans up after a bad actor converts a loud failure into a
quiet one.** Before the watchdog, a wedged worker announced itself by holding a job in progress
forever - ugly, but impossible to miss. After it, the jobs complete (as failures) and the fleet looks
like it is working. That is a real improvement and a real regression in detectability, and the second
half only gets recorded if somebody writes it down at the time.

Filed at medium rather than low because the sink behaviour is unbounded in the number of jobs it can
fail, and because two sibling items are already waiting on the same endpoint decision. If the
endpoint work happens for any other reason, all three become small. **(2026-08-21: the endpoint work
has now happened. This item did not become small - it became the LAST of the four, because the
endpoint was never its hard part. Its hard part is the per-worker key and the writer ambiguity, both
untouched.)**
