# User Profile Update Endpoint — Design

**Date:** 2026-04-29
**Backlog items:**
- [`idea-2026-04-27-user-profile-update-endpoint`](../../backlog/idea-2026-04-27-user-profile-update-endpoint.md)
- [`idea-2026-04-27-getuserbyemail-fetches-password-hash`](../../backlog/idea-2026-04-27-getuserbyemail-fetches-password-hash.md)

## Goal

Add `PATCH` endpoints so users can update their own display name and admins can update any user's display name. Bundle a small security-tightening cleanup in the same change: replace the `GetUserByEmail` call on the public email-filter path of `GET /v1/users` with a new `GetUserByEmailPublic` query that does not select `password_hash`.

## Non-goals

- **Email change.** The original backlog proposal mentioned `email` as a possible field. Email is the login identifier and a uniqueness constraint; changing it implies session revocation, collision handling, and possibly bootstrap-admin re-matching. Deferred to a fresh backlog entry to be opened when this lands.
- **Length cap on `name`.** `handleRegister` accepts arbitrary-length names with no cap. Adding a cap to `PATCH` only would be asymmetric. Deferred until we want to apply a cap to both paths together.
- **Audit log for admin updates.** No audit-log pattern exists in the codebase yet; introducing one is a separate design.
- **Self-service password change CLI.** A natural future addition under the new `profile` subcommand, but out of scope here.
- **Pagination, substring search** — not relevant; this PR doesn't add new list surface.

## API

### `PATCH /v1/users/me` (any authenticated user)

Routed through `auth(...)`. Updates the calling user's `name`.

**Request body:**

```json
{ "name": "Alice Anderson" }
```

**Response:** `200 OK`, single JSON object matching the existing `userResponse` shape:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "email": "alice@example.com",
  "name": "Alice Anderson",
  "is_admin": false,
  "created_at": "2026-04-01T12:00:00Z"
}
```

**Errors:**

- `400 "invalid request body"` — JSON parse failure.
- `400 "name is required"` — field absent or empty after `strings.TrimSpace`.
- `401` — missing/invalid bearer token (handled by `BearerAuth`).
- `500 "failed to update user"` — DB error. (`pgx.ErrNoRows` here would be a server-side bug — the bearer token already proved the user exists — and is treated as `500`.)

### `PATCH /v1/users/{id}` (admin-only)

Routed through `auth(admin(...))`. Updates any user's `name` by UUID.

Same request/response shape as `/v1/users/me`. Additional errors:

- `400 "invalid user id"` — `id` is not a valid UUID.
- `403` — non-admin caller (handled by `AdminOnly`).
- `404 "user not found"` — `id` is well-formed but no row matches.

### `GET /v1/users?email=...` (cleanup, no behavior change)

The email-filter branch swaps `GetUserByEmail` → `GetUserByEmailPublic`. Response shape, status codes, and ordering are unchanged. The change tightens the security boundary by ensuring `password_hash` is never read from Postgres on this code path.

## Validation rules

- `name` is required.
- After `strings.TrimSpace`, must be non-empty. The trimmed value is what's persisted.
- No length cap (matches `handleRegister`).

## Routing

Added in `internal/api/server.go` near the existing `/v1/users` entries:

```go
mux.Handle("PATCH /v1/users/me",   auth(http.HandlerFunc(s.handleUpdateMe)))
mux.Handle("PATCH /v1/users/{id}", auth(admin(http.HandlerFunc(s.handleAdminUpdateUser))))
```

Go 1.22+ pattern matching gives literal segments precedence over wildcards, so `/v1/users/me` is matched before `/v1/users/{id}` automatically. No manual ordering required.

## Store layer

Two new sqlc queries appended to `internal/store/query/users.sql`:

```sql
-- name: GetUserByEmailPublic :one
SELECT id, email, name, is_admin, created_at
FROM users WHERE email = $1;

-- name: UpdateUserName :one
UPDATE users SET name = $2 WHERE id = $1
RETURNING id, email, name, is_admin, created_at;
```

Notes:

- Both return only the five public columns. Generated `GetUserByEmailPublicRow` and `UpdateUserNameRow` types will not contain `PasswordHash`.
- `UpdateUserName` is `:one` (not `:exec`) so the admin handler can detect "no row updated" via `pgx.ErrNoRows` and return `404`.
- `RETURNING` lets handlers respond with the new state in one round trip.
- The existing `GetUserByEmail` query stays — `handleLogin` still needs `password_hash`. No other callers of `GetUserByEmail` exist after this change.
- No new index or migration. `users.email` is uniquely indexed by migration `0001`; `users.id` is the primary key.

## Handlers

All handler code lives in `internal/api/users.go`.

**Shared request type and validator:**

```go
type updateUserRequest struct {
    Name string `json:"name"`
}

// parseUpdateUserRequest reads JSON, trims whitespace, and validates name.
// Returns the trimmed name and writes the appropriate error response on failure.
func parseUpdateUserRequest(w http.ResponseWriter, r *http.Request) (string, bool) { ... }
```

**Shared mapping helper** (also extracts the dedup in `handleListUsers`):

```go
func toUserResponse(id pgtype.UUID, email, name string, isAdmin bool, createdAt pgtype.Timestamptz) userResponse { ... }
```

`handleListUsers` is updated to call `toUserResponse` for both branches.

**`handleUpdateMe`:**

1. Pull `AuthUser` from request context.
2. Call `parseUpdateUserRequest`.
3. `s.q.UpdateUserName(ctx, UpdateUserNameParams{ID: authUser.ID, Name: name})`.
4. On success, write `200` + `toUserResponse(...)`.
5. Any DB error → `500`.

**`handleAdminUpdateUser`:**

1. `id := r.PathValue("id")`; parse as UUID; `400` on failure.
2. Call `parseUpdateUserRequest`.
3. `s.q.UpdateUserName(ctx, UpdateUserNameParams{ID: parsedID, Name: name})`.
4. `pgx.ErrNoRows` → `404 "user not found"`. Other errors → `500`. Success → `200` + `toUserResponse(...)`.

No transaction is needed in either handler — single `UPDATE`, no ancillary writes (unlike password change which also revokes sessions).

## CLI

### New file: `internal/cli/profile.go`

```
relay profile update --name "<name>"
```

- Registered in `cli.Dispatch()`: `"profile"` → `doProfile` (mirrors how `admin` is wired).
- `doProfile` switches on subcommand. For now only `update` is implemented; structure leaves room for `show` and `change-password` later.
- `doProfileUpdate`:
  - Parses `--name` via `flag.NewFlagSet("profile update", flag.ContinueOnError)`.
  - Errors if `--name` is missing or empty after trim (before any HTTP call).
  - `cfg.NewClient().do("PATCH", "/v1/users/me", body)`.
  - Decodes the response into the existing `userListItem` struct (declared in `admin_users.go`, same package — direct reference, no move needed).
  - Prints labeled key-value output via the existing `printUserDetail` helper.

### Edit: `internal/cli/admin_users.go`

Add `update` case to the existing `doAdminUsers` switch:

```
relay admin users update <email-or-id> --name "<name>"
```

- `doAdminUsersUpdate`:
  - Positional argument is either a UUID or an email.
  - If `uuid.Parse(arg)` succeeds → use directly.
  - Otherwise → `GET /v1/users?email=<arg>` to resolve to UUID. Empty array → friendly "user not found" error, no PATCH issued.
  - `PATCH /v1/users/{id}` with `{"name": "..."}`.
  - Decode and print using the same helpers as the self path.
- `--name` validation mirrors `profile update`.

### Help text

Updated `--help` listings for both `profile` and `admin users` to include the new commands.

## Testing strategy

### Integration tests (`internal/api/users_integration_test.go`)

Self path (`PATCH /v1/users/me`):

1. Happy path — non-admin user updates own name; response body matches new state; `GET /v1/users?email=` confirms persistence.
2. Empty `name` → `400`.
3. Whitespace-only `name` → `400`.
4. Surrounding whitespace is trimmed before persisting.
5. Missing `name` field → `400`.
6. Invalid JSON body → `400`.
7. No bearer token → `401` (smoke check at middleware level).

Admin path (`PATCH /v1/users/{id}`):

8. Admin updates another user's name → `200`, body matches.
9. Non-admin attempting `/v1/users/{id}` → `403`.
10. Admin with non-existent UUID → `404`.
11. Admin with malformed UUID → `400`.
12. Admin updating their own id via the admin path → `200` (no special case).

Cleanup:

13. `GET /v1/users?email=foo@bar` returns the same shape after the `GetUserByEmailPublic` swap (regression check on the existing endpoint's happy path and miss path).

### CLI unit tests

`profile update` (`internal/cli/profile_test.go`):

- Sends `PATCH /v1/users/me` with the right body and bearer header.
- Empty `--name` → error before HTTP call.
- Server `400` is surfaced cleanly to stderr.
- Prints the returned user in labeled format.

`admin users update` (additions to `internal/cli/admin_users_test.go`):

- UUID-shaped positional → goes straight to `PATCH /v1/users/{id}`.
- Email-shaped positional → first `GET /v1/users?email=…`, then `PATCH /v1/users/{id}`.
- Email with no match → error, no PATCH issued.
- Server `403` (non-admin context) → surfaced cleanly.

### Store-layer tests

sqlc-generated queries are exercised through the integration tests — no separate unit tests.

## Files touched (expected)

- `internal/store/query/users.sql` — two new queries.
- `internal/store/users.sql.go` — regenerated (only stage this file from the regen output; restore others if `make generate` rewrites unrelated files with LF endings).
- `internal/api/users.go` — `updateUserRequest`, `parseUpdateUserRequest`, `toUserResponse`, `handleUpdateMe`, `handleAdminUpdateUser`; cleanup swap in `handleListUsers`.
- `internal/api/users_integration_test.go` — new tests per the matrix above.
- `internal/api/server.go` — two new routes.
- `internal/cli/profile.go` — new file.
- `internal/cli/profile_test.go` — new file.
- `internal/cli/admin_users.go` — new `update` case + helper.
- `internal/cli/admin_users_test.go` — additions.
- `internal/cli/cli.go` (or wherever `Dispatch` lives) — register `"profile"`.
- `CLAUDE.md` — add the new endpoints and CLI commands to the relevant sections.
- `docs/backlog/closed/` — move both backlog items here as part of the change (per the backlog-housekeeping rule).
