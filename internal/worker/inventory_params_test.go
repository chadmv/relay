package worker

import (
	"errors"
	"strings"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testWorkerID is the resolved-at-registration identity the constructor takes as
// a parameter. It is deliberately NOT reachable from any WorkspaceInventoryUpdate
// field, which is what "the constructor never reads an identity off the wire"
// means concretely.
var testWorkerID = pgtype.UUID{
	Bytes: [16]byte{0x77, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	Valid: true,
}

// multiByteFiller is one rune that is two bytes in UTF-8, written as a code
// point rather than as a source literal so this file stays pure ASCII: a raw
// non-ASCII byte in a source file is unverifiable by eye and survives every
// encoding check this repo runs. Its byte length is asserted at the point of use
// rather than assumed, because the whole bytes-not-runes property is arithmetic
// on it.
var multiByteFiller = string(rune(0x00E9))

// legalUpdate is a row every predicate accepts. Each of its four TEXT fields
// carries a value that names its own field, so a transposition of two same-typed
// adjacent arguments cannot survive: the four fields of
// store.UpsertWorkerWorkspaceParams that this feeds - SourceType, SourceKey,
// ShortID, BaselineHash - are all string and all adjacent.
func legalUpdate() *relayv1.WorkspaceInventoryUpdate {
	return &relayv1.WorkspaceInventoryUpdate{
		SourceType:   "st-perforce",
		SourceKey:    "//sk/one",
		ShortId:      "shid-one",
		BaselineHash: "bh-one",
		LastUsedAt:   "2026-09-10T12:00:00Z",
	}
}

// TestInventoryUpsertParams_MapsEveryFieldToItsOwnColumn is the transposition
// guard. Four adjacent string fields can be permuted without a compile error, so
// every expectation here is a value only that one field could have produced.
func TestInventoryUpsertParams_MapsEveryFieldToItsOwnColumn(t *testing.T) {
	p, err := inventoryUpsertParams(testWorkerID, legalUpdate())
	require.NoError(t, err)

	assert.Equal(t, testWorkerID, p.WorkerID, "worker_id must come from the parameter, never from the wire message")
	assert.Equal(t, "st-perforce", p.SourceType)
	assert.Equal(t, "//sk/one", p.SourceKey)
	assert.Equal(t, "shid-one", p.ShortID)
	assert.Equal(t, "bh-one", p.BaselineHash)
	assert.True(t, p.LastUsedAt.Valid, "a parseable non-zero timestamp must bind as NOT NULL")
}

// TestInventoryUpsertParams_SourceKeyIsBoundedInBytesNotRunes pins BOTH the
// measure and the boundary, and one input does both.
//
// The one-byte-over multi-byte row is the discriminator: it is roughly half as
// many runes as bytes, so an implementation using utf8.RuneCountInString accepts
// it while the byte measure refuses it. The ASCII pair pins the off-by-one on
// the boundary itself, where > and >= differ.
//
// The over-long fillers are multiByteFiller and "a", neither of which appears in
// any expectation below, so no assertion can be satisfied by the injected value
// itself.
func TestInventoryUpsertParams_SourceKeyIsBoundedInBytesNotRunes(t *testing.T) {
	require.Equal(t, 2, len(multiByteFiller),
		"fixture: the filler must be two BYTES and one rune, or the arithmetic below means nothing")
	require.Equal(t, 1, len([]rune(multiByteFiller)), "fixture: and exactly one rune")

	multiAtBound := strings.Repeat(multiByteFiller, maxWorkspaceSourceKeyBytes/2)
	require.Equal(t, maxWorkspaceSourceKeyBytes, len(multiAtBound),
		"fixture: the at-bound multi-byte input must be exactly at the bound in BYTES")

	multiOneOver := multiAtBound + "a"
	require.Equal(t, maxWorkspaceSourceKeyBytes+1, len(multiOneOver),
		"fixture: one byte over")
	require.Less(t, len([]rune(multiOneOver)), maxWorkspaceSourceKeyBytes,
		"fixture: and comfortably UNDER the bound in runes - this is what makes it "+
			"discriminate a rune implementation from a byte one")

	cases := []struct {
		name     string
		key      string
		accepted bool
	}{
		{"ascii at the bound", strings.Repeat("a", maxWorkspaceSourceKeyBytes), true},
		{"ascii one byte over", strings.Repeat("a", maxWorkspaceSourceKeyBytes+1), false},
		{"multi-byte at the bound", multiAtBound, true},
		{"multi-byte one byte over", multiOneOver, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := legalUpdate()
			u.SourceKey = tc.key
			p, err := inventoryUpsertParams(testWorkerID, u)
			if tc.accepted {
				require.NoError(t, err)
				assert.Equal(t, tc.key, p.SourceKey, "an accepted key must be stored whole, never truncated")
				return
			}
			require.Error(t, err, "REFUSAL is the property. The accepted rows above are what an "+
				"ABSENT bound also produces; only this arm distinguishes the two.")
			assert.True(t, errors.Is(err, errUnstorableInventoryRow),
				"callers branch on this sentinel, so the wrapper must keep it wrapped")
			assert.NotContains(t, err.Error(), tc.key,
				"the refusal must carry no wire value: a caller that logs it must not be "+
					"loggable-into by the agent")
		})
	}
}

// TestInventoryUpsertParams_EveryAgentSuppliedStringIsBounded pins the boundary
// on each column separately: exactly at the bound accepted, one byte over
// refused. Only one field varies per case, so a case can fail for exactly one
// reason.
//
// baseline_hash is here for correctness rather than tidiness: source_type,
// source_key and baseline_hash share one worker_workspaces_lookup_idx entry, so
// "no accepted row can fail an index" is false while any one of the three is
// unbounded.
func TestInventoryUpsertParams_EveryAgentSuppliedStringIsBounded(t *testing.T) {
	cases := []struct {
		column string
		max    int
		set    func(u *relayv1.WorkspaceInventoryUpdate, v string)
	}{
		{"source_type", maxWorkspaceSourceTypeBytes, func(u *relayv1.WorkspaceInventoryUpdate, v string) { u.SourceType = v }},
		{"source_key", maxWorkspaceSourceKeyBytes, func(u *relayv1.WorkspaceInventoryUpdate, v string) { u.SourceKey = v }},
		{"short_id", maxWorkspaceShortIDBytes, func(u *relayv1.WorkspaceInventoryUpdate, v string) { u.ShortId = v }},
		{"baseline_hash", maxWorkspaceBaselineHashBytes, func(u *relayv1.WorkspaceInventoryUpdate, v string) { u.BaselineHash = v }},
	}
	for _, tc := range cases {
		t.Run(tc.column+" at the bound is accepted", func(t *testing.T) {
			u := legalUpdate()
			tc.set(u, strings.Repeat("Q", tc.max))
			_, err := inventoryUpsertParams(testWorkerID, u)
			require.NoError(t, err)
		})
		t.Run(tc.column+" one byte over is refused", func(t *testing.T) {
			u := legalUpdate()
			tc.set(u, strings.Repeat("Q", tc.max+1))
			_, err := inventoryUpsertParams(testWorkerID, u)
			require.Error(t, err, "REFUSAL is the property; the at-bound case above is what an "+
				"absent bound also produces")
			assert.True(t, errors.Is(err, errUnstorableInventoryRow))
			assert.Contains(t, err.Error(), tc.column,
				"the refusal must name the COLUMN, which is a compile-time literal, and nothing else")
		})
		t.Run(tc.column+" carrying a NUL is refused", func(t *testing.T) {
			u := legalUpdate()
			tc.set(u, "ok"+string(rune(0))+"ay")
			_, err := inventoryUpsertParams(testWorkerID, u)
			require.Error(t, err, "a NUL is legal in a proto3 string and illegal in TEXT "+
				"(SQLSTATE 22021), so without this the row fails its statement instead")
			assert.True(t, errors.Is(err, errUnstorableInventoryRow))
			assert.Contains(t, err.Error(), tc.column)
		})
	}
}

// TestInventoryUpsertParams_LastUsedAtMustParseAndBeNonZero closes the other way
// one bad row rolls back a batch: an unparseable value yields the zero time and
// binds pgtype.Timestamptz{Valid: false}, i.e. SQL NULL, into a TIMESTAMPTZ NOT
// NULL column.
//
// The zero-time case is NOT a restatement of the unparseable one and is the
// reachable honest case: an agent whose registry entry has an unset timestamp
// formats it as the year-one instant, which parses cleanly and is still NULL by
// the time it is bound. Checking only that the parse succeeded would let it
// through.
func TestInventoryUpsertParams_LastUsedAtMustParseAndBeNonZero(t *testing.T) {
	cases := []struct {
		name string
		ts   string
	}{
		{"blank", ""},
		{"not RFC3339", "10 September 2026"},
		{"parses but is the zero time", "0001-01-01T00:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := legalUpdate()
			u.LastUsedAt = tc.ts
			_, err := inventoryUpsertParams(testWorkerID, u)
			require.Error(t, err)
			assert.True(t, errors.Is(err, errUnstorableInventoryRow))
			assert.Contains(t, err.Error(), "last_used_at")
			if tc.ts != "" {
				assert.NotContains(t, err.Error(), tc.ts,
					"time.Parse's own message echoes its input, which is agent-supplied. The "+
						"wrapper must not carry it: handleInventoryUpdate's rule is that no wire "+
						"value reaches a log line.")
			}
		})
	}

	t.Run("a real timestamp binds as NOT NULL", func(t *testing.T) {
		p, err := inventoryUpsertParams(testWorkerID, legalUpdate())
		require.NoError(t, err)
		require.True(t, p.LastUsedAt.Valid,
			"Valid false is SQL NULL, which is what the NOT NULL column refuses")
		assert.Equal(t, 2026, p.LastUsedAt.Time.Year())
	})
}
