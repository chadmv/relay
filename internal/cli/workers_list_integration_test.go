//go:build integration

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIntegration_WorkersList_RendersARealWorker is this lane's container-axis
// witness (the envelope: Total comes from CountWorkers, items from buildPage)
// and its field-axis witness for six worker fields at once.
//
// It asserts the NAME column, not a hostname: doWorkersList's non-revoked table
// is ID/NAME/STATUS/CPU/RAM GB/GPUS/GPU MODEL and prints wk.Name. There is no
// HOSTNAME column here at all - hostname is covered by the delete-by-hostname
// test, which resolves through resolveWorkerIDIn's `wk.Hostname == target`.
// seedWorker gives name and hostname DIFFERENT values, so the two assertions
// are independent and a transposition cannot satisfy both.
func TestIntegration_WorkersList_RendersARealWorker(t *testing.T) {
	s := startRelayServer(t)
	id := seedWorker(t, s, "farm-01-display", "farm-01", "offline")

	var out bytes.Buffer
	require.NoError(t, doWorkers(testCtx(t), s.adminCfg(), []string{"list"}, &out))

	got := out.String()
	require.Contains(t, got, "Total: 1")

	// The WHOLE row, with tabwriter's space padding collapsed. Asserting the
	// row rather than substrings is what stops "16" from matching a uuid.
	var row string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, id) {
			row = strings.Join(strings.Fields(line), " ")
		}
	}
	require.Equal(t, id+" farm-01-display offline 16 64 2 RTX 4090", row,
		"columns are ID NAME STATUS CPU RAM-GB GPUS GPU-MODEL; full output:\n%s", got)
}
