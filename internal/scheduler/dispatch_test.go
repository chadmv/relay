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
	"relay/internal/testsupport/pgdsn"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	dsn := pgdsn.NewIntegrationDSN(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { pgdsn.BoundedCleanup(t, "pool.Close", pool.Close) })

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
		Commands: []byte(`[["echo","hello"]]`),
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
	d := scheduler.NewDispatcher(q, registry, broker, "")

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
			JobID:    job.ID,
			Name:     fmt.Sprintf("task-%d", i),
			Commands: []byte(fmt.Sprintf(`[["echo","%d"]]`, i)),
			Env:      []byte(`{}`),
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
	d := scheduler.NewDispatcher(q, registry, broker, "")
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
		JobID: job.ID, Name: "t", Commands: []byte(`[["echo"]]`),
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
	_, err = q.RequeueTask(ctx, store.RequeueTaskParams{
		ID: task.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: w.ID,
	})
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
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
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

	d := scheduler.NewDispatcher(q, registry, events.NewBroker(), "")
	d.RunOnce(ctx)

	// warm must win: score = 1 (free slot) + 1,000 (stream match) = 1001 vs cold = 8.
	require.Len(t, warmSender.sent, 1, "warm worker (stream match) should be preferred")
	require.Empty(t, coldSender.sent, "cold worker should not be chosen")
}

func TestDispatcher_ColdFallback_NoWarmWorker(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

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
		JobID: job.ID, Name: "t2", Commands: []byte(`[["true"]]`),
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
	sender := &fakeSender{}
	registry := worker.NewRegistry()
	registry.Register(uuidStr(w.ID), sender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker(), "")
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
		Commands: []byte(`[["echo","source"]]`),
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
	d := scheduler.NewDispatcher(q, registry, broker, "")
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

func TestDispatcher_BadCommandsJSON_FailsTaskNoRequeue(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "badcmd@x.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	// Poison: commands is valid JSON but not a [][]string (an object, not an array
	// of arrays). json.Unmarshal into [][]string fails - persistent, unretryable.
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "poison", Commands: []byte(`{"bad":"shape"}`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "w", Hostname: "w", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker(), "")

	// Run two cycles. The bug requeued, so the second cycle would re-claim and the
	// epoch would climb. The fix marks the task 'failed' on cycle one; cycle two is
	// a no-op because the task is no longer 'pending'.
	d.RunOnce(ctx)
	afterFirst, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", afterFirst.Status, "poison commands must fail the task, not requeue it")
	require.Empty(t, sender.sent, "poison task must never be sent to the worker")

	epochAfterFirst := afterFirst.AssignmentEpoch
	d.RunOnce(ctx)
	afterSecond, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", afterSecond.Status, "task stays failed across cycles")
	require.Equal(t, epochAfterFirst, afterSecond.AssignmentEpoch,
		"no churn: a failed task is not re-claimed, so assignment_epoch must not climb")
}

func TestDispatcher_FailClaimedTask_PublishesJobEventOnTerminal(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "failjobevent@x.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	// Poison commands: the only task in the job fails terminally, so RecomputeJobStatus
	// flips the whole job to 'failed'. The dispatcher must mirror handleTaskStatus and
	// publish a 'job' event in addition to the 'task' event.
	_, err = q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "poison", Commands: []byte(`{"bad":"shape"}`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "w", Hostname: "w", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	broker := events.NewBroker()
	ch, cancel := broker.Subscribe(events.Filter{})
	defer cancel()

	d := scheduler.NewDispatcher(q, registry, broker, "")
	d.RunOnce(ctx)

	jobIDStr := uuidStr(job.ID)
	var sawTask, sawJob bool
drain:
	for {
		select {
		case e := <-ch:
			switch e.Type {
			case "task":
				sawTask = true
			case "job":
				assert.Equal(t, jobIDStr, e.JobID, "job event must carry the job id")
				assert.JSONEq(t, fmt.Sprintf(`{"id":%q,"status":"failed"}`, jobIDStr), string(e.Data),
					"job event payload must mirror handleTaskStatus")
				sawJob = true
			}
		case <-time.After(time.Second):
			break drain
		}
	}

	assert.True(t, sawTask, "dispatcher must publish a task event on terminal fail")
	assert.True(t, sawJob, "dispatcher must publish a job event when the job flips terminal")
}

func TestDispatcher_BadSourceJSON_FailsTaskNoLeak(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "badsrc@x.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	// Poison: source is valid Postgres JSON (so the column accepts it) but cannot
	// unmarshal into SourceSpec (an array, not an object). taskIsSourceBearing
	// returns false for an unparseable spec, so selectWorker does NOT require a
	// workspace provider - the task is selected, claimed, then the in-sendTask
	// json.Unmarshal of claimed.Source fails.
	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "w", Hostname: "w", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	task, err := q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
		JobID: job.ID, Name: "poison-src", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Source: []byte(`[1,2,3]`),
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker(), "")
	d.RunOnce(ctx)

	got, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", got.Status,
		"poison source must fail the task, not leave it dispatched (slot leak)")
	require.Empty(t, sender.sent, "poison task must never be sent to the worker")
}

// failingSender is a registered stream whose Send always fails, standing in for
// workerSender.Send's two error values: ErrWorkerDisconnected, and ErrSendTimeout
// after 5s of a full queue. (The third way a dispatch send fails - "worker is not
// connected" - is Registry.Send's own error, returned before workerSender.Send is
// reached, so it is not one of the sender's returns.) Only the ErrSendTimeout
// branch leaves a window wide enough to race: a disconnected sender unblocks
// immediately.
type failingSender struct {
	attempts int
}

func (f *failingSender) Send(*relayv1.CoordinatorMessage) error {
	f.attempts++
	return fmt.Errorf("worker is wedged")
}

// TestDispatcher_SendFailureRequeuesWithRealFenceValues guards the ONE
// production call site of RequeueTask, which had no coverage at all before this
// test: mutating either fence argument in dispatch.go to a zero value left the
// whole scheduler suite green.
//
// THAT IS THE "SHIPS INERT" HAZARD, and it is specific to adding a required
// field to a generated params struct. store.RequeueTaskParams{ID: claimed.ID}
// still compiles; it binds AssignmentEpoch 0 and a NULL WorkerID, the fence then
// matches nothing, and the dispatcher silently stops requeueing tasks whose
// dispatch failed - they sit 'dispatched' forever against a worker that never
// received them, with no log line beyond the send failure itself. No store-level
// test can see that, because the store statement is perfectly correct.
//
// The epoch assertion is what discriminates. A broken fence leaves the task
// 'dispatched' at epoch 1; a working one returns it to 'pending' at epoch 2
// (claim bumps 0 -> 1, requeue bumps 1 -> 2).
func TestDispatcher_SendFailureRequeuesWithRealFenceValues(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "sendfail", Email: "sendfail@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "sendfail-job", Priority: "normal", SubmittedBy: user.ID,
		Labels: []byte(`{}`), ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "sendfail-task", Commands: []byte(`[["echo","hi"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Retries: 0,
	})
	require.NoError(t, err)
	require.Equal(t, int32(0), task.AssignmentEpoch)

	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "worker-sendfail", Hostname: "worker-sendfail", CpuCores: 4, RamGb: 8,
		GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	sender := &failingSender{}
	registry.Register(uuidStr(w.ID), sender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker(), "")
	d.Trigger()
	d.RunOnce(ctx)

	require.Equal(t, 1, sender.attempts, "fixture: the dispatcher must have attempted the send")

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", after.Status,
		"a task whose dispatch could not be sent must be requeued, not left dispatched")
	assert.False(t, after.WorkerID.Valid, "the failed assignment must be cleared")
	assert.Equal(t, int32(2), after.AssignmentEpoch,
		"claim bumps 0 -> 1 and the requeue bumps 1 -> 2; a fence bound to zero values would stop at 1")
}

// TestDispatcher_RendersJobAndTaskURLsFromTheClaimedRow is the wire-level half
// of the feature. It asserts the URLs against THE IDS THIS TEST SEEDED, never
// against dt.JobId / dt.TaskId: sourcing both sides of the comparison from the
// same message makes the test agree with itself by construction and blind both
// to the two ids being swapped and to the URLs being built from the wrong row.
//
// The base carries a path prefix because that is the shape a reverse-proxied
// deployment uses and the one where an accidental separator shows up.
func TestDispatcher_RendersJobAndTaskURLsFromTheClaimedRow(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "urls@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "url-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "url-task", Commands: []byte(`[["echo","hello"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Retries: 0,
	})
	require.NoError(t, err)

	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "url-worker", Hostname: "url-worker", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker(), "https://ops.example.com/relay")
	d.RunOnce(ctx)

	require.Len(t, sender.sent, 1)
	dt := sender.sent[0].GetDispatchTask()
	require.NotNil(t, dt)
	assert.Equal(t, "https://ops.example.com/relay/jobs/"+uuidStr(job.ID), dt.JobUrl)
	assert.Equal(t,
		"https://ops.example.com/relay/jobs/"+uuidStr(job.ID)+"/tasks/"+uuidStr(task.ID),
		dt.TaskUrl,
		"the task URL must nest the TASK id under the JOB id; the two are independently "+
			"generated UUIDs, so a transposed argument pair cannot produce this string")
}

// TestDispatcher_NoPublicURLSendsEmptyURLFieldsButStillSendsTheIds is the
// conjunction. The empty-URL half alone is green against a dispatcher that
// never learned to render anything at all.
func TestDispatcher_NoPublicURLSendsEmptyURLFieldsButStillSendsTheIds(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "nourls@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "no-url-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "no-url-task", Commands: []byte(`[["echo","hello"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Retries: 0,
	})
	require.NoError(t, err)

	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "no-url-worker", Hostname: "no-url-worker", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker(), "")
	d.RunOnce(ctx)

	require.Len(t, sender.sent, 1)
	dt := sender.sent[0].GetDispatchTask()
	require.NotNil(t, dt)
	assert.Empty(t, dt.JobUrl)
	assert.Empty(t, dt.TaskUrl)
	assert.Equal(t, uuidStr(job.ID), dt.JobId,
		"only the URLs depend on RELAY_PUBLIC_URL; the ids must still reach the agent")
	assert.Equal(t, uuidStr(task.ID), dt.TaskId)
}
