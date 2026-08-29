package schedrunner_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"relay/internal/schedrunner"

	"github.com/stretchr/testify/require"
)

// storedSpecJSON marshals a job spec of nTasks tasks with perTask commands each,
// the way a release before these bounds would have stored it in
// scheduled_jobs.job_spec.
func storedSpecJSON(t *testing.T, nTasks, perTask int) []byte {
	t.Helper()
	tasks := make([]map[string]any, nTasks)
	for i := range tasks {
		cmds := make([][]string, perTask)
		for j := range cmds {
			cmds[j] = []string{"true"}
		}
		tasks[i] = map[string]any{"name": "t" + strconv.Itoa(i), "commands": cmds}
	}
	b, err := json.Marshal(map[string]any{"name": "legacy", "tasks": tasks})
	require.NoError(t, err)
	return b
}

// TestValidateStoredSchedule_RefusesAStoredSpecOverACountBound covers the two
// STORED-SPEC paths that reach jobspec.Validate through this one exported symbol:
// schedrunner.ValidateStoredSpecsOnStartup (the boot sweep, which writes the
// returned message into scheduled_jobs.last_error over every ENABLED schedule)
// and internal/api's handlePatchScheduledJob clear-decision (which clears the
// failure record if and only if this returns nil on the EFFECTIVE row).
//
// IT IS UNTAGGED ON PURPOSE. ValidateStoredSchedule is a pure function of three
// stored values, so the retroactivity fact - a spec an older release accepted
// stops validating - needs no Postgres. Putting it behind the integration tag
// would put the only count-axis coverage of two call sites in the lane
// .github/workflows/go-ci.yml never runs.
//
// WHAT IT DOES NOT PROVE, stated so nobody reads more into it: that either caller
// is wired to this function. That is proven message-agnostically by
// TestValidateStoredSpecsOnStartup and by the PATCH clear-decision tests in
// internal/api, both of which use the retries message and neither of which cares
// which rule produced it.
func TestValidateStoredSchedule_RefusesAStoredSpecOverACountBound(t *testing.T) {
	cases := []struct {
		name string
		spec []byte
		want string
	}{
		{"over the task count", storedSpecJSON(t, 5001, 1),
			"at most 5000 tasks are allowed, got 5001"},
		{"over the per-task command count", storedSpecJSON(t, 1, 501),
			"task t0: at most 500 commands are allowed, got 501"},
		{"over the job-wide command total", storedSpecJSON(t, 51, 500),
			"at most 25000 commands in total across all tasks are allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.EqualError(t,
				schedrunner.ValidateStoredSchedule(tc.spec, "@hourly", "UTC"), tc.want,
				"a stored spec over a count bound must be refused with the bound's OWN message: it is "+
					"what the boot sweep writes into last_error and what run-now answers with, and a "+
					"generic string is what makes a silently-stopped schedule unexplainable")
		})
	}

	// CONTROL, on the same function and after the three refusals: a stored spec at
	// the per-task and job-total bounds exactly must still validate. Without it a
	// ValidateStoredSchedule that had started refusing everything - a broken cron
	// parser, a changed permanent() vocabulary - would pass all three cases above.
	require.NoError(t,
		schedrunner.ValidateStoredSchedule(storedSpecJSON(t, 50, 500), "@hourly", "UTC"),
		"control: 50 tasks x 500 commands is exactly at maxCommandsPerTask and exactly at "+
			"maxCommandsPerJob, and must still be fireable")
}
