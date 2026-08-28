# CLI real-server integration lane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `internal/cli` an integration-tagged lane whose every test drives a real `internal/api` server over HTTP against a real Postgres, plus a CI job that runs it, so a response-shape drift in a handler reddens a CLI test instead of staying invisible to 26 hand-rolled `httptest` fixtures.

**Architecture:** Two new `//go:build integration` helper files in `package cli` supply a per-test Postgres (testcontainer by default, one freshly `CREATE`d database on a shared server when `RELAY_TEST_DATABASE_URL` is set) and a live `api.New(...)` wrapped in `httptest.NewServer`. The injection point is `Config.ServerURL` - `internal/relayclient` is the single HTTP path out of `internal/cli` and has no transport seam, so swapping the base URL is the entire wiring. Ten new tests call the unexported `doWorkers`, `doListJobs`, `doGetJob`, `doSubmit`, `doCancelJob`, `doSchedules`, `doAdminUsers` and `doLogs` entrypoints. No existing test is deleted or repointed.

**Tech Stack:** Go 1.26.2, `testcontainers-go` + `modules/postgres`, `pgx/v5` + `pgxpool`, `golang-migrate` (via `store.Migrate`), `testify/require`, GitHub Actions `services: postgres`.

**Source spec:** `docs/superpowers/specs/2026-08-27-cli-real-server-integration-lane.md`
**Backlog item closed:** `docs/backlog/idea-2026-08-23-cli-tests-never-hit-real-server.md`

---

## Slice independence declaration

- **Frontend slices: none.** This is Go plus one YAML workflow plus two Makefile/CLAUDE.md edits. Nothing under `web/` is touched. `relay-frontend-engineer` is not dispatched for this plan.
- **Parallelism: SEQUENTIAL. Do not run two engineers in Phase 3.** Tasks 3-9 all consume the harness built in Tasks 1-2, and Task 6 (the B1 fix) is defined by the RED that Task 5 leaves behind. There is no independent pair worth the risk: concurrent agents share one git index on this worktree ([[feedback_concurrent_agents_share_one_git_index]]), and the one genuinely disjoint task (Task 10, Makefile + workflow) is five minutes of work.
- **Owners:**

  | Task | Owner | Why |
  |---|---|---|
  | 1, 2 | `relay-integration-tester` | Container/DSN harness and server wiring - integration infrastructure. |
  | 3, 4, 5 | `relay-integration-tester` | Integration-tagged tests. |
  | 6 | **`relay-backend-engineer`** | Edits production code (`internal/cli/jobs.go`). The test half is already on disk from Task 5. |
  | 7, 8, 9 | `relay-integration-tester` | Integration-tagged tests. |
  | 10 | `relay-integration-tester` | Makefile target and CI lane. |
  | 11 | `relay-integration-tester` | CLAUDE.md routing rules (doc, no code). |
  | 12 | `relay-integration-tester` | Mutation battery - a verification task with its own acceptance. |
  | 13 | Conductor | Final gate measurements and `/backlog close`. |

  If the conductor prefers a single agent for the whole slice, `relay-integration-tester` may carry Task 6 as well - but Phase 4's review lenses must treat Task 6's diff as production code, not as test scaffolding.

- **Halt safety:** every task boundary leaves the tree green and committable. Task 6 is the only task with an intra-task RED, and it opens and closes it in the same task.

---

## What I refuted in the spec

Read once for contradiction before acting, per `docs/agent-team/README.md`. **I did not refute nothing.** Nine claims checked against the tree; six confirmed exactly, three refuted or corrected. Every task below encodes the corrected version, not the spec's version.

**Confirmed exactly (do not re-check):**

1. `store.Migrate(dsn string) error` in `internal/store/migrate.go`, doc comment requires a `pgx5://` scheme. The three existing copies splice it as `"pgx5" + dsn[len("postgres"):]`.
2. `store.Migrate` **is** already paid per test inside `newTestPool` and `newTestQueries` (`internal/api/testhelper_test.go`), so per-database migration in the shared-service mode is the existing cost minus the container, exactly as D2 claims.
3. `web/e2e/ensure-db.mjs` does do `const adminUrl = new URL(databaseUrl); adminUrl.pathname = '/postgres'` before `CREATE DATABASE`. The move D2 copies is real.
4. **No advisory-lock serialization.** `golang-migrate` v4.19.1's pgx/v5 driver takes `pg_advisory_lock` keyed on `database.GenerateAdvisoryLockId(p.config.DatabaseName, schema, table)`. Postgres advisory locks are cluster-global, so this WOULD serialize if two tests migrated the same database name - but every per-test database has a distinct generated name, so the lock ids differ and concurrent per-database migrations against one server do not block each other. **Recorded because it is a live trap for the template-database optimisation D2 defers**: a fixed template name puts every migration back on one lock.
5. The `handleListWorkers` compile constraint is real. Its final statement is `writeJSON(w, http.StatusOK, page[workerResponse]{Items: items, NextCursor: next, Total: total})` and it is the **only** read of `next` (`var next string`) and `total` (`total, err := s.q.CountWorkers(ctx)`). Replacing that literal without `_, _ = next, total` is `declared and not used` on both - a non-result, not a survival, exactly as D4 warns.
6. CI numbers do not collide. `go-ci.yml` has one job, `test`, at `timeout-minutes: 15` running `go test -race ./... -timeout 180s`; `make test-integration` uses `-timeout 900s`; `web-ci.yml`'s `web` job is `timeout-minutes: 20`. A new job at `timeout-minutes: 10` with `-timeout 480s` collides with nothing, and the `services: postgres` block D3 copies from `web-ci.yml` is quoted accurately in substance (image, the three `POSTGRES_*` env vars, `5432:5432`, and the four `--health-*` options). D3 renders it in YAML flow style; **Task 10 writes it in the repo's block style instead**, matching `web-ci.yml`'s structure.

**Refuted / corrected:**

- **R-a. Test 5 cannot assert "the rendered hostname".** `doWorkersList`'s non-revoked table is `ID NAME STATUS CPU RAM GB GPUS GPU MODEL` and it prints `wk.Name`. There is **no HOSTNAME column**. The spec's test 5 as written would either fail or, worse, pass because the fixture set `name == hostname`. Corrected two ways in Task 4: the list test asserts the **NAME** column, and `seedWorker` gives every worker a `Name` that differs from its `Hostname` (`<host>-display`), so a name/hostname transposition cannot survive ([[reference_same_typed_args_transpose_silently]]). `hostname` is then covered where it is actually load-bearing: Task 3's delete-by-hostname test, whose resolution runs through `resolveWorkerIDIn`'s `wk.Hostname == target`.
- **R-b. Tests 9 and 10 do NOT exit cleanly.** The spec says "assert all 201 lines in order on stdout and a clean exit". It is not clean. The sequence cancels the job, so the final job status is `cancelled`; `watchOutcomeError` returns `silentError{}` for any complete-output run whose status is not `done`. `doLogs` therefore returns a **non-nil** error. Task 9's tests assert `errors.As(err, &silentError{})`, not `require.NoError`. A test written the spec's way goes red on the happy path and would be "fixed" by deleting the assertion.
- **R-c. The B2 count is 19, not 29.** `rg 'relayclient\.PageEnvelope\[' internal/cli --glob '*_test.go'` returns **26** sites across 10 files. Seven of them (`admin_output_test.go` x2, `admin_users_test.go` x5) are `PageEnvelope[map[string]any]` with literal keys - simulators, not tautologies, exactly as R1 says. That leaves **19** sites encoding through the CLI's own response structs, across the eight files R1 names (whose file list is correct). Task 11's CLAUDE.md paragraph says 19; the B2 backlog item must say 19.

**Additional finding the spec does not state, and Task 12 depends on it:** `internal/api` has untagged test files (`workers_response_test.go`, `list_endpoint_projection_test.go`, `pagination_test.go`, and others), so M1 and M2 may well redden `go test ./internal/api/...` in the default lane too. That is a good fact and it is **not** the claim under test. Task 12 records `./internal/api/...` as a third, separately-labelled column so the transcript cannot be read as "the default lane caught nothing".

---

## File structure

**Create (all `//go:build integration`, all `package cli`):**

| File | Responsibility |
|---|---|
| `internal/cli/pgharness_integration_test.go` | `newIntegrationDSN(t) string` and its two modes. **Imports only `relay/internal/store`, `pgx`, testcontainers, stdlib, testify. Nothing from `internal/cli`, nothing from `internal/api`.** This is the extraction candidate for B4 and the import rule is a review condition, not a suggestion ([[reference_guard_never_sees_real_producer]]). |
| `internal/cli/relayharness_integration_test.go` | `relayServer`, `startRelayServer`, `adminCfg`/`userCfg`, `testCtx`, and the seeding helpers (`seedUserWithToken`, `seedWorker`, `seedLogRows`, `writeSpecFile`, `uuidString`, `firstTaskID`). Returns production types plus its own struct; takes and returns nothing declared in `internal/cli` beyond `*Config`. |
| `internal/cli/workers_delete_integration_test.go` | Tests 1-4: the four-deep refusal ladder. |
| `internal/cli/workers_list_integration_test.go` | Test 5: the container-axis and field-axis witness. |
| `internal/cli/jobs_integration_test.go` | Test 6: submit -> list -> get round trip, plus Task 6's commands assertion. |
| `internal/cli/schedules_integration_test.go` | Test 7: create -> list -> show, including the `time.Time` / `*time.Time` pairing. |
| `internal/cli/admin_users_integration_test.go` | Test 8: admin users list/get plus the non-admin 403. |
| `internal/cli/logs_integration_test.go` | Tests 9-10: the real page boundary and the exact-multiple drain. |

**Modify:**

| File | Change |
|---|---|
| `internal/cli/jobs.go` | `taskResp.Command []string \`json:"command"\`` -> `taskResp.Commands [][]string \`json:"commands"\``. Task 6. |
| `Makefile` | New `test-cli-integration` target + `.PHONY` entry. Task 10. |
| `.github/workflows/go-ci.yml` | New `cli-integration` job. Task 10. |
| `CLAUDE.md` | One `make` line under Commands, one routing-rules paragraph under Key Design Decisions. Task 11. |

**Never touched:** any existing `_test.go` in `internal/cli`, any file under `internal/api` except transiently during Task 12's mutations (each reverted in the same step), `internal/store/*.sql.go`, `models.go`, anything under `web/`.

---

## The B1 decision: IN, and sequenced to produce the strongest possible proof

**Decision: fix the `command`/`commands` drift in this slice, at Task 6, after Task 5 has already put the passing half of the round trip on disk.**

The defect, confirmed independently at HEAD:

- `internal/api`'s `taskResponse` (in `internal/api/jobs.go`) carries `Commands json.RawMessage \`json:"commands"\``, populated by `toTaskResponse` from `t.Commands`.
- `internal/cli`'s `taskResp` (in `internal/cli/jobs.go`) carries `Command []string \`json:"command"\``.
- Migration `000008_task_commands.up.sql` does `ALTER TABLE tasks ADD COLUMN commands JSONB`, backfills, then `ALTER TABLE tasks DROP COLUMN command`. The server has not emitted a `command` key since.
- The field decodes to nil on every response, and `doGetJob`'s `--json` and `--pretty` paths re-encode `jobResp`, so `relay get <job-id> --json` emits `"command":null` and carries no task commands at all.
- **Nothing else in `internal/cli` reads the field.** `rg '\.Command\b|Command:' internal/cli` returns zero matches outside the struct declaration. The fix is one field.

**Why in, not filed:**

1. The lane's entire purpose is catching this defect class. Shipping the lane beside a live, known instance of its own class is a weak deliverable.
2. It converts a synthetic mutation into a real one. A test that goes RED against unfixed production code and GREEN against the fix is strictly better evidence than M1 and M2, which are mutants a human wrote to be caught. This is the one thing in the slice that cannot be gamed.
3. The diff is three lines of production code in one file with zero other readers.

**The spec's argument for out is honoured, not overridden.** D4 says the lane must not assert the defect's OUTPUT ([[reference_test_green_because_of_the_bug]]). It does not. Task 5 ships the round-trip test with no `command` assertion at all - green, committable, correct. Task 6 then ADDS the `commands` assertion, observes it RED against unfixed code, and fixes it. At no point does any test pin `"command":null`.

**The contract decision, stated rather than made silently.** `relay get <job-id> --json` changes from emitting `"command":null` to emitting `"commands":[["echo","..."]]`. Nothing can depend on the old key's value, because it was `null` for every task on every response since 2026-05. The new key is the one the server already emits, so the CLI's `--json` and the server's own JSON stop disagreeing. **Not in scope:** adding a COMMAND column to `doGetJob`'s human-readable task table. That is a rendering change with no defect behind it; leave it.

**Note for whoever writes the fix:** `internal/api`'s request-side `taskSpec` still accepts `command` (singular) and `jobspec.Validate` normalises it into `Commands`. So `command` is a live REQUEST key and a dead RESPONSE key, which is exactly why the CLI's decoder looked plausible. Say so in the field's comment.

---

## Task 1: the Postgres harness (`newIntegrationDSN`)

**Owner:** `relay-integration-tester`

**Files:**
- Create: `internal/cli/pgharness_integration_test.go`

**Context you need before writing a line.** This package has never had a `//go:build integration` file, and there is no `TestMain` anywhere in the module, so nothing is inherited. `store.Migrate` requires the `pgx5://` scheme (read its doc comment in `internal/store/migrate.go`). The `WithOccurrence(2)` wait strategy is **load-bearing and must be copied verbatim**: `postgres:16` prints "database system is ready to accept connections" once during its own init pass, before the real listener is up.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/pgharness_integration_test.go` with exactly this content. The helper comes in Step 3, so this step's file does not compile - which is the RED.

```go
//go:build integration

package cli

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/cli/... -run TestIntegration_HarnessDSNIsMigratedAndEmpty -count=1 -timeout 300s`
Expected: FAIL - a build error, `undefined: newIntegrationDSN`.

- [ ] **Step 3: Write the implementation**

Rewrite the file so it reads: build tag, package, the import block below, constants, helpers, then the test from Step 1 unchanged.

```go
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
```

- [ ] **Step 4: Run test to verify it passes, in BOTH modes**

Run (container mode - Docker Desktop must be running):
`go test -tags integration ./internal/cli/... -run TestIntegration_HarnessDSNIsMigratedAndEmpty -count=1 -timeout 300s -v`
Expected: `--- PASS: TestIntegration_HarnessDSNIsMigratedAndEmpty` then `ok relay/internal/cli`.

Run (shared-service mode - needs a Postgres at `127.0.0.1:5432`; on this machine `scripts/dev.ps1` manages one). In PowerShell:
```powershell
$env:RELAY_TEST_DATABASE_URL = "postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable"
go test -tags integration ./internal/cli/... -run TestIntegration_HarnessDSNIsMigratedAndEmpty -count=1 -timeout 300s -v
Remove-Item Env:\RELAY_TEST_DATABASE_URL
```
Expected: PASS, and visibly faster (no container start).

Use `127.0.0.1`, never `localhost`: `localhost` can resolve to `::1` first and a published Docker port may not answer there. `web/e2e/ensure-db.mjs` carries the same note.

- [ ] **Step 5: Confirm the default lane is untouched**

Run: `go test ./internal/cli/... -count=1 -timeout 120s`
Expected: `ok relay/internal/cli` in roughly 1.0s. The new file is `//go:build integration`, so this is true by construction - the run is here to catch a missing or misspelled build tag.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/pgharness_integration_test.go
git commit -m "test(cli): per-test Postgres harness with a testcontainer and a shared-service mode"
```

---

## Task 2: the relay-server harness (`startRelayServer`)

**Owner:** `relay-integration-tester`

**Files:**
- Create: `internal/cli/relayharness_integration_test.go`

**Context.** `api.New`'s signature is `New(pool *pgxpool.Pool, q *store.Queries, broker *events.Broker, registry *worker.Registry, corsOrigins []string, loginLimitN int, loginLimitWin time.Duration, registerLimitN int, registerLimitWin time.Duration) *Server`. The four trailing zeros disable the login and register rate limiters - `Handler()` only wraps those two routes when both N and Win are positive. `pool` is non-optional: `handleDeleteWorker` calls `s.pool.Begin` directly. `Config` is exactly `{ServerURL, Token}` and is built as a literal at every call site, so swapping the base URL is the entire injection.

Seeding needs **no test-only exports**: `bcrypt.GenerateFromPassword` at `bcrypt.MinCost` (`internal/api/export_test.go`'s `SetBcryptCostForTest` is `package api` and invisible here), then `q.CreateUserWithPassword`, then 16 random bytes to hex, `tokenhash.Hash`, `q.CreateToken` with a zero `pgtype.Timestamptz{}` (SQL NULL, never expires). This mirrors `createTestUser`/`createTestToken` in `internal/api/api_test.go`.

**Deliberately not wired:** gRPC, `scheduler.Dispatcher`, `schedrunner`, the metrics sweeper, `GraceRegistry`, the stale-task watchdog, `webui.Handler()`, `bootstrapAdmin`. Consequence: no task ever runs and no job leaves `pending` on its own. `worker.NewRegistry()` is empty, so `handleDisableWorker`/`handleDeleteWorker` send no cancels - which is why worker status is driven through `UpsertWorkerByHostname` + `UpdateWorkerStatus`, not through a connection.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/relayharness_integration_test.go` containing only this test for now.

```go
//go:build integration

package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIntegration_HarnessServesAndAuthenticates is the server harness's own
// test, for the same reason Task 1's exists: it makes every later RED
// attributable to the endpoint under test rather than to the wiring.
func TestIntegration_HarnessServesAndAuthenticates(t *testing.T) {
	s := startRelayServer(t)

	var out bytes.Buffer
	require.NoError(t, doWorkers(testCtx(t), s.adminCfg(), []string{"list"}, &out))
	require.Contains(t, out.String(), "Total: 0")

	// The non-admin token authenticates but is not an admin. /v1/workers is
	// auth-only, so this must succeed too - it pins that userCfg is a VALID
	// token and not merely a broken one, which every 403 test below relies on.
	var userOut bytes.Buffer
	require.NoError(t, doWorkers(testCtx(t), s.userCfg(), []string{"list"}, &userOut))
	require.Contains(t, userOut.String(), "Total: 0")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/cli/... -run TestIntegration_HarnessServesAndAuthenticates -count=1 -timeout 300s`
Expected: FAIL - build errors, `undefined: startRelayServer`, `undefined: testCtx`.

- [ ] **Step 3: Write the implementation**

Replace the file with the full version below. Note the **cleanup ordering**: `newIntegrationDSN` registers its teardown first, then `pool.Close`, then `httpSrv.Close`. `t.Cleanup` is LIFO, so they run server -> pool -> drop/terminate. That order is required and it is a consequence of the call order in this function; do not reorder the calls.

```go
//go:build integration

package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/tokenhash"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// relayServer is one live internal/api server over HTTP, backed by one Postgres
// database that belongs to the test that started it.
//
// It exposes ONLY production types plus itself. It must never take or return
// anything declared in internal/cli beyond *Config, which is this package's own
// two-field config struct and the single injection point.
type relayServer struct {
	BaseURL    string
	Pool       *pgxpool.Pool
	Q          *store.Queries
	AdminToken string
	UserToken  string
	AdminEmail string
	UserEmail  string
}

func (s *relayServer) adminCfg() *Config { return &Config{ServerURL: s.BaseURL, Token: s.AdminToken} }
func (s *relayServer) userCfg() *Config  { return &Config{ServerURL: s.BaseURL, Token: s.UserToken} }

// testCtx returns a context with an EXPLICIT deadline, and every doX call in
// this lane must be given one. t.Context() alone is not enough: it is cancelled
// at test END, so a hang inside doLogs' SSE wait would consume the whole
// package timeout and produce the nameless panic: banner the teardown backlog
// item describes. handleEvents holds a connection open with no heartbeat and no
// server-side timeout, so that hang is reachable, not theoretical.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func startRelayServer(t *testing.T) *relayServer {
	t.Helper()
	dsn := newIntegrationDSN(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	q := store.New(pool)
	// The four trailing zeros disable the login and register rate limiters.
	// Handler() only wraps those two routes when both N and Win are positive, so
	// 0 means "off", not "zero requests allowed" - startRelayForMCP in
	// internal/mcp passes the same zeros and then requires a real
	// POST /v1/auth/login to return 201.
	//
	// NOT WIRED, deliberately: gRPC, scheduler.Dispatcher, schedrunner, the
	// metrics sweeper, GraceRegistry, the stale-task watchdog, webui.Handler()
	// and bootstrapAdmin (which is package main and unimportable). No task ever
	// runs here and no job leaves pending on its own.
	apiSrv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	httpSrv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(httpSrv.Close)

	s := &relayServer{
		BaseURL:    httpSrv.URL,
		Pool:       pool,
		Q:          q,
		AdminEmail: "admin@cli-lane.test",
		UserEmail:  "user@cli-lane.test",
	}
	s.AdminToken = seedUserWithToken(t, q, s.AdminEmail, true)
	s.UserToken = seedUserWithToken(t, q, s.UserEmail, false)
	return s
}

// seedUserWithToken creates a user and an API token for it using only exported
// production symbols, and returns the raw hex token the client presents.
func seedUserWithToken(t *testing.T, q *store.Queries, email string, isAdmin bool) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("cli-lane-password"), bcrypt.MinCost)
	require.NoError(t, err)
	u, err := q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name:         email,
		Email:        email,
		IsAdmin:      isAdmin,
		PasswordHash: string(hash),
	})
	require.NoError(t, err)

	raw := make([]byte, 16)
	_, err = rand.Read(raw)
	require.NoError(t, err)
	rawHex := hex.EncodeToString(raw)
	_, err = q.CreateToken(t.Context(), store.CreateTokenParams{
		UserID:    u.ID,
		TokenHash: tokenhash.Hash(rawHex),
		ExpiresAt: pgtype.Timestamptz{}, // SQL NULL: never expires
	})
	require.NoError(t, err)
	return rawHex
}

// seedWorker inserts a worker, forces its status, and returns its uuid as text.
//
// name AND hostname are both parameters and callers must pass DIFFERENT values.
// They are both strings on both sides of the wire, so a fixture that sets them
// equal makes a name/hostname transposition invisible - the list test asserts
// the NAME column while the delete test resolves by HOSTNAME, and only distinct
// values make those two assertions independent.
func seedWorker(t *testing.T, s *relayServer, name, hostname, status string) string {
	t.Helper()
	w, err := s.Q.UpsertWorkerByHostname(t.Context(), store.UpsertWorkerByHostnameParams{
		Name:     name,
		Hostname: hostname,
		CpuCores: 16,
		RamGb:    64,
		GpuCount: 2,
		GpuModel: "RTX 4090",
		Os:       "linux",
	})
	require.NoError(t, err)
	// workers.status defaults to 'offline'; set it explicitly anyway so each
	// test states the status its assertion depends on.
	_, err = s.Q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{
		ID:     w.ID,
		Status: status,
	})
	require.NoError(t, err)
	return uuidString(t, s, w.ID)
}

// uuidString renders a pgtype.UUID the way the server does, by asking Postgres
// rather than by hand-writing the format string a seventh time (internal/api's
// uuidStr, cmd/relay-server, internal/metrics, internal/scheduler,
// internal/worker and internal/cli's canonicalJobID already carry copies).
func uuidString(t *testing.T, s *relayServer, id pgtype.UUID) string {
	t.Helper()
	var out string
	require.NoError(t, s.Pool.QueryRow(t.Context(), `SELECT $1::uuid::text`, id).Scan(&out))
	return out
}

// firstTaskID returns the id of a job's first task, read straight from the
// database. The API route would work too; the pool is used because it cannot
// itself be broken by the response-shape drift this lane exists to catch.
func firstTaskID(t *testing.T, s *relayServer, jobID string) string {
	t.Helper()
	var id string
	require.NoError(t, s.Pool.QueryRow(t.Context(),
		`SELECT id::text FROM tasks WHERE job_id = $1::uuid ORDER BY created_at, id LIMIT 1`,
		jobID).Scan(&id))
	return id
}

// seedLogRows inserts n log rows for a task directly via the pool, exactly as
// internal/api/tasks_integration_test.go's seedLogRow already does.
// GetTaskLogsPage is `WHERE task_id = $1 AND id > $2` with no fence of any
// kind, so no agent and no gRPC is needed - and the cost, that AppendTaskLog's
// epoch/identity/recency fence goes unexercised, is a recorded uncovered axis.
//
// ROW 1 CARRIES THE DISCRIMINATING STREAM VALUE and row n the discriminating
// content. A distinctive input placed only at the END cannot detect an
// early-exit defect ([[reference_mutation_proof_position]]), so both ends are
// distinctive and both are asserted.
func seedLogRows(t *testing.T, s *relayServer, taskID string, n int) {
	t.Helper()
	_, err := s.Pool.Exec(t.Context(), `
		INSERT INTO task_logs (task_id, stream, content)
		SELECT $1::uuid,
		       CASE WHEN g = 1 THEN 'stderr' ELSE 'stdout' END,
		       'line-' || g
		FROM generate_series(1, $2::int) AS g
		ORDER BY g`, taskID, n)
	require.NoError(t, err)
}

// writeSpecFile writes a job spec into the test's temp dir and returns its path.
// doSubmit and doSchedulesCreate both read a real file named on argv, so the
// lane gives them one rather than reaching past the entrypoint.
func writeSpecFile(t *testing.T, spec string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "job.json")
	require.NoError(t, os.WriteFile(path, []byte(spec), 0o600))
	return path
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags integration ./internal/cli/... -run 'TestIntegration_Harness' -count=1 -timeout 300s -v`
Expected: both `TestIntegration_HarnessDSNIsMigratedAndEmpty` and `TestIntegration_HarnessServesAndAuthenticates` PASS.

`seedWorker`, `uuidString`, `firstTaskID`, `seedLogRows` and `writeSpecFile` have no callers yet. Unused functions in a `_test.go` file do not fail the build; Tasks 3-9 use all five. Do not delete them.

- [ ] **Step 5: Verify the integration-tagged build**

Run: `make vet-integration`
Expected: no output, exit 0. This is the gate CI's `test` job already runs (`.github/workflows/go-ci.yml`, step "Integration-tagged build check"), so a compile break here would redden an existing required check.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/relayharness_integration_test.go
git commit -m "test(cli): live relay-server harness for the integration lane"
```

---

## Task 3: workers delete - the four-deep refusal ladder (tests 1-4)

**Owner:** `relay-integration-tester`

**Files:**
- Create: `internal/cli/workers_delete_integration_test.go`

**Context you must have right or these tests measure the wrong thing.** `DELETE /v1/workers/{id}` is routed `auth(admin(...))` in `internal/api/server.go`. The ladder, in order:

1. non-admin token -> **403 "admin access required"** from `AdminOnly`, before anything else;
2. malformed id -> **400 "invalid worker id"** from `parseUUID`;
3. well-formed id naming no row -> **404 "worker not found"** from `GetWorkerForUpdate`;
4. row whose status is not `offline` or `revoked` -> **409** with the handler's own long refusal text.

And **before any of those**, `doWorkersDelete` calls `resolveWorkerIDIncludingRevoked`, whose `looksLikeUUID` short-circuits for a UUID-shaped target. For a HOSTNAME that matches nothing it fails **locally** with `no worker found with hostname %q` and issues no delete request at all. So a 404 test is only reachable with a well-formed UUID that names no row.

Assert the STATUS CODE, not the message, wherever the code is the point: `doWorkersDelete` wraps with `fmt.Errorf("delete worker: %w", err)` and `relayclient.Client.Do` returns `*relayclient.ResponseError`, so `errors.As` reaches it. Asserting 403 rather than 409 on the non-admin test is what pins the ladder ORDER, which is the whole point of test 4.

- [ ] **Step 1: Write the tests**

```go
//go:build integration

package cli

import (
	"bytes"
	"errors"
	"testing"

	"relay/internal/relayclient"

	"github.com/stretchr/testify/require"
)

// TestIntegration_WorkersDelete_OfflineWorker_Succeeds targets the worker BY
// HOSTNAME, so it exercises resolveWorkerIDIncludingRevoked against the real
// list endpoint as well as the delete itself. The four counts come from the
// real deleteWorkerResponse, whose identity fields arrive via an EMBEDDED
// workerResponse - a flattening a hand-written fixture gets right only by
// accident.
func TestIntegration_WorkersDelete_OfflineWorker_Succeeds(t *testing.T) {
	s := startRelayServer(t)
	id := seedWorker(t, s, "render-node-a-display", "render-node-a", "offline")

	var out bytes.Buffer
	require.NoError(t, doWorkers(testCtx(t), s.adminCfg(),
		[]string{"delete", "--yes", "render-node-a"}, &out))

	got := out.String()
	require.Contains(t, got, `deleted worker "render-node-a" (`+id+`)`)
	require.Contains(t, got, "0 task(s) requeued")
	require.Contains(t, got, "0 reservation(s) updated")
	require.Contains(t, got, "0 enrollment(s) unlinked")
	require.Contains(t, got, "0 finished task(s) lost their worker attribution")

	var n int64
	require.NoError(t, s.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM workers`).Scan(&n))
	require.Zero(t, n)
}

// TestIntegration_WorkersDelete_ConnectedWorker_Is409 targets by UUID, so the
// list endpoint is never called and the 409 is attributable to the status gate
// alone.
func TestIntegration_WorkersDelete_ConnectedWorker_Is409(t *testing.T) {
	s := startRelayServer(t)
	id := seedWorker(t, s, "render-node-b-display", "render-node-b", "online")

	var out bytes.Buffer
	err := doWorkers(testCtx(t), s.adminCfg(), []string{"delete", "--yes", id}, &out)
	require.Error(t, err)

	var re *relayclient.ResponseError
	require.True(t, errors.As(err, &re), "want a ResponseError, got %T: %v", err, err)
	require.Equal(t, 409, re.StatusCode)
	require.Contains(t, err.Error(), "worker is connected")

	// The row survives a refusal.
	var n int64
	require.NoError(t, s.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM workers WHERE id = $1::uuid`, id).Scan(&n))
	require.EqualValues(t, 1, n)
}

// TestIntegration_WorkersDelete_UnknownUUID_Is404. The target MUST be
// UUID-shaped: for a hostname that matches nothing, doWorkersDelete fails
// locally in resolveWorkerIDIncludingRevoked and never issues a request, so a
// hostname here would assert nothing about the handler.
func TestIntegration_WorkersDelete_UnknownUUID_Is404(t *testing.T) {
	s := startRelayServer(t)

	var out bytes.Buffer
	err := doWorkers(testCtx(t), s.adminCfg(),
		[]string{"delete", "--yes", "3f2b1a0c-9d8e-4c7b-8a6f-5e4d3c2b1a09"}, &out)
	require.Error(t, err)

	var re *relayclient.ResponseError
	require.True(t, errors.As(err, &re), "want a ResponseError, got %T: %v", err, err)
	require.Equal(t, 404, re.StatusCode)
	require.Contains(t, err.Error(), "worker not found")
}

// TestIntegration_WorkersDelete_NonAdmin_Is403BeforeTheStatusGate pins the
// LADDER ORDER. The worker is online, so a delete that reached the handler
// would be a 409; DELETE /v1/workers/{id} is auth(admin(...)), so a non-admin
// gets 403 first. Asserting 403 and not merely "an error" is the whole test.
func TestIntegration_WorkersDelete_NonAdmin_Is403BeforeTheStatusGate(t *testing.T) {
	s := startRelayServer(t)
	id := seedWorker(t, s, "render-node-c-display", "render-node-c", "online")

	var out bytes.Buffer
	err := doWorkers(testCtx(t), s.userCfg(), []string{"delete", "--yes", id}, &out)
	require.Error(t, err)

	var re *relayclient.ResponseError
	require.True(t, errors.As(err, &re), "want a ResponseError, got %T: %v", err, err)
	require.Equal(t, 403, re.StatusCode,
		"a non-admin must be refused by AdminOnly BEFORE the status gate returns 409")
	require.Contains(t, err.Error(), "admin access required")
}
```

- [ ] **Step 2: Run the tests**

This task has **no artificial RED**. The production code it exercises already exists and is expected to be correct; the RED that proves this lane works is Task 6's, plus Task 12's mutation battery. Manufacturing a RED here (by writing a deliberately wrong expectation first) would prove nothing except that `require.Equal` works.

Run: `go test -tags integration ./internal/cli/... -run 'TestIntegration_WorkersDelete' -count=1 -timeout 480s -v`
Expected: 4 PASS. If any fails, the failure is real - read it before changing the assertion.

- [ ] **Step 3: Confirm the default lane and the tagged build**

Run: `go test ./internal/cli/... -count=1 -timeout 120s`
Expected: `ok relay/internal/cli` in roughly 1.0s.

Run: `make vet-integration`
Expected: exit 0, no output.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/workers_delete_integration_test.go
git commit -m "test(cli): real-server coverage for workers delete's four-deep refusal ladder"
```

---

## Task 4: workers list - the container and field witness (test 5)

**Owner:** `relay-integration-tester`

**Files:**
- Create: `internal/cli/workers_list_integration_test.go`

**Context.** `doWorkersList` renders `ID NAME STATUS CPU RAM GB GPUS GPU MODEL` and prints `wk.Name` - there is **no HOSTNAME column**, which is why this test asserts the NAME column and Task 3 covers `hostname` through resolution. `relayclient.FetchAllPages[workerResp]` decodes into `PageEnvelope`, so this is also the test that fails loudly if the endpoint ever returns a bare array again.

**Two assertion traps, resolved here so the engineer does not re-derive them.** (a) The table is rendered by `text/tabwriter`, which pads with **spaces, not tabs**, so a `NotContains(got, "farm-01\t")` guard against a name/hostname swap can never fire - it would be reassuring noise. (b) `Contains(got, "16")` is nearly vacuous: most UUIDs contain `16`. The test therefore extracts the worker's row, collapses its padding with `strings.Fields`, and compares the **whole row** in one `require.Equal`. That is a single assertion no subset of the fields can satisfy by accident, and it is what a name/hostname transposition breaks.

- [ ] **Step 1: Write the test**

```go
//go:build integration

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIntegration_WorkersList_RendersARealWorker is this lane's container-axis
// witness (the envelope: Total comes from CountWorkers, items from buildPage)
// and its field-axis witness for six worker fields at once.
//
// It asserts the NAME column, not a hostname: doWorkersList's non-revoked table
// is ID/NAME/STATUS/CPU/RAM GB/GPUS/GPU MODEL and prints wk.Name. There is no
// HOSTNAME column here at all - hostname is covered by the delete-by-hostname
// test, which resolves through resolveWorkerIDIn's `wk.Hostname == target`.
// seedWorker gives name and hostname DIFFERENT values, so the two assertions
// are independent and a transposition cannot satisfy both.
func TestIntegration_WorkersList_RendersARealWorker(t *testing.T) {
	s := startRelayServer(t)
	id := seedWorker(t, s, "farm-01-display", "farm-01", "offline")

	var out bytes.Buffer
	require.NoError(t, doWorkers(testCtx(t), s.adminCfg(), []string{"list"}, &out))

	got := out.String()
	require.Contains(t, got, "Total: 1")

	// The WHOLE row, with tabwriter's space padding collapsed. Asserting the
	// row rather than substrings is what stops "16" from matching a uuid.
	var row string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, id) {
			row = strings.Join(strings.Fields(line), " ")
		}
	}
	require.Equal(t, id+" farm-01-display offline 16 64 2 RTX 4090", row,
		"columns are ID NAME STATUS CPU RAM-GB GPUS GPU-MODEL; full output:\n%s", got)
}
```

- [ ] **Step 2: Run the test**

Run: `go test -tags integration ./internal/cli/... -run 'TestIntegration_WorkersList' -count=1 -timeout 480s -v`
Expected: PASS.

- [ ] **Step 3: Confirm the default lane**

Run: `go test ./internal/cli/... -count=1 -timeout 120s`
Expected: `ok relay/internal/cli`.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/workers_list_integration_test.go
git commit -m "test(cli): real-server workers list renders a real worker"
```

---

## Task 5: jobs submit -> list -> get round trip, WITHOUT the commands assertion (test 6)

**Owner:** `relay-integration-tester`

**Files:**
- Create: `internal/cli/jobs_integration_test.go`

**Context.** `doSubmit(ctx, cfg, args, w, errOut)` takes a second writer; `doListJobs`/`doGetJob`/`doCancelJob` take `(ctx, cfg, args, w)`. With `--detach`, `doSubmit` prints the job id and returns without watching. The request-side spec accepts `command` (singular); `jobspec.Validate` normalises it into `Commands`, and the response emits `commands`. **This task deliberately asserts nothing about either key.** Task 6 adds that.

- [ ] **Step 1: Write the test**

```go
//go:build integration

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// laneJobSpec is the spec every jobs/logs test submits. The REQUEST key is
// `command` (singular) - internal/api's taskSpec accepts it and
// jobspec.Validate normalises it into Commands - while the RESPONSE key is
// `commands`. That asymmetry is real and is the subject of the next task.
const laneJobSpec = `{
  "name": "lane-job",
  "priority": "high",
  "tasks": [
    {"name": "t1", "command": ["echo", "hello-from-the-lane"]}
  ]
}`

// submitLaneJob submits laneJobSpec with --detach and returns the job id.
func submitLaneJob(t *testing.T, s *relayServer) string {
	t.Helper()
	var out, errOut bytes.Buffer
	require.NoError(t, doSubmit(testCtx(t), s.adminCfg(),
		[]string{"--detach", writeSpecFile(t, laneJobSpec)}, &out, &errOut))
	id := strings.TrimSpace(out.String())
	require.NotEmpty(t, id)
	return id
}

func TestIntegration_SubmitListGet_RoundTrip(t *testing.T) {
	s := startRelayServer(t)
	jobID := submitLaneJob(t, s)

	// list: the envelope's Total and the row the real handler produced.
	var listOut bytes.Buffer
	require.NoError(t, doListJobs(testCtx(t), s.adminCfg(), nil, &listOut))
	list := listOut.String()
	require.Contains(t, list, "Total: 1")
	require.Contains(t, list, jobID)
	require.Contains(t, list, "lane-job")
	require.Contains(t, list, "pending")
	// submitted_by_email is enrichment the list handler joins in; a fixture
	// marshalled from jobResp would agree with the decoder whatever the
	// handler did.
	require.Contains(t, list, s.AdminEmail)

	// get: the detail body, including the nested task list.
	var getOut bytes.Buffer
	require.NoError(t, doGetJob(testCtx(t), s.adminCfg(), []string{jobID}, &getOut))
	got := getOut.String()
	require.Contains(t, got, "ID:           "+jobID)
	require.Contains(t, got, "Name:         lane-job")
	require.Contains(t, got, "Priority:     high")
	require.Contains(t, got, "Status:       pending")
	require.Contains(t, got, "Submitted by: "+s.AdminEmail)
	require.Contains(t, got, "Tasks:")
	require.Contains(t, got, "t1")
}
```

- [ ] **Step 2: Run the test**

Run: `go test -tags integration ./internal/cli/... -run 'TestIntegration_SubmitListGet' -count=1 -timeout 480s -v`
Expected: PASS.

- [ ] **Step 3: Confirm the default lane**

Run: `go test ./internal/cli/... -count=1 -timeout 120s`
Expected: `ok relay/internal/cli`.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/jobs_integration_test.go
git commit -m "test(cli): real-server submit/list/get round trip"
```

---

## Task 6: B1 - the lane catches a live bug (`command` vs `commands`)

**Owner:** `relay-backend-engineer`

**Files:**
- Modify: `internal/cli/jobs_integration_test.go` (add one test)
- Modify: `internal/cli/jobs.go` (`taskResp`)

**This is the strongest evidence in the slice.** Step 1 writes an assertion against production code that is wrong at HEAD, Step 2 observes it RED, Step 3 fixes production code, Step 4 observes it GREEN. Read the "B1 decision" section above before starting; it explains why this is in scope and what contract change it makes.

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/jobs_integration_test.go`:

```go
// TestIntegration_GetJobJSON_CarriesTheTasksCommands is a REAL instance of the
// defect class this lane exists for, not a synthetic mutation.
//
// internal/api's taskResponse emits `commands` (a [][]string, per migration
// 000008_task_commands, which dropped tasks.command and added tasks.commands).
// internal/cli's taskResp decoded `command` as []string - wrong key and wrong
// type - so `relay get <job-id> --json` emitted "command":null and carried no
// task definition at all, for every job, since 2026-05. The human-readable path
// prints only name/status/worker, which is why nobody saw it.
//
// The assertion is on the exact compact-encoded substring because doGetJob's
// --json path is json.NewEncoder(w).Encode(job) with no indent, so the key and
// its value appear adjacent and unspaced.
func TestIntegration_GetJobJSON_CarriesTheTasksCommands(t *testing.T) {
	s := startRelayServer(t)
	jobID := submitLaneJob(t, s)

	var out bytes.Buffer
	require.NoError(t, doGetJob(testCtx(t), s.adminCfg(), []string{jobID, "--json"}, &out))
	got := out.String()

	require.Contains(t, got, `"commands":[["echo","hello-from-the-lane"]]`)
	require.NotContains(t, got, `"command":null`,
		"the CLI must not re-emit a key the server stopped sending at migration 000008")
}
```

- [ ] **Step 2: Run the test to verify it fails - and record the failure**

Run: `go test -tags integration ./internal/cli/... -run 'TestIntegration_GetJobJSON_CarriesTheTasksCommands' -count=1 -timeout 480s -v`
Expected: FAIL. Both assertions fire. The output shows the actual body containing `"command":null` and no `commands` key.

**Copy the failure output into the PR body.** This is the recorded proof that the lane catches real drift, and it is worth more than M1 and M2 combined, because nobody wrote the bug to be caught.

- [ ] **Step 3: Write the minimal implementation**

In `internal/cli/jobs.go`, replace the `Command` field of `taskResp`. The struct becomes:

```go
type taskResp struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	// `commands`, plural, and [][]string: migration 000008_task_commands
	// dropped tasks.command (TEXT[]) and added tasks.commands (JSONB), and
	// internal/api's taskResponse has emitted `commands` ever since.
	//
	// `command` (singular, []string) is still a live REQUEST key - internal/api's
	// taskSpec accepts it and jobspec.Validate normalises it into Commands -
	// which is exactly why the decoder here looked right. It is not a RESPONSE
	// key and has not been one since 2026-05. Decoding it gave
	// `relay get <job-id> --json` a "command":null and no task definition at
	// all for three months, with the whole CLI suite green.
	Commands       [][]string      `json:"commands"`
	Env            json.RawMessage `json:"env"`
	Requires       json.RawMessage `json:"requires"`
	TimeoutSeconds *int32          `json:"timeout_seconds"`
	Retries        int32           `json:"retries"`
	DependsOn      []string        `json:"depends_on,omitempty"`
	WorkerID       string          `json:"worker_id,omitempty"`
}
```

Then run `gofmt -w internal/cli/jobs.go` so the field alignment matches the rest of the file.

No other change is needed: `rg '\.Command\b|Command:' internal/cli` returns no matches, so the field has no reader other than the `--json`/`--pretty` re-encode.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -tags integration ./internal/cli/... -run 'TestIntegration_' -count=1 -timeout 480s -v`
Expected: every `TestIntegration_` test PASSES, including both jobs tests.

- [ ] **Step 5: Run the full default lane and the tagged build**

Run: `go test ./... -count=1 -timeout 120s`
Expected: all packages `ok`. `taskResp.Command` had no readers, so nothing in the default lane should move; if something does, read it before "fixing" it.

Run: `make vet-integration`
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/jobs.go internal/cli/jobs_integration_test.go
git commit -m "fix(cli): relay get --json emitted command:null and carried no task commands"
```

---

## Task 7: schedules create -> list -> show (test 7)

**Owner:** `relay-integration-tester`

**Files:**
- Create: `internal/cli/schedules_integration_test.go`

**Context.** `doSchedules(ctx, cfg, args, w)` is the only entrypoint - the per-subcommand helpers take a `*relayclient.Client`, not a `*Config`. `doSchedulesCreate` requires `--name`, `--cron` and `--spec`. The pairing this test exists for: the server's `scheduledJobResponse.NextRunAt` is `time.Time` with tag `next_run_at` (no omitempty), while the CLI's `scheduleResp.NextRunAt` is `*time.Time` with `next_run_at,omitempty`. A fixture marshalled from `scheduleResp` cannot test that pairing; a real server can.

- [ ] **Step 1: Write the test**

```go
//go:build integration

package cli

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// nextColumn matches a rendered NEXT cell, which doSchedulesList formats as
// "2006-01-02 15:04". An empty NEXT means next_run_at decoded to nil, which is
// the failure this test is for.
var nextColumn = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}`)

func TestIntegration_SchedulesCreateListShow(t *testing.T) {
	s := startRelayServer(t)
	specPath := writeSpecFile(t, laneJobSpec)

	var createOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{
		"create",
		"--name", "nightly-lane",
		"--cron", "0 3 * * *",
		"--tz", "America/New_York",
		"--spec", specPath,
	}, &createOut))
	require.Contains(t, createOut.String(), "created: nightly-lane")

	var listOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{"list"}, &listOut))
	list := listOut.String()
	require.Contains(t, list, "Total: 1")
	require.Contains(t, list, "nightly-lane")
	require.Contains(t, list, "0 3 * * *")
	require.Contains(t, list, "America/New_York")
	require.Contains(t, list, "true") // enabled
	// The pairing a struct-encoded fixture cannot test: the server sends
	// next_run_at as a bare time.Time, the client decodes it into a
	// *time.Time with ,omitempty. A nil pointer renders an EMPTY cell.
	require.Regexp(t, nextColumn, list,
		"NEXT must be rendered, so next_run_at decoded non-nil")

	// show: read the id off the database rather than parsing the table.
	var scheduleID string
	require.NoError(t, s.Pool.QueryRow(t.Context(),
		`SELECT id::text FROM scheduled_jobs WHERE name = 'nightly-lane'`).Scan(&scheduleID))

	var showOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(),
		[]string{"show", scheduleID}, &showOut))
	show := showOut.String()
	require.Contains(t, show, "ID:       "+scheduleID)
	require.Contains(t, show, "Name:     nightly-lane")
	require.Contains(t, show, "Cron:     0 3 * * *")
	require.Contains(t, show, "Timezone: America/New_York")
	require.Contains(t, show, "Enabled:  true")
	require.Contains(t, show, "Next:     ")
}
```

- [ ] **Step 2: Run the test**

Run: `go test -tags integration ./internal/cli/... -run 'TestIntegration_SchedulesCreateListShow' -count=1 -timeout 480s -v`
Expected: PASS.

If `Next:` is absent from `show`, do **not** delete the assertion - `doSchedulesShow` only prints it when `out.NextRunAt != nil`, so its absence IS the bug this test is for.

- [ ] **Step 3: Confirm the default lane**

Run: `go test ./internal/cli/... -count=1 -timeout 120s`
Expected: `ok relay/internal/cli`.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/schedules_integration_test.go
git commit -m "test(cli): real-server schedules create/list/show, including next_run_at"
```

---

## Task 8: admin users list and get, plus the non-admin 403 (test 8)

**Owner:** `relay-integration-tester`

**Files:**
- Create: `internal/cli/admin_users_integration_test.go`

**Context.** `doAdminUsers(ctx, cfg, args, out)`. `doAdminUsersGet` takes an **email**, not an id, and issues `GET /v1/users?email=<escaped>` - `handleListUsers` has an `?email=` branch returning a one-item envelope. `GET /v1/users` is `auth(admin(...))`, so `userCfg` gets 403. `doAdminUsersList` wraps its error as `list users: %w`.

- [ ] **Step 1: Write the tests**

```go
//go:build integration

package cli

import (
	"bytes"
	"errors"
	"testing"

	"relay/internal/relayclient"

	"github.com/stretchr/testify/require"
)

func TestIntegration_AdminUsersListGet(t *testing.T) {
	s := startRelayServer(t)

	var listOut bytes.Buffer
	require.NoError(t, doAdminUsers(testCtx(t), s.adminCfg(), []string{"list"}, &listOut))
	list := listOut.String()
	require.Contains(t, list, "Total: 2")
	require.Contains(t, list, s.AdminEmail)
	require.Contains(t, list, s.UserEmail)

	var getOut bytes.Buffer
	require.NoError(t, doAdminUsers(testCtx(t), s.adminCfg(),
		[]string{"get", s.UserEmail}, &getOut))
	got := getOut.String()
	require.Contains(t, got, "Email:    "+s.UserEmail)
	require.Contains(t, got, "Admin:    no")
	require.Contains(t, got, "Archived: no")

	// The admin column for the OTHER user, read through the same detail view,
	// so `is_admin` is asserted in both directions rather than only in the one
	// a default-valued bool would satisfy.
	var adminOut bytes.Buffer
	require.NoError(t, doAdminUsers(testCtx(t), s.adminCfg(),
		[]string{"get", s.AdminEmail}, &adminOut))
	require.Contains(t, adminOut.String(), "Admin:    yes")
}

// TestIntegration_AdminUsersList_NonAdmin_Is403 pins that GET /v1/users is
// auth(admin(...)) and that the CLI surfaces the status rather than an empty
// list.
func TestIntegration_AdminUsersList_NonAdmin_Is403(t *testing.T) {
	s := startRelayServer(t)

	var out bytes.Buffer
	err := doAdminUsers(testCtx(t), s.userCfg(), []string{"list"}, &out)
	require.Error(t, err)

	var re *relayclient.ResponseError
	require.True(t, errors.As(err, &re), "want a ResponseError, got %T: %v", err, err)
	require.Equal(t, 403, re.StatusCode)
	require.Contains(t, err.Error(), "admin access required")
}
```

- [ ] **Step 2: Run the tests**

Run: `go test -tags integration ./internal/cli/... -run 'TestIntegration_AdminUsers' -count=1 -timeout 480s -v`
Expected: 2 PASS.

- [ ] **Step 3: Confirm the default lane**

Run: `go test ./internal/cli/... -count=1 -timeout 120s`
Expected: `ok relay/internal/cli`.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/admin_users_integration_test.go
git commit -m "test(cli): real-server admin users list/get and the non-admin 403"
```

---

## Task 9: logs across a real page boundary (tests 9-10)

**Owner:** `relay-integration-tester`

**Files:**
- Create: `internal/cli/logs_integration_test.go`

**Five facts this task rests on. Get any of them wrong and the test hangs or passes vacuously.**

1. **`doLogs` hangs on a non-terminal job.** `handleEvents` has no heartbeat and no server-side timeout, and nothing in this harness ever runs a task. The deterministic sequence is: submit -> insert log rows -> **cancel the job** -> call `doLogs`. `handleCancelJob` runs `CancelJobTasks`, which flips every non-terminal task to `failed` in one statement (its allow-list is `('pending','queued','running','dispatched')`, so a freshly submitted `pending` task is covered), and then sets the job to `cancelled`. `watchJobLogs`'s `onSubscribed` snapshot then sees a terminal job and a terminal task, `emitSnapshot` prints, and the stream is stopped from inside the callback. No event timing is involved. **The cancel must precede the `doLogs` call.**
2. **`doLogs` returns a non-nil error here.** Status is `cancelled`, output is complete, so `watchOutcomeError` returns `silentError{}` (which `Dispatch` turns into exit 1 with nothing printed). Assert that, not `require.NoError`.
3. **The discriminator is the LINE COUNT, not the error.** Under Task 12's M2 mutation the CLI still returns `silentError{}` and still reports the log as drained - it just prints 200 lines instead of 201. If this test asserts only the error, M2 survives and the battery reports a false negative.
4. **`relayclient.PageRequestLimit` is 200**, and `handleGetTaskLogs` sets `next_seq = 0` when `len(items) < limit`. 201 rows means two requests; exactly 200 rows means page 1 is full (non-zero `next_seq`) and page 2 comes back empty with `next_seq = 0`, which `printTaskLogs` returns on **before** reaching its empty-page error arm.
5. **Output format** is `fmt.Fprintf(out, "[%s %s] %s\n", taskName, l.Stream, l.Content)`, where `taskName` comes from the snapshot's task list.

- [ ] **Step 1: Write the tests**

```go
//go:build integration

package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// runLaneLogs submits the lane job, seeds n log rows for its only task, cancels
// the job so doLogs can terminate, then runs doLogs and returns its stdout
// lines.
//
// THE CANCEL MUST COME BEFORE doLogs. handleEvents holds the SSE connection
// open with no heartbeat and no server-side timeout, and nothing in this
// harness runs a task, so a non-terminal job makes doLogs wait forever -
// bounded only by testCtx's explicit 30s deadline, which is exactly what that
// deadline exists to convert into a named failure.
func runLaneLogs(t *testing.T, s *relayServer, n int) []string {
	t.Helper()
	jobID := submitLaneJob(t, s)
	seedLogRows(t, s, firstTaskID(t, s, jobID), n)

	var cancelOut bytes.Buffer
	require.NoError(t, doCancelJob(testCtx(t), s.adminCfg(), []string{jobID}, &cancelOut))
	require.Contains(t, cancelOut.String(), "cancelled")

	var out, errOut bytes.Buffer
	err := doLogs(testCtx(t), s.adminCfg(), []string{jobID}, &out, &errOut)

	// NOT NoError. CancelJobTasks makes the job `cancelled`, and
	// watchOutcomeError returns silentError{} for any complete-output run whose
	// final status is not "done". A cancelled job's logs printing IN FULL is
	// exactly this outcome, so silentError IS the pass condition here.
	var se silentError
	require.True(t, errors.As(err, &se),
		"want silentError (job cancelled, output complete), got %T: %v", err, err)
	require.Empty(t, errOut.String(), "no per-task incompleteness diagnostic expected")

	return strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
}

// TestIntegration_Logs_PagesARealLogAcrossThePageBoundary. 201 rows forces two
// requests at relayclient.PageRequestLimit == 200, so this is the only test in
// the repo that exercises the cursor protocol against the real handler:
// since_seq is EXCLUSIVE server-side (`WHERE task_id = $1 AND id > $2`), so the
// CLI must pass next_seq VERBATIM and never lastSeq+1 - task_logs.id is a
// global BIGSERIAL, so +1 skips the next row when one task is logging alone.
//
// The LINE COUNT is the load-bearing assertion. A CLI that stops one page early
// still returns silentError{} and still reports the log as drained; only the
// count tells the two apart.
func TestIntegration_Logs_PagesARealLogAcrossThePageBoundary(t *testing.T) {
	s := startRelayServer(t)
	lines := runLaneLogs(t, s, 201)

	require.Len(t, lines, 201)
	// Row 1 carries the discriminating stream value, deliberately NOT last.
	require.Equal(t, "[t1 stderr] line-1", lines[0])
	require.Equal(t, "[t1 stdout] line-2", lines[1])
	// Row 200 is the last row of page 1 and row 201 the only row of page 2, so
	// this pair straddles the page boundary itself.
	require.Equal(t, "[t1 stdout] line-200", lines[199])
	require.Equal(t, "[t1 stdout] line-201", lines[200])
}

// TestIntegration_Logs_ExactPageMultiple_TerminatesOnTheEmptyFinalPage. With
// exactly 200 rows, page 1 is FULL and therefore carries a non-zero next_seq,
// and page 2 comes back empty with next_seq = 0. That is the real handler's
// drain rule (`if int32(len(items)) < limit { nextSeq = 0 }`) under test rather
// than a fixture's imitation of it, and it is the input on which
// printTaskLogs' "empty page without reporting drained" error arm must NOT
// fire.
func TestIntegration_Logs_ExactPageMultiple_TerminatesOnTheEmptyFinalPage(t *testing.T) {
	s := startRelayServer(t)
	lines := runLaneLogs(t, s, 200)

	require.Len(t, lines, 200)
	require.Equal(t, "[t1 stderr] line-1", lines[0])
	require.Equal(t, "[t1 stdout] line-200", lines[199])
}
```

- [ ] **Step 2: Run the tests**

Run: `go test -tags integration ./internal/cli/... -run 'TestIntegration_Logs' -count=1 -timeout 480s -v`
Expected: 2 PASS.

If either test times out at 30s inside `doLogs`, the cancel did not take - check `doCancelJob`'s output for `cancelled` before debugging anything else.

- [ ] **Step 3: Confirm the default lane**

Run: `go test ./internal/cli/... -count=1 -timeout 120s`
Expected: `ok relay/internal/cli`. In particular the 42 tests in `logs_test.go` are untouched: D5 keeps them because most assert behaviour a real server cannot be made to produce (an empty page that does not report drained, a non-advancing cursor, a 500 on page 2, a stdout that rejects every write, a body describing another job) and those are why `printTaskLogs` has three stops.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/logs_integration_test.go
git commit -m "test(cli): real-server logs paging across a real page boundary"
```

---

## Task 10: the Makefile target and the CI job

**Owner:** `relay-integration-tester`

**Files:**
- Modify: `Makefile` (the `.PHONY` line, and a new target)
- Modify: `.github/workflows/go-ci.yml` (a new job)

- [ ] **Step 1: Add the Makefile target**

In `Makefile`, add `test-cli-integration` to the `.PHONY` list on line 1, so it reads:

```make
.PHONY: build test test-integration test-cli-integration test-race vet-integration generate clean python-test python-test-integration python-lint web-install web-build web-dev test-e2e
```

Then insert this target immediately after the existing `test-integration` target (whose recipe line is `go test -tags integration -p 1 ./... -timeout 900s`), before the `vet-integration` comment block:

```make
# Run the CLI real-server integration lane. Every test in it drives a live
# internal/api server over HTTP against a real Postgres, so a response-shape
# drift in a handler reddens here instead of staying invisible to
# internal/cli's httptest fixtures.
#
# Two modes, selected by RELAY_TEST_DATABASE_URL:
#   unset - one Postgres testcontainer per test (needs Docker), like every
#           other integration package in this repo.
#   set   - one freshly CREATEd database per test on the supplied server, e.g.
#           postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable (the
#           relay-postgres container scripts/dev.ps1 already manages). This is
#           what .github/workflows/go-ci.yml's cli-integration job uses, and the
#           command there is this same target so the two cannot drift.
#
# -p 1 is NOT needed: the pattern names one package. The 480s Go timeout is
# deliberately distinct from the 10-minute job timeout in CI so a Go panic and a
# GitHub job kill name themselves instead of looking identical.
test-cli-integration:
	go test -tags integration ./internal/cli/... -timeout 480s
```

- [ ] **Step 2: Verify the target runs**

Run (from Git Bash on Windows, as the Makefile's other sh-syntax targets require):
`make test-cli-integration`
Expected: `ok relay/internal/cli` with every `TestIntegration_` test passing.

- [ ] **Step 3: Add the CI job**

Append to `.github/workflows/go-ci.yml`, after the existing `test` job's final `run:` line, at the same indentation as `test:` (two spaces):

```yaml

  cli-integration:
    name: cli integration (real server)
    runs-on: ubuntu-latest
    # DELIBERATELY NOT 15. The `test` job above uses timeout-minutes: 15 and
    # `make test-integration` uses -timeout 900s - the same number - so today a
    # Go panic and a GitHub job kill are indistinguishable in the log. 10
    # minutes here against the target's -timeout 480s means the two failures
    # name themselves.
    timeout-minutes: 10
    services:
      # Same image, user, password and port as web-ci.yml's service and as the
      # relay-postgres container scripts/dev.ps1 manages locally, so
      # postgres://relay:relay@127.0.0.1:5432 is one string in all three.
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: relay
          POSTGRES_PASSWORD: relay
          POSTGRES_DB: relay
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
    env:
      # Setting this selects the harness's shared-service mode: one freshly
      # CREATEd database per test on the service above. No Docker API, no image
      # pull, no Ryuk reaper. The path names `postgres` because
      # newIntegrationDSN connects there only to issue CREATE/DROP DATABASE; no
      # test ever uses that database.
      #
      # 127.0.0.1, never localhost: localhost can resolve to ::1 first and a
      # published Docker port may not answer there. web/e2e/ensure-db.mjs
      # carries the same note.
      RELAY_TEST_DATABASE_URL: postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true
      # A SEPARATE JOB, not a step in `test`. A step would inherit required-check
      # status for free, which is a real advantage, but it would also attach this
      # Postgres service to the race job (making every race run wait on a health
      # check) and put both lanes on one 15-minute clock, which makes worse
      # exactly the ambiguity the timeout above is fixing.
      - name: CLI integration lane (real relay-server, real Postgres)
        run: make test-cli-integration
```

- [ ] **Step 4: Validate the YAML**

There is no yamllint in this repo's toolchain, so validate by reading: the new job key sits at the same indentation as `test:`, `services:`/`env:`/`steps:` sit at four spaces, and the `options: >-` continuation lines at ten. Compare side by side with `web-ci.yml`'s `services:` block, which this copies.

Run: `git diff --check`
Expected: no output (no whitespace errors).

- [ ] **Step 5: Commit**

```bash
git add Makefile .github/workflows/go-ci.yml
git commit -m "ci: run the CLI integration lane against a real Postgres service"
```

- [ ] **Step 6: Record the human step this cannot do**

A new job does **not** automatically become a required check. There is no ruleset file in `.github/` (it holds four workflows and nothing else) - branch protection is a GitHub repository setting. Until a human adds **`cli integration (real server)`** to the required status checks for `main`, this lane runs on every PR and shows red without blocking a merge, which is the exact failure mode the backlog item is about.

Put this in the PR description verbatim, as an action for the human:

> **Action required after merge:** add the check `cli integration (real server)` to the required status checks for `main`. Until then this lane is advisory. If that is declined, the fallback is to fold the lane into the existing `test` job as a second step and accept the shared 15-minute clock.

---

## Task 11: the CLAUDE.md routing rules

**Owner:** `relay-integration-tester`

**Files:**
- Modify: `CLAUDE.md`

A rule that lives only in a spec is a rule nobody reads. Two edits, both tight - CLAUDE.md is context-budget-sensitive and was cut twice on 2026-08-26.

- [ ] **Step 1: Add the command**

In the `## Commands` fenced block, immediately after the `make test-integration` line, insert:

```bash

# CLI real-server integration lane (internal/cli only): every test drives a live
# internal/api server over HTTP. Needs Docker, or set RELAY_TEST_DATABASE_URL to a
# running Postgres for one fresh database per test instead of one container per test.
make test-cli-integration
```

- [ ] **Step 2: Add the routing rules**

In `## Key Design Decisions`, immediately after the `**Testability overrides**` paragraph, insert:

```markdown
**Where a CLI test goes.** Ask whether the assertion's truth depends on what the SERVER puts on the wire. Yes (status codes from real handlers, response container shape, field names and types, cursor behaviour across a real page boundary, authorization outcomes) -> the integration lane, `internal/cli/*_integration_test.go`, `make test-cli-integration`. No (flag parsing, argument reordering, a refusal issued before any request, output formatting given a known input, error wording, adversarial or impossible server responses) -> the default lane with an `httptest` fixture. **And a default-lane fixture must never encode its response through the CLI's own response struct.** A fixture marshalled from `relayclient.PageEnvelope[workerResp]` agrees with the decoder by construction, on both the envelope keys and the item fields, and can never detect drift in either direction - 19 such sites remain. Hand-write the JSON, or marshal through a locally declared struct whose json tags are deliberately independent of the production type, as `writeTaskLogPage`'s `logRow` in `internal/cli/logs_test.go` does.
```

- [ ] **Step 3: Verify**

Run: `git diff CLAUDE.md`
Expected: exactly two added hunks, no reflowed neighbours. CLAUDE.md is CRLF - confirm the diff shows no whole-file line-ending churn.

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: CLI test routing rules and the new integration lane command"
```

---

## Task 12: the mutation battery (a verification task with its own acceptance)

**Owner:** `relay-integration-tester`

**Files:** none permanently. `internal/api/workers.go` and `internal/api/tasks.go` are edited and reverted within this task; the tree must be clean at the end.

**This task is not a footnote and its deliverable is a transcript.** A battery with uniform results means the harness is broken, not that coverage is good ([[reference_mutation_battery_needs_green_baseline]]). Mutations have silently failed to apply on this repo four times ([[reference_verify_the_mutation_applied]]), and a mutant that fails to apply reports "survived".

### How to apply and verify a mutation on THIS tree

**The repo is CRLF.** `sed -i`, `str.replace`-style rewrites and multi-line anchors have silently matched zero times here. Every mutation below is therefore:

- applied with a **single-line, exact-string edit** (the Edit tool, or an editor). **Never** anchor a pattern across a line break;
- verified by `git diff -U0 <file>`, which must show exactly the expected `-`/`+` lines and nothing else;
- verified by a **fixed-string grep of the mutated line**, whose hit count must be exactly **1**;
- reverted with an explicit pathspec: `git checkout -- internal/api/workers.go`. **Never `git stash`** - concurrent agents share one git index on this worktree.

**Every test command carries `-count=1`**, so a cached result can never be reported as a fresh run.

### The three lanes, each recorded separately

| Label | Command |
|---|---|
| NEW | `go test -tags integration ./internal/cli/... -run 'TestIntegration_' -count=1 -timeout 480s -v` |
| CLI-DEFAULT | `go test ./internal/cli/... -count=1 -timeout 120s` |
| API-DEFAULT | `go test ./internal/api/... -count=1 -timeout 120s` |

API-DEFAULT is recorded because `internal/api` has untagged test files (`workers_response_test.go`, `list_endpoint_projection_test.go`, `pagination_test.go` and others) that may catch M1 or M2 on their own. That is a good fact and it is **not** the claim under test. The claim is that **CLI-DEFAULT stays GREEN**, because that is the half proving the new lane is not redundant with the CLI's own fixtures - the half the backlog item's criterion omitted.

- [ ] **Step 1: Green baseline**

Run all three lanes, unmutated, on the exact command lines above. Record the pass counts.
Expected: all three GREEN. If any is red, **STOP** - a battery on a red baseline measures nothing.

- [ ] **Step 2: M0 - the control that MUST die**

In `internal/api/workers.go`, in `handleListWorkers`, change only the status argument of the final `writeJSON`:

```go
	writeJSON(w, http.StatusInternalServerError, page[workerResponse]{Items: items, NextCursor: next, Total: total})
```

(from `http.StatusOK`.) One token; no variable becomes unused, so it compiles.

Verify it applied:
```bash
git diff -U0 internal/api/workers.go
rg --fixed-strings 'writeJSON(w, http.StatusInternalServerError, page[workerResponse]{Items: items, NextCursor: next, Total: total})' internal/api/ --count
```
Expected: one `-`/`+` pair; grep count exactly **1**. **Record both.**

Run NEW.
Expected: `TestIntegration_WorkersDelete_OfflineWorker_Succeeds` (test 1, which resolves by hostname), `TestIntegration_WorkersList_RendersARealWorker` (test 5) and `TestIntegration_HarnessServesAndAuthenticates` all **FAIL**.

**If M0 does not kill those tests, STOP the battery.** The harness is not reaching the endpoint and every subsequent result is meaningless.

Revert and re-confirm:
```bash
git checkout -- internal/api/workers.go
```
Run NEW. Expected: GREEN.

- [ ] **Step 3: M1 - the container axis**

In `internal/api/workers.go`, in `handleListWorkers`, replace the final `writeJSON` line with these two lines:

```go
	_, _ = next, total
	writeJSON(w, http.StatusOK, items)
```

The `_, _ = next, total` line is **not optional**: without it, `next` (`var next string`) and `total` (`total, err := s.q.CountWorkers(ctx)`) become `declared and not used` and the package fails to compile. A mutation that does not compile is neither killed nor survived - it is no result at all.

This reproduces the exact shape of the real historical drift (`a90c727`, which moved a bare array to an envelope) on a different endpoint. `relayclient.FetchAllPages[workerResp]` decodes into `PageEnvelope`, so a bare array is a json unmarshal error and `relay workers list` fails outright.

Verify it applied:
```bash
git diff -U0 internal/api/workers.go
rg --fixed-strings 'writeJSON(w, http.StatusOK, items)' internal/api/workers.go --count
```
Expected: one `-` and two `+` lines; grep count exactly **1**. **Record both.**

Run NEW.
- Expected RED: **test 1** (`WorkersDelete_OfflineWorker_Succeeds`, which resolves by hostname and so calls the list endpoint), **test 5** (`WorkersList_RendersARealWorker`), and `HarnessServesAndAuthenticates`.
- Expected GREEN: tests 2, 3 and 4 (`ConnectedWorker_Is409`, `UnknownUUID_Is404`, `NonAdmin_Is403BeforeTheStatusGate`) - all three pass UUID-shaped targets, so `resolveWorkerIDIn`'s `looksLikeUUID` short-circuit means they never call the list endpoint.
- **Record that split by test name.** A transcript saying "the workers tests went red" would be false.

Run CLI-DEFAULT.
Expected: **GREEN**, because `workers_test.go`'s fixture still marshals `relayclient.PageEnvelope[workerResp]` itself. Record the pass count.

Run API-DEFAULT. Record GREEN or RED and, if red, which tests.

Revert and re-confirm all three lanes green:
```bash
git checkout -- internal/api/workers.go
```

- [ ] **Step 4: M2 - the field axis**

In `internal/api/tasks.go`, in `handleGetTaskLogs`'s final `writeJSON` map literal, change the key:

```go
		"nextSeq": nextSeq,
```

(from `"next_seq": nextSeq,`.) This reproduces the exact defect class that broke `relay logs`, at the exact endpoint it broke at, and it is realistic drift because the wire keys there are string literals in a `map[string]any` rather than struct tags, so a rename touches no type and compiles silently. `taskLogPage.NextSeq` then decodes as 0, the CLI concludes "drained" after page 1, and prints 200 of 201 rows.

Verify it applied:
```bash
git diff -U0 internal/api/tasks.go
rg --fixed-strings '"nextSeq": nextSeq,' internal/api/tasks.go --count
```
Expected: one `-`/`+` pair; grep count exactly **1**. **Record both.**

Run NEW.
- Expected RED: **test 9 only** (`Logs_PagesARealLogAcrossThePageBoundary`), on the line count - `require.Len(t, lines, 201)` gets 200.
- Expected GREEN: **test 10** (`Logs_ExactPageMultiple_...`), because with exactly 200 rows the CLI prints all 200 whether or not it makes the second request. Everything else GREEN.
- **Note in the transcript that the error return does NOT change under M2**: `doLogs` still returns `silentError{}` and still reports the log as drained. The line-count assertion is the only thing that kills this mutant, which is why Task 9 asserts it.

Run CLI-DEFAULT.
Expected: **GREEN**, because `writeTaskLogPage` marshals through its own inline struct with its own `json:"next_seq"` tag, deliberately independent of `taskLogPage`. Record the pass count.

Run API-DEFAULT. Record.

Revert and re-confirm all three lanes green:
```bash
git checkout -- internal/api/tasks.go
```

- [ ] **Step 5: Assert a clean tree**

Run: `git status --porcelain`
Expected: **empty**. A crashed or partially reverted mutation harness is indistinguishable from a survived mutant.

- [ ] **Step 6: Write the transcript into the PR body**

Not a committed file - this repo does not keep report files. Paste into the PR description, in this shape, one row per (mutation, lane):

```
| Mutation | Edit                                                       | grep hits | NEW lane                          | CLI-DEFAULT | API-DEFAULT |
|----------|------------------------------------------------------------|-----------|-----------------------------------|-------------|-------------|
| baseline | -                                                          | -         | GREEN (N tests)                   | GREEN (N)   | GREEN (N)   |
| M0       | workers.go StatusOK -> StatusInternalServerError            | 1         | RED: tests 1, 5, harness          | ...         | ...         |
| M0 rev   | -                                                          | -         | GREEN                             | GREEN       | GREEN       |
| M1       | workers.go envelope -> bare items (+ `_, _ = next, total`)  | 1         | RED: 1, 5 / GREEN: 2, 3, 4        | GREEN (N)   | ...         |
| M1 rev   | -                                                          | -         | GREEN                             | GREEN       | GREEN       |
| M2       | tasks.go "next_seq" -> "nextSeq"                            | 1         | RED: test 9 only (200 of 201)     | GREEN (N)   | ...         |
| M2 rev   | -                                                          | -         | GREEN                             | GREEN       | GREEN       |
| final    | `git status --porcelain`                                    | -         | empty                             | -           | -           |
```

Alongside it, paste Task 6 Step 2's recorded failure output. **That is the battery's strongest row and it is not a mutation at all**: a real live defect, caught by this lane, before any mutant was written.

---

## Task 13: final gates (conductor)

**Owner:** conductor

- [ ] **A1.** `go test -tags integration ./internal/cli/... -count=1 -timeout 480s` passes with `RELAY_TEST_DATABASE_URL` **unset** (testcontainer mode). Record the wall time.
- [ ] **A2.** The same command passes with `RELAY_TEST_DATABASE_URL` **set** to a running Postgres (shared-service mode). Record the wall time. Both modes ship, so both are measured; a mode nobody ran is a mode that does not work.
- [ ] **A3.** `go test ./internal/cli/... -count=1 -timeout 120s` passes and its runtime is within noise of the recorded 1.0s.
- [ ] **A4.** `make vet-integration` passes.
- [ ] **A5.** Read the diff: every new test passes `testCtx(t)` into its `doX` call. A test that omits it can hang the package with no name attached.
- [ ] **A6.** Task 12's transcript is in the PR body, complete, with the M0 control killed and a clean tree at the end.
- [ ] **A7.** The `cli-integration` job runs and is green on the PR.
- [x] **A8.** **Human step, not automatable from the repo.** `cli integration (real server)` is added to the required status checks for `main`. Until then the job is advisory. If declined, take Task 10 Step 6's fallback. **RESOLVED 2026-08-27 as DECLINED, and the criterion's premise was wrong.** It assumed a list of required checks existed to add to. `main` has no branch protection and no rulesets (protection API 404 "Branch not protected"; rulesets API `[]`), so no check on this repo has ever blocked a merge - `race + integration-build` included, despite CLAUDE.md having claimed otherwise for two months. The user decided to keep it that way: solo repo, checks run locally, branch protection's friction not worth buying. Task 10 Step 6's fallback (fold the lane into the `test` job) was NOT taken either, because its only benefit was inheriting a required-check status that does not exist. The job stays a separate advisory job, and the docs were corrected instead.
- [ ] **A9.** The routing rules are in `CLAUDE.md`.
- [ ] **A10.** Propose backlog items B2-B6 below; on acceptance, file them, then close the parent with `/backlog close cli-tests-never-hit-real-server` (which `git mv`s it to `docs/backlog/closed/`). Its Python half does not block the close: it is carried by B3, and the item's own Acceptance section is scoped to `internal/cli`.

**B1 is NOT proposed - it is fixed in Task 6.** Note that in the close.

### Backlog items to propose (B1 removed, B2's number corrected)

- **B2 (idea, medium).** **19** `internal/cli` fixture sites marshal through the CLI's own response structs (`relayclient.PageEnvelope[workerResp]`, `[jobResp]`, `[scheduleResp]`, `[reservationResp]`) across `workers_test.go`, `jobs_test.go`, `schedules_test.go`, `reservations_test.go`, `workers_delete_test.go`, `workers_disable_test.go`, `workers_revoke_test.go` and `workers_workspaces_test.go`, so they agree with the decoder by construction and can never detect drift in either direction. (`rg 'relayclient\.PageEnvelope\[' internal/cli --glob '*_test.go'` returns 26; the seven in `admin_output_test.go` and `admin_users_test.go` are `PageEnvelope[map[string]any]` with literal keys - simulators, not tautologies.) Repoint the 19 at hand-written literals or at locally declared structs, following `writeTaskLogPage`'s `logRow`. The integration lane bounds what that vacuity can cost; it does not remove it.
- **B3 (idea, medium).** `python/tests/integration/` has never executed in CI; `.github/workflows/python.yml` runs only `tests/unit` and `lint`. Standing that lane up by hand during the SDK sweep immediately found `Job.labels` arriving as `null`, which four reading-based review lenses had passed.
- **B4 (append to `idea-2026-08-23-integration-only-guards-ci-never-runs`).** Extract `newIntegrationDSN` into a shared package (`internal/pgtest`) that `api`, `store`, `worker`, `mcp` and `cli` all use, and price running the rest of the integration suite in CI on a shared Postgres service. **Precondition met once this slice's DSN mode is green in CI.** Two costs from the spec: a non-test package taking `*testing.T` imports `testing` into the production module, and tagging every file `//go:build integration` makes `go build ./...` fail with "build constraints exclude all Go files" unless one untagged file carries the package clause. **Third cost, found while planning this slice and not previously recorded:** `golang-migrate` keys its `pg_advisory_lock` on the DATABASE NAME, and Postgres advisory locks are cluster-global - so per-test databases with distinct generated names do not serialize, but a shared TEMPLATE database with a fixed name would put every concurrent migration on one lock.
- **B5 (idea, low).** The CLI's auth area - `login`, `register`, `passwd`, `profile`, `invites`, `agent enroll` - has no real-server coverage. `relay login` is how every user starts and its response shape is unpinned against the real handler. `doLogin` needs the `readPasswordFn` and `saveConfigFn` overrides, so it is a different setup shape.
- **B6 (append to `bug-2026-08-26-integration-lane-times-out-on-docker-teardown`).** This slice's `internal/cli/pgharness_integration_test.go` bounds both `Terminate` and `DROP DATABASE` with a 30s context and reports failure via `t.Errorf`, so a hung teardown fails one named test. The three existing copies (`internal/api/testhelper_test.go`'s `newTestPool` and `newTestQueries`, `internal/store/testhelper_test.go`, `internal/mcp/mcp_integration_test.go`'s `startRelayForMCP`) still discard the error under `context.Background()`. The shape to copy now exists.

---

## What this lane can and cannot see

Restated here because a lane described only by what it covers invites the reader to believe the surface is closed ([[feedback_sweep_count_needs_its_axis]]). Anyone reading a green `cli-integration` job needs both halves.

**Covered axes.** The HTTP contract between `internal/cli` and `internal/api`: URL path and method; authorization outcome (200 / 403 / 404 / 409); response container shape (envelope vs bare array); response field names and types; cursor semantics across a real page boundary; the CLI's rendering of a real response.

**NOT covered, by construction.** Task dispatch (`scheduler.Dispatcher` is not started). Agent log ingest and `AppendTaskLog`'s epoch/identity/recency fence (rows are inserted directly). The gRPC surface. SSE frames produced by a live worker - the lane drives only the subscribe-time snapshot path. `bootstrapAdmin` (`package main`, unimportable). The embedded SPA. Field nullability on responses whose nulls only appear under a state this lane does not create. **No job ever runs, so no test observes a `running` or `done` task.** A future reader who takes this lane as end-to-end coverage will be wrong.

**Two residual risks that outlive the slice.** CI exercises only the DSN path while local developers default to the container path, so the two modes can diverge and A1/A2 measure each exactly once (a periodic manual A1 run is the cheap mitigation). And a `cli-integration` job that is not a required check reproduces, at the workflow level, the exact defect the backlog item describes at the test level: a signal that exists and blocks nothing. That one lives outside the repo; A8 is the only fix.

---

## Self-review against the spec

Every numbered decision and acceptance item in `docs/superpowers/specs/2026-08-27-cli-real-server-integration-lane.md` maps to a task:

| Spec | Task |
|---|---|
| D1 (harness siting, two files, no consumer imports, bounded teardown) | 1, 2 |
| D2 (two-mode DSN, generated name, prefix assertion, per-test database) | 1 |
| D3 (CI job, distinct timeouts, `internal/cli` only, the advisory-check consequence) | 10 |
| D4 7.1 (harness surface, `api.New` zeros, no test-only exports, per-test deadline) | 2 |
| D4 7.2 (tests 1-10, fixture ordering) | 3, 4, 5, 7, 8, 9 |
| D4 7.3 (M0/M1/M2, verification protocol, what the lane must NOT assert) | 12, plus 5/6 for the must-not-assert |
| D5 (beside, not instead; routing rules in CLAUDE.md) | 11, plus "never touched" in File structure |
| D6 (`logs` is in, two tests, no agent needed) | 9 |
| A1-A10 | 13 |
| B1 | **Task 6 - fixed here, not filed.** Reasoning in the B1 decision section. |
| B2-B6 | 13 (proposed) |

**Type consistency check.** `newIntegrationDSN(t) string` (Task 1) is called by `startRelayServer` (Task 2). `relayServer`'s fields `BaseURL`/`Pool`/`Q`/`AdminToken`/`UserToken`/`AdminEmail`/`UserEmail` are used verbatim in Tasks 3-9. `seedWorker(t, s, name, hostname, status) string` returns the canonical uuid text used by Tasks 3 and 4. `seedLogRows(t, s, taskID, n)`, `firstTaskID(t, s, jobID) string`, `writeSpecFile(t, spec) string`, `uuidString(t, s, id) string` and `testCtx(t) context.Context` are declared once in Task 2 and used under those exact names throughout. `laneJobSpec` and `submitLaneJob(t, s) string` are declared in Task 5's file and used by Tasks 6, 7 and 9. `taskResp.Commands` (Task 6) has no reader other than the `--json`/`--pretty` re-encode.

**Placeholder scan:** no TBD, no "similar to Task N", no "add appropriate error handling". Every code step carries the code it wants written.
