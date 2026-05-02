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
	// Override host-side P4 environment that may be persisted via `p4 set` so
	// the test isolates from operator config. Without these, a developer
	// running the test on a workstation with a unicode-mode `p4` client or
	// a previously-set P4CLIENT will see the wrong client/charset get
	// inherited by the agent's p4 subprocess calls.
	t.Setenv("P4CHARSET", "none")
	t.Setenv("P4CONFIG", "")
	// The agent creates a stream-bound client named relay_<hostname>_<shortid>
	// where shortid = first 6 chars of lowercase base32(sha256(stream)). Compute
	// the same value here and inject it as P4CLIENT so the agent's `p4 sync`
	// (which the production code currently relies on env to provide; see
	// client.go's "Caller is responsible for setting P4CLIENT" comment) finds
	// the right client.
	t.Setenv("P4CLIENT", expectedClientName("ci", "//test/main"))

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
	// Note: progress callback is not asserted on here. Production code runs
	// `p4 sync -q` which suppresses per-file output entirely; with the
	// fixture's single readme.txt baseline, the sync emits zero lines on
	// success. We retain the callback to surface unexpected `[recover] ...`
	// diagnostic lines in test output if a crash-recovery path fires.
	h, err := prov.Prepare(ctx, "task-1", spec, func(s string) {
		t.Logf("prepare-progress: %s", s)
	})
	require.NoError(t, err, "Prepare should succeed")

	inv := h.Inventory()
	require.Equal(t, "perforce", inv.SourceType)
	require.Equal(t, "//test/main", inv.SourceKey)
	require.NotEmpty(t, inv.ShortID, "ShortID must be set")
	require.NotEmpty(t, inv.BaselineHash, "BaselineHash must be set after sync")

	// Workspace directory must exist on disk.
	wsDir := filepath.Join(root, inv.ShortID)
	_, err = os.Stat(wsDir)
	require.NoError(t, err, "workspace directory should exist")

	// Finalize must run before checking the registry: the unshelve created a
	// pending CL that's only cleared in Finalize. Call it explicitly here
	// rather than via t.Cleanup so the assertions below see the post-Finalize
	// state.
	require.NoError(t, h.Finalize(ctx), "Finalize should succeed")

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
	require.Empty(t, progress2, "second Prepare with same baseline should not trigger re-sync")
	require.NoError(t, h2.Finalize(ctx), "second Finalize should succeed")

	// Workspace dir must still exist after second finalize.
	_, err = os.Stat(wsDir)
	require.NoError(t, err, "workspace directory should persist after second finalize")
}
