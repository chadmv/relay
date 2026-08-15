---
title: Agent-ingest error logging is attacker-driven and unbounded (task-log limiter keyed on wire input; task-status path has no limiter at all)
type: bug
status: open
created: 2026-08-12
updated: 2026-08-14
priority: medium
source: Phase 4 review of the task-log assignee-fence iteration (2026-08-12); broadened to the status path by the task-status assignee-fence retro (2026-08-12)
---

# Agent-ingest error logging is attacker-driven and unbounded

Two instances of one defect on the gRPC recv goroutine, both reachable by any principal that can
open a `Connect` stream. **Fix them together**; they share a threat model, a cost model and a fix
shape, and splitting them would leave the same vector half-closed.

## Summary

### A. The task-log persist-failure limiter is keyed on attacker-controlled input

`taskLogErrLimiter.shouldLog` (`internal/worker/handler.go`, the `shouldLog` method) exists to stop a repeating
persist failure from flooding the server log from the gRPC recv goroutine. It keys on
`chunk.TaskId` and `int32(chunk.Epoch)` (the `shouldLog` call inside `handleTaskLog`'s persist-error branch), **both read straight off the
wire**, and on overflow it drops the whole map rather than suppressing:

```go
if len(l.reported) >= taskLogErrLimiterMax { // 1024
    l.reported = make(map[string]int32)
}
```

So the limiter bounds an *honest* repeating failure - one task streaming binary output, which is
what it was built for - and bounds nothing at all against a caller that varies the key. A caller
emitting a fresh random UUID per message gets one `log.Printf` per message, indefinitely.

### B. The task-status path logs twice before either gate, with no limiter at all

`handleTaskStatus` (`internal/worker/handler.go`) has two unconditional `log.Printf` calls that
run **ahead of both** the identity gate and the currency gate:

- **bad task id** - `log.Printf("worker: handleTaskStatus bad task id %q: %v", upd.TaskId, err)`, when
  `taskID.Scan` rejects the string.
- **GetTask failure** - `log.Printf("worker: handleTaskStatus GetTask %s: %v", upd.TaskId, err)`, when `GetTask`
  fails, which for a well-formed but nonexistent UUID is `pgx.ErrNoRows`.

Neither is rate-limited, and the 2026-08-12 assignee fence cannot help: both gates are downstream of
both lines. Any enrolled agent gets one unbounded log line per gRPC message by sending a stream of
`TaskStatusUpdate`s naming freshly generated random UUIDs, synchronously on the recv goroutine, ahead
of that worker's real status, log, inventory and telemetry ingest.

**This half is strictly more expensive per message than half A**, because the GetTask line is reached
*after* a `GetTask` round trip: each forged message consumes a pool connection and a query as well as
the global `log` mutex.

Neither line is a log-*injection* vector, and that should be preserved rather than rediscovered:
the bad-task-id line uses `%q`, which quotes and escapes, and the GetTask line is reachable only after `taskID.Scan`
succeeded, so the string has already been constrained to pgtype's accepted UUID forms (32 or 36
characters, hex plus hyphens). The problem is volume, not content.

Note the asymmetry with the log path, which is worth resolving deliberately in whichever direction:
`handleTaskLog`'s own `taskID.Scan` failure returns **silently** (the `taskID.Scan` guard at the top of `handleTaskLog`), so the log
path has no pre-gate log line at all.

## Repro / Symptoms

Any principal that can open a `Connect` stream (any valid long-lived agent token, or no credential
when `RELAY_ALLOW_AUTO_ENROLL` is on):

**A.** Send a stream of `TaskLogChunk`s where:

- `TaskId` is a freshly generated random UUID. The only upstream validation is
  `taskID.Scan(chunk.TaskId)` at the top of `handleTaskLog`, which checks that the string **parses** as a
  UUID. It need not name a real task.
- `Content` contains a NUL byte.

Each message then reaches `log.Printf`, for a specific reason worth stating precisely: Postgres
rejects a NUL byte in a text parameter with SQLSTATE `22021` while **decoding the bind parameter**,
before the fence CTE is evaluated. That was verified empirically during the 2026-08-12 review by
executing the generated statement against a real Postgres. So the error is a non-`pgx.ErrNoRows`
error - the branch guarded by `!errors.Is(err, pgx.ErrNoRows) && taskLogErrs.shouldLog(...)` - and
the fence's own rejection never gets the chance to short-circuit it. The task id being fictitious is
irrelevant, and so is the shipped assignee predicate.

**B.** Simpler, and needs no NUL byte or content trick at all. Send a stream of
`TaskStatusUpdate`s whose `TaskId` is a freshly generated random UUID. Every message costs a
`GetTask` and one `log.Printf` on the GetTask branch. Sending garbage that does not parse as a UUID hits the bad-task-id branch
instead, one line per message, without even the query.

Impact in both cases is a log flood plus lock and pool contention: these handlers run synchronously
on the `Connect` recv goroutine that also carries that worker's status, inventory and telemetry, and
`log.Printf` serializes on the `log` package's global mutex shared with every other connection. So
the cost lands on the attacker's own ingest path first, and on every other worker's second. This is
a degrade-the-server vector, not a data-integrity one.

## Context

Half A was found during Phase 4 of the task-log assignee-fence work. It corrects a claim in that
iteration's own spec (`docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md`
section 7), which argued against logging fence rejections partly on the grounds that the existing
limiter "bounds persist-failure logging to one line per task per epoch" and could be reused. It does
not, against an adversary. The spec now carries a dated correction; the correction makes that
section's conclusion *more* correct, not less, since logging rejections would have added a second
flood vector keyed on a fully attacker-controlled string with no containment.

**Still live as of 2026-08-12 (retry-resurrect status guard).** That iteration wrapped both of
`handleTaskStatus`'s *write-error* log sites in `!errors.Is(err, pgx.ErrNoRows)`, so a forged
message rejected by either SQL fence now logs nothing. It did **not** touch the two pre-gate
lines in half B, which remain unconditional and attacker-keyed. That iteration's Phase 4 review
corrected a comment in `handleTaskStatus` that had cited this item as a reason the Go identity
gate saves log lines: it does not, because the pre-gate lines run ahead of the gate and the
post-gate ones are now silent.

Half B was found while writing the task-status assignee-fence retro
(`docs/retros/2026-08-12-taskstatus-update-assignee-fence.md`). Note that the same spec's section 3.3
identified this shape as a reason the status identity check had to go in Go rather than in SQL: an
SQL-only fence would have turned every forged status message into a `log.Printf` on
`UpdateTaskStatus`'s `pgx.ErrNoRows`. That vector was avoided, but the two pre-existing pre-gate
lines above it were not addressed, and they are the same defect.

### Update 2026-08-14 - deliberately NOT folded into the trailing-window slice, and why

The `2026-08-14-tasklog-terminal-append-bound` slice shipped next door (it bounded how long a
terminal task accepts log appends) and **deliberately left this item alone**. Recorded here because
ROADMAP.md had scheduled the two as one slice, and that recommendation was wrong:

- ROADMAP's rationale was "both are `handleTaskLog` and both fence the same wire-supplied fields".
  **Both halves are false.** This item's half B is in `handleTaskStatus`, a different handler and a
  different message type, and the item requires both halves be fixed together - so "one slice" would
  have been a three-handler slice. And **a logging rate limiter fences nothing**: there is no
  predicate here and no wire-supplied field being validated. The shared shape is a coincidence of
  file, not of mechanism. That rationale survived two roadmap refreshes unchecked.
- **The two changes provably do not interact**, and this is now evidence rather than an argument:
  `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` passed with **no edit at all** after
  the fence gained a third predicate. It does so for the reason half A's repro describes - NUL-bearing
  content fails during bind-parameter **decode**, before the fence CTE is ever evaluated - so the new
  predicate is unreachable from this item's test, and this item's test is unreachable from it.
- **This item should still go next, and second**, so it can reuse the trailing-window slice's handler
  fixtures and be written by somebody who has just read `handleTaskLog` closely.

**One new constraint that slice creates for this one.** `AppendTaskLog`'s `pgx.ErrNoRows` now has a
**third** meaning - "the trailing window has closed" - alongside stale generation and wrong assignee,
and the third one occurs legitimately. So the "should a rejection log?" question this item touches is
now strictly more settled in the "no" direction: a log line on that branch would fire on honest late
flushes as well as forged chunks. The diagnosability gap that creates is filed separately as
[[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]], whose proposal is a counter through
`Handler.Metrics` and **explicitly not a log line**. If both are scheduled, they should agree on one
mechanism rather than inventing two.

## Proposal

**A.** Key the limiter on something the sender does not control. The connection's authenticated
worker UUID is already in scope in `handleTaskLog` as of the 2026-08-12 change (it is the value the
fence binds), so the cheapest correct version keys on `(workerID, taskID, epoch)` and caps **per
worker** rather than globally - a misbehaving agent then cannot consume another agent's budget, and
cannot exceed its own.

**B.** The status path's authenticated worker UUID is likewise already in scope (`handleTaskStatus`
gained it on 2026-08-12), so the same per-connection budget covers both lines. Settle two things
first:

- **Whether the GetTask branch should log at all when the error is `pgx.ErrNoRows`.** A status update naming a
  task that does not exist is indistinguishable from a forged one, carries no diagnostic value the
  operator can act on, and is exactly the case an attacker drives. Dropping it silently, as the log
  path already does for its own parse failure, may be the whole fix for that line - and it is
  cheaper than a limiter. Real errors (a pool failure, a context cancellation) should still log,
  under the shared budget.
- **Whether the bad-task-id branch should log at all.** `handleTaskLog`'s equivalent returns silently. Pick one behavior for both handlers and say why in a comment; the current
  split is an accident, and a future reader will otherwise "fix" the inconsistency in whichever
  direction they happen to notice first.

Points to settle for the shared mechanism:

- **Per-worker cap versus a token bucket.** A per-worker map still grows with distinct task ids from
  one worker, so it needs its own bound; on overflow, prefer *suppressing* to resetting, because a
  reset is what converts overflow into "log everything again". A small per-connection token bucket
  (say N lines per minute, shared across every log line the connection can trigger) is simpler to
  reason about, is bounded by construction, and is the version that naturally covers both handlers.
  Either is acceptable; pick one and say why in the comment.
- **Where the state lives.** Today `taskLogErrs` is a package-level var with a
  `ResetTaskLogErrLimiterForTest` hook. Per-connection state would more naturally hang off the
  connection, which removes the global mutex and the test reset hook, and is what makes one budget
  serve both handlers - but it touches `Connect`'s shape. Weigh that against the standing constraint
  on this path: no new query, no goroutine, no queue on the recv goroutine.
- **Do not lose the diagnostic.** The realistic honest failure (a job writing binary stdout) must
  still produce exactly one identifiable line, and a new assignment generation must still earn one
  more. The existing test is the contract; it should pass unchanged, or any needed assertion change
  is itself a finding.
- **Never log `chunk.Content`.** The existing comment block explains why `%v` on the pgx error is
  safe (`pgconn.PgError.Error()` renders severity, message and SQLSTATE, never `Detail`). Preserve
  that. Preserve the `%q` on the bad-task-id line too, for the same class of reason.

## Acceptance / Done When

- A single connection emitting `TaskLogChunk`s with distinct random task ids and NUL-bearing content
  produces a bounded number of log lines, proven by a test that is RED against today's limiter.
- A single connection emitting `TaskStatusUpdate`s naming distinct random (well-formed) task ids
  produces a bounded number of log lines, and a bounded number of `GetTask` round trips, proven by a
  test that is RED against today's handler.
- A single connection emitting `TaskStatusUpdate`s whose task ids do not parse produces a bounded
  number of log lines, or none, per the decision above.
- One misbehaving worker cannot suppress or consume another worker's logging budget, on either path.
- Overflow suppresses rather than resetting, or the chosen alternative is bounded by construction
  and the reasoning is in the comment.
- `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` still passes with no assertion
  changed.
- `handleTaskLog` still performs exactly one DB round trip and one statement, and `handleTaskStatus`
  gains no round trip; no query, goroutine or queue is added to the recv goroutine path.
- Chunk content still never reaches the log, and neither log line becomes injectable (`%q` on the
  unparsed id, no `pgErr.Detail`).

## Related
- Source, log path: `internal/worker/handler.go` - `taskLogErrLimiterMax`, the `taskLogErrLimiter`
  type and its `shouldLog` method, the `taskID.Scan` at the top of `handleTaskLog`, and the
  `shouldLog` call in that function's persist-error branch;
  `internal/worker/handler_tasklog_integration_test.go`,
  `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch`
- Source, status path: `internal/worker/handler.go`, `handleTaskStatus` - both pre-gate log lines,
  with the identity and currency gates that cannot help them further down the same function
- Corrects section 7 of `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md`;
  extends the flood-vector reasoning in section 3.3 of
  `docs/superpowers/specs/2026-08-12-taskstatus-update-assignee-fence.md`
- The tasklog spec's section 10 lists "rate limiting the gRPC message loop" as out of scope; a
  per-connection bucket here is the narrow version of that, and if a general recv-loop limiter is
  ever built this item folds into it
- Why this was not folded into the 2026-08-14 slice, with the evidence:
  `docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md` section 5,
  `docs/retros/2026-08-14-tasklog-terminal-append-bound.md`
- Should agree on one mechanism with: [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]]
- Adjacent: [[bug-2026-08-12-tasklog-epoch-int32-truncation]], same call site, same wire-supplied
  epoch value

## Notes
The general shape is worth naming beyond this one map: **a rate limiter keyed on a field the
rate-limited party supplies is not a rate limiter**, and **an unlimited log line placed before an
authorization gate is not protected by that gate**. Anything on the agent-facing ingest path that
dedupes, caches, bounds or logs by a wire value has the same defect by construction, so it is worth
a grep for other map keys and log arguments derived from `chunk.*` or `upd.*` while fixing these
two.

**Citations in this item are by symbol, not by line offset (converted 2026-08-12).** They had gone
stale twice in a row: `handleTaskStatus` grew by about 40 lines in the task-status assignee fence
and by roughly 30 more in the retry-resurrect status guard, so every offset this item carried was
wrong within days of being written. Cite symbols here.
