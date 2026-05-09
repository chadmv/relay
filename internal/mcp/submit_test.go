package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/jobspec"

	"github.com/stretchr/testify/require"
)

func TestSubmitJob_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/jobs", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), `"name":"test-job"`)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "newjob", "status": "pending"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	spec := jobspec.JobSpec{
		Name:  "test-job",
		Tasks: []jobspec.TaskSpec{{Name: "t1", Command: []string{"echo", "hi"}}},
	}
	out, terr := s.callSubmitJob(context.Background(), submitJobArgs{JobSpec: spec})
	require.Nil(t, terr)
	require.Equal(t, "newjob", out["job_id"])
	require.Equal(t, "pending", out["status"])
}

func TestSubmitJob_ValidationError(t *testing.T) {
	s, _ := NewServer("http://x", "t")
	out, terr := s.callSubmitJob(context.Background(), submitJobArgs{
		JobSpec: jobspec.JobSpec{Name: ""}, // missing name
	})
	require.Nil(t, out)
	require.Equal(t, "validation", terr.Code)
	require.Contains(t, terr.Message, "name is required")
}

func TestSubmitJob_ServerRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "server-side validation failed"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	_, terr := s.callSubmitJob(context.Background(), submitJobArgs{
		JobSpec: jobspec.JobSpec{
			Name: "ok", Tasks: []jobspec.TaskSpec{{Name: "t", Command: []string{"x"}}},
		},
	})
	require.Equal(t, "validation", terr.Code)
	require.True(t, strings.Contains(terr.Message, "server-side"))
}
