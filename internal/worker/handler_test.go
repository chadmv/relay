//go:build integration

package worker_test

import (
	"context"
	"fmt"
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

func TestHandleTaskStatus_EpochGate(t *testing.T) {
	ctx := context.Background()
	q, _ := newTestStore(t)
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	h := worker.NewHandler(q, registry, broker, func() {})

	// Create a user to satisfy the jobs.submitted_by foreign key.
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name:         "test-user",
		Email:        "test@example.com",
		IsAdmin:      false,
		PasswordHash: "x",
	})
	require.NoError(t, err)

	// Create a job.
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name:        "epoch-gate-job",
		Priority:    "normal",
		SubmittedBy: user.ID,
		Labels:      []byte("{}"),
	})
	require.NoError(t, err)

	// Create a task.
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID:    job.ID,
		Name:     "epoch-gate-task",
		Command:  []string{"echo", "hi"},
		Env:      []byte("{}"),
		Requires: []byte("[]"),
		Retries:  0,
	})
	require.NoError(t, err)

	// Create a worker to claim the task.
	wk, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name:     "test-worker",
		Hostname: "test-worker-01",
		CpuCores: 4,
		RamGb:    8,
		GpuCount: 0,
		GpuModel: "",
		Os:       "linux",
	})
	require.NoError(t, err)

	// Claim the task — epoch transitions from 0 → 1.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID:       task.ID,
		WorkerID: wk.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	require.Equal(t, "dispatched", claimed.Status)

	taskIDStr := claimed.ID.Bytes
	taskUUID := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		taskIDStr[0:4], taskIDStr[4:6], taskIDStr[6:8], taskIDStr[8:10], taskIDStr[10:16])

	// Send a stale update (epoch=0) — should be silently dropped.
	h.HandleTaskStatus(ctx, &relayv1.TaskStatusUpdate{
		TaskId: taskUUID,
		Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		Epoch:  0,
	})

	// Task must still be "dispatched".
	afterStale, err := q.GetTask(ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", afterStale.Status, "stale epoch update must be dropped")

	// Send a valid update (epoch=1) — should be applied.
	h.HandleTaskStatus(ctx, &relayv1.TaskStatusUpdate{
		TaskId: taskUUID,
		Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		Epoch:  1,
	})

	// Task must now be "running".
	afterValid, err := q.GetTask(ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", afterValid.Status, "valid epoch update must be applied")
}
