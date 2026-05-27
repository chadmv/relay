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

type listSchedulesArgs struct {
	Limit  int    `json:"limit"  jsonschema:"Maximum number of scheduled jobs to return (1-200). Defaults to 50 when 0."`
	Cursor string `json:"cursor" jsonschema:"Pagination cursor from a previous response."`
	Sort   string `json:"sort"   jsonschema:"Sort order. One of \"created_at\", \"-created_at\" (default), \"name\", \"-name\", \"next_run_at\", \"-next_run_at\", \"updated_at\", \"-updated_at\". Prefix '-' reverses to descending."`
}

type getScheduleArgs struct {
	ScheduleID string `json:"schedule_id" jsonschema:"The scheduled job ID to fetch."`
}

func (s *Server) registerSchedules() {
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_list_schedules",
		Description: "List relay scheduled jobs (cron schedules).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args listSchedulesArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callListSchedules(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})

	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_get_schedule",
		Description: "Get details of a single relay scheduled job by ID.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args getScheduleArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callGetSchedule(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})
}

func (s *Server) callListSchedules(ctx context.Context, args listSchedulesArgs) (map[string]any, *ToolError) {
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

	path := "/v1/scheduled-jobs?" + params.Encode()

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

func (s *Server) callGetSchedule(ctx context.Context, args getScheduleArgs) (map[string]any, *ToolError) {
	if args.ScheduleID == "" {
		return nil, &ToolError{Code: "validation", Message: "schedule_id is required"}
	}

	var resp map[string]any
	if err := s.client.Do(ctx, "GET", fmt.Sprintf("/v1/scheduled-jobs/%s", args.ScheduleID), nil, &resp); err != nil {
		return nil, MapError(err)
	}
	return resp, nil
}
