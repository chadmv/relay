package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type cancelJobArgs struct {
	JobID string `json:"job_id" jsonschema:"The ID of the job to cancel."`
}

func (s *Server) registerCancel() {
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_cancel_job",
		Description: "Cancel a running or pending relay job by ID.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args cancelJobArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callCancelJob(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})
}

func (s *Server) callCancelJob(ctx context.Context, args cancelJobArgs) (map[string]any, *ToolError) {
	if args.JobID == "" {
		return nil, &ToolError{Code: "validation", Message: "job_id is required"}
	}

	var resp map[string]any
	if err := s.client.Do(ctx, "DELETE", fmt.Sprintf("/v1/jobs/%s", args.JobID), nil, &resp); err != nil {
		return nil, MapError(err)
	}
	return resp, nil
}
