//go:build integration

package store_test

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// newNamedTestUser is newTestUser with a caller-supplied name, because this file
// needs two distinct users in one test and newTestUser derives its unique email
// from t.Name().
func newNamedTestUser(t *testing.T, q *store.Queries, name string) store.User {
	t.Helper()
	ph, err := bcrypt.GenerateFromPassword([]byte("testpass"), bcrypt.MinCost)
	require.NoError(t, err)
	u, err := q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name: name, Email: name + "@example.com", IsAdmin: false, PasswordHash: string(ph),
	})
	require.NoError(t, err)
	return u
}

// newTestToken mints a never-expiring api_token for userID and returns the raw hex.
func newTestToken(t *testing.T, q *store.Queries, userID pgtype.UUID) string {
	t.Helper()
	raw := make([]byte, 16)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	rawHex := hex.EncodeToString(raw)
	_, err = q.CreateToken(t.Context(), store.CreateTokenParams{
		UserID:    userID,
		TokenHash: tokenhash.Hash(rawHex),
	})
	require.NoError(t, err)
	return rawHex
}

// DeleteToken must scope on user_id, not on the row id alone.
//
// The id proves WHICH row, never WHOSE. Today's only call site passes a token id
// that BearerAuth resolved from the presented credential, so an unscoped
// statement is not exploitable - but GET /v1/auth/tokens now hands every user
// the UUIDs of their own sessions, and token ids are not secret. The obvious
// implementation of the per-session revoke endpoint that list exists to enable
// (DELETE /v1/auth/tokens/{id}: parse the path UUID, call DeleteToken) would
// turn an unscoped statement into an IDOR that force-logs-out any user,
// including admins. This test is that call site written down in advance: the id
// comes from the wire, the user id comes from the request context, and the two
// belonging to different people must delete nothing.
//
// The second half is not decoration. Without it "delete nothing, ever" would
// satisfy the first half.
func TestDeleteToken_IsScopedToTheOwningUser(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	alice := newNamedTestUser(t, q, "alice-delete-scope")
	bob := newNamedTestUser(t, q, "bob-delete-scope")
	aliceRaw := newTestToken(t, q, alice.ID)
	bobRaw := newTestToken(t, q, bob.ID)

	// Resolve both identities the way BearerAuth does, so the ids under test are
	// the ones a handler would actually hold.
	aliceAuth, err := q.GetTokenWithUser(ctx, tokenhash.Hash(aliceRaw))
	require.NoError(t, err)
	bobAuth, err := q.GetTokenWithUser(ctx, tokenhash.Hash(bobRaw))
	require.NoError(t, err)
	require.NotEqual(t, aliceAuth.TokenUserID, bobAuth.TokenUserID, "fixture: two distinct users")

	// Bob presents Alice's token id. :exec reports no error either way; the
	// assertion is that Alice's session survives.
	require.NoError(t, q.DeleteToken(ctx, store.DeleteTokenParams{
		ID:     aliceAuth.TokenID,
		UserID: bobAuth.TokenUserID,
	}))

	survived, err := q.GetTokenWithUser(ctx, tokenhash.Hash(aliceRaw))
	require.NoError(t, err,
		"Alice's session must survive a delete presented with Bob's identity")
	assert.Equal(t, aliceAuth.TokenID, survived.TokenID)

	// Bob's own token is untouched by the failed attempt.
	_, err = q.GetTokenWithUser(ctx, tokenhash.Hash(bobRaw))
	require.NoError(t, err, "the rejected delete must not have hit the caller's own row")

	// The owner can still revoke their own session.
	require.NoError(t, q.DeleteToken(ctx, store.DeleteTokenParams{
		ID:     aliceAuth.TokenID,
		UserID: aliceAuth.TokenUserID,
	}))
	_, err = q.GetTokenWithUser(ctx, tokenhash.Hash(aliceRaw))
	assert.ErrorIs(t, err, pgx.ErrNoRows,
		"a user's own token id plus their own user id must delete the row")
}
