package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type listTasksArgs struct {
	JobID string `json:"job_id" jsonschema:"The job ID whose tasks to list."`
}

type getTaskArgs struct {
	TaskID string `json:"task_id" jsonschema:"The task ID to fetch."`
}

func (s *Server) registerTasks() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_list_tasks",
		Description: "List all tasks belonging to a relay job.",
	}, s.callListTasks)

	addTool(s, &mcpsdk.Tool{
		Name:        "relay_get_task",
		Description: "Get details of a single relay task by ID.",
	}, s.callGetTask)
}

func (s *Server) callListTasks(ctx context.Context, args listTasksArgs) (map[string]any, *ToolError) {
	if args.JobID == "" {
		return nil, &ToolError{Code: "validation", Message: "job_id is required"}
	}

	var items []map[string]any
	if err := s.client.Do(ctx, "GET", fmt.Sprintf("/v1/jobs/%s/tasks", args.JobID), nil, &items); err != nil {
		return nil, MapError(err)
	}

	anyItems := make([]any, len(items))
	for i, item := range items {
		anyItems[i] = item
	}
	return map[string]any{"items": anyItems}, nil
}

func (s *Server) callGetTask(ctx context.Context, args getTaskArgs) (map[string]any, *ToolError) {
	if args.TaskID == "" {
		return nil, &ToolError{Code: "validation", Message: "task_id is required"}
	}

	var resp map[string]any
	if err := s.client.Do(ctx, "GET", fmt.Sprintf("/v1/tasks/%s", args.TaskID), nil, &resp); err != nil {
		return nil, MapError(err)
	}
	return resp, nil
}
