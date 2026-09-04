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

// postAsUser drives one request through the REAL http.Server buildHTTPServer
// returned, authenticated by stubAdminDB with no Postgres.
func postAsUser(t *testing.T, srv *http.Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer any-token-the-stub-resolves")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// submitBucketServer builds a server whose only configured subsystem is the
// job-submit bucket. pool is nil on purpose: every request below is answered
// before any pool use, and stubAdminDB panics on any statement other than the
// bearer-auth lookup, so a handler that grew a query fails loudly here.
func submitBucketServer(n int, win time.Duration) *http.Server {
	return buildHTTPServer(httpServerDeps{
		addr:              "127.0.0.1:0",
		q:                 store.New(stubAdminDB{}),
		jobSubmitLimitN:   n,
		jobSubmitLimitWin: win,
	})
}

// TestBuildHTTPServer_TheJobSubmitBucketIsWiredWithTheConfiguredLimit is the
// strongest available wiring guard, and it is worth naming what it covers that a
// source scan does not: the route is wrapped, the composition order puts the
// limiter AFTER BearerAuth, and the limiter uses the value buildHTTPServer was
// GIVEN rather than a freshly constructed one.
//
// The 400 is load-bearing. ValidateJobSpec refuses `{}` for a missing name
// before handleCreateJob opens a transaction, so the first two requests prove
// they reached the real handler with no database at all. A 429 there would mean
// the wired count is smaller than the configured one; a 401 would mean the
// limiter sits OUTSIDE the auth chain and never sees a principal.
func TestBuildHTTPServer_TheJobSubmitBucketIsWiredWithTheConfiguredLimit(t *testing.T) {
	srv := submitBucketServer(2, time.Minute)

	for i := 1; i <= 2; i++ {
		rec := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"request %d must reach handleCreateJob and be refused by ValidateJobSpec. body: %s",
			i, rec.Body.String())
	}

	rec := postAsUser(t, srv, "/v1/jobs", `{}`)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"the third request must be refused by the bucket buildHTTPServer was GIVEN. A deleted "+
			"assignment, a hard-coded count and an unwrapped route all answer 400 here. body: %s",
		rec.Body.String())
	require.NotEmpty(t, rec.Header().Get("Retry-After"),
		"a refusal must tell the caller when to come back")
}

// TestBuildHTTPServer_RetryAndRunNowDrawOnTheSubmitBucket makes the
// one-bucket-not-three decision executable in the DEFAULT lane.
//
// IT NEEDS NO DATABASE, WHICH IS WHY IT IS HERE. A refusal is issued before the
// handler runs, so proving route B draws on route A's bucket never reaches a
// handler at all. The id is deliberately NOT a uuid: an unbucketed request is
// then answered 400 by the handler's own parseUUID with no pool, so 429 and 400
// are the two outcomes and they say different things. Reaching a SUCCESS code on
// these routes would need real handlers and a container; reaching the REFUSAL
// does not.
func TestBuildHTTPServer_RetryAndRunNowDrawOnTheSubmitBucket(t *testing.T) {
	cases := []struct{ name, path string }{
		{"retry", "/v1/jobs/not-a-uuid/retry"},
		{"run-now", "/v1/scheduled-jobs/not-a-uuid/run-now"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := submitBucketServer(1, time.Minute)

			spend := postAsUser(t, srv, "/v1/jobs", `{}`)
			require.Equal(t, http.StatusBadRequest, spend.Code,
				"the fixture must actually spend the budget on POST /v1/jobs. body: %s",
				spend.Body.String())

			rec := postAsUser(t, srv, tc.path, "")
			require.Equal(t, http.StatusTooManyRequests, rec.Code,
				"%s must draw on the SAME bucket POST /v1/jobs just spent. A 400 here means the "+
					"route is unwrapped or carries its own UserRateLimit instance, and a caller "+
					"alternating between the two gets twice the ceiling. body: %s",
				tc.path, rec.Body.String())
		})
	}
}
