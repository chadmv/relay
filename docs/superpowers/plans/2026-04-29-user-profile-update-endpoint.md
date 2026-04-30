# User Profile Update Endpoint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `PATCH /v1/users/me` (any user) and `PATCH /v1/users/{id}` (admin) for updating display names, plus a `GetUserByEmailPublic` cleanup that drops `password_hash` from the email-filter path of `GET /v1/users`. Add matching CLI commands.

**Architecture:** Two new sqlc queries (one public-shape email lookup, one name update with `RETURNING`). Two new HTTP handlers in `internal/api/users.go` reusing a shared `parseUpdateUserRequest` validator and a shared `toUserResponse` mapping helper. New `relay profile update --name "<name>"` CLI subcommand and a parallel `relay admin users update <email-or-id> --name "<name>"`. Both CLI commands resolve to the same JSON shape returned by the handlers and use the existing `printUserDetail` helper (extracted from `doAdminUsersGet` in this plan).

**Tech Stack:** Go 1.22+, pgx/v5, sqlc, golang-migrate (no new migration), stdlib `net/http` + `flag`, `testify/require` + `testify/assert`.

**Spec:** [`docs/superpowers/specs/2026-04-29-user-profile-update-endpoint-design.md`](../specs/2026-04-29-user-profile-update-endpoint-design.md)

**Backlog items closed by this plan:**
- [`docs/backlog/idea-2026-04-27-user-profile-update-endpoint.md`](../../backlog/idea-2026-04-27-user-profile-update-endpoint.md)
- [`docs/backlog/idea-2026-04-27-getuserbyemail-fetches-password-hash.md`](../../backlog/idea-2026-04-27-getuserbyemail-fetches-password-hash.md)

---

## File map

| File | Action | Purpose |
|---|---|---|
| `internal/store/query/users.sql` | Modify | Add `GetUserByEmailPublic` and `UpdateUserName` queries |
| `internal/store/users.sql.go` | Regenerate (sqlc) | Generated row/param types — stage only this file from regen |
| `internal/api/users.go` | Modify | Add `updateUserRequest`, `parseUpdateUserRequest`, `toUserResponse`; swap `GetUserByEmail` → `GetUserByEmailPublic`; add `handleUpdateMe` and `handleAdminUpdateUser` |
| `internal/api/users_integration_test.go` | Modify | Add tests for both PATCH endpoints + regression check on the cleanup |
| `internal/api/server.go` | Modify | Register two new routes |
| `internal/cli/admin_users.go` | Modify | Extract `printUserDetail` helper; add `update` switch case + `doAdminUsersUpdate` |
| `internal/cli/admin_users_test.go` | Modify | Add tests for the new `update` subcommand |
| `internal/cli/profile.go` | Create | New `profile` top-level subcommand (`profile update`) |
| `internal/cli/profile_test.go` | Create | Unit tests for `profile update` |
| `cmd/relay/main.go` | Modify | Register `cli.ProfileCommand()` |
| `CLAUDE.md` | Modify | Document new endpoints + CLI commands |
| `docs/backlog/idea-2026-04-27-user-profile-update-endpoint.md` | Move → `docs/backlog/closed/` | Close item |
| `docs/backlog/idea-2026-04-27-getuserbyemail-fetches-password-hash.md` | Move → `docs/backlog/closed/` | Close item |

---

## Task 1: Add sqlc queries and regenerate

**Files:**
- Modify: `internal/store/query/users.sql`
- Regenerate: `internal/store/users.sql.go` (do not edit by hand)

- [ ] **Step 1: Append the two new queries to `internal/store/query/users.sql`**

Add at the end of the file:

```sql

-- name: GetUserByEmailPublic :one
SELECT id, email, name, is_admin, created_at
FROM users WHERE email = $1;

-- name: UpdateUserName :one
UPDATE users SET name = $2 WHERE id = $1
RETURNING id, email, name, is_admin, created_at;
```

- [ ] **Step 2: Run sqlc generation**

Run: `make generate`
Expected: completes without errors. `internal/store/users.sql.go` will be regenerated; other `*.sql.go` files may also be touched only because of LF/CRLF line-ending differences on Windows.

- [ ] **Step 3: Verify the generated symbols exist**

Run: `grep -E "GetUserByEmailPublic|UpdateUserName|UpdateUserNameParams|UpdateUserNameRow|GetUserByEmailPublicRow" internal/store/users.sql.go`

Expected output includes definitions for `GetUserByEmailPublic`, `UpdateUserName`, `UpdateUserNameParams`, `UpdateUserNameRow`, and `GetUserByEmailPublicRow`. Confirm the generated row types contain `Name`, `Email`, `IsAdmin`, `CreatedAt`, `ID` — and **do not** contain `PasswordHash`.

- [ ] **Step 4: Restore unrelated regen drift, then stage and commit**

Stage only the two intended files:

```bash
git add internal/store/query/users.sql internal/store/users.sql.go
git diff --stat --cached
```

Expected: only those two files appear in the diff.

If `git status` shows other `internal/store/*.sql.go` files modified (line-ending churn), restore them:

```bash
git checkout -- internal/store/
git status
```

(The two staged files remain in the index because `git checkout --` only touches the working tree of unstaged files.)

Commit:

```bash
git commit -m "$(cat <<'EOF'
store: add GetUserByEmailPublic and UpdateUserName queries

GetUserByEmailPublic returns the same five public columns as ListUsers
(no password_hash). UpdateUserName updates the name column and returns
the updated row in the same public shape.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Refactor `users.go` with shared helpers + apply the cleanup swap

**Files:**
- Modify: `internal/api/users.go`
- Modify: `internal/api/users_integration_test.go` (regression check on the cleanup)

- [ ] **Step 1: Write a failing regression test for the cleanup**

This test pins down that `GET /v1/users?email=...` keeps the existing public shape and never includes `password_hash` after the swap. The existing tests cover this behaviorally; add an explicit check on a hit case that there is no `password_hash` field even when only one match exists.

Open `internal/api/users_integration_test.go` and add this test near the bottom:

```go
func TestListUsers_FilterByEmailHit_NoPasswordHash(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	seedUser(t, q, "alice@test.com", "Alice")

	code, users, _ := getUsers(t, srv, adminToken, "email=alice@test.com")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, users, 1)
	_, hasHash := users[0]["password_hash"]
	assert.False(t, hasHash, "email-filter hit must not include password_hash")
	// Public columns still present.
	assert.Equal(t, "alice@test.com", users[0]["email"])
	assert.Equal(t, "Alice", users[0]["name"])
	assert.Equal(t, false, users[0]["is_admin"])
	assert.NotEmpty(t, users[0]["id"])
	assert.NotEmpty(t, users[0]["created_at"])
}
```

- [ ] **Step 2: Run the test to confirm it currently passes (the existing handler also avoids leaking it via the response struct)**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListUsers_FilterByEmailHit_NoPasswordHash -v -timeout 120s`
Expected: PASS. (This test is a regression guard; we want it green both before and after the swap.)

- [ ] **Step 3: Refactor `internal/api/users.go` — extract `toUserResponse`, add `parseUpdateUserRequest`, swap to `GetUserByEmailPublic`**

Replace the entire contents of `internal/api/users.go` with:

```go
package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// userResponse is the public shape returned by GET /v1/users and the PATCH
// endpoints. Defined as a private struct (not the store row) to guarantee the
// password hash never leaks even if a store row type changes.
type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserResponse(id pgtype.UUID, email, name string, isAdmin bool, createdAt pgtype.Timestamptz) userResponse {
	return userResponse{
		ID:        uuidStr(id),
		Email:     email,
		Name:      name,
		IsAdmin:   isAdmin,
		CreatedAt: createdAt.Time,
	}
}

// updateUserRequest is the request body for PATCH /v1/users/me and
// PATCH /v1/users/{id}.
type updateUserRequest struct {
	Name string `json:"name"`
}

// parseUpdateUserRequest reads and validates the JSON body. On failure it
// writes the appropriate error response and returns ok=false. On success it
// returns the trimmed name.
func parseUpdateUserRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req updateUserRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return "", false
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return "", false
	}
	return name, true
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusOK, []userResponse{
			toUserResponse(u.ID, u.Email, u.Name, u.IsAdmin, u.CreatedAt),
		})
		return
	}

	rows, err := s.q.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	out := make([]userResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt))
	}
	writeJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 4: Run the regression test to confirm it still passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListUsers -v -timeout 120s`
Expected: All `TestListUsers_*` tests pass, including the new `TestListUsers_FilterByEmailHit_NoPasswordHash`.

- [ ] **Step 5: Run the full unit test suite to confirm nothing else broke**

Run: `make test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/users.go internal/api/users_integration_test.go
git commit -m "$(cat <<'EOF'
api: swap GetUserByEmail to GetUserByEmailPublic on /v1/users?email path

Removes password_hash from the columns transferred from Postgres on the
public email-filter code path. Also extracts toUserResponse and adds
parseUpdateUserRequest helpers in preparation for PATCH endpoints.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Implement `PATCH /v1/users/me` (handler + route + tests)

**Files:**
- Modify: `internal/api/users.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/users_integration_test.go`

- [ ] **Step 1: Write the failing happy-path test**

Append to `internal/api/users_integration_test.go`:

```go
// patchJSON sends a PATCH with a JSON body and returns (status, parsedBody, errBody).
// parsedBody is non-nil when the response is a JSON object; errBody is non-nil
// when the response is an error envelope.
func patchJSON(t *testing.T, srv *api.Server, token, path string, body any) (int, map[string]any) {
	t.Helper()
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest("PATCH", path, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var obj map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &obj)
	return rec.Code, obj
}

func TestUpdateMe_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, body := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{"name": "New Name"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "user@test.com", body["email"])
	assert.Equal(t, "New Name", body["name"])
	assert.Equal(t, false, body["is_admin"])
	assert.NotEmpty(t, body["id"])
	assert.NotEmpty(t, body["created_at"])
	_, hasHash := body["password_hash"]
	assert.False(t, hasHash)
}
```

You will also need to add `"bytes"` to the import block at the top of the file if it isn't already there.

- [ ] **Step 2: Run the test — confirm it fails because the route doesn't exist**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestUpdateMe_HappyPath -v -timeout 120s`
Expected: FAIL. Status will likely be `404` because no route is registered for `PATCH /v1/users/me`.

- [ ] **Step 3: Implement `handleUpdateMe` in `internal/api/users.go`**

Append at the bottom of `internal/api/users.go`:

```go
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	authUser, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	name, ok := parseUpdateUserRequest(w, r)
	if !ok {
		return
	}

	row, err := s.q.UpdateUserName(r.Context(), store.UpdateUserNameParams{
		ID:   authUser.ID,
		Name: name,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt))
}
```

Add `"relay/internal/store"` to the import block at the top of `internal/api/users.go`.

- [ ] **Step 4: Register the route in `internal/api/server.go`**

In `internal/api/server.go`, locate the `// User management (admin-only)` block (around line 114) and replace it with:

```go
	// User management
	mux.Handle("GET /v1/users", auth(admin(http.HandlerFunc(s.handleListUsers))))
	mux.Handle("POST /v1/users/password-reset", auth(admin(http.HandlerFunc(s.handleAdminPasswordReset))))
	mux.Handle("PATCH /v1/users/me", auth(http.HandlerFunc(s.handleUpdateMe)))
```

- [ ] **Step 5: Run the test — confirm it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestUpdateMe_HappyPath -v -timeout 120s`
Expected: PASS.

- [ ] **Step 6: Add the remaining `/v1/users/me` tests**

Append to `internal/api/users_integration_test.go`:

```go
func TestUpdateMe_EmptyName(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, body := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{"name": ""})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body["error"], "name is required")
}

func TestUpdateMe_WhitespaceOnlyName(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, body := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{"name": "   "})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body["error"], "name is required")
}

func TestUpdateMe_TrimsSurroundingWhitespace(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, body := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{"name": "  Padded Name  "})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Padded Name", body["name"])
}

func TestUpdateMe_MissingNameField(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, body := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body["error"], "name is required")
}

func TestUpdateMe_InvalidJSON(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	req := httptest.NewRequest("PATCH", "/v1/users/me", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateMe_NoToken(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	code, _ := patchJSON(t, srv, "", "/v1/users/me", map[string]any{"name": "x"})
	require.Equal(t, http.StatusUnauthorized, code)
}

func TestUpdateMe_PersistsAcrossList(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, _ := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{"name": "Persisted"})
	require.Equal(t, http.StatusOK, code)

	code, users, _ := getUsers(t, srv, adminToken, "email=user@test.com")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, users, 1)
	assert.Equal(t, "Persisted", users[0]["name"])
}
```

Add `"strings"` to the import block at the top of `internal/api/users_integration_test.go` if it isn't already present.

- [ ] **Step 7: Run all `TestUpdateMe_*` tests**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestUpdateMe -v -timeout 120s`
Expected: All seven tests PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/api/users.go internal/api/server.go internal/api/users_integration_test.go
git commit -m "$(cat <<'EOF'
api: add PATCH /v1/users/me for self-service name update

Authenticated users can update their own display name. Empty/whitespace
names are rejected; surrounding whitespace is trimmed before persisting.
Response is the same userResponse shape returned by GET /v1/users.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Implement `PATCH /v1/users/{id}` (handler + route + tests)

**Files:**
- Modify: `internal/api/users.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/users_integration_test.go`

- [ ] **Step 1: Write the failing happy-path test**

Append to `internal/api/users_integration_test.go`:

```go
func TestAdminUpdateUser_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	target := seedUser(t, q, "alice@test.com", "Alice")

	path := "/v1/users/" + uuidString(target.ID)
	code, body := patchJSON(t, srv, adminToken, path, map[string]any{"name": "Alice Anderson"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "alice@test.com", body["email"])
	assert.Equal(t, "Alice Anderson", body["name"])
	assert.Equal(t, false, body["is_admin"])
}

// uuidString converts a pgtype.UUID into the canonical hyphenated string form
// (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx) used in URL paths.
func uuidString(id pgtype.UUID) string {
	b := id.Bytes
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	for i, j := 0, 0; i < 16; i++ {
		if j == 8 || j == 13 || j == 18 || j == 23 {
			out[j] = '-'
			j++
		}
		out[j] = hex[b[i]>>4]
		out[j+1] = hex[b[i]&0x0f]
		j += 2
	}
	return string(out)
}
```

Add `"github.com/jackc/pgx/v5/pgtype"` to the import block of the test file.

- [ ] **Step 2: Run the test — confirm it fails**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestAdminUpdateUser_HappyPath -v -timeout 120s`
Expected: FAIL with status `404` (no route).

- [ ] **Step 3: Implement `handleAdminUpdateUser` in `internal/api/users.go`**

Append at the bottom of `internal/api/users.go`:

```go
func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	name, ok := parseUpdateUserRequest(w, r)
	if !ok {
		return
	}

	row, err := s.q.UpdateUserName(r.Context(), store.UpdateUserNameParams{
		ID:   id,
		Name: name,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt))
}
```

- [ ] **Step 4: Register the route in `internal/api/server.go`**

In `internal/api/server.go`, in the `// User management` block from Task 3, add the admin PATCH so the block reads:

```go
	// User management
	mux.Handle("GET /v1/users", auth(admin(http.HandlerFunc(s.handleListUsers))))
	mux.Handle("POST /v1/users/password-reset", auth(admin(http.HandlerFunc(s.handleAdminPasswordReset))))
	mux.Handle("PATCH /v1/users/me", auth(http.HandlerFunc(s.handleUpdateMe)))
	mux.Handle("PATCH /v1/users/{id}", auth(admin(http.HandlerFunc(s.handleAdminUpdateUser))))
```

(Go 1.22+ `http.ServeMux` chooses the more specific pattern: `/v1/users/me` wins over `/v1/users/{id}` automatically — no manual ordering required.)

- [ ] **Step 5: Run the test — confirm it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestAdminUpdateUser_HappyPath -v -timeout 120s`
Expected: PASS.

- [ ] **Step 6: Add the remaining admin PATCH tests**

Append to `internal/api/users_integration_test.go`:

```go
func TestAdminUpdateUser_NonAdminForbidden(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")
	target := seedUser(t, q, "alice@test.com", "Alice")

	path := "/v1/users/" + uuidString(target.ID)
	code, body := patchJSON(t, srv, userToken, path, map[string]any{"name": "x"})
	require.Equal(t, http.StatusForbidden, code)
	assert.NotEmpty(t, body["error"])
}

func TestAdminUpdateUser_NotFound(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	path := "/v1/users/00000000-0000-0000-0000-000000000000"
	code, body := patchJSON(t, srv, adminToken, path, map[string]any{"name": "Nobody"})
	require.Equal(t, http.StatusNotFound, code)
	assert.Contains(t, body["error"], "user not found")
}

func TestAdminUpdateUser_InvalidUUID(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	code, body := patchJSON(t, srv, adminToken, "/v1/users/not-a-uuid", map[string]any{"name": "x"})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body["error"], "invalid user id")
}

func TestAdminUpdateUser_AdminUpdatesSelfViaAdminPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	admin, err := q.GetUserByEmail(t.Context(), "admin@test.com")
	require.NoError(t, err)

	path := "/v1/users/" + uuidString(admin.ID)
	code, body := patchJSON(t, srv, adminToken, path, map[string]any{"name": "Renamed Admin"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Renamed Admin", body["name"])
	assert.Equal(t, true, body["is_admin"])
}
```

- [ ] **Step 7: Run all admin PATCH tests**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestAdminUpdateUser -v -timeout 120s`
Expected: All four tests PASS.

- [ ] **Step 8: Run the full integration test suite for the api package**

Run: `go test -tags integration -p 1 ./internal/api/... -v -timeout 300s`
Expected: All tests PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/api/users.go internal/api/server.go internal/api/users_integration_test.go
git commit -m "$(cat <<'EOF'
api: add PATCH /v1/users/{id} admin endpoint for name updates

Admins can update any user's display name by UUID. Non-admins receive
403; unknown UUIDs return 404; malformed UUIDs return 400. Response
mirrors the self-service path.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Extract `printUserDetail` helper in CLI

**Files:**
- Modify: `internal/cli/admin_users.go`

This is a refactor with no behavior change — `doAdminUsersGet`'s inline label printing is moved into a reusable helper that the new commands will share.

- [ ] **Step 1: Edit `internal/cli/admin_users.go` — extract the helper**

Replace the body of `doAdminUsersGet` (lines around 65–94) with a call to the new helper, and add `printUserDetail`. After the edit, the bottom of the file should look like this:

```go
func doAdminUsersGet(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: relay admin users get <email>")
	}
	email := args[0]

	c := cfg.NewClient()
	var users []userListItem
	path := "/v1/users?email=" + url.QueryEscape(email)
	if err := c.do(ctx, "GET", path, nil, &users); err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	if len(users) == 0 {
		return fmt.Errorf("user not found: %s", email)
	}
	printUserDetail(out, users[0])
	return nil
}

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
}
```

- [ ] **Step 2: Run the existing CLI tests to confirm no regression**

Run: `go test ./internal/cli/... -run TestAdminUsers -v -timeout 30s`
Expected: All four existing `TestAdminUsersGet_*` and `TestAdminUsersList_*` tests still PASS — output is byte-for-byte the same.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/admin_users.go
git commit -m "$(cat <<'EOF'
cli: extract printUserDetail helper from doAdminUsersGet

Refactor only — no behavior change. Prepares the helper for reuse by the
upcoming profile update and admin users update subcommands.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Add `relay profile update --name` CLI command

**Files:**
- Create: `internal/cli/profile.go`
- Create: `internal/cli/profile_test.go`
- Modify: `cmd/relay/main.go`

- [ ] **Step 1: Write the failing happy-path test**

Create `internal/cli/profile_test.go` with:

```go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProfileUpdate_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PATCH", r.Method)
		require.Equal(t, "/v1/users/me", r.URL.Path)
		require.Equal(t, "Bearer usertoken", r.Header.Get("Authorization"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "New Name", body["name"])

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         "22222222-2222-2222-2222-222222222222",
			"email":      "user@test.com",
			"name":       "New Name",
			"is_admin":   false,
			"created_at": "2026-04-02T12:00:00Z",
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update", "--name", "New Name"}, &out)
	require.NoError(t, err)

	got := out.String()
	require.Contains(t, got, "ID:")
	require.Contains(t, got, "22222222-2222-2222-2222-222222222222")
	require.Contains(t, got, "Email:")
	require.Contains(t, got, "user@test.com")
	require.Contains(t, got, "Name:")
	require.Contains(t, got, "New Name")
}

func TestProfileUpdate_EmptyName(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update", "--name", ""}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--name is required")
}

func TestProfileUpdate_WhitespaceOnlyName(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update", "--name", "   "}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--name is required")
}

func TestProfileUpdate_MissingNameFlag(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--name is required")
}

func TestProfileUpdate_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update", "--name", "x"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}

func TestProfileUpdate_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "name is required"})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update", "--name", "x"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "name is required")
}

func TestProfile_UnknownSubcommand(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"frobnicate"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown profile subcommand")
}

func TestProfile_NoArgs(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, nil, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage: relay profile update")
}
```

- [ ] **Step 2: Run the tests — confirm they all fail with "undefined: doProfile"**

Run: `go test ./internal/cli/... -run TestProfile -v -timeout 30s`
Expected: build error mentioning `doProfile` is undefined.

- [ ] **Step 3: Create `internal/cli/profile.go`**

```go
// internal/cli/profile.go
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

// ProfileCommand returns the relay profile Command (subcommand group).
func ProfileCommand() Command {
	return Command{
		Name:  "profile",
		Usage: "profile <update> [flags]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doProfile(ctx, cfg, args, stderrWriter())
		},
	}
}

func doProfile(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay profile update --name \"<name>\"")
	}
	switch args[0] {
	case "update":
		return doProfileUpdate(ctx, cfg, args[1:], out)
	default:
		return fmt.Errorf("unknown profile subcommand: %s", args[0])
	}
}

func doProfileUpdate(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	fs := flag.NewFlagSet("profile update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "new display name (required)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(*name)
	if trimmed == "" {
		return fmt.Errorf("--name is required")
	}

	c := cfg.NewClient()
	var u userListItem
	body := map[string]string{"name": trimmed}
	if err := c.do(ctx, "PATCH", "/v1/users/me", body, &u); err != nil {
		return fmt.Errorf("update profile: %w", err)
	}
	printUserDetail(out, u)
	return nil
}
```

- [ ] **Step 4: Run the tests — confirm they pass**

Run: `go test ./internal/cli/... -run TestProfile -v -timeout 30s`
Expected: All eight tests PASS.

- [ ] **Step 5: Register the command in `cmd/relay/main.go`**

Open `cmd/relay/main.go`. Find the slice where `cli.AdminCommand()` is added (around line 38) and add `cli.ProfileCommand()` to the same slice. The exact placement is alphabetical near `AdminCommand`; if the file uses a different ordering, follow that ordering.

Verify the change:

```bash
grep -n "ProfileCommand\|AdminCommand" cmd/relay/main.go
```

Expected: both lines appear.

- [ ] **Step 6: Build all binaries to confirm wiring compiles**

Run: `make build`
Expected: succeeds; `bin/relay`, `bin/relay-server`, `bin/relay-agent` produced.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/profile.go internal/cli/profile_test.go cmd/relay/main.go
git commit -m "$(cat <<'EOF'
cli: add 'relay profile update --name' for self-service name change

New top-level 'profile' subcommand. Validates --name client-side before
issuing PATCH /v1/users/me; prints the updated user using the shared
printUserDetail helper.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Add `relay admin users update` CLI command

**Files:**
- Modify: `internal/cli/admin_users.go`
- Modify: `internal/cli/admin_users_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/admin_users_test.go`:

```go
func TestAdminUsersUpdate_ByUUID(t *testing.T) {
	const targetID = "22222222-2222-2222-2222-222222222222"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PATCH", r.Method)
		require.Equal(t, "/v1/users/"+targetID, r.URL.Path)
		require.Equal(t, "Bearer admintoken", r.Header.Get("Authorization"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "Renamed", body["name"])

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         targetID,
			"email":      "alice@test.com",
			"name":       "Renamed",
			"is_admin":   false,
			"created_at": "2026-04-02T12:00:00Z",
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update", targetID, "--name", "Renamed"}, &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), "Renamed")
}

func TestAdminUsersUpdate_ByEmail(t *testing.T) {
	const targetID = "22222222-2222-2222-2222-222222222222"
	var calls []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/users":
			require.Equal(t, "email=alice%40test.com", r.URL.RawQuery)
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":         targetID,
				"email":      "alice@test.com",
				"name":       "Alice",
				"is_admin":   false,
				"created_at": "2026-04-02T12:00:00Z",
			}})
		case r.Method == "PATCH" && r.URL.Path == "/v1/users/"+targetID:
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "Renamed", body["name"])
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         targetID,
				"email":      "alice@test.com",
				"name":       "Renamed",
				"is_admin":   false,
				"created_at": "2026-04-02T12:00:00Z",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update", "alice@test.com", "--name", "Renamed"}, &out)
	require.NoError(t, err)
	require.Len(t, calls, 2)
	require.Contains(t, calls[0], "GET /v1/users")
	require.Contains(t, calls[1], "PATCH /v1/users/"+targetID)
}

func TestAdminUsersUpdate_EmailNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method, "should not call PATCH when email lookup misses")
		require.Equal(t, "/v1/users", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update", "nobody@test.com", "--name", "x"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "user not found: nobody@test.com")
}

func TestAdminUsersUpdate_EmptyName(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update", "alice@test.com", "--name", ""}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--name is required")
}

func TestAdminUsersUpdate_MissingPositional(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage: relay admin users update")
}

func TestAdminUsersUpdate_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "update", "alice@test.com", "--name", "x"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}

func TestAdminUsersUpdate_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const targetID = "22222222-2222-2222-2222-222222222222"
		require.Equal(t, "PATCH", r.Method)
		require.Equal(t, "/v1/users/"+targetID, r.URL.Path)
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin only"})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "usertoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{
		"users", "update",
		"22222222-2222-2222-2222-222222222222",
		"--name", "x",
	}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "admin only")
}
```

- [ ] **Step 2: Run the tests — confirm they fail because `update` isn't a known subcommand**

Run: `go test ./internal/cli/... -run TestAdminUsersUpdate -v -timeout 30s`
Expected: FAIL with errors like `unknown admin users subcommand: update`.

- [ ] **Step 3: Add the `update` case and `doAdminUsersUpdate` to `internal/cli/admin_users.go`**

In `internal/cli/admin_users.go`, expand the `doAdminUsers` switch to include `update`:

```go
func doAdminUsers(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay admin users <list|get|update> [args]")
	}
	switch args[0] {
	case "list":
		return doAdminUsersList(ctx, cfg, args[1:], out)
	case "get":
		return doAdminUsersGet(ctx, cfg, args[1:], out)
	case "update":
		return doAdminUsersUpdate(ctx, cfg, args[1:], out)
	default:
		return fmt.Errorf("unknown admin users subcommand: %s", args[0])
	}
}
```

Append `doAdminUsersUpdate` and a small `looksLikeUUID` helper at the bottom of the same file:

```go
func doAdminUsersUpdate(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	fs := flag.NewFlagSet("admin users update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "new display name (required)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: relay admin users update <email-or-id> --name \"<name>\"")
	}
	target := fs.Arg(0)

	trimmed := strings.TrimSpace(*name)
	if trimmed == "" {
		return fmt.Errorf("--name is required")
	}

	c := cfg.NewClient()

	// Resolve target → UUID. If the positional looks like a UUID, use it
	// directly; otherwise treat it as an email and look up via /v1/users.
	id := target
	if !looksLikeUUID(target) {
		var users []userListItem
		path := "/v1/users?email=" + url.QueryEscape(target)
		if err := c.do(ctx, "GET", path, nil, &users); err != nil {
			return fmt.Errorf("look up user: %w", err)
		}
		if len(users) == 0 {
			return fmt.Errorf("user not found: %s", target)
		}
		id = users[0].ID
	}

	var u userListItem
	body := map[string]string{"name": trimmed}
	if err := c.do(ctx, "PATCH", "/v1/users/"+id, body, &u); err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	printUserDetail(out, u)
	return nil
}

// looksLikeUUID returns true for the canonical 36-char hyphenated UUID form.
// Avoids a uuid library dependency for a one-line shape check.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
```

Add `"flag"` and `"strings"` to the import block of `internal/cli/admin_users.go`.

- [ ] **Step 4: Run the tests — confirm they pass**

Run: `go test ./internal/cli/... -run TestAdminUsersUpdate -v -timeout 30s`
Expected: All seven tests PASS.

- [ ] **Step 5: Run the full CLI test suite to confirm no regression**

Run: `go test ./internal/cli/... -v -timeout 60s`
Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/admin_users.go internal/cli/admin_users_test.go
git commit -m "$(cat <<'EOF'
cli: add 'relay admin users update' for admin name overrides

Accepts either a UUID or an email as the positional target. Email is
resolved via GET /v1/users?email= before the PATCH /v1/users/{id} call.
Reuses the printUserDetail helper for output.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Documentation and backlog housekeeping

**Files:**
- Modify: `CLAUDE.md`
- Move: `docs/backlog/idea-2026-04-27-user-profile-update-endpoint.md` → `docs/backlog/closed/`
- Move: `docs/backlog/idea-2026-04-27-getuserbyemail-fetches-password-hash.md` → `docs/backlog/closed/`

- [ ] **Step 1: Update `CLAUDE.md`**

Find the `internal/api/` description (the one mentioning `users.go` and `GET /v1/users`). Replace its `users.go` clause with:

```
`users.go` exposes `GET /v1/users` (admin-only) for listing accounts; supports `?email=<exact>` filter for direct lookup; uses `GetUserByEmailPublic` so the password hash is never read on the public path; never returns `password_hash`. Also `PATCH /v1/users/me` (any authenticated user) and `PATCH /v1/users/{id}` (admin-only) update the display name, validating that the trimmed name is non-empty and returning the same `userResponse` shape.
```

Find the `relay CLI internals` section that lists the relevant subcommands and append:

```
- `relay profile update --name "<name>"` — update your own display name; calls `PATCH /v1/users/me`
- `relay admin users update <email-or-id> --name "<name>"` — admin-only override of another user's display name; resolves email→UUID via `GET /v1/users?email=` when the positional isn't a UUID
```

(Add them under the existing `relay admin users` and similar entries — match the surrounding style.)

- [ ] **Step 2: Move the closed backlog items**

```bash
git mv docs/backlog/idea-2026-04-27-user-profile-update-endpoint.md docs/backlog/closed/
git mv docs/backlog/idea-2026-04-27-getuserbyemail-fetches-password-hash.md docs/backlog/closed/
```

- [ ] **Step 3: In each moved file, update the `status` frontmatter from `open` to `closed` and add a `closed:` line**

Edit `docs/backlog/closed/idea-2026-04-27-user-profile-update-endpoint.md`. Change:

```yaml
status: open
```

to:

```yaml
status: closed
closed: 2026-04-29
```

Repeat for `docs/backlog/closed/idea-2026-04-27-getuserbyemail-fetches-password-hash.md`.

- [ ] **Step 4: Run the full test suite as a final check**

Run: `make test`
Expected: PASS.

Run: `make test-integration` (Docker required)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md docs/backlog/
git commit -m "$(cat <<'EOF'
docs: document profile update endpoints; close two backlog items

CLAUDE.md updated with the new PATCH endpoints and the two CLI
subcommands. Closes:
- idea-2026-04-27-user-profile-update-endpoint
- idea-2026-04-27-getuserbyemail-fetches-password-hash

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Final verification

After Task 8 is committed, do one full sweep:

- [ ] **Build:** `make build` → succeeds.
- [ ] **Unit:** `make test` → all PASS.
- [ ] **Integration:** `make test-integration` → all PASS.
- [ ] **Diff sanity:** `git log --oneline master..HEAD` → eight commits, one per task, in order.
- [ ] **No leftover changes:** `git status` → clean.
