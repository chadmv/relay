package worker

import (
	"context"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// scriptedStream is the minimum grpc.BidiStreamingServer the registration path
// touches. It is deliberately NOT handler_test.go's fakeStream: that file is
// //go:build integration, and this behaviour needs no database at all.
//
// Recv hands back queued messages, then BLOCKS until release is closed - which
// is what a peer that opens a stream and says nothing looks like from here. The
// block is also how the goroutine leak is measured: the recv goroutine must
// still be parked in Recv when Connect returns, and must exit once the stream
// is torn down.
type scriptedStream struct {
	msgs     []*relayv1.AgentMessage
	pos      int
	ctx      context.Context
	release  chan struct{}
	recvDone chan struct{} // closed by the Recv that returns after release
	delay    time.Duration // sleep before handing back the first message
}

func (s *scriptedStream) Recv() (*relayv1.AgentMessage, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
		s.delay = 0
	}
	if s.pos < len(s.msgs) {
		m := s.msgs[s.pos]
		s.pos++
		return m, nil
	}
	<-s.release
	if s.recvDone != nil {
		close(s.recvDone)
	}
	return nil, status.Error(codes.Canceled, "stream torn down")
}

func (s *scriptedStream) Send(*relayv1.CoordinatorMessage) error { return nil }
func (s *scriptedStream) Context() context.Context               { return s.ctx }
func (s *scriptedStream) RecvMsg(any) error                      { return nil }
func (s *scriptedStream) SendMsg(any) error                      { return nil }
func (s *scriptedStream) SetHeader(metadata.MD) error            { return nil }
func (s *scriptedStream) SendHeader(metadata.MD) error           { return nil }
func (s *scriptedStream) SetTrailer(metadata.MD)                 {}

// TestConnect_SilentPeerIsDisconnectedAtTheRegistrationDeadline.
//
// THE FIRST Recv WAS UNBOUNDED, AND THAT MADE THE CONNECTION CAP A WEAPON. A
// peer that opened a stream and sent nothing sat here forever: it never
// authenticated, so it cost no credential and no database round trip, and it
// never went idle either - opening a stream zeroes grpc-go's t.idle, and
// MaxConnectionIdle only reaps a transport whose t.idle is non-zero
// (http2_server.go:582-585, :1204-1220). The keepalive liveness probe does not
// help: any frame the peer reads re-stamps t.lastRead, so that arm resets
// forever. Reproduced holding at 55s against a 200ms MaxConnectionIdle.
//
// Before the connection caps existed that was a nuisance bounded by file
// descriptors, orders of magnitude away. WITH the caps it is a cheap, permanent,
// fleet-wide denial: a handful of source prefixes x 64 parked streams fills the
// 1024 default, and every real agent is refused from then on.
//
// Returning is what hands the connection back to MaxConnectionIdle, because the
// last stream closing re-stamps t.idle.
//
// RED at HEAD: Connect never returns and this fails on its five-second bound.
func TestConnect_SilentPeerIsDisconnectedAtTheRegistrationDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &scriptedStream{ctx: ctx, release: make(chan struct{}), recvDone: make(chan struct{})}
	h := &Handler{RegistrationTimeout: 150 * time.Millisecond}

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- h.Connect(s) }()

	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Connect never returned for a peer that opened a stream and sent nothing. That peer holds " +
			"a netlimit slot forever: it never authenticates, and a stream being open is exactly what " +
			"stops MaxConnectionIdle from reaping the connection.")
	}
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err),
		"the registration deadline must be reported as DeadlineExceeded, not as a generic recv failure")
	assert.Less(t, time.Since(start), 3*time.Second, "it must return AT the deadline, not on some other timer")

	// The recv goroutine must be parked in Recv, not leaked past teardown: it is
	// released when grpc closes the stream, which is what returning causes.
	select {
	case <-s.recvDone:
		t.Fatal("the bounded Recv already returned before the stream was torn down")
	default:
	}
	close(s.release)
	select {
	case <-s.recvDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the recv goroutine did not exit once the stream was torn down: it would leak once per " +
			"parked peer, which is the resource exhaustion this fix exists to close, one level up")
	}
}

// TestConnect_RegistrationArrivingInTimeIsNotDisconnected is the other half, and
// without it the fix above could be "always reject". The credential is absent
// and AllowAutoEnroll is false, so authenticateAndRegister rejects it with
// Unauthenticated WITHOUT touching the database - which is exactly the signal
// wanted here: the message got through the bound, intact, and reached auth.
func TestConnect_RegistrationArrivingInTimeIsNotDisconnected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &scriptedStream{
		ctx:     ctx,
		release: make(chan struct{}),
		msgs: []*relayv1.AgentMessage{{Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{Hostname: "h1"}}}},
	}
	h := &Handler{RegistrationTimeout: 5 * time.Second}

	err := h.Connect(s)
	assert.Equal(t, codes.Unauthenticated, status.Code(err),
		"a RegisterRequest inside the deadline must reach authenticateAndRegister, not be cut off by it")
}

// TestConnect_RegistrationSlowerThanTheDeadlineIsCutOff pins that the bound is
// on the DELAY and not merely on "nothing was ever sent". A peer that dribbles a
// RegisterRequest out after the deadline is the same parked slot.
func TestConnect_RegistrationSlowerThanTheDeadlineIsCutOff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &scriptedStream{
		ctx:     ctx,
		release: make(chan struct{}),
		delay:   2 * time.Second,
		msgs: []*relayv1.AgentMessage{{Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{Hostname: "h1"}}}},
	}
	h := &Handler{RegistrationTimeout: 100 * time.Millisecond}

	err := h.Connect(s)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
	close(s.release)
}

// TestConnect_FirstMessageMustStillBeARegisterRequest guards the ordering: the
// deadline wraps the Recv, so the type check that used to sit immediately after
// it must not have been lost in the move.
func TestConnect_FirstMessageMustStillBeARegisterRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &scriptedStream{
		ctx:     ctx,
		release: make(chan struct{}),
		msgs: []*relayv1.AgentMessage{{Payload: &relayv1.AgentMessage_Telemetry{
			Telemetry: &relayv1.WorkerTelemetry{CpuPercent: 1}}}},
	}
	h := &Handler{RegistrationTimeout: 5 * time.Second}

	err := h.Connect(s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RegisterRequest")
}

// TestRegistrationTimeout_ZeroMeansTheDefault keeps every existing
// NewHandler/NewHandlerWithGrace call site correct with no edit, matching
// TrailingLogWindow. There is deliberately NO "disabled" value: the only
// fail-aggressive direction is a window too SHORT, whose fix is to raise it, and
// no proxy can enforce an application-layer "send RegisterRequest within N" on
// the server's behalf. An operator who genuinely wants the old behaviour writes
// a very large duration and can see in the startup line that they did.
func TestRegistrationTimeout_ZeroMeansTheDefault(t *testing.T) {
	assert.Equal(t, DefaultRegistrationTimeout, (&Handler{}).registrationTimeout())
	assert.Equal(t, DefaultRegistrationTimeout, (&Handler{RegistrationTimeout: -1}).registrationTimeout())
	assert.Equal(t, time.Second, (&Handler{RegistrationTimeout: time.Second}).registrationTimeout())
}
