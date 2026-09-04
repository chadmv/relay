//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
