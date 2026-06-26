# sendInventory Wedge-Escape Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `Runner.sendInventory` use a bounded best-effort try-send on the cancelled/abandoned cleanup path so the deferred cleanup goroutine cannot park unbounded on a wedged `sendCh`, while leaving normal-completion inventory delivery blocking and unchanged.

**Architecture:** Single local edit to `sendInventory` in `internal/agent/runner.go`: branch on `r.cancelled.Load() || r.abandoned.Load()` and route the inventory message through the same room-first try-send (`select { case r.sendCh <- msg: default: }`) that `sendFinalStatus` already uses for the cancelled path; otherwise keep the existing blocking two-case `r.send`. No new field, channel, or sender. Regression tests mirror the parent fix's wedged-full `//go:build !windows` harness in `internal/agent/runner_cancel_test.go` and reuse the existing `fakeProvider` / `fakeHandle` (whose `Inventory()` already returns a non-empty entry and whose `Finalize` does not re-park).

**Tech Stack:** Go, `sync/atomic`, `context`, `testify/require`, `relayv1` protobuf bindings.

**Slice independence:** This is **backend-only**. There is no frontend slice. Nothing here touches proto, API, CLI, the server-side handler, or the web SPA. No parallelism declaration is needed for Phase 3.

---

## Background the implementer needs

Read these before starting:

- `internal/agent/runner.go` - the whole file is ~426 lines. Key sites:
  - `Run`'s default-cancel cleanup `defer` at `runner.go:134-143` (calls `handle.Finalize(r.ctx)` then `r.sendInventory(handle.Inventory())`).
  - `sendFinalStatus` at `runner.go:311-340` - the existing bounded-send pattern to mirror. Its cancelled branch (`runner.go:331-338`) is the exact `select { case r.sendCh <- msg: default: }` shape to reuse.
  - `send` at `runner.go:342-348` - the blocking two-case (`r.sendCh <- msg` / `<-r.ctx.Done()`). `r.ctx` is the parent/agent context, which only closes on agent shutdown - that is why a wedged `sendCh` parks `sendInventory` indefinitely.
  - `sendInventory` at `runner.go:413-425` - the function this plan edits.
  - The atomics `r.cancelled` (set by both `Cancel(false)` and `Cancel(true)`) and `r.abandoned` (set by `Abandon()` only). See `Cancel` at `runner.go:63-78` and `Abandon` at `runner.go:83-87`.
- `internal/agent/runner_cancel_test.go` - the `//go:build !windows` wedged-full harness. `TestRunner_DefaultCancel_WedgedFull_ReturnsQuickly` (lines 186-221) and `TestRunner_Abandon_WedgedFull_ReturnsQuickly` (lines 278-310) are the templates: small-cap undrained `sendCh`, a continuous-output subprocess, a fill-to-capacity spin loop, then cancel/abandon and a return-bound assertion.
- `internal/agent/runner_test.go:18-43` - the shared `fakeProvider` and `fakeHandle` helpers. `fakeHandle.Inventory()` (lines 41-43) already returns a non-empty entry (`ShortID: "abc"`), and `fakeHandle.Finalize` (line 40) just sets a bool and returns `nil` - it does not re-park. The deferred cleanup therefore reaches `sendInventory` and, with `sendCh` still wedged, `sendInventory` is the ONLY residual park. Reuse these helpers; do not define new ones.
- `internal/agent/export_test.go:20-21` - `SetProviderForTest` injects the provider.

### Why the parent fix did not already cover this

The parent fix (`docs/superpowers/plans/2026-06-21-default-cancel-abandon-hang-plan.md`) gave the log-write path and the terminal-status send a per-task-cancel escape: `sendOrAbort` selects on `r.cancelledCh`, which frees exec's copy goroutine so `cmd.Wait()` returns, and `sendFinalStatus` uses a bounded try-send on `r.cancelled`. With those in place, after a default cancel on a wedged channel: the copy goroutine aborts, `cmd.Wait()` returns, the deferred cleanup runs `Finalize` (returns immediately), and then `sendInventory` calls the still-blocking `r.send` - which parks on the full `sendCh` until agent shutdown because `r.ctx` is the parent context. That residual park is what this plan removes.

---

## File Structure

- **Modify:** `internal/agent/runner.go` - `sendInventory` only (the function body at lines 413-425). No other function changes.
- **Modify:** `internal/agent/runner_cancel_test.go` - add three tests (criteria 1, 2, 3 from the spec). Reuse the existing `fakeProvider` / `fakeHandle` from `runner_test.go` (same package).

No new files. No proto/SQL/migration changes, so no `make generate` step.

---

## Task 1: Regression test - default cancel under a wedged sendCh isolates the inventory park (RED)

This test pins the inventory park specifically: it must hang RED against the current blocking `sendInventory` (which parks on `r.send`) and pass GREEN after Task 3. To isolate the inventory path, it uses a source-bearing task with the fake provider so the cleanup defer runs `Finalize` then `sendInventory`, and a continuous-output subprocess to wedge `sendCh` full before cancel.

**Files:**
- Test: `internal/agent/runner_cancel_test.go` (append a new test function)

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/runner_cancel_test.go`:

```go
// TestRunner_DefaultCancel_WedgedFull_InventoryDoesNotPark is the
// sendInventory-wedge-escape criterion 1. It isolates the deferred
// sendInventory park as the residual hang after the parent default-cancel fix.
//
// A source-bearing task drives the cleanup defer (Finalize then sendInventory).
// fakeHandle.Finalize returns immediately and does not re-park, and its
// Inventory() returns a non-empty entry, so once cmd.Wait is freed by the
// parent fix the ONLY thing that can still park Run is sendInventory's send.
// With sendCh wedged full, the blocking send parks until agent shutdown; the
// bounded try-send must instead free Run within the bound.
//
// Return-bound assertion only: inventory may legitimately be dropped when
// sendCh is wedged full, so this makes no inventory-delivery assertion.
func TestRunner_DefaultCancel_WedgedFull_InventoryDoesNotPark(t *testing.T) {
	// Small cap so we can wedge it full quickly. No consumer ever drains it.
	sendCh := make(chan *relayv1.AgentMessage, 4)
	fh := &fakeHandle{dir: t.TempDir()}
	prov := &fakeProvider{handle: fh}

	task := &relayv1.DispatchTask{
		TaskId:   "t-inv-default-wedge",
		JobId:    "j-inv-default-wedge",
		Commands: singleCmd([]string{"sh", "-c", "while true; do echo x; done"}),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)
	r.SetProviderForTest(prov)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	// Wait until sendCh is wedged full: the continuous-output subprocess fills
	// the buffer and a copy goroutine parks inside chunkWriter.Write.
	deadline := time.Now().Add(5 * time.Second)
	for len(sendCh) < cap(sendCh) {
		if time.Now().After(deadline) {
			t.Fatal("sendCh never filled to capacity; cannot reproduce the wedged-full park")
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	r.Cancel(false)

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("default cancel hung: Run did not return within 8s; deferred sendInventory parked on a wedged sendCh")
	}
	require.Less(t, time.Since(start), 8*time.Second,
		"default cancel should free Run well under the unbounded hang; took %s", time.Since(start))

	require.True(t, fh.finalized, "Finalize must still run on default cancel before sendInventory")
}
```

- [ ] **Step 2: Run the test to verify it fails (RED)**

This test must be observed on Linux/Docker, not Windows (the file's `//go:build !windows` tag skips it on Windows; `make test` on Windows would silently pass-by-skip). Run inside a Linux environment:

Run: `go test ./internal/agent/... -run TestRunner_DefaultCancel_WedgedFull_InventoryDoesNotPark -v -timeout 60s`

Expected: FAIL with the `t.Fatal("default cancel hung: ...")` message at ~8s, because the current `sendInventory` calls the blocking `r.send` and parks on the full `sendCh` (the parent context never closes during a per-task cancel).

To make the RED unambiguous, confirm the failure is the 8s hang timeout, not the fill-loop `t.Fatal` (the latter would mean the wedge never reproduced). If the fill loop fails instead, the subprocess is not producing output fast enough - that is an environment issue, not the bug under test.

- [ ] **Step 3: Commit the failing test**

```bash
git add internal/agent/runner_cancel_test.go
git commit -m "test(agent): RED - default cancel parks on wedged sendInventory"
```

---

## Task 2: Regression test - Abandon under a wedged sendCh (RED)

`Abandon()` sets `r.abandoned` but NOT `r.cancelled`, and the Finalize defer still runs on abandon (it is gated only on `r.forced`). So this exercises the `r.abandoned.Load()` arm of the inventory gate the fix adds in Task 3.

**Files:**
- Test: `internal/agent/runner_cancel_test.go` (append a new test function)

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/runner_cancel_test.go`:

```go
// TestRunner_Abandon_WedgedFull_InventoryDoesNotPark is the
// sendInventory-wedge-escape criterion 2. Abandon() sets r.abandoned but not
// r.cancelled, and the Finalize defer still runs on abandon (gated only on
// r.forced), so the deferred sendInventory is reached. This exercises the
// r.abandoned.Load() arm of the inventory bounded-send gate.
//
// Return-bound assertion only; abandon also suppresses terminal status via the
// r.abandoned early return in sendFinalStatus.
func TestRunner_Abandon_WedgedFull_InventoryDoesNotPark(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 4)
	fh := &fakeHandle{dir: t.TempDir()}
	prov := &fakeProvider{handle: fh}

	task := &relayv1.DispatchTask{
		TaskId:   "t-inv-abandon-wedge",
		JobId:    "j-inv-abandon-wedge",
		Commands: singleCmd([]string{"sh", "-c", "while true; do echo x; done"}),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)
	r.SetProviderForTest(prov)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	deadline := time.Now().Add(5 * time.Second)
	for len(sendCh) < cap(sendCh) {
		if time.Now().After(deadline) {
			t.Fatal("sendCh never filled to capacity; cannot reproduce the wedged-full park")
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	r.Abandon()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("abandon hung: Run did not return within 8s; deferred sendInventory parked on a wedged sendCh")
	}
	require.Less(t, time.Since(start), 8*time.Second,
		"abandon should free Run well under the unbounded hang; took %s", time.Since(start))

	require.True(t, fh.finalized, "Finalize must still run on abandon before sendInventory")
}
```

- [ ] **Step 2: Run the test to verify it fails (RED)**

Run (on Linux/Docker): `go test ./internal/agent/... -run TestRunner_Abandon_WedgedFull_InventoryDoesNotPark -v -timeout 60s`

Expected: FAIL with the `t.Fatal("abandon hung: ...")` message at ~8s. Current `sendInventory` parks on the blocking `r.send` because neither `r.cancelled` nor `r.abandoned` gates it today.

- [ ] **Step 3: Commit the failing test**

```bash
git add internal/agent/runner_cancel_test.go
git commit -m "test(agent): RED - abandon parks on wedged sendInventory"
```

---

## Task 3: Production fix - bounded best-effort inventory send on the cancelled/abandoned path (GREEN)

**Files:**
- Modify: `internal/agent/runner.go:413-425` (`sendInventory`)

- [ ] **Step 1: Replace the `sendInventory` body with the gated bounded send**

Current `sendInventory` (runner.go:413-425):

```go
// sendInventory reports a workspace inventory entry to the coordinator.
func (r *Runner) sendInventory(e source.InventoryEntry) {
	r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_WorkspaceInventory{
		WorkspaceInventory: &relayv1.WorkspaceInventoryUpdate{
			SourceType:   e.SourceType,
			SourceKey:    e.SourceKey,
			ShortId:      e.ShortID,
			BaselineHash: e.BaselineHash,
			LastUsedAt:   e.LastUsedAt.Format("2006-01-02T15:04:05Z07:00"),
			Deleted:      e.Deleted,
		},
	}})
}
```

Replace it with:

```go
// sendInventory reports a workspace inventory entry to the coordinator. On a
// per-task cancel or abandon the cleanup runs through the deferred path while
// sendCh may still be wedged full and r.ctx (the parent/agent context) is not
// done, so a blocking send would park until agent shutdown. Mirror
// sendFinalStatus's cancelled branch: a room-first, bounded try-send that
// abandons the entry best-effort when sendCh is full. Dropping it is safe -
// Finalize already reconciled the workspace locally, the server is
// authoritative, and the entry is recomputed on next workspace use. Normal
// completion (none of cancelled/abandoned set) keeps the blocking send so
// inventory is still delivered under a merely-slow-but-live consumer.
func (r *Runner) sendInventory(e source.InventoryEntry) {
	msg := &relayv1.AgentMessage{Payload: &relayv1.AgentMessage_WorkspaceInventory{
		WorkspaceInventory: &relayv1.WorkspaceInventoryUpdate{
			SourceType:   e.SourceType,
			SourceKey:    e.SourceKey,
			ShortId:      e.ShortID,
			BaselineHash: e.BaselineHash,
			LastUsedAt:   e.LastUsedAt.Format("2006-01-02T15:04:05Z07:00"),
			Deleted:      e.Deleted,
		},
	}}
	if r.cancelled.Load() || r.abandoned.Load() {
		select {
		case r.sendCh <- msg:
		default:
			// sendCh full and wedged; abandon best-effort. Cleanup path only;
			// server is authoritative and Finalize already reconciled the workspace.
		}
		return
	}
	r.send(msg)
}
```

This is the only production change. `Cancel`, `Abandon`, `send`, `sendOrAbort`, `sendFinalStatus`, and the Finalize defer are untouched. No new field, channel, or sender. The bounded branch still writes only to `sendCh`, consumed by the single sender in `agent.connect`, so the "one bounded sender per gRPC stream" invariant is served (the change shortens a worst-case park). Inventory is a `WorkspaceInventoryUpdate`, not a write to `tasks.status` or `task_logs`, so the epoch-fence invariant is not in play.

- [ ] **Step 2: Run the two RED tests to verify they pass (GREEN)**

Run (on Linux/Docker):

```
go test ./internal/agent/... -run 'TestRunner_DefaultCancel_WedgedFull_InventoryDoesNotPark|TestRunner_Abandon_WedgedFull_InventoryDoesNotPark' -v -timeout 60s
```

Expected: PASS. Both return well under the 8s bound (the bounded try-send hits its `default` immediately on a full `sendCh`).

- [ ] **Step 3: Commit the fix**

```bash
git add internal/agent/runner.go
git commit -m "fix(agent): bound sendInventory on cancelled/abandoned cleanup path"
```

---

## Task 4: Regression test - normal completion still delivers inventory (blocking, unchanged)

Guards the load-bearing boundary from the spec: with none of `r.cancelled` / `r.abandoned` set, `sendInventory` must still call the blocking `r.send` and deliver the inventory entry. This proves the drop-on-cancel branch did not leak into normal completion.

**Files:**
- Test: `internal/agent/runner_cancel_test.go` (append a new test function)

- [ ] **Step 1: Write the test**

Append to `internal/agent/runner_cancel_test.go`:

```go
// TestRunner_NormalCompletion_DeliversInventory is the sendInventory-wedge-escape
// criterion 3. With headroom on sendCh and no cancel/abandon, a task that runs
// to normal completion must still deliver a WorkspaceInventory message via the
// blocking send. Guards that the bounded drop-on-cancel branch did not leak into
// the normal-completion path.
func TestRunner_NormalCompletion_DeliversInventory(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 4096)
	fh := &fakeHandle{dir: t.TempDir()}
	prov := &fakeProvider{handle: fh}

	task := &relayv1.DispatchTask{
		TaskId:   "t-inv-normal",
		JobId:    "j-inv-normal",
		Commands: singleCmd([]string{"sh", "-c", "echo done"}),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)
	r.SetProviderForTest(prov)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("runner did not return within 8s on normal completion")
	}

	require.True(t, fh.finalized, "Finalize must run on normal completion")

	// Drain sendCh and assert a WorkspaceInventory message was delivered.
	var sawInventory bool
	for {
		select {
		case msg := <-sendCh:
			if msg.GetWorkspaceInventory() != nil {
				sawInventory = true
			}
		default:
			require.True(t, sawInventory,
				"normal completion must deliver a WorkspaceInventory message via the blocking send")
			return
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it passes**

Run (on Linux/Docker): `go test ./internal/agent/... -run TestRunner_NormalCompletion_DeliversInventory -v -timeout 60s`

Expected: PASS. The task exits 0, the cleanup defer runs `Finalize` then `sendInventory`; with neither atomic set it takes the blocking `r.send` and the message lands on the roomy `sendCh`.

- [ ] **Step 3: Commit the test**

```bash
git add internal/agent/runner_cancel_test.go
git commit -m "test(agent): normal completion still delivers inventory"
```

---

## Task 5: Full-suite verification and red-before-green confirmation

**Files:** none (verification only).

- [ ] **Step 1: Run the full agent package test suite on Linux/Docker**

Run: `go test ./internal/agent/... -v -timeout 120s`

Expected: PASS, including all parent-fix tests:
`TestRunner_DefaultCancel_RunsWorkspaceFinalize`, `TestRunner_ForceCancel_SkipsWorkspaceFinalize`, `TestRunner_ForceCancel_ReturnsQuickly`, `TestRunner_ForceCancel_StillSendsTerminalFailed`, `TestRunner_DefaultCancel_WedgedFull_ReturnsQuickly`, `TestRunner_DefaultCancel_NonWedged_SendsTerminalFailed`, `TestRunner_Abandon_WedgedFull_ReturnsQuickly`, and `TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang`, plus the three new tests.

- [ ] **Step 2: Vet the package**

Run: `go vet ./internal/agent/...`

Expected: no output (clean).

- [ ] **Step 3 (optional but recommended): Race-detector run**

`go test -race` on this repo needs the MSYS2 mingw64 gcc, not the default Strawberry Perl gcc (which fails with exit 0xc0000139). Per project setup, set `CC=/c/msys64/mingw64/bin/gcc.exe`. The wedge tests are `//go:build !windows`, so run the race detector inside the Linux/Docker environment where these tests actually execute:

Run: `go test -race ./internal/agent/... -timeout 180s`

Expected: PASS with no data-race reports. The change adds only two atomic `.Load()` reads (`r.cancelled`, `r.abandoned`) on the runner's own cleanup goroutine - the same atomics `sendFinalStatus` already reads - so no new shared-state access is introduced.

- [ ] **Step 4: Confirm red-before-green is provable (spec criterion 5)**

This step proves the fix is load-bearing. On Linux/Docker:

1. Stash or revert only the `sendInventory` change in `internal/agent/runner.go` (keep the tests).
2. Run: `go test ./internal/agent/... -run 'TestRunner_DefaultCancel_WedgedFull_InventoryDoesNotPark|TestRunner_Abandon_WedgedFull_InventoryDoesNotPark' -v -timeout 60s`
   Expected: FAIL (both hang to the 8s bound) - confirming the tests detect the residual inventory park, not a sanitized environment.
3. Restore the fix and re-run the same command. Expected: PASS.

Do NOT commit anything in this step; it is a confirmation only. Restore the working tree to the committed (fixed) state before finishing.

---

## Self-Review

**Spec coverage:**

- Spec "Mechanism" / "Chosen approach A" -> Task 3 (gated bounded try-send mirroring `sendFinalStatus`, gated on `r.cancelled.Load() || r.abandoned.Load()`, normal path keeps blocking `r.send`).
- Spec success criterion 1 (cleanup-goroutine bound under wedged sendCh after default cancel, inventory park isolated) -> Task 1.
- Spec success criterion 2 (`Abandon()` under wedged sendCh, `r.abandoned` arm) -> Task 2.
- Spec success criterion 3 (normal-completion inventory still delivers, blocking) -> Task 4.
- Spec success criterion 4 (existing parent-fix tests stay green) -> Task 5 Step 1.
- Spec success criterion 5 (red-before-green provable on Linux/Docker) -> Task 1/2 Step 2 (RED) and Task 5 Step 4 (revert-and-confirm).
- Spec "Invariant-compliance argument" (one bounded sender; epoch fence not in play) -> documented in Task 3 Step 1 and the Background section.
- Spec "Files in scope" -> Task 3 (`runner.go`, `sendInventory` only) and Tasks 1/2/4 (`runner_cancel_test.go`).

**Placeholder scan:** No TBD/TODO/"handle edge cases"/"similar to" placeholders. Every code step shows the full code. No `make generate` step (no proto/SQL changes).

**Type consistency:** `r.cancelled` / `r.abandoned` are `atomic.Bool` (runner.go:26-28); `.Load()` is correct. `r.send`, `r.sendCh`, `source.InventoryEntry`, `relayv1.AgentMessage_WorkspaceInventory`, `relayv1.WorkspaceInventoryUpdate`, `msg.GetWorkspaceInventory()`, `fakeHandle`, `fakeProvider`, `singleCmd`, `SetProviderForTest`, `newRunner` all match existing definitions in `runner.go` / `runner_test.go` / `export_test.go` / the generated proto. The `msg` local is introduced in Task 3 and used consistently in both the bounded branch and the blocking call.

**Platform note:** All three new tests live in `runner_cancel_test.go` under its `//go:build !windows` tag and use a POSIX shell (`sh -c`). They are skipped by `make test` on Windows and MUST be observed on Linux/Docker, per the project's platform-gated-test-verification rule.

## Verify commands (summary)

- `go test ./internal/agent/... -v -timeout 120s` (full agent suite; run on Linux/Docker for the `!windows` tests)
- `go test ./internal/agent/... -run <TestName> -v -timeout 60s` (single test)
- `go vet ./internal/agent/...`
- `go test -race ./internal/agent/... -timeout 180s` (needs `CC=/c/msys64/mingw64/bin/gcc.exe` per project race-detector setup; run on Linux/Docker for the gated tests)
