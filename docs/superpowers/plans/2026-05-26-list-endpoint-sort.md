# Configurable Sort Order for List Endpoints — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in `?sort=<key>` query parameter to all six paginated REST list endpoints, with cursor encoding the active sort key and 400 on mismatch.

**Architecture:** Extend the shared pagination layer (`internal/api/pagination.go`) to carry a per-endpoint sort allowlist (`sortSpec`); generate per-(table, key, direction) sqlc queries for each endpoint's primary list; route to the right query from a switch in each handler; surface `?sort=` through the `relay` CLI and the MCP tool input structs.

**Tech Stack:** Go 1.22, sqlc (Postgres), pgx/v5, golang-migrate. Tests via `testing` + `stretchr/testify`; integration tests with testcontainers-go under `//go:build integration`.

**Spec:** [docs/superpowers/specs/2026-05-26-list-endpoint-sort-design.md](../specs/2026-05-26-list-endpoint-sort-design.md)

---

## Conventions used throughout this plan

- "Run unit tests" means `go test ./internal/api/... -run <TestName> -v -timeout 30s` from the repo root.
- "Run integration tests" means `go test -tags integration -p 1 ./internal/api/... -run <TestName> -v -timeout 180s`. Requires Docker Desktop running.
- All file paths are repo-relative. Repo root contains `go.mod`, `Makefile`, `cmd/`, `internal/`.
- `make generate` regenerates sqlc-bound code under `internal/store/*.sql.go`. **Run after editing any `.sql` file.** Never hand-edit the generated files.
- Commits should each pass `go vet ./... && go build ./... && make test`. The implementation plan calls out which task crosses which boundary.

---

## Task 1: Extend cursor and wire format with sort metadata

**Files:**
- Modify: `internal/api/pagination.go`
- Test: `internal/api/pagination_test.go`

Backward-compat goal: cursors emitted by the old code (no `s` field) must continue to decode and route to the historical default sort `-created_at`. This task introduces the new fields but does not yet change any caller.

- [ ] **Step 1: Write the failing test for the new cursor fields**

Append to `internal/api/pagination_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./internal/api/... -run "TestCursor_Encodes|TestCursor_Legacy" -v -timeout 30s`
Expected: build fails — `encodeCursorV2`, `anySortVal`, `cursor.Sort`, `cursor.StrVal` undefined.

- [ ] **Step 3: Extend the cursor types and add `encodeCursorV2`**

In `internal/api/pagination.go`, replace the `cursor` and `cursorWire` types and add the new helpers:

```go
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
```

Update `decodeCursor` to populate the new fields:

```go
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
```

Keep the existing `encodeCursor(t, id)` function as a thin shim so callers that haven't been migrated yet still compile:

```go
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
```

- [ ] **Step 4: Run the new and existing tests**

Run: `go test ./internal/api/... -run "TestCursor" -v -timeout 30s`
Expected: all `TestCursor_*` PASS (including the three new ones and the four existing ones: `RoundTrip`, `TruncatesNanos`, `Empty`, `InvalidBase64`, `InvalidJSON`).

- [ ] **Step 5: Commit**

```bash
git add internal/api/pagination.go internal/api/pagination_test.go
git commit -m "api/pagination: extend cursor with sort key and value variants

Adds cursor.Sort, cursor.StrVal, encodeCursorV2, and the V/S wire fields.
encodeCursor is kept as a shim for legacy callers; decodeCursor populates
Sort='' for legacy cursors so the caller can substitute the spec default.
"
```

---

## Task 2: Add `sortSpec` type and `parseSort` helper

**Files:**
- Modify: `internal/api/pagination.go`
- Test: `internal/api/pagination_test.go`

This task introduces the per-endpoint sort allowlist as a standalone construct. `parsePage` is not yet touched.

- [ ] **Step 1: Write the failing tests**

Append to `internal/api/pagination_test.go`:

```go
func TestParseSort_DefaultWhenAbsent(t *testing.T) {
	spec := sortSpec{
		Default: "-created_at",
		Keys: map[string]sortKeyKind{
			"created_at": sortKeyTimestamp,
			"name":       sortKeyText,
		},
	}
	got, kind, err := parseSort("", spec)
	require.NoError(t, err)
	assert.Equal(t, "-created_at", got)
	assert.Equal(t, sortKeyTimestamp, kind)
}

func TestParseSort_AscAndDesc(t *testing.T) {
	spec := sortSpec{
		Default: "-created_at",
		Keys: map[string]sortKeyKind{
			"created_at": sortKeyTimestamp,
			"name":       sortKeyText,
		},
	}
	asc, kind, err := parseSort("name", spec)
	require.NoError(t, err)
	assert.Equal(t, "name", asc)
	assert.Equal(t, sortKeyText, kind)

	desc, kind, err := parseSort("-name", spec)
	require.NoError(t, err)
	assert.Equal(t, "-name", desc)
	assert.Equal(t, sortKeyText, kind)
}

func TestParseSort_UnknownKey(t *testing.T) {
	spec := sortSpec{
		Default: "-created_at",
		Keys:    map[string]sortKeyKind{"created_at": sortKeyTimestamp, "name": sortKeyText},
	}
	_, _, err := parseSort("labels", spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported sort key 'labels'")
	assert.Contains(t, err.Error(), "created_at")
	assert.Contains(t, err.Error(), "name")
}

func TestParseSort_RejectsEmptyAndWrongSyntax(t *testing.T) {
	spec := sortSpec{Default: "-created_at", Keys: map[string]sortKeyKind{"name": sortKeyText}}
	for _, bad := range []string{"-", "--name", "name asc", "name:desc"} {
		_, _, err := parseSort(bad, spec)
		assert.Error(t, err, "expected error for sort=%q", bad)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./internal/api/... -run "TestParseSort" -v -timeout 30s`
Expected: build fails — `sortSpec`, `sortKeyKind`, `parseSort` undefined.

- [ ] **Step 3: Add the types and helper**

In `internal/api/pagination.go`, after the cursor block:

```go
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
	if key == "" || key == "-" {
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
		return "", 0, fmt.Errorf("unsupported sort key %q; supported: %s", key, strings.Join(allowed, ", "))
	}
	return raw, k, nil
}
```

Add the new imports to the existing import block at the top of the file:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/... -run "TestParseSort" -v -timeout 30s`
Expected: all four `TestParseSort_*` PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/pagination.go internal/api/pagination_test.go
git commit -m "api/pagination: add sortSpec type and parseSort helper

Standalone allowlist construct; not yet wired into parsePage."
```

---

## Task 3: Thread `sortSpec` through `parsePage`; add sort/cursor mismatch detection

**Files:**
- Modify: `internal/api/pagination.go`
- Test: `internal/api/pagination_test.go`
- Modify: `internal/api/jobs.go`, `workers.go`, `users.go`, `scheduled_jobs.go`, `reservations.go`, `agent_enrollments.go` (call-site updates only)

This task changes the `parsePage` signature. To keep the diff manageable, every caller passes a temporary default-only `sortSpec{Default: "-created_at", Keys: map[string]sortKeyKind{"created_at": sortKeyTimestamp}}` so behaviour remains identical until the per-endpoint specs land in later tasks.

- [ ] **Step 1: Write failing tests for the new parsePage behaviour**

Append to `internal/api/pagination_test.go`:

```go
var testDefaultSpec = sortSpec{
	Default: "-created_at",
	Keys: map[string]sortKeyKind{
		"created_at": sortKeyTimestamp,
		"name":       sortKeyText,
	},
}

func TestParsePage_SortKeyAccepted(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/jobs?sort=name", nil)
	w := httptest.NewRecorder()
	pp, ok := parsePage(w, r, testDefaultSpec)
	require.True(t, ok)
	assert.Equal(t, "name", pp.Sort)
	assert.Equal(t, sortKeyText, pp.SortKind)
}

func TestParsePage_UnknownSortKey_400(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/jobs?sort=labels", nil)
	w := httptest.NewRecorder()
	_, ok := parsePage(w, r, testDefaultSpec)
	assert.False(t, ok)
	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "unsupported sort key 'labels'")
}

func TestParsePage_CursorSortMismatch_400(t *testing.T) {
	// Encode a cursor under sort="-created_at", then request with ?sort=name.
	id := pgtype.UUID{Valid: true}
	tt := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	cur := encodeCursorV2("-created_at", anySortVal(tt), id)

	r := httptest.NewRequest("GET", "/v1/jobs?sort=name&cursor="+cur, nil)
	w := httptest.NewRecorder()
	_, ok := parsePage(w, r, testDefaultSpec)
	assert.False(t, ok)
	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "cursor sort key does not match")
}

func TestParsePage_LegacyCursorMatchesDefault(t *testing.T) {
	// Legacy cursor (no S field) must be accepted when the request omits
	// ?sort=, since the spec's Default is the historical "-created_at".
	id := pgtype.UUID{Valid: true}
	tt := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	cur := encodeCursor(tt, id)

	r := httptest.NewRequest("GET", "/v1/jobs?cursor="+cur, nil)
	w := httptest.NewRecorder()
	pp, ok := parsePage(w, r, testDefaultSpec)
	require.True(t, ok)
	assert.Equal(t, "-created_at", pp.Sort)
}
```

Update the existing `TestParsePage_Defaults`, `TestParsePage_LimitClamping`, and `TestParsePage_BadCursor` to pass `testDefaultSpec`:

```go
func TestParsePage_Defaults(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/jobs", nil)
	w := httptest.NewRecorder()
	pp, ok := parsePage(w, r, testDefaultSpec)
	require.True(t, ok)
	assert.Equal(t, defaultLimit, pp.Limit)
	assert.False(t, pp.Cursor.Set)
	assert.Equal(t, "-created_at", pp.Sort)
}
// ...similar edits to the other two existing tests, threading testDefaultSpec.
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./internal/api/... -run "TestParsePage" -v -timeout 30s`
Expected: build fails — `parsePage` does not accept a third argument; `pp.Sort` / `pp.SortKind` undefined.

- [ ] **Step 3: Update `pageParams` and `parsePage`**

In `internal/api/pagination.go`:

```go
// pageParams captures validated pagination input from the URL query string.
type pageParams struct {
	Limit    int32
	Cursor   cursor
	Sort     string      // canonical sort string ("name" / "-name" / "-created_at")
	SortKind sortKeyKind // value type for the active sort key
}

// parsePage extracts ?limit=, ?cursor=, and ?sort= from the request. On
// invalid input it writes the 400 response itself and returns ok=false.
// Defaults: limit=50, sort=spec.Default. Range: limit [1, 200]. Bad cursor
// or sort → 400.
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
			effective = "-created_at"
		}
		if effective != canon {
			writeError(w, http.StatusBadRequest, "cursor sort key does not match requested sort; drop the cursor or change the sort")
			return pageParams{}, false
		}
	}
	pp.Cursor = c
	return pp, true
}
```

- [ ] **Step 4: Update all six call sites to pass the temporary default spec**

In each of `internal/api/jobs.go`, `workers.go`, `users.go`, `scheduled_jobs.go`, `reservations.go`, `agent_enrollments.go`, at the top of each handler that calls `parsePage`, add:

```go
// Temporary default spec; per-endpoint specs land in later tasks.
defaultSortSpec := sortSpec{
    Default: "-created_at",
    Keys:    map[string]sortKeyKind{"created_at": sortKeyTimestamp},
}
pp, ok := parsePage(w, r, defaultSortSpec)
```

(Find each `parsePage(w, r)` call site with `grep -rn "parsePage(w, r)" internal/api/` — there are six.)

- [ ] **Step 5: Run all unit tests**

Run: `go test ./internal/api/... -v -timeout 60s`
Expected: every existing test still passes; the four new `TestParsePage_*` cases pass; total unit-test count up by 4.

- [ ] **Step 6: Commit**

```bash
git add internal/api/pagination.go internal/api/pagination_test.go \
        internal/api/jobs.go internal/api/workers.go internal/api/users.go \
        internal/api/scheduled_jobs.go internal/api/reservations.go \
        internal/api/agent_enrollments.go
git commit -m "api/pagination: thread sortSpec through parsePage

parsePage now validates ?sort= against a per-endpoint allowlist and
rejects cursor/sort mismatch with 400. Behaviour unchanged because every
call site still passes a default-only spec; per-endpoint specs follow
in subsequent commits."
```

---

## Task 4: Generalize `buildPage` to emit sort-aware cursors

**Files:**
- Modify: `internal/api/pagination.go`
- Test: `internal/api/pagination_test.go`
- Modify: each handler that calls `buildPage` (six files; same set as Task 3)

- [ ] **Step 1: Write failing tests for the new `buildPage` signature**

Replace the existing `TestBuildPage_HasMore` test in `internal/api/pagination_test.go` and add a new one:

```go
func TestBuildPage_HasMore_TimestampSort(t *testing.T) {
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
	key := func(r row) (anySortVal, pgtype.UUID) { return r.t, r.id }

	items, next := buildPage(rows, 2, "-created_at", conv, key)
	assert.Len(t, items, 2)
	require.NotEmpty(t, next, "must emit cursor when limit+1 rows fetched")

	c, err := decodeCursor(next)
	require.NoError(t, err)
	assert.Equal(t, "-created_at", c.Sort)
	assert.True(t, c.T.Equal(rows[1].t.Truncate(time.Microsecond)))
}

func TestBuildPage_HasMore_TextSort(t *testing.T) {
	type row struct {
		name string
		id   pgtype.UUID
	}
	id := pgtype.UUID{Valid: true}
	rows := []row{
		{"alpha", id},
		{"beta", id},
		{"gamma", id},
	}
	conv := func(r row) string { return r.name }
	key := func(r row) (anySortVal, pgtype.UUID) { return r.name, r.id }

	items, next := buildPage(rows, 2, "name", conv, key)
	assert.Len(t, items, 2)
	require.NotEmpty(t, next)

	c, err := decodeCursor(next)
	require.NoError(t, err)
	assert.Equal(t, "name", c.Sort)
	assert.Equal(t, "beta", c.StrVal)
}
```

Update the existing `TestBuildPage_NoMore` and `TestBuildPage_EmptyResult` to match the new key-callback signature `func(row) (anySortVal, pgtype.UUID)` and to pass a sort string argument:

```go
func TestBuildPage_NoMore(t *testing.T) {
	// ...
	key := func(r row) (anySortVal, pgtype.UUID) { return r.t, r.id }
	items, next := buildPage(rows, 50, "-created_at", conv, key)
	// ...
}

func TestBuildPage_EmptyResult(t *testing.T) {
	// ...
	key := func(r row) (anySortVal, pgtype.UUID) { return r.t, r.id }
	items, next := buildPage([]row{}, 50, "-created_at", conv, key)
	// ...
}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./internal/api/... -run "TestBuildPage" -v -timeout 30s`
Expected: build fails — `buildPage` signature mismatch.

- [ ] **Step 3: Update `buildPage`**

Replace the existing `buildPage` in `internal/api/pagination.go`:

```go
// buildPage trims a (limit+1)-row fetch result to limit rows and emits the
// cursor pointing at the LAST KEPT row's key — never the trimmed extra row.
//
//   - Fewer than limit+1 rows fetched → no cursor (last page).
//   - Empty input → empty items, empty cursor (do not echo input cursor).
//   - Otherwise → trim to limit, encode cursor from items[limit-1] using
//     the sort string the request resolved to.
//
// The key callback returns (sortVal, id) where sortVal is either time.Time
// or string; encodeCursorV2 dispatches on the runtime type.
func buildPage[Row, Out any](
	rows []Row,
	limit int32,
	sort string,
	conv func(Row) Out,
	key func(Row) (anySortVal, pgtype.UUID),
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
	val, id := key(last)
	return items, encodeCursorV2(sort, val, id)
}
```

- [ ] **Step 4: Update every `buildPage` call site to match the new signature**

For each of the six handlers, update the `buildPage(...)` calls to:
1. Pass `pp.Sort` as the third argument.
2. Change each `*RowKey*` helper's return type from `(time.Time, pgtype.UUID)` to `(anySortVal, pgtype.UUID)`. The body is unchanged because `time.Time` satisfies `anySortVal`.

Example for `internal/api/jobs.go`:

```go
func jobsRowKeyDefault(r store.ListJobsWithEmailPageRow) (anySortVal, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}
func jobsRowKeyByStatus(r store.ListJobsByStatusWithEmailPageRow) (anySortVal, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}
func jobsRowKeyByScheduled(r store.ListJobsByScheduledJobWithEmailPageRow) (anySortVal, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}
```

And in each `buildPage` call:

```go
items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseDefault, jobsRowKeyDefault)
```

Find all call sites: `grep -rn "buildPage(rows" internal/api/`. Update each one.

- [ ] **Step 5: Run the full unit suite**

Run: `go test ./internal/api/... -v -timeout 60s`
Expected: all existing and new tests pass; cursor field `S` now appears in emitted cursors (a manual `decodeCursor` of a real response would show `Sort: "-created_at"`).

- [ ] **Step 6: Run integration tests for one endpoint to confirm wire compat**

Run: `go test -tags integration -p 1 ./internal/api/... -run "TestListJobs" -v -timeout 180s`
Expected: existing pagination tests pass; emitted cursors are decodable and round-trip through subsequent page fetches. (Cursor bytes will differ from pre-change due to the new `s` field, but the contract holds.)

- [ ] **Step 7: Commit**

```bash
git add internal/api/pagination.go internal/api/pagination_test.go \
        internal/api/jobs.go internal/api/workers.go internal/api/users.go \
        internal/api/scheduled_jobs.go internal/api/reservations.go \
        internal/api/agent_enrollments.go
git commit -m "api/pagination: buildPage takes sort string, emits sort-aware cursors

Row-key callbacks now return anySortVal so a future text-sort variant
fits the same generic interface."
```

---

## Task 5: Add migration `000013_paginated_sort_indexes`

**Files:**
- Create: `internal/store/migrations/000013_paginated_sort_indexes.up.sql`
- Create: `internal/store/migrations/000013_paginated_sort_indexes.down.sql`
- Test: covered by Task 14's `pg_indexes` existence test

The numbering continues from `000012_workers_disabled_at`. Use `DESC` ordering in index definitions to match the existing convention in `000011_pagination_indexes.up.sql`; Postgres scans either direction regardless.

- [ ] **Step 1: Write the up migration**

Create `internal/store/migrations/000013_paginated_sort_indexes.up.sql`:

```sql
-- Composite indexes supporting cursor pagination with configurable ?sort=.
-- Each index covers ORDER BY <col> DESC, id DESC (asc scans use the same
-- index in reverse). Nullable-timestamp keys get both NULLS LAST and NULLS
-- FIRST variants so cursor pagination works in both directions.

-- jobs: name, priority, status, updated_at
CREATE INDEX idx_jobs_name_id     ON jobs (name DESC, id DESC);
CREATE INDEX idx_jobs_priority_id ON jobs (priority DESC, id DESC);
CREATE INDEX idx_jobs_status_id   ON jobs (status DESC, id DESC);
CREATE INDEX idx_jobs_updated_id  ON jobs (updated_at DESC, id DESC);

-- workers: name, status, last_seen_at (nullable)
CREATE INDEX idx_workers_name_id        ON workers (name DESC, id DESC);
CREATE INDEX idx_workers_status_id      ON workers (status DESC, id DESC);
CREATE INDEX idx_workers_last_seen_desc ON workers (last_seen_at DESC NULLS LAST, id DESC);
CREATE INDEX idx_workers_last_seen_asc  ON workers (last_seen_at ASC NULLS FIRST, id ASC);

-- users: name, email
CREATE INDEX idx_users_name_id  ON users (name DESC, id DESC);
CREATE INDEX idx_users_email_id ON users (email DESC, id DESC);

-- scheduled_jobs: name, next_run_at, updated_at
CREATE INDEX idx_sched_jobs_name_id     ON scheduled_jobs (name DESC, id DESC);
CREATE INDEX idx_sched_jobs_next_run_id ON scheduled_jobs (next_run_at DESC, id DESC);
CREATE INDEX idx_sched_jobs_updated_id  ON scheduled_jobs (updated_at DESC, id DESC);

-- reservations: name, starts_at (nullable), ends_at (nullable)
CREATE INDEX idx_reservations_name_id        ON reservations (name DESC, id DESC);
CREATE INDEX idx_reservations_starts_desc    ON reservations (starts_at DESC NULLS LAST, id DESC);
CREATE INDEX idx_reservations_starts_asc     ON reservations (starts_at ASC NULLS FIRST, id ASC);
CREATE INDEX idx_reservations_ends_desc      ON reservations (ends_at DESC NULLS LAST, id DESC);
CREATE INDEX idx_reservations_ends_asc       ON reservations (ends_at ASC NULLS FIRST, id ASC);

-- agent_enrollments: expires_at
CREATE INDEX idx_agent_enr_expires_id ON agent_enrollments (expires_at DESC, id DESC);
```

- [ ] **Step 2: Write the down migration**

Create `internal/store/migrations/000013_paginated_sort_indexes.down.sql`:

```sql
DROP INDEX IF EXISTS idx_jobs_name_id;
DROP INDEX IF EXISTS idx_jobs_priority_id;
DROP INDEX IF EXISTS idx_jobs_status_id;
DROP INDEX IF EXISTS idx_jobs_updated_id;

DROP INDEX IF EXISTS idx_workers_name_id;
DROP INDEX IF EXISTS idx_workers_status_id;
DROP INDEX IF EXISTS idx_workers_last_seen_desc;
DROP INDEX IF EXISTS idx_workers_last_seen_asc;

DROP INDEX IF EXISTS idx_users_name_id;
DROP INDEX IF EXISTS idx_users_email_id;

DROP INDEX IF EXISTS idx_sched_jobs_name_id;
DROP INDEX IF EXISTS idx_sched_jobs_next_run_id;
DROP INDEX IF EXISTS idx_sched_jobs_updated_id;

DROP INDEX IF EXISTS idx_reservations_name_id;
DROP INDEX IF EXISTS idx_reservations_starts_desc;
DROP INDEX IF EXISTS idx_reservations_starts_asc;
DROP INDEX IF EXISTS idx_reservations_ends_desc;
DROP INDEX IF EXISTS idx_reservations_ends_asc;

DROP INDEX IF EXISTS idx_agent_enr_expires_id;
```

- [ ] **Step 3: Verify migration syntax by running the existing integration suite**

Migrations are embedded and run at server startup. Any integration test that boots `relay-server` exercises them. Run a known-good integration test as a smoke check:

Run: `go test -tags integration -p 1 ./internal/api/... -run "TestListWorkers_Pagination" -v -timeout 180s`
Expected: PASS. If migration fails to apply, the testcontainer setup will surface the error in the log.

- [ ] **Step 4: Commit**

```bash
git add internal/store/migrations/000013_paginated_sort_indexes.up.sql \
        internal/store/migrations/000013_paginated_sort_indexes.down.sql
git commit -m "store/migrations: add composite indexes for paginated sort keys"
```

---

## Task 6: Wire `/v1/jobs` end-to-end (SQL queries, handler dispatch, integration tests)

**Files:**
- Modify: `internal/store/query/jobs.sql`
- Modify: `internal/api/jobs.go`
- Create: `internal/api/jobs_sort_integration_test.go`

This task establishes the pattern that Tasks 7–11 repeat verbatim for the remaining endpoints. Read it carefully before moving on.

The jobs allowlist (per spec): default `-created_at`, additional keys `name`, `priority`, `status`, `updated_at`. Each non-default key needs two sqlc queries (asc + desc) → 8 new queries.

- [ ] **Step 1: Add the 8 new sqlc queries to `internal/store/query/jobs.sql`**

Append to `internal/store/query/jobs.sql`. The existing `ListJobsWithEmailPage` stays as-is; the new queries follow the same JOIN + email shape:

```sql
-- name: ListJobsWithEmailPageByNameDesc :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE NOT @cursor_set::bool OR (j.name, j.id) < (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.name DESC, j.id DESC
LIMIT @page_limit;

-- name: ListJobsWithEmailPageByNameAsc :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE NOT @cursor_set::bool OR (j.name, j.id) > (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.name ASC, j.id ASC
LIMIT @page_limit;

-- name: ListJobsWithEmailPageByPriorityDesc :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE NOT @cursor_set::bool OR (j.priority, j.id) < (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.priority DESC, j.id DESC
LIMIT @page_limit;

-- name: ListJobsWithEmailPageByPriorityAsc :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE NOT @cursor_set::bool OR (j.priority, j.id) > (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.priority ASC, j.id ASC
LIMIT @page_limit;

-- name: ListJobsWithEmailPageByStatusDesc :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE NOT @cursor_set::bool OR (j.status, j.id) < (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.status DESC, j.id DESC
LIMIT @page_limit;

-- name: ListJobsWithEmailPageByStatusAsc :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE NOT @cursor_set::bool OR (j.status, j.id) > (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.status ASC, j.id ASC
LIMIT @page_limit;

-- name: ListJobsWithEmailPageByUpdatedDesc :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE NOT @cursor_set::bool OR (j.updated_at, j.id) < (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY j.updated_at DESC, j.id DESC
LIMIT @page_limit;

-- name: ListJobsWithEmailPageByUpdatedAsc :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE NOT @cursor_set::bool OR (j.updated_at, j.id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY j.updated_at ASC, j.id ASC
LIMIT @page_limit;
```

- [ ] **Step 2: Run `make generate`**

Run: `make generate`
Expected: regenerates `internal/store/jobs.sql.go` with eight new `ListJobsWithEmailPageBy*` methods on `*Queries`. No diff in any other generated file (since only jobs.sql changed).

Verify: `git diff --stat internal/store/` should show changes only to `jobs.sql.go` (plus the `.sql` edit itself).

- [ ] **Step 3: Wire the per-endpoint sortSpec and dispatch switch in the handler**

In `internal/api/jobs.go`, replace the temporary default-only spec from Task 3 with the real spec and add the dispatch switch.

At the top of the file's `var` block (or just above `handleListJobs`):

```go
var jobsSortSpec = sortSpec{
	Default: "-created_at",
	Keys: map[string]sortKeyKind{
		"created_at": sortKeyTimestamp,
		"name":       sortKeyText,
		"priority":   sortKeyText,
		"status":     sortKeyText,
		"updated_at": sortKeyTimestamp,
	},
}
```

Replace the body of the "Default branch: no filter" portion of `handleListJobs` with a dispatch switch. The filtered branches (`?status=` and `?scheduled_job_id=`) keep their default sort and reject `?sort=` per the spec:

```go
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, jobsSortSpec)
	if !ok {
		return
	}

	// Filtered variants — sort= not yet supported.
	hasSort := r.URL.Query().Get("sort") != ""
	hasFilter := r.URL.Query().Get("status") != "" || r.URL.Query().Get("scheduled_job_id") != ""
	if hasSort && hasFilter {
		writeError(w, http.StatusBadRequest, "sort not supported on filtered list variant; remove the filter or remove the sort")
		return
	}

	// (Filtered branches unchanged — they still call ListJobsByStatusWithEmailPage
	// and ListJobsByScheduledJobWithEmailPage with pp.CursorTs() and pp.Cursor.ID.)

	if schedIDStr := r.URL.Query().Get("scheduled_job_id"); schedIDStr != "" {
		// ...existing code from current handleListJobs, unchanged...
	}
	if status := r.URL.Query().Get("status"); status != "" {
		// ...existing code, unchanged...
	}

	// Default branch: dispatch on pp.Sort.
	rows, next, err := s.listJobsBySort(ctx, pp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list jobs failed")
		return
	}
	total, err := s.q.CountJobs(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count jobs failed")
		return
	}
	writeJSON(w, http.StatusOK, page[jobResponse]{Items: rows, NextCursor: next, Total: total})
}

// listJobsBySort dispatches the unfiltered jobs list to the right sqlc query
// for the requested sort. Returns the converted response items and the
// next-cursor string.
func (s *Server) listJobsBySort(ctx context.Context, pp pageParams) ([]jobResponse, string, error) {
	// Common cursor payloads (only one is consumed per branch).
	cursorTs := pgtype.Timestamptz{Time: pp.Cursor.T, Valid: pp.Cursor.Set}
	cursorStr := pp.Cursor.StrVal
	cursorID := pp.Cursor.ID
	cursorSet := pp.Cursor.Set
	limit := pp.Limit + 1 // fetch one extra for has-more detection

	switch pp.Sort {
	case "-created_at":
		rows, err := s.q.ListJobsWithEmailPage(ctx, store.ListJobsWithEmailPageParams{
			CursorSet: cursorSet, CursorTs: cursorTs, CursorID: cursorID, PageLimit: limit,
		})
		if err != nil {
			return nil, "", err
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseDefault, jobsRowKeyDefault)
		return items, next, nil
	case "created_at":
		// asc variant of the default — covered in the same way once we add ListJobsWithEmailPageByCreatedAsc.
		// For this task, omit "created_at" (asc) from the allowlist and add it only when its query lands;
		// or alternatively, add the asc variant now alongside the existing desc.
		return nil, "", fmt.Errorf("created_at asc not implemented")
	case "-name":
		rows, err := s.q.ListJobsWithEmailPageByNameDesc(ctx, store.ListJobsWithEmailPageByNameDescParams{
			CursorSet: cursorSet, CursorV: cursorStr, CursorID: cursorID, PageLimit: limit,
		})
		if err != nil { return nil, "", err }
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByName, jobsRowKeyByName)
		return items, next, nil
	case "name":
		rows, err := s.q.ListJobsWithEmailPageByNameAsc(ctx, store.ListJobsWithEmailPageByNameAscParams{
			CursorSet: cursorSet, CursorV: cursorStr, CursorID: cursorID, PageLimit: limit,
		})
		if err != nil { return nil, "", err }
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByName, jobsRowKeyByName)
		return items, next, nil
	case "-priority":
		// ...analogous to -name, using ListJobsWithEmailPageByPriorityDesc and jobsRowKeyByPriority
	case "priority":
		// ...analogous to name (asc)
	case "-status":
		// ...
	case "status":
		// ...
	case "-updated_at":
		rows, err := s.q.ListJobsWithEmailPageByUpdatedDesc(ctx, store.ListJobsWithEmailPageByUpdatedDescParams{
			CursorSet: cursorSet, CursorTs: cursorTs, CursorID: cursorID, PageLimit: limit,
		})
		if err != nil { return nil, "", err }
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByUpdated, jobsRowKeyByUpdated)
		return items, next, nil
	case "updated_at":
		// ...analogous to -updated_at, using ListJobsWithEmailPageByUpdatedAsc
	}
	return nil, "", fmt.Errorf("unhandled sort %q", pp.Sort)
}
```

Add the new row-key and row-to-response helpers in the same file. The shape mirrors `jobsRowKeyDefault` / `jobRowToResponseDefault`, but the row type and the value pulled into the cursor differ:

```go
func jobsRowKeyByName(r store.ListJobsWithEmailPageByNameDescRow) (anySortVal, pgtype.UUID) {
	return r.Name, r.ID
}
// ...one per (key, direction) — but since the row shape is identical for
// asc and desc within a given key, you can reuse one row-key helper if the
// generated types are identical (verify in the regenerated jobs.sql.go).

func jobRowToResponseByName(r store.ListJobsWithEmailPageByNameDescRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}
```

**Two notes on the dispatch switch:**

1. `created_at` ascending was not in the original allowlist table; for symmetry, include it now by adding `ListJobsWithEmailPageByCreatedAsc` to `jobs.sql` (the desc form already exists as the legacy `ListJobsWithEmailPage`). Update `jobsSortSpec.Keys` accordingly (the table already lists `created_at` as the default key, so `created_at` asc was implicit — make it explicit).
2. If two generated row types are byte-identical (same column list), sqlc may collapse them; verify with `grep "type ListJobsWithEmailPageBy" internal/store/jobs.sql.go` after `make generate` and reuse a single row-key helper across the two directions.

- [ ] **Step 4: Add the `created_at` asc variant to `jobs.sql` and re-generate**

Append to `internal/store/query/jobs.sql`:

```sql
-- name: ListJobsWithEmailPageByCreatedAsc :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE NOT @cursor_set::bool OR (j.created_at, j.id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY j.created_at ASC, j.id ASC
LIMIT @page_limit;
```

Run `make generate`. Wire the `case "created_at":` arm of the switch to this new query.

- [ ] **Step 5: Compile**

Run: `go build ./...`
Expected: clean build. Fix any sqlc parameter-naming mismatches (sqlc may camelCase `@cursor_v` to `CursorV`; check the generated `*Params` struct field names against the handler).

- [ ] **Step 6: Write the integration test file**

Create `internal/api/jobs_sort_integration_test.go`:

```go
//go:build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobsSortKeys enumerates every (key, direction) pair in jobsSortSpec.
// New keys added to the allowlist get coverage by appending here.
var jobsSortKeys = []string{
	"-created_at", "created_at",
	"-name", "name",
	"-priority", "priority",
	"-status", "status",
	"-updated_at", "updated_at",
}

func TestListJobs_Sort_OrderingAcrossKeys(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	token := env.createUserAndLogin(t, "sort@test.local", "p4ssw0rd!")

	// Seed 10 jobs with names/priorities/statuses chosen so that the
	// default created_at order differs from each sort key's order.
	priorities := []string{"low", "normal", "high", "critical", "normal", "low", "high", "critical", "low", "normal"}
	statuses := []string{"pending", "running", "succeeded", "failed", "running", "pending", "succeeded", "failed", "running", "succeeded"}
	for i := 0; i < 10; i++ {
		env.submitJobWithFields(t, token,
			fmt.Sprintf("job-%02d", 9-i), // names descending in creation order
			priorities[i],
			statuses[i],
		)
		time.Sleep(2 * time.Millisecond) // ensure distinct created_at
	}

	for _, sortKey := range jobsSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			items := env.listJobsSorted(t, token, sortKey, 50, "")
			require.Len(t, items, 10)
			assertSorted(t, items, sortKey)
		})
	}
}

func TestListJobs_Sort_PaginationAcrossPages(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()
	token := env.createUserAndLogin(t, "page@test.local", "p4ssw0rd!")
	for i := 0; i < 10; i++ {
		env.submitJobWithFields(t, token, fmt.Sprintf("p-%02d", i), "normal", "pending")
		time.Sleep(2 * time.Millisecond)
	}

	for _, sortKey := range jobsSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			single := env.listJobsSorted(t, token, sortKey, 50, "")

			// Walk in pages of 3.
			var paged []map[string]any
			cursor := ""
			for {
				items, next := env.listJobsSortedRaw(t, token, sortKey, 3, cursor)
				paged = append(paged, items...)
				if next == "" {
					break
				}
				cursor = next
			}
			require.Equal(t, len(single), len(paged), "paged length mismatch for sort=%s", sortKey)
			for i := range single {
				assert.Equal(t, single[i]["id"], paged[i]["id"], "row %d differs for sort=%s", i, sortKey)
			}
		})
	}
}

func TestListJobs_Sort_CursorMismatchRejected(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()
	token := env.createUserAndLogin(t, "mismatch@test.local", "p4ssw0rd!")
	for i := 0; i < 5; i++ {
		env.submitJobWithFields(t, token, fmt.Sprintf("m-%02d", i), "normal", "pending")
	}

	// Get a cursor under sort=-name.
	_, next := env.listJobsSortedRaw(t, token, "-name", 2, "")
	require.NotEmpty(t, next, "expected has-more cursor")

	// Resend under sort=-priority — must 400.
	q := url.Values{}
	q.Set("sort", "-priority")
	q.Set("cursor", next)
	q.Set("limit", "2")
	resp := env.do(t, "GET", "/v1/jobs?"+q.Encode(), nil, token)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// assertSorted decodes the cursor-less items and confirms the field implied
// by sortKey is monotonic in the right direction.
func assertSorted(t *testing.T, items []map[string]any, sortKey string) {
	t.Helper()
	desc := false
	key := sortKey
	if len(sortKey) > 0 && sortKey[0] == '-' {
		desc = true
		key = sortKey[1:]
	}
	for i := 1; i < len(items); i++ {
		a, _ := items[i-1][key].(string)
		b, _ := items[i][key].(string)
		if desc {
			assert.GreaterOrEqual(t, a, b, "sort=%s not monotone at i=%d (%v vs %v)", sortKey, i, a, b)
		} else {
			assert.LessOrEqual(t, a, b, "sort=%s not monotone at i=%d (%v vs %v)", sortKey, i, a, b)
		}
	}
}
```

This file assumes test helpers exist on `*testEnv`:
- `submitJobWithFields(t, token, name, priority, status)` — wraps the existing job-submission helper.
- `listJobsSorted(t, token, sort, limit, cursor) []map[string]any` — returns the `items` slice from a single GET.
- `listJobsSortedRaw(t, token, sort, limit, cursor) (items, nextCursor)` — same but also returns the cursor string.

Check `internal/api/api_test_helpers.go` (or wherever `testEnv` lives — likely a `helpers_test.go` or similar — `grep -rn "type testEnv" internal/api/`) and add any missing helpers there in this same task. The existing tests already submit jobs; the new helper just generalizes those calls.

- [ ] **Step 7: Run unit tests, then integration tests**

Run: `go test ./internal/api/... -v -timeout 60s`
Expected: existing unit tests still pass; no new unit tests in this task.

Run: `go test -tags integration -p 1 ./internal/api/... -run "TestListJobs_Sort" -v -timeout 300s`
Expected: all three new integration tests pass; total of `2 + 1 = 3` test functions, with the first two each running through `len(jobsSortKeys) = 10` subtests.

- [ ] **Step 8: Commit**

```bash
git add internal/store/query/jobs.sql internal/store/jobs.sql.go \
        internal/api/jobs.go internal/api/jobs_sort_integration_test.go \
        internal/api/<helpers file if changed>
git commit -m "api/jobs: configurable ?sort= over name/priority/status/updated_at

Adds ListJobsWithEmailPageBy{Name,Priority,Status,Updated,Created}{Asc,Desc}
sqlc queries, the jobsSortSpec allowlist, dispatch switch in handleListJobs,
and Tier 2 integration tests that walk every (key, direction) pair under
both single-page and paginated retrieval, plus a cursor/sort mismatch
rejection test.

Filtered branches (?status=, ?scheduled_job_id=) still reject ?sort=
with 400 per the design."
```

---

## Task 7: Wire `/v1/workers` end-to-end

**Files:**
- Modify: `internal/store/query/workers.sql`
- Modify: `internal/api/workers.go`
- Create: `internal/api/workers_sort_integration_test.go`

Workers allowlist: default `-created_at`, additional `name`, `status`, `last_seen_at`. `last_seen_at` is nullable — its cursor predicate needs explicit null handling.

- [ ] **Step 1: Add the sqlc queries**

Append to `internal/store/query/workers.sql`. For non-null text keys, mirror the jobs pattern. For `last_seen_at`, the predicate must handle nulls:

```sql
-- name: ListWorkersPageByCreatedAsc :many
SELECT * FROM workers
WHERE NOT @cursor_set::bool OR (created_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY created_at ASC, id ASC
LIMIT @page_limit;

-- name: ListWorkersPageByNameDesc :many
SELECT * FROM workers
WHERE NOT @cursor_set::bool OR (name, id) < (@cursor_v::text, @cursor_id::uuid)
ORDER BY name DESC, id DESC
LIMIT @page_limit;

-- name: ListWorkersPageByNameAsc :many
SELECT * FROM workers
WHERE NOT @cursor_set::bool OR (name, id) > (@cursor_v::text, @cursor_id::uuid)
ORDER BY name ASC, id ASC
LIMIT @page_limit;

-- name: ListWorkersPageByStatusDesc :many
SELECT * FROM workers
WHERE NOT @cursor_set::bool OR (status, id) < (@cursor_v::text, @cursor_id::uuid)
ORDER BY status DESC, id DESC
LIMIT @page_limit;

-- name: ListWorkersPageByStatusAsc :many
SELECT * FROM workers
WHERE NOT @cursor_set::bool OR (status, id) > (@cursor_v::text, @cursor_id::uuid)
ORDER BY status ASC, id ASC
LIMIT @page_limit;

-- name: ListWorkersPageByLastSeenDesc :many
-- DESC NULLS LAST. Cursor predicate splits on whether the cursor value
-- is itself null: a null cursor means "we're already in the null tail",
-- so only rows with id < cursor_id qualify; a non-null cursor means
-- "still in the non-null head", so the next page is anything with a
-- last_seen_at strictly less than the cursor, OR equal-and-id-less, OR
-- any null (since nulls come after in DESC NULLS LAST).
SELECT * FROM workers
WHERE NOT @cursor_set::bool
   OR (
       CASE WHEN @cursor_ts_valid::bool THEN
            (last_seen_at IS NULL)
         OR (last_seen_at IS NOT NULL AND
             (last_seen_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
       ELSE
            (last_seen_at IS NULL AND id < @cursor_id::uuid)
       END
   )
ORDER BY last_seen_at DESC NULLS LAST, id DESC
LIMIT @page_limit;

-- name: ListWorkersPageByLastSeenAsc :many
-- ASC NULLS FIRST. Mirror image: a null cursor means "still in null head",
-- so move to id > cursor_id or any non-null; a non-null cursor means we're
-- in the non-null tail, so just (last_seen_at, id) > cursor.
SELECT * FROM workers
WHERE NOT @cursor_set::bool
   OR (
       CASE WHEN @cursor_ts_valid::bool THEN
            (last_seen_at IS NOT NULL AND
             (last_seen_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
       ELSE
            (last_seen_at IS NOT NULL)
         OR (last_seen_at IS NULL AND id > @cursor_id::uuid)
       END
   )
ORDER BY last_seen_at ASC NULLS FIRST, id ASC
LIMIT @page_limit;
```

**Important:** the `cursor_ts_valid` parameter is new — it tells the query whether the cursor's timestamp is null (the previous page ended on a null `last_seen_at` row). In Go, this is `pp.Cursor.Set && !pp.Cursor.T.IsZero()`. Plumb a `cursorTsValid bool` through `pageParams` if you need it broadly, or compute it inline in the handler before calling the query.

- [ ] **Step 2: Run `make generate`**

- [ ] **Step 3: Add `workersSortSpec`, dispatch switch, row-key/row-conv helpers in `internal/api/workers.go`**

Pattern identical to Task 6 jobs handler. Allowlist:

```go
var workersSortSpec = sortSpec{
	Default: "-created_at",
	Keys: map[string]sortKeyKind{
		"created_at":   sortKeyTimestamp,
		"name":         sortKeyText,
		"status":       sortKeyText,
		"last_seen_at": sortKeyTimestamp,
	},
}
```

For the nullable `last_seen_at` cursor, the row-key callback must return a `time.Time` (the row's `last_seen_at.Time`) when the timestamp is valid, and a sentinel "is-null" marker otherwise. Simplest: have the row-key callback return a `*time.Time` (nil for null), and update `encodeCursorV2` to accept `*time.Time` as a third value-type case that encodes via setting the `T` field to empty and adding a sibling `n` bool — OR keep `encodeCursorV2` strict and emit two different cursor shapes by setting `T` to empty string + a separate `null` field.

**Recommended simpler approach:** widen the cursor wire format with one more field `N bool` ("value is null"); when set, the timestamp/value is ignored on decode. Add to `cursorWire`:

```go
type cursorWire struct {
    T string `json:"t,omitempty"`
    I string `json:"i"`
    S string `json:"s,omitempty"`
    V string `json:"v,omitempty"`
    N bool   `json:"n,omitempty"` // sort value is NULL
}
```

And in `encodeCursorV2`, add a `*time.Time` case:

```go
case *time.Time:
    if v == nil {
        w.N = true
    } else {
        w.T = v.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
    }
```

Plumb `N` into `cursor.IsNull bool` on decode. Add a unit test in `pagination_test.go` covering the null-timestamp round-trip before wiring this into the workers handler.

- [ ] **Step 4: Create `internal/api/workers_sort_integration_test.go`**

Same shape as `jobs_sort_integration_test.go`, with workers-specific seed data:

```go
var workersSortKeys = []string{
	"-created_at", "created_at",
	"-name", "name",
	"-status", "status",
	"-last_seen_at", "last_seen_at",
}
```

Seed ~8 workers with distinct names, distinct statuses, and a mix of `last_seen_at` values where 2 are NULL (never-seen workers) to exercise the null-handling cursor path. Run the same three test scenarios from jobs (ordering, paginated walk, mismatch rejection).

- [ ] **Step 5: Run tests**

Run: `go test ./internal/api/... -run "TestCursor|TestParse|TestBuildPage" -v -timeout 30s`
Run: `go test -tags integration -p 1 ./internal/api/... -run "TestListWorkers_Sort" -v -timeout 300s`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/pagination.go internal/api/pagination_test.go \
        internal/store/query/workers.sql internal/store/workers.sql.go \
        internal/api/workers.go internal/api/workers_sort_integration_test.go
git commit -m "api/workers: configurable ?sort= over name/status/last_seen_at

Adds nullable-timestamp handling to the cursor wire format (N field) so
last_seen_at sorts correctly across the NULL boundary."
```

---

## Task 8: Wire `/v1/users` end-to-end (both auth-driven variants)

**Files:**
- Modify: `internal/store/query/users.sql`
- Modify: `internal/api/users.go`
- Create: `internal/api/users_sort_integration_test.go`

Users allowlist: default `-created_at`, additional `name`, `email`. Two variant queries (`ListUsersPage` and `ListUsersIncludingArchivedPage`) each need the full per-key matrix because variant selection is auth-driven (caller can't opt out).

- [ ] **Step 1: Add 8 sqlc queries to `internal/store/query/users.sql`**

For each variant (active-only and including-archived), add `ByName{Asc,Desc}`, `ByEmail{Asc,Desc}`, and `ByCreatedAsc` (desc already exists as the base). 2 variants × (2 keys × 2 dirs + 1 created_asc) = 10 new queries. Follow the jobs pattern; the `WHERE archived_at IS NULL` clause on the active-only variant is preserved on every new query.

- [ ] **Step 2: Run `make generate`**

- [ ] **Step 3: Wire `usersSortSpec` and dispatch in `internal/api/users.go`**

```go
var usersSortSpec = sortSpec{
	Default: "-created_at",
	Keys: map[string]sortKeyKind{
		"created_at": sortKeyTimestamp,
		"name":       sortKeyText,
		"email":      sortKeyText,
	},
}
```

The handler already branches on archived vs not based on `AuthUser.IsAdmin` (or a query param — check the current code). Each branch gets its own dispatch switch.

- [ ] **Step 4: Create `internal/api/users_sort_integration_test.go`**

Mirror the jobs test pattern. Seed users with distinct names and emails so the orderings are unambiguous. Test both variant paths by running once as a non-admin (active-only) and once as admin (including-archived).

- [ ] **Step 5: Run tests**

Run: `go test -tags integration -p 1 ./internal/api/... -run "TestListUsers_Sort" -v -timeout 300s`
Expected: PASS for both variants.

- [ ] **Step 6: Commit**

```bash
git add internal/store/query/users.sql internal/store/users.sql.go \
        internal/api/users.go internal/api/users_sort_integration_test.go
git commit -m "api/users: configurable ?sort= over name/email (both variants)"
```

---

## Task 9: Wire `/v1/scheduled-jobs` end-to-end (both auth-driven variants)

**Files:**
- Modify: `internal/store/query/scheduled_jobs.sql`
- Modify: `internal/api/scheduled_jobs.go`
- Create: `internal/api/scheduled_jobs_sort_integration_test.go`

Scheduled-jobs allowlist: default `-created_at`, additional `name`, `next_run_at`, `updated_at`. Two variant queries (`ListScheduledJobsPage` admin and `ListScheduledJobsByOwnerPage` non-admin). 2 variants × (3 keys × 2 dirs + 1 created_asc) = 14 new queries.

Same pattern as Tasks 6/8. None of the columns are nullable for this endpoint, so no null-handling needed.

- [ ] **Step 1: Add the sqlc queries; `make generate`**
- [ ] **Step 2: Add `scheduledJobsSortSpec` and dispatch**
- [ ] **Step 3: Add integration test file with both auth variants**
- [ ] **Step 4: Run tests and commit**

```bash
git commit -m "api/scheduled_jobs: configurable ?sort= over name/next_run_at/updated_at (both variants)"
```

---

## Task 10: Wire `/v1/reservations` end-to-end (nullable timestamps)

**Files:**
- Modify: `internal/store/query/reservations.sql`
- Modify: `internal/api/reservations.go`
- Create: `internal/api/reservations_sort_integration_test.go`

Reservations allowlist: default `-created_at`, additional `name`, `starts_at` (nullable), `ends_at` (nullable). Reuses the null-handling cursor mechanism added in Task 7.

- [ ] **Step 1: Add sqlc queries; `make generate`**

7 new queries (`ByCreatedAsc` + 3 keys × 2 dirs). The two nullable-timestamp keys use the same `CASE WHEN @cursor_ts_valid::bool THEN ... END` pattern as workers `last_seen_at`.

- [ ] **Step 2: Add `reservationsSortSpec` and dispatch**

- [ ] **Step 3: Create integration test; seed rows with both NULL and non-NULL `starts_at` / `ends_at`**

- [ ] **Step 4: Run tests and commit**

```bash
git commit -m "api/reservations: configurable ?sort= over name/starts_at/ends_at"
```

---

## Task 11: Wire `/v1/agent-enrollments` end-to-end

**Files:**
- Modify: `internal/store/query/agent_enrollments.sql`
- Modify: `internal/api/agent_enrollments.go`
- Create: `internal/api/agent_enrollments_sort_integration_test.go`

Agent-enrollments allowlist: default `-created_at`, additional `expires_at`. 3 new queries (`ByCreatedAsc`, `ByExpiresDesc`, `ByExpiresAsc`).

Note the existing query already filters `WHERE consumed_at IS NULL` (only active enrollments). Preserve this clause in every new query.

- [ ] **Step 1: Add sqlc queries; `make generate`**
- [ ] **Step 2: Add `agentEnrollmentsSortSpec` and dispatch**
- [ ] **Step 3: Integration test**
- [ ] **Step 4: Run tests and commit**

```bash
git commit -m "api/agent_enrollments: configurable ?sort= over expires_at"
```

---

## Task 12: Add `--sort` flag to CLI list subcommands

**Files:**
- Modify: `internal/cli/jobs.go`, `internal/cli/workers.go`, `internal/cli/users.go`, `internal/cli/schedules.go`, `internal/cli/reservations.go`
- Test: `internal/cli/jobs_test.go` (or wherever per-command CLI tests live — `grep -rn "func TestJobsList" internal/cli/` to locate)

The CLI does not duplicate the server-side allowlist — it passes whatever the user supplied through to `?sort=` verbatim. Invalid values produce a clean 400 from the server.

- [ ] **Step 1: Add the flag to each list subcommand**

For each affected command, find the existing list subcommand definition (look for the `flag.NewFlagSet` block) and add:

```go
sort := fs.String("sort", "", "sort order; e.g. -priority or name (server-validated)")
```

In the request-building section, conditionally add the param:

```go
if *sort != "" {
    q.Set("sort", *sort)
}
```

- [ ] **Step 2: Add a unit test per command verifying the flag becomes the right query string**

Use `httptest.NewServer` to capture the outgoing query string. Pattern (one example):

```go
func TestJobsList_SortFlag(t *testing.T) {
	var capturedRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRawQuery = r.URL.RawQuery
		w.Write([]byte(`{"items":[],"next_cursor":"","total":0}`))
	}))
	defer srv.Close()

	cmd := newJobsListCommand() // existing factory or constructor
	require.NoError(t, cmd.Run(context.Background(), []string{"--sort", "-priority", "--server", srv.URL, "--token", "fake"}))
	assert.Contains(t, capturedRawQuery, "sort=-priority")
}
```

Replicate the test for workers, users, schedules, reservations.

- [ ] **Step 3: Run CLI unit tests**

Run: `go test ./internal/cli/... -v -timeout 30s`
Expected: PASS, including the five new flag tests.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/jobs.go internal/cli/workers.go internal/cli/users.go \
        internal/cli/schedules.go internal/cli/reservations.go \
        internal/cli/*_test.go
git commit -m "cli: add --sort flag to list subcommands

Pass-through to server's ?sort=; the server validates against the
per-endpoint allowlist."
```

---

## Task 13: Add `sort` parameter to MCP list tools + drift test

**Files:**
- Modify: `internal/mcp/jobs.go`, `internal/mcp/workers.go`, `internal/mcp/schedules.go`, `internal/mcp/reservations.go`
- Test: `internal/mcp/sort_drift_test.go` (new)

- [ ] **Step 1: Add the `sort` field to each tool's input struct**

For each list tool, locate the input struct (likely `type listJobsInput struct { ... }` near the tool's registration). Add:

```go
type listJobsInput struct {
	// ...existing fields...
	Sort        string `json:"sort,omitempty"`
	description string `json:"-"` // ignored by serde
}
```

Update the description string in the tool registration to document the allowed sort values inline. Use the canonical list for that endpoint. Example for jobs:

```go
SortDescription = "Sort order. One of \"created_at\", \"-created_at\" (default), \"name\", \"-name\", \"priority\", \"-priority\", \"status\", \"-status\", \"updated_at\", \"-updated_at\". Prefix '-' reverses to descending."
```

In the tool handler, forward `Sort` to the underlying `relayclient.Client.ListJobs(...)` (which should already accept arbitrary query params via the same mechanism as the CLI; if not, add a `Sort` field to the client's `ListOptions` struct).

- [ ] **Step 2: Write the drift test**

Create `internal/mcp/sort_drift_test.go`:

```go
package mcp

import (
	"testing"

	"relay/internal/api"
	"github.com/stretchr/testify/assert"
)

// TestMCPSortDescriptionsMatchServerAllowlist asserts that every (key,
// direction) value advertised in an MCP tool's sort description is
// actually accepted by the corresponding server-side sortSpec. Drift
// (a key removed from the server but still listed in the MCP doc, or
// a new server key not surfaced to the LLM) fails CI.
func TestMCPSortDescriptionsMatchServerAllowlist(t *testing.T) {
	cases := []struct {
		name        string
		mcpKeys     []string
		serverSpec  api.SortSpec // export the per-endpoint spec from internal/api
	}{
		{"jobs", jobsMCPSortKeys, api.JobsSortSpec},
		{"workers", workersMCPSortKeys, api.WorkersSortSpec},
		{"schedules", schedulesMCPSortKeys, api.ScheduledJobsSortSpec},
		{"reservations", reservationsMCPSortKeys, api.ReservationsSortSpec},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Strip leading dashes; every base key in the MCP list must
			// appear in the server's Keys map.
			for _, k := range tc.mcpKeys {
				base := k
				if len(k) > 0 && k[0] == '-' {
					base = k[1:]
				}
				_, ok := tc.serverSpec.Keys[base]
				assert.True(t, ok, "MCP advertises sort key %q for %s but server allowlist omits it", k, tc.name)
			}
		})
	}
}
```

This requires exporting `sortSpec` and the per-endpoint vars from `internal/api`. Rename `sortSpec` → `SortSpec`, `sortKeyKind` → `SortKeyKind`, and capitalize the constant identifiers. Capitalize the per-endpoint specs (`jobsSortSpec` → `JobsSortSpec`, etc.) and update every internal reference in one sweep.

Define the per-tool slice constants in `internal/mcp/<tool>.go`:

```go
var jobsMCPSortKeys = []string{
	"created_at", "-created_at",
	"name", "-name",
	"priority", "-priority",
	"status", "-status",
	"updated_at", "-updated_at",
}
```

- [ ] **Step 3: Run MCP unit tests**

Run: `go test ./internal/mcp/... -v -timeout 30s`
Expected: PASS including the new drift test.

- [ ] **Step 4: Commit**

```bash
git add internal/api/pagination.go internal/api/jobs.go internal/api/workers.go \
        internal/api/users.go internal/api/scheduled_jobs.go \
        internal/api/reservations.go internal/api/agent_enrollments.go \
        internal/mcp/jobs.go internal/mcp/workers.go internal/mcp/schedules.go \
        internal/mcp/reservations.go internal/mcp/sort_drift_test.go
git commit -m "mcp: surface sort= on list tools; export api.SortSpec for drift test

Documents the per-tool allowlist inline so the LLM picks valid values
without external docs. The new sort_drift_test fails if an MCP tool
advertises a sort key the server doesn't accept (or vice versa)."
```

---

## Task 14: Documentation, `pg_indexes` test, README updates

**Files:**
- Modify: `README.md`
- Create: `internal/store/migrations_indexes_integration_test.go` (or similar — see below)

- [ ] **Step 1: Update the REST API section of `README.md`**

Find the "List endpoints" subsection (search for `?limit=` or `?cursor=`). Append a `?sort=` paragraph and the per-endpoint allowlist table from the spec (Section "Per-endpoint sort-key allowlist"). Mention the leading-dash convention, the default, and the cursor mismatch behaviour.

- [ ] **Step 2: Update the MCP section of `README.md`**

Find the tool reference for `relay_list_jobs`, `relay_list_workers`, `relay_list_schedules`, `relay_list_reservations`. Add a one-line `sort` parameter row to each, with a pointer to the REST allowlist table for the full list.

- [ ] **Step 3: Update CLI compatibility note**

Add a paragraph noting that `--sort` against a pre-feature server silently falls back to default ordering (the server ignores unknown query params).

- [ ] **Step 4: Add the `pg_indexes` existence test**

Create `internal/store/migrations_sort_indexes_integration_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	// import the test-env package that spins up Postgres for store tests
)

// TestSortIndexesExist confirms that migration 000013 created every
// composite index the new ?sort= feature depends on. Failing this test
// after adding a new sort key means the migration was not updated.
func TestSortIndexesExist(t *testing.T) {
	pool := newTestPool(t) // existing test helper; check internal/store for the right import
	defer pool.Close()

	expected := []string{
		"idx_jobs_name_id", "idx_jobs_priority_id", "idx_jobs_status_id", "idx_jobs_updated_id",
		"idx_workers_name_id", "idx_workers_status_id", "idx_workers_last_seen_desc", "idx_workers_last_seen_asc",
		"idx_users_name_id", "idx_users_email_id",
		"idx_sched_jobs_name_id", "idx_sched_jobs_next_run_id", "idx_sched_jobs_updated_id",
		"idx_reservations_name_id", "idx_reservations_starts_desc", "idx_reservations_starts_asc",
		"idx_reservations_ends_desc", "idx_reservations_ends_asc",
		"idx_agent_enr_expires_id",
	}

	rows, err := pool.Query(context.Background(), "SELECT indexname FROM pg_indexes WHERE schemaname = 'public'")
	require.NoError(t, err)
	defer rows.Close()

	got := map[string]bool{}
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		got[name] = true
	}
	require.NoError(t, rows.Err())

	for _, name := range expected {
		assert.True(t, got[name], "expected index %s not found in pg_indexes", name)
	}
}
```

If a `newTestPool(t)` helper doesn't exist in `internal/store/`, locate the testcontainers setup used by other store integration tests and adapt that file's setup (commonly named `setup_test.go` or similar).

- [ ] **Step 5: Run integration suite**

Run: `go test -tags integration -p 1 ./... -timeout 600s`
Expected: ALL pass — full integration suite green.

- [ ] **Step 6: Run unit suite**

Run: `make test`
Expected: PASS.

- [ ] **Step 7: Final commit**

```bash
git add README.md internal/store/migrations_sort_indexes_integration_test.go
git commit -m "docs: document ?sort= REST/MCP/CLI; add pg_indexes existence test"
```

---

## Manual Performance Verification (one-time, not committed)

Before merging the branch, run `EXPLAIN ANALYZE` against a populated dev `jobs` table to confirm the planner uses the new indexes:

```bash
psql relay_dev -c "EXPLAIN ANALYZE SELECT * FROM jobs ORDER BY priority DESC, id DESC LIMIT 50"
psql relay_dev -c "EXPLAIN ANALYZE SELECT * FROM jobs WHERE (priority, id) < ('high', '00000000-0000-0000-0000-000000000000'::uuid) ORDER BY priority DESC, id DESC LIMIT 50"
```

Expected output should include `Index Scan using idx_jobs_priority_id` (or backward scan thereof), NOT `Seq Scan` + `Sort`. If any new sort path falls back to a sort node, revisit the index definition before merging.

Document the EXPLAIN output in the post-merge retro.

---

## Self-Review Notes

Scanned the plan against the spec:

- **Spec coverage:** Each spec section is implemented by Tasks 1–14. Sort syntax (Task 2–3), cursor encoding (Task 1), allowlist per endpoint (Tasks 6–11), CLI flags (Task 12), MCP integration (Task 13), README (Task 14), `pg_indexes` test (Task 14), nullable timestamps (Tasks 7, 10), backward-compat for legacy cursors (Task 1).
- **Spec gaps the plan adds:** the cursor `N` field (null-value flag) for nullable timestamps; the `cursor_ts_valid` parameter on workers/reservations queries. Both are mechanical consequences of supporting nullable sort keys and are noted in the relevant tasks rather than left implicit.
- **No placeholders.** Where Tasks 8–11 say "mirror the jobs pattern" they point at Task 6's complete code block; the engineer reads Task 6 first and applies the same template per the per-endpoint allowlist.
- **Symbol consistency:** `sortSpec` / `SortSpec` rename happens in Task 13 in one atomic commit; every reference before that uses lowercase. `parseSort`, `parsePage`, `buildPage`, `cursor`, `cursorWire`, `encodeCursorV2`, `anySortVal` are all introduced in Tasks 1–4 and used consistently thereafter.
