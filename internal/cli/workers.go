// internal/cli/workers.go
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

	"relay/internal/relayclient"
)

type workerResp struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Hostname  string `json:"hostname"`
	CpuCores  int32  `json:"cpu_cores"`
	RamGb     int32  `json:"ram_gb"`
	GpuCount  int32  `json:"gpu_count"`
	GpuModel  string `json:"gpu_model"`
	Os        string `json:"os"`
	MaxSlots  int32  `json:"max_slots"`
	Status    string `json:"status"`
	RevokedAt string `json:"revoked_at"`
}

// WorkersCommand returns the relay workers Command.
// Subcommands: list, get, disable, enable, revoke, delete, workspaces, evict-workspace
func WorkersCommand() Command {
	return Command{
		Name:  "workers",
		Usage: "workers <list|get|disable|enable|revoke|delete|workspaces|evict-workspace> [args]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doWorkers(ctx, cfg, args, os.Stdout)
		},
	}
}

func doWorkers(ctx context.Context, cfg *Config, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay workers <list|get|disable|enable|revoke|delete|workspaces|evict-workspace>")
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
	case "disable":
		return doWorkersDisable(ctx, c, args[1:], w)
	case "enable":
		return doWorkersEnable(ctx, c, args[1:], w)
	case "revoke":
		return doWorkersRevoke(ctx, c, args[1:], w)
	case "delete":
		return doWorkersDelete(ctx, c, args[1:], w)
	case "workspaces":
		return doWorkersWorkspaces(ctx, c, args[1:], w)
	case "evict-workspace":
		return doWorkersEvictWorkspace(ctx, c, args[1:], w)
	default:
		return fmt.Errorf("unknown workers subcommand: %s", args[0])
	}
}

func doWorkersList(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output raw JSON")
	limitFlag := fs.Int("limit", 0, "cap output at N rows (0 = all)")
	sortFlag := fs.String("sort", "", "sort order; e.g. -name or status (server-validated)")
	revoked := fs.Bool("revoked", false, "list revoked (decommissioned) workers instead (admin only)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	path := "/v1/workers"
	if *revoked {
		path = "/v1/workers/revoked"
	}

	var params url.Values
	if *sortFlag != "" {
		params = url.Values{}
		params.Set("sort", *sortFlag)
	}
	workers, total, err := relayclient.FetchAllPages[workerResp](ctx, c, path, params, *limitFlag)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(workers)
	}
	fmt.Fprintf(w, "Total: %d\n", total)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if *revoked {
		fmt.Fprintln(tw, "ID\tNAME\tHOSTNAME\tREVOKED AT")
		for _, wk := range workers {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", wk.ID, wk.Name, wk.Hostname, wk.RevokedAt)
		}
		return tw.Flush()
	}
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tCPU\tRAM GB\tGPUS\tGPU MODEL")
	for _, wk := range workers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			wk.ID, wk.Name, wk.Status, wk.CpuCores, wk.RamGb, wk.GpuCount, wk.GpuModel)
	}
	return tw.Flush()
}

func doWorkersGet(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers get", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output raw JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay workers get <worker-id>")
	}
	var wk workerResp
	if err := c.Do(ctx, "GET", "/v1/workers/"+fs.Arg(0), nil, &wk); err != nil {
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

func doWorkersRevoke(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers revoke", flag.ContinueOnError)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay workers revoke <worker-id-or-hostname>")
	}
	id, err := resolveWorkerID(ctx, c, fs.Arg(0))
	if err != nil {
		return err
	}
	if err := c.Do(ctx, "DELETE", "/v1/workers/"+id+"/token", nil, nil); err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	fmt.Fprintln(w, "revoked.")
	return nil
}

// deleteResp decodes what DELETE /v1/workers/{id} reports. Relay has no audit
// log, so the hostname plus these four numbers are the only record of what was
// destroyed - which is why Hostname is printed and not merely decoded.
type deleteResp struct {
	Hostname            string `json:"hostname"`
	RequeuedTasks       int    `json:"requeued_tasks"`
	ReservationsUpdated int    `json:"reservations_updated"`
	EnrollmentsUnlinked int    `json:"enrollments_unlinked"`
	AttributionCleared  int    `json:"attribution_cleared"`
}

// doWorkersDelete destroys a worker identity (admin only). --yes IS REQUIRED AND
// IS NOT AN INTERACTIVE PROMPT: every destructive path in this CLI is flag-driven
// and non-interactive, and a prompt breaks scripted use (spec 8.5).
func doWorkersDelete(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm the delete; required, and there is no undo")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay workers delete --yes <worker-id-or-hostname>")
	}
	// REFUSE AMBIGUITY rather than silently taking the first. fs.Arg(0) alone
	// meant `delete --yes hostA hostB` destroyed hostA, reported success, and
	// named neither - unacceptable for a command with no undo.
	if fs.NArg() > 1 {
		return fmt.Errorf("delete takes exactly one worker, got %d (%v); there is no undo, so run it once per worker",
			fs.NArg(), fs.Args())
	}
	target := fs.Arg(0)
	// The refusal prints BEFORE resolving the id, so it issues no request at all.
	if !*yes {
		fmt.Fprintf(w, "would delete worker %s (its assignments are requeued, its reservations scrubbed, its enrollment link nulled).\n", target)
		return fmt.Errorf("refusing to delete without --yes")
	}
	id, err := resolveWorkerIDIncludingRevoked(ctx, c, target)
	if err != nil {
		return err
	}
	var resp deleteResp
	if err := c.Do(ctx, "DELETE", "/v1/workers/"+id, nil, &resp); err != nil {
		return fmt.Errorf("delete worker: %w", err)
	}
	fmt.Fprintf(w, "deleted worker %q (%s); %d task(s) requeued, %d reservation(s) updated, "+
		"%d enrollment(s) unlinked, %d finished task(s) lost their worker attribution.\n",
		resp.Hostname, id, resp.RequeuedTasks, resp.ReservationsUpdated,
		resp.EnrollmentsUnlinked, resp.AttributionCleared)
	return nil
}

// resolveWorkerID returns the UUID for target, resolving a hostname via GET
// /v1/workers when target is not already UUID-shaped.
//
// IT DOES NOT SEE REVOKED WORKERS, and that is deliberate rather than an
// oversight - see resolveWorkerIDIncludingRevoked, which only `delete` calls.
// Widening this helper would silently give disable, enable, workspaces and
// evict-workspace the ability to be addressed by a revoked worker's hostname,
// which nobody asked for and which is a behaviour change with no spec of its own.
// It would also cost every hostname MISS on those four commands a second request,
// which for a non-admin is a 403 on an endpoint they did not ask about.
func resolveWorkerID(ctx context.Context, c *relayclient.Client, target string) (string, error) {
	return resolveWorkerIDIn(ctx, c, target, "/v1/workers")
}

// resolveWorkerIDIncludingRevoked resolves a hostname against GET /v1/workers
// and, on a miss, GET /v1/workers/revoked. ONLY doWorkersDelete CALLS THIS.
//
// THE FALLBACK IS NOT COSMETIC. Every paginated variant behind GET /v1/workers
// carries `WHERE status != 'revoked'` (query/workers.sql), so without it a
// hostname cannot be resolved for exactly the rows an operator most wants to
// delete - revoke-then-later-delete is the natural sequence, and it would break
// on the second step. Delete is the one verb for which reaching a revoked row by
// hostname is the POINT, so the widening is scoped to it.
func resolveWorkerIDIncludingRevoked(ctx context.Context, c *relayclient.Client, target string) (string, error) {
	return resolveWorkerIDIn(ctx, c, target, "/v1/workers", "/v1/workers/revoked")
}

// resolveWorkerIDIn is the shared body. THE PATH ORDER IS THE CALLER'S CONTRACT,
// not an implementation detail: the primary list is tried first so a live worker
// never costs a second round trip.
func resolveWorkerIDIn(ctx context.Context, c *relayclient.Client, target string, paths ...string) (string, error) {
	if looksLikeUUID(target) {
		return target, nil
	}
	for i, path := range paths {
		workers, _, err := relayclient.FetchAllPages[workerResp](ctx, c, path, nil, 0)
		if err != nil {
			// The PRIMARY list's error is fatal; a fallback's is soft, because the
			// revoked list is admin-only and a non-admin should still see the
			// primary list's miss rather than an auth error about an endpoint they
			// did not ask for. Keyed on the INDEX, not on the path string: a
			// literal comparison would silently turn a third path's 500 or 403
			// into "no worker found".
			//
			// KNOWN CONSEQUENCE, recorded rather than discovered in review.
			// Since FetchAllPages grew termination stops, this branch can now
			// swallow a PAGINATION error on a fallback path - a repeated cursor
			// or a page cap on /v1/workers/revoked - and report it to the
			// operator as `no worker found with hostname "..."`, which is a
			// wrong diagnosis rather than merely a terse one. The soft rule is
			// right for the 403 it was written for and wrong for this; narrowing
			// it means deciding which error classes are soft (plausibly: a
			// relayclient.ResponseError with 401/403 stays soft, everything else
			// becomes fatal), which is a separate judgement with its own tests
			// and is deliberately NOT made here.
			if i == 0 {
				return "", fmt.Errorf("list workers: %w", err)
			}
			break
		}
		for _, wk := range workers {
			if wk.Hostname == target {
				return wk.ID, nil
			}
		}
	}
	return "", fmt.Errorf("no worker found with hostname %q", target)
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

// disableResp decodes the relevant fields of the disable endpoint response.
type disableResp struct {
	RequeuedTasks int `json:"requeued_tasks"`
}

func doWorkersDisable(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers disable", flag.ContinueOnError)
	requeue := fs.Bool("requeue", false, "requeue active tasks immediately instead of draining")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay workers disable [--requeue] <worker-id-or-hostname>")
	}
	id, err := resolveWorkerID(ctx, c, fs.Arg(0))
	if err != nil {
		return err
	}
	path := "/v1/workers/" + id + "/disable"
	if *requeue {
		path += "?requeue=true"
	}
	var resp disableResp
	if err := c.Do(ctx, "POST", path, nil, &resp); err != nil {
		return fmt.Errorf("disable worker: %w", err)
	}
	if *requeue {
		fmt.Fprintf(w, "disabled; %d task(s) requeued.\n", resp.RequeuedTasks)
	} else {
		fmt.Fprintln(w, "disabled.")
	}
	return nil
}

func doWorkersEnable(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers enable", flag.ContinueOnError)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay workers enable <worker-id-or-hostname>")
	}
	id, err := resolveWorkerID(ctx, c, fs.Arg(0))
	if err != nil {
		return err
	}
	if err := c.Do(ctx, "POST", "/v1/workers/"+id+"/enable", nil, nil); err != nil {
		return fmt.Errorf("enable worker: %w", err)
	}
	fmt.Fprintln(w, "enabled.")
	return nil
}
