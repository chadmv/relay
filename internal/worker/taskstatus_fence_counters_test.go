package worker

import (
	"reflect"
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
