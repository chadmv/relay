package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type cancelJobArgs struct {
	JobID string `json:"job_id" jsonschema:"The ID of the job to cancel."`
}

func (s *Server) registerCancel() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_cancel_job",
		Description: "Cancel a running or pending relay job by ID.",
	}, s.callCancelJob)
}

func (s *Server) callCancelJob(ctx context.Context, args cancelJobArgs) (map[string]any, *ToolError) {
	if args.JobID == "" {
		return nil, &ToolError{Code: "validation", Message: "job_id is required"}
	}

	var resp map[string]any
	if err := s.do(ctx, "DELETE", fmt.Sprintf("/v1/jobs/%s", args.JobID), nil, &resp); err != nil {
		return nil, MapError(err)
	}
	return resp, nil
}
