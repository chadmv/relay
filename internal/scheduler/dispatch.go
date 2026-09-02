package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"relay/internal/api"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Dispatcher runs the scheduling loop, matching eligible tasks to available workers.
type Dispatcher struct {
	q             *store.Queries
	registry      *worker.Registry
	broker        *events.Broker
	publicBaseURL string        // "" disables the rendered URLs; see jobURL/taskURL
	trigger       chan struct{} // buffered 1, coalesced
}

// NewDispatcher returns a ready-to-use Dispatcher. publicBaseURL is the
// normalized RELAY_PUBLIC_URL, or "" when the operator has not set one.
//
// It is a parameter rather than a settable field because a caller that forgets
// to set it produces silently absent URLs - indistinguishable from an
// unconfigured server, with every test green.
//
// The trailing slash is trimmed HERE because jobURL and taskURL concatenate
// with no separator logic and the guarantee they rely on is produced in
// package main, which this package cannot reference.
func NewDispatcher(q *store.Queries, r *worker.Registry, b *events.Broker, publicBaseURL string) *Dispatcher {
	return &Dispatcher{
		q:             q,
		registry:      r,
		broker:        b,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		trigger:       make(chan struct{}, 1),
	}
}

// Trigger signals a dispatch cycle (non-blocking, coalesced).
func (d *Dispatcher) Trigger() {
	select {
	case d.trigger <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is cancelled; fires on Trigger(), on NOTIFY (via
// NotifyListener), or every 30s as a safety-net poll.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
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
	if err != nil {
		log.Printf("dispatch: GetEligibleTasks: %v", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	workers, err := d.q.ListWorkers(ctx)
	if err != nil {
		log.Printf("dispatch: ListWorkers: %v", err)
		return
	}

	// Whether any connected worker advertises a workspace provider. Used only to
	// distinguish "source-bearing task held because no capable worker exists"
	// (observable) from "all capable workers busy" (normal backpressure, silent).
	anyProviderWorker := false
	for i := range workers {
		w := &workers[i]
		if (w.Status == "online" || w.Status == "stale") && !w.DisabledAt.Valid && w.SupportsWorkspaces {
			anyProviderWorker = true
			break
		}
	}

	reservations, err := d.q.ListActiveReservations(ctx)
	if err != nil {
		log.Printf("dispatch: ListActiveReservations: %v", err)
		return
	}

	counts, err := d.q.CountActiveTasksByAllWorkers(ctx)
	if err != nil {
		log.Printf("dispatch: CountActiveTasksByAllWorkers: %v", err)
		return
	}
	activeByWorker := make(map[pgtype.UUID]int64, len(counts))
	for _, c := range counts {
		activeByWorker[c.WorkerID] = c.Active
	}

	// Build warm-workspace map for tasks that have a source spec.
	streamsByType := make(map[string][]string) // source_type → []source_key
	for _, task := range tasks {
		if len(task.Source) == 0 {
			continue
		}
		var s api.SourceSpec
		if err := json.Unmarshal(task.Source, &s); err != nil {
			continue
		}
		if s.Type != "" && s.Stream != "" {
			streamsByType[s.Type] = append(streamsByType[s.Type], s.Stream)
		}
	}
	warmByWorker := make(map[pgtype.UUID][]store.WorkerWorkspace)
	for typ, keys := range streamsByType {
		rows, err := d.q.ListWarmWorkspacesForKeys(ctx, store.ListWarmWorkspacesForKeysParams{
			SourceType: typ, SourceKeys: keys,
		})
		if err != nil {
			continue // warm scoring is best-effort
		}
		for _, w := range rows {
			warmByWorker[w.WorkerID] = append(warmByWorker[w.WorkerID], w)
		}
	}

	heldSourceTasks := 0
	for _, task := range tasks {
		w := d.selectWorker(task, workers, reservations, activeByWorker, warmByWorker)
		if w != nil {
			if d.sendTask(ctx, task, *w) {
				activeByWorker[w.ID]++ // track in-cycle dispatches
			}
			continue
		}
		// No worker selected. If this is a source-bearing task and no connected
		// worker advertises a workspace provider, it is held for lack of a
		// capable worker (distinct from normal "all busy" backpressure).
		if !anyProviderWorker && taskIsSourceBearing(task) {
			heldSourceTasks++
		}
	}
	if heldSourceTasks > 0 {
		log.Printf("dispatch: %d source-bearing task(s) held pending; no connected worker has a workspace provider", heldSourceTasks)
	}
}

// taskIsSourceBearing reports whether a task carries a parseable source spec
// with a non-empty Type - i.e. one that names a workspace provider. This is the
// single predicate selectWorker uses to require a provider-capable worker and
// the held-pending count uses to decide a task is stuck for lack of one, so the
// two never disagree. A parseable but typeless spec ({}) is not source-bearing.
func taskIsSourceBearing(task store.Task) bool {
	if len(task.Source) == 0 {
		return false
	}
	var s api.SourceSpec
	if err := json.Unmarshal(task.Source, &s); err != nil {
		return false
	}
	return s.Type != ""
}

func (d *Dispatcher) selectWorker(
	task store.Task,
	workers []store.Worker,
	reservations []store.Reservation,
	activeByWorker map[pgtype.UUID]int64,
	warmByWorker map[pgtype.UUID][]store.WorkerWorkspace,
) *store.Worker {
	// Build set of reserved worker IDs.
	reservedIDs := make(map[string]bool)
	for _, res := range reservations {
		for _, wid := range res.WorkerIds {
			reservedIDs[uuidStr(wid)] = true
		}
	}

	// Parse task source (for warm scoring). Whether the task actually requires a
	// workspace provider is decided by taskIsSourceBearing below, not by this
	// parse succeeding - a parseable but typeless spec ({}) is not source-bearing.
	var taskSrc *api.SourceSpec
	if len(task.Source) > 0 {
		var s api.SourceSpec
		if err := json.Unmarshal(task.Source, &s); err == nil {
			taskSrc = &s
		}
	}
	sourceBearing := taskIsSourceBearing(task)

	var best *store.Worker
	var bestScore int64 = -1

	for i := range workers {
		w := &workers[i]
		// A "stale" worker is still connected and able to run tasks; the
		// status only signals missing telemetry, so it stays dispatch-eligible.
		// Only non-connected statuses (e.g. "offline") are excluded.
		if w.Status != "online" && w.Status != "stale" {
			continue
		}
		// A disabled worker keeps its connection and liveness status but must
		// not receive new task dispatches.
		if w.DisabledAt.Valid {
			continue
		}
		if reservedIDs[uuidStr(w.ID)] {
			continue
		}
		ok, err := LabelMatch(task.Requires, w.Labels)
		if err != nil || !ok {
			continue
		}
		free := int64(w.MaxSlots) - activeByWorker[w.ID]
		if free <= 0 {
			continue
		}
		// Source-bearing tasks require a worker with a workspace provider.
		// Skipping providerless workers here (rather than scoring them lower) is
		// the hard requirement: a source-bearing task must never be dispatched to
		// a worker that will only PREPARE_FAILED it. The source-bearing decision
		// goes through taskIsSourceBearing so this filter and the held-pending
		// count always agree. For a non-source task it is a no-op.
		if sourceBearing && !w.SupportsWorkspaces {
			continue
		}
		score := free
		if taskSrc != nil {
			for _, ws := range warmByWorker[w.ID] {
				if ws.SourceType == taskSrc.Type && ws.SourceKey == taskSrc.Stream {
					estimate := BaselineHashFromAPISpec(taskSrc)
					if estimate != "" && ws.BaselineHash == estimate {
						score += 10_000
					} else {
						score += 1_000
					}
					break
				}
			}
		}
		if score > bestScore {
			bestScore = score
			best = w
		}
	}

	return best
}

func (d *Dispatcher) sendTask(ctx context.Context, task store.Task, w store.Worker) bool {
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
		ID:         task.ID,
		WorkerID:   w.ID,
		AssignedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		// pgx.ErrNoRows is the benign claim race (another dispatcher won) and
		// fires on the normal happy path; only log genuine errors.
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("dispatch: ClaimTaskForWorker for task %s: %v", uuidStr(task.ID), err)
		}
		return false
	}

	var commandsArgv [][]string
	if len(claimed.Commands) > 0 {
		if err := json.Unmarshal(claimed.Commands, &commandsArgv); err != nil {
			d.failClaimedTask(ctx, claimed, fmt.Sprintf("bad commands JSON: %v", err))
			return false
		}
	}
	dtCommands := make([]*relayv1.CommandLine, 0, len(commandsArgv))
	for _, argv := range commandsArgv {
		dtCommands = append(dtCommands, &relayv1.CommandLine{Argv: argv})
	}
	// Both ids come off `claimed` - the RETURNING row of the fenced
	// ClaimTaskForWorker - and the URLs are rendered from those same two locals.
	// One row, so a link cannot name a different row than the dispatch it
	// travels on.
	jobIDStr := uuidStr(claimed.JobID)
	taskIDStr := uuidStr(claimed.ID)
	dt := &relayv1.DispatchTask{
		TaskId:         taskIDStr,
		JobId:          jobIDStr,
		JobUrl:         jobURL(d.publicBaseURL, jobIDStr),
		TaskUrl:        taskURL(d.publicBaseURL, jobIDStr, taskIDStr),
		Commands:       dtCommands,
		Env:            env,
		TimeoutSeconds: timeoutSecs,
		Epoch:          int64(claimed.AssignmentEpoch),
	}
	if len(claimed.Source) > 0 {
		var ss api.SourceSpec
		if err := json.Unmarshal(claimed.Source, &ss); err != nil {
			d.failClaimedTask(ctx, claimed, fmt.Sprintf("bad source JSON: %v", err))
			return false
		}
		dt.Source = sourceSpecToProto(&ss)
	}
	msg := &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: dt,
		},
	}

	if err := d.registry.Send(uuidStr(w.ID), msg); err != nil {
		// Worker disappeared or is wedged between claim and send; revert so
		// another pass (or another worker) can pick the task up.
		log.Printf("dispatch: send to worker %s failed: %v; requeueing task %s", uuidStr(w.ID), err, claimed.ID)
		// BOTH FENCES ARE ALREADY IN HAND, and both are read off the SAME
		// RETURNING row: claimed.AssignmentEpoch and claimed.WorkerID come from the
		// one ClaimTaskForWorker result, so the pair is provably consistent -
		// failClaimedTask below binds the same two the same way. Do not substitute
		// w.ID: it is equal today, but ClaimTaskForWorker does not reject a NULL
		// worker_id argument and tasks.worker_id is nullable, so sourcing the two
		// facts from one row is what keeps them from drifting apart.
		//
		// Passing them is what stops this goroutine - which has been sitting in
		// Send for up to sendTimeout - from requeueing a task that was released and
		// re-dispatched to another worker while it was blocked. The window belongs
		// to ErrSendTimeout on a WEDGED-BUT-REGISTERED sender; a disconnected
		// sender unblocks immediately and a missing one never reaches here at all.
		// The releases that reach it are an admin disable and a second Connect from
		// the same agent - NOT a grace timer, which is armed only after the sender
		// is already closed. See the statement's own comment in query/tasks.sql.
		//
		// The rowcount is discarded on purpose. Zero rows is the correct outcome
		// (another writer ended this assignment first), dispatchOne returns false
		// either way, and the unconditional log line above already reports the
		// failure that brought us here - a second line would add nothing.
		_, _ = d.q.RequeueTask(ctx, store.RequeueTaskParams{
			ID:              claimed.ID,
			AssignmentEpoch: claimed.AssignmentEpoch,
			WorkerID:        claimed.WorkerID,
		})
		return false
	}

	d.broker.Publish(events.Event{
		Type:  "task",
		JobID: uuidStr(claimed.JobID),
		Data:  []byte(fmt.Sprintf(`{"id":%q,"status":"dispatched","worker_id":%q}`, uuidStr(claimed.ID), uuidStr(w.ID))),
	})
	return true
}

// terminalTailStore is the subset of *store.Queries the shared terminal tail
// needs. *store.Queries satisfies it; the watchdog's own store interface embeds
// it so both callers reach the same code.
type terminalTailStore interface {
	FailDependentTasks(ctx context.Context, failedTaskID pgtype.UUID) error
	RecomputeJobStatus(ctx context.Context, id pgtype.UUID) (string, error)
}

// finalizeTerminalTask runs the tail every coordinator-side terminal writer
// shares: cascade to dependents, recompute the job, publish the task event, and
// publish a job event if the job itself went terminal. `task` must be the row
// UpdateTaskStatus RETURNED, not the row that was read before it - calling this
// for a write the fence rejected would cascade a failure the database refused.
//
// NotifyTaskCompleted is deliberately NOT here. Dispatcher.failClaimedTask has
// never called it and this extraction must not change that; the watchdog calls
// it itself.
//
// logPrefix keeps each caller's log lines byte-identical to what it emitted
// before the extraction.
func finalizeTerminalTask(
	ctx context.Context,
	q terminalTailStore,
	broker *events.Broker,
	logPrefix string,
	task store.Task,
	status string,
) {
	if err := q.FailDependentTasks(ctx, task.ID); err != nil {
		log.Printf("%s: FailDependentTasks for task %s: %v", logPrefix, uuidStr(task.ID), err)
	}
	jobStatus, err := q.RecomputeJobStatus(ctx, task.JobID)
	if err != nil {
		log.Printf("%s: RecomputeJobStatus for job %s: %v", logPrefix, uuidStr(task.JobID), err)
	}
	broker.Publish(events.Event{
		Type:  "task",
		JobID: uuidStr(task.JobID),
		Data:  []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, uuidStr(task.ID), status)),
	})
	if jobStatus == "done" || jobStatus == "failed" {
		broker.Publish(events.Event{
			Type:  "job",
			JobID: uuidStr(task.JobID),
			Data:  []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, uuidStr(task.JobID), jobStatus)),
		})
	}
}

// failClaimedTask marks an already-claimed task terminally 'failed' and cascades
// to its dependents. It is the single path the dispatcher uses when a claimed
// task carries poison persistent data (unparseable commands or source JSON):
// retrying can never succeed, so the task must not be requeued (which would churn
// the claim/requeue loop) nor left 'dispatched' (which would leak a worker slot).
//
// Epoch fence: the write goes through UpdateTaskStatus fenced on the claim's own
// assignment_epoch (a real, non-zero value from ClaimTaskForWorker). 'failed' is
// terminal, so the assignment ends and the epoch is intentionally NOT bumped. If
// another path ended the assignment between claim and here, UpdateTaskStatus
// affects zero rows (pgx.ErrNoRows); we stop SILENTLY, without retry, requeue or
// a log line - the race is expected, and the attempt is already reported by the
// unconditional line at the top of this function.
func (d *Dispatcher) failClaimedTask(ctx context.Context, claimed store.Task, reason string) {
	failClaimedTask(ctx, d.q, d.broker, claimed, reason)
}

// failClaimedStore is the subset of *store.Queries the terminal-fail path needs,
// narrowed exactly as terminalTailStore already was when that tail was
// extracted. Dispatcher.q is a concrete *store.Queries, so this is what makes
// the fence-rejection branch drivable by a fake - without Postgres, and
// therefore in the DEFAULT lane.
type failClaimedStore interface {
	terminalTailStore
	UpdateTaskStatus(ctx context.Context, arg store.UpdateTaskStatusParams) (store.Task, error)
}

// failClaimedTask is the body of the method above; see its doc comment.
func failClaimedTask(
	ctx context.Context,
	q failClaimedStore,
	broker *events.Broker,
	claimed store.Task,
	reason string,
) {
	log.Printf("dispatch: failing task %s terminally: %s", uuidStr(claimed.ID), reason)
	updated, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID:              claimed.ID,
		Status:          "failed",
		WorkerID:        claimed.WorkerID,
		StartedAt:       claimed.StartedAt,
		FinishedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
		AssignmentEpoch: claimed.AssignmentEpoch,
	})
	if err != nil {
		// A FENCE-REJECTION SITE OF THE `:one` KIND, and until now the only one
		// of that kind that did not distinguish pgx.ErrNoRows.
		//
		// NAME THE PARTITION, NOT A COUNT. A bare "the fifth of five" is a
		// uniqueness claim, and a uniqueness claim is a claim about the
		// COMPLEMENT - it cannot be checked by opening its subject, only by
		// searching for the shape (CLAUDE.md says exactly this about
		// RequeueTaskByID, which is where the rule was learned). The earlier
		// wording here said "the other four" and missed ClaimTaskForWorker in
		// Dispatcher.sendTask, earlier in this same file.
		//
		// Go-side sites where an epoch fence rejects, split by HOW the
		// rejection arrives:
		//
		//   - As pgx.ErrNoRows from a `:one` statement, which is the only shape
		//     an `err != nil` arm can tell apart: handleTaskLog's AppendTaskLog
		//     arm, handleTaskStatus's IncrementTaskRetryCount and
		//     UpdateTaskStatus arms, Watchdog.SweepOnce, this one, and
		//     Dispatcher.sendTask's ClaimTaskForWorker - which CLAUDE.md
		//     names as the canonical "conditionally end the assignment" branch
		//     of the epoch fence.
		//   - As a rowcount or an empty slice, where there is no error to
		//     inspect at all: RequeueTask and RequeueTaskByID (`:execrows`) and
		//     RequeueWorkerTasksIfEpoch (`:many`). A rejection there is
		//     indistinguishable from "there was nothing to requeue", so those
		//     sites do not have this branch to get right and cannot grow one
		//     without a statement change.
		//
		// ErrNoRows means another path ended this assignment between the claim
		// and here - a cancel, a grace requeue, a sibling replica. That is the
		// CORRECT outcome, not a failure, so it is not logged. Any other error
		// is real.
		//
		// THE SILENCE COSTS NO SIGNAL: the unconditional "failing task ...
		// terminally" line above is emitted before this write on every attempt,
		// so a poison task being re-claimed in a loop stays visible through it.
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("dispatch: UpdateTaskStatus(failed) for task %s: %v", uuidStr(claimed.ID), err)
		}
		return
	}
	finalizeTerminalTask(ctx, q, broker, "dispatch", updated, "failed")
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
