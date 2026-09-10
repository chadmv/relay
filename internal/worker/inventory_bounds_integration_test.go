//go:build integration

package worker_test

import (
	"context"
	"math/rand/v2"
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

// incompressibleASCII returns n bytes of printable ASCII drawn from a fixed
// seed, and is what the worst-case fixture is built from instead of
// strings.Repeat.
//
// A RUN OF ONE CHARACTER IS THE WRONG FILLER FOR A BYTE BOUND. A btree index
// tuple cannot be stored out of line, but it can be stored COMPRESSED, so a
// repeated-character value occupies far fewer bytes in the index entry than its
// length claims. The mutation the worst-case test exists to catch - a bound
// raised past what one entry can hold - therefore inserts anyway against a
// repeated-character fixture, and the test stays green while the property is
// gone. The seeds at the call sites are literals so a failure reproduces.
func incompressibleASCII(seed uint64, n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	r := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.IntN(len(alphabet))]
	}
	return string(b)
}

// distinctBytes counts the distinct byte values in s. It is how the fixture
// proves its filler is not a run of one character: strings.Repeat("Q", n)
// returns 1 here whatever n is.
func distinctBytes(s string) int {
	var seen [256]bool
	n := 0
	for i := 0; i < len(s); i++ {
		if !seen[s[i]] {
			seen[s[i]] = true
			n++
		}
	}
	return n
}

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
// dies if a bound is ever raised past what a btree entry can hold.
//
// SOURCE_TYPE, SOURCE_KEY AND BASELINE_HASH ARE ALL AT THEIR MAXIMA IN THE SAME
// ROW, because those three share one lookup-index entry: the quantity that has
// to fit is their SUM (64 + 512 + 128 = 704), not any one of them. Maxing
// source_key alone leaves most of that entry to the other two columns' short
// fixture values, which proves a smaller entry than these bounds permit.
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

	// 64/512/128/128 are the bounds in force; keep these literals in step with the
	// constants and the CHECK. source_key is "//" plus filler, so it is shaped like
	// a depot path. short_id is maxed too, though it reaches neither index - it is
	// stored per row, so it belongs in the worst-case ROW even though it cannot
	// contribute to the worst-case index ENTRY.
	atType := incompressibleASCII(1, 64)
	atBound := "//" + incompressibleASCII(2, 510)
	atShort := incompressibleASCII(3, 128)
	atBaseline := incompressibleASCII(4, 128)
	require.Equal(t, 64, len(atType), "fixture: source_type exactly at its bound")
	require.Equal(t, 512, len(atBound), "fixture: source_key exactly at its bound")
	require.Equal(t, 128, len(atShort), "fixture: short_id exactly at its bound")
	require.Equal(t, 128, len(atBaseline), "fixture: baseline_hash exactly at its bound")
	require.Equal(t, 704, len(atType)+len(atBound)+len(atBaseline),
		"fixture: 704 bytes is what one worker_workspaces_lookup_idx entry has to hold")
	require.Greater(t, distinctBytes(atType+atBound+atBaseline), 32,
		"fixture: the filler is not a run of one character, so these byte counts are the "+
			"bytes that reach the index entry rather than what compressing them leaves")

	// THE TWO KEYS USE DIFFERENT FILLERS ON PURPOSE, and the obvious fixture -
	// oneOver := atBound + "Q" - is the one this must not be. Under a TRUNCATING
	// implementation those two collapse to the same 512 bytes, so the row count
	// stays 1 and rows[0].SourceKey still equals atBound: the assertion below that
	// names the canonicalise-onto-one-row hazard cannot see the hazard. With
	// distinct fillers, truncation stores TWO rows and the length assertion is what
	// dies.
	//
	// AND THE ONE-OVER ROW'S OTHER THREE FIELDS STAY SHORT, so its source_key length
	// is the only thing about it that can be refused. Maxing them here too would
	// leave the refusal count below unable to say WHICH bound produced it.
	oneOver := "//" + incompressibleASCII(5, 511)
	require.Equal(t, 513, len(oneOver), "fixture: one byte over")
	require.NotEqual(t, atBound, oneOver[:512],
		"fixture: and its first 512 bytes differ from the at-bound key, so a truncating "+
			"implementation produces a SECOND row rather than silently overwriting this one")

	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, h.ApplyInventory(ctx, w.ID, []*relayv1.WorkspaceInventoryUpdate{
		{SourceType: "perforce", SourceKey: oneOver, ShortId: "over", BaselineHash: "overbase", LastUsedAt: now},
		{SourceType: atType, SourceKey: atBound, ShortId: atShort, BaselineHash: atBaseline, LastUsedAt: now},
	}))

	rows, err := q.ListWorkerWorkspaces(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1,
		"exactly one row: the at-bound one landed, the one-over one was dropped, and the "+
			"pre-existing row was replaced - which only happens if the transaction COMMITTED")
	assert.Equal(t, atBound, rows[0].SourceKey,
		"and it is stored WHOLE - a truncating implementation would store 512 bytes of the "+
			"513-byte key, which is a different workspace under the same primary key")
	assert.Equal(t, atType, rows[0].SourceType)
	assert.Equal(t, atShort, rows[0].ShortID,
		"positionally the AT-BOUND row, not the over one: short_id, source_key and "+
			"baseline_hash are adjacent strings and a transposition would not otherwise show")
	assert.Equal(t, atBaseline, rows[0].BaselineHash)
	assert.Equal(t, 704, len(rows[0].SourceType)+len(rows[0].SourceKey)+len(rows[0].BaselineHash),
		"read back: 704 bytes across the three columns that share one lookup-index entry, "+
			"which is the worst case these bounds permit")
	assert.Equal(t, uint64(1), h.InventoryRowRejections(),
		"exactly one refusal, from the one-over row")
}
