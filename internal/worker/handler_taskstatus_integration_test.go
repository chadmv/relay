//go:build integration

package worker_test

import (
	"context"
	"testing"

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
// This test also pins the Go NULL trap. It is the only case where both sides of
// the comparison can be zero-valued, which is the only case that discriminates a
// NULL-tolerant comparison from a NULL-rejecting one. Task 4 mutation-proves it.
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
