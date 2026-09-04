package perforce

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// prepareFailingAtHeadResolution drives one Prepare that creates the workspace
// and then fails resolving head, and returns the provider, its registry, the
// workspace root, the short id and the client name. Head resolution is where the
// injection goes because it is the first call after creation that carries a
// job-supplied path, so it is the realistic first-use failure.
func prepareFailingAtHeadResolution(t *testing.T) (*Provider, *Registry, string, string, string) {
	t.Helper()
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	client := expectedClientName("h", "//s/x")
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("client -d "+client, "Client deleted.\n")
	fr.setErr("-c "+client+" changes -m1 //"+client+"/...#head", errors.New("no such file(s)."))

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	_, err := p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.Error(t, err, "the injected head-resolution failure must fail Prepare")

	reg, regErr := p.Registry()
	require.NoError(t, regErr)
	shortID := allocateShortID("//s/x", &Registry{})
	return p, reg, root, shortID, client
}

// A Prepare that dies at head resolution has created a workspace directory and a
// p4 client spec, and neither is reachable by anything but the registry: the
// sweeper's only input is reg.Snapshot(). The assertions are ORDERED so that the
// one which fails names which tree is under test - a tree that creates nothing
// before head resolution dies on the directory, and a tree that creates without
// registering dies on reg.Get.
func TestProvider_AResolveHeadFailureOnFirstUseLeavesAReclaimableWorkspace(t *testing.T) {
	p, reg, root, shortID, client := prepareFailingAtHeadResolution(t)

	require.DirExists(t, filepath.Join(root, shortID),
		"the workspace directory must exist after the failed Prepare")

	e, ok := reg.Get(shortID)
	require.True(t, ok, "and the registry must have an entry for it")
	require.Equal(t, "", e.BaselineHash, "registered before any sync, so no baseline")
	require.Equal(t, client, e.ClientName)

	require.NoError(t, reg.Mutate(shortID, func(w *WorkspaceEntry) {
		w.LastUsedAt = time.Now().Add(-30 * 24 * time.Hour)
	}))
	sw := &Sweeper{
		Root:        root,
		Reg:         reg,
		MaxAge:      14 * 24 * time.Hour,
		Client:      p.Client(),
		ListLocked:  p.LockedShortIDs,
		Claim:       p.ReserveForEvict,
		OnEvictedCB: p.InvalidateWorkspace,
	}
	evicted, err := sw.SweepOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{shortID}, evicted, "a configured sweeper must reclaim it")
	require.NoDirExists(t, filepath.Join(root, shortID))
}

// The acceptance criterion stated verbatim: nothing under the workspace root is
// invisible to the registry. It is a regression guard, not a red-first
// criterion - it is satisfied where nothing is created at all as readily as
// where everything created is registered. Deleting the registration in Prepare
// is what makes it fail.
func TestProvider_AFailedPrepareLeavesNoUnregisteredWorkspaceDirectory(t *testing.T) {
	_, reg, root, _, _ := prepareFailingAtHeadResolution(t)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		_, ok := reg.Get(d.Name())
		require.Truef(t, ok, "directory %q under the workspace root has no registry entry", d.Name())
	}
}

// The in-memory registry dies with the process. What makes the workspace
// reclaimable after a crash - the case the directory-plus-client leak is about -
// is that the registration reached .relay-registry.json, so the next agent's
// LoadRegistry sees it. A fresh Registry read from disk is the only instrument
// that can tell the two apart.
func TestProvider_TheFirstUseRegistrationReachesDisk(t *testing.T) {
	_, _, root, shortID, client := prepareFailingAtHeadResolution(t)

	onDisk, err := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	require.NoError(t, err)
	e, ok := onDisk.Get(shortID)
	require.True(t, ok, "the registration must be readable by the next agent process")
	require.Equal(t, client, e.ClientName)
}
