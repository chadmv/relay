# Keyset-Page and Narrow ReconcileOnStartup's Overdue Read - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `schedrunner.ReconcileOnStartup` read overdue schedules one keyset page of 100 at a time over four narrow columns instead of one unbounded `SELECT *`, and make a cancelled pass return instead of logging one line per remaining row.

**Architecture:** `ListOverdueScheduledJobsForCatchup` (no LIMIT, `SELECT *`) becomes `ListOverdueScheduledJobsForCatchupPage` - `SELECT id, name, cron_expr, timezone`, plus the `cursor_set` / `cursor_id` / `page_limit` triple this query file already uses, `ORDER BY id`, no `+ 1`. `ReconcileOnStartup` loops until a page comes back short, advancing the cursor to the last row's `id`, with a `ctx.Err()` return at the top of the ROW loop and `now` captured once above the page loop. Three new integration guards and one untagged field-set guard pin the result. Every prose site the change falsifies is corrected or deleted in the same slice.

**Tech Stack:** Go 1.26, sqlc (`sqlc.yaml`, `emit_sql_as_comment: true`), pgx/v5 (`pgxpool`, `pgx.QueryTracer`), testcontainers-go / `internal/testsupport/pgdsn`, testify, robfig/cron/v3.

**Source spec:** `docs/superpowers/specs/2026-09-10-reconcile-on-startup-paging.md` (in this worktree).
**Backlog item:** `docs/backlog/bug-2026-09-04-reconcileonstartup-lists-every-overdue-schedule-unbounded.md`.
**Worktree:** `D:/dev/relay/.claude/worktrees/lane-boot-bounds`. Every command below runs from there. Do **not** `cd D:/dev/relay`.

---

## Slice independence declaration

**This is ONE backend slice, ONE PR, ONE session. There is no frontend slice.** Phase 3 gets a single lane; there is nothing here to run in parallel.

The tasks are **strictly sequential** and must not be split across agents:

| Task | Files | Why it cannot run beside its neighbour |
| --- | --- | --- |
| 0 | none (measurement + conductor prerequisites) | Everything downstream reads its ledger. |
| 1, 2, 3 | `internal/schedrunner/reconcile_paging_integration_test.go` (new), one signature line in `internal/schedrunner/startup_validation_paging_integration_test.go` | All three tests live in one new file and share its tracer, matcher and seed helpers. |
| 4 | `internal/store/query/scheduled_jobs.sql`, `internal/store/scheduled_jobs.sql.go` | Leaves the tree not compiling on purpose. Task 5 is the only thing that repairs it. |
| 5 | `internal/schedrunner/runner.go`, `internal/schedrunner/overdue_catchup_row_surface_test.go` (new) | Compiles only against Task 4's regenerated store, and turns Tasks 1-3's REDs green. |
| 6 | `README.md`, `docs/backlog/bug-2026-08-28-...md`, two test-file comments | Doc-only, but two of its sentences describe what Task 5 shipped. Write it after. |
| 7 | none (mutation battery, restores after each) | Needs Tasks 1-6 committed and green. |
| 8 | none (full gates, hygiene, handoff) | Last. |

**This plan does NOT need `/backlog phases`.** It has no multi-session stages. It does need the conductor to file three new backlog items and to close one - see Task 0 and Task 8.

---

## What this plan refutes in the spec

The spec is well-verified: its file corrections, its four-field read analysis, its `isSweepPageRead` hazard, its no-fence argument and its no-remainder decision all hold against the tree. Six things did not survive checking. Each is corrected inline in the task that owns it; they are collected here so a reviewer diffing plan against spec is not surprised.

### 1. Guard 3's prescribed input does NOT redden the design it exists to reject. This is the important one.

The spec's Guard 3 says: "Plant more than one page of overdue enabled rows, **at least one** carrying an unparseable `cron_expr`", hedged with "plant more than one poisoned row so at least one lands early with high probability".

**Work the rejected drain loop through with one poisoned row and it terminates.** The drain loop is `SELECT ... WHERE enabled AND next_run_at < NOW() LIMIT N` repeated until fewer than N rows come back. Healthy rows are advanced and leave the predicate; poisoned rows stay. With P poisoned rows and H healthy, the loop keeps going only while a page comes back FULL, and once H is exhausted the page contains exactly P rows. **If P < N the page is short and the drain loop terminates** - having done extra work, and satisfying every assertion the spec's Guard 3 makes (nil error, healthy advanced, poisoned not). The mutant survives, and the test the spec calls "the guard that matters most" pins nothing about the design it was written to reject.

**The load-bearing property of the input is P >= `reconcilePageSize`.** With 150 poisoned rows against a page size of 100, every drain-loop page is permanently full of rows that can never be advanced, so the mutant hangs and the 60 s deadline fires. Task 2 plants 150 poisoned + 100 healthy = 250 rows.

**And Guard 3 must count page reads too.** See refutation 2, which is the second half of the same finding.

### 2. The min-cursor mutation survives the spec's Guard 2, because reconcile's predicate self-heals

The conductor's battery asks for a mutation that sets the cursor to the page MINIMUM (`rows[0].ID`) instead of the maximum. Work it through against 250 healthy overdue rows:

- Page 1 returns rows 1-100 in id order. All 100 are advanced, so they no longer satisfy `next_run_at < NOW()`. Cursor becomes `rows[0].ID`.
- Page 2 is `WHERE overdue AND id > rows[0].ID`. Rows 2-100 are excluded **by the predicate**, not by the cursor, so page 2 is rows 101-200. Page 3 is the short 50.
- **Statement count: 3. Identical to the correct code. The mutation survives.**

It survives because reconcile writes the column its own predicate selects on - the very property that made the drain loop tempting. The mutation is caught only where rows are NOT advanced: in Task 2's mixed fixture the 150 poisoned rows stay overdue, so a minimum cursor advances by one row per page and the count explodes past 3. **Task 2's test therefore asserts an exact page-read count of 3, and that assertion is what kills the min-cursor mutant.** Task 1's count assertion does not, and Task 7 records that honestly rather than claiming a kill it did not get.

### 3. The termination proof's quantity is wrong, and its concurrency case is missing

The spec argues: "The candidate key space is finite and **strictly shrinks by at least `reconcilePageSize` ids per full page**."

The key space is 2^128 uuid values. Shrinking it by 100 *ids* per page is not a termination argument. The quantity that shrinks by at least `reconcilePageSize` per full page is the number of **rows permanently retired from the candidate set**, and the argument that closes it is that the candidate set of ROWS is finite while no row is ever read twice.

The proof also omits the case the sibling's own header states for the sweep: **every page is a fresh snapshot, so a row INSERTed above the cursor mid-pass joins the work set.** For the sweep that residual is "duration amplification rather than non-termination" (`startup_validation.go`'s header says so in those words). The reconcile inherits it in a strictly weaker form, because a row must be OVERDUE to join, and no HTTP route writes a `next_run_at` that is newly in the past - `handleCreateScheduledJob` and `handlePatchScheduledJob` both compute it forward from the cron, and the PATCH's other branch preserves a value the row already had (so that row's id has not moved).

**What to do with this:** the corrected statement of the invariant goes in the SQL comment (Task 4), phrased as rows-retired rather than ids. **Do not write a claim about which routes can and cannot write a past `next_run_at` into any comment** - that is a claim about the complement of the write sites, pinned by nothing. It is recorded here, in a dated plan, and that is where it stays.

Verified for the proof's other legs: `internal/store/migrations/000006_scheduled_jobs.up.sql` declares `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`, so the cursor key is unique and NOT NULL; no statement in `scheduled_jobs.sql` writes `id`, so it is immutable. The `ParseSchedule` failure branch, the failed-UPDATE branch and the short-interval branch are all excluded by `id > cursor_id` regardless of what the loop did, exactly as the spec says.

### 4. Decision 6 changes the error contract and the spec designs no test for it

The spec adds the `ctx.Err()` row-loop return and argues at length that reconcile's exposure is worse than the sweep's - and then designs Guards 1, 2 and 3, none of which touch cancellation. A behaviour change with no RED is unpinned. **Task 3 supplies `TestReconcileOnStartup_ACancelledPassReturnsInsteadOfLoggingEveryRow`**, mirroring the sweep's existing cancellation test, and its RED is real: at HEAD reconcile returns nil and logs `reconcile advance for` once per remaining row.

### 5. The rename has THREE comment sites in live files, not two

The spec's "Required by the rename" table lists `scheduled_jobs.sql`'s `ListEnabledScheduledJobsPage` comment and `seedBrokenSchedules`'s comment. A search for the statement name outside `docs/` returns a third live site: **`internal/schedrunner/startup_validation_integration_test.go`, in `TestValidateStoredSpecsOnStartup`'s header** ("ListEligibleScheduledJobs and ListOverdueScheduledJobsForCatchup both require next_run_at to have passed"). All three are fixed here. `ROADMAP.md` also names it and is **not** hand-edited - it is regenerated by `/roadmap` from `docs/backlog/`.

### 6. The README fix is three list items, not two

The spec says "Keep the edit to these two list items" (6 and 7). But item 5 is the enrollment janitor, and in `main` the janitor goroutine starts **after** the reconcile, the sweep, the runner, the metrics sweeper and the watchdog - so swapping 6 and 7 alone leaves the list still claiming the janitor precedes the reconcile. An ordered list of checkable facts is being corrected precisely because it must be true; leaving a third false ordering in the same five lines is the partial-repair shape. **Task 6 rewrites items 5, 6 and 7 and nothing else.** Item 4's lumping of dispatcher, LISTEN/NOTIFY and watchdog straddles the reconcile in `main` and stays as it is: pre-existing, and out of scope exactly as the spec says.

### Spec claims checked and SURVIVING, recorded so nobody re-litigates them

- **`isSweepPageRead` reads exactly as the spec describes** (`internal/schedrunner/startup_validation_paging_integration_test.go`): `FROM scheduled_jobs` + `WHERE enabled` + `ORDER BY id`. The reconcile's new page read satisfies all three, so the two matchers are genuinely not interchangeable, and the spec's recommendation - a second matcher beside it - is taken. Note the two tests do not today share a traced pool with the reconcile, so reuse would not produce a wrong count *today*; the second matcher is for attribution surviving any future test that exercises both, and for a failure message that names the right statement.
- **`ReconcileOnStartup` reads exactly `ID`, `Name`, `CronExpr`, `Timezone`.** Re-read line by line: `Name` appears only in the two `log.Printf` lines. Nothing else on the row is touched.
- **The sqlc row type is not shared.** `ListOverdueScheduledJobsForCatchup` returns `[]ScheduledJob` today; narrowing gives it a statement-private row struct. No test reads the row type - the two existing reconcile tests in `runner_test.go` assert through `GetScheduledJob`.
- **`TestScheduledJobRowStillCarriesNoFailureSurface` keeps covering `store.ScheduledJob`**, which is unchanged: the sweep's statement keeps `SELECT *`.
- **This slice adds no Makefile and no workflow change, and both CLAUDE.md-required links exist.** Confirmed, not restated: `internal/schedrunner/runner_test.go` takes its DSN from `pgdsn.NewIntegrationDSN(t)`; `Makefile`'s `test-pg-integration` recipe names `./internal/schedrunner/...`; `.github/workflows/go-ci.yml`'s `pg-integration` job runs `make test-pg-integration`. An `//go:build integration` test added here executes in CI.
- **No migration.** This slice touches no schema. If you somehow conclude one is needed, **STOP and report** - LANE W is adding a migration concurrently, so a number hard-coded now will collide. Do not pick a number without re-reading `internal/store/migrations/` at that moment.

---

## The pre-listener enumeration, and where it lives

The spec's "The pre-listener query enumeration" table is the durable record of every statement reachable from `main` before the `srv.ListenAndServe()` goroutine, and it is a dated design record - it does not drift. **Do not copy it into a code comment.** A census of other code in a comment is exactly what CLAUDE.md forbids, and it is the shape that keeps regenerating on this project.

Two things must survive out of it:

- **`ListGraceCandidates` is unbounded and pre-listener**, and no existing item names it. Task 0 has the conductor file it.
- Slice 2 of this lane (`docs/backlog/feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep.md`) builds on it. Do **not** plan or write slice 2's work here.

---

## File structure

| File | Change | Task |
| --- | --- | --- |
| `internal/schedrunner/reconcile_paging_integration_test.go` | **new.** `//go:build integration`, `package schedrunner_test`. Tracer, matcher, seeds, Guards 2, 3 and 4. | 1, 2, 3 |
| `internal/schedrunner/startup_validation_paging_integration_test.go` | ONE signature line (`tracedPool`'s tracer parameter widened) + ONE comment word (statement name). **The two existing test bodies stay byte-identical.** | 1, 6 |
| `internal/store/query/scheduled_jobs.sql` | `ListOverdueScheduledJobsForCatchup` -> `ListOverdueScheduledJobsForCatchupPage`, narrowed and paged, new header; plus the statement-name fix inside `ListEnabledScheduledJobsPage`'s comment | 4 |
| `internal/store/scheduled_jobs.sql.go` | regenerated by `make generate`. **Never hand-edit.** | 4 |
| `internal/schedrunner/runner.go` | `reconcilePageSize`; page loop; `ctx.Err()` return; `now` hoisted; header rewritten | 5 |
| `internal/schedrunner/overdue_catchup_row_surface_test.go` | **new.** UNTAGGED field-set guard over the narrowed row struct | 5 |
| `internal/schedrunner/startup_validation_integration_test.go` | one comment word (statement name) | 6 |
| `README.md` | "Startup sequence" list items 5, 6, 7 only | 6 |
| `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md` | delete one false sentence | 6 |

**Not touched, deliberately:** `internal/store/models.go`, `cmd/relay-server/main.go` (no call moves; `schedrunner_startup_wiring_test.go` must stay green untouched), `internal/schedrunner/startup_validation.go`, `ROADMAP.md` (regenerated by `/roadmap`), `Makefile`, `.github/workflows/`, `internal/jobspec/` (LANE S), `internal/worker/` and `internal/agent/source/perforce/` and `internal/store/query/worker_workspaces.sql` (LANE W), `internal/testsupport/pgdsn/` (LANE E), `internal/schedrunner/stored_spec_count_bounds_test.go` (ours, but nothing in this slice adds a row to it and LANE S deliberately declined to - do not add one on anyone's behalf).

---

## Standing rules for every task

Read once; not repeated per step.

- **Work only inside `D:/dev/relay/.claude/worktrees/lane-boot-bounds`.** Three sibling lanes are running Go tests and Docker containers on this machine. Do not touch `D:/dev/relay` or any sibling worktree, and do not stop containers you did not start.
- **LANE E is fixing a guard in `internal/testsupport/pgdsn` that is RED on a clean tree inside the `golang:1.26` container.** If you see it fail, it is not yours. Report it, do not chase it.
- **Plan-supplied test bodies are guesses.** Every code block below is intent plus the assertion that matters. Run each RED yourself and record the number you actually saw. **If a number differs from the one written here, STOP and report - do not adjust the assertion to match.**
- **A mutation battery needs a green baseline** (Task 0, M1). Uniform results mean a broken harness. **A compile error is not a kill.**
- **Verify each mutation actually applied**: re-read the mutated line after editing. A silently unapplied mutation reports "survived".
- **Never `git checkout --` to revert a mutation.** It discards uncommitted work under test. Copy the file to the scratchpad first and restore from the copy.
- **Commit with an explicit pathspec.** `git add <paths>`, never `git add -A`. Sibling agents share this machine's git index.
- **After ANY programmatic edit to a tracked text file**, before committing: check `git diff --stat` against the size of the change you intended, run `git ls-files --eol` on every touched path (each must read `i/lf`), and confirm the file still decodes as UTF-8. `git diff` and `git status` disagree by design on this repo - never conclude "nothing to revert" from `git diff` alone.
- **Comments state a hazard or constraint the code cannot show.** No dates, no change history, no session or measurement narrative, no counts of other code, no uniqueness or completeness claims about OTHER code. A function's own contract is fine and is required here by the item's second acceptance criterion.

---

## Task 0: Measurement ledger and conductor prerequisites

**Nothing in the spec was measured** - it was written without a Bash tool. Everything below is an instruction to OBTAIN a value, not a value. **Do not fill a Record box from this plan, from the spec, or from what you expect.** An unfilled box blocks the task that cites it.

**Files:** none. This task produces recorded values and two filed backlog items.

### The ledger

| # | Measurement | Measured at | Record |
| --- | --- | --- | --- |
| M1 | Green baseline: `make test-pg-integration` in this worktree | Task 0 Step 2 | PASS / FAIL + failing tests: `______` |
| M2 | The traced SQL text pgx actually sees for the reconcile read, at HEAD and after the change | Task 1 Step 3, Task 5 Step 5 | verbatim, both: `______` |
| M3 | The row struct name sqlc emits for the narrowed statement | Task 4 Step 3 | `______` |
| M4 | Is `pgx.TraceQueryEndData.CommandTag.RowsAffected()` populated for a SELECT in the vendored pgx? | Task 1 Step 3 | populated / not, value seen: `______` |
| M5 | Complement search for the "peak memory is one page" claim: hit count, and which hits are BOOT-scoped | Task 0 Step 3 | count `______`, boot-scoped sites `______` |
| M6 | `reconcilePageSize` value, and whether anyone asked to raise it | Task 0 Step 4 | `______` |
| M7 | The diff to `startup_validation_paging_integration_test.go` | Task 6 Step 4 | lines changed: `______` |

- [ ] **Step 1: Conductor files two backlog items and takes one decision**

The engineer does not run `/backlog`. The conductor files, from the spec's "Backlog items this spec proposes":

1. **`ListGraceCandidates` is unbounded and runs before the HTTP listener.** `SELECT DISTINCT w.id, w.disconnected_at, w.connection_epoch FROM workers w JOIN tasks t ON t.worker_id = w.id WHERE t.status IN ('dispatched','preparing','running')`, no LIMIT, reached from `seedGraceTimersFromActiveTasks`. Three narrow columns and a fleet-sized population, so it is a much smaller exposure than either schedule statement - which is the argument for filing it rather than folding it into one of them.
2. **`ReconcileOnStartup` can clobber a concurrent PATCH's `next_run_at`.** Pre-existing, reachable multi-replica, bounded at one fire, improved from O(N) to O(page) by this slice without being closed. **The item must NOT prescribe a fence** - the spec's Decision 5 refuses one on merits, because a fenced non-match skips the advance and a row left overdue produces the one spurious fire never-catch-up forbids.

The spec proposes a third (the boot is asymmetrically started: gRPC accepts while HTTP does not). That is a readiness and pool-contention observation rather than a bug; the conductor decides whether to file it. **Nothing in this plan cites either filed item by filename**, so no task blocks on the result.

- [ ] **Step 2: M1 - green baseline BEFORE any code**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-bounds
git status --porcelain   # note anything already dirty; it is not yours
make test-pg-integration
```

Record PASS/FAIL and, if FAIL, the exact test names. **A red here is not a reason to proceed quietly.** If the only failure is in `internal/testsupport/pgdsn`, that is LANE E's known clean-tree red - record it and continue. Any other failure: STOP and report.

This is the baseline the Task 7 mutation battery is measured against. Without it, a uniform battery result cannot be distinguished from a broken harness.

- [ ] **Step 3: M5 - the complement search for the peak-memory claim**

The item's third acceptance criterion says "The claim that the boot's peak memory is one page is either made true or corrected wherever it is written down." That is a claim about a COMPLEMENT. The spec asserts no such site exists; **it must be searched for, with a recorded count, not inherited.**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-bounds
rg -i -c "peak memory|peak resident|peak bytes" .
rg -i -n "peak memory|peak resident|peak bytes" . > "$env:TEMP/claude/.../scratchpad/laneB-peakmem.txt"
```

**Prior, obtained by the planner from this worktree at plan time with the first command: 36 occurrences across 12 files.** The tree moves under three concurrent lanes, so re-run it and record your own number. Then classify every hit into SWEEP-scoped (accurate as written - `ValidateStoredSpecsOnStartup`, `sweepPageSize`, the boot-sweep spec/plan/retro, ROADMAP's sweep entries) versus BOOT-scoped (a claim about the whole boot, which would be false). **Only BOOT-scoped hits are work.** If you find one, STOP and report before deciding delete-or-correct: the two live prose defects this slice already owns are in README and in the sibling backlog item, and both are about ORDER and about a false census, not about memory.

Note the search axis you used, and note the one nobody enumerated: this searches for three phrasings of the claim. A site that says the same thing in other words is not covered by it.

- [ ] **Step 4: M6 - the page size**

`reconcilePageSize = 100`, its own unexported constant in `internal/schedrunner/runner.go`, **not** aliased to `sweepPageSize` and **not** aliased to `BatchLimit`. Record: "reused 100; no raise requested; no number obtained."

The spec declines raising it and says why: after narrowing, round trips rather than memory become the binding constraint, and raising the page size is a DURATION change made without a measurement. **If anyone asks for a larger value, it needs a number first** - a round-trip or wall-clock measurement of the boot at a realistic overdue count - and getting that number is a task nobody has scheduled. Record the request and refuse the change in this slice.

---

## Task 1: Guard 2 - the reconcile reads in pages of one hundred

**Files:**
- Create: `internal/schedrunner/reconcile_paging_integration_test.go`
- Modify: `internal/schedrunner/startup_validation_paging_integration_test.go` (the `tracedPool` signature line only)

### The property this task pins, and why the input discriminates

**Property:** `ReconcileOnStartup` reads the overdue enabled set one page of 100 at a time, terminating on a short page, and advances every row on every page including the short last one.

**Discriminating input: 250 overdue enabled rows.** All three properties are load-bearing: more than one page, more than two pages, and not a multiple of the page size so the final page is short. Any input under 101 rows behaves identically on paged and unpaged code and pins nothing.

**The positive assertion is not optional.** A pass that dropped its final page, or stopped after one, satisfies a statement count alone. Asserting that all 250 rows now have `next_run_at` in the future is what closes that.

- [ ] **Step 1: Widen `tracedPool`'s tracer parameter**

In `internal/schedrunner/startup_validation_paging_integration_test.go`, change exactly one line:

```go
func tracedPool(t *testing.T, h *runnerHarness, tr pgx.QueryTracer) *pgxpool.Pool {
```

(from `tr *sweepTracer`). The file already imports `github.com/jackc/pgx/v5`. `*sweepTracer` satisfies `pgx.QueryTracer`, so **both existing call sites compile unchanged and both existing test bodies stay byte-identical** - which is the spec's requirement. Do not touch `tracedPool`'s doc comment: every word of it is still true.

Confirm with `git diff --stat -- internal/schedrunner/startup_validation_paging_integration_test.go`: 1 insertion, 1 deletion.

- [ ] **Step 2: Write the new test file with the tracer, the matcher, the seeds and Guard 2**

Create `internal/schedrunner/reconcile_paging_integration_test.go`:

```go
//go:build integration

package schedrunner_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// reconcileTracer counts the page reads ReconcileOnStartup issues and can fire a
// hook when a statement finishes.
//
// IT COUNTS AT TraceQueryStart AND HOOKS AT TraceQueryEnd, which is why the SQL
// is stashed in the context: TraceQueryEndData carries a CommandTag and an error
// and no SQL, and the returned context is the only channel pgx gives between the
// two calls.
//
// The mutex is not decoration. pgxpool hands out a connection per acquire and
// nothing in pgx promises one goroutine.
type reconcileTracer struct {
	mu sync.Mutex
	// selects counts statements matching the reconcile's page read.
	selects int
	// onEnd, if set, is called with the finished statement's SQL, ON THE
	// CALLER'S GOROUTINE, after the statement has completed.
	onEnd func(sql string)
}

type reconcileTracerSQLKey struct{}

func (tr *reconcileTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if isReconcilePageRead(d.SQL) {
		tr.mu.Lock()
		tr.selects++
		tr.mu.Unlock()
	}
	return context.WithValue(ctx, reconcileTracerSQLKey{}, d.SQL)
}

func (tr *reconcileTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	sql, _ := ctx.Value(reconcileTracerSQLKey{}).(string)
	tr.mu.Lock()
	hook := tr.onEnd
	tr.mu.Unlock()
	if hook != nil {
		hook(sql)
	}
}

func (tr *reconcileTracer) setOnEnd(hook func(sql string)) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.onEnd = hook
}

func (tr *reconcileTracer) selectCount() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.selects
}

// isReconcilePageRead matches the reconcile's read STRUCTURALLY, never by
// statement name. The name is what this slice changes, so a name matcher reports
// zero before the change - which is also what a test that never reached the
// function reports, making a broken instrument and a real failure
// indistinguishable.
//
// next_run_at < NOW() IS THE DISCRIMINATOR AND THIS IS NOT INTERCHANGEABLE WITH
// isSweepPageRead. That matcher's three fragments are all satisfied by this
// statement too, so it cannot attribute a read to one pass or the other. This
// needle excludes the sweep's page read, which names no next_run_at at all, and
// excludes ListEligibleScheduledJobs, whose predicate is next_run_at <= NOW() -
// a string this needle does not match, and the reason the needle carries NOW()
// rather than stopping at the operator.
func isReconcilePageRead(sql string) bool {
	return strings.Contains(sql, "FROM scheduled_jobs") &&
		strings.Contains(sql, "WHERE enabled") &&
		strings.Contains(sql, "next_run_at < NOW()")
}

// seedOverdueSchedules plants n enabled schedules that are already overdue, in
// ONE statement.
//
// cronExpr is a parameter because the termination guard needs rows
// ReconcileOnStartup cannot advance, and namePrefix is one because the
// assertions partition the table by it.
//
// THE RAW INSERT BYPASSES handleCreateScheduledJob'S VALIDATION, which is what
// lets an unparseable cron reach the row - and it is also why that input is
// reachable in production, since a migration or an older binary writes through
// the same statement. job_spec is a minimal valid object: the reconcile reads
// that column zero times.
func seedOverdueSchedules(t *testing.T, h *runnerHarness, owner pgtype.UUID, namePrefix, cronExpr string, n int) {
	t.Helper()
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO scheduled_jobs (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at)
		SELECT $4::text || g::text, $1, $5::text, 'UTC', $2::jsonb, 'skip', TRUE, NOW() - INTERVAL '25 hours'
		FROM generate_series(1, $3::int) AS g`,
		owner, `{"name":"r","tasks":[{"name":"t","command":["echo","hi"]}]}`, n, namePrefix, cronExpr)
	require.NoError(t, err)
}

// countAdvanced counts rows under namePrefix whose next_run_at is now in the
// future, through the harness's own UNTRACED pool so the assertion's own read
// cannot move the statement counter.
func countAdvanced(t *testing.T, h *runnerHarness, namePrefix string) int {
	t.Helper()
	var n int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scheduled_jobs WHERE name LIKE $1 || '%' AND next_run_at > NOW()`,
		namePrefix).Scan(&n))
	return n
}

// TestReconcileOnStartup_ReadsInPagesOfOneHundred pins that the reconcile reads
// the overdue enabled set in pages rather than in one unbounded statement, and
// that every row on every page - including the SHORT last one - is advanced.
//
// 250 IS THE DISCRIMINATING INPUT and each of its three properties is load
// bearing: more than one page, more than two pages, and not a multiple of the
// page size, so the final page is short. Any input below 101 rows behaves
// identically on paged and unpaged code and pins nothing.
//
// THE LITERAL 3 IS DELIBERATE AND SO IS THE ABSENCE OF A SEAM.
// reconcilePageSize is unexported and this is an external test package, so the
// expected count cannot be derived from the thing under test. 250 rows at 100
// per page is 100 + 100 + 50.
//
// IT DOES NOT PIN THE CURSOR'S DIRECTION. A cursor taking each page's MINIMUM
// id still reports 3 here, because every row this fixture reads is advanced out
// of the predicate before the next page runs.
// TestReconcileOnStartup_TerminatesWhenAStoredCronNoLongerParses is where that
// mutant dies.
func TestReconcileOnStartup_ReadsInPagesOfOneHundred(t *testing.T) {
	h := newRunnerHarness(t)
	owner := h.createUser(t, "paged-reconcile@example.com")
	seedOverdueSchedules(t, h, owner, "reconcile-paged-", "@hourly", 250)

	tr := &reconcileTracer{}
	q := store.New(tracedPool(t, h, tr))

	// BOUNDED FAILURE. A cursor that fails to advance is an infinite loop, and a
	// hang is indistinguishable from infrastructure trouble. Under a deadline
	// that mutant fails as a named timeout instead of consuming the package
	// clock.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	require.NoError(t, schedrunner.ReconcileOnStartup(ctx, q),
		"a context deadline here means the pass did not terminate")

	require.Equal(t, 3, tr.selectCount(),
		"250 overdue rows at 100 per page is three reads: 100, 100, then a short 50. "+
			"1 means the reconcile still materializes every overdue row in one statement")

	require.Equal(t, 250, countAdvanced(t, h, "reconcile-paged-"),
		"THE POSITIVE ASSERTION. Without it a pass that dropped the final page, or stopped "+
			"after one page, would still satisfy a statement count alone")
}
```

**Notes for the engineer, all of which you must check rather than assume:**

- `newRunnerHarness`, `h.createUser` and `tracedPool` live in `runner_test.go` and `startup_validation_paging_integration_test.go`, same package, same build tag.
- `captureLog(t, &buf)` lives in `startup_validation_fence_integration_test.go`; Guard 2 needs no log capture because a clean reconcile logs nothing.
- If pgx rejects `$2::jsonb` against a Go `string`, wrap it: `[]byte(...)`. If Postgres cannot infer a placeholder, the casts are already explicit - report the exact error rather than guessing.
- `bytes` and `sync` are imported for Tasks 2 and 3; Go will not compile an unused import, so add them when those tasks add their uses, or write all three tests before compiling.

- [ ] **Step 3: Run at HEAD, record the RED, and capture M2 and M4 while you are here**

Temporarily add, as the first line of `TraceQueryStart`:

```go
	t.Log(d.SQL)  // TEMPORARY - M2 capture. Remove before commit.
```

(`TraceQueryStart` has no `t`; instead put the print in the test body's tracer construction, or simply add `fmt.Println("SQL>>>", d.SQL)` and `fmt.Println("TAG>>>", d.CommandTag.String(), d.CommandTag.RowsAffected())` in `TraceQueryStart` / `TraceQueryEnd` respectively. Either is fine - it comes straight back out.)

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-bounds
go test -tags integration -p 1 ./internal/schedrunner/... -run TestReconcileOnStartup_ReadsInPagesOfOneHundred -v -timeout 600s
```

Expected: FAIL at the first `require.Equal`, reporting `expected: 3 / actual: 1`.

**The number 1 is the check on the instrument, not just the outcome.** `actual: 0` means the matcher is wrong - most likely keyed on a name, or on a fragment the traced text does not contain - and the RED is not the one this test claims. Fix the matcher and re-run before going further. `actual: 250` means the matcher is catching the UPDATEs. **Record the number you actually saw.**

**M2:** paste the reconcile read's traced text verbatim into the ledger. **Pick the discriminator against THAT text, not against the `.sql` source** - sqlc prepends a `-- name:` header and may reflow whitespace. If `next_run_at < NOW()` does not appear verbatim in the traced text, the matcher above is wrong; use what the artifact actually contains and say so in the ledger.

**M4:** record whether `CommandTag.RowsAffected()` returned a row count for the SELECT or zero. **Do not decide this from documentation or from the pgx version alone.** If it IS populated, Task 5 Step 6 adds a per-statement ceiling assertion; if it is NOT, Task 5 Step 6 writes one line in the test comment saying the per-statement ceiling is not asserted and what would have to exist for it to be.

Remove the temporary prints before Step 4.

- [ ] **Step 4: Do NOT commit**

The tree is red on purpose and stays that way until Task 5. Tasks 2 and 3 add to this same file.

---

## Task 2: Guard 3 - termination when a stored cron no longer parses

**Files:**
- Modify: `internal/schedrunner/reconcile_paging_integration_test.go`

### The property this task pins, and why the input discriminates

**Property:** the pass terminates, in a bounded number of page reads, over a set containing more than one page of rows it can never advance - and it advances every row it can while leaving the others alone.

**This is the guard that earns its keep**, because it is the one that goes RED against the design a reader is most likely to reach for (the drain loop of the spec's Decision 1), where a page-count test over healthy rows does not. But see refutation 1: **the discriminating property of the input is that at least `reconcilePageSize` rows can never be advanced.** With fewer, the drain loop terminates and this test proves nothing about it.

**150 poisoned + 100 healthy = 250.** 150 >= 100 makes every drain-loop page permanently full. 250 is still three pages under the correct design, so the count assertion is the same literal 3, and the healthy/poisoned split makes the min-cursor mutant explode past it (refutation 2).

- [ ] **Step 1: Append the test**

```go
// TestReconcileOnStartup_TerminatesWhenAStoredCronNoLongerParses pins that the
// pass terminates, and in a bounded number of reads, over rows it cannot
// advance.
//
// IT IS THE GUARD FOR THE CURSOR ITSELF. Termination here is a property of
// `id > cursor_id` alone: a row is excluded once read whatever the loop did with
// it. A design that re-queried `next_run_at < NOW()` without a cursor, relying
// on each row's own advance to remove it from the predicate, re-reads these rows
// forever - and this fixture is what makes that true rather than merely slow.
//
// 150 POISONED IS THE LOAD-BEARING NUMBER, not "at least one". A drain loop ends
// when a page comes back short, so with fewer unadvanceable rows than fit in one
// page it terminates after extra work and satisfies every assertion below. At
// 150 against a page size of 100 every page it reads is full, forever.
//
// THE 100 HEALTHY ROWS ARE THE SECOND HALF OF THE INSTRUMENT. They make the
// count assertion discriminate a cursor that takes each page's MAXIMUM id from
// one that takes its MINIMUM: with rows that stay overdue, a minimum cursor
// advances by one row per page instead of one page per page.
//
// THE DEADLINE IS THE BOUNDED FAILURE. Without it the non-terminating mutant
// hangs, and a hang is indistinguishable from infrastructure trouble.
func TestReconcileOnStartup_TerminatesWhenAStoredCronNoLongerParses(t *testing.T) {
	// One "reconcile skip for" line per poisoned row. Nothing here reads them
	// back; the capture only keeps them off the test output.
	var discarded bytes.Buffer
	captureLog(t, &discarded)

	h := newRunnerHarness(t)
	owner := h.createUser(t, "poisoned-reconcile@example.com")
	seedOverdueSchedules(t, h, owner, "reconcile-poison-", "@nonsense-not-a-cron", 150)
	seedOverdueSchedules(t, h, owner, "reconcile-healthy-", "@hourly", 100)

	tr := &reconcileTracer{}
	q := store.New(tracedPool(t, h, tr))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	require.NoError(t, schedrunner.ReconcileOnStartup(ctx, q),
		"context deadline exceeded here means the pass did not terminate: the cursor is stuck, "+
			"so the 150 rows whose cron cannot be parsed are being re-read forever")

	require.Equal(t, 3, tr.selectCount(),
		"250 overdue rows at 100 per page is three reads whatever the loop does with them. "+
			"A number far above 3 means the cursor advances by rows rather than by pages - "+
			"a page MINIMUM id instead of the maximum")

	require.Equal(t, 100, countAdvanced(t, h, "reconcile-healthy-"),
		"every parseable row must advance, including the ones on the short final page")

	require.Equal(t, 0, countAdvanced(t, h, "reconcile-poison-"),
		"an unparseable cron is skipped WITHOUT advancing next_run_at, so the row stays overdue "+
			"for ListEligibleScheduledJobs to pick up within TickInterval. A non-zero here means "+
			"the cron parsed, so the fixture is not poisoned and the test is vacuous")
}
```

- [ ] **Step 2: Run at HEAD and record the RED**

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -run TestReconcileOnStartup_TerminatesWhenAStoredCronNoLongerParses -v -timeout 600s
```

Expected: FAIL at the count assertion, `expected: 3 / actual: 1`. The other three assertions are GREEN at HEAD, and that is correct and expected: **HEAD terminates.** This test's unique value is measured by mutation (Task 7, mutations 1 and 6), not by its HEAD RED. Say so in the commit message; do not claim a RED it did not produce.

If `countAdvanced(..., "reconcile-poison-")` is non-zero, `@nonsense-not-a-cron` parsed. Pick another unparseable expression, confirm the assertion goes to 0, and record what you used.

- [ ] **Step 3: Do NOT commit**

---

## Task 3: Guard 4 - a cancelled pass returns instead of logging every row

**Files:**
- Modify: `internal/schedrunner/reconcile_paging_integration_test.go`

### The property this task pins, and why the input discriminates

**Property:** a cancellation landing mid-pass returns the cause once instead of running on and logging one line per remaining row.

**Reconcile's exposure is strictly worse than the sweep's**, which is why this is not deferred: the sweep issues a statement only for BROKEN rows, while reconcile issues `AdvanceScheduledJobNextRun` for **every** row. A SIGTERM mid-reconcile makes every remaining row get `context canceled` back and log its own line, unconditionally.

**The cancellation must land MID-pass or the test proves nothing.** Cancelling before the call makes the page query itself fail, so a function with no `ctx.Err()` check also returns an error and the assertions cannot distinguish them. The tracer fires on the END of the first `AdvanceScheduledJobNextRun`, which pgx calls on this goroutine after the Exec completed - so row 1's write lands and the row loop's next iteration is the very next thing to run. No sleep, no poll, no second goroutine.

**Three rows, not 250.** This property is independent of the page size.

- [ ] **Step 1: Append the test**

```go
// TestReconcileOnStartup_ACancelledPassReturnsInsteadOfLoggingEveryRow pins the
// reconcile's behaviour under a mid-pass shutdown.
//
// THE HOOK MAY KEY ON THE STATEMENT NAME here, where the page-read counter may
// not: AdvanceScheduledJobNextRun is not being renamed, and the hook is a
// fixture rather than the thing under test.
//
// THE EXPOSURE IS ONE LINE PER REMAINING ROW, unconditionally, because this loop
// issues an UPDATE for every row rather than for broken rows only.
func TestReconcileOnStartup_ACancelledPassReturnsInsteadOfLoggingEveryRow(t *testing.T) {
	var logged bytes.Buffer
	captureLog(t, &logged)

	h := newRunnerHarness(t)
	owner := h.createUser(t, "cancelled-reconcile@example.com")
	seedOverdueSchedules(t, h, owner, "reconcile-cancel-", "@hourly", 3)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := &reconcileTracer{}
	var once sync.Once
	tr.setOnEnd(func(sql string) {
		if strings.Contains(sql, "-- name: AdvanceScheduledJobNextRun") {
			once.Do(cancel)
		}
	})
	q := store.New(tracedPool(t, h, tr))

	err := schedrunner.ReconcileOnStartup(ctx, q)

	require.ErrorIs(t, err, context.Canceled,
		"a cancelled pass must return the cause once, not swallow it and report a clean pass")

	require.Equal(t, 0, strings.Count(logged.String(), "reconcile advance for "),
		"after cancellation no further row may reach AdvanceScheduledJobNextRun, so none may log "+
			"its own context-canceled line")

	// ANTI-VACUITY. Without this a pass that returned before doing any work at
	// all would satisfy both assertions above.
	require.Equal(t, 1, countAdvanced(t, h, "reconcile-cancel-"),
		"exactly the one row processed before the cancellation must be advanced")
}
```

- [ ] **Step 2: Run at HEAD and record the RED**

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -run TestReconcileOnStartup_ACancelledPassReturnsInsteadOfLoggingEveryRow -v -timeout 300s
```

Expected: FAIL on the first assertion (`err` is nil at HEAD) and on the second (**2** occurrences of `reconcile advance for `, one per remaining row). The third is GREEN at HEAD. Record both numbers.

If the second assertion reports 0 while the first reports nil, the cancellation did not land mid-pass - check that the hook's needle matches the traced text you captured in M2 for the UPDATE statement.

- [ ] **Step 3: Do NOT commit. Confirm the whole file compiles**

```powershell
go vet -tags integration ./internal/schedrunner/...
```

All three tests are in one file now; `bytes`, `sync`, `strings`, `time`, `pgx`, `pgtype` must all be used.

---

## Task 4: Rewrite the statement and regenerate

**Files:**
- Modify: `internal/store/query/scheduled_jobs.sql` (the `ListOverdueScheduledJobsForCatchup` block; one word inside `ListEnabledScheduledJobsPage`'s comment)
- Regenerate: `internal/store/scheduled_jobs.sql.go`

**This task deliberately leaves the tree not compiling.** `runner.go` still calls the old function. Task 5 is the repair.

- [ ] **Step 1: Replace the statement**

In `internal/store/query/scheduled_jobs.sql`, replace the whole `ListOverdueScheduledJobsForCatchup` block (its `-- name:` line and its three SQL lines) with:

```sql
-- name: ListOverdueScheduledJobsForCatchupPage :many
-- ONE PAGE of overdue enabled schedules for schedrunner.ReconcileOnStartup,
-- keyset-paged on the primary key exactly as ListEnabledScheduledJobsPage is.
-- That statement's header carries the reasoning for ORDER BY id, for the exact
-- LIMIT with no `+ 1`, and for cursor_set rather than a zero-uuid seed. All of
-- it holds verbatim here and is not repeated; what follows is only what differs.
--
-- THE PREDICATE SELECTS ON A COLUMN THE CALLER WRITES, and the cursor is what
-- makes that safe. Each page is a fresh statement with a fresh NOW(), and the
-- loop advances next_run_at on the rows it reads. A design that re-queried this
-- predicate WITHOUT a cursor, relying on each row's own advance to remove it
-- from the result, re-reads forever every row the loop does not advance: a cron
-- that no longer parses, an UPDATE that failed, or a schedule whose interval is
-- shorter than the pass. `id > cursor_id` excludes every row already read
-- whatever the loop did with it, so each full page permanently retires
-- page_limit rows from a finite candidate set and termination does not depend on
-- the loop body succeeding at anything.
--
-- FOUR COLUMNS, NOT SELECT *, AND THE LIST IS THE BOUND. ReconcileOnStartup
-- reads id (the advance, and the cursor), name (its two log lines) and
-- cron_expr with timezone (ParseSchedule). It reads job_spec zero times and
-- sends nothing back, and job_spec is bounded only by maxBodyBytes at 1 MiB, so
-- dropping it is a larger lever per row than any page size - which is also why
-- ListEnabledScheduledJobsPage, whose validator reads job_spec and whose fence
-- sends it back, keeps SELECT *.
-- TestOverdueCatchupPageRowCarriesOnlyTheFourColumnsReconcileReads is what goes
-- red if a column comes back.
--
-- NO FENCE ON THE ADVANCE, unlike RecordScheduledJobFailure beside it, and the
-- difference is what each statement writes. That one writes a VERDICT about
-- content it read, so a stale write is a false alarm on a repaired schedule.
-- This one writes a VALUE computed from cron_expr and timezone, so two replicas
-- reconciling the same row write the same value and there is nothing to be
-- stale about. What replaces the fence is idempotence across replicas plus
-- self-healing on the next fire, since fireOne recomputes from the row's current
-- cron. A PATCH landing between this read and the advance can still have its
-- freshly computed next_run_at clobbered by one derived from the pre-patch cron;
-- that residual is bounded at one fire, and a fence would be worse - a fenced
-- non-match SKIPS the advance, and a row left overdue produces exactly the one
-- spurious fire the never-catch-up policy forbids.
SELECT id, name, cron_expr, timezone FROM scheduled_jobs
 WHERE enabled
   AND next_run_at < NOW()
   AND (NOT @cursor_set::bool OR id > @cursor_id::uuid)
 ORDER BY id
 LIMIT @page_limit::int;
```

- [ ] **Step 2: Fix the statement name inside `ListEnabledScheduledJobsPage`'s comment**

Same file, in the `EVERY enabled schedule, not just the overdue ones.` paragraph, one word:

```
-- and ListOverdueScheduledJobsForCatchupPage both require next_run_at to have
```

(from `ListOverdueScheduledJobsForCatchup`). The paragraph's substance stays true. **This must land in the same `make generate` as Step 1**, because sqlc copies the comment into `scheduled_jobs.sql.go`.

- [ ] **Step 3: Regenerate, and check the toolchain before trusting the diff**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-bounds
git status --porcelain -- internal/store   # only the .sql file should be listed
sqlc version
make generate
```

`make generate` runs `sqlc generate` AND `buf generate`. `sqlc` is not version-pinned. If regeneration changes generated files in ways unrelated to `scheduled_jobs.sql` - different helper shapes, different comment formatting, anything under the protobuf output - you are looking at a toolchain bump. **STOP and report. Do not commit a toolchain bump inside this slice.**

**M3:** read the emitted row struct's name out of `internal/store/scheduled_jobs.sql.go` and record it. The expected form is `ListOverdueScheduledJobsForCatchupPageRow`; **it is not verified.** Whatever sqlc emitted is what Task 5 uses. Record the params struct too - expected, from the sibling statement's shapes, `CursorSet bool`, `CursorID pgtype.UUID`, `PageLimit int32` - and use what you actually see.

- [ ] **Step 4: CRLF hygiene, then re-verify the generated file survived it**

This is the step that has silently discarded a regenerated `.sql.go` before. Do all of it, in order.

```powershell
# 1. The real content change. --ignore-all-space hides pure line-ending churn.
git diff --ignore-all-space -- internal/store

# 2. The files git actually considers modified. This list is LONGER than the one
#    above, by design: core.autocrlf makes `git diff` normalize LF churn away
#    while `git status` still lists the files. NEVER conclude "nothing to revert"
#    from `git diff` alone.
git status --porcelain -- internal/store internal/pb

# 3. Revert every file whose only change is line endings. KEEP
#    internal/store/scheduled_jobs.sql.go.
git checkout -- <each LF-only path>

# 4. RE-VERIFY THE GENERATED FILE. The revert in step 3 is how a regenerated
#    .sql.go gets thrown away without a word.
Select-String -Path internal/store/scheduled_jobs.sql.go -Pattern "ListOverdueScheduledJobsForCatchupPageRow"
Select-String -Path internal/store/scheduled_jobs.sql.go -Pattern "const listOverdueScheduledJobsForCatchup ="
```

The first `Select-String` **must** hit. The second **must not** - it is written with the trailing ` =` on purpose, because `listOverdueScheduledJobsForCatchupPage` contains `listOverdueScheduledJobsForCatchup` as a substring and a bare search would hit the new constant.

```powershell
# 5. Line endings, proportionality, encoding.
git ls-files --eol internal/store/scheduled_jobs.sql.go internal/store/query/scheduled_jobs.sql   # both i/lf
git diff --stat -- internal/store
```

The diffstat for `scheduled_jobs.sql.go` should be on the order of a hundred lines (one const, one params struct, one row struct, one rewritten function, one long doc comment), not thousands. A four-figure insertion count means a line-ending or encoding accident, not a rename.

- [ ] **Step 5: Confirm the expected build failure**

```powershell
go build ./...
```

Expected: FAIL, naming `q.ListOverdueScheduledJobsForCatchup undefined` at exactly one call site, `internal/schedrunner/runner.go`. Confirm it names only that one. Any other undefined reference means the statement had a second caller the spec did not find - **STOP and report.**

- [ ] **Step 6: Do NOT commit**

---

## Task 5: The page loop, the constant, the header, and the untagged column guard

**Files:**
- Modify: `internal/schedrunner/runner.go` (the `BatchLimit` neighbourhood; `ReconcileOnStartup` and its header)
- Create: `internal/schedrunner/overdue_catchup_row_surface_test.go`

- [ ] **Step 1: Back the file up before you touch it**

Task 7 restores from this copy. **Never `git checkout --`** - it would discard this task's uncommitted work.

```powershell
Copy-Item internal/schedrunner/runner.go "$env:TEMP/claude/.../scratchpad/laneB-runner.go.bak"
```

- [ ] **Step 2: Add the constant**

In `internal/schedrunner/runner.go`, immediately below `BatchLimit`:

```go
// reconcilePageSize is how many rows ReconcileOnStartup holds at once.
//
// IT IS NEITHER BatchLimit NOR sweepPageSize AND MUST NOT BE ALIASED TO EITHER.
// BatchLimit governs how many rows one tick holds LOCKED, since
// ListEligibleScheduledJobs is FOR UPDATE SKIP LOCKED inside a transaction;
// sweepPageSize governs the startup sweep's peak resident bytes; this one
// governs the reconcile's round-trip granularity. Three independent policies
// behind one number makes two of the three comments false the first time any of
// them moves.
//
// THE VALUE MATCHES THE OTHER TWO ON PURPOSE. With the column list narrowed to
// four, no statement on the boot path holds more rows at once than any other, so
// a reader reasoning about boot memory has one number to hold rather than three.
//
// A CONSTANT, NOT AN ENV VAR, for sweepPageSize's reason: the
// configurable-timeout convention is about waits whose right value depends on
// the operator's data, and no operator has information the code lacks about how
// many rows to hold at once.
const reconcilePageSize = 100
```

- [ ] **Step 3: Replace `ReconcileOnStartup` and its header**

Replace the function and its doc comment at the bottom of `internal/schedrunner/runner.go` with:

```go
// ReconcileOnStartup advances next_run_at past any missed triggers for every
// OVERDUE enabled schedule, implementing the never-catch-up policy. Call after
// migrations but before Runner.Run() starts.
//
// THE PREDICATE IS OVERDUE-ENABLED, not every enabled schedule: the statement
// filters on next_run_at < NOW(). A pass over WHERE enabled alone would be a
// full-table pass rewriting next_run_at on schedules that missed nothing.
//
// IT PAGES TO EXHAUSTION AND LEAVES NO REMAINDER. There is no ceiling on the
// number of pages, deliberately. A ceiling leaves rows overdue, and
// ListEligibleScheduledJobs then fires each of them once within TickInterval -
// one job per remainder schedule, for a firing never-catch-up exists to skip,
// which is the opposite of what README promises an operator. The only truncation
// is a cancelled boot, and that remainder has nowhere to go and needs nowhere:
// ctx at the call site is signal.NotifyContext, so a cancellation means the
// process is already exiting and the next boot's reconcile owns those rows.
//
// PEAK MEMORY IS ONE PAGE OF FOUR NARROW COLUMNS. THE DURATION IS BOUNDED BY
// NOTHING - one round trip per page plus one UPDATE per overdue row, all of it
// ahead of srv.ListenAndServe(). Paging converted an unbounded allocation into
// an unbounded duration, the same trade the startup sweep made.
//
// A PER-ROW FAILURE MUST NOT STOP THE BOOT. A cron that no longer parses is
// logged and skipped WITHOUT advancing next_run_at, so the row stays overdue and
// ListEligibleScheduledJobs picks it up at most TickInterval later, where
// fireOne's own ParseSchedule failure records it; ValidateStoredSpecsOnStartup's
// header carries that reasoning. An UPDATE that fails is logged. Two things ARE
// returned - a page query's error, and the cancellation the row loop checks for
// - and the caller in cmd/relay-server logs either as a warning.
//
// now IS CAPTURED ONCE, above the page loop, because it is the never-catch-up
// reference instant: every row's advance derives from the same boot instant
// rather than from where the row happened to land in id order. The SQL NOW() in
// the predicate floats per page, and the asymmetry is deliberate. Its
// consequence, so it is not mistaken for a defect: on a long pass a
// short-interval schedule can be advanced to a time already past and stay
// overdue, and the ticker then fires it once. That is one fire rather than one
// per missed trigger, and the id cursor makes revisiting it within this pass
// impossible.
func ReconcileOnStartup(ctx context.Context, q *store.Queries) error {
	var (
		cursor    pgtype.UUID
		cursorSet bool
	)
	now := time.Now()
	for {
		rows, err := q.ListOverdueScheduledJobsForCatchupPage(ctx, store.ListOverdueScheduledJobsForCatchupPageParams{
			CursorSet: cursorSet,
			CursorID:  cursor,
			PageLimit: reconcilePageSize,
		})
		if err != nil {
			return err
		}
		for _, row := range rows {
			// A CANCELLED PASS RETURNS RATHER THAN RUNNING ON. Every remaining row
			// would otherwise reach AdvanceScheduledJobNextRun, get context canceled
			// back and log its own line - one per row, unconditionally, because this
			// loop issues an UPDATE for every row rather than for broken rows only.
			// At the top of the ROW loop rather than the page loop, since one page is
			// already up to reconcilePageSize lines. RETURN rather than break, so the
			// caller names the cause once instead of reporting a clean pass.
			if err := ctx.Err(); err != nil {
				return err
			}
			sched, err := ParseSchedule(row.CronExpr, row.Timezone)
			if err != nil {
				log.Printf("schedrunner: reconcile skip for %s: %v", row.Name, err)
				continue
			}
			next := sched.Next(now)
			if err := q.AdvanceScheduledJobNextRun(ctx, store.AdvanceScheduledJobNextRunParams{
				ID:        row.ID,
				NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
			}); err != nil {
				log.Printf("schedrunner: reconcile advance for %s: %v", row.Name, err)
			}
		}
		// A SHORT PAGE IS THE END, not an empty one. On a table whose overdue count
		// is an exact multiple of reconcilePageSize this costs one empty round trip;
		// breaking on an empty page costs the same trip on every table and reads as
		// if a full page could be the last one.
		if len(rows) < reconcilePageSize {
			break
		}
		cursor = rows[len(rows)-1].ID
		cursorSet = true
	}
	return nil
}
```

`runner.go` already imports `context`, `log`, `time`, `store` and `pgtype`. No import changes. If M3 recorded different struct or field names, use those.

- [ ] **Step 4: Add the untagged column-list guard**

Create `internal/schedrunner/overdue_catchup_row_surface_test.go`:

```go
package schedrunner_test

import (
	"reflect"
	"testing"

	"relay/internal/store"
)

// overdueCatchupPageRowFields is the COMPLETE field set of the row
// ListOverdueScheduledJobsForCatchupPage returns. Adding a name here is the
// deliberate act this guard exists to force.
var overdueCatchupPageRowFields = []string{"ID", "Name", "CronExpr", "Timezone"}

// TestOverdueCatchupPageRowCarriesOnlyTheFourColumnsReconcileReads is the
// field-set guard over the reconcile's narrowed read.
//
// THE COLUMN LIST IS THE BOUND, alongside the page size. job_spec is bounded
// only by maxBodyBytes at 1 MiB and ReconcileOnStartup reads it zero times, so
// dropping it is a larger lever per row than the page size is. Restoring
// SELECT *, or adding one column for a validation step somebody bolts on later,
// brings that term back onto the boot path; without this guard nothing goes red.
//
// IT ASSERTS THE SET, NOT A COUNT. A NumField check is satisfied by any four
// fields; the set names what appeared and what went missing, in both directions.
// It shares scheduledJobFieldSetDiff with the guard over store.ScheduledJob for
// that reason.
//
// IT IS DELIBERATELY UNTAGGED. Pure reflection over a compiled struct, no
// database, so it runs in the plain `go test ./...` gate rather than only in the
// Postgres lane - the same placement decision scheduled_job_surface_test.go
// records for itself.
func TestOverdueCatchupPageRowCarriesOnlyTheFourColumnsReconcileReads(t *testing.T) {
	fields := reflect.VisibleFields(reflect.TypeOf(store.ListOverdueScheduledJobsForCatchupPageRow{}))
	got := make([]string, 0, len(fields))
	for _, f := range fields {
		got = append(got, f.Name)
	}

	ok, added, removed := scheduledJobFieldSetDiff(got, overdueCatchupPageRowFields)
	if ok {
		return
	}

	t.Fatalf("the reconcile's row struct moved: added %v, removed %v (have %v).\n"+
		"ReconcileOnStartup reads ID (the advance and the page cursor), Name (two log lines) "+
		"and CronExpr/Timezone (ParseSchedule), and nothing else. An ADDED field means a column "+
		"came back into a statement that runs ahead of the HTTP listener - if it is JobSpec, that "+
		"is a 1 MiB per-row term. A REMOVED field means the loop lost a value it reads.",
		added, removed, got)
}
```

- [ ] **Step 5: Run everything and confirm GREEN**

```powershell
go build ./...
go test ./internal/schedrunner/... -run TestOverdueCatchupPageRow -v -timeout 60s
go test -tags integration -p 1 ./internal/schedrunner/... -timeout 1200s
```

Expected: build clean; the untagged guard PASS; **every** schedrunner integration test PASS, including all three new ones, both existing reconcile tests in `runner_test.go`, both sweep paging tests, and both fence tests.

**M2, second half:** re-capture the reconcile page read's traced text now that it is the new statement, and confirm `isReconcilePageRead` matches it for the reason you think it does. Record it.

Then the untagged gate for the whole tree, which is what CI runs on every commit:

```powershell
go test ./... -count=1 -timeout 600s
```

- [ ] **Step 6: Apply M4's outcome to Guard 2**

If M4 recorded that `CommandTag.RowsAffected()` **is** populated for a SELECT, add one assertion to `TestReconcileOnStartup_ReadsInPagesOfOneHundred`: no matching statement returned more than 100 rows. That is a genuine per-statement ceiling and it is worth having.

If M4 recorded that it is **not** populated, add one line to that test's comment saying the per-statement ceiling is not asserted and that a row count off the tracer is what it would need. **Do not write the measurement narrative into the comment** - the constraint, not how you learned it.

- [ ] **Step 7: Commit**

**One commit, and the atomicity is forced rather than chosen:** the rename does not compile until `runner.go` moves, Tasks 1-3's tests are RED until then, and the untagged guard cannot exist before the struct does - a guard whose absence is a compile error has no RED to show.

```powershell
git add internal/store/query/scheduled_jobs.sql internal/store/scheduled_jobs.sql.go internal/schedrunner/runner.go internal/schedrunner/reconcile_paging_integration_test.go internal/schedrunner/overdue_catchup_row_surface_test.go internal/schedrunner/startup_validation_paging_integration_test.go
git status --porcelain   # confirm nothing else is staged
git commit -m "perf(schedrunner): keyset-page and narrow ReconcileOnStartup's overdue read"
```

The commit message body should carry, because none of it belongs in a comment:

- the RED numbers actually observed for all three integration guards;
- that Guard 3's HEAD RED is the page count only, since HEAD terminates, and that its unique value is proven by the mutation battery;
- that the untagged guard ships without a RED, for the compile-error reason above, and is proven by mutation instead;
- the one-line signature widening in `startup_validation_paging_integration_test.go`, and that both existing test bodies are byte-identical and were re-run;
- that the boot's DURATION is bounded by nothing, so "bounded" without that sentence would be false.

---

## Task 6: The prose this slice owes

Project rule is deletion-first for prose findings: a correction authors fresh claims. Each site below says which and why, and the decision is the spec's - carry it, do not re-take it.

**Files:**
- Modify: `README.md` ("Startup sequence" items 5-7)
- Modify: `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md` (delete one sentence)
- Modify: `internal/schedrunner/startup_validation_paging_integration_test.go` (one comment word)
- Modify: `internal/schedrunner/startup_validation_integration_test.go` (one comment word)

- [ ] **Step 1: README - CORRECT by reordering**

The list is an ordered set of checkable facts about `main`, and three of its lines are in the wrong order. It is corrected rather than deleted because the never-catch-up fact is real information and the defect is ORDER.

`main`'s actual sequence, cited by SYMBOL because line numbers rot: the gRPC `Serve` goroutine, then `go dispatcher.Run`, then a blocking `schedrunner.ReconcileOnStartup`, then a blocking `schedrunner.ValidateStoredSpecsOnStartup`, then `go schedrunner.NewRunner(...).Run`, then `go metrics.NewSweeper(...).Run`, then `go watchdog.Run`, then `go runEnrollmentJanitor`, and **last** the goroutine running `srv.ListenAndServe()`.

Replace exactly these three lines:

```
5. Start an hourly janitor that purges expired enrollment tokens
6. Start the HTTP server (CLI / API traffic)
7. Reconcile scheduled jobs (advance any `next_run_at` that fell in the past while the server was down, then start the scheduler polling loop)
```

with:

```
5. Reconcile scheduled jobs (advance any `next_run_at` that fell in the past while the server was down), then re-validate every enabled schedule's stored spec and record the ones that no longer validate, then start the scheduler polling loop. All three run before the HTTP listener.
6. Start an hourly janitor that purges expired enrollment tokens
7. Start the HTTP server (CLI / API traffic)
```

Three decisions, recorded:

- **The startup validation sweep is named**, in prose rather than by Go symbol, matching the register of the rest of the list. Slice 2 of this lane puts a wall-clock deadline on that step, and a deadline on a step an operator cannot see in the sequence is not something they can reason about.
- **"All three run before the HTTP listener" is the sentence the item is about.** It is what makes the boot's shape checkable from README.
- **Item 4 is NOT touched.** It lumps the dispatcher, the LISTEN/NOTIFY trigger and the watchdog, which straddle the reconcile in `main`. That imprecision is pre-existing and out of scope, and sibling lanes may also be editing README.

Use exact-anchor replacement, not a rewrite of the section. Then:

```powershell
git diff --stat -- README.md      # expect 4 insertions, 3 deletions, nothing else
git ls-files --eol README.md      # i/lf
git diff -- README.md
```

Read the diff. **Assert the file still decodes as UTF-8 and that you introduced no non-ASCII byte** - a raw Latin-1 byte survives every other check this repo runs. Everything above is ASCII; if a diff shows otherwise, you introduced it.

- [ ] **Step 2: The sibling backlog item - DELETE the sentence**

In `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md`, Context section, delete this sentence entirely (it spans four lines):

```
Every other read of `job_spec` in the tree is bounded: `handleListScheduledJobs` is paged,
`ListEligibleScheduledJobs` has `LIMIT $1` at `BatchLimit = 100`, and
`ListOverdueScheduledJobsForCatchup` is unbounded but filtered by `next_run_at < NOW()`, which
newly created schedules do not satisfy.
```

Its lead-in asserts every other read is bounded and its own third clause names one that is not, and it presents a filter as if it were a bound. **Deleted, not corrected**: a correction would author a fresh census over `job_spec` readers - a claim about the complement, pinned by nothing, and the exact shape that keeps regenerating on this project. Nothing is lost: that item's own Summary carries the accurate statement, and the spec carries the enumeration.

The item is still OPEN, so this is housekeeping on a live document rather than a rewrite of a closed record. Leave the blank line structure intact.

- [ ] **Step 3: The two statement-name comments**

`internal/schedrunner/startup_validation_paging_integration_test.go`, `seedBrokenSchedules`'s comment:

```
// next_run_at is far in the future for the reason TestValidateStoredSpecsOnStartup
// gives: neither ListEligibleScheduledJobs nor ListOverdueScheduledJobsForCatchupPage
```

`internal/schedrunner/startup_validation_integration_test.go`, `TestValidateStoredSpecsOnStartup`'s header:

```
//     whole point: ListEligibleScheduledJobs and ListOverdueScheduledJobsForCatchupPage
```

Both are the same word. **Neither test body changes.**

- [ ] **Step 4: M7 - record the diff to the shared test file, and re-run both its tests**

```powershell
git diff --stat -- internal/schedrunner/startup_validation_paging_integration_test.go
```

Expected: **4 changed lines total across this slice** - one `tracedPool` signature line (Task 1) and one comment line (Step 3), each counted as an insertion and a deletion. Record the actual number. If any test BODY changed, that is a re-verification obligation, not a free refactor: say so in the commit and re-run both.

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -run TestValidateStoredSpecsOnStartup -v -timeout 600s
```

Expected: both PASS.

- [ ] **Step 5: Hygiene, then commit**

```powershell
git ls-files --eol README.md docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md internal/schedrunner/startup_validation_paging_integration_test.go internal/schedrunner/startup_validation_integration_test.go
git diff --stat
git add README.md docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md internal/schedrunner/startup_validation_paging_integration_test.go internal/schedrunner/startup_validation_integration_test.go
git commit -m "docs: correct the startup sequence order and drop a false job_spec census"
```

Every path must read `i/lf`. The diffstat must be proportionate: a two-line change that commits as four figures means a line-ending or encoding accident.

---

## Task 7: Mutation battery

**Files:** none committed. Every mutation is reverted from a scratchpad copy.

- [ ] **Step 1: Establish the green baseline**

```powershell
Copy-Item internal/schedrunner/runner.go "$env:TEMP/claude/.../scratchpad/laneB-runner.go.bak"
Copy-Item internal/store/query/scheduled_jobs.sql "$env:TEMP/claude/.../scratchpad/laneB-scheduled_jobs.sql.bak"
Copy-Item internal/store/scheduled_jobs.sql.go "$env:TEMP/claude/.../scratchpad/laneB-scheduled_jobs.sql.go.bak"
go test -tags integration -p 1 ./internal/schedrunner/... -timeout 1200s
go test ./internal/schedrunner/... -timeout 60s
```

Both green, and M1 green, before anything below. **Uniform results mean a broken harness, not a strong suite.** A compile error is not a kill.

**Never `git checkout --` to revert.** Restore with `Copy-Item` from the `.bak` files, then re-run the control.

- [ ] **Step 2: Run the battery**

Run after each mutation, and **re-read the mutated line first** to confirm the edit applied:

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -run "TestReconcileOnStartup" -v -timeout 900s
go test ./internal/schedrunner/... -run TestOverdueCatchupPageRow -timeout 60s
```

| # | Mutation | In | Must go RED | Why, and what to watch |
| --- | --- | --- | --- | --- |
| 1 | `cursor = rows[len(rows)-1].ID` -> `cursor = rows[0].ID` | `runner.go` | **`..._TerminatesWhenAStoredCronNoLongerParses`**, on the count | `..._ReadsInPagesOfOneHundred` **survives this** and that is expected: with every row advanced out of the predicate, a minimum cursor still reports 3. Record it as a survival, not a kill. If the terminates-test's count comes back 3, the fixture is not doing its job - check that 150 poisoned rows really did not advance. |
| 2 | `cursorSet` initialised to `true` | `runner.go` | **both paging tests**, on the count (1) and on the positive assertion (0 advanced) | `id > NULL` is NULL, so the first page is empty and the pass silently does nothing - the failure shape a zero-value seed produces. The count alone would not name it; the "0 of 250 advanced" assertion is what does. |
| 3 | `if len(rows) < reconcilePageSize` -> `if len(rows) == 0` | `runner.go` | **both paging tests**, on the count (4 instead of 3) | The advancement assertions stay GREEN, which is the point: the count is the only thing that sees a pass that stops one round trip late. |
| 4 | `PageLimit: reconcilePageSize` -> `PageLimit: 250` | `runner.go` | **both paging tests**, on the count (1 or 2) | The LIMIT is the memory bound. Nothing else observes it. |
| 5 | SQL column list -> `SELECT id, name, cron_expr, timezone, job_spec`, then `make generate` | `scheduled_jobs.sql` | **`TestOverdueCatchupPageRowCarriesOnlyTheFourColumnsReconcileReads`**, `added [JobSpec]` | This is the mutant that matters: it COMPILES, and without the guard it is silent. Restoring `SELECT *` instead makes the guard fail to COMPILE (the row type stops existing) - still a red gate, but a build failure, not a kill; note the distinction rather than counting it. Restore all three `.bak` files afterwards, re-run `make generate` is NOT needed if you restore the generated file too - but re-run `go build ./...` to prove the tree is back. |
| 6 | **The rejected design.** Replace the whole loop with a drain loop: delete the cursor and its params, and re-issue the page read until `len(rows) < reconcilePageSize` | `runner.go` (requires a temporary SQL variant, or pass `CursorSet: false` every iteration) | **`..._TerminatesWhenAStoredCronNoLongerParses`**, as `context deadline exceeded` within 60 s | The cheapest form is to leave the statement alone and simply never set `cursorSet`. The 150 unadvanceable rows keep every page full forever. **Expect this run to take a full 60 s and to fail with the deadline message naming the stuck cursor** - that is the bounded failure the test was designed for. If it fails instantly instead, the mutation did not apply. |

- [ ] **Step 3: Restore, prove the restore, and record**

```powershell
Copy-Item "$env:TEMP/claude/.../scratchpad/laneB-runner.go.bak" internal/schedrunner/runner.go
Copy-Item "$env:TEMP/claude/.../scratchpad/laneB-scheduled_jobs.sql.bak" internal/store/query/scheduled_jobs.sql
Copy-Item "$env:TEMP/claude/.../scratchpad/laneB-scheduled_jobs.sql.go.bak" internal/store/scheduled_jobs.sql.go
git status --porcelain          # must be clean
git ls-files --eol internal/schedrunner/runner.go internal/store/query/scheduled_jobs.sql internal/store/scheduled_jobs.sql.go
go build ./...
go test -tags integration -p 1 ./internal/schedrunner/... -timeout 1200s
```

`git status` clean and the suite green is the control that the restore actually restored. Record every mutation's outcome, including mutation 1's survival on the paging test, in the PR body. **A kill must name its guard**: for each RED, name the assertion that failed, not just the test.

---

## Task 8: Full gates, hygiene and handoff

- [ ] **Step 1: Run every gate this slice can affect**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-bounds
go build ./...
go vet ./...
make vet-integration
go test ./... -count=1 -timeout 900s
make test-pg-integration
```

`make test-pg-integration` is the lane this slice's three integration guards actually run in, in CI, via `.github/workflows/go-ci.yml`'s `pg-integration` job. Compare its result against M1's baseline. **If something is red, get a number both ways** - with and without this slice - before calling anything pre-existing.

`make test-race` is the race lane. This slice adds no concurrency; the new tracer's mutex follows the existing one exactly. If the local `-race` lane is unavailable on this machine, **say plainly that `-race` did not run.** Do not substitute `-count=N`: it re-runs under the ordinary scheduler and cannot observe an unsynchronised access that never interleaves badly.

- [ ] **Step 2: Final hygiene sweep over every touched path**

```powershell
git log --stat -2
git ls-files --eol README.md internal/schedrunner/runner.go internal/schedrunner/reconcile_paging_integration_test.go internal/schedrunner/overdue_catchup_row_surface_test.go internal/schedrunner/startup_validation_paging_integration_test.go internal/schedrunner/startup_validation_integration_test.go internal/store/query/scheduled_jobs.sql internal/store/scheduled_jobs.sql.go docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md
```

Every path `i/lf`. Every diffstat proportionate to the change intended. No file outside the list in "File structure".

- [ ] **Step 3: Handoff notes for the conductor**

Report, and do not do any of it yourself:

1. **The item is closable.** All three acceptance criteria are met: peak memory is one page (criterion 1), the no-remainder decision is documented in `ReconcileOnStartup`'s own header (criterion 2), and criterion 3's complement search (M5) found no BOOT-scoped memory claim to correct, while the two genuinely false sites it did surface are fixed here. **Close it with `/backlog close <fragment>`, never by hand-editing `status`** - the command does the `git mv` into `docs/backlog/closed/` that a status flip skips.
2. **The Resolution note must carry three corrections to the item**, all from the spec: `ReconcileOnStartup` lives in `internal/schedrunner/runner.go`, not `startup_validation.go`, so the Related section is wrong; "before the server accepts a request" is true of HTTP and false of gRPC, which starts above the reconcile, so it should read "before the HTTP listener accepts a request"; and **the boot's DURATION is bounded by nothing** - the item must not read as if the exposure is closed.
3. **What may honestly be claimed after this slice**, verbatim, wherever it is written down: *No boot statement's result set grows with the number of enabled or overdue SCHEDULES. `ListGraceCandidates` still grows with the number of workers holding a non-terminal task, at three narrow columns per row. The boot's DURATION is bounded by nothing.* The shorter version - "the boot's peak memory is one page" - is the lossy-aggregate shape this item exists to correct.
4. **Slice 2 of this lane** (`docs/backlog/feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep.md`) now faces **two** unbounded-duration passes ahead of the listener, not one, and a deadline on the sweep is not the same decision as a deadline on the reconcile: a truncated sweep under-reports a diagnostic, while a truncated reconcile leaves schedules overdue and produces one spurious fire each, falsifying README's never-catch-up sentence. Slice 2 must say which of the two it bounds and must not write an acceptance criterion about "the boot".
5. **Two backlog items to file** if Task 0 Step 1 did not already: `ListGraceCandidates`, and the PATCH clobber window (which must not prescribe a fence).
6. **`ROADMAP.md` is not hand-edited.** It regenerates from `docs/backlog/` at the next `/roadmap` refresh.

---

## Self-review

**Spec coverage.** Decision 1 (keyset on `id`, drain loop rejected) -> Tasks 4 and 2. Decision 2 (no ceiling, no remainder) -> Task 5's header. Decision 3 (narrow the column list, rename) -> Task 4, guarded in Task 5 Step 4. Decision 4 (`reconcilePageSize`) -> Task 5 Step 2 and Task 0 M6. Decision 5 (no fence) -> Task 4's SQL comment. Decision 6 (`ctx.Err()`) -> Task 5 Step 3, pinned by Task 3. Guard 1 -> Task 5 Step 4. Guard 2 -> Task 1. Guard 3 -> Task 2, with its input corrected. The wiring guard is untouched and named in "Not touched". All three "genuinely false today" prose rows -> Task 6. Both rename rows -> Tasks 4 and 6, plus the third site the spec missed. The "no edit, and checked" list is carried in the spec and not re-checked here. All six deferred measurements -> Task 0's ledger. The enumeration and the three proposed items -> Task 0 Step 1 and Task 8.

**Placeholders.** Every code step carries the real code. The only deliberately blank things are Task 0's Record boxes, which are blank because filling them from this document is the failure mode the task exists to prevent.

**Type consistency.** `ListOverdueScheduledJobsForCatchupPage` / `...PageParams` / `...PageRow` are used identically in Tasks 4, 5 and 7, and every one of them is explicitly subject to M3 rather than assumed. `reconcileTracer`, `isReconcilePageRead`, `seedOverdueSchedules` and `countAdvanced` are defined once in Task 1 and used unchanged in Tasks 2 and 3. `tracedPool`'s widened parameter is the only edit to a shared helper and both existing call sites are unchanged by construction.
