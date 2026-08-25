# Worker Delete Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship an admin-only `DELETE /v1/workers/{id}` that ends every assignment generation before it releases the worker row, plus the CLI arm and the prose sweep that makes the tree stop saying relay has no worker-delete.

**Architecture:** Four new sqlc statements, one HTTP handler running them in one transaction in a fixed order (lock -> requeue -> scrub reservations -> unlink enrollment -> delete), one CLI subcommand requiring `--yes`, six prose sites. No migration, no proto, no files under `web/`.

**Tech Stack:** Go, sqlc, pgx/v5, Postgres 16, testcontainers-go, stdlib `flag`, testify.

**Spec:** `docs/superpowers/specs/2026-08-26-worker-delete.md` - cite by section, do not re-derive. **Closes** `docs/backlog/bug-2026-08-25-no-worker-delete-at-any-layer.md`; read that item's `## Corrections` section before its Summary, since three of its claims were refuted and the priority is now `medium`.

---

## Slice independence declaration

**This slice has ONE lane. There is no frontend slice and no Phase 3 parallelism.**

- Backend only: `internal/store/query/`, `internal/api/`, `internal/cli/`, `internal/agent/`, `internal/worker/` (comment only), `README.md`. **Zero files under `web/`** (spec 17.1 proposes the SPA affordance as a follow-on).
- Tasks are **strictly sequential**. Two orderings are load-bearing:
  1. **Task 8 (CLI arm) must land before Task 10 (agent message edit).** `internal/agent/cli_commands_exist_test.go` parses `internal/cli/*.go` and fails any `relay <cmd> <sub>` in an operator message that does not resolve. Editing the message first turns that guard red in between, for the wrong reason. Proof it held: Task 8 step 6 runs the guard green *before* any message edit, and Task 10 step 2 shows it red-then-green only via `messages_test.go`, never via the guard.
  2. **No task introduces a DELETE path without the requeue in the same transaction** (spec 6.2). Task 3 lands both together.

Dispatch one `relay-backend-engineer` for Tasks 1-11 in order. Task 12 is the conductor's.

---

## Critical files

| File | Why |
|---|---|
| `internal/store/query/workers.sql` | Two of the four statements; `CountWorkers`'s comment (`:133-150`) is a prose site. |
| `internal/api/workers.go:424-524` | `handleDisableWorker` is the structural precedent the new handler copies (spec 2.6). |
| `internal/store/query/tasks.sql:702-715` | `RequeueWorkerTasks`, reused byte-identical. Its `:581-608` neighbour is a prose site. |
| `internal/agent/cli_commands_exist_test.go` | **Must pass byte-identical throughout. Never edit it.** |
| `internal/agent/messages_test.go:100-153` | Goes RED on success; Task 10 makes exactly two edits in it. |
| CLAUDE.md Invariants | "End the generation before releasing the resource" is this slice's spine. |

## File inventory

**New (3):** `internal/store/workers_delete_integration_test.go` (integration lane, Tasks 1-2), `internal/api/workers_delete_integration_test.go` (integration lane, Tasks 3-7), `internal/cli/workers_delete_test.go` (default lane, Tasks 8-9).

**Edited (12):**

| File | Nature |
|---|---|
| `internal/store/query/workers.sql` | additive (2 statements); one comment rewrite in Task 11 |
| `internal/store/query/agent_enrollments.sql` | additive (1 statement) |
| `internal/store/query/reservations.sql` | additive (1 statement) |
| `internal/store/query/tasks.sql` | comment rewrite only (Task 11), no SQL change |
| `internal/store/*.sql.go` | **generated - never hand-edit**; `sqlc generate` only |
| `internal/api/workers.go` | additive: `deleteWorkerResponse` + `handleDeleteWorker`; adds the `log` import |
| `internal/api/server.go:175-178` | additive: one route line |
| `internal/cli/workers.go` | additive `doWorkersDelete` + switch arm; edits `:32`, `:36`, `:45`, `resolveWorkerID` (`:163-179`) |
| `internal/agent/messages.go:38-41` | one sentence replaced |
| `internal/agent/messages_test.go` | two edits (Task 10) |
| `internal/worker/handler.go:68-78` | comment rewrite |
| `README.md` | six sites (Task 11) |

**Never touched:** `internal/agent/cli_commands_exist_test.go`, `internal/store/models.go`, any `*.sql.go` by hand, `web/`, `docs/retros/`, existing specs, `ROADMAP.md`.

## Environment notes (read once)

- Tree is the worktree `D:/dev/relay/.claude/worktrees/reverent-solomon-87f44d`. Absolute paths only; do not `cd D:/dev/relay`.
- **`make` is not installed.** Run targets' commands directly. `make generate` is `sqlc generate` + `buf generate`; **this slice changes no `.proto`, so run `sqlc generate` only.**
- **CRLF procedure after every `sqlc generate`** (CLAUDE.md): sqlc emits LF and rewrites endings across all generated files. (1) `git diff --ignore-all-space --stat`; (2) `git checkout --` every generated file with no real content change; (3) **verify the regenerated file still contains the new statement** - the recorded failure mode is the revert silently discarding the regeneration.
- **The integration lane runs during implementation, not at the end.** This slice removes a behaviour (rows that could not be deleted now can) and adds SQL, so existing tests may be built on the old state. Run `go test -tags integration -p 1 ./internal/store/...` at the end of Task 2 and `./internal/api/...` at the end of Tasks 5 and 7. Needs Docker Desktop; `-p 1` is mandatory.
- `go test -race` is **unrunnable locally** (ThreadSanitizer allocation failure, environmental). CI's `race + integration-build` job is the gate. Do not attempt it and do not claim it.
- **Mutation batteries run in an isolated detached worktree**, never in the shared tree: `git worktree add --detach C:/Users/chadv/AppData/Local/Temp/relay-mut-<task> HEAD`. Every mutation carries an applied-check plus a control that must die.

## Task index

1. `GetWorkerForUpdate` + `DeleteWorker`, generate, allow-list lockstep
2. `ClearEnrollmentConsumerForWorker` + `RemoveWorkerFromReservations`, generate
3. Route + transaction spine: lock -> requeue -> delete (T-B1, T-B2, T-E1)
4. Unlink the enrollment inside the transaction (T-A1)
5. Scrub reservations inside the transaction (T-C1)
6. The Go status gate and the status codes (T-D1, T-D2, T-D3)
7. Pin what the handler inherits rather than implements (T-C2, T-E2)
8. CLI arm `relay workers delete --yes` (T-F1) - **before Task 10**
9. `resolveWorkerID` reaches revoked workers (T-F2)
10. The agent exit message; closing the ghost-command loop (T-G1, M10)
11. Prose sweep: README x6, `tasks.sql`, `workers.sql`, `handler.go`
12. Whole-slice verification and backlog close (conductor)

---

### Task 1: `GetWorkerForUpdate` and `DeleteWorker`, with the allow-list lockstep

**Files:** Create `internal/store/workers_delete_integration_test.go`; modify `internal/store/query/workers.sql` (after `:13` and after `:119`); generated `internal/store/workers.sql.go`.

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package store_test

import (
	"context"
	"sort"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// TestDeleteWorker_PermitsExactlyTheDisconnectedStatuses is the ALLOW-LIST test
// and the SQL arm's OWN kill (spec 8.2): handleDeleteWorker's Go status check
// does not exist at this layer, so nothing here can pass because of it. 'online'
// and 'stale' both mean CONNECTED - internal/scheduler/dispatch.go:210-215 says a
// stale worker is still connected and able to run tasks - so the permitted set is
// exactly the not-connected set.
func TestDeleteWorker_PermitsExactlyTheDisconnectedStatuses(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	q := store.New(pool)

	// 'stale' IS FIRST ON PURPOSE: it is the row that kills the tempting deny-list
	// `status != 'online'` (M5), and a poisoned input placed last cannot detect an
	// early-exit mutation.
	cases := []struct {
		status  string
		deleted bool
	}{
		{"stale", false},
		{"online", false},
		{"offline", true},
		{"revoked", true},
	}

	// LOCKSTEP: the table must enumerate the WHOLE vocabulary, so a fifth status
	// cannot appear without somebody deciding whether it is deletable - the job
	// TestTasksStatusVocabularyIsExactly does for tasks. literalRe is declared in
	// tasks_status_vocabulary_lockstep_test.go, same package, and is reused.
	var def string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'workers_status_check'`,
	).Scan(&def), "workers_status_check must exist; migration 000019 adds it")
	var vocab, covered []string
	for _, m := range literalRe.FindAllStringSubmatch(def, -1) {
		vocab = append(vocab, m[1])
	}
	for _, c := range cases {
		covered = append(covered, c.status)
	}
	sort.Strings(vocab)
	sort.Strings(covered)
	require.Equal(t, vocab, covered,
		"workers.status vocabulary changed - DeleteWorker's allow-list is ('offline','revoked'). "+
			"Decide which side the new status belongs on before updating this table (spec 8.1)")

	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			w := newTestWorker(t, q) // hostname derives from t.Name(), unique per subtest
			_, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{ID: w.ID, Status: c.status})
			require.NoError(t, err)

			n, err := q.DeleteWorker(ctx, w.ID)
			require.NoError(t, err)
			if c.deleted {
				require.Equal(t, int64(1), n, "%s is disconnected and must be deletable", c.status)
				_, err = q.GetWorker(ctx, w.ID)
				require.ErrorIs(t, err, pgx.ErrNoRows, "the row must be gone")
				return
			}
			require.Equal(t, int64(0), n, "%s means CONNECTED and must be refused", c.status)
			_, err = q.GetWorker(ctx, w.ID)
			require.NoError(t, err, "a refused delete must leave the row untouched")
		})
	}
}

// TestGetWorkerForUpdate_LocksAnExistingRowAndDistinguishesAMissingOne pins the
// 404 discrimination the handler makes INSIDE the transaction (spec 6.3 step 1).
func TestGetWorkerForUpdate_LocksAnExistingRowAndDistinguishesAMissingOne(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	w := newTestWorker(t, q)

	got, err := q.GetWorkerForUpdate(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, w.ID, got.ID)
	require.Equal(t, w.Hostname, got.Hostname)

	_, err = q.GetWorkerForUpdate(ctx, pgtype.UUID{Valid: true})
	require.ErrorIs(t, err, pgx.ErrNoRows, "a missing worker must be pgx.ErrNoRows, never a zero-value row")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/store/... -run "TestDeleteWorker_PermitsExactly|TestGetWorkerForUpdate" -v -timeout 300s`

Expected: **compile failure**, not a test failure:

```
internal\store\workers_delete_integration_test.go:NN:15: q.DeleteWorker undefined (type *store.Queries has no field or method DeleteWorker)
internal\store\workers_delete_integration_test.go:NN:16: q.GetWorkerForUpdate undefined (type *store.Queries has no field or method GetWorkerForUpdate)
```

- [ ] **Step 3: Write minimal implementation**

In `internal/store/query/workers.sql`, immediately after `GetWorkerByHostnameForUpdate` (`:12-13`):

```sql
-- name: GetWorkerForUpdate :one
-- The id-keyed twin of GetWorkerByHostnameForUpdate, for handleDeleteWorker.
-- IT IS STATEMENT 1 OF THE DELETE TRANSACTION AND ITS POSITION IS THE ARGUMENT:
-- taking the worker row FIRST matches the lock order of both enrollment
-- transactions (worker row, then agent_enrollments), so the delete cannot invert
-- it and needs no argument about why a cycle is not constructible (spec R9). It
-- also supplies the 404/409 discrimination inside the transaction, so there is no
-- window between the precondition and the DELETE: a concurrent
-- RegisterWorkerConnection is an UPDATE on this row and blocks until we commit or
-- roll back, so if it wins we read 'online' and refuse.
SELECT * FROM workers WHERE id = $1 FOR UPDATE;
```

After `ClearWorkerAgentToken` (`:116-119`):

```sql
-- name: DeleteWorker :execrows
-- Destroys a worker identity. The ONLY DELETE FROM workers in the tree.
--
-- THE STATUS PREDICATE IS AN ALLOW-LIST AND MUST STAY ONE. 'online' and 'stale'
-- both mean CONNECTED (internal/scheduler/dispatch.go:210-215), so the permitted
-- set is exactly the not-connected set. The equivalent deny-list
-- (`status != 'online' AND status != 'stale'`) is interchangeable against today's
-- vocabulary and FAILS OPEN on the next status added - a future 'quarantined'
-- worker would silently become deletable while connected. This fails closed.
-- TestDeleteWorker_PermitsExactlyTheDisconnectedStatuses enumerates the whole
-- vocabulary so the partition is revisited rather than desynchronized.
--
-- THIS PREDICATE IS THE CONTROL, not handleDeleteWorker's Go check. The Go check
-- reads the same status off the FOR UPDATE'd row and exists to turn a zero-row
-- delete into a 409 an operator can act on - a second question plus a better
-- error, the same shape as handleTaskStatus's Go identity gate. A future second
-- delete path that skips the lock is still refused here.
--
-- CALLERS MUST RUN RequeueWorkerTasks FIRST, in the same transaction. This
-- statement releases the resource; the requeue ends the generation. Reversed, the
-- FK's ON DELETE SET NULL nulls tasks.worker_id with no epoch bump and the row
-- becomes unreachable by EVERY worker-keyed statement in the tree (see
-- ListOverdueAssignedTasks's comment).
DELETE FROM workers WHERE id = $1 AND status IN ('offline', 'revoked');
```

- [ ] **Step 4: Generate, apply the CRLF procedure, verify the generation survived**

```powershell
sqlc generate
git diff --ignore-all-space --stat
```

Expect content changes only in `internal/store/workers.sql.go`; `git checkout --` every other generated file showing whitespace-only churn. Then:

```powershell
Select-String -Path internal/store/workers.sql.go -Pattern "func \(q \*Queries\) (DeleteWorker|GetWorkerForUpdate)"
```

Expected: two hits. Zero hits means the revert discarded the regeneration - re-run `sqlc generate` and redo the checkout more narrowly.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/store/... -run "TestDeleteWorker_PermitsExactly|TestGetWorkerForUpdate" -v -timeout 300s`
Expected: PASS - 4 subtests plus 2 top-level tests.

- [ ] **Step 6: Mutation battery (isolated worktree)**

`git worktree add --detach C:/Users/chadv/AppData/Local/Temp/relay-mut-t1 HEAD`. For each: edit the `.sql`, re-run `sqlc generate` in the mutant tree, **confirm the mutation applied** by grepping the generated `.sql.go` (a CRLF-defeated edit reports "survived" for the wrong reason), run the test, then `git checkout -- .`

| # | Mutation | Applied-check | Expected |
|---|---|---|---|
| **M0** control | `DeleteWorker` -> `WHERE id = $1 AND FALSE` | grep `AND FALSE` in `workers.sql.go` | **MUST DIE** on the `offline` and `revoked` subtests. Survival means the harness is broken. |
| M5 | allow-list -> `status != 'online'` | grep `!= 'online'` | dies on the **`stale`** subtest |
| M6-SQL | drop the predicate: `WHERE id = $1` | grep the shortened statement | dies on `stale` and `online` |
| M12 | `GetWorkerForUpdate` -> drop `FOR UPDATE` | grep its absence in the new func | **SURVIVES; declared unkillable (spec M12).** Record the survival in the commit body. Do NOT invent a flaky concurrency test to manufacture a kill. |

- [ ] **Step 7: Commit**

```bash
git add internal/store/query/workers.sql internal/store/workers.sql.go internal/store/workers_delete_integration_test.go
git commit -m "feat(store): DeleteWorker and GetWorkerForUpdate, with the status allow-list as the control

The allow-list is ('offline','revoked'), exactly the not-connected set. M0, M5 and
M6-SQL die; M12 (dropping FOR UPDATE) survives and is unkillable by design."
```

---

### Task 2: `ClearEnrollmentConsumerForWorker` and `RemoveWorkerFromReservations`

**Files:** modify `internal/store/query/agent_enrollments.sql` (append), `internal/store/query/reservations.sql` (append), `internal/store/workers_delete_integration_test.go` (append; add imports `time`, `github.com/jackc/pgx/v5/pgconn`).

- [ ] **Step 1: Write the failing tests**

```go
// TestDeleteWorker_IsRefusedWhileAnEnrollmentNamesTheWorker proves DECISION A's
// PREMISE, which is the whole reason the spec declined a migration:
// agent_enrollments.consumed_by's FK has NO ON DELETE action, so it fails CLOSED
// with SQLSTATE 23503 for any future deleter that forgets to unlink. If this goes
// green, somebody added ON DELETE SET NULL and the guard is gone.
func TestDeleteWorker_IsRefusedWhileAnEnrollmentNamesTheWorker(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	admin := newTestUser(t, q, true)
	w := newTestWorker(t, q)
	_, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{ID: w.ID, Status: "offline"})
	require.NoError(t, err)

	e, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "hash-" + t.Name(), CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
	rows, err := q.ConsumeAgentEnrollment(ctx, store.ConsumeAgentEnrollmentParams{ID: e.ID, ConsumedBy: w.ID})
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)

	_, err = q.DeleteWorker(ctx, w.ID)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23503", pgErr.Code, "the FK must fail closed for a deleter that did not unlink")

	n, err := q.ClearEnrollmentConsumerForWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "the count is what the delete response reports")

	deleted, err := q.DeleteWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	after, err := q.GetAgentEnrollmentByTokenHash(ctx, "hash-"+t.Name())
	require.NoError(t, err, "the enrollment row must survive the worker")
	require.True(t, after.ConsumedAt.Valid, "consumed_at must be intact")
	require.False(t, after.ConsumedBy.Valid, "consumed_by must be NULL")
}

func TestRemoveWorkerFromReservations_ScrubsOnlyTheReservationsThatNameIt(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	user := newTestUser(t, q, true)
	target := newTestWorker(t, q)
	other := newTestWorker(t, q)

	mk := func(name string, ids []pgtype.UUID) store.Reservation {
		r, err := q.CreateReservation(ctx, store.CreateReservationParams{
			Name: name, Selector: []byte("{}"), WorkerIds: ids, UserID: user.ID,
		})
		require.NoError(t, err)
		return r
	}
	// THE MIXED RESERVATION IS FIRST. A single-worker fixture passes against
	// `SET worker_ids = '{}'`; the mixed row is what forces array_remove
	// semantics, and it must not sit behind a benign row where an early-exit
	// mutation could hide.
	mixed := mk("mixed", []pgtype.UUID{target.ID, other.ID})
	only := mk("only", []pgtype.UUID{target.ID})
	none := mk("none", []pgtype.UUID{other.ID})

	n, err := q.RemoveWorkerFromReservations(ctx, target.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2), n,
		"the count must be how many reservations NAMED this worker - the WHERE clause is what makes it mean that")

	got, err := q.GetReservation(ctx, mixed.ID)
	require.NoError(t, err)
	require.Equal(t, []pgtype.UUID{other.ID}, got.WorkerIds, "array_remove must keep the other id")

	got, err = q.GetReservation(ctx, only.ID)
	require.NoError(t, err)
	require.Empty(t, got.WorkerIds)

	got, err = q.GetReservation(ctx, none.ID)
	require.NoError(t, err)
	require.Equal(t, []pgtype.UUID{other.ID}, got.WorkerIds, "an unrelated reservation must be untouched")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags integration -p 1 ./internal/store/... -run "TestDeleteWorker_IsRefused|TestRemoveWorkerFromReservations" -v -timeout 300s`
Expected: compile failure - `q.ClearEnrollmentConsumerForWorker undefined` and `q.RemoveWorkerFromReservations undefined`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/store/query/agent_enrollments.sql`:

```sql
-- name: ClearEnrollmentConsumerForWorker :execrows
-- Breaks the enrollment -> worker link so a worker row can be deleted. THE ONLY
-- STATEMENT PERMITTED TO SATISFY agent_enrollments.consumed_by's FOREIGN KEY,
-- which deliberately has NO ON DELETE ACTION (000005_agent_auth.up.sql:9).
--
-- THAT IS A DECISION, NOT AN OVERSIGHT (spec 5). A no-action FK fails CLOSED for
-- every future deleter - the planned TTL reaper included - with a loud SQLSTATE
-- 23503 that sends its author here. ON DELETE SET NULL would fail SILENT and
-- shred the link with no statement naming the act. If you arrived here from a
-- 23503, the guard is working: call this inside your delete transaction, before
-- the DELETE.
--
-- consumed_at is deliberately left alone, so the row still records that the token
-- was used and an unconsumed token stays distinguishable from a consumed one.
UPDATE agent_enrollments SET consumed_by = NULL WHERE consumed_by = $1;
```

Append to `internal/store/query/reservations.sql`:

```sql
-- name: RemoveWorkerFromReservations :execrows
-- Scrubs a deleted worker's id out of every reservation naming it.
-- reservations.worker_ids is a bare UUID[] with NO foreign key
-- (000001_initial.up.sql:89) - the one place a worker id can outlive its row.
--
-- THIS IS NOT A DISPATCH CORRECTNESS FIX and must not be sold as one. The
-- dispatcher's reservedIDs map (internal/scheduler/dispatch.go:185-191) is an
-- EXCLUSION set iterated over live workers rows, so a dangling id matches nothing
-- and withholds nothing. What this fixes is the contract - delete means "this id
-- ceases to exist" - and GET /v1/reservations showing a phantom.
--
-- THE WHERE CLAUSE IS NOT REDUNDANT WITH array_remove. Without it every
-- reservation is rewritten and the :execrows count becomes the table size instead
-- of "how many reservations named this worker", which is the number the delete
-- response reports and a test asserts.
--
-- A reservation whose array empties is LEFT ALONE: it becomes inert rather than
-- wrong, and deleting it would be a second destructive act the admin did not
-- request. README documents that limitation.
UPDATE reservations
SET worker_ids = array_remove(worker_ids, sqlc.arg(worker_id)::uuid)
WHERE sqlc.arg(worker_id)::uuid = ANY(worker_ids);
```

- [ ] **Step 4: Generate and apply the CRLF procedure**

```powershell
sqlc generate
git diff --ignore-all-space --stat
Select-String -Path internal/store/agent_enrollments.sql.go,internal/store/reservations.sql.go -Pattern "func \(q \*Queries\) (ClearEnrollmentConsumerForWorker|RemoveWorkerFromReservations)"
```

Expected: two hits. Confirm `RemoveWorkerFromReservations` takes a single `pgtype.UUID` (the two `sqlc.arg(worker_id)` uses collapse to one parameter). If sqlc emits a params struct, keep the casts and adjust the test call - do **not** drop the `WHERE` clause to simplify the signature.

- [ ] **Step 5: Run tests to verify they pass, then the whole store lane**

```powershell
go test -tags integration -p 1 ./internal/store/... -run "TestDeleteWorker|TestRemoveWorkerFromReservations|TestGetWorkerForUpdate" -v -timeout 600s
go test -tags integration -p 1 ./internal/store/... -timeout 900s
```

Expected: PASS both. The second is not optional: this task added SQL to a package whose existing tests were written when no worker could be deleted.

- [ ] **Step 6: Mutation battery**

| # | Mutation | Applied-check | Expected |
|---|---|---|---|
| **M0** control | `ClearEnrollmentConsumerForWorker` -> `WHERE consumed_by = $1 AND FALSE` | grep `AND FALSE` | **MUST DIE** (23503 on the second delete) |
| M4 | `array_remove(worker_ids, ...)` -> `worker_ids` | grep the body | dies on `mixed`/`only` |
| M4b | `array_remove(...)` -> `'{}'::uuid[]` | grep `'{}'::uuid[]` | dies on `mixed` (the other id is lost) |
| M9 | drop `WHERE ... = ANY(worker_ids)` | grep absence of `ANY(` | dies on `require.Equal(t, int64(2), n)` |

- [ ] **Step 7: Commit**

```bash
git add internal/store/query/agent_enrollments.sql internal/store/query/reservations.sql internal/store/agent_enrollments.sql.go internal/store/reservations.sql.go internal/store/workers_delete_integration_test.go
git commit -m "feat(store): unlink the enrollment consumer and scrub reservations for a deleted worker

The FK stays no-action deliberately - it fails closed for the next deleter where
ON DELETE SET NULL would fail silent (spec 5). M0, M4, M4b and M9 all die."
```

---

### Task 3: The route and the transaction spine (lock -> requeue -> delete)

**Files:** create `internal/api/workers_delete_integration_test.go`; modify `internal/api/workers.go` (add after `disableWorkerResponse` at `:40`, handler after `handleDisableWorker` ends at `:524`), `internal/api/server.go` (after `:177`).

**Lane note, answering spec 13.1's plan-time question:** every server-constructing helper in `internal/api` (`newTestServer`, `newTestServerWithPool`, `newCancelTestServer`) is behind `//go:build integration`; `internal/api` has **no default-lane seam that stubs the store**. So **T-D3 is integration-lane** and the CI default-lane gap widens by one test. Recorded, not papered over.

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func doDeleteWorker(t *testing.T, srv *api.Server, id, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("DELETE", "/v1/workers/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestDeleteWorker_RequeuesLiveTasksBeforeReleasingTheRow is the SPINE (T-B1).
// VACUITY WARNING: `worker_id IS NULL` alone passes against a build with NO
// REQUEUE AT ALL, because the FK's ON DELETE SET NULL produces it. The epoch
// increment and status='pending' are the only assertions the FK cannot produce,
// and they are what kills M2 (requeue moved after the delete).
func TestDeleteWorker_RequeuesLiveTasksBeforeReleasingTheRow(t *testing.T) {
	env := newCancelTestServer(t)
	admin := createTestUser(t, env.q, "Del Admin", "del-admin@example.com", true)
	adminToken := createTestToken(t, env.q, admin.ID)
	jobID := seedRunningTask(t, env, admin.ID)

	// The worker must be OFFLINE: delete refuses a connected one (spec 8.1), and
	// newCancelTestServer leaves the row at its default status.
	_, err := env.q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{
		ID: env.workerID, Status: "offline",
	})
	require.NoError(t, err)

	before, err := env.q.ListTasksByJob(t.Context(), mustParseUUID(t, jobID))
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.Equal(t, "running", before[0].Status)
	beforeEpoch := before[0].AssignmentEpoch

	rec := doDeleteWorker(t, env.srv, uuidString(env.workerID), adminToken)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	after, err := env.q.ListTasksByJob(t.Context(), mustParseUUID(t, jobID))
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.Equal(t, "pending", after[0].Status, "the FK cannot produce this - only RequeueWorkerTasks can")
	assert.Equal(t, beforeEpoch+1, after[0].AssignmentEpoch,
		"the generation must END BEFORE the row is released; a delete-first build leaves this unchanged")
	assert.False(t, after[0].WorkerID.Valid)
	assert.False(t, after[0].AssignedAt.Valid)
	assert.False(t, after[0].StartedAt.Valid)

	_, err = env.q.GetWorker(t.Context(), env.workerID)
	require.Error(t, err, "the worker row must be gone")

	// T-B2: reported, not merely done. Without this a handler that discards
	// RequeueWorkerTasks's return still passes everything above.
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, float64(1), body["requeued_tasks"])
}

// TestDeleteWorker_FreesTheHostnameForTokenlessAutoEnroll is the ITEM'S HEADLINE
// ACCEPTANCE CRITERION (T-E1), asserted at the level R7 says it can be tested at:
// a subsequent InsertWorkerForAutoEnroll returns a row instead of pgx.ErrNoRows.
// THE MIDDLE REFUSAL IS NOT OPTIONAL - without it this passes against a build
// where auto-enroll never refused anything at all.
func TestDeleteWorker_FreesTheHostnameForTokenlessAutoEnroll(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Free Admin", "free-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	const host = "render-07"

	params := store.InsertWorkerForAutoEnrollParams{
		Name: host, Hostname: host, CpuCores: 4, RamGb: 16, GpuCount: 0, GpuModel: "", Os: "linux",
	}
	first, err := q.InsertWorkerForAutoEnroll(t.Context(), params)
	require.NoError(t, err)

	_, err = q.InsertWorkerForAutoEnroll(t.Context(), params)
	require.Error(t, err, "the hostname is claimed, so the second auto-enroll must be refused")

	_, err = q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{ID: first, Status: "offline"})
	require.NoError(t, err)
	rec := doDeleteWorker(t, srv, uuidString(first), adminToken)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	third, err := q.InsertWorkerForAutoEnroll(t.Context(), params)
	require.NoError(t, err, "the hostname must be free again")
	assert.NotEqual(t, first, third, "a NEW row, not a revived one")
}
```

If `InsertWorkerForAutoEnroll`'s generated signature or `uuidString` differ from the above, adapt the call and keep every assertion.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestDeleteWorker -v -timeout 600s`
Expected: FAIL - `Error: Not equal: expected: 200, actual: 405` (net/http's mux has no `DELETE /v1/workers/{id}` pattern, so `DELETE` on a path that only registers `GET`/`PATCH` returns Method Not Allowed).

- [ ] **Step 3: Write minimal implementation**

Add to `internal/api/workers.go` after `disableWorkerResponse` (`:40`):

```go
// deleteWorkerResponse is the body returned by DELETE /v1/workers/{id}. It is a
// 200 with a body rather than a 204 ON PURPOSE (spec 6.4): relay has no audit
// log, so these three counts are the ONLY record of what the delete destroyed.
// The embedded workerResponse is the row as it was, read under the FOR UPDATE.
type deleteWorkerResponse struct {
	workerResponse
	RequeuedTasks       int `json:"requeued_tasks"`
	ReservationsUpdated int `json:"reservations_updated"`
	EnrollmentsUnlinked int `json:"enrollments_unlinked"`
}
```

Add the handler after `handleDisableWorker` (`:524`):

```go
// handleDeleteWorker destroys a worker identity (admin-only). Delete is the only
// verb that frees the hostname: revoke keeps the row, and every enrollment path
// keys on the UNIQUE hostname column.
//
// THE STATEMENT ORDER IS THE CORRECTNESS ARGUMENT, not a style choice. This is
// CLAUDE.md's first invariant in its original wording - end the generation before
// releasing the resource. The generation is tasks.assignment_epoch; the resource
// is the workers row. If the DELETE ran first, the FK's ON DELETE SET NULL would
// null tasks.worker_id with no epoch bump, and the row would then be unreachable
// by every worker-keyed statement in the tree, running forever, holding no slot,
// with its job never leaving 'running'. The requeue would then match zero rows
// and this handler would cheerfully report "requeued_tasks": 0.
func (s *Server) handleDeleteWorker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)

	// 1. Lock the worker row FIRST, matching both enrollment transactions' lock
	// order, and read the identity the response and the log line report.
	current, err := q.GetWorkerForUpdate(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	// 2. End every assignment generation while worker_id still names them.
	requeued, err := q.RequeueWorkerTasks(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "requeue tasks failed")
		return
	}

	// 3. Release the resource. :execrows, and the zero case is handled rather
	// than assumed - Task 6 turns it into the 409 it should be.
	n, err := q.DeleteWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete worker failed")
		return
	}
	if n == 0 {
		writeError(w, http.StatusInternalServerError, "delete worker failed")
		return
	}

	// 4. Wake the dispatcher so requeued tasks are placed promptly; skipped when
	// nothing moved, to avoid a spurious cycle (same as handleDisableWorker:488).
	if len(requeued) > 0 {
		if err := q.NotifyTaskSubmitted(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// NO CANCEL SIGNALS, deliberately. handleDisableWorker sends them because a
	// disabled worker is still connected; delete refuses a connected worker, so
	// by construction there is no agent to tell and sending anyway would imply a
	// connection this path exists to forbid (spec 6.3).

	writeJSON(w, http.StatusOK, deleteWorkerResponse{
		workerResponse: toWorkerResponse(current),
		RequeuedTasks:  len(requeued),
	})
}
```

In `internal/api/server.go`, after `:177`:

```go
	mux.Handle("DELETE /v1/workers/{id}", auth(admin(http.HandlerFunc(s.handleDeleteWorker))))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestDeleteWorker -v -timeout 600s`
Expected: PASS (both tests).

- [ ] **Step 5: Mutation battery**

Isolated worktree. Mutations are Go here, so the applied-check is `Select-String -Path internal/api/workers.go -Pattern "<new text>"` plus `go build ./...`.

| # | Mutation | Expected |
|---|---|---|
| **M0** control | `writeJSON(w, http.StatusOK, ...)` -> `http.StatusAccepted` | **MUST DIE** on both tests' `require.Equal(t, http.StatusOK, rec.Code)` |
| M1 | delete the `q.RequeueWorkerTasks` call (use `var requeued []pgtype.UUID`) | dies on T-B1's `status == "pending"` |
| M2 | **move the `RequeueWorkerTasks` block AFTER `DeleteWorker`** | dies on T-B1's `beforeEpoch+1` **and** `status == "pending"`, and NOT on `WorkerID.Valid` - confirm which assertions fail and record it, since that is the evidence the epoch assertion is load-bearing |
| M-B2 | `RequeuedTasks: len(requeued)` -> `RequeuedTasks: 0` | dies on `body["requeued_tasks"]` |

- [ ] **Step 6: Commit**

```bash
git add internal/api/workers.go internal/api/server.go internal/api/workers_delete_integration_test.go
git commit -m "feat(api): DELETE /v1/workers/{id} requeues before it releases the row

Ordering is the correctness argument (CLAUDE.md's first invariant). M2 - moving
the requeue after the delete - dies on the epoch assertion, which is the only one
the FK's ON DELETE SET NULL cannot produce by itself."
```

---

### Task 4: Unlink the enrollment inside the transaction (T-A1)

**Files:** modify `internal/api/workers_delete_integration_test.go`, `internal/api/workers.go`.

- [ ] **Step 1: Write the failing test**

```go
// TestDeleteWorker_SucceedsForATokenEnrolledWorker (T-A1). VACUITY: this passes
// trivially if the fixture never consumed a token, so the PRE-ASSERTION that
// consumed_by is non-NULL before the delete is the whole discriminator.
func TestDeleteWorker_SucceedsForATokenEnrolledWorker(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Enr Admin", "enr-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	row, err := q.UpsertWorkerByHostname(t.Context(), store.UpsertWorkerByHostnameParams{
		Name: "enrolled", Hostname: "enrolled", CpuCores: 4, RamGb: 16, Os: "linux",
	})
	require.NoError(t, err)
	_, err = q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{ID: row.ID, Status: "offline"})
	require.NoError(t, err)

	e, err := q.CreateAgentEnrollment(t.Context(), store.CreateAgentEnrollmentParams{
		TokenHash: "enr-hash", CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
	_, err = q.ConsumeAgentEnrollment(t.Context(), store.ConsumeAgentEnrollmentParams{ID: e.ID, ConsumedBy: row.ID})
	require.NoError(t, err)

	pre, err := q.GetAgentEnrollmentByTokenHash(t.Context(), "enr-hash")
	require.NoError(t, err)
	require.True(t, pre.ConsumedBy.Valid, "PRE-ASSERTION: without this the test is the generic delete test")

	rec := doDeleteWorker(t, srv, uuidString(row.ID), adminToken)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, float64(1), body["enrollments_unlinked"])

	post, err := q.GetAgentEnrollmentByTokenHash(t.Context(), "enr-hash")
	require.NoError(t, err, "the enrollment row must survive")
	assert.True(t, post.ConsumedAt.Valid, "consumed_at intact")
	assert.False(t, post.ConsumedBy.Valid, "consumed_by NULL")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestDeleteWorker_SucceedsForAToken -v -timeout 600s`
Expected: FAIL - `expected: 200, actual: 500` with body `{"error":"delete worker failed"}`; the server log carries the pgx error `SQLSTATE 23503 ... violates foreign key constraint "agent_enrollments_consumed_by_fkey"`.

- [ ] **Step 3: Write minimal implementation**

In `handleDeleteWorker`, between the requeue (step 2) and the `DeleteWorker` call, add:

```go
	// 3. Break the enrollment link. Must precede the DELETE or the no-action FK
	// fires; that FK is deliberately not ON DELETE SET NULL (spec 5).
	unlinked, err := q.ClearEnrollmentConsumerForWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unlink enrollments failed")
		return
	}
```

and set `EnrollmentsUnlinked: int(unlinked)` in the response literal. Renumber the trailing comments so the steps read 1..5.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestDeleteWorker -v -timeout 600s` - Expected: PASS (three tests).

- [ ] **Step 5: Mutation battery**

| # | Mutation | Expected |
|---|---|---|
| **M0** control | `EnrollmentsUnlinked: int(unlinked)` -> `: 0` | **MUST DIE** on `enrollments_unlinked` |
| M3 | delete the `ClearEnrollmentConsumerForWorker` call | dies with 500/23503 - which also re-proves the FK is genuinely no-action, i.e. Decision A's premise still holds |
| M3b | move the call AFTER `DeleteWorker` | dies the same way; the order within the transaction is not free |

- [ ] **Step 6: Commit**

```bash
git add internal/api/workers.go internal/api/workers_delete_integration_test.go
git commit -m "feat(api): unlink the consuming enrollment inside the delete transaction"
```

---

### Task 5: Scrub reservations inside the transaction (T-C1)

**Files:** modify `internal/api/workers_delete_integration_test.go`, `internal/api/workers.go`.

- [ ] **Step 1: Write the failing test**

```go
// TestDeleteWorker_RemovesTheIdFromReservationsThatNameIt (T-C1). The MIXED
// reservation is created FIRST: a single-reservation fixture passes against
// `SET worker_ids = '{}'`, and the untouched third row is what makes the
// statement's WHERE clause load-bearing rather than cosmetic.
func TestDeleteWorker_RemovesTheIdFromReservationsThatNameIt(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Res Admin", "res-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	target, err := q.UpsertWorkerByHostname(t.Context(), store.UpsertWorkerByHostnameParams{
		Name: "res-target", Hostname: "res-target", CpuCores: 4, RamGb: 16, Os: "linux",
	})
	require.NoError(t, err)
	other, err := q.UpsertWorkerByHostname(t.Context(), store.UpsertWorkerByHostnameParams{
		Name: "res-other", Hostname: "res-other", CpuCores: 4, RamGb: 16, Os: "linux",
	})
	require.NoError(t, err)
	_, err = q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{ID: target.ID, Status: "offline"})
	require.NoError(t, err)

	mk := func(name string, ids []pgtype.UUID) store.Reservation {
		r, err := q.CreateReservation(t.Context(), store.CreateReservationParams{
			Name: name, Selector: []byte("{}"), WorkerIds: ids, UserID: admin.ID,
		})
		require.NoError(t, err)
		return r
	}
	mixed := mk("mixed", []pgtype.UUID{target.ID, other.ID})
	only := mk("only", []pgtype.UUID{target.ID})
	none := mk("none", []pgtype.UUID{other.ID})

	rec := doDeleteWorker(t, srv, uuidString(target.ID), adminToken)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, float64(2), body["reservations_updated"])

	got, err := q.GetReservation(t.Context(), mixed.ID)
	require.NoError(t, err)
	assert.Equal(t, []pgtype.UUID{other.ID}, got.WorkerIds)
	got, err = q.GetReservation(t.Context(), only.ID)
	require.NoError(t, err)
	assert.Empty(t, got.WorkerIds)
	got, err = q.GetReservation(t.Context(), none.ID)
	require.NoError(t, err)
	assert.Equal(t, []pgtype.UUID{other.ID}, got.WorkerIds)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestDeleteWorker_RemovesTheId -v -timeout 600s`
Expected: FAIL - `Not equal: expected: 2, actual: <nil>` on `body["reservations_updated"]` (the key is absent because the field is zero-valued and always serialized... if the field serializes as `0`, the failure reads `expected: 2, actual: 0`). Either way the mixed reservation still contains the target id.

- [ ] **Step 3: Write minimal implementation**

In `handleDeleteWorker`, between the enrollment unlink and `DeleteWorker`:

```go
	// 4. Scrub the id out of reservations naming it. Before the DELETE because
	// after it there is no id to scrub by. NOT a dispatch fix (spec 7).
	scrubbed, err := q.RemoveWorkerFromReservations(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scrub reservations failed")
		return
	}
```

and set `ReservationsUpdated: int(scrubbed)` in the response literal.

- [ ] **Step 4: Run test to verify it passes, then the whole api integration lane**

```powershell
go test -tags integration -p 1 ./internal/api/... -run TestDeleteWorker -v -timeout 600s
go test -tags integration -p 1 ./internal/api/... -timeout 900s
```

Expected: PASS both. The second is required here, not deferred: the delete route is now live in every test server built by `newTestServer`.

- [ ] **Step 5: Mutation battery**

| # | Mutation | Expected |
|---|---|---|
| **M0** control | `ReservationsUpdated: int(scrubbed)` -> `: 0` | **MUST DIE** on `reservations_updated` |
| M4-api | delete the `RemoveWorkerFromReservations` call | dies on `mixed`'s contents and on the count |
| M-order | move the call AFTER `DeleteWorker` | dies (`reservations_updated: 0`) - the id no longer exists to scrub by, exactly as the statement's comment claims |

- [ ] **Step 6: Commit**

```bash
git add internal/api/workers.go internal/api/workers_delete_integration_test.go
git commit -m "feat(api): scrub the deleted worker out of every reservation naming it"
```

---

### Task 6: The Go status gate and the status codes (T-D1, T-D2, T-D3)

**Files:** modify `internal/api/workers_delete_integration_test.go`, `internal/api/workers.go`.

- [ ] **Step 1: Write the failing tests**

```go
// TestDeleteWorker_RefusesAConnectedWorker (T-D1). The TASK assertion is what
// proves the refusal happened BEFORE any write - it catches a handler that
// requeues and only then discovers it may not delete.
func TestDeleteWorker_RefusesAConnectedWorker(t *testing.T) {
	// 'stale' FIRST: it kills the deny-list `status != 'online'` (M5), and a
	// poisoned input placed last cannot catch an early-exit mutation.
	for _, status := range []string{"stale", "online"} {
		t.Run(status, func(t *testing.T) {
			env := newCancelTestServer(t)
			admin := createTestUser(t, env.q, "C Admin", "c-admin-"+status+"@example.com", true)
			adminToken := createTestToken(t, env.q, admin.ID)
			jobID := seedRunningTask(t, env, admin.ID)
			_, err := env.q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{
				ID: env.workerID, Status: status,
			})
			require.NoError(t, err)
			before, err := env.q.ListTasksByJob(t.Context(), mustParseUUID(t, jobID))
			require.NoError(t, err)

			rec := doDeleteWorker(t, env.srv, uuidString(env.workerID), adminToken)
			require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())

			_, err = env.q.GetWorker(t.Context(), env.workerID)
			require.NoError(t, err, "the row must survive a refusal")
			after, err := env.q.ListTasksByJob(t.Context(), mustParseUUID(t, jobID))
			require.NoError(t, err)
			require.Len(t, after, 1)
			assert.Equal(t, before[0].Status, after[0].Status, "a refusal must not requeue")
			assert.Equal(t, before[0].AssignmentEpoch, after[0].AssignmentEpoch,
				"a refusal must not bump the epoch - this is what catches requeue-then-discover")
		})
	}
}

// TestDeleteWorker_StatusCodes (T-D3). Asserts the CODE, not the message.
// VACUITY: asserting only "not 200" collapses all three failures into one.
func TestDeleteWorker_StatusCodes(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "SC Admin", "sc-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	user := createTestUser(t, q, "SC User", "sc-user@example.com", false)
	userToken := createTestToken(t, q, user.ID)

	assert.Equal(t, http.StatusBadRequest, doDeleteWorker(t, srv, "not-a-uuid", adminToken).Code)
	assert.Equal(t, http.StatusNotFound,
		doDeleteWorker(t, srv, "00000000-0000-0000-0000-000000000000", adminToken).Code)

	row, err := q.UpsertWorkerByHostname(t.Context(), store.UpsertWorkerByHostnameParams{
		Name: "sc-host", Hostname: "sc-host", CpuCores: 4, RamGb: 16, Os: "linux",
	})
	require.NoError(t, err)
	_, err = q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{ID: row.ID, Status: "online"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, doDeleteWorker(t, srv, uuidString(row.ID), userToken).Code,
		"admin-only: every mutating worker route is, and this is the most destructive")
	assert.Equal(t, http.StatusConflict, doDeleteWorker(t, srv, uuidString(row.ID), adminToken).Code)

	_, err = q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{ID: row.ID, Status: "offline"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, doDeleteWorker(t, srv, uuidString(row.ID), adminToken).Code)
}

// TestDeleteWorker_PermitsExactlyTheDisconnectedStatuses (T-D2) at the HTTP
// layer, over the WHOLE vocabulary, so a fifth status cannot be added without a
// decision about the route's behaviour too.
func TestDeleteWorker_PermitsExactlyTheDisconnectedStatuses(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "V Admin", "v-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	for _, tc := range []struct {
		status string
		want   int
	}{
		{"stale", http.StatusConflict},
		{"online", http.StatusConflict},
		{"offline", http.StatusOK},
		{"revoked", http.StatusOK},
	} {
		t.Run(tc.status, func(t *testing.T) {
			row, err := q.UpsertWorkerByHostname(t.Context(), store.UpsertWorkerByHostnameParams{
				Name: "vocab-" + tc.status, Hostname: "vocab-" + tc.status,
				CpuCores: 4, RamGb: 16, Os: "linux",
			})
			require.NoError(t, err)
			_, err = q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{
				ID: row.ID, Status: tc.status,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, doDeleteWorker(t, srv, uuidString(row.ID), adminToken).Code)
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags integration -p 1 ./internal/api/... -run "TestDeleteWorker_Refuses|TestDeleteWorker_StatusCodes|TestDeleteWorker_PermitsExactly" -v -timeout 600s`
Expected: FAIL with `expected: 409, actual: 500` on the `stale`, `online` and connected arms - Task 3 mapped the zero-row delete to a 500. The 400/404/403 arms already pass; note that in the run output rather than pretending otherwise.

- [ ] **Step 3: Write minimal implementation**

Immediately after the `GetWorkerForUpdate` block in `handleDeleteWorker`:

```go
	// 2. The status gate. THE SQL PREDICATE IN DeleteWorker IS THE CONTROL; this
	// is a second question plus a better error (spec 8.2). Because step 1 took
	// FOR UPDATE, the two cannot disagree within this transaction; the SQL arm is
	// defence for a future caller who writes a second delete path without a lock.
	//
	// WRITTEN AS AN ALLOW-LIST, like the SQL. The deny-list is interchangeable
	// today and fails OPEN on the next status added. 'online' and 'stale' both
	// mean CONNECTED; a disabled worker is still 'online' or 'offline'
	// underneath, and this keys on the underlying value, so a
	// disabled-and-connected worker is refused - correct, since disable does not
	// close the stream.
	switch current.Status {
	case "offline", "revoked":
	default:
		writeError(w, http.StatusConflict,
			"worker is connected; disable it and wait for it to go offline, or revoke it, before deleting")
		return
	}
```

and change the zero-row branch to:

```go
	if n == 0 {
		// A zero-row delete after a FOR UPDATE read that said the status was
		// permitted means something is wrong - most plausibly a concurrent
		// delete. Roll back and refuse; NEVER report success. Keep "the fence
		// said no" distinguishable from "the query failed", per markWorkerOffline.
		writeError(w, http.StatusConflict, "worker was modified concurrently; retry")
		return
	}
```

Also add the success log line just after `tx.Commit` (spec 6.4 - one unbudgeted line; the hostname comes from the deleted ROW, never echoed from the request):

```go
	// ONE UNBUDGETED LOG LINE, and the budget question is answered rather than
	// skipped: this site is reachable only by an authenticated admin, fires once
	// per successful delete of a row that then ceases to exist, and cannot be
	// driven by an unauthenticated peer. No counter, no new counters section, no
	// new logKind. No line on refusal: a refusal changes nothing and the caller
	// reads the 409 directly.
	log.Printf("worker deleted: id=%s hostname=%q requeued_tasks=%d reservations_updated=%d enrollments_unlinked=%d",
		uuidStr(id), current.Hostname, len(requeued), scrubbed, unlinked)
```

Add `"log"` to the import block at `internal/api/workers.go:3-14`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestDeleteWorker -v -timeout 900s` - Expected: PASS (all six tests, ten subtests).

- [ ] **Step 5: Mutation battery**

| # | Mutation | Expected |
|---|---|---|
| **M0** control | the `409` in the status gate -> `http.StatusTeapot` | **MUST DIE** on T-D1 and T-D2 |
| M5-go | the `switch` -> `if current.Status == "online" { ...409 }` | dies on T-D2's **`stale`** arm |
| M6 | remove the SQL predicate from `DeleteWorker` (keep the Go gate) | **T-D2 must still PASS** (the Go gate carries it) **and** the store-lane `TestDeleteWorker_PermitsExactlyTheDisconnectedStatuses` from Task 1 must DIE. Run both; that pair is the proof the SQL arm is load-bearing rather than decorative. |
| M7 | remove the Go gate (keep the SQL predicate) | dies on T-D1/T-D2/T-D3's `409` arms - the delete is still refused, only the CODE changes (500 vs 409). If nothing dies, T-D3 is asserting the wrong thing. |
| M8 | the `n == 0` branch -> `writeJSON(w, http.StatusNoContent, nil)` | dies on T-D3 and T-D2 (a no-op reported as success). This stands in for T-D4, which the spec declined to write. |
| M-log | delete the `log.Printf` | **SURVIVES** - no test asserts the log line. Record it; the response body is the tested record, the log line is the operator's. |

- [ ] **Step 6: Commit**

```bash
git add internal/api/workers.go internal/api/workers_delete_integration_test.go
git commit -m "feat(api): refuse deleting a connected worker with 409, and log the destruction

Allow-list in both SQL and Go. M6 and M7 each kill only their own arm, which is
what proves neither is decorative."
```

---

### Task 7: Pin what the handler inherits rather than implements (T-C2, T-E2)

**Files:** modify `internal/api/workers_delete_integration_test.go`.

**Honesty note:** both tests are green on arrival. They are characterization pins, not TDD steps - their RED is at HEAD (no route exists), not at this commit. T-C2 has **no available mutation**: the CASCADE lives in migration `000007` and mutating a migration in a mutant tree changes what every other test's fixture builds. Declared, like M12, rather than faked.

- [ ] **Step 1: Write the tests**

```go
// TestDeleteWorker_CascadesWorkerWorkspaces (T-C2). worker_workspaces.worker_id
// is ON DELETE CASCADE (000007_workspaces.up.sql:6), so this test's job is to
// prove the CASCADE is still there, not that we wrote code. The rows are a
// server-side mirror of agent inventory rebuilt on the next connect, and a
// deleted worker has no next connect.
func TestDeleteWorker_CascadesWorkerWorkspaces(t *testing.T) { /* upsert 2 workspaces via the
	store helper the workspaces tests already use, delete, assert
	ListWorkerWorkspaces returns zero rows */ }

// TestDeleteWorker_OfARevokedWorkerDoesNotChangeCountWorkers (T-E2) pins spec 9
// so README cannot drift into "delete frees budget". CountWorkers is
// `WHERE status != 'revoked'`, so deleting a revoked row frees ZERO ceiling
// budget; deleting an offline row decrements it.
func TestDeleteWorker_OfARevokedWorkerDoesNotChangeCountWorkers(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Ceil Admin", "ceil-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	mk := func(host, status string) pgtype.UUID {
		row, err := q.UpsertWorkerByHostname(t.Context(), store.UpsertWorkerByHostnameParams{
			Name: host, Hostname: host, CpuCores: 4, RamGb: 16, Os: "linux",
		})
		require.NoError(t, err)
		_, err = q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{ID: row.ID, Status: status})
		require.NoError(t, err)
		return row.ID
	}
	revoked := mk("ceil-revoked", "revoked")
	offline := mk("ceil-offline", "offline")

	base, err := q.CountWorkers(t.Context())
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, doDeleteWorker(t, srv, uuidString(revoked), adminToken).Code)
	afterRevoked, err := q.CountWorkers(t.Context())
	require.NoError(t, err)
	assert.Equal(t, base, afterRevoked, "a revoked row is already outside CountWorkers - delete frees NO budget")

	require.Equal(t, http.StatusOK, doDeleteWorker(t, srv, uuidString(offline), adminToken).Code)
	afterOffline, err := q.CountWorkers(t.Context())
	require.NoError(t, err)
	assert.Equal(t, base-1, afterOffline, "an offline row was counted, so deleting it does free budget")
}
```

For T-C2, use whatever `worker_workspaces` upsert `internal/api/workspaces_test.go` already uses (`UpsertWorkerWorkspace`-shaped) and `ListWorkerWorkspaces` to read back; do not invent a new store call.

- [ ] **Step 2: Run them and record the result**

Run: `go test -tags integration -p 1 ./internal/api/... -run "TestDeleteWorker_Cascades|TestDeleteWorker_OfARevoked" -v -timeout 600s`
Expected: PASS immediately. State in the commit body that these are pins, green on arrival.

- [ ] **Step 3: Mutation battery**

| # | Mutation | Expected |
|---|---|---|
| **M0** control | T-E2's `base-1` expectation is the assertion; mutate the handler's delete to skip when `status == "offline"` | **MUST DIE** on T-E2's second arm |
| T-C2 | none available (schema-level) | **Declared, not faked.** |

- [ ] **Step 4: Full api and store integration lanes**

```powershell
go test -tags integration -p 1 ./internal/api/... ./internal/store/... -timeout 900s
```

- [ ] **Step 5: Commit**

```bash
git add internal/api/workers_delete_integration_test.go
git commit -m "test(api): pin the CASCADE and the ceiling arithmetic a worker delete inherits

Both green on arrival - characterization pins, not TDD steps. T-C2 has no
available mutation because the CASCADE lives in a migration; declared rather than
faked, like M12."
```

---

### Task 8: The CLI arm - `relay workers delete --yes` (T-F1)

**THIS TASK MUST LAND BEFORE TASK 10.** Adding `case "delete":` to `doWorkers`'s switch is what makes `relay workers delete` resolvable to `internal/agent/cli_commands_exist_test.go`'s parser. The message edit before this task turns that guard red in between.

**Files:** create `internal/cli/workers_delete_test.go`; modify `internal/cli/workers.go` (`:32`, `:36`, `:45`, the switch at `:52-69`, and a new `doWorkersDelete`).

- [ ] **Step 1: Write the failing tests**

```go
// internal/cli/workers_delete_test.go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkersDelete_ByIDWithYes(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000011"
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/v1/workers/"+workerID, r.URL.Path)
		called = true
		json.NewEncoder(w).Encode(map[string]any{
			"id": workerID, "hostname": "render-07",
			"requeued_tasks": 2, "reservations_updated": 1, "enrollments_unlinked": 1,
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	require.NoError(t, doWorkers(context.Background(), cfg, []string{"delete", "--yes", workerID}, &out))
	require.True(t, called)
	got := out.String()
	require.Contains(t, got, "deleted")
	require.Contains(t, got, "2 task(s) requeued")
	require.Contains(t, got, "1 reservation(s) updated")
	require.Contains(t, got, "1 enrollment(s) unlinked")
}

// TestWorkersDelete_RequiresConfirmation (T-F1). VACUITY: assert NO REQUEST WAS
// MADE, not just the exit code - an implementation that deletes and then errors
// would pass an exit-code-only check.
func TestWorkersDelete_RequiresConfirmation(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000012"
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"delete", workerID}, &out)
	require.Error(t, err, "without --yes the command must exit non-zero")
	require.Contains(t, err.Error(), "--yes")
	require.Empty(t, requests, "no request may be issued without --yes")
	require.Contains(t, out.String(), workerID, "it must print what it WOULD delete")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/... -run TestWorkersDelete -v -timeout 60s`
Expected: FAIL - `unknown workers subcommand: delete` on both tests.

- [ ] **Step 3: Write minimal implementation**

`internal/cli/workers.go`: add `case "delete": return doWorkersDelete(ctx, c, args[1:], w)` to the switch (after `revoke`), and update **all three** usage strings - `:32` (the doc comment listing subcommands), `:36` (`Usage:`) and `:45` (the `len(args) == 0` error). They are three separate string literals and changing one is the partial-fix shape. New list: `<list|get|disable|enable|revoke|delete|workspaces|evict-workspace>`.

```go
// deleteResp decodes the counts DELETE /v1/workers/{id} reports. Relay has no
// audit log, so these three numbers are the only record of what was destroyed.
type deleteResp struct {
	Hostname            string `json:"hostname"`
	RequeuedTasks       int    `json:"requeued_tasks"`
	ReservationsUpdated int    `json:"reservations_updated"`
	EnrollmentsUnlinked int    `json:"enrollments_unlinked"`
}

// doWorkersDelete destroys a worker identity (admin only). --yes IS REQUIRED AND
// IS NOT AN INTERACTIVE PROMPT: every destructive path in this CLI is flag-driven
// and non-interactive, and a prompt breaks scripted use (spec 8.5).
func doWorkersDelete(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm the delete; required, and there is no undo")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay workers delete --yes <worker-id-or-hostname>")
	}
	target := fs.Arg(0)
	if !*yes {
		fmt.Fprintf(w, "would delete worker %s (its assignments are requeued, its reservations scrubbed, its enrollment link nulled).\n", target)
		return fmt.Errorf("refusing to delete without --yes")
	}
	id, err := resolveWorkerID(ctx, c, target)
	if err != nil {
		return err
	}
	var resp deleteResp
	if err := c.Do(ctx, "DELETE", "/v1/workers/"+id, nil, &resp); err != nil {
		return fmt.Errorf("delete worker: %w", err)
	}
	fmt.Fprintf(w, "deleted; %d task(s) requeued, %d reservation(s) updated, %d enrollment(s) unlinked.\n",
		resp.RequeuedTasks, resp.ReservationsUpdated, resp.EnrollmentsUnlinked)
	return nil
}
```

Note the `!*yes` branch prints **before** resolving the id, so it issues no request.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/... -run TestWorkersDelete -v -timeout 60s` - Expected: PASS.

- [ ] **Step 5: Prove the ordering constraint held**

Run: `go test ./internal/agent/... -run TestOperatorMessages_OnlyPrescribeCommandsThatExist -v -timeout 60s`
Expected: PASS, **with `internal/agent/cli_commands_exist_test.go` and `internal/agent/messages.go` both unmodified**. This is the "before" half of the ordering proof: the command now exists and no message names it yet, so the guard is green from both directions. Confirm with `git status` that nothing under `internal/agent/` is dirty.

- [ ] **Step 6: Mutation battery**

| # | Mutation | Expected |
|---|---|---|
| **M0** control | the DELETE path -> `"/v1/workers/"+id+"/token"` | **MUST DIE** on `TestWorkersDelete_ByIDWithYes` (`require.Equal` on the path) |
| M11 | delete the `if !*yes` block | dies on T-F1 (`require.Empty(t, requests)`) |
| M11b | `if !*yes { fmt.Fprintf(...) }` without the `return err` | dies on T-F1's `require.Error` **and** `require.Empty(t, requests)` |
| M-counts | `resp.RequeuedTasks` -> literal `0` in the Fprintf | dies on `2 task(s) requeued` |

- [ ] **Step 7: Commit**

```bash
git add internal/cli/workers.go internal/cli/workers_delete_test.go
git commit -m "feat(cli): relay workers delete --yes

--yes is a required flag, not a prompt: every destructive CLI path here is
flag-driven and a prompt breaks scripted use. All three usage strings updated.
Lands BEFORE the agent message edit so the ghost-command guard is never
transiently red for the wrong reason."
```

---

### Task 9: `resolveWorkerID` reaches revoked workers (T-F2)

**Files:** modify `internal/cli/workers.go:163-179`, `internal/cli/workers_delete_test.go`.

**Why this is required scope (spec R8):** `resolveWorkerID` lists `GET /v1/workers`, and every paginated variant carries `WHERE status != 'revoked'` (`workers.sql:125-131`). So `relay workers delete render-07` fails with `no worker found with hostname "render-07"` for exactly the rows an operator wants gone.

- [ ] **Step 1: Write the failing test**

```go
// TestWorkersDelete_ResolvesARevokedHostname (T-F2). VACUITY: a fixture serving
// the worker from /v1/workers too passes WITHOUT the fallback, so the worker must
// be ABSENT from the primary list.
func TestWorkersDelete_ResolvesARevokedHostname(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000013"
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/workers":
			// Empty on purpose: GET /v1/workers excludes revoked rows.
			json.NewEncoder(w).Encode(relayclient.PageEnvelope[workerResp]{Items: []workerResp{}, Total: 0})
		case r.Method == "GET" && r.URL.Path == "/v1/workers/revoked":
			json.NewEncoder(w).Encode(relayclient.PageEnvelope[workerResp]{
				Items: []workerResp{{ID: workerID, Hostname: "render-07", Status: "revoked"}}, Total: 1,
			})
		case r.Method == "DELETE" && r.URL.Path == "/v1/workers/"+workerID:
			deleted = true
			json.NewEncoder(w).Encode(map[string]any{"id": workerID})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	require.NoError(t, doWorkers(context.Background(), cfg, []string{"delete", "--yes", "render-07"}, &out))
	require.True(t, deleted, "the DELETE must reach the revoked worker's id")
}
```

Add the `relay/internal/relayclient` import.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/... -run TestWorkersDelete_ResolvesARevokedHostname -v -timeout 60s`
Expected: FAIL - `no worker found with hostname "render-07"`.

- [ ] **Step 3: Write minimal implementation**

Extend `resolveWorkerID` with a fallback (the shared-helper route, so `relay workers revoke <hostname>` gets the same fix for free - spec 8.5):

```go
// resolveWorkerID returns the UUID for target, resolving a hostname via GET
// /v1/workers and, on a miss, GET /v1/workers/revoked.
//
// THE FALLBACK IS NOT COSMETIC. Every paginated variant behind GET /v1/workers
// carries `WHERE status != 'revoked'` (query/workers.sql:125-131), so without it
// a hostname cannot be resolved for exactly the rows an operator most wants to
// delete. The primary list is tried first, so a live worker never costs a second
// round trip and no existing caller's behaviour changes for a non-revoked host.
func resolveWorkerID(ctx context.Context, c *relayclient.Client, target string) (string, error) {
	if looksLikeUUID(target) {
		return target, nil
	}
	for _, path := range []string{"/v1/workers", "/v1/workers/revoked"} {
		workers, _, err := relayclient.FetchAllPages[workerResp](ctx, c, path, nil, 0)
		if err != nil {
			// The revoked list is admin-only; a non-admin caller gets an error
			// there and should still see the primary list's miss, not an auth
			// error about an endpoint they did not ask for.
			if path == "/v1/workers" {
				return "", fmt.Errorf("list workers: %w", err)
			}
			break
		}
		for _, wk := range workers {
			if wk.Hostname == target {
				return wk.ID, nil
			}
		}
	}
	return "", fmt.Errorf("no worker found with hostname %q", target)
}
```

- [ ] **Step 4: Run the whole cli package**

Run: `go test ./internal/cli/... -v -timeout 120s`
Expected: PASS. **`TestWorkersRevoke_NotFound` (`workers_revoke_test.go:68-87`) is the one to watch**: its fixture serves only `/v1/workers` and now receives a second request to `/v1/workers/revoked`. Its handler asserts `require.Equal(t, "/v1/workers", r.URL.Path)`, so it will FAIL. **That is a real observable-behaviour change on `revoke`, which spec 8.5 anticipated.** Resolution: extend that fixture to serve `/v1/workers/revoked` as an empty page (its assertion is about the miss, not about the request count), and note the change in the commit. Do not weaken the delete fallback to keep the fixture untouched.

- [ ] **Step 5: Mutation battery**

| # | Mutation | Expected |
|---|---|---|
| **M0** control | the loop's second path -> `"/v1/workers/nonexistent"` | **MUST DIE** on T-F2 |
| M-order | reverse the two paths | **survives** T-F2 - record it, then confirm `TestWorkersRevoke_ByHostname` still passes; the order is a round-trip optimisation, not a correctness property, and saying so is better than implying a kill |
| M-fallback | drop the loop, restore the single-list body | dies on T-F2 |

- [ ] **Step 6: Commit**

```bash
git add internal/cli/workers.go internal/cli/workers_delete_test.go internal/cli/workers_revoke_test.go
git commit -m "fix(cli): resolve a hostname against the revoked list too

GET /v1/workers excludes revoked rows, so a hostname could not be resolved for
exactly the workers an operator wants to delete (spec R8). Shared helper, so
revoke gets it too; TestWorkersRevoke_NotFound's fixture gains the second route."
```

---

### Task 10: The agent exit message, and closing the ghost-command loop

**Files:** modify `internal/agent/messages.go:38-41`, `internal/agent/messages_test.go` (two edits). **`internal/agent/cli_commands_exist_test.go` is NOT edited.**

**This task closes the loop the previous slice opened.** `TestOperatorMessages_OnlyPrescribeCommandsThatExist` going green *because the command now exists* - not because a check was removed - is this slice's closing acceptance criterion, and it is the item's own Acceptance bullet 3.

- [ ] **Step 1: Edit the message**

In `authFailureMessage`'s token-less arm, replace this sentence (`messages.go:39-41`):

> `"Relay has no command that frees a claimed hostname for token-less enrollment, so if no admin will issue a token there is no remedy on this path;"`

with:

```go
			"hostname, so a renamed machine rejoins as a new worker. Or ask an admin to " +
			"free the hostname outright: `relay workers delete --yes <id-or-hostname>` " +
			"removes the worker row, which is the only thing that unclaims a hostname - it " +
			"requeues the worker's tasks and cannot be undone; (3) the fleet may be at " +
```

Keep the remedy ORDER (spec 12.4): revoke-then-enrollment-token stays remedy 1, rename stays remedy 2, delete becomes remedy 3. Do not promote delete.

- [ ] **Step 2: Run the tests to see both the RED and the guard**

Run: `go test ./internal/agent/... -v -timeout 60s`
Expected: exactly one failure, in `TestAuthFailureMessage_TokenlessArmNamesAllThreeCausesAndBothRemedies`:

```
Error: "agent: authentication failed - token-less auto-enroll was rejected. ..." should not contain "workers delete"
Test:  TestAuthFailureMessage_TokenlessArmNamesAllThreeCausesAndBothRemedies
Messages: this message must not prescribe a command that does not exist; add the subcommand to internal/cli/workers.go first, then say so here
```

**`TestOperatorMessages_OnlyPrescribeCommandsThatExist` must be GREEN in this same run.** That is the whole point: the property guard resolves `relay workers delete` against the real command set added in Task 8, while the stale deny-list does not know the ghost graduated. If the property guard is red here, Task 8 was skipped or its `case "delete":` did not land - stop and fix that, do not touch the guard.

- [ ] **Step 3: Make exactly two edits to `messages_test.go`**

(a) At `:148`, remove the `"workers delete"` entry and keep the other two, with the reason recorded:

```go
	// "workers delete" GRADUATED FROM GHOST TO REAL COMMAND on 2026-08-26
	// (docs/superpowers/plans/2026-08-26-worker-delete.md): internal/cli's workers
	// switch now has a delete arm, DeleteWorker is a real statement, and
	// DELETE /v1/workers/{id} is a real admin-only route. Forbidding it here would
	// forbid the true remedy. The other two spellings are still ghosts.
	for _, ghost := range []string{"relay workers rm", "workers remove"} {
```

(b) In the same test's want-list (`:103-120`), add a positive entry so remedy 3 is pinned rather than merely permitted:

```go
		"rename",         // remedy 2: the other escape hatch that actually exists
		"workers delete", // remedy 3: free the hostname outright - a REAL command since 2026-08-26
```

Make no other change to this file, and no change at all to `cli_commands_exist_test.go`.

- [ ] **Step 4: Run to verify green**

Run: `go test ./internal/agent/... -v -timeout 60s` - Expected: PASS, all tests.

- [ ] **Step 5: Mutation battery - M10, the loop-closing mutation**

| # | Mutation | Expected |
|---|---|---|
| **M0** control | in `messages.go`, `relay workers delete` -> `relay workers destroy` | **MUST DIE** in `TestOperatorMessages_OnlyPrescribeCommandsThatExist`: `... prescribes 'relay workers destroy', but "workers" has no "destroy" subcommand` |
| **M10** | remove `case "delete":` from `doWorkers`'s switch in `internal/cli/workers.go` | **dies in `TestOperatorMessages_OnlyPrescribeCommandsThatExist`** - a cross-package kill, and the direct evidence that the guard is green *because the command exists*. Record the exact failure text in the commit body. |
| M10b | remove the `"workers delete"` want-list entry added in (b) | survives - correct: it is a positive pin, and its own kill is M-msg below |
| M-msg | in `messages.go`, delete the whole new sentence | dies on the want-list entry from (b) |

- [ ] **Step 6: Commit**

```bash
git add internal/agent/messages.go internal/agent/messages_test.go
git commit -m "fix(agent): the terminal exit message names the real hostname-freeing command

The sentence saying relay has no such command became false in Task 8. Delete is
remedy 3, after revoke-then-token and rename - it is destructive and the other two
are not. TestOperatorMessages_OnlyPrescribeCommandsThatExist passes byte-identical
and unedited, BECAUSE the command exists: removing the CLI case arm (M10) turns it
red across package boundaries."
```

---

### Task 11: The prose sweep

Wrong prose is this project's dominant defect class and "update the docs" gets partially done, so every site is listed with its current wording to grep for. **All eight sites in one commit** - a partial sweep leaves the tree contradicting itself.

**Files:** `README.md` (6 sites), `internal/store/query/tasks.sql:603-608`, `internal/store/query/workers.sql:143-149`, `internal/worker/handler.go:71-78`.

- [ ] **Step 1: README, six sites**

1. **`:353`** - the parenthetical beginning "(This used to say "disable or delete the worker ...". **All four of its factual claims are now false.** Rewrite as what delete does: requeues assignments (does not orphan), scrubs reservations (does not leave them untouched), nulls the enrollment link (does not fail), and refuses a connected worker. **Do not just delete the parenthetical** - it is the only place README explains why the three relations matter.
2. **`:365-369`** - "it stays revoked until an admin clears or deletes it". Now true. Add the third route explicitly: `relay workers delete --yes <id>` removes the row and frees the hostname for token-less auto-enroll.
3. **`:371-378`** - "relay has no worker-delete, so the revoked row would block its own recovery permanently." Keep the asymmetry it justifies (enrollment tokens bind a NULL-hash row, auto-enroll does not); replace only the supporting clause - the enrollment-token path is the *non-destructive* recovery, delete is the destructive one.
4. **`:414-421`** - "**Nothing reclaims either the row or the hostname** - relay has no worker-delete at any layer, so revoked junk rows are permanent." Delete now reclaims both, **manually and per row**. Reaping is still not done. Say both halves; do not let the correction imply the TTL reaper landed.
5. **`:430-433`** (ladder step 1) - "is the only cleanup relay has - there is no worker-delete". Correct the clause. **DO NOT ADD DELETE AS A LADDER STEP** (spec 9 / spec E): the ladder is what an operator does in response to `fleet_at_ceiling`, a signal an attacker can drive, and deleting 1024 rows under an active attacker is the same treadmill as revoking them, only more destructive. **And README must not gain a blanket "delete frees budget" claim** - the accurate sentence is that delete always frees the **hostname** and frees **budget** only for a worker that was not already revoked, which means the natural revoke-then-later-delete sequence frees no budget at all.
6. **The CLI reference and the REST table** - the site most likely to be missed. Add a `#### relay workers delete` block after `relay workers revoke` (`:805-812`), and a row to the REST table after `:1334`:

```
| `DELETE` | `/v1/workers/{id}` | Delete a worker row (admin only). Permitted only while the worker is disconnected (`offline` or `revoked`); a connected worker returns 409. Requeues the worker's assigned tasks first, scrubs its id out of every reservation, nulls the consuming enrollment's `consumed_by`, and cascades its workspace rows. Returns 200 with `requeued_tasks`, `reservations_updated` and `enrollments_unlinked`. Frees the hostname for re-enrollment; frees ceiling budget only if the worker was not already revoked. |
```

The CLI block must state: `--yes` is required, there is no undo, a reservation left with an empty `worker_ids` reserves nothing and must be removed or re-pointed by hand (spec 7).

- [ ] **Step 2: `internal/store/query/tasks.sql:603-608`**

Replace "which is unreachable today, because nothing in this repo DELETEs a worker" with the truth:

```sql
-- also documents the one state this watchdog cannot recover - a `dispatched` row
-- whose worker_id was nulled by workers' ON DELETE SET NULL. THAT STATE IS NOW
-- REACHABLE - DeleteWorker exists (query/workers.sql) - and what keeps it from
-- occurring is ORDERING, not unreachability: handleDeleteWorker runs
-- RequeueWorkerTasks first, in the same transaction, so every assignment is ended
-- with an epoch bump while worker_id still names it. Any FUTURE deleter that
-- skips that step strands the row here permanently, unreachable by every
-- worker-keyed statement in this file.
```

- [ ] **Step 3: `internal/store/query/workers.sql:143-149`**

In `CountWorkers`'s comment, replace "revoke is the ONLY cleanup relay has - there is no worker-delete at any layer, so a revoked row is permanent" with: revoke is the first ladder remedy because it frees budget with no restart and is non-destructive; `DeleteWorker` also exists but **frees zero budget for an already-revoked row**, since this count already excludes it, and it is deliberately not in README's ceiling ladder. Keep the conclusion unchanged.

- [ ] **Step 4: `internal/worker/handler.go:71-78`**

In `errCredentialLive`'s comment, keep the decision and rebuild its support: the enrollment-token path is the **non-destructive** recovery for a revoked row, which is why refusing every existing row here would remove the only route that preserves the worker's history. Delete exists but destroys the row, so it is not a substitute.

- [ ] **Step 5: Regenerate and verify the query comments actually changed**

Two of the four edits are query comments, so:

```powershell
sqlc generate
git diff --ignore-all-space --stat
Select-String -Path internal/store/tasks.sql.go -Pattern "ORDERING, not unreachability"
Select-String -Path internal/store/workers.sql.go -Pattern "frees zero budget"
```

Expected: one hit each. **Zero hits means the CRLF revert silently discarded the regeneration** - the exact recorded failure mode for query-comment edits - leaving a generated doc comment contradicting its own source. Re-generate and redo the checkout more narrowly.

- [ ] **Step 6: Verify no site was missed**

```powershell
Select-String -Path README.md,internal/**/*.go,internal/**/*.sql -Pattern "no worker-delete|nothing in this repo DELETEs|has no command that frees"
```

Expected hits: **only** `docs/retros/2026-08-25-auto-enroll-guards.md`, `docs/superpowers/specs/2026-08-25-auto-enroll-guards.md`, `docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md:304` and this slice's own spec/plan - all dated records that **must not be rewritten** (spec 12.8). Any hit under `README.md`, `internal/` is a missed site.

- [ ] **Step 7: Run the default lane and commit**

```powershell
go test ./... -timeout 120s
go vet -tags integration ./...
```

```bash
git add README.md internal/store/query/tasks.sql internal/store/query/workers.sql internal/store/tasks.sql.go internal/store/workers.sql.go internal/worker/handler.go
git commit -m "docs: relay has a worker-delete, and four comments said otherwise

Six README sites, the watchdog's unreachability claim, CountWorkers's 'only
cleanup' clause, and errCredentialLive's support. Delete is deliberately NOT added
to the ceiling remedy ladder and README gains no 'delete frees budget' claim:
CountWorkers already excludes revoked rows, so deleting one frees nothing."
```

---

### Task 12: Whole-slice verification and backlog close (conductor)

- [ ] **Step 1: Run every gate this machine can run**

```powershell
go build ./...
go test ./... -timeout 120s
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/store/... ./internal/api/... ./internal/cli/... ./internal/agent/... ./internal/worker/... -timeout 900s
go test -tags integration -p 1 ./... -timeout 900s
```

`go test -race` is **not runnable here** (environmental ThreadSanitizer allocation failure). CI's `race + integration-build` job is the gate; do not claim a local race result.

- [ ] **Step 2: Confirm the two landmines are resolved**

```powershell
git diff --stat main -- internal/agent/cli_commands_exist_test.go   # MUST be empty
go test ./internal/agent/... -run TestOperatorMessages -v -timeout 60s   # MUST pass
```

The first is the byte-identical requirement. The second, passing with an unmodified guard file and a message that now names `relay workers delete`, is the slice's closing acceptance criterion.

- [ ] **Step 3: Confirm the working tree is exactly the expected file set**

```powershell
git diff --name-only main
```

Expected: the 12 edited files plus the 3 new test files from the inventory, and nothing else. **No files under `web/`** (in particular `web/dist/` must be clean), no `internal/store/models.go`, no hand-edited `*.sql.go` beyond the four regenerated ones.

- [ ] **Step 4: Close the backlog item**

Run `/backlog close no-worker-delete-at-any-layer`. Never hand-edit the item's `status`. The Resolution note must record, per spec 4.1: the feature shipped; the item's **severity argument was self-refuting** (delete is admin-only, so it needs exactly the admin whose absence was the premise) and the priority was corrected to `medium` at spec time; the FK blocker did not apply to the item's own auto-enrolled motivating case; and the stale-reservation claim was backwards - `reservedIDs` is an exclusion set, so the scrub ships for the contract, not for the dispatcher.

---

## Whole-slice verification commands

| Gate | Command | Notes |
|---|---|---|
| Build | `go build ./...` | |
| Default lane | `go test ./... -timeout 120s` | ~656 Go tests today |
| Integration-tag compile | `go vet -tags integration ./...` | catches shared-signature breaks the default lane never compiles |
| Integration lane | `go test -tags integration -p 1 ./... -timeout 900s` | Docker Desktop required; `-p 1` mandatory |
| Race | **CI only** | local ThreadSanitizer allocation failure is environmental |
| Web | **not applicable** | this slice touches no files under `web/` |

## Self-review notes, and what I would push back on in the spec

1. **Spec 13.1's open question is answered in Task 3's lane note**: `internal/api` has no default-lane server seam (every helper is `//go:build integration`), so **T-D3 is integration-lane** and the CI default-lane coverage gap widens by one test. The spec asked the plan to look and record rather than assume; recorded.
2. **T-F2 is not free, and the spec's "if that turns out to change `revoke`'s observable behaviour, add a delete-local resolver" hedge resolves to the first branch.** `TestWorkersRevoke_NotFound` (`internal/cli/workers_revoke_test.go:68-87`) asserts `require.Equal(t, "/v1/workers", r.URL.Path)` on every request, so the shared-helper fallback **does** break it. Task 9 takes the shared route anyway and extends the fixture, because a delete-local resolver would leave `revoke <revoked-hostname>` broken and file the remainder - two paths where one will do. Flagging it as a deviation from the spec's default reading.
3. **Spec 6.4's `deleteWorkerResponse` embeds `workerResponse`, which runs `toWorkerResponse` on the row read under `FOR UPDATE`.** That helper synthesises `status: "disabled"` when `disabled_at` is set - so a deleted worker that was disabled-and-offline reports `"status":"disabled"` in its own delete response, not `"offline"`. That is consistent with every other worker response and is not worth special-casing, but it means the response body's `status` is not the value the allow-list matched on. Worth one sentence in the handler comment; not worth a code change.
4. **T-C2 has no mutation and I did not manufacture one.** The CASCADE lives in migration `000007`, and mutating a migration changes what every other test's fixture builds. Declared alongside M12 rather than faked, per the spec's own precedent.
5. **The `n == 0` -> `409 "worker was modified concurrently"` message is reachable only via a race the tests cannot drive** (spec T-D4 said as much). M8 covers the *code*, not the string. No test asserts that message and none should.
6. **Nothing here is multi-session.** One spec, one plan, one PR. No `## Stage N` units, so **no `/backlog phases` run is needed** for this plan. The three follow-on proposals in spec 17 are the conductor's to file if wanted - the SPA delete affordance, preserving the enrollment audit link, and the silently-inert emptied reservation - plus spec 17.4 (`relay workers revoke <hostname>` for an already-revoked worker), which **Task 9 fixes outright** via the shared helper, so 17.4 should be closed as done rather than filed.
