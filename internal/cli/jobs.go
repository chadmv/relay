// internal/cli/jobs.go
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"text/tabwriter"
	"time"

	"relay/internal/relayclient"
)

// ─── Response types (mirror api package JSON output) ─────────────────────────
//
// These are MIRRORS, and doListJobs/doGetJob re-encode through them rather than
// proxying the server's bytes, so a field missing here is deleted from output
// the user was told is JSON - silently, with no error at any layer. Keep them
// field-for-field and tag-for-tag identical to internal/api's jobResponse and
// taskResponse, including omitempty, and do not compute anything client-side:
// a mirror that derives a value stops being able to show drift.

// jobResp mirrors internal/api's jobResponse. That is ONE struct on the server
// side serving both GET /v1/jobs (list) and GET /v1/jobs/{id} (detail), with the
// enrichment block below populated only on list rows by applyJobEnrichment - so
// this is one struct here too. A separate list type would have to be kept in
// sync with a server type that does not exist, and would reintroduce exactly the
// hand-copy arity gap this shape closes.
type jobResp struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Priority         string `json:"priority"`
	Status           string `json:"status"`
	SubmittedBy      string `json:"submitted_by"`
	SubmittedByEmail string `json:"submitted_by_email,omitempty"`
	// json.RawMessage, not map[string]string: a job submitted without labels has
	// the literal JSONB `null` in the column (jobcreate.CreateJobFromSpec
	// json.Marshals a nil map) and rawJSON only floors an EMPTY slice to {}, so
	// `labels` really is null on the wire. RawMessage round-trips that; a typed
	// map would quietly turn it into {} and misreport the server.
	Labels    json.RawMessage `json:"labels"`
	Tasks     []taskResp      `json:"tasks,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`

	// Enrichment the server populates only on list rows (GET /v1/jobs). The
	// detail handler leaves them zero, and total_tasks/done_tasks carry no
	// omitempty server-side, so a detail body genuinely says 0/0 - mirror that
	// rather than filling it in from len(Tasks).
	TotalTasks       int32      `json:"total_tasks"`
	DoneTasks        int32      `json:"done_tasks"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	ScheduledJobID   string     `json:"scheduled_job_id,omitempty"`
	ScheduledJobName string     `json:"scheduled_job_name,omitempty"`
}

type taskResp struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	// `commands`, plural, and [][]string: migration 000008_task_commands
	// dropped tasks.command (TEXT[]) and added tasks.commands (JSONB), and
	// internal/api's taskResponse has emitted `commands` ever since.
	//
	// `command` (singular, []string) is still a live REQUEST key - internal/api's
	// taskSpec accepts it and jobspec.Validate normalises it into Commands -
	// which is exactly why the decoder here looked right. It is not a RESPONSE
	// key and has not been one since 2026-05. Decoding it gave
	// `relay get <job-id> --json` a "command":null and no task definition at
	// all for three months, with the whole CLI suite green.
	Commands       [][]string      `json:"commands"`
	Env            json.RawMessage `json:"env"`
	Requires       json.RawMessage `json:"requires"`
	TimeoutSeconds *int32          `json:"timeout_seconds"`
	Retries        int32           `json:"retries"`
	RetryCount     int32           `json:"retry_count"`
	DependsOn      []string        `json:"depends_on,omitempty"`
	WorkerID       string          `json:"worker_id,omitempty"`
}

// ─── Commands ─────────────────────────────────────────────────────────────────

func ListCommand() Command {
	return Command{
		Name:  "list",
		Usage: "list jobs [--status <status>] [--limit N] [--json]",
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
		Usage: "cancel [--force] <job-id>",
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
	limitFlag := fs.Int("limit", 0, "cap output at N rows (0 = all)")
	sortFlag := fs.String("sort", "", "sort order; e.g. -priority or name (server-validated)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	params := url.Values{}
	if *status != "" {
		params.Set("status", *status)
	}
	if *sortFlag != "" {
		params.Set("sort", *sortFlag)
	}
	jobs, total, err := relayclient.FetchAllPages[jobResp](ctx, c, "/v1/jobs", params, *limitFlag)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(jobs)
	}
	fmt.Fprintf(w, "Total: %d\n", total)
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
	if err := c.Do(ctx, "GET", "/v1/jobs/"+fs.Arg(0), nil, &job); err != nil {
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
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	force := fs.Bool("force", false, "force termination: kills the entire process tree immediately and skips workspace cleanup. May leave workspaces in a dirty state.")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay cancel [--force] <job-id>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	path := "/v1/jobs/" + fs.Arg(0)
	if *force {
		path += "?force=true"
	}
	var job jobResp
	if err := c.Do(ctx, "DELETE", path, nil, &job); err != nil {
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
			return doSubmit(ctx, cfg, args, os.Stdout, os.Stderr)
		},
	}
}

func doSubmit(ctx context.Context, cfg *Config, args []string, w, errOut io.Writer) error {
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
	if err := c.Do(ctx, "POST", "/v1/jobs", body, &job); err != nil {
		return err
	}
	fmt.Fprintln(w, job.ID)

	if *detach {
		return nil
	}

	status, completeness, err := watchJobLogs(ctx, c, job.ID, w, errOut)
	if err != nil {
		return err
	}
	// Shared with doLogs. Leaving relay submit silent about an incomplete log
	// would re-create this bug in the sibling command, and the two commands
	// wording the same outcome differently is its own defect.
	return watchOutcomeError(status, completeness)
}
