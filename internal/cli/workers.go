// internal/cli/workers.go
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
)

type workerResp struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	CpuCores int32  `json:"cpu_cores"`
	RamGb    int32  `json:"ram_gb"`
	GpuCount int32  `json:"gpu_count"`
	GpuModel string `json:"gpu_model"`
	Os       string `json:"os"`
	MaxSlots int32  `json:"max_slots"`
	Status   string `json:"status"`
}

// WorkersCommand returns the relay workers Command.
// Subcommands: list, get, revoke, workspaces, evict-workspace
func WorkersCommand() Command {
	return Command{
		Name:  "workers",
		Usage: "workers <list|get|revoke|workspaces|evict-workspace> [args]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doWorkers(ctx, cfg, args, os.Stdout)
		},
	}
}

func doWorkers(ctx context.Context, cfg *Config, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay workers <list|get|revoke|workspaces|evict-workspace>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	switch args[0] {
	case "list":
		return doWorkersList(ctx, c, args[1:], w)
	case "get":
		return doWorkersGet(ctx, c, args[1:], w)
	case "revoke":
		return doWorkersRevoke(ctx, c, args[1:], w)
	case "workspaces":
		return doWorkersWorkspaces(ctx, c, args[1:], w)
	case "evict-workspace":
		return doWorkersEvictWorkspace(ctx, c, args[1:], w)
	default:
		return fmt.Errorf("unknown workers subcommand: %s", args[0])
	}
}

func doWorkersList(ctx context.Context, c *Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output raw JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	var workers []workerResp
	if err := c.do(ctx, "GET", "/v1/workers", nil, &workers); err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(workers)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tCPU\tRAM GB\tGPUS\tGPU MODEL")
	for _, wk := range workers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			wk.ID, wk.Name, wk.Status, wk.CpuCores, wk.RamGb, wk.GpuCount, wk.GpuModel)
	}
	return tw.Flush()
}

func doWorkersGet(ctx context.Context, c *Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers get", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output raw JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay workers get <worker-id>")
	}
	var wk workerResp
	if err := c.do(ctx, "GET", "/v1/workers/"+fs.Arg(0), nil, &wk); err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(wk)
	}
	fmt.Fprintf(w, "ID:        %s\n", wk.ID)
	fmt.Fprintf(w, "Name:      %s\n", wk.Name)
	fmt.Fprintf(w, "Hostname:  %s\n", wk.Hostname)
	fmt.Fprintf(w, "OS:        %s\n", wk.Os)
	fmt.Fprintf(w, "Status:    %s\n", wk.Status)
	fmt.Fprintf(w, "CPU cores: %d\n", wk.CpuCores)
	fmt.Fprintf(w, "RAM GB:    %d\n", wk.RamGb)
	fmt.Fprintf(w, "GPUs:      %d × %s\n", wk.GpuCount, wk.GpuModel)
	fmt.Fprintf(w, "Max slots: %d\n", wk.MaxSlots)
	return nil
}

func doWorkersRevoke(ctx context.Context, c *Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers revoke", flag.ContinueOnError)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay workers revoke <worker-id-or-hostname>")
	}
	target := fs.Arg(0)

	id := target
	if !looksLikeUUID(target) {
		// Resolve hostname to ID by listing workers.
		var workers []workerResp
		if err := c.do(ctx, "GET", "/v1/workers", nil, &workers); err != nil {
			return fmt.Errorf("list workers: %w", err)
		}
		found := false
		for _, wk := range workers {
			if wk.Hostname == target {
				id = wk.ID
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("no worker found with hostname %q", target)
		}
	}

	if err := c.do(ctx, "DELETE", "/v1/workers/"+id+"/token", nil, nil); err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	fmt.Fprintln(w, "revoked.")
	return nil
}

// looksLikeUUID reports whether s is a UUID-shaped string (8-4-4-4-12 hex).
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}
