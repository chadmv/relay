---
title: The Jobs timeline's truncation banner is not announced to assistive technology
type: bug
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# The Jobs timeline's truncation banner is not announced to assistive technology

## Summary
When the timeline walk stops at three pages the view renders a banner saying the window holds more jobs than shown. The banner is plain text with no live region and no role, so a screen-reader user paging through a busy window is not told the picture is incomplete.

## Context
From the JF review (PR #184); deferred out of its fix round.

## Proposal
Render the banner as a status region or announce it through the page's polite live region once per truncated result, and pin it with a test that reads the region.

## Related
- web/src/jobs/JobsTimeline.tsx
