//go:build integration

package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
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

// testDBPrefix is asserted immediately before every DROP. The database name is
// GENERATED and never read from the environment, so the DROP can only target
// something this file created; the prefix check is here so that a future edit
// which lets a name in from outside fails closed instead of silently widening
// the blast radius. This is web/e2e/ensure-db.mjs's ALLOWED_DB_NAME lesson
// applied in the direction that matters here - the danger is a plausible
// refactor, not a hostile env value.
const testDBPrefix = "relaytest_"

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
	require.NoError(t, err)
	t.Cleanup(func() {
		tctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
		defer cancel()
		if err := pg.Terminate(tctx); err != nil {
			t.Errorf("terminate postgres container: %v", err)
		}
	})

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(dsn, "postgres://"),
		"testcontainers DSN must start with postgres://, got %q", dsn)
	require.NoError(t, store.Migrate(migrateDSN(dsn)))
	return dsn
}

func newSharedServiceDSN(t *testing.T, base string) string {
	t.Helper()
	require.True(t, strings.HasPrefix(base, "postgres://"),
		"%s must start with postgres:// (not postgresql://), got %q", dsnEnvVar, base)

	// Connect to /postgres to issue CREATE/DROP DATABASE - the same move
	// web/e2e/ensure-db.mjs makes with adminUrl.pathname. Whatever database the
	// supplied DSN names is never touched.
	adminURL, err := url.Parse(base)
	require.NoError(t, err, "%s must be a URL", dsnEnvVar)
	adminURL.Path = "/postgres"
	adminDSN := adminURL.String()

	nameBytes := make([]byte, 8)
	_, err = rand.Read(nameBytes)
	require.NoError(t, err)
	dbName := testDBPrefix + hex.EncodeToString(nameBytes)

	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	adminConn, err := pgx.Connect(ctx, adminDSN)
	require.NoError(t, err)
	_, execErr := adminConn.Exec(ctx, `CREATE DATABASE "`+dbName+`"`)
	require.NoError(t, adminConn.Close(context.Background()))
	require.NoError(t, execErr)

	t.Cleanup(func() {
		// Fail closed rather than widen: see testDBPrefix.
		if !strings.HasPrefix(dbName, testDBPrefix) {
			t.Errorf("refusing to drop %q: not a %s database", dbName, testDBPrefix)
			return
		}
		tctx, tcancel := context.WithTimeout(context.Background(), teardownTimeout)
		defer tcancel()
		conn, err := pgx.Connect(tctx, adminDSN)
		if err != nil {
			t.Errorf("connect to drop database %s: %v", dbName, err)
			return
		}
		defer conn.Close(context.Background())
		// WITH (FORCE) terminates leftover sessions (PG13+; both environments
		// run postgres:16), so a pool a test forgot to close cannot wedge the
		// drop.
		if _, err := conn.Exec(tctx,
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop database %s: %v", dbName, err)
		}
	})

	testURL, err := url.Parse(base)
	require.NoError(t, err)
	testURL.Path = "/" + dbName
	dsn := testURL.String()

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
	defer conn.Close(context.Background())

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
