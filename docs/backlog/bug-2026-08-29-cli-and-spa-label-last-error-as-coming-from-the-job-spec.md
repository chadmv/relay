---
title: "The CLI and SPA label `last_error` as coming from the stored `job_spec`, but a whole class of it comes from `cron_expr` and `timezone`"
type: bug
status: open
created: 2026-08-29
priority: low
source: Left for the human's call by the Phase 4 fix round of the count-bounds slice (2026-08-29)
---

# The CLI and SPA label `last_error` as coming from the stored `job_spec`, but a whole class of it comes from `cron_expr` and `timezone`

## Summary

Two user-visible labels name the wrong column:

- `internal/cli/schedules.go` prints `Last error (from the stored job_spec, operator-supplied): %s`
- `web/src/schedules/ScheduleDetailPage.tsx` renders a panel with `meta="FROM THE STORED JOB SPEC"`

`schedrunner.ParseSchedule` emits `invalid timezone %q` and `invalid cron expression %q`, echoing
`cron_expr` and `timezone` verbatim. `ValidateStoredSchedule` and `fireOne` wrap that as
`parse cron: ...` into `last_error`. So for that whole class the label points at a column the text
never touched.

## Why it is filed rather than fixed

The count-bounds slice corrected the same false claim in the eight places where it was **prose** -
comments, docstrings, README, and the MCP `provenance` string a model reads. These two are **output**,
pinned by tests (`internal/cli/schedules_test.go` and
`internal/cli/schedules_failure_integration_test.go` both assert `Last error (from the stored job_spec`,
and `internal/mcp/schedules_untrusted_test.go` quotes the CLI label). Changing them is a behaviour
change with test churn across two subsystems, and that slice was scoped prose-only.

The comments at both sites now record why the label names `job_spec` - it is the common case and the
line is one line / a few words wide - and that the point the label makes, *these are not relay's
words*, holds for the cron case too. So the discrepancy is documented rather than silent, which is what
makes this low rather than medium.

## Proposal

Sketch. The substitution is small; the test churn is the actual work.

- CLI: `Last error (from this schedule's stored configuration, operator-supplied):`
- SPA: `FROM THE STORED SCHEDULE`

Update the three pinning tests. Check whether any other surface quotes the CLI label - the MCP test
does, which is itself a sign the string is load-bearing further than it looks.

Consider whether the label is worth keeping at all in the CLI's width budget, versus deferring the
provenance entirely to the MCP `provenance` field where there is room to be accurate.

## Acceptance / Done When

- Neither label names a column the message may not have come from.
- The pinning tests assert the new text, and the MCP test's quotation of the CLI label is updated or
  decoupled.
- The comments at both sites are re-read against whatever wording lands - they currently explain a
  compromise that would no longer exist.

## Related

- Source: `internal/cli/schedules.go`, `web/src/schedules/ScheduleDetailPage.tsx`,
  `internal/schedrunner/cron.go` (`ParseSchedule`), `internal/schedrunner/startup_validation.go`
- The prose half of the same claim was corrected in the count-bounds slice across eight sites,
  including `internal/mcp/untrusted.go`'s `provenance`.
- Different defect, same field: [[bug-2026-08-29-mcp-labels-the-last-error-excerpt-but-not-the-job-spec]]
