//go:build integration

package store_test

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInsertWorkerForAutoEnroll_RefusesASecondRowForTheSameHostname proves the
// statement's ENTIRE refusal signal against real Postgres.
//
// EVERYTHING ABOVE THIS RESTS ON ONE UNVERIFIED ASSUMPTION UNTIL THIS TEST
// EXISTS: that `ON CONFLICT (hostname) DO NOTHING RETURNING id`, emitted by sqlc
// as a :one, surfaces a conflict as pgx.ErrNoRows rather than as a nil id, a
// zero-value UUID, or some other error. autoEnrollAndRegister's create-only
// guard is exactly `errors.Is(err, pgx.ErrNoRows) -> refuse`, so if that
// assumption were wrong the guard would fall through to the generic error arm
// and a hostname takeover would present as a 500. It was previously proven only
// against the package's own fake plus one end-to-end path.
func TestInsertWorkerForAutoEnroll_RefusesASecondRowForTheSameHostname(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	params := store.InsertWorkerForAutoEnrollParams{
		Name: "auto-1", Hostname: "auto-enroll-conflict-host",
		CpuCores: 4, RamGb: 8, Os: "linux",
	}

	id, err := q.InsertWorkerForAutoEnroll(ctx, params)
	require.NoError(t, err, "a hostname with no row must insert")
	require.True(t, id.Valid, "the first insert must return a real id")

	// Second attempt on the same hostname, with DIFFERENT hardware, which is what
	// a claimant would send.
	second := params
	second.Name = "claimant"
	second.CpuCores = 64
	second.RamGb = 512

	_, err = q.InsertWorkerForAutoEnroll(ctx, second)
	require.Error(t, err, "a hostname that already has a row must be refused")
	assert.ErrorIs(t, err, pgx.ErrNoRows,
		"DO NOTHING returns no row on conflict, and errors.Is(err, pgx.ErrNoRows) is the whole of "+
			"autoEnrollAndRegister's create-only guard - any other error shape makes a takeover attempt a 500")

	// AND IT WROTE NOTHING. DO NOTHING must not have updated the hardware specs,
	// which is what distinguishes it from the DO UPDATE upsert it replaced.
	w, err := q.GetWorkerByHostname(ctx, "auto-enroll-conflict-host")
	require.NoError(t, err)
	assert.Equal(t, id, w.ID, "the existing row's identity must be untouched")
	assert.Equal(t, "auto-1", w.Name)
	assert.Equal(t, int32(4), w.CpuCores, "DO NOTHING must not refresh hardware specs")
	assert.Equal(t, int32(8), w.RamGb)
}

// TestInsertWorkerForAutoEnroll_RefusesARevokedHostnameToo pins that the
// conflict is on the HOSTNAME and consults no status at all - the property that
// replaced a deny-list on `status = 'revoked'` which failed open on every status
// added to the vocabulary.
func TestInsertWorkerForAutoEnroll_RefusesARevokedHostnameToo(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	id, err := q.InsertWorkerForAutoEnroll(ctx, store.InsertWorkerForAutoEnrollParams{
		Name: "auto-2", Hostname: "auto-enroll-revoked-host", CpuCores: 4, RamGb: 8, Os: "linux",
	})
	require.NoError(t, err)

	_, err = q.ClearWorkerAgentToken(ctx, id)
	require.NoError(t, err)
	revoked, err := q.GetWorkerByHostname(ctx, "auto-enroll-revoked-host")
	require.NoError(t, err)
	require.Equal(t, "revoked", revoked.Status)

	_, err = q.InsertWorkerForAutoEnroll(ctx, store.InsertWorkerForAutoEnrollParams{
		Name: "auto-2", Hostname: "auto-enroll-revoked-host", CpuCores: 4, RamGb: 8, Os: "linux",
	})
	assert.ErrorIs(t, err, pgx.ErrNoRows,
		"the conflict is on the hostname and consults no status: revoking frees ceiling BUDGET, "+
			"never the HOSTNAME, which is why the documented recovery is revoke-then-enrollment-token "+
			"or delete, and never revoke-then-retry-token-lessly")
}

// TestCountWorkers_RevokingFreesCeilingBudgetWithoutFreeingTheHostname is
// README's remedy 1 as a property, and it is the pair of facts an operator at
// the ceiling has to hold at once. CountWorkers is the ceiling predicate in
// autoEnrollAndRegister, so the first half is what makes `relay workers revoke`
// work as a remedy at all; the second half is why it is not the WHOLE remedy.
func TestCountWorkers_RevokingFreesCeilingBudgetWithoutFreeingTheHostname(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	before, err := q.CountWorkers(ctx)
	require.NoError(t, err)

	id, err := q.InsertWorkerForAutoEnroll(ctx, store.InsertWorkerForAutoEnrollParams{
		Name: "budget", Hostname: "auto-enroll-budget-host", CpuCores: 4, RamGb: 8, Os: "linux",
	})
	require.NoError(t, err)

	withRow, err := q.CountWorkers(ctx)
	require.NoError(t, err)
	require.Equal(t, before+1, withRow, "a new worker must consume one unit of ceiling budget")

	_, err = q.ClearWorkerAgentToken(ctx, id)
	require.NoError(t, err)

	afterRevoke, err := q.CountWorkers(ctx)
	require.NoError(t, err)
	assert.Equal(t, before, afterRevoke,
		"revoking must FREE ceiling budget - CountWorkers excludes revoked workers, and that exclusion "+
			"is what makes revoke a non-destructive remedy rather than requiring delete")

	// The row and its hostname are still there. This is why README says the total
	// row count is NOT bounded even though the counted total is.
	still, err := q.GetWorkerByHostname(ctx, "auto-enroll-budget-host")
	require.NoError(t, err)
	assert.Equal(t, id, still.ID,
		"revoking frees budget by EXCLUDING the row, not by removing it: the hostname stays claimed "+
			"and the table can keep growing while CountWorkers stays flat")
}
