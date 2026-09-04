// Package pgdsn is the shared Postgres test-database harness for this
// module's integration lanes. It hands a caller either a fresh, unmigrated
// database (NewEmptyDSN) or a fresh, migrated one (NewIntegrationDSN), in one
// of two modes selected by the RELAY_TEST_DATABASE_URL environment variable:
// one Postgres testcontainer per call when it is unset, or one freshly
// CREATEd database per call on the caller-supplied server when it is set -
// the mode .github/workflows/go-ci.yml's Postgres-service jobs use, with no
// Docker API, no image pull, and no Ryuk reaper.
//
// This package is deliberately untagged (no //go:build integration), so that
// the pure DSN-validation guards in pgdsn_guards_test.go run in the default
// lane and under -race. Its one database-touching test carries the tag on
// its own file. Because it is untagged, go build and go vet compile
// testcontainers-go and the testing package into it on every default run -
// both are already go.mod requirements, so this adds no new dependency.
//
// NewEmptyDSN and NewIntegrationDSN must only be called from an
// //go:build integration file: make test is documented as needing no
// Docker, and a caller reached from an untagged test would start pulling
// postgres:16 to satisfy it.
//
// IMPORT-CYCLE CONSTRAINT, PERMANENT: this package imports relay/internal/store
// to run migrations. A future relay/internal/store test file that wants a
// database from here would create an import cycle if it were an in-package
// test (package store); it must be package store_test instead, as every
// store test file except export_test.go already is. Do not "fix" this by
// splitting the harness apart - splitting it is what an import cycle here
// would otherwise force.
package pgdsn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
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
// call, what a developer with Docker gets for free. Set: one freshly CREATEd
// database per call on the supplied server, which is what
// .github/workflows/go-ci.yml's Postgres-service jobs use - no Docker API, no
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

// TeardownTimeout bounds EVERY cleanup step. Unbounded teardown is
// docs/backlog/bug-2026-08-26-integration-lane-times-out-on-docker-teardown:
// a hung Terminate/Close that passes context.Background() and discards the
// error renders as a bare panic:/FAIL with NO TEST NAME ATTACHED. Bounded
// plus t.Errorf makes a hung teardown fail one named test.
const TeardownTimeout = 30 * time.Second

// NewEmptyDSN returns a postgres:// DSN for a fresh, UNMIGRATED database that
// belongs to this call alone, and registers its teardown. Most callers want
// NewIntegrationDSN instead; this exists for the two internal/store callers
// that drive golang-migrate themselves (TestMigrate, whose subject is
// store.Migrate; and the down-migration tests, which need to migrate to a
// specific version rather than latest).
func NewEmptyDSN(t *testing.T) string {
	t.Helper()
	if base := os.Getenv(dsnEnvVar); base != "" {
		return newSharedServiceDSN(t, base)
	}
	return newContainerDSN(t)
}

// NewIntegrationDSN returns a postgres:// DSN for a migrated, empty database
// that belongs to this call alone, and registers its teardown.
func NewIntegrationDSN(t *testing.T) string {
	t.Helper()
	dsn := NewEmptyDSN(t)
	// golang-migrate takes pg_advisory_lock keyed on the database name being
	// migrated (database.GenerateAdvisoryLockId), and each call here migrates
	// a freshly random-named database, so concurrent calls do not serialize.
	// Advisory locks are also database-scoped (the locktag includes the
	// database OID), so even a colliding key would not conflict across two
	// distinct databases. Both properties break only if a future change
	// makes multiple calls migrate the SAME physical database, e.g. a shared,
	// pre-migrated template reused as a CREATE DATABASE ... TEMPLATE source -
	// do not add one without re-checking this.
	require.NoError(t, store.Migrate(MigrateDSN(dsn)))
	return dsn
}

// MigrateDSN rewrites a postgres:// DSN into the pgx5:// form store.Migrate
// requires (see its doc comment in internal/store/migrate.go). Panics if dsn
// does not start with postgres:// - strings.TrimPrefix is a silent no-op on a
// mismatched prefix, so without this check a non-postgres dsn would produce a
// wrong-but-plausible-looking pgx5 DSN instead of a loud failure.
func MigrateDSN(dsn string) string {
	if !strings.HasPrefix(dsn, "postgres://") {
		panic(fmt.Sprintf("pgdsn: MigrateDSN requires a postgres:// dsn, got %q", dsn))
	}
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
			// verbatim.
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
			tctx, cancel := context.WithTimeout(context.Background(), TeardownTimeout)
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
	return dsn
}

// AssertT is the minimal surface two different callers need, for two
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
//   - BoundedCleanup is typed against it for a different reason: there is no
//     way to prove a real *testing.T did NOT crash the process on a panic by
//     making it crash the process, so its own regression test needs the same
//     substitutable fake, not a smaller assertion surface.
//
// Every real call site, in both cases, passes a genuine *testing.T, which
// satisfies this interface unchanged.
type AssertT interface {
	require.TestingT
	Helper()
}

// BoundedCleanup runs fn in a goroutine and fails the named step, via
// t.Errorf, if fn either does not return within TeardownTimeout or panics -
// instead of letting either one hang or crash the whole test binary into the
// nameless panic: banner
// docs/backlog/bug-2026-08-26-integration-lane-times-out-on-docker-teardown
// describes. It exists because pgxpool.Pool.Close and httptest.Server.Close
// both take no context - unlike the pgx.Connect/Exec calls elsewhere in this
// package, which accept one directly, these two have no argument to bound
// with, so a goroutine plus a timeout is the only lever available.
//
// The recover here is load-bearing, not defensive filler: a bare
// t.Cleanup(pool.Close) runs on the test goroutine, so a panic inside it is
// caught by testing's own tRunner and attributed to the named test for free.
// Moving the call onto a bare goroutine loses that for free - a panic on an
// unrecovered goroutine crashes the entire process with no test name
// attached, which is the exact failure this helper exists to eliminate, so
// the recover has to be re-earned here explicitly.
//
// On the hang branch, a goroutine that never returns leaks past the failed
// test; that is an accepted cost of turning an indefinite hang into a named,
// bounded failure. The done channel is buffered so that leaked goroutine's
// eventual send (or, on the panic branch, one that already completed by the
// time the timeout fires) never blocks on a receiver that stopped listening.
func BoundedCleanup(t AssertT, name string, fn func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		fn()
	}()
	select {
	case v := <-done:
		if v != nil {
			t.Errorf("%s panicked: %v", name, v)
		}
	case <-time.After(TeardownTimeout):
		t.Errorf("%s did not complete within %s", name, TeardownTimeout)
	}
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
// harness's own base DSN, BEFORE either the admin URL or the per-call URL is
// built from it - closing the whole axis rather than the one member
// (`dbname`) the original H1 finding demonstrated. Checking only cfg.Database
// after the fact (assertDSNTargetsDatabase below) left `?host=`, `?port=`,
// `?user=` and `?password=` free to redirect CREATE DATABASE, DROP DATABASE
// ... WITH (FORCE), migrations and any admin-token seed to a different server
// while that check still reported "postgres, as intended".
//
// Called only from newSharedServiceDSN, i.e. only when RELAY_TEST_DATABASE_URL
// is set. newContainerDSN (the unset/testcontainer path) never calls this -
// not because its DSN is immune to redirection (tcpostgres.ConnectionString
// builds from DockerProvider.DaemonHost, which does honour
// TESTCONTAINERS_HOST_OVERRIDE/DOCKER_HOST/tc.host), but because that path
// never issues a CREATE or DROP DATABASE - both live only in
// newSharedServiceDSN below - so there is nothing here for a redirected
// target to put at risk. That exemption is deliberate, not an oversight - do
// not add a call on the container path.
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
func assertNoConnectionTargetOverride(t AssertT, parsedURL *url.URL) {
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
func assertDSNTargetsDatabase(t AssertT, dsn, wantDB, wantHost, wantPort, wantUser string) {
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
// out of sync with pgconn's own default the way an earlier zero-value
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
// of that require.Equal identically and the arm could never fail. Stripping
// the query here means only pgconn's genuine environment/service default
// survives into wantUser, restoring the arm as an independent check rather
// than a tautology - see
// TestAssertDSNTargetsDatabase_UserArmCatchesQueryOverrideOnNoUserinfoDSN.
//
// PG* environment variables (PGUSER) and a service file's user are NOT
// stripped out here, unlike the query - both still apply, identically, to
// this parse and to the real dsn's later parse in assertDSNTargetsDatabase,
// so they can never be the cause of a mismatch on the user arm and
// assertDSNTargetsDatabase's user message says so.
func wantDefaultUser(t AssertT, parsedBase *url.URL) string {
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
	// created marks that CREATE was ISSUED, not that it succeeded: CREATE
	// DATABASE can commit server-side while the client's response is lost (a
	// deadline firing, or the connection resetting, between the server's
	// commit and the client's read of the reply), so execErr != nil does not
	// prove nothing was created. Set it immediately before the Exec call, not
	// after checking execErr - checking execErr first hid that lost-response
	// window behind a silent t.Logf instead of a leaked-database t.Errorf -
	// and not before the admin Connect succeeds, which would report a leak
	// for a CREATE that was never attempted (Postgres down or unreachable).
	var created bool
	t.Cleanup(func() {
		// Fail closed rather than widen: see dbNamePattern.
		if !dbNamePattern.MatchString(dbName) {
			t.Errorf("refusing to drop %q: fails validation for a %s database", dbName, testDBPrefix)
			return
		}
		tctx, tcancel := context.WithTimeout(context.Background(), TeardownTimeout)
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
		// run postgres:16), so a pool a caller forgot to close cannot wedge the
		// drop.
		if _, err := conn.Exec(tctx,
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop database %s: %v", dbName, err)
		}
	})

	tctx, cancel := context.WithTimeout(context.Background(), TeardownTimeout)
	defer cancel()
	adminConn, err := pgx.Connect(tctx, adminDSN)
	require.NoError(t, err)
	created = true
	// TEMPLATE template0, not the default template1: template1 allows
	// connections, so a session merely connected to it at this instant makes
	// Postgres refuse the copy with "source database ... is being accessed by
	// other users" (SQLSTATE 55006) - observed directly, intermittently,
	// against a shared server. template0 has datallowconn=false, so nothing
	// can ever be connected to it and the failure class cannot occur.
	_, execErr := adminConn.Exec(tctx, `CREATE DATABASE "`+dbName+`" TEMPLATE template0`)
	closeErr := adminConn.Close(tctx)
	// execErr checked before closeErr: a genuine CREATE failure (out of disk,
	// permission, name collision) must be reported as what it is, not
	// misattributed to Close.
	require.NoError(t, execErr, "pgdsn: CREATE DATABASE %s", dbName)
	require.NoError(t, closeErr)

	testURL := *parsedBase
	testURL.Path = "/" + dbName
	dsn := testURL.String()
	assertDSNTargetsDatabase(t, dsn, dbName, wantHost, wantPort, wantUser)
	return dsn
}
