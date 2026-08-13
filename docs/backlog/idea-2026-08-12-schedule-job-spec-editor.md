---
title: A schedule's job spec is read-only on the detail page, though PATCH accepts job_spec
type: idea
status: open
created: 2026-08-12
priority: low
source: scoped out of the 2026-08-12-schedule-detail-page slice (decision 5)
---

# A schedule's job spec is read-only on the detail page, though PATCH accepts job_spec

## Summary

The Job spec panel on `/schedules/:id` renders `JSON.stringify(schedule.job_spec, null, 2)` into a
read-only `<pre>` (`web/src/schedules/ScheduleDetailPage.tsx:213-217`). The backend accepts edits:
`patchScheduledJobRequest` carries `JobSpec *json.RawMessage`
(`internal/api/scheduled_jobs.go:521-528`) and the handler unmarshals it into `JobSpec` and runs
`ValidateJobSpec` before storing it (`:570-582`). The hi-fi puts an `Edit` button on the panel
(`design_handoff_relay_holo/hifi3-holo-pages.jsx:1799`).

So changing a schedule's spec today means deleting the schedule and creating a new one, or using
the API directly.

## Context

Scoped out of the page slice deliberately, for a reason that is about **surface duplication**
rather than effort. The app already has exactly one job-spec editor: the JSON textarea plus its
`validateSpecText` in `web/src/jobs/NewJobPage.tsx:51-59`. Writing a second one inside the
schedules module would give the app two places where a user types a job spec, two places where
parse errors are worded, and two things to keep in step with `jobspec.TaskSpec` as it grows. That
is the frontend shape of the same argument the **single job-spec pipeline** invariant makes on the
backend.

Two facts that constrain any implementation:

- **The stored value is JSON, not YAML.** `scheduledJobResponse.JobSpec` is a `json.RawMessage`
  (`internal/api/scheduled_jobs.go:26`) and `web/` has no YAML serializer among its six runtime
  dependencies (`web/package.json:13-20`). The hi-fi renders YAML; that was scoped out with the
  editor and should not creep back in with it.
- **The panel renders `job_spec` verbatim and a spec can carry `env` values a user chose to
  store.** It must stay a React text child - never `dangerouslySetInnerHTML` - and nothing from
  `job_spec` may end up in a URL, a `title` attribute or a log line. The shipped comment at
  `ScheduleDetailPage.tsx:205-212` says this; an editor must preserve it, including on the error
  path (do not echo the offending spec into an error message that gets logged).

## Proposal

Frontend-only. Extract, then reuse - do not write a second editor.

1. **Extract the spec editor from `NewJobPage`** into a shared component (a textarea plus its
   parse/validate feedback, taking a value and an `onChange`, owning no submission logic).
   `NewJobPage` migrates onto it first, with its existing tests unchanged - **an assertion that
   needs adjusting during that migration is itself the finding**, since it means the extraction
   changed behaviour.
2. **Use it on the schedule detail panel** behind an Edit control, submitting through the existing
   `updateSchedule` client (`web/src/schedules/api.ts:92-94`) as `{ job_spec }` and **nothing
   else**. This is the critical constraint: `SchedulePatch` must carry only `job_spec` here,
   because a body that also carries `cron_expr` or `timezone` recomputes `next_run_at` from
   `time.Now()` even when unchanged (`internal/api/scheduled_jobs.go:585,595`). The page's
   changed-fields-only discipline applies to this surface too.
3. **Defer all semantics to the server.** The client may check that the text parses as JSON;
   everything else - required fields, task shapes, dependency references - is
   `ValidateJobSpec`'s answer, surfaced verbatim from the 400 exactly as the Trigger form already
   does for cron errors. Do not reimplement any part of `jobspec.Validate` in TypeScript. This is
   the **single job-spec pipeline** invariant seen from the client: one validator of record.

Deliberately **not** proposed: a YAML view, a structured form builder (that is
[[idea-2026-07-01-job-spec-form-builder]], a much larger and independent idea), or a diff view of
what changed. And note that editing a schedule's spec does **not** affect jobs it already produced;
if that turns out to be non-obvious to users, it is a copy question for the spec, not a behaviour
change.

## Acceptance / Done When

- The Job spec panel has an Edit control; saving `PATCH`es a body containing `job_spec` and no
  other key, asserted with `toEqual` on the whole parsed body (a property check cannot see an
  extra key, and the extra key is the failure mode).
- Cancel restores the loaded spec, and a poll landing mid-edit does not clobber the draft - the
  same two lifecycle rules `ScheduleTriggerForm` already honours.
- A server 400 from `ValidateJobSpec` renders verbatim in a `role="alert"` beside the editor, and
  the banner is dismissable (see the undismissable-banner finding in
  `docs/retros/2026-08-12-schedule-detail-page.md`).
- `NewJobPage`'s existing tests pass with **zero edits** after the extraction.
- Exactly one job-spec editing component exists in `web/src`.
- The comment naming this item at `web/src/schedules/ScheduleDetailPage.tsx:205-212` is removed.

## Related

- Design record: `docs/superpowers/specs/2026-08-12-schedule-detail-page.md` (decision 5, and the
  "Scope creep toward a job-spec editor" risk), `docs/retros/2026-08-12-schedule-detail-page.md`
- Source: `web/src/schedules/ScheduleDetailPage.tsx:205-217`, `web/src/jobs/NewJobPage.tsx:51-59`,
  `web/src/schedules/api.ts:71-94` (`SchedulePatch` and its recomputation warning),
  `internal/api/scheduled_jobs.go:521-528,570-582`
- The bigger, separate idea it must not become: [[idea-2026-07-01-job-spec-form-builder]]
- Same page, other gaps: [[idea-2026-08-12-schedule-next-fires-preview]],
  [[bug-2026-08-12-scheduled-job-detail-missing-owner-email]]

## Notes

Low priority because the workaround (delete and recreate) exists and schedules' specs change
rarely. The reason to file it rather than drop it is the **extraction**: the moment somebody wants
this, the tempting move is a second textarea in `web/src/schedules/`, and that is the decision this
item exists to pre-empt.
