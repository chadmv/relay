package main

import (
	"net/http"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/metrics"
	"relay/internal/netlimit"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgxpool"
)

// httpServerDeps is everything buildHTTPServer needs. A struct rather than a
// parameter list so that adding a subsystem is a named field at the call site
// instead of a positional argument nobody can read.
//
// Env parsing stays in main: several of those parses end in log.Fatalf, which
// no test can call.
type httpServerDeps struct {
	addr              string
	pool              *pgxpool.Pool
	q                 *store.Queries
	broker            *events.Broker
	registry          *worker.Registry
	corsOrigins       []string
	loginLimitN       int
	loginLimitWin     time.Duration
	registerLimitN    int
	registerLimitWin  time.Duration
	allowSelfRegister bool
	metrics           *metrics.Store

	// static is the embedded web UI, served for any path no /v1 route matches.
	static http.Handler

	// grpcAdmission is the netlimit listener actually serving the agent port,
	// and it is typed CONCRETELY rather than as api.GRPCAdmissionSource on
	// purpose. A (*netlimit.Listener)(nil) stored in that interface is NOT nil,
	// so handleServerCounters' `src != nil` would be true and Stats() would
	// panic on a nil receiver - a full goroutine stack trace to the log per
	// admin request, inside the feature whose whole subject is bounding log
	// volume. Filtering it here, at the concrete type, is the only place the
	// distinction is still visible.
	grpcAdmission *netlimit.Listener

	// agentHandler is the worker.Handler that serves gRPC, and it is typed
	// CONCRETELY rather than as api.IngestLogBudgetSource for the same reason
	// grpcAdmission is: a (*worker.Handler)(nil) stored in that interface is NOT
	// nil, so the counters handler's `src != nil` would be true and the snapshot
	// call would dereference a nil receiver.
	//
	// IT MUST BE THE SAME HANDLER main REGISTERS WITH
	// RegisterAgentServiceServer. A second Handler would count its own
	// (permanently zero) drops while the real ones went unread - an endpoint
	// reporting that a log budget has suppressed nothing is worse than no
	// endpoint.
	//
	// TWO SEPARATE QUESTIONS, TWO SEPARATE GUARDS, and an earlier version of
	// this comment ("TestServerCountersIsWiredByMain checks the identifier;
	// nothing executable can check it from here") ran them together and left the
	// second one unguarded:
	//
	//   - Does main pass the handler it registered? That is about main.go's
	//     identifiers and is checked syntactically, by
	//     TestServerCountersIsWiredByMain.
	//   - Does buildHTTPServer forward what it was GIVEN? That is executable and
	//     was not checked. Replacing the assignment below with a freshly
	//     constructed worker.NewHandler compiled, vetted clean and left all three
	//     packages green. It is now guarded TWICE, and the two are not the same
	//     strength. The DEFAULT lane rejects the crude form: the wiredDep table's
	//     countersAssignmentSources walk requires every s.Counters assignment
	//     here to be spelled `d.<field>`, so a substituted local or a helper call
	//     is RED with no container (measured). What that cannot see is a
	//     substitution that still LOOKS like a deps field, and the numbers not
	//     moving at all; that is
	//     TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers,
	//     which floods a real registered stream and reads the numbers back
	//     through the real route - the same treatment
	//     TestBuildHTTPServer_ServesTheRealListenersCounters gives grpcAdmission.
	//     It is in the INTEGRATION lane, because moving an ingest counter needs
	//     Connect's message loop and therefore a Postgres round trip.
	//
	// IT FEEDS TWO SECTIONS, ingest_log_budget and task_log_fence, which are
	// different nouns counted on different branches of the same `if` in
	// handleTaskLog. counters_wiring_test.go's table names both against this one
	// field; that is why its cardinality relation counts SECTIONS rather than deps
	// fields.
	agentHandler *worker.Handler

	// watchdog is the coordinator stale-task watchdog main runs, typed
	// CONCRETELY rather than as api.WatchdogSource for the same reason
	// grpcAdmission and agentHandler are: a (*scheduler.Watchdog)(nil) stored in
	// that interface is NOT nil, so the counters handler's `src != nil` would be
	// true and CounterSnapshot would dereference a nil receiver.
	//
	// IT MUST BE THE SAME Watchdog main RUNS. A second one would count its own
	// (permanently zero) sweeps while the real ones went unread - a confident
	// zero, which is worse than an absent section.
	//
	// NOTE WHAT IS AND IS NOT GUARDED, so nobody trims one of the three. That
	// main passes the watchdog it runs is syntactic
	// (TestServerCountersIsWiredByMain). That the assignment below is spelled
	// d.<field> is countersAssignmentSources. That buildHTTPServer forwards what
	// it was GIVEN is EXECUTED and, unlike the other two sections, in the
	// DEFAULT lane: scheduler's watchdogStore is an unexported interface with
	// exported methods, so a sweep can be driven from here with no Postgres and
	// no gRPC stream - see
	// TestBuildHTTPServer_ServesTheWiredWatchdogsSweepCounters.
	watchdog *scheduler.Watchdog
}

// buildHTTPServer assembles the api.Server AND the http.Server that serves it,
// and it is the ONLY place either is constructed.
//
// THE RETURN TYPE IS THE GUARD. This used to be `httpServer.Counters = ...` in
// main, one line among two hundred, and a structural test tried to keep it
// honest by parsing main.go. That test was evaded four separate ways, every one
// of them green across the whole repo: a pointer alias on the next line
// (`cs := &httpServer.Counters; cs.GRPCAdmission = nil`), a helper call in a
// sibling file of the same package (a CallExpr is not an AssignStmt), a
// conditionally-built listener that reached the field as a typed nil, and simply
// moving the assignment below the line that starts serving. Each left the
// endpoint answering `{"started_at":...}` forever, which reads exactly like a
// server whose admission control has never refused anything.
//
// Returning *http.Server rather than *api.Server means main never holds the
// api.Server at all, so none of those four shapes can be written: there is
// nothing to alias, nothing to mutate, nothing to reorder, and no second server
// to serve by mistake. What is left is checked by EXECUTION in
// counters_wiring_test.go, which calls this function with a real
// netlimit.Wrap'd socket and reads the counters back out through the real route.
//
// THAT CLAIM IS ABOUT Counters AND NOTHING ELSE. Two wiring mistakes are still
// writable INSIDE this function and nothing catches either:
//
//   - api.New is positional and takes four same-typed arguments in a row.
//     Swapping loginLimitN/loginLimitWin with registerLimitN/registerLimitWin
//     compiles, and every package stays green; login would then be rate-limited
//     at the registration budget. The named fields on httpServerDeps make the
//     CALL SITE in main readable; they do nothing for the four positions here.
//   - Deleting any of the three assignments below - Metrics, StaticHandler,
//     AllowSelfRegister - is likewise green everywhere. That is the same gap
//     already named for agentHandler.Metrics and agentHandler.AllowAutoEnroll
//     in trailing_log_window_test.go, which the conductor is filing; see the
//     note there about why a guard parsing main.go alone would now miss these.
func buildHTTPServer(d httpServerDeps) *http.Server {
	s := api.New(d.pool, d.q, d.broker, d.registry, d.corsOrigins,
		d.loginLimitN, d.loginLimitWin, d.registerLimitN, d.registerLimitWin)
	s.Metrics = d.metrics
	s.StaticHandler = d.static
	s.AllowSelfRegister = d.allowSelfRegister

	// A nil source leaves its section ABSENT, which is the payload's own
	// vocabulary for "this control is not wired on this replica". It is
	// deliberately NOT collapsed into a section of zeros, and no snapshot method
	// is made nil-tolerant: zeros mean "the control ran and stopped nothing", and
	// merging the two is the exact defect the endpoint exists to fix.
	//
	// PER SECTION, not per struct: each control is wired or not on its own.
	if d.grpcAdmission != nil {
		s.Counters.GRPCAdmission = d.grpcAdmission
	}
	// TWO SECTIONS, ONE OBJECT, AND THAT IS NOT THE WIDENED INTERFACE
	// IngestLogBudgetSource's comment forbids. api.CounterSources keeps a separate
	// nil-able field per section, so each is a per-SECTION fact in the payload and
	// a future source could satisfy one and not the other. What is shared here is
	// the WIRING: both controls live on this one *worker.Handler and neither exists
	// without it, so one nil filter is the honest shape and two identical `if`s
	// would imply an independence this deployment does not have.
	if d.agentHandler != nil {
		s.Counters.IngestLogBudget = d.agentHandler
		s.Counters.TaskLogFence = d.agentHandler
	}
	// ITS OWN `if`, because it is its own deps field. agentHandler feeds two
	// sections under one filter because both controls live on one object; the
	// watchdog is a separate object with a separate lifetime.
	if d.watchdog != nil {
		s.Counters.Watchdog = d.watchdog
	}

	return &http.Server{Addr: d.addr, Handler: s.Handler()}
}
