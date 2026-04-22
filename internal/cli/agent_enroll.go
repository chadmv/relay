// internal/cli/agent_enroll.go
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

// AgentCommand returns the relay agent Command.
func AgentCommand() Command {
	return Command{
		Name:  "agent",
		Usage: "manage agent enrollment tokens (admin) — subcommands: enroll",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			if len(args) == 0 || args[0] != "enroll" {
				return fmt.Errorf("usage: relay agent enroll [--hostname HINT] [--ttl DURATION]")
			}
			return doAgentEnroll(ctx, args[1:], cfg, os.Stdout, os.Stderr)
		},
	}
}

func doAgentEnroll(ctx context.Context, args []string, cfg *Config, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("agent enroll", flag.ContinueOnError)
	hostname := fs.String("hostname", "", "hostname hint (informational)")
	ttl := fs.Duration("ttl", 24*time.Hour, "token validity duration (e.g. 1h, 48h)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	body := map[string]any{
		"ttl_seconds": int64(ttl.Seconds()),
	}
	if *hostname != "" {
		body["hostname_hint"] = *hostname
	}

	c := cfg.NewClient()
	var resp struct {
		ID        string    `json:"id"`
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := c.do(ctx, "POST", "/v1/agent-enrollments", body, &resp); err != nil {
		return fmt.Errorf("create enrollment: %w", err)
	}

	// Token to stdout (easy to capture in scripts). Metadata to errOut.
	fmt.Fprintf(errOut, "enrollment expires at %s\n", resp.ExpiresAt.Format(time.RFC3339))
	fmt.Fprintln(out, resp.Token)
	fmt.Fprintf(errOut, "set on agent host: RELAY_AGENT_ENROLLMENT_TOKEN=%s\n", resp.Token)
	return nil
}
