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

func TestCreateSchedule_HappyPath(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/scheduled-jobs", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), `"name":"nightly"`)
		require.Contains(t, string(body), `"cron_expr":"0 2 * * *"`)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "s1", "name": "nightly", "cron_expr": "0 2 * * *"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callCreateSchedule(context.Background(), createScheduleArgs{
		Name:     "nightly",
		CronExpr: "0 2 * * *",
		JobSpec:  jobspec.JobSpec{Name: "job", Tasks: []jobspec.TaskSpec{{Name: "t", Command: []string{"echo"}}}},
	})
	require.Nil(t, terr)
	require.Equal(t, "s1", out["id"])
}

func TestCreateSchedule_BadCron(t *testing.T) {
	s, _ := NewServer("http://x", "t")
	_, terr := s.callCreateSchedule(context.Background(), createScheduleArgs{
		Name: "x", CronExpr: "",
		JobSpec: jobspec.JobSpec{Name: "j", Tasks: []jobspec.TaskSpec{{Name: "t", Command: []string{"x"}}}},
	})
	require.Equal(t, "validation", terr.Code)
	require.Contains(t, terr.Message, "cron_expr")
}

func TestUpdateSchedule_HappyPath(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PATCH", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/s1", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), `"enabled":false`)
		require.False(t, strings.Contains(string(body), `"cron_expr"`))
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "s1", "enabled": false})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	enabled := false
	out, terr := s.callUpdateSchedule(context.Background(), updateScheduleArgs{
		ScheduleID: "s1", Enabled: &enabled,
	})
	require.Nil(t, terr)
	require.Equal(t, false, out["enabled"])
}

func TestDeleteSchedule_HappyPath(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/s1", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callDeleteSchedule(context.Background(), deleteScheduleArgs{ScheduleID: "s1"})
	require.Nil(t, terr)
	require.Equal(t, true, out["ok"])
}
