//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runningAt claims a task at `at` and then moves it to 'running' through the
// production status writer, so the row carries a real assignee and a real epoch.
func runningAt(t *testing.T, f *assignedFixture, name string, at time.Time) store.Task {
	t.Helper()
	task := f.claimedAt(t, name, at)
	got, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID:              task.ID,
		Status:          "running",
		WorkerID:        f.w.ID,
		AssignmentEpoch: task.AssignmentEpoch,
		StartedAt:       pgtype.Timestamptz{Time: at.Add(time.Minute), Valid: true},
	})
	require.NoError(t, err)
	return got
}

func listPage(t *testing.T, f *assignedFixture, limit int32) []store.Task {
	t.Helper()
	rows, err := f.q.ListActiveTasksForWorkerPage(f.ctx, store.ListActiveTasksForWorkerPageParams{
		WorkerID:  f.w.ID,
		PageLimit: limit,
	})
	require.NoError(t, err)
	return rows
}

// TestListActiveTasksForWorkerPage_ReturnsEveryAssignedStatus is the positive arm
// of the status allow-list. Dropping any member from the IN list must go RED:
// that exact mutation stayed green across four suites for RequeueTaskByID.
func TestListActiveTasksForWorkerPage_ReturnsEveryAssignedStatus(t *testing.T) {
	f := newAssignedFixture(t)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	dispatched := f.claimedAt(t, "queued-to-rig", base)
	preparing := preparingAt(t, f, "syncing", base.Add(time.Minute))
	running := runningAt(t, f, "rendering", base.Add(2*time.Minute))

	rows := listPage(t, f, 50)
	require.Len(t, rows, 3, "dispatched, preparing and running are all 'currently assigned'")
	got := map[string]string{}
	for _, r := range rows {
		got[r.Name] = r.Status
	}
	assert.Equal(t, "dispatched", got[dispatched.Name])
	assert.Equal(t, "preparing", got[preparing.Name])
	assert.Equal(t, "running", got[running.Name])
}

// Terminal rows are outside the partition: a finished task holds no slot.
func TestListActiveTasksForWorkerPage_ExcludesTerminalRows(t *testing.T) {
	f := newAssignedFixture(t)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	f.claimedAt(t, "live", base)
	done := f.claimedAt(t, "finished", base.Add(time.Minute))
	_, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID:              done.ID,
		Status:          "done",
		WorkerID:        f.w.ID,
		AssignmentEpoch: done.AssignmentEpoch,
		FinishedAt:      pgtype.Timestamptz{Time: base.Add(2 * time.Minute), Valid: true},
	})
	require.NoError(t, err)

	rows := listPage(t, f, 50)
	require.Len(t, rows, 1)
	assert.Equal(t, "live", rows[0].Name)
}

// assigned_at DESC NULLS LAST, id DESC. started_at would bury every dispatched
// row in a NULL bucket, and those are the rows the panel exists to show.
func TestListActiveTasksForWorkerPage_OrdersByAssignedAtDesc(t *testing.T) {
	f := newAssignedFixture(t)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	f.claimedAt(t, "oldest", base)
	f.claimedAt(t, "middle", base.Add(time.Hour))
	f.claimedAt(t, "newest", base.Add(2*time.Hour))

	rows := listPage(t, f, 50)
	require.Len(t, rows, 3)
	assert.Equal(t, []string{"newest", "middle", "oldest"},
		[]string{rows[0].Name, rows[1].Name, rows[2].Name})
}

// TestCountActiveTasksForWorker_MatchesTheListStatement pins the two predicates
// together: the count feeds the Slots KPI while the list feeds the table, and a
// disagreement between them shows an operator a fraction the rows contradict.
func TestCountActiveTasksForWorker_MatchesTheListStatement(t *testing.T) {
	f := newAssignedFixture(t)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	f.claimedAt(t, "a", base)
	runningAt(t, f, "b", base.Add(time.Minute))
	preparingAt(t, f, "d", base.Add(90*time.Second))
	done := f.claimedAt(t, "c", base.Add(2*time.Minute))
	_, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID:              done.ID,
		Status:          "failed",
		WorkerID:        f.w.ID,
		AssignmentEpoch: done.AssignmentEpoch,
		FinishedAt:      pgtype.Timestamptz{Time: base.Add(3 * time.Minute), Valid: true},
	})
	require.NoError(t, err)

	n, err := f.q.CountActiveTasksForWorker(f.ctx, f.w.ID)
	require.NoError(t, err)
	rows := listPage(t, f, 50)
	assert.EqualValues(t, len(rows), n, "count and list must slice the same partition")
	assert.EqualValues(t, 3, n)
}

// The job-name lookup the handler uses instead of a JOIN, so no hand-written
// store.Task copy exists to drift as `tasks` gains columns.
func TestGetJobNamesByIDs_ReturnsNamesForTheGivenIDs(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()
	user := newTestUser(t, q, false)

	one, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "nightly-render", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	two, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "smoke", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)

	rows, err := q.GetJobNamesByIDs(ctx, []pgtype.UUID{one.ID, two.ID})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// pgtype.UUID is a comparable struct ([16]byte plus a bool), so it is a
	// valid map key and the assertion is positional rather than order-dependent.
	nameByID := map[pgtype.UUID]string{}
	for _, r := range rows {
		nameByID[r.ID] = r.Name
	}
	assert.Equal(t, "nightly-render", nameByID[one.ID])
	assert.Equal(t, "smoke", nameByID[two.ID])
}
