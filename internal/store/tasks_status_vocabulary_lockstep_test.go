//go:build integration

package store_test

import (
	"context"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// literalRe pulls the single-quoted literals out of a pg_get_constraintdef
// rendering. Postgres renders an IN list as
// `CHECK (((status)::text = ANY ((ARRAY['pending'::character varying, ...])::text[])))`,
// so matching the quoted values is stable against the casts and the ANY/ARRAY
// spelling in a way that string-comparing the whole definition is not.
var literalRe = regexp.MustCompile(`'([^']*)'`)

// TestTasksStatusVocabularyIsExactly is a LOCKSTEP GUARD, not a behavior test.
// It reads the live tasks_status_check constraint and fails if the vocabulary is
// anything other than the six values migration 000019 pinned.
//
// It exists because the statements listed below hard-code a slice of that
// vocabulary, and adding a seventh status silently desynchronizes all of them at
// once. A task-level `cancelled` is the concrete near-term candidate:
// CancelJobTasks squashes cancellation onto `failed` today, so somebody will
// eventually want the real thing.
//
// WHEN THIS TEST GOES RED, a status was added or removed. Do not just update the
// expected set - visit every one of these first and decide, per site, which side
// of the partition the new status belongs on:
//
//   - UpdateTaskStatus (query/tasks.sql) - `status IN ('pending','dispatched',
//     'running')`. A status omitted here is UNWRITABLE by an agent. That is the
//     fail-closed direction and it is deliberate, but a new non-terminal status
//     that agents must be able to write has to be added or status updates for it
//     are silently dropped.
//   - IncrementTaskRetryCount (query/tasks.sql) - the identical predicate. A
//     status omitted here cannot be retried. A new terminal status MUST stay
//     omitted, or the resurrection bug this predicate closes
//     (bug-2026-06-26-retry-resurrects-cancelled-task) re-opens for it.
//   - CountTerminalTasksForWorker (query/tasks.sql) - `status IN ('done',
//     'failed','timed_out')`, read by handleDeleteWorker BEFORE the DELETE for
//     the delete response's attribution_cleared count. It must remain the exact
//     complement of the assignment-partition group below AMONG ROWS THAT CARRY A
//     worker_id, because the two numbers jointly account for every such row: the
//     requeue rescues the assigned ones, this counts the ones that silently lose
//     worker_id to ON DELETE SET NULL. A new TERMINAL status must be ADDED here
//     or its rows are de-attributed without appearing in the only record the
//     delete produces. A new NON-TERMINAL status must stay out AND be added to
//     the assignment-partition group, or it falls between the two and is neither
//     rescued nor counted - the one gap this pairing cannot self-detect.
//   - RecomputeJobStatus (query/jobs.sql) - counts `('done','failed',
//     'timed_out')` as terminal. This must remain the exact complement of the
//     two predicates above; a status that one side treats as terminal and the
//     other does not is precisely the split-brain that produced that bug.
//   - RetryJobTasks (query/tasks.sql) - `status IN ('failed','timed_out')`, plus
//     'done' when include_done is true. This is the OPERATOR re-run's selection
//     (POST /v1/jobs/{id}/retry). A new TERMINAL status probably belongs in
//     ?task=all's widening, and belongs in ?task=failed's only if it is a
//     failure mode the way timed_out is. A new NON-TERMINAL status must stay out
//     of BOTH modes: this statement clears worker_id and bumps the epoch, so
//     admitting a non-terminal status would let an operator retry evict a live
//     agent. The same statement's dependents guard reads
//     `dep.status <> 'pending'` - a negation, because there the predicate
//     authorizes BLOCKING, so a new status must block. Do not "fix" it into an
//     allow-list.
//   - SelectRetryableTaskIDs (query/tasks.sql) - the unguarded twin of that
//     selection, used only to classify the endpoint's three 409s. Its status
//     predicate must stay byte-identical to RetryJobTasks's; change both or
//     neither.
//   - AppendTaskLog (query/tasks.sql) - `status IN ('pending','dispatched',
//     'running')` as the FIRST ARM of a disjunction with a finished_at window.
//     READ THIS SITE BACKWARDS FROM THE FIVE ABOVE. There, a new status must
//     usually stay OUT and the omission fails closed harmlessly - an unwritable
//     status, an unretryable task. HERE THE OMISSION IS CATASTROPHIC AND SILENT:
//     a new NON-TERMINAL status left out of this arm drops 100% of that state's
//     log output, because a non-terminal row has finished_at IS NULL and so
//     fails the second arm too, and the drop produces no error and no log line
//     anywhere. A new NON-TERMINAL status MUST BE ADDED here.
//     TASK_STATUS_PREPARING already exists in proto/relayv1/relay.proto and the
//     agent already streams prepare progress as LOG_STREAM_PREPARE chunks
//     (internal/agent/runner.go), so it is the concrete candidate - and its twin
//     TASK_STATUS_PREPARE_FAILED needs the OPPOSITE call. A new TERMINAL status
//     must stay OUT and is then bounded by finished_at like done/failed/
//     timed_out. Never conjoin this arm with the rest of the fence: that closes
//     the trailing flush.
//   - ListOverdueAssignedTasks (query/tasks.sql) - `status IN ('dispatched',
//     'running')`, the "currently assigned" partition the coordinator's
//     stale-task watchdog scans. READ THIS SITE BACKWARDS TOO: it is the SECOND
//     inverted one. A new NON-TERMINAL status omitted here is NEVER SWEPT, which
//     silently reopens the unbounded-assignment hole this statement exists to
//     close, for that status - a task in it could hold its worker slot and its
//     job forever with no error and no log line. `preparing` is the same live
//     candidate as for AppendTaskLog and would need adding to BOTH. A new
//     TERMINAL status must stay OUT - but NOT because it would resurrect
//     anything: this statement is read-only, and UpdateTaskStatus's own
//     allow-list would reject the write regardless. Including one simply buys a
//     guaranteed zero-row round trip on every sweep, forever.
//   - THE ASSIGNMENT-PARTITION GROUP - GetActiveTasksForWorker,
//     ListGraceCandidates, RequeueTaskByID, RequeueWorkerTasks and
//     RequeueWorkerTasksIfEpoch (query/tasks.sql), all carrying the identical
//     `status IN ('dispatched','running')`. THESE ARE INVERTED, exactly like
//     ListOverdueAssignedTasks, and they were missing from this list until the
//     RequeueTaskByID fence slice - which means five of the eight sites that
//     slice the "currently assigned" partition were unlisted while the one that
//     motivated the warning was spelled out at length.
//     A new NON-TERMINAL status omitted here fails OPEN in the damaging
//     direction at every one of them at once. Trace `preparing`, this file's own
//     named candidate: a task sitting in it through a long P4 sync is invisible
//     to GetActiveTasksForWorker, so reconcile never sees it and never requeues
//     it; invisible to ListGraceCandidates, so no grace timer covers it;
//     unmatched by all three requeue statements, so neither a disconnect nor an
//     admin disable releases it; and already unswept by
//     ListOverdueAssignedTasks. It holds its worker slot and its job FOREVER,
//     with no error and no log line - and it is outside idx_tasks_worker_active
//     as well. A new non-terminal status MUST BE ADDED to all five. A new
//     TERMINAL status must stay OUT: the three requeue statements write, so
//     admitting one would let a requeue resurrect a finished task, which is the
//     guarantee TestRequeueTaskByID_TerminalTaskIsNotResurrected pins.
//     The positive arm is pinned too, and needs to stay pinned:
//     TestRequeueTaskByID_RequeuesARunningTaskForItsAssignee exists because
//     halving that IN list to ('dispatched') left the store, worker, scheduler
//     and api suites ALL GREEN while silently stranding reconcile's dominant
//     case.
//   - RequeueTask (query/tasks.sql) - `status = 'dispatched'`, and the one
//     member of the requeue family that is DELIBERATELY NARROWER than the group
//     above. Do not "harmonize" it by adding 'running'. Its only caller is the
//     dispatcher's send-failure path, where the dispatch never reached the agent,
//     so the task cannot be running for any agent that reports only epochs it was
//     actually dispatched; the full argument is in the statement's own comment.
//     TestRequeueTask_RunningTaskIsNotRequeuedByTheSendFailurePath pins it. A new
//     status belongs here only if a task can be in it while its DispatchTask is
//     still in flight.
//
// THE FOURTEENTH ENTRY IS NOT A STATEMENT, and it is here because the list above
// presented itself as complete while something else sliced the same set:
//
//   - taskStatusIsWritable (internal/worker/taskstatus_fence_counters.go) - A GO
//     -SIDE MIRROR, not SQL. It restates UpdateTaskStatus's and
//     IncrementTaskRetryCount's `('pending','dispatched','running')` in Go so
//     that handleTaskStatus can label a fence rejection `raced` versus
//     `duplicate`/`conflicting` without a second round trip. It is the ONLY site
//     on this list that decides nothing: it labels a counter, so drift mislabels
//     a number and cannot admit or refuse a write. A new NON-TERMINAL status
//     belongs here whenever it is added to those two allow-lists, or every
//     rejection for such a row reads as a healthy race and the actionable key
//     goes quiet for that state. It is covered TRANSITIVELY today -
//     TestTaskStatusWritableSetMatchesTheSQLAllowList parses tasks.sql and
//     compares both directions, so moving the SQL turns it RED without anyone
//     consulting this list - which is exactly why it was easy to leave off, and
//     exactly why a completeness claim has to name it anyway. A claim about the
//     complement cannot be checked by opening its subject.
//
// The allow-list form of these predicates is what makes this guard the only
// thing standing between a new status and a silent regression: under the
// equivalent deny-list a new status would be writable and retryable by default,
// and this test would be the last chance to notice. AppendTaskLog and the whole
// "currently assigned" partition - ListOverdueAssignedTasks plus the
// assignment-partition group - are where the allow-list points the OTHER way and
// omission fails open, which is why they are spelled out at length above rather
// than folded into the list. Count them before assuming the fail-closed default:
// seven of the thirteen STATEMENTS named here are inverted. The fourteenth entry
// is not a statement and is not one of the seven; it is neither fail-open nor
// fail-closed, because it gates nothing at all.
func TestTasksStatusVocabularyIsExactly(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var def string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'tasks_status_check'`,
	).Scan(&def), "tasks_status_check must exist; migration 000019 adds it")

	var got []string
	for _, m := range literalRe.FindAllStringSubmatch(def, -1) {
		got = append(got, m[1])
	}
	sort.Strings(got)

	want := []string{"dispatched", "done", "failed", "pending", "running", "timed_out"}
	require.Equal(t, want, got,
		"tasks.status vocabulary changed - read this test's comment before updating it. These statements slice "+
			"this set: UpdateTaskStatus, IncrementTaskRetryCount, RecomputeJobStatus, RetryJobTasks, "+
			"SelectRetryableTaskIDs, AppendTaskLog, ListOverdueAssignedTasks, GetActiveTasksForWorker, "+
			"ListGraceCandidates, RequeueTask, RequeueTaskByID, RequeueWorkerTasks and RequeueWorkerTasksIfEpoch. "+
			"Revisit ALL OF THEM. SEVEN fail OPEN in the damaging direction. A new NON-TERMINAL status omitted "+
			"from AppendTaskLog's first arm silently discards 100% of that state's log output. One omitted from "+
			"the six that carry the 'currently assigned' partition - ListOverdueAssignedTasks, "+
			"GetActiveTasksForWorker, ListGraceCandidates, RequeueTaskByID, RequeueWorkerTasks and "+
			"RequeueWorkerTasksIfEpoch - means a task in that state is never seen by reconcile, never covered by "+
			"a grace timer, never requeued on disconnect or disable, and never swept: it holds its worker slot "+
			"and its job forever, with no error and no log line. RequeueTask's narrower 'dispatched'-only "+
			"predicate is deliberate - see its own comment before touching it. THERE IS ALSO ONE NON-STATEMENT "+
			"SITE: taskStatusIsWritable in internal/worker/taskstatus_fence_counters.go mirrors "+
			"UpdateTaskStatus's allow-list in Go to label fence-rejection counters. It gates nothing, so drift "+
			"there mislabels a number rather than admitting a write - but a new non-terminal status left out of "+
			"it makes every rejection for that state read as a healthy race")
}
