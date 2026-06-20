# Worker Connection-Epoch Fence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the residual `finishRegister` stale-teardown race by adding a monotonic `workers.connection_epoch` and fencing every teardown/grace write on it, so a stale connection's offline/requeue writes no-op the instant a fresher connection registers.

**Architecture:** Mirror the existing task-level `assignment_epoch` fence at the worker-connection level. A new `RegisterWorkerConnection` query bumps and returns the epoch atomically with coming online inside `finishRegister`. The owned epoch rides on the per-connection `*workerSender` (set once, immutable) and is threaded through `teardownConnection`, `markWorkerOffline`, `requeueWorkerTasks`, and the `GraceRegistry`. Two new epoch-fenced sibling queries (`MarkWorkerOfflineIfEpoch`, `RequeueWorkerTasksIfEpoch`) gate the offline flip and the requeue. The unconditional `UpdateWorkerStatus`/`RequeueWorkerTasks` are left intact for the liveness sweeper and admin-disable paths.

**Tech Stack:** Go, sqlc-generated store, golang-migrate, pgx, testcontainers-go (integration), testify.

## Slice independence

Backend-only. No frontend slice exists in this plan (no REST/SSE/CLI surface change). There is no Phase 3 frontend/backend parallelism to declare. The tasks below are mostly sequential because later wiring depends on the regenerated sqlc types.

## Spec

`docs/superpowers/specs/2026-06-20-finishregister-gap-connection-epoch-design.md`

## Ordering risks (read before starting)

- **Migration must land before `make generate`.** sqlc introspects the live schema via the migrations; `RegisterWorkerConnection` / `MarkWorkerOfflineIfEpoch` reference `connection_epoch`, which does not exist until migration `000016` is applied. Task 1 (migration) strictly precedes Task 2 (queries + generate).
- **sqlc CRLF cleanup (per CLAUDE.md).** sqlc emits LF line endings; on this CRLF repo `make generate` rewrites line endings across every generated file. After each generate: run `git diff --ignore-all-space` to confirm the only real change is your new query/column, then revert LF-only hunks with `git checkout -- <file>` on files you did not mean to change. Stage only the genuine content diffs.
- **Never hand-edit `*.sql.go` or `models.go`.** They are regenerated. `connection_epoch` lands in `models.go` via the migration + generate, not by editing.
- **Generated types gate the wiring.** Tasks 3-7 use `store.RegisterWorkerConnectionParams`, `store.MarkWorkerOfflineIfEpochParams`, `store.RequeueWorkerTasksIfEpochParams`, and the `ConnectionEpoch` field on `ListGraceCandidatesRow`. These only exist after Task 2's generate. Do not start Task 3 until Task 2 compiles.

## Integration vs unit

- **Integration (`//go:build integration`, Docker + testcontainers, `make test-integration`):** Task 8 (store-layer fence tests), Task 9 (handler regression tests). These spin up Postgres.
- **Unit (`make test`, no Docker):** Task 7 (grace epoch-propagation tests in `grace_test.go` - `package worker`, no build tag).
- Tasks 1-6 are non-test changes; verify each by compiling (`go build ./...`) and, where noted, by running the existing unit suite.

## File structure

- `internal/store/migrations/000016_workers_connection_epoch.up.sql` / `.down.sql` - new column (Task 1).
- `internal/store/query/workers.sql` - `RegisterWorkerConnection`, `MarkWorkerOfflineIfEpoch` (Task 2).
- `internal/store/query/tasks.sql` - `RequeueWorkerTasksIfEpoch`; add `connection_epoch` to `ListGraceCandidates` (Task 2).
- `internal/store/*.sql.go`, `internal/store/models.go` - regenerated, never hand-edited (Task 2).
- `internal/worker/sender.go` - immutable `connEpoch int32` field on `workerSender` (Task 3).
- `internal/worker/handler.go` - `finishRegister`, `teardownConnection`, `markWorkerOffline`, `requeueWorkerTasks` (Tasks 3-5).
- `internal/worker/grace.go` - thread `epoch int32` through `Start`/`StartWithDuration`/`ExpireNow`/`onExpire` (Task 6).
- `cmd/relay-server/main.go` - grace `onExpire` callback and `seedGraceTimersFromActiveTasks` (Task 6).
- `internal/worker/grace_test.go` - epoch-propagation unit tests (Task 7).
- `internal/worker/export_test.go` - hooks to stamp a sender's epoch and call `RegisterWorkerConnection` (Task 9).
- `internal/store/store_test.go` (or a sibling) - SQL fence tests (Task 8).
- `internal/worker/handler_teardown_test.go` (or a sibling) - handler regression tests (Task 9).
- `docs/backlog/bug-2026-06-19-finishregister-gap-connection-epoch-race.md` -> `docs/backlog/closed/` (Task 10).

---

## Task 1: Migration - add `connection_epoch` column

**Files:**
- Create: `internal/store/migrations/000016_workers_connection_epoch.up.sql`
- Create: `internal/store/migrations/000016_workers_connection_epoch.down.sql`

- [ ] **Step 1: Write the up migration**

`internal/store/migrations/000016_workers_connection_epoch.up.sql`:

```sql
ALTER TABLE workers ADD COLUMN connection_epoch INT NOT NULL DEFAULT 0;
```

- [ ] **Step 2: Write the down migration**

`internal/store/migrations/000016_workers_connection_epoch.down.sql`:

```sql
ALTER TABLE workers DROP COLUMN connection_epoch;
```

- [ ] **Step 3: Verify the migration applies (build the server, which runs embedded migrations on startup)**

Run: `go build ./...`
Expected: builds clean. (The migration runs on the next integration test container boot; there is no standalone "apply" command. Do not start `relay-server` against a real DB here.)

- [ ] **Step 4: Commit**

```bash
git add internal/store/migrations/000016_workers_connection_epoch.up.sql internal/store/migrations/000016_workers_connection_epoch.down.sql
git commit -m "feat(store): add workers.connection_epoch migration"
```

---

## Task 2: Add the three epoch queries and regenerate

Adds `RegisterWorkerConnection` + `MarkWorkerOfflineIfEpoch` to `workers.sql`, `RequeueWorkerTasksIfEpoch` to `tasks.sql`, and extends `ListGraceCandidates` with `connection_epoch`. Then regenerates the store.

**Files:**
- Modify: `internal/store/query/workers.sql` (after `UpdateWorkerStatus`, around line 28)
- Modify: `internal/store/query/tasks.sql` (`ListGraceCandidates` at lines 106-113; add `RequeueWorkerTasksIfEpoch` after `RequeueWorkerTasks` at line 195)
- Regenerate: `internal/store/workers.sql.go`, `internal/store/tasks.sql.go`, `internal/store/models.go`

- [ ] **Step 1: Add `RegisterWorkerConnection` and `MarkWorkerOfflineIfEpoch` to `internal/store/query/workers.sql`**

Insert immediately after the `UpdateWorkerStatus` query (line 28):

```sql
-- name: RegisterWorkerConnection :one
-- Marks the worker online and atomically allocates a fresh connection_epoch for
-- this connection. The returned connection_epoch is the value this connection
-- owns; all later teardown writes for this connection fence on it. Clears
-- disconnected_at because a reconnected worker has no live disconnect timestamp.
UPDATE workers
SET status = 'online',
    last_seen_at = $2,
    disconnected_at = NULL,
    connection_epoch = connection_epoch + 1
WHERE id = $1
RETURNING *;

-- name: MarkWorkerOfflineIfEpoch :execrows
-- Flip the worker offline only if connection_epoch still matches the epoch the
-- caller's connection owned. A stale teardown whose epoch has been superseded by
-- a fresh registration affects zero rows and leaves the live worker online.
UPDATE workers
SET status = 'offline',
    last_seen_at = $2,
    disconnected_at = $3
WHERE id = $1 AND connection_epoch = $4;
```

- [ ] **Step 2: Add `RequeueWorkerTasksIfEpoch` to `internal/store/query/tasks.sql`**

Insert immediately after the `RequeueWorkerTasks` query (after line 195):

```sql
-- name: RequeueWorkerTasksIfEpoch :many
-- Re-queue dispatched/running tasks for a disconnected worker, but only if the
-- worker's connection_epoch still matches the epoch the caller owned. If a fresh
-- connection has superseded it, the EXISTS guard fails and zero tasks move.
-- Bumps assignment_epoch on each requeued task (task-level fence preserved).
UPDATE tasks
SET status = 'pending',
    worker_id = NULL,
    started_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE worker_id = $1 AND status IN ('dispatched', 'running')
  AND EXISTS (SELECT 1 FROM workers w WHERE w.id = $1 AND w.connection_epoch = $2)
RETURNING id;
```

- [ ] **Step 3: Extend `ListGraceCandidates` projection with `connection_epoch`**

In `internal/store/query/tasks.sql`, change the `ListGraceCandidates` SELECT (line 110) from:

```sql
SELECT DISTINCT w.id, w.disconnected_at
```

to:

```sql
SELECT DISTINCT w.id, w.disconnected_at, w.connection_epoch
```

(Leave the comment, FROM, JOIN, and WHERE clauses unchanged.)

- [ ] **Step 4: Regenerate the store**

Run: `make generate`
Expected: `internal/store/workers.sql.go`, `internal/store/tasks.sql.go`, and `internal/store/models.go` are rewritten. New symbols appear: `Queries.RegisterWorkerConnection` (returns `(Worker, error)`), `Queries.MarkWorkerOfflineIfEpoch` (returns `(int64, error)`), `Queries.RequeueWorkerTasksIfEpoch` (returns `([]pgtype.UUID, error)`), plus param structs `RegisterWorkerConnectionParams`, `MarkWorkerOfflineIfEpochParams`, `RequeueWorkerTasksIfEpochParams`; `Worker.ConnectionEpoch int32` in `models.go`; and `ConnectionEpoch int32` on `ListGraceCandidatesRow`.

- [ ] **Step 5: Strip the LF-only noise hunks (CLAUDE.md cleanup)**

Run: `git diff --ignore-all-space`
Expected: the only real changes are the three new generated query functions, their param structs, the new `Worker.ConnectionEpoch` field, and the `ListGraceCandidatesRow.ConnectionEpoch` field. For any generated file whose only diff is line-ending churn (no content change under `--ignore-all-space`), revert it:

```bash
git checkout -- <file-with-only-LF-churn>
```

Then verify the real changes still compile:

Run: `go build ./...`
Expected: builds clean.

- [ ] **Step 6: Commit**

```bash
git add internal/store/query/workers.sql internal/store/query/tasks.sql internal/store/workers.sql.go internal/store/tasks.sql.go internal/store/models.go
git commit -m "feat(store): add connection-epoch fence queries"
```

---

## Task 3: Stamp the owned epoch onto the sender and bump it in finishRegister

Adds the immutable `connEpoch` field to `workerSender`, switches `finishRegister`'s online-write to `RegisterWorkerConnection`, and stamps the returned epoch on the new sender.

**Files:**
- Modify: `internal/worker/sender.go:25-44` (struct + constructor)
- Modify: `internal/worker/handler.go:293-337` (`finishRegister`)

- [ ] **Step 1: Add the immutable `connEpoch` field to `workerSender`**

In `internal/worker/sender.go`, change the struct (lines 25-31) to add the field:

```go
// workerSender serializes all writes to a gRPC stream through a single
// send goroutine. gRPC bidirectional streams are not concurrent-send-safe.
type workerSender struct {
	stream  Sender
	queue   chan *relayv1.CoordinatorMessage
	stopReq chan struct{}
	closed  chan struct{}
	once    sync.Once

	// connEpoch is the workers.connection_epoch this connection owns, set once
	// in finishRegister and never mutated. Teardown fences shared-state writes
	// on it so a stale connection's writes no-op once a fresher one registers.
	connEpoch int32
}
```

`NewWorkerSender` leaves `connEpoch` at its zero value; `finishRegister` sets it before registering (next step). No constructor signature change.

- [ ] **Step 2: Switch finishRegister's online-write to `RegisterWorkerConnection` and stamp the epoch**

In `internal/worker/handler.go`, replace the `UpdateWorkerStatus("online")` block (lines 294-301):

```go
	updated, err := h.q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:         id,
		Status:     "online",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		return "", nil, fmt.Errorf("update worker status: %w", err)
	}
```

with:

```go
	updated, err := h.q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID:         id,
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		return "", nil, fmt.Errorf("register worker connection: %w", err)
	}
```

Then, at the sender registration (lines 336-337), stamp the epoch before registering:

```go
	// From here on, all sends go through the serializing wrapper.
	sender := NewWorkerSender(stream)
	sender.connEpoch = updated.ConnectionEpoch
	h.registry.Register(workerID, sender)
```

(The `grace.Cancel`, `reconcileRunningTasks`, `applyInventory`, `stream.Send`, metrics, and broker lines between them are unchanged. `updated.ConnectionEpoch` is the freshly-bumped value because `RegisterWorkerConnection` returns `*`.)

- [ ] **Step 3: Verify it compiles**

Run: `go build ./...`
Expected: builds clean. (`markWorkerOffline`/`requeueWorkerTasks`/`teardownConnection` still use their old unconditional paths at this point - they are rewired in Tasks 4-5. The sender carries an epoch nothing reads yet, which is fine.)

- [ ] **Step 4: Run the existing unit suite to confirm no regression**

Run: `go test ./internal/worker/... -run TestGraceRegistry -v -timeout 30s`
Expected: PASS (grace unit tests still green; they do not touch the new field).

- [ ] **Step 5: Commit**

```bash
git add internal/worker/sender.go internal/worker/handler.go
git commit -m "feat(worker): bump and stamp connection epoch in finishRegister"
```

---

## Task 4: Epoch-fence the offline write in teardown

Threads the sender's epoch through `teardownConnection` into `markWorkerOffline`, which now returns the rowcount and only publishes the offline event / clears metrics when the fence held.

**Files:**
- Modify: `internal/worker/handler.go:541-576` (`teardownConnection`, `markWorkerOffline`)

- [ ] **Step 1: Make `markWorkerOffline` epoch-fenced and return the rowcount**

In `internal/worker/handler.go`, replace `markWorkerOffline` (lines 555-576):

```go
// markWorkerOffline is called in a defer after the stream ends. It is fenced on
// connection_epoch: if a fresher connection has bumped the epoch, the write
// affects zero rows and the offline broker event / metrics-clear are skipped.
// Returns the number of rows updated (0 = fence superseded, 1 = applied).
func (h *Handler) markWorkerOffline(workerID string, epoch int32) int64 {
	var id pgtype.UUID
	if err := id.Scan(workerID); err != nil {
		return 0
	}
	ctx := context.Background()
	now := time.Now()
	rows, err := h.q.MarkWorkerOfflineIfEpoch(ctx, store.MarkWorkerOfflineIfEpochParams{
		ID:             id,
		LastSeenAt:     pgtype.Timestamptz{Time: now, Valid: true},
		DisconnectedAt: pgtype.Timestamptz{Time: now, Valid: true},
		ConnectionEpoch: epoch,
	})
	if err != nil || rows == 0 {
		return 0
	}
	h.broker.Publish(events.Event{
		Type: "worker",
		Data: []byte(fmt.Sprintf(`{"id":%q,"status":"offline"}`, workerID)),
	})
	if h.Metrics != nil {
		h.Metrics.Clear(workerID)
	}
	return rows
}
```

(Confirm the generated `MarkWorkerOfflineIfEpochParams` field name for `$4` is `ConnectionEpoch` after Task 2's generate; sqlc names it from the column. If the generated name differs, use the generated name.)

- [ ] **Step 2: Thread the epoch through `teardownConnection` and gate grace/requeue on the rowcount**

Replace `teardownConnection` (lines 541-553):

```go
func (h *Handler) teardownConnection(workerID string, sender *workerSender) {
	owned := h.registry.UnregisterIf(workerID, sender)
	sender.Close() // always stop our own send goroutine
	if !owned {
		return // a newer connection owns this worker; leave shared state alone
	}
	epoch := sender.connEpoch
	if h.markWorkerOffline(workerID, epoch) == 0 {
		return // a fresher connection holds the epoch; leave grace/requeue alone
	}
	if h.grace != nil {
		h.grace.Start(workerID, epoch)
	} else {
		h.requeueWorkerTasks(workerID, epoch)
	}
}
```

(`grace.Start` gains its `epoch` parameter in Task 6 and `requeueWorkerTasks` in Task 5; this file will not compile until those land. Build verification is deferred to Task 5 Step 3, which is the next compile gate. If you want an intermediate compile, do Tasks 4-6 as one unit before building - but commit them separately as written.)

- [ ] **Step 3: Commit**

```bash
git add internal/worker/handler.go
git commit -m "feat(worker): epoch-fence offline write in teardownConnection"
```

---

## Task 5: Epoch-fence the requeue helper

Switches `requeueWorkerTasks` to `RequeueWorkerTasksIfEpoch` and gives it the `epoch` parameter the teardown path now passes.

**Files:**
- Modify: `internal/worker/handler.go:578-587` (`requeueWorkerTasks`)

- [ ] **Step 1: Make `requeueWorkerTasks` epoch-fenced**

In `internal/worker/handler.go`, replace `requeueWorkerTasks` (lines 578-587):

```go
// requeueWorkerTasks requeues dispatched/running tasks for a disconnected
// worker, fenced on connection_epoch: if a fresher connection has bumped the
// epoch, the EXISTS guard fails and zero tasks move. Bumps assignment_epoch on
// each requeued task (task-level fence preserved).
func (h *Handler) requeueWorkerTasks(workerID string, epoch int32) {
	var id pgtype.UUID
	if err := id.Scan(workerID); err != nil {
		return
	}
	ctx := context.Background()
	_, _ = h.q.RequeueWorkerTasksIfEpoch(ctx, store.RequeueWorkerTasksIfEpochParams{
		WorkerID:        id,
		ConnectionEpoch: epoch,
	})
	go h.triggerDispatch()
}
```

(Confirm the generated `RequeueWorkerTasksIfEpochParams` field names after Task 2's generate: `$1` is `WorkerID`, `$2` is `ConnectionEpoch`. Use the generated names if they differ.)

- [ ] **Step 2: Commit**

```bash
git add internal/worker/handler.go
git commit -m "feat(worker): epoch-fence requeueWorkerTasks"
```

- [ ] **Step 3: Compile gate**

This will still fail to build until Task 6 adds the `epoch` parameter to `grace.Start`. Proceed directly to Task 6; the first green `go build ./...` lands at Task 6 Step 4. (If you prefer a green checkpoint per commit, implement Task 6 before committing Tasks 4-5; the plan keeps them as separate commits for review granularity.)

---

## Task 6: Thread the epoch through GraceRegistry and the main.go wiring

Adds `epoch int32` to `Start`, `StartWithDuration`, `ExpireNow`, and the `onExpire` callback. Updates the `cmd/relay-server/main.go` callback to call `RequeueWorkerTasksIfEpoch` and the seeder to read `connection_epoch` from the candidate row.

**Files:**
- Modify: `internal/worker/grace.go` (whole file - signatures)
- Modify: `cmd/relay-server/main.go:123-130` (onExpire callback), `:269-281` (seeder loop)

- [ ] **Step 1: Thread `epoch` through `GraceRegistry`**

Replace `internal/worker/grace.go` in full:

```go
package worker

import (
	"sync"
	"time"
)

// graceEntry pairs a pending timer with the connection_epoch that was live when
// the worker disconnected. The epoch is passed to onExpire at fire time so the
// requeue can be fenced (RequeueWorkerTasksIfEpoch), no-opping if the worker has
// since reconnected at a newer epoch.
type graceEntry struct {
	timer *time.Timer
	epoch int32
}

// GraceRegistry tracks per-worker grace timers. When a worker disconnects,
// Start schedules its onExpire callback to fire after window. If the worker
// reconnects before expiry, Cancel stops the timer. Stop cancels all pending
// timers without firing any of them (used on server shutdown).
//
// GraceRegistry is safe for concurrent use.
type GraceRegistry struct {
	mu       sync.Mutex
	timers   map[string]*graceEntry
	window   time.Duration
	onExpire func(workerID string, epoch int32)
	stopped  bool
}

// NewGraceRegistry returns a registry configured with the given grace window
// and expiry callback.
func NewGraceRegistry(window time.Duration, onExpire func(workerID string, epoch int32)) *GraceRegistry {
	return &GraceRegistry{
		timers:   make(map[string]*graceEntry),
		window:   window,
		onExpire: onExpire,
	}
}

// Start schedules onExpire(workerID, epoch) to fire after g.window. If a timer
// already exists for workerID, it is reset to the full window (idempotent).
func (g *GraceRegistry) Start(workerID string, epoch int32) {
	g.StartWithDuration(workerID, epoch, g.window)
}

// StartWithDuration schedules onExpire(workerID, epoch) to fire after d. If a
// timer already exists for workerID, it is replaced. Used by startup
// reconciliation to honor remaining grace from a persisted disconnect time.
func (g *GraceRegistry) StartWithDuration(workerID string, epoch int32, d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	if old, ok := g.timers[workerID]; ok {
		old.timer.Stop()
	}
	entry := &graceEntry{epoch: epoch}
	entry.timer = time.AfterFunc(d, func() {
		g.mu.Lock()
		// Guard against ABA: only fire if this specific entry is still the
		// active one. A concurrent Start may have replaced it between timer
		// expiry and lock acquisition.
		if g.timers[workerID] != entry {
			g.mu.Unlock()
			return
		}
		delete(g.timers, workerID)
		g.mu.Unlock()
		g.onExpire(workerID, entry.epoch)
	})
	g.timers[workerID] = entry
}

// ExpireNow invokes onExpire(workerID, epoch) synchronously without scheduling a
// timer. If a timer was already pending for workerID, it is cancelled to
// preserve the ABA-safety invariant. No-op if the registry has been Stopped.
// Used by startup reconciliation when persisted grace has already expired
// during downtime.
func (g *GraceRegistry) ExpireNow(workerID string, epoch int32) {
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return
	}
	if old, ok := g.timers[workerID]; ok {
		old.timer.Stop()
		delete(g.timers, workerID)
	}
	g.mu.Unlock()
	g.onExpire(workerID, epoch)
}

// Cancel stops any pending timer for workerID. Safe to call if no timer exists.
func (g *GraceRegistry) Cancel(workerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e, ok := g.timers[workerID]; ok {
		e.timer.Stop()
		delete(g.timers, workerID)
	}
}

// Stop cancels all pending timers without firing any of them. After Stop,
// subsequent Start calls are no-ops.
func (g *GraceRegistry) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
	for id, e := range g.timers {
		e.timer.Stop()
		delete(g.timers, id)
	}
}
```

- [ ] **Step 2: Update the `onExpire` callback in `cmd/relay-server/main.go`**

Replace the `NewGraceRegistry` callback (lines 123-130):

```go
	grace := worker.NewGraceRegistry(graceWindow, func(workerID string, epoch int32) {
		var id pgtype.UUID
		if err := id.Scan(workerID); err != nil {
			return
		}
		_, _ = q.RequeueWorkerTasksIfEpoch(context.Background(), store.RequeueWorkerTasksIfEpochParams{
			WorkerID:        id,
			ConnectionEpoch: epoch,
		})
		dispatcher.Trigger()
	})
```

(Confirm `store` is already imported in `main.go` - it is, via `seedGraceTimersFromActiveTasks`. Use the generated param field names from Task 2.)

- [ ] **Step 3: Update `seedGraceTimersFromActiveTasks` to pass the persisted epoch**

In `cmd/relay-server/main.go`, replace the seeder loop body (lines 269-281):

```go
	for _, c := range candidates {
		id := uuidStrMain(c.ID)
		if !c.DisconnectedAt.Valid {
			grace.Start(id, c.ConnectionEpoch)
			continue
		}
		remaining := c.DisconnectedAt.Time.Add(window).Sub(now)
		if remaining > 0 {
			grace.StartWithDuration(id, c.ConnectionEpoch, remaining)
		} else {
			grace.ExpireNow(id, c.ConnectionEpoch)
		}
	}
```

(`c.ConnectionEpoch` exists because Task 2 added it to the `ListGraceCandidates` projection.)

- [ ] **Step 4: Verify the whole tree compiles**

Run: `go build ./...`
Expected: builds clean. This is the first green build since Task 3 - it confirms the Task 4/5 handler changes, the grace signatures, and the main.go wiring all line up.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/grace.go cmd/relay-server/main.go
git commit -m "feat(worker): thread connection epoch through grace registry and seeder"
```

---

## Task 7: Update grace unit tests for the epoch-carrying signature

Pure-Go (no Docker). Updates every `grace_test.go` call to the new `Start(id, epoch)` / `StartWithDuration(id, epoch, d)` / `ExpireNow(id, epoch)` / `func(workerID string, epoch int32)` signatures, and adds an assertion that the epoch is propagated to `onExpire`.

**Files:**
- Modify: `internal/worker/grace_test.go` (every test)

- [ ] **Step 1: Add an epoch-propagation test (red)**

Add this test to `internal/worker/grace_test.go`:

```go
func TestGraceRegistry_StartPropagatesEpochToOnExpire(t *testing.T) {
	var gotEpoch atomic.Int32
	var fired atomic.Int32
	g := NewGraceRegistry(30*time.Millisecond, func(workerID string, epoch int32) {
		if workerID == "w1" {
			gotEpoch.Store(epoch)
			fired.Add(1)
		}
	})
	defer g.Stop()

	g.Start("w1", 7)
	require.Eventually(t, func() bool {
		return fired.Load() == 1
	}, 200*time.Millisecond, 5*time.Millisecond)
	assert.Equal(t, int32(7), gotEpoch.Load(), "onExpire must receive the epoch Start was called with")
}
```

- [ ] **Step 2: Run it to verify it fails to COMPILE (red)**

Run: `go test ./internal/worker/... -run TestGraceRegistry -v -timeout 30s`
Expected: build failure - the existing tests still call `Start("w1")`, `func(workerID string)`, etc., which no longer match the Task 6 signatures.

- [ ] **Step 3: Update every existing grace test to the new signatures (green)**

Make these edits in `internal/worker/grace_test.go`:

- `TestGraceRegistry_StartFiresAfterWindow`: callback `func(workerID string)` -> `func(workerID string, epoch int32)`; `g.Start("w1")` -> `g.Start("w1", 1)`.
- `TestGraceRegistry_CancelPreventsFire`: callback gains `, epoch int32`; `g.Start("w1")` -> `g.Start("w1", 1)`.
- `TestGraceRegistry_StartIsIdempotent`: callback gains `, epoch int32`; all three `g.Start("w1")` -> `g.Start("w1", 1)`.
- `TestGraceRegistry_StopPreventsAllFires`: callback gains `, epoch int32`; `g.Start("w1")` -> `g.Start("w1", 1)`, `g.Start("w2")` -> `g.Start("w2", 1)`.
- `TestGraceRegistry_CancelNonexistentIsSafe`: callback `func(workerID string) {}` -> `func(workerID string, epoch int32) {}`.
- `TestGraceRegistry_ConcurrentStartCancelStop`: callback `func(workerID string) {}` -> `func(workerID string, epoch int32) {}`; `g.Start("w1")` -> `g.Start("w1", 1)`, `g.Start("w2")` -> `g.Start("w2", 1)`.
- `TestGraceRegistry_StartWithDurationFiresAfterCustomWindow`: callback gains `, epoch int32`; `g.StartWithDuration("w-custom", 30*time.Millisecond)` -> `g.StartWithDuration("w-custom", 1, 30*time.Millisecond)`.
- `TestGraceRegistry_ExpireNowFiresSynchronously`: callback `func(workerID string)` -> `func(workerID string, epoch int32)`; `g.ExpireNow("w-expired")` -> `g.ExpireNow("w-expired", 1)`.
- `TestGraceRegistry_ExpireNowAfterStopIsNoOp`: callback `func(string)` -> `func(string, int32)`; `g.ExpireNow("w-late")` -> `g.ExpireNow("w-late", 1)`.
- `TestGraceRegistry_ExpireNowReplacesPendingTimer`: callback `func(string)` -> `func(string, int32)`; `g.Start("w-x")` -> `g.Start("w-x", 1)`; `g.ExpireNow("w-x")` -> `g.ExpireNow("w-x", 1)`.

- [ ] **Step 4: Run the grace tests to verify they pass (green)**

Run: `go test ./internal/worker/... -run TestGraceRegistry -v -timeout 30s`
Expected: PASS, including the new `TestGraceRegistry_StartPropagatesEpochToOnExpire`.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/grace_test.go
git commit -m "test(worker): grace registry propagates connection epoch"
```

---

## Task 8: Store-layer SQL fence tests (integration)

Proves correctness where atomicity lives: the rowcount of the `IfEpoch` queries. One no-op test for the offline fence, one no-op test for the requeue fence, and one positive control. Requires Docker.

**Files:**
- Modify: `internal/store/store_test.go` (add three tests; `package store_test`, `//go:build integration` already at file top)

- [ ] **Step 1: Write the offline-fence test (red)**

Add to `internal/store/store_test.go`:

```go
func TestMarkWorkerOfflineIfEpoch_StaleEpochIsNoOp(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w-off", Hostname: "w-off-host", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	// Connection S registers: connection_epoch 0 -> 1, status online.
	s, err := q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID:         w.ID,
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), s.ConnectionEpoch)

	// Connection F reconnects: connection_epoch 1 -> 2, status stays online.
	f, err := q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID:         w.ID,
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), f.ConnectionEpoch)

	// S's stale teardown tries to mark offline at epoch 1: fence holds, 0 rows.
	now := time.Now()
	rows, err := q.MarkWorkerOfflineIfEpoch(ctx, store.MarkWorkerOfflineIfEpochParams{
		ID:              w.ID,
		LastSeenAt:      pgtype.Timestamptz{Time: now, Valid: true},
		DisconnectedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		ConnectionEpoch: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), rows, "stale-epoch offline must affect zero rows")

	after, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	assert.Equal(t, "online", after.Status, "live worker must stay online")

	// Current-epoch offline (epoch 2) applies: positive control.
	rows, err = q.MarkWorkerOfflineIfEpoch(ctx, store.MarkWorkerOfflineIfEpochParams{
		ID:              w.ID,
		LastSeenAt:      pgtype.Timestamptz{Time: now, Valid: true},
		DisconnectedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		ConnectionEpoch: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), rows, "current-epoch offline must apply")
	after, err = q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	assert.Equal(t, "offline", after.Status)
}
```

- [ ] **Step 2: Write the requeue-fence no-op test and the positive control (red)**

Add to `internal/store/store_test.go`:

```go
func TestRequeueWorkerTasksIfEpoch_StaleEpochIsNoOp(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Req-Stale", "req-stale@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w-req-stale", Hostname: "w-req-stale-host", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// Worker comes online at epoch 1, task claimed (assignment_epoch 0 -> 1, dispatched).
	_, err = q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID: w.ID, LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "dispatched", claimed.Status)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// Reconnect bumps connection_epoch 1 -> 2 (grace timer was armed at epoch 1).
	_, err = q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID: w.ID, LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	// Stale grace fire at epoch 1: EXISTS guard fails, zero tasks move.
	ids, err := q.RequeueWorkerTasksIfEpoch(ctx, store.RequeueWorkerTasksIfEpochParams{
		WorkerID: w.ID, ConnectionEpoch: 1,
	})
	require.NoError(t, err)
	assert.Empty(t, ids, "stale-epoch requeue must move zero tasks")

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status, "task must stay dispatched")
	assert.Equal(t, int32(1), after.AssignmentEpoch, "assignment_epoch must not be bumped")
	assert.Equal(t, w.ID, after.WorkerID, "task must remain assigned")
}

func TestRequeueWorkerTasksIfEpoch_CurrentEpochRequeues(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Req-Cur", "req-cur@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w-req-cur", Hostname: "w-req-cur-host", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// Worker online at epoch 1, task claimed (assignment_epoch -> 1).
	_, err = q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID: w.ID, LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// No reconnect: requeue at the current epoch 1 moves the task.
	ids, err := q.RequeueWorkerTasksIfEpoch(ctx, store.RequeueWorkerTasksIfEpochParams{
		WorkerID: w.ID, ConnectionEpoch: 1,
	})
	require.NoError(t, err)
	require.Len(t, ids, 1)

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", after.Status, "task must be requeued to pending")
	assert.Equal(t, int32(2), after.AssignmentEpoch, "assignment_epoch must be bumped 1 -> 2")
	assert.False(t, after.WorkerID.Valid, "worker_id must be cleared")
}
```

- [ ] **Step 3: Run the three tests to verify they fail before, pass after**

Because Tasks 1-6 already implemented the queries, these tests should pass on first run. Run them to confirm:

Run: `go test -tags integration -p 1 ./internal/store/... -run "TestMarkWorkerOfflineIfEpoch_StaleEpochIsNoOp|TestRequeueWorkerTasksIfEpoch" -v -timeout 180s`
Expected: PASS (all three). (Requires Docker Desktop running. If a query symbol is missing, Task 2's generate was incomplete - return to Task 2.)

- [ ] **Step 4: Commit**

```bash
git add internal/store/store_test.go
git commit -m "test(store): connection-epoch fence rowcount tests"
```

---

## Task 9: Handler regression test (integration)

Drives the residual hazard at the SQL fence through the handler's export hooks: a stale sender carrying epoch 1 while the worker row holds epoch 2. Mirrors `handler_teardown_test.go`. Requires Docker.

**Files:**
- Modify: `internal/worker/export_test.go` (add two hooks)
- Modify: `internal/worker/handler_teardown_test.go` (add the new test)

- [ ] **Step 1: Add export hooks to stamp a sender's epoch and to call `RegisterWorkerConnection`**

Add to `internal/worker/export_test.go`:

```go
// RegisteredSenderWithEpochForTest wraps stream in a real *workerSender, stamps
// the given connEpoch (as finishRegister does), registers it for workerID, and
// returns an opaque handle. Lets worker_test drive teardown with a known,
// possibly-stale, owned epoch.
func (h *Handler) RegisteredSenderWithEpochForTest(workerID string, stream Sender, epoch int32) *SenderHandle {
	s := NewWorkerSender(stream)
	s.connEpoch = epoch
	h.registry.Register(workerID, s)
	return &SenderHandle{s: s}
}

// RegisterWorkerConnectionForTest invokes the store's RegisterWorkerConnection
// so worker_test can advance a worker's connection_epoch and read the new value.
func (h *Handler) RegisterWorkerConnectionForTest(ctx context.Context, id pgtype.UUID) (int32, error) {
	w, err := h.q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID:         id,
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		return 0, err
	}
	return w.ConnectionEpoch, nil
}
```

Add the imports `"relay/internal/store"` and `"time"` to `export_test.go` if not already present (current imports are `context`, `testing`, `relayv1`, `pgtype` - add `store` and `time`).

- [ ] **Step 2: Write the handler regression test (red)**

Add to `internal/worker/handler_teardown_test.go`:

```go
// TestTeardownConnection_StaleEpochDoesNotRequeueLiveWorker proves the
// connection-epoch fence: a stale connection whose owned epoch (1) is older than
// the row's current epoch (2, set by a fresh reconnect) must not mark the worker
// offline or requeue its running task when its teardown runs.
func TestTeardownConnection_StaleEpochDoesNotRequeueLiveWorker(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	user, err := fx.Q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "epoch-user", Email: "epoch-user@test.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := fx.Q.CreateJob(ctx, store.CreateJobParams{
		Name: "epoch-job", Priority: "normal", SubmittedBy: user.ID,
		Labels: []byte("{}"), ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := fx.Q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "epoch-task",
		Commands: []byte(`[["echo","hi"]]`), Env: []byte("{}"), Requires: []byte("[]"), Retries: 0,
	})
	require.NoError(t, err)
	wk, err := fx.Q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "epoch-worker", Hostname: "epoch-worker-01",
		CpuCores: 4, RamGb: 8, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	// Stale connection S registers: connection_epoch 0 -> 1.
	epochS, err := fx.Handler.RegisterWorkerConnectionForTest(ctx, wk.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), epochS)

	// Claim the task at epoch 0 -> 1, dispatched, assigned to wk.
	claimed, err := fx.Q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: wk.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	require.Equal(t, "dispatched", claimed.Status)

	workerIDStr := fx.Handler.UUIDStringForTest(wk.ID)

	// Register stale sender A carrying connEpoch 1.
	staleStream := &fakeSender{}
	staleH := fx.Handler.RegisteredSenderWithEpochForTest(workerIDStr, staleStream, epochS)

	// Fresh reconnect F: connection_epoch 1 -> 2; register fresh sender B (epoch 2),
	// which replaces A in the registry.
	epochF, err := fx.Handler.RegisterWorkerConnectionForTest(ctx, wk.ID)
	require.NoError(t, err)
	require.Equal(t, int32(2), epochF)
	freshStream := &fakeSender{}
	freshH := fx.Handler.RegisteredSenderWithEpochForTest(workerIDStr, freshStream, epochF)

	// Run S's stale teardown. UnregisterIf returns false (B owns the slot), so it
	// short-circuits; even in the gap interleaving the SQL fence on epoch 1 (row
	// holds 2) would no-op the offline and requeue. Either way: no effect.
	fx.Handler.TeardownConnectionForTest(workerIDStr, staleH)

	// 1. Worker stays online (fence held; B is live).
	wAfter, err := fx.Q.GetWorker(ctx, wk.ID)
	require.NoError(t, err)
	assert.Equal(t, "online", wAfter.Status, "live worker must stay online")
	assert.Equal(t, int32(2), wAfter.ConnectionEpoch, "row must still hold the fresh epoch")

	// 2. Running task untouched: same epoch, dispatched, still assigned.
	taskAfter, err := fx.Q.GetTask(ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), taskAfter.AssignmentEpoch, "task epoch must not be bumped")
	assert.Equal(t, "dispatched", taskAfter.Status, "task must not be requeued")
	assert.Equal(t, wk.ID, taskAfter.WorkerID, "task must remain assigned")

	// 3. Fresh sender B still registered: a Send reaches it.
	dispatch := &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: &relayv1.DispatchTask{TaskId: "still-alive"},
		},
	}
	require.NoError(t, fx.Handler.SendToWorkerForTest(workerIDStr, dispatch),
		"fresh sender B must remain registered after stale teardown")

	// Clean up B's goroutine via its own legitimate teardown.
	fx.Handler.TeardownConnectionForTest(workerIDStr, freshH)
}
```

- [ ] **Step 3: Run the regression test (verify green)**

Run: `go test -tags integration -p 1 ./internal/worker/... -run "TestTeardownConnection" -v -timeout 180s`
Expected: PASS, including both the predecessor's `TestTeardownConnection_StaleSenderDoesNotClobberFreshRegistration` and the new `TestTeardownConnection_StaleEpochDoesNotRequeueLiveWorker`. (Requires Docker.)

- [ ] **Step 4: Commit**

```bash
git add internal/worker/export_test.go internal/worker/handler_teardown_test.go
git commit -m "test(worker): connection-epoch fence regression test"
```

---

## Task 10: Full verification and backlog close-out

**Files:**
- Move: `docs/backlog/bug-2026-06-19-finishregister-gap-connection-epoch-race.md` -> `docs/backlog/closed/`

- [ ] **Step 1: Run the unit suite**

Run: `make test`
Expected: PASS (grace epoch-propagation test and all existing unit tests green; no Docker needed).

- [ ] **Step 2: Run the affected integration suites**

Run: `go test -tags integration -p 1 ./internal/store/... ./internal/worker/... -timeout 300s`
Expected: PASS (Docker Desktop running). Covers the SQL fence tests and the handler regression tests plus all pre-existing store/worker integration tests against the new schema.

- [ ] **Step 3: Move the backlog item to closed (required scope, not optional cleanup)**

```bash
git mv docs/backlog/bug-2026-06-19-finishregister-gap-connection-epoch-race.md docs/backlog/closed/bug-2026-06-19-finishregister-gap-connection-epoch-race.md
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore(backlog): close finishregister-gap-connection-epoch-race"
```

---

## Self-review

**Spec coverage:**
- Migration `000016` up/down -> Task 1.
- `RegisterWorkerConnection`, `MarkWorkerOfflineIfEpoch`, `RequeueWorkerTasksIfEpoch`, `ListGraceCandidates` projection + regenerate -> Task 2.
- `workerSender.connEpoch` + `finishRegister` bump/stamp -> Task 3.
- `teardownConnection` + `markWorkerOffline` epoch fence (rowcount gates broker/metrics) -> Task 4.
- `requeueWorkerTasks` epoch fence -> Task 5.
- `GraceRegistry` `Start`/`StartWithDuration`/`ExpireNow`/`onExpire` epoch threading + `cmd/relay-server/main.go` callback and seeder -> Task 6.
- Grace unit tests -> Task 7.
- SQL fence rowcount tests (stale offline no-op, stale requeue no-op, current-epoch positive control) -> Task 8.
- Handler regression test + export hooks -> Task 9.
- Backlog close -> Task 10.
- Enrollment/reconnect/auto-enroll all funnel through `finishRegister`, so the single bump site in Task 3 covers all three (no per-path task needed, matching the spec decision).
- Out-of-scope items honored: `RequeueWorkerTasks` (disable path, `internal/api/workers.go:484`) untouched; `UpdateWorkerStatus` retained for the offline-via-sweeper and other callers; no frontend/REST/SSE/CLI change.

**Type consistency:** `connEpoch` (sender field) vs `ConnectionEpoch` (generated column/param field) are deliberately distinct - Go struct field on the unexported sender is `connEpoch`; the sqlc-generated names are `ConnectionEpoch`. `epoch int32` is the parameter name used uniformly across `markWorkerOffline`, `requeueWorkerTasks`, `teardownConnection`, and all `GraceRegistry` methods. `RegisterWorkerConnection` returns a full `Worker` (`RETURNING *`), so `.ConnectionEpoch` is available on `updated`. The generated param field names (`ConnectionEpoch`, `WorkerID`, `LastSeenAt`, `DisconnectedAt`) are flagged in Tasks 4/5/6 to confirm against the actual `make generate` output, since sqlc derives them from columns.

**Placeholder scan:** none. Every code step shows complete code; every run step has an exact command and expected result.
