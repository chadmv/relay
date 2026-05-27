package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
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

func TestParsePage_Defaults(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/jobs", nil)
	w := httptest.NewRecorder()
	pp, ok := parsePage(w, r)
	require.True(t, ok)
	assert.Equal(t, defaultLimit, pp.Limit)
	assert.False(t, pp.Cursor.Set)
}

func TestParsePage_LimitClamping(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		wantOK  bool
		wantLim int32
	}{
		{"valid mid", "?limit=37", true, 37},
		{"max", "?limit=200", true, 200},
		{"zero rejected", "?limit=0", false, 0},
		{"negative rejected", "?limit=-5", false, 0},
		{"over max rejected", "?limit=201", false, 0},
		{"non-numeric rejected", "?limit=abc", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/jobs"+tc.query, nil)
			w := httptest.NewRecorder()
			pp, ok := parsePage(w, r)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantLim, pp.Limit)
			}
		})
	}
}

func TestParsePage_BadCursor(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/jobs?cursor=garbage!!!", nil)
	w := httptest.NewRecorder()
	_, ok := parsePage(w, r)
	assert.False(t, ok)
	assert.Equal(t, 400, w.Code)
}

func TestBuildPage_NoMore(t *testing.T) {
	type row struct {
		t  time.Time
		id pgtype.UUID
	}
	id := pgtype.UUID{Valid: true}
	rows := []row{
		{time.Now(), id},
		{time.Now(), id},
	}
	conv := func(r row) string { return "x" }
	key := func(r row) (time.Time, pgtype.UUID) { return r.t, r.id }
	items, next := buildPage(rows, 50, conv, key)
	assert.Len(t, items, 2)
	assert.Empty(t, next, "next_cursor must be empty when fewer rows than limit")
}

func TestBuildPage_HasMore(t *testing.T) {
	type row struct {
		t  time.Time
		id pgtype.UUID
	}
	id := pgtype.UUID{Valid: true}
	t0 := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	rows := []row{
		{t0.Add(3 * time.Second), id},
		{t0.Add(2 * time.Second), id},
		{t0.Add(1 * time.Second), id},
	}
	conv := func(r row) string { return "x" }
	key := func(r row) (time.Time, pgtype.UUID) { return r.t, r.id }
	items, next := buildPage(rows, 2, conv, key) // limit=2, got 3 ⇒ trim, emit cursor
	assert.Len(t, items, 2)
	require.NotEmpty(t, next, "must emit cursor when limit+1 rows fetched")

	// Cursor must encode the LAST kept row's (t, id), not the trimmed extra.
	c, err := decodeCursor(next)
	require.NoError(t, err)
	assert.True(t, c.T.Equal(rows[1].t.Truncate(time.Microsecond)))
}

func TestBuildPage_EmptyResult(t *testing.T) {
	type row struct {
		t  time.Time
		id pgtype.UUID
	}
	conv := func(r row) string { return "x" }
	key := func(r row) (time.Time, pgtype.UUID) { return r.t, r.id }
	items, next := buildPage([]row{}, 50, conv, key)
	assert.Empty(t, items)
	assert.Empty(t, next, "empty result must yield empty cursor, not echo input")
}

func TestCursor_EncodesSortAndStringValue(t *testing.T) {
	id := pgtype.UUID{Valid: true}
	copy(id.Bytes[:], []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00})

	enc := encodeCursorV2("name", anySortVal("alpha"), id)
	require.NotEmpty(t, enc)

	got, err := decodeCursor(enc)
	require.NoError(t, err)
	require.True(t, got.Set)
	assert.Equal(t, "name", got.Sort)
	assert.Equal(t, "alpha", got.StrVal)
	assert.Equal(t, id, got.ID)
}

func TestCursor_EncodesTimestampVariant(t *testing.T) {
	id := pgtype.UUID{Valid: true}
	tt := time.Date(2026, 4, 16, 10, 30, 45, 123456000, time.UTC)

	enc := encodeCursorV2("-created_at", anySortVal(tt), id)
	got, err := decodeCursor(enc)
	require.NoError(t, err)
	require.True(t, got.Set)
	assert.Equal(t, "-created_at", got.Sort)
	assert.True(t, got.T.Equal(tt), "decoded time %v != original %v", got.T, tt)
}

func TestCursor_LegacyDecodeWithoutSortField(t *testing.T) {
	// A cursor written by pre-feature code: {"t":"...","i":"..."} only. Must
	// decode to cursor.Sort == "" so the caller can substitute the spec
	// default at parsePage time.
	tt := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	id := pgtype.UUID{Valid: true}
	legacy := encodeCursor(tt, id) // existing function, no S field

	got, err := decodeCursor(legacy)
	require.NoError(t, err)
	assert.Equal(t, "", got.Sort, "legacy cursor must yield empty Sort so caller can default it")
	assert.True(t, got.T.Equal(tt))
}

func TestCursor_RejectsBothTAndV(t *testing.T) {
	// A malicious or buggy client crafts a cursor with both T and V set.
	// decodeCursor must reject it rather than silently picking one.
	id := pgtype.UUID{Valid: true}
	w := cursorWire{
		T: "2026-04-16T10:00:00Z",
		I: uuidStr(id),
		S: "name",
		V: "alpha",
	}
	b, _ := json.Marshal(w)
	enc := base64.RawURLEncoding.EncodeToString(b)

	_, err := decodeCursor(enc)
	assert.ErrorIs(t, err, errBadCursor)
}

func TestCursor_RejectsNeitherTNorV(t *testing.T) {
	// A cursor with neither T nor V (just id, maybe sort) has no sort value
	// to compare against — malformed.
	id := pgtype.UUID{Valid: true}
	w := cursorWire{I: uuidStr(id), S: "name"}
	b, _ := json.Marshal(w)
	enc := base64.RawURLEncoding.EncodeToString(b)

	_, err := decodeCursor(enc)
	assert.ErrorIs(t, err, errBadCursor)
}
