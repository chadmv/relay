package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/metrics"
	"relay/internal/netlimit"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/schedrunner"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/worker"
	webui "relay/web"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
)

func main() {
	dsn := os.Getenv("RELAY_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://relay:relay@localhost:5432/relay?sslmode=disable"
	}
	httpAddr := os.Getenv("RELAY_HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = ":8080"
	}
	grpcAddr := os.Getenv("RELAY_GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = ":9090"
	}

	// Read before anything with an effect: parsePublicURL is fatal on a bad
	// value, and a typo should stop the process before it advances the schema.
	publicBaseURL, err := parsePublicURL("RELAY_PUBLIC_URL", os.Getenv("RELAY_PUBLIC_URL"))
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Print(publicURLLine(publicBaseURL))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run migrations (migrate DSN uses pgx5 prefix).
	migrateDSN := "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(dsn, "postgresql://"), "postgres://")
	if err := store.Migrate(migrateDSN); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	dbMaxConns := 25
	if v := os.Getenv("RELAY_DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dbMaxConns = n
		}
	}
	// Read before the config is built and fatal on a bad value, because
	// NewWithConfig does not necessarily open a connection eagerly: a malformed
	// runtime parameter would otherwise surface as a connection error at the
	// first query rather than at boot.
	statementTimeout, err := parseDBStatementTimeout(
		"RELAY_DB_STATEMENT_TIMEOUT", os.Getenv("RELAY_DB_STATEMENT_TIMEOUT"))
	if err != nil {
		log.Fatalf("%v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatalf("parse dsn: %v", err)
	}
	cfg.MaxConns = int32(dbMaxConns)
	// Beside MaxConns, which is the other pool-wide bound, and BEFORE
	// NewWithConfig copies the config. Migrations are already done by this point
	// and are unreachable from here - see applyStatementTimeout's header.
	applyStatementTimeout(cfg, statementTimeout)
	log.Print(dbStatementTimeoutLine(statementTimeout))
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)

	if bootstrapEmail := os.Getenv("RELAY_BOOTSTRAP_ADMIN"); bootstrapEmail != "" {
		bootstrapPassword := os.Getenv("RELAY_BOOTSTRAP_PASSWORD")
		if bootstrapPassword == "" {
			log.Fatalf("RELAY_BOOTSTRAP_PASSWORD must be set when RELAY_BOOTSTRAP_ADMIN is set")
		}
		if err := bootstrapAdmin(ctx, q, bootstrapEmail, bootstrapPassword); err != nil {
			log.Fatalf("bootstrap admin: %v", err)
		}
		// Clear from process env so it's not visible via /proc/<pid>/environ or
		// inherited by any future child process. The string itself may linger
		// in heap memory until GC; this is best-effort.
		os.Unsetenv("RELAY_BOOTSTRAP_PASSWORD")
		os.Unsetenv("RELAY_BOOTSTRAP_ADMIN")
		bootstrapPassword = ""
	}

	if adminExists, err := q.AdminExists(ctx); err != nil {
		log.Printf("warn: check admin: %v", err)
	} else if !adminExists {
		log.Println("WARNING: no admin user exists — set RELAY_BOOTSTRAP_ADMIN and RELAY_BOOTSTRAP_PASSWORD on next startup to create one")
	}

	broker := events.NewBroker()
	registry := worker.NewRegistry()

	telemetryWindow := 30 * time.Minute
	if v := os.Getenv("RELAY_TELEMETRY_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			telemetryWindow = d
		}
	}
	staleAfter := 30 * time.Second
	if v := os.Getenv("RELAY_TELEMETRY_STALE_AFTER"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			staleAfter = d
		}
	}
	metricsStore := metrics.NewStore(int(telemetryWindow / metrics.DefaultSampleInterval))

	dispatcher := scheduler.NewDispatcher(q, registry, broker, publicBaseURL)
	notifyListener := scheduler.NewNotifyListener(pool, dispatcher.Trigger)
	go notifyListener.Run(ctx)

	graceWindow := 2 * time.Minute
	if v := os.Getenv("RELAY_WORKER_GRACE_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			graceWindow = d
		}
	}
	grace := worker.NewGraceRegistry(graceWindow, func(workerID string, epoch int32) {
		var id pgtype.UUID
		if err := id.Scan(workerID); err != nil {
			return
		}
		_, _ = q.RequeueWorkerTasksIfEpoch(context.Background(), store.RequeueWorkerTasksIfEpochParams{
			WorkerID:        id,
			ConnectionEpoch: epoch,
		})
		dispatcher.Trigger()
	})
	defer grace.Stop()

	// Seed grace timers for any workers with active tasks. If agents reconnect
	// within the window they reconcile normally; if not, their tasks requeue.
	if err := seedGraceTimersFromActiveTasks(ctx, q, grace, graceWindow); err != nil {
		log.Printf("warn: seed grace timers: %v", err)
	}

	agentHandler := worker.NewHandlerWithGrace(q, pool, registry, broker, dispatcher.Trigger, grace)
	agentHandler.Metrics = metricsStore

	trailingLogWindow, trailingLogWindowWarning := parseTrailingLogWindow(os.Getenv("RELAY_TASKLOG_TRAILING_WINDOW"))
	if trailingLogWindowWarning != "" {
		log.Printf("WARNING: %s", trailingLogWindowWarning)
	}
	agentHandler.TrailingLogWindow = trailingLogWindow

	if v := os.Getenv("RELAY_ALLOW_AUTO_ENROLL"); v != "" {
		allow, err := strconv.ParseBool(v)
		if err != nil {
			log.Fatalf("parse RELAY_ALLOW_AUTO_ENROLL: %v", err)
		}
		agentHandler.AllowAutoEnroll = allow
	}

	autoEnrollCeiling, autoEnrollCeilingWarning := parseAutoEnrollCeiling(
		"RELAY_AUTO_ENROLL_WORKER_CEILING", os.Getenv("RELAY_AUTO_ENROLL_WORKER_CEILING"))
	if autoEnrollCeilingWarning != "" {
		log.Printf("WARNING: %s", autoEnrollCeilingWarning)
	}
	agentHandler.AutoEnrollWorkerCeiling = &autoEnrollCeiling

	corsOrigins, err := api.ParseCORSOrigins(os.Getenv("RELAY_CORS_ORIGINS"))
	if err != nil {
		log.Fatalf("parse RELAY_CORS_ORIGINS: %v", err)
	}

	loginN, loginWin, err := api.ParseRateLimit(envOrDefault("RELAY_LOGIN_RATE_LIMIT", "10:1m"))
	if err != nil {
		log.Fatalf("parse RELAY_LOGIN_RATE_LIMIT: %v", err)
	}
	registerN, registerWin, err := api.ParseRateLimit(envOrDefault("RELAY_REGISTER_RATE_LIMIT", "5:1m"))
	if err != nil {
		log.Fatalf("parse RELAY_REGISTER_RATE_LIMIT: %v", err)
	}
	jobSubmitN, jobSubmitWin, err := api.ParseRateLimit(
		envOrDefault("RELAY_JOB_SUBMIT_RATE_LIMIT", "120:10s"))
	if err != nil {
		log.Fatalf("parse RELAY_JOB_SUBMIT_RATE_LIMIT: %v", err)
	}

	// A SECOND INSTANCE of the user-keyed mechanism, not a second mounting of
	// api.RateLimit: that one keys on clientIP(r), which would collapse every
	// user behind one proxy into one bucket on an authenticated read.
	//
	// SEPARATE FROM THE WRITE BUCKET. Different quantity, different first-party
	// cadence - a polling read at 20 to 100 requests per minute versus an
	// interactive submit - and sharing them would let a search burst refuse a job
	// submission, which is the worse of the two outcomes to trade away.
	//
	// NO ZERO COUNT AND NO ZERO WINDOW: ParseRateLimit refuses both and this is
	// fatal. Every other value boots. The count is the burst and the window the
	// recovery time, so shrinking the window loosens the bound rather than
	// tightening it; the escape from a bound that is too tight is a large count.
	searchN, searchWin, err := api.ParseRateLimit(envOrDefault("RELAY_JOB_SEARCH_RATE_LIMIT", "120:10s"))
	if err != nil {
		log.Fatalf("parse RELAY_JOB_SEARCH_RATE_LIMIT: %v", err)
	}

	// A SEPARATE BUCKET from the submit and search ones. The quantity bounded
	// here is CPU spent in a key derivation function, not task execution and not
	// scan work; the ceilings are three orders of magnitude apart and no single
	// value works for two of them. Folded into the submit bucket, this route
	// would inherit a ceiling a human can never reach.
	//
	// TIGHTER THAN RELAY_LOGIN_RATE_LIMIT ON PURPOSE: a refused login is a user
	// who cannot get in, while a refused password change is a user who already
	// holds a valid session, whose session is untouched, and who waits.
	//
	// Same no-zero-value reasoning as the search bucket above.
	passwordChangeN, passwordChangeWin, err := api.ParseRateLimit(
		envOrDefault("RELAY_PASSWORD_CHANGE_RATE_LIMIT", "5:1m"))
	if err != nil {
		log.Fatalf("parse RELAY_PASSWORD_CHANGE_RATE_LIMIT: %v", err)
	}

	// Bound how many scheduled_jobs rows one owner may hold. Not fatal on a bad
	// value: the rate-limit family above fatals because a zero there unarms a
	// bucket silently, whereas parseScheduleCap always returns a usable positive
	// number. See cmd/relay-server/schedulecap_config.go.
	maxSchedulesPerOwner, scheduleCapWarning := parseScheduleCap(
		"RELAY_MAX_SCHEDULES_PER_OWNER", os.Getenv("RELAY_MAX_SCHEDULES_PER_OWNER"))
	if scheduleCapWarning != "" {
		log.Printf("WARNING: %s", scheduleCapWarning)
	}
	log.Print(scheduleCapLine(maxSchedulesPerOwner))

	allowSelfRegister := false
	if v := os.Getenv("RELAY_ALLOW_SELF_REGISTER"); v != "" {
		allow, err := strconv.ParseBool(v)
		if err != nil {
			log.Fatalf("parse RELAY_ALLOW_SELF_REGISTER: %v", err)
		}
		allowSelfRegister = allow
	}

	// Start gRPC. Admission on this port is bounded four ways - one stream per
	// connection, a total and per-source-prefix connection cap at the listener,
	// an idle-transport reaper, and a deadline on the first RegisterRequest -
	// because every per-connection control this server ships
	// (worker.ingestLogLimiter above all) states its budget per a unit that was
	// previously unbounded. See cmd/relay-server/grpc_config.go.
	//
	// Parsing lives in resolveGRPCBounds rather than here so that the value main
	// hands to netlimit cannot be shadowed between its construction and its use.
	grpcBnds, grpcBndsMsgs := resolveGRPCBounds(os.Getenv)
	for _, m := range grpcBndsMsgs {
		log.Printf("WARNING: %s", m)
	}
	log.Print(grpcBoundsLine(grpcBnds))
	// After grpcBoundsLine: the ceiling line quotes maxConns in its overshoot
	// arithmetic, so it cannot be printed before that value is resolved.
	log.Print(autoEnrollCeilingLine(autoEnrollCeiling, agentHandler.AllowAutoEnroll, grpcBnds.maxConns))
	agentHandler.RegistrationTimeout = grpcBnds.registrationTimeout

	grpcSrv := grpc.NewServer(grpcServerOptions(grpcBnds)...)
	relayv1.RegisterAgentServiceServer(grpcSrv, agentHandler)
	grpcRawLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("listen gRPC: %v", err)
	}
	grpcLis := netlimit.Wrap(grpcRawLis, netlimit.Config{
		MaxTotal: grpcBnds.maxConns,
		MaxPerIP: grpcBnds.maxConnsPerIP,
	})
	// Bound how long a task may hold an assignment. tasks.timeout_sec is
	// otherwise enforced only by the agent, so a wedged or lying agent holds its
	// task - and its worker slot, and its job - forever.
	//
	// CONSTRUCTED HERE, ABOVE buildHTTPServer, because the counters endpoint
	// reports this watchdog's sweeps and buildHTTPServer is the only place the
	// api.Server is built. It is constructed UNCONDITIONALLY even when both
	// bounds are zero (SweepOnce then returns before the round trip): a
	// `var wd *scheduler.Watchdog` assigned inside an `if` is a typed nil in the
	// source interface AND is RED under TestServerCountersIsWiredByMain, which
	// requires exactly one unconditional assignment on the chain. A disabled
	// watchdog therefore serves an honest section of zeros rather than
	// vanishing, which would say "this build has no watchdog".
	watchdogMargin, marginWarning := parseWatchdogDuration(
		"RELAY_TASK_WATCHDOG_MARGIN", os.Getenv("RELAY_TASK_WATCHDOG_MARGIN"),
		scheduler.DefaultWatchdogMargin, minWatchdogMarginDur)
	if marginWarning != "" {
		log.Printf("WARNING: %s", marginWarning)
	}
	maxAssignment, maxAssignmentWarning := parseWatchdogDuration(
		"RELAY_TASK_MAX_ASSIGNMENT", os.Getenv("RELAY_TASK_MAX_ASSIGNMENT"),
		scheduler.DefaultMaxAssignment, minMaxAssignmentDur)
	if maxAssignmentWarning != "" {
		log.Printf("WARNING: %s", maxAssignmentWarning)
	}
	log.Print(watchdogBoundsLine(watchdogMargin, maxAssignment))
	watchdog := scheduler.NewWatchdog(q, registry, broker, watchdogMargin, maxAssignment)

	// The HTTP server is built HERE, after the bounded gRPC listener exists,
	// because the counters endpoint reads that listener's snapshot on demand and
	// buildHTTPServer is the only place the api.Server is constructed. Building
	// it here rather than assigning to a field later is what removes the whole
	// class of ordering and mutation mistakes the old wiring had: main never
	// holds the api.Server, so there is nothing to unwire after the fact and no
	// "must come before serving" constraint left to get wrong.
	srv := buildHTTPServer(httpServerDeps{
		addr:                   httpAddr,
		pool:                   pool,
		q:                      q,
		broker:                 broker,
		registry:               registry,
		corsOrigins:            corsOrigins,
		loginLimitN:            loginN,
		loginLimitWin:          loginWin,
		registerLimitN:         registerN,
		registerLimitWin:       registerWin,
		jobSubmitLimitN:        jobSubmitN,
		jobSubmitLimitWin:      jobSubmitWin,
		searchLimitN:           searchN,
		searchLimitWin:         searchWin,
		passwordChangeLimitN:   passwordChangeN,
		passwordChangeLimitWin: passwordChangeWin,
		maxSchedulesPerOwner:   maxSchedulesPerOwner,
		allowSelfRegister:      allowSelfRegister,
		metrics:                metricsStore,
		static:                 webui.Handler(),
		grpcAdmission:          grpcLis,
		agentHandler:           agentHandler,
		watchdog:               watchdog,
	})
	go runRefusalReporter(ctx, grpcLis, grpcRefusalReportInterval)
	go func() {
		log.Printf("gRPC listening on %s", grpcAddr)
		if err := grpcSrv.Serve(grpcLis); err != nil {
			log.Printf("gRPC serve: %v", err)
		}
	}()

	// Start dispatcher.
	go dispatcher.Run(ctx)

	// Advance next_run_at past any triggers missed during downtime (never-catch-up policy).
	if err := schedrunner.ReconcileOnStartup(ctx, q); err != nil {
		log.Printf("warn: schedrunner reconcile: %v", err)
	}

	// Re-validate every enabled schedule's stored spec once, so a schedule that a
	// retroactive validation change killed is visible within seconds of this
	// deploy rather than at its next scheduled fire - which for @monthly is up to
	// a month away. Record-only: it never clears and it never moves next_run_at.
	//
	// PLACEMENT IS PART OF THE CONTRACT, in two directions.
	//
	// BEFORE the runner goroutine, because the sweep and TickOnce's fire path
	// write the same two columns on the same rows. With the runner not yet
	// started, nothing in THIS PROCESS can interleave between the sweep's LIST
	// and its UPDATEs, so a row whose fire succeeds seconds later cannot have its
	// fresh clear stamped back over with a failure the sweep read before that
	// fire happened.
	//
	// THAT ARGUMENT COVERS ONE PROCESS AND ONLY ONE. It is not the reason the
	// sweep is safe, and an earlier version of this comment claimed it was ("no
	// lock is needed for a pass that runs while nothing else is running"), which
	// is false the moment a second replica exists - and README documents
	// multi-replica as supported, which is why ListEligibleScheduledJobs is
	// FOR UPDATE SKIP LOCKED. What actually makes the sweep safe across replicas
	// is that RecordScheduledJobFailure is FENCED on the job_spec, cron_expr and
	// timezone the sweep validated, so a row repaired through another replica
	// between the LIST and the UPDATE cannot have the stale verdict stamped back
	// onto it. See that statement's header. Placement is a cost and ordering
	// choice; the fence is the correctness argument.
	//
	// AFTER ReconcileOnStartup only for cost, not for correctness: the two
	// commute (the sweep never reads or writes next_run_at, reconcile never reads
	// or writes the failure columns), and reconcile is what the runner needs to
	// be correct, so the purely diagnostic pass does not delay it.
	//
	// A FAILURE HERE MUST NOT STOP THE BOOT. Per-row record failures are logged
	// inside and the sweep continues; a page query's error and a mid-sweep
	// cancellation are returned and logged here as a warning, and the server
	// carries on. Turning a schedule problem into a server that will not start
	// would be worse than the invisibility this closes.
	if err := schedrunner.ValidateStoredSpecsOnStartup(ctx, q); err != nil {
		log.Printf("warn: schedrunner startup validation: %v", err)
	}
	go schedrunner.NewRunner(pool, q).Run(ctx)

	// Mark connected-but-silent workers stale based on telemetry age.
	go metrics.NewSweeper(q, broker, metricsStore, staleAfter).Run(ctx)

	// The watchdog itself is constructed above, next to buildHTTPServer, because
	// the counters endpoint reports its sweeps.
	go watchdog.Run(ctx)

	// Purge expired enrollment tokens hourly.
	go runEnrollmentJanitor(ctx, q)

	// Start HTTP. srv was assembled above, next to the gRPC listener it reports
	// counters for.
	go func() {
		log.Printf("HTTP listening on %s", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP serve: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	// Attempt a graceful gRPC stop, but fall back to a hard stop after 5 seconds.
	// Without the timeout, GracefulStop blocks until all streaming RPCs finish —
	// which means the server hangs as long as any agent is still connected.
	grpcStopped := make(chan struct{})
	go func() {
		grpcSrv.GracefulStop()
		close(grpcStopped)
	}()
	select {
	case <-grpcStopped:
	case <-time.After(5 * time.Second):
		grpcSrv.Stop()
	}

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
	fmt.Println("relay-server stopped")
}

func runEnrollmentJanitor(ctx context.Context, q *store.Queries) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := q.DeleteExpiredAgentEnrollments(ctx); err != nil {
				log.Printf("enrollment janitor: %v", err)
			}
		}
	}
}

// seedGraceTimersFromActiveTasks enumerates workers that have non-terminal
// tasks and schedules grace timers honoring any persisted disconnect time.
//   - disconnected_at IS NULL → start full window (server crashed while worker
//     was online; we don't know when it dropped).
//   - remaining > 0 → start with remaining duration.
//   - remaining <= 0 → fire onExpire synchronously to requeue immediately.
func seedGraceTimersFromActiveTasks(ctx context.Context, q *store.Queries, grace *worker.GraceRegistry, window time.Duration) error {
	candidates, err := q.ListGraceCandidates(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, c := range candidates {
		id := uuidStrMain(c.ID)
		if !c.DisconnectedAt.Valid {
			grace.Start(id, c.ConnectionEpoch)
			continue
		}
		remaining := c.DisconnectedAt.Time.Add(window).Sub(now)
		if remaining > 0 {
			grace.StartWithDuration(id, c.ConnectionEpoch, remaining)
		} else {
			grace.ExpireNow(id, c.ConnectionEpoch)
		}
	}
	return nil
}

// uuidStrMain converts a pgtype.UUID to its canonical string representation.
// Named with Main suffix to avoid collision with any other helper in this file.
func uuidStrMain(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// minSaneTrailingLogWindow is the documented worst case for a legitimately late
// log chunk on a single reconnect attempt (see worker.DefaultTrailingLogWindow).
// It is not a floor - a smaller window is accepted, because narrowing this knob
// is the operator's prerogative - only the point below which
// parseTrailingLogWindow says out loud what the value costs.
const minSaneTrailingLogWindow = 2 * time.Minute

// parseTrailingLogWindow resolves RELAY_TASKLOG_TRAILING_WINDOW's raw value into
// the window handed to worker.Handler, plus the startup warning to log, empty
// when there is nothing to say. There are three outcomes, not two, which is why
// the second return is a message and not an ok bool:
//
//   - Unset, or a sensible duration: used as-is, silently. The ordinary case.
//   - Unparseable, zero or negative: the default is used instead and the warning
//     says so. A silently-ignored typo would leave an operator believing they
//     had tightened a security-relevant knob they had not.
//   - Parseable, positive, but far smaller than a legitimately late chunk: the
//     operator's value is KEPT and the warning names the consequence. Units
//     confusion (`15s` for `15m`) is likelier than a typo and is the only
//     failure mode here that loses data rather than being a no-op - a rejected
//     chunk is dropped with no error to the agent and no line in the server log.
//
// Deliberately not a log.Fatalf, unlike RELAY_ALLOW_AUTO_ENROLL: an unparseable
// duration must not stop a server booting when a safe default exists. Follows
// the `d > 0 or keep the default` idiom of RELAY_TELEMETRY_WINDOW above, plus
// the warning.
func parseTrailingLogWindow(raw string) (time.Duration, string) {
	if raw == "" {
		return worker.DefaultTrailingLogWindow, ""
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return worker.DefaultTrailingLogWindow, fmt.Sprintf(
			"RELAY_TASKLOG_TRAILING_WINDOW=%q is not a positive Go duration; using %s",
			raw, worker.DefaultTrailingLogWindow)
	}
	if d < minSaneTrailingLogWindow {
		return d, fmt.Sprintf(
			"RELAY_TASKLOG_TRAILING_WINDOW=%q resolves to %s, below %s - the worst case for a legitimately late "+
				"log chunk. Using it anyway, but trailing task output will be silently truncated: a rejected chunk "+
				"produces no error to the agent and no line here. Check the units (%s, not %s?).",
			raw, d, minSaneTrailingLogWindow, worker.DefaultTrailingLogWindow, d)
	}
	return d, ""
}
