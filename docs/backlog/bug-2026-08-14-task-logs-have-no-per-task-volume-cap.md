---
title: A single task's log stream has no row or byte cap, live or finished
type: bug
status: open
created: 2026-08-14
priority: medium
source: spec section 10 and Phase 4 security lens of the 2026-08-14-tasklog-terminal-append-bound slice; deliberately out of scope there
---

# A single task's log stream has no row or byte cap, live or finished

## Summary

`AppendTaskLog` (`internal/store/query/tasks.sql`) inserts one `task_logs` row per
`TaskLogChunk` with no cap of any kind. There is no per-task row count, no per-task byte total, no
per-job total, and no rate limit on the gRPC recv goroutine that carries the chunks.

The 2026-08-14 trailing-window bound closed the **duration** half of this for *finished* tasks: a
terminal task now stops accepting appends `RELAY_TASKLOG_TRAILING_WINDOW` after its `finished_at`.
It closed nothing about **rate**, and nothing at all for a task that is still `running`. A principal
holding a worker's agent token with one live assignment can still write unbounded durable rows.

Per-chunk size is bounded only by gRPC's default 4 MB receive limit, and each insert is one round
trip on the sender's own recv goroutine, so the practical ceiling is thousands of small rows per
second per stream. Nothing prunes the table afterwards - see
[[idea-2026-08-14-task-logs-retention-and-pruning]].

## Repro / Symptoms

No user-visible symptom short of storage exhaustion. Demonstrable at the handler layer: claim a task,
leave it `running`, and send `TaskLogChunk`s in a loop at the correct epoch as the genuine assignee.
Every chunk is stored and published. The count is limited by nothing in the server.

An honest (non-adversarial) version of the same thing is a job whose command writes a multi-gigabyte
build log, or a tool that emits progress on every line of a large file. That case is the reason the
fix has to be a product decision about a size ceiling rather than a security threshold.

## Context

Filed out of `docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md` section 10, which
rejected folding a cap into the trailing-window slice for reasons worth preserving:

- A cap needs either a `COUNT` subquery on the recv path or a counter column on `tasks` (a migration
  plus a write on every chunk). The standing constraint on `handleTaskLog` is **exactly one DB round
  trip, one statement, no goroutine, no queue**, and both shapes push against it.
- It does not bound the indefinite case any better than time does, so it was not a substitute for
  the trailing window.
- **It is not specific to terminal tasks.** A live task is equally uncapped, so this belongs in an
  item about log volume, not in a fix for a window that never closed.

The Phase 4 security lens restated the same split in the scope language this item inherits: the
trailing window bounds the **post-terminal arm**; rate is a separate arm and so is a stale `running`
task (see [[bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task]]).

## Proposal

Settle three things at spec time. None is obvious and picking wrong is worse than not shipping.

- **Where the counter lives.** A `COUNT(*)` subquery inside the existing fence CTE costs an index
  scan per chunk on `idx_task_logs_task_id_id` and keeps the single-round-trip property. A
  `log_rows`/`log_bytes` counter column on `tasks` is cheaper to read and costs a write amplification
  on the hottest statement in the system plus a migration. Measure before choosing; do not assume the
  subquery is too expensive because it looks like one.
- **Rows, bytes, or both.** Rows alone is trivially defeated by 4 MB chunks; bytes alone lets a
  million one-byte chunks through and it is *rows* that cost the index. Bytes is the number an
  operator can reason about ("no task may produce more than 512 MB of log"), so bytes is probably the
  headline with a row cap as the cheap secondary.
- **What the ceiling is, and whether it is per task or per job.** This is a product question about how
  large a legitimate build log is, and the answer must have a number behind it the way
  `RELAY_TASKLOG_TRAILING_WINDOW`'s default does. Env-configurable per the project's standing rule on
  operational limits.

Two constraints the design must respect:

- **The rejection must stay silent**, joining the existing `pgx.ErrNoRows` drop before the publish. A
  log line here hands back the flood vector [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]]
  tracks. This is the same trade the trailing window took, and it creates the same diagnosability
  cost - so this item should land **after or together with**
  [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]], or a truncated log becomes a third
  indistinguishable silent failure.
- **Truncation must be visible to the reader, even if it is silent to the writer.** Unlike a stale
  epoch, hitting a cap is a normal operational event whose consequence a user sees (their log stops).
  Consider a synthetic terminal marker row, or a flag on the task, so the SPA can say "log truncated
  at N MB" rather than presenting a truncated log as a complete one. That is the part of this item
  that is a feature, not a control.

## Acceptance / Done When

- A task that has reached its configured cap rejects further chunks, proven by a test that is RED
  against today's code, at the handler layer, on a **live** (`running`) task - not only a finished one.
- A task below the cap is unaffected, including one that has been appended to many times.
- `handleTaskLog` still performs exactly one DB round trip and one statement; no goroutine and no
  queue is added to the recv goroutine.
- No new log line on the rejection path.
- The cap is env-configurable with a documented default and a README row that says what happens when
  it is hit.
- A truncated log is distinguishable from a complete one by a reader, or the decision not to do that
  is written down with its reason.

## Related

- Source: `internal/store/query/tasks.sql` (`AppendTaskLog`), `internal/worker/handler.go`
  (`handleTaskLog`), `internal/store/migrations/000018_*` (`idx_task_logs_task_id_id`, the only
  maintenance this table has ever received)
- The slice that bounded the other half: `docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md`,
  `docs/retros/2026-08-14-tasklog-terminal-append-bound.md`, and the closed
  [[bug-2026-08-12-tasklog-terminal-task-append-unbounded]]
- The storage half: [[idea-2026-08-14-task-logs-retention-and-pruning]]
- The other unbounded arm: [[bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task]]
- Must not regress: [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] (no log line on rejection)
- Should land with: [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]]
- Read side of the same table: [[idea-2026-08-09-task-log-tail-and-paging-improvements]]

## Notes

The useful framing to preserve: **the trailing-window fix bounded duration, and duration was the
easier half.** Duration has a defensible number behind it (the worst case for a legitimately late
chunk, derived from three agent-side timers). Volume does not: the ceiling is whatever the largest
legitimate build log in the deployment is, which nobody in this project has measured. That is why
this is a separate item and why its first task is a measurement, not a predicate.

**A new producer, and the first one whose rate is a constant rather than a consequence of
what a task prints.** The p4 sync heartbeat writes one durable row per interval per
syncing task for as long as the sync runs: 2/min at the 30s default, 12/min at the 5s
floor. An 8-hour sync is about 960 rows at the default, times the fleet's slot count.

The qualitative change matters more than the number. Before it, `p4 sync -q` plus no
heartbeat meant a multi-hour sync wrote **zero** durable rows - a task that produced no
output cost nothing. It now costs `duration / interval` rows unconditionally.

The interval is operator-set (`RELAY_SYNC_HEARTBEAT_INTERVAL`) and not caller-set, and
concurrency is bounded by fleet slots rather than by `maxTasksPerJob`, so this is a
constant factor on the existing amplifier rather than a new one. But whichever cap this
item lands on has to bound a **steady low-rate producer**, not just a burst of subprocess
output - a cap sized for "a task printed a lot" may not be the control a heartbeat needs.
