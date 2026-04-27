// internal/cli/admin.go
package cli

import (
	"context"
	"fmt"
	"io"
)

// AdminCommand returns the relay admin Command (subcommand group).
func AdminCommand() Command {
	return Command{
		Name:  "admin",
		Usage: "admin <passwd> [args]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doAdmin(ctx, cfg, args, stderrWriter())
		},
	}
}

func doAdmin(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay admin <passwd> [args]")
	}
	switch args[0] {
	case "passwd":
		return doAdminPasswd(ctx, cfg, args[1:], out)
	default:
		return fmt.Errorf("unknown admin subcommand: %s", args[0])
	}
}

func doAdminPasswd(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: relay admin passwd <email>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}
	email := args[0]

	newPass, err := readPasswordFn(out, "New password for "+email+": ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if newPass == "" {
		return fmt.Errorf("new password is required")
	}

	confirm, err := readPasswordFn(out, "Confirm new password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if newPass != confirm {
		return fmt.Errorf("passwords do not match")
	}

	c := cfg.NewClient()
	if err := c.do(ctx, "POST", "/v1/users/password-reset", map[string]string{
		"email":        email,
		"new_password": newPass,
	}, nil); err != nil {
		return fmt.Errorf("password reset failed: %w", err)
	}

	fmt.Fprintf(out, "Password reset for %s. All of their sessions have been revoked.\n", email)
	return nil
}
