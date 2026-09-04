# A server-side cost bound on `?q=` text search - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `?q=` text search two server-side bounds - a pool-wide `statement_timeout` and a per-user burst bucket charged inside the two list handlers at the point the needle is known to be non-nil - and correct every document that currently says neither exists.

**Architecture:** One pure parser in `cmd/relay-server` turns `RELAY_DB_STATEMENT_TIMEOUT` into a `statement_timeout` value written into `cfg.ConnConfig.Config.RuntimeParams` before the pool is built, so every pooled connection carries it in its startup packet. One lazily-constructed `rateLimiter` per `*api.Server`, keyed by authenticated user id, is charged from inside `handleListJobs` and `handleListScheduledJobs` immediately after the filters parse returns a non-nil `Q`. Wiring reaches `api.Server` through exported fields, never through `api.New`'s positional list.

**Tech Stack:** Go 1.26, `github.com/jackc/pgx/v5 v5.9.1` (`pgxpool`, `pgconn`), `net/http` + `net/http/httptest`, `testify`, `go/ast` for two structural guards, testcontainers-go for the integration lane.

**Spec:** `docs/superpowers/specs/2026-09-04-text-search-cost-bound.md`
**Closes:** `feature-2026-09-03-server-side-bound-for-text-search`

---

## Slice independence declaration

**This is ONE slice, ONE PR, ONE session. It is NOT a multi-stage plan. Do not run `/backlog phases` on it.**

There is no frontend slice. The only file under `web/` this plan touches is a **comment** in `web/src/jobs/JobsPage.tsx` whose premise (`GET /v1/jobs` carries no rate limit) becomes false; no TSX, no hook, no behaviour and no test under `web/` changes. There is therefore nothing for `relay-frontend-engineer` to run in parallel, and Phase 3 should be a single backend lane.

**Within the plan, two chains are independent of each other** and may be executed by two agents in parallel if the conductor wants the concurrency:

| Chain | Tasks | Files it touches |
|---|---|---|
| **A - statement timeout** | 2, 3, 12 (statement-timeout half) | `cmd/relay-server/dbtimeout_config.go`, `cmd/relay-server/dbtimeout_config_test.go`, `cmd/relay-server/main.go`, `internal/api/db_error_log.go`, `internal/api/jobs.go`, `internal/api/scheduled_jobs.go`, `internal/api/list_query_error_test.go`, `internal/scheduler/notify_statement_timeout_integration_test.go`, `internal/api/statement_timeout_integration_test.go` |
| **B - read bucket** | 4, 5, 6, 7, 8, 9 | `internal/api/ratelimit.go`, `internal/api/server.go`, `internal/api/search_ratelimit.go`, `internal/api/search_ratelimit_test.go`, `internal/api/jobs.go`, `internal/api/scheduled_jobs.go`, `cmd/relay-server/http_server.go`, `cmd/relay-server/main.go`, `cmd/relay-server/search_ratelimit_wiring_test.go` |

They collide in exactly three files - `cmd/relay-server/main.go`, `internal/api/jobs.go`, `internal/api/scheduled_jobs.go` - at different statements. If you run them in parallel, commit with an explicit pathspec (`git commit -- <paths>`), because concurrent agents share one git index.

Tasks 1, 10, 11, 13 and 14 are serial: task 1 is a precondition for everything, tasks 10-14 need both chains landed.

---

## Task 0: Read this before writing any code

### The rebase premise, stated as a premise

This branch **will be rebased onto a sibling lane's merge** - the write-side limiter, `RELAY_JOB_SUBMIT_RATE_LIMIT`. That lane is in PR and is **not in this worktree**. Verified: a search of the whole worktree for `userRateLimitKey`, `UserRateLimit`, `RELAY_JOB_SUBMIT_RATE_LIMIT` and `SubmitLimit` returns **one file, the spec doc**. No Go source defines any of them here.

What that lane is expected to add to `internal/api/ratelimit.go`:

- `func userRateLimitKey(u AuthUser) (string, bool)` - fails closed, so an unidentified caller yields `("", false)` and creates no map key.
- `func UserRateLimit(limit int, window time.Duration) func(http.Handler) http.Handler` - a middleware. **This slice does not use it.** See task 5 for the reason.
- Named fields on `*api.Server` plus threading through `cmd/relay-server/{main,http_server}.go`, plus a README row.

**Your slice reuses `userRateLimitKey` and the existing `rateLimiter` type. It must not re-implement either, and it must not adopt the middleware form.**

- [ ] **Step 1: Establish which world you are in**

Run, from the worktree root:

```bash
grep -rn "func userRateLimitKey" internal/api/
```

- **If it prints a definition:** the rebase has happened. Read the function body and its doc comment. Confirm the signature is exactly `(u AuthUser) (string, bool)` and that the `false` return is produced when the identity is missing (`!u.ID.Valid`), not when it is present. **If the signature differs from what this plan assumes, adjust every call in tasks 5, 6 and 9 to the real signature and say so in the PR body. Do not assume.**
- **If it prints nothing:** the rebase has not happened. Do **not** stop - define it once, exactly as in task 4 step 3, and record in the PR body that the rebase will produce a duplicate-definition conflict which must be resolved by **deleting your copy and keeping the sibling's**.

- [ ] **Step 2: Establish the write side's 429 error string and README row**

```bash
grep -rn "rate limit exceeded" internal/api/ README.md
```

Record two things in the PR body:

1. What string the write bucket returns on 429. This slice returns `search rate limit exceeded`, deliberately distinct from `RateLimit`'s `rate limit exceeded` which login and register already share. If the write side shipped a third spelling, say so and reconcile in one place.
2. Whether any README row for a rate limit makes an **unqualified `Retry-After` claim**. Task 13 corrects it. Measured against this worktree: `ApiError` in `web/src/lib/api.ts` is constructed as `new ApiError(res.status, code, ...)` and carries no headers, and `apiFetch` never reads `res.headers`. **No first-party client reads `Retry-After`.**

- [ ] **Step 3: Note the two facts about this repo that will bite a programmatic edit**

- `git diff` and `git status` disagree here because of `core.autocrlf=true`. After any programmatic edit to a tracked text file, check the diffstat against the size of the change you intended and run `git ls-files --eol` on the touched paths - every one must read `i/lf`.
- Nothing in this repo blocks a merge. `main` has no branch protection. The gate is that you run `make test`, `make test-integration` and `make test-race` locally and do not merge red. **Never describe any check in this plan as a "merge gate".**

---

## Task 1: Verified facts this plan rests on

No code. Read this once; every later task cites it.

- [ ] **Step 1: Confirm the pgx symbols against the module source, not the docs**

The spec says it confirmed `RuntimeParams` and `AfterConnect` "against the pgx v5.9.1 documentation". Both were re-confirmed **against the module source in the local module cache**, which is the stronger instrument:

- `C:/Users/chadv/go/pkg/mod/github.com/jackc/pgx/v5@v5.9.1/pgconn/config.go` declares, on `pgconn.Config`:
  `RuntimeParams map[string]string // Run-time parameters to set on connection as session default values (e.g. search_path or application_name)`
  and its `ParseConfig` initialises the map (`RuntimeParams: make(map[string]string)`) and then copies every DSN setting that is not in its `notRuntimeParams` set into it.
- `C:/Users/chadv/go/pkg/mod/github.com/jackc/pgx/v5@v5.9.1/conn.go` declares `type ConnConfig struct { pgconn.Config; ... }` - an **embedded** `pgconn.Config`, so both `cfg.ConnConfig.RuntimeParams` (promoted) and `cfg.ConnConfig.Config.RuntimeParams` (explicit) compile.
- `C:/Users/chadv/go/pkg/mod/github.com/jackc/pgx/v5@v5.9.1/pgxpool/pool.go` declares `AfterConnect func(context.Context, *pgx.Conn) error` on `pgxpool.Config`, and its own `ParseConfig` reaches the map as `config.ConnConfig.Config.RuntimeParams` - use that spelling, so this code and the library spell one thing one way.
- `go.mod` pins `github.com/jackc/pgx/v5 v5.9.1`. Confirmed.

**Verdict: both symbols exist as the spec describes. `RuntimeParams` is the one to use.** `AfterConnect` would cost a `SET` round trip per new connection plus an error path for a failed `SET`; `RuntimeParams` travels in the startup packet.

- [ ] **Step 2: Confirm the migration-ordering claim**

`cmd/relay-server/main.go` runs, in this order inside `func main`:

1. `migrateDSN := "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(dsn, "postgresql://"), "postgres://")`
2. `store.Migrate(migrateDSN)`
3. `cfg, err := pgxpool.ParseConfig(dsn)`
4. `cfg.MaxConns = int32(dbMaxConns)`
5. `pool, err := pgxpool.NewWithConfig(ctx, cfg)`

`store.Migrate` in `internal/store/migrate.go` calls `migrate.NewWithSourceInstance("iofs", src, dsn)`, which opens its own connection from that DSN.

**Verdict: the claim holds. Migrations run before the pool config exists and cannot be reached by a setting applied to it.** The consequence the spec draws is also real and load-bearing for `idea-2026-09-03-pg-trgm-index-for-text-search`, whose `CREATE INDEX` on a large table would otherwise be killed: **because `migrateDSN` is derived from `dsn` by prefix rewriting, a `statement_timeout` carried IN THE DSN WOULD reach migrations.** That is why this control is applied in Go and must never be documented as "put it in your DSN".

- [ ] **Step 3: Confirm the `statement_timeout = 0` trap and the exact value that rounds to zero**

`statement_timeout = 0` means **disabled** in Postgres. The parameter's default unit is milliseconds, so an integer value is read as milliseconds.

`time.Duration` is nanoseconds and `d.Milliseconds()` truncates toward zero. **Every positive duration strictly below `1ms` renders as `0` and turns the control off. The largest such value is `999999ns`, which `time.ParseDuration` also accepts spelled `999.999µs`.** The spec's `100us` is one member of that set, not its boundary. `1ms` renders as `1` and is safe.

So the refusal condition is exactly `d > 0 && d.Milliseconds() == 0`, and the boundary row your test must carry is **`999999ns` refused, `1ms` accepted**.

- [ ] **Step 4: Confirm `parseFilterQ`'s normalisation, which decides the placement**

`internal/api/list_filters.go`, function `parseFilterQ(w http.ResponseWriter, qs url.Values) (*string, bool)`, in this order:

1. `raw := qs.Get("q")`; `raw == ""` returns `(nil, true)` - **absent**.
2. `!utf8.ValidString(raw)` writes `400 q is not valid UTF-8` and returns `(nil, false)`.
3. `needle := strings.TrimSpace(raw)`; over `maxFilterQRunes` (200) writes `400 q is too long; maximum 200 characters` and returns `(nil, false)`.
4. `needle == ""` returns `(nil, true)` - **absent**. This is the `?q=%20%20` case.
5. otherwise `(&needle, true)`.

**`?q=%20%20` is therefore ABSENT to the server and PRESENT to `r.URL.Query().Get("q") != ""`. That single input is what tells the in-handler placement apart from a middleware predicate**, and it is the discriminating input of the whole slice.

`parsePage` in `internal/api/pagination.go` parses the query string itself with `url.ParseQuery(r.URL.RawQuery)` and hands the result on as `pp.Query`, precisely so a second parse cannot disagree. `%FF%FE` is valid percent-escaping, so it survives `url.ParseQuery` and reaches step 2 above as an invalid-UTF-8 400.

- [ ] **Step 5: Confirm the README precedence rule and its test**

README, in the "Query-string validation" section, states: *"All three are decided before any endpoint-specific rule. On `GET /v1/jobs` the one exception runs earlier still: the sort-versus-filter `400` below outranks this endpoint's own arity check, so `?sort=name&status=a&status=b` answers with the sort message."*

Its test is **`TestListJobs_SortVersusFilterGuardOutranksArity`** in `internal/api/list_params_rejection_integration_test.go` (integration lane). Placing the bucket after `parseJobFilters` leaves that test untouched, because `parsePage`, the sort-versus-filter guard and `rejectRepeatedParams` have all already run by then. **Do not move any of them.**

- [ ] **Step 6: Confirm the transpose hazard and the wiring route**

`cmd/relay-server/http_server.go`'s `buildHTTPServer` doc comment carries, as a measured fact:

> api.New is positional and takes four same-typed arguments in a row. Swapping loginLimitN/loginLimitWin with registerLimitN/registerLimitWin compiles, and every package stays green; login would then be rate-limited at the registration budget.

Confirmed verbatim. `api.New`'s tail is `loginLimitN int, loginLimitWin time.Duration, registerLimitN int, registerLimitWin time.Duration`. **Do not add a fifth and sixth argument to that run.** Use exported fields, as `AllowSelfRegister`, `Metrics`, `Counters` and `StaticHandler` already do.

Also confirmed: adding new fields to `httpServerDeps` does **not** redden the existing wiring tests. `TestServerCountersIsWiredByMain`'s `wiredDep` table and `countersAssignmentSources` both scope themselves to fields that feed `s.Counters`, and `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` counts `api.CounterSources` fields. Your new fields feed neither. **That is exactly why task 9 must build its own executed guard: nothing existing will notice if the assignment is dropped.**

---

## Task 2: `RELAY_DB_STATEMENT_TIMEOUT` - the parser and the config mutator

**Files:**
- Create: `cmd/relay-server/dbtimeout_config.go`
- Create: `cmd/relay-server/dbtimeout_config_test.go`

Two functions. `parseDBStatementTimeout` is pure and knows nothing about pgx. `applyStatementTimeout` is the only place the `RuntimeParams` key is written, which is what makes the wiring executable in the default lane with no Postgres: `pgxpool.ParseConfig` parses a DSN string offline and never connects.

- [ ] **Step 1: Write the failing tests**

Create `cmd/relay-server/dbtimeout_config_test.go`:

```go
package main

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseDBStatementTimeout_SubMillisecondIsRefused is the headline row and it
// goes FIRST, because a value that rounds to zero is the one failure mode that
// DISABLES the control while looking like a tightening: statement_timeout = 0
// means "no timeout" in Postgres, so `100us` would silently unarm the bound the
// operator believed they had just made aggressive.
//
// 999999ns is the boundary, not 100us: Duration is nanoseconds and
// Milliseconds() truncates, so every positive duration strictly below 1ms
// renders as 0 and 999999ns is the largest of them. The 1ms row is the control -
// without it a parser that refused ALL small values would pass on the row above
// alone.
func TestParseDBStatementTimeout_SubMillisecondIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		want    string
	}{
		{"largest value that truncates to zero", "999999ns", true, ""},
		{"the spec's example", "100us", true, ""},
		{"smallest expressible timeout", "1ms", false, "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDBStatementTimeout("RELAY_DB_STATEMENT_TIMEOUT", tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "RELAY_DB_STATEMENT_TIMEOUT",
					"a refusal must name the variable the operator has to fix")
				assert.Contains(t, err.Error(), "disable",
					"and must say what the accepted-but-rounded value would have DONE, "+
						"or the operator reads it as an arbitrary minimum")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseDBStatementTimeout_Outcomes(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"unset uses the default", "", "30000", false},
		{"explicit zero means do not set the key", "0", "", false},
		{"explicit zero seconds means the same", "0s", "", false},
		{"a plain duration renders as milliseconds", "5s", "5000", false},
		{"minutes render as milliseconds", "2m", "120000", false},
		{"negative is refused", "-5s", "", true},
		{"unparseable is refused", "thirty", "", true},
		{"a bare integer is refused, because Go durations need a unit", "30", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDBStatementTimeout("RELAY_DB_STATEMENT_TIMEOUT", tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The default is DERIVED from the constant, so moving the constant without
// moving README's table row cannot pass here silently.
func TestParseDBStatementTimeout_DefaultIsTheConstant(t *testing.T) {
	got, err := parseDBStatementTimeout("RELAY_DB_STATEMENT_TIMEOUT", "")
	require.NoError(t, err)
	assert.Equal(t, "30000", got)
	assert.Equal(t, int64(30000), defaultDBStatementTimeout.Milliseconds())
}

// TestApplyStatementTimeout_WritesTheRuntimeParam is EXECUTED and needs no
// Postgres: pgxpool.ParseConfig parses a DSN string and never connects.
//
// The two rows are a pair. An armed control must OVERWRITE what the DSN
// supplied - relay's setting wins, and that is a documented decision, not an
// accident - while the disabled value must leave the DSN's own value standing,
// which is the whole point of the escape.
func TestApplyStatementTimeout_WritesTheRuntimeParam(t *testing.T) {
	const dsn = "postgres://relay:relay@127.0.0.1:5432/relay?sslmode=disable&statement_timeout=7s"

	armed, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	require.Equal(t, "7s", armed.ConnConfig.Config.RuntimeParams["statement_timeout"],
		"fixture: pgx must carry a DSN-supplied runtime parameter through ParseConfig, or the "+
			"overwrite row below proves nothing")
	applyStatementTimeout(armed, "30000")
	assert.Equal(t, "30000", armed.ConnConfig.Config.RuntimeParams["statement_timeout"],
		"relay's setting must win over the DSN's; that precedence is documented in README")

	disabled, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	applyStatementTimeout(disabled, "")
	assert.Equal(t, "7s", disabled.ConnConfig.Config.RuntimeParams["statement_timeout"],
		"the empty value means relay does not touch the key at all, leaving whatever the DSN, the "+
			"role or the server default provides")
}

// A DSN with no statement_timeout must not gain the key when the control is
// disabled. Asserting the key is ABSENT, not that it is empty: an empty string
// would reach the startup packet as `statement_timeout=`, which is not the same
// thing as not sending it.
func TestApplyStatementTimeout_DisabledAddsNoKey(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://relay:relay@127.0.0.1:5432/relay?sslmode=disable")
	require.NoError(t, err)
	applyStatementTimeout(cfg, "")
	_, present := cfg.ConnConfig.Config.RuntimeParams["statement_timeout"]
	assert.False(t, present, "a disabled control must send no key at all")
}

// The disabled line must name the control as UNARMED. A silent disable is the
// failure this whole parser exists to make impossible, and a log line that reads
// like the armed one is a silent disable with extra steps.
func TestDBStatementTimeoutLine(t *testing.T) {
	armed := dbStatementTimeoutLine("30000")
	assert.Contains(t, armed, "30000")
	assert.NotContains(t, strings.ToLower(armed), "not set")

	off := dbStatementTimeoutLine("")
	assert.Contains(t, off, "RELAY_DB_STATEMENT_TIMEOUT")
	assert.Contains(t, strings.ToLower(off), "not set",
		"an operator scanning the boot log must be able to see that this control is off")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/relay-server/ -run 'TestParseDBStatementTimeout|TestApplyStatementTimeout|TestDBStatementTimeoutLine' -v -timeout 60s`
Expected: FAIL to build - `undefined: parseDBStatementTimeout`, `undefined: applyStatementTimeout`, `undefined: dbStatementTimeoutLine`, `undefined: defaultDBStatementTimeout`.

**A build failure is not a RED you can bank.** It proves the symbols are absent, nothing more. The RED that matters is step 4's: after the functions exist and before the sub-millisecond branch does.

- [ ] **Step 3: Write the implementation, deliberately WITHOUT the sub-millisecond branch**

Create `cmd/relay-server/dbtimeout_config.go`:

```go
package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultDBStatementTimeout is what an unset RELAY_DB_STATEMENT_TIMEOUT resolves
// to. It is NOT the control that bounds a ?q= scan at today's table size - the
// measured no-match needle is about 283 ms - and nobody should read it as one.
// Its job is to turn "a statement can hold a pool connection indefinitely if the
// table grows, the plan flips, or the box is contended" into a bounded hold, for
// every statement in the system rather than for two handlers.
const defaultDBStatementTimeout = 30 * time.Second

// parseDBStatementTimeout resolves RELAY_DB_STATEMENT_TIMEOUT into the value to
// write into the pool's statement_timeout runtime parameter, or "" meaning
// relay sets no key at all.
//
// FAIL-CLOSED ON A BAD VALUE, unlike RELAY_TASK_WATCHDOG_MARGIN next door, and
// the difference is the direction the mistake fails in. A watchdog bound that
// falls back to a default is still a bound. Here the value travels into a
// startup packet and pgxpool.NewWithConfig does not necessarily establish a
// connection eagerly, so a malformed runtime parameter surfaces as a connection
// error at the FIRST QUERY, minutes into a deploy, rather than at boot. The
// caller turns the error into a log.Fatalf, as parsePublicURL and ParseRateLimit
// already do.
//
// A DURATION THAT ROUNDS TO ZERO MILLISECONDS IS REFUSED, not clamped. Postgres
// reads statement_timeout = 0 as DISABLED, so `100us` - anything positive below
// 1ms, of which 999999ns is the largest - would render as `0` and turn the
// control OFF while reading like the most aggressive setting available. That is
// the one input where accepting the operator's value silently does the opposite
// of what they asked for.
//
// An explicit `0` returns "", meaning relay does not set the key and leaves
// whatever the DSN, the role or the server default provides. That is an escape
// for a deployment managing the setting elsewhere. It is NOT a tuning knob and
// must never appear in a remedy list beside "raise the value".
func parseDBStatementTimeout(name, raw string) (string, error) {
	if raw == "" {
		return strconv.FormatInt(defaultDBStatementTimeout.Milliseconds(), 10), nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return "", fmt.Errorf("%s is not a Go duration (try 30s, 500ms, 2m)", name)
	}
	if d < 0 {
		return "", fmt.Errorf("%s must not be negative", name)
	}
	if d == 0 {
		return "", nil
	}
	return strconv.FormatInt(d.Milliseconds(), 10), nil
}

// applyStatementTimeout writes param into the pool config's session runtime
// parameters, or leaves the key untouched when param is "".
//
// RuntimeParams rather than pgxpool.Config.AfterConnect: pgx sends these in the
// startup packet, so every pooled connection carries the setting from the moment
// it is established, with no extra round trip and no code path where a failed
// SET has to be handled. pgxpool.ParseConfig guarantees the map is non-nil - its
// own body reads and deletes from it - so a config that came from anywhere else
// is a programming error and should panic here rather than silently drop the
// bound.
//
// SETTING THE KEY OVERWRITES WHATEVER THE DSN SUPPLIED, deliberately: relay's
// setting wins, and the escape for a deployment that manages the timeout at the
// DSN or role level is the "" value, not a merge rule.
//
// THIS DOES NOT REACH MIGRATIONS AND MUST NOT. store.Migrate opens its own
// connection through golang-migrate before main ever calls pgxpool.ParseConfig,
// so a CREATE INDEX that runs for minutes on a large table is unaffected. That
// is also why this is applied in Go and never documented as "put
// statement_timeout in your DSN": migrateDSN is derived from dsn by prefix
// rewriting, so a DSN-carried timeout WOULD reach migrations.
func applyStatementTimeout(cfg *pgxpool.Config, param string) {
	if param == "" {
		return
	}
	cfg.ConnConfig.Config.RuntimeParams["statement_timeout"] = param
}

// dbStatementTimeoutLine renders the unconditional startup line. A fail-closed
// parser plus a silent success is half a control: the disabled state has to be
// visible in the boot log, because it is the state in which nothing else in the
// system will ever mention this bound again.
func dbStatementTimeoutLine(param string) string {
	if param == "" {
		return "database statement timeout: NOT SET by relay (RELAY_DB_STATEMENT_TIMEOUT=0), so a " +
			"statement's runtime is bounded only by whatever the DSN, the role or the server default " +
			"provides. A single query can hold a pool connection for as long as it runs."
	}
	return fmt.Sprintf("database statement timeout: %sms on every pooled connection "+
		"(RELAY_DB_STATEMENT_TIMEOUT). A statement exceeding it is cancelled by the server and the "+
		"request answers 500.", param)
}
```

- [ ] **Step 4: Run the tests and observe the REAL RED**

Run: `go test ./cmd/relay-server/ -run 'TestParseDBStatementTimeout|TestApplyStatementTimeout|TestDBStatementTimeoutLine' -v -timeout 60s`

Expected: **`TestParseDBStatementTimeout_SubMillisecondIsRefused/largest_value_that_truncates_to_zero` and `/the_spec's_example` FAIL** - they got `("0", nil)` where they wanted an error. Every other subtest passes.

**That is the RED this task exists for**, and it is the exposure itself: the parser as written above accepts `100us` and hands `"0"` to `applyStatementTimeout`, which writes `statement_timeout=0` into the startup packet and disables the control. Copy the failure output into the commit message.

- [ ] **Step 5: Add the sub-millisecond refusal**

In `parseDBStatementTimeout`, insert between the `d == 0` branch and the `return`:

```go
	// Postgres reads statement_timeout = 0 as DISABLED. Duration is nanoseconds
	// and Milliseconds() truncates, so every positive value below 1ms renders as
	// "0" - 999999ns is the largest of them - and would unarm the control while
	// reading as the tightest setting on offer. Refuse rather than clamp: an
	// operator who wrote 100us meant something this parameter cannot express.
	if d.Milliseconds() == 0 {
		return "", fmt.Errorf(
			"%s=%s is below 1ms, which Postgres would round to 0 and read as DISABLE. "+
				"Use 1ms or more, or exactly 0 if you meant to disable it.", name, d)
	}
```

Note `%s` of `d` renders the parsed duration, not the raw environment string. That is deliberate: the raw value is operator input and this message goes to a stderr that is usually shipped somewhere with broader read access than the environment had. `d` is a `time.Duration` and can carry nothing but a number and a unit.

- [ ] **Step 6: Run the tests and verify they pass**

Run: `go test ./cmd/relay-server/ -run 'TestParseDBStatementTimeout|TestApplyStatementTimeout|TestDBStatementTimeoutLine' -v -timeout 60s`
Expected: PASS, every subtest.

- [ ] **Step 7: Run the mutation battery with a green baseline and a control**

Baseline first: `go test ./cmd/relay-server/ -count=1 -timeout 300s` must be **green** before any mutation. A battery run against a red baseline reports uniform results and proves nothing.

Then, one at a time, restoring from a **copy** between each (never `git checkout --`, which would discard the uncommitted guard under test):

| # | Mutation | Must redden | Branch it reddens |
|---|---|---|---|
| M2a | delete the `d.Milliseconds() == 0` block | `TestParseDBStatementTimeout_SubMillisecondIsRefused` rows 1 and 2 | the sub-millisecond refusal |
| M2b | change `if param == "" { return }` to `if param == "-" { return }` in `applyStatementTimeout` | `TestApplyStatementTimeout_DisabledAddsNoKey` and the second half of `..._WritesTheRuntimeParam` | the disabled escape |
| M2c | change the assignment to `cfg.ConnConfig.Config.RuntimeParams["statement_timeouts"] = param` | `TestApplyStatementTimeout_WritesTheRuntimeParam` first half | the parameter NAME, which nothing else in the repo spells |
| M2d | `return "", nil` as the first line of `parseDBStatementTimeout` | `TestParseDBStatementTimeout_Outcomes` and `..._DefaultIsTheConstant` | **control - this must die.** If it survives, the harness is not running these tests and every row above is meaningless |

Record each kill by the **named failing test and the branch it belongs to**, not by "the package went red". A mutation may redden a test for a different guard.

- [ ] **Step 8: Commit**

```bash
git add cmd/relay-server/dbtimeout_config.go cmd/relay-server/dbtimeout_config_test.go
git commit -m "Parse RELAY_DB_STATEMENT_TIMEOUT, refusing a value Postgres would read as disabled"
```

---

## Task 3: Wire the statement timeout into the pool, with a guard on the ordering

**Files:**
- Modify: `cmd/relay-server/main.go` (the block between `dbMaxConns` and `pgxpool.NewWithConfig`)
- Modify: `cmd/relay-server/dbtimeout_config_test.go` (add the ordering guard)

`main` is not executable from a test. The executable half of this control is already guarded by task 2's `TestApplyStatementTimeout_*`. What is left is a question only a parse can answer: does `main` call `applyStatementTimeout`, exactly once, with the parsed value, **before** the pool is built? A call placed after `pgxpool.NewWithConfig` compiles, vets clean and leaves every package green while the control is entirely absent.

- [ ] **Step 1: Write the failing guard**

Append to `cmd/relay-server/dbtimeout_config_test.go` (and add `"go/ast"`, `"go/parser"`, `"go/token"` to its imports):

```go
// TestStatementTimeoutIsAppliedBeforeThePoolIsBuilt is a PARSED guard, and it is
// parsed because nothing else can be: main ends in log.Fatalf, which no test can
// call, and the pool it builds needs a database.
//
// SAY WHAT IT DOES AND DOES NOT COVER. It covers the two shapes that silently
// unarm this control while compiling and vetting clean: the call being deleted,
// and the call being moved BELOW pgxpool.NewWithConfig, where it mutates a
// config the pool has already copied. It does NOT cover the value: whether the
// second argument is the parsed timeout rather than some other string is not
// checked here, and is checked by nothing. It also does not cover
// applyStatementTimeout's own body, which is EXECUTED in
// TestApplyStatementTimeout_WritesTheRuntimeParam above - that is the stronger
// half, and this guard exists only to prove main reaches it.
func TestStatementTimeoutIsAppliedBeforeThePoolIsBuilt(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	var body *ast.BlockStmt
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "main" && fd.Recv == nil && fd.Body != nil {
			body = fd.Body
		}
	}
	require.NotNil(t, body, "main.go no longer declares func main with a body")

	// Statement INDEX, not source line: the two calls must be direct statements
	// of main's own body, so a call nested inside an if - the natural shape for
	// "only apply it when configured" - is not found at all and this fails.
	applyAt, poolAt := -1, -1
	for i, st := range body.List {
		found := map[string]bool{}
		ast.Inspect(st, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := ce.Fun.(type) {
			case *ast.Ident:
				found[fn.Name] = true
			case *ast.SelectorExpr:
				found[fn.Sel.Name] = true
			}
			return true
		})
		if found["applyStatementTimeout"] {
			require.Equal(t, -1, applyAt,
				"main calls applyStatementTimeout more than once; the last one decides and this guard "+
					"cannot say which config it mutated")
			applyAt = i
		}
		if found["NewWithConfig"] {
			require.Equal(t, -1, poolAt, "main builds more than one pool")
			poolAt = i
		}
	}

	require.NotEqual(t, -1, applyAt,
		"main never calls applyStatementTimeout as a direct statement of its own body, so "+
			"RELAY_DB_STATEMENT_TIMEOUT reaches no connection. The variable would still parse, the "+
			"startup line would still print, and nothing at all would be bounded.")
	require.NotEqual(t, -1, poolAt, "main no longer calls pgxpool.NewWithConfig")
	require.Less(t, applyAt, poolAt,
		"applyStatementTimeout runs at statement %d and the pool is built at statement %d. Mutating "+
			"the config after NewWithConfig has copied it is a no-op that compiles, vets clean and "+
			"leaves every package green.", applyAt, poolAt)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/relay-server/ -run TestStatementTimeoutIsAppliedBeforeThePoolIsBuilt -v -timeout 60s`
Expected: FAIL with *"main never calls applyStatementTimeout as a direct statement of its own body"*.

- [ ] **Step 3: Wire it in `main.go`**

In `cmd/relay-server/main.go`, replace the existing block:

```go
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatalf("parse dsn: %v", err)
	}
	cfg.MaxConns = int32(dbMaxConns)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
```

with:

```go
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
```

- [ ] **Step 4: Run the guard and the package**

Run: `go test ./cmd/relay-server/ -run TestStatementTimeoutIsAppliedBeforeThePoolIsBuilt -v -timeout 60s`
Expected: PASS.

Run: `go test ./cmd/relay-server/ -count=1 -timeout 300s`
Expected: PASS. In particular `TestServerCountersIsWiredByMain` must stay green - `applyStatementTimeout(cfg, statementTimeout)` is an `ExprStmt`, not an `AssignStmt`, so it adds nothing to that test's `assignedAnywhere` counts, and `statementTimeout` is assigned exactly once.

- [ ] **Step 5: Mutation checks**

Baseline green, then one at a time:

| # | Mutation | Must redden |
|---|---|---|
| M3a | delete the `applyStatementTimeout(cfg, statementTimeout)` line | `TestStatementTimeoutIsAppliedBeforeThePoolIsBuilt` on "never calls" |
| M3b | move that line to immediately after `pool, err := pgxpool.NewWithConfig(...)` | the same test on the ordering message |
| M3c | wrap it in `if statementTimeout != "" { ... }` | the same test on "never calls", because a nested call is not a direct statement of main's body |
| M3d | **control:** rename `applyStatementTimeout` at its definition and both call sites consistently | **compile error - this is neither a kill nor a survival. Discard it and do not count it.** |

Replace M3d with a real control: change `require.Less(t, applyAt, poolAt, ...)` to `require.NotEqual` and confirm M3b then **survives**. Restore. That proves the ordering assertion is the thing doing the work and not the presence check.

- [ ] **Step 6: Commit**

```bash
git add cmd/relay-server/main.go cmd/relay-server/dbtimeout_config_test.go
git commit -m "Apply the statement timeout to every pooled connection, before the pool is built"
```

---

## Task 4: The read bucket's state on `*api.Server`

**Files:**
- Modify: `internal/api/server.go` (the `Server` struct only - **do not touch `New`**)
- Create: `internal/api/search_ratelimit.go`
- Create: `internal/api/search_ratelimit_test.go`
- Modify (only if task 0 step 1 found it absent): `internal/api/ratelimit.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/search_ratelimit_test.go`:

```go
package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func searchTestUser(b byte) AuthUser {
	var raw [16]byte
	raw[15] = b
	return AuthUser{ID: pgtype.UUID{Bytes: raw, Valid: true}, Email: "caller@example.com"}
}

// ONE LIMITER PER SERVER, not one per call. RateLimit mints a fresh rateLimiter
// and starts an unstoppable `go rl.gcLoop()` on every invocation, so a Server
// whose Handler() ran twice would leak a goroutine and split its budget across
// two maps. The read bucket must not reproduce that, and the only way to see it
// from a test is to ask for the limiter twice and compare identities.
func TestSearchLimiter_IsConstructedOncePerServer(t *testing.T) {
	s := &Server{SearchLimitN: 3, SearchLimitWin: time.Minute}
	assert.Same(t, s.searchRateLimiter(), s.searchRateLimiter(),
		"a second call must return the same limiter; two maps means two budgets")
}

// The zero value means NO LIMIT, which is what keeps every existing
// construction of api.Server - including every test in this package - unchanged.
func TestSearchLimiter_ZeroFieldsDisableTheBucket(t *testing.T) {
	for _, s := range []*Server{
		{},
		{SearchLimitN: 3},
		{SearchLimitWin: time.Minute},
		{SearchLimitN: -1, SearchLimitWin: time.Minute},
	} {
		assert.Nil(t, s.searchRateLimiter(),
			"N=%d win=%s must leave the bucket unarmed", s.SearchLimitN, s.SearchLimitWin)
	}
}

// allowSearch is the whole decision, in one place, so the two handlers cannot
// drift. The FIRST row is the discriminator: an unidentified caller is refused
// with 401 and creates NO map key at all, which is a real security property of
// failing closed and not only a status code - an unauthenticated flood must not
// be able to grow the limiter's map.
func TestAllowSearch_UnidentifiedCallerIs401AndCreatesNoKey(t *testing.T) {
	s := &Server{SearchLimitN: 2, SearchLimitWin: time.Minute}
	rl := s.searchRateLimiter()
	require.NotNil(t, rl)

	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		require.False(t, s.allowSearch(rec, AuthUser{}), "attempt %d", i)
		require.Equal(t, 401, rec.Code, "attempt %d", i)
		assert.JSONEq(t, `{"error":"unauthorized"}`, rec.Body.String())
	}

	rl.mu.Lock()
	keys := len(rl.windows)
	rl.mu.Unlock()
	assert.Zero(t, keys,
		"a fail-closed key creates no map entry, so an unidentified flood cannot grow the limiter's "+
			"map. That bound is what makes this limiter's key space different from the IP-keyed ones.")
}

func TestAllowSearch_ChargesTheCeilingPerUser(t *testing.T) {
	s := &Server{SearchLimitN: 2, SearchLimitWin: time.Minute}

	alice, bob := searchTestUser(1), searchTestUser(2)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		assert.True(t, s.allowSearch(rec, alice), "alice attempt %d must be allowed", i)
		assert.Equal(t, 200, rec.Code, "an allowed call writes nothing, so the recorder keeps its default")
	}

	rec := httptest.NewRecorder()
	require.False(t, s.allowSearch(rec, alice))
	assert.Equal(t, 429, rec.Code)
	assert.JSONEq(t, `{"error":"search rate limit exceeded"}`, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("Retry-After"),
		"correct HTTP for a scripted client. No first-party client reads it - ApiError carries no "+
			"headers and apiFetch never touches res.headers - and README says so where the header is "+
			"documented.")

	other := httptest.NewRecorder()
	assert.True(t, s.allowSearch(other, bob),
		"the key is the user id, so a second principal has its own budget")
}

// An unarmed bucket must let everything through INCLUDING an unidentified
// caller, or every existing zero-valued test Server changes behaviour.
func TestAllowSearch_UnarmedBucketAllowsEverything(t *testing.T) {
	s := &Server{}
	for _, u := range []AuthUser{searchTestUser(1), {}} {
		rec := httptest.NewRecorder()
		assert.True(t, s.allowSearch(rec, u))
		assert.Equal(t, 200, rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api/ -run 'TestSearchLimiter|TestAllowSearch' -v -timeout 60s`
Expected: FAIL to build - `s.SearchLimitN undefined`, `s.searchRateLimiter undefined`, `s.allowSearch undefined`.

- [ ] **Step 3: If and only if task 0 step 1 found `userRateLimitKey` absent, add it**

Append to `internal/api/ratelimit.go`:

```go
// userRateLimitKey renders the bucket key for an authenticated principal, and
// FAILS CLOSED: a caller with no resolved identity yields ok=false and the
// caller must refuse the request rather than fall back to a shared key.
//
// The consequence is a bounded key space, unlike the IP-keyed limiters: an
// unauthenticated or unidentified flood creates ZERO map entries, so the map is
// bounded by the number of distinct users who acted inside the window and
// gcOnce prunes it.
func userRateLimitKey(u AuthUser) (string, bool) {
	if !u.ID.Valid {
		return "", false
	}
	return uuidStr(u.ID), true
}
```

**If the symbol already exists, skip this step entirely and delete nothing.** If you did add it, put the following line in the PR body verbatim: *"userRateLimitKey was defined in this branch because the write-side limiter had not merged. On rebase, resolve the duplicate definition by deleting this copy and keeping the sibling's."*

- [ ] **Step 4: Add the fields to `Server`**

In `internal/api/server.go`, add `"sync"` to the imports and insert after the `StaticHandler` field:

```go
	// SearchLimitN and SearchLimitWin bound how many ?q= text searches ONE
	// AUTHENTICATED PRINCIPAL may issue per window. Set by cmd/relay-server's
	// buildHTTPServer from RELAY_JOB_SEARCH_RATE_LIMIT. Either field at or below
	// zero leaves the bucket unarmed, which is the only disabled state and is
	// deliberately Go-reachable only: ParseRateLimit rejects a zero count and
	// main is fatal on it, so an operator cannot turn the control off from the
	// environment. The escape is a large number.
	//
	// Exported FIELDS rather than two more arguments on New. New's tail is
	// already four same-typed arguments in a row and buildHTTPServer's own doc
	// comment records, as a measured fact, that transposing two of them compiles
	// and leaves every package green.
	//
	// NOT A CPU BUDGET. At the measured ~283 ms per no-match needle, 120 per 10 s
	// is more database time per second than the box has. It is a fairness bound:
	// it stops one principal monopolizing the connection pool, and leaves the
	// pool itself as the concurrency ceiling it has always been.
	SearchLimitN   int
	SearchLimitWin time.Duration

	// searchLimiterOnce guards ONE limiter per Server. RateLimit mints a fresh
	// rateLimiter and starts an unstoppable gcLoop on every call, so a Server
	// whose Handler() ran twice would leak a goroutine and split its budget over
	// two maps. TestSearchLimiter_IsConstructedOncePerServer pins this.
	searchLimiterOnce sync.Once
	searchLimiter     *rateLimiter
```

- [ ] **Step 5: Write the decision helper**

Create `internal/api/search_ratelimit.go`:

```go
package api

import (
	"net/http"
	"strconv"
)

// searchRateLimiter returns this Server's single read bucket, or nil when the
// control is unarmed. Constructed lazily because the limits arrive as exported
// fields set after New, and once because a second instance would be a second
// budget and a second unstoppable gcLoop goroutine.
func (s *Server) searchRateLimiter() *rateLimiter {
	s.searchLimiterOnce.Do(func() {
		if s.SearchLimitN <= 0 || s.SearchLimitWin <= 0 {
			return
		}
		rl := &rateLimiter{
			windows: make(map[string][]time.Time),
			limit:   s.SearchLimitN,
			window:  s.SearchLimitWin,
		}
		go rl.gcLoop()
		s.searchLimiter = rl
	})
	return s.searchLimiter
}

// allowSearch charges one q-carrying list request to its principal's bucket. It
// returns false with the response already written, and true otherwise, writing
// nothing.
//
// CALLED FROM INSIDE THE HANDLER, at the point parseFilterQ has already returned
// a non-nil needle, and NEVER as middleware. A middleware predicate deciding
// "does this request carry a needle" would be a second implementation of
// parseFilterQ's decision, reading r.URL.Query() again - which discards
// percent-decoding errors, so it can disagree with the parse that was validated.
// It would also disagree on the cases parseFilterQ normalizes: ?q= and ?q=%20%20
// are both ABSENT after the trim, and a middleware testing Get("q") != "" counts
// them. TestListJobs_WhitespaceOnlyQIsNotCounted is that input.
//
// THE 401 IS NOT A COURTESY. The key must fail closed, so a q-carrying request
// with no resolved identity has no bucket to charge and cannot be allowed
// through - allowing it would be the one request nothing bounds. It is
// unreachable through the mux, where GET /v1/jobs is mounted as
// auth(http.HandlerFunc(s.handleListJobs)), and it aligns the jobs list with
// handleListScheduledJobs, which already answers 401 on a missing identity, and
// with mine=true, which already 401s on !u.ID.Valid.
//
// The body is deliberately NOT RateLimit's "rate limit exceeded", which login
// and register already share: a client and an operator reading a log must be
// able to tell which control fired.
func (s *Server) allowSearch(w http.ResponseWriter, u AuthUser) bool {
	rl := s.searchRateLimiter()
	if rl == nil {
		return true
	}
	key, ok := userRateLimitKey(u)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	retry, ok := rl.allow(key)
	if !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "search rate limit exceeded")
		return false
	}
	return true
}
```

Add `"time"` to that file's imports (the `rateLimiter` literal needs `map[string][]time.Time`).

- [ ] **Step 6: Run the tests and verify they pass**

Run: `go test ./internal/api/ -run 'TestSearchLimiter|TestAllowSearch' -v -timeout 60s`
Expected: PASS.

Run: `go test ./internal/api/ -count=1 -timeout 300s`
Expected: PASS. No existing test sets the new fields, so every existing `Server` is unarmed.

- [ ] **Step 7: Mutation checks**

| # | Mutation | Must redden |
|---|---|---|
| M4a | in `allowSearch`, replace the `!ok` 401 branch with `key = "anonymous"` (fail open) | `TestAllowSearch_UnidentifiedCallerIs401AndCreatesNoKey` on both the status AND the key count |
| M4b | drop the `sync.Once` and construct a fresh limiter on every `searchRateLimiter()` call | `TestSearchLimiter_IsConstructedOncePerServer` |
| M4c | change the guard to `if s.SearchLimitN < 0 \|\| s.SearchLimitWin < 0` | `TestSearchLimiter_ZeroFieldsDisableTheBucket` on the `{}` row |
| M4d | change the 429 body to `"rate limit exceeded"` | `TestAllowSearch_ChargesTheCeilingPerUser` |
| M4e | **control:** `return true` as the first line of `allowSearch` | `TestAllowSearch_UnidentifiedCallerIs401AndCreatesNoKey` and `..._ChargesTheCeilingPerUser`. **Must die.** |

- [ ] **Step 8: Commit**

```bash
git add internal/api/server.go internal/api/search_ratelimit.go internal/api/search_ratelimit_test.go internal/api/ratelimit.go
git commit -m "Add a per-user read bucket to api.Server, unarmed by default"
```

---

## Task 5: Charge the bucket in `handleListJobs`

**Files:**
- Modify: `internal/api/jobs.go` (`handleListJobs`, immediately after `parseJobFilters` returns)
- Modify: `internal/api/search_ratelimit_test.go`

This is the placement task and it carries the discriminating input of the whole slice.

- [ ] **Step 1: Write the failing tests**

Append to `internal/api/search_ratelimit_test.go` (adding `"context"`, `"net/http"`, `"relay/internal/store"`, `"github.com/jackc/pgx/v5"`, `"github.com/jackc/pgx/v5/pgconn"` to its imports):

```go
// countingDB is a store.DBTX that records every statement and refuses all of
// them, so a request that reaches the database answers 500 and a request that
// does not reaches nothing. Refusing rather than returning plausible zeros is
// deliberate: 500 and 429 are then distinguishable at the recorder, with no
// Postgres and no fixture rows.
type countingDB struct{ calls int }

func (d *countingDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	d.calls++
	return pgconn.CommandTag{}, errRefusedByStub
}

func (d *countingDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	d.calls++
	return nil, errRefusedByStub
}

func (d *countingDB) QueryRow(context.Context, string, ...any) pgx.Row {
	d.calls++
	return refusingRow{}
}

type refusingRow struct{}

func (refusingRow) Scan(...any) error { return errRefusedByStub }

var errRefusedByStub = errors.New("countingDB refuses every statement")

// listJobsAs drives handleListJobs directly with an injected identity. Direct
// rather than through Handler(), because BearerAuth would need a token row and
// because the 401 this slice introduces is only reachable this way.
func listJobsAs(t *testing.T, s *Server, u AuthUser, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/jobs?"+rawQuery, nil)
	req = req.WithContext(ctxWithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	s.handleListJobs(rec, req)
	return rec
}

func newSearchTestServer(t *testing.T, n int, win time.Duration) (*Server, *countingDB) {
	t.Helper()
	db := &countingDB{}
	return &Server{q: store.New(db), SearchLimitN: n, SearchLimitWin: win}, db
}

// TestListJobs_WhitespaceOnlyQIsNotCounted IS THE TEST THAT PROVES THE
// PLACEMENT, and the whitespace rows go FIRST because a discriminating input
// placed last misses an early-exit mutation.
//
// ?q=%20%20 is PRESENT to r.URL.Query().Get("q") and ABSENT to parseFilterQ,
// which trims it. The ceiling here is 1, so if the bucket were charged by a
// middleware predicate testing Get("q") != "", the two whitespace requests would
// exhaust it and the FIRST real needle would be refused. Charged where the
// needle is known to be non-nil, they cost nothing and the first needle is
// allowed.
//
// It kills the middleware form of this control outright: not "the middleware is
// less tidy" but "the middleware counts a set that is not the expensive set".
func TestListJobs_WhitespaceOnlyQIsNotCounted(t *testing.T) {
	s, _ := newSearchTestServer(t, 1, time.Minute)
	u := searchTestUser(1)

	for _, q := range []string{"q=%20%20", "q=%20%09%20", "q="} {
		rec := listJobsAs(t, s, u, q)
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code,
			"%s trims to absent and must not be charged: this is the exact input a middleware "+
				"predicate would count and the in-handler placement cannot", q)
	}

	first := listJobsAs(t, s, u, "q=needle")
	require.NotEqual(t, http.StatusTooManyRequests, first.Code,
		"the whole budget must still be here; if this is 429 the check is counting absent needles")

	second := listJobsAs(t, s, u, "q=needle")
	assert.Equal(t, http.StatusTooManyRequests, second.Code,
		"and the budget must be real: a check that counts nothing would let this through too")
}

// Unfiltered polling is UNAFFECTED, proven rather than asserted. Same handler,
// same user, same limiter instance, so the needle is the only discriminator.
func TestListJobs_UnfilteredPollingIsNotCounted(t *testing.T) {
	s, _ := newSearchTestServer(t, 2, time.Minute)
	u := searchTestUser(1)

	for i := 0; i < 7; i++ {
		rec := listJobsAs(t, s, u, "limit=10")
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code,
			"unfiltered request %d was refused; the 3 s SPA poll must never reach this bucket", i)
	}

	for i := 0; i < 2; i++ {
		rec := listJobsAs(t, s, u, "q=needle")
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "needle request %d", i)
	}
	assert.Equal(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)
}

// A REFUSED NEEDLE COSTS NO BUDGET, and the two 400 rows go first. An over-long
// or non-UTF-8 q never reaches a statement, so charging for it would let a
// caller spend another caller's ceiling on requests that cost the database
// nothing.
func TestListJobs_RejectedNeedleCostsNoBudget(t *testing.T) {
	s, _ := newSearchTestServer(t, 2, time.Minute)
	u := searchTestUser(1)

	tooLong := listJobsAs(t, s, u, "q="+strings.Repeat("a", maxFilterQRunes+1))
	require.Equal(t, http.StatusBadRequest, tooLong.Code)
	assert.JSONEq(t, `{"error":"`+maxFilterQMessage+`"}`, tooLong.Body.String())

	badUTF8 := listJobsAs(t, s, u, "q=%FF%FE")
	require.Equal(t, http.StatusBadRequest, badUTF8.Code)
	assert.JSONEq(t, `{"error":"q is not valid UTF-8"}`, badUTF8.Body.String())

	for i := 0; i < 2; i++ {
		rec := listJobsAs(t, s, u, "q=needle")
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code,
			"needle request %d: the two 400s above must have left the budget untouched", i)
	}
	assert.Equal(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)
}

// A 400 OUTRANKS THE 429. The input carries BOTH conditions: the budget is
// already spent AND the needle is malformed. README documents the precedence
// direction for this endpoint's 400s and TestListJobs_SortVersusFilterGuard-
// OutranksArity pins the sharpest case of it; this row extends the same rule to
// the new refusal.
func TestListJobs_MalformedQOutranksTheRateLimit(t *testing.T) {
	s, _ := newSearchTestServer(t, 1, time.Minute)
	u := searchTestUser(1)

	require.NotEqual(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)
	require.Equal(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code,
		"fixture: the budget must be exhausted, or the row below proves nothing")

	rec := listJobsAs(t, s, u, "q="+strings.Repeat("a", maxFilterQRunes+1))
	assert.Equal(t, http.StatusBadRequest, rec.Code,
		"a malformed needle answers 400 even with the budget gone: a 429 here would tell a caller "+
			"to slow down about a request that will never be valid")
}

// A 401 OUTRANKS THE 429 TOO, on the same input, and by construction: the key
// is computed before the bucket is consulted.
func TestListJobs_MissingIdentityIs401NotA429(t *testing.T) {
	s, _ := newSearchTestServer(t, 1, time.Minute)

	rec := listJobsAs(t, s, AuthUser{}, "q=needle")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.JSONEq(t, `{"error":"unauthorized"}`, rec.Body.String())

	// DECLARED BEHAVIOUR CHANGE. handleListJobs previously read the identity as
	// `u, _ := UserFromCtx(ctx)` and discarded the ok, so this request would have
	// listed the whole farm. It is unreachable through the mux, where the route
	// is auth(http.HandlerFunc(s.handleListJobs)), and it aligns the jobs list
	// with handleListScheduledJobs and with mine=true.
	//
	// It must NOT reach the database, which is the half a status code alone does
	// not say: a 401 that still ran the count would be a refusal that costs
	// exactly what it refused.
	deep := listJobsAs(t, s, AuthUser{}, "q=needle&limit=5")
	assert.Equal(t, http.StatusUnauthorized, deep.Code)
}

// The refusal touches NO DATABASE STATEMENT, asserted structurally rather than
// by timing. store.Queries accepts any DBTX, so a recording stub is the seam.
func TestListJobs_RefusalMakesNoDatabaseCall(t *testing.T) {
	s, db := newSearchTestServer(t, 1, time.Minute)
	u := searchTestUser(1)

	require.NotEqual(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)
	require.Positive(t, db.calls,
		"fixture: an ALLOWED search must reach the database, or 'the refusal made no call' is "+
			"vacuously true of every request")
	spent := db.calls

	rec := listJobsAs(t, s, u, "q=needle")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, spent, db.calls,
		"a refused search must make zero statements: the bucket exists to stop pool occupancy, and a "+
			"refusal that still occupied a connection would bound nothing")
}
```

Add `"errors"` and `"strings"` to the file's imports.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/api/ -run 'TestListJobs_(Whitespace|Unfiltered|Rejected|Malformed|Missing|Refusal)' -v -timeout 120s`

Expected: FAIL. Specifically:
- `..._WhitespaceOnlyQIsNotCounted`: fails on the last assertion - the second needle request is **not** 429, because nothing charges the bucket yet.
- `..._UnfilteredPollingIsNotCounted`: same, on its last line.
- `..._RejectedNeedleCostsNoBudget`: same.
- `..._MalformedQOutranksTheRateLimit`: fails on its `require.Equal(429)` fixture line.
- `..._MissingIdentityIs401NotA429`: fails - the handler currently discards the `ok` and answers 500 from the stub.
- `..._RefusalMakesNoDatabaseCall`: fails on `require.Equal(429)`.

**Verify each of these RED messages yourself. The bodies above are a guess written without running them; if a message differs, fix the test, not the expectation, and record what you changed.**

- [ ] **Step 3: Implement the placement**

In `internal/api/jobs.go`, replace the block in `handleListJobs`:

```go
	u, _ := UserFromCtx(ctx)
	filters, ok := parseJobFilters(w, pp.Query, u)
	if !ok {
		return
	}
```

with:

```go
	u, _ := UserFromCtx(ctx)
	filters, ok := parseJobFilters(w, pp.Query, u)
	if !ok {
		return
	}

	// HERE, and not as middleware. filters.Q != nil is the established in-handler
	// expression of "this request is the expensive kind" - countJobs,
	// countJobsByStatus and countJobsByScheduledJob each open with exactly this
	// test - so the counted set and the expensive set are the same set. A
	// middleware predicate would re-read r.URL.Query() and count ?q= and
	// ?q=%20%20, which parseFilterQ has already normalized to absent.
	//
	// AFTER the filters parse, so every 400 on this route outranks the 429:
	// parsePage, the sort-versus-filter guard, rejectRepeatedParams and every 400
	// inside parseJobFilters have all run. A malformed needle never reaches a
	// statement, so it must not spend budget either.
	//
	// The identity is read above with the ok discarded, which is unchanged.
	// allowSearch is what re-imposes it, and only for a q-carrying request: the
	// key fails closed, so there is no bucket to charge without a principal.
	if filters.Q != nil && !s.allowSearch(w, u) {
		return
	}
```

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/api/ -run 'TestListJobs_(Whitespace|Unfiltered|Rejected|Malformed|Missing|Refusal)' -v -timeout 120s`
Expected: PASS.

Run: `go test ./internal/api/ -count=1 -timeout 300s`
Expected: PASS.

- [ ] **Step 5: Run the placement mutation battery**

Baseline green first. One mutation at a time, restored from a copy, each kill named by its failing test **and** the branch it belongs to.

| # | Mutation | Must redden | Branch |
|---|---|---|---|
| M5a | drop the gate: `if !s.allowSearch(w, u) { return }` | `TestListJobs_UnfilteredPollingIsNotCounted` (last line) **and** `TestListJobs_WhitespaceOnlyQIsNotCounted` | the `filters.Q != nil` gate |
| M5b | re-express as middleware: mount `GET /v1/jobs` in `Handler()` behind a wrapper that calls `s.allowSearch` when `r.URL.Query().Get("q") != ""`, and delete the in-handler call | `TestListJobs_WhitespaceOnlyQIsNotCounted` (the first needle is 429) | the placement, on the normalisation `parseFilterQ` performs |
| M5c | move the call above `parseJobFilters`, gated on `pp.Query.Get("q") != ""` | `TestListJobs_RejectedNeedleCostsNoBudget` **and** `TestListJobs_MalformedQOutranksTheRateLimit` | the ordering against the 400s |
| M5d | move the call above `rejectRepeatedParams` | `TestListJobs_SortVersusFilterGuardOutranksArity` in the **integration** lane - run it | README's documented precedence |
| M5e | in `allowSearch`, return `true` instead of writing 401 when the key fails | `TestListJobs_MissingIdentityIs401NotA429` | the fail-closed key |
| M5f | **control:** delete the whole `if filters.Q != nil && !s.allowSearch(...)` block | every test in step 1 except the two 400 bodies. **Must die.** |

M5d needs Docker: `go test -tags integration -p 1 ./internal/api/ -run TestListJobs_SortVersusFilterGuardOutranksArity -v -timeout 300s`.

- [ ] **Step 6: Commit**

```bash
git add internal/api/jobs.go internal/api/search_ratelimit_test.go
git commit -m "Charge the read bucket in handleListJobs, where the needle is known to be non-nil"
```

---

## Task 6: Charge the same bucket in `handleListScheduledJobs`

**Files:**
- Modify: `internal/api/scheduled_jobs.go` (`handleListScheduledJobs`, immediately after `parseScheduleFilters` returns)
- Modify: `internal/api/search_ratelimit_test.go`

One shared bucket over both routes: the quantity bounded is scan work and it does not care which route bought it. Two buckets would hand an adversary who alternates routes exactly twice the ceiling.

- [ ] **Step 1: Write the failing tests**

Append to `internal/api/search_ratelimit_test.go`:

```go
func listSchedulesAs(t *testing.T, s *Server, u AuthUser, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/scheduled-jobs?"+rawQuery, nil)
	req = req.WithContext(ctxWithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	s.handleListScheduledJobs(rec, req)
	return rec
}

// ONE BUCKET OVER BOTH ROUTES. Two buckets would give a caller who alternates
// routes exactly twice the ceiling, which is the shape where per-axis bounds
// reduce nothing. The interleaving is the discriminator: with a ceiling of 2, a
// jobs search and a schedules search must together exhaust it.
func TestSearchBucket_IsSharedAcrossBothListRoutes(t *testing.T) {
	s, _ := newSearchTestServer(t, 2, time.Minute)
	u := searchTestUser(1)

	require.NotEqual(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)
	require.NotEqual(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code)

	assert.Equal(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code,
		"the schedules route must see a budget the jobs route already spent")
	assert.Equal(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code,
		"and the reverse direction too, or the two routes have separate maps")
}

func TestListScheduledJobs_WhitespaceOnlyQIsNotCounted(t *testing.T) {
	s, _ := newSearchTestServer(t, 1, time.Minute)
	u := searchTestUser(1)

	for _, q := range []string{"q=%20%20", "q="} {
		rec := listSchedulesAs(t, s, u, q)
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "%s trims to absent", q)
	}
	require.NotEqual(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code)
	assert.Equal(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code)
}

func TestListScheduledJobs_UnfilteredPollingIsNotCounted(t *testing.T) {
	s, _ := newSearchTestServer(t, 2, time.Minute)
	u := searchTestUser(1)

	for i := 0; i < 7; i++ {
		require.NotEqual(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "limit=10").Code,
			"unfiltered request %d", i)
	}
	for i := 0; i < 2; i++ {
		require.NotEqual(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code, i)
	}
	assert.Equal(t, http.StatusTooManyRequests, listSchedulesAs(t, s, u, "q=needle").Code)
}

// The 429 body must be BYTE-IDENTICAL across the two endpoints, mirroring
// TestFilterQ_BodiesAreIdenticalAcrossEndpoints' rule for the q 400s. Two
// endpoints growing their own copies of a refusal drift without either
// endpoint's own tests noticing.
func TestSearchRefusal_BodyIsIdenticalAcrossEndpoints(t *testing.T) {
	s, _ := newSearchTestServer(t, 1, time.Minute)
	u := searchTestUser(1)
	require.NotEqual(t, http.StatusTooManyRequests, listJobsAs(t, s, u, "q=needle").Code)

	jobs := listJobsAs(t, s, u, "q=needle")
	scheds := listSchedulesAs(t, s, u, "q=needle")
	require.Equal(t, http.StatusTooManyRequests, jobs.Code)
	require.Equal(t, http.StatusTooManyRequests, scheds.Code)
	assert.Equal(t, jobs.Body.String(), scheds.Body.String(),
		"one control, one body: an operator reading a log must not have to know which route it came from")
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/api/ -run 'TestSearchBucket|TestListScheduledJobs_(Whitespace|Unfiltered)|TestSearchRefusal' -v -timeout 120s`
Expected: FAIL on the 429 assertions, since nothing charges from the schedules handler. `TestSearchBucket_IsSharedAcrossBothListRoutes` will fail on its third line.

- [ ] **Step 3: Implement**

In `internal/api/scheduled_jobs.go`, in `handleListScheduledJobs`, replace:

```go
	filters, ok := parseScheduleFilters(w, pp.Query)
	if !ok {
		return
	}
```

with:

```go
	filters, ok := parseScheduleFilters(w, pp.Query)
	if !ok {
		return
	}

	// The same bucket handleListJobs charges, at the same point in the same
	// order: after the filters parse, gated on a non-nil needle. ONE bucket over
	// both routes, because the quantity bounded is scan work and it does not care
	// which route bought it - two buckets would hand a caller alternating routes
	// exactly twice the ceiling. The identity 401 above has already run, so
	// allowSearch's own fail-closed 401 is unreachable from here; it is kept
	// because the helper, not its callers, owns that decision.
	if filters.Q != nil && !s.allowSearch(w, u) {
		return
	}
```

- [ ] **Step 4: Run and verify PASS**

Run: `go test ./internal/api/ -count=1 -timeout 300s`
Expected: PASS.

- [ ] **Step 5: Mutation checks**

| # | Mutation | Must redden |
|---|---|---|
| M6a | give the schedules handler its own `rateLimiter` instance | `TestSearchBucket_IsSharedAcrossBothListRoutes` |
| M6b | drop the `filters.Q != nil` gate here | `TestListScheduledJobs_UnfilteredPollingIsNotCounted` and `..._WhitespaceOnlyQIsNotCounted` |
| M6c | change the schedules 429 body text | `TestSearchRefusal_BodyIsIdenticalAcrossEndpoints` |
| M6d | **control:** delete the whole block | every test in step 1. **Must die.** |

- [ ] **Step 6: Commit**

```bash
git add internal/api/scheduled_jobs.go internal/api/search_ratelimit_test.go
git commit -m "Charge the same read bucket on the schedules list"
```

---

## Task 7: Log the underlying error at every list 500

**Files:**
- Create: `internal/api/db_error_log.go`
- Create: `internal/api/db_error_log_test.go`
- Modify: `internal/api/jobs.go` (5 sites)
- Modify: `internal/api/scheduled_jobs.go` (the sites inside `handleListScheduledJobs`)

A tripped `statement_timeout` returns SQLSTATE 57014 to pgx, which these handlers turn into their existing `500 list jobs failed`. This slice keeps that response. It must not keep the silence: a timeout that is indistinguishable from every other database failure, in a log that says nothing, is a control nobody can tell fired.

Measured: `grep -c` for the four list/count 500 messages returns **5 in `internal/api/jobs.go` and 20 in `internal/api/scheduled_jobs.go`**. Convert every one that is inside `handleListJobs`, `listJobsBySort` or `handleListScheduledJobs`. Leave sites in other handlers alone - they are out of scope.

- [ ] **Step 1: Write the failing tests**

Create `internal/api/db_error_log_test.go`:

```go
package api

import (
	"bytes"
	"log"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/store"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLog redirects the standard logger for the duration of fn.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(prevFlags) })
	fn()
	return buf.String()
}

// A PgError renders its SQLSTATE and the server's Message, and NOTHING ELSE.
// Detail, Hint and Where can echo a parameter value, and the parameter here is
// caller-supplied text: a needle in a log line is caller input in a stream an
// operator reads, which is how a log pipeline acquires an injected field.
//
// The 57014 row goes FIRST because it is the code this slice creates: a
// statement_timeout cancellation is the failure the new control produces and the
// one an operator will be looking for.
func TestListQueryError_RendersSQLStateWithoutDetail(t *testing.T) {
	err := &pgconn.PgError{
		Severity: "ERROR",
		Code:     "57014",
		Message:  "canceling statement due to statement timeout",
		Detail:   "needle=SUPERSECRET",
		Hint:     "hint=SUPERSECRET",
		Where:    "where=SUPERSECRET",
	}
	rec := httptest.NewRecorder()
	out := captureLog(t, func() {
		req := httptest.NewRequest("GET", "/v1/jobs?q=SUPERSECRET", nil)
		listQueryError(rec, req, err, "list jobs failed")
	})

	assert.Contains(t, out, "57014", "the SQLSTATE is what makes a timeout diagnosable")
	assert.Contains(t, out, "canceling statement due to statement timeout")
	assert.Contains(t, out, "/v1/jobs", "the route path locates the failure")
	assert.NotContains(t, out, "SUPERSECRET",
		"neither the pg error's Detail/Hint/Where nor the request's query string may reach the log; "+
			"the needle is caller-supplied text and this line is read by an operator's pipeline")

	assert.Equal(t, 500, rec.Code)
	assert.JSONEq(t, `{"error":"list jobs failed"}`, rec.Body.String(),
		"the response body is UNCHANGED by this slice; mapping 57014 to a distinguishable response "+
			"is explicitly out of scope")
}

func TestListQueryError_NonPgErrorStillLogs(t *testing.T) {
	rec := httptest.NewRecorder()
	out := captureLog(t, func() {
		req := httptest.NewRequest("GET", "/v1/scheduled-jobs", nil)
		listQueryError(rec, req, errRefusedByStub, "list scheduled jobs failed")
	})
	assert.Contains(t, out, "/v1/scheduled-jobs")
	assert.Contains(t, out, errRefusedByStub.Error())
	assert.Equal(t, 500, rec.Code)
}

// The two list handlers must actually USE it. Executed through the real
// handlers, off a stub that refuses every statement - which is the shape a
// timeout takes at this layer: an error out of the query.
func TestListHandlers_LogTheUnderlyingDatabaseError(t *testing.T) {
	db := &countingDB{}
	s := &Server{q: store.New(db)}
	u := searchTestUser(1)
	_ = time.Second

	jobsLog := captureLog(t, func() {
		rec := listJobsAs(t, s, u, "q=needle")
		require.Equal(t, 500, rec.Code)
	})
	assert.Contains(t, jobsLog, errRefusedByStub.Error(),
		"handleListJobs must log the error it turned into a 500, or a tripped statement_timeout is "+
			"indistinguishable from every other database failure and from success followed by silence")

	schedLog := captureLog(t, func() {
		rec := listSchedulesAs(t, s, u, "q=needle")
		require.Equal(t, 500, rec.Code)
	})
	assert.Contains(t, schedLog, errRefusedByStub.Error(),
		"and so must handleListScheduledJobs")
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api/ -run 'TestListQueryError|TestListHandlers_Log' -v -timeout 60s`
Expected: FAIL to build (`undefined: listQueryError`).

- [ ] **Step 3: Implement the helper**

Create `internal/api/db_error_log.go`:

```go
package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
)

// listQueryError writes a list endpoint's existing 500 body and logs one line
// naming the underlying failure.
//
// IT EXISTS BECAUSE OF THE STATEMENT TIMEOUT. A statement cancelled by
// RELAY_DB_STATEMENT_TIMEOUT reaches here as SQLSTATE 57014, and this slice
// deliberately does NOT give it a distinguishable response - a timed-out search
// answers 500 like any other database failure. Without a log line, a control
// that fires is a control nobody can observe firing.
//
// A PgError renders its Code and Message and NOTHING ELSE. Detail, Hint and
// Where can quote a parameter value, and on these two routes the parameter is a
// caller-supplied needle: rendering it would put caller input into an operator's
// log pipeline. r.URL.Path, never r.URL.String(), for the same reason.
func listQueryError(w http.ResponseWriter, r *http.Request, err error, msg string) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		log.Printf("%s %s: %s (SQLSTATE %s)", r.Method, r.URL.Path, pgErr.Message, pgErr.Code)
	} else {
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	}
	writeError(w, http.StatusInternalServerError, msg)
}
```

- [ ] **Step 4: Convert the call sites**

In `internal/api/jobs.go`, inside `handleListJobs` only, replace each of the five:

- `writeError(w, http.StatusInternalServerError, "list jobs failed")` -> `listQueryError(w, r, err, "list jobs failed")`
- `writeError(w, http.StatusInternalServerError, "count jobs failed")` -> `listQueryError(w, r, err, "count jobs failed")`

`listJobsBySort` returns its error up to `handleListJobs`, so its own sites need no change - the default branch's single site covers all ten sort arms and both count arms.

In `internal/api/scheduled_jobs.go`, inside `handleListScheduledJobs` only, replace every
`writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")` with
`listQueryError(w, r, err, "list scheduled jobs failed")`, and the same for any
`"count scheduled jobs failed"` site inside that handler.

- [ ] **Step 5: Add the completeness guard**

Append to `internal/api/db_error_log_test.go` (adding `"go/ast"`, `"go/parser"`, `"go/token"`):

```go
// TestListHandlers_NoBare500Remains is a PARSED guard and it is parsed because
// the alternative is 25 executed tests for 25 identical branches, most of which
// are sort arms unreachable without a real page of rows.
//
// SAY WHAT IT COVERS. It covers a NEW arm added later with a bare
// writeError(...500...), which is the realistic regression: someone adds an
// eleventh sort variant by copying the tenth. It does NOT prove the argument
// passed is the right error, and it does not look outside these three
// functions. That listQueryError itself renders the right thing is EXECUTED, in
// TestListQueryError_RendersSQLStateWithoutDetail, and that the handlers reach
// it at all is EXECUTED in TestListHandlers_LogTheUnderlyingDatabaseError.
func TestListHandlers_NoBare500Remains(t *testing.T) {
	for file, fns := range map[string][]string{
		"jobs.go":           {"handleListJobs", "listJobsBySort"},
		"scheduled_jobs.go": {"handleListScheduledJobs"},
	} {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		require.NoError(t, err)

		wanted := map[string]bool{}
		for _, n := range fns {
			wanted[n] = true
		}
		seen := map[string]bool{}
		for _, d := range parsed.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !wanted[fd.Name.Name] || fd.Body == nil {
				continue
			}
			seen[fd.Name.Name] = true
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id, ok := ce.Fun.(*ast.Ident)
				if !ok || id.Name != "writeError" || len(ce.Args) < 2 {
					return true
				}
				sel, ok := ce.Args[1].(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "StatusInternalServerError" {
					return true
				}
				t.Errorf("%s: %s writes a bare 500 at %s. Use listQueryError so a tripped "+
					"statement_timeout leaves a SQLSTATE in the log instead of being "+
					"indistinguishable from every other database failure.",
					file, fd.Name.Name, fset.Position(ce.Pos()))
				return true
			})
		}
		require.Equal(t, len(wanted), len(seen),
			"%s no longer declares %v; this guard is walking nothing", file, fns)
	}
}
```

- [ ] **Step 6: Run and verify PASS**

Run: `go test ./internal/api/ -count=1 -timeout 300s`
Expected: PASS.

- [ ] **Step 7: Mutation checks**

| # | Mutation | Must redden |
|---|---|---|
| M7a | in `listQueryError`, add `pgErr.Detail` to the format | `TestListQueryError_RendersSQLStateWithoutDetail` on `SUPERSECRET` |
| M7b | drop the `log.Printf` from the PgError branch | the same test on `57014` |
| M7c | revert one converted site in `handleListScheduledJobs` to a bare `writeError` | `TestListHandlers_NoBare500Remains` |
| M7d | change `r.URL.Path` to `r.URL.String()` | `TestListQueryError_RendersSQLStateWithoutDetail` on `SUPERSECRET`, since the fixture request carries `?q=SUPERSECRET` |
| M7e | **control:** make `listQueryError` write 200 instead of 500 | `TestListQueryError_*` and `TestListHandlers_LogTheUnderlyingDatabaseError`. **Must die.** |

M7d is the one worth pausing on: it is the shape a well-meaning "make the log line more useful" edit takes, and only the fixture's query string catches it.

- [ ] **Step 8: Commit**

```bash
git add internal/api/db_error_log.go internal/api/db_error_log_test.go internal/api/jobs.go internal/api/scheduled_jobs.go
git commit -m "Log the SQLSTATE behind a list 500, so a statement timeout is diagnosable"
```

---

## Task 8: Parse `RELAY_JOB_SEARCH_RATE_LIMIT` in `main`

**Files:**
- Modify: `cmd/relay-server/main.go` (beside the two existing `ParseRateLimit` calls)
- Modify: `cmd/relay-server/http_server.go` (`httpServerDeps` + `buildHTTPServer`)

No test in this task; the guard is task 9. Env parsing stays in `main` per `httpServerDeps`'s own comment, because these parses end in `log.Fatalf` which no test can call.

- [ ] **Step 1: Add the parse**

In `cmd/relay-server/main.go`, immediately after the `registerN, registerWin` block:

```go
	// A SECOND INSTANCE of the user-keyed mechanism, not a second mounting of
	// api.RateLimit: that one keys on clientIP(r), which would collapse every
	// user behind one proxy into one bucket on an authenticated read.
	//
	// SEPARATE FROM THE WRITE BUCKET. Different quantity, different first-party
	// cadence - a polling read at 20 to 100 requests per minute versus an
	// interactive submit - and sharing them would let a search burst refuse a job
	// submission, which is the worse of the two outcomes to trade away.
	//
	// THERE IS NO OFF VALUE, deliberately: ParseRateLimit rejects a zero count
	// and this is fatal, so an operator cannot disable the control from the
	// environment. The escape is a large number, 100000:1s, which leaves the
	// bound visible as a number in README and in the environment.
	searchN, searchWin, err := api.ParseRateLimit(envOrDefault("RELAY_JOB_SEARCH_RATE_LIMIT", "120:10s"))
	if err != nil {
		log.Fatalf("parse RELAY_JOB_SEARCH_RATE_LIMIT: %v", err)
	}
```

- [ ] **Step 2: Add the deps fields**

In `cmd/relay-server/http_server.go`, add to `httpServerDeps` after `registerLimitWin`:

```go
	// searchLimitN and searchLimitWin bound ?q= text searches per authenticated
	// principal, across GET /v1/jobs and GET /v1/scheduled-jobs together. They
	// reach api.Server as exported FIELDS, never as two more arguments on
	// api.New: that call's tail is already four same-typed arguments in a row and
	// this file's own header records a measured green transpose across them.
	searchLimitN   int
	searchLimitWin time.Duration
```

- [ ] **Step 3: Assign them**

In `buildHTTPServer`, after `s.AllowSelfRegister = d.allowSelfRegister`:

```go
	s.SearchLimitN = d.searchLimitN
	s.SearchLimitWin = d.searchLimitWin
```

And add to `buildHTTPServer`'s doc comment, inside the existing "Deleting any of the three assignments below" bullet, changing "three" to "five" and appending `, SearchLimitN, SearchLimitWin` to the list, plus one sentence:

```
//     The two Search fields are the exception: they are EXECUTED-guarded by
//     TestBuildHTTPServer_SearchLimitRefusesAQCarryingRequestPastTheCeiling in
//     search_ratelimit_wiring_test.go, which drives a real request past the
//     ceiling through the real route and asserts 429.
```

- [ ] **Step 4: Pass them at the call site**

In `main.go`'s `buildHTTPServer(httpServerDeps{...})` literal, after `registerLimitWin: registerWin,`:

```go
		searchLimitN:      searchN,
		searchLimitWin:    searchWin,
```

- [ ] **Step 5: Verify nothing existing broke**

Run: `go build ./... && go test ./cmd/relay-server/ -count=1 -timeout 300s`
Expected: PASS. `TestServerCountersIsWiredByMain` in particular: `searchN`/`searchWin` are assigned exactly once each in main's body, and neither feeds `s.Counters`, so neither the `wiredDep` table nor `countersAssignmentSources` changes.

- [ ] **Step 6: Commit**

```bash
git add cmd/relay-server/main.go cmd/relay-server/http_server.go
git commit -m "Parse RELAY_JOB_SEARCH_RATE_LIMIT and thread it to api.Server by field"
```

---

## Task 9: The executed wiring guard

**Files:**
- Create: `cmd/relay-server/search_ratelimit_wiring_test.go`

`idea-2026-08-14-generalize-the-env-to-field-wiring-guard` names an executed guard as the top rung, and this is the case for it: deleting `s.Metrics = d.metrics` in `buildHTTPServer` is green across every package, measured, and the two new assignments have the identical property.

**State plainly what this guard does and does not cover, because a sibling lane measured the boundary:** mutating `main.go`'s `httpServerDeps` literal - setting `searchLimitN: 0` there - leaves the whole `cmd/relay-server` lane green. **This guard does not see main.go at all.** It covers `buildHTTPServer` forwarding what it was given, and `api.Server` acting on it, end to end through a real request. It does not cover whether `main` passes the value it parsed.

- [ ] **Step 1: Write the failing guard**

Create `cmd/relay-server/search_ratelimit_wiring_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// searchWiringDB resolves a bearer token to a NON-ADMIN user WITH A VALID ID and
// refuses every other statement.
//
// A SEPARATE STUB FROM stubAdminDB, and the difference is the whole point:
// stubAdminDB fills only *bool and *string destinations, so every pgtype.UUID in
// GetTokenWithUserRow stays invalid. A caller whose ID is invalid is refused 401
// by the fail-closed bucket key, and this test would measure that instead of the
// ceiling.
type searchWiringDB struct{}

func (searchWiringDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errSearchWiringRefused
}

func (searchWiringDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errSearchWiringRefused
}

func (searchWiringDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	// Only the bearer-auth lookup is answered. Every list statement is refused,
	// so an ALLOWED search answers 500 and a REFUSED one answers 429: the two are
	// then distinguishable at the recorder with no Postgres.
	if !isTokenLookup(sql) {
		return searchWiringRefusingRow{}
	}
	return searchWiringTokenRow{}
}

type searchWiringRefusingRow struct{}

func (searchWiringRefusingRow) Scan(...any) error { return errSearchWiringRefused }

type searchWiringTokenRow struct{}

// Scan fills BY DESTINATION TYPE, and fills every *pgtype.UUID with a valid
// value: an invalid user id would make every request below a 401 and the
// ceiling assertion would never be reached.
func (searchWiringTokenRow) Scan(dest ...any) error {
	uuids := 0
	for _, d := range dest {
		switch v := d.(type) {
		case *bool:
			*v = false
		case *string:
			*v = "search-wiring"
		case *pgtype.UUID:
			var raw [16]byte
			raw[15] = 7
			*v = pgtype.UUID{Bytes: raw, Valid: true}
			uuids++
		}
	}
	if uuids == 0 {
		return fmt.Errorf("searchWiringDB: GetTokenWithUserRow has no pgtype.UUID destination; the " +
			"row shape changed and this stub can no longer produce an identified caller")
	}
	return nil
}

var errSearchWiringRefused = fmt.Errorf("searchWiringDB refuses list statements")

func isTokenLookup(sql string) bool {
	// GetTokenWithUser is the only statement BearerAuth issues, and it is the
	// only one that must succeed here.
	return len(sql) > 0 && containsFold(sql, "FROM tokens")
}

func containsFold(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if equalFold(haystack[i:i+len(needle)], needle) {
					return true
				}
			}
			return false
		}()
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

func searchWiringRequest(t *testing.T, srv *http.Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Authorization", "Bearer any-token-the-stub-resolves")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// TestBuildHTTPServer_SearchLimitRefusesAQCarryingRequestPastTheCeiling is an
// EXECUTED wiring guard: it builds the server the way main does and drives real
// requests through the real route.
//
// WHAT IT COVERS: buildHTTPServer forwarding searchLimitN and searchLimitWin
// into api.Server, and api.Server acting on them. Deleting either assignment in
// buildHTTPServer makes this RED, which an AST row would not - and this test
// adds no row to any AST table.
//
// WHAT IT DOES NOT COVER, said plainly because a sibling lane MEASURED the
// boundary: it does not look at main.go. Setting `searchLimitN: 0` in main's own
// httpServerDeps literal leaves the whole cmd/relay-server lane green, this test
// included. That half is unguarded.
func TestBuildHTTPServer_SearchLimitRefusesAQCarryingRequestPastTheCeiling(t *testing.T) {
	srv := buildHTTPServer(httpServerDeps{
		addr:           "127.0.0.1:0",
		q:              store.New(searchWiringDB{}),
		searchLimitN:   2,
		searchLimitWin: time.Minute,
	})

	// A ceiling of 2: two q-carrying requests are ALLOWED, and an allowed one
	// reaches the refusing stub and answers 500. That 500 is the fixture, not the
	// subject - it is what proves the request got past the bucket.
	for i := 0; i < 2; i++ {
		rec := searchWiringRequest(t, srv, "/v1/jobs?q=needle")
		require.Equal(t, http.StatusInternalServerError, rec.Code,
			"request %d must be ALLOWED past the bucket and then fail at the stub; a 401 here means "+
				"the stub is not producing an identified caller, a 429 means the ceiling is wrong", i)
	}

	rec := searchWiringRequest(t, srv, "/v1/jobs?q=needle")
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"the third q-carrying request must be refused. If this is 500, buildHTTPServer did not "+
			"forward searchLimitN/searchLimitWin and the control is entirely absent on this build.")

	var body struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "search rate limit exceeded", body.Error,
		"distinct from RateLimit's shared 'rate limit exceeded', so an operator reading a log can "+
			"tell which control fired")
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
}

// And unfiltered polling is unaffected THROUGH THE REAL ROUTE, not only through
// the handler: this is the assertion that would catch the control being mounted
// as middleware during the wiring rather than inside the handler.
func TestBuildHTTPServer_SearchLimitLeavesUnfilteredPollingAlone(t *testing.T) {
	srv := buildHTTPServer(httpServerDeps{
		addr:           "127.0.0.1:0",
		q:              store.New(searchWiringDB{}),
		searchLimitN:   1,
		searchLimitWin: time.Minute,
	})

	for i := 0; i < 6; i++ {
		rec := searchWiringRequest(t, srv, "/v1/jobs?limit=10")
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "unfiltered request %d", i)
	}
	for i := 0; i < 4; i++ {
		rec := searchWiringRequest(t, srv, "/v1/jobs?q=%20%20")
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code,
			"whitespace-only request %d trims to absent and must not be charged", i)
	}

	require.Equal(t, http.StatusInternalServerError,
		searchWiringRequest(t, srv, "/v1/jobs?q=needle").Code,
		"the single slot must still be unspent after ten non-needle requests")
	assert.Equal(t, http.StatusTooManyRequests,
		searchWiringRequest(t, srv, "/v1/jobs?q=needle").Code)
}
```

**The `isTokenLookup` predicate is a guess.** Before trusting it, print the SQL the stub receives and confirm `GetTokenWithUser`'s text is the only one that must succeed. If a simpler discriminator exists (for example, `BearerAuth` is the only caller reaching `QueryRow` before the handler runs), simplify - a stub with a fragile string match is a stub that will pass for the wrong reason later.

- [ ] **Step 2: Run to verify it fails**

**Do this before task 8's changes are on disk if you are running the chains in parallel;** otherwise stash task 8's `http_server.go` edit, run, and restore. Expected without the assignments: `TestBuildHTTPServer_SearchLimitRefusesAQCarryingRequestPastTheCeiling` FAILS on the third request being 500 rather than 429, with the message *"buildHTTPServer did not forward searchLimitN/searchLimitWin"*.

- [ ] **Step 3: Run with task 8 in place and verify PASS**

Run: `go test ./cmd/relay-server/ -run TestBuildHTTPServer_SearchLimit -v -timeout 120s`
Expected: PASS.

- [ ] **Step 4: Mutation checks**

| # | Mutation | Must redden |
|---|---|---|
| M9a | delete `s.SearchLimitN = d.searchLimitN` in `buildHTTPServer` | `..._RefusesAQCarryingRequestPastTheCeiling` |
| M9b | delete `s.SearchLimitWin = d.searchLimitWin` | the same test (a zero window leaves the bucket unarmed) |
| M9c | transpose: `s.SearchLimitN = d.registerLimitN` | the same test (register defaults to 5, not 2) |
| M9d | set `searchLimitN: 0` in **main.go's** deps literal | **nothing. Expected survival, and it is the documented gap.** Record it as a survival, not a kill, and do not fix it by weakening the test. |
| M9e | **control:** `s.SearchLimitN = 0` unconditionally in `buildHTTPServer` | both tests in this file. **Must die.** |

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/search_ratelimit_wiring_test.go
git commit -m "Guard the search-limit wiring by driving a real request past the ceiling"
```

---

## Task 10: Integration lane - the NOTIFY listener survives a short statement timeout

**Files:**
- Create: `internal/scheduler/notify_statement_timeout_integration_test.go`

This is the one claim in the spec whose failure mode is **silent**: if `statement_timeout` did bound `WaitForNotification`, the process would lose cross-process dispatch wakeups and nothing in the system would report it. The reasoning is that the two `LISTEN` statements complete immediately and the wait is idle time with no statement running - so it is pinned, not asserted.

- [ ] **Step 1: Write the failing-if-wrong test**

Create `internal/scheduler/notify_statement_timeout_integration_test.go`:

```go
//go:build integration

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestNotifyListener_SurvivesAStatementTimeoutShorterThanItsIdleWait pins the
// one claim in the statement-timeout decision whose failure mode is SILENT.
//
// NotifyListener.session runs two LISTEN statements and then blocks in
// WaitForNotification indefinitely. statement_timeout bounds statement
// EXECUTION; the two LISTENs complete at once and the wait is idle time with no
// statement running. If that reasoning is wrong, the connection is killed while
// idle, the process stops receiving cross-process dispatch wakeups, and NOTHING
// ELSE IN THE SYSTEM WOULD REPORT IT - the dispatcher would simply run on its
// poll interval forever.
//
// The timeout is set to 200ms and the NOTIFY arrives after 1s, so the idle wait
// is five times the timeout. A shorter margin would let the test pass on a slow
// box for the wrong reason.
//
// BOUNDED FAILURE, never a hang: the trigger is awaited on a deadline and a
// missed wakeup fails with an assertion. A test that hangs here is
// indistinguishable from container trouble, which is exactly what a mutation
// would need to be distinguishable from.
func TestNotifyListener_SurvivesAStatementTimeoutShorterThanItsIdleWait(t *testing.T) {
	dsn := setupTestDB(t)

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.Config.RuntimeParams["statement_timeout"] = "200"
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()

	// Control FIRST: prove the timeout is actually armed on this pool, or every
	// assertion below is vacuous - a pool with no timeout trivially survives one.
	var one int
	err = pool.QueryRow(context.Background(), "SELECT pg_sleep(2), 1").Scan(&one)
	require.Error(t, err, "fixture: a 2s statement must be cancelled by a 200ms statement_timeout; "+
		"if this succeeds the runtime parameter did not reach the connection and this test proves nothing")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	triggered := make(chan struct{}, 8)
	lis := NewNotifyListener(pool, func() {
		select {
		case triggered <- struct{}{}:
		default:
		}
	})
	go lis.Run(ctx)

	// session() calls trigger() once as soon as both LISTENs attach, to drain a
	// startup gap. Consume that so the assertion below is about the NOTIFY.
	select {
	case <-triggered:
	case <-time.After(20 * time.Second):
		t.Fatal("the listener never attached: no startup trigger inside 20s")
	}

	// Idle for five times the timeout, then notify.
	time.Sleep(time.Second)
	_, err = pool.Exec(context.Background(), "NOTIFY relay_task_completed")
	require.NoError(t, err)

	select {
	case <-triggered:
	case <-time.After(20 * time.Second):
		t.Fatal("the NOTIFY produced no trigger inside 20s. If statement_timeout kills an idle " +
			"LISTEN connection, this process silently stops receiving cross-process dispatch " +
			"wakeups and nothing else reports it.")
	}
}
```

**`setupTestDB` is a guess at this package's helper name.** Before running, open `internal/scheduler/dispatch_test.go` and use whatever that file uses to obtain a migrated database and DSN. Do not invent a second helper.

- [ ] **Step 2: Run it**

Run: `go test -tags integration -p 1 ./internal/scheduler/ -run TestNotifyListener_SurvivesAStatementTimeout -v -timeout 600s`
Expected: PASS.

**This test does not have a RED against HEAD, and that is honest rather than a gap:** it pins a claim about Postgres semantics, not about code this slice writes. Its value is the failure it would produce if the claim were wrong, and the control assertion (`pg_sleep(2)` must be cancelled) is what stops it passing vacuously. Say exactly this in the commit message; do not claim a RED you did not observe.

- [ ] **Step 3: Mutation check**

| # | Mutation | Must redden |
|---|---|---|
| M10a | change `RuntimeParams["statement_timeout"] = "200"` to `"0"` | the **control** assertion - `pg_sleep(2)` succeeds. Proves the fixture is load-bearing and this test cannot pass with the timeout unarmed. |

- [ ] **Step 4: Commit**

```bash
git add internal/scheduler/notify_statement_timeout_integration_test.go
git commit -m "Pin that a pool statement timeout does not kill an idle LISTEN connection"
```

---

## Task 11: Integration lane - a search that exceeds the timeout answers 500

**Files:**
- Create: `internal/api/statement_timeout_integration_test.go`

- [ ] **Step 1: Write the test**

Create `internal/api/statement_timeout_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListJobs_AStatementTimeoutAnswers500AndLogsTheSQLState is the end-to-end
// half of the timeout decision: the mechanism reaches a real ?q= request, the
// existing 500 body is unchanged, and the SQLSTATE lands in the log.
//
// It also gives internal/api's listQueryError its first look at a REAL PRODUCER.
// Every other test of that helper feeds it a *pgconn.PgError constructed in a
// test file; this one gets its 57014 from Postgres.
//
// BOUNDED FAILURE: the request runs in a goroutine and the test fails on a
// deadline. A statement that is never cancelled would otherwise hang, and a hang
// is indistinguishable from container trouble.
func TestListJobs_AStatementTimeoutAnswers500AndLogsTheSQLState(t *testing.T) {
	// A pool with NO timeout, to seed. The seeding statement would otherwise be
	// the first thing the timeout killed.
	srv, q, dsn := newTestServerWithPool(t)
	_ = srv
	user := createTestUser(t, q, "Seeder", "seeder@timeout.test", false)

	seedPool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	defer seedPool.Close()

	// 20k rows is far more scan work than 1ms of budget, on any box. One
	// statement, so seeding is fast.
	_, err = seedPool.Exec(context.Background(), `
		INSERT INTO jobs (name, priority, status, submitted_by)
		SELECT 'job-' || g, 'normal', 'pending', $1
		FROM generate_series(1, 20000) g`, user.ID)
	require.NoError(t, err)

	timedSrv, timedToken := newTestServerWithStatementTimeout(t, dsn, "1", user.ID)

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	type result struct {
		code int
		body string
	}
	done := make(chan result, 1)
	go func() {
		req := httptest.NewRequest("GET", "/v1/jobs?q=zzqqxx-no-match", nil)
		req.Header.Set("Authorization", "Bearer "+timedToken)
		rec := httptest.NewRecorder()
		timedSrv.Handler().ServeHTTP(rec, req)
		done <- result{rec.Code, rec.Body.String()}
	}()

	select {
	case got := <-done:
		require.Equal(t, http.StatusInternalServerError, got.code,
			"a search the server cancelled must answer the endpoint's existing 500; mapping 57014 to "+
				"a distinguishable response is explicitly out of scope. body=%s", got.body)
		assert.JSONEq(t, `{"error":"count jobs failed"}`, got.body)
	case <-time.After(60 * time.Second):
		t.Fatal("the request did not return inside 60s. A statement_timeout the backend does not " +
			"enforce is the failure this whole decision is built to avoid, and a hang here would be " +
			"indistinguishable from container trouble.")
	}

	assert.Contains(t, buf.String(), "57014",
		"the SQLSTATE must reach the log, or a tripped timeout is indistinguishable from every "+
			"other database failure")
	assert.NotContains(t, buf.String(), "zzqqxx-no-match",
		"and the needle must not: it is caller-supplied text and this line goes to an operator's pipeline")
}
```

- [ ] **Step 2: Add the helper**

`newTestServerWithStatementTimeout` does not exist. Add it to this file (not to the shared `testhelper_test.go`, so its blast radius is one test):

```go
// newTestServerWithStatementTimeout builds a second api.Server over the SAME
// database through a pool carrying statement_timeout, and mints a token for
// userID. A second pool rather than a second database: the rows seeded through
// the untimed pool have to be visible to the timed one.
func newTestServerWithStatementTimeout(t *testing.T, dsn, ms string, userID pgtype.UUID) (*api.Server, string)
```

Implement it by copying the shape `newTestServerWithPool` uses to build a `*api.Server` - open `internal/api/testhelper_test.go` and follow it exactly - but with `pgxpool.ParseConfig(dsn)`, `cfg.ConnConfig.Config.RuntimeParams["statement_timeout"] = ms`, `pgxpool.NewWithConfig`. Register a `t.Cleanup` that closes the pool. Mint the token with the same `createTestToken` helper.

**Do not guess `newTestServerWithPool`'s return signature.** The call `srv, q, dsn := newTestServerWithPool(t)` in step 1 assumes a third return that may not exist; check and adjust.

- [ ] **Step 3: Run it**

Run: `go test -tags integration -p 1 ./internal/api/ -run TestListJobs_AStatementTimeoutAnswers500 -v -timeout 900s`
Expected: PASS. (The whole `internal/api` integration package runs about 9.5 minutes; use `-timeout 1800s` when running it all.)

If the request unexpectedly succeeds, the seed was too small or the plan too cheap. **Raise the row count until it reliably trips; do not lower the assertion, and do not add a skip.** Record the row count you needed.

- [ ] **Step 4: Mutation check**

| # | Mutation | Must redden |
|---|---|---|
| M11a | set the helper's `ms` to `""` so no key is written | the 500 assertion (the request succeeds with a 200) - proving the timeout is what produced the failure |
| M11b | revert `handleListJobs`'s `count jobs failed` site to a bare `writeError` | the `57014` log assertion - **this is the real-producer kill task 7's helper could not buy from a literal** |

- [ ] **Step 5: Commit**

```bash
git add internal/api/statement_timeout_integration_test.go
git commit -m "Prove a text search over the statement timeout answers 500 and logs 57014"
```

---

## Task 12: Re-measure the amplifier, recording the input with every number

**Files:**
- Create: `scripts/qcost/main.go`
- Create: `scripts/qcost/README.md`
- Create: `docs/retros/2026-09-04-q-cost-measurement.md`

The item requires re-measuring. That requirement is kept and tightened: **a measurement without its input reads as the typical case.** The two numbers this slice inherits - 283 ms and 10.8 ms - have not been reproduced, and no related item records the needle.

`scripts/explain_sort_indexes` already exists as a seeding harness (Postgres 16 testcontainer, embedded migrations, 10k users and 100k jobs with realistic skew, then `ANALYZE`). Reuse its seeding approach; do not write a new one from scratch. `lane JB`'s probe files were left uncommitted and had to be re-created, so **commit whatever you build.**

- [ ] **Step 1: Build the harness**

`scripts/qcost` is a one-shot program that:

1. Spins up a Postgres testcontainer (same version `explain_sort_indexes` uses - read it, do not assume 16).
2. Runs every embedded migration.
3. Seeds `users` and `jobs` to the row counts given by `-users` and `-jobs` (defaults 10000 and 200000), reusing `explain_sort_indexes/seed.go`'s approach.
4. `ANALYZE`s.
5. For each case, runs the statement `N` times (`-repeat`, default 20) and reports min/median/max wall time **and** which statement was timed.
6. Writes a markdown report to `-out`.

The cases, at minimum:

| # | Case | Statement | Why |
|---|---|---|---|
| 1 | unfiltered list | `CountJobs` + `ListJobsWithEmailPage` with `Q` nil, `limit=50`, sort `-created_at` | the regression check for "unfiltered polling is unaffected" and for the pool-wide timeout |
| 2 | no-match needle | `CountJobsWithText` + `ListJobsWithEmailPage` with `Q` = the exact needle, same limit, same sort | **expected UNCHANGED, near 283 ms** |
| 3 | matching needle | same, with a needle that matches roughly 1% of rows | records that the cost is not monotone in match count, which is why "the worst case is a needle that matches nothing" |

Report the count statement and the list statement **separately**. The item's 283 ms does not say which it measured, and repeating that ambiguity would be the same defect one lane later.

- [ ] **Step 2: Take the measurement and record every input**

Run it, then write `docs/retros/2026-09-04-q-cost-measurement.md`. **Every number must carry, in the same sentence:**

- the row count in `jobs` **and** in `users`;
- **the exact needle string**, quoted;
- whether it matches zero rows, and if not, how many;
- the sort arm;
- the `limit`;
- any other filters on the request (state "none" explicitly);
- the Postgres version;
- whether the machine was otherwise idle;
- whether the number is the **count** statement, the **list** statement, or the whole request.

- [ ] **Step 3: Write the honest conclusion**

The write-up must state, in its own words and prominently:

> **This slice does not make the scan cheaper.** The no-match needle is expected to measure unchanged, near the 283 ms the item recorded. What changes is how many of these one principal can buy per window, not what one costs. `idea-2026-09-03-pg-trgm-index-for-text-search` is the item that reduces the cost; landing either does not close the other.

And it must note that the item's 283 ms and the pg_trgm item's "about 31 ms" **are not comparable and neither says so**: one is described as database CPU for a request at 200k rows, the other as a plan node's share at 50k rows, with no needle recorded in either. A reader who puts them side by side infers a scaling curve neither measurement supports.

- [ ] **Step 4: Count the first-party cadence, per view, against a running server**

The spec's table is **configured cadences read from source, not observed counts**. Confirmed against the tree: `useJobLanes` maps `LANE_ORDER` (5 statuses) with `refetchInterval: intervalMs` defaulting to `3000`. TanStack does not start an interval refetch while one is in flight for the same key, so the real counts are **at or below** the table.

Procedure:

1. `scripts/dev.ps1` for Postgres; `make build`; run `relay-server`.
2. Seed a handful of jobs and schedules so the lists are non-empty. **Measure the populated state** - an empty table can behave differently.
3. Open the SPA, type a needle into the search box, and let it settle.
4. In the browser Network panel, filter on `q=`, clear, and count requests over **exactly 60 seconds**.
5. Repeat for each of: jobs table view, jobs lanes view, jobs timeline view, schedules page.

Record four numbers, **each with the view it was taken in**, in the same document. The table's worst case is the lanes view at roughly 100 per minute; if your observation is materially below that, say so and say why (in-flight suppression is the expected reason). `120:10s` is about 5x the worst realistic legitimate 10 s window for one tab, which survives four tabs.

- [ ] **Step 5: Commit**

```bash
git add scripts/qcost/ docs/retros/2026-09-04-q-cost-measurement.md
git commit -m "Measure the q= amplifier again, recording the needle and the row count with every number"
```

---

## Task 13: Documentation - the sentences that become false

**Files:**
- Modify: `README.md` (environment table; the `?q=` cost paragraph; the write bucket's `Retry-After` row)
- Modify: `web/src/jobs/JobsPage.tsx` (one comment)

A wrong contract in docs is a defect. These sentences are true today and false the moment this lands.

- [ ] **Step 1: The environment table**

Add two rows beside `RELAY_DB_MAX_CONNS` and the two existing rate limits:

```
| `RELAY_DB_STATEMENT_TIMEOUT` | `30s` | Go duration. Applied as `statement_timeout` on every pooled connection, overriding any value the DSN supplies. A statement exceeding it is cancelled by the server and the request answers `500`. **Migrations are not affected** - they run on their own connection before the pool exists. A value below `1ms` is refused at startup, because Postgres would round it to `0` and read that as DISABLED. `0` means relay sets nothing, leaving the DSN, role or server default; the server logs that the control is unarmed. |
| `RELAY_JOB_SEARCH_RATE_LIMIT` | `120:10s` | Per-authenticated-user burst ceiling on `?q=` text searches, shared across `GET /v1/jobs` and `GET /v1/scheduled-jobs` (format `N:duration`). Only requests carrying a non-empty needle are counted: `?q=` and `?q=%20%20` are absent and cost nothing, and a `q` refused with a `400` costs nothing. Over the ceiling answers `429 {"error": "search rate limit exceeded"}` with a `Retry-After` header. There is no off value - the escape is a large number such as `100000:1s`. |
```

- [ ] **Step 2: The `?q=` cost paragraph**

Replace this sentence, which is currently in README's "Filtering the jobs list" section under **`?q=` cost**:

> The server applies no rate limit and no statement timeout to this today, so the cost is bounded only by the table size and by how often clients ask.

with:

> The server applies two bounds and neither makes the scan cheaper. `RELAY_DB_STATEMENT_TIMEOUT` (default `30s`) caps how long any single statement may hold a pool connection; at the measured cost of a no-match needle it will not fire, and it exists so that a growing table, a flipped plan or a contended box cannot turn one request into an unbounded hold. A search that does exceed it answers `500`, like any other database failure. `RELAY_JOB_SEARCH_RATE_LIMIT` (default `120:10s`) caps how many text searches one authenticated principal may issue, across this endpoint and `GET /v1/scheduled-jobs` together; it is a fairness bound on pool occupancy, not a CPU budget - 120 per 10 s is more database time per second than the box has. **What one search costs is unchanged; how many of them one principal can buy is not.** Neither bound is per-IP and neither degrades gracefully across accounts: the key is the user id, so extra tokens for one user share one bucket and extra accounts do not. Debouncing at 250 ms or more client-side still reduces how many of these a typing user generates; it still does not bound what a caller can request.

- [ ] **Step 3: Check the schedules paragraph**

README's schedules `?q=` paragraph reads *"`?q=` costs what it costs on the jobs list, for the same reason and with the same advice: see [`?q=` cost](#filtering-the-jobs-list)."* It inherits the rewrite by reference. **Read it again after step 2 and confirm it still reads correctly** - specifically that "the same advice" is still true now that the advice names a shared bucket. If it is not, add one sentence naming the sharing; do not copy the numbers.

- [ ] **Step 4: The write bucket's `Retry-After` row**

Locate the write-side row found in task 0 step 2. If it makes an unqualified `Retry-After` claim, correct it in this same edit, appending:

> The header is present and correct HTTP, and a scripted or third-party client can act on it. **The bundled SPA does not read it**: `ApiError` in `web/src/lib/api.ts` carries only the status and the parsed `{error}` string, and `apiFetch` never touches `res.headers`.

**Verified in this worktree:** `ApiError` is constructed as `new ApiError(res.status, code, ...)` and declares `readonly status`, `readonly code` and a message. There is no header field and no `res.headers` read anywhere in `apiFetch`. Repeating a known-wrong advertisement because a sibling shipped it is the worst available reason.

- [ ] **Step 5: The `JobsPage` comment**

In `web/src/jobs/JobsPage.tsx`, the comment currently reads:

```
// THE DEBOUNCE IS NOT A BOUND. GET /v1/jobs carries no rate limit and ?q= is an
// unindexed scan by design, so a caller that is not a typing user is unaffected
// by a client-side timer. It reduces how many scans one person's typing costs
// and bounds nothing else.
```

Its point survives; its premise does not. Replace with:

```
// THE DEBOUNCE IS NOT A BOUND. ?q= is an unindexed scan by design and a caller
// that is not a typing user is unaffected by a client-side timer. The server
// does bound it now - RELAY_JOB_SEARCH_RATE_LIMIT is a per-user ceiling on
// q-carrying requests, RELAY_DB_STATEMENT_TIMEOUT caps how long one statement
// may hold a connection - and this timer is neither of them. It reduces how many
// scans one person's typing costs and bounds nothing else. On a 429 the SPA does
// nothing visible: queryClient's retry: 1 turns one refusal into two requests,
// keepPreviousData keeps the previous rows on screen, and the table surfaces an
// error only when there is nothing else to render.
```

**Tailwind v4 scans prose as source.** This comment introduces no class-shaped substring (no `bg-`, `text-`, `p-` followed by a value), so no CSS is emitted. Confirm by eye before committing.

- [ ] **Step 6: Verify the edit did not corrupt the files**

```bash
git ls-files --eol README.md web/src/jobs/JobsPage.tsx
git diff --stat README.md web/src/jobs/JobsPage.tsx
```

Every path must read `i/lf`. The diffstat must be proportionate to the change you intended - a two-paragraph edit that reports hundreds of insertions means the file was reclassified as binary. Every byte you wrote above is ASCII; if any non-ASCII byte appears in the diff, it did not come from this plan.

- [ ] **Step 7: Commit**

```bash
git add README.md web/src/jobs/JobsPage.tsx
git commit -m "Correct every sentence that says q= is unbounded, and the Retry-After advertisement"
```

---

## Task 14: Full-suite verification

- [ ] **Step 1: Default lane**

Run: `make test`
Expected: PASS.

- [ ] **Step 2: Integration lane**

Needs Docker Desktop and `p4` on PATH.

Run: `go test -tags integration -p 1 ./internal/api/... -timeout 1800s`
Run: `go test -tags integration -p 1 ./internal/scheduler/... -timeout 900s`
Run: `go test -tags integration -p 1 ./cmd/relay-server/... -timeout 900s`

Expected: PASS. `TestListJobs_SortVersusFilterGuardOutranksArity` and `TestFilterQ_BodiesAreIdenticalAcrossEndpoints` must be green - both are the precedence and parity rules this slice sat down next to.

**Run the integration lane even though this slice adds behaviour rather than removing it**, because task 7 rewrote 25 call sites in two heavily-tested handlers and a converted site that swallowed an error would be invisible to diff review.

- [ ] **Step 3: Race detector**

`make test-race` is the canonical target. On this machine the native Windows lane is unreliable; the container is the route that works, and it is also the only local way to run the `//go:build !windows` files that Windows `go test` silently skips:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

The read bucket adds a `sync.Once` and a shared `map` behind an existing mutex, so this lane is load-bearing here. **If the lane is genuinely unavailable, say so plainly rather than substituting `-count=N`**, which re-runs under the ordinary scheduler and cannot observe an unsynchronised access that never happens to interleave badly.

If it fails naming `ThreadSanitizer failed to allocate`, that is environmental and intermittent - re-run at `origin/main` on an untouched package before concluding this change caused it.

- [ ] **Step 4: Web lane**

Run: `cd web && npm test`
Expected: PASS. The only change under `web/` is a comment; if anything here moves, find out why before continuing.

- [ ] **Step 5: `web/dist` hygiene**

`web/dist` is tracked but not maintained per PR. Before assembling the PR:

```bash
git checkout -- web/dist/
```

- [ ] **Step 6: Assemble the PR body**

It must contain:

1. The two facts from task 0 step 2 - which 429 string the write side shipped, and whether its README row needed the `Retry-After` correction.
2. Whether `userRateLimitKey` was reused or defined here, and if defined, the rebase instruction.
3. Every mutation from every battery, with **the named test it reddened and the branch that test guards**, plus the survivals - explicitly including M9d (mutating main.go's deps literal is green) and M3d being discarded as a compile error.
4. The measurement document's headline numbers **with their inputs**, and the sentence that the no-match needle is unchanged.
5. What is **not** covered, taken from the spec's section 13: no pg_trgm index, no bound on the unfiltered `CountJobs`, no counter on `/v1/server/counters`, no 57014-specific response, no `?q=` anywhere new, no SPA change, no aggregate bound across principals.

---

## Self-review

**Spec coverage.** Every numbered section of the spec maps to a task: §3 statement timeout -> tasks 2, 3, 10, 11; §3.5 the two failure modes -> task 2 steps 3-5 and task 3; §4 the read bucket and its number -> tasks 4, 8, 12 step 4; §5 one bucket over both routes -> task 6; §6 placement and the four proofs -> tasks 5 and 6; §7 what a refused caller sees -> task 4 step 5 and task 13 step 4; §8 `maxFilterQRunes` -> task 13 step 2's prose; §9 re-measurement -> task 12; §10 wiring -> tasks 8 and 9; §11 documentation -> task 13; §12 testing -> tasks 10, 11, 14; §13 what is not covered -> task 14 step 6 item 5; §14 the item's contradictions -> task 1 and the report below.

**Placeholders.** None. Every code step carries the code. The three places that say "verify before trusting" - `isTokenLookup`, `setupTestDB`, `newTestServerWithPool`'s signature - name a specific file to open and a specific question to answer, because a plan-supplied test body is a guess and "matches the plan" is not verification.

**Type consistency.** `parseDBStatementTimeout(name, raw string) (string, error)`, `applyStatementTimeout(cfg *pgxpool.Config, param string)`, `dbStatementTimeoutLine(param string) string`, `(*Server).searchRateLimiter() *rateLimiter`, `(*Server).allowSearch(w http.ResponseWriter, u AuthUser) bool`, `userRateLimitKey(u AuthUser) (string, bool)`, `listQueryError(w http.ResponseWriter, r *http.Request, err error, msg string)`, `Server.SearchLimitN int`, `Server.SearchLimitWin time.Duration`, `httpServerDeps.searchLimitN int`, `httpServerDeps.searchLimitWin time.Duration`. Each is used with the same name and arity everywhere it appears.
