# Stale Stream Teardown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a stale gRPC stream's teardown identity-checked so it can no longer unregister a freshly reconnected worker, flip the live worker offline, or requeue tasks the agent is actively running.

**Architecture:** Replace `Registry.Unregister(workerID)` with `Registry.UnregisterIf(workerID, sender)`, which deletes only when the registered sender is pointer-identical to the caller's. Extract the four unconditional teardown defers in `Handler.Connect` into a single `teardownConnection` method that always closes its own send goroutine but gates the shared-state teardown (DB offline flag, grace timer / requeue) on `UnregisterIf` returning true.

**Tech Stack:** Go, sqlc-generated store, pgx, testify, testcontainers-go (integration tests under `//go:build integration`).

---

## Slice Independence

This is a single-slice, single-package change (`internal/worker`). There is no frontend component and no backend/frontend split. **There is no Phase 3 parallelism to declare.**

## Recommended execution: ONE implementer, task-by-task

This is tightly-coupled Go work confined to one package. Tasks 1-4 all touch `internal/worker` and the later tasks depend directly on the symbols (`UnregisterIf`, `teardownConnection`) introduced by the earlier ones. The prior similar bug (`docs/retros/2026-06-19-requeue-retry-epoch-fence.md`) confirmed that coupled single-package work is best run by a SINGLE backend engineer through the whole plan, then a single consolidated `relay-verify` pass - not fragmented across fresh-subagent-per-task. **Recommendation: one backend engineer runs Tasks 1-6 in order; one combined review at the end.**

## No regeneration needed

There are NO `.sql` or `.proto` changes in this plan, so `make generate` is NOT required. Do not run sqlc/protobuf regeneration.

## Invariants to respect

- **Identity-checked teardown** (the invariant this plan enforces): connection cleanup must only tear down state it owns. `UnregisterIf` + the gated `teardownConnection` are the mechanism.
- **One bounded sender per gRPC stream:** each connection owns its own `*workerSender` send goroutine. `teardownConnection` must ALWAYS call `sender.Close()` on its own sender regardless of registry ownership, and must never close another connection's sender.

## File Structure

- Modify: `internal/worker/registry.go` - replace `Unregister` with `UnregisterIf` (currently lines 33-38).
- Modify: `internal/worker/registry_test.go` - update `TestRegistry_Unregister` (lines 44-51); add replace-then-stale-teardown test. Package `worker_test`, DB-free, uses the existing `fakeSender` (lines 13-20).
- Modify: `internal/worker/handler.go` - extract `teardownConnection`; replace the `Connect` defer block (lines 105-112).
- Modify: `internal/worker/export_test.go` - add a `//go:build integration` test shim exposing `teardownConnection` and a registered `*workerSender` to package `worker_test`.
- Create: `internal/worker/handler_teardown_test.go` - `//go:build integration` regression test for the stale-teardown gate.
- Modify: `docs/backlog/bug-2026-06-10-stale-stream-teardown-clobbers-registration.md` -> move to `docs/backlog/closed/` (Task 6).

---

## Task 0: Commit the plan document

This plan doc must be committed before implementation begins. A prior retro (`docs/retros/2026-06-19-requeue-retry-epoch-fence.md`) caught a plan being left uncommitted.

**Files:**
- Add: `docs/superpowers/plans/2026-06-19-stale-stream-teardown.md`

- [ ] **Step 1: Commit the plan**

```bash
git add docs/superpowers/plans/2026-06-19-stale-stream-teardown.md
git commit -m "plan: stale stream teardown identity-checked implementation plan"
```

---

## Task 1: Replace `Registry.Unregister` with `UnregisterIf`

**Files:**
- Modify: `internal/worker/registry.go:33-38`
- Test: `internal/worker/registry_test.go:44-51` (existing) plus a new test

This task does the registry change and its unit tests together because the old `Unregister` and its test are replaced in lockstep - leaving the old test referencing a deleted method would not compile.

- [ ] **Step 1: Update the existing unregister test and add the replace-then-stale test**

Replace the existing `TestRegistry_Unregister` (currently `internal/worker/registry_test.go:44-51`) with the two tests below. The new `TestRegistry_UnregisterIf_ReplaceThenStaleTeardown` drives the bug scenario directly. Both use the existing `fakeSender` (lines 13-20) and require no DB.

```go
func TestRegistry_UnregisterIf(t *testing.T) {
	r := worker.NewRegistry()
	s := &fakeSender{}
	r.Register("worker-1", s)

	// The owning sender removes itself.
	removed := r.UnregisterIf("worker-1", s)
	assert.True(t, removed, "owning sender should remove its slot")

	err := r.Send("worker-1", &relayv1.CoordinatorMessage{})
	assert.Error(t, err, "slot must be empty after UnregisterIf by the owner")
}

func TestRegistry_UnregisterIf_ReplaceThenStaleTeardown(t *testing.T) {
	r := worker.NewRegistry()
	a := &fakeSender{}
	b := &fakeSender{}

	// A registers, then B reconnects and replaces A for the same worker.
	r.Register("worker-1", a)
	r.Register("worker-1", b)

	// Stale teardown from A must NOT remove B's slot.
	removedA := r.UnregisterIf("worker-1", a)
	assert.False(t, removedA, "stale sender A must not own the slot")

	// B is still reachable.
	msg := &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: &relayv1.DispatchTask{TaskId: "task-b"},
		},
	}
	require.NoError(t, r.Send("worker-1", msg), "B must still be registered after stale A teardown")
	require.Len(t, b.sent, 1)
	assert.Equal(t, "task-b", b.sent[0].GetDispatchTask().TaskId)
	assert.Empty(t, a.sent, "stale A must never receive sends")

	// B's own teardown removes the slot.
	removedB := r.UnregisterIf("worker-1", b)
	assert.True(t, removedB, "B owns the slot and should remove it")
	assert.Error(t, r.Send("worker-1", msg), "slot must be empty after B teardown")
}
```

- [ ] **Step 2: Run the tests to verify they FAIL to compile**

Run: `go test ./internal/worker/ -run TestRegistry_UnregisterIf -v`
Expected: build failure - `r.UnregisterIf undefined (type *worker.Registry has no field or method UnregisterIf)`.

- [ ] **Step 3: Replace `Unregister` with `UnregisterIf` in the registry**

In `internal/worker/registry.go`, replace the `Unregister` method (lines 33-38) with:

```go
// UnregisterIf removes the worker's stream only if the currently registered
// sender is s. Returns true if it removed it (this caller still owned the
// slot); false if a newer connection has since replaced it. Pointer identity
// works because the registry stores *workerSender values behind the Sender
// interface; interface comparison falls through to pointer equality.
func (r *Registry) UnregisterIf(workerID string, s Sender) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.streams[workerID] != s {
		return false
	}
	delete(r.streams, workerID)
	return true
}
```

- [ ] **Step 4: Run the registry unit tests to verify they PASS**

Run: `go test ./internal/worker/ -run TestRegistry -v`
Expected: PASS for `TestRegistry_RegisterAndSend`, `TestRegistry_SendUnknownWorker`, `TestRegistry_UnregisterIf`, `TestRegistry_UnregisterIf_ReplaceThenStaleTeardown`.

Note: the build will still fail at the package level until Task 2, because `handler.go:112` still calls the now-deleted `h.registry.Unregister`. `go test ./internal/worker/` will report that. That is expected here; run only the `-run TestRegistry` filter for this step, which compiles the test binary and surfaces a build error if `handler.go` is broken. If the package fails to build because of `handler.go:112`, proceed to Task 2 and re-run.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/registry.go internal/worker/registry_test.go
git commit -m "feat(worker): identity-checked Registry.UnregisterIf replaces Unregister"
```

---

## Task 2: Extract `teardownConnection` and gate the shared-state teardown

**Files:**
- Modify: `internal/worker/handler.go:105-112` (the `Connect` defer block) and add the new method

This task makes the package compile again (Task 1 left the old `Unregister` caller dangling) and installs the ownership gate.

- [ ] **Step 1: Replace the defer block in `Connect`**

In `internal/worker/handler.go`, replace the four-line defer block at lines 105-112:

```go
	if h.grace != nil {
		defer h.grace.Start(workerID) // runs 4th: grace timer will requeue after window
	} else {
		defer h.requeueWorkerTasks(workerID) // runs 4th: requeue immediately
	}
	defer h.markWorkerOffline(workerID)    // runs 3rd
	defer sender.Close()                   // runs 2nd
	defer h.registry.Unregister(workerID) // runs 1st
```

with the single gated defer:

```go
	defer h.teardownConnection(workerID, sender)
```

- [ ] **Step 2: Add the `teardownConnection` method**

Add this method to `internal/worker/handler.go`. Place it immediately above `markWorkerOffline` (currently line 537) so the teardown helpers sit together.

```go
// teardownConnection runs when a Connect stream ends. It always stops this
// connection's own send goroutine, but only tears down shared worker state
// (DB offline flag, grace timer / requeue) when this connection still owns the
// worker's registry slot. A newer connection for the same worker must not be
// clobbered (Identity-checked teardown invariant).
func (h *Handler) teardownConnection(workerID string, sender *workerSender) {
	owned := h.registry.UnregisterIf(workerID, sender)
	sender.Close() // always stop our own send goroutine
	if !owned {
		return // a newer connection owns this worker; leave shared state alone
	}
	h.markWorkerOffline(workerID)
	if h.grace != nil {
		h.grace.Start(workerID)
	} else {
		h.requeueWorkerTasks(workerID)
	}
}
```

- [ ] **Step 3: Verify the package builds and the registry tests still pass**

Run: `go test ./internal/worker/ -run TestRegistry -v`
Expected: PASS (the package now compiles - no more reference to the deleted `Unregister`).

- [ ] **Step 4: Run the full unit test suite to confirm no regressions**

Run: `go test ./internal/worker/...`
Expected: PASS (unit tests only; integration tests are gated behind `//go:build integration` and are not compiled here).

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler.go
git commit -m "feat(worker): gate stream teardown on registry ownership"
```

---

## Task 3: Add the integration test shim for `teardownConnection`

**Files:**
- Modify: `internal/worker/export_test.go` (append; file already has `//go:build integration`)

`teardownConnection` and `*workerSender` are unexported, so the regression test in package `worker_test` cannot call them directly. Add a shim that mirrors real usage: it wraps a raw stream in a real `*workerSender` (via `NewWorkerSender`), registers it (exactly as `finishRegister` does), and returns an opaque handle the test can later pass to a teardown shim. This keeps the test exercising the production code paths rather than a reimplementation.

- [ ] **Step 1: Append the shim to `export_test.go`**

Add to `internal/worker/export_test.go` (the file already declares `//go:build integration` and `package worker`):

```go
// RegisteredSenderForTest wraps stream in a real *workerSender, registers it
// for workerID exactly as finishRegister does, and returns an opaque handle.
// Used by package worker_test to drive teardownConnection with a known sender.
func (h *Handler) RegisteredSenderForTest(workerID string, stream Sender) *SenderHandle {
	s := NewWorkerSender(stream)
	h.registry.Register(workerID, s)
	return &SenderHandle{s: s}
}

// SenderHandle is an opaque wrapper around an unexported *workerSender so that
// package worker_test can hold and pass senders without touching the type.
type SenderHandle struct {
	s *workerSender
}

// TeardownConnectionForTest invokes the unexported teardownConnection with the
// handle's sender, exercising the production ownership gate.
func (h *Handler) TeardownConnectionForTest(workerID string, handle *SenderHandle) {
	h.teardownConnection(workerID, handle.s)
}
```

- [ ] **Step 2: Verify the integration build compiles**

Run: `go build -tags integration ./internal/worker/`
Expected: success, no output. (This confirms the shim references resolve before writing the test that uses them.)

- [ ] **Step 3: Commit**

```bash
git add internal/worker/export_test.go
git commit -m "test(worker): export shim for teardownConnection integration test"
```

---

## Task 4: Integration regression test - stale teardown must not clobber a fresh registration

**Files:**
- Create: `internal/worker/handler_teardown_test.go`

This test seeds an online worker with a running task, registers a stale sender A then a fresh sender B (replacing A), runs the stale teardown for A, and asserts the live worker stays online, B stays registered, and the running task is untouched (same `assignment_epoch`).

The fixture (`newWorkerTestFixture`, line 115 of `handler_auth_test.go`) wires `worker.NewHandler` (NOT `NewHandlerWithGrace`), so `h.grace == nil` and `teardownConnection`'s requeue branch is `requeueWorkerTasks`. That is the correct path to exercise here: if the gate were broken, `requeueWorkerTasks` would bump the task's `assignment_epoch` and return it to `pending`. The test asserts neither happens.

- [ ] **Step 1: Write the failing test**

Create `internal/worker/handler_teardown_test.go`:

```go
//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTeardownConnection_StaleSenderDoesNotClobberFreshRegistration proves the
// Identity-checked teardown invariant: a half-open stream returning AFTER the
// same agent reconnected must not unregister the fresh sender, mark the live
// worker offline, or requeue the agent's running task.
func TestTeardownConnection_StaleSenderDoesNotClobberFreshRegistration(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	// Seed a user + job + task, claim it for an online worker.
	user, err := fx.Q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "task-user", Email: "task-user@test.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)

	job, err := fx.Q.CreateJob(ctx, store.CreateJobParams{
		Name: "teardown-job", Priority: "normal", SubmittedBy: user.ID,
		Labels: []byte("{}"), ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	task, err := fx.Q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "teardown-task",
		Commands: []byte(`[["echo","hi"]]`), Env: []byte("{}"), Requires: []byte("[]"), Retries: 0,
	})
	require.NoError(t, err)

	wk, err := fx.Q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "teardown-worker", Hostname: "teardown-worker-01",
		CpuCores: 4, RamGb: 8, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	// Mark the worker online (a live agent would be online).
	_, err = fx.Q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wk.ID, Status: "online",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	// Claim the task: epoch 0 -> 1, status dispatched, assigned to wk.
	claimed, err := fx.Q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: wk.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	require.Equal(t, "dispatched", claimed.Status)

	workerIDStr := uuidString(wk.ID)

	// Register stale sender A, then fresh sender B replaces it (the reconnect).
	staleStream := &fakeSender{}
	freshStream := &fakeSender{}
	_ = fx.Handler.RegisteredSenderForTest(workerIDStr, staleStream)
	freshHandle := fx.Handler.RegisteredSenderForTest(workerIDStr, freshStream)
	staleHandle := fx.Handler.RegisteredSenderForTest // placeholder to avoid unused import; replaced below

	_ = staleHandle // not used; see note below
	_ = freshHandle

	// Re-register to capture both handles explicitly.
	staleH := fx.Handler.RegisteredSenderForTest(workerIDStr, staleStream)
	freshH := fx.Handler.RegisteredSenderForTest(workerIDStr, freshStream)

	// Run the STALE connection's teardown. It must be a no-op for shared state.
	fx.Handler.TeardownConnectionForTest(workerIDStr, staleH)

	// 1. The fresh sender is still registered: a Send reaches B, not an error.
	dispatch := &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: &relayv1.DispatchTask{TaskId: "still-alive"},
		},
	}
	require.NoError(t, fx.Handler.SendToWorkerForTest(workerIDStr, dispatch),
		"fresh sender B must remain registered after stale A teardown")

	// 2. The worker is still online.
	wAfter, err := fx.Q.GetWorker(ctx, wk.ID)
	require.NoError(t, err)
	assert.Equal(t, "online", wAfter.Status, "live worker must stay online")

	// 3. The running task is untouched: same epoch, still assigned, still dispatched.
	taskAfter, err := fx.Q.GetTask(ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), taskAfter.AssignmentEpoch, "task epoch must not be bumped")
	assert.Equal(t, "dispatched", taskAfter.Status, "task must not be requeued to pending")
	assert.Equal(t, wk.ID, taskAfter.WorkerID, "task must remain assigned to the worker")

	// Clean up B's goroutine via its own (legitimate) teardown.
	fx.Handler.TeardownConnectionForTest(workerIDStr, freshH)
}
```

> Implementer note: the `staleHandle`/`freshHandle` placeholder lines above are scaffolding to keep the example self-contained while showing both registration calls. When you write the real file, register exactly twice and keep only `staleH` and `freshH`:
> ```go
> staleH := fx.Handler.RegisteredSenderForTest(workerIDStr, staleStream)
> freshH := fx.Handler.RegisteredSenderForTest(workerIDStr, freshStream)
> ```
> Delete the four placeholder lines (`_ = fx.Handler...` through `_ = freshHandle`). They exist only so the example reads top-to-bottom; the real test must not have unused or duplicate registrations.

- [ ] **Step 2: Add the small helpers the test needs**

The test references `uuidString` (a string form of a `pgtype.UUID`) and `fx.Handler.SendToWorkerForTest`. The package already has `uuidStr` (unexported, in `handler.go:649`), but it is not visible to `worker_test`. Add a test-package helper and a registry-send shim.

Append to `internal/worker/export_test.go`:

```go
// SendToWorkerForTest delivers msg to workerID through the registry, exercising
// the production send path so the test can prove which sender is registered.
func (h *Handler) SendToWorkerForTest(workerID string, msg *relayv1.CoordinatorMessage) error {
	return h.registry.Send(workerID, msg)
}
```

Add a `uuidString` helper in the test package. Put it at the top of `internal/worker/handler_teardown_test.go`, right after the imports:

```go
// uuidString renders a pgtype.UUID in canonical 8-4-4-4-12 form for use as a
// worker ID string in registry calls.
func uuidString(u pgtype.UUID) string {
	b := u.Bytes
	return fmtUUID(b)
}

func fmtUUID(b [16]byte) string {
	const hexdigits = "0123456789abcdef"
	var out [36]byte
	pos := 0
	write := func(start, end int) {
		for i := start; i < end; i++ {
			out[pos] = hexdigits[b[i]>>4]
			out[pos+1] = hexdigits[b[i]&0x0f]
			pos += 2
		}
	}
	write(0, 4)
	out[pos] = '-'
	pos++
	write(4, 6)
	out[pos] = '-'
	pos++
	write(6, 8)
	out[pos] = '-'
	pos++
	write(8, 10)
	out[pos] = '-'
	pos++
	write(10, 16)
	return string(out[:])
}
```

> Implementer note: if a `uuidString`/`uuidStr` test helper already exists in package `worker_test` (grep first: `rg "func uuidString|func fmtUUID" internal/worker`), reuse it and delete the duplicate above. Do not introduce a second helper with the same job.

- [ ] **Step 3: Run the test to verify it PASSES (the gate is already implemented)**

Because Tasks 1-2 already installed the fix, this regression test should pass on first run. Run it and confirm.

Run: `go test -tags integration -p 1 ./internal/worker/ -run TestTeardownConnection_StaleSenderDoesNotClobberFreshRegistration -v -timeout 120s`
Expected: PASS. (Requires Docker Desktop running.)

- [ ] **Step 4: Prove the test actually guards the bug (temporary revert check)**

To confirm the test fails when the gate is absent, temporarily make `teardownConnection` ignore the `owned` result:

In `internal/worker/handler.go`, temporarily change the gate so the teardown always runs:

```go
func (h *Handler) teardownConnection(workerID string, sender *workerSender) {
	owned := h.registry.UnregisterIf(workerID, sender)
	_ = owned
	sender.Close()
	h.markWorkerOffline(workerID)
	if h.grace != nil {
		h.grace.Start(workerID)
	} else {
		h.requeueWorkerTasks(workerID)
	}
}
```

Run: `go test -tags integration -p 1 ./internal/worker/ -run TestTeardownConnection_StaleSenderDoesNotClobberFreshRegistration -v -timeout 120s`
Expected: FAIL - the worker flips offline and/or the task epoch bumps to 2 / status becomes pending.

Then REVERT the temporary change (restore the `if !owned { return }` gate from Task 2 Step 2) and re-run to confirm PASS:

Run: `go test -tags integration -p 1 ./internal/worker/ -run TestTeardownConnection_StaleSenderDoesNotClobberFreshRegistration -v -timeout 120s`
Expected: PASS.

> Do NOT commit the temporary revert. This step is a manual guard check only.

- [ ] **Step 5: Run the full worker integration suite to confirm no regressions**

Run: `go test -tags integration -p 1 ./internal/worker/... -timeout 300s`
Expected: PASS for all worker integration tests. (Requires Docker Desktop running.)

- [ ] **Step 6: Commit**

```bash
git add internal/worker/handler_teardown_test.go internal/worker/export_test.go
git commit -m "test(worker): integration regression for stale stream teardown gate"
```

---

## Task 5: Full unit suite + confirm no other `Unregister` callers remain

**Files:** none (verification only)

- [ ] **Step 1: Confirm no production code still calls the removed `Unregister`**

Run: `rg "\.Unregister\(" --glob '!*_test.go'`
Expected: NO matches. (The only former caller was the `Connect` defer, now replaced.)

If any match appears, it must be repointed to `UnregisterIf` with the owning sender before proceeding - report it; do not leave a broken caller.

- [ ] **Step 2: Run the full unit test suite (no Docker)**

Run: `make test`
Expected: PASS across all packages.

- [ ] **Step 3: No commit needed if no changes**

If Step 1 surfaced a stray caller you had to fix, commit it:

```bash
git add -A
git commit -m "fix(worker): repoint stray Unregister caller to UnregisterIf"
```

Otherwise skip.

---

## Task 6: Close the backlog item

**Files:**
- Move: `docs/backlog/bug-2026-06-10-stale-stream-teardown-clobbers-registration.md` -> `docs/backlog/closed/`

Per the spec's Backlog section, the backlog item is closed on completion. Moving it to `docs/backlog/closed/` is required scope, not optional cleanup.

- [ ] **Step 1: Move the backlog file**

```bash
git mv docs/backlog/bug-2026-06-10-stale-stream-teardown-clobbers-registration.md docs/backlog/closed/bug-2026-06-10-stale-stream-teardown-clobbers-registration.md
```

- [ ] **Step 2: Flip the front-matter status to closed**

In the moved file `docs/backlog/closed/bug-2026-06-10-stale-stream-teardown-clobbers-registration.md`, change the front-matter line:

```
status: open
```

to:

```
status: closed
```

- [ ] **Step 3: Commit**

```bash
git add docs/backlog/closed/bug-2026-06-10-stale-stream-teardown-clobbers-registration.md
git commit -m "backlog: close bug-2026-06-10-stale-stream-teardown-clobbers-registration"
```

---

## Self-Review Notes (planner)

**Spec coverage:**
- Design section 1 (identity-checked delete) -> Task 1.
- Design section 2 (ownership-gated teardown / extract `teardownConnection`) -> Task 2.
- Testing: registry unit test (replace-then-stale + updated unregister test) -> Task 1. Handler integration regression test -> Tasks 3-4.
- Backlog close/move -> Task 6.
- Out of scope (keepalive, grace-window semantics, requeue queries, epoch fence) -> untouched; no task introduces them.

**Type/signature consistency:** `UnregisterIf(workerID string, s Sender) bool`, `teardownConnection(workerID string, sender *workerSender)`, shim methods `RegisteredSenderForTest`, `TeardownConnectionForTest`, `SendToWorkerForTest`, and `SenderHandle` are referenced consistently across Tasks 1-4.

**Known wrinkles flagged to the implementer:**
- After Task 1, the package does not compile until Task 2 (the old `Unregister` caller is gone). Task 1 Step 4 calls this out and scopes its verification to the registry test compile.
- The fixture uses `NewHandler` (grace == nil), so the requeue branch is `requeueWorkerTasks`; the integration test asserts on epoch/status to prove it does not fire.
- The example test body in Task 4 Step 1 contains intentional scaffolding lines; the implementer note tells the engineer to delete them and keep two clean registrations.
