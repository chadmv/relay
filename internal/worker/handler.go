package worker

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/events"
	"relay/internal/metrics"
	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// agentTokenGenerator generates a fresh (rawHex, hash) pair for a new agent token.
// Overridable in tests via SetAgentTokenGeneratorForTest.
var agentTokenGenerator = func() (string, string) {
	rawBytes := make([]byte, 32)
	if _, err := cryptorand.Read(rawBytes); err != nil {
		return "", ""
	}
	rawHex := hex.EncodeToString(rawBytes)
	return rawHex, tokenhash.Hash(rawHex)
}

// errEnrollmentNotConsumable is returned inside the enrollment transaction when
// ConsumeAgentEnrollment returns rows == 0 (already consumed or concurrent race).
var errEnrollmentNotConsumable = errors.New("enrollment not consumable")

// errWorkerRevoked is returned inside the auto-enroll transaction when the
// existing worker row for this hostname has status 'revoked'.
var errWorkerRevoked = errors.New("worker revoked")

// remoteAddr returns the gRPC peer address for logging, or "unknown".
func remoteAddr(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return "unknown"
}

// Handler implements relayv1.AgentServiceServer.
type Handler struct {
	relayv1.UnimplementedAgentServiceServer
	q               *store.Queries
	pool            *pgxpool.Pool
	registry        *Registry
	broker          *events.Broker
	triggerDispatch func()
	grace           *GraceRegistry

	// Metrics, when non-nil, receives worker utilization samples and tracks
	// per-worker liveness. Set by cmd/relay-server after construction.
	Metrics *metrics.Store

	// AllowAutoEnroll, when true, permits agents to register with no credential
	// (token-less auto-enrollment). Set by cmd/relay-server after construction.
	AllowAutoEnroll bool
}

// NewHandler returns a Handler wired to the given dependencies.
func NewHandler(q *store.Queries, pool *pgxpool.Pool, r *Registry, b *events.Broker, triggerDispatch func()) *Handler {
	return &Handler{q: q, pool: pool, registry: r, broker: b, triggerDispatch: triggerDispatch}
}

// NewHandlerWithGrace is like NewHandler but also wires in a GraceRegistry so
// that agent disconnects start a grace timer instead of immediately requeueing.
func NewHandlerWithGrace(q *store.Queries, pool *pgxpool.Pool, r *Registry, b *events.Broker, triggerDispatch func(), g *GraceRegistry) *Handler {
	return &Handler{q: q, pool: pool, registry: r, broker: b, triggerDispatch: triggerDispatch, grace: g}
}

// Connect implements relayv1.AgentServiceServer.
func (h *Handler) Connect(stream relayv1.AgentService_ConnectServer) error {
	ctx := stream.Context()

	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("recv first message: %w", err)
	}
	reg := first.GetRegister()
	if reg == nil {
		return fmt.Errorf("first message must be RegisterRequest")
	}

	workerID, sender, err := h.authenticateAndRegister(ctx, stream, reg)
	if err != nil {
		return err
	}

	// Registered above, so the teardown defer must be armed BEFORE any path that
	// can return early below, or a failed connection leaves its sender in the
	// registry (identity-checked teardown).
	defer h.teardownConnection(workerID, sender)

	// This UUID is the identity every task_log write from this connection is
	// fenced on, so a failure to parse it is not survivable: it would silently
	// drop 100% of this worker's log output. workerID came from uuidStr() over a
	// UUID the server just read out of Postgres, so a failure here means
	// something is badly wrong. Fail loudly and close the stream instead.
	var workerUUID pgtype.UUID
	if err := workerUUID.Scan(workerID); err != nil {
		log.Printf("worker: connection rejected, worker id %q is not a usable UUID: %v", workerID, err)
		return status.Errorf(codes.Internal, "worker identity unusable")
	}

	// Message loop.
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch p := msg.Payload.(type) {
		case *relayv1.AgentMessage_TaskStatus:
			h.handleTaskStatus(ctx, p.TaskStatus)
		case *relayv1.AgentMessage_TaskLog:
			h.handleTaskLog(ctx, workerUUID, p.TaskLog)
		case *relayv1.AgentMessage_WorkspaceInventory:
			if err := h.applyInventoryUpdate(ctx, workerUUID, p.WorkspaceInventory); err != nil {
				log.Printf("worker: inventory update failed: %v", err)
			}
		case *relayv1.AgentMessage_Telemetry:
			h.handleTelemetry(workerID, p.Telemetry)
		}
	}
}

// authenticateAndRegister dispatches to the appropriate auth path based on the credential type.
func (h *Handler) authenticateAndRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest) (string, *workerSender, error) {
	switch cred := reg.Credential.(type) {
	case *relayv1.RegisterRequest_EnrollmentToken:
		return h.enrollAndRegister(ctx, stream, reg, cred.EnrollmentToken)
	case *relayv1.RegisterRequest_AgentToken:
		return h.reconnectAndRegister(ctx, stream, reg, cred.AgentToken)
	default:
		if h.AllowAutoEnroll {
			return h.autoEnrollAndRegister(ctx, stream, reg)
		}
		return "", nil, status.Errorf(codes.Unauthenticated, "auto-enroll disabled")
	}
}

// enrollAndRegister handles first-time enrollment using an enrollment token.
// All DB writes (worker upsert, enrollment consume, agent-token set) execute
// inside a single transaction so that a failure anywhere leaves no partial
// state — either the agent is fully enrolled or not at all.
func (h *Handler) enrollAndRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest, rawEnroll string) (string, *workerSender, error) {
	if rawEnroll == "" {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}

	hash := tokenhash.Hash(rawEnroll)
	enroll, err := h.q.GetAgentEnrollmentByTokenHash(ctx, hash)
	if err != nil {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}
	if enroll.ConsumedAt.Valid {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}
	if time.Now().After(enroll.ExpiresAt.Time) {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}

	rawAgent, agentHash := agentTokenGenerator()
	if rawAgent == "" || agentHash == "" {
		return "", nil, status.Errorf(codes.Internal, "token gen failed")
	}

	var workerID pgtype.UUID
	txErr := pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := h.q.WithTx(tx)

		w, err := txq.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
			Name:               reg.Hostname,
			Hostname:           reg.Hostname,
			CpuCores:           reg.CpuCores,
			RamGb:              reg.RamGb,
			GpuCount:           reg.GpuCount,
			GpuModel:           reg.GpuModel,
			Os:                 reg.Os,
			SupportsWorkspaces: reg.SupportsWorkspaces,
		})
		if err != nil {
			return fmt.Errorf("upsert worker: %w", err)
		}
		workerID = w.ID

		rows, err := txq.ConsumeAgentEnrollment(ctx, store.ConsumeAgentEnrollmentParams{
			ID:         enroll.ID,
			ConsumedBy: w.ID,
		})
		if err != nil {
			return fmt.Errorf("consume enrollment: %w", err)
		}
		if rows == 0 {
			return errEnrollmentNotConsumable
		}

		if err := txq.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
			ID: w.ID, AgentTokenHash: &agentHash,
		}); err != nil {
			return fmt.Errorf("set agent token: %w", err)
		}
		return nil
	})

	if txErr != nil {
		if errors.Is(txErr, errEnrollmentNotConsumable) {
			return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
		}
		return "", nil, txErr
	}

	return h.finishRegister(ctx, stream, reg, workerID, rawAgent)
}

// reconnectAndRegister handles agent reconnection using a previously issued agent token.
func (h *Handler) reconnectAndRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest, rawAgent string) (string, *workerSender, error) {
	if rawAgent == "" {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}
	hash := tokenhash.Hash(rawAgent)

	w, err := h.q.GetWorkerByAgentTokenHash(ctx, &hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
		}
		return "", nil, status.Errorf(codes.Internal, "token lookup failed")
	}

	return h.finishRegister(ctx, stream, reg, w.ID, "")
}

// autoEnrollAndRegister handles token-less enrollment when AllowAutoEnroll is
// set. It upserts the worker by hostname and issues a fresh agent token without
// consuming any enrollment record.
func (h *Handler) autoEnrollAndRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest) (string, *workerSender, error) {
	rawAgent, agentHash := agentTokenGenerator()
	if rawAgent == "" || agentHash == "" {
		return "", nil, status.Errorf(codes.Internal, "token gen failed")
	}

	var workerID pgtype.UUID
	txErr := pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := h.q.WithTx(tx)

		existing, err := txq.GetWorkerByHostnameForUpdate(ctx, reg.Hostname)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lookup worker: %w", err)
		}
		if err == nil && existing.Status == "revoked" {
			return errWorkerRevoked
		}

		w, err := txq.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
			Name:               reg.Hostname,
			Hostname:           reg.Hostname,
			CpuCores:           reg.CpuCores,
			RamGb:              reg.RamGb,
			GpuCount:           reg.GpuCount,
			GpuModel:           reg.GpuModel,
			Os:                 reg.Os,
			SupportsWorkspaces: reg.SupportsWorkspaces,
		})
		if err != nil {
			return fmt.Errorf("upsert worker: %w", err)
		}
		workerID = w.ID

		if err := txq.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
			ID: w.ID, AgentTokenHash: &agentHash,
		}); err != nil {
			return fmt.Errorf("set agent token: %w", err)
		}
		return nil
	})
	if txErr != nil {
		if errors.Is(txErr, errWorkerRevoked) {
			return "", nil, status.Errorf(codes.Unauthenticated, "worker revoked")
		}
		return "", nil, txErr
	}

	log.Printf("worker: auto-enrolled worker %s (hostname=%s) from %s", uuidStr(workerID), reg.Hostname, remoteAddr(ctx))
	return h.finishRegister(ctx, stream, reg, workerID, rawAgent)
}

// finishRegister updates worker status, reconciles running tasks, sends RegisterResponse,
// registers the sender, and triggers dispatch.
func (h *Handler) finishRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest, id pgtype.UUID, rawAgentToken string) (string, *workerSender, error) {
	updated, err := h.q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID:                 id,
		LastSeenAt:         pgtype.Timestamptz{Time: time.Now(), Valid: true},
		SupportsWorkspaces: reg.SupportsWorkspaces,
	})
	if err != nil {
		return "", nil, fmt.Errorf("register worker connection: %w", err)
	}

	workerID := uuidStr(updated.ID)

	// Agent reconnected within its grace window — stop the requeue timer.
	if h.grace != nil {
		h.grace.Cancel(workerID)
	}

	// Reconcile the agent's running-task report against DB state.
	cancelIDs, err := h.reconcileRunningTasks(ctx, updated.ID, reg.RunningTasks)
	if err != nil {
		return "", nil, fmt.Errorf("reconcile: %w", err)
	}

	// Replace workspace inventory with what the agent reported at reconnect.
	if err := h.applyInventory(ctx, updated.ID, reg.Inventory); err != nil {
		log.Printf("worker: register inventory replace failed for %s: %v", uuidStr(updated.ID), err)
	}

	// Send RegisterResponse on the raw stream. At this point the worker is not
	// yet in the registry, so no other goroutine can race us on stream.Send.
	if err := stream.Send(&relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_RegisterResponse{
			RegisterResponse: &relayv1.RegisterResponse{
				WorkerId:      workerID,
				CancelTaskIds: cancelIDs,
				AgentToken:    rawAgentToken,
			},
		},
	}); err != nil {
		return "", nil, fmt.Errorf("send register response: %w", err)
	}

	// From here on, all sends go through the serializing wrapper.
	sender := NewWorkerSender(stream)
	sender.connEpoch = updated.ConnectionEpoch
	h.registry.Register(workerID, sender)

	if h.Metrics != nil {
		h.Metrics.Activate(workerID, time.Now())
	}

	h.broker.Publish(events.Event{
		Type: "worker",
		Data: []byte(fmt.Sprintf(`{"id":%q,"status":"online"}`, workerID)),
	})

	go h.triggerDispatch()

	return workerID, sender, nil
}

// reconcileRunningTasks compares the agent's reported running tasks against
// the coordinator's DB state. Returns the list of task IDs the agent should
// cancel (stale epoch or unknown to coordinator). Any task the coordinator
// has assigned to this worker but the agent didn't report is requeued.
func (h *Handler) reconcileRunningTasks(ctx context.Context, workerID pgtype.UUID, reported []*relayv1.RunningTask) ([]string, error) {
	serverTasks, err := h.q.GetActiveTasksForWorker(ctx, workerID)
	if err != nil {
		return nil, err
	}

	serverSet := make(map[string]int64, len(serverTasks))
	for _, t := range serverTasks {
		serverSet[uuidStr(t.ID)] = int64(t.AssignmentEpoch)
	}

	var cancelIDs []string
	agentSet := make(map[string]bool, len(reported))
	for _, rt := range reported {
		agentSet[rt.TaskId] = true
		srvEpoch, ok := serverSet[rt.TaskId]
		if !ok || srvEpoch != rt.Epoch {
			cancelIDs = append(cancelIDs, rt.TaskId)
		}
	}

	// Anything server has but agent didn't report → requeue.
	requeued := 0
	for taskIDStr := range serverSet {
		if agentSet[taskIDStr] {
			continue
		}
		var tID pgtype.UUID
		if err := tID.Scan(taskIDStr); err != nil {
			continue
		}
		_ = h.q.RequeueTaskByID(ctx, tID)
		requeued++
	}

	// Wake the scheduler so requeued tasks are dispatched immediately.
	if requeued > 0 {
		go h.triggerDispatch()
	}

	return cancelIDs, nil
}

// handleTaskStatus processes a TaskStatusUpdate from an agent.
func (h *Handler) handleTaskStatus(ctx context.Context, upd *relayv1.TaskStatusUpdate) {
	var taskID pgtype.UUID
	if err := taskID.Scan(upd.TaskId); err != nil {
		log.Printf("worker: handleTaskStatus bad task id %q: %v", upd.TaskId, err)
		return
	}

	task, err := h.q.GetTask(ctx, taskID)
	if err != nil {
		log.Printf("worker: handleTaskStatus GetTask %s: %v", upd.TaskId, err)
		return
	}

	// Epoch gate: reject any status update whose epoch doesn't match the
	// current assignment. Retry logic below must not run on stale updates.
	if int64(task.AssignmentEpoch) != upd.Epoch {
		return
	}

	// Map proto enum to string status.
	var statusStr string
	switch upd.Status {
	case relayv1.TaskStatus_TASK_STATUS_RUNNING:
		statusStr = "running"
	case relayv1.TaskStatus_TASK_STATUS_DONE:
		statusStr = "done"
	case relayv1.TaskStatus_TASK_STATUS_FAILED:
		statusStr = "failed"
	case relayv1.TaskStatus_TASK_STATUS_TIMED_OUT:
		statusStr = "timed_out"
	case relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED:
		// A prepare failure (sync failed, or the worker has no workspace
		// provider for a source-bearing task) is a terminal failure: route it
		// through the existing "failed" path so retry, dependent-cascade, and
		// slot release all apply.
		statusStr = "failed"
	default:
		return
	}

	terminal := statusStr == "failed" || statusStr == "timed_out"

	// Retry if applicable. Epoch guard above ensures we don't double-retry.
	if terminal && task.RetryCount < task.Retries {
		if _, err := h.q.IncrementTaskRetryCount(ctx, taskID); err != nil {
			log.Printf("worker: handleTaskStatus IncrementTaskRetryCount %s: %v", upd.TaskId, err)
		} else {
			updateJobStatusFromTasks(ctx, h.q, task.JobID)
			_ = h.q.NotifyTaskSubmitted(ctx)
		}
		return
	}

	// Determine timestamps.
	startedAt := task.StartedAt
	if statusStr == "running" {
		startedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	var finishedAt pgtype.Timestamptz
	if terminal || statusStr == "done" {
		finishedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}

	updated, err := h.q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID:              taskID,
		Status:          statusStr,
		WorkerID:        task.WorkerID,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		AssignmentEpoch: int32(upd.Epoch),
	})
	if err != nil {
		log.Printf("worker: handleTaskStatus UpdateTaskStatus %s -> %s: %v", upd.TaskId, statusStr, err)
		return
	}

	if terminal {
		if err := h.q.FailDependentTasks(ctx, taskID); err != nil {
			log.Printf("worker: handleTaskStatus FailDependentTasks %s: %v", upd.TaskId, err)
		}
	}

	jobStatus := updateJobStatusFromTasks(ctx, h.q, updated.JobID)

	h.broker.Publish(events.Event{
		Type:  "task",
		JobID: uuidStr(updated.JobID),
		Data:  []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, uuidStr(taskID), statusStr)),
	})

	if jobStatus == "done" || jobStatus == "failed" {
		h.broker.Publish(events.Event{
			Type:  "job",
			JobID: uuidStr(updated.JobID),
			Data:  []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, uuidStr(updated.JobID), jobStatus)),
		})
	}

	// Any terminal status or task completion frees a worker slot — wake dispatcher.
	if terminal || statusStr == "done" {
		_ = h.q.NotifyTaskCompleted(ctx)
	}
}

// taskLogEvent is the JSON payload of an events.TypeTaskLog SSE frame. seq,
// stream, content and created_at are field-identical to the polling endpoint's
// logEntry (internal/api/tasks.go) so a consumer can merge live frames with
// GET /v1/tasks/{id}/logs pages using one type. task_id and job_id are added so
// a "?task_id="-only subscriber can route and cache-key by job without a second
// request. seq is task_logs.id, so it is a total order per task and an exact
// dedupe key against the backfill.
type taskLogEvent struct {
	TaskID    string    `json:"task_id"`
	JobID     string    `json:"job_id"`
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// taskLogPublishes counts chunks that got past the HasLogSubscriber fast path,
// i.e. that are about to be marshalled and published. Test-only observability (read via
// TaskLogPublishesForTest in export_test.go) for the "nothing is marshalled when
// nobody is tailing" guarantee, which is otherwise unobservable from outside.
// Production code never reads it.
var taskLogPublishes atomic.Int64

// taskLogErrLimiterMax caps how many task ids the persist-failure limiter
// retains. Only tasks whose chunks actually failed ever land here, but a
// long-lived server must not accumulate them without bound, so the whole set is
// dropped on overflow - the worst case is one extra log line per task that is
// still failing after the reset.
const taskLogErrLimiterMax = 1024

// taskLogErrs bounds handleTaskLog's persist-failure logging to one line per
// task per assignment epoch.
//
// The realistic non-stale failure repeats for every chunk of a task rather than
// once: a subprocess writing binary stdout makes Postgres reject each insert with
// 'invalid byte sequence for encoding "UTF8": 0x00'. Because handleTaskLog runs
// synchronously on the Connect recv goroutine, logging per chunk would put tens
// of thousands of serialized log writes in front of that worker's status,
// inventory and telemetry ingest for a single large binary stream. One line per
// generation carries the same diagnostic information.
var taskLogErrs taskLogErrLimiter

type taskLogErrLimiter struct {
	mu       sync.Mutex
	reported map[string]int32 // task id -> the assignment epoch already logged
}

// shouldLog reports whether this task+epoch has not yet been logged, recording
// it if so. A later epoch for the same task reports again: a new assignment
// generation is a new failure worth one line.
func (l *taskLogErrLimiter) shouldLog(taskID string, epoch int32) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.reported == nil {
		l.reported = make(map[string]int32)
	}
	if got, ok := l.reported[taskID]; ok && got == epoch {
		return false
	}
	if len(l.reported) >= taskLogErrLimiterMax {
		l.reported = make(map[string]int32)
	}
	l.reported[taskID] = epoch
	return true
}

func (l *taskLogErrLimiter) reset() {
	l.mu.Lock()
	l.reported = nil
	l.mu.Unlock()
}

// handleTaskLog appends a log chunk from an agent and, if anyone is tailing that
// task, publishes it to the SSE broker.
//
// workerID is the connection's own authenticated worker, resolved at
// registration and never taken from the wire - an agent cannot influence it, so
// it cannot claim to be somebody else. It is threaded here in the same way
// applyInventoryUpdate already receives it.
//
// This runs synchronously on the Connect recv goroutine, which also carries that
// worker's status, inventory and telemetry messages, so everything below is
// deliberately cheap: exactly one DB round trip (the insert itself returns the
// job id and seq), one map lookup when nobody is watching, and a non-blocking
// Publish. Do not add a query, a goroutine, or a queue here.
func (h *Handler) handleTaskLog(ctx context.Context, workerID pgtype.UUID, chunk *relayv1.TaskLogChunk) {
	var taskID pgtype.UUID
	if err := taskID.Scan(chunk.TaskId); err != nil {
		return
	}

	stream := "stdout"
	if chunk.Stream == relayv1.LogStream_LOG_STREAM_STDERR {
		stream = "stderr"
	}

	row, err := h.q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID:          taskID,
		Stream:          stream,
		Content:         string(chunk.Content),
		AssignmentEpoch: int32(chunk.Epoch),
		WorkerID:        workerID,
	})
	if err != nil {
		// pgx.ErrNoRows means the fence rejected the chunk, for either of two
		// independent reasons: the sender is not the task's current assignee (a
		// forged or misrouted chunk - workerID comes from the authenticated
		// registration, never from the wire), or the sender's generation is stale
		// because the task was requeued or cancelled (both bump
		// assignment_epoch). The two are deliberately indistinguishable here; see
		// the comment on AppendTaskLog. Expected - drop it silently, and in
		// particular do NOT publish it: a zombie agent's output would otherwise
		// appear in a live view and then vanish on refresh, because it was
		// correctly never stored. Anything else is a real persist failure that
		// used to be swallowed by `_ =`.
		//
		// Rate-limited to one line per task per assignment epoch: the realistic
		// such failure repeats for every chunk of the task (binary stdout ->
		// 'invalid byte sequence for encoding "UTF8"'), and this runs on the recv
		// goroutine, so logging per chunk would delay that worker's status,
		// inventory and telemetry ingest. See taskLogErrs.
		//
		// Never log chunk.Content: it is raw subprocess output and can contain
		// secrets a job's own script echoed. Logging the error with %v is safe
		// because pgconn.PgError.Error() renders only severity, message and
		// SQLSTATE - never Detail, which is where Postgres puts "Failing row
		// contains (...)". Do not start logging pgErr.Detail here.
		if !errors.Is(err, pgx.ErrNoRows) && taskLogErrs.shouldLog(chunk.TaskId, int32(chunk.Epoch)) {
			log.Printf("worker: handleTaskLog AppendTaskLog %s: %v", chunk.TaskId, err)
		}
		return
	}

	// Persistence is unconditional and strictly precedes any publish; the publish
	// is derived from the stored row, so no line is ever published unstored.
	taskIDStr := uuidStr(taskID)
	if !h.broker.HasLogSubscriber(taskIDStr) {
		return // steady state: one map lookup, no marshal, no allocation
	}

	taskLogPublishes.Add(1)
	data, err := json.Marshal(taskLogEvent{
		TaskID:    taskIDStr,
		JobID:     uuidStr(row.JobID),
		Seq:       row.ID,
		Stream:    stream,
		Content:   string(chunk.Content),
		CreatedAt: row.CreatedAt.Time,
	})
	if err != nil {
		log.Printf("worker: handleTaskLog marshal %s: %v", chunk.TaskId, err)
		return
	}

	h.broker.Publish(events.Event{
		Type:   events.TypeTaskLog,
		JobID:  uuidStr(row.JobID),
		TaskID: taskIDStr,
		Data:   data,
	})
}

// handleTelemetry records a host-utilization sample from an agent, stamped
// with the server's receipt time.
func (h *Handler) handleTelemetry(workerID string, t *relayv1.WorkerTelemetry) {
	if h.Metrics == nil {
		return
	}
	h.Metrics.Append(workerID, metrics.Sample{
		At:             time.Now(),
		CPUPercent:     t.CpuPercent,
		MemUsedBytes:   t.MemUsedBytes,
		MemTotalBytes:  t.MemTotalBytes,
		HasGPU:         t.HasGpu,
		GPUUtilPercent: t.GpuUtilPercent,
		GPUMemUsed:     t.GpuMemUsedBytes,
		GPUMemTotal:    t.GpuMemTotalBytes,
	})
}

// teardownConnection runs when a Connect stream ends. It always stops this
// connection's own send goroutine, but only tears down shared worker state
// (DB offline flag, grace timer / requeue) when this connection still owns the
// worker's registry slot. A newer connection for the same worker must not be
// clobbered (Identity-checked teardown invariant).
func (h *Handler) teardownConnection(workerID string, sender *workerSender) {
	owned := h.registry.UnregisterIf(workerID, sender)
	sender.Close() // always stop our own send goroutine
	if !owned {
		return // a newer connection owns this worker; leave shared state alone
	}
	epoch := sender.connEpoch
	if h.markWorkerOffline(workerID, epoch) == 0 {
		return // a fresher connection holds the epoch; leave grace/requeue alone
	}
	if h.grace != nil {
		h.grace.Start(workerID, epoch)
	} else {
		h.requeueWorkerTasks(workerID, epoch)
	}
}

// markWorkerOffline is called in a defer after the stream ends. It is fenced on
// connection_epoch: if a fresher connection has bumped the epoch, the write
// affects zero rows and the offline broker event / metrics-clear are skipped.
// Returns the number of rows updated (0 = fence superseded, 1 = applied).
func (h *Handler) markWorkerOffline(workerID string, epoch int32) int64 {
	var id pgtype.UUID
	if err := id.Scan(workerID); err != nil {
		return 0
	}
	ctx := context.Background()
	now := time.Now()
	rows, err := h.q.MarkWorkerOfflineIfEpoch(ctx, store.MarkWorkerOfflineIfEpochParams{
		ID:              id,
		LastSeenAt:      pgtype.Timestamptz{Time: now, Valid: true},
		DisconnectedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		ConnectionEpoch: epoch,
	})
	if err != nil || rows == 0 {
		return 0
	}
	h.broker.Publish(events.Event{
		Type: "worker",
		Data: []byte(fmt.Sprintf(`{"id":%q,"status":"offline"}`, workerID)),
	})
	if h.Metrics != nil {
		h.Metrics.Clear(workerID)
	}
	return rows
}

// requeueWorkerTasks requeues dispatched/running tasks for a disconnected
// worker, fenced on connection_epoch: if a fresher connection has bumped the
// epoch, the EXISTS guard fails and zero tasks move. Bumps assignment_epoch on
// each requeued task (task-level fence preserved).
func (h *Handler) requeueWorkerTasks(workerID string, epoch int32) {
	var id pgtype.UUID
	if err := id.Scan(workerID); err != nil {
		return
	}
	ctx := context.Background()
	_, _ = h.q.RequeueWorkerTasksIfEpoch(ctx, store.RequeueWorkerTasksIfEpochParams{
		WorkerID:        id,
		ConnectionEpoch: epoch,
	})
	go h.triggerDispatch()
}

// updateJobStatusFromTasks atomically recomputes and persists a job's status
// from its tasks in a single SQL statement, so concurrent last-task completions
// can never strand the job in 'running'. Returns the new status string, or ""
// if it could not be determined (e.g. the job has no tasks).
func updateJobStatusFromTasks(ctx context.Context, q *store.Queries, jobID pgtype.UUID) string {
	status, err := q.RecomputeJobStatus(ctx, jobID)
	if err != nil {
		return ""
	}
	return status
}

// applyInventory does a transactional full-replace of workspace inventory for
// a worker: deletes all existing rows, then inserts each non-deleted entry.
func (h *Handler) applyInventory(ctx context.Context, workerID pgtype.UUID, inv []*relayv1.WorkspaceInventoryUpdate) error {
	return pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := h.q.WithTx(tx)
		if err := q.ReplaceWorkerInventory(ctx, workerID); err != nil {
			return err
		}
		for _, u := range inv {
			if u.Deleted {
				continue
			}
			ts, _ := time.Parse(time.RFC3339, u.LastUsedAt) // blank → zero time
			if err := q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
				WorkerID:     workerID,
				SourceType:   u.SourceType,
				SourceKey:    u.SourceKey,
				ShortID:      u.ShortId,
				BaselineHash: u.BaselineHash,
				LastUsedAt:   pgtype.Timestamptz{Time: ts, Valid: !ts.IsZero()},
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// applyInventoryUpdate upserts or deletes a single workspace inventory row.
func (h *Handler) applyInventoryUpdate(ctx context.Context, workerID pgtype.UUID, u *relayv1.WorkspaceInventoryUpdate) error {
	if u.Deleted {
		return h.q.DeleteWorkerWorkspace(ctx, store.DeleteWorkerWorkspaceParams{
			WorkerID: workerID, SourceType: u.SourceType, SourceKey: u.SourceKey,
		})
	}
	ts, _ := time.Parse(time.RFC3339, u.LastUsedAt)
	return h.q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID:     workerID,
		SourceType:   u.SourceType,
		SourceKey:    u.SourceKey,
		ShortID:      u.ShortId,
		BaselineHash: u.BaselineHash,
		LastUsedAt:   pgtype.Timestamptz{Time: ts, Valid: !ts.IsZero()},
	})
}

// uuidStr converts a pgtype.UUID to its canonical string representation.
func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
