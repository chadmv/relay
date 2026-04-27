---
title: Remove synthetic step marker text line once consumers use step_index/step_total
type: idea
status: open
created: 2026-04-26
source: 2026-04-26 multi-command-tech-debt retro — Known Limitations
---

# Remove synthetic step marker text line once consumers use step_index/step_total

## Summary
The synthetic `=== relay step N/M ===` text marker line in `sendStepMarker` (`internal/agent/runner.go`) is now redundant — `step_index` and `step_total` on `TaskLogChunk` carry the same information in structured form. The text line was retained for one release so existing log-tailing tools see no behavioral change. Once any consumers that render per-step status have been updated to read the structured fields, the text line should be removed (one-line deletion in `sendStepMarker`).

## Related
- Structured fields added in commit `c459104` (agent: structured step_index/step_total on TaskLogChunk)
- `internal/agent/runner.go` — `sendStepMarker` function
