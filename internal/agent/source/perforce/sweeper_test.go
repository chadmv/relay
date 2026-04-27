package perforce

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSweeper_AgeEviction(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture()
	fr.set("client -d relay_h_old", "Client deleted.\n")

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{ShortID: "old", SourceKey: "//s/x",
		ClientName: "relay_h_old", LastUsedAt: time.Now().Add(-30 * 24 * time.Hour)})
	reg.Upsert(WorkspaceEntry{ShortID: "fresh", SourceKey: "//s/y",
		ClientName: "relay_h_fresh", LastUsedAt: time.Now()})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "old"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "fresh"), 0o755))

	s := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     &Client{r: fr},
		ListLocked: func() map[string]bool { return nil },
	}
	evicted, err := s.SweepOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"old"}, evicted)

	_, err = os.Stat(filepath.Join(root, "old"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(root, "fresh"))
	require.NoError(t, err)
}

func TestSweeper_PressureEviction(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture()
	fr.set("client -d relay_h_a", "Client deleted.\n")

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/a",
		ClientName: "relay_h_a", LastUsedAt: time.Now().Add(-5 * time.Hour)})
	reg.Upsert(WorkspaceEntry{ShortID: "b", SourceKey: "//s/b",
		ClientName: "relay_h_b", LastUsedAt: time.Now().Add(-1 * time.Hour)})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "b"), 0o755))

	var freeGB int64 = 50
	s := &Sweeper{
		Root: root, Reg: reg, MinFreeGB: 100, Client: &Client{r: fr},
		FreeDiskGB:  func(string) (int64, error) { return freeGB, nil },
		ListLocked:  func() map[string]bool { return nil },
		OnEvictedCB: func(string) { freeGB = 200 },
	}
	evicted, err := s.SweepOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, evicted)
}

func TestSweeper_UsesInjectedRegistry(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture()
	fr.set("client -d relay_h_old", "Client deleted.\n")

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{ShortID: "old", SourceKey: "//s/x",
		ClientName: "relay_h_old", LastUsedAt: time.Now().Add(-30 * 24 * time.Hour)})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "old"), 0o755))

	s := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     &Client{r: fr},
		ListLocked: func() map[string]bool { return nil },
	}
	evicted, err := s.SweepOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"old"}, evicted)

	// The eviction must be visible directly on the injected registry pointer.
	require.Nil(t, reg.Get("old"))
}

func TestSweeper_SkipsLockedWorkspaces(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture()

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{ShortID: "locked", SourceKey: "//s/x",
		ClientName: "relay_h_locked", LastUsedAt: time.Now().Add(-30 * 24 * time.Hour)})
	require.NoError(t, reg.Save())

	s := &Sweeper{
		Root: root, Reg: reg, MaxAge: 14 * 24 * time.Hour, Client: &Client{r: fr},
		ListLocked: func() map[string]bool { return map[string]bool{"locked": true} },
	}
	evicted, err := s.SweepOnce(context.Background())
	require.NoError(t, err)
	require.Empty(t, evicted)
	require.Empty(t, fr.argHistory(), "must not call p4 on locked workspaces")
}
