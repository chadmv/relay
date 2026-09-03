---
title: A browser e2e spec for the dialog shell: focus trap, focus restore and Escape scoping in chromium and webkit
type: feature
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# A browser e2e spec for the dialog shell: focus trap, focus restore and Escape scoping in chromium and webkit

## Summary
The dialog shell's trap, restore and Escape behaviour are pinned in jsdom, which does no layout and no real focus management. The native-dialog item was closed as wontfix-for-now with a conjunctive trigger: a jsdom implementation of the dialog element plus an e2e dialog spec. This spec is the half of that trigger the project controls.

## Context
From lane DL (PR #181).

## Proposal
One spec opening each dialog surface, tabbing past both ends, pressing Escape with a nested popover open, and asserting focus lands back on the opener, in both engines.

## Related
- web/e2e, web/src/lib/dialogStack.ts
