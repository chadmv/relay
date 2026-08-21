//go:build integration

package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"relay/internal/store"
)

// TestRequeueTask_DoesNotTearOffAFreshAssignment is the SECOND instance of the
// shape bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence describes.
// The item claims RequeueTaskByID is "the only requeue statement in the tree
// whose WHERE clause names nothing but the task id and a status allow-list".
// THAT CLAIM IS FALSE: RequeueTask, three statements away in the same file, had
// exactly the same WHERE.
//
// Its trigger CORRELATES WITH THE HAZARD, which makes it at least as reachable
// as its sibling. Its only production caller is the dispatcher's send-failure
// path (internal/scheduler/dispatch.go), reached precisely when the worker has
// disappeared or is wedged - and a disconnected worker is what arms its own
// grace timer. registry.Send is bounded by a 5s sendTimeout, so up to five
// seconds separate ClaimTaskForWorker returning `claimed` from RequeueTask
// firing on that now-stale snapshot:
//
//  1. Dispatcher claims T for W1: dispatched, epoch 1.
//  2. Send blocks and fails because W1 is gone.
//  3. W1's grace timer returns T to pending, epoch 2.
//  4. The dispatcher claims T for W2: dispatched, epoch 3.
//  5. The original goroutine calls RequeueTask on its epoch-1 snapshot. On the
//     id and 'dispatched' alone it MATCHED, tearing T off W2 mid-run.
//
// Same end state as the sibling bug: duplicate execution, no log line.
func TestRequeueTask_DoesNotTearOffAFreshAssignment(t *testing.T) {
	f := newRequeueFence(t)

	// The snapshot the dispatcher goroutine is holding while Send blocks.
	claimed := f.claimedBy(t, "rqt-repro", f.w1)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// The grace timer returns it to pending. This is the LEGITIMATE requeue and
	// it must succeed.
	require.NoError(t, f.q.RequeueTask(f.ctx, claimed.ID))

	// The dispatcher hands it to a DIFFERENT worker.
	redispatched, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: claimed.ID, WorkerID: f.w2.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), redispatched.AssignmentEpoch)

	// Send finally fails and the original goroutine requeues its stale snapshot.
	require.NoError(t, f.q.RequeueTask(f.ctx, claimed.ID))

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status,
		"a stale send-failure requeue must not requeue a re-dispatched task")
	require.True(t, after.WorkerID.Valid, "the fresh assignment must survive")
	assert.Equal(t, f.w2.ID.Bytes, after.WorkerID.Bytes,
		"the task must still belong to the worker it was re-dispatched to")
	assert.Equal(t, int32(3), after.AssignmentEpoch,
		"a rejected requeue must not bump the epoch")
}
