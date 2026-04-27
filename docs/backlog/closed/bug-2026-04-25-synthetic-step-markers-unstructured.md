---
title: Synthetic step markers aren't structured
type: bug
status: closed
created: 2026-04-25
closed: 2026-04-26
source: 2026-04-25 multi-command-tasks retro — Known Limitations
---

# Synthetic step markers aren't structured

## Summary
The synthetic step markers aren't structured. Anything that wants to render per-step status in a UI has to parse the marker line. A `step_index` field on `TaskLogChunk` is a follow-up.

## Resolution

Closed on 2026-04-26. Added `int32 step_index = 5` and `int32 step_total = 6` to the `TaskLogChunk` proto message. The agent runner now stamps these on every log chunk emitted during a step (1-indexed; 0 = PREPARE-phase chunk). The synthetic text marker line is retained for one release so existing log-tailing tools see no change. Implemented in commit `c459104` (agent: structured step_index/step_total on TaskLogChunk).
