package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// TestTaskStatusFenceReasonsAreADenseRunFromZero pins the property the counter
// array depends on. UNLIKE logKind, THESE START AT 0: the array is sized
// [fenceReasonCount] and indexed directly, so a run starting at 1 would put the
// last reason one past the end. record fails closed rather than panicking (a
// panic on the recv goroutine kills the process), so getting this wrong is a
// SILENT loss of that reason's counts - the exact defect this slice closes.
func TestTaskStatusFenceReasonsAreADenseRunFromZero(t *testing.T) {
	run := []taskStatusFenceReason{
		fenceReasonRaced,
		fenceReasonDuplicate,
		fenceReasonConflicting,
	}
	for i, r := range run {
		require.Equal(t, taskStatusFenceReason(i), r,
			"reason #%d is %d. The reasons index the counter array directly, so they must stay a DENSE "+
				"RUN starting at 0.", i, r)
	}
	require.Equal(t, taskStatusFenceReason(len(run)), fenceReasonCount,
		"this test's `run` list has %d entries and fenceReasonCount is %d. IF YOU JUST ADDED A REASON, "+
			"ADD IT TO `run` ABOVE - this compares the hardcoded list's length to the sentinel, so a "+
			"correctly-added reason fails here first. OTHERWISE fenceReasonCount is no longer the length "+
			"of the counter array and a reason at or beyond it is never counted.",
		len(run), int(fenceReasonCount))
}

// TestTaskStatusFenceCounters_EveryReasonIsPublishedDistinctly drives every
// reason a DIFFERENT number of times and requires the published struct to carry
// exactly those numbers IN ORDER.
//
// ORDERED, NOT ElementsMatch, and for the measured reason recorded in
// TestIngestLogCounters_EveryKindIsPublishedDistinctly: an order-insensitive
// match leaves a crossed pair of assignments in snapshot() green.
func TestTaskStatusFenceCounters_EveryReasonIsPublishedDistinctly(t *testing.T) {
	var c statusFenceCounters

	var want []uint64
	n := uint64(1)
	for r := taskStatusFenceReason(0); r < fenceReasonCount; r++ {
		for i := uint64(0); i < n; i++ {
			c.record(r)
		}
		want = append(want, n)
		n++
	}

	got := c.snapshot()
	rv := reflect.ValueOf(got)
	require.Equal(t, len(want), rv.NumField(),
		"there are %d reasons and %d published fields. A reason with no field is counted into a cell "+
			"nobody reads - which is slice 2's defect, one package smaller.", len(want), rv.NumField())
	fields := make([]uint64, 0, rv.NumField())
	for i := 0; i < rv.NumField(); i++ {
		fields = append(fields, rv.Field(i).Uint())
	}
	require.Equal(t, want, fields,
		"every reason must publish its OWN cell, IN ORDER: field i of TaskStatusFenceCounts must read "+
			"reason i. A missing value means a reason is counted but never published; a permutation "+
			"means two fields are crossed.")
}

// TestTaskStatusFenceCounters_AnOutOfRangeReasonIsDroppedNotPanicked. The bounds
// check exists because the alternative on the gRPC recv goroutine is a panic
// that kills the process. It is unreachable while the dense-run test is green,
// and this exists so "unreachable" does not mean "untested".
func TestTaskStatusFenceCounters_AnOutOfRangeReasonIsDroppedNotPanicked(t *testing.T) {
	var c statusFenceCounters
	require.NotPanics(t, func() {
		c.record(fenceReasonCount)
		c.record(taskStatusFenceReason(200))
	})
	require.Equal(t, TaskStatusFenceCounts{}, c.snapshot(),
		"an out-of-range reason must be DROPPED, not folded into some other cell")
}

// TestTaskStatusFenceRejections_TwoHandlersDoNotShareCounts pins the HOME. A
// package-level var would make every exact-count assertion in this package
// order-dependent on every other test in the binary; production has exactly one
// Handler, so a value field IS process-wide there.
func TestTaskStatusFenceRejections_TwoHandlersDoNotShareCounts(t *testing.T) {
	var a, b Handler
	a.statusFence.record(fenceReasonDuplicate)

	require.Equal(t, uint64(1), a.TaskStatusFenceRejections().Duplicate)
	require.Equal(t, TaskStatusFenceCounts{}, b.TaskStatusFenceRejections(),
		"counters are per Handler, not per package")
}

// TestTaskStatusWritableSetMatchesTheSQLAllowList reads the allow-list out of
// internal/store/query/tasks.sql and requires the Go mirror to be exactly it,
// for BOTH statements handleTaskStatus writes through.
//
// WHY A GUARD AND NOT JUST A COMMENT. taskStatusIsWritable restates a set that
// lives in SQL, and this repo's recorded lesson is that a hand-written copy
// needs something comparing it to its source. The parse is deliberately narrow:
// it slices the file between one `-- name: X` header and the next, DROPS EVERY
// `--` COMMENT LINE, then reads the single `status IN (...)` clause left in the
// executable text, so a predicate added to a DIFFERENT statement cannot satisfy
// it.
//
// THE COMMENT STRIP IS LOAD-BEARING AND WAS FOUND BY RUNNING IT, not assumed:
// IncrementTaskRetryCount's own doc block quotes RetryJobTasks' allow-list
// (`status IN ('failed','timed_out')`, tasks.sql, in the paragraph that
// separates the two statements), so a parse over the raw block sees TWO clauses
// and reads a set that is not a predicate at all. A quoted allow-list in prose
// is exactly the thing this guard must not mistake for the statement's own.
//
// STATE THE STAKE HONESTLY, because it is lower than every other status
// allow-list in this tree and a reader who assumes otherwise will over-react to
// a failure here: this set gates NOTHING. It labels a counter. Drift mislabels a
// number; it cannot admit a write. That is exactly why the guard is cheap enough
// to be worth having.
func TestTaskStatusWritableSetMatchesTheSQLAllowList(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "store", "query", "tasks.sql"))
	require.NoError(t, err)
	sql := string(src)

	clause := regexp.MustCompile(`status IN \(([^)]*)\)`)
	quoted := regexp.MustCompile(`'([a-z_]+)'`)

	for _, stmt := range []string{"UpdateTaskStatus", "IncrementTaskRetryCount"} {
		start := strings.Index(sql, "-- name: "+stmt+" ")
		require.GreaterOrEqual(t, start, 0,
			"tasks.sql no longer declares %s. handleTaskStatus writes through it, so either it was "+
				"renamed (update this list) or the write path changed (re-derive this whole guard).", stmt)
		end := strings.Index(sql[start+1:], "-- name: ")
		require.GreaterOrEqual(t, end, 0, "%s is the last statement in tasks.sql; this parse needs a terminator", stmt)
		body := sql[start : start+1+end]

		// Executable text only. See the comment-strip paragraph above.
		var stripped []string
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			stripped = append(stripped, line)
		}
		body = strings.Join(stripped, "\n")

		found := clause.FindAllStringSubmatch(body, -1)
		require.Len(t, found, 1,
			"%s carries %d `status IN (...)` clauses in its EXECUTABLE text. This guard reads exactly "+
				"one; if the statement now has two, decide which one taskStatusIsWritable mirrors and "+
				"say so here.", stmt, len(found))

		var want []string
		for _, m := range quoted.FindAllStringSubmatch(found[0][1], -1) {
			want = append(want, m[1])
		}
		require.NotEmpty(t, want, "parsed no statuses out of %s's allow-list; the parse is broken, not the code", stmt)

		for _, s := range want {
			require.True(t, taskStatusIsWritable(s),
				"tasks.sql's %s admits status %q and taskStatusIsWritable says it is NOT writable. The "+
					"two have drifted: a rejection for a %q row would now be labelled `duplicate` or "+
					"`conflicting` when it is in fact a `raced`. Add it.", stmt, s, s)
		}
		for _, s := range []string{"done", "failed", "timed_out"} {
			require.False(t, taskStatusIsWritable(s),
				"%q is not in %s's allow-list but taskStatusIsWritable says it is writable. Every "+
					"terminality rejection would then be labelled `raced` and conflicting_total would "+
					"read zero forever - the actionable key silenced.", s, stmt)
		}
	}
}

// TestClassifyStatusFenceRejection is the classifier's own truth table, with the
// watchdog case named because it is the reason this slice exists.
func TestClassifyStatusFenceRejection(t *testing.T) {
	tests := []struct {
		name          string
		row, reported string
		want          taskStatusFenceReason
	}{
		{"still writable at T0 is a race", "running", "done", fenceReasonRaced},
		{"dispatched is writable too", "dispatched", "running", fenceReasonRaced},
		{"pending is writable too", "pending", "failed", fenceReasonRaced},
		{"the agent repeats its own terminal", "done", "done", fenceReasonDuplicate},
		{"a repeated failure", "failed", "failed", fenceReasonDuplicate},
		{"watchdog swept it and the agent reports success", "timed_out", "done", fenceReasonConflicting},
		{"watchdog swept it and the agent is still heartbeating", "timed_out", "running", fenceReasonConflicting},
		{"the agent contradicts its own outcome", "done", "failed", fenceReasonConflicting},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, classifyStatusFenceRejection(tc.row, tc.reported))
		})
	}
}

// stubStatusDB is the narrowest store.DBTX that drives handleTaskStatus's write
// path WITHOUT Postgres, which is what puts this proof in the lane CI actually
// runs (go-ci: `go test -race ./...`, no tag, no container).
//
// IT DISPATCHES ON THE STATEMENT'S OWN `-- name:` HEADER, which sqlc emits as
// the first line of every generated SQL constant. That is a property of the
// generated code rather than of a hand-copied SQL fragment, so a reformatted
// statement cannot silently re-route a call.
//
// Unlike handleTaskLog, this handler is MORE than one statement - GetTask, then
// one of two writes, then (on the success path) FailDependentTasks,
// RecomputeJobStatus and NotifyTaskCompleted - so Exec and Query return benign
// values instead of panicking. calls records what was actually reached, which is
// how the success leg establishes acceptance POSITIVELY rather than through a
// projection every other arm also produces.
type stubStatusDB struct {
	task     store.Task // what GetTask returns
	writeErr error      // what the retry/update statement returns
	execErr  error

	// mu protects calls ONLY. It is fixture bookkeeping: task, writeErr and
	// execErr are written once before any goroutine starts and are read-only
	// afterwards, so the subject under test acquires nothing this stub owns and
	// the no-new-lock constraint on the recv goroutine is not violated by the
	// production path. Without it the concurrency test below races on the
	// FIXTURE rather than on the subject, which would make its -race half prove
	// the wrong thing.
	mu    sync.Mutex
	calls []string
}

// callsSnapshot is how the single-threaded tests read `calls`. Reading the slice
// directly would be a race the concurrency test can see, and -race reporting the
// fixture is indistinguishable in the output from -race reporting the counters.
func (d *stubStatusDB) callsSnapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func (d *stubStatusDB) note(sql string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, name := range []string{
		"GetTask", "UpdateTaskStatus", "IncrementTaskRetryCount",
		"FailDependentTasks", "RecomputeJobStatus", "NotifyTaskCompleted", "NotifyTaskSubmitted",
	} {
		if strings.Contains(sql, "-- name: "+name+" ") {
			d.calls = append(d.calls, name)
			return name
		}
	}
	d.calls = append(d.calls, "UNKNOWN")
	return "UNKNOWN"
}

func (d *stubStatusDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	d.note(sql)
	return pgconn.CommandTag{}, d.execErr
}

func (d *stubStatusDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.note(sql)
	return nil, errors.New("stubStatusDB: handleTaskStatus performs no multi-row Query")
}

func (d *stubStatusDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch d.note(sql) {
	case "GetTask":
		return stubTaskRow{task: d.task}
	case "UpdateTaskStatus", "IncrementTaskRetryCount":
		return stubTaskRow{task: d.task, err: d.writeErr}
	case "RecomputeJobStatus":
		return stubStringRow{s: "running"}
	}
	return stubTaskRow{err: errors.New("stubStatusDB: unexpected QueryRow")}
}

// stubTaskRow fills a store.Task BY POSITION, and the positional copy is safe
// for a checked reason rather than by luck: sqlc scans a `SELECT *` row in
// MODEL FIELD ORDER (internal/store/tasks.sql.go: &i.ID, &i.JobID, ... matches
// store.Task's declaration exactly), so reflecting over the value gives the same
// order the generated Scan asks for. The arity assertion is what makes that a
// checked claim: a regenerated model with a new column fails here loudly instead
// of silently shifting every field by one.
type stubTaskRow struct {
	task store.Task
	err  error
}

func (r stubTaskRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	rv := reflect.ValueOf(r.task)
	if len(dest) != rv.NumField() {
		return fmt.Errorf("stubTaskRow: generated Scan wants %d columns and store.Task has %d fields. "+
			"This stub copies by position because sqlc scans in model field order; that assumption no "+
			"longer holds, so re-derive it rather than padding the list", len(dest), rv.NumField())
	}
	for i, d := range dest {
		dv := reflect.ValueOf(d)
		if dv.Kind() != reflect.Pointer || dv.Elem().Type() != rv.Field(i).Type() {
			return fmt.Errorf("stubTaskRow: column %d is %T and store.Task field %d is %s - the scan "+
				"order and the field order have diverged", i, d, i, rv.Field(i).Type())
		}
		dv.Elem().Set(rv.Field(i))
	}
	return nil
}

type stubStringRow struct{ s string }

func (r stubStringRow) Scan(dest ...any) error {
	if len(dest) == 1 {
		if p, ok := dest[0].(*string); ok {
			*p = r.s
		}
	}
	return nil
}

func statusWorkerID() pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{9}, Valid: true} }

const statusTaskID = "3f1c0a2e-7b64-4d8a-9c31-0e5b6a7d8c90"

func statusTaskIDUUID(t *testing.T) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	require.NoError(t, u.Scan(statusTaskID))
	return u
}

// newStatusHandler wires a Handler over the stub with a task the connection's
// worker OWNS at the CURRENT epoch, so both Go-side gates pass and control
// really reaches the write. Any test that wants a gate to reject changes the
// fixture, never the handler.
func newStatusHandler(t *testing.T, rowStatus string, retries, retryCount int32, writeErr error) (*Handler, *stubStatusDB) {
	t.Helper()
	db := &stubStatusDB{
		task: store.Task{
			ID:              statusTaskIDUUID(t),
			JobID:           pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
			Status:          rowStatus,
			WorkerID:        statusWorkerID(),
			AssignmentEpoch: 7,
			Retries:         retries,
			RetryCount:      retryCount,
		},
		writeErr: writeErr,
	}
	return &Handler{q: store.New(db), broker: events.NewBroker()}, db
}

func statusUpdate(s relayv1.TaskStatus) *relayv1.TaskStatusUpdate {
	return &relayv1.TaskStatusUpdate{TaskId: statusTaskID, Status: s, Epoch: 7}
}

// TestHandleTaskStatus_TheUpdateArmCountsEachRejectionReasonAndASuccessCountsNothing
// is item 1's own Done-When at the UpdateTaskStatus arm: read the counters
// across each rejection AND across a success.
//
// EACH LEG IS ASSERTED IMMEDIATELY AFTER IT RUNS. A battery that only checks
// totals at the end cannot tell "the success incremented" from "the third
// rejection did not", and a poisoned input observed only at the end cannot
// detect an early-exit mutation.
func TestHandleTaskStatus_TheUpdateArmCountsEachRejectionReasonAndASuccessCountsNothing(t *testing.T) {
	ctx := context.Background()
	logged := captureUnitLog(t)

	// CONFLICTING FIRST, because it is the leg this slice exists for and a
	// poisoned input placed last cannot detect an early-exit mutation. The
	// watchdog stamped `timed_out`; the agent reports `done`.
	h, db := newStatusHandler(t, "timed_out", 0, 0, pgx.ErrNoRows)
	lim := newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Conflicting: 1}, h.TaskStatusFenceRejections(),
		"a task the coordinator marked timed_out whose agent reports done is the ACTIONABLE case: a "+
			"successful task recorded as a timeout. Before this number there was no runtime signal of "+
			"any kind for it.")
	require.Contains(t, db.callsSnapshot(), "UpdateTaskStatus", "fixture: control must reach the write")
	require.NotContains(t, db.callsSnapshot(), "FailDependentTasks",
		"fixture: a rejected write must return before any follow-on effect")

	// DUPLICATE: same row status as the report. The expected healthy floor.
	h, _ = newStatusHandler(t, "done", 0, 0, pgx.ErrNoRows)
	lim = newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Duplicate: 1}, h.TaskStatusFenceRejections(),
		"a duplicate terminal from a healthy assignee is an EXPECTED rejection and must be counted "+
			"under its own key, or the actionable number reads as constant alarm")

	// RACED: the row was still writable at T0, so something ended the generation
	// inside this handler's own window.
	h, _ = newStatusHandler(t, "running", 0, 0, pgx.ErrNoRows)
	lim = newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Raced: 1}, h.TaskStatusFenceRejections())

	// ACCUMULATION on ONE handler: an Add, never a Store.
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Raced: 2}, h.TaskStatusFenceRejections())

	// SUCCESS MUST NOT COUNT, on the SAME handler whose counter has already
	// moved: a counter that increments unconditionally passes a fresh-handler
	// check. Acceptance is established POSITIVELY, by the follow-on effect only
	// the accepted path produces.
	db2 := &stubStatusDB{task: store.Task{
		ID: statusTaskIDUUID(t), JobID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		Status: "running", WorkerID: statusWorkerID(), AssignmentEpoch: 7,
	}}
	h2 := &Handler{q: store.New(db2), broker: events.NewBroker()}
	h2.handleTaskStatus(ctx, statusWorkerID(), newIngestLogLimiter(&h2.ingestDrops),
		statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{}, h2.TaskStatusFenceRejections(),
		"an ACCEPTED status report must not be counted as a rejection. This number is what an operator "+
			"reads as 'reports are being discarded'; incrementing it on the happy path makes it noise.")
	require.Contains(t, db2.callsSnapshot(), "RecomputeJobStatus",
		"THE REPORT WAS ACCEPTED, and this is what says so. Without a positive marker the leg above "+
			"asserts a negative through a projection every other arm shares.")
	require.Contains(t, db2.callsSnapshot(), "NotifyTaskCompleted")

	require.Equal(t, "", logged(),
		"a fence rejection must emit NO log line of any wording, including a budgeted one: it is "+
			"caller-driven volume on the recv goroutine, firing on the legitimate duplicate-terminal case")
}

// TestHandleTaskStatus_TheRetryArmCountsItsOwnRejections. The retry branch is
// reached instead of the update when the report is terminal and a retry is
// left, so it needs its own fixture and its own leg.
func TestHandleTaskStatus_TheRetryArmCountsItsOwnRejections(t *testing.T) {
	ctx := context.Background()
	logged := captureUnitLog(t)

	// CONFLICTING FIRST again. The watchdog stamped timed_out; the agent reports
	// failed and still has a retry left.
	h, db := newStatusHandler(t, "timed_out", 3, 0, pgx.ErrNoRows)
	lim := newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	require.Contains(t, db.callsSnapshot(), "IncrementTaskRetryCount",
		"fixture: terminal + retries remaining must take the RETRY branch")
	require.NotContains(t, db.callsSnapshot(), "UpdateTaskStatus",
		"fixture: the retry branch returns; the two arms are mutually exclusive and no input executes both")
	require.Equal(t, TaskStatusFenceCounts{Conflicting: 1}, h.TaskStatusFenceRejections(),
		"the retry arm's rejections are the SAME noun as the update arm's - the agent's report of this "+
			"task's outcome was discarded - so they share the section and are split by REASON, not by "+
			"statement")

	// DUPLICATE at the retry arm.
	h, _ = newStatusHandler(t, "failed", 3, 0, pgx.ErrNoRows)
	lim = newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	require.Equal(t, TaskStatusFenceCounts{Duplicate: 1}, h.TaskStatusFenceRejections())

	// A SUCCESSFUL retry must not count, and must still wake the dispatcher.
	h, db = newStatusHandler(t, "running", 3, 0, nil)
	lim = newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	require.Equal(t, TaskStatusFenceCounts{}, h.TaskStatusFenceRejections())
	require.Contains(t, db.callsSnapshot(), "NotifyTaskSubmitted",
		"the accepted retry must still wake the dispatcher; this is the positive marker for this arm")

	require.Equal(t, "", logged(), "no arm of the retry branch logs")
}

// TestHandleTaskStatus_ARealDatabaseErrorIsNotAFenceRejection is the poisoned
// input in its own test, and it is what kills a record() written ABOVE the
// errors.Is check.
func TestHandleTaskStatus_ARealDatabaseErrorIsNotAFenceRejection(t *testing.T) {
	h, _ := newStatusHandler(t, "running", 0, 0,
		errors.New(`ERROR: could not serialize access due to concurrent update (SQLSTATE 40001)`))
	lim := newIngestLogLimiter(&h.ingestDrops)
	logged := captureUnitLog(t)

	h.handleTaskStatus(context.Background(), statusWorkerID(), lim,
		statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))

	require.Equal(t, TaskStatusFenceCounts{}, h.TaskStatusFenceRejections(),
		"a REAL database error is a different arm with a different meaning. A record() placed above the "+
			"errors.Is check counts every database error and makes the number unreadable: the whole "+
			"value of this section is that it means the FENCE refused something.")
	require.Contains(t, logged(), "handleTaskStatus UpdateTaskStatus",
		"fixture: the other arm still logs, so this test is exercising it rather than falling through")
}

// TestTaskStatusFenceCounters_ConcurrentRejectionsAreExact is what makes
// "atomics, not a mutex" a checked decision rather than a comment: in production
// every connection has its own recv goroutine and they all write these three
// words.
//
// EACH GOROUTINE HAS ITS OWN Handler-FREE FIXTURE? NO - deliberately the
// opposite. They share ONE Handler, because that is the production shape (one
// Handler per process, one limiter per connection) and it is the only
// arrangement in which the mutation this test exists for is observable.
//
// WHAT KILLS WHAT, and the halves are not equally strong. The mutation is
// statusFenceCounters.n changed from atomic.Uint64 to a plain uint64 with `++`,
// WITH the .Load() calls in snapshot dropped to match - leave them in and the
// "kill" is a compile error, which measures nothing. The -race half kills
// through happens-before analysis and does not need true parallelism; the
// exactness half only catches a lost update when two goroutines interleave
// inside the read-modify-write and is inert at GOMAXPROCS=1. Both are live in
// CI, which runs `go test -race ./...` on 2-4 vCPUs.
//
// MEASURED FIGURES ARE FILLED IN BY THIS SLICE'S MUTATION MATRIX RUN (M6) and
// are recorded below rather than asserted from theory. Until that run lands this
// paragraph is deliberately empty; do not populate it from reasoning.
func TestTaskStatusFenceCounters_ConcurrentRejectionsAreExact(t *testing.T) {
	h, _ := newStatusHandler(t, "timed_out", 0, 0, pgx.ErrNoRows)
	const goroutines, each = 8, 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One limiter per goroutine, exactly as there is one per connection.
			lim := newIngestLogLimiter(&h.ingestDrops)
			for i := 0; i < each; i++ {
				h.handleTaskStatus(context.Background(), statusWorkerID(), lim,
					statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
			}
		}()
	}
	wg.Wait()

	require.Equal(t, TaskStatusFenceCounts{Conflicting: goroutines * each}, h.TaskStatusFenceRejections(),
		"every rejection from every connection must land, and all of them under ONE reason. A plain "+
			"uint64 here loses counts silently and is a data race -race can see.")
}
