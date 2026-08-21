---
title: The watchdog's error-branch log line repeats every tick for as long as a write failure persists
type: bug
status: open
created: 2026-08-20
priority: low
source: Phase 4 security lens of the 2026-08-20-coordinator-stale-task-watchdog slice
---

# The watchdog's error-branch log line repeats every tick for as long as a write failure persists

## Summary

In `Watchdog.SweepOnce` (`internal/scheduler/watchdog.go`), the per-row error branch logs a line for
a row it did **not** transition:

```go
if err != nil {
    if !errors.Is(err, pgx.ErrNoRows) {
        log.Printf("watchdog: UpdateTaskStatus(timed_out) for task %s: %v", uuidStr(t.ID), err)
    }
    continue
}
```

Because the write did not land, the row **stays in the scan partition** (`status IN
('dispatched','running')`, `worker_id IS NOT NULL`, still past its bound) and is returned by the very
next scan. So the same line is emitted again 60 seconds later, and again, for as long as the failure
persists. With N overdue rows and a persistent failure that is N lines per minute, indefinitely.

The failure class that produces it is a **partial degradation**: the scan succeeds and the write does
not. A statement timeout, a saturated connection pool, a lock wait, a serialization failure, a
transient network blip - anything that is not `pgx.ErrNoRows`. Those are exactly the conditions in
which a database is already under stress and log volume is least welcome.

**The comment four lines below argues the opposite case correctly and does not notice it does not
cover this one:**

> One line per SWEPT task, unbudgeted, and that is safe: the count per sweep is bounded by
> `WatchdogMaxRowsPerSweep`, **each task can be swept at most once (it is terminal afterwards)**, and
> nothing in the line is caller-supplied.

Every clause of that is true **of the success line**. The "swept at most once" clause is precisely
what the error line does not get, because the row it describes is the one that stayed non-terminal.
The claim and its counter-example are in the same screen of code.

## Repro / Symptoms

1. Seed one overdue assigned task (backdated `assigned_at`, `status = 'dispatched'`, `worker_id` set)
   and let the watchdog run.
2. Make `UpdateTaskStatus` fail with something other than `pgx.ErrNoRows` for the duration - a
   `statement_timeout` low enough to trip it, a `pg_advisory_lock` held on the row from another
   session, or a fault-injecting `watchdogStore` in a unit test.
3. Observed: one `watchdog: UpdateTaskStatus(timed_out) for task <uuid>: ...` line every 60 seconds
   per overdue row, forever, with no deduplication, no rate limit and no aggregation.

Expected: the condition is reported, and then reported at a bounded rate rather than at the sweep
cadence.

## Context

Found by the Phase 4 security lens of the watchdog slice, in the same pass that established the
success line is safe.

**This is not a new instance of the 2026-08-15 log-flood class**, and the distinction matters for how
it should be fixed. [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] (closed) was about an
**attacker-driven** line on the gRPC recv path: a caller chose the rate, and the remedy was a
per-connection token bucket sized against an adversary. Nothing here is caller-supplied and nothing
here is caller-triggered. The rate is set by `WatchdogSweepInterval` and the volume by the size of
the overdue set, both of which the operator controls. It is a **volume-during-an-incident** bug, not
a security bug, and it should not inherit a security-shaped remedy.

It is also genuinely low severity: the condition it floods on is one in which the operator wants to
know something is wrong, and the first few lines are the useful ones. What makes it worth a file is
that the flood happens exactly when the operator is trying to read the log for something else.

## Proposal

Two shapes, either acceptable. Argue at implementation time; do not do both.

- **Aggregate to one line per sweep.** Count failures inside the loop and emit
  `watchdog: N of M writes failed, last error: %v` after it, keeping the first error's text.
  Cheapest, needs no new state that outlives a sweep, and it is arguably the better log line anyway
  because "23 of 23 failed" is a diagnosis and 23 separate lines are not. It loses the per-task ids,
  which is an acceptable trade for a failure that is almost never per-task.
- **Or budget that one line**, with a small time-based re-arm keyed on nothing, in the shape of
  `ingestLogLimiter`'s token bucket. More machinery, and it imports a security-shaped tool for a
  non-security problem, but it preserves per-task detail for the low-rate case.

In either case:

- **Leave the success line alone.** Its argument is sound and the comment states it correctly.
- **Leave the `pgx.ErrNoRows` arm silent.** It is the correct outcome, not a failure, and the comment
  already says so.
- **Update the comment.** Whatever lands, the "each task can be swept at most once" sentence needs to
  say which line it is about, so the next reader does not re-derive the same false coverage. That is
  the durable half of this item: the code will be fine either way, and the comment is what led a
  careful reader to the wrong conclusion once already.

Worth looking at alongside [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]],
which also wants a once-per-sweep aggregate line. If both land, they should be one line, not two.

## Acceptance / Done When

- A persistent non-`ErrNoRows` write failure over an overdue set produces a bounded number of log
  lines per unit time, proven by a unit test with a fault-injecting `watchdogStore` and a frozen or
  advanced clock that runs many sweeps and asserts a bound on the captured log.
- The first occurrence is still reported promptly - a fix that suppresses the condition entirely, or
  reports it only after a delay, is worse than the bug.
- The success line's volume and wording are unchanged, asserted by an existing test staying green.
- The `pgx.ErrNoRows` arm still emits nothing, asserted directly (the existing whole-log-is-empty
  style, so any future wording on that arm reddens).
- The comment above the success line names which line its "swept at most once" argument covers.

## Related

- Source: `internal/scheduler/watchdog.go` - `SweepOnce`'s per-row error branch, and the comment above
  the success `log.Printf` whose argument does not extend to it
- The class this is **not**: [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] (closed),
  `internal/worker/ingest_log_limiter.go` (the token-bucket shape, if the budget route is chosen)
- Wants the same once-per-sweep line:
  [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]]
- The slice that introduced the line: `docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md`
  (section 8.1, "Logging"), `docs/retros/2026-08-20-coordinator-stale-task-watchdog.md`
- The item the slice closed: [[bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task]]

## Notes

The transferable observation is the same one this project recorded on 2026-08-15 as **"a principle in
a comment is not a check"**, arriving in its milder form: here the comment is not wrong about
anything it claims, it is just **scoped to the adjacent branch** and reads as if it covered both. A
correctness argument written next to one of two branches will be read as covering the pair. Say which
branch, or move the argument.
