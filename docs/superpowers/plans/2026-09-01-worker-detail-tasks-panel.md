# Worker Detail Current-Tasks Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `GET /v1/workers/{id}/tasks` and build the worker-detail "Current tasks" panel and the real Slots KPI on it.

**Architecture:** Two new read-only store statements over the existing `idx_tasks_worker_active` partial index (no migration), plus one job-name lookup mirroring `GetUserEmailsByIDs`; one handler that reuses `toTaskResponse` through an embedded `taskResponse` and the standard `page[T]`/`parsePage`/`buildPage` machinery; one React panel on the holo `Table` primitive fed by a 3 s polling hook, whose `total` also drives the Slots KPI.

**Tech Stack:** Go 1.26, sqlc, pgx v5, Postgres 16, testcontainers-go, React 18, TanStack Query v5, MSW v2, Vitest, Tailwind v4.

**Spec:** `docs/superpowers/specs/2026-09-01-worker-detail-tasks-panel-design.md`

---

## Slice independence declaration

**SEQUENTIAL. Backend first, frontend second. They must NOT run in parallel.**

The frontend hook consumes a route that does not exist at HEAD, and the frontend tests assert
against hand-written fixtures whose field names come from the Go response struct. Task 12 also
rewrites `WorkerDetailPage.test.tsx`, which cannot be made green until the panel exists.

| Tasks | Owner | Dispatch |
|-------|-------|----------|
| 1, 2, 3, 4, 5, 6, 7, 8 | `relay-backend-engineer` | First, in order |
| 9, 10, 11, 12 | `relay-frontend-engineer` | Only after Task 8 is committed |
| 13 | either (final gate run) | Last |

Everything below runs in the worktree
`D:/dev/relay/.claude/worktrees/web-e-worker-tasks`. Never `cd D:/dev/relay`.

## Rules that apply to every task

- **Never edit `internal/store/*.sql.go` or `internal/store/models.go` by hand.** They are sqlc
  output. Edit `internal/store/query/*.sql` and run `make generate`.
- **Commit with an explicit pathspec.** Never `git add -A`, never `git add .`. Sibling agents share
  one git index.
- **`web/dist` is tracked but not maintained per-PR.** Never stage it. Before assembling the PR run
  `git checkout -- web/dist/`.
- **Do NOT run `/backlog close`.** The conductor closes
  `docs/backlog/feature-2026-06-05-worker-detail-activity-panel.md`.
- **Comment policy (CLAUDE.md).** A comment states a hazard or constraint the code cannot show, and
  may cite the test that pins it. No dates, no history, no counts of other code, no censuses, no
  measurement provenance. All of that goes in the commit message. The one deliberate exception is
  `internal/store/tasks_status_vocabulary_lockstep_test.go`, which IS a census by design.
- **No em dashes or en dashes anywhere**, in code, comments, commit messages or docs. Regular
  hyphens only.
- **After any programmatic edit to a tracked text file**, check the diffstat against the size of the
  change you intended and run `git ls-files --eol <paths>`; every entry must read `i/lf`.

## Refutations found while reading the spec against HEAD

Read these before Task 2. They change what Task 2 does.

1. **The assignment-partition census goes from six members to NINE, not eight.**
   `CountActiveTasksByAllWorkers` (`internal/store/query/tasks.sql:612-619`) already carries
   `status IN ('dispatched', 'running')` and is absent from both the comment list and the failure
   message of `TestTasksStatusVocabularyIsExactly`, even though `ListOverdueAssignedTasks`'s own
   comment names it as a member of the same set. The shape search that establishes this is
   `rg "IN \('dispatched', 'running'\)" internal/store/query` -> seven statement hits at HEAD
   (`GetActiveTasksForWorker`, `ListGraceCandidates`, `RequeueTaskByID`,
   `CountActiveTasksByAllWorkers`, `ListOverdueAssignedTasks`, `RequeueWorkerTasks`,
   `RequeueWorkerTasksIfEpoch`) plus two non-statement sites (the `idx_tasks_worker_active` predicate
   in migration 000018 and the one-off backfill in migration 000021). Six of the seven are named by
   the guard. Task 2 adds the missing one along with this slice's two.
2. **The guard's own totals are already inconsistent at HEAD.** The failure message enumerates
   thirteen statements; the comment's bullet list enumerates fourteen (it adds
   `CountTerminalTasksForWorker`), while the prose says "seven of the thirteen STATEMENTS named here"
   and the two non-statement entries are headed "THE FOURTEENTH ENTRY" and "THE FIFTEENTH ENTRY". The
   ordinals were correct before `CountTerminalTasksForWorker` was added and nobody renumbered. Task 2
   therefore replaces the numeric ratio and the ordinal headings with enumerations rather than
   authoring fresh numbers on top of wrong ones.
3. **`ProgressBar` already clamps.** `web/src/components/holo/ProgressBar.tsx:16-17` computes
   `Math.min(Math.max(raw, 0), 100)`. The spec's conditional ("if `ProgressBar` does not already
   clamp, the panel clamps before passing the value") resolves to: write no clamp. Task 12 pins the
   clamp with an assertion instead, using a fixture whose `total` exceeds `max_slots`.
4. **Nothing else in the spec was refuted.** Verified present at HEAD: `idx_tasks_worker_active`
   (000018:10-11) covers the predicate exactly, so no migration; `GetUserEmailsByIDs`
   (`query/users.sql:156-157`) is a real exemplar and generates
   `func (q *Queries) GetUserEmailsByIDs(ctx, dollar_1 []pgtype.UUID) ([]GetUserEmailsByIDsRow, error)`;
   `ListWorkersPageByLastSeenDesc` (`query/workers.sql:262-284`) is the DESC-NULLS-LAST cursor shape;
   `handleListRevokedWorkers` (`internal/api/workers.go:315-351`) is the fixed-order pattern;
   `disableWorkerResponse` (`workers.go:38-41`) is the embedding pattern; `GET /v1/workers/{id}` and
   `/metrics` are `auth(...)` (`server.go:153-154`) while `/workspaces` is `auth(admin(...))`
   (`server.go:191`); `web/src/test/setup.ts:5` sets `onUnhandledRequest: 'error'`, which is why
   every existing `WorkerDetailPage` test needs a handler for the new route; `Table`'s `minWidth` is
   required (`Table.tsx:102`); `/jobs/:id` and `/jobs/:id/tasks/:taskId` both exist
   (`router.tsx:28-29`).

## File structure

**Backend, modified:**

- `internal/store/query/tasks.sql` - append `ListActiveTasksForWorkerPage` and
  `CountActiveTasksForWorker` after `CountTerminalTasksForWorker` (currently ends at line 943).
- `internal/store/query/jobs.sql` - append `GetJobNamesByIDs` after `GetJobForUpdate`.
- `internal/store/tasks.sql.go`, `internal/store/jobs.sql.go` - regenerated by `make generate`, never
  hand-edited.
- `internal/store/tasks_status_vocabulary_lockstep_test.go` - the census (lines 102-127, 188-201,
  138, 157, 219-239).
- `internal/api/workers.go` - `workerTaskResponse`, `WorkerTasksSortSpec`, `workerTasksRowKey`,
  `toWorkerTaskResponse`, `handleListWorkerTasks`, `fillJobNames`.
- `internal/api/server.go:154` - route registration, immediately after the `/metrics` line.
- `README.md:1534` - one row appended to the Workers table.

**Backend, created:**

- `internal/store/list_active_tasks_for_worker_integration_test.go`
- `internal/api/worker_tasks_test.go` (default lane)
- `internal/api/workers_tasks_integration_test.go`

**Frontend, modified:**

- `web/src/workers/api.ts` - `WorkerTask`, `WorkerTasksPage`, `listWorkerTasks`.
- `web/src/workers/api.test.ts` - the new client's tests.
- `web/src/workers/WorkerDetailPage.tsx` - panel body, Slots KPI, Jobs-today comment pointer.
- `web/src/workers/WorkerDetailPage.test.tsx` - handler in `renderDetail`, two tests deleted, two
  rewritten.

**Frontend, created:**

- `web/src/workers/useWorkerTasks.ts`
- `web/src/workers/useWorkerTasks.test.tsx`
- `web/src/workers/WorkerTasksPanel.tsx`
- `web/src/workers/WorkerTasksPanel.test.tsx`

---

# Backend

### Task 1: the two task statements and the job-name lookup

**Owner:** `relay-backend-engineer`

**Files:**
- Modify: `internal/store/query/tasks.sql` (append after line 943)
- Modify: `internal/store/query/jobs.sql` (append after `GetJobForUpdate`, currently the last
  statement)
- Regenerate: `internal/store/tasks.sql.go`, `internal/store/jobs.sql.go` (via `make generate`)
- Test: `internal/store/list_active_tasks_for_worker_integration_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/store/list_active_tasks_for_worker_integration_test.go`:

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

// runningAt claims a task at `at` and then moves it to 'running' through the
// production status writer, so the row carries a real assignee and a real epoch.
func runningAt(t *testing.T, f *assignedFixture, name string, at time.Time) store.Task {
	t.Helper()
	task := f.claimedAt(t, name, at)
	got, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID:              task.ID,
		Status:          "running",
		WorkerID:        f.w.ID,
		AssignmentEpoch: task.AssignmentEpoch,
		StartedAt:       pgtype.Timestamptz{Time: at.Add(time.Minute), Valid: true},
	})
	require.NoError(t, err)
	return got
}

func listPage(t *testing.T, f *assignedFixture, limit int32) []store.Task {
	t.Helper()
	rows, err := f.q.ListActiveTasksForWorkerPage(f.ctx, store.ListActiveTasksForWorkerPageParams{
		WorkerID:  f.w.ID,
		PageLimit: limit,
	})
	require.NoError(t, err)
	return rows
}

// TestListActiveTasksForWorkerPage_ReturnsBothAssignedStatuses is the positive
// arm of the status allow-list. Halving the IN list to one member must go RED:
// that exact mutation stayed green across four suites for RequeueTaskByID.
func TestListActiveTasksForWorkerPage_ReturnsBothAssignedStatuses(t *testing.T) {
	f := newAssignedFixture(t)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	dispatched := f.claimedAt(t, "syncing", base)
	running := runningAt(t, f, "rendering", base.Add(time.Minute))

	rows := listPage(t, f, 50)
	require.Len(t, rows, 2, "both dispatched and running are 'currently assigned'")
	got := map[string]string{}
	for _, r := range rows {
		got[r.Name] = r.Status
	}
	assert.Equal(t, "dispatched", got[dispatched.Name])
	assert.Equal(t, "running", got[running.Name])
}

// Terminal rows are outside the partition: a finished task holds no slot.
func TestListActiveTasksForWorkerPage_ExcludesTerminalRows(t *testing.T) {
	f := newAssignedFixture(t)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	f.claimedAt(t, "live", base)
	done := f.claimedAt(t, "finished", base.Add(time.Minute))
	_, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID:              done.ID,
		Status:          "done",
		WorkerID:        f.w.ID,
		AssignmentEpoch: done.AssignmentEpoch,
		FinishedAt:      pgtype.Timestamptz{Time: base.Add(2 * time.Minute), Valid: true},
	})
	require.NoError(t, err)

	rows := listPage(t, f, 50)
	require.Len(t, rows, 1)
	assert.Equal(t, "live", rows[0].Name)
}

// assigned_at DESC NULLS LAST, id DESC. started_at would bury every dispatched
// row in a NULL bucket, and those are the rows the panel exists to show.
func TestListActiveTasksForWorkerPage_OrdersByAssignedAtDesc(t *testing.T) {
	f := newAssignedFixture(t)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	f.claimedAt(t, "oldest", base)
	f.claimedAt(t, "middle", base.Add(time.Hour))
	f.claimedAt(t, "newest", base.Add(2*time.Hour))

	rows := listPage(t, f, 50)
	require.Len(t, rows, 3)
	assert.Equal(t, []string{"newest", "middle", "oldest"},
		[]string{rows[0].Name, rows[1].Name, rows[2].Name})
}

// TestCountActiveTasksForWorker_MatchesTheListStatement pins the two predicates
// together: the count feeds the Slots KPI while the list feeds the table, and a
// disagreement between them shows an operator a fraction the rows contradict.
func TestCountActiveTasksForWorker_MatchesTheListStatement(t *testing.T) {
	f := newAssignedFixture(t)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	f.claimedAt(t, "a", base)
	runningAt(t, f, "b", base.Add(time.Minute))
	done := f.claimedAt(t, "c", base.Add(2*time.Minute))
	_, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID:              done.ID,
		Status:          "failed",
		WorkerID:        f.w.ID,
		AssignmentEpoch: done.AssignmentEpoch,
		FinishedAt:      pgtype.Timestamptz{Time: base.Add(3 * time.Minute), Valid: true},
	})
	require.NoError(t, err)

	n, err := f.q.CountActiveTasksForWorker(f.ctx, f.w.ID)
	require.NoError(t, err)
	rows := listPage(t, f, 50)
	assert.EqualValues(t, len(rows), n, "count and list must slice the same partition")
	assert.EqualValues(t, 2, n)
}

// The job-name lookup the handler uses instead of a JOIN, so no hand-written
// store.Task copy exists to drift as `tasks` gains columns.
func TestGetJobNamesByIDs_ReturnsNamesForTheGivenIDs(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()
	user := newTestUser(t, q, false)

	one, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "nightly-render", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	two, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "smoke", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)

	rows, err := q.GetJobNamesByIDs(ctx, []pgtype.UUID{one.ID, two.ID})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// pgtype.UUID is a comparable struct ([16]byte plus a bool), so it is a
	// valid map key and the assertion is positional rather than order-dependent.
	nameByID := map[pgtype.UUID]string{}
	for _, r := range rows {
		nameByID[r.ID] = r.Name
	}
	assert.Equal(t, "nightly-render", nameByID[one.ID])
	assert.Equal(t, "smoke", nameByID[two.ID])
}
```

`assignedFixture`, `newAssignedFixture`, `claimedAt`, `newTestQueries` and `newTestUser` already
exist in this package: `internal/store/tasks_assigned_at_integration_test.go:66-108` and
`internal/store/testhelper_test.go:20-84`. Do not redefine them.

- [ ] **Step 2: Run the test to verify it fails**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks
go test -tags integration -p 1 ./internal/store/... -run 'TestListActiveTasksForWorkerPage|TestCountActiveTasksForWorker|TestGetJobNamesByIDs' -v -timeout 600s
```

Expected: FAIL to compile, with
`undefined: store.ListActiveTasksForWorkerPageParams`, `q.ListActiveTasksForWorkerPage undefined`,
`q.CountActiveTasksForWorker undefined` and `q.GetJobNamesByIDs undefined`. A compile failure is the
correct RED for a statement that does not exist yet; do not proceed until you have seen those four
names in the error output.

- [ ] **Step 3: Add the SQL**

Append to `internal/store/query/tasks.sql` (after `CountTerminalTasksForWorker`, currently the last
statement, ending at line 943):

```sql

-- name: ListActiveTasksForWorkerPage :many
-- One page of the tasks CURRENTLY ASSIGNED to a worker. Read-only, and the only
-- thing it can break is what an operator sees.
--
-- READ THE ALLOW-LIST BACKWARDS. A new NON-TERMINAL status omitted here is
-- invisible in the panel and uncounted by CountActiveTasksForWorker, so an
-- operator sees an idle worker that is busy and a Slots KPI that under-reports
-- its load - no error, no log line. `preparing` is the live candidate. A new
-- TERMINAL status must stay OUT: a finished task holds no slot.
-- TestTasksStatusVocabularyIsExactly names this statement, and
-- TestListActiveTasksForWorkerPage_ReturnsBothAssignedStatuses pins the positive
-- arm - halving this IN list must turn it RED.
--
-- SELECT * so sqlc emits []store.Task and the handler calls toTaskResponse on a
-- real row. The job name comes from GetJobNamesByIDs rather than a JOIN, so
-- there is no hand-written store.Task copy to lose a column `tasks` gains.
--
-- Ordered by assigned_at, not started_at: started_at is NULL for every
-- `dispatched` row and a task spends the whole workspace sync as `dispatched`,
-- so ordering by it would bury the rows this panel exists to show. Every row
-- here has an assigned_at in practice (ClaimTaskForWorker is the only route into
-- this partition and stamps it in the same statement), but the column has no NOT
-- NULL constraint, so the NULLS LAST branch stays.
SELECT * FROM tasks
WHERE worker_id = sqlc.arg(worker_id)
  AND status IN ('dispatched', 'running')
  AND (
       NOT sqlc.arg(cursor_set)::bool
    OR (
       CASE WHEN sqlc.arg(cursor_is_null)::bool THEN
            assigned_at IS NULL AND id < sqlc.arg(cursor_id)::uuid
       ELSE
            (assigned_at IS NOT NULL AND
             (assigned_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
         OR assigned_at IS NULL
       END
   ))
ORDER BY assigned_at DESC NULLS LAST, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountActiveTasksForWorker :one
-- `total` for the page above, and the number the Slots KPI renders as used
-- slots. The status predicate must stay byte-identical to
-- ListActiveTasksForWorkerPage's - change both or neither, or the fraction an
-- operator reads contradicts the rows underneath it.
-- Do NOT serve one worker from CountActiveTasksByAllWorkers: it has no worker
-- filter and aggregates the whole table on every poll.
SELECT COUNT(*) FROM tasks
WHERE worker_id = sqlc.arg(worker_id)
  AND status IN ('dispatched', 'running');
```

Append to `internal/store/query/jobs.sql` (after `GetJobForUpdate`):

```sql

-- name: GetJobNamesByIDs :many
-- Job names for one page of tasks. Mirrors GetUserEmailsByIDs; the handler builds
-- a map and reads it per row. Bounded by the page limit, on the primary key.
SELECT id, name FROM jobs WHERE id = ANY($1::uuid[]);
```

- [ ] **Step 4: Regenerate, then apply the CRLF discipline**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks
git status --short
make generate
git status --short
git diff --ignore-all-space --stat
```

The first `git status --short` is the before-picture. sqlc emits LF and this is a CRLF repo, so
`make generate` rewrites line endings across every generated file. `git diff` alone normalizes that
churn away while `git status` still lists the files: **never conclude "nothing to revert" from
`git diff`.**

`git diff --ignore-all-space --stat` shows only the files with a real content change. Exactly two
files should appear: `internal/store/tasks.sql.go` and `internal/store/jobs.sql.go`. For every other
generated file listed by `git status --short`, revert it:

```
git checkout -- internal/store/<file>.sql.go
```

Then verify the regenerated content actually survived, because the revert step is where it gets
silently discarded:

```
git ls-files --eol internal/store/tasks.sql.go internal/store/jobs.sql.go
git diff --ignore-all-space -- internal/store/tasks.sql.go internal/store/jobs.sql.go
rg -n "ListActiveTasksForWorkerPage|CountActiveTasksForWorker" internal/store/tasks.sql.go
rg -n "GetJobNamesByIDs" internal/store/jobs.sql.go
```

Expected: `i/lf` on both paths; the diff shows the added constants, Params structs and methods; the
searches find the new symbols. If a search finds nothing, you reverted a file you needed - re-run
`make generate` and redo the revert more carefully.

- [ ] **Step 5: Run the test to verify it passes**

```
go test -tags integration -p 1 ./internal/store/... -run 'TestListActiveTasksForWorkerPage|TestCountActiveTasksForWorker|TestGetJobNamesByIDs' -v -timeout 600s
```

Expected: PASS, five tests.

- [ ] **Step 6: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/query/jobs.sql internal/store/tasks.sql.go internal/store/jobs.sql.go internal/store/list_active_tasks_for_worker_integration_test.go
git commit -m "feat(store): per-worker active task page, count, and job-name lookup

Two read-only statements over the existing idx_tasks_worker_active partial
index (no migration: its WHERE clause is this exact predicate), plus
GetJobNamesByIDs mirroring GetUserEmailsByIDs.

Two statements rather than a JOIN so sqlc emits []store.Task and the handler
calls toTaskResponse on a real row. jobs.go already carries six partial
hand-written store.Job copies from the JOIN pattern, each of which silently
drops any column the table gains; tasks has seventeen columns.

Ordered by assigned_at, not started_at: started_at is NULL for the whole
workspace sync, which is exactly the row this panel exists to surface."
```

---

### Task 2: the status-vocabulary census gains three members

**Owner:** `relay-backend-engineer`

The two new statements join the "currently assigned" partition, and reading the file turned up a
third member that was already missing (Refutation 1). This is required scope: a census that says six
when the answer is nine is a false claim about the complement.

**Files:**
- Modify: `internal/store/tasks_status_vocabulary_lockstep_test.go`

- [ ] **Step 1: Record the shape search**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks
rg -n "IN \('dispatched', 'running'\)" internal/store/query internal/store/migrations
```

Expected after Task 1: nine hits in `internal/store/query/tasks.sql` (the seven listed in Refutation
1 plus this slice's two) and two in `internal/store/migrations` (the `idx_tasks_worker_active`
predicate in 000018 and the one-off backfill in 000021). Paste this output into the commit message;
it is the evidence for the census, and it does not belong in a comment.

- [ ] **Step 2: Replace the assignment-partition group bullet**

In `internal/store/tasks_status_vocabulary_lockstep_test.go`, replace the whole bullet that starts
`//   - THE ASSIGNMENT-PARTITION GROUP` (lines 102-127 at HEAD) with:

```go
//   - THE ASSIGNMENT-PARTITION GROUP - GetActiveTasksForWorker,
//     ListGraceCandidates, RequeueTaskByID, RequeueWorkerTasks,
//     RequeueWorkerTasksIfEpoch, CountActiveTasksByAllWorkers,
//     ListActiveTasksForWorkerPage and CountActiveTasksForWorker
//     (query/tasks.sql), all carrying the identical
//     `status IN ('dispatched','running')`. THESE ARE INVERTED, exactly like
//     ListOverdueAssignedTasks.
//     A new NON-TERMINAL status omitted here fails OPEN in the damaging
//     direction at every one of them at once. Trace `preparing`, this file's own
//     named candidate: a task sitting in it through a long P4 sync is invisible
//     to GetActiveTasksForWorker, so reconcile never sees it and never requeues
//     it; invisible to ListGraceCandidates, so no grace timer covers it;
//     unmatched by all three requeue statements, so neither a disconnect nor an
//     admin disable releases it; uncounted by CountActiveTasksByAllWorkers, so
//     the dispatcher reads the slot it holds as free and can overcommit the
//     worker; absent from the worker-detail Current-tasks panel and undercounted
//     by its Slots KPI (ListActiveTasksForWorkerPage, CountActiveTasksForWorker),
//     so an operator sees an idle worker that is busy; and already unswept by
//     ListOverdueAssignedTasks. It holds its worker slot and its job FOREVER,
//     with no error and no log line - and it is outside idx_tasks_worker_active
//     as well, whose WHERE clause is a copy of this same predicate that nothing
//     on this list reads: a status added to the statements but not to the index
//     turns the two panel queries into sequential scans rather than making them
//     wrong. A new non-terminal status MUST BE ADDED to all eight.
//     A new TERMINAL status must stay OUT. For the three requeue statements the
//     reason is that they WRITE, so admitting one would let a requeue resurrect
//     a finished task, which is the guarantee
//     TestRequeueTaskByID_TerminalTaskIsNotResurrected pins. For the two panel
//     statements the reason is different: they are read-only and can admit no
//     write at all, but a terminal task holds no slot, so including one would
//     over-report used slots on a card an operator reads as capacity.
//     The positive arm is pinned too, and needs to stay pinned:
//     TestRequeueTaskByID_RequeuesARunningTaskForItsAssignee exists because
//     halving that IN list to ('dispatched') left the store, worker, scheduler
//     and api suites ALL GREEN while silently stranding reconcile's dominant
//     case, and
//     TestListActiveTasksForWorkerPage_ReturnsBothAssignedStatuses is the same
//     guard for the panel statement.
```

- [ ] **Step 3: Replace the two ordinal headings and the ratio**

Three anchored replacements in the same file.

Replace line 138:

```go
// THE FOURTEENTH ENTRY IS NOT A STATEMENT, and it is here because the list above
```

with:

```go
// ONE ENTRY IS NOT A STATEMENT, and it is here because the list above
```

Replace line 157:

```go
// THE FIFTEENTH ENTRY IS NOT A STATEMENT EITHER, and it is not even in the server
```

with:

```go
// A SECOND ENTRY IS NOT A STATEMENT EITHER, and it is not even in the server
```

Replace the sentence at lines 195-201 that begins `Count them before assuming the fail-closed
default` and runs to the end of the comment, with:

```go
// default: the inverted sites are AppendTaskLog, ListOverdueAssignedTasks, and
// every member of the assignment-partition group above. The two non-statement
// entries are not among them; neither gates a write, so neither is fail-open or
// fail-closed in that sense. Drift in the first mislabels a counter. Drift in the
// second silently breaks a promise the CLI makes to a shell script, which is the
// fail-OPEN direction for the one thing that entry does control.
```

Keep the preceding sentence (`The allow-list form of these predicates ... folded into the list.`)
byte-identical up to `Count them before assuming the fail-closed`.

Ordinals and ratios are why this hunk exists: the file's own "seven of the thirteen" and
"FOURTEENTH"/"FIFTEENTH" were already off by one before this slice, because
`CountTerminalTasksForWorker` was added without renumbering. Enumerations are checkable by reading
the list; a total is not.

- [ ] **Step 4: Update the failure message**

Replace the `require.Equal` message (lines 219-239) with:

```go
	require.Equal(t, want, got,
		"tasks.status vocabulary changed - read this test's comment before updating it. These statements slice "+
			"this set: UpdateTaskStatus, IncrementTaskRetryCount, RecomputeJobStatus, RetryJobTasks, "+
			"SelectRetryableTaskIDs, AppendTaskLog, ListOverdueAssignedTasks, GetActiveTasksForWorker, "+
			"ListGraceCandidates, RequeueTask, RequeueTaskByID, RequeueWorkerTasks, RequeueWorkerTasksIfEpoch, "+
			"CountActiveTasksByAllWorkers, ListActiveTasksForWorkerPage and CountActiveTasksForWorker. "+
			"Revisit ALL OF THEM. AppendTaskLog and every statement carrying the 'currently assigned' partition "+
			"fail OPEN in the damaging direction. A new NON-TERMINAL status omitted from AppendTaskLog's first "+
			"arm silently discards 100% of that state's log output. One omitted from the nine that carry the "+
			"'currently assigned' partition - ListOverdueAssignedTasks, GetActiveTasksForWorker, "+
			"ListGraceCandidates, RequeueTaskByID, RequeueWorkerTasks, RequeueWorkerTasksIfEpoch, "+
			"CountActiveTasksByAllWorkers, ListActiveTasksForWorkerPage and CountActiveTasksForWorker - means a "+
			"task in that state is never seen by reconcile, never covered by a grace timer, never requeued on "+
			"disconnect or disable, never swept, counted as a free slot by the dispatcher, and missing from the "+
			"worker-detail panel and its Slots KPI: it holds its worker slot and its job forever, with no error "+
			"and no log line. RequeueTask's narrower 'dispatched'-only predicate is deliberate - see its own "+
			"comment before touching it. THERE ARE ALSO TWO NON-STATEMENT SITES. taskStatusIsWritable in "+
			"internal/worker/taskstatus_fence_counters.go mirrors UpdateTaskStatus's allow-list in Go to label "+
			"fence-rejection counters. It gates nothing, so drift there mislabels a number rather than admitting "+
			"a write - but a new non-terminal status left out of it makes every rejection for that state read as "+
			"a healthy race. taskIsTerminal and jobIsTerminal in internal/cli/logs.go are the CLI's copies, and "+
			"jobIsTerminal is the only site on this list slicing the JOBS vocabulary. A new TERMINAL task status "+
			"omitted from taskIsTerminal means relay logs never fetches that task's log while still exiting 0, "+
			"which it documents as meaning every task's log printed in full; a new terminal JOB status omitted "+
			"from jobIsTerminal makes relay logs hang until the connection drops and then report 'connection "+
			"lost' about a job that finished long ago")
```

The one number kept is "the nine that carry", and it is immediately followed by the nine names, so it
is checkable in place.

- [ ] **Step 5: Run the guard and check the edit did not mangle the file**

```
go test -tags integration -p 1 ./internal/store/... -run TestTasksStatusVocabularyIsExactly -v -timeout 300s
git diff --stat -- internal/store/tasks_status_vocabulary_lockstep_test.go
git ls-files --eol internal/store/tasks_status_vocabulary_lockstep_test.go
```

Expected: PASS (the vocabulary is unchanged; only prose moved). The diffstat must be on the order of
50 changed lines, not hundreds - a three-digit number means the editor rewrote line endings. `eol`
must read `i/lf`.

- [ ] **Step 6: Commit**

```bash
git add internal/store/tasks_status_vocabulary_lockstep_test.go
git commit -m "test(store): the assignment-partition census goes from six members to nine

Two of the three are this slice's new statements. The third,
CountActiveTasksByAllWorkers, was already a member and already unlisted: it
carries status IN ('dispatched','running') and ListOverdueAssignedTasks's own
comment names it as part of the same set, but the guard did not.

Established by shape search rather than by opening the named subjects, since a
census is a claim about the complement: ripgrep for the IN-list literal across
internal/store/query and internal/store/migrations returns nine statement hits
plus two non-statement sites (the idx_tasks_worker_active predicate in 000018
and the 000021 backfill).

Also replaces 'seven of the thirteen STATEMENTS' and the FOURTEENTH/FIFTEENTH
ordinal headings with enumerations. Those totals were already wrong at HEAD:
the failure message named thirteen statements, the comment bullets named
fourteen, and the ordinals predate CountTerminalTasksForWorker being added
without a renumber. A total drifts silently; an enumeration is checkable by
reading the list it sits above."
```

---

### Task 3: the response struct and the sort spec

**Owner:** `relay-backend-engineer`

**Files:**
- Modify: `internal/api/workers.go` (add after `RevokedWorkersSortSpec`, line 144, and after
  `workersRowKeyByLastSeen`, line 164)
- Test: `internal/api/worker_tasks_test.go` (create; **default lane**, package `api`, no build tag)

Lane note: `workers_response_test.go` is in package `api` with no build tag, and every test that
needs a database is `//go:build integration` in package `api_test`. These assertions need neither a
server nor a database, so they go in the default lane.

- [ ] **Step 1: Write the failing test**

Create `internal/api/worker_tasks_test.go`:

```go
package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonKeys returns every json key a struct serializes, following embedded
// structs the way encoding/json does.
func jsonKeys(t reflect.Type) map[string]struct{} {
	out := map[string]struct{}{}
	for _, f := range reflect.VisibleFields(t) {
		if f.Anonymous {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		out[strings.Split(tag, ",")[0]] = struct{}{}
	}
	return out
}

// TestWorkerTaskResponseCarriesEveryTaskResponseField goes RED if the embedding
// is ever replaced by a hand-written copy that drops a field. The endpoint must
// not be able to drift from GET /v1/tasks/{id} on the task's own fields.
func TestWorkerTaskResponseCarriesEveryTaskResponseField(t *testing.T) {
	base := jsonKeys(reflect.TypeOf(taskResponse{}))
	require.NotEmpty(t, base, "control: taskResponse must expose json keys")

	got := jsonKeys(reflect.TypeOf(workerTaskResponse{}))
	for k := range base {
		assert.Contains(t, got, k, "workerTaskResponse must carry taskResponse's %q", k)
	}
	for _, extra := range []string{"job_id", "job_name", "assigned_at", "started_at"} {
		assert.Contains(t, got, extra)
	}
}

// assignment_epoch is a fence token. Publishing (task id, current epoch) pairs
// for a named worker to any authenticated user hands out exactly the two values
// RequeueTask's comment says a forged status update would otherwise have to
// guess.
func TestWorkerTaskResponseDoesNotDeclareAssignmentEpoch(t *testing.T) {
	got := jsonKeys(reflect.TypeOf(workerTaskResponse{}))
	assert.NotContains(t, got, "assignment_epoch")
}

// The endpoint serves one order. The spec exists so parsePage resolves the
// default and tags the cursor with it; the handler refuses anything else.
func TestWorkerTasksSortSpecAllowsOnlyAssignedAt(t *testing.T) {
	canon, kind, err := parseSort("", WorkerTasksSortSpec)
	require.NoError(t, err)
	assert.Equal(t, "-assigned_at", canon)
	assert.Equal(t, SortKeyTimestamp, kind)

	canon, _, err = parseSort("assigned_at", WorkerTasksSortSpec)
	require.NoError(t, err, "the ascending form must resolve; the handler refuses it, not parseSort")
	assert.Equal(t, "assigned_at", canon)

	for _, bad := range []string{"name", "-name", "status", "created_at", "-started_at", "id"} {
		_, _, err := parseSort(bad, WorkerTasksSortSpec)
		assert.Error(t, err, "sort key %q must be refused", bad)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```
go test ./internal/api/... -run 'TestWorkerTaskResponse|TestWorkerTasksSortSpec' -v -timeout 60s
```

Expected: FAIL to compile, `undefined: workerTaskResponse` and `undefined: WorkerTasksSortSpec`.

- [ ] **Step 3: Add the struct, the spec and the row key**

In `internal/api/workers.go`, add after `RevokedWorkersSortSpec` (ends line 144):

```go
// WorkerTasksSortSpec drives GET /v1/workers/{id}/tasks. The endpoint serves one
// order; the assigned_at key exists so the "-assigned_at" default resolves in
// parseSort and tags the cursor. handleListWorkerTasks refuses an ascending
// request, as handleListRevokedWorkers does.
var WorkerTasksSortSpec = SortSpec{
	Default: "-assigned_at",
	Keys: map[string]SortKeyKind{
		"assigned_at": SortKeyTimestamp,
	},
}
```

Add after `workersRowKeyByLastSeen` (ends line 164):

```go
// workerTaskResponse is one currently-assigned task. It EMBEDS taskResponse so
// this endpoint cannot drift from GET /v1/tasks/{id} on the task's own fields,
// exactly as disableWorkerResponse embeds workerResponse.
// assignment_epoch is deliberately absent and must stay absent: it is a fence
// token, and this response would otherwise publish live (task id, epoch) pairs
// for a named worker to any authenticated user - the two values RequeueTask's
// comment says a forged status update would otherwise have to guess.
// TestWorkerTaskResponseDoesNotDeclareAssignmentEpoch and
// TestListWorkerTasks_DoesNotExposeAssignmentEpoch pin the absence.
type workerTaskResponse struct {
	taskResponse
	JobID      string     `json:"job_id"`
	JobName    string     `json:"job_name"`
	AssignedAt *time.Time `json:"assigned_at,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
}

func toWorkerTaskResponse(t store.Task) workerTaskResponse {
	resp := workerTaskResponse{
		taskResponse: toTaskResponse(t, nil),
		JobID:        uuidStr(t.JobID),
	}
	if t.AssignedAt.Valid {
		at := t.AssignedAt.Time
		resp.AssignedAt = &at
	}
	if t.StartedAt.Valid {
		st := t.StartedAt.Time
		resp.StartedAt = &st
	}
	return resp
}

// A nil *time.Time is how encodeCursorV2 represents a NULL sort value, which is
// the NULLS LAST tail of the query's order.
func workerTasksRowKey(t store.Task) (anySortVal, pgtype.UUID) {
	if !t.AssignedAt.Valid {
		return (*time.Time)(nil), t.ID
	}
	at := t.AssignedAt.Time
	return &at, t.ID
}
```

`time`, `store` and `pgtype` are already imported by `workers.go` (lines 9-14).

- [ ] **Step 4: Run the test to verify it passes**

```
go test ./internal/api/... -run 'TestWorkerTaskResponse|TestWorkerTasksSortSpec' -v -timeout 60s
go vet ./internal/api/...
```

Expected: PASS, three tests. `go vet` clean.

- [ ] **Step 5: Commit**

```bash
git add internal/api/workers.go internal/api/worker_tasks_test.go
git commit -m "feat(api): workerTaskResponse, WorkerTasksSortSpec, and the assigned_at row key

The response EMBEDS taskResponse rather than copying it, so the new endpoint
cannot drift from GET /v1/tasks/{id} on the task's own fields; the reflection
test goes RED if somebody replaces the embedding with a hand-written struct.

assignment_epoch is absent by decision, not by omission, and the absence is
pinned by a test rather than trusted to the struct definition."
```

---

### Task 4: the handler and the route

**Owner:** `relay-backend-engineer`

**Files:**
- Modify: `internal/api/workers.go` (handler, appended after `handleGetWorker`, line 378)
- Modify: `internal/api/server.go:154` (route)
- Test: `internal/api/workers_tasks_integration_test.go` (create; **integration lane**, package
  `api_test`)

Lane note: every worker handler test that needs a database is in the integration lane
(`workers_delete_integration_test.go`, `workers_sort_integration_test.go`,
`workers_list_revoked_integration_test.go`, ...). Follow that.

- [ ] **Step 1: Write the failing test**

Create `internal/api/workers_tasks_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedJob inserts a job directly so tests control its name.
func seedJob(t *testing.T, pool *pgxpool.Pool, owner store.User, name string) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO jobs (name, priority, submitted_by, labels)
		 VALUES ($1, 'normal', $2, '{}'::jsonb) RETURNING id`,
		name, owner.ID,
	).Scan(&id))
	return id
}

// seedWorkerTask inserts one task row in a chosen state. Direct SQL, matching
// seedWorker and seedLogRow: this suite tests what the READ endpoint puts on the
// wire, and the write paths have their own store-level tests.
func seedWorkerTask(t *testing.T, pool *pgxpool.Pool, jobID, workerID, name, status string,
	assignedAt *time.Time, startedAt *time.Time) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO tasks (job_id, name, commands, env, requires, retries, status, worker_id, assigned_at, started_at)
		 VALUES ($1, $2, '[["echo","x"]]'::jsonb, '{}'::jsonb, '{}'::jsonb, 0, $3, $4, $5, $6)
		 RETURNING id`,
		jobID, name, status, workerID, assignedAt, startedAt,
	).Scan(&id))
	return id
}

func getWorkerTasks(t *testing.T, srv *api.Server, token, workerID, query string) (int, pageEnvelope[map[string]any]) {
	t.Helper()
	url := "/v1/workers/" + workerID + "/tasks"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp pageEnvelope[map[string]any]
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	}
	return rec.Code, resp
}

func names(p pageEnvelope[map[string]any]) []string {
	out := make([]string, 0, len(p.Items))
	for _, it := range p.Items {
		out = append(out, it["name"].(string))
	}
	return out
}

// Only the assignment partition. Seeds one task per status of the full six-value
// vocabulary and asserts exactly the two assigned ones come back.
func TestListWorkerTasks_ReturnsOnlyTheAssignmentPartition(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Part", "partition@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	for i, status := range []string{"pending", "dispatched", "running", "done", "failed", "timed_out"} {
		ts := at.Add(time.Duration(i) * time.Minute)
		seedWorkerTask(t, pool, jobID, workerID, "t-"+status, status, &ts, nil)
	}

	code, p := getWorkerTasks(t, srv, token, workerID, "")
	require.Equal(t, http.StatusOK, code)
	assert.ElementsMatch(t, []string{"t-dispatched", "t-running"}, names(p))
}

// Rows are scoped to the path worker, in both directions.
func TestListWorkerTasks_DoesNotLeakAnotherWorkersTasks(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Scope", "scope@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	a := seedWorker(t, pool, "rig-a", "online", nil)
	b := seedWorker(t, pool, "rig-b", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedWorkerTask(t, pool, jobID, a, "on-a", "running", &at, &at)
	seedWorkerTask(t, pool, jobID, b, "on-b", "running", &at, &at)

	_, pa := getWorkerTasks(t, srv, token, a, "")
	assert.Equal(t, []string{"on-a"}, names(pa))
	_, pb := getWorkerTasks(t, srv, token, b, "")
	assert.Equal(t, []string{"on-b"}, names(pb))
}

// The posture pin. This goes RED if the route is ever wrapped in admin(...):
// both neighbours (GET /v1/workers/{id} and /metrics) are auth-only, and every
// task read route is auth-only under an explicit render-farm-semantics comment.
func TestListWorkerTasks_IsReadableByANonAdmin(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Plain", "plain@tasks-test.com", false)
	require.False(t, user.IsAdmin, "control: this user must not be an admin")
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)

	code, _ := getWorkerTasks(t, srv, token, workerID, "")
	assert.Equal(t, http.StatusOK, code)
}

func TestListWorkerTasks_RequiresAuthentication(t *testing.T) {
	srv, _, pool := newTestServerWithPool(t)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)

	code, _ := getWorkerTasks(t, srv, "", workerID, "")
	assert.Equal(t, http.StatusUnauthorized, code)
}

func TestListWorkerTasks_UnknownWorkerIs404AndMalformedIdIs400(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "NF", "notfound@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)

	code, _ := getWorkerTasks(t, srv, token, "3f7c1b6e-0000-4000-8000-000000000000", "")
	assert.Equal(t, http.StatusNotFound, code)

	code, _ = getWorkerTasks(t, srv, token, "not-a-uuid", "")
	assert.Equal(t, http.StatusBadRequest, code)
}

// limit is validated, not clamped, and the endpoint serves exactly one order.
func TestListWorkerTasks_RejectsBadLimitAndUnsupportedSort(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Lim", "limits@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)

	for _, query := range []string{"limit=0", "limit=201", "limit=abc", "sort=name", "sort=assigned_at", "cursor=zzz"} {
		code, _ := getWorkerTasks(t, srv, token, workerID, query)
		assert.Equal(t, http.StatusBadRequest, code, "query %q must be a 400", query)
	}

	code, _ := getWorkerTasks(t, srv, token, workerID, "limit=200&sort=-assigned_at")
	assert.Equal(t, http.StatusOK, code, "control: the supported limit and sort are accepted")
}
```

`newTestServerWithPool` is `internal/api/tasks_integration_test.go:60`; `seedWorker` is
`internal/api/workers_sort_integration_test.go:30`; `createTestUser` / `createTestToken` are
`internal/api/api_test.go:37,48`; `pageEnvelope[T]` is `internal/api/testhelper_test.go:21`. Do not
redefine any of them. `fmt` is deliberately NOT imported here; Task 6 adds it with its first use.

- [ ] **Step 2: Run the test to verify it fails**

```
go test -tags integration -p 1 ./internal/api/... -run TestListWorkerTasks -v -timeout 900s
```

Expected: FAIL. `TestListWorkerTasks_ReturnsOnlyTheAssignmentPartition` fails with
`Not equal: expected 200, actual 404` because Go's `ServeMux` has no pattern for
`/v1/workers/{id}/tasks`. Confirm the failure is the 404 from the mux, not a compile error and not a
panic.

- [ ] **Step 3: Write the handler and register the route**

In `internal/api/workers.go`, add after `handleGetWorker` (ends line 378):

```go
// handleListWorkerTasks lists the tasks CURRENTLY ASSIGNED to one worker, newest
// assignment first. Read-only, and auth-only rather than admin: both neighbouring
// worker reads and every task read route are auth-only, and this is a projection
// of task rows keyed by worker, so gating it on admin would be stricter than
// either thing it is made of.
//
// The worker is read before the page is built, so an unknown id is a 404 rather
// than an empty list - the same ordering handleGetTaskLogs uses. That read runs
// before parsePage, so an unknown worker with a bad ?limit= is a 404, not a 400.
// A revoked worker is returned by GetWorker and is therefore not a 404 here,
// matching GET /v1/workers/{id}.
//
// items and total come from two statements, so under concurrent dispatch they
// can disagree by one for an instant. Every list endpoint here has that property.
func (s *Server) handleListWorkerTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}

	if _, err := s.q.GetWorker(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	pp, ok := parsePage(w, r, WorkerTasksSortSpec)
	if !ok {
		return
	}
	// The SQL ordering is fixed, so an ascending request cannot be honored.
	// Refuse it rather than silently returning descending rows, exactly as
	// handleListRevokedWorkers does.
	if pp.Sort != "-assigned_at" {
		writeError(w, http.StatusBadRequest, "worker tasks can only be sorted by -assigned_at")
		return
	}

	total, err := s.q.CountActiveTasksForWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count worker tasks failed")
		return
	}

	rows, err := s.q.ListActiveTasksForWorkerPage(ctx, store.ListActiveTasksForWorkerPageParams{
		WorkerID:     id,
		CursorSet:    pp.Cursor.Set,
		CursorIsNull: pp.Cursor.IsNull,
		CursorTs:     pp.CursorTs(),
		CursorID:     pp.Cursor.ID,
		PageLimit:    pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list worker tasks failed")
		return
	}

	items, next := buildPage(rows, pp.Limit, pp.Sort, toWorkerTaskResponse, workerTasksRowKey)
	writeJSON(w, http.StatusOK, page[workerTaskResponse]{Items: items, NextCursor: next, Total: total})
}
```

In `internal/api/server.go`, add immediately after line 154 (the `/metrics` route):

```go
	mux.Handle("GET /v1/workers/{id}/tasks", auth(http.HandlerFunc(s.handleListWorkerTasks)))
```

- [ ] **Step 4: Run the test to verify it passes**

```
go test -tags integration -p 1 ./internal/api/... -run TestListWorkerTasks -v -timeout 900s
go vet ./internal/api/...
```

Expected: PASS, six tests. `job_name` is still empty on every item; Task 5 fills it.

- [ ] **Step 5: Commit**

```bash
git add internal/api/workers.go internal/api/server.go internal/api/workers_tasks_integration_test.go
git commit -m "feat(api): GET /v1/workers/{id}/tasks

Answers one question - what is assigned to this worker right now - over the
existing idx_tasks_worker_active partial index. auth(...), not admin: both
neighbouring worker reads and every task read route are auth-only, and this is
a projection of task rows keyed by worker.

Fixed order with a one-key SortSpec and an explicit refusal of the ascending
form, the same shape handleListRevokedWorkers uses, so limit and cursor
validation come for free without advertising a sort the SQL cannot serve."
```

---

### Task 5: the job name

**Owner:** `relay-backend-engineer`

**Files:**
- Modify: `internal/api/workers.go` (`handleListWorkerTasks`, plus a new `fillJobNames`)
- Modify: `internal/api/workers_tasks_integration_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/api/workers_tasks_integration_test.go`:

```go
// job_name comes from a second statement over the page's job ids rather than a
// JOIN, so two tasks of the same job must both carry it.
func TestListWorkerTasks_CarriesTheJobName(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "JN", "jobname@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	shared := seedJob(t, pool, user, "nightly-render")
	other := seedJob(t, pool, user, "smoke")

	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedWorkerTask(t, pool, shared, workerID, "shot-1", "running", &at, &at)
	seedWorkerTask(t, pool, shared, workerID, "shot-2", "dispatched", &at, nil)
	seedWorkerTask(t, pool, other, workerID, "smoke-1", "running", &at, &at)

	code, p := getWorkerTasks(t, srv, token, workerID, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 3)

	byName := map[string]string{}
	for _, it := range p.Items {
		byName[it["name"].(string)] = it["job_name"].(string)
	}
	assert.Equal(t, "nightly-render", byName["shot-1"])
	assert.Equal(t, "nightly-render", byName["shot-2"])
	assert.Equal(t, "smoke", byName["smoke-1"])
}
```

- [ ] **Step 2: Run the test to verify it fails**

```
go test -tags integration -p 1 ./internal/api/... -run TestListWorkerTasks_CarriesTheJobName -v -timeout 600s
```

Expected: FAIL with `expected: "nightly-render" actual: ""` - the field is on the wire but empty.

- [ ] **Step 3: Fill it in**

In `handleListWorkerTasks`, replace the final two statements (`items, next := buildPage(...)` and the
`writeJSON`) and append the new helper, so the tail of the function and the helper read:

```go
	items, next := buildPage(rows, pp.Limit, pp.Sort, toWorkerTaskResponse, workerTasksRowKey)
	if err := s.fillJobNames(ctx, items); err != nil {
		writeError(w, http.StatusInternalServerError, "list worker tasks failed")
		return
	}
	writeJSON(w, http.StatusOK, page[workerTaskResponse]{Items: items, NextCursor: next, Total: total})
}

// fillJobNames resolves job_name for one page of tasks in a single lookup on the
// jobs primary key, bounded by the page limit. It is a second statement rather
// than a JOIN so the list query stays a bare SELECT * that sqlc emits as
// []store.Task: a JOIN row would have to be hand-copied into a store.Task to
// reach toTaskResponse, and such a copy silently loses any column tasks gains.
// tasks.job_id is NOT NULL with an FK, so a missing name is a database fault,
// not a normal absence - hence an error rather than an empty string.
func (s *Server) fillJobNames(ctx context.Context, items []workerTaskResponse) error {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	ids := make([]pgtype.UUID, 0, len(items))
	for _, it := range items {
		if _, ok := seen[it.JobID]; ok {
			continue
		}
		seen[it.JobID] = struct{}{}
		id, err := parseUUID(it.JobID)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	rows, err := s.q.GetJobNamesByIDs(ctx, ids)
	if err != nil {
		return err
	}
	nameByID := make(map[string]string, len(rows))
	for _, row := range rows {
		nameByID[uuidStr(row.ID)] = row.Name
	}
	for i := range items {
		items[i].JobName = nameByID[items[i].JobID]
	}
	return nil
}
```

Add `"context"` to the import block at the top of `internal/api/workers.go` (it currently imports
`encoding/json`, `errors`, `log`, `net/http`, `strconv`, `time`).

- [ ] **Step 4: Run the test to verify it passes**

```
go test -tags integration -p 1 ./internal/api/... -run TestListWorkerTasks -v -timeout 900s
go vet ./internal/api/...
```

Expected: PASS, seven tests.

- [ ] **Step 5: Commit**

```bash
git add internal/api/workers.go internal/api/workers_tasks_integration_test.go
git commit -m "feat(api): resolve job_name for the worker tasks page

One lookup on the jobs primary key over the page's distinct job ids, mirroring
GetUserEmailsByIDs, instead of a JOIN. The JOIN version would force a
hand-written store.Task copy in the handler to reach toTaskResponse, and every
one of the six such copies already in jobs.go silently drops any column its
table gains."
```

---

### Task 6: page semantics, guarded by mutation

**Owner:** `relay-backend-engineer`

These five properties are implemented by Tasks 1, 4 and 5. Writing the tests after the code means
there is no natural RED, so each one is proved by a deliberate mutation instead. **A test that
survives its mutation is not a test; fix it before moving on.**

**Files:**
- Modify: `internal/api/workers_tasks_integration_test.go` (append, and add `"fmt"` to its imports)

- [ ] **Step 1: Write the tests**

Add `"fmt"` to the import block of `internal/api/workers_tasks_integration_test.go` (its first use is
in `TestListWorkerTasks_TotalIsTheActiveCount` below), then append:

```go
// Order is assigned_at DESC with id DESC as the tiebreak. uuid comparison in
// Postgres is bytewise, and the canonical text form is those bytes in hex, so
// sorting the two ids as strings agrees with the SQL.
func TestListWorkerTasks_OrdersByAssignedAtDescThenIDDesc(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Ord", "order@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	newest := base.Add(2 * time.Hour)
	middle := base.Add(time.Hour)

	seedWorkerTask(t, pool, jobID, workerID, "newest", "running", &newest, &newest)
	seedWorkerTask(t, pool, jobID, workerID, "middle", "running", &middle, &middle)
	tieA := seedWorkerTask(t, pool, jobID, workerID, "tie-a", "dispatched", &base, nil)
	tieB := seedWorkerTask(t, pool, jobID, workerID, "tie-b", "dispatched", &base, nil)

	code, p := getWorkerTasks(t, srv, token, workerID, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 4)
	assert.Equal(t, []string{"newest", "middle"}, names(p)[:2])

	firstTie, secondTie := "tie-a", "tie-b"
	if tieB > tieA {
		firstTie, secondTie = "tie-b", "tie-a"
	}
	assert.Equal(t, []string{firstTie, secondTie}, names(p)[2:],
		"equal assigned_at must break on id DESC")
}

// Crosses a REAL page boundary: limit=1 over three rows, following next_cursor,
// asserting the union is the full set with no duplicate and no skip.
func TestListWorkerTasks_PagesAcrossARealBoundary(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Pg", "paging@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	for i, n := range []string{"a", "b", "c"} {
		ts := base.Add(time.Duration(i) * time.Hour)
		seedWorkerTask(t, pool, jobID, workerID, n, "running", &ts, &ts)
	}

	var seen []string
	cursor := ""
	for i := 0; i < 4; i++ {
		query := "limit=1"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		code, p := getWorkerTasks(t, srv, token, workerID, query)
		require.Equal(t, http.StatusOK, code)
		seen = append(seen, names(p)...)
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	assert.Equal(t, []string{"c", "b", "a"}, seen, "no duplicate and no skip across the boundary")
}

// total is the ACTIVE count, on every page: not the worker's total task count
// and not a table count.
func TestListWorkerTasks_TotalIsTheActiveCount(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Tot", "total@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedWorkerTask(t, pool, jobID, workerID, "live-1", "running", &base, &base)
	seedWorkerTask(t, pool, jobID, workerID, "live-2", "dispatched", &base, nil)
	for i, status := range []string{"done", "failed", "timed_out"} {
		ts := base.Add(time.Duration(i) * time.Minute)
		seedWorkerTask(t, pool, jobID, workerID, "old-"+status, status, &ts, &ts)
	}

	code, p := getWorkerTasks(t, srv, token, workerID, "limit=1")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	assert.EqualValues(t, 2, p.Total, "total is the active count, not the row count of this page")

	code, p = getWorkerTasks(t, srv, token, workerID, fmt.Sprintf("limit=1&cursor=%s", p.NextCursor))
	require.Equal(t, http.StatusOK, code)
	assert.EqualValues(t, 2, p.Total, "and it is the same on every page")
}

// assignment_epoch is a fence token and appears nowhere. Decoded into
// map[string]any so this sees the WIRE, not the struct definition.
func TestListWorkerTasks_DoesNotExposeAssignmentEpoch(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Ep", "epoch@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedWorkerTask(t, pool, jobID, workerID, "shot-1", "running", &at, &at)

	req := httptest.NewRequest("GET", "/v1/workers/"+workerID+"/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.NotContains(t, envelope, "assignment_epoch")
	items, ok := envelope["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1, "control: the assertion below must have a row to inspect")
	for _, raw := range items {
		item := raw.(map[string]any)
		assert.NotContains(t, item, "assignment_epoch")
		assert.Contains(t, item, "id", "control: the item decoded into real keys")
	}
}

// A dispatched row mid-workspace-sync has no started_at. It must still be
// returned, and the key must be ABSENT rather than a zero timestamp.
func TestListWorkerTasks_ADispatchedTaskWithNoStartTimeIsReturned(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Sync", "sync@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedWorkerTask(t, pool, jobID, workerID, "sync-depot", "dispatched", &at, nil)

	code, p := getWorkerTasks(t, srv, token, workerID, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	assert.Equal(t, "sync-depot", p.Items[0]["name"])
	assert.NotContains(t, p.Items[0], "started_at", "an absent start time must be absent, not a zero time")
	assert.Contains(t, p.Items[0], "assigned_at", "control: the sibling timestamp IS present")
}
```

- [ ] **Step 2: Run them and expect GREEN**

```
go test -tags integration -p 1 ./internal/api/... -run TestListWorkerTasks -v -timeout 900s
```

Expected: PASS, twelve tests. Green is expected here; step 3 is what makes these tests mean
something.

- [ ] **Step 3: Prove each test with its mutation**

Before mutating, save a clean copy of each file you are about to edit, because **you must never
`git checkout --` to revert a mutation**: uncommitted work in the same file would be destroyed.

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks
cp internal/api/workers.go internal/api/workers.go.bak
cp internal/store/query/tasks.sql internal/store/query/tasks.sql.bak
```

Apply each mutation, run the named test, confirm it goes RED, then restore with
`cp <file>.bak <file>` and re-run to confirm GREEN again.

| # | Mutation | Must go RED |
|---|----------|-------------|
| M1 | In `handleListWorkerTasks`, pass `CountTerminalTasksForWorker(ctx, id)` as `total` instead of `CountActiveTasksForWorker`. | `TestListWorkerTasks_TotalIsTheActiveCount` |
| M2 | Add `AssignmentEpoch int32 \`json:"assignment_epoch"\`` to `workerTaskResponse` and set it in `toWorkerTaskResponse` from `t.AssignmentEpoch`. | `TestListWorkerTasks_DoesNotExposeAssignmentEpoch` and `TestWorkerTaskResponseDoesNotDeclareAssignmentEpoch` |
| M3 | Drop the `,omitempty` from `StartedAt`'s json tag. | `TestListWorkerTasks_ADispatchedTaskWithNoStartTimeIsReturned` |
| M4 | In `ListActiveTasksForWorkerPage`, change `LIMIT sqlc.arg(page_limit)::int + 1` to `LIMIT sqlc.arg(page_limit)::int`, then `make generate` (and apply the CRLF discipline). | `TestListWorkerTasks_PagesAcrossARealBoundary` |
| M5 | In `ListActiveTasksForWorkerPage`, change `ORDER BY assigned_at DESC NULLS LAST, id DESC` to `ORDER BY assigned_at DESC NULLS LAST, id ASC`, then `make generate`. | `TestListWorkerTasks_OrdersByAssignedAtDescThenIDDesc` |

M4 and M5 touch generated code. After restoring `tasks.sql` from the `.bak`, re-run `make generate`
and redo the CRLF procedure from Task 1 Step 4, then confirm `git diff --ignore-all-space` is empty
across `internal/store/` before continuing. Delete both `.bak` files when finished, and confirm
`git status --short` lists no stray file.

If any mutation leaves its test GREEN, the test does not pin the property. Strengthen it and re-run.

- [ ] **Step 4: Commit**

```bash
git add internal/api/workers_tasks_integration_test.go
git commit -m "test(api): pin worker-tasks paging, total, ordering and epoch absence

Written after the implementation, so each is proved by mutation rather than by
a natural RED, and each mutation was run and observed:

  M1 total from CountTerminalTasksForWorker  -> TotalIsTheActiveCount RED
  M2 assignment_epoch added to the response  -> DoesNotExposeAssignmentEpoch RED
  M3 omitempty dropped from started_at       -> ADispatchedTaskWithNoStartTime RED
  M4 LIMIT+1 reduced to LIMIT                -> PagesAcrossARealBoundary RED
  M5 id DESC tiebreak flipped to id ASC      -> OrdersByAssignedAtDescThenIDDesc RED

The paging test crosses a real boundary rather than fitting one page under a
200-row limit, which is the gap the CLI integration lane still has for lists."
```

---

### Task 7: README

**Owner:** `relay-backend-engineer`

**Files:**
- Modify: `README.md` (append after line 1534, the `/metrics` row that ends the Workers table)

- [ ] **Step 1: Add the row and the honesty note**

Insert immediately after the `GET | /v1/workers/{id}/metrics` row and before the blank line that
precedes `### Server`:

```markdown
| `GET` | `/v1/workers/{id}/tasks` | List the tasks currently assigned to a worker (`dispatched` or `running`), newest assignment first. Paginated, standard `page` envelope; `total` is the count of ACTIVE tasks for this worker, which is the same number the dispatcher treats as used slots. Fixed order, sortable only by `-assigned_at` (the default). 404 if the worker does not exist. Same bearer-auth as `GET /v1/workers/{id}`. |

`GET /v1/workers/{id}/tasks` does not return a worker's terminal tasks, and there is no endpoint that does: a per-worker task history has none. `items` and `total` come from two statements, so under concurrent dispatch they can disagree by one for an instant. The used-slot count can legitimately exceed `max_slots`, because `max_slots` is a dispatcher input rather than a database constraint and lowering it via `PATCH /v1/workers/{id}` requeues nothing.
```

Write "there is no endpoint that does", not "not yet": the second promises a schedule.

- [ ] **Step 2: Check the edit did not mangle the file**

```
git diff --stat -- README.md
git ls-files --eol README.md
python -c "open('README.md','rb').read().decode('utf-8')"
```

Expected: a diffstat of 3 or 4 inserted lines, `i/lf`, and the decode raising nothing. The decode
check is there because a programmatic edit that writes a non-UTF-8 byte passes every other check in
this repo.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document GET /v1/workers/{id}/tasks

States the two things a consumer would otherwise discover the hard way: total
and items come from separate statements and can disagree by one for an instant,
and used slots can exceed max_slots because max_slots is a dispatcher input,
not a constraint, and lowering it requeues nothing."
```

---

### Task 8: backend gate

**Owner:** `relay-backend-engineer`

- [ ] **Step 1: Run the full backend gate**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks
go vet ./...
make test
go test -tags integration -p 1 ./internal/store/... -timeout 1800s
go test -tags integration -p 1 ./internal/api/... -timeout 1800s
```

Expected: all green. `-race` is not required for this half: it adds no goroutine, no shared mutable
state and no new concurrency. State that skip and its reason in the handoff rather than substituting
`-count=N`, which would not be equivalent.

- [ ] **Step 2: Confirm the tree is clean and nothing generated was left behind**

```
git status --short
git diff --stat
rg -n "ListActiveTasksForWorkerPage" internal/store/tasks.sql.go
```

Expected: no modified or untracked files, and the generated symbol still present. Report to the
conductor: backend half complete, frontend may start.

---

# Frontend

Start only after Task 8 is committed.

### Task 9: the API client

**Owner:** `relay-frontend-engineer`

**Files:**
- Modify: `web/src/workers/api.ts`
- Modify: `web/src/workers/api.test.ts`

- [ ] **Step 1: Write the failing test**

Append to `web/src/workers/api.test.ts`, and add `listWorkerTasks` to the existing import block at
the top of the file (lines 5-13):

```ts
test('listWorkerTasks requests the worker tasks route and decodes the envelope', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/workers/w1/tasks', ({ request }) => {
      path = new URL(request.url).pathname
      // Hand-written JSON. A fixture marshalled through the app's own response
      // type agrees with the decoder by construction and can never detect drift.
      return HttpResponse.json({
        items: [
          {
            id: 't1',
            name: 'render-shot-042',
            status: 'running',
            commands: [['blender', '-b', 'shot042.blend']],
            env: null,
            requires: null,
            timeout_seconds: 3600,
            retries: 2,
            retry_count: 1,
            worker_id: 'w1',
            job_id: 'j1',
            job_name: 'nightly-render',
            assigned_at: '2026-09-01T09:14:02.118Z',
            started_at: '2026-09-01T09:16:40.902Z',
          },
        ],
        next_cursor: '',
        total: 1,
      })
    }),
  )
  const page = await listWorkerTasks('w1')
  expect(path).toBe('/v1/workers/w1/tasks')
  expect(page.total).toBe(1)
  expect(page.items[0].job_name).toBe('nightly-render')
  expect(page.items[0].started_at).toBe('2026-09-01T09:16:40.902Z')
})

test('listWorkerTasks decodes a dispatched task with no start time', async () => {
  server.use(
    http.get('/v1/workers/w1/tasks', () =>
      HttpResponse.json({
        items: [
          {
            id: 't2',
            name: 'sync-depot',
            status: 'dispatched',
            commands: [['p4', 'sync']],
            env: null,
            requires: null,
            timeout_seconds: null,
            retries: 0,
            retry_count: 0,
            worker_id: 'w1',
            job_id: 'j1',
            job_name: 'nightly-render',
            assigned_at: '2026-09-01T09:13:55.004Z',
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
  )
  const page = await listWorkerTasks('w1')
  expect(page.items[0].started_at).toBeUndefined()
})

test('listWorkerTasks throws ApiError on the error envelope', async () => {
  server.use(
    http.get('/v1/workers/w1/tasks', () => HttpResponse.json({ error: 'boom' }, { status: 500 })),
  )
  await expect(listWorkerTasks('w1')).rejects.toBeInstanceOf(ApiError)
})
```

- [ ] **Step 2: Run the test to verify it fails**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks/web
npx vitest run src/workers/api.test.ts
```

Expected: FAIL with `No "listWorkerTasks" export is defined on the "./api" module`.

- [ ] **Step 3: Add the types and the client**

Append to `web/src/workers/api.ts`:

```ts
// One currently-assigned task. Field-for-field the Go workerTaskResponse
// (internal/api/workers.go): the embedded taskResponse fields plus job_id,
// job_name, assigned_at and started_at. assignment_epoch is a fence token and is
// deliberately not on the wire.
export interface WorkerTask {
  id: string
  name: string
  status: TaskStatus
  // commands/env/requires come from opaque JSON columns and are `null`, not
  // `{}`/`[]`, for a task that omits them.
  commands: string[][] | null
  env: Record<string, string> | null
  requires: Record<string, string> | null
  timeout_seconds: number | null
  retries: number
  retry_count: number
  worker_id?: string
  job_id: string
  job_name: string
  assigned_at?: string
  started_at?: string
}

export interface WorkerTasksPage {
  items: WorkerTask[]
  next_cursor: string
  total: number
}

// The worker's currently assigned tasks (dispatched or running), newest
// assignment first. First page only: `total` is the active count for the whole
// worker, which the Slots KPI renders as used slots, so the panel is correct
// about that number without a paging control.
export function listWorkerTasks(id: string): Promise<WorkerTasksPage> {
  return apiFetch<WorkerTasksPage>(`/workers/${id}/tasks`)
}
```

Add the type import at the top of `web/src/workers/api.ts`, below the existing `apiFetch` import:

```ts
import type { TaskStatus } from '../jobs/api'
```

The cross-feature type import from `../jobs/` follows `web/src/schedules/ScheduleRunsPanel.tsx:3-4`.

- [ ] **Step 4: Run the test to verify it passes**

```
npx vitest run src/workers/api.test.ts
npx tsc -b
```

Expected: PASS, all tests in the file. `tsc` clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/api.ts web/src/workers/api.test.ts
git commit -m "feat(web): listWorkerTasks client and WorkerTask types

Fixtures are hand-written JSON, never marshalled through WorkerTasksPage: a
fixture built from the app's own response type agrees with the decoder by
construction and cannot detect drift in either direction."
```

---

### Task 10: the polling hook

**Owner:** `relay-frontend-engineer`

**Files:**
- Create: `web/src/workers/useWorkerTasks.ts`
- Create: `web/src/workers/useWorkerTasks.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/useWorkerTasks.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkerTasks } from './useWorkerTasks'

const EMPTY = { items: [], next_cursor: '', total: 0 }

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

afterEach(() => vi.useRealTimers())

test('caches under ["worker", id, "tasks"] and exposes the page', async () => {
  server.use(
    http.get('/v1/workers/w1/tasks', () =>
      HttpResponse.json({ items: [], next_cursor: '', total: 3 }),
    ),
  )
  const client = newClient()
  const { result } = renderHook(() => useWorkerTasks('w1'), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(client.getQueryData(['worker', 'w1', 'tasks'])).toEqual({
    items: [],
    next_cursor: '',
    total: 3,
  })
})

test('an injected interval drives the poll', async () => {
  let calls = 0
  server.use(
    http.get('/v1/workers/w1/tasks', () => {
      calls++
      return HttpResponse.json(EMPTY)
    }),
  )
  renderHook(() => useWorkerTasks('w1', 20), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2))
})

test('polls on the DEFAULT 3s worker cadence, and not before', async () => {
  // Behavioral, not constant-reading: a test that imports an exported constant
  // proves nothing about what the hook passes to refetchInterval. The call
  // counter is its own positive control, so the equality below is about the
  // interval and not about a dead instrument.
  vi.useFakeTimers({ shouldAdvanceTime: true })
  let calls = 0
  server.use(
    http.get('/v1/workers/w1/tasks', () => {
      calls++
      return HttpResponse.json(EMPTY)
    }),
  )
  const { result } = renderHook(() => useWorkerTasks('w1'), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  // 2.5s: still one call. This half fails if the default were the 10s metrics
  // cadence or the 15s workspaces one.
  await act(async () => {
    vi.advanceTimersByTime(2_500)
  })
  expect(calls).toBe(1)

  await act(async () => {
    vi.advanceTimersByTime(1_000)
  })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2))
})
```

- [ ] **Step 2: Run the test to verify it fails**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks/web
npx vitest run src/workers/useWorkerTasks.test.tsx
```

Expected: FAIL, `Failed to resolve import "./useWorkerTasks"`.

- [ ] **Step 3: Write the hook**

Create `web/src/workers/useWorkerTasks.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listWorkerTasks } from './api'

// Polls a worker's currently assigned tasks. The default 3000 matches useWorker:
// the header status and this panel answer the same question at the same
// timescale, and the Slots KPI is composed from BOTH queries, so a mismatched
// cadence would put the two halves of one displayed fraction an interval apart.
// Tests inject a small value.
//
// keepPreviousData matches its two siblings. With a changing route id it can
// render worker A's rows under worker B's id for one tick; the query key
// includes id, so that render is transient and self-corrects, and this panel
// issues no writes, so the confused-deputy form of that hazard does not apply.
export function useWorkerTasks(id: string, intervalMs = 3000) {
  return useQuery({
    queryKey: ['worker', id, 'tasks'],
    queryFn: () => listWorkerTasks(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

```
npx vitest run src/workers/useWorkerTasks.test.tsx
```

Expected: PASS, three tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/useWorkerTasks.ts web/src/workers/useWorkerTasks.test.tsx
git commit -m "feat(web): useWorkerTasks polls a worker's assigned tasks at 3s

Cadence is asserted behaviorally, off a call counter under fake timers, rather
than by reading an exported constant: a constant proves nothing about what its
consumer passes to refetchInterval."
```

---

### Task 11: the panel

**Owner:** `relay-frontend-engineer`

**Files:**
- Create: `web/src/workers/WorkerTasksPanel.tsx`
- Create: `web/src/workers/WorkerTasksPanel.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/WorkerTasksPanel.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { WorkerTasksPanel } from './WorkerTasksPanel'

// Hand-written JSON, deliberately independent of the WorkerTask type: a fixture
// built from the app's own type agrees with the decoder by construction.
const RUNNING = {
  id: 't1',
  name: 'render-shot-042',
  status: 'running',
  commands: [['blender', '-b', 'shot042.blend']],
  env: null,
  requires: null,
  timeout_seconds: 3600,
  retries: 2,
  retry_count: 1,
  worker_id: 'w1',
  job_id: 'j1',
  job_name: 'nightly-render',
  assigned_at: '2026-09-01T09:14:02.118Z',
  started_at: '2026-09-01T09:16:40.902Z',
}

const DISPATCHED = {
  id: 't2',
  name: 'sync-depot',
  status: 'dispatched',
  commands: [['p4', 'sync']],
  env: null,
  requires: null,
  timeout_seconds: null,
  retries: 0,
  retry_count: 0,
  worker_id: 'w1',
  job_id: 'j1',
  job_name: 'nightly-render',
  assigned_at: '2026-09-01T09:13:55.004Z',
}

function renderPanel(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

function tasksPage(items: unknown[], total = items.length) {
  return { items, next_cursor: '', total }
}

test('renders a row per assigned task with links to the job and the task log', async () => {
  server.use(
    http.get('/v1/workers/w1/tasks', () => HttpResponse.json(tasksPage([RUNNING, DISPATCHED]))),
  )
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  expect(await screen.findByText('render-shot-042')).toBeInTheDocument()
  const table = screen.getByRole('table')
  expect(table).toHaveAccessibleName('Current tasks')
  // Header row plus one row per task.
  expect(within(table).getAllByRole('row')).toHaveLength(3)

  expect(screen.getByRole('link', { name: 'render-shot-042' })).toHaveAttribute(
    'href',
    '/jobs/j1/tasks/t1',
  )
  expect(screen.getAllByRole('link', { name: 'nightly-render' })[0]).toHaveAttribute('href', '/jobs/j1')
  expect(screen.getByText('running')).toBeInTheDocument()
  // retries > 0 renders the fraction; retries === 0 renders a single hyphen.
  expect(screen.getByText('1/2')).toBeInTheDocument()
  expect(screen.getByText('-')).toBeInTheDocument()
})

test('shows an empty state when the worker has no active tasks', async () => {
  server.use(http.get('/v1/workers/w1/tasks', () => HttpResponse.json(tasksPage([]))))
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  expect(await screen.findByText('No active tasks.')).toBeInTheDocument()
  // The header row still renders; there is no data row.
  expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(1)
})

test('shows a loading line before the first page arrives', async () => {
  server.use(http.get('/v1/workers/w1/tasks', () => HttpResponse.json(tasksPage([RUNNING]))))
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  expect(screen.getByText('loading tasks...')).toBeInTheDocument()
  await screen.findByText('render-shot-042')
  expect(screen.queryByText('loading tasks...')).not.toBeInTheDocument()
})

test('shows the error and a Retry inside the panel', async () => {
  let calls = 0
  server.use(
    http.get('/v1/workers/w1/tasks', () => {
      calls++
      return HttpResponse.json({ error: 'boom' }, { status: 500 })
    }),
  )
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  expect(await screen.findByText('boom')).toBeInTheDocument()
  // The panel owns its own error surface; it must not be empty-stated as well.
  expect(screen.queryByText('No active tasks.')).not.toBeInTheDocument()
  const before = calls
  await userEvent.click(screen.getByRole('button', { name: /retry/i }))
  await waitFor(() => expect(calls).toBeGreaterThan(before))
})

test('renders a dispatched task with no start time', async () => {
  server.use(http.get('/v1/workers/w1/tasks', () => HttpResponse.json(tasksPage([DISPATCHED]))))
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  expect(await screen.findByText('sync-depot')).toBeInTheDocument()
  expect(screen.getByText('not started')).toBeInTheDocument()
  expect(screen.queryByText(/Invalid Date/)).not.toBeInTheDocument()
  expect(screen.queryByText(/1970/)).not.toBeInTheDocument()
  expect(screen.queryByText('NaN')).not.toBeInTheDocument()
})

test('renders no progress affordance', async () => {
  // relay has no progress column, no progress field on any proto message and no
  // agent-side computation of one. The hi-fi's per-task bar has nothing behind it.
  server.use(
    http.get('/v1/workers/w1/tasks', () => HttpResponse.json(tasksPage([RUNNING, DISPATCHED]))),
  )
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  await screen.findByText('render-shot-042')
  expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  expect(screen.queryByTestId('progress-fill')).not.toBeInTheDocument()
  expect(screen.queryByText(/\d+%/)).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks/web
npx vitest run src/workers/WorkerTasksPanel.test.tsx
```

Expected: FAIL, `Failed to resolve import "./WorkerTasksPanel"`.

- [ ] **Step 3: Write the component**

Create `web/src/workers/WorkerTasksPanel.tsx`:

```tsx
import { Link } from 'react-router-dom'
import { Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import { taskStatusColor } from '../jobs/taskStatus'
import { formatRelativeTime } from './liveness'
import { useWorkerTasks } from './useWorkerTasks'

const COLS = 'grid-cols-[1fr_1fr_100px_90px_60px]'
// Fixed tracks total 250px. This sits in the same detail-page column as
// WorkspacesPanel, whose 600 is documented as the largest value that does not
// scroll at 1280, so 560 can never be the widest element in the column. Both fr
// cells carry `truncate`, which is the precondition Table.tsx states for the
// min-width budget to hold.
const MIN_W = 'min-w-[560px]'

const HEADERS: TableColumn[] = [
  { label: 'TASK' },
  { label: 'JOB' },
  { label: 'STATUS' },
  { label: 'STARTED' },
  { label: 'RETRY', align: 'right' },
]

// The worker's currently assigned tasks. Rendered inside the page's Panel (which
// supplies the frame and the "Current tasks" title), so this component is only
// the header row, the data rows and the panel-level states.
//
// No progress column is rendered: relay has none, on the row, in the proto or in
// the agent, so there is nothing to render honestly.
export function WorkerTasksPanel({ workerId }: { workerId: string }) {
  const { data, isLoading, error, refetch } = useWorkerTasks(workerId)
  const rows = data?.items ?? []

  return (
    <div className="flex flex-col">
      {/* aria-label matches the visible title on the page Panel that wraps this. */}
      <Table
        label="Current tasks"
        columns={COLS}
        minWidth={MIN_W}
        headers={HEADERS}
        headerClassName="px-4 py-2 tracking-wider"
      >
        {rows.map((t) => {
          const c = taskStatusColor(t.status)
          return (
            <TableRow key={t.id} className="border-b border-border/40 px-4 py-2 font-mono text-[11px]">
              <TableCell className="truncate">
                <Link to={`/jobs/${t.job_id}/tasks/${t.id}`} className="text-accent hover:text-accent-b">
                  {t.name}
                </Link>
              </TableCell>
              <TableCell className="truncate">
                <Link to={`/jobs/${t.job_id}`} className="text-fg-mute hover:text-fg">
                  {t.job_name}
                </Link>
              </TableCell>
              <TableCell className={`flex items-center gap-2 ${c.text}`}>
                <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
                {t.status}
              </TableCell>
              {/* A dispatched task spends the whole workspace sync with no
                  started_at, and that is the row this panel most exists to show. */}
              <TableCell className="text-fg-mute">
                {t.started_at ? formatRelativeTime(t.started_at) : 'not started'}
              </TableCell>
              <TableCell className="text-right text-fg-mute">
                {t.retries > 0 ? `${t.retry_count}/${t.retries}` : '-'}
              </TableCell>
            </TableRow>
          )
        })}
      </Table>

      {/* The loading line, the error banner and the empty state are SIBLINGS of
          the role="table" subtree, never children: none is a valid child of a
          table, and the header row must stay present in every state. */}
      {isLoading && !data && (
        <div className="px-4 py-3 font-mono text-[11px] tracking-[0.04em] text-fg-dim">
          loading tasks...
        </div>
      )}

      {error ? (
        <div className="mx-4 my-2 flex items-center justify-between gap-3 rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          <span>{(error as Error).message}</span>
          <button type="button" className="text-[11px] underline" onClick={() => refetch()}>
            Retry
          </button>
        </div>
      ) : null}

      {/* One message, not the hi-fi's two. An offline worker inside the grace
          window still has its tasks assigned, so an empty list does not
          establish that being offline is the reason. */}
      {!isLoading && !error && rows.length === 0 && (
        <div className="px-4 py-3 text-[12px] text-fg-mute">No active tasks.</div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

```
npx vitest run src/workers/WorkerTasksPanel.test.tsx
npx vitest run src/components/holo/responsive.guard.test.ts
npx tsc -b
```

Expected: PASS, six tests. The responsive guard must stay green: `grid-cols-[...]` is an arbitrary
track list that its numeric rule does not match, and this file introduces no `md:grid-cols-`, so its
pinned four-file list is unchanged.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/WorkerTasksPanel.tsx web/src/workers/WorkerTasksPanel.test.tsx
git commit -m "feat(web): WorkerTasksPanel renders a worker's assigned tasks

Width is an arithmetic argument, not a measurement, and the plan says so: fixed
tracks sum to 250 under a 560 min-width, itself under the 600 its sibling in the
same column documents as measured-safe. jsdom performs no layout and the browser
lane runs no agent, so no lane in this repo can measure the populated panel.

No progress column: relay has none on the row, in the proto or in the agent.
One empty message rather than the hi-fi's two, because an offline worker inside
the grace window still holds its assignments, so an empty list does not
establish the cause."
```

---

### Task 12: page wiring, and the tests scaffolded on the placeholders

**Owner:** `relay-frontend-engineer`

**Files:**
- Modify: `web/src/workers/WorkerDetailPage.test.tsx`
- Modify: `web/src/workers/WorkerDetailPage.tsx` (lines 106-124)

This task edits the tests and the page together, because `web/src/test/setup.ts:5` sets
`onUnhandledRequest: 'error'`: the moment the page mounts the hook, every test in the file that has
no handler for the new route fails.

- [ ] **Step 0: Confirm the backlog item the repointed comment will cite exists**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks
ls docs/backlog/feature-2026-09-01-worker-activity-aggregate.md
```

If that file does not exist, **stop and report to the conductor**. The Jobs-today comment must cite a
findable item; do not invent a slug, and do not leave it citing the item this slice closes.

- [ ] **Step 1: Update the test file**

Four edits to `web/src/workers/WorkerDetailPage.test.tsx`.

**(a)** Give `renderDetail` a tasks fixture parameter and register the handler inside it, so every
test gets one. Replace the whole `renderDetail` function (lines 43-62) with:

```tsx
// Every test needs a handler for /v1/workers/:id/tasks: the page mounts
// useWorkerTasks unconditionally (hooks run before the loading and error early
// returns), and setup.ts fails on an unhandled request. Registered here rather
// than per test so a test that does not care about tasks does not have to.
// The fixture is a hand-written literal, not the app's WorkerTasksPage type.
function renderDetail(isAdmin: boolean, tasks: Record<string, unknown> = { items: [], next_cursor: '', total: 0 }) {
  setToken('test-token')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: isAdmin }),
    ),
    http.get(`/v1/workers/${ID}/tasks`, () => HttpResponse.json(tasks)),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/workers/${ID}`]}>
        <AuthProvider>
          <Routes>
            <Route path="/workers/:id" element={<WorkerDetailPage />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}
```

**(b)** Delete two tests outright. `renders the current-tasks placeholder note, not an empty table`
(lines 93-98) asserts a string that no longer exists. `the current-tasks panel contains no fabricated
task rows` (lines 109-116) asserts that no table and no row exist page-wide, which is now false by
design; its intent survives as `renders no progress affordance` in `WorkerTasksPanel.test.tsx`.

**(c)** Replace `renders the CPU/RAM and Slots KPI cards` (lines 75-82) with:

```tsx
test('renders the CPU/RAM and Slots KPI cards', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false, { items: [], next_cursor: '', total: 3 })
  expect(await screen.findByText('32c · 128G')).toBeInTheDocument()
  // Slots is real now: used comes from the tasks page total, which is the same
  // number the dispatcher treats as a used slot.
  expect(await screen.findByText('3 / 4')).toBeInTheDocument()
})

test('the Slots progress bar clamps when used exceeds max_slots', async () => {
  // max_slots is a dispatcher input, not a database constraint: lowering it via
  // PATCH requeues nothing, so used > max is a reachable state and the fill must
  // not render above 100%.
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false, { items: [], next_cursor: '', total: 6 })
  expect(await screen.findByText('6 / 4')).toBeInTheDocument()
  const fills = screen.getAllByTestId('progress-fill')
  expect(fills.some((el) => el.style.width === '100%')).toBe(true)
})
```

The `32c · 128G` literal contains a non-ASCII middle dot. Copy that line from the file rather than
retyping it, and do not retype any other non-ASCII literal in this file.

**(d)** Replace `the reservations panel contains no fabricated reservation rows` (lines 118-132)
with:

```tsx
test('the reservations panel contains no fabricated reservation rows', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(http.get(`/v1/workers/${ID}/workspaces`, () => HttpResponse.json([])))
  renderDetail(true)
  expect(await screen.findByText('no per-worker reservation lookup yet')).toBeInTheDocument()
  // Identified by accessible name rather than asserted absent. There are exactly
  // two real tables on an admin's page and both are empty here, so each
  // contributes only its header row. A fabricated reservations table would show
  // up as a third table or as an extra row.
  const tables = screen.getAllByRole('table')
  expect(tables.map((el) => el.getAttribute('aria-label')).sort()).toEqual([
    'Current tasks',
    'Source workspaces',
  ])
  expect(screen.getAllByRole('row')).toHaveLength(2)
})
```

`renders the Jobs-today placeholder KPI with no fabricated data` keeps its assertions unchanged.

- [ ] **Step 2: Run the tests to verify they fail**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks/web
npx vitest run src/workers/WorkerDetailPage.test.tsx
```

Expected: FAIL. `renders the CPU/RAM and Slots KPI cards` cannot find `3 / 4` (the page still renders
the em-dash placeholder), the clamp test cannot find `6 / 4`, and the reservations test finds one
table instead of two. Confirm those three are the failures before implementing.

- [ ] **Step 3: Wire the page**

In `web/src/workers/WorkerDetailPage.tsx`:

Add the imports beside the existing ones:

```tsx
import { WorkerTasksPanel } from './WorkerTasksPanel'
import { useWorkerTasks } from './useWorkerTasks'
```

Add the hook call after `useWorkerMetrics` (line 28):

```tsx
  const { data: tasks } = useWorkerTasks(id)
```

Add the derived value beside the other derived values (after line 68, `const isStale = ...`):

```tsx
  // used slots = this worker's active task count, which is the same number the
  // dispatcher derives when it decides whether the worker has capacity. It can
  // exceed max_slots: max_slots is a dispatcher input, not a constraint, and
  // lowering it via PATCH requeues nothing. ProgressBar clamps the fill.
  const usedSlots = tasks?.total ?? 0
```

Replace lines 106-108 (the Slots card and the two comment lines above it) with:

```tsx
        <KpiStat
          label="Slots"
          value={`${usedSlots} / ${worker.max_slots}`}
          progress={{ used: usedSlots, max: worker.max_slots }}
        />
```

Replace lines 109-110 (the two comment lines above the Jobs-today card) with:

```tsx
        {/* Backend-blocked: no per-worker activity aggregate exists yet.
            Enabler: feature-2026-09-01-worker-activity-aggregate. */}
```

Leave line 111, the `KpiStat label="Jobs today"` card itself, byte-identical: its `value` contains a
non-ASCII character, and retyping it is how a file stops being valid UTF-8. Edit only the two comment
lines above it, by anchored replacement.

Replace lines 118-124 (the Current-tasks panel and its Backend-blocked comment) with:

```tsx
          <Panel title="Current tasks" meta="GET /v1/workers/{id}/tasks">
            <WorkerTasksPanel workerId={id} />
          </Panel>
```

- [ ] **Step 4: Run the tests to verify they pass**

```
npx vitest run src/workers/WorkerDetailPage.test.tsx
npx vitest run
npx tsc -b
```

Expected: PASS across the whole web suite.

- [ ] **Step 5: Check the edit did not mangle the page file**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks
git diff --stat -- web/src/workers/WorkerDetailPage.tsx web/src/workers/WorkerDetailPage.test.tsx
git ls-files --eol web/src/workers/WorkerDetailPage.tsx web/src/workers/WorkerDetailPage.test.tsx
python -c "open('web/src/workers/WorkerDetailPage.tsx','rb').read().decode('utf-8')"
python -c "open('web/src/workers/WorkerDetailPage.test.tsx','rb').read().decode('utf-8')"
```

Expected: a diffstat in the low tens of lines, `i/lf` on both, and both decodes raising nothing. A
three-digit diffstat means the editor rewrote line endings; a decode failure means a non-ASCII
literal was retyped as Latin-1 instead of UTF-8, which no other check in this repo can see.

- [ ] **Step 6: Commit**

```bash
git add web/src/workers/WorkerDetailPage.tsx web/src/workers/WorkerDetailPage.test.tsx
git commit -m "feat(web): mount the current-tasks panel and make the Slots KPI real

Two of the page's three backend-blocked placeholders become real. The third,
Jobs today, keeps its placeholder and its comment is repointed at
feature-2026-09-01-worker-activity-aggregate so it does not cite the item this
slice closes.

Slots renders total/max_slots from the same query the panel uses, so the two
halves of the fraction cannot be an interval apart. No clamp is added here:
ProgressBar already clamps, and a test now pins that with a fixture whose used
exceeds max.

Two tests are deleted rather than left to fail: both asserted the placeholder
string or the ABSENCE of any table on the page, which is now false by design.
The reservations guard is rewritten rather than deleted - it is a real guard
against that placeholder growing a fabricated table - and now identifies tables
by accessible name."
```

---

### Task 13: full gate before the PR

**Owner:** either engineer

- [ ] **Step 1: Restore web/dist**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks
git checkout -- web/dist/
git status --short
```

`web/dist` is tracked but not maintained per-PR. It must never appear in the diff.

- [ ] **Step 2: Run every gate**

```
go vet ./...
make test
go test -tags integration -p 1 ./internal/store/... -timeout 1800s
go test -tags integration -p 1 ./internal/api/... -timeout 1800s
cd web
npm test
npx tsc -b
npm run build
```

Expected: all green. `npm run build` runs `tsc -b && vite build`.

- [ ] **Step 3: Restore web/dist again and confirm the final diff**

```
cd D:/dev/relay/.claude/worktrees/web-e-worker-tasks
git checkout -- web/dist/
git status --short
git diff --stat origin/main...HEAD
```

Expected: a clean tree, and a diff touching only the files this plan names. If `web/dist` appears,
`npm run build` regenerated it; restore it again.

- [ ] **Step 4: Report**

Report to the conductor:

- Every gate that ran and its result.
- That `-race` was NOT run for this slice, with the reason: the backend half adds no goroutine, no
  shared mutable state and no new concurrency. Do not substitute `-count=N`; it is not equivalent.
- That the populated panel's layout was NOT measured, with the reason: jsdom performs no layout, and
  `web/e2e/` runs no agent so no worker there ever has an assigned task. What shipped is an
  arithmetic argument (fixed tracks 250, min-width 560, sibling's measured-safe 600), not a
  measurement.
- The five backend mutations from Task 6 and their observed results.
- That `/backlog close` was NOT run, and that the conductor still owes:
  `feature-2026-06-05-worker-detail-activity-panel` closed via `/backlog close`, and four proposed
  follow-up items (worker activity aggregate, per-worker task history, agent-reported task progress,
  measuring the populated worker-detail panels) plus the `jobs.go` hand-written `store.Job` copies
  item.

---

## Spec coverage check

| Spec item | Task |
|-----------|------|
| Decision 1, active only, no `?status=` | 1 (SQL predicate), 4 (B1) |
| Decision 2, aggregate deferred, comment repointed | 12 |
| Decision 3, census updated | 2 (extended to nine members; see Refutation 1) |
| Decision 4, two statements not a join | 1, 5 |
| Decision 5, `assigned_at DESC NULLS LAST, id DESC`, fixed order | 1, 3, 4, 6 |
| Decision 6, `auth(...)` | 4 (route, B8, B9) |
| Decision 7, `assignment_epoch` absent, pinned by a test | 3, 6 (M2) |
| Decision 8, no progress | 11 |
| Decision 9, `total` is filtered, feeds Slots, clamps | 4, 6, 12 |
| Decision 10, 3000 cadence | 10 |
| Decision 11, placement and width | 11 |
| Decision 12, panel meta is the route | 12 |
| Decision 13, two cell links, no row link | 11 |
| Decision 14, one empty message | 11 |
| Decision 15, item closure | conductor, not the engineer |
| B1-B12 | 4, 5, 6 |
| B13-B14 | 3 |
| S1-S3 | 1, 2 |
| F1-F10 | 9, 10, 11, 12 |
| README | 7 |
| Gates | 8, 13 |
