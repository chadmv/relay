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
	e := r2.Get("a")
	require.NotNil(t, e)
	require.Len(t, e.OpenTaskChangelists, 1)
	require.Equal(t, int64(91244), e.OpenTaskChangelists[0].PendingCL)

	require.NoError(t, r2.RemovePendingCL("a", "task1"))
	require.NoError(t, r2.MarkDirtyDelete("a", true))
	require.NoError(t, r2.Save())

	r3, _ := LoadRegistry(path)
	e = r3.Get("a")
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
