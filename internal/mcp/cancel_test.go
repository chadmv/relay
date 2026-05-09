package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCancelJob_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/v1/jobs/j1", r.URL.Path)
		require.Empty(t, r.URL.Query().Get("force")) // no force in v1
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "cancelled"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callCancelJob(context.Background(), cancelJobArgs{JobID: "j1"})
	require.Nil(t, terr)
	require.Equal(t, "cancelled", out["status"])
}

func TestCancelJob_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no such job"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	_, terr := s.callCancelJob(context.Background(), cancelJobArgs{JobID: "j1"})
	require.Equal(t, "not_found", terr.Code)
}
