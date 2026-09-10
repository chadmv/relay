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
