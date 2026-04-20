# Major Concurrency & Scaling Fixes — Design

**Date:** 2026-04-20
**Scope:** Addresses four Major issues from the relay design review: in-flight task survival across disconnects, scheduler query efficiency, real-time dispatch wake-ups, and database pool sizing.
**Non-goals:** Multi-server support (deferred to its own design). Any protocol-breaking rework beyond what these four issues require. Sticky routing or session persistence beyond a single coordinator process.

## Motivation

After the Critical concurrency fixes (2026-04-19), the coordinator is safe under concurrent pressure — no double-dispatch, no gRPC concurrent-send panics, no silent SSE drops, no agent-side message loss. The next tier of issues are about *behavior under adverse conditions* and *baseline efficiency*:

- **Issue 1 — In-flight task reconciliation.** Today a brief network blip between agent and coordinator kills every running subprocess on that agent. On a render farm where tasks can run for hours, a two-second wifi glitch throws away real work. The coordinator also blanket-requeues everything at startup, so a clean bounce of `relay-server` has the same effect.
- **Issue 2 — N×M scheduler queries.** The dispatch loop calls `CountActiveTasksForWorker` inside the task×worker loop. With 100 tasks and 20 workers, that's up to 2,000 round-trips per cycle.
- **Issue 3 — 5s scheduler polling.** Dispatch wakes only every 5 seconds, so newly submitted tasks wait up to 5s for dispatch even when workers are idle.
- **Issue 5 — pgxpool MaxConns=4.** The default is tuned for test machines, not for a coordinator holding streaming connections from 10+ agents plus CLI/UI traffic.

Fixed together because they all live in the coordinator/agent coordination layer and reinforce each other: the reconciliation work depends on SQL changes that touch the same files as the N×M fix; the LISTEN/NOTIFY work touches the same scheduler loop; the pool-sizing fix is a one-liner that's needed to make the others breathe.

## Guiding principle

Long-running tasks should survive brief network blips and clean server restarts. The coordinator and agent reconcile explicitly on reconnect using a per-assignment epoch, within a bounded grace window. Beyond the window, the coordinator gives up and requeues — so failure modes remain bounded and observable.

## Approach summary

1. Add an `assignment_epoch` column to `tasks`, incremented on every claim. Every task-related proto message carries the epoch.
2. Agent runners live for the agent's lifetime, not the connection's lifetime. A disconnect no longer cancels subprocesses.
3. On reconnect, the agent's `RegisterRequest` carries the list of tasks it's still running (`task_id`, `epoch`). The coordinator diffs against DB state and replies with a `cancel_task_ids` list for any assignments the agent should abandon (because the grace window expired and the task was reassigned).
4. On agent disconnect, the coordinator starts a per-worker grace timer instead of requeueing immediately. The timer is cancelled by reconnect; it fires `RequeueWorkerTasks` after the window.
5. On coordinator startup, seed a grace timer for every worker that has non-terminal tasks in the DB. Don't blanket-requeue. Reconnecting agents reconcile their tasks within the window.
6. Scheduler computes per-worker active-task counts in a single aggregate query before the dispatch loop.
7. `LISTEN/NOTIFY` becomes the dispatch wake signal for task-state changes; the 5s ticker relaxes to 30s as a safety net.
8. `pgxpool.MaxConns` is configurable via `RELAY_DB_MAX_CONNS`, default 25.

## Data model

### Migration

New file `internal/store/migrations/NNN_add_assignment_epoch.up.sql`:

```sql
ALTER TABLE tasks ADD COLUMN assignment_epoch int NOT NULL DEFAULT 0;
```

Down migration drops the column. No backfill needed — existing in-flight tasks will get `epoch=1` when next re-claimed; any pre-upgrade message without an epoch field deserializes to `epoch=0`, which won't match a re-claimed task's current epoch.

### Proto additions

In `internal/proto/relayv1/*.proto`:

```proto
message DispatchTask {
  // ... existing fields ...
  int64 epoch = N;
}

message TaskStatusUpdate {
  // ... existing fields ...
  int64 epoch = N;
}

message TaskLogChunk {
  // ... existing fields ...
  int64 epoch = N;     // late chunks from a stale generation get dropped
}

message RegisterRequest {
  // ... existing fields ...
  repeated RunningTask running_tasks = N;
}

message RunningTask {
  string task_id = 1;
  int64  epoch   = 2;
}

message RegisterResponse {
  // ... existing fields ...
  repeated string cancel_task_ids = N;  // agent abandons these without final-status
}
```

Field numbers are TBD based on the current proto; whatever slots are next available.

### SQL / sqlc queries

**Modified** (in `internal/store/query/tasks.sql`):

- `ClaimTaskForWorker` — adds `assignment_epoch = assignment_epoch + 1` to the SET list and `assignment_epoch` to the RETURNING list.
- `UpdateTaskStatus` — takes an `epoch` parameter; adds `AND assignment_epoch = $epoch` to the WHERE clause. Stale updates affect 0 rows. The handler checks `RowsAffected` and silently drops non-matching updates.
- `AppendTaskLog` — same epoch-guarded WHERE clause.

**New**:

- `CountActiveTasksByAllWorkers :many` — `SELECT worker_id, count(*)::bigint AS active FROM tasks WHERE worker_id IS NOT NULL AND status IN ('dispatched','running') GROUP BY worker_id`. Fixes the N×M query pattern.
- `GetActiveTasksForWorker :many` — returns `(task_id, assignment_epoch)` for all dispatched/running tasks for a given worker. Used by the reconcile path.
- `ListWorkersWithActiveTasks :many` — returns distinct worker IDs with non-terminal tasks. Used at server startup to seed grace timers.
- `RequeueTaskByID :exec` — requeues a single task by ID (used by reconcile path when server has a task the agent didn't report).

**Removed / retired**:

- `RequeueAllActiveTasks` — no longer called at server startup. Kept in the file for now (may be useful as an admin tool) but unused in the server main.

## Server-side architecture

### New: `internal/worker/grace.go`

A small in-memory registry mapping `worker_id → *time.Timer`:

```go
type GraceRegistry struct {
    mu       sync.Mutex
    timers   map[string]*time.Timer
    window   time.Duration
    onExpire func(workerID string)  // calls RequeueWorkerTasks + triggers dispatch
}

func NewGraceRegistry(window time.Duration, onExpire func(string)) *GraceRegistry
func (g *GraceRegistry) Start(workerID string)   // idempotent — restarts if existing
func (g *GraceRegistry) Cancel(workerID string)  // worker reconnected
func (g *GraceRegistry) Stop()                   // server shutdown — cancel all, no fire
```

Not persisted. Under single-server deployment, a coordinator crash drops all timers; the new coordinator's startup reconciliation logic reseeds them.

### `internal/worker/handler.go` changes

**Disconnect defer chain** (replace immediate requeue with grace timer):

```go
// OLD:
defer h.requeueWorkerTasks(workerID)

// NEW:
defer h.grace.Start(workerID)
```

Other defers (`markWorkerOffline`, `sender.Close`, `registry.Unregister`) unchanged.

**Reconcile inside `registerWorker`** (after DB upserts, before `stream.Send(RegisterResponse)`):

```go
h.grace.Cancel(workerID)  // agent is back — stop the clock

serverTasks, err := h.q.GetActiveTasksForWorker(ctx, w.ID)
// ... handle error ...

serverSet := make(map[string]int64, len(serverTasks))
for _, t := range serverTasks {
    serverSet[uuidStr(t.ID)] = t.AssignmentEpoch
}

var cancelIDs []string
agentSet := make(map[string]bool, len(reg.RunningTasks))
for _, rt := range reg.RunningTasks {
    agentSet[rt.TaskId] = true
    serverEpoch, ok := serverSet[rt.TaskId]
    if !ok || serverEpoch != rt.Epoch {
        cancelIDs = append(cancelIDs, rt.TaskId)
    }
}

for taskID := range serverSet {
    if !agentSet[taskID] {
        var tID pgtype.UUID
        _ = tID.Scan(taskID)
        _ = h.q.RequeueTaskByID(ctx, tID)
    }
}

// Include cancelIDs in RegisterResponse.
```

**Epoch enforcement in `handleTaskStatus`**: after `GetTask`, compare `task.AssignmentEpoch` to `upd.Epoch`; mismatch → return immediately (no retry, no status update, no broker publish). This early gate matters because the existing retry path (`IncrementTaskRetryCount`) runs *before* `UpdateTaskStatus` and must not fire on stale updates. `UpdateTaskStatus` also gains an epoch-guarded WHERE clause as defense-in-depth for the update itself. Log at DEBUG only — these are expected during reconnect races.

**Epoch enforcement in `handleTaskLog`**: `AppendTaskLog` changes from a simple `INSERT` to `INSERT INTO task_logs (...) SELECT $1, $2, $3 WHERE EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND assignment_epoch = $4)`. Stale chunks from a reassigned generation silently insert zero rows. Remains `:exec` — no caller change needed.

### `cmd/relay-server/main.go` changes

**Startup**:

```go
// OLD:
if err := q.RequeueAllActiveTasks(ctx); err != nil { ... }

// NEW:
graceWindow := 2 * time.Minute
if v := os.Getenv("RELAY_WORKER_GRACE_WINDOW"); v != "" {
    if d, err := time.ParseDuration(v); err == nil {
        graceWindow = d
    }
}

grace := worker.NewGraceRegistry(graceWindow, func(workerID string) {
    var id pgtype.UUID
    _ = id.Scan(workerID)
    _ = q.RequeueWorkerTasks(context.Background(), id)
    dispatcher.Trigger()
})

workersWithTasks, _ := q.ListWorkersWithActiveTasks(ctx)
for _, wID := range workersWithTasks {
    grace.Start(uuidStr(wID))
}
```

**Pool sizing**:

```go
dbMaxConns := 25
if v := os.Getenv("RELAY_DB_MAX_CONNS"); v != "" {
    if n, err := strconv.Atoi(v); err == nil && n > 0 {
        dbMaxConns = n
    }
}
cfg, err := pgxpool.ParseConfig(dsn)
if err != nil { log.Fatalf("parse dsn: %v", err) }
cfg.MaxConns = int32(dbMaxConns)
pool, err := pgxpool.NewWithConfig(ctx, cfg)
```

**Graceful shutdown**: call `grace.Stop()` before closing the gRPC listener. Clean shutdowns do not requeue; the next `relay-server` instance's startup reconciliation covers the gap.

### New: `internal/scheduler/notify.go`

Dedicated-connection LISTEN loop:

```go
type NotifyListener struct {
    pool    *pgxpool.Pool
    trigger func()
}

// Run blocks until ctx is cancelled. Acquires a dedicated connection,
// LISTENs on relay_task_submitted and relay_task_completed, and calls
// trigger() on any notification. On error: release, back off (1s→60s), retry.
func (n *NotifyListener) Run(ctx context.Context)
```

Started in `main.go` alongside `dispatcher.Run()`. When its connection is broken (pool churn, postgres restart), the 30s polling ticker covers the gap until the listener reconnects.

### NOTIFY firing sites

- `internal/api/jobs.go` — after `InsertTask` succeeds, call new helper that runs `SELECT pg_notify('relay_task_submitted', '')`. Replaces the existing in-process `triggerDispatch()` call on this path.
- `internal/worker/handler.go` `handleTaskStatus` — after accepting a terminal status, call the notify helper for `relay_task_completed`. Replaces the in-process `go h.triggerDispatch()`.
- Worker-online trigger in `registerWorker` is kept as a direct in-process `dispatcher.Trigger()` — no task row changed, no need for NOTIFY.

### Dispatch loop changes (`internal/scheduler/dispatch.go`)

**Ticker interval**: 5s → 30s.

**N×M fix** in `dispatch`:

```go
func (d *Dispatcher) dispatch(ctx context.Context) {
    tasks, err := d.q.GetEligibleTasks(ctx)
    if err != nil || len(tasks) == 0 { return }

    workers, err := d.q.ListWorkers(ctx)
    if err != nil { return }

    reservations, err := d.q.ListActiveReservations(ctx)
    if err != nil { return }

    counts, err := d.q.CountActiveTasksByAllWorkers(ctx)
    if err != nil { return }
    activeByWorker := make(map[pgtype.UUID]int64, len(counts))
    for _, c := range counts {
        activeByWorker[c.WorkerID] = c.Active
    }

    for _, task := range tasks {
        w := d.selectWorker(task, workers, reservations, activeByWorker)
        if w != nil {
            if d.sendTask(ctx, task, *w) {
                activeByWorker[w.ID]++  // reflect this cycle's dispatch in remaining iterations
            }
        }
    }
}
```

`selectWorker` loses its `ctx` parameter (no DB access needed) and takes the pre-computed map. `sendTask` returns `bool` indicating whether a dispatch was successfully claimed + sent (so the caller can update the map).

## Agent-side architecture

### Runner lifetime is agent-lifetime

`Agent` gains a field for the long-lived parent context:

```go
type Agent struct {
    // ... existing ...
    runCtx context.Context  // set once in Run(); passed to runners
}
```

`handleDispatch` constructs runners with `a.runCtx` rather than `connCtx`:

```go
func (a *Agent) handleDispatch(connCtx context.Context, task *relayv1.DispatchTask) {
    runner, runCtx := newRunner(task.TaskId, task.Epoch, a.sendCh, a.runCtx, task.TimeoutSeconds)
    // ... existing registration in a.runners map ...
    a.runnerWG.Add(1)
    go func() {
        defer a.runnerWG.Done()
        runner.Run(runCtx, task)
        a.mu.Lock()
        delete(a.runners, task.TaskId)
        a.mu.Unlock()
    }()
}
```

`newRunner` gains an `epoch int64` parameter stored on the Runner. All outgoing `TaskStatusUpdate` and `TaskLogChunk` messages include `r.epoch`.

**Two contexts to keep straight on the runner:**
- `r.ctx` — stored on the Runner struct, used as the fallback branch in `r.send`'s blocking select. Bound to `a.runCtx` (agent-lifetime). A stream drop does *not* cancel this; only agent shutdown does. This is what prevents pending sends during a disconnect from being silently dropped.
- `runCtx` (returned from `newRunner`, passed to `Run`) — derived from `a.runCtx` with `context.WithTimeout` if the task has a timeout, else `context.WithCancel`. Used for `exec.CommandContext`. Cancelling this kills the subprocess; `r.Cancel()` and `r.Abandon()` both do so.

Net effect: stream drops don't kill subprocesses; agent shutdown does; task timeout does; `Abandon` does.

### No runner cancellation on stream error

The recv loop's cleanup:

```go
// OLD:
a.mu.Lock()
for _, r := range a.runners { r.Cancel() }
a.mu.Unlock()

// NEW:
// (nothing — runners continue running)
```

The sender goroutine still exits on `connCtx.Done()` (that's correct — it's scoped to this stream). Buffered messages in `a.sendCh` wait for the next connection's sender goroutine.

### No more sendCh drain or runnerWG.Wait on reconnect

The drain loop at the top of `connect()` is removed. The epoch check at the DB layer protects against stale messages. `runnerWG.Wait()` is also removed — runners are no longer generation-scoped.

**What happens to buffered messages from an abandoned runner?** When `RegisterResponse.cancel_task_ids` arrives and the agent calls `Abandon`, any messages for that task already in `sendCh` are not drained. They flush out on the new stream, tagged with the old epoch, and the coordinator drops them at the DB layer (epoch-guarded WHERE clause returns 0 rows for logs; `GetTask`-then-compare returns early for status updates). Wasteful but correct, and bounded by `sendCh`'s capacity of 64 per agent.

### RegisterRequest includes running tasks

```go
a.mu.Lock()
running := make([]*relayv1.RunningTask, 0, len(a.runners))
for _, r := range a.runners {
    running = append(running, &relayv1.RunningTask{
        TaskId: r.taskID, Epoch: r.epoch,
    })
}
a.mu.Unlock()

stream.Send(&relayv1.AgentMessage{
    Payload: &relayv1.AgentMessage_Register{
        Register: &relayv1.RegisterRequest{
            // ... existing capability fields ...
            RunningTasks: running,
        },
    },
})
```

### Handle RegisterResponse cancel_task_ids

```go
for _, tid := range reg.CancelTaskIds {
    a.mu.Lock()
    r, ok := a.runners[tid]
    a.mu.Unlock()
    if ok {
        r.Abandon()
    }
}
```

### Runner additions

```go
type Runner struct {
    // ... existing ...
    epoch     int64
    abandoned atomic.Bool
}

// Abandon is like Cancel but suppresses the final status send.
// Used when the coordinator says "we reassigned this to another worker."
func (r *Runner) Abandon() {
    r.abandoned.Store(true)
    r.cancel()
}

func (r *Runner) sendFinalStatus(status relayv1.TaskStatus, exitCode *int32) {
    if r.abandoned.Load() {
        return
    }
    // ... existing send logic, now includes r.epoch ...
}
```

## Failure mode analysis

| Scenario | Outcome |
|---|---|
| Agent's network blips for < grace window | Tasks continue running. Agent reconnects, reports tasks at their epochs, server confirms. No user-visible impact beyond a few seconds of log-buffering. |
| Agent's host is disconnected for > grace window | Grace timer fires → `RequeueWorkerTasks`. Tasks go back to `pending`, re-dispatched to other workers (new epoch). When original agent comes back, its `RunningTasks` entries have stale epochs → all appear in `cancel_task_ids` → agent abandons (no final status). Original runners are killed; new runners on new workers complete. Wasted CPU during the overlap is bounded by the grace window. |
| Coordinator crashes and restarts | No blanket requeue. Startup enumerates workers with active tasks, starts grace timers for each. Agents reconnect (their own backoff is seconds, < window), reconcile normally. Tasks keep running. |
| Zombie final status arrives for a reassigned task | `UpdateTaskStatus ... WHERE assignment_epoch = $epoch` matches 0 rows. Silent drop at the DB layer. |
| Agent claims to be running a task the coordinator has no record of | Appears in `cancel_task_ids` (via the `!ok` branch). Agent abandons. |
| Coordinator has a task assigned to a worker, but the reconnecting agent didn't report it | Requeued via `RequeueTaskByID`. Agent clearly lost state (crash-restart of agent process mid-reconnect, etc.). |
| Task-row change happens without NOTIFY firing (bug, or direct DB mutation by an operator) | Dispatch still wakes within 30s from the polling ticker. |

## Testing strategy

### Unit tests (no DB)

- **`internal/worker/grace_test.go`** (new) — timer idempotency, cancellation, stop semantics; race-safe under `-race`.
- **`internal/worker/reconcile_test.go`** (new) — diff logic: matching → no action; mismatched epoch → cancel; agent-only → cancel; server-only → requeue.
- **`internal/agent/runner_test.go`** (extend) — `Abandon()` kills subprocess without final-status send; runner survives `connCtx` cancellation.

### Integration tests (testcontainers postgres)

- **`internal/store/queries_epoch_test.go`** (new) — `ClaimTaskForWorker` bumps epoch; `UpdateTaskStatus` with stale epoch affects 0 rows; `RequeueTask` preserves epoch until next claim.
- **`internal/worker/handler_reconnect_test.go`** (new, marquee) — dispatch a long-running task, close stream, reconnect within window, verify no cancel + task keeps running + final `done` persisted.
- **`internal/worker/handler_reassign_test.go`** (new) — reconnect after grace expires, verify task was requeued, new worker took it at epoch+1, old agent's reconnect produces `cancel_task_ids` entry.
- **`cmd/relay-server/startup_reconcile_test.go`** (new) — seed DB with running task, start server, verify task is still running (not blanket-requeued); agent reconnect reconciles cleanly. Also: no reconnect within grace → task requeued.
- **`internal/scheduler/notify_test.go`** (new) — `pg_notify` triggers dispatcher; connection loss → reconnect; subsequent notifications still wake dispatcher.
- **`internal/scheduler/dispatch_test.go`** (extend) — 10 workers, 50 tasks, assert `CountActiveTasksByAllWorkers` called once per cycle; within-cycle map updates respect `MaxSlots`.

### Tests that need updating

- Any existing test constructing `DispatchTask`, `TaskStatusUpdate`, `TaskLogChunk` → add epoch field.
- `handler_test.go` disconnect test → expect grace timer started, not immediate requeue.
- Any test calling `RequeueAllActiveTasks` at startup → remove expectation.

### Not tested

- The 30s poll fallback firing (would require ticker injection; covered implicitly by existing polling-era tests).
- pgxpool sizing (trivial config; covered by runtime behavior).

## Rollout

1. Merge migration + sqlc regeneration first (additive column, non-breaking).
2. Merge proto additions (additive fields, non-breaking for gRPC but old agents won't send `running_tasks` — coordinator treats them as "no tasks running," falling back to requeue-after-grace. This is the same as today's behavior.).
3. Merge server-side grace registry + reconcile + startup changes.
4. Merge agent-side runner lifetime + reconnect-report + abandon handling.
5. Merge LISTEN/NOTIFY infrastructure and firing sites.
6. Merge N×M query + pool sizing.

Each step should leave the system in a shipping state. Steps 1–2 are prerequisites for 3–4. Steps 5 and 6 are independent and can land in any order relative to 3–4.

## Configuration reference

| Env var | Default | Description |
|---|---|---|
| `RELAY_WORKER_GRACE_WINDOW` | `2m` | How long the coordinator waits for a disconnected/absent worker to reconnect before requeueing its tasks. Accepts Go duration format. |
| `RELAY_DB_MAX_CONNS` | `25` | `pgxpool.MaxConns`. One connection is dedicated to the NOTIFY listener. |

## Open questions

None at spec-writing time. Proto field numbers and migration number are determined at implementation time based on the current state of those files.
