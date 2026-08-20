package scheduler

// Unit tests for the stale-task watchdog. No Docker, no database: the store and
// the canceller are narrow interfaces and the clock is injected, which is the
// whole reason they are interfaces.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWatchdogStore records every call and lets a test script the outcome of
// UpdateTaskStatus per task id.
type fakeWatchdogStore struct {
	mu sync.Mutex

	listCalls  int
	listParams []store.ListOverdueAssignedTasksParams
	overdue    []store.Task

	updates    []store.UpdateTaskStatusParams
	updateErr  map[pgtype.UUID]error
	cascaded   []pgtype.UUID
	recomputed []pgtype.UUID
	notifies   int

	events []string // append-only trace of "update:<id>" / "cancel:<id>"
}

func (f *fakeWatchdogStore) ListOverdueAssignedTasks(_ context.Context, p store.ListOverdueAssignedTasksParams) ([]store.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	f.listParams = append(f.listParams, p)
	return f.overdue, nil
}

func (f *fakeWatchdogStore) UpdateTaskStatus(_ context.Context, p store.UpdateTaskStatusParams) (store.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, p)
	f.events = append(f.events, "update:"+uuidStr(p.ID))
	if err, ok := f.updateErr[p.ID]; ok && err != nil {
		return store.Task{}, err
	}
	return store.Task{
		ID: p.ID, JobID: makeUUID(99), Status: p.Status, WorkerID: p.WorkerID,
		StartedAt: p.StartedAt, FinishedAt: p.FinishedAt, AssignmentEpoch: p.AssignmentEpoch,
	}, nil
}

func (f *fakeWatchdogStore) FailDependentTasks(_ context.Context, id pgtype.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cascaded = append(f.cascaded, id)
	return nil
}

func (f *fakeWatchdogStore) RecomputeJobStatus(_ context.Context, id pgtype.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recomputed = append(f.recomputed, id)
	return "failed", nil
}

func (f *fakeWatchdogStore) NotifyTaskCompleted(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notifies++
	return nil
}

func (f *fakeWatchdogStore) trace() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

// overdueRow builds an assigned task the scan would have returned.
func overdueRow(id byte, epoch int32, startedAt, assignedAt time.Time) store.Task {
	return store.Task{
		ID:              makeUUID(id),
		JobID:           makeUUID(99),
		Status:          "running",
		WorkerID:        makeUUID(200),
		AssignmentEpoch: epoch,
		StartedAt:       pgtype.Timestamptz{Time: startedAt, Valid: true},
		AssignedAt:      pgtype.Timestamptz{Time: assignedAt, Valid: true},
	}
}

type nopCanceller struct{}

func (nopCanceller) SendCancel(string, string, bool) error { return nil }

func newTestWatchdog(t *testing.T, q *fakeWatchdogStore, now time.Time) *Watchdog {
	t.Helper()
	w := NewWatchdog(q, nopCanceller{}, events.NewBroker(), 30*time.Minute, 24*time.Hour)
	w.now = func() time.Time { return now }
	return w
}

// TestWatchdog_WritesTimedOutWithTheRowsOwnFences is the headline write. The
// epoch and worker id must come from the row the scan returned: they are the
// TOCTOU guard between the scan and the write, and binding a zero value makes
// the statement match the wrong generation (epoch) or nothing at all (worker).
func TestWatchdog_WritesTimedOutWithTheRowsOwnFences(t *testing.T) {
	now := time.Now()
	started := now.Add(-2 * time.Hour)
	q := &fakeWatchdogStore{overdue: []store.Task{overdueRow(1, 7, started, now.Add(-3*time.Hour))}}
	w := newTestWatchdog(t, q, now)

	require.NoError(t, w.SweepOnce(context.Background()))

	require.Len(t, q.updates, 1)
	got := q.updates[0]
	assert.Equal(t, "timed_out", got.Status)
	assert.Equal(t, int32(7), got.AssignmentEpoch, "the write must bind the row's own epoch, never zero")
	assert.Equal(t, makeUUID(200), got.WorkerID, "the write must bind the row's own worker id, never a zero UUID")
	assert.Equal(t, started, got.StartedAt.Time, "started_at must be preserved unchanged")
	assert.Equal(t, now, got.FinishedAt.Time, "finished_at is the sweep's own clock")

	assert.Equal(t, []pgtype.UUID{makeUUID(1)}, q.cascaded)
	assert.Equal(t, []pgtype.UUID{makeUUID(99)}, q.recomputed)
	assert.Equal(t, 1, q.notifies, "a freed slot must wake the dispatchers")
}

// TestWatchdog_FenceRejectionIsASilentNoOp: pgx.ErrNoRows means somebody else got
// there first - the agent finished, a cancel landed, a grace expiry requeued, or
// a sibling replica swept it. Nothing downstream may run: a cascade on a write
// the database refused would fail the dependents of a task that is still live.
func TestWatchdog_FenceRejectionIsASilentNoOp(t *testing.T) {
	now := time.Now()
	row := overdueRow(1, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	q := &fakeWatchdogStore{
		overdue:   []store.Task{row},
		updateErr: map[pgtype.UUID]error{row.ID: pgx.ErrNoRows},
	}
	w := newTestWatchdog(t, q, now)

	require.NoError(t, w.SweepOnce(context.Background()))

	assert.Empty(t, q.cascaded, "a rejected write must not cascade")
	assert.Empty(t, q.recomputed, "a rejected write must not recompute the job")
	assert.Zero(t, q.notifies, "a rejected write must not wake the dispatcher")
}

// TestWatchdog_APoisonedFirstRowDoesNotStopTheSweep. THE POISONED ROW IS FIRST
// AND TWO HEALTHY ROWS FOLLOW IT, deliberately: with the bad row last, mutating
// the loop's `continue` to `break` is structurally undetectable.
func TestWatchdog_APoisonedFirstRowDoesNotStopTheSweep(t *testing.T) {
	now := time.Now()
	bad := overdueRow(1, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	good1 := overdueRow(2, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	good2 := overdueRow(3, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	q := &fakeWatchdogStore{
		overdue:   []store.Task{bad, good1, good2},
		updateErr: map[pgtype.UUID]error{bad.ID: errors.New("connection reset")},
	}
	w := newTestWatchdog(t, q, now)

	require.NoError(t, w.SweepOnce(context.Background()))

	require.Len(t, q.updates, 3, "every overdue row must be attempted; one bad row must not end the sweep")
	assert.Equal(t, []pgtype.UUID{good1.ID, good2.ID}, q.cascaded,
		"only the rows whose write actually matched may be finalized")
}

// TestWatchdog_BothArmsDisabledSkipsTheScanEntirely pins the decision that a
// fully disabled watchdog does not issue a guaranteed-empty query every tick.
func TestWatchdog_BothArmsDisabledSkipsTheScanEntirely(t *testing.T) {
	q := &fakeWatchdogStore{}
	w := NewWatchdog(q, nopCanceller{}, events.NewBroker(), 0, 0)

	require.NoError(t, w.SweepOnce(context.Background()))

	assert.Zero(t, q.listCalls, "with both arms off there is nothing to ask the database")
	assert.Empty(t, q.updates)
}

// TestWatchdog_ScanParametersDeriveFromTheConfiguredBounds proves the Go-side
// cutoffs are what reach the statement.
func TestWatchdog_ScanParametersDeriveFromTheConfiguredBounds(t *testing.T) {
	now := time.Now()
	q := &fakeWatchdogStore{}
	w := NewWatchdog(q, nopCanceller{}, events.NewBroker(), 90*time.Second, 6*time.Hour)
	w.now = func() time.Time { return now }

	require.NoError(t, w.SweepOnce(context.Background()))

	require.Len(t, q.listParams, 1)
	p := q.listParams[0]
	assert.True(t, p.ExecEnabled)
	assert.True(t, p.AbsoluteEnabled)
	assert.Equal(t, int64(90), p.MarginSeconds)
	assert.Equal(t, now.Add(-6*time.Hour), p.AbsoluteCutoff.Time,
		"the absolute cutoff must be an ABSOLUTE Go-computed instant, never NOW() - interval")
	assert.Equal(t, now, p.Now.Time)
}

// TestWatchdog_OneArmDisabledStillScansWithOnlyThatArmOff is the other half of
// BothArmsDisabledSkipsTheScanEntirely: the short-circuit must be an AND of both
// arms being off, not an OR, or configuring one arm off silently disables the
// whole watchdog.
func TestWatchdog_OneArmDisabledStillScansWithOnlyThatArmOff(t *testing.T) {
	now := time.Now()

	q := &fakeWatchdogStore{}
	w := NewWatchdog(q, nopCanceller{}, events.NewBroker(), 0, 24*time.Hour)
	w.now = func() time.Time { return now }
	require.NoError(t, w.SweepOnce(context.Background()))
	require.Len(t, q.listParams, 1, "a disabled execution arm must not disable the scan")
	assert.False(t, q.listParams[0].ExecEnabled)
	assert.True(t, q.listParams[0].AbsoluteEnabled)

	q = &fakeWatchdogStore{}
	w = NewWatchdog(q, nopCanceller{}, events.NewBroker(), 30*time.Minute, 0)
	w.now = func() time.Time { return now }
	require.NoError(t, w.SweepOnce(context.Background()))
	require.Len(t, q.listParams, 1, "a disabled absolute arm must not disable the scan")
	assert.True(t, q.listParams[0].ExecEnabled)
	assert.False(t, q.listParams[0].AbsoluteEnabled)
}

// recordingCanceller records each send in order and can block, so one test can
// assert ordering relative to the write and another can assert the fan-out is
// concurrent.
type recordingCanceller struct {
	mu    sync.Mutex
	store *fakeWatchdogStore
	block time.Duration
	sends []cancelRecord
}

type cancelRecord struct {
	workerID string
	taskID   string
	force    bool
}

func (c *recordingCanceller) SendCancel(workerID, taskID string, force bool) error {
	if c.block > 0 {
		time.Sleep(c.block)
	}
	if c.store != nil {
		c.store.mu.Lock()
		c.store.events = append(c.store.events, "cancel:"+taskID)
		c.store.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends = append(c.sends, cancelRecord{workerID, taskID, force})
	return nil
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

// TestWatchdog_CancelsAfterTheWriteAndOnlyForMatchedRows. Two properties in one
// test because they are the same property: the send is a CONSEQUENCE of the
// write having matched.
//
// force=false is the conservative arm and a genuine trade. force=true skips
// workspace finalize, which risks leaving a P4 workspace in a state that poisons
// warm-workspace scoring for every later task on that machine; force=false still
// closes cancelledCh, which is the escape that frees a log write parked on a
// full sendCh. It also matches handleDisableWorker, the other place the
// coordinator unilaterally takes tasks from a still-connected agent.
//
// THE REJECTED ROW IS FIRST, so a mutation that stops the sweep on the first
// fence rejection cannot hide behind the good row having already been handled.
func TestWatchdog_CancelsAfterTheWriteAndOnlyForMatchedRows(t *testing.T) {
	now := time.Now()
	rejected := overdueRow(1, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	swept := overdueRow(2, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	q := &fakeWatchdogStore{
		overdue:   []store.Task{rejected, swept},
		updateErr: map[pgtype.UUID]error{rejected.ID: pgx.ErrNoRows},
	}
	c := &recordingCanceller{store: q}
	w := NewWatchdog(q, c, events.NewBroker(), 30*time.Minute, 24*time.Hour)
	w.now = func() time.Time { return now }

	require.NoError(t, w.SweepOnce(context.Background()))

	require.Len(t, c.sends, 1,
		"a row the fence rejected belongs to somebody else now; cancelling it would tear a LIVE assignment off a worker")
	assert.Equal(t, uuidStr(swept.ID), c.sends[0].taskID)
	assert.Equal(t, uuidStr(swept.WorkerID), c.sends[0].workerID)
	assert.False(t, c.sends[0].force,
		"force=false: skipping workspace finalize can poison warm-workspace scoring for every later task")

	trace := q.trace()
	assert.Less(t, indexOf(trace, "update:"+uuidStr(swept.ID)), indexOf(trace, "cancel:"+uuidStr(swept.ID)),
		"the write must be durable before the agent is told to stop: sending first would leave an agent told to "+
			"abandon a task the coordinator still considers live")
}

// TestWatchdog_CancelFanOutIsConcurrent: N overdue tasks on ONE wedged worker
// must cost ~one send timeout, not N of them. Modelled on
// internal/api/cancel_signals_test.go:29-62.
func TestWatchdog_CancelFanOutIsConcurrent(t *testing.T) {
	const block = 200 * time.Millisecond
	const n = 5

	now := time.Now()
	rows := make([]store.Task, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, overdueRow(byte(10+i), 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour)))
	}
	q := &fakeWatchdogStore{overdue: rows}
	c := &recordingCanceller{block: block}
	w := NewWatchdog(q, c, events.NewBroker(), 30*time.Minute, 24*time.Hour)
	w.now = func() time.Time { return now }

	start := time.Now()
	require.NoError(t, w.SweepOnce(context.Background()))
	elapsed := time.Since(start)

	require.Len(t, c.sends, n)
	assert.Less(t, elapsed, (n-1)*block,
		"the cancel fan-out must be concurrent: a sequential loop over one wedged worker costs N send timeouts")
}
