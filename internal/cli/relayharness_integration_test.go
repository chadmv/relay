//go:build integration

package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/testsupport/pgdsn"
	"relay/internal/tokenhash"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// relayServer is one live internal/api server over HTTP, backed by one Postgres
// database that belongs to the test that started it.
//
// It exposes ONLY production types plus itself. It must never take or return
// anything declared in internal/cli beyond *Config, which is this package's own
// two-field config struct and the single injection point.
type relayServer struct {
	BaseURL    string
	Pool       *pgxpool.Pool
	Q          *store.Queries
	AdminToken string
	UserToken  string
	AdminEmail string
	UserEmail  string
}

func (s *relayServer) adminCfg() *Config { return &Config{ServerURL: s.BaseURL, Token: s.AdminToken} }
func (s *relayServer) userCfg() *Config  { return &Config{ServerURL: s.BaseURL, Token: s.UserToken} }

// testCtx returns a context with an EXPLICIT deadline, and every doX call in
// this lane must be given one. t.Context() alone is not enough: it is cancelled
// at test END, so a hang inside doLogs' SSE wait would consume the whole
// package timeout and produce the nameless panic: banner the teardown backlog
// item describes. handleEvents holds a connection open with no heartbeat and no
// server-side timeout, so that hang is reachable, not theoretical.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func startRelayServer(t *testing.T) *relayServer {
	t.Helper()
	dsn := pgdsn.NewIntegrationDSN(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { pgdsn.BoundedCleanup(t, "pool.Close", pool.Close) })

	q := store.New(pool)
	// The four trailing zeros disable the login and register rate limiters.
	// Handler() only wraps those two routes when both N and Win are positive, so
	// 0 means "off", not "zero requests allowed" - startRelayForMCP in
	// internal/mcp passes the same zeros and then requires a real
	// POST /v1/auth/login to return 201.
	//
	// NOT WIRED, deliberately: gRPC, scheduler.Dispatcher, schedrunner, the
	// metrics sweeper, GraceRegistry, the stale-task watchdog, webui.Handler()
	// and bootstrapAdmin (which is package main and unimportable). No task ever
	// runs here and no job leaves pending on its own.
	apiSrv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	httpSrv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(func() { pgdsn.BoundedCleanup(t, "httpSrv.Close", httpSrv.Close) })

	s := &relayServer{
		BaseURL:    httpSrv.URL,
		Pool:       pool,
		Q:          q,
		AdminEmail: "admin@cli-lane.test",
		UserEmail:  "user@cli-lane.test",
	}
	s.AdminToken = seedUserWithToken(t, q, s.AdminEmail, true)
	s.UserToken = seedUserWithToken(t, q, s.UserEmail, false)
	return s
}

// cliLanePassword is the plaintext behind every seeded user's stored hash. It is
// a const rather than a literal inside seedUserWithToken because
// TestIntegration_LoginAgainstTheRealEndpoint drives a real POST /v1/auth/login
// with it, and a second copy could drift from the hash this harness writes.
const cliLanePassword = "cli-lane-password"

// seedUserWithToken creates a user and an API token for it using only exported
// production symbols, and returns the raw hex token the client presents.
func seedUserWithToken(t *testing.T, q *store.Queries, email string, isAdmin bool) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(cliLanePassword), bcrypt.MinCost)
	require.NoError(t, err)
	u, err := q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name:         email,
		Email:        email,
		IsAdmin:      isAdmin,
		PasswordHash: string(hash),
	})
	require.NoError(t, err)

	raw := make([]byte, 16)
	_, err = rand.Read(raw)
	require.NoError(t, err)
	rawHex := hex.EncodeToString(raw)
	_, err = q.CreateToken(t.Context(), store.CreateTokenParams{
		UserID:    u.ID,
		TokenHash: tokenhash.Hash(rawHex),
		ExpiresAt: pgtype.Timestamptz{}, // SQL NULL: never expires
	})
	require.NoError(t, err)
	return rawHex
}

// seedWorker inserts a worker, forces its status, and returns its uuid as text.
//
// name AND hostname are both parameters and callers must pass DIFFERENT values.
// They are both strings on both sides of the wire, so a fixture that sets them
// equal makes a name/hostname transposition invisible - the list test asserts
// the NAME column while the delete test resolves by HOSTNAME, and only distinct
// values make those two assertions independent.
func seedWorker(t *testing.T, s *relayServer, name, hostname, status string) string {
	t.Helper()
	w, err := s.Q.UpsertWorkerByHostname(t.Context(), store.UpsertWorkerByHostnameParams{
		Name:     name,
		Hostname: hostname,
		CpuCores: 16,
		RamGb:    64,
		GpuCount: 2,
		GpuModel: "RTX 4090",
		Os:       "linux",
	})
	require.NoError(t, err)
	// workers.status defaults to 'offline'; set it explicitly anyway so each
	// test states the status its assertion depends on.
	_, err = s.Q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{
		ID:     w.ID,
		Status: status,
	})
	require.NoError(t, err)
	return uuidString(t, s, w.ID)
}

// uuidString renders a pgtype.UUID the way the server does, by asking Postgres
// rather than by hand-writing the format string a seventh time (internal/api's
// uuidStr, cmd/relay-server, internal/metrics, internal/scheduler,
// internal/worker and internal/cli's canonicalJobID already carry copies).
func uuidString(t *testing.T, s *relayServer, id pgtype.UUID) string {
	t.Helper()
	var out string
	require.NoError(t, s.Pool.QueryRow(t.Context(), `SELECT $1::uuid::text`, id).Scan(&out))
	return out
}

// firstTaskID returns the id of a job's first task, read straight from the
// database. The API route would work too; the pool is used because it cannot
// itself be broken by the response-shape drift this lane exists to catch.
func firstTaskID(t *testing.T, s *relayServer, jobID string) string {
	t.Helper()
	var id string
	require.NoError(t, s.Pool.QueryRow(t.Context(),
		`SELECT id::text FROM tasks WHERE job_id = $1::uuid ORDER BY created_at, id LIMIT 1`,
		jobID).Scan(&id))
	return id
}

// seedLogRows inserts n log rows for a task directly via the pool, exactly as
// internal/api/tasks_integration_test.go's seedLogRow already does.
// GetTaskLogsPage is `WHERE task_id = $1 AND id > $2` with no fence of any
// kind, so no agent and no gRPC is needed - and the cost, that AppendTaskLog's
// epoch/identity/recency fence goes unexercised, is a recorded uncovered axis.
//
// ROW 1 CARRIES THE DISCRIMINATING STREAM VALUE and row n the discriminating
// content. A distinctive input placed only at the END cannot detect an
// early-exit defect ([[reference_mutation_proof_position]]), so both ends are
// distinctive and both are asserted.
//
// This depends on `INSERT ... SELECT ... ORDER BY g` assigning the BIGSERIAL
// id column in scan order, so that row g=1 really is the row with the lowest
// id and lines[0] in a caller's assertion really is "line-1". Measured (not
// assumed): 6/6 trials at n=250 held, and the plan shape is a serial
// ModifyTable consuming a sorted generate_series, which is why. A future edit
// that adds a WHERE clause or a join here could change the scan order without
// changing this comment - re-verify if one is added.
func seedLogRows(t *testing.T, s *relayServer, taskID string, n int) {
	t.Helper()
	_, err := s.Pool.Exec(t.Context(), `
		INSERT INTO task_logs (task_id, stream, content)
		SELECT $1::uuid,
		       CASE WHEN g = 1 THEN 'stderr' ELSE 'stdout' END,
		       'line-' || g
		FROM generate_series(1, $2::int) AS g
		ORDER BY g`, taskID, n)
	require.NoError(t, err)
}

// writeSpecFile writes a job spec into the test's temp dir and returns its path.
// doSubmit and doSchedulesCreate both read a real file named on argv, so the
// lane gives them one rather than reaching past the entrypoint.
func writeSpecFile(t *testing.T, spec string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "job.json")
	require.NoError(t, os.WriteFile(path, []byte(spec), 0o600))
	return path
}

// TestIntegration_HarnessServesAndAuthenticates is the server harness's own
// test, for the same reason Task 1's exists: it makes every later RED
// attributable to the endpoint under test rather than to the wiring.
func TestIntegration_HarnessServesAndAuthenticates(t *testing.T) {
	s := startRelayServer(t)

	var out bytes.Buffer
	require.NoError(t, doWorkers(testCtx(t), s.adminCfg(), []string{"list"}, &out))
	require.Contains(t, out.String(), "Total: 0")

	// The non-admin token authenticates but is not an admin. /v1/workers is
	// auth-only, so this must succeed too - it pins that userCfg is a VALID
	// token and not merely a broken one, which every 403 test below relies on.
	var userOut bytes.Buffer
	require.NoError(t, doWorkers(testCtx(t), s.userCfg(), []string{"list"}, &userOut))
	require.Contains(t, userOut.String(), "Total: 0")
}

