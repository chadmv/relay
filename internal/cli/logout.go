// internal/cli/logout.go
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// LogoutCommand returns the relay logout Command.
func LogoutCommand() Command {
	return Command{
		Name:  "logout",
		Usage: "revoke the current session token (--all to revoke every session)",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doLogout(ctx, cfg, args, stderrWriter())
		},
	}
}

func doLogout(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	all := fs.Bool("all", false, "revoke every session for the current user")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	if cfg.Token == "" {
		fmt.Fprintln(out, "not logged in")
		return nil
	}

	c := cfg.NewClient()
	path := "/v1/auth/token"
	if *all {
		path = "/v1/auth/tokens"
	}
	if err := c.do(ctx, "DELETE", path, nil, nil); err != nil {
		return fmt.Errorf("logout failed: %w", err)
	}

	cfg.Token = ""
	if err := saveConfigFn(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if *all {
		fmt.Fprintln(out, "Logged out of all sessions.")
	} else {
		fmt.Fprintln(out, "Logged out.")
	}
	return nil
}
