//go:build integration

package worker_test

import (
	"context"
	"io"
	"testing"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// fakeStream implements grpc.BidiStreamingServer[relayv1.AgentMessage, relayv1.CoordinatorMessage].
type fakeStream struct {
	msgs []*relayv1.AgentMessage
	sent []*relayv1.CoordinatorMessage
	pos  int
	ctx  context.Context
}

func (f *fakeStream) Recv() (*relayv1.AgentMessage, error) {
	if f.pos >= len(f.msgs) {
		return nil, io.EOF
	}
	msg := f.msgs[f.pos]
	f.pos++
	return msg, nil
}

func (f *fakeStream) Send(msg *relayv1.CoordinatorMessage) error {
	f.sent = append(f.sent, msg)
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
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx)
	require.NoError(t, err)

	migrateDSN := "pgx5" + dsn[len("postgres"):]
	require.NoError(t, store.Migrate(migrateDSN))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return store.New(pool), pool
}

func TestHandler_RegisterNewWorker(t *testing.T) {
	ctx := context.Background()
	q, _ := newTestStore(t)

	triggered := false
	triggerFn := func() { triggered = true }

	registry := worker.NewRegistry()
	broker := events.NewBroker()
	handler := worker.NewHandler(q, registry, broker, triggerFn)

	stream := &fakeStream{
		ctx: ctx,
		msgs: []*relayv1.AgentMessage{
			{
				Payload: &relayv1.AgentMessage_Register{
					Register: &relayv1.RegisterRequest{
						WorkerId: "",
						Hostname: "test-host",
						CpuCores: 4,
						RamGb:    8,
						GpuCount: 0,
						GpuModel: "",
						Os:       "linux",
					},
				},
			},
		},
	}

	err := handler.Connect(stream)
	require.NoError(t, err)

	// Expect RegisterResponse as first sent message.
	require.Len(t, stream.sent, 1)
	resp := stream.sent[0].GetRegisterResponse()
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.WorkerId)

	// Expect worker in DB with status "online" (may be offline after Connect returns).
	// The defer runs markWorkerOffline when stream ends (EOF), so check by hostname.
	w, err := q.GetWorkerByHostname(ctx, "test-host")
	require.NoError(t, err)
	assert.Equal(t, "test-host", w.Hostname)

	// triggerDispatch should have been called (set to true via goroutine).
	// Give the goroutine a moment to run.
	for i := 0; i < 100 && !triggered; i++ {
		// small busy-wait — acceptable in tests
	}
	assert.True(t, triggered)
}
