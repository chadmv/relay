---
title: One app-level focus-visible rule instead of per-control outlines
type: feature
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# One app-level focus-visible rule instead of per-control outlines

## Summary
There is no app-level focus-visible rule. Lane TB gave the task rows an inset ring, lane MF gave the split separator the accent outline, and every other control falls back to the browser default, so keyboard focus looks different on each page. A shared token in tokens.css applied through a base rule would make the three agree and cover the controls nobody has touched.

## Context
From the TB and MF reviews.

## Proposal
Define the outline once in tokens.css, apply it through a base focus-visible rule, delete the per-control variants, and extend the keyboard e2e spec to assert the computed outline on one control per page.

## Related
- web/src/tokens.css, web/e2e/keyboard.spec.ts
