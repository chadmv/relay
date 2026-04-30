// internal/cli/profile.go
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

// ProfileCommand returns the relay profile Command (subcommand group).
func ProfileCommand() Command {
	return Command{
		Name:  "profile",
		Usage: "profile <update> [flags]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doProfile(ctx, cfg, args, stderrWriter())
		},
	}
}

func doProfile(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay profile update --name \"<name>\"")
	}
	switch args[0] {
	case "update":
		return doProfileUpdate(ctx, cfg, args[1:], out)
	default:
		return fmt.Errorf("unknown profile subcommand: %s", args[0])
	}
}

func doProfileUpdate(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	fs := flag.NewFlagSet("profile update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "new display name (required)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(*name)
	if trimmed == "" {
		return fmt.Errorf("--name is required")
	}

	c := cfg.NewClient()
	var u userListItem
	body := map[string]string{"name": trimmed}
	if err := c.do(ctx, "PATCH", "/v1/users/me", body, &u); err != nil {
		return fmt.Errorf("update profile: %w", err)
	}
	printUserDetail(out, u)
	return nil
}
