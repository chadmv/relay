//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTeardownConnection_StaleSenderDoesNotClobberFreshRegistration proves the
// Identity-checked teardown invariant: a half-open stream returning AFTER the
// same agent reconnected must not unregister the fresh sender, mark the live
// worker offline, or requeue the agent's running task.
func TestTeardownConnection_StaleSenderDoesNotClobberFreshRegistration(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	// Seed a user + job + task, claim it for an online worker.
	user, err := fx.Q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "task-user", Email: "task-user@test.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)

	job, err := fx.Q.CreateJob(ctx, store.CreateJobParams{
		Name: "teardown-job", Priority: "normal", SubmittedBy: user.ID,
		Labels: []byte("{}"), ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	task, err := fx.Q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "teardown-task",
		Commands: []byte(`[["echo","hi"]]`), Env: []byte("{}"), Requires: []byte("[]"), Retries: 0,
	})
	require.NoError(t, err)

	wk, err := fx.Q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "teardown-worker", Hostname: "teardown-worker-01",
		CpuCores: 4, RamGb: 8, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	// Mark the worker online (a live agent would be online).
	_, err = fx.Q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wk.ID, Status: "online",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	// Claim the task: epoch 0 -> 1, status dispatched, assigned to wk.
	claimed, err := fx.Q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: wk.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	require.Equal(t, "dispatched", claimed.Status)

	workerIDStr := fx.Handler.UUIDStringForTest(wk.ID)

	// Register stale sender A, then fresh sender B replaces it (the reconnect).
	staleStream := &fakeSender{}
	freshStream := &fakeSender{}
	staleH := fx.Handler.RegisteredSenderForTest(workerIDStr, staleStream)
	freshH := fx.Handler.RegisteredSenderForTest(workerIDStr, freshStream)

	// Run the STALE connection's teardown. It must be a no-op for shared state.
	fx.Handler.TeardownConnectionForTest(workerIDStr, staleH)

	// 1. The fresh sender is still registered: a Send reaches B, not an error.
	dispatch := &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: &relayv1.DispatchTask{TaskId: "still-alive"},
		},
	}
	require.NoError(t, fx.Handler.SendToWorkerForTest(workerIDStr, dispatch),
		"fresh sender B must remain registered after stale A teardown")

	// 2. The worker is still online.
	wAfter, err := fx.Q.GetWorker(ctx, wk.ID)
	require.NoError(t, err)
	assert.Equal(t, "online", wAfter.Status, "live worker must stay online")

	// 3. The running task is untouched: same epoch, still assigned, still dispatched.
	taskAfter, err := fx.Q.GetTask(ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), taskAfter.AssignmentEpoch, "task epoch must not be bumped")
	assert.Equal(t, "dispatched", taskAfter.Status, "task must not be requeued to pending")
	assert.Equal(t, wk.ID, taskAfter.WorkerID, "task must remain assigned to the worker")

	// Clean up B's goroutine via its own (legitimate) teardown.
	fx.Handler.TeardownConnectionForTest(workerIDStr, freshH)
}

// TestTeardownConnection_StaleEpochDoesNotRequeueLiveWorker proves the
// connection-epoch fence: a stale connection whose owned epoch (1) is older than
// the row's current epoch (2, set by a fresh reconnect) must not mark the worker
// offline or requeue its running task when its teardown runs.
func TestTeardownConnection_StaleEpochDoesNotRequeueLiveWorker(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	user, err := fx.Q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "epoch-user", Email: "epoch-user@test.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := fx.Q.CreateJob(ctx, store.CreateJobParams{
		Name: "epoch-job", Priority: "normal", SubmittedBy: user.ID,
		Labels: []byte("{}"), ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := fx.Q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "epoch-task",
		Commands: []byte(`[["echo","hi"]]`), Env: []byte("{}"), Requires: []byte("[]"), Retries: 0,
	})
	require.NoError(t, err)
	wk, err := fx.Q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "epoch-worker", Hostname: "epoch-worker-01",
		CpuCores: 4, RamGb: 8, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	// Stale connection S registers: connection_epoch 0 -> 1.
	epochS, err := fx.Handler.RegisterWorkerConnectionForTest(ctx, wk.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), epochS)

	// Claim the task at epoch 0 -> 1, dispatched, assigned to wk.
	claimed, err := fx.Q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: wk.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	require.Equal(t, "dispatched", claimed.Status)

	workerIDStr := fx.Handler.UUIDStringForTest(wk.ID)

	// Register stale sender A carrying connEpoch 1.
	staleStream := &fakeSender{}
	staleH := fx.Handler.RegisteredSenderWithEpochForTest(workerIDStr, staleStream, epochS)

	// Fresh reconnect F: connection_epoch 1 -> 2; register fresh sender B (epoch 2),
	// which replaces A in the registry.
	epochF, err := fx.Handler.RegisterWorkerConnectionForTest(ctx, wk.ID)
	require.NoError(t, err)
	require.Equal(t, int32(2), epochF)
	freshStream := &fakeSender{}
	freshH := fx.Handler.RegisteredSenderWithEpochForTest(workerIDStr, freshStream, epochF)

	// Run S's stale teardown. UnregisterIf returns false (B owns the slot), so it
	// short-circuits; even in the gap interleaving the SQL fence on epoch 1 (row
	// holds 2) would no-op the offline and requeue. Either way: no effect.
	fx.Handler.TeardownConnectionForTest(workerIDStr, staleH)

	// 1. Worker stays online (fence held; B is live).
	wAfter, err := fx.Q.GetWorker(ctx, wk.ID)
	require.NoError(t, err)
	assert.Equal(t, "online", wAfter.Status, "live worker must stay online")
	assert.Equal(t, int32(2), wAfter.ConnectionEpoch, "row must still hold the fresh epoch")

	// 2. Running task untouched: same epoch, dispatched, still assigned.
	taskAfter, err := fx.Q.GetTask(ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), taskAfter.AssignmentEpoch, "task epoch must not be bumped")
	assert.Equal(t, "dispatched", taskAfter.Status, "task must not be requeued")
	assert.Equal(t, wk.ID, taskAfter.WorkerID, "task must remain assigned")

	// 3. Fresh sender B still registered: a Send reaches it.
	dispatch := &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: &relayv1.DispatchTask{TaskId: "still-alive"},
		},
	}
	require.NoError(t, fx.Handler.SendToWorkerForTest(workerIDStr, dispatch),
		"fresh sender B must remain registered after stale teardown")

	// Clean up B's goroutine via its own legitimate teardown.
	fx.Handler.TeardownConnectionForTest(workerIDStr, freshH)
}

// TestTeardownConnection_GapPath_SqlFenceBlocksOfflineAndRequeue covers the
// finishRegister gap: stale sender S is the sole registry owner (UnregisterIf
// returns true), but the DB row's connection_epoch has already been advanced to
// 2 by a fresh RegisterWorkerConnection that has NOT yet called
// registry.Register. The SQL fence in MarkWorkerOfflineIfEpoch must return
// zero rows, causing teardownConnection to exit without requeueing tasks.
//
// This is the path the previous test (StaleEpochDoesNotRequeueLiveWorker) does
// NOT exercise: there, UnregisterIf returns false and teardownConnection returns
// before touching the DB at all.
func TestTeardownConnection_GapPath_SqlFenceBlocksOfflineAndRequeue(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	// No grace registry: when teardown succeeds the offline write, it would call
	// requeueWorkerTasks directly. The SQL fence must stop it before that.
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "gap-user", Email: "gap-user@test.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "gap-job", Priority: "normal", SubmittedBy: user.ID,
		Labels: []byte("{}"), ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "gap-task",
		Commands: []byte(`[["echo","hi"]]`), Env: []byte("{}"), Requires: []byte("[]"), Retries: 0,
	})
	require.NoError(t, err)
	wk, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "gap-worker", Hostname: "gap-worker-01",
		CpuCores: 4, RamGb: 8, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	workerIDStr := h.UUIDStringForTest(wk.ID)

	// Stale connection S: connection_epoch 0 -> 1, register sender A with epoch 1.
	epochS, err := h.RegisterWorkerConnectionForTest(ctx, wk.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), epochS)

	staleStream := &fakeSender{}
	staleH := h.RegisteredSenderWithEpochForTest(workerIDStr, staleStream, epochS)

	// Claim the task at epoch 0 -> 1.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: wk.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	require.Equal(t, "dispatched", claimed.Status)

	// Fresh reconnect advances the DB row to connection_epoch 2, but the fresh
	// sender has NOT been registered in the in-memory registry yet. This is the
	// finishRegister gap: S's sender still owns the slot.
	_, err = h.RegisterWorkerConnectionForTest(ctx, wk.ID)
	require.NoError(t, err)

	wRow, err := q.GetWorker(ctx, wk.ID)
	require.NoError(t, err)
	require.Equal(t, int32(2), wRow.ConnectionEpoch, "DB must show epoch 2 before stale teardown")

	// Run S's teardown. UnregisterIf returns true (S still owns the slot), so
	// teardownConnection proceeds to MarkWorkerOfflineIfEpoch(epoch=1). The SQL
	// fence sees row epoch=2 != 1 and writes zero rows, so teardownConnection
	// returns without calling requeueWorkerTasks.
	h.TeardownConnectionForTest(workerIDStr, staleH)

	// Worker row must remain online at epoch 2.
	wAfter, err := q.GetWorker(ctx, wk.ID)
	require.NoError(t, err)
	assert.Equal(t, "online", wAfter.Status, "SQL fence must keep worker online")
	assert.Equal(t, int32(2), wAfter.ConnectionEpoch, "epoch must be unchanged")

	// Task must remain dispatched and assigned (not requeued).
	taskAfter, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", taskAfter.Status, "task must not be requeued to pending")
	assert.Equal(t, int32(1), taskAfter.AssignmentEpoch, "task assignment_epoch must be unchanged")
	assert.Equal(t, wk.ID, taskAfter.WorkerID, "task must remain assigned to the worker")
}

// TestGraceFireTime_StaleEpochFencesRequeue proves the grace fire-time fence
// end-to-end: a grace timer armed at connection_epoch 1 fires AFTER the worker
// has reconnected (advancing the DB row to epoch 2). The onExpire callback
// mirrors what cmd/relay-server/main.go wires (direct call to
// RequeueWorkerTasksIfEpoch), so the SQL fence must block the requeue.
func TestGraceFireTime_StaleEpochFencesRequeue(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	registry := worker.NewRegistry()
	broker := events.NewBroker()

	// Wire onExpire exactly as main.go does: call RequeueWorkerTasksIfEpoch
	// directly with the epoch the timer was armed with. A short window so the
	// test completes fast.
	graceWindow := 50 * time.Millisecond
	grace := worker.NewGraceRegistry(graceWindow, func(workerID string, epoch int32) {
		var id pgtype.UUID
		if err := id.Scan(workerID); err != nil {
			return
		}
		_, _ = q.RequeueWorkerTasksIfEpoch(context.Background(), store.RequeueWorkerTasksIfEpochParams{
			WorkerID:        id,
			ConnectionEpoch: epoch,
		})
	})
	defer grace.Stop()

	h := worker.NewHandlerWithGrace(q, pool, registry, broker, func() {}, grace)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "grace-fence-user", Email: "grace-fence@test.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "grace-fence-job", Priority: "normal", SubmittedBy: user.ID,
		Labels: []byte("{}"), ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "grace-fence-task",
		Commands: []byte(`[["echo","hi"]]`), Env: []byte("{}"), Requires: []byte("[]"), Retries: 0,
	})
	require.NoError(t, err)
	wk, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "grace-fence-worker", Hostname: "grace-fence-worker-01",
		CpuCores: 4, RamGb: 8, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	workerIDStr := h.UUIDStringForTest(wk.ID)

	// S connects at epoch 1, claims the task.
	epochS, err := h.RegisterWorkerConnectionForTest(ctx, wk.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), epochS)

	staleStream := &fakeSender{}
	staleH := h.RegisteredSenderWithEpochForTest(workerIDStr, staleStream, epochS)

	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: wk.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// S disconnects: teardownConnection arms a grace timer at epoch 1.
	// S owns the slot, MarkWorkerOfflineIfEpoch(epoch=1) succeeds (row is at 1).
	h.TeardownConnectionForTest(workerIDStr, staleH)

	wAfterDisconnect, err := q.GetWorker(ctx, wk.ID)
	require.NoError(t, err)
	require.Equal(t, "offline", wAfterDisconnect.Status, "worker must be offline after disconnect")

	// Before the grace window elapses, fresh reconnect F bumps DB epoch 1 -> 2
	// and cancels the grace timer (as finishRegister would via grace.Cancel).
	epochF, err := h.RegisterWorkerConnectionForTest(ctx, wk.ID)
	require.NoError(t, err)
	require.Equal(t, int32(2), epochF)
	grace.Cancel(workerIDStr)

	// Confirm the task is still dispatched (grace timer was cancelled before firing).
	taskMid, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "dispatched", taskMid.Status, "task must still be dispatched after reconnect")

	// Now simulate a stale grace timer firing at the old epoch 1 AFTER the
	// reconnect - this is the bug scenario: a timer that slipped past the Cancel
	// (or a seeded timer from an older code path). Fire onExpire directly with
	// the stale epoch.
	grace.ExpireNow(workerIDStr, epochS)

	// SQL fence: RequeueWorkerTasksIfEpoch(epoch=1) sees row epoch=2, moves 0 tasks.
	taskAfter, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", taskAfter.Status, "stale grace fire must not requeue the task")
	assert.Equal(t, int32(1), taskAfter.AssignmentEpoch, "task assignment_epoch must be unchanged")
	assert.Equal(t, wk.ID, taskAfter.WorkerID, "task must remain assigned")

	// Clean up: run F's teardown so F's send goroutine is stopped.
	freshStream := &fakeSender{}
	freshH := h.RegisteredSenderWithEpochForTest(workerIDStr, freshStream, epochF)
	h.TeardownConnectionForTest(workerIDStr, freshH)
}
