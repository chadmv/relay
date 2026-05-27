//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// usersSortKeys enumerates every (key, direction) in the server's usersSortSpec.
var usersSortKeys = []string{
	"-created_at", "created_at",
	"-name", "name",
	"-email", "email",
}

// getUsersPage calls GET /v1/users and returns the decoded page envelope.
func getUsersPage(t *testing.T, srv interface {
	Handler() http.Handler
}, token, query string) (int, pageEnvelope[map[string]any]) {
	t.Helper()
	url := "/v1/users"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp pageEnvelope[map[string]any]
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	}
	return rec.Code, resp
}

// seedUserWithCreatedAt inserts a user directly via the pool so we can control
// name, email, and created_at (which CreateUserWithPassword doesn't expose).
func seedUserWithCreatedAt(t *testing.T, pool *pgxpool.Pool, email, name string, createdAt time.Time) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("placeholder"), bcrypt.MinCost)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO users (name, email, is_admin, password_hash, created_at)
		 VALUES ($1, $2, FALSE, $3, $4)`,
		name, email, string(hash), createdAt,
	)
	require.NoError(t, err, "seedUserWithCreatedAt %s", email)
}

// seedUsersForSort seeds 8 active users whose alphabetical name/email order
// differs from their creation-time order, so each sort key yields a distinct
// result set.
func seedUsersForSort(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	type u struct {
		email string
		name  string
	}
	// Inserted in this order (hotel first, alpha last), so created_at DESC
	// puts hotel at top. Name/email DESC would put Hotel/hotel at top too but
	// the ordering of the full 8-element set differs from created_at order
	// (alpha is last by created_at but first alphabetically).
	users := []u{
		{"user-hotel@test.com", "Hotel"},
		{"user-golf@test.com", "Golf"},
		{"user-foxtrot@test.com", "Foxtrot"},
		{"user-echo@test.com", "Echo"},
		{"user-delta@test.com", "Delta"},
		{"user-charlie@test.com", "Charlie"},
		{"user-bravo@test.com", "Bravo"},
		{"user-alpha@test.com", "Alpha"},
	}

	for i, u := range users {
		seedUserWithCreatedAt(t, pool, u.email, u.name, base.Add(time.Duration(i)*time.Second))
	}
}

// archiveUserByEmail archives a user directly via the pool.
func archiveUserByEmail(t *testing.T, pool *pgxpool.Pool, email string) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`UPDATE users SET archived_at = NOW() WHERE email = $1`, email)
	require.NoError(t, err)
}

// assertUsersSorted confirms the field implied by sortKey is monotonic.
func assertUsersSorted(t *testing.T, items []map[string]any, sortKey string) {
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

func TestListUsers_Sort_OrderingAcrossKeys_ActiveOnly(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "usort-admin@test.com", true)
	token := createTestToken(t, q, admin.ID)

	seedUsersForSort(t, pool)

	// 8 seeded users + 1 admin = 9 active users total.
	for _, sortKey := range usersSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			code, p := getUsersPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code, "sort=%s", sortKey)
			require.Len(t, p.Items, 9, "sort=%s: expected 9 active users, got %d", sortKey, len(p.Items))
			assertUsersSorted(t, p.Items, sortKey)
		})
	}
}

func TestListUsers_Sort_OrderingAcrossKeys_IncludingArchived(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "uinc-admin@test.com", true)
	token := createTestToken(t, q, admin.ID)

	seedUsersForSort(t, pool)

	// Archive 2 users so the IncludingArchived variant returns more than the
	// active-only variant. Total: 8 seeded + 1 admin = 9, all included.
	archiveUserByEmail(t, pool, "user-alpha@test.com")
	archiveUserByEmail(t, pool, "user-bravo@test.com")

	for _, sortKey := range usersSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			code, p := getUsersPage(t, srv, token, "sort="+sortKey+"&include_archived=true&limit=50")
			require.Equal(t, http.StatusOK, code, "sort=%s", sortKey)
			// All 9 rows (8 seeded + 1 admin) are visible, including the 2 archived.
			require.Len(t, p.Items, 9, "sort=%s: expected 9 users (8 seeded + 1 admin), got %d", sortKey, len(p.Items))
			assertUsersSorted(t, p.Items, sortKey)
		})
	}
}

func TestListUsers_Sort_PaginationAcrossPages_ActiveOnly(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "upage-admin@test.com", true)
	token := createTestToken(t, q, admin.ID)

	seedUsersForSort(t, pool)

	for _, sortKey := range usersSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			// Single-page baseline (8 seeded + 1 admin = 9).
			code, single := getUsersPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code)
			require.Len(t, single.Items, 9, "sort=%s: expected 9 users", sortKey)

			// Walk in pages of 3.
			var paged []map[string]any
			cursor := ""
			for i := 0; i < 5; i++ { // safety bound
				qs := fmt.Sprintf("sort=%s&limit=3", sortKey)
				if cursor != "" {
					qs += "&cursor=" + cursor
				}
				code, p := getUsersPage(t, srv, token, qs)
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

func TestListUsers_Sort_CursorMismatchRejected(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "umismatch-admin@test.com", true)
	token := createTestToken(t, q, admin.ID)

	seedUsersForSort(t, pool)

	// Get a cursor under sort=-name.
	code, p := getUsersPage(t, srv, token, "sort=-name&limit=3")
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, p.NextCursor, "expected has-more cursor")

	// Resend under sort=-email -- must 400.
	code, _ = getUsersPage(t, srv, token, "sort=-email&limit=3&cursor="+p.NextCursor)
	require.Equal(t, http.StatusBadRequest, code)
}
