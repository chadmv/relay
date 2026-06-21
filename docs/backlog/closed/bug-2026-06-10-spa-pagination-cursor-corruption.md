---
title: SPA pagination corrupts the cursor stack when next is clicked during a fetch
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-21
resolution: fixed
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

## Resolution
Fixed 2026-06-21 (spa-pagination-cursor-corruption). Both JobsPage and SchedulesPage now
destructure `isPlaceholderData` from their React Query result and disable BOTH the next and prev
pagination buttons while it is true, so no cursor mutation happens while the displayed rows still
belong to the previous page. This closes the double-next-during-fetch window where the stale
`next_cursor` of the page just left was pushed onto the cursor stack, stranding a later prev.
Because the cursor is part of each query key, a background `refetchInterval` poll of a stable page
does not enter placeholder state, so the buttons are not needlessly disabled between polls (code
review confirmed - no per-poll regression). Each page got a non-vacuous regression test (a deferred
page-2 fetch held in flight; both buttons asserted disabled during the in-flight window), proven
RED against the unfixed gating. No other paginated list in the SPA shares the pattern (WorkersPage
has no cursor pagination). Code review returned no findings. The committed `web/dist` scaffold
artifact was intentionally not rebuilt (no prior frontend PR maintains it; it is regenerated at
server-build time).
