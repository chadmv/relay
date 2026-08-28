//go:build integration

package store_test

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIncrementTaskRetryCount_BudgetPredicate_AnExhaustedTaskMovesZeroRows is the
// backlog item's own repro for half B, at the layer where it is reachable.
//
// THE POSITIVE CONTROL IS THE FIRST LEG AND IT IS ALSO THE SETUP, which is why
// this is one test and not two: the only way to reach retry_count == retries
// through production statements is to spend the budget, so the successful retry
// has to happen first anyway. Doing it deliberately also satisfies this file's
// standing convention - a suite where the statement stopped working at all must
// not be able to look like a suite of successful rejections.
//
// EVERY OTHER PREDICATE PASSES ON THE SECOND CALL. The task is freshly
// re-claimed, so the epoch is current, the worker is the assignee and the status
// is 'dispatched'. Only the budget predicate can reject it, which is what makes
// this test discriminating rather than merely red.
func TestIncrementTaskRetryCount_BudgetPredicate_AnExhaustedTaskMovesZeroRows(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Bud", "budget@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "budget-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "bw1", Hostname: "budget-w1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t-budget", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Retries: 1,
	})
	require.NoError(t, err)

	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// LEG 1 - POSITIVE CONTROL. The task's one retry, spent legitimately by its
	// own assignee at the current epoch. Asserted in full: this is also the
	// backlog item's "a task with a normal budget still retries exactly as many
	// times as configured" at the statement layer.
	first, err := q.IncrementTaskRetryCount(ctx, store.IncrementTaskRetryCountParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: w.ID,
	})
	require.NoError(t, err, "the assignee's own retry at the current epoch, inside budget, must succeed")
	assert.Equal(t, int32(1), first.RetryCount, "the retry burns exactly one")
	assert.Equal(t, "pending", first.Status, "the retry returns the task to the queue")
	assert.False(t, first.WorkerID.Valid, "the retry releases the assignee")
	assert.Equal(t, int32(2), first.AssignmentEpoch, "the retry ends the generation")

	// The dispatcher hands it out again. The budget is now SPENT.
	reclaimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), reclaimed.AssignmentEpoch, "fixture: claim, retry, claim moves the epoch 1 -> 2 -> 3")
	require.Equal(t, "dispatched", reclaimed.Status, "fixture: the row must be non-terminal, or terminality would reject too")
	require.Equal(t, int32(1), reclaimed.RetryCount, "fixture: retry_count must equal retries, or nothing here is at budget")
	require.Equal(t, int32(1), reclaimed.Retries)

	// LEG 2 - THE SUBJECT. Correct id, correct epoch, correct assignee,
	// non-terminal status. At HEAD this SUCCEEDS and leaves retry_count = 2.
	_, err = q.IncrementTaskRetryCount(ctx, store.IncrementTaskRetryCountParams{
		ID: reclaimed.ID, AssignmentEpoch: reclaimed.AssignmentEpoch, WorkerID: w.ID,
	})
	assert.ErrorIs(t, err, pgx.ErrNoRows,
		"a task at retry_count == retries has no retry left, and the STATEMENT must say so - the budget "+
			"was the one part of the caller's decision that lived only in Go, so a second caller could "+
			"take a task past its budget by omission")

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	// Non-fatal asserts so a RED run reports every part of the exposure rather
	// than stopping at the first one.
	assert.Equal(t, int32(1), after.RetryCount, "a rejected retry must not burn a retry it does not have")
	assert.Equal(t, "dispatched", after.Status, "a rejected retry must not return the task to the queue")
	assert.Equal(t, int32(3), after.AssignmentEpoch, "a rejected retry must not end the assignment")
	require.True(t, after.WorkerID.Valid)
	assert.Equal(t, w.ID.Bytes, after.WorkerID.Bytes, "a rejected retry must not clear the assignee")
}
