---
title: Grace window is per-server process
type: bug
status: closed
created: 2026-04-25
closed: 2026-04-26
resolution: fixed
source: 2026-04-22 major-concurrency-fixes retro — Known Limitations
---

# Grace window is per-server process

## Summary
**Grace window is per-server process**: if the server restarts (not just the agent), grace timers are re-seeded from the DB but the in-memory state is lost. Tasks assigned to workers that were online at crash time get a fresh grace window rather than the remaining time.

## Resolution
New `workers.disconnected_at TIMESTAMPTZ NULL` column (migration 000009) persists the disconnect moment. `UpdateWorkerStatus` writes `now()` on offline and `NULL` on online. New `GraceRegistry.StartWithDuration` and `ExpireNow` allow the startup reconciler to honor remaining grace: `disconnected_at` NULL → full window; positive remaining → partial; expired → fire synchronously. A worker 1m55s into a 2m grace before crash now gets ~5s after restart, not a fresh 2m. Server crashloops can no longer reset grace indefinitely. Spec: [docs/superpowers/specs/2026-04-26-dispatcher-lifecycle-correctness-design.md](../../superpowers/specs/2026-04-26-dispatcher-lifecycle-correctness-design.md).
