//go:build integration

package perforce

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// rawP4Clients asks the fixture server whether a client spec named name exists,
// shelling out the way rawP4Files in this lane already does rather than adding a
// production method that only a test would call.
func rawP4Clients(t *testing.T, ctx context.Context, name string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "p4", "clients", "-e", name)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "p4 clients -e stderr: %s", stderr.String())
	return strings.TrimSpace(stdout.String())
}

// TestClient_CreateStreamClient_RefusesAStreamThatDoesNotExist pins the premise
// the per-stream exclusion ceiling rests on: an author cannot invent streams, so
// the number of streams one agent can be driven to hold workspaces for is
// bounded by something the author does not control. The ceiling then bounds
// exclusion sets per stream, which is a bound moved rather than a bound removed.
//
// IT CANNOT RUN IN CI, and startP4dContainer's own comment names exactly what a
// workflow job would have to supply for it to. Until that exists it is human-run.
// It cannot move to the default lane at all: the property is what REAL p4 does
// with a stream name that is not there, and a fake runner echoes whatever it is
// told.
//
// THE QUESTION IS WHETHER THE SPEC CAN BE PERSISTED, not whether one command
// exits non-zero. p4 exits zero on conditions it also reports on stderr - the
// reason PathHasFiles asserts positively on stdout - so the reading is the whole
// create, one `client -o` followed by one `client -i`, plus a check that the
// server holds no such client afterwards. An error alone is necessary and not
// sufficient: a create that failed on its second call having already saved a spec
// would satisfy it and still leave the artifact behind.
//
// The control on a real stream is what makes the refusal meaningful. Without it
// a CreateStreamClient failing for any unrelated reason - a bad ticket, a
// fixture that never came up - satisfies the assertion.
func TestClient_CreateStreamClient_RefusesAStreamThatDoesNotExist(t *testing.T) {
	p4dEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	c := NewClient()
	const bogusStream = "//test/no-such-stream-qqzz"
	const bogusName = "relay_ci_bogus_stream"
	const realName = "relay_ci_real_stream"

	require.NoError(t, c.CreateStreamClient(ctx, realName, t.TempDir(), "//test/main", "", false),
		"the control: a client for a stream that DOES exist is creatable, so a refusal below "+
			"is about the missing stream and not about this fixture or these arguments")
	defer func() { _ = c.DeleteClient(ctx, realName) }()
	require.NotEmpty(t, rawP4Clients(t, ctx, realName),
		"the control again, on the reading that matters: a real stream's client IS persisted")

	err := c.CreateStreamClient(ctx, bogusName, t.TempDir(), bogusStream, "", false)
	if err == nil {
		// Clean up before failing, so a surprising pass does not leave a client on
		// the fixture server for whatever runs next in this lane.
		_ = c.DeleteClient(ctx, bogusName)
	}
	require.Error(t, err,
		"a client for a stream that does not exist must not be creatable: the exclusion-set "+
			"ceiling bounds workspaces PER STREAM, and that is a bound only while the set of "+
			"streams is not something a job author can invent")

	require.Empty(t, rawP4Clients(t, ctx, bogusName),
		"and no client spec for it was persisted on the shared Perforce server")
}
