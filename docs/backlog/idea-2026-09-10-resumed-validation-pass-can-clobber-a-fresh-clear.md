---
title: A validation sweep resumed after the listener would stamp stale verdicts over fresh clears
type: idea
status: open
created: 2026-09-10
priority: medium
source: Decision in the startup-validation deadline spec (PR #210), which refused a continuation on this ground
---

# A validation sweep resumed after the listener would stamp stale verdicts over fresh clears

## Summary

The startup validation sweep is now bounded by a wall-clock budget, and the unswept remainder goes
nowhere. The obvious improvement - finish the remainder in a later pass, after the HTTP listener is up
- cannot be written safely today, because that pass would run concurrently with the scheduler runner
and `RecordScheduledJobFailure`'s fence does not cover the clearing statement.

**This item does not prescribe a fence, and an earlier filename did.** It was filed as
`...-needs-a-fence`, which named the remedy in the identifier a reader sees first while the body
explains why that remedy has a real cost. Renamed to describe the defect instead. The open question
is what to do, not how.

## Context

The sweep's safety argument while it runs pre-listener is that nothing else in the process is running.
A continuation gives that up.

**The specific gap:** `RecordScheduledJobFailure` fences on `job_spec`, `cron_expr` and `timezone` -
the three inputs it validated - so a row repaired through another replica between the LIST and the
UPDATE cannot have a stale verdict stamped onto it. But `AdvanceScheduledJob`, the success path,
**clears `last_error` and touches none of those three**. So a row read by the continuation, then fired
successfully, then written by the continuation matches the fence and gets its fresh clear overwritten
with a verdict computed before the fire.

That is the opposite of the failure the fence exists to prevent: a schedule that is working reports a
failure, and nothing clears it until its next successful fire.

## Proposal

Sketch only, and **the item must not prescribe the fence** - choosing it is the design work, and the
naive choice has a known cost.

Candidates: extend the fence to cover whatever `AdvanceScheduledJob` changes (note it deliberately
touches none of the three, so this is a new predicate rather than a wider one); have the continuation
re-validate immediately before writing rather than trusting its earlier read; or decide the remainder
genuinely belongs nowhere and record that, which is the status quo.

Read the truncation semantics first: the sweep only ever ADDS records and has no clearing sibling, so
an absent `last_error` already means "not checked or not failing" and cannot be distinguished. A
continuation that writes late changes what an absence means.

## Acceptance / Done When

- Either a continuation exists and cannot stamp a stale verdict over a fresh clear, or the decision to
  have no continuation is recorded with this reason.
- If a fence is added, it is pinned by a test that drives the fire between the read and the write.

## Related

- `internal/store/query/scheduled_jobs.sql` (`RecordScheduledJobFailure`, `AdvanceScheduledJob`),
  `internal/schedrunner/startup_validation.go`
- [[feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep]] - closed; introduced the remainder
- [[idea-2026-09-10-reconcile-patch-clobber-window]] - the sibling fence question, same caution
