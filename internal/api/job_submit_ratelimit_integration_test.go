//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// postAs issues one request against a PRE-BUILT handler. The handler is built
// once by each caller and reused, which is not decoration: srv.Handler()
// constructs a fresh mux AND a fresh UserRateLimit with an empty map, so the
// per-request `srv.Handler().ServeHTTP(...)` idiom the rest of this package uses
// would give every request its own full bucket and make every assertion below
// vacuous.
func postAs(t *testing.T, h http.Handler, token, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const rlJobBody = `{"name":"rl","tasks":[{"name":"t","command":["echo","hi"]}]}`

// TestPostJobs_ABurstIsRefusedAndTheNextUserIsNot is the behavioural
// reproduction of the source item, end to end: without the bucket four
// submissions in a row all answer 201.
//
// BOB'S REQUEST IS THE ISOLATION PROOF AND IT IS NOT INCIDENTAL.
// httptest.NewRequest gives every request the same RemoteAddr, so Alice and Bob
// present from one address here by construction. A limiter keyed on the address
// refuses Bob; the one this slice ships does not.
func TestPostJobs_ABurstIsRefusedAndTheNextUserIsNot(t *testing.T) {
	srv, q := newTestServer(t)
	srv.JobSubmitLimitN = 3
	srv.JobSubmitLimitWin = time.Minute
	h := srv.Handler()

	alice := createTestUser(t, q, "Alice", "alice-ratelimit@example.com", false)
	aliceTok := createTestToken(t, q, alice.ID)
	bob := createTestUser(t, q, "Bob", "bob-ratelimit@example.com", false)
	bobTok := createTestToken(t, q, bob.ID)

	for i := 1; i <= 3; i++ {
		rec := postAs(t, h, aliceTok, "/v1/jobs", rlJobBody)
		require.Equal(t, http.StatusCreated, rec.Code,
			"submission %d is inside the budget. body: %s", i, rec.Body.String())
	}

	over := postAs(t, h, aliceTok, "/v1/jobs", rlJobBody)
	require.Equal(t, http.StatusTooManyRequests, over.Code,
		"the fourth submission is over the budget. body: %s", over.Body.String())
	require.NotEmpty(t, over.Header().Get("Retry-After"))

	next := postAs(t, h, bobTok, "/v1/jobs", rlJobBody)
	require.Equal(t, http.StatusCreated, next.Code,
		"bob's FIRST submission, from the same source address alice just filled her budget from, "+
			"must succeed. body: %s", next.Body.String())
}

// TestSubmitRunNowAndRetryShareOneBucket proves the one-bucket decision through
// three real handlers with real success codes.
//
// THE SEQUENCE IS THE ARGUMENT. At a limit of 2: the submit takes hit 1 and the
// run-now takes hit 2, so the retry is refused. If run-now had its own bucket
// the retry would find room and answer 409, because a job created a moment ago
// is pending and handleRetryJob admits only a done or failed job. 429 versus 409
// is therefore the discriminator, and it needs no worker simulation - which is
// why this does not drive a job to `failed` first.
//
// The default-lane sibling in cmd/relay-server carries the same decision without
// a container. This one adds what that cannot: two real 201s.
func TestSubmitRunNowAndRetryShareOneBucket(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Owner", "owner-ratelimit@example.com", false)
	token := createTestToken(t, q, user.ID)

	// Seed through a handler built while the bucket is OFF, so the fixtures do
	// not spend the budget the assertions depend on.
	seed := srv.Handler()

	jobRec := postAs(t, seed, token, "/v1/jobs", rlJobBody)
	require.Equal(t, http.StatusCreated, jobRec.Code, "body: %s", jobRec.Body.String())
	var job struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(jobRec.Body.Bytes(), &job))
	require.NotEmpty(t, job.ID)

	// "0 3 * * *" is a day apart, comfortably above minScheduleInterval.
	schedRec := postAs(t, seed, token, "/v1/scheduled-jobs", `{
		"name": "rl-schedule",
		"cron_expr": "0 3 * * *",
		"timezone": "UTC",
		"overlap_policy": "skip",
		"job_spec": {"name":"rl-template","tasks":[{"name":"t","command":["echo","x"]}]}
	}`)
	require.Equal(t, http.StatusCreated, schedRec.Code, "body: %s", schedRec.Body.String())
	var sched struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(schedRec.Body.Bytes(), &sched))
	require.NotEmpty(t, sched.ID)

	srv.JobSubmitLimitN = 2
	srv.JobSubmitLimitWin = time.Minute
	h := srv.Handler()

	first := postAs(t, h, token, "/v1/jobs", rlJobBody)
	require.Equal(t, http.StatusCreated, first.Code, "hit 1. body: %s", first.Body.String())

	second := postAs(t, h, token, "/v1/scheduled-jobs/"+sched.ID+"/run-now", "")
	require.Equal(t, http.StatusCreated, second.Code, "hit 2. body: %s", second.Body.String())

	third := postAs(t, h, token, "/v1/jobs/"+job.ID+"/retry?task=all", "")
	require.Equal(t, http.StatusTooManyRequests, third.Code,
		"the retry must be refused by the budget the submit and the run-now spent between them. A "+
			"409 here means run-now has its own bucket and the retry found room. body: %s",
		third.Body.String())
}
