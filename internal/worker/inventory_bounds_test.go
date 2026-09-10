package worker

import (
	"context"
	"strings"
	"testing"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newInventoryFixture is applyInventory driven with no Postgres: a fake pool
// handing out one recording fakeTx (handler_register_success_test.go), and a
// strandDB that only exists to construct the *store.Queries the closure calls
// WithTx on.
func newInventoryFixture(t *testing.T) (*Handler, *fakeTx) {
	t.Helper()
	tx := &fakeTx{}
	return &Handler{
		q:    store.New(&strandDB{execTag: "INSERT 1"}),
		pool: &fakePool{tx: tx},
	}, tx
}

// overLongKey is a filler length far past any bound this package sets. The
// filler character appears in no assertion in this file, so no expectation can
// be satisfied by the injected value itself. It is a literal length rather than
// a reference to maxWorkspaceSourceKeyBytes, which is what lets this test say
// what it says without depending on the constant it is meant to discriminate.
const overLongKey = 4096

// TestApplyInventory_AnOverLongSourceKeyIsDroppedAndTheBatchCommits is the
// property the slice exists for. Failing the batch instead of the row is not a
// smaller version of this behaviour, it is a worse one: BeginTxFunc rolls back
// ReplaceWorkerInventory's DELETE with everything else, so the worker keeps its
// previous rows and an agent that reports the same bad row on every registration
// never updates its inventory again - a permanent, self-sustaining desync with no
// error anywhere.
//
// THE BAD ENTRY IS FIRST, DELIBERATELY. Placed last it cannot detect an
// implementation that returns or breaks on the first refusal, which is the
// mutation most likely to be written by accident.
func TestApplyInventory_AnOverLongSourceKeyIsDroppedAndTheBatchCommits(t *testing.T) {
	h, tx := newInventoryFixture(t)

	bad := &relayv1.WorkspaceInventoryUpdate{
		SourceType:   "st-perforce",
		SourceKey:    "//bad/" + strings.Repeat("Q", overLongKey),
		ShortId:      "shid-bad",
		BaselineHash: "bh-bad",
		LastUsedAt:   "2026-09-10T12:00:00Z",
	}
	good1 := &relayv1.WorkspaceInventoryUpdate{
		SourceType:   "st-perforce",
		SourceKey:    "//good/one",
		ShortId:      "shid-one",
		BaselineHash: "bh-one",
		LastUsedAt:   "2026-09-10T12:00:01Z",
	}
	good2 := &relayv1.WorkspaceInventoryUpdate{
		SourceType:   "st-perforce",
		SourceKey:    "//good/two",
		ShortId:      "shid-two",
		BaselineHash: "bh-two",
		LastUsedAt:   "2026-09-10T12:00:02Z",
	}

	require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
		[]*relayv1.WorkspaceInventoryUpdate{bad, good1, good2}),
		"a dropped row is not a store fault: finishRegister's only response to an error here "+
			"is a log line, so returning one would be a diagnostic about a working system")

	execs := tx.execsSeen()
	require.Len(t, execs, 3,
		"one ReplaceWorkerInventory plus exactly TWO upserts. Three upserts means no bound; "+
			"one means the loop stopped at the refusal")
	assert.Contains(t, execs[0].sql, "DELETE FROM worker_workspaces",
		"the replace must still run: it is what clears the worker's stale rows")

	// POSITIONAL, not a set and not a count. args are (worker_id, source_type,
	// source_key, short_id, baseline_hash, last_used_at); each row's four TEXT
	// values name their own row and their own column, so neither a reordering of
	// the surviving rows nor a transposition of two adjacent string columns can
	// pass.
	assert.Equal(t, "//good/one", execs[1].args[2])
	assert.Equal(t, "shid-one", execs[1].args[3])
	assert.Equal(t, "bh-one", execs[1].args[4])
	assert.Equal(t, "//good/two", execs[2].args[2])
	assert.Equal(t, "shid-two", execs[2].args[3])
	assert.Equal(t, "bh-two", execs[2].args[4])

	commits, _ := tx.outcome()
	assert.Equal(t, 1, commits,
		"THE LOAD-BEARING ASSERTION. Failing the batch produces zero commits and rolls the "+
			"DELETE back with it, leaving the worker's previous rows in place forever. Asserting "+
			"rollbacks == 0 instead would fail against correct code: pgx defers a rollback after a "+
			"successful commit.")
}

// TestApplyInventory_EveryRefusedRowIsDroppedAndTheBatchStillCommits is the batch
// half of its sibling table: it is not enough that the constructor refuses, the
// loop must keep going. The refused entry is FIRST in every case, for the reason
// its sibling gives.
func TestApplyInventory_EveryRefusedRowIsDroppedAndTheBatchStillCommits(t *testing.T) {
	nul := string(rune(0))
	cases := []struct {
		name string
		bad  func(u *relayv1.WorkspaceInventoryUpdate)
	}{
		{"source_type over bound", func(u *relayv1.WorkspaceInventoryUpdate) { u.SourceType = strings.Repeat("Q", overLongKey) }},
		{"source_key over bound", func(u *relayv1.WorkspaceInventoryUpdate) { u.SourceKey = strings.Repeat("Q", overLongKey) }},
		{"short_id over bound", func(u *relayv1.WorkspaceInventoryUpdate) { u.ShortId = strings.Repeat("Q", overLongKey) }},
		{"baseline_hash over bound", func(u *relayv1.WorkspaceInventoryUpdate) { u.BaselineHash = strings.Repeat("Q", overLongKey) }},
		{"source_type with a NUL", func(u *relayv1.WorkspaceInventoryUpdate) { u.SourceType = "st" + nul + "perforce" }},
		{"source_key with a NUL", func(u *relayv1.WorkspaceInventoryUpdate) { u.SourceKey = "//sk" + nul + "bad" }},
		{"short_id with a NUL", func(u *relayv1.WorkspaceInventoryUpdate) { u.ShortId = "shid" + nul + "bad" }},
		{"baseline_hash with a NUL", func(u *relayv1.WorkspaceInventoryUpdate) { u.BaselineHash = "bh" + nul + "bad" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, tx := newInventoryFixture(t)

			bad := &relayv1.WorkspaceInventoryUpdate{
				SourceType: "st-perforce", SourceKey: "//sk/bad", ShortId: "shid-bad",
				BaselineHash: "bh-bad", LastUsedAt: "2026-09-10T12:00:00Z",
			}
			tc.bad(bad)
			good := &relayv1.WorkspaceInventoryUpdate{
				SourceType: "st-perforce", SourceKey: "//good/survivor", ShortId: "shid-survivor",
				BaselineHash: "bh-survivor", LastUsedAt: "2026-09-10T12:00:01Z",
			}

			require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
				[]*relayv1.WorkspaceInventoryUpdate{bad, good}))

			execs := tx.execsSeen()
			require.Len(t, execs, 2, "the DELETE and exactly one upsert")
			assert.Equal(t, "//good/survivor", execs[1].args[2],
				"the surviving row must be the GOOD one, positionally")
			commits, _ := tx.outcome()
			assert.Equal(t, 1, commits, "and the batch must have committed")
		})
	}
}
