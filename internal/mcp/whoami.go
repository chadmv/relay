package mcp

import (
	"context"
	"encoding/json"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) registerWhoami() {
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "relay_whoami",
		Description: "Return the identity of the authenticated relay user (email, user ID, admin flag) and the server URL.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, any, error) {
		out, terr := s.callWhoami(ctx)
		if terr != nil {
			return nil, nil, terr
		}
		b, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, nil, nil
	})
}

func (s *Server) callWhoami(ctx context.Context) (map[string]any, *ToolError) {
	var resp map[string]any
	if err := s.client.Do(ctx, "GET", "/v1/users/me", nil, &resp); err != nil {
		return nil, MapError(err)
	}

	out := map[string]any{
		"user_id":    resp["id"],
		"email":      resp["email"],
		"name":       resp["name"],
		"is_admin":   resp["is_admin"],
		"server_url": s.client.BaseURL(),
	}
	return out, nil
}
