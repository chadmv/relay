# Dispatcher & Lifecycle Correctness Pass — Design

**Date:** 2026-04-26
**Scope:** Three correctness bugs surfaced from prior retros, fixed independently and shipped as three separate PRs in the order below.

## Background

Three bugs share a theme — *state correctness across restarts and reconnections* — but live in different subsystems:

1. **NotifyListener gap** ([internal/scheduler/notify.go](internal/scheduler/notify.go)). One pool connection holds `LISTEN`. If it drops, the listener reconnects with exponential backoff. Task NOTIFYs fired during the gap are missed; only the 30s safety-net poll covers them. Source: 2026-04-22 major-concurrency-fixes retro — Known Limitations.
2. **Grace window per-process** ([internal/worker/grace.go](internal/worker/grace.go), [cmd/relay-server/main.go:216](cmd/relay-server/main.go:216)). On server restart, `seedGraceTimersFromActiveTasks` always starts a fresh full-window timer for every worker with active tasks. A worker that was 1m55s into its grace before the crash gets a fresh 2m after restart — up to ~2× the configured window, indefinitely if the server crashloops. Source: 2026-04-22 major-concurrency-fixes retro — Known Limitations.
3. **Sweeper independent Registry** ([internal/agent/source/perforce/sweeper.go:47](internal/agent/source/perforce/sweeper.go:47)). The agent-side workspace sweeper calls `LoadRegistry(...)` fresh on every pass while `Provider` keeps its own cached `p.reg`. Consistency relies on `OnEvictedCB → InvalidateWorkspace` nilling out `p.reg`. The retro flagged a "read-then-overwrite race window" that the cleanup callback only papers over. Source: 2026-04-25 perforce-workspace-management retro — Known Limitations.

## Approach Summary

- **Bug 1:** Trigger-on-LISTEN. Minimal change.
- **Bug 2:** Persist `disconnected_at` on `workers`; on startup, compute remaining grace from it. Thorough change with one new column.
- **Bug 3:** Share `*Registry` between `Provider` and `Sweeper`; eliminate the per-pass reload. Thorough refactor as the retro suggested.

PR order: 1 → 3 → 2 (smallest first; the DB-migration change last).

---

## Bug 1 — NotifyListener trigger on (re)connect

### Change

[internal/scheduler/notify.go](internal/scheduler/notify.go), inside `session()`, after both `LISTEN` statements succeed and before entering `WaitForNotification`: call `n.trigger()` once.

```go
if _, err := raw.Exec(ctx, "LISTEN relay_task_completed"); err != nil {
    return err
}
n.trigger() // drain anything submitted during reconnect gap or before startup
for {
    _, err := raw.WaitForNotification(ctx)
    ...
}
```

### Rationale

Every entry into the loop represents a fresh connection — initial startup or post-backoff reconnect. A single `trigger()` at that boundary closes the missed-NOTIFY window with no new state and no extra connections. The dispatcher's `Trigger()` is a non-blocking signal to run a poll cycle; spurious extra invocations are harmless.

Alternatives considered:
- Redundant listeners on independent connections: doubles connection cost, adds idempotency considerations, no behavioral benefit over trigger-on-LISTEN.
- Adaptive safety-net poll (faster while disconnected): more moving parts; trigger-on-LISTEN already closes the window.

### Tests

New unit test in `internal/scheduler/notify_test.go`:
- Start a `NotifyListener` against a testcontainers Postgres with a counting trigger callback.
- Assert the trigger is invoked at least once after `Run` starts, even with no `pg_notify` sent.
- Force a connection drop (e.g. by closing the underlying `*pgxpool.Conn` or running `pg_terminate_backend` on the listener PID), wait for reconnect, assert the counter increments again.

### Risk

The dispatcher's `Trigger()` is idempotent and signal-style — extra cycles cost a single poll round-trip. No new failure modes.

---

## Bug 2 — Persist worker disconnect time across server restarts

### Schema

New migration `internal/store/migrations/000009_workers_disconnected_at.up.sql` (next sequential number after `000008_task_commands`):

```sql
ALTER TABLE workers ADD COLUMN disconnected_at TIMESTAMPTZ NULL;
```

Down migration drops the column. No partial index needed; the column is read by a single startup query that already filters on active tasks.

We considered reusing `last_seen_at`, which today is updated only on connect and disconnect, so it currently equals disconnect-time when `status='offline'`. Rejected because the semantics are coincidental — any future heartbeat code that updates `last_seen_at` would silently break grace correctness. A dedicated column is unambiguous.

### SQL changes

In `internal/store/query/workers.sql`:

- Modify `UpdateWorkerStatus` to also write `disconnected_at`:
  ```sql
  -- name: UpdateWorkerStatus :one
  UPDATE workers
  SET status = $2, last_seen_at = $3, disconnected_at = $4
  WHERE id = $1
  RETURNING *;
  ```
  Callers pass `disconnected_at = now()` on the offline path and `NULL` on the online path. Single round-trip per state change.

- Replace `ListWorkersWithActiveTasks` in `internal/store/query/tasks.sql` with `ListGraceCandidates :many` returning `(id, disconnected_at)` for each worker that has at least one non-terminal task. The existing query has a single production caller (`cmd/relay-server/main.go`) and one store test (`store_test.go`); both migrate to the new shape.

After editing the `.sql` files, run `make generate` to regenerate `tasks.sql.go` / `workers.sql.go` / `models.go`.

### Handler changes

[internal/worker/handler.go:221](internal/worker/handler.go:221) (`finishRegister`, online path):
- Pass `DisconnectedAt: pgtype.Timestamptz{Valid: false}` to `UpdateWorkerStatus`.

[internal/worker/handler.go:442](internal/worker/handler.go:442) (`markWorkerOffline`, offline path):
- Pass `DisconnectedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}` to `UpdateWorkerStatus`.

These are paired with the existing `grace.Cancel` (line 234) and `grace.Start` (line 84) calls. No ordering changes.

### GraceRegistry change

[internal/worker/grace.go](internal/worker/grace.go) gains:

```go
// StartWithDuration is like Start but uses the given duration instead of g.window.
// Used by startup reconciliation to honor remaining grace from a persisted
// disconnect time. Panics on d <= 0; callers handle the expired case directly.
func (g *GraceRegistry) StartWithDuration(workerID string, d time.Duration) {
    // existing Start body, parameterized on d
}

func (g *GraceRegistry) Start(workerID string) {
    g.StartWithDuration(workerID, g.window)
}
```

`d <= 0` is a programmer error: the caller must inspect `remaining` and either call `StartWithDuration` (positive) or invoke the expiry path directly (non-positive).

### Startup reconciliation

[cmd/relay-server/main.go:216](cmd/relay-server/main.go:216) (`seedGraceTimersFromActiveTasks`):

```go
candidates, err := q.ListGraceCandidates(ctx)
if err != nil { return err }
for _, c := range candidates {
    id := uuidStrMain(c.ID)
    if !c.DisconnectedAt.Valid {
        // status was 'online' at crash; full window
        grace.Start(id)
        continue
    }
    remaining := c.DisconnectedAt.Time.Add(graceWindow).Sub(time.Now())
    if remaining > 0 {
        grace.StartWithDuration(id, remaining)
    } else {
        // grace already elapsed during downtime — fire synchronously
        grace.ExpireNow(id)
    }
}
```

`GraceRegistry` gains `func (g *GraceRegistry) ExpireNow(workerID string)`, which invokes the configured `onExpire` callback inline without scheduling a timer. Same lifecycle guards as the timer path: no fire after `Stop`; cancels and replaces any pending timer for the same `workerID` to preserve the ABA-safety invariant from `Start`.

### Tests

`internal/worker/grace_test.go`:
- `TestGraceRegistry_StartWithDuration` — uses a 10ms duration, asserts firing within 50ms (Eventually).
- `TestGraceRegistry_ExpireNow` — fires synchronously, no timer scheduled.
- `TestGraceRegistry_ExpireNowAfterStop` — no-op after Stop.

`cmd/relay-server/startup_reconcile_test.go` (existing): extend with three integration cases:
- Worker with active tasks and `disconnected_at = NULL` → full window.
- Worker with active tasks and `disconnected_at = now-30s`, grace_window 60s → ~30s remaining.
- Worker with active tasks and `disconnected_at = now-90s`, grace_window 60s → fires immediately, tasks requeued.

`internal/worker/handler_test.go`: extend the existing register/disconnect paths to assert `disconnected_at` is written on offline and cleared on online.

### Risk / edge cases

- **Clock issues:** all writes and reads happen on the server's wall clock. Cross-machine skew only matters if the DB is migrated across hosts mid-flight — out of scope.
- **Stale `disconnected_at` on workers without active tasks:** `ListGraceCandidates` filters on active tasks, so workers with no requeueable work are skipped at startup. The column is left set; harmless and useful for observability.
- **Race between startup-seeded timer and a fast reconnect:** `finishRegister` calls `grace.Cancel(workerID)` and clears `disconnected_at` in the same path. Cancel handles the still-pending timer; the column write reflects the new state. No new ordering risk.
- **Crashloop pathology (the bug's worst case):** with this fix, each restart preserves `disconnected_at`, so `remaining` decreases monotonically. A worker stuck in grace will eventually fire its expiry across restarts.

---

## Bug 3 — Sweeper shares Provider's Registry

### Provider change

[internal/agent/source/perforce/perforce.go](internal/agent/source/perforce/perforce.go):

- New exported method `Provider.Registry() (*Registry, error)` — wraps `loadRegistry()`. Documented as "the single shared registry instance for this provider; safe for concurrent use via its internal lock."
- `InvalidateWorkspace` keeps `delete(p.workspaces, shortID)` (per-task cache) but **drops** `p.reg = nil`. The sweeper now mutates `p.reg` directly via `reg.Remove` / `reg.Save`, so the provider's `ListInventory` / `Prepare` paths see fresh state without a reload.

### Sweeper change

[internal/agent/source/perforce/sweeper.go](internal/agent/source/perforce/sweeper.go):

- Add field `Reg *Registry`. Keep struct-literal construction to match the existing style.
- `SweepOnce` drops the `LoadRegistry(filepath.Join(s.Root, ".relay-registry.json"))` call at line 47 and uses `s.Reg` directly. Returns an error early if `s.Reg == nil` (programmer error, surfaces in tests immediately).
- `Registry`'s internal `mu` continues to serialize concurrent reads/writes.

### Wiring

[cmd/relay-agent/main.go:77](cmd/relay-agent/main.go:77):

```go
reg, err := pp.Registry()
if err != nil {
    log.Fatalf("workspace registry: %v", err)
}
sw := &perforce.Sweeper{
    Root:        root,
    MaxAge:      maxAge,
    MinFreeGB:   minFreeGB,
    SweepInterval: sweepInterval,
    Client:      pp.Client(),
    Reg:         reg,
    ListLocked:  pp.LockedShortIDs,
    FreeDiskGB:  freeDiskGB,
    OnEvictedCB: pp.InvalidateWorkspace,
}
```

The `OnEvictedCB` stays — it still serves the per-task workspace cache deletion. Only the registry-coherence dependency is removed.

### Tests

- `sweeper_test.go`: existing tests construct their own `Registry` via files. Adapt to pass `Reg: reg` into the `Sweeper` literal. Behavior unchanged.
- New integration-style test (likely `perforce_test.go` or a new `provider_sweeper_test.go`): construct a `Provider`, call `Provider.Registry()`, build a `Sweeper` against the same `Reg`, call `SweepOnce` to evict an aged workspace, then call `Provider.ListInventory()` and assert the evicted entry is absent — proves coherence without relying on `OnEvictedCB`.
- `EvictWorkspace` (line 232) still constructs a one-shot Sweeper internally; either pass it `Reg: reg` for consistency or leave as-is (it operates on a freshly loaded reg today). For coherence with the rest of the change, pass it the shared `reg`.

### Risk

Mechanical refactor. The Sweeper's existing tests need a one-line construction change. Provider's lazy-load is preserved (`Registry()` still loads on first call); only the agent's `main` wiring forces it eager, which it has to do anyway to construct the sweeper.

---

## Out of scope

- Multi-server / HA dispatcher coordination. The grace fix uses a DB column, which is HA-friendly *as a side effect*, but no leader election or distributed timer reconciliation is in scope here.
- Heartbeat-based `last_seen_at` updates. Mentioned only as a reason to add a dedicated `disconnected_at` column rather than overload `last_seen_at`.
- Workspace registry schema or locking changes beyond the ownership move. The `Registry`'s existing `sync.Mutex` already covers concurrent readers/writers.

## Files Touched (Estimate)

| Bug | Files | Migration |
|---|---|---|
| 1 | `internal/scheduler/notify.go`, `internal/scheduler/notify_test.go` | none |
| 2 | `internal/store/migrations/000N_*.{up,down}.sql`, `internal/store/query/workers.sql` (regen → `*.sql.go`, `models.go`), `internal/worker/handler.go`, `internal/worker/grace.go`, `internal/worker/grace_test.go`, `internal/worker/handler_test.go`, `cmd/relay-server/main.go`, `cmd/relay-server/startup_reconcile_test.go` | yes (1 column add) |
| 3 | `internal/agent/source/perforce/perforce.go`, `internal/agent/source/perforce/sweeper.go`, `internal/agent/source/perforce/sweeper_test.go`, new `provider_sweeper_test.go` (or extend existing), `cmd/relay-agent/main.go` | none |
