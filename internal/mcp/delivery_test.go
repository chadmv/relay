package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// connectClient stands up s's MCP server over an in-memory transport and returns
// a connected client session. The caller owns closing the session.
func connectClient(t *testing.T, s *Server) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	if _, err := s.mcp.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestDelivery_ToolErrorReachesClient drives a real registered tool through the MCP
// transport with a backend that returns 401 and asserts the structured ToolError
// (code + hint) is delivered as an IsError result, not flattened to plain text.
func TestDelivery_ToolErrorReachesClient(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "token expired"})
	}))
	defer backend.Close()

	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "relay_whoami",
		Arguments: map[string]any{},
	})
	require.NoError(t, err, "transport-level call must succeed; the failure is a tool result, not a protocol error")
	require.True(t, res.IsError, "tool result must have IsError=true")
	require.NotEmpty(t, res.Content, "tool result must carry content")

	text, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok, "content[0] must be TextContent")

	var got ToolError
	require.NoError(t, json.Unmarshal([]byte(text.Text), &got),
		"delivered text must be the JSON-encoded ToolError, got %q", text.Text)
	require.Equal(t, "auth_expired", got.Code)
	require.Equal(t, "run `relay login` to refresh credentials", got.Hint)
}
