//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getInvitesPage issues GET /v1/invites with the given bearer token and raw
// query string, returning the status, the decoded envelope, and the RAW body
// string. The raw body is returned because the hash-leak sweep in
// list_endpoint_leak_integration_test.go asserts on bytes, not on parsed keys: a
// handler that leaked the hash under a differently spelled key would pass a
// parsed-struct assertion.
func getInvitesPage(t *testing.T, srv *api.Server, token, query string) (int, pageEnvelope[map[string]any], string) {
	t.Helper()
	url := "/v1/invites"
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

// createInviteViaAPI posts an invite as the given admin and returns the raw
// token and the invite id. Going through the API rather than the store keeps
// the raw token in hand for the leak sweep.
func createInviteViaAPI(t *testing.T, srv *api.Server, adminToken, body string) (rawToken, id string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/invites", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "seed invite: %s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp["token"].(string), resp["id"].(string)
}

func TestListInvites_Gating(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Inv Admin", "inv-list-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	user := createTestUser(t, q, "Inv User", "inv-list-user@example.com", false)
	userToken := createTestToken(t, q, user.ID)

	// Unauthenticated -> 401.
	req := httptest.NewRequest("GET", "/v1/invites", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "no bearer token must be 401")

	// Authenticated non-admin -> 403. Invites carry invitee email addresses, so
	// a non-admin read is a disclosure, not just a policy miss.
	code, _, _ := getInvitesPage(t, srv, userToken, "")
	assert.Equal(t, http.StatusForbidden, code, "a non-admin must not read invites")

	// Admin -> 200.
	code, _, _ = getInvitesPage(t, srv, adminToken, "")
	assert.Equal(t, http.StatusOK, code, "an admin must be able to read invites")
}

func TestListInvites_ItemShapeIsExactly(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Shape Admin", "inv-shape-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	// One email-bound invite and one unbound invite. Neither is redeemed, so
	// used_at must be absent from both.
	createInviteViaAPI(t, srv, adminToken, `{"email":"bound@example.com","expires_in":"24h"}`)
	createInviteViaAPI(t, srv, adminToken, `{"expires_in":"24h"}`)

	code, p, _ := getInvitesPage(t, srv, adminToken, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 2)
	assert.Equal(t, int64(2), p.Total)
	assert.Equal(t, "", p.NextCursor, "two rows under the default limit is the last page")

	var bound, unbound map[string]any
	for _, it := range p.Items {
		if _, ok := it["email"]; ok {
			bound = it
		} else {
			unbound = it
		}
	}
	require.NotNil(t, bound, "the email-bound invite must carry an email key")
	require.NotNil(t, unbound, "the unbound invite must omit the email key entirely")

	// Key-set equality, not a series of per-key assertions: an ADDED key must
	// fail this test. That is what makes it the regression gate on the shape.
	assert.ElementsMatch(t,
		[]string{"id", "created_at", "expires_at", "created_by", "created_by_email", "email"},
		keysOf(bound), "email-bound invite key set")
	assert.ElementsMatch(t,
		[]string{"id", "created_at", "expires_at", "created_by", "created_by_email"},
		keysOf(unbound), "unbound invite key set")

	assert.Equal(t, "bound@example.com", bound["email"])
	assert.Equal(t, "inv-shape-admin@example.com", bound["created_by_email"],
		"created_by_email must be the creating admin's address, from the JOIN")
	assert.Equal(t, fmtUUID(admin.ID), bound["created_by"])

	// Two-return lookup, not a nil comparison: a nulled key and an absent key
	// are different wire shapes and only one of them is correct here.
	_, hasUsedAt := unbound["used_at"]
	assert.False(t, hasUsedAt, "used_at must be ABSENT on an unredeemed invite, not null")
}

// keysOf returns the key set of a decoded item for ElementsMatch comparison.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The invites list applies NO filter: redeemed and expired invites are exactly
// what the tab exists to show, unlike GET /v1/agent-enrollments where a
// consumed row simply vanishes. All four client-side pill states must be
// derivable from the rows this returns.
func TestListInvites_ReturnsEveryStateUnfiltered(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "State Admin", "inv-state-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	now := time.Now()
	past := now.Add(-48 * time.Hour)
	redeemedAt := now.Add(-time.Hour)

	activeID := seedInvite(t, pool, admin.ID, "hash-inv-active",
		now.Add(-time.Hour), now.Add(72*time.Hour), nil)
	expiringID := seedInvite(t, pool, admin.ID, "hash-inv-expiring",
		now.Add(-time.Hour), now.Add(30*time.Minute), nil)
	expiredID := seedInvite(t, pool, admin.ID, "hash-inv-expired",
		past, now.Add(-time.Hour), nil)
	redeemedID := seedInvite(t, pool, admin.ID, "hash-inv-redeemed",
		past, now.Add(72*time.Hour), &redeemedAt)
	// Redeemed AND past its expiry: the client's precedence rule checks used_at
	// FIRST, so this row must still carry used_at and must still be returned.
	redeemedExpiredID := seedInvite(t, pool, admin.ID, "hash-inv-redeemed-expired",
		past, now.Add(-time.Hour), &redeemedAt)

	code, p, _ := getInvitesPage(t, srv, adminToken, "limit=200")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, int64(5), p.Total, "total is the unfiltered row count")
	require.Len(t, p.Items, 5)

	byID := map[string]map[string]any{}
	for _, it := range p.Items {
		byID[it["id"].(string)] = it
	}
	for _, id := range []string{activeID, expiringID, expiredID, redeemedID, redeemedExpiredID} {
		require.Contains(t, byID, id, "every invite in every state must be listed")
	}

	// used_at presence discriminates redeemed from unredeemed at the data level.
	for _, id := range []string{activeID, expiringID, expiredID} {
		_, has := byID[id]["used_at"]
		assert.False(t, has, "unredeemed invite %s must omit used_at", id)
	}
	for _, id := range []string{redeemedID, redeemedExpiredID} {
		_, has := byID[id]["used_at"]
		assert.True(t, has, "redeemed invite %s must carry used_at", id)
	}

	// Every row carries the creator's email from the inner JOIN.
	for id, it := range byID {
		assert.Equal(t, "inv-state-admin@example.com", it["created_by_email"],
			"created_by_email missing or wrong on %s", id)
	}
}

func TestListInvites_CursorWalkVisitsEveryRowExactlyOnce(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Walk Admin", "inv-walk-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	seedInvitesForSort(t, pool, admin.ID)

	code, single, _ := getInvitesPage(t, srv, adminToken, "limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, single.Items, 3)
	require.Equal(t, int64(3), single.Total)

	var walked []string
	cursor := ""
	for i := 0; i < 5; i++ { // safety bound; 3 rows at limit=1 needs 3 pages
		qs := "limit=1"
		if cursor != "" {
			qs += "&cursor=" + cursor
		}
		code, p, _ := getInvitesPage(t, srv, adminToken, qs)
		require.Equal(t, http.StatusOK, code, "page %d", i)
		require.Equal(t, int64(3), p.Total,
			"total must be the full row count on every page, not the page size")
		for _, it := range p.Items {
			walked = append(walked, it["id"].(string))
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}

	require.Len(t, walked, 3, "the walk must visit every row with no duplicate and no omission")
	for i, it := range single.Items {
		assert.Equal(t, it["id"], walked[i], "row %d differs between single-page and paged walk", i)
	}
	assert.NotEqual(t, walked[0], walked[1])
	assert.NotEqual(t, walked[1], walked[2])
}

func TestListInvites_LimitOutOfRangeIs400(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Limit Admin", "inv-limit-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	for _, bad := range []string{"0", "201", "-1", "abc"} {
		code, _, body := getInvitesPage(t, srv, adminToken, "limit="+bad)
		assert.Equal(t, http.StatusBadRequest, code, "limit=%s must be rejected", bad)
		assert.Contains(t, body, "invalid limit")
	}
}
