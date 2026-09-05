# Per-owner scheduled_jobs cap - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound how many `scheduled_jobs` rows one owner may hold, enforced in `handleCreateScheduledJob` inside one transaction (per-owner row lock, bounded count, insert), refusing with `409` at or above `RELAY_MAX_SCHEDULES_PER_OWNER`.

**Architecture:** An admission policy in `internal/api`, not a schema constraint. Two new statements in `internal/store/query/scheduled_jobs.sql` (`LockOwnerForScheduleCap`, `CountScheduledJobsForOwnerUpTo`), a new `Server.MaxSchedulesPerOwner` field resolved through a method that folds a non-positive value to `DefaultMaxSchedulesPerOwner`, and a new `cmd/relay-server/schedulecap_config.go` carrying `parseScheduleCap` plus an unconditional startup line. Owners already over the cap are grandfathered: every route except creation keeps working.

**Tech Stack:** Go 1.26, pgx/v5, sqlc, Postgres 16, testify. Test lanes: `make test` (default), `make test-pg-integration` (CI's `pg-integration` job), `make test-integration` (`internal/api`, not run by CI today).

**Spec:** `docs/superpowers/specs/2026-09-04-per-owner-schedule-cap.md` (commit `4da8f7c`)
**Backlog item:** `docs/backlog/feature-2026-09-04-per-owner-schedule-cap.md`

---

## Slice independence declaration

**This slice has ONE lane. There is no frontend slice at all.** `web/src/schedules/api.ts` exports list, stats, run-now, get, update, delete and setEnabled and has **no create call**, so the SPA cannot reach the only enforcement point and renders nothing new. Phase 3 dispatches `relay-backend-engineer` alone; there is nothing for `relay-frontend-engineer` to do and no parallelism to buy.

**The tasks below are SEQUENTIAL, not independent.** Task 1's RED reproduction must run against HEAD before anything is written; Task 5 cannot compile until Task 4's `make generate` has emitted the two new store methods; Tasks 6-9 assert against behaviour Task 5 introduces. Task 0 is a measurement gate and blocks everything, because the whole design rests on a concurrency claim the spec did not measure.

**This is a single-PR slice.** It does NOT divide into stages and needs no `/backlog phases` run.

---

## What I refuted in the spec, and what I could NOT measure

### The Bash tool was DISABLED in the planning session

The brief asked for five things to be demonstrated against the running Postgres with real SQL and real output. **The Bash tool is disabled for this session** (`Error: No such tool available: Bash. Bash is disabled for this session, in subagents as well as here`), so **I ran nothing**. Every finding below is read-from-tree, exactly like the spec's own provenance section, and the concurrency and lock-mode claims are **still unmeasured after this plan was written**.

That is not a reason to skip them. **Task 0 makes each one a blocking implementation obligation** with the exact SQL to run and the exact output to record. Do not start Task 1 until Task 0's five results are in the PR body. `RELAY_MAX_SCHEDULES_PER_OWNER` is a control, and a control whose central mechanism was argued rather than observed is the shape this repo has been burned by before.

### Refuted: the lock-ordering argument (spec, Decision 1, "Lock ordering, so this cannot deadlock with the archive path")

The spec says the transaction takes `users` then `scheduled_jobs`, matches `handleAdminArchiveUser`, and therefore no cycle exists. **A uniqueness claim is a claim about the complement, and one transaction is not the complement.** I enumerated every transaction in the tree that touches both tables (found by searching for `Begin(`, `BeginTx(` and `BeginTxFunc` across `*.go`, then reading each):

| Transaction | `users` lock it takes | `scheduled_jobs` lock it takes | Order |
| --- | --- | --- | --- |
| `handleAdminArchiveUser` (`internal/api/users.go`) | `ArchiveUser` UPDATE -> `FOR NO KEY UPDATE` | `DisableScheduledJobsByOwner` UPDATE -> `FOR NO KEY UPDATE` | users, then scheduled_jobs |
| `schedrunner.TickOnce` (`internal/schedrunner/runner.go`) | `fireOne` -> `CreateJobFromSpec` INSERT INTO jobs, FK on `jobs.submitted_by` -> `FOR KEY SHARE` | `ListEligibleScheduledJobs` -> `FOR UPDATE SKIP LOCKED` | **scheduled_jobs, then users** |
| `handleRunScheduledJobNow` (`internal/api/scheduled_jobs.go`) | INSERT INTO jobs FK -> `FOR KEY SHARE` | same INSERT's FK on `jobs.scheduled_job_id` -> `FOR KEY SHARE` | one statement, both KEY SHARE |
| `handleCreateJob` (`internal/api/jobs.go`) | INSERT INTO jobs FK -> `FOR KEY SHARE` | none | users only |
| **the new cap transaction** | `LockOwnerForScheduleCap` -> `FOR NO KEY UPDATE` | INSERT of a **new** row. `scheduled_jobs` has no unique constraint but its PK, whose value is a fresh `gen_random_uuid()` (`internal/store/migrations/000006_scheduled_jobs.up.sql`), so it waits on **no existing row** | users only, in the wait graph |

**`schedrunner.TickOnce` is a counter-example to the spec's stated argument**: it locks in the opposite order. The conclusion (no deadlock) survives, but for a different and stronger reason, and that reason is what must go in the query comment:

1. The cap transaction never WAITS on a `scheduled_jobs` row, so it can never supply the second edge of a cycle.
2. `TickOnce`'s request on the `users` row is `FOR KEY SHARE`, which does **not** conflict with `FOR NO KEY UPDATE`, so the tick never waits on the cap transaction either.

**This is also a second, better argument for `FOR NO KEY UPDATE` than the one the spec gives.** Under `FOR UPDATE` the fleet-wide scheduler tick - which holds up to `BatchLimit` (100) `scheduled_jobs` rows locked for the whole tick - would block on one owner's schedule-create transaction. The spec named only the caller's own `POST /v1/jobs`; the tick is the larger blast radius and the spec does not mention it.

### Refined: `LIMIT sqlc.arg(ceiling)::int` is an int32 overflow waiting to happen

The spec's statement 2 writes `LIMIT sqlc.arg(ceiling)::int`, which sqlc emits as a Go `int32`. The spec ALSO says (Decision 6) that "an arbitrarily large value is accepted and is the spelling for effectively-unbounded". Those two are incompatible on a 64-bit host: `strconv.Atoi("9999999999")` succeeds, the value narrows to a negative `int32`, and Postgres errors with `LIMIT must not be negative` - a runtime 500 on a value the parser accepted and printed in the startup line.

**Use `::bigint`.** `Ceiling` is then `int64`, Go `int` widens losslessly on both 32- and 64-bit hosts, and the hazard is deleted rather than clamped. AC7 gains a row pinning that a very large value is used as-is.

### Confirmed: `ReconcileOnStartup` is unpaged and runs before the listener - FILE IT

`ListOverdueScheduledJobsForCatchup` in `internal/store/query/scheduled_jobs.sql` is `SELECT * FROM scheduled_jobs WHERE enabled AND next_run_at < NOW();` with **no LIMIT of any kind**. `cmd/relay-server/main.go` calls `schedrunner.ReconcileOnStartup` (which is its only caller) unconditionally, and `srv.ListenAndServe()` is started in a goroutine **below** that call. So the boot still materializes every overdue enabled row in one result set before the HTTP listener comes up, and the paging slice's "peak memory is one page" is true of the SWEEP and false of the BOOT. **The spec's finding holds; file the item.**

### Held, and re-read against the tree

- `POST /v1/scheduled-jobs` is bare `auth(...)` in `internal/api/server.go`'s route table; its two siblings on that surface are `auth(userLimit(...))`.
- `handleCreateScheduledJob` sets `OwnerID: u.ID` unconditionally, so owner and caller are the same principal at the only enforcement point.
- `idx_scheduled_jobs_owner ON scheduled_jobs(owner_id)` exists and serves the count predicate. No new index.
- `handleRunScheduledJobNow` inserts into `jobs` and `tasks` and never into `scheduled_jobs`, so it is neither an enforcement point nor an evasion.
- `cmd/relay-server` really is in a CI lane. **Both edits CLAUDE.md requires are already present**: the `Makefile`'s `test-pg-integration` target runs `go test -tags integration -count=1 ./internal/store/... ./internal/schedrunner/... ./internal/testsupport/... ./cmd/relay-server/... -timeout 600s`, and `.github/workflows/go-ci.yml`'s `pg-integration` job runs `make test-pg-integration` against a `services: postgres` with `RELAY_TEST_DATABASE_URL` set. `internal/api`'s integration lane is run by no CI job today.

---

## Critical files

Read these before touching anything. Cited by SYMBOL, never by line - line citations rot.

| File | Why it is critical |
| --- | --- |
| `internal/api/scheduled_jobs.go` | `handleCreateScheduledJob` is the ONLY enforcement point. Its validation order (readJSON, required fields, overlap_policy, Unmarshal, `ValidateJobSpec`, `ValidateMinInterval`, `ParseSchedule`) all runs before the transaction. `handleRunScheduledJobNow` is the transaction shape to copy. |
| `internal/api/users.go` | `handleAdminArchiveUser` is the other transaction that touches both tables, and the one the lock-ordering comment must name. |
| `internal/schedrunner/runner.go` | `TickOnce` locks the two tables in the OPPOSITE order. Its `ListEligibleScheduledJobs`+`fireOne` pair is the counter-example the query comment must survive. |
| `internal/schedrunner/startup_validation.go` | `ValidateStoredSpecsOnStartup`'s header currently says the cap WOULD bound the starting work set and cites the backlog item by filename. That conditional becomes false. |
| `internal/store/query/scheduled_jobs.sql` | Where the two new statements go. Never edit `internal/store/scheduled_jobs.sql.go`. |
| `cmd/relay-server/http_server.go` | `httpServerDeps` and `buildHTTPServer`. Its header records exactly which wiring mistakes are and are not guarded; the new pair must be added to that ledger truthfully. |
| `cmd/relay-server/main.go` | The single `buildHTTPServer(httpServerDeps{...})` literal. |
| `cmd/relay-server/autoenroll_config.go` | `parseAutoEnrollCeiling` and `autoEnrollCeilingLine` are the shape `parseScheduleCap` and `scheduleCapLine` copy, with the `0` arm deliberately changed. |
| `cmd/relay-server/password_ratelimit_wiring_test.go` | Declares `mainBodyOfPackage` and `identNamed` in package `main`, default lane. **Reuse `mainBodyOfPackage`; do not paste a second copy.** |
| `cmd/relay-server/counters_wiring_test.go` | Declares `stubAdminDB`, `countersStubUUID` and `countersAsAdmin`. The new integration test does NOT use `stubAdminDB` (it needs real users), but must not collide with those names. |

## Files created or modified

| File | Change |
| --- | --- |
| `internal/store/query/scheduled_jobs.sql` | ADD `LockOwnerForScheduleCap` and `CountScheduledJobsForOwnerUpTo` with their comments |
| `internal/store/scheduled_jobs.sql.go` | REGENERATED by `make generate`. Never hand-edited. |
| `internal/api/server.go` | ADD `DefaultMaxSchedulesPerOwner`, `Server.MaxSchedulesPerOwner`, `(*Server).maxSchedulesPerOwner()` |
| `internal/api/scheduled_jobs.go` | `handleCreateScheduledJob` grows a transaction after validation |
| `cmd/relay-server/schedulecap_config.go` | NEW: `parseScheduleCap`, `scheduleCapLine` |
| `cmd/relay-server/http_server.go` | ADD `maxSchedulesPerOwner` field; ADD `s.MaxSchedulesPerOwner = d.maxSchedulesPerOwner`; extend the header's guarded/unguarded ledger |
| `cmd/relay-server/main.go` | Parse, warn, print the line, pass the field |
| `cmd/relay-server/schedulecap_config_test.go` | NEW, default lane: AC7, AC8 |
| `cmd/relay-server/schedulecap_wiring_integration_test.go` | NEW, integration lane (`pg-integration` in CI): AC1, AC2, AC4 |
| `internal/store/scheduled_jobs_cap_integration_test.go` | NEW, integration lane (`pg-integration` in CI): AC3 + its control, AC6 |
| `internal/api/scheduled_jobs_cap_integration_test.go` | NEW, integration lane (NOT in CI today): AC5 |
| `README.md` | New env row; the submit row's added clause; the 409 in the API reference |
| `internal/schedrunner/startup_validation.go` | The header's conditional sentence becomes a statement; the citation re-points |

## Which lane runs which assertion, and what moves if the sibling lands

| Test | Lane | Run by CI? |
| --- | --- | --- |
| `TestParseScheduleCap`, `TestScheduleCapLine*`, `TestMain_PassesTheScheduleCapItParsed` | `cmd/relay-server`, default | YES - `go test -race ./...` in the `test` job |
| `TestScheduleCap_*` (AC1, AC2, AC4) | `cmd/relay-server`, integration | YES - `pg-integration` job runs `make test-pg-integration` |
| `TestScheduleCapLock_*`, `TestCreateScheduledJob_TheStoreDoesNotEnforceTheCap` (AC3, AC6) | `internal/store`, integration | YES - same job |
| `TestScheduledJobCap_AnOverCapOwnerKeepsEveryRouteButCreate` (AC5) | `internal/api`, integration | **NO** |

**The headline guards - the ones that say the cap exists at all and uses the operator's number - are in `cmd/relay-server`'s integration lane, which CI already runs.** That is what makes this slice independent of the in-flight `internal/api` CI job.

**What would move if the `internal/api` lane lands:** only AC5 (`internal/api/scheduled_jobs_cap_integration_test.go`), which moves nowhere - it starts running in CI unchanged, in place. Nothing needs rewriting. Its PATCH and run-now arms are the ones that most want CI, because they are the arms a later "consistency" edit would break. **Do not move AC1/AC2/AC4 into `internal/api` when that lands.** They exercise `buildHTTPServer`'s forwarding, which only `cmd/relay-server` can see.

---

## Task 0: Measure what the spec argued (BLOCKING - no code until these five results exist)

**Files:** none. Output goes in the PR body and, for the two permanent ones, into Task 8's tests.

Use a database you create and drop yourself on the running `relay-postgres` container. **Do NOT use the shared `relay` database and do NOT stop, restart or "restore" the container** - sibling lanes depend on it.

- [ ] **Step 1: Create a throwaway database with a minimal replica of the three tables**

```bash
docker exec -i relay-postgres psql -U relay -d postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE lane_d_capdemo;"
docker exec -i relay-postgres psql -U relay -d lane_d_capdemo -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE users (id uuid PRIMARY KEY DEFAULT gen_random_uuid());
CREATE TABLE jobs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  submitted_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT
);
CREATE TABLE scheduled_jobs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_scheduled_jobs_owner ON scheduled_jobs(owner_id);
INSERT INTO users (id) VALUES ('11111111-1111-1111-1111-111111111111');
INSERT INTO scheduled_jobs (owner_id) VALUES ('11111111-1111-1111-1111-111111111111');
SQL
```

The `jobs.submitted_by ... ON DELETE RESTRICT` FK is copied from `internal/store/migrations/000001_initial.up.sql`; it is what makes step 4's `FOR KEY SHARE` real rather than assumed.

- [ ] **Step 2: MEASUREMENT 1 - the single conditional INSERT does NOT serialise**

This is the spec's central refutation of the backlog item. Open two interactive sessions:

```bash
# terminal A
docker exec -it relay-postgres psql -U relay -d lane_d_capdemo
# terminal B
docker exec -it relay-postgres psql -U relay -d lane_d_capdemo
```

A:
```sql
BEGIN;
INSERT INTO scheduled_jobs (owner_id)
SELECT '11111111-1111-1111-1111-111111111111'::uuid
 WHERE (SELECT COUNT(*) FROM scheduled_jobs
         WHERE owner_id = '11111111-1111-1111-1111-111111111111'::uuid) < 2;
```
B, while A is still open:
```sql
BEGIN;
INSERT INTO scheduled_jobs (owner_id)
SELECT '11111111-1111-1111-1111-111111111111'::uuid
 WHERE (SELECT COUNT(*) FROM scheduled_jobs
         WHERE owner_id = '11111111-1111-1111-1111-111111111111'::uuid) < 2;
```
Then `COMMIT;` in A, `COMMIT;` in B, then:
```sql
SELECT count(*) FROM scheduled_jobs WHERE owner_id = '11111111-1111-1111-1111-111111111111';
```

Expected: both report `INSERT 0 1`, B does **not** block, and the final count is `3` over a cap of `2`. **Record the actual output verbatim.** If B blocks or reports `INSERT 0 0`, the spec's refutation of the item is wrong and the whole design must be re-argued before any code is written - STOP and report.

- [ ] **Step 3: MEASUREMENT 2 - the earlier-statement lock DOES serialise**

```sql
TRUNCATE scheduled_jobs;
INSERT INTO scheduled_jobs (owner_id) VALUES ('11111111-1111-1111-1111-111111111111');
```
A:
```sql
BEGIN;
SELECT id FROM users WHERE id = '11111111-1111-1111-1111-111111111111' FOR NO KEY UPDATE;
SELECT COUNT(*) FROM (
  SELECT 1 FROM scheduled_jobs
   WHERE owner_id = '11111111-1111-1111-1111-111111111111'::uuid
   LIMIT 2) t;
INSERT INTO scheduled_jobs (owner_id) VALUES ('11111111-1111-1111-1111-111111111111');
```
B, while A is open:
```sql
BEGIN;
SELECT id FROM users WHERE id = '11111111-1111-1111-1111-111111111111' FOR NO KEY UPDATE;
```
Expected: A's count is `1`; **B blocks here**. Confirm from a third session:
```sql
SELECT pid, state, wait_event_type, wait_event, left(query, 60)
  FROM pg_stat_activity WHERE datname = 'lane_d_capdemo';
```
Then `COMMIT;` in A. B's SELECT returns; run B's count:
```sql
SELECT COUNT(*) FROM (
  SELECT 1 FROM scheduled_jobs
   WHERE owner_id = '11111111-1111-1111-1111-111111111111'::uuid
   LIMIT 2) t;
ROLLBACK;
```
Expected: B's count is `2`, so B refuses. **Record both counts and the `wait_event_type = 'Lock'` row.** If B does not block, the design does not work - STOP and report.

- [ ] **Step 4: MEASUREMENT 3 - `FOR NO KEY UPDATE` vs `FOR UPDATE` against the FK's `FOR KEY SHARE`**

This is the pair that is easy to state backwards. Run both directions.

Direction 1 (the chosen mode, must NOT block):
```sql
-- A
BEGIN; SELECT id FROM users WHERE id = '11111111-1111-1111-1111-111111111111' FOR NO KEY UPDATE;
-- B, while A is open
BEGIN; INSERT INTO jobs (submitted_by) VALUES ('11111111-1111-1111-1111-111111111111');
-- expected: B returns INSERT 0 1 immediately
ROLLBACK;  -- in both
```
Direction 2 (the rejected mode, must block):
```sql
-- A
BEGIN; SELECT id FROM users WHERE id = '11111111-1111-1111-1111-111111111111' FOR UPDATE;
-- B, while A is open
BEGIN; INSERT INTO jobs (submitted_by) VALUES ('11111111-1111-1111-1111-111111111111');
-- expected: B BLOCKS. Confirm with the pg_stat_activity query from step 3.
```
Then `COMMIT;` in A and watch B return. **Record both.** If direction 1 blocks or direction 2 does not, the spec's `FOR NO KEY UPDATE` reason is inverted and `internal/store/query/scheduled_jobs.sql`'s comment must be rewritten from the observation rather than from the spec.

- [ ] **Step 5: MEASUREMENT 4 - the inner LIMIT actually bounds the plan**

```sql
TRUNCATE scheduled_jobs;
INSERT INTO scheduled_jobs (owner_id)
SELECT '11111111-1111-1111-1111-111111111111' FROM generate_series(1, 10000);
ANALYZE scheduled_jobs;

EXPLAIN (ANALYZE, BUFFERS)
SELECT COUNT(*) FROM (
  SELECT 1 FROM scheduled_jobs
   WHERE owner_id = '11111111-1111-1111-1111-111111111111'::uuid
   LIMIT 100) t;

EXPLAIN (ANALYZE, BUFFERS)
SELECT COUNT(*) FROM scheduled_jobs
 WHERE owner_id = '11111111-1111-1111-1111-111111111111'::uuid;
```
Expected: the bounded form shows a `Limit` node with `actual rows=100` above an index scan on `idx_scheduled_jobs_owner`, and a buffer count that does not grow with the table; the plain `COUNT(*)` reads all 10 000. **Record both plans, including the `Buffers:` lines**, and re-run the bounded form at 1 000 rows to show the buffer count is flat. Do not assume the planner cooperates - if the `Limit` is not pushed below the aggregate, say so and the statement's comment must state the real cost.

- [ ] **Step 6: Drop the throwaway database**

```bash
docker exec -i relay-postgres psql -U relay -d postgres -c "DROP DATABASE lane_d_capdemo;"
docker ps --filter name=relay-postgres --format "{{.Names}} {{.Status}}"
```

The second command is not optional: it proves the container is still running for the sibling lanes.

- [ ] **Step 7: Write the five results into the PR body under a "Measured" heading**

Each with its input, not just its number. "B blocked" without the SQL that produced it reads as the typical case.

---

## Task 1: The RED reproduction - three creates, three 201s at HEAD

**Files:**
- Create: `cmd/relay-server/schedulecap_wiring_integration_test.go`

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// createAPIToken mints a real api_tokens row and returns the raw hex the client
// presents. Real tokens, not stubAdminDB: the cap's subject is a per-OWNER count,
// so every request in this file has to carry a distinct, real users row.
func createAPIToken(t *testing.T, q *store.Queries, userID pgtype.UUID) string {
	t.Helper()
	raw := make([]byte, 16)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	rawHex := hex.EncodeToString(raw)
	_, err = q.CreateToken(t.Context(), store.CreateTokenParams{
		UserID:    userID,
		TokenHash: tokenhash.Hash(rawHex),
		ExpiresAt: pgtype.Timestamptz{},
	})
	require.NoError(t, err)
	return rawHex
}

// postSchedule drives one POST /v1/scheduled-jobs through the REAL http.Server
// buildHTTPServer returned, against a real Postgres.
func postSchedule(t *testing.T, srv *http.Server, token, name string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(
		`{"name":%q,"cron_expr":"@hourly","job_spec":{"name":"j","tasks":[{"name":"t","command":["echo","x"]}]}}`,
		name)
	req := httptest.NewRequest("POST", "/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// TestScheduleCap_TheThirdCreateIsRefusedAtACapOfTwo is AC1 and AC2 in one
// fixture.
//
// THE THIRD REQUEST IS NOT OPTIONAL. Two successes under a cap of two are also
// exactly what an ABSENT control produces, so without the refusal this test is
// vacuous against the implementation it describes. That defect shipped in this
// batch once already and was fixed in review.
//
// EVERY OTHER LIMIT FIELD IS LEFT AT ZERO, and that is load-bearing rather than
// incidental: a crossed assignment in buildHTTPServer
// (s.MaxSchedulesPerOwner = d.jobSubmitLimitN) then produces a zero, which the
// resolver folds to DefaultMaxSchedulesPerOwner (100), so the third create
// SUCCEEDS and this test is RED for the right reason. A plausible non-zero
// number in a sibling field would let a crossed assignment pass.
func TestScheduleCap_TheThirdCreateIsRefusedAtACapOfTwo(t *testing.T) {
	pool, q := newPgdsnPoolAndQueries(t)
	user := createUserWithTestPassword(t, q, "Capped", "cap-third@example.com", false)
	token := createAPIToken(t, q, user.ID)

	srv := buildHTTPServer(httpServerDeps{
		addr:                 "127.0.0.1:0",
		pool:                 pool,
		q:                    q,
		maxSchedulesPerOwner: 2,
	})

	for i := 1; i <= 2; i++ {
		rec := postSchedule(t, srv, token, fmt.Sprintf("under-the-cap-%d", i))
		require.Equal(t, http.StatusCreated, rec.Code,
			"create %d of 2 must be admitted under a cap of 2. body: %s", i, rec.Body.String())
	}

	over := postSchedule(t, srv, token, "over-the-cap")
	require.Equal(t, http.StatusConflict, over.Code,
		"the third create must be refused with 409 by the cap buildHTTPServer was GIVEN. A missing "+
			"check, `n > cap` instead of `n >= cap`, a check placed after the insert, a hard-coded "+
			"default, and a deleted or crossed s.MaxSchedulesPerOwner assignment all answer 201 here. "+
			"body: %s", over.Body.String())
	require.Contains(t, over.Body.String(), "Delete a scheduled job before creating another",
		"the refusal must carry the self-service remedy, which is the only remedy an actor who can "+
			"drive this refusal should be told about")

	// AND NOTHING WAS WRITTEN. The refusal must roll back, not leave a row that
	// the count will then charge the owner for.
	var n int64
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM scheduled_jobs WHERE owner_id = $1`, user.ID).Scan(&n))
	require.Equal(t, int64(2), n, "a refused create must write nothing")
}
```

- [ ] **Step 2: Run it against HEAD to see it fail**

```bash
go test -tags integration -count=1 ./cmd/relay-server/... -run TestScheduleCap_TheThirdCreateIsRefusedAtACapOfTwo -v -timeout 300s
```

Expected: **compile failure** - `unknown field maxSchedulesPerOwner in struct literal of type httpServerDeps`. That is the honest RED for a field that does not exist. To get the BEHAVIOURAL reproduction the brief asks for, temporarily delete the `maxSchedulesPerOwner: 2,` line and re-run: expected **three 201s**, with the failure at `the third create must be refused with 409` and `Actual: 201`. Record that output; it is the behavioural reproduction. Then restore the line.

- [ ] **Step 3: Commit the RED**

```bash
git add cmd/relay-server/schedulecap_wiring_integration_test.go
git commit -m "test: RED - POST /v1/scheduled-jobs admits an unbounded number of schedules per owner"
```

---

## Task 2: `parseScheduleCap` and the startup line

**Files:**
- Create: `cmd/relay-server/schedulecap_config.go`
- Create: `cmd/relay-server/schedulecap_config_test.go`

- [ ] **Step 1: Write the failing test (AC7)**

`cmd/relay-server/schedulecap_config_test.go`:

```go
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"relay/internal/api"
)

// TestParseScheduleCap pins the three-outcome contract as BEHAVIOUR, because the
// claim "there is no environment value that disables this check" is exactly the
// kind of sentence a sibling slice had to delete from three places after writing
// it from memory. Whatever README says about what the parser refuses must be
// phrased as what this table pins.
//
// THE ZERO ROW IS FIRST. A poisoned input placed after its target is read by
// neither the code nor the mutant: with `0` last, a mutant that returns early on
// the first row never reaches it.
func TestParseScheduleCap(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int
		msgPart string
	}{
		{"zero is NOT an off switch: it folds to the default and warns", "0", api.DefaultMaxSchedulesPerOwner, "is not a positive integer"},
		{"negative folds to the default and warns", "-1", api.DefaultMaxSchedulesPerOwner, "is not a positive integer"},
		{"unparseable folds to the default and warns", "abc", api.DefaultMaxSchedulesPerOwner, "is not a positive integer"},
		{"unset uses the default and says nothing", "", api.DefaultMaxSchedulesPerOwner, ""},
		{"a positive value is used as-is, silently", "7", 7, ""},
		{"a very large value is used as-is: that is the spelling for effectively-unbounded", "9999999999", 9999999999, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseScheduleCap("RELAY_MAX_SCHEDULES_PER_OWNER", tc.raw)
			require.Equal(t, tc.want, got)
			if tc.msgPart == "" {
				assert.Empty(t, msg)
				return
			}
			assert.Contains(t, msg, tc.msgPart)
			assert.Contains(t, msg, tc.raw,
				"a warning that does not name the ignored value leaves an operator believing they "+
					"tightened a bound they did not")
		})
	}
}

// TestScheduleCapLineIsUnconditionalAndNamesGrandfathering. An operator
// upgrading into a new refusal needs to see the number and the retroactivity
// without reading release notes.
func TestScheduleCapLineIsUnconditionalAndNamesGrandfathering(t *testing.T) {
	line := scheduleCapLine(100)
	assert.Contains(t, line, "100")
	assert.Contains(t, line, "keep",
		"the line must say existing owners over the cap KEEP their schedules; an operator who reads "+
			"this as a deletion has no way to find out otherwise before the deploy")
	assert.Contains(t, line, "per owner",
		"the line must not let an operator read the number as a fleet ceiling")
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./cmd/relay-server/... -run 'TestParseScheduleCap|TestScheduleCapLine' -v -timeout 60s
```

Expected: `undefined: parseScheduleCap`, `undefined: scheduleCapLine`, `undefined: api.DefaultMaxSchedulesPerOwner`.

- [ ] **Step 3: Write the constant, then the parser**

First add to `internal/api/server.go`, immediately above the `Server` struct's field block that already holds `SearchLimitN` and friends (a `const` at file scope, beside the struct):

```go
// DefaultMaxSchedulesPerOwner bounds how many scheduled_jobs rows one owner may
// hold before POST /v1/scheduled-jobs refuses to create another.
//
// WHAT IT MUST NOT REFUSE: a studio maintaining one schedule per project per
// cadence - a nightly build, a weekly cleanup, a per-show render - which is tens
// at the outside. WHAT IT DELIBERATELY REFUSES: a pipeline service account
// minting one schedule per shot or per asset. The remedy for that shape is one
// schedule whose job_spec fans out into tasks, which is the model relay is built
// around; raising the number is the other, and it stays visible in the
// environment.
//
// The cap counts ALL of an owner's rows, enabled or not, so PATCH cannot
// increase the count and creation stays the only enforcement point.
const DefaultMaxSchedulesPerOwner = 100
```

Then `cmd/relay-server/schedulecap_config.go`:

```go
package main

import (
	"fmt"
	"strconv"

	"relay/internal/api"
)

// parseScheduleCap resolves RELAY_MAX_SCHEDULES_PER_OWNER. Same three-outcome
// shape as parseAutoEnrollCeiling, parseConnLimit and parseWatchdogDuration, and
// deliberately not a log.Fatalf: a typo must not stop a farm booting when a safe
// default exists. The rate-limit family fatals; this is not in that family.
//
//   - Unset, or a valid integer >= 1: used as-is, silently.
//   - 0, negative or unparseable: the default is used and the message names the
//     ignored value.
//
// THE ZERO ARM IS WHERE THIS DIVERGES FROM parseAutoEnrollCeiling, and the
// divergence has a reason. That ceiling gates a path with a non-refused
// alternative - enrollment tokens are never refused by it - so an operator on a
// trusted network can legitimately turn it off. This gates the ONLY route that
// creates a scheduled job. There is no off token and no value that disables the
// check; a very large number is the spelling for effectively-unbounded, which
// differs from an off switch in the way that matters, because it stays visible
// as a number in the environment and in the startup line.
func parseScheduleCap(name, raw string) (int, string) {
	if raw == "" {
		return api.DefaultMaxSchedulesPerOwner, ""
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return api.DefaultMaxSchedulesPerOwner, fmt.Sprintf(
			"%s=%q is not a positive integer; using %d", name, raw, api.DefaultMaxSchedulesPerOwner)
	}
	return n, ""
}

// scheduleCapLine renders the unconditional startup line, in the shape of
// autoEnrollCeilingLine and watchdogBoundsLine.
//
// IT NAMES GRANDFATHERING because that is the half an operator cannot discover
// from the number: the cap does not shrink an existing table by one row, so on
// the deploy that lands it the boot sweep's work set is exactly what it was the
// day before.
func scheduleCapLine(n int) string {
	return fmt.Sprintf(
		"scheduled jobs: refusing creation at %d schedules per owner. Owners already over it keep "+
			"every schedule and are refused only new ones. The bound is per owner and not a fleet "+
			"ceiling, so M accounts hold M x %d.", n, n)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./cmd/relay-server/... -run 'TestParseScheduleCap|TestScheduleCapLine' -v -timeout 60s
go test ./internal/api/... -count=1 -timeout 120s
```

Expected: PASS on both.

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go cmd/relay-server/schedulecap_config.go cmd/relay-server/schedulecap_config_test.go
git commit -m "feat: parse RELAY_MAX_SCHEDULES_PER_OWNER and print its startup line"
```

---

## Task 3: `Server.MaxSchedulesPerOwner` and its resolver

**Files:**
- Modify: `internal/api/server.go`
- Create: `internal/api/schedule_cap_test.go`

- [ ] **Step 1: Write the failing test**

`internal/api/schedule_cap_test.go` (default lane, no build tag - this is a pure function over a struct field and needs no Postgres, which is the cheapest rung CLAUDE.md's guard ladder offers):

```go
package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMaxSchedulesPerOwner_NonPositiveFoldsToTheDefault pins the failure
// DIRECTION, which is the whole reason the field is resolved through a method
// instead of read raw.
//
// A deleted or crossed wiring assignment in cmd/relay-server's buildHTTPServer
// leaves this field at zero. Read raw, zero means "refuse everything" - a
// control that fails from a wiring slip into a total outage. Folded, it means
// "the operator's number was ignored", which is the same direction
// Handler.autoEnrollWorkerCeiling's neighbours take and the same reason its
// comment gives: a direct-construction caller fails BOUNDED rather than
// refusing everything.
//
// It also means every &Server{} and api.New(...) in the test lanes gets the
// default with no edit.
func TestMaxSchedulesPerOwner_NonPositiveFoldsToTheDefault(t *testing.T) {
	for _, n := range []int{0, -1} {
		s := &Server{MaxSchedulesPerOwner: n}
		require.Equal(t, DefaultMaxSchedulesPerOwner, s.maxSchedulesPerOwner(),
			"%d must fold to the default: a zeroed wiring field must degrade to 'the operator's "+
				"number was ignored', never to 'every create is refused'", n)
	}
	require.Equal(t, DefaultMaxSchedulesPerOwner, (&Server{}).maxSchedulesPerOwner(),
		"an unset field is the state every test-lane api.New call is in")
	require.Equal(t, 7, (&Server{MaxSchedulesPerOwner: 7}).maxSchedulesPerOwner(),
		"a positive value is used as-is; folding it would make the environment variable dead")
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/api/... -run TestMaxSchedulesPerOwner -v -timeout 60s
```

Expected: `s.maxSchedulesPerOwner undefined (type *Server has no field or method maxSchedulesPerOwner)`.

- [ ] **Step 3: Add the field and the resolver**

In `internal/api/server.go`, add the field immediately after the `PasswordChangeLimitN`/`PasswordChangeLimitWin` pair:

```go
	// MaxSchedulesPerOwner bounds how many scheduled_jobs rows one owner may
	// hold. Set by cmd/relay-server's buildHTTPServer from
	// RELAY_MAX_SCHEDULES_PER_OWNER. A NAMED FIELD, never another positional
	// argument to New, whose tail is already four same-typed arguments in a row
	// with a measured green transpose across them.
	//
	// Read it through maxSchedulesPerOwner(), never directly: a non-positive
	// value folds to DefaultMaxSchedulesPerOwner, so a deleted or crossed wiring
	// assignment degrades to "the operator's number was ignored" instead of
	// "every create is refused".
	MaxSchedulesPerOwner int
```

And the resolver, beside the field's accessors near the bottom of the file (above `writeJSON`):

```go
// maxSchedulesPerOwner resolves the effective per-owner schedule cap. Unlike
// Handler.autoEnrollWorkerCeiling, zero is NOT a real answer here and does not
// disable the cap: there is no route by which an operator can turn this control
// off, and parseScheduleCap folds 0 to the default before it ever arrives.
func (s *Server) maxSchedulesPerOwner() int {
	if s.MaxSchedulesPerOwner < 1 {
		return DefaultMaxSchedulesPerOwner
	}
	return s.MaxSchedulesPerOwner
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./internal/api/... -run TestMaxSchedulesPerOwner -v -timeout 60s
go test ./internal/api/... -count=1 -timeout 120s
```

Expected: PASS. The second run is the guard against an unused-method vet failure and any name collision.

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/api/schedule_cap_test.go
git commit -m "feat: Server.MaxSchedulesPerOwner, folding a non-positive value to the default"
```

---

## Task 4: The two store statements, and `make generate`

**Files:**
- Modify: `internal/store/query/scheduled_jobs.sql`
- Regenerate: `internal/store/scheduled_jobs.sql.go`

**Never edit `*.sql.go` or `models.go` directly.**

- [ ] **Step 1: Add both statements to `internal/store/query/scheduled_jobs.sql`**

Put them immediately after `CountScheduledJobsByOwner`, so the cap's count sits next to the list's count and a reader comparing them finds both.

```sql
-- name: LockOwnerForScheduleCap :one
-- The per-owner schedule cap's FIRST statement. Its only job is the lock; the
-- returned id is never used.
--
-- IT MUST BE ITS OWN STATEMENT, BEFORE THE COUNT. Under READ COMMITTED a
-- statement's snapshot is taken when the statement STARTS. A lock acquired
-- part-way through the counting statement is granted after the competitor
-- commits, but the count has already been evaluated against the older snapshot -
-- so merging the two re-opens the exact race the lock exists to close, and two
-- requests at cap-1 both pass. TestScheduleCapLock_WithoutTheLockBothTransactions-
-- Insert is the control that shows the race is real.
--
-- FOR NO KEY UPDATE, NOT FOR UPDATE, and there are two reasons. FOR UPDATE
-- conflicts with FOR KEY SHARE, which is what any insert of a row referencing
-- users(id) takes: it would block this same caller's concurrent POST /v1/jobs,
-- and - the larger blast radius - it would block schedrunner.TickOnce, whose
-- INSERT INTO jobs takes FOR KEY SHARE on the owner while the tick holds up to
-- BatchLimit scheduled_jobs rows locked. FOR NO KEY UPDATE conflicts with
-- itself, which is all this needs.
--
-- LOCK ORDERING: A TRANSACTION TOUCHING BOTH TABLES TAKES users FIRST.
-- handleAdminArchiveUser already does (ArchiveUser, then
-- DisableScheduledJobsByOwner). schedrunner.TickOnce takes them in the OPPOSITE
-- order (ListEligibleScheduledJobs FOR UPDATE, then users FOR KEY SHARE through
-- the FK), and there is still no cycle for two reasons that must both hold: this
-- transaction's INSERT creates a NEW row and waits on no existing scheduled_jobs
-- row, and the tick's FOR KEY SHARE does not conflict with FOR NO KEY UPDATE.
-- Both would be false under FOR UPDATE.
--
-- pgx.ErrNoRows is unreachable today - users are archived, never deleted, and
-- the FK on scheduled_jobs.owner_id guarantees the row exists - and the caller
-- handles it anyway, so a future hard delete fails CLOSED instead of skipping
-- the count.
SELECT id FROM users WHERE id = sqlc.arg(owner_id)::uuid FOR NO KEY UPDATE;

-- name: CountScheduledJobsForOwnerUpTo :one
-- The per-owner schedule cap's SECOND statement.
--
-- THE INNER LIMIT IS NOT AN OPTIMIZATION. Owners over the cap are grandfathered
-- and this route is in no rate-limit bucket, so a plain COUNT(*) would make
-- every REFUSED create cost a scan proportional to how many rows the owner
-- already holds - handing the actor who is already over the cap an amplification
-- primitive that grows with the damage they have done. The LIMIT makes the check
-- cost O(ceiling) whatever the owner holds, and it answers exactly the question
-- asked: "is the count at least ceiling".
--
-- THE RESULT SATURATES AT ceiling AND IS NEVER A CENSUS. Nothing may serve it,
-- log it as a total, or feed it into handleScheduledJobStats, which has its own
-- real count in ScheduledJobCounts. The refusal message therefore says "at the
-- limit" and never "you own N".
--
-- ::bigint, NOT ::int. sqlc emits ::int as an int32, and a cap large enough to
-- mean "effectively unbounded" - which is the only spelling this control has for
-- that - narrows to a NEGATIVE int32 and makes Postgres reject the LIMIT at
-- runtime, on a value the parser accepted and the startup line printed.
--
-- idx_scheduled_jobs_owner serves this predicate; no new index.
SELECT COUNT(*) FROM (
  SELECT 1 FROM scheduled_jobs
   WHERE owner_id = sqlc.arg(owner_id)::uuid
   LIMIT sqlc.arg(ceiling)::bigint
) t;
```

**Why a new statement rather than reusing `CountScheduledJobsByOwner`:** that one carries the list's two optional filters and its `LEFT JOIN users`, and calling it with both filters NULL happens to return the same number today. Reusing it would tie the cap's meaning to the list's filter vocabulary, and `ListScheduledJobsPage`'s own comment records the failure shape - a call site that omits a filter field disables that filter silently, with no error.

- [ ] **Step 2: Regenerate**

```bash
make generate
```

- [ ] **Step 3: Deal with the CRLF rewrite - this is a required step, not cleanup**

sqlc emits LF and this is a CRLF repo, so it rewrites line endings across **every** generated file. `git diff` and `git status` disagree by design here (`core.autocrlf=true` makes `git diff` normalize LF churn away while `git status` still lists the files as modified), so **never conclude "nothing to revert" from `git diff` alone**.

```bash
git status --short
git diff --ignore-all-space --stat
```

Keep only the real content change - `internal/store/scheduled_jobs.sql.go` - and revert every file whose only change is line endings:

```bash
git checkout -- <each LF-only file>
```

- [ ] **Step 4: VERIFY the regenerated file survived the revert**

The revert is exactly how a regenerated `.sql.go` gets silently discarded. Assert the new symbols are present, in the file, after the revert:

```bash
grep -c "func (q \*Queries) LockOwnerForScheduleCap" internal/store/scheduled_jobs.sql.go
grep -c "func (q \*Queries) CountScheduledJobsForOwnerUpTo" internal/store/scheduled_jobs.sql.go
git ls-files --eol internal/store/scheduled_jobs.sql.go internal/store/query/scheduled_jobs.sql
git diff --stat
```

Expected: `1` from each `grep`; `i/lf` from `git ls-files --eol` on both paths; and a `git diff --stat` whose line count is proportionate to two statements (roughly 40-60 added lines in `.sql.go`), not hundreds. A diffstat far larger than the change you intended means the CRLF rewrite is still in the index.

- [ ] **Step 5: Verify the emitted types are what Task 5 expects**

```bash
grep -A6 "type CountScheduledJobsForOwnerUpToParams" internal/store/scheduled_jobs.sql.go
```

Expected: `OwnerID pgtype.UUID` and `Ceiling int64`. If sqlc emitted something else, Task 5's call site must be written against what is actually in the file, not against this plan.

```bash
go build ./... && go vet ./...
```

- [ ] **Step 6: Commit**

```bash
git add internal/store/query/scheduled_jobs.sql internal/store/scheduled_jobs.sql.go
git commit -m "feat(store): LockOwnerForScheduleCap and CountScheduledJobsForOwnerUpTo"
```

---

## Task 5: The transaction in `handleCreateScheduledJob`

**Files:**
- Modify: `internal/api/scheduled_jobs.go` (`handleCreateScheduledJob`)

- [ ] **Step 1: Replace the bare `s.q.CreateScheduledJob` call with the transaction**

Find the block that currently reads:

```go
	row, err := s.q.CreateScheduledJob(r.Context(), store.CreateScheduledJobParams{
```

Replace everything from that line down to (and including) the `if err != nil { writeError(...500...); return }` that follows it with:

```go
	// THE TRANSACTION OPENS HERE, AFTER ALL BODY VALIDATION. readJSON, the
	// required-field checks, the overlap_policy check, json.Unmarshal,
	// ValidateJobSpec, ValidateMinInterval and ParseSchedule have all run and are
	// CPU-only. Putting the cap check ahead of them would turn a malformed-body
	// flood into a lock-acquisition flood on the owner's users row and buy
	// nothing: an invalid request cannot create a row whether the owner is at the
	// cap or not.
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create scheduled job failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	txq := s.q.WithTx(tx)

	// Statement 1: the lock. Its own statement, BEFORE the count, so the count's
	// snapshot is taken after any competitor has committed. See the query's own
	// header for why merging the two re-opens the race.
	if _, err := txq.LockOwnerForScheduleCap(ctx, u.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Unreachable today: users are archived, never deleted. Handled so a
			// future hard delete fails CLOSED rather than skipping the count.
			log.Printf("scheduled_jobs: cap lock found no owner row for %s", uuidStr(u.ID))
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		writeError(w, http.StatusInternalServerError, "create scheduled job failed")
		return
	}

	// Statement 2: the BOUNDED count. n saturates at the cap and is never a
	// census, which is why the refusal below names the limit and never the count.
	limit := s.maxSchedulesPerOwner()
	n, err := txq.CountScheduledJobsForOwnerUpTo(ctx, store.CountScheduledJobsForOwnerUpToParams{
		OwnerID: u.ID,
		Ceiling: int64(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create scheduled job failed")
		return
	}
	// 409, not 429 and not 400: it is not a rate and the input is not invalid.
	// relayclient.ErrorIsTransient classifies 409 as NOT transient, so no poller
	// retries it and the caller must act. Admins are NOT exempt - this route is
	// reachable by every authenticated principal, so an exemption would carve a
	// hole in a control everyone else is subject to for the population most
	// likely to be running the automation that fills the table.
	//
	// The message does not name the environment variable and does not say "ask an
	// operator to raise it": a refusal an actor can drive must not advertise, to
	// that actor, the remedy that loosens the control.
	if n >= int64(limit) {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"scheduled job limit reached: this account is at the per-owner limit of %d. "+
				"Delete a scheduled job before creating another.", limit))
		return
	}

	// Statement 3: the insert, unchanged.
	row, err := txq.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          req.Name,
		OwnerID:       u.ID,
		CronExpr:      req.CronExpr,
		Timezone:      req.Timezone,
		JobSpec:       req.JobSpec,
		OverlapPolicy: req.OverlapPolicy,
		Enabled:       enabled,
		NextRunAt:     pgtype.Timestamptz{Time: next, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create scheduled job failed")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "create scheduled job failed")
		return
	}
```

Leave the response-building block below it (`items`, `fillOwnerEmails`, `fillLastJobStatuses`, `writeJSON`) exactly as it is: it reads through `s.q` on the pool, after the commit.

Add `"fmt"` to the import block if it is not already there (it is - `internal/api/scheduled_jobs.go` already imports `fmt`, `errors`, `log`, and `pgx`; confirm rather than assume).

- [ ] **Step 2: Run the RED test from Task 1 to verify it now passes**

```bash
go test -tags integration -count=1 ./cmd/relay-server/... -run TestScheduleCap_TheThirdCreateIsRefusedAtACapOfTwo -v -timeout 300s
```

Expected: PASS.

- [ ] **Step 3: Run the packages that could regress**

```bash
go test ./internal/api/... ./cmd/relay-server/... -count=1 -timeout 180s
go vet -tags integration ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/api/scheduled_jobs.go
git commit -m "feat(api): refuse POST /v1/scheduled-jobs with 409 at the per-owner cap"
```

---

## Task 6: Wire the deps field and prove an admin is not exempt

**Files:**
- Modify: `cmd/relay-server/http_server.go`
- Modify: `cmd/relay-server/main.go`
- Modify: `cmd/relay-server/schedulecap_wiring_integration_test.go` (add AC4)

- [ ] **Step 1: Write the failing test (AC4)**

Append to `cmd/relay-server/schedulecap_wiring_integration_test.go`:

```go
// TestScheduleCap_AnAdminIsRefusedExactlyAsANonAdminIs is AC4.
//
// THE ADMIN CASE RUNS FIRST, so an early-exit exemption
// (`if u.IsAdmin { skip the check }`) cannot pass by never being reached: a
// decoy placed after its target is read by neither the code nor the mutant.
//
// Both owners share one server and one cap, which is what makes this a statement
// about the CHECK rather than about two independently configured servers.
func TestScheduleCap_AnAdminIsRefusedExactlyAsANonAdminIs(t *testing.T) {
	pool, q := newPgdsnPoolAndQueries(t)
	admin := createUserWithTestPassword(t, q, "Admin", "cap-admin@example.com", true)
	plain := createUserWithTestPassword(t, q, "Plain", "cap-plain@example.com", false)
	adminToken := createAPIToken(t, q, admin.ID)
	plainToken := createAPIToken(t, q, plain.ID)

	srv := buildHTTPServer(httpServerDeps{
		addr:                 "127.0.0.1:0",
		pool:                 pool,
		q:                    q,
		maxSchedulesPerOwner: 2,
	})

	for _, tc := range []struct {
		who   string
		token string
	}{
		{"admin", adminToken},
		{"non-admin", plainToken},
	} {
		t.Run(tc.who, func(t *testing.T) {
			for i := 1; i <= 2; i++ {
				rec := postSchedule(t, srv, tc.token, fmt.Sprintf("%s-under-%d", tc.who, i))
				require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
			}
			over := postSchedule(t, srv, tc.token, tc.who+"-over")
			require.Equal(t, http.StatusConflict, over.Code,
				"%s must be refused at the cap. An exemption for admins would carve a hole in a "+
					"control everyone else is subject to, for the population most likely to be "+
					"running the automation that fills the table, and the boot sweep does not care "+
					"whose rows they are. body: %s", tc.who, over.Body.String())
		})
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test -tags integration -count=1 ./cmd/relay-server/... -run TestScheduleCap_ -v -timeout 300s
```

Expected: still a compile failure on `maxSchedulesPerOwner` - the field does not exist yet.

- [ ] **Step 3: Add the deps field and the assignment**

In `cmd/relay-server/http_server.go`, add to `httpServerDeps`, immediately after the `passwordChangeLimitN`/`passwordChangeLimitWin` pair:

```go
	// maxSchedulesPerOwner bounds how many scheduled_jobs rows one owner may
	// hold. An exported FIELD on api.Server rather than another argument on
	// api.New, for the same reason as searchLimitN above.
	maxSchedulesPerOwner int
```

And in `buildHTTPServer`, after `s.PasswordChangeLimitWin = d.passwordChangeLimitWin`:

```go
	s.MaxSchedulesPerOwner = d.maxSchedulesPerOwner
```

Then extend `buildHTTPServer`'s header ledger. Find the paragraph that ends with the SearchLimit and PasswordChangeLimit sentences and append:

```
//     The MaxSchedulesPerOwner field is guarded the same way, through
//     TestScheduleCap_TheThirdCreateIsRefusedAtACapOfTwo, which drives three real
//     creates through this function's output at a cap of two against a real
//     Postgres and asserts 201, 201, 409. It is in the INTEGRATION lane because
//     the count and the lock are database statements; that lane is run by
//     go-ci.yml's pg-integration job.
```

And to the paragraph beginning "THAT LAST CLAIM STOPS AT THIS FUNCTION'S OWN ASSIGNMENTS", append:

```
// The maxSchedulesPerOwner literal is covered by
// TestMain_PassesTheScheduleCapItParsed, on the same terms as the
// passwordChangeLimit pair.
```

- [ ] **Step 4: Wire main**

In `cmd/relay-server/main.go`, immediately after the `passwordChangeN, passwordChangeWin, err := api.ParseRateLimit(...)` block and its `log.Fatalf` guard, add - as a DIRECT child of `main`'s body, not inside any `if`:

```go
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
```

And add to the `buildHTTPServer(httpServerDeps{...})` literal, after `passwordChangeLimitWin: passwordChangeWin,`:

```go
		maxSchedulesPerOwner:   maxSchedulesPerOwner,
```

- [ ] **Step 5: Run to verify green**

```bash
go build ./... && go vet -tags integration ./...
go test -tags integration -count=1 ./cmd/relay-server/... -run TestScheduleCap_ -v -timeout 300s
```

Expected: both `TestScheduleCap_` tests PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/relay-server/http_server.go cmd/relay-server/main.go cmd/relay-server/schedulecap_wiring_integration_test.go
git commit -m "feat: wire RELAY_MAX_SCHEDULES_PER_OWNER into the api.Server"
```

---

## Task 7: The wiring guard on main's deps literal (AC8)

**Files:**
- Modify: `cmd/relay-server/schedulecap_config_test.go`

**Why a parser guard and not a behavioural one.** Every hop AFTER the literal is already covered behaviourally, by Task 6's tests: they supply `maxSchedulesPerOwner` themselves and drive a real refusal through `buildHTTPServer`'s output, so a deleted, hard-coded or crossed `s.MaxSchedulesPerOwner = d.maxSchedulesPerOwner` is RED there. The one hop nothing executable can reach is main's literal: `main` is not callable from a test, it opens a pool, and it can `log.Fatalf` several times before reaching the literal. **Zeroing that literal left an entire lane green earlier in this batch.** CLAUDE.md is right that a parser guard is the expensive fallback; it is taken here because the cheaper rung does not exist, and the guard's own comment must say so.

- [ ] **Step 1: Write the failing test**

Append to `cmd/relay-server/schedulecap_config_test.go` (which needs `go/ast`, `go/token` and `strings` added to its imports):

```go
// TestMain_PassesTheScheduleCapItParsed closes the one gap the executed tests
// cannot reach: TestScheduleCap_TheThirdCreateIsRefusedAtACapOfTwo supplies the
// cap itself, so it says nothing about what main puts in the httpServerDeps
// literal. Zeroing that literal, or trading it for another of main's same-typed
// int locals, leaves this whole package green while the operator's number is
// ignored in production.
//
// A PARSER GUARD IS THE EXPENSIVE FALLBACK and this project has watched one get
// evaded four ways. It is here because nothing executable inside the process can
// see main's literal: main is not callable from a test and can log.Fatalf before
// it reaches that line. Tasks 5 and 6's tests cover every hop AFTER it.
//
// WHAT IT CANNOT SEE, so its name is not read as more than it checks: a value
// laundered through an intermediate local is followed, but a value TRANSFORMED
// on the way is not. It proves the wiring was not deleted, zeroed or crossed. It
// proves nothing about fidelity.
//
// DO NOT PASTE ANOTHER COPY OF THIS GUARD FAMILY. The row below is written in
// the shape prescribed by
// docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md - one
// row per wired field, with the function its value must derive from - so a
// generalization lifts it without redesign. mainBodyOfPackage is shared with
// TestMain_PassesThePasswordChangeLimitItParsed rather than duplicated.
//
// IT DOES NOT CHECK A FATAL-ON-ERROR FOLLOW-UP, unlike its password sibling, and
// that is deliberate rather than an omission: parseScheduleCap returns no error.
// Its three outcomes are pinned by TestParseScheduleCap instead.
func TestMain_PassesTheScheduleCapItParsed(t *testing.T) {
	body := mainBodyOfPackage(t)

	// from[name] = identifiers its RHS mentions, collected only from assignments
	// that are DIRECT children of main's body, so a parse moved inside an if
	// reaches nothing. Arity-tolerant: parseScheduleCap binds two names from one
	// call.
	from := map[string][]string{}
	for _, st := range body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok {
			continue
		}
		var rhs []string
		for _, e := range as.Rhs {
			ast.Inspect(e, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					rhs = append(rhs, id.Name)
				}
				return true
			})
		}
		for _, l := range as.Lhs {
			if id, ok := l.(*ast.Ident); ok {
				from[id.Name] = append(from[id.Name], rhs...)
			}
		}
	}

	// Every identifier assigned anywhere in main's subtree - ifs, loops,
	// switches and closures included. Derivation alone is defeated by a later
	// assignment of zero inside an if.
	assignedAnywhere := map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, l := range as.Lhs {
			if id, ok := l.(*ast.Ident); ok {
				assignedAnywhere[id.Name]++
			}
		}
		return true
	})

	fields := map[string]ast.Expr{}
	calls := 0
	ast.Inspect(body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := ce.Fun.(*ast.Ident)
		if !ok || id.Name != "buildHTTPServer" {
			return true
		}
		calls++
		require.Len(t, ce.Args, 1)
		cl, ok := ce.Args[0].(*ast.CompositeLit)
		require.True(t, ok,
			"buildHTTPServer must be called with an httpServerDeps composite literal at the call "+
				"site, so every dependency is readable there")
		for _, e := range cl.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if k, ok := kv.Key.(*ast.Ident); ok {
				fields[k.Name] = kv.Value
			}
		}
		return true
	})
	require.Equal(t, 1, calls,
		"main must call buildHTTPServer exactly once: called twice the last one decides and this "+
			"guard cannot say which")

	const field = "maxSchedulesPerOwner"
	const mustReach = "parseScheduleCap"

	value, present := fields[field]
	require.True(t, present,
		"buildHTTPServer is called with no %s field, so RELAY_MAX_SCHEDULES_PER_OWNER is ignored in "+
			"production while every test in this package stays green", field)

	ident, isIdent := value.(*ast.Ident)
	require.True(t, isIdent,
		"httpServerDeps.%s must be fed a plain identifier, not %T. A literal there is a hard-coded "+
			"cap that RELAY_MAX_SCHEDULES_PER_OWNER no longer controls.", field, value)

	seen := map[string]bool{}
	queue := []string{ident.Name}
	reached := false
	for len(queue) > 0 && !reached {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		if name == mustReach {
			reached = true
			break
		}
		queue = append(queue, from[name]...)
	}
	require.True(t, reached,
		"httpServerDeps.%s is fed %q, which does not derive from %s through an unconditional "+
			"assignment in main's body. Crossing it with another int local - jobSubmitN, searchN, "+
			"loginN - compiles and leaves this package green.", field, ident.Name, mustReach)

	require.Equal(t, 1, assignedAnywhere[ident.Name],
		"%q is assigned %d times inside main. Exactly one unconditional assignment is the whole "+
			"basis on which this test concludes anything: a second one, in an if or a loop, can take "+
			"the wiring back on some deployments while every check above still passes.",
		ident.Name, assignedAnywhere[ident.Name])

	require.True(t, seen["RELAY_MAX_SCHEDULES_PER_OWNER"] || func() bool {
		for _, r := range from[ident.Name] {
			if strings.Contains(r, "RELAY_MAX_SCHEDULES_PER_OWNER") {
				return true
			}
		}
		return false
	}(), "the chain feeding httpServerDeps.%s never mentions RELAY_MAX_SCHEDULES_PER_OWNER", field)
}
```

**Note on the env-var arm:** `from` collects identifiers only, not string literals, so the final assertion needs the string-literal collection the password guard does. If it is RED on correct code, extend the `from` walk in this test to also collect `*ast.BasicLit` with `bl.Kind == token.STRING` via `strconv.Unquote`, exactly as `TestMain_PassesThePasswordChangeLimitItParsed` does, and add `strconv` to the imports. **Do not delete the assertion to make it green** - it is the only thing distinguishing this int local from `jobSubmitN`.

- [ ] **Step 2: Run it to verify it passes on the wiring from Task 6**

```bash
go test ./cmd/relay-server/... -run TestMain_PassesTheScheduleCapItParsed -v -timeout 60s
```

- [ ] **Step 3: Prove it kills the mutation it exists for**

Copy `main.go` first: **never `git checkout --` to revert a mutation** - it would discard the uncommitted guard under test.

```bash
cp cmd/relay-server/main.go "$SCRATCH/lane-d-plan-main.go.bak"
```

Apply each of these four mutations in turn. **Three of the four orphan the parsed local, so their naive form does NOT compile ("declared and not used") - and a build failure is not a kill.** Add `_ = maxSchedulesPerOwner` beside each so the mutant compiles and the guard is genuinely exercised:

| Mutation | Compiles naively? | Expected |
| --- | --- | --- |
| Delete the `maxSchedulesPerOwner:` key from the literal | NO - add `_ = maxSchedulesPerOwner` | RED: "called with no maxSchedulesPerOwner field" |
| `maxSchedulesPerOwner: 100,` | NO - add `_ = maxSchedulesPerOwner` | RED: "must be fed a plain identifier" |
| `maxSchedulesPerOwner: jobSubmitN,` | NO - add `_ = maxSchedulesPerOwner` | RED: "does not derive from parseScheduleCap" |
| Add `maxSchedulesPerOwner = 0` inside an existing `if` in main | YES | RED: "is assigned 2 times inside main" |

Restore from the copy after each:

```bash
cp "$SCRATCH/lane-d-plan-main.go.bak" cmd/relay-server/main.go
go test ./cmd/relay-server/... -run TestMain_PassesTheScheduleCapItParsed -timeout 60s
```

The final run is the control that should be GREEN: a mutation battery with no green baseline reports uniform results and proves nothing.

- [ ] **Step 4: Commit**

```bash
git add cmd/relay-server/schedulecap_config_test.go
git commit -m "test: guard that main passes the schedule cap it parsed into the deps literal"
```

---

## Task 8: The store lane - the concurrency guard, its control, and no-enforcement-in-SQL

**Files:**
- Create: `internal/store/scheduled_jobs_cap_integration_test.go`

- [ ] **Step 1: Write the tests**

```go
//go:build integration

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capFixture is one owner plus helpers that insert and count that owner's
// schedules through the production statements.
type capFixture struct {
	pool  *pgxpool.Pool
	q     *store.Queries
	ctx   context.Context
	owner store.User
}

func newCapFixture(t *testing.T) *capFixture {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	return &capFixture{pool: pool, q: q, ctx: context.Background(), owner: newTestUser(t, q, false)}
}

func (f *capFixture) insert(t *testing.T, q *store.Queries, name string) {
	t.Helper()
	_, err := q.CreateScheduledJob(f.ctx, store.CreateScheduledJobParams{
		Name: name, OwnerID: f.owner.ID, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec:       []byte(`{"name":"j","tasks":[{"name":"t","command":["echo","x"]}]}`),
		OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
}

func (f *capFixture) countUpTo(t *testing.T, q *store.Queries, ceiling int64) int64 {
	t.Helper()
	n, err := q.CountScheduledJobsForOwnerUpTo(f.ctx, store.CountScheduledJobsForOwnerUpToParams{
		OwnerID: f.owner.ID, Ceiling: ceiling,
	})
	require.NoError(t, err)
	return n
}

func (f *capFixture) rows(t *testing.T) int64 {
	t.Helper()
	var n int64
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM scheduled_jobs WHERE owner_id = $1`, f.owner.ID).Scan(&n))
	return n
}

// waitUntilOneSessionIsBlockedOnALock replaces a sleep: if B has not reached the
// lock, committing A would let B run against a post-A snapshot and the test
// would prove nothing.
//
// THE WAIT IS OWNED BY THIS TEST. pgdsn gives every test its own freshly CREATEd
// database, and the predicate is scoped to current_database(), so no sibling
// lane can satisfy it - the failure mode where a concurrency wait passes OPEN
// because someone else's session matched.
//
// The condition runs on testify's goroutine, so a poll error is captured and
// re-asserted on the test goroutine; assert.Eventually rather than require, for
// the same reason as internal/store's retry fixture - require would FailNow on
// timeout and the poll error would never be reported.
func (f *capFixture) waitUntilOneSessionIsBlockedOnALock(t *testing.T) {
	t.Helper()
	var (
		pollMu  sync.Mutex
		pollErr error
	)
	ok := assert.Eventually(t, func() bool {
		var n int
		if err := f.pool.QueryRow(f.ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock' AND state = 'active'`).Scan(&n); err != nil {
			pollMu.Lock()
			pollErr = err
			pollMu.Unlock()
			return false
		}
		return n > 0
	}, 10*time.Second, 50*time.Millisecond,
		"B never blocked on A's users-row lock, so this test would prove nothing about ordering")
	pollMu.Lock()
	defer pollMu.Unlock()
	require.NoError(t, pollErr, "the pg_stat_activity poll itself failed")
	require.True(t, ok)
}

// TestScheduleCapLock_TwoConcurrentCreatesAtCapMinusOneInsertExactlyOne is AC3,
// sequenced deterministically rather than by timing.
//
// THE PROPERTY: because statement 1 is its OWN statement, B's COUNTING STATEMENT
// does not begin until the lock is granted, which is after A commits - so B's
// snapshot includes A's row and B counts the cap.
//
// BOUND THE FAILURE. B's lock acquisition runs under a context deadline, so a
// mutant that never blocks or never releases fails BY NAME instead of hanging;
// a hang is indistinguishable from infrastructure trouble.
func TestScheduleCapLock_TwoConcurrentCreatesAtCapMinusOneInsertExactlyOne(t *testing.T) {
	f := newCapFixture(t)
	const capValue = int64(2)
	f.insert(t, f.q, "already-held") // the owner is now at cap - 1

	txA, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txA.Rollback(f.ctx) //nolint:errcheck
	qa := f.q.WithTx(txA)

	_, err = qa.LockOwnerForScheduleCap(f.ctx, f.owner.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), f.countUpTo(t, qa, capValue), "A sees one row and is admitted")

	txB, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txB.Rollback(f.ctx) //nolint:errcheck
	qb := f.q.WithTx(txB)

	lockCtx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()

	var (
		wg        sync.WaitGroup
		lockErrB  error
		nB        int64
		countErrB error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, lockErrB = qb.LockOwnerForScheduleCap(lockCtx, f.owner.ID); lockErrB != nil {
			return
		}
		nB, countErrB = qb.CountScheduledJobsForOwnerUpTo(lockCtx,
			store.CountScheduledJobsForOwnerUpToParams{OwnerID: f.owner.ID, Ceiling: capValue})
	}()

	f.waitUntilOneSessionIsBlockedOnALock(t)

	f.insert(t, qa, "a-wins")
	require.NoError(t, txA.Commit(f.ctx))

	wg.Wait()
	require.NoError(t, lockErrB,
		"B's lock must be granted once A commits; a deadline error here means it never was")
	require.NoError(t, countErrB)
	require.Equal(t, capValue, nB,
		"B's counting statement must take a snapshot AFTER the lock was granted, so it sees A's "+
			"committed row and refuses. A count of 1 here means the lock was dropped, merged into "+
			"the counting statement, or replaced by a single conditional INSERT - and B would write "+
			"a third row over a cap of two.")

	require.NoError(t, txB.Rollback(f.ctx))
	require.Equal(t, capValue, f.rows(t), "exactly one insert survives")
}

// TestScheduleCapLock_WithoutTheLockBothTransactionsInsert is THE CONTROL, and
// without it a green sibling is indistinguishable from a test whose sessions
// never overlapped.
//
// It is also the permanent form of the two-psql-session measurement the spec
// owed: under READ COMMITTED a count subquery is evaluated against the snapshot
// taken when its statement began, which cannot see a concurrent uncommitted
// insert - so two requests at cap-1 both pass and both commit. This is the same
// overshoot internal/worker/handler.go's auto-enroll ceiling documents for the
// same shape.
func TestScheduleCapLock_WithoutTheLockBothTransactionsInsert(t *testing.T) {
	f := newCapFixture(t)
	const capValue = int64(2)
	f.insert(t, f.q, "already-held")

	txA, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txA.Rollback(f.ctx) //nolint:errcheck
	txB, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txB.Rollback(f.ctx) //nolint:errcheck
	qa, qb := f.q.WithTx(txA), f.q.WithTx(txB)

	// NO LockOwnerForScheduleCap in either transaction. Neither count blocks.
	require.Equal(t, int64(1), f.countUpTo(t, qa, capValue))
	require.Equal(t, int64(1), f.countUpTo(t, qb, capValue),
		"B's count does not block and does not see A: this is the race the lock exists to close")

	f.insert(t, qa, "a")
	f.insert(t, qb, "b")
	require.NoError(t, txA.Commit(f.ctx))
	require.NoError(t, txB.Commit(f.ctx))

	require.Equal(t, int64(3), f.rows(t),
		"both transactions were admitted at cap-1 and both committed, so the owner holds three rows "+
			"over a cap of two")
}

// TestCreateScheduledJob_TheStoreDoesNotEnforceTheCap is AC6. The cap is an
// ADMISSION policy: no constraint and no trigger enforces it, and
// ValidateStoredSchedule never learns about it.
//
// A CHECK constraint or a trigger would be retroactively hostile in the way a
// validator tightening usually is: it would make an over-cap owner's rows
// unwritable by ANY statement, including the boot sweep's own
// RecordScheduledJobFailure. It would also break every fixture that plants rows
// directly - internal/schedrunner's paging test plants 250 for one owner.
//
// 105 is the shipped default plus five, spelled as a literal because
// internal/store must not import internal/api (that direction is the cycle).
func TestCreateScheduledJob_TheStoreDoesNotEnforceTheCap(t *testing.T) {
	f := newCapFixture(t)
	const overDefaultCap = 105
	for i := 0; i < overDefaultCap; i++ {
		f.insert(t, f.q, "direct-"+strconv.Itoa(i))
	}
	require.Equal(t, int64(overDefaultCap), f.rows(t),
		"every direct CreateScheduledJob must succeed: enforcing the cap in SQL would make an "+
			"over-cap owner's rows unwritable by the sweep and would break every planting fixture")

	// AND THE BOUNDED COUNT SATURATES rather than reporting the true total.
	require.Equal(t, int64(100), f.countUpTo(t, f.q, 100),
		"the count saturates at its ceiling; anything that reads it as a census is reading a number "+
			"the statement cannot support")
}
```

Add `strconv` and `pgx` to the imports as the compiler demands (`pgx` is used only if you add an `errors.Is` arm; drop it if unused).

- [ ] **Step 2: Run to verify**

```bash
go test -tags integration -count=1 ./internal/store/... -run 'TestScheduleCapLock|TestCreateScheduledJob_TheStoreDoesNotEnforceTheCap' -v -timeout 300s
```

Expected: all three PASS. If `TestScheduleCapLock_WithoutTheLockBothTransactionsInsert` reports `2` instead of `3`, the race is not reproducible in this environment and the concurrency argument must be re-derived - STOP and report, because that also invalidates Task 0's measurement 1.

- [ ] **Step 3: Prove the concurrency guard kills its mutation**

```bash
cp internal/store/query/scheduled_jobs.sql "$SCRATCH/lane-d-plan-scheduled_jobs.sql.bak"
cp internal/store/scheduled_jobs.sql.go "$SCRATCH/lane-d-plan-scheduled_jobs.sql.go.bak"
```

Mutate `LockOwnerForScheduleCap` to `FOR SHARE` (a mode that does not conflict with itself), `make generate`, re-run. Expected: `TestScheduleCapLock_TwoConcurrentCreatesAtCapMinusOneInsertExactlyOne` fails at `B never blocked on A's users-row lock`, which names the right guard. Restore **both** files from the copies, `make generate` again, re-run for a green control.

- [ ] **Step 4: Commit**

```bash
git add internal/store/scheduled_jobs_cap_integration_test.go
git commit -m "test(store): the cap's lock serialises two creates at cap-1, with the control that shows the race"
```

---

## Task 9: Retroactivity - an over-cap owner reads, patches and deletes but cannot create (AC5)

**Files:**
- Create: `internal/api/scheduled_jobs_cap_integration_test.go`

**This is THE test that pins the grandfathering decision.** The decision is that owners already over the cap keep every row and lose exactly one capability. Nothing else in the tree counts an owner's schedules, so the retroactivity is confined to one route - and this test is what stops a later "consistency" edit adding the check to PATCH or run-now.

- [ ] **Step 1: Write the test**

```go
//go:build integration

package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// TestScheduledJobCap_AnOverCapOwnerKeepsEveryRouteButCreate pins Decision 3.
//
// The alternatives were REJECTED for reasons this test makes executable:
// deleting the excess destroys an operator's configuration on a rule they have
// never seen, with no undo; disabling it silently stops production work and
// teaches operators that enabled=false means nothing.
//
// THE POST ARM IS NOT OPTIONAL. Five green "unchanged" arms are also what a
// server with no cap at all produces, so the refusal is what makes the other
// five mean "grandfathered" rather than "nothing was implemented".
//
// PATCH AND RUN-NOW ARE THE ARMS THAT MOST WANT THIS GUARD: they are where a
// later consistency edit would add a check, and a PATCH that refuses is a PATCH
// that can refuse an owner's attempt to REPAIR a broken schedule. DELETE is the
// self-service remedy the refusal message names, so it must never be refused.
//
// LANE: internal/api integration, which no CI job runs today. If the sibling
// internal/api CI lane lands, this file starts running in CI unchanged and needs
// no edit.
func TestScheduledJobCap_AnOverCapOwnerKeepsEveryRouteButCreate(t *testing.T) {
	srv, q := newTestServer(t)
	srv.MaxSchedulesPerOwner = 2
	user := createTestUser(t, q, "Legacy", "cap-grandfathered@example.com", false)
	token := createTestToken(t, q, user.ID)

	// cap + 3 rows planted through the store, which is exactly how an over-cap
	// owner exists on the deploy that lands the cap.
	ctx := context.Background()
	var planted []store.ScheduledJob
	for i := 0; i < 5; i++ {
		row, err := q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
			Name: fmt.Sprintf("legacy-%d", i), OwnerID: user.ID, CronExpr: "@hourly", Timezone: "UTC",
			JobSpec:       []byte(`{"name":"j","tasks":[{"name":"t","command":["echo","x"]}]}`),
			OverlapPolicy: "skip", Enabled: false,
			NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		})
		require.NoError(t, err)
		planted = append(planted, row)
	}

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	id := uuidString(planted[0].ID)

	t.Run("list still returns every row", func(t *testing.T) {
		rec := do("GET", "/v1/scheduled-jobs", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "legacy-4")
	})

	t.Run("get still returns one row", func(t *testing.T) {
		rec := do("GET", "/v1/scheduled-jobs/"+id, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("patch succeeds, INCLUDING enabling", func(t *testing.T) {
		rec := do("PATCH", "/v1/scheduled-jobs/"+id, `{"enabled":true,"name":"repaired"}`)
		require.Equal(t, http.StatusOK, rec.Code,
			"PATCH must never enforce the cap: it cannot increase the count, and a refusal here "+
				"would block an over-cap owner from repairing or disabling a schedule. body: %s",
			rec.Body.String())
	})

	t.Run("run-now succeeds", func(t *testing.T) {
		rec := do("POST", "/v1/scheduled-jobs/"+id+"/run-now", "")
		require.Equal(t, http.StatusCreated, rec.Code,
			"run-now creates a JOB from a stored spec and no scheduled_jobs row at all, so it is "+
				"neither an enforcement point nor an evasion. body: %s", rec.Body.String())
	})

	t.Run("delete succeeds - it is the remedy the refusal names", func(t *testing.T) {
		rec := do("DELETE", "/v1/scheduled-jobs/"+uuidString(planted[4].ID), "")
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	})

	t.Run("only POST is refused", func(t *testing.T) {
		rec := do("POST", "/v1/scheduled-jobs",
			`{"name":"one-more","cron_expr":"@hourly","job_spec":{"name":"j","tasks":[{"name":"t","command":["echo","x"]}]}}`)
		require.Equal(t, http.StatusConflict, rec.Code,
			"the ONE thing an over-cap owner loses is the ability to create another schedule. "+
				"Without this arm the five green arms above are also what a server with no cap "+
				"produces. body: %s", rec.Body.String())
	})

	// AND NOTHING WAS DESTROYED. The cap does not shrink an existing table by one
	// row; four remain after the one deliberate DELETE.
	var n int64
	require.NoError(t, srvPool(t, q).QueryRow(ctx,
		`SELECT count(*) FROM scheduled_jobs WHERE owner_id = $1`, user.ID).Scan(&n))
	require.Equal(t, int64(4), n,
		"grandfathering means nothing is deleted, disabled or flagged by the cap itself")
	_ = api.DefaultMaxSchedulesPerOwner
}
```

**Note:** `newTestServer` returns `(*api.Server, *store.Queries)` and does not expose the pool. For the final count either use `newTestServerWithPool` (declared in `internal/api/tasks_integration_test.go`, same `api_test` package) and set `MaxSchedulesPerOwner` on the returned server, or drop the raw-SQL count and assert through `GET /v1/scheduled-jobs/stats`. **Prefer `newTestServerWithPool`** - the helper already exists and the raw count is the assertion that cannot be satisfied by a handler bug. Delete the `srvPool` placeholder and the trailing `_ = api.DefaultMaxSchedulesPerOwner` when you do.

- [ ] **Step 2: Run to verify**

```bash
go test -tags integration -count=1 ./internal/api/... -run TestScheduledJobCap_ -v -timeout 600s
```

Expected: all six subtests PASS.

- [ ] **Step 3: Prove the PATCH arm kills its mutation**

```bash
cp internal/api/scheduled_jobs.go "$SCRATCH/lane-d-plan-scheduled_jobs.go.bak"
```

Add the same cap check to `handlePatchScheduledJob`. Expected: the `patch succeeds, INCLUDING enabling` subtest goes RED with `409`. Restore from the copy and re-run for a green control.

- [ ] **Step 4: Commit**

```bash
git add internal/api/scheduled_jobs_cap_integration_test.go
git commit -m "test(api): an over-cap owner keeps every route but create"
```

---

## Task 10: Advertisement surfaces

**Files:**
- Modify: `README.md`
- Modify: `internal/schedrunner/startup_validation.go`

**Before any programmatic edit here, read CLAUDE.md's "Line endings" section.** After each edit: check the diffstat against the size of the change you intended, run `git ls-files --eol` on the touched paths (every one must read `i/lf`), and assert the file still decodes as UTF-8. Prefer exact-anchor replacement over regex, and print before/after line counts.

- [ ] **Step 1: Add the README env row**

Insert immediately after the `RELAY_AUTO_ENROLL_WORKER_CEILING` row, so the two count ceilings are adjacent:

```
| `RELAY_MAX_SCHEDULES_PER_OWNER` | `100` | Refuses `POST /v1/scheduled-jobs` with `409` once one owner holds this many `scheduled_jobs` rows. **Per owner, not a fleet ceiling:** M accounts hold M x the cap, and with `RELAY_ALLOW_SELF_REGISTER=true` the account population is bounded only by a per-source-address rate limit. **It counts ALL of an owner's rows, enabled or not**, and that is what keeps creation the only enforcement point: a `PATCH` cannot increase the count, so no `PATCH` is ever refused by this. **Owners already over the cap keep every schedule** - nothing is deleted, disabled or flagged - and are refused only new ones; list, get, patch, delete and run-now all keep working for them. The refusal names the self-service remedy: delete a scheduled job before creating another. **There is no off value.** `0`, a negative value and an unparseable value all keep the default and warn, naming the value that was ignored; a very large number is the spelling for effectively-unbounded, and it stays visible as a number in the environment and in the startup line. **Admins are not exempt.** Requires a server restart to change. **The failure mode to expect:** a pipeline service account that mints one schedule per shot or per asset hits this, and in a studio where one service account owns every schedule that one account hits it while every artist owns zero. The two remedies are one schedule whose `job_spec` fans out into tasks - the model relay is built around, and the same advice the submit row gives - or raise the number. **It bounds the boot sweep's STARTING work set, not the sweep's duration:** every page of `ValidateStoredSpecsOnStartup` is a fresh snapshot, so an owner at the cap can delete one row and post another for as long as the sweep runs. |
```

- [ ] **Step 2: Extend the `RELAY_JOB_SUBMIT_RATE_LIMIT` row**

Find the exact existing sentence and **do not remove it** - it is the record of a decision. Replace:

```
**`POST /v1/scheduled-jobs` is deliberately not in this bucket** - a creation-rate limit bounds how fast `scheduled_jobs` fills, not how full it gets - and **no HTTP rate limit anywhere bounds a schedule's own firing**.
```

with the same text plus one clause:

```
**`POST /v1/scheduled-jobs` is deliberately not in this bucket** - a creation-rate limit bounds how fast `scheduled_jobs` fills, not how full it gets, and `RELAY_MAX_SCHEDULES_PER_OWNER` is what bounds how full it gets - and **no HTTP rate limit anywhere bounds a schedule's own firing**.
```

- [ ] **Step 3: Add the 409 to the API reference**

In the Scheduled Jobs endpoint table, change the `POST /v1/scheduled-jobs` row's description to:

```
| `POST` | `/v1/scheduled-jobs` | Create a scheduled job. `409` when the caller is at `RELAY_MAX_SCHEDULES_PER_OWNER`; delete one first. Admins are not exempt. |
```

- [ ] **Step 4: Rewrite the sweep header's conditional**

In `internal/schedrunner/startup_validation.go`, `ValidateStoredSpecsOnStartup`'s header currently reads:

```
// Growing that table is an ordinary authenticated user's privilege. A per-owner schedule cap
// would bound the STARTING work set and not the pass, because every page is a
// fresh snapshot: ...
// The cap and that open
// question: docs/backlog/feature-2026-09-04-per-owner-schedule-cap.md.
```

Replace with:

```
// Growing that table is an ordinary authenticated user's privilege, bounded per
// owner by RELAY_MAX_SCHEDULES_PER_OWNER. THAT CAP BOUNDS THE STARTING WORK SET
// AND NOT THE PASS, because every page is a fresh snapshot: a row inserted
// mid-sweep joins the work set whenever its gen_random_uuid() id sorts above the
// cursor, so an owner sitting at the cap can delete and re-POST to keep feeding
// one. The pass still converges, since the unswept fraction of the key space
// only shrinks, so that residual is duration amplification rather than
// non-termination - and bounding the duration itself wants a deadline on this
// sweep: docs/backlog/<the sweep-deadline item the conductor files>.
```

**The citation must point at a backlog item that EXISTS.** If the conductor has not yet filed the sweep-deadline item (proposal 1 below), leave the sentence naming the mechanism and drop the filename rather than citing a file that is not there - a dangling citation is a wrong prose claim, which is this repo's dominant defect class.

- [ ] **Step 5: Verify the edits**

```bash
git diff --stat README.md internal/schedrunner/startup_validation.go
git ls-files --eol README.md internal/schedrunner/startup_validation.go
python -c "open('README.md','rb').read().decode('utf-8'); print('README utf-8 ok')"
go build ./... && go test ./internal/schedrunner/... -count=1 -timeout 120s
```

Expected: a diffstat of roughly 4 changed lines in README and ~10 in the Go file; `i/lf` on both; UTF-8 clean.

- [ ] **Step 6: Commit**

```bash
git add README.md internal/schedrunner/startup_validation.go
git commit -m "docs: RELAY_MAX_SCHEDULES_PER_OWNER, the 409, and the sweep header's conditional"
```

---

## Task 11: The mutation battery

**Files:** none permanently. Work in an isolated copy of the worktree - **never mutate the shared worktree while sibling agents read it.**

- [ ] **Step 1: Establish a green baseline**

```bash
make test
go test -tags integration -count=1 ./internal/store/... ./cmd/relay-server/... -timeout 600s
go test -tags integration -count=1 ./internal/api/... -run 'TestScheduledJobCap_' -timeout 600s
```

A battery with no green baseline reports uniform results and proves nothing.

- [ ] **Step 2: Run each mutation, verify it APPLIED, record the killer**

| Mutation | Killed by | Verify it applied |
| --- | --- | --- |
| Delete the `n >= limit` block | `TestScheduleCap_TheThirdCreateIsRefusedAtACapOfTwo` | the block is gone from `handleCreateScheduledJob` |
| `n > int64(limit)` | same (the third create succeeds) | the operator changed |
| Move the count check below `CreateScheduledJob` | same | the statement order changed |
| `limit := DefaultMaxSchedulesPerOwner` (hard-code) | same (cap of 2 becomes 100) | the resolver call is gone |
| Delete `s.MaxSchedulesPerOwner = d.maxSchedulesPerOwner` | same | the line is gone from `buildHTTPServer` |
| `s.MaxSchedulesPerOwner = d.jobSubmitLimitN` (cross) | same (zero folds to 100) | the RHS changed |
| Drop `LockOwnerForScheduleCap` from the handler | `TestScheduleCapLock_*` only via the store test; **the handler-level drop is NOT killed by AC1** - say so | the call is gone |
| `FOR SHARE` in the lock statement (+ `make generate`) | `TestScheduleCapLock_TwoConcurrentCreates...` at "B never blocked" | `grep "FOR SHARE" internal/store/scheduled_jobs.sql.go` |
| Remove the inner `LIMIT` | nothing goes RED - it is a COST property, pinned by Task 0's `EXPLAIN` and by the statement's comment. **Record it as a survivor and say why.** | the LIMIT is gone |
| `if u.IsAdmin { skip }` | `TestScheduleCap_AnAdminIsRefusedExactlyAsANonAdminIs` | the branch exists |
| Add the cap check to `handlePatchScheduledJob` | `TestScheduledJobCap_...` PATCH arm | the check exists |
| `if raw == "0" { return 0, "" }` in `parseScheduleCap` | `TestParseScheduleCap`'s first row | the arm exists |
| Zero `maxSchedulesPerOwner` in main's deps literal | `TestMain_PassesTheScheduleCapItParsed` (needs `_ = maxSchedulesPerOwner`) | the literal changed AND it compiles |

**A kill must name its guard.** For each RED, read the failing assertion and confirm it is the one the row claims, not a different guard reddening for an unrelated reason.

**Two rows above are honest survivors.** Record them as survivors with their reason rather than manufacturing a test:
- Dropping the lock from the HANDLER is not killed by any handler-level test, because AC1's three requests are sequential. The store lane pins the mechanism; the handler's use of it is pinned by code review and by the query's own comment. **State this in the PR.**
- Removing the inner `LIMIT` changes cost, not behaviour. Task 0's `EXPLAIN` output is the evidence, and it is a measurement rather than a guard.

- [ ] **Step 3: Restore and re-run the control**

Restore every mutated file from a COPY, never with `git checkout --`, and re-run the baseline. A green control is what distinguishes "the mutation survived" from "the harness was broken".

---

## Phase 4 verification lanes

- `relay-code-reviewer` x3 (invariants / correctness / security), plus `relay-integration-tester`, dispatched in one message after the conductor runs `/code-review`.
- **Invariants lens, specific asks:** the transaction opens after validation and not before; the lock is its own statement ahead of the count; `FOR NO KEY UPDATE` and not `FOR UPDATE`; the bounded count is never read as a census anywhere; no `*.sql.go` was hand-edited; the regenerated `.sql.go` survived the CRLF revert.
- **Security lens, specific asks:** the refusal message does not name the environment variable and does not advertise the operator remedy to an actor who can drive the refusal; admins are not exempt; a refused create writes nothing; the count is keyed on the OWNER.
- **Correctness lens, specific asks:** re-derive the deadlock argument against `schedrunner.TickOnce`, which locks the two tables in the OPPOSITE order (this plan's refutation); confirm `Ceiling` is `int64` and not `int32`.
- **Integration tester:** run `make test-pg-integration` end to end, plus `go test -tags integration ./internal/api/... -run TestScheduledJobCap_`.

---

## Phase 6 proposals - backlog items for the conductor to file

None are filed by this plan. The conductor files them with `/backlog`.

1. **A wall-clock deadline on `ValidateStoredSpecsOnStartup`.** The cap does not bound the sweep's duration. The opening constraint for that item: a truncated sweep under-reports, and an empty failure surface reads as "nothing is broken" - the exact invisibility the sweep exists to fix, so its log line must say the pass was cut short and how far it got. **This is the item Task 10's sweep header must cite, so it wants filing before or alongside this PR.**
2. **`ListOverdueScheduledJobsForCatchup` is unpaged and runs before `ListenAndServe`.** CONFIRMED against the tree by this plan: the statement carries no LIMIT, `ReconcileOnStartup` is its only caller, and `main` calls it above the goroutine that starts `srv.ListenAndServe()`. The boot still materializes every overdue enabled row, so "the boot's peak memory is one page" is not true of the boot.
3. **`ListEligibleScheduledJobs` has no per-owner fairness term.** One owner's due schedules can occupy the whole `BatchLimit` batch and delay every other owner's fires. The cap mitigates this in proportion to its value and does not fix it.
4. **MCP's 409 hint reads wrong for a quota refusal.** `MapError` returns "another change conflicts with this request" for every 409, which will now include "you are at the schedule limit".

---

## Self-review

**Spec coverage.** Decision 1 -> Tasks 4, 5, 8. Decision 2 (admins not exempt) -> Task 6 AC4. Decision 3 (grandfathering) -> Task 9 AC5, plus Task 8 AC6 for the no-SQL-enforcement half. Decision 4 (duration is out of scope) -> Task 10's header rewrite and proposal 1. Decision 5 (run-now has no surface) -> Task 9's run-now arm. Decision 6 (configuration) -> Tasks 2, 3, 6, 7. Advertisement surfaces -> Task 10. All nine acceptance criteria are mapped: AC1/AC2 Task 1+6, AC3 Task 8, AC4 Task 6, AC5 Task 9, AC6 Task 8, AC7 Task 2, AC8 Task 7, AC9 Task 10.

**Deviations from the spec, each argued above:** `::bigint` instead of `::int` (int32 overflow on a value the parser accepts); the deadlock argument re-derived from the lock-mode matrix rather than from lock ordering, because `schedrunner.TickOnce` is a counter-example to the ordering claim; the `FOR NO KEY UPDATE` justification extended to name the scheduler tick, which is the larger blast radius.

**Type consistency.** `parseScheduleCap(name, raw string) (int, string)`; `scheduleCapLine(n int) string`; `api.DefaultMaxSchedulesPerOwner` (untyped const 100); `Server.MaxSchedulesPerOwner int`; `(*Server).maxSchedulesPerOwner() int`; `httpServerDeps.maxSchedulesPerOwner int`; `store.CountScheduledJobsForOwnerUpToParams{OwnerID pgtype.UUID, Ceiling int64}` returning `(int64, error)`; `LockOwnerForScheduleCap(ctx, pgtype.UUID) (pgtype.UUID, error)`. Task 4 Step 5 verifies the emitted types rather than trusting this list.

**Known plan risks, stated rather than hidden.** (a) Nothing in this plan was measured - Task 0 exists because of that and blocks everything. (b) Task 7's env-var assertion may need the string-literal walk the password guard uses; the plan says to extend the walk, never to delete the assertion. (c) Task 9's final count needs `newTestServerWithPool`; the placeholder is flagged inline.
