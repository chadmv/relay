# Security Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Gate new-user registration behind admin-issued one-time invite tokens, and provide an env-var bootstrap mechanism to create the first admin.

**Architecture:** New `invites` DB table stores SHA256-hashed one-time tokens. `POST /v1/auth/token` issues a token immediately for existing users (unchanged) but requires a valid invite token for new ones. A new `POST /v1/invites` admin-only endpoint creates invites. `RELAY_BOOTSTRAP_ADMIN` env var promotes/creates the first admin at startup before any other admin can exist.

**Tech Stack:** Go 1.22, PostgreSQL, sqlc (pgx/v5), stdlib `net/http`, testcontainers-go (integration tests)

---

## File Map

| File | Action | Purpose |
|---|---|---|
| `internal/store/migrations/000002_invites.up.sql` | Create | Adds `invites` table |
| `internal/store/migrations/000002_invites.down.sql` | Create | Drops `invites` table |
| `internal/store/query/invites.sql` | Create | sqlc queries for invites |
| `internal/store/query/users.sql` | Modify | Add `AdminExists`, `PromoteUserToAdmin` queries |
| `internal/store/invites.sql.go` | Generated | sqlc output for invites |
| `internal/store/users.sql.go` | Generated | Regenerated with new user queries |
| `cmd/relay-server/bootstrap.go` | Create | `bootstrapAdmin(ctx, q, email)` function |
| `cmd/relay-server/bootstrap_test.go` | Create | Integration tests for bootstrap |
| `cmd/relay-server/main.go` | Modify | Read `RELAY_BOOTSTRAP_ADMIN`, call `bootstrapAdmin` |
| `internal/api/invites.go` | Create | `handleCreateInvite` handler |
| `internal/api/invites_test.go` | Create | Integration tests for invite endpoint |
| `internal/api/token.go` | Modify | Gate new-user creation behind invite |
| `internal/api/token_test.go` | Create | Integration tests for modified token endpoint |
| `internal/api/server.go` | Modify | Register `POST /v1/invites` route |
| `internal/cli/client.go` | Modify | Add `ResponseError` type so callers can inspect status code |
| `internal/cli/invites.go` | Create | `relay invite create` command |
| `internal/cli/invites_test.go` | Create | Unit tests for invite command |
| `internal/cli/login.go` | Modify | Retry with invite token on 403 "invite required" |
| `internal/cli/login_test.go` | Modify | Add test for invite-required flow |
| `cmd/relay/main.go` | Modify | Register `InviteCommand()` |

---

## Task 1: Database migration and sqlc queries

**Files:**
- Create: `internal/store/migrations/000002_invites.up.sql`
- Create: `internal/store/migrations/000002_invites.down.sql`
- Create: `internal/store/query/invites.sql`
- Modify: `internal/store/query/users.sql`

- [ ] **Step 1: Write the up migration**

Create `internal/store/migrations/000002_invites.up.sql`:

```sql
CREATE TABLE invites (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash  TEXT        NOT NULL UNIQUE,
    email       TEXT,
    created_by  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    used_by     UUID        REFERENCES users(id) ON DELETE SET NULL
);
```

- [ ] **Step 2: Write the down migration**

Create `internal/store/migrations/000002_invites.down.sql`:

```sql
DROP TABLE IF EXISTS invites;
```

- [ ] **Step 3: Write the invites sqlc queries**

Create `internal/store/query/invites.sql`:

```sql
-- name: CreateInvite :one
INSERT INTO invites (token_hash, email, created_by, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetInviteByTokenHash :one
SELECT * FROM invites WHERE token_hash = $1;

-- name: MarkInviteUsed :execrows
UPDATE invites
SET used_at = NOW(), used_by = $2
WHERE id = $1 AND used_at IS NULL;
```

`:execrows` returns the number of rows affected. If 0, the invite was already redeemed (race-safe).

- [ ] **Step 4: Add AdminExists and PromoteUserToAdmin to users.sql**

Append to `internal/store/query/users.sql`:

```sql
-- name: AdminExists :one
SELECT EXISTS(
    SELECT 1 FROM users WHERE is_admin = TRUE
) AS "exists";

-- name: PromoteUserToAdmin :exec
UPDATE users SET is_admin = TRUE WHERE id = $1;
```

- [ ] **Step 5: Run sqlc generate**

```bash
cd /d/dev/relay && make generate
```

Expected: no errors. New file `internal/store/invites.sql.go` created. `internal/store/users.sql.go` updated.

- [ ] **Step 6: Verify generated files**

```bash
ls internal/store/invites.sql.go
grep -n "func.*AdminExists\|func.*PromoteUserToAdmin\|func.*CreateInvite\|func.*GetInviteByTokenHash\|func.*MarkInviteUsed" internal/store/*.go
```

Expected output (5 lines, one per function):
```
internal/store/invites.sql.go:func (q *Queries) CreateInvite(...)
internal/store/invites.sql.go:func (q *Queries) GetInviteByTokenHash(...)
internal/store/invites.sql.go:func (q *Queries) MarkInviteUsed(...)
internal/store/users.sql.go:func (q *Queries) AdminExists(...)
internal/store/users.sql.go:func (q *Queries) PromoteUserToAdmin(...)
```

- [ ] **Step 7: Confirm the project builds**

```bash
cd /d/dev/relay && go build ./...
```

Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add internal/store/migrations/000002_invites.up.sql \
        internal/store/migrations/000002_invites.down.sql \
        internal/store/query/invites.sql \
        internal/store/query/users.sql \
        internal/store/invites.sql.go \
        internal/store/users.sql.go \
        internal/store/db.go \
        internal/store/models.go
git commit -m "feat(store): add invites table migration and sqlc queries"
```

---

## Task 2: Server admin bootstrap

**Files:**
- Create: `cmd/relay-server/bootstrap.go`
- Create: `cmd/relay-server/bootstrap_test.go`
- Modify: `cmd/relay-server/main.go`

- [ ] **Step 1: Write the failing integration tests**

Create `cmd/relay-server/bootstrap_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func newTestQueries(t *testing.T) *store.Queries {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("relay_test"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	migrateDSN := "pgx5" + dsn[len("postgres"):]
	require.NoError(t, store.Migrate(migrateDSN))
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return store.New(pool)
}

func TestBootstrapAdmin_NoUsers_CreatesAdmin(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	require.NoError(t, bootstrapAdmin(ctx, q, "admin@example.com"))

	user, err := q.GetUserByEmail(ctx, "admin@example.com")
	require.NoError(t, err)
	assert.True(t, user.IsAdmin)
	assert.Equal(t, "admin@example.com", user.Email)
}

func TestBootstrapAdmin_ExistingUser_Promotes(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	_, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Bob", Email: "admin@example.com", IsAdmin: false,
	})
	require.NoError(t, err)

	require.NoError(t, bootstrapAdmin(ctx, q, "admin@example.com"))

	user, err := q.GetUserByEmail(ctx, "admin@example.com")
	require.NoError(t, err)
	assert.True(t, user.IsAdmin)
}

func TestBootstrapAdmin_AdminAlreadyExists_Skips(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	_, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Existing Admin", Email: "other@example.com", IsAdmin: true,
	})
	require.NoError(t, err)

	require.NoError(t, bootstrapAdmin(ctx, q, "new@example.com"))

	// The requested email must NOT have been created.
	_, err = q.GetUserByEmail(ctx, "new@example.com")
	require.Error(t, err, "expected no user created when an admin already exists")
}
```

- [ ] **Step 2: Run tests to confirm they fail (compile error expected)**

```bash
cd /d/dev/relay && go test -tags integration ./cmd/relay-server/ -run TestBootstrapAdmin -v 2>&1 | head -20
```

Expected: compile error — `bootstrapAdmin` undefined.

- [ ] **Step 3: Implement bootstrapAdmin**

Create `cmd/relay-server/bootstrap.go`:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"relay/internal/store"
)

// bootstrapAdmin creates or promotes an admin user when no admin exists.
// Safe to call on every startup — becomes a no-op once any admin exists.
func bootstrapAdmin(ctx context.Context, q *store.Queries, email string) error {
	exists, err := q.AdminExists(ctx)
	if err != nil {
		return fmt.Errorf("check admin exists: %w", err)
	}
	if exists {
		log.Println("bootstrap-admin skipped: admin already exists")
		return nil
	}

	user, err := q.GetUserByEmail(ctx, email)
	if err == nil {
		if err := q.PromoteUserToAdmin(ctx, user.ID); err != nil {
			return fmt.Errorf("promote user to admin: %w", err)
		}
		log.Printf("bootstrap admin ready (promoted existing user): %s", email)
		return nil
	}

	if _, err := q.CreateUser(ctx, store.CreateUserParams{
		Name:    email,
		Email:   email,
		IsAdmin: true,
	}); err != nil {
		return fmt.Errorf("create admin user: %w", err)
	}
	log.Printf("bootstrap admin ready (created new user): %s", email)
	return nil
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
cd /d/dev/relay && go test -tags integration ./cmd/relay-server/ -run TestBootstrapAdmin -v
```

Expected: `PASS` for all three tests.

- [ ] **Step 5: Wire bootstrap into main.go**

In `cmd/relay-server/main.go`, add this block immediately after `q := store.New(pool)` and the `RequeueAllActiveTasks` call:

```go
if bootstrapEmail := os.Getenv("RELAY_BOOTSTRAP_ADMIN"); bootstrapEmail != "" {
	if err := bootstrapAdmin(ctx, q, bootstrapEmail); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
	}
}
```

- [ ] **Step 6: Build to confirm no errors**

```bash
cd /d/dev/relay && go build ./cmd/relay-server/
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add cmd/relay-server/bootstrap.go cmd/relay-server/bootstrap_test.go cmd/relay-server/main.go
git commit -m "feat(server): add RELAY_BOOTSTRAP_ADMIN startup bootstrap"
```

---

## Task 3: Add POST /v1/invites endpoint

**Files:**
- Create: `internal/api/invites.go`
- Create: `internal/api/invites_test.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: Write the failing integration tests**

Create `internal/api/invites_test.go`:

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateInvite_NonAdmin_Forbidden(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	user, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Alice", Email: "alice@test.com", IsAdmin: false,
	})
	require.NoError(t, err)
	token := createTestToken(t, q, user.ID)

	srv := api.New(pool, q, nil, nil, func() {})

	req := httptest.NewRequest("POST", "/v1/invites", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCreateInvite_Admin_ReturnsToken(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	admin, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Admin", Email: "admin@test.com", IsAdmin: true,
	})
	require.NoError(t, err)
	token := createTestToken(t, q, admin.ID)

	srv := api.New(pool, q, nil, nil, func() {})

	body := `{"expires_in": "24h"}`
	req := httptest.NewRequest("POST", "/v1/invites", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp["token"])
	assert.NotEmpty(t, resp["id"])
	assert.NotEmpty(t, resp["expires_at"])
	assert.Nil(t, resp["email"]) // no email bound
}

func TestCreateInvite_Admin_EmailBound(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	admin, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Admin", Email: "admin@test.com", IsAdmin: true,
	})
	require.NoError(t, err)
	token := createTestToken(t, q, admin.ID)

	srv := api.New(pool, q, nil, nil, func() {})

	body := `{"email": "newuser@test.com", "expires_in": "24h"}`
	req := httptest.NewRequest("POST", "/v1/invites", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp["token"])
	assert.Equal(t, "newuser@test.com", resp["email"])
}
```

The tests reference `createTestToken` — check if it already exists in `api_test.go`. If not, add this helper at the bottom of `internal/api/testhelper_test.go`:

```go
func createTestToken(t *testing.T, q *store.Queries, userID pgtype.UUID) string {
	t.Helper()
	ctx := t.Context()
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])
	_, err := q.CreateToken(ctx, store.CreateTokenParams{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{},
	})
	require.NoError(t, err)
	return rawHex
}
```

(Add imports `"crypto/rand"`, `"crypto/sha256"`, `"encoding/hex"` to the test helper file if not present.)

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd /d/dev/relay && go test -tags integration ./internal/api/ -run TestCreateInvite -v 2>&1 | head -30
```

Expected: compile error or 404 — `handleCreateInvite` not yet wired.

- [ ] **Step 3: Implement the invite handler**

Create `internal/api/invites.go`:

```go
package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		Email     string `json:"email"`
		ExpiresIn string `json:"expires_in"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	dur := 72 * time.Hour
	if req.ExpiresIn != "" {
		var err error
		dur, err = time.ParseDuration(req.ExpiresIn)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid expires_in: use a Go duration like '72h' or '24h'")
			return
		}
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])

	expiresAt := pgtype.Timestamptz{Time: time.Now().Add(dur), Valid: true}

	params := store.CreateInviteParams{
		TokenHash: hash,
		CreatedBy: u.ID,
		ExpiresAt: expiresAt,
	}
	if req.Email != "" {
		params.Email = &req.Email
	}

	invite, err := s.q.CreateInvite(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create invite")
		return
	}

	resp := map[string]any{
		"id":         uuidStr(invite.ID),
		"token":      rawHex,
		"expires_at": invite.ExpiresAt.Time,
	}
	if invite.Email != nil {
		resp["email"] = *invite.Email
	}
	writeJSON(w, http.StatusCreated, resp)
}
```

- [ ] **Step 4: Register the route in server.go**

In `internal/api/server.go`, inside `Handler()`, after the reservations block add:

```go
// Invites (admin-only)
mux.Handle("POST /v1/invites", auth(admin(http.HandlerFunc(s.handleCreateInvite))))
```

- [ ] **Step 5: Run tests to confirm they pass**

```bash
cd /d/dev/relay && go test -tags integration ./internal/api/ -run TestCreateInvite -v
```

Expected: all three tests `PASS`.

- [ ] **Step 6: Commit**

```bash
git add internal/api/invites.go internal/api/invites_test.go internal/api/server.go internal/api/testhelper_test.go
git commit -m "feat(api): add POST /v1/invites admin endpoint"
```

---

## Task 4: Gate POST /v1/auth/token for new users

**Files:**
- Modify: `internal/api/token.go`
- Create: `internal/api/token_test.go`

- [ ] **Step 1: Write the failing integration tests**

Create `internal/api/token_test.go`:

```go
//go:build integration

package api_test

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateToken_ExistingUser_NoInviteNeeded(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	_, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Alice", Email: "alice@test.com", IsAdmin: false,
	})
	require.NoError(t, err)

	srv := api.New(pool, q, nil, nil, func() {})

	body := `{"email": "alice@test.com"}`
	req := httptest.NewRequest("POST", "/v1/auth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestCreateToken_NewUser_NoInvite_Forbidden(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, nil, nil, func() {})

	body := `{"email": "newuser@test.com"}`
	req := httptest.NewRequest("POST", "/v1/auth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "invite required", resp["error"])
}

func TestCreateToken_NewUser_ValidInvite_CreatesAccount(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	admin, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Admin", Email: "admin@test.com", IsAdmin: true,
	})
	require.NoError(t, err)

	// Create invite token manually.
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])
	_, err = q.CreateInvite(ctx, store.CreateInviteParams{
		TokenHash: hash,
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(72 * time.Hour), Valid: true},
	})
	require.NoError(t, err)

	srv := api.New(pool, q, nil, nil, func() {})

	body, _ := json.Marshal(map[string]string{
		"email":        "newuser@test.com",
		"invite_token": rawHex,
	})
	req := httptest.NewRequest("POST", "/v1/auth/token", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp["token"])

	// User should now exist.
	user, err := q.GetUserByEmail(ctx, "newuser@test.com")
	require.NoError(t, err)
	assert.Equal(t, "newuser@test.com", user.Email)
}

func TestCreateToken_NewUser_ExpiredInvite_BadRequest(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	admin, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Admin", Email: "admin@test.com", IsAdmin: true,
	})
	require.NoError(t, err)

	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])
	_, err = q.CreateInvite(ctx, store.CreateInviteParams{
		TokenHash: hash,
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Hour), Valid: true}, // expired
	})
	require.NoError(t, err)

	srv := api.New(pool, q, nil, nil, func() {})

	body, _ := json.Marshal(map[string]string{
		"email":        "newuser@test.com",
		"invite_token": rawHex,
	})
	req := httptest.NewRequest("POST", "/v1/auth/token", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "invite expired", resp["error"])
}

func TestCreateToken_NewUser_EmailMismatch_BadRequest(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	admin, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Admin", Email: "admin@test.com", IsAdmin: true,
	})
	require.NoError(t, err)

	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])
	boundEmail := "expected@test.com"
	_, err = q.CreateInvite(ctx, store.CreateInviteParams{
		TokenHash: hash,
		Email:     &boundEmail,
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(72 * time.Hour), Valid: true},
	})
	require.NoError(t, err)

	srv := api.New(pool, q, nil, nil, func() {})

	body, _ := json.Marshal(map[string]string{
		"email":        "wrong@test.com",
		"invite_token": rawHex,
	})
	req := httptest.NewRequest("POST", "/v1/auth/token", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "invite not valid for this email", resp["error"])
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd /d/dev/relay && go test -tags integration ./internal/api/ -run TestCreateToken -v 2>&1 | head -30
```

Expected: `TestCreateToken_NewUser_NoInvite_Forbidden` fails (currently returns 201 instead of 403).

- [ ] **Step 3: Modify handleCreateToken to gate new users**

Replace the full contents of `internal/api/token.go`:

```go
package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email       string `json:"email"`
		Name        string `json:"name"`
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

	ctx := r.Context()

	user, err := s.q.GetUserByEmail(ctx, req.Email)
	if err != nil {
		// Unknown user — require a valid invite token.
		if req.InviteToken == "" {
			writeError(w, http.StatusForbidden, "invite required")
			return
		}

		sum := sha256.Sum256([]byte(req.InviteToken))
		hash := hex.EncodeToString(sum[:])

		invite, err := s.q.GetInviteByTokenHash(ctx, hash)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid invite token")
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

		name := req.Name
		if name == "" {
			name = req.Email
		}
		user, err = s.q.CreateUser(ctx, store.CreateUserParams{
			Name:    name,
			Email:   req.Email,
			IsAdmin: false,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create user")
			return
		}

		// Mark invite used atomically (WHERE used_at IS NULL prevents double-redemption).
		rows, err := s.q.MarkInviteUsed(ctx, store.MarkInviteUsedParams{
			ID:     invite.ID,
			UsedBy: user.ID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to redeem invite")
			return
		}
		if rows == 0 {
			writeError(w, http.StatusBadRequest, "invite already used")
			return
		}
	}

	// Generate a random 32-byte API token.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])

	expires := pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true}
	if _, err := s.q.CreateToken(ctx, store.CreateTokenParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: expires,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      rawHex,
		"expires_at": expires.Time,
	})
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
cd /d/dev/relay && go test -tags integration ./internal/api/ -run TestCreateToken -v
```

Expected: all five tests `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/api/token.go internal/api/token_test.go
git commit -m "feat(api): gate new-user registration behind invite token"
```

---

## Task 5: Add `relay invite create` CLI command

**Files:**
- Modify: `internal/cli/client.go`
- Create: `internal/cli/invites.go`
- Create: `internal/cli/invites_test.go`
- Modify: `cmd/relay/main.go`

- [ ] **Step 1: Add ResponseError type to client.go**

In `internal/cli/client.go`, add this type above `Client`:

```go
// ResponseError is returned by Client.do for non-2xx responses.
// Callers can inspect StatusCode to handle specific HTTP errors.
type ResponseError struct {
	StatusCode int
	Message    string
}

func (e *ResponseError) Error() string { return e.Message }
```

Then in the `do()` method, replace the three `return fmt.Errorf(...)` lines inside `if resp.StatusCode >= 400` with:

```go
if resp.StatusCode >= 400 {
    var errBody struct {
        Error string `json:"error"`
    }
    if decodeErr := json.NewDecoder(resp.Body).Decode(&errBody); decodeErr == nil && errBody.Error != "" {
        return &ResponseError{StatusCode: resp.StatusCode, Message: errBody.Error}
    }
    if resp.StatusCode >= 500 {
        return &ResponseError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("server error (%d) — try again", resp.StatusCode)}
    }
    return &ResponseError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("request failed (%d)", resp.StatusCode)}
}
```

- [ ] **Step 2: Build to confirm no errors**

```bash
cd /d/dev/relay && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Write the failing unit test**

Create `internal/cli/invites_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInviteCreate_PrintsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/invites", r.URL.Path)

		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "user@example.com", body["email"])
		assert.Equal(t, "24h", body["expires_in"])

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "some-uuid",
			"token":      "the-invite-token",
			"expires_at": time.Now().Add(24 * time.Hour),
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-token"}
	var out strings.Builder
	err := doInviteCreate(context.Background(), []string{"--email", "user@example.com", "--expires", "24h"}, cfg, &out)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "the-invite-token")
}

func TestInviteCreate_NoEmail_DefaultExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "", body["email"])
		assert.Equal(t, "72h", body["expires_in"])

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "some-uuid",
			"token":      "tok-xyz",
			"expires_at": time.Now().Add(72 * time.Hour),
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-token"}
	var out strings.Builder
	err := doInviteCreate(context.Background(), []string{}, cfg, &out)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "tok-xyz")
}
```

- [ ] **Step 4: Run tests to confirm they fail**

```bash
cd /d/dev/relay && go test ./internal/cli/ -run TestInviteCreate -v 2>&1 | head -20
```

Expected: compile error — `doInviteCreate` undefined.

- [ ] **Step 5: Implement InviteCommand**

Create `internal/cli/invites.go`:

```go
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

// InviteCommand returns the relay invite Command.
func InviteCommand() Command {
	return Command{
		Name:  "invite",
		Usage: "manage invites (subcommands: create)",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			if len(args) == 0 || args[0] != "create" {
				return fmt.Errorf("usage: relay invite create [--email EMAIL] [--expires DURATION]")
			}
			return doInviteCreate(ctx, args[1:], cfg, os.Stdout)
		},
	}
}

func doInviteCreate(ctx context.Context, args []string, cfg *Config, out io.Writer) error {
	fs := flag.NewFlagSet("invite create", flag.ContinueOnError)
	email := fs.String("email", "", "bind invite to this email address (optional)")
	expires := fs.String("expires", "72h", "invite lifetime, e.g. 24h or 168h")

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	var req struct {
		Email     string `json:"email,omitempty"`
		ExpiresIn string `json:"expires_in,omitempty"`
	}
	req.Email = *email
	req.ExpiresIn = *expires

	c := cfg.NewClient()
	var resp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := c.do(ctx, "POST", "/v1/invites", req, &resp); err != nil {
		return fmt.Errorf("create invite: %w", err)
	}

	fmt.Fprintf(out, "%s\n", resp.Token)
	return nil
}
```

- [ ] **Step 6: Register InviteCommand in cmd/relay/main.go**

In `cmd/relay/main.go`, add `cli.InviteCommand()` to the commands slice:

```go
commands := []cli.Command{
    cli.LoginCommand(),
    cli.InviteCommand(),
    cli.SubmitCommand(),
    cli.ListCommand(),
    cli.GetCommand(),
    cli.CancelCommand(),
    cli.LogsCommand(),
    cli.WorkersCommand(),
    cli.ReservationsCommand(),
}
```

- [ ] **Step 7: Run tests to confirm they pass**

```bash
cd /d/dev/relay && go test ./internal/cli/ -run TestInviteCreate -v
```

Expected: both tests `PASS`.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/client.go internal/cli/invites.go internal/cli/invites_test.go cmd/relay/main.go
git commit -m "feat(cli): add relay invite create command and ResponseError type"
```

---

## Task 6: Add invite-token retry to `relay login`

**Files:**
- Modify: `internal/cli/login.go`
- Modify: `internal/cli/login_test.go`

- [ ] **Step 1: Write the failing test**

Add this test to `internal/cli/login_test.go`:

```go
func TestRunLogin_InviteRequired_PromptsAndRetries(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		callCount++
		if callCount == 1 {
			// First call: no invite token — server rejects.
			assert.Equal(t, "", body["invite_token"])
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "invite required"})
			return
		}
		// Second call: invite token present.
		assert.Equal(t, "my-invite-token", body["invite_token"])
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"token":      "tok-new",
			"expires_at": time.Now().Add(30 * 24 * time.Hour),
		})
	}))
	defer srv.Close()

	var saved *Config
	origSave := saveConfigFn
	saveConfigFn = func(cfg *Config) error { saved = cfg; return nil }
	t.Cleanup(func() { saveConfigFn = origSave })

	cfg := &Config{ServerURL: srv.URL}
	// hits Enter to accept pre-filled URL, types email, types invite token
	input := strings.NewReader("\nnewuser@example.com\nmy-invite-token\n")
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.NoError(t, err)
	assert.Equal(t, 2, callCount)
	require.NotNil(t, saved)
	assert.Equal(t, "tok-new", saved.Token)
	assert.Contains(t, out.String(), "Logged in")
}
```

Also add `"net/http"` and `"time"` to the imports if not already present.

- [ ] **Step 2: Run test to confirm it fails**

```bash
cd /d/dev/relay && go test ./internal/cli/ -run TestRunLogin_InviteRequired -v 2>&1 | head -20
```

Expected: FAIL — login returns an error instead of prompting for the invite token.

- [ ] **Step 3: Modify doLogin to handle invite-required**

Replace the full contents of `internal/cli/login.go`:

```go
// internal/cli/login.go
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// saveConfigFn is a variable so tests can override it.
var saveConfigFn = SaveConfig

// LoginCommand returns the relay login Command.
func LoginCommand() Command {
	return Command{
		Name:  "login",
		Usage: "authenticate and save credentials to config file",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doLogin(ctx, cfg, stdinReader(), stderrWriter())
		},
	}
}

func doLogin(ctx context.Context, cfg *Config, in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)

	serverURL := cfg.ServerURL
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}
	fmt.Fprintf(out, "Server URL [%s]: ", serverURL)
	if line, _ := r.ReadString('\n'); strings.TrimSpace(line) != "" {
		serverURL = strings.TrimSpace(line)
	}

	fmt.Fprint(out, "Email: ")
	email, _ := r.ReadString('\n')
	email = strings.TrimSpace(email)
	if email == "" {
		return fmt.Errorf("email is required")
	}

	type tokenRequest struct {
		Email       string `json:"email"`
		InviteToken string `json:"invite_token,omitempty"`
	}

	c := NewClient(serverURL, "")
	var resp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}

	reqBody := tokenRequest{Email: email}
	if err := c.do(ctx, "POST", "/v1/auth/token", reqBody, &resp); err != nil {
		var re *ResponseError
		if errors.As(err, &re) && re.StatusCode == http.StatusForbidden && re.Message == "invite required" {
			fmt.Fprint(out, "Invite token: ")
			inviteToken, _ := r.ReadString('\n')
			inviteToken = strings.TrimSpace(inviteToken)
			if inviteToken == "" {
				return fmt.Errorf("invite token required for new accounts")
			}
			reqBody.InviteToken = inviteToken
			if err := c.do(ctx, "POST", "/v1/auth/token", reqBody, &resp); err != nil {
				return fmt.Errorf("login failed: %w", err)
			}
		} else {
			return fmt.Errorf("login failed: %w", err)
		}
	}

	cfg.ServerURL = serverURL
	cfg.Token = resp.Token
	if err := saveConfigFn(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(out, "Logged in. Token expires %s.\n", resp.ExpiresAt.Format("2006-01-02"))
	return nil
}
```

- [ ] **Step 4: Run all login tests to confirm they pass**

```bash
cd /d/dev/relay && go test ./internal/cli/ -run TestRunLogin -v
```

Expected: all login tests `PASS` including the new `TestRunLogin_InviteRequired_PromptsAndRetries`.

- [ ] **Step 5: Run the full unit test suite to check for regressions**

```bash
cd /d/dev/relay && go test ./...
```

Expected: all tests pass (integration tests skipped without the tag).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/login.go internal/cli/login_test.go
git commit -m "feat(cli): prompt for invite token when server requires it on login"
```

---

## Final verification

- [ ] **Step 1: Run all non-integration tests**

```bash
cd /d/dev/relay && go test ./...
```

Expected: PASS with no failures.

- [ ] **Step 2: Build all binaries**

```bash
cd /d/dev/relay && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Confirm new env var is documented**

Add `RELAY_BOOTSTRAP_ADMIN` to the server's configuration section in `README.md` if a configuration reference exists there. Search first:

```bash
grep -n "RELAY_" /d/dev/relay/README.md | head -20
```

If the README lists env vars, add:
```
RELAY_BOOTSTRAP_ADMIN  email address — creates or promotes this user to admin on startup if no admin exists
```

- [ ] **Step 4: Final commit (README only if changed)**

```bash
git add README.md
git commit -m "docs: document RELAY_BOOTSTRAP_ADMIN env var"
```
