//go:build integration

package scheduler_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/events"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

type fakeSender struct {
	sent []*relayv1.CoordinatorMessage
}

func (f *fakeSender) Send(msg *relayv1.CoordinatorMessage) error {
	f.sent = append(f.sent, msg)
	return nil
}

func newTestStore(t *testing.T) *store.Queries {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx,
		"postgres:16",
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

	migrateDSN := "pgx5" + dsn[len("postgres"):]
	require.NoError(t, store.Migrate(migrateDSN))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return store.New(pool)
}

func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func TestDispatcher_DispatchesEligibleTask(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	// Create user.
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name:         "test",
		Email:        "test@example.com",
		IsAdmin:      false,
		PasswordHash: "x",
	})
	require.NoError(t, err)

	// Create job.
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name:        "test-job",
		Priority:    "normal",
		SubmittedBy: user.ID,
		Labels:      []byte(`{}`),
	})
	require.NoError(t, err)

	// Create task.
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID:    job.ID,
		Name:     "test-task",
		Command:  []string{"echo", "hello"},
		Env:      []byte(`{}`),
		Requires: []byte(`{}`),
		Retries:  0,
	})
	require.NoError(t, err)

	// Upsert worker and set it online.
	w, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name:     "worker-1",
		Hostname: "worker-1",
		CpuCores: 4,
		RamGb:    8,
		GpuCount: 0,
		GpuModel: "",
		Os:       "linux",
	})
	require.NoError(t, err)

	w, err = q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:         w.ID,
		Status:     "online",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	// Register a fake sender in the registry.
	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	broker := events.NewBroker()
	d := scheduler.NewDispatcher(q, registry, broker)

	d.Trigger()
	d.RunOnce(ctx)

	// Assert task was dispatched.
	updated, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", updated.Status)

	// Assert correct task was sent to the worker.
	require.Len(t, sender.sent, 1)
	dt := sender.sent[0].GetDispatchTask()
	require.NotNil(t, dt)
	assert.Equal(t, uuidStr(task.ID), dt.TaskId)
}
