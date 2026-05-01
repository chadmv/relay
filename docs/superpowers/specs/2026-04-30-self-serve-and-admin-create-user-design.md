# Self-Serve Registration & Admin-Create User — Design

**Date:** 2026-04-30
**Status:** Draft
**Closes:** [docs/backlog/bug-2026-04-25-relay-register-requires-invite-token.md](../../backlog/bug-2026-04-25-relay-register-requires-invite-token.md)

## Summary

Two adjacent gaps in Relay's user-onboarding surface are addressed together:

1. **Gap A — self-serve registration.** `POST /v1/auth/register` currently requires an invite token minted by an admin. A new operator-controlled flag `RELAY_ALLOW_SELF_REGISTER` makes the invite token optional; when the flag is on and no invite is supplied, the server creates a non-admin user directly.
2. **Gap B — admin-create user.** No admin-facing endpoint exists today to provision a user in one call (admins can only mint invites and wait for the recipient to redeem them, and admins cannot create another admin without writing SQL). A new admin-only `POST /v1/users` accepts `{email, name?, password, is_admin?}` and creates the account directly.

The two changes share request validation, response shapes, and CLI patterns, so they are bundled into one spec.

## Motivation

- The bootstrap admin can mint invites for others, but to onboard themselves to a fresh Relay deployment they must set `RELAY_BOOTSTRAP_ADMIN` + `RELAY_BOOTSTRAP_PASSWORD` and restart the server. There is no path for a deployment that wants users to onboard without an admin in the loop.
- Admins cannot promote a user to admin without direct database access. The bootstrap env vars only fire when the database is empty, so they cannot be used to add a second admin.
- Integration tests bypass the API and call `store.CreateUserWithPassword` directly because the API has no path to mint an admin (see comment at [internal/api/password_reset_integration_test.go:22-23](../../../internal/api/password_reset_integration_test.go)). A real `POST /v1/users` lets tests use the same surface as production.

## Out of Scope

- **Email verification.** Relay is an internal render-farm coordinator, not a public SaaS. Adding verification means an SMTP dependency, a verification-token table, and a redemption flow — all premature.
- **Self-serve admin promotion.** Only `POST /v1/users` (admin-only) can mint admins. Self-serve registration always produces a non-admin user, matching today's invite-redemption behavior.
- **Email-domain allowlist.** A `RELAY_SELF_REGISTER_DOMAINS=acme.com,acme.co.uk` style allowlist was discussed and rejected for v1. It is a clean follow-up on top of the env-var flag if domain-bound onboarding becomes a real ask.
- **Hot-reload of `RELAY_ALLOW_SELF_REGISTER`.** The flag is read once at server boot. Toggling it requires a restart, matching the precedent set by `RELAY_CORS_ORIGINS`, `RELAY_LOGIN_RATE_LIMIT`, and other operator config.
- **Email-enumeration hardening.** `POST /v1/auth/register` already returns `409 email already registered` for duplicate emails — that is a pre-existing enumeration vector. Self-serve does not introduce it; this spec does not fix it either.

## Architecture

### Server config

A new field on `api.Server`:

```go
type Server struct {
    // ...existing fields...
    AllowSelfRegister bool
}
```

`cmd/relay-server/main.go` parses `os.Getenv("RELAY_ALLOW_SELF_REGISTER")` with `strconv.ParseBool`. Empty string → `false`. Any value `ParseBool` accepts (`1`, `true`, `TRUE`, `t`, etc.) → `true`. Any other value is a fatal startup error with a clear message — the same fail-fast pattern used by `ParseCORSOrigins` and `ParseRateLimit`. The parsed bool is assigned into `Server.AllowSelfRegister` before the HTTP server starts.

The CLAUDE.md env-var table gains one row.

### `POST /v1/auth/register` — modified

The existing `handleRegister` in [internal/api/auth.go](../../../internal/api/auth.go) gains one new branch. Pseudocode:

```
if req.InviteToken == "" {
    if !s.AllowSelfRegister {
        writeError(w, 400, "invite_token is required")
        return
    }
    // self-serve path:
    //   - bcrypt password
    //   - tx: CreateUserWithPassword (is_admin=false), issue session token
    //   - return {token, expires_at}
    return
}
// existing invite-validation path: unchanged
```

The error message `invite_token is required` is preserved verbatim so older CLIs see the same response when the operator has not enabled self-serve.

The self-serve branch reuses:
- `bcryptCost` package var (test-overrideable).
- `CreateUserWithPassword` store query (with `IsAdmin: false`, `Name` defaulted to email when empty — matching the invite branch's existing behavior).
- `issueToken` helper for the 30-day session token.
- The `pgconn.PgError` `23505` → `409` duplicate-email handling, factored out so both branches share it.

The existing `RELAY_REGISTER_RATE_LIMIT` per-IP limiter wraps the route in `server.go` and continues to apply — self-serve inherits rate limiting at no extra design cost.

### `POST /v1/users` — new

Admin-only endpoint registered in `server.go`:

```go
mux.Handle("POST /v1/users", auth(admin(http.HandlerFunc(s.handleAdminCreateUser))))
```

Request body:

```json
{
  "email":    "ops@acme.com",
  "name":     "Ops Bot",
  "password": "...",
  "is_admin": false
}
```

Behavior:

- `email` required; validated via `mail.ParseAddress` (matches `handleRegister`).
- `password` required; validated `len >= 8` (matches `handleRegister`).
- `name` optional; trimmed; defaulted to `email` when empty (matches `handleRegister`).
- `is_admin` optional; defaults to `false`.
- Bcrypt the password, then call `CreateUserWithPassword` with the supplied `is_admin`. No transaction needed — it is a single INSERT. **No session token is issued.** The created user logs in separately via `POST /v1/auth/login`.
- Returns `201 Created` with the existing `userResponse` shape (`{id, email, name, is_admin, created_at}`) produced by the `toUserResponse` helper from `users.go`.

Error responses:

| Condition | Status | Body |
|---|---|---|
| Body fails to parse | 400 | `invalid request body` |
| Missing/invalid email | 400 | `email is required` / `invalid email address` |
| Password < 8 chars | 400 | `password must be at least 8 characters` |
| Duplicate email (`23505`) | 409 | `email already registered` |
| Caller not authenticated | 401 | (from `BearerAuth` middleware) |
| Caller not admin | 403 | (from `AdminOnly` middleware) |
| Other store error | 500 | `failed to create user` |

The handler lives in `internal/api/users.go` next to the existing user-management handlers.

## CLI Changes

### `relay register` — modified

In [internal/cli/register.go](../../../internal/cli/register.go), the invite-token prompt becomes optional with a clarifying hint:

```
Invite token (leave blank if your server allows self-serve registration):
```

If blank, the request is sent with `invite_token: ""` (the existing `registerRequest` struct lacks `omitempty` on that field; either add `omitempty` or leave as-is — server treats `""` and absent identically since it checks `req.InviteToken == ""`). If the server has `RELAY_ALLOW_SELF_REGISTER=false`, the response is `400 invite_token is required` and the CLI surfaces that error verbatim. The CLI does **not** probe the server for capability — try-and-fail is acceptable because nothing is destructive.

All other behavior (config save, success message, exit code) is unchanged.

### `relay admin users create` — new

A new subcommand mirroring `relay admin users update`. Lives as a new `doAdminUsersCreate` function in [internal/cli/admin_users.go](../../../internal/cli/admin_users.go). Usage:

```
relay admin users create --email <email> [--name <name>] [--admin]
```

Behavior:

- `--email` required; basic client-side validation; server is authoritative.
- `--name` optional; passed only when non-empty so the server applies its email-as-name default.
- `--admin` boolean flag; sends `is_admin: true` when present.
- **Password is read interactively** via `readPasswordFn` twice (matches `relay register`). Never accepted on the command line — keeps it out of shell history, `ps` output, and CI logs.
- POSTs to `/v1/users` with the configured bearer token.
- On success, prints the created user via the existing `printUserDetail` helper (extracted in commit [04fdae8](https://github.com/)).

Help-text registration in `internal/cli/cli.go` (or wherever the `admin users` subtree is wired) gains one entry.

## Validation & Error Handling

Shared by both endpoints:

- Request bodies parsed via `readJSON`; malformed JSON → `400 invalid request body`.
- Email validation via `mail.ParseAddress`.
- Password `len >= 8`.
- Duplicate-email detection via `pgconn.PgError.Code == "23505"`.
- Bcrypt cost `12` in production; integration tests override via `SetBcryptCostForTest`.

The existing `parseUpdateUserRequest` helper handles request parsing for the PATCH endpoints. The new `handleAdminCreateUser` does not reuse it (its body shape is different — it includes `password` and `is_admin`); a new local helper or inline parsing is fine. No new package-level abstractions are introduced.

## Testing

### Server integration tests (build tag `integration`)

In `internal/api/auth_integration_test.go`:

- `TestRegister_SelfServe_HappyPath` — flag on, no `invite_token` → 201; user row exists with `is_admin=false`.
- `TestRegister_SelfServe_DisabledByDefault` — flag unset, no `invite_token` → 400 with the existing message (regression guard).
- `TestRegister_SelfServe_FlagOnInviteStillWorks` — flag on, valid `invite_token` → existing path runs (invite consumed, email binding enforced).
- `TestRegister_SelfServe_DuplicateEmail` — flag on, email exists → 409.
- `TestRegister_SelfServe_RateLimited` — flag on, exhaust the per-IP limiter → 429.
- `TestRegister_SelfServe_AlwaysNonAdmin` — confirms `is_admin=false` is hardcoded on the self-serve path even if a malicious client sends an `is_admin` field.

In `internal/api/users_integration_test.go`:

- `TestAdminCreateUser_HappyPath` — admin token, all fields, `is_admin=false` → 201 + correct `userResponse`; the new user can subsequently log in.
- `TestAdminCreateUser_CreatesAdmin` — `is_admin=true` → 201; the created user can hit an admin-only route.
- `TestAdminCreateUser_NonAdminForbidden` — non-admin token → 403.
- `TestAdminCreateUser_Unauthenticated` — no token → 401.
- `TestAdminCreateUser_DuplicateEmail` → 409.
- `TestAdminCreateUser_InvalidEmail` → 400.
- `TestAdminCreateUser_WeakPassword` → 400.
- `TestAdminCreateUser_MissingPassword` → 400.
- `TestAdminCreateUser_NameDefaultsToEmail` → response `name` equals email when omitted.

Both suites use the existing `SetBcryptCostForTest` hook for speed.

### CLI unit tests (no Postgres)

In `internal/cli/register_test.go`:

- Extend an existing happy-path test (or add `TestRegister_NoInviteToken`) — empty invite-token prompt input → request body omits `invite_token`.

In `internal/cli/admin_users_test.go`:

- `TestAdminUsersCreate_HappyPath` — flags + interactive password prompts → POST to `/v1/users` with correct body; success message printed.
- `TestAdminUsersCreate_Admin` — `--admin` flag → request body `is_admin: true`.
- `TestAdminUsersCreate_MissingEmail` → exits with usage error before any HTTP call.
- `TestAdminUsersCreate_PasswordMismatch` → exits with error before any HTTP call.
- `TestAdminUsersCreate_ServerError` → server returns 409 → CLI surfaces the error.

Use `httptest.Server` and the `readPasswordFn` swap to inject test passwords.

### Boot-time parsing

A small unit test next to `cmd/relay-server/main.go` (or in a dedicated parser file in `internal/api`) covers the `RELAY_ALLOW_SELF_REGISTER` parse helper: empty → false, `true`/`1` → true, garbage → fatal.

## Documentation

- **CLAUDE.md** — add `RELAY_ALLOW_SELF_REGISTER` to the relay-server env-var table; update the relay-CLI section to mention `relay admin users create` and the now-optional invite flow on `relay register`; extend the API overview's mention of `users.go` and `auth.go` to cover the new endpoint and branch.
- **No new top-level docs file.** The spec itself plus CLAUDE.md updates are sufficient.

## Migration

None. No schema changes. Net additions: one env var, one route, one CLI subcommand, one modified handler branch.

## Backlog Cleanup

`git mv docs/backlog/bug-2026-04-25-relay-register-requires-invite-token.md docs/backlog/closed/` as part of the same merge.

## Approval

- [x] Architecture & scope
- [x] API endpoints
- [x] CLI changes
- [x] Testing strategy
