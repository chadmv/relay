# Job Cancel: Missing Owner-or-Admin Authorization

- Date: 2026-06-10 (backlog item); spec authored 2026-06-20
- Status: design approved (autonomous mode - decision recorded, no human sign-off gate)
- Backlog: `docs/backlog/bug-2026-06-10-job-cancel-missing-authz.md`
- Scope: backend security fix, `internal/api/jobs.go` only

## Problem

`DELETE /v1/jobs/{id}` is registered with `auth(...)` only (`internal/api/server.go:107`),
and `handleCancelJob` (`internal/api/jobs.go:677-768`) never reads the authenticated
user. After loading the job it proceeds straight to cancelling every non-terminal task
(`CancelJobTasks`), flipping the job to `cancelled`, and signalling agents to kill the
running subprocesses (`CancelTask`).

Result: any authenticated user can destructively cancel any other user's job. This is a
real authorization hole. The analogous scheduled-jobs mutations are already strictly
owner/admin scoped through `ownedScheduledJob` (`internal/api/scheduled_jobs.go:146-168`),
which makes the job-cancel path an inconsistent gap rather than an intentional design.

## Decision

Add an owner-or-admin authorization gate to `handleCancelJob`. Do **not** leave global
cancel as-is.

Rationale:
- Destructive cancel by an arbitrary non-owner is a genuine authz hole: it kills running
  work and signals agents to terminate subprocesses.
- The codebase already scopes the equivalent scheduled-jobs mutations to owner/admin
  (`ownedScheduledJob`), so this aligns the destructive job path with an existing,
  established pattern rather than inventing a new policy.
- Global **read** visibility of jobs may be intentional render-farm semantics and is out
  of scope here. This fix governs only the destructive **write** (cancel).

Policy:
- Owner is `job.SubmittedBy == user.ID`.
- Admin (`user.IsAdmin`) bypasses the owner check and may cancel any job.
- A non-owner, non-admin caller receives **404 "job not found"**, not 403. This matches
  the enumeration-safe pattern `ownedScheduledJob` already uses
  (`internal/api/scheduled_jobs.go:163-165`): a caller who does not own the job cannot
  even confirm it exists.

## Implementation

All identifiers below are verified against current code.

- `AuthUser` (`internal/api/context.go:14-20`) has fields `ID pgtype.UUID` and
  `IsAdmin bool`. `UserFromCtx(ctx) (AuthUser, bool)` reads it from request context.
- `store.Job.SubmittedBy` is `pgtype.UUID` (`internal/store/models.go:46`). The job is
  loaded via `q.GetJob(ctx, id)` at `internal/api/jobs.go:695`, before any cancel or
  signal side effect.
- The error helper is `writeError(w http.ResponseWriter, status int, msg string)`
  (`internal/api/server.go:176`); there is no `writeJSONError`. Use `writeError`.
- `pgtype.UUID` equality with `!=` is the existing idiom: `ownedScheduledJob` compares
  `row.OwnerID != u.ID` the same way.

Placement: immediately after the successful `GetJob` load and before the terminal-state
conflict check at `internal/api/jobs.go:704` (and therefore before `CancelJobTasks`,
`UpdateJobStatus`, the agent `CancelTask` sends, and the broker publish). The gate must
sit on the path with the loaded `job` in scope and before any side effect.

The change is the gate plus reading the user. Approximately:

```go
u, ok := UserFromCtx(ctx)
if !ok {
    writeError(w, http.StatusUnauthorized, "unauthorized")
    return
}
if !u.IsAdmin && job.SubmittedBy != u.ID {
    writeError(w, http.StatusNotFound, "job not found")
    return
}
```

Notes:
- The `!ok` branch mirrors `ownedScheduledJob` defensively; under the `auth(...)`
  middleware a user is always present, but the check keeps the handler self-contained and
  avoids a zero-value `AuthUser` comparison.
- The transaction is already open at this point (`tx` begun at line 686, `defer
  tx.Rollback`). Returning here rolls back cleanly with no committed state and no agent
  signal, since both occur only after `tx.Commit` at line 745. The gate is purely
  pre-side-effect.

## Out of scope / no other handler changes

- This is the only destructive job-mutation route. `handleCreateJob` already reads the
  user; job read routes (`handleGetJob`, `handleListJobs`, stats) are left as-is
  (global read visibility is a separate, possibly intentional, decision).
- Per-task retry/requeue routes live in `internal/api/tasks.go`, not this handler, and
  are not part of this fix. No same-handler gap exists.

## Invariants

- Epoch fence: untouched. `CancelJobTasks` still owns the epoch bump; this fix adds no
  task-status write and no epoch logic.
- Single JSON entry point, one bounded sender per stream, identity-checked teardown: all
  untouched. This is a pure pre-side-effect authorization gate.

## Success criteria

1. A non-owner, non-admin caller doing `DELETE /v1/jobs/{id}` on another user's job
   receives HTTP 404 with body `{"error":"job not found"}`.
2. No side effects occur on that 404: the job remains in its prior status (not
   `cancelled`), its tasks are unchanged, and no agent `CancelTask` signal is sent.
   (The transaction rolls back; agent sends and broker publish happen only post-commit.)
3. The job owner can cancel their own job (existing success behaviour, HTTP 200,
   job becomes `cancelled`).
4. An admin can cancel any job, including one they do not own (HTTP 200,
   job becomes `cancelled`).
5. The change is surgical: roughly 4-8 lines added in `handleCancelJob` plus tests; no
   other handler or query modified.

## Testing

Add API/integration tests exercising all three authorization paths against
`handleCancelJob`. Follow the existing table/integration style in
`internal/api/*_test.go` (real Postgres via testcontainers under `//go:build
integration`, `SetBcryptCostForTest` where users are created).

- Owner cancels own job -> 200, job status `cancelled`.
- Admin cancels another user's job -> 200, job status `cancelled`.
- Non-owner non-admin cancels another user's job -> 404 `job not found`, and assert
  **no side effects**: re-fetch the job and confirm its status is unchanged (still
  e.g. `pending`/`running`), and confirm the underlying tasks were not cancelled. The
  no-side-effect assertion is the security-critical part of this test and must not be
  omitted.

A unit-style test that does not hit Postgres is acceptable for the gate logic only if it
can construct a loaded job and authenticated context, but the integration test covering
the no-side-effect rollback is the primary coverage.

## Risks and tradeoffs

- 404-vs-403 enumeration choice: returning 404 hides existence from non-owners, matching
  `ownedScheduledJob`. The minor cost is that a legitimate owner who mistypes/loses access
  cannot distinguish "does not exist" from "not yours". This is the deliberate,
  consistent project convention and is accepted.
- The `job` is loaded by primary key regardless of caller, so the 404 path still performs
  one indexed read before denying. This is identical to the existing scheduled-jobs flow
  and is not a meaningful information leak (timing is dominated by a single PK lookup).
- Global job read visibility is intentionally left unchanged; this fix narrows only the
  destructive write surface.
