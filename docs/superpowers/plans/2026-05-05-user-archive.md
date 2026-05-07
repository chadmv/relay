# User Archive (Admin Soft-Delete) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an admin-only archive capability for users — soft-delete via a non-null `users.archived_at` timestamp, reversible via unarchive, with API token revocation, login rejection, and email-lock preservation.

**Architecture:** A single Postgres migration adds `archived_at TIMESTAMPTZ` plus a partial index on active rows. Five new sqlc queries (`ArchiveUser`, `UnarchiveUser`, `CountActiveAdmins`, `DeleteUserAPITokens`, `ListUsersIncludingArchived`) plus a filter on `ListUsers` express the soft-delete model. Two admin-only HTTP routes — `POST /v1/users/{id}/archive` and `POST /v1/users/{id}/unarchive` — drive the flow, with `handleLogin` checking `archived_at` after the bcrypt compare to preserve email-enumeration timing parity. CLI subcommands wrap the new endpoints. Email stays locked (no schema change to `email`'s `UNIQUE` constraint).

**Tech Stack:** Go 1.22 stdlib HTTP mux, `pgx/v5` + sqlc-generated store, `golang-migrate` migrations, testcontainers-go Postgres for integration tests, `bcrypt` (forced to `MinCost` in tests via `SetBcryptCostForTest`).

**Reference spec:** [docs/superpowers/specs/2026-05-05-user-archive-design.md](../specs/2026-05-05-user-archive-design.md)

---

## Pre-flight

Before starting, confirm the worktree is clean and on a feature branch.

```bash
git status
git branch --show-current
```

Expected: clean working tree, on `claude/laughing-bouman-c92d86` (or a fresh branch the user created).

---

## Task 1: Schema migration

Adds `archived_at` to `users` plus a partial index for active-user lookups. After this task, `make generate` is *not* run yet — that's Task 2.

**Files:**
- Create: `internal/store/migrations/000010_user_archive.up.sql`
- Create: `internal/store/migrations/000010_user_archive.down.sql`

- [ ] **Step 1: Write the up migration**

Create `internal/store/migrations/000010_user_archive.up.sql`:

```sql
ALTER TABLE users ADD COLUMN archived_at TIMESTAMPTZ;
CREATE INDEX users_active_idx ON users (id) WHERE archived_at IS NULL;
```

- [ ] **Step 2: Write the down migration**

Create `internal/store/migrations/000010_user_archive.down.sql`:

```sql
DROP INDEX IF EXISTS users_active_idx;
ALTER TABLE users DROP COLUMN archived_at;
```

- [ ] **Step 3: Verify migrations parse and apply cleanly**

The migrations are embedded into the binary at build time. To sanity-check now, build the server (this re-embeds migrations and would fail on syntactically broken SQL; semantic problems will surface in Task 2 integration runs).

Run: `make build`
Expected: three binaries produced under `bin/`, no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/store/migrations/000010_user_archive.up.sql internal/store/migrations/000010_user_archive.down.sql
git commit -m "feat(db): add users.archived_at + partial active-users index"
```

---

## Task 2: Store queries + sqlc generation

Adds the new queries and modifies `ListUsers` and `UpdateUserName` to play with the new column. Then runs `make generate` to refresh `*.sql.go` and `models.go`.

**Files:**
- Modify: `internal/store/query/users.sql`
- Generated (do not hand-edit): `internal/store/users.sql.go`, `internal/store/models.go`

- [ ] **Step 1: Edit `internal/store/query/users.sql`**

Replace the entire file contents with:

```sql
-- name: CreateUserWithPassword :one
INSERT INTO users (name, email, is_admin, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: AdminExists :one
SELECT EXISTS(
    SELECT 1 FROM users WHERE is_admin = TRUE
) AS "exists";

-- name: PromoteUserToAdmin :exec
UPDATE users SET is_admin = TRUE WHERE id = $1;

-- name: ListUsers :many
SELECT id, email, name, is_admin, created_at
FROM users
WHERE archived_at IS NULL
ORDER BY created_at;

-- name: ListUsersIncludingArchived :many
SELECT id, email, name, is_admin, created_at, archived_at
FROM users
ORDER BY created_at;

-- name: GetUserByEmailPublic :one
SELECT id, email, name, is_admin, created_at, archived_at
FROM users WHERE email = $1;

-- name: UpdateUserName :one
UPDATE users SET name = $2 WHERE id = $1
RETURNING id, email, name, is_admin, created_at, archived_at;

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
```

Note: `GetUserByEmailPublic` and `UpdateUserName` now return `archived_at` so callers can populate `userResponse.ArchivedAt` correctly. `GetUserByEmail` (full row) is unchanged — `store.User` already exposes `ArchivedAt` after sqlc regenerates.

- [ ] **Step 2: Run `make generate`**

Run: `make generate`
Expected: `internal/store/users.sql.go` and `internal/store/models.go` are updated. No errors. New methods exist: `q.ArchiveUser`, `q.UnarchiveUser`, `q.CountActiveAdmins`, `q.DeleteUserAPITokens`, `q.ListUsersIncludingArchived`. `store.User` has an `ArchivedAt pgtype.Timestamptz` field. `store.ListUsersRow` is unchanged shape (no archived_at field). `store.UpdateUserNameRow` and `store.GetUserByEmailPublicRow` now include `ArchivedAt`.

- [ ] **Step 3: Verify the build still succeeds**

The userResponse helper hasn't been updated yet, so call sites that pass `UpdateUserNameRow.CreatedAt` (a `pgtype.Timestamptz`) into a function that expects exactly five args will still compile — sqlc only adds a field, it doesn't change positional ordering at call sites. Confirm:

Run: `make build`
Expected: builds cleanly.

- [ ] **Step 4: Commit**

```bash
git add internal/store/query/users.sql internal/store/users.sql.go internal/store/models.go
git commit -m "feat(store): add archive/unarchive queries and active-admin count"
```

---

## Task 3: Refactor `userResponse` to include `ArchivedAt`

Adds the field and threads it through every existing call site. This is a structural change with no behavior change yet — guards against the easy mistake of forgetting one of the four current callers.

**Files:**
- Modify: `internal/api/users.go`

- [ ] **Step 1: Update `userResponse` and `toUserResponse`**

Edit `internal/api/users.go`. Replace the existing `userResponse` struct and `toUserResponse` function (lines 21–37 of the current file) with:

```go
type userResponse struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	IsAdmin    bool       `json:"is_admin"`
	CreatedAt  time.Time  `json:"created_at"`
	ArchivedAt *time.Time `json:"archived_at"`
}

func toUserResponse(id pgtype.UUID, email, name string, isAdmin bool, createdAt, archivedAt pgtype.Timestamptz) userResponse {
	var arch *time.Time
	if archivedAt.Valid {
		t := archivedAt.Time
		arch = &t
	}
	return userResponse{
		ID:         uuidStr(id),
		Email:      email,
		Name:       name,
		IsAdmin:    isAdmin,
		CreatedAt:  createdAt.Time,
		ArchivedAt: arch,
	}
}
```

- [ ] **Step 2: Update every call site of `toUserResponse` in `users.go`**

Find each call (there are four: `handleListUsers` email branch + list loop, `handleUpdateMe`, `handleAdminUpdateUser`, `handleAdminCreateUser`) and add the appropriate sixth argument.

For `handleListUsers` — both the `?email=` branch and the list loop — `q.GetUserByEmailPublic` and `q.ListUsers` no longer match. The `?email=` branch gets `archived_at` from the regenerated `GetUserByEmailPublicRow`. The list loop iterates `ListUsersRow` which does **not** include `archived_at`; pass `pgtype.Timestamptz{}` (the zero value, `Valid: false`).

```go
// in handleListUsers, ?email= branch (replaces existing toUserResponse call):
writeJSON(w, http.StatusOK, []userResponse{
    toUserResponse(u.ID, u.Email, u.Name, u.IsAdmin, u.CreatedAt, u.ArchivedAt),
})

// in handleListUsers, list loop (replaces existing toUserResponse call):
out = append(out, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, pgtype.Timestamptz{}))
```

For `handleUpdateMe` and `handleAdminUpdateUser`, `q.UpdateUserName` now returns `ArchivedAt` (from Task 2). Update the calls:

```go
// handleUpdateMe (replaces existing toUserResponse call):
writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, row.ArchivedAt))

// handleAdminUpdateUser (replaces existing toUserResponse call):
writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, row.ArchivedAt))
```

For `handleAdminCreateUser`:

```go
writeJSON(w, http.StatusCreated, toUserResponse(user.ID, user.Email, user.Name, user.IsAdmin, user.CreatedAt, user.ArchivedAt))
```

- [ ] **Step 3: Build to confirm all call sites updated**

Run: `make build`
Expected: builds cleanly. If a call site was missed, the compiler will fail at that line — fix and rebuild.

- [ ] **Step 4: Run unit tests**

Run: `make test`
Expected: all passing. `userResponse` JSON now contains `"archived_at": null` for all existing callers (since none of those flows produce an archived user). If any test asserted a strict struct equality and broke, update it to expect the new field as `null`.

- [ ] **Step 5: Commit**

```bash
git add internal/api/users.go
git commit -m "refactor(api): include archived_at in userResponse"
```

---

## Task 4: Archive endpoint — tests + handler + route

TDD: write all integration test cases first, watch them fail, then implement the handler, then watch them pass.

**Files:**
- Modify: `internal/api/users.go` (new handler)
- Modify: `internal/api/server.go` (route registration)
- Modify: `internal/api/users_integration_test.go` (new tests)

- [ ] **Step 1: Add a `seedAdmin` helper if it doesn't already exist**

Check `internal/api/users_integration_test.go` and `password_reset_integration_test.go` for an existing helper that creates an admin user *without* logging in (we need this for the last-admin guard test where we want two admins).

If no such helper exists, add this near `seedUser` in `internal/api/users_integration_test.go`:

```go
// seedAdmin creates an admin user directly via the store. Does not log in.
func seedAdmin(t *testing.T, q *store.Queries, email, name string) store.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("placeholder"), bcrypt.MinCost)
	require.NoError(t, err)
	u, err := q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name: name, Email: email, IsAdmin: true, PasswordHash: string(hash),
	})
	require.NoError(t, err)
	return u
}

// archiveUser sends POST /v1/users/{id}/archive as the given admin. Returns
// the response code and decoded body (object on error or success).
func archiveUser(t *testing.T, srv *api.Server, token, userID string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/users/"+userID+"/archive", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// countAPITokens returns the number of api_tokens rows for a user — used
// to verify the cascade in archive.
func countAPITokens(t *testing.T, pool *pgxpool.Pool, userID pgtype.UUID) int {
	t.Helper()
	var n int
	err := pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM api_tokens WHERE user_id = $1`, userID).Scan(&n)
	require.NoError(t, err)
	return n
}
```

You will need an additional import of `github.com/jackc/pgx/v5/pgxpool` if not already present in the test file. Add it to the import block.

- [ ] **Step 2: Write failing integration tests for archive**

Append to `internal/api/users_integration_test.go`:

```go
func TestArchiveUser_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	// Create alice with a known password and log her in (creates+logs in in
	// one shot — do NOT seedUser separately or you'll get a unique-violation
	// on email).
	aliceToken := createAndLoginUser(t, srv, q, "alice@test.com", "alicepass")
	target, err := q.GetUserByEmail(t.Context(), "alice@test.com")
	require.NoError(t, err)

	code, body := archiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusOK, code)
	require.NotNil(t, body["archived_at"])

	// Pre-existing alice token must now be rejected.
	req := httptest.NewRequest("GET", "/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Login attempt now returns generic 401.
	loginBody, _ := json.Marshal(map[string]string{"email": "alice@test.com", "password": "alicepass"})
	req = httptest.NewRequest("POST", "/v1/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// api_tokens cascade.
	assert.Equal(t, 0, countAPITokens(t, pool, target.ID))
}

func TestArchiveUser_SelfArchiveForbidden(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	admin, err := q.GetUserByEmail(t.Context(), "admin@test.com")
	require.NoError(t, err)

	code, body := archiveUser(t, srv, adminToken, uuidStrTest(admin.ID))
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "cannot archive yourself", body["error"])
}

// TestArchiveUser_LastAdminGuard exercises the last-admin guard by simulating
// a race: admin A authenticates, then A's row is archived externally (e.g.,
// by another admin in a parallel request) before A's archive call reaches the
// guard. BearerAuth does not filter on archived_at (see middleware.go), so
// A's still-valid token passes auth; the guard then catches the inconsistent
// state when A tries to archive admin B (the only remaining active admin).
func TestArchiveUser_LastAdminGuard(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminAToken := loginAsAdmin(t, srv, q, "admin-a@test.com", "passa")
	adminB := seedAdmin(t, q, "admin-b@test.com", "Admin B")

	// Simulate the race: archive A directly in the DB, leaving B as the only
	// active admin. A's token is still valid because we used direct SQL.
	_, err := pool.Exec(t.Context(),
		`UPDATE users SET archived_at = NOW() WHERE email = 'admin-a@test.com'`)
	require.NoError(t, err)

	// A (using their still-valid token) tries to archive B. CountActiveAdmins
	// returns 1 (just B). Guard fires.
	code, body := archiveUser(t, srv, adminAToken, uuidStrTest(adminB.ID))
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "cannot archive the last active admin", body["error"])
}

func TestCountActiveAdmins(t *testing.T) {
	q := newTestQueries(t)

	// 0 admins.
	n, err := q.CountActiveAdmins(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	// Seed two admins, count = 2.
	_ = seedAdmin(t, q, "a@test.com", "A")
	_ = seedAdmin(t, q, "b@test.com", "B")
	n, err = q.CountActiveAdmins(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	// Archive one, count = 1.
	_, err = q.ArchiveUser(t.Context(), mustGetUser(t, q, "b@test.com").ID)
	require.NoError(t, err)
	n, err = q.CountActiveAdmins(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

func TestArchiveUser_AlreadyArchived(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	target := seedUser(t, q, "alice@test.com", "Alice")

	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusOK, code)

	code, body := archiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "user is already archived", body["error"])
}

func TestArchiveUser_NotFound(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	code, body := archiveUser(t, srv, adminToken, "00000000-0000-0000-0000-000000000000")
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "user not found", body["error"])
}

func TestArchiveUser_InvalidUUID(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	code, body := archiveUser(t, srv, adminToken, "not-a-uuid")
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "invalid user id", body["error"])
}

// uuidStrTest is a tiny helper duplicating the package-private uuidStr so the
// _test package can stringify pgtype.UUID without exporting the helper.
func uuidStrTest(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// createAndLoginUser seeds a non-admin user with the given password, then
// logs in and returns the token. Mirrors loginAsAdmin but without IsAdmin.
// IMPORTANT: do not call seedUser separately for the same email — this
// helper already creates the row.
func createAndLoginUser(t *testing.T, srv *api.Server, q *store.Queries, email, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	_, err = q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name: email, Email: email, IsAdmin: false, PasswordHash: string(hash),
	})
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest("POST", "/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp["token"].(string)
}

// mustGetUser is a small helper for store-level tests.
func mustGetUser(t *testing.T, q *store.Queries, email string) store.User {
	t.Helper()
	u, err := q.GetUserByEmail(t.Context(), email)
	require.NoError(t, err)
	return u
}
```

You will also need to add `fmt` to the imports if not already present.

- [ ] **Step 3: Run the new tests — expect failures**

Run: `go test -tags integration -p 1 ./internal/api/... -run 'TestArchiveUser|TestCountActiveAdmins' -v -timeout 120s`

Expected: all `TestArchiveUser_*` tests fail with `404 page not found` or similar (route not registered). `TestCountActiveAdmins` should pass (the query exists from Task 2).

- [ ] **Step 4: Implement `handleAdminArchiveUser`**

Append to `internal/api/users.go`:

```go
func (s *Server) handleAdminArchiveUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	authUser, _ := UserFromCtx(r.Context())
	if authUser.ID == id {
		writeError(w, http.StatusBadRequest, "cannot archive yourself")
		return
	}

	ctx := r.Context()

	target, err := s.q.GetUser(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to look up user")
		}
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	txq := s.q.WithTx(tx)

	if target.IsAdmin && !target.ArchivedAt.Valid {
		n, err := txq.CountActiveAdmins(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to count admins")
			return
		}
		if n <= 1 {
			writeError(w, http.StatusBadRequest, "cannot archive the last active admin")
			return
		}
	}

	row, err := txq.ArchiveUser(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "user is already archived")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to archive user")
		}
		return
	}

	if _, err := txq.DeleteUserAPITokens(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke tokens")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit archive")
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, row.ArchivedAt))
}
```

- [ ] **Step 5: Register the route in `internal/api/server.go`**

In `Handler()`, after the existing `/v1/users/{id}` PATCH registration (line 124), insert:

```go
mux.Handle("POST /v1/users/{id}/archive", auth(admin(http.HandlerFunc(s.handleAdminArchiveUser))))
```

- [ ] **Step 6: Run the tests — expect passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run 'TestArchiveUser|TestCountActiveAdmins' -v -timeout 120s`

Expected: all pass except `TestArchiveUser_LastAdminGuard` which is skipped per its `t.Skip(...)` comment.

- [ ] **Step 7: Commit**

```bash
git add internal/api/users.go internal/api/server.go internal/api/users_integration_test.go
git commit -m "feat(api): admin archive user endpoint with token revocation"
```

---

## Task 5: Unarchive endpoint — tests + handler + route

Same TDD pattern: tests, fail, implement, pass, commit.

**Files:**
- Modify: `internal/api/users.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/users_integration_test.go`

- [ ] **Step 1: Add `unarchiveUser` helper near the existing `archiveUser` helper**

Append to the helpers section of `internal/api/users_integration_test.go`:

```go
func unarchiveUser(t *testing.T, srv *api.Server, token, userID string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/users/"+userID+"/unarchive", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}
```

- [ ] **Step 2: Write failing integration tests**

Append to `internal/api/users_integration_test.go`:

```go
func TestUnarchiveUser_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	aliceToken := createAndLoginUser(t, srv, q, "alice@test.com", "alicepass")
	target, err := q.GetUserByEmail(t.Context(), "alice@test.com")
	require.NoError(t, err)

	// Archive, then unarchive.
	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusOK, code)

	code, body := unarchiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusOK, code)
	assert.Nil(t, body["archived_at"])

	// Old token still revoked.
	req := httptest.NewRequest("GET", "/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// New login succeeds.
	loginBody, _ := json.Marshal(map[string]string{"email": "alice@test.com", "password": "alicepass"})
	req = httptest.NewRequest("POST", "/v1/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestUnarchiveUser_NotArchived(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	target := seedUser(t, q, "alice@test.com", "Alice")

	code, body := unarchiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "user is not archived", body["error"])
}

func TestUnarchiveUser_NotFound(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	code, body := unarchiveUser(t, srv, adminToken, "00000000-0000-0000-0000-000000000000")
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "user not found", body["error"])
}
```

- [ ] **Step 3: Run tests — expect failures**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestUnarchiveUser -v -timeout 120s`

Expected: 404 from the mux on every call (route missing).

- [ ] **Step 4: Implement `handleAdminUnarchiveUser`**

Append to `internal/api/users.go`:

```go
func (s *Server) handleAdminUnarchiveUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	authUser, _ := UserFromCtx(r.Context())
	if authUser.ID == id {
		writeError(w, http.StatusBadRequest, "cannot unarchive yourself")
		return
	}

	ctx := r.Context()

	if _, err := s.q.GetUser(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to look up user")
		}
		return
	}

	row, err := s.q.UnarchiveUser(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "user is not archived")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to unarchive user")
		}
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, row.ArchivedAt))
}
```

- [ ] **Step 5: Register the route in `internal/api/server.go`**

Right below the archive route registered in Task 4:

```go
mux.Handle("POST /v1/users/{id}/unarchive", auth(admin(http.HandlerFunc(s.handleAdminUnarchiveUser))))
```

- [ ] **Step 6: Run tests — expect passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestUnarchiveUser -v -timeout 120s`
Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/api/users.go internal/api/server.go internal/api/users_integration_test.go
git commit -m "feat(api): admin unarchive user endpoint"
```

---

## Task 6: List endpoint — `?include_archived` filter

**Files:**
- Modify: `internal/api/users.go`
- Modify: `internal/api/users_integration_test.go`

- [ ] **Step 1: Write failing list-filter tests**

Append to `internal/api/users_integration_test.go`:

```go
func TestListUsers_FiltersArchivedByDefault(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	alice := seedUser(t, q, "alice@test.com", "Alice")
	_ = seedUser(t, q, "bob@test.com", "Bob")

	// Archive alice.
	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(alice.ID))
	require.Equal(t, http.StatusOK, code)

	// Default list excludes alice.
	code, users, _ := getUsers(t, srv, adminToken, "")
	require.Equal(t, http.StatusOK, code)
	emails := emailSet(users)
	assert.NotContains(t, emails, "alice@test.com")
	assert.Contains(t, emails, "bob@test.com")
	assert.Contains(t, emails, "admin@test.com")
}

func TestListUsers_IncludeArchived(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	alice := seedUser(t, q, "alice@test.com", "Alice")
	_ = seedUser(t, q, "bob@test.com", "Bob")

	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(alice.ID))
	require.Equal(t, http.StatusOK, code)

	code, users, _ := getUsers(t, srv, adminToken, "include_archived=true")
	require.Equal(t, http.StatusOK, code)
	emails := emailSet(users)
	assert.Contains(t, emails, "alice@test.com")
	assert.Contains(t, emails, "bob@test.com")

	// And the archived row carries archived_at.
	for _, u := range users {
		if u["email"] == "alice@test.com" {
			assert.NotNil(t, u["archived_at"])
		} else {
			assert.Nil(t, u["archived_at"])
		}
	}
}

func TestListUsers_EmailLookupHidesArchived(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	alice := seedUser(t, q, "alice@test.com", "Alice")
	_, _ = archiveUser(t, srv, adminToken, uuidStrTest(alice.ID))

	// Without include_archived, lookup returns [].
	code, users, _ := getUsers(t, srv, adminToken, "email=alice%40test.com")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, users, 0)

	// With include_archived, lookup returns the archived user.
	code, users, _ = getUsers(t, srv, adminToken, "email=alice%40test.com&include_archived=true")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, users, 1)
	assert.Equal(t, "alice@test.com", users[0]["email"])
	assert.NotNil(t, users[0]["archived_at"])
}

// emailSet extracts the email field from a list of decoded user rows.
func emailSet(users []map[string]any) map[string]bool {
	out := make(map[string]bool, len(users))
	for _, u := range users {
		out[u["email"].(string)] = true
	}
	return out
}
```

- [ ] **Step 2: Run tests — expect failures**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListUsers -v -timeout 120s`

Expected: `TestListUsers_AdminSeesAll` (existing) still passes. The three new tests will fail because:
- `TestListUsers_FiltersArchivedByDefault` may pass already (Task 2's `WHERE archived_at IS NULL` on `ListUsers` makes this work).
- `TestListUsers_IncludeArchived` fails because the handler doesn't read the query param yet.
- `TestListUsers_EmailLookupHidesArchived` fails likewise for the email branch.

- [ ] **Step 3: Update `handleListUsers`**

Replace the existing `handleListUsers` body in `internal/api/users.go` with:

```go
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	includeArchived := r.URL.Query().Get("include_archived") == "true"

	if email := r.URL.Query().Get("email"); email != "" {
		u, err := s.q.GetUserByEmailPublic(r.Context(), email)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusOK, []userResponse{})
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to look up user")
			return
		}
		if u.ArchivedAt.Valid && !includeArchived {
			writeJSON(w, http.StatusOK, []userResponse{})
			return
		}
		writeJSON(w, http.StatusOK, []userResponse{
			toUserResponse(u.ID, u.Email, u.Name, u.IsAdmin, u.CreatedAt, u.ArchivedAt),
		})
		return
	}

	if includeArchived {
		rows, err := s.q.ListUsersIncludingArchived(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return
		}
		out := make([]userResponse, 0, len(rows))
		for _, row := range rows {
			out = append(out, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, row.ArchivedAt))
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	rows, err := s.q.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	out := make([]userResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, pgtype.Timestamptz{}))
	}
	writeJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 4: Run tests — expect passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListUsers -v -timeout 120s`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/api/users.go internal/api/users_integration_test.go
git commit -m "feat(api): include_archived filter for list and email lookup"
```

---

## Task 7: `handleLogin` rejects archived users with timing parity

**Files:**
- Modify: `internal/api/auth.go`
- Modify: `internal/api/auth_integration_test.go`

- [ ] **Step 1: Write failing login-parity tests**

Append to `internal/api/auth_integration_test.go`:

```go
func TestLogin_ArchivedUserRejectedGenericMessage(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	alice := seedUser(t, q, "alice@test.com", "Alice")

	// Set alice's password so we can attempt login with the correct one.
	hash, err := bcrypt.GenerateFromPassword([]byte("alicepass"), bcrypt.MinCost)
	require.NoError(t, err)
	require.NoError(t, q.SetPasswordHash(t.Context(), store.SetPasswordHashParams{
		ID: alice.ID, PasswordHash: string(hash),
	}))

	// Archive alice.
	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(alice.ID))
	require.Equal(t, http.StatusOK, code)

	tries := []struct {
		email, password string
	}{
		{"alice@test.com", "alicepass"},   // archived + correct password
		{"alice@test.com", "wrongpass"},   // archived + wrong password
		{"nobody@test.com", "anything"},   // unknown email
		{"admin@test.com", "wrongpass"},   // active + wrong password
	}
	for _, tt := range tries {
		body, _ := json.Marshal(map[string]string{"email": tt.email, "password": tt.password})
		req := httptest.NewRequest("POST", "/v1/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "case %+v", tt)
		var resp map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		assert.Equal(t, "invalid email or password", resp["error"], "case %+v", tt)
	}
}
```

(Note: existing `auth_integration_test.go` already imports `bytes`, `encoding/json`, etc. If not, add them.)

- [ ] **Step 2: Run test — expect failure on the archived+correct-password case**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestLogin_ArchivedUserRejectedGenericMessage -v -timeout 120s`

Expected: the archived+correct-password case returns `201 Created` (because the bcrypt compare succeeds and `handleLogin` issues a token). Test fails on that assertion.

- [ ] **Step 3: Add the archived check in `handleLogin`**

In `internal/api/auth.go`, modify `handleLogin` (the function starting around line 232). After the existing block:

```go
bcryptErr := bcrypt.CompareHashAndPassword(hashToCompare, []byte(req.Password))
if bcryptErr != nil || errors.Is(err, pgx.ErrNoRows) {
    writeError(w, http.StatusUnauthorized, "invalid email or password")
    return
}
```

…and **before** the `s.issueToken(...)` call, insert:

```go
if user.ArchivedAt.Valid {
    writeError(w, http.StatusUnauthorized, "invalid email or password")
    return
}
```

The placement is load-bearing: bcrypt has already run for both active and archived users, so the response time is dominated by bcrypt and doesn't leak the user's archive status.

- [ ] **Step 4: Run tests — expect passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestLogin_ArchivedUserRejectedGenericMessage -v -timeout 120s`
Expected: pass.

- [ ] **Step 5: Run the full integration suite to make sure nothing else broke**

Run: `make test-integration`
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/api/auth.go internal/api/auth_integration_test.go
git commit -m "feat(api): reject login for archived users with timing parity"
```

---

## Task 8: CLI `archive` and `unarchive` subcommands

**Files:**
- Modify: `internal/cli/admin_users.go`
- Modify: `internal/cli/admin_users_test.go`

- [ ] **Step 1: Add `Archived` to `userListItem` and update `printUserDetail`**

In `internal/cli/admin_users.go`, replace the existing `userListItem` struct (lines 32–38) with:

```go
type userListItem struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	IsAdmin    bool       `json:"is_admin"`
	CreatedAt  time.Time  `json:"created_at"`
	ArchivedAt *time.Time `json:"archived_at"`
}
```

Update `printUserDetail` (currently around line 93) to add an `Archived:` line after `Created:`:

```go
func printUserDetail(out io.Writer, u userListItem) {
	admin := "no"
	if u.IsAdmin {
		admin = "yes"
	}
	fmt.Fprintf(out, "ID:       %s\n", u.ID)
	fmt.Fprintf(out, "Email:    %s\n", u.Email)
	fmt.Fprintf(out, "Name:     %s\n", u.Name)
	fmt.Fprintf(out, "Admin:    %s\n", admin)
	fmt.Fprintf(out, "Created:  %s\n", u.CreatedAt.Format(time.RFC3339))
	archived := "no"
	if u.ArchivedAt != nil {
		archived = u.ArchivedAt.Format(time.RFC3339)
	}
	fmt.Fprintf(out, "Archived: %s\n", archived)
}
```

- [ ] **Step 2: Wire `archive` and `unarchive` into `doAdminUsers` switch**

Replace the switch in `doAdminUsers` (lines 18–29) with:

```go
switch args[0] {
case "list":
    return doAdminUsersList(ctx, cfg, args[1:], out)
case "get":
    return doAdminUsersGet(ctx, cfg, args[1:], out)
case "create":
    return doAdminUsersCreate(ctx, cfg, args[1:], out)
case "update":
    return doAdminUsersUpdate(ctx, cfg, args[1:], out)
case "archive":
    return doAdminUsersArchive(ctx, cfg, args[1:], out)
case "unarchive":
    return doAdminUsersUnarchive(ctx, cfg, args[1:], out)
default:
    return fmt.Errorf("unknown admin users subcommand: %s", args[0])
}
```

Update the usage string at line 16 to include the new verbs:

```go
return fmt.Errorf("usage: relay admin users <list|get|create|update|archive|unarchive> [args]")
```

- [ ] **Step 3: Implement the two new functions**

Append to `internal/cli/admin_users.go`:

```go
func doAdminUsersArchive(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	return doAdminUsersArchiveAction(ctx, cfg, args, out, "archive")
}

func doAdminUsersUnarchive(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	return doAdminUsersArchiveAction(ctx, cfg, args, out, "unarchive")
}

// doAdminUsersArchiveAction implements both archive and unarchive — they share
// the same shape: resolve target email-or-id, then POST to the corresponding
// action endpoint.
func doAdminUsersArchiveAction(ctx context.Context, cfg *Config, args []string, out io.Writer, action string) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: relay admin users %s <email-or-id>", action)
	}
	target := args[0]

	c := cfg.NewClient()

	id := target
	if !looksLikeUUID(target) {
		var users []userListItem
		path := "/v1/users?email=" + url.QueryEscape(target) + "&include_archived=true"
		if err := c.do(ctx, "GET", path, nil, &users); err != nil {
			return fmt.Errorf("look up user: %w", err)
		}
		if len(users) == 0 {
			return fmt.Errorf("user not found: %s", target)
		}
		id = users[0].ID
	}

	var u userListItem
	if err := c.do(ctx, "POST", "/v1/users/"+id+"/"+action, nil, &u); err != nil {
		return fmt.Errorf("%s user: %w", action, err)
	}
	printUserDetail(out, u)
	return nil
}
```

The `&include_archived=true` on the lookup is intentional: an admin running `relay admin users unarchive bob@example.com` needs to find bob even though bob is archived. For `archive`, including archived users in the lookup is harmless (the server still rejects re-archive with 409).

- [ ] **Step 4: Write CLI unit tests**

Append to `internal/cli/admin_users_test.go`:

```go
func TestAdminUsersArchive_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path+"?"+r.URL.RawQuery
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/users":
			// email lookup
			_, _ = w.Write([]byte(`[{"id":"00000000-0000-0000-0000-000000000001","email":"alice@test.com","name":"Alice","is_admin":false,"created_at":"2026-01-01T00:00:00Z","archived_at":null}]`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/archive"):
			_, _ = w.Write([]byte(`{"id":"00000000-0000-0000-0000-000000000001","email":"alice@test.com","name":"Alice","is_admin":false,"created_at":"2026-01-01T00:00:00Z","archived_at":"2026-05-05T12:00:00Z"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "t"}
	out := &bytes.Buffer{}
	err := doAdminUsers(context.Background(), cfg, []string{"archive", "alice@test.com"}, out)
	require.NoError(t, err)
	assert.Equal(t, "POST", gotMethod)
	assert.Contains(t, gotPath, "/archive")
	assert.Contains(t, out.String(), "Archived: 2026-05-05T12:00:00Z")
}

func TestAdminUsersUnarchive_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/users":
			_, _ = w.Write([]byte(`[{"id":"00000000-0000-0000-0000-000000000001","email":"alice@test.com","name":"Alice","is_admin":false,"created_at":"2026-01-01T00:00:00Z","archived_at":"2026-05-05T12:00:00Z"}]`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/unarchive"):
			_, _ = w.Write([]byte(`{"id":"00000000-0000-0000-0000-000000000001","email":"alice@test.com","name":"Alice","is_admin":false,"created_at":"2026-01-01T00:00:00Z","archived_at":null}`))
		default:
			t.Fatalf("unexpected: %s %s", r.Method, r.URL)
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "t"}
	out := &bytes.Buffer{}
	err := doAdminUsers(context.Background(), cfg, []string{"unarchive", "alice@test.com"}, out)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Archived: no")
}

func TestAdminUsersArchive_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://unused", Token: ""}
	err := doAdminUsers(context.Background(), cfg, []string{"archive", "alice@test.com"}, &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not logged in")
}

func TestAdminUsersArchive_MissingArg(t *testing.T) {
	cfg := &Config{ServerURL: "http://unused", Token: "t"}
	err := doAdminUsers(context.Background(), cfg, []string{"archive"}, &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage:")
}
```

(Adjust imports: `bytes`, `context`, `net/http`, `net/http/httptest`, `strings`, `testing`, plus the existing testify ones, should all already be present from existing tests.)

- [ ] **Step 5: Run tests**

Run: `make test`
Expected: all pass, including the four new CLI tests.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/admin_users.go internal/cli/admin_users_test.go
git commit -m "feat(cli): admin users archive/unarchive subcommands"
```

---

## Task 9: CLI `--include-archived` flag on `list`

**Files:**
- Modify: `internal/cli/admin_users.go`
- Modify: `internal/cli/admin_users_test.go`

- [ ] **Step 1: Add the flag and pass-through in `doAdminUsersList`**

Replace the existing `doAdminUsersList` body (lines 40–55) with:

```go
func doAdminUsersList(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	fs := flag.NewFlagSet("admin users list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	includeArchived := fs.Bool("include-archived", false, "include archived users in the list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: relay admin users list [--include-archived]")
	}

	c := cfg.NewClient()
	path := "/v1/users"
	if *includeArchived {
		path += "?include_archived=true"
	}
	var users []userListItem
	if err := c.do(ctx, "GET", path, nil, &users); err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	printUsersTable(out, users, *includeArchived)
	return nil
}
```

- [ ] **Step 2: Update `printUsersTable` to optionally render the `ARCHIVED` column**

Replace the existing `printUsersTable` body (lines 57–69):

```go
func printUsersTable(out io.Writer, users []userListItem, includeArchived bool) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if includeArchived {
		fmt.Fprintln(tw, "ID\tEMAIL\tNAME\tADMIN\tCREATED\tARCHIVED")
	} else {
		fmt.Fprintln(tw, "ID\tEMAIL\tNAME\tADMIN\tCREATED")
	}
	for _, u := range users {
		admin := "no"
		if u.IsAdmin {
			admin = "yes"
		}
		if includeArchived {
			archived := "no"
			if u.ArchivedAt != nil {
				archived = u.ArchivedAt.Format("2006-01-02")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				u.ID, u.Email, u.Name, admin, u.CreatedAt.Format("2006-01-02"), archived)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				u.ID, u.Email, u.Name, admin, u.CreatedAt.Format("2006-01-02"))
		}
	}
	_ = tw.Flush()
}
```

- [ ] **Step 3: Write the test**

Append to `internal/cli/admin_users_test.go`:

```go
func TestAdminUsersList_IncludeArchived(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[
			{"id":"00000000-0000-0000-0000-000000000001","email":"alice@test.com","name":"A","is_admin":false,"created_at":"2026-01-01T00:00:00Z","archived_at":null},
			{"id":"00000000-0000-0000-0000-000000000002","email":"bob@test.com","name":"B","is_admin":false,"created_at":"2026-01-02T00:00:00Z","archived_at":"2026-05-05T12:00:00Z"}
		]`))
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "t"}
	out := &bytes.Buffer{}
	err := doAdminUsers(context.Background(), cfg, []string{"list", "--include-archived"}, out)
	require.NoError(t, err)
	assert.Equal(t, "include_archived=true", gotQuery)
	assert.Contains(t, out.String(), "ARCHIVED")
	assert.Contains(t, out.String(), "2026-05-05")
}
```

- [ ] **Step 4: Run tests**

Run: `make test`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/admin_users.go internal/cli/admin_users_test.go
git commit -m "feat(cli): admin users list --include-archived"
```

---

## Task 10: README updates

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the REST endpoint table**

Open `README.md`, locate the table of REST endpoints under user management. Add two new rows after the row for `PATCH /v1/users/{id}` (preserve the existing table format — replicate the surrounding column widths/separators):

```
| POST /v1/users/{id}/archive   | admin | Archive (soft-delete) a user. Revokes all of their API tokens. |
| POST /v1/users/{id}/unarchive | admin | Restore an archived user. Old tokens stay revoked.             |
```

Update the `GET /v1/users` row's description to mention `?include_archived=true` if not already.

- [ ] **Step 2: Update the CLI reference table**

In the CLI section of the README, add rows after `relay admin users update`:

```
| relay admin users archive <email-or-id>   | Soft-delete a user (admin).        |
| relay admin users unarchive <email-or-id> | Restore an archived user (admin).  |
```

And update the `relay admin users list` row to mention the new flag:

```
| relay admin users list [--include-archived] | List users; opt-in flag includes archived rows. |
```

- [ ] **Step 3: Verify the markdown renders cleanly**

Run: `git diff README.md`
Confirm visually that table alignment and section structure are preserved. Run any markdown linter the repo uses; if none is configured, skip.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: document user archive endpoints and CLI commands"
```

---

## Task 11: Final verification

- [ ] **Step 1: Full unit test suite**

Run: `make test`
Expected: pass.

- [ ] **Step 2: Full integration test suite**

Run: `make test-integration`
Expected: pass. This includes every test added across Tasks 4–7 plus the existing suite.

- [ ] **Step 3: Build all three binaries**

Run: `make build`
Expected: `bin/relay-server`, `bin/relay-agent`, `bin/relay` produced.

- [ ] **Step 4: Manual smoke test**

In one terminal:

```bash
./bin/relay-server
```

In another:

```bash
# Login as admin (assumes bootstrap admin credentials are seeded — adjust to env)
./bin/relay login --server http://localhost:8080
# Create a test user (interactive password prompt)
./bin/relay admin users create --email smoke@test.com --name Smoke
# Archive them
./bin/relay admin users archive smoke@test.com
# Verify list filters them out
./bin/relay admin users list
# And the flag shows them
./bin/relay admin users list --include-archived
# Try to log in as them — should fail
./bin/relay login --email smoke@test.com   # expect failure
# Unarchive
./bin/relay admin users unarchive smoke@test.com
# Now login works
./bin/relay login --email smoke@test.com   # expect success
```

If any step misbehaves, capture the error and revisit the corresponding task.

- [ ] **Step 5: No commit needed**

This is a verification-only step. The branch is now ready for review.

---

## Out of scope (deferred)

The spec lists future work that is **not** part of this plan:

- Hard delete escape hatch (`DELETE /v1/users/{id}` for users with no history).
- Email reuse on archive (would add an `archived_email` column).
- Bulk archive.
- Audit log of archive actions.

If any of these are requested as in-scope mid-implementation, halt and re-spec.
