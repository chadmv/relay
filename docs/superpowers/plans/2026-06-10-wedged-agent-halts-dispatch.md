# Wedged Agent Halts Dispatch - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop a single wedged agent connection from freezing task dispatch for the entire farm.

**Architecture:** Two surgical changes. (1) Bound the blocking enqueue in `workerSender.Send` with a ~5s timeout so no single send blocks forever. (2) Configure server-side gRPC keepalive so a wedged/half-open stream is torn down (~40s), unblocking the parked send goroutine. A one-line dispatcher log makes wedged workers diagnosable.

**Tech Stack:** Go, `google.golang.org/grpc` + `google.golang.org/grpc/keepalive`, testify.

**Spec:** `docs/superpowers/specs/2026-06-10-wedged-agent-halts-dispatch-design.md`

---

## File Structure

- Modify: `internal/worker/sender.go` - add `ErrSendTimeout`, `sendTimeout` var, timeout select in `Send`, file-wide field rename.
- Create: `internal/worker/export_sender_test.go` - test-only hook to override `sendTimeout` (internal `package worker`, no build tag).
- Modify: `internal/worker/sender_test.go` - add two new tests (full-buffer timeout, closed-mid-wait).
- Modify: `cmd/relay-server/main.go:176` - keepalive params on `grpc.NewServer`.
- Modify: `internal/scheduler/dispatch.go:262` - add a log line on send failure.

---

## Task 1: Bound `workerSender.Send` with a timeout

**Files:**
- Modify: `internal/worker/sender.go`
- Create: `internal/worker/export_sender_test.go`
- Test: `internal/worker/sender_test.go`

This task does the rename and the timeout together because the new `Send` body
uses the renamed fields; splitting them would leave the file half-renamed
between commits.

- [ ] **Step 1: Add the test-only override hook**

Create `internal/worker/export_sender_test.go` (internal test package, no build tag - it compiles into the same test binary as the external `worker_test` tests and lets them lower the timeout):

```go
package worker

import (
	"testing"
	"time"
)

// SetSendTimeoutForTest lowers the Send buffer-full timeout for the duration of
// t. Restores the previous value on cleanup.
func SetSendTimeoutForTest(t *testing.T, d time.Duration) {
	t.Helper()
	prev := sendTimeout
	sendTimeout = d
	t.Cleanup(func() { sendTimeout = prev })
}
```

- [ ] **Step 2: Write the failing tests**

Add to the end of `internal/worker/sender_test.go`. Also add `"errors"` to that file's import block (used by the new stub) - the current imports are `"sync"`, `"testing"`, `"time"`, the relayv1/worker packages, and testify assert/require.

```go
// wedgedStream simulates an agent whose stream blocks: the send goroutine parks
// inside Send until release is closed, then Send returns an error (mimicking the
// transport dying). entered is closed the first time Send is reached.
type wedgedStream struct {
	entered   chan struct{}
	release   chan struct{}
	onceEnter sync.Once
}

func (s *wedgedStream) Send(*relayv1.CoordinatorMessage) error {
	s.onceEnter.Do(func() { close(s.entered) })
	<-s.release
	return errors.New("stream closed")
}

func newWedgedStream() *wedgedStream {
	return &wedgedStream{entered: make(chan struct{}), release: make(chan struct{})}
}

// fillBuffer parks the send goroutine inside Send and fills the 64-slot buffer,
// so the next Send call has nowhere to go.
func fillBuffer(t *testing.T, ws worker.Sender, stream *wedgedStream) {
	t.Helper()
	require.NoError(t, ws.Send(&relayv1.CoordinatorMessage{})) // pulled by loop, parks in Send
	<-stream.entered                                           // loop goroutine now blocked inside Send
	for i := 0; i < 64; i++ {
		require.NoError(t, ws.Send(&relayv1.CoordinatorMessage{})) // fills the buffer
	}
}

func TestWorkerSender_TimesOutWhenBufferFull(t *testing.T) {
	worker.SetSendTimeoutForTest(t, 50*time.Millisecond)
	stream := newWedgedStream()
	ws := worker.NewWorkerSender(stream)
	t.Cleanup(func() { close(stream.release); ws.Close() })

	fillBuffer(t, ws, stream)

	// Run the blocking send in a goroutine so the test fails fast (rather than
	// hanging) against the unfixed code.
	errc := make(chan error, 1)
	go func() { errc <- ws.Send(&relayv1.CoordinatorMessage{}) }()
	select {
	case err := <-errc:
		require.ErrorIs(t, err, worker.ErrSendTimeout)
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not return; it blocked forever on a full buffer")
	}
}

func TestWorkerSender_ReturnsDisconnectedWhenStreamDiesMidWait(t *testing.T) {
	worker.SetSendTimeoutForTest(t, 5*time.Second) // long; the stream dies first
	stream := newWedgedStream()
	ws := worker.NewWorkerSender(stream)
	t.Cleanup(func() { ws.Close() })

	fillBuffer(t, ws, stream)

	// A send blocked on the full buffer...
	errc := make(chan error, 1)
	go func() { errc <- ws.Send(&relayv1.CoordinatorMessage{}) }()

	// ...then the stream dies: the parked Send returns an error, the loop exits
	// and closes the sender. With no consumer draining the full buffer, the
	// blocked Send must observe the closed sender, not free space.
	time.Sleep(50 * time.Millisecond)
	close(stream.release)

	select {
	case err := <-errc:
		require.ErrorIs(t, err, worker.ErrWorkerDisconnected)
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not return after the stream died")
	}
}
```

- [ ] **Step 3: Run the new tests to verify they fail**

Run: `go test ./internal/worker/ -run 'TestWorkerSender_TimesOutWhenBufferFull|TestWorkerSender_ReturnsDisconnectedWhenStreamDiesMidWait' -v -timeout 30s`

Expected: FAIL. `TestWorkerSender_TimesOutWhenBufferFull` fails because `worker.ErrSendTimeout` is undefined (compile error) - that is expected; it is defined in Step 4. (Once Step 4 lands, the unfixed `Send` would make this test hit its 2s `t.Fatal`.)

- [ ] **Step 4: Rewrite `sender.go` with the rename and timeout**

Replace the entire contents of `internal/worker/sender.go` with:

```go
package worker

import (
	"errors"
	"sync"
	"time"

	relayv1 "relay/internal/proto/relayv1"
)

// ErrWorkerDisconnected is returned when a send is attempted on a closed sender.
var ErrWorkerDisconnected = errors.New("worker disconnected")

// ErrSendTimeout is returned when a send cannot be enqueued before sendTimeout
// elapses, which happens when the buffer is full because the send goroutine is
// wedged inside stream.Send on a dead peer.
var ErrSendTimeout = errors.New("worker send timed out")

// sendTimeout bounds how long Send waits for buffer space. It is a var (not a
// const) so tests can lower it via SetSendTimeoutForTest.
var sendTimeout = 5 * time.Second

// workerSender serializes all writes to a gRPC stream through a single
// send goroutine. gRPC bidirectional streams are not concurrent-send-safe.
type workerSender struct {
	stream  Sender
	queue   chan *relayv1.CoordinatorMessage
	stopReq chan struct{}
	closed  chan struct{}
	once    sync.Once
}

// NewWorkerSender wraps a raw stream in a send goroutine and returns a Sender
// that is safe for concurrent use. Call Close when the underlying stream ends.
func NewWorkerSender(stream Sender) *workerSender {
	sender := &workerSender{
		stream:  stream,
		queue:   make(chan *relayv1.CoordinatorMessage, 64),
		stopReq: make(chan struct{}),
		closed:  make(chan struct{}),
	}
	go sender.loop()
	return sender
}

func (sender *workerSender) loop() {
	defer close(sender.closed)
	for {
		select {
		case msg := <-sender.queue:
			if err := sender.stream.Send(msg); err != nil {
				return
			}
		case <-sender.stopReq:
			return
		}
	}
}

// Send enqueues message for delivery. Blocks if the internal buffer is full
// until space is available, the sender is closed, or sendTimeout elapses.
func (sender *workerSender) Send(message *relayv1.CoordinatorMessage) error {
	// Check closed first (non-blocking) to give a deterministic closed signal.
	select {
	case <-sender.closed:
		return ErrWorkerDisconnected
	default:
	}
	timeout := time.NewTimer(sendTimeout)
	defer timeout.Stop()
	select {
	case sender.queue <- message:
		return nil
	case <-sender.closed:
		return ErrWorkerDisconnected
	case <-timeout.C:
		return ErrSendTimeout
	}
}

// Close stops the send loop. Safe to call multiple times.
func (sender *workerSender) Close() {
	sender.once.Do(func() { close(sender.stopReq) })
	<-sender.closed
}
```

- [ ] **Step 5: Run the worker package tests to verify they pass**

Run: `go test ./internal/worker/ -v -timeout 60s`

Expected: PASS, including the two new tests and the pre-existing `TestWorkerSender_SerializesConcurrentSends` and `TestWorkerSender_SendAfterClose` (the rename must not break them).

- [ ] **Step 6: Commit**

```bash
git add internal/worker/sender.go internal/worker/export_sender_test.go internal/worker/sender_test.go
git commit -m "fix: bound workerSender.Send with a timeout"
```

---

## Task 2: Configure server-side gRPC keepalive

**Files:**
- Modify: `cmd/relay-server/main.go` (import block + line 176)

No unit test - `keepalive.ServerParameters` is gRPC-internal config with no
unit-testable behavior (see spec). Verification is a successful build.

- [ ] **Step 1: Add the keepalive import**

In `cmd/relay-server/main.go`, the third-party import group currently ends with:

```go
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
```

Add the keepalive subpackage:

```go
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
```

- [ ] **Step 2: Pass keepalive params to `grpc.NewServer`**

Replace line 176:

```go
	grpcSrv := grpc.NewServer()
```

with:

```go
	grpcSrv := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second, // ping after 30s of transport inactivity
			Timeout: 10 * time.Second, // close the transport if no ack within 10s
		}),
	)
```

(`time` is already imported.)

- [ ] **Step 3: Build to verify it compiles**

Run: `go build ./cmd/relay-server/`

Expected: success, no output.

- [ ] **Step 4: Commit**

```bash
git add cmd/relay-server/main.go
git commit -m "fix: add server-side gRPC keepalive to detect wedged agents"
```

---

## Task 3: Log dispatch send failures

**Files:**
- Modify: `internal/scheduler/dispatch.go` (around line 262)

- [ ] **Step 1: Add a log line on send failure**

In `internal/scheduler/dispatch.go`, the current `sendTask` send block is:

```go
	if err := d.registry.Send(uuidStr(w.ID), msg); err != nil {
		// Worker disappeared between claim and send; revert so another pass
		// (or another worker) can pick the task up.
		_ = d.q.RequeueTask(ctx, claimed.ID)
		return false
	}
```

Replace it with:

```go
	if err := d.registry.Send(uuidStr(w.ID), msg); err != nil {
		// Worker disappeared or is wedged between claim and send; revert so
		// another pass (or another worker) can pick the task up.
		log.Printf("dispatch: send to worker %s failed: %v; requeueing task %s", uuidStr(w.ID), err, claimed.ID)
		_ = d.q.RequeueTask(ctx, claimed.ID)
		return false
	}
```

(`log` is already imported in this file.)

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./internal/scheduler/`

Expected: success, no output.

- [ ] **Step 3: Run scheduler tests to confirm no regression**

Run: `go test ./internal/scheduler/ -timeout 60s`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/scheduler/dispatch.go
git commit -m "chore: log dispatch send failures for wedged-worker diagnosis"
```

---

## Task 4: Full build and verification

**Files:** none (verification only)

- [ ] **Step 1: Build all binaries**

Run: `make build`

Expected: success; `bin/relay-server`, `bin/relay-agent`, `bin/relay` produced.

- [ ] **Step 2: Run the full unit test suite**

Run: `make test`

Expected: PASS across all packages.

- [ ] **Step 3: Confirm the regression is real (optional sanity check)**

Temporarily revert only the `Send` body in `sender.go` to its old two-case
select (no `timeout` case), run
`go test ./internal/worker/ -run TestWorkerSender_TimesOutWhenBufferFull -v -timeout 30s`,
confirm it FAILS via the 2s `t.Fatal`, then restore the fix. This proves the
test guards the bug. (Skip if you are confident.)

---

## Self-Review Notes

- **Spec coverage:** Change 1 (bounded send) -> Task 1. Change 2 (keepalive) ->
  Task 2. Change 3 (dispatcher log) -> Task 3. Testing section -> Task 1 tests +
  Task 4. HTTP handlers: spec says no change, so no task - correct.
- **Out-of-scope items** (epoch bump, agent keepalive, stale teardown) are
  intentionally untouched; no task references them.
- **Type consistency:** `ErrSendTimeout`, `sendTimeout`, `SetSendTimeoutForTest`,
  and the renamed fields (`queue`, `stopReq`, `closed`, receiver `sender`) are
  used identically across `sender.go`, `export_sender_test.go`, and
  `sender_test.go`.
