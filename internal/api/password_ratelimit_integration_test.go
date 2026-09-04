//go:build integration

package api_test

import (
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// putPassword drives one PUT /v1/users/me/password through the handler h.
//
// h IS BOUND ONCE BY THE CALLER, never re-derived per request: Server.Handler
// allocates a fresh bucket for every armed user-keyed limiter on every call, so
// a test that calls srv.Handler() per request gives each request its own empty
// budget and can never observe a ceiling.
func putPassword(t *testing.T, h http.Handler, token, current, next string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"current_password": current, "new_password": next})
	require.NoError(t, err)
	req := httptest.NewRequest("PUT", "/v1/users/me/password", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestChangePassword_ABurstIsRefusedAndAnotherUserIsNot pins that a fourth
// attempt from one principal is refused before the handler runs, and that a
// second principal's first attempt is not.
//
// EVERY ATTEMPT USES A WRONG CURRENT PASSWORD, so each one that reaches the
// handler answers 403 having run the compare and changed nothing. That makes
// 403 and 429 the two outcomes, and they say different things: 403 is "the
// bcrypt compare ran", 429 is "it did not".
func TestChangePassword_ABurstIsRefusedAndAnotherUserIsNot(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	srv.PasswordChangeLimitN = 3
	srv.PasswordChangeLimitWin = time.Minute
	h := srv.Handler()

	tokenA := registerAndLogin(t, srv, q, "burst-a@test.com", "correctpassword")
	tokenB := registerAndLogin(t, srv, q, "burst-b@test.com", "correctpassword")

	for i := 1; i <= 3; i++ {
		rec := putPassword(t, h, tokenA, "wrongpassword", "newpassword1")
		require.Equal(t, http.StatusForbidden, rec.Code,
			"attempt %d must reach the handler and be refused by the compare. body: %s",
			i, rec.Body.String())
	}

	over := putPassword(t, h, tokenA, "wrongpassword", "newpassword1")
	require.Equal(t, http.StatusTooManyRequests, over.Code,
		"the fourth attempt must be refused BEFORE the bcrypt compare. A 403 here means the route "+
			"is unbounded and the compare ran. body: %s", over.Body.String())
	// PRESENCE ONLY. The header's VALUE is pinned for this exact middleware by
	// TestUserRateLimit_RetryAfterNamesWhenTheWindowActuallyClears, whose band
	// under a one-minute window is what kills a constant-retry mutation. This
	// slice adds no rate-limit arithmetic, so a second band assertion would kill
	// nothing.
	assert.NotEmpty(t, over.Header().Get("Retry-After"),
		"a refusal must tell the caller when to come back")

	other := putPassword(t, h, tokenB, "wrongpassword", "newpassword1")
	assert.Equal(t, http.StatusForbidden, other.Code,
		"the bucket is keyed on the authenticated user, so a second principal has its own budget")
}

// TestChangePassword_ANormalChangeSucceedsUnderTheBucket is the acceptance
// criterion "a normal password change is unaffected", proven end to end: with
// the bucket armed, one correct change answers 204 and the caller's own token
// still authenticates afterwards.
//
// WHY IT CANNOT RUN IN THE DEFAULT LANE, per CLAUDE.md's rule that a guard
// behind a build tag must be able to run. It is the only assertion in this slice
// that reaches SetPasswordHash and the commit, which needs a pool and a
// transaction. cmd/relay-server's wiring tests reach handleChangePassword with
// no Postgres but stop at its length guard, because stubAdminDB cannot answer
// GetUser. What would have to exist for this to run in CI is a services:postgres
// lane covering internal/api, which
// docs/superpowers/specs/2026-09-04-integration-guards-ci-coverage.md section
// 4.1 excludes on cost and which
// docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md tracks.
//
// IT CANNOT MEASURE THE COMPARE'S COST AT ALL. This lane calls
// api.SetBcryptCostForTest(), so every hash here is bcrypt.MinCost and the
// compare is cheap by construction.
func TestChangePassword_ANormalChangeSucceedsUnderTheBucket(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	srv.PasswordChangeLimitN = 5
	srv.PasswordChangeLimitWin = time.Minute
	// BOUND ONCE. Handler allocates a fresh bucket per call, so re-deriving it
	// per request would give every request its own budget.
	h := srv.Handler()

	token := registerAndLogin(t, srv, q, "under-bucket@test.com", "oldpassword")

	rec := putPassword(t, h, token, "oldpassword", "newpassword1")
	require.Equal(t, http.StatusNoContent, rec.Code,
		"one correct change under an armed bucket must succeed. body: %s", rec.Body.String())

	probe := httptest.NewRequest("GET", "/v1/jobs", nil)
	probe.Header.Set("Authorization", "Bearer "+token)
	probeRec := httptest.NewRecorder()
	h.ServeHTTP(probeRec, probe)
	assert.Equal(t, http.StatusOK, probeRec.Code,
		"the caller's own token survives its own password change")

	// The login bucket is unarmed on this server (api.New(..., 0, 0, 0, 0)), so
	// sharing h with the requests above cannot refuse this one.
	login, err := json.Marshal(map[string]string{
		"email": "under-bucket@test.com", "password": "newpassword1",
	})
	require.NoError(t, err)
	loginReq := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(login)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	h.ServeHTTP(loginRec, loginReq)
	assert.Equal(t, http.StatusCreated, loginRec.Code,
		"the stored hash actually changed: the new password authenticates")
}
