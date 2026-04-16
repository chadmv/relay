# Relay Agent Design

**Date:** 2026-04-15
**Status:** Approved

---

## Overview

`relay-agent` is the worker binary that runs on each render node. It connects to the coordinator (`relay-server`) via a persistent bidirectional gRPC stream, receives task dispatch instructions, executes subprocesses, and streams status and log output back. This is Plan 3 of the relay render farm build.

---

## Architecture

Three focused sub-components in `internal/agent`, plus a standalone `internal/discovery` package:

| Component | Responsibility |
|---|---|
| `Capabilities` | One-shot hardware detection at startup |
| `Runner` | Subprocess execution for a single task |
| `Agent` | gRPC stream lifecycle, reconnect loop, task dispatch |
| `discovery` | mDNS browse for coordinator address |

`cmd/relay-agent/main.go` wires everything: parse flags, load state file, detect capabilities, resolve coordinator address, call `Agent.Run`.

---

## Package Structure

```
internal/
  agent/
    capabilities.go       Detect cpu_cores, ram_gb, gpu_count, gpu_model, os, hostname
    capabilities_test.go  Unit tests (injected exec func for nvidia-smi)
    runner.go             Subprocess execution; streams logs + status via sendCh
    runner_test.go        Unit tests with a fake send channel
    agent.go              gRPC connect/reconnect loop; dispatches DispatchTask to Runner
    agent_test.go         Integration test (fake in-process gRPC server; build tag: integration)
  discovery/
    mdns.go               Browse _relay._tcp.local; return first host:port found
    mdns_test.go          Unit test with a local mDNS advertiser
cmd/
  relay-agent/
    main.go               Flags, state file, capabilities, discovery, Agent.Run
```

`discovery` is separate from `agent` because mDNS is a one-shot lookup at startup with no ongoing relationship to the connection loop — independently testable and replaceable.

---

## Capabilities Detection

```go
type Capabilities struct {
    Hostname string
    OS       string
    CPUCores int32
    RAMGB    int32
    GPUCount int32
    GPUModel string
}

func Detect() Capabilities
```

| Field | Source |
|---|---|
| `Hostname` | `os.Hostname()` |
| `OS` | `runtime.GOOS` |
| `CPUCores` | `runtime.NumCPU()` |
| `RAMGB` | `github.com/pbnjay/memory` — total system RAM, rounded to nearest GB |
| `GPUCount` | Count of lines from `nvidia-smi --query-gpu=name --format=csv,noheader,nounits` |
| `GPUModel` | Unique model names from the same output, joined with `", "` |

If `nvidia-smi` is absent or fails, `GPUCount` is 0 and `GPUModel` is empty — not an error. For testability, the nvidia-smi exec call is injected as `func(args ...string) ([]byte, error)`; `Detect()` uses the real `exec.Command` by default.

---

## State File

**Location:** `<state-dir>/worker-id`

**Format:** Plain UUID string (no JSON, no newline).

**Flags:**
- `--state-dir` — default `/var/lib/relay-agent` (Linux/macOS) or `%ProgramData%\relay` (Windows), via `runtime.GOOS` check in `main.go`

**Lifecycle:**
- On first start: file absent → `RegisterRequest.worker_id` is empty → coordinator assigns ID → agent writes returned ID to state file
- On restart: file present → agent reads it → sends it in `RegisterRequest.worker_id` → coordinator matches existing DB record via `UpsertWorkerByHostname`

---

## mDNS Discovery

**Package:** `internal/discovery`

**Dependency:** `github.com/grandcat/zeroconf`

**Behaviour:**
- Browse `_relay._tcp.local` with a 5-second timeout
- Return the first `host:port` discovered
- If timeout expires with no result, return an error; `main` exits with a clear message

**Flag override:**
- `--coordinator host:port` — skips discovery entirely; passed directly to `Agent`

`main.go` resolves the coordinator address once at startup:
```go
if *coordinator == "" {
    addr, err = discovery.Browse(ctx)
} else {
    addr = *coordinator
}
```

---

## Agent — gRPC Lifecycle

```go
type Agent struct {
    coord    string
    caps     Capabilities
    workerID string        // empty on first run; persisted after RegisterResponse
    sendCh   chan *relayv1.AgentMessage  // buffered (64); created once for Agent lifetime
    mu       sync.Mutex
    runners  map[string]*Runner          // protected by mu
    saveID   func(string) error          // writes worker-id to state file
}
```

**Connect sequence:**
1. Dial coordinator: `grpc.Dial(coord, grpc.WithTransportCredentials(insecure.NewCredentials()))`
2. Open `AgentService.Connect` stream
3. Send `RegisterRequest` with capabilities and current `workerID`
4. Receive `RegisterResponse` — if `worker_id` differs from current, update `workerID` and call `saveID`
5. Start **send goroutine**: drains `sendCh`, calls `stream.Send` (gRPC send is not concurrent-safe — all outbound messages funnel here)
6. Enter **recv loop**: handle `DispatchTask` and `CancelTask`

**DispatchTask:**
- Create `Runner{taskID, sendCh, timeout}`; store in `runners[taskID]` (under mu)
- Launch goroutine: `runner.Run(ctx, task)` then `mu.Lock(); delete(runners, taskID); mu.Unlock()`

**CancelTask:**
- Look up `runners[taskID]` (under mu); call `runner.Cancel()` if found

**Reconnect loop (in `Agent.Run`):**
- On any stream error or EOF: cancel all in-flight runners (under mu), clear `runners`
- `sendCh` is created once at `Agent` construction (buffered, capacity 64); shared across reconnects so runners draining into it during teardown don't block
- Exponential backoff: 1s → 2s → 4s … capped at 60s
- Backoff resets to 1s after a successful `RegisterResponse`

---

## Runner — Task Execution

```go
type Runner struct {
    taskID    string
    sendCh    chan *relayv1.AgentMessage
    cancel    context.CancelFunc
    cancelled atomic.Bool
}
```

**Execution flow:**
1. Build `exec.CommandContext(ctx, command[0], command[1:]...)` with task `env` merged over the current process environment
2. Attach separate stdout and stderr pipes
3. Start process; send `TASK_STATUS_RUNNING`
4. Two goroutines read each pipe in 4 KB chunks; each chunk becomes a `TaskLogChunk` on `sendCh`
5. `cmd.Wait()` — determine final status:
   - Exit code 0 → `TASK_STATUS_DONE`
   - `cancelled.Load() == true` → `TASK_STATUS_FAILED` (coordinator-initiated cancel)
   - `ctx.Err() != nil` (deadline exceeded) → `TASK_STATUS_TIMED_OUT`
   - Any other non-zero exit → `TASK_STATUS_FAILED`
6. Send `TaskStatusUpdate` with status and exit code; return (Agent removes from `runners` map after goroutine returns)

**Timeout:** Set as `ctx` deadline from `DispatchTask.timeout_seconds` (0 means no timeout — use a background context with no deadline).

**Cancel vs Timeout distinction:**
- `runner.Cancel()` sets `cancelled = true` then calls the cancel func → process killed → status reported as `TASK_STATUS_FAILED`
- Natural deadline expiry → process killed by `exec.CommandContext` → `ctx.Err() == context.DeadlineExceeded` → `TASK_STATUS_TIMED_OUT`

---

## main.go

Flags:
```
--coordinator  string   coordinator host:port (skips mDNS if set)
--state-dir    string   directory for persistent state (default: OS-appropriate)
```

Startup sequence:
1. Parse flags
2. Determine state dir default from `runtime.GOOS`
3. Load worker ID from `<state-dir>/worker-id` (ignore not-found error)
4. Detect capabilities via `agent.Detect()`
5. Resolve coordinator address (mDNS or flag)
6. Construct `Agent`; call `agent.Run(ctx)` — blocks until signal
7. Handle `SIGINT`/`SIGTERM` for graceful shutdown: cancel context → reconnect loop exits → in-flight runners cancelled

---

## Dependencies

| Package | Purpose |
|---|---|
| `github.com/pbnjay/memory` | Total system RAM (no cgo, cross-platform) |
| `github.com/grandcat/zeroconf` | mDNS browse/announce |
| `google.golang.org/grpc` | Already present (Plan 1) |

---

## Testing

| File | Scope | Notes |
|---|---|---|
| `capabilities_test.go` | Unit | Inject fake exec func; test multi-GPU parsing, nvidia-smi absent |
| `runner_test.go` | Unit | Run `echo hello`; assert RUNNING + log chunk + DONE; run with 1ms timeout → TIMED_OUT; cancel mid-run → FAILED |
| `mdns_test.go` | Unit | Advertise a local mDNS service; assert Browse returns correct address |
| `agent_test.go` | Integration | In-process fake gRPC server; assert RegisterRequest; dispatch task; assert log + status; close stream → assert reconnect |

---

## Key Design Decisions

- **Trust coordinator for slot management** — agent executes every `DispatchTask` received; no local semaphore
- **Single send goroutine** — gRPC streams are not concurrent-send-safe; all outbound messages funnel through one goroutine draining `sendCh`
- **NVIDIA-only GPU detection** — render farms are overwhelmingly NVIDIA; `nvidia-smi` requires no extra dependency
- **mDNS with flag override** — zero-config on flat networks; `--coordinator` for multi-subnet deployments
- **No TLS** — v1; internal network only
- **Cancel vs Timeout** — distinguished by `cancelled atomic.Bool` flag; coordinator-initiated kills report `FAILED`, deadline expiry reports `TIMED_OUT`

---

## Out of Scope (v1)

- TLS / mutual TLS between agent and coordinator
- AMD / Intel GPU detection
- Task output artifact upload
- Agent self-update
