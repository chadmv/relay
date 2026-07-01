---
title: "New Job submit form (+ New job)"
type: feature
status: open
created: 2026-07-01
priority: high
source: carved from feature-2026-06-26-job-actions-submit-cancel-retry during 2026-07-01 job-cancel-actions
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
