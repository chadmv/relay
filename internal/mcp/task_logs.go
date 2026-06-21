package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxTaskLogLimit = 200

type getTaskLogsArgs struct {
	TaskID   string `json:"task_id"   jsonschema:"The task ID to fetch logs for."`
	SinceSeq int    `json:"since_seq" jsonschema:"Return only log entries with seq >= this value. Defaults to 0."`
	Limit    int    `json:"limit"     jsonschema:"Maximum number of log lines to return (1-200). Defaults to 100 when 0."`
}

func (s *Server) registerTaskLogs() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_get_task_logs",
		Description: "Fetch stdout/stderr log lines for a relay task, with optional sequence-number pagination.",
	}, s.callGetTaskLogs)
}

func (s *Server) callGetTaskLogs(ctx context.Context, args getTaskLogsArgs) (map[string]any, *ToolError) {
	if args.TaskID == "" {
		return nil, &ToolError{Code: "validation", Message: "task_id is required"}
	}
	if args.Limit > maxTaskLogLimit {
		return nil, &ToolError{Code: "validation", Message: fmt.Sprintf("limit must be <= %d", maxTaskLogLimit)}
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 100
	}

	params := url.Values{}
	params.Set("since_seq", strconv.Itoa(args.SinceSeq))
	params.Set("limit", strconv.Itoa(limit))

	path := fmt.Sprintf("/v1/tasks/%s/logs?%s", args.TaskID, params.Encode())

	var resp map[string]any
	if err := s.client.Do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, MapError(err)
	}
	return resp, nil
}
