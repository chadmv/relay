//go:build integration

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/jobspec"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
)

// startRelayForMCP spins up a Postgres testcontainer, runs migrations, creates the
// api.Server wrapped in an httptest.Server, and seeds an admin and a non-admin user.
// It returns the base URL, admin bearer token, non-admin bearer token, and a teardown func.
func startRelayForMCP(t *testing.T) (baseURL, adminToken, userToken string, teardown func()) {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("relay_test"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	migrateDSN := "pgx5" + dsn[len("postgres"):]
	require.NoError(t, store.Migrate(migrateDSN))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	q := store.New(pool)
	broker := events.NewBroker()
	registry := worker.NewRegistry()
	apiSrv := api.New(pool, q, broker, registry, nil, 0, 0, 0, 0)

	httpSrv := httptest.NewServer(apiSrv.Handler())

	adminToken = seedAndLogin(t, httpSrv.URL, q, "admin@relay-mcp-test.com", "adminpassword1", true)
	userToken = seedAndLogin(t, httpSrv.URL, q, "user@relay-mcp-test.com", "userpassword1", false)

	teardown = func() {
		httpSrv.Close()
		pool.Close()
		_ = pg.Terminate(ctx)
	}
	return httpSrv.URL, adminToken, userToken, teardown
}

// seedAndLogin creates a user directly via the store using bcrypt.MinCost, then
// logs in via the HTTP API and returns the bearer token.
func seedAndLogin(t *testing.T, baseURL string, q *store.Queries, email, password string, isAdmin bool) string {
	t.Helper()
	ctx := context.Background()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)

	_, err = q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name:         email,
		Email:        email,
		IsAdmin:      isAdmin,
		PasswordHash: string(hash),
	})
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := http.Post(baseURL+"/v1/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var loginResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&loginResp))
	token, ok := loginResp["token"].(string)
	require.True(t, ok, "login response must include a token string")
	return token
}

// TestIntegration_Whoami verifies that callWhoami returns the admin user identity
// with is_admin == true.
func TestIntegration_Whoami(t *testing.T) {
	baseURL, adminToken, _, teardown := startRelayForMCP(t)
	defer teardown()

	s, err := NewServer(baseURL, adminToken)
	require.NoError(t, err)

	out, terr := s.callWhoami(context.Background())
	require.Nil(t, terr)
	assert.Equal(t, "admin@relay-mcp-test.com", out["email"])
	assert.Equal(t, true, out["is_admin"])
	assert.Equal(t, baseURL, out["server_url"])
	assert.NotEmpty(t, out["user_id"])
}

// TestIntegration_SubmitWaitLogs submits a trivial job, waits with a short timeout
// (no worker, so the job stays pending), then calls get_task_logs. Verifies the
// API surface works end-to-end without a worker agent.
func TestIntegration_SubmitWaitLogs(t *testing.T) {
	baseURL, adminToken, _, teardown := startRelayForMCP(t)
	defer teardown()

	s, err := NewServer(baseURL, adminToken)
	require.NoError(t, err)
	// Use a very short poll interval so the wait loop doesn't block long.
	s.waitPoll = 100 * time.Millisecond

	spec := jobspec.JobSpec{
		Name: "integration-test-job",
		Tasks: []jobspec.TaskSpec{
			{Name: "say-hi", Command: []string{"echo", "hi"}},
		},
	}

	// Submit.
	submitOut, terr := s.callSubmitJob(context.Background(), submitJobArgs{JobSpec: spec})
	require.Nil(t, terr, "callSubmitJob failed: %v", terr)
	jobID, ok := submitOut["job_id"].(string)
	require.True(t, ok, "job_id must be a string")
	assert.NotEmpty(t, jobID)

	// Wait with a very short timeout; without a worker the job stays pending.
	waitOut, terr := s.callWaitForJob(context.Background(), waitForJobArgs{
		JobID:          jobID,
		TimeoutSeconds: 2,
	})
	require.Nil(t, terr, "callWaitForJob failed: %v", terr)
	// Either the job completed (unlikely without a worker) or we timed out.
	timedOut, _ := waitOut["timed_out"].(bool)
	status, _ := waitOut["status"].(string)
	assert.True(t, timedOut || terminalStatuses[status],
		"expected timed_out=true or a terminal status, got %v", waitOut)

	// Get the task list for this job so we have a task ID to query logs for.
	// The endpoint returns a plain JSON array (not a paginated envelope).
	httpResp, err := doAuthRequest(t, "GET", baseURL+"/v1/jobs/"+jobID+"/tasks", adminToken, nil)
	require.NoError(t, err)
	defer httpResp.Body.Close()
	require.Equal(t, http.StatusOK, httpResp.StatusCode)

	var tasks []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(httpResp.Body).Decode(&tasks))
	require.NotEmpty(t, tasks, "job must have at least one task")
	taskID := tasks[0].ID

	// Get logs — no worker so expect empty items.
	logsOut, terr := s.callGetTaskLogs(context.Background(), getTaskLogsArgs{TaskID: taskID})
	require.Nil(t, terr, "callGetTaskLogs failed: %v", terr)
	items, _ := logsOut["items"]
	// Items is either nil or an empty array when no worker has run the task.
	switch v := items.(type) {
	case nil:
		// fine
	case []any:
		assert.Empty(t, v, "no logs expected without a worker")
	}
}

// TestIntegration_ListJobsPagination submits 3 jobs, lists with limit=2, verifies
// next_cursor is non-empty, then fetches page 2 using the cursor.
func TestIntegration_ListJobsPagination(t *testing.T) {
	baseURL, adminToken, _, teardown := startRelayForMCP(t)
	defer teardown()

	s, err := NewServer(baseURL, adminToken)
	require.NoError(t, err)

	// Submit 3 jobs.
	for i := 0; i < 3; i++ {
		spec := jobspec.JobSpec{
			Name: "pagination-job",
			Tasks: []jobspec.TaskSpec{
				{Name: "t", Command: []string{"echo", "x"}},
			},
		}
		_, terr := s.callSubmitJob(context.Background(), submitJobArgs{JobSpec: spec})
		require.Nil(t, terr, "failed to submit job %d: %v", i, terr)
	}

	// Page 1: limit=2.
	page1, terr := s.callListJobs(context.Background(), listJobsArgs{Limit: 2})
	require.Nil(t, terr, "callListJobs page 1 failed: %v", terr)
	items1, ok := page1["items"].([]any)
	require.True(t, ok)
	assert.Len(t, items1, 2)
	nextCursor, _ := page1["next_cursor"].(string)
	assert.NotEmpty(t, nextCursor, "next_cursor must be non-empty after page 1")

	// Page 2: use cursor.
	page2, terr := s.callListJobs(context.Background(), listJobsArgs{Limit: 2, Cursor: nextCursor})
	require.Nil(t, terr, "callListJobs page 2 failed: %v", terr)
	items2, ok := page2["items"].([]any)
	require.True(t, ok)
	assert.NotEmpty(t, items2, "page 2 must have at least one job")
}

// TestIntegration_ForbiddenAsNonAdmin calls callListReservations with a non-admin
// token and expects a "forbidden" ToolError.
func TestIntegration_ForbiddenAsNonAdmin(t *testing.T) {
	baseURL, _, userToken, teardown := startRelayForMCP(t)
	defer teardown()

	s, err := NewServer(baseURL, userToken)
	require.NoError(t, err)

	_, terr := s.callListReservations(context.Background(), listReservationsArgs{})
	require.NotNil(t, terr)
	assert.Equal(t, "forbidden", terr.Code)
}

// TestIntegration_AuthExpired revokes the admin token via DELETE /v1/auth/token
// and then verifies that callWhoami returns terr.Code == "auth_expired".
func TestIntegration_AuthExpired(t *testing.T) {
	baseURL, adminToken, _, teardown := startRelayForMCP(t)
	defer teardown()

	// Revoke the token.
	resp, err := doAuthRequest(t, "DELETE", baseURL+"/v1/auth/token", adminToken, nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// NewServer now resolves identity at startup via GET /v1/users/me, so a revoked
	// token fails construction with the same auth_expired ToolError that callWhoami
	// would have returned.
	s, err := NewServer(baseURL, adminToken)
	require.Nil(t, s)
	require.Error(t, err)
	terr, ok := err.(*ToolError)
	require.True(t, ok, "NewServer error must be a *ToolError, got %T", err)
	assert.Equal(t, "auth_expired", terr.Code)
}

// TestIntegration_ScheduleRoundTrip creates a scheduled job, updates it (enabled=false),
// deletes it, then confirms that get returns a "not_found" ToolError.
func TestIntegration_ScheduleRoundTrip(t *testing.T) {
	baseURL, adminToken, _, teardown := startRelayForMCP(t)
	defer teardown()

	s, err := NewServer(baseURL, adminToken)
	require.NoError(t, err)

	// Create.
	created, terr := s.callCreateSchedule(context.Background(), createScheduleArgs{
		Name:     "test-schedule",
		CronExpr: "0 * * * *",
		JobSpec: jobspec.JobSpec{
			Name: "hourly-job",
			Tasks: []jobspec.TaskSpec{
				{Name: "t", Command: []string{"echo", "tick"}},
			},
		},
	})
	require.Nil(t, terr, "callCreateSchedule failed: %v", terr)
	scheduleID, ok := created["id"].(string)
	require.True(t, ok, "created schedule must have an id")
	assert.NotEmpty(t, scheduleID)

	// Update: disable.
	enabled := false
	updated, terr := s.callUpdateSchedule(context.Background(), updateScheduleArgs{
		ScheduleID: scheduleID,
		Enabled:    &enabled,
	})
	require.Nil(t, terr, "callUpdateSchedule failed: %v", terr)
	assert.Equal(t, false, updated["enabled"])

	// Delete.
	deleted, terr := s.callDeleteSchedule(context.Background(), deleteScheduleArgs{ScheduleID: scheduleID})
	require.Nil(t, terr, "callDeleteSchedule failed: %v", terr)
	assert.Equal(t, true, deleted["ok"])

	// Get — must return not_found.
	_, terr = s.callGetSchedule(context.Background(), getScheduleArgs{ScheduleID: scheduleID})
	require.NotNil(t, terr)
	assert.Equal(t, "not_found", terr.Code)
}

// TestIntegration_NonAdminHidesReservations verifies a non-admin session does not
// list relay_list_reservations against a real /v1/users/me.
func TestIntegration_NonAdminHidesReservations(t *testing.T) {
	baseURL, _, userToken, teardown := startRelayForMCP(t)
	defer teardown()

	s, err := NewServer(baseURL, userToken)
	require.NoError(t, err)

	names := listedToolNames(t, s)
	require.False(t, names["relay_list_reservations"],
		"non-admin must not list relay_list_reservations")
	require.True(t, names["relay_whoami"], "relay_whoami must always be present")
}

// TestIntegration_AdminListsAndCallsReservations verifies an admin session lists
// relay_list_reservations and can call it against a real backend.
func TestIntegration_AdminListsAndCallsReservations(t *testing.T) {
	baseURL, adminToken, _, teardown := startRelayForMCP(t)
	defer teardown()

	s, err := NewServer(baseURL, adminToken)
	require.NoError(t, err)

	names := listedToolNames(t, s)
	require.True(t, names["relay_list_reservations"],
		"admin must list relay_list_reservations")

	_, terr := s.callListReservations(context.Background(), listReservationsArgs{})
	require.Nil(t, terr, "admin call to reservations must succeed: %v", terr)
}

// doAuthRequest is a small helper that sends an authenticated HTTP request.
func doAuthRequest(t *testing.T, method, url, token string, body []byte) (*http.Response, error) {
	t.Helper()
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, url, bytes.NewReader(body))
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}
