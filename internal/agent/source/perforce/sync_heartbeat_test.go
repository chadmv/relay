package perforce

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// threeSyncedFiles is the counter state the renderer table asserts against:
// three depot-path lines, none of them "other", the last one c.ma.
func threeSyncedFiles() *syncProgress {
	sp := &syncProgress{}
	sp.onLine("//depot/x/a.ma#3 - added as /ws/a.ma")
	sp.onLine("//depot/x/b.ma#1 - updating /ws/b.ma")
	sp.onLine("//depot/x/c.ma#2 - refreshing /ws/c.ma")
	return sp
}

// The summary is an operator-facing contract: five fields, fixed order, never
// omitted. The full-contract row asserts exact equality rather than Contains
// because a Contains check cannot see a field reordering, and the field order
// is the operator's question order (is it moving, will it fit, where is it).
func TestProvider_SyncSummaryRendersFiveFixedFields(t *testing.T) {
	t.Run("full_contract", func(t *testing.T) {
		p := New(Config{
			Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)},
			FreeDiskGB: func(string) (int64, error) { return 811, nil },
		})
		assert.Equal(t,
			"[sync] 4m30s; 3 files; 0 other lines; 811 GB free; last //depot/x/c.ma",
			p.syncSummary("[sync]", threeSyncedFiles(), 270*time.Second))
	})

	// A nil FreeDiskGB is a supported production state: every in-package test
	// constructs Config without it, and so does any embedder that has no
	// platform helper to pass.
	t.Run("nil_free_disk", func(t *testing.T) {
		p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
		assert.Contains(t, p.syncSummary("[sync]", threeSyncedFiles(), time.Second), "- GB free")
	})

	// The counter is what makes the disable sticky: one error must stop the
	// sampling for the rest of the sync, or a wedged volume is re-probed once
	// per heartbeat for the length of a multi-hour transfer.
	t.Run("erroring_free_disk_is_sampled_once", func(t *testing.T) {
		calls := 0
		p := New(Config{
			Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)},
			FreeDiskGB: func(string) (int64, error) { calls++; return 0, fmt.Errorf("statfs: boom") },
		})
		sp := threeSyncedFiles()
		assert.Contains(t, p.syncSummary("[sync]", sp, time.Second), "- GB free")
		assert.Contains(t, p.syncSummary("[sync]", sp, 2*time.Second), "- GB free")
		assert.Equal(t, 1, calls, "the free-disk probe must be disabled after its first error")
	})

	t.Run("no_file_line_yet", func(t *testing.T) {
		p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
		assert.Contains(t, p.syncSummary("[sync]", &syncProgress{}, time.Second), "last -")
	})

	t.Run("zero_elapsed", func(t *testing.T) {
		p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
		assert.Contains(t, p.syncSummary("[sync]", &syncProgress{}, 0), "[sync] 0s;")
	})

	t.Run("complete_prefix_carries_all_five_fields", func(t *testing.T) {
		p := New(Config{
			Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)},
			FreeDiskGB: func(string) (int64, error) { return 811, nil },
		})
		assert.Equal(t,
			"[sync] complete: 1s; 3 files; 0 other lines; 811 GB free; last //depot/x/c.ma",
			p.syncSummary("[sync] complete:", threeSyncedFiles(), time.Second))
	})

	// The number in the log must be the number the sweeper and
	// RELAY_WORKSPACE_MIN_FREE_GB act on, so the probe reads the provider root
	// rather than a per-workspace subdirectory.
	t.Run("free_disk_reads_the_provider_root", func(t *testing.T) {
		root := t.TempDir()
		var got string
		p := New(Config{
			Root: root, Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)},
			FreeDiskGB: func(path string) (int64, error) { got = path; return 1, nil },
		})
		p.syncSummary("[sync]", threeSyncedFiles(), time.Second)
		require.Equal(t, root, got)
	})
}
