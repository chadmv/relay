package api

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func testUUID(b byte) pgtype.UUID {
	var u pgtype.UUID
	for i := range u.Bytes {
		u.Bytes[i] = b
	}
	u.Valid = true
	return u
}

func TestInviteEntry_FullRowKeySetIsExact(t *testing.T) {
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	expires := created.Add(72 * time.Hour)
	used := created.Add(time.Hour)
	email := "bound@example.com"

	entry := inviteEntry(testUUID(0x11), &email, testUUID(0x22), "admin@example.com",
		ts(created), ts(expires), ts(used))

	// Key-set equality. An added key fails here, which is the point: this is the
	// regression gate on the wire shape, and token_hash must never be able to
	// appear even under a different spelling.
	got := make([]string, 0, len(entry))
	for k := range entry {
		got = append(got, k)
	}
	assert.ElementsMatch(t,
		[]string{"id", "created_at", "expires_at", "created_by", "created_by_email", "email", "used_at"},
		got)

	assert.Equal(t, "11111111-1111-1111-1111-111111111111", entry["id"])
	assert.Equal(t, "22222222-2222-2222-2222-222222222222", entry["created_by"])
	assert.Equal(t, "admin@example.com", entry["created_by_email"])
	assert.Equal(t, "bound@example.com", entry["email"])
	assert.Equal(t, created, entry["created_at"])
	assert.Equal(t, expires, entry["expires_at"])
	assert.Equal(t, used, entry["used_at"])
}

func TestInviteEntry_OmitsUnsetOptionalKeys(t *testing.T) {
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	entry := inviteEntry(testUUID(0x11), nil, testUUID(0x22), "admin@example.com",
		ts(created), ts(created.Add(72*time.Hour)), pgtype.Timestamptz{})

	// Two-return lookups. Comparing to nil would pass against a handler that
	// wrote an explicit null, which is a different wire shape and the wrong one:
	// with `used_at?: string` in TypeScript, an absent key makes the wrong
	// client check (`used_at !== null`) a compile error, and a nulled field
	// would let that same mistake compile.
	_, hasEmail := entry["email"]
	require.False(t, hasEmail, "email must be absent, not null, on an unbound invite")
	_, hasUsedAt := entry["used_at"]
	require.False(t, hasUsedAt, "used_at must be absent, not null, on an unredeemed invite")

	got := make([]string, 0, len(entry))
	for k := range entry {
		got = append(got, k)
	}
	assert.ElementsMatch(t,
		[]string{"id", "created_at", "expires_at", "created_by", "created_by_email"}, got)
}

func TestInviteEntry_EmptyStringEmailIsStillPresent(t *testing.T) {
	// A non-nil pointer to the empty string is a bound-but-empty address, which
	// CreateInvite cannot produce today (invites.go:65-71 only sets the pointer
	// for a non-empty, parseable address). The case is pinned anyway so that the
	// presence rule is "the pointer is non-nil", not "the string is non-empty":
	// those differ, and only the first one matches the column semantics.
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	empty := ""
	entry := inviteEntry(testUUID(0x11), &empty, testUUID(0x22), "admin@example.com",
		ts(created), ts(created), pgtype.Timestamptz{})
	v, ok := entry["email"]
	require.True(t, ok, "a non-nil email pointer must produce the key")
	assert.Equal(t, "", v)
}
