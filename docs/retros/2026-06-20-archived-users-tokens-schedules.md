---
date: 2026-06-20
topic: archived-users-tokens-schedules
branch: claude/dazzling-easley-b7e79e
range: 70e945b..3edd4cf (impl)
pr: 2026-06-20 / archived-users-tokens-schedules
merge: 2026-06-20 / archived-users-tokens-schedules
---

# Session Retro: 2026-06-20 - Archived Users: Token Validation Race and Schedules That Keep Firing

**TL;DR:** Closed `bug-2026-06-10-archived-users-tokens-schedules`, two user-offboarding
gaps found in the 2026-06-10 codebase review. Gap 1: an archived user's bearer token
could still authenticate, because `GetTokenWithUser` had no archived predicate and the
only enforcement was token deletion inside the archive transaction, which a login racing
the archive could sidestep (user row read before commit, token inserted after). Gap 2: an
offboarded user's `scheduled_jobs` kept firing indefinitely, creating jobs attributed to
them, because nothing disabled them at archive time. Both were closed with two small,
atomic backend changes (no migration, no frontend): an `AND u.archived_at IS NULL`
predicate on the token query, and a `DisableScheduledJobsByOwner` write folded into the
existing archive transaction. The fix is enforced in SQL, not handler code, so it is
un-bypassable and leaves `middleware.go` untouched. testcontainers integration tests
carried the verification.

## What Was Built

An archived user's token now resolves to `pgx.ErrNoRows` (which `BearerAuth` already maps
to `401 invalid token`), and archiving a user atomically disables all of their enabled
schedules.

- **Auth (Gap 1)** (`internal/store/query/tokens.sql`) - `GetTokenWithUser` gained
  `AND u.archived_at IS NULL`. No `middleware.go` change. This is defense-in-depth layered
  under the existing token-deletion-on-archive, and it makes the login-vs-archive race
  harmless: even if a token for an archived user exists, the query will not return it.
- **Schedules (Gap 2)** (`internal/store/query/scheduled_jobs.sql` + generated) - new
  `DisableScheduledJobsByOwner` (`:execrows`, `WHERE owner_id = $1 AND enabled = TRUE`).
- **Archive transaction** (`internal/api/users.go`, `handleAdminArchiveUser`) - the new
  query is called via the tx-scoped queries between `DeleteUserAPITokens` and
  `tx.Commit`, so archive + token-delete + schedule-disable commit as one unit.
- **Tests** (`internal/api/users_integration_test.go`) - 6 new regression tests plus
  helpers, covering token rejection at the HTTP/BearerAuth boundary, schedule-disable on
  archive, isolation of other owners' schedules and already-disabled schedules, and that
  unarchive does not re-enable. The obsolete `TestArchiveUser_LastAdminGuard` was removed:
  it depended on an archived admin's token still authenticating, which the Gap 1 fix now
  correctly 401s.

Confined to the two query files (plus their generated `.sql.go`), `internal/api/users.go`,
and the integration test file.

## Key Decisions

- **Auth enforced in SQL, not in handler code.** Adding the predicate to `GetTokenWithUser`
  makes the rule un-bypassable for every caller of the query and keeps `BearerAuth` /
  `middleware.go` untouched. A handler-level archived check would have been one more place
  to forget; the SQL predicate cannot be sidestepped.
- **Disable-at-archive-time, not filter-at-eligibility-time.** The alternative was adding
  an owner-archived join to `ListEligibleScheduledJobs`. Disabling at archive time keeps
  `enabled` the single source of truth for whether a schedule fires, makes the disable
  explicit and auditable, and avoids a users-join on every schedrunner tick.
- **Unarchive does not re-enable schedules (conservative).** The `scheduled_jobs` table
  cannot distinguish a schedule that was disabled by archiving from one a user
  intentionally paused. Re-enabling on unarchive would silently resurrect intentionally
  paused schedules, so unarchive deliberately leaves `enabled` alone. Re-enabling is a
  manual admin action.
- **Atomicity via the existing archive transaction.** The disable is folded into the same
  tx as `ArchiveUser` and `DeleteUserAPITokens`, so an offboard is all-or-nothing: a failed
  commit leaves the user active with working tokens and live schedules, never a half-state
  with tokens revoked but schedules still firing.

## Problems Encountered

- **A security predicate at the auth boundary rendered a downstream handler guard
  unreachable.** Gap 1's `AND u.archived_at IS NULL` matches the predicate
  `CountActiveAdmins` already uses, so the authenticated admin running the archive is
  always counted as active (n >= 2), which makes the handler-level last-admin guard in
  `handleAdminArchiveUser` structurally unreachable via the API. The verify phase caught
  this. The engineer correctly **refused** to add a production-only test seam to fabricate
  coverage for provably route-dead code. The guard stays as store-tested
  (`TestCountActiveAdmins`) defense-in-depth; the obsolete HTTP-level
  `TestArchiveUser_LastAdminGuard`, which only passed because of the old auth behavior, was
  removed rather than propped up.

## Improvement Goals

- **When you add a predicate at the auth boundary, audit downstream guards.** A new
  authentication or authorization predicate can silently make handler-level guards
  unreachable by changing what the rest of the request can observe (here, who counts as an
  active admin). Treat "does this make any downstream check dead?" as a standard part of
  reviewing an auth-boundary change.
- **Do not fabricate coverage for dead-but-defensive branches.** A defense-in-depth guard
  that is provably unreachable through the public surface should be covered at the layer
  where it can actually fire (store/unit), not behind a production test seam that exists
  only to reach dead code. Removing a now-false HTTP test is the right move, not rewriting
  it to keep a green check.
- **Backend security fixes verified well under testcontainers.** This was backend-only, so
  the missing live-backend E2E gate (`idea-2026-06-03-web-e2e-harness`) was less relevant;
  the integration tests asserting rejection at the real HTTP/BearerAuth boundary gave high
  confidence. Worth remembering that the E2E gap bites the web workstream, not pure-backend
  security work.

## Backlog Triage

Both triage candidates were considered and **declined** as new items; neither is worth a
tracked entry.

1. **Operational backfill of pre-existing archived users' enabled schedules - DECLINED.**
   The deploy note already lives in the closed backlog item
   (`docs/backlog/closed/bug-2026-06-10-archived-users-tokens-schedules.md`): users archived
   before this deploy are not retroactively backfilled - their stale tokens stop working on
   deploy (the SQL predicate is retroactive), but already-enabled schedules need a manual
   disable or re-archive. This is a one-time, run-once-on-deploy operational action with a
   trivial remedy (re-archive or a one-line `UPDATE`), not ongoing engineering work, and the
   deploy note is the right home for it. Filing a tracked bug would imply latent product
   risk that does not exist. The closed-item deploy note is sufficient.

2. **Atomicity rollback fault-injection test for the archive tx - DECLINED.** Verify flagged
   this low. The three writes already share one transaction with a `defer tx.Rollback`, which
   is the standard codebase idiom; injecting a mid-tx fault to assert rollback would test
   pgx/Postgres transaction semantics rather than relay logic, and there is no precedent in
   the suite for that kind of test. Low value, not specific enough to a relay defect to
   warrant tracking.

No new backlog items filed this cycle.

## Files Most Touched

- `internal/store/query/tokens.sql` - `GetTokenWithUser` gained `AND u.archived_at IS NULL`.
- `internal/store/query/scheduled_jobs.sql` - new `DisableScheduledJobsByOwner`.
- `internal/api/users.go` - `handleAdminArchiveUser` calls `DisableScheduledJobsByOwner`
  inside the existing archive transaction.
- `internal/api/users_integration_test.go` - 6 new regression tests and helpers; removed the
  now-obsolete `TestArchiveUser_LastAdminGuard`.
- `docs/superpowers/specs/2026-06-20-archived-users-tokens-schedules-design.md` - the design.
- `docs/backlog/closed/bug-2026-06-10-archived-users-tokens-schedules.md` - closed, with a
  resolution that records both gaps, the conservative unarchive choice, the unreachable-guard
  side effect, and the deploy note.
