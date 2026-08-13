//go:build integration

package api_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mintToken creates an api_token for userID with an EXPLICIT expires_at and
// returns the raw hex.
//
// Why not createTestToken (api_test.go:48-61)? That helper mints a
// NULL-expires_at token. Every test in this file except the dedicated
// NULL-expiry one must use an explicitly-expiring token, so that the
// discriminating test for the expiry predicate is the ONLY test that a naive
// `expires_at > NOW()` predicate turns red. If the caller's own token were
// NULL-expiry, that predicate would empty every list in this file and the
// signal would be lost in the noise.
func mintToken(t *testing.T, q *store.Queries, userID pgtype.UUID, expiresAt pgtype.Timestamptz) string {
	t.Helper()
	raw := make([]byte, 16)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	rawHex := hex.EncodeToString(raw)
	_, err = q.CreateToken(t.Context(), store.CreateTokenParams{
		UserID:    userID,
		TokenHash: tokenhash.Hash(rawHex),
		ExpiresAt: expiresAt,
	})
	require.NoError(t, err)
	return rawHex
}

func future(d time.Duration) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Now().Add(d), Valid: true}
}

// getTokensPage issues GET /v1/auth/tokens and returns status, envelope and the
// RAW body string (needed by the leak sweep and by the ?user_id= equality test).
func getTokensPage(t *testing.T, srv *api.Server, token, query string) (int, pageEnvelope[map[string]any], string) {
	t.Helper()
	url := "/v1/auth/tokens"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	var resp pageEnvelope[map[string]any]
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(strings.NewReader(body)).Decode(&resp))
	}
	return rec.Code, resp, body
}

func TestListTokens_Gating(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Tok User", "tok-gate-user@example.com", false)
	userToken := mintToken(t, q, user.ID, future(30*24*time.Hour))

	req := httptest.NewRequest("GET", "/v1/auth/tokens", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "no bearer token must be 401")

	// 200 for a NON-admin. This is the paired positive against the invites 403
	// test: writing both together is what catches a mis-chained admin(...).
	code, _, _ := getTokensPage(t, srv, userToken, "")
	assert.Equal(t, http.StatusOK, code, "this is the self-service block, not the admin block")
}

func TestListTokens_ScopedToCallerAndIgnoresUserIDParam(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Me", "tok-scope-me@example.com", false)
	other := createTestUser(t, q, "Other", "tok-scope-other@example.com", false)

	myToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	mySecond := mintToken(t, q, me.ID, future(30*24*time.Hour))
	_ = mySecond
	mintToken(t, q, other.ID, future(30*24*time.Hour))
	mintToken(t, q, other.ID, future(30*24*time.Hour))

	code, p, body := getTokensPage(t, srv, myToken, "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, p.Items, 2, "exactly the caller's two rows")
	assert.Equal(t, int64(2), p.Total, "total must be scoped to the caller too")

	// The other user's rows must be absent by id, not merely by count.
	otherRows, err := q.ListActiveTokensForUserPage(t.Context(), store.ListActiveTokensForUserPageParams{
		UserID: other.ID, PageLimit: 50,
	})
	require.NoError(t, err)
	require.Len(t, otherRows, 2, "fixture: the other user really has two rows")
	for _, r := range otherRows {
		assert.NotContains(t, body, fmtUUID(r.ID), "another user's token id must never appear")
	}

	// A user_id query parameter must be ignored outright. The identity is the
	// bearer token; the caller does not get to name the rows they receive.
	code2, _, body2 := getTokensPage(t, srv, myToken, "user_id="+fmtUUID(other.ID))
	require.Equal(t, http.StatusOK, code2)
	assert.Equal(t, body, body2, "?user_id= must change nothing at all")
}

// is_current must be true for EXACTLY ONE row and it must be the presented
// token's row. A test asserting only "some row is current" passes against a
// handler that marks the first row.
func TestListTokens_IsCurrentIdentifiesThePresentedToken(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Cur", "tok-current@example.com", false)

	older := mintToken(t, q, me.ID, future(30*24*time.Hour))
	time.Sleep(2 * time.Millisecond) // distinct created_at so the order is stable
	newer := mintToken(t, q, me.ID, future(30*24*time.Hour))
	_ = newer

	// Authenticate with the OLDER token, which under the default -created_at
	// sort is the LAST row. A handler that flagged items[0] would fail here.
	code, p, _ := getTokensPage(t, srv, older, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 2)

	currentIDs := []string{}
	for _, it := range p.Items {
		v, ok := it["is_current"]
		require.True(t, ok, "is_current must be present on every row, never omitted")
		b, ok := v.(bool)
		require.True(t, ok, "is_current must be a bool, got %T", v)
		if b {
			currentIDs = append(currentIDs, it["id"].(string))
		}
	}
	require.Len(t, currentIDs, 1, "exactly one row is the caller's current session")

	// Resolve the presented token's row id independently, through the same
	// lookup BearerAuth uses, so the assertion is against the real identity
	// rather than against the handler's own answer.
	row, err := q.GetTokenWithUser(t.Context(), tokenhash.Hash(older))
	require.NoError(t, err)
	assert.Equal(t, fmtUUID(row.TokenID), currentIDs[0],
		"is_current must mark the row whose id equals AuthUser.TokenID")
	assert.Equal(t, fmtUUID(row.TokenID), p.Items[1]["id"],
		"fixture: the older token must be the last row under -created_at")
}

func TestListTokens_ItemShapeIsExactly(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Shape", "tok-shape@example.com", false)
	tok := mintToken(t, q, me.ID, future(30*24*time.Hour))

	code, p, _ := getTokensPage(t, srv, tok, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)

	assert.ElementsMatch(t,
		[]string{"id", "created_at", "expires_at", "is_current"},
		keysOf(p.Items[0]))
	assert.Equal(t, true, p.Items[0]["is_current"])
}

// A token whose expires_at is NULL never expires and authenticates forever
// (internal/api/middleware.go:32-35 only rejects on Valid && Before(now)). It
// MUST appear in the list, with expires_at absent from the item.
//
// This is the discriminating test for the `expires_at > NOW()` trap. An
// implementation whose predicate omits the `expires_at IS NULL OR` arm passes
// every other test in this file and fails only this one - which is why every
// other test in this file mints tokens with an explicit expiry via mintToken
// rather than through createTestToken (api_test.go:48-61, which mints
// NULL-expiry tokens).
func TestListTokens_NeverExpiringTokenIsListed(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Null Exp", "tok-nullexp@example.com", false)

	// The caller authenticates with an explicitly-expiring token so this test
	// fails for the NULL row's absence, never for its own 401.
	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	neverExpires := mintToken(t, q, me.ID, pgtype.Timestamptz{})

	neverRow, err := q.GetTokenWithUser(t.Context(), tokenhash.Hash(neverExpires))
	require.NoError(t, err)
	require.False(t, neverRow.ExpiresAt.Valid, "fixture: the seeded token must have a NULL expires_at")

	code, p, body := getTokensPage(t, srv, callerToken, "limit=200")
	require.Equal(t, http.StatusOK, code)

	var found map[string]any
	for _, it := range p.Items {
		if it["id"] == fmtUUID(neverRow.TokenID) {
			found = it
		}
	}
	require.NotNil(t, found,
		"a NULL-expires_at token never expires and MUST be listed; a bare `expires_at > NOW()` predicate hides exactly the most powerful credentials in the system. body: %s", body)

	_, hasExpires := found["expires_at"]
	assert.False(t, hasExpires,
		"a never-expiring token must OMIT expires_at, so the client can render 'never' rather than a date")
	assert.Equal(t, int64(2), p.Total, "total must count the never-expiring row too")
}

// The mirror of the test above. Paired with it deliberately: neither
// "return everything" nor "return nothing" satisfies both.
func TestListTokens_ExpiredTokenIsNotListed(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Exp", "tok-expired@example.com", false)

	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	expired := mintToken(t, q, me.ID, pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true})

	expiredRow, err := q.GetTokenWithUser(t.Context(), tokenhash.Hash(expired))
	require.NoError(t, err)
	require.True(t, expiredRow.ExpiresAt.Valid, "fixture: the seeded token must have an expiry")
	require.True(t, expiredRow.ExpiresAt.Time.Before(time.Now()), "fixture: it must be in the past")

	code, p, body := getTokensPage(t, srv, callerToken, "limit=200")
	require.Equal(t, http.StatusOK, code)

	assert.NotContains(t, body, fmtUUID(expiredRow.TokenID),
		"an expired token cannot authenticate (middleware.go:32-35) and must not be listed")
	assert.Len(t, p.Items, 1, "only the caller's live token")
	assert.Equal(t, int64(1), p.Total,
		"total must exclude the expired row too, or the footer states a number the caller cannot page to")
}

// After PUT /v1/users/me/password, DeleteOtherTokensForUser (auth.go:325-328)
// has run, so the list must contain exactly one row and it must be the caller's.
// This exercises the new read path against an existing write path end to end.
func TestListTokens_AfterPasswordChangeExactlyOneCurrentRowRemains(t *testing.T) {
	api.SetBcryptCostForTest()
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Pwd", "tok-pwd@example.com", false)

	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	mintToken(t, q, me.ID, future(30*24*time.Hour))
	mintToken(t, q, me.ID, future(30*24*time.Hour))

	code, before, _ := getTokensPage(t, srv, callerToken, "limit=200")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, before.Items, 3, "fixture: three live sessions before the password change")

	req := httptest.NewRequest("PUT", "/v1/users/me/password",
		strings.NewReader(`{"current_password":"testpassword1","new_password":"newpassword1"}`))
	req.Header.Set("Authorization", "Bearer "+callerToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code, "password change: %s", rec.Body.String())

	code, after, _ := getTokensPage(t, srv, callerToken, "limit=200")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, after.Items, 1, "a password change revokes every OTHER session")
	assert.Equal(t, int64(1), after.Total)
	assert.Equal(t, true, after.Items[0]["is_current"],
		"the surviving row is the caller's own session")
}
