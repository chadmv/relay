# Relay — Render Farm System Design

**Date:** 2026-04-14  
**Status:** Approved

---

## Overview

Relay is a general-purpose distributed task runner designed for internal networks. Users submit jobs composed of tasks arranged as a directed acyclic graph (DAG). The system distributes work across worker nodes based on machine capabilities and admin-managed labels, with priority queuing and reservation support. An API-first design means the CLI and web UI are consumers of the same REST API.

---

## Architecture

Three layers:

1. **Clients** — CLI, web UI, and external systems talk to the coordinator over REST/HTTP.
2. **relay-server** — The coordinator: a single Go binary housing the REST API, Scheduler, gRPC Gateway, and DAG Resolver. All state lives in PostgreSQL, making the coordinator stateless and restartable.
3. **relay-agent** — A Go binary running on each worker node, connected to the coordinator via a persistent bidirectional gRPC stream.

**Communication:**
- Clients ↔ Coordinator: REST/HTTP + Server-Sent Events for live updates
- Coordinator ↔ Workers: bidirectional gRPC streaming (task dispatch + status events)
- Coordinator ↔ Database: SQL (PostgreSQL)

---

## Job & DAG Model

### Job

The top-level unit of work submitted by a user.

| Field | Type | Description |
|---|---|---|
| id | uuid | Unique identifier |
| name | string | Human-readable name |
| priority | enum | `low`, `normal`, `high`, `critical` |
| status | enum | `pending` → `running` → `done` / `failed` / `cancelled` |
| submitted_by | uuid | User ID |
| labels | map[string]string | Arbitrary metadata |
| created_at | timestamp | |

### Task

A single unit of work within a job.

| Field | Type | Description |
|---|---|---|
| id | uuid | Unique identifier |
| job_id | uuid | Parent job |
| command | []string | argv — the executable and its arguments |
| env | map[string]string | Environment variables |
| requires | map[string]string | Label selectors — worker must match all |
| depends_on | []uuid | Task IDs that must succeed before this task is dispatched |
| timeout | duration | Max execution time; agent kills subprocess if exceeded |
| retries | int | Max retry attempts on failure (default: 0) |
| status | enum | `pending` → `dispatched` → `running` → `done` / `failed` / `timed_out` |
| worker_id | uuid | Worker assigned to this task |
| started_at | timestamp | |
| finished_at | timestamp | |

### DAG Rules

- Tasks with no `depends_on` are eligible for dispatch immediately when the job is submitted.
- A task becomes eligible only when all its dependencies have status `done`.
- If any task fails, all tasks that depend on it (directly or transitively) are marked `failed` without being dispatched.
- Tasks within the same job that have no dependency relationship run in parallel.

---

## Worker Agent

### Configuration

The agent requires only one piece of local configuration: the coordinator address. If omitted, the agent discovers the coordinator via mDNS (`_relay._tcp.local`). This provides zero-config operation on simple flat networks, with an optional `--coordinator` flag override for multi-subnet environments.

A small local state file (not human-managed) stores the agent's assigned worker ID (UUID) so it can identify itself consistently across restarts.

### Capabilities

On startup the agent auto-detects hardware and merges it with centrally-managed labels:

**Auto-detected:**
- `cpu_cores` — logical CPU count
- `ram_gb` — total RAM
- `gpu_count` — number of GPUs
- `gpu_model` — GPU model name(s)
- `os` — operating system
- `hostname` — machine hostname

**Centrally managed (via API):**
- Arbitrary admin labels: e.g., `zone=studio-a`, `tier=high`, `software.blender=4.1`
- `name` — human-readable worker name (defaults to hostname, editable via API)
- `max_slots` — max concurrent tasks (default: 1, editable via API)

### Lifecycle

1. **Start** — auto-detect hardware capabilities.
2. **Connect** — open bidirectional gRPC stream to coordinator. Send capabilities. Coordinator creates or updates the worker record in Postgres and marks the worker online.
3. **Receive tasks** — coordinator pushes tasks over the stream. Agent forks a subprocess per task, captures stdout/stderr, streams status events back (started, progress, done, failed).
4. **Heartbeat** — the gRPC stream itself serves as the heartbeat. No separate ping needed.
5. **Disconnect/crash** — coordinator detects stream loss within seconds. In-flight tasks are re-queued (up to their retry limit). Agent reconnects with exponential backoff.

### Concurrency

Each agent has a `max_slots` value (managed centrally). The coordinator tracks available slots per worker and never dispatches more tasks than a worker has free slots.

---

## Scheduling

Task dispatch follows a four-step pipeline:

1. **Filter eligible workers** — worker must match all `requires` label selectors on the task, and must not be reserved for a different user/project.
2. **Check reservations** — if the submitting user/project has a reservation covering any eligible worker, those workers are preferred and used exclusively for that user/project.
3. **Sort by priority** — among eligible workers, pick the one with the most free slots. Among queued tasks, dispatch the highest-priority job first. Priority order: `critical` > `high` > `normal` > `low`.
4. **Dispatch** — task sent to the selected worker over its gRPC stream. Worker ACKs and begins execution.

The scheduler runs on every worker connect event and on a periodic tick, so queued tasks are dispatched as soon as capacity becomes available.

**Priority behaviour:** higher-priority jobs jump ahead in the queue but do not preempt running tasks on workers.

### Reservations

- Managed by admins only via the API.
- Can target workers by label selector (e.g., all workers with `zone=studio-a`) or by specific worker IDs.
- Scoped to a user or project.
- Support an optional time window (start/end); reservations auto-expire at the end time.
- Reserved workers are used exclusively by the designated user/project — other jobs skip them during dispatch.

---

## REST API

All endpoints require a Bearer token. Admin-only endpoints return 403 for non-admin tokens.

### Jobs
| Method | Path | Description |
|---|---|---|
| POST | /v1/jobs | Submit a new job |
| GET | /v1/jobs | List jobs (filterable by status, user, priority) |
| GET | /v1/jobs/:id | Job detail including task graph |
| DELETE | /v1/jobs/:id | Cancel a job |

### Tasks
| Method | Path | Description |
|---|---|---|
| GET | /v1/jobs/:id/tasks | List tasks for a job |
| GET | /v1/tasks/:id | Task detail and current status |
| GET | /v1/tasks/:id/logs | Captured stdout/stderr output |

### Workers
| Method | Path | Description |
|---|---|---|
| GET | /v1/workers | List workers with current status |
| GET | /v1/workers/:id | Worker detail including capabilities and labels |
| PATCH | /v1/workers/:id | Update name, labels, or max_slots (admin only) |

### Reservations (admin only)
| Method | Path | Description |
|---|---|---|
| GET | /v1/reservations | List all reservations |
| POST | /v1/reservations | Create a reservation |
| DELETE | /v1/reservations/:id | Remove a reservation |

### Auth & System
| Method | Path | Description |
|---|---|---|
| POST | /v1/auth/token | Issue an API token |
| GET | /v1/health | Coordinator health check |
| GET | /v1/events | SSE stream — real-time job/worker status events |

---

## Fault Tolerance

| Scenario | Behaviour |
|---|---|
| Worker crashes mid-task | gRPC stream drops → coordinator re-queues task within seconds → retried on another worker up to retry limit → task/job marked failed if retries exhausted |
| Task exits non-zero | Task marked failed; dependent tasks cascade to failed; logs preserved |
| Task timeout exceeded | Agent kills subprocess; treated as failure; retried if retries remain |
| Coordinator restarts | Stateless — all state in Postgres. Workers reconnect via mDNS; in-flight tasks from prior session re-queued on reconnect |
| No eligible worker available | Task stays queued indefinitely; dispatcher retries on every worker connect event and on a periodic tick |

---

## Testing Strategy

| Layer | Scope | Approach |
|---|---|---|
| Unit | DAG resolver, scheduler logic, label selector matching | Pure functions, no I/O, fast |
| Integration | Coordinator + Postgres | Real Postgres via testcontainers; submit jobs, verify state transitions |
| Agent | Task execution, log streaming, reconnect, crash recovery | In-process fake coordinator |
| End-to-end | Full system | Real coordinator + N in-process agents; submit DAG jobs, verify execution order and API state |

---

## Repository Structure

```
relay/
  cmd/
    relay-server/     # coordinator binary
    relay-agent/      # worker agent binary
    relay/            # CLI binary
  internal/
    api/              # REST handlers
    scheduler/        # dispatch logic, DAG resolver
    worker/           # gRPC gateway, worker registry
    store/            # Postgres queries (sqlc)
    proto/            # protobuf definitions for agent gRPC
  web/                # web UI (to be designed separately)
  docs/
    superpowers/
      specs/
```

---

## Out of Scope (v1)

- Web UI implementation (separate design)
- Resource quotas / billing
- Multi-coordinator (HA) clustering
- Docker/container-based task isolation
- Cross-subnet mDNS bridging
