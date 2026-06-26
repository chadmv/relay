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

func TestResourceTemplates_Listed(t *testing.T) {
	b := newWhoamiBackend(t, true)
	s, err := NewServer(b.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	res, err := cs.ListResourceTemplates(context.Background(), &mcpsdk.ListResourceTemplatesParams{})
	require.NoError(t, err)

	got := map[string]bool{}
	for _, rt := range res.ResourceTemplates {
		got[rt.URITemplate] = true
	}
	require.True(t, got["relay://jobs/{id}"], "jobs template missing")
	require.True(t, got["relay://tasks/{id}"], "tasks template missing")
}

func TestResourceTemplate_Job_ResolvesEndpoint(t *testing.T) {
	var hit string
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		hit = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "status": "done"})
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	res, err := cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://jobs/j1"})
	require.NoError(t, err)
	require.Equal(t, "/v1/jobs/j1", hit)
	require.Len(t, res.Contents, 1)
	require.Equal(t, "relay://jobs/j1", res.Contents[0].URI)
	require.Equal(t, "application/json", res.Contents[0].MIMEType)
	require.Contains(t, res.Contents[0].Text, `"j1"`)
}

func TestResourceTemplate_Task_ResolvesEndpoint(t *testing.T) {
	var hit string
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		hit = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "t1", "status": "running"})
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	res, err := cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://tasks/t1"})
	require.NoError(t, err)
	require.Equal(t, "/v1/tasks/t1", hit)
	require.Contains(t, res.Contents[0].Text, `"t1"`)
}
