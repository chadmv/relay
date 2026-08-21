package main

import (
	"net/http"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/metrics"
	"relay/internal/netlimit"
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
func buildHTTPServer(d httpServerDeps) *http.Server {
	s := api.New(d.pool, d.q, d.broker, d.registry, d.corsOrigins,
		d.loginLimitN, d.loginLimitWin, d.registerLimitN, d.registerLimitWin)
	s.Metrics = d.metrics
	s.StaticHandler = d.static
	s.AllowSelfRegister = d.allowSelfRegister

	// A nil listener leaves the section ABSENT, which is the payload's own
	// vocabulary for "this control is not wired on this replica". It is
	// deliberately NOT collapsed into a section of zeros, and Stats() is
	// deliberately not made nil-tolerant: zeros mean "the control ran and
	// stopped nothing", and merging the two is the exact defect the endpoint
	// exists to fix.
	if d.grpcAdmission != nil {
		s.Counters = api.CounterSources{GRPCAdmission: d.grpcAdmission}
	}

	return &http.Server{Addr: d.addr, Handler: s.Handler()}
}
