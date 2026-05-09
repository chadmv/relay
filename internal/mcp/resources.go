package mcp

import (
	"context"
	"encoding/json"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"relay/internal/relayclient"
)

func (s *Server) registerResourcesImpl() {
	s.mcp.AddResource(&mcpsdk.Resource{
		URI:         "relay://server-info",
		Name:        "server-info",
		Title:       "Relay Server Info",
		Description: "Current relay server URL, version, and authenticated user identity.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		body, terr := s.readServerInfo(ctx)
		if terr != nil {
			return nil, terr
		}
		return &mcpsdk.ReadResourceResult{
			Contents: []*mcpsdk.ResourceContents{
				{URI: "relay://server-info", MIMEType: "application/json", Text: string(body)},
			},
		}, nil
	})

	s.mcp.AddResource(&mcpsdk.Resource{
		URI:         "relay://recent-jobs",
		Name:        "recent-jobs",
		Title:       "Recent Jobs",
		Description: "The 20 most recently created jobs on the relay server.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		body, terr := s.readRecentJobs(ctx)
		if terr != nil {
			return nil, terr
		}
		return &mcpsdk.ReadResourceResult{
			Contents: []*mcpsdk.ResourceContents{
				{URI: "relay://recent-jobs", MIMEType: "application/json", Text: string(body)},
			},
		}, nil
	})
}

func (s *Server) readServerInfo(ctx context.Context) ([]byte, *ToolError) {
	var user struct {
		ID      string `json:"id"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		IsAdmin bool   `json:"is_admin"`
	}
	if err := s.client.Do(ctx, "GET", "/v1/users/me", nil, &user); err != nil {
		return nil, MapError(err)
	}

	var health struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	_ = s.client.Do(ctx, "GET", "/v1/health", nil, &health) // tolerate failure

	body, err := json.Marshal(map[string]any{
		"server_url":     s.client.BaseURL(),
		"server_version": health.Version,
		"user":           user,
	})
	if err != nil {
		return nil, &ToolError{Code: "internal_error", Message: err.Error()}
	}
	return body, nil
}

func (s *Server) readRecentJobs(ctx context.Context) ([]byte, *ToolError) {
	var resp relayclient.PageEnvelope[map[string]any]
	if err := s.client.Do(ctx, "GET", "/v1/jobs?limit=20", nil, &resp); err != nil {
		return nil, MapError(err)
	}
	items := resp.Items
	if items == nil {
		items = []map[string]any{}
	}
	body, err := json.Marshal(map[string]any{
		"items": items,
		"total": resp.Total,
	})
	if err != nil {
		return nil, &ToolError{Code: "internal_error", Message: err.Error()}
	}
	return body, nil
}
