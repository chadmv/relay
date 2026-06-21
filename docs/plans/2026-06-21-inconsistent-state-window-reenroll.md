# Fix Inconsistent-State Window on Worker Re-Enroll Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the brief window where a re-enrolling worker is `status='revoked'` with `revoked_at=NULL` by folding the revoked-status clear into the `SetWorkerAgentToken` query.

**Architecture:** The fix is a query-text-only change in `internal/store/query/workers.sql`. `SetWorkerAgentToken` already clears `revoked_at`; we extend it to also reset a `'revoked'` status to `'offline'` via a `CASE`, so `revoked_at` and the revoked status are cleared atomically inside the enroll transaction. The `CASE` leaves every non-revoked caller's status byte-identical. The transient inconsistency lives between transaction commit and the post-commit `RegisterWorkerConnection`, so it is not directly observable in a live test; instead the deterministic post-condition is asserted at the store-query level with an integration test.

**Tech Stack:** Go, sqlc, PostgreSQL, testcontainers-go integration tests (`//go:build integration`).

---

## Slice independence

**Backend-only.** This change touches only the store layer (one SQL query + its sqlc-generated Go) and one store integration test. There is no frontend work and no API/handler change. No Phase 3 parallelism applies.

## Validation of the analysis (confirmed against the real code/schema)

- **Bug confirmed.** `ClearWorkerAgentToken` (internal/store/query/workers.sql:77-80) sets `status='revoked', revoked_at=NOW()`. `SetWorkerAgentToken` (workers.sql:70-75) currently reads exactly:
  `UPDATE workers SET agent_token_hash = $2, revoked_at = NULL WHERE id = $1;` - it clears `revoked_at` but leaves `status`. `enrollAndRegister` calls it inside the enroll tx (internal/worker/handler.go:203-207); `status` only flips to `'online'` post-commit in `finishRegister` -> `RegisterWorkerConnection` (handler.go:296-303, workers.sql:30-44). So between commit and that query the row is `status='revoked', revoked_at=NULL`. Confirmed.
- **autoEnroll path is not affected.** `autoEnrollAndRegister` rejects `status='revoked'` first via `errWorkerRevoked` (handler.go:256-258) before calling `SetWorkerAgentToken`. `reconnectAndRegister` never calls `SetWorkerAgentToken`. So the window is specific to the enroll-token revive path. Confirmed.
- **CHECK constraint vocabulary is legal for the fix.** Migration `000019_status_vocabulary_checks.up.sql:8-10` adds `workers_status_check CHECK (status IN ('online','offline','stale','revoked'))`. Both `'offline'` and `'revoked'` are in the vocabulary, so `CASE WHEN status = 'revoked' THEN 'offline' ELSE status END` is constraint-legal. Confirmed.
- **Query-text-only change.** Generated `SetWorkerAgentTokenParams` (internal/store/workers.sql.go:1168-1171) has exactly `ID` and `AgentTokenHash`. Adding a `CASE` over an existing column introduces no new bind parameter, so `make generate` only updates the SQL string and doc comment; the `SetWorkerAgentTokenParams` struct and the two call sites in handler.go (lines 203, 275) are unchanged. Confirmed.
- **Test fixtures exist.** `internal/store/testhelper_test.go` provides `newTestQueries(t)`, `newTestWorker(t, q)` (upserts by hostname derived from `t.Name()`, then `GetWorker`), and `ptrStr`. `internal/store/workers_revoked_test.go` already exercises the `ClearWorkerAgentToken` -> `SetWorkerAgentToken` round trip (`TestSetWorkerAgentToken_ClearsRevokedAt`) and is the natural place to add the new status assertion. Confirmed.

## File structure

- Modify: `internal/store/query/workers.sql` (lines 70-75: the `SetWorkerAgentToken` query body + doc comment).
- Modify (generated, via `make generate` only - never hand-edit): `internal/store/workers.sql.go` (the `setWorkerAgentToken` const string and doc comment around lines 1164-1182).
- Modify (test): `internal/store/workers_revoked_test.go` (add one integration test next to `TestSetWorkerAgentToken_ClearsRevokedAt`).

No other files change. Do not touch `models.go`, `*.sql.go` by hand, or any handler/API code.

---

### Task 1: Failing integration test for the re-enroll status reset

**Files:**
- Test: `internal/store/workers_revoked_test.go` (append a new test function; reuse existing helpers `newTestQueries`, `newTestWorker`, `ptrStr`)

- [ ] **Step 1: Write the failing test**

Append this function to `internal/store/workers_revoked_test.go` (after `TestSetWorkerAgentToken_ClearsRevokedAt`, before `TestListRevokedWorkersPage_ReturnsOnlyRevoked`). It asserts the deterministic post-condition the fix guarantees: after revoke-then-reenroll, `revoked_at` is null AND `status` is no longer `'revoked'` (specifically `'offline'`).

```go
func TestSetWorkerAgentToken_RevivesRevokedStatus(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	w := newTestWorker(t, q)

	// Give the worker a token, then revoke it: status='revoked', revoked_at set.
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: w.ID, AgentTokenHash: ptrStr("hash-1"),
	}))
	_, err := q.ClearWorkerAgentToken(ctx, w.ID)
	require.NoError(t, err)

	revoked, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, "revoked", revoked.Status)
	require.True(t, revoked.RevokedAt.Valid, "precondition: revoke stamps revoked_at")

	// Re-enroll: setting a fresh token must clear BOTH revoked_at and the
	// revoked status atomically, leaving no window where a revoked worker has
	// a null revocation timestamp.
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: w.ID, AgentTokenHash: ptrStr("hash-2"),
	}))

	reloaded, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.False(t, reloaded.RevokedAt.Valid, "revoked_at must be cleared on re-enroll")
	require.NotEqual(t, "revoked", reloaded.Status, "status must not remain revoked after re-enroll")
	require.Equal(t, "offline", reloaded.Status, "revived worker should be offline until it connects")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run (requires Docker Desktop running; the `p4` CLI is not needed for store tests):
```bash
go test -tags integration -p 1 ./internal/store/... -run TestSetWorkerAgentToken_RevivesRevokedStatus -v -timeout 120s
```
Expected: FAIL. The final assertions fail because the current query leaves `status='revoked'`: `require.NotEqual` / `require.Equal "offline"` fail with actual `"revoked"`. (The `revoked_at` assertion passes - that part already works; the status assertions are what prove RED.)

- [ ] **Step 3: Commit the failing test**

```bash
git add internal/store/workers_revoked_test.go
git commit -m "test(store): assert re-enroll clears revoked status, not just revoked_at"
```

---

### Task 2: Fix the SetWorkerAgentToken query

**Files:**
- Modify: `internal/store/query/workers.sql:70-75`
- Regenerate (via `make generate`, do not hand-edit): `internal/store/workers.sql.go`

- [ ] **Step 1: Edit the query**

In `internal/store/query/workers.sql`, replace the `SetWorkerAgentToken` block (lines 70-75) with:

```sql
-- name: SetWorkerAgentToken :exec
-- Sets the long-lived agent token on (re)enrollment. Clears revoked_at and, for
-- a previously revoked worker, resets status to 'offline' so revoked_at and the
-- revoked status are cleared together (regaining a valid token means the worker
-- is no longer revoked). This is the one place a revoked worker is revived
-- (revocation nulls the token, so the reconnect-by-token path can no longer find
-- it). The CASE leaves every non-revoked caller's status unchanged. 'offline' is
-- the natural not-yet-connected state; RegisterWorkerConnection flips it to
-- 'online' a moment later when the agent's connection registers.
UPDATE workers SET agent_token_hash = $2, revoked_at = NULL,
    status = CASE WHEN status = 'revoked' THEN 'offline' ELSE status END
WHERE id = $1;
```

- [ ] **Step 2: Regenerate the sqlc store layer**

Run:
```bash
make generate
```
Then handle the known CRLF caveat from CLAUDE.md (sqlc emits LF; on this CRLF repo it rewrites line endings across generated files):
```bash
git diff --ignore-all-space
```
Confirm the only real content change is in `internal/store/workers.sql.go`: the `setWorkerAgentToken` const string now contains the `CASE ... END` clause and the doc comment matches the new query. Revert any file whose diff is LF-only (no real content change), e.g.:
```bash
git checkout -- <path-with-only-LF-changes>
```
Verify no new struct field was emitted (the change adds no bind parameter): `SetWorkerAgentTokenParams` must still have exactly `ID` and `AgentTokenHash`, and `func (q *Queries) SetWorkerAgentToken` must still call `q.db.Exec(ctx, setWorkerAgentToken, arg.ID, arg.AgentTokenHash)`. If `make generate` produced a new parameter, the SQL was mis-edited - re-check Step 1.

- [ ] **Step 3: Run the new test to verify it passes**

Run:
```bash
go test -tags integration -p 1 ./internal/store/... -run TestSetWorkerAgentToken_RevivesRevokedStatus -v -timeout 120s
```
Expected: PASS.

- [ ] **Step 4: Run the surrounding token/revoked tests to confirm no regression**

Run (covers `TestSetWorkerAgentToken_ClearsRevokedAt`, `TestWorkerAgentToken_*`, `TestClearWorkerAgentToken_StampsRevokedAt`, `TestListRevokedWorkersPage_ReturnsOnlyRevoked`):
```bash
go test -tags integration -p 1 ./internal/store/... -run "TestWorkerAgentToken|TestSetWorkerAgentToken|TestClearWorkerAgentToken|TestListRevokedWorkersPage" -v -timeout 180s
```
Expected: all PASS. These prove non-revoked callers are unaffected (status unchanged for a fresh/live worker) and the revoke + list paths still work.

- [ ] **Step 5: Run the unit build/test to confirm the generated change compiles**

Run:
```bash
make test
```
Expected: PASS (no integration tests run; this just confirms the regenerated `workers.sql.go` compiles and nothing else broke).

- [ ] **Step 6: Commit**

```bash
git add internal/store/query/workers.sql internal/store/workers.sql.go
git commit -m "fix(store): clear revoked status on re-enroll, not just revoked_at"
```

---

## Self-review

- **Spec coverage.** The single requirement (close the revoked/`revoked_at=NULL` window by folding the status flip into the enroll path) is implemented by the `CASE` in Task 2 and verified by the Task 1 test. The chosen fix matches the spec's SQL exactly.
- **Placeholder scan.** No TBD/TODO; every code step shows real code; the test body is complete and uses only confirmed helpers (`newTestQueries`, `newTestWorker`, `ptrStr`, `q.SetWorkerAgentToken`, `q.ClearWorkerAgentToken`, `q.GetWorker`) and confirmed fields (`store.Worker.Status`, `.RevokedAt.Valid`).
- **Type consistency.** `SetWorkerAgentTokenParams{ID, AgentTokenHash}` matches the generated struct; the query change adds no parameter, so the struct and call sites are stable. `ClearWorkerAgentToken` returns rows (`:execrows`) - the test discards the count with `_, err`, matching existing tests.
- **Invariants.** No epoch-fenced query is touched. The status transition lands inside the existing enroll transaction (caller unchanged) and does not return a task to pending, so the epoch fence is not in scope. `make generate` is included as required for any `.sql` edit; the never-hand-edit-`*.sql.go` rule is respected (only `make generate` writes it).
