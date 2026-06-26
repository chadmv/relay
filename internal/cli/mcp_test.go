package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPCommand_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: ""} // no token, no URL
	cmd := MCPCommand()
	err := cmd.Run(context.Background(), nil, cfg)
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "not logged in") ||
			strings.Contains(err.Error(), "RELAY_URL"))
}

func TestMCPCommand_Name(t *testing.T) {
	require.Equal(t, "mcp", MCPCommand().Name)
}

// TestMCPConfigReloader_ResolvesEnv verifies the reloader the CLI builds reads the
// token via LoadConfig (config file + env overrides). RELAY_TOKEN must win.
func TestMCPConfigReloader_ResolvesEnv(t *testing.T) {
	t.Setenv("RELAY_TOKEN", "from-env")
	t.Setenv("RELAY_URL", "http://x")
	tok, err := mcpTokenReloader()
	require.NoError(t, err)
	require.Equal(t, "from-env", tok)
}
