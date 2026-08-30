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

// commandCountSpec builds a two-task spec whose OFFENDING task carries n
// commands, placed at index pos (0 or 1). The other task is healthy.
//
// IT IS NOT twoTaskSpec FROM jobspec_bounds_test.go AND MUST NOT BE REPLACED BY
// IT. That helper assigns bad.Command before returning, and a task carrying both
// Command and Commands is refused by normalizeTaskCommands with "set either
// command or commands, not both" - so every case here would go red at HEAD, green
// after the change, and be exercising the command-form rule rather than the
// bound. That is the "a test can be green because of the bug" shape, in its
// helper-reuse spelling.
func commandCountSpec(pos, n int) *JobSpec {
	bad := TaskSpec{Name: "bad-task", Commands: argvN(n)}
	healthy := TaskSpec{Name: "healthy-task", Command: []string{"echo", "y"}}
	tasks := []TaskSpec{bad, healthy}
	if pos == 1 {
		tasks = []TaskSpec{healthy, bad}
	}
	return &JobSpec{Name: "counts", Tasks: tasks}
}

// TestValidate_ThePerTaskCommandCountIsBounded is the concentration control's
// test: the bound on how much sequential work one request can pin to ONE worker
// slot. RED at HEAD: Validate never reads len(ts.Commands) except through
// normalizeTaskCommands' per-entry argv check.
func TestValidate_ThePerTaskCommandCountIsBounded(t *testing.T) {
	const overMsg = "task bad-task: at most 500 commands are allowed, got 501"

	t.Run("one over the cap, offender FIRST", func(t *testing.T) {
		// The offender is first and a healthy task follows it, so the refusal cannot
		// have been produced by "the last task lost" or by an early exit.
		require.EqualError(t, Validate(commandCountSpec(0, 501)), overMsg,
			"the message must NAME the offending task: a caller with a fifty-task spec has to be told "+
				"WHICH task to split")
	})

	t.Run("one over the cap, offender SECOND", func(t *testing.T) {
		// The mirror, and BOTH positions are needed. An offender at index 0 defeats
		// a loop body that never runs; an offender at index 1 defeats a loop body
		// guarded with `i == 0`. jobspec_bounds_test.go records that the second
		// mutant SURVIVED that entire file before its own index-1 cases existed, so
		// this is a measured hazard on this exact function, not a hypothetical.
		require.EqualError(t, Validate(commandCountSpec(1, 501)), overMsg,
			"the bound must apply to EVERY task, and the message must name the SECOND one")
	})

	t.Run("exactly at the cap is accepted", func(t *testing.T) {
		require.NoError(t, Validate(commandCountSpec(0, 500)),
			"a task AT the boundary must still be accepted - the leg a >= breaks")
	})

	t.Run("the per-task bound wins over the job-wide total", func(t *testing.T) {
		// ONE task with 25,001 commands violates BOTH command bounds. The per-task
		// check runs first, so the message must NAME THE TASK. Transpose the two
		// checks and this returns the job-level total message, which would accuse
		// the whole job for what is one task's fault - and would tell the caller to
		// shrink a spec that has only one thing wrong with it.
		require.EqualError(t, Validate(commandCountSpec(0, 25001)),
			"task bad-task: at most 500 commands are allowed, got 25001")
	})
}

// totalSpec builds a spec of nTasks tasks, each carrying perTask commands and a
// unique name.
func totalSpec(nTasks, perTask int) *JobSpec {
	tasks := make([]TaskSpec, nTasks)
	for i := range tasks {
		tasks[i] = TaskSpec{Name: "c" + strconv.Itoa(i), Commands: argvN(perTask)}
	}
	return &JobSpec{Name: "counts", Tasks: tasks}
}

// TestValidate_TheJobWideCommandTotalIsBounded is the case NEITHER per-axis cap
// can catch, and it is the entire argument for the third constant. Every task in
// every case below is inside maxCommandsPerTask and every task count is inside
// maxTasksPerJob, so with only the two per-axis bounds in place all four cases
// are accepted and the third constant could be deleted with every other test in
// the tree still green.
//
// RED at HEAD on three of the four.
func TestValidate_TheJobWideCommandTotalIsBounded(t *testing.T) {
	const overMsg = "at most 25000 commands in total across all tasks are allowed"

	t.Run("one over the total, with every task inside its own bound", func(t *testing.T) {
		// 50 x 500 is exactly 25,000; the 51st task's single command crosses it.
		// 50 <= 5000 and 500 <= 500, so neither per-axis cap fires.
		spec := totalSpec(50, 500)
		spec.Tasks = append(spec.Tasks, TaskSpec{Name: "one-more", Commands: argvN(1)})
		require.EqualError(t, Validate(spec), overMsg,
			"NO 'got' CLAUSE, AND THAT IS A DECISION: the check fires the moment the budget is "+
				"exceeded and does not know the final total, so a running figure would be false and "+
				"would vary with task ordering for the same spec")
	})

	t.Run("exactly at the total is accepted", func(t *testing.T) {
		require.NoError(t, Validate(totalSpec(50, 500)),
			"25,000 exactly must still be accepted - the leg a >= breaks")
	})

	t.Run("a legacy command task counts toward the total", func(t *testing.T) {
		// THE DISCRIMINATING INPUT FOR THE ACCUMULATOR'S POSITION, and the only one
		// in the tree. The 51st task uses the legacy `command` spelling, so it
		// carries len(Commands) == 0 until normalizeTaskCommands rewrites it. An
		// accumulator placed ABOVE the normalization adds 0 for it, the total stops
		// at exactly 25,000, and the spec is accepted.
		//
		// The per-task check's position is NOT distinguishable by any input (a
		// legacy Command can only ever produce one command, and 0 > 500 and 1 > 500
		// are both false). This case covers the half that is.
		spec := totalSpec(50, 500)
		spec.Tasks = append(spec.Tasks, TaskSpec{Name: "legacy", Command: []string{"true"}})
		require.EqualError(t, Validate(spec), overMsg)
	})

	t.Run("the total is refused before traversal completes", func(t *testing.T) {
		// 70 tasks x 400 commands is 28,000, and the running total crosses 25,000 at
		// index 62. A DUPLICATE TASK NAME sits at index 65, three tasks past the
		// crossing.
		//
		// Checking inside the loop reports the total. Completing the pass and
		// checking afterwards reports the duplicate. This pins the "fail as soon as
		// it is exceeded" decision, which is what stops a body-sized spec of
		// 150,000-plus commands being walked in full before it is refused - and the
		// message is the only
		// observable trace of "as soon as".
		spec := totalSpec(70, 400)
		spec.Tasks[65].Name = spec.Tasks[0].Name
		require.EqualError(t, Validate(spec), overMsg)
	})
}
