package cli

import (
	"context"
	"fmt"
	"os"

	internalmcp "relay/internal/mcp"
)

// MCPCommand returns the "mcp" subcommand, which starts a stdio-based MCP
// server that exposes relay's jobs, tasks, and workers to MCP clients.
func MCPCommand() Command {
	return Command{
		Name:  "mcp",
		Usage: "mcp",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			if cfg == nil || cfg.Token == "" || cfg.ServerURL == "" {
				return fmt.Errorf("not logged in — run `relay login` first (or set RELAY_URL and RELAY_TOKEN)")
			}
			srv, err := internalmcp.NewServer(cfg.ServerURL, cfg.Token)
			if err != nil {
				return err
			}
			srv.SetTokenReloader(mcpTokenReloader)
			return srv.Run(ctx, os.Stdin, os.Stdout)
		},
	}
}

// mcpTokenReloader re-reads the auth token from config (file + RELAY_TOKEN env
// override) so a token refreshed out of band by `relay login` is picked up on a
// mid-session 401 without restarting the MCP process.
func mcpTokenReloader() (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}
	return cfg.Token, nil
}
