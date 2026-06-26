// Package mcp implements the MCP (Model Context Protocol) server for relay.
// It wraps the go-sdk server and exposes relay's jobs, tasks, and workers
// as MCP tools and resources.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"relay/internal/relayclient"
)

// Server wraps the MCP SDK server and a relay API client.
type Server struct {
	client   *relayclient.Client
	mcp      *mcpsdk.Server
	waitPoll time.Duration // overridable in tests; 0 means use defaultWaitPoll
	isAdmin  bool          // resolved once at startup via GET /v1/users/me
	// reloadToken, when non-nil, re-reads the auth token from config (file + env)
	// so a token refreshed out of band (relay login) is picked up on a 401 without
	// restarting the process. Nil means no reload is attempted. Set once at
	// construction; read-only thereafter.
	reloadToken func() (string, error)
}

// NewServer constructs a Server that talks to the relay API at serverURL
// using the given bearer token. Returns an error if either is empty.
func NewServer(serverURL, token string) (*Server, error) {
	if serverURL == "" {
		return nil, fmt.Errorf("serverURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}

	impl := &mcpsdk.Implementation{
		Name:    "relay",
		Version: "0.1.0",
	}
	mcpServer := mcpsdk.NewServer(impl, nil)

	s := &Server{
		client: relayclient.NewClient(serverURL, token),
		mcp:    mcpServer,
	}

	// Resolve the caller identity once at startup so admin-only tools can be
	// filtered at registration time. A failure here (unreachable backend, expired
	// token) is fatal: the server is useless without an authenticated backend, and
	// internal/cli/mcp.go already exits cleanly on a NewServer error.
	who, terr := s.callWhoami(context.Background())
	if terr != nil {
		return nil, terr
	}
	if v, ok := who["is_admin"].(bool); ok {
		s.isAdmin = v
	}

	s.registerTools()
	s.registerResources()
	return s, nil
}

// Run serves MCP requests over the given reader/writer pair using the stdio
// (newline-delimited JSON) framing. Blocks until ctx is cancelled or the
// transport is closed.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	transport := &mcpsdk.IOTransport{
		Reader: io.NopCloser(in),
		Writer: nopWriteCloser{out},
	}
	return s.mcp.Run(ctx, transport)
}

// registerTools wires relay operations as MCP tools.
func (s *Server) registerTools() {
	s.registerWhoami()
	s.registerJobs()
	s.registerTasks()
	s.registerTaskLogs()
	s.registerWorkers()
	s.registerSchedules()
	s.registerSchedulesWrite()
	if s.isAdmin {
		// Admin-only: hidden from non-admin sessions at discovery time. The
		// server-side AdminOnly check and the forbidden ToolError in
		// callListReservations remain the authoritative enforcement.
		s.registerReservations()
	}
	s.registerSubmit()
	s.registerCancel()
	s.registerWait()
	s.registerRunNow()
}

// registerResources exposes relay resources via MCP.
func (s *Server) registerResources() {
	s.registerResourcesImpl()
}

// nopWriteCloser wraps an io.Writer with a no-op Close so it satisfies io.WriteCloser.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// addTool registers a relay MCP tool whose call function returns either a result
// to JSON-encode or a structured *ToolError. On a *ToolError it returns an
// IsError CallToolResult carrying the marshaled ToolError (code/message/hint),
// instead of returning the error to the SDK (which would flatten it to plain text
// via CallToolResult.SetError and drop the hint and JSON structure).
func addTool[A, R any](s *Server, tool *mcpsdk.Tool, call func(context.Context, A) (R, *ToolError)) {
	mcpsdk.AddTool(s.mcp, tool, func(ctx context.Context, _ *mcpsdk.CallToolRequest, args A) (*mcpsdk.CallToolResult, any, error) {
		out, terr := call(ctx, args)
		if terr != nil {
			b, _ := json.Marshal(terr)
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
				IsError: true,
			}, nil, nil
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})
}
