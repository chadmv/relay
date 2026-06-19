---
date: 2026-06-19
topic: requeue-retry-epoch-fence
branch: claude/recursing-euclid-1fecda
range: 717e46ebfd728d50611931019f0552d18c218706..4ec35aed35293778eb138f8219370b349f806cb3
---

# Session Retro: 2026-06-19 - Requeue/Retry Epoch-Fence

**TL;DR:** Closed `bug-2026-06-10-requeue-paths-skip-epoch-bump` by bumping
`assignment_epoch` on every requeue/retry path, consolidating the two
worker-requeue queries into one, and deleting a dead query; shipped via the full
agent-team flow (tpm spec review -> planner -> backend engineer -> relay-verify)
and opened PR #30.

## What Was Built

Every query that returned a task to `pending` now bumps `assignment_epoch`, so a
late status update or log chunk from a prior assignment can no longer slip past
the epoch fence.

- **Three live queries fixed** (`IncrementTaskRetryCount`, `RequeueTask`,
  `RequeueTaskByID`): added `assignment_epoch = assignment_epoch + 1`. Three new
  store-layer integration tests prove the bump plus stale-update fencing; the
  retry test also asserts a stale generation cannot drive a second retry.
- **Worker-requeue consolidated**: deleted the non-bumping `RequeueWorkerTasks`,
  renamed the epoch-bumping `RequeueWorkerTasksWithEpoch` -> `RequeueWorkerTasks`
  (kept `:many` / `RETURNING id`), and repointed all three callers
  (disconnect, grace-expiry, disable-worker).
- **Dead `RequeueAllActiveTasks` deleted** (no Go caller; superseded by
  grace-timer seeding).
- **CLAUDE.md Epoch-fence invariant** repointed at the renamed query.
- Closed the backlog item; filed a new one for a pre-existing failure found
  during verification.

## Key Decisions

- **Refined the backlog's "fix all five" into a cleaner scope.** Investigation
  during brainstorming showed one of the five queries was dead code and another
  became a strict near-duplicate once fixed. Surfaced both as explicit user
  choices (AskUserQuestion) rather than silently fixing all five: outcome was
  3 fixed, 1 consolidated away, 1 deleted. A backlog item's proposal is a
  starting point, not a contract.
- **One implementer for tightly-coupled tasks, not fresh-subagent-per-task.**
  All five plan tasks edit the same `tasks.sql` and regenerate the same
  `tasks.sql.go`. Fragmenting them across subagents would have churned the
  generated file repeatedly. Ran one backend-engineer through the whole plan
  task-by-task, then one consolidated `relay-verify` pass - the right altitude
  for coupled SQL work.
- **Independently verified the implementer's "pre-existing failure" claim.**
  The implementer reported `TestDisableWorker_DrainModeLeavesRunningTaskAlone`
  failing but pre-existing. Did not take it on trust: `relay-verify` reproduced
  it against the `main` worktree, confirming it predates this work. Then filed
  it as `bug-2026-06-19-drain-mode-disable-test-asserts-running`.

## Problems Encountered

- **The retro chain forked.** The newest prior retro
  (`2026-06-11-dependency-cycle-validation`) records a `range` ending at a SHA on
  a different feature branch; `that-SHA..HEAD` spans several intervening sessions
  that merged to main without chaining a retro (agent-team 2026-06-18, roadmap,
  request-body-size-limit). Scoped this retro to its own session
  (`717e46e..HEAD`, the branch's merge-base) instead of over-claiming that work.
- **Plan doc was left uncommitted** by the implementer (the plan never instructs
  committing itself). Caught it at retro time and committed it, since every
  prior plan lives in `docs/superpowers/plans/`.

## Improvement Goals

- **Combined single review for trivial/no-logic tasks** (carried from
  2026-06-10, applied in dependency-cycle-validation and request-body-size-limit
  retros). Applied again here, more aggressively: the doc-edit and
  backlog-housekeeping tasks were folded into the single implementer dispatch and
  covered by one `relay-verify` pass rather than per-task two-stage reviews.
  Carried and applied across 3+ retros; promoted to a feedback memory
  ([[feedback-combined-review-trivial-tasks]]) so it is no longer re-derived.
- **Expect sqlc/`make generate` line-ending churn; stage only intended files**
  (carried from dependency-cycle-validation). Institutionalized this time by
  baking it into the plan up front: the plan's regeneration steps explicitly
  told the engineer to run `git diff --ignore-all-space` and revert LF-only
  hunks, which the engineer did cleanly. Promoted to CLAUDE.md (the store/sqlc
  guidance) - a recurring, repo-specific build gotcha, now durable rather than
  rediscovered each session.
- **New:** when scoping a retro, if the recorded prior `range` spans work from
  unrelated merged sessions, scope to the current branch's merge-base rather
  than blindly chaining - and write a retro even for infra/agent-team sessions so
  the chain does not fork.
- **New (applied):** matched commit here-string syntax to the tool's shell (bash
  `-F -` heredocs throughout) - the lesson from the job-cancel-epoch retro held.

## Files Most Touched

- `internal/store/query/tasks.sql` - source of truth: three epoch bumps, query
  consolidation, dead-query deletion.
- `internal/store/tasks.sql.go` - regenerated by sqlc; content-only diff.
- `internal/store/store_test.go` - three new epoch/fence integration tests, plus
  the `TestClaimTaskForWorker_IncrementsEpoch` assertion bumped to epoch 3.
- `internal/store/workers_disabled_test.go` - renamed test to drop `WithEpoch`.
- `internal/api/workers.go`, `internal/worker/handler.go`,
  `cmd/relay-server/main.go` - repointed worker-requeue callers.
- `CLAUDE.md` - Epoch-fence invariant wording.
- `docs/superpowers/specs/2026-06-19-requeue-retry-epoch-fence-design.md`,
  `docs/superpowers/plans/2026-06-19-requeue-retry-epoch-fence.md` - spec + plan.
- `docs/backlog/closed/bug-2026-06-10-requeue-paths-skip-epoch-bump.md` - closed.
- `docs/backlog/bug-2026-06-19-drain-mode-disable-test-asserts-running.md` -
  filed for the pre-existing failure.
