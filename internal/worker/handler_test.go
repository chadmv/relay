//go:build integration

package worker_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
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
	"google.golang.org/grpc/metadata"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// seedWorkerWithAgentToken creates a worker in the DB with a known agent token.
func seedWorkerWithAgentToken(t *testing.T, ctx context.Context, q *store.Queries, hostname string) (pgtype.UUID, string) {
	t.Helper()
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: hostname, Hostname: hostname, CpuCores: 1, RamGb: 1, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)
	raw := "test-agent-token-" + hostname
	h := tokenhash.Hash(raw)
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{ID: w.ID, AgentTokenHash: &h}))
	return w.ID, raw
}

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
	q, pool := newTestStore(t)
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	triggered := make(chan struct{}, 1)
	h := worker.NewHandler(q, pool, registry, broker, func() {
		select {
		case triggered <- struct{}{}:
		default:
		}
	})

	_, rawToken := seedWorkerWithAgentToken(t, context.Background(), q, "render-01")

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
					Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawToken},
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
	q, pool := newTestStore(t)
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

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
		Name:           "epoch-gate-job",
		Priority:       "normal",
		SubmittedBy:    user.ID,
		Labels:         []byte("{}"),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	// Create a task.
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID:    job.ID,
		Name:     "epoch-gate-task",
		Commands: []byte(`[["echo","hi"]]`),
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

func TestRegisterWorker_ReconcilesRunningTasks(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	broker := events.NewBroker()
	registry := worker.NewRegistry()
	grace := worker.NewGraceRegistry(1*time.Minute, func(string) {})
	h := worker.NewHandlerWithGrace(q, pool, registry, broker, func() {}, grace)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "recon@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	workerRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "recon", Hostname: "recon-host", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	rawRecon := "test-token-recon"
	h2 := tokenhash.Hash(rawRecon)
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: workerRow.ID, AgentTokenHash: &h2,
	}))

	tMatch, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "match", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	tMatchClaimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: tMatch.ID, WorkerID: pgtype.UUID{Bytes: workerRow.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	tStale, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "stale", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: tStale.ID, WorkerID: pgtype.UUID{Bytes: workerRow.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	tServerOnly, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "server-only", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: tServerOnly.ID, WorkerID: pgtype.UUID{Bytes: workerRow.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	// Helper to format UUID as string (same as uuidStr in worker package)
	fmtUUID := func(u pgtype.UUID) string {
		b := u.Bytes
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	}
	matchIDStr := fmtUUID(tMatchClaimed.ID)
	staleIDStr := fmtUUID(tStale.ID)

	stream := &fakeStream{
		ctx: ctx,
		msgs: []*relayv1.AgentMessage{{
			Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname: "recon-host",
					CpuCores: 1, RamGb: 1, Os: "linux",
					RunningTasks: []*relayv1.RunningTask{
						{TaskId: matchIDStr, Epoch: int64(tMatchClaimed.AssignmentEpoch)},
						{TaskId: staleIDStr, Epoch: 999}, // stale epoch
					},
					Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawRecon},
				},
			},
		}},
		sentCh: make(chan struct{}, 1),
		hold:   make(chan struct{}),
	}

	done := make(chan error, 1)
	go func() { done <- h.Connect(stream) }()

	select {
	case <-stream.sentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("RegisterResponse never sent")
	}

	close(stream.hold)
	<-done

	// RegisterResponse.cancel_task_ids must contain only tStale (stale epoch).
	require.Len(t, stream.sent, 1)
	resp := stream.sent[0].GetRegisterResponse()
	require.NotNil(t, resp)
	assert.ElementsMatch(t, []string{staleIDStr}, resp.CancelTaskIds)

	// tServerOnly was not reported by agent → must be requeued.
	fetchedServerOnly, err := q.GetTask(ctx, tServerOnly.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", fetchedServerOnly.Status)

	// tMatch reported with correct epoch → still dispatched.
	fetchedMatch, err := q.GetTask(ctx, tMatch.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", fetchedMatch.Status)

	// tStale reported with wrong epoch → server issued cancel but did not requeue yet.
	fetchedStale, err := q.GetTask(ctx, tStale.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", fetchedStale.Status)
}

func TestConnect_DisconnectStartsGraceTimer(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	broker := events.NewBroker()
	registry := worker.NewRegistry()

	var fired atomic.Int32
	grace := worker.NewGraceRegistry(50*time.Millisecond, func(workerID string) {
		fired.Add(1)
	})
	defer grace.Stop()

	h := worker.NewHandlerWithGrace(q, pool, registry, broker, func() {}, grace)

	_, rawToken := seedWorkerWithAgentToken(t, ctx, q, "grace-host")

	stream := &fakeStream{
		ctx: ctx,
		msgs: []*relayv1.AgentMessage{{
			Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname: "grace-host", CpuCores: 1, RamGb: 1, Os: "linux",
					Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawToken},
				},
			},
		}},
		sentCh: make(chan struct{}, 1),
	}

	done := make(chan error, 1)
	go func() { done <- h.Connect(stream) }()

	select {
	case <-stream.sentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("RegisterResponse never sent")
	}
	<-done

	// Before the grace window elapses, no requeue.
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(0), fired.Load())

	// After the window elapses, the timer fires once for this worker.
	require.Eventually(t, func() bool {
		return fired.Load() == 1
	}, 500*time.Millisecond, 5*time.Millisecond, "grace timer should fire once")
}

func TestHandler_RegisterReplacesWorkerInventory(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	// Create a worker directly
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "h", Hostname: "h", CpuCores: 1, RamGb: 1, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	// Pre-seed stale workspace entry
	require.NoError(t, q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID: w.ID, SourceType: "perforce", SourceKey: "//old", ShortID: "old",
		BaselineHash: "x", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}))

	// Apply a full replacement inventory
	inv := []*relayv1.WorkspaceInventoryUpdate{
		{SourceType: "perforce", SourceKey: "//new", ShortId: "n",
			BaselineHash: "y", LastUsedAt: time.Now().UTC().Format(time.RFC3339)},
	}
	require.NoError(t, h.ApplyInventory(ctx, w.ID, inv))

	rows, err := q.ListWorkerWorkspaces(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "//new", rows[0].SourceKey)
}

func TestHandler_WorkspaceInventoryUpdate_Apply(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "h2", Hostname: "h2", CpuCores: 1, RamGb: 1, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	upd := &relayv1.WorkspaceInventoryUpdate{
		SourceType: "perforce", SourceKey: "//s/x", ShortId: "abc",
		BaselineHash: "xyz", LastUsedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, h.ApplyInventoryUpdate(ctx, w.ID, upd))
	rows, err := q.ListWorkerWorkspaces(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)

	upd.Deleted = true
	require.NoError(t, h.ApplyInventoryUpdate(ctx, w.ID, upd))
	rows, err = q.ListWorkerWorkspaces(ctx, w.ID)
	require.NoError(t, err)
	require.Empty(t, rows)
}

// TestConnect_RegisterRequest_PersistsInventory verifies the full Connect path:
// inventory in RegisterRequest is applied to the DB during registration.
func TestConnect_RegisterRequest_PersistsInventory(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	broker := events.NewBroker()
	registry := worker.NewRegistry()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	workerID, rawToken := seedWorkerWithAgentToken(t, ctx, q, "inv-host")

	// Pre-seed a stale entry that the replacement should remove.
	require.NoError(t, q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID: workerID, SourceType: "perforce", SourceKey: "//stale/...", ShortID: "stale",
		BaselineHash: "old", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}))

	stream := &fakeStream{
		ctx:    ctx,
		sentCh: make(chan struct{}, 1),
		hold:   make(chan struct{}),
		msgs: []*relayv1.AgentMessage{{
			Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname: "inv-host", CpuCores: 1, RamGb: 1, Os: "linux",
					Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawToken},
					Inventory: []*relayv1.WorkspaceInventoryUpdate{
						{SourceType: "perforce", SourceKey: "//s/main", ShortId: "abc",
							BaselineHash: "deadbeef",
							LastUsedAt:   time.Now().UTC().Format(time.RFC3339)},
					},
				},
			},
		}},
	}

	done := make(chan error, 1)
	go func() { done <- h.Connect(stream) }()

	select {
	case <-stream.sentCh:
	case <-time.After(5 * time.Second):
		t.Fatal("RegisterResponse never sent")
	}

	close(stream.hold)
	require.NoError(t, <-done)

	rows, err := q.ListWorkerWorkspaces(ctx, workerID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "stale entry should be replaced by inventory from RegisterRequest")
	require.Equal(t, "//s/main", rows[0].SourceKey)
	require.Equal(t, "deadbeef", rows[0].BaselineHash)
}

// TestConnect_WorkspaceInventoryUpdate_Message verifies that a WorkspaceInventoryUpdate
// message sent after registration is persisted to the DB.
func TestConnect_WorkspaceInventoryUpdate_Message(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	broker := events.NewBroker()
	registry := worker.NewRegistry()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	workerID, rawToken := seedWorkerWithAgentToken(t, ctx, q, "inv-msg-host")

	stream := &fakeStream{
		ctx:    ctx,
		sentCh: make(chan struct{}, 1),
		msgs: []*relayv1.AgentMessage{
			{Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname: "inv-msg-host", CpuCores: 1, RamGb: 1, Os: "linux",
					Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawToken},
				},
			}},
			{Payload: &relayv1.AgentMessage_WorkspaceInventory{
				WorkspaceInventory: &relayv1.WorkspaceInventoryUpdate{
					SourceType: "perforce", SourceKey: "//s/x", ShortId: "xyz",
					BaselineHash: "abc123", LastUsedAt: time.Now().UTC().Format(time.RFC3339),
				},
			}},
		},
	}

	require.NoError(t, h.Connect(stream))

	rows, err := q.ListWorkerWorkspaces(ctx, workerID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "//s/x", rows[0].SourceKey)
	require.Equal(t, "abc123", rows[0].BaselineHash)
}

func TestHandler_DisconnectAndRegisterTrackDisconnectedAt(t *testing.T) {
	ctx := context.Background()
	q, _ := newTestStore(t)

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w-dc", Hostname: "host-dc", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err = q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:             w.ID,
		Status:         "offline",
		LastSeenAt:     pgtype.Timestamptz{Time: now, Valid: true},
		DisconnectedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	require.NoError(t, err)

	fetched, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.True(t, fetched.DisconnectedAt.Valid, "disconnected_at must be set after offline transition")

	_, err = q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:             w.ID,
		Status:         "online",
		LastSeenAt:     pgtype.Timestamptz{Time: now, Valid: true},
		DisconnectedAt: pgtype.Timestamptz{},
	})
	require.NoError(t, err)

	fetched, err = q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.False(t, fetched.DisconnectedAt.Valid, "disconnected_at must be NULL after register")
}
