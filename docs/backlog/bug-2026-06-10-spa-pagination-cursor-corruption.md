---
title: SPA pagination corrupts the cursor stack when next is clicked during a fetch
type: bug
status: open
created: 2026-06-10
priority: medium
source: full-codebase review (2026-06-10)
---

# SPA pagination corrupts the cursor stack when next is clicked during a fetch

## Summary
With `keepPreviousData`, `data` during a page transition is the previous page's payload, and the next button is only disabled by `!data?.next_cursor` - the stale cursor of the page just left. Clicking next twice quickly pushes a duplicate cursor onto the stack without advancing, so the first "prev" click afterward appears to do nothing. Affects both JobsPage and SchedulesPage.

## Proposal
Disable paging while showing placeholder data:

```tsx
const { data, isPlaceholderData, ... } = useJobs(sort, status, cursor)
...
disabled={!data?.next_cursor || isPlaceholderData}
```

Apply to next and prev in both pages.

## Related
- `web/src/jobs/JobsPage.tsx:50-54, 150`
- `web/src/schedules/SchedulesPage.tsx:155-163`
