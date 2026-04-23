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

type scheduleResp struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	CronExpr      string     `json:"cron_expr"`
	Timezone      string     `json:"timezone"`
	OverlapPolicy string     `json:"overlap_policy"`
	Enabled       bool       `json:"enabled"`
	NextRunAt     *time.Time `json:"next_run_at,omitempty"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
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

func doSchedulesList(ctx context.Context, c *Client, _ []string, w io.Writer) error {
	var out []scheduleResp
	if err := c.do(ctx, "GET", "/v1/scheduled-jobs", nil, &out); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tCRON\tTZ\tENABLED\tNEXT")
	for _, s := range out {
		next := ""
		if s.NextRunAt != nil {
			next = s.NextRunAt.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%s\n", s.ID, s.Name, s.CronExpr, s.Timezone, s.Enabled, next)
	}
	return tw.Flush()
}

func doSchedulesCreate(ctx context.Context, c *Client, args []string, w io.Writer) error {
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
	if err := c.do(ctx, "POST", "/v1/scheduled-jobs", body, &out); err != nil {
		return err
	}
	fmt.Fprintf(w, "Schedule %s created: %s\n", out.ID, out.Name)
	return nil
}

func doSchedulesShow(ctx context.Context, c *Client, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay schedules show <id>")
	}
	var out scheduleResp
	if err := c.do(ctx, "GET", "/v1/scheduled-jobs/"+args[0], nil, &out); err != nil {
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
	return nil
}

func doSchedulesUpdate(ctx context.Context, c *Client, args []string, w io.Writer) error {
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
	if err := c.do(ctx, "PATCH", "/v1/scheduled-jobs/"+id, body, &out); err != nil {
		return err
	}
	fmt.Fprintf(w, "Schedule %s updated.\n", out.ID)
	return nil
}

func doSchedulesDelete(ctx context.Context, c *Client, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay schedules delete <id>")
	}
	if err := c.do(ctx, "DELETE", "/v1/scheduled-jobs/"+args[0], nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(w, "Schedule %s deleted.\n", args[0])
	return nil
}

func doSchedulesRunNow(ctx context.Context, c *Client, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay schedules run-now <id>")
	}
	var job map[string]any
	if err := c.do(ctx, "POST", "/v1/scheduled-jobs/"+args[0]+"/run-now", nil, &job); err != nil {
		return err
	}
	fmt.Fprintf(w, "Job %v created for schedule %s (status: %v)\n", job["id"], args[0], job["status"])
	return nil
}
