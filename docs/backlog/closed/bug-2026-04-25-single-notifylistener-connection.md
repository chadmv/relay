---
title: Single NotifyListener connection — dispatch gap during reconnect
type: bug
status: closed
created: 2026-04-25
closed: 2026-04-26
resolution: fixed
source: 2026-04-22 major-concurrency-fixes retro — Known Limitations
---

# Single NotifyListener connection — dispatch gap during reconnect

## Summary
**Single NotifyListener connection**: if the dedicated LISTEN connection drops, `NotifyListener.session()` reconnects with backoff. During the gap, the 30s safety-net poll provides coverage, but there is a window where a task submission is not immediately dispatched.

## Resolution
`NotifyListener.session()` now invokes `trigger()` once after both `LISTEN` statements succeed, before entering `WaitForNotification`. This drains anything submitted during a startup or reconnect gap via the dispatcher's idempotent `Trigger`. New `TestNotifyListener_TriggersOnceAtStart` asserts the cold-start drain. Spec: [docs/superpowers/specs/2026-04-26-dispatcher-lifecycle-correctness-design.md](../../superpowers/specs/2026-04-26-dispatcher-lifecycle-correctness-design.md).
