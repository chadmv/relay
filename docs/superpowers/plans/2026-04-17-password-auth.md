# Password Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the email-only token endpoint with password-protected register and login endpoints, add a password-change endpoint, and update the CLI accordingly.

**Architecture:** Add a `password_hash` column to `users`, split `POST /v1/auth/token` into `POST /v1/auth/register` and `POST /v1/auth/login`, add `PUT /v1/users/me/password`, and wire three new CLI commands (`register`, updated `login`, `passwd`). The bootstrap admin gains a `RELAY_BOOTSTRAP_PASSWORD` env var. Passwords are bcrypt-hashed at cost 12; login uses constant-time comparison to prevent email enumeration.

**Tech Stack:** Go 1.26, PostgreSQL, sqlc (pgx/v5), golang.org/x/crypto/bcrypt, golang.org/x/term, testify, testcontainers

---

## File Map

| Action   | Path |
|----------|------|
| Create   | `internal/store/migrations/000003_passwords.up.sql` |
| Create   | `internal/store/migrations/000003_passwords.down.sql` |
| Modify   | `internal/store/query/users.sql` |
| Regen    | `internal/store/users.sql.go` (via `make generate`) |
| Regen    | `internal/store/models.go` (via `make generate`) |
| Delete   | `internal/api/token.go` |
| Delete   | `internal/api/token_test.go` |
| Create   | `internal/api/auth.go` |
| Create   | `internal/api/export_test.go` |
| Create   | `internal/api/auth_integration_test.go` |
| Modify   | `internal/api/api_test.go` |
| Modify   | `internal/api/server.go` |
| Modify   | `cmd/relay-server/bootstrap.go` |
| Modify   | `cmd/relay-server/bootstrap_test.go` |
| Modify   | `cmd/relay-server/main.go` |
| Modify   | `internal/cli/login.go` |
| Modify   | `internal/cli/login_test.go` |
| Create   | `internal/cli/register.go` |
| Create   | `internal/cli/register_test.go` |
| Create   | `internal/cli/passwd.go` |
| Create   | `internal/cli/passwd_test.go` |
| Modify   | `cmd/relay/main.go` |
| Modify   | `README.md` |

---

## Task 1: Add dependency and migration files

**Files:**
- Modify: `go.mod`, `go.sum` (via go get)
- Create: `internal/store/migrations/000003_passwords.up.sql`
- Create: `internal/store/migrations/000003_passwords.down.sql`

- [ ] **Step 1: Add golang.org/x/term**

```bash
go get golang.org/x/term
go mod tidy
```

Expected: `go.mod` adds `golang.org/x/term` as a direct dependency. `go mod tidy` exits 0.

- [ ] **Step 2: Create the up migration**

`internal/store/migrations/000003_passwords.up.sql`:
```sql
ALTER TABLE users ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE users ALTER COLUMN password_hash DROP DEFAULT;
```

The `DEFAULT ''` is needed only to satisfy NOT NULL during the ALTER; the second statement removes the default so future INSERTs must provide a value.

- [ ] **Step 3: Create the down migration**

`internal/store/migrations/000003_passwords.down.sql`:
```sql
ALTER TABLE users DROP COLUMN password_hash;
```

- [ ] **Step 4: Verify the migration test still passes**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestMigrate -v -timeout 120s
```

Expected: `PASS` — the migration test spins up a Postgres container, applies all migrations, and verifies each up/down without error.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/store/migrations/000003_passwords.up.sql internal/store/migrations/000003_passwords.down.sql
git commit -m "feat(store): add password_hash column migration and golang.org/x/term dep"
```

---

## Task 2: Update sqlc queries and regenerate the store

**Files:**
- Modify: `internal/store/query/users.sql`
- Regen: `internal/store/users.sql.go`, `internal/store/models.go`

- [ ] **Step 1: Update users.sql**

Replace the entire contents of `internal/store/query/users.sql` with:

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
```

- [ ] **Step 2: Regenerate the store**

```bash
make generate
```

Expected: `sqlc generate` and `buf generate` both exit 0. The file `internal/store/users.sql.go` is updated with `CreateUserWithPassword`, `SetPasswordHash`, and updated `User` struct (now including `PasswordHash string`). The file `internal/store/models.go` `User` struct gains a `PasswordHash string` field.

- [ ] **Step 3: Confirm the generated User struct**

Open `internal/store/models.go` and verify `User` now includes:
```go
type User struct {
    ID           pgtype.UUID        `json:"id"`
    Name         string             `json:"name"`
    Email        string             `json:"email"`
    IsAdmin      bool               `json:"is_admin"`
    CreatedAt    pgtype.Timestamptz `json:"created_at"`
    PasswordHash string             `json:"password_hash"`
}
```

- [ ] **Step 4: Commit**

```bash
git add internal/store/query/users.sql internal/store/users.sql.go internal/store/models.go
git commit -m "feat(store): replace CreateUser with CreateUserWithPassword, add SetPasswordHash query"
```

---

## Task 3: Restore compilation — delete token endpoint, add auth stubs, fix all callers

After Task 2, `store.CreateUser` and `store.CreateUserParams` no longer exist. This task fixes every file that referenced them and adds the new auth handler stubs so the project compiles.

**Files:**
- Delete: `internal/api/token.go`
- Delete: `internal/api/token_test.go`
- Create: `internal/api/auth.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/api_test.go`
- Modify: `cmd/relay-server/bootstrap.go`
- Modify: `cmd/relay-server/main.go`

- [ ] **Step 1: Delete the old token handler and its tests**

```bash
rm internal/api/token.go internal/api/token_test.go
```

- [ ] **Step 2: Create auth.go with stub handlers**

Create `internal/api/auth.go`:

```go
package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

var bcryptCost = 12

var (
	dummyHashOnce sync.Once
	dummyBcryptHash []byte
)

func getDummyHash() []byte {
	dummyHashOnce.Do(func() {
		dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("relay-auth-sentinel"), bcryptCost)
	})
	return dummyBcryptHash
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (s *Server) issueToken(ctx interface{ Value(any) any }, userID pgtype.UUID) (string, time.Time, error) {
	return "", time.Time{}, errors.New("not implemented")
}

// validateEmail returns false if email is empty or malformed.
func validateEmail(email string) bool {
	if email == "" {
		return false
	}
	_, err := mail.ParseAddress(email)
	return err == nil
}

// hashPassword hashes a plaintext password using bcrypt.
func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(h), err
}

// generateBearerToken generates a 32-byte random hex token and its SHA-256 hash.
func generateBearerToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	raw = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(sum[:])
	return
}

var _ = strings.EqualFold  // imported for use in handleRegister
var _ = store.SetPasswordHashParams{}
```

This stub compiles and will be replaced step-by-step in subsequent tasks. The dummy imports keep the compiler happy.

Actually, a cleaner stub avoids importing things we don't use yet. Replace the above with a minimal compiling stub:

```go
package api

import (
	"net/http"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

var bcryptCost = 12

var (
	dummyHashOnce   sync.Once
	dummyBcryptHash []byte
)

func getDummyHash() []byte {
	dummyHashOnce.Do(func() {
		dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("relay-auth-sentinel"), bcryptCost)
	})
	return dummyBcryptHash
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) issueToken(userID pgtype.UUID) (rawHex string, expires time.Time, err error) {
	return "", time.Time{}, nil
}
```

Wait — `time.Time` needs an import. Use this final version:

```go
package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

var bcryptCost = 12

var (
	dummyHashOnce   sync.Once
	dummyBcryptHash []byte
)

func getDummyHash() []byte {
	dummyHashOnce.Do(func() {
		dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("relay-auth-sentinel"), bcryptCost)
	})
	return dummyBcryptHash
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) issueToken(userID pgtype.UUID) (string, time.Time, error) {
	return "", time.Time{}, nil
}
```

- [ ] **Step 3: Update server routes in server.go**

In `internal/api/server.go`, replace:
```go
mux.HandleFunc("POST /v1/auth/token", s.handleCreateToken)
```
with:
```go
mux.HandleFunc("POST /v1/auth/register", s.handleRegister)
mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
mux.Handle("PUT /v1/users/me/password", auth(http.HandlerFunc(s.handleChangePassword)))
```

- [ ] **Step 4: Update api_test.go — replace CreateUser calls and remove TestCreateToken**

Replace the entire `internal/api/api_test.go` with:

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
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func newTestServer(t *testing.T) (*api.Server, *store.Queries) {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	broker := events.NewBroker()
	registry := worker.NewRegistry()
	srv := api.New(pool, q, broker, registry, func() {})
	return srv, q
}

func createTestUser(t *testing.T, q *store.Queries, name, email string, isAdmin bool) store.User {
	t.Helper()
	ph, err := bcrypt.GenerateFromPassword([]byte("testpassword1"), bcrypt.MinCost)
	require.NoError(t, err)
	user, err := q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name: name, Email: email, IsAdmin: isAdmin, PasswordHash: string(ph),
	})
	require.NoError(t, err)
	return user
}

func createTestToken(t *testing.T, q *store.Queries, userID pgtype.UUID) string {
	t.Helper()
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])
	_, err := q.CreateToken(t.Context(), store.CreateTokenParams{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{},
	})
	require.NoError(t, err)
	return rawHex
}

func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/v1/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestCreateAndGetJob(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "alice@example.com", false)
	token := createTestToken(t, q, user.ID)

	body := `{
        "name": "render-job",
        "priority": "normal",
        "tasks": [
            {"name": "task-a", "command": ["echo", "a"], "depends_on": []},
            {"name": "task-b", "command": ["echo", "b"], "depends_on": ["task-a"]}
        ]
    }`
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	jobID, ok := resp["id"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, jobID)

	req2 := httptest.NewRequest("GET", "/v1/jobs/"+jobID, nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	var job map[string]any
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&job))
	assert.Equal(t, "render-job", job["name"])
	tasks := job["tasks"].([]any)
	assert.Len(t, tasks, 2)
}

func TestGetTaskAndLogs(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "alice2@example.com", false)
	token := createTestToken(t, q, user.ID)

	body := `{"name":"j","tasks":[{"name":"t","command":["echo","hi"]}]}`
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var job map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&job))
	jobID := job["id"].(string)
	taskID := job["tasks"].([]any)[0].(map[string]any)["id"].(string)

	req2 := httptest.NewRequest("GET", "/v1/jobs/"+jobID+"/tasks", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	req3 := httptest.NewRequest("GET", "/v1/tasks/"+taskID, nil)
	req3.Header.Set("Authorization", "Bearer "+token)
	rec3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec3, req3)
	require.Equal(t, http.StatusOK, rec3.Code)

	req4 := httptest.NewRequest("GET", "/v1/tasks/"+taskID+"/logs", nil)
	req4.Header.Set("Authorization", "Bearer "+token)
	rec4 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec4, req4)
	require.Equal(t, http.StatusOK, rec4.Code)
}

func TestListWorkers(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Admin", "admin@example.com", true)
	token := createTestToken(t, q, user.ID)

	req := httptest.NewRequest("GET", "/v1/workers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var workers []any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&workers))
	assert.Empty(t, workers)
}

func TestSSESubscribe(t *testing.T) {
	srv, q := newTestServer(t)
	ctx := t.Context()
	user := createTestUser(t, q, "Alice", "alice3@example.com", false)
	token := createTestToken(t, q, user.ID)

	req := httptest.NewRequest("GET", "/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Handler().ServeHTTP(rec, req)
	}()

	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
}
```

- [ ] **Step 5: Update bootstrap.go**

Replace `internal/cmd/relay-server/bootstrap.go` with:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"relay/internal/store"

	"golang.org/x/crypto/bcrypt"
)

// bootstrapAdmin creates or promotes an admin user when no admin exists.
// Safe to call on every startup — becomes a no-op once any admin exists.
func bootstrapAdmin(ctx context.Context, q *store.Queries, email, password string) error {
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

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	newUser, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name:         email,
		Email:        email,
		IsAdmin:      true,
		PasswordHash: string(hash),
	})
	if err != nil {
		existingUser, lookupErr := q.GetUserByEmail(ctx, email)
		if lookupErr != nil {
			return fmt.Errorf("create admin user: %w", err)
		}
		newUser = existingUser
		if err := q.PromoteUserToAdmin(ctx, newUser.ID); err != nil {
			return fmt.Errorf("promote user to admin (concurrent startup): %w", err)
		}
		log.Printf("bootstrap admin ready (concurrent startup, promoted): %s", email)
		return nil
	}
	_ = newUser
	log.Printf("bootstrap admin ready (created new user): %s", email)
	return nil
}
```

- [ ] **Step 6: Update main.go to require RELAY_BOOTSTRAP_PASSWORD**

In `cmd/relay-server/main.go`, replace:
```go
if bootstrapEmail := os.Getenv("RELAY_BOOTSTRAP_ADMIN"); bootstrapEmail != "" {
    if err := bootstrapAdmin(ctx, q, bootstrapEmail); err != nil {
        log.Fatalf("bootstrap admin: %v", err)
    }
}
```
with:
```go
if bootstrapEmail := os.Getenv("RELAY_BOOTSTRAP_ADMIN"); bootstrapEmail != "" {
    bootstrapPassword := os.Getenv("RELAY_BOOTSTRAP_PASSWORD")
    if bootstrapPassword == "" {
        log.Fatalf("RELAY_BOOTSTRAP_PASSWORD must be set when RELAY_BOOTSTRAP_ADMIN is set")
    }
    if err := bootstrapAdmin(ctx, q, bootstrapEmail, bootstrapPassword); err != nil {
        log.Fatalf("bootstrap admin: %v", err)
    }
}
```

- [ ] **Step 7: Verify compilation**

```bash
go build ./...
```

Expected: exits 0, no errors.

- [ ] **Step 8: Verify unit tests pass**

```bash
go test ./... -timeout 120s
```

Expected: all packages pass. (Integration tests are not run here.)

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "refactor(api): replace token endpoint with register/login/passwd stubs; fix all compile errors"
```

---

## Task 4: TDD — POST /v1/auth/register

**Files:**
- Create: `internal/api/export_test.go`
- Create: `internal/api/auth_integration_test.go`
- Modify: `internal/api/auth.go` (implement handleRegister and issueToken)

- [ ] **Step 1: Create export_test.go to allow cost override**

Create `internal/api/export_test.go`:

```go
//go:build integration

package api

import "golang.org/x/crypto/bcrypt"

// SetBcryptCostForTest sets bcrypt cost to the minimum so integration tests run fast.
func SetBcryptCostForTest() { bcryptCost = bcrypt.MinCost }
```

- [ ] **Step 2: Create auth_integration_test.go with register tests**

Create `internal/api/auth_integration_test.go`:

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
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	api.SetBcryptCostForTest()
}

// createTestInvite inserts a valid invite and returns the raw token string.
func createTestInvite(t *testing.T, q *store.Queries, createdBy pgtype.UUID, boundEmail *string, expiresIn time.Duration) string {
	t.Helper()
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])
	_, err := q.CreateInvite(t.Context(), store.CreateInviteParams{
		TokenHash: hash,
		Email:     boundEmail,
		CreatedBy: createdBy,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(expiresIn), Valid: true},
	})
	require.NoError(t, err)
	return rawHex
}

// ── Register ─────────────────────────────────────────────────────────────────

func TestRegister_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()
	admin := createTestUser(t, q, "Admin", "admin@test.com", true)
	inviteToken := createTestInvite(t, q, admin.ID, nil, 72*time.Hour)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})
	body, _ := json.Marshal(map[string]string{
		"email": "newuser@test.com", "name": "New User",
		"password": "securepass1", "invite_token": inviteToken,
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

	user, err := q.GetUserByEmail(ctx, "newuser@test.com")
	require.NoError(t, err)
	assert.Equal(t, "New User", user.Name)
	assert.NotEmpty(t, user.PasswordHash)
}

func TestRegister_PasswordTooShort(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	admin := createTestUser(t, q, "Admin", "admin@test.com", true)
	inviteToken := createTestInvite(t, q, admin.ID, nil, 72*time.Hour)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})
	body, _ := json.Marshal(map[string]string{
		"email": "newuser@test.com", "password": "short",
		"invite_token": inviteToken,
	})
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "password must be at least 8 characters", resp["error"])
}

func TestRegister_MissingInviteToken(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})
	body, _ := json.Marshal(map[string]string{
		"email": "newuser@test.com", "password": "securepass1",
	})
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "invite_token is required", resp["error"])
}

func TestRegister_InvalidInviteToken(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})
	body, _ := json.Marshal(map[string]string{
		"email": "newuser@test.com", "password": "securepass1",
		"invite_token": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	})
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "invalid invite token", resp["error"])
}

func TestRegister_ExpiredInvite(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	admin := createTestUser(t, q, "Admin", "admin@test.com", true)
	inviteToken := createTestInvite(t, q, admin.ID, nil, -1*time.Hour)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})
	body, _ := json.Marshal(map[string]string{
		"email": "newuser@test.com", "password": "securepass1",
		"invite_token": inviteToken,
	})
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "invite expired", resp["error"])
}

func TestRegister_UsedInvite(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	admin := createTestUser(t, q, "Admin", "admin@test.com", true)
	inviteToken := createTestInvite(t, q, admin.ID, nil, 72*time.Hour)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})

	// Use it once.
	body, _ := json.Marshal(map[string]string{
		"email": "first@test.com", "password": "securepass1",
		"invite_token": inviteToken,
	})
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	// Try again with same invite.
	body2, _ := json.Marshal(map[string]string{
		"email": "second@test.com", "password": "securepass1",
		"invite_token": inviteToken,
	})
	req2 := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body2)))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusBadRequest, rec2.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&resp))
	assert.Equal(t, "invite already used", resp["error"])
}

func TestRegister_EmailBoundInvite_WrongEmail(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	admin := createTestUser(t, q, "Admin", "admin@test.com", true)
	bound := "expected@test.com"
	inviteToken := createTestInvite(t, q, admin.ID, &bound, 72*time.Hour)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})
	body, _ := json.Marshal(map[string]string{
		"email": "wrong@test.com", "password": "securepass1",
		"invite_token": inviteToken,
	})
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "invite not valid for this email", resp["error"])
}
```

- [ ] **Step 3: Run the register tests — expect failure**

```bash
go test -tags integration -p 1 ./internal/api/... -run TestRegister -v -timeout 120s
```

Expected: all `TestRegister_*` tests FAIL with `501 Not Implemented`.

- [ ] **Step 4: Implement handleRegister and issueToken in auth.go**

Replace `internal/api/auth.go` with:

```go
package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

var bcryptCost = 12

var (
	dummyHashOnce   sync.Once
	dummyBcryptHash []byte
)

func getDummyHash() []byte {
	dummyHashOnce.Do(func() {
		dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("relay-auth-sentinel"), bcryptCost)
	})
	return dummyBcryptHash
}

// issueToken generates a 32-byte random bearer token, stores its SHA-256 hash,
// and returns the raw hex token and its expiry.
func (s *Server) issueToken(userID pgtype.UUID) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])
	expires := time.Now().Add(30 * 24 * time.Hour)
	if _, err := s.q.CreateToken(nil, store.CreateTokenParams{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return "", time.Time{}, err
	}
	return rawHex, expires, nil
}

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
	if req.InviteToken == "" {
		writeError(w, http.StatusBadRequest, "invite_token is required")
		return
	}

	ctx := r.Context()

	sum := sha256.Sum256([]byte(req.InviteToken))
	hash := hex.EncodeToString(sum[:])
	invite, err := s.q.GetInviteByTokenHash(ctx, hash)
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
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	rows, err := txq.MarkInviteUsed(ctx, store.MarkInviteUsedParams{
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

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit registration")
		return
	}

	token, expires, err := s.issueToken(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      token,
		"expires_at": expires,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}
```

Note: `s.issueToken` passes `nil` as the context argument to `CreateToken`. Fix this — pass the request context:

The `issueToken` signature should accept a context:

```go
func (s *Server) issueToken(ctx context.Context, userID pgtype.UUID) (string, time.Time, error) {
```

Update the call inside `handleRegister`:
```go
token, expires, err := s.issueToken(ctx, user.ID)
```

And in `issueToken`, use `ctx` for `s.q.CreateToken(ctx, ...)`.

Here is the corrected, final `auth.go` after implementing `handleRegister`:

```go
package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

var bcryptCost = 12

var (
	dummyHashOnce   sync.Once
	dummyBcryptHash []byte
)

func getDummyHash() []byte {
	dummyHashOnce.Do(func() {
		dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("relay-auth-sentinel"), bcryptCost)
	})
	return dummyBcryptHash
}

func (s *Server) issueToken(ctx context.Context, userID pgtype.UUID) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])
	expires := time.Now().Add(30 * 24 * time.Hour)
	if _, err := s.q.CreateToken(ctx, store.CreateTokenParams{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return "", time.Time{}, err
	}
	return rawHex, expires, nil
}

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
	if req.InviteToken == "" {
		writeError(w, http.StatusBadRequest, "invite_token is required")
		return
	}

	ctx := r.Context()

	sum := sha256.Sum256([]byte(req.InviteToken))
	tokenHash := hex.EncodeToString(sum[:])
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
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	rows, err := txq.MarkInviteUsed(ctx, store.MarkInviteUsedParams{
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

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit registration")
		return
	}

	token, expires, err := s.issueToken(ctx, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      token,
		"expires_at": expires,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}
```

- [ ] **Step 5: Run the register tests — expect all pass**

```bash
go test -tags integration -p 1 ./internal/api/... -run TestRegister -v -timeout 120s
```

Expected: all `TestRegister_*` tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/auth.go internal/api/export_test.go internal/api/auth_integration_test.go
git commit -m "feat(api): implement POST /v1/auth/register with invite validation and bcrypt password"
```

---

## Task 5: TDD — POST /v1/auth/login

**Files:**
- Modify: `internal/api/auth_integration_test.go` (add login tests)
- Modify: `internal/api/auth.go` (implement handleLogin)

- [ ] **Step 1: Add login tests to auth_integration_test.go**

Append to `internal/api/auth_integration_test.go`:

```go
// ── Login ─────────────────────────────────────────────────────────────────────

func TestLogin_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	admin := createTestUser(t, q, "Admin", "admin@test.com", true)
	inviteToken := createTestInvite(t, q, admin.ID, nil, 72*time.Hour)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})

	// Register first.
	regBody, _ := json.Marshal(map[string]string{
		"email": "alice@test.com", "password": "alicepassword",
		"invite_token": inviteToken,
	})
	regReq := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(regBody)))
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(regRec, regReq)
	require.Equal(t, http.StatusCreated, regRec.Code)

	// Now login.
	loginBody, _ := json.Marshal(map[string]string{
		"email": "alice@test.com", "password": "alicepassword",
	})
	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(loginBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp["token"])
}

func TestLogin_WrongPassword(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	admin := createTestUser(t, q, "Admin", "admin@test.com", true)
	inviteToken := createTestInvite(t, q, admin.ID, nil, 72*time.Hour)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})

	regBody, _ := json.Marshal(map[string]string{
		"email": "alice@test.com", "password": "correctpassword",
		"invite_token": inviteToken,
	})
	regReq := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(regBody)))
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(regRec, regReq)
	require.Equal(t, http.StatusCreated, regRec.Code)

	loginBody, _ := json.Marshal(map[string]string{
		"email": "alice@test.com", "password": "wrongpassword",
	})
	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(loginBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "invalid email or password", resp["error"])
}

func TestLogin_UnknownEmail_SameErrorAsWrongPassword(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})

	loginBody, _ := json.Marshal(map[string]string{
		"email": "nobody@test.com", "password": "anypassword",
	})
	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(loginBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "invalid email or password", resp["error"])
}
```

- [ ] **Step 2: Run the login tests — expect failure**

```bash
go test -tags integration -p 1 ./internal/api/... -run TestLogin -v -timeout 120s
```

Expected: all `TestLogin_*` tests FAIL with `501 Not Implemented`.

- [ ] **Step 3: Implement handleLogin in auth.go**

Replace the `handleLogin` stub in `internal/api/auth.go` with:

```go
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	user, err := s.q.GetUserByEmail(ctx, req.Email)

	var hashToCompare []byte
	if err == nil {
		hashToCompare = []byte(user.PasswordHash)
	} else if errors.Is(err, pgx.ErrNoRows) {
		hashToCompare = getDummyHash()
	} else {
		writeError(w, http.StatusInternalServerError, "failed to look up user")
		return
	}

	bcryptErr := bcrypt.CompareHashAndPassword(hashToCompare, []byte(req.Password))
	if bcryptErr != nil || errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	token, expires, err := s.issueToken(ctx, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      token,
		"expires_at": expires,
	})
}
```

- [ ] **Step 4: Run login tests — expect all pass**

```bash
go test -tags integration -p 1 ./internal/api/... -run TestLogin -v -timeout 120s
```

Expected: all `TestLogin_*` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/auth.go internal/api/auth_integration_test.go
git commit -m "feat(api): implement POST /v1/auth/login with bcrypt verification and enumeration protection"
```

---

## Task 6: TDD — PUT /v1/users/me/password

**Files:**
- Modify: `internal/api/auth_integration_test.go` (add password change tests)
- Modify: `internal/api/auth.go` (implement handleChangePassword)

- [ ] **Step 1: Add change-password tests to auth_integration_test.go**

Append to `internal/api/auth_integration_test.go`:

```go
// ── Change password ──────────────────────────────────────────────────────────

func registerAndLogin(t *testing.T, srv *api.Server, q *store.Queries, email, password string) string {
	t.Helper()
	admin := createTestUser(t, q, "Admin", "admin-"+email, true)
	inviteToken := createTestInvite(t, q, admin.ID, nil, 72*time.Hour)

	regBody, _ := json.Marshal(map[string]string{
		"email": email, "password": password, "invite_token": inviteToken,
	})
	regReq := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(regBody)))
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(regRec, regReq)
	require.Equal(t, http.StatusCreated, regRec.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(regRec.Body).Decode(&resp))
	return resp["token"].(string)
}

func TestChangePassword_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})
	token := registerAndLogin(t, srv, q, "alice@test.com", "oldpassword")

	body, _ := json.Marshal(map[string]string{
		"current_password": "oldpassword", "new_password": "newpassword1",
	})
	req := httptest.NewRequest("PUT", "/v1/users/me/password", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Old password no longer works.
	loginBody, _ := json.Marshal(map[string]string{
		"email": "alice@test.com", "password": "oldpassword",
	})
	loginReq := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(loginBody)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(loginRec, loginReq)
	assert.Equal(t, http.StatusUnauthorized, loginRec.Code)

	// New password works.
	loginBody2, _ := json.Marshal(map[string]string{
		"email": "alice@test.com", "password": "newpassword1",
	})
	loginReq2 := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(loginBody2)))
	loginReq2.Header.Set("Content-Type", "application/json")
	loginRec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(loginRec2, loginReq2)
	assert.Equal(t, http.StatusCreated, loginRec2.Code)
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})
	token := registerAndLogin(t, srv, q, "bob@test.com", "correctpassword")

	body, _ := json.Marshal(map[string]string{
		"current_password": "wrongpassword", "new_password": "newpassword1",
	})
	req := httptest.NewRequest("PUT", "/v1/users/me/password", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "current password is incorrect", resp["error"])
}

func TestChangePassword_NewPasswordTooShort(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})
	token := registerAndLogin(t, srv, q, "carol@test.com", "correctpassword")

	body, _ := json.Marshal(map[string]string{
		"current_password": "correctpassword", "new_password": "short",
	})
	req := httptest.NewRequest("PUT", "/v1/users/me/password", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "password must be at least 8 characters", resp["error"])
}

func TestChangePassword_NoToken(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})

	body, _ := json.Marshal(map[string]string{
		"current_password": "old", "new_password": "newpassword1",
	})
	req := httptest.NewRequest("PUT", "/v1/users/me/password", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
```

- [ ] **Step 2: Run change-password tests — expect failure**

```bash
go test -tags integration -p 1 ./internal/api/... -run TestChangePassword -v -timeout 120s
```

Expected: all `TestChangePassword_*` tests FAIL with `501 Not Implemented`.

- [ ] **Step 3: Implement handleChangePassword in auth.go**

Replace the `handleChangePassword` stub in `internal/api/auth.go` with:

```go
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	authUser, _ := UserFromCtx(r.Context())
	ctx := r.Context()

	user, err := s.q.GetUser(ctx, authUser.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up user")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		writeError(w, http.StatusForbidden, "current password is incorrect")
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	if err := s.q.SetPasswordHash(ctx, store.SetPasswordHashParams{
		ID:           authUser.ID,
		PasswordHash: string(newHash),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Run change-password tests — expect all pass**

```bash
go test -tags integration -p 1 ./internal/api/... -run TestChangePassword -v -timeout 120s
```

Expected: all `TestChangePassword_*` tests PASS.

- [ ] **Step 5: Run all API integration tests**

```bash
go test -tags integration -p 1 ./internal/api/... -v -timeout 120s
```

Expected: all tests PASS (including TestHealth, TestCreateAndGetJob, etc.).

- [ ] **Step 6: Commit**

```bash
git add internal/api/auth.go internal/api/auth_integration_test.go
git commit -m "feat(api): implement PUT /v1/users/me/password with current-password verification"
```

---

## Task 7: Update bootstrap — tests and implementation

**Files:**
- Modify: `cmd/relay-server/bootstrap_test.go`

- [ ] **Step 1: Update bootstrap_test.go**

Replace the entire `cmd/relay-server/bootstrap_test.go` with:

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
	"golang.org/x/crypto/bcrypt"
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

func createUserWithTestPassword(t *testing.T, q *store.Queries, name, email string, isAdmin bool) store.User {
	t.Helper()
	ph, err := bcrypt.GenerateFromPassword([]byte("testpass"), bcrypt.MinCost)
	require.NoError(t, err)
	user, err := q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name: name, Email: email, IsAdmin: isAdmin, PasswordHash: string(ph),
	})
	require.NoError(t, err)
	return user
}

func TestBootstrapAdmin_NoUsers_CreatesAdmin(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	require.NoError(t, bootstrapAdmin(ctx, q, "admin@example.com", "bootstrappassword"))

	user, err := q.GetUserByEmail(ctx, "admin@example.com")
	require.NoError(t, err)
	assert.True(t, user.IsAdmin)
	assert.Equal(t, "admin@example.com", user.Email)
	assert.NotEmpty(t, user.PasswordHash)
}

func TestBootstrapAdmin_ExistingUser_Promotes(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	createUserWithTestPassword(t, q, "Bob", "admin@example.com", false)

	require.NoError(t, bootstrapAdmin(ctx, q, "admin@example.com", "bootstrappassword"))

	user, err := q.GetUserByEmail(ctx, "admin@example.com")
	require.NoError(t, err)
	assert.True(t, user.IsAdmin)
}

func TestBootstrapAdmin_AdminAlreadyExists_Skips(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	createUserWithTestPassword(t, q, "Existing Admin", "other@example.com", true)

	require.NoError(t, bootstrapAdmin(ctx, q, "new@example.com", "bootstrappassword"))

	_, err := q.GetUserByEmail(ctx, "new@example.com")
	require.Error(t, err, "expected no user created when an admin already exists")
}
```

- [ ] **Step 2: Run bootstrap integration tests — expect all pass**

```bash
go test -tags integration -p 1 ./cmd/relay-server/... -run TestBootstrap -v -timeout 120s
```

Expected: all `TestBootstrapAdmin_*` tests PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-server/bootstrap.go cmd/relay-server/bootstrap_test.go cmd/relay-server/main.go
git commit -m "feat(server): update bootstrapAdmin to accept and hash password; require RELAY_BOOTSTRAP_PASSWORD"
```

---

## Task 8: Update CLI login command

**Files:**
- Modify: `internal/cli/login.go`
- Modify: `internal/cli/login_test.go`

- [ ] **Step 1: Update login_test.go**

Replace `internal/cli/login_test.go` with:

```go
// internal/cli/login_test.go
package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func withMockPassword(t *testing.T, password string) {
	t.Helper()
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return password, nil
	}
	t.Cleanup(func() { readPasswordFn = orig })
}

func TestRunLogin_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/auth/login", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "user@example.com", body["email"])
		require.Equal(t, "mypassword1", body["password"])
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"token":      "tok-abc",
			"expires_at": time.Now().Add(30 * 24 * time.Hour),
		})
	}))
	defer srv.Close()

	var saved *Config
	origSave := saveConfigFn
	saveConfigFn = func(cfg *Config) error { saved = cfg; return nil }
	t.Cleanup(func() { saveConfigFn = origSave })

	withMockPassword(t, "mypassword1")

	cfg := &Config{ServerURL: srv.URL}
	input := strings.NewReader("\nuser@example.com\n") // accept URL, type email
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.NoError(t, err)

	require.NotNil(t, saved)
	require.Equal(t, srv.URL, saved.ServerURL)
	require.Equal(t, "tok-abc", saved.Token)
	require.Contains(t, out.String(), "Logged in")
}

func TestRunLogin_EmptyEmailReturnsError(t *testing.T) {
	withMockPassword(t, "mypassword1")
	cfg := &Config{ServerURL: "http://localhost"}
	input := strings.NewReader("\n\n") // blank URL, blank email
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "email is required")
}

func TestRunLogin_EmptyPasswordReturnsError(t *testing.T) {
	withMockPassword(t, "")
	cfg := &Config{ServerURL: "http://localhost"}
	input := strings.NewReader("\nuser@example.com\n")
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "password is required")
}

func TestRunLogin_ServerReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid email or password"})
	}))
	defer srv.Close()

	withMockPassword(t, "wrongpassword")
	cfg := &Config{ServerURL: srv.URL}
	input := strings.NewReader("\nuser@example.com\n")
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid email or password")
}
```

- [ ] **Step 2: Run login tests — expect failure**

```bash
go test ./internal/cli/... -run TestRunLogin -v -timeout 30s
```

Expected: `TestRunLogin_Success` fails because `login.go` still hits `/v1/auth/token` and doesn't prompt for a password.

- [ ] **Step 3: Update login.go**

Replace `internal/cli/login.go` with:

```go
// internal/cli/login.go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

var saveConfigFn = SaveConfig

// readPasswordFn reads a masked password prompt. Tests override this to avoid
// requiring a real terminal file descriptor.
var readPasswordFn = func(out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

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

	password, err := readPasswordFn(out, "Password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if password == "" {
		return fmt.Errorf("password is required")
	}

	type loginRequest struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	c := NewClient(serverURL, "")
	var resp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}

	if err := c.do(ctx, "POST", "/v1/auth/login", loginRequest{Email: email, Password: password}, &resp); err != nil {
		return fmt.Errorf("login failed: %w", err)
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

- [ ] **Step 4: Run login tests — expect all pass**

```bash
go test ./internal/cli/... -run TestRunLogin -v -timeout 30s
```

Expected: all `TestRunLogin_*` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/login.go internal/cli/login_test.go
git commit -m "feat(cli): update relay login to prompt for password and use POST /v1/auth/login"
```

---

## Task 9: Add CLI register command

**Files:**
- Create: `internal/cli/register_test.go`
- Create: `internal/cli/register.go`

- [ ] **Step 1: Write register_test.go**

Create `internal/cli/register_test.go`:

```go
// internal/cli/register_test.go
package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunRegister_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/auth/register", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "user@example.com", body["email"])
		require.Equal(t, "Alice", body["name"])
		require.Equal(t, "myinvite123", body["invite_token"])
		require.Equal(t, "mypassword1", body["password"])
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"token":      "tok-reg",
			"expires_at": time.Now().Add(30 * 24 * time.Hour),
		})
	}))
	defer srv.Close()

	var saved *Config
	origSave := saveConfigFn
	saveConfigFn = func(cfg *Config) error { saved = cfg; return nil }
	t.Cleanup(func() { saveConfigFn = origSave })

	// readPasswordFn will be called twice: password + confirm.
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		callCount++
		return "mypassword1", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL}
	// URL (accept), email, name, invite token
	input := strings.NewReader("\nuser@example.com\nAlice\nmyinvite123\n")
	var out strings.Builder
	err := doRegister(context.Background(), cfg, input, &out)
	require.NoError(t, err)

	require.Equal(t, 2, callCount, "expected two password prompts (password + confirm)")
	require.NotNil(t, saved)
	require.Equal(t, "tok-reg", saved.Token)
	require.Contains(t, out.String(), "Registered and logged in")
}

func TestRunRegister_PasswordMismatch(t *testing.T) {
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return "password1234", nil
		}
		return "different1234", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: "http://localhost"}
	input := strings.NewReader("\nuser@example.com\n\nmyinvite\n")
	var out strings.Builder
	err := doRegister(context.Background(), cfg, input, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "passwords do not match")
}

func TestRunRegister_EmptyInviteToken(t *testing.T) {
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) { return "password1", nil }
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: "http://localhost"}
	input := strings.NewReader("\nuser@example.com\n\n\n") // blank invite token
	var out strings.Builder
	err := doRegister(context.Background(), cfg, input, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invite token is required")
}
```

- [ ] **Step 2: Run register tests — expect compile failure or test failure**

```bash
go test ./internal/cli/... -run TestRunRegister -v -timeout 30s
```

Expected: compile error — `doRegister` is undefined.

- [ ] **Step 3: Create register.go**

Create `internal/cli/register.go`:

```go
// internal/cli/register.go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// RegisterCommand returns the relay register Command.
func RegisterCommand() Command {
	return Command{
		Name:  "register",
		Usage: "create a new account using an invite token",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doRegister(ctx, cfg, stdinReader(), stderrWriter())
		},
	}
}

func doRegister(ctx context.Context, cfg *Config, in io.Reader, out io.Writer) error {
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

	fmt.Fprint(out, "Name (optional): ")
	name, _ := r.ReadString('\n')
	name = strings.TrimSpace(name)

	fmt.Fprint(out, "Invite token: ")
	inviteToken, _ := r.ReadString('\n')
	inviteToken = strings.TrimSpace(inviteToken)
	if inviteToken == "" {
		return fmt.Errorf("invite token is required")
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

	type registerRequest struct {
		Email       string `json:"email"`
		Name        string `json:"name,omitempty"`
		Password    string `json:"password"`
		InviteToken string `json:"invite_token"`
	}

	c := NewClient(serverURL, "")
	var resp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := c.do(ctx, "POST", "/v1/auth/register", registerRequest{
		Email:       email,
		Name:        name,
		Password:    password,
		InviteToken: inviteToken,
	}, &resp); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	cfg.ServerURL = serverURL
	cfg.Token = resp.Token
	if err := saveConfigFn(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(out, "Registered and logged in. Token expires %s.\n", resp.ExpiresAt.Format("2006-01-02"))
	return nil
}
```

- [ ] **Step 4: Run register tests — expect all pass**

```bash
go test ./internal/cli/... -run TestRunRegister -v -timeout 30s
```

Expected: all `TestRunRegister_*` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/register.go internal/cli/register_test.go
git commit -m "feat(cli): add relay register command"
```

---

## Task 10: Add CLI passwd command

**Files:**
- Create: `internal/cli/passwd_test.go`
- Create: `internal/cli/passwd.go`

- [ ] **Step 1: Write passwd_test.go**

Create `internal/cli/passwd_test.go`:

```go
// internal/cli/passwd_test.go
package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunPasswd_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PUT", r.Method)
		require.Equal(t, "/v1/users/me/password", r.URL.Path)
		require.Equal(t, "Bearer mytoken", r.Header.Get("Authorization"))
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "oldpassword", body["current_password"])
		require.Equal(t, "newpassword1", body["new_password"])
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	passwords := []string{"oldpassword", "newpassword1", "newpassword1"}
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		p := passwords[callCount]
		callCount++
		return p, nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "mytoken"}
	var out strings.Builder
	err := doPasswd(context.Background(), cfg, &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), "Password changed")
}

func TestRunPasswd_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	var out strings.Builder
	err := doPasswd(context.Background(), cfg, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}

func TestRunPasswd_PasswordMismatch(t *testing.T) {
	passwords := []string{"oldpassword", "newpassword1", "different1234"}
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		p := passwords[callCount]
		callCount++
		return p, nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: "http://localhost", Token: "mytoken"}
	var out strings.Builder
	err := doPasswd(context.Background(), cfg, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "passwords do not match")
}

func TestRunPasswd_ServerReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "current password is incorrect"})
	}))
	defer srv.Close()

	passwords := []string{"wrongpassword", "newpassword1", "newpassword1"}
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		p := passwords[callCount]
		callCount++
		return p, nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "mytoken"}
	var out strings.Builder
	err := doPasswd(context.Background(), cfg, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "current password is incorrect")
}
```

- [ ] **Step 2: Run passwd tests — expect compile failure**

```bash
go test ./internal/cli/... -run TestRunPasswd -v -timeout 30s
```

Expected: compile error — `doPasswd` is undefined.

- [ ] **Step 3: Create passwd.go**

Create `internal/cli/passwd.go`:

```go
// internal/cli/passwd.go
package cli

import (
	"context"
	"fmt"
	"io"
)

// PasswdCommand returns the relay passwd Command.
func PasswdCommand() Command {
	return Command{
		Name:  "passwd",
		Usage: "change your password",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doPasswd(ctx, cfg, stderrWriter())
		},
	}
}

func doPasswd(ctx context.Context, cfg *Config, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	current, err := readPasswordFn(out, "Current password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if current == "" {
		return fmt.Errorf("current password is required")
	}

	newPass, err := readPasswordFn(out, "New password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if newPass == "" {
		return fmt.Errorf("new password is required")
	}

	confirm, err := readPasswordFn(out, "Confirm new password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if newPass != confirm {
		return fmt.Errorf("passwords do not match")
	}

	c := cfg.NewClient()
	if err := c.do(ctx, "PUT", "/v1/users/me/password", map[string]string{
		"current_password": current,
		"new_password":     newPass,
	}, nil); err != nil {
		return fmt.Errorf("change password failed: %w", err)
	}

	fmt.Fprintln(out, "Password changed.")
	return nil
}
```

- [ ] **Step 4: Run passwd tests — expect all pass**

```bash
go test ./internal/cli/... -run TestRunPasswd -v -timeout 30s
```

Expected: all `TestRunPasswd_*` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/passwd.go internal/cli/passwd_test.go
git commit -m "feat(cli): add relay passwd command"
```

---

## Task 11: Wire new commands + README update

**Files:**
- Modify: `cmd/relay/main.go`
- Modify: `README.md`

- [ ] **Step 1: Add RegisterCommand and PasswdCommand to main.go**

In `cmd/relay/main.go`, replace:
```go
commands := []cli.Command{
    cli.LoginCommand(),
    cli.InviteCommand(),
```
with:
```go
commands := []cli.Command{
    cli.LoginCommand(),
    cli.RegisterCommand(),
    cli.PasswdCommand(),
    cli.InviteCommand(),
```

- [ ] **Step 2: Verify full build**

```bash
go build ./...
```

Expected: exits 0.

- [ ] **Step 3: Verify all unit tests pass**

```bash
go test ./... -timeout 120s
```

Expected: all packages pass.

- [ ] **Step 4: Add TLS note to README**

In `README.md`, find the authentication or deployment section (or append before the closing section) and add:

```markdown
## Transport Security

Relay's HTTP server does not handle TLS directly. When passwords are in use, deploy Relay behind a TLS-terminating reverse proxy to protect credentials in transit.

**Example — Caddy (`Caddyfile`):**
```
relay.internal {
    reverse_proxy localhost:8080
}
```
Caddy automatically provisions a certificate from your internal CA or Let's Encrypt. No changes to Relay's configuration are needed.

**Example — nginx (`/etc/nginx/conf.d/relay.conf`):**
```
server {
    listen 443 ssl;
    server_name relay.internal;
    ssl_certificate     /etc/ssl/certs/relay.crt;
    ssl_certificate_key /etc/ssl/private/relay.key;
    location / {
        proxy_pass http://127.0.0.1:8080;
    }
}
```
```

- [ ] **Step 5: Run integration tests to confirm everything still works end-to-end**

```bash
go test -tags integration -p 1 ./... -timeout 300s
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add cmd/relay/main.go README.md
git commit -m "feat(cli): wire relay register and passwd commands; add TLS reverse proxy note to README"
```

---

## Self-Review Checklist

**Spec coverage:**

| Requirement | Task |
|-------------|------|
| `password_hash` column on users | Task 1–2 |
| `POST /v1/auth/register` | Task 4 |
| `POST /v1/auth/login` | Task 5 |
| `PUT /v1/users/me/password` | Task 6 |
| Remove `POST /v1/auth/token` | Task 3 |
| Bootstrap admin with RELAY_BOOTSTRAP_PASSWORD | Task 7 |
| relay login prompts for password | Task 8 |
| relay register command | Task 9 |
| relay passwd command | Task 10 |
| Password min length 8 | Tasks 4, 6, 9, 10 |
| bcrypt cost 12 | Task 4 (auth.go) |
| Email enumeration prevention | Task 5 |
| Unit tests for all CLI commands | Tasks 8–10 |
| Integration tests for all API handlers | Tasks 4–6 |
| TLS proxy note in README | Task 11 |

All spec requirements are covered.
