//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/api"
	"relay/internal/store"

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

var _ = store.Invite{} // keep the store import honest for later tests in this file
