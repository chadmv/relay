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
// It exists because six statements in this repo hard-code a slice of that
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
//
// The allow-list form of the two predicates is what makes this guard the only
// thing standing between a new status and a silent regression: under the
// equivalent deny-list a new status would be writable and retryable by default,
// and this test would be the last chance to notice.
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
		"tasks.status vocabulary changed - read this test's comment before updating it; "+
			"UpdateTaskStatus, IncrementTaskRetryCount and RecomputeJobStatus all partition this set")
}
