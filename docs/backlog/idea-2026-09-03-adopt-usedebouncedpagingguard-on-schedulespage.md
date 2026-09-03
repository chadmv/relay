---
title: Adopt the shared useDebouncedPagingGuard on SchedulesPage
type: idea
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# Adopt the shared useDebouncedPagingGuard on SchedulesPage

## Summary
Lanes SF and JF found the same debounce-window cursor race and fixed it twice in the same day: SF inline on SchedulesPage (PR #182), JF as the shared hook useDebouncedPagingGuard on JobsPage (PR #184) after SF had merged. The two agree on trimmed-versus-trimmed comparison and on resetting the pager when the debounced value lands; the schedules page should call the hook so the next list page does not write a third copy.

## Context
From the JF fix round.

## Proposal
Replace the inline guard with the hook under a byte-identical gate on SchedulesPage.filters.test.tsx.

## Related
- web/src/lib/useDebouncedPagingGuard.ts, web/src/schedules/SchedulesPage.tsx
