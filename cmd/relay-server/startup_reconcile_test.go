//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestStartupReconcile_SeedsGraceTimersForActiveWorkers(t *testing.T) {
	ctx := context.Background()
	q, _ := setupPgForStartup(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "startup@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w", Hostname: "startup-h", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	// Run the startup reconcile logic.
	var fired []string
	grace := worker.NewGraceRegistry(30*time.Millisecond, func(workerID string) {
		fired = append(fired, workerID)
	})
	require.NoError(t, seedGraceTimersFromActiveTasks(ctx, q, grace))

	// The task is still "dispatched" — we did NOT blanket-requeue.
	fetched, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", fetched.Status)

	// After the grace window with no reconnect, the timer fires.
	time.Sleep(80 * time.Millisecond)
	grace.Stop()
	require.Len(t, fired, 1)
}
