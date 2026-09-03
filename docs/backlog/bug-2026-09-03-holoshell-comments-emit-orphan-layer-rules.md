---
title: HoloShell.tsx and its test spell two layering utilities in comments, which Tailwind v4 emits as orphan rules
type: bug
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# HoloShell.tsx and its test spell two layering utilities in comments, which Tailwind v4 emits as orphan rules

## Summary
Tailwind v4 scans prose as source, and a comment in HoloShell.tsx and one in HoloShell.test.tsx spell two z-index utility classes that no component uses, so the production CSS carries two rules with no owner. The dialog-shell guard from lane DL strips comments before scanning and so does not see them; the CSS bundle does.

## Context
Found in lane DL's first re-verify.

## Proposal
Reword the two comments so no class-shaped token appears, rebuild, and confirm the two rules are gone from the bundle. Consider a guard that diffs the emitted class list against the classes referenced outside comments.

## Related
- web/src/HoloShell.tsx, web/src/HoloShell.test.tsx, web/CLAUDE.md
