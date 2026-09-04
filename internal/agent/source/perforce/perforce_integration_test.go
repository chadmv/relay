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
	// Defense in depth against host-leaked credentials. The fixture's p4d
	// runs at security level 0 (no auth required), so leaked tickets
	// won't actively break the test, but neutralizing them removes one
	// more variable from "why did this fail on developer X's box?".
	t.Setenv("P4PASSWD", "")
	t.Setenv("P4TICKETS", "")

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
	// This is the only place in the slice where real p4 is the producer, and it
	// pins both halves of the -q removal at once: some file lines reached
	// onLine, so the count in the completion bracket is non-zero, and none of
	// that text reached progress. A fakeRunner echoes whatever it is told and
	// can prove neither.
	var progress1 []string
	h, err := prov.Prepare(ctx, "task-1", spec, func(s string) {
		t.Logf("prepare-progress: %s", s)
		progress1 = append(progress1, s)
	})
	require.NoError(t, err, "Prepare should succeed")

	// The one-file baseline produces exactly one file line and no totals line.
	require.Len(t, progress1, 2, "the two brackets and nothing else, got: %v", progress1)
	require.Equal(t, 1, countLinesContaining(progress1, "1 files; 0 other lines"),
		"real p4 wrote one file line and the counter saw it, got: %v", progress1)
	// The depot path IS carried, as the summary's trailing field, so its
	// presence proves nothing. What a forwarded p4 file line would carry and a
	// summary never can is the rev separator: syncLineDepotPath cuts the path at
	// the first '#'.
	for _, l := range progress1 {
		require.NotContains(t, l, "#",
			"p4's per-file text must not reach progress, got: %q", l)
	}

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
	e, ok := reg.Get(inv.ShortID)
	require.True(t, ok, "workspace entry should remain in registry after finalize")
	require.Empty(t, e.OpenTaskChangelists, "Finalize should clear pending changelists")

	// --- Second prepare: same spec, on a workspace already at that baseline ---
	// This spec carries an unshelve, so tryAdmit's needsExclusive arm hands out
	// ModeExclusive on every Prepare and needsSync is true regardless of the
	// baseline: the second Prepare DOES run p4 sync, and it is a no-op.
	// Asserting the progress callback saw nothing still cannot express "no
	// re-sync", and the reason has changed: p4 reports an up-to-date client on
	// STDERR, which execRunner.Stream discards on a zero exit
	// (docs/backlog/bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero.md).
	// So do not assert on a file count here - 0 files reads the same before and
	// after this change, and is equally the correct reading for a legitimately
	// empty subtree. The bracket lines are what make the sync observable.
	var progress2 []string
	h2, err := prov.Prepare(ctx, "task-2", spec, func(s string) {
		progress2 = append(progress2, s)
	})
	require.NoError(t, err, "second Prepare on same baseline should succeed")
	// The count is part of the property: the per-phrase assertions below admit
	// arbitrary extra output on their own. Keep both halves. It stays at 2, and
	// now rests on two further facts - the summary rides on the existing
	// [sync] complete bracket rather than adding a third line, and the 30s
	// default heartbeat cannot fire during a sub-second no-op sync. Do not
	// loosen it to a range: it is the end-to-end assertion that per-file output
	// does not reach progress.
	require.Len(t, progress2, 2, "the two brackets and nothing else, got: %v", progress2)
	require.Equal(t, 1, countLinesContaining(progress2, "[sync] starting"),
		"the second Prepare syncs under exclusive mode, got: %v", progress2)
	require.Equal(t, 1, countLinesContaining(progress2, "[sync] complete"),
		"and it completes, got: %v", progress2)
	require.Equal(t, 0, countLinesContaining(progress2, "[recover]"),
		"no crash-recovery path may fire here, got: %v", progress2)
	require.NoError(t, h2.Finalize(ctx), "second Finalize should succeed")

	// Workspace dir must still exist after second finalize.
	_, err = os.Stat(wsDir)
	require.NoError(t, err, "workspace directory should persist after second finalize")
}
