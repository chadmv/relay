# Worker Disable Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an admin-only ability to disable a worker - stopping new task dispatch to it - without revoking its agent token, so it can be re-enabled instantly.

**Architecture:** A new nullable `disabled_at` column on `workers`, orthogonal to the liveness `status` column. The scheduler skips workers with `disabled_at` set. The API coalesces `status` to `"disabled"` in responses. Disable supports a drain mode (default: leave running tasks alone) and a requeue mode (requeue active tasks and cancel them on the agent).

**Tech Stack:** Go, PostgreSQL, sqlc (generated store layer), golang-migrate (embedded migrations), gRPC (agent coordination), stdlib `net/http` + `flag`, testify, testcontainers-go for integration tests.

**Source spec:** `docs/superpowers/specs/2026-05-20-worker-disable-design.md`

---

## Background for the implementer

- **Never edit `internal/store/*.sql.go` or `internal/store/models.go` directly.** They are generated. Edit the `.sql` files under `internal/store/query/` and `internal/store/migrations/`, then run `make generate`.
- Integration tests use the `//go:build integration` tag and need Docker Desktop running. Run them with `-p 1` to avoid container conflicts.
- Unit tests (no Docker) run with plain `go test` / `make test`.
- `:execrows` sqlc queries generate a Go func returning `(int64, error)` - the affected-row count.
- A `:many` query returning a single column `id` generates a func returning `([]pgtype.UUID, error)`.
- The `store.Worker` struct already has fields like `Status string` and `DisconnectedAt pgtype.Timestamptz`. The new `disabled_at` column will generate `DisabledAt pgtype.Timestamptz`.
- The API server has `s.q` (`*store.Queries`), `s.pool` (`*pgxpool.Pool`), `s.registry` (`*worker.Registry`). It has **no** dispatcher reference - wake the dispatcher with the `NotifyTaskSubmitted` query (`SELECT pg_notify('relay_task_submitted','')`).

---

## File Structure

**Created:**
- `internal/store/migrations/000012_workers_disabled_at.up.sql` - add column
- `internal/store/migrations/000012_workers_disabled_at.down.sql` - drop column
- `internal/store/workers_disabled_test.go` - store-layer integration tests
- `internal/api/workers_disable_test.go` - API integration tests
- `internal/cli/workers_disable_test.go` - CLI unit tests

**Modified:**
- `internal/store/query/workers.sql` - `DisableWorker`, `EnableWorker` queries; `disabled_at` in `UpsertWorkerByHostname` RETURNING list
- `internal/store/query/tasks.sql` - `RequeueWorkerTasksWithEpoch` query
- `internal/scheduler/dispatch.go` - skip disabled workers in `selectWorker`
- `internal/scheduler/select_worker_test.go` - unit test for the exclusion
- `internal/api/workers.go` - `workerResponse.DisabledAt`, `toWorkerResponse` coalescing, `handleDisableWorker`, `handleEnableWorker`
- `internal/api/server.go` - two new routes
- `internal/cli/workers.go` - `disable`/`enable` subcommands
- `README.md` - REST API table, CLI reference, revoke-vs-disable note

**Regenerated (do not hand-edit):** `internal/store/*.sql.go`, `internal/store/models.go`.

---

## Task 1: Migration + DisableWorker/EnableWorker store queries

**Files:**
- Create: `internal/store/migrations/000012_workers_disabled_at.up.sql`
- Create: `internal/store/migrations/000012_workers_disabled_at.down.sql`
- Modify: `internal/store/query/workers.sql`
- Create: `internal/store/workers_disabled_test.go`
- Regenerated: `internal/store/workers.sql.go`, `internal/store/models.go`

- [ ] **Step 1: Write the migration files**

Create `internal/store/migrations/000012_workers_disabled_at.up.sql`:

```sql
ALTER TABLE workers ADD COLUMN disabled_at TIMESTAMPTZ NULL;
```

Create `internal/store/migrations/000012_workers_disabled_at.down.sql`:

```sql
ALTER TABLE workers DROP COLUMN disabled_at;
```

- [ ] **Step 2: Add the sqlc queries**

In `internal/store/query/workers.sql`, append these two queries at the end of the file:

```sql
-- name: DisableWorker :execrows
-- Marks a worker disabled. Idempotent: the disabled_at IS NULL guard means a
-- second call affects zero rows and does not re-stamp the timestamp.
UPDATE workers SET disabled_at = NOW() WHERE id = $1 AND disabled_at IS NULL;

-- name: EnableWorker :execrows
-- Clears the disabled flag. Idempotent: affects zero rows if already enabled.
UPDATE workers SET disabled_at = NULL WHERE id = $1;
```

Still in `internal/store/query/workers.sql`, find the `UpsertWorkerByHostname` query and append `disabled_at` to its explicit `RETURNING` column list. The line currently reads:

```sql
RETURNING id, name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, max_slots, labels, status, last_seen_at, created_at;
```

Change it to:

```sql
RETURNING id, name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, max_slots, labels, status, last_seen_at, created_at, disabled_at;
```

- [ ] **Step 3: Regenerate the store layer**

Run: `make generate`
Expected: completes with no errors. `git status` shows modified `internal/store/workers.sql.go` and `internal/store/models.go`. `models.go` now has `DisabledAt pgtype.Timestamptz` on the `Worker` struct; `workers.sql.go` has `DisableWorker` and `EnableWorker` funcs returning `(int64, error)`.

- [ ] **Step 4: Write the failing store test**

Create `internal/store/workers_disabled_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/stretchr/testify/require"
)

func TestWorkerDisableEnable_RoundTrip(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	w := newTestWorker(t, q)
	require.False(t, w.DisabledAt.Valid, "a new worker must start enabled")

	n, err := q.DisableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "first disable must affect one row")

	got, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.True(t, got.DisabledAt.Valid, "worker must be disabled")

	// Idempotent: a second disable affects zero rows and does not re-stamp.
	n, err = q.DisableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), n, "second disable must affect zero rows")

	n, err = q.EnableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "enable must affect one row")

	got, err = q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.False(t, got.DisabledAt.Valid, "worker must be enabled again")

	// Idempotent: a second enable affects zero rows.
	n, err = q.EnableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), n, "second enable must affect zero rows")
}
```

Note: `newTestQueries` and `newTestWorker` already exist in `internal/store/testhelper_test.go`.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/store/... -run TestWorkerDisableEnable -v -timeout 120s`
Expected: PASS. (The test exercises freshly generated code, so it should pass directly once Step 3 succeeded. If it fails to compile with "DisabledAt undefined", Step 3 did not regenerate correctly - re-run `make generate`.)

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/000012_workers_disabled_at.up.sql internal/store/migrations/000012_workers_disabled_at.down.sql internal/store/query/workers.sql internal/store/workers.sql.go internal/store/models.go internal/store/workers_disabled_test.go
git commit -m "feat: add disabled_at column and worker disable/enable queries"
```

---

## Task 2: RequeueWorkerTasksWithEpoch store query

**Files:**
- Modify: `internal/store/query/tasks.sql`
- Modify: `internal/store/workers_disabled_test.go`
- Regenerated: `internal/store/tasks.sql.go`

- [ ] **Step 1: Add the sqlc query**

In `internal/store/query/tasks.sql`, append this query at the end of the file:

```sql
-- name: RequeueWorkerTasksWithEpoch :many
-- Re-queue dispatched/running tasks for a worker that is being disabled.
-- Unlike RequeueWorkerTasks, this bumps assignment_epoch so a stale status
-- update from the still-connected agent (whose subprocess we are about to
-- cancel) is rejected by the epoch fence. Returns the affected task ids.
UPDATE tasks
SET status = 'pending',
    worker_id = NULL,
    started_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE worker_id = $1 AND status IN ('dispatched', 'running')
RETURNING id;
```

- [ ] **Step 2: Regenerate the store layer**

Run: `make generate`
Expected: completes with no errors. `internal/store/tasks.sql.go` now has a `RequeueWorkerTasksWithEpoch` func returning `([]pgtype.UUID, error)`.

- [ ] **Step 3: Write the failing store test**

Append to `internal/store/workers_disabled_test.go` (and add `"relay/internal/store"` is already imported):

```go
func TestRequeueWorkerTasksWithEpoch_BumpsEpochAndFencesStaleUpdates(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name:        "requeue-job",
		Priority:    "normal",
		SubmittedBy: user.ID,
		Labels:      []byte("{}"),
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID:    job.ID,
		Name:     "requeue-task",
		Commands: []byte(`[["echo","hi"]]`),
		Env:      []byte("{}"),
		Requires: []byte("{}"),
		Retries:  0,
	})
	require.NoError(t, err)

	// Claim the task onto the worker: status -> 'dispatched', epoch 0 -> 1.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID:       task.ID,
		WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	ids, err := q.RequeueWorkerTasksWithEpoch(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, ids, 1, "the one active task must be requeued")
	require.Equal(t, task.ID, ids[0])

	got, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", got.Status, "task must be back to pending")
	require.False(t, got.WorkerID.Valid, "worker_id must be cleared")
	require.Equal(t, int32(2), got.AssignmentEpoch, "epoch must be bumped to 2")

	// A stale status update at the old epoch (1) must be rejected.
	_, err = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
		ID:     task.ID,
		Status: "done",
		Epoch:  1,
	})
	require.Error(t, err, "stale update at epoch 1 must be rejected after requeue")
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/store/... -run TestRequeueWorkerTasksWithEpoch -v -timeout 120s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go internal/store/workers_disabled_test.go
git commit -m "feat: add RequeueWorkerTasksWithEpoch query for worker disable"
```

---

## Task 3: Scheduler skips disabled workers

**Files:**
- Modify: `internal/scheduler/dispatch.go` (the `selectWorker` method, around line 162)
- Modify: `internal/scheduler/select_worker_test.go`

- [ ] **Step 1: Write the failing unit test**

In `internal/scheduler/select_worker_test.go`, add `"time"` to the import block (the block currently imports `"testing"`, `"relay/internal/store"`, `pgtype`, `assert`, `require`). The import block becomes:

```go
import (
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

Then append this test at the end of the file:

```go
func TestSelectWorker_DisabledWorkerIsNotEligible(t *testing.T) {
	d := newDispatcherForTest()
	task := baseTask()
	wk := baseWorker(60, "online")
	wk.DisabledAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	workers := []store.Worker{wk}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	assert.Nil(t, got, "a disabled worker must NOT be selected for dispatch")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/scheduler/... -run TestSelectWorker_DisabledWorkerIsNotEligible -v -timeout 30s`
Expected: FAIL - the test returns a non-nil worker because `selectWorker` does not yet check `DisabledAt`.

- [ ] **Step 3: Add the exclusion to selectWorker**

In `internal/scheduler/dispatch.go`, inside the `for i := range workers` loop of `selectWorker`, find this existing block:

```go
		// A "stale" worker is still connected and able to run tasks; the
		// status only signals missing telemetry, so it stays dispatch-eligible.
		// Only non-connected statuses (e.g. "offline") are excluded.
		if w.Status != "online" && w.Status != "stale" {
			continue
		}
```

Immediately after it, add:

```go
		// A disabled worker keeps its connection and liveness status but must
		// not receive new task dispatches.
		if w.DisabledAt.Valid {
			continue
		}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/scheduler/... -run TestSelectWorker -v -timeout 30s`
Expected: PASS for all `TestSelectWorker_*` tests, including the new one and the pre-existing eligibility tests.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/dispatch.go internal/scheduler/select_worker_test.go
git commit -m "feat: exclude disabled workers from scheduler dispatch"
```

---

## Task 4: API response coalescing

**Files:**
- Modify: `internal/api/workers.go` (the `workerResponse` struct and `toWorkerResponse` func)
- Create: `internal/api/workers_response_test.go`

This task makes `toWorkerResponse` report `status: "disabled"` and expose a `disabled_at` field. It is unit-testable because `toWorkerResponse` is a pure function of a `store.Worker`.

- [ ] **Step 1: Write the failing unit test**

Create `internal/api/workers_response_test.go`:

```go
package api

import (
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToWorkerResponse_EnabledWorkerKeepsLiveStatus(t *testing.T) {
	w := store.Worker{
		Status: "online",
		Labels: []byte(`{}`),
	}
	resp := toWorkerResponse(w)
	assert.Equal(t, "online", resp.Status)
	assert.Nil(t, resp.DisabledAt, "disabled_at must be nil for an enabled worker")
}

func TestToWorkerResponse_DisabledWorkerCoalescesStatus(t *testing.T) {
	disabledAt := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	w := store.Worker{
		Status:     "online",
		Labels:     []byte(`{}`),
		DisabledAt: pgtype.Timestamptz{Time: disabledAt, Valid: true},
	}
	resp := toWorkerResponse(w)
	assert.Equal(t, "disabled", resp.Status, "status must coalesce to 'disabled'")
	require.NotNil(t, resp.DisabledAt)
	assert.True(t, resp.DisabledAt.Equal(disabledAt))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/... -run TestToWorkerResponse -v -timeout 30s`
Expected: FAIL to compile - `resp.DisabledAt` is an unknown field on `workerResponse`.

- [ ] **Step 3: Add the field and coalescing logic**

In `internal/api/workers.go`, add a `DisabledAt` field to the `workerResponse` struct. The struct currently ends with:

```go
	LastSeenAt   *time.Time      `json:"last_seen_at,omitempty"`
	LastSampleAt *time.Time      `json:"last_sample_at,omitempty"`
}
```

Change it to:

```go
	LastSeenAt   *time.Time      `json:"last_seen_at,omitempty"`
	LastSampleAt *time.Time      `json:"last_sample_at,omitempty"`
	DisabledAt   *time.Time      `json:"disabled_at,omitempty"`
}
```

Then update `toWorkerResponse`. It currently reads:

```go
func toWorkerResponse(w store.Worker) workerResponse {
	var lastSeen *time.Time
	if w.LastSeenAt.Valid {
		t := w.LastSeenAt.Time
		lastSeen = &t
	}
	return workerResponse{
		ID:         uuidStr(w.ID),
		Name:       w.Name,
		Hostname:   w.Hostname,
		CpuCores:   w.CpuCores,
		RamGb:      w.RamGb,
		GpuCount:   w.GpuCount,
		GpuModel:   w.GpuModel,
		Os:         w.Os,
		MaxSlots:   w.MaxSlots,
		Labels:     rawJSON(w.Labels),
		Status:     w.Status,
		LastSeenAt: lastSeen,
	}
}
```

Replace it with:

```go
func toWorkerResponse(w store.Worker) workerResponse {
	var lastSeen *time.Time
	if w.LastSeenAt.Valid {
		t := w.LastSeenAt.Time
		lastSeen = &t
	}
	// A disabled worker keeps its live liveness status internally, but the API
	// reports "disabled" so existing consumers that read only `status` treat it
	// as unavailable. `disabled_at` is also exposed so both states are visible.
	status := w.Status
	var disabledAt *time.Time
	if w.DisabledAt.Valid {
		t := w.DisabledAt.Time
		disabledAt = &t
		status = "disabled"
	}
	return workerResponse{
		ID:         uuidStr(w.ID),
		Name:       w.Name,
		Hostname:   w.Hostname,
		CpuCores:   w.CpuCores,
		RamGb:      w.RamGb,
		GpuCount:   w.GpuCount,
		GpuModel:   w.GpuModel,
		Os:         w.Os,
		MaxSlots:   w.MaxSlots,
		Labels:     rawJSON(w.Labels),
		Status:     status,
		LastSeenAt: lastSeen,
		DisabledAt: disabledAt,
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/api/... -run TestToWorkerResponse -v -timeout 30s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/workers.go internal/api/workers_response_test.go
git commit -m "feat: coalesce worker status to 'disabled' in API responses"
```

---

## Task 5: Disable/enable HTTP handlers and routes

**Files:**
- Modify: `internal/api/workers.go` (add imports, `disableWorkerResponse` type, two handlers)
- Modify: `internal/api/server.go` (two routes)

This task adds the handlers and wires the routes. It is verified by the integration tests in Task 6; this task's own check is that the code compiles and the unit tests still pass.

- [ ] **Step 1: Add imports to workers.go**

`internal/api/workers.go` currently imports:

```go
import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)
```

Change it to add `strconv` and the protobuf package:

```go
import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)
```

- [ ] **Step 2: Add the disableWorkerResponse type**

In `internal/api/workers.go`, immediately after the `workerResponse` struct definition, add:

```go
// disableWorkerResponse is the body returned by the disable endpoint. It embeds
// workerResponse (its fields flatten into the JSON object) and adds the count of
// tasks that were requeued - always 0 in drain mode.
type disableWorkerResponse struct {
	workerResponse
	RequeuedTasks int `json:"requeued_tasks"`
}
```

- [ ] **Step 3: Add the handlers**

Append both handlers to the end of `internal/api/workers.go`:

```go
func (s *Server) handleDisableWorker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}
	requeue, _ := strconv.ParseBool(r.URL.Query().Get("requeue"))

	current, err := s.q.GetWorker(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	// Already disabled: no-op. Do not re-stamp disabled_at or re-cancel tasks.
	if current.DisabledAt.Valid {
		writeJSON(w, http.StatusOK, disableWorkerResponse{
			workerResponse: toWorkerResponse(current),
		})
		return
	}

	var requeuedIDs []pgtype.UUID
	if requeue {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		defer tx.Rollback(ctx)
		q := s.q.WithTx(tx)

		// Set disabled_at first so a dispatcher woken by NotifyTaskSubmitted
		// already sees the worker as ineligible and won't re-dispatch to it.
		if _, err := q.DisableWorker(ctx, id); err != nil {
			writeError(w, http.StatusInternalServerError, "disable worker failed")
			return
		}
		requeuedIDs, err = q.RequeueWorkerTasksWithEpoch(ctx, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "requeue tasks failed")
			return
		}
		if err := q.NotifyTaskSubmitted(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}

		// Tell the still-connected agent to kill the now-orphaned subprocesses.
		// Best-effort: a failed send just means the agent already lost the task.
		for _, tid := range requeuedIDs {
			_ = s.registry.Send(uuidStr(id), &relayv1.CoordinatorMessage{
				Payload: &relayv1.CoordinatorMessage_CancelTask{
					CancelTask: &relayv1.CancelTask{
						TaskId: uuidStr(tid),
						Force:  false,
					},
				},
			})
		}
	} else {
		if _, err := s.q.DisableWorker(ctx, id); err != nil {
			writeError(w, http.StatusInternalServerError, "disable worker failed")
			return
		}
	}

	updated, err := s.q.GetWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, disableWorkerResponse{
		workerResponse: toWorkerResponse(updated),
		RequeuedTasks:  len(requeuedIDs),
	})
}

func (s *Server) handleEnableWorker(w http.ResponseWriter, r *http.Request) {
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

	if _, err := s.q.EnableWorker(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "enable worker failed")
		return
	}
	// Wake the dispatcher so the re-enabled worker can pick up pending tasks
	// immediately rather than waiting for the next ticker cycle.
	if err := s.q.NotifyTaskSubmitted(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	updated, err := s.q.GetWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, toWorkerResponse(updated))
}
```

- [ ] **Step 4: Register the routes**

In `internal/api/server.go`, find the worker routes block (near line 124, the `DELETE /v1/workers/{id}/token` line). Immediately after that line, add:

```go
	mux.Handle("POST /v1/workers/{id}/disable", auth(admin(http.HandlerFunc(s.handleDisableWorker))))
	mux.Handle("POST /v1/workers/{id}/enable", auth(admin(http.HandlerFunc(s.handleEnableWorker))))
```

- [ ] **Step 5: Verify the package compiles and unit tests pass**

Run: `go build ./... && go test ./internal/api/... -run TestToWorkerResponse -v -timeout 30s`
Expected: build succeeds; `TestToWorkerResponse_*` PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/workers.go internal/api/server.go
git commit -m "feat: add worker disable/enable HTTP endpoints"
```

---

## Task 6: API integration tests for disable/enable

**Files:**
- Create: `internal/api/workers_disable_test.go`

These tests use the `//go:build integration` tag and need Docker. They reuse existing helpers from the `api_test` package: `newTestServer`, `createTestUser`, `createTestToken`, `fmtUUID` (see `internal/api/agent_enrollments_test.go`); and `newCancelTestServer`, `seedRunningTask`, plus the `captureSender` type (see `internal/api/jobs_cancel_test.go`).

- [ ] **Step 1: Write the integration tests**

Create `internal/api/workers_disable_test.go`:

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

// doRequest is a small helper: issues an authenticated request and returns the recorder.
func doDisableReq(t *testing.T, srv interface {
	Handler() http.Handler
}, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestDisableWorker_AdminOnly(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Dis User", "dis-user@example.com", false)
	userToken := createTestToken(t, q, user.ID)
	admin := createTestUser(t, q, "Dis Admin", "dis-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	row, err := q.UpsertWorkerByHostname(t.Context(), store.UpsertWorkerByHostnameParams{
		Name: "dw", Hostname: "dw", CpuCores: 4, RamGb: 16,
		GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)
	workerID := fmtUUID(row.ID)

	// Non-admin -> 403.
	rec := doDisableReq(t, srv, "POST", "/v1/workers/"+workerID+"/disable", userToken)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Admin -> 200, status coalesced to "disabled".
	rec = doDisableReq(t, srv, "POST", "/v1/workers/"+workerID+"/disable", adminToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "disabled", body["status"])
	assert.NotNil(t, body["disabled_at"])
	assert.Equal(t, float64(0), body["requeued_tasks"], "drain mode requeues nothing")

	// Idempotent: second disable also 200, still disabled.
	rec = doDisableReq(t, srv, "POST", "/v1/workers/"+workerID+"/disable", adminToken)
	require.Equal(t, http.StatusOK, rec.Code)

	// Enable -> 200, status reverts, disabled_at gone.
	rec = doDisableReq(t, srv, "POST", "/v1/workers/"+workerID+"/enable", adminToken)
	require.Equal(t, http.StatusOK, rec.Code)
	body = map[string]any{}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.NotEqual(t, "disabled", body["status"])
	_, hasDisabledAt := body["disabled_at"]
	assert.False(t, hasDisabledAt, "disabled_at must be omitted once enabled")

	// Idempotent enable: second enable also 200.
	rec = doDisableReq(t, srv, "POST", "/v1/workers/"+workerID+"/enable", adminToken)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestDisableWorker_UnknownWorkerIs404(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Dis Admin 404", "dis-admin-404@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	const missing = "/v1/workers/00000000-0000-0000-0000-000000000000/disable"
	rec := doDisableReq(t, srv, "POST", missing, adminToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = doDisableReq(t, srv, "POST",
		"/v1/workers/00000000-0000-0000-0000-000000000000/enable", adminToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDisableWorker_DrainModeLeavesRunningTaskAlone(t *testing.T) {
	env := newCancelTestServer(t)
	user := createTestUser(t, env.q, "Drain User", "drain-user@example.com", true)
	userToken := createTestToken(t, env.q, user.ID)
	jobID := seedRunningTask(t, env, user.ID)

	// Disable without ?requeue: the running task must stay running.
	rec := doDisableReq(t, env.srv, "POST",
		"/v1/workers/"+uuidString(env.workerID)+"/disable", userToken)
	require.Equal(t, http.StatusOK, rec.Code)

	tasks, err := env.q.ListTasksByJob(t.Context(), mustParseUUID(t, jobID))
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "running", tasks[0].Status, "drain mode must not touch the running task")

	// No CancelTask should have been sent to the agent.
	for _, m := range env.cs.snapshot() {
		assert.Nil(t, m.GetCancelTask(), "drain mode must not send CancelTask")
	}
}

func TestDisableWorker_RequeueModeRequeuesAndCancels(t *testing.T) {
	env := newCancelTestServer(t)
	user := createTestUser(t, env.q, "Requeue User", "requeue-user@example.com", true)
	userToken := createTestToken(t, env.q, user.ID)
	jobID := seedRunningTask(t, env, user.ID)

	rec := doDisableReq(t, env.srv, "POST",
		"/v1/workers/"+uuidString(env.workerID)+"/disable?requeue=true", userToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, float64(1), body["requeued_tasks"])

	// Task is back to pending, unassigned.
	tasks, err := env.q.ListTasksByJob(t.Context(), mustParseUUID(t, jobID))
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "pending", tasks[0].Status)
	assert.False(t, tasks[0].WorkerID.Valid)

	// A CancelTask was sent to the agent for that task.
	var sawCancel bool
	for _, m := range env.cs.snapshot() {
		if c := m.GetCancelTask(); c != nil && c.TaskId == uuidString(tasks[0].ID) {
			sawCancel = true
		}
	}
	assert.True(t, sawCancel, "requeue mode must send CancelTask for the requeued task")
}
```

Note on helpers used:
- `uuidString` (UUID -> string) is the helper defined in the `api_test` package and used by `jobs_cancel_test.go`. This test file is package `api_test`, so it uses `uuidString`, not the unexported `api`-package `uuidStr`.
- `mustParseUUID` does not exist yet - add the small helper below to this file.

Add this helper to `internal/api/workers_disable_test.go`:

```go
func mustParseUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	require.NoError(t, u.Scan(s))
	return u
}
```

and add `"github.com/jackc/pgx/v5/pgtype"` to the import block.

- [ ] **Step 2: Run the tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/api/... -run "TestDisableWorker" -v -timeout 300s`
Expected: all four `TestDisableWorker_*` tests PASS.

If a test fails to compile because `uuidString` or `seedRunningTask` is not found, confirm the helper names by reading `internal/api/jobs_cancel_test.go` and adjust the calls to match the actual exported-in-test helper names.

- [ ] **Step 3: Commit**

```bash
git add internal/api/workers_disable_test.go
git commit -m "test: add integration tests for worker disable/enable endpoints"
```

---

## Task 7: CLI disable/enable subcommands

**Files:**
- Modify: `internal/cli/workers.go`
- Create: `internal/cli/workers_disable_test.go`

- [ ] **Step 1: Write the failing CLI tests**

Create `internal/cli/workers_disable_test.go`:

```go
// internal/cli/workers_disable_test.go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"relay/internal/relayclient"
)

func TestWorkersDisable_Drain(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000011"
	called := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/workers/"+workerID+"/disable", r.URL.Path)
		require.Equal(t, "", r.URL.Query().Get("requeue"), "drain mode sends no requeue param")
		called = true
		json.NewEncoder(w).Encode(map[string]any{"status": "disabled", "requeued_tasks": 0})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"disable", workerID}, &out)
	require.NoError(t, err)
	require.True(t, called)
	require.Contains(t, out.String(), "disabled.")
}

func TestWorkersDisable_Requeue(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000012"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/workers/"+workerID+"/disable", r.URL.Path)
		require.Equal(t, "true", r.URL.Query().Get("requeue"))
		json.NewEncoder(w).Encode(map[string]any{"status": "disabled", "requeued_tasks": 3})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"disable", "--requeue", workerID}, &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), "3 task(s) requeued")
}

func TestWorkersEnable_ByHostname(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000013"
	enabled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/workers":
			json.NewEncoder(w).Encode(relayclient.PageEnvelope[workerResp]{
				Items: []workerResp{{ID: workerID, Hostname: "render-node-9", Status: "disabled"}},
				Total: 1,
			})
		case r.Method == "POST" && r.URL.Path == "/v1/workers/"+workerID+"/enable":
			enabled = true
			json.NewEncoder(w).Encode(map[string]any{"status": "online"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"enable", "render-node-9"}, &out)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Contains(t, out.String(), "enabled.")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/... -run "TestWorkersDisable|TestWorkersEnable" -v -timeout 30s`
Expected: FAIL - `doWorkers` returns `unknown workers subcommand: disable` / `enable`.

- [ ] **Step 3: Implement the subcommands**

In `internal/cli/workers.go`:

First, update the doc comment and `Usage` string. The comment on line 30 and the `WorkersCommand` func become:

```go
// WorkersCommand returns the relay workers Command.
// Subcommands: list, get, disable, enable, revoke, workspaces, evict-workspace
func WorkersCommand() Command {
	return Command{
		Name:  "workers",
		Usage: "workers <list|get|disable|enable|revoke|workspaces|evict-workspace> [args]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doWorkers(ctx, cfg, args, os.Stdout)
		},
	}
}
```

Update the usage error string in `doWorkers`:

```go
	if len(args) == 0 {
		return fmt.Errorf("usage: relay workers <list|get|disable|enable|revoke|workspaces|evict-workspace>")
	}
```

Add two cases to the `switch args[0]` block in `doWorkers`, immediately before `case "revoke":`:

```go
	case "disable":
		return doWorkersDisable(ctx, c, args[1:], w)
	case "enable":
		return doWorkersEnable(ctx, c, args[1:], w)
```

Then append the two subcommand functions to the end of `internal/cli/workers.go`:

```go
// disableResp decodes the relevant fields of the disable endpoint response.
type disableResp struct {
	RequeuedTasks int `json:"requeued_tasks"`
}

func doWorkersDisable(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers disable", flag.ContinueOnError)
	requeue := fs.Bool("requeue", false, "requeue active tasks immediately instead of draining")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay workers disable [--requeue] <worker-id-or-hostname>")
	}
	id, err := resolveWorkerID(ctx, c, fs.Arg(0))
	if err != nil {
		return err
	}
	path := "/v1/workers/" + id + "/disable"
	if *requeue {
		path += "?requeue=true"
	}
	var resp disableResp
	if err := c.Do(ctx, "POST", path, nil, &resp); err != nil {
		return fmt.Errorf("disable worker: %w", err)
	}
	if *requeue {
		fmt.Fprintf(w, "disabled; %d task(s) requeued.\n", resp.RequeuedTasks)
	} else {
		fmt.Fprintln(w, "disabled.")
	}
	return nil
}

func doWorkersEnable(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers enable", flag.ContinueOnError)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay workers enable <worker-id-or-hostname>")
	}
	id, err := resolveWorkerID(ctx, c, fs.Arg(0))
	if err != nil {
		return err
	}
	if err := c.Do(ctx, "POST", "/v1/workers/"+id+"/enable", nil, nil); err != nil {
		return fmt.Errorf("enable worker: %w", err)
	}
	fmt.Fprintln(w, "enabled.")
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/... -run "TestWorkersDisable|TestWorkersEnable|TestWorkersRevoke" -v -timeout 30s`
Expected: PASS for all `TestWorkersDisable_*`, `TestWorkersEnable_*`, and the pre-existing `TestWorkersRevoke_*` tests (the revoke tests confirm the switch-statement change did not regress them).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/workers.go internal/cli/workers_disable_test.go
git commit -m "feat: add relay workers disable/enable CLI subcommands"
```

---

## Task 8: Documentation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Locate the worker REST API rows**

Open `README.md` and find the REST API table rows for workers (search for `/v1/workers/{id}/token` - the revoke row). Add two rows immediately after it:

```markdown
| `POST` | `/v1/workers/{id}/disable` | Disable a worker so the scheduler assigns it no new tasks. Pass `?requeue=true` to requeue and cancel its active tasks immediately; the default drains (running tasks finish). The token and connection are kept. Admin only. |
| `POST` | `/v1/workers/{id}/enable` | Re-enable a disabled worker. Admin only. |
```

- [ ] **Step 2: Locate the CLI reference for `relay workers`**

Find the `relay workers` CLI reference section (search for `relay workers revoke`). Add entries for the two new subcommands, matching the surrounding format:

```markdown
#### `relay workers disable <id-or-hostname> [--requeue]`

Disable a worker (admin only). A disabled worker keeps its agent token and gRPC
connection but receives no new task dispatches. By default running tasks are
left to finish (drain); pass `--requeue` to requeue the worker's active tasks
immediately and cancel their subprocesses on the agent. The positional argument
may be a worker UUID or a hostname.

#### `relay workers enable <id-or-hostname>`

Re-enable a disabled worker (admin only). Takes effect immediately. The
positional argument may be a worker UUID or a hostname.
```

- [ ] **Step 3: Add a revoke-vs-disable note**

Find the agent enrollment / revoke documentation (search for the sentence "If the token is revoked by an admin"). Add a short clarifying paragraph nearby:

```markdown
**Disable vs revoke.** *Disabling* a worker (`relay workers disable`) takes it
out of the scheduler's rotation while keeping its token and connection, so it
can be re-enabled instantly with `relay workers enable`. *Revoking* a worker
(`relay workers revoke`) destroys its agent token and forces a fresh enrollment
before it can rejoin. The two are independent: a worker can be both disabled and
revoked, and re-enrollment clears the revoked state but leaves a disabled worker
disabled.
```

- [ ] **Step 4: Verify the build and full unit test suite**

Run: `make build && make test`
Expected: build produces the three binaries; all unit tests pass.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: document worker disable/enable endpoints and CLI"
```

---

## Final Verification

After all tasks, run the complete relevant suites:

- [ ] Unit tests: `make test` - all pass.
- [ ] Scheduler unit tests: `go test ./internal/scheduler/... -v -timeout 60s` - all pass.
- [ ] Store integration tests: `go test -tags integration -p 1 ./internal/store/... -run "TestWorkerDisableEnable|TestRequeueWorkerTasksWithEpoch" -v -timeout 180s` - all pass.
- [ ] API integration tests: `go test -tags integration -p 1 ./internal/api/... -run "TestDisableWorker" -v -timeout 300s` - all pass.
- [ ] CLI tests: `go test ./internal/cli/... -run "TestWorkers" -v -timeout 60s` - all pass.
- [ ] `make build` succeeds.

---

## Self-Review Notes

**Spec coverage check (against `2026-05-20-worker-disable-design.md`):**
- Data model (migration, `DisableWorker`/`EnableWorker`, `UpsertWorkerByHostname` RETURNING fix) -> Task 1.
- `RequeueWorkerTasksWithEpoch` -> Task 2.
- Dispatch exclusion -> Task 3.
- `workerResponse` coalescing + `DisabledAt` field -> Task 4.
- `POST .../disable` (drain + requeue), `POST .../enable`, routes, `NotifyTaskSubmitted`, `CancelTask` sends, epoch fence -> Task 5.
- Error handling: 404 unknown, 400 invalid id, 403 non-admin, idempotent double-disable/enable -> Tasks 5 (handlers) + 6 (tests).
- CLI `disable`/`enable` with `--requeue`, hostname resolution -> Task 7.
- README REST table, CLI reference, revoke-vs-disable note -> Task 8.
- Liveness sweeper unaffected: no code change needed (it filters on `status IN ('online','stale')`, untouched) - correctly results in no task.
- Disconnect-while-disabled: no code change needed (`disabled_at` is a separate column never touched by `UpdateWorkerStatus`) - correctly results in no task.

**Type consistency:** `DisabledAt` is `pgtype.Timestamptz` on `store.Worker` and `*time.Time` on `workerResponse`; `DisableWorker`/`EnableWorker` return `(int64, error)`; `RequeueWorkerTasksWithEpoch` returns `([]pgtype.UUID, error)`; `AssignmentEpoch` is `int32`. Handler names `handleDisableWorker`/`handleEnableWorker` match between Task 5 and the routes. CLI funcs `doWorkersDisable`/`doWorkersEnable` match the switch cases.
