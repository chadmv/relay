# Job Cancel Owner-or-Admin Authorization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an owner-or-admin authorization gate to `handleCancelJob` so a non-owner non-admin caller gets a 404 with zero side effects, while owners and admins keep cancelling.

**Architecture:** A pure pre-side-effect authorization gate inside `handleCancelJob`, placed immediately after the `GetJob` load and before the terminal-state check. Returning early lets the already-deferred `tx.Rollback` undo the transaction, so no task is cancelled, no job status flips, and no agent `CancelTask` signal is sent (agent sends and broker publish happen only after `tx.Commit`). The fix mirrors the established `ownedScheduledJob` pattern.

**Tech Stack:** Go, `net/http`, sqlc store (`store.Queries`), `pgtype.UUID`, testify, testcontainers-go Postgres (integration tests).

---

## Slice declaration

- **Backend-only.** Single slice, executed by one relay-backend-engineer. **Not parallel** - there is no frontend component. The entire change lives in `internal/api/jobs.go` and its integration test file.

## Critical facts (verified against current code, do not re-guess)

- Handler: `handleCancelJob` in `internal/api/jobs.go:677-768`.
  - `GetJob` load at `internal/api/jobs.go:695` (inside the open `tx`; `tx` begun at line 686, `defer tx.Rollback(ctx)` at line 691).
  - Terminal-state check at `internal/api/jobs.go:704`.
  - `tx.Commit` at line 745; agent `CancelTask` sends loop at 750-759; broker publish at 761. All side effects are post-commit.
- `AuthUser` (`internal/api/context.go:14-20`) has `ID pgtype.UUID` and `IsAdmin bool`. `UserFromCtx(ctx) (AuthUser, bool)` reads it.
- `store.Job.SubmittedBy` is `pgtype.UUID`.
- **pgtype.UUID comparison idiom (verified):** `ownedScheduledJob` (`internal/api/scheduled_jobs.go:163`) uses `row.OwnerID != u.ID` directly with `!=` on two `pgtype.UUID` values. Use the same idiom: `job.SubmittedBy != u.ID`. Do NOT introduce a `.Bytes` compare or a new helper.
- Error helper is `writeError(w http.ResponseWriter, status int, msg string)` (`internal/api/server.go:176`). There is no `writeJSONError`.
- Route registration is unchanged: `mux.Handle("DELETE /v1/jobs/{id}", auth(http.HandlerFunc(s.handleCancelJob)))` (`internal/api/server.go:107`). **Do not touch server.go.**

## Test harness facts (verified)

- Cancel tests are **integration** tests: `internal/api/jobs_cancel_test.go` starts with `//go:build integration` and package `api_test`, using a real Postgres container.
  - **This means Docker Desktop must be running.** Run with `go test -tags integration -p 1 ./internal/api/... -run <Test> -v -timeout 120s`.
  - Per the platform-gated-test-verification memory: integration tests do not run under a plain `make test` on Windows. They must be executed with the `integration` build tag and Docker available, or in a Linux Docker context. Do not claim the new test passes without actually running it under the integration tag.
- Available helpers (all in package `api_test`, already imported by `jobs_cancel_test.go`):
  - `newCancelTestServer(t) *cancelTestEnv` - spins up Postgres, a worker row, a `captureSender` registered in the registry, and the `*api.Server`. Fields: `srv`, `q`, `pool`, `cs` (captureSender), `workerID`.
  - `seedRunningTask(t, env, userID pgtype.UUID) string` - creates a job owned by `userID` plus one task, claims it to the worker (epoch -> 1), advances it to `running`, and returns the job ID string.
  - `createTestUser(t, q, name, email string, isAdmin bool) store.User` - creates a user; pass `true` for admin.
  - `createTestToken(t, q, userID pgtype.UUID) string` - returns a raw bearer token for that user.
  - `uuidString(id pgtype.UUID) string` - formats a `pgtype.UUID` for URL paths.
  - `env.cs.snapshot() []*relayv1.CoordinatorMessage` - records every message sent to the worker; assert length 0 to prove no agent signal.
- For no-side-effect assertions, re-read state directly through the store after the request:
  - `env.q.GetJob(t.Context(), jobUUID)` returns `store.Job` with `.Status` (string).
  - `env.q.ListTasksByJob(t.Context(), jobUUID)` returns `[]store.Task`, each with `.Status` (string).
  - `seedRunningTask` returns the job ID as a string; parse it back with `pgtype.UUID.Scan(jobIDStr)` to call store getters. Use the pattern below in the test code.

---

## Task 1: Failing authz tests (red)

Write three integration tests in the existing cancel test file so they reuse the harness. Two (owner, admin) describe behaviour that already passes; the non-owner test is the new red bar - today a non-owner DELETE succeeds (200, job cancelled, agent signalled), so the non-owner test MUST fail before the fix.

**Files:**
- Test: `internal/api/jobs_cancel_test.go` (append; do not modify existing tests)

- [ ] **Step 1: Write the failing tests**

Append these three tests to `internal/api/jobs_cancel_test.go`. They rely on `pgtype` (already imported) and the existing helpers.

```go
// parseJobUUID converts a job ID string (as returned by seedRunningTask) back
// into a pgtype.UUID for direct store reads in no-side-effect assertions.
func parseJobUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	require.NoError(t, id.Scan(s))
	return id
}

func TestCancelJob_Owner_Succeeds(t *testing.T) {
	env := newCancelTestServer(t)

	owner := createTestUser(t, env.q, "Owner", "cancel-owner@example.com", false)
	token := createTestToken(t, env.q, owner.ID)
	jobID := seedRunningTask(t, env, owner.ID)

	req := httptest.NewRequest(http.MethodDelete, "/v1/jobs/"+jobID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	job, err := env.q.GetJob(t.Context(), parseJobUUID(t, jobID))
	require.NoError(t, err)
	assert.Equal(t, "cancelled", job.Status)
}

func TestCancelJob_Admin_CancelsAnyJob(t *testing.T) {
	env := newCancelTestServer(t)

	owner := createTestUser(t, env.q, "Owner", "cancel-admin-owner@example.com", false)
	admin := createTestUser(t, env.q, "Admin", "cancel-admin@example.com", true)
	adminToken := createTestToken(t, env.q, admin.ID)
	jobID := seedRunningTask(t, env, owner.ID)

	req := httptest.NewRequest(http.MethodDelete, "/v1/jobs/"+jobID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	job, err := env.q.GetJob(t.Context(), parseJobUUID(t, jobID))
	require.NoError(t, err)
	assert.Equal(t, "cancelled", job.Status)
}

func TestCancelJob_NonOwner_404_NoSideEffects(t *testing.T) {
	env := newCancelTestServer(t)

	owner := createTestUser(t, env.q, "Owner", "cancel-victim@example.com", false)
	attacker := createTestUser(t, env.q, "Attacker", "cancel-attacker@example.com", false)
	attackerToken := createTestToken(t, env.q, attacker.ID)
	jobID := seedRunningTask(t, env, owner.ID)
	jobUUID := parseJobUUID(t, jobID)

	req := httptest.NewRequest(http.MethodDelete, "/v1/jobs/"+jobID, nil)
	req.Header.Set("Authorization", "Bearer "+attackerToken)
	rec := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(rec, req)

	// 404, body "job not found".
	require.Equal(t, http.StatusNotFound, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "job not found", body["error"])

	// Security-critical: zero side effects.
	// 1. Job status unchanged (task was advanced to running, job is not cancelled).
	job, err := env.q.GetJob(t.Context(), jobUUID)
	require.NoError(t, err)
	assert.NotEqual(t, "cancelled", job.Status)

	// 2. Underlying task NOT cancelled (still running).
	tasks, err := env.q.ListTasksByJob(t.Context(), jobUUID)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "running", tasks[0].Status)

	// 3. No agent CancelTask signal was sent.
	assert.Empty(t, env.cs.snapshot())
}
```

- [ ] **Step 2: Run the tests to verify the non-owner one FAILS (observe red)**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestCancelJob_NonOwner_404_NoSideEffects -v -timeout 120s`
Expected: **FAIL**. Without the gate, the attacker request returns 200, the job becomes `cancelled`, the task is cancelled, and a `CancelTask` is sent - so the 404 assertion (and the no-side-effect assertions) fail. This proves the test exercises the hole.

Also confirm the two positive tests already pass:
Run: `go test -tags integration -p 1 ./internal/api/... -run 'TestCancelJob_Owner_Succeeds|TestCancelJob_Admin_CancelsAnyJob' -v -timeout 120s`
Expected: PASS (these describe current allowed behaviour).

- [ ] **Step 3: Commit the failing test**

```bash
git add internal/api/jobs_cancel_test.go
git commit -m "test(api): cover job-cancel owner-or-admin authz (red)"
```

---

## Task 2: Add the owner-or-admin gate (green)

**Files:**
- Modify: `internal/api/jobs.go` (inside `handleCancelJob`, between the `GetJob` error handling that ends at line 703 and the terminal-state check at line 704)

- [ ] **Step 1: Insert the gate**

In `internal/api/jobs.go`, immediately after the `GetJob` load + error block (the block that ends with the closing `}` and `return` at line 703) and **before** `if job.Status == "cancelled" || job.Status == "done" {` (line 704), insert:

```go
	// Owner-or-admin gate. A non-owner non-admin caller gets 404 (existence
	// hidden), matching ownedScheduledJob. Returning here rolls back the open
	// tx, so no task is cancelled and no agent signal is sent.
	u, ok := UserFromCtx(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !u.IsAdmin && job.SubmittedBy != u.ID {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
```

Do NOT change anything else in the handler: no edits to `CancelJobTasks`, `UpdateJobStatus`, the epoch logic, the agent send loop, or the commit. `ctx` is already in scope (`ctx := r.Context()` at line 678).

- [ ] **Step 2: Run the non-owner test to verify it PASSES**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestCancelJob_NonOwner_404_NoSideEffects -v -timeout 120s`
Expected: PASS - 404 returned, job status not `cancelled`, task still `running`, no `CancelTask` sent.

- [ ] **Step 3: Run the full cancel test suite to confirm no regression**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestCancelJob -v -timeout 120s`
Expected: PASS for all `TestCancelJob_*` tests (owner, admin, default force, force true, query-param parsing). The existing force/parsing tests use owner tokens via `seedRunningTask(..., user.ID)`, so they stay green under the gate.

- [ ] **Step 4: Commit the fix**

```bash
git add internal/api/jobs.go
git commit -m "fix(api): gate job cancel on owner-or-admin (404 for non-owners)"
```

---

## Task 3: Final verification

**Files:** none modified (verification only)

- [ ] **Step 1: Build**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: no findings.

- [ ] **Step 3: Unit test suite (no Docker)**

Run: `go test ./...`
Expected: PASS. This excludes integration-tagged tests; it confirms the source edit did not break compilation of the non-integration build.

- [ ] **Step 4: Integration cancel tests (Docker required)**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestCancelJob -v -timeout 120s`
Expected: PASS. Per the platform-gated-test-verification memory, this step is mandatory before declaring done; do not skip it. Requires Docker Desktop running.

- [ ] **Step 5: Final commit (only if anything was left uncommitted)**

No code changes expected here. If `git status` is clean, skip.

---

## Self-review (against the spec)

- **Spec policy** (owner = `job.SubmittedBy == user.ID`, admin bypass, 404 for non-owner): implemented in Task 2 with the verified `!=` idiom. Covered.
- **Placement before any side effect** (after `GetJob`, before terminal-state check): Task 2 Step 1 specifies exactly that insertion point. Covered.
- **Success criteria 1-4** (non-owner 404, no side effects, owner 200, admin 200): Tasks 1 and 2 assert all four, including the security-critical no-side-effect re-read (job status + task status + empty sender snapshot). Covered.
- **Success criterion 5** (surgical, ~4-8 lines, no other handler/query changed): only `handleCancelJob` and the test file are touched; `server.go` and all queries untouched. Covered.
- **Invariants** (epoch fence, single JSON entry point, bounded sender, identity-checked teardown): untouched - the gate adds no task-status write and no `make generate` step (no `.sql`/`.proto` change). Covered.
- **404-not-403 enumeration choice:** test asserts 404 + `"job not found"`, matching `ownedScheduledJob`. Covered.
- **pgtype.UUID comparison:** named explicitly as `!=` per `scheduled_jobs.go:163`, not a guess. Covered.

## Execution Handoff

Plan complete and saved. Recommended execution: subagent-driven-development (fresh subagent per task, review between tasks). This is an unattended autopilot run, so proceed task-by-task with the two positive tests as the green guardrail and the non-owner test as the red-then-green bar.
