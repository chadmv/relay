---
date: 2026-06-19
topic: cron-source-spec-unify-job-creation
status: approved
backlog: bug-2026-06-10-cron-jobs-drop-source
---

# Cron-fired scheduled jobs silently drop task source specs

## Problem

`schedrunner.fireOne` builds jobs through a private, hand-rolled creation path
(`runnerSpec` / `runnerTaskSpec` + `Runner.createJob`, `internal/schedrunner/runner.go:74-193`).
Those duplicate spec structs have **no `Source` field**, and `createJob` always
calls `store.CreateTask`, never `CreateTaskWithSource`. As a result every cron
fire of a Perforce-sourced schedule creates tasks with no workspace spec.

The same schedule run manually ("run now") preserves the source, because
`scheduled_jobs.go:660` routes through `api.CreateJobFromSpec`, which handles
`Source` via `CreateTaskWithSource`. So cron and run-now diverge silently - data
loss visible only at runtime.

This is a violation of the documented **Single job-spec pipeline** invariant,
which already names `jobspec.Validate` + `CreateJobFromSpec` as the one creation
path and lists schedrunner as a consumer. The duplicate structs existed to dodge
an `api -> schedrunner` import cycle that no longer requires them, because the
canonical types now live in the dependency-free `internal/jobspec` package.

## Goal

Cron-fired scheduled jobs persist task `source` identically to run-now and direct
`POST /v1/jobs`. Eliminate the parallel creation path so future `TaskSpec` fields
(like `Source`) reach every consumer for free.

## Design (Option A - shared creation package)

schedrunner cannot import `internal/api` (the `api -> schedrunner` cycle is real:
`internal/api/scheduled_jobs.go` imports schedrunner). So the shared creation
helper moves to a new package both can import.

### New package `internal/jobcreate`

`internal/jobcreate/jobcreate.go` holds `CreateJobFromSpec`, moved verbatim from
`internal/api/job_spec.go`. It already:

- validates via `jobspec.Validate`,
- defaults priority to `"normal"`,
- inserts the job, then each task via `CreateTaskWithSource` when `ts.Source != nil`
  and `CreateTask` otherwise,
- wires `task_dependencies`,
- emits `NotifyTaskSubmitted`.

Imports: `internal/jobspec` + `internal/store` (plus pgtype). No cycle:
`store` and `jobspec` have no relay-internal imports; `api` and `schedrunner`
both already import `store`.

**Why a new package and not `internal/jobspec`:** `internal/mcp` imports
`jobspec` but not `store` - it is a thin HTTP client that validates locally and
POSTs to the REST API. Folding DB-touching creation into `jobspec` would force
`store` + pgx as a transitive dependency onto the MCP binary for no benefit.
Keeping `jobspec` pure (validation/types only) preserves that boundary.

### `internal/api/job_spec.go`

Replace the moved function body with a thin delegating wrapper:

```go
func CreateJobFromSpec(ctx context.Context, q *store.Queries, spec JobSpec,
    submittedBy, scheduledID pgtype.UUID) (store.Job, []store.Task, error) {
    return jobcreate.CreateJobFromSpec(ctx, q, spec, submittedBy, scheduledID)
}
```

This keeps `api.CreateJobFromSpec` and its existing tests
(`job_spec_test.go`) green with no call-site churn. The `JobSpec`/`TaskSpec`/
`SourceSpec` type aliases and the `ValidateJobSpec` value-signature helper stay
in `api` unchanged.

### `internal/schedrunner/runner.go`

- Delete `runnerSpec`, `runnerTaskSpec`, and `Runner.createJob` (including the
  hand-rolled legacy `command -> commands` normalization at `runner.go:160-163` -
  `jobspec.Validate` does this now).
- In `fireOne`, unmarshal `row.JobSpec` into a `jobspec.JobSpec` and call
  `jobcreate.CreateJobFromSpec(ctx, q, spec, row.OwnerID, row.ID)`, returning the
  job ID. The existing error handling in `fireOne` (log + `advance` to next fire)
  is unchanged; `CreateJobFromSpec` returning an error flows through the same
  path `createJob`'s error did.

Net effect: cron fires now go through `jobspec.Validate` (previously skipped) and
`CreateTaskWithSource`, so `source` is persisted.

## Behavioral notes

- **Validation at fire time is new but safe.** Stored `job_spec` rows were
  validated at schedule-creation time. Re-validating at fire time can only reject
  a spec that was already malformed; on rejection `fireOne` logs and advances
  `next_run_at` (never-catch-up), the same as today's unmarshal-error path. This
  is the invariant's intent.
- **Legacy command normalization** moves from schedrunner's bespoke code to
  `jobspec.Validate`, which performs the identical `command -> commands` collapse.
- No new DB status, query, or migration. No proto change.

## Testing

- **Reproduce first (red):** integration test in
  `internal/schedrunner/runner_test.go` - create a schedule whose `job_spec`
  carries a valid Perforce `source` block, run `TickOnce`, then assert the created
  task's `source` column is non-null and round-trips the stream/sync. This fails
  on the current code (source dropped) and passes after the change.
- **No regression:** existing `internal/api` tests for `CreateJobFromSpec` and
  `ValidateJobSpec` stay green through the delegating wrapper; existing
  schedrunner tests (`TestRunner_FiresEligibleSchedule`, `OverlapSkip`,
  `ReconcileOnStartup`) stay green.
- Unit-testable surface (no Docker) is limited because creation touches the DB;
  the source-persistence assertion lives in the integration suite alongside the
  existing runner tests.

## Out of scope

- Dispatch-side provider-capability filtering (tracked separately as
  `bug-2026-06-19-dispatch-provider-capability-filter`).
- Any change to MCP or CLI job submission.

## Files

- `internal/jobcreate/jobcreate.go` - new; moved `CreateJobFromSpec`.
- `internal/api/job_spec.go` - function becomes a thin wrapper.
- `internal/schedrunner/runner.go` - delete duplicate structs + `createJob`,
  call `jobcreate.CreateJobFromSpec`.
- `internal/schedrunner/runner_test.go` - new source-persistence integration test.
- `docs/backlog/closed/bug-2026-06-10-cron-jobs-drop-source.md` - moved on close.
