package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsReservedIdentityNameFor_FoldsExactlyWhereOsExecFolds drives the goos
// seam directly rather than through runtime.GOOS, because CI runs on Linux only
// and the Windows half of the rule would otherwise be killed by no lane at all.
//
// The long-s rows are the discriminating ones. os/exec folds a Windows key with
// strings.ToLower; U+017F simple-folds with 'S' under strings.EqualFold but is
// already lower-case under ToLower, so an EqualFold predicate deletes a spec key
// that os/exec would have carried through as a distinct variable. It is written
// as a Go escape: a raw non-ASCII byte in a source literal is unverifiable by
// eye.
func TestIsReservedIdentityNameFor_FoldsExactlyWhereOsExecFolds(t *testing.T) {
	cases := []struct {
		name string
		goos string
		key  string
		want bool
	}{
		{"exact spelling, windows", "windows", "RELAY_JOB_URL", true},
		{"exact spelling, linux", "linux", "RELAY_JOB_URL", true},
		{"exact spelling of the id, windows", "windows", "RELAY_TASK_ID", true},
		{"exact spelling of the id, linux", "linux", "RELAY_TASK_ID", true},
		{"lower case is the same variable on windows", "windows", "relay_job_url", true},
		{"lower case is a distinct variable on linux", "linux", "relay_job_url", false},
		{"mixed case is the same variable on windows", "windows", "Relay_Task_Url", true},
		{"mixed case is a distinct variable on linux", "linux", "Relay_Task_Url", false},
		{"long s is a distinct variable on windows", "windows", "RELAY_TA\u017fK_ID", false},
		{"long s is a distinct variable on linux", "linux", "RELAY_TA\u017fK_ID", false},
		{"a longer name that merely starts with one", "windows", "RELAY_JOB_URL_EXTRA", false},
		{"an unrelated name", "linux", "PATH", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isReservedIdentityNameFor(tc.goos, tc.key))
		})
	}
}
