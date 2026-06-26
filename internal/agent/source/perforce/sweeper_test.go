package perforce

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFakeRunner_BlockHookHonorsCtxCancel(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.setBlock("client -d relay_h_x")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := fr.Run(ctx, "", []string{"client", "-d", "relay_h_x"}, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 2*time.Second, "block hook must unblock on ctx deadline")
}

func TestSweeper_EvictTimesOutOnHangingDeleteClient(t *testing.T) {
	prev := evictTimeout
	evictTimeout = 50 * time.Millisecond
	defer func() { evictTimeout = prev }()

	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	fr.setBlock("client -d relay_h_stuck")

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{ShortID: "stuck", SourceKey: "//s/x",
		ClientName: "relay_h_stuck", LastUsedAt: time.Now().Add(-30 * 24 * time.Hour)})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "stuck"), 0o755))

	s := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     &Client{r: fr},
		ListLocked: func() map[string]bool { return nil },
	}

	done := make(chan struct{})
	var evicted []string
	var err error
	go func() {
		evicted, err = s.SweepOnce(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SweepOnce did not return; per-eviction timeout not enforced")
	}

	require.NoError(t, err, "a timed-out eviction is logged-and-skipped, not a SweepOnce error")
	require.Empty(t, evicted, "the stuck workspace must not be reported as evicted")

	// os.RemoveAll was never reached: the directory still exists.
	_, statErr := os.Stat(filepath.Join(root, "stuck"))
	require.NoError(t, statErr, "os.RemoveAll must not run when p4 client -d times out")

	// No DirtyDelete marker: the next sweep retries the FULL eviction.
	e, ok := reg.Get("stuck")
	require.True(t, ok, "the entry stays in the registry for retry")
	require.False(t, e.DirtyDelete, "a p4 timeout must NOT set DirtyDelete (dir untouched)")
}

func TestSweeper_ContinuesPastEvictTimeout(t *testing.T) {
	prev := evictTimeout
	evictTimeout = 50 * time.Millisecond
	defer func() { evictTimeout = prev }()

	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	// Oldest candidate hangs; newer candidate's client -d succeeds.
	fr.setBlock("client -d relay_h_stuck")
	fr.set("client -d relay_h_good", "Client deleted.\n")

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{ShortID: "stuck", SourceKey: "//s/stuck",
		ClientName: "relay_h_stuck", LastUsedAt: time.Now().Add(-40 * 24 * time.Hour)})
	reg.Upsert(WorkspaceEntry{ShortID: "good", SourceKey: "//s/good",
		ClientName: "relay_h_good", LastUsedAt: time.Now().Add(-30 * 24 * time.Hour)})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "stuck"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "good"), 0o755))

	s := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     &Client{r: fr},
		ListLocked: func() map[string]bool { return nil },
	}

	done := make(chan struct{})
	var evicted []string
	var err error
	go func() {
		evicted, err = s.SweepOnce(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SweepOnce wedged on the stuck candidate; pass did not continue")
	}

	require.NoError(t, err)
	require.Equal(t, []string{"good"}, evicted, "the second candidate must still be evicted")

	// good is fully gone; stuck remains (un-dirty) for a future retry.
	_, statErr := os.Stat(filepath.Join(root, "good"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
	_, ok := reg.Get("good")
	require.False(t, ok)
	stuckEntry, ok := reg.Get("stuck")
	require.True(t, ok)
	require.False(t, stuckEntry.DirtyDelete)
}

func TestSweeper_AgeEviction(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
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
	fr := newFakeP4Fixture(t)
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
	fr := newFakeP4Fixture(t)
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
	_, ok := reg.Get("old")
	require.False(t, ok)
}

func TestSweeper_SkipsLockedWorkspaces(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)

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

func TestSweeper_DirtyDeleteSkipsDeleteClient(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	// Deliberately register NO fixture for "client -d relay_h_dirty".
	// If evict calls DeleteClient, fakeRunner.Run will t.Errorf and fail.

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{
		ShortID:     "dirty",
		SourceKey:   "//s/x",
		ClientName:  "relay_h_dirty",
		LastUsedAt:  time.Now().Add(-30 * 24 * time.Hour),
		DirtyDelete: true, // client already deleted on a prior sweep
	})
	require.NoError(t, reg.Save())
	// Directory now exists and is removable (the transient RemoveAll failure cleared).
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dirty"), 0o755))

	s := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     &Client{r: fr},
		ListLocked: func() map[string]bool { return nil },
	}
	evicted, err := s.SweepOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"dirty"}, evicted)

	// Directory gone, entry gone, and p4 client -d was never called.
	_, statErr := os.Stat(filepath.Join(root, "dirty"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
	_, ok := reg.Get("dirty")
	require.False(t, ok)
	require.Empty(t, fr.argHistory(), "DeleteClient must be skipped for a DirtyDelete entry")
}

func TestSweeper_ContinuesPastEvictFailure(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	// Oldest entry: DeleteClient fails (simulates a still-present client that
	// cannot be deleted). Newer entry: DeleteClient succeeds.
	fr.setErr("client -d relay_h_bad", errors.New("p4 client -d relay_h_bad: boom"))
	fr.set("client -d relay_h_good", "Client deleted.\n")

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{ShortID: "bad", SourceKey: "//s/bad",
		ClientName: "relay_h_bad", LastUsedAt: time.Now().Add(-40 * 24 * time.Hour)})
	reg.Upsert(WorkspaceEntry{ShortID: "good", SourceKey: "//s/good",
		ClientName: "relay_h_good", LastUsedAt: time.Now().Add(-30 * 24 * time.Hour)})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bad"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "good"), 0o755))

	s := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     &Client{r: fr},
		ListLocked: func() map[string]bool { return nil },
	}
	evicted, err := s.SweepOnce(context.Background())
	require.NoError(t, err, "one bad entry must not abort the whole pass")
	require.Equal(t, []string{"good"}, evicted)

	// The good workspace is gone; the bad one remains for a future attempt.
	_, statErr := os.Stat(filepath.Join(root, "good"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
	_, ok := reg.Get("good")
	require.False(t, ok)
	_, ok = reg.Get("bad")
	require.True(t, ok, "the failed entry stays in the registry")
}
