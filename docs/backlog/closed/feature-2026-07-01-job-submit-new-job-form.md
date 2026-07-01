---
title: "New Job submit form (+ New job)"
type: feature
status: closed
created: 2026-07-01
priority: high
source: carved from feature-2026-06-26-job-actions-submit-cancel-retry during 2026-07-01 job-cancel-actions
closed: 2026-07-01
resolution: fixed
---

# New Job submit form (+ New job)

## Summary
The "+ New job" submit flow - `POST /v1/jobs` with a job-spec body - carved out of the job
write-actions item. Frontend-only (the endpoint exists), but the job-spec editor (YAML and/or form)
is a non-trivial surface that warrants its own slice rather than riding along with the cancel action.

## Context
The 2026-07-01 job-cancel-actions slice deliberately narrowed the parent
[[feature-2026-06-26-job-actions-submit-cancel-retry]] to just graceful cancel and force-cancel (the
one genuinely frontend-only, unblocked part). Submit was split out here because the spec editor is
its own design problem: it needs client-side validation of a job-spec shape, not just a button and a
confirm dialog.

## Proposal
- A **New Job entry point** on the jobs views (a "+ New job" button on the jobs list, matching the
  Holo design).
- A **spec editor** (YAML and/or form) with client-side validation of the job-spec shape before
  submit.
- **Error handling on submit** - surface backend validation errors (400) inline rather than as an
  opaque failure.
- **Navigation to the created job's detail page** on success.

## Acceptance / Done When
- A "+ New job" entry point exists on the jobs views.
- The submit flow creates a job via `POST /v1/jobs` with a valid job-spec body.
- Client-side validation rejects malformed specs before submit; backend validation errors surface
  inline on the form.
- On success the user is navigated to the created job's detail page.

## Related
- Carved from [[feature-2026-06-26-job-actions-submit-cancel-retry]] during the 2026-07-01
  job-cancel-actions slice.
- Design: `design_handoff_relay_holo/reference/screens/jobs-list.js`
- Source: `internal/api/jobs.go` (`POST /v1/jobs`), `web/src/jobs/`

## Notes
Frontend-only; the endpoint exists. The complexity is the job-spec editor surface, which is why this
is its own item rather than a sub-task of the cancel work.

## Resolution
Shipped the "+ New job" flow (feature commit 23eadae, autopilot iteration 4, 2026-07-01
job-submit-form). A /jobs/new route (NewJobPage) with a JSON job-spec textarea editor prefilled
with a minimal valid starter spec, a "+ New job" entry point on the jobs list (auth-only, not
admin-gated), createJob(spec) + useCreateJob (invalidates ['jobs'] + ['job-stats'], navigates to
the created job on 201). Client validation stays minimal (valid JSON + name + non-empty tasks) and
defers the rest to the server's jobspec.Validate so no parallel validation path drifts from the
single job-spec pipeline; backend {"error": msg} errors surface inline in a role="alert" banner.
Full web suite green (290 tests), production build clean, code review CLEAN with mutation-tested
non-vacuous assertions. A structured/visual form-builder is deferred to
[[idea-2026-07-01-job-spec-form-builder]]; a YAML mode and draft persistence remain possible
follow-ups. Design: docs/superpowers/specs/2026-07-01-job-submit-form-design.md;
plan: docs/plans/2026-07-01-job-submit-form-plan.md.
