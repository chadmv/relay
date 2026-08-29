---
title: run-now is step 1 of the documented remedy ladder and is a no-op on the signal it points at
type: bug
status: open
created: 2026-08-28
priority: medium
source: Phase 4 invariants lens of the unfireable-schedule-visibility slice (2026-08-28)
---

# run-now is step 1 of the documented remedy ladder and is a no-op on the signal it points at

## Summary
`handleRunScheduledJobNow` validates the stored spec, opens its own transaction, calls
`CreateJobFromSpec`, commits, and never touches `scheduled_jobs`. It neither clears
`last_error` on success nor records it on failure. README, the SPA "Last failure" panel and
the CLI's `Re-check with: relay schedules run-now <id>` line all name run-now as the FIRST
remedy for a failing schedule, so the one action relay tells an operator to take does nothing
to the signal that sent them there.

## Context
Filed out of the Phase 4 review of the slice that added `last_error` / `last_error_at`
([[bug-2026-08-23-unfireable-schedule-is-invisible]]). It was deliberately scoped out of that
PR because it is a behaviour change with its own design question, not a defect the slice
introduced: run-now had these semantics before the columns existed.

The asymmetry is what makes it worth fixing. A SUCCESSFUL run-now proves exactly the condition
`AdvanceScheduledJob` uses to justify clearing - the schedrunner's own comment says a completed
`CreateJobFromSpec` is "the only event that proves the stored spec both validates and inserts",
and run-now performs that same event. So the evidence for clearing is already in hand and is
thrown away.

## Repro / Symptoms
1. A schedule stops firing because its stored spec no longer passes `jobspec.Validate`; the
   schedrunner records `last_error`.
2. The spec is repaired by any route other than PATCH - a binary upgrade that relaxes the rule,
   or a direct-SQL repair.
3. Operator runs `relay schedules run-now <id>` as README instructs. It returns 201.
4. The FAILING chip, the CLI `STATE` column and the API field all still say failing, until the
   next scheduled fire. On `@monthly` that is up to a month.

The failure direction is milder but also wrong: a failed run-now returns a 400 carrying the
fresh message while the stored text keeps whatever it had, so the response and the panel can
disagree about why the same schedule is broken.

## Proposal
On the success path, after the commit, clear the two failure columns only - NOT `next_run_at`,
`last_run_at` or `last_job_id`, because run-now is deliberately not a scheduled fire and must
not be able to skip one. That needs a `ClearScheduledJobFailure` statement; the existing
`AdvanceScheduledJob` moves too much.

The 400 path is a genuine open question rather than an oversight, and should be decided rather
than assumed: recording there would let any user with run-now access write the field at will,
which is a different trust story from the schedrunner writing it. Deciding NOT to record is
defensible - but then README must say run-now does not update the stored record, because right
now it implies otherwise.

## Acceptance / Done When
- A successful run-now clears `last_error` and `last_error_at` and moves nothing else.
- The 400 path's behaviour is decided explicitly and README agrees with whatever was chosen.
- A test drives record -> run-now -> GET and asserts the field is gone, with a control proving
  `next_run_at` and `last_run_at` did not move.

## Related
- `internal/api/scheduled_jobs.go` (`handleRunScheduledJobNow`), `internal/schedrunner/runner.go`
  (`advance`), `internal/store/query/scheduled_jobs.sql` (`AdvanceScheduledJob`)
- [[bug-2026-08-23-unfireable-schedule-is-invisible]] - the slice that made run-now a documented
  remedy step and therefore made this gap matter
