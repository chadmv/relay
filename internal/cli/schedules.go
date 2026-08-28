package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"relay/internal/relayclient"
)

type scheduleResp struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	CronExpr      string     `json:"cron_expr"`
	Timezone      string     `json:"timezone"`
	OverlapPolicy string     `json:"overlap_policy"`
	Enabled       bool       `json:"enabled"`
	NextRunAt     *time.Time `json:"next_run_at,omitempty"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	// Absent means healthy. The server omits both keys entirely, never "" and
	// never null, so a nil pointer is the whole test.
	//
	// A POINTER RATHER THAN A PLAIN string, deliberately, even though the server
	// sends a plain string: absent, empty and present are THREE states and the
	// original defect was exactly the failure to distinguish two of them. A plain
	// string would collapse absent and "" into one value here, and the collapse
	// would be invisible - which is the shape of the bug this field exists to
	// report. Every read is `!= nil && *p != ""`.
	//
	// NOTE THIS STRUCT IS ALREADY A LOSSY VIEW of scheduledJobResponse: it
	// carries no owner_email and no last_job_id. That is pre-existing and this
	// slice does not fix it. It adds these two because a schedule that has
	// silently stopped producing jobs is otherwise indistinguishable from a
	// working one in every CLI output there is.
	LastError   *string    `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
}

// hasFailure reports whether the schedule carries a recorded fire failure.
//
// ABSENT, EMPTY AND PRESENT ARE THREE STATES and this is the one place that
// partitions them, so that `relay schedules list` and `relay schedules show`
// cannot drift apart on the question they both answer. The server's write site
// never stores an empty string precisely so `omitempty` on a string is safe, but
// the CLI cannot verify that from here - it decodes whatever a remote server
// sent - so an explicit "" reads as healthy rather than as a labelled blank.
func (s scheduleResp) hasFailure() bool {
	return s.LastError != nil && *s.LastError != ""
}

// terminalSafeLine renders one server-supplied string as a single terminal line,
// replacing every C0 control character and DEL with a space.
//
// THE SERVER ALREADY DOES THIS AT THE WRITE SITE and this is still not
// redundant, for two reasons that are about the CLI rather than about doubt:
//
//   - THAT SANITIZER IS IN ANOTHER PROCESS, reached over a ServerURL the
//     operator sets from ~/.relay/config.json or RELAY_URL. `relay` renders
//     whatever that endpoint decodes to. "The server strips control characters"
//     is a claim about a peer; a client that echoes a response to a terminal
//     cannot verify it, so it enforces it.
//   - A NEWLINE DEFEATS THE PROVENANCE PREFIX. doSchedulesShow prints one
//     "Label: value" per line, so a value carrying \n forges further lines in
//     relay's own output; the prefix names the provenance of the first line and
//     says nothing about the ones the value invented. Collapsing the value to
//     one line is what makes the label a mitigation rather than a decoration.
//
// It does not truncate: the server bounds the stored text at 1 KB, and a second
// bound here would silently disagree with the "use run-now for the full message"
// remedy the caller prints two lines later.
func terminalSafeLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

// SchedulesCommand returns the relay schedules Command.
func SchedulesCommand() Command {
	return Command{
		Name:  "schedules",
		Usage: "schedules <list|create|show|update|delete|run-now> [args]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doSchedules(ctx, cfg, args, os.Stdout)
		},
	}
}

func doSchedules(ctx context.Context, cfg *Config, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay schedules <list|create|show|update|delete|run-now>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()
	switch args[0] {
	case "list":
		return doSchedulesList(ctx, c, args[1:], w)
	case "create":
		return doSchedulesCreate(ctx, c, args[1:], w)
	case "show":
		return doSchedulesShow(ctx, c, args[1:], w)
	case "update":
		return doSchedulesUpdate(ctx, c, args[1:], w)
	case "delete":
		return doSchedulesDelete(ctx, c, args[1:], w)
	case "run-now":
		return doSchedulesRunNow(ctx, c, args[1:], w)
	default:
		return fmt.Errorf("unknown schedules subcommand: %s", args[0])
	}
}

func doSchedulesList(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("schedules list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output raw JSON")
	limitFlag := fs.Int("limit", 0, "cap output at N rows (0 = all)")
	sortFlag := fs.String("sort", "", "sort order; e.g. -name or next_run_at (server-validated)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	var params url.Values
	if *sortFlag != "" {
		params = url.Values{}
		params.Set("sort", *sortFlag)
	}
	schedules, total, err := relayclient.FetchAllPages[scheduleResp](ctx, c, "/v1/scheduled-jobs", params, *limitFlag)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(schedules)
	}
	fmt.Fprintf(w, "Total: %d\n", total)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tCRON\tTZ\tENABLED\tNEXT")
	for _, s := range schedules {
		next := ""
		if s.NextRunAt != nil {
			next = s.NextRunAt.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%s\n", s.ID, s.Name, s.CronExpr, s.Timezone, s.Enabled, next)
	}
	return tw.Flush()
}

func doSchedulesCreate(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	name := fs.String("name", "", "schedule name (required)")
	cronExpr := fs.String("cron", "", "cron expression (required)")
	tz := fs.String("tz", "UTC", "IANA timezone")
	overlap := fs.String("overlap", "skip", "overlap policy: skip|allow")
	specFile := fs.String("spec", "", "path to job spec JSON (required)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if *name == "" || *cronExpr == "" || *specFile == "" {
		return fmt.Errorf("usage: relay schedules create --name NAME --cron EXPR --spec FILE [--tz ZONE] [--overlap skip|allow]")
	}

	data, err := os.ReadFile(*specFile)
	if err != nil {
		return fmt.Errorf("read spec file: %w", err)
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("invalid spec JSON: %w", err)
	}

	body := map[string]any{
		"name":           *name,
		"cron_expr":      *cronExpr,
		"timezone":       *tz,
		"overlap_policy": *overlap,
		"job_spec":       spec,
	}
	var out scheduleResp
	if err := c.Do(ctx, "POST", "/v1/scheduled-jobs", body, &out); err != nil {
		return err
	}
	fmt.Fprintf(w, "Schedule %s created: %s\n", out.ID, out.Name)
	return nil
}

func doSchedulesShow(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay schedules show <id>")
	}
	var out scheduleResp
	if err := c.Do(ctx, "GET", "/v1/scheduled-jobs/"+args[0], nil, &out); err != nil {
		return err
	}
	fmt.Fprintf(w, "ID:       %s\n", out.ID)
	fmt.Fprintf(w, "Name:     %s\n", out.Name)
	fmt.Fprintf(w, "Cron:     %s\n", out.CronExpr)
	fmt.Fprintf(w, "Timezone: %s\n", out.Timezone)
	fmt.Fprintf(w, "Enabled:  %t\n", out.Enabled)
	if out.NextRunAt != nil {
		fmt.Fprintf(w, "Next:     %s\n", out.NextRunAt.Format(time.RFC3339))
	}
	if out.LastRunAt != nil {
		fmt.Fprintf(w, "Last run: %s\n", out.LastRunAt.Format(time.RFC3339))
	}
	if out.hasFailure() {
		// THE PROVENANCE PREFIX IS DELIBERATE. The message is derived from the
		// stored job_spec and embeds a task name the schedule's owner chose, so
		// an admin inspecting another user's schedule is reading partly
		// attacker-chosen prose, and the one real risk is text crafted to read
		// like relay's own output. Naming where it came from is what closes that.
		fmt.Fprintf(w, "Last error (from the stored job_spec, operator-supplied): %s\n",
			terminalSafeLine(*out.LastError))
		if out.LastErrorAt != nil {
			fmt.Fprintf(w, "Failed at: %s\n", out.LastErrorAt.Format(time.RFC3339))
		}
		// The stored text is truncated to 1 KB; run-now returns it in full and
		// re-checks the spec live. Naming a command that exists is the point:
		// before `relay schedules update --spec`, the fix this signal points at
		// was reachable only from the Python SDK or curl.
		fmt.Fprintf(w, "Re-check with: relay schedules run-now %s\n", out.ID)
	}
	return nil
}

func doSchedulesUpdate(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	cronExpr := fs.String("cron", "", "new cron expression")
	tz := fs.String("tz", "", "new IANA timezone")
	enable := fs.Bool("enable", false, "enable the schedule")
	disable := fs.Bool("disable", false, "disable the schedule")
	overlap := fs.String("overlap", "", "new overlap policy: skip|allow")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay schedules update <id> [--cron EXPR] [--tz ZONE] [--enable|--disable] [--overlap ...]")
	}
	id := fs.Arg(0)

	body := map[string]any{}
	if *cronExpr != "" {
		body["cron_expr"] = *cronExpr
	}
	if *tz != "" {
		body["timezone"] = *tz
	}
	if *overlap != "" {
		body["overlap_policy"] = *overlap
	}
	if *enable {
		body["enabled"] = true
	}
	if *disable {
		body["enabled"] = false
	}
	if len(body) == 0 {
		return fmt.Errorf("no changes specified")
	}

	var out scheduleResp
	if err := c.Do(ctx, "PATCH", "/v1/scheduled-jobs/"+id, body, &out); err != nil {
		return err
	}
	fmt.Fprintf(w, "Schedule %s updated.\n", out.ID)
	return nil
}

func doSchedulesDelete(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay schedules delete <id>")
	}
	if err := c.Do(ctx, "DELETE", "/v1/scheduled-jobs/"+args[0], nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(w, "Schedule %s deleted.\n", args[0])
	return nil
}

func doSchedulesRunNow(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay schedules run-now <id>")
	}
	var job map[string]any
	if err := c.Do(ctx, "POST", "/v1/scheduled-jobs/"+args[0]+"/run-now", nil, &job); err != nil {
		return err
	}
	fmt.Fprintf(w, "Job %v created for schedule %s (status: %v)\n", job["id"], args[0], job["status"])
	return nil
}
