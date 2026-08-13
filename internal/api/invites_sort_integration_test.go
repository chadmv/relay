//go:build integration

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"relay/internal/api"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedInvite inserts an invite directly through the pool so the test controls
// created_at, expires_at and used_at, none of which POST /v1/invites exposes.
func seedInvite(t *testing.T, pool *pgxpool.Pool, createdBy pgtype.UUID, tokenHash string,
	createdAt, expiresAt time.Time, usedAt *time.Time) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO invites (token_hash, created_by, created_at, expires_at, used_at)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		tokenHash, createdBy, createdAt, expiresAt, usedAt,
	).Scan(&id)
	require.NoError(t, err, "seedInvite %s", tokenHash)
	return id
}

// seedInvitesForSort seeds three invites whose created_at and expires_at
// orderings are REVERSED relative to each other, so a handler that dispatched
// -expires_at to the created_at query would produce the wrong order and fail.
// Returns the ids in created_at ASC order.
func seedInvitesForSort(t *testing.T, pool *pgxpool.Pool, createdBy pgtype.UUID) []string {
	t.Helper()
	baseCreated := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	baseExpires := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	ids := make([]string, 0, 3)
	for i, name := range []string{"alpha", "bravo", "charlie"} {
		ids = append(ids, seedInvite(t, pool, createdBy, "hash-inv-sort-"+name,
			baseCreated.Add(time.Duration(i)*time.Hour),
			baseExpires.Add(time.Duration(3-i)*time.Hour), nil))
	}
	return ids
}

// TestListInvites_EverySortKeyWorksInBothDirections is the panic gate. It is
// driven off api.InvitesSortSpec.Keys rather than a literal list, so a key
// added to the spec without a dispatch arm turns this test red automatically -
// which is exactly the failure mode the spec calls out: parseSort strips the
// leading '-' before the allowlist check, so both directions of every key reach
// the switch, and an unimplemented arm is a 500 plus a dropped connection
// triggered by an ordinary query parameter.
func TestListInvites_EverySortKeyWorksInBothDirections(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Sort Admin", "inv-sort-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	seedInvitesForSort(t, pool, admin.ID)

	require.NotEmpty(t, api.InvitesSortSpec.Keys, "the spec must allowlist at least one key")
	for key := range api.InvitesSortSpec.Keys {
		for _, sortStr := range []string{key, "-" + key} {
			t.Run(sortStr, func(t *testing.T) {
				code, p, body := getInvitesPage(t, srv, adminToken, "sort="+sortStr+"&limit=50")
				require.Equal(t, http.StatusOK, code,
					"sort=%s must not 500; a missing dispatch arm panics. body: %s", sortStr, body)
				require.Len(t, p.Items, 3)
				assertItemsSorted(t, p.Items, sortStr)
			})
		}
	}
}

// assertItemsSorted confirms the field implied by sortStr is monotonic across
// the page. RFC3339 timestamps compare correctly as strings when they share a
// zone, which the Go JSON encoder guarantees here.
func assertItemsSorted(t *testing.T, items []map[string]any, sortStr string) {
	t.Helper()
	desc := len(sortStr) > 0 && sortStr[0] == '-'
	key := sortStr
	if desc {
		key = sortStr[1:]
	}
	values := make([]string, len(items))
	for i, it := range items {
		v, ok := it[key].(string)
		require.True(t, ok, "item %d has no string %q: %v", i, key, it)
		values[i] = v
	}
	for i := 1; i < len(values); i++ {
		if desc {
			assert.GreaterOrEqual(t, values[i-1], values[i],
				"sort=%s not monotonic at i=%d (%v vs %v)", sortStr, i, values[i-1], values[i])
		} else {
			assert.LessOrEqual(t, values[i-1], values[i],
				"sort=%s not monotonic at i=%d (%v vs %v)", sortStr, i, values[i-1], values[i])
		}
	}
}

// The orderings of created_at and expires_at are reversed in the fixture, so
// this pins that each arm dispatched to its OWN query. A 200 with monotonic
// output on the wrong column would still pass assertItemsSorted for that
// column's key, but cannot produce both of these id orders at once.
func TestListInvites_SortArmsDispatchToTheirOwnQuery(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Dispatch Admin", "inv-dispatch-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	ids := seedInvitesForSort(t, pool, admin.ID) // created ASC: [0]=alpha [1]=bravo [2]=charlie

	code, p, _ := getInvitesPage(t, srv, adminToken, "sort=created_at&limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 3)
	assert.Equal(t, []any{ids[0], ids[1], ids[2]},
		[]any{p.Items[0]["id"], p.Items[1]["id"], p.Items[2]["id"]},
		"created_at ASC order")

	// expires_at is seeded in the REVERSE order of created_at.
	code, p, _ = getInvitesPage(t, srv, adminToken, "sort=expires_at&limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 3)
	assert.Equal(t, []any{ids[2], ids[1], ids[0]},
		[]any{p.Items[0]["id"], p.Items[1]["id"], p.Items[2]["id"]},
		"expires_at ASC order must be the reverse of created_at ASC in this fixture")
}

func TestListInvites_UnknownSortKeyIs400NamingKeysAndPath(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Bad Sort Admin", "inv-badsort-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	code, _, body := getInvitesPage(t, srv, adminToken, "sort=token_hash")
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "unsupported sort key 'token_hash'")
	assert.Contains(t, body, "/v1/invites", "the 400 must name the request path")
	assert.Contains(t, body, "created_at")
	assert.Contains(t, body, "expires_at")
}

func TestListInvites_CursorFromAnotherSortIsRejected(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Cursor Admin", "inv-cursor-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	seedInvitesForSort(t, pool, admin.ID)

	code, p, _ := getInvitesPage(t, srv, adminToken, "sort=-created_at&limit=1")
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, p.NextCursor, "three rows at limit=1 must yield a cursor")

	code, _, body := getInvitesPage(t, srv, adminToken,
		fmt.Sprintf("sort=expires_at&limit=1&cursor=%s", p.NextCursor))
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "cursor sort key does not match requested sort")

	code, _, body = getInvitesPage(t, srv, adminToken, "sort=-created_at&cursor=not-a-cursor")
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "invalid cursor")
}
