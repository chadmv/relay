package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"
)

type workspaceResp struct {
	SourceType   string     `json:"source_type"`
	SourceKey    string     `json:"source_key"`
	ShortID      string     `json:"short_id"`
	BaselineHash string     `json:"baseline_hash"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

func doWorkersWorkspaces(ctx context.Context, c *Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers workspaces", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output raw JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: relay workers workspaces <worker-id>")
	}
	workerID := fs.Arg(0)

	var workspaces []workspaceResp
	if err := c.do(ctx, "GET", "/v1/workers/"+workerID+"/workspaces", nil, &workspaces); err != nil {
		return err
	}

	if *asJSON {
		return json.NewEncoder(w).Encode(workspaces)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SHORT_ID\tSOURCE_TYPE\tSOURCE_KEY\tBASELINE\tLAST_USED")
	for _, ws := range workspaces {
		lastUsed := ""
		if ws.LastUsedAt != nil {
			lastUsed = ws.LastUsedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			ws.ShortID, ws.SourceType, ws.SourceKey, ws.BaselineHash, lastUsed)
	}
	return tw.Flush()
}

func doWorkersEvictWorkspace(ctx context.Context, c *Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers evict-workspace", flag.ContinueOnError)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: relay workers evict-workspace <worker-id> <short-id>")
	}
	workerID, shortID := fs.Arg(0), fs.Arg(1)

	if err := c.do(ctx, "POST",
		fmt.Sprintf("/v1/workers/%s/workspaces/%s/evict", workerID, shortID),
		nil, nil); err != nil {
		return err
	}

	fmt.Fprintf(w, "evict requested for workspace %s on worker %s\n", shortID, workerID)
	return nil
}
