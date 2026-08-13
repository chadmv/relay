//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedTaskAndTwoWorkers creates a user, a job, an UNCLAIMED task with the given
// retry budget, and two distinct workers. Claiming is left to each test: some of
// these tests are specifically about a task that was never claimed, which is the
// state where assignment_epoch is 0 and worker_id is NULL.
//
// w2 is not an attacker in any exotic sense - it is an ordinary enrolled worker
// holding a perfectly valid agent token that simply is not this task's assignee.
// That is the realistic threat model: an agent token is a 0600 file on a host
// that by design runs untrusted job payloads.
func seedTaskAndTwoWorkers(t *testing.T, ctx context.Context, q *store.Queries, prefix string, retries int32) (jobID, taskID, w1, w2 pgtype.UUID) {
	t.Helper()
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: prefix + "@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	a, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: prefix + "-w1", Hostname: prefix + "-w1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	b, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: prefix + "-w2", Hostname: prefix + "-w2", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	require.NotEqual(t, a.ID, b.ID, "the forging worker must be a different row")
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Retries: retries,
	})
	require.NoError(t, err)
	return job.ID, task.ID, a.ID, b.ID
}

// The epoch fence answers "is this generation current". It never answered "are
// you the worker this task is assigned to". Every test in this file sends the
// task's CURRENT epoch on purpose: the epoch predicate matches, so nothing but
// an identity check can reject the message. A stale-epoch variant would be green
// today and therefore vacuous.
//
// Consequence 1 from the backlog item: a forged DONE reports work that never ran
// as successful, and GetEligibleTasks then unblocks the rest of the DAG against
// it.
func TestHandleTaskStatus_RejectsDoneFromANonAssignee(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, w2 := seedTaskAndTwoWorkers(t, ctx, q, "status1", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	taskIDStr := h.UUIDStringForTest(taskID)

	h.HandleTaskStatus(ctx, w2, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr,
		Status: relayv1.TaskStatus_TASK_STATUS_DONE,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	// Non-fatal asserts so a RED run reports every part of the exposure rather
	// than stopping at the first one.
	assert.Equal(t, "dispatched", after.Status, "a non-assignee must not be able to complete a task")
	assert.False(t, after.FinishedAt.Valid, "a rejected update must not stamp finished_at")
	assert.Equal(t, w1, after.WorkerID, "a rejected update must not touch the assignee")
	if t.Failed() {
		t.FailNow() // the forgery got through; the positive control below is moot
	}

	// Positive control on the SAME code path: the real assignee at the same epoch
	// still lands. Without it, a handleTaskStatus that had stopped accepting
	// anything at all would pass every assertion above.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr,
		Status: relayv1.TaskStatus_TASK_STATUS_DONE,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	done, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "done", done.Status, "positive control: the assignee's own DONE must land")
	assert.True(t, done.FinishedAt.Valid, "positive control: finished_at must be stamped")
	assert.Equal(t, w1, done.WorkerID, "a terminal transition must keep the assignee")
}

// Consequence 2, retries = 0 branch: a forged FAILED is a one-message denial of
// service against any job, because FailDependentTasks walks the recursive CTE
// and marks the whole transitive downstream failed.
//
// The assertion that matters here is the one on task B. "A's status did not
// change" is a string comparison; "B is still pending" is the absence of the
// cascade, which is the actual harm.
func TestHandleTaskStatus_RejectsFailedFromANonAssigneeAndDoesNotCascade(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	jobID, taskAID, w1, w2 := seedTaskAndTwoWorkers(t, ctx, q, "status2", 0)
	taskB, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: jobID, Name: "t-b", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NoError(t, q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: taskB.ID, DependsOnTaskID: taskAID,
	}))

	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskAID, WorkerID: w1,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	taskAStr := h.UUIDStringForTest(taskAID)

	h.HandleTaskStatus(ctx, w2, &relayv1.TaskStatusUpdate{
		TaskId: taskAStr,
		Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	afterA, err := q.GetTask(ctx, taskAID)
	require.NoError(t, err)
	afterB, err := q.GetTask(ctx, taskB.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", afterA.Status, "a non-assignee must not be able to fail a task")
	assert.Equal(t, "pending", afterB.Status, "a non-assignee must not be able to cascade a DAG to failed")
	if t.Failed() {
		t.FailNow()
	}

	// Positive control: the real assignee's FAILED still fails A and still
	// cascades to B, so rejection is about identity and not about the cascade
	// having been broken.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskAStr,
		Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	realA, err := q.GetTask(ctx, taskAID)
	require.NoError(t, err)
	realB, err := q.GetTask(ctx, taskB.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", realA.Status, "positive control: the assignee's own FAILED must land")
	assert.Equal(t, "failed", realB.Status, "positive control: the dependent cascade must still fire")
}

// THE test the SQL fence alone does not pass, and the reason the identity check
// has to be in Go.
//
// On a task that opted into retries, a forged FAILED never reaches
// UpdateTaskStatus at all: handleTaskStatus takes the retry branch, calls
// IncrementTaskRetryCount - which has a bare `WHERE id = $1`, no epoch fence and
// no worker fence - and returns. That flips a live task back to pending, NULLs
// its worker_id and BUMPS assignment_epoch, which ends the generation of the
// agent that is genuinely running it and silently kills its log ingest and its
// own status updates. Repeat until retries are exhausted, then the cascade fires
// anyway.
//
// A reviewer must be able to confirm this test goes RED if the Go gate is
// removed while the SQL predicate stays.
func TestHandleTaskStatus_NonAssigneeCannotBurnARetry(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, w2 := seedTaskAndTwoWorkers(t, ctx, q, "status3", 1)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	require.Equal(t, int32(1), claimed.Retries)
	require.Equal(t, int32(0), claimed.RetryCount)
	taskIDStr := h.UUIDStringForTest(taskID)

	h.HandleTaskStatus(ctx, w2, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr,
		Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, int32(0), after.RetryCount, "a non-assignee must not be able to burn a retry")
	assert.Equal(t, "dispatched", after.Status, "a non-assignee must not requeue a live task")
	assert.Equal(t, int32(1), after.AssignmentEpoch, "a non-assignee must not end the assignment generation")
	assert.Equal(t, w1, after.WorkerID, "a non-assignee must not evict the agent that is running this task")
	if t.Failed() {
		t.FailNow()
	}

	// Positive control: the assignee's own FAILED still burns the retry and
	// requeues the task, ending the generation exactly as designed.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr,
		Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	retried, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, int32(1), retried.RetryCount, "positive control: the assignee's FAILED must burn a retry")
	assert.Equal(t, "pending", retried.Status, "positive control: the retry must requeue the task")
	assert.Equal(t, int32(2), retried.AssignmentEpoch, "positive control: the requeue must bump the epoch")
}

// Consequence 3, the backlog item's literal repro and the worst outcome: a
// forged RUNNING on a never-claimed task writes status='running' with worker_id
// NULL, and that row is then invisible to every path that could move it.
// GetEligibleTasks wants status='pending'; RequeueWorkerTasks,
// RequeueWorkerTasksIfEpoch, GetActiveTasksForWorker and ListGraceCandidates are
// all keyed on worker_id, which NULL never matches; nothing sweeps running tasks
// by age. The task stays running forever and its dependents stay pending
// forever.
//
// So the load-bearing assertion is not "the status string did not change" - it
// is that the task is STILL RETURNED BY GetEligibleTasks, i.e. still
// dispatchable. That is the property the wedge destroys.
//
// What this test does NOT pin: the Go NULL trap. It passes a real w1 against a
// NULL task.WorkerID, so a bare `task.WorkerID.Bytes != workerID.Bytes` with
// both .Valid checks dropped still rejects it. Only a case where BOTH sides are
// zero-valued discriminates a NULL-tolerant comparison from a NULL-rejecting
// one, and this is not that case. What it pins through the handler is the fence
// as a whole, which the SQL predicate would also satisfy on this path. The Go
// gate's own NULL rejection is pinned by
// TestHandleTaskStatus_ZeroValueWorkerIdCannotBurnARetryOnANeverClaimedTask
// below.
func TestHandleTaskStatus_RejectsRunningForANeverClaimedTask(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "status4", 0)

	unclaimed, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, int32(0), unclaimed.AssignmentEpoch, "a never-claimed task must sit at epoch 0")
	require.False(t, unclaimed.WorkerID.Valid, "a never-claimed task must have a NULL worker_id")
	taskIDStr := h.UUIDStringForTest(taskID)

	// Epoch 0 is a free guess, so this is the cheapest message in the whole
	// threat model - and RUNNING is the first status a real agent sends, so it is
	// also the easiest to trigger by accident.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr,
		Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		Epoch:  0,
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "pending", after.Status, "a never-claimed task must reject RUNNING from every worker")
	assert.False(t, after.WorkerID.Valid, "a rejected update must not assign the task")

	eligible, err := q.GetEligibleTasks(ctx)
	require.NoError(t, err)
	var dispatchable bool
	for _, e := range eligible {
		if e.ID == taskID {
			dispatchable = true
		}
	}
	assert.True(t, dispatchable, "the task must still be dispatchable - a forged RUNNING must not wedge it out of GetEligibleTasks")
	if t.Failed() {
		t.FailNow()
	}

	// Positive control: once the task really is claimed by w1, that same worker's
	// RUNNING at the new epoch lands. Rejection must be about assignment, not
	// about this worker or this task being inert.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr,
		Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	running, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "running", running.Status, "positive control: the assignee's RUNNING must land after the claim")
	assert.True(t, running.StartedAt.Valid, "positive control: started_at must be stamped")
}

// NOTE (2026-08-12, retry-resurrect status guard): this test no longer
// discriminates the GO gate. It was written as the permanent guard for that
// gate's NULL rejection, and it was discriminating only because the retry branch
// escaped the SQL fence. IncrementTaskRetryCount now carries its own
// `worker_id = $` predicate, so a zero-value caller is rejected in SQL and this
// test stays green even with the Go gate deleted outright. It is kept because it
// is still a valid guard - at the SQL layer, on the `=`-not-`IS NOT DISTINCT
// FROM` rule, reached through the real handler rather than through a store-level
// call - and because the construction below is still the only one in this
// package that is NULL on both sides. Its discriminating power moved; the test
// did not become worthless, it changed what it proves. That no test now
// discriminates the Go gate is recorded as a Known Limitation in
// docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md; the gate's
// remaining value is non-functional - one database round trip instead of two per
// forged message, since GetTask has already run before the gate - which is why it
// is written down rather than pinned. It saves no log lines: both write sites
// drop pgx.ErrNoRows silently.
//
// What the construction below still buys, unchanged:
//
// Two properties have to hold at once for a test to catch a gate that dropped
// them, and no other test in this package has both:
//
//   - Both sides of the comparison must be zero-valued. A never-claimed task has
//     worker_id NULL; the caller passes pgtype.UUID{}. pgtype.UUID is a
//     comparable struct, so with the .Valid checks gone the two zero values
//     compare EQUAL - the Go form of IS NOT DISTINCT FROM - and the gate falls
//     open. Every other test here puts a real UUID on at least one side, where a
//     bare comparison still rejects and therefore proves nothing.
//   - The forged message is routed through the RETRY branch, so the task
//     carries Retries: 1 and the status is FAILED. That branch returns before
//     UpdateTaskStatus is ever reached. Before the retry statement was fenced
//     this is what made the test independent of the SQL layer entirely; now it
//     is what selects WHICH statement's worker predicate the test exercises -
//     IncrementTaskRetryCount's, not UpdateTaskStatus's. A DONE or RUNNING
//     variant would exercise UpdateTaskStatus's instead, which
//     TestUpdateTaskStatus_AssigneeGuarded case 4 already covers at the store
//     layer.
//
// Epoch 0 on a never-claimed task means the currency gate matches, leaving
// identity as the only thing that can reject - in Go, in SQL, or both. What this
// test discriminates now, MEASURED rather than reasoned:
//
//	Go gate deleted, SQL predicate intact ....... PASSES (verified)
//	SQL worker predicate neutered (matrix M3) ... PASSES (whole package ok)
//	SQL worker predicate as IS NOT DISTINCT FROM
//	  (matrix M4) ............................... PASSES (whole package ok)
//
// So it discriminates NEITHER layer on its own - only the conjunction, and no
// matrix row removes both at once. An earlier draft of this comment claimed it
// "goes red instead if IncrementTaskRetryCount's worker predicate is dropped or
// rewritten"; that is false, and the plan's own matrix rows M3 and M4 list the
// handler tests in the GREEN column, so the matrix was right and the prose was
// wrong. The SQL-layer property is pinned by store cases 6 and 7 instead, which
// is where M3 and M4 actually go red.
//
// It is therefore retained as a SCENARIO, not as a guard: NULL on both sides of
// the comparison, driven through the real handler rather than through a
// store-level call, which is a shape no other test in this package has. Keep it
// for the coverage of that path; do not cite it as evidence for either layer.
func TestHandleTaskStatus_ZeroValueWorkerIdCannotBurnARetryOnANeverClaimedTask(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "status6", 1)

	unclaimed, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, int32(0), unclaimed.AssignmentEpoch, "a never-claimed task must sit at epoch 0")
	require.False(t, unclaimed.WorkerID.Valid, "a never-claimed task must have a NULL worker_id")
	require.Equal(t, int32(1), unclaimed.Retries, "the retry branch is what makes this independent of the SQL fence")
	taskIDStr := h.UUIDStringForTest(taskID)

	// A caller that lost its identity. In production Connect closes the stream
	// rather than reaching here with a zero value, so this is defense in depth -
	// but it is the behavior the two .Valid checks exist to produce, and it must
	// fail CLOSED.
	h.HandleTaskStatus(ctx, pgtype.UUID{}, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr,
		Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch:  0,
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, int32(0), after.RetryCount, "a zero-value worker id must not be able to burn a retry")
	assert.Equal(t, int32(0), after.AssignmentEpoch, "a zero-value worker id must not be able to bump the epoch")
	assert.Equal(t, "pending", after.Status, "a rejected update must not move the row")
	if t.Failed() {
		t.FailNow()
	}

	// Positive control: the same FAILED, from the task's real assignee at the
	// real epoch, does burn the retry. Rejection above is about the identity
	// being absent, not about this task or this branch being inert.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr,
		Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	retried, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, int32(1), retried.RetryCount, "positive control: the assignee's FAILED must burn a retry")
	assert.Equal(t, int32(2), retried.AssignmentEpoch, "positive control: the requeue must bump the epoch")
}

// Modelled on TestConnect_TaskLogChunkIsFencedOnTheConnectionsOwnWorker in
// handler_tasklog_integration_test.go, for the same reason: the shim-driven
// tests above leave Connect's own call site unpinned, and a zero-value or wrong
// worker id there would fail closed on every status update from every agent
// while the whole package stayed green.
//
// Two fixture details are load-bearing:
//   - Auto-enroll upserts by hostname and returns the EXISTING row's id, so
//     seeding the claimed task against the same hostname makes this connection
//     resolve to the very worker the task is assigned to. The assertion on the
//     register response's worker_id is what proves that actually happened; drop
//     it and the test proves nothing.
//   - The register message must report the claimed task in RunningTasks.
//     finishRegister runs reconcileRunningTasks, which requeues any task the
//     coordinator has assigned to this worker but the agent did not report, and a
//     requeue bumps assignment_epoch - which would make the status update below
//     stale for a reason that has nothing to do with what is under test.
func TestConnect_TaskStatusIsFencedOnTheConnectionsOwnWorker(t *testing.T) {
	fx := newWorkerTestFixture(t)
	fx.Handler.AllowAutoEnroll = true
	ctx := context.Background()
	q := fx.Q

	const hostname = "w-connect-status-wiring"
	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "status5@example.com", hostname)
	taskIDStr := fx.Handler.UUIDStringForTest(taskID)

	stream := newMockConnectStream(t)
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: hostname,
				CpuCores: 1, RamGb: 1, Os: "linux",
				RunningTasks: []*relayv1.RunningTask{
					{TaskId: taskIDStr, Epoch: int64(epoch)},
				},
			},
		},
	})
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskStatus{
			TaskStatus: &relayv1.TaskStatusUpdate{
				TaskId: taskIDStr,
				Status: relayv1.TaskStatus_TASK_STATUS_DONE,
				Epoch:  int64(epoch),
			},
		},
	})

	done := make(chan error, 1)
	go func() { done <- fx.Handler.Connect(stream) }()

	resp := stream.RecvFromServer(t, 5*time.Second).GetRegisterResponse()
	require.NotNil(t, resp)
	require.Equal(t, fx.Handler.UUIDStringForTest(workerID), resp.WorkerId,
		"the connection must resolve to the task's assignee, or this test proves nothing")

	// The status message is processed after the register response is sent, so
	// poll rather than racing it.
	require.Eventually(t, func() bool {
		fresh, err := q.GetTask(ctx, taskID)
		return err == nil && fresh.Status == "done"
	}, 10*time.Second, 20*time.Millisecond,
		"Connect must pass its own authenticated worker id to handleTaskStatus")

	fresh, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.True(t, fresh.FinishedAt.Valid, "a terminal transition must stamp finished_at")
	// A terminal transition keeps the assignee and does not bump the epoch, so
	// trailing log chunks from the agent that just finished still pass
	// AppendTaskLog's fence.
	assert.Equal(t, epoch, fresh.AssignmentEpoch, "UpdateTaskStatus must not bump the epoch")
	assert.Equal(t, workerID, fresh.WorkerID, "a terminal transition must not release the assignee")

	stream.CloseSend()
	<-done
}

// ROUTE B2: a second terminal message from the task's OWN assignee, at the
// CURRENT epoch, on a task with no retries left. Both of handleTaskStatus's
// gates legitimately pass - it really is the assignee and the epoch really is
// current - because a terminal transition deliberately does not bump
// assignment_epoch and does not clear worker_id (that is what lets a trailing
// log chunk still pass AppendTaskLog's fence). With retries exhausted the retry
// branch is skipped, so this message goes straight to UpdateTaskStatus and
// flips a `done` task to `failed`.
//
// The load-bearing assertion is the one on task B. "A's status did not change"
// is a string comparison; "B is still pending" is the absence of the
// FailDependentTasks cascade, which is the actual harm: a completed task's
// entire still-pending downstream destroyed by a duplicate message.
//
// A correct agent never produces this - Runner.Run sends exactly one terminal
// status per invocation and gRPC does not redeliver - but a crash-looping or
// double-dispatching agent does, with no attacker and no concurrency.
//
// This test can only be red for UpdateTaskStatus's status predicate: its FAILED
// skips the retry branch entirely, so IncrementTaskRetryCount is never reached.
// Its sibling
// TestHandleTaskStatus_AssigneeCannotResurrectItsOwnCompletedTaskViaRetry is the
// mirror image and can only be red for the retry statement's predicate.
func TestHandleTaskStatus_ASecondTerminalFromTheAssigneeDoesNotOverwriteOrCascade(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	jobID, taskAID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "status8", 0)
	taskB, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: jobID, Name: "t-b", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NoError(t, q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: taskB.ID, DependsOnTaskID: taskAID,
	}))

	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskAID, WorkerID: w1,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	taskAStr := h.UUIDStringForTest(taskAID)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskAStr,
		Status: relayv1.TaskStatus_TASK_STATUS_DONE,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	afterDone, err := q.GetTask(ctx, taskAID)
	require.NoError(t, err)
	require.Equal(t, "done", afterDone.Status, "fixture: the assignee's own DONE must land")
	require.True(t, afterDone.FinishedAt.Valid, "fixture: DONE must stamp finished_at")

	// The duplicate. Same worker, same epoch, terminal status.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskAStr,
		Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	afterFailed, err := q.GetTask(ctx, taskAID)
	require.NoError(t, err)
	afterB, err := q.GetTask(ctx, taskB.ID)
	require.NoError(t, err)
	// Non-fatal asserts so a RED run reports every part of the exposure rather
	// than stopping at the first one.
	assert.Equal(t, "done", afterFailed.Status, "a finished task must not be overwritten by a second terminal status")
	assert.True(t, afterFailed.FinishedAt.Time.Equal(afterDone.FinishedAt.Time),
		"a rejected update must not restamp finished_at")
	assert.Equal(t, "pending", afterB.Status,
		"a duplicate terminal must not cascade FailDependentTasks across a completed task's downstream")
	if t.Failed() {
		t.FailNow() // the overwrite got through; the positive control below is moot
	}

	// Positive control on the SAME code path: a task that is genuinely running
	// still fails and still cascades to its dependent. Without it, a
	// handleTaskStatus that had stopped accepting anything at all would pass
	// every assertion above.
	taskC, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: jobID, Name: "t-c", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	taskD, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: jobID, Name: "t-d", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NoError(t, q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: taskD.ID, DependsOnTaskID: taskC.ID,
	}))
	claimedC, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskC.ID, WorkerID: w1,
	})
	require.NoError(t, err)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskC.ID),
		Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch:  int64(claimedC.AssignmentEpoch),
	})
	realC, err := q.GetTask(ctx, taskC.ID)
	require.NoError(t, err)
	realD, err := q.GetTask(ctx, taskD.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", realC.Status, "positive control: a live task's FAILED must still land")
	assert.Equal(t, "failed", realD.Status, "positive control: the dependent cascade must still fire")
}

// dispatchable reports whether GetEligibleTasks currently returns taskID, i.e.
// whether the dispatcher would pick it up right now. Asserting on this rather
// than on a status string is what makes the resurrection tests express the harm
// instead of the symptom.
func dispatchable(t *testing.T, ctx context.Context, q *store.Queries, taskID pgtype.UUID) bool {
	t.Helper()
	eligible, err := q.GetEligibleTasks(ctx)
	require.NoError(t, err)
	for _, e := range eligible {
		if e.ID == taskID {
			return true
		}
	}
	return false
}

// ROUTE B1: the task's OWN assignee, at the CURRENT epoch, resurrecting a task
// it already completed - on a task that still has a retry budget, so the second
// terminal takes the RETRY branch and never reaches UpdateTaskStatus.
//
// Both gates pass legitimately. A terminal transition does not bump
// assignment_epoch and does not clear worker_id (that is what lets a trailing
// log chunk still pass AppendTaskLog's fence), so after a DONE the row still
// satisfies identity and currency for that worker at that epoch. In
// handleTaskStatus the `terminal` computation and the retry branch's condition
// read only the wire status and the T0 row's RetryCount/Retries - never the T0
// row's status - so IncrementTaskRetryCount moves a COMPLETED task back to
// pending. (Symbols, not line numbers: an earlier draft cited handler.go:512 and
// :515, which this PR's own edits made stale before it merged.)
//
// The load-bearing assertion is the one on the dependent: "A is still done" is a
// string comparison, "B is STILL dispatchable" is the harm - a task resurrected
// and re-dispatched while its dependents are already running.
//
// This test can only be red for IncrementTaskRetryCount's status predicate: its
// FAILED takes the retry branch and returns, so UpdateTaskStatus is never
// reached. Its sibling
// TestHandleTaskStatus_ASecondTerminalFromTheAssigneeDoesNotOverwriteOrCascade
// is the mirror image. Removing either predicate reddens exactly one of them.
func TestHandleTaskStatus_AssigneeCannotResurrectItsOwnCompletedTaskViaRetry(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	jobID, taskAID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "status7", 1)
	taskB, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: jobID, Name: "t-b", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NoError(t, q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: taskB.ID, DependsOnTaskID: taskAID,
	}))

	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskAID, WorkerID: w1,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	require.Equal(t, int32(1), claimed.Retries)
	taskAStr := h.UUIDStringForTest(taskAID)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskAStr,
		Status: relayv1.TaskStatus_TASK_STATUS_DONE,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	afterDone, err := q.GetTask(ctx, taskAID)
	require.NoError(t, err)
	require.Equal(t, "done", afterDone.Status, "fixture: the assignee's own DONE must land")
	require.True(t, dispatchable(t, ctx, q, taskB.ID),
		"fixture: B must become eligible once A is done, or the assertion below proves nothing")

	// The duplicate. Same worker, same epoch, terminal status, retry budget left.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskAStr,
		Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	afterFailed, err := q.GetTask(ctx, taskAID)
	require.NoError(t, err)
	assert.Equal(t, "done", afterFailed.Status, "a completed task must not be resurrected by a second terminal")
	assert.Equal(t, int32(0), afterFailed.RetryCount, "a completed task has no generation to fail - no retry may be burned")
	assert.Equal(t, int32(1), afterFailed.AssignmentEpoch, "a rejected retry must not end the assignment")
	assert.Equal(t, w1, afterFailed.WorkerID, "a rejected retry must not clear the assignee")
	assert.True(t, dispatchable(t, ctx, q, taskB.ID),
		"the dependent must stay dispatchable - resurrecting A drops B out of GetEligibleTasks")
	if t.Failed() {
		t.FailNow() // the resurrection got through; the positive control below is moot
	}

	// Positive control on the SAME code path: a task that is genuinely running
	// still burns its retry when its assignee reports FAILED. Without it, a
	// handleTaskStatus that had stopped accepting anything at all would pass
	// every assertion above.
	live, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: jobID, Name: "t-live", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Retries: 1,
	})
	require.NoError(t, err)
	claimedLive, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: live.ID, WorkerID: w1,
	})
	require.NoError(t, err)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(live.ID),
		Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch:  int64(claimedLive.AssignmentEpoch),
	})
	retried, err := q.GetTask(ctx, live.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), retried.RetryCount, "positive control: a live task's FAILED must still burn a retry")
	assert.Equal(t, "pending", retried.Status, "positive control: the retry must requeue the task")
	assert.Equal(t, int32(2), retried.AssignmentEpoch, "positive control: the requeue must bump the epoch")
}

// Route B over the REAL message loop rather than the exported shim. Three
// fixture details are load-bearing:
//   - Auto-enroll upserts by hostname and returns the EXISTING row's id, so
//     seeding the task against seedTaskAndTwoWorkers' w1 hostname (<prefix>-w1)
//     makes this connection resolve to the very worker the task is assigned to.
//     The assertion on the register response's worker_id is what proves that
//     happened; drop it and the test proves nothing.
//   - Both tasks must be reported in RunningTasks. finishRegister runs
//     reconcileRunningTasks, which requeues any task the coordinator has
//     assigned to this worker but the agent did not report, and a requeue bumps
//     assignment_epoch - which would make the messages below stale for a reason
//     that has nothing to do with what is under test.
//   - The barrier task exists because CloseSend does not drain queued messages
//     (mockConnectStream.Recv selects on the close channel alongside the queue),
//     so the test cannot wait for the stream to end. The recv loop handles
//     messages strictly in order, so once the barrier chunk is stored the FAILED
//     ahead of it has already been processed. The barrier is on a SEPARATE task
//     so it stays observable whether or not the FAILED was rejected.
func TestConnect_ASecondTerminalOverTheRealMessageLoopDoesNotResurrectTheTask(t *testing.T) {
	fx := newWorkerTestFixture(t)
	fx.Handler.AllowAutoEnroll = true
	ctx := context.Background()
	q := fx.Q

	const prefix = "status9"
	const hostname = prefix + "-w1"
	jobID, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, prefix, 1)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	taskIDStr := fx.Handler.UUIDStringForTest(taskID)

	barrier, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: jobID, Name: "t-barrier", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	claimedBarrier, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: barrier.ID, WorkerID: w1,
	})
	require.NoError(t, err)
	barrierStr := fx.Handler.UUIDStringForTest(barrier.ID)

	stream := newMockConnectStream(t)
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: hostname,
				CpuCores: 1, RamGb: 1, Os: "linux",
				RunningTasks: []*relayv1.RunningTask{
					{TaskId: taskIDStr, Epoch: int64(claimed.AssignmentEpoch)},
					{TaskId: barrierStr, Epoch: int64(claimedBarrier.AssignmentEpoch)},
				},
			},
		},
	})
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskStatus{
			TaskStatus: &relayv1.TaskStatusUpdate{
				TaskId: taskIDStr,
				Status: relayv1.TaskStatus_TASK_STATUS_DONE,
				Epoch:  int64(claimed.AssignmentEpoch),
			},
		},
	})
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskStatus{
			TaskStatus: &relayv1.TaskStatusUpdate{
				TaskId: taskIDStr,
				Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
				Epoch:  int64(claimed.AssignmentEpoch),
			},
		},
	})
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskLog{
			TaskLog: &relayv1.TaskLogChunk{
				TaskId: barrierStr, Content: []byte("barrier\n"),
				Epoch: int64(claimedBarrier.AssignmentEpoch),
			},
		},
	})

	done := make(chan error, 1)
	go func() { done <- fx.Handler.Connect(stream) }()

	resp := stream.RecvFromServer(t, 5*time.Second).GetRegisterResponse()
	require.NotNil(t, resp)
	require.Equal(t, fx.Handler.UUIDStringForTest(w1), resp.WorkerId,
		"the connection must resolve to the task's assignee, or this test proves nothing")

	require.Eventually(t, func() bool {
		rows, err := q.GetTaskLogs(ctx, barrier.ID)
		return err == nil && len(rows) == 1
	}, 10*time.Second, 20*time.Millisecond,
		"the barrier chunk must be stored, which proves the FAILED ahead of it was processed")

	fresh, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "done", fresh.Status, "a second terminal over the real loop must not resurrect the task")
	assert.Equal(t, int32(0), fresh.RetryCount, "a second terminal over the real loop must not burn a retry")
	assert.Equal(t, int32(1), fresh.AssignmentEpoch, "a rejected retry must not end the assignment")
	assert.Equal(t, w1, fresh.WorkerID, "a terminal transition must not release the assignee")

	stream.CloseSend()
	<-done
}
