package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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

	// mu guards sent, and it is load-bearing rather than defensive. The
	// successful-registration tests next door run Connect on their own goroutine
	// and read this slice from the test goroutine, and the sends arrive from TWO
	// goroutines even within Connect: finishRegister writes the RegisterResponse
	// directly, and every send after that goes through the workerSender's send
	// loop. CI runs `go test -race ./...`, which reports an unguarded slice as a
	// failure rather than as a flake.
	mu              sync.Mutex
	sent            []*relayv1.CoordinatorMessage
	agentTokensSent int

	// sendErr, when set, is what Send returns after recording. A peer that
	// vanished between RegisterWorkerConnection and the RegisterResponse looks
	// exactly like this from the server's side, and it is the second of the two
	// arms a failed registration can take.
	sendErr error
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

// Send records what it was asked to deliver, MINUS the raw agent token. It used
// to discard its argument entirely, which is what made "the RegisterResponse was
// actually sent" unobservable in this lane - the message never left the stream
// fake, so no default-lane test could tell a sent response from a deleted send.
//
// THE CREDENTIAL IS SCRUBBED AT THE POINT OF RETENTION, not on the way back out,
// and the difference is the whole point: a projection that cleans one field in
// sentMsgs() still leaves the secret sitting in the slice behind it, where a
// panic dump or a future accessor reaches it. Only agentTokensSent survives -
// "a token was issued" is the assertable property, and the value itself is not
// one any test in this package needs.
//
// THE SCRUB NOW FIRES, AND THIS PARAGRAPH USED TO PREDICT IT RATHER THAN RECORD
// IT. It said "the next test written against this fixture is the one that would
// have leaked", because finishRegister's reconnect caller passes rawAgentToken
// "" and, at the time, every user of this fake drove that arm. Both enrollment
// callers pass a real minted token, and handler_enroll_guards_test.go now drives
// both without Postgres - so the redaction branch has real consumers.
//
// TestConnect_AutoEnrollStillCreatesAWorkerForAFreshHostname is the one to read:
// it asserts tokensSent() == 1 AND that the retained message carries the
// placeholder. The first half is what stops the test passing against a build that
// never minted a token; the second is what distinguishes "scrubbed" from "never
// sent". TestScriptedStream_DoesNotRetainARawAgentToken keeps the absence half
// from being silent.
//
// proto.Clone rather than a hand-written field copy: a field-by-field rebuild of
// RegisterResponse would silently drop anything added to the message later, and
// a fixture that quietly stops recording a new field makes every test that reads
// it prove less.
//
// THE SCOPE IS RELAY-ISSUED CREDENTIALS, AND THAT BOUNDARY WAS CHOSEN RATHER
// THAN MISSED. RegisterResponse.AgentToken is the only field on any
// CoordinatorMessage that relay MINTS and hands to a peer, so it is the only one
// whose retention here would leak something the coordinator created. The nearest
// other candidate is DispatchTask.env (relay.proto), a caller-supplied string map
// that reaches this fake whenever a dispatch goes through NewWorkerSender: it is
// retained unscrubbed, deliberately. It is user-authored job-spec input rather
// than a relay credential, no test drives one through this fixture today, and
// scrubbing it would cost a future dispatch test the ability to assert what the
// agent was actually told to run. Revisit if relay ever puts a secret of its own
// into a dispatch.
func (s *scriptedStream) Send(m *relayv1.CoordinatorMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rr := m.GetRegisterResponse(); rr != nil && rr.AgentToken != "" {
		s.agentTokensSent++
		redacted := proto.Clone(m).(*relayv1.CoordinatorMessage)
		redacted.GetRegisterResponse().AgentToken = "[redacted by scriptedStream]"
		m = redacted
	}
	s.sent = append(s.sent, m)
	return s.sendErr
}

// tokensSent reports how many RegisterResponses carried a non-empty raw agent
// token.
//
// IT EXISTS BECAUSE THE FIELD WITHOUT IT HAS ONLY A RACY SPELLING. sent got
// sentMsgs() and agentTokensSent got nothing, so the only way to read it was as
// a bare field - and this fixture's stated next consumer, a test pointing it at
// enrollAndRegister or autoEnrollAndRegister, is by construction a
// Connect-on-a-goroutine test where that read is a real -race failure. The
// sibling field in this struct already had the guard; adding a property and
// forgetting its guard is the recurring shape this is avoiding.
func (s *scriptedStream) tokensSent() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentTokensSent
}

// sentMsgs returns a copy of what has been sent so far, so callers never read
// the slice the send goroutine is appending to.
func (s *scriptedStream) sentMsgs() []*relayv1.CoordinatorMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*relayv1.CoordinatorMessage, len(s.sent))
	copy(out, s.sent)
	return out
}

func (s *scriptedStream) Context() context.Context     { return s.ctx }
func (s *scriptedStream) RecvMsg(any) error            { return nil }
func (s *scriptedStream) SendMsg(any) error            { return nil }
func (s *scriptedStream) SetHeader(metadata.MD) error  { return nil }
func (s *scriptedStream) SendHeader(metadata.MD) error { return nil }
func (s *scriptedStream) SetTrailer(metadata.MD)       {}

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

// TestScriptedStream_DoesNotRetainARawAgentToken pins a property of the FIXTURE,
// not of the handler, and it is here because this fake started recording what it
// was sent and the tests read it back through testify assertions that render the
// whole message on failure.
//
// IT IS SAFE TODAY ONLY BY ACCIDENT OF WHICH ARM IS DRIVEN. finishRegister's
// reconnect caller passes rawAgentToken "" (handler.go:552), so nothing sensitive
// has ever reached this slice. Both enrollment callers pass a real one
// (handler.go:534, :618), and making those two drivable in the default lane is
// this seam's stated next consumer - so the first test that points this fixture
// at enrollAndRegister or autoEnrollAndRegister would put a live credential into
// retained memory and into every CI failure dump, with nothing here to notice.
//
// The assertion is against the RETAINED slice rather than against sentMsgs()'s
// return, because a projection that cleans one field on the way out still leaves
// the secret in the store behind it.
func TestScriptedStream_DoesNotRetainARawAgentToken(t *testing.T) {
	const secret = "b8f1e2c3d4a5960718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f"

	s := &scriptedStream{ctx: context.Background(), release: make(chan struct{})}
	require.NoError(t, s.Send(&relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_RegisterResponse{
			RegisterResponse: &relayv1.RegisterResponse{
				WorkerId:      "5a010203-0405-0607-0809-0a0b0c0d0e0f",
				CancelTaskIds: []string{"t1"},
				AgentToken:    secret,
			},
		},
	}))

	// The RAW retained slice, under the lock rather than through sentMsgs(): what
	// this test exists to check is what the fixture KEEPS, and a copy taken by an
	// accessor could in principle be cleaner than the store behind it.
	s.mu.Lock()
	for i, m := range s.sent {
		assert.NotContains(t, m.String(), secret,
			"retained message %d still carries the raw agent token. This slice is read back by "+
				"assertions that print the whole message when they fail, so a real enrollment test "+
				"would publish a live credential into CI logs.", i)
	}
	s.mu.Unlock()
	for i, m := range s.sentMsgs() {
		assert.NotContains(t, m.String(), secret,
			"message %d handed to a test still carries the raw agent token", i)
	}

	require.Equal(t, 1, s.tokensSent(),
		"the fact that a token WAS issued must survive the redaction - that is the assertable "+
			"property an enrollment test needs, and the only thing about the credential worth keeping")

	sent := s.sentMsgs()
	require.Len(t, sent, 1)
	rr := sent[0].GetRegisterResponse()
	require.NotNil(t, rr)
	assert.Equal(t, "5a010203-0405-0607-0809-0a0b0c0d0e0f", rr.WorkerId,
		"redaction must cost no assertion power: every other field survives intact")
	assert.Equal(t, []string{"t1"}, rr.CancelTaskIds)
}
