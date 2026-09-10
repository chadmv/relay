package worker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storableEntries returns n entries every bound in this package accepts, each
// with a distinct source key so a positional assertion can name one.
func storableEntries(n int) []*relayv1.WorkspaceInventoryUpdate {
	out := make([]*relayv1.WorkspaceInventoryUpdate, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, &relayv1.WorkspaceInventoryUpdate{
			SourceType:   "st-perforce",
			SourceKey:    fmt.Sprintf("//sk/%d", i),
			ShortId:      fmt.Sprintf("shid-%d", i),
			BaselineHash: "bh",
			LastUsedAt:   "2026-09-10T12:00:00Z",
		})
	}
	return out
}

// AN OVER-COUNT BATCH IS TRUNCATED AND COMMITS. Returning an error instead rolls
// ReplaceWorkerInventory's DELETE back with everything else, so the worker keeps
// its previous rows and an agent reporting the same over-count inventory on every
// registration never updates its inventory again - the freeze the byte-bounds
// slice removed, re-created on a new axis.
//
// commits == 1 is load-bearing: "the DELETE was issued" is satisfied just as well
// by a transaction that issued it and then rolled back.
func TestApplyInventory_AnOverCountBatchIsTruncatedAndCommits(t *testing.T) {
	h, tx := newInventoryFixture(t)

	require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
		storableEntries(maxInventoryRowsPerBatch+2)))

	execs := tx.execsSeen()
	require.Len(t, execs, maxInventoryRowsPerBatch+1, "the DELETE plus exactly max upserts")
	assert.Contains(t, execs[0].sql, "worker_workspaces")
	assert.Equal(t, "//sk/0", execs[1].args[2], "the surviving rows are the HEAD of the batch")
	assert.Equal(t, fmt.Sprintf("//sk/%d", maxInventoryRowsPerBatch-1),
		execs[len(execs)-1].args[2], "and the TAIL is what was dropped")

	commits, _ := tx.outcome()
	assert.Equal(t, 1, commits, "the transaction COMMITTED; a refusal would leave this at zero")
	assert.Equal(t, uint64(2), h.InventoryBatchOverflowDrops(), "one increment per dropped ENTRY")
}

// THE CONTROL. A batch of exactly max: every row is upserted and the counter
// moves by ZERO. Without this half, an increment on the accept path - or outside
// the branch entirely - passes the test above.
func TestApplyInventory_ABatchAtExactlyTheBoundDropsNothing(t *testing.T) {
	h, tx := newInventoryFixture(t)

	require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
		storableEntries(maxInventoryRowsPerBatch)))

	require.Len(t, tx.execsSeen(), maxInventoryRowsPerBatch+1, "the DELETE plus every row")
	commits, _ := tx.outcome()
	assert.Equal(t, 1, commits)
	assert.Equal(t, uint64(0), h.InventoryBatchOverflowDrops(),
		"THE CONTROL: an increment in the accept branch, or outside the branch, dies here")
}

// THE TWO COUNTERS ARE DISTINCT NOUNS and neither stands in for the other: one
// counts rows refused for CONTENT, the other entries past a COUNT bound. Folding
// them together makes a climbing number unattributable to either cause, and the
// remedies differ.
//
// They are disjoint PER ENTRY, which is the property the third subtest pins: the
// truncation runs ahead of the loop, so an entry dropped for overflow never
// reaches inventoryUpsertParams and cannot also be counted as a content refusal.
// One BATCH can move both, and that is not a contradiction.
func TestInventoryCounters_OverflowAndContentRefusalAreSeparateNumbers(t *testing.T) {
	t.Run("an over-count batch moves only the overflow counter", func(t *testing.T) {
		h, _ := newInventoryFixture(t)
		require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
			storableEntries(maxInventoryRowsPerBatch+3)))
		assert.Equal(t, uint64(3), h.InventoryBatchOverflowDrops())
		assert.Equal(t, uint64(0), h.InventoryRowRejections(),
			"no row was refused for content")
	})

	t.Run("a content-refused row moves only the rejection counter", func(t *testing.T) {
		h, _ := newInventoryFixture(t)
		inv := storableEntries(3)
		inv[0].SourceKey = strings.Repeat("Q", overLongKey)
		require.NoError(t, h.applyInventory(context.Background(), testWorkerID, inv))
		assert.Equal(t, uint64(1), h.InventoryRowRejections())
		assert.Equal(t, uint64(0), h.InventoryBatchOverflowDrops(),
			"an under-count batch drops no entry for overflow")
	})

	// ONE BATCH CAN MOVE BOTH, and each by its own amount. This is the row that
	// would go red if either increment were folded into the other counter, and it
	// is also what makes the per-entry disjointness checkable rather than asserted.
	t.Run("a batch that is both over-count and carries a bad row moves each once", func(t *testing.T) {
		h, _ := newInventoryFixture(t)
		inv := storableEntries(maxInventoryRowsPerBatch + 2)
		inv[0].SourceKey = strings.Repeat("Q", overLongKey)
		require.NoError(t, h.applyInventory(context.Background(), testWorkerID, inv))
		assert.Equal(t, uint64(2), h.InventoryBatchOverflowDrops(), "two entries past the bound")
		assert.Equal(t, uint64(1), h.InventoryRowRejections(), "one surviving row refused for content")
	})
}
