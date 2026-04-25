//go:build integration

package perforce

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	if out, err := exec.Command("p4", "info").CombinedOutput(); err != nil {
		t.Skipf("p4 server at %s is unreachable: %v\n%s", p4port, err, out)
	}

	root := t.TempDir()
	prov := New(Config{Root: root, Hostname: "ci"})

	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//test/main",
			Sync:   []*relayv1.SyncEntry{{Path: "//test/main/...", Rev: "#head"}},
		},
	}}

	// Wire in shelved CL if provided.
	if shelved := os.Getenv("P4_TEST_SHELVED_CL"); shelved != "" {
		if cl, err := strconv.ParseInt(strings.TrimSpace(shelved), 10, 64); err == nil && cl > 0 {
			spec.GetPerforce().Unshelves = []int64{cl}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// --- First prepare: creates workspace, syncs to head ---
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
