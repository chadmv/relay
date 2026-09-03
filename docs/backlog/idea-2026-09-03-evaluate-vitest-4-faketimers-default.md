---
title: Decide deliberately whether to adopt vitest 4's wider fakeTimers.toFake default
type: idea
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# Decide deliberately whether to adopt vitest 4's wider fakeTimers.toFake default

## Summary
Vitest 4 widened the default list of timer APIs that vi.useFakeTimers fakes. Lane T pinned the vitest 2 list in vitest.config.ts so the upgrade changed no test's behaviour; that pin is a decision deferred, not made. Several suites written since (the debounce-window and burst-of-keystrokes tests) rely on shouldAdvanceTime and on Date being faked, and the wider default may simplify or break them.

## Context
From the combined review of PR #177.

## Proposal
Remove the pin on a branch, run the SPA suite three times, and either adopt the default with the failures fixed or keep the pin with a comment stating which API's faking breaks which test.

## Related
- web/vitest.config.ts
