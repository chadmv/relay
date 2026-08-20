---
title: Retro skill cannot be invoked mid-session
type: bug
status: closed
created: 2026-04-25
closed: 2026-05-04
resolution: fixed
source: 2026-04-18 password-auth retro — Known Limitations
---

# Retro skill cannot be invoked mid-session

## Summary
The retro skill cannot be invoked mid-session (skills load at session start); first-run verification was done by manually executing the skill steps.

## Resolution
Verified 2026-05-04 (`3810d82`): the retro skill is present in the session-start skill list and invocable mid-session via the Skill tool. The original limitation no longer applies.
