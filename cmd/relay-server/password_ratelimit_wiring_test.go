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

// TestBuildHTTPServer_AHumansRetryRunIsNotRefused is the executable form of "a
// normal password change is unaffected" for the case that actually produces a
// burst: a user who mistypes their current password and retries. Five attempts
// inside one minute is more than the two shipped clients can produce by hand -
// the SPA disables its button while the mutation is pending and the CLI asks
// three masked prompts per attempt - so the default ceiling is above anything a
// person reaches.
//
// THE SIXTH REQUEST IS NOT OPTIONAL. Five 400s under a limit of five are also
// what a limiter that does nothing produces, so without it this test is vacuous
// against exactly the implementation it describes.
func TestBuildHTTPServer_AHumansRetryRunIsNotRefused(t *testing.T) {
	srv := passwordBucketServer(5, time.Minute)

	for i := 1; i <= 5; i++ {
		rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"attempt %d of a five-attempt retry run must reach the handler. body: %s",
			i, rec.Body.String())
	}

	over := putAsUser(t, srv, "/v1/users/me/password", `{}`)
	require.Equal(t, http.StatusTooManyRequests, over.Code,
		"the sixth attempt must be refused: without this the five 400s above are also what an "+
			"unwrapped route produces. body: %s", over.Body.String())
}

// TestBuildHTTPServer_ThePasswordBucketIsSeparateFromTheSubmitBucket makes the
// two-buckets decision executable rather than prose, in the lane CI runs.
//
// THE MIDDLE ASSERTION IN EACH DIRECTION IS THE CONTROL. Without proving the
// first bucket is FULL, the final 400 is also what a fixture whose limiter never
// ran produces, and the test would be green for the wrong reason.
//
// Both buckets are set to 1 so a single spend fills either one.
func TestBuildHTTPServer_ThePasswordBucketIsSeparateFromTheSubmitBucket(t *testing.T) {
	build := func() *http.Server {
		return buildHTTPServer(httpServerDeps{
			addr:                   "127.0.0.1:0",
			q:                      store.New(stubAdminDB{}),
			jobSubmitLimitN:        1,
			jobSubmitLimitWin:      time.Minute,
			passwordChangeLimitN:   1,
			passwordChangeLimitWin: time.Minute,
		})
	}

	t.Run("a spent submit budget does not refuse a password change", func(t *testing.T) {
		srv := build()

		spend := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusBadRequest, spend.Code, "body: %s", spend.Body.String())
		over := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusTooManyRequests, over.Code,
			"control: the submit bucket must be provably full before the assertion below means anything")

		rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"a spent submit budget must not refuse a password change: the two buckets bound "+
				"different quantities and sharing one would trade the wrong direction. body: %s",
			rec.Body.String())
	})

	t.Run("a spent password budget does not refuse a job submission", func(t *testing.T) {
		srv := build()

		spend := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, spend.Code, "body: %s", spend.Body.String())
		over := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusTooManyRequests, over.Code,
			"control: the password bucket must be provably full before the assertion below means anything")

		rec := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"a spent password budget must not refuse a job submission. body: %s", rec.Body.String())
	})
}

// TestBuildHTTPServer_AHalfConfiguredPasswordLimitLeavesTheBucketOff pins the
// field pair's promise: zero on EITHER field leaves the bucket off, which is a
// conjunction and not a disjunction.
//
// THE ZERO-COUNT ROW IS THE DISCRIMINATING ONE, and it is placed first because a
// poisoned input read after its target is read by neither the code nor the
// mutant. Relaxing the guard in Server.Handler to an OR constructs a limiter
// whose limit is 0, and rateLimiter.allow takes its over-limit branch on an
// empty window and indexes hits[0], so that row fails loudly on the first
// request. The zero-WINDOW row cannot discriminate against the same relaxation -
// a limiter with a zero window prunes every hit before it counts them and admits
// everything, exactly as no limiter does - and it is here to state the contract
// on the field the count row does not exercise.
func TestBuildHTTPServer_AHalfConfiguredPasswordLimitLeavesTheBucketOff(t *testing.T) {
	cases := []struct {
		name string
		n    int
		win  time.Duration
	}{
		{"window set, count zero", 0, time.Minute},
		{"count set, window zero", 5, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := passwordBucketServer(tc.n, tc.win)

			for i := 1; i <= 3; i++ {
				rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
				require.Equal(t, http.StatusBadRequest, rec.Code,
					"request %d: a half-configured pair must leave the bucket off, so every request "+
						"reaches the handler. body: %s", i, rec.Body.String())
			}
		})
	}
}
