// internal/cli/passwd.go
package cli

import (
	"context"
	"fmt"
	"io"
)

// PasswdCommand returns the relay passwd Command.
func PasswdCommand() Command {
	return Command{
		Name:  "passwd",
		Usage: "change your password",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doPasswd(ctx, cfg, stderrWriter())
		},
	}
}

func doPasswd(ctx context.Context, cfg *Config, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	current, err := readPasswordFn(out, "Current password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if current == "" {
		return fmt.Errorf("current password is required")
	}

	newPass, err := readPasswordFn(out, "New password: ")
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
	if err := c.do(ctx, "PUT", "/v1/users/me/password", map[string]string{
		"current_password": current,
		"new_password":     newPass,
	}, nil); err != nil {
		return fmt.Errorf("change password failed: %w", err)
	}

	fmt.Fprintln(out, "Password changed.")
	return nil
}
