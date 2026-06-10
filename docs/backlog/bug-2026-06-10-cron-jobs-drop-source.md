---
title: Cron-fired scheduled jobs silently drop task source specs
type: bug
status: open
created: 2026-06-10
priority: high
source: full-codebase review (2026-06-10)
---

# Cron-fired scheduled jobs silently drop task source specs

## Summary
The schedrunner's hand-rolled `runnerTaskSpec` has no `Source` field and the cron path always calls `CreateTask`, never `CreateTaskWithSource`. A Perforce-sourced schedule validates and stores fine, and "run now" preserves the source via `CreateJobFromSpec`, but every cron fire creates tasks with no workspace spec. Combined with the nil-provider fall-through bug, those tasks then run anyway in the wrong directory. Silent data loss, inconsistent with manual run-now of the same schedule.

## Proposal
The import cycle that motivated the duplicate types no longer exists (the cycle is api -> schedrunner, and `internal/jobspec` is dependency-free). Delete `runnerSpec`/`runnerTaskSpec`, unmarshal into `jobspec.JobSpec`, and share the task-creation logic with `CreateJobFromSpec` (move it somewhere both can import if needed). The alias comment in `internal/api/job_spec.go:14` already anticipates this.

## Related
- `internal/schedrunner/runner.go:74-90` (runnerSpec/runnerTaskSpec), `:165` (CreateTask call)
- `internal/api/job_spec.go` (`CreateJobFromSpec`)
- `internal/jobspec/jobspec.go:38` (`TaskSpec.Source`)
- bug-2026-06-10-source-tasks-run-without-workspace
