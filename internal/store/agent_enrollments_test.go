//go:build integration

package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func TestAgentEnrollments_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	admin := newTestUser(t, q, true)

	created, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash:    "abc123",
		HostnameHint: ptrStr("render-node-01"),
		CreatedBy:    admin.ID,
		ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	got, err := q.GetAgentEnrollmentByTokenHash(ctx, "abc123")
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.False(t, got.ConsumedAt.Valid)
}

func TestAgentEnrollments_ConsumeIsSingleShot(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	admin := newTestUser(t, q, true)
	worker := newTestWorker(t, q)

	enroll, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "consume1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	rows, err := q.ConsumeAgentEnrollment(ctx, store.ConsumeAgentEnrollmentParams{
		ID:         enroll.ID,
		ConsumedBy: worker.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)

	// Second consume should affect 0 rows.
	rows2, err := q.ConsumeAgentEnrollment(ctx, store.ConsumeAgentEnrollmentParams{
		ID:         enroll.ID,
		ConsumedBy: worker.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), rows2)
}

func TestAgentEnrollments_ListActiveExcludesConsumedAndExpired(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	admin := newTestUser(t, q, true)
	worker := newTestWorker(t, q)

	active, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "active1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	expired, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "expired1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err)

	consumed, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "consumed1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
	_, err = q.ConsumeAgentEnrollment(ctx, store.ConsumeAgentEnrollmentParams{ID: consumed.ID, ConsumedBy: worker.ID})
	require.NoError(t, err)

	list, err := q.ListActiveAgentEnrollments(ctx)
	require.NoError(t, err)

	// Compare IDs by UUID bytes string representation
	uuidStr := func(u pgtype.UUID) string { return fmt.Sprintf("%x", u.Bytes) }
	seen := make(map[string]bool)
	for _, e := range list {
		seen[uuidStr(e.ID)] = true
	}
	require.True(t, seen[uuidStr(active.ID)], "active should be listed")
	require.False(t, seen[uuidStr(expired.ID)], "expired should not be listed")
	require.False(t, seen[uuidStr(consumed.ID)], "consumed should not be listed")
}

func TestAgentEnrollments_DeleteExpired(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	admin := newTestUser(t, q, true)

	_, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "old1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err)
	_, err = q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "fresh1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	rows, err := q.DeleteExpiredAgentEnrollments(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)
}
