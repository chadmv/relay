package api

import (
	"net/http/httptest"
	"testing"
	"time"

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
