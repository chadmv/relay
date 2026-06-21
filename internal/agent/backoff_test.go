package agent

import (
	"context"
	"net"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

func TestNextReconnectBackoff(t *testing.T) {
	// Healthy session resets to 1s regardless of the prior backoff.
	assert.Equal(t, time.Second, nextReconnectBackoff(32*time.Second, true))
	assert.Equal(t, time.Second, nextReconnectBackoff(60*time.Second, true))
	assert.Equal(t, time.Second, nextReconnectBackoff(time.Second, true))

	// Unhealthy session doubles the backoff.
	assert.Equal(t, 2*time.Second, nextReconnectBackoff(time.Second, false))
	assert.Equal(t, 8*time.Second, nextReconnectBackoff(4*time.Second, false))

	// Doubling is capped at 60s.
	assert.Equal(t, 60*time.Second, nextReconnectBackoff(40*time.Second, false))
	assert.Equal(t, 60*time.Second, nextReconnectBackoff(60*time.Second, false))
}

// registerThenCloseServer accepts one Connect stream, expects a Register
// message, replies with a RegisterResponse, then returns (closing the stream).
type registerThenCloseServer struct {
	relayv1.UnimplementedAgentServiceServer
}

func (registerThenCloseServer) Connect(stream grpc.BidiStreamingServer[relayv1.AgentMessage, relayv1.CoordinatorMessage]) error {
	if _, err := stream.Recv(); err != nil { // the Register message
		return err
	}
	if err := stream.Send(&relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_RegisterResponse{
			RegisterResponse: &relayv1.RegisterResponse{WorkerId: "w-1"},
		},
	}); err != nil {
		return err
	}
	return nil // close the stream -> agent's recv loop sees EOF
}

// TestConnect_ReportsRegisteredAfterHealthySessionDrops drives connect against
// an in-process server that accepts registration then drops the stream. connect
// must report registered==true even though it also returns the drop error, so
// the reconnect backoff resets after a session that genuinely registered.
func TestConnect_ReportsRegisteredAfterHealthySessionDrops(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	relayv1.RegisterAgentServiceServer(srv, registerThenCloseServer{})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	dialContextFn = func(ctx context.Context) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
	defer func() { dialContextFn = nil }()

	creds, _ := LoadCredentials(t.TempDir())
	creds.SetEnrollmentToken("test-enrollment")
	a := NewAgent("passthrough:ignored", Capabilities{Hostname: "h"}, "", creds,
		func(string) error { return nil }, nil)
	a.runCtx = context.Background()

	registered, err := a.connect(context.Background())
	require.True(t, registered, "registered must be true after a session that registered")
	require.Error(t, err, "the dropped stream still surfaces an error")
}

// TestRun_ResetsBackoffBeforeSleepingAfterHealthyDrop is the ordering test for
// the reconnect-backoff-never-resets fix. It seeds the loop with an accumulated
// (capped) backoff, then lets a healthy session register and immediately drop.
// The reset must be applied BEFORE the loop sleeps, so the FIRST wait after the
// drop is the short reset value (~1s), not the stale accumulated value (60s).
//
// Against the buggy after-sleep ordering, the first observed wait is the stale
// 60s; the fix makes it 1s.
func TestRun_ResetsBackoffBeforeSleepingAfterHealthyDrop(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	relayv1.RegisterAgentServiceServer(srv, registerThenCloseServer{})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	dialContextFn = func(ctx context.Context) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
	defer func() { dialContextFn = nil }()

	// Seed an accumulated, capped backoff as if prior failures had ramped it up.
	initialReconnectBackoff = 60 * time.Second
	defer func() { initialReconnectBackoff = time.Second }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Capture the first wait the loop asks for, then cancel so Run returns
	// without ever really sleeping. No real time passes.
	var firstWait time.Duration
	got := make(chan struct{})
	reconnectSleep = func(_ context.Context, d time.Duration) bool {
		firstWait = d
		close(got)
		cancel()
		return false // ctx cancelled: tell the loop to return
	}
	defer func() { reconnectSleep = nil }()

	creds, _ := LoadCredentials(t.TempDir())
	creds.SetEnrollmentToken("test-enrollment")
	a := NewAgent("passthrough:ignored", Capabilities{Hostname: "h"}, "", creds,
		func(string) error { return nil }, nil)

	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()

	select {
	case <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("loop never reached the reconnect sleep")
	}
	<-done

	assert.Equal(t, time.Second, firstWait,
		"first wait after a healthy session drop must be the reset value, not the stale accumulated backoff")
}

// TestRun_FirstUnhealthyFailureWaitsOneSecond guards requirement #3: a first
// unhealthy failure from a fresh start (connect never registers) still waits
// ~1s before retrying. The reset-before-sleep fix must not regress this initial
// wait to 2s by doubling before the first sleep.
func TestRun_FirstUnhealthyFailureWaitsOneSecond(t *testing.T) {
	// Dialer always fails, so connect returns registered=false before it ever
	// registers: a fresh-start unhealthy failure.
	dialContextFn = func(context.Context) (net.Conn, error) {
		return nil, assert.AnError
	}
	defer func() { dialContextFn = nil }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var firstWait time.Duration
	got := make(chan struct{})
	reconnectSleep = func(_ context.Context, d time.Duration) bool {
		firstWait = d
		close(got)
		cancel()
		return false
	}
	defer func() { reconnectSleep = nil }()

	creds, _ := LoadCredentials(t.TempDir())
	creds.SetEnrollmentToken("test-enrollment")
	a := NewAgent("passthrough:ignored", Capabilities{Hostname: "h"}, "", creds,
		func(string) error { return nil }, nil)

	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()

	select {
	case <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("loop never reached the reconnect sleep")
	}
	<-done

	assert.Equal(t, time.Second, firstWait,
		"first unhealthy failure from a fresh start must wait ~1s, not double to 2s")
}
