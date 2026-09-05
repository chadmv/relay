package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"relay/internal/events"
	"relay/internal/metrics"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultMaxSchedulesPerOwner bounds how many scheduled_jobs rows one owner may
// hold before POST /v1/scheduled-jobs refuses to create another.
//
// WHAT IT MUST NOT REFUSE: a studio maintaining one schedule per project per
// cadence - a nightly build, a weekly cleanup, a per-show render - which is tens
// at the outside. WHAT IT DELIBERATELY REFUSES: a pipeline service account
// minting one schedule per shot or per asset. The remedy for that shape is one
// schedule whose job_spec fans out into tasks, which is the model relay is built
// around; raising the number is the other, and it stays visible in the
// environment.
//
// The cap counts ALL of an owner's rows, enabled or not, so a PATCH cannot
// increase the count and creation stays the only enforcement point.
const DefaultMaxSchedulesPerOwner = 100

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

	// JobSubmitLimitN and JobSubmitLimitWin size the per-user bucket over the
	// routes that let one principal buy task execution. Named fields rather than
	// two more positional arguments to New, which already takes four same-typed
	// arguments in a row; an int and a time.Duration cannot be transposed
	// without a compile error.
	//
	// Zero on EITHER leaves the bucket off, which is what a Go caller that
	// builds a Server directly wants - and the guard in Handler is not cosmetic:
	// rateLimiter.allow indexes hits[0] whenever len(hits) >= limit, so a zero
	// limit panics on the first request.
	JobSubmitLimitN   int
	JobSubmitLimitWin time.Duration

	// AllowSelfRegister, when true, lets POST /v1/auth/register succeed without
	// an invite_token. Set by main.go from RELAY_ALLOW_SELF_REGISTER. Defaults
	// to false so existing deployments continue to require invites.
	AllowSelfRegister bool

	// Metrics, when non-nil, supplies worker utilization history. Set by
	// cmd/relay-server after construction.
	Metrics *metrics.Store

	// Counters, when its fields are non-nil, supplies process-lifetime counters
	// for GET /v1/server/counters. A nil field means the section is ABSENT from
	// the payload, not zero. Set by cmd/relay-server's buildHTTPServer, which
	// owns construction of this value and of the http.Server that serves it -
	// see the typed-nil note on CounterSources before wiring a new section.
	Counters CounterSources

	// startedAt is when this server object was constructed - which is NOT
	// process start, and the difference is worth a sentence because the field is
	// served to operators. New runs after the pool, the migrations and the
	// bootstrap admin, and the counters it timestamps do not start moving until
	// cmd/relay-server has built the bounded gRPC listener. Read it as "when
	// these counters began", which is what it is for: a restart zeroes every
	// counter, so a stalled counter and a restart are otherwise identical.
	startedAt time.Time

	// StaticHandler, when non-nil, serves the embedded web UI for any path not
	// matched by a /v1 API route. Set by cmd/relay-server from package webui.
	StaticHandler http.Handler

	// SearchLimitN and SearchLimitWin bound how many ?q= text searches ONE
	// AUTHENTICATED PRINCIPAL may issue per window, across GET /v1/jobs and
	// GET /v1/scheduled-jobs together. Set by cmd/relay-server's buildHTTPServer
	// from RELAY_JOB_SEARCH_RATE_LIMIT. Either field at or below zero leaves the
	// bucket unarmed, and the environment cannot reach that state: ParseRateLimit
	// refuses a zero count and a zero window, and main is fatal on the error.
	// Every other value boots, and a smaller window is a LOOSER bound rather than
	// a tighter one.
	//
	// Exported FIELDS rather than two more arguments on New, whose tail is
	// already four same-typed arguments in a row; buildHTTPServer's own doc
	// comment records a measured green transpose across them.
	//
	// NOT A CPU BUDGET. At the measured cost of a no-match needle, 120 per 10 s
	// is more database time per second than the box has. It is a fairness bound:
	// it stops one principal monopolizing the connection pool, and leaves the
	// pool itself as the concurrency ceiling it has always been.
	SearchLimitN   int
	SearchLimitWin time.Duration

	// PasswordChangeLimitN and PasswordChangeLimitWin bound how many
	// PUT /v1/users/me/password requests ONE AUTHENTICATED PRINCIPAL may issue
	// per window. Set by cmd/relay-server's buildHTTPServer from
	// RELAY_PASSWORD_CHANGE_RATE_LIMIT.
	//
	// The ceiling is small because handleChangePassword runs a bcrypt compare at
	// the shipped cost on every request that gets past readJSON and the
	// eight-character length guard, and a second bcrypt operation on success,
	// while the legitimate pattern is a human retyping a credential into a form.
	//
	// Zero on EITHER field leaves the bucket off, which is what a Go caller
	// building a Server directly wants, and the guard in Handler is not
	// cosmetic: rateLimiter.allow indexes hits[0] whenever len(hits) >= limit,
	// so a zero limit panics on the first request. Same environment reasoning as
	// SearchLimitN above.
	PasswordChangeLimitN   int
	PasswordChangeLimitWin time.Duration

	// searchLimiterOnce guards ONE limiter per Server. Every limiter constructor
	// in this package starts a gcLoop goroutine that is never stopped, so a
	// second instance would be a second budget and a leaked goroutine.
	// TestSearchLimiter_IsConstructedOncePerServer pins this.
	searchLimiterOnce sync.Once
	searchLimiter     *rateLimiter
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
		startedAt:        time.Now().UTC(),
	}
}

// Handler returns an http.Handler with all routes registered.
//
// CALL IT ONCE PER Server. Every limiter this function builds is a fresh bucket
// with its own gc goroutine that nothing stops, so a second call is a second
// budget as well as a leak. The search bucket is the carve-out and is unaffected:
// searchRateLimiter memoizes one per Server and is built inside the handler, for
// the reason search_ratelimit.go gives.
//
// A test that drives more than one request through a limiter built here must bind
// the result once and reuse it; re-deriving it per request gives each request its
// own empty window.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Authenticated helpers
	auth := BearerAuth(s.q)
	admin := AdminOnly

	// ONE bucket over the routes that let a non-admin buy EXECUTION, and it is
	// shared on purpose: the quantity bounded is how much execution one
	// principal can buy per unit time, and it does not care which verb bought
	// it. Three instances would triple the ceiling and let a caller alternate
	// between the routes to stay under all three.
	//
	// Built once here, not per route: UserRateLimit starts a gc goroutine that
	// is never stopped. The rule for a future wrap is auth(userLimit(admin(h))),
	// so a non-admin's rejected probes are charged to the prober's own bucket
	// instead of being free.
	userLimit := func(h http.Handler) http.Handler { return h }
	if s.JobSubmitLimitN > 0 && s.JobSubmitLimitWin > 0 {
		userLimit = UserRateLimit(s.JobSubmitLimitN, s.JobSubmitLimitWin)
	}

	// A SEPARATE bucket from userLimit, not a fourth route on it. That one
	// bounds how much task EXECUTION a principal buys, at a burst sized for job
	// submission; this bounds how much CPU in a key-derivation function it buys,
	// at a burst sized for a human retyping a password. Folded together, either
	// this route inherits a ceiling it can never reach or the submit ceiling
	// drops to password-change rates.
	//
	// Built here, not per route and not per request: UserRateLimit starts a gc
	// goroutine that is never stopped, so a second instance is a second budget
	// and a leak. cmd/relay-server's TestBuildHTTPServer_ThePasswordBucketIsWired-
	// WithTheConfiguredLimit is what pins that, at a ceiling of two.
	passwordLimit := func(h http.Handler) http.Handler { return h }
	if s.PasswordChangeLimitN > 0 && s.PasswordChangeLimitWin > 0 {
		passwordLimit = UserRateLimit(s.PasswordChangeLimitN, s.PasswordChangeLimitWin)
	}

	// Public endpoints
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/config", s.handleConfig)

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

	// Auth (self-service)
	mux.Handle("GET /v1/users/me", auth(http.HandlerFunc(s.handleGetMe)))
	// auth(passwordLimit(h)), never passwordLimit(auth(h)): UserRateLimit reads
	// the principal off the request context, which only BearerAuth puts there,
	// so the outer form refuses every request with a 401 it has no business
	// issuing. RELAY_PASSWORD_CHANGE_RATE_LIMIT.
	mux.Handle("PUT /v1/users/me/password", auth(passwordLimit(http.HandlerFunc(s.handleChangePassword))))
	mux.Handle("DELETE /v1/auth/token", auth(http.HandlerFunc(s.handleLogoutCurrent)))
	mux.Handle("DELETE /v1/auth/tokens", auth(http.HandlerFunc(s.handleLogoutAll)))
	// auth(...) only, NOT admin(...): this is the self-service block. Rows are
	// scoped to authUser.ID from the context, never to a query parameter.
	mux.Handle("GET /v1/auth/tokens", auth(http.HandlerFunc(s.handleListTokens)))

	// Jobs
	//
	// Read routes (GET) are intentionally global: any authenticated user may
	// read any job's metadata, task list, and logs. This is deliberate
	// render-farm semantics - a shared farm where operators inspect any job.
	// The two destructive writes - cancel (DELETE) and retry
	// (POST /v1/jobs/{id}/retry) - are owner-or-admin gated inside their
	// handlers, via the single jobOwnerOr404 helper in jobs.go.
	//
	// Note the interaction with that cancel gate: handleCancelJob and
	// handleRetryJob return 404
	// (not 403) on deny to avoid leaking job existence, but these global reads
	// already expose existence and metadata to any authenticated user. The
	// cancel 404 is therefore defense-in-depth for the destructive action, not
	// a true existence secret. See the closed item
	// docs/backlog/closed/bug-2026-06-20-job-task-read-routes-missing-authz.md.
	//
	// POST /v1/jobs, POST /v1/jobs/{id}/retry and
	// POST /v1/scheduled-jobs/{id}/run-now share ONE per-user bucket
	// (RELAY_JOB_SUBMIT_RATE_LIMIT). retry carries no body and no spec
	// validation and still re-buys a whole job's execution, which is why the
	// cheapest of the three draws on the same budget as the most expensive.
	mux.Handle("POST /v1/jobs", auth(userLimit(http.HandlerFunc(s.handleCreateJob))))
	mux.Handle("GET /v1/jobs", auth(http.HandlerFunc(s.handleListJobs)))
	mux.Handle("GET /v1/jobs/stats", auth(http.HandlerFunc(s.handleJobStats)))
	mux.Handle("GET /v1/jobs/{id}", auth(http.HandlerFunc(s.handleGetJob)))
	mux.Handle("DELETE /v1/jobs/{id}", auth(http.HandlerFunc(s.handleCancelJob)))
	mux.Handle("POST /v1/jobs/{id}/retry", auth(userLimit(http.HandlerFunc(s.handleRetryJob))))

	// Tasks (read routes are intentionally global - see Jobs note above).
	mux.Handle("GET /v1/jobs/{id}/tasks", auth(http.HandlerFunc(s.handleListTasks)))
	mux.Handle("GET /v1/tasks/{id}", auth(http.HandlerFunc(s.handleGetTask)))
	mux.Handle("GET /v1/tasks/{id}/logs", auth(http.HandlerFunc(s.handleGetTaskLogs)))

	// Workers (PATCH is admin-only)
	mux.Handle("GET /v1/workers", auth(http.HandlerFunc(s.handleListWorkers)))
	mux.Handle("GET /v1/workers/stats", auth(http.HandlerFunc(s.handleWorkerStats)))
	mux.Handle("GET /v1/workers/{id}", auth(http.HandlerFunc(s.handleGetWorker)))
	mux.Handle("GET /v1/workers/{id}/metrics", auth(http.HandlerFunc(s.handleGetWorkerMetrics)))
	mux.Handle("GET /v1/workers/{id}/tasks", auth(http.HandlerFunc(s.handleListWorkerTasks)))
	mux.Handle("PATCH /v1/workers/{id}", auth(admin(http.HandlerFunc(s.handleUpdateWorker))))

	// Server-wide counters (admin-only). NOT auth-only like /v1/workers/stats:
	// that is a database census of the fleet, while these are process-lifetime
	// in-memory numbers describing adversary activity and internal control
	// state.
	mux.Handle("GET /v1/server/counters", auth(admin(http.HandlerFunc(s.handleServerCounters))))

	// Reservations (admin-only)
	mux.Handle("GET /v1/reservations", auth(admin(http.HandlerFunc(s.handleListReservations))))
	mux.Handle("POST /v1/reservations", auth(admin(http.HandlerFunc(s.handleCreateReservation))))
	mux.Handle("DELETE /v1/reservations/{id}", auth(admin(http.HandlerFunc(s.handleDeleteReservation))))

	// Invites (admin-only)
	mux.Handle("POST /v1/invites", auth(admin(http.HandlerFunc(s.handleCreateInvite))))
	mux.Handle("GET /v1/invites", auth(admin(http.HandlerFunc(s.handleListInvites))))

	// Agent enrollments (admin-only)
	mux.Handle("POST /v1/agent-enrollments", auth(admin(http.HandlerFunc(s.handleCreateAgentEnrollment))))
	mux.Handle("GET /v1/agent-enrollments", auth(admin(http.HandlerFunc(s.handleListAgentEnrollments))))
	mux.Handle("DELETE /v1/workers/{id}/token", auth(admin(http.HandlerFunc(s.handleDeleteWorkerToken))))
	mux.Handle("GET /v1/workers/revoked", auth(admin(http.HandlerFunc(s.handleListRevokedWorkers))))
	mux.Handle("POST /v1/workers/{id}/disable", auth(admin(http.HandlerFunc(s.handleDisableWorker))))
	mux.Handle("POST /v1/workers/{id}/enable", auth(admin(http.HandlerFunc(s.handleEnableWorker))))
	mux.Handle("DELETE /v1/workers/{id}", auth(admin(http.HandlerFunc(s.handleDeleteWorker))))

	// User management
	mux.Handle("GET /v1/users", auth(admin(http.HandlerFunc(s.handleListUsers))))
	mux.Handle("POST /v1/users", auth(admin(http.HandlerFunc(s.handleAdminCreateUser))))
	mux.Handle("POST /v1/users/password-reset", auth(admin(http.HandlerFunc(s.handleAdminPasswordReset))))
	mux.Handle("PATCH /v1/users/me", auth(http.HandlerFunc(s.handleUpdateMe)))
	mux.Handle("PATCH /v1/users/{id}", auth(admin(http.HandlerFunc(s.handleAdminUpdateUser))))
	mux.Handle("POST /v1/users/{id}/archive", auth(admin(http.HandlerFunc(s.handleAdminArchiveUser))))
	mux.Handle("POST /v1/users/{id}/unarchive", auth(admin(http.HandlerFunc(s.handleAdminUnarchiveUser))))

	// Worker workspaces (admin-only)
	mux.Handle("GET /v1/workers/{id}/workspaces", auth(admin(http.HandlerFunc(s.handleListWorkerWorkspaces))))
	mux.Handle("POST /v1/workers/{id}/workspaces/{short_id}/evict", auth(admin(http.HandlerFunc(s.handleEvictWorkerWorkspace))))

	// Scheduled jobs
	mux.Handle("POST /v1/scheduled-jobs", auth(http.HandlerFunc(s.handleCreateScheduledJob)))
	mux.Handle("GET /v1/scheduled-jobs", auth(http.HandlerFunc(s.handleListScheduledJobs)))
	// Auth-only, matching /v1/workers/stats and deliberately not
	// /v1/server/counters. ServeMux prefers the literal segment over
	// /v1/scheduled-jobs/{id}, the same way /v1/jobs/stats already coexists with
	// /v1/jobs/{id}.
	mux.Handle("GET /v1/scheduled-jobs/stats", auth(http.HandlerFunc(s.handleScheduledJobStats)))
	mux.Handle("GET /v1/scheduled-jobs/{id}", auth(http.HandlerFunc(s.handleGetScheduledJob)))
	mux.Handle("PATCH /v1/scheduled-jobs/{id}", auth(http.HandlerFunc(s.handlePatchScheduledJob)))
	mux.Handle("DELETE /v1/scheduled-jobs/{id}", auth(http.HandlerFunc(s.handleDeleteScheduledJob)))
	mux.Handle("POST /v1/scheduled-jobs/{id}/run-now", auth(userLimit(http.HandlerFunc(s.handleRunScheduledJobNow))))

	// SSE
	mux.Handle("GET /v1/events", auth(http.HandlerFunc(s.handleEvents)))

	if s.StaticHandler != nil {
		mux.Handle("/", s.StaticHandler)
	}

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

// maxBodyBytes caps the size of any JSON request body. It bounds server memory
// against arbitrarily large bodies, including on unauthenticated endpoints.
const maxBodyBytes = 1 << 20 // 1 MiB

// readJSON decodes the request body into v, enforcing maxBodyBytes. On failure
// it writes an error response (413 for an oversize body, 400 for malformed
// JSON) and returns false; callers should simply return.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid request body")
		}
		return false
	}
	return true
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

// rawObject is like rawJSON but for fields that are always JSON *objects* (env,
// requires). An omitted map is stored as the literal `null` (json.Marshal of a
// nil map), which rawJSON would pass through unchanged; rawObject normalizes both
// empty and `null` to `{}` so a client never receives a null where an object is
// expected (the web job-detail page did Object.entries() on it and crashed).
func rawObject(b []byte) json.RawMessage {
	if len(b) == 0 || string(b) == "null" {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(b)
}
