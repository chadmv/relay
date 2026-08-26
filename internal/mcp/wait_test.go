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

func TestNextWaitInterval_AdaptiveSchedule(t *testing.T) {
	// First fastWaitCount (4) intervals are the fast poll; everything after is steady.
	require.Equal(t, 500*time.Millisecond, nextWaitInterval(0))
	require.Equal(t, 500*time.Millisecond, nextWaitInterval(1))
	require.Equal(t, 500*time.Millisecond, nextWaitInterval(2))
	require.Equal(t, 500*time.Millisecond, nextWaitInterval(3))
	require.Equal(t, 2*time.Second, nextWaitInterval(4))
	require.Equal(t, 2*time.Second, nextWaitInterval(5))
	require.Equal(t, 2*time.Second, nextWaitInterval(100))
}

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

func TestWaitForJob_AdaptiveScheduleFastJob(t *testing.T) {
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
	// waitPoll left 0: exercise the adaptive schedule (first sleep is fastWaitPoll).

	start := time.Now()
	out, terr := s.callWaitForJob(context.Background(), waitForJobArgs{JobID: "j1", TimeoutSeconds: 5})
	require.Nil(t, terr)
	require.Equal(t, "done", out["status"])
	require.GreaterOrEqual(t, atomic.LoadInt32(&n), int32(2))
	// The first inter-poll sleep is the adaptive fastWaitPoll (500ms), so the call
	// returns well under 1.5s. A regression to a flat defaultWaitPoll (2s) would
	// sleep ~2s here and blow this bound, keeping the assertion non-vacuous.
	require.Less(t, time.Since(start), 1500*time.Millisecond)
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

// relay_wait_for_job reads exactly one field of the poll response, `status`, and
// has never needed the task list. Until handleGetJob started checking
// ListTasksByJob's error, a failure of that read answered 200 with `tasks` absent
// and this loop never noticed. It answers 500 now - which is the honest answer,
// and is right for the client that made the change - and the first one of those
// used to destroy a wait that may have been polling for minutes.
//
// The caller cannot recover from that: the job is still running, the wait is
// gone, and nothing about the failure had anything to do with the field being
// waited on.
func TestWaitForJob_TransientServerError_KeepsPolling(t *testing.T) {
	var n int32
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&n, 1) {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "status": "running"})
		case 2:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "status": "done"})
		}
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	s.waitPoll = 10 * time.Millisecond

	out, terr := s.callWaitForJob(context.Background(), waitForJobArgs{JobID: "j1", TimeoutSeconds: 5})
	require.Nil(t, terr, "a transient failure of a field this tool does not read must not end the wait")
	require.Equal(t, "done", out["status"])
}

// The other side of the same line. Tolerance is for failures that a later poll
// can outlive; it is not a licence to poll a job that does not exist, a token
// that has expired, or an id the server rejects. Those answers are as true on the
// hundredth read as on the first, so they end the wait at once.
func TestWaitForJob_NonTransientError_EndsTheWaitAtOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		want string
	}{
		{"not found", http.StatusNotFound, "not_found"},
		{"bad id", http.StatusBadRequest, "validation"},
		{"forbidden", http.StatusForbidden, "forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n int32
			srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&n, 1)
				w.WriteHeader(tc.code)
			}))
			defer srv.Close()

			s, _ := NewServer(srv.URL, "t")
			s.waitPoll = 10 * time.Millisecond

			_, terr := s.callWaitForJob(context.Background(), waitForJobArgs{JobID: "j1", TimeoutSeconds: 5})
			require.NotNil(t, terr)
			require.Equal(t, tc.want, terr.Code)
			require.Equal(t, int32(1), atomic.LoadInt32(&n),
				"an answer that will be the same on every later poll is asked for once")
		})
	}
}

// Tolerance is bounded, so a server that is simply down is reported rather than
// waited out to the deadline. The bound is on CONSECUTIVE failures, so a flaky
// server that answers in between keeps the wait alive indefinitely, which is what
// TestWaitForJob_TransientServerError_KeepsPolling covers.
func TestWaitForJob_PersistentServerError_GivesUpAfterTheBound(t *testing.T) {
	var n int32
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	s.waitPoll = 10 * time.Millisecond

	_, terr := s.callWaitForJob(context.Background(), waitForJobArgs{JobID: "j1", TimeoutSeconds: 30})
	require.NotNil(t, terr)
	require.Equal(t, "server_error", terr.Code)
	require.Equal(t, int32(maxConsecutiveWaitFailures), atomic.LoadInt32(&n),
		"the loop tolerates a bounded run of failures and then reports the last one")
}
