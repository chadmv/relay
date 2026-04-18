// internal/cli/login.go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// saveConfigFn is a variable so tests can override it.
var saveConfigFn = SaveConfig

// readPasswordFn reads a masked password prompt. Tests override this to avoid
// requiring a real terminal file descriptor.
var readPasswordFn = func(out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

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

	password, err := readPasswordFn(out, "Password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if password == "" {
		return fmt.Errorf("password is required")
	}

	type loginRequest struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	c := NewClient(serverURL, "")
	var resp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}

	if err := c.do(ctx, "POST", "/v1/auth/login", loginRequest{Email: email, Password: password}, &resp); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	cfg.ServerURL = serverURL
	cfg.Token = resp.Token
	if err := saveConfigFn(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(out, "Logged in. Token expires %s.\n", resp.ExpiresAt.Format("2006-01-02"))
	return nil
}
