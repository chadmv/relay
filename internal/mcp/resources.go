package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

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
		body, terr := s.recentJobs.get(ctx, s.readRecentJobs)
		if terr != nil {
			return nil, terr
		}
		return &mcpsdk.ReadResourceResult{
			Contents: []*mcpsdk.ResourceContents{
				{URI: "relay://recent-jobs", MIMEType: "application/json", Text: string(body)},
			},
		}, nil
	})

	s.mcp.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "relay://jobs/{id}",
		Name:        "job",
		Title:       "Relay Job",
		Description: "A single relay job by id, including its task DAG.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		return s.readEntityByID(ctx, req.Params.URI, "relay://jobs/", "/v1/jobs/")
	})

	s.mcp.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "relay://tasks/{id}",
		Name:        "task",
		Title:       "Relay Task",
		Description: "A single relay task by id.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		return s.readEntityByID(ctx, req.Params.URI, "relay://tasks/", "/v1/tasks/")
	})
}

// readEntityByID resolves a single-entity resource template. It strips prefix from
// the concrete URI to recover the id, GETs apiPath+id through the s.do chokepoint,
// and returns the entity JSON as a ReadResourceResult matching the fixed-resource
// content shape. A malformed/empty id or a backend 404 is reported to MCP clients
// as ResourceNotFoundError(uri); any other error is returned to the SDK as-is.
func (s *Server) readEntityByID(ctx context.Context, uri, prefix, apiPath string) (*mcpsdk.ReadResourceResult, error) {
	id := strings.TrimPrefix(uri, prefix)
	if id == "" {
		return nil, mcpsdk.ResourceNotFoundError(uri)
	}
	// Escape the client-supplied id so it can only be a single literal path
	// segment: PathEscape encodes '/', '?', '#', etc., preventing query-string
	// injection and encoded path traversal through the s.do request path.
	escaped := url.PathEscape(id)
	var entity map[string]any
	if err := s.do(ctx, "GET", apiPath+escaped, nil, &entity); err != nil {
		if MapError(err).Code == "not_found" {
			return nil, mcpsdk.ResourceNotFoundError(uri)
		}
		return nil, err
	}
	body, err := json.Marshal(entity)
	if err != nil {
		return nil, err
	}
	return &mcpsdk.ReadResourceResult{
		Contents: []*mcpsdk.ResourceContents{
			{URI: uri, MIMEType: "application/json", Text: string(body)},
		},
	}, nil
}

func (s *Server) readServerInfo(ctx context.Context) ([]byte, *ToolError) {
	var user struct {
		ID      string `json:"id"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		IsAdmin bool   `json:"is_admin"`
	}
	if err := s.do(ctx, "GET", "/v1/users/me", nil, &user); err != nil {
		return nil, MapError(err)
	}

	var health struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	_ = s.do(ctx, "GET", "/v1/health", nil, &health) // tolerate failure

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
	if err := s.do(ctx, "GET", "/v1/jobs?limit=20", nil, &resp); err != nil {
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
