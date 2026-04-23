// internal/cli/jobs.go
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"
)

// ─── Response types (mirror api package JSON output) ─────────────────────────

type jobResp struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Priority         string     `json:"priority"`
	Status           string     `json:"status"`
	SubmittedBy      string     `json:"submitted_by"`
	SubmittedByEmail string     `json:"submitted_by_email"`
	Tasks            []taskResp `json:"tasks,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type taskResp struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Status         string          `json:"status"`
	Command        []string        `json:"command"`
	Env            json.RawMessage `json:"env"`
	Requires       json.RawMessage `json:"requires"`
	TimeoutSeconds *int32          `json:"timeout_seconds"`
	Retries        int32           `json:"retries"`
	DependsOn      []string        `json:"depends_on,omitempty"`
	WorkerID       string          `json:"worker_id,omitempty"`
}

// ─── Commands ─────────────────────────────────────────────────────────────────

func ListCommand() Command {
	return Command{
		Name:  "list",
		Usage: "list jobs [--status <status>] [--json]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doListJobs(ctx, cfg, args, os.Stdout)
		},
	}
}

func GetCommand() Command {
	return Command{
		Name:  "get",
		Usage: "get <job-id> [--json]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doGetJob(ctx, cfg, args, os.Stdout)
		},
	}
}

func CancelCommand() Command {
	return Command{
		Name:  "cancel",
		Usage: "cancel <job-id>",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doCancelJob(ctx, cfg, args, os.Stdout)
		},
	}
}

// ─── Implementations ─────────────────────────────────────────────────────────

func doListJobs(ctx context.Context, cfg *Config, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	status := fs.String("status", "", "filter by status")
	asJSON := fs.Bool("json", false, "output raw JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	path := "/v1/jobs"
	if *status != "" {
		path += "?status=" + *status
	}
	var jobs []jobResp
	if err := c.do(ctx, "GET", path, nil, &jobs); err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(jobs)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tSUBMITTED BY\tCREATED")
	for _, j := range jobs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", j.ID, j.Name, j.Status, j.SubmittedByEmail, j.CreatedAt.Format("2006-01-02 15:04"))
	}
	return tw.Flush()
}

func doGetJob(ctx context.Context, cfg *Config, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output raw JSON")
	pretty := fs.Bool("pretty", false, "output indented JSON (implies --json)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay get <job-id>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	var job jobResp
	if err := c.do(ctx, "GET", "/v1/jobs/"+fs.Arg(0), nil, &job); err != nil {
		return err
	}
	if *pretty {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(job)
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(job)
	}
	fmt.Fprintf(w, "ID:           %s\n", job.ID)
	fmt.Fprintf(w, "Name:         %s\n", job.Name)
	fmt.Fprintf(w, "Priority:     %s\n", job.Priority)
	fmt.Fprintf(w, "Status:       %s\n", job.Status)
	fmt.Fprintf(w, "Submitted by: %s\n", job.SubmittedByEmail)
	if len(job.Tasks) > 0 {
		fmt.Fprintln(w, "Tasks:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  NAME\tSTATUS\tWORKER")
		for _, t := range job.Tasks {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", t.Name, t.Status, t.WorkerID)
		}
		_ = tw.Flush()
	}
	return nil
}

func doCancelJob(ctx context.Context, cfg *Config, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay cancel <job-id>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	var job jobResp
	if err := c.do(ctx, "DELETE", "/v1/jobs/"+args[0], nil, &job); err != nil {
		return err
	}
	fmt.Fprintf(w, "Job %s: %s\n", job.ID, job.Status)
	return nil
}

// SubmitCommand returns the relay submit Command.
func SubmitCommand() Command {
	return Command{
		Name:  "submit",
		Usage: "submit <job.json> [--detach]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doSubmit(ctx, cfg, args, os.Stdout)
		},
	}
}

func doSubmit(ctx context.Context, cfg *Config, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	detach := fs.Bool("detach", false, "print job ID and exit without waiting for completion")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay submit [--detach] <job.json>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}

	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	c := cfg.NewClient()
	var job jobResp
	if err := c.do(ctx, "POST", "/v1/jobs", body, &job); err != nil {
		return err
	}
	fmt.Fprintln(w, job.ID)

	if *detach {
		return nil
	}

	status, err := watchJobLogs(ctx, c, job.ID, w)
	if err != nil {
		return err
	}
	if status != "done" {
		return silentError{}
	}
	return nil
}
