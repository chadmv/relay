---
title: Fix flaky TestNotifyListener_TriggersOnNotify
date: 2026-05-04
status: design
---

# Fix flaky TestNotifyListener_TriggersOnNotify

## Problem

`TestNotifyListener_TriggersOnNotify` in `internal/scheduler/notify_test.go` flakes intermittently under `make test-integration` but passes in isolation.

The test uses `time.Sleep(200 * time.Millisecond)` to wait for the listener's `LISTEN` to attach before issuing `pg_notify`. Postgres NOTIFY only delivers to currently-listening sessions; notifications fired before `LISTEN` attaches are silently dropped. Under integration-suite load, the 200 ms sleep can be insufficient.

The first NOTIFY's assertion (`triggered >= 1`) is masked by the listener's startup-drain `trigger()` call at [internal/scheduler/notify.go:70](../../../internal/scheduler/notify.go#L70), which always increments the counter once on attach. The second NOTIFY's assertion (`triggered >= 2`) is what actually fails when both NOTIFYs arrive before LISTEN attaches.

## Approach

Test-only fix. Replace the fixed sleep + single-NOTIFY pattern with a "send-until-consumed" helper that retries `pg_notify` until the listener picks it up.

Production code (`internal/scheduler/notify.go`) is unchanged. The companion test `TestNotifyListener_TriggersOnceAtStart` is unchanged (it depends on the startup-drain behavior).

## Test rewrite

In `TestNotifyListener_TriggersOnNotify`, replace the sleep + two NOTIFY blocks with:

```go
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

// Unrelated channel should be ignored.
before := triggered.Load()
_, err := pool.Exec(ctx, "SELECT pg_notify('some_other_channel', '')")
require.NoError(t, err)
time.Sleep(200 * time.Millisecond)
assert.Equal(t, before, triggered.Load())
```

Keep the `defer cancel()`, pool setup, `NewNotifyListener` construction, and `go l.Run(ctx)` lines as they are.

## Why this works

- The first `sendUntilConsumed` call retries until the counter advances. The startup-drain `trigger()` may satisfy the assertion before any real NOTIFY is delivered — that is fine, because once the drain has fired, `LISTEN` is definitely attached, and subsequent NOTIFYs are guaranteed to be delivered.
- The second `sendUntilConsumed` call runs against a verified-attached listener and converges in 1–2 iterations.
- The unrelated-channel check still uses a fixed 200 ms wait. By that point the listener is verified attached, so flakiness is not possible there.

## Verification

- `go test -tags integration -p 1 ./internal/scheduler/... -run TestNotifyListener -v -timeout 120s` passes in isolation.
- `make test-integration` run 5+ consecutive times with no flake on `TestNotifyListener_TriggersOnNotify`.
- `TestNotifyListener_TriggersOnceAtStart` still passes (regression check on the startup-drain path).

## Risks

- The retry loop fires extra NOTIFYs beyond what the test strictly needs. Each `pg_notify` is microseconds and the listener's `trigger()` is an `atomic.Add`; cost is negligible.
- Failure window grows from 2 s to 5 s per channel. Acceptable for a flaky-test fix; failures are still surfaced clearly.

## Out of scope

- Adding a production-side `Ready()` signal to `NotifyListener`. No current caller needs ordering guarantees on listener startup; deferred unless a real consumer appears.
- Changes to `notify.go` itself or to `TestNotifyListener_TriggersOnceAtStart`.

## Done when

- `TestNotifyListener_TriggersOnNotify` passes reliably across 5+ consecutive `make test-integration` runs.
- `docs/backlog/bug-2026-05-01-flaky-testnotifylistener-triggersonnotify.md` is moved to `docs/backlog/closed/` as part of the fix (per project convention: backlog housekeeping is in-scope).
