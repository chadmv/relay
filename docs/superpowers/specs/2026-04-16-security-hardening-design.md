# Security Hardening Design

**Date:** 2026-04-16  
**Status:** Approved

## Problem

The current auth system has two gaps:

1. `POST /v1/auth/token` is open — anyone can create an account by supplying an email address with no verification or gating.
2. There is no way to create or promote an admin user through the system; it requires direct database manipulation.

## Goals

- Gate new user registration behind admin-issued one-time invite tokens.
- Provide a bootstrap mechanism to create the first admin without DB access.
- Preserve the existing token-based auth model (no passwords).
- Existing users re-login without any disruption.

## Out of Scope

- Admin user management (list users, revoke tokens, promote users via CLI endpoint).
- Email delivery of invites (admin delivers tokens out-of-band).
- Per-user token management (multiple named tokens, revocation).

---

## Design

### 1. Database — new `invites` table

New migration adds one table:

```sql
CREATE TABLE invites (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash  TEXT        NOT NULL UNIQUE,
    email       TEXT,                     -- optional: lock invite to a specific address
    created_by  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL,     -- always required, default 72h
    used_at     TIMESTAMPTZ,              -- NULL = not yet claimed
    used_by     UUID        REFERENCES users(id) ON DELETE SET NULL
);
```

Three new sqlc queries for invites:

- `CreateInvite(ctx, params) (Invite, error)`
- `GetInviteByTokenHash(ctx, tokenHash string) (Invite, error)`
- `MarkInviteUsed(ctx, params) error` — conditional `UPDATE ... WHERE used_at IS NULL`, returns error if already used (prevents double-redemption)

Two new sqlc queries for bootstrap:

- `AdminExists(ctx) (bool, error)` — `SELECT EXISTS(SELECT 1 FROM users WHERE is_admin = TRUE)`
- `PromoteUserToAdmin(ctx, id pgtype.UUID) error` — `UPDATE users SET is_admin = TRUE WHERE id = $1`

Token format: 32 random bytes, hex-encoded raw token returned to caller; SHA256 hash stored in DB. Matches the existing API token pattern.

---

### 2. Server bootstrap

**Flag:** `--bootstrap-admin=<email>` (also readable from `RELAY_BOOTSTRAP_ADMIN` env var)

**Startup behaviour:**

1. If the flag is not set, skip.
2. Query whether any admin user exists (`SELECT 1 FROM users WHERE is_admin = TRUE LIMIT 1`).
3. If at least one admin exists: log a warning (`bootstrap-admin skipped: admin already exists`) and skip.
4. If no admin exists:
   - Look up user by email.
   - If found: set `is_admin = TRUE`.
   - If not found: create user with `is_admin = TRUE`.
5. Log confirmation: `bootstrap admin ready: <email>`.

The flag is safe to leave set permanently — it becomes a no-op once any admin exists.

---

### 3. Modified `POST /v1/auth/token`

Request body gains an optional field:

```json
{ "email": "user@example.com", "invite_token": "<raw token>" }
```

**Logic:**

| Scenario | Behaviour |
|---|---|
| Email exists in DB | Issue new API token (existing re-login, unchanged) |
| Email not found, no `invite_token` | `403 Forbidden` — `"invite required"` |
| Email not found, `invite_token` present, invalid/expired/used | `400 Bad Request` — descriptive error |
| Email not found, `invite_token` present, email-bound and mismatch | `400 Bad Request` — `"invite not valid for this email"` |
| Email not found, `invite_token` valid | Create user, mark invite used, issue API token |

Invite validation:
- Hash the raw token, look up by hash.
- `used_at` must be NULL.
- `expires_at` must be in the future.
- If `email` column is set on the invite, it must match the request email (case-insensitive).

---

### 4. New endpoint: `POST /v1/invites` (admin only)

**Request:**

```json
{ "email": "optional@example.com", "expires_in": "72h" }
```

- `email` — optional; binds the invite to a specific address.
- `expires_in` — optional duration string (`"1h"`, `"24h"`, `"168h"`); defaults to `"72h"`.

**Response:**

```json
{
  "id": "<uuid>",
  "token": "<raw token>",
  "email": "optional@example.com",
  "expires_at": "2026-04-19T12:00:00Z"
}
```

The raw token is returned once and never again. Protected by the existing `auth(admin(...))` middleware chain.

---

### 5. CLI changes

#### New command: `relay invite create`

```
relay invite create [--email user@example.com] [--expires 72h]
```

- Calls `POST /v1/invites`.
- Prints the raw invite token to stdout.
- Admin copies it and delivers to the recipient out-of-band.

Registered in the existing `Command` struct registry in `internal/cli`.

#### Modified: `relay login`

Current flow: prompt server URL → prompt email → `POST /v1/auth/token` → save config.

New flow:

1. Prompt server URL.
2. Prompt email.
3. `POST /v1/auth/token` with email only.
4. If response is `403 "invite required"`: prompt for invite token, retry with `invite_token` included.
5. Save token to config on success.

Existing users never see the invite prompt — it only appears when the server signals it is needed. The retry is transparent to the caller.

---

## Security properties

- **Invite tokens**: 32 random bytes (256 bits entropy), hex-encoded; only SHA256 hash stored in DB. Raw token never persisted.
- **One-time use**: `used_at` set atomically on redemption; concurrent redemption attempts are rejected.
- **Always expires**: `expires_at` is required on every invite; no open-ended tokens.
- **Email binding**: optional but enforced server-side when set.
- **No behaviour change for existing users**: re-login path is unchanged.
- **Bootstrap idempotency**: flag becomes a no-op once any admin exists; safe to leave permanently set.

---

## File changes summary

| File | Change |
|---|---|
| `internal/store/migrations/000002_invites.up.sql` | New migration — `invites` table |
| `internal/store/migrations/000002_invites.down.sql` | Drop `invites` table |
| `internal/store/query/invites.sql` | Three sqlc queries for invites |
| `internal/store/query/users.sql` | Add `AdminExists` and `PromoteUserToAdmin` queries |
| `internal/store/invites.sql.go` | Generated by sqlc |
| `internal/store/users.sql.go` | Regenerated by sqlc |
| `internal/store/store.go` | Add invite query methods to interface |
| `internal/api/server.go` | Wire `--bootstrap-admin` flag; register `POST /v1/invites` route |
| `internal/api/invites.go` | New file — `handleCreateInvite` handler |
| `internal/api/token.go` | Modify `handleCreateToken` — invite gate for new users |
| `cmd/relay-server/main.go` | Accept and pass `--bootstrap-admin` flag |
| `internal/cli/invites.go` | New file — `relay invite create` command |
| `internal/cli/login.go` | Retry with invite token on `403 "invite required"` |
| `internal/cli/command.go` | Register `invite` subcommand |
