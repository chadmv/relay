//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"relay/internal/store"
	"relay/internal/testsupport/pgdsn"
)

// newTestPool returns a *pgxpool.Pool over a fresh, migrated database that
// belongs to this test alone. The pool is cleaned up when t ends;
// pgdsn.NewIntegrationDSN owns the database's own teardown.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := pgdsn.NewIntegrationDSN(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { pgdsn.BoundedCleanup(t, "pool.Close", pool.Close) })

	return pool
}

// newTestQueries returns a *store.Queries over a fresh, migrated database
// that belongs to this test alone.
func newTestQueries(t *testing.T) *store.Queries {
	t.Helper()
	return store.New(newTestPool(t))
}

func newTestUser(t *testing.T, q *store.Queries, isAdmin bool) store.User {
	t.Helper()
	ctx := context.Background()
	ph, err := bcrypt.GenerateFromPassword([]byte("testpass"), bcrypt.MinCost)
	require.NoError(t, err)
	name := "user-" + t.Name()
	email := name + "@example.com"
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: name, Email: email, IsAdmin: isAdmin, PasswordHash: string(ph),
	})
	require.NoError(t, err)
	return user
}

func newTestWorker(t *testing.T, q *store.Queries) store.Worker {
	t.Helper()
	ctx := context.Background()
	hostname := "test-worker-" + t.Name()
	row, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: hostname, Hostname: hostname, CpuCores: 4, RamGb: 16,
		GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.GetWorker(ctx, row.ID)
	require.NoError(t, err)
	return w
}

func ptrStr(s string) *string { return &s }
