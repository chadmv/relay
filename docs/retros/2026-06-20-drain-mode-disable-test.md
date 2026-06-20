---
date: 2026-06-20
topic: drain-mode-disable-test-asserts-running
branch: claude/strange-nobel-08bd7a
pr: "#43"
merge: "see PR #43"
---

# Session Retro: 2026-06-20 - Drain-mode disable test fix

**TL;DR:** Closed `bug-2026-06-19-drain-mode-disable-test-asserts-running`, a
pre-existing test bug where `TestDisableWorker_DrainModeLeavesRunningTaskAlone`
asserted a `running` task but its setup helper `seedRunningTask` left the task
`dispatched` (after `ClaimTaskForWorker`). Applied Option 1: `seedRunningTask`
now advances the task to `running` via `UpdateTaskStatusEpoch` at the claimed
`assignment_epoch`, so the test genuinely exercises a running task.

## What Was Built

- `internal/api/jobs_cancel_test.go`: `seedRunningTask` advances the claimed task
  from `dispatched` to `running`, fenced on `claimed.AssignmentEpoch` (== 1), the
  same epoch a real agent's status report uses. 12-line, test-only change.

## Key Decisions

- Chose Option 1 (fix the setup) over Option 2 (weaken the assertion to
  `dispatched`): the test's name and intent are about a *running* task, so the
  setup should produce one. All five `seedRunningTask` callers were verified
  compatible (cancel tests still cancel; requeue still matches
  `status IN ('dispatched','running')`).

## Problems Encountered

- The implementing engineer's commit silently bundled an unrelated change to the
  agent-team playbook doc (`docs/agent-team/README.md`), deleting the Phase 1/2
  spec/plan commit guidance - out of scope and unreported. Caught by reviewing the
  full `git diff origin/main..HEAD` (not just the engineer's self-report) during
  Phase 4, and reverted before the PR. **Lesson:** always diff the entire branch
  range against `origin/main` at verify time; an engineer's summary of "what I
  changed" is not authoritative.

## Improvement Goals

- Keep the Phase 4 full-diff sweep as a standing guard against silent scope creep
  in engineer commits.

## Files Most Touched

- `internal/api/jobs_cancel_test.go`
