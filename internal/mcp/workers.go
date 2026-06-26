package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"relay/internal/relayclient"
)

type listWorkersArgs struct {
	Limit  int    `json:"limit"  jsonschema:"Maximum number of workers to return (1-200). Defaults to 50 when 0."`
	Cursor string `json:"cursor" jsonschema:"Pagination cursor from a previous response."`
	Sort   string `json:"sort"   jsonschema:"Sort order. One of \"created_at\", \"-created_at\" (default), \"name\", \"-name\", \"status\", \"-status\", \"last_seen_at\", \"-last_seen_at\". Prefix '-' reverses to descending."`
}

type getWorkerArgs struct {
	WorkerID string `json:"worker_id" jsonschema:"The worker ID to fetch."`
}

func (s *Server) registerWorkers() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_list_workers",
		Description: "List relay workers (agents) connected to the server.",
	}, s.callListWorkers)

	addTool(s, &mcpsdk.Tool{
		Name:        "relay_get_worker",
		Description: "Get details of a single relay worker by ID.",
	}, s.callGetWorker)
}

func (s *Server) callListWorkers(ctx context.Context, args listWorkersArgs) (map[string]any, *ToolError) {
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

	path := "/v1/workers?" + params.Encode()

	var resp relayclient.PageEnvelope[map[string]any]
	if err := s.do(ctx, "GET", path, nil, &resp); err != nil {
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

func (s *Server) callGetWorker(ctx context.Context, args getWorkerArgs) (map[string]any, *ToolError) {
	if args.WorkerID == "" {
		return nil, &ToolError{Code: "validation", Message: "worker_id is required"}
	}

	var resp map[string]any
	if err := s.do(ctx, "GET", fmt.Sprintf("/v1/workers/%s", args.WorkerID), nil, &resp); err != nil {
		return nil, MapError(err)
	}
	return resp, nil
}
