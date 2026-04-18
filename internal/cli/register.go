// internal/cli/register.go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// RegisterCommand returns the relay register Command.
func RegisterCommand() Command {
	return Command{
		Name:  "register",
		Usage: "create a new account using an invite token",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doRegister(ctx, cfg, stdinReader(), stderrWriter())
		},
	}
}

func doRegister(ctx context.Context, cfg *Config, in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)

	serverURL := cfg.ServerURL
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}
	fmt.Fprintf(out, "Server URL [%s]: ", serverURL)
	if line, _ := r.ReadString('\n'); strings.TrimSpace(line) != "" {
		serverURL = strings.TrimSpace(line)
	}

	fmt.Fprint(out, "Email: ")
	email, _ := r.ReadString('\n')
	email = strings.TrimSpace(email)
	if email == "" {
		return fmt.Errorf("email is required")
	}

	fmt.Fprint(out, "Name (optional): ")
	name, _ := r.ReadString('\n')
	name = strings.TrimSpace(name)

	fmt.Fprint(out, "Invite token: ")
	inviteToken, _ := r.ReadString('\n')
	inviteToken = strings.TrimSpace(inviteToken)
	if inviteToken == "" {
		return fmt.Errorf("invite token is required")
	}

	password, err := readPasswordFn(out, "Password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if password == "" {
		return fmt.Errorf("password is required")
	}

	confirm, err := readPasswordFn(out, "Confirm password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if password != confirm {
		return fmt.Errorf("passwords do not match")
	}

	type registerRequest struct {
		Email       string `json:"email"`
		Name        string `json:"name,omitempty"`
		Password    string `json:"password"`
		InviteToken string `json:"invite_token"`
	}

	c := NewClient(serverURL, "")
	var resp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := c.do(ctx, "POST", "/v1/auth/register", registerRequest{
		Email:       email,
		Name:        name,
		Password:    password,
		InviteToken: inviteToken,
	}, &resp); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	cfg.ServerURL = serverURL
	cfg.Token = resp.Token
	if err := saveConfigFn(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(out, "Registered and logged in. Token expires %s.\n", resp.ExpiresAt.Format("2006-01-02"))
	return nil
}
