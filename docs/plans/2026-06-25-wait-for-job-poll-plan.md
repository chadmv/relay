# relay_wait_for_job adaptive poll schedule - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the flat 2 s poll cadence in `relay_wait_for_job` with an adaptive schedule (500 ms x4 then 2 s steady) so sub-2 s jobs return within ~500 ms of completion without adding GET load to long waits.

**Architecture:** Pure client-side change in `internal/mcp/wait.go`. A pure helper `nextWaitInterval(attempt int) time.Duration` returns the inter-poll sleep for a given attempt index; the existing wait loop calls it instead of the flat `poll` value. The `s.waitPoll` struct field is preserved as a flat-interval override (non-zero = flat), so every existing test stays deterministic and green. No server, DB, `.sql`, `.proto`, gRPC, or shared-registry change. None of the six Invariants are touched.

**Tech Stack:** Go, standard library `time`, `testify/require`, `net/http/httptest`. Test harness reuses the existing `whoamiHandler` helper (see `internal/mcp/whoami_test_helper_test.go`).

---

## Slice independence

**Backend-only.** The MCP server is a Go REST client (`internal/mcp`). There is no frontend slice and no backend-endpoint dependency. This plan runs as a single backend slice; nothing in Phase 3 parallelizes against it.

## Test seam (decision)

**Chosen seam: a pure `nextWaitInterval(attempt int) time.Duration` helper, plus the existing `s.waitPoll` flat override - NO injectable sleep var.**

Rationale: the schedule itself is the only new logic, and a pure helper makes it deterministically unit-testable with zero timing dependence (no real sleep, no recorder, no flake). The loop's sleep mechanics (`select { <-ctx.Done(); <-time.After(d) }`) are unchanged and already proven by the existing loop tests, which inject a tiny `s.waitPoll` (10 ms) so they run fast. Redefining `s.waitPoll != 0` to mean "flat interval, bypass the adaptive schedule" keeps all four existing `wait_test.go` tests green unmodified and keeps the loop-level tests cheap. Adding a `sleepFn` package var would be a second, redundant seam for logic the pure helper already covers; per CLAUDE.md "Simplicity First" we do not add it.

## File structure

- Modify: `internal/mcp/wait.go`
  - Add constants `fastWaitPoll = 500 * time.Millisecond` and `fastWaitCount = 4` to the existing `const (...)` block (lines 11-15). Keep `defaultWaitPoll = 2 * time.Second` as the steady interval.
  - Add a pure unexported helper `nextWaitInterval(attempt int) time.Duration`.
  - Edit the wait loop in `callWaitForJob` (lines 56-100): track an attempt counter, and compute `waitFor` from the schedule when `s.waitPoll == 0`, or from the flat override when `s.waitPoll != 0`. Terminal/timeout/cancel/clamp behavior stays byte-for-byte equivalent.
- Modify: `internal/mcp/wait_test.go`
  - Add a pure unit test for `nextWaitInterval`.
  - Add a test that an adaptive (waitPoll == 0) fast job returns after exactly one fast interval.
  - Existing tests (`TestWaitForJob_TerminalImmediately`, `TestWaitForJob_RunningThenDone`, `TestWaitForJob_Timeout`, `TestWaitForJob_NegativeTimeout`, `TestWaitForJob_TimeoutTooLarge`) are unchanged and must stay green.

No other files change. `internal/mcp/server.go` keeps the `waitPoll time.Duration` field as-is (its doc comment already says "overridable in tests; 0 means use defaultWaitPoll"; the semantics broaden to "flat override" but the field, type, and zero-value default are unchanged, so no edit is required).

---

### Task 1: Pure adaptive-schedule helper

**Files:**
- Modify: `internal/mcp/wait.go:11-15` (constants), add helper after the constants
- Test: `internal/mcp/wait_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/mcp/wait_test.go`:

```go
func TestNextWaitInterval_AdaptiveSchedule(t *testing.T) {
	// First fastWaitCount (4) intervals are the fast poll; everything after is steady.
	require.Equal(t, 500*time.Millisecond, nextWaitInterval(0))
	require.Equal(t, 500*time.Millisecond, nextWaitInterval(1))
	require.Equal(t, 500*time.Millisecond, nextWaitInterval(2))
	require.Equal(t, 500*time.Millisecond, nextWaitInterval(3))
	require.Equal(t, 2*time.Second, nextWaitInterval(4))
	require.Equal(t, 2*time.Second, nextWaitInterval(5))
	require.Equal(t, 2*time.Second, nextWaitInterval(100))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestNextWaitInterval_AdaptiveSchedule -v`
Expected: FAIL - `undefined: nextWaitInterval`.

- [ ] **Step 3: Write minimal implementation**

In `internal/mcp/wait.go`, change the const block (lines 11-15) to:

```go
const (
	defaultWaitTimeout = 60 * time.Second
	maxWaitTimeout     = 300 * time.Second
	defaultWaitPoll    = 2 * time.Second        // steady-state poll interval
	fastWaitPoll       = 500 * time.Millisecond // poll interval during the fast phase
	fastWaitCount      = 4                       // number of fast intervals before widening
)
```

Add the helper immediately after the const block (before `terminalStatuses`):

```go
// nextWaitInterval returns the inter-poll sleep for the given zero-based attempt.
// The first fastWaitCount sleeps are fast (catching sub-2s jobs within ~500 ms of
// completion); every sleep thereafter is the steady interval, so a long wait does
// not increase GET load beyond today's 2 s cadence.
func nextWaitInterval(attempt int) time.Duration {
	if attempt < fastWaitCount {
		return fastWaitPoll
	}
	return defaultWaitPoll
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run TestNextWaitInterval_AdaptiveSchedule -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/wait.go internal/mcp/wait_test.go
git commit -m "feat(mcp): add adaptive nextWaitInterval helper for wait poll"
```

---

### Task 2: Drive the wait loop from the adaptive schedule

**Files:**
- Modify: `internal/mcp/wait.go:56-100` (the poll-interval setup and the loop)
- Test: `internal/mcp/wait_test.go`

- [ ] **Step 1: Write the failing test**

This test leaves `s.waitPoll == 0` so the adaptive schedule is in force, and asserts an adaptive (non-flat) wait returns promptly after one fast interval. The backend returns `running` once then `done`; with the adaptive schedule the single sleep is ~500 ms, so a 5 s timeout cannot fire. We assert correctness, not wall-clock timing, to stay flake-free.

Add to `internal/mcp/wait_test.go`:

```go
func TestWaitForJob_AdaptiveScheduleFastJob(t *testing.T) {
	var n int32
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&n, 1)
		status := "running"
		if current >= 2 {
			status = "done"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "status": status})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	// waitPoll left 0: exercise the adaptive schedule (first sleep is fastWaitPoll).

	out, terr := s.callWaitForJob(context.Background(), waitForJobArgs{JobID: "j1", TimeoutSeconds: 5})
	require.Nil(t, terr)
	require.Equal(t, "done", out["status"])
	require.GreaterOrEqual(t, atomic.LoadInt32(&n), int32(2))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestWaitForJob_AdaptiveScheduleFastJob -v`
Expected: FAIL - with the current flat `defaultWaitPoll = 2s` the test would still pass functionally, but it is here to lock behavior in. If it already passes, that is acceptable (the helper is wired in the next step); proceed to Step 3 to wire the loop. (Note: this test guards against a regression where someone removes the adaptive call; it is a behavioral guard, not a strict RED. The strict RED test is Task 1's `TestNextWaitInterval_AdaptiveSchedule`.)

- [ ] **Step 3: Wire the loop to the schedule**

In `internal/mcp/wait.go`, replace the poll-interval setup (lines 56-60) and the loop (lines 65-100) so the inter-poll sleep comes from `nextWaitInterval(attempt)` when there is no flat override, and from `s.waitPoll` when there is. Everything else (immediate first GET, terminal check, deadline check, remaining-time clamp, ctx cancel) is preserved exactly.

Replace lines 56-60:

```go
	// Determine poll interval. A non-zero s.waitPoll is a flat-interval override
	// (used by tests for determinism); zero means use the adaptive schedule.
	flatPoll := s.waitPoll
```

Replace the loop (lines 65-100) with:

```go
	var lastResp map[string]any
	for attempt := 0; ; attempt++ {
		if err := s.do(ctx, "GET", path, nil, &lastResp); err != nil {
			return nil, MapError(err)
		}
		status, _ := lastResp["status"].(string)
		if terminalStatuses[status] {
			return lastResp, nil
		}

		// Check if we've hit the deadline.
		if !time.Now().Before(deadline) {
			return map[string]any{
				"timed_out":  true,
				"last_state": lastResp,
			}, nil
		}

		poll := flatPoll
		if poll == 0 {
			poll = nextWaitInterval(attempt)
		}

		remaining := time.Until(deadline)
		waitFor := poll
		if remaining < poll {
			waitFor = remaining
		}
		if waitFor <= 0 {
			return map[string]any{
				"timed_out":  true,
				"last_state": lastResp,
			}, nil
		}

		select {
		case <-ctx.Done():
			return nil, &ToolError{Code: "cancelled", Message: "context cancelled"}
		case <-time.After(waitFor):
		}
	}
```

- [ ] **Step 4: Run the full wait suite to verify green**

Run: `go test ./internal/mcp/... -run TestWaitForJob -v`
Expected: PASS for all of `TestWaitForJob_TerminalImmediately`, `TestWaitForJob_RunningThenDone`, `TestWaitForJob_Timeout`, `TestWaitForJob_NegativeTimeout`, `TestWaitForJob_TimeoutTooLarge`, and `TestWaitForJob_AdaptiveScheduleFastJob`. The existing tests inject `s.waitPoll = 10ms`, which is now the flat override, so they behave exactly as before.

- [ ] **Step 5: Run vet and the whole package**

Run: `go vet ./internal/mcp/...`
Expected: no output (clean).

Run: `go test ./internal/mcp/...`
Expected: ok.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/wait.go internal/mcp/wait_test.go
git commit -m "feat(mcp): drive wait loop from adaptive poll schedule"
```

---

## Behavior preserved (verification checklist)

These are guaranteed by reusing the existing structure; confirm each in review of the Task 2 diff:

- First GET is immediate (loop GETs before any sleep). An already-terminal job returns on attempt 0 with zero sleeps - covered by `TestWaitForJob_TerminalImmediately`.
- Terminal status set unchanged (`done`, `failed`, `cancelled` via `terminalStatuses`).
- Timeout return shape unchanged (`{timed_out: true, last_state: ...}`) and the remaining-time clamp is unchanged - covered by `TestWaitForJob_Timeout`.
- `ctx.Done()` cancellation returns `{Code: "cancelled"}` - unchanged select arm.
- Flat override (`s.waitPoll != 0`) forces a flat interval and bypasses the schedule - covered by every existing test that sets `s.waitPoll`.

## Invariants

Client-side only. No write to `tasks.status` / `task_logs` (epoch fence n/a), no job-spec ingestion (single job-spec pipeline n/a), no gRPC stream send (one bounded sender n/a), no connection teardown (identity-checked teardown n/a), no shared registry getter (no interior pointers n/a), no HTTP request body decode (single JSON entry point n/a). Confirmed: no server change, no DB, no `.sql`, no `.proto`.

## Verify commands

These are NOT `//go:build`-gated, so they run on Windows and in Docker without containers:

```bash
go test ./internal/mcp/...
go vet ./internal/mcp/...
```

Optional (race), per repo memory the race toolchain needs MSYS2 mingw64 gcc:

```bash
CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./internal/mcp/... -run TestWaitForJob
```

## Self-review

- **Spec coverage:** adaptive schedule (constants `fastWaitPoll`/`fastWaitCount`/`defaultWaitPoll`) - Task 1. Loop driven by schedule, first GET immediate, terminal/timeout/cancel/clamp preserved - Task 2. Flat override retained (`s.waitPoll != 0`) - Task 2 `flatPoll` branch + existing tests. Deterministic schedule test with no real sleeps - Task 1. No server/DB/sql/proto change, Invariants untouched - documented above. All acceptance criteria 1-4 mapped.
- **Placeholder scan:** none; every code step shows full code.
- **Type consistency:** `nextWaitInterval(attempt int) time.Duration` used identically in Task 1 and Task 2. `fastWaitCount`, `fastWaitPoll`, `defaultWaitPoll`, `flatPoll`, `s.waitPoll` consistent throughout.
