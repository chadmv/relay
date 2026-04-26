---
title: Synthetic step markers aren't structured
type: bug
status: open
created: 2026-04-25
source: 2026-04-25 multi-command-tasks retro — Known Limitations
---

# Synthetic step markers aren't structured

## Summary
The synthetic step markers aren't structured. Anything that wants to render per-step status in a UI has to parse the marker line. A `step_index` field on `TaskLogChunk` is a follow-up.
