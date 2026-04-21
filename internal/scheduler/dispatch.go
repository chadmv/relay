package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
)

// Dispatcher runs the scheduling loop, matching eligible tasks to available workers.
type Dispatcher struct {
	q        *store.Queries
	registry *worker.Registry
	broker   *events.Broker
	trigger  chan struct{} // buffered 1, coalesced
}

// NewDispatcher returns a ready-to-use Dispatcher.
func NewDispatcher(q *store.Queries, r *worker.Registry, b *events.Broker) *Dispatcher {
	return &Dispatcher{
		q:        q,
		registry: r,
		broker:   b,
		trigger:  make(chan struct{}, 1),
	}
}

// Trigger signals a dispatch cycle (non-blocking, coalesced).
func (d *Dispatcher) Trigger() {
	select {
	case d.trigger <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is cancelled; fires on Trigger() or every 5s.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.trigger:
			d.dispatch(ctx)
		case <-ticker.C:
			d.dispatch(ctx)
		}
	}
}

// RunOnce runs a single dispatch cycle (for tests).
func (d *Dispatcher) RunOnce(ctx context.Context) {
	d.dispatch(ctx)
}

func (d *Dispatcher) dispatch(ctx context.Context) {
	tasks, err := d.q.GetEligibleTasks(ctx)
	if err != nil || len(tasks) == 0 {
		return
	}

	workers, err := d.q.ListWorkers(ctx)
	if err != nil {
		return
	}

	reservations, err := d.q.ListActiveReservations(ctx)
	if err != nil {
		return
	}

	for _, task := range tasks {
		w := d.selectWorker(ctx, task, workers, reservations)
		if w != nil {
			d.sendTask(ctx, task, *w)
		}
	}
}

func (d *Dispatcher) selectWorker(
	ctx context.Context,
	task store.Task,
	workers []store.Worker,
	reservations []store.Reservation,
) *store.Worker {
	// Build set of reserved worker IDs.
	reservedIDs := make(map[string]bool)
	for _, res := range reservations {
		for _, wid := range res.WorkerIds {
			reservedIDs[uuidStr(wid)] = true
		}
	}

	var best *store.Worker
	var bestFree int64 = -1

	for i := range workers {
		w := &workers[i]
		if w.Status != "online" {
			continue
		}
		if reservedIDs[uuidStr(w.ID)] {
			continue
		}
		// Check label match.
		ok, err := LabelMatch(task.Requires, w.Labels)
		if err != nil || !ok {
			continue
		}
		active, err := d.q.CountActiveTasksForWorker(ctx, w.ID)
		if err != nil {
			continue
		}
		free := int64(w.MaxSlots) - active
		if free <= 0 {
			continue
		}
		if free > bestFree {
			bestFree = free
			best = w
		}
	}

	return best
}

func (d *Dispatcher) sendTask(ctx context.Context, task store.Task, w store.Worker) {
	var env map[string]string
	if len(task.Env) > 0 {
		if err := json.Unmarshal(task.Env, &env); err != nil {
			env = nil
		}
	}

	var timeoutSecs int32
	if task.TimeoutSeconds != nil {
		timeoutSecs = *task.TimeoutSeconds
	}

	// Atomically claim the task before dispatching. If another dispatcher or
	// pass has already claimed it, ClaimTaskForWorker returns pgx.ErrNoRows and
	// we skip silently — this is the critical race guard.
	claimed, err := d.q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID:       task.ID,
		WorkerID: w.ID,
	})
	if err != nil {
		return
	}

	msg := &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: &relayv1.DispatchTask{
				TaskId:         uuidStr(claimed.ID),
				JobId:          uuidStr(claimed.JobID),
				Command:        claimed.Command,
				Env:            env,
				TimeoutSeconds: timeoutSecs,
				Epoch:          int64(claimed.AssignmentEpoch),
			},
		},
	}

	if err := d.registry.Send(uuidStr(w.ID), msg); err != nil {
		// Worker disappeared between claim and send; revert so another pass
		// (or another worker) can pick the task up.
		_ = d.q.RequeueTask(ctx, claimed.ID)
		return
	}

	d.broker.Publish(events.Event{
		Type:  "task",
		JobID: uuidStr(claimed.JobID),
		Data:  []byte(fmt.Sprintf(`{"id":%q,"status":"dispatched","worker_id":%q}`, uuidStr(claimed.ID), uuidStr(w.ID))),
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
