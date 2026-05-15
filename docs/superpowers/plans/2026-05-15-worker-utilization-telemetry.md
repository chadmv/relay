# Worker Utilization Telemetry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Agents periodically sample host CPU/memory/GPU utilization and report it to the server, which retains a short-term in-memory history per worker, derives liveness from sample freshness, and exposes the history over a REST endpoint.

**Architecture:** A new periodic `WorkerTelemetry` gRPC message flows agent → server on the existing `Connect` stream. The server stores samples in a per-worker in-memory ring buffer (`internal/metrics.Store`). A background sweeper (`internal/metrics.Sweeper`) flips workers between `online` and `stale` based on sample age. `GET /v1/workers/{id}/metrics` serves the buffer.

**Tech Stack:** Go, gRPC/protobuf, sqlc + Postgres, gopsutil v4, stdlib `net/http`.

**Reference spec:** `docs/superpowers/specs/2026-05-15-worker-utilization-telemetry-design.md`

---

## File Structure

**Created:**
- `internal/metrics/store.go` — `Store`: per-worker ring buffer of samples.
- `internal/metrics/store_test.go` — unit tests for `Store`.
- `internal/metrics/sweep.go` — `Sweeper`: derives `online`/`stale` from sample age.
- `internal/metrics/sweep_test.go` — unit tests for `Sweeper`.
- `internal/agent/telemetry.go` — host sampler + GPU parsing.
- `internal/agent/telemetry_test.go` — unit tests for GPU parsing / sampling.
- `internal/api/worker_metrics.go` — `GET /v1/workers/{id}/metrics` handler.
- `internal/api/worker_metrics_test.go` — unit test for the handler.
- `internal/worker/handler_telemetry_test.go` — unit test for telemetry ingestion.

**Modified:**
- `proto/relayv1/relay.proto` — new `WorkerTelemetry` message + `oneof` field.
- `internal/store/query/workers.sql` — `ListWorkersByLiveness`, `SetWorkerStatus`.
- `internal/agent/agent.go` — `TelemetryInterval` field; start sampler in `Run`.
- `internal/worker/handler.go` — `Metrics` field; ingest telemetry; lifecycle calls.
- `internal/api/server.go` — `Metrics` field; route registration.
- `internal/api/workers.go` — `last_sample_at` on `workerResponse`.
- `cmd/relay-server/main.go` — build `Store`/`Sweeper`, wire, start sweeper.
- `cmd/relay-agent/main.go` — parse `RELAY_TELEMETRY_INTERVAL`.
- `README.md` — document the three new env vars and the `stale` status.

**Design note — wiring via exported fields, not constructor params:** `worker.Handler` and `api.Server` receive the metrics store through an exported `Metrics` field set after construction (mirroring the existing `httpServer.AllowSelfRegister` pattern), NOT through `NewHandler*`/`api.New` parameters. This avoids editing ~60 `api.New` test call sites and keeps every task independently compilable. Handler methods nil-guard the field so existing tests that never set it still pass.

---

## Task 1: Proto — add WorkerTelemetry message

**Files:**
- Modify: `proto/relayv1/relay.proto`

- [ ] **Step 1: Add the telemetry payload to the AgentMessage oneof**

In `proto/relayv1/relay.proto`, change the `AgentMessage` message (currently lines 11-18) to add a fifth payload:

```proto
message AgentMessage {
  oneof payload {
    RegisterRequest          register             = 1;
    TaskStatusUpdate         task_status          = 2;
    TaskLogChunk             task_log             = 3;
    WorkspaceInventoryUpdate workspace_inventory  = 4;
    WorkerTelemetry          telemetry            = 5;
  }
}
```

- [ ] **Step 2: Add the WorkerTelemetry message definition**

In the same file, add this message immediately after the `AgentMessage` message:

```proto
// WorkerTelemetry is a periodic host-utilization sample sent by the agent. It
// doubles as an application-level heartbeat. The server stamps receipt time on
// arrival, so the message carries no timestamp. has_gpu distinguishes a host
// with no NVIDIA GPU from one whose GPU is idle at 0%.
message WorkerTelemetry {
  double cpu_percent         = 1;  // 0-100, host-wide
  uint64 mem_used_bytes      = 2;
  uint64 mem_total_bytes     = 3;
  bool   has_gpu             = 4;
  double gpu_util_percent    = 5;  // averaged across GPUs
  uint64 gpu_mem_used_bytes  = 6;  // summed across GPUs
  uint64 gpu_mem_total_bytes = 7;
}
```

- [ ] **Step 3: Regenerate protobuf bindings**

Run: `make generate`
Expected: no errors; `internal/proto/relayv1/relay.pb.go` now contains `WorkerTelemetry` and `AgentMessage_Telemetry`.

- [ ] **Step 4: Verify the build**

Run: `go build ./...`
Expected: builds with no errors (nothing uses the new types yet).

- [ ] **Step 5: Commit**

```bash
git add proto/relayv1/relay.proto internal/proto/relayv1/
git commit -m "feat: add WorkerTelemetry proto message"
```

---

## Task 2: metrics.Store — per-worker ring buffer

**Files:**
- Create: `internal/metrics/store.go`
- Test: `internal/metrics/store_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/metrics/store_test.go`:

```go
package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_AppendAndSnapshot(t *testing.T) {
	s := NewStore(10)
	s.Activate("w1", time.Unix(0, 0))
	s.Append("w1", Sample{At: time.Unix(1, 0), CPUPercent: 10})
	s.Append("w1", Sample{At: time.Unix(2, 0), CPUPercent: 20})

	snap := s.Snapshot("w1")
	require.Len(t, snap, 2)
	assert.Equal(t, 10.0, snap[0].CPUPercent)
	assert.Equal(t, 20.0, snap[1].CPUPercent)
}

func TestStore_RingBufferEvictsOldest(t *testing.T) {
	s := NewStore(3)
	s.Activate("w1", time.Unix(0, 0))
	for i := 1; i <= 5; i++ {
		s.Append("w1", Sample{At: time.Unix(int64(i), 0), CPUPercent: float64(i)})
	}
	snap := s.Snapshot("w1")
	require.Len(t, snap, 3)
	assert.Equal(t, 3.0, snap[0].CPUPercent)
	assert.Equal(t, 5.0, snap[2].CPUPercent)
}

func TestStore_AppendUntrackedWorkerIsNoOp(t *testing.T) {
	s := NewStore(10)
	s.Append("ghost", Sample{At: time.Unix(1, 0)})
	assert.Empty(t, s.Snapshot("ghost"))
}

func TestStore_SnapshotUnknownWorkerIsEmptyNotNil(t *testing.T) {
	s := NewStore(10)
	assert.Equal(t, []Sample{}, s.Snapshot("nobody"))
}

func TestStore_LastSampleAt(t *testing.T) {
	s := NewStore(10)

	_, ok := s.LastSampleAt("unknown")
	assert.False(t, ok)

	s.Activate("w1", time.Unix(100, 0))
	at, ok := s.LastSampleAt("w1")
	require.True(t, ok)
	assert.Equal(t, time.Unix(100, 0), at, "empty buffer returns activation time")

	s.Append("w1", Sample{At: time.Unix(200, 0)})
	at, ok = s.LastSampleAt("w1")
	require.True(t, ok)
	assert.Equal(t, time.Unix(200, 0), at, "non-empty buffer returns newest sample time")
}

func TestStore_ClearStopsTracking(t *testing.T) {
	s := NewStore(10)
	s.Activate("w1", time.Unix(0, 0))
	s.Append("w1", Sample{At: time.Unix(1, 0)})
	s.Clear("w1")

	_, ok := s.LastSampleAt("w1")
	assert.False(t, ok)
	assert.Empty(t, s.Snapshot("w1"))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/metrics/... -v`
Expected: FAIL — `internal/metrics/store.go` does not exist (`undefined: NewStore`, `undefined: Sample`).

- [ ] **Step 3: Write the implementation**

Create `internal/metrics/store.go`:

```go
// Package metrics holds short-term, in-memory worker utilization telemetry and
// derives worker liveness from how recently each worker reported a sample.
package metrics

import (
	"sync"
	"time"
)

// DefaultSampleInterval is the assumed agent sampling cadence. The server uses
// it to size ring buffers and to report sample_interval_seconds to API clients.
// It is intentionally a constant, not configurable: the agent's actual cadence
// (RELAY_TELEMETRY_INTERVAL) may differ, which only shifts how much wall-clock
// history a fixed-size buffer holds.
const DefaultSampleInterval = 10 * time.Second

// Sample is one host-utilization reading for a worker, stamped with the
// server's receipt time.
type Sample struct {
	At             time.Time
	CPUPercent     float64
	MemUsedBytes   uint64
	MemTotalBytes  uint64
	HasGPU         bool
	GPUUtilPercent float64
	GPUMemUsed     uint64
	GPUMemTotal    uint64
}

// ring is one worker's bounded sample history.
type ring struct {
	activatedAt time.Time
	samples     []Sample // oldest-first; len <= capacity
}

// Store holds a bounded ring buffer of utilization samples per worker. All
// methods are safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	capacity int
	workers  map[string]*ring
}

// NewStore returns a Store whose per-worker buffers hold at most capacity
// samples. capacity is clamped to a minimum of 1.
func NewStore(capacity int) *Store {
	if capacity < 1 {
		capacity = 1
	}
	return &Store{capacity: capacity, workers: make(map[string]*ring)}
}

// Activate begins tracking a worker, seeding an empty buffer with the given
// activation time. Called when a worker registers.
func (s *Store) Activate(workerID string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[workerID] = &ring{activatedAt: now}
}

// Append records a sample for a worker. It is a no-op if the worker is not
// currently tracked (i.e. Activate has not been called, or Clear has).
func (s *Store) Append(workerID string, sample Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.workers[workerID]
	if !ok {
		return
	}
	r.samples = append(r.samples, sample)
	if len(r.samples) > s.capacity {
		r.samples = r.samples[len(r.samples)-s.capacity:]
	}
}

// Clear stops tracking a worker and discards its samples.
func (s *Store) Clear(workerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workers, workerID)
}

// Snapshot returns a copy of the worker's samples, oldest-first. It returns a
// non-nil empty slice for an unknown or sample-less worker.
func (s *Store) Snapshot(workerID string) []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.workers[workerID]
	if !ok || len(r.samples) == 0 {
		return []Sample{}
	}
	out := make([]Sample, len(r.samples))
	copy(out, r.samples)
	return out
}

// LastSampleAt returns the time of the worker's most recent sample, or its
// activation time if it has reported no samples yet. The bool is false if the
// worker is not tracked.
func (s *Store) LastSampleAt(workerID string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.workers[workerID]
	if !ok {
		return time.Time{}, false
	}
	if len(r.samples) == 0 {
		return r.activatedAt, true
	}
	return r.samples[len(r.samples)-1].At, true
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/metrics/... -v`
Expected: PASS — all six tests.

- [ ] **Step 5: Commit**

```bash
git add internal/metrics/store.go internal/metrics/store_test.go
git commit -m "feat: add in-memory worker telemetry ring buffer"
```

---

## Task 3: SQL queries + metrics.Sweeper

**Files:**
- Modify: `internal/store/query/workers.sql`
- Create: `internal/metrics/sweep.go`
- Test: `internal/metrics/sweep_test.go`

- [ ] **Step 1: Add the two SQL queries**

Append to `internal/store/query/workers.sql`:

```sql
-- name: ListWorkersByLiveness :many
-- Workers eligible for staleness sweeping: those currently connected.
SELECT * FROM workers WHERE status IN ('online', 'stale');

-- name: SetWorkerStatus :exec
-- Updates only the status column, leaving last_seen_at and disconnected_at
-- untouched. Used by the liveness sweeper for online<->stale transitions.
UPDATE workers SET status = $2 WHERE id = $1;
```

- [ ] **Step 2: Regenerate the store layer**

Run: `make generate`
Expected: no errors; `internal/store/workers.sql.go` now defines `ListWorkersByLiveness` and `SetWorkerStatus`/`SetWorkerStatusParams`.

- [ ] **Step 3: Verify the build**

Run: `go build ./...`
Expected: builds with no errors.

- [ ] **Step 4: Write the failing test**

Create `internal/metrics/sweep_test.go`:

```go
package metrics

import (
	"context"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testWorkerID = "11111111-1111-1111-1111-111111111111"

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	require.NoError(t, u.Scan(s))
	return u
}

type fakeSweepStore struct {
	workers []store.Worker
	updates []store.SetWorkerStatusParams
}

func (f *fakeSweepStore) ListWorkersByLiveness(ctx context.Context) ([]store.Worker, error) {
	return f.workers, nil
}

func (f *fakeSweepStore) SetWorkerStatus(ctx context.Context, arg store.SetWorkerStatusParams) error {
	f.updates = append(f.updates, arg)
	return nil
}

func TestSweeper_OnlineToStale(t *testing.T) {
	st := NewStore(10)
	st.Activate(testWorkerID, time.Unix(0, 0)) // last sample at t=0

	fake := &fakeSweepStore{workers: []store.Worker{
		{ID: mustUUID(t, testWorkerID), Status: "online"},
	}}
	sw := NewSweeper(fake, events.NewBroker(), st, 30*time.Second)
	sw.now = func() time.Time { return time.Unix(40, 0) } // 40s > 30s threshold

	require.NoError(t, sw.SweepOnce(context.Background()))
	require.Len(t, fake.updates, 1)
	assert.Equal(t, "stale", fake.updates[0].Status)
}

func TestSweeper_StaleToOnline(t *testing.T) {
	st := NewStore(10)
	st.Activate(testWorkerID, time.Unix(0, 0))
	st.Append(testWorkerID, Sample{At: time.Unix(100, 0)}) // fresh sample

	fake := &fakeSweepStore{workers: []store.Worker{
		{ID: mustUUID(t, testWorkerID), Status: "stale"},
	}}
	sw := NewSweeper(fake, events.NewBroker(), st, 30*time.Second)
	sw.now = func() time.Time { return time.Unix(110, 0) } // 10s <= 30s threshold

	require.NoError(t, sw.SweepOnce(context.Background()))
	require.Len(t, fake.updates, 1)
	assert.Equal(t, "online", fake.updates[0].Status)
}

func TestSweeper_NoTransitionWhenFresh(t *testing.T) {
	st := NewStore(10)
	st.Activate(testWorkerID, time.Unix(0, 0))
	st.Append(testWorkerID, Sample{At: time.Unix(100, 0)})

	fake := &fakeSweepStore{workers: []store.Worker{
		{ID: mustUUID(t, testWorkerID), Status: "online"},
	}}
	sw := NewSweeper(fake, events.NewBroker(), st, 30*time.Second)
	sw.now = func() time.Time { return time.Unix(110, 0) }

	require.NoError(t, sw.SweepOnce(context.Background()))
	assert.Empty(t, fake.updates, "fresh online worker stays online")
}

func TestSweeper_SkipsUntrackedWorker(t *testing.T) {
	st := NewStore(10) // testWorkerID never Activated

	fake := &fakeSweepStore{workers: []store.Worker{
		{ID: mustUUID(t, testWorkerID), Status: "online"},
	}}
	sw := NewSweeper(fake, events.NewBroker(), st, 30*time.Second)
	sw.now = func() time.Time { return time.Unix(9999, 0) }

	require.NoError(t, sw.SweepOnce(context.Background()))
	assert.Empty(t, fake.updates, "untracked workers are skipped, not marked stale")
}
```

- [ ] **Step 5: Run the test to verify it fails**

Run: `go test ./internal/metrics/... -run TestSweeper -v`
Expected: FAIL — `undefined: NewSweeper`.

- [ ] **Step 6: Write the implementation**

Create `internal/metrics/sweep.go`:

```go
package metrics

import (
	"context"
	"fmt"
	"log"
	"time"

	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// SweepInterval is how often the Sweeper re-evaluates worker liveness.
const SweepInterval = 10 * time.Second

// sweepStore is the subset of *store.Queries the Sweeper needs. *store.Queries
// satisfies it; tests supply a fake.
type sweepStore interface {
	ListWorkersByLiveness(ctx context.Context) ([]store.Worker, error)
	SetWorkerStatus(ctx context.Context, arg store.SetWorkerStatusParams) error
}

// Sweeper flips connected workers between "online" and "stale" based on how
// recently they reported telemetry. It never requeues tasks — a stale worker
// is still connected; disconnect-driven requeue stays with worker.GraceRegistry.
type Sweeper struct {
	q          sweepStore
	broker     *events.Broker
	store      *Store
	staleAfter time.Duration
	now        func() time.Time // injectable clock; defaults to time.Now
}

// NewSweeper constructs a Sweeper. staleAfter is the maximum age of a worker's
// last sample before an online worker is marked stale.
func NewSweeper(q sweepStore, broker *events.Broker, st *Store, staleAfter time.Duration) *Sweeper {
	return &Sweeper{q: q, broker: broker, store: st, staleAfter: staleAfter, now: time.Now}
}

// Run blocks until ctx is cancelled, sweeping every SweepInterval.
func (s *Sweeper) Run(ctx context.Context) {
	t := time.NewTicker(SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.SweepOnce(ctx); err != nil {
				log.Printf("metrics sweeper: %v", err)
			}
		}
	}
}

// SweepOnce performs one liveness check over all online/stale workers.
func (s *Sweeper) SweepOnce(ctx context.Context) error {
	workers, err := s.q.ListWorkersByLiveness(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for _, w := range workers {
		id := uuidString(w.ID)
		lastAt, tracked := s.store.LastSampleAt(id)
		if !tracked {
			// No telemetry tracking for this worker (e.g. it disconnected
			// between the query and now). Leave its status alone.
			continue
		}
		age := now.Sub(lastAt)
		switch {
		case w.Status == "online" && age > s.staleAfter:
			s.transition(ctx, id, "stale")
		case w.Status == "stale" && age <= s.staleAfter:
			s.transition(ctx, id, "online")
		}
	}
	return nil
}

// transition persists a status change and broadcasts it over SSE.
func (s *Sweeper) transition(ctx context.Context, workerID, status string) {
	var id pgtype.UUID
	if err := id.Scan(workerID); err != nil {
		return
	}
	if err := s.q.SetWorkerStatus(ctx, store.SetWorkerStatusParams{ID: id, Status: status}); err != nil {
		log.Printf("metrics sweeper: set status %s for %s: %v", status, workerID, err)
		return
	}
	s.broker.Publish(events.Event{
		Type: "worker",
		Data: []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, workerID, status)),
	})
}

// uuidString converts a pgtype.UUID to its canonical string representation,
// matching the key format used elsewhere (worker.finishRegister).
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/metrics/... -v`
Expected: PASS — all `Store` and `Sweeper` tests.

- [ ] **Step 8: Commit**

```bash
git add internal/store/query/workers.sql internal/store/workers.sql.go internal/metrics/sweep.go internal/metrics/sweep_test.go
git commit -m "feat: add worker liveness sweeper driven by telemetry age"
```

---

## Task 4: Agent telemetry sampler

**Files:**
- Create: `internal/agent/telemetry.go`
- Test: `internal/agent/telemetry_test.go`
- Modify: `internal/agent/agent.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agent/telemetry_test.go`:

```go
package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseGPUTelemetry_noOutput(t *testing.T) {
	g := parseGPUTelemetry([]byte(""))
	assert.False(t, g.hasGPU)
	assert.Zero(t, g.utilPct)
	assert.Zero(t, g.memTotal)
}

func TestParseGPUTelemetry_singleGPU(t *testing.T) {
	g := parseGPUTelemetry([]byte("45, 2048, 8192\n"))
	assert.True(t, g.hasGPU)
	assert.Equal(t, 45.0, g.utilPct)
	assert.Equal(t, uint64(2048)*1024*1024, g.memUsed)
	assert.Equal(t, uint64(8192)*1024*1024, g.memTotal)
}

func TestParseGPUTelemetry_multipleGPUs(t *testing.T) {
	// util averaged (20+80)/2 = 50; memory summed.
	g := parseGPUTelemetry([]byte("20, 1024, 8192\n80, 3072, 8192\n"))
	assert.True(t, g.hasGPU)
	assert.Equal(t, 50.0, g.utilPct)
	assert.Equal(t, uint64(4096)*1024*1024, g.memUsed)
	assert.Equal(t, uint64(16384)*1024*1024, g.memTotal)
}

func TestParseGPUTelemetry_malformedLineSkipped(t *testing.T) {
	g := parseGPUTelemetry([]byte("garbage\n45, 2048, 8192\n"))
	assert.True(t, g.hasGPU)
	assert.Equal(t, 45.0, g.utilPct)
}

func TestSampleTelemetry_populatesGPUFromExec(t *testing.T) {
	msg := sampleTelemetry(func(name string, args ...string) ([]byte, error) {
		return []byte("70, 4096, 8192\n"), nil
	})
	assert.True(t, msg.HasGpu)
	assert.Equal(t, 70.0, msg.GpuUtilPercent)
	assert.Equal(t, uint64(4096)*1024*1024, msg.GpuMemUsedBytes)
}

func TestSampleTelemetry_noGPUWhenExecFails(t *testing.T) {
	msg := sampleTelemetry(func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("nvidia-smi not found")
	})
	assert.False(t, msg.HasGpu)
	// Host memory total should still be populated by gopsutil on any real host.
	assert.Greater(t, msg.MemTotalBytes, uint64(0))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/agent/... -run "Telemetry" -v`
Expected: FAIL — `undefined: parseGPUTelemetry`, `undefined: sampleTelemetry`.

- [ ] **Step 3: Write the sampler implementation**

Create `internal/agent/telemetry.go`:

```go
package agent

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// execCommand runs an external command and returns its combined stdout. It
// matches the execFn signature defined in capabilities.go.
func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// gpuTelemetry holds parsed nvidia-smi utilization output.
type gpuTelemetry struct {
	hasGPU   bool
	utilPct  float64 // averaged across GPUs
	memUsed  uint64  // bytes, summed across GPUs
	memTotal uint64  // bytes, summed across GPUs
}

// parseGPUTelemetry parses the CSV output of
// `nvidia-smi --query-gpu=utilization.gpu,memory.used,memory.total
// --format=csv,noheader,nounits`. One line per GPU: "util, memUsedMiB,
// memTotalMiB". Utilization is averaged and memory summed across GPUs; memory
// values are converted from MiB to bytes. Malformed lines are skipped.
func parseGPUTelemetry(out []byte) gpuTelemetry {
	var g gpuTelemetry
	var utilSum float64
	var count int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) != 3 {
			continue
		}
		util, err1 := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
		memU, err2 := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
		memT, err3 := strconv.ParseUint(strings.TrimSpace(fields[2]), 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		count++
		utilSum += util
		g.memUsed += memU * 1024 * 1024
		g.memTotal += memT * 1024 * 1024
	}
	if count > 0 {
		g.hasGPU = true
		g.utilPct = utilSum / float64(count)
	}
	return g
}

// sampleTelemetry takes one host-utilization reading. CPU and memory come from
// gopsutil; GPU data comes from running nvidia-smi via the injected exec fn.
// Any individual source that errors leaves its fields at zero — the sampler
// never fails.
func sampleTelemetry(execFn execFn) *relayv1.WorkerTelemetry {
	t := &relayv1.WorkerTelemetry{}

	// cpu.Percent(0, false) reports usage since the previous call (non-blocking).
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		t.CpuPercent = pcts[0]
	}
	if v, err := mem.VirtualMemory(); err == nil {
		t.MemUsedBytes = v.Used
		t.MemTotalBytes = v.Total
	}
	if out, err := execFn("nvidia-smi",
		"--query-gpu=utilization.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits"); err == nil {
		g := parseGPUTelemetry(out)
		t.HasGpu = g.hasGPU
		t.GpuUtilPercent = g.utilPct
		t.GpuMemUsedBytes = g.memUsed
		t.GpuMemTotalBytes = g.memTotal
	}
	return t
}

// runTelemetry samples utilization every TelemetryInterval and enqueues each
// reading on sendCh. If sendCh is full it drops the sample — telemetry is
// lossy by nature and must never block the agent's single send goroutine.
func (a *Agent) runTelemetry(ctx context.Context) {
	// Prime cpu.Percent so the first ticked reading reflects a real delta.
	_, _ = cpu.Percent(0, false)

	t := time.NewTicker(a.TelemetryInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			msg := &relayv1.AgentMessage{
				Payload: &relayv1.AgentMessage_Telemetry{
					Telemetry: sampleTelemetry(execCommand),
				},
			}
			select {
			case a.sendCh <- msg:
			default: // sendCh full — drop this sample
			}
		}
	}
}
```

- [ ] **Step 4: Add the TelemetryInterval field and start the sampler**

In `internal/agent/agent.go`, add a field to the `Agent` struct (after `provider source.Provider` on line 33):

```go
	provider source.Provider // optional; nil = no workspace management

	// TelemetryInterval is the host-utilization sampling cadence. NewAgent sets
	// a default; cmd/relay-agent overrides it from RELAY_TELEMETRY_INTERVAL.
	TelemetryInterval time.Duration
```

In `NewAgent`, add the default to the returned struct literal (after `provider: provider,`):

```go
		provider: provider,
		TelemetryInterval: 10 * time.Second,
```

In `Run`, start the sampler once, immediately after `a.runCtx = ctx` (line 55):

```go
func (a *Agent) Run(ctx context.Context) {
	a.runCtx = ctx
	go a.runTelemetry(ctx)
	backoff := time.Second
```

- [ ] **Step 5: Run the tests and build**

Run: `go test ./internal/agent/... -run "Telemetry" -v`
Expected: PASS — all six telemetry tests.

Run: `go build ./...`
Expected: builds with no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/telemetry.go internal/agent/telemetry_test.go internal/agent/agent.go
git commit -m "feat: sample host utilization in the agent"
```

---

## Task 5: Server handler — ingest telemetry

**Files:**
- Modify: `internal/worker/handler.go`
- Test: `internal/worker/handler_telemetry_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/worker/handler_telemetry_test.go`:

```go
package worker

import (
	"testing"
	"time"

	"relay/internal/metrics"
	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleTelemetry_appendsSampleToStore(t *testing.T) {
	store := metrics.NewStore(10)
	store.Activate("w1", time.Now())
	h := &Handler{Metrics: store}

	h.handleTelemetry("w1", &relayv1.WorkerTelemetry{
		CpuPercent:    55.5,
		MemUsedBytes:  100,
		MemTotalBytes: 200,
		HasGpu:        true,
		GpuUtilPercent: 70.0,
	})

	snap := store.Snapshot("w1")
	require.Len(t, snap, 1)
	assert.Equal(t, 55.5, snap[0].CPUPercent)
	assert.Equal(t, uint64(100), snap[0].MemUsedBytes)
	assert.True(t, snap[0].HasGPU)
	assert.Equal(t, 70.0, snap[0].GPUUtilPercent)
}

func TestHandleTelemetry_nilMetricsIsSafe(t *testing.T) {
	h := &Handler{} // Metrics not set
	// Must not panic.
	h.handleTelemetry("w1", &relayv1.WorkerTelemetry{CpuPercent: 1})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/worker/... -run TestHandleTelemetry -v`
Expected: FAIL — `Handler` has no field `Metrics`; `undefined: h.handleTelemetry`.

- [ ] **Step 3: Add the Metrics field to the Handler struct**

In `internal/worker/handler.go`, add the import `"relay/internal/metrics"` to the import block, then add a field to the `Handler` struct (after `grace *GraceRegistry` on line 48):

```go
	grace           *GraceRegistry

	// Metrics, when non-nil, receives worker utilization samples and tracks
	// per-worker liveness. Set by cmd/relay-server after construction.
	Metrics *metrics.Store
```

- [ ] **Step 4: Handle the telemetry message in the recv loop**

In `Handler.Connect`, add a case to the `switch p := msg.Payload.(type)` block (after the `AgentMessage_WorkspaceInventory` case, around line 110):

```go
		case *relayv1.AgentMessage_Telemetry:
			h.handleTelemetry(workerID, p.Telemetry)
```

- [ ] **Step 5: Add the handleTelemetry method**

In `internal/worker/handler.go`, add this method (place it after `handleTaskLog`, around line 433):

```go
// handleTelemetry records a host-utilization sample from an agent, stamped
// with the server's receipt time.
func (h *Handler) handleTelemetry(workerID string, t *relayv1.WorkerTelemetry) {
	if h.Metrics == nil {
		return
	}
	h.Metrics.Append(workerID, metrics.Sample{
		At:             time.Now(),
		CPUPercent:     t.CpuPercent,
		MemUsedBytes:   t.MemUsedBytes,
		MemTotalBytes:  t.MemTotalBytes,
		HasGPU:         t.HasGpu,
		GPUUtilPercent: t.GpuUtilPercent,
		GPUMemUsed:     t.GpuMemUsedBytes,
		GPUMemTotal:    t.GpuMemTotalBytes,
	})
}
```

- [ ] **Step 6: Activate tracking on register**

In `finishRegister`, immediately after `h.registry.Register(workerID, sender)` (line 264):

```go
	h.registry.Register(workerID, sender)

	if h.Metrics != nil {
		h.Metrics.Activate(workerID, time.Now())
	}
```

- [ ] **Step 7: Clear tracking on disconnect**

In `markWorkerOffline`, add at the end of the function body (after the `h.broker.Publish` call, before the closing brace, around line 452):

```go
	if h.Metrics != nil {
		h.Metrics.Clear(workerID)
	}
}
```

- [ ] **Step 8: Run the tests and build**

Run: `go test ./internal/worker/... -run TestHandleTelemetry -v`
Expected: PASS — both tests.

Run: `go build ./...`
Expected: builds with no errors (no constructor signatures changed).

- [ ] **Step 9: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_telemetry_test.go
git commit -m "feat: ingest worker telemetry and track liveness in the handler"
```

---

## Task 6: REST endpoint — GET /v1/workers/{id}/metrics

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/workers.go`
- Create: `internal/api/worker_metrics.go`
- Test: `internal/api/worker_metrics_test.go`

- [ ] **Step 1: Add the Metrics field and route to the Server**

In `internal/api/server.go`, add the import `"relay/internal/metrics"` to the import block. Add a field to the `Server` struct (after `AllowSelfRegister bool` on line 32):

```go
	AllowSelfRegister bool

	// Metrics, when non-nil, supplies worker utilization history. Set by
	// cmd/relay-server after construction.
	Metrics *metrics.Store
```

In `Handler()`, add a route after the existing `GET /v1/workers/{id}` route (line 104):

```go
	mux.Handle("GET /v1/workers/{id}", auth(http.HandlerFunc(s.handleGetWorker)))
	mux.Handle("GET /v1/workers/{id}/metrics", auth(http.HandlerFunc(s.handleGetWorkerMetrics)))
```

- [ ] **Step 2: Add last_sample_at to workerResponse**

In `internal/api/workers.go`, add a field to the `workerResponse` struct (after `LastSeenAt *time.Time` on line 27):

```go
	LastSeenAt    *time.Time      `json:"last_seen_at,omitempty"`
	LastSampleAt  *time.Time      `json:"last_sample_at,omitempty"`
```

`toWorkerResponse` stays unchanged — it remains a pure mapper. `last_sample_at` is filled in by the handlers below.

In `handleGetWorker`, replace the final `writeJSON` call (line 101):

```go
	resp := toWorkerResponse(worker)
	if s.Metrics != nil {
		if at, ok := s.Metrics.LastSampleAt(uuidStr(worker.ID)); ok {
			resp.LastSampleAt = &at
		}
	}
	writeJSON(w, http.StatusOK, resp)
```

In `handleListWorkers`, replace the final two lines (`items, next := ...` and `writeJSON(...)`, lines 79-80):

```go
	items, next := buildPage(rows, pp.Limit, toWorkerResponse, workersRowKey)
	if s.Metrics != nil {
		for i := range items {
			if at, ok := s.Metrics.LastSampleAt(items[i].ID); ok {
				items[i].LastSampleAt = &at
			}
		}
	}
	writeJSON(w, http.StatusOK, page[workerResponse]{Items: items, NextCursor: next, Total: total})
```

- [ ] **Step 3: Write the failing test**

Create `internal/api/worker_metrics_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/metrics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildWorkerMetricsResponse_withSamples(t *testing.T) {
	store := metrics.NewStore(10)
	store.Activate("w1", time.Unix(0, 0))
	store.Append("w1", metrics.Sample{
		At: time.Unix(10, 0), CPUPercent: 42, MemUsedBytes: 100, MemTotalBytes: 200,
		HasGPU: true, GPUUtilPercent: 71, GPUMemUsed: 5, GPUMemTotal: 8,
	})

	resp := buildWorkerMetricsResponse("w1", store)
	assert.Equal(t, "w1", resp.WorkerID)
	assert.Equal(t, 10, resp.SampleIntervalSeconds)
	require.Len(t, resp.Samples, 1)
	assert.Equal(t, 42.0, resp.Samples[0].CPUPct)
	assert.True(t, resp.Samples[0].GPU)
	assert.Equal(t, uint64(5), resp.Samples[0].GPUMemUsed)
}

func TestBuildWorkerMetricsResponse_emptyWhenUntracked(t *testing.T) {
	resp := buildWorkerMetricsResponse("ghost", metrics.NewStore(10))
	assert.Equal(t, []metricSampleResponse{}, resp.Samples)
}

func TestBuildWorkerMetricsResponse_nilStore(t *testing.T) {
	resp := buildWorkerMetricsResponse("w1", nil)
	assert.Equal(t, []metricSampleResponse{}, resp.Samples)
}

func TestHandleGetWorkerMetrics_invalidID(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/v1/workers/not-a-uuid/metrics", nil)
	req.SetPathValue("id", "not-a-uuid")
	rec := httptest.NewRecorder()

	s.handleGetWorkerMetrics(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "invalid worker id", body["error"])
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/api/... -run "WorkerMetrics" -v`
Expected: FAIL — `undefined: buildWorkerMetricsResponse`, `undefined: metricSampleResponse`, `undefined: handleGetWorkerMetrics`.

- [ ] **Step 5: Write the handler implementation**

Create `internal/api/worker_metrics.go`:

```go
package api

import (
	"errors"
	"net/http"
	"time"

	"relay/internal/metrics"

	"github.com/jackc/pgx/v5"
)

type metricSampleResponse struct {
	T           time.Time `json:"t"`
	CPUPct      float64   `json:"cpu_pct"`
	MemUsed     uint64    `json:"mem_used"`
	MemTotal    uint64    `json:"mem_total"`
	GPU         bool      `json:"gpu"`
	GPUUtilPct  float64   `json:"gpu_util_pct"`
	GPUMemUsed  uint64    `json:"gpu_mem_used"`
	GPUMemTotal uint64    `json:"gpu_mem_total"`
}

type workerMetricsResponse struct {
	WorkerID              string                 `json:"worker_id"`
	SampleIntervalSeconds int                    `json:"sample_interval_seconds"`
	Samples               []metricSampleResponse `json:"samples"`
}

// buildWorkerMetricsResponse assembles the JSON payload from the ring buffer.
// A nil store or an untracked worker yields an empty (non-nil) sample slice.
func buildWorkerMetricsResponse(workerID string, store *metrics.Store) workerMetricsResponse {
	samples := []metricSampleResponse{}
	if store != nil {
		for _, s := range store.Snapshot(workerID) {
			samples = append(samples, metricSampleResponse{
				T:           s.At,
				CPUPct:      s.CPUPercent,
				MemUsed:     s.MemUsedBytes,
				MemTotal:    s.MemTotalBytes,
				GPU:         s.HasGPU,
				GPUUtilPct:  s.GPUUtilPercent,
				GPUMemUsed:  s.GPUMemUsed,
				GPUMemTotal: s.GPUMemTotal,
			})
		}
	}
	return workerMetricsResponse{
		WorkerID:              workerID,
		SampleIntervalSeconds: int(metrics.DefaultSampleInterval / time.Second),
		Samples:               samples,
	}
}

// handleGetWorkerMetrics serves a worker's short-term utilization history.
func (s *Server) handleGetWorkerMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}

	if _, err := s.q.GetWorker(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	writeJSON(w, http.StatusOK, buildWorkerMetricsResponse(uuidStr(id), s.Metrics))
}
```

- [ ] **Step 6: Run the tests and build**

Run: `go test ./internal/api/... -run "WorkerMetrics" -v`
Expected: PASS — all four tests.

Run: `go build ./...`
Expected: builds with no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/api/server.go internal/api/workers.go internal/api/worker_metrics.go internal/api/worker_metrics_test.go
git commit -m "feat: add GET /v1/workers/{id}/metrics endpoint"
```

---

## Task 7: Wire everything in main

**Files:**
- Modify: `cmd/relay-server/main.go`
- Modify: `cmd/relay-agent/main.go`

- [ ] **Step 1: Build the metrics store and sweeper in relay-server**

In `cmd/relay-server/main.go`, add the import `"relay/internal/metrics"` to the import block.

After the `registry := worker.NewRegistry()` line (line 94), add:

```go
	registry := worker.NewRegistry()

	telemetryWindow := 30 * time.Minute
	if v := os.Getenv("RELAY_TELEMETRY_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			telemetryWindow = d
		}
	}
	staleAfter := 30 * time.Second
	if v := os.Getenv("RELAY_TELEMETRY_STALE_AFTER"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			staleAfter = d
		}
	}
	metricsStore := metrics.NewStore(int(telemetryWindow / metrics.DefaultSampleInterval))
```

- [ ] **Step 2: Attach the store to the agent handler**

In `cmd/relay-server/main.go`, after the `agentHandler := worker.NewHandlerWithGrace(...)` line (line 121):

```go
	agentHandler := worker.NewHandlerWithGrace(q, pool, registry, broker, dispatcher.Trigger, grace)
	agentHandler.Metrics = metricsStore
```

- [ ] **Step 3: Attach the store to the HTTP server**

In `cmd/relay-server/main.go`, after the `httpServer := api.New(...)` line (line 137):

```go
	httpServer := api.New(pool, q, broker, registry, corsOrigins, loginN, loginWin, registerN, registerWin)
	httpServer.Metrics = metricsStore
```

- [ ] **Step 4: Start the liveness sweeper**

In `cmd/relay-server/main.go`, after the `go schedrunner.NewRunner(pool, q).Run(ctx)` line (line 168):

```go
	go schedrunner.NewRunner(pool, q).Run(ctx)

	// Mark connected-but-silent workers stale based on telemetry age.
	go metrics.NewSweeper(q, broker, metricsStore, staleAfter).Run(ctx)
```

- [ ] **Step 5: Parse the agent telemetry interval**

In `cmd/relay-agent/main.go`, locate the `a := agent.NewAgent(...)` call (line 103) and the following `a.Run(ctx)` (line 107). Insert between them:

```go
	if v := os.Getenv("RELAY_TELEMETRY_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			a.TelemetryInterval = d
		}
	}
	a.Run(ctx)
```

If `os` or `time` is not already imported in `cmd/relay-agent/main.go`, add it to the import block.

- [ ] **Step 6: Build and run the full unit test suite**

Run: `go build ./...`
Expected: builds with no errors.

Run: `make test`
Expected: all unit tests pass.

- [ ] **Step 7: Commit**

```bash
git add cmd/relay-server/main.go cmd/relay-agent/main.go
git commit -m "feat: wire telemetry store, sweeper, and agent sampler"
```

---

## Task 8: Documentation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document the new environment variables**

In `README.md`, find the section listing environment variables. Add these three rows/entries, matching the file's existing format:

- `RELAY_TELEMETRY_INTERVAL` — agent host-utilization sampling cadence. Default `10s`.
- `RELAY_TELEMETRY_WINDOW` — server-side retention window for the in-memory utilization ring buffer. Default `30m`.
- `RELAY_TELEMETRY_STALE_AFTER` — server-side threshold; a connected worker with no telemetry for longer than this is marked `stale`. Default `30s`. Must be greater than `RELAY_TELEMETRY_INTERVAL`.

- [ ] **Step 2: Document the new endpoint and status**

In `README.md`, in the REST API section, add an entry for `GET /v1/workers/{id}/metrics` describing that it returns the worker's short-term utilization history (`samples` array with `cpu_pct`, `mem_used`/`mem_total`, `gpu` flag and GPU fields; empty for an offline or untracked worker).

Where worker `status` values are described, add `stale` ("connected but no recent telemetry") alongside `online`/`offline`.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document worker telemetry env vars and endpoint"
```

---

## Self-Review

**Spec coverage:**
- Architecture & data flow → Tasks 1-7. ✓
- Proto change (`WorkerTelemetry`, oneof tag 5, server-stamped receipt time) → Task 1; receipt time stamped in `handleTelemetry` (Task 5). ✓
- Agent sampler (CPU delta via `cpu.Percent(0,…)`, mem, nvidia-smi GPU, drop-on-full `sendCh`, never crash) → Task 4. ✓
- metrics.Store lifecycle (`Activate`/`Append`/`Clear`/`Snapshot`/`LastSampleAt`, cold-start activation time) → Tasks 2, 5. ✓
- Stale sweeper (10s ticker, `ListWorkersByLiveness`, online↔stale, SSE publish, no requeue, no registry dep) → Task 3, started in Task 7. ✓
- Worker status enum gains `stale`; no migration (column is plain `TEXT`, no CHECK constraint — verified in `000001_initial.up.sql`). ✓
- REST contract (`GET /workers/{id}/metrics`, 404 unknown, 200+empty offline, `last_sample_at` on `workerResponse` and list) → Task 6. ✓
- Config (`RELAY_TELEMETRY_INTERVAL`/`_WINDOW`/`_STALE_AFTER`) → Tasks 4, 7; documented Task 8. ✓
- Error handling (sampling failure → zero fields; `sendCh` full → drop; malformed telemetry ignored; 404) → Tasks 4, 5, 6. ✓
- Testing (sampler, store, sweeper, handler, REST) → each task is TDD. ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code. ✓

**Type consistency:** `metrics.Sample` field names (`CPUPercent`, `MemUsedBytes`, `MemTotalBytes`, `HasGPU`, `GPUUtilPercent`, `GPUMemUsed`, `GPUMemTotal`) used identically in Tasks 2, 3, 5, 6. Proto field accessors (`CpuPercent`, `MemUsedBytes`, `HasGpu`, `GpuUtilPercent`, `GpuMemUsedBytes`, `GpuMemTotalBytes`) consistent across Tasks 1, 4, 5. `Store` methods and `Sweeper`/`NewSweeper` signatures consistent across Tasks 2, 3, 7. `buildWorkerMetricsResponse`/`metricSampleResponse`/`workerMetricsResponse` consistent within Task 6. ✓

**Note for the implementer:** the metrics store keys workers by the canonical lowercase-hyphenated UUID string. `worker.finishRegister` uses `uuidStr(updated.ID)`, the sweeper uses `uuidString(w.ID)`, and the REST handler uses `uuidStr(id)` — all produce the same format. Do not key by raw request path values.
