---
title: A permanently un-fireable schedule is invisible - a spec that stops validating advances next_run_at forever with only a log line
type: bug
status: closed
created: 2026-08-23
updated: 2026-08-28
closed: 2026-08-28
resolution: fixed
priority: medium
source: 2026-08-23 deep roadmap refresh - backend invariants lens finding N4
---

# A permanently un-fireable schedule is invisible - a spec that stops validating advances next_run_at forever with only a log line

## Summary
`schedrunner.fireOne` re-validates the stored spec on every fire, because `CreateJobFromSpec`
calls `jobspec.Validate` (`internal/jobcreate/jobcreate.go:32`). On failure `TickOnce` logs one
line and calls `advanceNextRun` (`internal/schedrunner/runner.go:80-85`), so `next_run_at` keeps
marching. There is no `last_error` column in any migration and no failure field on
`scheduledJobResponse` (`internal/api/scheduled_jobs.go:19-34`): `last_run_at` and `last_job_id`
simply stop moving while `next_run_at` looks perfectly healthy, on the API, in the CLI, and on the
SPA's schedule pages.

## Context
This composes directly with [[bug-2026-08-12-retries-unvalidated-and-budget-only-in-go]]: adding a
`retries`/`timeout_seconds` bound to `jobspec.Validate` retroactively disables every stored
schedule that exceeds the new bound, with no operator-visible signal - which is why the roadmap
sequences this item beside that one. Also relevant: `handleCreateScheduledJob` stores the client's
raw bytes (`internal/api/scheduled_jobs.go`, `JobSpec: req.JobSpec`), so the stored spec is exactly
what the client sent.

**(2026-08-28) The composition above is no longer hypothetical, and the sentence explaining the raw
bytes was wrong.** Both corrections matter to whoever scopes this, so both are recorded rather than
silently rewritten.

**The bound shipped, and it shipped WITHOUT this item.** `jobspec.Validate` now rejects
`retries` outside `[0,10]` and `timeout_seconds` outside `[0,604800]`. `ROADMAP.md` had said to ship
the two together; a deliberate gate decision shipped the bound alone, with a documented-hazard test
as the agreed mitigation. So this item is no longer the reason that one was dangerous - it is the
outstanding half of a change that is already in the tree. Three things now exist that did not when
this item was filed:

- `TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard`
  (`internal/schedrunner/stored_spec_bounds_test.go`) asserts the CURRENT, wrong behaviour on
  purpose. Its header names this item and states, per assertion, which ones stay and which one
  inverts when the fix lands. **Read it before designing the fix**; it is addressed to you.
- A field-set tripwire over `store.ScheduledJob`
  (`internal/schedrunner/scheduled_job_surface_test.go`, plain lane, no Docker) goes RED the moment
  any new column lands - deliberately including `last_error`. That is intended: the instruction is to
  invert the hazard test, not to add an exemption.
- `POST /v1/scheduled-jobs/{id}/run-now` now answers a stored spec that fails validation with **400
  and the per-task message**, where it previously returned `500 "create job failed"`. That is one
  interactive way to ask why a schedule stopped firing, and the fix here should reference it rather
  than rebuild it. It is not a substitute: it requires the operator to already suspect the schedule.

**The refuted sentence, left visible per convention.** The original text read: "`ValidateJobSpec`
takes a value, so normalization is discarded and the stored spec is exactly what the client sent."
The mechanism is wrong. `JobSpec.Tasks` is `[]TaskSpec` (`internal/jobspec/jobspec.go`), so the value
copy shares the backing array, and `Validate` normalizes each element in place through
`ts := &spec.Tasks[i]` - a caller's task goes from `Command=[echo hi] Commands=[]` to
`Command=[] Commands=[[echo hi]]` across the call. Proven empirically 2026-08-28, and
`internal/api/job_spec.go`'s doc comment was corrected to state it. **The conclusion survives for a
different reason**, which is the one now in the text above: the handler stores `req.JobSpec` - the
raw request bytes - and never re-marshals the validated struct, so normalization never reaches the
stored row whether or not it mutated the local copy.

## Proposal
A `last_error TEXT` (nullable) + `last_error_at` on `scheduled_jobs`, written by the schedrunner
on a failed fire and cleared on a successful one, exposed on the response and rendered on the
schedule detail page (which already has the panel structure for it). Absent-not-zero applies: no
error means the fields are absent, not empty strings.

## Acceptance / Done When
- A schedule whose spec fails validation at fire time records the error where
  `GET /v1/scheduled-jobs/{id}` returns it, and a successful fire clears it.
- The schedule detail page surfaces the failure state without fabricating data the backend
  cannot supply.
- A regression test drives a stored-then-invalidated spec through a tick and asserts the error is
  visible via the API (RED at HEAD: nothing but a log line).

## Related
- `internal/schedrunner/runner.go:80-127`, `internal/jobcreate/jobcreate.go:32`, `internal/api/scheduled_jobs.go:19-34,134`
- [[bug-2026-08-12-retries-unvalidated-and-budget-only-in-go]] - land together; its bound is what makes this visible-failure surface urgent
- [[idea-2026-08-12-schedule-next-fires-preview]] - same response struct, different field

## Resolution
Fixed on `claude/unfireable-schedule-invisible-7ba5fb` (42 commits). `scheduled_jobs` gains
`last_error` and `last_error_at` (migration 000022, nullable, catalog-only, no backfill, so it
cannot refuse to boot). `TickOnce` records them on the OUTER transaction beside `advanceNextRun`
- never inside `fireOne`, whose savepoint rollback would discard the write silently - for the
three PERMANENT failure classes only, and clears them on a successful fire. Wrapped pgx errors are
logged and not recorded, so a database blip neither becomes a fact about the schedule nor
overwrites a real record. A record-only startup sweep covers the long-cadence schedules neither
existing loop sees. Surfaced on the REST response, the SPA list chip and detail panel, the CLI
`STATE` column and `show`, the Python SDK, and the MCP server. `relay schedules update --spec FILE`
was added so the remedy the signal points at is reachable from relay's own CLI.

**Two of the item's own claims were refuted before any code was written**, and both changed the
design. `last_run_at` does not mean "a job was produced": `fireOne` called `advance` from two
sites, and the second was the `overlap_policy=skip` branch, which stamps `last_run_at` while
returning BEFORE validation - so the obvious clearing rule would have cleared on zero evidence.
The statement was split. And the route is `PATCH` with an all-pointer request struct, not `PUT`,
so the clear is a boolean SQL argument rather than a read-modify-write that could carry a stale
error over a failure a tick recorded in between.

**The `_DocumentedHazard` test left by #158 was inverted as its header instructed**: auto-disable
was rejected at the spec gate, so all six of its original assertions stay TRUE including "still
Enabled", and none was flipped. The field-set tripwire's list was updated rather than exempted.

Phase 4 found ten further defects, eight fixed here. The two largest were not in the original
diagnosis at all: a PATCH of `cron_expr` alone erased a `job_spec` failure that was still true
(re-creating this very bug through the fix's own clearing rule, reproduced with a live probe and
now gated on the effective post-patch row validating), and MCP was an unenumerated fifth renderer
feeding attacker-chosen prose to a model holding destructive write tools. Deferred, with items
filed: [[bug-2026-08-28-run-now-neither-clears-nor-records-the-failure]],
[[bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener]] and
[[bug-2026-08-28-schedrunner-logs-operator-controlled-schedule-names-raw]].

The headline regression test runs in a lane CI does not execute; that is recorded as an eighth
instance on [[idea-2026-08-23-integration-only-guards-ci-never-runs]] rather than papered over.
The CI-visible witness is `internal/cli/schedules_failure_integration_test.go`, which covers the
read half only.
