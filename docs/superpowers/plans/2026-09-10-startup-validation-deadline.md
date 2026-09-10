# A Wall-Clock Deadline on the Startup Validation Sweep - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `schedrunner.ValidateStoredSpecsOnStartup` a wall-clock budget supplied by `RELAY_STARTUP_VALIDATION_DEADLINE` (default `45s`), return the pass's counts and a truncation flag in one value, and make the boot log say plainly when the pass stopped early and what that costs the reader of `scheduled_jobs.last_error`.

**Architecture:** The signature becomes `ValidateStoredSpecsOnStartup(ctx, q, budget time.Duration) (SweepResult, error)` with `context.WithTimeout` built INSIDE the function, so no bounded context exists in `main`'s scope where five long-lived consumers take a `ctx`. `SweepResult` carries `Checked`, `Invalid` and `Truncated` together, and `Truncated` is derived from the child context's `Err()` at every non-nil return - never from the returned error - so a SIGTERM and the budget's own deadline produce different operator-facing output. A new `cmd/relay-server/startupvalidation_config.go` owns the default, the parser and the two boot-line formatters; one formatter owns all four output shapes, so no caller can print a count without the sentence saying whether it is a total.

**Tech Stack:** Go 1.26, pgx/v5 (`pgxpool`, `pgx.QueryTracer`), `internal/testsupport/pgdsn` + testcontainers-go, testify, `go/ast` for the two wiring guards.

**Source spec:** `docs/superpowers/specs/2026-09-10-startup-validation-deadline.md` (committed at HEAD of this worktree).
**Backlog item:** `docs/backlog/feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep.md`.
**Worktree:** `D:/dev/relay/.claude/worktrees/lane-boot-deadline`, branch `claude/lane-boot-deadline`. **Every command below runs from there.** Do **not** `cd D:/dev/relay` - that lands commits on the main repo's `main`.

---

## Slice independence declaration

**This is ONE backend slice, ONE PR, ONE session. There is no frontend slice.** Nothing in `web/` changes, no API response changes, no SQL changes. Phase 3 gets a single lane; there is nothing here to run in parallel.

The tasks are **strictly sequential**:

| Task | Files | Why it cannot run beside its neighbour |
| --- | --- | --- |
| 0 | none (measurements + conductor prerequisites) | Seven downstream decisions read its ledger. |
| 1 | `cmd/relay-server/startupvalidation_wiring_test.go` (new) | Deliberately RED at HEAD. It is the slice's only pre-change RED. |
| 2 | `internal/schedrunner/startup_validation.go` | Leaves the tree not compiling on purpose (8 call sites). |
| 3 | `cmd/relay-server/startupvalidation_config.go`, `..._config_test.go` (both new) | Still not compiling; Task 4 is the only thing that repairs it. |
| 4 | `cmd/relay-server/main.go`, the 7 existing integration call sites, `internal/schedrunner/runner.go` (one clause) | Repairs the build. Turns Task 1's RED green. |
| 5 | none (mutation battery 1, then **Commit A**) | Needs 1-4 written and green; its kills go in A's message. |
| 6 | `internal/schedrunner/startup_validation_deadline_integration_test.go` (new) | Needs the new signature from Task 2. |
| 7 | none (mutation battery 2, then **Commit B**) | Needs Task 6 green; its kills go in B's message. |
| 8 | `README.md` (**Commit C**) | Two of its sentences describe what Tasks 2-4 shipped. |
| 9 | none (full gates, hygiene, handoff) | Last. |

**This plan does NOT need `/backlog phases`.** It contains no multi-session stages. It does need the conductor to file four new backlog items (Task 0, Step 1) and to close one with `/backlog close` (Task 9).

---

## What this plan refutes in the spec

The spec is unusually well-checked: its Decision 1 encapsulation argument, its Decision 3 shutdown-versus-deadline argument, its Decision 4 fence-gap finding, its Decision 6 zero-budget reasoning and its Decision 7 category argument all hold verbatim against the tree. Nine things did not survive checking. Each is corrected inline in the task that owns it; they are collected here so a reviewer diffing plan against spec is not surprised.

### 1. The spec leaves `main`'s NEW job unguarded, and forbids the guard using a reason that does not apply

The spec says `cmd/relay-server/schedrunner_startup_wiring_test.go` "stays byte-identical ... Do not add an assertion about `ListenAndServe` or about the new statements: a criterion that is green before the change pins nothing."

**The premise is right and the conclusion over-reaches.** An assertion about the sweep call's THIRD ARGUMENT is not green before the change - at HEAD the call has two arguments, so it fails immediately. And the gap is real and large: `main` holds at least six `time.Duration` locals (`staleAfter`, `trailingLogWindow`, `watchdogMargin`, `maxAssignment`, the two gRPC admission bounds), every one of which compiles in the new third position. Passing `watchdogMargin` there gives a 2-minute boot budget and every package stays green. This is the same-typed-argument transposition `TestWatchdogConfigIsWiredByMain` already guards positionally for `NewWatchdog`'s two trailing durations - and its comment records that the swap "left `go test ./cmd/relay-server/...` fully green". A slice that hands `main` a new job must pin it.

**What this plan does instead of extending the ordering guard:** a separate file, `cmd/relay-server/startupvalidation_wiring_test.go` (Task 1). The ordering guard's subject is placement; a second subject in one test makes a red ambiguous, which is the spec's own reasoning for not extending the existing cancellation test. The new guard reuses `parseMainBodyAndPkgName` and `schedrunnerPkgPath` from the ordering guard's file **without editing that file**, so it stays byte-identical as the spec requires.

It also closes a second hole the spec does not mention: deleting the `log.Print` of the result line leaves the whole package green and the boot silent about truncation, which is the ITEM's headline defect. Task 1 requires `main` to call `startupValidationLines` exactly once and `startupValidationDeadlineLine` at least once.

### 2. The pgx-wrapping measurement is G5's dependency in the spec and is actually G3's ONLY

The spec says of the returned error: "Whether pgx does wrap is plan Task 0; the decision is correct either way, but **G5's assertion on the returned error** does [depend on it]."

**G5's returned error never passes through pgx.** G5 arranges for the deadline to fire inside a `setOnEnd` hook that runs AFTER `RecordScheduledJobFailure`'s Exec completes. The next thing to run is the row loop's `ctx.Err()` check, which returns the stdlib sentinel `context.DeadlineExceeded` unwrapped. `require.ErrorIs(t, err, context.DeadlineExceeded)` is safe in G5 whatever pgx does.

**G3 is the test that depends on it**, because with `budget = 0` the first thing to fail is the page query and the error comes back from pgx (probably from `pgxpool`'s acquire). M3 in Task 0 is therefore a prerequisite for G3's error assertion, not G5's, and Task 6 writes both branches of it so neither can be assumed.

### 3. G6's fixture cannot kill the mutation its own field comment is about

The spec's `Invalid` comment says the field counts VERDICTS, not writes - and G6 plants 5 healthy + 3 freshly-broken rows, where every verdict produces a landed write. `Invalid` is 3 under both the correct code and a mutant that increments after the `n == 0` continue. The guard for the asymmetry is absent.

**Fix: G6 runs the sweep TWICE.** On the second pass the three broken rows already carry the identical message, so `last_error IS DISTINCT FROM sqlc.arg(last_error)` makes every UPDATE a no-op - zero writes, three verdicts. `res.Invalid == 3` on the second pass is what kills the count-writes mutant, and it is the existing contract `TestValidateStoredSpecsOnStartup_ReRecordingAnIdenticalMessageIsANoOp` already established, so the fixture is not speculative.

### 4. G4 needs an exact `Checked` assertion, or moving the counter above the fence survives

The spec's G4 asserts `errors.Is(err, context.Canceled)`, `res.Truncated == false` and `countRecordedFailures == 1`. All three survive a mutant that increments `res.Checked` ABOVE the `ctx.Err()` check, which counts a row the pass never validated. G5's `Checked` assertion is a deliberate range and cannot see it either.

**Fix: G4 also asserts `res.Checked == 1` exactly.** It is fully deterministic (the hook cancels after row 1's write, so row 2's iteration returns at the fence), and it supplies the second exact sibling that brackets G5's loosened middle assertion - the spec asks for brackets and names only G3 and G6.

### 5. The spec's prose list misses the one sentence in `startup_validation.go` the change makes flatly false

The spec's "Prose this slice owes" table covers the `THAT IS NARROWER...` paragraph and adds a new one. It does not mention the paragraph above it, which reads: "Two things ARE returned: a page query's error, and the cancellation the row loop checks for. The caller in cmd/relay-server logs either as a warning."

After this slice **three** things are returned (the third being this pass's own expired budget), and the caller logs them as three different shapes, only one of which is a warning. Task 2 corrects that sentence.

### 6. `main.go`'s placement comment needs one sentence, and the spec's "no edit" row is about the wrong half

The spec's "True today and after" table says `main.go`'s long placement comment needs no edit because "Placement is unchanged". Placement is indeed unchanged. But that comment's last paragraph says "a page query's error and a mid-sweep cancellation are returned and logged here as a warning" - the same enumeration as refutation 5, now missing the deadline case and wrong about "as a warning" for it. Task 4 edits exactly that one sentence and nothing else in the comment.

### 7. The default constant belongs in `cmd/relay-server`, and the spec never says why

Three sibling defaults live in the package that ENFORCES them (`api.DefaultMaxSchedulesPerOwner`, `worker.DefaultTrailingLogWindow`, `scheduler.DefaultWatchdogMargin`), so `schedrunner.DefaultStartupValidationDeadline` is the shape a reader expects. It is refused here and the reason is Decision 6: each of those three is the fallback its own package applies when the field is ZERO. `schedrunner` has no fallback, by design - a zero budget is an expired deadline. A default constant in that package is exactly the "0 means the default" temptation Decision 6 forbids, sitting in the file that forbids it. `defaultStartupValidationDeadline` is an unexported const in `cmd/relay-server/startupvalidation_config.go`.

### 8. `errors.Is` at the page-query site cannot be collapsed into a `defer`, and the reason is mutation coverage

A `defer` over named returns would satisfy "set on every error path" structurally and is the tidier code. It is refused: it collapses both error paths into ONE statement, so a single mutation kills both G3 and G4 and no mutation can distinguish the page-query site from the row-loop site - which is precisely the distinction the spec calls the likeliest wrong implementation. Two explicit assignments give two independent kill sites: B2.2 (delete the page-query one) dies on G3 alone, B2.1 (hardcode the row-loop one) dies on G4 alone. A deferred version also silently sets `Truncated` on the `nil`-error return when the budget expires after the last short page - a complete pass reported as truncated.

### 9. The truncated line is emitted as THREE lines, not one - see "Decision: the truncated line's shape" below

The spec's single ~500-character line is refused on readability, and no clause is cut.

### Spec claims checked and SURVIVING, recorded so nobody re-litigates them

- **`Truncated` from the child context is expressible against the current control flow.** `ValidateStoredSpecsOnStartup` has exactly two non-nil return sites - the page query's `if err != nil { return err }` (line 119-121) and the row loop's `if err := ctx.Err(); err != nil { return err }` (line 131-133). The per-row record failure and the `n == 0` branch both `continue` and return nothing. Two sites, two one-line additions.
- **No `budget <= 0` branch is needed.** `context.WithTimeout(ctx, 0)` returns an already-expired context with no special case; `ctx.Err()` is `context.DeadlineExceeded` immediately. Negative is identical (deadline in the past). The only caller that could pass a non-positive value is a test, because `parseStartupValidationDeadline` folds zero and negative to the default.
- **`Invalid` counts verdicts, and `RecordScheduledJobFailure` really does return 0 for two different ordinary reasons.** Read off the statement in `internal/store/query/scheduled_jobs.sql:389-397`: the fence is `AND job_spec = ... AND cron_expr = ... AND timezone = ...` (a repair through another replica), and the idempotence predicate is `AND last_error IS DISTINCT FROM sqlc.arg(last_error)` (the identical message is already stored - the steady state for the whole life of a broken schedule). Both confirmed.
- **Truncation produces only false negatives, because the sweep has no clearing sibling ON THIS PATH.** Confirmed twice: `RecordScheduledJobFailure`'s own header says "RECORD-ONLY: there is no clearing sibling for the sweep, on purpose", and the sweep's body issues exactly two statements - `ListEnabledScheduledJobsPage` and `RecordScheduledJobFailure`. Nothing in `ValidateStoredSpecsOnStartup` can clear a `last_error`. (`AdvanceScheduledJob` does clear it, and it is the ticker's, not the sweep's - which is what Decision 4's refusal of a resumed pass is about.)
- **This slice adds no Makefile and no workflow change, and both CLAUDE.md-required links exist.** Confirmed, not restated: `internal/schedrunner/runner_test.go:28` takes its DSN from `pgdsn.NewIntegrationDSN(t)`; `Makefile:168`'s `test-pg-integration` recipe names `./internal/schedrunner/...` **and** `./cmd/relay-server/...`; `.github/workflows/go-ci.yml:184` runs `make test-pg-integration` in the `pg-integration` job. The four untagged guards additionally run in CI's `test` job (`go test -race ./...`, no tags).
- **No migration.** This slice touches no schema and no `.sql` file. **If you conclude a `.sql` edit is needed, STOP and report** - the sibling lane holds `000024`, and a `.sql` comment edit would additionally drag in `make generate` and this repo's CRLF/sqlc revert hazard, which this slice is otherwise free of.
- **`TestSchedrunnerStartupSweepIsWiredInOrderByMain` stays green through the signature change**, and Task 4 Step 6 verifies rather than assumes it. Why it stays green: `findPkgCalls` counts `<pkg>.ValidateStoredSpecsOnStartup` CallExprs and tags each with the index of main's top-level statement containing it, plus whether it sits inside a `FuncLit`/`GoStmt`/`DeferStmt`. After the change the call is still exactly one hit, still a direct child of main's body (an `AssignStmt` rather than an `IfStmt`, which the guard does not care about), and still inside none of those three. The four new statements around it (the parse, the `if warn != ""`, the bounds `log.Print`, the `start := time.Now()`) and the `for ... range` print loop contain no schedrunner selector, so they move both indices up without reordering them: `reconcile < sweep < runner` holds. **The print loop is a `RangeStmt`, which is NOT in `findPkgCalls`'s async set** - and that is fine, because the sweep call is not inside it.

---

## Decision: the truncated line's shape - THREE lines, no clause cut

The conductor asked for a decision and a justification. **Three consecutive `log.Print` lines, produced by one formatter returning `[]string`.** Every clause of the spec's draft survives; nothing is shortened.

**Why not one line.** At ~500 characters with five clauses it is the longest log line in the binary by a factor of about 1.4 over the largest precedent (`parseWatchdogDuration`'s sub-floor warning, ~330 characters). A boot log is read in a terminal, usually soft-wrapped; one line means the operator finds the remedy ladder by scanning a wrapped paragraph for the word "raising".

**Why three is safe, which is the part that needs an argument.** Splitting a disclosure normally breaks the spec's own safety property - a reader who sees only the first line gets a count without its caveat. Two rules make it safe here:

1. **The count and the words `THESE ARE FLOORS, NOT TOTALS` are on the SAME line.** The split is allowed only because of this. Line 1 alone is still honest; lines 2 and 3 add detail, not the caveat. G2 asserts this, so the split rule is pinned rather than remembered.
2. **Every line of every shape begins with the same prefix, `schedrunner: startup validation`.** That is the phrase an operator greps after seeing one of them, so a grep retrieves the whole block rather than the headline. G2 asserts the prefix on every line of every shape.

**Why not embedded `\n` in one `log.Print`.** `log` prefixes the record, not each line, so continuation lines would arrive with no timestamp and a line-based log shipper would emit two malformed events. Three `log.Print` calls give three well-formed, individually timestamped, individually greppable records.

**The new risk the split introduces, and its closure.** A caller could print a prefix of the slice. `main` prints with a two-line `for ... range` that has nothing to index, and Task 1's guard requires `startupValidationLines` to be called exactly once in `main`'s body. Rule 1 above means even the worst partial print is not dishonest.

The seams are the natural ones: **(1)** verdict + bound + elapsed + both counts + the floors label; **(2)** what the loss means - unknown remainder size, and the absence-versus-presence asymmetry; **(3)** the remedy ladder, tightening-first, plus where the remainder went.

---

## File structure

| File | Change | Task |
| --- | --- | --- |
| `cmd/relay-server/startupvalidation_wiring_test.go` | **new, UNTAGGED.** G7: the sweep call's third argument derives from `parseStartupValidationDeadline` and names `RELAY_STARTUP_VALIDATION_DEADLINE`; `main` calls both formatters. | 1 |
| `internal/schedrunner/startup_validation.go` | `SweepResult`; `budget time.Duration` parameter; `(SweepResult, error)` return; `context.WithTimeout` + `defer cancel()`; `res.Checked++` / `res.Invalid++`; `res.Truncated` at both error returns; four header paragraphs corrected or added | 2 |
| `cmd/relay-server/startupvalidation_config.go` | **new.** `defaultStartupValidationDeadline`, `parseStartupValidationDeadline`, `startupValidationDeadlineLine`, `startupValidationLines` | 3 |
| `cmd/relay-server/startupvalidation_config_test.go` | **new, UNTAGGED.** G1 (parser table), G1b (the default's literal value), G2 (truncated shape), G2b (shutdown shape), G2c (completed and empty shapes) | 3 |
| `cmd/relay-server/main.go` | boot region only: the parse, the warning, the bounds line, `time.Now()`, the new call shape, the print loop, removal of the `if err != nil` branch, and ONE sentence of the placement comment | 4 |
| `internal/schedrunner/startup_validation_integration_test.go` | one call site (line 94) | 4 |
| `internal/schedrunner/startup_validation_fence_integration_test.go` | three call sites (lines 130, 194, 214) | 4 |
| `internal/schedrunner/startup_validation_paging_integration_test.go` | three call sites (lines 179, 226, 278) + two added assertions on line 179's test | 4 |
| `internal/schedrunner/runner.go` | one trailing clause of `ReconcileOnStartup`'s header. No code change. | 4 |
| `internal/schedrunner/startup_validation_deadline_integration_test.go` | **new.** G3, G4, G5, G6, `seedHealthySchedules` | 6 |
| `README.md` | startup-sequence item 5; one env-var row; the `failing` row; one `last_error` paragraph | 8 |

**Not touched, deliberately:** `cmd/relay-server/schedrunner_startup_wiring_test.go` (must stay byte-identical), any `.sql` or `*.sql.go` or `models.go`, `internal/store/migrations/`, `Makefile`, `.github/workflows/`, `web/` and `web/dist/`, `ROADMAP.md` (regenerated by `/roadmap` from `docs/backlog/`), and the sibling lane's `internal/agent/source/perforce/`, `internal/worker/`, `internal/store/query/worker_workspaces.sql` and README's source-workspaces field table.

---

## Standing rules for every task

Read once; not repeated per step.

- **Work only inside `D:/dev/relay/.claude/worktrees/lane-boot-deadline`.** A sibling lane is running Go tests and Docker containers on this machine. Do not touch `D:/dev/relay` or any sibling worktree, and do not stop containers you did not start.
- **Plan-supplied test bodies are guesses.** Every code block below is intent plus the assertion that matters. Run each RED yourself and record the number you actually saw. **If a number differs from the one written here, STOP and report - do not adjust the assertion to match.**
- **A mutation battery needs a green baseline** (Task 0, M1). Uniform results mean a broken harness. **A compile error is not a kill.**
- **Mutations run through `go test -overlay`, never by editing the worktree** (recipe in Task 5, Step 1). A sibling agent reads this machine; and an overlay cannot be "forgotten restored". **Never `git checkout --` anything.**
- **Verify each mutation actually applied**: read the mutant file back before running, and require the control mutation to die FIRST.
- **Commit with an explicit pathspec.** `git add <paths>`, never `git add -A`. Sibling agents share this machine's git index. Read `git reflog` before any reset.
- **After ANY programmatic edit to a tracked text file**, before committing: print before/after line counts, check `git diff --stat` against the size of the change you intended, run `git ls-files --eol` on every touched path (each must read `i/lf`), and confirm the file still decodes as UTF-8. `git diff` and `git status` disagree by design on this repo - never conclude "nothing to revert" from `git diff` alone. Prefer exact-anchor replacement over a section rewrite.
- **Comments state a hazard or constraint the code cannot show.** No dates, no change history, no session or measurement narrative, no counts of other code, no uniqueness or completeness claims about OTHER code. A function's own contract is fine and is required here. **Measured numbers go in the commit message, never in a comment.**
- Never use em dashes or en dashes. Regular hyphens only.

---

## Task 0: Measurement ledger and conductor prerequisites

**Nothing in the spec was measured** - it was written without a Bash tool, and it says so. Everything below is an instruction to OBTAIN a value, not a value. **Do not fill a Record box from this plan, from the spec, or from what you expect.** An unfilled box blocks the task that cites it. If a measurement cannot be taken, write "NOT OBTAINED" and the reason, and say so in the handoff - do not substitute a different instrument.

**Files:** none. This task produces recorded values and filed backlog items.

### The ledger

| # | Measurement | Taken in | Record |
| --- | --- | --- | --- |
| M1 | Green baseline: `go test ./...` and `make test-pg-integration`, BEFORE any edit | Step 2 | PASS / FAIL + failing test names: `______` |
| M2 | Sweep throughput in two regimes, **with its inputs** | Step 3 | healthy rows/s `______`, broken rows/s `______`, rows 45s covers `______`, inputs: `______` |
| M3 | Does pgx's error for a query whose context expired satisfy `errors.Is(err, context.DeadlineExceeded)`? | Step 4 | YES / NO; verbatim error text: `______` |
| M4 | Does `WithTimeout(ctx, 0)` produce a PROMPT error from the first page query (not a hang, not a panic)? | Step 4 | elapsed `______`; prompt / hung / panicked |
| M5 | G5's timing: first-page-read duration, and the budget + sleep chosen from it | Step 5 | page read `______`, budget `______`, sleep `______` |
| M6 | Is any sibling lane editing README's scheduled-jobs section? | Step 6 | `______` |
| M7 | The 7 existing call sites after the edit, and whether any went vacuous | Task 4 Step 5 | per-site wording + lane result: `______` |
| M8 | Stale-prose complement search for "the sweep's duration is bounded by nothing" | Step 7 | hit count `______`, SWEEP-scoped sites `______`, other-scoped `______` |
| M9 | G7's RED at HEAD | Task 1 Step 3 | verbatim failure: `______` |
| M10 | The boot region's real log output, verbatim, from a started server | Task 4 Step 7 | pasted lines: `______` |
| M11 | README line count before and after, and `git ls-files --eol` | Task 8 Step 4 | before `______`, after `______`, eol `______` |

- [ ] **Step 1: Conductor files four backlog items**

The engineer does not run `/backlog`. From the spec's "Backlog items this spec proposes", the conductor files:

1. **A resumable startup validation pass needs a fence that covers a concurrent CLEAR.** `RecordScheduledJobFailure` fences on `job_spec`, `cron_expr` and `timezone`; `AdvanceScheduledJob` clears `last_error` without touching any of them, so a sweep continuing after the runner starts can stamp a stale verdict over a fresh clear. **The item must NOT prescribe the fence** - `last_error_at` and a generation column are both candidates and the wrong one is worse than no continuation. Filing it is what makes Decision 4's refusal falsifiable.
2. **`GET /v1/scheduled-jobs/stats`'s `failing` is a floor and nothing on the wire says so.** Three independent causes of under-count: a never-fired never-swept schedule, a truncated boot sweep, and the sweep's own fence non-matches. The remedy is a disclosure on the response or in the SPA strip, not a change to the count. Owned by `internal/api` and `web/`.
3. **A deadline on `ReconcileOnStartup` needs a policy for the rows it does not reach.** The design question is "what happens to unreconciled schedules", not "what number": each unreconciled row costs one unscheduled fire on the ticker, which falsifies README's never-catch-up contract.
4. **`store.Migrate` has no duration bound and deliberately no statement timeout.** `applyStatementTimeout`'s own header records that `RELAY_DB_STATEMENT_TIMEOUT` cannot reach it. Probably correct as designed; worth an item so the boot's enumeration is complete.

**Nothing in this plan cites any filed item by filename**, so no task blocks on the result.

One observation for the conductor to accept or drop, NOT filed by this plan: nothing guards that `main` still prints `watchdogBoundsLine`'s output either - deleting that `log.Print` leaves the package green. This slice guards its own two boot lines (Task 1) and does not reach across to the watchdog's.

- [ ] **Step 2: M1 - green baseline BEFORE any edit**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-deadline
git status --porcelain          # note anything already dirty; it is not yours
go build ./...
go test ./... -count=1
make test-pg-integration
```

Record PASS/FAIL and, on FAIL, the exact test names. **A red here is not a reason to proceed quietly** - a later "kill" measured against an already-red baseline is not a kill. If a failure is outside `internal/schedrunner` and `cmd/relay-server`, record it and report before continuing.

- [ ] **Step 3: M2 - sweep throughput in two regimes, reported WITH its inputs**

This is the measurement that may move the 45s default, so it is the one that must not be guessed. **Report the input alongside every number** - a bare rows-per-second figure reads as the typical case.

Create a THROWAWAY file `internal/schedrunner/zz_throughput_scratch_test.go`. It is deleted in this same step; it must never be committed.

```go
//go:build integration

package schedrunner_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// fatSpecJSON is a spec that VALIDATES and is large enough that per-row CPU is
// not noise: 200 tasks, one command each.
func fatSpecJSON(t *testing.T) []byte {
	t.Helper()
	tasks := make([]map[string]any, 0, 200)
	for i := 0; i < 200; i++ {
		tasks = append(tasks, map[string]any{
			"name":    "task-" + string(rune('a'+i%26)) + "-" + time.Duration(i).String(),
			"command": []string{"echo", "hello from a realistically sized job spec row"},
		})
	}
	spec, err := json.Marshal(map[string]any{"name": "fat", "tasks": tasks})
	require.NoError(t, err)
	return spec
}

func seedN(t *testing.T, h *runnerHarness, owner pgtype.UUID, prefix string, spec []byte, n int) {
	t.Helper()
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO scheduled_jobs (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at)
		SELECT $4 || g::text, $1, '@hourly', 'UTC', $2::jsonb, 'skip', TRUE, NOW() + INTERVAL '720 hours'
		FROM generate_series(1, $3::int) AS g`, owner, string(spec), n, prefix)
	require.NoError(t, err)
}

func TestScratchSweepThroughput(t *testing.T) {
	const rows = 2000
	for _, tc := range []struct {
		name string
		spec func(*testing.T) []byte
	}{
		{"all-healthy-small", makeSpecJSON},
		{"all-healthy-fat", fatSpecJSON},
		{"all-broken-small", makeOverBudgetSpecJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRunnerHarness(t)
			owner := h.createUser(t, tc.name+"@example.com")
			spec := tc.spec(t)
			seedN(t, h, owner, tc.name+"-", spec, rows)

			var bytesPerRow int
			require.NoError(t, h.pool.QueryRow(context.Background(),
				`SELECT octet_length(job_spec::text) FROM scheduled_jobs LIMIT 1`).Scan(&bytesPerRow))

			start := time.Now()
			res, err := schedrunner.ValidateStoredSpecsOnStartup(
				context.Background(), h.q, 10*time.Minute)
			elapsed := time.Since(start)
			require.NoError(t, err)

			t.Logf("REGIME=%s rows=%d job_spec_bytes=%d checked=%d invalid=%d elapsed=%s rows_per_sec=%.1f rows_in_45s=%.0f",
				tc.name, rows, bytesPerRow, res.Checked, res.Invalid, elapsed,
				float64(res.Checked)/elapsed.Seconds(),
				float64(res.Checked)/elapsed.Seconds()*45)
		})
	}
}
```

**This file uses the NEW signature, so it only runs after Task 2.** Run it at the end of Task 4, before Task 5's commit:

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-deadline
go test -tags integration -count=1 ./internal/schedrunner/... -run TestScratchSweepThroughput -v -timeout 1800s
```

Record, for each of the three regimes: row count, `job_spec` bytes per row, elapsed, rows/sec, rows covered in 45s. **Record the input environment too**: container mode (Docker testcontainer) or `RELAY_TEST_DATABASE_URL` shared server, and whether the database is on this host.

Then:

```powershell
Remove-Item internal/schedrunner/zz_throughput_scratch_test.go
git status --porcelain   # must NOT list zz_throughput_scratch_test.go
```

**If 45s does not cover a plausible fleet** - take "a plausible fleet" as 10,000 enabled schedules at the fat spec size - **STOP and report the number.** The conductor decides whether the default moves; the three constraints in Decision 7 (above `RELAY_DB_STATEMENT_TIMEOUT`'s 30s, below an orchestrator probe budget, above a healthy fleet's pass) are what a new number must satisfy, and whatever it ends up being goes in Commit A's message with its input.

- [ ] **Step 4: M3 and M4 - the expired-context error, and that it is prompt rather than a hang**

Both are about the same call and both are taken from G3 itself (Task 6), so this step is a preparation: **write down, now, what each outcome means for G3's assertions, so neither branch can be assumed later.**

- **M3 = YES** (`errors.Is(err, context.DeadlineExceeded)` holds for the error pgx returns): G3 asserts `require.ErrorIs(t, err, context.DeadlineExceeded)` in addition to `require.Error`.
- **M3 = NO**: G3 asserts `require.Error(t, err)` only, plus a comment in the test naming the error text that was actually observed and stating that the truncation verdict comes from the context rather than from this error. **Do not assert a substring of pgx's message** - that is parsing another component's prose.

Record M3 by running G3 with BOTH assertions present and reading which one fails, then deleting the one that does not hold. Record M4 from G3's elapsed assertion: the test fails loudly if the call is not prompt, which is the bounded failure a gated test needs (a hang under mutation is indistinguishable from infrastructure trouble). **If the call HANGS rather than failing promptly, STOP and report** - G3 depends on promptness entirely and a different guard would be needed.

- [ ] **Step 5: M5 - G5's sleep timing**

Confirm from `internal/schedrunner/startup_validation_paging_integration_test.go:52-60` that `sweepTracer.TraceQueryEnd` calls the hook synchronously, on the caller's goroutine, after the statement has completed (the existing cancellation test depends on this shape, but not for a SLEEPING hook - which is the new thing). Then measure the first page read of 150 rows: the `TestScratchSweepThroughput` log line above gives the per-row cost; 150 rows is one page plus 50.

Pick G5's budget and sleep from the measurement, not from this plan. **Starting point: budget 10s, sleep 12s.** Raise both proportionally if a 150-row page read measures above 2s on this machine. Record both values and the measured page-read time.

- [ ] **Step 6: M6 - sibling lane check, then move on**

**The conductor states there is exactly one other open lane and it owns `internal/agent/source/perforce/`, `internal/worker/` and README's source-workspaces field table - NOT the scheduled-jobs section.** Confirm at the moment of the README edit (Task 8), not now: in a concurrent batch the complement moves while you count it.

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-deadline
git fetch origin
git log --oneline origin/main -5
git log --oneline -3 --all -- README.md
```

Record the answer. **The cut line is therefore NOT taken**: Task 8 ships all four README edits, and Commit C's message says so.

- [ ] **Step 7: M8 - the stale-prose complement search**

This slice changes a GLOBAL property ("the sweep's duration is bounded by nothing"), which stales prose in files this slice otherwise does not touch. That is a claim about a complement and must be SEARCHED for with a recorded count, never inherited from the spec's list.

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-deadline
rg -n -i "bounded by nothing|unbounded duration|bounds that number|wall clock is still|wall-clock deadline-on-the-boot-sweep|wall-clock-deadline-on-the-boot-sweep" . > "$env:TEMP\claude\D--dev-relay--claude-worktrees-roadmap-now-dependencies-581b21\50da71ee-f761-480b-a23d-f1f7d875cc3b\scratchpad\staleprose.txt"
rg -n -c "bounded by nothing" .
```

Classify every hit into three buckets and record the counts:

- **SWEEP-scoped** - a claim about `ValidateStoredSpecsOnStartup`'s duration. Becomes false. Work. Known: `internal/schedrunner/startup_validation.go`'s `THAT IS NARROWER...` paragraph (Task 2), `internal/schedrunner/runner.go`'s `...the same trade ValidateStoredSpecsOnStartup makes` clause (Task 4), README item 5 (Task 8).
- **RECONCILE-scoped or MIGRATE-scoped** - stays true. `ReconcileOnStartup`'s own "THE DURATION IS BOUNDED BY NOTHING" stays, and is the tripwire for whoever bounds it.
- **Dated records** - specs, plans, retros, `docs/backlog/`, `ROADMAP.md`. **Do not edit any of these.** A dated document records a moment.

Two known hits that need a judgement call, pre-resolved so nobody reopens them:

- README's `RELAY_MAX_SCHEDULES_PER_OWNER` row ends "**It bounds the boot sweep's STARTING work set, not the sweep's duration:** every page of `ValidateStoredSpecsOnStartup` is a fresh snapshot...". **LEAVE IT.** It is exactly true - the cap still does not bound the duration - and rewriting it would author a fresh claim where nothing is wrong. The new env-var row sits adjacent and names what does bound the duration.
- `internal/store/query/scheduled_jobs.sql`'s `ListEnabledScheduledJobsPage` comment says the mid-pass INSERT residual is "duration amplification rather than non-termination". Still true. **LEAVE IT** - and note that editing it would require `make generate`, which this slice must not need.

**If you find a SWEEP-scoped hit outside the three known sites, STOP and report** before deciding correct-or-delete. Prefer DELETE: a sentence whose only job was to say the duration was unbounded has no replacement to write.

---

## Task 1: G7 - main's new argument is wired, and both boot lines are printed

**Files:**
- Create: `cmd/relay-server/startupvalidation_wiring_test.go`

### The property this task pins, and why the input discriminates

**Property:** `main` hands `schedrunner.ValidateStoredSpecsOnStartup`'s third parameter a value derived from `parseStartupValidationDeadline` applied to `RELAY_STARTUP_VALIDATION_DEADLINE`, and prints both boot lines.

**Why an AST guard and not something cheaper.** `main` is not callable from a test, it opens a pool, and it can `log.Fatalf` before reaching this line, so nothing executable in any lane this package has can observe the argument. That is the stated condition for this repo's parser-guard family (`TestMain_PassesTheScheduleCapItParsed`, `TestWatchdogConfigIsWiredByMain`, `TestTrailingLogWindowIsWiredIntoTheHandler`).

**The discriminating input is `main`'s own source, and the discriminator is the env-var literal.** `main` holds at least six `time.Duration` locals (`staleAfter`, `trailingLogWindow`, `watchdogMargin`, `maxAssignment`, and the two gRPC admission bounds). Every one of them compiles in the third position and nothing about the type says which number arrived, so the chain must be required to mention this control's env var and no other `RELAY_` name.

**This is the slice's only RED that exists before the change**: at HEAD the call has two arguments.

- [ ] **Step 1: Write the guard**

Create `cmd/relay-server/startupvalidation_wiring_test.go`:

```go
package main

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStartupValidationDeadlineIsWiredAndLoggedByMain pins the two jobs this
// slice gives main, neither of which any executable test can see.
//
// (1) THE THIRD ARGUMENT IS THE OPERATOR'S BUDGET AND NOT ANOTHER DURATION.
// main holds several time.Duration locals and every one of them compiles in that
// position, so the type says nothing about which number arrived. The chain is
// required to reach parseStartupValidationDeadline AND to mention
// RELAY_STARTUP_VALIDATION_DEADLINE, and to mention no other RELAY_ name, so an
// argument built from two controls is RED rather than passing on the half it
// happens to name.
//
// (2) THE OUTCOME LINE IS PRINTED AT ALL. Deleting the result line leaves every
// package green and the boot silent about a truncated pass, which is the defect
// the whole slice exists to close.
//
// IT DOES NOT OWN THE CALL'S PLACEMENT. TestSchedrunnerStartupSweepIsWiredInOrderByMain
// owns that; the single-call requirement here is only so there is one argument
// list to index.
//
// WHAT IT CANNOT SEE, so its name is not read as more than it checks: a value
// laundered through an intermediate local is followed, a value TRANSFORMED on the
// way is not; and a result line computed and then discarded satisfies it.
func TestStartupValidationDeadlineIsWiredAndLoggedByMain(t *testing.T) {
	body, pkg := parseMainBodyAndPkgName(t, schedrunnerPkgPath)

	// from[name] = identifiers AND unquoted string literals the RHS mentions,
	// collected only from assignments that are DIRECT children of main's body, so
	// a parse moved inside an if reaches nothing.
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
				if bl, ok := m.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					if unquoted, err := strconv.Unquote(bl.Value); err == nil {
						rhs = append(rhs, unquoted)
					}
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

	var sweepCalls []*ast.CallExpr
	plainCalls := map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := ce.Fun.(type) {
		case *ast.SelectorExpr:
			id, ok := fn.X.(*ast.Ident)
			if ok && id.Name == pkg && fn.Sel.Name == "ValidateStoredSpecsOnStartup" {
				sweepCalls = append(sweepCalls, ce)
			}
		case *ast.Ident:
			plainCalls[fn.Name]++
		}
		return true
	})

	require.Len(t, sweepCalls, 1,
		"main's body must call %s.ValidateStoredSpecsOnStartup exactly once so this guard has one "+
			"argument list to index. Found %d.", pkg, len(sweepCalls))
	call := sweepCalls[0]
	require.Len(t, call.Args, 3,
		"%s.ValidateStoredSpecsOnStartup is called with %d arguments; this positional check is written "+
			"against the 3-parameter signature (ctx, q, budget). If the signature changed, update the "+
			"position below - do not delete the check.", pkg, len(call.Args))

	id, isIdent := call.Args[2].(*ast.Ident)
	require.True(t, isIdent,
		"the budget argument must be a plain identifier so this guard can trace which env var it was "+
			"parsed from; got %T. A literal there is a hard-coded budget that "+
			"RELAY_STARTUP_VALIDATION_DEADLINE no longer controls.", call.Args[2])

	seen := map[string]bool{}
	queue := []string{id.Name}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		queue = append(queue, from[name]...)
	}
	var otherEnv []string
	for name := range seen {
		if strings.HasPrefix(name, "RELAY_") && name != "RELAY_STARTUP_VALIDATION_DEADLINE" {
			otherEnv = append(otherEnv, name)
		}
	}

	require.True(t, seen["parseStartupValidationDeadline"],
		"the budget argument is %q, which does not derive from parseStartupValidationDeadline through "+
			"an unconditional assignment in main's body. Crossing it with another duration local - "+
			"staleAfter, watchdogMargin, trailingLogWindow - compiles and leaves every package green "+
			"while the boot runs on some other control's number.", id.Name)
	require.True(t, seen["RELAY_STARTUP_VALIDATION_DEADLINE"],
		"the budget argument is %q, whose chain never mentions RELAY_STARTUP_VALIDATION_DEADLINE. Every "+
			"candidate local here is a time.Duration, so the env-var name is the only thing that says "+
			"WHICH duration arrived.", id.Name)
	require.Empty(t, otherEnv,
		"the budget argument is %q, whose chain reaches %v - another control's variable - so this guard "+
			"cannot say which number it carries.", id.Name, otherEnv)

	require.Equal(t, 1, plainCalls["startupValidationLines"],
		"main's body must call startupValidationLines exactly once. Zero means a truncated sweep says "+
			"NOTHING in the boot log, which is the lossy-aggregate defect this slice exists to close; "+
			"more than once means two outcome lines for one pass. Found %d.",
		plainCalls["startupValidationLines"])
	require.GreaterOrEqual(t, plainCalls["startupValidationDeadlineLine"], 1,
		"main's body must print the bounds line. The boot log between the dispatcher and "+
			"'HTTP listening on' is otherwise silent, so without it an operator watching a slow boot "+
			"sees nothing at all during the window this knob governs.")
}
```

- [ ] **Step 2: Confirm the helpers it reuses exist and that their file is untouched**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-deadline
rg -n "func parseMainBodyAndPkgName|schedrunnerPkgPath =" cmd/relay-server/
git diff --stat -- cmd/relay-server/schedrunner_startup_wiring_test.go
```

Expected: both symbols found in `cmd/relay-server/schedrunner_startup_wiring_test.go`, and an EMPTY diffstat for that file. **That file must stay byte-identical for the whole slice.**

- [ ] **Step 3: Run it to verify the RED (M9)**

```powershell
go test ./cmd/relay-server/... -run TestStartupValidationDeadlineIsWiredAndLoggedByMain -v -count=1
```

Expected: FAIL on the argument count - something of the form `ValidateStoredSpecsOnStartup is called with 2 arguments; this positional check is written against the 3-parameter signature`. Record the verbatim message as M9.

**If it fails on anything else first** (for example a compile error in the package, or zero sweep calls), STOP and report: the guard is not measuring what it claims.

**Do not commit.** The tree is intentionally red until Task 4. Tasks 2 and 3 make it worse (the package stops compiling) before Task 4 repairs everything.

---

## Task 2: The library change - `SweepResult`, the budget, and the truncation verdict

**Files:**
- Modify: `internal/schedrunner/startup_validation.go` (header lines 85-103 rewritten; lines 77-83 one sentence; signature line 108; body lines 108-179)

### The property this task delivers

The pass is bounded at `budget` plus one row's work, it reports how far it got and how many verdicts it formed, and it says whether the budget - as distinct from a shutdown - is what stopped it.

- [ ] **Step 1: Add the imports and the result type**

In the import block, add `"errors"` and `"time"` (keep the existing grouping: stdlib first, then `relay/...`, then third-party).

Insert `SweepResult` immediately above `ValidateStoredSpecsOnStartup`'s header block:

```go
// SweepResult is what one ValidateStoredSpecsOnStartup pass observed.
//
// THE THREE FIELDS TRAVEL TOGETHER ON PURPOSE. A caller that wanted to print
// Checked without the caveat would have to actively ignore an adjacent field of
// the same struct, which is stronger than a boolean the call site reconstructs.
type SweepResult struct {
	// Checked is how many enabled rows this pass validated.
	Checked int
	// Invalid is how many of those no longer validate. It counts VERDICTS, not
	// writes: a verdict whose UPDATE was refused by the content fence, or
	// errored, or was already recorded, is counted here and produced no log line.
	Invalid int
	// Truncated means the budget ran out. When it is true, Checked and Invalid are
	// FLOORS over an enabled set whose size this pass never learned.
	Truncated bool
}
```

`Elapsed` is deliberately not a field: the caller owns the clock and measures it with `time.Since`.

- [ ] **Step 2: Correct the `A PER-ROW FAILURE` paragraph's last two sentences**

Exact-anchor replacement inside the existing header. Replace:

```
// operator-visible schedule problem into a server that will not start would be
// strictly worse than the invisibility this sweep exists to fix.
```

keeping that text, but first replace the two sentences above it. Replace exactly:

```
// line rather than the remaining schedules. Two things ARE returned: a page
// query's error, and the cancellation the row loop checks for. The caller in
// cmd/relay-server logs either as a warning. Converting an
```

with:

```
// line rather than the remaining schedules. THREE things ARE returned: a page
// query's error, a parent cancellation, and this pass's own expired budget. The
// caller distinguishes them - only one of the three is a warning. Converting an
```

- [ ] **Step 3: Replace the `THAT IS NARROWER...` paragraph**

Replace the whole paragraph from `// THAT IS NARROWER THAN "THIS SWEEP CANNOT STOP THE BOOT"` through `// docs/backlog/feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep.md.` with:

```go
// THE PASS IS BOUNDED AT budget PLUS ONE ROW'S WORK, and stating it as exactly
// budget would be the overclaim. The ctx.Err() check is at the top of the ROW
// loop, so the last row admitted before the deadline still runs its full
// jobspec.Validate - up to maxCommandsPerJob commands - and its
// RecordScheduledJobFailure round trip past it. THAT RESIDUAL IS A PER-ROW
// CONSTANT: it does not grow with the number of stored schedules, which is the
// property a bound on this pass has to deliver. Peak memory stays one page.
//
// WHAT THE BOUND COSTS INSTEAD IS COVERAGE, and the quantity that drives it is
// the enabled schedule count, which an ordinary authenticated user grows -
// bounded per owner, NOT per fleet, by RELAY_MAX_SCHEDULES_PER_OWNER, over an
// owner population that is itself unbounded under RELAY_ALLOW_SELF_REGISTER.
// THAT CAP BOUNDS THE STARTING WORK SET AND NOT THE PASS, because every page is
// a fresh snapshot: a row inserted mid-sweep joins the work set whenever its
// gen_random_uuid() id sorts above the cursor. So the remedy for a truncated
// pass is to tighten that quantity first; raising the budget widens a boot delay
// the same population can drive.
//
// TRUNCATION PRODUCES FALSE NEGATIVES AND NEVER FALSE POSITIVES. This pass only
// ever ADDS records - see RecordScheduledJobFailure's own header for why it has
// no clearing sibling - so a last_error that is SET is exactly as trustworthy
// after a truncated pass as after a complete one. Only the ABSENCE of one loses
// its meaning: it means "nothing was recorded", which after a truncated pass
// does not mean "this spec validates".
//
// A ZERO OR NEGATIVE BUDGET IS AN EXPIRED DEADLINE, NOT AN ABSENT ONE.
// context.WithTimeout gives that with no special case: the pass checks nothing
// and reports Truncated with Checked 0, loudly. DO NOT ADD A "budget <= 0 means
// unbounded" BRANCH. It would restore an unbounded boot from a zero value, the
// same failure shape as an epoch-fenced query called with a zero-value epoch.
// TestValidateStoredSpecsOnStartup_AnExpiredBudgetChecksNothingRatherThanRunningUnbounded
// is what goes red.
//
// Truncated IS DERIVED FROM THIS FUNCTION'S OWN CHILD CONTEXT, never from the
// returned error, and it is set at EVERY non-nil return including the first page
// query's. A parent cancellation and this budget's deadline both stop the pass
// and only the context tells them apart, so "Truncated: err != nil" would make a
// mid-boot shutdown advertise a deadline that did not fire and prescribe a knob
// that is not the problem.
// TestValidateStoredSpecsOnStartup_AShutdownIsNotATruncation kills that.
//
// THE BUDGET IS A PARAMETER AND THE TIMEOUT IS BUILT HERE rather than by the
// caller, because every other statement in the caller's boot region takes a ctx
// meant to live for the whole process; a bounded one in that scope is a variable
// that looks exactly like ctx and kills whichever long-lived consumer receives
// it. It is env-configurable where sweepPageSize above refuses to be, and the
// two answers differ for a reason rather than by inconsistency: the right budget
// depends on the operator's fleet, database and startup probe, while no operator
// has information the code lacks about how many rows to hold at once.
```

- [ ] **Step 4: Change the signature and the body**

```go
func ValidateStoredSpecsOnStartup(ctx context.Context, q *store.Queries, budget time.Duration) (SweepResult, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	var (
		res       SweepResult
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
			res.Truncated = errors.Is(ctx.Err(), context.DeadlineExceeded)
			return res, err
		}
		for _, row := range rows {
			// A CANCELLED SWEEP RETURNS RATHER THAN RUNNING ON. Every remaining
			// BROKEN row would otherwise reach RecordScheduledJobFailure, get
			// context canceled back, and log its own line - and "most rows broken"
			// is the case this sweep exists for, since that is the release that
			// lands a new retroactive rule. At the top of the ROW loop rather than
			// the page loop, because one page of broken rows is already up to
			// sweepPageSize lines. RETURN rather than break, so the caller names
			// the cause once instead of reporting a clean pass.
			if err := ctx.Err(); err != nil {
				res.Truncated = errors.Is(ctx.Err(), context.DeadlineExceeded)
				return res, err
			}
			res.Checked++

			text, ok := recordableFailure(validateStoredRow(row))
			if !ok {
				continue
			}
			// COUNTED AT THE VERDICT, not at the write. The UPDATE below returns 0
			// for two ordinary reasons, so a count of writes would read as "how
			// many schedules are broken" and be wrong for every schedule that was
			// already broken yesterday.
			res.Invalid++
			// THE THREE COLUMNS PASSED BACK ARE THE FENCE, and they are exactly the
			// three validateStoredRow read. The write lands only if the row is still
			// the generation this verdict is about; see RecordScheduledJobFailure's
			// own header for why the fence is the content rather than updated_at.
			n, err := q.RecordScheduledJobFailure(ctx, store.RecordScheduledJobFailureParams{
				ID:        row.ID,
				LastError: &text,
				JobSpec:   row.JobSpec,
				CronExpr:  row.CronExpr,
				Timezone:  row.Timezone,
			})
			if err != nil {
				log.Printf("schedrunner: startup validation record for %s: %v", row.Name, err)
				continue
			}
			if n == 0 {
				// EITHER the row changed between the LIST and this UPDATE - another
				// replica, or an operator repairing it through one - OR the identical
				// message is already recorded. Neither is news, and the second is the
				// steady state for the entire life of a broken schedule, so logging
				// here would put a line in every boot's output forever.
				//
				// THIS IS THE WHOLE REASON THE QUERY IS :execrows. With :exec a fence
				// that said no and a database fault are the same nil, and the branch
				// below could not exist.
				continue
			}
			log.Printf("schedrunner: startup validation recorded a new failure for schedule %s: %s", row.Name, text)
		}
		// A SHORT PAGE IS THE END, not an empty one. On a table whose enabled
		// count is an exact multiple of sweepPageSize this costs one empty round
		// trip; breaking on an empty page costs the same trip on every table and
		// reads as if a full page could be the last one.
		if len(rows) < sweepPageSize {
			break
		}
		cursor = rows[len(rows)-1].ID
		cursorSet = true
	}
	return res, nil
}
```

Three things about this body that a reviewer must be able to check:

- **`ctx` is SHADOWED by the child.** Every `q.*` call below uses the bounded context by construction; a future edit cannot reach the parent without introducing a new name.
- **`res.Checked++` is BELOW the fence**, so a row the pass never validated is never counted. `TestValidateStoredSpecsOnStartup_AShutdownIsNotATruncation`'s exact `Checked == 1` is what goes red if it moves.
- **The two `res.Truncated` assignments are deliberately not one deferred assignment.** Two sites give two independent mutation kill sites, and a deferred version would also mark a COMPLETE pass truncated when the budget expires after the last short page.

- [ ] **Step 5: Confirm the tree is now red in exactly the expected way**

```powershell
go build ./... 2>&1 | Select-Object -First 40
```

Expected: compile errors at 8 call sites - `cmd/relay-server/main.go:403` and 7 in `internal/schedrunner/*_integration_test.go` (the test ones appear under `go vet -tags integration ./...`, not `go build`). **This is on purpose.** Task 4 is the only thing that repairs it.

```powershell
go vet -tags integration ./internal/schedrunner/... 2>&1 | Select-Object -First 40
```

Record the list of 7 test call sites the compiler names, and check it against the spec's list (`startup_validation_integration_test.go:94`; `..._fence_integration_test.go:130, 194, 214`; `..._paging_integration_test.go:179, 226, 278`). **If the compiler names a site the spec does not, STOP and report** - an eighth caller is a consumer nobody enumerated.

- [ ] **Step 6: Do not commit**

The tree does not compile. Task 4 repairs it and Task 5 commits.

---

## Task 3: The parser, the default, and the four line shapes

**Files:**
- Create: `cmd/relay-server/startupvalidation_config.go`
- Create: `cmd/relay-server/startupvalidation_config_test.go`

- [ ] **Step 1: Write `startupvalidation_config.go`**

```go
package main

import (
	"fmt"
	"time"

	"relay/internal/schedrunner"
)

// defaultStartupValidationDeadline bounds schedrunner.ValidateStoredSpecsOnStartup's
// pass over the enabled set.
//
// IT SITS DELIBERATELY ABOVE RELAY_DB_STATEMENT_TIMEOUT's default, and that is
// the load-bearing constraint rather than a round number. At or below the
// per-statement bound, one slow statement can consume the whole pass - and two
// knobs sharing a number read as coupled and get maintained as if they were.
//
// The other two constraints pull in opposite directions and are the operator's
// to resolve with the knob: short enough that an orchestrator's startup probe
// does not restart the process mid-pass, because a boot slow enough to be killed
// turns an incomplete diagnostic into a crash loop; and long enough that a
// healthy fleet is never truncated, so the truncation line means something when
// it appears.
const defaultStartupValidationDeadline = 45 * time.Second

// parseStartupValidationDeadline resolves RELAY_STARTUP_VALIDATION_DEADLINE into
// the budget handed to schedrunner.ValidateStoredSpecsOnStartup, plus a startup
// message to log, empty when there is nothing to say. Two outcomes:
//
//   - Unset, or a valid positive Go duration: used as-is, silently.
//   - Zero, negative or unparseable: the default is used and the message names
//     the ignored value. A silently-ignored typo would leave an operator
//     believing they had tightened a bound they had not.
//
// NO ZERO-DISABLES ARM, and this diverges from parseWatchdogDuration next door.
// That one accepts 0 because disabling a control that TERMINATES A USER'S WORK is
// a legitimate operational choice with real consequences either way. This control
// terminates nothing; the only thing an off token buys is the unbounded boot the
// bound exists to close, and an operator who genuinely wants a complete pass at
// any cost writes 24h, which stays visible as a number in the environment and in
// the startup line.
//
// AND NO FLOOR ARM, which diverges from parseTrailingLogWindow as well. THE
// DISTINGUISHING QUESTION IS WHETHER THE FAIL-AGGRESSIVE DIRECTION IS SILENT,
// not whether the value is small: a rejected log chunk produces no error and no
// line, and an aggressive watchdog destroys work, so both of those warn below a
// floor. A one-second budget here announces itself on every boot in a line naming
// the budget and the counts, so a floor would be a constant nobody can justify
// guarding a failure mode that is already loud.
//
// Deliberately not a log.Fatalf, following parseScheduleCap: a bad duration must
// not stop a server booting when a safe default exists.
func parseStartupValidationDeadline(name, raw string) (time.Duration, string) {
	if raw == "" {
		return defaultStartupValidationDeadline, ""
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultStartupValidationDeadline, fmt.Sprintf(
			"%s=%q is not a positive Go duration; using %s", name, raw, defaultStartupValidationDeadline)
	}
	return d, ""
}

// startupValidationPrefix is on every line this pass emits, including the
// warning shape, so one grep retrieves the whole block rather than its headline.
const startupValidationPrefix = "schedrunner: startup validation"

// startupValidationDeadlineLine renders the unconditional bounds line, in the
// shape of watchdogBoundsLine and scheduleCapLine.
//
// ITS VALUE IS ITS ADJACENCY to the pass it bounds. The boot log between the
// dispatcher and "HTTP listening on" is otherwise silent - the reconcile prints
// nothing - so an operator watching a slow boot sees nothing at all during the
// window this knob governs.
func startupValidationDeadlineLine(budget time.Duration) string {
	return fmt.Sprintf(
		"%s bounded at %s (RELAY_STARTUP_VALIDATION_DEADLINE). A pass that runs out of budget checks "+
			"only part of the enabled set; the line after it says which.", startupValidationPrefix, budget)
}

// startupValidationLines renders the sweep's outcome. ONE FORMATTER OWNS ALL FOUR
// SHAPES, which is what makes it impossible for a caller to print a count without
// the sentence saying whether it is a total.
//
// THE ORDER OF THE CASES IS PART OF THE CONTRACT. A truncated pass ALSO returns a
// non-nil error, so testing err first would print the shutdown shape for a fired
// deadline and the remedy would never be named.
//
// THE COUNT AND THE WORDS "FLOORS, NOT TOTALS" SHARE THE FIRST LINE, and the
// split into three is allowed only because they do: a reader who sees only the
// first line still gets the caveat that makes the number honest, and lines two
// and three add detail rather than the caveat.
//
// THE REMEDY LADDER IS ORDERED TIGHTENING-FIRST, not cheapest-first. The quantity
// that drives truncation is the enabled schedule count, which any authenticated
// user grows, so "raise the deadline" is a remedy that WIDENS a boot delay a
// careless or hostile population can drive; it goes second with its cost stated
// inline. There is no disabling option anywhere in the ladder because there is no
// off token to offer.
func startupValidationLines(res schedrunner.SweepResult, err error, budget, elapsed time.Duration) []string {
	switch {
	case res.Truncated:
		return []string{
			fmt.Sprintf("%s STOPPED AT ITS %s DEADLINE after checking %d enabled schedules in %s, "+
				"%d of which no longer validate. THESE ARE FLOORS, NOT TOTALS.",
				startupValidationPrefix, budget, res.Checked, elapsed.Round(time.Millisecond), res.Invalid),
			fmt.Sprintf("%s: the rest of the enabled set was not checked on this boot and its size is "+
				"unknown, so a schedule carrying no recorded failure may simply never have been looked "+
				"at. Recorded failures are still trustworthy - this pass only ever adds them, never "+
				"clears them.", startupValidationPrefix),
			fmt.Sprintf("%s: to get a complete pass, first reduce the enabled set "+
				"(RELAY_MAX_SCHEDULES_PER_OWNER bounds it per owner, not per fleet); raising "+
				"RELAY_STARTUP_VALIDATION_DEADLINE also works and costs exactly that much more boot time "+
				"before the HTTP API answers. The next boot starts again from the beginning of the set, "+
				"not from here.", startupValidationPrefix),
		}
	case err != nil:
		// SHUTDOWN AND A PAGE-QUERY FAULT SHARE ONE SHAPE rather than getting a
		// third and fourth vocabulary. A mid-boot SIGTERM means nobody will read
		// that boot's last_error; a page-query fault means the process IS
		// continuing with partial coverage. The shared shape costs nothing on the
		// first and is required by the second. It does not mention the deadline,
		// because the deadline is not why it stopped.
		return []string{fmt.Sprintf(
			"warn: %s DID NOT COMPLETE after checking %d enabled schedules, %d of which no longer "+
				"validate (floors, not totals - the rest of the enabled set was not checked): %v",
			startupValidationPrefix, res.Checked, res.Invalid, err)}
	case res.Checked == 0:
		// So an empty table does not read as a broken instrument.
		return []string{fmt.Sprintf("%s completed: no enabled schedules to check.", startupValidationPrefix)}
	default:
		// "completed", not "all": the pass reads a moving table one page at a time
		// and ListEnabledScheduledJobsPage's comment enumerates which concurrent
		// writes it can miss.
		return []string{fmt.Sprintf("%s completed: %d enabled schedules checked in %s, %d of which no "+
			"longer validate.", startupValidationPrefix, res.Checked, elapsed.Round(time.Millisecond),
			res.Invalid)}
	}
}
```

- [ ] **Step 2: Write the untagged guards**

Create `cmd/relay-server/startupvalidation_config_test.go`. No build tag: these are pure functions, so they run under `go test ./...` and in CI's `test` job.

```go
package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"relay/internal/schedrunner"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultStartupValidationDeadlineIsAboveTheStatementTimeoutDefault pins the
// constant's VALUE in its own test, separately from the parser table below.
//
// THE TWO ASSERTIONS ARE SPLIT ON PURPOSE. A parser test that compares against
// defaultStartupValidationDeadline derives its expectation from part of its own
// subject, so the constant could move to 1ms with that table still green. This
// test is the half that pins the number; the table is the half that pins the
// behaviour.
func TestDefaultStartupValidationDeadlineIsAboveTheStatementTimeoutDefault(t *testing.T) {
	require.Equal(t, 45*time.Second, defaultStartupValidationDeadline)
	require.Greater(t, defaultStartupValidationDeadline, 30*time.Second,
		"the budget must sit above RELAY_DB_STATEMENT_TIMEOUT's 30s default: at or below it one slow "+
			"statement can consume the whole pass, and two knobs sharing a number read as coupled")
}

// TestParseStartupValidationDeadline pins the two-outcome contract as BEHAVIOUR.
// Whatever README says about what this parser refuses must be phrased as what
// this table pins, never written from memory.
//
// THE ZERO AND NEGATIVE ROWS ARE THE ONES THAT DISCRIMINATE. 0 is the value an
// operator reaches for as an off switch, and a parser that accepted it would
// restore the unbounded boot this whole slice exists to close while every other
// row here stayed green. The unset row is the second discriminator: a parser that
// warned on the ordinary path would put a line in every boot forever.
func TestParseStartupValidationDeadline(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    time.Duration
		msgPart string
	}{
		{"unset uses the default and says nothing", "", defaultStartupValidationDeadline, ""},
		{"a valid duration is used as-is, silently", "90s", 90 * time.Second, ""},
		{"the documented escape hatch is honoured", "24h", 24 * time.Hour, ""},
		{"zero is NOT an off switch: it folds to the default and warns", "0s", defaultStartupValidationDeadline, "is not a positive Go duration"},
		{"negative folds to the default and warns", "-5m", defaultStartupValidationDeadline, "is not a positive Go duration"},
		{"unparseable folds to the default and warns", "forty-five seconds", defaultStartupValidationDeadline, "is not a positive Go duration"},
		{"a bare integer is not a Go duration", "45", defaultStartupValidationDeadline, "is not a positive Go duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseStartupValidationDeadline("RELAY_STARTUP_VALIDATION_DEADLINE", tc.raw)
			require.Equal(t, tc.want, got)
			if tc.msgPart == "" {
				require.Empty(t, msg,
					"warning on the ordinary unset or sensible case is as wrong as staying silent on a typo")
				return
			}
			assert.Contains(t, msg, tc.msgPart)
			assert.Contains(t, msg, tc.raw,
				"a warning that does not name the ignored value leaves an operator believing they "+
					"tightened a bound they did not")
			assert.NotContains(t, msg, "disable",
				"there is no off token to offer, so no message may imply one")
		})
	}
}

// TestStartupValidationDeadlineLineNamesTheBoundAndItsConsequence.
func TestStartupValidationDeadlineLineNamesTheBoundAndItsConsequence(t *testing.T) {
	line := startupValidationDeadlineLine(45 * time.Second)
	assert.True(t, strings.HasPrefix(line, startupValidationPrefix),
		"every line of this pass carries one prefix, so one grep retrieves the whole block")
	assert.Contains(t, line, "45s")
	assert.Contains(t, line, "RELAY_STARTUP_VALIDATION_DEADLINE",
		"the bound is useless to an operator who cannot find the knob")
	assert.Contains(t, line, "only part of the enabled set",
		"an operator must learn from the bounds line that the bound costs coverage, before the pass runs")
}

// truncatedFixture is a result no other path in these tests can produce, so an
// assertion below cannot pass because the value it matched came from somewhere
// else. 901 and 3 are neither 0 nor 1 nor any digit of a duration used here.
func truncatedFixture() schedrunner.SweepResult {
	return schedrunner.SweepResult{Checked: 901, Invalid: 3, Truncated: true}
}

// TestStartupValidationLines_ATruncatedPassNeverReadsAsATotal is the guard for
// the item's headline defect: a partial count presented as a total.
//
// THE MUTATION IT EXISTS TO KILL is a formatter that falls through to the
// completed string for a truncated result - a single change that re-creates the
// defect. It asserts on words that DIFFER between the shapes rather than on the
// whole line, so rewording either one does not produce a false alarm.
//
// BOTH DURATIONS ARE ASSERTED POSITIONALLY AND THEY ARE DELIBERATELY UNLIKE EACH
// OTHER. budget and elapsed are adjacent time.Duration parameters, so swapping
// them at the call site COMPILES; 45s against 47.123s makes the swap visible,
// where two similar values would not. The two int counts are asserted the same
// way and for the same reason.
func TestStartupValidationLines_ATruncatedPassNeverReadsAsATotal(t *testing.T) {
	lines := startupValidationLines(truncatedFixture(), context.DeadlineExceeded,
		45*time.Second, 47123*time.Millisecond)

	require.Len(t, lines, 3,
		"the truncated outcome is three consecutive lines. One 500-character line is unreadable in a "+
			"boot log, and dropping a line drops a disclosure")
	for i, line := range lines {
		assert.True(t, strings.HasPrefix(line, startupValidationPrefix),
			"line %d does not carry the shared prefix, so an operator's grep finds the headline and "+
				"misses the caveat: %q", i, line)
	}
	joined := strings.Join(lines, "\n")

	// THE FIRST LINE MUST BE HONEST ON ITS OWN. This is what makes the split safe:
	// the count and the word that qualifies it are never on different lines.
	assert.Contains(t, lines[0], "checking 901 enabled schedules",
		"the checked count must appear, and in its own clause: without it an operator cannot tell "+
			"whether the budget is nearly enough or nowhere near")
	assert.Contains(t, lines[0], "3 of which no longer validate",
		"the invalid count must appear in its own clause; swapping the two counts must be visible")
	assert.Contains(t, lines[0], "FLOORS, NOT TOTALS",
		"THE SPLIT IS ONLY SAFE BECAUSE THIS IS ON THE SAME LINE AS THE COUNT. Move it to line 2 and "+
			"a reader who sees one line reads a floor as a total")
	assert.Contains(t, lines[0], "45s",
		"the bound must be named so the operator can see how close the budget was")
	assert.Contains(t, lines[0], "47.123s",
		"the ELAPSED time must be named, in its own clause. A swap with the budget compiles")
	assert.Contains(t, lines[0], "STOPPED AT ITS",
		"the word the completed shape does not contain")

	assert.NotContains(t, joined, "completed:",
		"THE MUTATION THIS KILLS: a formatter that falls through to the completed string for a "+
			"truncated result re-creates the defect the whole slice exists to close")
	assert.NotContains(t, joined, "DID NOT COMPLETE",
		"a fired deadline must not print the shutdown shape: testing err before Truncated does exactly "+
			"that, and then the remedy is never named")
	assert.Contains(t, joined, "size is unknown",
		"the remainder's SIZE must be stated as unknown - not zero, not estimated")
	assert.Contains(t, joined, "only ever adds them, never clears them",
		"the absence-versus-presence asymmetry is the only thing that keeps last_error readable after "+
			"a truncated boot")
	assert.Contains(t, joined, "RELAY_STARTUP_VALIDATION_DEADLINE")
	assert.Contains(t, joined, "RELAY_MAX_SCHEDULES_PER_OWNER")
	assert.Less(t, strings.Index(joined, "RELAY_MAX_SCHEDULES_PER_OWNER"),
		strings.Index(joined, "RELAY_STARTUP_VALIDATION_DEADLINE also works"),
		"the ladder is ordered tightening-first: the loosening remedy widens a boot delay the "+
			"population that caused the truncation can drive, so it must not come first")
	assert.Contains(t, joined, "from the beginning of the set",
		"where the remainder went (nowhere) and what the next boot does")
	for _, forbidden := range []string{"disable", "turn it off", "set it to 0"} {
		assert.NotContains(t, strings.ToLower(joined), forbidden,
			"there is no off token for this bound, so no line may offer one as a remedy")
	}
}

// TestStartupValidationLines_AShutdownDoesNotAdvertiseTheDeadline is the
// formatter half of the deadline-versus-shutdown distinction;
// TestValidateStoredSpecsOnStartup_AShutdownIsNotATruncation is the library half.
//
// THE DISCRIMINATING INPUT IS Truncated: false WITH A NON-NIL ERROR. That is
// exactly what a mid-boot SIGTERM produces, and the wrong implementation -
// Truncated derived from err != nil - cannot produce it at all.
func TestStartupValidationLines_AShutdownDoesNotAdvertiseTheDeadline(t *testing.T) {
	lines := startupValidationLines(
		schedrunner.SweepResult{Checked: 901, Invalid: 3, Truncated: false},
		context.Canceled, 45*time.Second, 2500*time.Millisecond)

	require.Len(t, lines, 1)
	line := lines[0]
	assert.Contains(t, line, "DID NOT COMPLETE")
	assert.Contains(t, line, "901")
	assert.Contains(t, line, "3 of which no longer validate")
	assert.Contains(t, line, "floors, not totals")
	assert.Contains(t, line, "context canceled",
		"the cause must be printed: this shape also covers a page-query fault, where the process "+
			"continues with partial coverage and the operator must know why")
	assert.NotContains(t, line, "DEADLINE",
		"THE MUTANT THIS KILLS advertises a deadline that did not fire and prescribes a knob that is "+
			"not the problem")
	assert.NotContains(t, line, "RELAY_STARTUP_VALIDATION_DEADLINE",
		"same: a shutdown must not send the operator to this knob")
	assert.NotContains(t, line, "completed:")
	assert.NotContains(t, line, "45s",
		"the budget is not why it stopped, so naming it would be a false attribution")
}

// TestStartupValidationLines_TheCompleteShapes covers the two non-error outcomes.
//
// THE ZERO CASE HAS ITS OWN SHAPE so an empty table does not read as a broken
// instrument: "0 enabled schedules checked" is what a sweep that never ran also
// prints.
func TestStartupValidationLines_TheCompleteShapes(t *testing.T) {
	full := startupValidationLines(
		schedrunner.SweepResult{Checked: 1432, Invalid: 3}, nil, 45*time.Second, 4100*time.Millisecond)
	require.Len(t, full, 1)
	assert.Contains(t, full[0], "completed: 1432 enabled schedules checked in 4.1s")
	assert.Contains(t, full[0], "3 of which no longer validate")
	assert.NotContains(t, full[0], "FLOORS",
		"a complete pass must not hedge: a caveat on every boot is a caveat nobody reads")
	assert.NotContains(t, full[0], "all ",
		"'completed', not 'all': the pass reads a moving table one page at a time")

	empty := startupValidationLines(schedrunner.SweepResult{}, nil, 45*time.Second, 12*time.Millisecond)
	require.Len(t, empty, 1)
	assert.Contains(t, empty[0], "no enabled schedules to check")
	assert.NotContains(t, empty[0], "0 enabled schedules checked",
		"the zero case must not read as a broken instrument")
}
```

- [ ] **Step 3: Compile-check this package alone**

```powershell
go vet ./cmd/relay-server/ 2>&1 | Select-Object -First 20
```

Expected: still the ONE error from `main.go:403`'s two-argument call. The new files themselves must produce no diagnostic. **If they do, fix them before Task 4** - otherwise Task 4's failures are ambiguous.

- [ ] **Step 4: Do not commit**

---

## Task 4: Repair the build - `main`, the seven call sites, and the reconcile's stale clause

**Files:**
- Modify: `cmd/relay-server/main.go:398-405` (one comment sentence at 399-401, then the call block at 403-405)
- Modify: `internal/schedrunner/startup_validation_integration_test.go:94`
- Modify: `internal/schedrunner/startup_validation_fence_integration_test.go:129-130, 194, 214`
- Modify: `internal/schedrunner/startup_validation_paging_integration_test.go:179, 226, 278`
- Modify: `internal/schedrunner/runner.go:279-282` (one clause)

- [ ] **Step 1: Correct main's placement comment, one sentence only**

Replace exactly:

```
	// A FAILURE HERE MUST NOT STOP THE BOOT. Per-row record failures are logged
	// inside and the sweep continues; a page query's error and a mid-sweep
	// cancellation are returned and logged here as a warning, and the server
	// carries on. Turning a schedule problem into a server that will not start
	// would be worse than the invisibility this closes.
```

with:

```
	// A FAILURE HERE MUST NOT STOP THE BOOT. Per-row record failures are logged
	// inside and the sweep continues; a page query's error, a shutdown and the
	// pass's own expired budget are returned, and startupValidationLines gives
	// each of the three its own shape - only one of them is a warning. The server
	// carries on in every case. Turning a schedule problem into a server that will
	// not start would be worse than the invisibility this closes.
```

Everything above it in that comment - the two placement paragraphs and the fence paragraph - stays byte-identical. Placement is unchanged by this slice.

- [ ] **Step 2: Replace the call block**

Replace exactly:

```go
	if err := schedrunner.ValidateStoredSpecsOnStartup(ctx, q); err != nil {
		log.Printf("warn: schedrunner startup validation: %v", err)
	}
```

with:

```go
	startupValidationDeadline, startupValidationWarning := parseStartupValidationDeadline(
		"RELAY_STARTUP_VALIDATION_DEADLINE", os.Getenv("RELAY_STARTUP_VALIDATION_DEADLINE"))
	if startupValidationWarning != "" {
		log.Printf("WARNING: %s", startupValidationWarning)
	}
	log.Print(startupValidationDeadlineLine(startupValidationDeadline))

	startupValidationStart := time.Now()
	sweep, sweepErr := schedrunner.ValidateStoredSpecsOnStartup(ctx, q, startupValidationDeadline)
	for _, line := range startupValidationLines(sweep, sweepErr, startupValidationDeadline,
		time.Since(startupValidationStart)) {
		log.Print(line)
	}
```

Three constraints on this block:

- **Both the parse and its line sit HERE, in the boot region**, not up with the other `parse*` calls. This parser cannot fatal, so there is no fail-early benefit to hoisting it, and the bounds line's whole value is its adjacency to the pass it bounds.
- **There is no `if sweepErr != nil` branch.** One formatter owns every outcome; an `if` here is how a count gets printed without its caveat.
- **`sweepErr`, not `err`.** `main` has an `err` in scope and reusing it here buys nothing and invites a shadowing mistake.

- [ ] **Step 3: Run the two untagged wiring guards**

```powershell
go build ./... 2>&1 | Select-Object -First 20
go test ./cmd/relay-server/... -count=1 -run "TestStartupValidation|TestParseStartupValidation|TestDefaultStartupValidation|TestSchedrunnerStartupSweepIsWiredInOrderByMain" -v
```

Expected: `go build` clean for non-test code; **G7 now GREEN** (it was the RED recorded as M9), G1/G1b/G2/G2b/G2c green, and `TestSchedrunnerStartupSweepIsWiredInOrderByMain` green with its file untouched.

**If `TestSchedrunnerStartupSweepIsWiredInOrderByMain` goes RED, STOP and report the property number it names.** Do not edit that file. A red there means the call's statement position or its nesting actually moved, which this slice must not do.

- [ ] **Step 4: Move the seven existing call sites**

All seven are mechanical. Each gets a generous `60*time.Second` budget, matching the 60s `context.WithTimeout` deadlines already in these files. A budget is also a bounded failure for the three sites that use `context.Background()` today, which previously had none.

**`startup_validation_integration_test.go:94`** - replace

```go
	require.NoError(t, schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q))
```

with

```go
	_, err = schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q, 60*time.Second)
	require.NoError(t, err)
```

(`err` is already in scope from line 63.)

**`startup_validation_fence_integration_test.go:129-130`** - replace

```go
	done := make(chan error, 1)
	go func() { done <- schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q) }()
```

with

```go
	done := make(chan error, 1)
	go func() {
		_, sweepErr := schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q, 60*time.Second)
		done <- sweepErr
	}()
```

**`startup_validation_fence_integration_test.go:194` and `:214`** - each is

```go
	require.NoError(t, schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q))
```

and each becomes

```go
	_, err = schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q, 60*time.Second)
	require.NoError(t, err)
```

(`err` is in scope from line 185 in both cases.)

**`startup_validation_paging_integration_test.go:179`** - replace

```go
	require.NoError(t, schedrunner.ValidateStoredSpecsOnStartup(ctx, q))
```

with

```go
	res, err := schedrunner.ValidateStoredSpecsOnStartup(ctx, q, 60*time.Second)
	require.NoError(t, err)
```

and add, immediately after the existing `require.Equal(t, 250, countRecordedFailures(t, h), ...)`:

```go
	require.Equal(t, 250, res.Checked,
		"THE EXACT SIBLING AT THE FULL-COUNT END. A range assertion elsewhere is only bracketed if "+
			"one test pins Checked against a known complete pass")
	require.False(t, res.Truncated,
		"a pass that fits inside its budget must not hedge: a caveat on every boot is one nobody reads")
```

**`startup_validation_paging_integration_test.go:226`** - replace

```go
	err := schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q)
```

with

```go
	_, err := schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q, 60*time.Second)
```

**Do not add assertions here.** This test's subject is log volume under cancellation, and a second subject makes a red ambiguous.

**`startup_validation_paging_integration_test.go:278`** - replace

```go
	require.NoError(t, schedrunner.ValidateStoredSpecsOnStartup(ctx, q))
```

with

```go
	_, err := schedrunner.ValidateStoredSpecsOnStartup(ctx, q, 60*time.Second)
	require.NoError(t, err)
```

**Do not extend the fence tests.** Their subject - the content fence and the identical-message no-op - is unrelated to the budget.

- [ ] **Step 5: Run the integration lane and check for vacuity (M7)**

```powershell
go vet -tags integration ./... 2>&1 | Select-Object -First 20
go test -tags integration -count=1 ./internal/schedrunner/... -v -timeout 1800s
```

Expected: PASS, including all five existing sweep tests and both reconcile tests.

**Record M7 per site**: the exact new wording, and whether the assertion still bites. The vacuity question to ask at each site: *does the test still fail if the sweep does nothing?* A production change that is a no-op for a fixture still breaks fixtures, and a test that goes vacuous under an edit is invisible to diff review. The positive assertions that must still be present: `require.NotNil(t, brokenRow.LastError)` (site 1), `assert.Nil(t, after.LastError)` (site 2), the two `countRecordedFailures` assertions (sites 5 and 7), `assert.Contains(t, firstBoot.String(), "reboot-me")` (site 3).

**Re-run the lane rather than reading the diff.** That is the instruction the spec gives and it is the right one.

- [ ] **Step 6: Correct `ReconcileOnStartup`'s stale clause**

In `internal/schedrunner/runner.go`, replace exactly:

```
// PEAK MEMORY IS ONE PAGE OF FOUR NARROW COLUMNS. THE DURATION IS BOUNDED BY
// NOTHING - one round trip per page plus one UPDATE per overdue row, all of it
// ahead of srv.ListenAndServe(). Paging bounds the allocation and not the
// duration, the same trade ValidateStoredSpecsOnStartup makes.
```

with:

```
// PEAK MEMORY IS ONE PAGE OF FOUR NARROW COLUMNS. THE DURATION IS BOUNDED BY
// NOTHING - one round trip per page plus one UPDATE per overdue row, all of it
// ahead of srv.ListenAndServe(). Paging bounds the allocation and not the
// duration. Bounding THIS pass is a different decision with a user-facing cost,
// not a second application of the sweep's: a truncated reconcile leaves rows
// overdue, and the ticker then fires each of them ONCE, which is exactly the
// catch-up README promises never happens.
```

The trailing clause becomes a false comparison the moment the sweep stops making that trade, and deleting it alone would leave a reader wondering why the reconcile is different. The replacement says why, and it is the tripwire for whoever bounds the reconcile later.

- [ ] **Step 7: M10 - read the real boot log**

The truncated line's shape was decided against "how it will look in a real log", so look at it.

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-deadline
# Needs the dev Postgres that scripts/dev.ps1 manages, at
# postgres://relay:relay@127.0.0.1:5432
go build -o bin/relay-server.exe ./cmd/relay-server
$env:RELAY_DATABASE_URL = "postgres://relay:relay@127.0.0.1:5432/relay?sslmode=disable"
$env:RELAY_STARTUP_VALIDATION_DEADLINE = "1ns"
./bin/relay-server.exe
# read the boot region, then Ctrl+C
```

`1ns` forces the truncated path on any database, including an empty one, so the three-line shape is observable without seeding anything. Paste the boot region verbatim into M10 - from the dispatcher's line through `HTTP listening on`.

Check three things and report if any is false: each of the three lines carries its own timestamp; each begins with `schedrunner: startup validation`; and line 1 alone is honest (count plus `FLOORS, NOT TOTALS`). Then unset the env var and boot once more with the default to capture the ordinary two-line output.

**If the dev Postgres is not available, say so plainly in the handoff** rather than substituting the test output for a real log - the test sees the strings, not the log framing, and the framing is the thing this step is about.

- [ ] **Step 8: Run Task 0's throughput measurement (M2) now that the signature exists**

Follow Task 0, Step 3. Record M2 and delete the scratch file. **Confirm `git status --porcelain` does not list it** before going on.

- [ ] **Step 9: Do not commit yet** - Task 5's battery runs first so its kills can go in the commit message.

---

## Task 5: Mutation battery 1, then Commit A

**Files:** none modified in the worktree. All mutations run through `go test -overlay`.

- [ ] **Step 1: Set up the overlay harness**

```powershell
$SP = "C:\Users\chadv\AppData\Local\Temp\claude\D--dev-relay--claude-worktrees-roadmap-now-dependencies-581b21\50da71ee-f761-480b-a23d-f1f7d875cc3b\scratchpad"
$W  = "D:/dev/relay/.claude/worktrees/lane-boot-deadline"
New-Item -ItemType Directory -Force "$SP\mutants" | Out-Null
Copy-Item "$W/cmd/relay-server/startupvalidation_config.go" "$SP\mutants\config_orig.go"
Copy-Item "$W/cmd/relay-server/main.go"                     "$SP\mutants\main_orig.go"
```

For each mutation: copy the `_orig` file to `$SP\mutants\m.go`, edit THAT copy, write an overlay file, and run the tests with `-overlay`.

```powershell
# overlay.json - one entry per mutated file, absolute paths, forward slashes
'{"Replace":{"D:/dev/relay/.claude/worktrees/lane-boot-deadline/cmd/relay-server/startupvalidation_config.go":"C:/.../scratchpad/mutants/m.go"}}' | Set-Content "$SP\overlay.json"
go test -overlay "$SP\overlay.json" ./cmd/relay-server/... -count=1 -run "TestStartupValidation|TestParseStartupValidation|TestDefaultStartupValidation"
```

**Why an overlay and not an edit.** The worktree is never modified, so there is nothing to restore, nothing for a sibling agent to read mid-mutation, and no temptation to `git checkout --` over an uncommitted guard. **Before each run, read the mutant file back** and confirm the line you meant to change actually changed - a silently unapplied mutation reports "survived".

- [ ] **Step 2: The control that must die first**

Make `parseStartupValidationDeadline` always `return time.Nanosecond, ""`.

Expected: `TestParseStartupValidationDeadline` RED on several rows. **If everything is green, the harness is broken** - the overlay path is wrong, or the test selector matched nothing. Fix the harness before trusting any result below. **A compile error is not a kill**: record it as "did not run" and rewrite the mutant.

- [ ] **Step 3: Run the battery and record each kill WITH the guard that died**

| # | Mutation | Expected kill | Record: RED test + the assertion that failed |
| --- | --- | --- | --- |
| B1.1 | `d <= 0` becomes `d < 0` (0 becomes an off switch) | `TestParseStartupValidationDeadline/zero...` | `______` |
| B1.2 | `case res.Truncated:` branch returns the `default:` completed string | `..._ATruncatedPassNeverReadsAsATotal` (`NotContains "completed:"`) | `______` |
| B1.3 | drop `THESE ARE FLOORS, NOT TOTALS.` from line 1, add it to line 2 | `..._ATruncatedPassNeverReadsAsATotal` (`lines[0]` contains FLOORS) | `______` |
| B1.4 | reorder the switch so `case err != nil:` precedes `case res.Truncated:` | `..._ATruncatedPassNeverReadsAsATotal` (`NotContains "DID NOT COMPLETE"`) | `______` |
| B1.5 | the `err != nil` shape returns the truncated three lines | `..._AShutdownDoesNotAdvertiseTheDeadline` (`NotContains "DEADLINE"`) | `______` |
| B1.6 | swap `res.Checked` and `res.Invalid` in the truncated Sprintf | `..._ATruncatedPassNeverReadsAsATotal` (positional count clauses) | `______` |
| B1.7 | swap `budget` and `elapsed` in the truncated Sprintf | `..._ATruncatedPassNeverReadsAsATotal` (`45s` / `47.123s` clauses) | `______` |
| B1.8 | drop `res.Checked == 0`'s case (empty table falls to `default:`) | `..._TheCompleteShapes` (`NotContains "0 enabled schedules checked"`) | `______` |
| B1.9 | in `main.go`, pass `staleAfter` as the third argument | `TestStartupValidationDeadlineIsWiredAndLoggedByMain` (chain never mentions the env var) | `______` |
| B1.10 | in `main.go`, delete the `for ... range` print loop | same guard (`startupValidationLines` called 0 times) | `______` |
| B1.11 | in `main.go`, delete the bounds `log.Print` | same guard (`startupValidationDeadlineLine` >= 1) | `______` |
| B1.12 | swap the ladder's order (raise-the-deadline first) | `..._ATruncatedPassNeverReadsAsATotal` (the `strings.Index` ordering assertion) | `______` |

**A kill must name its guard.** A mutation can redden a test written for a different property, so record WHICH assertion failed, not just which test. If a mutation reddens something unexpected, trace the failing branch before recording it - and suspect a degenerate fixture value.

Record any SURVIVOR honestly. Two are expected to survive and are not defects to chase:

- moving `res.Checked++` above the `ctx.Err()` fence (no untagged guard can see it; battery 2's G4 kills it);
- deleting `defer cancel()` (`go vet`'s `lostcancel` is the guard; confirm with `go vet ./internal/schedrunner/`).

- [ ] **Step 4: Commit A**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-deadline
git status --porcelain
git diff --stat -- internal/schedrunner/ cmd/relay-server/
git ls-files --eol internal/schedrunner/startup_validation.go internal/schedrunner/runner.go cmd/relay-server/main.go cmd/relay-server/startupvalidation_config.go cmd/relay-server/startupvalidation_config_test.go cmd/relay-server/startupvalidation_wiring_test.go internal/schedrunner/startup_validation_integration_test.go internal/schedrunner/startup_validation_fence_integration_test.go internal/schedrunner/startup_validation_paging_integration_test.go
```

Every path must read `i/lf`. Confirm `cmd/relay-server/schedrunner_startup_wiring_test.go` is NOT in the diff.

```bash
git add internal/schedrunner/startup_validation.go internal/schedrunner/runner.go \
  internal/schedrunner/startup_validation_integration_test.go \
  internal/schedrunner/startup_validation_fence_integration_test.go \
  internal/schedrunner/startup_validation_paging_integration_test.go \
  cmd/relay-server/main.go cmd/relay-server/startupvalidation_config.go \
  cmd/relay-server/startupvalidation_config_test.go \
  cmd/relay-server/startupvalidation_wiring_test.go
git commit -F - <<'EOF'
feat(schedrunner): bound the startup validation sweep at RELAY_STARTUP_VALIDATION_DEADLINE

ValidateStoredSpecsOnStartup ran to exhaustion over every enabled schedule
before srv.ListenAndServe(), and its own header said its duration was bounded
by nothing. It now takes a budget, builds the timeout itself, and returns a
SweepResult carrying Checked, Invalid and Truncated together so no caller can
print a partial count without the field that says it is partial.

Truncated is derived from the child context's Err(), at every non-nil return
including the first page query's - never from the returned error. A SIGTERM
gives context.Canceled and the timer gives context.DeadlineExceeded, and the
two print different shapes: a shutdown must not advertise a deadline that did
not fire and prescribe a knob that is not the problem.

A zero or negative budget is an EXPIRED deadline, not an absent one. There is
no `budget <= 0 means unbounded` branch and no off token at either layer.

The truncated outcome is three consecutive lines rather than one ~500-character
line. No clause was cut. The count and the words FLOORS, NOT TOTALS share the
first line, which is what makes the split safe, and every line carries the
`schedrunner: startup validation` prefix so one grep retrieves the block.

Measured (container mode, <fill from M2>):
  all-healthy-small  <N> rows, job_spec <B> bytes: <X> rows/s, 45s covers <Y>
  all-healthy-fat    <N> rows, job_spec <B> bytes: <X> rows/s, 45s covers <Y>
  all-broken-small   <N> rows, job_spec <B> bytes: <X> rows/s, 45s covers <Y>
pgx's expired-context error satisfies errors.Is(err, context.DeadlineExceeded): <M3>
WithTimeout(ctx, 0) fails from the first page query in <M4>.

Mutations killed (mutation -> guard -> assertion):
  <one line per B1.x from the table, with the assertion that failed>
Survivors, recorded rather than chased:
  res.Checked++ above the ctx.Err() fence - killed by battery 2's G4
  deleted `defer cancel()` - caught by go vet lostcancel

No migration, no .sql change, no make generate.
cmd/relay-server/schedrunner_startup_wiring_test.go is byte-identical and green.
EOF
```

---

## Task 6: G3-G6 - the integration guards

**Files:**
- Create: `internal/schedrunner/startup_validation_deadline_integration_test.go`

**Why these need the tag, and why no Makefile edit follows.** The property is about a pass that issues real statements against real rows, and the sweep's first action is a page query, so there is no seam that reaches the deadline logic without a database. CLAUDE.md's step 1 does not apply; step 2 does, and both required links already exist (see "Spec claims checked and SURVIVING" above), so these run in CI's `pg-integration` job with no Makefile or workflow change.

- [ ] **Step 1: Write the file**

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

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// seedHealthySchedules plants n enabled schedules whose stored spec still
// validates, in ONE statement, beside seedBrokenSchedules.
//
// THE MIXED FIXTURE IS WHAT MAKES THE TWO COUNTS SEPARABLE. With broken rows
// only, Checked and Invalid are equal and a mutant returning one for both
// survives - a degenerate fixture value is a guard that cannot fail.
//
// next_run_at is far in the future for the reason seedBrokenSchedules gives:
// neither ListEligibleScheduledJobs nor ListOverdueScheduledJobsForCatchupPage
// can reach these rows, so a pass is attributable to the sweep.
func seedHealthySchedules(t *testing.T, h *runnerHarness, owner pgtype.UUID, n int) {
	t.Helper()
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO scheduled_jobs (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at)
		SELECT 'healthy-' || g::text, $1, '@hourly', 'UTC', $2::jsonb, 'skip', TRUE, NOW() + INTERVAL '720 hours'
		FROM generate_series(1, $3::int) AS g`,
		owner, string(makeSpecJSON(t)), n)
	require.NoError(t, err)
}

// TestValidateStoredSpecsOnStartup_AnExpiredBudgetChecksNothingRatherThanRunningUnbounded
// is the guard for "a zero budget is an EXPIRED deadline, not an absent one".
//
// THE DISCRIMINATING INPUT IS THE BUDGET ITSELF, and the recorded-failure count
// is what discriminates: against a `budget <= 0 means unbounded` branch - the
// kind-looking branch a later reader will reach for - three rows are recorded
// instead of none. Fully deterministic: no timing and no sleep.
//
// IT ALSO PINS THAT THE FAILURE IS PROMPT. The whole guard depends on
// WithTimeout(ctx, 0) producing an error from the first page query rather than a
// hang, and a hang under mutation is indistinguishable from infrastructure
// trouble - so the parent deadline bounds it and the elapsed assertion names the
// cause.
func TestValidateStoredSpecsOnStartup_AnExpiredBudgetChecksNothingRatherThanRunningUnbounded(t *testing.T) {
	h := newRunnerHarness(t)
	owner := h.createUser(t, "expired-budget@example.com")
	seedBrokenSchedules(t, h, owner, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	res, err := schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q, 0)
	elapsed := time.Since(start)

	require.Error(t, err,
		"an expired budget must be reported, not swallowed: a silent empty pass reads exactly like a "+
			"healthy fleet")
	// M3 decides whether the line below stays. Keep it only if pgx's error for a
	// query whose context expired satisfies errors.Is; otherwise delete it and
	// record the observed error text in the commit message. Do NOT assert a
	// substring of pgx's message - that is parsing another component's prose.
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.True(t, res.Truncated,
		"Truncated comes from the child context, so it must be set even when the budget expired before "+
			"the first page returned - which is exactly this case")
	require.Equal(t, 0, res.Checked,
		"THE EXACT SIBLING AT THE ZERO END, bracketing the mid-pass test's range assertion")
	require.Equal(t, 0, res.Invalid)
	require.Equal(t, 0, countRecordedFailures(t, h),
		"THE DISCRIMINATOR. A `budget <= 0 means unbounded` branch records all three planted rows and "+
			"restores the unbounded boot from a zero value")
	require.Less(t, elapsed, 10*time.Second,
		"an expired budget must fail PROMPTLY from the first page query. A slow failure here means the "+
			"child deadline is not reaching pgx, and the only reason this assertion is loose is that its "+
			"job is to separate prompt from hung, not to benchmark")
}

// TestValidateStoredSpecsOnStartup_AShutdownIsNotATruncation is the guard for
// "Truncated comes from the child context, never from the returned error".
//
// IT KILLS THE LIKELIEST WRONG IMPLEMENTATION, `Truncated: err != nil`. A
// mid-boot SIGTERM cancels the parent, so the child's Err() is
// context.Canceled and not DeadlineExceeded, and a pass that conflated them would
// print a line advertising a deadline that did not fire and prescribing a knob
// that is not the problem.
//
// THE BUDGET IS GENEROUS AND CANNOT FIRE, which is what makes the cancellation
// the only possible cause. The instrument is the existing cancellation test's: the
// hook fires on the END of the first RecordScheduledJobFailure, which pgx calls on
// this goroutine after the Exec has completed, so row 1's write lands and the row
// loop's next iteration is the very next thing to run. No sleep, no poll, no
// second goroutine.
//
// IT IS A SEPARATE TEST FROM ...ACancelledSweepReturnsInsteadOfLoggingEveryRow
// because that test's subject is log volume, and a second subject in one test
// makes a red ambiguous.
func TestValidateStoredSpecsOnStartup_AShutdownIsNotATruncation(t *testing.T) {
	var discarded bytes.Buffer
	captureLog(t, &discarded)

	h := newRunnerHarness(t)
	owner := h.createUser(t, "shutdown-not-truncation@example.com")
	seedBrokenSchedules(t, h, owner, 3)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := &sweepTracer{}
	var once sync.Once
	tr.setOnEnd(func(sql string) {
		if strings.Contains(sql, "-- name: RecordScheduledJobFailure") {
			once.Do(cancel)
		}
	})
	q := store.New(tracedPool(t, h, tr))

	res, err := schedrunner.ValidateStoredSpecsOnStartup(ctx, q, 60*time.Second)

	require.ErrorIs(t, err, context.Canceled,
		"a parent cancellation must come back as Canceled; if this is DeadlineExceeded the 60s budget "+
			"fired, and this test is no longer about a shutdown")
	require.False(t, res.Truncated,
		"THE MUTANT THIS KILLS is `Truncated: err != nil`. A shutdown stopped this pass, not the "+
			"budget, and claiming the deadline fired sends the operator to the wrong knob")
	require.Equal(t, 1, res.Checked,
		"EXACTLY the one row validated before the cancellation. 2 means the counter sits ABOVE the "+
			"ctx.Err() fence and is counting a row the pass never checked")
	require.Equal(t, 1, res.Invalid)
	require.Equal(t, 1, countRecordedFailures(t, h),
		"ANTI-VACUITY: without this, a sweep that returned before doing any work satisfies everything above")
}

// TestValidateStoredSpecsOnStartup_ADeadlineMidPassReportsPartialCountsAsFloors
// is the guard that the pass actually STOPS EARLY, not merely that it returns.
//
// A TEST THAT PASSES WHEN THE DEADLINE NEVER FIRES PINS NOTHING, which is why
// `res.Checked < planted` is asserted alongside Truncated: the planted set is more
// than one page, so a pass that ran to exhaustion is plainly distinguishable from
// one the budget stopped.
//
// THE TIMING IS ARRANGED WITHOUT A POLL OR A SECOND GOROUTINE. The hook runs on
// the caller's goroutine after the first RecordScheduledJobFailure's Exec
// completes, so a single sleep past the remaining budget means the row loop's next
// ctx.Err() check is the very next thing to run and it sees an expired deadline.
//
// THE Checked ASSERTION IS A RANGE, DELIBERATELY, AND IT HAS EXACT SIBLINGS ON
// BOTH SIDES. An exact 1 would go red on a machine where the page read itself
// outlives the budget, and a flaky guard for a real property gets deleted.
// ...AnExpiredBudget... pins 0 exactly, ...AShutdownIsNotATruncation pins 1
// exactly, and ...ReadsInPagesOfOneHundred pins the full count, so the loosened
// middle is bracketed on both sides.
func TestValidateStoredSpecsOnStartup_ADeadlineMidPassReportsPartialCountsAsFloors(t *testing.T) {
	var discarded bytes.Buffer
	captureLog(t, &discarded)

	// More than one page, so a complete pass is far from a truncated one.
	const planted = 150
	// Budget and sleep come from Task 0's M5. The sleep must exceed the budget.
	const budget = 10 * time.Second
	const sleep = 12 * time.Second

	h := newRunnerHarness(t)
	owner := h.createUser(t, "deadline-midpass@example.com")
	seedBrokenSchedules(t, h, owner, planted)

	tr := &sweepTracer{}
	var once sync.Once
	tr.setOnEnd(func(sql string) {
		if strings.Contains(sql, "-- name: RecordScheduledJobFailure") {
			once.Do(func() { time.Sleep(sleep) })
		}
	})
	q := store.New(tracedPool(t, h, tr))

	res, err := schedrunner.ValidateStoredSpecsOnStartup(context.Background(), q, budget)

	require.True(t, res.Truncated,
		"the budget expired mid-pass, so the result must say so; the flag is what stops the boot line "+
			"presenting a floor as a total")
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"this error comes from the row loop's own ctx.Err(), so it is the stdlib sentinel and does not "+
			"depend on how pgx wraps anything")
	require.Greater(t, res.Checked, 0,
		"the pass must have checked SOMETHING before the deadline. 0 means this machine's first page "+
			"read outlived the %s budget, which is a too-tight budget for this lane rather than a "+
			"defect in the sweep - raise budget and sleep together", budget)
	require.Less(t, res.Checked, planted,
		"THE REFUSAL IS THE POINT. A pass that reached all %d planted rows was never stopped by its "+
			"budget, and a test that passes when the deadline never fires pins nothing", planted)
	require.Equal(t, res.Invalid, countRecordedFailures(t, h),
		"every verdict this pass formed was a fresh message on a row nobody had touched, so each one "+
			"landed; a mismatch means the counter and the write site disagree")
}

// TestValidateStoredSpecsOnStartup_CountsCheckedAndInvalidSeparately pins that
// the two counts are two different quantities, and that Invalid counts VERDICTS
// rather than writes.
//
// THE MIXED FIXTURE IS THE FIRST HALF. seedBrokenSchedules plants only broken
// rows, so the existing paging test gives Checked == Invalid == 250 and a mutant
// returning Checked for both fields survives. 5 healthy and 3 broken separates
// them.
//
// THE SECOND PASS IS THE SECOND HALF, and it is what the field's own comment is
// about. On the second pass the three broken rows already carry the identical
// message, so RecordScheduledJobFailure's `last_error IS DISTINCT FROM` predicate
// makes every UPDATE a no-op: three verdicts, zero writes. A mutant that counts
// writes reports Invalid 0 there and 3 on the first pass, and only the second
// pass can tell them apart.
func TestValidateStoredSpecsOnStartup_CountsCheckedAndInvalidSeparately(t *testing.T) {
	var discarded bytes.Buffer
	captureLog(t, &discarded)

	h := newRunnerHarness(t)
	owner := h.createUser(t, "counts-separately@example.com")
	seedHealthySchedules(t, h, owner, 5)
	seedBrokenSchedules(t, h, owner, 3)

	first, err := schedulerSweep(t, h)
	require.NoError(t, err)
	require.Equal(t, 8, first.Checked, "every enabled row is checked, healthy or not")
	require.Equal(t, 3, first.Invalid, "only the broken ones form a verdict")
	require.False(t, first.Truncated, "a generous budget must not report truncation")
	require.Equal(t, 3, countRecordedFailures(t, h))

	second, err := schedulerSweep(t, h)
	require.NoError(t, err)
	require.Equal(t, 8, second.Checked)
	require.Equal(t, 3, second.Invalid,
		"INVALID COUNTS VERDICTS, NOT WRITES. Every UPDATE on this pass was refused by the "+
			"identical-message predicate, so a counter placed after that branch reports 0 here while "+
			"staying green on the first pass")
	require.False(t, second.Truncated)
	require.Equal(t, 3, countRecordedFailures(t, h),
		"and nothing new was written, which is what makes the 3 above a count of verdicts")
}

// schedulerSweep runs one pass with a budget generous enough that it cannot fire,
// so a test about counts is never about timing.
func schedulerSweep(t *testing.T, h *runnerHarness) (schedrunner.SweepResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	return schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q, 60*time.Second)
}
```

- [ ] **Step 2: Run the four guards**

```powershell
go test -tags integration -count=1 ./internal/schedrunner/... -v -timeout 1800s -run "TestValidateStoredSpecsOnStartup_"
```

Expected: all four new guards PASS, along with the three existing `TestValidateStoredSpecsOnStartup_*` tests.

**Decide M3 here.** If `require.ErrorIs(t, err, context.DeadlineExceeded)` in G3 fails, delete that one line, record the verbatim error text as M3 = NO, and add a one-sentence comment naming what pgx actually returns and that the verdict comes from the context instead.

**If G5 fails on `res.Checked > 0`, the budget is too tight for this machine** - raise `budget` and `sleep` together (keeping `sleep > budget`) and record the new values as M5. That is the failure the message is written for, and it is not a defect in the sweep.

- [ ] **Step 3: Run the whole lane once**

```powershell
go test -tags integration -count=1 ./internal/schedrunner/... ./cmd/relay-server/... -timeout 1800s
```

- [ ] **Step 4: Do not commit yet** - Task 7's battery runs first.

---

## Task 7: Mutation battery 2, then Commit B

**Files:** none modified. Mutations run through `go test -overlay` against `internal/schedrunner/startup_validation.go`.

- [ ] **Step 1: Copy the subject and write the overlay**

```powershell
Copy-Item "$W/internal/schedrunner/startup_validation.go" "$SP\mutants\sweep_orig.go"
```

Overlay target: `D:/dev/relay/.claude/worktrees/lane-boot-deadline/internal/schedrunner/startup_validation.go`. Test selector for every run below:

```powershell
go test -overlay "$SP\overlay.json" -tags integration -count=1 ./internal/schedrunner/... -run "TestValidateStoredSpecsOnStartup_" -timeout 1800s
```

- [ ] **Step 2: The control that must die first**

Make the function `return SweepResult{}, nil` immediately after building the child context.

Expected: G3 (`require.Error`), G4, G5 and G6 all RED. **If anything is green, the harness is broken.** Fix it before trusting a result.

- [ ] **Step 3: Run the battery**

| # | Mutation | Expected kill | Record: RED test + assertion |
| --- | --- | --- | --- |
| B2.1 | row-loop site: `res.Truncated = true` unconditionally | G4 (`require.False(res.Truncated)`) | `______` |
| B2.2 | delete the page-query site's `res.Truncated = ...` line | G3 (`require.True(res.Truncated)`) | `______` |
| B2.3 | add `if budget <= 0 { budget = time.Hour }` before `WithTimeout` | G3 (`countRecordedFailures == 0`) | `______` |
| B2.4 | move `res.Checked++` ABOVE the `ctx.Err()` check | G4 (`res.Checked == 1`) | `______` |
| B2.5 | move `res.Invalid++` below the `n == 0` continue (count writes) | G6 second pass (`Invalid == 3`) | `______` |
| B2.6 | `errors.Is(ctx.Err(), context.Canceled)` at both sites | G3 and G4 both | `______` |
| B2.7 | pass the PARENT ctx to `ListEnabledScheduledJobsPage` (timeout built, never applied) | G3 (`Checked == 0`, `countRecordedFailures == 0`) | `______` |
| B2.8 | `res.Truncated = err != nil` at both sites (the spec's named wrong implementation) | G4 | `______` |
| B2.9 | set `res.Truncated` unconditionally before the final `return res, nil` | nothing expected - record as SURVIVED | `______` |

B2.9 is in the table precisely because it is expected to survive: every completed-pass test uses a budget that cannot fire, so `ctx.Err()` is nil there. Record it as a survivor with that reason rather than inventing a guard for an unreachable state.

**A kill must name its guard.** B2.6 reddens two tests; record both and check that each failed on the assertion about the flag rather than on something incidental.

- [ ] **Step 4: Commit B**

```powershell
git diff --stat -- internal/schedrunner/
git ls-files --eol internal/schedrunner/startup_validation_deadline_integration_test.go
```

```bash
git add internal/schedrunner/startup_validation_deadline_integration_test.go
git commit -F - <<'EOF'
test(schedrunner): pin the budget's refusal, the shutdown distinction and the two counts

Four integration guards, in the lane CI's pg-integration job runs:

- an EXPIRED budget checks nothing rather than running unbounded, and fails
  promptly from the first page query. Its discriminator is the recorded-failure
  count: a `budget <= 0 means unbounded` branch records all three planted rows.
- a SHUTDOWN is not a truncation. The parent is cancelled mid-pass with a
  budget that cannot fire, so Canceled and DeadlineExceeded are separable, and
  `Truncated: err != nil` dies here.
- a DEADLINE MID-PASS reports partial counts as floors, and the guard asserts
  the REFUSAL - Checked strictly below the planted count - not merely that the
  call returned. A test that passes when the deadline never fires pins nothing.
- Checked and Invalid are counted SEPARATELY, over a mixed fixture, and the
  pass runs TWICE: on the second pass every UPDATE is refused by the
  identical-message predicate, so three verdicts produce zero writes. That
  second pass is the only thing that distinguishes a verdict counter from a
  write counter.

pgx's expired-context error satisfies errors.Is(..., context.DeadlineExceeded): <M3>
G5's measured first-page read: <M5>; budget <M5>; sleep <M5>.

Mutations killed (mutation -> guard -> assertion):
  <one line per B2.x>
Survivors, recorded rather than chased:
  Truncated set unconditionally before the nil return - unreachable in any
  completed-pass fixture, since those budgets cannot fire.
EOF
```

---

## Task 8: README, and Commit C

**Files:**
- Modify: `README.md` - four exact-anchor edits

- [ ] **Step 1: Re-check M6 at the moment of the edit**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-deadline
git fetch origin
git log --oneline -5 origin/main -- README.md
```

The conductor's answer is that the only other open lane owns README's source-workspaces field table, not the scheduled-jobs section. Confirm and record. **The cut line is not taken**: all four edits ship, and Commit C says so.

- [ ] **Step 2: Record the line count, then make the four edits**

```powershell
(Get-Content README.md).Count
```

**Edit 1 - startup sequence, item 5 (around line 323).** Replace exactly:

```
5. Reconcile scheduled jobs (advance any `next_run_at` that fell in the past while the server was down), then re-validate every enabled schedule's stored spec and record the ones that no longer validate, then start the scheduler polling loop. All three run before the HTTP listener.
```

with:

```
5. Reconcile scheduled jobs (advance any `next_run_at` that fell in the past while the server was down), then re-validate every enabled schedule's stored spec and record the ones that no longer validate, then start the scheduler polling loop. All three run before the HTTP listener. **The re-validation pass is bounded** by `RELAY_STARTUP_VALIDATION_DEADLINE` (default `45s`) and says so in the log when it stops early, naming how far it got and that the counts are floors. **The reconcile is NOT bounded**, so a long outage can still delay the HTTP listener by an amount that grows with the number of overdue schedules, and neither is `store.Migrate` - `RELAY_DB_STATEMENT_TIMEOUT` deliberately does not reach migrations.
```

**Edit 2 - the server env-var table.** Insert ONE new row immediately after the `RELAY_MAX_SCHEDULES_PER_OWNER` row (it is the last row of that table, around line 297, and it is the knob over the quantity this bound is about):

```
| `RELAY_STARTUP_VALIDATION_DEADLINE` | `45s` | Wall-clock budget for the boot-time re-validation of every enabled schedule's stored spec, the pass that runs between the reconcile and the scheduler loop. **When it runs out, the pass stops and the boot continues**: the rest of the enabled set is not checked, its size is not known, and the boot log says so in three lines naming the bound, how far it got, and both counts as floors. **A truncated pass can only produce false negatives.** The pass only ever ADDS failure records and never clears one, so a `last_error` that is SET is exactly as trustworthy after a truncated pass as after a complete one; only its ABSENCE loses its meaning, and `GET /v1/scheduled-jobs/stats`'s `failing` becomes a floor for that boot. The remainder goes nowhere - the next boot starts again from the beginning of the set, not from where this one stopped, so raising the budget always strictly increases coverage. **There is no value that disables it**: zero, negative and unparseable all keep the default and warn, naming the ignored value, and a very large number (`24h`) is the spelling for effectively-unbounded, which stays visible in the environment and in the startup line. **Read it together with `RELAY_DB_STATEMENT_TIMEOUT`** - the default sits deliberately above that knob's `30s`, because at or below the per-statement bound one slow statement can consume the whole pass. Upgrade consequence: a deployment whose sweep currently takes longer than this boots identically today and starts truncating after this deploy, and the truncation line is how you learn. Requires a server restart to change. |
```

**Edit 3 - the `/v1/scheduled-jobs/stats` field table (around line 1958).** Replace exactly:

```
| `failing` | Schedules in scope carrying a `last_error`. **Not windowed.** |
```

with:

```
| `failing` | Schedules in scope carrying a `last_error`. **Not windowed.** It is a FLOOR when the boot's validation sweep was truncated (`RELAY_STARTUP_VALIDATION_DEADLINE`), because a schedule that was never checked carries no record. |
```

**Do not rewrite the definition** - it is exactly true as written, and rewriting it would author a fresh claim about a complement.

**Edit 4 - the `last_error` explanation.** Insert a new paragraph immediately BEFORE the line `**When a schedule reports a failure:**` (around line 1118), separated by a blank line:

```
**A `last_error` that is present is trustworthy; its absence is weaker than it looks.** Absence means "no failure has been recorded", which is not the same as "this spec validates": a schedule that has never fired and was never reached by a boot validation sweep carries no record at all, and after a sweep truncated by `RELAY_STARTUP_VALIDATION_DEADLINE` that is true of every schedule past the point the budget ran out. Nothing clears a record except a successful fire or a `PATCH` that changed `job_spec`, `cron_expr` or `timezone`, so the field never shows a failure that is not real.
```

- [ ] **Step 3: Complement check for the stale global claim**

Re-run Task 0 Step 7's search and confirm every SWEEP-scoped hit is now corrected or deleted. Record the count. **If a hit remains, fix it here** (if it is in a file this slice owns) or **STOP and report** (if it is not).

- [ ] **Step 4: M11 - hygiene, before committing**

```powershell
(Get-Content README.md).Count
git diff --stat -- README.md
git ls-files --eol README.md
# UTF-8 validity and the non-ASCII census: the four inserts are pure ASCII, so
# a non-ASCII byte appearing in the diff is a defect, not a choice.
git diff -- README.md | Select-String -Pattern "[^\u0000-\u007F]"
```

Expected: line count up by about **3** (one env row, one paragraph plus its blank line; edits 1 and 3 replace single lines). `git diff --stat` proportionate to four edits. `i/lf`. **The non-ASCII search must return nothing** - a raw Latin-1 byte written where an escape was needed passes every other check here and silently stops the file being valid UTF-8.

**If the diffstat is far larger than four edits**, a programmatic rewrite has reclassified line endings. Do NOT conclude "nothing to revert" from `git diff` alone; check `git status` too, and restore from `git stash` or a scratchpad copy rather than re-running the edit.

- [ ] **Step 5: Commit C**

```bash
git add README.md
git commit -F - <<'EOF'
docs: name the startup validation deadline, and what a truncated pass costs its readers

Four edits, and the last two cross into the scheduled-jobs section on purpose -
that is where the only NUMERIC reader of this sweep's output lives, and a
disclosure belongs where the signal is READ. The spec offered a cut line in
case a sibling lane held that section; no lane does, so the pair ships whole
rather than silently reduced to one.

- Startup sequence item 5: the re-validation pass is bounded; the reconcile and
  store.Migrate still are not, named in the same breath.
- A server env-var row for RELAY_STARTUP_VALIDATION_DEADLINE: the truncation
  consequence, the false-negative-only asymmetry, that nothing disables it, that
  the remainder goes nowhere, the RELAY_DB_STATEMENT_TIMEOUT relationship, and
  the upgrade consequence.
- The /v1/scheduled-jobs/stats `failing` row: one sentence saying it is a floor
  after a truncated sweep. The definition itself is untouched - it is exactly
  true as written.
- The last_error explanation: presence is trustworthy, absence is not the same
  as "this spec validates".

README line count <before> -> <after>; git ls-files --eol reads i/lf; no
non-ASCII byte introduced.
EOF
```

---

## Task 9: Full gates, handoff, and the conductor's close

- [ ] **Step 1: Every gate, from a clean tree**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-boot-deadline
git status --porcelain            # must be clean
go build ./...
go vet ./...
go vet -tags integration ./...
go test ./... -count=1
make test-pg-integration
```

All green. Compare against M1's baseline: **a failure that was red in M1 is not yours, and a failure that was green in M1 is.**

- [ ] **Step 2: The race lane**

CI runs `go test -race ./...` with no tags. This slice adds no goroutine and no shared state, so the value is mostly that the build is clean - but run it, because the lane exists:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

The container is the reliable route on this machine, and it is also the only local way to run the `//go:build !windows` files that Windows silently skips. **If it is genuinely unavailable, state plainly in the handoff that `-race` did not run.** Do not substitute `-count=N`: that re-runs under the ordinary scheduler and raises confidence in flakiness, not in race-freedom.

- [ ] **Step 3: Confirm the untouched files really are untouched**

```powershell
git diff --stat origin/main -- cmd/relay-server/schedrunner_startup_wiring_test.go
git diff --stat origin/main -- internal/store/ web/ Makefile .github/ internal/worker/ internal/agent/
```

Both must be EMPTY. A non-empty second line means this slice reached into the sibling lane's scope or into generated code.

- [ ] **Step 4: Handoff**

Report, with the ledger filled in:

- M1 through M11, each with the value actually observed and the input it was taken with. **Never report a measurement without its input** - a bare rows-per-second figure reads as the typical case.
- Whether M2 moved the 45s default, and if so to what and why.
- M3's answer and which of G3's two assertion branches survives in the committed test.
- The mutation tables from Tasks 5 and 7, including the survivors.
- M10's pasted boot lines, which are the evidence for the three-line decision.
- Anything that did not run, named plainly.

- [ ] **Step 5: Conductor closes the backlog item**

Not the engineer's step. The conductor runs:

```
/backlog close wall-clock-deadline-on-the-boot-sweep
```

That `git mv`s the file into `docs/backlog/closed/`, stamps the frontmatter and appends a `## Resolution` note. **Never flip `status:` by hand** - `/backlog list` reports that as a malformed open item.

The resolution note must record what was and was not met, because the item's own acceptance criteria are not all achievable as worded:

- **Criterion 1 met, with the honest bound.** The pass cannot delay `ListenAndServe` past `budget` plus ONE ROW's work, and that residual is a per-row constant independent of the number of stored schedules, which is exactly what the criterion asks. It is not zero.
- **Criterion 2 met in the sense that matters and not in the sense it is written.** "A truncated sweep says so wherever its result is read" is not achievable: per-row disclosure on `scheduled_jobs` would require writing to every row the pass did not check, which is the unbounded work the deadline just declined to do. What ships instead is sharper: the sweep only ever ADDS records, so truncation produces a false NEGATIVE and can never produce a false POSITIVE, and that asymmetry is stated in the boot line and at all three README sites. `GET /v1/scheduled-jobs/stats`'s `failing` is documented as a floor and **not fixed** - filed as its own item.
- **Criterion 3 met.** The remainder goes nowhere and the next boot starts from the beginning of the set. The resume-after-the-listener option is refused on a fence gap, not on cost, and that gap is filed as its own item - which is what makes the refusal falsifiable.

---

## Self-review

**Spec coverage.** Decision 1 -> Task 2 Step 4. Decision 2 (`SweepResult`) -> Task 2 Step 1. Decision 3 (`Truncated` from the child) -> Task 2 Step 4 + G3/G4. Decision 4 (remainder nowhere) -> the truncated line's third clause + Task 0 Step 1's item 1. Decision 5 (bounds the sweep, not the boot) -> Task 4 Step 6 + README edit 1. Decision 6 (no off token, zero is expired) -> Task 3 Step 1 + G1 + G3. Decision 7 (name and default) -> Task 3 Step 1 + G1b + M2. Decision 8 (the exact lines) -> Task 3 Step 1 + G2/G2b/G2c + M10. Guards G1-G6 -> Tasks 3 and 6. The seven existing call sites -> Task 4 Step 4. "The wiring guard needs no change" -> honoured for `schedrunner_startup_wiring_test.go`, and refuted as a blanket rule by the new G7 in its own file. Every row of "Prose this slice owes" -> Tasks 2, 4 and 8, plus the two sentences the spec's table missed (refutations 5 and 6). All seven deferred measurements -> Task 0's ledger, M1-M8.

**Placeholders.** None. Every code step carries the code. The only blanks are the Record boxes in Task 0 and the mutation tables, which are blanks ON PURPOSE - they are values to obtain, and the plan says what each one blocks.

**Type consistency.** `ValidateStoredSpecsOnStartup(ctx context.Context, q *store.Queries, budget time.Duration) (SweepResult, error)` is the signature in Task 2, in Task 4's seven call sites, in Task 6's four guards, in Task 0's throughput scratch file, and in G7's `require.Len(call.Args, 3)`. `SweepResult` has exactly the fields `Checked int`, `Invalid int`, `Truncated bool` at every use. `startupValidationLines(res schedrunner.SweepResult, err error, budget, elapsed time.Duration) []string` and `startupValidationDeadlineLine(budget time.Duration) string` match between Task 3, Task 4's `main` block and G7's call-name assertions. `parseStartupValidationDeadline(name, raw string) (time.Duration, string)` matches Task 3, Task 4 and G1.
