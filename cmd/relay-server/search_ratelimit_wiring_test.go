package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// searchWiringDB authenticates a caller and REFUSES every other statement, so
// an allowed search answers 500 and a refused one answers 429 - the two are then
// distinguishable at the recorder with no Postgres.
//
// A SEPARATE STUB FROM stubAdminDB, and not for the reason it looks like.
// stubAdminRow does fill every *pgtype.UUID with a valid value, so it produces
// an identified caller and the user-keyed limiters work against it. What it
// cannot do is serve a list route: it PANICS on Query, and a q-carrying
// GET /v1/jobs reaches Query after the count. Refusing is what this test needs;
// panicking is not.
type searchWiringDB struct{}

var errSearchWiringRefused = errors.New("searchWiringDB refuses every statement but the token lookup")

func (searchWiringDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errSearchWiringRefused
}

func (searchWiringDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errSearchWiringRefused
}

func (searchWiringDB) QueryRow(context.Context, string, ...any) pgx.Row { return searchWiringRow{} }

type searchWiringRow struct{}

// Scan fills BY DESTINATION TYPE and discriminates on the SHAPE of the row
// rather than on the SQL text, so there is no string match to rot: only
// GetTokenWithUserRow has *pgtype.UUID destinations on this path, and a count
// statement scans an int64. Anything with no uuid destination is refused like
// every other list statement.
//
// The uuid must be VALID. A caller whose id is invalid is refused 401 by the
// fail-closed bucket key, and this test would then measure that instead of the
// ceiling.
func (searchWiringRow) Scan(dest ...any) error {
	uuids := 0
	for _, d := range dest {
		switch v := d.(type) {
		case *bool:
			*v = false
		case *string:
			*v = "search-wiring"
		case *pgtype.UUID:
			var raw [16]byte
			raw[0] = 0xc0
			uuids++
			raw[15] = byte(uuids)
			*v = pgtype.UUID{Bytes: raw, Valid: true}
		}
	}
	if uuids == 0 {
		return errSearchWiringRefused
	}
	return nil
}

func searchWiringRequest(t *testing.T, srv *http.Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Authorization", "Bearer any-token-the-stub-resolves")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

func searchWiringServer(n int, win time.Duration) *http.Server {
	return buildHTTPServer(httpServerDeps{
		addr:           "127.0.0.1:0",
		q:              store.New(searchWiringDB{}),
		searchLimitN:   n,
		searchLimitWin: win,
	})
}

// An EXECUTED wiring guard: it builds the server the way main does and drives
// real requests through the real route.
//
// WHAT IT COVERS: buildHTTPServer forwarding searchLimitN and searchLimitWin
// into api.Server, and api.Server acting on them. Deleting either assignment in
// buildHTTPServer makes this RED, which an AST row would not - and this test
// adds no row to any AST table.
//
// WHAT IT DOES NOT COVER, said plainly because the boundary was MEASURED: it
// does not look at main.go. Setting searchLimitN: 0 in main's own httpServerDeps
// literal leaves the whole cmd/relay-server lane green, this test included. That
// half is unguarded, and closing it needs the generalized env-to-field wiring
// guard, not a weaker version of this test.
func TestBuildHTTPServer_SearchLimitRefusesAQCarryingRequestPastTheCeiling(t *testing.T) {
	srv := searchWiringServer(2, time.Minute)

	// A ceiling of 2: two q-carrying requests are ALLOWED, and an allowed one
	// reaches the refusing stub and answers 500. That 500 is the fixture, not the
	// subject - it is what proves the request got past the bucket.
	for i := 0; i < 2; i++ {
		rec := searchWiringRequest(t, srv, "/v1/jobs?q=needle")
		require.Equal(t, http.StatusInternalServerError, rec.Code,
			"request %d must be ALLOWED past the bucket and then fail at the stub; a 401 here means "+
				"the stub is not producing an identified caller, a 429 means the ceiling is wrong. "+
				"body: %s", i, rec.Body.String())
	}

	rec := searchWiringRequest(t, srv, "/v1/jobs?q=needle")
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"the third q-carrying request must be refused. If this is 500, buildHTTPServer did not "+
			"forward searchLimitN/searchLimitWin and the control is entirely absent on this build. "+
			"body: %s", rec.Body.String())

	var body struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "search rate limit exceeded", body.Error,
		"distinct from the 'rate limit exceeded' RateLimit and UserRateLimit share, so an operator "+
			"reading a log can tell which control fired")
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
}

// Unfiltered polling is unaffected THROUGH THE REAL ROUTE, not only through the
// handler. THE WHITESPACE ROWS ARE THE POINT: this is the assertion that catches
// the control being mounted as middleware during the wiring, because a
// middleware predicate testing r.URL.Query().Get("q") != "" counts ?q=%20%20 and
// parseFilterQ trims it to absent.
func TestBuildHTTPServer_SearchLimitLeavesUnfilteredPollingAlone(t *testing.T) {
	srv := searchWiringServer(1, time.Minute)

	for i := 0; i < 4; i++ {
		rec := searchWiringRequest(t, srv, "/v1/jobs?q=%20%20")
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code,
			"whitespace-only request %d trims to absent and must not be charged", i)
	}
	for i := 0; i < 6; i++ {
		rec := searchWiringRequest(t, srv, "/v1/jobs?limit=10")
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "unfiltered request %d", i)
	}

	require.Equal(t, http.StatusInternalServerError,
		searchWiringRequest(t, srv, "/v1/jobs?q=needle").Code,
		"the single slot must still be unspent after ten non-needle requests")
	assert.Equal(t, http.StatusTooManyRequests,
		searchWiringRequest(t, srv, "/v1/jobs?q=needle").Code)
}
