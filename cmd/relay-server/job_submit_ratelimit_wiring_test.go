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
// before any pool use, and stubAdminDB panics on Exec and Query, so a handler
// that grew a write or a multi-row read fails loudly here. A QueryRow is
// answered rather than refused, so it is the nil pool, not the stub, that bounds
// a handler which grows a single-row read.
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

// TestBuildHTTPServer_ScheduleCreationIsNotInTheSubmitBucket makes the OUT
// verdict executable rather than prose.
//
// The verdict's reason: the hazard that asked for this limit is the SIZE of
// scheduled_jobs at boot, and a creation-rate limit bounds how fast the table
// fills, not how full it gets. And a schedule's own firing runs on the
// schedrunner goroutine and never touches an HTTP route, so no HTTP rate limit
// anywhere can bound it. The control that bounds the table is a per-owner count
// cap, which is a quota and is not this.
//
// THE MIDDLE ASSERTION IS THE CONTROL. Without proving the bucket is FULL, the
// final 400 is also what a fixture whose limiter never ran produces, and the
// test would be green for the wrong reason.
func TestBuildHTTPServer_ScheduleCreationIsNotInTheSubmitBucket(t *testing.T) {
	srv := submitBucketServer(1, time.Minute)

	spend := postAsUser(t, srv, "/v1/jobs", `{}`)
	require.Equal(t, http.StatusBadRequest, spend.Code, "body: %s", spend.Body.String())

	over := postAsUser(t, srv, "/v1/jobs", `{}`)
	require.Equal(t, http.StatusTooManyRequests, over.Code,
		"control: the bucket must be provably full before the assertion below means anything")

	// 400 exactly, not merely "not 429": handleCreateScheduledJob answers
	// `name is required` after readJSON and before any pool use, so the code is
	// determinate and a loosened assertion would lose coverage for nothing.
	rec := postAsUser(t, srv, "/v1/scheduled-jobs", `{}`)
	require.Equal(t, http.StatusBadRequest, rec.Code,
		"POST /v1/scheduled-jobs is deliberately OUT of this bucket and must reach its own "+
			"validation even when the submit budget is spent. body: %s", rec.Body.String())
}

// TestBuildHTTPServer_AZeroLimitLeavesTheBucketOff pins the guard in
// Server.Handler, which is NOT cosmetic. rateLimiter.allow indexes hits[0]
// whenever len(hits) >= limit, so constructing a limiter with a zero count
// panics on the first request rather than admitting it. The Go-constructed off
// state is what internal/api's own test server and the CLI harness both rely on;
// the environment cannot reach it, because ParseRateLimit refuses a zero count
// and main fatals on the error.
// TestBuildHTTPServer_AHalfConfiguredLimitLeavesTheBucketOff pins the other
// half of the Server field's promise: zero on EITHER of the pair leaves the
// bucket off, which is a conjunction and not a disjunction.
//
// THE ZERO-COUNT ROW IS THE DISCRIMINATING ONE. Relaxing the guard to an OR
// constructs a limiter whose limit is 0, and rateLimiter.allow takes its
// over-limit branch on an empty window and indexes hits[0], so that row fails
// loudly on the first request. The zero-WINDOW row cannot discriminate against
// the same relaxation - a limiter with a zero window prunes every hit before it
// counts them and so admits everything, exactly as no limiter does - and it is
// here to state the contract on the field the count row does not exercise.
func TestBuildHTTPServer_AHalfConfiguredLimitLeavesTheBucketOff(t *testing.T) {
	cases := []struct {
		name string
		n    int
		win  time.Duration
	}{
		{"count set, window zero", 5, 0},
		{"window set, count zero", 0, time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := submitBucketServer(tc.n, tc.win)

			for i := 1; i <= 3; i++ {
				rec := postAsUser(t, srv, "/v1/jobs", `{}`)
				require.Equal(t, http.StatusBadRequest, rec.Code,
					"request %d: a half-configured pair must leave the bucket off, so every "+
						"request reaches the handler. body: %s", i, rec.Body.String())
			}
		})
	}
}

func TestBuildHTTPServer_AZeroLimitLeavesTheBucketOff(t *testing.T) {
	srv := buildHTTPServer(httpServerDeps{
		addr: "127.0.0.1:0",
		q:    store.New(stubAdminDB{}),
		// jobSubmitLimitN and jobSubmitLimitWin deliberately unset.
	})

	for i := 1; i <= 3; i++ {
		rec := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"request %d: with no configured limit every request must reach the handler. A 429 means "+
				"a zero-count limiter was constructed; a panic means it was constructed AND allow "+
				"indexed an empty window. body: %s", i, rec.Body.String())
	}
}
