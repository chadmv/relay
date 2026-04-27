//go:build integration

package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupPgForStartup(t *testing.T) (*store.Queries, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("relay_test"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, store.Migrate("pgx5"+dsn[len("postgres"):]))
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return store.New(pool), pool
}

// seedWorkerWithDispatchedTask creates user/job/worker/task and claims it.
func seedWorkerWithDispatchedTask(t *testing.T, ctx context.Context, q *store.Queries, hostname string) store.Worker {
	t.Helper()
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: hostname + "@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w", Hostname: hostname, CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
	return w
}

func TestStartupReconcile_NullDisconnectedAtUsesFullWindow(t *testing.T) {
	ctx := context.Background()
	q, _ := setupPgForStartup(t)
	_ = seedWorkerWithDispatchedTask(t, ctx, q, "host-null")
	// Worker row has disconnected_at = NULL by default.

	var fired []string
	var mu sync.Mutex
	window := 50 * time.Millisecond
	grace := worker.NewGraceRegistry(window, func(workerID string) {
		mu.Lock()
		fired = append(fired, workerID)
		mu.Unlock()
	})
	require.NoError(t, seedGraceTimersFromActiveTasks(ctx, q, grace, window))

	// Should not fire before the full window elapses.
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	require.Empty(t, fired, "must not fire before the full window elapses")
	mu.Unlock()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(fired) == 1
	}, 500*time.Millisecond, 5*time.Millisecond)
	grace.Stop()
}

func TestStartupReconcile_PartialRemainingHonored(t *testing.T) {
	ctx := context.Background()
	q, _ := setupPgForStartup(t)
	w := seedWorkerWithDispatchedTask(t, ctx, q, "host-partial")
	// disconnected_at = now - 30ms, window = 50ms → remaining = 20ms.
	disconnectedAt := time.Now().Add(-30 * time.Millisecond)
	_, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:             w.ID,
		Status:         "offline",
		LastSeenAt:     pgtype.Timestamptz{Time: disconnectedAt, Valid: true},
		DisconnectedAt: pgtype.Timestamptz{Time: disconnectedAt, Valid: true},
	})
	require.NoError(t, err)

	var fired atomic.Int32
	window := 50 * time.Millisecond
	grace := worker.NewGraceRegistry(window, func(string) { fired.Add(1) })
	require.NoError(t, seedGraceTimersFromActiveTasks(ctx, q, grace, window))

	require.Eventually(t, func() bool {
		return fired.Load() == 1
	}, 500*time.Millisecond, 5*time.Millisecond)
	grace.Stop()
}

func TestStartupReconcile_ExpiredRemainingFiresImmediately(t *testing.T) {
	ctx := context.Background()
	q, _ := setupPgForStartup(t)
	w := seedWorkerWithDispatchedTask(t, ctx, q, "host-expired")
	// disconnected_at = now - 1h, window = 50ms → already expired.
	disconnectedAt := time.Now().Add(-time.Hour)
	_, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:             w.ID,
		Status:         "offline",
		LastSeenAt:     pgtype.Timestamptz{Time: disconnectedAt, Valid: true},
		DisconnectedAt: pgtype.Timestamptz{Time: disconnectedAt, Valid: true},
	})
	require.NoError(t, err)

	var fired atomic.Int32
	window := 50 * time.Millisecond
	grace := worker.NewGraceRegistry(window, func(string) { fired.Add(1) })
	require.NoError(t, seedGraceTimersFromActiveTasks(ctx, q, grace, window))

	// ExpireNow path is synchronous; fired must be 1 immediately.
	require.Equal(t, int32(1), fired.Load(), "expired remaining must fire synchronously")
	grace.Stop()
}
