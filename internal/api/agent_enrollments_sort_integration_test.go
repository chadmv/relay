//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var agentEnrollmentsSortKeys = []string{
	"-created_at", "created_at",
	"-expires_at", "expires_at",
}

// seedEnrollment inserts an enrollment directly via the pool so we can control
// created_at and expires_at (which CreateAgentEnrollment doesn't expose for created_at).
func seedEnrollment(t *testing.T, pool *pgxpool.Pool, createdBy pgtype.UUID, tokenHash string, createdAt, expiresAt time.Time) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO agent_enrollments (token_hash, created_by, created_at, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		tokenHash, createdBy, createdAt, expiresAt,
	).Scan(&id)
	require.NoError(t, err, "seedEnrollment %s", tokenHash)
	return id
}

// seedEnrollmentsForSort seeds 6 enrollments with distinct created_at and expires_at
// values whose orderings differ from each other (so sort tests are meaningful).
// All expires_at values are in the future so the WHERE expires_at > NOW() filter passes.
func seedEnrollmentsForSort(t *testing.T, pool *pgxpool.Pool, createdBy pgtype.UUID) {
	t.Helper()

	// Use a recent past base for created_at (we control these exactly).
	baseCreated := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	// expires_at uses a far-future base so all rows pass expires_at > NOW().
	// Offsets are reversed relative to created_at so the two orderings differ:
	//   created_at ASC  = alpha, bravo, charlie, delta, echo, foxtrot
	//   expires_at ASC  = foxtrot, echo, delta, charlie, bravo, alpha
	baseFuture := time.Now().Add(365 * 24 * time.Hour) // 1 year from now

	type e struct {
		tokenHash string
		createdAt time.Time
		expiresAt time.Time
	}

	enrollments := []e{
		{"hash-enr-alpha", baseCreated.Add(1 * time.Hour), baseFuture.Add(6 * time.Hour)},
		{"hash-enr-bravo", baseCreated.Add(2 * time.Hour), baseFuture.Add(5 * time.Hour)},
		{"hash-enr-charlie", baseCreated.Add(3 * time.Hour), baseFuture.Add(4 * time.Hour)},
		{"hash-enr-delta", baseCreated.Add(4 * time.Hour), baseFuture.Add(3 * time.Hour)},
		{"hash-enr-echo", baseCreated.Add(5 * time.Hour), baseFuture.Add(2 * time.Hour)},
		{"hash-enr-foxtrot", baseCreated.Add(6 * time.Hour), baseFuture.Add(1 * time.Hour)},
	}

	for _, enr := range enrollments {
		seedEnrollment(t, pool, createdBy, enr.tokenHash, enr.createdAt, enr.expiresAt)
	}
}

// getEnrollmentsPage calls GET /v1/agent-enrollments with the given token and query string.
func getEnrollmentsPage(t *testing.T, srv interface {
	Handler() http.Handler
}, token, query string) (int, pageEnvelope[map[string]any]) {
	t.Helper()
	url := "/v1/agent-enrollments"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp pageEnvelope[map[string]any]
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	}
	return rec.Code, resp
}

// assertEnrollmentsSorted confirms the field implied by sortKey is monotonic.
func assertEnrollmentsSorted(t *testing.T, items []map[string]any, sortKey string) {
	t.Helper()
	desc := len(sortKey) > 0 && sortKey[0] == '-'
	key := sortKey
	if desc {
		key = sortKey[1:]
	}

	values := make([]string, len(items))
	for i, it := range items {
		v, _ := it[key].(string)
		values[i] = v
	}
	for i := 1; i < len(values); i++ {
		if desc {
			assert.GreaterOrEqual(t, values[i-1], values[i],
				"sort=%s not monotonic at i=%d (%v vs %v)", sortKey, i, values[i-1], values[i])
		} else {
			assert.LessOrEqual(t, values[i-1], values[i],
				"sort=%s not monotonic at i=%d (%v vs %v)", sortKey, i, values[i-1], values[i])
		}
	}
}

func TestListAgentEnrollments_Sort_OrderingAcrossKeys(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "enrsort-admin@test.com", true)
	token := createTestToken(t, q, admin.ID)

	seedEnrollmentsForSort(t, pool, admin.ID)

	for _, sortKey := range agentEnrollmentsSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			code, p := getEnrollmentsPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code, "sort=%s: status", sortKey)
			require.Len(t, p.Items, 6, "sort=%s: expected 6 enrollments, got %d", sortKey, len(p.Items))
			assertEnrollmentsSorted(t, p.Items, sortKey)
		})
	}
}

func TestListAgentEnrollments_Sort_PaginationAcrossPages(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "enrpage-admin@test.com", true)
	token := createTestToken(t, q, admin.ID)

	seedEnrollmentsForSort(t, pool, admin.ID)

	for _, sortKey := range agentEnrollmentsSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			// Single-page baseline.
			code, single := getEnrollmentsPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code)
			require.Len(t, single.Items, 6, "sort=%s: expected 6 enrollments", sortKey)

			// Walk in pages of 2.
			var paged []map[string]any
			cursor := ""
			for i := 0; i < 5; i++ { // safety bound
				qs := fmt.Sprintf("sort=%s&limit=2", sortKey)
				if cursor != "" {
					qs += "&cursor=" + cursor
				}
				code, p := getEnrollmentsPage(t, srv, token, qs)
				require.Equal(t, http.StatusOK, code, "sort=%s page %d", sortKey, i)
				paged = append(paged, p.Items...)
				if p.NextCursor == "" {
					break
				}
				cursor = p.NextCursor
			}

			require.Equal(t, len(single.Items), len(paged), "paged length mismatch for sort=%s", sortKey)
			for i := range single.Items {
				assert.Equal(t, single.Items[i]["id"], paged[i]["id"],
					"row %d differs for sort=%s", i, sortKey)
			}
		})
	}
}

func TestListAgentEnrollments_Sort_CursorMismatchRejected(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "enrmismatch-admin@test.com", true)
	token := createTestToken(t, q, admin.ID)

	seedEnrollmentsForSort(t, pool, admin.ID)

	// Get a cursor under sort=-created_at.
	code, p := getEnrollmentsPage(t, srv, token, "sort=-created_at&limit=2")
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, p.NextCursor, "expected has-more cursor")

	// Resend under sort=expires_at -- must 400.
	code, _ = getEnrollmentsPage(t, srv, token, "sort=expires_at&limit=2&cursor="+p.NextCursor)
	require.Equal(t, http.StatusBadRequest, code)
}
