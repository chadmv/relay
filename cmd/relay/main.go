// cmd/relay/main.go
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"relay/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := cli.LoadConfig()
	if err != nil {
		// Non-fatal; commands will report missing token/URL as needed.
		_ = err
	}

	commands := []cli.Command{
		cli.LoginCommand(),
		cli.LogoutCommand(),
		cli.RegisterCommand(),
		cli.PasswdCommand(),
		cli.InviteCommand(),
		cli.AgentCommand(),
		cli.SubmitCommand(),
		cli.ListCommand(),
		cli.GetCommand(),
		cli.CancelCommand(),
		cli.LogsCommand(),
		cli.WorkersCommand(),
		cli.ReservationsCommand(),
		cli.SchedulesCommand(),
		cli.AdminCommand(),
	}

	os.Exit(cli.Dispatch(ctx, commands, os.Args[1:], cfg))
}
