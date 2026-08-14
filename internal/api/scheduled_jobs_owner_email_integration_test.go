//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/api"

	"github.com/stretchr/testify/require"
)

// Admin list view: each row carries the correct owner's email.
func TestListScheduledJobs_OwnerEmail_AdminMultiOwner(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjmail-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	owner1 := createTestUser(t, q, "Owner1", "sjmail-owner1@test.com", false)
	owner2 := createTestUser(t, q, "Owner2", "sjmail-owner2@test.com", false)

	seedScheduledJob(t, pool, "mail-sched-a", uuidString(owner1.ID),
		time.Now().Add(1*time.Hour), time.Now())
	seedScheduledJob(t, pool, "mail-sched-b", uuidString(owner2.ID),
		time.Now().Add(2*time.Hour), time.Now())

	code, p := getScheduledJobsPage(t, srv, adminToken, "sort=name&limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 2)

	byName := map[string]string{}
	for _, it := range p.Items {
		name, _ := it["name"].(string)
		ownerEmail, _ := it["owner_email"].(string)
		byName[name] = ownerEmail
	}
	require.Equal(t, "sjmail-owner1@test.com", byName["mail-sched-a"])
	require.Equal(t, "sjmail-owner2@test.com", byName["mail-sched-b"])
}

// Owner-scoped view: the caller's email on their own rows.
func TestListScheduledJobs_OwnerEmail_OwnerScoped(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	alice := createTestUser(t, q, "Alice", "sjmail-alice@test.com", false)
	aliceToken := createTestToken(t, q, alice.ID)

	seedScheduledJob(t, pool, "alice-sched", uuidString(alice.ID),
		time.Now().Add(1*time.Hour), time.Now())

	code, p := getScheduledJobsPage(t, srv, aliceToken, "sort=name&limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	ownerEmail, _ := p.Items[0]["owner_email"].(string)
	require.Equal(t, "sjmail-alice@test.com", ownerEmail)
}

// getScheduledJobDetail fetches one schedule as a raw map, so the assertions read
// the wire shape rather than a re-decoded Go struct.
func getScheduledJobDetail(t *testing.T, srv *api.Server, token, id string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/scheduled-jobs/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var body map[string]any
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	}
	return rec.Code, body
}

// Admin detail view: the ROW OWNER's email, not the caller's. This is the
// discriminating direction - the owner-scoped test below also passes against a
// handler that just stamps the caller's own email onto every response, and this
// one does not.
func TestGetScheduledJob_OwnerEmail_AdminSeesOwnersEmail(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjget-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	owner := createTestUser(t, q, "Owner", "sjget-owner@test.com", false)

	id := seedScheduledJob(t, pool, "get-mail-sched", uuidString(owner.ID),
		time.Now().Add(1*time.Hour), time.Now())

	code, body := getScheduledJobDetail(t, srv, adminToken, id)
	require.Equal(t, http.StatusOK, code)
	ownerEmail, _ := body["owner_email"].(string)
	require.Equal(t, "sjget-owner@test.com", ownerEmail)
}

// Owner detail view: the caller's own email on their own row.
func TestGetScheduledJob_OwnerEmail_OwnerSeesOwn(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	alice := createTestUser(t, q, "Alice", "sjget-alice@test.com", false)
	aliceToken := createTestToken(t, q, alice.ID)

	id := seedScheduledJob(t, pool, "alice-get-sched", uuidString(alice.ID),
		time.Now().Add(1*time.Hour), time.Now())

	code, body := getScheduledJobDetail(t, srv, aliceToken, id)
	require.Equal(t, http.StatusOK, code)
	ownerEmail, _ := body["owner_email"].(string)
	require.Equal(t, "sjget-alice@test.com", ownerEmail)
}
