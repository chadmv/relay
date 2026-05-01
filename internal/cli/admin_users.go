package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"
)

func doAdminUsers(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay admin users <list|get|create|update> [args]")
	}
	switch args[0] {
	case "list":
		return doAdminUsersList(ctx, cfg, args[1:], out)
	case "get":
		return doAdminUsersGet(ctx, cfg, args[1:], out)
	case "create":
		return doAdminUsersCreate(ctx, cfg, args[1:], out)
	case "update":
		return doAdminUsersUpdate(ctx, cfg, args[1:], out)
	default:
		return fmt.Errorf("unknown admin users subcommand: %s", args[0])
	}
}

type userListItem struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

func doAdminUsersList(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}
	if len(args) > 0 {
		return fmt.Errorf("usage: relay admin users list")
	}

	c := cfg.NewClient()
	var users []userListItem
	if err := c.do(ctx, "GET", "/v1/users", nil, &users); err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	printUsersTable(out, users)
	return nil
}

func printUsersTable(out io.Writer, users []userListItem) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tEMAIL\tNAME\tADMIN\tCREATED")
	for _, u := range users {
		admin := "no"
		if u.IsAdmin {
			admin = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			u.ID, u.Email, u.Name, admin, u.CreatedAt.Format("2006-01-02"))
	}
	_ = tw.Flush()
}

func doAdminUsersGet(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: relay admin users get <email>")
	}
	email := args[0]

	c := cfg.NewClient()
	var users []userListItem
	path := "/v1/users?email=" + url.QueryEscape(email)
	if err := c.do(ctx, "GET", path, nil, &users); err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	if len(users) == 0 {
		return fmt.Errorf("user not found: %s", email)
	}
	printUserDetail(out, users[0])
	return nil
}

func printUserDetail(out io.Writer, u userListItem) {
	admin := "no"
	if u.IsAdmin {
		admin = "yes"
	}
	fmt.Fprintf(out, "ID:       %s\n", u.ID)
	fmt.Fprintf(out, "Email:    %s\n", u.Email)
	fmt.Fprintf(out, "Name:     %s\n", u.Name)
	fmt.Fprintf(out, "Admin:    %s\n", admin)
	fmt.Fprintf(out, "Created:  %s\n", u.CreatedAt.Format(time.RFC3339))
}

func doAdminUsersUpdate(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	fs := flag.NewFlagSet("admin users update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "new display name (required)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: relay admin users update <email-or-id> --name \"<name>\"")
	}
	target := fs.Arg(0)

	trimmed := strings.TrimSpace(*name)
	if trimmed == "" {
		return fmt.Errorf("--name is required")
	}

	c := cfg.NewClient()

	// Resolve target → UUID. If the positional looks like a UUID, use it
	// directly; otherwise treat it as an email and look up via /v1/users.
	id := target
	if !looksLikeUUID(target) {
		var users []userListItem
		path := "/v1/users?email=" + url.QueryEscape(target)
		if err := c.do(ctx, "GET", path, nil, &users); err != nil {
			return fmt.Errorf("look up user: %w", err)
		}
		if len(users) == 0 {
			return fmt.Errorf("user not found: %s", target)
		}
		id = users[0].ID
	}

	var u userListItem
	body := map[string]string{"name": trimmed}
	if err := c.do(ctx, "PATCH", "/v1/users/"+id, body, &u); err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	printUserDetail(out, u)
	return nil
}

func doAdminUsersCreate(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	fs := flag.NewFlagSet("admin users create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	email := fs.String("email", "", "email address (required)")
	name := fs.String("name", "", "display name (optional, defaults to email)")
	isAdmin := fs.Bool("admin", false, "create the user as an admin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" {
		return fmt.Errorf("--email is required")
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

	body := map[string]any{
		"email":    *email,
		"password": password,
		"is_admin": *isAdmin,
	}
	if trimmed := strings.TrimSpace(*name); trimmed != "" {
		body["name"] = trimmed
	}

	c := cfg.NewClient()
	var u userListItem
	if err := c.do(ctx, "POST", "/v1/users", body, &u); err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	printUserDetail(out, u)
	return nil
}

