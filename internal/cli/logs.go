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
		Usage: "logs <job-id>  - print each task's log as the task finishes, until the job is done",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doLogs(ctx, cfg, args, os.Stdout, os.Stderr)
		},
	}
}

func doLogs(ctx context.Context, cfg *Config, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay logs <job-id>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	status, logFailures, err := watchJobLogs(ctx, c, args[0], out, errOut)
	if err != nil {
		return err
	}
	// A described failure takes precedence over silentError{}: both exit 1, and
	// the described one is strictly more informative. Silence is the thing being
	// fixed, so where the two compete silence loses.
	if logFailures > 0 {
		return fmt.Errorf("logs incomplete for %d of the job's tasks", logFailures)
	}
	if status != "done" {
		return silentError{}
	}
	return nil
}

// watchJobLogs subscribes to SSE events for jobID, then takes a snapshot so a job
// that went terminal before the subscribe is still caught (the broker has no replay).
// When a task reaches a terminal state its logs are fetched and printed once.
// Returns the final job status ("done", "failed", or "cancelled"), the number of
// tasks whose logs could not be printed in full, and any error.
//
// A log failure never aborts the watch: the remaining tasks still stream and
// print. It is reported on errOut immediately and counted, and doLogs turns a
// non-zero count into a non-silent error.
func watchJobLogs(ctx context.Context, c *relayclient.Client, jobID string, out, errOut io.Writer) (string, int, error) {
	taskNames := make(map[string]string)
	printed := make(map[string]bool)
	var finalStatus string
	logFailures := 0

	// emit prints one task's log and reports an incomplete one on errOut. One
	// diagnostic per failing task, naming the task, the task id and the last
	// seq written; the error's own text is the reason it stopped.
	emit := func(taskID, taskName string) {
		lastSeq, err := printTaskLogs(ctx, c, taskID, taskName, out)
		if err != nil {
			logFailures++
			fmt.Fprintf(errOut, "relay: logs for task %s (%s) are incomplete - stopped after seq %d: %v\n",
				taskName, taskID, lastSeq, err)
		}
	}

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
					emit(t.ID, t.Name)
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
					emit(data.ID, taskNames[data.ID])
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
		return "", logFailures, err
	}
	if finalStatus == "" {
		return "", logFailures, fmt.Errorf("connection lost — job %s may still be running", jobID)
	}
	return finalStatus, logFailures, nil
}

// printTaskLogs fetches a task's log and writes every line to out. It returns
// the seq of the last row written (0 when nothing was written) and the reason
// it stopped early, or a nil error when the server reported the log as drained.
//
// The last seq is returned rather than logged here because the caller owns the
// diagnostic's wording, and the seq is what makes that diagnostic actionable:
// it tells an operator where the output stops and what since_seq to resume from
// by hand.
func printTaskLogs(ctx context.Context, c *relayclient.Client, taskID, taskName string, out io.Writer) (int64, error) {
	var lastSeq int64
	var page taskLogPage
	path := fmt.Sprintf("/v1/tasks/%s/logs?since_seq=%d&limit=%d", taskID, 0, relayclient.PageRequestLimit)
	if err := c.Do(ctx, "GET", path, nil, &page); err != nil {
		return lastSeq, err
	}
	for _, l := range page.Items {
		fmt.Fprintf(out, "[%s %s] %s\n", taskName, l.Stream, l.Content)
		lastSeq = l.Seq
	}
	return lastSeq, nil
}
