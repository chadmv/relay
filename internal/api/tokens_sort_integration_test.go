//go:build integration

package api_test

import (
	"net/http"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/tokenhash"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The panic gate for GET /v1/auth/tokens, driven off api.TokensSortSpec.Keys so
// a key added without a dispatch arm turns this red automatically.
func TestListTokens_EverySortKeyWorksInBothDirections(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Sort", "tok-sort@example.com", false)
	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	time.Sleep(2 * time.Millisecond)
	mintToken(t, q, me.ID, future(30*24*time.Hour))
	time.Sleep(2 * time.Millisecond)
	mintToken(t, q, me.ID, future(30*24*time.Hour))

	require.NotEmpty(t, api.TokensSortSpec.Keys)
	for key := range api.TokensSortSpec.Keys {
		for _, sortStr := range []string{key, "-" + key} {
			t.Run(sortStr, func(t *testing.T) {
				code, p, body := getTokensPage(t, srv, callerToken, "sort="+sortStr+"&limit=50")
				require.Equal(t, http.StatusOK, code,
					"sort=%s must not 500; a missing dispatch arm panics. body: %s", sortStr, body)
				require.Len(t, p.Items, 3)
				assertItemsSorted(t, p.Items, sortStr)
			})
		}
	}
}

func TestListTokens_UnknownSortKeyIs400NamingKeysAndPath(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Bad Sort", "tok-badsort@example.com", false)
	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))

	code, _, body := getTokensPage(t, srv, callerToken, "sort=expires_at")
	require.Equal(t, http.StatusBadRequest, code,
		"expires_at is nullable and deliberately not a sort key here")
	assert.Contains(t, body, "unsupported sort key 'expires_at'")
	assert.Contains(t, body, "/v1/auth/tokens")
	assert.Contains(t, body, "created_at")

	code, _, body = getTokensPage(t, srv, callerToken, "cursor=not-a-cursor")
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "invalid cursor")
}

// is_current must be computed per ROW against the caller's token id, not per
// page against the first row. With limit=1 and the caller holding the OLDEST
// token, the flag must land on the LAST page.
func TestListTokens_IsCurrentSurvivesPagination(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Page Cur", "tok-pagecur@example.com", false)

	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	time.Sleep(2 * time.Millisecond)
	mintToken(t, q, me.ID, future(30*24*time.Hour))

	callerRow, err := q.GetTokenWithUser(t.Context(), tokenhash.Hash(callerToken))
	require.NoError(t, err)

	var walked []map[string]any
	cursor := ""
	for i := 0; i < 4; i++ { // safety bound
		qs := "limit=1"
		if cursor != "" {
			qs += "&cursor=" + cursor
		}
		code, p, _ := getTokensPage(t, srv, callerToken, qs)
		require.Equal(t, http.StatusOK, code, "page %d", i)
		require.Equal(t, int64(2), p.Total, "total must be the full count on every page")
		walked = append(walked, p.Items...)
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}

	require.Len(t, walked, 2, "the walk must visit both rows exactly once")
	assert.Equal(t, false, walked[0]["is_current"],
		"page 1 under -created_at is the NEWER token, which is not the caller's")
	assert.Equal(t, true, walked[1]["is_current"],
		"page 2 is the caller's own older token; a per-page flag would put it on page 1")
	assert.Equal(t, fmtUUID(callerRow.TokenID), walked[1]["id"])
}
