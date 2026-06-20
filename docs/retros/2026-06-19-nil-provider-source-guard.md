---
date: 2026-06-19
topic: nil-provider-source-guard
branch: claude/determined-sinoussi-4bf0c2
range: 74afa91..ab6fed6
---

# Session Retro: 2026-06-19 - Nil-Provider Source-Task Guard

**TL;DR:** Closed `bug-2026-06-10-source-tasks-run-without-workspace` by guarding
`Runner.Run` so a source-bearing task on a providerless worker emits
`TASK_STATUS_PREPARE_FAILED` instead of silently building in the agent's cwd; code
review then caught that the server *dropped* that status, so scope expanded to map
`PREPARE_FAILED` to the terminal `"failed"` path - shipped via the full agent-team
flow (brainstorm -> plan -> backend engineer -> review -> backend engineer -> review).

## What Was Built

A source-bearing task can no longer "succeed" against whatever happens to be on
disk when the agent has no workspace provider.

- **Agent guard** (`internal/agent/runner.go`) - before the prepare phase,
  `task.Source != nil && r.provider == nil` now emits `TASK_STATUS_PREPARE_FAILED`
  (carrying `r.epoch`) and returns before any command runs. Mutually exclusive with
  the existing `r.provider != nil` prepare path. Covered by a Docker-free unit test
  asserting only `PREPARE_FAILED` is emitted and the command never ran.
- **Server-side terminality** (`internal/worker/handler.go`) - `handleTaskStatus`
  had no case for `PREPARE_FAILED` and dropped it via `default: return`, leaving the
  task non-terminal. Added `case TASK_STATUS_PREPARE_FAILED: statusStr = "failed"`,
  routing it through the existing terminal path (retry, `FailDependentTasks` cascade,
  job rollup, SSE events, slot release). No new DB status, no migration. Covered by an
  integration test asserting the task reaches `"failed"` with `finished_at` set.
- **Stale comment fix** (`cmd/relay-agent/main.go`) - the comment claiming source
  tasks "fail at dispatch with the existing 'no source provider' path" was false (no
  such path); corrected to describe the runtime runner-side rejection.

## Key Decisions

- **Scoped to the agent guard, deferred the dispatch-side filter.** The backlog
  item's "longer term" idea - a provider-capability requirement in `selectWorker`
  so these tasks are never dispatched to providerless workers - was kept out of
  scope; it needs workers to report provider capability over gRPC plus an
  unschedulable path, and deserves its own spec.
- **Let code review expand scope when it found a real hole.** The first review was
  clean on the guard itself but flagged (as a pre-existing, out-of-scope note) that
  the server drops `PREPARE_FAILED`. That note was load-bearing: without the
  server-side fix the guard traded "silently wrong build" for "task hung
  non-terminal." Surfaced the tradeoff to the user, who chose to fix it now. Updated
  the spec (which had wrongly asserted the server "handles it with no other changes")
  and plan, then ran a second engineer + review cycle.
- **Mapped to existing `"failed"`, not a new status.** YAGNI - a distinct
  `prepare_failed` DB status would mean a migration and UI changes for no behavioral
  gain; the terminal semantics are identical to `failed`.
- **Two implementer dispatches, one review each.** Tasks 1-3 (agent guard, no DB
  surface) got one agent-side `relay-code-reviewer` pass; tasks 4-5 (DB-backed
  handler) got a second review plus real Docker integration tests. Avoided a full
  `relay-verify` fan-out since the integration surface was a single handler test the
  engineer ran directly.

## Known Limitations

- See [`bug-2026-06-19-dispatch-provider-capability-filter`](../backlog/bug-2026-06-19-dispatch-provider-capability-filter.md) - dispatch still has no provider-capability filter: `selectWorker` (`internal/scheduler/dispatch.go:157-202`) can route a source-bearing task to a providerless worker, where it now fails fast with `PREPARE_FAILED` rather than being avoided. Warm-workspace affinity is only a score bonus, not a hard requirement.

## Improvement Goals

- **Trace the full lifecycle of any status/event you emit before claiming "no other
  changes needed."** The spec asserted the server already handled `PREPARE_FAILED`;
  it did not (the switch dropped it via `default: return`). The claim was made from
  the changed file's perspective without reading the consumer. Verifying the
  consumer would have caught it at spec time instead of mid-implementation review.
  New this session.
- **Combined single review for trivial/no-logic tasks** (carried, already promoted
  to [[feedback-combined-review-trivial-tasks]]). Applied: agent-guard tasks rode one
  combined review; the DB-backed handler change got its own focused pass.
- **Treat a backlog proposal as a starting point, not a contract** (carried, already
  promoted to [[feedback-backlog-proposal-not-contract]]). Applied: verified the
  proposal's "fail at dispatch" claim against code (false) and confirmed
  `selectWorker` has no provider filter before scoping.
- **Match commit here-string syntax to the tool's shell** (carried). Applied: bash
  heredocs throughout.

## Files Most Touched

- `internal/worker/handler.go` - `PREPARE_FAILED` -> terminal `"failed"` switch case.
- `internal/worker/handler_test.go` - integration test for terminal `PREPARE_FAILED`.
- `internal/agent/runner.go` - nil-provider guard ahead of the prepare phase.
- `internal/agent/runner_test.go` - unit test proving no command runs on nil provider.
- `cmd/relay-agent/main.go` - corrected the stale provider-disabled comment.
- `docs/superpowers/specs/2026-06-19-nil-provider-source-task-guard-design.md` - spec
  (later corrected for the wrong server-handling claim).
- `docs/superpowers/plans/2026-06-19-nil-provider-source-task-guard.md` - plan
  (extended with the server-side tasks 4-5).
- `docs/backlog/closed/bug-2026-06-10-source-tasks-run-without-workspace.md` - closed.
