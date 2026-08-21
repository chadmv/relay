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
