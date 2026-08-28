# A permanently un-fireable schedule must be visible - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `scheduled_jobs` a `last_error` / `last_error_at` pair that the schedrunner writes on a permanent fire failure and clears on a successful fire, and surface it on the REST response, the SPA list and detail pages, the CLI, and the Python SDK, so a schedule that a retroactive validation change killed is discoverable without suspecting it first.

**Architecture:** Two nullable columns (migration `000022`). `fireOne` gains an explicit `jobspec.Validate` hoist so a permanent, operator-facing failure can be told apart from an infrastructure fault, and wraps the three permanent classes in an unexported marker type. `TickOnce` classifies the returned error **after** the savepoint rollback and writes the failure on the OUTER transaction. `AdvanceScheduledJob` clears the pair on a successful fire; a new `AdvanceScheduledJobSkipped` handles the `skip` branch without clearing; `UpdateScheduledJob` clears conditionally on a boolean argument the PATCH handler sets from which keys were supplied. A record-only startup sweep populates the surface for long-cadence schedules at deploy time.

**Tech Stack:** Go 1.26, sqlc, pgx/v5, testify, testcontainers-go (integration lanes), Postgres 16, React 18 + TanStack Query + vitest/jsdom + Playwright, Python 3 + pydantic.

**Spec:** `docs/superpowers/specs/2026-08-28-unfireable-schedule-visibility.md`
**Backlog item closed:** `docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md`

**THIS PLAN IS IN TWO FILES.** This one carries the independence declaration, the refutations, the
standing constraints, and Slices A (Tasks A1-A9) and A2 (Task A10). Slices B, C and D (Tasks B1-D1),
the nine verification gates, and the conductor steps are in
`docs/superpowers/plans/2026-08-28-unfireable-schedule-visibility-clients.md`. Dispatch the client
slices from that file; read this one first for the constraints that govern all of them.

---

## Slice independence declaration

**Read this before dispatching Phase 3. The spec's claim that all four slices are independent is HALF TRUE and the conductor must not inherit it unchecked.**

**This is one plan, one PR, one session.** It has no `## Stage N` headings and must NOT be handed to `/backlog phases`. It is large, and if the conductor wants it split across sessions the only natural cut is backend (Slice A + A2) then clients (Slices B + C + D) - but that split would need this plan re-issued with `## Stage N` headings, which it deliberately does not have.

### What is genuinely parallel

- **Slice A (backend) and Slice A2 (startup sweep)** are strictly sequential: A2 uses `permanent()`, `recordableFailure()` and the `RecordScheduledJobFailure` statement that A creates.
- **Slices B (frontend), C (CLI) and D (Python SDK) are independent of each other.** They touch disjoint files, share no symbol, and each has its own lane. Dispatch all three concurrently.

### What FORCES a sequence, and why the spec missed it

The spec says (section 10) "Every frontend test in `web/src/schedules/` is fixture-driven, so the frontend needs no running server." That is true of the **vitest** half and false of the **Playwright** half the same spec demands in section 7.3. Two client tasks need Slice A's column to exist in a real database:

1. **Task B4 (Playwright).** The spec asks for the FAILING chip to be measured in a real browser against a POPULATED table. `web/e2e/fixtures.ts` seeds exclusively through the REST API and forbids direct SQL by a written rule - and **no REST path can produce a `last_error`**: `handleCreateScheduledJob` and `handlePatchScheduledJob` both validate before storing, so a spec that fails validation cannot be stored through either. Resolved in Task B4 by a Playwright route interception rather than a seeded row (see that task for the full argument), which removes the dependency on A entirely. **After that resolution B4 does not need A.**
2. **Task C4 (CLI integration lane).** It writes `last_error` with a direct `UPDATE` through `s.Pool` and reads it back through a real `internal/api` server. This one genuinely needs the migration and the response fields.

### The declaration the conductor should act on

- **Dispatch Slice A first, alone.** Tasks A1 to A9.
- **Once Task A6 (response fields) has landed in the tree, dispatch B, C and D concurrently.** Slice A2 may run in the same wave as B/C/D - it touches only `internal/schedrunner`, `internal/store/query/scheduled_jobs.sql` and `cmd/relay-server/main.go`, none of which B, C or D touch.
- The only cross-slice contract is the wire shape, and it is frozen here: `last_error` (string) and `last_error_at` (RFC3339 string), both `omitempty`, **absent means healthy** - never `""`, never `null`.
- If the conductor insists on dispatching B/C/D concurrently with A: everything in B, C and D except **Task C4** will complete against a tree without the column, because their fixtures are hand-written. C4 must then be re-run after A lands.

---

## What I refuted in the spec

Six findings. Four change work that would otherwise be done wrong; two are corrections to claims the spec makes about the tree.

### F1. The spec says README has "nothing to correct". README currently DOCUMENTS THE DEFECT, in two places, and both become false prose

Spec section 7.6 opens "README documents the scheduled-jobs endpoints but not a single response field, so there is nothing to correct - only a short subsection to add." That is wrong, and it is exactly the project's dominant defect class.

- `README.md`'s "A stored spec is re-validated on every fire, so job-spec rules are retroactive" paragraph ends: "**and the only record is one line in the server log**." Slice A makes that sentence false.
- The next paragraph reads: "**`relay schedules update` has no `--spec` flag**, so from the CLI it is delete plus `relay schedules create --spec`." Slice C makes that sentence false.

Both are load-bearing operator instructions. Task A9 fixes the first (it belongs to the slice that invalidates it); Task C5 fixes the second, plus the `relay schedules list` output-columns line and the `relay schedules update` flag table. **Cite by symbol, never by line number** - the spec's own R4 records that `web/src/schedules/api.ts`'s line citations have already rotted.

### F2. `last_error` is `*string`, not `pgtype.Text`. Every code sample in the spec that touches it is unwritten and every guess is likely wrong

`sqlc.yaml` sets `emit_pointers_for_null_types: true`. Measured against the tree: `store.Worker.AgentTokenHash` is a nullable `TEXT` column and sqlc emits it as `*string`, not `pgtype.Text`. `store.Task.TimeoutSeconds` is a nullable `INTEGER` and is `*int32`. Nullable `TIMESTAMPTZ` stays `pgtype.Timestamptz` (`LastRunAt`, `AssignedAt`).

So after `sqlc generate` this plan expects:

```go
LastError   *string            // NOT pgtype.Text
LastErrorAt pgtype.Timestamptz
```

Every read is `if sj.LastError != nil`, every write is `LastError: &text`. **Task A2 includes a step that reads the regenerated file and confirms this**, because it is an inference from a sibling column and not a measurement of the generator's actual output for this one.

### F3. The spec's Playwright requirement is not satisfiable by the e2e harness's own rules

Spec 7.3 asks for the browser lane "against the schedules list at a narrow viewport with at least one failing row present", measuring "the populated state, not an empty table". `web/e2e/fixtures.ts` carries a written rule that fixtures are created through the REST API and **not** by direct SQL, with a stated reason (a direct-SQL fixture could encode a state production cannot produce). No REST path can create a `last_error`. So the requirement as written cannot be met without either breaking that rule or adding `@types/pg` so a `.ts` setup file can talk to Postgres (`web/tsconfig.json` includes `e2e` under `strict`, and `pg` is a devDependency deliberately consumed only from `.mjs` "so `pg` needs no @types").

Task B4 resolves it with a **Playwright route interception** that rewrites the real server's real list response to carry `last_error` on one real seeded row. That is honest for what the browser lane is actually for here (layout), and the full wire chain is pinned elsewhere and in CI by Task C4. The argument is written into the code in Task B4 so nobody reads it as laziness.

### F4. The spec's CI story understates what CI can run, by one whole lane

Spec section 8.3 concludes "accept, and pay for it two ways" with two default-lane siblings. It never considers `make test-cli-integration`, which **does run in CI** (`.github/workflows/go-ci.yml`, job `cli-integration`) and whose harness (`internal/cli/relayharness_integration_test.go`, `startRelayServer`) gives a test a **real `internal/api` server over HTTP, a real migrated Postgres, and a raw `*pgxpool.Pool`** - and deliberately does not wire schedrunner, so nothing can clobber a planted row.

That means Task C4 can, in CI, prove: the column exists, `toScheduledJobResponse` maps it, the JSON key is right, and a client decodes and renders it. It does **not** prove the schedrunner writes it - that half stays integration-only and uncovered. Record the split honestly; do not upgrade "the response half runs in CI" into "the fix is covered by CI".

### F5. `web/src/schedules/*.test.tsx` fixtures ARE built from the `Schedule` interface, and that is correct here - do not "fix" it

Spec 8.4 says frontend fixtures should be "hand-written JSON shapes, not built from the `Schedule` interface's own type in a way that makes a key rename invisible". Measured: `SchedulesTable.test.tsx` and `ScheduleDetailPage.test.tsx` both build fixtures through `function sched(over: Partial<Schedule>): Schedule`. Leave them. The CLAUDE.md vacuous-fixture rule is about a **Go client type used to encode a fixture that the same Go client type then decodes**; on the TS side the interface is not a decoder, `tsc` is, and a rename of `last_error` in `api.ts` breaks every consumer at compile time. The property that a TS fixture cannot check - that the SERVER actually sends `last_error` - is pinned by Task A6 (untagged, in CI) and Task C4 (integration, in CI), which is where it belongs.

### F6. Line citations in the spec and in the tree

Spec section 2's evidence table and `web/src/schedules/api.ts`'s comments cite line numbers that have already moved (`internal/api/scheduled_jobs.go:147-169` for `ownedScheduledJob` is now `:149`; `:521-528` for `patchScheduledJobRequest` is now `:538`; `scheduled_jobs.sql:32-43` for `UpdateScheduledJob` is now `:32-43` only by luck and moves in Task A2). Task B1 converts the `api.ts` citations to symbol names. **Add no new line-number citations anywhere in this slice.**

### Checked, not refuted, recorded so nobody re-derives it

- `store.Migrate` embeds `migrations/*.sql` by glob, so a new `000022` pair needs no registration.
- `internal/api` already imports `internal/schedrunner` (`ValidateMinInterval`, `ParseSchedule`) and `schedrunner` imports nothing from `internal/api`, so Task A8 constructing a `schedrunner.Runner` inside an `internal/api` integration test creates no cycle.
- `jobspec.Validate` is idempotent for the purposes of the hoist: `normalizeTaskCommands`'s switch sees `hasCommand == false, hasCommands == true` on a second call and falls through without error.
- `store.ScheduledJob` has 13 fields at HEAD and `scheduledJobResponse` has 14 (the extra is `OwnerEmail`, filled by `fillOwnerEmails`). After this slice: 15 and 16. Task A6 pins that relationship with an arity guard.
- `Table`/`TableRow` render real `role="table"` / `role="row"` / `role="cell"`, so both the vitest cell scoping in Task B2 and the Playwright row filter in Task B4 are valid locators.
- `relayclient.PageEnvelope` json tags are `items` / `next_cursor` / `total`. Task C2's hand-written fixture uses those exact keys.
- The highest migration is `000021_tasks_assigned_at`. Next is `000022`.

---

## Standing constraints every task inherits

Read these once. They are repeated inside the tasks that can get them wrong.

**S1. `make` is NOT installed on this machine.** Every command in this plan is the literal underlying command transcribed from the `Makefile`. Never write `make <target>` into a terminal.

| Makefile target | Literal command |
|---|---|
| `make test` | `go test ./... -timeout 120s` |
| `make test-integration` | `go test -tags integration -p 1 ./... -timeout 900s` |
| `make test-cli-integration` | `go test -tags integration -count=1 ./internal/cli/... -timeout 480s` |
| `make vet-integration` | `go vet -tags integration ./...` |
| `make test-race` | `go test -race ./... -timeout 180s` |
| `make generate` | `sqlc generate` then `buf generate` |
| `make web-build` | `cd web && npm run build` |
| `make test-e2e` | see Task B4 - it is four commands, and the order is load-bearing |

**S2. Never use em dashes or en dashes.** Not in the plan, not in code comments, not in operator strings, not in README edits, not in commit messages. Hyphens only. (`internal/cli/schedules.go` already contains one in an existing error string. Leave it; do not widen the change.)

**S3. This is a CRLF repo and `git diff` and `git status` disagree by design.** After ANY programmatic edit to a tracked text file, and always after `sqlc generate`:

```powershell
git diff --ignore-all-space
git status --short
git ls-files --eol <every touched path>
git diff --stat
```

Every touched path must read `i/lf`. **Never conclude "nothing to revert" from `git diff` alone** - `core.autocrlf=true` normalizes LF churn out of `git diff` while `git status` still lists the files as modified. Compare the diffstat against the size of the change you intended: a two-line change that commits as 1800 insertions means a programmatic rewrite reclassified a file as binary.

**S4. Never edit `internal/store/*.sql.go` or `internal/store/models.go` by hand.** They are sqlc output. Edit `internal/store/query/*.sql` and regenerate.

**S5. Every test body in this plan is a GUESS until an engineer runs it.** Plan-supplied tests are untrusted. Each task states what makes its test RED at HEAD and what makes it GREEN after; if the observed RED is not the stated RED, stop and report rather than adjusting the test until it passes.

---

## File structure

**Create:**

| Path | Responsibility |
|---|---|
| `internal/store/migrations/000022_scheduled_jobs_last_error.up.sql` | Two nullable `ADD COLUMN`s. No default, no backfill, no CHECK. |
| `internal/store/migrations/000022_scheduled_jobs_last_error.down.sql` | Drops both. |
| `internal/store/scheduled_jobs_last_error_integration_test.go` | Migration up/down/up round trip and column typing. |
| `internal/schedrunner/failure.go` | `permanentFireError`, `permanent`, `recordableFailure`, `sanitizeFailureText`. Pure, no database. |
| `internal/schedrunner/failure_test.go` | Untagged, `package schedrunner`. The default-lane sibling for classification and sanitization. |
| `internal/schedrunner/startup_validation.go` | Slice A2. `ValidateStoredSpecsOnStartup`, record-only. |
| `internal/schedrunner/startup_validation_integration_test.go` | Slice A2 proof. |
| `internal/schedrunner/skip_preserves_failure_integration_test.go` | The `skip` branch must not clear a recorded failure. |
| `internal/api/scheduled_jobs_response_test.go` | **Untagged**, `package api`. Wire-contract and arity guard for `toScheduledJobResponse`. Runs in CI. |
| `internal/api/scheduled_jobs_failure_visibility_integration_test.go` | The headline regression test. Does NOT run in CI. |
| `internal/cli/schedules_failure_integration_test.go` | The CI-running response-shape proof. |

**Modify:**

| Path | Change |
|---|---|
| `internal/store/query/scheduled_jobs.sql` | `AdvanceScheduledJob` clears; new `AdvanceScheduledJobSkipped`, `AdvanceScheduledJobAfterFailure`, `RecordScheduledJobFailure`, `ListEnabledScheduledJobs`; `UpdateScheduledJob` gains `clear_failure`. |
| `internal/schedrunner/runner.go` | `fireOne` hoist + `permanent` wrapping; `TickOnce` failure branch classifies; `advance`/`advanceSkipped`/`advanceAfterFailure`. |
| `internal/schedrunner/scheduled_job_surface_test.go` | `scheduledJobFields` gains two names. Update the list; do NOT add an exemption. |
| `internal/schedrunner/stored_spec_bounds_test.go` | Rename off `_DocumentedHazard`, rewrite the header, add the recorded-and-cleared assertions. All six existing assertions STAY TRUE. |
| `internal/api/scheduled_jobs.go` | Two response fields, two mapper lines, `ClearFailure` on the PATCH call. |
| `cmd/relay-server/main.go` | One call to `ValidateStoredSpecsOnStartup`. |
| `README.md` | The false sentence, the new field subsection with the remedy ladder, the CLI docs. |
| `web/src/schedules/api.ts` | Two optional interface fields; symbol citations. |
| `web/src/schedules/SchedulesTable.tsx` | FAILING chip inside the NAME cell. No tenth column. |
| `web/src/schedules/ScheduleDetailPage.tsx` | Sub-line marker + "Last failure" panel. |
| `web/src/schedules/SchedulesTable.test.tsx`, `ScheduleDetailPage.test.tsx` | Present and absent cases. |
| `web/e2e/surfaces.ts`, `web/e2e/layout.spec.ts` | A `prepare` hook and one new surface. |
| `internal/cli/schedules.go` | `scheduleResp` fields, `show` output, `list` STATE column, `update --spec`. |
| `internal/cli/schedules_test.go` | Hand-written-JSON fixtures for the new output. |
| `python/src/relay/models.py`, `python/tests/unit/test_models.py` | Two fields, two tests. |

---

# Slice A - backend, migration, response, README

## Task A1: Migration 000022

**Files:**
- Create: `internal/store/migrations/000022_scheduled_jobs_last_error.up.sql`
- Create: `internal/store/migrations/000022_scheduled_jobs_last_error.down.sql`
- Create: `internal/store/scheduled_jobs_last_error_integration_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/scheduled_jobs_last_error_integration_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lastErrorDownTarget is the schema version just below 000022, i.e. the state
// its down migration restores.
const lastErrorDownTarget = 21

// TestMigration000022AddsLastError asserts both columns exist after the full up
// migration, are NULLABLE, and carry the intended types.
//
// NULLABLE IS THE WHOLE POINT AND IT IS NOT COSMETIC. Migrations are embedded
// and run on startup (cmd/relay-server/main.go), so a migration that can fail is
// a deployment that cannot start. A nullable ADD COLUMN with no DEFAULT, no
// CHECK and no backfill has no existing row it can reject and no expression it
// can fail to evaluate; in Postgres it is a catalog-only change that takes a
// brief ACCESS EXCLUSIVE lock and returns without rewriting the table, whatever
// its size. This is the same reasoning by which the retry-bounds slice declined
// a CHECK constraint on tasks.retries.
//
// NULL also carries meaning: it is how a schedule says "no recorded failure".
// A NOT NULL DEFAULT '' would make an empty string and an absent failure the
// same value, which is precisely what the response's `omitempty` relies on being
// distinguishable.
func TestMigration000022AddsLastError(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	for _, c := range []struct{ column, dataType string }{
		{"last_error", "text"},
		{"last_error_at", "timestamp with time zone"},
	} {
		var dataType, isNullable string
		err := pool.QueryRow(ctx, `
			SELECT data_type, is_nullable
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'scheduled_jobs'
			  AND column_name = $1`, c.column,
		).Scan(&dataType, &isNullable)
		require.NoError(t, err, "scheduled_jobs.%s must exist after migration 000022", c.column)
		assert.Equal(t, c.dataType, dataType, "%s type", c.column)
		assert.Equal(t, "YES", isNullable,
			"%s must be nullable: NULL is how a schedule says it has no recorded failure", c.column)
	}
}

// TestMigration000022DownUp confirms the down migration drops both columns and
// migrating back up round-trips cleanly (no duplicate-column collision).
func TestMigration000022DownUp(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := context.Background()

	countColumns := func() int {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT count(*) FROM information_schema.columns
			WHERE table_schema='public' AND table_name='scheduled_jobs'
			  AND column_name IN ('last_error','last_error_at')`,
		).Scan(&n))
		return n
	}

	require.NoError(t, store.MigrateTo(dsn, lastErrorDownTarget),
		"down migration to 000021 must succeed")
	assert.Equal(t, 0, countColumns(), "down must drop both columns")

	require.NoError(t, store.Migrate(dsn), "re-applying up must succeed")
	assert.Equal(t, 2, countColumns(), "up must restore both columns")
}
```

- [ ] **Step 2: Run test to verify it fails**

Requires Docker Desktop running.

```powershell
go test -tags integration -p 1 ./internal/store/... -run TestMigration000022 -v -timeout 300s
```

Expected: FAIL. `TestMigration000022AddsLastError` fails on the first `require.NoError` with `no rows in result set` (the column does not exist). `TestMigration000022DownUp` reports `2 != 0` or a migration-version error.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/000022_scheduled_jobs_last_error.up.sql`:

```sql
-- last_error records the reason the SCHEDULER last failed to produce a job from
-- this schedule, and last_error_at when it did. Both are NULL for a schedule
-- that has never failed, and both are cleared by a successful fire.
--
-- WHY THIS EXISTS. schedrunner re-validates the stored job_spec on EVERY fire,
-- because fireOne reaches jobspec.Validate. A validation rule added by a later
-- release is therefore retroactive over stored rows: a schedule accepted years
-- ago stops producing jobs the instant the new bound deploys, while next_run_at
-- keeps advancing and nothing anywhere says why. Before these columns the only
-- record was one line in the server log.
--
-- NULLABLE, NO DEFAULT, NO CHECK, NO BACKFILL - AND THAT IS A REQUIREMENT, NOT A
-- STYLE CHOICE. Migrations are embedded and run on startup, so a migration that
-- can fail is a deployment that cannot start. These two statements have no
-- existing row they can reject and no expression they can fail to evaluate. In
-- Postgres a nullable ADD COLUMN with no default is a catalog-only change: a
-- brief ACCESS EXCLUSIVE lock, no table rewrite, whatever the table's size.
--
-- NULL IS LOAD-BEARING. It means "no recorded failure", and the API response
-- relies on being able to distinguish it from an empty string: scheduledJobResponse
-- carries `omitempty` on both fields, so absent means healthy. The write site
-- (internal/schedrunner/failure.go, sanitizeFailureText) never stores an empty
-- string for exactly this reason.
--
-- NO CONSECUTIVE-FAILURE COUNT, deliberately. "How long has this been dead" is
-- readable from the interval last_run_at -> now given clear-on-success semantics,
-- and "is it still being tried" is readable from last_error_at moving. A counter
-- would add a column a restart or a manual edit can desynchronize from reality,
-- and a semantic question ("does a skip reset it?") with no good answer.
ALTER TABLE scheduled_jobs ADD COLUMN last_error TEXT NULL;
ALTER TABLE scheduled_jobs ADD COLUMN last_error_at TIMESTAMPTZ NULL;
```

Create `internal/store/migrations/000022_scheduled_jobs_last_error.down.sql`:

```sql
ALTER TABLE scheduled_jobs DROP COLUMN last_error_at;
ALTER TABLE scheduled_jobs DROP COLUMN last_error;
```

- [ ] **Step 4: Run test to verify it passes**

```powershell
go test -tags integration -p 1 ./internal/store/... -run TestMigration000022 -v -timeout 300s
```

Expected: PASS, both tests.

- [ ] **Step 5: Check line endings and commit**

```powershell
git ls-files --eol internal/store/migrations/000022_scheduled_jobs_last_error.up.sql internal/store/migrations/000022_scheduled_jobs_last_error.down.sql internal/store/scheduled_jobs_last_error_integration_test.go
git diff --stat
```

All three must read `i/lf`. Then:

```powershell
git add internal/store/migrations/000022_scheduled_jobs_last_error.up.sql internal/store/migrations/000022_scheduled_jobs_last_error.down.sql internal/store/scheduled_jobs_last_error_integration_test.go
git commit -m "feat(store): migration 000022 adds scheduled_jobs.last_error and last_error_at"
```

---

## Task A2: The SQL statements, ONE generate, and the field-set tripwire

This task is the single riskiest mechanical step in the slice. It runs `sqlc generate` exactly once, which regenerates `models.go` (picking up the migration from Task A1) **and** all the query changes below. Doing them in two generate rounds doubles the CRLF dance for no benefit.

**Files:**
- Modify: `internal/store/query/scheduled_jobs.sql`
- Regenerated (never hand-edited): `internal/store/models.go`, `internal/store/scheduled_jobs.sql.go`
- Modify: `internal/schedrunner/scheduled_job_surface_test.go`

- [ ] **Step 1: Edit the query file**

In `internal/store/query/scheduled_jobs.sql`, replace the existing `AdvanceScheduledJob` block (currently between `ListOverdueScheduledJobsForCatchup` and `AdvanceScheduledJobNextRun`) with:

```sql
-- name: AdvanceScheduledJob :exec
-- THE SUCCESS STATEMENT, and the ONLY thing that clears a recorded failure.
-- Called from fireOne after CreateJobFromSpec returned a job, which is the only
-- event that proves the stored spec both validates and inserts.
--
-- Its COALESCE($3, last_job_id) is now vestigial: after the skip path was split
-- out into AdvanceScheduledJobSkipped, this statement's single caller always
-- passes a valid job id. LEAVE IT. Removing it is an unrelated behaviour change.
UPDATE scheduled_jobs
SET next_run_at   = $2,
    last_run_at   = NOW(),
    last_job_id   = COALESCE($3, last_job_id),
    last_error    = NULL,
    last_error_at = NULL,
    updated_at    = NOW()
WHERE id = $1;

-- name: AdvanceScheduledJobSkipped :exec
-- The overlap_policy = 'skip' branch, split out of AdvanceScheduledJob so the
-- clearing rule is not hidden behind a parameter overload.
--
-- IT DELIBERATELY DOES NOT CLEAR. The skip branch returns BEFORE
-- jobspec.Validate runs, so reaching it is no evidence the stored spec is valid.
-- Clearing here would make a poisoned schedule whose predecessor is long-running
-- flicker between "failing" and "healthy" on alternate ticks.
--
-- It DOES stamp last_run_at, preserving the behaviour AdvanceScheduledJob had on
-- this path. That means last_run_at has always meant "the runner reached the end
-- of a fire attempt", not "a job was produced". That is pre-existing and is
-- filed separately; do not change it here.
UPDATE scheduled_jobs
SET next_run_at = $2,
    last_run_at = NOW(),
    updated_at  = NOW()
WHERE id = $1;

-- name: AdvanceScheduledJobAfterFailure :exec
-- The failure statement. Called from TickOnce's fireErr branch, on the OUTER
-- transaction, for the three PERMANENT failure classes only (an undecodable
-- job_spec, an unparseable cron, a spec that fails jobspec.Validate).
--
-- IT MUST NOT BE CALLED FROM INSIDE fireOne. fireOne runs against a nested
-- transaction (a savepoint) that TickOnce ROLLS BACK on failure, so a write
-- issued there is discarded silently - the row would simply never carry an error
-- and the test would fail with no clue why. See internal/schedrunner/runner.go's
-- TickOnce for the write site.
--
-- It does NOT touch last_run_at or last_job_id: no run completed and no job
-- exists to point at. It DOES advance next_run_at, so a poisoned schedule does
-- not hot-loop every tick.
--
-- NOW() rather than a Go clock, matching last_run_at immediately beside it.
-- Within one transaction NOW() is the transaction start time, which for a
-- 100-row tick is at most a few seconds stale. Consistency with the field it
-- sits next to beats that; do not "fix" this to time.Now() in isolation.
UPDATE scheduled_jobs
SET next_run_at   = $2,
    last_error    = $3,
    last_error_at = NOW(),
    updated_at    = NOW()
WHERE id = $1;

-- name: RecordScheduledJobFailure :exec
-- The startup validation sweep's statement (schedrunner.ValidateStoredSpecsOnStartup).
-- Failure fields ONLY.
--
-- next_run_at MUST NOT MOVE HERE. ReconcileOnStartup already owns the
-- never-catch-up policy at boot; a second statement advancing it would skip a
-- fire the operator was entitled to.
--
-- RECORD-ONLY: there is no clearing sibling for the sweep, on purpose. A spec
-- that validates at boot has not been proven to FIRE - the insert could still
-- fail - so clearing on a boot-time pass would assert something the sweep did
-- not observe. Clearing stays the exclusive job of a successful fire and of a
-- PATCH that changed the inputs.
UPDATE scheduled_jobs
SET last_error    = $2,
    last_error_at = NOW(),
    updated_at    = NOW()
WHERE id = $1;

-- name: ListEnabledScheduledJobs :many
-- EVERY enabled schedule, not just the overdue ones. The startup sweep's whole
-- point is the schedules NEITHER existing loop sees: ListEligibleScheduledJobs
-- and ListOverdueScheduledJobsForCatchup both require next_run_at to have
-- passed, so a healthy-looking @monthly schedule broken by a retroactive
-- validation change stays invisible for up to a month after the fix deploys.
-- Ordered by id purely for a deterministic sweep order in tests.
SELECT * FROM scheduled_jobs
 WHERE enabled
 ORDER BY id;
```

Then replace the `UpdateScheduledJob` block:

```sql
-- name: UpdateScheduledJob :one
-- handlePatchScheduledJob's write. It rewrites every mutable column, which is
-- where the "PUT handler" impression comes from; the HANDLER is a genuine PATCH
-- (patchScheduledJobRequest is all pointers, an omitted key means leave alone)
-- and builds every value in Go before calling this.
--
-- clear_failure IS A BOOLEAN ARGUMENT, NOT A READ-MODIFY-WRITE, and that is the
-- whole design. The handler reads the row through ownedScheduledJob WITHOUT a
-- lock. Reading last_error into Go and writing it back would let a PATCH carry a
-- stale error forward over a failure a tick recorded in between; expressing it
-- as a CASE means the row's own value is never round-tripped through the
-- application and there is no window.
--
-- The handler sets it from `req.JobSpec != nil || req.CronExpr != nil ||
-- req.Timezone != nil` - exactly the three inputs the three recorded failure
-- classes are about, all of which the handler has already validated before
-- reaching here. A PATCH of `name`, `overlap_policy` or `enabled` PRESERVES the
-- record: renaming a schedule must not erase the only signal that it is broken,
-- and on an @monthly schedule nothing would rewrite it for a month.
UPDATE scheduled_jobs
SET name           = $2,
    cron_expr      = $3,
    timezone       = $4,
    job_spec       = $5,
    overlap_policy = $6,
    enabled        = $7,
    next_run_at    = $8,
    last_error     = CASE WHEN sqlc.arg(clear_failure)::bool THEN NULL ELSE last_error END,
    last_error_at  = CASE WHEN sqlc.arg(clear_failure)::bool THEN NULL ELSE last_error_at END,
    updated_at     = NOW()
WHERE id = $1
RETURNING *;
```

- [ ] **Step 2: Generate**

```powershell
sqlc generate
buf generate
```

If `sqlc generate` errors on mixing positional `$N` and `sqlc.arg()` in `UpdateScheduledJob`, convert that ONE statement wholly to named arguments (`sqlc.arg(name)`, `sqlc.arg(cron_expr)`, ...). The generated params struct field names are derived from the argument names either way, so `store.UpdateScheduledJobParams`'s existing fields keep their spelling and the call site in `internal/api/scheduled_jobs.go` does not change except for the new field.

- [ ] **Step 3: THE CRLF PROCEDURE. Do not skip and do not shorten**

sqlc emits LF. On this CRLF repo it rewrites line endings across every generated file, and `git diff` and `git status` disagree about it by design.

```powershell
git status --short
git diff --ignore-all-space
```

`git status --short` will list many `internal/store/*.sql.go` files. `git diff --ignore-all-space` shows only the REAL content changes. **Never conclude "nothing to revert" from `git diff` alone.** For every file that `git status` lists but `git diff --ignore-all-space` shows no content change for:

```powershell
git checkout -- internal/store/<that-file>.sql.go
```

Keep exactly two regenerated files: `internal/store/models.go` and `internal/store/scheduled_jobs.sql.go`.

- [ ] **Step 4: VERIFY THE REGENERATED FILES ACTUALLY CONTAIN THE NEW COLUMNS**

There is a specific past failure on this repo where the CRLF revert silently discarded a regenerated `.sql.go`, leaving its doc comment contradicting its own source. Check, do not assume:

```powershell
Select-String -Path internal/store/models.go -Pattern "LastError"
Select-String -Path internal/store/scheduled_jobs.sql.go -Pattern "AdvanceScheduledJobSkipped|AdvanceScheduledJobAfterFailure|RecordScheduledJobFailure|ListEnabledScheduledJobs|ClearFailure"
git ls-files --eol internal/store/models.go internal/store/scheduled_jobs.sql.go internal/store/query/scheduled_jobs.sql
```

Expected: `models.go` shows `LastError` and `LastErrorAt` inside `type ScheduledJob struct`; `scheduled_jobs.sql.go` shows all five new symbols; all three paths read `i/lf`.

**Now record the ACTUAL Go types**, because the rest of this plan depends on them:

```powershell
Select-String -Path internal/store/models.go -Pattern "LastError" -Context 0,1
```

This plan expects `LastError *string` and `LastErrorAt pgtype.Timestamptz` (F2: `sqlc.yaml` sets `emit_pointers_for_null_types: true`, and the sibling nullable `TEXT` column `Worker.AgentTokenHash` is `*string`). **If sqlc emitted `pgtype.Text` instead, every `*string` in Tasks A4, A6, A8 and A10 becomes `pgtype.Text{String: ..., Valid: true}` / `.Valid` / `.String`.** Adapt and say so in the task report.

- [ ] **Step 5: Run the field-set tripwire and watch it go RED**

```powershell
go test ./internal/schedrunner/... -run TestScheduledJobRowStillCarriesNoFailureSurface -v -timeout 60s
```

Expected: FAIL with `store.ScheduledJob's field set moved: added [LastError LastErrorAt], removed []`, plus a hint naming `LastError` as reading like a failure surface, plus an instruction to invert the hazard test.

**This is the tripwire firing exactly as designed.** Its header says: update the list, do NOT add an exemption. Do neither of the two wrong things: do not add a name-based exemption, and do not delete the test.

- [ ] **Step 6: Update the tripwire's field list**

In `internal/schedrunner/scheduled_job_surface_test.go`, change `scheduledJobFields` to:

```go
// scheduledJobFields is store.ScheduledJob's COMPLETE field set as of the
// last_error change. Adding a row here is the deliberate act the test below
// exists to force.
var scheduledJobFields = []string{
	"ID", "Name", "OwnerID", "CronExpr", "Timezone", "JobSpec", "OverlapPolicy",
	"Enabled", "NextRunAt", "LastRunAt", "LastJobID", "LastError", "LastErrorAt",
	"CreatedAt", "UpdatedAt",
}
```

Then update the test's own header, which currently predicts this moment in the future tense. Replace the paragraph beginning "Every user-visible read of a schedule is built from this row" with:

```go
// TestScheduledJobRowStillCarriesNoFailureSurface is the field-set guard over
// store.ScheduledJob.
//
// IT WAS FILED AS A TRIPWIRE FOR
// docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md, whose whole
// claim was that no field on this row could carry a fire-time failure. That item
// shipped on 2026-08-28: last_error and last_error_at are in the list above, and
// the hazard test in internal/schedrunner/stored_spec_bounds_test.go was inverted
// in the same commit rather than exempted, exactly as this header instructed.
//
// THE NAME IS NOW WRONG AND IS KEPT ON PURPOSE. Renaming it would break the
// only link between this guard and the item that explains why an exact-set
// assertion over a generated struct is worth its maintenance cost. What the
// guard buys from here on is unchanged: every user-visible read of a schedule -
// GET /v1/scheduled-jobs/{id}, `relay schedules`, the SPA - is built from this
// row, so a column added or removed without a deliberate act lands here first.
//
// IT ASSERTS THE WHOLE SET, NOT A DENY-LIST, AND THAT IS THE WHOLE DESIGN. The
```

(keep the rest of the existing header from "first version of this check asserted only that no field name contained" onwards, unchanged.)

Finally, in the `t.Fatalf` message, replace the sentence beginning "If this is bug-2026-08-23-unfireable-schedule-is-invisible shipping its column" with:

```go
		"That item already shipped (2026-08-28), so a NEW addition here is a NEW "+
		"deliberate act: update this list in the same commit that adds the column, and "+
		"check whether internal/schedrunner/stored_spec_bounds_test.go needs to say "+
		"anything about it. The substring hint above is colour only: this test gates on "+
		"the SET, so a column called anything at all lands here.", added, removed, got, hint)
```

- [ ] **Step 7: Run the tripwire and the whole default lane**

```powershell
go test ./internal/schedrunner/... -v -timeout 60s
go test ./... -timeout 120s
```

Expected: PASS. `TestScheduledJobFieldSetDiff_GatesOnTheSetNotTheOrder` also stays green - note its `reordered` literal is a 13-element list that is deliberately independent of `scheduledJobFields`, so it does NOT need updating and must not be "harmonised" with it; it is a fixed discriminating input, not a mirror.

Hold on - that last claim needs checking, not asserting. `TestScheduledJobFieldSetDiff_GatesOnTheSetNotTheOrder` compares `reordered` against `scheduledJobFields`, so growing `scheduledJobFields` by two DOES break its first assertion (`ok` becomes false, with `removed [LastError LastErrorAt]`). **Add the two names to `reordered` too**, keeping it a genuine reorder of the new list:

```go
	reordered := []string{
		"Name", "ID", "OwnerID", "CronExpr", "Timezone", "JobSpec", "OverlapPolicy",
		"Enabled", "NextRunAt", "LastRunAt", "LastJobID", "LastError", "LastErrorAt",
		"CreatedAt", "UpdatedAt",
	}
```

Its other two legs use their own local literals and are unaffected. Re-run Step 7 after this edit.

- [ ] **Step 8: Compile the integration-tagged tree**

```powershell
go vet -tags integration ./...
```

Expected: no output. This catches any integration file that referenced the old `AdvanceScheduledJob` params shape.

- [ ] **Step 9: Commit**

```powershell
git ls-files --eol internal/store/query/scheduled_jobs.sql internal/store/models.go internal/store/scheduled_jobs.sql.go internal/schedrunner/scheduled_job_surface_test.go
git diff --stat
git add internal/store/query/scheduled_jobs.sql internal/store/models.go internal/store/scheduled_jobs.sql.go internal/schedrunner/scheduled_job_surface_test.go
git commit -m "feat(store): failure-recording statements for scheduled jobs, and the field-set guard update"
```

---

## Task A3: The classification and sanitization helpers

Pure functions, no database, **untagged** so they run in `go test ./...` and therefore in CI. This is one of the two default-lane siblings the spec's G5 decision promises.

**Files:**
- Create: `internal/schedrunner/failure.go`
- Create: `internal/schedrunner/failure_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/schedrunner/failure_test.go`. Note the package: `package schedrunner`, an INTERNAL test file, so the helpers can stay unexported. This directory already holds `package schedrunner_test` files; Go permits both.

```go
package schedrunner

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestRecordableFailure is the DEFAULT-LANE sibling for the failure
// classification, and it exists because the end-to-end proof
// (internal/api/scheduled_jobs_failure_visibility_integration_test.go) is
// integration-tagged and CI runs no tags. See that file's header for the full
// decision.
//
// THE TWO DISCRIMINATING CASES ARE FIRST AND SECOND, not last. A poisoned input
// placed at the end of a table cannot distinguish a function that examined it
// from one that returned before reaching it. Case 1 kills a mutant that always
// returns (`", false`); case 2 kills a mutant that always returns
// (`err.Error(), true`).
func TestRecordableFailure(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantOK   bool
		wantText string
	}{
		{
			name:     "a stored spec that fails jobspec.Validate IS recordable",
			err:      permanent(errors.New("task t: retries must be between 0 and 10")),
			wantOK:   true,
			wantText: "task t: retries must be between 0 and 10",
		},
		{
			name:   "a database fault is NOT recordable",
			err:    fmt.Errorf("count active jobs: %w", errors.New("conn closed by peer")),
			wantOK: false,
		},
		{
			name:   "a create-job insert fault is NOT recordable",
			err:    fmt.Errorf("create job: %w", errors.New("duplicate key value violates unique constraint \"jobs_pkey\"")),
			wantOK: false,
		},
		{
			name:   "nil is NOT recordable",
			err:    nil,
			wantOK: false,
		},
		{
			name:     "an undecodable job_spec IS recordable, and keeps fireOne's prefix",
			err:      permanent(fmt.Errorf("invalid job_spec: %w", errors.New("unexpected end of JSON input"))),
			wantOK:   true,
			wantText: "invalid job_spec: unexpected end of JSON input",
		},
		{
			name:     "an unparseable cron IS recordable",
			err:      permanent(fmt.Errorf("parse cron: %w", errors.New("expected 5 fields, found 3: \"0 2 *\""))),
			wantOK:   true,
			wantText: "parse cron: expected 5 fields, found 3: \"0 2 *\"",
		},
		{
			name:     "a permanent error wrapped further is still recordable, and the OUTER text is stored",
			err:      fmt.Errorf("outer: %w", permanent(errors.New("inner"))),
			wantOK:   true,
			wantText: "outer: inner",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := recordableFailure(tc.err)
			if ok != tc.wantOK {
				t.Fatalf("recordableFailure ok = %v, want %v (err = %v)", ok, tc.wantOK, tc.err)
			}
			if got != tc.wantText {
				t.Errorf("recordableFailure text = %q, want %q", got, tc.wantText)
			}
		})
	}
}

// TestSanitizeFailureText is the second default-lane sibling. Every property
// here is a real constraint, not a style preference:
//   - control characters: the text is operator-controlled (a task name flows
//     verbatim into "task %s: retries must be between 0 and %d") and four
//     clients render it, one of them a terminal.
//   - rune-boundary truncation: last_error is a TEXT column and Postgres rejects
//     invalid UTF-8 in TEXT, so a byte-slicing truncation is a genuine write
//     failure, not a cosmetic bug.
//   - never empty: scheduledJobResponse's `omitempty` makes "" indistinguishable
//     from absent, and absent must mean "no failure".
func TestSanitizeFailureText(t *testing.T) {
	t.Run("control characters become spaces, closing terminal escape injection", func(t *testing.T) {
		got := sanitizeFailureText("task \x1b[31mred\x1b[0m: bad\nvalue\there\x07")
		if strings.ContainsAny(got, "\x00\x07\x09\x0a\x0d\x1b\x7f") {
			t.Fatalf("sanitizeFailureText left a control character in %q", got)
		}
		if !strings.Contains(got, "red") || !strings.Contains(got, "value") {
			t.Errorf("sanitizeFailureText dropped legible content: %q", got)
		}
	})

	t.Run("a message that sanitizes to nothing becomes the fixed fallback", func(t *testing.T) {
		got := sanitizeFailureText("\x00\x01 \t\r\n")
		if got != failureTextUnavailable {
			t.Fatalf("sanitizeFailureText = %q, want the fallback %q", got, failureTextUnavailable)
		}
		if got == "" {
			t.Fatal("sanitizeFailureText must never return an empty string")
		}
	})

	t.Run("a long ASCII message is truncated with the marker", func(t *testing.T) {
		got := sanitizeFailureText(strings.Repeat("x", 4000))
		if len(got) > maxFailureTextBytes {
			t.Fatalf("sanitizeFailureText returned %d bytes, want <= %d", len(got), maxFailureTextBytes)
		}
		if !strings.HasSuffix(got, truncationMarker) {
			t.Errorf("a truncated message must end with %q, got %q", truncationMarker, got[max(0, len(got)-40):])
		}
	})

	t.Run("truncation cuts on a RUNE boundary, not a byte boundary", func(t *testing.T) {
		// Every rune is 2 bytes, so a byte-boundary cut has a 50% chance of
		// splitting one and producing invalid UTF-8 that Postgres refuses.
		got := sanitizeFailureText(strings.Repeat("é", 2000))
		if !utf8.ValidString(got) {
			t.Fatalf("sanitizeFailureText produced invalid UTF-8: %q", got)
		}
		if len(got) > maxFailureTextBytes {
			t.Fatalf("sanitizeFailureText returned %d bytes, want <= %d", len(got), maxFailureTextBytes)
		}
		if !strings.HasSuffix(got, truncationMarker) {
			t.Errorf("a truncated message must end with %q", truncationMarker)
		}
	})

	t.Run("a short clean message is returned unchanged", func(t *testing.T) {
		const in = "task t: retries must be between 0 and 10"
		if got := sanitizeFailureText(in); got != in {
			t.Errorf("sanitizeFailureText(%q) = %q, want it unchanged", in, got)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/schedrunner/... -run "TestRecordableFailure|TestSanitizeFailureText" -v -timeout 60s
```

Expected: FAIL to COMPILE. `undefined: permanent`, `undefined: recordableFailure`, `undefined: sanitizeFailureText`, `undefined: failureTextUnavailable`, `undefined: maxFailureTextBytes`, `undefined: truncationMarker`. That is the strongest available RED.

- [ ] **Step 3: Write the implementation**

Create `internal/schedrunner/failure.go`:

```go
package schedrunner

import (
	"errors"
	"strings"
)

// maxFailureTextBytes bounds what a fire failure writes into
// scheduled_jobs.last_error. 1024 is comfortably above every fixed-format
// message jobspec.Validate emits; the only way to exceed it is an
// operator-chosen task name of roughly a kilobyte.
//
// STORAGE IS BOUNDED BY CONSTRUCTION, not by this number alone: the write is an
// UPDATE of one already-locked row, not an append, so a failing schedule costs
// at most 1 KB, once, forever.
const maxFailureTextBytes = 1024

// truncationMarker is appended when the bound bites, so a reader can tell a
// truncated message from a short one. run-now returns the untruncated message.
const truncationMarker = "... (truncated)"

// failureTextUnavailable is stored when sanitization leaves nothing legible.
//
// STORING AN EMPTY STRING IS NOT AN OPTION. scheduledJobResponse carries
// `omitempty` on last_error, so "" is indistinguishable from absent on the wire,
// and absent must mean "no failure". A fixed fallback keeps the two apart.
const failureTextUnavailable = "fire failed; message unavailable"

// permanentFireError marks a fireOne failure as a fact about the schedule's OWN
// STORED DATA rather than about the infrastructure.
//
// THE PARTITION IS JUSTIFIED TWICE OVER, and both arguments point the same way.
// Semantically: a database blip is not a fact about the schedule, and an
// operator who learns to ignore a noisy field has lost the field. The three
// permanent classes share the property that an identical attempt later gets an
// identical answer - the same partition relayclient.ErrorIsTransient documents
// and the same one handleRunScheduledJobNow reasons about when it chooses 400
// over 500. By disclosure: the permanent messages are derived from data the
// schedule's owner supplied, while the transient ones are wrapped pgx errors,
// which can carry constraint names, column names, connection strings and host
// names. internal/api has a settled convention of not disclosing internals
// (writeError(w, 500, "db error"), never the pgx message), and storing a pgx
// error in a column four clients render would sidestep that convention through
// the back door.
//
// Error() delegates to the wrapped error and adds NO PREFIX OF ITS OWN, so the
// stored string is exactly what fireOne composed and nothing about this type
// leaks into an operator's view.
type permanentFireError struct{ err error }

func (e *permanentFireError) Error() string { return e.err.Error() }
func (e *permanentFireError) Unwrap() error { return e.err }

// permanent marks err as a recordable, operator-facing fire failure.
func permanent(err error) error { return &permanentFireError{err: err} }

// recordableFailure reports whether err should be written to
// scheduled_jobs.last_error, and returns the sanitized text to write.
//
// It is a pure function of the error, so it is testable without a database -
// which is the whole reason the classification is a named function rather than
// an inline branch in TickOnce.
//
// The text comes from err.Error(), the OUTERMOST message, not from the wrapped
// one: fireOne wraps as permanent(fmt.Errorf("invalid job_spec: %w", err)), so
// the outermost text is the one carrying the human-readable prefix.
func recordableFailure(err error) (string, bool) {
	var p *permanentFireError
	if err == nil || !errors.As(err, &p) {
		return "", false
	}
	return sanitizeFailureText(err.Error()), true
}

// sanitizeFailureText makes an error message safe to store and to render, at the
// SINGLE write site. One place, four readers (REST, SPA, CLI, Python SDK).
//
// Every rune below U+0020 and U+007F becomes a space. Newlines are not needed by
// any of the three recorded classes, and this closes ANSI escape injection into
// `relay schedules show`'s terminal output. The text is operator-controlled - a
// task name flows verbatim into "task %s: retries must be between 0 and %d" -
// and a new sink is the right moment to close the class rather than widen it.
//
// Invalid UTF-8 in the input is replaced with U+FFFD by the range-over-string
// plus WriteRune round trip, so the output is always valid UTF-8. That matters:
// last_error is a TEXT column and Postgres rejects invalid UTF-8 in TEXT.
func sanitizeFailureText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return failureTextUnavailable
	}
	if len(out) <= maxFailureTextBytes {
		return out
	}
	// TRUNCATE ON A RUNE BOUNDARY. Ranging a string yields the byte index of
	// each rune's FIRST byte, so the largest such index at or below the budget
	// is a safe cut point. A plain out[:limit] would split a multi-byte rune
	// roughly half the time and the UPDATE would fail, not merely look wrong.
	limit := maxFailureTextBytes - len(truncationMarker)
	cut := 0
	for i := range out {
		if i > limit {
			break
		}
		cut = i
	}
	return out[:cut] + truncationMarker
}
```

- [ ] **Step 4: Run test to verify it passes**

```powershell
go test ./internal/schedrunner/... -run "TestRecordableFailure|TestSanitizeFailureText" -v -timeout 60s
```

Expected: PASS, all sub-tests. If `max` is undefined in the test file, the toolchain predates Go 1.21's builtin - replace `got[max(0, len(got)-40):]` with `got`.

- [ ] **Step 5: Commit**

```powershell
git ls-files --eol internal/schedrunner/failure.go internal/schedrunner/failure_test.go
git add internal/schedrunner/failure.go internal/schedrunner/failure_test.go
git commit -m "feat(schedrunner): classify and sanitize permanent fire failures"
```

---

## Task A4: fireOne, TickOnce, and the hazard test inversion

**THIS IS THE HIGHEST-RISK TASK IN THE SLICE.** It rewrites a test that currently asserts a defect on purpose. Read Step 4 before you write any code.

**Files:**
- Modify: `internal/schedrunner/runner.go`
- Modify: `internal/schedrunner/stored_spec_bounds_test.go`

- [ ] **Step 1: Rewrite the hazard test FIRST, so the RED is the real one**

Open `internal/schedrunner/stored_spec_bounds_test.go`. Its header enumerates six assertions with a per-assertion instruction, and its `_DocumentedHazard` suffix is a promise that the suffix comes off when the sibling ships.

**Under the approved design ALL SIX EXISTING ASSERTIONS STAY TRUE, `Enabled` included. Not one of them inverts.** The gate decision (spec G1) rejected auto-disable, now and additively, so `assert.True(t, row.Enabled, ...)` is still correct - only its MESSAGE changes, from a statement of the hazard to a statement of the policy. If you find yourself flipping an assertion to make a test pass, stop: you have destroyed the control this file exists to be.

Replace the header comment and the function, keeping `makeOverBudgetSpecJSON` above it byte-identical:

```go
// TestTickOnce_AStoredSpecOverTheBoundRecordsItsFailureAndStaysEnabled is the
// end-to-end proof, at the runner level, for
// docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md.
//
// IT USED TO ASSERT A DEFECT ON PURPOSE, under the name
// TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard.
// Bounding `retries` in jobspec.Validate is retroactive on stored
// scheduled_jobs rows, because the spec is re-validated at fire time - so a
// schedule stored with retries: 50 stopped producing jobs the instant that
// deployed, and TickOnce logged one line and advanced next_run_at, leaving
// GET /v1/scheduled-jobs/{id}, `relay schedules` and the SPA all showing a
// healthy schedule whose last_run_at had quietly stopped moving. That was the
// DISCOVERY gap: run-now could already explain a schedule you SUSPECTED, and
// nothing anywhere pointed at which one to suspect.
//
// The gap is closed. last_error and last_error_at (migration 000022) are written
// by TickOnce's failure branch and cleared by a successful fire, and every one
// of the six assertions below is now a positive statement of correct behaviour.
//
// NONE OF THE SIX INVERTED, INCLUDING `Enabled`, and that is the deliberate
// outcome of a gate decision rather than an accident:
//   - the control, "a still-valid stored spec still fires", is what stops every
//     assertion below from passing vacuously.
//   - "the poisoned schedule fires no job": a spec that does not validate must
//     not produce a job.
//   - "next_run_at still advances": what stops a poisoned schedule hot-looping.
//   - "last_run_at stays unset": no run happened.
//   - "last_job_id stays unset": no job exists to point at.
//   - "the schedule is still Enabled": relay does NOT auto-disable a failing
//     schedule. See docs/superpowers/specs/2026-08-28-unfireable-schedule-visibility.md
//     section 9.1 for the five reasons, the first of which is that the failure
//     mode this whole item exists for is "a release retroactively invalidated
//     stored data", and answering that by turning the operator's schedule off
//     compounds a server-driven change to user data with a server-driven change
//     to user configuration.
//
// WHAT IS NEW is the last two legs: the failure IS recorded with the bound's own
// message, and a subsequent SUCCESSFUL fire CLEARS it. The healthy control is
// asserted to carry NEITHER field, which is what makes "recorded" a claim about
// this schedule rather than about the column being non-null everywhere.
//
// The row's field SET is still guarded separately and OUTSIDE this file, in
// internal/schedrunner/scheduled_job_surface_test.go, which is untagged so it
// runs in the plain `go test ./...` gate rather than only under Docker. Do not
// re-add a failure-shaped-name check to this file: a deny-list of spellings
// fails open on the next addition, which is how the first version of it was
// written.
func TestTickOnce_AStoredSpecOverTheBoundRecordsItsFailureAndStaysEnabled(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	owner := h.createUser(t, "legacy-spec@example.com")

	overdue := pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true}

	// The poisoned schedule sorts FIRST (older next_run_at), so it cannot pass by
	// being skipped after the healthy one already proved the tick works.
	poisoned, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "legacy-retries", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeOverBudgetSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-10 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	// CONTROL, in the same tick: a schedule whose spec still validates must still
	// fire. Without it, a TickOnce that had stopped firing anything at all would
	// pass every assertion below.
	healthy, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "healthy", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: overdue,
	})
	require.NoError(t, err)

	require.NoError(t, schedrunner.NewRunner(h.pool, h.q).TickOnce(ctx))

	healthyJobs, err := h.q.ListJobsByScheduledJob(ctx, healthy.ID)
	require.NoError(t, err)
	require.Len(t, healthyJobs, 1, "control: a still-valid stored spec must still fire")

	poisonedJobs, err := h.q.ListJobsByScheduledJob(ctx, poisoned.ID)
	require.NoError(t, err)
	assert.Empty(t, poisonedJobs,
		"CORRECT AND PERMANENT: a stored spec that no longer validates must not produce a job")

	row, err := h.q.GetScheduledJob(ctx, poisoned.ID)
	require.NoError(t, err)
	assert.True(t, row.NextRunAt.Time.After(time.Now()),
		"CORRECT AND PERMANENT: next_run_at must advance so the poisoned schedule does not hot-loop")
	assert.False(t, row.LastRunAt.Valid,
		"no run happened, so last_run_at must stay unset")
	assert.False(t, row.LastJobID.Valid,
		"no job was created, so last_job_id must stay unset")
	assert.True(t, row.Enabled,
		"RELAY DOES NOT AUTO-DISABLE A FAILING SCHEDULE. The failure is recorded, not acted on: "+
			"the schedule stays enabled and the operator decides. See the spec's section 9.1 - "+
			"auto-disable would answer a server-driven change to user data with a server-driven "+
			"change to user configuration, and it would provide no diagnosis, only a state change")

	// THE NEW HALF. The bound's own message, verbatim, is what run-now already
	// answers with, so an operator sees the same sentence from both surfaces.
	require.NotNil(t, row.LastError,
		"THE POINT OF THIS SLICE: a permanently un-fireable schedule must record WHY")
	assert.Contains(t, *row.LastError, "retries must be between 0 and 10",
		"the recorded text must be jobspec.Validate's own per-task message, not a generic string")
	assert.True(t, row.LastErrorAt.Valid,
		"last_error_at is what proves the scheduler is STILL evaluating the row, which is how an "+
			"operator tells 'failing every hour' from 'failed once in March'")

	// The healthy control carries NEITHER field. Without this the assertions
	// above would also pass if every schedule got an error stamped on it.
	healthyRow, err := h.q.GetScheduledJob(ctx, healthy.ID)
	require.NoError(t, err)
	assert.Nil(t, healthyRow.LastError,
		"a schedule that fired must carry no recorded failure")
	assert.False(t, healthyRow.LastErrorAt.Valid,
		"a schedule that fired must carry no recorded failure time")

	// CLEARING. Repair the stored spec and make the row eligible again WITHOUT
	// touching the failure columns, then tick: only a successful fire may clear
	// them, and this is the statement (AdvanceScheduledJob) that does it.
	_, err = h.pool.Exec(ctx,
		`UPDATE scheduled_jobs SET job_spec = $1, next_run_at = NOW() - INTERVAL '1 second' WHERE id = $2`,
		makeSpecJSON(t), poisoned.ID)
	require.NoError(t, err)

	require.NoError(t, schedrunner.NewRunner(h.pool, h.q).TickOnce(ctx))

	repaired, err := h.q.GetScheduledJob(ctx, poisoned.ID)
	require.NoError(t, err)
	assert.Nil(t, repaired.LastError,
		"a successful fire is the only event that proves the schedule works, and it must clear the record")
	assert.False(t, repaired.LastErrorAt.Valid,
		"the clear must take BOTH columns; one without the other is a half-cleared row nobody can read")
	assert.True(t, repaired.LastRunAt.Valid, "and the successful fire must stamp last_run_at")
}
```

- [ ] **Step 2: Run the test to verify it fails, and CHECK THE FAILURE IS THE RIGHT ONE**

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -run TestTickOnce_AStoredSpecOverTheBoundRecordsItsFailureAndStaysEnabled -v -timeout 300s
```

Expected: FAIL at `require.NotNil(t, row.LastError, ...)` - the column exists (Task A1) and `models.go` has the field (Task A2), but nothing writes it yet, so it is nil.

**It must NOT fail earlier.** If it fails on `Enabled`, on `next_run_at`, or on the healthy control, something in Tasks A1/A2 is wrong and this test is not the subject. Stop and report.

- [ ] **Step 3: Write the implementation**

In `internal/schedrunner/runner.go`, replace `fireOne`, `advance`, and add two helpers. The full replacement for the block from `// fireOne attempts...` to the end of `advanceNextRun`:

```go
// fireOne attempts to fire one schedule using q. On success it creates the job
// AND advances the schedule (last_run_at + last_job_id, and CLEARS any recorded
// failure) on q, then returns a nil error. On failure it returns the next_run_at
// the caller should advance to (without setting last_run_at) and a non-nil error.
// The caller is responsible for the savepoint and the failure-path advance on
// the outer tx.
//
// THE THREE PERMANENT FAILURES ARE WRAPPED IN permanent(). That marker is what
// TickOnce reads to decide whether the failure is a fact about the SCHEDULE
// (operator-supplied data, recorded) or about the INFRASTRUCTURE (a wrapped pgx
// error, logged only). See internal/schedrunner/failure.go for why the partition
// is worth making rather than recording everything uniformly.
func (r *Runner) fireOne(ctx context.Context, q *store.Queries, row store.ScheduledJob) (time.Time, error) {
	var spec jobspec.JobSpec
	if err := json.Unmarshal(row.JobSpec, &spec); err != nil {
		return time.Now().Add(time.Minute), permanent(fmt.Errorf("invalid job_spec: %w", err))
	}
	sched, err := ParseSchedule(row.CronExpr, row.Timezone)
	if err != nil {
		return time.Now().Add(time.Minute), permanent(fmt.Errorf("parse cron: %w", err))
	}
	nextFire := sched.Next(time.Now())

	// Validate the STORED spec explicitly, here, ahead of the overlap check and
	// ahead of CreateJobFromSpec.
	//
	// WHY IT IS HOISTED. CreateJobFromSpec validates too, but every error it
	// returns collapses into one "create job: %w", so a validation failure and an
	// insert failure are indistinguishable at the call site - and this slice has
	// to tell them apart, because one is a permanent fact about the schedule's
	// own data and the other is a transient infrastructure fault whose pgx text
	// must not be stored in a column four clients render.
	//
	// IT IS THE PRECEDENT, NOT A NEW IDEA. handleRunScheduledJobNow already does
	// exactly this, for exactly this reason, and its comment says so at length.
	// It respects the Single job-spec pipeline invariant: this is the same
	// jobspec.Validate, not a parallel check. And it is idempotent -
	// normalizeTaskCommands sees hasCommand == false, hasCommands == true on the
	// second call and falls through without error.
	//
	// NOTE THE ORDERING CONSEQUENCE, TAKEN DELIBERATELY: a poisoned spec now
	// reports its validation error even when a previous run is still active,
	// where before it would have skipped silently. That is the correct order. A
	// spec that cannot produce a job is a fact about the schedule regardless of
	// what else is running.
	if err := jobspec.Validate(&spec); err != nil {
		return nextFire, permanent(err)
	}

	if row.OverlapPolicy == "skip" {
		active, err := q.CountActiveJobsForSchedule(ctx, row.ID)
		if err != nil {
			return nextFire, fmt.Errorf("count active jobs: %w", err)
		}
		if active > 0 {
			log.Printf("schedrunner: skipping schedule %s (previous run still active)", row.Name)
			r.advanceSkipped(ctx, q, row, nextFire)
			return nextFire, nil
		}
	}

	job, _, err := jobcreate.CreateJobFromSpec(ctx, q, spec, row.OwnerID, row.ID)
	if err != nil {
		return nextFire, fmt.Errorf("create job: %w", err)
	}
	r.advance(ctx, q, row, job.ID, nextFire)
	return nextFire, nil
}

// advance is the SUCCESS path only. AdvanceScheduledJob clears last_error and
// last_error_at, which is correct here and only here: a completed
// CreateJobFromSpec is the only event that proves the stored spec both validates
// and inserts.
func (r *Runner) advance(ctx context.Context, q *store.Queries, row store.ScheduledJob, newJobID pgtype.UUID, next time.Time) {
	if err := q.AdvanceScheduledJob(ctx, store.AdvanceScheduledJobParams{
		ID:        row.ID,
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
		LastJobID: newJobID, // COALESCE in SQL preserves old value when this is invalid
	}); err != nil {
		log.Printf("schedrunner: AdvanceScheduledJob for %s: %v", row.Name, err)
	}
}

// advanceSkipped is the overlap_policy = 'skip' path. It stamps last_run_at, as
// this path always has, and it DOES NOT CLEAR the failure record: the skip
// branch returns before jobspec.Validate runs, so it is no evidence the spec is
// valid. Clearing here would make a poisoned schedule with a long-running
// predecessor flicker between "failing" and "healthy".
func (r *Runner) advanceSkipped(ctx context.Context, q *store.Queries, row store.ScheduledJob, next time.Time) {
	if err := q.AdvanceScheduledJobSkipped(ctx, store.AdvanceScheduledJobSkippedParams{
		ID:        row.ID,
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
	}); err != nil {
		log.Printf("schedrunner: AdvanceScheduledJobSkipped for %s: %v", row.Name, err)
	}
}

// advanceAfterFailure records a PERMANENT fire failure and advances next_run_at.
//
// IT MUST ONLY EVER BE CALLED WITH THE OUTER TRANSACTION'S q. See TickOnce.
func (r *Runner) advanceAfterFailure(ctx context.Context, q *store.Queries, row store.ScheduledJob, next time.Time, text string) {
	if err := q.AdvanceScheduledJobAfterFailure(ctx, store.AdvanceScheduledJobAfterFailureParams{
		ID:        row.ID,
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
		LastError: &text,
	}); err != nil {
		log.Printf("schedrunner: AdvanceScheduledJobAfterFailure for %s: %v", row.Name, err)
	}
}

func (r *Runner) advanceNextRun(ctx context.Context, q *store.Queries, row store.ScheduledJob, next time.Time) {
	if err := q.AdvanceScheduledJobNextRun(ctx, store.AdvanceScheduledJobNextRunParams{
		ID:        row.ID,
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
	}); err != nil {
		log.Printf("schedrunner: AdvanceScheduledJobNextRun for %s: %v", row.Name, err)
	}
}
```

Then, in `TickOnce`, replace the `fireErr != nil` block:

```go
		next, fireErr := r.fireOne(ctx, r.q.WithTx(sp), row)
		if fireErr != nil {
			// Roll back ONLY this schedule's writes; the outer tx stays usable.
			if rbErr := sp.Rollback(ctx); rbErr != nil {
				log.Printf("schedrunner: rollback savepoint for %s: %v", row.Name, rbErr)
			}
			log.Printf("schedrunner: fire schedule %s: %v", row.Name, fireErr)

			// EVERY WRITE BELOW GOES ON THE OUTER TRANSACTION'S q, NEVER ON THE
			// SAVEPOINT'S. The savepoint was just rolled back - that rollback is
			// the entire point of the nested-transaction design, since it is what
			// stops one poisoned schedule aborting the healthy rows' commits. A
			// failure write issued inside fireOne would be discarded by it, and
			// discarded SILENTLY: the row would simply never carry an error and a
			// test would fail with no clue why. Do not move the classification or
			// the write into fireOne to "keep it together".
			//
			// fireErr itself is a Go value and is unaffected by the rollback, so
			// classifying it here is safe and is the only place it can be done.
			//
			// The row is held FOR UPDATE by the outer transaction for the whole
			// tick, so this write cannot race a concurrent PATCH: the PATCH blocks
			// on the row lock and the ordering is serialized by the database.
			if text, ok := recordableFailure(fireErr); ok {
				r.advanceAfterFailure(ctx, q, row, next, text)
			} else {
				// A transient infrastructure fault. Advance next_run_at so the
				// schedule does not hot-loop, and PRESERVE any existing record: a
				// blip is not news about the schedule, and overwriting a real
				// failure with a database hiccup would lose the only signal.
				r.advanceNextRun(ctx, q, row, next)
			}
			continue
		}
```

Also update `TickOnce`'s doc comment, which currently ends "The failed schedule's next_run_at is still advanced on the outer tx (without setting last_run_at) so it does not hot-loop every tick." Append:

```go
// A PERMANENT failure - an undecodable job_spec, an unparseable cron, or a spec
// that no longer passes jobspec.Validate - additionally records its message in
// last_error/last_error_at on that same outer tx. A transient one (a database
// fault) records nothing and preserves whatever was already there.
```

- [ ] **Step 4: Run the test to verify it passes**

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -run TestTickOnce_AStoredSpecOverTheBoundRecordsItsFailureAndStaysEnabled -v -timeout 300s
```

Expected: PASS.

- [ ] **Step 5: Run the whole schedrunner integration package**

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -v -timeout 600s
```

Expected: PASS. In particular `runner_test.go`'s existing skip-path and success-path tests must still pass; the `advance` split changed which statement runs on the skip path, and a test asserting `last_run_at` moves on a skip is exercising `AdvanceScheduledJobSkipped` now.

- [ ] **Step 6: Commit**

```powershell
git ls-files --eol internal/schedrunner/runner.go internal/schedrunner/stored_spec_bounds_test.go
git add internal/schedrunner/runner.go internal/schedrunner/stored_spec_bounds_test.go
git commit -m "feat(schedrunner): record a permanent fire failure on the outer tx, and clear it on a successful fire"
```

---

## Task A5: The skip path must not clear a recorded failure

The spec's clearing table has a row for this and nothing in the tree pins it. It is also the only test of the new `AdvanceScheduledJobSkipped` statement in isolation.

**Files:**
- Create: `internal/schedrunner/skip_preserves_failure_integration_test.go`

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package schedrunner_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTickOnce_ASkippedFireDoesNotClearARecordedFailure pins the one row of the
// clearing table that has no other witness.
//
// WHY IT IS NOT OBVIOUS. Before this slice the skip branch and the success
// branch shared ONE statement (AdvanceScheduledJob), distinguished only by
// whether the last_job_id parameter carried a value. A "clear the failure
// whenever AdvanceScheduledJob runs" implementation would therefore have cleared
// on skip - and the skip branch returns BEFORE jobspec.Validate runs, so
// reaching it is no evidence at all that the stored spec is valid. A poisoned
// schedule with a long-running predecessor would flicker between "failing" and
// "healthy" on alternate ticks, which is worse than the invisibility this slice
// exists to fix.
//
// It also documents, in passing, that last_run_at moves on the skip path. That
// means last_run_at has always meant "the runner reached the end of a fire
// attempt", not "a job was produced". Pre-existing, filed separately, asserted
// here so the split into two statements did not silently change it.
func TestTickOnce_ASkippedFireDoesNotClearARecordedFailure(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	owner := h.createUser(t, "skip-preserve@example.com")

	sched, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "skip-preserve", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	// Tick once to produce a job. It stays pending (no agent runs here), which is
	// what CountActiveJobsForSchedule counts, so the NEXT tick takes the skip
	// branch.
	require.NoError(t, schedrunner.NewRunner(h.pool, h.q).TickOnce(ctx))
	jobs, err := h.q.ListJobsByScheduledJob(ctx, sched.ID)
	require.NoError(t, err)
	require.Len(t, jobs, 1, "precondition: the first tick must produce the job that makes the second one skip")

	// Plant a recorded failure and make the row eligible again. Planted rather
	// than produced, because producing one requires a poisoned spec and a
	// poisoned spec never reaches the skip branch - which is exactly why this
	// case has no other witness.
	planted := "task t: retries must be between 0 and 10"
	_, err = h.pool.Exec(ctx, `
		UPDATE scheduled_jobs
		   SET last_error = $1, last_error_at = NOW(), next_run_at = NOW() - INTERVAL '1 second'
		 WHERE id = $2`, planted, sched.ID)
	require.NoError(t, err)

	require.NoError(t, schedrunner.NewRunner(h.pool, h.q).TickOnce(ctx))

	after, err := h.q.ListJobsByScheduledJob(ctx, sched.ID)
	require.NoError(t, err)
	require.Len(t, after, 1, "the second tick must have SKIPPED, not fired, or this test proves nothing")

	row, err := h.q.GetScheduledJob(ctx, sched.ID)
	require.NoError(t, err)
	require.NotNil(t, row.LastError,
		"a SKIPPED fire must not clear the failure record: it returns before jobspec.Validate and is "+
			"therefore no evidence the stored spec is valid")
	assert.Equal(t, planted, *row.LastError)
	assert.True(t, row.LastErrorAt.Valid, "and it must not clear the timestamp either")
	assert.True(t, row.LastRunAt.Valid,
		"the skip path DOES stamp last_run_at, as it always has - recorded so the statement split "+
			"is visibly behaviour-preserving on this axis")
}
```

- [ ] **Step 2: Run it**

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -run TestTickOnce_ASkippedFireDoesNotClearARecordedFailure -v -timeout 300s
```

Expected: PASS immediately, because Task A4 already split the statement. **That is acceptable here and it is not a vacuous test**: run the mutation below to prove it is load-bearing.

- [ ] **Step 3: Prove the test is load-bearing**

Temporarily change `AdvanceScheduledJobSkipped` in `internal/store/query/scheduled_jobs.sql` to also set `last_error = NULL, last_error_at = NULL`, run `sqlc generate`, and re-run Step 2.

Expected: FAIL at `require.NotNil(t, row.LastError, ...)`.

Then revert: `git checkout -- internal/store/query/scheduled_jobs.sql internal/store/scheduled_jobs.sql.go` and re-run Step 2 to confirm PASS. Follow S3's line-ending check after the revert.

- [ ] **Step 4: Commit**

```powershell
git ls-files --eol internal/schedrunner/skip_preserves_failure_integration_test.go
git add internal/schedrunner/skip_preserves_failure_integration_test.go
git commit -m "test(schedrunner): a skipped fire must not clear a recorded failure"
```

---

## Task A6: The response fields, and the untagged wire-contract guard

**Files:**
- Modify: `internal/api/scheduled_jobs.go`
- Create: `internal/api/scheduled_jobs_response_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/scheduled_jobs_response_test.go`. **Package `api`, NOT `api_test`, and NO build tag.** The package already holds untagged files in package `api` (`cors_test.go`, `pagination_test.go`, `ratelimit_test.go`).

```go
package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file is one of the TWO DEFAULT-LANE SIBLINGS carrying what CI can check
// about docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md. The
// end-to-end proof is in
// internal/api/scheduled_jobs_failure_visibility_integration_test.go and is
// //go:build integration, and .github/workflows/go-ci.yml runs no tags, so it
// never runs in CI. See that file's header for the full decision. The other
// sibling is internal/schedrunner/failure_test.go.
//
// WHAT THIS PINS IN CI, with no database: the wire contract. Field names,
// absent-not-zero for a healthy schedule, present-with-values for a failing one,
// and the arity relationship between the row and the response.
//
// IT MUST NOT ASSERT THROUGH scheduledJobResponse. A fixture built from the type
// under test agrees with itself by construction on both the key names and the
// omitempty behaviour, and a deep-equal against such a fixture cannot see an
// absent optional field at all. Everything below goes through a
// map[string]any decoded from real marshalled JSON.

func mustResponseMap(t *testing.T, row store.ScheduledJob) map[string]any {
	t.Helper()
	b, err := json.Marshal(toScheduledJobResponse(row))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func cleanScheduledJobRow() store.ScheduledJob {
	now := pgtype.Timestamptz{Time: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC), Valid: true}
	return store.ScheduledJob{
		Name:          "nightly",
		CronExpr:      "@daily",
		Timezone:      "UTC",
		JobSpec:       []byte(`{"name":"n","tasks":[]}`),
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// TestToScheduledJobResponse_HealthyScheduleOmitsBothFailureKeys is the absent
// half, and it is the half that matters most: ABSENT MEANS HEALTHY. The keys
// must not be present as "" and must not be present as null, because every
// client - the SPA's `schedule.last_error ? ... : null`, the CLI's
// `*string != nil`, the Python SDK's `Optional[str] = None` - reads presence as
// the signal.
func TestToScheduledJobResponse_HealthyScheduleOmitsBothFailureKeys(t *testing.T) {
	m := mustResponseMap(t, cleanScheduledJobRow())

	for _, k := range []string{"last_error", "last_error_at"} {
		if _, present := m[k]; present {
			t.Errorf("a schedule with no recorded failure must omit %q entirely, got %#v", k, m[k])
		}
	}
	// Control: the keys that are ALWAYS present still are, so a mutation that
	// dropped every optional key would not look like a pass here.
	for _, k := range []string{"id", "name", "cron_expr", "enabled", "next_run_at"} {
		if _, present := m[k]; !present {
			t.Errorf("required key %q is missing from the response", k)
		}
	}
}

// TestToScheduledJobResponse_FailingScheduleCarriesBothFailureKeys is the
// present half, asserted against HAND-WRITTEN key names and values.
func TestToScheduledJobResponse_FailingScheduleCarriesBothFailureKeys(t *testing.T) {
	row := cleanScheduledJobRow()
	text := "task t: retries must be between 0 and 10"
	row.LastError = &text
	row.LastErrorAt = pgtype.Timestamptz{Time: time.Date(2026, 8, 28, 11, 59, 0, 0, time.UTC), Valid: true}

	m := mustResponseMap(t, row)

	got, ok := m["last_error"].(string)
	if !ok {
		t.Fatalf("last_error must be a JSON string, got %T (%#v)", m["last_error"], m["last_error"])
	}
	if got != text {
		t.Errorf("last_error = %q, want %q", got, text)
	}

	at, ok := m["last_error_at"].(string)
	if !ok {
		t.Fatalf("last_error_at must be a JSON string, got %T (%#v)", m["last_error_at"], m["last_error_at"])
	}
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		t.Errorf("last_error_at must be RFC3339, got %q: %v", at, err)
	}
}

// TestScheduledJobResponse_ArityMatchesTheRow is the ARITY CHECK for a
// hand-written field-by-field copy.
//
// toScheduledJobResponse maps store.ScheduledJob to scheduledJobResponse by
// hand, one assignment per field. A mapper like that silently drops anything
// added to its source: a new column would land in models.go, in the SQL, in the
// database, and simply never reach a client, with every existing test green.
//
// The relationship is response = row + 1, and the +1 is OwnerEmail, which no
// column supplies - fillOwnerEmails writes it separately from a users lookup.
// If this fails after adding a column, add the mapping AND its assertions; if it
// fails after adding a response-only field, update the constant below and say in
// the commit message what supplies it.
func TestScheduledJobResponse_ArityMatchesTheRow(t *testing.T) {
	const responseOnlyFields = 1 // OwnerEmail, supplied by fillOwnerEmails

	rowFields := reflect.TypeOf(store.ScheduledJob{}).NumField()
	respFields := reflect.TypeOf(scheduledJobResponse{}).NumField()

	if respFields != rowFields+responseOnlyFields {
		t.Fatalf("scheduledJobResponse has %d fields and store.ScheduledJob has %d; expected %d = %d + %d "+
			"response-only field(s). toScheduledJobResponse is a hand-written field-by-field copy, so a "+
			"column added to the row without a matching response field is silently invisible to every client.",
			respFields, rowFields, rowFields+responseOnlyFields, rowFields, responseOnlyFields)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/api/... -run "TestToScheduledJobResponse|TestScheduledJobResponse_Arity" -v -timeout 60s
```

Expected: FAIL to COMPILE - `row.LastError undefined` is not the failure (Task A2 added it to `models.go`), so the compile succeeds and instead:
- `TestToScheduledJobResponse_HealthyScheduleOmitsBothFailureKeys`: PASSES vacuously (the keys do not exist because the response type has no such fields). **This is expected and is why the next two tests exist.**
- `TestToScheduledJobResponse_FailingScheduleCarriesBothFailureKeys`: FAILS with `last_error must be a JSON string, got <nil>`.
- `TestScheduledJobResponse_ArityMatchesTheRow`: FAILS with `scheduledJobResponse has 14 fields and store.ScheduledJob has 15; expected 16`.

Record that the first test is vacuous at HEAD. It becomes non-vacuous the moment the fields exist, and it is the one that catches a future `omitempty` removal.

- [ ] **Step 3: Write the implementation**

In `internal/api/scheduled_jobs.go`, add two fields to `scheduledJobResponse` immediately after `LastJobID`:

```go
	LastJobID     string          `json:"last_job_id,omitempty"`
	// The last time the SCHEDULER failed to produce a job from this schedule,
	// and why. ABSENT MEANS HEALTHY - not "" and not null - which is what makes
	// `omitempty` on a string safe here: the write site
	// (internal/schedrunner/failure.go) never stores an empty string, precisely
	// so that an empty one cannot be confused with an absent one.
	//
	// THE TEXT IS OPERATOR-SUPPLIED. It is derived from the stored job_spec: a
	// task name the schedule's owner chose flows verbatim into jobspec.Validate's
	// "task %s: ..." message. An admin reading someone else's schedule is
	// therefore reading partly attacker-chosen prose. It is sanitized at the
	// write site (control characters stripped, truncated to 1 KB on a rune
	// boundary), and every renderer must treat it as untrusted text: a React text
	// child, never chrome, never dangerouslySetInnerHTML, and prefixed with its
	// provenance in the CLI.
	//
	// It is safe to serve because the read is owner-or-admin: ownedScheduledJob
	// 404s everyone else and both non-admin list arms are owner-scoped.
	LastError     string          `json:"last_error,omitempty"`
	LastErrorAt   *time.Time      `json:"last_error_at,omitempty"`
```

And two blocks in `toScheduledJobResponse`, after the `LastJobID` block:

```go
	if sj.LastError != nil {
		out.LastError = *sj.LastError
	}
	if sj.LastErrorAt.Valid {
		t := sj.LastErrorAt.Time
		out.LastErrorAt = &t
	}
```

(If Task A2 reported `pgtype.Text` instead of `*string`, use `if sj.LastError.Valid { out.LastError = sj.LastError.String }`.)

- [ ] **Step 4: Run test to verify it passes**

```powershell
go test ./internal/api/... -timeout 120s
```

Expected: PASS, all three new tests plus the package's existing untagged tests.

- [ ] **Step 5: Commit**

```powershell
git ls-files --eol internal/api/scheduled_jobs.go internal/api/scheduled_jobs_response_test.go
git add internal/api/scheduled_jobs.go internal/api/scheduled_jobs_response_test.go
git commit -m "feat(api): expose last_error and last_error_at on the scheduled-job response"
```

---

## Task A7: PATCH clears the record when, and only when, it changed an input

**Files:**
- Modify: `internal/api/scheduled_jobs.go` (`handlePatchScheduledJob`)

- [ ] **Step 1: Confirm the compile break that is already waiting**

```powershell
go build ./internal/api/...
```

Expected: FAIL. `store.UpdateScheduledJobParams` gained `ClearFailure` in Task A2 and the call site does not set it. Go does not require every struct field to be set in a composite literal with field names, so this may in fact COMPILE with `ClearFailure` defaulting to `false`.

**That is the hazard.** A zero value here means "never clear", which is silently wrong rather than loudly wrong. Do not rely on the compiler; the assertion in Task A8 is the actual guard.

- [ ] **Step 2: Write the implementation**

In `handlePatchScheduledJob`, replace the `s.q.UpdateScheduledJob(...)` call:

```go
	// CLEAR THE FAILURE RECORD IF, AND ONLY IF, THIS PATCH CHANGED ONE OF THE
	// THREE INPUTS THE THREE RECORDED FAILURE CLASSES ARE ABOUT.
	//
	// job_spec, cron_expr and timezone are exactly what an undecodable spec, an
	// unparseable cron and a failed jobspec.Validate are about, and all three have
	// already been validated above before reaching here - so any recorded failure
	// about the OLD values is stale by construction.
	//
	// A patch of name, overlap_policy or enabled PRESERVES the record. Renaming a
	// schedule must not erase the only signal that it is broken, and on an
	// @monthly schedule nothing would rewrite it for a month. Enabling and
	// disabling preserve it too: nothing about the spec changed, and a re-enabled
	// schedule that still carries its failure is showing the truth at the most
	// useful moment to see it.
	//
	// It is a BOOLEAN ARGUMENT rather than a read-modify-write. The row was read
	// through ownedScheduledJob without a lock, so reading last_error into Go and
	// writing it back would let this PATCH carry a stale error forward over a
	// failure a tick recorded in between. The SQL CASE means the row's own value
	// is never round-tripped through the application. (next_run_at in this same
	// handler DOES have that read-modify-write hazard; this slice does not fix it
	// and does not join it.)
	clearFailure := req.JobSpec != nil || req.CronExpr != nil || req.Timezone != nil

	updated, err := s.q.UpdateScheduledJob(r.Context(), store.UpdateScheduledJobParams{
		ID:            id,
		Name:          name,
		CronExpr:      cronExpr,
		Timezone:      tz,
		JobSpec:       jobSpecJSON,
		OverlapPolicy: overlap,
		Enabled:       enabled,
		NextRunAt:     nextRunAt,
		ClearFailure:  clearFailure,
	})
```

- [ ] **Step 3: Build and run the untagged lane**

```powershell
go build ./...
go test ./internal/api/... -timeout 120s
```

Expected: PASS. The clearing behaviour itself is asserted in Task A8; this step only proves the tree still builds.

- [ ] **Step 4: Commit**

```powershell
git ls-files --eol internal/api/scheduled_jobs.go
git add internal/api/scheduled_jobs.go
git commit -m "feat(api): PATCH clears a recorded schedule failure only when it changed job_spec, cron_expr or timezone"
```

---

## Task A8: The headline regression test

This is the backlog item's third acceptance criterion. **It will not run in CI.** Say so in the test's own comment, name the siblings that do, and do not describe it as a gate.

**Files:**
- Create: `internal/api/scheduled_jobs_failure_visibility_integration_test.go`

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/schedrunner"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScheduledJobFailure_IsVisibleOnGetAndOnList_AndClearedByPatch is the
// headline regression test for
// docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md.
//
// ################ THIS TEST DOES NOT RUN IN CI. ################
//
// .github/workflows/go-ci.yml runs `go test -race ./...` with NO TAGS and
// `make test-cli-integration` (which names ./internal/cli/... only). This file
// is //go:build integration in internal/api, so it is in neither job. It is not
// a gate and must not be described as one; it runs when a human runs
// `go test -tags integration -p 1 ./internal/api/...`, which needs Docker.
//
// That is a DECISION, recorded here in the form
// docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md's own
// acceptance criteria allow ("a written decision in the test's own comment
// saying why not"). Closing it properly means extending go-ci.yml's
// `services: postgres` job to internal/api, which requires moving
// newIntegrationDSN out of internal/cli's test files and converting
// internal/api's harness to honour RELAY_TEST_DATABASE_URL - a refactor of that
// item's own scope, on this one's critical path. That item is separately in Now
// and already names internal/api as its sharpest instance.
//
// WHAT DOES RUN IN CI, and what each covers:
//   - internal/api/scheduled_jobs_response_test.go (untagged): the wire
//     contract. Field names, absent-not-zero, present-with-values, and the arity
//     relationship between store.ScheduledJob and scheduledJobResponse. No
//     database.
//   - internal/schedrunner/failure_test.go (untagged): the classification (which
//     failure classes are recordable) and the sanitize-and-truncate helper. No
//     database.
//   - internal/cli/schedules_failure_integration_test.go: runs under
//     `make test-cli-integration`, which IS a CI job. It plants last_error with
//     SQL and reads it back through a REAL internal/api server over HTTP, so it
//     covers column -> handler -> JSON -> client in CI.
//
// WHAT NOTHING IN CI COVERS, and this test is the only witness: that the
// SCHEDRUNNER writes the record at all. That half needs a tick, and a tick needs
// Postgres.
//
// WHY internal/api RATHER THAN internal/schedrunner. The criterion requires
// asserting the error is visible VIA THE API, which crosses two packages.
// internal/api already imports internal/schedrunner (ValidateMinInterval,
// ParseSchedule) and schedrunner imports nothing from internal/api, so there is
// no cycle; an internal/api integration test has the httptest server, a real
// pool, and can construct schedrunner.NewRunner and call TickOnce.
// internal/schedrunner's harness cannot do the reverse - it has no server.
//
// ASSERTIONS GO THROUGH map[string]any, NOT scheduledJobResponse. An assertion
// routed through the response struct agrees with itself by construction on both
// the key names and the omitempty behaviour, and a deep-equal against a fixture
// cannot see an absent optional field at all.
func TestScheduledJobFailure_IsVisibleOnGetAndOnList_AndClearedByPatch(t *testing.T) {
	srv, q, pool := newFailureVisibilityServer(t)
	user := createTestUser(t, q, "Alice", "failvis-alice@example.com", false)
	token := createTestToken(t, q, user.ID)

	// STORED DIRECTLY, NOT THROUGH POST, and that is the whole point of the
	// defect: POST validates before storing, so the API cannot create the row
	// this test is about. The row is what a schedule accepted by an EARLIER
	// RELEASE looks like after a later release tightened jobspec.Validate.
	// retries: 50 was accepted by every relay release before the retry-bounds
	// change.
	overBound := []byte(`{"name":"legacy","tasks":[{"name":"t","command":["echo","hi"],"retries":50}]}`)
	healthySpec := []byte(`{"name":"fine","tasks":[{"name":"t","command":["echo","hi"]}]}`)

	poisoned, err := q.CreateScheduledJob(t.Context(), store.CreateScheduledJobParams{
		Name: "failvis-poisoned", OwnerID: user.ID, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: overBound, OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-10 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	// CONTROL, in the same tick. Without it, a TickOnce that had stopped firing
	// anything would satisfy every assertion below, and the "neither key is
	// present" assertions would be true of a response that never carries them.
	healthy, err := q.CreateScheduledJob(t.Context(), store.CreateScheduledJobParams{
		Name: "failvis-healthy", OwnerID: user.ID, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: healthySpec, OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	require.NoError(t, schedrunner.NewRunner(pool, q).TickOnce(t.Context()))

	// ---- DIAGNOSIS: the detail endpoint ----
	body := getScheduleBody(t, srv, token, uuidString(t, poisoned.ID))
	errText, ok := body["last_error"].(string)
	require.True(t, ok, "GET /v1/scheduled-jobs/{id} must carry last_error as a string, got %#v", body["last_error"])
	assert.Contains(t, errText, "retries must be between 0 and 10",
		"the recorded text must be jobspec.Validate's own per-task message, which is the same sentence run-now answers with")
	at, ok := body["last_error_at"].(string)
	require.True(t, ok, "GET must carry last_error_at, got %#v", body["last_error_at"])
	_, perr := time.Parse(time.RFC3339, at)
	assert.NoError(t, perr, "last_error_at must be RFC3339")

	healthyBody := getScheduleBody(t, srv, token, uuidString(t, healthy.ID))
	assertNoFailureKeys(t, healthyBody, "a schedule that fired")

	// ---- DISCOVERY: the LIST endpoint. This is the half the item exists for ----
	// run-now already closes diagnosis. What was missing is a way to see WHICH
	// schedule to suspect without suspecting anything first, and that is the list.
	items := listScheduleBodies(t, srv, token)
	poisonedItem := items[uuidString(t, poisoned.ID)]
	require.NotNil(t, poisonedItem, "the poisoned schedule must appear in GET /v1/scheduled-jobs")
	listErrText, ok := poisonedItem["last_error"].(string)
	require.True(t, ok,
		"THE POINT OF THIS SLICE: the LIST must carry last_error too, or an operator still has to "+
			"suspect a schedule before they can learn anything about it")
	assert.Contains(t, listErrText, "retries must be between 0 and 10")

	healthyItem := items[uuidString(t, healthy.ID)]
	require.NotNil(t, healthyItem)
	assertNoFailureKeys(t, healthyItem, "a healthy row in the list")

	// ---- PRESERVE: a PATCH that changes none of the three inputs ----
	patchSchedule(t, srv, token, uuidString(t, poisoned.ID), `{"name":"failvis-renamed"}`)
	afterRename := getScheduleBody(t, srv, token, uuidString(t, poisoned.ID))
	assert.Equal(t, "failvis-renamed", afterRename["name"], "precondition: the rename must have applied")
	_, stillThere := afterRename["last_error"]
	assert.True(t, stillThere,
		"renaming a schedule must NOT erase the only signal that it is broken. On an @monthly schedule "+
			"nothing would rewrite the record for a month")

	// ---- CLEAR: a PATCH that supplies a valid job_spec ----
	patchSchedule(t, srv, token, uuidString(t, poisoned.ID),
		`{"job_spec":{"name":"repaired","tasks":[{"name":"t","command":["echo","hi"]}]}}`)
	afterRepair := getScheduleBody(t, srv, token, uuidString(t, poisoned.ID))
	assertNoFailureKeys(t, afterRepair,
		"a PATCH that replaced job_spec (validated before storing, so any record about the old one is stale)")
}

// newFailureVisibilityServer mirrors newTestServer but also returns the pool,
// which schedrunner.NewRunner needs. newTestServer returns only (srv, q) and has
// many call sites; widening it is a bigger change than this one helper.
func newFailureVisibilityServer(t *testing.T) (*api.Server, *store.Queries, *pgxpool.Pool) {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	return srv, q, pool
}

func uuidString(t *testing.T, id pgtype.UUID) string {
	t.Helper()
	b, err := id.MarshalJSON()
	require.NoError(t, err)
	return strings.Trim(string(b), `"`)
}

func getScheduleBody(t *testing.T, srv *api.Server, token, id string) map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/scheduled-jobs/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
	return m
}

func listScheduleBodies(t *testing.T, srv *api.Server, token string) map[string]map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/scheduled-jobs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var env pageEnvelope[map[string]any]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	out := make(map[string]map[string]any, len(env.Items))
	for _, it := range env.Items {
		id, _ := it["id"].(string)
		out[id] = it
	}
	return out
}

func patchSchedule(t *testing.T, srv *api.Server, token, id, body string) {
	t.Helper()
	req := httptest.NewRequest("PATCH", "/v1/scheduled-jobs/"+id, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

// assertNoFailureKeys checks ABSENCE, not emptiness. `""` and `null` are both
// failures here: absent is the only spelling of "healthy" the four clients read.
func assertNoFailureKeys(t *testing.T, m map[string]any, subject string) {
	t.Helper()
	for _, k := range []string{"last_error", "last_error_at"} {
		v, present := m[k]
		assert.False(t, present, "%s must omit %q entirely, got %#v", subject, k, v)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

At HEAD (before Tasks A6/A7) this file does not compile, which is the strongest RED. Executed in this task order, A6 and A7 have already landed, so run it and check the failure is not a false green:

```powershell
go test -tags integration -p 1 ./internal/api/... -run TestScheduledJobFailure_IsVisibleOnGetAndOnList_AndClearedByPatch -v -timeout 600s
```

Expected: PASS. To prove it is not vacuous, run the mutation in Step 3 before committing.

- [ ] **Step 3: Prove three separate legs are load-bearing**

Run each mutation, confirm the named failure, then revert it before the next.

1. **Delete the `ClearFailure: clearFailure` line** from `handlePatchScheduledJob` (Task A7). Expected: FAIL at the final `assertNoFailureKeys` with `must omit "last_error"`. This is the only guard on that field; a zero value there compiles silently.
2. **Change `clearFailure` to `req.JobSpec != nil`** only, then send the rename patch. Expected: still PASS - the rename leg does not distinguish it. Now change it to a bare `true`. Expected: FAIL at the PRESERVE leg with `renaming a schedule must NOT erase...`.
3. **In `TickOnce`, change `if text, ok := recordableFailure(fireErr); ok` to `if false`.** Expected: FAIL at the very first `require.True(t, ok, "GET ... must carry last_error")`.

Revert all three:

```powershell
git checkout -- internal/api/scheduled_jobs.go internal/schedrunner/runner.go
go test -tags integration -p 1 ./internal/api/... -run TestScheduledJobFailure -v -timeout 600s
```

Expected: PASS.

- [ ] **Step 4: Commit**

```powershell
git ls-files --eol internal/api/scheduled_jobs_failure_visibility_integration_test.go
git add internal/api/scheduled_jobs_failure_visibility_integration_test.go
git commit -m "test(api): a stored spec that stops validating records its failure on GET and on the LIST"
```

---

## Task A9: README - correct the false sentence, and document the fields

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Find the exact text to correct**

```powershell
Select-String -Path README.md -Pattern "the only record is one line in the server log"
Select-String -Path README.md -Pattern "A stored spec is re-validated on every fire"
```

Both are in the `### Scheduled jobs` section. The paragraph beginning `**A stored spec is re-validated on every fire, so job-spec rules are retroactive.**` currently ends with a sentence this slice makes FALSE.

- [ ] **Step 2: Make the correction and add the subsection**

Replace the clause `and the only record is one line in the server log` with `and until 2026-08-28 the only record was one line in the server log`. Keep the rest of that paragraph, including its list of rules with the retroactive property, byte-identical.

Then insert a new paragraph immediately after that paragraph and before the `**To check a schedule you suspect, fire it by hand.**` paragraph:

```markdown
**A schedule that stops firing now says so.** `GET /v1/scheduled-jobs/{id}` and `GET /v1/scheduled-jobs` both carry two optional fields: `last_error`, the reason the scheduler last failed to produce a job, and `last_error_at`, when it failed. **Absent means healthy** - a schedule that has never failed carries neither key, not an empty string and not `null`. Because the list carries them too, an operator scanning `relay schedules` or the SPA's schedules table can see *which* schedule to suspect without suspecting anything first.

Only permanent failures are recorded: a `job_spec` that will not decode, a `cron_expr` that will not parse, and a spec that fails validation. A transient database fault is logged and not recorded, and it does not overwrite an existing record - a blip is not news about the schedule. `last_error` is **cleared by a successful fire**, and by a `PATCH` that supplies a new `job_spec`, `cron_expr` or `timezone`. It is **preserved** by a skipped fire (`overlap_policy: skip` with the previous run still active), by a `PATCH` that only renames the schedule or changes `overlap_policy` or `enabled`, and by disabling and re-enabling.

`last_error` is **derived from the stored `job_spec` and is operator-supplied**: the message embeds a task name the schedule's owner chose. It is sanitized (control characters removed) and truncated to 1 KB; `run-now` returns the untruncated message. Render it as text, never as markup.

**When a schedule reports a failure:**

1. `POST /v1/scheduled-jobs/{id}/run-now`, or `relay schedules run-now <id>`, or the SPA's **Run now**, to re-check interactively and get the current message in full and untruncated.
2. Repair the stored spec: `PATCH /v1/scheduled-jobs/{id}` with a new `job_spec`, or `relay schedules update <id> --spec FILE`.
3. Disable the schedule if it should not run: `relay schedules update <id> --disable`.

There is no fourth step. In particular there is no "relax the validator" step: the bounds are not environment-configurable by design, because an env-tunable bound would make retroactive schedule invalidation environment-dependent - the same stored spec would fire on one replica's configuration and silently stop on another's.
```

Note step 2 names `relay schedules update --spec FILE`, which Slice C adds. If Slice C is dropped, that line must be changed to `delete and recreate with relay schedules create --spec FILE` in the same commit that drops it.

Finally, in the same section, correct the sentence `` `relay schedules update` has no `--spec` flag, so from the CLI it is delete plus `relay schedules create --spec`. `` to:

```markdown
Replacing the stored spec is a `PATCH /v1/scheduled-jobs/{id}` with a new `job_spec`, or `relay schedules update <id> --spec FILE`.
```

- [ ] **Step 3: Verify the edit did not corrupt the file**

README has been reclassified as binary once on this repo by a programmatic edit that produced `\r\r\n`, turning a two-line change into 1845 insertions. Check, every time:

```powershell
git diff --stat README.md
git ls-files --eol README.md
```

Expected: a diffstat in the range of roughly 20 to 30 changed lines, and `i/lf w/crlf attr/`. If the insertion count is in the hundreds, `git checkout -- README.md` and redo the edit.

Also confirm no em dash or en dash was introduced:

```powershell
Select-String -Path README.md -Pattern "[–—]" | Select-Object -First 5
```

README already contains pre-existing em dashes elsewhere. Compare against `git stash`-free baseline: run `git diff README.md | Select-String -Pattern "^\+" | Select-String -Pattern "[–—]"` - this must return nothing.

- [ ] **Step 4: Commit**

```powershell
git add README.md
git commit -m "docs: README documents last_error, its clearing rules, and the remedy ladder"
```

---

# Slice A2 - the startup validation sweep

Depends on Slice A. Sequenced last so it can be dropped without unwinding anything else. If it is dropped, nothing in Slices A, B, C or D changes, and the only consequence is that a schedule broken by a retroactive validation change stays invisible until its next scheduled fire - up to a month for `@monthly`.

## Task A10: `ValidateStoredSpecsOnStartup`

**Files:**
- Create: `internal/schedrunner/startup_validation.go`
- Create: `internal/schedrunner/startup_validation_integration_test.go`
- Modify: `cmd/relay-server/main.go`

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package schedrunner_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateStoredSpecsOnStartup covers the hole this slice would otherwise
// leave aimed precisely at its own audience.
//
// ReconcileOnStartup advances next_run_at past missed triggers (never-catch-up),
// so without this sweep a schedule broken by a retroactive validation change
// records NOTHING until its next scheduled fire - up to a day for @daily, up to
// a month for @monthly. The population most likely to be broken right now is
// exactly the population of long-cadence schedules nobody has looked at
// recently.
//
// FOUR PROPERTIES, and the last two are the ones an implementation gets wrong:
//   - a broken enabled schedule that is NOT overdue is recorded. This is the
//     whole point: ListEligibleScheduledJobs and ListOverdueScheduledJobsForCatchup
//     both require next_run_at to have passed, so neither loop sees this row.
//   - a healthy schedule is left alone.
//   - next_run_at DOES NOT MOVE. ReconcileOnStartup owns never-catch-up; a
//     second statement advancing it would skip a fire the operator was entitled
//     to.
//   - the sweep NEVER CLEARS. A spec that validates at boot has not been proven
//     to FIRE - the insert could still fail - so clearing here would assert
//     something the sweep did not observe. Clearing stays the exclusive job of a
//     successful fire and of a PATCH.
func TestValidateStoredSpecsOnStartup(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	owner := h.createUser(t, "startup-sweep@example.com")

	// FAR IN THE FUTURE, deliberately: neither existing startup loop nor
	// TickOnce can reach this row, so a pass here is attributable to the sweep.
	future := pgtype.Timestamptz{Time: time.Now().Add(720 * time.Hour), Valid: true}

	broken, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "monthly-broken", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeOverBudgetSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: future,
	})
	require.NoError(t, err)

	fine, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "monthly-fine", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: future,
	})
	require.NoError(t, err)

	// A DISABLED broken schedule. It is out of scope: the sweep lists enabled
	// schedules only, because a disabled schedule is not trying to fire and a
	// failure record about it would be noise the operator did not ask for.
	disabled, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "disabled-broken", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeOverBudgetSpecJSON(t), OverlapPolicy: "skip", Enabled: false,
		NextRunAt: future,
	})
	require.NoError(t, err)

	// A row carrying a record whose spec is now FINE. The sweep must leave it.
	planted := "task t: retries must be between 0 and 10"
	_, err = h.pool.Exec(ctx,
		`UPDATE scheduled_jobs SET last_error = $1, last_error_at = NOW() WHERE id = $2`,
		planted, fine.ID)
	require.NoError(t, err)

	require.NoError(t, schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q))

	brokenRow, err := h.q.GetScheduledJob(ctx, broken.ID)
	require.NoError(t, err)
	require.NotNil(t, brokenRow.LastError,
		"a broken enabled schedule that is NOT overdue must be recorded at startup: no other loop sees it")
	assert.Contains(t, *brokenRow.LastError, "retries must be between 0 and 10")
	assert.True(t, brokenRow.LastErrorAt.Valid)
	assert.WithinDuration(t, future.Time, brokenRow.NextRunAt.Time, time.Second,
		"the sweep must NOT move next_run_at: ReconcileOnStartup owns never-catch-up")

	fineRow, err := h.q.GetScheduledJob(ctx, fine.ID)
	require.NoError(t, err)
	require.NotNil(t, fineRow.LastError,
		"THE SWEEP NEVER CLEARS. A spec that validates at boot has not been proven to FIRE, so clearing "+
			"here would assert something the sweep did not observe")
	assert.Equal(t, planted, *fineRow.LastError)

	disabledRow, err := h.q.GetScheduledJob(ctx, disabled.ID)
	require.NoError(t, err)
	assert.Nil(t, disabledRow.LastError,
		"a DISABLED schedule is not trying to fire, so the sweep must not record anything about it")
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -run TestValidateStoredSpecsOnStartup -v -timeout 300s
```

Expected: FAIL to COMPILE, `undefined: schedrunner.ValidateStoredSpecsOnStartup`.

- [ ] **Step 3: Write the implementation**

Create `internal/schedrunner/startup_validation.go`:

```go
package schedrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"relay/internal/jobspec"
	"relay/internal/store"
)

// ValidateStoredSpecsOnStartup re-validates every ENABLED schedule's stored spec
// once at boot and records a failure for each one that no longer passes. Call
// after migrations, beside ReconcileOnStartup, before Runner.Run().
//
// WHY IT EXISTS. jobspec.Validate's rules are retroactive over stored
// scheduled_jobs rows, and ReconcileOnStartup implements never-catch-up, so
// after the deploy that carries a new rule a schedule broken by it records
// nothing until its NEXT SCHEDULED FIRE. For @daily that is up to a day; for
// @monthly, up to a month. The population most likely to be broken right now is
// exactly the population of long-cadence schedules nobody has looked at
// recently, so without this the failure surface is empty precisely where it is
// needed most.
//
// IT IS RECORD-ONLY AND IT NEVER CLEARS. A spec that validates at boot has not
// been proven to fire - CreateJobFromSpec's insert could still fail - so
// clearing here would assert something this pass did not observe, and a stale
// failure record is the more conservative state to leave standing. Clearing
// stays the exclusive job of a successful fire (AdvanceScheduledJob) and of a
// PATCH that changed job_spec, cron_expr or timezone (UpdateScheduledJob).
//
// IT DOES NOT TOUCH next_run_at. RecordScheduledJobFailure writes the failure
// columns only. ReconcileOnStartup owns the never-catch-up advance and running
// two statements that both move next_run_at at boot would skip a fire.
//
// IT IS NOT THE SAME QUESTION AS ReconcileOnStartup'S OWN ParseSchedule FAILURE,
// which deliberately records nothing: when that one fails it logs and continues
// WITHOUT advancing next_run_at, so the row stays overdue and
// ListEligibleScheduledJobs picks it up on the very next tick at most 10 seconds
// later, where fireOne's own ParseSchedule failure records it. A write there
// would be redundant within ten seconds and would add a second code path to keep
// in step. This function is about schedules that are NOT overdue and are
// therefore seen by neither loop for a full cron period.
//
// Cost: one pass over N enabled schedules at boot, with no I/O per row beyond
// the read that lists them and one UPDATE per BROKEN row.
func ValidateStoredSpecsOnStartup(ctx context.Context, q *store.Queries) error {
	rows, err := q.ListEnabledScheduledJobs(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		text, ok := recordableFailure(validateStoredRow(row))
		if !ok {
			continue
		}
		if err := q.RecordScheduledJobFailure(ctx, store.RecordScheduledJobFailureParams{
			ID:        row.ID,
			LastError: &text,
		}); err != nil {
			log.Printf("schedrunner: startup validation record for %s: %v", row.Name, err)
		}
	}
	return nil
}

// validateStoredRow returns a permanent() error if the row's stored data cannot
// produce a job, or nil.
//
// IT DELIBERATELY DUPLICATES THE FIRST THREE CHECKS OF fireOne rather than
// sharing a helper with it. fireOne needs the PARSED spec and the PARSED
// schedule for the work that follows; this needs only the verdict. Extracting a
// helper that returned both would give fireOne a second return path to keep in
// step for no gain. What IS shared, and is the part that matters, is the
// permanent() vocabulary and recordableFailure's classification - so the two
// sites cannot disagree about what counts as recordable.
func validateStoredRow(row store.ScheduledJob) error {
	var spec jobspec.JobSpec
	if err := json.Unmarshal(row.JobSpec, &spec); err != nil {
		return permanent(fmt.Errorf("invalid job_spec: %w", err))
	}
	if _, err := ParseSchedule(row.CronExpr, row.Timezone); err != nil {
		return permanent(fmt.Errorf("parse cron: %w", err))
	}
	if err := jobspec.Validate(&spec); err != nil {
		return permanent(err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```powershell
go test -tags integration -p 1 ./internal/schedrunner/... -run TestValidateStoredSpecsOnStartup -v -timeout 300s
```

Expected: PASS.

- [ ] **Step 5: Wire it into main**

In `cmd/relay-server/main.go`, immediately after the `ReconcileOnStartup` block and before `go schedrunner.NewRunner(pool, q).Run(ctx)`:

```go
	// Re-validate every enabled schedule's stored spec once, so a schedule that a
	// retroactive validation change killed is visible within seconds of this
	// deploy rather than at its next scheduled fire - which for @monthly is up to
	// a month away. Record-only: it never clears. Failures inside are logged
	// per-row, so a broken schedule cannot stop the server booting.
	if err := schedrunner.ValidateStoredSpecsOnStartup(ctx, q); err != nil {
		log.Printf("warn: schedrunner startup validation: %v", err)
	}
```

- [ ] **Step 6: Build and run the default lane**

```powershell
go build ./...
go test ./... -timeout 120s
go vet -tags integration ./...
```

Expected: all clean.

- [ ] **Step 7: Commit**

```powershell
git ls-files --eol internal/schedrunner/startup_validation.go internal/schedrunner/startup_validation_integration_test.go cmd/relay-server/main.go
git add internal/schedrunner/startup_validation.go internal/schedrunner/startup_validation_integration_test.go cmd/relay-server/main.go
git commit -m "feat(schedrunner): record-only startup validation sweep for enabled schedules"
```
