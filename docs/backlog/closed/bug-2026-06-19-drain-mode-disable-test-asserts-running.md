---
title: Drain-mode disable test asserts 'running' but seedRunningTask seeds 'dispatched'
type: bug
status: closed
created: 2026-06-19
closed: 2026-06-20
priority: medium
source: discovered by relay-verify while fixing bug-2026-06-10-requeue-paths-skip-epoch-bump
---

## Resolution
Fixed in PR #43, 2026-06-20 (Option 1). `seedRunningTask`
(`internal/api/jobs_cancel_test.go`) now advances the claimed task from
`dispatched` to `running` via `UpdateTaskStatusEpoch` at the claimed
`assignment_epoch` (1, from `ClaimTaskForWorker`), honoring the epoch-fence
invariant and mirroring how a real agent's status report transitions the task.
`TestDisableWorker_DrainModeLeavesRunningTaskAlone` now genuinely exercises a
running task; all five `seedRunningTask` callers verified compatible.

# Drain-mode disable test asserts 'running' but seedRunningTask seeds 'dispatched'

## Summary
`TestDisableWorker_DrainModeLeavesRunningTaskAlone` fails because its setup
helper leaves the task in a different state than the test asserts. The helper
`seedRunningTask` claims the task via `ClaimTaskForWorker`, which sets status to
`dispatched`, but the test asserts the status is `running`. The test has never
passed in this configuration; it is unrelated to the epoch-fence work that
surfaced it.

## Repro / Symptoms
```
go test -tags integration -p 1 ./internal/api/... -run TestDisableWorker_DrainModeLeavesRunningTaskAlone -v
--- FAIL: TestDisableWorker_DrainModeLeavesRunningTaskAlone
    expected task status "running", got "dispatched"
```
Confirmed failing on `main` (717e46e) independently of any local change, so it
is pre-existing, not a regression.

## Proposal
Two options identified during review:

1. **Fix the setup (more correct).** Make `seedRunningTask`
   (`internal/api/jobs_cancel_test.go`) advance the task from `dispatched` to
   `running` before returning - e.g. via `UpdateTaskStatusEpoch` at the claimed
   epoch - so the drain-mode test actually exercises a running task.
2. **Adjust the assertion.** Change the expected status to `dispatched`, since
   `dispatched` is also a legitimate active state the drain path must leave
   alone. This narrows what the test proves.

Option 1 is preferred: the test's name and intent are about a *running* task, so
the setup should produce one rather than weakening the assertion.

## Related
- `internal/api/workers_disable_test.go` (the failing test)
- `internal/api/jobs_cancel_test.go` (`seedRunningTask` helper)
- [[bug-2026-06-10-requeue-paths-skip-epoch-bump]] (the work during which this was found)
- [[bug-2026-06-10-job-cancel-epoch-zero]] (prior change that reworked `seedRunningTask` to claim via `ClaimTaskForWorker`)
