//go:build integration

package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// TestScheduledJobCap_AnOverCapOwnerKeepsEveryRouteButCreate pins the
// grandfathering decision: owners already over the cap keep every row and lose
// exactly one capability.
//
// The alternatives were REJECTED for reasons this test makes executable.
// Deleting the excess destroys an operator's configuration on a rule they have
// never seen, with no undo; disabling it silently stops production work and
// teaches operators that enabled=false means nothing.
//
// THE POST ARM IS NOT OPTIONAL. Five green "unchanged" arms are also what a
// server with no cap at all produces, so the refusal is what makes the other
// five mean "grandfathered" rather than "nothing was implemented".
//
// PATCH AND RUN-NOW ARE THE ARMS THAT MOST WANT THIS GUARD: they are where a
// later consistency edit would add a check, and a PATCH that refuses is a PATCH
// that can refuse an owner's attempt to REPAIR a broken schedule. DELETE is the
// self-service remedy the refusal message names, so it must never be refused.
//
// LANE: internal/api integration, which no CI job runs today. It needs a real
// Postgres for the count and the lock, and it would start running in CI
// unchanged if internal/api gains a lane.
func TestScheduledJobCap_AnOverCapOwnerKeepsEveryRouteButCreate(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	srv.MaxSchedulesPerOwner = 2
	user := createTestUser(t, q, "Legacy", "cap-grandfathered@example.com", false)
	token := createTestToken(t, q, user.ID)

	// cap + 3 rows planted through the store, which is exactly how an over-cap
	// owner exists on the deploy that lands the cap.
	ctx := context.Background()
	var planted []store.ScheduledJob
	for i := 0; i < 5; i++ {
		row, err := q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
			Name: fmt.Sprintf("legacy-%d", i), OwnerID: user.ID, CronExpr: "@hourly", Timezone: "UTC",
			JobSpec:       []byte(`{"name":"j","tasks":[{"name":"t","command":["echo","x"]}]}`),
			OverlapPolicy: "skip", Enabled: false,
			NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		})
		require.NoError(t, err)
		planted = append(planted, row)
	}

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	id := uuidString(planted[0].ID)

	t.Run("list still returns every row", func(t *testing.T) {
		rec := do("GET", "/v1/scheduled-jobs", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "legacy-4")
	})

	t.Run("get still returns one row", func(t *testing.T) {
		rec := do("GET", "/v1/scheduled-jobs/"+id, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("patch succeeds, INCLUDING enabling", func(t *testing.T) {
		rec := do("PATCH", "/v1/scheduled-jobs/"+id, `{"enabled":true,"name":"repaired"}`)
		require.Equal(t, http.StatusOK, rec.Code,
			"PATCH must never enforce the cap: it cannot increase the count, and a refusal here "+
				"would block an over-cap owner from repairing or disabling a schedule. body: %s",
			rec.Body.String())
	})

	t.Run("run-now succeeds", func(t *testing.T) {
		rec := do("POST", "/v1/scheduled-jobs/"+id+"/run-now", "")
		require.Equal(t, http.StatusCreated, rec.Code,
			"run-now creates a JOB from a stored spec and no scheduled_jobs row at all, so it is "+
				"neither an enforcement point nor an evasion. body: %s", rec.Body.String())
	})

	t.Run("delete succeeds - it is the remedy the refusal names", func(t *testing.T) {
		rec := do("DELETE", "/v1/scheduled-jobs/"+uuidString(planted[4].ID), "")
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	})

	t.Run("only POST is refused", func(t *testing.T) {
		rec := do("POST", "/v1/scheduled-jobs",
			`{"name":"one-more","cron_expr":"@hourly","job_spec":{"name":"j","tasks":[{"name":"t","command":["echo","x"]}]}}`)
		require.Equal(t, http.StatusConflict, rec.Code,
			"the ONE thing an over-cap owner loses is the ability to create another schedule. "+
				"Without this arm the five green arms above are also what a server with no cap "+
				"produces. body: %s", rec.Body.String())
	})

	// AND NOTHING WAS DESTROYED. The cap does not shrink an existing table by one
	// row; four remain after the one deliberate DELETE. The raw count is the
	// assertion no handler bug can satisfy.
	var n int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM scheduled_jobs WHERE owner_id = $1`, user.ID).Scan(&n))
	require.Equal(t, int64(4), n,
		"grandfathering means nothing is deleted, disabled or flagged by the cap itself")
}
