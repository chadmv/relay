# Reconnect Backoff Never Resets - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the exponential reconnect backoff in both the agent (`internal/agent/agent.go`) and the server's `NotifyListener` (`internal/scheduler/notify.go`) reset to 1s after a session that actually got established, so a healthy-then-dropped session no longer leaves the backoff permanently degraded toward the 60s cap.

**Architecture:** Both reconnect loops have the same shape: a `backoff` local that doubles on each failure, capped at 60s, with a `backoff = time.Second` reset that is currently unreachable on any path that established a session and then failed. The fix is to make each inner call return a boolean that signals "this session got far enough to count as healthy" - for the agent, `registered` (the RegisterResponse was accepted and the send goroutine started); for the listener, `listened` (both `LISTEN` statements succeeded). The `Run` loop resets `backoff` to 1s whenever that boolean is true, regardless of whether the session later errored. The reset decision is factored into a tiny pure helper per site so it can be unit-tested with no timing, no sleeps, and no `-race`.

**Tech Stack:** Go 1.26, standard library (`context`, `time`, `sync`), testify for assertions. Agent tests are white-box (package `agent`, no build tag) matching `lifetime_test.go`/`sender_test.go`. The `NotifyListener` reset helper is tested as a pure unit (package `scheduler`, no build tag); the end-to-end listen/reconnect behavior remains covered by the existing integration tests in `notify_test.go` (`//go:build integration`), which the conductor can run under Docker.

**Slice declaration:** This is **backend-only**. There is **no frontend slice**. There are **two backend slices** that touch different files in different packages with no shared symbol:

- Slice A: `internal/agent` (agent reconnect backoff + dead error branch removal)
- Slice B: `internal/scheduler` (NotifyListener reconnect backoff)

**The two slices are INDEPENDENT.** They share no types, no functions, and no files. Phase 3 can run them in **parallel**. Each picks the same conceptual fix (registered/listened-bool reset) but implements it locally in its own package; there is deliberately no shared helper, because no such shared util exists today and introducing one would couple two otherwise-unrelated reconnect loops for a 3-line function (YAGNI).

**Files in scope:**

Slice A:
- Modify: `internal/agent/agent.go`
  - `Run()` (currently lines 61-98): reset `backoff` based on the new `registered` return from `connect`.
  - `connect()` (currently lines 133-240): change signature to `(registered bool, err error)`; set `registered = true` once the RegisterResponse is accepted and the send goroutine is started; return it from every return site.
  - `buildRegisterRequest()` (currently lines 273-328): change signature to drop the always-nil `error` return; update its one caller in `connect`.
  - Add a tiny pure helper `nextReconnectBackoff` (computes the next backoff given the current backoff and whether the prior session was healthy).
- Test: `internal/agent/backoff_test.go` (new) - white-box unit tests for `nextReconnectBackoff` and for `connect`'s `registered` return on a session that registers and then drops.

Slice B:
- Modify: `internal/scheduler/notify.go`
  - `Run()` (currently lines 28-49): reset `backoff` based on the new `listened` return from `session`.
  - `session()` (currently lines 53-79): change signature to `(listened bool, err error)`; set `listened = true` once both `LISTEN` statements succeed; return it from every return site.
  - Add a tiny pure helper `nextReconnectBackoff` (same shape as the agent's, local to package `scheduler`).
- Test: `internal/scheduler/backoff_test.go` (new) - pure unit tests for `nextReconnectBackoff` (package `scheduler`, no build tag).

No other files change. `make generate` is **not** required: no `.sql` or `.proto` files are touched.

---

## Background: exact current code (verified against the worktree; the backlog line numbers are stale)

The backlog item cites `agent.go:94, 117-120, 178-185` and `notify.go:28-49`. `agent.go` was just rewritten by the send-goroutine-join merge (`sendWG`, `runSender`, `connect` now calls `a.sendWG.Wait()`), so the agent line numbers are wrong. The plan below is written against the current worktree.

### Agent (`internal/agent/agent.go`)

`Run()` (lines 61-98):

```go
func (a *Agent) Run(ctx context.Context) {
	a.runCtx = ctx
	go a.runTelemetry(ctx)
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			a.runnerWG.Wait()
			return
		}
		if err := a.connect(ctx); err != nil {
			if ctx.Err() != nil {
				a.runnerWG.Wait()
				return
			}
			if status.Code(err) == codes.Unauthenticated {
				log.Print(authFailureMessage(/* ... */))
				a.runnerWG.Wait()
				return
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				a.runnerWG.Wait()
				return
			}
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			continue
		}
		backoff = time.Second   // <-- line 96: only reachable if connect returns nil
	}
}
```

`connect()` returns `error` and **every** return site returns a non-nil error:
- `return err` on dial failure (line 147), stream open (line 154), register send (line 166), first `Recv` (line 171), persist token (line 179), and the recv-loop `Recv` error (line 216).
- `return fmt.Errorf(...)` on build-register failure (line 160), unexpected first message (line 175).

There is no `return nil`. The only way out of the recv loop (lines 210-239) is the `return err` at line 216 when `stream.Recv()` fails (stream drop or ctx cancel). So `connect` never returns nil, so `backoff = time.Second` at line 96 is dead. Backoff doubles forever.

`buildRegisterRequest()` (lines 273-328) returns `(*relayv1.RegisterRequest, error)` and the only `return` is `return req, nil` at line 327. The error is **always nil**. Its sole caller (lines 158-161) has a dead error branch:

```go
regReq, err := a.buildRegisterRequest()
if err != nil {
	return fmt.Errorf("build register: %w", err)
}
```

Note: the inventory-listing failure inside `buildRegisterRequest` (lines 313-315) is already handled by logging and continuing - it does **not** propagate an error - so dropping the return value loses nothing.

### NotifyListener (`internal/scheduler/notify.go`)

`Run()` (lines 28-49):

```go
func (n *NotifyListener) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := n.session(ctx); err != nil && ctx.Err() == nil {
			log.Printf("notify listener: %v (backoff %s)", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			continue
		}
		backoff = time.Second   // <-- line 47
	}
}
```

`session()` (lines 53-79) returns non-nil on `Acquire` failure (line 56), either `LISTEN` (lines 62, 65), or the `WaitForNotification` loop error (line 74). It only returns `nil` if the loop's `WaitForNotification` returns a nil error - which never happens in practice (the loop only exits on error). So the `backoff = time.Second` at line 47 is reached only when `session` returns nil-error, i.e. effectively only on `ctx` cancellation paths where the next loop iteration immediately returns at line 32. The reset is therefore unreachable for a real reconnect: a healthy listen session that drops returns a non-nil error, takes the `continue` path, and never resets.

---

## Design decision: registered/listened-bool reset (NOT the elapsed-time threshold)

The backlog item proposes two ideas: (1) an elapsed-time threshold (`if time.Since(start) > 30*time.Second { backoff = time.Second }`), and (2) for the agent, signal successful registration out of `connect` via a `registered bool`.

**This plan uses the bool-signal approach for BOTH sites.** Rationale:

- **The bool is a cleaner, more precise signal of a healthy session than a wall-clock threshold.** "We registered" / "we LISTENed successfully" means the session genuinely got established. The elapsed-time threshold is a proxy: a session that fails to register but blocks for 31s in a slow dial would falsely reset, and a session that registers cleanly but drops at 29s would fail to reset even though it was healthy.
- **Both sites have a clean inflection point.** The agent already validates the RegisterResponse and then starts the send goroutine - that is exactly "registered". The listener runs two `LISTEN` statements and then loops - "both LISTENs succeeded" is exactly "the subscription is live". No new wall clock, no magic 30s constant.
- **Determinism.** The bool flows out of the inner call as a plain return value, so the reset decision is a pure function of `(currentBackoff, healthy bool)`. That makes it unit-testable with zero sleeps and zero `-race` dependence, which matters because `make test-race` excludes `internal/agent` on this Windows host. An elapsed-time threshold would force tests to either sleep past 30s (flaky/slow) or inject a clock (more machinery).
- **Uniformity.** Using the same shape at both sites keeps the two reconnect loops easy to reason about together, even though the helper is duplicated (3 lines) rather than shared.

**Deviation from the backlog proposal:** the backlog's headline code snippet is the elapsed-time threshold; this plan does not use it. It uses the bool-signal variant the backlog also mentions, extended to the listener as well. This is an intentional, documented deviation, not an oversight.

---

# Slice A: Agent (`internal/agent`)

### Task A1: Add the pure `nextReconnectBackoff` helper with failing test

**Files:**
- Create: `internal/agent/backoff_test.go`
- Modify: `internal/agent/agent.go` (add helper)

- [ ] **Step 1: Write the failing test**

Create `internal/agent/backoff_test.go`:

```go
package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNextReconnectBackoff(t *testing.T) {
	// Healthy session resets to 1s regardless of the prior backoff.
	assert.Equal(t, time.Second, nextReconnectBackoff(32*time.Second, true))
	assert.Equal(t, time.Second, nextReconnectBackoff(60*time.Second, true))
	assert.Equal(t, time.Second, nextReconnectBackoff(time.Second, true))

	// Unhealthy session doubles the backoff.
	assert.Equal(t, 2*time.Second, nextReconnectBackoff(time.Second, false))
	assert.Equal(t, 8*time.Second, nextReconnectBackoff(4*time.Second, false))

	// Doubling is capped at 60s.
	assert.Equal(t, 60*time.Second, nextReconnectBackoff(40*time.Second, false))
	assert.Equal(t, 60*time.Second, nextReconnectBackoff(60*time.Second, false))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/agent/... -run TestNextReconnectBackoff -v`
Expected: FAIL - `undefined: nextReconnectBackoff`.

- [ ] **Step 3: Add the minimal implementation**

In `internal/agent/agent.go`, add this helper (place it directly above `Run`, after the `NewAgent` func):

```go
// nextReconnectBackoff returns the backoff to use before the next reconnect
// attempt. A healthy session (one that registered before dropping) resets the
// backoff to 1s; an unhealthy session doubles it, capped at 60s. Keeping this a
// pure function lets the reset rule be unit-tested without timing or -race.
func nextReconnectBackoff(current time.Duration, healthy bool) time.Duration {
	if healthy {
		return time.Second
	}
	next := current * 2
	if next > 60*time.Second {
		next = 60 * time.Second
	}
	return next
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/agent/... -run TestNextReconnectBackoff -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/backoff_test.go internal/agent/agent.go
git commit -m "feat(agent): add pure nextReconnectBackoff helper"
```

---

### Task A2: Make `connect` return a `registered` bool and test it via a healthy-then-dropped session

**Files:**
- Modify: `internal/agent/agent.go` - `connect()` signature and all return sites; `buildRegisterRequest()` signature; `Run()` caller.
- Modify: `internal/agent/backoff_test.go` - add the connect-return test.

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/backoff_test.go`. This drives `connect` against an in-process buffered gRPC server that accepts the registration, returns a `RegisterResponse`, then closes the stream. The point is to assert `connect` returns `registered == true` even though it also returns a non-nil error (the stream drop). Reuse the existing in-process gRPC test harness pattern from `agent_test.go` if one is exported; otherwise use the minimal fake below.

```go
import (
	"context"
	"net"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// registerThenCloseServer accepts one Connect stream, expects a Register
// message, replies with a RegisterResponse, then returns (closing the stream).
type registerThenCloseServer struct {
	relayv1.UnimplementedAgentServiceServer
}

func (registerThenCloseServer) Connect(stream relayv1.AgentService_ConnectServer) error {
	if _, err := stream.Recv(); err != nil { // the Register message
		return err
	}
	if err := stream.Send(&relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_RegisterResponse{
			RegisterResponse: &relayv1.RegisterResponse{WorkerId: "w-1"},
		},
	}); err != nil {
		return err
	}
	return nil // close the stream -> agent's recv loop sees EOF
}

func TestConnect_ReportsRegisteredAfterHealthySessionDrops(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	relayv1.RegisterAgentServiceServer(srv, registerThenCloseServer{})
	go srv.Serve(lis)
	defer srv.Stop()

	// Override the dialer so grpc.NewClient(a.coord, ...) reaches the bufconn.
	// connect() uses grpc.NewClient(a.coord, ...); point coord at "passthrough"
	// and inject the bufconn dialer via a package-level dial override (added in
	// this task's export_test.go change below).
	dialContextFn = func(ctx context.Context) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
	defer func() { dialContextFn = nil }()

	creds, _ := LoadCredentials(t.TempDir())
	creds.SetEnrollmentToken("test-enrollment")
	a := NewAgent("passthrough:ignored", Capabilities{Hostname: "h"}, "", creds,
		func(string) error { return nil }, nil)
	a.runCtx = context.Background()

	registered, err := a.connect(context.Background())
	require.True(t, registered, "registered must be true after a session that registered")
	require.Error(t, err, "the dropped stream still surfaces an error")
}
```

Note for the implementer: `connect` currently calls `grpc.NewClient(a.coord, grpc.WithTransportCredentials(insecure.NewCredentials()))` (line 145). To make the dial injectable for this test, add a package var and use it in `connect`:

```go
// dialContextFn, when non-nil, overrides the transport dialer used by connect.
// Tests set it to a bufconn dialer; production leaves it nil. (No build tag,
// matching the existing saveConfigFn/readPasswordFn override pattern elsewhere.)
var dialContextFn func(context.Context) (net.Conn, error)
```

and in `connect`, build the client with a custom dialer when the override is set:

```go
opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
if dialContextFn != nil {
	opts = append(opts, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return dialContextFn(ctx)
	}))
}
conn, err := grpc.NewClient(a.coord, opts...)
```

If `agent_test.go` already exports an in-process server + dialer harness (check it first), reuse that instead of adding `dialContextFn` and the fake server, to avoid duplicate scaffolding.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/agent/... -run TestConnect_ReportsRegisteredAfterHealthySessionDrops -v`
Expected: FAIL - `a.connect` returns a single value (`registered, err := a.connect(...)` does not compile), and `dialContextFn` is undefined.

- [ ] **Step 3: Implement - change `connect` and `buildRegisterRequest` signatures**

In `internal/agent/agent.go`:

1. Add the `dialContextFn` package var and the custom-dialer wiring shown in Step 1 (skip if reusing an existing harness).

2. Change `buildRegisterRequest` to drop the always-nil error. New signature and final return:

```go
func (a *Agent) buildRegisterRequest() *relayv1.RegisterRequest {
	// ... body unchanged ...
	return req
}
```

3. In `connect`, change the call site (currently lines 158-161) from:

```go
regReq, err := a.buildRegisterRequest()
if err != nil {
	return fmt.Errorf("build register: %w", err)
}
```

to:

```go
regReq := a.buildRegisterRequest()
```

   This removes the dead error branch. After this, `fmt` may become unused if no other `fmt.Errorf`/`fmt.Fprintf` remains - it does not: `connect` still has `fmt.Errorf` at the unexpected-message return and `buildRegisterRequest`/`handleDispatch` still use `fmt.Fprintf`. Leave the `fmt` import.

4. Change `connect`'s signature to `(registered bool, err error)` and update **every** return site to return the bool first. The bool is `false` for every return that happens before registration is confirmed, and `true` for the recv-loop drop (which only runs after registration). Concretely:

```go
func (a *Agent) connect(ctx context.Context) (registered bool, err error) {
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	a.sendWG.Wait()

	conn, err := grpc.NewClient(a.coord, opts...) // opts from Step 3.1
	if err != nil {
		return false, err
	}
	defer conn.Close()

	client := relayv1.NewAgentServiceClient(conn)
	stream, err := client.Connect(connCtx)
	if err != nil {
		return false, err
	}

	regReq := a.buildRegisterRequest()
	if err := stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{Register: regReq},
	}); err != nil {
		return false, err
	}

	resp, err := stream.Recv()
	if err != nil {
		return false, err
	}
	reg := resp.GetRegisterResponse()
	if reg == nil {
		return false, fmt.Errorf("agent: expected RegisterResponse, got %T", resp.Payload)
	}
	if reg.AgentToken != "" {
		if err := a.creds.Persist(reg.AgentToken); err != nil {
			return false, fmt.Errorf("persist agent token: %w", err)
		}
		log.Printf("agent token persisted to %s", a.creds.TokenFilePath())
	}
	if reg.WorkerId != a.workerID {
		a.workerID = reg.WorkerId
		if err := a.saveID(a.workerID); err != nil {
			fmt.Fprintf(os.Stderr, "relay-agent: warning: failed to persist worker ID: %v\n", err)
		}
	}

	for _, tid := range reg.CancelTaskIds {
		a.mu.Lock()
		r, ok := a.runners[tid]
		a.mu.Unlock()
		if ok {
			r.Abandon()
		}
	}

	log.Printf("connected to coordinator %s (worker ID: %s)", a.coord, a.workerID)

	// From here the session is established: the coordinator accepted our
	// registration and we are about to stream. Any error after this point is a
	// drop of a healthy session, so report registered=true to reset the backoff.
	registered = true

	a.sendWG.Add(1)
	go a.runSender(connCtx, connCancel, stream.Send)

	for {
		msg, err := stream.Recv()
		if err != nil {
			connCancel()
			return registered, err
		}

		switch p := msg.Payload.(type) {
		// ... unchanged switch body ...
		}
	}
}
```

   Design note on placement: `registered = true` is set **after** the token-persist and worker-ID-save steps and **before** starting the send goroutine and entering the recv loop. This is the precise "session is healthy" inflection point. The token-persist failure (line 178-180 today) still returns `false` because if we cannot persist a freshly-issued token the session is not viable.

- [ ] **Step 4: Update the `Run` caller to use the bool**

In `Run()`, replace `if err := a.connect(ctx); err != nil {` and the trailing `backoff = time.Second` with logic that captures `registered` and uses `nextReconnectBackoff`:

```go
for {
	if ctx.Err() != nil {
		a.runnerWG.Wait()
		return
	}
	registered, err := a.connect(ctx)
	if err != nil {
		if ctx.Err() != nil {
			a.runnerWG.Wait()
			return
		}
		if status.Code(err) == codes.Unauthenticated {
			log.Print(authFailureMessage(
				a.creds.HasAgentToken(),
				a.creds.EnrollmentToken() != "",
				a.creds.TokenFilePath(),
			))
			a.runnerWG.Wait()
			return
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			a.runnerWG.Wait()
			return
		}
		backoff = nextReconnectBackoff(backoff, registered)
		continue
	}
	backoff = nextReconnectBackoff(backoff, registered)
}
```

   The old `backoff *= 2` / cap block (lines 90-93) is replaced by `nextReconnectBackoff(backoff, registered)`, and the trailing `backoff = time.Second` (line 96) is replaced by `nextReconnectBackoff(backoff, registered)` too. Because `connect` never returns `nil` error today, the `err == nil` branch (the final line) is still effectively unreachable, but wiring it through `nextReconnectBackoff` keeps it correct if a `nil`-returning path is ever added.

- [ ] **Step 5: Run the connect test to verify it passes**

Run: `go test ./internal/agent/... -run TestConnect_ReportsRegisteredAfterHealthySessionDrops -v`
Expected: PASS.

- [ ] **Step 6: Update the now-stale `buildRegisterRequest` tests**

`internal/agent/lifetime_test.go` calls `req, err := a.buildRegisterRequest()` in three tests (`TestAgent_BuildRegisterRequest_IncludesRunningTasks`, `_IncludesInventory`, `_NoCredentialsLeavesCredentialUnset`) and asserts `require.NoError(t, err)`. Update each to the single-return signature:

```go
req := a.buildRegisterRequest()
// delete the `require.NoError(t, err)` line in each test
```

   Verify no other caller exists:

Run: `git grep -n "buildRegisterRequest"`
Expected: only `agent.go` (definition + call) and `lifetime_test.go` (the three tests).

- [ ] **Step 7: Run the full agent package to verify everything passes**

Run: `go test ./internal/agent/... -v`
Expected: PASS (including the integration-tagged tests only if run with `-tags integration`; plain `go test` runs the white-box unit tests).

- [ ] **Step 8: Commit**

```bash
git add internal/agent/agent.go internal/agent/backoff_test.go internal/agent/lifetime_test.go
git commit -m "fix(agent): reset reconnect backoff after a registered session; drop dead buildRegisterRequest error"
```

---

# Slice B: NotifyListener (`internal/scheduler`)

### Task B1: Add the pure `nextReconnectBackoff` helper with failing test

**Files:**
- Create: `internal/scheduler/backoff_test.go`
- Modify: `internal/scheduler/notify.go` (add helper)

- [ ] **Step 1: Write the failing test**

Create `internal/scheduler/backoff_test.go`:

```go
package scheduler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNextReconnectBackoff(t *testing.T) {
	// Healthy session (LISTEN succeeded) resets to 1s regardless of prior value.
	assert.Equal(t, time.Second, nextReconnectBackoff(32*time.Second, true))
	assert.Equal(t, time.Second, nextReconnectBackoff(60*time.Second, true))

	// Unhealthy session doubles, capped at 60s.
	assert.Equal(t, 2*time.Second, nextReconnectBackoff(time.Second, false))
	assert.Equal(t, 60*time.Second, nextReconnectBackoff(40*time.Second, false))
	assert.Equal(t, 60*time.Second, nextReconnectBackoff(60*time.Second, false))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/scheduler/... -run TestNextReconnectBackoff -v`
Expected: FAIL - `undefined: nextReconnectBackoff`.

- [ ] **Step 3: Add the minimal implementation**

In `internal/scheduler/notify.go`, add this helper directly above `Run`:

```go
// nextReconnectBackoff returns the backoff before the next reconnect attempt. A
// healthy session (one where both LISTENs succeeded before the connection
// dropped) resets to 1s; an unhealthy session doubles, capped at 60s. Pure so
// the reset rule is unit-testable without a live Postgres or timing.
func nextReconnectBackoff(current time.Duration, healthy bool) time.Duration {
	if healthy {
		return time.Second
	}
	next := current * 2
	if next > 60*time.Second {
		next = 60 * time.Second
	}
	return next
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/scheduler/... -run TestNextReconnectBackoff -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/backoff_test.go internal/scheduler/notify.go
git commit -m "feat(scheduler): add pure nextReconnectBackoff helper for notify listener"
```

---

### Task B2: Make `session` return a `listened` bool and wire the reset into `Run`

**Files:**
- Modify: `internal/scheduler/notify.go` - `session()` signature and return sites; `Run()` reset logic.

- [ ] **Step 1: Change `session` to return `(listened bool, err error)`**

In `internal/scheduler/notify.go`, rewrite `session`:

```go
// session acquires a connection, LISTENs, and loops on WaitForNotification
// until an error occurs or ctx is cancelled. The returned listened bool is true
// once both LISTEN statements succeeded, signalling a healthy session whose
// later drop should reset the reconnect backoff.
func (n *NotifyListener) session(ctx context.Context) (listened bool, err error) {
	conn, err := n.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()

	raw := conn.Conn()
	if _, err := raw.Exec(ctx, "LISTEN relay_task_submitted"); err != nil {
		return false, err
	}
	if _, err := raw.Exec(ctx, "LISTEN relay_task_completed"); err != nil {
		return false, err
	}

	// Both LISTENs are attached: the subscription is live. Any error after this
	// point is a drop of a healthy session.
	listened = true

	// Drain anything submitted during a startup or reconnect gap. The
	// dispatcher's Trigger is idempotent.
	n.trigger()

	for {
		if _, err := raw.WaitForNotification(ctx); err != nil {
			return listened, err
		}
		n.trigger()
	}
}
```

   Design note: `listened = true` is set after both `LISTEN` statements and before the drain `trigger()` and the wait loop. The drain `trigger()` and every subsequent loop iteration are part of a live, healthy session.

- [ ] **Step 2: Wire the reset into `Run`**

Rewrite `Run`:

```go
func (n *NotifyListener) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		listened, err := n.session(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("notify listener: %v (backoff %s)", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff = nextReconnectBackoff(backoff, listened)
			continue
		}
		backoff = nextReconnectBackoff(backoff, listened)
	}
}
```

   The old `backoff *= 2` / cap block (lines 41-44) and the trailing `backoff = time.Second` (line 47) are both replaced by `nextReconnectBackoff(backoff, listened)`. Now a healthy session that drops (`listened == true`, non-nil error) takes the `continue` path and resets to 1s; a session that fails before LISTEN (`listened == false`) doubles as before.

- [ ] **Step 3: Run the scheduler unit tests to verify nothing broke**

Run: `go test ./internal/scheduler/... -v`
Expected: PASS (the pure `nextReconnectBackoff` test and any other non-integration tests; the `notify_test.go` integration tests are skipped without `-tags integration`).

- [ ] **Step 4: Run the integration tests (conductor, with Docker)**

Run: `go test -tags integration -p 1 ./internal/scheduler/... -run TestNotifyListener -v -timeout 120s`
Expected: PASS - `TestNotifyListener_TriggersOnNotify` and `TestNotifyListener_TriggersOnceAtStart` still pass; the `listened` change does not alter the observable trigger behavior.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/notify.go
git commit -m "fix(scheduler): reset notify listener reconnect backoff after a live session"
```

---

## Whole-feature verification (after both slices land)

- [ ] **Run the full unit suite**

Run: `make test`
Expected: PASS on Windows (agent + scheduler white-box unit tests run; integration tests skipped).

- [ ] **Run vet**

Run: `go vet ./internal/agent/... ./internal/scheduler/...`
Expected: no diagnostics. In particular, confirm no "unused import" for `fmt` in `agent.go` (it is still used) and no unused variable warnings from the signature changes.

- [ ] **Run the integration suite for both touched packages (conductor, with Docker)**

Run: `go test -tags integration -p 1 ./internal/agent/... ./internal/scheduler/... -v -timeout 180s`
Expected: PASS.

---

## Invariants check

- **One bounded sender per gRPC stream** (agent): unchanged. `connect` still calls `a.sendWG.Wait()` before `a.sendWG.Add(1); go a.runSender(...)`. The only change near the sender is that `registered = true` is set on the line just before `a.sendWG.Add(1)`, which does not affect the join ordering.
- **Identity-checked teardown** (agent): unchanged. No teardown logic is modified; only the return type and the backoff math change.
- **Epoch fence / single job-spec pipeline / single JSON entry point / no interior pointers across locks:** not touched - this change is confined to two reconnect loops and a register-request builder.
- **`make generate`:** not required; no `.sql` or `.proto` files are edited, and no `*.sql.go`/`models.go` is touched.

## Conflicts / risks flagged

- **Stale backlog line numbers.** The backlog cites `agent.go:94, 117-120, 178-185`; those refer to pre-merge code (before `sendWG`/`runSender`). This plan is written against the current worktree. Confirmed during planning.
- **Deviation from the backlog's headline snippet.** The backlog leads with the elapsed-time threshold; this plan uses the registered/listened-bool signal at both sites (the backlog's secondary suggestion for the agent, extended to the listener). Rationale documented above.
- **Dial injection for the agent test (Task A2).** The connect-return test needs to dial an in-process server. The plan adds a `dialContextFn` package var following the existing untagged-override pattern (`saveConfigFn`/`readPasswordFn` in `internal/cli`). **Before adding it, the implementer must check `internal/agent/agent_test.go` for an existing in-process gRPC harness/dialer and reuse it if present** to avoid duplicate scaffolding. If `connect`'s registered-bool is hard to exercise via the existing harness, the `nextReconnectBackoff` pure test (Task A1) plus the signature change already give the load-bearing coverage; the connect-return test is the belt-and-suspenders layer.
- **No `-race` dependence.** All new tests are deterministic: the backoff helpers are pure, and the connect test asserts a return value, not timing. This respects `make test-race` excluding `internal/agent` on Windows; CI's Linux `-race` run remains a backstop.
