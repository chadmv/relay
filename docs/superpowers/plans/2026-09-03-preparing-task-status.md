# Persist the `preparing` task status - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `preparing` a real value of `tasks.status`, so a task that is syncing its Perforce workspace is distinguishable from a wedged agent, without changing any bound the coordinator watchdog enforces.

**Architecture:** One atomic partition widening. Migration `000023` widens `tasks_status_check` and the partial index `idx_tasks_worker_active` together; `handleTaskStatus` gains one `case`; thirteen SQL predicates in `internal/store/query/tasks.sql` gain `'preparing'`; the Go mirror `taskStatusIsWritable`, the `internal/api` cancel-signal filter, the TypeScript union, the Python enum and eleven prose passages follow. No new statement writes `tasks.status`, and `started_at` stays stamped only at `running`.

**Tech Stack:** Go 1.26, sqlc, golang-migrate, pgx/v5, testify, testcontainers-go, React + TypeScript + vitest, pydantic v2 + pytest.

**Source spec:** `docs/superpowers/specs/2026-09-03-preparing-task-status.md`
**Backlog item this closes:** `docs/backlog/feature-2026-09-03-preparing-task-status.md`

---

## Slice independence declaration

**One lane. Do not split. Backend, frontend, SDK and docs all land in one branch and one PR.**

| Group | Files | Depends on |
|---|---|---|
| Backend | `internal/store/migrations/000023_*`, `internal/store/query/tasks.sql`, `internal/store/*.sql.go` (generated), `internal/worker/handler.go`, `internal/worker/taskstatus_fence_counters.go`, `internal/api/jobs.go`, all Go tests | nothing outside itself |
| Frontend | `web/src/jobs/api.ts`, `web/src/jobs/taskStatus.ts`, `web/src/jobs/taskStatus.test.ts`, `web/src/workers/WorkerTasksPanel.tsx` (comment only) | nothing |
| Python SDK | `python/src/relay/models.py`, `python/tests/unit/test_models.py` | nothing |
| Docs | `README.md`, `CLAUDE.md`, comments in the files above | nothing |

**Technically independent, deliberately not parallelised.** The frontend group needs no new backend endpoint: `GET /v1/jobs/{id}` and `GET /v1/workers/{id}/tasks` already return `status` as a verbatim string, and `web/src/jobs/api.ts`'s `TaskStatus` is a type-only union with no runtime validator, so widening it compiles against a server that has not changed. The file sets are disjoint. It could run in a second worktree.

**It should not.** The frontend work is three lines of production code (one union member, one `case` label in `taskStatusColor`) plus three test edits, and the Python work is one enum member plus one test. A second worktree, branch, PR and review round costs more than the change. More importantly, the spec's finding F4 is that this slice is a single atomic partition widening in which **every** omission is a live regression against today's behaviour: a reviewer has to read the partition table (spec section 5) against one diff to check that each site landed on the correct side. Two PRs make that check impossible to perform in either one.

**This is one slice, one PR, one session. It is not a multi-stage plan and needs no `/backlog phases` run.**

---

## Ordering hazards - read before sequencing anything

Three constraints. Violating any of them leaves an intermediate commit whose tree is wrong in a way no gate reports.

1. **The migration lands before the handler case.** `UpdateTaskStatus`'s status predicate tests the row's CURRENT status, not the value being written, so with the handler case in place and no migration the statement happily attempts `SET status = 'preparing'` and Postgres rejects it with a `check_violation`. That is a real database error, not a fence rejection: `handleTaskStatus` takes the `else` branch and logs `worker: handleTaskStatus UpdateTaskStatus <id> -> preparing: ...` under the connection's budget, on every source-bearing task. Migration first.

2. **`UpdateTaskStatus`'s allow-list is widened before the handler case, not after.** The allow-list is not what lets a `dispatched` row become `preparing` - it is what lets a `preparing` row become `running`. With the handler case shipped and the allow-list still `('pending', 'dispatched', 'running')`, every source-bearing task reaches `preparing` and is then **permanently unwritable**: its own `RUNNING`, `DONE` and `FAILED` reports all match zero rows, `classifyStatusFenceRejection('preparing', 'running')` returns `fenceReasonConflicting` (the row was not writable at T0 and the reported status differs), and `task_status_fence.counts.conflicting_total` - the key README calls the actionable number - climbs once per report, forever. Allow-list first.

3. **The index is widened in the same migration as the constraint, and therefore strictly before the eleven statements.** Postgres uses a partial index only where the query predicate implies the index predicate. `status IN ('dispatched', 'preparing', 'running')` does not imply `status IN ('dispatched', 'running')`, so widening the statements while the index stays narrow makes `idx_tasks_worker_active` unusable for **all** of them, including `CountActiveTasksByAllWorkers`, which the dispatcher runs every cycle over the whole `tasks` table. Nothing in the tree can see this today, which is why Task 2 adds a guard for it. Index-in-the-migration first is the safe order and it is what this plan does; the reverse (a follow-up migration for the index) ships a known silent whole-table scan.

4. **The migration and the `tasksStatusVocabulary` rewrite are one commit.** Writing `000023_task_preparing_status.up.sql` alone makes `tasksStatusVocabulary`'s `require.Len(t, from, 1)` fail, which fails `TestTaskStatusWritableSetMatchesTheSQLAllowList` in the **default** lane - `make test` goes red at that commit. Observe the RED (Task 3), fix the helper (Task 4), then commit Tasks 2 through 6 together.

---

## Lanes: the two guards do not live in the same one

The spec's F5 is confirmed. Every task below names its lane and its command.

| Guard | File | Build tag | Lane |
|---|---|---|---|
| `TestTaskStatusWritableSetMatchesTheSQLAllowList` | `internal/worker/taskstatus_fence_counters_test.go` | none | `make test`, **no Docker** |
| `tasksStatusVocabulary` (helper it calls) | same file | none | same |
| `TestTasksStatusVocabularyIsExactly` | `internal/store/tasks_status_vocabulary_lockstep_test.go` | `//go:build integration` | **Docker required** |
| `TestStatusVocabularyConstraints_*` | `internal/store/status_vocabulary_constraints_test.go` | `//go:build integration` | Docker |
| `TestHotPathIndexes`, `TestHotPathIndexesDownUp` | `internal/store/hot_path_indexes_integration_test.go` | `//go:build integration` | Docker |
| `TestListActiveTasksForWorkerPage_*` | `internal/store/list_active_tasks_for_worker_integration_test.go` | `//go:build integration` | Docker |
| `TestListOverdueAssignedTasks_*` | `internal/store/list_overdue_assigned_tasks_integration_test.go` | `//go:build integration` | Docker |
| `TestWatchdog_*` (end to end) | `internal/worker/handler_watchdog_e2e_integration_test.go` | `//go:build integration` | Docker |
| `TestListWorkerTasks_*` | `internal/api/workers_tasks_integration_test.go` | `//go:build integration` | Docker |
| `TestCancelJob_*` | `internal/api/jobs_cancel_test.go` | `//go:build integration` | Docker |

`idea-2026-08-23-integration-only-guards-ci-never-runs` is open: an integration-only RED is a RED nobody sees in CI. Where a property can be pinned in the default lane it is (Tasks 4 and 7); where it cannot, the task says so.

---

## Test bodies below are SKETCHES

Every Go, TypeScript and Python test body in this plan is a guess written from reading the fixtures, not a verified compile. **Check each one against the real helper signatures before running it** - in particular `newAssignedFixture`/`claimedAt`/`get` (`internal/store/tasks_assigned_at_integration_test.go:66-108`), `newOverdueFixture`/`dispatched`/`running`/`bothArms`/`list` (`internal/store/list_overdue_assigned_tasks_integration_test.go:26-127`), `newTestStore` and `seedTaskAndTwoWorkers` (`internal/worker`), `newCancelTestServer`/`seedRunningTask` (`internal/api/jobs_cancel_test.go:46-142`), and `seedWorkerTask` (`internal/api/workers_tasks_integration_test.go`). "It matches the plan" is not verification.

---

## Task 1: Baseline, measured both ways

**Files:** none.

- [ ] **Step 1: Record the default lane green at HEAD**

```bash
cd /d/dev/relay/.claude/worktrees/pr-merging-session-65b658
go test ./... -count=1 -timeout 300s 2>&1 | tail -40
```

Expected: `ok` or `no test files` for every package, no `FAIL`. Write the output down. Nothing later may be diagnosed against an unmeasured baseline.

- [ ] **Step 2: Record the four integration packages green at HEAD**

Docker Desktop must be running.

```bash
go test -tags integration -p 1 ./internal/store/... -count=1 -timeout 1200s 2>&1 | tail -30
go test -tags integration -p 1 ./internal/worker/... -count=1 -timeout 1200s 2>&1 | tail -30
go test -tags integration -p 1 ./internal/scheduler/... -count=1 -timeout 900s 2>&1 | tail -30
go test -tags integration -p 1 ./internal/api/... -count=1 -timeout 1800s 2>&1 | tail -30
```

`internal/api`'s integration package runs about 9.5 minutes; a 600s timeout reports FAIL with no `--- FAIL` line under it. Use 1800s.

- [ ] **Step 3: Record the web and python lanes green at HEAD**

```bash
cd web && npx vitest run --reporter=dot 2>&1 | tail -20 ; cd ..
make python-test 2>&1 | tail -20
```

No commit.

---

## Task 2: The index-predicate guard goes RED at HEAD

Nothing in the tree can see the partial index's predicate. `TestHotPathIndexes` and `TestHotPathIndexesDownUp` assert index **names** only (verified: `internal/store/hot_path_indexes_integration_test.go:21-27, 57-97`), so both stay green across a predicate change. That is exactly why this guard is needed.

**Files:**
- Create: `internal/store/active_task_index_predicate_integration_test.go`

- [ ] **Step 1: Write the failing test**

Sketch - verify `newTestPool` is in scope (it is, `internal/store/testhelper_test.go`, package `store_test`):

```go
//go:build integration

package store_test

import (
	"context"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// indexPredicateLiteralRe pulls the single-quoted literals out of a
// pg_get_expr rendering of a partial index's WHERE clause. Postgres renders an
// IN list as `((status)::text = ANY ((ARRAY['dispatched'::character varying,
// ...])::text[]))`, so matching the quoted values is stable against the casts
// and the ANY/ARRAY spelling in a way that string-comparing the whole
// expression is not.
var indexPredicateLiteralRe = regexp.MustCompile(`'([^']*)'`)

// TestActiveTaskIndexPredicateMatchesTheAssignmentPartition is the only thing in
// the tree that reads idx_tasks_worker_active's WHERE clause. TestHotPathIndexes
// and TestHotPathIndexesDownUp assert index NAMES, so a predicate that has
// drifted from the statements leaves both of them green.
//
// The consequence of drift is not a wrong answer, it is a silent plan change:
// Postgres uses a partial index only where the query predicate IMPLIES the index
// predicate, so a statement admitting a status the index does not is served by a
// sequential scan over every task row the system has ever created. The worst of
// them is CountActiveTasksByAllWorkers, which the dispatcher runs every cycle.
//
// No EXPLAIN assertion, deliberately: plan choice depends on statistics and
// table size, so a green EXPLAIN on a small test table proves nothing and a red
// one is a flake. The predicate is the property; the plan is its consequence.
func TestActiveTaskIndexPredicateMatchesTheAssignmentPartition(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var pred string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT pg_get_expr(i.indpred, i.indrelid)
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = 'idx_tasks_worker_active'`,
	).Scan(&pred), "idx_tasks_worker_active must exist and must still be a PARTIAL index; "+
		"a NULL indpred means somebody made it a full index and every statement's plan changed")

	var got []string
	for _, m := range indexPredicateLiteralRe.FindAllStringSubmatch(pred, -1) {
		got = append(got, m[1])
	}
	sort.Strings(got)

	require.Equal(t, []string{"dispatched", "preparing", "running"}, got,
		"idx_tasks_worker_active's predicate is %q. It must name exactly the statuses the "+
			"assignment-partition statements admit - GetActiveTasksForWorker, ListGraceCandidates, "+
			"RequeueTaskByID, RequeueWorkerTasks, RequeueWorkerTasksIfEpoch, "+
			"CountActiveTasksByAllWorkers, ListOverdueAssignedTasks, ListActiveTasksForWorkerPage "+
			"and CountActiveTasksForWorker. A status in the statements and not here does not make "+
			"any of them WRONG; it makes all of them scan the whole table, with no error and no "+
			"log line", pred)
}
```

- [ ] **Step 2: Run it and record the RED**

```bash
go test -tags integration -p 1 ./internal/store/ -run TestActiveTaskIndexPredicateMatchesTheAssignmentPartition -count=1 -v -timeout 600s
```

Expected: `--- FAIL`, with `Error: Not equal: expected: []string{"dispatched", "preparing", "running"}  actual: []string{"dispatched", "running"}`.

Do not commit yet. Task 3 turns this green.

---

## Task 3: Migration `000023_task_preparing_status`, with a deliberately incomplete down

**Files:**
- Create: `internal/store/migrations/000023_task_preparing_status.up.sql`
- Create: `internal/store/migrations/000023_task_preparing_status.down.sql`

Next free version confirmed: the directory runs `000001` to `000022_scheduled_jobs_last_error`.

- [ ] **Step 1: Write the up migration**

`internal/store/migrations/000023_task_preparing_status.up.sql`:

```sql
-- Add `preparing` to the tasks.status vocabulary and to the partial index that
-- covers the currently-assigned partition. BOTH objects move here, in one
-- migration, deliberately: Postgres uses a partial index only where the query
-- predicate implies the index predicate, so widening the statements without
-- widening this index makes it unusable for every one of them - including
-- CountActiveTasksByAllWorkers, which the dispatcher runs every cycle over the
-- whole tasks table.
--
-- Plain CREATE INDEX (no CONCURRENTLY): golang-migrate wraps each migration in a
-- transaction and CONCURRENTLY cannot run in one, exactly as 000018 notes. The
-- build takes a lock that blocks writes to tasks, and migrations run at server
-- startup - but the index is partial over the currently-assigned rows only, so
-- the build is bounded by live work rather than by history.
--
-- THE THREE-LINE ALTER SHAPE IS READ BY A TEST. tasksStatusVocabulary
-- (internal/worker/taskstatus_fence_counters_test.go) matches
-- `ADD CONSTRAINT tasks_status_check\s+CHECK \(status IN \(([^)]*)\)` across
-- every up-migration. Anything other than whitespace between the constraint name
-- and CHECK - a trailing `--` comment on that line is the realistic case - makes
-- the parse miss this file, and the guard then reports 000019's SIX-value
-- vocabulary while passing. That is a fail-open.
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','preparing','running','done','failed','timed_out'));

DROP INDEX IF EXISTS idx_tasks_worker_active;

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'preparing', 'running');
```

- [ ] **Step 2: Write the down migration WITHOUT its demoting UPDATE**

This is deliberate. Task 6 writes the test that proves the UPDATE is load-bearing, and it can only be RED if the UPDATE is absent first.

`internal/store/migrations/000023_task_preparing_status.down.sql`:

```sql
-- Reverse 000023. The ORDER of the statements below is the correctness argument;
-- see the note added in the next commit.
DROP INDEX IF EXISTS idx_tasks_worker_active;

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'running');

ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','running','done','failed','timed_out'));
```

Note for the reader: this `.down.sql` re-adds a constraint by the same name and with the same three-line shape, and that does **not** re-break the parse. `tasksStatusVocabulary` filters on `strings.HasSuffix(e.Name(), ".up.sql")` (verified at `internal/worker/taskstatus_fence_counters_test.go:620-622`).

- [ ] **Step 3: Run the index guard - it must now be GREEN**

```bash
go test -tags integration -p 1 ./internal/store/ -run TestActiveTaskIndexPredicateMatchesTheAssignmentPartition -count=1 -v -timeout 600s
```

Expected: `--- PASS`.

- [ ] **Step 4: Run the default-lane vocabulary guard and record the RED**

```bash
go test ./internal/worker/ -run TestTaskStatusWritableSetMatchesTheSQLAllowList -count=1 -v -timeout 60s
```

Expected: `--- FAIL`, message:

```
expected exactly one up-migration to ADD CONSTRAINT tasks_status_check, found 2
([000019_status_vocabulary_checks.up.sql 000023_task_preparing_status.up.sql])
```

**Record the two filenames.** This message is the only proof anywhere that the new migration's formatting matches the regex, and nothing else checks it. **If it says `found 1`, STOP**: the regex did not match `000023_task_preparing_status.up.sql`, the guard is silently reading 000019's six-value vocabulary, and the migration's `ALTER ... ADD CONSTRAINT ... CHECK (status IN (` shape must be fixed before proceeding.

- [ ] **Step 5: Run the integration lockstep guard and record its RED**

```bash
go test -tags integration -p 1 ./internal/store/ -run TestTasksStatusVocabularyIsExactly -count=1 -v -timeout 600s
```

Expected: `--- FAIL` with `expected: []string{"dispatched","done","failed","pending","running","timed_out"}` against an actual that also contains `"preparing"`.

No commit yet.

---

## Task 4: Rewrite `tasksStatusVocabulary` to read the LAST definition

**Files:**
- Modify: `internal/worker/taskstatus_fence_counters_test.go:594-640` (the `tasksStatusVocabulary` helper and its doc comment)

- [ ] **Step 1: Replace the helper**

The `require.Len(t, from, 1)` rung is what stops the parse reading a stale vocabulary. Removing it without a replacement re-creates exactly the fail-open it closes, so the replacement asserts that the file chosen is the lexically greatest match (decision D9).

```go
// tasksStatusVocabulary reads the literal set of tasks_status_check out of the
// LAST up-migration that adds it - the same constraint
// internal/store/tasks_status_vocabulary_lockstep_test.go reads back off a live
// Postgres. This lane cannot reach a database (it is the no-tag CI lane), so it
// reads the source the constraint is built from instead.
//
// IT SCANS EVERY up-MIGRATION AND TAKES THE LEXICALLY GREATEST MATCH rather than
// opening one migration by name, and it ASSERTS that it did. A hard-coded path
// goes stale silently the day a later migration drops and re-adds the constraint
// with a wider set. So did the previous shape of this helper, differently: it
// required exactly ONE definition, which was correct until 000023 legitimately
// added a second, and it would have been correct forever if nobody ever widened
// the vocabulary again. The require.Len is replaced, not deleted - taking the
// FIRST match, or taking any match without checking it is the greatest, reads
// 000019's six values forever while passing, which is the same fail-open in a
// new shape.
//
// `--` COMMENT LINES ARE STRIPPED BEFORE MATCHING. A migration's own doc block
// may legitimately quote a prior vocabulary, and a quoted definition in prose is
// exactly the thing this parse must not mistake for a real one. The
// down-migrations are excluded because 000019's and 000023's both drop or re-add
// the constraint and would otherwise be extra hits.
func tasksStatusVocabulary(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("..", "store", "migrations")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	// `\s` spans newlines, which matters: the ALTER TABLE is written across
	// three lines with the constraint name on its own.
	def := regexp.MustCompile(`ADD CONSTRAINT tasks_status_check\s+CHECK \(status IN \(([^)]*)\)`)
	quoted := regexp.MustCompile(`'([a-z_]+)'`)

	type hit struct {
		file     string
		statuses []string
	}
	var hits []hit
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)

		// Executable text only. See the comment-strip paragraph above.
		var stripped []string
		for _, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			stripped = append(stripped, line)
		}
		body := strings.Join(stripped, "\n")

		for _, m := range def.FindAllStringSubmatch(body, -1) {
			var out []string
			for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
				out = append(out, q[1])
			}
			hits = append(hits, hit{file: e.Name(), statuses: out})
		}
	}

	require.NotEmpty(t, hits,
		"no up-migration ADDs CONSTRAINT tasks_status_check. Either the constraint moved, or this "+
			"parse no longer matches the migration's formatting - which is a FAIL-OPEN, because a "+
			"parse that silently returns nothing makes every comparison it feeds vacuous. Re-derive it.")

	last := hits[len(hits)-1]
	for _, h := range hits {
		require.LessOrEqual(t, h.file, last.file,
			"this parse takes the LAST match in os.ReadDir order and got %s, but %s sorts after it. "+
				"os.ReadDir returns entries sorted by filename and migration filenames are "+
				"zero-padded, so the last match is the newest definition. If that stopped being "+
				"true, this helper is reading a STALE vocabulary and the universe it feeds no "+
				"longer contains every status a row can hold.", last.file, h.file)
	}
	require.NotEmpty(t, last.statuses,
		"parsed no statuses out of tasks_status_check in %s; the parse is broken, not the code", last.file)
	return last.statuses
}
```

- [ ] **Step 2: Run the default lane - it must now be GREEN**

```bash
go test ./internal/worker/ -run TestTaskStatusWritableSetMatchesTheSQLAllowList -count=1 -v -timeout 60s
```

Expected: `--- PASS`. The helper now returns the seven-value vocabulary; `taskStatusUniverse` gains `preparing` as a candidate, and the two-directional comparison still holds because `taskStatusIsWritable` does not admit it and `UpdateTaskStatus`'s allow-list does not either. Both sides say "not writable", which is the correct agreement at this commit.

- [ ] **Step 3: Run the whole default lane**

```bash
make test
```

Expected: no `FAIL`.

---

## Task 5: Update the integration lockstep guard's expected set and its census

**Files:**
- Modify: `internal/store/tasks_status_vocabulary_lockstep_test.go:229` (the `want` slice)
- Modify: `internal/store/tasks_status_vocabulary_lockstep_test.go:21-213` (the doc comment)
- Modify: `internal/store/tasks_status_vocabulary_lockstep_test.go:230-256` (the failure message)

- [ ] **Step 1: Widen `want`**

```go
	want := []string{"dispatched", "done", "failed", "pending", "preparing", "running", "timed_out"}
```

(`sort.Strings` runs on `got`; `preparing` sorts between `pending` and `running`.)

- [ ] **Step 2: Add `CancelJobTasks` to the census**

This is the spec's finding 7.2, verified: the doc comment's list and the failure message both present themselves as complete and neither names `CancelJobTasks`, which carries `status IN ('pending', 'queued', 'running', 'dispatched')` - a non-terminal allow-list on a statement that WRITES. Add an entry to the bulleted list, between the `AppendTaskLog` and `ListOverdueAssignedTasks` entries:

```go
//   - CancelJobTasks (query/tasks.sql) - `status IN ('pending','queued',
//     'preparing','running','dispatched')`, the non-terminal set a job cancel
//     fails. It WRITES: it stamps `failed`, nulls worker_id and assigned_at and
//     bumps the epoch. A new NON-TERMINAL status omitted here means a cancelled
//     job leaves that task live, with its agent still executing, while the job
//     reads `cancelled` - and internal/cli/logs.go's emitSnapshot documents that
//     exact reachability. A new TERMINAL status must stay OUT: it would restamp
//     a finished task's finished_at and bump an epoch the trailing-log flush
//     still needs. The `queued` literal in this list is DEAD - jobs_status_check
//     admits `queued`, tasks_status_check never has - and removing it belongs to
//     idea-2026-07-01-dead-status-vocabulary, not here.
```

- [ ] **Step 3: Rewrite the comment's future tense to the present**

Per spec 7.1. In this file `preparing` is named five times as a candidate. Each becomes a statement about the current partition. The near-term candidate named throughout becomes the task-level `cancelled` the comment's own header already names. Do not increment any cardinal: the failure message says "the nine that carry the 'currently assigned' partition" and lists nine statements - the widening does not change that count, so it stays; but the message's index sentence and the `AppendTaskLog` / `ListOverdueAssignedTasks` paragraphs that use `preparing` as a hypothetical must be re-pointed. `TASK_STATUS_PREPARE_FAILED` is the live instance of the opposite (terminal) shape and is the correct replacement example.

- [ ] **Step 4: Run the integration lockstep guard**

```bash
go test -tags integration -p 1 ./internal/store/ -run 'TestTasksStatusVocabularyIsExactly|TestStatusVocabularyConstraints|TestHotPathIndexes|TestActiveTaskIndexPredicate' -count=1 -v -timeout 900s
```

Expected: all PASS. `TestStatusVocabularyConstraints_RoundTrip` drives `MigrateTo(dsn, 18)`, which runs `000023.down` as a side effect, and must stay green - it seeds no `preparing` row, so the missing demoting UPDATE is invisible to it. That is precisely why Task 6 exists.

---

## Task 6: The down migration demotes `preparing` rows

**Files:**
- Create: `internal/store/migration_000023_preparing_integration_test.go`
- Modify: `internal/store/migrations/000023_task_preparing_status.down.sql`

- [ ] **Step 1: Write the failing test**

Sketch, modelled on `TestMigration000020_DownDropsListEndpointIndexes` (`internal/store/list_endpoint_indexes_integration_test.go:48-85`) and `TestMigration000021DownUp` (`internal/store/tasks_assigned_at_integration_test.go:41-60`). `storeMigrateTo` is the existing package-local alias declared at `list_endpoint_indexes_integration_test.go:85`; reuse it, do not redeclare it. `newMigratedPoolWithDSN` is in `internal/store/migrate_down_test.go`.

```go
//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// preparingDownTarget is the schema version just below 000023, i.e. the state
// its down migration restores.
const preparingDownTarget = 22

// TestMigration000023_DownDemotesPreparingRowsAndNarrowsTheConstraint pins the
// ORDER of the statements in 000023_task_preparing_status.down.sql, which is the
// whole correctness argument of that file.
//
// THE SEEDED ROW IS WHAT MAKES THE ORDER OBSERVABLE. With the narrowed
// ADD CONSTRAINT placed before the demoting UPDATE, the down migration is simply
// unrunnable against any database that has a `preparing` row - and the version
// of the file that "looks right" is the version that has never been run with
// data. Every other test that drives MigrateTo past 000023
// (TestStatusVocabularyConstraints_RoundTrip, TestHotPathIndexesDownUp) seeds no
// such row, so all of them stay green with the ordering wrong.
//
// Demotion is to `dispatched`, not to `pending`, and that is deliberate: the row
// still has a live agent, a worker_id and an assignment_epoch, and `dispatched`
// is what described it truthfully in the old vocabulary. Demoting to `pending`
// would end a live assignment without bumping the epoch - an epoch-fence
// violation performed by a migration.
func TestMigration000023_DownDemotesPreparingRowsAndNarrowsTheConstraint(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := context.Background()
	q := store.New(pool)

	// Seed a real `preparing` row through the production statements: create,
	// claim, then UpdateTaskStatus off the `dispatched` row (which the allow-list
	// admits at this commit) so the row carries a real assignee and epoch.
	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "down-23", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "syncing", Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"),
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
		AssignedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	prep, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: "preparing", WorkerID: w.ID,
		AssignmentEpoch: claimed.AssignmentEpoch,
	})
	require.NoError(t, err, "precondition: 000023's widened constraint must admit `preparing`")
	require.Equal(t, "preparing", prep.Status, "precondition")

	require.NoError(t, storeMigrateTo(dsn, preparingDownTarget),
		"the down migration must RUN against a database containing a preparing row. If this fails "+
			"with a check_violation on tasks_status_check, the narrowed ADD CONSTRAINT is placed "+
			"before the demoting UPDATE in 000023_task_preparing_status.down.sql")

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status,
		"a preparing row must be demoted to dispatched, which is what described it in the old vocabulary")
	assert.True(t, after.WorkerID.Valid, "the demotion must not end the assignment")
	assert.Equal(t, claimed.AssignmentEpoch, after.AssignmentEpoch,
		"and must not bump the epoch: ending an assignment is not this migration's job")

	// The constraint is narrow again.
	_, err = pool.Exec(ctx,
		`INSERT INTO tasks (job_id, name, status) VALUES ($1, 'post-down', 'preparing')`, job.ID)
	require.Error(t, err, "after the down migration tasks_status_check must reject 'preparing'")

	// And so is the index.
	var pred string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT pg_get_expr(i.indpred, i.indrelid)
		FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = 'idx_tasks_worker_active'`).Scan(&pred))
	assert.NotContains(t, pred, "preparing",
		"the down migration must restore the NARROW index predicate too; 000023's up widened it, "+
			"so a down that only narrows the constraint leaves the two disagreeing")

	// Re-up must be clean: no duplicate-name collision on the index, constraint back.
	require.NoError(t, store.Migrate(dsn), "re-applying up after down must succeed")
	_, err = pool.Exec(ctx,
		`INSERT INTO tasks (job_id, name, status) VALUES ($1, 'post-up', 'preparing')`, job.ID)
	require.NoError(t, err, "after the re-up tasks_status_check must accept 'preparing' again")
}
```

- [ ] **Step 2: Run it and record the RED**

```bash
go test -tags integration -p 1 ./internal/store/ -run TestMigration000023_DownDemotesPreparingRowsAndNarrowsTheConstraint -count=1 -v -timeout 600s
```

Expected: `--- FAIL` at the `storeMigrateTo` assertion, with a golang-migrate error wrapping `ERROR: check constraint "tasks_status_check" of relation "tasks" is violated by some row (SQLSTATE 23514)`.

- [ ] **Step 3: Add the demoting UPDATE and the ordering comment**

Final `internal/store/migrations/000023_task_preparing_status.down.sql`:

```sql
-- Reverse 000023. THE ORDER BELOW IS THE CORRECTNESS ARGUMENT.
--
-- The UPDATE must precede the narrowed ADD CONSTRAINT, or the constraint add
-- fails against any existing `preparing` row and this migration is unrunnable
-- against a real database. Pinned by
-- TestMigration000023_DownDemotesPreparingRowsAndNarrowsTheConstraint, which
-- seeds such a row precisely so the ordering is the thing under test; every
-- other test that drives MigrateTo past this version seeds none.
--
-- Demotion is to `dispatched` and not to `pending`: the row still has a live
-- agent, a worker_id and an assignment_epoch, and `dispatched` is the state that
-- described it truthfully in the old vocabulary. Demoting to `pending` would end
-- a live assignment without bumping the epoch.
UPDATE tasks SET status = 'dispatched' WHERE status = 'preparing';

DROP INDEX IF EXISTS idx_tasks_worker_active;

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'running');

ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','running','done','failed','timed_out'));
```

- [ ] **Step 4: Run it - GREEN**

```bash
go test -tags integration -p 1 ./internal/store/ -run TestMigration000023_DownDemotesPreparingRowsAndNarrowsTheConstraint -count=1 -v -timeout 600s
```

Expected: `--- PASS`.

- [ ] **Step 5: Run the whole store integration package and the default lane**

```bash
go test -tags integration -p 1 ./internal/store/... -count=1 -timeout 1200s
make test
```

Expected: no `FAIL` in either.

- [ ] **Step 6: Commit Tasks 2 through 6 together**

```bash
git add internal/store/migrations/000023_task_preparing_status.up.sql \
        internal/store/migrations/000023_task_preparing_status.down.sql \
        internal/store/active_task_index_predicate_integration_test.go \
        internal/store/migration_000023_preparing_integration_test.go \
        internal/store/tasks_status_vocabulary_lockstep_test.go \
        internal/worker/taskstatus_fence_counters_test.go
git commit -m "feat(store): migration 000023 admits preparing in the vocabulary and the active-task index

The constraint and the partial index move together: a widened statement set
against a narrow idx_tasks_worker_active predicate makes the index unusable for
every assignment-partition statement, including the dispatcher's per-cycle
CountActiveTasksByAllWorkers. Nothing in the tree could see that, so
TestActiveTaskIndexPredicateMatchesTheAssignmentPartition is added; TestHotPathIndexes
asserts index names only and stays green either way.

tasksStatusVocabulary required exactly one ADD CONSTRAINT across up-migrations,
which this makes two. Its replacement takes the lexically greatest match and
asserts that it did, so a future rewrite that takes the first match fails loudly
instead of reading 000019's vocabulary forever."
```

**Use an explicit pathspec.** Concurrent agents share one git index.

---

## Task 7: Widen the two write allow-lists and the Go mirror

**Files:**
- Modify: `internal/store/query/tasks.sql:136` (`UpdateTaskStatus`)
- Modify: `internal/store/query/tasks.sql:230` (`IncrementTaskRetryCount`)
- Modify: `internal/worker/taskstatus_fence_counters.go:233` (`taskStatusIsWritable`)
- Regenerate: `internal/store/tasks.sql.go`

- [ ] **Step 1: Widen the two SQL predicates only**

`internal/store/query/tasks.sql:136` and `:230`, both currently:

```sql
  AND status IN ('pending', 'dispatched', 'running')
```

become:

```sql
  AND status IN ('pending', 'dispatched', 'preparing', 'running')
```

Nothing else in this step. Do **not** touch `AppendTaskLog` yet - it belongs to Task 10's batch.

- [ ] **Step 2: Run the default-lane guard and record the RED**

```bash
go test ./internal/worker/ -run TestTaskStatusWritableSetMatchesTheSQLAllowList -count=1 -v -timeout 60s
```

Expected: `--- FAIL` with

```
tasks.sql's UpdateTaskStatus admits status "preparing" and taskStatusIsWritable says it is
NOT writable. The two have drifted: a rejection for a "preparing" row would now be labelled
`duplicate` or `conflicting` when it is in fact a `raced`. Add it.
```

This is the SQL-to-Go containment loop. It reads `tasks.sql` as a **source file**, so this RED does not depend on `make generate` having run.

- [ ] **Step 3: Widen the Go mirror**

`internal/worker/taskstatus_fence_counters.go:233`:

```go
	case "pending", "dispatched", "preparing", "running":
```

Keep the statuses spelled inline. `taskStatusWritableLiterals` reads the function's own AST and fails closed with a message saying so if they move to a var, a const or a helper (decision D8).

- [ ] **Step 4: Run it - GREEN**

```bash
go test ./internal/worker/ -run TestTaskStatusWritableSetMatchesTheSQLAllowList -count=1 -v -timeout 60s
```

Expected: `--- PASS`.

- [ ] **Step 5: Measure the OPPOSITE order, on a scratch copy**

The guard's own comment claims both directions go RED. Turn the claim into a measurement.

```bash
SCRATCH="$TMPDIR/preparing-scratch"
mkdir -p "$SCRATCH"
cp internal/store/query/tasks.sql "$SCRATCH/tasks.sql.bak"
cp internal/worker/taskstatus_fence_counters.go "$SCRATCH/counters.go.bak"

# Revert the SQL half only, leaving the Go mirror wide.
python3 - <<'PY'
import io
p = "internal/store/query/tasks.sql"
s = io.open(p, encoding="utf-8", newline="").read()
s = s.replace("AND status IN ('pending', 'dispatched', 'preparing', 'running')",
              "AND status IN ('pending', 'dispatched', 'running')")
io.open(p, "w", encoding="utf-8", newline="").write(s)
PY

go test ./internal/worker/ -run TestTaskStatusWritableSetMatchesTheSQLAllowList -count=1 -v -timeout 60s
```

Expected: `--- FAIL` naming the set-comparison rung, `taskStatusIsWritable's own source names a different set of statuses than UpdateTaskStatus's allow-list`. Record which message each direction produced.

**Restore from the copy, never with `git checkout --`** - the working tree holds uncommitted work from Steps 1 and 3, and `git checkout --` would discard it:

```bash
cp "$SCRATCH/tasks.sql.bak" internal/store/query/tasks.sql
go test ./internal/worker/ -run TestTaskStatusWritableSetMatchesTheSQLAllowList -count=1 -v -timeout 60s
```

Expected: `--- PASS` (the control - confirms the restore worked and the mutation was really applied and really removed).

- [ ] **Step 6: `make generate`, then the CRLF revert procedure**

```bash
make generate
git status --porcelain
```

sqlc emits LF and this is a CRLF repo, so it rewrites line endings across **all** generated files, not just the one that changed.

```bash
git diff --ignore-all-space -- internal/store/ | head -80
```

That shows the real content changes. Everything else is LF-only churn. For each file in `git status --porcelain` whose `git diff --ignore-all-space` output is empty:

```bash
git checkout -- <that file>
```

**Never conclude "nothing to revert" from `git diff` alone.** `core.autocrlf=true` makes `git diff` normalise LF churn away while `git status` still lists the files as modified.

- [ ] **Step 7: VERIFY the regeneration survived the revert**

This repo has lost a regenerated file to that revert before. The instrument must match the claim, and the claim is "the generated file mirrors the source", so compare the two:

```bash
echo "source: $(grep -c -F "'pending', 'dispatched', 'preparing', 'running'" internal/store/query/tasks.sql)"
echo "gen:    $(grep -c -F "'pending', 'dispatched', 'preparing', 'running'" internal/store/tasks.sql.go)"
```

Both must read **2** at this commit (`UpdateTaskStatus`, `IncrementTaskRetryCount`), and they must be **equal**. If `gen` reads 0, the revert discarded the regeneration: re-run `make generate` and redo Step 6 more carefully. If `gen` exceeds `source`, a doc comment inside a statement quotes the predicate with identical spacing - read the extra hit and confirm it is a comment before accepting it.

Then the line-ending and diffstat checks:

```bash
git ls-files --eol internal/store/tasks.sql.go     # must read i/lf
git diff --stat internal/store/
```

The diffstat must be proportionate to a two-line change plus sqlc's own formatting. `gofmt -l` is useless as a signal here; it lists hundreds of files on a clean tree purely because of working-copy CRLF.

- [ ] **Step 8: Run both lanes**

```bash
make test
go test -tags integration -p 1 ./internal/store/... ./internal/worker/... -count=1 -timeout 1800s
```

Expected: no `FAIL`.

- [ ] **Step 9: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go \
        internal/worker/taskstatus_fence_counters.go
git commit -m "feat(store): UpdateTaskStatus and IncrementTaskRetryCount admit a preparing row

The allow-list is not what lets a dispatched row BECOME preparing - it is what
lets a preparing row become running. Without it, a task that reports PREPARING is
permanently unwritable and every subsequent report from its own assignee is
classified fenceReasonConflicting, driving the counter README calls the
actionable number. Widened before the handler case for that reason.

Both directions of TestTaskStatusWritableSetMatchesTheSQLAllowList were measured:
SQL-first fails the containment loop, Go-first fails the set comparison."
```

---

## Task 8: `handleTaskStatus` maps `TASK_STATUS_PREPARING`

**Files:**
- Modify: `internal/worker/handler.go:1478-1495` (the enum switch)
- Create: `internal/worker/handler_taskstatus_preparing_integration_test.go`

The agent already sends `PREPARING` (`internal/agent/runner.go`, once per source-bearing task, strictly before `provider.Prepare` and strictly before the `RUNNING` send). No agent change.

- [ ] **Step 1: Write the failing tests**

Sketch. `newTestStore` returns `(q, pool)` and `seedTaskAndTwoWorkers(t, ctx, q, name, retries)` returns `(jobID, taskID, w1, w2)` - both verified in use at `internal/worker/handler_watchdog_e2e_integration_test.go:41-47`. `h.HandleTaskStatus` and `h.UUIDStringForTest` are the integration-tagged shims in `internal/worker/export_test.go:66,174`.

```go
//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleTaskStatus_APreparingReportMovesTheRowAndLeavesStartedAtNull is the
// feature's headline property and its central obligation, in one test.
//
// The started_at half is the one with the blast radius, and it is the fork's
// regression this slice must not port. ListOverdueAssignedTasks' execution arm
// keys on started_at IS NOT NULL together with timeout_seconds, and README's
// RELAY_TASK_WATCHDOG_MARGIN row states the contract in operator-facing words:
// "applies only to tasks with timeout_sec > 0 that have reported running".
// PREPARING arrives BEFORE the sync starts, so any clock the coordinator starts
// on that message is a clock that runs for the whole sync - a task with a
// 30-minute timeout and a two-hour sync would be swept timed_out mid-sync, with
// no way for the agent to object.
func TestHandleTaskStatus_APreparingReportMovesTheRowAndLeavesStartedAtNull(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "preparing-basic", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
		AssignedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.False(t, claimed.StartedAt.Valid, "precondition: a dispatched row has no start time")

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_PREPARING,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	got, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "preparing", got.Status,
		"the assignee's own PREPARING report at its own epoch must move the row. At HEAD the enum "+
			"switch falls to `default: return` and the row stays dispatched for the whole sync")
	assert.False(t, got.StartedAt.Valid,
		"started_at must stay NULL through preparing. Stamping it here starts the watchdog's "+
			"EXECUTION bound during the workspace sync, so a task with a 30-minute timeout_sec and a "+
			"two-hour sync is swept timed_out mid-sync")
	assert.False(t, got.FinishedAt.Valid, "preparing is not terminal")
	assert.Equal(t, claimed.AssignmentEpoch, got.AssignmentEpoch,
		"a preparing transition ends no generation and must not bump the epoch")
	assert.True(t, got.WorkerID.Valid, "and must not clear the assignee")

	job, err := q.GetJob(ctx, got.JobID.Bytes)
	_ = job
	_ = err
}

// TestHandleTaskStatus_APreparingReportAfterRunningDoesNotClearStartedAt pins
// decision D5. After this slice UpdateTaskStatus's allow-list admits `running`,
// so a SECOND PREPARING message from a task's own assignee at its own current
// epoch moves a running row back to preparing. That is unreachable for a
// well-behaved agent (the runner sends PREPARING once, before Prepare, and never
// after RUNNING) and reachable for a misbehaving one.
//
// It is ACCEPTED and BOUNDED rather than forbidden, because forbidding it needs
// a second writer of tasks.status and the whole shape of this slice is that it
// adds none. The bound is what this test asserts: started_at survives via the
// COALESCE, so the execution arm is unaffected; assigned_at and the epoch are
// untouched, so the absolute arm is unaffected; the row stays in the assigned
// partition everywhere. The damage is a misleading status string on one row,
// driven by that row's own assignee - the "identity is not honesty" shape the
// fence counters already document.
func TestHandleTaskStatus_APreparingReportAfterRunningDoesNotClearStartedAt(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "preparing-backward", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
		AssignedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	mid, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.True(t, mid.StartedAt.Valid, "precondition: RUNNING stamps a real start time")

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_PREPARING,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "preparing", after.Status, "the backward transition is accepted, not refused")
	require.True(t, after.StartedAt.Valid,
		"UpdateTaskStatus COALESCEs started_at, so a backward transition cannot clear the clock the "+
			"execution arm is measured from - which is what bounds this capability")
	assert.WithinDuration(t, mid.StartedAt.Time, after.StartedAt.Time, time.Millisecond)
	assert.Equal(t, claimed.AssignmentEpoch, after.AssignmentEpoch)
}
```

- [ ] **Step 2: Run and record the RED**

```bash
go test -tags integration -p 1 ./internal/worker/ -run 'TestHandleTaskStatus_APreparingReport' -count=1 -v -timeout 900s
```

Expected: both `--- FAIL`, the first at `require.Equal(t, "preparing", got.Status)` with `actual: "dispatched"`, the second at `assert.Equal(t, "preparing", after.Status)` with `actual: "running"`. Both because `upd.Status` falls to `default: return` and no write happens at all.

- [ ] **Step 3: Add the case**

`internal/worker/handler.go`, in the enum switch, between the `RUNNING` and `DONE` arms:

```go
	case relayv1.TaskStatus_TASK_STATUS_PREPARING:
		statusStr = "preparing"
```

**Change nothing else in this function.** Everything downstream is already correct:
- `terminal := statusStr == "failed" || statusStr == "timed_out"` - `preparing` is non-terminal, so the error-message append, the retry branch, `FailDependentTasks` and the `NotifyTaskCompleted` wake are all skipped.
- `startedAt := task.StartedAt; if statusStr == "running" { ... }` - **stays exactly as written**. Do not add the fork's Go-side `!task.StartedAt.Valid` guard: it duplicates `UpdateTaskStatus`'s `COALESCE`, and a second guard on a property the statement already enforces is the shape that goes stale when the statement changes.
- `finishedAt` stays the zero value, binding SQL NULL.
- `updateJobStatusFromTasks` returns `running`, because `RecomputeJobStatus` (`internal/store/query/jobs.sql`) counts terminality with `status NOT IN ('done','failed','timed_out')` - a deny-list on the terminal set, so a new status is non-terminal without an edit. This is the one place in the tree where a deny-list is the correct shape, and it is correct because the complement it fails open into is the harmless one.
- The SSE `task` frame publishes `{"id":...,"status":"preparing"}`, so the SPA's job view and the CLI's log follower both see the transition live.

- [ ] **Step 4: Run - GREEN**

```bash
go test -tags integration -p 1 ./internal/worker/ -run 'TestHandleTaskStatus_APreparingReport' -count=1 -v -timeout 900s
```

Expected: both `--- PASS`.

- [ ] **Step 5: The started_at mutation - prove the second assertion discriminates**

A kill must name its guard, so run the mutation and confirm the failing assertion is the `started_at` one and not something else.

```bash
cp internal/worker/handler.go "$SCRATCH/handler.go.bak"
```

Edit `internal/worker/handler.go` and change

```go
	if statusStr == "running" {
```

to

```go
	if statusStr == "running" || statusStr == "preparing" {
```

Run:

```bash
go test -tags integration -p 1 ./internal/worker/ -run TestHandleTaskStatus_APreparingReportMovesTheRowAndLeavesStartedAtNull -count=1 -v -timeout 900s
```

Expected: `--- FAIL` at `assert.False(t, got.StartedAt.Valid, "started_at must stay NULL through preparing...")`. If it passes, the mutation did not apply - check the edit landed - and if it fails on a different assertion, the test is not discriminating and must be strengthened before proceeding.

**Leave the mutation in place; Task 11 needs it again.** Note the backup path.

- [ ] **Step 6: Restore from the copy and re-run the control**

```bash
cp "$SCRATCH/handler.go.bak" internal/worker/handler.go
go test -tags integration -p 1 ./internal/worker/ -run 'TestHandleTaskStatus_APreparingReport' -count=1 -v -timeout 900s
```

Expected: both `--- PASS`.

- [ ] **Step 7: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_taskstatus_preparing_integration_test.go
git commit -m "feat(worker): handleTaskStatus persists TASK_STATUS_PREPARING

One case, and nothing else in the function moves. started_at stays stamped only
at running: the watchdog spec's R3 obligation is that a workspace sync stays
bounded by exactly one arm, the absolute one keyed on assigned_at, and PREPARING
arrives before the sync starts, so any clock started on it runs for the whole
sync. The fork's Go-side !task.StartedAt.Valid guard is deliberately not ported;
it duplicates UpdateTaskStatus's COALESCE.

The backward running -> preparing transition an assignee can now drive is
accepted and bounded, not forbidden: forbidding it would need a second writer of
tasks.status."
```

---

## Task 9: The eleven assignment-partition predicates, red first

Every test in this task is written **before** any SQL moves, and all must be observed RED together. Then one SQL pass and one `make generate`.

**Files:**
- Modify: `internal/store/query/tasks.sql` (11 predicates)
- Modify: `internal/store/list_active_tasks_for_worker_integration_test.go`
- Modify: `internal/store/list_overdue_assigned_tasks_integration_test.go`
- Modify: `internal/api/workers_tasks_integration_test.go:78-97`
- Create: `internal/store/preparing_partition_integration_test.go`
- Create: `internal/worker/handler_preparing_watchdog_integration_test.go`
- Regenerate: `internal/store/tasks.sql.go`

- [ ] **Step 1: Add a `preparing` helper to each store fixture**

`internal/store/tasks_assigned_at_integration_test.go` already has `assignedFixture.claimedAt`. Add, in `internal/store/preparing_partition_integration_test.go`:

```go
// preparingAt claims a task at `at` and then moves it to 'preparing' through the
// production status writer, so the row carries a real assignee and a real epoch -
// the same construction runningAt uses for 'running'.
func preparingAt(t *testing.T, f *assignedFixture, name string, at time.Time) store.Task {
	t.Helper()
	task := f.claimedAt(t, name, at)
	got, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID:              task.ID,
		Status:          "preparing",
		WorkerID:        f.w.ID,
		AssignmentEpoch: task.AssignmentEpoch,
	})
	require.NoError(t, err)
	require.False(t, got.StartedAt.Valid, "a preparing row has no start time")
	return got
}
```

And in `internal/store/list_overdue_assigned_tasks_integration_test.go`, beside `dispatched` and `running`:

```go
// preparing drives a claimed row to 'preparing'. started_at stays NULL, which is
// the state a task sits in for the whole workspace sync.
func (f *overdueFixture) preparing(t *testing.T, name string, timeoutSec *int32, assignedAt time.Time) store.Task {
	t.Helper()
	claimed := f.dispatched(t, name, timeoutSec, assignedAt)
	updated, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: "preparing", WorkerID: f.w.ID,
		AssignmentEpoch: claimed.AssignmentEpoch,
	})
	require.NoError(t, err)
	require.False(t, updated.StartedAt.Valid, "precondition: preparing stamps no start time")
	return updated
}
```

- [ ] **Step 2: Write the twelve failing assertions**

Each row below is one test and one expected RED. Write them all before running anything.

| Test | File | Statement | Expected RED |
|---|---|---|---|
| `TestAppendTaskLog_APreparingTaskAcceptsLogChunks` | `preparing_partition_integration_test.go` | `AppendTaskLog` | `pgx.ErrNoRows` - the fence matches nothing |
| `TestGetActiveTasksForWorker_IncludesAPreparingTask` | same | `GetActiveTasksForWorker` | row absent from the returned slice |
| `TestListGraceCandidates_AWorkerWithOnlyAPreparingTaskIsACandidate` | same | `ListGraceCandidates` | worker absent |
| `TestRequeueTaskByID_RequeuesAPreparingTaskForItsAssignee` | same | `RequeueTaskByID` | rowcount 0, row still `preparing` |
| `TestRequeueWorkerTasks_RequeuesAPreparingTask` | same | `RequeueWorkerTasks` | id not in the returned slice |
| `TestRequeueWorkerTasksIfEpoch_RequeuesAPreparingTask` | same | `RequeueWorkerTasksIfEpoch` | id not returned |
| `TestCountActiveTasksByAllWorkers_CountsAPreparingTask` | same | `CountActiveTasksByAllWorkers` | count short by one |
| `TestCancelJobTasks_FailsAPreparingTask` | same | `CancelJobTasks` | row still `preparing`, `worker_id` still set |
| `TestListActiveTasksForWorkerPage_ReturnsEveryAssignedStatus` (rename of `..._ReturnsBothAssignedStatuses`, third row added) | `list_active_tasks_for_worker_integration_test.go:46` | `ListActiveTasksForWorkerPage` | `Len(rows) == 2`, expected 3 |
| `TestCountActiveTasksForWorker_MatchesTheListStatement` (extend) | same file, line 103 | `CountActiveTasksForWorker` | total 2, expected 3 |
| `TestListOverdueAssignedTasks_AbsoluteArmSweepsAPreparingTask` | `list_overdue_assigned_tasks_integration_test.go` | `ListOverdueAssignedTasks` | row absent from the sweep |
| `TestListWorkerTasks_ReturnsOnlyTheAssignmentPartition` (extend to seven statuses) | `internal/api/workers_tasks_integration_test.go:78` | end to end | seeds 7, `ElementsMatch` expects 3, gets 2 |

Rename the `_ReturnsBothAssignedStatuses` test rather than leaving its name behind: "Both" becomes false with a third member, and a name is not something any check will redden. Delete the cardinal, do not increment it. Same for the api test's comment, which currently says "Seeds one task per status of the full six-value vocabulary" - make it "one task per status of the vocabulary" and let `TestTasksStatusVocabularyIsExactly` own the number.

Sketch of the two that carry the most argument:

```go
// TestAppendTaskLog_APreparingTaskAcceptsLogChunks is the site where omission is
// catastrophic and silent. A preparing row has finished_at IS NULL, so it fails
// the recency arm too; leaving `preparing` out of the FIRST arm discards 100% of
// every workspace sync's log output, with no error and no log line - and the
// agent already streams that output as LOG_STREAM_PREPARE chunks today.
func TestAppendTaskLog_APreparingTaskAcceptsLogChunks(t *testing.T) {
	f := newAssignedFixture(t)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	prep := preparingAt(t, f, "syncing", base)

	row, err := f.q.AppendTaskLog(f.ctx, store.AppendTaskLogParams{
		TaskID: prep.ID, AssignmentEpoch: prep.AssignmentEpoch, WorkerID: f.w.ID,
		Stream: "stdout", Content: "//depot/... - syncing",
		MinFinishedAt: pgtype.Timestamptz{Time: base.Add(-15 * time.Minute), Valid: true},
	})
	require.NoError(t, err,
		"a preparing task's own assignee must be able to append. pgx.ErrNoRows here means the first "+
			"arm of AppendTaskLog's disjunction omits `preparing`, which drops every workspace-sync "+
			"log line in the system silently")
	require.Equal(t, prep.JobID, row.JobID)
}

// TestListOverdueAssignedTasks_AbsoluteArmSweepsAPreparingTask is the watchdog
// spec's R3 obligation at the statement level: a workspace sync must remain
// bounded by exactly one arm, the absolute one. Omitting `preparing` from this
// partition reopens the unbounded-assignment hole this statement exists to
// close, for exactly the state that most needs it.
func TestListOverdueAssignedTasks_AbsoluteArmSweepsAPreparingTask(t *testing.T) {
	f := newOverdueFixture(t)

	// timeout_seconds = 0 and started_at NULL: the execution arm cannot see this
	// row at all, so a positive result can only come from the absolute arm.
	syncing := f.preparing(t, "syncing", i32(0), f.now.Add(-30*time.Hour))
	fresh := f.preparing(t, "fresh", i32(0), f.now.Add(-time.Minute))

	got := f.list(t, f.bothArms())
	assert.True(t, got[syncing.ID],
		"a preparing task past RELAY_TASK_MAX_ASSIGNMENT must still be swept. Left out of this "+
			"partition it holds its worker slot and its job forever, unswept, with no log line")
	assert.False(t, got[fresh.ID], "a freshly-preparing row must be left alone")
}
```

- [ ] **Step 3: Run all twelve and record every RED**

```bash
go test -tags integration -p 1 ./internal/store/ -run 'Preparing|APreparingTask|ReturnsEveryAssignedStatus|MatchesTheListStatement' -count=1 -v -timeout 1200s
go test -tags integration -p 1 ./internal/api/ -run TestListWorkerTasks_ReturnsOnlyTheAssignmentPartition -count=1 -v -timeout 1800s
```

Expected: twelve `--- FAIL` lines, each matching the table above. Write the actual messages down. A test that passes here is not exercising the predicate it names - fix it before proceeding.

- [ ] **Step 4: Widen the eleven predicates**

In `internal/store/query/tasks.sql`. Nine take the identical assignment-partition list; write it in the order the existing lists use, so the diff reads as an insertion:

```sql
  status IN ('dispatched', 'preparing', 'running')
```

at `GetActiveTasksForWorker` (line 505), `ListGraceCandidates` (515, as `t.status IN (...)`), `RequeueTaskByID` (601), `CountActiveTasksByAllWorkers` (618), `ListOverdueAssignedTasks` (689), `RequeueWorkerTasks` (788), `RequeueWorkerTasksIfEpoch` (802), `ListActiveTasksForWorkerPage` (996), `CountActiveTasksForWorker` (1020).

`CancelJobTasks` (line 774) keeps its own non-canonical ordering; insert only:

```sql
WHERE job_id = $1 AND status IN ('pending', 'queued', 'preparing', 'running', 'dispatched');
```

The dead `'queued'` literal stays. `jobs_status_check` admits `queued` for jobs; `tasks_status_check` never has, so it is dead in this statement - removing it is a behaviour-neutral cleanup belonging to `idea-2026-07-01-dead-status-vocabulary`, and folding it in here puts an unrelated deletion inside a diff a reviewer must read as a pure set widening (decision D7).

`AppendTaskLog` (lines 353-354) gains it in the **first arm of the disjunction only**:

```sql
      AND (t.status IN ('pending', 'dispatched', 'preparing', 'running')
           OR t.finished_at > sqlc.arg(min_finished_at)::timestamptz)
```

**The disjunction must never become a conjunction.** Writing `AND t.status IN (...)` in place of the arm rejects the trailing chunk that arrives just after a terminal status - a real and common ordering - and silently truncates the tail of every task's output in production. Step 8 runs that mutant.

**Do not touch** `RequeueTask` (line 497, `status = 'dispatched'`), `ClaimTaskForWorker`, `GetEligibleTasks`, `FailDependentTasks`, `UpdateTaskStatusEpoch`, `SelectRetryableTaskIDs`, `RetryJobTasks` or `CountTerminalTasksForWorker`. Spec section 1.1 records why each stays, and spec section 5 is the whole partition as one table.

- [ ] **Step 5: `make generate` and the CRLF revert**

Same procedure as Task 7 Step 6. **This is the second `make generate` in the lane, and it is safe only because Task 7's output is already committed**: the revert works by `git checkout -- <file>`, which restores HEAD, so an uncommitted earlier regeneration would be discarded by it. Confirm before running:

```bash
git status --porcelain internal/store/tasks.sql.go   # must be empty
make generate
git status --porcelain
git diff --ignore-all-space -- internal/store/ | head -120
# git checkout -- <each file whose --ignore-all-space diff is empty>
```

- [ ] **Step 6: VERIFY the regeneration survived**

```bash
for pat in "'dispatched', 'preparing', 'running'" \
           "'pending', 'dispatched', 'preparing', 'running'" \
           "'pending', 'queued', 'preparing', 'running', 'dispatched'"; do
  s=$(grep -c -F "$pat" internal/store/query/tasks.sql)
  g=$(grep -c -F "$pat" internal/store/tasks.sql.go)
  echo "source=$s gen=$g  <<$pat>>"
done
```

Expected, and each pair must be **equal**: 9, 3, 1. That totals thirteen predicates. If any `gen` reads 0 the revert discarded the regeneration - re-run `make generate` and redo Step 5. If a `gen` exceeds its `source`, read the extra hit: a statement's own doc comment quoting the predicate with identical spacing is benign, anything else is not.

```bash
git ls-files --eol internal/store/tasks.sql.go   # must read i/lf
git diff --stat internal/store/
```

- [ ] **Step 7: Run all twelve - GREEN**

```bash
go test -tags integration -p 1 ./internal/store/... -count=1 -timeout 1200s
go test -tags integration -p 1 ./internal/api/ -run TestListWorkerTasks -count=1 -v -timeout 1800s
make test
```

Expected: no `FAIL`.

- [ ] **Step 8: The `AppendTaskLog` conjunction mutant**

```bash
cp internal/store/query/tasks.sql "$SCRATCH/tasks.sql.pre-mutant"
cp internal/store/tasks.sql.go "$SCRATCH/tasks.sql.go.pre-mutant"
```

Replace the disjunction in `AppendTaskLog` with a bare conjunction:

```sql
      AND t.status IN ('pending', 'dispatched', 'preparing', 'running')
```

Then `make generate` (and the revert), and run:

```bash
go test -tags integration -p 1 ./internal/worker/ -run TestHandleTaskLog_TrailingChunkJustAfterATerminalStatusIsStillStored -count=1 -v -timeout 900s
```

Expected: `--- FAIL`. If it passes, the mutation did not reach the generated code - check `internal/store/tasks.sql.go` contains the conjunction - because a silently-unapplied mutation reports "survived".

**Restore from the copies, not with `git checkout --`:**

```bash
cp "$SCRATCH/tasks.sql.pre-mutant" internal/store/query/tasks.sql
cp "$SCRATCH/tasks.sql.go.pre-mutant" internal/store/tasks.sql.go
go test -tags integration -p 1 ./internal/worker/ -run TestHandleTaskLog_TrailingChunkJustAfterATerminalStatusIsStillStored -count=1 -v -timeout 900s
```

Expected: `--- PASS` (the control).

- [ ] **Step 9: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go \
        internal/store/preparing_partition_integration_test.go \
        internal/store/list_active_tasks_for_worker_integration_test.go \
        internal/store/list_overdue_assigned_tasks_integration_test.go \
        internal/api/workers_tasks_integration_test.go
git commit -m "feat(store): the assignment partition and the log fence admit preparing

Eleven predicates, each with its own red-first test. Not one of these sites was
broken at HEAD - a syncing task's row IS dispatched today, so every one of them
matches it - which is why every omission here is a live regression against
current behaviour rather than a missed improvement, and eleven of the twelve are
silent.

AppendTaskLog gains it in the FIRST ARM ONLY. The conjunction mutant was run and
reddens TestHandleTaskLog_TrailingChunkJustAfterATerminalStatusIsStillStored.
RequeueTask stays 'dispatched'-only: at its caller's own (epoch, worker) pair the
DispatchTask was never queued, so the task cannot be preparing for the same
reason it cannot be running."
```

---

## Task 10: `RequeueTask` must NOT gain `preparing` - pin it

Decision D1. This test is **green the moment it is written**, and saying so is the point: it is a regression guard, not a red-first criterion. Its discriminating power comes from the mutation in Step 3, which must be run.

**Files:**
- Modify: `internal/store/requeue_task_fence_integration_test.go` (add beside `TestRequeueTask_RunningTaskIsNotRequeuedByTheSendFailurePath:280`)

- [ ] **Step 1: Write the test**

Mirror the existing `_RunningTaskIsNotRequeuedByTheSendFailurePath` exactly; read it first and copy its fixture construction.

```go
// TestRequeueTask_APreparingTaskIsNotRequeuedByTheSendFailurePath is the
// negative half of this slice, and it is GREEN AT HEAD OF THIS BRANCH - stated
// rather than disguised. Its value is as a regression guard against a later
// "harmonize with RequeueTaskByID" edit, and its discriminating power is
// established by mutation, not by a red-first run.
//
// RequeueTask's only production caller is Dispatcher.dispatchOne's send-failure
// path, reached only when Registry.Send or workerSender.Send returns an error -
// and on every one of those three error values the DispatchTask was never
// queued, never written to the stream and never seen by the agent. PREPARING is
// sent by a Runner that exists only because the agent received a DispatchTask,
// so at this caller's own (epoch, worker) pair the task cannot be preparing, for
// exactly the reason it cannot be running. Widening a statement that ENDS AN
// ASSIGNMENT for symmetry is the fail-open direction.
```

- [ ] **Step 2: Run it - PASS, and label it**

```bash
go test -tags integration -p 1 ./internal/store/ -run TestRequeueTask_APreparingTaskIsNotRequeuedByTheSendFailurePath -count=1 -v -timeout 600s
```

Expected: `--- PASS`. Record that it passed on first write.

- [ ] **Step 3: Mutate `RequeueTask` and require the RED**

Copy `tasks.sql` and `tasks.sql.go` to `$SCRATCH` first. Change line 497 to `AND status IN ('dispatched', 'preparing')`, run `make generate` plus the revert, then:

```bash
go test -tags integration -p 1 ./internal/store/ -run TestRequeueTask_ -count=1 -v -timeout 600s
```

Expected: `TestRequeueTask_APreparingTaskIsNotRequeuedByTheSendFailurePath` `--- FAIL`. Trace which assertion failed and confirm it is the rowcount one - a mutation may redden a test for a different guard.

Restore from the copies, re-run, expect `--- PASS`.

- [ ] **Step 4: Commit**

```bash
git add internal/store/requeue_task_fence_integration_test.go
git commit -m "test(store): pin that RequeueTask does not requeue a preparing task

Green on first write and labelled as such: a regression guard against a later
harmonization with RequeueTaskByID, whose discriminating power comes from the
widening mutation rather than from a red-first run."
```

---

## Task 11: The watchdog's execution arm stays blind to a `preparing` row

The spec proposes reddening this by the Task 8 `started_at` mutation. That works **only** if the test also proves the row is inside the scanned partition - otherwise the status predicate excludes it and the test passes under the mutation too, proving nothing. This task therefore carries an absolute-arm positive control in the same test, and it must run **after** Task 9 has widened `ListOverdueAssignedTasks`.

**Files:**
- Create: `internal/worker/handler_preparing_watchdog_integration_test.go`

- [ ] **Step 1: Write the test with its positive control**

```go
//go:build integration

package worker_test

// TestWatchdog_APreparingTaskIsInvisibleToTheExecutionArm pins the whole point
// of not stamping started_at at `preparing`, end to end: the row is produced by
// the REAL handler from a REAL PREPARING message, not planted.
//
// IT CANNOT BE RED AT HEAD, because a preparing row is not representable there.
// Its RED is supplied by a named mutation - handler.go's
// `if statusStr == "running"` widened to `|| statusStr == "preparing"`, the
// fork's regression - and that mutation must be run and recorded, or this test
// is a shape and not a proof.
//
// THE ABSOLUTE-ARM LEG IS NOT DECORATION AND MUST NOT BE DELETED. Without it,
// this test is satisfied by ListOverdueAssignedTasks' STATUS predicate excluding
// `preparing` entirely - which is the very regression Task 9 exists to prevent -
// and would then stay green under the started_at mutation as well. The leg is
// what proves the row is inside the scanned partition, so that the execution
// arm's silence is a statement about started_at rather than about membership.
func TestWatchdog_APreparingTaskIsInvisibleToTheExecutionArm(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "preparing-exec-arm", 0)
	// A one-second timeout: about as hostile to the execution arm as a task can be.
	_, err := pool.Exec(ctx, `UPDATE tasks SET timeout_seconds = 1 WHERE id = $1`, taskID)
	require.NoError(t, err)

	assignedAt := time.Now().Add(-30 * time.Hour)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
		AssignedAt: pgtype.Timestamptz{Time: assignedAt, Valid: true},
	})
	require.NoError(t, err)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_PREPARING,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	row, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "preparing", row.Status, "precondition: the handler moved the row")

	// The scan's clock is bound as a parameter, so this test owns it: two days
	// past the claim, with a ZERO margin. Nothing about elapsed time can excuse a
	// row from the execution arm here; only started_at IS NOT NULL can.
	execOnly := store.ListOverdueAssignedTasksParams{
		AbsoluteEnabled: false,
		ExecEnabled:     true,
		Now:             pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		MarginSeconds:   0,
		MaxRows:         100,
	}
	got, err := q.ListOverdueAssignedTasks(ctx, execOnly)
	require.NoError(t, err)
	for _, r := range got {
		assert.NotEqual(t, taskID, r.ID,
			"a preparing task must be invisible to the EXECUTION arm at any margin, because a "+
				"workspace sync legitimately outruns any timeout_sec the task carries. Stamping "+
				"started_at at the preparing transition is what breaks this, and it is the fork's "+
				"regression")
	}

	// POSITIVE CONTROL, and the reason this test is not vacuous: the same row IS
	// in the scanned partition, and the ABSOLUTE arm does reach it.
	absOnly := execOnly
	absOnly.ExecEnabled = false
	absOnly.AbsoluteEnabled = true
	absOnly.AbsoluteCutoff = pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true}
	abs, err := q.ListOverdueAssignedTasks(ctx, absOnly)
	require.NoError(t, err)
	var found bool
	for _, r := range abs {
		if r.ID == taskID {
			found = true
		}
	}
	require.True(t, found,
		"the absolute arm MUST still sweep a preparing task. This leg is what makes the assertion "+
			"above a statement about started_at rather than about the status predicate: without it, "+
			"a ListOverdueAssignedTasks that excluded `preparing` entirely would satisfy this test "+
			"while silently reopening the unbounded-assignment hole for the whole workspace sync")
}
```

- [ ] **Step 2: Run - GREEN on first write, and say so**

```bash
go test -tags integration -p 1 ./internal/worker/ -run TestWatchdog_APreparingTaskIsInvisibleToTheExecutionArm -count=1 -v -timeout 900s
```

Expected: `--- PASS`. Record that it passed on first write, and that the mutation below is what makes it a proof.

- [ ] **Step 3: Re-apply the `started_at` mutation and require the RED**

```bash
cp internal/worker/handler.go "$SCRATCH/handler.go.bak2"
```

Change `if statusStr == "running" {` to `if statusStr == "running" || statusStr == "preparing" {` and run:

```bash
go test -tags integration -p 1 ./internal/worker/ -run TestWatchdog_APreparingTaskIsInvisibleToTheExecutionArm -count=1 -v -timeout 900s
```

Expected: `--- FAIL` at the `assert.NotEqual` inside the execution-arm loop. The positive control must still pass in the same run - if the control also fails, the mutation broke something else and the RED does not name this guard.

- [ ] **Step 4: Restore and re-run the control**

```bash
cp "$SCRATCH/handler.go.bak2" internal/worker/handler.go
go test -tags integration -p 1 ./internal/worker/ -run TestWatchdog_APreparingTaskIsInvisibleToTheExecutionArm -count=1 -v -timeout 900s
```

Expected: `--- PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler_preparing_watchdog_integration_test.go
git commit -m "test(worker): the execution arm cannot see a preparing task, end to end

Green on first write - a preparing row is not representable at HEAD, so this
property has no red-first run. Its RED is the named mutation (started_at stamped
at preparing), which was run and reddens the execution-arm assertion.

The absolute-arm leg in the same test is load-bearing: without it the assertion is
satisfied by ListOverdueAssignedTasks excluding preparing from its partition
altogether, which is the opposite regression."
```

---

## Task 12: The cancel handler signals a `preparing` task's agent

**Files:**
- Modify: `internal/api/jobs.go:918`
- Modify: `internal/api/jobs_cancel_test.go`

This is preservation, not a new capability: at HEAD a syncing task's row is `dispatched`, so it is collected today. It breaks the moment the row stops being `dispatched`.

- [ ] **Step 1: Write the failing test**

Add to `internal/api/jobs_cancel_test.go`. `seedRunningTask` (line 98) drives the row to `running` through `UpdateTaskStatusEpoch`; write a sibling that stops at `preparing` the same way.

```go
// seedPreparingTask is seedRunningTask with the row left at `preparing` - the
// state a source-bearing task holds for the whole workspace sync.
func seedPreparingTask(t *testing.T, env *cancelTestEnv, userID pgtype.UUID) string { /* ... */ }

// TestCancelJob_SendsACancelSignalForAPreparingTask. Its RED is the Go filter in
// internal/api/jobs.go, NOT the SQL: run it only against a tree where
// CancelJobTasks already admits `preparing`, or it goes red for the wrong reason
// and proves nothing about the filter.
//
// The agent side needs no change: internal/agent/agent.go's handleCancel looks
// the runner up in a.runners, which is populated before Runner.Run sends
// PREPARING, so r.Cancel(force) cancels the context provider.Prepare is running
// under.
func TestCancelJob_SendsACancelSignalForAPreparingTask(t *testing.T) {
	env := newCancelTestServer(t)
	user := createTestUser(t, env.q, "Prep", "cancel-preparing@example.com", false)
	token := createTestToken(t, env.q, user.ID)
	jobID := seedPreparingTask(t, env, user.ID)

	req := httptest.NewRequest(http.MethodDelete, "/v1/jobs/"+jobID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	msgs := env.cs.snapshot()
	require.Len(t, msgs, 1,
		"a preparing task's agent must be told to stop. Without it the task is failed in the "+
			"database and its p4 sync keeps running against the workspace, orphaned")
	require.NotNil(t, msgs[0].GetCancelTask())
}
```

- [ ] **Step 2: Run and record the RED**

```bash
go test -tags integration -p 1 ./internal/api/ -run TestCancelJob_SendsACancelSignalForAPreparingTask -count=1 -v -timeout 1800s
```

Expected: `--- FAIL` at `require.Len(t, msgs, 1)` with `actual: 0`.

- [ ] **Step 3: Widen the filter**

`internal/api/jobs.go:918`:

```go
		if (t.Status == "running" || t.Status == "dispatched" || t.Status == "preparing") && t.WorkerID.Valid {
```

Update the two comments above it (lines 908 and 914) from "running/dispatched" to name the assignment partition rather than listing it, so the next status added does not leave a stale enumeration behind.

- [ ] **Step 4: Run - GREEN**

```bash
go test -tags integration -p 1 ./internal/api/ -run TestCancelJob -count=1 -v -timeout 1800s
```

- [ ] **Step 5: Commit**

```bash
git add internal/api/jobs.go internal/api/jobs_cancel_test.go
git commit -m "fix(api): cancel a preparing task's agent, not just its row

Preservation, not a new capability: at HEAD a syncing task's row is dispatched
and is collected today. Its RED depends on CancelJobTasks already admitting
preparing, so this lands after that widening."
```

---

## Task 13 (frontend): the `TaskStatus` union and the colour arm

**Files:**
- Modify: `web/src/jobs/api.ts:153`
- Modify: `web/src/jobs/taskStatus.ts:8-26`
- Modify: `web/src/jobs/taskStatus.test.ts:4,13`

- [ ] **Step 1: Write the failing test**

In `web/src/jobs/taskStatus.test.ts`, rename the first test - **delete the cardinal rather than incrementing it**, because "seven" is false at the next member and no check reddens a name - and add the assertion:

```ts
test('maps every task status to a dot class', () => {
  expect(taskStatusColor('done').dot).toBe('bg-ok')
  expect(taskStatusColor('running').dot).toBe('bg-accent')
  expect(taskStatusColor('dispatched').dot).toBe('bg-accent')
  expect(taskStatusColor('preparing').dot).toBe('bg-accent')
  expect(taskStatusColor('pending').dot).toBe('bg-warn')
  expect(taskStatusColor('failed').dot).toBe('bg-err')
  expect(taskStatusColor('timed_out').dot).toBe('bg-err')
})

test('covers dispatched, preparing and timed_out (the statuses status.ts lacks)', () => {
  expect(taskStatusColor('dispatched').text).toBe('text-accent')
  expect(taskStatusColor('preparing').text).toBe('text-accent')
  expect(taskStatusColor('timed_out').text).toBe('text-err')
})
```

And extend the terminal test with a line that is **green at HEAD and labelled as such**:

```ts
  // Green before the switch case is added and always will be: a regression guard
  // against a future edit that puts `preparing` in TERMINAL, not a red-first
  // criterion. Adding it there would make useJob stop tailing a task's log the
  // moment its workspace sync begins.
  expect(isTerminalTask('preparing')).toBe(false)
```

- [ ] **Step 2: Run and record the RED**

```bash
cd web && npx vitest run src/jobs/taskStatus.test.ts
```

Expected: two failures. The colour test fails first - `expected 'bg-fg-mute' to be 'bg-accent'`, because `taskStatusColor`'s `default:` arm returns the muted pair. TypeScript will also reject `'preparing'` as an argument, since the union does not contain it; depending on the vitest/esbuild setup that surfaces as a type error rather than an assertion failure. Either is the RED - record which you got.

The `default:` arm is why the union widening and the `switch` case **must land together**: the union alone changes nothing a user sees, and the compiler will not complain, because the `default` makes the switch non-exhaustive by construction.

- [ ] **Step 3: Widen the union and the switch**

`web/src/jobs/api.ts:153`:

```ts
export type TaskStatus = 'pending' | 'dispatched' | 'preparing' | 'running' | 'done' | 'failed' | 'timed_out'
```

`web/src/jobs/taskStatus.ts`, in `taskStatusColor`:

```ts
    case 'running':
    case 'dispatched':
    case 'preparing':
      return { text: 'text-accent', dot: 'bg-accent' }
```

and update the comment above the function from `running/dispatched=accent` to `running/dispatched/preparing=accent`.

- [ ] **Step 4: Run - GREEN, then the whole web suite**

```bash
cd web && npx vitest run src/jobs/taskStatus.test.ts
cd web && npx vitest run --reporter=dot
npx tsc --noEmit -p web/tsconfig.json
```

`web/src/workers/api.ts:163` and `web/src/jobs/dagLayout.ts:5` both consume `TaskStatus`; neither switches on its members, so both widen for free. `tsc` is what proves that.

- [ ] **Step 5: Commit**

`web/dist` is tracked but not maintained per-PR. Before staging: `git checkout -- web/dist/` if `make generate` or a build touched it.

```bash
git add web/src/jobs/api.ts web/src/jobs/taskStatus.ts web/src/jobs/taskStatus.test.ts
git commit -m "feat(web): render preparing as an accent status

The union and the switch case land together: taskStatusColor's default arm makes
the switch non-exhaustive by construction, so widening the union alone changes
nothing a user sees and the compiler does not complain."
```

---

## Task 14 (Python SDK): the enum member, and a test that pins the non-raising property

Spec finding F2 is confirmed: `Task.status` is `Optional[str]`, not `Optional[TaskStatus]` (`python/src/relay/models.py:227`), and the sibling `JobStatus` docstring states the policy verbatim (lines 53-58). An SDK built before this change parses `"preparing"` without complaint, so there is **no compatibility case**. The member is added for autocomplete only.

**Files:**
- Modify: `python/src/relay/models.py:67-78`
- Modify: `python/tests/unit/test_models.py`

- [ ] **Step 1: Add the member**

Between `DISPATCHED` and `RUNNING`:

```python
    PREPARING = "preparing"
```

Do **not** touch `QUEUED`, `BLOCKED` or `CANCELLED`. None of those is producible by the server (`tasks_status_check` admits seven values and none of the three is among them); removing them is `idea-2026-07-01-dead-status-vocabulary`'s subject, not this slice's.

- [ ] **Step 2: Add the test, labelled green-at-HEAD**

```python
def test_task_status_accepts_an_unknown_server_value():
    """Task.status is Optional[str], not Optional[TaskStatus].

    GREEN BEFORE THIS SLICE AND AFTER IT, deliberately. It is not a red-first
    criterion; it pins the typing choice against a future "tightening" to the
    enum type, which is the only way this SDK can break on a new server status -
    it would turn every unknown value into a parse error at every consumer at
    once. An old SDK against a new server is NOT the compatibility case here.
    """
    assert Task(name="t", status="preparing").status == "preparing"
    assert Task(name="t", status="a-status-from-the-future").status == "a-status-from-the-future"
    assert TaskStatus.PREPARING == "preparing"
```

- [ ] **Step 3: Run**

```bash
make python-test
make python-lint
```

Expected: PASS, and clean ruff/mypy.

- [ ] **Step 4: Commit**

```bash
git add python/src/relay/models.py python/tests/unit/test_models.py
git commit -m "feat(python): TaskStatus.PREPARING, plus a guard on the Optional[str] choice

Task.status is Optional[str], so an unknown value already parses and there is no
old-SDK compatibility case. The enum member is for autocomplete; the test pins
the property that makes that true, against a future tightening."
```

---

## Task 15: The prose sweep

Wrong prose about correct code is this repo's dominant defect class, and eleven passages name `preparing` as a **future** candidate. After this slice every one is a statement about the past written in the present tense. This is required scope, not cleanup.

**Files:**
- Modify: `internal/store/query/tasks.sql` (three passages)
- Modify: `internal/worker/taskstatus_fence_counters.go:221-226`
- Modify: `internal/worker/taskstatus_fence_counters_test.go` (`taskStatusUniverse`'s doc, lines 535-560)
- Modify: `internal/cli/logs.go:141-142` and `:295-308`
- Modify: `web/src/workers/WorkerTasksPanel.tsx:75-76`
- Modify: `README.md:280, 965, 1693`
- Modify: `CLAUDE.md` (the epoch-fence bullet, two passages)

(`internal/store/tasks_status_vocabulary_lockstep_test.go` was already rewritten in Task 5.)

- [ ] **Step 1: `internal/store/query/tasks.sql`, three passages**

- `AppendTaskLog`: "The day `preparing` becomes a persisted status and is not added here, every workspace-sync log line in the system disappears." becomes the present tense - `preparing` **is** in this arm, the hazard now belongs to the next non-terminal status, and `TASK_STATUS_PREPARE_FAILED` remains the opposite case.
- `ListOverdueAssignedTasks`: "a task spends the whole workspace sync as `dispatched` (handleTaskStatus has no case for TASK_STATUS_PREPARING, so the row does not move)" and "`preparing` is the live candidate." The sync is now `preparing`, it is in this partition, and the absolute arm is still its only bound. **Delete** the live-candidate sentence rather than re-pointing it at a hypothetical.
- `ListActiveTasksForWorkerPage`: "`preparing` is the live candidate." and, in the ORDER BY paragraph, "a task spends the whole workspace sync as `dispatched`". The ordering argument survives in substance - `started_at` is still NULL through the sync - only the noun changes.

- [ ] **Step 2: `taskStatusIsWritable`'s comment**

"A NEW NON-TERMINAL STATUS (`preparing` is the live candidate: TASK_STATUS_PREPARING is already in the proto) MUST BE ADDED HERE" - keep the rule, drop the example, or re-point it at `cancelled`, which is terminal and therefore the counter-example.

- [ ] **Step 3: `taskStatusUniverse`'s comment**

The paragraph justifying why the proto is a separate source from the vocabulary. The argument survives; its example is now historical, and `TASK_STATUS_PREPARE_FAILED` is the live instance of the same shape.

- [ ] **Step 4: `internal/cli/logs.go`, two passages**

- `taskIsTerminal` header (line 141): "`preparing` is harmless at both because it is non-terminal" - still true and no longer hypothetical. Keep, minus the future tense.
- `emitSnapshot` (lines 302-305): **the reason expires.** Once `CancelJobTasks` admits `preparing`, a cancel makes every such task `failed`, so this branch is no longer reachable by the cancel route. It stays reachable by the ordering route the same file documents two comments below, and by any future non-terminal status. Rewrite to name the ordering route as the live one. **Do not delete the branch.**

- [ ] **Step 5: `web/src/workers/WorkerTasksPanel.tsx:75-76`**

"A dispatched task spends the whole workspace sync with no started_at" becomes `preparing`. The `not started` rendering is unchanged and correct. **Watch the Tailwind scanner**: this file is under `web/`, so a class-shaped substring written into the comment emits CSS. Do not introduce one.

- [ ] **Step 6: README, three lines - and one that must NOT change**

| Line | Change |
|---|---|
| 279, `RELAY_TASK_WATCHDOG_MARGIN` | **No change.** "Applies only to tasks with `timeout_sec > 0` that have reported `running`" is the contract this design preserves. Editing it is the tell that `started_at` moved. |
| 280, `RELAY_TASK_MAX_ASSIGNMENT` | "a task spends its entire workspace sync in `dispatched`" becomes `preparing`; and in the same row "or one still syncing its workspace" gains "(in `preparing`)" so the two rows read as one story. |
| 965, worker delete | "only `dispatched`/`running` tasks are requeued" becomes `dispatched`/`preparing`/`running`. This is the prose half of the `CountTerminalTasksForWorker` / `RequeueWorkerTasks` pairing that `TestTasksStatusVocabularyIsExactly`'s comment calls the one gap the pairing cannot self-detect. |
| 1693, `GET /v1/workers/{id}/tasks` | "(`dispatched` or `running`)" gains `preparing`. A documented API contract, so a stale version here is a defect, not a typo. |

- [ ] **Step 7: CLAUDE.md, two passages in the epoch-fence bullet**

Both call `preparing` "the live candidate". The `AppendTaskLog` carve-out becomes a description of the current partition (`('pending','dispatched','preparing','running')`) with `PREPARE_FAILED` as the terminal counter-example; the `ListOverdueAssignedTasks` sentence takes the same treatment. **The rules themselves are unchanged.**

- [ ] **Step 8: NOT changed, on purpose**

`docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md` R3 says `handleTaskStatus` "has no case for `TASK_STATUS_PREPARING`". That is a record of the tree on 2026-08-20 and stays as written. Specs and retros are records of a moment; comments and README are live contracts. The temptation to "fix" the spec will be strong and doing so destroys the record that the decision was made against that tree.

- [ ] **Step 9: Post-edit checks on every touched document**

After any programmatic edit to a tracked text file:

```bash
git diff --stat            # proportionate to the intended change?
git ls-files --eol README.md CLAUDE.md internal/cli/logs.go internal/store/query/tasks.sql \
                        internal/worker/taskstatus_fence_counters.go \
                        web/src/workers/WorkerTasksPanel.tsx
python3 -c "import io,sys
for p in sys.argv[1:]:
    io.open(p, encoding='utf-8').read()
    print('utf-8 ok:', p)" README.md CLAUDE.md internal/cli/logs.go internal/store/query/tasks.sql
```

Every `git ls-files --eol` line must read `i/lf`. Every file must decode as UTF-8. Introduce no non-ASCII byte: a raw non-ASCII literal in a document is unverifiable by eye and survives every check this repo runs. Prefer exact-anchor replacement over a regex sweep, and print the before/after line counts of each edited file.

- [ ] **Step 10: Run everything the prose touches**

`internal/store/query/tasks.sql` changed, so `make generate` must run again if any comment inside a statement moved (sqlc embeds statement comments into the generated constants). Follow the same procedure and the same verification as Task 9 Steps 5-6.

```bash
make test
go test -tags integration -p 1 ./internal/store/... ./internal/worker/... ./internal/cli/... -count=1 -timeout 1800s
cd web && npx vitest run --reporter=dot ; cd ..
```

Also re-check the Tailwind scanner did not gain a rule from the `web/` comment edit:

```bash
cd web && npm run build && git diff --stat dist/ ; cd ..
git checkout -- web/dist/
```

- [ ] **Step 11: Commit**

```bash
git add README.md CLAUDE.md internal/cli/logs.go internal/store/query/tasks.sql \
        internal/store/tasks.sql.go internal/worker/taskstatus_fence_counters.go \
        internal/worker/taskstatus_fence_counters_test.go \
        web/src/workers/WorkerTasksPanel.tsx
git commit -m "docs: preparing is a persisted status, in the present tense

Eleven passages named it as a future candidate. Each is rewritten as a statement
about the current partition, with TASK_STATUS_PREPARE_FAILED taking over as the
live example of the opposite (terminal) shape. No cardinal is incremented; each
is deleted or replaced by a named guard.

README's RELAY_TASK_WATCHDOG_MARGIN row is deliberately unchanged: 'applies only
to tasks that have reported running' is the contract this slice preserves, and
editing it would be the tell that started_at moved. The watchdog spec stays as
written - it is a record of a moment."
```

---

## Task 16: Whole-slice verification

- [ ] **Step 1: Default lane**

```bash
make test
```

Expected: no `FAIL`. Runtime about 2 minutes.

- [ ] **Step 2: `vet` under both tag sets**

```bash
go vet ./...
go vet -tags integration ./...
```

- [ ] **Step 3: Integration, all four touched packages**

```bash
go test -tags integration -p 1 ./internal/store/... -count=1 -timeout 1200s
go test -tags integration -p 1 ./internal/worker/... -count=1 -timeout 1200s
go test -tags integration -p 1 ./internal/scheduler/... -count=1 -timeout 900s
go test -tags integration -p 1 ./internal/api/... -count=1 -timeout 1800s
```

`internal/api` takes about 9.5 minutes; anything under 1800s risks a timeout that reports FAIL with no `--- FAIL` beneath it.

- [ ] **Step 4: The CLI lane**

`internal/cli/logs.go`'s comments changed and `internal/cli/jobs.go` prints `t.Status` verbatim, so nothing there needed a code change - but the lane runs to confirm that.

```bash
make test-cli-integration
```

- [ ] **Step 5: Race detector, in the container**

The native Windows lane is unreliable and silently skips every `//go:build !windows` file. Use the container.

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

Expected: all packages `ok`, zero data races. **If the lane is genuinely unavailable, say so plainly rather than substituting `-count=N`** - repetition raises confidence in flakiness, not in race-freedom.

- [ ] **Step 6: Web and Python**

```bash
cd web && npx vitest run --reporter=dot ; cd ..
npx tsc --noEmit -p web/tsconfig.json
make python-test
make python-lint
```

- [ ] **Step 7: Line endings and encoding across the whole diff**

```bash
git diff --stat origin/main...HEAD
git diff --name-only origin/main...HEAD | xargs git ls-files --eol
```

Every entry must read `i/lf`. Any `i/-text` is a file git has reclassified as binary - almost certainly a `\r\r\n` from a programmatic edit - and must be fixed before the PR.

- [ ] **Step 8: Close the backlog item**

```
/backlog close feature-2026-09-03-preparing-task-status
```

Never hand-edit the item's `status` field. The command does the `git mv` into `docs/backlog/closed/`, stamps the frontmatter, appends the `## Resolution` note and commits.

---

## Acceptance criteria

Assessed from the spec's section 8, with the item's fourth criterion restated because it is false as written.

- All thirteen SQL predicates plus the partial index admit `preparing`; the eight terminal-side sites do not; `RequeueTask` does not.
- `handleTaskStatus` maps `TASK_STATUS_PREPARING` to `"preparing"` and stamps no `started_at`.
- The execution arm does not fire on a `preparing` row at any margin, **and the same row is proven to be inside the scanned partition in the same test**.
- The absolute arm still sweeps a `preparing` row past `RELAY_TASK_MAX_ASSIGNMENT`.
- `CancelJobTasks` fails a `preparing` row **and** its agent receives a `CancelTask`.
- `AppendTaskLog` accepts chunks for a `preparing` task, and the conjunction mutant reddens `TestHandleTaskLog_TrailingChunkJustAfterATerminalStatusIsStillStored`.
- `taskStatusIsWritable` equals the SQL allow-list, by both rungs, with both edit orders measured.
- `tasksStatusVocabulary` reads 000023's vocabulary, not 000019's, and fails closed if a future rewrite takes the first match.
- The 000023 down migration runs against a database containing a `preparing` row, and the re-up is clean.
- Every guard whose subject this slice changes went red first. **Three cannot, and each is labelled with the reason and, where a mutation can produce the red, the mutation is run and recorded**: `isTerminalTask('preparing') === false` (green at HEAD and always will be), the Python round-trip test (green by design; it pins the `Optional[str]` choice), and `TestWatchdog_APreparingTaskIsInvisibleToTheExecutionArm` (a `preparing` row is unrepresentable at HEAD; RED by the `started_at` mutation). `TestRequeueTask_APreparingTaskIsNotRequeuedByTheSendFailurePath` is a fourth, RED by the widening mutation.
- Every passage in the prose sweep is rewritten; no count is incremented - each is deleted or replaced by a named guard.

---

## Self-review notes

**Spec coverage.** Spec sections 4.1 (migration) -> Tasks 2,3,6; 4.2 (handler) -> Task 8; 4.3 (SQL + generate) -> Tasks 7,9; 4.4 (Go mirror) -> Task 7; 4.5 (api filter) -> Task 12; 4.6 (reconcile, no change needed) -> covered by Task 9's `GetActiveTasksForWorker` test; section 6 R1-R9 -> Tasks 3,4,5,7,8,9,10,11,13,14,15; section 7 -> Tasks 5 and 15; decisions D1 -> Task 10, D2 (no elapsed column) -> not implemented, correct, D3 -> Task 14, D4 -> Task 8, D5 -> Task 8, D6 -> Task 3, D7 -> Task 9 Step 4, D8 -> Task 7 Step 3, D9 -> Task 4, D10 -> Task 2.

**Deviations from the spec, each deliberate:**

1. **The spec's regex claim is wrong and the plan does not repeat it.** See "What was refuted" below.
2. **`make generate` runs more than once.** The spec's section 10 says once. The actual invariant is narrower and is stated in Task 9 Step 5: the revert works by `git checkout -- <file>`, which restores HEAD, so a second generate is safe exactly when the first one's output is already committed. Splitting the SQL into two passes is what buys eleven separate REDs.
3. **The execution-arm watchdog test carries an absolute-arm positive control and runs after the partition widening**, not before. Without the control it is satisfied by the status predicate excluding `preparing`, and it stays green under the very mutation the spec nominates as its RED.
4. **The down migration is written incomplete on purpose in Task 3** so that Task 6's test has a real RED rather than a mutation-supplied one.
