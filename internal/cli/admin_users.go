package cli

import (
	"context"
	"fmt"
	"io"
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

func doAdminUsersList(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	return fmt.Errorf("not implemented")
}

func doAdminUsersGet(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	return fmt.Errorf("not implemented")
}
