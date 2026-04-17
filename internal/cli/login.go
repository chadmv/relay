// internal/cli/login.go
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// saveConfigFn is a variable so tests can override it.
var saveConfigFn = SaveConfig

// LoginCommand returns the relay login Command.
func LoginCommand() Command {
	return Command{
		Name:  "login",
		Usage: "authenticate and save credentials to config file",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doLogin(ctx, cfg, stdinReader(), stderrWriter())
		},
	}
}

func doLogin(ctx context.Context, cfg *Config, in io.Reader, out io.Writer) error {
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

	type tokenRequest struct {
		Email       string `json:"email"`
		InviteToken string `json:"invite_token,omitempty"`
	}

	c := NewClient(serverURL, "")
	var resp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}

	reqBody := tokenRequest{Email: email}
	if err := c.do(ctx, "POST", "/v1/auth/token", reqBody, &resp); err != nil {
		var re *ResponseError
		if errors.As(err, &re) && re.StatusCode == http.StatusForbidden && re.Message == "invite required" {
			fmt.Fprint(out, "Invite token: ")
			inviteToken, _ := r.ReadString('\n')
			inviteToken = strings.TrimSpace(inviteToken)
			if inviteToken == "" {
				return fmt.Errorf("invite token required for new accounts")
			}
			reqBody.InviteToken = inviteToken
			if err := c.do(ctx, "POST", "/v1/auth/token", reqBody, &resp); err != nil {
				return fmt.Errorf("login failed: %w", err)
			}
		} else {
			return fmt.Errorf("login failed: %w", err)
		}
	}

	cfg.ServerURL = serverURL
	cfg.Token = resp.Token
	if err := saveConfigFn(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(out, "Logged in. Token expires %s.\n", resp.ExpiresAt.Format("2006-01-02"))
	return nil
}
