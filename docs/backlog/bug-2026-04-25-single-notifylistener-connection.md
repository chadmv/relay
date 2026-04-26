---
title: Single NotifyListener connection — dispatch gap during reconnect
type: bug
status: open
created: 2026-04-25
source: 2026-04-22 major-concurrency-fixes retro — Known Limitations
---

# Single NotifyListener connection — dispatch gap during reconnect

## Summary
**Single NotifyListener connection**: if the dedicated LISTEN connection drops, `NotifyListener.session()` reconnects with backoff. During the gap, the 30s safety-net poll provides coverage, but there is a window where a task submission is not immediately dispatched.
