//go:build integration

package worker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// multiByteFiller is one rune that is two bytes in UTF-8, written as a code
// point rather than as a source literal so this file stays pure ASCII: a raw
// non-ASCII byte in a source file is unverifiable by eye and survives every
// encoding check this repo runs.
var multiByteFiller = string(rune(0x00E9))

// TestUpsertWorkerWorkspace_TheCheckConstraintRefusesAnOverLongKey calls the
// store statement DIRECTLY, bypassing internal/worker's constructor by
// construction. BYPASSING THE GO CHOKEPOINT IS THE POINT: driving this through
// the handler would prove nothing about the constraint, because the Go check
// would refuse the row first and the database would never see it. The constraint
// is the backstop for a writer that does not exist yet - a sweeper, an eviction
// path, a second provider.
//
// The key is MULTI-BYTE, which is what makes this discriminate: a CHECK written
// on length() instead of octet_length() measures runes here and accepts the row,
// while octet_length measures bytes and refuses it. The two agree on every ASCII
// input, so an ASCII key could not tell them apart.
func TestUpsertWorkerWorkspace_TheCheckConstraintRefusesAnOverLongKey(t *testing.T) {
	ctx := context.Background()
	q, _ := newTestStore(t)

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "ck", Hostname: "ck", CpuCores: 1, RamGb: 1, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	require.Equal(t, 2, len(multiByteFiller), "fixture: the filler must be two BYTES and one rune")
	key := "//" + strings.Repeat(multiByteFiller, 256)
	require.Equal(t, 514, len(key), "fixture: 514 BYTES, two over the bound")
	require.Equal(t, 258, len([]rune(key)),
		"fixture: and only 258 runes, well under the bound - a length() CHECK would accept this")

	err = q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID: w.ID, SourceType: "perforce", SourceKey: key, ShortID: "ck1",
		BaselineHash: "ckbase", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.Error(t, err,
		"the database must refuse this on its own. Without the constraint the row inserts, "+
			"and the backstop for a future writer that skips the Go chokepoint does not exist.")
	assert.Contains(t, err.Error(), "source_key",
		"the constraint's name must say which column refused it - that name is the whole "+
			"diagnostic a writer bypassing the Go layer gets")
}

// TestApplyInventory_ARowAtTheBoundIsStoredAndOneOverIsNot is the end-to-end
// half. Against a real database, the at-bound row must survive the Go check, the
// primary key, worker_workspaces_lookup_idx AND the new CHECK - which is what
// dies if a bound is ever raised past what a btree entry can hold, since
// source_type, source_key and baseline_hash share one lookup-index entry.
//
// The pre-existing row is the third assertion and the one a batch-failure
// implementation cannot satisfy: BeginTxFunc rolling back would leave it in
// place, so its ABSENCE is what proves the replace committed.
func TestApplyInventory_ARowAtTheBoundIsStoredAndOneOverIsNot(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "bnd", Hostname: "bnd", CpuCores: 1, RamGb: 1, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	require.NoError(t, q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID: w.ID, SourceType: "perforce", SourceKey: "//pre/existing", ShortID: "pre",
		BaselineHash: "prebase", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}))

	// 512 is the bound in force; keep these literals in step with the constant and
	// the CHECK. "//" plus filler, so each key is shaped like a depot path.
	//
	// THE TWO KEYS USE DIFFERENT FILLERS ON PURPOSE, and the obvious fixture -
	// oneOver := atBound + "Q" - is the one this must not be. Under a TRUNCATING
	// implementation those two collapse to the same 512 bytes, so the row count
	// stays 1 and rows[0].SourceKey still equals atBound: the assertion below that
	// names the canonicalise-onto-one-row hazard cannot see the hazard. With
	// distinct fillers, truncation stores TWO rows and the length assertion is what
	// dies.
	atBound := "//" + strings.Repeat("Q", 510)
	require.Equal(t, 512, len(atBound), "fixture: exactly at the bound")
	oneOver := "//" + strings.Repeat("Z", 511)
	require.Equal(t, 513, len(oneOver), "fixture: one byte over")
	require.NotEqual(t, atBound, oneOver[:512],
		"fixture: and its first 512 bytes differ from the at-bound key, so a truncating "+
			"implementation produces a SECOND row rather than silently overwriting this one")

	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, h.ApplyInventory(ctx, w.ID, []*relayv1.WorkspaceInventoryUpdate{
		{SourceType: "perforce", SourceKey: oneOver, ShortId: "over", BaselineHash: "overbase", LastUsedAt: now},
		{SourceType: "perforce", SourceKey: atBound, ShortId: "at", BaselineHash: "atbase", LastUsedAt: now},
	}))

	rows, err := q.ListWorkerWorkspaces(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1,
		"exactly one row: the at-bound one landed, the one-over one was dropped, and the "+
			"pre-existing row was replaced - which only happens if the transaction COMMITTED")
	assert.Equal(t, atBound, rows[0].SourceKey,
		"and it is stored WHOLE - a truncating implementation would store 512 bytes of the "+
			"513-byte key, which is a different workspace under the same primary key")
	assert.Equal(t, "at", rows[0].ShortID,
		"positionally the AT-BOUND row, not the over one: short_id, source_key and "+
			"baseline_hash are adjacent strings and a transposition would not otherwise show")
	assert.Equal(t, "atbase", rows[0].BaselineHash)
	assert.Equal(t, uint64(1), h.InventoryRowRejections(),
		"exactly one refusal, from the one-over row")
}
