// internal/cli/invites.go
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

// InviteCommand returns the relay invite Command.
func InviteCommand() Command {
	return Command{
		Name:  "invite",
		Usage: "manage invites (subcommands: create)",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			if len(args) == 0 || args[0] != "create" {
				return fmt.Errorf("usage: relay invite create [--email EMAIL] [--expires DURATION]")
			}
			return doInviteCreate(ctx, args[1:], cfg, os.Stdout)
		},
	}
}

func doInviteCreate(ctx context.Context, args []string, cfg *Config, out io.Writer) error {
	fs := flag.NewFlagSet("invite create", flag.ContinueOnError)
	email := fs.String("email", "", "bind invite to this email address (optional)")
	expires := fs.String("expires", "72h", "invite lifetime, e.g. 24h or 168h")

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	var req struct {
		Email     string `json:"email,omitempty"`
		ExpiresIn string `json:"expires_in,omitempty"`
	}
	req.Email = *email
	req.ExpiresIn = *expires

	c := cfg.NewClient()
	var resp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := c.do(ctx, "POST", "/v1/invites", req, &resp); err != nil {
		return fmt.Errorf("create invite: %w", err)
	}

	fmt.Fprintf(out, "%s\n", resp.Token)
	return nil
}
