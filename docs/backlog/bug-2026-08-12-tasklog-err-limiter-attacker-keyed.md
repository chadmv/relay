---
title: The task-log persist-failure limiter is keyed on attacker-controlled input, so it bounds nothing
type: bug
status: open
created: 2026-08-12
priority: medium
source: Phase 4 review of the task-log assignee-fence iteration (2026-08-12)
---

# The task-log persist-failure limiter is keyed on attacker-controlled input, so it bounds nothing

## Summary
`taskLogErrLimiter.shouldLog` (`internal/worker/handler.go:572-586`) exists to stop a repeating
persist failure from flooding the server log from the gRPC recv goroutine. It keys on
`chunk.TaskId` and `int32(chunk.Epoch)` (the call at `handler.go:649`), **both read straight off the
wire**, and on overflow it drops the whole map rather than suppressing:

```go
if len(l.reported) >= taskLogErrLimiterMax { // 1024
    l.reported = make(map[string]int32)
}
```

So the limiter bounds an *honest* repeating failure - one task streaming binary output, which is
what it was built for - and bounds nothing at all against a caller that varies the key. A caller
emitting a fresh random UUID per message gets one `log.Printf` per message, indefinitely.

## Repro / Symptoms
Any principal that can open a `Connect` stream (any valid long-lived agent token, or no credential
when `RELAY_ALLOW_AUTO_ENROLL` is on) sends a stream of `TaskLogChunk`s where:

- `TaskId` is a freshly generated random UUID. The only upstream validation is
  `taskID.Scan(chunk.TaskId)` at `handler.go:609`, which checks that the string **parses** as a
  UUID. It need not name a real task.
- `Content` contains a NUL byte.

Each message then reaches `log.Printf`, for a specific reason worth stating precisely: Postgres
rejects a NUL byte in a text parameter with SQLSTATE `22021` while **decoding the bind parameter**,
before the fence CTE is evaluated. That was verified empirically during the 2026-08-12 review by
executing the generated statement against a real Postgres. So the error is a non-`pgx.ErrNoRows`
error - the branch guarded by `!errors.Is(err, pgx.ErrNoRows) && taskLogErrs.shouldLog(...)` - and
the fence's own rejection never gets the chance to short-circuit it. The task id being fictitious is
irrelevant, and so is the freshly shipped assignee predicate.

Impact is a log flood plus lock contention: `handleTaskLog` runs synchronously on the `Connect` recv
goroutine that also carries that worker's status, inventory and telemetry, and `log.Printf`
serializes on the `log` package's global mutex shared with every other connection. So the cost lands
on the attacker's own ingest path first, and on every other worker's second. This is a
degrade-the-server vector, not a data-integrity one.

## Context
Found during Phase 4 of the task-log assignee-fence work. It corrects a claim in that iteration's own
spec (`docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md` section 7), which argued
against logging fence rejections partly on the grounds that the existing limiter "bounds
persist-failure logging to one line per task per epoch" and could be reused. It does not, against an
adversary. The spec now carries a dated correction; the correction makes that section's conclusion
*more* correct, not less, since logging rejections would have added a second flood vector keyed on a
fully attacker-controlled string with no containment.

The limiter's honest-failure behavior is well covered by
`TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch`
(`internal/worker/handler_tasklog_integration_test.go:274`), which is worth reading before changing
anything here: it pins one line per task per epoch, that a new assignment generation earns one more
line, that a stale-epoch drop stays silent, and that chunk content never reaches the log.

## Proposal
Key the limiter on something the sender does not control. The connection's authenticated worker UUID
is already in scope in `handleTaskLog` as of the 2026-08-12 change (it is the value the fence binds),
so the cheapest correct version keys on `(workerID, taskID, epoch)` and caps **per worker** rather
than globally - a misbehaving agent then cannot consume another agent's budget, and cannot exceed its
own.

Points to settle:

- **Per-worker cap versus a token bucket.** A per-worker map still grows with distinct task ids from
  one worker, so it needs its own bound; on overflow, prefer *suppressing* to resetting, because a
  reset is what converts overflow into "log everything again". A small per-connection token bucket
  (say N lines per minute) is simpler to reason about and is bounded by construction. Either is
  acceptable; pick one and say why in the comment.
- **Where the state lives.** Today `taskLogErrs` is a package-level var with a
  `ResetTaskLogErrLimiterForTest` hook. Per-connection state would more naturally hang off the
  connection, which removes the global mutex and the test reset hook, but touches `Connect`'s shape.
  Weigh that against the standing constraint on this path: no new query, no goroutine, no queue on
  the recv goroutine.
- **Do not lose the diagnostic.** The realistic honest failure (a job writing binary stdout) must
  still produce exactly one identifiable line, and a new assignment generation must still earn one
  more. The existing test is the contract; it should pass unchanged, or any needed assertion change
  is itself a finding.
- **Never log `chunk.Content`.** The existing comment block explains why `%v` on the pgx error is
  safe (`pgconn.PgError.Error()` renders severity, message and SQLSTATE, never `Detail`). Preserve
  that.

## Acceptance / Done When
- A single connection emitting messages with distinct random task ids and NUL-bearing content
  produces a bounded number of log lines, proven by a test that is RED against today's limiter.
- One misbehaving worker cannot suppress or consume another worker's logging budget.
- Overflow suppresses rather than resetting, or the chosen alternative is bounded by construction
  and the reasoning is in the comment.
- `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` still passes with no assertion
  changed.
- `handleTaskLog` still performs exactly one DB round trip and one statement; no query, goroutine or
  queue is added to the recv goroutine path.
- Chunk content still never reaches the log.

## Related
- Source: `internal/worker/handler.go:545-592` (`taskLogErrLimiterMax`, `taskLogErrLimiter`) and
  `:649` (the call site), `internal/worker/handler_tasklog_integration_test.go:274`
- Corrects section 7 of `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md`
- The same spec's section 10 lists "rate limiting the gRPC message loop" as out of scope; a
  per-connection bucket here is the narrow version of that, and if a general recv-loop limiter is
  ever built this item folds into it
- Adjacent: [[bug-2026-08-12-tasklog-epoch-int32-truncation]], same call site, same wire-supplied
  epoch value

## Notes
The general shape is worth naming beyond this one map: **a rate limiter keyed on a field the
rate-limited party supplies is not a rate limiter.** Anything on the agent-facing ingest path that
dedupes, caches or bounds by a wire value has the same defect by construction, so it is worth a grep
for other map keys derived from `chunk.*` or `upd.*` while fixing this one.
