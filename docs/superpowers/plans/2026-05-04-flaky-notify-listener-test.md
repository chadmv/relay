# Flaky NotifyListener Test Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the intermittent flake in `TestNotifyListener_TriggersOnNotify` by replacing a fixed-sleep + single-NOTIFY pattern with a send-until-consumed retry loop.

**Architecture:** Test-only change. The listener's `LISTEN` attach is asynchronous; Postgres NOTIFY is dropped if no session is listening. Instead of guessing how long the attach takes (the current 200 ms sleep), the test retries `pg_notify` until the listener picks it up. Production code in `internal/scheduler/notify.go` is unchanged.

**Tech Stack:** Go 1.x, `pgx/v5`, `testify` (`require.Eventually`, `assert.Equal`), Postgres `LISTEN/NOTIFY`, `make test-integration`.

**Spec:** [docs/superpowers/specs/2026-05-04-flaky-notify-listener-test-design.md](../specs/2026-05-04-flaky-notify-listener-test-design.md)

---

## Task 1: Rewrite TestNotifyListener_TriggersOnNotify

**Files:**
- Modify: `internal/scheduler/notify_test.go` (function `TestNotifyListener_TriggersOnNotify`, lines 17–55)

**Context for the engineer:**
- The file has a `//go:build integration` tag — these tests only run via `make test-integration` (which requires Docker Desktop and the `p4` CLI on PATH per CLAUDE.md).
- `newTestPoolFromQueries(t)` is a test helper defined elsewhere in the `scheduler_test` package — leave it alone.
- The companion test `TestNotifyListener_TriggersOnceAtStart` (lines 57–76) **must not be modified**. It explicitly tests the startup-drain `trigger()` call at [internal/scheduler/notify.go:70](../../../internal/scheduler/notify.go#L70).
- `pgxpool.Pool.Exec` accepts parameterized queries via positional `$1`, `$2`, ... arguments — use this rather than concatenating channel names into the SQL string.

- [ ] **Step 1: Read the current test file**

Run: `cat internal/scheduler/notify_test.go`

Confirm `TestNotifyListener_TriggersOnNotify` matches what's described in the spec. If not, stop and reconcile before proceeding.

- [ ] **Step 2: Replace the body of TestNotifyListener_TriggersOnNotify**

Replace lines 17–55 (the entire `TestNotifyListener_TriggersOnNotify` function) with:

```go
func TestNotifyListener_TriggersOnNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool := newTestPoolFromQueries(t)

	var triggered atomic.Int32
	l := scheduler.NewNotifyListener(pool, func() {
		triggered.Add(1)
	})

	go l.Run(ctx)

	// Postgres NOTIFY is dropped on the floor if no session has LISTENed yet.
	// We don't know exactly when the listener's LISTEN has attached, so we
	// retry the NOTIFY until we observe the trigger fire. Once it fires,
	// LISTEN is definitely attached and subsequent NOTIFYs are reliable.
	sendUntilConsumed := func(channel string) {
		before := triggered.Load()
		require.Eventually(t, func() bool {
			_, err := pool.Exec(ctx, "SELECT pg_notify($1, '')", channel)
			require.NoError(t, err)
			return triggered.Load() > before
		}, 5*time.Second, 20*time.Millisecond)
	}

	sendUntilConsumed("relay_task_submitted")
	sendUntilConsumed("relay_task_completed")

	// Unrelated channel should be ignored. Listener is verified attached by now.
	before := triggered.Load()
	_, err := pool.Exec(ctx, "SELECT pg_notify('some_other_channel', '')")
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, before, triggered.Load())
}
```

Leave `TestNotifyListener_TriggersOnceAtStart` and the imports unchanged. The imports already cover `context`, `sync/atomic`, `testing`, `time`, `relay/internal/scheduler`, `testify/assert`, `testify/require` — no new imports needed.

- [ ] **Step 3: Verify the file compiles**

Run: `go vet -tags integration ./internal/scheduler/...`

Expected: no output (clean).

If vet reports an error, fix it and re-run before proceeding.

- [ ] **Step 4: Run the rewritten test in isolation**

Run: `go test -tags integration -p 1 ./internal/scheduler/... -run TestNotifyListener_TriggersOnNotify -v -timeout 120s`

Expected: `--- PASS: TestNotifyListener_TriggersOnNotify` and `PASS` at the end.

This requires Docker Desktop running. If it fails with a Docker/testcontainers error, that is an environment problem, not a plan problem — surface it to the user.

- [ ] **Step 5: Run both NotifyListener tests together to confirm no regression**

Run: `go test -tags integration -p 1 ./internal/scheduler/... -run TestNotifyListener -v -timeout 120s`

Expected: both `TestNotifyListener_TriggersOnNotify` and `TestNotifyListener_TriggersOnceAtStart` PASS.

- [ ] **Step 6: Run the test 5 times in a row to confirm the flake is gone**

Run: `go test -tags integration -p 1 -count=5 ./internal/scheduler/... -run TestNotifyListener_TriggersOnNotify -v -timeout 300s`

Expected: 5 consecutive PASS, no FAIL.

If any run fails, the fix is incomplete. Capture the failure output and stop — do not proceed to commit.

- [ ] **Step 7: Run the full integration suite once**

Run: `make test-integration`

Expected: all tests pass. This is a regression check — make sure neither test was somehow load-bearing for sibling tests.

If unrelated tests fail, do NOT assume they are caused by this change without evidence (the integration suite has historical flakes — that's the entire premise of this task). Re-run any failing tests in isolation to triage. Surface persistent unrelated failures to the user before committing.

- [ ] **Step 8: Commit the test fix**

```bash
git add internal/scheduler/notify_test.go
git commit -m "test(scheduler): de-flake TestNotifyListener_TriggersOnNotify

Postgres NOTIFY is dropped if no session has LISTENed yet. The previous
test relied on a 200ms sleep to wait for the listener's LISTEN to attach,
which was insufficient under integration-suite load. Retry pg_notify until
the listener picks it up; once it does, LISTEN is definitely attached and
the rest of the test runs deterministically."
```

---

## Task 2: Close the backlog item

**Files:**
- Move: `docs/backlog/bug-2026-05-01-flaky-testnotifylistener-triggersonnotify.md` → `docs/backlog/closed/bug-2026-05-01-flaky-testnotifylistener-triggersonnotify.md`

**Context for the engineer:**
- Per project memory, closing a backlog item via `git mv` to `docs/backlog/closed/` is required scope when a task closes the item — not optional cleanup.
- The `docs/backlog/closed/` directory already exists.

- [ ] **Step 1: Move the backlog file with git mv**

Run:
```bash
git mv docs/backlog/bug-2026-05-01-flaky-testnotifylistener-triggersonnotify.md docs/backlog/closed/bug-2026-05-01-flaky-testnotifylistener-triggersonnotify.md
```

Expected: no output. `git status` should now show one rename.

- [ ] **Step 2: Verify the move**

Run: `git status --short`

Expected: a single line of the form
```
R  docs/backlog/bug-2026-05-01-flaky-testnotifylistener-triggersonnotify.md -> docs/backlog/closed/bug-2026-05-01-flaky-testnotifylistener-triggersonnotify.md
```

- [ ] **Step 3: Commit the move**

```bash
git commit -m "docs(backlog): close flaky TestNotifyListener_TriggersOnNotify

Fixed in the preceding commit."
```

---

## Done when

- `internal/scheduler/notify_test.go` contains the rewritten `TestNotifyListener_TriggersOnNotify` with the `sendUntilConsumed` helper.
- `TestNotifyListener_TriggersOnceAtStart` is unchanged.
- `internal/scheduler/notify.go` is unchanged.
- 5+ consecutive runs of the test pass with no flake.
- The backlog file has been moved to `docs/backlog/closed/` via `git mv`.
- Two commits exist on the branch: the test fix and the backlog close.
