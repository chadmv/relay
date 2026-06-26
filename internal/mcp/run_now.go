package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type runScheduleNowArgs struct {
	ScheduleID string `json:"schedule_id" jsonschema:"The scheduled job ID to trigger immediately."`
}

func (s *Server) registerRunNow() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_run_schedule_now",
		Description: "Trigger a relay scheduled job to run immediately, outside its normal cron schedule.",
	}, s.callRunScheduleNow)
}

func (s *Server) callRunScheduleNow(ctx context.Context, args runScheduleNowArgs) (map[string]any, *ToolError) {
	if args.ScheduleID == "" {
		return nil, &ToolError{Code: "validation", Message: "schedule_id is required"}
	}

	var resp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	path := fmt.Sprintf("/v1/scheduled-jobs/%s/run-now", args.ScheduleID)
	if err := s.do(ctx, "POST", path, nil, &resp); err != nil {
		return nil, MapError(err)
	}
	return map[string]any{"job_id": resp.ID, "status": resp.Status}, nil
}
