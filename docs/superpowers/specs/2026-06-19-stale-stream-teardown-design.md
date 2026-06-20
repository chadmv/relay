---
date: 2026-06-19
topic: stale-stream-teardown
status: approved
backlog: bug-2026-06-10-stale-stream-teardown-clobbers-registration
---

# Stale stream teardown clobbers a fresh registration - Design

## Problem

`Handler.Connect` (`internal/worker/handler.go:105-112`) installs four
unconditional teardown defers after a successful registration. On return they
run LIFO:

1. `h.registry.Unregister(workerID)`
2. `sender.Close()`
3. `h.markWorkerOffline(workerID)`
4. `h.grace.Start(workerID)` (or `h.requeueWorkerTasks(workerID)` when no grace
   registry is wired)

None of them checks whether *this* connection still owns the worker's registry
slot. When a half-open stream (e.g. a NAT-timed-out connection) finally returns
*after* the same agent has reconnected and re-registered a new sender, the stale
teardown:

- deletes the **new** sender from the registry (`Unregister` deletes by worker
  ID without checking which sender it removes),
- flips the **live** worker to `offline` in the DB,
- arms a grace timer the fresh connection already passed its chance to cancel
  (it called `grace.Cancel` during its own `finishRegister`, before this stale
  teardown runs).

When the grace window expires, `RequeueWorkerTasks` requeues tasks the agent is
actively running; another worker can then claim them, producing duplicate
execution.

This is a violation of the **Identity-checked teardown** invariant in
CLAUDE.md: "Connection cleanup must only tear down state it owns ... A stale
connection's defers must not clobber a fresh registration."

## Scope

This fix covers only the identity-checked teardown. The backlog proposal's third
bullet (configure `keepalive.ServerParameters`) is **already implemented** at
`cmd/relay-server/main.go:177-182` (Time 30s, Timeout 10s), which satisfies the
"half-open streams die quickly" goal. No keepalive change is in scope.

## Design

### 1. Identity-checked delete on the registry

Replace `Registry.Unregister` with an identity-checked variant in
`internal/worker/registry.go`:

```go
// UnregisterIf removes the worker's stream only if the currently registered
// sender is s. Returns true if it removed it (this caller still owned the
// slot); false if a newer connection has since replaced it.
func (r *Registry) UnregisterIf(workerID string, s Sender) bool {
    r.mu.Lock()
    defer r.mu.Unlock()
    if r.streams[workerID] != s {
        return false
    }
    delete(r.streams, workerID)
    return true
}
```

Pointer identity works because the registry stores `*workerSender` values behind
the `Sender` interface; interface comparison falls through to pointer equality.

`Unregister` is **replaced**, not kept alongside `UnregisterIf`: its only
production caller is the `Connect` defer being changed here, so leaving it would
be production-dead code. Its existing unit test is updated to drive
`UnregisterIf`.

### 2. Ownership-gated teardown

Extract the four defers into a single named method on `Handler` so the gating
logic is a directly testable unit, and replace the defer block in `Connect`
(lines 105-112) with `defer h.teardownConnection(workerID, sender)`.

```go
// teardownConnection runs when a Connect stream ends. It always stops this
// connection's own send goroutine, but only tears down shared worker state
// (DB offline flag, grace timer / requeue) when this connection still owns the
// worker's registry slot - a newer connection for the same worker must not be
// clobbered.
func (h *Handler) teardownConnection(workerID string, sender *workerSender) {
    owned := h.registry.UnregisterIf(workerID, sender)
    sender.Close() // always stop our own send goroutine
    if !owned {
        return // a newer connection owns this worker; leave shared state alone
    }
    h.markWorkerOffline(workerID)
    if h.grace != nil {
        h.grace.Start(workerID)
    } else {
        h.requeueWorkerTasks(workerID)
    }
}
```

Key properties:

- `sender.Close()` is **always** called: each connection owns its own send
  goroutine and must stop it regardless of registry ownership. Closing the local
  `sender` never touches the new connection's separate sender.
- `markWorkerOffline` + grace/requeue run **only** when `UnregisterIf` returned
  true. If a newer connection owns the slot, this teardown returns without
  touching shared DB state or timers.

## Testing

### Registry unit test (DB-free, `registry_test.go`)

Replace-then-stale-teardown:

1. Register sender A for `worker-1`, then register sender B (replaces A).
2. `UnregisterIf("worker-1", A)` returns `false`; B is still registered (a
   `Send` to `worker-1` reaches B).
3. `UnregisterIf("worker-1", B)` returns `true`; the slot is empty (a `Send`
   errors).

Update the existing `TestRegistry_Unregister` to use `UnregisterIf`.

### Handler regression test (integration, `//go:build integration`)

Proves the gate using the extracted `teardownConnection`:

1. Seed an online worker with a running task assigned to it.
2. Register a stale sender A, then a fresh sender B (replacing A); ensure the
   worker is marked online.
3. Call `h.teardownConnection(workerID, A)` directly (the stale teardown).
4. Assert:
   - the worker is **still online**,
   - sender B is **still registered** (a `Send` reaches B, not an error),
   - the running task is **not requeued** (still assigned to the worker, same
     `assignment_epoch`),
   - on the grace path, no grace timer fires against the live worker.

## Files

- `internal/worker/registry.go` - replace `Unregister` with `UnregisterIf`.
- `internal/worker/registry_test.go` - new replace-then-stale test; update the
  existing unregister test.
- `internal/worker/handler.go` - extract `teardownConnection`; replace the
  `Connect` defer block.
- `internal/worker/handler_test.go` (or a sibling integration test file) - new
  teardown regression test.

## Out of scope

- Keepalive configuration (already present).
- Any change to grace-window semantics, requeue queries, or the epoch fence.

## Backlog

Closes `docs/backlog/bug-2026-06-10-stale-stream-teardown-clobbers-registration.md`
(move to `docs/backlog/closed/` on completion).
