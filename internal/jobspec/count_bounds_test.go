package jobspec

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// argvN returns n runnable one-element argv slices.
//
// `["true"]` is deliberate: internal/agent/runner.go emits its per-command step
// marker only for commands it actually EXECUTES - the loop breaks on an empty
// argv and on a failed Start or Wait - so the cheapest entry that costs anything
// is a runnable one. That is the shape all three bounds' arguments are written
// against, and it is why an unrunnable `["a"]` filler would misrepresent them.
func argvN(n int) [][]string {
	out := make([][]string, n)
	for i := range out {
		out[i] = []string{"true"}
	}
	return out
}

// nTaskSpec builds a valid spec of n tasks, each with a unique name and exactly
// ONE command. It exercises the task-count axis alone: n commands in total, one
// per task, so for every n this file uses neither command bound is in play and a
// failure can only have come from the task count.
func nTaskSpec(n int) *JobSpec {
	tasks := make([]TaskSpec, n)
	for i := range tasks {
		tasks[i] = TaskSpec{Name: "t" + strconv.Itoa(i), Command: []string{"true"}}
	}
	return &JobSpec{Name: "counts", Tasks: tasks}
}

// TestValidate_TheTaskCountIsBoundedAtBothEnds pins the upper end of the range
// whose lower end is "at least one task is required". RED at HEAD: Validate reads
// len(spec.Tasks) only against zero, so the 5001-task spec is accepted.
//
// THE NUMBERS ARE LITERALS, NOT maxTasksPerJob, ON PURPOSE. A test that spells
// the constant agrees with the implementation by construction and cannot detect
// the constant moving - which is the single most likely defect in a change that
// is three constants and three comparisons.
func TestValidate_TheTaskCountIsBoundedAtBothEnds(t *testing.T) {
	t.Run("one over the cap is rejected and the message reports the count", func(t *testing.T) {
		require.EqualError(t, Validate(nTaskSpec(5001)),
			"at most 5000 tasks are allowed, got 5001",
			"the message must STATE the limit and REPORT what arrived: a caller who generated the "+
				"spec has to know by how much to chunk it, and a limit-only message tells them nothing "+
				"about their own input")
	})

	t.Run("exactly at the cap is accepted", func(t *testing.T) {
		require.NoError(t, Validate(nTaskSpec(5000)),
			"a spec AT the boundary must still be accepted - this is the leg an off-by-one written "+
				"as >= breaks, and nothing else in the tree catches it")
	})

	t.Run("the count is refused before the per-task loop runs", func(t *testing.T) {
		// A 5001-task spec whose first two tasks share a name. In the current
		// placement the job-level count wins; a count check moved into or below the
		// per-task loop reports "duplicate task name: t0" instead.
		//
		// THE PRECEDENCE IS THE INSTRUMENT, NOT THE POINT. The point is that the
		// spec is refused BEFORE nameSet is allocated at len(spec.Tasks) capacity
		// and before 5001 normalizeTaskCommands calls - which is work this bound
		// exists to avoid doing, and the only observable trace of "before" is which
		// message comes back.
		spec := nTaskSpec(5001)
		spec.Tasks[1].Name = spec.Tasks[0].Name
		require.EqualError(t, Validate(spec), "at most 5000 tasks are allowed, got 5001")
	})
}
