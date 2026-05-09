package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListTasks_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/jobs/j1/tasks", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "t1", "name": "task-a"},
			{"id": "t2", "name": "task-b"},
		})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callListTasks(context.Background(), listTasksArgs{JobID: "j1"})
	require.Nil(t, terr)
	require.Len(t, out["items"], 2)
}

func TestListTasks_MissingJobID(t *testing.T) {
	s, _ := NewServer("http://x", "t")
	_, terr := s.callListTasks(context.Background(), listTasksArgs{})
	require.Equal(t, "validation", terr.Code)
}

func TestGetTask_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/tasks/t1", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "t1", "status": "done"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callGetTask(context.Background(), getTaskArgs{TaskID: "t1"})
	require.Nil(t, terr)
	require.Equal(t, "t1", out["id"])
}
