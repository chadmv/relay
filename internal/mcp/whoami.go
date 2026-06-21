package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) registerWhoami() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_whoami",
		Description: "Return the identity of the authenticated relay user (email, user ID, admin flag) and the server URL.",
	}, func(ctx context.Context, _ struct{}) (map[string]any, *ToolError) {
		return s.callWhoami(ctx)
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
