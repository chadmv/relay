---
title: The server discards the agent's prepare-failure error message, so a failed sync leaves a task with empty logs
type: bug
status: closed
created: 2026-09-03
closed: 2026-09-03
resolution: fixed
priority: high
source: SDNM fork divergence analysis (relay_updates.md, PR-7), evaluated 2026-09-03
---

# The server discards the agent's prepare-failure error message, so a failed sync leaves a task with empty logs

## Summary

`Runner.Run` sends `TASK_STATUS_PREPARE_FAILED` with `ErrorMessage` set to the provider's error
(a p4 sync failure, a missing workspace provider). `handleTaskStatus` maps that status onto `failed`
and never reads `ErrorMessage` - nothing under `internal/worker/` references the field. The task
shows `failed` with no log line saying why. A deployment running a fork of relay hit this in
production and documents it as "P4-source task fails at prepare with empty logs".

## Repro / Symptoms

Submit a task with a `source` block whose stream does not exist on the p4 server. The agent's
prepare fails, the task goes `failed`, and `GET /v1/tasks/{id}/logs` is empty. The only record of
the cause is the agent process's own stdout, if anyone kept it.

## Context

The fork's fix appends the message to `task_logs` through `AppendTaskLog` with the caller's epoch,
the connection's worker id, and a `MinFinishedAt` derived from the trailing-log window - the same
fence arguments `handleTaskLog` already computes. That shape is correct and invariant-clean. Two
things it leaves out are below.

This was invisible to every test lane because no test drives a real agent's prepare failure through
gRPC into a `task_logs` read; see [[idea-2026-08-25-no-e2e-path-from-agent-subprocess-to-task-logs]].

## Proposal

Work in `internal/worker/handler.go`, `handleTaskStatus`, after the identity gate, the currency
gate and the enum-to-string mapping, and BEFORE the retry branch and the `UpdateTaskStatus` write.
Writing before the terminal write matters: the row is still non-terminal, so the first arm of
`AppendTaskLog`'s predicate admits the chunk and the trailing window is only a backstop.

1. When `upd.ErrorMessage != ""`, bound the message (a package constant, on the order of 4 KiB; the
   field is agent-controlled and unbounded on the wire) and call `AppendTaskLog` with:
   `TaskID`, `Stream` set to whatever stream name `handleTaskLog` stores for
   `LOG_STREAM_PREPARE` chunks so the CLI and the SPA group it with the sync output,
   `Content` of the form `[failed] <message>\n`, `AssignmentEpoch: int32(upd.Epoch)` (the
   currency gate already compared it against the int32 column), `WorkerID` from the connection
   (never from the wire), and `MinFinishedAt` resolved exactly as `handleTaskLog` resolves it from
   `h.TrailingLogWindow` and `DefaultTrailingLogWindow`.
2. Gate the side effect on the fence having matched. `pgx.ErrNoRows` is dropped silently, joining
   the existing rejection counter if `taskLogFenceRejects` is the right home; no log line (the
   attacker-keyed volume argument in the existing comments applies unchanged).
3. On a match, publish the log event through the same broker path `handleTaskLog` uses, and do it
   BEFORE the status-change event for the terminal transition. The CLI's log follower stops at the
   terminal frame, and the SPA's tail stops on a terminal status, so a line published after the
   status event is a line the live view never shows.
4. Apply it for any status carrying a message, not only `PREPARE_FAILED`; today only the two
   prepare paths set the field, but the handler should not know that.
5. README: in the source-provider section and the task-log section, say that a prepare failure's
   cause is the last line of the task log, and name the stream it lands on.

Tests, with the handler as the subject, in the integration lane beside the existing
`handler_tasklog` tests. Write the first one RED at HEAD before touching the handler.

- The assignee reports `PREPARE_FAILED` with a message: exactly one `task_logs` row exists
  afterwards, its content contains the message, and the broker saw the log event before the status
  event.
- A different registered worker sends the same message for the same task: zero rows, and the
  status-fence counters do not move.
- A stale epoch: zero rows.
- A message longer than the bound: one row, truncated to the bound.
- An empty message: zero rows (no blank line).
- Comment discipline per CLAUDE.md: the handler comment states the ordering hazard (write before the
  terminal write; publish before the status event) and names the test that pins it, nothing else.

## Acceptance / Done When

- A task whose prepare fails carries the provider's error as a log line readable through
  `GET /v1/tasks/{id}/logs`, `relay logs`, and the SPA's task log view, live and on refresh.
- A non-assignee or stale-epoch report writes nothing and is not logged.
- The message is bounded, and the bound is a named constant.
- README documents where the line appears.

## Related

- `internal/worker/handler.go` - `handleTaskStatus` (the write site), `handleTaskLog` (the fence
  arguments and the publish path to copy)
- `internal/agent/runner.go` - the two `PREPARE_FAILED` sends that set `ErrorMessage`
- [[idea-2026-08-25-no-e2e-path-from-agent-subprocess-to-task-logs]] - why this was never observed
- [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]] - the bound on the message is a small
  instance of the general cap that item owns
- [[bug-2026-09-03-perforce-virtual-and-remap-streams-fail-to-sync]] - the failure that made the
  empty log visible in production

## Resolution

Fixed on `claude/top-3-roadmap-items-65856f`. `handleTaskStatus` now writes the agent's
`ErrorMessage` through `AppendTaskLog` with the connection's authenticated `workerID` and
`int32(upd.Epoch)`, above the retry branch (which bumps the epoch and returns, so an append below
it could never run on that path), and publishes the log frame before the status frame.

**Two of this item's prescriptions were wrong and the shipped code deviates.** The stream is
`stderr`, not the one the item named: `task_logs.stream` carries a CHECK admitting only `stdout`
and `stderr` (migration 000019), so `prepare` was unwritable rather than merely unused. And the
fence-rejection arm counts nothing - joining `taskLogFenceRejects` would have falsified that
counter's published meaning, and no count is needed because `AppendTaskLog`'s fence is strictly
weaker than the status write that follows, so any rejection there is already counted in
`task_status_fence`.

The message is bounded at `MaxAgentErrorMessageBytes` (4096), NUL-stripped, coerced to valid UTF-8
and cut at a rune boundary; the emptiness guard tests the sanitised value, so a NUL-only message
writes no blank line. A non-`ErrNoRows` append failure is logged under the connection's budget on
its own `kindStatusLogPersist` key, because sharing `kindTaskLogPersist` let the log path silence
this line without owning the task.

Rationale in `docs/superpowers/specs/2026-09-03-prepare-failure-visibility.md` and the retro
`docs/retros/2026-09-03-prepare-failure-visibility.md`.
