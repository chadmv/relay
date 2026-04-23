//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestWorkerAgentToken_SetAndGet(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	w := newTestWorker(t, q)

	err := q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID:             w.ID,
		AgentTokenHash: ptrStr("hash-abc"),
	})
	require.NoError(t, err)

	got, err := q.GetWorkerByAgentTokenHash(ctx, ptrStr("hash-abc"))
	require.NoError(t, err)
	require.Equal(t, w.ID, got.ID)
}

func TestWorkerAgentToken_ClearSetsRevokedAndBlocksLookup(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	w := newTestWorker(t, q)

	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID:             w.ID,
		AgentTokenHash: ptrStr("hash-xyz"),
	}))

	_, err := q.ClearWorkerAgentToken(ctx, w.ID)
	require.NoError(t, err)

	_, err = q.GetWorkerByAgentTokenHash(ctx, ptrStr("hash-xyz"))
	require.Error(t, err)
	require.True(t, errors.Is(err, pgx.ErrNoRows))

	// Status should now be "revoked".
	reloaded, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, "revoked", reloaded.Status)
}

func TestWorkerAgentToken_RevokedWorkerNotFoundByHash(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	w := newTestWorker(t, q)

	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID:             w.ID,
		AgentTokenHash: ptrStr("still-set"),
	}))
	_, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:         w.ID,
		Status:     "revoked",
		LastSeenAt: w.LastSeenAt,
	})
	require.NoError(t, err)

	_, err = q.GetWorkerByAgentTokenHash(ctx, ptrStr("still-set"))
	require.Error(t, err)
	require.True(t, errors.Is(err, pgx.ErrNoRows))
}
