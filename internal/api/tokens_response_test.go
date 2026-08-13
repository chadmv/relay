package api

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenEntry_KeySetIsExactAndIsCurrentAlwaysPresent(t *testing.T) {
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	expires := created.Add(30 * 24 * time.Hour)
	id := testUUID(0x33)

	entry := tokenEntry(id, ts(created), ts(expires), id)

	got := make([]string, 0, len(entry))
	for k := range entry {
		got = append(got, k)
	}
	assert.ElementsMatch(t, []string{"id", "created_at", "expires_at", "is_current"}, got)
	assert.Equal(t, "33333333-3333-3333-3333-333333333333", entry["id"])
	assert.Equal(t, created, entry["created_at"])
	assert.Equal(t, expires, entry["expires_at"])
	assert.Equal(t, true, entry["is_current"])
}

// A NULL expires_at means the token never expires. The key is OMITTED, and
// is_current is still present - "not your current session" is a positive fact
// the UI states, so it is never omitted even when false.
func TestTokenEntry_NullExpiresAtOmitsTheKeyAndKeepsIsCurrent(t *testing.T) {
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	entry := tokenEntry(testUUID(0x33), ts(created), pgtype.Timestamptz{}, testUUID(0x44))

	_, hasExpires := entry["expires_at"]
	require.False(t, hasExpires, "a never-expiring token must omit expires_at, not null it")
	v, hasCurrent := entry["is_current"]
	require.True(t, hasCurrent, "is_current is never omitted")
	assert.Equal(t, false, v)

	got := make([]string, 0, len(entry))
	for k := range entry {
		got = append(got, k)
	}
	assert.ElementsMatch(t, []string{"id", "created_at", "is_current"}, got)
}

// is_current is a full UUID comparison, not a prefix or a Valid check. The
// near-miss case (one differing byte) is what discriminates a real comparison
// from a sloppy one.
func TestTokenEntry_IsCurrentTrueOnlyForTheExactID(t *testing.T) {
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	rowID := testUUID(0x55)

	nearMiss := rowID
	nearMiss.Bytes[15] ^= 0x01

	assert.Equal(t, true, tokenEntry(rowID, ts(created), ts(created), rowID)["is_current"])
	assert.Equal(t, false, tokenEntry(rowID, ts(created), ts(created), nearMiss)["is_current"],
		"a UUID differing in one byte is not the current session")

	// A zero-value current token id must fail CLOSED: no row is marked current,
	// rather than an arbitrary row being marked. This state is unreachable
	// through the handler (BearerAuth must succeed first) and is pinned anyway.
	assert.Equal(t, false, tokenEntry(rowID, ts(created), ts(created), pgtype.UUID{})["is_current"])
}
