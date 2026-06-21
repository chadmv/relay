//go:build integration

package store_test

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/stretchr/testify/require"
)

func TestClearWorkerAgentToken_StampsRevokedAt(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	w := newTestWorker(t, q)

	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: w.ID, AgentTokenHash: ptrStr("hash-1"),
	}))

	_, err := q.ClearWorkerAgentToken(ctx, w.ID)
	require.NoError(t, err)

	reloaded, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, "revoked", reloaded.Status)
	require.True(t, reloaded.RevokedAt.Valid, "revoked_at must be stamped on revoke")
}

func TestSetWorkerAgentToken_ClearsRevokedAt(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	w := newTestWorker(t, q)

	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: w.ID, AgentTokenHash: ptrStr("hash-1"),
	}))
	_, err := q.ClearWorkerAgentToken(ctx, w.ID)
	require.NoError(t, err)

	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: w.ID, AgentTokenHash: ptrStr("hash-2"),
	}))

	reloaded, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.False(t, reloaded.RevokedAt.Valid, "revoked_at must be cleared on re-enroll")
}

func TestSetWorkerAgentToken_RevivesRevokedStatus(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	w := newTestWorker(t, q)

	// Give the worker a token, then revoke it: status='revoked', revoked_at set.
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: w.ID, AgentTokenHash: ptrStr("hash-1"),
	}))
	_, err := q.ClearWorkerAgentToken(ctx, w.ID)
	require.NoError(t, err)

	revoked, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, "revoked", revoked.Status)
	require.True(t, revoked.RevokedAt.Valid, "precondition: revoke stamps revoked_at")

	// Re-enroll: setting a fresh token must clear BOTH revoked_at and the
	// revoked status atomically, leaving no window where a revoked worker has
	// a null revocation timestamp.
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: w.ID, AgentTokenHash: ptrStr("hash-2"),
	}))

	reloaded, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.False(t, reloaded.RevokedAt.Valid, "revoked_at must be cleared on re-enroll")
	require.NotEqual(t, "revoked", reloaded.Status, "status must not remain revoked after re-enroll")
	require.Equal(t, "offline", reloaded.Status, "revived worker should be offline until it connects")
}

func TestListRevokedWorkersPage_ReturnsOnlyRevoked(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	// newTestWorker derives its hostname from t.Name() and upserts by hostname,
	// so two calls in the same test would collide on one row. Subtests give each
	// worker a distinct t.Name() and therefore a distinct hostname.
	var live, gone store.Worker
	t.Run("live", func(t *testing.T) { live = newTestWorker(t, q) })
	t.Run("gone", func(t *testing.T) { gone = newTestWorker(t, q) })

	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: gone.ID, AgentTokenHash: ptrStr("h"),
	}))
	_, err := q.ClearWorkerAgentToken(ctx, gone.ID)
	require.NoError(t, err)

	rows, err := q.ListRevokedWorkersPage(ctx, store.ListRevokedWorkersPageParams{
		CursorSet: false,
		PageLimit: 50,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, gone.ID, rows[0].ID)
	require.NotEqual(t, live.ID, rows[0].ID)
}
