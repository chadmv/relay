package perforce

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

// assertCwdContract partitions the runner's calls by argv SHAPE: a call that
// carries `-c <client>` and is not head resolution must run from wsRoot, and
// every other call must run with an empty cwd. It cannot see WHEN a call was
// made, so it says nothing about ordering against ws.Acquire.
//
// Head resolution is carved out because it runs with no workspace handle;
// TestProvider_HeadResolutionRunsWithNoWorkspaceCwd pins its empty cwd.
func assertCwdContract(t *testing.T, fr *fakeRunner, wsRoot string) {
	t.Helper()
	sawWorkspaceCall := false
	for _, c := range fr.calls {
		if len(c.args) > 0 && c.args[0] == "-c" && !isHeadResolution(c.args) {
			sawWorkspaceCall = true
			require.Equalf(t, wsRoot, c.cwd, "call %q runs under the handle and must run from wsRoot", c.args)
		} else {
			require.Equalf(t, "", c.cwd, "call %q holds no workspace handle and must run with empty cwd", c.args)
		}
	}
	require.True(t, sawWorkspaceCall, "expected at least one invocation made under the workspace handle")
}

// isHeadResolution matches the `-c <client> changes -m1 <path>#head` argv.
// PendingChangesByDescPrefix also issues a `changes` subcommand, from wsRoot
// under the handle, so the -m1 is what discriminates.
func isHeadResolution(args []string) bool {
	return len(args) >= 4 && args[0] == "-c" && args[2] == "changes" && args[3] == "-m1"
}

// Head resolution runs before ws.Acquire, so the workspace has no holders,
// LockedShortIDs does not report it, and a sweep can select it and call
// os.RemoveAll on it. A live subprocess cwd inside that directory makes the
// RemoveAll fail on Windows, which sets the one-way DirtyDelete and returns
// before reg.Remove - leaving a row whose baseline no longer describes what is
// on disk. The cwd also decides which .p4config p4 picks up, and a previous
// task's own build script can have written one into the workspace root.
//
// The -c <client> flag is what selects the client; the cwd is not required.
func TestProvider_HeadResolutionRunsWithNoWorkspaceCwd(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	client := expectedClientName("h", "//s/x")
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -m1 //"+client+"/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.setStream("-c "+client+" sync --parallel=4 //"+client+"/...@12345", "1 of 1 files\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	h, err := p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	var found bool
	for _, c := range fr.calls {
		if len(c.args) >= 4 && c.args[0] == "-c" && c.args[2] == "changes" && c.args[3] == "-m1" {
			found = true
			require.Equal(t, "", c.cwd, "head resolution must not hold a cwd on an unheld workspace")
		}
	}
	require.True(t, found, "expected a head-resolution invocation")
}

func TestProvider_PrepareCreatesClientAndSyncs(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	expectedClient := expectedClientName("h", "//s/x")
	fr.set("-c "+expectedClient+" changes -m1 //"+expectedClient+"/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	// CreateStreamClient: fetch existing spec (empty = new client), then write it back.
	fr.set("client -o -S //s/x "+expectedClient, "")
	fr.set("client -i", "Client saved.\n")
	// recoverOrphanedCLs: no pending CLs on a fresh workspace.
	fr.set("-c "+expectedClient+" changes -c "+expectedClient+" -s pending -l", "")
	// SyncStream: now invoked with global -c <client>.
	fr.setStream("-c "+expectedClient+" sync --parallel=4 //"+expectedClient+"/...@12345", "1 of 1 files\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	var lines []string
	h, err := p.Prepare(context.Background(), "task-1", spec, func(s string) { lines = append(lines, s) })
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	inv := h.Inventory()
	require.Equal(t, "perforce", inv.SourceType)
	require.Equal(t, "//s/x", inv.SourceKey)
	require.NotEmpty(t, inv.ShortID)
	require.NotEmpty(t, inv.BaselineHash)

	require.True(t, filepath.IsAbs(h.WorkingDir()))
	require.Contains(t, h.WorkingDir(), inv.ShortID)
	require.Contains(t, h.Env()["P4CLIENT"], inv.ShortID)
	require.NotEmpty(t, lines, "the bracket lines are what make the sync observable at all: "+
		"p4's own per-file output is counted, never forwarded")

	// Pin the contract: the sync invocation MUST start with `-c <client>`.
	// This guards against a future refactor silently dropping the global flag.
	var syncCall []string
	for _, c := range fr.argHistory() {
		if len(c) >= 3 && c[2] == "sync" {
			syncCall = c
			break
		}
	}
	require.NotNil(t, syncCall, "expected a sync invocation in argHistory")
	require.Equal(t, []string{"-c", expectedClient}, syncCall[:2],
		"sync invocation must begin with -c <client>")

	assertCwdContract(t, fr, h.WorkingDir())
}

func TestProvider_UnshelveAndFinalizeRevert(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	expectedClient := expectedClientName("h", "//s/x")
	fr.set("-c "+expectedClient+" changes -m1 //"+expectedClient+"/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	fr.set("client -o -S //s/x "+expectedClient, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+expectedClient+" changes -c "+expectedClient+" -s pending -l", "")
	fr.setStream("-c "+expectedClient+" sync --parallel=4 //"+expectedClient+"/...@12345", "1 of 1 files\n")
	fr.set("-c "+expectedClient+" change -o", "Change: new\nDescription:\t<enter description here>\n")
	fr.set("-c "+expectedClient+" change -i", "Change 91244 created.\n")
	fr.set("-c "+expectedClient+" unshelve -s 12346 -c 91244", "//s/x/foo - unshelved\n")
	fr.set("-c "+expectedClient+" revert -c 91244 //...", "//s/x/foo - reverted\n")
	fr.set("-c "+expectedClient+" change -d 91244", "Change 91244 deleted.\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream:    "//s/x",
			Sync:      []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
			Unshelves: []int64{12346},
		},
	}}

	h, err := p.Prepare(context.Background(), "task-unshelve", spec, func(string) {})
	require.NoError(t, err)
	require.NoError(t, h.Finalize(context.Background()))

	args := fr.argHistory()
	// Verify the CL lifecycle
	found := func(target []string) bool {
		for _, a := range args {
			if len(a) == len(target) {
				match := true
				for i := range a {
					if a[i] != target[i] {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		}
		return false
	}
	require.True(t, found([]string{"-c", expectedClient, "change", "-i"}), "expected change -i (create CL)")
	require.True(t, found([]string{"-c", expectedClient, "unshelve", "-s", "12346", "-c", "91244"}))
	require.True(t, found([]string{"-c", expectedClient, "revert", "-c", "91244", "//..."}))
	require.True(t, found([]string{"-c", expectedClient, "change", "-d", "91244"}))

	assertCwdContract(t, fr, h.WorkingDir())

	// Registry must be clean after Finalize.
	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	e, ok := reg.GetBySourceKey("//s/x")
	require.True(t, ok)
	require.Empty(t, e.OpenTaskChangelists)
}

func TestProvider_CrashRecovery_DeletesOrphanedPendingCLs(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)

	// Pre-seed registry with an existing workspace that has an orphaned CL
	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	shortID := allocateShortID("//s/x", reg)
	clientName := fmt.Sprintf("relay_h_%s", shortID)
	reg.Upsert(WorkspaceEntry{
		ShortID: shortID, SourceKey: "//s/x",
		ClientName: clientName, BaselineHash: "oldhash",
		LastUsedAt:          time.Now(),
		OpenTaskChangelists: []OpenTaskChangelist{{TaskID: "old", PendingCL: 91244}},
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, shortID), 0o755))

	fr.set("-c "+clientName+" changes -m1 //"+clientName+"/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	fr.set("client -o -S //s/x "+clientName, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+clientName+" changes -c "+clientName+" -s pending -l",
		"Change 91244 on 2026-04-24 by relay@h *pending*\n\trelay-task-old\n\nChange 99999 on 2026-04-24 by other@h *pending*\n\thuman work\n")
	fr.set("-c "+clientName+" revert -c 91244 //...", "//... - reverted\n")
	fr.set("-c "+clientName+" change -d 91244", "Change 91244 deleted.\n")
	fr.setStream("-c "+clientName+" sync --parallel=4 //"+clientName+"/...@12345", "ok\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	h, err := p.Prepare(context.Background(), "task-new", spec, func(string) {})
	require.NoError(t, err)
	require.NoError(t, h.Finalize(context.Background()))

	args := fr.argHistory()
	found := func(target []string) bool {
		for _, a := range args {
			if len(a) == len(target) {
				match := true
				for i := range a {
					if a[i] != target[i] {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		}
		return false
	}
	require.True(t, found([]string{"-c", clientName, "revert", "-c", "91244", "//..."}))
	require.True(t, found([]string{"-c", clientName, "change", "-d", "91244"}))
	// Must NOT touch CL 99999 (not relay-owned)
	require.False(t, found([]string{"-c", clientName, "change", "-d", "99999"}))

	assertCwdContract(t, fr, h.WorkingDir())
}

func TestProvider_RegistryReturnsSharedInstance(t *testing.T) {
	root := t.TempDir()
	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: newFakeP4Fixture(t)}})

	r1, err := p.Registry()
	require.NoError(t, err)
	r2, err := p.Registry()
	require.NoError(t, err)
	require.Same(t, r1, r2, "Registry() must return the same cached instance")
}

func TestProvider_Prepare_ClassifiesAuthError(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	// Inject at head resolution the canonical "ticket invalid" stderr that
	// execRunner would surface in production.
	expectedClient := expectedClientName("h", "//s/x")
	fr.set("client -o -S //s/x "+expectedClient, "")
	fr.set("client -i", "Client saved.\n")
	fr.setErr("-c "+expectedClient+" changes -m1 //"+expectedClient+"/...#head",
		fmt.Errorf("p4 changes -m1 //s/x/...#head: exit status 1 (stderr: Perforce password (P4PASSWD) invalid or unset.)"))

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	_, err := p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.Error(t, err)
	require.Contains(t, err.Error(), "operator must run 'p4 login'",
		"Prepare must surface the classified message so it appears in task failure logs")
}

func TestProvider_Prepare_ClassifiesRecoverError(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	expectedClient := expectedClientName("h", "//s/x")

	// Set up a dirty workspace so needsSync=true and recoverOrphanedCLs is called.
	fr.set("-c "+expectedClient+" changes -m1 //"+expectedClient+"/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	fr.set("client -o -S //s/x "+expectedClient, "")
	fr.set("client -i", "Client saved.\n")
	// recoverOrphanedCLs: inject auth error so it surfaces in progress output.
	fr.setErr("-c "+expectedClient+" changes -c "+expectedClient+" -s pending -l",
		fmt.Errorf("p4 changes ...: exit status 1 (stderr: Perforce password (P4PASSWD) invalid or unset.)"))
	// Sync proceeds after recovery error (which only goes to progress, not task failure).
	fr.setStream("-c "+expectedClient+" sync --parallel=4 //"+expectedClient+"/...@12345", "")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	var progressLines []string
	_, _ = p.Prepare(context.Background(), "task-1", spec, func(s string) {
		progressLines = append(progressLines, s)
	})
	// recoverOrphanedCLs errors are logged via progress(), not returned as task failure.
	// Verify the classified message appears in progress output.
	found := false
	for _, line := range progressLines {
		if strings.Contains(line, "operator must run 'p4 login'") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected classified auth error in progress lines, got: %v", progressLines)
	}
}

// The exact preempt argv, and that it PRECEDES the sync. The discriminating
// input is an include at #head: a mutant that passes the literal revision
// through emits "#head" where the resolved "@12345" belongs, and a preempt at
// the wrong revision does not merely fail to exclude - p4 syncs a file whose
// have-revision differs in EITHER direction, so it fetches the whole subtree.
func TestProvider_AnExclusionIsPreemptedAtItsCoveringIncludesResolvedRevision(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	pf := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/...", Rev: "#head"},
			{Path: "//s/x/heavy/...", Exclude: true},
		},
	}
	client := expectedClientName("h", SourceKey(pf))
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -m1 //"+client+"/...#head", "Change 12345 on 2026-09-04 by relay@h ...\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.setStream("-c "+client+" sync -k //"+client+"/heavy/...@12345", "//x/heavy/a.ma#1 - added\n")
	fr.setStream("-c "+client+" sync --parallel=4 //"+client+"/...@12345", "1 of 1 files\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	var lines []string
	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}},
		func(s string) { lines = append(lines, s) })
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	preemptAt, syncAt := -1, -1
	for i, c := range fr.argHistory() {
		if len(c) >= 4 && c[2] == "sync" && c[3] == "-k" {
			preemptAt = i
			require.Equal(t, []string{"-c", client, "sync", "-k", "//" + client + "/heavy/...@12345"}, c,
				"the preempt runs at the covering include RESOLVED revision")
		}
		if len(c) >= 4 && c[2] == "sync" && c[3] == "--parallel=4" {
			syncAt = i
		}
	}
	require.NotEqual(t, -1, preemptAt, "expected a sync -k invocation")
	require.Less(t, preemptAt, syncAt, "the preempt must precede the sync or it excludes nothing")

	// The excluded path must NOT reach the real sync argv, and must NOT be
	// recorded as synced: Request.SyncPaths feeds Workspace.syncedPaths, and
	// putting an excluded path there asserts content is present that is
	// deliberately absent.
	for _, c := range fr.argHistory() {
		if len(c) >= 4 && c[3] == "--parallel=4" {
			require.NotContains(t, strings.Join(c, " "), "/heavy/")
		}
	}

	// One count line, then one line per exclusion rendering the DEPOT path with
	// %q and LAST, following syncSummary rule: a forged path cannot spell a
	// convincing line of its own.
	require.Contains(t, lines, "[sync] excluding: 1 path(s)")
	require.Contains(t, lines, `[sync] exclude "//s/x/heavy/..."`)
}

// Every workspace-identity site must read the same key. A site left on pf.Stream
// puts an excluded task into the unexcluding task workspace, which is the
// poisoning hazard this design exists to close.
func TestProvider_EveryWorkspaceIdentitySiteUsesTheSameKey(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	pf := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/...", Rev: "@100"},
			{Path: "//s/x/heavy/...", Exclude: true},
		},
	}
	key := SourceKey(pf)
	client := expectedClientName("h", key)
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.setStream("-c "+client+" sync -k //"+client+"/heavy/...@100", "//x/heavy/a.ma#1 - added\n")
	fr.setStream("-c "+client+" sync --parallel=4 //"+client+"/...@100", "1 of 1 files\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	inv := h.Inventory()
	require.Equal(t, key, inv.SourceKey, "the handle and the registry row carry the composite key")
	require.Equal(t, allocateShortID(key, &Registry{}), inv.ShortID,
		"the short id is derived from the key, not from the bare stream")

	reg, err := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	require.NoError(t, err)
	e, ok := reg.GetBySourceKey(key)
	require.True(t, ok, "GetBySourceKey must find the row the Upsert wrote")
	require.Equal(t, inv.ShortID, e.ShortID)
}

// p4 exits ZERO when a filespec matches nothing, so a silently inert exclusion
// is indistinguishable from a working one and the volume fills. Refusing costs
// a false refusal on a legitimately-empty subtree; that trade is taken, and the
// operator escape is to delete an exclusion that was doing nothing anyway.
func TestProvider_APreemptThatMatchedNothingFailsThePrepare(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	pf := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/...", Rev: "@100"},
			{Path: "//s/x/typo/...", Exclude: true},
		},
	}
	client := expectedClientName("h", SourceKey(pf))
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.setStream("-c "+client+" sync -k //"+client+"/typo/...@100", "")
	fr.setStreamStderr("-c "+client+" sync -k //"+client+"/typo/...@100",
		"//"+client+"/typo/... - no such file(s).\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	var lines []string
	_, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}},
		func(s string) { lines = append(lines, s) })

	require.Error(t, err)
	require.ErrorContains(t, err, "//s/x/typo/...")
	// The cause travels on the returned error and is NOT repeated on a progress
	// line, the convention TestProvider_ASyncFailureProgressLineDoesNotRepeatTheCause
	// already pins for the sync branch.
	for _, l := range lines {
		require.NotContains(t, l, "no such file")
	}
	// No sync may have run: an inert exclusion followed by a full sync is the
	// exact outcome the refusal exists to prevent.
	for _, c := range fr.argHistory() {
		require.NotContains(t, strings.Join(c, " "), "--parallel=4")
	}
}

// A preempt reporting nothing at all is an ALREADY-EXCLUDED subtree on a warm
// workspace, which is success. Reading zero output as failure would refuse every
// prepare after the first.
func TestProvider_APreemptReportingUpToDateSucceeds(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	pf := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/...", Rev: "@100"},
			{Path: "//s/x/heavy/...", Exclude: true},
		},
	}
	client := expectedClientName("h", SourceKey(pf))
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.setStream("-c "+client+" sync -k //"+client+"/heavy/...@100", "")
	fr.setStreamStderr("-c "+client+" sync -k //"+client+"/heavy/...@100",
		"//"+client+"/heavy/... - file(s) up-to-date.\n")
	fr.setStream("-c "+client+" sync --parallel=4 //"+client+"/...@100", "1 of 1 files\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})
	require.NoError(t, err, "zero per-file lines is success, not emptiness")
	require.NoError(t, h.Finalize(context.Background()))
}

// Request.SyncPaths feeds Workspace.syncedPaths, which is what a LATER holder
// reads to decide whether it must re-sync exclusively. Recording an excluded
// path there asserts content is present that is deliberately absent, so the
// next task asking for that subtree is admitted shared into a workspace that
// does not hold it.
//
// The argv assertions elsewhere cannot see this: syncSpecs (the p4 argv) and
// syncPaths (this record) are built as two separate slices, so an excluded
// path can be absent from one and present in the other. Finalize is called
// before the read because Workspace.release is what populates the map.
func TestProvider_AnExcludedPathIsNotRecordedAsSynced(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	pf := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/...", Rev: "@100"},
			{Path: "//s/x/heavy/...", Exclude: true},
		},
	}
	client := expectedClientName("h", SourceKey(pf))
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.setStream("-c "+client+" sync -k //"+client+"/heavy/...@100", "//x/heavy/a.ma#1 - added\n")
	fr.setStream("-c "+client+" sync --parallel=4 //"+client+"/...@100", "1 of 1 files\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})
	require.NoError(t, err)
	require.NoError(t, h.Finalize(context.Background()))

	p.mu.Lock()
	ws := p.workspaces[h.Inventory().ShortID]
	p.mu.Unlock()
	require.NotNil(t, ws)
	ws.mu.Lock()
	synced := make([]string, 0, len(ws.syncedPaths))
	for path := range ws.syncedPaths {
		synced = append(synced, path)
	}
	ws.mu.Unlock()

	require.Contains(t, synced, "//s/x/...", "the include must be recorded as synced")
	require.NotContains(t, synced, "//s/x/heavy/...",
		"an excluded path was never transferred and must not be recorded as synced")
}
