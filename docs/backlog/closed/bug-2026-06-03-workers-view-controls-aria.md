---
title: Workers view toggle lacks aria-pressed; sort headers lack aria-sort
type: bug
status: open
created: 2026-06-03
priority: low
source: web workers slice retro (final code review flagged it as a minor a11y gap)
---

# Workers view toggle lacks aria-pressed; sort headers lack aria-sort

## Summary
On the Workers list page, the Grid/Table view toggle does not set `aria-pressed` on the active button, and the sortable table column headers (Name, Status, Last seen) do not expose `aria-sort`. The active view and current sort direction are conveyed only visually, so screen-reader users cannot perceive them.

## Proposal
- Add `aria-pressed={view === v}` to the two toggle buttons in `WorkersPage.tsx`.
- Add `aria-sort` (`"ascending"` / `"descending"` / `"none"`) to the sortable header cells in `WorkersTable.tsx`, reflecting the active sort.

## Acceptance / Done When
- The active Grid/Table toggle button reports `aria-pressed="true"`.
- The active sort header reports the matching `aria-sort` value; inactive sortable headers report `"none"`.

## Related
- `web/src/workers/WorkersPage.tsx` - the view toggle.
- `web/src/workers/WorkersTable.tsx` - the sortable headers.
- [`bug-2026-06-03-usermenu-aria-attributes`](usermenu-aria-attributes.md) - sibling a11y gap on the UserMenu toggle.
