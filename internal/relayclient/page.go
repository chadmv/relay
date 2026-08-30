package relayclient

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// PageEnvelope mirrors the server's pagination envelope.
type PageEnvelope[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	Total      int64  `json:"total"`
}

// PageRequestLimit is the per-request limit the CLI uses when auto-paginating.
// 200 matches the server's max so we minimize round-trips.
const PageRequestLimit = 200

// maxCursorInMessage bounds how much of a server-supplied cursor an error
// message may quote. The cursor is chosen by the SERVER and its length is
// unbounded, so a message that interpolates it whole is unbounded too.
//
// 200 bytes does NOT cover every legitimate cursor, and no fixed number can:
// a TEXT-sort cursor carries the row's sort value, which is unbounded, so a
// CORRECT server emits cursors this code truncates. The cost of cutting one
// is cosmetic: quoteCursor reports the TRUE length beside the prefix, so a
// long-but-legitimate cursor stays distinguishable from a pathological one.
// What the bound buys is that the message length is the CLIENT's to choose.
const maxCursorInMessage = 200

// quoteCursor renders a server-supplied cursor for an error message, bounded.
// The prefix is cut at a BYTE boundary and may split a UTF-8 rune;
// strconv.Quote escapes the resulting invalid bytes rather than emitting them,
// which is the safe direction for a value the client does not control. The true
// length is reported, so a 5 MB cursor and a 201-byte one do not produce the
// same message.
func quoteCursor(cursor string) string {
	if len(cursor) <= maxCursorInMessage {
		return strconv.Quote(cursor)
	}
	return fmt.Sprintf("%s (truncated from %d bytes)",
		strconv.Quote(cursor[:maxCursorInMessage]), len(cursor))
}

// maxListPages bounds the NUMBER OF REQUESTS FetchAllPages makes against a
// server whose next_cursor keeps advancing but which never reports the list as
// drained - the case neither the empty-page stop nor the repeated-cursor stop
// can see. 10000 pages at PageRequestLimit rows is 2,000,000 rows.
//
// Requests is all it bounds. NewClient returns &http.Client{} with no Timeout
// and cmd/relay/main.go builds its context with signal.NotifyContext and no
// deadline, so wall clock, response bytes and the memory of one response are
// all open; they belong to
// bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout.
//
// Client EGRESS is open too, and it is the axis the CURSOR uniquely creates:
// the server picks the cursor and this loop echoes it straight back in the
// request URI, uncompressed, once per page, up to maxListPages times, and
// percent-encoding expands a cursor outside the base64url alphabet while the
// server can compress the same bytes inbound. A cursor that IS base64url does
// not expand at all, and a real relay-server answers 431, so the practical
// reach is a hostile endpoint the operator chose to point at. Named here
// because the tracked item above names response bytes and timeouts, not this.
//
// A var rather than a const so a test can shrink it. It is package-global
// state: a test that shrinks it must NOT call t.Parallel().
var maxListPages = 10000

// FetchAllPages walks ?cursor= until next_cursor is empty, or until userLimit
// rows have been collected (when userLimit > 0). Returns the merged slice and
// the total reported by the first page response. Caller-supplied params are
// forwarded on every page request alongside ?limit=200&cursor=<...>.
//
// Beyond the server's own drained signal the loop has THREE stops, and all
// three are needed. The cursor is server-supplied and drives a client loop, and
// the provenance of a value says nothing about who controls its content. An
// empty page that still advertises more catches a server the client cannot page
// at all; a repeated cursor catches a self-loop on request 2 and an A,B,A cycle
// on request 3; maxListPages catches an ever-advancing cursor that never drains,
// which neither of the other two can see.
//
// On any of those the return is `nil, 0, err` - NOT the partial slice: no
// caller has anywhere to put a partial list, and the existing transport-error
// path already returns nil, 0.
func FetchAllPages[T any](
	ctx context.Context,
	c *Client,
	basePath string,
	params url.Values,
	userLimit int,
) ([]T, int64, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("limit", strconv.Itoa(PageRequestLimit))

	var (
		out    []T
		total  int64
		cursor string
		first  = true
		pages  int
		seen   = map[string]struct{}{}
	)
	for {
		pages++
		if cursor != "" {
			params.Set("cursor", cursor)
		} else {
			params.Del("cursor")
		}
		path := basePath
		if encoded := params.Encode(); encoded != "" {
			path += "?" + encoded
		}
		var resp PageEnvelope[T]
		if err := c.Do(ctx, "GET", path, nil, &resp); err != nil {
			return nil, 0, fmt.Errorf("paginate %s: %w", basePath, err)
		}
		if first {
			total = resp.Total
			first = false
		}
		out = append(out, resp.Items...)
		if userLimit > 0 && len(out) >= userLimit {
			return out[:userLimit], total, nil
		}
		// THE DRAINED RETURN BELOW MUST STAY ABOVE THE EMPTY-PAGE STOP. buildPage
		// (internal/api/pagination.go) returns ([]Out{}, "") for zero rows, so a
		// list with no matching rows is an empty page that reports itself
		// drained, and so never reaches the stop. Inverted, `relay list` fails
		// against an empty jobs table.
		if resp.NextCursor == "" {
			return out, total, nil
		}
		if len(resp.Items) == 0 {
			return nil, 0, fmt.Errorf(
				"paginate %s: server returned an empty page while still advertising more rows (next_cursor %s)",
				basePath, quoteCursor(resp.NextCursor))
		}
		// The stop is: this walk already requested this cursor. A SET, not a
		// comparison against the previous cursor - the two catch different
		// things, and a two-cycle (A,B,A,B, which two replicas behind a load
		// balancer produce) is invisible to the comparison and runs to the page
		// cap. This is not a second stop; it is the container that implements
		// the one stop. Previous-cursor-only is this set restricted to its last
		// element.
		//
		// A repeated cursor is UNREACHABLE on a correct server: encodeCursorV2
		// (internal/api/pagination.go) encodes the LAST KEPT row's key, and the
		// next page's predicate is strictly past it with id as tiebreaker, so
		// cursor keys strictly decrease along a walk. Comparison is byte-exact
		// on the base64 string; this package never decodes it.
		//
		// No digest per entry. Beyond costing an exception to CLAUDE.md's rule
		// that all hashing goes through internal/tokenhash.Hash, the residual it
		// would close - a server sending one-item pages with multi-megabyte
		// cursors - is the unbounded-response-bytes axis owned by
		// bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout,
		// and the same attacker already has an equal retention channel through
		// Items.
		if _, ok := seen[resp.NextCursor]; ok {
			return nil, 0, fmt.Errorf(
				"paginate %s: server cursor did not advance - it repeated a cursor this walk had already requested (%s) after %d pages",
				basePath, quoteCursor(resp.NextCursor), pages)
		}
		if pages >= maxListPages {
			// This message reports what it has and asserts NEITHER possibility:
			// it does not blame the server, and it does not claim every row was
			// collected.
			//
			// Reaching this cap on a LIST means the server is misbehaving:
			// list queries fetch limit+1 rows and buildPage
			// (internal/api/pagination.go) emits a cursor only when that
			// extra row came back, so a list whose length is an exact
			// multiple of the page size drains at its last full page and
			// never reaches a cap at all. Settling
			// completeness here would settle it with `total`, a number that
			// same misbehaving actor supplies.
			//
			// Do NOT copy a task-log-style "may be incomplete" completeness
			// warning onto this count. Task-log paging is genuinely different:
			// GetTaskLogsPage (internal/store/query/tasks.sql) is `LIMIT $3`
			// with no over-fetch and handleGetTaskLogs (internal/api/tasks.go)
			// zeroes next_seq only when `len(items) < limit`, so a FULL last
			// log page really does carry a cursor and that walk really can stop
			// one request short of learning it was done. And this package could
			// not count completeness honestly anyway: T is a bare type
			// parameter with no constraint and no id, so counting distinct rows
			// would take reflection or a decode change, either of which couples
			// this leaf package to its callers' row shapes - and a count of
			// rows APPENDED is not a count of distinct rows received, since a
			// server re-serving a page behind an advancing cursor drives them
			// apart.
			//
			// `total` is the FIRST page's total (see `if first` above) - the
			// existing contract of this function's return value - so the message
			// says which page it came from rather than implying it is current.
			return nil, 0, fmt.Errorf(
				"paginate %s: truncated after %d pages - hit the client's page cap; %d rows collected, the server's first page reported %d, and it had not yet reported the list as drained",
				basePath, maxListPages, len(out), total)
		}
		seen[resp.NextCursor] = struct{}{}
		cursor = resp.NextCursor
	}
}
