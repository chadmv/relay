package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitForJob_TerminalImmediately(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/jobs/j1", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "status": "done"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callWaitForJob(context.Background(), waitForJobArgs{JobID: "j1", TimeoutSeconds: 5})
	require.Nil(t, terr)
	require.Equal(t, "done", out["status"])
	require.NotContains(t, out, "timed_out")
}

func TestWaitForJob_RunningThenDone(t *testing.T) {
	var n int32
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&n, 1)
		status := "running"
		if current >= 2 {
			status = "done"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "status": status})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	s.waitPoll = 10 * time.Millisecond

	out, terr := s.callWaitForJob(context.Background(), waitForJobArgs{JobID: "j1", TimeoutSeconds: 5})
	require.Nil(t, terr)
	require.Equal(t, "done", out["status"])
}

func TestWaitForJob_Timeout(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "status": "running"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	s.waitPoll = 10 * time.Millisecond

	out, terr := s.callWaitForJob(context.Background(), waitForJobArgs{JobID: "j1", TimeoutSeconds: 1})
	require.Nil(t, terr)
	require.Equal(t, true, out["timed_out"])
	require.Equal(t, "running", out["last_state"].(map[string]any)["status"])
}

func TestWaitForJob_NegativeTimeout(t *testing.T) {
	backend := newWhoamiBackend(t, true)
	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)
	_, terr := s.callWaitForJob(context.Background(), waitForJobArgs{JobID: "j", TimeoutSeconds: -1})
	require.Equal(t, "validation", terr.Code)
}

func TestWaitForJob_TimeoutTooLarge(t *testing.T) {
	backend := newWhoamiBackend(t, true)
	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)
	_, terr := s.callWaitForJob(context.Background(), waitForJobArgs{JobID: "j", TimeoutSeconds: 9999})
	require.Equal(t, "validation", terr.Code)
}
