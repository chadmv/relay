# Password Authentication Design

**Date:** 2026-04-17  
**Status:** Approved  
**Scope:** Add password-based authentication to protect user accounts from impersonation

## Problem

`POST /v1/auth/token` currently issues a bearer token to any caller who provides a known email address. There is no secret that proves the caller is the account owner. Any user who knows another user's email can obtain a valid token for that account.

## Solution

Add a bcrypt password to every user account. Split the existing token endpoint into two dedicated endpoints — one for registration, one for login. Add a password-change endpoint and corresponding CLI command.

## Database

**New migration:** Add `password_hash TEXT NOT NULL` to the `users` table.

**New sqlc queries:**
- `CreateUserWithPassword(name, email, is_admin, password_hash)` — replaces `CreateUser` in the registration path
- `GetUserByEmailWithPassword(email)` — returns the full user row including `password_hash` for login verification
- `GetPasswordHashByID(user_id)` — returns only `password_hash` for the password-change endpoint (user ID is available from bearer token context; email is not needed)
- `SetPasswordHash(user_id, password_hash)` — used by the password-change endpoint and bootstrap

**Password hashing:** bcrypt at cost factor 12 (`golang.org/x/crypto/bcrypt`). Approximately 250ms per hash on modern hardware — acceptable for interactive login, impractical to brute-force.

## API Endpoints

### `POST /v1/auth/register`

Creates a new user account and returns a bearer token.

**Request:**
```json
{ "email": "user@example.com", "name": "Alice", "password": "...", "invite_token": "..." }
```

**Behavior:**
1. Validate email format and that password is at least 8 characters.
2. Validate invite token (must exist, unused, not expired, email matches if restricted).
3. Hash password with bcrypt cost 12.
4. In a single transaction: create user, mark invite used.
5. Generate and store a 30-day bearer token, return it.

**Errors:**
- `400` — missing/invalid email, password too short, invalid/used/expired/mismatched invite
- `500` — database or token generation failure

### `POST /v1/auth/login`

Authenticates an existing user and returns a bearer token.

**Request:**
```json
{ "email": "user@example.com", "password": "..." }
```

**Behavior:**
1. Look up user by email.
2. If user not found, run a dummy `bcrypt.CompareHashAndPassword` against a static placeholder hash to ensure constant-time response (prevents email enumeration via timing).
3. Verify password against stored bcrypt hash.
4. Generate and store a 30-day bearer token, return it.

**Errors:**
- `401 Unauthorized` — `"invalid email or password"` for any credential failure (unknown email or wrong password — identical message and timing to prevent enumeration)
- `500` — database or token generation failure

### `PUT /v1/users/me/password`

Changes the authenticated user's password.

**Request:**
```json
{ "current_password": "...", "new_password": "..." }
```

**Behavior:**
1. Require valid bearer token (existing `BearerAuth` middleware).
2. Fetch the user's current `password_hash`.
3. Verify `current_password` against stored hash.
4. Validate `new_password` is at least 8 characters.
5. Hash and store new password.

**Errors:**
- `401` — missing/invalid bearer token
- `403 Forbidden` — `"current password is incorrect"`
- `400` — new password too short
- `500` — database failure

### Removed endpoint

`POST /v1/auth/token` is deleted. The system is not in production so no deprecation period is needed.

## Bootstrap Admin

The `RELAY_BOOTSTRAP_ADMIN` env var (email) is joined by a new `RELAY_BOOTSTRAP_PASSWORD` env var. Both must be set together. On startup, if no admin exists, the server creates the admin account using `CreateUserWithPassword` with the bcrypt-hashed bootstrap password.

If `RELAY_BOOTSTRAP_ADMIN` is set but `RELAY_BOOTSTRAP_PASSWORD` is not (or is empty), startup fails with a clear error message.

## CLI Changes

### `relay login` (updated)

Prompts in order: server URL (if not configured), email, password (masked — no echo). Hits `POST /v1/auth/login`. Saves returned token to `~/.relay/config.json` (Linux/Mac) or `%APPDATA%\relay\config.json` (Windows).

### `relay register` (new)

Prompts in order: server URL (if not configured), email, name, invite token, password (masked), confirm password (masked — must match). Hits `POST /v1/auth/register`. Saves returned token to config.

### `relay passwd` (new)

Requires an existing valid token in config. Prompts for current password (masked), new password (masked), confirm new password (masked — must match). Hits `PUT /v1/users/me/password`.

## Security Rules

- **Email enumeration prevention:** `POST /v1/auth/login` returns identical error message and response time regardless of whether the email exists or the password is wrong. A constant-time dummy bcrypt comparison is performed when the email is not found.
- **Password minimum length:** 8 characters. Enforced at register and passwd endpoints.
- **No complexity rules:** Length is the most effective constraint; complexity rules provide marginal benefit and reduce usability.
- **No password in logs:** Request bodies are not logged (existing convention, no change needed).

## Testing

### Unit tests

**`internal/api/auth_test.go`** (replaces `token_test.go`):
- Register: happy path, missing invite, used invite, expired invite, mismatched email invite, password too short
- Login: happy path, wrong password, unknown email (verify same `401` response as wrong password)
- Password change: happy path, wrong current password, new password too short

**`internal/cli/login_test.go`** (updated):
- Password prompt present, masked input

**`internal/cli/register_test.go`** (new):
- Happy path, password mismatch prompt retry

**`internal/cli/passwd_test.go`** (new):
- Happy path, wrong current password error, password mismatch prompt retry

### Integration tests (`//go:build integration`)

**`internal/api/auth_integration_test.go`**:
- Full register → login → passwd flow against a real Postgres container
- Token remains valid after password change (existing tokens are not invalidated)
- Expired and used invite rejection
- Enumeration: verify identical HTTP status and body for unknown email vs wrong password

## Out of Scope

- Account lockout after failed login attempts (can be added later)
- Password reset via email (requires SMTP infrastructure)
- Multi-factor authentication
- Token invalidation on password change (existing tokens stay valid; can be added when needed)
- Admin password reset for other users
