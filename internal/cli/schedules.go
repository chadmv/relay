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
// THE RUNE SET IS WIDER THAN C0 AND DEL, and each family is here for its own
// reason:
//
//   - C0 (U+0000-U+001F) AND DEL. ESC starts an ANSI sequence and newline forges
//     a line. TAB is in this range and is covered by it deliberately: a raw tab
//     inside a cell shifts tabwriter's column boundaries in doSchedulesList, so
//     narrowing this arm would reopen a column-forging hole and not only an
//     escape one.
//   - C1 (U+0080-U+009F). U+009B IS the single-character CSI. A terminal that
//     accepts it starts an escape sequence with no ESC byte anywhere in the
//     stream, so stripping ESC alone does not strip escape sequences.
//   - BIDI OVERRIDES AND ISOLATES (U+200E, U+200F, U+202A-U+202E,
//     U+2066-U+2069). These reorder the glyphs that follow them without changing
//     any byte a reader can find, which is how a value swallows the provenance
//     prefix printed before it into a right-to-left run.
//
// EVERY OTHER RUNE ABOVE U+007F IS LEFT ALONE. Mapping the whole non-ASCII range
// would be simpler and would destroy the field for every operator who does not
// write in English.
//
// internal/schedrunner/failure.go's sanitizeFailureText makes the byte-identical
// mapping at the server's write site, and internal/relayclient's
// sanitizeServerText makes it for server-supplied error messages. THE THREE MUST
// STAY IN STEP. They are three functions rather than one because internal/cli
// must not import internal/schedrunner - a client package importing the
// scheduler to borrow ten lines is a worse defect than the duplication - and
// because relayclient's copy runs on a different subject.
func terminalSafeLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r >= 0x7f && r <= 0x9f:
			return ' '
		case r == 0x200e, r == 0x200f:
			return ' '
		case r >= 0x202a && r <= 0x202e:
			return ' '
		case r >= 0x2066 && r <= 0x2069:
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
	fmt.Fprintln(tw, "ID\tNAME\tCRON\tTZ\tENABLED\tNEXT\tSTATE")
	for _, s := range schedules {
		next := ""
		if s.NextRunAt != nil {
			next = s.NextRunAt.Format("2006-01-02 15:04")
		}
		// STATE IS A SEPARATE AXIS FROM ENABLED. A schedule that has stopped
		// producing jobs is still enabled - relay does not auto-disable one - so
		// ENABLED keeps telling the truth about the operator's own setting and
		// this column says whether the scheduler can actually use the schedule.
		//
		// PUTTING IT IN THE LIST IS THE POINT. run-now already explains a
		// schedule you SUSPECT; what was missing was any way to see which one to
		// suspect without suspecting anything first.
		//
		// ITS OWN COLUMN rather than a marker appended to NEXT: NEXT is a
		// timestamp that schedules_integration_test.go matches with a regex, and
		// appending prose would make one cell mean two things. tabwriter has no
		// width budget to blow, so the argument that forced a chip rather than a
		// tenth column in the SPA does not apply here.
		state := "OK"
		if s.hasFailure() {
			state = "FAILING"
		}
		// EVERY SERVER-SUPPLIED CELL GOES THROUGH terminalSafeLine, not only the
		// one carrying last_error. STATE is a trust signal and the other cells on
		// its row are chosen by the party it reports on: schedule names are
		// unvalidated - create rejects only "" and PATCH does not even do that -
		// so a name carrying a newline ends the row early, pushes the real STATE
		// cell onto a junk continuation line, and supplies a forged line reading
		// OK under the attacker's own ID. A tab does the narrower version of the
		// same thing by shifting tabwriter's column boundaries.
		//
		// next and state are relay's own strings and are passed through
		// unwrapped, so a reader can see which cells are attacker-chosen.
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
			terminalSafeLine(s.ID), terminalSafeLine(s.Name), terminalSafeLine(s.CronExpr),
			terminalSafeLine(s.Timezone), s.Enabled, next, state)
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
	// SAME REASON AS THE list TABLE, one shape down: this prints one
	// "Label: value" per line, so any unsanitized value carrying a newline
	// forges a further line - a "Last error ..." line included, which is the
	// one whose provenance prefix this command added.
	fmt.Fprintf(w, "ID:       %s\n", terminalSafeLine(out.ID))
	fmt.Fprintf(w, "Name:     %s\n", terminalSafeLine(out.Name))
	fmt.Fprintf(w, "Cron:     %s\n", terminalSafeLine(out.CronExpr))
	fmt.Fprintf(w, "Timezone: %s\n", terminalSafeLine(out.Timezone))
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
	// --spec is THE REMEDY for a schedule whose stored job_spec no longer
	// validates, which is what `relay schedules list`'s FAILING marker and
	// `relay schedules show`'s Last error line point an operator at. Before it,
	// the only routes were the Python SDK and curl: the SPA's Job spec panel is
	// read-only and this command had no spec flag, so relay advertised a failure
	// whose fix relay could not perform.
	//
	// SYNTAX ONLY, mirroring doSchedulesCreate: this unmarshals to confirm the
	// file PARSES and sends the object. The server is the validator of record and
	// its 400 renders verbatim.
	specFile := fs.String("spec", "", "path to a replacement job spec JSON file")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay schedules update <id> [--cron EXPR] [--tz ZONE] [--spec FILE] [--enable|--disable] [--overlap skip|allow]")
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
	if *specFile != "" {
		data, err := os.ReadFile(*specFile)
		if err != nil {
			return fmt.Errorf("read spec file: %w", err)
		}
		var spec map[string]any
		if err := json.Unmarshal(data, &spec); err != nil {
			return fmt.Errorf("invalid spec JSON: %w", err)
		}
		body["job_spec"] = spec
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
