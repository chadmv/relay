package mcp

import (
	"context"
	"encoding/json"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"relay/internal/jobspec"
)

type submitJobArgs struct {
	JobSpec jobspec.JobSpec `json:"job_spec"`
}

func (s *Server) registerSubmit() {
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_submit_job",
		Description: "Submit a new relay job from a job spec. Validates the spec client-side before sending.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args submitJobArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callSubmitJob(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})
}

func (s *Server) callSubmitJob(ctx context.Context, args submitJobArgs) (map[string]any, *ToolError) {
	spec := args.JobSpec
	if err := jobspec.Validate(&spec); err != nil {
		return nil, &ToolError{Code: "validation", Message: err.Error(), Hint: "fix the job spec and try again"}
	}

	var resp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := s.client.Do(ctx, "POST", "/v1/jobs", spec, &resp); err != nil {
		return nil, MapError(err)
	}
	return map[string]any{"job_id": resp.ID, "status": resp.Status}, nil
}
