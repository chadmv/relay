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

func TestListJobs_PassesQueryParams(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/jobs", r.URL.Path)
		require.Equal(t, "running", r.URL.Query().Get("status"))
		require.Equal(t, "10", r.URL.Query().Get("limit"))
		require.Equal(t, "abc", r.URL.Query().Get("cursor"))
		_ = json.NewEncoder(w).Encode(relayclient.PageEnvelope[map[string]any]{
			Items:      []map[string]any{{"id": "j1", "status": "running"}},
			NextCursor: "next",
			Total:      42,
		})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callListJobs(context.Background(), listJobsArgs{
		Status: "running", Limit: 10, Cursor: "abc",
	})
	require.Nil(t, terr)
	require.Len(t, out["items"], 1)
	require.Equal(t, "next", out["next_cursor"])
	require.Equal(t, int64(42), out["total"])
}

func TestGetJob_HappyPath(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/jobs/j1", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "name": "n", "status": "done"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callGetJob(context.Background(), getJobArgs{JobID: "j1"})
	require.Nil(t, terr)
	require.Equal(t, "j1", out["id"])
}

func TestGetJob_NotFound(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no such job"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	_, terr := s.callGetJob(context.Background(), getJobArgs{JobID: "j1"})
	require.NotNil(t, terr)
	require.Equal(t, "not_found", terr.Code)
}

func TestGetJob_MissingID(t *testing.T) {
	s, _ := NewServer("http://x", "t")
	_, terr := s.callGetJob(context.Background(), getJobArgs{JobID: ""})
	require.NotNil(t, terr)
	require.Equal(t, "validation", terr.Code)
}
