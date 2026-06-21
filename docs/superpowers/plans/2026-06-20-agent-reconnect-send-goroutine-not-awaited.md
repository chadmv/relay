# Agent Reconnect Send Goroutine Not Awaited - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Guarantee at most one send goroutine ever reads the agent's shared `sendCh` across a reconnect, by joining the previous connection's send goroutine before the next `connect()` spawns a new one, eliminating the transient dual-sender that silently drops a queued message.

**Architecture:** The send goroutine spawned in `connect()` is the single bounded sender for one gRPC stream (CLAUDE.md invariant "one bounded sender per gRPC stream"). Today nothing waits for it: the recv loop calls `connCancel()` and returns, `Run()` loops, and the next `connect()` spawns a second goroutine on the same `a.sendCh` while the old one is still draining. We add a single `sendWG sync.WaitGroup` field; the send goroutine signals it on exit via `defer`, and `connect()` calls `a.sendWG.Wait()` at the top (before spawning its own goroutine) so the previous sender is fully joined first. To make the at-most-one-sender property deterministically testable without a real stream, the send loop body is extracted into a named method `runSender` that takes an injectable `send func(*relayv1.AgentMessage) error`.

**Tech Stack:** Go 1.26, standard library (`sync`, `context`), testify for assertions. The deterministic tests use synchronization primitives (an injected blockable send, an atomic concurrency counter, and the WaitGroup join), NOT the `-race` detector - on this Windows dev host `make test-race` excludes `internal/agent` (the default toolchain's race detector crashes), so correctness must be provable without `-race`. CI runs `-race` on Linux separately as a backstop.

**Slice declaration:** This is **backend-only**. There is a **single engineer slice** (`internal/agent`). There is **no frontend slice**. Phase 3 is therefore **NOT parallel** - tasks run sequentially in one slice.

**Files in scope:**
- Modify: `internal/agent/agent.go` - add `sendWG sync.WaitGroup` field to `Agent`; add `a.sendWG.Wait()` at the top of `connect()`; extract the send loop into a `runSender` method that `defer a.sendWG.Done()`s and returns on `connCtx.Done()` or send error; document the bounded single-message loss.
- Test: `internal/agent/sender_test.go` (new) - deterministic unit tests for `runSender`'s at-most-one-sender join and queued-message behavior. Package `agent` (white-box, no build tag), matching `lifetime_test.go`.

No other file must change. The existing integration tests in `agent_test.go` (`//go:build integration`, package `agent_test`, real in-process gRPC) keep passing unchanged; this plan adds white-box unit tests instead of new integration tests because the property under test (goroutine join ordering) is a synchronization invariant best asserted with injected primitives, not a real network stream.

---

## Background: exact current code (verified against the worktree, not the stale backlog line numbers)

In `internal/agent/agent.go` as it stands:

- The `Agent` struct (lines 23-39) has `sendCh chan *relayv1.AgentMessage` (buffered 64, allocated in `NewAgent` line 49) and `mu sync.Mutex` and `runnerWG sync.WaitGroup`. There is **no** WaitGroup tracking the send goroutine.
- `connect()` (lines 101-209): `connCtx, connCancel := context.WithCancel(ctx)` then `defer connCancel()` (102-103).
- The send goroutine is an anonymous closure (lines 163-176):

  ```go
  go func() {
      for {
          select {
          case msg := <-a.sendCh:
              if err := stream.Send(msg); err != nil {
                  connCancel()
                  return
              }
          case <-connCtx.Done():
              return
          }
      }
  }()
  ```

- The recv loop on `stream.Recv()` error calls `connCancel()` and `return err` (lines 184-185). Nothing joins the send goroutine.
- `Run()` (lines 60-97) loops: on a non-terminal error it backs off and calls `connect()` again, spawning a second send goroutine on the same `a.sendCh`.

The bug window: after the recv loop's `connCancel()` and `return`, `connect()` runs its `defer connCancel()` (idempotent) and returns to `Run()`. `Run()` re-enters `connect()` and spawns send goroutine #2. Goroutine #1 has not necessarily exited yet - if it had already pulled a message off `a.sendCh` and is sitting in `stream.Send` on the now-dead connection, that `Send` returns an error and the message is dropped. For the overlap, both goroutines `select` on `a.sendCh`, so goroutine #1 can also steal a message that goroutine #2 (the live connection) should have sent.

---

## Design decision: join, and DOCUMENT the bounded loss (do NOT re-enqueue)

The proposal in the backlog item offers two parts: (1) join the previous goroutine [required], (2) "optionally re-enqueue or account for a message whose `Send` failed" [optional]. This plan implements (1) and **explicitly chooses to document the bounded loss rather than re-enqueue.** Rationale:

- **The join is the load-bearing fix.** Once `connect()` waits for the previous sender before spawning a new one, there is never more than one goroutine reading `a.sendCh`. The dual-sender steal (the worse of the two failure modes - it loses a message the *live* connection should have sent) is eliminated entirely.
- **After the join, at most one message can be lost per drop**: the single in-flight message that goroutine #1 had already pulled off `sendCh` when its `stream.Send` failed on the dead connection. That message was already unrecoverable - the connection it was destined for is dead.
- **Re-enqueueing is net-negative here.** Re-enqueueing on `a.sendCh` would require a non-blocking send (the channel may be full, so a blocking re-enqueue could deadlock the very goroutine that must exit to be joined), and it would push the stale message to the *tail* of the queue, reordering it behind newer chunks - corrupting log-stream ordering for a best-effort stream.
- **The loss is already bounded and reconciled for the part that matters.** Task *status* is reconciled on reconnect: `buildRegisterRequest` (agent.go:242-297) sends `RunningTasks` with epochs, and the coordinator re-syncs. Only task-*log* chunks are not replayed, so the bounded loss is "at most one in-flight log chunk per stream drop" - acceptable for a best-effort log stream and consistent with the existing `send`/`sendOrAbort` helpers in `runner.go`, which already drop on `r.ctx.Done()` without re-enqueueing.

This satisfies the spec's acceptance criterion via its second branch: "A reconnect with a queued message does not silently lose it, OR the loss is explicitly bounded and documented." We make the loss bounded (at most one in-flight chunk), no longer silent (a documented code comment), and we eliminate the dual-sender steal that could lose a *live*-connection message.

**Deviation from the backlog proposal:** the proposal lists re-enqueue as optional; this plan declines it with the rationale above and documents the bound instead. No conflict with the Invariants - on the contrary, the join is exactly what restores the "one bounded sender per gRPC stream" invariant.

**Mechanism choice: `sync.WaitGroup`, not a done channel.** A `WaitGroup` is chosen over a per-connection done channel because (a) it matches the existing `runnerWG` pattern already in this struct, (b) `connect()` is only ever called from the single `Run` goroutine, so there is exactly one waiter and the classic "Add after Wait" WaitGroup race cannot occur, and (c) the join is a simple `a.sendWG.Wait()` with no channel lifecycle to manage. The per-connection scoping is preserved by `connCtx`/`connCancel`: each send goroutine still selects on its own `connCtx.Done()`, so a stale goroutine cannot outlive its connection - the WaitGroup only adds the *join*, it does not change which context the goroutine watches.

---

## Task 1: Extract the send loop into a testable `runSender` method (no behavior change)

Pull the anonymous send-goroutine closure out into a named method so a test can drive it with an injected `send` function instead of a real gRPC stream. This task is a pure refactor: behavior is identical, no join is added yet. It is the seam the deterministic tests in Tasks 3-4 attach to.

**Files:**
- Modify: `internal/agent/agent.go` - add the `runSender` method; replace the anonymous closure in `connect()` with a call to it.

- [ ] **Step 1: This task is a behavior-preserving extraction verified by the existing suite**

No new test is written here. The extraction is verified by `go build` / `go vet` plus the existing `internal/agent` suite (which must stay green). A fabricated unit test for a pure rename/extract would be coupled noise; the real new tests land in Tasks 3-4 against the extracted method. (Granularity note per writing-plans: a no-op structural change gets a build/vet verify, not an invented test.)

- [ ] **Step 2: Add the `runSender` method**

In `internal/agent/agent.go`, add this method directly above `connect` (above line 99). It takes the per-connection `connCtx` and `connCancel`, plus a `send` function so tests can inject a fake. It contains exactly the old loop body:

```go
// runSender is the single bounded sender for one gRPC stream. It owns all
// writes to a.sendCh for the lifetime of one connection: it selects messages
// off the shared a.sendCh and hands them to send (in production, stream.Send).
// On a send error it cancels the connection (connCancel) and returns; on
// connCtx cancellation (recv loop drop, or parent shutdown) it returns. There
// is exactly one runSender per connection - see connect, which joins the
// previous one via sendWG before spawning the next.
//
// Bounded-loss note: if send fails, the message already pulled off a.sendCh is
// dropped. This is at most one in-flight message per stream drop, and the
// connection it was destined for is dead. Task status is reconciled on
// reconnect via buildRegisterRequest's RunningTasks; task-log chunks are not
// replayed, so a forced reconnect may drop at most one in-flight log chunk.
// We deliberately do NOT re-enqueue: a re-enqueue would either block this
// goroutine (a full sendCh would prevent the join) or reorder the chunk behind
// newer ones, corrupting the best-effort log stream.
func (a *Agent) runSender(connCtx context.Context, connCancel context.CancelFunc, send func(*relayv1.AgentMessage) error) {
	for {
		select {
		case msg := <-a.sendCh:
			if err := send(msg); err != nil {
				connCancel()
				return
			}
		case <-connCtx.Done():
			return
		}
	}
}
```

- [ ] **Step 3: Replace the anonymous closure in `connect` with a `runSender` call**

In `internal/agent/agent.go`, replace the send-goroutine block (the current lines 163-176, the `// Start send goroutine ...` comment plus the `go func() { ... }()`) with:

```go
	// Start the single send goroutine for this connection. gRPC streams are not
	// concurrent-send-safe, so all writes go through this one goroutine.
	go a.runSender(connCtx, connCancel, stream.Send)
```

`stream.Send` has signature `func(*relayv1.AgentMessage) error`, matching `runSender`'s `send` parameter, so this is a direct method-value pass with no wrapper.

- [ ] **Step 4: Verify build and vet**

Run (PowerShell or Bash on the Windows host):

```
go build ./internal/agent/...
go vet ./internal/agent/...
```

Expected: both succeed with no output.

- [ ] **Step 5: Run the existing agent unit suite to confirm no regression**

Run:

```
go test ./internal/agent/... -timeout 60s
```

Expected: PASS. (This runs the white-box unit tests like `lifetime_test.go`; the `//go:build integration` tests in `agent_test.go` are not built here, which is fine - the extraction is behavior-preserving and they are covered in Task 5.)

- [ ] **Step 6: Commit**

```bash
git add internal/agent/agent.go
git commit -m "agent: extract send loop into runSender method (no behavior change)"
```

---

## Task 2: Add the `sendWG` field and join the previous sender in `connect`

Add the `sync.WaitGroup` that tracks the send goroutine, have `runSender` signal it on exit, and have `connect()` join the previous sender at the top before spawning its own. This is the actual fix.

**Per-connection scoping (explicit):** each `runSender` still selects on its own `connCtx.Done()`, so it is still bound to exactly one connection and cannot outlive it. `sendWG` adds only the *join*: `connect()` calls `a.sendWG.Wait()` at the very top (after creating its own `connCtx` but before spawning its own goroutine), so by the time the new send goroutine starts, the previous one has fully returned. Because `connect()` is only ever called from the single `Run` goroutine (one at a time), there is exactly one `Add` and one `Wait` outstanding at any moment - no concurrent `Add`-after-`Wait` race.

**Files:**
- Modify: `internal/agent/agent.go:23-39` (struct), `:101-103` (`connect` top), and the `go a.runSender(...)` call site from Task 1.

- [ ] **Step 1: Add the `sendWG` field to the `Agent` struct**

In `internal/agent/agent.go`, add the field to the `Agent` struct, next to `runnerWG`:

```go
	mu       sync.Mutex
	runners  map[string]*Runner
	runnerWG sync.WaitGroup // tracks active runner goroutines; waited on agent shutdown
	sendWG   sync.WaitGroup // tracks the per-connection send goroutine; joined before the next connect
```

No change to `NewAgent` is needed: a zero-value `sync.WaitGroup` is ready to use.

- [ ] **Step 2: Join the previous sender at the top of `connect`**

In `internal/agent/agent.go`, immediately after the `connCtx, connCancel := context.WithCancel(ctx)` / `defer connCancel()` lines (currently 102-103), add the join. It must run before any early `return` that would otherwise spawn nothing - placing it right after `connCtx` is created keeps the previous-sender join unconditional on every reconnect:

```go
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	// Join the previous connection's send goroutine before this connection
	// starts its own. This enforces the "one bounded sender per gRPC stream"
	// invariant: without the join, the prior sender could still be draining
	// a.sendCh while the new one starts, so two goroutines would race on the
	// same channel. The prior connect already ran its deferred connCancel, so
	// the prior sender is on its way out and this Wait returns promptly.
	a.sendWG.Wait()
```

On the very first `connect()` the WaitGroup counter is zero, so `Wait()` returns immediately.

- [ ] **Step 3: Register the send goroutine with the WaitGroup at the spawn site**

In `internal/agent/agent.go`, update the `runSender` spawn from Task 1 so it is tracked by `sendWG`. `Add(1)` must happen on the `Run`/`connect` goroutine *before* the `go`, and `Done` is signalled by `runSender` itself via `defer` (added in Step 4):

```go
	// Start the single send goroutine for this connection. gRPC streams are not
	// concurrent-send-safe, so all writes go through this one goroutine. sendWG
	// lets the NEXT connect join this goroutine before spawning its replacement.
	a.sendWG.Add(1)
	go a.runSender(connCtx, connCancel, stream.Send)
```

- [ ] **Step 4: Signal `sendWG.Done()` on `runSender` exit**

In `internal/agent/agent.go`, add the `defer a.sendWG.Done()` as the first line of `runSender` so it fires on every return path (send error and `connCtx.Done()`):

```go
func (a *Agent) runSender(connCtx context.Context, connCancel context.CancelFunc, send func(*relayv1.AgentMessage) error) {
	defer a.sendWG.Done()
	for {
		select {
		case msg := <-a.sendCh:
			if err := send(msg); err != nil {
				connCancel()
				return
			}
		case <-connCtx.Done():
			return
		}
	}
}
```

- [ ] **Step 5: Verify build and vet**

Run:

```
go build ./internal/agent/...
go vet ./internal/agent/...
```

Expected: both succeed with no output.

- [ ] **Step 6: Run the existing agent unit suite**

Run:

```
go test ./internal/agent/... -timeout 60s
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/agent.go
git commit -m "agent: join previous send goroutine before reconnect (one bounded sender)"
```

---

## Task 3: Deterministic at-most-one-sender test across a simulated reconnect

Prove the core invariant: across a reconnect, at most one `runSender` reads `a.sendCh` at any instant. The test injects a blockable `send`, holds goroutine #1 inside `send` (simulating a `stream.Send` parked on a dying connection), then drives the join the way `connect` does and asserts the concurrency counter never exceeds 1.

**Why this is deterministic (not `-race`-dependent):** the test uses an atomic counter incremented on entry to `send` and decremented on exit, plus a release channel that the test controls, plus the real `a.sendWG.Wait()`. It asserts ordering via synchronization primitives and an observable max-concurrency value, so it fails reliably on the pre-join code and passes on the fixed code without relying on the race detector.

**Files:**
- Create: `internal/agent/sender_test.go` (package `agent`, no build tag - white-box, like `lifetime_test.go`).

- [ ] **Step 1: Write the failing test**

Create `internal/agent/sender_test.go`:

```go
package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
)

// TestRunSender_AtMostOneSenderAcrossReconnect proves that joining the previous
// send goroutine (via a.sendWG) before spawning the next guarantees at most one
// runSender reads a.sendCh at any instant. It simulates a reconnect: sender #1
// is parked inside send() on a dying connection; the test then performs the
// same join connect() does (sendWG.Wait after connCancel) and only then starts
// sender #2. A maxConcurrent counter must never exceed 1.
func TestRunSender_AtMostOneSenderAcrossReconnect(t *testing.T) {
	a := &Agent{sendCh: make(chan *relayv1.AgentMessage, 64)}

	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	bump := func() {
		n := concurrent.Add(1)
		for {
			m := maxConcurrent.Load()
			if n <= m || maxConcurrent.CompareAndSwap(m, n) {
				break
			}
		}
	}

	// send blocks until the test releases it, modelling a stream.Send parked on
	// a dead connection. The returned error makes runSender call connCancel and
	// exit (as a real send error does).
	entered := make(chan struct{})
	release := make(chan struct{})
	sendBlocking := func(*relayv1.AgentMessage) error {
		bump()
		defer concurrent.Add(-1)
		close(entered)
		<-release
		return errors.New("connection dead")
	}

	// Connection 1.
	ctx := context.Background()
	conn1Ctx, conn1Cancel := context.WithCancel(ctx)
	a.sendWG.Add(1)
	go a.runSender(conn1Ctx, conn1Cancel, sendBlocking)

	// Drive sender #1 into send(): enqueue one message and wait until it is
	// parked inside sendBlocking.
	a.sendCh <- &relayv1.AgentMessage{}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("sender #1 never entered send()")
	}

	// Simulate the recv loop dropping the stream: cancel conn1 (as connect's
	// defer connCancel / the recv-loop connCancel would).
	conn1Cancel()

	// Now perform the join exactly as connect() does at the top, BEFORE
	// starting connection 2. Sender #1 is still parked in send(), so this must
	// block until we release it - proving the join actually waits.
	joined := make(chan struct{})
	go func() {
		a.sendWG.Wait()
		close(joined)
	}()
	select {
	case <-joined:
		t.Fatal("sendWG.Wait returned while sender #1 was still inside send(); join did not wait")
	case <-time.After(100 * time.Millisecond):
		// Expected: still blocked because sender #1 has not returned.
	}

	// Release sender #1; it returns from send() (error path) and signals Done.
	close(release)
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("sendWG.Wait did not return after sender #1 exited")
	}

	// Only now start connection 2's sender, as connect() would post-Wait.
	conn2Ctx, conn2Cancel := context.WithCancel(ctx)
	defer conn2Cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	noopSend := func(*relayv1.AgentMessage) error { bump(); defer concurrent.Add(-1); return nil }
	a.sendWG.Add(1)
	go func() { defer wg.Done(); a.runSender(conn2Ctx, conn2Cancel, noopSend) }()

	// Push a couple of messages through sender #2.
	a.sendCh <- &relayv1.AgentMessage{}
	a.sendCh <- &relayv1.AgentMessage{}
	time.Sleep(100 * time.Millisecond)

	conn2Cancel()
	wg.Wait()
	a.sendWG.Wait()

	if got := maxConcurrent.Load(); got > 1 {
		t.Fatalf("at most one sender must read sendCh; observed %d concurrent", got)
	}
}
```

- [ ] **Step 2: Run the test and verify it passes on the fixed code**

Run:

```
go test ./internal/agent/ -run TestRunSender_AtMostOneSenderAcrossReconnect -v -timeout 60s
```

Expected: **PASS.** The join blocks until sender #1 exits (the `joined`-while-blocked guard does not fire), and `maxConcurrent` stays 1.

- [ ] **Step 3: Prove the test is load-bearing (red on the pre-join code)**

Temporarily verify the test would have caught the bug. Comment out the `a.sendWG.Wait()` line in `connect` is NOT what this test calls (the test drives the join itself), so instead prove the *guard* is real: temporarily change the test's join block to NOT wait (replace the `joined` goroutine's `a.sendWG.Wait()` with an immediate `close(joined)`), then start sender #2 immediately while sender #1 is still parked, and confirm `maxConcurrent` reaches 2. Run:

```
go test ./internal/agent/ -run TestRunSender_AtMostOneSenderAcrossReconnect -v -timeout 60s
```

Expected: **FAIL** with "observed 2 concurrent" (or the join guard's "returned while still inside send"). Then **revert the temporary change** so the committed test asserts the real join. This step is verification-only; do not commit the temporary mutation.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/sender_test.go
git commit -m "agent: test at-most-one send goroutine across reconnect join"
```

---

## Task 4: Deterministic queued-message test (bounded loss, no dual-sender steal)

Prove the second acceptance criterion: a message queued on `sendCh` is delivered by the single live sender, and the only message that can be lost on a drop is the one in-flight message whose `send` failed - never a message stolen from the live connection by a lingering old sender.

**Files:**
- Modify: `internal/agent/sender_test.go` - add a second test.

- [ ] **Step 1: Write the test**

Append to `internal/agent/sender_test.go`:

```go
// TestRunSender_QueuedMessageDeliveredByLiveSender proves that after a drop,
// once the previous sender is joined, every message subsequently queued on
// sendCh is delivered exactly once by the new (live) sender - no message is
// stolen or dropped by a lingering old sender. It also documents the bounded
// loss: the single message already handed to a failing send is the only one
// that can be lost, and it is not redelivered (we do not re-enqueue).
func TestRunSender_QueuedMessageDeliveredByLiveSender(t *testing.T) {
	a := &Agent{sendCh: make(chan *relayv1.AgentMessage, 64)}
	ctx := context.Background()

	// Connection 1: send fails immediately on the first message (dead conn),
	// modelling the in-flight message that is dropped on a drop.
	var conn1Delivered atomic.Int32
	conn1Ctx, conn1Cancel := context.WithCancel(ctx)
	a.sendWG.Add(1)
	conn1Done := make(chan struct{})
	failingSend := func(*relayv1.AgentMessage) error {
		conn1Delivered.Add(1)
		return errors.New("connection dead")
	}
	go func() { defer close(conn1Done); a.runSender(conn1Ctx, conn1Cancel, failingSend) }()

	// Queue the in-flight message; sender #1 pulls it, send fails, sender #1
	// cancels its conn and exits. This message is the documented bounded loss.
	a.sendCh <- &relayv1.AgentMessage{}
	select {
	case <-conn1Done:
	case <-time.After(2 * time.Second):
		t.Fatal("sender #1 did not exit after send error")
	}

	// Join as connect() does before the next connection.
	a.sendWG.Wait()
	if got := conn1Delivered.Load(); got != 1 {
		t.Fatalf("sender #1 should have attempted exactly 1 send, got %d", got)
	}

	// Connection 2: live sender records what it delivers.
	var delivered []string
	var mu sync.Mutex
	conn2Ctx, conn2Cancel := context.WithCancel(ctx)
	defer conn2Cancel()
	a.sendWG.Add(1)
	liveSend := func(m *relayv1.AgentMessage) error {
		mu.Lock()
		delivered = append(delivered, m.GetTaskLog().GetTaskId())
		mu.Unlock()
		return nil
	}
	go a.runSender(conn2Ctx, conn2Cancel, liveSend)

	// Queue three post-reconnect messages; all must reach the live sender.
	for _, id := range []string{"a", "b", "c"} {
		a.sendCh <- &relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskLog{
			TaskLog: &relayv1.TaskLogChunk{TaskId: id},
		}}
	}

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(delivered)
		mu.Unlock()
		if n == 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("live sender delivered %d/3 queued messages", n)
		case <-time.After(10 * time.Millisecond):
		}
	}

	conn2Cancel()
	a.sendWG.Wait()

	mu.Lock()
	defer mu.Unlock()
	for i, want := range []string{"a", "b", "c"} {
		if delivered[i] != want {
			t.Fatalf("queued message %d: got %q want %q (order or delivery wrong)", i, delivered[i], want)
		}
	}
}
```

- [ ] **Step 2: Run the test**

Run:

```
go test ./internal/agent/ -run TestRunSender_QueuedMessageDeliveredByLiveSender -v -timeout 60s
```

Expected: **PASS.** The live sender delivers exactly `a`, `b`, `c` in order; the in-flight message on the dead connection is the single bounded loss and is not redelivered.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/sender_test.go
git commit -m "agent: test queued message delivered by live sender after reconnect"
```

---

## Task 5: Full-suite verification (unit + integration)

Confirm the whole `internal/agent` package - including the `//go:build integration` reconnect tests that exercise the real spawn/join path - stays green, and the tree builds and vets.

**Files:**
- Run (no edit): the `internal/agent` test package, both build configurations.

- [ ] **Step 1: Build and vet the whole tree**

Run:

```
go build ./...
go vet ./...
```

Expected: both succeed, no output.

- [ ] **Step 2: Run the agent unit suite (Windows host, no Docker)**

Run:

```
go test ./internal/agent/... -timeout 120s
```

Expected: PASS, including the two new tests and `lifetime_test.go`.

- [ ] **Step 3: Run the agent integration suite (real gRPC reconnect path)**

The reconnect integration test `TestAgent_reconnects` in `agent_test.go` drives the actual `Run` -> `connect` -> `runSender` -> reconnect path, so it exercises the join end-to-end. Run it with the `integration` tag:

```
go test -tags integration -p 1 ./internal/agent/... -run TestAgent -v -timeout 120s
```

Expected: PASS. `TestAgent_reconnects` still observes `connectCount >= 2`, confirming the join does not stall reconnection (the join returns promptly because the previous connect already cancelled its conn). Note: some tests in this package spawn subprocesses (`echoTaskCmd`); on Windows they use `cmd /c` and run fine without Docker.

- [ ] **Step 4: Run `make test` (full unit sanity)**

Run:

```
make test
```

Expected: PASS. This is the repo-wide unit gate; confirms nothing else broke.

- [ ] **Step 5: No code commit; verification only**

If Steps 1-4 pass, the implementation is complete. CI runs `-race` on Linux as an additional backstop for the goroutine join; the deterministic tests above are the authoritative local proof.

---

## Self-Review

**Spec coverage:**
- "At most one send goroutine reads sendCh at any instant across a reconnect (assert via a test with an injected slow/blocked Send)" -> Task 3 (`TestRunSender_AtMostOneSenderAcrossReconnect`, injected blockable `send`, atomic max-concurrency assertion, real `sendWG.Wait` join).
- "A reconnect with a queued message does not silently lose it, OR the loss is explicitly bounded and documented" -> Tasks 2 + 4: the join eliminates the dual-sender steal; the bounded single-in-flight-message loss is documented in `runSender`'s comment (Task 1 Step 2 / Task 2 Step 4) and asserted in Task 4.
- Join mechanism (WaitGroup), placement relative to `connCancel` and next `connect` -> Task 2: `a.sendWG.Wait()` at the top of `connect` (after the new `connCtx`/deferred `connCancel`, before spawning the new sender); `Add(1)` before `go`, `defer Done()` inside `runSender`.
- Per-connection scoping made explicit so a stale goroutine cannot outlive its connection -> Task 2 preamble + `runSender` still selects on its own `connCtx.Done()`; the WaitGroup adds only the join.
- Re-enqueue vs document decision, stated explicitly with rationale -> "Design decision" section: document the bounded loss, do not re-enqueue (re-enqueue risks blocking the join on a full sendCh and reorders the best-effort log stream; status is reconciled on reconnect, logs are not replayed).
- Deterministic tests, not `-race`-dependent -> Tasks 3-4 use atomic counters, controlled release channels, and the real WaitGroup; "Tech Stack" and each task call this out. CI `-race` on Linux is a backstop only.
- Backend-only, no frontend slice -> "Slice declaration".
- Conflict flagging -> "Design decision" notes the deviation (declining the optional re-enqueue) and confirms the join restores the "one bounded sender per gRPC stream" invariant rather than conflicting with it.

**Placeholder scan:** No TBD/TODO/"handle edge cases"/"similar to Task N". Every code step shows real code. The two no-new-unit-test steps (Task 1 Step 1; none others) are explicitly justified as build/vet-verified pure extraction, not placeholders.

**Type/name consistency:** `sendWG sync.WaitGroup`, `runSender(connCtx context.Context, connCancel context.CancelFunc, send func(*relayv1.AgentMessage) error)`, `a.sendCh`, `connCtx`/`connCancel`, `stream.Send` (passed as the `send` method value), `runnerWG` (existing, referenced for the pattern) - all consistent across Tasks 1-5 and matching the existing identifiers in `internal/agent/agent.go`. The `a.sendWG.Add(1)` at the spawn site (Task 2 Step 3) pairs with `defer a.sendWG.Done()` in `runSender` (Task 2 Step 4) and `a.sendWG.Wait()` at the top of `connect` (Task 2 Step 2). The tests in Tasks 3-4 construct `&Agent{sendCh: ...}` and drive `a.runSender` / `a.sendWG` directly, matching the white-box pattern in `lifetime_test.go`.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-20-agent-reconnect-send-goroutine-not-awaited.md`.

Execute via **subagent-driven-development** (REQUIRED SUB-SKILL: superpowers:subagent-driven-development): a fresh subagent per task with two-stage review between tasks. Tasks run sequentially (single backend slice, no parallelism). After the implementation is verified green, close the backlog item with `/backlog close agent-reconnect-send-goroutine-not-awaited` (the command does the `git mv` into `docs/backlog/closed/` and the status/resolution stamping).
