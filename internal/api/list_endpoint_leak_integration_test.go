//go:build integration

package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertNoSecretInBody sweeps the SERIALIZED response bytes. Asserting on the
// parsed items would be weaker: it passes against a handler that returns the
// hash under a different key name, or nests it, or puts it in the envelope.
func assertNoSecretInBody(t *testing.T, body string, secrets map[string]string) {
	t.Helper()
	require.NotEmpty(t, body, "an empty body would satisfy every assertion below")
	for label, secret := range secrets {
		require.NotEmpty(t, secret, "fixture: %s must be non-empty or this proves nothing", label)
		assert.NotContains(t, body, secret, "%s leaked into the response body", label)
	}
	assert.NotContains(t, strings.ToLower(body), "token",
		"no response key or value may contain the substring 'token'; body: %s", body)
}

func TestListInvites_NeverLeaksTheInviteTokenOrItsHash(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Leak Admin", "inv-leak-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	rawA, _ := createInviteViaAPI(t, srv, adminToken, `{"email":"a@example.com","expires_in":"24h"}`)
	rawB, _ := createInviteViaAPI(t, srv, adminToken, `{"expires_in":"24h"}`)

	secrets := map[string]string{
		"invite A raw token":  rawA,
		"invite A token hash": tokenhash.Hash(rawA),
		"invite B raw token":  rawB,
		"invite B token hash": tokenhash.Hash(rawB),
		"caller's own token":  adminToken,
		"caller's own hash":   tokenhash.Hash(adminToken),
	}

	// First page.
	code, p, body := getInvitesPage(t, srv, adminToken, "limit=1")
	require.Equal(t, http.StatusOK, code)
	assertNoSecretInBody(t, body, secrets)
	require.NotEmpty(t, p.NextCursor, "two rows at limit=1 must yield a cursor")

	// A later page, because a leak could plausibly live in one dispatch arm.
	code, _, body = getInvitesPage(t, srv, adminToken, "limit=1&cursor="+p.NextCursor)
	require.Equal(t, http.StatusOK, code)
	assertNoSecretInBody(t, body, secrets)

	// Every sort arm.
	for _, sortStr := range []string{"created_at", "-created_at", "expires_at", "-expires_at"} {
		code, _, body = getInvitesPage(t, srv, adminToken, "sort="+sortStr+"&limit=200")
		require.Equal(t, http.StatusOK, code, "sort=%s", sortStr)
		assertNoSecretInBody(t, body, secrets)
	}
}

func TestListTokens_NeverLeaksTheBearerTokenOrItsHash(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Leak", "tok-leak@example.com", false)

	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	other := mintToken(t, q, me.ID, future(30*24*time.Hour))
	never := mintToken(t, q, me.ID, pgtype.Timestamptz{})

	secrets := map[string]string{
		"caller's raw token":   callerToken,
		"caller's token hash":  tokenhash.Hash(callerToken),
		"second raw token":     other,
		"second token hash":    tokenhash.Hash(other),
		"never-expiring token": never,
		"never-expiring hash":  tokenhash.Hash(never),
	}

	code, p, body := getTokensPage(t, srv, callerToken, "limit=1")
	require.Equal(t, http.StatusOK, code)
	assertNoSecretInBody(t, body, secrets)
	require.NotEmpty(t, p.NextCursor)

	code, _, body = getTokensPage(t, srv, callerToken, "limit=1&cursor="+p.NextCursor)
	require.Equal(t, http.StatusOK, code)
	assertNoSecretInBody(t, body, secrets)

	for _, sortStr := range []string{"created_at", "-created_at"} {
		code, _, body = getTokensPage(t, srv, callerToken, "sort="+sortStr+"&limit=200")
		require.Equal(t, http.StatusOK, code, "sort=%s", sortStr)
		assertNoSecretInBody(t, body, secrets)
	}
}
