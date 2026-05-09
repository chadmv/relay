package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultWaitTimeout = 60 * time.Second
	maxWaitTimeout     = 300 * time.Second
	defaultWaitPoll    = 2 * time.Second
)

var terminalStatuses = map[string]bool{
	"done":      true,
	"failed":    true,
	"cancelled": true,
}

type waitForJobArgs struct {
	JobID          string `json:"job_id"          jsonschema:"The job ID to wait for."`
	TimeoutSeconds int    `json:"timeout_seconds" jsonschema:"Seconds to wait before returning (0=use default 60s, max 300)."`
}

func (s *Server) registerWait() {
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_wait_for_job",
		Description: "Poll a relay job until it reaches a terminal state (done, failed, cancelled) or the timeout elapses.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args waitForJobArgs) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callWaitForJob(ctx, args)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})
}

func (s *Server) callWaitForJob(ctx context.Context, args waitForJobArgs) (map[string]any, *ToolError) {
	if args.JobID == "" {
		return nil, &ToolError{Code: "validation", Message: "job_id is required"}
	}

	// Determine timeout duration.
	timeout := defaultWaitTimeout
	if args.TimeoutSeconds != 0 {
		if args.TimeoutSeconds < 0 {
			return nil, &ToolError{Code: "validation", Message: "timeout_seconds must be non-negative"}
		}
		t := time.Duration(args.TimeoutSeconds) * time.Second
		if t > maxWaitTimeout {
			return nil, &ToolError{
				Code:    "validation",
				Message: fmt.Sprintf("timeout_seconds must be <= %d", int(maxWaitTimeout/time.Second)),
			}
		}
		timeout = t
	}

	// Determine poll interval.
	poll := s.waitPoll
	if poll == 0 {
		poll = defaultWaitPoll
	}

	deadline := time.Now().Add(timeout)
	path := fmt.Sprintf("/v1/jobs/%s", args.JobID)

	var lastResp map[string]any
	for {
		if err := s.client.Do(ctx, "GET", path, nil, &lastResp); err != nil {
			return nil, MapError(err)
		}
		status, _ := lastResp["status"].(string)
		if terminalStatuses[status] {
			return lastResp, nil
		}

		// Check if we've hit the deadline.
		if !time.Now().Before(deadline) {
			return map[string]any{
				"timed_out":  true,
				"last_state": lastResp,
			}, nil
		}

		remaining := time.Until(deadline)
		waitFor := poll
		if remaining < poll {
			waitFor = remaining
		}
		if waitFor <= 0 {
			return map[string]any{
				"timed_out":  true,
				"last_state": lastResp,
			}, nil
		}

		select {
		case <-ctx.Done():
			return nil, &ToolError{Code: "cancelled", Message: "context cancelled"}
		case <-time.After(waitFor):
		}
	}
}
