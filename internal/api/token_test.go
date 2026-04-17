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

func TestCreateToken_ExistingUser_NoInviteNeeded(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	_, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Alice", Email: "alice@test.com", IsAdmin: false,
	})
	require.NoError(t, err)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})

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
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})

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

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})

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
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Hour), Valid: true},
	})
	require.NoError(t, err)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})

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

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})

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

func TestCreateToken_NewUser_UsedInvite_BadRequest(t *testing.T) {
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
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(72 * time.Hour), Valid: true},
	})
	require.NoError(t, err)

	// Use the invite once successfully.
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})
	body, _ := json.Marshal(map[string]string{"email": "firstuser@test.com", "invite_token": rawHex})
	req := httptest.NewRequest("POST", "/v1/auth/token", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	// Try to reuse the same invite for a different user.
	body2, _ := json.Marshal(map[string]string{"email": "seconduser@test.com", "invite_token": rawHex})
	req2 := httptest.NewRequest("POST", "/v1/auth/token", strings.NewReader(string(body2)))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusBadRequest, rec2.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&resp))
	assert.Equal(t, "invite already used", resp["error"])
}

func TestCreateToken_NewUser_InvalidInviteToken_BadRequest(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), func() {})

	body, _ := json.Marshal(map[string]string{
		"email":        "newuser@test.com",
		"invite_token": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	})
	req := httptest.NewRequest("POST", "/v1/auth/token", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "invalid invite token", resp["error"])
}
