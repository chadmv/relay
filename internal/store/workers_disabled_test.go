//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkerDisableEnable_RoundTrip(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	w := newTestWorker(t, q)
	require.False(t, w.DisabledAt.Valid, "a new worker must start enabled")

	n, err := q.DisableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "first disable must affect one row")

	got, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.True(t, got.DisabledAt.Valid, "worker must be disabled")

	// Idempotent: a second disable affects zero rows and does not re-stamp.
	n, err = q.DisableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), n, "second disable must affect zero rows")

	n, err = q.EnableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "enable must affect one row")

	got, err = q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.False(t, got.DisabledAt.Valid, "worker must be enabled again")

	// Idempotent: a second enable affects zero rows.
	n, err = q.EnableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), n, "second enable must affect zero rows")
}
