package scheduler

import (
	"context"
	"errors"
	"log"
	"time"

	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// WatchdogSweepInterval is how often the Watchdog re-scans for overdue
// assignments. It is a constant, not a knob: it is an implementation cadence,
// not an operational timeout, and against hour-scale bounds a 60s tick
// contributes nothing to the bound's accuracy. Named for the watchdog rather
// than as a bare SweepInterval so it is never confused with
// metrics.SweepInterval, which means something different.
const WatchdogSweepInterval = 60 * time.Second

// DefaultWatchdogMargin is added to a task's own timeout_sec before the
// coordinator declares it timed out. It has to absorb the whole gap between the
// agent's deadline firing and the coordinator seeing the terminal update:
// subprocess kill, proctree cleanup, final log flush, and a gRPC reconnect if
// the stream dropped - which README's own analysis puts at roughly 105s. 30m is
// about seventeen times that, chosen generously because the failure direction of
// "too small" is killing healthy work.
const DefaultWatchdogMargin = 30 * time.Minute

// DefaultMaxAssignment is the absolute cap on how long a task may stay assigned,
// measured from dispatch. It must exceed the longest LEGITIMATE assignment,
// which is dominated by a P4 sync on a 1 TB+ workspace plus the task's own run.
const DefaultMaxAssignment = 24 * time.Hour

// watchdogStore is the subset of *store.Queries the Watchdog needs.
// *store.Queries satisfies it; tests supply a fake, which is what makes the
// whole sweep unit-testable without Docker.
type watchdogStore interface {
	terminalTailStore
	ListOverdueAssignedTasks(ctx context.Context, arg store.ListOverdueAssignedTasksParams) ([]store.Task, error)
	UpdateTaskStatus(ctx context.Context, arg store.UpdateTaskStatusParams) (store.Task, error)
	NotifyTaskCompleted(ctx context.Context) error
}

// taskCanceller is the subset of *worker.Registry the Watchdog needs.
type taskCanceller interface {
	SendCancel(workerID, taskID string, force bool) error
}

// Watchdog ends the assignment of a task that has been non-terminal for too
// long. It is the coordinator's own bound on task duration, and it exists
// because tasks.timeout_sec is otherwise enforced only by the agent - a timeout
// the agent is free not to honour is a suggestion, not a timeout.
//
// EPOCH FENCE, branch one: it writes through the existing UpdateTaskStatus,
// binding the assignment_epoch and worker_id read off the row its own scan just
// returned. It does NOT bump the epoch, and must not: this is a terminal
// transition, and the assignment surviving completion is load-bearing for the
// trailing-log flush.
//
// GRACE OWNS DISCONNECT; THE WATCHDOG OWNS DURATION. Two timers can fire on one
// row and the epoch fence is what makes that safe, in both orders:
//
//   - Watchdog first, grace second: the row is terminal, and grace's
//     RequeueWorkerTasksIfEpoch matches only ('dispatched','running'), so it
//     moves zero rows. Correct - the task was overdue whether or not its worker
//     later dropped.
//   - Grace first, watchdog second: the requeue set pending, worker_id NULL and
//     epoch N+1, so the watchdog's already-issued UpdateTaskStatus binds epoch N
//     and matches zero rows - on the epoch, first and independently of the other
//     two predicates. The requeue wins. Correct - an assignment that ended is
//     not the watchdog's to finish. The window is only between the scan and the
//     write; the scan itself would no longer return the row.
//   - Two replicas sweeping at once: first write wins, the second matches
//     nothing on the status allow-list. No leader election, no advisory lock.
//
// THE WATCHDOG IS DELIBERATELY REGISTRY-BLIND WHEN DECIDING TO WRITE. It never
// asks whether the worker is connected to THIS process: under multi-replica
// operation the agent may be connected to a different replica, so a local
// registry miss proves nothing, and the orphaned-`dispatched` case is precisely
// a row whose agent has been told to abandon it. The registry is consulted only
// to decide whether a cancel can be DELIVERED, which is why that send is
// best-effort by construction.
type Watchdog struct {
	q             watchdogStore
	canceller     taskCanceller
	broker        *events.Broker
	margin        time.Duration
	maxAssignment time.Duration
	now           func() time.Time // injectable clock; defaults to time.Now
}

// NewWatchdog constructs a Watchdog. A zero margin disables the execution arm
// and a zero maxAssignment disables the absolute arm; both zero disables the
// watchdog entirely, which is the documented escape hatch.
func NewWatchdog(q watchdogStore, c taskCanceller, broker *events.Broker, margin, maxAssignment time.Duration) *Watchdog {
	return &Watchdog{
		q: q, canceller: c, broker: broker,
		margin: margin, maxAssignment: maxAssignment,
		now: time.Now,
	}
}

// Run blocks until ctx is cancelled, sweeping every WatchdogSweepInterval.
func (w *Watchdog) Run(ctx context.Context) {
	t := time.NewTicker(WatchdogSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.SweepOnce(ctx); err != nil {
				log.Printf("watchdog: %v", err)
			}
		}
	}
}

// SweepOnce performs one pass over the overdue set. Exported for tests.
func (w *Watchdog) SweepOnce(ctx context.Context) error {
	execEnabled := w.margin > 0
	absoluteEnabled := w.maxAssignment > 0
	if !execEnabled && !absoluteEnabled {
		// Both arms off. A scan here is a guaranteed-empty round trip every tick,
		// forever; skip it rather than pay for it. The condition is an AND of both
		// arms being off - with an OR, configuring one arm off would silently
		// disable the whole watchdog.
		return nil
	}

	now := w.now()
	overdue, err := w.q.ListOverdueAssignedTasks(ctx, store.ListOverdueAssignedTasksParams{
		AbsoluteEnabled: absoluteEnabled,
		AbsoluteCutoff:  pgtype.Timestamptz{Time: now.Add(-w.maxAssignment), Valid: true},
		ExecEnabled:     execEnabled,
		Now:             pgtype.Timestamptz{Time: now, Valid: true},
		MarginSeconds:   int64(w.margin / time.Second),
	})
	if err != nil {
		return err
	}

	for _, t := range overdue {
		updated, err := w.q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
			ID:              t.ID,
			Status:          "timed_out",
			WorkerID:        t.WorkerID,        // fence, not a value
			StartedAt:       t.StartedAt,       // preserved unchanged
			FinishedAt:      pgtype.Timestamptz{Time: now, Valid: true},
			AssignmentEpoch: t.AssignmentEpoch, // fence: real and non-zero, from the row
		})
		if err != nil {
			// pgx.ErrNoRows means somebody else got there first - the agent
			// finished, a cancel landed, a grace expiry requeued, or a sibling
			// replica swept it. That is the CORRECT outcome, not a failure, so it
			// is not logged. Any other error is real. Either way, continue to the
			// next row: one bad row must never end the sweep.
			if !errors.Is(err, pgx.ErrNoRows) {
				log.Printf("watchdog: UpdateTaskStatus(timed_out) for task %s: %v", uuidStr(t.ID), err)
			}
			continue
		}

		arm, age, bound := overdueReason(t, now, w.margin, w.maxAssignment)
		// One line per SWEPT task, unbudgeted, and that is safe: the count per
		// sweep is bounded by the fleet's assigned-task count, each task can be
		// swept at most once (it is terminal afterwards), and nothing in the line
		// is caller-supplied. A watchdog that kills somebody's work without saying
		// why it decided to is worse than no watchdog.
		log.Printf("watchdog: task %s (job %s, worker %s) timed out by the %s bound: assignment age %s exceeds %s",
			uuidStr(updated.ID), uuidStr(updated.JobID), uuidStr(t.WorkerID), arm, age.Round(time.Second), bound)

		finalizeTerminalTask(ctx, w.q, w.broker, "watchdog", updated, "timed_out")
		if err := w.q.NotifyTaskCompleted(ctx); err != nil {
			log.Printf("watchdog: NotifyTaskCompleted: %v", err)
		}
	}
	return nil
}

// overdueReason reports which bound a swept row blew, FOR THE LOG LINE ONLY. It
// is not a second gate: the database decided, and this only re-derives the
// explanation. If a row somehow satisfies neither arm in Go it is reported as
// "absolute", because that arm applies to every assigned row.
func overdueReason(t store.Task, now time.Time, margin, maxAssignment time.Duration) (arm string, age, bound time.Duration) {
	if margin > 0 && t.StartedAt.Valid && t.TimeoutSeconds != nil && *t.TimeoutSeconds > 0 {
		execAge := now.Sub(t.StartedAt.Time)
		execBound := time.Duration(*t.TimeoutSeconds)*time.Second + margin
		if execAge > execBound {
			return "execution", execAge, execBound
		}
	}
	if t.AssignedAt.Valid {
		age = now.Sub(t.AssignedAt.Time)
	}
	return "absolute", age, maxAssignment
}
