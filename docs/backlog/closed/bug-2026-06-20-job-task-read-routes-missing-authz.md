---
title: Job and task READ routes have no owner check (any authenticated user can read any job's metadata, task list, and logs)
type: bug
status: closed
created: 2026-06-20
closed: 2026-06-20
priority: low
source: discovered by relay-code-reviewer during job-cancel-missing-authz
---

## Resolution
Resolved 2026-06-20 as **working-as-intended**. Product decision: global READ
across all jobs/tasks is deliberate render-farm semantics - a shared farm where
any authenticated operator can inspect any job's metadata, task list, and logs.
No code gate was added to the four read routes; instead the decision was
documented next to the route registrations in `internal/api/server.go` (the
Jobs/Tasks blocks), including the interaction with the cancel-deny path: the
cancel route's 404-on-deny is defense-in-depth for the destructive action rather
than a true existence secret, since these global reads already expose existence
and metadata to any authenticated user. No test change (no behavior change).

# Job and task READ routes have no owner check

## Summary
The job/task read endpoints are registered with `auth(...)` only and never
consult `UserFromCtx` for ownership, so any authenticated user can read any
job's metadata, its task list, and any task's logs regardless of who submitted
the job:

- `GET /v1/jobs/{id}` - `handleGetJob` (`internal/api/jobs.go:633`)
- `GET /v1/jobs/{id}/tasks`
- `GET /v1/tasks/{id}`
- `GET /v1/tasks/{id}/logs`

(routes registered around `internal/api/server.go:110-112`)

This is read-only information disclosure - strictly weaker than the
destructive-cancel hole that was just closed
([bug-2026-06-10-job-cancel-missing-authz](closed/bug-2026-06-10-job-cancel-missing-authz.md)),
which now denies a non-owner non-admin cancel with a 404 and zero side effects.
It is filed separately because it is a distinct, pre-existing gap and was out of
scope for that fix.

## Context
Global READ visibility across all jobs MAY be intentional render-farm semantics:
a shared farm where operators can inspect any job and its logs is a reasonable
product stance. So, like the cancel item, this needs an explicit decide-or-fix
rather than an assumed-bug fix - the answer is a product decision, not just a
code change.

What makes it worth tracking now: the cancel fix deliberately returns 404 (not
403) on deny to keep job existence enumeration-safe. But a non-owner can still
confirm a job exists - and read its full metadata and logs - via
`GET /v1/jobs/{id}`. So the read routes partially undercut the enumeration-hiding
intent of the cancel fix. The destructive path is now gated; the read paths that
leak the same existence signal are not. If the decision is "reads are global on
purpose," the cancel fix's 404 is still correct for the destructive action, but
the enumeration-safety rationale should be understood as defense-in-depth rather
than a true secret, and that should be written down so the contradiction is not
mistaken for a bug later.

## Proposal
Decide, then either document or fix:

- **If global read is intended:** document it next to the route registrations
  (and note the interaction with the cancel-deny 404 above, so the
  apparent contradiction is explained rather than rediscovered as a bug).
- **If reads should be owner-scoped:** mirror the now-established cancel pattern
  on each of the four routes - load the job (and resolve the owning job for the
  task routes), then:

  ```go
  u, _ := UserFromCtx(r.Context())
  if !u.IsAdmin && job.SubmittedBy != u.ID {
      writeError(w, http.StatusNotFound, "job not found")
      return
  }
  ```

  Admin bypasses; owner = `job.SubmittedBy == u.ID` via the direct `pgtype.UUID`
  `!=` idiom; 404 (not 403) to stay enumeration-safe and consistent with the
  cancel route. The task routes (`/v1/tasks/{id}`, `/v1/tasks/{id}/logs`) must
  resolve the task's parent job to find `SubmittedBy` before applying the gate.

If scoped, pin the behavior with a test that a non-owner non-admin GET returns
404 for each route, matching the no-side-effects assertion style of the cancel
test.

## Related
- [bug-2026-06-10-job-cancel-missing-authz](closed/bug-2026-06-10-job-cancel-missing-authz.md) -
  the closed destructive-path fix whose resolution links here; establishes the
  owner-or-admin / 404 / enumeration-safe pattern to mirror.
- `internal/api/jobs.go:633` (`handleGetJob`) and the `/v1/jobs/{id}/tasks` handler.
- The `/v1/tasks/{id}` and `/v1/tasks/{id}/logs` handlers (`internal/api/tasks.go`).
- `internal/api/server.go:110-112` (read-route registrations under `auth(...)`).
- `internal/api/scheduled_jobs.go` (`ownedScheduledJob`) - the contrasting
  owner-scoped read pattern already used for scheduled jobs.

## Notes
Priority set to **low**: this is read-only information disclosure with no
destructive capability, strictly weaker than the medium-rated cancel hole. The
practical exposure is metadata and logs, not mutation or escalation; and global
read may turn out to be intended, in which case the resolution is documentation
only. Raise to medium if a product decision lands on "reads must be owner-scoped"
and the leak is judged sensitive (e.g. logs can contain job-specific secrets or
paths).
