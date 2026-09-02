---
title: Agent-reported task progress, the enabler for the hi-fi's per-task progress bar
type: idea
status: open
created: 2026-09-02
priority: low
source: 2026-09-01 worker-detail-tasks-panel spec (lane E), Decision 8
---

# Agent-reported task progress

## Summary
The hi-fi's current-tasks panel shows a progress bar per task. Relay computes no task progress: there
is no column, no proto field and nothing on the agent that could report one, so the panel omits it
rather than fabricate it. Making it real is a protocol change: a progress field on the agent's status
message, a column or derived store, and a decision about what a fraction means for an arbitrary
subprocess (frames rendered, a script-emitted marker, elapsed over a declared estimate).

## Related
- [[feature-2026-07-01-per-task-timing]] (the timing half of the same panel)
- [[feature-2026-06-05-worker-detail-activity-panel]] (closed)
- internal/proto, internal/agent/runner.go
