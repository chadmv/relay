---
title: Any authenticated user can cancel any other user's job
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-20
priority: medium
source: full-codebase review (2026-06-10)
---

## Resolution
Fixed 2026-06-20 (job-cancel-missing-authz). Added an owner-or-admin gate in
`handleCancelJob` (`internal/api/jobs.go`) immediately after the `GetJob` load and
before any side effect: `if !u.IsAdmin && job.SubmittedBy != u.ID { writeError(w,
http.StatusNotFound, "job not found"); return }` (plus a `!ok` 401 from
`UserFromCtx` for defense-in-depth). Admin bypasses; owner = `job.SubmittedBy ==
u.ID` via the established direct `pgtype.UUID` `!=` idiom (matching
`ownedScheduledJob`). 404 (not 403) keeps it enumeration-safe. The gate sits before
`CancelJobTasks`/`UpdateJobStatus`/`tx.Commit`/agent `CancelTask` signals, so a deny
hits the existing `defer tx.Rollback` with zero side effects. An integration test
proves a non-owner non-admin DELETE returns 404 AND leaves the job uncancelled, the
task still `running`, and no agent signal sent (empty sender snapshot); owner-can and
admin-can-any cases stay green. Confined to `internal/api/jobs.go` and
`internal/api/jobs_cancel_test.go`. Code review noted (out of scope, filed separately)
that the sibling READ routes still lack owner scoping - see
[job-task-read-routes-missing-authz](bug-2026-06-20-job-task-read-routes-missing-authz.md).

# Any authenticated user can cancel any other user's job

## Summary
`DELETE /v1/jobs/{id}` is registered with `auth(...)` only, and `handleCancelJob` never reads `UserFromCtx` - there is no owner-or-admin check before cancelling all tasks and signalling agents to kill subprocesses. This contrasts with scheduled jobs, which are strictly owner/admin scoped via `ownedScheduledJob`. Global read visibility may be intentional render-farm semantics, but destructive cancel by an arbitrary non-owner deserves an explicit decision.

## Proposal
Decide and either document it next to the route, or add:

```go
u, _ := UserFromCtx(r.Context())
if !u.IsAdmin && job.SubmittedBy != u.ID {
    writeError(w, http.StatusNotFound, "job not found")
    return
}
```

## Related
- `internal/api/jobs.go:678-772` (`handleCancelJob`)
- `internal/api/server.go:106` (route registration)
- `internal/api/scheduled_jobs.go:149-169` (`ownedScheduledJob`, the contrasting pattern)
