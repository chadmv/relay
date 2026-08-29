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
// 200 bytes. A real relay cursor is base64url of a ~96-byte {t,i,s} JSON
// (encodeCursorV2, internal/api/pagination.go), so ~128 bytes: every legitimate
// cursor is quoted in full and only a cursor no correct server emits is cut.
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

// FetchAllPages walks ?cursor= until next_cursor is empty, or until userLimit
// rows have been collected (when userLimit > 0). Returns the merged slice and
// the total reported by the first page response. Caller-supplied params are
// forwarded on every page request alongside ?limit=200&cursor=<...>.
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
		if resp.NextCursor == "" {
			return out, total, nil
		}
		// This arm MUST stay above the empty-page stop below. buildPage
		// (internal/api/pagination.go) returns ([]Out{}, "") for zero rows, so a
		// list with no matching rows is an empty page that reports itself
		// drained. Inverted, `relay list` fails against an empty jobs table.
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
		seen[resp.NextCursor] = struct{}{}
		cursor = resp.NextCursor
	}
}
