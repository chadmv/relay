# Boot Sweep Keyset Paging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keyset-page `schedrunner.ValidateStoredSpecsOnStartup`'s read at 100 rows per page and make a cancelled sweep return instead of logging one line per remaining broken row.

**Architecture:** `ListEnabledScheduledJobs` (no LIMIT) becomes `ListEnabledScheduledJobsPage` with the `cursor_set` / `cursor_id` / `page_limit` triple this query file already uses everywhere else. The sweep loops until a page comes back short, advancing the cursor to the last row's `id`. A `ctx.Err()` check at the top of the ROW loop returns instead of running on. Three doc-comment paragraphs and one SQL comment line that describe the unpaged shape are rewritten in the same commits that falsify them.

**Tech Stack:** Go 1.26, sqlc (`sqlc.yaml`, `emit_sql_as_comment: true`), pgx/v5 v5.9.1 (`pgxpool`, `pgx.QueryTracer`), testcontainers-go, testify.

**Source spec:** `docs/superpowers/specs/2026-09-04-boot-sweep-keyset-paging.md` (committed at fcaf212).
**Backlog item:** `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md`, half 1.

---

## Slice independence declaration

**This is ONE backend slice, ONE PR, ONE session. There is no frontend slice.** Phase 3 gets a single lane; there is nothing here to run in parallel.

The three implementation tasks are **strictly sequential** and must not be split across agents:

| Task | Files | Why it cannot run beside its neighbour |
| --- | --- | --- |
| 1 (paging) | `internal/store/query/scheduled_jobs.sql`, `internal/store/scheduled_jobs.sql.go`, `internal/schedrunner/startup_validation.go`, new `internal/schedrunner/startup_validation_paging_integration_test.go` | Introduces the traced-pool helper Task 2's test uses, and rewrites the function body Task 2 edits. |
| 2 (`ctx.Err()`) | `internal/schedrunner/startup_validation.go`, `internal/schedrunner/startup_validation_paging_integration_test.go`, `cmd/relay-server/main.go` | Same function body as Task 1; its test lives in Task 1's new file. |
| 3 (backlog amendments) | `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md` | Doc-only. Independent of Tasks 1 and 2 in the tree, but amendment 5 restates an acceptance criterion whose truth Task 1 establishes, so write it last so the sentence describes what actually shipped. |

**This plan does NOT need `/backlog phases`.** It has no multi-session stages. It does need the conductor to file one new backlog item - see Task 0.

---

## What this plan refutes in the spec

The spec is unusually well-verified and its three load-bearing claims all hold. Four things did not survive checking. Each is corrected inline in the task that owns it; they are listed here so a reviewer diffing plan against spec is not surprised.

1. **The harness DSN change is unnecessary AND less correct than the alternative.** The spec prescribes "expose the DSN on `runnerHarness` so the new test can build its own traced pool via `pgxpool.ParseConfig` plus `pgxpool.NewWithConfig`". `pgxpool.Pool.Config()` is documented as *"returns a copy of config that was used to initialize this pool"* and its body is `return p.config.Copy()`; `Config.Copy()` deep-copies `ConnConfig` and preserves the unexported `createdByParseConfig` flag `NewWithConfig` panics without. So the test can build a traced pool from `h.pool.Config()` with **zero lines changed in `runner_test.go`**. That matters twice: it removes the rebase conflict with the sibling lane rewiring `runner_test.go` onto `internal/testsupport/pgdsn`, and it is correct under that lane's shared-Postgres mode, where the per-test database DSN is not the service DSN a `dsn` field would most naturally be set from. **Do not add a DSN field.**
2. **The tracer's SELECT matcher must be name-agnostic, or the spec's own RED number is wrong.** The spec says "At HEAD: 1. This is the RED, and it is a real one." That holds only for a *structural* matcher (`FROM scheduled_jobs` + `WHERE enabled` + `ORDER BY id`), which both the old statement and the new one satisfy. A matcher keyed on the new name `ListEnabledScheduledJobsPage` returns **0** at HEAD, and 0 is also what a test that never reached the sweep produces. Task 1 pins the matcher and requires the engineer to record the HEAD number as 1.
3. **The spec identifies three stale prose sites. There are five.** It misses `internal/store/query/scheduled_jobs.sql`'s `-- Ordered by id purely for a deterministic sweep order in tests.` (`ORDER BY id` becomes the cursor's order and is load-bearing) and `cmd/relay-server/main.go`'s `the list query's error is logged here as a warning` (singular, and now false in two directions: there are up to `floor(N/100)+1` page queries, and `ctx.Err()` is a returned error that is not a query error at all). Both are fixed here.
4. **The spec prescribes the `ctx.Err()` change with no test for it.** Section 7 designs only the paging test. A behaviour change with no RED is exactly the shape this project treats as unpinned. Task 2 supplies one, and its RED is real: at HEAD the cancelled sweep returns **nil**.

Two spec claims were checked and **survive**, recorded so nobody re-litigates them:

- `RecordScheduledJobFailure`'s SQL header ("its LIST and its UPDATEs are separate implicit transactions") and `ValidateStoredSpecsOnStartup`'s "ITS WRITE IS FENCED" paragraph ("q is pool-backed, so the LIST and every UPDATE are separate statements") are **not** falsified by paging. Each page read is still its own implicit transaction and each UPDATE still its own. Rewording "the LIST" to "each page read" is cosmetic. **Do not churn them.**
- `TestScheduledJobRowStillCarriesNoFailureSurface` (`internal/schedrunner/scheduled_job_surface_test.go`, untagged) keeps covering the sweep's read, because the statement keeps `SELECT *` and therefore keeps returning `store.ScheduledJob`. This is the spec's section 3 argument and it holds.

---

## Lane placement: the assumption this plan is built on

**Assume the sibling `pg-integration` CI job lands FIRST.** That lane adds a Makefile target running `./internal/store/... ./internal/schedrunner/...` against a `services: postgres` block, and rewires `newRunnerHarness` onto a new `internal/testsupport/pgdsn`. If it lands, the two tests written here execute in CI for free.

**If it does not land, nothing in this plan changes.** Verified at this worktree's HEAD: `.github/workflows/go-ci.yml` has exactly two jobs, `race + integration-build` (which runs `make vet-integration`, i.e. `go vet -tags integration ./...`, and `go test -race ./... -timeout 180s`) and `cli integration (real server)` (`make test-cli-integration`). Neither calls `make test-integration`. So without the sibling lane these tests are **compiled** by CI and **executed** only locally. That is a fact about coverage, not a reason to weaken the tests.

Do not write the commit messages as if the sibling lane has landed. Do not describe any CI job as a merge gate: `main` on this repo carries no branch protection and no rulesets, so every check is advisory.

**Rebase-conflict flag:** the only file this plan touches that the sibling lane also touches is `internal/schedrunner/runner_test.go`, and **this plan does not touch it** (see refutation 1). If a conflict appears there during rebase it did not come from this slice.

---

## File structure

| File | Change | Task |
| --- | --- | --- |
| `internal/store/query/scheduled_jobs.sql` | `ListEnabledScheduledJobs` -> `ListEnabledScheduledJobsPage`; rewritten header comment | 1 |
| `internal/store/scheduled_jobs.sql.go` | regenerated by `make generate`. **Never hand-edit.** | 1 |
| `internal/schedrunner/startup_validation.go` | `sweepPageSize` const; page loop; two rewritten comment paragraphs | 1 |
| `internal/schedrunner/startup_validation.go` | `ctx.Err()` return; one rewritten comment clause | 2 |
| `internal/schedrunner/startup_validation_paging_integration_test.go` | **new.** Tracer helper + both tests. `//go:build integration`, `package schedrunner_test` | 1, 2 |
| `cmd/relay-server/main.go` | one rewritten comment clause at the call site | 2 |
| `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md` | six amendments | 3 |

**Not touched, deliberately:** `internal/schedrunner/runner_test.go`, `internal/store/models.go`, `ROADMAP.md` (regenerated by `/roadmap` from `docs/backlog/`; closing the item removes it from Now at the next refresh - do not hand-edit it), `internal/api/*`, `BatchLimit`, `ListEligibleScheduledJobs`, `TickOnce`.

---

## Standing rules for every task in this plan

Read these once. They are not repeated per step.

- **`sqlc` output and CRLF.** This is a CRLF repo, sqlc emits LF, and `git diff` normalizes what `git status` still lists. The full procedure is in Task 1 Step 5. Do not shortcut it, and do not conclude "nothing to revert" from `git diff` alone.
- **Never `git checkout --` to revert a mutation.** It discards uncommitted work. Copy the file to the scratchpad first and restore from the copy.
- **Mutate only inside this worktree.** Sibling lanes are running Go tests and Docker containers on this machine. Do not touch `D:/dev/relay` or any sibling worktree, and do not start or stop containers you did not create.
- **Plan-supplied test bodies are guesses.** Everything in a code block below is intent plus the assertion that matters. You must run the RED yourself, at HEAD, and record the number you actually saw. If the number differs from the one written here, STOP and report - do not adjust the assertion to match.
- **A mutation battery needs a green baseline.** Run the test green before mutating. If every mutation "kills", the harness is broken. A compile error is not a kill.
- **Verify the mutation applied.** After editing, re-read the edited line. A silently-unapplied mutation reports "survived".
- **Commit with an explicit pathspec.** `git add <paths>`, never `git add -A`.

---

## Task 0: Prerequisite the conductor owns (not the engineer)

Task 1 Step 8 rewrites a doc comment that must cite the surviving backlog scope by filename. That item does not exist yet. A decision conditioned on "the cap is coming" is unfalsifiable until the cap is a findable item.

- [ ] **Step 1: Conductor files the per-owner schedule cap item**

Run `/backlog` to file a new item for the surviving half of Proposal half 2. Content to carry into it, from the spec's "What this slice does NOT cover":

> A per-owner schedule cap. It needs a limit value, a counting query, an error shape on `POST /v1/scheduled-jobs`, and an answer for owners who already exceed the cap when it lands - tightening a validator is retroactive over stored data and the re-validating readers are hard to find. It is the only thing that bounds N, and therefore the only thing that bounds the boot sweep's DURATION; paging bounds the sweep's memory and per-statement work and nothing else.

- [ ] **Step 2: Hand the engineer the exact filename**

The expected shape is `docs/backlog/bug-2026-09-04-no-per-owner-schedule-cap.md`, but use whatever `/backlog` actually produced. Task 1 Step 8 verifies the path exists before committing and STOPS if it does not.

---

## Task 1: Keyset-page the sweep

**Files:**
- Create: `internal/schedrunner/startup_validation_paging_integration_test.go`
- Modify: `internal/store/query/scheduled_jobs.sql` (the `ListEnabledScheduledJobs` block)
- Regenerate: `internal/store/scheduled_jobs.sql.go`
- Modify: `internal/schedrunner/startup_validation.go` (the `Cost:` and `THAT IS NARROWER` paragraphs; the function body)

### The property this task pins, and why the input discriminates

**Property:** the sweep reads enabled schedules one page of 100 at a time, terminating on a short page, and records a failure for every broken row on every page including the short last one.

**Discriminating input: 250 enabled broken rows.** 250 is chosen for three independent reasons and all three matter:

- It is **more than one page**. Any input under 101 rows produces identical behaviour on paged and unpaged code and pins nothing.
- It is **more than two pages**, so a loop that runs exactly twice (a plausible off-by-one) is distinguished from one that runs until short.
- It is **not a multiple of 100**, so the final page is SHORT. A termination condition of `len(rows) == 0` yields four SELECTs against `len(rows) < sweepPageSize`'s three, and the assertion separates them.

**Why every row must be broken.** `gen_random_uuid()` is random, so rows cannot be planted in key order and "the row on the final page" is not addressable from the test. Asserting all 250 carry `last_error` covers a dropped final page without depending on an order the test cannot arrange. This is also why the sweep necessarily issues 250 UPDATEs in this test - it is not incidental cost that could be tuned away.

**There is no page-size seam, and that is structural rather than disciplinary.** The test file is `package schedrunner_test` and `sweepPageSize` is unexported in `package schedrunner`, so the test *cannot* reference it even by accident. The test asserts 3 against the literal 250 rows it planted. It therefore compiles and runs at HEAD, where it goes RED for the real reason. A package var lowered to 2 in the test would force the headline assertion onto a symbol absent at HEAD, and a compile failure is not a RED.

- [ ] **Step 1: Write the tracer helper and the RED test**

Create `internal/schedrunner/startup_validation_paging_integration_test.go`.

```go
//go:build integration

package schedrunner_test

import (
	"context"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// sweepTracer counts the statements the sweep issues and can fire a hook when
// one of them finishes.
//
// IT COUNTS AT TraceQueryStart AND HOOKS AT TraceQueryEnd, which is why the SQL
// is stashed in the context: TraceQueryEndData carries a CommandTag and an error
// and no SQL, and the returned context is the only channel pgx gives between the
// two calls.
//
// The mutex is not decoration. pgxpool hands out connections per acquire and
// nothing in pgx promises one goroutine.
type sweepTracer struct {
	mu sync.Mutex
	// selects counts statements matching the sweep's page read.
	selects int
	// onEnd, if set, is called with the finished statement's SQL, ON THE
	// CALLER'S GOROUTINE, after the statement has completed.
	onEnd func(sql string)
}

type sweepTracerSQLKey struct{}

func (tr *sweepTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if isSweepPageRead(d.SQL) {
		tr.mu.Lock()
		tr.selects++
		tr.mu.Unlock()
	}
	return context.WithValue(ctx, sweepTracerSQLKey{}, d.SQL)
}

func (tr *sweepTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	sql, _ := ctx.Value(sweepTracerSQLKey{}).(string)
	tr.mu.Lock()
	hook := tr.onEnd
	tr.mu.Unlock()
	if hook != nil {
		hook(sql)
	}
}

func (tr *sweepTracer) selectCount() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.selects
}

// isSweepPageRead matches the sweep's read STRUCTURALLY, never by statement
// name. The name is what this slice changes, and a matcher keyed on the new name
// reports 0 before the change - which is also what a test that never reached the
// sweep reports. Matching on the shape reports 1 before and 3 after, so the RED
// distinguishes "unpaged" from "did not run".
//
// The three fragments together exclude every other statement the sweep's
// package issues: ListEligibleScheduledJobs and
// ListOverdueScheduledJobsForCatchup carry no `ORDER BY id`, the API list
// statements order by `sj.<col>` and carry no bare `WHERE enabled`, and
// RecordScheduledJobFailure is an UPDATE.
func isSweepPageRead(sql string) bool {
	return strings.Contains(sql, "FROM scheduled_jobs") &&
		strings.Contains(sql, "WHERE enabled") &&
		strings.Contains(sql, "ORDER BY id")
}

// tracedPool builds a second pool onto the SAME database the harness migrated,
// with tr attached.
//
// h.pool.Config() is documented as returning a copy, and Config.Copy() deep
// copies ConnConfig and carries the createdByParseConfig flag NewWithConfig
// panics without - so this needs no DSN, and in particular needs no change to
// runner_test.go. It is also the only formulation that stays correct if the
// harness moves from a container per test to one database per test on a shared
// server, because the pool's own config is the per-test database by
// construction.
func tracedPool(t *testing.T, h *runnerHarness, tr *sweepTracer) *pgxpool.Pool {
	t.Helper()
	cfg := h.pool.Config()
	cfg.ConnConfig.Tracer = tr
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(p.Close)
	return p
}

// seedBrokenSchedules plants n enabled schedules whose stored spec no longer
// validates, in ONE statement.
//
// next_run_at is far in the future for the reason TestValidateStoredSpecsOnStartup
// gives: neither ListEligibleScheduledJobs nor ListOverdueScheduledJobsForCatchup
// can reach these rows, so a pass is attributable to the sweep.
func seedBrokenSchedules(t *testing.T, h *runnerHarness, owner pgtype.UUID, n int) {
	t.Helper()
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO scheduled_jobs (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at)
		SELECT 'paged-' || g, $1, '@hourly', 'UTC', $2::jsonb, 'skip', TRUE, NOW() + INTERVAL '720 hours'
		FROM generate_series(1, $3) AS g`,
		owner, makeOverBudgetSpecJSON(t), n)
	require.NoError(t, err)
}

// TestValidateStoredSpecsOnStartup_ReadsInPagesOfOneHundred pins that the sweep
// reads the enabled set in pages rather than in one unbounded statement, and
// that every row on every page - including the SHORT last one - is recorded.
//
// 250 IS THE DISCRIMINATING INPUT and each of its three properties is load
// bearing: it is more than one page, more than two pages, and not a multiple of
// the page size, so the final page is short. Any input below 101 rows behaves
// identically on paged and unpaged code.
//
// THE LITERAL 3 IS DELIBERATE AND SO IS THE ABSENCE OF A SEAM. sweepPageSize is
// unexported and this is an external test package, so the count cannot be
// derived from the thing under test. 250 rows at 100 per page is 100 + 100 + 50.
//
// IT ALSO PINS THE TERMINATION CONDITION. `len(rows) < sweepPageSize` gives 3;
// `len(rows) == 0` gives 4. A reader who changes one must change the other and
// re-read the statement's header, which is the point.
func TestValidateStoredSpecsOnStartup_ReadsInPagesOfOneHundred(t *testing.T) {
	// One log line per recorded failure, and there are 250 of them.
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })

	h := newRunnerHarness(t)
	owner := h.createUser(t, "paged-sweep@example.com")
	seedBrokenSchedules(t, h, owner, 250)

	tr := &sweepTracer{}
	q := store.New(tracedPool(t, h, tr))

	// BOUNDED FAILURE. A cursor that fails to advance is an infinite loop, and a
	// hang is indistinguishable from infrastructure trouble. Under a deadline
	// that mutant fails as a named timeout instead of consuming the package
	// clock.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	require.NoError(t, schedrunner.ValidateStoredSpecsOnStartup(ctx, q))

	require.Equal(t, 3, tr.selectCount(),
		"250 enabled rows at 100 per page is three reads: 100, 100, then a short 50. "+
			"1 means the sweep still materializes the whole enabled set in one statement")

	var recorded int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scheduled_jobs WHERE last_error IS NOT NULL`).Scan(&recorded))
	require.Equal(t, 250, recorded,
		"THE POSITIVE ASSERTION. Without it a sweep that dropped the final page, or stopped after "+
			"one page, would still satisfy a statement count alone")
}
```

**Notes for the engineer, all of which you must check rather than assume:**

- `pgtype` must be imported for `seedBrokenSchedules`'s `owner` parameter: `"github.com/jackc/pgx/v5/pgtype"`. `h.createUser` returns `pgtype.UUID`.
- `makeOverBudgetSpecJSON` lives in `internal/schedrunner/stored_spec_bounds_test.go`, same package, `//go:build integration`. It produces 77 bytes: `{"name":"legacy","tasks":[{"command":["echo","hi"],"name":"t","retries":50}]}`. 250 rows is about 19 KB of `job_spec` in total.
- `$2::jsonb` takes `[]byte`. If pgx complains, pass `string(makeOverBudgetSpecJSON(t))`.
- `generate_series(1, $3)` needs `$3` typed; if Postgres cannot infer it, write `$3::int`.
- The `log.SetOutput` pattern with `log.Writer()` and `t.Cleanup` follows `cmd/relay-server/counters_wiring_test.go`.

- [ ] **Step 2: Run the test at HEAD and record the RED**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-sweep-paging
go test -tags integration -p 1 ./internal/schedrunner/... -run TestValidateStoredSpecsOnStartup_ReadsInPagesOfOneHundred -v -timeout 300s
```

Expected: FAIL, at the first `require.Equal`, reporting `expected: 3 / actual: 1`.

**The number 1 is the check on the instrument, not just the outcome.** If you see `actual: 0`, the matcher is wrong - most likely it is keyed on a statement name - and the RED is not the one this test claims. Fix the matcher and re-run before going further. If you see `actual: 250`, the matcher is catching the UPDATEs. Record the number you actually saw.

Requires Docker. Do not run the whole integration lane yet.

- [ ] **Step 3: Rewrite the SQL statement**

In `internal/store/query/scheduled_jobs.sql`, replace the whole `ListEnabledScheduledJobs` block (its comment and its three SQL lines) with:

```sql
-- name: ListEnabledScheduledJobsPage :many
-- ONE PAGE of enabled schedules for schedrunner.ValidateStoredSpecsOnStartup,
-- keyset-paged on the primary key.
--
-- EVERY enabled schedule, not just the overdue ones. The startup sweep's whole
-- point is the schedules NEITHER existing loop sees: ListEligibleScheduledJobs
-- and ListOverdueScheduledJobsForCatchup both require next_run_at to have
-- passed, so a healthy-looking @monthly schedule broken by a retroactive
-- validation change stays invisible for up to a month after the fix deploys.
--
-- ORDER BY id IS THE CURSOR'S ORDER. It is not a test convenience, which is what
-- this comment used to call it. Postgres compares uuid bytewise, so it is a
-- total order and `id > cursor_id` is a well-defined range served by the primary
-- key index. No statement in this file writes id, so the key is immutable and
-- the paging is skip-free and duplicate-free over rows that exist for the whole
-- sweep.
--
-- THE LIMIT IS EXACT: at most page_limit rows, and the caller detects the end by
-- a SHORT page. Adding a `+ 1` - the shape the API list statements use, where a
-- client needs a NextCursor and must know whether a further page exists - makes
-- the last full page indistinguishable from a short one.
--
-- cursor_set, NOT A ZERO-UUID SEED. A pgtype.UUID zero value has Valid: false,
-- which encodes as SQL NULL, and `id > NULL` is NULL, so the first page would
-- return no rows and the sweep would silently do nothing at all: no error, no
-- log line. Same failure shape as an epoch-fenced query called with a zero-value
-- epoch.
--
-- WHAT A CONCURRENT WRITER DOES TO A SWEEP IN PROGRESS:
--   DELETE - safe. A keyset cursor is a value in the key space, not a row
--     offset, so removing a row before the cursor cannot shift a later row into
--     or out of a page. With OFFSET n it would, silently. That is why OFFSET is
--     rejected here.
--   INSERT - id defaults to gen_random_uuid(), which is RANDOM, not monotonic,
--     so a new row lands uniformly at random relative to the cursor and is seen
--     or missed in proportion to how much of the key space is left. There are no
--     appends and nothing here is "stable for appends". Missing one is harmless:
--     it arrived through a route that ran jobspec.Validate from the same binary
--     generation the sweep is applying, so this binary's retroactive rule cannot
--     have broken it.
--   enabled flipped - a row disabled after being read is still processed, as it
--     was under the single unpaged read. A row enabled mid-sweep is seen if its
--     id sorts above the cursor and missed otherwise; under the unpaged read it
--     was always missed. Paging can only see MORE rows, never fewer.
SELECT * FROM scheduled_jobs
 WHERE enabled
   AND (NOT @cursor_set::bool OR id > @cursor_id::uuid)
 ORDER BY id
 LIMIT @page_limit::int;
```

Keep `SELECT *`. Narrowing the column list is declined in the spec's section 3: `job_spec` is the term that dominates a row and is load-bearing twice (the validator reads it, `RecordScheduledJobFailure`'s fence sends it back), the ten unused columns are order 1 KB against order 1 MiB, and a narrowed row struct would sit outside `TestScheduledJobRowStillCarriesNoFailureSurface`.

- [ ] **Step 4: Regenerate, and check the toolchain before trusting the diff**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-sweep-paging
git status --porcelain -- internal/store   # must be empty before you start
sqlc version
make generate
```

`make generate` runs `sqlc generate` AND `buf generate`. `sqlc` is not version-pinned in `sqlc.yaml`. If the regeneration changes generated files in ways unrelated to `scheduled_jobs.sql` - different helper shapes, different comment formatting, changes under the protobuf output - you are looking at a toolchain bump, not at this slice. **STOP and report it. Do not commit a toolchain bump inside this slice.**

- [ ] **Step 5: CRLF hygiene, and then re-verify the generated file survived it**

This is the step that has silently discarded a regenerated `.sql.go` before. Do all of it, in order.

```powershell
# 1. The real content change. --ignore-all-space hides pure line-ending churn.
git diff --ignore-all-space -- internal/store

# 2. The files git actually considers modified. This list is LONGER than the one
#    above, by design: core.autocrlf makes `git diff` normalize LF churn away
#    while `git status` still lists the files. NEVER conclude "nothing to revert"
#    from `git diff` alone.
git status --porcelain -- internal/store internal/pb

# 3. Revert every file whose only change is line endings. Keep
#    internal/store/scheduled_jobs.sql.go.
git checkout -- <each LF-only path>

# 4. RE-VERIFY THE GENERATED FILE. The revert in step 3 is how a regenerated
#    .sql.go gets thrown away without a word.
Select-String -Path internal/store/scheduled_jobs.sql.go -Pattern "ListEnabledScheduledJobsPageParams"
Select-String -Path internal/store/scheduled_jobs.sql.go -Pattern "const listEnabledScheduledJobs ="
```

The first `Select-String` **must** hit. The second **must not** - note it is written with the trailing ` =` on purpose, because `listEnabledScheduledJobsPage` contains `listEnabledScheduledJobs` as a substring and a bare search would hit the new constant.

```powershell
# 5. Line endings and proportionality.
git ls-files --eol internal/store/scheduled_jobs.sql.go   # must read i/lf
git diff --stat -- internal/store
```

The diffstat for `scheduled_jobs.sql.go` should be on the order of tens of lines (one const, one params struct, one renamed function, one doc comment), not hundreds. A four-figure insertion count means a line-ending or encoding accident, not a rename.

```powershell
# 6. It compiles as SQL and as Go.
go build ./...
```

`go build ./...` will FAIL here with `q.ListEnabledScheduledJobs undefined`. That is expected and correct: `startup_validation.go` is the statement's only production caller and Step 6 fixes it. Confirm the failure names only that one call site.

**Expected generated shapes, which you should confirm rather than assume** (derived from `ListOverdueAssignedTasksParams`, where `sqlc.arg(max_rows)::int` emits `MaxRows int32`, and from `ListScheduledJobsPageByCreatedAscParams`, where `@cursor_set::bool` emits `CursorSet bool` and `@cursor_id::uuid` emits `CursorID pgtype.UUID`):

```go
type ListEnabledScheduledJobsPageParams struct {
	CursorSet bool        `json:"cursor_set"`
	CursorID  pgtype.UUID `json:"cursor_id"`
	PageLimit int32       `json:"page_limit"`
}
```

If sqlc emits a different field name or type, use what it emitted and adjust Step 6.

- [ ] **Step 6: Add the constant and the page loop**

In `internal/schedrunner/startup_validation.go`, add `"github.com/jackc/pgx/v5/pgtype"` to the imports, and above `ValidateStoredSpecsOnStartup` add:

```go
// sweepPageSize is how many rows ValidateStoredSpecsOnStartup holds at once.
//
// IT IS NOT BatchLimit AND MUST NOT BE ALIASED TO IT. BatchLimit governs how
// many rows one tick holds LOCKED - ListEligibleScheduledJobs is FOR UPDATE SKIP
// LOCKED inside a transaction. This sweep takes no locks and runs once; its
// limit governs peak resident bytes. Two independent policies behind one number
// makes one of the two comments false the first time either moves.
//
// THE PAGE SIZE IS THE LEVER FOR PEAK BYTES, NOT THE COLUMN LIST. job_spec
// dominates a row - it is bounded only by maxBodyBytes, 1 MiB - and it is
// load-bearing twice, since the validator reads it and
// RecordScheduledJobFailure's fence sends it back, so it cannot be dropped. At
// 100 the sweep's peak resident row set is one tick's batch, which the runner
// already sustains every TickInterval for the life of the process. That is the
// argument for the number, not a claim that 100 MiB is comfortable.
//
// A CONSTANT, NOT AN ENV VAR. The configurable-timeout convention is about waits
// whose right value depends on the operator's data. No operator has information
// the code lacks about how many rows to hold at once; if the ceiling moves it
// should move for everyone, in a commit that says why.
const sweepPageSize = 100
```

Then replace the function body's read and loop. Everything between `text, ok := recordableFailure(...)` and the `log.Printf("schedrunner: startup validation recorded a new failure...")` line is **unchanged** - including its three `continue` statements, which still target the row loop:

```go
func ValidateStoredSpecsOnStartup(ctx context.Context, q *store.Queries) error {
	var (
		cursor    pgtype.UUID
		cursorSet bool
	)
	for {
		rows, err := q.ListEnabledScheduledJobsPage(ctx, store.ListEnabledScheduledJobsPageParams{
			CursorSet: cursorSet,
			CursorID:  cursor,
			PageLimit: sweepPageSize,
		})
		if err != nil {
			return err
		}
		for _, row := range rows {
			// ... the existing body, unchanged ...
		}
		// A SHORT PAGE IS THE END, not an empty one. On a table whose enabled
		// count is an exact multiple of sweepPageSize this costs one empty round
		// trip; `len(rows) == 0` costs the same trip on every table and reads as
		// if a full page could be the last one.
		if len(rows) < sweepPageSize {
			break
		}
		cursor = rows[len(rows)-1].ID
		cursorSet = true
	}
	return nil
}
```

- [ ] **Step 7: Run the test and confirm GREEN**

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -run TestValidateStoredSpecsOnStartup_ReadsInPagesOfOneHundred -v -timeout 300s
```

Expected: PASS. Then run the whole package to confirm nothing else moved:

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -timeout 900s
```

Expected: all PASS, including `TestValidateStoredSpecsOnStartup`, both fence tests, and the untagged surface guard.

- [ ] **Step 8: Rewrite the two comment paragraphs paging falsifies**

Prose that becomes false on this commit is a defect on this project, so this lands in the same commit as the behaviour change.

First, copy the file so the mutation battery in Step 9 can restore it:

```powershell
Copy-Item internal/schedrunner/startup_validation.go "$env:TEMP/claude/.../scratchpad/laneE-startup_validation.go.bak"
```

**8a. The `Cost:` paragraph.** Replace:

```go
// Cost: one pass over N enabled schedules at boot, with no I/O per row beyond
// the read that lists them and one UPDATE per BROKEN row.
```

with:

```go
// Cost: for N enabled schedules that do not change during the pass,
// floor(N/sweepPageSize)+1 SELECTs and one UPDATE per BROKEN row, with peak
// resident rows of one page rather than N.
```

The `+1` is exact rather than defensive: a table whose enabled count is an exact multiple of the page size costs one extra empty round trip, which is what the short-page termination buys.

**8b. The `THAT IS NARROWER THAN "THIS SWEEP CANNOT STOP THE BOOT"` paragraph.** It currently names the unbounded read as the hole and defers paging to the backlog item. Replace the whole paragraph with a statement of what is now true and what is still not:

```go
// THAT IS NARROWER THAN "THIS SWEEP CANNOT STOP THE BOOT", and the gap is in
// FRONT of the loop rather than inside it. The read is paged, so peak memory and
// per-statement work are bounded by sweepPageSize. THE SWEEP'S TOTAL WALL CLOCK
// IS STILL PROPORTIONAL TO THE NUMBER OF ENABLED SCHEDULES, and nothing here
// bounds that number: paging converted an unbounded allocation into an unbounded
// duration. The caller runs this before srv.ListenAndServe(), so a large enough
// scheduled_jobs table still delays the boot, and the HTTP API an operator would
// use to delete the offending rows is exactly what never comes up. What bounds
// the number is a per-owner schedule cap, which does not exist:
// docs/backlog/<the cap item's filename>.
```

**Before committing this paragraph, verify the cited file exists:**

```powershell
Test-Path docs/backlog/<the cap item's filename>
```

If it returns `False`, **STOP and report to the conductor**. Task 0 exists precisely so this citation is not a promise. Do not substitute a description in place of a filename and do not cite the boot-sweep item, which this slice closes.

Do **not** touch the `ITS WRITE IS FENCED ON THE GENERATION IT VALIDATED` paragraph. Its claim - that `q` is pool-backed so the read and every UPDATE are separate implicit transactions - is exactly as true with three page reads as with one.

- [ ] **Step 9: Mutation battery**

Confirm the green baseline first (Step 7 passed). Restore from `laneE-startup_validation.go.bak` after each mutation; **never** `git checkout --`, which would discard the Step 8 comment work. After each edit, re-read the mutated line to confirm it applied.

Run after each: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestValidateStoredSpecsOnStartup_ReadsInPagesOfOneHundred -timeout 300s`

**M1 goes first because it is the spec's named hazard and because a mutation that makes the sweep exit early would mask it if it ran later.**

| # | Mutation | Must die on | Expected observation |
| --- | --- | --- | --- |
| M1 | Seed `CursorSet: true` on the first page with the zero-value `cursor` | assertion 1 and 2 | `id > NULL` is NULL, first page returns 0 rows, sweep does nothing. `selects` = 1, `recorded` = 0. This is the silent-no-op the statement's comment warns about. |
| M2 | Delete the outer `for {}`; issue one page and return | assertion 1 and 2 | `selects` = 1, `recorded` = 100 |
| M3 | Change termination to `if len(rows) == 0 { break }` | assertion 1 | `selects` = 4 |
| M4 | Never advance: delete `cursor = rows[len(rows)-1].ID` | the 60s deadline | Infinite loop; the sweep returns `context.DeadlineExceeded` from the page query and `require.NoError` fails as a NAMED timeout rather than by consuming the package clock. This is what the deadline is for. |
| M5 | Never set `cursorSet = true` | the 60s deadline | Same shape as M4: page 1 forever. |
| M6 | `PageLimit: 250` | assertion 1 | `selects` = 2 |

**Control (must die, proving the harness is live):** change `250` to `50` in `seedBrokenSchedules`'s call. Assertion 1 goes `1 != 3`. If this survives, the test is not running the code you think it is.

Every mutation above is killed by a test that stays in the tree. There is nothing to leave behind separately.

- [ ] **Step 10: Full local gates**

```powershell
go vet -tags integration ./...
go build ./...
go test ./... -timeout 300s
go test -tags integration -p 1 ./internal/schedrunner/... ./internal/store/... -timeout 900s
```

`go vet -tags integration ./...` is what CI's `make vet-integration` runs, and it is the only CI step that sees the new test file today.

- [ ] **Step 11: Commit**

```bash
git add internal/store/query/scheduled_jobs.sql internal/store/scheduled_jobs.sql.go internal/schedrunner/startup_validation.go internal/schedrunner/startup_validation_paging_integration_test.go
git commit -F <scratchpad file>
```

Commit message body (the argument goes here, not in the code):

- The page size is 100 and is not `BatchLimit`: `BatchLimit` governs a lock window, this governs peak resident bytes, and aliasing them would make `BatchLimit`'s own comment false.
- `gen_random_uuid()` is UUIDv4 and therefore random, not monotonic. There are no appends; a row inserted mid-sweep lands uniformly at random relative to the cursor. Missing one is harmless because it came through a route that ran `jobspec.Validate` from the binary generation the sweep is applying.
- The fence is unaffected and the window it cares about SHRINKS: the maximum per-row read-to-write span falls from O(N) to O(page), because a row is now read at the start of its own page and written within it.
- No `+ 1` on the LIMIT: the sweep detects the end by a short page, so a `+ 1` would make the last full page indistinguishable from a short one.
- The RED was measured: one matching SELECT before, three after, with a structural matcher that is blind to the statement's name.

---

## Task 2: Return on cancellation instead of logging one line per remaining broken row

**Files:**
- Modify: `internal/schedrunner/startup_validation.go` (the row loop; the `A PER-ROW FAILURE` paragraph's closing clause)
- Modify: `internal/schedrunner/startup_validation_paging_integration_test.go` (add one test and one tracer hook)
- Modify: `cmd/relay-server/main.go` (the call site's `A FAILURE HERE MUST NOT STOP THE BOOT` paragraph)

### The property this task pins, and why the input discriminates

**Property:** once `ctx` is cancelled mid-sweep, the sweep RETURNS `ctx.Err()` rather than running every remaining broken row into `RecordScheduledJobFailure` and logging one line each.

**The item's own sentence needed correcting, and the spec is right about it.** The item says "on a shutdown mid-sweep every remaining row currently logs its own `context canceled` line". Half right: `validateStoredRow` does no I/O, so a cancelled sweep runs through HEALTHY rows silently at memory speed. Only BROKEN rows reach the record call and log. The conclusion survives, because the sweep's worst case IS "most rows broken" - that is the release that lands a new retroactive rule, which is the scenario the sweep exists for.

**Discriminating input: three broken rows, and a cancellation that lands between row 1 and row 2.**

- Three rows, not 250. This property is independent of the page size, and a container spin dominates the cost anyway.
- The cancellation MUST land mid-sweep. **Cancelling before the sweep does not discriminate**: the first page query itself fails and both HEAD and the fixed code return a non-nil error. The RED evaporates.
- The cancellation is fired from the tracer's `TraceQueryEnd` on the FIRST `RecordScheduledJobFailure`. That is deterministic and race-free: `TraceQueryEnd` runs on the caller's goroutine after `Conn.Exec` has completed (`pgx@v5.9.1/conn.go`, `Exec` calls `c.exec` and then the tracer), so row 1's write lands, and the sweep's next loop iteration is the first thing that runs afterwards. No sleep, no poll, no goroutine, and no data race on the log buffer.
- The hook may key on the statement NAME (`-- name: RecordScheduledJobFailure :execrows`, which sqlc keeps as the first line of the emitted const). That is safe here and was not safe for Task 1's counter: this statement is not being renamed, and the hook is a fixture rather than the RED.

**What HEAD does:** row 1 records; the tracer cancels; row 2's `RecordScheduledJobFailure` gets `context.Canceled` back, hits `log.Printf("schedrunner: startup validation record for %s: %v", ...)` and `continue`s; row 3 the same. The function reaches its `return nil`. **HEAD returns nil, and that is the RED.**

- [ ] **Step 1: Write the RED test**

Add to `internal/schedrunner/startup_validation_paging_integration_test.go`. Add `"bytes"` and `"errors"` to the imports if not already present.

```go
// TestValidateStoredSpecsOnStartup_ACancelledSweepReturnsInsteadOfLoggingEveryRow
// pins the sweep's behaviour under a mid-pass shutdown.
//
// THE CANCELLATION MUST LAND MID-SWEEP OR THE TEST PROVES NOTHING. Cancelling
// before the call makes the first page query itself fail, so a sweep with no
// ctx.Err() check also returns an error and the assertion below cannot
// distinguish them. The tracer fires on the END of the first
// RecordScheduledJobFailure, which pgx calls on this goroutine after the Exec
// has completed - so row 1's write lands and the row loop's next iteration is
// the very next thing to run.
//
// THREE ROWS, NOT 250. This property is independent of the page size.
func TestValidateStoredSpecsOnStartup_ACancelledSweepReturnsInsteadOfLoggingEveryRow(t *testing.T) {
	var logged bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(prev) })

	h := newRunnerHarness(t)
	owner := h.createUser(t, "cancelled-sweep@example.com")
	seedBrokenSchedules(t, h, owner, 3)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := &sweepTracer{}
	var once sync.Once
	tr.onEnd = func(sql string) {
		if strings.Contains(sql, "-- name: RecordScheduledJobFailure") {
			once.Do(cancel)
		}
	}
	q := store.New(tracedPool(t, h, tr))

	err := schedrunner.ValidateStoredSpecsOnStartup(ctx, q)

	// THE RED. Before this change the sweep ran the remaining rows into a
	// cancelled context, logged each rejection and returned nil, so a shutdown
	// mid-sweep was indistinguishable at the call site from a clean pass.
	require.ErrorIs(t, err, context.Canceled,
		"a cancelled sweep must return the cause once, not swallow it")

	// THE EXPOSURE THE ITEM NAMES. One line per remaining BROKEN row. The
	// success line reads "startup validation recorded a new failure for
	// schedule", which does not contain this needle.
	require.Equal(t, 0, strings.Count(logged.String(), "startup validation record for "),
		"after cancellation no further row may reach RecordScheduledJobFailure, so none may log its rejection")

	// ANTI-VACUITY. Without this, a sweep that returned before doing any work at
	// all would satisfy both assertions above.
	var recorded int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scheduled_jobs WHERE last_error IS NOT NULL`).Scan(&recorded))
	require.Equal(t, 1, recorded,
		"exactly the one row processed before the cancellation must be recorded")
}
```

`tr.onEnd` is written before the traced pool is used and read under the tracer's mutex, so there is no race on the field. If the race detector disagrees when you run the container lane under `-race`, set `onEnd` through a small mutex-guarded setter rather than by direct field assignment.

- [ ] **Step 2: Run the test and record the RED**

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -run TestValidateStoredSpecsOnStartup_ACancelledSweepReturnsInsteadOfLoggingEveryRow -v -timeout 300s
```

Expected: FAIL on the first assertion, reporting that `err` is `<nil>` and does not match `context.Canceled`. If instead it fails on the log count with 2, the first assertion unexpectedly passed and you must find out why before continuing - most likely the cancellation landed before the first page query.

- [ ] **Step 3: Add the `ctx.Err()` check**

In `internal/schedrunner/startup_validation.go`, at the **top of the ROW loop** (not the page loop):

```go
		for _, row := range rows {
			// A CANCELLED SWEEP RETURNS RATHER THAN RUNNING ON. Every remaining
			// BROKEN row would otherwise reach RecordScheduledJobFailure, get
			// context canceled back, and log its own line - and "most rows
			// broken" is the case this sweep exists for, since that is the
			// release that lands a new retroactive rule. At the top of the ROW
			// loop rather than the page loop, because one page of broken rows is
			// already up to sweepPageSize lines. RETURN rather than break, so the
			// caller names the cause once.
			if err := ctx.Err(); err != nil {
				return err
			}

			text, ok := recordableFailure(validateStoredRow(row))
			// ... rest unchanged ...
		}
```

The page loop needs no separate check: a cancelled context makes the next page query fail, and that error is already returned.

- [ ] **Step 4: Run and confirm GREEN**

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -timeout 900s
```

Expected: all PASS. In particular Task 1's paging test must still pass - it runs under a 60s deadline that is never reached on a healthy pass, so `ctx.Err()` returns nil for all 250 rows.

- [ ] **Step 5: Fix the two prose clauses this change falsifies**

**5a. `internal/schedrunner/startup_validation.go`.** In the `A PER-ROW FAILURE MUST NOT STOP THE SERVER BOOTING` paragraph, replace:

```
// line rather than the remaining schedules; the only returned error is the list
// query's, which the caller in cmd/relay-server logs as a warning.
```

with:

```
// line rather than the remaining schedules. Two things ARE returned: a page
// query's error, and the cancellation the row loop checks for. The caller in
// cmd/relay-server logs either as a warning.
```

**5b. `cmd/relay-server/main.go`,** in the `A FAILURE HERE MUST NOT STOP THE BOOT` paragraph above the `ValidateStoredSpecsOnStartup` call. Replace:

```
	// A FAILURE HERE MUST NOT STOP THE BOOT. Per-row record failures are logged
	// inside and the sweep continues; the list query's error is logged here as a
	// warning and the server carries on. Turning a schedule problem into a server
	// that will not start would be worse than the invisibility this closes.
```

with:

```
	// A FAILURE HERE MUST NOT STOP THE BOOT. Per-row record failures are logged
	// inside and the sweep continues; a page query's error and a mid-sweep
	// cancellation are returned and logged here as a warning, and the server
	// carries on. Turning a schedule problem into a server that will not start
	// would be worse than the invisibility this closes.
```

This site is not in the spec's file list. It carried the same singular "the list query's error" and is falsified twice over: the read is now several statements, and `ctx.Err()` is a returned error that is not a query error at all.

Leave the rest of that comment alone. Its "BEFORE the runner goroutine" and "THAT ARGUMENT COVERS ONE PROCESS AND ONLY ONE" paragraphs are unaffected by paging.

- [ ] **Step 6: Mutation battery**

Green baseline confirmed in Step 4. Restore from a scratchpad copy, not `git checkout --`.

Run after each: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestValidateStoredSpecsOnStartup_A -timeout 300s`

| # | Mutation | Must die on | Expected observation |
| --- | --- | --- | --- |
| M1 | Delete the `if err := ctx.Err(); err != nil { return err }` block | assertion 1 and 2 | `err` is nil; the log buffer holds 2 `record for ` lines. This is HEAD's behaviour. |
| M2 | Change `return err` to `break` | assertion 1 | Leaves the row loop, then `len(rows) < sweepPageSize` breaks the page loop, then `return nil`. `err` is nil. This is why the spec says return, not break. |
| M3 | Move the check to the top of the PAGE loop instead | assertion 2 | Rows 2 and 3 are still processed under a cancelled context; 2 `record for ` lines. `err` may or may not be nil depending on the page count, which is exactly why assertion 2 is separate from assertion 1. |

**Control (must die):** change `require.Equal(t, 1, recorded)` to `require.Equal(t, 2, recorded)`. If it passes, the anti-vacuity leg is not measuring what you think.

- [ ] **Step 7: Full local gates**

```powershell
go vet -tags integration ./...
go build ./...
go test ./... -timeout 300s
go test -tags integration -p 1 ./internal/schedrunner/... -timeout 900s
```

- [ ] **Step 8: Commit**

```bash
git add internal/schedrunner/startup_validation.go internal/schedrunner/startup_validation_paging_integration_test.go cmd/relay-server/main.go
git commit -F <scratchpad file>
```

Commit message body:

- Corrects the item's claim: one line per remaining BROKEN row, not per remaining row. `validateStoredRow` does no I/O, so healthy rows run silently at memory speed; only broken rows reach the record call.
- The conclusion survives the correction, because the sweep's worst case IS "most rows broken" - that is the release that lands a new retroactive rule.
- Return, not break, so the caller's existing warning names the cause once.
- Top of the ROW loop, not the page loop: one page of broken rows is already up to 100 lines.
- The RED was measured: HEAD returns nil from a cancelled sweep and logs one rejection per remaining broken row.

---

## Task 3: Amend the backlog item

**Files:**
- Modify: `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md`

Doc-only. No test. Written last so amendment 5 describes what actually shipped.

- [ ] **Step 1: Apply the six amendments**

**1. Rewrite Proposal half 2 as the per-owner cap alone, and record the rate limit as DECIDED OUT.** Replace the current half 2 with:

```markdown
2. A per-owner schedule cap. Filed separately: [[<the cap item's filename without extension>]].

   **A rate limit on `POST /v1/scheduled-jobs` is DECIDED OUT for this item**, on two
   arguments, both checked against the tree:
   - A creation-rate limit bounds growth RATE, not table SIZE, and size is what breaks the
     boot. `RateLimit` is a per-IP token bucket over a window: it changes how long an actor
     needs to reach N schedules and puts no ceiling on N. The sweep's cost is a function of N
     alone. Shipping it and recording "bounded" here would be a control that reads as a fix
     for a property it does not have.
   - No HTTP rate limit anywhere bounds a schedule's FIRING. `fireOne` has exactly one
     caller, `Runner.TickOnce`, reached from `Runner.Run` on the goroutine
     `cmd/relay-server` starts. It never touches an HTTP route.
     (`handleRunScheduledJobNow` IS an HTTP route that creates a job from a stored spec, but
     it is a separate path with its own `jobspec.Validate` call, not `fireOne`. Whether
     run-now wants a rate limit is a different question on a different route.)
```

This satisfies the item's third acceptance criterion for the rate limit. Not "deferred" - decided.

**2. Correct half 1's `ctx.Err()` sentence.** Replace "on a shutdown mid-sweep every remaining row currently logs its own `context canceled` line" with: "on a shutdown mid-sweep every remaining BROKEN row logs its own `context canceled` line. `validateStoredRow` does no I/O, so healthy rows run silently at memory speed; only broken rows reach `RecordScheduledJobFailure`. The count still matters, because the sweep's worst case is 'most rows broken' - the release that lands a new retroactive rule, which is the scenario the sweep exists for."

**3. Correct the option-3 paragraph.** Replace "the current no-lock argument depends on the sweep completing before the runner starts, so making it async requires the row-generation predicate to be in place first" with: "the no-lock argument was already retired: `cmd/relay-server`'s own comment records that 'no lock is needed for a pass that runs while nothing else is running' is false the moment a second replica exists, and what makes the sweep safe is `RecordScheduledJobFailure`'s content fence, which is placement-independent. The real reasons to defer async placement are that it changes when a boot reports ready and that it puts the sweep's UPDATEs in contention with `TickOnce`'s row locks. Neither is a correctness objection; both are behaviour changes wanting their own slice."

**4. Note that the second acceptance criterion was already met before this slice.** Add under `## Acceptance / Done When`, against "The sweep's doc comment states the property it actually has": "Already met at the time this slice was scoped - the `THAT IS NARROWER THAN` paragraph was accurate at HEAD. A criterion green before the change pins nothing, so this slice did not treat it as work. What it created instead was the obligation to keep it true: five prose sites were rewritten because paging or the `ctx.Err()` return falsified them."

**5. Restate the first acceptance criterion honestly.** Replace "The boot sweep's memory and query cost is proportional to a page, not to the table" with:

```markdown
- The boot sweep's MEMORY and PER-STATEMENT cost is proportional to a page, not to the
  table. **Met.** Its DURATION is not, and this item must not read as if the exposure is
  closed: paging converted an unbounded ALLOCATION into an unbounded DURATION. An actor with
  a million enabled schedules still delays the boot by O(N) round trips before
  `ListenAndServe`, and the HTTP API an operator would use to delete them still comes up
  last. What bounds N is the per-owner cap, which is not here.
```

**6. Confirm the per-owner cap item is filed.** Task 0 did this. Verify with `Test-Path` and use the real filename in amendments 1 and 5.

- [ ] **Step 2: Verify the edit did not damage the file**

Programmatic edits to tracked text files on this repo have three known failure modes and none of the three checks sees the other two.

```powershell
git diff --stat -- docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md
git ls-files --eol docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md
```

The diffstat must be proportionate to the amendments you intended - a three-figure insertion count on a 77-line file means a line-ending accident. `git ls-files --eol` must read `i/lf`. And the amendments above are pure ASCII: if any non-ASCII byte appears in the diff, you did not put it there and the file has stopped being what you think it is.

- [ ] **Step 3: Commit**

```bash
git add docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md
git commit -m "docs: amend the boot-sweep item for what half 1 actually closed"
```

- [ ] **Step 4: Hand back to the conductor for the close**

**Do not close the item by hand.** Report to the conductor that the amendments are committed and the item is ready for `/backlog close bug-2026-08-28-boot-sweep`. That command does the `git mv` into `docs/backlog/closed/`, stamps the `status`/`closed`/`resolution` frontmatter, and appends the `## Resolution` note. Flipping `status` in place leaves the file in the open directory and `/backlog list` reports it as malformed.

The resolution note should say: half 1 shipped (keyset paging plus the cancellation return); half 2 survives as the per-owner cap, filed separately; the rate limit is decided out, not deferred; and the exposure is **narrowed, not closed** - the sweep's duration is still O(N).

---

## Verification budget

For Phase 4 planning. Measured inputs, not guesses about them:

- `internal/schedrunner`'s integration lane is ~25s today (`ROADMAP.md` records "~26s"; the brief records 24.9s), across the 12 tests that call `newRunnerHarness`, so ~2.1s per test dominated by the container spin and `store.Migrate`.
- This plan adds two harnesses: **+~4.2s of container**, plus the 250-row test's own database work.
- That work is 1 seeding statement (`generate_series`, one round trip) + 3 SELECTs + **250 UPDATEs**, all sequential. At 0.5-2 ms per round trip against a Docker-published port that is **0.13-0.5s**. The 250 UPDATEs are irreducible: every row must be broken because uuid order is random and "the row on the final page" is not addressable, so the positive assertion has to be "all 250 recorded".
- Payload is negligible: `makeOverBudgetSpecJSON` is 77 bytes, so 250 rows is about 19 KB.
- **Expect the lane at roughly 29-30s, up about 18%, of which the 250 rows account for at most half a second.** Seeding 250 rows is not the cost; two containers are. Measure it rather than trusting this paragraph: `Measure-Command { go test -tags integration -p 1 ./internal/schedrunner/... -timeout 900s }` before and after.

---

## Self-review against the spec

| Spec section | Task |
| --- | --- |
| 1. The keyset page | Task 1 Step 3 |
| 2. Page size 100, not `BatchLimit`, not env-configurable | Task 1 Step 6 |
| 3. Keep `SELECT *` | Task 1 Step 3 (explicitly kept, with the reason) |
| 4. `ctx.Err()` in the row loop, return not break | Task 2 Step 3, mutation M2 |
| 5. The fence still holds | No code change; recorded in Task 1 Step 11's commit message |
| 6. Delete / insert / `enabled` flipped / no re-read | Task 1 Step 3's statement comment |
| 7. The regression test | Task 1 Steps 1-2, 7, 9 |
| 8. Comment changes this slice owes | Task 1 Step 8 (2 of 3), Task 2 Step 5 (the third), **plus 2 the spec missed**: Task 1 Step 3's `ORDER BY id` line and Task 2 Step 5b's `cmd/relay-server/main.go` clause |
| Amendments 1-6 | Task 3 Step 1 |
| Amendment 6 (file the cap item) | Task 0, sequenced first because Task 1 Step 8 cites it |
| Harness DSN exposure | **Refuted.** `h.pool.Config()` needs no harness change and is more correct under the incoming shared-Postgres CI lane. |
| `CommandTag.RowsAffected()` for SELECT | Checked: `pgconn.CommandTag.RowsAffected` parses trailing digits, so `SELECT 100` yields 100, and pgx calls `TraceQueryEnd` from `baseRows.Close()` with the populated tag. The stronger per-statement assertion is therefore **available**. It is **not used**: the statement count already kills every mutation in the battery, and a second assertion over the same instrument would add a failure mode without adding a kill. Recorded so the next reader does not re-derive it. |
