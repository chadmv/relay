---
title: Jobs pagination footer lacks absolute X-Y range
type: bug
status: open
created: 2026-06-05
priority: low
source: jobs-list-frontend retro (2026-06-05)
---

# Jobs pagination footer lacks absolute X-Y range

## Summary
The Jobs table pagination footer shows only the current-page count (`SHOWING N OF total`), not an absolute `X–Y of total` range. With opaque cursors and possibly-partial pages, the true start offset is not computable client-side without tracking cumulative page sizes.

## Context
The design mock shows `1–50 of 2,341`. The naive `stack.length * 50` offset breaks on partial pages, so the footer was simplified to a page count. To restore an accurate absolute range, track a running offset by accumulating actual page sizes as the user pages forward/back.

## Related
- `web/src/jobs/JobsPage.tsx` (pagination footer + cursor stack)
- `docs/retros/2026-06-05-web-jobs-list.md`
