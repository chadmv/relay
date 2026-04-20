package worker

import (
	"context"
	"fmt"
	"io"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// Handler implements relayv1.AgentServiceServer.
type Handler struct {
	relayv1.UnimplementedAgentServiceServer
	q               *store.Queries
	registry        *Registry
	broker          *events.Broker
	triggerDispatch func()
}

// NewHandler returns a Handler wired to the given dependencies.
func NewHandler(q *store.Queries, r *Registry, b *events.Broker, triggerDispatch func()) *Handler {
	return &Handler{q: q, registry: r, broker: b, triggerDispatch: triggerDispatch}
}

// Connect implements relayv1.AgentServiceServer.
func (h *Handler) Connect(stream relayv1.AgentService_ConnectServer) error {
	ctx := stream.Context()

	// First message must be a RegisterRequest.
	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("recv first message: %w", err)
	}
	reg := first.GetRegister()
	if reg == nil {
		return fmt.Errorf("first message must be RegisterRequest")
	}

	workerID, sender, err := h.registerWorker(ctx, stream, reg)
	if err != nil {
		return fmt.Errorf("register worker: %w", err)
	}

	defer h.requeueWorkerTasks(workerID)   // runs 4th
	defer h.markWorkerOffline(workerID)    // runs 3rd
	defer sender.Close()                   // runs 2nd
	defer h.registry.Unregister(workerID) // runs 1st

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
			h.handleTaskLog(ctx, p.TaskLog)
		}
	}
}

// registerWorker upserts the worker, marks it online, sends the RegisterResponse,
// publishes an SSE event, and triggers the dispatch loop.
func (h *Handler) registerWorker(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest) (string, *workerSender, error) {
	w, err := h.q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name:     reg.Hostname,
		Hostname: reg.Hostname,
		CpuCores: reg.CpuCores,
		RamGb:    reg.RamGb,
		GpuCount: reg.GpuCount,
		GpuModel: reg.GpuModel,
		Os:       reg.Os,
	})
	if err != nil {
		return "", nil, fmt.Errorf("upsert worker: %w", err)
	}

	w, err = h.q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:         w.ID,
		Status:     "online",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		return "", nil, fmt.Errorf("update worker status: %w", err)
	}

	workerID := uuidStr(w.ID)

	// Send RegisterResponse on the raw stream. At this point the worker is not
	// yet in the registry, so no other goroutine can race us on stream.Send.
	if err := stream.Send(&relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_RegisterResponse{
			RegisterResponse: &relayv1.RegisterResponse{WorkerId: workerID},
		},
	}); err != nil {
		return "", nil, fmt.Errorf("send register response: %w", err)
	}

	// From here on, all sends go through the serializing wrapper.
	sender := NewWorkerSender(stream)
	h.registry.Register(workerID, sender)

	h.broker.Publish(events.Event{
		Type: "worker",
		Data: []byte(fmt.Sprintf(`{"id":%q,"status":"online"}`, workerID)),
	})

	go h.triggerDispatch()

	return workerID, sender, nil
}

// handleTaskStatus processes a TaskStatusUpdate from an agent.
func (h *Handler) handleTaskStatus(ctx context.Context, upd *relayv1.TaskStatusUpdate) {
	var taskID pgtype.UUID
	if err := taskID.Scan(upd.TaskId); err != nil {
		return
	}

	task, err := h.q.GetTask(ctx, taskID)
	if err != nil {
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
	default:
		return
	}

	terminal := statusStr == "failed" || statusStr == "timed_out"

	// Retry if applicable.
	if terminal && task.RetryCount < task.Retries {
		if _, err := h.q.IncrementTaskRetryCount(ctx, taskID); err == nil {
			updateJobStatusFromTasks(ctx, h.q, task.JobID)
			go h.triggerDispatch()
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
		ID:         taskID,
		Status:     statusStr,
		WorkerID:   task.WorkerID,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	})
	if err != nil {
		return
	}

	if terminal {
		_ = h.q.FailDependentTasks(ctx, taskID)
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

	if statusStr == "done" {
		go h.triggerDispatch()
	}
}

// handleTaskLog appends a log chunk from an agent.
func (h *Handler) handleTaskLog(ctx context.Context, chunk *relayv1.TaskLogChunk) {
	var taskID pgtype.UUID
	if err := taskID.Scan(chunk.TaskId); err != nil {
		return
	}

	stream := "stdout"
	if chunk.Stream == relayv1.LogStream_LOG_STREAM_STDERR {
		stream = "stderr"
	}

	_ = h.q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID:  taskID,
		Stream:  stream,
		Content: string(chunk.Content),
	})
}

// markWorkerOffline is called in a defer after the stream ends.
func (h *Handler) markWorkerOffline(workerID string) {
	var id pgtype.UUID
	if err := id.Scan(workerID); err != nil {
		return
	}
	ctx := context.Background()
	_, _ = h.q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:         id,
		Status:     "offline",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	h.broker.Publish(events.Event{
		Type: "worker",
		Data: []byte(fmt.Sprintf(`{"id":%q,"status":"offline"}`, workerID)),
	})
}

// requeueWorkerTasks requeues dispatched/running tasks for a disconnected worker.
func (h *Handler) requeueWorkerTasks(workerID string) {
	var id pgtype.UUID
	if err := id.Scan(workerID); err != nil {
		return
	}
	ctx := context.Background()
	_ = h.q.RequeueWorkerTasks(ctx, id)
	go h.triggerDispatch()
}

// updateJobStatusFromTasks recomputes and persists a job's status from its tasks.
// Returns the new status string, or "" if the status could not be determined.
func updateJobStatusFromTasks(ctx context.Context, q *store.Queries, jobID pgtype.UUID) string {
	tasks, err := q.ListTasksByJob(ctx, jobID)
	if err != nil || len(tasks) == 0 {
		return ""
	}
	var done, failed, active int
	for _, t := range tasks {
		switch t.Status {
		case "done":
			done++
		case "failed", "timed_out":
			failed++
		default:
			active++
		}
	}
	var newStatus string
	switch {
	case active > 0:
		newStatus = "running"
	case done == len(tasks):
		newStatus = "done"
	default:
		newStatus = "failed"
	}
	_, _ = q.UpdateJobStatus(ctx, store.UpdateJobStatusParams{ID: jobID, Status: newStatus})
	return newStatus
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

