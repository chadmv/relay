package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"relay/internal/api"
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
// WatchdogMaxRowsPerSweep bounds one sweep. It is not only a volume cap: every
// row in a batch is written against the same scan, so an unbounded sweep makes
// the scan-to-write window for the last row the whole loop rather than an
// instant, and that window is where a concurrent agent transition lands. A few
// hundred comfortably exceeds a healthy fleet's entire assigned set, so in
// normal operation it never binds; when it does, the 60s tick drains the
// remainder oldest-first and the sweep says so in the log.
const WatchdogMaxRowsPerSweep = 500

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

	// counters is a VALUE field, so the zero value works and a bare &Watchdog{}
	// in a test has a working counter set with no nil case to get wrong.
	counters watchdogCounters
}

// CounterSnapshot satisfies api.WatchdogSource. The interface is declared in
// internal/api rather than here because internal/scheduler imports internal/api
// (dispatch.go) and the reverse import is impossible - so for this section, and
// unlike every other, the CONSUMER owns the type.
func (w *Watchdog) CounterSnapshot() api.WatchdogCounters { return w.counters.snapshot() }

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

	scanNow := w.now()
	overdue, err := w.q.ListOverdueAssignedTasks(ctx, store.ListOverdueAssignedTasksParams{
		AbsoluteEnabled: absoluteEnabled,
		AbsoluteCutoff:  pgtype.Timestamptz{Time: scanNow.Add(-w.maxAssignment), Valid: true},
		ExecEnabled:     execEnabled,
		Now:             pgtype.Timestamptz{Time: scanNow, Valid: true},
		MarginSeconds:   int64(w.margin / time.Second),
		MaxRows:         WatchdogMaxRowsPerSweep,
	})
	if err != nil {
		return err
	}
	if len(overdue) >= WatchdogMaxRowsPerSweep {
		// Say so, or a capped sweep is indistinguishable from a complete one and
		// an operator reading "N swept" concludes the fleet is healthy again.
		log.Printf("watchdog: sweep hit its %d-row cap; the remainder is left for the next tick (oldest first)",
			WatchdogMaxRowsPerSweep)
	}

	// Cancels are dispatched AS EACH ROW IS SWEPT, not batched after the loop.
	// Batching delays a send by the length of the whole sweep, and CancelTask
	// carries no epoch - the agent cancels whatever a.runners[taskID] finds - so a
	// late cancel can kill a FRESH run of the same task id that an operator retry
	// reopened in the meantime. Adding an epoch to the proto is a named non-goal;
	// not delaying the send is free. Each send still runs on its own goroutine, so
	// N overdue tasks on ONE wedged worker cost ~one send timeout, not N.
	var wg sync.WaitGroup
	defer wg.Wait()

	for _, t := range overdue {
		// The clock is read PER ROW. Sharing one reading across a batch stamps
		// every row with a finished_at from the start of the loop, and - now that
		// started_at is write-once - a row whose agent stamped a real start time
		// mid-sweep could end up with started_at LATER than finished_at.
		now := w.now()
		updated, err := w.q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
			ID:     t.ID,
			Status: "timed_out",
			// WorkerID and AssignmentEpoch are FENCES, not values. StartedAt is
			// neither: UpdateTaskStatus COALESCEs it, so this argument only ever
			// fills a NULL and can no longer clobber a start time the agent
			// stamped after this row was scanned.
			WorkerID:        t.WorkerID,
			StartedAt:       t.StartedAt,
			FinishedAt:      pgtype.Timestamptz{Time: now, Valid: true},
			AssignmentEpoch: t.AssignmentEpoch,
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

		// COUNTED ONLY WHEN THE WRITE MATCHED. A fence-rejected write ended
		// nothing, and counting it would inflate the one number an operator uses
		// to decide whether to disable a machine.
		w.counters.record(uuidStr(t.WorkerID))

		// One line per SWEPT task, unbudgeted, and that is safe: the count per
		// sweep is bounded by WatchdogMaxRowsPerSweep, each task can be swept at
		// most once (it is terminal afterwards), and nothing in the line is
		// caller-supplied. A watchdog that kills somebody's work without saying
		// why it decided to is worse than no watchdog - which is also why the line
		// must never assert something false; see watchdogSweptLine.
		log.Printf("watchdog: task %s (job %s, worker %s) %s",
			uuidStr(updated.ID), uuidStr(updated.JobID), uuidStr(t.WorkerID),
			watchdogSweptLine(t, now, w.margin, w.maxAssignment))

		finalizeTerminalTask(ctx, w.q, w.broker, "watchdog", updated, "timed_out")
		if err := w.q.NotifyTaskCompleted(ctx); err != nil {
			log.Printf("watchdog: NotifyTaskCompleted: %v", err)
		}

		workerID, taskID := uuidStr(t.WorkerID), uuidStr(t.ID)
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.sendCancel(workerID, taskID)
		}()
	}
	return nil
}

// sendCancel tells one swept task's agent to stop, so the coordinator does not
// merely do bookkeeping while an orphan subprocess keeps running against a
// workspace and the freed slot over-subscribes the machine that is already in
// trouble. Deliberately the same shape as api.sendCancelSignals, which is the
// reviewed precedent for a coordinator-side terminal write over a live
// assignment - handleCancelJob does exactly this today.
//
// Best-effort: the return value is ignored, because a failed send just means the
// agent already lost the task, and the watchdog is registry-blind by design -
// under multi-replica operation the agent may be connected elsewhere.
//
// force=false deliberately. force=true skips workspace finalize, which risks
// leaving a P4 workspace in a state that poisons warm-workspace scoring for
// every later task on that machine; force=false still closes cancelledCh, which
// is the escape that frees a log write parked on a full sendCh. It also matches
// handleDisableWorker, the other place the coordinator unilaterally takes tasks
// away from a still-connected agent.
func (w *Watchdog) sendCancel(workerID, taskID string) {
	_ = w.canceller.SendCancel(workerID, taskID, false)
}

// overdueReason reports which bound a swept row blew, FOR THE LOG LINE ONLY. It
// is not a second gate: the database decided, and this only re-derives the
// explanation from a row read at a slightly different instant, so the two CAN
// disagree.
//
// When neither arm explains the row it returns "unknown" rather than naming one.
// The previous version fell back to "absolute" unconditionally and so could print
// "timed out by the absolute bound: assignment age 3h0m0s exceeds 24h0m0s" - a
// sentence contradicted by its own numbers. Since this line is the whole
// justification for logging one per swept task without a budget, it may not
// assert something an operator can see is false.
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
	if maxAssignment > 0 && t.AssignedAt.Valid && age > maxAssignment {
		return "absolute", age, maxAssignment
	}
	return "unknown", age, maxAssignment
}

// watchdogSweptLine renders the explanation half of the per-task sweep line. The
// "exceeds" clause appears only when the derived age really does exceed the
// derived bound; otherwise the line reports that the row no longer looks overdue
// from here, which is informative and true rather than confident and false.
func watchdogSweptLine(t store.Task, now time.Time, margin, maxAssignment time.Duration) string {
	arm, age, bound := overdueReason(t, now, margin, maxAssignment)
	if arm == "unknown" {
		return fmt.Sprintf(
			"timed out by the scan, but neither bound explains it when re-derived here (assignment age %s, "+
				"cap %s): the row was re-read after the scan compared it",
			age.Round(time.Second), maxAssignment)
	}
	return fmt.Sprintf("timed out by the %s bound: age %s exceeds %s",
		arm, age.Round(time.Second), bound)
}
