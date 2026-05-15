# Worker Utilization Telemetry — Design

**Date:** 2026-05-15
**Status:** Approved

## Goal

Surface live agent worker load — CPU, memory, and GPU utilization — so a future
front-end worker detail page can display current values and a short-term trend.

This design covers the **backend telemetry pipeline and the REST contract** the
front-end will consume. It does not cover the front-end page itself.

## Background

Two gaps in the current system motivate this work:

1. **No utilization data exists.** `internal/agent/capabilities.go` detects only
   *static* hardware caps (CPU cores, RAM GB, GPU model) once at startup. Nothing
   samples live usage.
2. **No application-level liveness.** Worker down-detection is purely
   connection-based: a worker is `online` exactly while its gRPC stream is open
   (`Handler.Connect`). A wedged-but-connected agent is never noticed, and
   `last_seen_at` is only written at connect/disconnect, so it is really just the
   connect time for an online worker.

The periodic telemetry message introduced here doubles as the first
application-level heartbeat, closing gap 2.

## Decisions

| Decision | Choice |
|---|---|
| Scope | Backend telemetry pipeline + REST contract; no front-end page |
| Retention | In-memory ring buffer per worker (~30 min window); no DB persistence |
| Liveness | The telemetry message owns liveness detection |
| Granularity | Whole-node only (no per-task attribution) |
| Delivery to front-end | REST poll only; liveness *transitions* ride the existing SSE `worker` event |
| Stale detection | Background sweep ticker; adds a `stale` status; no task requeue |

Rationale for the retention choice: the data is intrinsically live/ephemeral and
the use case is a worker detail page, not capacity analytics. A ring buffer gives
gauges plus short-term sparklines with no migration, no DB write amplification,
and no pruning job. History blanking briefly after a server restart is acceptable
(agents re-report within one interval).

Rationale for excluding task requeue on stale: a `stale` agent is not a
*disconnected* agent — its TCP stream is still open and it may still be running
tasks. Automated requeue would risk double-running work. Disconnect-driven
requeue stays with `GraceRegistry`.

## Architecture & data flow

```
Agent (sampler, 10s tick)
  → builds WorkerTelemetry proto msg
  → enqueues on existing sendCh (single send goroutine owns writes)
  → gRPC stream
Server Handler.Connect message loop
  → new AgentMessage_Telemetry case
  → metrics.Store.Append(workerID, sample)  // sample carries server receipt time
Stale sweeper (server, 10s ticker)
  → queries DB for online/stale workers
  → checks metrics.Store.LastSampleAt per worker
  → flips online↔stale, publishes existing "worker" SSE event
Front-end
  → GET /workers/{id}/metrics → metrics.Store.Snapshot → JSON
```

New single-purpose units:

- `internal/agent/telemetry.go` — `TelemetrySampler`: samples the host and builds
  the proto message. Testable via injected exec fn + clock (same pattern as
  `detect` in `capabilities.go`).
- `internal/metrics/store.go` — `Store`: per-worker ring buffers. Pure in-memory
  data structure, no external dependencies.
- `internal/metrics/sweep.go` — `Sweeper`: derives liveness from sample age.
  Dependencies: `*store.Queries`, `*events.Broker`, `*Store`, a clock.

Plus proto, handler-wiring, and REST-handler changes.

## Proto change

New `AgentMessage` payload, `oneof` tag 5:

```proto
message WorkerTelemetry {
  double cpu_percent         = 1;  // 0-100, host-wide
  uint64 mem_used_bytes      = 2;
  uint64 mem_total_bytes     = 3;
  bool   has_gpu             = 4;  // distinguishes "no GPU" from "0% GPU"
  double gpu_util_percent    = 5;  // average across GPUs
  uint64 gpu_mem_used_bytes  = 6;  // summed across GPUs
  uint64 gpu_mem_total_bytes = 7;
}
```

No timestamp field — the **server stamps receipt time** on arrival. This avoids
clock-skew handling; the cadence is fixed, so sample age = `now - receiptTime`.

`AgentMessage.payload` gains `WorkerTelemetry telemetry = 5;`. Run `make generate`
after editing the `.proto`.

## Agent sampler

`TelemetrySampler` runs a goroutine ticking every `RELAY_TELEMETRY_INTERVAL`
(default 10s):

- **CPU%:** retain the prior `cpu.Times` snapshot and compute the delta each tick
  (non-blocking — avoids the sleeping form of `cpu.Percent`).
- **Memory:** `mem.VirtualMemory()` (gopsutil, already a dependency).
- **GPU:** reuse the `nvidia-smi` exec pattern from `capabilities.go` — query
  `utilization.gpu,memory.used,memory.total`; average util and sum memory across
  GPUs. No NVIDIA GPU → `has_gpu = false`.
- Enqueues the message onto the existing `sendCh`. **If `sendCh` is full
  (64-cap), the sample is dropped** — telemetry is lossy by nature and must never
  block the single send goroutine.
- Any sampling error yields a sample with zero/absent fields; the sampler never
  crashes.

## Server: metrics store & lifecycle

`metrics.Store` holds a fixed-capacity ring buffer per worker. Capacity =
`RELAY_TELEMETRY_WINDOW / RELAY_TELEMETRY_INTERVAL` (30 min / 10 s = 180).
Mutex-guarded. Lifecycle methods, called from `Handler`:

- `Activate(workerID)` — called in `finishRegister`; seeds an empty buffer with an
  activation timestamp.
- `Append(workerID, sample)` — called in the new telemetry message case.
- `Clear(workerID)` — called in `markWorkerOffline`; offline workers hold no
  metrics.
- `Snapshot(workerID) []Sample` — for the REST handler; oldest-to-newest order.
- `LastSampleAt(workerID)` — the newest sample's time, or the activation time if
  the buffer is empty. This handles cold-start: a freshly connected worker is not
  instantly considered `stale`.

## Server: stale sweeper

A background ticker (~10s), started in `cmd/relay-server` alongside the
`schedrunner` loop:

- Queries the DB for workers with status `online` or `stale` (new query
  `ListWorkersByLiveness`).
- For each worker: if `now - Store.LastSampleAt > RELAY_TELEMETRY_STALE_AFTER`
  (default 30s ≈ 3 missed intervals) and status is `online` → set `stale`. If
  samples have resumed and status is `stale` → set `online`.
- On every transition, publish the existing `worker` SSE event so dashboards
  update live.
- **No task requeue** — disconnect-driven requeue remains with `GraceRegistry`.
- The sweeper has no dependency on `worker.Registry`; it operates purely off DB
  status plus the metrics store.

Worker status enum grows from `online`/`offline` to `online`/`stale`/`offline`.
`workers.status` is a plain string column; the design assumes no `CHECK`
constraint. The implementation verifies this and adds a migration only if a
constraint exists.

## REST contract

`GET /workers/{id}/metrics` — same `BearerAuth` middleware as `handleGetWorker`:

```json
{
  "worker_id": "uuid",
  "sample_interval_seconds": 10,
  "samples": [
    {"t": "2026-05-15T12:00:00Z", "cpu_pct": 42.1,
     "mem_used": 8123456789, "mem_total": 16000000000,
     "gpu": true, "gpu_util_pct": 71.0,
     "gpu_mem_used": 4000000000, "gpu_mem_total": 8000000000}
  ]
}
```

- Unknown worker → `404`.
- Offline worker, or worker with no data yet → `200` with an empty `samples`
  array.
- The existing `workerResponse` (GET `/workers/{id}` and the list endpoint) gains
  a `last_sample_at` field so the worker list can show liveness without a second
  call. The new `stale` status flows through the existing string `status` field
  with no struct change.

## Configuration

| Env var | Default | Used by |
|---|---|---|
| `RELAY_TELEMETRY_INTERVAL` | `10s` | Agent — sample cadence |
| `RELAY_TELEMETRY_WINDOW` | `30m` | Server — ring buffer retention (→ capacity) |
| `RELAY_TELEMETRY_STALE_AFTER` | `30s` | Server — sweeper staleness threshold |

The agent interval and the server threshold are decoupled. The only constraint,
which must be documented in the README, is `RELAY_TELEMETRY_STALE_AFTER` >
`RELAY_TELEMETRY_INTERVAL`.

## Error handling

- Agent sampling failure (missing `nvidia-smi`, gopsutil error) → emit a sample
  with zero/absent fields; never crash the sampler.
- `sendCh` full → drop the sample.
- Server receives malformed telemetry → ignore it (consistent with the other
  message handlers in `Handler.Connect`).
- `GET /workers/{id}/metrics` for an unknown worker → `404`; for an offline
  worker → `200` with empty `samples`.

## Testing

- `TelemetrySampler` — injected exec fn + clock; GPU present vs absent;
  sampling-error fallback.
- `metrics.Store` — ring-buffer wrap-around, concurrency safety, snapshot
  ordering, cold-start `LastSampleAt`.
- `Sweeper` — injected clock; `online → stale → online` transitions and the SSE
  publish on each transition.
- `Handler` — telemetry message appends to the store; `Clear` on disconnect.
- REST — `GET /workers/{id}/metrics`: happy path, empty, `404`.
- Integration (optional) — agent connects, server receives telemetry, sweeper
  marks the worker `stale` when the sampler is paused.

## Out of scope

- The front-end worker detail page (UI/UX, framework).
- Per-task resource attribution.
- Long-term / persisted time-series and capacity analytics.
- Automatic task requeue on `stale` status.
