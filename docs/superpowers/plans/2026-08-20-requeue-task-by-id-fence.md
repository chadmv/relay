# RequeueTaskByID Epoch + Assignee Fence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `RequeueTaskByID` the two predicates it is missing - `assignment_epoch` (currency) and `worker_id` (identity) - and have its one production caller pass the values it already holds, so a reconcile walking a stale snapshot can no longer tear a freshly re-dispatched task off a live worker.

**Architecture:** Three-predicate-plus-status fence in the SQL, matching `UpdateTaskStatus`, `IncrementTaskRetryCount` and `AppendTaskLog`. The statement becomes `:execrows` so the caller counts matches rather than attempts. `reconcileRunningTasks`'s requeue loop passes the epoch that is already the value of its `serverSet` map plus the connection's authenticated worker id, which is already a parameter of the function. No new metrics surface, no new export seam, no caller refactor.

**Tech Stack:** Go, sqlc (`make generate`), pgx/v5, Postgres 16, testify, testcontainers-go integration tests.

---

## Slice independence declaration

**This is a single backend slice. There is no frontend component at all.** Nothing under `web/` is touched, no HTTP handler, no API response shape, no proto message. Phase 3 dispatches `relay-backend-engineer` alone; there is nothing for `relay-frontend-engineer` to run in parallel with.

**This plan is NOT multi-stage.** One spec-sized change, one PR, one session. Do not run `/backlog phases` on it.

**Closes:** `bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence`

---

## Verification of the backlog item against HEAD

The item is a proposal, not a contract. Every claim below was checked by reading the tree at HEAD in the worktree `D:/dev/relay/.claude/worktrees/stoic-cannon-15b269`.

### Confirmed

| Claim | Evidence at HEAD |
|---|---|
| `RequeueTaskByID` fences on id + status allow-list only | `internal/store/query/tasks.sql:410-417`: `WHERE id = $1 AND status IN ('dispatched', 'running')` |
| Exactly **one production caller** | `internal/worker/handler.go:523`, `_ = h.q.RequeueTaskByID(ctx, tID)`. Nothing in `internal/api`, `internal/scheduler` (including the newly shipped `internal/scheduler/watchdog.go`), `internal/cli`, or `cmd/**` references the identifier. |
| The caller holds the epoch | `internal/worker/handler.go:438-441` builds `serverSet map[string]int64` whose **value** is `int64(t.AssignmentEpoch)` from `GetActiveTasksForWorker`. The requeue loop at `:515` ranges over keys only and throws the value away. |
| The caller holds the authenticated worker id | `reconcileRunningTasks(ctx context.Context, workerID pgtype.UUID, reported ...)` at `:432`; `finishRegister` calls it with `updated.ID` at `:385`. |
| The doc comment defends the wrong thing | `tasks.sql:389-391` - "it is the backstop for the window ... and it is what keeps reconcile from being able to resurrect a terminal task". It is silent about a re-dispatch inside that same window, which is `dispatched` and therefore admitted. |
| **`RequeueTaskByID` now also nulls `assigned_at`** | `tasks.sql:413`, added by the coordinator-watchdog slice (migration 000021). It is pinned by `internal/store/tasks_assigned_at_integration_test.go:134-141`. **This plan preserves it, unchanged, in the SET clause.** |
| `:execrows` has a local convention | Ten statements already use it (`workers.sql`, `users.sql`, `invites.sql`, `scheduled_jobs.sql`, `agent_enrollments.sql`). sqlc emits `func (q *Queries) X(...) (int64, error)` returning `result.RowsAffected()` - see `internal/store/workers.sql.go:1092-1103`. |
| The watchdog did **not** add a second caller | `internal/scheduler/watchdog.go` writes through `UpdateTaskStatus`. The requeue-shaped option was explicitly rejected (`docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md:118`). So this stays a race hardening, not a hard prerequisite, exactly as the item's Notes predicted. |

### Refuted or corrected

1. **There are THREE test call sites, not two.** The item lists `internal/store/store_test.go` twice and says "nothing else". The tree has moved: `internal/store/tasks_assigned_at_integration_test.go:137` calls `f.q.RequeueTaskByID(f.ctx, task.ID)` inside `TestAssignedAtIsClearedWhereverWorkerIDIs`, and it postdates the item. It breaks on the signature change and must be updated in the same commit. (`retry_job_tasks_integration_test.go:248`, `updatetaskstatus_startedat_integration_test.go:30` and `incrementtaskretrycount_guard_test.go:28` mention the name in prose only - leave them alone.)

2. **`Handler.Metrics` does not have a shape that fits a counter, on two independent grounds.** `Handler.Metrics` is a `*metrics.Store` (`internal/worker/handler.go:101`), and `internal/metrics/store.go` is a per-worker **ring buffer of utilization samples**: its entire API is `Activate/Append/Clear/Snapshot/LastSampleAt` over a `[]Sample` of CPU/mem/GPU floats. There is no counter, and adding one means designing a new metrics surface. Worse, the lifecycle is wrong: `h.Metrics.Activate(workerID, ...)` runs at `handler.go:414`, **after** `reconcileRunningTasks` at `:385`, and `Append` is documented as a no-op for a worker that has not been activated. A counter call from reconcile would be dropped on the floor by construction. **Decision: do nothing observable here.** See "Settled decision 3".

3. **The acceptance criterion "a call with a zero-value `worker_id` moves zero rows (... the `=` versus `IS NOT DISTINCT FROM` property)" does not actually test that property as stated.** Against a row whose `worker_id` is a real worker, a NULL argument is rejected under *both* operators (`NULL = 'w'` is UNKNOWN; `NULL IS NOT DISTINCT FROM 'w'` is FALSE). The discriminating case is NULL **on both sides**. `AppendTaskLog`'s test gets that for free from a never-claimed task (`store_test.go:367-377`), but `RequeueTaskByID` cannot: a never-claimed task is `pending`, which its status allow-list excludes, so **the allow-list makes the NULL/NULL state unreachable through production statements**. The plan therefore ships *both* tests: the reachable zero-value case (a behaviour pin) and a **planted** NULL-worker `dispatched` row (the actual `=` discriminator), with the planting explained at the site. Task 3, tests C and D.

4. **The item's "integration test on the handler covers the repro" is not the right shape, and the plan deliberately does not add one.** The repro's essential content - "a write carrying epoch 5 must not move a row now at epoch 7 owned by somebody else" - is a *store* property and is fully expressible there (Task 1). The handler's only contribution is passing real values, and staging a genuinely stale in-flight `serverSet` through `Connect` is not deterministic; faking it needs a new export seam or a function extraction, both of which are symbols absent at HEAD, and the project rule is that a test seam must not destroy the RED. Instead the plan pins the caller with **an existing byte-identical test plus a mandated mutation**: `TestRegisterWorker_ReconcilesRunningTasks` (`internal/worker/handler_test.go:330-448`) asserts `tServerOnly` becomes `pending` through the real `Connect` path, so binding a zero epoch or a NULL worker at the call site makes it RED. Task 4 proves that by mutating it. If a Phase 4 reviewer still wants a handler-level repro, the honest way in is to extract `requeueUnreported` and export it in `export_test.go` - argue it there, do not smuggle it in here.

5. **Neither structural guard needs updating, and neither goes RED.**
   - `internal/store/incrementtaskretrycount_guard_test.go` substring-matches `IncrementTaskRetryCount` over all non-test `.go` files, exempting `internal/store/*.sql.go` and allowing `internal/worker/handler.go`. This slice edits `tasks.sql` (not a `.go` file), `tasks.sql.go` (exempt) and `handler.go` (allowed). **Untouched, stays green.** Confirm it still runs in `make test` after the change.
   - `TestTasksStatusVocabularyIsExactly` (`internal/store/tasks_status_vocabulary_lockstep_test.go`) reads `tasks_status_check` and lists seven statements whose status predicates slice that vocabulary. **This slice does not change any status predicate**, so the guard is unaffected and must not be edited. *Noted for the roadmap, not for this slice:* the guard's list already omits `RequeueTaskByID`, `RequeueTask`, `RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch`, `CancelJobTasks` and `ClaimTaskForWorker`, all of which slice the same vocabulary. That gap is pre-existing, needs per-site prose for six statements, and is out of scope here. File it; do not fix it in this PR.

---

## Settled design decisions

### 1. `:execrows`, not `:exec`

**Take it.** Reasons, in order of weight:

- The caller's `requeued` counter currently counts **attempts**. Post-fence a rejected write is a normal outcome, and waking the dispatcher for it is a spurious wake for work another writer already woke it for.
- It lets the fence tests assert **both** the row count and the row state. Row state alone ("still dispatched") is satisfiable by an UPDATE that matched and then wrote the same values; a `0` rowcount is not.
- Ten statements in this repo already use `:execrows`, so there is a convention and this follows it. sqlc emits `(int64, error)`; the caller pattern is `n, err := ...`.
- The current `_ = h.q.RequeueTaskByID(...)` swallows a real DB error. With `:execrows`, `n` is `0` on error (the generated body returns `0, err` early), so error handling and match-counting collapse into one expression rather than needing a branch.

**Cost, stated plainly:** three test call sites change shape from `require.NoError(t, call)` to `n, err := call` + `require.NoError(t, err)`. That is a call-line reshape, not an assertion change; see the gate below.

### 2. What the caller does on zero rows: nothing, deliberately

**No log line, no counter, no error return.** Just `requeued += int(n)`.

- A log line is forbidden: reconcile runs inside `finishRegister`, **before** `Connect` allocates this connection's `ingestLogLimiter`, so the site has no budget at all. The unparseable-id branch twelve lines above says exactly this and is pinned by `TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing`, which asserts the whole captured log is empty.
- A counter through `Handler.Metrics` is refuted above - wrong shape and wrong lifecycle.
- Zero rows is the **correct** outcome, not a failure: it means another writer legitimately ended this assignment first. There is nothing to report.

The observability gap is real and is already tracked by `idea-2026-08-14-tasklog-fence-rejection-is-unobservable`, which wants a fence-rejection surface across *all* fenced statements. This slice adds a fourth site to that item's motivation and nothing else. Record it in the reasoning comment at the call site so the next reader does not re-derive it.

### 3. The `t.ID` cleanup: **not taken**, and the companion item is not narrowed

The item says "carrying the epoch also lets the requeue loop hold `t.ID` directly instead of round-tripping the canonical string back through `Scan`". **That is not how the epoch arrives.** `serverSet` is `map[string]int64` and the epoch is its **value**; changing `for taskIDStr := range serverSet` to `for taskIDStr, srvEpoch := range serverSet` gets it with a one-token diff and no re-keying at all. Holding `t.ID` requires re-keying both maps on `[16]byte`, dropping the `canonical := uuidStr(tID)` line, and adding the explicit `if !t.ID.Valid { continue }` fail-closed guard that item calls out as its one trap.

So: the epoch does **not** drag in the re-keying, and the re-keying does not fall out for free. `idea-2026-08-20-key-reconcile-task-maps-on-raw-uuid-bytes` stays open, unchanged and un-narrowed - its proposal is intact, because the loop still holds a string and still re-`Scan`s it after this slice. Do not touch it. **Do not silently widen scope into it.**

### 4. Keep the status allow-list

`AND status IN ('dispatched', 'running')` stays, byte-identical. It is a third independent guarantee (no terminal resurrection) and, per refutation 3 above, it is also what makes the NULL-worker state unreachable in practice. Removing it would be a separate regression. Task 3 test E pins it with a case where the epoch and worker predicates both **pass**, so only the status arm can reject.

---

## File structure

| File | Change |
|---|---|
| `internal/store/query/tasks.sql:382-417` | `RequeueTaskByID`: `:exec` -> `:execrows`, two new WHERE predicates, doc comment corrected and extended. **The only hand-edited SQL.** |
| `internal/store/tasks.sql.go` | **GENERATED.** Regenerated by `make generate`. Never hand-edit. |
| `internal/worker/handler.go:513-530` | The requeue loop passes the epoch and the authenticated worker id; counts matches. |
| `internal/store/store_test.go:489-492, 527, 711` | Two call sites updated; one `_, err =` becomes a named capture so the epoch is available. |
| `internal/store/tasks_assigned_at_integration_test.go:134-141` | Third call site updated. |
| `internal/store/requeue_task_by_id_fence_integration_test.go` | **NEW.** The repro plus four discriminating fence tests. |

**Must NOT change:** `internal/worker/handler_test.go` (`TestRegisterWorker_ReconcilesRunningTasks` is the production-wiring guard and must stay byte-identical), `internal/worker/handler_reconcile_canonical_test.go`, `internal/store/tasks_status_vocabulary_lockstep_test.go`, `internal/store/incrementtaskretrycount_guard_test.go`, anything under `web/`, any migration.

---

## The acceptance gate on `TestRequeueTaskByID_BumpsEpochAndFencesStaleUpdates`

`internal/store/store_test.go:687-727`. The permitted diff on this test is **exactly** two things:

1. Line 711, `require.NoError(t, q.RequeueTaskByID(ctx, task.ID))`, becomes a three-line call using the params struct plus a `require.NoError(t, err)`.
2. Optionally one **added** line asserting `n == 1`.

These five lines must come out of `git diff` **byte-identical**:

```go
	require.Equal(t, "pending", got.Status, "task must be back to pending")
	require.False(t, got.WorkerID.Valid, "worker_id must be cleared")
	require.Equal(t, int32(2), got.AssignmentEpoch, "epoch must be bumped to 2")
	require.ErrorIs(t, err, pgx.ErrNoRows, "stale update at epoch 1 must be rejected after requeue")
	require.Equal(t, "pending", got2.Status, "stale update must not have changed task status")
```

**If any of those needs adjusting, STOP and report it as a finding.** It would mean the fence changed the behaviour of a legitimate requeue, which is not what this slice is for.

---

## `make generate` procedure - READ BEFORE TASK 2

`sqlc` emits LF; this repo is CRLF. Every `make generate` therefore rewrites line endings across **all** generated files, burying the real change.

- [ ] Run `make generate` from the worktree root: `D:/dev/relay/.claude/worktrees/stoic-cannon-15b269`.
- [ ] Run `git diff --ignore-all-space --stat` to find which files have a **content** change. For this slice that is **exactly one file**: `internal/store/tasks.sql.go`. `models.go` must NOT appear (no column change). No `internal/proto/**` file may appear (`make generate` also runs `buf generate`, but no `.proto` changes here, so any proto diff is line-endings only).
- [ ] `git checkout -- <file>` every file whose `--ignore-all-space` diff is empty.
- [ ] **Then verify the regeneration actually survived.** This repo has silently discarded a regenerated `.sql.go` through this dance before, leaving a generated file whose doc comment contradicted its own source. After the checkout sweep, run all four of these and confirm each one:

```bash
# 1. The statement is :execrows, not :exec.
rg -n "name: RequeueTaskByID :execrows" internal/store/tasks.sql.go
# 2. The const's SQL body carries both new predicates.
rg -n -A10 "const requeueTaskByID" internal/store/tasks.sql.go
# 3. The params struct exists with all three fields.
rg -n -A5 "type RequeueTaskByIDParams struct" internal/store/tasks.sql.go
# 4. The DOC COMMENT was regenerated too, not just the body.
rg -n "EPOCH ESTABLISHES CURRENCY, NOT IDENTITY" internal/store/tasks.sql.go
```

Check 4 is the one that has been skipped before. The body and the doc comment are emitted from the same `.sql` and are reverted together, so a comment that still says "it is the backstop for the window between that read and this write" while the body carries the new WHERE means the checkout ate the regeneration and you must redo it.

- [ ] Confirm the emitted signature is `func (q *Queries) RequeueTaskByID(ctx context.Context, arg RequeueTaskByIDParams) (int64, error)`.
- [ ] Never hand-edit `*.sql.go` or `models.go`. If the content is wrong, fix the `.sql` and regenerate.

---

### Task 1: RED - a stale reconcile tears a fresh assignment off a live worker

This is the headline RED and it **compiles and runs against HEAD unmodified**, using today's `RequeueTaskByID(ctx, id)` signature. It reproduces the item's step 1-6 exactly and fails on a real assertion, not a compile error.

**Files:**
- Create: `internal/store/requeue_task_by_id_fence_integration_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/requeue_task_by_id_fence_integration_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"relay/internal/store"
)

// TestRequeueTaskByID_DoesNotTearOffAFreshAssignment is the repro from
// bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence, at the layer
// where the bug actually lives.
//
// Two overlapping registrations B and C of the same worker W1 both read
// {task: epoch 1} from GetActiveTasksForWorker. B requeues, the dispatcher hands
// the task to W2, and then C writes - still walking its OWN snapshot. Before the
// fence, C's write matched on nothing but the id and a status allow-list that
// admits 'dispatched', so it tore a task off a worker that had only just been
// given it, bumped the epoch, and left W2's subprocess running with every
// subsequent message it sent fenced out in silence.
//
// The handler is not involved: it contributes no predicate C could fail. What
// makes C distinguishable from B is the two values it carries, and that is a
// property of the statement.
func TestRequeueTaskByID_DoesNotTearOffAFreshAssignment(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Rex", "rex@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "rqid-fence", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)

	w1, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "rqid-fence-1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w2, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w2", Hostname: "rqid-fence-2", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// The state both B and C read: assigned to W1 at epoch 1.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// B requeues first. This is the LEGITIMATE call and it must succeed.
	require.NoError(t, q.RequeueTaskByID(ctx, task.ID))

	// The dispatcher claims it for a DIFFERENT worker.
	redispatched, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w2.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), redispatched.AssignmentEpoch)

	// C writes, still holding epoch 1 and worker W1. It must move nothing.
	require.NoError(t, q.RequeueTaskByID(ctx, task.ID))

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status,
		"a stale reconcile must not requeue a task that has been re-dispatched")
	require.True(t, after.WorkerID.Valid, "the fresh assignment must survive")
	assert.Equal(t, w2.ID.Bytes, after.WorkerID.Bytes,
		"the task must still belong to the worker it was re-dispatched to")
	assert.Equal(t, int32(3), after.AssignmentEpoch,
		"a rejected requeue must not bump the epoch")
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestRequeueTaskByID_DoesNotTearOffAFreshAssignment -v -timeout 300s
```

Expected: **FAIL**, on the first of the four final assertions:

```
Error:  Not equal:
        expected: "dispatched"
        actual  : "pending"
Test:   TestRequeueTaskByID_DoesNotTearOffAFreshAssignment
Messages: a stale reconcile must not requeue a task that has been re-dispatched
```

If it PASSES, stop: the statement has already been fixed, or the fixture is not reaching the state it claims. Check `redispatched.AssignmentEpoch == 3` first - if that `require` is what failed, the fixture is wrong and the test proves nothing.

- [ ] **Step 3: Commit the RED test**

```bash
git add internal/store/requeue_task_by_id_fence_integration_test.go
git commit -m "test(store): failing repro - a stale reconcile requeues a re-dispatched task"
```

---

### Task 2: Fence the statement, regenerate, update all four call sites

One atomic task: the signature change breaks the build until every call site moves, so the SQL edit, the regeneration and the four call sites land together.

**Files:**
- Modify: `internal/store/query/tasks.sql:382-417`
- Generated: `internal/store/tasks.sql.go`
- Modify: `internal/worker/handler.go:513-530`
- Modify: `internal/store/store_test.go:489-492, 526-527, 711`
- Modify: `internal/store/tasks_assigned_at_integration_test.go:134-141`
- Modify: `internal/store/requeue_task_by_id_fence_integration_test.go` (the two call lines from Task 1)

- [ ] **Step 1: Rewrite the statement and its doc comment**

Replace `internal/store/query/tasks.sql` lines 382-417 **in full** with:

```sql
-- name: RequeueTaskByID :execrows
-- Revert a single ASSIGNED task back to 'pending', on FOUR predicates. Each
-- answers a different question, none is redundant with the others, and none may
-- be deleted:
--   * id               - WHICH ROW.
--   * assignment_epoch - IS THE CALLER'S VIEW STILL CURRENT? (currency)
--   * worker_id        - IS THIS THE CALLER'S OWN ASSIGNMENT? (identity)
--   * status           - IS THE ROW STILL ASSIGNED AT ALL, i.e. not terminal.
-- Used by the reconcile path when the coordinator has a task assigned that the
-- agent didn't report as running (internal/worker/handler.go,
-- reconcileRunningTasks). Candidates come from GetActiveTasksForWorker, which
-- reads the id and the epoch under one snapshot; the worker id is the
-- connection's own, resolved at registration and never taken from the wire.
--
-- WHAT THE STATUS ALLOW-LIST ALONE DOES NOT COVER, and what this comment used to
-- claim it did. It called the allow-list "the backstop for the window between
-- that read and this write". It is the backstop for exactly ONE thing that can
-- happen in that window - the task FINISHING - and it was silent about the more
-- damaging one: the task being requeued by somebody else and RE-DISPATCHED to a
-- second worker. A fresh assignment is 'dispatched', which the allow-list
-- admits, so a reconcile walking a stale snapshot used to tear a live task off a
-- worker that had only just been given it, bump the epoch, and leave the first
-- agent's subprocess running with every message it sent afterwards fenced out in
-- silence - duplicate execution, with no log line anywhere. Two overlapping
-- registrations of one worker reach that, and so does a grace timer firing just
-- before finishRegister cancels it. See
-- bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence.
--
-- The epoch predicate closes it: a re-dispatch bumps assignment_epoch, so the
-- stale caller's epoch no longer matches and zero rows move. The worker
-- predicate closes it independently AND closes what the epoch cannot, because
-- THE EPOCH ESTABLISHES CURRENCY, NOT IDENTITY: a matching epoch proves the
-- caller's generation is current, never that the caller was entitled to end it.
-- Keep all four.
--
-- The worker_id comparison must stay a plain `=`, never IS NOT DISTINCT FROM,
-- for exactly the reason spelled out at UpdateTaskStatus: tasks.worker_id is
-- NULLABLE and a zero-value pgtype.UUID binds SQL NULL, so `=` makes a caller
-- that lost its identity fail CLOSED instead of matching an unassigned row. Do
-- not "fix the NULL bug" here either. The status allow-list happens to make that
-- state unreachable today - a row with a NULL worker_id is 'pending' or terminal,
-- and neither is admitted - which is why
-- TestRequeueTaskByID_NullWorkerIDDoesNotMatchANullArgument has to PLANT the row
-- it tests. That is a second guarantee behind the first, not a reason to relax
-- the first.
--
-- Bumps assignment_epoch so a late update from the prior assignment is fenced
-- out. The bump is inside the same UPDATE as the WHERE, so it happens only for
-- rows that actually matched - the "conditionally end the assignment" branch of
-- the epoch fence, never an unconditional bump.
--
-- :execrows, not :exec, so the caller counts MATCHES rather than ATTEMPTS. Zero
-- rows is a NORMAL, CORRECT outcome here - it means another writer ended this
-- assignment first - and it is deliberately neither logged nor counted: this
-- runs inside finishRegister, ahead of the connection's ingestLogLimiter, so the
-- site has no budget to spend, and Handler.Metrics is a utilization ring buffer
-- that is not even Activated yet at that point. The general gap is tracked by
-- idea-2026-08-14-tasklog-fence-rejection-is-unobservable; do not close it here
-- with a one-off.
--
-- assigned_at is nulled alongside worker_id. Every statement in this file that
-- nulls worker_id does the same, and ClaimTaskForWorker is the only statement
-- that sets either.
-- THE CLAIM IS ONE-DIRECTIONAL, and it is worth being precise because the
-- tempting shorthand is false: `assigned_at IS NULL` means the row holds no
-- assignment, but the CONVERSE DOES NOT HOLD. UpdateTaskStatus deliberately
-- writes neither column, so every `done`, every `timed_out` and every `failed`
-- row that got there through it still carries a non-NULL assigned_at - which is
-- correct, since the assignment must outlive the task for the trailing-log
-- flush. (CancelJobTasks nulls it, so the column does not even mean one
-- consistent thing across terminal rows.) Anything that means "CURRENTLY
-- assigned" must say so with the status predicate, exactly as
-- ListOverdueAssignedTasks does; a query keying on `assigned_at IS NOT NULL`
-- alone would select every task ever dispatched.
UPDATE tasks
SET status = 'pending',
    worker_id = NULL,
    assigned_at = NULL,
    started_at = NULL,
    finished_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE id = sqlc.arg(id)
  AND assignment_epoch = sqlc.arg(assignment_epoch)
  AND worker_id = sqlc.arg(worker_id)
  AND status IN ('dispatched', 'running');
```

- [ ] **Step 2: Regenerate**

Run the full **`make generate` procedure** section above, including all four read-back checks. Do not proceed until check 4 passes.

- [ ] **Step 3: Update the production caller**

In `internal/worker/handler.go`, replace lines 513-530 (from the `// Anything server has but agent didn't report` comment through the closing brace of the `if requeued > 0` block) with:

```go
	// Anything server has but agent didn't report → requeue.
	//
	// BOTH FENCES ARE ALREADY IN HAND HERE, which is why the statement can demand
	// them. serverSet's VALUE is the assignment_epoch GetActiveTasksForWorker read
	// under the same snapshot as the id, and workerID is this connection's own
	// authenticated worker, resolved at registration and never taken from the
	// wire. Passing them is what stops a reconcile walking a STALE snapshot from
	// tearing a task off the worker it was re-dispatched to in the meantime - see
	// the statement's own comment in query/tasks.sql.
	//
	// The int32 conversion is lossless: serverSet widened tasks.assignment_epoch
	// (int32) to int64 above only so the reported-task loop can compare it against
	// proto's int64 RunningTask.Epoch.
	requeued := 0
	for taskIDStr, srvEpoch := range serverSet {
		if agentSet[taskIDStr] {
			continue
		}
		var tID pgtype.UUID
		if err := tID.Scan(taskIDStr); err != nil {
			continue
		}
		// n counts MATCHES, not attempts. Zero is normal and CORRECT post-fence: it
		// means another writer ended this assignment first, and whoever did that
		// already woke the dispatcher, so there is nothing left here to wake it for.
		//
		// THE ERROR IS DROPPED ON PURPOSE, exactly as it was before this fence
		// existed. This runs inside finishRegister, BEFORE Connect allocates this
		// connection's ingestLogLimiter, so the site has no log budget at all -
		// the same rule as the unparseable-id branch above. n is 0 on error, so a
		// failed statement can neither inflate the count nor fake a dispatch wake.
		n, _ := h.q.RequeueTaskByID(ctx, store.RequeueTaskByIDParams{
			ID:              tID,
			AssignmentEpoch: int32(srvEpoch),
			WorkerID:        workerID,
		})
		requeued += int(n)
	}

	// Wake the scheduler so requeued tasks are dispatched immediately.
	if requeued > 0 {
		go h.triggerDispatch()
	}
```

- [ ] **Step 4: Update the three existing test call sites**

**4a.** `internal/store/store_test.go`, in `TestReconciliationQueries`. Capture the claim so the epoch is available - change lines 489-492 from:

```go
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskA.ID, WorkerID: pgtype.UUID{Bytes: w1.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
```

to:

```go
	claimedA, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskA.ID, WorkerID: pgtype.UUID{Bytes: w1.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
```

and change lines 526-527 from:

```go
	// RequeueTaskByID: requeue task A; it should be pending with worker_id cleared.
	require.NoError(t, q.RequeueTaskByID(ctx, taskA.ID))
```

to:

```go
	// RequeueTaskByID: requeue task A; it should be pending with worker_id cleared.
	requeuedA, err := q.RequeueTaskByID(ctx, store.RequeueTaskByIDParams{
		ID: taskA.ID, AssignmentEpoch: claimedA.AssignmentEpoch, WorkerID: w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), requeuedA, "the assignee's own requeue at the current epoch must match")
```

**4b.** `internal/store/store_test.go:711`, in `TestRequeueTaskByID_BumpsEpochAndFencesStaleUpdates`. Replace that single line with:

```go
	n, err := q.RequeueTaskByID(ctx, store.RequeueTaskByIDParams{
		ID: task.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "the fenced requeue must move exactly one row")
```

**Nothing else in this test may change.** Re-read the acceptance-gate section above before you touch it. `claimed` is already captured at line 705 and `w` at line 692.

**4c.** `internal/store/tasks_assigned_at_integration_test.go:137`, in the `RequeueTaskByID` sub-test. Replace that single line with:

```go
		_, err := f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
			ID: task.ID, AssignmentEpoch: task.AssignmentEpoch, WorkerID: f.w.ID,
		})
		require.NoError(t, err)
```

`task` here is the row returned by `f.claimedAt`, so it carries the live epoch. This mirrors the `IncrementTaskRetryCount` sub-test three arms below (`:170-172`), which already passes exactly these two fences. The two `assert.False` lines after it must not change.

- [ ] **Step 5: Update the Task 1 repro's two call lines**

In `internal/store/requeue_task_by_id_fence_integration_test.go`, replace

```go
	// B requeues first. This is the LEGITIMATE call and it must succeed.
	require.NoError(t, q.RequeueTaskByID(ctx, task.ID))
```

with

```go
	// B requeues first, carrying the epoch and worker it read. This is the
	// LEGITIMATE call and it must succeed.
	nB, err := q.RequeueTaskByID(ctx, store.RequeueTaskByIDParams{
		ID: task.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), nB, "B's requeue is current and must move the row")
```

and replace

```go
	// C writes, still holding epoch 1 and worker W1. It must move nothing.
	require.NoError(t, q.RequeueTaskByID(ctx, task.ID))
```

with

```go
	// C writes, still holding epoch 1 and worker W1 from its own snapshot. It must
	// move nothing: the epoch is stale AND the task belongs to somebody else now.
	nC, err := q.RequeueTaskByID(ctx, store.RequeueTaskByIDParams{
		ID: task.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), nC, "a stale reconcile must move zero rows")
```

The four final assertions stay byte-identical.

- [ ] **Step 6: Compile the integration-tagged tree**

```bash
go build ./...
make vet-integration
```

Expected: both clean. `vet-integration` is what catches a missed integration-tagged call site - `make test` never compiles those files. If it reports a `RequeueTaskByID` call anywhere not listed in Step 4, that is a fourth call site the plan missed: report it, then fix it the same way.

- [ ] **Step 7: Run the repro and the two touched store tests**

```bash
go test -tags integration -p 1 ./internal/store/... -run 'TestRequeueTaskByID_DoesNotTearOffAFreshAssignment|TestRequeueTaskByID_BumpsEpochAndFencesStaleUpdates|TestReconciliationQueries|TestAssignedAtIsClearedWhereverWorkerIDIs' -v -timeout 900s
```

Expected: **all PASS**. Each spins its own Postgres container, hence the generous timeout.

- [ ] **Step 8: Run the worker registration guard**

```bash
go test -tags integration -p 1 ./internal/worker/... -run 'TestRegisterWorker_' -v -timeout 900s
```

Expected: **all PASS**, in particular `TestRegisterWorker_ReconcilesRunningTasks`. Its `tServerOnly` assertion (`assert.Equal(t, "pending", fetchedServerOnly.Status)`) is the proof that the production call site binds real values rather than zeroes - a zero epoch or a NULL worker id makes the fence reject and leaves that task `dispatched`. **`internal/worker/handler_test.go` must be byte-identical; confirm with `git diff --stat` that it does not appear.**

- [ ] **Step 9: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go \
        internal/worker/handler.go internal/store/store_test.go \
        internal/store/tasks_assigned_at_integration_test.go \
        internal/store/requeue_task_by_id_fence_integration_test.go
git commit -m "fix(store): fence RequeueTaskByID on assignment_epoch and worker_id"
```

---

### Task 3: The discriminating fence tests, each proved load-bearing by mutation

Task 1's repro goes green if **either** new predicate is present - in that scenario the epoch advanced *and* the assignee changed. These four tests isolate one predicate each. They are born GREEN, so TDD's RED is replaced by a mutation battery: each mutation must redden exactly the test that names it.

**Files:**
- Modify: `internal/store/requeue_task_by_id_fence_integration_test.go`

- [ ] **Step 1: Confirm a GREEN baseline before writing anything**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestRequeueTaskByID -v -timeout 900s
```

Expected: PASS. **A mutation battery is only meaningful on a green baseline of the same tree.** A previous slice ran one against a broken fixture and every mutation "passed" uniformly, which proved nothing. If Step 5 below gives you uniform results across different mutations, that is a harness smell, not a finding - stop and fix the harness.

- [ ] **Step 2: Add the four tests**

Append to `internal/store/requeue_task_by_id_fence_integration_test.go`. Add `"github.com/jackc/pgx/v5/pgtype"` and `"time"` to its import block.

```go
// requeueFence is one job, two real workers, and a pool, so a test can both use
// the production statements and plant a state they cannot reach.
type requeueFence struct {
	q   *store.Queries
	ctx context.Context
	job store.Job
	w1  store.Worker
	w2  store.Worker
}

func newRequeueFence(t *testing.T) *requeueFence {
	t.Helper()
	q := newTestQueries(t)
	ctx := context.Background()
	user := makeTestUser(t, q, ctx, "Fen", "fen@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "rqid-fence", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	w1, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "rqid-w1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w2, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w2", Hostname: "rqid-w2", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	return &requeueFence{q: q, ctx: ctx, job: job, w1: w1, w2: w2}
}

// claimedBy creates a task and claims it for the given worker, THROUGH the
// production statement, so the row carries a real assignee and a real epoch.
func (f *requeueFence) claimedBy(t *testing.T, name string, w store.Worker) store.Task {
	t.Helper()
	task, err := f.q.CreateTask(f.ctx, store.CreateTaskParams{
		JobID: f.job.ID, Name: name, Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	claimed, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	return claimed
}

// A: THE EPOCH PREDICATE, ISOLATED. The worker predicate and the status
// allow-list both PASS here - the task is still assigned to W1 and still
// 'dispatched' - so only the epoch can reject. This is the case the repro cannot
// distinguish, because there the assignee changed too.
func TestRequeueTaskByID_StaleEpochMovesZeroRowsEvenForTheSameWorker(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "a", f.w1)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// A legitimate requeue by the assignee, then a re-claim by the SAME worker.
	n, err := f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	reclaimed, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: claimed.ID, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), reclaimed.AssignmentEpoch)

	// The stale caller: right worker, right status, WRONG generation.
	n, err = f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "a stale epoch must move zero rows")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status)
	assert.Equal(t, int32(3), after.AssignmentEpoch, "a rejected requeue must not bump the epoch")
}

// B: THE WORKER PREDICATE, ISOLATED. The epoch MATCHES (both rows are at their
// first assignment, epoch 1) and the status allow-list passes, so only the
// assignee predicate can reject. This is the "the epoch establishes currency,
// not identity" case: W1 holds a perfectly current epoch for a task that is not
// its own.
func TestRequeueTaskByID_WrongWorkerMovesZeroRows(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "b", f.w2)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	n, err := f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "a worker that is not the assignee must move zero rows")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status)
	require.True(t, after.WorkerID.Valid)
	assert.Equal(t, f.w2.ID.Bytes, after.WorkerID.Bytes, "the real assignee must be untouched")
	assert.Equal(t, int32(1), after.AssignmentEpoch)
}

// C: A CALLER THAT LOST ITS IDENTITY. pgtype.UUID{} binds SQL NULL and
// `worker_id = NULL` is never true, so it fails closed.
//
// READ THIS TOGETHER WITH TEST D. This case is rejected under BOTH `=` and
// `IS NOT DISTINCT FROM`, because the row's worker_id is non-NULL, so it does
// NOT pin the operator. It pins the behaviour a real caller would hit. D is the
// one that pins the operator.
func TestRequeueTaskByID_ZeroValueWorkerIDMovesZeroRows(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "c", f.w1)

	n, err := f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: pgtype.UUID{},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "a zero-value worker id must fail closed")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status)
	assert.Equal(t, int32(1), after.AssignmentEpoch)
}

// D: THE REGRESSION TEST FOR THE COMPARISON STAYING A PLAIN `=`. Under
// IS NOT DISTINCT FROM two NULLs compare equal, the fence matches, and a caller
// with no identity at all can requeue an unassigned row.
//
// THE ROW IS PLANTED WITH RAW SQL, AND THAT IS THE POINT, not a shortcut. A
// (status='dispatched', worker_id IS NULL) row is unreachable through the
// production statements: everything that nulls worker_id also sets status to
// 'pending' or a terminal value, and the schema reaches it only through workers'
// ON DELETE SET NULL, which nothing in this repo triggers because nothing DELETEs
// a worker. So the status allow-list is a second guarantee standing behind this
// one. This test exists so that if the allow-list ever widens, the operator
// choice is already pinned rather than rediscovered.
//
// AppendTaskLog's equivalent (store_test.go, "NULL matching NULL") gets the state
// for free from a never-claimed task, because its allow-list admits 'pending'.
// This statement's does not.
func TestRequeueTaskByID_NullWorkerIDDoesNotMatchANullArgument(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Nel", "nel@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "rqid-null", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "wn", Hostname: "rqid-null-w", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "d", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)

	// Plant the otherwise-unreachable state: still 'dispatched', no assignee.
	_, err = pool.Exec(ctx, `UPDATE tasks SET worker_id = NULL WHERE id = $1`, claimed.ID)
	require.NoError(t, err)
	planted, err := q.GetTask(ctx, claimed.ID)
	require.NoError(t, err)
	require.Equal(t, "dispatched", planted.Status, "fixture: the row must stay in the allow-list")
	require.False(t, planted.WorkerID.Valid, "fixture: the row must have no assignee")
	require.Equal(t, int32(1), planted.AssignmentEpoch)

	// NULL on both sides. `=` yields UNKNOWN and the row is left alone.
	n, err := q.RequeueTaskByID(ctx, store.RequeueTaskByIDParams{
		ID: claimed.ID, AssignmentEpoch: planted.AssignmentEpoch, WorkerID: pgtype.UUID{},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "NULL worker_id must not match a NULL worker id argument")

	after, err := q.GetTask(ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status)
	assert.Equal(t, int32(1), after.AssignmentEpoch)
}

// E: THE STATUS ALLOW-LIST, ISOLATED. The epoch AND the worker predicate both
// PASS here, which is the whole point: a terminal transition through
// UpdateTaskStatus neither bumps assignment_epoch nor clears worker_id -
// deliberately, so the trailing-log flush still works - so the assignee's own
// stale reconcile arrives with two matching fences. Only the allow-list stands
// between it and resurrecting a finished task.
func TestRequeueTaskByID_TerminalTaskIsNotResurrected(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "e", f.w1)

	done, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: "done", WorkerID: f.w1.ID,
		AssignmentEpoch: claimed.AssignmentEpoch,
		FinishedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, "done", done.Status)
	require.Equal(t, claimed.AssignmentEpoch, done.AssignmentEpoch,
		"precondition: a terminal transition must not bump the epoch")
	require.True(t, done.WorkerID.Valid,
		"precondition: a terminal transition must not clear the assignee")

	n, err := f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "a terminal task must not be resurrected by a requeue")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", after.Status)
	assert.Equal(t, int32(1), after.AssignmentEpoch)
}
```

- [ ] **Step 3: Run them**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestRequeueTaskByID -v -timeout 1200s
```

Expected: **all six PASS** (the repro plus these five). If D fails with `n == 1`, the emitted SQL is not using a plain `=` - re-check the `make generate` read-back.

- [ ] **Step 4: Commit before mutating**

```bash
git add internal/store/requeue_task_by_id_fence_integration_test.go
git commit -m "test(store): isolate each RequeueTaskByID predicate"
```

Commit first so the mutation battery has a clean tree to `git checkout --` back to.

- [ ] **Step 5: The mutation battery**

Each mutation is a single edit to `internal/store/query/tasks.sql` followed by `make generate` (and the CRLF sweep). After each, run the suite, record which tests reddened, then `git checkout -- internal/store/query/tasks.sql internal/store/tasks.sql.go` and confirm `git status` is clean before the next one.

> If any sibling agent is reading this worktree concurrently, do the battery in a detached worktree instead - a mutated shared tree makes another agent's run meaningless.

| # | Mutation to `RequeueTaskByID`'s WHERE | Must redden | Must stay green |
|---|---|---|---|
| M1 | delete `AND assignment_epoch = sqlc.arg(assignment_epoch)` | `..._StaleEpochMovesZeroRowsEvenForTheSameWorker` | B, C, D, E |
| M2 | delete `AND worker_id = sqlc.arg(worker_id)` | `..._WrongWorkerMovesZeroRows`, `..._ZeroValueWorkerIDMovesZeroRows`, `..._NullWorkerIDDoesNotMatchANullArgument` | A, E |
| M3 | `worker_id = sqlc.arg(worker_id)` -> `worker_id IS NOT DISTINCT FROM sqlc.arg(worker_id)` | `..._NullWorkerIDDoesNotMatchANullArgument` **only** | A, B, C, E |
| M4 | delete `AND status IN ('dispatched', 'running')` | `..._TerminalTaskIsNotResurrected` | A, B, C, D |

M3 is the one that matters most: it is the only evidence that C and D are different tests rather than one test written twice. If M3 reddens C as well, one of them is mis-constructed. If M3 reddens nothing, D is not reaching the planted state - check its `require.False(t, planted.WorkerID.Valid)` fixture assertion.

Note that M2 reddening three tests while M3 reddens one is the expected asymmetry, not a smell: deleting the predicate removes all three guarantees, weakening the operator removes only the NULL/NULL one.

- [ ] **Step 6: Restore and verify the tree is clean**

```bash
git checkout -- internal/store/query/tasks.sql internal/store/tasks.sql.go
git status --porcelain
```

Expected: empty output. Then re-run Step 3's suite once more and confirm all six PASS on the restored tree. Record the M1-M4 results in the PR body.

---

### Task 4: Prove the production call site binds real values

**Adding a required field to a generated params struct silently binds a zero value at every keyed literal that omits it.** Go will not complain: `store.RequeueTaskByIDParams{ID: tID}` compiles, binds `AssignmentEpoch: 0` and `WorkerID: pgtype.UUID{}` (SQL NULL), and the fence then rejects **every** requeue forever. Reconcile would stop requeuing unreported tasks entirely, in silence. That is exactly how this fence could ship inert, and no store-level test can see it.

This task proves the wiring by mutation. There is no code change here.

**Files:** none modified permanently.

- [ ] **Step 1: Confirm a green baseline**

```bash
go test -tags integration -p 1 ./internal/worker/... -run TestRegisterWorker_ -v -timeout 900s
```

Expected: PASS, including `TestRegisterWorker_ReconcilesRunningTasks`.

- [ ] **Step 2: Mutation W1 - drop the epoch**

In `internal/worker/handler.go`'s requeue loop, change `AssignmentEpoch: int32(srvEpoch),` to `AssignmentEpoch: 0,`. No regeneration needed. Run:

```bash
go test -tags integration -p 1 ./internal/worker/... -run TestRegisterWorker_ReconcilesRunningTasks -v -timeout 300s
```

Expected: **FAIL** on

```
Error: Not equal: expected: "pending" actual: "dispatched"
```

which is the `tServerOnly` assertion at `handler_test.go:437`. Revert with `git checkout -- internal/worker/handler.go`.

- [ ] **Step 3: Mutation W2 - drop the identity**

Change `WorkerID: workerID,` to `WorkerID: pgtype.UUID{},`. Run the same command. Expected: **FAIL** on the same assertion. Revert with `git checkout -- internal/worker/handler.go`.

If either mutation leaves the test GREEN, the test is not exercising the requeue path and the fence has no wiring coverage at all - stop and report it as a finding before going further.

- [ ] **Step 4: Verify the tree is clean and re-run the baseline**

```bash
git status --porcelain
```

Expected: empty. Then re-run Step 1 and confirm PASS.

- [ ] **Step 5: The full gate**

```bash
make test
make vet-integration
go test -tags integration -p 1 ./internal/store/... -timeout 1800s
go test -tags integration -p 1 ./internal/worker/... -timeout 1800s
```

Expected: all green. Specifically confirm in the `make test` output that `TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath` and `TestUpdateTaskStatusEpochHasNoProductionCaller` both ran and passed - they are the structural guards this slice was checked against, and they run untagged.

`TestTasksStatusVocabularyIsExactly` runs in the store integration package; confirm it passed and that `tasks_status_vocabulary_lockstep_test.go` does **not** appear in `git diff --stat`.

- [ ] **Step 6: Confirm the exact file set**

```bash
git diff --stat origin/main
```

Expected, and nothing else:

```
 internal/store/query/tasks.sql
 internal/store/tasks.sql.go
 internal/store/store_test.go
 internal/store/tasks_assigned_at_integration_test.go
 internal/store/requeue_task_by_id_fence_integration_test.go
 internal/worker/handler.go
```

`internal/worker/handler_test.go`, `internal/store/models.go`, anything under `web/` or `internal/proto/` appearing here is a defect. `web/dist` in particular is tracked but stale; if it is dirty, `git checkout -- web/dist/`.

- [ ] **Step 7: Commit anything left over**

If Steps 1-6 required no edits, there is nothing to commit and that is the expected outcome. Record the W1/W2 mutation results in the PR body alongside M1-M4.

---

## Handoff notes for the conductor (not engineer steps)

- **Closing the item.** `bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence` closes with this PR. Use `/backlog close requeuetaskbyid-has-no-epoch`, never a hand-edit - the `git mv` into `docs/backlog/closed/` is required scope.
- **Do not close** `idea-2026-08-20-key-reconcile-task-maps-on-raw-uuid-bytes`. Its proposal survives this slice intact; see Settled decision 3.
- **New item to file:** `TestTasksStatusVocabularyIsExactly`'s statement list omits six statements that slice the same vocabulary (`RequeueTaskByID`, `RequeueTask`, `RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch`, `CancelJobTasks`, `ClaimTaskForWorker`). Pre-existing, out of scope here, needs per-site prose.
- **Item accuracy for the retro:** the item's diagnosis and its prescribed SQL were both correct - the first time in three slices. Its errors were peripheral: a stale caller list (two test sites, not three), a metrics seam that does not exist in a usable shape, an acceptance criterion that does not test the property it names, and a handler-test shape that cannot be built without a seam the RED rule forbids.

---

## Self-review

- **Spec coverage.** Every acceptance bullet in the item maps to a task: stale epoch -> Task 3A; wrong worker -> Task 3B; zero-value worker -> Task 3C + 3D (split, with the reason); existing behaviour preserved with unchanged assertions -> Task 2 step 4b plus the gate section; terminal not resurrected -> Task 3E; both structural guards checked -> refutation 5 plus Task 4 step 5. The one bullet deliberately not implemented - a handler-level repro integration test - is refuted in writing with an alternative (Task 4's mutation battery) rather than dropped.
- **Placeholders.** None. Every code step carries the full text to write, every command carries its expected output, and every mutation carries the exact test name that must redden.
- **Type consistency.** `RequeueTaskByIDParams{ID pgtype.UUID, AssignmentEpoch int32, WorkerID pgtype.UUID}` is used identically at all six call sites, and matches the field order and types sqlc emits for `IncrementTaskRetryCountParams` from the same argument spelling. `RequeueTaskByID` returns `(int64, error)` everywhere, so every rowcount assertion uses `int64`, while every `AssignmentEpoch` assertion uses `int32` to match `store.Task`.
