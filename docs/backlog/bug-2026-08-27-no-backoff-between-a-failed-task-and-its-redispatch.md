---
title: "A retried task is redispatched at full rate: there is no backoff anywhere on the retry path"
type: bug
status: open
created: 2026-08-27
priority: medium
source: Phase 1 spec of the retry-bounds slice (2026-08-27), while arguing the `retries` cap
---

# A retried task is redispatched at full rate: there is no backoff anywhere on the retry path

## Summary

Nothing on relay's retry path waits. `IncrementTaskRetryCount` sets `status = 'pending'`,
`handleTaskStatus` immediately calls `NotifyTaskSubmitted`, the dispatcher wakes, and
`GetEligibleTasks` selects the row on its next poll. A task whose command fails in a millisecond
therefore cycles at whatever rate Postgres and the gRPC stream will sustain, until its budget is
exhausted. The retry feature exists for transient faults - a flaky network mount, a contended
license server - and every one of those is a fault that a wait would clear and an immediate retry
will not.

## Repro / Symptoms

Submit a task with `retries: 10` and a command that exits non-zero immediately (`/bin/false`, or a
binary that does not exist). Observe: ten dispatch cycles complete in well under a second. Each one
writes a status transition, a `task_logs` row set, a `RecomputeJobStatus`, and a `pg_notify`. The
job then goes terminal, so the damage is bounded by the budget - which is exactly why the bound
mattered - but the work is done at full rate rather than spread over the interval during which the
transient fault might have cleared.

## Context

Found while arguing the `retries` cap in
`docs/superpowers/specs/2026-08-27-retry-bounds-and-budget-predicate.md`. The absence of backoff is
what made the cap argument come out low: with no wait between attempts, a large retry budget buys
no additional waiting for a fault to clear, only a faster burn, so the contended-license-server case
argues *against* a big number rather than for it. That is a strange conclusion, and the reason it is
strange is this defect.

Verified at HEAD: the only `backoff` in `internal/scheduler` is `notify.go`'s LISTEN/NOTIFY
reconnect, which is unrelated. `internal/store/query/tasks.sql` has no delay column and
`GetEligibleTasks` has no time predicate.

This item is a **consequence** of the retry-bounds slice's analysis, not a blocker for it. The bound
landing first is correct: it makes the unbounded case unreachable, which is what turns this from a
denial-of-service into a quality-of-implementation problem.

## Proposal

Sketch only; the design is brainstorming's job.

The natural shape is a `not_before TIMESTAMPTZ` column on `tasks`, set by
`IncrementTaskRetryCount` to `now() + f(retry_count)`, with `GetEligibleTasks` gaining
`AND (not_before IS NULL OR not_before <= now())`. That has a consequence worth naming up front:
the dispatcher currently wakes on notification and finds work or does not, so a time-gated task
needs something to wake the dispatcher when its wait expires. The existing 10s schedrunner ticker
and the dispatcher's own poll interval are candidates; a task whose `not_before` passes between
polls simply waits until the next one, which is acceptable if the minimum backoff is comfortably
above the poll interval.

Whether the delay is exponential, and whether it is configurable, are open. A fixed delay well
above the poll interval would close most of the value for a fraction of the design cost.

## Acceptance / Done When

- A task that fails and retries is not redispatched before its computed delay has elapsed, proven by
  a test that is RED against today's code.
- A task with no retry history is dispatched with no added delay - the positive control that keeps
  this from taxing the normal path.
- The dispatcher reliably picks up a task whose delay expires while nothing else is happening (no
  notification arrives to wake it). This is the part most likely to be missed, because it only fails
  on an idle system.
- Whatever bound is chosen is documented in README's job-spec table beside `retries`, since the two
  compose into the user-visible worst-case duration of a failing task.

## Related

- Source: `internal/store/query/tasks.sql` (`IncrementTaskRetryCount`, `GetEligibleTasks`),
  `internal/worker/handler.go` (the `NotifyTaskSubmitted` on the retry success path),
  `internal/scheduler/dispatch.go`
- Filed from: `docs/superpowers/specs/2026-08-27-retry-bounds-and-budget-predicate.md`
- The item whose slice found it: [[bug-2026-08-12-retries-unvalidated-and-budget-only-in-go]]
