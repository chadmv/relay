package perforce

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

func TestProvider_PrepareCreatesClientAndSyncs(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture()
	expectedClient := expectedClientName("h", "//s/x")
	// ResolveHead: "changes -m1 //s/x/...#head" → CL 12345
	fr.set("changes -m1 //s/x/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	// CreateStreamClient: "client -i" succeeds; "client -o -S //s/x <name>" returns empty (ok)
	fr.set("client -i", "Client saved.\n")
	// SyncStream: now invoked with global -c <client>.
	fr.setStream("-c "+expectedClient+" sync -q --parallel=4 //s/x/...@12345", "1 of 1 files\n")

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
}

func TestProvider_UnshelveAndFinalizeRevert(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture()
	expectedClient := expectedClientName("h", "//s/x")
	fr.set("changes -m1 //s/x/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	fr.set("client -i", "Client saved.\n")
	fr.setStream("-c "+expectedClient+" sync -q --parallel=4 //s/x/...@12345", "1 of 1 files\n")
	fr.set("-c "+expectedClient+" change -o", "Change: new\nDescription:\t<enter description here>\n")
	fr.set("-c "+expectedClient+" change -i", "Change 91244 created.\n")
	fr.set("unshelve -s 12346 -c 91244", "//s/x/foo - unshelved\n")
	fr.set("revert -c 91244 //...", "//s/x/foo - reverted\n")
	fr.set("change -d 91244", "Change 91244 deleted.\n")

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
	require.True(t, found([]string{"unshelve", "-s", "12346", "-c", "91244"}))
	require.True(t, found([]string{"revert", "-c", "91244", "//..."}))
	require.True(t, found([]string{"change", "-d", "91244"}))

	// Registry must be clean after Finalize.
	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	e := reg.GetBySourceKey("//s/x")
	require.NotNil(t, e)
	require.Empty(t, e.OpenTaskChangelists)
}

func TestProvider_CrashRecovery_DeletesOrphanedPendingCLs(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture()

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

	fr.set("changes -m1 //s/x/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	fr.set("changes -c "+clientName+" -s pending -l",
		"Change 91244 on 2026-04-24 by relay@h *pending*\n\trelay-task-old\n\nChange 99999 on 2026-04-24 by other@h *pending*\n\thuman work\n")
	fr.set("revert -c 91244 //...", "//... - reverted\n")
	fr.set("change -d 91244", "Change 91244 deleted.\n")
	fr.setStream("sync -q --parallel=4 //s/x/...@12345", "ok\n")

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
	require.True(t, found([]string{"revert", "-c", "91244", "//..."}))
	require.True(t, found([]string{"change", "-d", "91244"}))
	// Must NOT touch CL 99999 (not relay-owned)
	require.False(t, found([]string{"change", "-d", "99999"}))
}

func TestProvider_RegistryReturnsSharedInstance(t *testing.T) {
	root := t.TempDir()
	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: newFakeP4Fixture()}})

	r1, err := p.Registry()
	require.NoError(t, err)
	r2, err := p.Registry()
	require.NoError(t, err)
	require.Same(t, r1, r2, "Registry() must return the same cached instance")
}
