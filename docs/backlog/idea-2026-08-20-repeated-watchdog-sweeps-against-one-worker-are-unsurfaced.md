---
title: Repeated watchdog sweeps against one worker are unsurfaced, so a wedged worker becomes a silent sink for queued work
type: idea
status: open
created: 2026-08-20
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

**The read surface is the shared expensive part**, and it is unchanged since 2026-08-15:
`internal/api/server.go` routes `GET /v1/config`, `GET /v1/jobs/stats`, `GET /v1/workers/stats` and
`GET /v1/workers/{id}/metrics`, and nothing that carries a server-wide counter. All three items
therefore either extend `GET /v1/workers/stats` or depend on
[[feature-2026-08-09-server-info-allowlist-endpoint]].

**This one has a genuinely easier answer than the other two, and that is worth noting at spec time.**
The other two must count on the recv goroutine, where the constraint is "no new lock, queue,
goroutine or round trip". The watchdog runs on its own goroutine on a 60s ticker with no hot path at
all, so a per-worker counter here is cheap by comparison - and unlike the other two, the underlying
event is **already durable in the database**: a `timed_out` row carries its `worker_id`, so a
`COUNT(*) ... WHERE status = 'timed_out' AND worker_id = $1 AND finished_at > $2` is available with
no new in-memory state whatsoever. That option does not exist for the sibling items and should be
weighed first here.

## Proposal

To be argued at spec time rather than adopted as written.

- **Prefer the query to the counter, if the numbers reconcile.** A swept task is a durable row with a
  worker id and a `finished_at`; a windowed count over `tasks` needs no process state, survives
  restarts, and is correct across replicas. The catch to settle: a `timed_out` row written by the
  **agent** (`handleTaskStatus`) is indistinguishable in the table from one written by the
  **watchdog**, and they mean opposite things about the worker's health - the first is the agent
  behaving correctly. If the query route is taken, the two must be distinguishable, which probably
  means a column or a distinct status and is the reason this may not be as cheap as it looks.
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
  diagnostic and matches where `metrics.Store` already keys.

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
  down.

## Related

- Source: `internal/scheduler/watchdog.go` (`SweepOnce`'s per-task log line; `sendCancel`, which
  discards `SendCancel`'s error and says why), `internal/worker/registry.go` (`SendCancel`),
  `internal/store/query/tasks.sql` (`CountActiveTasksByAllWorkers`, which is what makes the slot free
  the moment the row goes terminal), `internal/api/server.go` (the route table, which has no
  server-wide counter surface), `internal/api/workers.go` (`handleDisableWorker`, the existing
  operator remedy)
- Siblings on the same shape, to be specced together and shipped separately:
  [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]],
  [[idea-2026-08-15-ingest-log-suppression-is-uncounted]]
- Possible dependency for the read surface: [[feature-2026-08-09-server-info-allowlist-endpoint]]
- Adjacent, on what one sweep should say: [[bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick]]
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
endpoint work happens for any other reason, all three become small.
