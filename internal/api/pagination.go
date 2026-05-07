package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

var errBadCursor = errors.New("invalid cursor")

// cursor is the decoded form of an opaque pagination cursor.
type cursor struct {
	Set bool        // false → first page (no cursor sent)
	T   time.Time   // last-seen created_at, microsecond precision
	ID  pgtype.UUID // last-seen row id (tiebreaker)
}

// cursorWire is the JSON shape encoded inside the base64 envelope.
type cursorWire struct {
	T string `json:"t"`
	I string `json:"i"`
}

// encodeCursor serializes (t, id) as base64url(JSON). The timestamp is
// truncated to microsecond precision: Postgres timestamptz is µs-precise,
// and a nanosecond-precise cursor would skip the boundary row when the
// query does (created_at, id) < (cursor_ts, cursor_id).
func encodeCursor(t time.Time, id pgtype.UUID) string {
	tUTC := t.UTC().Truncate(time.Microsecond)
	w := cursorWire{
		T: tUTC.Format(time.RFC3339Nano),
		I: uuidStr(id),
	}
	b, _ := json.Marshal(w)
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor reverses encodeCursor. Empty input yields a zero cursor with
// Set=false (used for first-page requests). Malformed input returns
// errBadCursor; the caller MUST translate this to a 400 response and MUST
// NOT echo decoded bytes to the client.
func decodeCursor(s string) (cursor, error) {
	if s == "" {
		return cursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, errBadCursor
	}
	var w cursorWire
	if err := json.Unmarshal(b, &w); err != nil {
		return cursor{}, errBadCursor
	}
	t, err := time.Parse(time.RFC3339Nano, w.T)
	if err != nil {
		return cursor{}, errBadCursor
	}
	id, err := parseUUID(w.I)
	if err != nil {
		return cursor{}, errBadCursor
	}
	return cursor{Set: true, T: t, ID: id}, nil
}
