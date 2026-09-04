//go:build integration

package perforce

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

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
// two `services: postgres` jobs; nothing there provides a p4d server or the `p4`
// client binary. For this to join a CI lane there would have to be a workflow job
// that (a) builds testdata/p4d or runs an equivalent service container, (b)
// installs the Perforce CLI on the runner, and (c) is added to a Makefile
// target's package list the way test-pg-integration hardcodes its own. Until
// then it is human-run.
//
// It cannot move to the default lane at all: the property under test is what
// REAL p4 writes and which exit status it pairs that text with. A fake runner
// echoes whatever it is told.
func TestPerforce_E2E_SyncKReportsNoSuchFilesOnStderrAndExitsZero(t *testing.T) {
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
	wsRoot := h.WorkingDir()
	c := NewClient()

	capture := func(name, filespec string) string {
		t.Helper()
		var lines []string
		stderr, err := c.SyncPreempt(ctx, wsRoot, client, filespec, func(l string) { lines = append(lines, l) })
		require.NoError(t, err, "p4 sync -k must not fail for %s", filespec)
		body := "$ p4 -c <client> sync -k " + filespec + "\n" +
			"--- stdout ---" + "\n" + strings.Join(lines, "\n") + "\n" +
			"--- stderr ---" + "\n" + stderr + "\n"
		require.NoError(t, os.WriteFile(filepath.Join("testdata", "p4-sync-k", name), []byte(body), 0o644))
		t.Logf("captured %s:%s%s", name, "\n", body)
		return body
	}

	// Prepare has already synced the whole stream, so the have-list ALREADY
	// covers heavy/ at #head. Capturing "marked" here without this line
	// produces a file byte-identical to "uptodate", and the two readings the
	// parser has to keep apart would then be pinned by one artifact wearing two
	// names. #none clears the have-list for that subtree so the next call is a
	// real have-marking with per-file output.
	_, err = c.SyncPreempt(ctx, wsRoot, client, "//"+client+"/heavy/...#none", func(string) {})
	require.NoError(t, err)

	marked := capture("marked.txt", "//"+client+"/heavy/...#head")     // have-list cleared above
	uptodate := capture("uptodate.txt", "//"+client+"/heavy/...#head") // already at that have-rev
	nosuch := capture("nosuchfile.txt", "//"+client+"/does-not-exist/...#head")

	// The three artifacts must be three readings, not two. A capture order that
	// let "marked" and "uptodate" coincide would leave the predicate's
	// discriminating input untested while every assertion below still passed.
	require.NotEqual(t, marked, uptodate,
		"a real have-marking and an already-marked no-op must be distinguishable captures")

	// The two behaviours the parser depends on, asserted rather than described.
	// TEXT, not exit status: p4 exits ZERO for all three
	// (bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero), which is
	// the whole reason SyncPreempt returns stderr at all.
	require.Contains(t, strings.ToLower(nosuch), "no such file",
		"a filespec that matched nothing must be distinguishable, and only its text distinguishes it")
	require.NotContains(t, strings.ToLower(uptodate), "no such file",
		"an already-excluded subtree on a warm workspace must not read as a typo; "+
			"zero per-file lines is success here, not emptiness")
	require.NotContains(t, strings.ToLower(marked), "no such file")
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
// Same CI note as TestPerforce_E2E_SyncKReportsNoSuchFilesOnStderrAndExitsZero
// above: nothing in .github/workflows provides p4d or the p4 client, so this is
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
