//go:build integration

package store_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// TestDeleteWorker_PermitsExactlyTheDisconnectedStatuses is the ALLOW-LIST test
// and the SQL arm's OWN kill (spec 8.2): handleDeleteWorker's Go status check
// does not exist at this layer, so nothing here can pass because of it. 'online'
// and 'stale' both mean CONNECTED - internal/scheduler/dispatch.go:210-215 says a
// stale worker is still connected and able to run tasks - so the permitted set is
// exactly the not-connected set.
func TestDeleteWorker_PermitsExactlyTheDisconnectedStatuses(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	q := store.New(pool)

	// 'stale' IS FIRST ON PURPOSE: it is the row that kills the tempting deny-list
	// `status != 'online'` (M5), and a poisoned input placed last cannot detect an
	// early-exit mutation.
	cases := []struct {
		status  string
		deleted bool
	}{
		{"stale", false},
		{"online", false},
		{"offline", true},
		{"revoked", true},
	}

	// LOCKSTEP: the table must enumerate the WHOLE vocabulary, so a fifth status
	// cannot appear without somebody deciding whether it is deletable - the job
	// TestTasksStatusVocabularyIsExactly does for tasks. literalRe is declared in
	// tasks_status_vocabulary_lockstep_test.go, same package, and is reused.
	var def string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'workers_status_check'`,
	).Scan(&def), "workers_status_check must exist; migration 000019 adds it")
	var vocab, covered []string
	for _, m := range literalRe.FindAllStringSubmatch(def, -1) {
		vocab = append(vocab, m[1])
	}
	for _, c := range cases {
		covered = append(covered, c.status)
	}
	sort.Strings(vocab)
	sort.Strings(covered)
	require.Equal(t, vocab, covered,
		"workers.status vocabulary changed - DeleteWorker's allow-list is ('offline','revoked'). "+
			"Decide which side the new status belongs on before updating this table (spec 8.1)")

	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			w := newTestWorker(t, q) // hostname derives from t.Name(), unique per subtest
			_, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{ID: w.ID, Status: c.status})
			require.NoError(t, err)

			n, err := q.DeleteWorker(ctx, w.ID)
			require.NoError(t, err)
			if c.deleted {
				require.Equal(t, int64(1), n, "%s is disconnected and must be deletable", c.status)
				_, err = q.GetWorker(ctx, w.ID)
				require.ErrorIs(t, err, pgx.ErrNoRows, "the row must be gone")
				return
			}
			require.Equal(t, int64(0), n, "%s means CONNECTED and must be refused", c.status)
			_, err = q.GetWorker(ctx, w.ID)
			require.NoError(t, err, "a refused delete must leave the row untouched")
		})
	}
}

// TestGetWorkerForUpdate_LocksAnExistingRowAndDistinguishesAMissingOne pins the
// 404 discrimination the handler makes INSIDE the transaction (spec 6.3 step 1).
func TestGetWorkerForUpdate_LocksAnExistingRowAndDistinguishesAMissingOne(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	w := newTestWorker(t, q)

	got, err := q.GetWorkerForUpdate(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, w.ID, got.ID)
	require.Equal(t, w.Hostname, got.Hostname)

	_, err = q.GetWorkerForUpdate(ctx, pgtype.UUID{Valid: true})
	require.ErrorIs(t, err, pgx.ErrNoRows, "a missing worker must be pgx.ErrNoRows, never a zero-value row")
}

// TestDeleteWorker_IsRefusedWhileAnEnrollmentNamesTheWorker proves DECISION A's
// PREMISE, which is the whole reason the spec declined a migration:
// agent_enrollments.consumed_by's FK has NO ON DELETE action, so it fails CLOSED
// with SQLSTATE 23503 for any future deleter that forgets to unlink. If this goes
// green, somebody added ON DELETE SET NULL and the guard is gone.
func TestDeleteWorker_IsRefusedWhileAnEnrollmentNamesTheWorker(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	admin := newTestUser(t, q, true)
	w := newTestWorker(t, q)
	_, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{ID: w.ID, Status: "offline"})
	require.NoError(t, err)

	e, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "hash-" + t.Name(), CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
	rows, err := q.ConsumeAgentEnrollment(ctx, store.ConsumeAgentEnrollmentParams{ID: e.ID, ConsumedBy: w.ID})
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)

	_, err = q.DeleteWorker(ctx, w.ID)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23503", pgErr.Code, "the FK must fail closed for a deleter that did not unlink")

	n, err := q.ClearEnrollmentConsumerForWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "the count is what the delete response reports")

	deleted, err := q.DeleteWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	after, err := q.GetAgentEnrollmentByTokenHash(ctx, "hash-"+t.Name())
	require.NoError(t, err, "the enrollment row must survive the worker")
	require.True(t, after.ConsumedAt.Valid, "consumed_at must be intact")
	require.False(t, after.ConsumedBy.Valid, "consumed_by must be NULL")
}

func TestRemoveWorkerFromReservations_ScrubsOnlyTheReservationsThatNameIt(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	user := newTestUser(t, q, true)
	target := newTestWorker(t, q)
	other, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "other-" + t.Name(), Hostname: "other-" + t.Name(),
		CpuCores: 4, RamGb: 16, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	mk := func(name string, ids []pgtype.UUID) store.Reservation {
		r, err := q.CreateReservation(ctx, store.CreateReservationParams{
			Name: name, Selector: []byte("{}"), WorkerIds: ids, UserID: user.ID,
		})
		require.NoError(t, err)
		return r
	}
	// THE MIXED RESERVATION IS FIRST. A single-worker fixture passes against
	// `SET worker_ids = '{}'`; the mixed row is what forces array_remove
	// semantics, and it must not sit behind a benign row where an early-exit
	// mutation could hide.
	mixed := mk("mixed", []pgtype.UUID{target.ID, other.ID})
	only := mk("only", []pgtype.UUID{target.ID})
	none := mk("none", []pgtype.UUID{other.ID})

	n, err := q.RemoveWorkerFromReservations(ctx, target.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2), n,
		"the count must be how many reservations NAMED this worker - the WHERE clause is what makes it mean that")

	got, err := q.GetReservation(ctx, mixed.ID)
	require.NoError(t, err)
	require.Equal(t, []pgtype.UUID{other.ID}, got.WorkerIds, "array_remove must keep the other id")

	got, err = q.GetReservation(ctx, only.ID)
	require.NoError(t, err)
	require.Empty(t, got.WorkerIds)

	got, err = q.GetReservation(ctx, none.ID)
	require.NoError(t, err)
	require.Equal(t, []pgtype.UUID{other.ID}, got.WorkerIds, "an unrelated reservation must be untouched")
}
