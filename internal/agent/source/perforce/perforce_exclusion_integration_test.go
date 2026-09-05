//go:build integration

package perforce

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

// recaptureEnv is the explicit opt-in to REWRITE the committed artifacts.
// Without it this lane compares and FAILS on a mismatch, which is the only way
// the capture can detect a p4 that changed its wording: a test that rewrites its
// own fixture absorbs the change instead, and the unit test then passes against
// text nobody read.
const recaptureEnv = "RELAY_RECAPTURE_P4_FILES"

// p4dEnv points the process at the fixture container and neutralises host P4
// configuration, as every other test in this lane does.
func p4dEnv(t *testing.T) p4dHandle {
	t.Helper()
	p4d := startP4dContainer(t)
	t.Setenv("P4PORT", p4d.P4Port)
	t.Setenv("P4USER", p4d.P4User)
	t.Setenv("P4CHARSET", "none")
	t.Setenv("P4CONFIG", "")
	t.Setenv("P4PASSWD", "")
	t.Setenv("P4TICKETS", "")
	return p4d
}

// THIS TEST CANNOT RUN IN CI, AND THE GAP IS DELIBERATE.
// .github/workflows/go-ci.yml runs `go test -race ./...` with no build tags plus
// three `services: postgres` jobs; nothing there provides a p4d server or the
// `p4` client binary. For this to join a CI lane there would have to be a workflow job
// that (a) builds testdata/p4d or runs an equivalent service container, (b)
// installs the Perforce CLI on the runner, and (c) is added to a Makefile
// target's package list the way test-pg-integration hardcodes its own. Until
// then it is human-run.
//
// It cannot move to the default lane at all: the property under test is what
// REAL p4 writes and which exit status it pairs that text with. A fake runner
// echoes whatever it is told.
//
// THE FOUR READINGS ARE THE POINT. p4 exits ZERO for every one of them and
// separates them only by which stream it writes to; the three failing ones are
// three different wordings of a single condition, which is why PathHasFiles
// asserts POSITIVELY on stdout rather than matching any of them.
func TestPerforce_E2E_PathHasFilesReadingsAreCaptured(t *testing.T) {
	p4dEnv(t)

	root := t.TempDir()
	prov := New(Config{Root: root, Hostname: "ci"})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//test/main",
			Sync:   []*relayv1.SyncEntry{{Path: "//test/main/...", Rev: "#head"}},
		},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	h, err := prov.Prepare(ctx, "task-capture", spec, func(s string) { t.Logf("prepare: %s", s) })
	require.NoError(t, err)
	defer func() { _ = h.Finalize(ctx) }()

	client := h.Env()["P4CLIENT"]
	c := NewClient()

	// A client on the VIRTUAL stream, whose view remaps every path under sub/.
	// It is the only way to reach "file(s) not in client view" here, and that
	// reading is not decoration: it is what the second of the two routes the
	// refusal exists for actually produces.
	virtClient := "relay_ci_capture_virt"
	require.NoError(t, c.CreateStreamClient(ctx, virtClient, t.TempDir(), "//test/virt", "", false))
	defer func() { _ = c.DeleteClient(ctx, virtClient) }()

	capture := func(name, cl, filespec string) {
		t.Helper()
		stdout, stderr := rawP4Files(t, ctx, cl, filespec)
		body := "$ p4 -c <client> files -m1 " + redactP4(filespec, cl) + "\n" +
			"--- stdout ---\n" + redactP4(stdout, cl) +
			"--- stderr ---\n" + redactP4(stderr, cl)
		path := filepath.Join("testdata", "p4-files", name)
		if os.Getenv(recaptureEnv) != "" {
			require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
			t.Logf("recaptured %s:\n%s", name, body)
			return
		}
		want, err := os.ReadFile(path)
		require.NoError(t, err, "missing artifact; regenerate with %s=1", recaptureEnv)
		require.Equal(t, string(want), body,
			"p4 no longer produces the captured output for %s. PathHasFiles is written "+
				"against these artifacts; read the diff before regenerating with %s=1",
			name, recaptureEnv)
	}

	capture("exists.txt", client, "//"+client+"/heavy/...#head")
	capture("nosuchfile.txt", client, "//"+client+"/does-not-exist/...#head")
	capture("nofilesatchangelist.txt", client, "//"+client+"/heavy/...@2")
	capture("notinclientview.txt", virtClient, "//"+virtClient+"/heavy/...#head")

	// The predicate against the live server rather than against the artifacts:
	// the unit test proves it reads the recorded text correctly, and this proves
	// the recording is of the call the production code actually makes.
	ok, err := c.PathHasFiles(ctx, client, "//"+client+"/heavy/...#head")
	require.NoError(t, err)
	require.True(t, ok, "a subtree that exists must resolve to at least one file")
	for _, tc := range []struct{ cl, spec string }{
		{client, "//" + client + "/does-not-exist/...#head"},
		{client, "//" + client + "/heavy/...@2"},
		{virtClient, "//" + virtClient + "/heavy/...#head"},
	} {
		ok, err := c.PathHasFiles(ctx, tc.cl, tc.spec)
		require.NoError(t, err, "p4 exits ZERO for %s, so this must not surface as an error", tc.spec)
		require.False(t, ok, "%s resolves to no file and must read as such", tc.spec)
	}
}

// rawP4Files runs the probe's exact argv and returns stdout and stderr
// separately. It does not go through PathHasFiles or Runner: the artifact has to
// record what p4 wrote, not what either of them made of it - and Runner.Run
// discards stderr on the zero exit every one of these readings produces.
func rawP4Files(t *testing.T, ctx context.Context, client, filespec string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "p4", "-c", client, "files", "-m1", filespec)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// A non-zero exit is a fixture fault here, not a reading: p4 exits zero for
	// all four, and that pairing is half of what the artifacts record.
	require.NoError(t, cmd.Run(), "p4 files %s exited non-zero (stderr: %s)", filespec, stderr.String())
	return stdout.String(), stderr.String()
}

// redactP4 removes what would make an artifact machine-specific: the generated
// client name, which carries the agent hostname; the home and temp directories,
// which appear in any p4 message naming a local path; and the line terminator,
// because p4.exe writes CRLF and p4 on Linux writes LF. The terminator is the
// one normalisation that is safe here - the artifact exists to pin p4 WORDING,
// and leaving it in would make every capture fail on the other platform.
func redactP4(s, client string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, client, "<client>")
	for _, dir := range []string{osUserHome(), os.TempDir()} {
		if dir == "" {
			continue
		}
		s = strings.ReplaceAll(s, dir, "<dir>")
		s = strings.ReplaceAll(s, strings.ReplaceAll(dir, `\`, `/`), "<dir>")
	}
	return s
}

func osUserHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// THE ORDER IS LOAD-BEARING: the EXCLUDING task runs FIRST.
//
// Run the unexcluding task first and its full sync leaves heavy/asset.txt on
// disk; the excluding task then shares the same directory under a build that
// ignores exclusions in the workspace key, finds the file already there, and
// every assertion below passes against exactly the defect this design exists to
// prevent. Only excluding-then-including can observe a workspace missing files
// the second task asked for.
//
// THE MUTATION THIS MUST KILL: make SourceKey ignore exclusions and return the
// bare stream. Task B then shares Task A's workspace, the preempted files are
// never fetched, and B's read of heavy/asset.txt goes RED.
//
// Same CI note as TestPerforce_E2E_PathHasFilesReadingsAreCaptured above:
// nothing in .github/workflows provides p4d or the p4 client, so this is
// human-run until a workflow job builds testdata/p4d, installs the Perforce CLI
// and is added to a Makefile target's package list. It cannot move to the
// default lane at all - a fake runner cannot say whether a file is on disk.
func TestPerforce_E2E_AnExcludingTaskDoesNotStripFilesFromAnUnexcludingPeer(t *testing.T) {
	p4dEnv(t)

	root := t.TempDir()
	prov := New(Config{Root: root, Hostname: "ci"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	mk := func(exclude bool) *relayv1.SourceSpec {
		sync := []*relayv1.SyncEntry{{Path: "//test/main/...", Rev: "#head"}}
		if exclude {
			sync = append(sync, &relayv1.SyncEntry{Path: "//test/main/heavy/...", Exclude: true})
		}
		return &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//test/main", Sync: sync},
		}}
	}

	// --- Task A: EXCLUDES heavy/. Runs FIRST; see the comment above. ---
	hA, err := prov.Prepare(ctx, "task-a", mk(true), func(s string) { t.Logf("A: %s", s) })
	require.NoError(t, err, "the excluding prepare must succeed")
	invA := hA.Inventory()
	wsA := hA.WorkingDir()

	require.NoFileExists(t, filepath.Join(wsA, "heavy", "asset.txt"),
		"the excluded subtree must never be transferred")
	require.FileExists(t, filepath.Join(wsA, "readme.txt"),
		"everything outside the exclusion must still be synced")

	require.NoError(t, hA.Finalize(ctx))

	// --- Task B: NO exclusion. It must not observe a workspace missing files. ---
	hB, err := prov.Prepare(ctx, "task-b", mk(false), func(s string) { t.Logf("B: %s", s) })
	require.NoError(t, err)
	invB := hB.Inventory()
	wsB := hB.WorkingDir()
	defer func() { _ = hB.Finalize(ctx) }()

	// THE ACCEPTANCE CRITERION IS ASSERTED FIRST, before any structural
	// assertion about the workspaces. Put the NotEqual checks above it and a
	// SourceKey that ignores exclusions is caught by a PROXY - two paths being
	// equal - and the test halts before it ever reads the file the criterion is
	// about. The criterion has to be the assertion that goes RED.
	b, err := os.ReadFile(filepath.Join(wsB, "heavy", "asset.txt"))
	require.NoError(t, err,
		"THE ACCEPTANCE CRITERION: a task with no exclusions gets the whole stream, "+
			"whatever a previous task on the same stream excluded")
	require.Equal(t, "heavy", strings.TrimSpace(string(b)))

	require.NotEqual(t, wsA, wsB, "a different exclusion set is a different workspace")
	require.NotEqual(t, invA.ShortID, invB.ShortID)

	// The keys themselves: B's is exactly today's, A's is the versioned composite.
	require.Equal(t, "//test/main", invB.SourceKey,
		"a task with no exclusions keeps today's key byte for byte")
	require.True(t, strings.HasPrefix(invA.SourceKey, "x1|"))
	require.True(t, strings.HasSuffix(invA.SourceKey, "|//test/main"))

	reg, err := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	require.NoError(t, err)
	keys := map[string]bool{}
	for _, e := range reg.Snapshot() {
		keys[e.SourceKey] = true
	}
	require.Len(t, keys, 2, "the registry holds two entries with distinct source keys")
	require.True(t, keys["//test/main"] && keys[invA.SourceKey])
}

// THE EXCLUSION IS INERT AND THE TASK MUST NOT REPORT SUCCESS. //test/virt's
// view remaps everything under sub/, so //<client>/heavy/... addresses nothing -
// the second of the two routes the refusal exists for, and the one whose wording
// ("file(s) not in client view.") a single-phrase predicate does not match.
// Without the refusal p4 exits zero, the whole subtree transfers, and the task
// is green. The default-lane guard cannot reach this: only a real p4 decides
// what a remapped view resolves.
//
// Same CI note as the two tests above.
func TestPerforce_E2E_AnExclusionUnderARemappingStreamFailsThePrepare(t *testing.T) {
	p4dEnv(t)

	root := t.TempDir()
	prov := New(Config{Root: root, Hostname: "ci"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//test/virt",
			Sync: []*relayv1.SyncEntry{
				{Path: "//test/virt/...", Rev: "#head"},
				{Path: "//test/virt/heavy/...", Exclude: true},
			},
		},
	}}

	_, err := prov.Prepare(ctx, "task-remap", spec, func(s string) { t.Logf("prepare: %s", s) })
	require.Error(t, err, "an exclusion that addresses nothing must fail the prepare")
	require.ErrorContains(t, err, "//test/virt/heavy/...")

	// Nothing may have been transferred: the refusal exists to stop the excluded
	// subtree arriving in full, so a green require.Error over a full workspace
	// would be the defect wearing the fix's name.
	var found []string
	require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "asset.txt" {
			found = append(found, p)
		}
		return nil
	}))
	require.Empty(t, found, "the refused prepare must not have synced the stream")
}
