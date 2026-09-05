//go:build integration

package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// createScheduleCapToken mints a real api_tokens row and returns the raw hex the
// client presents. Real tokens rather than a stub admin DB: the cap's subject is
// a per-OWNER count, so every request in this file has to carry a distinct, real
// users row.
func createScheduleCapToken(t *testing.T, q *store.Queries, userID pgtype.UUID) string {
	t.Helper()
	raw := make([]byte, 16)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	rawHex := hex.EncodeToString(raw)
	_, err = q.CreateToken(t.Context(), store.CreateTokenParams{
		UserID:    userID,
		TokenHash: tokenhash.Hash(rawHex),
		ExpiresAt: pgtype.Timestamptz{},
	})
	require.NoError(t, err)
	return rawHex
}

// postSchedule drives one POST /v1/scheduled-jobs through the real http.Server
// buildHTTPServer returned, against a real Postgres.
func postSchedule(t *testing.T, srv *http.Server, token, name string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(
		`{"name":%q,"cron_expr":"@hourly","job_spec":{"name":"j","tasks":[{"name":"t","command":["echo","x"]}]}}`,
		name)
	req := httptest.NewRequest("POST", "/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// TestScheduleCap_TheThirdCreateIsRefusedAtACapOfTwo pins that the cap exists
// and that it uses the number buildHTTPServer was GIVEN.
//
// THE THIRD REQUEST IS NOT OPTIONAL. Two successes under a cap of two are also
// exactly what an ABSENT control produces, so without the refusal this test is
// vacuous against the implementation it describes.
//
// EVERY OTHER LIMIT FIELD IS LEFT AT ZERO, and that is load-bearing rather than
// incidental: a crossed assignment in buildHTTPServer
// (s.MaxSchedulesPerOwner = d.jobSubmitLimitN) then produces a zero, which the
// resolver folds to DefaultMaxSchedulesPerOwner, so the third create SUCCEEDS
// and this test is RED. A plausible non-zero number in a sibling field would let
// a crossed assignment pass.
func TestScheduleCap_TheThirdCreateIsRefusedAtACapOfTwo(t *testing.T) {
	pool, q := newPgdsnPoolAndQueries(t)
	user := createUserWithTestPassword(t, q, "Capped", "cap-third@example.com", false)
	token := createScheduleCapToken(t, q, user.ID)

	srv := buildHTTPServer(httpServerDeps{
		addr:                 "127.0.0.1:0",
		pool:                 pool,
		q:                    q,
		maxSchedulesPerOwner: 2,
	})

	for i := 1; i <= 2; i++ {
		rec := postSchedule(t, srv, token, fmt.Sprintf("under-the-cap-%d", i))
		require.Equal(t, http.StatusCreated, rec.Code,
			"create %d of 2 must be admitted under a cap of 2. body: %s", i, rec.Body.String())
	}

	over := postSchedule(t, srv, token, "over-the-cap")
	require.Equal(t, http.StatusConflict, over.Code,
		"the third create must be refused with 409 by the cap buildHTTPServer was GIVEN. A missing "+
			"check, `n > cap` instead of `n >= cap`, a hard-coded default, and a deleted or crossed "+
			"s.MaxSchedulesPerOwner assignment all answer 201 here. body: %s", over.Body.String())
	require.Contains(t, over.Body.String(), "Delete a scheduled job before creating another",
		"the refusal must carry the self-service remedy, which is the only remedy an actor who can "+
			"drive this refusal should be told about")

	// A refused create must roll back, not leave a row the count then charges
	// the owner for.
	//
	// THIS IS A SECOND GUARD, NOT A FLOURISH, and it is the only thing that sees
	// a check moved below tx.Commit: that mutant still answers 409 above, and
	// leaves the row behind. Moving the check below the INSERT but above the
	// commit is instead behaviourally EQUIVALENT - the deferred Rollback discards
	// the row - so no assertion here distinguishes it, and none should pretend to.
	var n int64
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM scheduled_jobs WHERE owner_id = $1`, user.ID).Scan(&n))
	require.Equal(t, int64(2), n, "a refused create must write nothing")
}

// TestScheduleCap_AnAdminIsRefusedExactlyAsANonAdminIs pins that there is no
// admin exemption.
//
// THE ADMIN CASE RUNS FIRST, so an early-exit exemption
// (`if u.IsAdmin { skip the check }`) cannot pass by never being reached: a
// decoy placed after its target is read by neither the code nor the mutant.
//
// The refused request is in each arm for the same reason as the sibling test
// above: two successes under a cap of two are also what an absent control
// produces, so only the 409 distinguishes "the admin is subject to the cap"
// from "nobody is".
//
// Both owners share one server and one cap, which is what makes this a
// statement about the CHECK rather than about two independently configured
// servers.
func TestScheduleCap_AnAdminIsRefusedExactlyAsANonAdminIs(t *testing.T) {
	pool, q := newPgdsnPoolAndQueries(t)
	admin := createUserWithTestPassword(t, q, "Admin", "cap-admin@example.com", true)
	plain := createUserWithTestPassword(t, q, "Plain", "cap-plain@example.com", false)
	adminToken := createScheduleCapToken(t, q, admin.ID)
	plainToken := createScheduleCapToken(t, q, plain.ID)

	srv := buildHTTPServer(httpServerDeps{
		addr:                 "127.0.0.1:0",
		pool:                 pool,
		q:                    q,
		maxSchedulesPerOwner: 2,
	})

	for _, tc := range []struct {
		who   string
		token string
	}{
		{"admin", adminToken},
		{"non-admin", plainToken},
	} {
		t.Run(tc.who, func(t *testing.T) {
			for i := 1; i <= 2; i++ {
				rec := postSchedule(t, srv, tc.token, fmt.Sprintf("%s-under-%d", tc.who, i))
				require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
			}
			over := postSchedule(t, srv, tc.token, tc.who+"-over")
			require.Equal(t, http.StatusConflict, over.Code,
				"%s must be refused at the cap. An exemption for admins would carve a hole in a "+
					"control everyone else is subject to, for the population most likely to be "+
					"running the automation that fills the table, and the boot sweep does not care "+
					"whose rows they are. body: %s", tc.who, over.Body.String())
		})
	}
}
