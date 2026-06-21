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
	s.registerReservations()
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
