---
title: "`parseDurationEnv` silently falls back on garbage input"
type: bug
status: closed
created: 2026-04-25
source: 2026-04-25 perforce-workspace-management retro — Known Limitations
---

# `parseDurationEnv` silently falls back on garbage input

## Summary
**`parseDurationEnv` silently falls back on garbage input.** `RELAY_WORKSPACE_MAX_AGE=7days` (correct intent, wrong format) silently disables age-based eviction with no log line. Operators won't notice until the disk fills up.
