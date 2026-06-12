---
date: 2026-06-11
topic: job-cancel-epoch-fix
branch: claude/eloquent-wilson-744328
range: 6ac89dfccb96cccf2ba335fb0c02e7d06ac95dda..015ef6f2a7a397057b2e87beef9d583edc5b1145
---

# Session Retro: 2026-06-11 - Job Cancel Epoch-0 Fix

**TL;DR:** Job cancellation 500'd for any job whose tasks had ever been
dispatched because `handleCancelJob` called the epoch-fenced `UpdateTaskStatus`
with a zero-value epoch; fixed with a set-based `CancelJobTasks` query that
bumps `assignment_epoch`, closing `bug-2026-06-10-job-cancel-epoch-zero`.

## What Was Built

Fixed the high-priority backlog bug end-to-end via TDD.

- **Root cause:** `handleCancelJob` cancelled each non-terminal task through
  `UpdateTaskStatus`, which fences on `assignment_epoch`. It never set the
  epoch, so it defaulted to 0. Any task that had ever been dispatched has
  `assignment_epoch >= 1`, so the update returned `pgx.ErrNoRows`, the handler
  returned 500, and the whole cancel transaction rolled back.
- **Fix:** new set-based `CancelJobTasks :exec` query
  (`internal/store/query/tasks.sql`) that fails all non-terminal tasks of a job
  in one statement and bumps `assignment_epoch` - satisfying the epoch-fence
  invariant (cancellation ends the assignment, so late agent updates are fenced
  out). The handler now collects running/dispatched tasks for agent cancel
  signals first, then issues the single query; the per-task `UpdateTaskStatus`
  loop is gone.
- **Test fix:** `seedRunningTask` in `jobs_cancel_test.go` was masking the bug
  by seeding `status='running'` via a raw `UPDATE` that left epoch at 0 - a
  state that cannot occur in production. It now dispatches via
  `ClaimTaskForWorker` (epoch -> 1) like the scheduler does, which made the
  three existing cancel tests reproduce the 500 before the fix.

## Key Decisions

- **`:exec`, not `:many`.** The backlog proposal sketched a `:many` returning
  `id, worker_id, status`. But the same statement sets `worker_id = NULL`, so
  `RETURNING worker_id` yields NULL - useless for the agent cancel signals. The
  handler already collects running/dispatched tasks (with their worker IDs)
  from the pre-update `ListTasksByJob` snapshot, so no return value is needed.
  Simpler query.
- **Bypass the fence deliberately, don't thread an epoch.** The cancel handler
  has no per-task epoch to pass. Rather than fetch each task's epoch, the query
  is intentionally unfenced and bumps the epoch itself - the documented
  alternative the epoch invariant allows ("end the assignment").
- **Reverted sqlc's line-ending noise.** `sqlc generate` rewrote all 13 store
  `*.go` files LF-where-the-repo-uses-CRLF. Only `tasks.sql.go` had real
  content; reverted the other 11 to keep the diff surgical (verified with
  `git diff --ignore-all-space`).
- **Left the sibling bug untouched.** `bug-2026-06-10-requeue-paths-skip-epoch-bump`
  (five requeue/retry queries that return tasks to `pending` without bumping
  the epoch) is a distinct open item; out of scope here.

## Problems Encountered

- **`make` not on PATH.** Ran `sqlc generate` and `go test`/`go build` directly
  instead of the Makefile targets. Docker, sqlc v1.30.0, and `p4` were all
  available, so the integration tests ran cleanly.
- **PowerShell here-string in the Bash tool.** First commit used `git commit -m
  @'...'@` syntax, which is PowerShell, not bash - it injected a stray `@` as
  the subject line. Amended with a bash `-F -` heredoc. Lesson: match
  here-string syntax to the actual shell the tool runs.

## Improvement Goals

- Prior retro's goal (combine spec+quality review into one pass for one-liner
  tasks) did not apply this session - this was a single self-contained bugfix
  driven directly by TDD rather than subagent-driven-development, which was the
  right altitude for a one-query change. Carry the goal forward for the next
  multi-task plan.
- New: when committing multi-line messages, pick the heredoc/here-string form
  that matches the tool's shell (bash `-F -` heredoc vs PowerShell `@'...'@`).

## Files Most Touched

- `internal/store/query/tasks.sql` - new `CancelJobTasks` query (source of truth).
- `internal/store/tasks.sql.go` - regenerated; the one real generated change.
- `internal/api/jobs.go` - `handleCancelJob` rewritten to use `CancelJobTasks`.
- `internal/api/jobs_cancel_test.go` - `seedRunningTask` now claims via `ClaimTaskForWorker`.
- `docs/backlog/closed/bug-2026-06-10-job-cancel-epoch-zero.md` - closed (moved).
