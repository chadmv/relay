package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

var errBadCursor = errors.New("invalid cursor")

// cursor is the decoded form of an opaque pagination cursor.
type cursor struct {
	Set    bool        // false → first page (no cursor sent)
	Sort   string      // canonical sort string the cursor was issued for; "" for legacy cursors
	T      time.Time   // populated when the sort key's value type is timestamp
	StrVal string      // populated when the sort key's value type is text
	ID     pgtype.UUID // last-seen row id (tiebreaker)
}

// cursorWire is the JSON shape encoded inside the base64 envelope.
type cursorWire struct {
	T string `json:"t,omitempty"` // timestamp value
	I string `json:"i"`           // row id
	S string `json:"s,omitempty"` // sort string; omitted for legacy default-sort cursors
	V string `json:"v,omitempty"` // text value (populated when sort key is text)
}

// anySortVal is a tiny helper that lets buildPage's row-key callback return
// either a time.Time or a string without exploding the buildPage generic
// signature. encodeCursorV2 dispatches on the concrete runtime type.
type anySortVal any

// encodeCursorV2 serializes (sort, val, id) as base64url(JSON). val must be
// time.Time (for timestamp sort keys) or string (for text sort keys); any
// other type causes a panic — that's a programmer error in the per-endpoint
// sortSpec, not user input.
//
// Timestamps are truncated to microsecond precision: Postgres timestamptz
// is µs-precise, and a nanosecond-precise cursor would skip the boundary
// row when the query does (col, id) < (cursor_val, cursor_id).
func encodeCursorV2(sort string, val anySortVal, id pgtype.UUID) string {
	w := cursorWire{
		I: uuidStr(id),
		S: sort,
	}
	switch v := val.(type) {
	case time.Time:
		w.T = v.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
	case string:
		w.V = v
	default:
		panic("encodeCursorV2: unsupported sort value type")
	}
	b, _ := json.Marshal(w)
	return base64.RawURLEncoding.EncodeToString(b)
}

// encodeCursor is the legacy entrypoint that emits cursors with no S field,
// preserving wire compatibility while callers are migrated to encodeCursorV2.
// Remove once every caller passes through buildPage's new sort-aware path.
func encodeCursor(t time.Time, id pgtype.UUID) string {
	tUTC := t.UTC().Truncate(time.Microsecond)
	w := cursorWire{
		T: tUTC.Format(time.RFC3339Nano),
		I: uuidStr(id),
	}
	b, _ := json.Marshal(w)
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor reverses encodeCursor/encodeCursorV2. Empty input yields a
// zero cursor with Set=false (used for first-page requests). Malformed input
// returns errBadCursor; the caller MUST translate this to a 400 response and
// MUST NOT echo decoded bytes to the client.
//
// Legacy cursors (no S field) decode to Sort="" so the caller can substitute
// the spec default at parsePage time.
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
	// A well-formed cursor must have exactly one of T or V populated.
	// Both set is ambiguous (would silently pick one); neither set means
	// there is no sort value to compare against.
	if (w.T != "") == (w.V != "") {
		return cursor{}, errBadCursor
	}
	id, err := parseUUID(w.I)
	if err != nil {
		return cursor{}, errBadCursor
	}
	c := cursor{Set: true, Sort: w.S, StrVal: w.V, ID: id}
	if w.T != "" {
		t, err := time.Parse(time.RFC3339Nano, w.T)
		if err != nil {
			return cursor{}, errBadCursor
		}
		c.T = t
	}
	return c, nil
}

// sortKeyKind tells parsePage how to populate the cursor from the value
// returned by buildPage's row-key callback for this column.
type sortKeyKind int

const (
	sortKeyTimestamp sortKeyKind = iota // populates cursor.T
	sortKeyText                         // populates cursor.StrVal
)

// sortSpec is the per-endpoint allowlist. Default is the canonical sort
// string used when the client sends no ?sort= param; Keys maps each
// allowed key name (without leading dash) to its value kind.
type sortSpec struct {
	Default string
	Keys    map[string]sortKeyKind
}

// parseSort validates and canonicalizes the raw ?sort= value against the
// allowlist. Returns the canonical sort string ("name" / "-name") and the
// value kind. Empty raw input resolves to spec.Default.
func parseSort(raw string, spec sortSpec) (canonical string, kind sortKeyKind, err error) {
	if raw == "" {
		raw = spec.Default
	}
	key := raw
	if len(raw) > 0 && raw[0] == '-' {
		key = raw[1:]
	}
	if key == "" {
		return "", 0, fmt.Errorf("invalid sort %q", raw)
	}
	// Reject any character that wouldn't appear in a column name.
	for i := 0; i < len(key); i++ {
		c := key[i]
		isOK := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
		if !isOK {
			return "", 0, fmt.Errorf("invalid sort %q", raw)
		}
	}
	k, ok := spec.Keys[key]
	if !ok {
		allowed := make([]string, 0, len(spec.Keys))
		for k := range spec.Keys {
			allowed = append(allowed, k)
		}
		sort.Strings(allowed)
		return "", 0, fmt.Errorf("unsupported sort key '%s'; supported: %s", key, strings.Join(allowed, ", "))
	}
	return raw, k, nil
}

const (
	defaultLimit int32 = 50
	maxLimit     int32 = 200

	// historicalDefaultSort is the sort string that every pre-feature
	// cursor implicitly encoded. Before the ?sort= feature, all paginated
	// endpoints ordered by created_at DESC, id DESC, and cursors only ever
	// carried a created_at timestamp. When a cursor lacks an explicit Sort
	// field, we treat it as if issued under this sort - NOT the calling
	// endpoint's spec.Default (which may differ in some hypothetical future
	// endpoint). Using spec.Default here would silently accept legacy
	// cursors paired with arbitrary ?sort= values, producing wrong rows.
	historicalDefaultSort = "-created_at"
)

// pageParams captures validated pagination input from the URL query string.
type pageParams struct {
	Limit    int32
	Cursor   cursor
	Sort     string      // canonical sort string ("name" / "-name" / "-created_at")
	SortKind sortKeyKind // value type for the active sort key
}

// CursorTs returns the cursor timestamp as a pgtype.Timestamptz. The Valid
// flag tracks whether the cursor was actually sent (first-page requests
// produce a zero, invalid value).
func (p pageParams) CursorTs() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: p.Cursor.T, Valid: p.Cursor.Set}
}

// parsePage extracts ?limit=, ?cursor=, and ?sort= from the request. On
// invalid input it writes the 400 response itself and returns ok=false.
// Defaults: limit=50, sort=spec.Default. Range: limit [1, 200]. Bad cursor
// or sort -> 400.
func parsePage(w http.ResponseWriter, r *http.Request, spec sortSpec) (pageParams, bool) {
	pp := pageParams{Limit: defaultLimit}

	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil || n < 1 || n > int64(maxLimit) {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return pageParams{}, false
		}
		pp.Limit = int32(n)
	}

	sortRaw := r.URL.Query().Get("sort")
	canon, kind, err := parseSort(sortRaw, spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return pageParams{}, false
	}
	pp.Sort = canon
	pp.SortKind = kind

	c, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return pageParams{}, false
	}
	if c.Set {
		// Legacy cursor (Sort=="") is acceptable iff the resolved sort
		// equals the historical default that legacy cursors implied.
		effective := c.Sort
		if effective == "" {
			effective = historicalDefaultSort
		}
		if effective != canon {
			writeError(w, http.StatusBadRequest, "cursor sort key does not match requested sort; drop the cursor or change the sort")
			return pageParams{}, false
		}
	}
	pp.Cursor = c
	return pp, true
}

// page is the JSON envelope for paginated list endpoints.
type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	Total      int64  `json:"total"`
}

// buildPage trims a (limit+1)-row fetch result to limit rows and emits the
// cursor pointing at the LAST KEPT row's key — never the trimmed extra row.
//
// - Fewer than limit+1 rows fetched → no cursor (last page).
// - Empty input → empty items, empty cursor (do not echo input cursor).
// - Otherwise → trim to limit, encode cursor from items[limit-1].
func buildPage[Row, Out any](
	rows []Row,
	limit int32,
	conv func(Row) Out,
	key func(Row) (time.Time, pgtype.UUID),
) ([]Out, string) {
	if len(rows) == 0 {
		return []Out{}, ""
	}
	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := make([]Out, len(rows))
	for i, r := range rows {
		items[i] = conv(r)
	}
	if !hasMore {
		return items, ""
	}
	last := rows[len(rows)-1]
	t, id := key(last)
	return items, encodeCursor(t, id)
}
