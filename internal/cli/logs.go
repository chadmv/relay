// internal/cli/logs.go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"relay/internal/relayclient"
)

// taskLogPage mirrors the envelope GET /v1/tasks/{id}/logs returns
// (handleGetTaskLogs, internal/api/tasks.go). The handler has written this
// object since 2026-05-08; the CLI decoded a bare array into a slice until
// 2026-08-26, which fails and printed nothing for three and a half months.
type taskLogPage struct {
	Items   []taskLogEntry `json:"items"`
	NextSeq int64          `json:"next_seq"`
	Total   int64          `json:"total"`
}

// taskLogEntry is one row. created_at is deliberately not decoded: the CLI does
// not print it, and an unused field is a maintenance claim this package cannot
// keep. Seq is decoded because the incomplete-log diagnostic names the last seq
// printed.
type taskLogEntry struct {
	Seq     int64  `json:"seq"`
	Stream  string `json:"stream"`
	Content string `json:"content"`
}

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

// watchJobLogs subscribes to SSE events for jobID, then takes a snapshot so a job
// that went terminal before the subscribe is still caught (the broker has no replay).
// When a task reaches a terminal state its logs are fetched and printed once.
// Returns the final job status ("done", "failed", or "cancelled") and any error.
func watchJobLogs(ctx context.Context, c *relayclient.Client, jobID string, w io.Writer) (string, error) {
	taskNames := make(map[string]string)
	printed := make(map[string]bool)
	var finalStatus string

	// onSubscribed runs after the SSE subscription is live. Any task or job already
	// terminal at this point would never produce a future event, so we GET a snapshot
	// and handle it here. Returning false stops the stream when the job is done.
	onSubscribed := func() bool {
		var job jobResp
		if err := c.Do(ctx, "GET", "/v1/jobs/"+jobID, nil, &job); err != nil {
			// Fall through to the stream; a transient snapshot error should not abort.
			// taskNames stays empty here, so any subsequent stream task event prints
			// with a blank name - acceptable on this degraded path (the stream event
			// payload carries only id/status, never the name).
			return true
		}
		for _, t := range job.Tasks {
			taskNames[t.ID] = t.Name
		}
		for _, t := range job.Tasks {
			if t.Status == "done" || t.Status == "failed" || t.Status == "timed_out" {
				if !printed[t.ID] {
					printed[t.ID] = true
					printTaskLogs(ctx, c, t.ID, t.Name, w)
				}
			}
		}
		if job.Status == "done" || job.Status == "failed" || job.Status == "cancelled" {
			finalStatus = job.Status
			return false
		}
		return true
	}

	handler := func(e relayclient.SSEEvent) bool {
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
				if !printed[data.ID] {
					printed[data.ID] = true
					printTaskLogs(ctx, c, data.ID, taskNames[data.ID], w)
				}
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
	}

	if err := c.StreamEvents(ctx, "/v1/events?job_id="+jobID, onSubscribed, handler); err != nil {
		return "", err
	}
	if finalStatus == "" {
		return "", fmt.Errorf("connection lost — job %s may still be running", jobID)
	}
	return finalStatus, nil
}

// printTaskLogs fetches and prints all log lines for a task.
// Errors are silently ignored - best-effort output.
func printTaskLogs(ctx context.Context, c *relayclient.Client, taskID, taskName string, w io.Writer) {
	var page taskLogPage
	if err := c.Do(ctx, "GET", "/v1/tasks/"+taskID+"/logs", nil, &page); err != nil {
		return
	}
	for _, l := range page.Items {
		fmt.Fprintf(w, "[%s %s] %s\n", taskName, l.Stream, l.Content)
	}
}
