package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"relay/internal/relayclient"
)

type listWorkersArgs struct {
	Limit  int    `json:"limit"  jsonschema:"Maximum number of workers to return (1-200). Defaults to 50 when 0."`
	Cursor string `json:"cursor" jsonschema:"Pagination cursor from a previous response."`
}

type getWorkerArgs struct {
	WorkerID string `json:"worker_id" jsonschema:"The worker ID to fetch."`
}

func (s *Server) registerWorkers() {
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_list_workers",
		Description: "List relay workers (agents) connected to the server.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args listWorkersArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callListWorkers(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})

	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_get_worker",
		Description: "Get details of a single relay worker by ID.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args getWorkerArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callGetWorker(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})
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

	path := "/v1/workers?" + params.Encode()

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

func (s *Server) callGetWorker(ctx context.Context, args getWorkerArgs) (map[string]any, *ToolError) {
	if args.WorkerID == "" {
		return nil, &ToolError{Code: "validation", Message: "worker_id is required"}
	}

	var resp map[string]any
	if err := s.client.Do(ctx, "GET", fmt.Sprintf("/v1/workers/%s", args.WorkerID), nil, &resp); err != nil {
		return nil, MapError(err)
	}
	return resp, nil
}
