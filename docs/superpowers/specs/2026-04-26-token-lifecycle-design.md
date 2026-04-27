# Token Lifecycle Management — Design

**Date:** 2026-04-26
**Source:** 2026-04-18 password-auth retro (Known Limitations + Open Questions); backlog items `bug-2026-04-25-relay-passwd-no-admin-reset`, `idea-2026-04-25-logout-endpoint`, `idea-2026-04-25-password-change-invalidates-tokens`

## Goal

Close three coordinated gaps in the token/auth lifecycle:

1. Admins cannot reset another user's password.
2. Users cannot revoke their own tokens (no logout).
3. Changing a password does not revoke other live tokens.

These ship together because they share the same DB primitives (delete operations on `api_tokens`) and the admin reset is conceptually a superset of "revoke all tokens for a user."

## Non-Goals

- Audit logging. No audit infrastructure exists today; bolting on partial coverage for these four endpoints would create an inconsistent feature. Defer to a holistic audit-log design.
- Admin user listing/lookup endpoint (`GET /v1/users`). Tracked separately as `idea-2026-04-26-admin-user-list-endpoint`. The admin reset endpoint accepts `email` directly in the request body to avoid taking on this dependency.
- Password-change UX changes (e.g., complexity requirements, history). Out of scope.
- Forced "must change password on next login" flag. Considered for the admin reset flow; rejected because it expands schema and login-handler scope.

## Schema

No table changes. Add three new sqlc queries on `api_tokens` in `internal/store/query/tokens.sql`:

```sql
-- name: DeleteToken :exec
DELETE FROM api_tokens WHERE id = $1;

-- name: DeleteTokensForUser :exec
DELETE FROM api_tokens WHERE user_id = $1;

-- name: DeleteOtherTokensForUser :exec
DELETE FROM api_tokens WHERE user_id = $1 AND id <> $2;
```

Run `make generate` after editing.

## Middleware Change

Extend `AuthUser` in `internal/api/context.go` with a `TokenID pgtype.UUID` field. `BearerAuth` (in `internal/api/middleware.go`) already loads the token row via `GetTokenWithUser`, which already aliases `t.id AS token_id` in `internal/store/query/tokens.sql`. The only change needed is to populate `AuthUser.TokenID` from `row.TokenID` in the middleware. No store query changes for this.

## HTTP Endpoints

| Method | Path | Auth | Behavior |
|---|---|---|---|
| `DELETE` | `/v1/auth/token` | user | `DeleteToken(ctx.TokenID)`. 204. Idempotent — succeeds even if token already gone. |
| `DELETE` | `/v1/auth/tokens` | user | `DeleteTokensForUser(ctx.UserID)`. 204. Idempotent. |
| `POST` | `/v1/users/password-reset` | admin | Body: `{"email": "...", "new_password": "..."}`. Looks up user by email, hashes (bcrypt cost from package var), `SetPasswordHash`, then `DeleteTokensForUser(targetID)`. 204. Returns 404 if email not found, 400 if `new_password` < 8 chars or body malformed. |
| `PUT` | `/v1/users/me/password` | user | **Existing handler, modified.** After `SetPasswordHash` succeeds, also call `DeleteOtherTokensForUser(ctx.UserID, ctx.TokenID)`. Still returns 204. |

All routes are wired in `internal/api/server.go`; admin route uses `admin(auth(...))` chain matching existing patterns (e.g., `DELETE /v1/workers/{id}/token`).

## CLI Surface

```
relay logout                    → DELETE /v1/auth/token              (current token only)
relay logout --all              → DELETE /v1/auth/tokens             (all tokens for self)
relay admin passwd <email>      → POST   /v1/users/password-reset
                                  prompts twice for new password via x/term
relay passwd                    → unchanged path/UX; server-side now revokes other tokens
```

`relay logout` (with or without `--all`) clears the saved token from `~/.relay/config.json` (Linux/Mac) or `%APPDATA%\relay\config.json` (Windows) on success. Otherwise the user is left with a dead token in their config that produces 401s on the next command.

`relay admin` is a new subcommand group (matches the `relay schedules` / `relay workers` style). For now `admin passwd` is the only entry; future admin commands can join it under the same group.

## Error Handling

- 401/403 — handled by existing `BearerAuth` and `AdminOnly` middleware. No changes.
- Admin reset:
  - 404 if email not found.
  - 400 if `new_password` < 8 chars or body malformed.
  - 500 on bcrypt or DB error.
- Self password change:
  - 403 if current password wrong (existing).
  - 400 if new password < 8 chars (existing).
  - The new revocation step runs after `SetPasswordHash` succeeds. If `DeleteOtherTokensForUser` fails, the password change has already committed — log the error server-side and return 500. The user can recover by calling `logout --all`. We accept the minor inconsistency rather than wrapping the password update + revocation in a transaction (the operations are independent and the failure mode is benign).
- Logout endpoints: idempotent, always 204.

## Testing

TDD per repo convention. New/extended tests:

- **Unit tests** (`internal/api/`):
  - `auth_test.go` — extend `handleChangePassword` tests: assert other tokens are revoked, assert current token survives.
  - `logout_test.go` (new) — `DELETE /v1/auth/token` and `DELETE /v1/auth/tokens` happy paths, idempotency, missing auth.
  - `password_reset_test.go` (new) — admin reset happy path, 404 unknown email, 400 short password, 403 non-admin caller, assert all target's tokens are revoked.
  - `middleware_test.go` — assert `AuthUser.TokenID` is populated.
- **Integration tests** (`internal/api/auth_integration_test.go` or new file under `//go:build integration`):
  - Full lifecycle: register → login → call protected endpoint → change password → assert other tokens revoked, current token still works.
  - Admin reset lifecycle: admin token + target user's token → admin resets → assert target's token is now 401, admin's token still works.
- **CLI tests** (`internal/cli/`):
  - `logout_test.go` (new) — stub `readPasswordFn` (not needed for logout) and `saveConfigFn`; assert config is cleared on success.
  - `admin_passwd_test.go` (new) — stub `readPasswordFn`; httptest server returns 204; assert correct request body.

## Backlog Housekeeping (Required)

When the work lands, move all three closed backlog items to `docs/backlog/closed/` in the same PR as the implementation:

- `docs/backlog/bug-2026-04-25-relay-passwd-no-admin-reset.md`
- `docs/backlog/idea-2026-04-25-logout-endpoint.md`
- `docs/backlog/idea-2026-04-25-password-change-invalidates-tokens.md`

Each moved file gets `status: closed`, `closed: <date>`, `resolution: fixed` in frontmatter, plus a `## Resolution` section noting the implementing commit. Use `git mv` (not `Write` + `git rm`) per the 2026-04-26 multi-command-tech-debt retro lesson.

## Open Questions

None remaining; all four design questions resolved during brainstorm.

## Files Likely Touched

- `internal/store/query/tokens.sql` — three new queries; one alias change on `GetTokenWithUser`
- `internal/store/queries.sql.go`, `internal/store/models.go` — regenerated
- `internal/api/context.go` — `AuthUser.TokenID` field
- `internal/api/middleware.go` — populate `TokenID`
- `internal/api/auth.go` — modify `handleChangePassword`; new `handleLogoutCurrent`, `handleLogoutAll`, `handleAdminPasswordReset`
- `internal/api/server.go` — wire four new routes
- `internal/api/auth_test.go`, `internal/api/logout_test.go`, `internal/api/password_reset_test.go`, `internal/api/middleware_test.go`
- `internal/api/auth_integration_test.go` (or split)
- `internal/cli/logout.go`, `internal/cli/logout_test.go`
- `internal/cli/admin_passwd.go`, `internal/cli/admin_passwd_test.go`
- `cmd/relay/main.go` — register `logout` and `admin` commands in the `[]cli.Command` slice passed to `cli.Dispatch`
- `internal/cli/passwd.go` — no behavior change; possibly note in comment that server now revokes other tokens
- `docs/backlog/bug-2026-04-25-relay-passwd-no-admin-reset.md` → `closed/`
- `docs/backlog/idea-2026-04-25-logout-endpoint.md` → `closed/`
- `docs/backlog/idea-2026-04-25-password-change-invalidates-tokens.md` → `closed/`
