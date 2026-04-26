---
title: Grace window is per-server process
type: bug
status: open
created: 2026-04-25
source: 2026-04-22 major-concurrency-fixes retro — Known Limitations
---

# Grace window is per-server process

## Summary
**Grace window is per-server process**: if the server restarts (not just the agent), grace timers are re-seeded from the DB but the in-memory state is lost. Tasks assigned to workers that were online at crash time get a fresh grace window rather than the remaining time.
