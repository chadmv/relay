package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/stretchr/testify/require"
)

// putAsUser drives one PUT through the REAL http.Server buildHTTPServer
// returned, authenticated by stubAdminDB with no Postgres.
func putAsUser(t *testing.T, srv *http.Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer any-token-the-stub-resolves")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// passwordBucketServer builds a server whose ONLY configured subsystem is the
// password-change bucket.
//
// LEAVING EVERY OTHER LIMIT FIELD ZERO IS LOAD-BEARING, not incidental: it is
// what makes a crossed assignment in buildHTTPServer
// (s.PasswordChangeLimitN = d.searchLimitN) produce a zero limit, an unarmed
// bucket and a RED test rather than a plausible one.
//
// pool is nil on purpose: every request below is answered before any pool use,
// and stubAdminDB panics on Exec and Query, so a handler that grew a write or a
// multi-row read fails loudly here.
func passwordBucketServer(n int, win time.Duration) *http.Server {
	return buildHTTPServer(httpServerDeps{
		addr:                   "127.0.0.1:0",
		q:                      store.New(stubAdminDB{}),
		passwordChangeLimitN:   n,
		passwordChangeLimitWin: win,
	})
}

// TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit covers four
// separate properties that a source scan does not:
//
//   - the route is wrapped at all;
//   - the composition order puts the limiter AFTER BearerAuth. Written
//     passwordLimit(auth(h)) the limiter runs before anything has put a
//     principal in the context, userRateLimitKey fails closed, and the FIRST
//     request answers 401 instead of 400 - the first assertion below, and the
//     failure names the composition order rather than the ceiling;
//   - the limiter uses the value buildHTTPServer was GIVEN, not a fresh or
//     hard-coded one;
//   - the limiter is constructed ONCE, at Handler() time. Built inside a route
//     closure or per request, every request carries its own empty map and the
//     third answers 400.
//
// THE 400 IS LOAD-BEARING. handleChangePassword refuses `{}` at
// len(NewPassword) < 8, before GetUser and before either bcrypt call, so the
// first two requests prove they reached the real handler with no database at
// all. A 429 there would mean the wired count is smaller than the configured
// one.
func TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit(t *testing.T) {
	srv := passwordBucketServer(2, time.Minute)

	for i := 1; i <= 2; i++ {
		rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"request %d must reach handleChangePassword and be refused by its length guard. "+
				"A 401 here means the limiter sits OUTSIDE the auth chain. body: %s",
			i, rec.Body.String())
	}

	rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"the third request must be refused by the bucket buildHTTPServer was GIVEN. An unwrapped "+
			"route, a hard-coded count, a deleted or crossed assignment and a per-request limiter "+
			"all answer 400 here. body: %s", rec.Body.String())
	// PRESENCE ONLY. The header's VALUE is pinned for this exact middleware by
	// internal/api's TestUserRateLimit_RetryAfterNamesWhenTheWindowActuallyClears,
	// whose band under a one-minute window kills a constant-retry mutation. This
	// slice adds no rate-limit arithmetic, so a second band assertion here would
	// kill nothing.
	require.NotEmpty(t, rec.Header().Get("Retry-After"),
		"a refusal must tell the caller when to come back")
}
