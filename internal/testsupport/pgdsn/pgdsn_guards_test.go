package pgdsn

import (
	"net/url"
	"os/user"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// fakeAssertT is a minimal AssertT that records failure as a value instead of
// failing the real *testing.T it runs inside. FailNow is require's FailNow
// contract, which real *testing.T implements via runtime.Goexit; a fake
// standing in for it must stop execution the same way, so it panics with a
// sentinel and the run* helpers below recover only that sentinel.
type fakeAssertT struct{ failed bool }

func (f *fakeAssertT) Errorf(string, ...any) { f.failed = true }
func (f *fakeAssertT) FailNow()              { f.failed = true; panic(fakeAssertFailNow{}) }
func (f *fakeAssertT) Helper()               {}

type fakeAssertFailNow struct{}

// runAssertDSN runs assertDSNTargetsDatabase against a fake T and reports
// whether it failed, without that failure propagating to the *testing.T
// calling this helper.
func runAssertDSN(dsn, wantDB, wantHost, wantPort, wantUser string) (failed bool) {
	ft := &fakeAssertT{}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(fakeAssertFailNow); ok {
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
	ft := &fakeAssertT{}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(fakeAssertFailNow); ok {
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
// integration lane in milliseconds - and untagged, in the default lane too.
//
// It drives host=, port=, user= and database= in addition to the original
// dbname= case: assertDSNTargetsDatabase checked only cfg.Database until a
// review found that the exact same pgx override loop lets any of these four
// win too - a guard that closes one axis and leaves its siblings open is
// worse than the finding it descends from, since it reports success while a
// different server, port, or user is actually in play.
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
// connection-tuning params are left alone. This is the guard that closes the
// axis at the harness's one entry point, ahead of either URL (admin or
// per-call) being built from the supplied base DSN.
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

	// Regression test: `?host+=evil` decodes (net/url treats `+` in a query
	// STRING, not a query VALUE, as an encoded space - RFC 1866
	// application/x-www-form-urlencoded) to the key "host " (trailing space),
	// which strings.ToLower alone leaves as "host " - a miss on the map
	// lookup that let this padded spelling through unrejected.
	t.Run("padded_key", func(t *testing.T) {
		u, err := url.Parse("postgres://u:p@example.invalid:5432/wanted?host+=evil.example")
		require.NoError(t, err)
		require.True(t, runAssertNoOverride(u),
			"must reject a dsn whose query string carries a connection-target key "+
				"padded with whitespace")
	})
}

// TestAssertDSNTargetsDatabase_AcceptsLegitimateDSNs measures shapes of
// RELAY_TEST_DATABASE_URL that must keep working: an IPv6 host and a
// percent-encoded password containing both `@` and `/`, on top of the tuning
// params already covered above. Neither assertNoConnectionTargetOverride nor
// assertDSNTargetsDatabase should reject any of these.
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
// for the finding that wantPort got a "5432" default when the DSN carries no
// port, but wantUser got no analogous treatment, so a portless-userinfo DSN
// like postgres://localhost:5432/postgres - a normal peer/trust-auth
// local-dev shape - made newSharedServiceDSN compare pgconn's real default
// user (from os/user.Current(), see pgconn/defaults_windows.go and
// pgconn/defaults.go) against wantUser's zero value and reject the DSN with a
// message blaming a query parameter that was never there.
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
		// has nothing to do with wantDefaultUser. parseEnvSettings skips
		// empty values, so setting these to "" (not unsetting them) reliably
		// restores the OS default regardless of what the ambient shell
		// carries.
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
// the regression test for the finding that wantDefaultUser used to derive its
// default by calling pgx.ParseConfig on the FULL dsn, query string included -
// the exact same parse assertDSNTargetsDatabase's user arm then compares
// cfg.User against. For a userinfo-less DSN carrying `?user=`, both sides of
// that require.Equal came from the same parse of the same string, so the arm
// could never fail: wantUser moved with the query exactly as far as cfg.User
// did.
//
// This drives wantDefaultUser directly against a DSN
// TestAssertNoConnectionTargetOverride_RejectsConnectionTargetKeys already
// proves assertNoConnectionTargetOverride rejects, so that this test proves
// the user arm's OWN discrimination, not its upstream guard's - the arm is
// documented as defence in depth "independent of how the guard is (or isn't)
// wired in", and this is what makes that claim true again.
func TestAssertDSNTargetsDatabase_UserArmCatchesQueryOverrideOnNoUserinfoDSN(t *testing.T) {
	// Pin the ambient environment for the same reason
	// TestWantDefaultUser_DerivesOSUserWhenDSNCarriesNone/no_userinfo does: an
	// ordinary PGUSER in the shell (root, say) makes pgx.ParseConfig return
	// that value instead of the OS default, and this test's own "must not
	// equal root" assertion goes red for a reason unrelated to the guard.
	t.Setenv("PGUSER", "")
	t.Setenv("PGSERVICE", "")
	t.Setenv("PGSERVICEFILE", "")

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

// TestBoundedCleanup_RecoversPanicAndAttributesToNamedTest is the regression
// test for the finding that a bare t.Cleanup(pool.Close) running on the test
// goroutine lets a panic inside it be caught by testing's tRunner and
// attributed to the named test - and that moving the call onto a bare
// goroutine with no recover re-creates exactly the failure mode
// BoundedCleanup exists to eliminate: a panic there kills the whole test
// binary with an unrecovered goroutine panic and no test name attached
// (docs/backlog/bug-2026-08-26-integration-lane-times-out-on-docker-teardown).
//
// This uses fakeAssertT rather than a real *testing.T, for the same reason
// that type exists: there is no way to prove a *testing.T did NOT crash the
// process by making it crash the process. It needs no database, so it runs
// alongside the DSN guards above in the untagged lane.
func TestBoundedCleanup_RecoversPanicAndAttributesToNamedTest(t *testing.T) {
	ft := &fakeAssertT{}
	BoundedCleanup(ft, "example.Cleanup", func() {
		panic("boom")
	})
	require.True(t, ft.failed,
		"a panic inside fn must be reported via Errorf on the named step, not left to crash "+
			"the process with no test name attached")
}

// TestMigrateDSN_PanicsOnNonPostgresPrefix pins that a DSN without the
// postgres:// prefix MigrateDSN's doc comment requires cannot silently
// produce a wrong-but-plausible pgx5 DSN. strings.TrimPrefix is a no-op when
// the prefix is absent, so "mysql://x" would otherwise become "pgx5mysql://x"
// - a value that looks like a driver rewrite but names a database that was
// never intended.
func TestMigrateDSN_PanicsOnNonPostgresPrefix(t *testing.T) {
	require.Panics(t, func() { MigrateDSN("mysql://u:p@host/db") })
}

// TestMigrateDSN_RewritesPostgresPrefix pins the documented behavior on a
// well-formed input, so the panic guard above cannot be satisfied by making
// MigrateDSN reject everything.
func TestMigrateDSN_RewritesPostgresPrefix(t *testing.T) {
	require.Equal(t, "pgx5://u:p@host/db", MigrateDSN("postgres://u:p@host/db"))
}
