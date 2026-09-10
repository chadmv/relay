//go:build integration

package worker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"relay/internal/store"

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
		"the constraint's name must say which column refused it - that name is the only "+
			"diagnostic a writer bypassing the Go layer gets")
}
