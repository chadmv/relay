package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunScheduleNow_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/s1/run-now", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "newjob", "status": "pending"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callRunScheduleNow(context.Background(), runScheduleNowArgs{ScheduleID: "s1"})
	require.Nil(t, terr)
	require.Equal(t, "newjob", out["job_id"])
}

func TestRunScheduleNow_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin only"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	_, terr := s.callRunScheduleNow(context.Background(), runScheduleNowArgs{ScheduleID: "s1"})
	require.Equal(t, "forbidden", terr.Code)
}
