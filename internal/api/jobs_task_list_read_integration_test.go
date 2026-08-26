//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var errInjectedQueryFailure = errors.New("injected query failure")

// failOneQueryDB is a store.DBTX that delegates every statement to the pool
// except the one sqlc query it is named for, which always fails. It matches on
// the `-- name: <Name> :` header sqlc emits at the top of every generated query
// constant, so the selector is the query's own name rather than a fragment of
// its SQL.
//
// A trigger cannot express this. installFailDeleteTrigger works because a
// DELETE can be intercepted in the database; the statement under test here is a
// SELECT, and the test role is the container's superuser so REVOKE does not
// bind. This seam also keeps the failure to ONE statement: BearerAuth and
// GetJobWithEmail run through the same *store.Queries and must still succeed,
// or the request never reaches the line under test.
type failOneQueryDB struct {
	pool *pgxpool.Pool
	name string
}

func (d failOneQueryDB) fails(sql string) bool {
	return strings.Contains(sql, "-- name: "+d.name+" :")
}

func (d failOneQueryDB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if d.fails(sql) {
		return pgconn.CommandTag{}, errInjectedQueryFailure
	}
	return d.pool.Exec(ctx, sql, args...)
}

func (d failOneQueryDB) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	if d.fails(sql) {
		return nil, errInjectedQueryFailure
	}
	return d.pool.Query(ctx, sql, args...)
}

func (d failOneQueryDB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	if d.fails(sql) {
		return failedRow{}
	}
	return d.pool.QueryRow(ctx, sql, args...)
}

type failedRow struct{}

func (failedRow) Scan(dest ...any) error { return errInjectedQueryFailure }

// handleGetJob discarded ListTasksByJob's error, and it was the only read on
// that handler that did - every other one 500s. So a transient failure of that
// one statement (pool exhaustion, statement timeout, a cancelled context)
// answered 200 with `tasks` absent, because jobResponse's tasks field is
// omitempty.
//
// That is the one failure shape a client cannot tell from a real answer, and
// relay logs' final reconcile was built on top of it: it iterated the empty
// list, set nothing, and reported the job fully reconciled - exit 0, nothing on
// either stream, which is the production symptom this whole slice exists to fix.
// A 500 is distinguishable; a silently task-less 200 is not.
func TestGetJob_TaskListReadFails_IsAnError(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	healthy := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	user := createTestUser(t, q, "Tasklist", "tasklist-fail@test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, healthy, token)

	// Same pool, same data; only ListTasksByJob is broken.
	crippled := api.New(pool, store.New(failOneQueryDB{pool: pool, name: "ListTasksByJob"}),
		events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	req := httptest.NewRequest("GET", "/v1/jobs/"+jobID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	crippled.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"a task list that could not be read must not be reported as a job with no tasks")

	// And the healthy server still answers with the task, so the injection is
	// proven to be the failure under test and not a broken fixture.
	req = httptest.NewRequest("GET", "/v1/jobs/"+jobID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	healthy.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Tasks []map[string]any `json:"tasks"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Tasks, 1)
}
