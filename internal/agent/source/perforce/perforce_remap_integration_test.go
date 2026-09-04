//go:build integration

package perforce

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

// A stream whose client view remaps its parent's storage has no depot storage
// under its own name: `p4 files //test/virt/...` reports no such file(s), so
// addressing p4 by the stream-name depot path resolves nothing. The remapped
// on-disk path below is reachable only through the client view, which is why it
// is the assertion - a fake runner echoes whatever it is told and can say
// nothing about this.
//
// The content is compared after trimming: the depot file is text, so a p4
// client with LineEnd local translates it on sync and the bytes on disk differ
// by platform.
func TestPerforce_E2E_VirtualStreamWithARemapSyncsIntoTheRemappedLayout(t *testing.T) {
	p4d := startP4dContainer(t)
	t.Setenv("P4PORT", p4d.P4Port)
	t.Setenv("P4USER", p4d.P4User)
	t.Setenv("P4CHARSET", "none")
	t.Setenv("P4CONFIG", "")
	t.Setenv("P4PASSWD", "")
	t.Setenv("P4TICKETS", "")

	root := t.TempDir()
	prov := New(Config{Root: root, Hostname: "ci"})

	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//test/virt",
			Sync:   []*relayv1.SyncEntry{{Path: "//test/virt/...", Rev: "#head"}},
		},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	h, err := prov.Prepare(ctx, "task-remap", spec, func(s string) { t.Logf("prepare-progress: %s", s) })
	require.NoError(t, err, "Prepare must succeed against a remapped stream")
	defer func() { _ = h.Finalize(ctx) }()

	inv := h.Inventory()
	wsDir := filepath.Join(root, inv.ShortID)

	b, err := os.ReadFile(filepath.Join(wsDir, "sub", "readme.txt"))
	require.NoError(t, err, "the baseline file must land at the REMAPPED path under the workspace root")
	require.Equal(t, "baseline", strings.TrimSpace(string(b)))

	// The remap is what defines the layout, so the un-remapped location must
	// stay empty; a sync that ignored the client view would land it here.
	require.NoFileExists(t, filepath.Join(wsDir, "readme.txt"))
}
