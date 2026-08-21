//go:build integration

package api_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/netlimit"

	"github.com/stretchr/testify/require"
)

// This file closes a coverage gap identified during Phase 4 integration review
// of the counters endpoint (internal/api/server_counters.go). Every existing
// test that exercises the grpc_admission section - unit tests in
// server_counters_test.go, and TestServerCounters_Gating next door - feeds the
// handler a fakeAdmissionSource with hand-picked numbers. Nothing in the repo
// wraps a REAL net.Listener with netlimit.Wrap, opens REAL sockets against it,
// and reads the resulting levels and counts back out of the real HTTP route.
// That is exactly the production wiring cmd/relay-server/main.go assembles
// (grpcLis := netlimit.Wrap(...); httpServer.Counters =
// api.CounterSources{GRPCAdmission: grpcLis}), and it is where a mapping bug
// between netlimit.Occupancy and the wire's "levels" object would actually
// surface. The tests below drive that path end to end.

// countersOf decodes GET /v1/server/counters served by srv using adminToken
// and returns the grpc_admission section.
func countersOf(t *testing.T, srv *api.Server, adminToken string) struct {
	Counts struct {
		RefusedTotal     uint64 `json:"refused_total"`
		RefusedPerSource uint64 `json:"refused_per_source"`
	} `json:"counts"`
	Levels struct {
		LiveTotal       uint64 `json:"live_total"`
		DistinctSources uint64 `json:"distinct_sources"`
		MaxPerSource    uint64 `json:"max_per_source"`
	} `json:"levels"`
} {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/server/counters", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "counters request failed: %s", rec.Body.String())

	var body struct {
		GRPCAdmission struct {
			Counts struct {
				RefusedTotal     uint64 `json:"refused_total"`
				RefusedPerSource uint64 `json:"refused_per_source"`
			} `json:"counts"`
			Levels struct {
				LiveTotal       uint64 `json:"live_total"`
				DistinctSources uint64 `json:"distinct_sources"`
				MaxPerSource    uint64 `json:"max_per_source"`
			} `json:"levels"`
		} `json:"grpc_admission"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.GRPCAdmission
}

// acceptHolder Accepts every connection lis hands it and holds the SERVER SIDE
// net.Conn open (never reads, never writes) until the test closes it directly.
// netlimit's slot-release hook is conn.Close, so the test controls exactly when
// a slot is released by closing the accepted conn itself, rather than relying
// on the client side hanging up and a read failing server-side.
type acceptHolder struct {
	conns chan net.Conn
	done  chan struct{}
}

func startAcceptHolder(lis net.Listener) *acceptHolder {
	h := &acceptHolder{conns: make(chan net.Conn, 64), done: make(chan struct{})}
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				close(h.done)
				return
			}
			h.conns <- c
		}
	}()
	return h
}

// waitForAccepted drains n newly-accepted conns off h.conns, failing the test
// if they do not arrive within the deadline. Bounded: this never blocks
// forever even if the listener silently drops a connection.
func waitForAccepted(t *testing.T, h *acceptHolder, n int) []net.Conn {
	t.Helper()
	out := make([]net.Conn, 0, n)
	deadline := time.After(10 * time.Second)
	for len(out) < n {
		select {
		case c := <-h.conns:
			out = append(out, c)
		case <-deadline:
			t.Fatalf("timed out waiting for %d accepted connections, got %d", n, len(out))
		}
	}
	return out
}

// TestServerCounters_LevelsMoveWithRealSockets proves grpc_admission.levels
// tracks real, live TCP connections through a real netlimit.Listener, and goes
// back down when they close - not just a hand-fed netlimit.Stats value.
func TestServerCounters_LevelsMoveWithRealSockets(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Counters Socket Admin", "counters-socket-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	lis := netlimit.Wrap(raw, netlimit.Config{MaxTotal: 10, MaxPerIP: 10})
	t.Cleanup(func() { _ = lis.Close() })
	srv.Counters = api.CounterSources{GRPCAdmission: lis}

	holder := startAcceptHolder(lis)

	before := countersOf(t, srv, adminToken)
	require.Equal(t, uint64(0), before.Levels.LiveTotal)
	require.Equal(t, uint64(0), before.Levels.DistinctSources)
	require.Equal(t, uint64(0), before.Levels.MaxPerSource)

	// Open three real client connections from this process, all from loopback -
	// the only source address available in a test process, hence
	// DistinctSources == 1 throughout rather than 3.
	clients := make([]net.Conn, 0, 3)
	for i := 0; i < 3; i++ {
		c, err := net.DialTimeout("tcp", raw.Addr().String(), 3*time.Second)
		require.NoError(t, err)
		clients = append(clients, c)
	}
	t.Cleanup(func() {
		for _, c := range clients {
			_ = c.Close()
		}
	})
	serverSide := waitForAccepted(t, holder, 3)

	during := countersOf(t, srv, adminToken)
	require.Equal(t, uint64(3), during.Levels.LiveTotal,
		"three real live connections must be reflected in live_total")
	require.Equal(t, uint64(1), during.Levels.DistinctSources,
		"all three connections share one source address (loopback)")
	require.Equal(t, uint64(3), during.Levels.MaxPerSource,
		"max_per_source must count real per-address occupancy, not a placeholder")

	// Release the slots by closing the SERVER SIDE conns - the hook netlimit
	// actually wires to Close, per conn.Close in internal/netlimit/listener.go.
	for _, c := range serverSide {
		require.NoError(t, c.Close())
	}
	require.Eventually(t, func() bool {
		return countersOf(t, srv, adminToken).Levels.LiveTotal == 0
	}, 5*time.Second, 50*time.Millisecond, "live_total must return to zero once every connection is closed")

	after := countersOf(t, srv, adminToken)
	require.Equal(t, uint64(0), after.Levels.DistinctSources)
	require.Equal(t, uint64(0), after.Levels.MaxPerSource)
}

// TestServerCounters_RefusedCountsMoveWithRealSockets proves
// grpc_admission.counts increments when a real socket is actually refused by a
// real netlimit.Listener, distinguishing the per-source count from the total
// count exactly as internal/netlimit/listener.go's admit() does: the total cap
// is checked first, so a connection that only trips the per-source cap must
// count against refused_per_source, not refused_total.
func TestServerCounters_RefusedCountsMoveWithRealSockets(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Counters Refusal Admin", "counters-refusal-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	// MaxTotal well above MaxPerIP so a third loopback connection trips the
	// per-source cap specifically, not the fleet cap.
	lis := netlimit.Wrap(raw, netlimit.Config{MaxTotal: 10, MaxPerIP: 2})
	t.Cleanup(func() { _ = lis.Close() })
	srv.Counters = api.CounterSources{GRPCAdmission: lis}

	holder := startAcceptHolder(lis)

	var clients []net.Conn
	t.Cleanup(func() {
		for _, c := range clients {
			_ = c.Close()
		}
	})
	for i := 0; i < 2; i++ {
		c, err := net.DialTimeout("tcp", raw.Addr().String(), 3*time.Second)
		require.NoError(t, err)
		clients = append(clients, c)
	}
	waitForAccepted(t, holder, 2)

	before := countersOf(t, srv, adminToken)
	require.Equal(t, uint64(0), before.Counts.RefusedTotal)
	require.Equal(t, uint64(0), before.Counts.RefusedPerSource)

	// A third connection from the same source must be accepted-then-closed by
	// the listener rather than left open: read against it must observe EOF
	// (or a reset) within a bounded window, never hang.
	third, err := net.DialTimeout("tcp", raw.Addr().String(), 3*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = third.Close() })
	require.NoError(t, third.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, readErr := third.Read(make([]byte, 1))
	require.Error(t, readErr, "a refused connection must be closed by the listener, never left open")

	after := countersOf(t, srv, adminToken)
	require.Equal(t, uint64(0), after.Counts.RefusedTotal,
		"the total cap (10) was never reached, so nothing may count against refused_total")
	require.Equal(t, uint64(1), after.Counts.RefusedPerSource,
		"the per-source cap (2) was tripped by the third loopback connection")
}

// TestServerCounters_BothCapsDisabledReportsZeroLevelsWithLiveConnections
// verifies empirically the disclosure documented in netlimit.Listener.Stats,
// README, and server_counters.go's own doc comment: with both
// RELAY_GRPC_MAX_CONNS and RELAY_GRPC_MAX_CONNS_PER_IP effectively disabled
// (MaxTotal<=0 and MaxPerIP<=0), Accept returns every connection UNWRAPPED and
// does no accounting at all, so grpc_admission.levels reads all-zero no matter
// how many connections are actually live. This is the one place "not measured"
// and "nothing there" are the same payload, and it is worth proving against
// real sockets rather than trusting the comment.
func TestServerCounters_BothCapsDisabledReportsZeroLevelsWithLiveConnections(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Counters Disabled Admin", "counters-disabled-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	lis := netlimit.Wrap(raw, netlimit.Config{MaxTotal: 0, MaxPerIP: 0})
	t.Cleanup(func() { _ = lis.Close() })
	srv.Counters = api.CounterSources{GRPCAdmission: lis}

	holder := startAcceptHolder(lis)

	var clients []net.Conn
	t.Cleanup(func() {
		for _, c := range clients {
			_ = c.Close()
		}
	})
	for i := 0; i < 5; i++ {
		c, err := net.DialTimeout("tcp", raw.Addr().String(), 3*time.Second)
		require.NoError(t, err)
		clients = append(clients, c)
	}
	serverSide := waitForAccepted(t, holder, 5)
	t.Cleanup(func() {
		for _, c := range serverSide {
			_ = c.Close()
		}
	})

	got := countersOf(t, srv, adminToken)
	require.Equal(t, uint64(0), got.Levels.LiveTotal,
		"with both caps disabled, live_total must read 0 even with 5 real live connections")
	require.Equal(t, uint64(0), got.Levels.DistinctSources)
	require.Equal(t, uint64(0), got.Levels.MaxPerSource)
	require.Equal(t, uint64(0), got.Counts.RefusedTotal)
	require.Equal(t, uint64(0), got.Counts.RefusedPerSource)
}
