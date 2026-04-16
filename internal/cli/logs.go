// internal/cli/logs.go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// LogsCommand returns the relay logs Command.
func LogsCommand() Command {
	return Command{
		Name:  "logs",
		Usage: "logs <job-id>  — tail logs until job completes",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doLogs(ctx, cfg, args, os.Stdout)
		},
	}
}

func doLogs(ctx context.Context, cfg *Config, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay logs <job-id>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	status, err := watchJobLogs(ctx, c, args[0], w)
	if err != nil {
		return err
	}
	if status != "done" {
		return silentError{}
	}
	return nil
}

// watchJobLogs watches SSE events for jobID.
// When a task reaches a terminal state its logs are fetched and printed.
// Returns the final job status ("done", "failed", or "cancelled") and any error.
func watchJobLogs(ctx context.Context, c *Client, jobID string, w io.Writer) (string, error) {
	// Pre-fetch tasks to build id→name map.
	var tasks []taskResp
	if err := c.do(ctx, "GET", "/v1/jobs/"+jobID+"/tasks", nil, &tasks); err != nil {
		return "", fmt.Errorf("list tasks: %w", err)
	}
	taskNames := make(map[string]string, len(tasks))
	for _, t := range tasks {
		taskNames[t.ID] = t.Name
	}

	var finalStatus string
	err := c.StreamEvents(ctx, "/v1/events?job_id="+jobID, func(e SSEEvent) bool {
		switch e.Type {
		case "task":
			var data struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(e.Data), &data) != nil {
				return true
			}
			if data.Status == "done" || data.Status == "failed" || data.Status == "timed_out" {
				printTaskLogs(ctx, c, data.ID, taskNames[data.ID], w)
			}
		case "job":
			var data struct {
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(e.Data), &data) != nil {
				return true
			}
			if data.Status == "done" || data.Status == "failed" || data.Status == "cancelled" {
				finalStatus = data.Status
				return false
			}
		}
		return true
	})
	if err != nil {
		return "", err
	}
	if finalStatus == "" {
		return "", fmt.Errorf("connection lost — job %s may still be running", jobID)
	}
	return finalStatus, nil
}

// printTaskLogs fetches and prints all log lines for a task.
// Errors are silently ignored — best-effort output.
func printTaskLogs(ctx context.Context, c *Client, taskID, taskName string, w io.Writer) {
	var logs []struct {
		Stream  string `json:"stream"`
		Content string `json:"content"`
	}
	if err := c.do(ctx, "GET", "/v1/tasks/"+taskID+"/logs", nil, &logs); err != nil {
		return
	}
	for _, l := range logs {
		fmt.Fprintf(w, "[%s %s] %s\n", taskName, l.Stream, l.Content)
	}
}
