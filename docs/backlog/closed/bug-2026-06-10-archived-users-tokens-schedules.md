---
title: Archived users - token validation race and schedules that keep firing
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-20
priority: medium
source: full-codebase review (2026-06-10)
---

## Resolution
Resolved 2026-06-20. Both offboarding gaps closed with two small, atomic backend
changes (no migration, no frontend):

1. **Auth race (Gap 1).** `GetTokenWithUser` now carries `AND u.archived_at IS NULL`,
   so an archived user's bearer token resolves to `pgx.ErrNoRows`, which `BearerAuth`
   already maps to `401 invalid token`. This is enforced in SQL (un-bypassable for any
   caller of the query) as defense-in-depth under the existing token-deletion-on-archive,
   and makes the login-vs-archive race harmless. No `middleware.go` change was needed.
2. **Schedules keep firing (Gap 2).** A new `DisableScheduledJobsByOwner` query
   (`:execrows`, `WHERE owner_id = $1 AND enabled = TRUE`) is called via the tx-scoped
   queries inside `handleAdminArchiveUser`, between `DeleteUserAPITokens` and
   `tx.Commit`, so archive + token-delete + schedule-disable commit atomically. Unarchive
   deliberately does **not** re-enable schedules (the table cannot distinguish
   archive-disabled from intentionally-paused; conservative).

Regression coverage in `internal/api/users_integration_test.go` verifies token rejection
at the HTTP/BearerAuth boundary, schedule-disable on archive, isolation of other owners'
and already-disabled schedules, and that unarchive does not re-enable. A side effect of
Gap 1's predicate: an archived admin's own token is now rejected at the auth boundary,
making the handler-level last-admin guard structurally unreachable via the API (it stays
as store-tested defense-in-depth). Spec: `docs/superpowers/specs/2026-06-20-archived-users-tokens-schedules-design.md`.
Deploy note (intentional non-goal): users archived before this deploy are not retroactively
backfilled - their stale tokens stop working on deploy, but already-enabled schedules need
a manual disable or re-archive.

# Archived users - token validation race and schedules that keep firing

## Summary
Two gaps around user archiving. (1) `GetTokenWithUser` joins `users` but has no `archived_at IS NULL` predicate, and `BearerAuth` does not check archived status; the only enforcement is token deletion inside the archive transaction. A login racing the archive (user row read before commit, token inserted after) leaves an archived user with a valid 30-day session. (2) `ListEligibleScheduledJobs` never checks the owner, and the archive handler does not disable or reassign the user's `scheduled_jobs`, so an offboarded user's schedules keep creating jobs attributed to them indefinitely.

## Proposal
- Add `AND u.archived_at IS NULL` to `GetTokenWithUser` (defense in depth; also makes the login race harmless).
- In the archive transaction: `UPDATE scheduled_jobs SET enabled = FALSE WHERE owner_id = $1` (explicit and auditable, preferable to filtering in the eligibility query).

## Related
- `internal/api/middleware.go:27-43` (`BearerAuth`)
- `internal/store/query/tokens.sql` (`GetTokenWithUser`)
- `internal/api/users.go:505-518` (`handleAdminArchiveUser`)
- `internal/store/query/scheduled_jobs.sql:48-54` (`ListEligibleScheduledJobs`)
