package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func searchTestUser(b byte) AuthUser {
	var raw [16]byte
	raw[15] = b
	return AuthUser{ID: pgtype.UUID{Bytes: raw, Valid: true}, Email: "caller@example.com"}
}

// ONE LIMITER PER SERVER, not one per call. RateLimit and UserRateLimit each
// mint a fresh rateLimiter and start an unstoppable `go rl.gcLoop()` on every
// invocation, so a Server whose Handler() ran twice leaks a goroutine and splits
// its budget across two maps. The only way to see that from a test is to ask for
// the limiter twice and compare identities.
func TestSearchLimiter_IsConstructedOncePerServer(t *testing.T) {
	s := &Server{SearchLimitN: 3, SearchLimitWin: time.Minute}
	assert.Same(t, s.searchRateLimiter(), s.searchRateLimiter(),
		"a second call must return the same limiter; two maps means two budgets")
}

// The zero value means NO LIMIT, which is what keeps every existing
// construction of api.Server - including every test in this package - unchanged.
func TestSearchLimiter_ZeroFieldsDisableTheBucket(t *testing.T) {
	for _, s := range []*Server{
		{},
		{SearchLimitN: 3},
		{SearchLimitWin: time.Minute},
		{SearchLimitN: -1, SearchLimitWin: time.Minute},
	} {
		assert.Nil(t, s.searchRateLimiter(),
			"N=%d win=%s must leave the bucket unarmed", s.SearchLimitN, s.SearchLimitWin)
	}
}

// The FIRST assertion is the discriminator: an unidentified caller is refused
// with 401 and creates NO map key at all. That is a real security property of
// failing closed and not only a status code - an unauthenticated flood must not
// be able to grow the limiter's map, which is what makes this limiter's key
// space different from the IP-keyed ones.
func TestAllowSearch_UnidentifiedCallerIs401AndCreatesNoKey(t *testing.T) {
	s := &Server{SearchLimitN: 2, SearchLimitWin: time.Minute}
	rl := s.searchRateLimiter()
	require.NotNil(t, rl)

	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		require.False(t, s.allowSearch(rec, AuthUser{}), "attempt %d", i)
		require.Equal(t, 401, rec.Code, "attempt %d", i)
		assert.JSONEq(t, `{"error":"unauthorized"}`, rec.Body.String())
	}

	rl.mu.Lock()
	keys := len(rl.windows)
	rl.mu.Unlock()
	assert.Zero(t, keys,
		"a fail-closed key creates no map entry, so an unidentified flood cannot grow the limiter's map")
}

func TestAllowSearch_ChargesTheCeilingPerUser(t *testing.T) {
	s := &Server{SearchLimitN: 2, SearchLimitWin: time.Minute}

	alice, bob := searchTestUser(1), searchTestUser(2)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		assert.True(t, s.allowSearch(rec, alice), "alice attempt %d must be allowed", i)
		assert.Equal(t, 200, rec.Code, "an allowed call writes nothing, so the recorder keeps its default")
	}

	rec := httptest.NewRecorder()
	require.False(t, s.allowSearch(rec, alice))
	assert.Equal(t, 429, rec.Code)
	assert.JSONEq(t, `{"error":"search rate limit exceeded"}`, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("Retry-After"),
		"correct HTTP for a scripted client. No first-party client reads it - ApiError carries no "+
			"headers and apiFetch never touches res.headers - and README says so where the header is "+
			"documented.")

	other := httptest.NewRecorder()
	assert.True(t, s.allowSearch(other, bob),
		"the key is the user id, so a second principal has its own budget")
}

// An unarmed bucket must let everything through INCLUDING an unidentified
// caller, or every existing zero-valued test Server changes behaviour.
func TestAllowSearch_UnarmedBucketAllowsEverything(t *testing.T) {
	s := &Server{}
	for _, u := range []AuthUser{searchTestUser(1), {}} {
		rec := httptest.NewRecorder()
		assert.True(t, s.allowSearch(rec, u))
		assert.Equal(t, 200, rec.Code)
	}
}

// countingDB is a store.DBTX that records every statement and refuses all of
// them, so a request that reaches the database answers 500 and a request that
// does not reaches nothing. Refusing rather than returning plausible zeros is
// deliberate: 500 and 429 are then distinguishable at the recorder, with no
// Postgres and no fixture rows.
type countingDB struct{ calls int }

var errRefusedByStub = errors.New("countingDB refuses every statement")

func (d *countingDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	d.calls++
	return pgconn.CommandTag{}, errRefusedByStub
}

func (d *countingDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	d.calls++
	return nil, errRefusedByStub
}

func (d *countingDB) QueryRow(context.Context, string, ...any) pgx.Row {
	d.calls++
	return refusingRow{}
}

type refusingRow struct{}

func (refusingRow) Scan(...any) error { return errRefusedByStub }

// listJobsAs drives handleListJobs directly with an injected identity. Direct
// rather than through Handler(), because BearerAuth would need a token row and
// because the 401 this slice introduces is only reachable this way.
func listJobsAs(t *testing.T, s *Server, u AuthUser, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/jobs?"+rawQuery, nil)
	req = req.WithContext(ctxWithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	s.handleListJobs(rec, req)
	return rec
}

func newSearchTestServer(t *testing.T, n int, win time.Duration) (*Server, *countingDB) {
	t.Helper()
	db := &countingDB{}
	return &Server{q: store.New(db), SearchLimitN: n, SearchLimitWin: win}, db
}

// THE TEST THAT PROVES THE PLACEMENT, and the whitespace rows go FIRST because a
// discriminating input placed last misses an early-exit mutation.
//
// ?q=%20%20 is PRESENT to r.URL.Query().Get("q") and ABSENT to parseFilterQ,
// which trims it. The ceiling here is 1, so if the bucket were charged by a
// middleware predicate testing Get("q") != "", the whitespace requests would
// exhaust it and the FIRST real needle would be refused. Charged where the
// needle is known to be non-nil, they cost nothing and the first needle is
// allowed.
//
// It kills the middleware form of this control outright: not "the middleware is
// less tidy" but "the middleware counts a set that is not the expensive set".
func TestListJobs_WhitespaceOnlyQIsNotCounted(t *testing.T) {
	s, _ := newSearchTestServer(t, 1, time.Minute)
	u := searchTestUser(1)

	for _, q := range []string{"q=%20%20", "q=%20%09%20", "q="} {
		rec := listJobsAs(t, s, u, q)
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code,
			"%s trims to absent and must not be charged: this is the exact input a middleware "+
				"predicate would count and the in-handler placement cannot", q)
	}

	first := listJobsAs(t, s, u, "q=needle")
	require.NotEqual(t, http.StatusTooManyRequests, first.Code,
		"the whole budget must still be here; if this is 429 the check is counting absent needles")

	second := listJobsAs(t, s, u, "q=needle")
	assert.Equal(t, http.StatusTooManyRequests, second.Code,
		"and the budget must be real: a check that counts nothing would let this through too")
}

// Unfiltered polling is UNAFFECTED, proven rather than asserted. Same handler,
// same user, same limiter instance, so the needle is the only discriminator.
func TestListJobs_UnfilteredPollingIsNotCounted(t *testing.T) {
	s, _ := newSearchTestServer(t, 2, time.Minute)
	u := searchTestUser(1)

	for i := 0; i < 7; i++ {
		rec := listJobsAs(t, s, u, "limit=10")
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code,
			"unfiltered request %d was refused; the 3 s SPA poll must never reach this bucket", i)
	}

	for i := 0; i < 2; i++ {
		rec := listJobsAs(t, s, u, "q=needle")
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "needle request %d", i)
	}
	assert.Equal(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)
}

// A REFUSED NEEDLE COSTS NO BUDGET, and the two 400 rows go first. An over-long
// or non-UTF-8 q never reaches a statement, so charging for it would let a
// caller spend budget on requests that cost the database nothing.
func TestListJobs_RejectedNeedleCostsNoBudget(t *testing.T) {
	s, _ := newSearchTestServer(t, 2, time.Minute)
	u := searchTestUser(1)

	tooLong := listJobsAs(t, s, u, "q="+strings.Repeat("a", maxFilterQRunes+1))
	require.Equal(t, http.StatusBadRequest, tooLong.Code)
	assert.JSONEq(t, `{"error":"`+maxFilterQMessage+`"}`, tooLong.Body.String())

	badUTF8 := listJobsAs(t, s, u, "q=%FF%FE")
	require.Equal(t, http.StatusBadRequest, badUTF8.Code)
	assert.JSONEq(t, `{"error":"q is not valid UTF-8"}`, badUTF8.Body.String())

	for i := 0; i < 2; i++ {
		rec := listJobsAs(t, s, u, "q=needle")
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code,
			"needle request %d: the two 400s above must have left the budget untouched", i)
	}
	assert.Equal(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)
}

// A 400 OUTRANKS THE 429. The input carries BOTH conditions: the budget is
// already spent AND the needle is malformed. README documents the precedence
// direction for this endpoint's 400s; this extends the same rule to the new
// refusal.
func TestListJobs_MalformedQOutranksTheRateLimit(t *testing.T) {
	s, _ := newSearchTestServer(t, 1, time.Minute)
	u := searchTestUser(1)

	require.NotEqual(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)
	require.Equal(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code,
		"fixture: the budget must be exhausted, or the assertion below proves nothing")

	rec := listJobsAs(t, s, u, "q="+strings.Repeat("a", maxFilterQRunes+1))
	assert.Equal(t, http.StatusBadRequest, rec.Code,
		"a malformed needle answers 400 even with the budget gone: a 429 here would tell a caller "+
			"to slow down about a request that will never be valid")
}

// A 401 OUTRANKS THE 429 TOO, on the same input, and by construction: the key is
// computed before the bucket is consulted.
func TestListJobs_MissingIdentityIs401NotA429(t *testing.T) {
	s, db := newSearchTestServer(t, 1, time.Minute)

	rec := listJobsAs(t, s, AuthUser{}, "q=needle")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.JSONEq(t, `{"error":"unauthorized"}`, rec.Body.String())

	// DECLARED BEHAVIOUR CHANGE. handleListJobs reads the identity as
	// `u, _ := UserFromCtx(ctx)` and discards the ok, so this request would
	// previously have listed the whole farm. It is unreachable through the mux,
	// where the route is auth(http.HandlerFunc(s.handleListJobs)), and it aligns
	// the jobs list with handleListScheduledJobs and with mine=true.
	//
	// It must NOT reach the database, which is the half a status code alone does
	// not say: a 401 that still ran the count would be a refusal that costs
	// exactly what it refused.
	assert.Zero(t, db.calls, "a refusal must make no statement")
}

// The refusal touches NO DATABASE STATEMENT, asserted structurally rather than
// by timing. store.Queries accepts any DBTX, so a recording stub is the seam.
func TestListJobs_RefusalMakesNoDatabaseCall(t *testing.T) {
	s, db := newSearchTestServer(t, 1, time.Minute)
	u := searchTestUser(1)

	require.NotEqual(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)
	require.Positive(t, db.calls,
		"fixture: an ALLOWED search must reach the database, or 'the refusal made no call' is "+
			"vacuously true of every request")
	spent := db.calls

	rec := listJobsAs(t, s, u, "q=needle")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, spent, db.calls,
		"a refused search must make zero statements: the bucket exists to stop pool occupancy, and a "+
			"refusal that still occupied a connection would bound nothing")
}

func listSchedulesAs(t *testing.T, s *Server, u AuthUser, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/scheduled-jobs?"+rawQuery, nil)
	req = req.WithContext(ctxWithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	s.handleListScheduledJobs(rec, req)
	return rec
}

// ONE BUCKET OVER BOTH ROUTES. Two buckets would give a caller who alternates
// routes exactly twice the ceiling, which is the shape where per-axis bounds
// reduce nothing. The interleaving is the discriminator: with a ceiling of 2, a
// jobs search and a schedules search must together exhaust it.
func TestSearchBucket_IsSharedAcrossBothListRoutes(t *testing.T) {
	s, _ := newSearchTestServer(t, 2, time.Minute)
	u := searchTestUser(1)

	require.NotEqual(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)
	require.NotEqual(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code)

	assert.Equal(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code,
		"the schedules route must see a budget the jobs route already spent")
	assert.Equal(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code,
		"and the reverse direction too, or the two routes have separate maps")
}

func TestListScheduledJobs_WhitespaceOnlyQIsNotCounted(t *testing.T) {
	s, _ := newSearchTestServer(t, 1, time.Minute)
	u := searchTestUser(1)

	for _, q := range []string{"q=%20%20", "q="} {
		rec := listSchedulesAs(t, s, u, q)
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "%s trims to absent", q)
	}
	require.NotEqual(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code)
	assert.Equal(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code)
}

func TestListScheduledJobs_UnfilteredPollingIsNotCounted(t *testing.T) {
	s, _ := newSearchTestServer(t, 2, time.Minute)
	u := searchTestUser(1)

	for i := 0; i < 7; i++ {
		require.NotEqual(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "limit=10").Code,
			"unfiltered request %d", i)
	}
	for i := 0; i < 2; i++ {
		require.NotEqual(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code, i)
	}
	assert.Equal(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code)
}

// The 429 body must be BYTE-IDENTICAL across the two endpoints, mirroring
// TestFilterQ_BodiesAreIdenticalAcrossEndpoints' rule for the q 400s. Two
// endpoints growing their own copies of a refusal drift without either
// endpoint's own tests noticing.
func TestSearchRefusal_BodyIsIdenticalAcrossEndpoints(t *testing.T) {
	s, _ := newSearchTestServer(t, 1, time.Minute)
	u := searchTestUser(1)
	require.NotEqual(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)

	jobs := listJobsAs(t, s, u, "q=needle")
	scheds := listSchedulesAs(t, s, u, "q=needle")
	require.Equal(t, http.StatusTooManyRequests, jobs.Code)
	require.Equal(t, http.StatusTooManyRequests, scheds.Code)
	assert.Equal(t, jobs.Body.String(), scheds.Body.String(),
		"one control, one body: an operator reading a log must not have to know which route it came from")
}
