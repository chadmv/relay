package api

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursor_RoundTrip(t *testing.T) {
	id := pgtype.UUID{Valid: true}
	copy(id.Bytes[:], []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10})
	tt := time.Date(2026, 4, 16, 10, 30, 45, 123456000, time.UTC) // µs precision

	enc := encodeCursor(tt, id)
	require.NotEmpty(t, enc)

	got, err := decodeCursor(enc)
	require.NoError(t, err)
	require.True(t, got.Set)
	assert.True(t, got.T.Equal(tt), "decoded time %v != original %v", got.T, tt)
	assert.Equal(t, id, got.ID)
}

func TestCursor_TruncatesNanos(t *testing.T) {
	// Postgres timestamptz is microsecond precision. The cursor codec must
	// truncate nanos on encode so a strict (created_at, id) < (cursor_ts, ...)
	// comparison won't accidentally skip the row at the boundary.
	id := pgtype.UUID{Valid: true}
	tt := time.Date(2026, 4, 16, 10, 30, 45, 123456789, time.UTC)
	expected := tt.Truncate(time.Microsecond)

	enc := encodeCursor(tt, id)
	got, err := decodeCursor(enc)
	require.NoError(t, err)
	assert.True(t, got.T.Equal(expected), "got %v, want %v", got.T, expected)
}

func TestCursor_Empty(t *testing.T) {
	got, err := decodeCursor("")
	require.NoError(t, err)
	assert.False(t, got.Set, "empty cursor must yield Set=false")
}

func TestCursor_InvalidBase64(t *testing.T) {
	_, err := decodeCursor("not!valid!base64!")
	assert.ErrorIs(t, err, errBadCursor)
}

func TestCursor_InvalidJSON(t *testing.T) {
	// Valid base64 wrapping non-JSON contents.
	_, err := decodeCursor("bm90LWpzb24") // base64url("not-json")
	assert.ErrorIs(t, err, errBadCursor)
}
