package perforce

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// TestEvictWorkspace_InvalidatesPerTaskState verifies that a manual eviction
// drops the in-memory *Workspace (and its syncedPaths) for the evicted short
// ID, matching the background sweeper's OnEvictedCB behavior.
func TestEvictWorkspace_InvalidatesPerTaskState(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	fr.set("client -d relay_h_ws1", "Client deleted.\n")

	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: fr}})

	// Seed the on-disk registry with a workspace to evict.
	reg, err := p.Registry()
	require.NoError(t, err)
	reg.Upsert(WorkspaceEntry{
		ShortID:    "ws1",
		SourceKey:  "//s/x",
		ClientName: "relay_h_ws1",
		LastUsedAt: time.Now(),
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ws1"), 0o755))

	// Seed in-memory per-task state for ws1 so we can prove it gets cleared.
	p.mu.Lock()
	w := NewWorkspace("ws1")
	w.syncedPaths = map[string]string{"//s/x/...": "baseline-abc"}
	p.workspaces["ws1"] = w
	p.mu.Unlock()

	require.NoError(t, p.EvictWorkspace(context.Background(), "ws1"))

	// The in-memory workspace entry (with syncedPaths) must be gone.
	p.mu.Lock()
	_, present := p.workspaces["ws1"]
	p.mu.Unlock()
	require.False(t, present, "EvictWorkspace must invalidate per-task state for the evicted short ID")
}

// TestEvictWorkspace_PrepareRefusedWhileReserved verifies the atomic claim:
// once EvictWorkspace has reserved a short ID, a concurrent Prepare for that
// same stream is refused (cannot acquire the workspace mid-evict). We drive
// this deterministically by reserving the short ID directly under the lock
// (the same state EvictWorkspace sets) and asserting Prepare refuses.
func TestEvictWorkspace_PrepareRefusedWhileReserved(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: fr}})

	// Resolve the short ID Prepare would allocate for this stream.
	reg, err := p.Registry()
	require.NoError(t, err)
	shortID := allocateShortID("//depot/main", reg)

	// Simulate an in-flight eviction holding the reservation.
	p.mu.Lock()
	p.evicting[shortID] = true
	p.mu.Unlock()

	spec := &relayv1.SourceSpec{
		Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{
				Stream: "//depot/main",
				Sync:   []*relayv1.SyncEntry{{Path: "//depot/main/...", Rev: "@1"}},
			},
		},
	}
	_, err = p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.Error(t, err, "Prepare must refuse a short ID reserved for eviction")
	require.Contains(t, err.Error(), "being evicted")
}

// TestEvictWorkspace_RefusesHeldWorkspace verifies the inline holder check:
// when a workspace currently has a holder, EvictWorkspace returns an error and
// does not attempt to delete the p4 client or the on-disk directory.
func TestEvictWorkspace_RefusesHeldWorkspace(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t) // no client -d fixture: any DeleteClient call fails the test
	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: fr}})

	reg, err := p.Registry()
	require.NoError(t, err)
	reg.Upsert(WorkspaceEntry{
		ShortID:    "ws2",
		SourceKey:  "//s/y",
		ClientName: "relay_h_ws2",
		LastUsedAt: time.Now(),
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ws2"), 0o755))

	// Put a live holder on the workspace.
	p.mu.Lock()
	w := NewWorkspace("ws2")
	p.workspaces["ws2"] = w
	p.mu.Unlock()
	h, err := w.Acquire(context.Background(), Request{SyncPaths: []string{"//s/y/..."}})
	require.NoError(t, err)
	defer h.Release()

	err = p.EvictWorkspace(context.Background(), "ws2")
	require.Error(t, err, "EvictWorkspace must refuse a workspace that is currently held")
	require.Contains(t, err.Error(), "currently in use")

	// The on-disk directory must still exist (eviction never ran).
	_, statErr := os.Stat(filepath.Join(root, "ws2"))
	require.NoError(t, statErr, "held workspace directory must not be removed")
}
