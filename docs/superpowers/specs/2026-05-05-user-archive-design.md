# User Archive (Admin Soft-Delete) — Design

**Date:** 2026-05-05
**Status:** Approved for implementation

## Problem

Admins have no way to remove a user from Relay. Once a user is created, they can change their name and password, be promoted to admin, but cannot be deactivated or deleted. This is a gap for offboarding (departing teammates), compromised account response, and cleaning up test accounts.

A hard `DELETE` is incompatible with the existing schema by design: `jobs.submitted_by` is `ON DELETE RESTRICT` with the comment "intentional: preserve job history" (see [internal/store/migrations/000001_initial.up.sql:44](../../../internal/store/migrations/000001_initial.up.sql)). Any user who has ever submitted a job cannot be hard-deleted without violating that intent.

## Solution

Add an admin-only **archive** capability for users. Archive is a soft-delete: a non-null `archived_at` timestamp on `users`. Archive is reversible via an explicit unarchive action. Hard delete is **not** added in this iteration.

When a user is archived:

- They cannot log in. `handleLogin` rejects archived users with the existing generic "invalid credentials" message, preserving the email-enumeration timing defense.
- All of their API tokens are revoked (`api_tokens` rows deleted in the same transaction). Existing cached tokens become invalid immediately.
- Their `email` stays locked (the `UNIQUE` constraint on `users.email` is preserved). No new user can register with that address while the original remains in the table. Job and audit history retains the original email.
- Their **scheduled jobs continue to fire**, their **running jobs and tasks continue to execute**, and their **agent enrollments remain valid**. Archive offboards the human, not the workloads they own. Admins who need to disable a schedule or cancel a job do so explicitly.

Two guards apply at archive time:

- **Self-archive forbidden.** An admin attempting to archive their own user ID receives `400 cannot archive yourself`.
- **Last-admin guard.** Archiving the last active (non-archived) admin returns `400 cannot archive the last active admin`.

Unarchive (`POST /v1/users/{id}/unarchive`) clears `archived_at`. The user's old API tokens stay revoked — they must log in again to obtain a new one. Unarchive does not need a transaction; it is a single `UPDATE`.

## Schema

New migration `000010_user_archive.up.sql`:

```sql
ALTER TABLE users ADD COLUMN archived_at TIMESTAMPTZ;
CREATE INDEX users_active_idx ON users (id) WHERE archived_at IS NULL;
```

Down migration drops the index then the column.

The partial index supports the "active users only" lookup, which becomes the default for `ListUsers` and the login path.

After the migration runs, `make generate` regenerates `internal/store/users.sql.go` and `internal/store/models.go`. `store.User` gains `ArchivedAt pgtype.Timestamptz`. **Generated files are not hand-edited.**

## Store queries

Edit `internal/store/query/users.sql`:

**New queries:**

```sql
-- name: ArchiveUser :one
UPDATE users SET archived_at = NOW()
WHERE id = $1 AND archived_at IS NULL
RETURNING id, email, name, is_admin, created_at, archived_at;

-- name: UnarchiveUser :one
UPDATE users SET archived_at = NULL
WHERE id = $1 AND archived_at IS NOT NULL
RETURNING id, email, name, is_admin, created_at, archived_at;

-- name: CountActiveAdmins :one
SELECT COUNT(*) FROM users
WHERE is_admin = TRUE AND archived_at IS NULL;

-- name: DeleteUserAPITokens :execrows
DELETE FROM api_tokens WHERE user_id = $1;

-- name: ListUsersIncludingArchived :many
SELECT id, email, name, is_admin, created_at, archived_at
FROM users
ORDER BY created_at;
```

The state-filter clauses on `ArchiveUser` / `UnarchiveUser` (`WHERE ... AND archived_at IS [NOT] NULL`) cause a no-op call to return `pgx.ErrNoRows`. The handler distinguishes "not found" from "wrong state" via a separate `GetUser` probe and surfaces `409 Conflict` for the latter.

**Modified queries:**

- `ListUsers` — add `WHERE archived_at IS NULL`. Row shape unchanged (does not return `archived_at`, since by definition all returned rows have it null).

**Unchanged queries (deliberately):**

- `GetUserByEmail` — login path uses this, then checks `ArchivedAt.Valid` in Go. Filtering at the SQL layer would short-circuit the bcrypt timing defense by skipping the dummy compare for archived users.
- `GetUserByEmailPublic` — admin email-lookup branch of `GET /v1/users` filters in Go based on `?include_archived`.
- `GetUser` — returns the row including `archived_at`; handlers decide what to do.

After editing, run `make generate`.

## API

Two new routes registered in [internal/api/server.go](../../../internal/api/server.go) alongside the existing `/v1/users` routes (line 120 area), both behind `BearerAuth` + `AdminOnly`:

```
POST /v1/users/{id}/archive    → handleAdminArchiveUser
POST /v1/users/{id}/unarchive  → handleAdminUnarchiveUser
```

`GET /v1/users` accepts an additional optional query parameter: `?include_archived=true`.

### `handleAdminArchiveUser`

Single transaction (`pool.BeginTx` → `q.WithTx(tx)`):

1. Parse `{id}` → UUID. Return `400 invalid user id` on parse failure.
2. Pull `AuthUser` from context. If `authUser.ID == id`, return `400 cannot archive yourself`.
3. `q.GetUser(ctx, id)`. Return `404 user not found` on `pgx.ErrNoRows`, `500` on other error.
4. If target row has `IsAdmin && !ArchivedAt.Valid`:
   - `q.CountActiveAdmins(ctx)`. If `<= 1`, return `400 cannot archive the last active admin`.
5. `q.ArchiveUser(ctx, id)`. If `pgx.ErrNoRows`, return `409 user is already archived`.
6. `q.DeleteUserAPITokens(ctx, id)`. Row count is logged but not surfaced.
7. `tx.Commit()`. On failure, rollback and return `500`.
8. Return `200` with `userResponse` (now including `archived_at`).

### `handleAdminUnarchiveUser`

No transaction needed.

1. Parse `{id}`, pull `AuthUser`. Reject self-target with the same `400` (defensive — an archived user shouldn't be logged in, but cheap to check).
2. `q.GetUser(ctx, id)`. `404` on not found.
3. `q.UnarchiveUser(ctx, id)`. `409 user is not archived` on `pgx.ErrNoRows`.
4. Return `200` with the updated `userResponse`.

### `handleListUsers` changes

Read `?include_archived=true`.

- If true → `q.ListUsersIncludingArchived`.
- If absent or false → existing `q.ListUsers` (now archived-filtered at the SQL layer).
- Email-lookup branch (`?email=`) calls the unchanged `GetUserByEmailPublic`. If the row is archived and `include_archived` is not set, return `[]userResponse{}` — matches the existing empty-list behavior for unknown emails and preserves enumeration resistance for archived users.

### `userResponse` shape

Add a single field:

```go
ArchivedAt *time.Time `json:"archived_at"`
```

Pointer so it serializes as `null` for active users. The `toUserResponse` helper grows a parameter (or a sibling helper takes the full row); active-only callers pass `nil` or are migrated to a helper that derives it from `pgtype.Timestamptz`.

### `handleLogin` change

Single new check in `internal/api/auth.go`, slotted **after** the bcrypt compare and **before** token issuance:

```go
if user.ArchivedAt.Valid {
    writeError(w, http.StatusUnauthorized, "invalid credentials")
    return
}
```

Placement after bcrypt is load-bearing: it keeps the timing path identical for active-wrong-password, archived-correct-password, and unknown-email (which already runs the dummy compare). The user-facing message matches a wrong password.

## CLI

Edit `internal/cli/admin_users.go`. Two new subcommands wired into the existing `doAdminUsers` switch:

```
relay admin users archive   <email-or-id>
relay admin users unarchive <email-or-id>
```

Both follow the same pattern as `update` ([internal/cli/admin_users.go:105](../../../internal/cli/admin_users.go)): the positional accepts either a UUID (used directly) or an email (resolved via `GET /v1/users?email=...` first, using `looksLikeUUID`). Then `POST /v1/users/{id}/archive` or `/unarchive`. Print the resulting user via `printUserDetail`, which gains an `Archived:` line that shows the timestamp or `no` if `archived_at` is null.

`relay admin users list` gains a `--include-archived` boolean flag. When set, the CLI sends `?include_archived=true` and `printUsersTable` adds an `ARCHIVED` column. Without the flag, behavior is unchanged.

`userListItem` struct gains `ArchivedAt *time.Time \`json:"archived_at"\``.

No changes to `register.go`, `login.go`, `passwd.go`, or other flows. The server-side `handleLogin` rejection is sufficient; the CLI sees a generic `401 invalid credentials` and surfaces it as a login failure.

## Testing

### Unit tests

`internal/cli/admin_users_test.go` — argument parsing for `archive`, `unarchive`, and `list --include-archived`; UUID-vs-email resolution; error formatting. Existing tests use a fake server via `httptest`; new ones follow the same pattern.

`internal/api/users_test.go` / `auth_test.go` — pure-Go coverage is limited because the handlers funnel through sqlc; what's testable in unit form: parse failures (400 on invalid UUID), self-archive guard at the auth-context level.

### Integration tests (`//go:build integration`)

In `internal/api/users_integration_test.go` and `auth_integration_test.go`. All use the existing testcontainer harness with bcrypt-min-cost.

1. **Happy path archive.** Admin archives a regular user → `200`, `archived_at` non-null. The user's pre-existing API token now returns `401` on a follow-up authenticated call. Login attempts return `401 invalid credentials`.
2. **Happy path unarchive.** Archive then unarchive → user can log in with the original password and receives a *new* token. Old token stays revoked.
3. **Self-archive forbidden.** Admin tries to archive own ID → `400 cannot archive yourself`.
4. **Last-admin guard.** Two admins A and B; archive B; archive A → `400 cannot archive the last active admin`.
5. **Last-admin guard does not fire for non-admin archive.** One admin, one regular user; archive the regular user → `200` regardless of admin count.
6. **Last-admin guard ignores already-archived admins.** Admins A and B; archive B; archive A → `400`. Unarchive B; archive A → `200`.
7. **Already-archived → 409.** Archive twice → second call `409 user is already archived`.
8. **Not-archived → 409.** Unarchive an active user → `409 user is not archived`.
9. **Not found → 404.** Archive/unarchive a random UUID → `404`.
10. **Login message parity.** Three login attempts — wrong password on active user, correct password on archived user, wrong password on unknown email — all return identical `401` body content. Assert message equality. (Timing parity is enforced by the dummy-bcrypt mechanism, not asserted directly.)
11. **List filtering.**
    - `GET /v1/users` returns active only.
    - `GET /v1/users?include_archived=true` returns active + archived.
    - `GET /v1/users?email=<archived>` returns `[]`.
    - `GET /v1/users?email=<archived>&include_archived=true` returns the archived user.
12. **API token cascade.** After archive, direct SQL `SELECT COUNT(*) FROM api_tokens WHERE user_id = $1` returns 0.
13. **Scheduled jobs untouched.** Create a scheduled job owned by the user; archive the user; assert `scheduled_jobs.enabled` is unchanged and the row still exists.
14. **Email lock.** Archive `bob@example.com`; attempt to register or `admin users create` with that email → `409`. Confirms the email-stays-locked decision.

### Manual verification

- `make test` passes.
- `make test-integration` passes.
- `make build` produces all three binaries.
- Smoke: spin up `relay-server`, log in as admin, archive a test user via CLI, verify they cannot log in; unarchive, verify they can.

## Files touched

**New files**

- `internal/store/migrations/000010_user_archive.up.sql`
- `internal/store/migrations/000010_user_archive.down.sql`

**Modified — generated (do not hand-edit, run `make generate`)**

- `internal/store/users.sql.go`
- `internal/store/models.go`

**Modified — hand-edited**

- `internal/store/query/users.sql` — new queries; `ListUsers` archive filter; new `ListUsersIncludingArchived`.
- `internal/api/users.go` — `handleAdminArchiveUser`, `handleAdminUnarchiveUser`, `handleListUsers` `?include_archived` branching, `userResponse.ArchivedAt`.
- `internal/api/auth.go` — `handleLogin` archive-check after bcrypt compare.
- `internal/api/server.go` — register the two new routes near the existing `/v1/users` mux entries (line 120 area).
- `internal/cli/admin_users.go` — `archive`/`unarchive` subcommands; `--include-archived` flag on `list`; `userListItem.ArchivedAt`; `printUserDetail` archive line.
- `internal/api/users_test.go`, `internal/cli/admin_users_test.go` — unit tests.
- `internal/api/users_integration_test.go` — integration tests for archive/unarchive flows and list filtering.
- `internal/api/auth_integration_test.go` — login parity test.
- `README.md` — CLI reference and REST endpoint table updates.

**Deliberately not touched**

- `internal/api/agent_enrollments.go` — agents authenticate with their own tokens; archived users with active enrollments don't disrupt agent operation.
- `internal/scheduler/`, `internal/schedrunner/` — scheduled jobs owned by archived users continue firing.
- `internal/worker/`, `internal/agent/` — agent-side untouched.
- `CLAUDE.md` — no architecture change.

## Out of scope (future work)

- **Hard delete escape hatch** for users with no job/enrollment history. Could be added as `DELETE /v1/users/{id}` later without breaking the archive surface.
- **Email reuse on archive.** If users ever need to recycle an archived email (e.g., a rehire or a shared address), add an `archived_email` column and rewrite `email` to a placeholder on archive. Pure additive change; no existing code breaks.
- **Bulk archive / archive by query.** Out of scope; admins archive one user at a time via the new endpoint.
- **Audit log of archive actions.** No audit table exists yet; if one is added in the future, archive/unarchive should write to it.
