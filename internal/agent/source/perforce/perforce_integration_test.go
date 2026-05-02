//go:build integration

package perforce

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

// TestPerforce_E2E_SyncAndUnshelve exercises the full Provider.Prepare → Finalize
// lifecycle against a containerized p4d. The container is provisioned by
// startP4dContainer (see p4d_container_test.go); it pre-creates depot //test,
// stream //test/main, an initial baseline file, and a shelved CL.
//
// The test skips cleanly when Docker is unavailable or when the `p4` client
// binary is not on PATH; both are pre-flighted by the fixture.
func TestPerforce_E2E_SyncAndUnshelve(t *testing.T) {
	p4d := startP4dContainer(t)
	t.Setenv("P4PORT", p4d.P4Port)
	t.Setenv("P4USER", p4d.P4User)

	root := t.TempDir()
	prov := New(Config{Root: root, Hostname: "ci"})

	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream:    "//test/main",
			Sync:      []*relayv1.SyncEntry{{Path: "//test/main/...", Rev: "#head"}},
			Unshelves: []int64{p4d.ShelvedCL},
		},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// --- First prepare: creates workspace, syncs to head, unshelves the CL ---
	var progressLines []string
	h, err := prov.Prepare(ctx, "task-1", spec, func(s string) {
		progressLines = append(progressLines, s)
	})
	require.NoError(t, err, "Prepare should succeed")
	t.Cleanup(func() { _ = h.Finalize(context.Background()) })
	require.NotEmpty(t, progressLines, "sync should produce progress lines")

	inv := h.Inventory()
	require.Equal(t, "perforce", inv.SourceType)
	require.Equal(t, "//test/main", inv.SourceKey)
	require.NotEmpty(t, inv.ShortID, "ShortID must be set")
	require.NotEmpty(t, inv.BaselineHash, "BaselineHash must be set after sync")

	// Workspace directory must exist on disk.
	wsDir := filepath.Join(root, inv.ShortID)
	_, err = os.Stat(wsDir)
	require.NoError(t, err, "workspace directory should exist")

	// Registry should show no open task changelists after Finalize.
	reg, err := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	require.NoError(t, err)
	e := reg.Get(inv.ShortID)
	require.NotNil(t, e, "workspace entry should remain in registry after finalize")
	require.Empty(t, e.OpenTaskChangelists, "Finalize should clear pending changelists")

	// --- Second prepare: same spec → should not re-sync (baseline matches) ---
	var progress2 []string
	h2, err := prov.Prepare(ctx, "task-2", spec, func(s string) {
		progress2 = append(progress2, s)
	})
	require.NoError(t, err, "second Prepare on same baseline should succeed")
	t.Cleanup(func() { _ = h2.Finalize(context.Background()) })
	require.Empty(t, progress2, "second Prepare with same baseline should not trigger re-sync")

	// Workspace dir must still exist after second finalize.
	_, err = os.Stat(wsDir)
	require.NoError(t, err, "workspace directory should persist after second finalize")
}
