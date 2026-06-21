---
title: Archived users - token validation race and schedules that keep firing
type: design
status: approved
created: 2026-06-20
mode: autonomous
backlog: docs/backlog/bug-2026-06-10-archived-users-tokens-schedules.md
scope: backend store/api only (no frontend)
---

# Archived users - token validation race and schedules that keep firing

## Autonomous mode note

This spec was authored in autonomous mode: no human was available to answer
gate questions, so the design decisions below were made by the author and are
recorded inline with rationale. Each decision flagged "AUTONOMOUS DECISION" is a
point where a human reviewer might reasonably choose differently; they are
called out so a reviewer can override before implementation.

## Problem

Two independent gaps appear when an admin archives a user. Both were found in
the 2026-06-10 full-codebase review and confirmed against current code while
writing this spec.

### Gap 1 - archived user can keep a valid session

`handleAdminArchiveUser` (`internal/api/users.go`) archives the user and deletes
their API tokens inside one transaction (`ArchiveUser` then `DeleteUserAPITokens`
via `txq`, committed together). That covers tokens that already exist at archive
time. It does not cover a token created concurrently:

1. A login request reads the user row (user still active) and begins computing
   the session token.
2. The archive transaction commits (user archived, existing tokens deleted).
3. The login request inserts its freshly minted token.

The new token now points at an archived user and survives the archive. Nothing
on the read path rejects it: `GetTokenWithUser` (`internal/store/query/tokens.sql`)
joins `users` purely to hydrate name/email/admin and has no `archived_at`
predicate, and `BearerAuth` (`internal/api/middleware.go`) only checks token
existence and expiry. Tokens default to a 30-day TTL, so the archived user keeps
access for up to 30 days.

This race is narrow but real, and the existing integration test
`TestArchiveUser_LastAdminGuard` documents in its comment that "BearerAuth does
not filter on archived_at," confirming the gap is known and currently
unguarded.

### Gap 2 - an archived owner's schedules keep firing

`scheduled_jobs` rows carry an `owner_id`. The schedrunner's eligibility queries
`ListEligibleScheduledJobs` and `ListOverdueScheduledJobsForCatchup`
(`internal/store/query/scheduled_jobs.sql`) select on `enabled` and timing only;
neither consults the owner's archived status. `handleAdminArchiveUser` does
nothing to the user's `scheduled_jobs`. So after an offboarded user is archived,
their cron schedules keep creating jobs indefinitely, attributed to a user who
can no longer log in.

## Goals

- An archived user cannot authenticate, even if a token was minted in a race
  with the archive.
- An archived user's scheduled jobs stop firing as part of archiving them.
- Changes are auditable and explicit.

## Non-goals

- Reassigning a departed user's schedules to another owner. Out of scope; the
  schedules are disabled, not transferred or deleted.
- Auto re-enabling schedules on unarchive (see AUTONOMOUS DECISION 2).
- Any frontend change. This is backend store/api only. The admin UI already
  surfaces archive/unarchive; schedule enabled-state is already shown via the
  existing schedules views and needs no new affordance for this fix.
- Reducing token TTL or adding token revocation lists. The archived-user check
  makes those unnecessary for this bug.

## Design

Two small, independent changes. They do not interact, but ship together because
they close the same offboarding hole.

### Change A - reject archived users at the auth boundary

Add `AND u.archived_at IS NULL` to the `GetTokenWithUser` join predicate.

```sql
-- name: GetTokenWithUser :one
SELECT
    t.id          AS token_id,
    ...
FROM api_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1
  AND u.archived_at IS NULL;
```

With this predicate, a token belonging to an archived user produces
`pgx.ErrNoRows`, which `BearerAuth` already maps to `401 invalid token`. No
change to `middleware.go` is required: the existing `err != nil` branch handles
it. This is the single enforcement point because all authenticated requests go
through `GetTokenWithUser`.

AUTONOMOUS DECISION 1 - enforce at the query, not in handler code.
Rationale: putting the check in the SQL predicate makes it impossible to bypass
from any caller of `GetTokenWithUser`, present or future, and keeps `BearerAuth`
unchanged (smaller diff, the existing 401 path is reused). The alternative -
selecting `archived_at` and branching in `middleware.go` - leaks an extra column
and an extra branch into hot-path Go code for no benefit, and a future second
caller of the query would not get the protection. The query-level check is
strictly defense in depth layered under the existing token-deletion-on-archive;
both stay.

Security note: the response for an archived user's token is identical to the
response for a nonexistent token (`401 invalid token`). This is correct - it
does not leak whether a token ever existed or whether a user was archived.

### Change B - disable an archived user's schedules in the archive transaction

Add a new sqlc query and call it inside the existing archive transaction.

New query (`internal/store/query/scheduled_jobs.sql`):

```sql
-- name: DisableScheduledJobsByOwner :execrows
UPDATE scheduled_jobs
SET enabled = FALSE,
    updated_at = NOW()
WHERE owner_id = $1
  AND enabled = TRUE;
```

In `handleAdminArchiveUser` (`internal/api/users.go`), call it via `txq`
alongside the existing token deletion, before `tx.Commit`:

```go
row, err := txq.ArchiveUser(ctx, id)
// ... existing error handling ...

if _, err := txq.DeleteUserAPITokens(ctx, id); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to revoke tokens")
    return
}

if _, err := txq.DisableScheduledJobsByOwner(ctx, id); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to disable schedules")
    return
}

if err := tx.Commit(ctx); err != nil { ... }
```

Because this runs inside the same transaction as `ArchiveUser`, archiving and
schedule-disabling commit atomically: there is no window where the user is
archived but their schedules are still enabled, and none where schedules are
disabled but the archive rolled back.

AUTONOMOUS DECISION 3 - disable at archive time rather than filter in the
eligibility queries. Rationale: this matches the backlog proposal and is more
auditable - the `scheduled_jobs.enabled` column reflects reality, so the
schedules UI and `relay schedules list` show the schedule as disabled with no
special-casing, and `updated_at` records when it happened. Filtering on owner
archived status inside `ListEligibleScheduledJobs` would (a) require joining
`users` in two hot polling queries on every tick, (b) leave the stored
`enabled` flag lying about the true state, and (c) silently re-fire all the
schedules the instant the owner is unarchived, which contradicts DECISION 2.
We do NOT additionally add an owner-archived filter to the eligibility queries;
the atomic disable in the same transaction as the archive is sufficient, and a
redundant join on every poll tick is not worth the cost. The `enabled` column
is the single source of truth.

The `enabled = TRUE` predicate in the WHERE clause means already-disabled
schedules are left untouched (their `updated_at` is not bumped), and
`:execrows` lets the handler observe how many were disabled if we ever want to
log it. The handler does not currently need the count, so it is discarded with
`_`; the `:execrows` form is chosen over `:exec` only so a future audit-log line
is a one-line change.

## Decision: unarchive does not re-enable schedules

AUTONOMOUS DECISION 2 - archiving disables schedules; unarchiving does NOT
re-enable them. `handleAdminUnarchiveUser` is unchanged.

Rationale: a schedule can be disabled for two reasons - archived by this flow,
or paused intentionally by the user before they were archived. The
`scheduled_jobs` table does not record which, and adding provenance tracking is
out of scope for a defect fix. Auto re-enabling on unarchive would resurrect
schedules the user had deliberately paused, which is a worse failure (unexpected
job execution) than the conservative behavior (admin or user re-enables the
specific schedules they want). The unarchived user regains the ability to enable
their own schedules through the normal update path, so nothing is permanently
lost. This is the conservative choice the backlog item asked for.

If a future requirement demands "restore exactly the schedules archiving
disabled," that needs a provenance column (e.g. `disabled_by_archive_at`) and is
a separate spec.

## Failure modes and load

- Auth path: the added predicate is covered by the existing partial index
  `users_active_idx ON users (id) WHERE archived_at IS NULL` (migration
  000010). The join already filters by `t.token_hash` (the primary selector) and
  resolves a single user row by PK; adding `archived_at IS NULL` is a cheap
  column check on that one row. No measurable hot-path cost.
- Archive path: `DisableScheduledJobsByOwner` is a bounded `UPDATE` over one
  user's schedules (realistically a handful of rows), run once per archive
  operation - an admin action, not a hot path. It takes row locks only on that
  owner's `scheduled_jobs` rows. The schedrunner's `ListEligibleScheduledJobs`
  uses `FOR UPDATE SKIP LOCKED`, so even if a tick races the archive, it skips
  any row the archive transaction has locked rather than blocking, and once the
  archive commits those rows are `enabled = FALSE` and no longer eligible.
- No new gRPC, stream, or epoch surface is touched, so no project Invariant
  (epoch fence, single sender per stream, identity-checked teardown, single
  job-spec pipeline, single JSON entry point) is affected. The single-job-spec
  pipeline is untouched: we disable schedules, we do not create or validate
  jobs.

## Files and queries to change

Backend store/api only. No frontend.

1. `internal/store/query/tokens.sql` - add `AND u.archived_at IS NULL` to
   `GetTokenWithUser`.
2. `internal/store/query/scheduled_jobs.sql` - add new query
   `DisableScheduledJobsByOwner` (`:execrows`).
3. `internal/api/users.go` - in `handleAdminArchiveUser`, call
   `txq.DisableScheduledJobsByOwner(ctx, id)` inside the existing transaction,
   after `DeleteUserAPITokens` and before `tx.Commit`.
4. `make generate` - regenerate `internal/store/tokens.sql.go` and
   `internal/store/scheduled_jobs.sql.go` from the edited `.sql` files. Follow
   CLAUDE.md's sqlc LF/CRLF guidance (`git diff --ignore-all-space`, revert
   LF-only hunks). Do NOT hand-edit `*.sql.go`.

New sqlc query needed: YES - `DisableScheduledJobsByOwner` in
`scheduled_jobs.sql`. No new migration: both `users.archived_at` and
`scheduled_jobs.enabled` columns already exist.

`internal/api/middleware.go` is NOT changed - the query-level predicate reuses
the existing 401 path.

## Testing

Integration tests (Postgres via testcontainers; `//go:build integration`), in
the packages that own the behavior:

Gap 1 (`internal/api` or `internal/store`):
- Token for an archived user is rejected: seed user, mint a token directly,
  archive the user via direct SQL (to leave the token in place, mirroring the
  race), then assert an authenticated request with that token returns
  `401 invalid token`. This is the regression test for the race.
- Token for an active user still authenticates (no regression).

Gap 2 (`internal/api`):
- Archiving a user with enabled schedules flips them to `enabled = FALSE`:
  seed user, create enabled scheduled jobs, archive via the handler, assert the
  rows are now disabled and `updated_at` advanced.
- An already-disabled schedule owned by the user is untouched (its `updated_at`
  is not bumped) - guards the `enabled = TRUE` predicate.
- Unarchiving does NOT re-enable schedules: archive (schedules disabled),
  unarchive, assert schedules remain `enabled = FALSE`. Locks in DECISION 2.
- Atomicity: schedules of OTHER users are not affected by archiving one user.

## Rollout

Single deploy. No migration, no config flag, no data backfill. The auth-path
change takes effect immediately for all tokens; any token currently held by an
already-archived user (if one slipped through before this fix) stops working on
deploy, which is the desired behavior. Existing schedules owned by
already-archived users remain `enabled` until those users are re-archived or an
admin disables the schedules manually; if backfilling them is wanted, that is a
one-line `UPDATE` operational task, called out here but intentionally not
automated in this change (it is not a code path).
