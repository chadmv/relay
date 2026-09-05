package perforce

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// capturedStdout returns the stdout section of a p4-files artifact, which is the
// only section PathHasFiles reads. Handing it the whole file instead would let a
// predicate that keyed on the recorded stderr - the phrase match this design
// replaced - pass against every artifact.
func capturedStdout(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "p4-files", name))
	require.NoError(t, err, "capture the artifact in the p4d lane first")
	_, after, ok := strings.Cut(string(b), "--- stdout ---\n")
	require.True(t, ok, "artifact %s has no stdout section", name)
	before, _, ok := strings.Cut(after, "--- stderr ---\n")
	require.True(t, ok, "artifact %s has no stderr section", name)
	return before
}

// The inputs are the ARTIFACTS captured from real p4 in the p4d lane, not
// literals typed from documentation, and they are run through the production
// predicate rather than through a copy of it.
//
// THE THREE FAILING READINGS ARE THREE DIFFERENT WORDINGS of one condition -
// "no such file(s).", "no file(s) at that changelist number." and "file(s) not
// in client view." - and p4 exits ZERO for all three, which is why the
// predicate reads stdout rather than any of them. The middle and last are the
// ones a "no such file" phrase match missed, and the last is what the remapped
// stream route (the second of the two routes the refusal exists for) produces.
func TestPathHasFiles_ReadsTheCapturedArtifacts(t *testing.T) {
	for _, tc := range []struct {
		artifact string
		want     bool
	}{
		{"exists.txt", true},
		{"nosuchfile.txt", false},
		{"nofilesatchangelist.txt", false},
		{"notinclientview.txt", false},
	} {
		t.Run(tc.artifact, func(t *testing.T) {
			fr := newFakeP4Fixture(t)
			fr.set("-c c files -m1 //c/x/...", capturedStdout(t, tc.artifact))
			got, err := (&Client{r: fr}).PathHasFiles(context.Background(), "c", "//c/x/...")
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// A p4 error is not an answer. Reading a failed probe as "resolved to nothing"
// would turn any transient server fault into a refusal blaming the operator's
// path, and reading it as "resolved" would restore the silent full transfer the
// probe exists to prevent.
func TestPathHasFiles_APropagatedErrorIsNotAnAnswer(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.setErr("-c c files -m1 //c/x/...", context.DeadlineExceeded)
	got, err := (&Client{r: fr}).PathHasFiles(context.Background(), "c", "//c/x/...")
	require.Error(t, err)
	require.False(t, got)
}
