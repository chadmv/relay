//go:build integration

package worker_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/tokenhash"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// mockConnectStream is an interactive bidirectional mock stream for auth tests.
type mockConnectStream struct {
	toServer   chan *relayv1.AgentMessage
	fromServer chan *relayv1.CoordinatorMessage
	closeOnce  sync.Once
	closed     chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
}

func newMockConnectStream(t *testing.T) *mockConnectStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	s := &mockConnectStream{
		toServer:   make(chan *relayv1.AgentMessage, 8),
		fromServer: make(chan *relayv1.CoordinatorMessage, 8),
		closed:     make(chan struct{}),
		ctx:        ctx,
		cancel:     cancel,
	}
	t.Cleanup(s.Close)
	return s
}

func (s *mockConnectStream) Recv() (*relayv1.AgentMessage, error) {
	select {
	case msg, ok := <-s.toServer:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-s.closed:
		return nil, io.EOF
	case <-s.ctx.Done():
		return nil, io.EOF
	}
}

func (s *mockConnectStream) Send(msg *relayv1.CoordinatorMessage) error {
	select {
	case s.fromServer <- msg:
		return nil
	case <-s.ctx.Done():
		return io.EOF
	}
}

func (s *mockConnectStream) Context() context.Context    { return s.ctx }
func (s *mockConnectStream) RecvMsg(m any) error         { return nil }
func (s *mockConnectStream) SendMsg(m any) error         { return nil }
func (s *mockConnectStream) SetHeader(metadata.MD) error { return nil }
func (s *mockConnectStream) SendHeader(metadata.MD) error { return nil }
func (s *mockConnectStream) SetTrailer(metadata.MD)      {}

func (s *mockConnectStream) SendToServer(msg *relayv1.AgentMessage) {
	s.toServer <- msg
}

func (s *mockConnectStream) RecvFromServer(t *testing.T, timeout time.Duration) *relayv1.CoordinatorMessage {
	t.Helper()
	select {
	case msg := <-s.fromServer:
		return msg
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for message from server")
		return nil
	}
}

func (s *mockConnectStream) CloseSend() {
	s.closeOnce.Do(func() { close(s.closed) })
}

func (s *mockConnectStream) Close() {
	s.cancel()
	s.CloseSend()
}

// workerTestFixture holds test dependencies.
type workerTestFixture struct {
	Q       *store.Queries
	Pool    *pgxpool.Pool
	Handler *worker.Handler
	AdminID pgtype.UUID
}

func (f *workerTestFixture) Cleanup() {}

func newWorkerTestFixture(t *testing.T) *workerTestFixture {
	t.Helper()
	q, pool := newTestStore(t)
	ctx := context.Background()

	admin, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "admin", Email: "admin@test.com", IsAdmin: true, PasswordHash: "x",
	})
	require.NoError(t, err)

	broker := events.NewBroker()
	registry := worker.NewRegistry()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	return &workerTestFixture{
		Q:       q,
		Pool:    pool,
		Handler: h,
		AdminID: admin.ID,
	}
}

// seedEnrollment creates an enrollment token in the DB and returns the raw token string.
func seedEnrollment(t *testing.T, ctx context.Context, q *store.Queries, adminID pgtype.UUID, ttl time.Duration) (rawToken string) {
	t.Helper()
	raw := "enroll-" + t.Name()
	h := tokenhash.Hash(raw)
	_, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: h,
		CreatedBy: adminID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
	})
	require.NoError(t, err)
	return raw
}

func TestConnect_ValidEnrollmentIssuesAgentToken(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	rawEnroll := seedEnrollment(t, ctx, fx.Q, fx.AdminID, time.Hour)

	stream := newMockConnectStream(t)
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "enroll-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawEnroll},
			},
		},
	})

	done := make(chan error, 1)
	go func() { done <- fx.Handler.Connect(stream) }()

	msg := stream.RecvFromServer(t, 5*time.Second)
	resp := msg.GetRegisterResponse()
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.WorkerId)
	assert.NotEmpty(t, resp.AgentToken)

	stream.CloseSend()
	<-done
}

func TestConnect_AgentTokenAuthSucceeds(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	rawEnroll := seedEnrollment(t, ctx, fx.Q, fx.AdminID, time.Hour)

	// First connect: enroll and get agent token.
	stream1 := newMockConnectStream(t)
	stream1.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "agent-auth-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawEnroll},
			},
		},
	})

	done1 := make(chan error, 1)
	go func() { done1 <- fx.Handler.Connect(stream1) }()

	msg1 := stream1.RecvFromServer(t, 5*time.Second)
	resp1 := msg1.GetRegisterResponse()
	require.NotNil(t, resp1)
	agentToken := resp1.AgentToken
	require.NotEmpty(t, agentToken)

	stream1.CloseSend()
	<-done1

	// Second connect: use agent token.
	stream2 := newMockConnectStream(t)
	stream2.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "agent-auth-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: agentToken},
			},
		},
	})

	done2 := make(chan error, 1)
	go func() { done2 <- fx.Handler.Connect(stream2) }()

	msg2 := stream2.RecvFromServer(t, 5*time.Second)
	resp2 := msg2.GetRegisterResponse()
	require.NotNil(t, resp2)

	stream2.CloseSend()
	<-done2
}

func TestConnect_RevokedTokenRejected(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	rawEnroll := seedEnrollment(t, ctx, fx.Q, fx.AdminID, time.Hour)

	// First connect: enroll.
	stream1 := newMockConnectStream(t)
	stream1.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "revoke-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawEnroll},
			},
		},
	})

	done1 := make(chan error, 1)
	go func() { done1 <- fx.Handler.Connect(stream1) }()

	msg1 := stream1.RecvFromServer(t, 5*time.Second)
	resp1 := msg1.GetRegisterResponse()
	require.NotNil(t, resp1)
	agentToken := resp1.AgentToken
	workerIDStr := resp1.WorkerId
	require.NotEmpty(t, agentToken)
	require.NotEmpty(t, workerIDStr)

	stream1.CloseSend()
	<-done1

	// Revoke the token.
	var wID pgtype.UUID
	require.NoError(t, wID.Scan(workerIDStr))
	_, err := fx.Q.ClearWorkerAgentToken(ctx, wID)
	require.NoError(t, err)

	// Second connect: revoked token should be rejected.
	stream2 := newMockConnectStream(t)
	stream2.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "revoke-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: agentToken},
			},
		},
	})

	done2 := make(chan error, 1)
	go func() { done2 <- fx.Handler.Connect(stream2) }()

	err2 := <-done2
	require.Error(t, err2)
	assert.Equal(t, codes.Unauthenticated, status.Code(err2))
}

func TestConnect_EnrollmentTokenSingleShot(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	rawEnroll := seedEnrollment(t, ctx, fx.Q, fx.AdminID, time.Hour)

	// First use: succeeds.
	stream1 := newMockConnectStream(t)
	stream1.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "single-shot-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawEnroll},
			},
		},
	})

	done1 := make(chan error, 1)
	go func() { done1 <- fx.Handler.Connect(stream1) }()

	msg1 := stream1.RecvFromServer(t, 5*time.Second)
	require.NotNil(t, msg1.GetRegisterResponse())
	stream1.CloseSend()
	<-done1

	// Second use: same enrollment token must be rejected.
	stream2 := newMockConnectStream(t)
	stream2.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "single-shot-host-2",
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawEnroll},
			},
		},
	})

	done2 := make(chan error, 1)
	go func() { done2 <- fx.Handler.Connect(stream2) }()

	err2 := <-done2
	require.Error(t, err2)
	assert.Equal(t, codes.Unauthenticated, status.Code(err2))
}

func TestConnect_ExpiredEnrollmentRejected(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	// Create enrollment that is already expired.
	rawEnroll := seedEnrollment(t, ctx, fx.Q, fx.AdminID, -time.Hour)

	stream := newMockConnectStream(t)
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "expired-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawEnroll},
			},
		},
	})

	done := make(chan error, 1)
	go func() { done <- fx.Handler.Connect(stream) }()

	err := <-done
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestConnect_NoCredentialRejected(t *testing.T) {
	fx := newWorkerTestFixture(t)

	stream := newMockConnectStream(t)
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "no-cred-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
				// No credential field.
			},
		},
	})

	done := make(chan error, 1)
	go func() { done <- fx.Handler.Connect(stream) }()

	err := <-done
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}
