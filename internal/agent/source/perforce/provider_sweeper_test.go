package perforce

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestProviderSweeper_CoherentWithoutInvalidateCallback verifies that when the
// sweeper shares the provider's *Registry directly, an eviction is visible to
// Provider.ListInventory without relying on OnEvictedCB to nil out p.reg.
func TestProviderSweeper_CoherentWithoutInvalidateCallback(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture()
	fr.set("client -d relay_h_stale", "Client deleted.\n")

	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: fr}})

	// Seed a stale workspace through the shared registry.
	reg, err := p.Registry()
	require.NoError(t, err)
	reg.Upsert(WorkspaceEntry{
		ShortID:    "stale",
		SourceKey:  "//s/x",
		ClientName: "relay_h_stale",
		LastUsedAt: time.Now().Add(-30 * 24 * time.Hour),
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "stale"), 0o755))

	// Build a sweeper sharing the same Reg, with NO OnEvictedCB.
	s := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     p.Client(),
		ListLocked: p.LockedShortIDs,
	}
	evicted, err := s.SweepOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"stale"}, evicted)

	// Provider.ListInventory must see the eviction without InvalidateWorkspace
	// having been called.
	inv, err := p.ListInventory()
	require.NoError(t, err)
	for _, e := range inv {
		require.NotEqual(t, "stale", e.ShortID, "evicted entry must not appear in inventory")
	}
}
