//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"relay/internal/store"
)

// TestRequeueTaskByID_DoesNotTearOffAFreshAssignment is the repro from
// bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence, at the layer
// where the bug actually lives.
//
// Two overlapping registrations B and C of the same worker W1 both read
// {task: epoch 1} from GetActiveTasksForWorker. B requeues, the dispatcher hands
// the task to W2, and then C writes - still walking its OWN snapshot. Before the
// fence, C's write matched on nothing but the id and a status allow-list that
// admits 'dispatched', so it tore a task off a worker that had only just been
// given it, bumped the epoch, and left W2's subprocess running with every
// subsequent message it sent fenced out in silence.
//
// The handler is not involved: it contributes no predicate C could fail. What
// makes C distinguishable from B is the two values it carries, and that is a
// property of the statement.
func TestRequeueTaskByID_DoesNotTearOffAFreshAssignment(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Rex", "rex@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "rqid-fence", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)

	w1, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "rqid-fence-1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w2, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w2", Hostname: "rqid-fence-2", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// The state both B and C read: assigned to W1 at epoch 1.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// B requeues first. This is the LEGITIMATE call and it must succeed.
	require.NoError(t, q.RequeueTaskByID(ctx, task.ID))

	// The dispatcher claims it for a DIFFERENT worker.
	redispatched, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w2.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), redispatched.AssignmentEpoch)

	// C writes, still holding epoch 1 and worker W1. It must move nothing.
	require.NoError(t, q.RequeueTaskByID(ctx, task.ID))

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status,
		"a stale reconcile must not requeue a task that has been re-dispatched")
	require.True(t, after.WorkerID.Valid, "the fresh assignment must survive")
	assert.Equal(t, w2.ID.Bytes, after.WorkerID.Bytes,
		"the task must still belong to the worker it was re-dispatched to")
	assert.Equal(t, int32(3), after.AssignmentEpoch,
		"a rejected requeue must not bump the epoch")
}
