# Worker Disable - Design

Date: 2026-05-20

## Problem

An admin can currently take a worker out of service only by revoking its agent
token (`relay workers revoke`). Revocation is a one-shot kick: it nulls
`agent_token_hash`, sets `status = 'revoked'`, and forces the operator to issue a
fresh enrollment token before the worker can rejoin.

There is no way to temporarily park a worker - for example, during a GPU driver
upgrade or hardware diagnostics - while keeping its credentials intact so it can
be brought back with a single command.

## Goal

Add an admin-only ability to **disable** a worker. A disabled worker:

- Receives no new task dispatches from the scheduler.
- Keeps its agent token and its live gRPC connection.
- Keeps reporting telemetry/heartbeats.
- Can be **enabled** again instantly, with no re-enrollment and no reconnect.

Disable is orthogonal to revoke. The two can coexist on the same worker.

## Non-Goals

- No "draining status" lifecycle beyond the per-call drain/requeue choice
  described below.
- No automatic disable (e.g. on repeated task failures). Disable is a manual
  admin action only.
- No change to revoke/enrollment behavior.

## Data Model

### Migration `000012_workers_disabled_at`

```sql
-- 000012_workers_disabled_at.up.sql
ALTER TABLE workers ADD COLUMN disabled_at timestamptz;

-- 000012_workers_disabled_at.down.sql
ALTER TABLE workers DROP COLUMN disabled_at;
```

`disabled_at IS NULL` means the worker is enabled. A non-NULL value is both the
on/off flag and a record of when it was disabled. No CHECK constraint is needed.

The `disabled_at` column is **separate from and orthogonal to** the existing
`status` column. `status` continues to track liveness (`online`, `stale`,
`offline`, `revoked`) and is freely mutated by the connect/disconnect lifecycle
and the liveness sweeper. `disabled_at` is mutated **only** by the admin
disable/enable handlers. The two never collide, so a disabled worker stays
disabled across reconnects and sweeps with no extra code.

This mirrors the existing `disconnected_at` column, which is likewise a separate
nullable timestamp rather than an encoded `status` value.

### sqlc queries (`internal/store/query/workers.sql`)

New queries:

- `DisableWorker :execrows` -
  `UPDATE workers SET disabled_at = NOW() WHERE id = $1 AND disabled_at IS NULL`.
  The `disabled_at IS NULL` guard makes it idempotent; the affected-row count
  lets the handler distinguish "already disabled" (0 rows) from the action
  actually taking effect (1 row).
- `EnableWorker :execrows` -
  `UPDATE workers SET disabled_at = NULL WHERE id = $1`.
- `RequeueWorkerTasksWithEpoch :many` -
  `UPDATE tasks SET status='pending', worker_id=NULL, started_at=NULL,
  assignment_epoch = assignment_epoch + 1
  WHERE worker_id=$1 AND status IN ('dispatched','running') RETURNING id`.
  Used by requeue-mode disable (see below). Distinct from the existing
  `RequeueWorkerTasks` because it bumps `assignment_epoch` to fence stale status
  updates from the still-connected agent.

Unchanged: `CreateWorker`, `UpsertWorkerByHostname`, `UpdateWorker`,
`UpdateWorkerStatus`, `SetWorkerStatus`, `ClearWorkerAgentToken`. None of them
touch `disabled_at`.

`UpsertWorkerByHostname` has an explicit `RETURNING` column list - append
`disabled_at` to it. `SELECT *` / `RETURNING *` queries pick up the new column
automatically.

Run `make generate` after editing the `.sql` files.

## API Surface

Two new admin-only endpoints, chained after `BearerAuth` + `AdminOnly`, mirroring
the existing `DELETE /v1/workers/{id}/token` revoke route. Handlers
(`handleDisableWorker`, `handleEnableWorker`) live in `internal/api/workers.go`.

### `POST /v1/workers/{id}/disable`

Optional query param `?requeue=true` (default `false`).

- `false` (drain): set `disabled_at`, leave running tasks alone.
- `true` (requeue): set `disabled_at`, requeue active tasks, cancel them on the
  agent (see Scheduler Integration).

Responses:

- `200` with the updated `workerResponse`.
- `200` (no-op) if the worker is already disabled.
- `404` if the worker id is unknown.
- `400` on an invalid worker id.
- `403` if the caller is not an admin.

`POST` is used rather than `PATCH` because disable has a side effect (cancelling
tasks) beyond a field write, and it parallels the verb-style `DELETE .../token`
route.

### `POST /v1/workers/{id}/enable`

No query params. Clears `disabled_at` and wakes the dispatcher (via
`NotifyTaskSubmitted`, see Scheduler Integration) so a re-enabled worker becomes
usable immediately rather than waiting for the next 10s tick.

Responses: `200` with the updated `workerResponse`; `200` no-op if already
enabled; `404`/`400`/`403` as above.

### `workerResponse` changes (`internal/api/workers.go`)

- `Status` is **coalesced**: `toWorkerResponse` reports `"disabled"` when
  `disabled_at` is set, otherwise the live `status` value. `"disabled"` takes
  display precedence over `"revoked"` (the operator-actionable state is shown
  first), but both remain visible via the timestamp fields.
- New field: `DisabledAt *time.Time` with JSON tag `disabled_at,omitempty`.

Coalescing means existing API consumers that read only `status` see `"disabled"`
and naturally treat the worker as unavailable, with no client changes required.

## CLI Surface

Add `disable` and `enable` to the `workers` subcommand dispatch in
`internal/cli/workers.go` (currently `list|get|revoke|workspaces|evict-workspace`).
Update the `workersCmd` usage string and the `// Subcommands:` comment.

- `relay workers disable <id-or-hostname> [--requeue]`
  Resolves a hostname to an id using the same lookup path `revoke` uses, then
  `POST /v1/workers/{id}/disable` (with `?requeue=true` when `--requeue` is set).
  Prints `disabled.` in drain mode, or `disabled; N task(s) requeued.` in
  requeue mode.
- `relay workers enable <id-or-hostname>`
  `POST /v1/workers/{id}/enable`. Prints `enabled.`

The README REST API table and CLI reference get the two new rows.

## Scheduler Integration

### Dispatch exclusion

In `selectWorker` (`internal/scheduler/dispatch.go`), after the existing status
check:

```go
if w.Status != "online" && w.Status != "stale" {
    continue
}
if w.DisabledAt.Valid { // skip disabled workers
    continue
}
```

`ListWorkers` already does `SELECT *`, so `DisabledAt` is populated on the
`store.Worker` values the dispatcher iterates. Disabled workers also drop out of
any telemetry-based scoring for free.

### Drain mode (default)

The disable handler sets `disabled_at` and returns. Running/`dispatched` tasks on
the worker are untouched: the agent finishes them and reports completion through
the normal path (assignment epochs still match because nothing was reassigned).
The scheduler simply stops selecting the worker for new work. No dispatcher
trigger is needed.

### Requeue mode (`?requeue=true`)

The handler must both free the tasks and stop the live subprocesses:

1. In a single transaction:
   - `DisableWorker(id)` - set `disabled_at` **first**, so a dispatcher that
     wakes from the trigger in step 3 already sees the worker as disabled and
     will not hand the task straight back to it.
   - `RequeueWorkerTasksWithEpoch(id)` - requeue all `dispatched`/`running`
     tasks for the worker to `pending`, clearing `worker_id`/`started_at` and
     bumping `assignment_epoch`. Returns the affected task ids.
   - `NotifyTaskSubmitted(ctx)` - `SELECT pg_notify('relay_task_submitted', '')`.
     `pg_notify` inside a transaction is delivered on commit, which wakes the
     `NotifyListener` and triggers a dispatch cycle. This is the same mechanism
     `CreateJobFromSpec` uses to wake the dispatcher; the API server has no
     direct dispatcher reference.
2. Commit. Then, for each returned task id, send
   `CoordinatorMessage_CancelTask{TaskId, Force: false}` via
   `s.registry.Send(...)` - the same mechanism `handleCancelJob` uses. The agent
   kills the subprocess. Any late status update the agent sends for that task is
   rejected because `assignment_epoch` was bumped (the epoch fence used
   throughout the task lifecycle).

The response reports the number of requeued tasks.

`RequeueWorkerTasksWithEpoch` is intentionally separate from the existing
`RequeueWorkerTasks` (used by disconnect-driven grace requeue). Disconnect
requeue does not need an epoch bump because the agent is already gone and cannot
send a stale update. Disable-requeue keeps the agent connected, so the epoch
fence is required.

## Edge Cases & Error Handling

- **Unknown worker id** -> `404`. Handlers call `GetWorker` first, matching the
  codebase's 404-on-unknown pattern.
- **Disable an already-disabled worker** -> `200` no-op. `DisableWorker` returns
  0 rows (the `disabled_at IS NULL` guard); if the worker exists, return `200`
  with current state. Do not re-stamp `disabled_at` and do not re-cancel tasks.
- **Enable an already-enabled worker** -> `200` no-op.
- **Invalid UUID / unresolvable hostname** -> `400`.
- **Disable + revoke coexist.** They are independent columns. The coalesced
  `status` shows `"disabled"` first; `disabled_at` remains in the JSON so both
  states are visible. Re-enrollment clears `revoked` but **not** `disabled_at` -
  a re-enrolled worker that was disabled stays disabled. Documented in the
  README.
- **Liveness sweeper** (`internal/metrics/sweep.go`, `ListWorkersByLiveness`) is
  unaffected: it keys off `status IN ('online','stale')`, which disable never
  changes. A disabled worker still transitions `online <-> stale` underneath;
  the API just displays `"disabled"`.
- **Disconnect while disabled.** Agent drops -> `UpdateWorkerStatus` sets
  `status='offline'`, `disabled_at` untouched. Grace/requeue of its tasks
  proceeds normally. On reconnect the worker is `online` again but still
  disabled.

## Testing

- `internal/store/workers_disabled_test.go` (integration) -
  `DisableWorker`/`EnableWorker` round-trip; `DisableWorker` returns 0 rows when
  already disabled; `RequeueWorkerTasksWithEpoch` bumps `assignment_epoch` and
  returns affected task ids.
- `internal/scheduler/select_worker_test.go` - add
  `TestSelectWorker_DisabledWorkerIsNotEligible` (unit, no Docker; the
  `baseWorker` helper sets `DisabledAt`).
- `internal/api` (integration) - disable in drain mode leaves running tasks
  alone; disable in requeue mode requeues tasks, sends `CancelTask`, and bumps
  the epoch so a stale status update is rejected; enable clears state and the
  worker becomes dispatch-eligible again; `404` on unknown id; idempotent
  double-disable and double-enable; admin-only enforcement (non-admin -> `403`).
- `internal/cli/workers_disable_test.go` - `disable`, `enable`, and `--requeue`
  hit the correct endpoints; hostname resolution; output strings.

## Files Touched

- `internal/store/migrations/000012_workers_disabled_at.{up,down}.sql` (new)
- `internal/store/query/workers.sql` - new queries, `UpsertWorkerByHostname`
  return list
- `internal/store/query/tasks.sql` - `RequeueWorkerTasksWithEpoch`
- Regenerated: `internal/store/*.sql.go`, `internal/store/models.go`
- `internal/api/workers.go` - handlers, `workerResponse`, `toWorkerResponse`
- `internal/api/server.go` - two new routes registered near the existing
  `DELETE /v1/workers/{id}/token` route (line ~124), both wrapped in
  `auth(admin(...))`
- `internal/scheduler/dispatch.go` - dispatch exclusion
- `internal/cli/workers.go` - `disable`/`enable` subcommands
- Tests as listed above
- `README.md` - REST API table, CLI reference, revoke-vs-disable note
