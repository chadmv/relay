# Perforce Workspace Management — Design

**Status:** design, approved for planning
**Date:** 2026-04-24

## Problem

Relay workers need to sync or unshelve Perforce files before executing task commands. Depots are large (Unreal projects can exceed 1.5 TB), so workspaces must be **reused across jobs** rather than recreated per task. Concurrent tasks on the same worker must arbitrate access without corrupting each other's workspace state. Workers participate in a job farm that mixes tools with very different exclusivity requirements (Unreal — one at a time per worker; Maya exports — potentially concurrent).

The current relay dispatch pipeline assigns individual tasks to workers independently; there is no job→worker affinity and no workspace model. A naive implementation that sync'd 1.5 TB on every task would make this intractable.

## Goals

1. Workers prepare a local Perforce workspace (sync + optional unshelve) before running the task command.
2. Workspaces are **reused across jobs** keyed by Perforce stream. Warm workspaces are preferred by the scheduler.
3. Concurrent tasks on the same worker can share a workspace safely when their sync state is compatible; otherwise they serialize.
4. Workspace lifecycle (create, reuse, evict) is observable end-to-end — operators can see workspace inventory and sync progress.
5. The architecture supports a future git / svn / S3 provider without reshaping the data model.

## Non-Goals (v1)

- Non-Perforce providers. The interface supports them; only the Perforce implementation ships.
- Relay-managed Perforce credentials. Operators provision P4 tickets on worker machines out-of-band (imaging, config-management, manual `p4 login`). Relay assumes `p4` just works.
- Job-level task pinning to a single worker. Parallelism across a job's tasks is preserved.
- Distributed build artifact caching (FASTBuild/IncrediBuild-style). Unreal's own incremental build inside the warm workspace is sufficient.
- Multi-task bin-packing (deliberately co-locating tasks with identical sources on one worker).
- Workspace replication or pre-warming across workers.
- Superset additive concurrency (holder has `A/...`, request wants `A/... + B/...` — should coexist by extending sync). v1 serializes this case.

## Architecture

A **pluggable source-provider abstraction** on the agent. Tasks declare an optional `source` spec; the agent's provider runs a `Prepare` phase before the task command and a `Finalize` phase after. Perforce is the only v1 implementation. The server-side data model and protobuf are source-type-agnostic.

```
                                ┌──────────────────────────────┐
  relay-server (dispatcher)     │ worker_workspaces table      │
  prefers warm workers          │ (worker_id, source_type,     │
                         ─────▶ │  source_key, baseline_hash,  │
                                │  short_id, last_used_at)     │
                                └──────────────────────────────┘
           │
           │ Dispatch(task{source: {type: "perforce", sync: [...], unshelves: [...]}})
           ▼
  relay-agent
     │
     ├─ SourceProvider.Prepare(ctx, spec, progress)  ← status: PREPARING
     │     ├─ Resolve #head → @CL, compute baseline hash
     │     ├─ Acquire workspace by stream
     │     ├─ Create p4 client + workspace dir if missing
     │     ├─ Arbitrate lock (baseline-match / disjoint / exclusive)
     │     ├─ p4 sync <spec>  (logs streamed via progress callback)
     │     └─ p4 unshelve -s <cl> -c <per-task-pending-cl>
     │
     ├─ Runner.Run(task.command)   ← status: RUNNING
     │
     └─ SourceProvider.Finalize(ctx)
           ├─ p4 revert -c <per-task-pending-cl>
           ├─ p4 change -d <per-task-pending-cl>
           └─ release lock, update last_used_at, emit inventory update
```

**Boundary summary:**
- **Server** knows: per-worker workspace inventory, task `source` spec, task phase (`PREPARING` / `RUNNING`), failure class (`PREPARE_FAILED` vs `FAILED`).
- **Agent** knows: local workspace layout, RWLock state, sync-spec resolution, p4 invocation. Never sees P4 credentials.
- **Source provider interface** is a Go interface in `internal/agent/source/`; Perforce is one implementation. No P4 concepts leak into `relay-server`, the scheduler, or the generic protobuf task shape (which gains a `source` sub-message with a `oneof` for provider-specific config).

## Data Model

### New: `worker_workspaces` table

```sql
CREATE TABLE worker_workspaces (
  worker_id     UUID NOT NULL REFERENCES workers(id) ON DELETE CASCADE,
  source_type   TEXT NOT NULL,         -- 'perforce'
  source_key    TEXT NOT NULL,         -- canonical stream path for p4
  short_id      TEXT NOT NULL,         -- 6-char base32 hash; matches on-disk dir name
  baseline_hash TEXT NOT NULL,         -- SHA-256 of resolved sync spec + unshelves (truncated to 16 hex)
  last_used_at  TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (worker_id, source_type, source_key)
);
CREATE INDEX ON worker_workspaces (source_type, source_key, baseline_hash);
```

The secondary index backs the dispatcher's warm-lookup query.

### Modified: `tasks`

- New nullable column `source JSONB` — the source spec. Null = no workspace prep (existing tasks unchanged).
- `status` enum gains `'preparing'` and `'prepare_failed'`. Flow becomes `pending → assigned → preparing → running → done/failed/timed_out/prepare_failed`. Tasks without a `source` skip `preparing`.

### Protobuf (`proto/relayv1`)

Field numbers below use `N` where the concrete number must be the next free value in the existing `relayv1` `.proto` definitions at implementation time. New enum values must also be appended (not inserted) to preserve wire compatibility.

```proto
message DispatchTask {
  // ... existing fields ...
  SourceSpec source = N;
}

message SourceSpec {
  oneof provider {
    PerforceSource perforce = 1;
  }
}

message PerforceSource {
  string stream = 1;                      // e.g. "//streams/GameX/main"
  repeated SyncEntry sync = 2;
  repeated int64 unshelves = 3;
  bool workspace_exclusive = 4;
  optional string client_template = 5;    // passed to `p4 client -t <template>`
}

message SyncEntry {
  string path = 1;    // depot path, must be under PerforceSource.stream
  string rev  = 2;    // "#head", "@<cl>", "@<label>", "#<rev>"
}

enum TaskStatus {
  // ... existing values ...
  TASK_STATUS_PREPARING = N;       // append, do not renumber
  TASK_STATUS_PREPARE_FAILED = N;  // append, do not renumber
}

enum LogStream {
  // ... existing values ...
  LOG_STREAM_PREPARE = N;          // append, do not renumber
}

message WorkspaceInventoryUpdate {
  string source_type   = 1;
  string source_key    = 2;
  string short_id      = 3;
  string baseline_hash = 4;
  google.protobuf.Timestamp last_used_at = 5;
  bool deleted         = 6;   // true on eviction
}
```

`AgentMessage` gains a `WorkspaceInventoryUpdate` payload variant. `RegisterRequest` gains a `repeated WorkspaceInventoryUpdate inventory = N;` for full resync on reconnect.

### JobSpec (HTTP API)

`TaskSpec` (`internal/api/job_spec.go`) gains an optional field:

```json
"source": {
  "type": "perforce",
  "stream": "//streams/GameX/main",
  "sync": [
    {"path": "//streams/GameX/main/GameX/...", "rev": "#head"},
    {"path": "//depot/tools/...",              "rev": "@label-stable"}
  ],
  "unshelves": [12346],
  "workspace_exclusive": true,
  "client_template": null
}
```

`ValidateJobSpec` extensions when `source.type == "perforce"`:

- Require `stream` (must start with `//`).
- Require at least one `sync` entry.
- Each `sync.path` must start with `//` and be either `stream`, `stream + "/..."`, or start with `stream + "/"` (i.e. the stream itself or any depot path under it). Paths outside `stream` are rejected so a task can't accidentally pull files from an unrelated depot area via the stream client.
- Each `sync.rev` must match one of the supported forms (`#head`, `@<cl>`, `@<label>`, `#<rev>`).
- `unshelves` entries must be positive integers.
- `client_template`, if set, must match `[A-Za-z0-9_.-]+`.

## Agent Source-Provider Package

```
internal/agent/source/
  source.go         — Provider interface + Register/Get
  perforce/
    perforce.go     — PerforceProvider
    client.go       — p4 subprocess wrapper (uses `-ztag -G` for structured reads)
    workspace.go    — on-disk workspace + RWLock + arbitration
    registry.go     — .relay-registry.json reader/writer with file lock
    baseline.go     — sync-spec canonicalization + hashing
    sweeper.go      — eviction goroutine
```

### Interface

```go
package source

type Provider interface {
    Type() string
    Prepare(ctx context.Context, spec *relayv1.SourceSpec, progress func(line string)) (Handle, error)
}

type Handle interface {
    WorkingDir() string
    Env() map[string]string
    Finalize(ctx context.Context) error
    Inventory() InventoryEntry
}

type InventoryEntry struct {
    SourceType   string
    SourceKey    string
    ShortID      string
    BaselineHash string
    LastUsedAt   time.Time
}
```

Registration: `source.Register("perforce", perforce.New)` in an `init()`. A future git provider adds a sibling directory with no changes to `agent.go`.

### Runner integration (`internal/agent/agent.go`, `runner.go`)

On `DispatchTask`:

1. If `task.Source != nil`:
   - Emit `TASK_STATUS_PREPARING`.
   - Call `provider.Prepare(runCtx, task.Source, logFn)`. `logFn` sends each line as a `TaskLogChunk` with `stream = LOG_STREAM_PREPARE`, batched with a 500ms flush window.
   - On error: send `TASK_STATUS_PREPARE_FAILED`, skip the task command, return.
   - On success: runner sets `exec.Cmd.Dir = handle.WorkingDir()` and merges `handle.Env()` into the process env.
2. `defer handle.Finalize(runCtx)` — always runs, regardless of task outcome. Uses the agent lifetime context (not the connection context), consistent with the existing task-outlives-connection invariant from the major-concurrency-fixes work.
3. After Finalize, agent sends a `WorkspaceInventoryUpdate` to the server.

## Concurrency Model

Each workspace carries a list of `current_holders`, each with `{mode: exclusive|shared, baseline_hash, sync_paths, unshelves}`. A new request is arbitrated against the current holders:

1. **Identical baseline** — `request.baseline_hash == holder.baseline_hash` AND `request.unshelves == holder.unshelves` AND all holders are `shared` AND request is not `workspace_exclusive` → admit as shared, no sync.
2. **Disjoint additive** — no holder has unshelves, no holder is `workspace_exclusive`, request has no unshelves and is not `workspace_exclusive`, and no request `sync.path` prefix overlaps any holder's `sync.path` prefix → admit as shared; partial sync covers only the new paths.
3. **Otherwise** — wait on a per-workspace condition variable until `len(current_holders) == 0`, then admit as exclusive.

The actual `p4 sync` / `p4 unshelve` commands run under exclusive hold. Post-sync, if the request is not `workspace_exclusive` and declared no unshelves, the lock downgrades to shared before the task command starts so sibling shared tasks can run.

`#head` is resolved to an explicit CL at prepare time (via `p4 changes -m1 <path>`) so the baseline hash is stable across tasks submitted back-to-back against an unchanged depot.

## Workspace Layout and Naming

- **`RELAY_WORKSPACE_ROOT`** — required env var; operator picks a short path, e.g. `D:\rw\`. Documented recommendation: keep under 8 characters because of Windows MAX_PATH.
- **Per workspace:** `<root>/<short_id>/` where `short_id` is the first 6 characters of a base32-encoded SHA-256 of the stream path. If a collision with an existing workspace on this worker is detected at allocation time (different `source_key` but same `short_id`), extend the hash by 2 more characters and retry. That directory is the P4 client root. Typical resulting path depth: `D:\rw\a3f9c1\GameX\Intermediate\Build\...` — ~13 characters of relay overhead above the root.
- **Client spec name on server:** `relay_<sanitized_hostname>_<short_id>`, where `sanitized_hostname` is the agent's hostname with any character not in `[A-Za-z0-9-]` replaced by `_`, truncated to 32 characters. Predictable, greppable, avoids collisions across workers and is always a legal Perforce client name.
- **Registry:** single file `<root>\.relay-registry.json` listing all workspaces on this worker. Avoids adding any `.relay/` directory inside individual workspace trees (which would lengthen paths inside the workspace).

Per-workspace registry entry:

```json
{
  "short_id": "a3f9c1",
  "source_key": "//streams/GameX/main",
  "client_name": "relay_render-07_a3f9c1",
  "baseline_hash": "3b71...",
  "last_used_at": "2026-04-24T15:02:11Z",
  "open_task_changelists": [
    {"task_id": "<uuid>", "pending_cl": 91244}
  ],
  "dirty_delete": false
}
```

On agent startup: scan the registry, reconcile with `p4 clients -u <user> -e 'relay_<hostname>_*'` to detect drift (e.g. a client that was deleted server-side while the agent was down), and repair or mark workspaces as needing rebuild.

## Prepare Phase

```
 Prepare(spec):
   1. Resolve #head → @CL for each sync entry   (p4 changes -m1 <path>)
      Compute baseline hash                      (SHA-256 of sorted spec + unshelves)

   2. Look up workspace by source_key in the registry.
      If missing: allocate short_id, create <root>/<short_id>/,
                  create p4 client with View: derived from stream
                  (optionally `p4 client -t <client_template>`).

   3. Arbitrate lock per the three rules above.

   4. If lock is exclusive:
        Run crash-recovery pass (see below).
        p4 sync <spec>   (`-q --parallel=4`, stream lines via progress callback)
        If spec.unshelves is non-empty:
          p4 --field "Description=relay-task-<task_id>" change -o | p4 change -i
             → pending CL T
          For each shelved_cl in spec.unshelves:
              p4 unshelve -s <shelved_cl> -c T
          Record {task_id, pending_cl: T} in registry.
        Update baseline_hash and last_used_at in registry.

   5. If not workspace_exclusive and no unshelves:
        Downgrade lock to shared.

   6. Return Handle { WorkingDir, Env with P4CLIENT=<client_name>, Finalize, Inventory }.
```

### Unshelve Lifecycle

Unshelves use a dedicated per-task pending changelist so reverts are precisely scoped:

- **Prepare** creates a pending CL with description `relay-task-<task_id>`, captures the returned CL number `T`, and runs `p4 unshelve -s <shelved_cl> -c T` for each requested CL.
- **Finalize** runs `p4 revert -c T //...` and then `p4 change -d T`. Only files opened in `T` are reverted; concurrent tasks on the same workspace are untouched.
- **Crash recovery** (runs at the start of every exclusive prepare, before the sync): `p4 changes -u <p4user> -c <client> -s pending -L` and delete any pending CL whose description starts with `relay-task-`.

### Sync Progress Streaming

- `p4 sync -q --parallel=4` writes one line per file action. Agent batches lines with a 500ms flush window and emits one `TaskLogChunk { stream: LOG_STREAM_PREPARE, content: <batched lines> }` per flush.
- Optional pre-pass: `p4 sync -n -q --parallel=4 | wc -l` to compute file count, flushed as the first line (`syncing 12,048 files`). Skippable on very large specs.
- CLI and UI render `LOG_STREAM_PREPARE` chunks under a `[prepare]` prefix so they are visually separable from task stdout.

### Failure Taxonomy

All below → `TASK_STATUS_PREPARE_FAILED`:

- P4 server unreachable, auth failed, ticket expired. Task retries per its `Retries` count.
- Client-spec creation refused (e.g. bad stream name). Terminal for every retry.
- Disk full during sync. Agent triggers an eviction sweep before the next prepare.
- Unshelve conflict (depot path changed since the CL was shelved). Terminal for that task attempt.
- Registry / `p4 have` mismatch exceeding a threshold. Workspace marked for full rebuild; current task fails so the operator sees the event.

## Dispatcher Warm-Preference Scoring

Change location: [internal/scheduler/dispatch.go:101](internal/scheduler/dispatch.go:101) — `selectWorker`. Filters are unchanged (`online`, not reserved, label match, free slots); scoring is augmented.

### Inputs

- `task.Source` — read from the new `source` column.
- `warmByWorker map[WorkerID]WorkerWorkspace` — built once per dispatch cycle by joining `worker_workspaces` against the distinct `(source_type, source_key)` tuples across the current eligible-task batch. One query per cycle, not per (task, worker).

### Score

```go
type WarmScore struct {
    Matches  bool   // identical source_type + source_key
    SameHash bool   // baseline_hash matches the server's estimate
}

score := free_slots
if ws, ok := warmByWorker[w.ID]; ok {
    s := warmScore(task, ws)
    switch {
    case s.SameHash: score += 10_000
    case s.Matches:  score += 1_000
    }
}
// best = argmax(score)
```

The `10_000 / 1_000` constants guarantee any warm match outranks any realistic free-slot advantage on a cold worker, and exact-baseline matches outrank stream-only matches. The server's baseline-hash calculation uses the literal spec (treating `#head` as its own sentinel) — a strong hint, not a guarantee. The agent does the real resolution and may discover the workspace is staler than estimated; that's fine, the dispatcher is a preference, not a correctness constraint.

### Inventory Freshness

- `worker_workspaces` is updated by the agent via `WorkspaceInventoryUpdate` on workspace create, baseline change, Finalize (bumps `last_used_at`), and eviction.
- On agent reconnect, `RegisterRequest` carries the full current inventory. The server upserts-and-prunes in one transaction so stale entries from a previous session can't shadow reality.
- If the server believes a workspace is warm but the agent's disk has been wiped, the first prepare attempt recreates cleanly and emits a corrected inventory update. One task pays a cold-sync cost; subsequent dispatches are correct.

## Eviction

Opt-in. Sweeper goroutine does not start unless at least one threshold is configured.

### Env Vars

| Var | Default | Meaning |
|---|---|---|
| `RELAY_WORKSPACE_ROOT` | — | Required for any source provider |
| `RELAY_WORKSPACE_MAX_AGE` | — | e.g. `14d`; evict workspaces unused longer than this |
| `RELAY_WORKSPACE_MIN_FREE_GB` | — | e.g. `200`; evict LRU until free disk ≥ this |
| `RELAY_WORKSPACE_SWEEP_INTERVAL` | `15m` | Only consulted if either threshold is set |

### Sweep Loop (`internal/agent/source/sweeper.go`)

- Tick at `SWEEP_INTERVAL`. Load registry. Compute free disk. Candidates = workspaces whose RWLock is unheld.
- **Age pass:** evict every candidate with `last_used_at < now - MAX_AGE`.
- **Pressure pass:** while `free_disk < MIN_FREE_GB * GiB` and candidates remain, evict the oldest.
- Eviction steps:
  1. `p4 client -d <client_name>` on the server.
  2. `os.RemoveAll(<root>/<short_id>)`.
  3. Remove from `.relay-registry.json`.
  4. Emit `WorkspaceInventoryUpdate { deleted: true }`.
- Never touches a locked workspace. If every workspace is locked and disk is still low, the sweeper logs a warning and waits for the next tick.

### Failure Modes

- `p4 client -d` refused (e.g. client has shelved or pending CLs not owned by relay): agent logs, skips on-disk removal, retries next tick. Safe-by-default guard against partial destruction of a workspace with human-submitted work.
- `os.RemoveAll` fails mid-way (file in use by a rogue process): agent sets `dirty_delete: true` in the registry and retries next tick.

## Operator Surfaces

### CLI

- `relay workers workspaces <worker-id-or-hostname>` — list warm workspaces (source_key, short_id, baseline_hash, last_used_at, size-on-disk if reported).
- `relay workers evict-workspace <worker> <short-id>` — admin-only; sends an `EvictWorkspaceCommand` to the agent. Useful when a workspace is corrupt or an operator wants to free disk immediately.
- Existing `relay jobs submit` accepts a job spec that can include the new `source` field; no new subcommand.

### Server HTTP API

- `GET /v1/workers/{id}/workspaces` — admin-only; reads from `worker_workspaces`.
- `POST /v1/workers/{id}/workspaces/{short_id}/evict` — admin-only; enqueues the eviction command to the connected agent.

### Observability

- `TASK_STATUS_PREPARING` and `TASK_STATUS_PREPARE_FAILED` flow through the existing SSE event stream. `relay jobs watch` renders them for free.
- `LOG_STREAM_PREPARE` chunks render under a `[prepare]` prefix in `relay tasks logs <task>`.
- Future Prometheus metrics (not blocking v1): `relay_agent_prepare_duration_seconds`, `relay_agent_sync_bytes_total`, `relay_agent_workspace_count`, `relay_agent_workspace_evictions_total{reason="age|pressure|manual"}`.

## Security Considerations

- Agents never see P4 passwords; operators provision tickets out-of-band, consistent with the existing "relay doesn't manage Perforce credentials" boundary.
- `client_template`, `stream`, and `sync.path` inputs are validated against strict patterns in `ValidateJobSpec` before reaching the agent — prevents shell injection into `p4` arguments. The agent uses `exec.Command` (no shell), so even if validation missed something, arguments are not interpolated into a shell.
- `.relay-registry.json` is written atomically (temp file + rename) under a file lock to prevent corruption if two agent processes start on the same host (unsupported config, but shouldn't destroy state if it happens).
- Eviction requires admin role on the HTTP API, same as the existing worker-revoke endpoint.
- The crash-recovery pass deletes only pending CLs owned by the configured `p4 user` AND with a description starting with the `relay-task-` prefix. Cannot accidentally delete a human's work.

## Testing Strategy

- Unit tests for `baseline.go` (canonicalization, hash stability, `#head` sentinel handling).
- Unit tests for the arbitration state machine in `workspace.go` (identical / disjoint / serial cases, downgrade-after-exclusive, `workspace_exclusive` override).
- Unit tests for `registry.go` atomic writes + corruption recovery.
- Unit tests for `sweeper.go` using a mock file system and clock.
- Unit tests for the new dispatcher warm-score branch in `selectWorker`, including the "cold fallback when no warm worker has capacity" case.
- Integration tests (`//go:build integration`) against a real P4 server in a container: create, sync, unshelve, concurrent access, crash recovery of orphaned pending CLs, eviction, agent reconnect with inventory resync.
- `SetBcryptCostForTest`-style helpers for any new `var fn =` injection points (follow the pattern in `internal/cli` and `internal/api`).

## Open Questions / Follow-Ups

None blocking v1. Candidates for v2:

- Git / svn / S3 providers.
- Superset additive concurrency.
- Workspace pre-warming from a peer worker.
- Relay-managed P4 credentials (optional fallback to operator-provisioned).
- Per-stream quota so one large stream can't evict everything else.
