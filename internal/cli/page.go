package cli

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// pageEnvelope mirrors the server's pagination envelope.
type pageEnvelope[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	Total      int64  `json:"total"`
}

// pageRequestLimit is the per-request limit the CLI uses when auto-paginating.
// 200 matches the server's max so we minimize round-trips.
const pageRequestLimit = 200

// fetchAllPages walks ?cursor= until next_cursor is empty, or until userLimit
// rows have been collected (when userLimit > 0). Returns the merged slice and
// the total reported by the first page response. Caller-supplied params are
// forwarded on every page request alongside ?limit=200&cursor=<...>.
func fetchAllPages[T any](
	ctx context.Context,
	c *Client,
	basePath string,
	params url.Values,
	userLimit int,
) ([]T, int64, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("limit", strconv.Itoa(pageRequestLimit))

	var (
		out    []T
		total  int64
		cursor string
		first  = true
	)
	for {
		if cursor != "" {
			params.Set("cursor", cursor)
		} else {
			params.Del("cursor")
		}
		path := basePath
		if encoded := params.Encode(); encoded != "" {
			path += "?" + encoded
		}
		var resp pageEnvelope[T]
		if err := c.do(ctx, "GET", path, nil, &resp); err != nil {
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
		cursor = resp.NextCursor
	}
}
