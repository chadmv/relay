# Admin User List Endpoint — Design

**Date:** 2026-04-27
**Backlog item:** [`idea-2026-04-26-admin-user-list-endpoint`](../../backlog/idea-2026-04-26-admin-user-list-endpoint.md)

## Goal

Add an admin-only `GET /v1/users` endpoint and matching CLI subcommands so admins can enumerate accounts and resolve email→UUID without embedding lookups in every operation. Closes the gap surfaced in the 2026-04-26 token-lifecycle brainstorm.

## Non-goals

- Pagination (deferred until user count makes it necessary).
- Substring/fuzzy search. Exact-match `?email=` only; revisit if it becomes painful.
- A user profile update endpoint. The `users.name` column is currently write-once-at-registration; surfacing the existing field here does not commit us to providing an update path.

## API

### `GET /v1/users` (admin-only)

Routed through `auth(admin(...))` like other admin endpoints (see `internal/api/server.go`).

**Query params:**

- `email` (optional): exact-match filter. Case-sensitive (matches existing `GetUserByEmail` semantics).

**Response:** `200 OK`, JSON array. Each element:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "email": "alice@example.com",
  "name": "Alice",
  "is_admin": true,
  "created_at": "2026-04-01T12:00:00Z"
}
```

- Always returns an array, even when `?email=` matches nothing (`[]`).
- Never includes `password_hash`.
- Order: `ORDER BY created_at ASC` — bootstrap admin appears first, stable for snapshots.

**Errors:**

- `403 Forbidden` — caller is not admin (handled by `AdminOnly` middleware).
- `500 Internal Server Error` — store failure.

## Store layer

New sqlc query in `internal/store/query/users.sql`:

```sql
-- name: ListUsers :many
SELECT id, email, name, is_admin, created_at
FROM users
ORDER BY created_at;
```

Selecting columns explicitly (not `SELECT *`) keeps `password_hash` out of the generated row type, eliminating any risk of leaking it through the handler.

The email-filter case reuses the existing `GetUserByEmail` query — handler branches to it when `?email=` is present, treats `pgx.ErrNoRows` as empty result, returns `[]`.

After editing the SQL, `make generate` regenerates `internal/store/users.sql.go` and `internal/store/models.go`.

## Handler

New file `internal/api/users.go`:

- `handleListUsers(w http.ResponseWriter, r *http.Request)`
  - If `r.URL.Query().Get("email")` is non-empty: call `s.q.GetUserByEmail`. On `errors.Is(err, pgx.ErrNoRows)` → return `[]`. On other error → `500`. On success → return single-element array.
  - Otherwise: call `s.q.ListUsers` → marshal each row into the public response shape.
  - Both paths use a small private `userResponse` struct to ensure the field set is identical regardless of the path taken.

Registered in `internal/api/server.go` next to the other admin user-management routes:

```go
mux.Handle("GET /v1/users", auth(admin(http.HandlerFunc(s.handleListUsers))))
```

## CLI

New file `internal/cli/admin_users.go` with two subcommands wired through the existing `doAdmin` switch in `internal/cli/admin.go`:

- `relay admin users list` — `GET /v1/users`, prints a table:
  ```
  ID                                    EMAIL                  NAME           ADMIN   CREATED
  550e8400-...                          alice@example.com      Alice          yes     2026-04-01
  ...
  ```
- `relay admin users get <email>` — `GET /v1/users?email=<email>`. If empty → exits with `user not found: <email>`. Otherwise prints labeled fields:
  ```
  ID:         550e8400-...
  Email:      alice@example.com
  Name:       Alice
  Admin:      yes
  Created:    2026-04-01T12:00:00Z
  ```

Both subcommands require `cfg.Token` (same "not logged in" check as `admin passwd`).

The `doAdmin` dispatcher gains a `users` case that dispatches further to `list`/`get`. Usage strings are updated to `admin <passwd|users> [args]`.

## Testing

### Integration test (`internal/api/users_integration_test.go`, build tag `integration`)

Following the patterns in `password_reset_integration_test.go` and `agent_enrollments_test.go`:

- `TestListUsers_AdminSeesAll` — admin caller, two non-admin users seeded; asserts response contains all three with correct fields, no `password_hash` key.
- `TestListUsers_NonAdminForbidden` — non-admin caller → `403`.
- `TestListUsers_FilterByEmailHit` — `?email=alice@example.com` returns single-element array with the expected user.
- `TestListUsers_FilterByEmailMiss` — `?email=nobody@example.com` returns `200` with `[]`.
- `TestListUsers_OrderedByCreatedAt` — three users created with controlled timestamps; response order matches insertion order.

### CLI unit tests (`internal/cli/admin_users_test.go`)

- Dispatcher test: `relay admin users list` and `users get foo@bar` reach the right handler functions (existing `admin_test.go` patterns use a stubbed HTTP roundtripper).
- Output formatting: list with multiple rows produces the expected table; `get` with empty response produces `user not found: <email>` error.
- "Not logged in" guard: empty token returns the same error message as `admin passwd`.

## Backlog housekeeping

Per the 2026-04-26 retro lesson, the implementation plan must include:

- `git mv docs/backlog/idea-2026-04-26-admin-user-list-endpoint.md docs/backlog/closed/`
- Add `status: closed` and `closed: 2026-04-27` to the frontmatter, plus a `## Resolution` section pointing to the merge commit.

## Files touched

| File | Action |
|---|---|
| `internal/store/query/users.sql` | Add `ListUsers` query |
| `internal/store/users.sql.go` | Regenerated via `make generate` |
| `internal/store/models.go` | Regenerated (no schema change expected, just verify) |
| `internal/api/users.go` | New: `handleListUsers` |
| `internal/api/server.go` | Register `GET /v1/users` route |
| `internal/api/users_integration_test.go` | New: integration tests |
| `internal/cli/admin.go` | Extend `doAdmin` switch with `users` case |
| `internal/cli/admin_users.go` | New: list/get subcommand handlers |
| `internal/cli/admin_users_test.go` | New: CLI tests |
| `docs/backlog/idea-2026-04-26-admin-user-list-endpoint.md` | `git mv` to `closed/` with Resolution |
| `CLAUDE.md` | Update API section to mention `GET /v1/users` route, `relay admin users` subcommand |

## Open questions

None.
