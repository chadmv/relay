//go:build integration

package store_test

import (
	"context"
	"sort"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
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
