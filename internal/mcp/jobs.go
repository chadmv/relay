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

type listJobsArgs struct {
	Status string `json:"status" jsonschema:"description=Filter by job status (e.g. running, done, failed, cancelled). Empty returns all."`
	Limit  int    `json:"limit"  jsonschema:"description=Maximum number of jobs to return (1-200). Defaults to 50 when 0."`
	Cursor string `json:"cursor" jsonschema:"description=Pagination cursor from a previous response."`
}

type getJobArgs struct {
	JobID string `json:"job_id" jsonschema:"description=The job ID to fetch.,required"`
}

func (s *Server) registerJobs() {
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_list_jobs",
		Description: "List relay jobs with optional status filter and pagination.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args listJobsArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callListJobs(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})

	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_get_job",
		Description: "Get details of a single relay job by ID.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args getJobArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callGetJob(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})
}

func (s *Server) callListJobs(ctx context.Context, args listJobsArgs) (map[string]any, *ToolError) {
	params := url.Values{}
	if args.Status != "" {
		params.Set("status", args.Status)
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}
	params.Set("limit", strconv.Itoa(limit))
	if args.Cursor != "" {
		params.Set("cursor", args.Cursor)
	}

	path := "/v1/jobs"
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}

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

func (s *Server) callGetJob(ctx context.Context, args getJobArgs) (map[string]any, *ToolError) {
	if args.JobID == "" {
		return nil, &ToolError{Code: "validation", Message: "job_id is required"}
	}

	var resp map[string]any
	if err := s.client.Do(ctx, "GET", fmt.Sprintf("/v1/jobs/%s", args.JobID), nil, &resp); err != nil {
		return nil, MapError(err)
	}
	return resp, nil
}
