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
// It exists because three places in this repo hard-code a partition of that
// vocabulary, and adding a seventh status silently desynchronizes all three at
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
