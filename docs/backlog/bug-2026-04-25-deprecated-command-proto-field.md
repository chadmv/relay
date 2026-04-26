---
title: Deprecated `command` proto field still defined
type: bug
status: open
created: 2026-04-25
source: 2026-04-25 multi-command-tasks retro — Known Limitations
---

# Deprecated `command` proto field still defined

## Summary
The deprecated `command` proto field is still defined — server stops populating it, agent stops reading it, but field number 3 is reserved and the field remains until a follow-up release retires it cleanly.
