//go:build integration

package perforce

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

// TestPerforce_E2E_SyncAndUnshelve exercises the full Provider.Prepare → Finalize
// lifecycle against a real Perforce server.
//
// Required env vars:
//
//	P4_TEST_HOST — host:port of a running P4 server (e.g. "localhost:1666")
//	P4_TEST_USER — P4 user with access to //test/main (defaults to current P4USER)
//	P4_TEST_SHELVED_CL — (optional) changelist number with shelved files to unshelve
//
// The server must have a stream depot with stream "//test/main".
func TestPerforce_E2E_SyncAndUnshelve(t *testing.T) {
	p4port := os.Getenv("P4_TEST_HOST")
	if p4port == "" {
		t.Skip("set P4_TEST_HOST=host:port to run Perforce integration tests; assumes //test/main stream exists")
	}

	t.Setenv("P4PORT", p4port)
	if user := os.Getenv("P4_TEST_USER"); user != "" {
		t.Setenv("P4USER", user)
	}

	// Verify the P4 server is reachable before doing anything.
	if err := exec.Command("p4", "info").Run(); err != nil {
		t.Skipf("p4 server at %s is unreachable: %v", p4port, err)
	}

	root := t.TempDir()
	prov := New(Config{Root: root, Hostname: "ci-integration"})

	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//test/main",
			Sync:   []*relayv1.SyncEntry{{Path: "//test/main/...", Rev: "#head"}},
		},
	}}

	// Wire in shelved CL if provided.
	if shelved := os.Getenv("P4_TEST_SHELVED_CL"); shelved != "" {
		var cl int64
		if _, err := fmt.Sscanf(shelved, "%d", &cl); err == nil && cl > 0 {
			spec.GetPerforce().Unshelves = []int64{cl}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// --- First prepare: creates workspace, syncs to head ---
	var progressLines []string
	h, err := prov.Prepare(ctx, "task-e2e-1", spec, func(s string) {
		progressLines = append(progressLines, s)
	})
	require.NoError(t, err, "Prepare should succeed")
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

	// Finalize should revert any unshelved files and release the lock.
	require.NoError(t, h.Finalize(ctx), "Finalize should succeed")

	// Registry should show no open task changelists after Finalize.
	reg, err := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	require.NoError(t, err)
	e := reg.Get(inv.ShortID)
	require.NotNil(t, e, "workspace entry should remain in registry after finalize")
	require.Empty(t, e.OpenTaskChangelists, "Finalize should clear pending changelists")

	// --- Second prepare: same spec → should not re-sync (baseline matches) ---
	h2, err := prov.Prepare(ctx, "task-e2e-2", spec, func(string) {})
	require.NoError(t, err, "second Prepare on same baseline should succeed")
	require.NoError(t, h2.Finalize(ctx), "second Finalize should succeed")

	// Workspace dir must still exist after second finalize.
	_, err = os.Stat(wsDir)
	require.NoError(t, err, "workspace directory should persist after second finalize")
}
