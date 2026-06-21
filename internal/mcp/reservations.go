package mcp

import (
	"context"
	"net/url"
	"strconv"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"relay/internal/relayclient"
)

type listReservationsArgs struct {
	Limit  int    `json:"limit"  jsonschema:"Maximum number of reservations to return (1-200). Defaults to 50 when 0."`
	Cursor string `json:"cursor" jsonschema:"Pagination cursor from a previous response."`
	Sort   string `json:"sort"   jsonschema:"Sort order. One of \"created_at\", \"-created_at\" (default), \"name\", \"-name\", \"starts_at\", \"-starts_at\", \"ends_at\", \"-ends_at\". Prefix '-' reverses to descending."`
}

func (s *Server) registerReservations() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_list_reservations",
		Description: "List relay reservations (admin-only). Returns a paginated list of worker reservations.",
	}, s.callListReservations)
}

func (s *Server) callListReservations(ctx context.Context, args listReservationsArgs) (map[string]any, *ToolError) {
	params := url.Values{}
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}
	params.Set("limit", strconv.Itoa(limit))
	if args.Cursor != "" {
		params.Set("cursor", args.Cursor)
	}
	if args.Sort != "" {
		params.Set("sort", args.Sort)
	}

	path := "/v1/reservations?" + params.Encode()

	var resp relayclient.PageEnvelope[map[string]any]
	if err := s.client.Do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, MapError(err)
	}

	items := make([]any, len(resp.Items))
	for i, item := range resp.Items {
		items[i] = item
	}
	return map[string]any{
		"items":       items,
		"next_cursor": resp.NextCursor,
		"total":       resp.Total,
	}, nil
}
