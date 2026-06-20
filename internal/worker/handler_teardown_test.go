//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

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
