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
	// Serve a successful whoami at startup, then 401 for the tool path under test.
	// relay_whoami's tool call would hit /v1/users/me (same as the startup probe),
	// so drive relay_list_jobs (-> /v1/jobs) to keep the 401-on-tool-path
	// assertion meaningful while NewServer's startup whoami succeeds.
	backend := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "token expired"})
	}))
	defer backend.Close()

	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "relay_list_jobs",
		Arguments: map[string]any{"status": "", "limit": 0, "cursor": "", "sort": ""},
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

// TestDelivery_ValidationErrorReachesClient drives relay_get_job with an empty job_id
// (a client-side validation *ToolError that never reaches the backend) and asserts the
// structured error is delivered as an IsError result.
func TestDelivery_ValidationErrorReachesClient(t *testing.T) {
	// Only the startup whoami probe may hit the backend; the tool path must not be
	// reached for a client-side validation error - fail loudly if it is.
	backend := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("backend should not be called for a validation error; got %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "relay_get_job",
		Arguments: map[string]any{"job_id": ""},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.NotEmpty(t, res.Content)

	text, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)

	var got ToolError
	require.NoError(t, json.Unmarshal([]byte(text.Text), &got),
		"delivered text must be the JSON-encoded ToolError, got %q", text.Text)
	require.Equal(t, "validation", got.Code)
	require.Contains(t, got.Message, "job_id is required")
}
