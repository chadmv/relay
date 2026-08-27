//go:build integration

package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// dsnEnvVar selects the harness mode. Unset: one Postgres testcontainer per
// test, which is what every other integration package in this repo does and
// what a developer with Docker gets for free. Set: one freshly CREATEd
// database per test on the supplied server, which is what
// .github/workflows/go-ci.yml's cli-integration job uses - no Docker API, no
// image pull, no Ryuk reaper. The name follows the existing
// RELAY_E2E_DATABASE_URL convention rather than inventing a new one.
const dsnEnvVar = "RELAY_TEST_DATABASE_URL"

// testDBPrefix is the fixed prefix every generated database name carries.
const testDBPrefix = "relaytest_"

// dbNamePattern is the FULL-STRING validation applied to a generated database
// name once, at generation, before it is used in the CREATE - and reused
// unchanged at DROP time. A prefix-only check (strings.HasPrefix) constrains
// the first ten characters and nothing after, so
// `relaytest_x" WITH (FORCE); DROP DATABASE "relay_prod` would have passed
// it; both the CREATE and the DROP here are argument-free pgx.Exec calls,
// which use the simple protocol and therefore permit statement chaining
// (pgx/v5 conn.go: "Always use simple protocol when there are no arguments"),
// so this validation is the whole control, not defense in depth. It is not
// reachable today - dbName is crypto/rand hex over [0-9a-f], generated once
// and never reassigned - but the CREATE previously had no check of its own at
// all, only the DROP cleanup did, so this closes that gap too. This is
// web/e2e/ensure-db.mjs's ALLOWED_DB_NAME lesson applied in the direction
// that matters here - the danger is a plausible refactor, not a hostile env
// value.
var dbNamePattern = regexp.MustCompile(`^relaytest_[0-9a-f]{16}$`)

// teardownTimeout bounds EVERY cleanup step. Unbounded teardown is
// docs/backlog/bug-2026-08-26-integration-lane-times-out-on-docker-teardown:
// the three existing copies pass context.Background() and discard the error, so
// a hung Terminate renders as a bare panic:/FAIL with NO TEST NAME ATTACHED.
// Bounded plus t.Errorf makes a hung teardown fail one named test.
const teardownTimeout = 30 * time.Second

// newIntegrationDSN returns a postgres:// DSN for a migrated, empty database
// that belongs to this test alone, and registers its teardown.
//
// IT MUST NOT IMPORT ANYTHING FROM internal/cli OR internal/api. It takes a
// *testing.T and returns a string; that is the whole surface. Keeping it true
// is what makes the later extraction into a shared package (backlog B4) a file
// move rather than a redesign - a shared harness that imports its consumer's
// types cannot be shared.
func newIntegrationDSN(t *testing.T) string {
	t.Helper()
	if base := os.Getenv(dsnEnvVar); base != "" {
		return newSharedServiceDSN(t, base)
	}
	return newContainerDSN(t)
}

// migrateDSN rewrites a postgres:// DSN into the pgx5:// form store.Migrate
// requires (see its doc comment in internal/store/migrate.go). Callers must
// have already established the postgres:// prefix.
func migrateDSN(dsn string) string {
	return "pgx5" + strings.TrimPrefix(dsn, "postgres")
}

func newContainerDSN(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("relay_test"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			// WithOccurrence(2) IS LOAD-BEARING. postgres:16 emits this line once
			// during its own init pass, before the real listener is up. Copy it
			// verbatim; the three existing copies in this repo all do.
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	// tcpostgres.Run deliberately returns a live container ALONGSIDE a non-nil
	// error (e.g. a wait-strategy timeout after the container started) so the
	// caller can terminate it - see modules/postgres@v0.42.0/postgres.go. Arm
	// the cleanup on that possibility before the require.NoError below can
	// FailNow/Goexit out of this function, or a container started under load
	// leaks forever.
	if pg != nil {
		t.Cleanup(func() {
			tctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
			defer cancel()
			if err := pg.Terminate(tctx); err != nil {
				t.Errorf("terminate postgres container: %v", err)
			}
		})
	}
	require.NoError(t, err)

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(dsn, "postgres://"),
		"testcontainers DSN must start with postgres://, got %q", dsn)
	require.NoError(t, store.Migrate(migrateDSN(dsn)))
	return dsn
}

// dsnAssertT is the minimal surface two different callers need, for two
// different reasons, neither of which is "the minimal surface
// assertDSNTargetsDatabase needs" alone:
//
//   - assertDSNTargetsDatabase and assertNoConnectionTargetOverride (this
//     file) are typed against it, not *testing.T, so their own regression
//     tests can substitute a fake and observe pass/fail as a returned value -
//     Go's testing package marks a parent test permanently failed the moment
//     any t.Run child inside it fails, with no way to un-fail it afterward,
//     which would make this file's own "this must be rejected" case red
//     forever instead of proving the rejection happened.
//   - boundedCleanup (relayharness_integration_test.go, same package) is
//     typed against it for a different reason: there is no way to prove a
//     real *testing.T did NOT crash the process on a panic by making it
//     crash the process, so its own regression test
//     (TestBoundedCleanup_RecoversPanicAndAttributesToNamedTest) needs the
//     same substitutable fake, not a smaller assertion surface.
//
// Every real call site, in both cases, passes a genuine *testing.T, which
// satisfies this interface unchanged.
type dsnAssertT interface {
	require.TestingT
	Helper()
}

// pgConnectionTargetQueryKeys is NOT the closed set of every pgconn URL query
// key that can affect WHO or WHERE a connection ends up as - see the two
// documented gaps below. It is the set of keys that can redirect the
// AUTHORITY-DERIVED target (the host/port/user/database this harness itself
// names in the DSN's userinfo, authority and path), enumerated from the
// pinned github.com/jackc/pgx/v5@v5.9.1 source (pgconn/config.go). Two things
// in that file establish this set:
//
//   - parseURLSettings ends with an unconditional
//     `for k, v := range parsedURL.Query() { settings[k] = v[0] }` (only after
//     name-mapping "dbname" to "database"), so ANY of these keys in the query
//     string overwrites what the URL's userinfo/authority/path already
//     established, with no "path/authority wins" branch anywhere.
//   - ParseConfigWithOptions's notRuntimeParams map (the set pgconn treats as
//     connection settings rather than passthrough RuntimeParams) names host,
//     port, database, user, password, passfile, service, servicefile
//     alongside TLS/kerberos/protocol-tuning keys that do NOT affect which
//     server/db/user is targeted (sslmode, connect_timeout, etc.) and are
//     deliberately left out of this set.
//
// service/servicefile matter even though a service file's own dbname loses
// to a non-empty path (mergeSettings puts connStringSettings, i.e. the query
// string, last - see the doc comment on assertDSNTargetsDatabase below and
// M2 in the 2026-08-27 review): a service block can still redirect host,
// port, or user, none of which either call site below ever sets explicitly.
// passfile matters because pgconn substitutes it for config.Password only
// when the URL carries no password - defence in depth for a future call site
// that omits one.
//
// Two members of the true "affects who/where" set are deliberately left OUT
// of this map, and each is a distinct kind of gap:
//
//   - `options` is NOT in pgconn's notRuntimeParams, so it is NOT
//     intercepted here - it falls through to config.RuntimeParams and is
//     sent verbatim in the startup packet. Postgres accepts `-c NAME=VALUE`
//     there, so `?options=-c%20role%3Dpostgres` changes AS WHOM the session
//     runs (measured: RuntimeParams carries `options:-c role=postgres`, and
//     Postgres applies it at session start). Not an escalation under this
//     threat model - whoever controls RELAY_TEST_DATABASE_URL already
//     controls its credentials - but it is a real member of "AS WHOM" that
//     this set does not name.
//   - `target_session_attrs` IS in notRuntimeParams (so it does NOT leak into
//     RuntimeParams) but is intentionally absent from THIS map: it sets
//     config.ValidateConnect (read-write/read-only/primary/standby), which
//     selects WHICH of config.Fallbacks a multi-host connection actually
//     uses - so with more than one host it does affect where a connection
//     lands, contrary to the parenthetical above that files it as
//     targeting-inert alongside sslmode/connect_timeout.
//
// Separately, note where a multi-host authority's coverage actually comes
// from: `?host=` and friends above are the query-string axis, but a DSN
// whose AUTHORITY itself names multiple hosts
// (postgres://u:p@host1:5432,host2:6666/wanted) is caught by
// assertDSNTargetsDatabase below only INCIDENTALLY - url.URL.Hostname()
// returns the whole comma-joined blob ("host1:5432,host2"), which then
// mismatches cfg.Host (pgx.ParseConfig resolves that to just "host1", with
// "host2" moved into cfg.Fallbacks). The guard never inspects cfg.Fallbacks
// itself; do not "clean up" the Hostname() comparison to tolerate this shape
// without re-adding an explicit Fallbacks check, or the multi-host axis
// reopens silently.
var pgConnectionTargetQueryKeys = map[string]bool{
	"host":        true,
	"port":        true,
	"user":        true,
	"password":    true,
	"dbname":      true,
	"database":    true,
	"service":     true,
	"servicefile": true,
	"passfile":    true,
}

// assertNoConnectionTargetOverride rejects a DSN whose query string carries
// any key in pgConnectionTargetQueryKeys. It exists to be run once, on the
// harness's own base DSN, BEFORE either the admin URL or the per-test URL is
// built from it - closing the whole axis rather than the one member
// (`dbname`) the original H1 finding demonstrated. Checking only cfg.Database
// after the fact (assertDSNTargetsDatabase below) left `?host=`, `?port=`,
// `?user=` and `?password=` free to redirect CREATE DATABASE, DROP DATABASE
// ... WITH (FORCE), migrations and the admin-token seed to a different server
// while that check still reported "postgres, as intended".
//
// Called only from newSharedServiceDSN, i.e. only when RELAY_TEST_DATABASE_URL
// is set. newContainerDSN (the unset/testcontainer path) never calls this: its
// DSN comes from tcpostgres.Run/pg.ConnectionString, not from the environment,
// so there is nothing here for an operator's env var to redirect. That
// exemption is deliberate, not an oversight - do not add a call on the
// container path.
//
// This narrows the set of DSNs RELAY_TEST_DATABASE_URL may legitimately use:
// a unix-socket DSN such as postgres:///wanted?host=/var/run/postgresql is a
// valid pgconn spelling (pgconn treats a leading-slash `host` value as a
// socket directory) and is now rejected, because `host` is in
// pgConnectionTargetQueryKeys regardless of what value it carries. Failing
// closed on that shape is arguably the right tradeoff - a socket path is
// itself a redirection vector - but it is a newly narrowed input surface
// relative to before this guard existed, and is recorded here so it is not
// mistaken for an oversight if someone hits it.
func assertNoConnectionTargetOverride(t dsnAssertT, parsedURL *url.URL) {
	t.Helper()
	for k := range parsedURL.Query() {
		// pgConnectionTargetQueryKeys is lowercase-only, and pgconn's own
		// nameMap/notRuntimeParams are exact-case lowercase maps too - pgx
		// does not honour a mixed-case key like `HOST` either, so it falls
		// through to RuntimeParams and the connection dies on an unrecognized
		// startup parameter. That means case is harmless to pgx today, but
		// only because of Postgres's happenstance rejection, not because this
		// guard's own assertion closes the axis - normalize case here so the
		// guard is the thing doing the rejecting.
		//
		// TrimSpace closes a second, narrower gap: `?host+=x` decodes (net/
		// url treats a literal `+` in the query STRING as an encoded space,
		// RFC 1866) to the key "host " with a trailing space, which ToLower
		// alone leaves unmatched against the map's "host". This closes case
		// and whitespace padding; it does not attempt every OTHER
		// non-canonical spelling pgconn might tolerate (a stray query-string
		// separator variant, for instance) - those remain pgx-inert by
		// construction, per pgConnectionTargetQueryKeys's own doc comment: a
		// spelling this guard's exact-match lookup misses is, by the same
		// exact-match logic, also a spelling pgconn's nameMap/notRuntimeParams
		// miss, so it falls through to RuntimeParams and Postgres rejects it
		// as an unrecognized startup parameter rather than silently applying
		// it.
		require.False(t, pgConnectionTargetQueryKeys[strings.ToLower(strings.TrimSpace(k))],
			"%s must not carry a %q query parameter - it can override which "+
				"server, database, or user this harness's CREATE/DROP DATABASE "+
				"and migrations target", dsnEnvVar, k)
	}
}

// assertDSNTargetsDatabase parses dsn and requires that it will actually
// connect to wantDB, as wantUser, at wantHost:wantPort.
//
// This exists because pgx's parseURLSettings sets settings["database"] from
// the URL PATH and then unconditionally iterates the URL's query string and
// OVERWRITES it - dbname is name-mapped onto database, and there is no "path
// wins" branch. So building a DSN by only reassigning url.URL.Path (as both
// call sites below do) is not enough to know, let alone guarantee, which
// database it opens: a stray `?dbname=` (or `?database=`) in the supplied
// RELAY_TEST_DATABASE_URL silently wins over the path. Confirmed with a
// standalone pgx program:
// pgx.ParseConfig("postgres://u:p@h:5432/wanted?dbname=attacker").Database
// == "attacker". `?service=` is NOT an instance of this - a service file's
// dbname is merged in BEFORE connStringSettings (mergeSettings(defaultSettings,
// envSettings, serviceSettings, connStringSettings)), so it can never beat a
// path-derived database; only an empty path would let it win, and neither
// call site below ever builds one. Only pgx.ParseConfig, which is the same
// parser pgx.Connect uses internally, tells the truth about what a DSN will
// do.
//
// Host/port/user are checked here too, as defence in depth alongside
// assertNoConnectionTargetOverride above: that guard closes the query-key
// axis at the harness's one entry point, but this function is what a caller
// actually relies on for the specific DSN it is about to use, and pins the
// same properties independent of how the guard is (or isn't) wired in.
func assertDSNTargetsDatabase(t dsnAssertT, dsn, wantDB, wantHost, wantPort, wantUser string) {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err, "parse dsn as a pgx config")
	require.Equal(t, wantDB, cfg.Database,
		"dsn would connect to database %q, not %q - a query parameter "+
			"(dbname/database) is overriding the intended path", cfg.Database, wantDB)
	// host/port mismatches are reported cause-agnostically: pgconn merges a
	// query parameter, a service file (service/servicefile), and PG*
	// environment variables (PGHOST/PGPORT) into these same two settings (see
	// pgConnectionTargetQueryKeys's doc comment above), so naming "a query
	// parameter" as though it were the only possible cause is itself wrong
	// whenever one of the other two is what actually moved the value - and an
	// operator who greps the DSN for a query string that was never there
	// wastes real time on it. wantHost/wantPort themselves come straight from
	// parsedBase.Hostname()/Port() (see newSharedServiceDSN below), never
	// through pgx.ParseConfig, so unlike wantUser below they carry none of
	// PGHOST/PGPORT/a service file's host or port - a real one of either can
	// therefore genuinely move cfg.Host/cfg.Port away from these wantHost/
	// wantPort values.
	require.Equal(t, wantHost, cfg.Host,
		"dsn would connect to host %q, not %q - a query parameter, a service "+
			"file, or a PG* environment variable is overriding the intended "+
			"host", cfg.Host, wantHost)
	gotPort := strconv.Itoa(int(cfg.Port))
	require.Equal(t, wantPort, gotPort,
		"dsn would connect to port %q, not %q - a query parameter, a service "+
			"file, or a PG* environment variable is overriding the intended "+
			"port", gotPort, wantPort)
	// wantUser, unlike wantHost/wantPort, is derived via wantDefaultUser -
	// either the DSN's own userinfo, or (for a userinfo-less DSN) the same
	// pgx.ParseConfig call this function makes, on a QUERY-STRIPPED copy (see
	// wantDefaultUser's doc comment). A service file's user and PGUSER apply
	// identically to that derivation and to cfg.User's own parse below, since
	// neither side strips those - so neither can ever be the true cause of a
	// mismatch here, and the message says so, unlike host/port's above.
	require.Equal(t, wantUser, cfg.User,
		"dsn would connect as user %q, not %q - a query parameter is "+
			"overriding the intended user", cfg.User, wantUser)
}

// wantDefaultUser returns the user a connection to parsedBase will actually
// authenticate as: the DSN's own userinfo when present, otherwise the same
// default pgconn's defaultSettings() applies (os/user.Current(), stripped of
// a Windows DOMAIN\ prefix - see pgconn/defaults.go and
// pgconn/defaults_windows.go in the pinned jackc/pgx@v5.9.1). Deriving it via
// pgx.ParseConfig, the same entry point pgx.Connect uses, rather than
// hand-rolling a second OS-username lookup, is what keeps this from drifting
// out of sync with pgconn's own default the way the previous zero-value
// wantUser did: assertDSNTargetsDatabase then compared pgconn's real default
// user against an empty string and rejected an ordinary, portless-userinfo
// DSN such as postgres://localhost:5432/postgres.
//
// parsedBase must have already passed assertNoConnectionTargetOverride, so
// its query string carries no key that could redirect host/port/user - this
// parse is safe to lean on for the default this DSN itself left unstated.
//
// The parse is done on a QUERY-STRIPPED copy, deliberately, even though a
// real call site's query has already passed assertNoConnectionTargetOverride
// by the time it gets here. If it parsed parsedBase.String() unstripped, this
// function would derive its default via the exact same pgx.ParseConfig call
// that assertDSNTargetsDatabase's user arm later re-parses to get cfg.User -
// so for a userinfo-less DSN, a `?user=` in the query would move BOTH sides
// of that require.Equal identically and the arm could never fail (2026-08-27
// review, finding M1). Stripping the query here means only pgconn's genuine
// environment/service default survives into wantUser, restoring the arm as
// an independent check rather than a tautology - see
// TestAssertDSNTargetsDatabase_UserArmCatchesQueryOverrideOnNoUserinfoDSN.
//
// PG* environment variables (PGUSER) and a service file's user are NOT
// stripped out here, unlike the query - both still apply, identically, to
// this parse and to the real dsn's later parse in assertDSNTargetsDatabase,
// so they can never be the cause of a mismatch on the user arm and
// assertDSNTargetsDatabase's user message says so.
func wantDefaultUser(t dsnAssertT, parsedBase *url.URL) string {
	t.Helper()
	if u := parsedBase.User.Username(); u != "" {
		return u
	}
	stripped := *parsedBase
	stripped.RawQuery = ""
	cfg, err := pgx.ParseConfig(stripped.String())
	require.NoError(t, err, "parse dsn as a pgx config")
	return cfg.User
}

func newSharedServiceDSN(t *testing.T, base string) string {
	t.Helper()
	parsedBase, parseErr := url.Parse(base)
	// require.NoError would append parseErr.Error() to the failure output, and
	// url.Error.Error() renders as `parse "<url>": <reason>` - the raw URL,
	// password included, verbatim. Strip it down to the underlying reason
	// before handing it to require: the *url.Error wrapper is the only thing
	// carrying the raw string, so unwrapping it once is enough.
	var urlErr *url.Error
	if errors.As(parseErr, &urlErr) {
		parseErr = urlErr.Err
	}
	require.NoError(t, parseErr, "%s must be a URL", dsnEnvVar)
	require.Equal(t, "postgres", parsedBase.Scheme,
		"%s must start with postgres:// (not postgresql://), got %s",
		dsnEnvVar, parsedBase.Redacted())

	// Reject any query key that could redirect where/who this harness's
	// CREATE/DROP DATABASE and migrations target, before either URL below is
	// built from it.
	assertNoConnectionTargetOverride(t, parsedBase)

	wantHost := parsedBase.Hostname()
	wantPort := parsedBase.Port()
	if wantPort == "" {
		wantPort = "5432" // pgconn's default when the DSN carries no port.
	}
	wantUser := wantDefaultUser(t, parsedBase)

	// Connect to /postgres to issue CREATE/DROP DATABASE - the same move
	// web/e2e/ensure-db.mjs makes with adminUrl.pathname. Whatever database the
	// supplied DSN names on its OWN PATH is never touched; a query parameter
	// that overrides that path is caught above, before either connection is
	// opened.
	adminURL := *parsedBase
	adminURL.Path = "/postgres"
	adminDSN := adminURL.String()
	assertDSNTargetsDatabase(t, adminDSN, "postgres", wantHost, wantPort, wantUser)

	nameBytes := make([]byte, 8)
	_, err := rand.Read(nameBytes)
	require.NoError(t, err)
	dbName := testDBPrefix + hex.EncodeToString(nameBytes)
	require.True(t, dbNamePattern.MatchString(dbName),
		"generated database name %q failed its own validation", dbName)

	// Arm the DROP cleanup BEFORE the CREATE is issued, not after. DROP
	// DATABASE IF EXISTS is idempotent and cheap on a name that was never
	// created, so registering it here closes the whole acquire-to-armed
	// window rather than just the two require.NoError calls that used to sit
	// between CREATE and t.Cleanup: a successful CREATE followed by a FailNow
	// (Goexit, so nothing after it runs) previously leaked a relaytest_
	// database on the shared server permanently.
	// created is a LOWER BOUND on whether the CREATE below actually
	// succeeded, not a certainty: execErr == nil proves the server REPORTED
	// success, not merely that a response arrived, and that is not the gap -
	// the gap is the converse, execErr != nil does not prove the CREATE did
	// not commit: CREATE DATABASE can commit server-side while its response
	// is lost (a deadline firing, or the connection resetting, in the window
	// after the server commits but before the client reads the reply). In
	// that window created is false even though the database now exists, which
	// downgrades the cleanup's connect failure below from t.Errorf to
	// t.Logf - silently leaking a relaytest_ database exactly the class the
	// arm-before-CREATE ordering above exists to prevent. The DROP itself is
	// still attempted whenever the cleanup's connect succeeds regardless of
	// created; only the SEVERITY of a connect failure depends on it. Accepted
	// as a lower bound rather than closed, because closing it would mean
	// treating every CREATE as having possibly succeeded, which reintroduces
	// the "flaky server buries the real error" problem this variable exists
	// to solve: a connect failure in the cleanup is only a real problem when a
	// database might exist to drop. Without created, a Postgres that is down
	// or unreachable at CREATE time makes the cleanup connect again, fail
	// again, and t.Errorf a database that was never created - a second,
	// misleading error on top of the real one that buries it under a flaky
	// server.
	var created bool
	t.Cleanup(func() {
		// Fail closed rather than widen: see dbNamePattern.
		if !dbNamePattern.MatchString(dbName) {
			t.Errorf("refusing to drop %q: fails validation for a %s database", dbName, testDBPrefix)
			return
		}
		tctx, tcancel := context.WithTimeout(context.Background(), teardownTimeout)
		defer tcancel()
		conn, err := pgx.Connect(tctx, adminDSN)
		if err != nil {
			if created {
				t.Errorf("connect to drop database %s: %v", dbName, err)
			} else {
				t.Logf("connect to drop database %s: %v (database was never created)", dbName, err)
			}
			return
		}
		defer conn.Close(tctx)
		// WITH (FORCE) terminates leftover sessions (PG13+; both environments
		// run postgres:16), so a pool a test forgot to close cannot wedge the
		// drop.
		if _, err := conn.Exec(tctx,
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop database %s: %v", dbName, err)
		}
	})

	tctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	adminConn, err := pgx.Connect(tctx, adminDSN)
	require.NoError(t, err)
	_, execErr := adminConn.Exec(tctx, `CREATE DATABASE "`+dbName+`"`)
	created = execErr == nil
	closeErr := adminConn.Close(tctx)
	// execErr checked before closeErr: a genuine CREATE failure (out of disk,
	// permission, name collision) must be reported as what it is, not
	// misattributed to Close.
	require.NoError(t, execErr)
	require.NoError(t, closeErr)

	testURL := *parsedBase
	testURL.Path = "/" + dbName
	dsn := testURL.String()
	assertDSNTargetsDatabase(t, dsn, dbName, wantHost, wantPort, wantUser)

	// One migration run per database, exactly as newTestPool already pays per
	// test. golang-migrate takes pg_advisory_lock keyed on the DATABASE NAME
	// (database.GenerateAdvisoryLockId), and every name here is distinct, so
	// concurrent per-database migrations against one server do not serialize.
	// A future TEMPLATE-database optimisation with a fixed name would put them
	// all back on one lock - do not add one without re-checking this.
	require.NoError(t, store.Migrate(migrateDSN(dsn)))
	return dsn
}

// TestIntegration_HarnessDSNIsMigratedAndEmpty is the harness's own test. It
// exists so that a later test's RED is attributable: without it, "the workers
// list test failed" could mean the harness never produced a usable database.
func TestIntegration_HarnessDSNIsMigratedAndEmpty(t *testing.T) {
	dsn := newIntegrationDSN(t)

	conn, err := pgx.Connect(t.Context(), dsn)
	require.NoError(t, err)
	closeCtx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	defer conn.Close(closeCtx)

	// Migrations ran: schema_migrations exists and carries a version.
	var version int64
	require.NoError(t, conn.QueryRow(t.Context(),
		`SELECT version FROM schema_migrations`).Scan(&version))
	require.Positive(t, version)

	// The database is this test's alone. Every list assertion in this lane
	// (Total: 1, one row rendered) is only meaningful against a known-empty
	// database, so this is the property the whole lane rests on.
	for _, table := range []string{"workers", "jobs", "tasks", "users", "task_logs"} {
		var n int64
		require.NoError(t, conn.QueryRow(t.Context(),
			`SELECT count(*) FROM `+table).Scan(&n), "counting %s", table)
		require.Zero(t, n, "table %s must be empty in a fresh test database", table)
	}
}

// fakeDSNAssertT is a minimal dsnAssertT that records failure as a value
// instead of failing the real *testing.T it runs inside. FailNow is
// require's FailNow contract, which real *testing.T implements via
// runtime.Goexit; a fake standing in for it must stop execution the same
// way, so it panics with a sentinel and runAssertDSN recovers only that
// sentinel.
type fakeDSNAssertT struct{ failed bool }

func (f *fakeDSNAssertT) Errorf(string, ...any) { f.failed = true }
func (f *fakeDSNAssertT) FailNow()              { f.failed = true; panic(fakeDSNAssertFailNow{}) }
func (f *fakeDSNAssertT) Helper()               {}

type fakeDSNAssertFailNow struct{}

// runAssertDSN runs assertDSNTargetsDatabase against a fake T and reports
// whether it failed, without that failure propagating to the *testing.T
// calling this helper.
func runAssertDSN(dsn, wantDB, wantHost, wantPort, wantUser string) (failed bool) {
	ft := &fakeDSNAssertT{}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(fakeDSNAssertFailNow); ok {
				failed = true
				return
			}
			panic(r)
		}
	}()
	assertDSNTargetsDatabase(ft, dsn, wantDB, wantHost, wantPort, wantUser)
	return ft.failed
}

// runAssertNoOverride runs assertNoConnectionTargetOverride against a fake T
// and reports whether it failed, using the same non-propagating pattern as
// runAssertDSN above.
func runAssertNoOverride(parsedURL *url.URL) (failed bool) {
	ft := &fakeDSNAssertT{}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(fakeDSNAssertFailNow); ok {
				failed = true
				return
			}
			panic(r)
		}
	}()
	assertNoConnectionTargetOverride(ft, parsedURL)
	return ft.failed
}

// TestAssertDSNTargetsDatabase_CatchesQueryOverridingPath is the harness's
// regression test for the defect assertDSNTargetsDatabase exists to close.
// It needs no Docker and no reachable Postgres: pgx.ParseConfig only parses
// the connection string, it never dials, so this runs in every mode of the
// integration lane in milliseconds.
//
// It drives host=, port=, user= and database= in addition to the original
// dbname= case - assertDSNTargetsDatabase checked only cfg.Database until the
// 2026-08-27 review found that the exact same pgx override loop lets any of
// these four win too (H1): a guard that closes one axis and leaves its
// siblings open is worse than the finding it descends from, since it reports
// success while a different server, port, or user is actually in play.
func TestAssertDSNTargetsDatabase_CatchesQueryOverridingPath(t *testing.T) {
	const goodDSN = "postgres://u:p@example.invalid:5432/wanted"
	const wantDB, wantHost, wantPort, wantUser = "wanted", "example.invalid", "5432", "u"

	require.False(t, runAssertDSN(goodDSN, wantDB, wantHost, wantPort, wantUser),
		"must accept a dsn that actually targets the wanted database, host, port and user")

	require.False(t, runAssertDSN(
		"postgres://u:p@example.invalid:5432/wanted?sslmode=disable&connect_timeout=5&pool_max_conns=3",
		wantDB, wantHost, wantPort, wantUser),
		"must accept ordinary connection-tuning query params alongside a correct path")

	// Characterization: proves the pgx behaviour this whole guard exists for,
	// independent of assertDSNTargetsDatabase itself. If a future pgx upgrade
	// changes this, the guard's premise is gone and this fails loudly rather
	// than the guard silently stopping being necessary or sufficient.
	cfg, err := pgx.ParseConfig("postgres://u:p@example.invalid:5432/wanted?dbname=attacker")
	require.NoError(t, err)
	require.Equal(t, "attacker", cfg.Database,
		"pgx must let dbname win over the path - this is the defect the guard closes, "+
			"not a property assertDSNTargetsDatabase should try to prevent")

	cases := []struct {
		name string
		dsn  string
	}{
		{"dbname", goodDSN + "?dbname=attacker"},
		{"database", goodDSN + "?database=attacker"},
		{"host", goodDSN + "?host=evil.invalid"},
		{"port", goodDSN + "?port=6666"},
		{"user", goodDSN + "?user=root"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.True(t, runAssertDSN(c.dsn, wantDB, wantHost, wantPort, wantUser),
				"assertDSNTargetsDatabase must reject a dsn whose actual %s "+
					"differs from wanted, even though its path/authority names "+
					"the right one", c.name)
		})
	}
}

// TestAssertNoConnectionTargetOverride_RejectsConnectionTargetKeys drives
// every member of pgConnectionTargetQueryKeys - not just the one spelling
// (dbname) the original finding demonstrated - and proves ordinary
// connection-tuning params are left alone. This is the guard H1 added to
// close the axis at the harness's one entry point, ahead of either URL
// (admin or per-test) being built from the supplied base DSN.
func TestAssertNoConnectionTargetOverride_RejectsConnectionTargetKeys(t *testing.T) {
	for key := range pgConnectionTargetQueryKeys {
		t.Run(key, func(t *testing.T) {
			u, err := url.Parse("postgres://u:p@example.invalid:5432/wanted?" + key + "=x")
			require.NoError(t, err)
			require.True(t, runAssertNoOverride(u),
				"must reject a dsn whose query string carries %q", key)
		})
	}

	t.Run("ordinary_tuning_params", func(t *testing.T) {
		u, err := url.Parse("postgres://u:p@example.invalid:5432/wanted" +
			"?sslmode=disable&connect_timeout=5&pool_max_conns=3" +
			"&application_name=cli-lane&search_path=public")
		require.NoError(t, err)
		require.False(t, runAssertNoOverride(u),
			"must accept ordinary connection-tuning query params")
	})

	// Mixed case must not slip past the map lookup. pgconn's own nameMap and
	// notRuntimeParams are exact-case lowercase maps, so pgx does not honour
	// `HOST` either - a mixed-case key currently escapes only because Postgres
	// happens to reject an unrecognized startup parameter, not because this
	// guard's own assertion closes the axis. See the case-fold comment on
	// assertNoConnectionTargetOverride.
	t.Run("mixed_case_key", func(t *testing.T) {
		u, err := url.Parse("postgres://u:p@example.invalid:5432/wanted?HOST=evil.example")
		require.NoError(t, err)
		require.True(t, runAssertNoOverride(u),
			"must reject a dsn whose query string carries a mixed-case connection-target key")
	})

	// Regression test for the 2026-08-27 review's finding L5: `?host+=evil`
	// decodes (net/url treats `+` in a query STRING, not a query VALUE, as an
	// encoded space - RFC 1866 application/x-www-form-urlencoded) to the key
	// "host " (trailing space), which strings.ToLower alone leaves as "host "
	// - a miss on the map lookup that let this padded spelling through
	// unrejected.
	t.Run("padded_key", func(t *testing.T) {
		u, err := url.Parse("postgres://u:p@example.invalid:5432/wanted?host+=evil.example")
		require.NoError(t, err)
		require.True(t, runAssertNoOverride(u),
			"must reject a dsn whose query string carries a connection-target key "+
				"padded with whitespace")
	})
}

// TestAssertDSNTargetsDatabase_AcceptsLegitimateDSNs measures the shapes of
// RELAY_TEST_DATABASE_URL the 2026-08-27 review named as needing to keep
// working after H1: an IPv6 host and a percent-encoded password containing
// both `@` and `/`, on top of the tuning params already covered above.
// Neither assertNoConnectionTargetOverride nor assertDSNTargetsDatabase
// should reject any of these.
func TestAssertDSNTargetsDatabase_AcceptsLegitimateDSNs(t *testing.T) {
	cases := []struct {
		name                         string
		dsn                          string
		wantHost, wantPort, wantUser string
	}{
		{
			name: "ipv6_host", wantHost: "::1", wantPort: "5432", wantUser: "u",
			dsn: "postgres://u:p@[::1]:5432/wanted",
		},
		{
			// Password is p@/pw, percent-encoded so url.Parse does not treat
			// the @ or / as delimiters.
			name:     "percent_encoded_password_with_at_and_slash",
			wantHost: "example.invalid", wantPort: "5432", wantUser: "u",
			dsn: "postgres://u:p%40%2Fpw@example.invalid:5432/wanted",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, err := url.Parse(c.dsn)
			require.NoError(t, err)
			require.False(t, runAssertNoOverride(u),
				"legitimate dsn must not be rejected by the query-key guard")
			require.False(t, runAssertDSN(c.dsn, "wanted", c.wantHost, c.wantPort, c.wantUser),
				"legitimate dsn must pass assertDSNTargetsDatabase")
		})
	}
}

// TestWantDefaultUser_DerivesOSUserWhenDSNCarriesNone is the regression test
// for the 2026-08-27 review's finding A: wantPort got a "5432" default when
// the DSN carries no port, but wantUser got no analogous treatment, so a
// portless-userinfo DSN like postgres://localhost:5432/postgres - a normal
// peer/trust-auth local-dev shape - made newSharedServiceDSN compare pgconn's
// real default user (from os/user.Current(), see pgconn/defaults_windows.go
// and pgconn/defaults.go) against wantUser's zero value and reject the DSN
// with a message blaming a query parameter that was never there.
//
// wantDefaultUser must derive the same default pgx.ParseConfig itself would
// apply, not hand-roll a second, independent OS-username lookup that could
// drift from pgconn's.
func TestWantDefaultUser_DerivesOSUserWhenDSNCarriesNone(t *testing.T) {
	t.Run("no_userinfo", func(t *testing.T) {
		// Pin the environment this test's own expectation (the OS user) must
		// win against. Without this, an entirely ordinary PGUSER/PGSERVICE/
		// PGSERVICEFILE in a developer's or CI's shell (anyone who uses psql
		// sets PGUSER routinely) makes pgx.ParseConfig return THAT value
		// instead of the OS default, and this test goes red for a reason that
		// has nothing to do with wantDefaultUser - a false failure the
		// 2026-08-27 review's finding M2 caught by exporting PGUSER=attacker
		// and watching this red. parseEnvSettings skips empty values, so
		// setting these to "" (not unsetting them) reliably restores the OS
		// default regardless of what the ambient shell carries.
		t.Setenv("PGUSER", "")
		t.Setenv("PGSERVICE", "")
		t.Setenv("PGSERVICEFILE", "")

		u, err := url.Parse("postgres://localhost:5432/wanted")
		require.NoError(t, err)

		osUser, err := user.Current()
		require.NoError(t, err)
		wantOSUsername := osUser.Username
		if idx := strings.LastIndex(wantOSUsername, `\`); idx >= 0 {
			// Windows gives DOMAIN\user or LOCALPCNAME\user; pgconn strips the
			// domain (see defaults_windows.go) and this test must match that,
			// not assert the raw os/user value.
			wantOSUsername = wantOSUsername[idx+1:]
		}

		got := wantDefaultUser(t, u)
		require.Equal(t, wantOSUsername, got,
			"a DSN with no userinfo must derive the same default user pgconn itself applies")
		require.NotEmpty(t, got,
			"an empty wantUser is what let assertDSNTargetsDatabase compare pgconn's real "+
				"default user against a blank and reject a legitimate DSN")
	})

	t.Run("explicit_userinfo_wins", func(t *testing.T) {
		u, err := url.Parse("postgres://explicit-user@localhost:5432/wanted")
		require.NoError(t, err)
		require.Equal(t, "explicit-user", wantDefaultUser(t, u),
			"a DSN that names a user explicitly must not be overridden by the OS default")
	})
}

// TestAssertDSNTargetsDatabase_UserArmCatchesQueryOverrideOnNoUserinfoDSN is
// the regression test for the 2026-08-27 review's finding M1: wantDefaultUser
// used to derive its default by calling pgx.ParseConfig on the FULL dsn,
// query string included - the exact same parse assertDSNTargetsDatabase's
// user arm then compares cfg.User against. For a userinfo-less DSN carrying
// `?user=`, both sides of that require.Equal came from the same parse of the
// same string, so the arm could never fail: wantUser moved with the query
// exactly as far as cfg.User did.
//
// This drives wantDefaultUser directly against a DSN
// TestAssertNoConnectionTargetOverride_RejectsConnectionTargetKeys already
// proves assertNoConnectionTargetOverride rejects, so that this test proves
// the user arm's OWN discrimination, not its upstream guard's - the arm is
// documented as defence in depth "independent of how the guard is (or isn't)
// wired in", and this is what makes that claim true again.
func TestAssertDSNTargetsDatabase_UserArmCatchesQueryOverrideOnNoUserinfoDSN(t *testing.T) {
	const dsn = "postgres://example.invalid:5432/wanted?user=root"
	u, err := url.Parse(dsn)
	require.NoError(t, err)

	wantUser := wantDefaultUser(t, u)
	require.NotEqual(t, "root", wantUser,
		"wantDefaultUser must not itself adopt the query override it exists to let "+
			"assertDSNTargetsDatabase detect")

	require.True(t, runAssertDSN(dsn, "wanted", "example.invalid", "5432", wantUser),
		"assertDSNTargetsDatabase's user arm must reject a dsn whose query overrides "+
			"the user, independent of assertNoConnectionTargetOverride")
}
