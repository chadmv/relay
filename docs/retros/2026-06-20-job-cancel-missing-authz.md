---
date: 2026-06-20
topic: job-cancel-missing-authz
branch: claude/suspicious-beaver-5f66ef
range: 9c2c59c..d24b189 (impl)
pr: 2026-06-20 / job-cancel-missing-authz
merge: 2026-06-20 / job-cancel-missing-authz
---

# Session Retro: 2026-06-20 - Job Cancel Missing Authz

**TL;DR:** Closed `bug-2026-06-10-job-cancel-missing-authz`, a destructive
authorization hole where `DELETE /v1/jobs/{id}` was registered with `auth(...)`
only and `handleCancelJob` never consulted `UserFromCtx` - so any authenticated
user could cancel any other user's job, killing its tasks and signalling agents
to terminate subprocesses. The fix adds an owner-or-admin gate that sits before
every side effect, returns 404 (not 403) to stay enumeration-safe, and is proven
by an integration test that asserts no side effects on deny, not merely the
status code. Code review surfaced a sibling read-route gap (out of scope) now
tracked as its own backlog item.

## What Was Built

A non-owner non-admin `DELETE /v1/jobs/{id}` now returns 404 with zero side
effects; admin bypasses; the owner is whoever submitted the job.

- **Gate placement** (`internal/api/jobs.go`, `handleCancelJob`) - immediately
  after the `GetJob` load and before `CancelJobTasks` / `UpdateJobStatus` /
  `tx.Commit` / the agent `CancelTask` signals:
  `if !u.IsAdmin && job.SubmittedBy != u.ID { writeError(w,
  http.StatusNotFound, "job not found"); return }`. A `!ok` 401 from
  `UserFromCtx` is added for defense-in-depth.
- **Owner check** - `job.SubmittedBy == u.ID` via the established direct
  `pgtype.UUID` `!=` idiom, matching `ownedScheduledJob` rather than inventing a
  new comparison helper.
- **Test** (`internal/api/jobs_cancel_test.go`) - a non-owner non-admin DELETE
  returns 404 AND leaves the job uncancelled, the task still `running`, and the
  agent sender snapshot empty (no kill signal sent); owner-can and
  admin-can-any cases stay green.

Confined to `internal/api/jobs.go` and `internal/api/jobs_cancel_test.go`.

## Key Decisions

- **404, not 403, on deny (enumeration safety).** Returning 403 would confirm to
  a non-owner that the job exists. 404 makes a job the caller does not own
  indistinguishable from a job that was never created, so the cancel route leaks
  no existence signal. This matched the contrasting scheduled-job pattern
  (`ownedScheduledJob` also 404s) and keeps the two resource families
  consistent.
- **Gate before side effects, so deny is zero-cost.** Placing the check
  immediately after the load - and before `CancelJobTasks`, the status update,
  the commit, and the agent signals - means a denied request hits the existing
  `defer tx.Rollback` and performs no work at all. There is no partial-cancel
  window to reason about: the open transaction simply rolls back. This is the
  same epoch/side-effect discipline the codebase applies to mutating paths -
  decide authorization before you touch state, not after.
- **The test asserts no side effects, not just the status code.** A test that
  only checked `404` would pass even if the handler returned 404 *after*
  cancelling the tasks - the exact bug the gate-before-side-effects placement
  prevents. So the test pins the real contract: job still uncancelled, task
  still `running`, empty sender snapshot (no agent kill signal). The status code
  is necessary but not sufficient evidence that the hole is closed.

## Sibling Read-Route Finding (out of scope, filed)

The Phase 4 code-review/security pass found a real, pre-existing, distinct gap:
the sibling READ routes have no owner check at all -
`GET /v1/jobs/{id}` (`handleGetJob`, `internal/api/jobs.go:633`),
`GET /v1/jobs/{id}/tasks`, `GET /v1/tasks/{id}`, and
`GET /v1/tasks/{id}/logs` (registered around `internal/api/server.go:110-112`).
Any authenticated user can read any job's metadata, task list, and task logs.

This is read-only information disclosure - strictly weaker than the
destructive-cancel hole just closed - but it also partially undercuts the
enumeration-hiding intent of the cancel fix's 404: a non-owner can still confirm
a job exists (and read its logs) via `GET /v1/jobs/{id}`. Whether global read is
intended render-farm semantics is a product decision, so like the cancel item
this needs an explicit decide-or-fix. It was correctly kept out of scope for the
destructive-path fix and is now tracked at
`docs/backlog/bug-2026-06-20-job-task-read-routes-missing-authz.md`
(priority low; the closed cancel item's resolution links to it).

## Files Most Touched

- `internal/api/jobs.go` - the owner-or-admin gate in `handleCancelJob`,
  positioned before all side effects.
- `internal/api/jobs_cancel_test.go` - the no-side-effects integration test
  (deny leaves job uncancelled, task running, sender snapshot empty) plus the
  owner-can and admin-can-any cases.
- `docs/backlog/closed/bug-2026-06-10-job-cancel-missing-authz.md` - closed,
  with a resolution that links the new read-route item.
- `docs/backlog/bug-2026-06-20-job-task-read-routes-missing-authz.md` - new,
  tracking the sibling read-route gap.
