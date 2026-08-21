# Coordinator-side stale-task watchdog - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the coordinator its own bound on how long a task may hold an assignment, so a connected-but-hung agent can no longer keep a task non-terminal (and a job unfinished, and a worker slot consumed) forever.

**Architecture:** A new nullable `tasks.assigned_at` column records when an assignment began. A new read-only statement `ListOverdueAssignedTasks` returns assigned rows that have blown either of two bounds (execution: `now - started_at > timeout_seconds + margin`; absolute: `now - assigned_at > max assignment`). A new `scheduler.Watchdog` ticks every 60s, writes `timed_out` through the **existing** `UpdateTaskStatus` (fenced on the row's own epoch and worker id), runs the same terminal tail the dispatcher runs, then best-effort tells the agent to cancel. Zero new statements write `tasks.status`.

**Tech Stack:** Go 1.2x, pgx/v5 + sqlc, golang-migrate embedded migrations, testcontainers-go for integration tests, testify.

---

## Slice independence declaration

- **Backend only.** Nothing under `web/` changes. `taskResponse` (`internal/api/jobs.go:42-54`) is an explicit DTO that does not enumerate `store.Task`, so the new column does not reach the API surface and no frontend work is created.
- **Not parallelisable.** There is exactly one slice here and its tasks are strictly sequential (migration -> generated code -> statement -> watchdog -> wiring -> docs). Phase 3 dispatches **one** engineer (`relay-backend-engineer`). Do not fan out.
- **`TestTasksStatusVocabularyIsExactly` MUST gain the new site.** `ListOverdueAssignedTasks` hard-codes `status IN ('dispatched','running')`, which is a new decision-relevant partition. Task 10 covers it, including the inversion direction (this is the **second** site where omitting a new non-terminal status fails *open*).

---

## Verification of the spec against the tree at HEAD (`0fc1efc`)

The spec is a proposal. Everything load-bearing in it was re-checked. **Confirmed** claims are listed compactly; **refuted / corrected** claims changed this plan and are spelled out.

### Confirmed

| Spec claim | Evidence at HEAD |
|---|---|
| No timestamp bounds a `dispatched` row | `store.Task` (`internal/store/models.go:81-98`) has only `StartedAt`, `FinishedAt`, `CreatedAt`. `ClaimTaskForWorker` (`internal/store/query/tasks.sql:298-308`) writes `status`, `worker_id`, `assignment_epoch` and no timestamp. A new column is required. |
| Latest migration is `000020_list_endpoint_indexes` | Confirmed by directory listing; `000021` is free. |
| `UpdateTaskStatus` already carries all three fences and does not bump the epoch | `internal/store/query/tasks.sql:87-95`. |
| `failClaimedTask`'s tail is FailDependentTasks -> RecomputeJobStatus -> publish task -> publish job, and it does **not** call `NotifyTaskCompleted` | `internal/scheduler/dispatch.go:353-386`. The extraction in Task 5 must therefore leave `NotifyTaskCompleted` out of the helper. |
| `handleTaskStatus`'s terminal tail is the same plus `NotifyTaskCompleted` | `internal/worker/handler.go:788-813`. |
| `worker.Registry.Send` exists, is bounded, and `SendEvictCommand` is the exact shape to copy | `internal/worker/registry.go:50-66`; bound is `sendTimeout = 5 * time.Second` (`internal/worker/sender.go:21`). |
| `api.sendCancelSignals` fans out concurrently and has a timing test to model | `internal/api/cancel_signals.go:25-40`, `internal/api/cancel_signals_test.go:29-62`. |
| `internal/scheduler` already imports `api`, `worker`, `events`, `store` - no new edge, no cycle | `internal/scheduler/dispatch.go:11-19`. |
| `idx_tasks_worker_active` is `ON tasks(worker_id) WHERE status IN ('dispatched','running')` | `internal/store/migrations/000018_hot_path_indexes.up.sql:10-11`. |
| An existing dispatcher test covers the tail being extracted, so the mutation proof in Task 5 can actually kill something | `TestDispatcher_FailClaimedTask_PublishesJobEventOnTerminal` (`internal/scheduler/dispatch_test.go:532-597`) asserts `sawJob`, which is reachable only through `RecomputeJobStatus`. |
| `parseTrailingLogWindow` is the shape to copy for env parsing, and `TestTrailingLogWindowIsWiredIntoTheHandler` is the shape to copy for a wiring guard | `cmd/relay-server/main.go:312-356`, `cmd/relay-server/trailing_log_window_test.go:88-149`. |
| `sqlc.arg(x)::bool` in a boolean expression is already used and generates cleanly | `RetryJobTasks` / `SelectRetryableTaskIDs` (`internal/store/query/tasks.sql:460-461`). |

### Refuted or corrected

**C1. The spec's headline worry about `assigned_at` - "a requeued task that keeps a stale `assigned_at` would be swept the instant it is re-dispatched" - is FALSE, and the plan says so out loud so nobody re-derives it wrongly.** Enumerate every route into the scan's status allow-list (`dispatched`, `running`):

- `ClaimTaskForWorker` is the only statement that sets `status='dispatched'`, and after Task 2 it always overwrites `assigned_at`.
- `UpdateTaskStatus` can write `status='running'`, but only on a row whose `worker_id` matches a non-NULL bound value, i.e. a row a claim already produced - `assigned_at` is that claim's, which is correct, not stale.
- `UpdateTaskStatusEpoch` is TEST-ONLY and writes no `worker_id`; any row it moves has `worker_id IS NULL` and the scan's `worker_id IS NOT NULL` excludes it.

So the **only** load-bearing write is the claim-time one. Nulling on requeue is defence-in-depth and a readability property (`assigned_at IS NOT NULL` means "currently assigned"), not correctness. It is still mandatory in this plan - one line per statement, and the property is worth having - but do not report it to reviewers as the fix.

**C2. The spec's own nulling list is incomplete.** It names `RequeueTask`, `RequeueTaskByID`, `RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch`, `IncrementTaskRetryCount`, `RetryJobTasks` and omits **`CancelJobTasks`**, which also sets `worker_id = NULL` (`internal/store/query/tasks.sql:412-417`). Task 3 uses a mechanical rule instead of a list: **`assigned_at` is written exactly where `worker_id` is written, and nowhere else.** That is greppable (`rg 'worker_id\s*=' internal/store/query/tasks.sql`) and has no memorised exceptions.

**C3. The spec's `SweepInterval` naming would collide conceptually with `metrics.SweepInterval`.** Use `WatchdogSweepInterval` in `internal/scheduler` - the same package would otherwise carry a bare `SweepInterval` meaning something different from the one next door.

**C4. The spec leaves "does the scan still run when both arms are disabled?" open ("Pin whether the scan still runs").** Decided here: **no.** `SweepOnce` returns `nil` before touching the store when both arms are off. A guaranteed-empty query every 60s forever is pure cost. Task 6 pins it.

**C5. The spec's extraction of `finalizeTerminalTask` would silently change the dispatcher's log prefixes** (`"dispatch: FailDependentTasks..."` -> whatever the helper says). Since the extraction is gated on a zero-line test diff, a changed log string is invisible. Fixed here by giving the helper an explicit `logPrefix string` parameter, so `failClaimedTask` keeps emitting byte-identical lines.

**C6. `worker_test` already imports `relay/internal/scheduler`** (`internal/worker/handler_test.go:16`), so the end-to-end test in Task 9 needs no new dependency and raises no cycle question at all. The spec treated this as a thing to check.

**Not refuted, restated as a standing risk:** adding `AssignedAt` to `ClaimTaskForWorkerParams` does **not** break any existing call site, because every one uses a keyed struct literal. Omitting the field binds SQL NULL. That is fail-closed (the absolute arm simply never fires for that row) but it is silent. Task 2's dispatcher-level test exists specifically to catch the production omission; the test-fixture omissions are harmless and deliberate.

---

## File structure

**Create**

| File | Responsibility |
|---|---|
| `internal/store/migrations/000021_tasks_assigned_at.up.sql` | Add the column, backfill in-flight rows. |
| `internal/store/migrations/000021_tasks_assigned_at.down.sql` | Drop the column. |
| `internal/store/tasks_assigned_at_integration_test.go` | Migration round-trip + per-statement `assigned_at` write/clear contract. |
| `internal/store/list_overdue_assigned_tasks_integration_test.go` | The scan's cases. |
| `internal/scheduler/watchdog.go` | `Watchdog`, its two narrow interfaces, `Run`, `SweepOnce`, `overdueReason`, cancel fan-out. |
| `internal/scheduler/watchdog_test.go` | Unit tests with fakes and a frozen clock (no Docker; `package scheduler`). |
| `internal/scheduler/watchdog_integration_test.go` | Dispatcher-claims-then-watchdog-sweeps against a real DB. |
| `internal/worker/handler_watchdog_e2e_integration_test.go` | The item's headline criterion with a **connected** worker, plus the late-terminal-update no-op. |
| `internal/worker/registry_sendcancel_test.go` | `Registry.SendCancel` payload + not-connected behaviour. |
| `cmd/relay-server/watchdog_config.go` | `parseWatchdogDuration`. |
| `cmd/relay-server/watchdog_config_test.go` | Table test for parsing + AST wiring guard. |

**Modify**

| File | What |
|---|---|
| `internal/store/query/tasks.sql` | `ClaimTaskForWorker` (298-308) writes `assigned_at`; seven statements clear it; new `ListOverdueAssignedTasks`; doc-comment amendments on `UpdateTaskStatus` (12-95) and `RetryJobTasks` (464-573). |
| `internal/store/tasks.sql.go`, `internal/store/models.go` | **Generated only.** Never hand-edited. |
| `internal/scheduler/dispatch.go` | `sendTask` supplies `AssignedAt` (280-283); `failClaimedTask` (353-386) delegates its tail to `finalizeTerminalTask`. |
| `internal/worker/registry.go` | New `SendCancel` beside `SendEvictCommand` (60-66). |
| `cmd/relay-server/main.go` | Parse the two env vars and start the watchdog next to `go metrics.NewSweeper(...)` (214). |
| `internal/store/tasks_status_vocabulary_lockstep_test.go` | Name the new site; apply the count fix. |
| `CLAUDE.md` | One clause on the Epoch fence bullet. |
| `README.md` | Two config rows (after 277) + startup-sequence line (309). |

**Critical files** (read these before writing anything): `internal/store/query/tasks.sql` (the whole file, especially the `UpdateTaskStatus` and `RetryJobTasks` comment blocks), `internal/scheduler/dispatch.go`, `CLAUDE.md`'s Invariants section.

---

## The `assigned_at` decision table - READ BEFORE TASK 2

The rule: **`assigned_at` is written exactly where `worker_id` is written.** One line per statement, no exceptions to remember.

| Statement (`internal/store/query/tasks.sql`) | Writes `worker_id`? | `assigned_at` action | Load-bearing? |
|---|---|---|---|
| `ClaimTaskForWorker` (298) | sets it | **SET** from a Go-supplied parameter | **YES - the only one.** See C1. |
| `UpdateTaskStatus` (87) | no (fence only) | **untouched** | n/a - and it must stay untouched: a terminal transition deliberately keeps the assignment alive for the trailing-log flush, so clearing `assigned_at` here would be the same mistake as clearing `worker_id`. |
| `UpdateTaskStatusEpoch` (390, TEST-ONLY) | no | untouched | n/a |
| `IncrementTaskRetryCount` (139) | NULL | **NULL** | hygiene |
| `RequeueTask` (314) | NULL | **NULL** | hygiene |
| `RequeueTaskByID` (351) | NULL | **NULL** | hygiene |
| `RequeueWorkerTasks` (425) | NULL | **NULL** | hygiene |
| `RequeueWorkerTasksIfEpoch` (438) | NULL | **NULL** | hygiene |
| `CancelJobTasks` (412) | NULL | **NULL** | hygiene - **the one the spec forgot** (C2) |
| `RetryJobTasks` (556) | NULL | **NULL** | hygiene |
| `CreateTask` (1) / `CreateTaskWithSource` (377) | no | leave NULL (column default) | n/a |

"Hygiene" means: correctness does not depend on it (C1), but it makes `assigned_at IS NOT NULL` mean exactly "currently assigned". Do all of them; each is one line.

**Column shape:** `TIMESTAMPTZ NULL`, no default. Not `NOT NULL DEFAULT NOW()`: that would stamp a meaningless value on every never-claimed task, destroy the "IS NOT NULL means assigned" property, and make the nulling rule unrepresentable.

**Backfill `NOW()` is right.** The alternative (leave pre-existing in-flight rows NULL) is fail-closed but *permanently* so: the absolute arm requires `assigned_at IS NOT NULL`, so a row that is `dispatched` at deploy time and never reaches `running` would be exempt from both arms forever - which is precisely the case the column exists to cover. `NOW()` gives every in-flight assignment a fresh clock, so a deploy cannot sweep a fleet's worth of healthy long-running work in its first hours, and every row is eventually covered. It is the only DB-clock write of this column and it happens once, before any watchdog exists.

**No new index.** `idx_tasks_worker_active` is a partial index whose predicate (`status IN ('dispatched','running')`) *implies* the scan's status predicate, so the planner may satisfy the whole scan from it; the row set is bounded by the fleet's total slots (hundreds), read once per 60s. Do not add an index speculatively, and do not add an `EXPLAIN` test - at test-fixture scale the planner will seq-scan a ten-row table no matter what the index says, so such a test would assert nothing. **Named escalation if a production sweep is ever slow:** `CREATE INDEX idx_tasks_assigned_active ON tasks(assigned_at) WHERE status IN ('dispatched','running')`. Do not ship it now.

---

## `make generate` procedure - READ BEFORE TASKS 1, 2, 3, 4 AND 10

`sqlc` emits LF; this repo is CRLF. Every `make generate` therefore rewrites line endings across **all** generated files, burying the real change.

- [ ] Run `make generate` (from the worktree root: `D:/dev/relay/.claude/worktrees/stoic-cannon-15b269`).
- [ ] Run `git diff --ignore-all-space --stat` to find which files have a **content** change. For a `tasks` column change that is exactly `internal/store/models.go` and `internal/store/tasks.sql.go` (`internal/store/query/jobs.sql` only aggregates over `tasks`; it has no `tasks.*` projection, so `jobs.sql.go` must NOT appear).
- [ ] `git checkout -- <file>` every file whose `--ignore-all-space` diff is empty.
- [ ] **Then verify the regeneration actually survived.** This repo has silently discarded a regenerated `.sql.go` through this dance before, leaving a generated file whose doc comment contradicted its own source. Concretely, after each generate step, grep the generated file for the thing you just added and confirm it is there:
  - After Task 1: `rg "AssignedAt" internal/store/models.go`
  - After Task 2: `rg -A8 "type ClaimTaskForWorkerParams struct" internal/store/tasks.sql.go` must show `AssignedAt`.
  - After Task 4: `rg "func \(q \*Queries\) ListOverdueAssignedTasks" internal/store/tasks.sql.go`
  - After Task 10: `rg "coordinator watchdog" internal/store/tasks.sql.go` must find the amended `RetryJobTasks` / `UpdateTaskStatus` prose.
- [ ] Never hand-edit `*.sql.go` or `models.go`. If the content is wrong, fix the `.sql` and regenerate.

`make generate` also runs `buf generate`. No `.proto` changes here, so any `internal/proto/**` diff is line-endings only - revert it.

---

### Task 1: Migration 000021 adds `tasks.assigned_at`

**Files:**
- Create: `internal/store/migrations/000021_tasks_assigned_at.up.sql`
- Create: `internal/store/migrations/000021_tasks_assigned_at.down.sql`
- Create: `internal/store/tasks_assigned_at_integration_test.go`
- Generated: `internal/store/models.go`, `internal/store/tasks.sql.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/tasks_assigned_at_integration_test.go`:

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

// assignedAtDownTarget is the schema version just below 000021, i.e. the state
// its down migration restores.
const assignedAtDownTarget = 20

// TestMigration000021AddsAssignedAt asserts the column exists after the full up
// migration, is nullable, and is TIMESTAMPTZ.
func TestMigration000021AddsAssignedAt(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var dataType, isNullable string
	err := pool.QueryRow(ctx, `
		SELECT data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'tasks' AND column_name = 'assigned_at'`,
	).Scan(&dataType, &isNullable)
	require.NoError(t, err, "tasks.assigned_at must exist after migration 000021")
	assert.Equal(t, "timestamp with time zone", dataType)
	assert.Equal(t, "YES", isNullable,
		"assigned_at must be nullable: NULL is how a task says it holds no assignment")
}

// TestMigration000021DownUp confirms the down migration drops the column and
// migrating back up round-trips cleanly (no duplicate-column collision).
func TestMigration000021DownUp(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := context.Background()

	countColumn := func() int {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT count(*) FROM information_schema.columns
			WHERE table_schema='public' AND table_name='tasks' AND column_name='assigned_at'`,
		).Scan(&n))
		return n
	}

	require.NoError(t, store.MigrateTo(dsn, assignedAtDownTarget),
		"down migration to 000020 must succeed")
	assert.Equal(t, 0, countColumn(), "down must drop tasks.assigned_at")

	require.NoError(t, store.Migrate(dsn), "re-applying up must succeed")
	assert.Equal(t, 1, countColumn(), "up must restore tasks.assigned_at")
}
```

(`newTestPool` is `internal/store/testhelper_test.go:20`; `newMigratedPoolWithDSN` and `store.MigrateTo` are already used by `internal/store/hot_path_indexes_integration_test.go:73-77`.)

- [ ] **Step 2: Run the test to verify it fails**

```
go test -tags integration -p 1 ./internal/store/... -run TestMigration000021 -v -timeout 300s
```

Expected: FAIL - `no rows in result set` on the first test (the column does not exist), and `0 != 1` on the second.

- [ ] **Step 3: Write the migration**

`internal/store/migrations/000021_tasks_assigned_at.up.sql`:

```sql
-- assigned_at records when the CURRENT assignment began. It is the clock the
-- coordinator-side stale-task watchdog measures its absolute bound from
-- (internal/scheduler/watchdog.go, ListOverdueAssignedTasks).
--
-- Why a new column rather than an existing timestamp: tasks had exactly three,
-- and none of them bounds a `dispatched` row. started_at is written only on the
-- `running` transition, so it is NULL for a task still syncing its workspace;
-- finished_at is NULL until terminal; created_at is JOB SUBMISSION time, so a
-- task that queued six hours behind a busy fleet and was dispatched a minute ago
-- is six hours old by it. Keying the absolute bound on created_at would kill
-- healthy, just-dispatched work.
--
-- NULLABLE, no default. NULL means "holds no assignment". A NOT NULL DEFAULT
-- NOW() would stamp a meaningless value on every never-claimed task and destroy
-- that meaning. The column is written exactly where worker_id is written: set by
-- ClaimTaskForWorker from a Go-supplied parameter, nulled by every statement
-- that nulls worker_id, and untouched by UpdateTaskStatus (whose worker_id
-- argument is a fence, not a value).
ALTER TABLE tasks ADD COLUMN assigned_at TIMESTAMPTZ NULL;

-- Backfill with NOW() deliberately, so every assignment that is in flight at
-- deploy time gets a FRESH clock. Leaving these NULL would be fail-closed but
-- permanently so: the watchdog's absolute arm requires assigned_at IS NOT NULL,
-- so a row that is `dispatched` at deploy and never reaches `running` would be
-- exempt from both arms forever - exactly the case this column exists to cover.
-- This is the only database-clock write of assigned_at, and it happens once,
-- before any watchdog exists.
UPDATE tasks SET assigned_at = NOW() WHERE status IN ('dispatched', 'running');
```

`internal/store/migrations/000021_tasks_assigned_at.down.sql`:

```sql
ALTER TABLE tasks DROP COLUMN assigned_at;
```

- [ ] **Step 4: Regenerate and clean the CRLF noise**

Follow the `make generate` procedure section above verbatim. Confirm `AssignedAt pgtype.Timestamptz` with a `json:"assigned_at"` tag is now in `internal/store/models.go`'s `Task` struct and that every `SELECT *`-over-tasks scan in `internal/store/tasks.sql.go` gained an `&i.AssignedAt` line.

- [ ] **Step 5: Run the test to verify it passes**

```
go test -tags integration -p 1 ./internal/store/... -run TestMigration000021 -v -timeout 300s
go build ./... && go vet -tags integration ./...
```

Expected: PASS, and the whole module still builds (the new struct field breaks nothing - every construction of `store.Task` in the tree is a keyed literal or a scan).

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/000021_tasks_assigned_at.up.sql \
        internal/store/migrations/000021_tasks_assigned_at.down.sql \
        internal/store/tasks_assigned_at_integration_test.go \
        internal/store/models.go internal/store/tasks.sql.go
git commit -m "feat(store): add tasks.assigned_at (migration 000021)"
```

---

### Task 2: `ClaimTaskForWorker` stamps `assigned_at`, and the dispatcher supplies it

**Files:**
- Modify: `internal/store/query/tasks.sql:298-308`
- Modify: `internal/scheduler/dispatch.go:280-283`
- Create: `internal/scheduler/watchdog_integration_test.go`
- Generated: `internal/store/tasks.sql.go`

- [ ] **Step 1: Write the failing test**

This is a genuine behavioural RED that compiles at the current HEAD - no new symbol is referenced. Create `internal/scheduler/watchdog_integration_test.go`:

```go
//go:build integration

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDispatcher_ClaimStampsAssignedAt is the production guard for the one
// load-bearing write of tasks.assigned_at. ClaimTaskForWorkerParams gained a new
// field, and a keyed struct literal that omits it still COMPILES and binds SQL
// NULL - which silently exempts every claimed task from the watchdog's absolute
// arm forever, with no error anywhere. Nothing but this test fails if
// dispatch.go's call site stops passing it.
func TestDispatcher_ClaimStampsAssignedAt(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "assignedat@x.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "w", Hostname: "w", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	registry.Register(uuidStr(w.ID), &fakeSender{})

	before := time.Now()
	scheduler.NewDispatcher(q, registry, events.NewBroker()).RunOnce(ctx)

	got, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "dispatched", got.Status, "precondition: the task must have been claimed")
	require.True(t, got.AssignedAt.Valid,
		"a claimed task must carry assigned_at; a NULL here exempts it from the watchdog's absolute bound forever")
	assert.False(t, got.AssignedAt.Time.Before(before.Add(-time.Second)),
		"assigned_at must be the claim's own clock, not something older")
	assert.False(t, got.AssignedAt.Time.After(time.Now().Add(time.Second)),
		"assigned_at must not be in the future")
}
```

(`newTestStore`, `fakeSender` and `uuidStr` already exist in this package's test files: `internal/scheduler/dispatch_test.go:28-35,67-70,77`.)

- [ ] **Step 2: Run the test to verify it fails**

```
go test -tags integration -p 1 ./internal/scheduler/... -run TestDispatcher_ClaimStampsAssignedAt -v -timeout 300s
```

Expected: FAIL at `require.True(t, got.AssignedAt.Valid, ...)` - nothing writes the column yet.

- [ ] **Step 3: Write the minimal implementation**

In `internal/store/query/tasks.sql`, replace the `ClaimTaskForWorker` statement (keep the existing comment, add the new paragraph):

```sql
-- name: ClaimTaskForWorker :one
-- Atomically transition a pending task to 'dispatched' on the given worker.
-- Increments assignment_epoch so subsequent status updates from prior
-- generations can be rejected. Returns pgx.ErrNoRows if the task is no longer
-- pending (another dispatcher already claimed it, or the row vanished).
-- THIS IS THE ONLY LOAD-BEARING WRITE OF assigned_at. It is the sole route into
-- the ('dispatched','running') partition that ListOverdueAssignedTasks scans, so
-- a stale assigned_at left behind by a requeue can never be observed by the
-- watchdog: this statement overwrites it on the way back in. assigned_at is
-- supplied by the caller's Go clock, never NOW(), so it is directly comparable
-- with the watchdog's Go-computed cutoff (same argument as AppendTaskLog's
-- min_finished_at). A caller that omits the parameter binds SQL NULL, which
-- fails CLOSED - the row is simply never swept by the absolute arm - and is
-- caught for the production call site by TestDispatcher_ClaimStampsAssignedAt.
UPDATE tasks
SET status = 'dispatched',
    worker_id = sqlc.arg(worker_id),
    assigned_at = sqlc.arg(assigned_at),
    assignment_epoch = assignment_epoch + 1
WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;
```

Named args replace `$1`/`$2` so the emitted field names are stable and predictable; every existing caller uses a keyed literal (`ID:`, `WorkerID:`), so field reordering is harmless.

Then in `internal/scheduler/dispatch.go`, `sendTask` (line 280):

```go
	claimed, err := d.q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID:         task.ID,
		WorkerID:   w.ID,
		AssignedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
```

- [ ] **Step 4: Regenerate, then run the test to verify it passes**

Follow the `make generate` procedure. Then:

```
go test -tags integration -p 1 ./internal/scheduler/... -run TestDispatcher_ClaimStampsAssignedAt -v -timeout 300s
go vet -tags integration ./...
```

Expected: PASS. `vet -tags integration` must be clean - it is the only thing that compiles the integration-tagged files across every package.

- [ ] **Step 5: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go \
        internal/scheduler/dispatch.go internal/scheduler/watchdog_integration_test.go
git commit -m "feat(store): ClaimTaskForWorker stamps assigned_at from the dispatcher's clock"
```

---

### Task 3: Every statement that nulls `worker_id` also nulls `assigned_at`

**Files:**
- Modify: `internal/store/query/tasks.sql` - `IncrementTaskRetryCount` (139), `RequeueTask` (314), `RequeueTaskByID` (351), `CancelJobTasks` (412), `RequeueWorkerTasks` (425), `RequeueWorkerTasksIfEpoch` (438), `RetryJobTasks` (556)
- Modify: `internal/store/tasks_assigned_at_integration_test.go`
- Generated: `internal/store/tasks.sql.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/tasks_assigned_at_integration_test.go`, adding `"time"` and `"github.com/jackc/pgx/v5/pgtype"` to its imports:

```go
// assignedFixture is one job, one worker, and helpers that put a task into a
// state THROUGH THE PRODUCTION STATEMENTS - claim, then the fenced
// UpdateTaskStatus - so a planted row carries a real assignee, a real epoch and
// a real assigned_at.
type assignedFixture struct {
	q   *store.Queries
	ctx context.Context
	job store.Job
	w   store.Worker
}

func newAssignedFixture(t *testing.T) *assignedFixture {
	t.Helper()
	q := newTestQueries(t)
	ctx := context.Background()
	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "assigned-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	return &assignedFixture{q: q, ctx: ctx, job: job, w: w}
}

// claimedAt creates a task and claims it with the given assigned_at.
func (f *assignedFixture) claimedAt(t *testing.T, name string, at time.Time) store.Task {
	t.Helper()
	task, err := f.q.CreateTask(f.ctx, store.CreateTaskParams{
		JobID: f.job.ID, Name: name, Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), Retries: 1,
	})
	require.NoError(t, err)
	claimed, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: f.w.ID,
		AssignedAt: pgtype.Timestamptz{Time: at, Valid: true},
	})
	require.NoError(t, err)
	require.True(t, claimed.AssignedAt.Valid, "precondition: the claim stamps assigned_at")
	return claimed
}

func (f *assignedFixture) get(t *testing.T, id pgtype.UUID) store.Task {
	t.Helper()
	got, err := f.q.GetTask(f.ctx, id)
	require.NoError(t, err)
	return got
}

// TestAssignedAtIsClearedWhereverWorkerIDIs pins the rule this column lives by:
// assigned_at is written exactly where worker_id is written. Correctness does not
// depend on the clearing half (ClaimTaskForWorker is the only route back into the
// scanned partition and it always overwrites), but the property "assigned_at IS
// NOT NULL means currently assigned" does, and that is what the next reader will
// rely on. Every arm asserts BOTH columns so a statement that clears one and not
// the other is a failure, not silent drift.
func TestAssignedAtIsClearedWhereverWorkerIDIs(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)

	t.Run("RequeueTask", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "rq", old)
		require.NoError(t, f.q.RequeueTask(f.ctx, task.ID))
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "RequeueTask must clear assigned_at")
	})

	t.Run("RequeueTaskByID", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "rqid", old)
		require.NoError(t, f.q.RequeueTaskByID(f.ctx, task.ID))
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "RequeueTaskByID must clear assigned_at")
	})

	t.Run("RequeueWorkerTasks", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "rqw", old)
		ids, err := f.q.RequeueWorkerTasks(f.ctx, f.w.ID)
		require.NoError(t, err)
		require.Len(t, ids, 1)
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "RequeueWorkerTasks must clear assigned_at")
	})

	t.Run("RequeueWorkerTasksIfEpoch", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "rqwe", old)
		ids, err := f.q.RequeueWorkerTasksIfEpoch(f.ctx, store.RequeueWorkerTasksIfEpochParams{
			WorkerID: f.w.ID, ConnectionEpoch: f.w.ConnectionEpoch,
		})
		require.NoError(t, err)
		require.Len(t, ids, 1)
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "RequeueWorkerTasksIfEpoch must clear assigned_at")
	})

	t.Run("IncrementTaskRetryCount", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "retry", old)
		_, err := f.q.IncrementTaskRetryCount(f.ctx, store.IncrementTaskRetryCountParams{
			ID: task.ID, AssignmentEpoch: task.AssignmentEpoch, WorkerID: f.w.ID,
		})
		require.NoError(t, err)
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "IncrementTaskRetryCount must clear assigned_at")
	})

	t.Run("CancelJobTasks", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "cancel", old)
		require.NoError(t, f.q.CancelJobTasks(f.ctx, f.job.ID))
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid,
			"CancelJobTasks must clear assigned_at (the spec's own list omitted it)")
	})

	t.Run("RetryJobTasks", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "operator-retry", old)
		_, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
			ID: task.ID, Status: "failed", WorkerID: f.w.ID,
			AssignmentEpoch: task.AssignmentEpoch,
			FinishedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		require.NoError(t, err)
		reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
			JobID: f.job.ID, IncludeDone: false,
		})
		require.NoError(t, err)
		require.Len(t, reopened, 1)
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "RetryJobTasks must clear assigned_at")
	})

	t.Run("UpdateTaskStatus leaves assigned_at ALONE", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "terminal", old)
		_, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
			ID: task.ID, Status: "done", WorkerID: f.w.ID,
			AssignmentEpoch: task.AssignmentEpoch,
			FinishedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		require.NoError(t, err)
		after := f.get(t, task.ID)
		require.True(t, after.AssignedAt.Valid,
			"a terminal transition must NOT clear assigned_at: the assignment deliberately outlives the task "+
				"so trailing log chunks still pass AppendTaskLog's fence, and assigned_at is part of that record")
		assert.WithinDuration(t, old, after.AssignedAt.Time, time.Second)
		assert.True(t, after.WorkerID.Valid, "and it must not clear worker_id either")
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

```
go test -tags integration -p 1 ./internal/store/... -run TestAssignedAtIsClearedWhereverWorkerIDIs -v -timeout 600s
```

Expected: FAIL on all seven clearing sub-tests (`assigned_at` is still set); the `UpdateTaskStatus` sub-test passes already, which is the control.

- [ ] **Step 3: Write the minimal implementation**

In `internal/store/query/tasks.sql`, add exactly one line - `assigned_at = NULL,` - to each of the seven SET clauses, immediately after each statement's `worker_id = NULL,`. For `RetryJobTasks` the line goes in the aligned block as `    assigned_at      = NULL,`.

Add this once, to the `RequeueTaskByID` comment (it is the statement whose comment already explains the requeue contract):

```sql
-- assigned_at is nulled alongside worker_id. Every statement in this file that
-- nulls worker_id does the same, and ClaimTaskForWorker is the only statement
-- that sets either - so `assigned_at IS NOT NULL` means exactly "currently
-- assigned". The watchdog's correctness does not depend on this (the claim
-- overwrites on the way back in); the readable invariant does.
```

- [ ] **Step 4: Regenerate, then run the test to verify it passes**

Follow the `make generate` procedure, then:

```
go test -tags integration -p 1 ./internal/store/... -run TestAssignedAtIsClearedWhereverWorkerIDIs -v -timeout 600s
```

Expected: PASS, all eight sub-tests.

- [ ] **Step 5: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go \
        internal/store/tasks_assigned_at_integration_test.go
git commit -m "feat(store): clear assigned_at wherever worker_id is cleared"
```

---

### Task 4: `ListOverdueAssignedTasks`

**Files:**
- Modify: `internal/store/query/tasks.sql` (append after `CountActiveTasksByAllWorkers`, around line 375)
- Create: `internal/store/list_overdue_assigned_tasks_integration_test.go`
- Generated: `internal/store/tasks.sql.go`

**RED honesty note:** this statement does not exist at HEAD, so the RED is a compile failure, not an assertion failure. That is the honest RED for a new query and it does not violate the "a test seam must not destroy the RED" rule - no existing behaviour is being re-pointed at a new symbol. The genuinely discriminating REDs for this task are the mutation battery in Step 5.

- [ ] **Step 1: Write the failing test**

Create `internal/store/list_overdue_assigned_tasks_integration_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ts is pgtype.Timestamptz sugar.
func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func i32(v int32) *int32 { return &v }

// overdueFixture drives tasks into states through the production statements. It
// holds the pool as well as the queries because created_at is the one column no
// statement in the repo writes after insert, so backdating it needs raw SQL.
type overdueFixture struct {
	q    *store.Queries
	pool *pgxpool.Pool
	ctx  context.Context
	job  store.Job
	w    store.Worker
	now  time.Time
}

func newOverdueFixture(t *testing.T) *overdueFixture {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := context.Background()
	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "overdue-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	return &overdueFixture{q: q, pool: pool, ctx: ctx, job: job, w: w, now: time.Now()}
}

// backdateCreatedAt rewrites tasks.created_at directly - the only way to
// represent "this job was submitted a month ago".
func (f *overdueFixture) backdateCreatedAt(t *testing.T, id pgtype.UUID, at time.Time) {
	t.Helper()
	_, err := f.pool.Exec(f.ctx, `UPDATE tasks SET created_at = $2 WHERE id = $1`, id, at)
	require.NoError(t, err)
}

// dispatched creates a task with the given timeout and claims it at assignedAt.
// The row never reaches `running`: started_at stays NULL, which is the state a
// task sits in for the whole workspace sync.
func (f *overdueFixture) dispatched(t *testing.T, name string, timeoutSec *int32, assignedAt time.Time) store.Task {
	t.Helper()
	task, err := f.q.CreateTask(f.ctx, store.CreateTaskParams{
		JobID: f.job.ID, Name: name, Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), TimeoutSeconds: timeoutSec,
	})
	require.NoError(t, err)
	claimed, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: f.w.ID, AssignedAt: ts(assignedAt),
	})
	require.NoError(t, err)
	return claimed
}

// running additionally drives the row to `running` with the given started_at.
func (f *overdueFixture) running(t *testing.T, name string, timeoutSec *int32, assignedAt, startedAt time.Time) store.Task {
	t.Helper()
	claimed := f.dispatched(t, name, timeoutSec, assignedAt)
	updated, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: "running", WorkerID: f.w.ID,
		AssignmentEpoch: claimed.AssignmentEpoch, StartedAt: ts(startedAt),
	})
	require.NoError(t, err)
	return updated
}

// terminal drives a claimed row all the way to a terminal status.
func (f *overdueFixture) terminal(t *testing.T, name, status string, timeoutSec *int32, assignedAt, startedAt time.Time) store.Task {
	t.Helper()
	r := f.running(t, name, timeoutSec, assignedAt, startedAt)
	updated, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: r.ID, Status: status, WorkerID: f.w.ID,
		AssignmentEpoch: r.AssignmentEpoch, StartedAt: r.StartedAt,
		FinishedAt: ts(startedAt.Add(time.Minute)),
	})
	require.NoError(t, err)
	return updated
}

// bothArms is the ordinary production parameter set: 30m margin, 24h cap.
func (f *overdueFixture) bothArms() store.ListOverdueAssignedTasksParams {
	return store.ListOverdueAssignedTasksParams{
		AbsoluteEnabled: true,
		AbsoluteCutoff:  ts(f.now.Add(-24 * time.Hour)),
		ExecEnabled:     true,
		Now:             ts(f.now),
		MarginSeconds:   int64((30 * time.Minute) / time.Second),
	}
}

func (f *overdueFixture) list(t *testing.T, p store.ListOverdueAssignedTasksParams) map[pgtype.UUID]bool {
	t.Helper()
	rows, err := f.q.ListOverdueAssignedTasks(f.ctx, p)
	require.NoError(t, err)
	got := make(map[pgtype.UUID]bool, len(rows))
	for _, r := range rows {
		got[r.ID] = true
	}
	return got
}

// TestListOverdueAssignedTasks_ExecutionArm covers the timeout+margin bound.
func TestListOverdueAssignedTasks_ExecutionArm(t *testing.T) {
	f := newOverdueFixture(t)

	// 60s timeout, started 2h ago: 2h > 60s + 30m. Overdue.
	over := f.running(t, "over", i32(60), f.now.Add(-3*time.Hour), f.now.Add(-2*time.Hour))
	// 60s timeout, started 5m ago: 5m < 60s + 30m. Within bound - and this is the
	// long-running-but-healthy control the backlog item asks for.
	under := f.running(t, "under", i32(60), f.now.Add(-3*time.Hour), f.now.Add(-5*time.Minute))

	got := f.list(t, f.bothArms())
	assert.True(t, got[over.ID], "a task past timeout+margin must be returned")
	assert.False(t, got[under.ID], "a task inside timeout+margin must be left alone")
}

// TestListOverdueAssignedTasks_ActivityDoesNotCount proves the bound is on
// assignment age and NOT on last activity. A MAX(task_logs.created_at) liveness
// signal is tempting and wrong: it is agent-controlled, so a hung-but-chatty
// agent would look healthy forever.
func TestListOverdueAssignedTasks_ActivityDoesNotCount(t *testing.T) {
	f := newOverdueFixture(t)

	chatty := f.running(t, "chatty", i32(60), f.now.Add(-3*time.Hour), f.now.Add(-2*time.Hour))
	_, err := f.q.AppendTaskLog(f.ctx, store.AppendTaskLogParams{
		TaskID: chatty.ID, AssignmentEpoch: chatty.AssignmentEpoch, WorkerID: f.w.ID,
		Stream: "stdout", Content: "still here", MinFinishedAt: ts(f.now.Add(-15 * time.Minute)),
	})
	require.NoError(t, err, "precondition: the assignment still accepts logs")

	got := f.list(t, f.bothArms())
	assert.True(t, got[chatty.ID],
		"a task past its bound is overdue no matter how recently it wrote a log line")
}

// TestListOverdueAssignedTasks_AbsoluteArm covers timeout_sec = 0 and NULL, which
// have no execution bound at all, and the `dispatched` row that never reached
// `running` - the 2026-08-20 amendment's orphaned-assignment case.
func TestListOverdueAssignedTasks_AbsoluteArm(t *testing.T) {
	f := newOverdueFixture(t)

	zero := f.running(t, "zero-timeout", i32(0), f.now.Add(-30*time.Hour), f.now.Add(-29*time.Hour))
	null := f.running(t, "null-timeout", nil, f.now.Add(-30*time.Hour), f.now.Add(-29*time.Hour))
	// AMENDMENT CASE 1: a stale-epoch reconcile put this task into cancelIDs AND
	// marked it reported, so it was neither cancelled server-side nor requeued. It
	// is `dispatched`, worker_id set, epoch non-zero, started_at NULL - and
	// nothing but the absolute arm can ever see it.
	orphan := f.dispatched(t, "never-ran", i32(0), f.now.Add(-30*time.Hour))
	require.False(t, orphan.StartedAt.Valid, "precondition: the orphan never went running")
	require.True(t, orphan.WorkerID.Valid, "precondition: the orphan still names its worker")
	require.NotZero(t, orphan.AssignmentEpoch, "precondition: the orphan has a real epoch")
	fresh := f.dispatched(t, "fresh", i32(0), f.now.Add(-time.Minute))

	got := f.list(t, f.bothArms())
	assert.True(t, got[zero.ID], "timeout_sec=0 must still be bounded by the absolute cap")
	assert.True(t, got[null.ID], "a NULL timeout must still be bounded by the absolute cap")
	assert.True(t, got[orphan.ID], "a dispatched row that never ran must be recovered by the absolute arm")
	assert.False(t, got[fresh.ID], "a freshly dispatched row must be left alone")
}

// TestListOverdueAssignedTasks_KeysOnAssignedAtNotCreatedAt is the single row
// that pins the whole reason migration 000021 exists. Without it, a future editor
// "simplifying" the absolute arm to created_at breaks nothing visible.
func TestListOverdueAssignedTasks_KeysOnAssignedAtNotCreatedAt(t *testing.T) {
	f := newOverdueFixture(t)

	// Submitted 30 days ago, queued behind a busy fleet, dispatched one minute
	// ago. Perfectly healthy.
	task := f.dispatched(t, "long-queued", i32(0), f.now.Add(-time.Minute))
	f.backdateCreatedAt(t, task.ID, f.now.Add(-30*24*time.Hour))

	got := f.list(t, f.bothArms())
	assert.False(t, got[task.ID],
		"the absolute bound is measured from assigned_at, never created_at: created_at is JOB SUBMISSION time, "+
			"so keying on it would kill healthy, just-dispatched work that queued behind a busy fleet")
}

// TestListOverdueAssignedTasks_StatusAndAssigneeGuards covers the two predicates
// that keep the scan inside the assigned partition.
func TestListOverdueAssignedTasks_StatusAndAssigneeGuards(t *testing.T) {
	f := newOverdueFixture(t)
	old, older := f.now.Add(-29*time.Hour), f.now.Add(-30*time.Hour)

	done := f.terminal(t, "done", "done", i32(60), older, old)
	failed := f.terminal(t, "failed", "failed", i32(60), older, old)
	timedOut := f.terminal(t, "timed-out", "timed_out", i32(60), older, old)
	// Never claimed: pending, worker_id NULL, epoch 0. created_at is backdated so
	// age is not what excludes it.
	unclaimed, err := f.q.CreateTask(f.ctx, store.CreateTaskParams{
		JobID: f.job.ID, Name: "unclaimed", Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), TimeoutSeconds: i32(60),
	})
	require.NoError(t, err)
	f.backdateCreatedAt(t, unclaimed.ID, older)

	got := f.list(t, f.bothArms())
	assert.False(t, got[done.ID], "a terminal row is never overdue, however old")
	assert.False(t, got[failed.ID], "a terminal row is never overdue, however old")
	assert.False(t, got[timedOut.ID], "a terminal row is never overdue, however old")
	assert.False(t, got[unclaimed.ID],
		"a row with a NULL worker_id can never be written by UpdateTaskStatus's plain `=`, so selecting it "+
			"would buy a guaranteed zero-row round trip every sweep")
}

// TestListOverdueAssignedTasks_ArmsAreIndependentlyDisablable proves each arm's
// enable flag switches off exactly its own arm.
func TestListOverdueAssignedTasks_ArmsAreIndependentlyDisablable(t *testing.T) {
	f := newOverdueFixture(t)

	execOnly := f.running(t, "exec-only", i32(60), f.now.Add(-time.Hour), f.now.Add(-50*time.Minute))
	absOnly := f.dispatched(t, "abs-only", i32(0), f.now.Add(-30*time.Hour))

	both := f.list(t, f.bothArms())
	require.True(t, both[execOnly.ID], "precondition")
	require.True(t, both[absOnly.ID], "precondition")

	p := f.bothArms()
	p.ExecEnabled = false
	got := f.list(t, p)
	assert.False(t, got[execOnly.ID], "disabling the execution arm must silence it")
	assert.True(t, got[absOnly.ID], "and must leave the absolute arm firing")

	p = f.bothArms()
	p.AbsoluteEnabled = false
	got = f.list(t, p)
	assert.True(t, got[execOnly.ID], "disabling the absolute arm must leave the execution arm firing")
	assert.False(t, got[absOnly.ID], "and must silence the absolute arm")
}
```

- [ ] **Step 2: Run the test to verify it fails**

```
go test -tags integration -p 1 ./internal/store/... -run TestListOverdueAssignedTasks -v -timeout 900s
```

Expected: FAIL to compile - `undefined: store.ListOverdueAssignedTasksParams`, `q.ListOverdueAssignedTasks undefined`.

- [ ] **Step 3: Write the minimal implementation**

Append to `internal/store/query/tasks.sql`, after `CountActiveTasksByAllWorkers`:

```sql
-- name: ListOverdueAssignedTasks :many
-- The coordinator-side stale-task watchdog's scan (internal/scheduler/watchdog.go).
-- Returns every ASSIGNED task that has blown one of two independent bounds. It
-- is READ-ONLY: the watchdog writes through UpdateTaskStatus, so this slice adds
-- no new writer of tasks.status and no new status partition on a write path.
--
-- THE PARTITION IS ('dispatched','running'), i.e. "currently assigned" - the
-- same set GetActiveTasksForWorker, CountActiveTasksByAllWorkers,
-- ListGraceCandidates, RequeueWorkerTasks(IfEpoch) and idx_tasks_worker_active
-- already use. It is deliberately NOT `status = 'running'`: a task spends the
-- whole workspace sync as `dispatched` (handleTaskStatus has no case for
-- TASK_STATUS_PREPARING, so the row does not move), and a stale-epoch reconcile
-- can strand a `dispatched` row whose worker was told to abandon it. Keying on
-- `running` would miss both.
--
-- READ THIS ALLOW-LIST BACKWARDS, exactly like AppendTaskLog's first arm and
-- unlike every other status predicate in this file. A new NON-TERMINAL status
-- omitted here is NEVER SWEPT, which silently reopens the unbounded-assignment
-- hole this statement exists to close, for that status - no error, no log line.
-- `preparing` is the live candidate. A new TERMINAL status must stay OUT.
-- TestTasksStatusVocabularyIsExactly names this site.
--
-- worker_id IS NOT NULL is not decoration. UpdateTaskStatus's worker predicate is
-- a plain `=`, so a row with a NULL worker_id can never be written by it;
-- selecting such a row would buy a guaranteed zero-row round trip every sweep. It
-- also documents the one state this watchdog cannot recover - a `dispatched` row
-- whose worker_id was nulled by workers' ON DELETE SET NULL - which is
-- unreachable today, because nothing in this repo DELETEs a worker.
--
-- EVERY ARM FAILS CLOSED ON A MISSING VALUE. A NULL assigned_at, a NULL
-- started_at, and a NULL or zero timeout_seconds each make their arm FALSE
-- rather than true, and the row is left alone. Do NOT "fix" any of them into
-- `IS NULL OR ...`: that is the fail-OPEN direction and it would let the
-- watchdog kill work it knows nothing about. Same rule as AppendTaskLog's
-- second arm.
--
-- Both cutoffs are computed in Go and bound as parameters, never NOW() -
-- interval. started_at is written by a relay-server Go clock and by nothing
-- else (handleTaskStatus); assigned_at is written by a relay-server Go clock and
-- by nothing else except migration 000021's one-time backfill. Cross-replica
-- that is app-vs-app NTP skew (milliseconds) against bounds measured in hours,
-- whereas NOW() would put app-vs-database skew on every comparison.
--
-- Each arm carries its own explicit _enabled bool rather than encoding "off" as
-- a sentinel cutoff; `0` in either env var means "this arm is off".
-- EXTRACT(EPOCH FROM ...) is preferred over make_interval so only a timestamptz
-- and a bigint are bound - sqlc's handling of interval parameters is exactly the
-- kind of thing that emits a surprising Go type.
SELECT * FROM tasks
WHERE status IN ('dispatched', 'running')
  AND worker_id IS NOT NULL
  AND (
        ( sqlc.arg(absolute_enabled)::bool
          AND assigned_at IS NOT NULL
          AND assigned_at < sqlc.arg(absolute_cutoff)::timestamptz )
     OR ( sqlc.arg(exec_enabled)::bool
          AND started_at IS NOT NULL
          AND timeout_seconds IS NOT NULL AND timeout_seconds > 0
          AND EXTRACT(EPOCH FROM (sqlc.arg(now)::timestamptz - started_at))
              > timeout_seconds + sqlc.arg(margin_seconds)::bigint )
      )
ORDER BY assigned_at NULLS LAST, id;
```

- [ ] **Step 4: Regenerate, reconcile the param names, and run the test**

Follow the `make generate` procedure. Then **read the emitted struct**:

```
rg -A8 "type ListOverdueAssignedTasksParams struct" internal/store/tasks.sql.go
```

The field names used above (`AbsoluteEnabled`, `AbsoluteCutoff`, `ExecEnabled`, `Now`, `MarginSeconds`) are predictions from sqlc's snake-to-Camel rule. If the emitted names differ, **fix the test and the later Go call sites to match what was emitted** - do not hand-edit the generated file. Then:

```
go test -tags integration -p 1 ./internal/store/... -run TestListOverdueAssignedTasks -v -timeout 900s
```

Expected: PASS, all six tests.

- [ ] **Step 5: Run the mutation battery (each mutation must redden a test that stays in the tree)**

Apply each mutation to `internal/store/query/tasks.sql`, run `make generate`, run the tests, confirm RED, then revert the mutation and regenerate. **Do this in a detached worktree if any sibling agent is reading this tree.**

| Mutation | Must redden |
|---|---|
| `assigned_at < ...` -> `created_at < ...` in the absolute arm | `..._KeysOnAssignedAtNotCreatedAt` |
| Delete `AND worker_id IS NOT NULL` | `..._StatusAndAssigneeGuards` (the `unclaimed` row) |
| Delete `AND timeout_seconds > 0` | `..._AbsoluteArm` - if that run is green, re-run with `AbsoluteEnabled: false` so only the execution arm can return `zero`, and add that variant permanently |
| Widen the status list to include `'done'` | `..._StatusAndAssigneeGuards` |
| `sqlc.arg(exec_enabled)::bool` -> `true` | `..._ArmsAreIndependentlyDisablable` |

If any mutation leaves the suite green, the gate is decorative - add the discriminating input to a permanent test before continuing.

- [ ] **Step 6: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go \
        internal/store/list_overdue_assigned_tasks_integration_test.go
git commit -m "feat(store): ListOverdueAssignedTasks scans assigned tasks past either bound"
```

---

### Task 5: Extract `finalizeTerminalTask` from `Dispatcher.failClaimedTask`

Pure refactor. **Gate: no file under `internal/scheduler/*_test.go` may change in this task.** A test that needs adjusting *is* the finding - stop and report it.

**Files:**
- Modify: `internal/scheduler/dispatch.go:353-386`

- [ ] **Step 1: Record the gate baseline**

```
git status --porcelain internal/scheduler/
go test -tags integration -p 1 ./internal/scheduler/... -timeout 900s
```

Expected: clean tree, all green. Write down the pass count.

- [ ] **Step 2: Write the implementation**

In `internal/scheduler/dispatch.go`, add above `failClaimedTask`:

```go
// terminalTailStore is the subset of *store.Queries the shared terminal tail
// needs. *store.Queries satisfies it; the watchdog's own store interface embeds
// it so both callers reach the same code.
type terminalTailStore interface {
	FailDependentTasks(ctx context.Context, failedTaskID pgtype.UUID) error
	RecomputeJobStatus(ctx context.Context, id pgtype.UUID) (string, error)
}

// finalizeTerminalTask runs the tail every coordinator-side terminal writer
// shares: cascade to dependents, recompute the job, publish the task event, and
// publish a job event if the job itself went terminal. `task` must be the row
// UpdateTaskStatus RETURNED, not the row that was read before it - calling this
// for a write the fence rejected would cascade a failure the database refused.
//
// NotifyTaskCompleted is deliberately NOT here. Dispatcher.failClaimedTask has
// never called it and this extraction must not change that; the watchdog calls
// it itself.
//
// logPrefix keeps each caller's log lines byte-identical to what it emitted
// before the extraction.
func finalizeTerminalTask(
	ctx context.Context,
	q terminalTailStore,
	broker *events.Broker,
	logPrefix string,
	task store.Task,
	status string,
) {
	if err := q.FailDependentTasks(ctx, task.ID); err != nil {
		log.Printf("%s: FailDependentTasks for task %s: %v", logPrefix, uuidStr(task.ID), err)
	}
	jobStatus, err := q.RecomputeJobStatus(ctx, task.JobID)
	if err != nil {
		log.Printf("%s: RecomputeJobStatus for job %s: %v", logPrefix, uuidStr(task.JobID), err)
	}
	broker.Publish(events.Event{
		Type:  "task",
		JobID: uuidStr(task.JobID),
		Data:  []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, uuidStr(task.ID), status)),
	})
	if jobStatus == "done" || jobStatus == "failed" {
		broker.Publish(events.Event{
			Type:  "job",
			JobID: uuidStr(task.JobID),
			Data:  []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, uuidStr(task.JobID), jobStatus)),
		})
	}
}
```

Then replace the body of `failClaimedTask` after its `UpdateTaskStatus` error block (lines 367-385) with the single call:

```go
	finalizeTerminalTask(ctx, d.q, d.broker, "dispatch", updated, "failed")
```

Leave `failClaimedTask`'s own `log.Printf("dispatch: failing task %s terminally: %s", ...)`, its `UpdateTaskStatus` call, its error log and its whole doc comment exactly as they are.

- [ ] **Step 3: Verify the gate and re-run**

```
git diff --stat internal/scheduler/
go test -tags integration -p 1 ./internal/scheduler/... -timeout 900s
```

Expected: `git diff --stat` names `dispatch.go` and **nothing else**; same pass count as Step 1.

- [ ] **Step 4: Prove the gate is not decorative (mutation)**

Delete the `RecomputeJobStatus` call from `finalizeTerminalTask` (replace with `jobStatus := ""`), then:

```
go test -tags integration -p 1 ./internal/scheduler/... -run TestDispatcher_FailClaimedTask_PublishesJobEventOnTerminal -v -timeout 300s
```

Expected: FAIL on `sawJob` - the job event is unreachable without the recompute. Revert the mutation and re-run to confirm GREEN. A zero-line test diff alone proves nothing; this is what makes it load-bearing.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/dispatch.go
git commit -m "refactor(scheduler): extract finalizeTerminalTask from failClaimedTask"
```

---

### Task 6: The `Watchdog` type and its write path (no cancel yet)

**Files:**
- Create: `internal/scheduler/watchdog.go`
- Create: `internal/scheduler/watchdog_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/scheduler/watchdog_test.go` (note: `package scheduler`, no build tag - these are unit tests with fakes, exactly like `select_worker_test.go`):

```go
package scheduler

// Unit tests for the stale-task watchdog. No Docker, no database: the store and
// the canceller are narrow interfaces and the clock is injected, which is the
// whole reason they are interfaces.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWatchdogStore records every call and lets a test script the outcome of
// UpdateTaskStatus per task id.
type fakeWatchdogStore struct {
	mu sync.Mutex

	listCalls  int
	listParams []store.ListOverdueAssignedTasksParams
	overdue    []store.Task

	updates    []store.UpdateTaskStatusParams
	updateErr  map[pgtype.UUID]error
	cascaded   []pgtype.UUID
	recomputed []pgtype.UUID
	notifies   int

	events []string // append-only trace of "update:<id>" / "cancel:<id>"
}

func (f *fakeWatchdogStore) ListOverdueAssignedTasks(_ context.Context, p store.ListOverdueAssignedTasksParams) ([]store.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	f.listParams = append(f.listParams, p)
	return f.overdue, nil
}

func (f *fakeWatchdogStore) UpdateTaskStatus(_ context.Context, p store.UpdateTaskStatusParams) (store.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, p)
	f.events = append(f.events, "update:"+uuidStr(p.ID))
	if err, ok := f.updateErr[p.ID]; ok && err != nil {
		return store.Task{}, err
	}
	return store.Task{
		ID: p.ID, JobID: makeUUID(99), Status: p.Status, WorkerID: p.WorkerID,
		StartedAt: p.StartedAt, FinishedAt: p.FinishedAt, AssignmentEpoch: p.AssignmentEpoch,
	}, nil
}

func (f *fakeWatchdogStore) FailDependentTasks(_ context.Context, id pgtype.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cascaded = append(f.cascaded, id)
	return nil
}

func (f *fakeWatchdogStore) RecomputeJobStatus(_ context.Context, id pgtype.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recomputed = append(f.recomputed, id)
	return "failed", nil
}

func (f *fakeWatchdogStore) NotifyTaskCompleted(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notifies++
	return nil
}

func (f *fakeWatchdogStore) trace() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

// overdueRow builds an assigned task the scan would have returned.
func overdueRow(id byte, epoch int32, startedAt, assignedAt time.Time) store.Task {
	return store.Task{
		ID:              makeUUID(id),
		JobID:           makeUUID(99),
		Status:          "running",
		WorkerID:        makeUUID(200),
		AssignmentEpoch: epoch,
		StartedAt:       pgtype.Timestamptz{Time: startedAt, Valid: true},
		AssignedAt:      pgtype.Timestamptz{Time: assignedAt, Valid: true},
	}
}

type nopCanceller struct{}

func (nopCanceller) SendCancel(string, string, bool) error { return nil }

func newTestWatchdog(t *testing.T, q *fakeWatchdogStore, now time.Time) *Watchdog {
	t.Helper()
	w := NewWatchdog(q, nopCanceller{}, events.NewBroker(), 30*time.Minute, 24*time.Hour)
	w.now = func() time.Time { return now }
	return w
}

// TestWatchdog_WritesTimedOutWithTheRowsOwnFences is the headline write. The
// epoch and worker id must come from the row the scan returned: they are the
// TOCTOU guard between the scan and the write, and binding a zero value makes
// the statement match the wrong generation (epoch) or nothing at all (worker).
func TestWatchdog_WritesTimedOutWithTheRowsOwnFences(t *testing.T) {
	now := time.Now()
	started := now.Add(-2 * time.Hour)
	q := &fakeWatchdogStore{overdue: []store.Task{overdueRow(1, 7, started, now.Add(-3*time.Hour))}}
	w := newTestWatchdog(t, q, now)

	require.NoError(t, w.SweepOnce(context.Background()))

	require.Len(t, q.updates, 1)
	got := q.updates[0]
	assert.Equal(t, "timed_out", got.Status)
	assert.Equal(t, int32(7), got.AssignmentEpoch, "the write must bind the row's own epoch, never zero")
	assert.Equal(t, makeUUID(200), got.WorkerID, "the write must bind the row's own worker id, never a zero UUID")
	assert.Equal(t, started, got.StartedAt.Time, "started_at must be preserved unchanged")
	assert.Equal(t, now, got.FinishedAt.Time, "finished_at is the sweep's own clock")

	assert.Equal(t, []pgtype.UUID{makeUUID(1)}, q.cascaded)
	assert.Equal(t, []pgtype.UUID{makeUUID(99)}, q.recomputed)
	assert.Equal(t, 1, q.notifies, "a freed slot must wake the dispatchers")
}

// TestWatchdog_FenceRejectionIsASilentNoOp: pgx.ErrNoRows means somebody else got
// there first - the agent finished, a cancel landed, a grace expiry requeued, or
// a sibling replica swept it. Nothing downstream may run: a cascade on a write
// the database refused would fail the dependents of a task that is still live.
func TestWatchdog_FenceRejectionIsASilentNoOp(t *testing.T) {
	now := time.Now()
	row := overdueRow(1, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	q := &fakeWatchdogStore{
		overdue:   []store.Task{row},
		updateErr: map[pgtype.UUID]error{row.ID: pgx.ErrNoRows},
	}
	w := newTestWatchdog(t, q, now)

	require.NoError(t, w.SweepOnce(context.Background()))

	assert.Empty(t, q.cascaded, "a rejected write must not cascade")
	assert.Empty(t, q.recomputed, "a rejected write must not recompute the job")
	assert.Zero(t, q.notifies, "a rejected write must not wake the dispatcher")
}

// TestWatchdog_APoisonedFirstRowDoesNotStopTheSweep. THE POISONED ROW IS FIRST
// AND TWO HEALTHY ROWS FOLLOW IT, deliberately: with the bad row last, mutating
// the loop's `continue` to `break` is structurally undetectable.
func TestWatchdog_APoisonedFirstRowDoesNotStopTheSweep(t *testing.T) {
	now := time.Now()
	bad := overdueRow(1, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	good1 := overdueRow(2, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	good2 := overdueRow(3, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	q := &fakeWatchdogStore{
		overdue:   []store.Task{bad, good1, good2},
		updateErr: map[pgtype.UUID]error{bad.ID: errors.New("connection reset")},
	}
	w := newTestWatchdog(t, q, now)

	require.NoError(t, w.SweepOnce(context.Background()))

	require.Len(t, q.updates, 3, "every overdue row must be attempted; one bad row must not end the sweep")
	assert.Equal(t, []pgtype.UUID{good1.ID, good2.ID}, q.cascaded,
		"only the rows whose write actually matched may be finalized")
}

// TestWatchdog_BothArmsDisabledSkipsTheScanEntirely pins the decision that a
// fully disabled watchdog does not issue a guaranteed-empty query every tick.
func TestWatchdog_BothArmsDisabledSkipsTheScanEntirely(t *testing.T) {
	q := &fakeWatchdogStore{}
	w := NewWatchdog(q, nopCanceller{}, events.NewBroker(), 0, 0)

	require.NoError(t, w.SweepOnce(context.Background()))

	assert.Zero(t, q.listCalls, "with both arms off there is nothing to ask the database")
	assert.Empty(t, q.updates)
}

// TestWatchdog_ScanParametersDeriveFromTheConfiguredBounds proves the Go-side
// cutoffs are what reach the statement.
func TestWatchdog_ScanParametersDeriveFromTheConfiguredBounds(t *testing.T) {
	now := time.Now()
	q := &fakeWatchdogStore{}
	w := NewWatchdog(q, nopCanceller{}, events.NewBroker(), 90*time.Second, 6*time.Hour)
	w.now = func() time.Time { return now }

	require.NoError(t, w.SweepOnce(context.Background()))

	require.Len(t, q.listParams, 1)
	p := q.listParams[0]
	assert.True(t, p.ExecEnabled)
	assert.True(t, p.AbsoluteEnabled)
	assert.Equal(t, int64(90), p.MarginSeconds)
	assert.Equal(t, now.Add(-6*time.Hour), p.AbsoluteCutoff.Time,
		"the absolute cutoff must be an ABSOLUTE Go-computed instant, never NOW() - interval")
	assert.Equal(t, now, p.Now.Time)
}
```

`makeUUID` already exists in this package's tests (`internal/scheduler/select_worker_test.go:21-25`); `uuidStr` is in `dispatch.go:389`.

- [ ] **Step 2: Run the test to verify it fails**

```
go test ./internal/scheduler/... -run TestWatchdog -v -timeout 120s
```

Expected: FAIL to compile - `undefined: Watchdog`, `undefined: NewWatchdog`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/scheduler/watchdog.go`:

```go
package scheduler

import (
	"context"
	"errors"
	"log"
	"time"

	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// WatchdogSweepInterval is how often the Watchdog re-scans for overdue
// assignments. It is a constant, not a knob: it is an implementation cadence,
// not an operational timeout, and against hour-scale bounds a 60s tick
// contributes nothing to the bound's accuracy. Named for the watchdog rather
// than as a bare SweepInterval so it is never confused with
// metrics.SweepInterval, which means something different.
const WatchdogSweepInterval = 60 * time.Second

// DefaultWatchdogMargin is added to a task's own timeout_sec before the
// coordinator declares it timed out. It has to absorb the whole gap between the
// agent's deadline firing and the coordinator seeing the terminal update:
// subprocess kill, proctree cleanup, final log flush, and a gRPC reconnect if
// the stream dropped - which README's own analysis puts at roughly 105s. 30m is
// about seventeen times that, chosen generously because the failure direction of
// "too small" is killing healthy work.
const DefaultWatchdogMargin = 30 * time.Minute

// DefaultMaxAssignment is the absolute cap on how long a task may stay assigned,
// measured from dispatch. It must exceed the longest LEGITIMATE assignment,
// which is dominated by a P4 sync on a 1 TB+ workspace plus the task's own run.
const DefaultMaxAssignment = 24 * time.Hour

// watchdogStore is the subset of *store.Queries the Watchdog needs.
// *store.Queries satisfies it; tests supply a fake, which is what makes the
// whole sweep unit-testable without Docker.
type watchdogStore interface {
	terminalTailStore
	ListOverdueAssignedTasks(ctx context.Context, arg store.ListOverdueAssignedTasksParams) ([]store.Task, error)
	UpdateTaskStatus(ctx context.Context, arg store.UpdateTaskStatusParams) (store.Task, error)
	NotifyTaskCompleted(ctx context.Context) error
}

// taskCanceller is the subset of *worker.Registry the Watchdog needs.
type taskCanceller interface {
	SendCancel(workerID, taskID string, force bool) error
}

// Watchdog ends the assignment of a task that has been non-terminal for too
// long. It is the coordinator's own bound on task duration, and it exists
// because tasks.timeout_sec is otherwise enforced only by the agent - a timeout
// the agent is free not to honour is a suggestion, not a timeout.
//
// GRACE OWNS DISCONNECT; THE WATCHDOG OWNS DURATION. Two timers can fire on one
// row and the epoch fence is what makes that safe, in both orders:
//
//   - Watchdog first, grace second: the row is terminal, and grace's
//     RequeueWorkerTasksIfEpoch matches only ('dispatched','running'), so it
//     moves zero rows. Correct - the task was overdue whether or not its worker
//     later dropped.
//   - Grace first, watchdog second: the requeue set pending, worker_id NULL and
//     epoch N+1, so the watchdog's already-issued UpdateTaskStatus binds epoch N
//     and matches zero rows - on the epoch, first and independently of the other
//     two predicates. The requeue wins. Correct - an assignment that ended is
//     not the watchdog's to finish. The window is only between the scan and the
//     write; the scan itself would no longer return the row.
//   - Two replicas sweeping at once: first write wins, the second matches
//     nothing on the status allow-list. No leader election, no advisory lock.
//
// THE WATCHDOG IS DELIBERATELY REGISTRY-BLIND WHEN DECIDING TO WRITE. It never
// asks whether the worker is connected to THIS process: under multi-replica
// operation the agent may be connected to a different replica, so a local
// registry miss proves nothing, and the orphaned-`dispatched` case is precisely
// a row whose agent has been told to abandon it. The registry is consulted only
// to decide whether a cancel can be DELIVERED, which is why that send is
// best-effort by construction.
type Watchdog struct {
	q             watchdogStore
	canceller     taskCanceller
	broker        *events.Broker
	margin        time.Duration
	maxAssignment time.Duration
	now           func() time.Time // injectable clock; defaults to time.Now
}

// NewWatchdog constructs a Watchdog. A zero margin disables the execution arm
// and a zero maxAssignment disables the absolute arm; both zero disables the
// watchdog entirely, which is the documented escape hatch.
func NewWatchdog(q watchdogStore, c taskCanceller, broker *events.Broker, margin, maxAssignment time.Duration) *Watchdog {
	return &Watchdog{
		q: q, canceller: c, broker: broker,
		margin: margin, maxAssignment: maxAssignment,
		now: time.Now,
	}
}

// Run blocks until ctx is cancelled, sweeping every WatchdogSweepInterval.
func (w *Watchdog) Run(ctx context.Context) {
	t := time.NewTicker(WatchdogSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.SweepOnce(ctx); err != nil {
				log.Printf("watchdog: %v", err)
			}
		}
	}
}

// SweepOnce performs one pass over the overdue set. Exported for tests.
func (w *Watchdog) SweepOnce(ctx context.Context) error {
	execEnabled := w.margin > 0
	absoluteEnabled := w.maxAssignment > 0
	if !execEnabled && !absoluteEnabled {
		// Both arms off. A scan here is a guaranteed-empty round trip every tick,
		// forever; skip it rather than pay for it.
		return nil
	}

	now := w.now()
	overdue, err := w.q.ListOverdueAssignedTasks(ctx, store.ListOverdueAssignedTasksParams{
		AbsoluteEnabled: absoluteEnabled,
		AbsoluteCutoff:  pgtype.Timestamptz{Time: now.Add(-w.maxAssignment), Valid: true},
		ExecEnabled:     execEnabled,
		Now:             pgtype.Timestamptz{Time: now, Valid: true},
		MarginSeconds:   int64(w.margin / time.Second),
	})
	if err != nil {
		return err
	}

	for _, t := range overdue {
		updated, err := w.q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
			ID:              t.ID,
			Status:          "timed_out",
			WorkerID:        t.WorkerID,        // fence, not a value
			StartedAt:       t.StartedAt,       // preserved unchanged
			FinishedAt:      pgtype.Timestamptz{Time: now, Valid: true},
			AssignmentEpoch: t.AssignmentEpoch, // fence: real and non-zero, from the row
		})
		if err != nil {
			// pgx.ErrNoRows means somebody else got there first - the agent
			// finished, a cancel landed, a grace expiry requeued, or a sibling
			// replica swept it. That is the CORRECT outcome, not a failure, so it
			// is not logged. Any other error is real. Either way, continue to the
			// next row: one bad row must never end the sweep.
			if !errors.Is(err, pgx.ErrNoRows) {
				log.Printf("watchdog: UpdateTaskStatus(timed_out) for task %s: %v", uuidStr(t.ID), err)
			}
			continue
		}

		arm, age, bound := overdueReason(t, now, w.margin, w.maxAssignment)
		// One line per SWEPT task, unbudgeted, and that is safe: the count per
		// sweep is bounded by the fleet's assigned-task count, each task can be
		// swept at most once (it is terminal afterwards), and nothing in the line
		// is caller-supplied. A watchdog that kills somebody's work without saying
		// why it decided to is worse than no watchdog.
		log.Printf("watchdog: task %s (job %s, worker %s) timed out by the %s bound: assignment age %s exceeds %s",
			uuidStr(updated.ID), uuidStr(updated.JobID), uuidStr(t.WorkerID), arm, age.Round(time.Second), bound)

		finalizeTerminalTask(ctx, w.q, w.broker, "watchdog", updated, "timed_out")
		if err := w.q.NotifyTaskCompleted(ctx); err != nil {
			log.Printf("watchdog: NotifyTaskCompleted: %v", err)
		}
	}
	return nil
}

// overdueReason reports which bound a swept row blew, FOR THE LOG LINE ONLY. It
// is not a second gate: the database decided, and this only re-derives the
// explanation. If a row somehow satisfies neither arm in Go it is reported as
// "absolute", because that arm applies to every assigned row.
func overdueReason(t store.Task, now time.Time, margin, maxAssignment time.Duration) (arm string, age, bound time.Duration) {
	if margin > 0 && t.StartedAt.Valid && t.TimeoutSeconds != nil && *t.TimeoutSeconds > 0 {
		execAge := now.Sub(t.StartedAt.Time)
		execBound := time.Duration(*t.TimeoutSeconds)*time.Second + margin
		if execAge > execBound {
			return "execution", execAge, execBound
		}
	}
	if t.AssignedAt.Valid {
		age = now.Sub(t.AssignedAt.Time)
	}
	return "absolute", age, maxAssignment
}
```

- [ ] **Step 4: Run the test to verify it passes**

```
go test ./internal/scheduler/... -run TestWatchdog -v -timeout 120s
go test ./... -timeout 120s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/watchdog.go internal/scheduler/watchdog_test.go
git commit -m "feat(scheduler): stale-task watchdog writes timed_out through the existing fence"
```

---

### Task 7: `Registry.SendCancel` and the watchdog's best-effort cancel

**Files:**
- Modify: `internal/worker/registry.go:60-66`
- Modify: `internal/scheduler/watchdog.go`
- Modify: `internal/scheduler/watchdog_test.go`
- Create: `internal/worker/registry_sendcancel_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/worker/registry_sendcancel_test.go`:

```go
package worker

import (
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capturingSender struct{ sent []*relayv1.CoordinatorMessage }

func (c *capturingSender) Send(m *relayv1.CoordinatorMessage) error {
	c.sent = append(c.sent, m)
	return nil
}

// TestRegistry_SendCancel_BuildsTheCancelTaskPayload keeps CancelTask
// construction in one place. It mirrors SendEvictCommand exactly.
func TestRegistry_SendCancel_BuildsTheCancelTaskPayload(t *testing.T) {
	r := NewRegistry()
	s := &capturingSender{}
	r.Register("w1", s)

	require.NoError(t, r.SendCancel("w1", "t1", false))

	require.Len(t, s.sent, 1)
	ct := s.sent[0].GetCancelTask()
	require.NotNil(t, ct, "the payload must be a CancelTask")
	assert.Equal(t, "t1", ct.TaskId)
	assert.False(t, ct.Force)
}

// TestRegistry_SendCancel_UnconnectedWorkerIsAnError: the caller decides what to
// do about it. The watchdog ignores it - best-effort by construction.
func TestRegistry_SendCancel_UnconnectedWorkerIsAnError(t *testing.T) {
	assert.Error(t, NewRegistry().SendCancel("nobody", "t1", false))
}
```

Append to `internal/scheduler/watchdog_test.go`:

```go
// recordingCanceller records each send in order and can block, so one test can
// assert ordering relative to the write and another can assert the fan-out is
// concurrent.
type recordingCanceller struct {
	mu    sync.Mutex
	store *fakeWatchdogStore
	block time.Duration
	sends []cancelRecord
}

type cancelRecord struct {
	workerID string
	taskID   string
	force    bool
}

func (c *recordingCanceller) SendCancel(workerID, taskID string, force bool) error {
	if c.block > 0 {
		time.Sleep(c.block)
	}
	if c.store != nil {
		c.store.mu.Lock()
		c.store.events = append(c.store.events, "cancel:"+taskID)
		c.store.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends = append(c.sends, cancelRecord{workerID, taskID, force})
	return nil
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

// TestWatchdog_CancelsAfterTheWriteAndOnlyForMatchedRows. Two properties in one
// test because they are the same property: the send is a CONSEQUENCE of the
// write having matched.
//
// force=false is the conservative arm and a genuine trade. force=true skips
// workspace finalize, which risks leaving a P4 workspace in a state that poisons
// warm-workspace scoring for every later task on that machine; force=false still
// closes cancelledCh, which is the escape that frees a log write parked on a
// full sendCh. It also matches handleDisableWorker, the other place the
// coordinator unilaterally takes tasks from a still-connected agent.
func TestWatchdog_CancelsAfterTheWriteAndOnlyForMatchedRows(t *testing.T) {
	now := time.Now()
	rejected := overdueRow(1, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	swept := overdueRow(2, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	q := &fakeWatchdogStore{
		overdue:   []store.Task{rejected, swept},
		updateErr: map[pgtype.UUID]error{rejected.ID: pgx.ErrNoRows},
	}
	c := &recordingCanceller{store: q}
	w := NewWatchdog(q, c, events.NewBroker(), 30*time.Minute, 24*time.Hour)
	w.now = func() time.Time { return now }

	require.NoError(t, w.SweepOnce(context.Background()))

	require.Len(t, c.sends, 1,
		"a row the fence rejected belongs to somebody else now; cancelling it would tear a LIVE assignment off a worker")
	assert.Equal(t, uuidStr(swept.ID), c.sends[0].taskID)
	assert.Equal(t, uuidStr(swept.WorkerID), c.sends[0].workerID)
	assert.False(t, c.sends[0].force,
		"force=false: skipping workspace finalize can poison warm-workspace scoring for every later task")

	trace := q.trace()
	assert.Less(t, indexOf(trace, "update:"+uuidStr(swept.ID)), indexOf(trace, "cancel:"+uuidStr(swept.ID)),
		"the write must be durable before the agent is told to stop: sending first would leave an agent told to "+
			"abandon a task the coordinator still considers live")
}

// TestWatchdog_CancelFanOutIsConcurrent: N overdue tasks on ONE wedged worker
// must cost ~one send timeout, not N of them. Modelled on
// internal/api/cancel_signals_test.go:29-62.
func TestWatchdog_CancelFanOutIsConcurrent(t *testing.T) {
	const block = 200 * time.Millisecond
	const n = 5

	now := time.Now()
	rows := make([]store.Task, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, overdueRow(byte(10+i), 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour)))
	}
	q := &fakeWatchdogStore{overdue: rows}
	c := &recordingCanceller{block: block}
	w := NewWatchdog(q, c, events.NewBroker(), 30*time.Minute, 24*time.Hour)
	w.now = func() time.Time { return now }

	start := time.Now()
	require.NoError(t, w.SweepOnce(context.Background()))
	elapsed := time.Since(start)

	require.Len(t, c.sends, n)
	assert.Less(t, elapsed, (n-1)*block,
		"the cancel fan-out must be concurrent: a sequential loop over one wedged worker costs N send timeouts")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```
go test ./internal/worker/... -run TestRegistry_SendCancel -v -timeout 60s
go test ./internal/scheduler/... -run TestWatchdog_Cancel -v -timeout 60s
```

Expected: FAIL to compile (`r.SendCancel undefined`), and the watchdog sends nothing.

- [ ] **Step 3: Write the minimal implementation**

Append to `internal/worker/registry.go`:

```go
// SendCancel sends a CancelTask to the named connected worker. Returns an error
// if the worker is not connected. Together with api.sendCancelSignals this is the
// only construction site for CancelTask in the tree; both go through Send, so
// both are bounded by the worker sender's sendTimeout.
func (r *Registry) SendCancel(workerID, taskID string, force bool) error {
	return r.Send(workerID, &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_CancelTask{
			CancelTask: &relayv1.CancelTask{TaskId: taskID, Force: force},
		},
	})
}
```

In `internal/scheduler/watchdog.go`, add `"sync"` to the imports; declare the collector immediately before the `for _, t := range overdue` loop in `SweepOnce`:

```go
	var cancels []watchdogCancel
```

append inside the loop, as the last statement of the successful branch (after the `NotifyTaskCompleted` call):

```go
		cancels = append(cancels, watchdogCancel{
			workerID: uuidStr(t.WorkerID),
			taskID:   uuidStr(t.ID),
		})
```

and replace the function's trailing `return nil` with:

```go
	w.sendCancels(cancels)
	return nil
}

// watchdogCancel is one best-effort CancelTask to deliver to a connected agent.
type watchdogCancel struct {
	workerID string
	taskID   string
}

// sendCancels tells each swept task's agent to stop, so the coordinator does not
// merely do bookkeeping while an orphan subprocess keeps running against a
// workspace and the freed slot over-subscribes the machine that is already in
// trouble. Deliberately identical in shape to api.sendCancelSignals, which is
// the reviewed precedent for a coordinator-side terminal write over a live
// assignment - handleCancelJob does exactly this today.
//
// Best-effort: the return value is ignored, because a failed send just means the
// agent already lost the task, and the watchdog is registry-blind by design -
// under multi-replica operation the agent may be connected elsewhere. The sends
// run concurrently and this blocks until all complete, so N overdue tasks on ONE
// wedged worker cost ~one send timeout instead of N of them.
func (w *Watchdog) sendCancels(cancels []watchdogCancel) {
	var wg sync.WaitGroup
	for _, c := range cancels {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.canceller.SendCancel(c.workerID, c.taskID, false)
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/worker/... ./internal/scheduler/... -timeout 180s
```

Expected: PASS.

- [ ] **Step 5: Mutation check**

Move the `SendCancel` call inline into the loop **before** `UpdateTaskStatus` (and drop the collector). `TestWatchdog_CancelsAfterTheWriteAndOnlyForMatchedRows` must go RED on both the ordering assertion and `require.Len(t, c.sends, 1)`. Revert.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/registry.go internal/worker/registry_sendcancel_test.go \
        internal/scheduler/watchdog.go internal/scheduler/watchdog_test.go
git commit -m "feat(worker): Registry.SendCancel; watchdog tells the agent to stop"
```

---

### Task 8: Wire the watchdog into `relay-server`

**Files:**
- Create: `cmd/relay-server/watchdog_config.go`
- Create: `cmd/relay-server/watchdog_config_test.go`
- Modify: `cmd/relay-server/main.go` (around line 214)

- [ ] **Step 1: Write the failing test**

Create `cmd/relay-server/watchdog_config_test.go`:

```go
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"relay/internal/scheduler"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWatchdogDuration(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		want     time.Duration
		wantWarn string
	}{
		{"unset keeps the default and does NOT warn", "", scheduler.DefaultWatchdogMargin, ""},
		{"a sensible value is used as-is", "45m", 45 * time.Minute, ""},
		{"zero is ACCEPTED and disables the arm, with an informational line", "0s", 0, "disabled"},
		{"unparseable keeps the default and warns", "thirty minutes", scheduler.DefaultWatchdogMargin, "not a Go duration"},
		{"negative keeps the default and warns", "-5m", scheduler.DefaultWatchdogMargin, "not a Go duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warn := parseWatchdogDuration(
				"RELAY_TASK_WATCHDOG_MARGIN", tc.raw, scheduler.DefaultWatchdogMargin)
			assert.Equal(t, tc.want, got)
			if tc.wantWarn == "" {
				assert.Empty(t, warn, "a valid value must not produce startup noise")
				return
			}
			require.Contains(t, warn, tc.wantWarn,
				"the message is the only signal an operator gets; it must name the consequence")
			assert.Contains(t, warn, "RELAY_TASK_WATCHDOG_MARGIN",
				"the message must name the variable it is about")
		})
	}
}

// TestWatchdogIsStartedByMain is a structural guard in the same spirit as
// TestTrailingLogWindowIsWiredIntoTheHandler. Deleting the wiring block in main()
// compiles and leaves `go build ./... && go test ./...` fully green across every
// package: the watchdog keeps its own passing unit tests, the statement keeps its
// own passing store tests, and the coordinator silently has no bound on task
// duration again - which is the entire bug.
//
// go/ast, NOT a regex. A source-scanning regex guard in this repo was proven
// breakable by a single stray comment.
func TestWatchdogIsStartedByMain(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	// name assigned -> identifiers its RHS mentions, so the walk can follow
	// `x, warn := f(...)` and then NewWatchdog(..., x, ...).
	from := map[string][]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
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
		return true
	})

	var seeds []string
	ast.Inspect(file, func(n ast.Node) bool {
		gs, ok := n.(*ast.GoStmt)
		if !ok {
			return true
		}
		var idents []string
		ast.Inspect(gs.Call, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok {
				idents = append(idents, id.Name)
			}
			return true
		})
		for _, name := range idents {
			if name == "NewWatchdog" {
				seeds = append(seeds, idents...)
				break
			}
		}
		return true
	})
	require.NotEmpty(t, seeds,
		"main.go starts no goroutine mentioning NewWatchdog: the stale-task watchdog never runs and nothing else fails")

	seen := map[string]bool{}
	queue := append([]string(nil), seeds...)
	found := false
	for len(queue) > 0 && !found {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		if name == "parseWatchdogDuration" {
			found = true
			break
		}
		queue = append(queue, from[name]...)
	}
	require.True(t, found,
		"main.go starts the watchdog but its bounds do not derive from parseWatchdogDuration, "+
			"so RELAY_TASK_WATCHDOG_MARGIN and RELAY_TASK_MAX_ASSIGNMENT are no longer what reaches it")
}
```

- [ ] **Step 2: Run the test to verify it fails**

```
go test ./cmd/relay-server/... -run 'TestParseWatchdogDuration|TestWatchdogIsStartedByMain' -v -timeout 60s
```

Expected: FAIL to compile - `undefined: parseWatchdogDuration`.

- [ ] **Step 3: Write the minimal implementation**

Create `cmd/relay-server/watchdog_config.go`:

```go
package main

import (
	"fmt"
	"time"
)

// parseWatchdogDuration resolves one of the stale-task watchdog's two bounds
// into the duration handed to scheduler.NewWatchdog, plus a startup message to
// log, empty when there is nothing to say. Three outcomes, not two, which is why
// the second return is a message and not an ok bool:
//
//   - Unset, or a valid positive duration: used as-is, silently.
//   - Exactly zero: ACCEPTED, and the arm is disabled. `0` means "this arm is
//     off" in BOTH watchdog variables - one rule, no exceptions. An operator who
//     genuinely wants no margin writes `1s`; giving the same literal two meanings
//     across two adjacent knobs would be a footgun. Because disabling a safety
//     bound must never be silent, this returns an informational line naming what
//     is now unbounded.
//   - Unparseable or negative: the default is used and the message says so. A
//     silently-ignored typo would leave an operator believing they had tightened
//     a bound they had not.
//
// Deliberately not a log.Fatalf, following parseTrailingLogWindow: a bad
// duration must not stop a server booting when a safe default exists.
func parseWatchdogDuration(name, raw string, def time.Duration) (time.Duration, string) {
	if raw == "" {
		return def, ""
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return def, fmt.Sprintf("%s=%q is not a Go duration (or is negative); using %s", name, raw, def)
	}
	if d == 0 {
		return 0, fmt.Sprintf(
			"%s=%q: this arm of the stale-task watchdog is disabled. Tasks it would have bounded can now hold "+
				"an assignment indefinitely. Use 1s, not 0s, if you meant `no margin` rather than `no bound`.",
			name, raw)
	}
	return d, ""
}
```

In `cmd/relay-server/main.go`, immediately after `go metrics.NewSweeper(q, broker, metricsStore, staleAfter).Run(ctx)` (line 214):

```go
	// Bound how long a task may hold an assignment. tasks.timeout_sec is
	// otherwise enforced only by the agent, so a wedged or lying agent holds its
	// task - and its worker slot, and its job - forever.
	watchdogMargin, marginWarning := parseWatchdogDuration(
		"RELAY_TASK_WATCHDOG_MARGIN", os.Getenv("RELAY_TASK_WATCHDOG_MARGIN"), scheduler.DefaultWatchdogMargin)
	if marginWarning != "" {
		log.Printf("WARNING: %s", marginWarning)
	}
	maxAssignment, maxAssignmentWarning := parseWatchdogDuration(
		"RELAY_TASK_MAX_ASSIGNMENT", os.Getenv("RELAY_TASK_MAX_ASSIGNMENT"), scheduler.DefaultMaxAssignment)
	if maxAssignmentWarning != "" {
		log.Printf("WARNING: %s", maxAssignmentWarning)
	}
	go scheduler.NewWatchdog(q, registry, broker, watchdogMargin, maxAssignment).Run(ctx)
```

`scheduler`, `os` and `log` are already imported by `main.go`.

- [ ] **Step 4: Run the test to verify it passes**

```
go test ./cmd/relay-server/... -timeout 120s
go build ./...
```

Expected: PASS, build clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/watchdog_config.go cmd/relay-server/watchdog_config_test.go cmd/relay-server/main.go
git commit -m "feat(server): start the stale-task watchdog with configurable bounds"
```

---

### Task 9: The end-to-end criterion - a CONNECTED worker's hung task is swept, and its late report is a no-op

**Files:**
- Create: `internal/worker/handler_watchdog_e2e_integration_test.go`

This is the backlog item's headline acceptance criterion. It must use a **connected** worker: the disconnected case belongs to `GraceRegistry` and a test of it would be vacuous. `worker_test` already imports `relay/internal/scheduler` (`internal/worker/handler_test.go:16`), so there is nothing to arrange.

- [ ] **Step 1: Write the test**

```go
//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingCancelSender is a connected agent that records what it was told.
type capturingCancelSender struct{ taskIDs []string }

func (c *capturingCancelSender) Send(m *relayv1.CoordinatorMessage) error {
	if ct := m.GetCancelTask(); ct != nil {
		c.taskIDs = append(c.taskIDs, ct.TaskId)
	}
	return nil
}

// TestWatchdog_SweepsAHungTaskOnAConnectedWorker is the backlog item's headline
// criterion. The worker is REGISTERED for the whole test - connected, its stream
// healthy, its grace timer unarmed - because the disconnected case is
// GraceRegistry's and a test of it would prove nothing about this fix.
//
// The second half is the criterion that the agent's own terminal update,
// arriving after the sweep, is a silent no-op rather than a resurrection or a
// double-count. It needs no new machinery: UpdateTaskStatus's status allow-list
// already makes a terminal row unwritable.
func TestWatchdog_SweepsAHungTaskOnAConnectedWorker(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	jobID, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "watchdog-e2e", 3)

	// A dependent task, so the cascade has something to act on.
	dependent, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: jobID, Name: "dependent", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NoError(t, q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: dependent.ID, DependsOnTaskID: taskID,
	}))

	// Claimed 30 hours ago, running for 29 hours, 60s timeout: both bounds blown,
	// which is what a hung agent looks like.
	_, err = pool.Exec(ctx, `UPDATE tasks SET timeout_seconds = 60 WHERE id = $1`, taskID)
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
		AssignedAt: pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	_, err = q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "running", WorkerID: w1,
		AssignmentEpoch: claimed.AssignmentEpoch,
		StartedAt:       pgtype.Timestamptz{Time: time.Now().Add(-29 * time.Hour), Valid: true},
	})
	require.NoError(t, err)

	// The agent is CONNECTED and stays connected. It just never reports terminal.
	cancels := &capturingCancelSender{}
	registry.Register(h.UUIDStringForTest(w1), cancels)

	require.NoError(t, scheduler.
		NewWatchdog(q, registry, broker, 30*time.Minute, 24*time.Hour).
		SweepOnce(ctx))

	swept, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "timed_out", swept.Status, "the coordinator must end the assignment with no operator action")
	assert.True(t, swept.FinishedAt.Valid, "a swept task must be stamped finished")
	assert.True(t, swept.WorkerID.Valid,
		"the assignment must OUTLIVE the task: trailing log chunks still need the fence")
	assert.Equal(t, claimed.AssignmentEpoch, swept.AssignmentEpoch,
		"a terminal transition must NOT bump the epoch - that would close the trailing-log flush")
	assert.Equal(t, int32(0), swept.RetryCount,
		"the watchdog burns no retry; recovery is POST /v1/jobs/{id}/retry")

	dep, err := q.GetTask(ctx, dependent.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", dep.Status,
		"the dependent cascade must run, exactly as for an agent-reported failure")

	job, err := q.GetJob(ctx, jobID)
	require.NoError(t, err)
	assert.Contains(t, []string{"failed", "done"}, job.Status,
		"THE HEADLINE SYMPTOM: the job must now reach a terminal status")

	require.Len(t, cancels.taskIDs, 1,
		"the agent must be told to stop, or the subprocess keeps running orphaned against its workspace")
	assert.Equal(t, h.UUIDStringForTest(taskID), cancels.taskIDs[0])

	// The agent's own terminal update, arriving late at the SAME epoch. Both other
	// fences legitimately pass; only the status allow-list rejects it.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_DONE,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "timed_out", after.Status, "a late terminal update must not resurrect or reclassify a swept task")
	assert.Equal(t, swept.FinishedAt.Time, after.FinishedAt.Time, "and must not restamp finished_at")
	assert.Equal(t, int32(0), after.RetryCount, "and must not burn a retry")

	afterDep, err := q.GetTask(ctx, dependent.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", afterDep.Status, "and must not cascade a second time")
}
```

If `q.GetJob` takes different arguments than shown, read `internal/store/jobs.sql.go` and adapt - the assertion is what matters, not the accessor.

- [ ] **Step 2: Establish the RED by mutation, not by un-implementing**

The capability now exists, so the honest discriminating RED is a mutation. In `internal/scheduler/watchdog.go`, temporarily bind `AssignmentEpoch: 0` in the `UpdateTaskStatus` call, then:

```
go test -tags integration -p 1 ./internal/worker/... -run TestWatchdog_SweepsAHungTaskOnAConnectedWorker -v -timeout 600s
```

Expected with that mutation: FAIL (`"running" != "timed_out"`). Revert, re-run, expect PASS. Repeat once with `WorkerID: pgtype.UUID{}` - also RED, which is the NULL-rejecting `=` property. **The discriminating input (a task at a real non-zero epoch with a real assignee) stays in the tree permanently**, so both mutations remain killed.

- [ ] **Step 3: Commit**

```bash
git add internal/worker/handler_watchdog_e2e_integration_test.go
git commit -m "test(worker): watchdog sweeps a hung task on a connected worker; late report is a no-op"
```

---

### Task 10: The prose that this slice falsifies

Documentation is the dominant defect class in this repo and three artifacts below contain claims this slice makes false. These are acceptance criteria, not cleanup.

**Files:**
- Modify: `internal/store/query/tasks.sql` (`UpdateTaskStatus` 67-71, `RetryJobTasks` 495-502)
- Modify: `internal/store/tasks_status_vocabulary_lockstep_test.go`
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Generated: `internal/store/tasks.sql.go`

- [ ] **Step 1: Amend `RetryJobTasks`'s doc comment**

Replace the paragraph beginning `-- What would break this is a SERVER-SIDE watchdog:` (`internal/store/query/tasks.sql:495-502`) with:

```sql
-- THAT WATCHDOG NOW EXISTS, so read the paragraph above as history: `timed_out`
-- has TWO writers. The assignee itself (handleTaskStatus, after its subprocess
-- is already dead), and the coordinator watchdog
-- (internal/scheduler/watchdog.go), which stamps `timed_out` on a task whose
-- agent may still be happily running it. So a `timed_out` row selected here MAY
-- be terminal and still executing, and this statement can reopen it for a second
-- worker while the first agent's eventual completion is fenced out silently.
-- THAT HAZARD IS NOT NEW WITH THE WATCHDOG, which is why it did not block it:
-- CancelJobTasks already stamps `failed` on live `dispatched`/`running`
-- assignments and handleCancelJob mitigates with a best-effort
-- sendCancelSignals. The watchdog adopts the identical mitigation - it sends
-- CancelTask (force=false) to every swept task's worker after the write.
-- The residual is bounded by: the sweep fires only long past the deadline plus a
-- generous margin, the reopen is OPERATOR-GATED (nothing retries a swept task
-- automatically), and the original agent's own completion is fenced out.
-- Eliminating it entirely needs a per-assignment fencing token at the agent.
-- That is a NAMED NON-GOAL, not a plan.
```

- [ ] **Step 2: Amend `UpdateTaskStatus`'s doc comment**

Replace the line `-- Both callers are fenced by the same statement deliberately;` (line 67) with:

```sql
-- All THREE callers are fenced by the same statement deliberately;
-- Dispatcher.failClaimedTask passes claimed.WorkerID from ClaimTaskForWorker,
-- and scheduler.Watchdog passes the worker_id and assignment_epoch off the row
-- its own scan just returned - so in both the worker predicate is tautological
-- by design, exactly as this comment goes on to describe, while the EPOCH
-- predicate is the real TOCTOU guard and is not tautological in either. The
-- watchdog is why this slice added no new statement that writes tasks.status.
```

Leave the rest of that paragraph (`Dispatcher.failClaimedTask passes claimed.WorkerID... For why that beats...`) exactly as it is.

- [ ] **Step 3: Add the new site to `TestTasksStatusVocabularyIsExactly`**

In `internal/store/tasks_status_vocabulary_lockstep_test.go`:

1. Lines 25-26: replace `It exists because six statements in this repo hard-code a slice of that vocabulary` with `It exists because the statements listed below hard-code a slice of that vocabulary`. **Delete the count, do not increment it to seven.** The number was already wrong (at least eleven statements in `query/tasks.sql` slice the vocabulary; the ones listed are those whose partition choice is decision-relevant, which is the narrower and better claim), and a count in prose is a maintenance liability this slice would otherwise renew.
2. After the `AppendTaskLog` bullet (ends line 78), add:

```go
//   - ListOverdueAssignedTasks (query/tasks.sql) - `status IN ('dispatched',
//     'running')`, the "currently assigned" partition the coordinator's
//     stale-task watchdog scans. READ THIS SITE BACKWARDS TOO: it is the SECOND
//     inverted one. A new NON-TERMINAL status omitted here is NEVER SWEPT, which
//     silently reopens the unbounded-assignment hole this statement exists to
//     close, for that status - a task in it could hold its worker slot and its
//     job forever with no error and no log line. `preparing` is the same live
//     candidate as for AppendTaskLog and would need adding to BOTH. A new
//     TERMINAL status must stay OUT: sweeping a finished task is exactly the
//     resurrection every other predicate here exists to prevent.
```

3. Lines 83-85: replace `AppendTaskLog is the one site where the allow-list points the other way, which is why it is spelled out at length above rather than folded into the list.` with `AppendTaskLog and ListOverdueAssignedTasks are the two sites where the allow-list points the other way, which is why both are spelled out at length above rather than folded into the list.`
4. Lines 102-107: rewrite the failure message, dropping the count:

```go
	require.Equal(t, want, got,
		"tasks.status vocabulary changed - read this test's comment before updating it. These statements slice "+
			"this set: UpdateTaskStatus, IncrementTaskRetryCount, RecomputeJobStatus, RetryJobTasks, "+
			"SelectRetryableTaskIDs, AppendTaskLog and ListOverdueAssignedTasks. Revisit ALL OF THEM. The last "+
			"two fail OPEN in the damaging direction: a new NON-TERMINAL status omitted from AppendTaskLog's "+
			"first arm silently discards 100% of that state's log output, and one omitted from "+
			"ListOverdueAssignedTasks means a task in that state is never swept and holds its assignment forever")
```

- [ ] **Step 4: Amend CLAUDE.md's Epoch fence bullet**

One clause; nothing else moves. Find the sentence ending `...a new *terminal* status must stay out and be bounded by \`finished_at\` like \`done\`/\`failed\`/\`timed_out\`.` and insert immediately after it, before `` `handleTaskStatus` additionally checks identity in Go... ``:

```
`ListOverdueAssignedTasks` (the coordinator stale-task watchdog's scan) is the second such carve-out and reads the same way: omitting a new non-terminal status from its `status IN ('dispatched','running')` means a task in that state is never swept, so the unbounded-assignment hole that statement exists to close silently reopens for it; a new terminal status must stay out.
```

Do not restructure the bullet, and do not touch the phrase calling `AppendTaskLog` "the third status-predicate site" - it still is. What changes is that it is no longer the *only* carve-out, and the inserted clause says so.

- [ ] **Step 5: README rows**

Insert after `README.md:277` (the `RELAY_TASKLOG_TRAILING_WINDOW` row):

```markdown
| `RELAY_TASK_WATCHDOG_MARGIN` | `30m` | How long past a task's own `timeout_sec` the coordinator waits before declaring it `timed_out` itself. `timeout_sec` is otherwise enforced only by the agent, so this is what bounds a wedged or uncooperative one. The margin must absorb the whole gap between the agent's deadline firing and the coordinator seeing the terminal update (subprocess kill, proctree cleanup, final log flush, and a gRPC reconnect if the stream dropped - roughly 105s in the worst measured case), which is why the default is generous. **Set it too small and healthy work is killed with no way for the agent to object**, and the task is not retried automatically. `0` disables this arm entirely; write `1s` if you meant "no margin" rather than "no bound". Applies only to tasks with `timeout_sec > 0`. |
| `RELAY_TASK_MAX_ASSIGNMENT` | `24h` | Absolute cap on how long a task may stay assigned to a worker, measured from dispatch. This is the arm that bounds `timeout_sec = 0` tasks (documented as "no deadline") and tasks that never report `running` at all - a task spends its entire workspace sync in `dispatched`, and a P4 sync on a 1 TB+ workspace can legitimately run for hours, so the default must exceed the longest honest assignment. **Too small kills healthy long-running work silently.** `0` disables this arm. Setting both watchdog variables to `0` disables the watchdog completely, restoring the behaviour in which a connected agent could hold a task, its worker slot and its job forever. A swept task is marked `timed_out` and is **not** retried automatically - the retry budget is consumed only by agent-reported failures - so recovery is `POST /v1/jobs/{id}/retry`. |
```

Then update the startup sequence at `README.md:309`:

```markdown
4. Start the task dispatch scheduler, the Postgres LISTEN/NOTIFY trigger, and the stale-task watchdog (which ends assignments that blow `RELAY_TASK_WATCHDOG_MARGIN` or `RELAY_TASK_MAX_ASSIGNMENT`)
```

- [ ] **Step 6: Regenerate and VERIFY the comments landed in the generated file**

Both amended comments are on sqlc queries, so they exist twice. Follow the `make generate` procedure, then:

```
rg -n "coordinator watchdog" internal/store/tasks.sql.go
rg -n "All THREE callers are fenced" internal/store/tasks.sql.go
```

Both must match. If they do not, the CRLF revert discarded the regeneration and the generated file now contradicts its own source - regenerate and redo the diff triage.

- [ ] **Step 7: Run everything**

```
go test ./... -timeout 180s
go test -tags integration -p 1 ./internal/store/... ./internal/scheduler/... ./internal/worker/... ./cmd/... -timeout 1800s
go vet -tags integration ./...
```

Expected: all green.

- [ ] **Step 8: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go \
        internal/store/tasks_status_vocabulary_lockstep_test.go CLAUDE.md README.md
git commit -m "docs: name the coordinator watchdog as the second timed_out writer"
```

---

### Task 11: Close the backlog item (conductor step)

- [ ] **Step 1: Run the close command.** The engineer does not run `/backlog`.

```
/backlog close no-coordinator-watchdog
```

This `git mv`s `docs/backlog/bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task.md` into `docs/backlog/closed/`, stamps the frontmatter, appends a `## Resolution` note and commits. Never hand-edit `status:` in place.

- [ ] **Step 2: Update, do not close, the two related items**

- `bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence` - its promotion condition was explicitly "if the watchdog item is specced with the requeue shape". It was not. **Mark that condition as not triggered**; do not close it, the underlying unfenced write is still there.
- `bug-2026-08-14-task-logs-have-no-per-task-volume-cap` - unchanged. A swept task keeps its log write channel for `RELAY_TASKLOG_TRAILING_WINDOW`, and the volume inside that window is still unbounded.

---

## Known limitations to state in the PR body

- **The retry budget does not apply to a swept task.** `retries: 3` buys zero automatic attempts if the task hangs, because `task.retries` is consumed only by `handleTaskStatus`'s agent-driven branch and `IncrementTaskRetryCount` has a module-wide structural guard against non-agent callers. Recovery is `POST /v1/jobs/{id}/retry`. Deliberate: an automatic retry of a task that may still be executing is duplicate execution on a schedule.
- **A swept task that an operator retries can duplicate-execute** if the cancel did not take. Operator-gated, mitigated by the cancel, and already true of the cancel-then-retry path today.
- **The freed slot is optimistic.** The coordinator releases the worker's slot while the subprocess may still be running. Inherent to declaring a task over from the coordinator's side; the cancel is the only mitigation.
- **A `dispatched`/`running` row with a NULL `worker_id` is not recoverable** by this or any other fenced writer. Unreachable today - nothing DELETEs a worker.
- **`main()` wiring is untested by execution**; its protection is `TestWatchdogIsStartedByMain` plus sitting adjacent to its siblings.

---

## Self-review against the spec

| Spec section | Covered by |
|---|---|
| 3 - fail (`timed_out`), not requeue, through the existing `UpdateTaskStatus` | Task 6 |
| 3.1 - the end-to-end trace (cascade, recompute, notify, late-update no-op) | Tasks 6, 9 |
| 3.2 - the `RetryJobTasks` comment amendment | Task 10 step 1 |
| 4.1 / 4.2 - the two bounds | Task 4 (SQL), Task 6 (Go) |
| 4.3 - Go-computed cutoffs, no `NOW() - interval` | Task 4 comment, `TestWatchdog_ScanParametersDeriveFromTheConfiguredBounds` |
| 4.4 - the two env vars, `0` = off, one warning at startup, fixed interval | Task 8 |
| 5.1 - key on non-terminal duration, `worker_id IS NOT NULL`, never on activity | Task 4 (`..._ActivityDoesNotCount`, `..._StatusAndAssigneeGuards`) |
| 5.2 - migration 000021, nullable, backfill, no new index | Task 1 + the decision table |
| 5.3 - the statement | Task 4 |
| 5.4 - the write, `ErrNoRows` dropped silently | Task 6 |
| 5.5 - invariant compliance | Task 4 + Task 6 comments; the fence branch is stated below |
| 5.6 - the vocabulary test site + the CLAUDE.md clause | Task 10 steps 3, 4 |
| 6 - the grace ordering argument in the doc comment | Task 6 (`Watchdog`'s doc comment) |
| 7 - the cancel, `force=false`, write-then-send, concurrent fan-out | Task 7 |
| 7 (optional) - refactoring `api.sendCancelSignals` onto `SendCancel` | **DROPPED.** Explicitly droppable in the spec; it touches a shipped, reviewed path for no behaviour change and would put a second package's tests inside this slice's blast radius. Not filed - it is a three-line tidy for whoever is next in that file. |
| 8 - where it lives; the `finalizeTerminalTask` extraction with its zero-diff gate + mutation proof | Task 5 |
| 8.1 - one log line per swept task, naming the arm, the age and the bound | Task 6 |
| 9 - the test matrix and the mutation battery | Tasks 3, 4, 6, 7, 9 |
| 10 - all six prose items | Task 10 |
| 11 - limitations | PR body section above |
| 12 - no backlog recommendations | Task 11 step 2 |

**Which branch of the Epoch fence invariant this new writer satisfies, stated for the reviewer:** branch one - *fence on `assignment_epoch`*. The watchdog writes through `UpdateTaskStatus`, binding the epoch and worker id read from the row the scan just returned. It does **not** bump, and must not: this is a terminal transition, and the assignment surviving completion is load-bearing for the trailing-log flush (CLAUDE.md: "the fix for that is a status predicate, **never an epoch bump on terminal transitions**"). It is not branch two (conditional end-of-assignment) and does not rely on branch three (terminal-only writer resting on a status predicate alone, as `FailDependentTasks` does), though it happens to carry that predicate too, since `UpdateTaskStatus` already has it. The scan additionally carries `worker_id IS NOT NULL`, which guarantees the bound worker id is real, so the write's NULL-rejecting plain `=` is a genuine second check and never matches an unassigned row.
