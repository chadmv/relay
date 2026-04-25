package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/pgtype"
)

// Server holds shared dependencies for all HTTP handlers.
type Server struct {
	pool             *pgxpool.Pool
	q                *store.Queries
	broker           *events.Broker
	registry         *worker.Registry
	CORSOrigins      []string
	LoginLimitN      int
	LoginLimitWin    time.Duration
	RegisterLimitN   int
	RegisterLimitWin time.Duration
}

// New creates a Server.
func New(
	pool *pgxpool.Pool,
	q *store.Queries,
	broker *events.Broker,
	registry *worker.Registry,
	corsOrigins []string,
	loginLimitN int,
	loginLimitWin time.Duration,
	registerLimitN int,
	registerLimitWin time.Duration,
) *Server {
	return &Server{
		pool:             pool,
		q:                q,
		broker:           broker,
		registry:         registry,
		CORSOrigins:      corsOrigins,
		LoginLimitN:      loginLimitN,
		LoginLimitWin:    loginLimitWin,
		RegisterLimitN:   registerLimitN,
		RegisterLimitWin: registerLimitWin,
	}
}

// Handler returns an http.Handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Authenticated helpers
	auth := BearerAuth(s.q)
	admin := AdminOnly

	// Public endpoints
	mux.HandleFunc("GET /v1/health", s.handleHealth)

	registerH := http.HandlerFunc(s.handleRegister)
	if s.RegisterLimitN > 0 && s.RegisterLimitWin > 0 {
		mux.Handle("POST /v1/auth/register", RateLimit(s.RegisterLimitN, s.RegisterLimitWin)(registerH))
	} else {
		mux.Handle("POST /v1/auth/register", registerH)
	}

	loginH := http.HandlerFunc(s.handleLogin)
	if s.LoginLimitN > 0 && s.LoginLimitWin > 0 {
		mux.Handle("POST /v1/auth/login", RateLimit(s.LoginLimitN, s.LoginLimitWin)(loginH))
	} else {
		mux.Handle("POST /v1/auth/login", loginH)
	}

	mux.Handle("PUT /v1/users/me/password", auth(http.HandlerFunc(s.handleChangePassword)))

	// Jobs
	mux.Handle("POST /v1/jobs", auth(http.HandlerFunc(s.handleCreateJob)))
	mux.Handle("GET /v1/jobs", auth(http.HandlerFunc(s.handleListJobs)))
	mux.Handle("GET /v1/jobs/{id}", auth(http.HandlerFunc(s.handleGetJob)))
	mux.Handle("DELETE /v1/jobs/{id}", auth(http.HandlerFunc(s.handleCancelJob)))

	// Tasks
	mux.Handle("GET /v1/jobs/{id}/tasks", auth(http.HandlerFunc(s.handleListTasks)))
	mux.Handle("GET /v1/tasks/{id}", auth(http.HandlerFunc(s.handleGetTask)))
	mux.Handle("GET /v1/tasks/{id}/logs", auth(http.HandlerFunc(s.handleGetTaskLogs)))

	// Workers (PATCH is admin-only)
	mux.Handle("GET /v1/workers", auth(http.HandlerFunc(s.handleListWorkers)))
	mux.Handle("GET /v1/workers/{id}", auth(http.HandlerFunc(s.handleGetWorker)))
	mux.Handle("PATCH /v1/workers/{id}", auth(admin(http.HandlerFunc(s.handleUpdateWorker))))

	// Reservations (admin-only)
	mux.Handle("GET /v1/reservations", auth(admin(http.HandlerFunc(s.handleListReservations))))
	mux.Handle("POST /v1/reservations", auth(admin(http.HandlerFunc(s.handleCreateReservation))))
	mux.Handle("DELETE /v1/reservations/{id}", auth(admin(http.HandlerFunc(s.handleDeleteReservation))))

	// Invites (admin-only)
	mux.Handle("POST /v1/invites", auth(admin(http.HandlerFunc(s.handleCreateInvite))))

	// Agent enrollments (admin-only)
	mux.Handle("POST /v1/agent-enrollments", auth(admin(http.HandlerFunc(s.handleCreateAgentEnrollment))))
	mux.Handle("GET /v1/agent-enrollments", auth(admin(http.HandlerFunc(s.handleListAgentEnrollments))))
	mux.Handle("DELETE /v1/workers/{id}/token", auth(admin(http.HandlerFunc(s.handleDeleteWorkerToken))))

	// Worker workspaces (admin-only)
	mux.Handle("GET /v1/workers/{id}/workspaces", auth(admin(http.HandlerFunc(s.handleListWorkerWorkspaces))))
	mux.Handle("POST /v1/workers/{id}/workspaces/{short_id}/evict", auth(admin(http.HandlerFunc(s.handleEvictWorkerWorkspace))))

	// Scheduled jobs
	mux.Handle("POST /v1/scheduled-jobs", auth(http.HandlerFunc(s.handleCreateScheduledJob)))
	mux.Handle("GET /v1/scheduled-jobs", auth(http.HandlerFunc(s.handleListScheduledJobs)))
	mux.Handle("GET /v1/scheduled-jobs/{id}", auth(http.HandlerFunc(s.handleGetScheduledJob)))
	mux.Handle("PATCH /v1/scheduled-jobs/{id}", auth(http.HandlerFunc(s.handlePatchScheduledJob)))
	mux.Handle("DELETE /v1/scheduled-jobs/{id}", auth(http.HandlerFunc(s.handleDeleteScheduledJob)))
	mux.Handle("POST /v1/scheduled-jobs/{id}/run-now", auth(http.HandlerFunc(s.handleRunScheduledJobNow)))

	// SSE
	mux.Handle("GET /v1/events", auth(http.HandlerFunc(s.handleEvents)))

	return CORS(s.CORSOrigins)(mux)
}

// ─── JSON helpers ────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ─── UUID helpers ─────────────────────────────────────────────────────────────

// uuidStr converts a pgtype.UUID to its canonical string representation
// (e.g., "550e8400-e29b-41d4-a716-446655440000"). Returns "" if invalid.
func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// parseUUID scans a UUID string into pgtype.UUID.
func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID %q: %w", s, err)
	}
	return u, nil
}

// rawJSON returns b as json.RawMessage, defaulting to {} for nil/empty input.
func rawJSON(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(b)
}
