---
title: Archived users - token validation race and schedules that keep firing
type: bug
status: open
created: 2026-06-10
priority: medium
source: full-codebase review (2026-06-10)
---

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
