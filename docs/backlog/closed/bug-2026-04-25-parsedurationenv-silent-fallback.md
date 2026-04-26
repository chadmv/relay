---
title: "`parseDurationEnv` silently falls back on garbage input"
type: bug
status: closed
created: 2026-04-25
closed: 2026-04-26
resolution: fixed
source: 2026-04-25 perforce-workspace-management retro — Known Limitations
---

# `parseDurationEnv` silently falls back on garbage input

## Summary
**`parseDurationEnv` silently falls back on garbage input.** `RELAY_WORKSPACE_MAX_AGE=7days` (correct intent, wrong format) silently disables age-based eviction with no log line. Operators won't notice until the disk fills up.

## Resolution
Added a `name string` parameter to `parseDurationEnv` and a `log.Printf` warning when a non-empty value fails to parse. Empty values (var not set) remain silent. Fixed in commit b067908; tests added in `cmd/relay-agent/main_test.go`.
