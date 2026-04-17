//go:build integration

package worker_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// fakeStream implements grpc.BidiStreamingServer[relayv1.AgentMessage, relayv1.CoordinatorMessage].
type fakeStream struct {
	msgs   []*relayv1.AgentMessage
	sent   []*relayv1.CoordinatorMessage
	sentCh chan struct{} // signaled when first Send completes
	pos    int
	ctx    context.Context
	hold   chan struct{} // if non-nil, Recv blocks until this is closed after msgs exhausted
}

func (f *fakeStream) Recv() (*relayv1.AgentMessage, error) {
	if f.pos >= len(f.msgs) {
		if f.hold != nil {
			<-f.hold // block until test releases
		}
		return nil, io.EOF
	}
	msg := f.msgs[f.pos]
	f.pos++
	return msg, nil
}

func (f *fakeStream) Send(msg *relayv1.CoordinatorMessage) error {
	f.sent = append(f.sent, msg)
	if f.sentCh != nil {
		select {
		case f.sentCh <- struct{}{}:
		default:
		}
	}
	return nil
}

func (f *fakeStream) Context() context.Context        { return f.ctx }
func (f *fakeStream) RecvMsg(m any) error             { return io.EOF }
func (f *fakeStream) SendMsg(m any) error             { return nil }
func (f *fakeStream) SetHeader(metadata.MD) error     { return nil }
func (f *fakeStream) SendHeader(metadata.MD) error    { return nil }
func (f *fakeStream) SetTrailer(metadata.MD)          {}

func newTestStore(t *testing.T) (*store.Queries, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx,
		"postgres:16",
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

	migrateDSN := "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(dsn, "postgresql://"), "postgres://")
	require.NoError(t, store.Migrate(migrateDSN))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return store.New(pool), pool
}

func TestHandler_RegisterNewWorker(t *testing.T) {
	q, _ := newTestStore(t)
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	triggered := make(chan struct{}, 1)
	h := worker.NewHandler(q, registry, broker, func() {
		select {
		case triggered <- struct{}{}:
		default:
		}
	})

	// hold is closed by the test to let Connect proceed to io.EOF
	hold := make(chan struct{})
	stream := &fakeStream{
		ctx:    context.Background(),
		sentCh: make(chan struct{}, 1),
		hold:   hold,
		msgs: []*relayv1.AgentMessage{
			{Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname: "render-01",
					CpuCores: 16,
					RamGb:    64,
					GpuCount: 2,
					GpuModel: "RTX 4090",
					Os:       "linux",
				},
			}},
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- h.Connect(stream)
	}()

	// Wait for RegisterResponse
	select {
	case <-stream.sentCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for RegisterResponse")
	}

	// Assert RegisterResponse
	require.Len(t, stream.sent, 1)
	resp := stream.sent[0].GetRegisterResponse()
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.WorkerId)

	// Assert worker is online in DB while Connect is still running
	wk, err := q.GetWorkerByHostname(context.Background(), "render-01")
	require.NoError(t, err)
	assert.Equal(t, "online", wk.Status)

	// Assert dispatch was triggered
	select {
	case <-triggered:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for triggerDispatch")
	}

	// Let the stream end
	close(hold)

	// Wait for Connect to return
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Connect to return")
	}
}
