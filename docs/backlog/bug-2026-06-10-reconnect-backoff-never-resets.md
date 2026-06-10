---
title: Reconnect backoff never resets in agent and NotifyListener
type: bug
status: open
created: 2026-06-10
priority: medium
source: full-codebase review (2026-06-10)
---

# Reconnect backoff never resets in agent and NotifyListener

## Summary
Two instances of the same bug class. (1) Agent: `connect` has no nil-returning path (every return site returns a non-nil error, including the recv loop), so the `backoff = time.Second` reset is unreachable. Backoff doubles on every reconnect over the agent's lifetime regardless of how long the previous session was healthy; after ~6 disconnects total, every future blip costs a 60s outage. (2) Server: `NotifyListener`'s backoff reset is only reachable on shutdown, so LISTEN/NOTIFY reconnects also degrade monotonically toward the 60s cap, leaving dispatch latency at the 30s safety poll during the gap.

## Proposal
Reset backoff after a session that was healthy long enough:

```go
start := time.Now()
err := connectOrSession(ctx)
if time.Since(start) > 30*time.Second {
    backoff = time.Second
}
```

For the agent, signal successful registration out of `connect` (e.g. a `registered bool` return) and reset on that. Also remove the dead error branch on `buildRegisterRequest` (always returns nil).

## Related
- `internal/agent/agent.go:94, 117-120, 178-185`
- `internal/scheduler/notify.go:28-49`
