package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"relay/internal/relayclient"

	"github.com/stretchr/testify/require"
)

func TestListWorkers_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/workers", r.URL.Path)
		require.Equal(t, "25", r.URL.Query().Get("limit"))
		_ = json.NewEncoder(w).Encode(relayclient.PageEnvelope[map[string]any]{
			Items: []map[string]any{{"id": "w1"}}, Total: 1,
		})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callListWorkers(context.Background(), listWorkersArgs{Limit: 25})
	require.Nil(t, terr)
	require.Len(t, out["items"], 1)
}

func TestGetWorker_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/workers/w1", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "w1", "name": "host"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callGetWorker(context.Background(), getWorkerArgs{WorkerID: "w1"})
	require.Nil(t, terr)
	require.Equal(t, "w1", out["id"])
}
