package main

import (
	"context"
	"net"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// blockingAgentService is a stub AgentServiceServer that holds every stream open
// until its context is cancelled and sends nothing. Sending nothing matters:
// grpc-go resets the ping-strike counter on every outgoing DATA or HEADERS frame
// (http2_server.go:1046, :1114, :1158), and a chatty stub would mask policy
// behaviour. No database is involved, so these tests stay in `make test`.
type blockingAgentService struct {
	relayv1.UnimplementedAgentServiceServer
	entered chan struct{}
}

func (s *blockingAgentService) Connect(stream grpc.BidiStreamingServer[relayv1.AgentMessage, relayv1.CoordinatorMessage]) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-stream.Context().Done()
	return nil
}

// startTestGRPCServer serves the PRODUCTION option list over a fresh loopback
// listener and returns its address plus a channel that receives once per stream
// the handler enters. The listener is the raw one - connection-cap wiring is
// exercised by the end-to-end test below, not here.
func startTestGRPCServer(t *testing.T, b grpcBounds) (string, chan struct{}) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	stub := &blockingAgentService{entered: make(chan struct{}, 8)}
	srv := grpc.NewServer(grpcServerOptions(b)...)
	relayv1.RegisterAgentServiceServer(srv, stub)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), stub.entered
}

func dialTestServer(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cc.Close() })
	return cc
}

// waitReady forces the transport up and waits for the HTTP/2 handshake, which is
// what guarantees the server's SETTINGS frame has been applied client-side.
func waitReady(t *testing.T, cc *grpc.ClientConn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cc.Connect()
	for {
		s := cc.GetState()
		if s == connectivity.Ready {
			return
		}
		require.True(t, cc.WaitForStateChange(ctx, s), "transport never became READY")
	}
}

// TestGRPCServer_SecondStreamOnOneConnectionBlocks.
//
// THE DEADLINE IS THE ASSERTION, NOT A CONVENIENCE. A compliant grpc-go client
// never sees the server's RST_STREAM(REFUSED_STREAM): checkForStreamQuota parks
// on t.streamsQuotaAvailable with ctx.Done() as the only escape
// (http2_client.go:829-836, :885-908). A bare "expect an error" here would hang
// forever.
//
// RED at HEAD: with no SETTINGS_MAX_CONCURRENT_STREAMS advertised
// (http2_server.go:178-183) the client falls back to 100 (defaults.go:34), so
// the second stream opens instantly and err is nil.
func TestGRPCServer_SecondStreamOnOneConnectionBlocks(t *testing.T) {
	addr, entered := startTestGRPCServer(t, grpcBounds{})
	cc := dialTestServer(t, addr)
	waitReady(t, cc)
	client := relayv1.NewAgentServiceClient(cc)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	_, err := client.Connect(ctx1)
	require.NoError(t, err, "the first stream must open")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the server handler never entered for stream 1")
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	start := time.Now()
	_, err = client.Connect(ctx2)
	require.Error(t, err,
		"a SECOND concurrent stream on ONE connection must not open: grpcMaxConcurrentStreams is 1")
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
	assert.GreaterOrEqual(t, time.Since(start), 1500*time.Millisecond,
		"it must have BLOCKED on stream quota until the deadline, not failed fast for some other reason")
}

// signalListener reports every server-side conn Close on ch. This is a
// SERVER-SIDE signal on purpose: asserting via the client's connectivity state
// would depend on grpc-go's reconnect state machine rather than on whether the
// transport was reaped. ch is buffered because grpc-go double-closes routinely.
type signalListener struct {
	net.Listener
	ch chan struct{}
}

func (l *signalListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &signalConn{Conn: c, ch: l.ch}, nil
}

type signalConn struct {
	net.Conn
	ch chan struct{}
}

func (c *signalConn) Close() error {
	err := c.Conn.Close()
	select {
	case c.ch <- struct{}{}:
	default:
	}
	return err
}

func startTestGRPCServerWithCloseSignal(t *testing.T, b grpcBounds) (string, chan struct{}, chan struct{}) {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closed := make(chan struct{}, 16)

	stub := &blockingAgentService{entered: make(chan struct{}, 8)}
	srv := grpc.NewServer(grpcServerOptions(b)...)
	relayv1.RegisterAgentServiceServer(srv, stub)
	go func() { _ = srv.Serve(&signalListener{Listener: raw, ch: closed}) }()
	t.Cleanup(srv.Stop)
	return raw.Addr().String(), stub.entered, closed
}

// TestGRPCServer_IdleConnectionWithNoStreamIsClosed.
//
// RED at HEAD: defaultMaxConnectionIdle is infinity (defaults.go:35), so the
// connection is never reaped and this fails on its 5s bound. Without this
// option, a connection cap is a PARKING PRIMITIVE: an attacker completes the
// HTTP/2 preface, opens no stream, and holds RELAY_GRPC_MAX_CONNS_PER_IP slots
// forever.
func TestGRPCServer_IdleConnectionWithNoStreamIsClosed(t *testing.T) {
	addr, _, closed := startTestGRPCServerWithCloseSignal(t, grpcBounds{maxConnIdle: 200 * time.Millisecond})
	cc := dialTestServer(t, addr)
	waitReady(t, cc) // preface done, transport up, zero streams

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("a transport that completed the HTTP/2 preface and opened no stream was never closed: " +
			"MaxConnectionIdle is not set")
	}
}

// TestGRPCServer_ConnectionHoldingAStreamIsNotIdle proves the reading of t.idle
// behind grpcKeepaliveParams and prices MaxConnectionIdle at zero for a
// legitimate agent, which holds ONE silent stream for hours at a time. It is
// also what catches MaxConnectionAge-style semantics slipping in under this name.
func TestGRPCServer_ConnectionHoldingAStreamIsNotIdle(t *testing.T) {
	addr, entered, closed := startTestGRPCServerWithCloseSignal(t, grpcBounds{maxConnIdle: 200 * time.Millisecond})
	cc := dialTestServer(t, addr)
	waitReady(t, cc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := relayv1.NewAgentServiceClient(cc).Connect(ctx)
	require.NoError(t, err)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the server handler never entered")
	}

	// Ten idle windows of complete silence in both directions.
	time.Sleep(2 * time.Second)

	select {
	case <-closed:
		t.Fatal("a connection HOLDING A STREAM was closed. MaxConnectionIdle must never terminate a " +
			"working connection - if this fails, something gave it MaxConnectionAge semantics, which is " +
			"explicitly out of scope for this slice.")
	default:
	}
	require.NoError(t, stream.Send(&relayv1.AgentMessage{}),
		"the stream must still be usable after ten idle windows")
}
