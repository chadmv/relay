//go:build integration

package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"regexp"
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

// dsnAssertT is the minimal surface assertDSNTargetsDatabase needs. It is an
// interface, not *testing.T, only so its own regression test can substitute
// a fake and observe pass/fail as a returned value - Go's testing package
// marks a parent test permanently failed the moment any t.Run child inside
// it fails, with no way to un-fail it afterward, which would make this
// file's own "this must be rejected" case red forever instead of proving the
// rejection happened. Every real call site passes a genuine *testing.T,
// which satisfies this interface unchanged.
type dsnAssertT interface {
	require.TestingT
	Helper()
}

// assertDSNTargetsDatabase parses dsn and requires that it will actually
// connect to wantDB.
//
// This exists because pgx's parseURLSettings sets settings["database"] from
// the URL PATH and then unconditionally iterates the URL's query string and
// OVERWRITES it - dbname is name-mapped onto database, and there is no "path
// wins" branch. So building a DSN by only reassigning url.URL.Path (as both
// call sites below do) is not enough to know, let alone guarantee, which
// database it opens: a stray `?dbname=` (or `?database=` or `?service=`,
// which resolve to the same setting) in the supplied RELAY_TEST_DATABASE_URL
// silently wins over the path. Confirmed with a standalone pgx program:
// pgx.ParseConfig("postgres://u:p@h:5432/wanted?dbname=attacker").Database
// == "attacker". Only pgx.ParseConfig, which is the same parser
// pgx.Connect uses internally, tells the truth about what a DSN will do.
func assertDSNTargetsDatabase(t dsnAssertT, dsn, wantDB string) {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err, "parse dsn as a pgx config")
	require.Equal(t, wantDB, cfg.Database,
		"dsn would connect to database %q, not %q - a query parameter "+
			"(dbname/database/service) is overriding the intended path", cfg.Database, wantDB)
}

func newSharedServiceDSN(t *testing.T, base string) string {
	t.Helper()
	parsedBase, err := url.Parse(base)
	require.NoError(t, err, "%s must be a URL", dsnEnvVar)
	require.Equal(t, "postgres", parsedBase.Scheme,
		"%s must start with postgres:// (not postgresql://), got %s",
		dsnEnvVar, parsedBase.Redacted())

	// Connect to /postgres to issue CREATE/DROP DATABASE - the same move
	// web/e2e/ensure-db.mjs makes with adminUrl.pathname. Whatever database the
	// supplied DSN names on its OWN PATH is never touched; a query parameter
	// that overrides that path is caught below, before either connection is
	// opened.
	adminURL := *parsedBase
	adminURL.Path = "/postgres"
	adminDSN := adminURL.String()
	assertDSNTargetsDatabase(t, adminDSN, "postgres")

	nameBytes := make([]byte, 8)
	_, err = rand.Read(nameBytes)
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
			t.Errorf("connect to drop database %s: %v", dbName, err)
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
	closeErr := adminConn.Close(tctx)
	// execErr checked before closeErr: a genuine CREATE failure (out of disk,
	// permission, name collision) must be reported as what it is, not
	// misattributed to Close.
	require.NoError(t, execErr)
	require.NoError(t, closeErr)

	testURL := *parsedBase
	testURL.Path = "/" + dbName
	dsn := testURL.String()
	assertDSNTargetsDatabase(t, dsn, dbName)

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
func runAssertDSN(dsn, wantDB string) (failed bool) {
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
	assertDSNTargetsDatabase(ft, dsn, wantDB)
	return ft.failed
}

// TestAssertDSNTargetsDatabase_CatchesQueryOverridingPath is the harness's
// regression test for the defect assertDSNTargetsDatabase exists to close.
// It needs no Docker and no reachable Postgres: pgx.ParseConfig only parses
// the connection string, it never dials, so this runs in every mode of the
// integration lane in milliseconds.
func TestAssertDSNTargetsDatabase_CatchesQueryOverridingPath(t *testing.T) {
	require.False(t, runAssertDSN("postgres://u:p@example.invalid:5432/wanted", "wanted"),
		"must accept a dsn that actually targets the wanted database")

	require.False(t, runAssertDSN(
		"postgres://u:p@example.invalid:5432/wanted?sslmode=disable&connect_timeout=5&pool_max_conns=3",
		"wanted"),
		"must accept ordinary connection-tuning query params alongside a correct path")

	// Characterization: proves the pgx behaviour this whole guard exists for,
	// independent of assertDSNTargetsDatabase itself. If a future pgx upgrade
	// changes this, the guard's premise is gone and this fails loudly rather
	// than the guard silently stopping being necessary or sufficient.
	cfg, err := pgx.ParseConfig("postgres://u:p@example.invalid:5432/wanted?dbname=attacker")
	require.NoError(t, err)
	require.Equal(t, "attacker", cfg.Database,
		"pgx must let dbname win over the path - this is the defect F1 closes, "+
			"not a property assertDSNTargetsDatabase should try to prevent")

	require.True(t, runAssertDSN(
		"postgres://u:p@example.invalid:5432/wanted?dbname=attacker", "wanted"),
		"assertDSNTargetsDatabase must reject a dsn whose actual database "+
			"differs from wanted, even though its path names the right one")
}
