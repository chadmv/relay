package perforce

import (
	"context"
	"fmt"
	"strings"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func countLinesContaining(lines []string, sub string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// syncSpec is the fixture recipe for driving Prepare through fakeRunner without
// p4d: a single #head path that resolves to CL 12345 on a fresh workspace.
func syncFixture(t *testing.T) (*fakeRunner, string, *relayv1.SourceSpec) {
	t.Helper()
	fr := newFakeP4Fixture(t)
	client := expectedClientName("h", "//s/x")
	fr.set("-c "+client+" changes -m1 //"+client+"/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	return fr, "-c " + client + " sync --parallel=4 //" + client + "/...@12345", spec
}

// The brackets are one line each, and p4's per-file output is COUNTED rather
// than forwarded. The verb probes and the file count are two halves of one
// property and neither is sufficient alone: "no per-file line" is satisfied
// vacuously by a sync that emitted nothing at all, and the count is what
// refuses that reading by proving the counter is wired to onLine. Breaking
// either half by editing the fixture leaves the other looking healthy.
//
// The fixture's three lines are real p4 sync output shapes. The middle one
// carries " - " inside its filename, so it also fails a depot-path rule that
// splits there - but only weakly: such a split still yields a non-empty prefix
// and the line still counts as a file. TestSyncLineDepotPath is what kills that
// rule; do not conflate the two tests.
func TestProvider_PerFileSyncOutputIsCountedAndNeverForwarded(t *testing.T) {
	fr, syncKey, spec := syncFixture(t)
	fr.setStream(syncKey,
		"//depot/x/a.ma#3 - added as /ws/a.ma\n"+
			"//depot/x/My File - Copy.ma#1 - updating /ws/My File - Copy.ma\n"+
			"//depot/x/c.ma#2 - refreshing /ws/c.ma\n")

	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: fr}})
	var lines []string
	h, err := p.Prepare(context.Background(), "task-1", spec, func(s string) { lines = append(lines, s) })
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	assert.Equal(t, 1, countLinesContaining(lines, "[sync] starting"),
		"exactly one start bracket, got: %v", lines)
	assert.Equal(t, 1, countLinesContaining(lines, "[sync] complete"),
		"exactly one complete bracket, got: %v", lines)
	assert.Equal(t, 1, countLinesContaining(lines, "1 path"),
		"the start line reports how many paths are being synced, got: %v", lines)

	for _, verb := range []string{"added as", "updating", "refreshing"} {
		assert.Equal(t, 0, countLinesContaining(lines, verb),
			"p4's per-file output is counted, never forwarded: a large sync is millions of "+
				"lines and task_logs has no per-task cap, got: %v", lines)
	}
	assert.Equal(t, 1, countLinesContaining(lines, "3 files"),
		"and the count is what keeps the assertions above from being satisfied by a sync "+
			"that emitted nothing at all, got: %v", lines)
	assert.Equal(t, 1, countLinesContaining(lines, "0 other lines"),
		"every fixture line parsed as a depot path, got: %v", lines)
	assert.Equal(t, 1, countLinesContaining(lines, "last //depot/x/c.ma"),
		"lastPath tracks the LAST line, not the first, got: %v", lines)
}

// The cause has exactly ONE home. The error returned here becomes ErrorMessage
// on the agent's PREPARE_FAILED, and the coordinator stores that - classified
// and wrapped - as the task's last log line. Repeating it in the bracket would
// put the cause in the log twice in two spellings with nothing saying which is
// authoritative. A reviewer reading this diff alone will want to add the error
// text back into the failure line; this test is what refuses it.
func TestProvider_ASyncFailureProgressLineDoesNotRepeatTheCause(t *testing.T) {
	fr, syncKey, spec := syncFixture(t)
	fr.setStreamErr(syncKey, fmt.Errorf("exit status 1 (stderr: SYNC-CAUSE-SENTINEL no space left on device)"))

	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: fr}})
	var lines []string
	_, err := p.Prepare(context.Background(), "task-1", spec, func(s string) { lines = append(lines, s) })

	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of disk space on this agent's workspace volume",
		"the RETURNED error is where the classified cause lives")
	assert.Contains(t, err.Error(), "SYNC-CAUSE-SENTINEL", "and it still wraps the original")

	assert.Equal(t, 1, countLinesContaining(lines, "[sync] failed"),
		"exactly one failure bracket, got: %v", lines)
	assert.Equal(t, 1, countLinesContaining(lines, "[sync] starting"),
		"the start bracket must still have gone out, got: %v", lines)
	for _, l := range lines {
		assert.NotContains(t, l, "SYNC-CAUSE-SENTINEL",
			"the cause has one home and it is not this line: %q", l)
	}
}

// heldWorkspaceCount reports how many holders the provider's single workspace
// has right now. Used to observe, from inside the progress callback, whether the
// failing sync has already released its hold.
func heldWorkspaceCount(t *testing.T, p *Provider) int {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.Len(t, p.workspaces, 1, "fixture drives exactly one workspace")
	for _, ws := range p.workspaces {
		ws.mu.Lock()
		defer ws.mu.Unlock()
		return len(ws.holders)
	}
	return -1
}

// progress is makePrepareProgressFn's closure in production, and its flush calls
// Runner.send, which selects only on sendCh and the AGENT context - it cannot be
// woken by a per-task cancel. So it can park for the length of a coordinator
// outage. Anything between the sync failure and handle.Release() therefore holds
// a doomed workspace for that whole time, and every later task for the stream
// blocks in Workspace.Acquire. The hold must be gone before the line goes out.
func TestProvider_ASyncFailureReleasesTheWorkspaceBeforeItReportsAnything(t *testing.T) {
	fr, syncKey, spec := syncFixture(t)
	fr.setStreamErr(syncKey, fmt.Errorf("exit status 1 (stderr: no space left on device)"))

	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: fr}})

	heldAtFailureLine := -1
	_, err := p.Prepare(context.Background(), "task-1", spec, func(s string) {
		if strings.Contains(s, "[sync] failed") {
			heldAtFailureLine = heldWorkspaceCount(t, p)
		}
	})

	require.Error(t, err)
	require.NotEqual(t, -1, heldAtFailureLine, "the failure line must have been emitted at all")
	assert.Equal(t, 0, heldAtFailureLine,
		"the workspace must already be released when the failure line is emitted, "+
			"because emitting it can park until agent shutdown")
}
