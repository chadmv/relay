---
title: Deprecated `command` proto field still defined
type: bug
status: closed
created: 2026-04-25
closed: 2026-04-26
source: 2026-04-25 multi-command-tasks retro — Known Limitations
---

# Deprecated `command` proto field still defined

## Summary
The deprecated `command` proto field is still defined — server stops populating it, agent stops reading it, but field number 3 is reserved and the field remains until a follow-up release retires it cleanly.

## Resolution

Closed on 2026-04-26. Removed `repeated string command = 3 [deprecated = true]` from the `DispatchTask` proto message and replaced it with `reserved 3; reserved "command";` so the field number and name can never be accidentally reused. Implemented in commit `2cd0a5d` (proto: remove deprecated DispatchTask.command field).
