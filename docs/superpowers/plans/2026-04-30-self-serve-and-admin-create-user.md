# Self-Serve Registration & Admin-Create User — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an operator-gated self-serve registration path on `POST /v1/auth/register` and a new admin-only `POST /v1/users` endpoint for direct user provisioning (including admin creation), plus matching CLI subcommands.

**Architecture:** A new env var `RELAY_ALLOW_SELF_REGISTER` (default off) sets an exported `AllowSelfRegister` field on `api.Server`. When the field is true and a register request omits `invite_token`, the existing handler creates a non-admin user directly. Admin-create lives at `POST /v1/users`, mirrors the request shape of register plus an `is_admin` field, and reuses the existing `userResponse`/`toUserResponse`/`CreateUserWithPassword` plumbing.

**Tech Stack:** Go 1.22+, `net/http` ServeMux, sqlc/pgx, bcrypt, testify, testcontainers-go (integration tests).

**Spec:** [docs/superpowers/specs/2026-04-30-self-serve-and-admin-create-user-design.md](../specs/2026-04-30-self-serve-and-admin-create-user-design.md)

---

## File Map

**Modified:**
- `internal/api/server.go` — add `AllowSelfRegister bool` exported field; register `POST /v1/users` route
- `internal/api/auth.go` — modify `handleRegister` to allow missing `invite_token` when `s.AllowSelfRegister`
- `internal/api/users.go` — add `handleAdminCreateUser` + request struct
- `internal/api/auth_integration_test.go` — add self-serve register tests
- `internal/api/users_integration_test.go` — add admin-create-user tests
- `cmd/relay-server/main.go` — parse `RELAY_ALLOW_SELF_REGISTER` and assign to `srv.AllowSelfRegister`
- `internal/cli/register.go` — make invite-token prompt optional
- `internal/cli/register_test.go` — add no-invite happy-path test
- `internal/cli/admin_users.go` — add `doAdminUsersCreate`; update `doAdminUsers` dispatch and usage strings
- `internal/cli/admin_users_test.go` — tests for new subcommand
- `internal/cli/admin_test.go` — usage assertion updates if needed
- `CLAUDE.md` — env-var table row, CLI section additions

**Moved (backlog cleanup):**
- `docs/backlog/bug-2026-04-25-relay-register-requires-invite-token.md` → `docs/backlog/closed/`

**No new files.** No schema changes. No new sqlc queries.

---

## Task 1: Add `AllowSelfRegister` field to `api.Server`

**Files:**
- Modify: `internal/api/server.go:18-53`

This is a struct-only change. Existing call sites of `api.New(...)` are not touched — the field is exported, defaults to `false`, and is set post-construction. Tests that need it on will set `srv.AllowSelfRegister = true` after `api.New(...)` returns.

- [ ] **Step 1: Add exported field to `Server` struct**

In [internal/api/server.go](internal/api/server.go), modify the `Server` struct (lines 18-28) to add one field at the bottom:

```go
type Server struct {
	pool             *pgxpool.Pool
	q                *store.Queries
	broker           *events.Broker
	registry         *worker.Registry
	CORSOrigins      []string
	LoginLimitN      int
	LoginLimitWin    time.Duration
	RegisterLimitN   int
	RegisterLimitWin time.Duration

	// AllowSelfRegister, when true, lets POST /v1/auth/register succeed without
	// an invite_token. Set by main.go from RELAY_ALLOW_SELF_REGISTER. Defaults
	// to false so existing deployments continue to require invites.
	AllowSelfRegister bool
}
```

The `New(...)` constructor is **not modified** — it leaves `AllowSelfRegister` at zero (false), which is the safe default.

- [ ] **Step 2: Verify the build**

Run: `go build ./...`
Expected: succeeds with no errors. (No call sites change; tests still compile because the new field is unset by default.)

- [ ] **Step 3: Commit**

```bash
git add internal/api/server.go
git commit -m "api: add Server.AllowSelfRegister field

Exported field, default false. Will be wired from
RELAY_ALLOW_SELF_REGISTER in a follow-up commit and consumed by
handleRegister."
```

---

## Task 2: Self-serve branch in `handleRegister` — failing tests

**Files:**
- Test: `internal/api/auth_integration_test.go` (append new tests)

We write the integration tests first; they will fail until Task 3 implements the branch. Tests follow the existing patterns in the same file (testcontainers-spawned pool, in-process server via `httptest.NewRecorder`).

- [ ] **Step 1: Add `TestRegister_SelfServe_HappyPath`**

Append to [internal/api/auth_integration_test.go](internal/api/auth_integration_test.go):

```go
func TestRegister_SelfServe_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	srv.AllowSelfRegister = true

	body, _ := json.Marshal(map[string]string{
		"email":    "selfserve@test.com",
		"name":     "Self Serve",
		"password": "securepass1",
	})
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp["token"])
	assert.NotEmpty(t, resp["expires_at"])

	user, err := q.GetUserByEmail(ctx, "selfserve@test.com")
	require.NoError(t, err)
	assert.Equal(t, "Self Serve", user.Name)
	assert.False(t, user.IsAdmin, "self-serve users must never be admins")
	assert.NotEmpty(t, user.PasswordHash)
}
```

- [ ] **Step 2: Add `TestRegister_SelfServe_DisabledByDefault`**

```go
func TestRegister_SelfServe_DisabledByDefault(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	// srv.AllowSelfRegister left at default false.

	body, _ := json.Marshal(map[string]string{
		"email":    "denied@test.com",
		"password": "securepass1",
	})
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "invite_token is required", resp["error"])
}
```

- [ ] **Step 3: Add `TestRegister_SelfServe_FlagOnInviteStillWorks`**

```go
func TestRegister_SelfServe_FlagOnInviteStillWorks(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()
	admin := createTestUser(t, q, "Admin", "admin@test.com", true)
	inviteToken := createTestInvite(t, q, admin.ID, nil, 72*time.Hour)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	srv.AllowSelfRegister = true // both paths active

	body, _ := json.Marshal(map[string]string{
		"email":        "invited@test.com",
		"name":         "Invited User",
		"password":     "securepass1",
		"invite_token": inviteToken,
	})
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)

	// Invite is consumed even though self-serve is on.
	hash := tokenhash.Hash(inviteToken)
	invite, err := q.GetInviteByTokenHash(ctx, hash)
	require.NoError(t, err)
	assert.True(t, invite.UsedAt.Valid, "invite should be marked used")
}
```

- [ ] **Step 4: Add `TestRegister_SelfServe_DuplicateEmail`**

```go
func TestRegister_SelfServe_DuplicateEmail(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	createTestUser(t, q, "Existing", "dup@test.com", false)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	srv.AllowSelfRegister = true

	body, _ := json.Marshal(map[string]string{
		"email":    "dup@test.com",
		"password": "securepass1",
	})
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "email already registered", resp["error"])
}
```

- [ ] **Step 5: Run the new tests and verify they fail**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRegister_SelfServe -v -timeout 120s`

Expected: all four tests **FAIL**. The happy-path test will get `400 invite_token is required` instead of 201; `DisabledByDefault` will pass (it asserts the existing behavior); `FlagOnInviteStillWorks` and `DuplicateEmail` will fail or behave like the existing path.

If `DisabledByDefault` already passes — that's fine; it's a regression guard. The point of running here is to confirm the *new* assertions fail before implementation.

- [ ] **Step 6: Commit**

```bash
git add internal/api/auth_integration_test.go
git commit -m "test: add failing self-serve register integration tests

Covers happy path, default-off behavior, invite-still-works when flag
is on, and duplicate-email handling. Implementation follows."
```

---

## Task 3: Self-serve branch in `handleRegister` — implementation

**Files:**
- Modify: `internal/api/auth.go:55-170`

The existing handler has one big code path. We restructure it so that when `req.InviteToken == ""` we either reject (existing message) or fall into a new self-serve branch. The shared post-validation flow (bcrypt, user create with duplicate-email handling, token issue) is factored into a small helper to avoid duplication.

- [ ] **Step 1: Refactor `handleRegister` to add the self-serve branch**

Replace the body of [internal/api/auth.go](internal/api/auth.go) `handleRegister` (lines 55-170) with the following. Note the new helper `(s *Server) createUserAndIssueToken` factored out at the bottom.

```go
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email       string `json:"email"`
		Name        string `json:"name"`
		Password    string `json:"password"`
		InviteToken string `json:"invite_token"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, "invalid email address")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	ctx := r.Context()

	if req.InviteToken == "" {
		if !s.AllowSelfRegister {
			writeError(w, http.StatusBadRequest, "invite_token is required")
			return
		}
		s.registerSelfServe(ctx, w, req.Email, req.Name, req.Password)
		return
	}

	// Invite-redemption path (unchanged behavior).
	tokenHash := tokenhash.Hash(req.InviteToken)
	invite, err := s.q.GetInviteByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "invalid invite token")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to look up invite")
		}
		return
	}
	if invite.UsedAt.Valid {
		writeError(w, http.StatusBadRequest, "invite already used")
		return
	}
	if time.Now().After(invite.ExpiresAt.Time) {
		writeError(w, http.StatusBadRequest, "invite expired")
		return
	}
	if invite.Email != nil && !strings.EqualFold(*invite.Email, req.Email) {
		writeError(w, http.StatusBadRequest, "invite not valid for this email")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	txq := s.q.WithTx(tx)

	name := req.Name
	if name == "" {
		name = req.Email
	}
	user, err := txq.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name:         name,
		Email:        req.Email,
		IsAdmin:      false,
		PasswordHash: string(passwordHash),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "email already registered")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to create user")
		}
		return
	}

	rowsAffected, err := txq.MarkInviteUsed(ctx, store.MarkInviteUsedParams{
		ID:     invite.ID,
		UsedBy: user.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to redeem invite")
		return
	}
	if rowsAffected == 0 {
		writeError(w, http.StatusBadRequest, "invite already used")
		return
	}

	token, expires, err := s.issueToken(ctx, txq, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit registration")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      token,
		"expires_at": expires,
	})
}

// registerSelfServe creates a non-admin user with a fresh session token. Caller
// is responsible for prior validation of email and password length, and for
// confirming that AllowSelfRegister is true.
func (s *Server) registerSelfServe(ctx context.Context, w http.ResponseWriter, email, name, password string) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	txq := s.q.WithTx(tx)

	if name == "" {
		name = email
	}
	user, err := txq.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name:         name,
		Email:        email,
		IsAdmin:      false, // hardcoded; never admin via self-serve
		PasswordHash: string(passwordHash),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "email already registered")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to create user")
		}
		return
	}

	token, expires, err := s.issueToken(ctx, txq, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit registration")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      token,
		"expires_at": expires,
	})
}
```

Notes:
- The invite-redemption path is **byte-identical** to the existing implementation (no behavior change).
- `registerSelfServe` is intentionally a separate function for clarity — the two paths share enough that further factoring is tempting, but the invite path has a transaction-spanning `MarkInviteUsed` step in the middle that doesn't exist in self-serve. Keeping them separate avoids a `bool needsInvite` parameter that splits behavior internally.
- The `context` import already exists in this file (line 4) so no import changes needed.

- [ ] **Step 2: Run the integration tests added in Task 2**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRegister_SelfServe -v -timeout 120s`

Expected: all four tests pass.

- [ ] **Step 3: Run the full register test suite to confirm no regressions**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRegister -v -timeout 120s`

Expected: all `TestRegister_*` tests pass, including the existing `TestRegister_HappyPath`, `TestRegister_PasswordTooShort`, etc.

- [ ] **Step 4: Commit**

```bash
git add internal/api/auth.go
git commit -m "api: implement self-serve register branch

When AllowSelfRegister is true and invite_token is empty, register
creates a non-admin user directly. Invite path unchanged. Self-serve
users always have is_admin=false (hardcoded, never reads a client
field)."
```

---

## Task 4: Wire `RELAY_ALLOW_SELF_REGISTER` in `cmd/relay-server/main.go`

**Files:**
- Modify: `cmd/relay-server/main.go:123-137`

- [ ] **Step 1: Add the env-var parse and field assignment**

In [cmd/relay-server/main.go](cmd/relay-server/main.go), after line 137 (where `httpServer := api.New(...)` is called), add:

```go
	if v := os.Getenv("RELAY_ALLOW_SELF_REGISTER"); v != "" {
		allow, err := strconv.ParseBool(v)
		if err != nil {
			log.Fatalf("parse RELAY_ALLOW_SELF_REGISTER: %v", err)
		}
		httpServer.AllowSelfRegister = allow
	}
```

`strconv` is already imported (line 11). `log` and `os` are already imported. No import changes needed.

- [ ] **Step 2: Build the binary**

Run: `go build ./cmd/relay-server/...`
Expected: builds cleanly.

- [ ] **Step 3: Smoke-test the env-var parse**

Run a quick negative-path check by attempting to start the server with a bad value:

```bash
RELAY_ALLOW_SELF_REGISTER=notabool ./bin/relay-server 2>&1 | head -5
```

Expected: process exits with `parse RELAY_ALLOW_SELF_REGISTER: strconv.ParseBool: ...`.

(Skip this step if you don't want to spin up the binary; the unit tests in Task 2-3 already exercise the field, and `strconv.ParseBool` is stdlib.)

- [ ] **Step 4: Commit**

```bash
git add cmd/relay-server/main.go
git commit -m "cmd/relay-server: parse RELAY_ALLOW_SELF_REGISTER

Empty -> false. Any value strconv.ParseBool accepts is honored. Other
values fail-fast at startup, matching the pattern used by
RELAY_CORS_ORIGINS and RELAY_*_RATE_LIMIT."
```

---

## Task 5: Admin-create-user endpoint — failing tests

**Files:**
- Test: `internal/api/users_integration_test.go` (append)

- [ ] **Step 1: Add `TestAdminCreateUser_HappyPath`**

Append to [internal/api/users_integration_test.go](internal/api/users_integration_test.go):

```go
func TestAdminCreateUser_HappyPath(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q)

	body := map[string]any{
		"email":    "newhire@test.com",
		"name":     "New Hire",
		"password": "securepass1",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	require.Equal(t, http.StatusCreated, code)

	assert.NotEmpty(t, resp["id"])
	assert.Equal(t, "newhire@test.com", resp["email"])
	assert.Equal(t, "New Hire", resp["name"])
	assert.Equal(t, false, resp["is_admin"])
	assert.NotEmpty(t, resp["created_at"])
	assert.Nil(t, resp["password_hash"], "password_hash must never be returned")

	// New user can log in.
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "newhire@test.com",
		"password": "securepass1",
	})
	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(loginBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}
```

If `postJSON` does not exist in this file, add it next to the existing `patchJSON` helper. Mirror that helper's signature:

```go
func postJSON(t *testing.T, srv *api.Server, token, path string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp map[string]any
	if rec.Body.Len() > 0 {
		_ = json.NewDecoder(rec.Body).Decode(&resp)
	}
	return rec.Code, resp
}
```

(Check the existing helpers in the file before adding — `patchJSON` exists per the prior session retro. Re-use the same shape.)

- [ ] **Step 2: Add `TestAdminCreateUser_CreatesAdmin`**

```go
func TestAdminCreateUser_CreatesAdmin(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q)

	body := map[string]any{
		"email":    "admin2@test.com",
		"name":     "Second Admin",
		"password": "securepass1",
		"is_admin": true,
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	require.Equal(t, http.StatusCreated, code)
	assert.Equal(t, true, resp["is_admin"])

	// Sanity-check via the store: row truly has is_admin=true.
	u, err := q.GetUserByEmail(t.Context(), "admin2@test.com")
	require.NoError(t, err)
	assert.True(t, u.IsAdmin)
}
```

- [ ] **Step 3: Add `TestAdminCreateUser_NonAdminForbidden`**

```go
func TestAdminCreateUser_NonAdminForbidden(t *testing.T) {
	srv, q := newTestServer(t)
	seedUser(t, q, "user@test.com", "Plain User", false)
	userToken := loginAs(t, srv, "user@test.com", "testpassword1")

	body := map[string]any{
		"email":    "shouldfail@test.com",
		"password": "securepass1",
	}
	code, _ := postJSON(t, srv, userToken, "/v1/users", body)
	assert.Equal(t, http.StatusForbidden, code)
}
```

Use the existing `loginAs` / `seedUser` helpers from the same file (referenced in the prior PATCH tests). If a helper to log in a regular user does not exist, add a small one mirroring `loginAsAdmin`.

- [ ] **Step 4: Add `TestAdminCreateUser_Unauthenticated`**

```go
func TestAdminCreateUser_Unauthenticated(t *testing.T) {
	srv, _ := newTestServer(t)
	body := map[string]any{
		"email":    "x@test.com",
		"password": "securepass1",
	}
	code, _ := postJSON(t, srv, "", "/v1/users", body)
	assert.Equal(t, http.StatusUnauthorized, code)
}
```

- [ ] **Step 5: Add `TestAdminCreateUser_DuplicateEmail`**

```go
func TestAdminCreateUser_DuplicateEmail(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q)
	seedUser(t, q, "dup@test.com", "Existing", false)

	body := map[string]any{
		"email":    "dup@test.com",
		"password": "securepass1",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "email already registered", resp["error"])
}
```

- [ ] **Step 6: Add `TestAdminCreateUser_InvalidEmail`, `TestAdminCreateUser_WeakPassword`, `TestAdminCreateUser_MissingPassword`**

```go
func TestAdminCreateUser_InvalidEmail(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q)

	body := map[string]any{
		"email":    "not-an-email",
		"password": "securepass1",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "invalid email address", resp["error"])
}

func TestAdminCreateUser_WeakPassword(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q)

	body := map[string]any{
		"email":    "weak@test.com",
		"password": "short",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "password must be at least 8 characters", resp["error"])
}

func TestAdminCreateUser_MissingPassword(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q)

	body := map[string]any{
		"email": "nopw@test.com",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "password must be at least 8 characters", resp["error"])
}
```

- [ ] **Step 7: Add `TestAdminCreateUser_NameDefaultsToEmail`**

```go
func TestAdminCreateUser_NameDefaultsToEmail(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q)

	body := map[string]any{
		"email":    "noname@test.com",
		"password": "securepass1",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	require.Equal(t, http.StatusCreated, code)
	assert.Equal(t, "noname@test.com", resp["name"])
}
```

- [ ] **Step 8: Run the new tests and verify they fail**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestAdminCreateUser -v -timeout 120s`

Expected: all tests fail with `404 Not Found` (route not registered yet) or similar. Tests for `Unauthenticated`/`NonAdminForbidden` may pass because no route means the auth middleware doesn't run — that's OK, they'll be re-validated after Task 6.

- [ ] **Step 9: Commit**

```bash
git add internal/api/users_integration_test.go
git commit -m "test: add failing admin-create-user integration tests

Covers happy path, admin creation, non-admin forbidden, unauthenticated,
duplicate email, invalid email, weak/missing password, and name
defaulting to email."
```

---

## Task 6: Admin-create-user handler implementation

**Files:**
- Modify: `internal/api/users.go` (append handler + request struct)
- Modify: `internal/api/server.go:118` (register route)

- [ ] **Step 1: Add `handleAdminCreateUser` and helper**

Append to [internal/api/users.go](internal/api/users.go) at the end of the file:

```go
// createUserRequest is the request body for POST /v1/users (admin-only).
type createUserRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, "invalid email address")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = req.Email
	}

	user, err := s.q.CreateUserWithPassword(r.Context(), store.CreateUserWithPasswordParams{
		Name:         name,
		Email:        req.Email,
		IsAdmin:      req.IsAdmin,
		PasswordHash: string(passwordHash),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "email already registered")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to create user")
		}
		return
	}

	writeJSON(w, http.StatusCreated, toUserResponse(user.ID, user.Email, user.Name, user.IsAdmin, user.CreatedAt))
}
```

Required new imports in [internal/api/users.go](internal/api/users.go) (top of file):

```go
import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)
```

(Add `net/mail`, `github.com/jackc/pgx/v5/pgconn`, and `golang.org/x/crypto/bcrypt` to the existing import block.)

- [ ] **Step 2: Register the route**

In [internal/api/server.go](internal/api/server.go), modify the user-management section (lines 114-118). Add one line above the existing `mux.Handle("PATCH /v1/users/{id}", ...)`:

```go
	// User management
	mux.Handle("GET /v1/users", auth(admin(http.HandlerFunc(s.handleListUsers))))
	mux.Handle("POST /v1/users", auth(admin(http.HandlerFunc(s.handleAdminCreateUser))))
	mux.Handle("POST /v1/users/password-reset", auth(admin(http.HandlerFunc(s.handleAdminPasswordReset))))
	mux.Handle("PATCH /v1/users/me", auth(http.HandlerFunc(s.handleUpdateMe)))
	mux.Handle("PATCH /v1/users/{id}", auth(admin(http.HandlerFunc(s.handleAdminUpdateUser))))
```

Go 1.22+ ServeMux distinguishes by method, so `POST /v1/users` and `GET /v1/users` coexist without ordering concerns.

- [ ] **Step 3: Run the new admin-create tests**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestAdminCreateUser -v -timeout 120s`

Expected: all eight tests pass.

- [ ] **Step 4: Run the full users test suite to confirm no regressions**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestUser -v -timeout 120s`
Plus: `go test -tags integration -p 1 ./internal/api/... -run TestList -v -timeout 120s`
Plus: `go test -tags integration -p 1 ./internal/api/... -run TestUpdate -v -timeout 120s`

Expected: all existing user-management tests still pass.

- [ ] **Step 5: Commit**

```bash
git add internal/api/users.go internal/api/server.go
git commit -m "api: add POST /v1/users for admin user creation

Admin-only endpoint. Accepts {email, name?, password, is_admin?} and
returns the userResponse shape (no session token). Reuses
CreateUserWithPassword and toUserResponse. No session token is issued
because the admin uses their own session; the created user logs in
separately."
```

---

## Task 7: `relay register` CLI — optional invite token

**Files:**
- Modify: `internal/cli/register.go:47-52`
- Modify: `internal/cli/register_test.go` (add test)

- [ ] **Step 1: Add a failing test for the no-invite case**

Append to [internal/cli/register_test.go](internal/cli/register_test.go):

```go
func TestRunRegister_NoInviteToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/auth/register", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "user@example.com", body["email"])
		require.Equal(t, "mypassword1", body["password"])
		// invite_token is either absent or empty — both are acceptable.
		require.Empty(t, body["invite_token"])
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"token":      "tok-self",
			"expires_at": time.Now().Add(30 * 24 * time.Hour),
		})
	}))
	defer srv.Close()

	var saved *Config
	origSave := saveConfigFn
	saveConfigFn = func(cfg *Config) error { saved = cfg; return nil }
	t.Cleanup(func() { saveConfigFn = origSave })

	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return "mypassword1", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL}
	// URL (accept), email, name (blank), invite token (blank)
	input := strings.NewReader("\nuser@example.com\n\n\n")
	var out strings.Builder
	err := doRegister(context.Background(), cfg, input, &out)
	require.NoError(t, err)

	require.NotNil(t, saved)
	require.Equal(t, "tok-self", saved.Token)
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test ./internal/cli/... -run TestRunRegister_NoInviteToken -v -timeout 30s`

Expected: FAIL with `invite token is required`.

- [ ] **Step 3: Make the invite token optional in `doRegister`**

In [internal/cli/register.go](internal/cli/register.go), modify lines 47-52:

```go
	fmt.Fprint(out, "Invite token (leave blank if your server allows self-serve registration): ")
	inviteToken, _ := r.ReadString('\n')
	inviteToken = strings.TrimSpace(inviteToken)
```

Delete the `if inviteToken == "" { return fmt.Errorf("invite token is required") }` block (was lines 50-52 in the pre-modification file).

- [ ] **Step 4: Run both register tests to confirm they pass**

Run: `go test ./internal/cli/... -run TestRunRegister -v -timeout 30s`

Expected: `TestRunRegister_Success`, `TestRunRegister_PasswordMismatch`, and `TestRunRegister_NoInviteToken` all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/register.go internal/cli/register_test.go
git commit -m "cli: make invite token optional in 'relay register'

Server is the source of truth for whether self-serve is allowed; CLI
now sends an empty invite_token when the user leaves the prompt blank
and surfaces any 400 from the server verbatim."
```

---

## Task 8: `relay admin users create` CLI — failing tests

**Files:**
- Test: `internal/cli/admin_users_test.go` (append)

- [ ] **Step 1: Add `TestAdminUsersCreate_HappyPath`**

Append to [internal/cli/admin_users_test.go](internal/cli/admin_users_test.go):

```go
func TestAdminUsersCreate_HappyPath(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/users", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "00000000-0000-0000-0000-000000000001",
			"email":      capturedBody["email"],
			"name":       capturedBody["name"],
			"is_admin":   capturedBody["is_admin"],
			"created_at": time.Now(),
		})
	}))
	defer srv.Close()

	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return "newpassword1", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "admin-token"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "create",
		"--email", "ops@test.com",
		"--name", "Ops Bot",
	}, &out)
	require.NoError(t, err)

	assert.Equal(t, "ops@test.com", capturedBody["email"])
	assert.Equal(t, "Ops Bot", capturedBody["name"])
	assert.Equal(t, "newpassword1", capturedBody["password"])
	assert.Equal(t, false, capturedBody["is_admin"])
	assert.Contains(t, out.String(), "ops@test.com")
}
```

- [ ] **Step 2: Add `TestAdminUsersCreate_AdminFlag`**

```go
func TestAdminUsersCreate_AdminFlag(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "00000000-0000-0000-0000-000000000002",
			"email":      capturedBody["email"],
			"name":       capturedBody["email"],
			"is_admin":   true,
			"created_at": time.Now(),
		})
	}))
	defer srv.Close()

	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return "adminpass1", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "admin-token"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "create",
		"--email", "newadmin@test.com",
		"--admin",
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, true, capturedBody["is_admin"])
}
```

- [ ] **Step 3: Add `TestAdminUsersCreate_MissingEmail`**

```go
func TestAdminUsersCreate_MissingEmail(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "admin-token"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "create"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--email")
}
```

- [ ] **Step 4: Add `TestAdminUsersCreate_PasswordMismatch`**

```go
func TestAdminUsersCreate_PasswordMismatch(t *testing.T) {
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return "first1234", nil
		}
		return "second1234", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: "http://localhost", Token: "admin-token"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "create",
		"--email", "x@test.com",
	}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "passwords do not match")
}
```

- [ ] **Step 5: Add `TestAdminUsersCreate_ServerError`**

```go
func TestAdminUsersCreate_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "email already registered"})
	}))
	defer srv.Close()

	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return "anypassword1", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "admin-token"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "create",
		"--email", "dup@test.com",
	}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "email already registered")
}
```

- [ ] **Step 6: Add `TestAdminUsersCreate_NotLoggedIn`**

```go
func TestAdminUsersCreate_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"} // no Token
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "create",
		"--email", "x@test.com",
	}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}
```

- [ ] **Step 7: Run the new tests and verify they fail**

Run: `go test ./internal/cli/... -run TestAdminUsersCreate -v -timeout 30s`

Expected: failures with `unknown admin users subcommand: create` (the dispatch in `doAdminUsers` doesn't yet route to `create`).

- [ ] **Step 8: Commit**

```bash
git add internal/cli/admin_users_test.go
git commit -m "test: add failing tests for 'relay admin users create'

Covers happy path, --admin flag, missing email, password mismatch,
server error passthrough, and not-logged-in."
```

---

## Task 9: `relay admin users create` CLI — implementation

**Files:**
- Modify: `internal/cli/admin_users.go`

- [ ] **Step 1: Update the dispatch and usage strings**

In [internal/cli/admin_users.go](internal/cli/admin_users.go), modify `doAdminUsers` (lines 14-28):

```go
func doAdminUsers(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay admin users <list|get|create|update> [args]")
	}
	switch args[0] {
	case "list":
		return doAdminUsersList(ctx, cfg, args[1:], out)
	case "get":
		return doAdminUsersGet(ctx, cfg, args[1:], out)
	case "create":
		return doAdminUsersCreate(ctx, cfg, args[1:], out)
	case "update":
		return doAdminUsersUpdate(ctx, cfg, args[1:], out)
	default:
		return fmt.Errorf("unknown admin users subcommand: %s", args[0])
	}
}
```

- [ ] **Step 2: Add `doAdminUsersCreate`**

Append to [internal/cli/admin_users.go](internal/cli/admin_users.go) at the end of the file:

```go
func doAdminUsersCreate(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	fs := flag.NewFlagSet("admin users create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	email := fs.String("email", "", "email address (required)")
	name := fs.String("name", "", "display name (optional, defaults to email)")
	isAdmin := fs.Bool("admin", false, "create the user as an admin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" {
		return fmt.Errorf("--email is required")
	}

	password, err := readPasswordFn(out, "Password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if password == "" {
		return fmt.Errorf("password is required")
	}
	confirm, err := readPasswordFn(out, "Confirm password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if password != confirm {
		return fmt.Errorf("passwords do not match")
	}

	body := map[string]any{
		"email":    *email,
		"password": password,
		"is_admin": *isAdmin,
	}
	if trimmed := strings.TrimSpace(*name); trimmed != "" {
		body["name"] = trimmed
	}

	c := cfg.NewClient()
	var u userListItem
	if err := c.do(ctx, "POST", "/v1/users", body, &u); err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	printUserDetail(out, u)
	return nil
}
```

Notes on imports: `flag` is already imported (line 5). `strings` is already imported (line 9). No import changes needed.

- [ ] **Step 3: Update `admin_test.go` if it asserts the usage string**

In [internal/cli/admin_test.go](internal/cli/admin_test.go), check whether any test asserts on the exact string `"usage: relay admin users <list|get|update>"`. If so, update to `"usage: relay admin users <list|get|create|update>"`. (Run a grep first; the prior session's tests may not assert on the exact subcommand list.)

Run: `grep -n "list|get|update" internal/cli/admin_test.go internal/cli/admin_users_test.go`

If a test fails on this string, update it. If no match, skip this step.

- [ ] **Step 4: Run all CLI tests**

Run: `go test ./internal/cli/... -v -timeout 30s`

Expected: all tests pass, including the six new `TestAdminUsersCreate_*` tests.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/admin_users.go internal/cli/admin_test.go
git commit -m "cli: add 'relay admin users create' subcommand

Flags: --email (required), --name (optional), --admin (boolean).
Password is read interactively (twice) and never accepted on the
command line. Reuses printUserDetail for the success output."
```

---

## Task 10: Update CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add `RELAY_ALLOW_SELF_REGISTER` to the env-var table**

In [CLAUDE.md](CLAUDE.md), in the `## Environment Variables (relay-server)` table, add a new row above `RELAY_AGENT_ENROLLMENT_TOKEN`:

```markdown
| `RELAY_ALLOW_SELF_REGISTER` | _(unset)_ | When `true`, `POST /v1/auth/register` accepts requests without an `invite_token` and creates a non-admin user directly. Default off. Requires server restart to change. |
```

- [ ] **Step 2: Update the relay-CLI internals section**

In [CLAUDE.md](CLAUDE.md), in the `### relay CLI internals` section's bullet list, add:

```markdown
- `relay admin users create --email <email> [--name <name>] [--admin]` — admin-only direct user provisioning; password read interactively; calls `POST /v1/users`
```

And modify the existing `relay register` description (or any text about the invite token) to clarify the invite token is now optional when the server has self-serve enabled. If no specific bullet exists for `relay register`, leave register alone (it's a top-level command not in the admin subtree).

- [ ] **Step 3: Update the API description**

In [CLAUDE.md](CLAUDE.md), in the **`internal/api/`** paragraph that lists user-management endpoints, append after the `PATCH /v1/users/{id}` description:

```
Adds `POST /v1/users` (admin-only) for direct user provisioning, accepting `{email, name?, password, is_admin?}` and returning the same `userResponse` shape (no session token). The self-serve register branch on `POST /v1/auth/register` is gated by `Server.AllowSelfRegister` (set from `RELAY_ALLOW_SELF_REGISTER`); when on and `invite_token` is empty, the handler creates a non-admin user directly.
```

- [ ] **Step 4: Verify markdown rendering**

Run: `git diff CLAUDE.md` and skim for any broken table formatting or stray text.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md for self-serve register and POST /v1/users

Adds RELAY_ALLOW_SELF_REGISTER row to env-var table, mentions the new
admin CLI subcommand, and updates the API overview to cover both
changes."
```

---

## Task 11: Backlog cleanup

**Files:**
- Move: `docs/backlog/bug-2026-04-25-relay-register-requires-invite-token.md` → `docs/backlog/closed/`

- [ ] **Step 1: Confirm `docs/backlog/closed/` exists**

Run: `ls docs/backlog/closed/ | head -3`

If the directory does not exist, create it: `mkdir -p docs/backlog/closed`. (Per saved feedback, backlog housekeeping including the move is in scope.)

- [ ] **Step 2: Move the file**

Run:
```bash
git mv docs/backlog/bug-2026-04-25-relay-register-requires-invite-token.md docs/backlog/closed/
```

- [ ] **Step 3: Verify**

Run: `git status`
Expected: shows the file as renamed.

- [ ] **Step 4: Commit**

```bash
git commit -m "docs(backlog): close 'relay register requires invite token'

Closed by self-serve register + admin-create-user implementation."
```

---

## Task 12: Final full-test sweep

**Files:** none (verification only)

- [ ] **Step 1: Run unit tests**

Run: `make test`
Expected: all green.

- [ ] **Step 2: Run integration tests**

Run: `make test-integration`
Expected: all green; new `TestRegister_SelfServe_*` and `TestAdminCreateUser_*` tests visible in the output.

- [ ] **Step 3: Build all binaries**

Run: `make build`
Expected: `bin/relay-server`, `bin/relay-agent`, `bin/relay` all produced.

- [ ] **Step 4: Smoke-test the new CLI subcommand against a local server**

(Optional but recommended.) Start a local relay-server with `RELAY_ALLOW_SELF_REGISTER=true RELAY_BOOTSTRAP_ADMIN=admin@local RELAY_BOOTSTRAP_PASSWORD=adminpass1 ./bin/relay-server` in one terminal. In another:

```bash
./bin/relay login                   # log in as admin
./bin/relay admin users create --email ops@local --name "Ops Bot"
# enter password twice
./bin/relay admin users get ops@local
```

Expected: account is created and visible in the get output.

Then test self-serve from a separate config:

```bash
RELAY_HOME=/tmp/relay-selfserve ./bin/relay register
# Server URL: http://localhost:8080
# Email: self@local
# Name: (blank)
# Invite token: (blank)
# Password: anypass1234 (twice)
```

Expected: `Registered and logged in. ...`. Confirm via admin `relay admin users list` that the user exists with `is_admin=no`.

- [ ] **Step 5: No commit** — this task is verification only.

---

## Spec Coverage Check

| Spec section | Tasks |
|---|---|
| `RELAY_ALLOW_SELF_REGISTER` env var, `Server.AllowSelfRegister` field | Task 1, Task 4 |
| `POST /v1/auth/register` self-serve branch | Task 2 (tests), Task 3 (impl) |
| `POST /v1/users` admin-create endpoint | Task 5 (tests), Task 6 (impl) |
| `relay register` optional invite token | Task 7 |
| `relay admin users create` subcommand | Task 8 (tests), Task 9 (impl) |
| Documentation in CLAUDE.md | Task 10 |
| Backlog cleanup | Task 11 |
| Final verification | Task 12 |

**Out of scope (explicitly):** email verification, self-serve admin promotion, domain allowlist, hot-reload of the flag, fixing the pre-existing 409-based email-enumeration vector. None of these have tasks.
