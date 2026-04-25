//go:build integration

package scheduler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/events"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5"
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

func newTestStoreWithPool(t *testing.T) (*store.Queries, *pgxpool.Pool) {
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

	return store.New(pool), pool
}

// newTestStore kept for existing tests — discards pool.
func newTestStore(t *testing.T) *store.Queries {
	q, _ := newTestStoreWithPool(t)
	return q
}

func newTestPoolFromQueries(t *testing.T) *pgxpool.Pool {
	_, pool := newTestStoreWithPool(t)
	return pool
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
		Name:           "test-job",
		Priority:       "normal",
		SubmittedBy:    user.ID,
		Labels:         []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
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
	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name:     "worker-1",
		Hostname: "worker-1",
		CpuCores: 4,
		RamGb:    8,
		GpuCount: 0,
		GpuModel: "",
		Os:       "linux",
	})
	require.NoError(t, err)

	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:         wRow.ID,
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

// TestDispatcher_UsesAggregateCountQuery verifies that the in-cycle activeByWorker
// map prevents a single-slot worker from receiving more than one task per dispatch
// cycle, even when multiple eligible tasks are available. This locks in the behavior
// added by CountActiveTasksByAllWorkers: the map is pre-loaded from DB once, then
// incremented on each successful dispatch so selectWorker sees the updated count.
func TestDispatcher_UsesAggregateCountQuery(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "u@agg.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	// Create 3 independent pending tasks — all eligible, no dependencies.
	taskIDs := make([]pgtype.UUID, 3)
	for i := range taskIDs {
		task, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID:   job.ID,
			Name:    fmt.Sprintf("task-%d", i),
			Command: []string{"echo", fmt.Sprintf("%d", i)},
			Env:     []byte(`{}`),
			Requires: []byte(`{}`),
		})
		require.NoError(t, err)
		taskIDs[i] = task.ID
	}

	// Worker with MaxSlots=1 (the UpsertWorkerByHostname default).
	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "w", Hostname: "w", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:         wRow.ID,
		Status:     "online",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), w.MaxSlots)

	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	broker := events.NewBroker()
	d := scheduler.NewDispatcher(q, registry, broker)
	d.RunOnce(ctx)

	// Only 1 of the 3 tasks should have been dispatched despite all 3 being eligible.
	assert.Len(t, sender.sent, 1, "single-slot worker must receive exactly 1 task per dispatch cycle")

	dispatched := 0
	pending := 0
	for _, id := range taskIDs {
		task, err := q.GetTask(ctx, id)
		require.NoError(t, err)
		switch task.Status {
		case "dispatched":
			dispatched++
		case "pending":
			pending++
		}
	}
	assert.Equal(t, 1, dispatched, "exactly 1 task should be dispatched")
	assert.Equal(t, 2, pending, "remaining 2 tasks should still be pending")
}

func TestClaimTaskForWorker_IsAtomic(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	// Seed user, job, pending task, and worker.
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "u@x.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Command: []string{"echo"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	w, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "w", Hostname: "w", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	// First claim must succeed.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, "dispatched", claimed.Status)

	// Second claim of the same task must return ErrNoRows.
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	assert.ErrorIs(t, err, pgx.ErrNoRows)

	// Revert with RequeueTask restores the task to pending.
	err = q.RequeueTask(ctx, task.ID)
	require.NoError(t, err)
	reread, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", reread.Status)
	assert.False(t, reread.WorkerID.Valid)
}

func TestDispatcher_PrefersWarmWorker(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStoreWithPool(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "warm@x", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	src := []byte(`{"type":"perforce","stream":"//s/x","sync":[{"path":"//s/x/...","rev":"#head"}]}`)
	_, err = q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
		JobID: job.ID, Name: "t", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`), Source: src,
	})
	require.NoError(t, err)

	// Create two workers.
	coldRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "cold", Hostname: "cold", CpuCores: 8, RamGb: 8, Os: "linux",
	})
	require.NoError(t, err)
	cold, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: coldRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	// Give cold 8 slots so it would win on free slots alone.
	_, err = pool.Exec(ctx, "UPDATE workers SET max_slots = 8 WHERE id = $1", cold.ID)
	require.NoError(t, err)
	cold.MaxSlots = 8

	warmRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "warm", Hostname: "warm", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	warm, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: warmRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	// Give warm a workspace for the same stream (same source_key).
	require.NoError(t, q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID: warm.ID, SourceType: "perforce", SourceKey: "//s/x", ShortID: "abc",
		BaselineHash: "ignored", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}))

	coldSender := &fakeSender{}
	warmSender := &fakeSender{}
	registry := worker.NewRegistry()
	registry.Register(uuidStr(cold.ID), coldSender)
	registry.Register(uuidStr(warm.ID), warmSender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker())
	d.RunOnce(ctx)

	// warm must win: score = 1 (free slot) + 1,000 (stream match) = 1001 vs cold = 8.
	require.Len(t, warmSender.sent, 1, "warm worker (stream match) should be preferred")
	require.Empty(t, coldSender.sent, "cold worker should not be chosen")
}

func TestDispatcher_ColdFallback_NoWarmWorker(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStoreWithPool(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u2", Email: "cold@x", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j2", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	src := []byte(`{"type":"perforce","stream":"//s/y","sync":[{"path":"//s/y/...","rev":"#head"}]}`)
	_, err = q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
		JobID: job.ID, Name: "t2", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`), Source: src,
	})
	require.NoError(t, err)

	// Only one worker, no warm workspace — dispatcher should still assign it.
	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "only", Hostname: "only", CpuCores: 4, RamGb: 4, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	_ = pool // pool available for direct SQL if needed

	sender := &fakeSender{}
	registry := worker.NewRegistry()
	registry.Register(uuidStr(w.ID), sender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker())
	d.RunOnce(ctx)

	require.Len(t, sender.sent, 1, "task must still be dispatched when no warm worker exists")
}

func TestDispatcher_PassesSourceToAgent(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	// Create user.
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name:         "src-user",
		Email:        "src@example.com",
		IsAdmin:      false,
		PasswordHash: "x",
	})
	require.NoError(t, err)

	// Create job.
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name:           "source-job",
		Priority:       "normal",
		SubmittedBy:    user.ID,
		Labels:         []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	// Build and marshal a source spec JSON blob.
	sourceJSON, err := json.Marshal(map[string]any{
		"type":   "perforce",
		"stream": "//streams/X/main",
		"sync": []map[string]any{
			{"path": "//streams/X/main/...", "rev": "#head"},
		},
	})
	require.NoError(t, err)

	// Create task with source.
	task, err := q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
		JobID:    job.ID,
		Name:     "src-task",
		Command:  []string{"echo", "source"},
		Env:      []byte(`{}`),
		Requires: []byte(`{}`),
		Retries:  0,
		Source:   sourceJSON,
	})
	require.NoError(t, err)

	// Create worker and set online.
	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name:     "src-worker",
		Hostname: "src-worker",
		CpuCores: 4,
		RamGb:    8,
		GpuCount: 0,
		GpuModel: "",
		Os:       "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:         wRow.ID,
		Status:     "online",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	// Register fake sender.
	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	broker := events.NewBroker()
	d := scheduler.NewDispatcher(q, registry, broker)
	d.RunOnce(ctx)

	// Task should be dispatched.
	updated, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", updated.Status)

	// Dispatched message should carry source.
	require.Len(t, sender.sent, 1)
	dt := sender.sent[0].GetDispatchTask()
	require.NotNil(t, dt)
	assert.Equal(t, uuidStr(task.ID), dt.TaskId)
	require.NotNil(t, dt.Source, "DispatchTask.Source must be populated")
	pf := dt.Source.GetPerforce()
	require.NotNil(t, pf, "source provider must be perforce")
	assert.Equal(t, "//streams/X/main", pf.Stream)
	require.Len(t, pf.Sync, 1)
	assert.Equal(t, "//streams/X/main/...", pf.Sync[0].Path)
	assert.Equal(t, "#head", pf.Sync[0].Rev)
}
