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

// assertCwdContract pins the cwd half of the client-selection contract: a p4
// invocation runs from wsRoot only while Prepare holds the workspace handle for
// it. The client-selection half is the `-c <client>` argv, pinned separately.
//
// Head resolution is the exception because it runs BEFORE ws.Acquire, when the
// workspace has no holders and a sweep can be deleting it; it runs with an empty
// cwd like the client -o/-i/-d spec operations.
// TestProvider_HeadResolutionRunsWithNoWorkspaceCwd is its own guard.
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
	fr.setStream("-c "+client+" sync -q --parallel=4 //"+client+"/...@12345", "1 of 1 files\n")

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
	fr.setStream("-c "+expectedClient+" sync -q --parallel=4 //"+expectedClient+"/...@12345", "1 of 1 files\n")

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
	require.NotEmpty(t, lines, "sync stream should have produced progress lines")

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
	fr.setStream("-c "+expectedClient+" sync -q --parallel=4 //"+expectedClient+"/...@12345", "1 of 1 files\n")
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
	fr.setStream("-c "+clientName+" sync -q --parallel=4 //"+clientName+"/...@12345", "ok\n")

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
	// Head resolution is the first p4 call carrying a job-supplied path, which is
	// what makes it the realistic place for a ticket failure to surface. Inject
	// the canonical "ticket invalid" stderr that execRunner would surface in
	// production.
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
	fr.setStream("-c "+expectedClient+" sync -q --parallel=4 //"+expectedClient+"/...@12345", "")

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
