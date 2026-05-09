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

func TestListSchedules_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/scheduled-jobs", r.URL.Path)
		_ = json.NewEncoder(w).Encode(relayclient.PageEnvelope[map[string]any]{
			Items: []map[string]any{{"id": "s1", "name": "nightly"}},
		})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callListSchedules(context.Background(), listSchedulesArgs{})
	require.Nil(t, terr)
	require.Len(t, out["items"], 1)
}

func TestGetSchedule_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/scheduled-jobs/s1", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "s1", "cron_expr": "0 2 * * *"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callGetSchedule(context.Background(), getScheduleArgs{ScheduleID: "s1"})
	require.Nil(t, terr)
	require.Equal(t, "s1", out["id"])
}
