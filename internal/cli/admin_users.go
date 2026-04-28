package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"
)

func doAdminUsers(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay admin users <list|get> [args]")
	}
	switch args[0] {
	case "list":
		return doAdminUsersList(ctx, cfg, args[1:], out)
	case "get":
		return doAdminUsersGet(ctx, cfg, args[1:], out)
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
	return fmt.Errorf("not implemented")
}
