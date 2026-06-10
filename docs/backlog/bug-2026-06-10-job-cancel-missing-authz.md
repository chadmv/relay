---
title: Any authenticated user can cancel any other user's job
type: bug
status: open
created: 2026-06-10
priority: medium
source: full-codebase review (2026-06-10)
---

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
