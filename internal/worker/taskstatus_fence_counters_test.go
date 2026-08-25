package worker

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

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
