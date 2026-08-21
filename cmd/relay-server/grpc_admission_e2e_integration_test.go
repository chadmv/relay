//go:build integration

package main

// This file closes a coverage gap identified during Phase 4 integration review
// of the gRPC admission-bounds slice (grpc_config.go, internal/netlimit). Every
// existing test exercises the production pieces in isolation:
//
//   - grpc_server_test.go serves grpcServerOptions(...) over a real listener,
//     but with a STUB AgentServiceServer, never worker.Handler.
//   - internal/worker's integration suite drives worker.Handler directly against
//     a real Postgres container, but with an in-process fakeStream, never a real
//     net.Listener, never grpcServerOptions, never netlimit.Wrap.
//
// Nothing in the repo starts a server built from grpcServerOptions(...), wrapped
// by netlimit.Wrap, backed by the real worker.Handler and a real Postgres
// container, and then completes a real register handshake over a real TCP
// connection with a real gRPC client. That is exactly the path
// cmd/relay-server/main.go wires in production. The tests below drive that path
// end to end.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/netlimit"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/tokenhash"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// newTestPoolAndQueries is newTestQueries (bootstrap_test.go) plus the
// *pgxpool.Pool it discards. worker.Handler needs the pool itself, not just the
// *store.Queries wrapping it: applyInventory opens its own transaction via
// pgx.BeginTxFunc(ctx, h.pool, ...) even for an empty inventory update, on
// every finishRegister call including the reconnect path this file exercises.
func newTestPoolAndQueries(t *testing.T) (*pgxpool.Pool, *store.Queries) {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("relay_test"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	migrateDSN := "pgx5" + dsn[len("postgres"):]
	require.NoError(t, store.Migrate(migrateDSN))
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool, store.New(pool)
}

// seedE2EWorker creates a real worker row with a known agent token, the
// reconnect credential a long-lived agent presents (internal/agent/agent.go
// reads this token from <state-dir>/token and sends it as
// RegisterRequest_AgentToken on every reconnect).
func seedE2EWorker(t *testing.T, ctx context.Context, q *store.Queries, hostname string) string {
	t.Helper()
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: hostname, Hostname: hostname, CpuCores: 4, RamGb: 8, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)
	raw := "e2e-agent-token-" + hostname
	hash := tokenhash.Hash(raw)
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{ID: w.ID, AgentTokenHash: &hash}))
	return raw
}

// startProductionGRPCServer wires a listener exactly as cmd/relay-server's
// main() does: grpcServerOptions -> netlimit.Wrap -> grpc.Server ->
// worker.Handler, over real TCP on loopback.
func startProductionGRPCServer(t *testing.T, pool *pgxpool.Pool, q *store.Queries, cfg netlimit.Config, b grpcBounds) string {
	t.Helper()
	addr, _ := startProductionGRPCServerWithHandler(t, pool, q, cfg, b)
	return addr
}

// startProductionGRPCServerWithHandler is the same wiring, returning the
// *worker.Handler it registered. Only the counters test needs it: its whole
// subject is that the handler serving gRPC and the handler reporting counts are
// ONE object, so it cannot let the constructor stay hidden in here.
func startProductionGRPCServerWithHandler(t *testing.T, pool *pgxpool.Pool, q *store.Queries, cfg netlimit.Config, b grpcBounds) (string, *worker.Handler) {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	lis := netlimit.Wrap(raw, cfg)

	registry := worker.NewRegistry()
	broker := events.NewBroker()
	handler := worker.NewHandler(q, pool, registry, broker, func() {})

	srv := grpc.NewServer(grpcServerOptions(b)...)
	relayv1.RegisterAgentServiceServer(srv, handler)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return raw.Addr().String(), handler
}

func dialE2E(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cc.Close() })
	return cc
}

// registerOverRealConnection opens a stream, sends a real reconnect
// RegisterRequest carrying rawAgentToken, and returns the WorkerId the real
// worker.Handler resolved from a real Postgres lookup. The stream is left open
// (not closed) so the caller controls how long the connection lives - this is
// what lets the overlap test hold two connections open at once.
func registerOverRealConnection(t *testing.T, cc *grpc.ClientConn, hostname, rawAgentToken string) (relayv1.AgentService_ConnectClient, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	stream, err := relayv1.NewAgentServiceClient(cc).Connect(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: hostname,
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawAgentToken},
			},
		},
	}))

	msg, err := stream.Recv()
	require.NoError(t, err)
	resp := msg.GetRegisterResponse()
	require.NotNil(t, resp, "first message from a real production server must be a RegisterResponse")
	return stream, resp.WorkerId
}

// TestGRPCAdmissionEndToEnd_RealAgentRegistersThroughProductionServer proves the
// production option list and the netlimit-wrapped listener do not break a
// legitimate agent's register handshake against a real worker.Handler and a
// real Postgres container. Every existing test either fakes the network or
// fakes the handler; this one fakes neither.
func TestGRPCAdmissionEndToEnd_RealAgentRegistersThroughProductionServer(t *testing.T) {
	pool, q := newTestPoolAndQueries(t)
	ctx := context.Background()
	rawToken := seedE2EWorker(t, ctx, q, "e2e-happy-path")

	addr := startProductionGRPCServer(t, pool, q,
		netlimit.Config{MaxTotal: 10, MaxPerIP: 10},
		grpcBounds{maxConns: 10, maxConnsPerIP: 10, maxConnIdle: 0})

	cc := dialE2E(t, addr)
	stream, workerID := registerOverRealConnection(t, cc, "e2e-happy-path", rawToken)
	require.NotEmpty(t, workerID)

	w, err := q.GetWorker(ctx, mustParseUUID(t, workerID))
	require.NoError(t, err)
	require.Equal(t, "e2e-happy-path", w.Hostname)

	require.NoError(t, stream.CloseSend())
}

// mustParseUUID converts the WorkerId string the wire protocol returns back
// into pgtype.UUID for a direct DB lookup, proving the id round-trips through
// the real handler rather than trusting the response blindly.
func mustParseUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	require.NoError(t, u.Scan(s))
	return u
}

// TestGRPCAdmissionEndToEnd_ReconnectOverlapFromSameSourceIP answers the NAT
// question directly: internal/agent.Agent dials a fresh ClientConn per
// reconnect attempt and closes the old one only after the new one is up
// (internal/agent/agent.go:184-212), so two connections from the SAME source
// address briefly overlap. This test holds two REAL, fully-registered
// connections open at once from 127.0.0.1 (the only source address available in
// a test process) and measures how long the overlap lasted, to give the
// per-IP default (RELAY_GRPC_MAX_CONNS_PER_IP=64) a real number to be judged
// against instead of a theoretical one.
//
// The per-IP cap here is 2, the minimum that admits a legitimate two-agent
// overlap without masking the measurement behind a much larger production
// default.
func TestGRPCAdmissionEndToEnd_ReconnectOverlapFromSameSourceIP(t *testing.T) {
	pool, q := newTestPoolAndQueries(t)
	ctx := context.Background()
	tokenOld := seedE2EWorker(t, ctx, q, "e2e-overlap-old")
	tokenNew := seedE2EWorker(t, ctx, q, "e2e-overlap-new")

	addr := startProductionGRPCServer(t, pool, q,
		netlimit.Config{MaxTotal: 10, MaxPerIP: 2},
		grpcBounds{maxConns: 10, maxConnsPerIP: 2, maxConnIdle: 0})

	ccOld := dialE2E(t, addr)
	streamOld, _ := registerOverRealConnection(t, ccOld, "e2e-overlap-old", tokenOld)
	t.Cleanup(func() { _ = streamOld.CloseSend() })

	overlapStart := time.Now()
	ccNew := dialE2E(t, addr)
	streamNew, _ := registerOverRealConnection(t, ccNew, "e2e-overlap-new", tokenNew)
	overlap := time.Since(overlapStart)
	t.Logf("real register-to-register overlap window from one source IP: %s", overlap)

	// Both connections are simultaneously live: the old one must still be
	// usable, proving it was never torn down by the new one's admission.
	require.NoError(t, streamOld.Send(&relayv1.AgentMessage{}),
		"the OLD connection must still be usable while the NEW one is registering - "+
			"this is the overlap a reconnecting agent produces")

	// A third connection from the same source IP must now be refused: both
	// per-IP slots are held by streamOld and streamNew. Raw TCP, not a gRPC
	// client, matching TestGRPCServer_ConnectionBeyondPerIPCapIsRefused - a
	// refusal expressed as anything other than accept-then-close would either
	// hang the client or, worse, take Serve down for every other peer.
	rawThird, err := net.DialTimeout("tcp", addr, 3*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawThird.Close() })
	require.NoError(t, rawThird.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, err = rawThird.Read(make([]byte, 1))
	require.Error(t, err, "a third connection from one source IP must be refused (per-IP cap is 2)")

	require.NoError(t, streamNew.CloseSend())
}

// TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers is the
// EXECUTED half of the ingest section's wiring, and it exists because
// buildHTTPServer's own comment claimed no such thing was possible: "nothing
// executable can check it from here". That was true of cmd/relay-server's
// default lane and false of this one, and the asymmetry it left behind is what
// makes the claim worth disproving - grpc_admission's twin,
// TestBuildHTTPServer_ServesTheRealListenersCounters, dials real sockets and
// reads the occupancy back, so substituting its source reddens; the ingest
// section had only TestServerCountersIsWiredByMain's IDENTIFIER check on main's
// call site, and NOTHING checked that buildHTTPServer forwards the value it was
// handed. Measured, at HEAD before this test:
//
//	if d.agentHandler != nil {
//	    s.Counters.IngestLogBudget = worker.NewHandler(d.q, d.pool, d.registry, d.broker, func() {})
//	}
//
// compiled, vetted clean under both tag sets, and left internal/worker,
// internal/api and cmd/relay-server green. The typed-nil test still passed (the
// nil decision still reads d.agentHandler) and
// TestBuildHTTPServer_ServesTheWiredHandlersIngestSection still passed (it
// asserts the section exists with five keys per arm, and a fresh Handler's
// section does). In production that serves ingest_log_budget as permanent zeros
// while the real budget drops thousands of lines - a CONFIDENT ZERO, which
// counters_wiring_test.go itself calls worse than no endpoint.
//
// The numbers here can only come from the Handler that ran the recv loop: this
// test floods a REAL registered stream with unparseable task ids, which
// handleTaskLog's bad-id arm counts on the recv goroutine, and then reads them
// back through the REAL admin-gated route on a server built by buildHTTPServer.
// A second Handler, a stub, or a dropped assignment all report zero.
//
// WHY THE INTEGRATION LANE AND NOT THE DEFAULT ONE, stated so nobody assumes CI
// covers it: moving an ingest counter requires reaching Connect's message loop,
// which is past authenticateAndRegister, which is a Postgres round trip. There
// is no seam short of exporting one from internal/worker purely for a test, and
// go-ci runs `go test -race ./...` with no tag, so this guard runs under `make
// test-integration` only. Its cheap companion is the identifier check in
// TestServerCountersIsWiredByMain; the two answer different questions and
// neither replaces the other.
func TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers(t *testing.T) {
	pool, q := newTestPoolAndQueries(t)
	ctx := context.Background()
	rawToken := seedE2EWorker(t, ctx, q, "e2e-ingest-counters")

	addr, handler := startProductionGRPCServerWithHandler(t, pool, q,
		netlimit.Config{MaxTotal: 10, MaxPerIP: 10},
		grpcBounds{maxConns: 10, maxConnsPerIP: 10, maxConnIdle: 0})

	// Built exactly as main builds it, from the handler that is already serving
	// gRPC above.
	srv := buildHTTPServer(httpServerDeps{
		addr:         "127.0.0.1:0",
		q:            store.New(stubAdminDB{}),
		agentHandler: handler,
	})

	require.Zero(t, ingestDedupedCount(t, srv, "bad_task_id_log"),
		"fixture: nothing has been dropped yet, so the section is present and zero")

	cc := dialE2E(t, addr)
	stream, _ := registerOverRealConnection(t, cc, "e2e-ingest-counters", rawToken)

	// kindBadTaskIDLog carries NO wire value, so the whole flood is one dedupe
	// key: one log line and flood-1 deduped drops. No task and no DB write is
	// involved - handleTaskLog's bad-id arm returns before h.q is touched - so
	// the only thing under test is where the count landed.
	const flood = 40
	for i := 0; i < flood; i++ {
		require.NoError(t, stream.Send(&relayv1.AgentMessage{
			Payload: &relayv1.AgentMessage_TaskLog{TaskLog: &relayv1.TaskLogChunk{
				TaskId: "not-a-uuid", Content: []byte("x"),
			}},
		}))
	}

	// The recv loop is asynchronous, so poll the route rather than sleeping. An
	// EXACT target, not "non-zero": a partially-drained stream would otherwise
	// let this pass while proving less than it claims.
	deadline := time.Now().Add(30 * time.Second)
	var got uint64
	for time.Now().Before(deadline) {
		got = ingestDedupedCount(t, srv, "bad_task_id_log")
		if got == uint64(flood-1) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, uint64(flood-1), got,
		"GET /v1/server/counters must report the drops of the Handler that ran this connection's recv "+
			"loop. Zero here means buildHTTPServer served some OTHER source - a second Handler, a stub, "+
			"or nothing - and the endpoint is a confident zero over a live flood.")
	require.Zero(t, ingestSuppressedCount(t, srv, "bad_task_id_log"),
		"one repeating key costs one token per dedupe window, so nothing is budget-suppressed")
	require.Zero(t, ingestDedupedCount(t, srv, "task_log_persist"),
		"the drops must be attributed to the kind that lost them")

	require.NoError(t, stream.CloseSend())
}

// ingestDedupedCount and ingestSuppressedCount read one published kind back
// through the real admin-gated route, decoding the payload the way an operator's
// poller would rather than reaching into the Handler.
func ingestDedupedCount(t *testing.T, srv *http.Server, kind string) uint64 {
	t.Helper()
	return ingestCount(t, srv, "deduped", kind)
}

func ingestSuppressedCount(t *testing.T, srv *http.Server, kind string) uint64 {
	t.Helper()
	return ingestCount(t, srv, "suppressed", kind)
}

func ingestCount(t *testing.T, srv *http.Server, arm, kind string) uint64 {
	t.Helper()
	top := countersAsAdmin(t, srv)
	raw, ok := top["ingest_log_budget"]
	require.True(t, ok, "the ingest_log_budget section is ABSENT, so the handler was never wired at all")

	var section struct {
		Counts map[string]map[string]uint64 `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(raw, &section))
	require.Contains(t, section.Counts, arm)
	require.Contains(t, section.Counts[arm], kind)
	return section.Counts[arm][kind]
}
