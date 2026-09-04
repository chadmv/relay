package perforce

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// capturedStderr returns the stderr section of a p4-sync-k artifact, which is
// what the production caller passes the predicate. Handing it the whole file
// instead would let a predicate that keyed on the recorded command line pass.
func capturedStderr(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "p4-sync-k", name))
	require.NoError(t, err, "capture the artifact in the p4d lane first")
	_, after, ok := strings.Cut(string(b), "--- stderr ---\n")
	require.True(t, ok, "artifact %s has no stderr section", name)
	return after
}

// The inputs are the ARTIFACTS captured from real p4 in the p4d lane, not
// literals typed from documentation. The two readings this predicate keeps
// apart are the whole of the refusal: a filespec that matched nothing (refuse -
// the exclusion is inert and the volume fills) versus a warm workspace already
// at the target have-revision (accept - reading that as failure would refuse
// every prepare after the first). marked.txt is the third reading, and the
// capture shows why it is not redundant: a real have-marking writes its
// per-file lines to STDOUT and leaves stderr EMPTY, so a predicate that treated
// an empty stderr as anything but success would refuse every cold prepare.
func TestPreemptReportedNoSuchFiles_ReadsTheCapturedArtifacts(t *testing.T) {
	require.True(t, preemptReportedNoSuchFiles(capturedStderr(t, "nosuchfile.txt")))
	require.False(t, preemptReportedNoSuchFiles(capturedStderr(t, "uptodate.txt")))
	require.False(t, preemptReportedNoSuchFiles(capturedStderr(t, "marked.txt")))
}

// The decoy goes BEFORE the phrase, so a mutant reading only the first line is
// visible; a decoy placed last is read by neither the code nor the mutant.
func TestPreemptReportedNoSuchFiles_EdgeCases(t *testing.T) {
	require.False(t, preemptReportedNoSuchFiles(""))
	require.False(t, preemptReportedNoSuchFiles("//c/heavy/x.ma#1 - added as /w/heavy/x.ma\n"))
	require.True(t, preemptReportedNoSuchFiles(
		"//c/heavy/x.ma#1 - added as /w/heavy/x.ma\n//c/typo/... - no such file(s).\n"))
	require.True(t, preemptReportedNoSuchFiles("//c/typo/... - No such file(s).\n"),
		"the reading must not depend on p4's capitalisation")
}
