---
date: 2026-06-20
topic: finishregister-gap-connection-epoch
status: approved
backlog: bug-2026-06-19-finishregister-gap-connection-epoch-race
classification: backend-only
run: autonomous (in-pattern decisions made by PM; each logged with a one-line rationale)
---

# Worker connection-epoch fence closes the finishRegister teardown gap - Design

## Classification

Backend-only. No frontend (React/Vite SPA) surface is touched: no REST shape
change, no SSE payload change, no CLI change. The fix lives entirely in
`internal/store/migrations`, `internal/store/query`, and `internal/worker`.

## Problem

The predecessor fix (`bug-2026-06-10-stale-stream-teardown-clobbers-registration`,
spec `2026-06-19-stale-stream-teardown-design.md`) added an identity-checked
teardown: `Registry.UnregisterIf` gates `markWorkerOffline` / `grace.Start` on
whether this connection still owns the registry slot. That closes the common
case but leaves a narrow residual race because the gate's **check**
(`UnregisterIf`) and its **action** (`markWorkerOffline` / `grace.Start`) are not
atomic with respect to a fresh registration.

### Exact current ordering (verified against source 2026-06-20)

`finishRegister` (`internal/worker/handler.go:293-351`) runs in this order:

1. `UpdateWorkerStatus(... "online" ...)` writes `status=online` -
   handler.go:294-301.
2. `h.grace.Cancel(workerID)` stops any pending requeue timer - handler.go:306-308.
3. `reconcileRunningTasks` - handler.go:311.
4. `applyInventory` - handler.go:317.
5. `stream.Send(RegisterResponse)` on the raw stream - handler.go:323-333.
6. `sender := NewWorkerSender(stream)` then `h.registry.Register(workerID, sender)`
   - handler.go:336-337. **Registry ownership is established LAST.**
7. metrics activate / broker publish / triggerDispatch - handler.go:339-348.

`teardownConnection` (handler.go:541-553) is the stale path:

```
owned := h.registry.UnregisterIf(workerID, sender)  // CHECK
sender.Close()
if !owned { return }
h.markWorkerOffline(workerID)                        // ACTION (DB write)
if h.grace != nil { h.grace.Start(workerID) }        // ACTION (in-memory timer)
else { h.requeueWorkerTasks(workerID) }
```

(Backlog line numbers 301/314/344 have drifted to 294/307/337; the ordering it
describes is intact.)

### The interleaving that strands a live worker

1. Agent's old stream half-open; agent reconnects on fresh connection F.
2. F runs steps 1-2 above: writes `status=online`, calls `grace.Cancel`. F has
   NOT yet reached step 6 (`registry.Register`).
3. The old stream's `Recv` errors. Stale teardown S runs `UnregisterIf(workerID,
   senderS)`. Because F has not registered yet, **S is still the registered
   sender**, so `UnregisterIf` returns `owned=true` - S passes the ownership gate.
4. S then runs `markWorkerOffline` (DB -> offline) and `grace.Start` (arms a
   requeue timer) AFTER F's online-write and grace.Cancel.
5. F completes step 6+: registers its sender. Final state: registry is correct
   (F owns the slot), but the DB says `offline` and a grace timer is armed
   against a live worker. When the timer fires, `RequeueWorkerTasks` requeues the
   tasks the agent is actively running -> duplicate execution.

Merely moving `registry.Register` earlier does NOT fix this: even if F registers
before S's `UnregisterIf`, S's `markWorkerOffline`/`grace.Start` are separate
statements that can still land after F's online/cancel under a different
interleaving. The CHECK and ACTION must be made atomic against the fresh
registration. The project already has a tool for exactly this shape: an epoch
fence enforced by the database.

### Why the task-level epoch does not already cover this

`RequeueWorkerTasks` filters `WHERE worker_id = $1 AND status IN
('dispatched','running')` and bumps `assignment_epoch`. After F reconnects,
`reconcileRunningTasks` only requeues tasks the agent did NOT report; the tasks
the agent IS actively running stay assigned to the same `worker_id` at the same
epoch. So a stale grace timer firing after F's reconnect still matches those
live tasks and requeues them. The task-level `assignment_epoch` fences stale
*status updates from the agent*; it does not fence a stale *teardown decision*.
The fence must therefore sit on the connection lifecycle, not the task.

## Design

Mirror the task `assignment_epoch` fence at the worker-connection level. Add a
monotonic `connection_epoch` to `workers`; every successful `finishRegister`
bumps it and records the new value on the in-memory connection. The teardown
path carries the epoch it owns and fences every shared-state write on it via
conditional SQL, so a stale connection's writes no-op the moment a newer
connection has registered.

> Decision: connection-epoch fence (not a mutex/lock spanning register+teardown).
> Rationale: in-pattern with the project's "Epoch fence" invariant, DB-enforced,
> survives process restarts, and avoids holding a lock across DB round-trips
> inside `finishRegister`.

### 1. Migration `000016_workers_connection_epoch`

`up`:

```sql
ALTER TABLE workers ADD COLUMN connection_epoch INT NOT NULL DEFAULT 0;
```

`down`:

```sql
ALTER TABLE workers DROP COLUMN connection_epoch;
```

> Decision: `INT NOT NULL DEFAULT 0`, mirroring `000004_assignment_epoch`.
> Rationale: same monotonic-counter semantics; `NOT NULL` removes nil handling in
> Go; existing rows default to 0 and the first `finishRegister` bumps to 1, so no
> stale connection can ever hold the epoch a fresh one just allocated. The down
> migration drops the column cleanly; it is additive with no data dependency.

### 2. New / changed sqlc queries (`internal/store/query/workers.sql`)

**a. Bump-and-return on register.** A dedicated query so the bump is a single
atomic statement that returns the new epoch:

```sql
-- name: RegisterWorkerConnection :one
-- Marks the worker online and atomically allocates a fresh connection_epoch for
-- this connection. The returned connection_epoch is the value this connection
-- owns; all later teardown writes for this connection fence on it.
UPDATE workers
SET status = 'online',
    last_seen_at = $2,
    disconnected_at = NULL,
    connection_epoch = connection_epoch + 1
WHERE id = $1
RETURNING *;
```

> Decision: replace the `UpdateWorkerStatus(... "online" ...)` call in
> `finishRegister` with this query; keep `UpdateWorkerStatus` unchanged for the
> offline path. Rationale: the bump must happen exactly where the worker comes
> online, atomically with it, and only on the success path. `UpdateWorkerStatus`
> is still used by `markWorkerOffline`, the liveness sweeper writes a separate
> `SetWorkerStatus`, so widening the shared query would entangle unrelated
> callers. Also clears `disconnected_at` on (re)connect, which the prior
> online-write did not - harmless and slightly more correct (a reconnected worker
> has no live disconnect timestamp).

**b. Epoch-fenced offline.** The offline write no-ops if a newer connection has
since registered:

```sql
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

> Decision: `:execrows` so the Go caller can tell whether the fence held (rows
> == 1) or was superseded (rows == 0) and skip the offline broker publish /
> metrics-clear and the grace/requeue when it was a no-op. Rationale: the broker
> `{"status":"offline"}` event and `Metrics.Clear` are in-memory side effects
> that must also be fenced; gating them on the rowcount keeps them consistent
> with the DB.

**c. Epoch-fenced requeue.** The requeue at teardown/grace-fire time is gated on
the connection epoch, so a stale teardown (or a stale grace timer firing later)
cannot requeue a live worker's tasks:

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

> Decision: add an epoch-fenced sibling rather than changing `RequeueWorkerTasks`.
> Rationale: `RequeueWorkerTasks` is also called by the worker-disable path
> (`DisableWorker`), which legitimately requeues regardless of connection epoch
> (an admin disabling a worker should requeue its tasks even though the agent is
> still connected at the current epoch). Only the disconnect/grace path needs the
> fence, so it gets its own query and the disable path is untouched. Both still
> bump `assignment_epoch`, preserving the task-level epoch invariant.

### 3. Threading the epoch through the teardown path

`finishRegister` allocates and records the epoch before any other goroutine can
observe the connection:

1. Call `RegisterWorkerConnection` -> get `updated` including the new
   `connection_epoch` (call it `epoch`). This is the single point of allocation;
   only a successful `finishRegister` bumps. (Replaces the current
   `UpdateWorkerStatus("online")` at handler.go:294.)
2. `h.grace.Cancel(workerID)` (unchanged ordering; still cancels any inherited
   timer).
3. reconcile / inventory / send RegisterResponse (unchanged).
4. `sender := NewWorkerSender(stream); sender.epoch = epoch` then
   `h.registry.Register(workerID, sender)`.
5. Return the sender (which now carries its epoch) so the `Connect` defer can
   pass it to teardown.

Carry the epoch on the connection. The `*workerSender` is the per-connection
object already passed to `teardownConnection`, so it is the natural carrier:

> Decision: store the owned `connection_epoch` as an unexported field on
> `*workerSender` (set once at construction in `finishRegister`, never mutated).
> Rationale: `teardownConnection(workerID, sender)` already receives the sender;
> threading the epoch on it needs no new parameter plumbing and the value is
> immutable for the life of the connection. Alternative (a parallel
> `map[workerID]epoch`) reintroduces exactly the shared-mutable-state hazard the
> epoch is meant to remove.

`teardownConnection` becomes epoch-fenced:

```go
func (h *Handler) teardownConnection(workerID string, sender *workerSender) {
    owned := h.registry.UnregisterIf(workerID, sender)
    sender.Close() // always stop our own send goroutine
    if !owned {
        return
    }
    epoch := sender.connEpoch
    if h.markWorkerOffline(workerID, epoch) == 0 {
        return // a fresher connection holds the epoch; leave grace/requeue alone
    }
    if h.grace != nil {
        h.grace.Start(workerID) // safe: see grace-fence reasoning below
    } else {
        h.requeueWorkerTasks(workerID, epoch)
    }
}
```

`markWorkerOffline` gains an `epoch int32` parameter and returns rows affected
(from `MarkWorkerOfflineIfEpoch`); it only publishes the offline event and clears
metrics when rows == 1. `requeueWorkerTasks` gains an `epoch int32` parameter and
calls `RequeueWorkerTasksIfEpoch`.

### Why this ordering closes the window and opens no new one

- **F makes its epoch authoritative atomically with coming online** (step 1,
  single `UPDATE ... connection_epoch + 1`). The instant F's bump commits, the
  worker row holds F's epoch and no other value.
- **S captured its epoch at S's own `finishRegister`**, which is strictly older
  than F's (epoch is monotonic; only a successful `finishRegister` bumps, and
  F's ran after S's). So `S.connEpoch < F.connEpoch`.
- Two interleavings of S's teardown vs F's register:
  - S's `MarkWorkerOfflineIfEpoch(S.epoch)` runs **before** F's bump commits:
    the row still holds S's epoch, so S's offline lands - but F's subsequent bump
    immediately overwrites it back to `online` with a newer epoch (F's
    `RegisterWorkerConnection` sets `status='online'` unconditionally). S's grace
    timer, if armed, will fire later and call `RequeueWorkerTasksIfEpoch(S.epoch)`,
    which now fails the EXISTS guard (row holds F's epoch) and requeues nothing.
    Net: transient offline flicker that F's online-write corrects; no task
    requeued. Acceptable.
  - S's `MarkWorkerOfflineIfEpoch(S.epoch)` runs **after** F's bump commits: the
    row holds F's epoch, S's fence fails, rows == 0, S returns immediately
    without touching grace/requeue. Net: no effect at all.
- **No new window:** S never bumps the epoch (only `finishRegister` does), so S
  cannot make its own stale epoch authoritative. The worst case is a brief
  offline flicker that a concurrent F online-write heals, and the
  task-requeue - the part that causes duplicate execution - is fenced in both
  interleavings.

### The grace path: fence at requeue (grace-fire), not at arm

`grace.Start` arms an in-memory `time.AfterFunc`; its callback (wired in
`cmd/relay-server/main.go:123-130`) calls `q.RequeueWorkerTasks(...)` then
`dispatcher.Trigger()`. There are two candidate fence points:

- **At arm time** (don't `grace.Start` if epoch is stale): partial. The
  `markWorkerOffline` rowcount gate already prevents arming when F's bump has
  committed before S's teardown. But it does NOT cover the interleaving where S
  arms the timer legitimately (its offline landed) and F reconnects during the
  2-minute window - the timer would then fire against a now-live worker.
- **At fire time** (requeue no-ops if a newer connection exists): complete. This
  is the interleaving the bug is actually about and also subsumes the normal
  "agent reconnects within grace window" case.

> Decision: fence at grace-FIRE time, by switching the grace `onExpire` callback
> in `cmd/relay-server/main.go` and the `requeueWorkerTasks` teardown helper to
> `RequeueWorkerTasksIfEpoch(workerID, epoch)`. The grace registry must therefore
> carry the epoch alongside the worker ID. Rationale: the requeue is the only
> grace side effect that causes duplicate execution; fencing it at the moment it
> would act is both necessary and sufficient. Arming is left unconditional (the
> `markWorkerOffline` rowcount already prevents the obviously-stale arm), keeping
> `GraceRegistry` semantics minimal.

> Decision: thread the epoch through `GraceRegistry`. `Start(workerID)` becomes
> `Start(workerID, epoch)`; the registry stores the epoch with each timer and
> passes it to `onExpire(workerID, epoch)`. `StartWithDuration` / `ExpireNow` /
> the startup seeding (`seedGraceTimersFromActiveTasks`) gain the epoch too.
> Rationale: the timer's eventual requeue needs the connection epoch that was
> live when the worker disconnected. For startup seeding, the epoch comes from
> the persisted `workers.connection_epoch` (add it to the `ListGraceCandidates`
> projection); a seeded timer requeues only if no reconnect has bumped the epoch
> since the crash, which is exactly correct.

> Decision: the grace `onExpire` signature changes to `func(workerID string,
> epoch int32)`. Rationale: required to pass the fenced epoch to
> `RequeueWorkerTasksIfEpoch`. This is a single call site in
> `cmd/relay-server/main.go`.

### 4. Enrollment vs reconnect vs auto-enroll

All three auth paths (`enrollAndRegister`, `reconnectAndRegister`,
`autoEnrollAndRegister`) funnel into `finishRegister` (handler.go:217, 235, 288).
The epoch bump lives in `finishRegister` (via `RegisterWorkerConnection`), so all
three get it for free with no per-path change.

> Decision: bump in the shared `finishRegister`, never in the per-path helpers.
> Rationale: matches where the existing online-write already lives; keeps a single
> allocation site so the "only a successful finishRegister bumps" property holds
> for every connection type.

## Testing

### Registry/grace unit tests (DB-free)

- `GraceRegistry`: extend `grace_test.go` for the new epoch-carrying signature -
  assert `onExpire` receives the epoch passed to `Start`, and that
  `StartWithDuration` / `ExpireNow` propagate it. No fence logic lives here (the
  fence is in SQL), so these only prove the epoch is plumbed through.

### Handler regression test (integration, `//go:build integration`)

Mirror `handler_teardown_test.go` (the predecessor's regression test) and extend
it to exercise the finishRegister-gap interleaving deterministically. The
existing export hooks (`RegisteredSenderForTest`, `TeardownConnectionForTest`,
`SendToWorkerForTest`, `UUIDStringForTest`) cover most of it; add a hook to set
the sender's owned epoch and to invoke `RegisterWorkerConnection` so the test can
drive distinct epochs.

New test `TestTeardownConnection_StaleEpochDoesNotRequeueLiveWorker`:

1. Seed user + job + task; create a worker; claim the task (epoch 0 -> 1,
   status `dispatched`, assigned to the worker).
2. Simulate the stale connection S: call `RegisterWorkerConnection` (worker
   `connection_epoch` 0 -> 1); register stale sender A carrying connEpoch 1.
3. Simulate the fresh reconnect F: call `RegisterWorkerConnection` again
   (`connection_epoch` 1 -> 2, status online); register fresh sender B carrying
   connEpoch 2, replacing A in the registry.
4. Run S's teardown: `TeardownConnectionForTest(workerID, A)`.
5. Assert:
   - `UnregisterIf` returned false in the common case OR, to reproduce the exact
     gap, register B in the registry AFTER calling teardown's `UnregisterIf` is
     not directly observable; instead drive the residual hazard at the SQL fence
     by giving A connEpoch 1 while the row holds 2. Assert
     `MarkWorkerOfflineIfEpoch(id, 1)` returns rows == 0 (fence held) and the
     worker stays `online`.
   - The running task is untouched: `assignment_epoch` still 1, status still
     `dispatched`, still assigned to the worker (the requeue fence held).
   - Sender B is still registered: a `Send` reaches B.

Add a second test `TestGraceExpiry_StaleEpochRequeueIsNoOp`:

1. Seed a worker at `connection_epoch` 1 with a claimed task.
2. Bump the worker to `connection_epoch` 2 (simulating a reconnect after the
   grace timer was armed at epoch 1).
3. Call the grace `onExpire`-equivalent requeue directly:
   `RequeueWorkerTasksIfEpoch(id, 1)`.
4. Assert it returns zero rows and the task is still `dispatched` at its original
   `assignment_epoch`.

And a positive control `TestGraceExpiry_CurrentEpochRequeues`:

1. Worker at `connection_epoch` 1 with a claimed task, no reconnect.
2. `RequeueWorkerTasksIfEpoch(id, 1)` requeues the task: status `pending`,
   `assignment_epoch` bumped, `worker_id` null.

> Decision: prove correctness primarily at the SQL fence (the rowcount of the
> `IfEpoch` queries), because that is where atomicity actually lives. The Go-level
> interleaving is genuinely racy to reproduce deterministically; asserting the
> fence queries no-op on a stale epoch and act on the current epoch covers every
> interleaving the handler can produce. Rationale: matches how the predecessor
> test proved its gate via `UnregisterIf`'s boolean rather than trying to win a
> real goroutine race.

## Files

- `internal/store/migrations/000016_workers_connection_epoch.up.sql` /
  `.down.sql` - new column.
- `internal/store/query/workers.sql` - add `RegisterWorkerConnection`,
  `MarkWorkerOfflineIfEpoch`.
- `internal/store/query/tasks.sql` - add `RequeueWorkerTasksIfEpoch`; add
  `connection_epoch` to the `ListGraceCandidates` projection.
- `make generate` regenerates `internal/store/*.sql.go` and `models.go` (do NOT
  hand-edit; follow the CLAUDE.md LF/CRLF cleanup note).
- `internal/worker/handler.go` - `finishRegister` calls
  `RegisterWorkerConnection` and stamps the sender's epoch; `teardownConnection`,
  `markWorkerOffline`, `requeueWorkerTasks` take and fence on the epoch.
- `internal/worker/sender.go` - add an immutable `connEpoch int32` field to
  `workerSender`.
- `internal/worker/grace.go` - thread `epoch int32` through `Start`,
  `StartWithDuration`, `ExpireNow`, and the `onExpire` callback signature.
- `cmd/relay-server/main.go` - grace `onExpire` and
  `seedGraceTimersFromActiveTasks` pass the epoch; the seed reads
  `connection_epoch` from the candidate row.
- `internal/worker/export_test.go` - add a hook to stamp/read a sender's
  `connEpoch` and to call `RegisterWorkerConnection` from `worker_test`.
- `internal/worker/grace_test.go` - epoch-propagation assertions.
- `internal/worker/handler_teardown_test.go` (or a sibling) - the two new
  epoch-fence regression tests plus the positive control.

## Invariant compliance

- **Epoch fence.** Extends the existing fence to the connection lifecycle; every
  teardown write (`MarkWorkerOfflineIfEpoch`, `RequeueWorkerTasksIfEpoch`) is
  conditional on `connection_epoch`. The task-level `assignment_epoch` bump is
  preserved in the requeue query.
- **Identity-checked teardown.** Strengthens it: the registry pointer-identity
  gate stays, and the DB-enforced epoch fence closes the residual non-atomic
  window the pointer gate could not.
- **Single job-spec pipeline / one bounded sender / no interior pointers across
  locks / single JSON entry point.** Untouched. The epoch rides on the existing
  per-connection `*workerSender` (set once, immutable), introducing no new shared
  mutable state and no new lock.

## Out of scope

- Any change to `RequeueWorkerTasks` (the disable path keeps its unconditional
  requeue).
- Keepalive configuration (already present, per the predecessor spec).
- Any frontend, REST, SSE, or CLI change.

## Backlog

Closes `docs/backlog/bug-2026-06-19-finishregister-gap-connection-epoch-race.md`
(move to `docs/backlog/closed/` on completion; the `git mv` is required scope,
not optional cleanup).
