package perforce

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRegistry_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".relay-registry.json")

	r, err := LoadRegistry(path)
	require.NoError(t, err)
	require.Empty(t, r.Workspaces)

	r.Upsert(WorkspaceEntry{
		ShortID:      "abcdef",
		SourceKey:    "//s/x",
		ClientName:   "relay_h_abcdef",
		BaselineHash: "deadbeef",
		LastUsedAt:   time.Now(),
	})
	require.NoError(t, r.Save())

	r2, err := LoadRegistry(path)
	require.NoError(t, err)
	require.Len(t, r2.Workspaces, 1)
	require.Equal(t, "//s/x", r2.Workspaces[0].SourceKey)
}

func TestRegistry_TrackPendingCLAndDirtyDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".relay-registry.json")

	r, _ := LoadRegistry(path)
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", LastUsedAt: time.Now()})
	require.NoError(t, r.AddPendingCL("a", "task1", 91244))
	require.NoError(t, r.Save())

	r2, _ := LoadRegistry(path)
	e, ok := r2.Get("a")
	require.True(t, ok)
	require.Len(t, e.OpenTaskChangelists, 1)
	require.Equal(t, int64(91244), e.OpenTaskChangelists[0].PendingCL)

	require.NoError(t, r2.RemovePendingCL("a", "task1"))
	require.NoError(t, r2.MarkDirtyDelete("a", true))
	require.NoError(t, r2.Save())

	r3, _ := LoadRegistry(path)
	e, ok = r3.Get("a")
	require.True(t, ok)
	require.Empty(t, e.OpenTaskChangelists)
	require.True(t, e.DirtyDelete)
}

func TestRegistry_AtomicWrite(t *testing.T) {
	// Save writes to .tmp + rename. After Save, the temp file must not exist.
	dir := t.TempDir()
	path := filepath.Join(dir, ".relay-registry.json")
	r, _ := LoadRegistry(path)
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", LastUsedAt: time.Now()})
	require.NoError(t, r.Save())

	matches, _ := filepath.Glob(filepath.Join(dir, ".relay-registry.json.tmp*"))
	require.Empty(t, matches)
}

func TestRegistry_GetReturnsCopyNotPointer(t *testing.T) {
	r := &Registry{}
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", BaselineHash: "h1", LastUsedAt: time.Now()})

	got, ok := r.Get("a")
	require.True(t, ok)
	require.Equal(t, "h1", got.BaselineHash)

	// Mutating the returned copy must not touch registry memory.
	got.BaselineHash = "MUTATED"
	again, ok := r.Get("a")
	require.True(t, ok)
	require.Equal(t, "h1", again.BaselineHash)

	_, ok = r.Get("missing")
	require.False(t, ok)
}

func TestRegistry_GetBySourceKeyReturnsCopy(t *testing.T) {
	r := &Registry{}
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", LastUsedAt: time.Now()})

	got, ok := r.GetBySourceKey("//s/x")
	require.True(t, ok)
	require.Equal(t, "a", got.ShortID)

	_, ok = r.GetBySourceKey("//s/none")
	require.False(t, ok)
}

func TestRegistry_Mutate(t *testing.T) {
	r := &Registry{}
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", BaselineHash: "old", LastUsedAt: time.Now()})

	err := r.Mutate("a", func(e *WorkspaceEntry) { e.BaselineHash = "new" })
	require.NoError(t, err)

	got, ok := r.Get("a")
	require.True(t, ok)
	require.Equal(t, "new", got.BaselineHash)

	err = r.Mutate("missing", func(e *WorkspaceEntry) {})
	require.Error(t, err)
}

func TestRegistry_SnapshotIsIndependentCopy(t *testing.T) {
	r := &Registry{}
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", LastUsedAt: time.Now()})

	snap := r.Snapshot()
	require.Len(t, snap, 1)

	// Appending to / mutating the snapshot must not affect the registry.
	snap[0].SourceKey = "MUTATED"
	snap = append(snap, WorkspaceEntry{ShortID: "b"})

	got, ok := r.Get("a")
	require.True(t, ok)
	require.Equal(t, "//s/x", got.SourceKey)
	require.Len(t, r.Snapshot(), 1)
}

func TestRegistry_ShortIDInUse(t *testing.T) {
	r := &Registry{}
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", LastUsedAt: time.Now()})

	// Same shortID, different sourceKey -> collision.
	require.True(t, r.ShortIDInUse("a", "//s/y"))
	// Same shortID, same sourceKey -> not a collision (it's the same workspace).
	require.False(t, r.ShortIDInUse("a", "//s/x"))
	// Unknown shortID -> free.
	require.False(t, r.ShortIDInUse("z", "//s/y"))
}
