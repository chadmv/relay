package jobspec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// i32 returns a pointer to v. TaskSpec.TimeoutSeconds is a *int32 because nil is
// the documented "no deadline", so every timeout case here has to spell the
// pointer out.
func i32(v int32) *int32 { return &v }

// twoTaskSpec builds a valid two-task spec with the OFFENDING task FIRST and a
// healthy task second.
//
// THE ORDER IS THE POINT, not tidiness. Validate returns the first problem it
// finds, so a bad task placed LAST cannot distinguish "the bound rejected it"
// from "the loop exited early for some unrelated reason". Putting it first also
// forces the error to NAME the offending task, which is what the assertions
// below check rather than merely that an error occurred.
func twoTaskSpec(bad TaskSpec) *JobSpec {
	bad.Name = "bad-task"
	bad.Command = []string{"echo", "x"}
	return &JobSpec{
		Name: "bounds",
		Tasks: []TaskSpec{
			bad,
			{Name: "healthy-task", Command: []string{"echo", "y"}},
		},
	}
}

// TestValidate_RetriesAndTimeoutOutOfRangeAreRejected is the half-A rejection
// table. RED at HEAD: Validate reads neither field.
//
// The assertion is on the WHOLE message, not on a substring, because the
// per-task naming is the property the backlog item asks for - a caller with a
// fifty-task spec has to be told which task is wrong.
func TestValidate_RetriesAndTimeoutOutOfRangeAreRejected(t *testing.T) {
	const retriesMsg = "task bad-task: retries must be between 0 and 10"
	const timeoutMsg = "task bad-task: timeout_seconds must be between 0 and 604800 (0 or omitted means no deadline)"

	cases := []struct {
		name string
		task TaskSpec
		want string
	}{
		{"retries one over the cap", TaskSpec{Retries: 11}, retriesMsg},
		{"retries negative", TaskSpec{Retries: -1}, retriesMsg},
		{"retries at the item's own repro value", TaskSpec{Retries: 2000000000}, retriesMsg},
		{"timeout one over the cap", TaskSpec{TimeoutSeconds: i32(604801)}, timeoutMsg},
		{"timeout negative", TaskSpec{TimeoutSeconds: i32(-1)}, timeoutMsg},
		{"timeout at int32 max", TaskSpec{TimeoutSeconds: i32(2147483647)}, timeoutMsg},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(twoTaskSpec(tc.task))
			require.Error(t, err,
				"an out-of-range value must be refused at the single validation point, which REST, CLI, "+
					"MCP and schedrunner all inherit")
			require.Equal(t, tc.want, err.Error(),
				"the message must NAME the offending task and STATE the range, matching this file's "+
					"existing per-task error style")
		})
	}
}

// TestValidate_BoundaryValuesAreAccepted is the positive control, and it is what
// an off-by-one in either constant breaks. An off-by-one is the most likely
// defect in this whole change and nothing else catches it.
//
// The nil case is called out by the backlog item by name: TimeoutSeconds is a
// *int32 and nil is the documented "no deadline". 0 is its second, equally valid
// spelling and stays accepted - rejecting it would break stored specs for no
// benefit.
func TestValidate_BoundaryValuesAreAccepted(t *testing.T) {
	cases := []struct {
		name string
		task TaskSpec
	}{
		{"retries at zero, the default", TaskSpec{Retries: 0}},
		{"retries exactly at the cap", TaskSpec{Retries: 10}},
		{"timeout_seconds exactly zero", TaskSpec{TimeoutSeconds: i32(0)}},
		{"timeout_seconds exactly at the cap", TaskSpec{TimeoutSeconds: i32(604800)}},
		{"timeout_seconds omitted entirely", TaskSpec{}},
		{"both at their caps on one task", TaskSpec{Retries: 10, TimeoutSeconds: i32(604800)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, Validate(twoTaskSpec(tc.task)),
				"a spec AT the boundary must still be accepted")
		})
	}
}
