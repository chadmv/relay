---
title: POST /v1/scheduled-jobs with an explicit "job_spec": null bypasses the required-field guard and returns a misleading "name is required"
type: bug
status: open
created: 2026-08-27
priority: medium
source: Live integration lane while verifying bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys
---

# `"job_spec": null` bypasses the required-field guard and misreports the failure

## Summary
The guard is `len(req.JobSpec) == 0`, which is false for the four-byte literal `null`. The request
falls through to `json.Unmarshal`, which is a documented no-op on JSON null, so `ValidateJobSpec`
then validates a zero-value spec and returns `"name is required"` - which reads as the top-level
schedule name having failed, when the schedule name was supplied and correct.

## Repro / Symptoms
`POST /v1/scheduled-jobs` with a valid `name` and `cron_expr`, plus `"job_spec": null`.

Expected: 400 naming `job_spec` as required.
Observed: 400 `"name is required"`.

Confirmed against a live server that this is not a routing or decode problem: a temporary
`log.Printf` showed `req.Name` was correctly populated at the point of the wrong 400. The probe was
reverted and the tree left clean.

## Context
Two defects stacked, and the second is the one that costs debugging time: a guard that treats `null`
as present, and an error message that attributes a nested failure to a top-level field.
`jobspec.Validate` is shared by REST, CLI, MCP and schedrunner, so the misattribution is not local
to this handler - any caller of any of those surfaces can be told the wrong field failed.

Found incidentally while exercising `ScheduledJob.job_spec` against a real server for an unrelated
null-coercion check.

## Acceptance / Done When
- An explicit `"job_spec": null` is rejected as a missing `job_spec`.
- The error names the field that actually failed; a nested spec failure is distinguishable from a
  top-level one.
- Both halves are covered by a test.

## Related
- `internal/api/scheduled_jobs.go` - the `len(req.JobSpec) == 0` guard
- `internal/jobspec/jobspec.go` `Validate` - the `name is required` message
