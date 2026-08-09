---
title: Any enrolled agent can append log lines to any never-claimed task via Epoch 0
type: bug
status: open
created: 2026-08-09
priority: medium
source: Phase 4 review of the SSE task-log publishing iteration (2026-08-09)
---

# Any enrolled agent can append log lines to any never-claimed task via Epoch 0

## Summary
`handleTaskLog` performs no worker-identity check - it fences only on
`tasks.assignment_epoch` matching the epoch the agent sent. `assignment_epoch` defaults to `0`
(`internal/store/migrations/000004_assignment_epoch.up.sql:1`), so any enrolled agent can append
arbitrary log content to any task that has never been claimed by sending `Epoch: 0` with that
task's id. The fence stops a *stale* agent from writing to a task that has since been reassigned;
it does not establish that the sender is the task's assignee.

## Repro / Symptoms
An enrolled agent (a valid long-lived agent token, which is all `Connect` requires) sends a
`TaskLogChunk` with another job's pending `task_id` and `Epoch: 0`. The fence CTE in
`AppendTaskLog` matches, because the target task really is at epoch 0, and the chunk is persisted
against a task the sender was never assigned. The forged line is then indistinguishable from real
output in `GET /v1/tasks/{id}/logs`.

## Context
Pre-existing, not introduced by the SSE task-log publishing work - but that work amplifies the
consequence, so it was flagged during review rather than left implicit. Before, a forged line only
landed in the DB and surfaced on the next poll. Now it is also fanned out live to every SSE
subscriber tailing that task, so a forged line can appear in an operator's live log view in real
time.

Worth reading together with the observation that bearer auth on `GET /v1/events` is checked once at
connect, so a revoked or expired token keeps receiving live log content for the life of the held
connection. Neither property widens what a token can *read* relative to the polling endpoint - both
are about write forgery and session lifetime respectively.

## Proposal
Check the sender's identity, not just the epoch. `Connect` already knows which worker the stream
belongs to (that is what `RequeueWorkerTasks` and the grace registry rely on), so the natural fix
is to fence on assignee **and** epoch: extend `AppendTaskLog`'s fence to
`WHERE t.id = $1 AND t.assignment_epoch = $2 AND t.worker_id = $3`, passing the connection's own
worker id. That closes the epoch-0 hole without a new round trip, since it stays one statement.

Confirm the column name and how a claimed task records its worker before implementing - read
`ClaimTaskForWorker` in `internal/store/query/` rather than trusting the sketch above. Check
whether any legitimate path appends logs for a task with no assignee (a task that failed before
dispatch, for instance); if so, that path needs its own handling rather than a blanket epoch-0
allowance.

## Acceptance / Done When
- An agent that is not a task's assignee cannot append log lines to it, including when the task is
  at epoch 0, proven by a test that is RED against today's code.
- The existing stale-epoch rejection still works (a reassigned generation is still refused).
- No extra DB round trip on the log-ingest path - the bounded-sender invariant still holds, and
  `handleTaskLog` stays one statement.

## Related
- Source: `internal/worker/handler.go` (`handleTaskLog`), `internal/store/query/tasks.sql`
  (`AppendTaskLog`, whose fence CTE this extends),
  `internal/store/migrations/000004_assignment_epoch.up.sql`
- Amplified by the live fan-out added in the 2026-08-09 SSE task-log publishing work; see
  `docs/superpowers/specs/2026-08-09-sse-task-log-publishing.md`
- Adjacent: [[feature-2026-06-26-audit-log-admin-console-actions]]

## Notes
The epoch fence is doing exactly the job it was designed for - preventing a stale assignment
generation from writing - and this is not a bug in the fence. It is a missing second check that the
fence was never meant to provide. Worth stating that way in the fix so the invariant's purpose does
not get muddled.
