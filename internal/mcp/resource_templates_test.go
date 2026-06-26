package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// requireResourceNotFound asserts that err unwraps to a jsonrpc resource-not-found
// error (code CodeResourceNotFound) whose Data carries the requested uri, which is
// the contract ResourceNotFoundError(uri) produces. The transport flattens the
// message text, so the uri lives in Data, not err.Error().
func requireResourceNotFound(t *testing.T, err error, uri string) {
	t.Helper()
	require.Error(t, err)
	var je *jsonrpc.Error
	require.True(t, errors.As(err, &je), "want *jsonrpc.Error, got %T: %v", err, err)
	require.Equal(t, int64(mcpsdk.CodeResourceNotFound), je.Code)
	var data struct {
		URI string `json:"uri"`
	}
	require.NoError(t, json.Unmarshal(je.Data, &data))
	require.Equal(t, uri, data.URI)
}

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

func TestResourceTemplate_NotFound(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	_, err = cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://jobs/missing"})
	requireResourceNotFound(t, err, "relay://jobs/missing")
}

func TestResourceTemplate_EmptyID_NotFound(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("backend should not be hit for empty id, got %s", r.URL.Path)
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	_, err = cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://jobs/"})
	requireResourceNotFound(t, err, "relay://jobs/")
}

func TestResourceTemplate_ID_EscapedToSingleSegment(t *testing.T) {
	var gotReqURI, gotRawQuery string
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		gotReqURI = r.RequestURI
		gotRawQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "x", "status": "done"})
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	// The handler receives the raw id "..%2fadmin" from the matched URI. With a
	// raw apiPath+id concat the on-the-wire request becomes /v1/jobs/..%2fadmin,
	// which a router percent-decodes to a /v1/jobs/../admin traversal that escapes
	// the /v1/jobs/ namespace. PathEscape re-encodes the '%' so the id stays a
	// single literal segment (..%252fadmin) that can never be decoded to a slash.
	_, err = cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://jobs/..%2fadmin"})
	require.NoError(t, err)
	require.Empty(t, gotRawQuery, "client-supplied id must not become a query string")
	require.Equal(t, "/v1/jobs/..%252fadmin", gotReqURI, "id must be a single escaped path segment, not a traversal sequence")
}

func TestRecentJobsResource_CachedAcrossReads(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/jobs" {
			atomic.AddInt32(&calls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{"id": "j1"}},
				"total": 1,
			})
			return
		}
		http.Error(w, "unexpected: "+r.URL.Path, http.StatusNotFound)
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)
	// Force a long TTL regardless of the ambient env value.
	s.recentJobs.ttl = time.Minute

	cs := connectClient(t, s)
	_, err = cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://recent-jobs"})
	require.NoError(t, err)
	_, err = cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://recent-jobs"})
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "second recent-jobs read should hit the cache")
}
