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

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
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

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
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
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
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
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
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

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
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

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

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

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
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

func TestRegister_DuplicateEmail(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	admin := createTestUser(t, q, "Admin", "admin@test.com", true)
	invite1 := createTestInvite(t, q, admin.ID, nil, 72*time.Hour)
	invite2 := createTestInvite(t, q, admin.ID, nil, 72*time.Hour)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	// Register once.
	body, _ := json.Marshal(map[string]string{
		"email": "alice@test.com", "password": "securepass1", "invite_token": invite1,
	})
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	// Try to register again with same email.
	body2, _ := json.Marshal(map[string]string{
		"email": "alice@test.com", "password": "securepass1", "invite_token": invite2,
	})
	req2 := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(string(body2)))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusConflict, rec2.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&resp))
	assert.Equal(t, "email already registered", resp["error"])
}

// ── Login ─────────────────────────────────────────────────────────────────────

func TestLogin_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	admin := createTestUser(t, q, "Admin", "admin@test.com", true)
	inviteToken := createTestInvite(t, q, admin.ID, nil, 72*time.Hour)

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

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

	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

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
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

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
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
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
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
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
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
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
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	body, _ := json.Marshal(map[string]string{
		"current_password": "old", "new_password": "newpassword1",
	})
	req := httptest.NewRequest("PUT", "/v1/users/me/password", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
