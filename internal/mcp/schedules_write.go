package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"relay/internal/jobspec"
)

// ---- create ----

type createScheduleArgs struct {
	Name          string          `json:"name"           jsonschema:"Name for the scheduled job (required)."`
	CronExpr      string          `json:"cron_expr"      jsonschema:"Cron expression (required, 5-field or @hourly/@daily/@every <dur>)."`
	Timezone      string          `json:"timezone"       jsonschema:"IANA timezone for the cron schedule (e.g. America/New_York). Defaults to UTC."`
	OverlapPolicy string          `json:"overlap_policy" jsonschema:"What to do when a run is already active: skip or queue."`
	JobSpec       jobspec.JobSpec `json:"job_spec"       jsonschema:"The job template to run on each trigger."`
}

func (s *Server) registerSchedulesWrite() {
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_create_schedule",
		Description: "Create a new relay scheduled job (cron schedule).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args createScheduleArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callCreateSchedule(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})

	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_update_schedule",
		Description: "Update an existing relay scheduled job. Only provided fields are changed.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args updateScheduleArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callUpdateSchedule(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})

	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_delete_schedule",
		Description: "Delete a relay scheduled job by ID.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args deleteScheduleArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callDeleteSchedule(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})
}

func (s *Server) callCreateSchedule(ctx context.Context, args createScheduleArgs) (map[string]any, *ToolError) {
	if args.Name == "" {
		return nil, &ToolError{Code: "validation", Message: "name is required"}
	}
	if args.CronExpr == "" {
		return nil, &ToolError{Code: "validation", Message: "cron_expr is required"}
	}
	if err := jobspec.Validate(&args.JobSpec); err != nil {
		return nil, &ToolError{Code: "validation", Message: err.Error(), Hint: "fix the job spec and try again"}
	}

	body := map[string]any{
		"name":     args.Name,
		"cron_expr": args.CronExpr,
		"job_spec": args.JobSpec,
	}
	if args.Timezone != "" {
		body["timezone"] = args.Timezone
	}
	if args.OverlapPolicy != "" {
		body["overlap_policy"] = args.OverlapPolicy
	}

	var resp map[string]any
	if err := s.client.Do(ctx, "POST", "/v1/scheduled-jobs", body, &resp); err != nil {
		return nil, MapError(err)
	}
	return resp, nil
}

// ---- update ----

type updateScheduleArgs struct {
	ScheduleID    string  `json:"schedule_id"    jsonschema:"The scheduled job ID to update (required)."`
	CronExpr      *string `json:"cron_expr"      jsonschema:"New cron expression (omit to leave unchanged)."`
	Timezone      *string `json:"timezone"       jsonschema:"New IANA timezone (omit to leave unchanged)."`
	OverlapPolicy *string `json:"overlap_policy" jsonschema:"New overlap policy (omit to leave unchanged)."`
	Enabled       *bool   `json:"enabled"        jsonschema:"Enable or disable the schedule (omit to leave unchanged)."`
}

func (s *Server) callUpdateSchedule(ctx context.Context, args updateScheduleArgs) (map[string]any, *ToolError) {
	if args.ScheduleID == "" {
		return nil, &ToolError{Code: "validation", Message: "schedule_id is required"}
	}

	body := map[string]any{}
	if args.CronExpr != nil {
		body["cron_expr"] = *args.CronExpr
	}
	if args.Timezone != nil {
		body["timezone"] = *args.Timezone
	}
	if args.OverlapPolicy != nil {
		body["overlap_policy"] = *args.OverlapPolicy
	}
	if args.Enabled != nil {
		body["enabled"] = *args.Enabled
	}

	if len(body) == 0 {
		return nil, &ToolError{Code: "validation", Message: "at least one field must be provided to update"}
	}

	var resp map[string]any
	if err := s.client.Do(ctx, "PATCH", fmt.Sprintf("/v1/scheduled-jobs/%s", args.ScheduleID), body, &resp); err != nil {
		return nil, MapError(err)
	}
	return resp, nil
}

// ---- delete ----

type deleteScheduleArgs struct {
	ScheduleID string `json:"schedule_id" jsonschema:"The scheduled job ID to delete (required)."`
}

func (s *Server) callDeleteSchedule(ctx context.Context, args deleteScheduleArgs) (map[string]any, *ToolError) {
	if args.ScheduleID == "" {
		return nil, &ToolError{Code: "validation", Message: "schedule_id is required"}
	}

	// Pass nil as response target — the server returns 204 No Content.
	if err := s.client.Do(ctx, "DELETE", fmt.Sprintf("/v1/scheduled-jobs/%s", args.ScheduleID), nil, nil); err != nil {
		return nil, MapError(err)
	}
	return map[string]any{"ok": true}, nil
}
