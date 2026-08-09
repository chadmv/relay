---
title: "The sort caret is part of the column header's accessible name, so direction is announced twice"
type: idea
status: open
created: 2026-08-09
priority: low
source: deferred review finding from the shared accessible-table primitive (2026-08-09); the fix was attempted and reverted
---

# The sort caret is part of the column header's accessible name, so direction is announced twice

## Summary
`sortCaret` returns `' ▼'` / `' ▲'` as a plain text node inside the header's `<button>`
(`web/src/components/holo/Table.tsx`), so the accessible name of the button, and of the enclosing
`columnheader`, becomes something like `"LAST SEEN ▼"`. Screen readers announce U+25BC as "black
down-pointing triangle" (or drop it silently at low punctuation verbosity), on top of the `aria-sort`
attribute that already conveys the direction correctly. The direction is announced twice, once as
noise.

## Context
Pre-existing across the four sortable tables and carried unchanged through the 2026-08-09
shared-Table-primitive migration. The fix is now a one-line change in one file rather than four,
which is what makes it worth doing.

**The fix was attempted during that work's review-fix pass and deliberately reverted.** Wrapping the
caret in `<span aria-hidden="true">` broke **three** of the five test files the migration was holding
byte-identical to prove behavioral neutrality: `WorkersTable.test.tsx` ("shows a descending caret on
the active sort column"), `UsersTable.test.tsx` ("descending sort shows a descending caret"), and two
tests in `EnrollmentsTable.test.tsx`. Each asserts the caret glyph as part of a button's accessible
name via `getByRole('button', { name: ... })`. Review had predicted only one file would be affected,
and only via a substring match that would survive.

So this is not a stray glyph - the glyph is currently part of the tested contract in three places.
That is exactly why it needs its own change with its own intent, rather than riding along in a PR
gated on neutrality.

## Proposal
- Wrap the caret in `<span aria-hidden="true">` in `web/src/components/holo/Table.tsx`, leaving
  `aria-sort` as the sole machine-readable carrier of direction.
- Update the assertions in the three test files above to query by the plain column label and assert
  the direction via `aria-sort` on the `columnheader`, which is what a screen reader actually uses.
  This is the substantive half of the work: the assertions are testing the wrong surface today.

## Acceptance / Done When
- A column header's accessible name is the column label alone, with no glyph.
- Sort direction is asserted through `aria-sort`, not through the accessible name, in every test that
  covers it.
- The caret remains visible to sighted users, unchanged.

## Related
- `web/src/components/holo/Table.tsx` (`sortCaret`, the header button)
- `web/src/workers/WorkersTable.test.tsx`, `web/src/admin/users/UsersTable.test.tsx`,
  `web/src/admin/enrollments/EnrollmentsTable.test.tsx`
- Shipped the primitive that makes this a one-place fix:
  [[idea-2026-06-05-shared-accessible-table-primitive]]
- Sibling a11y follow-ups from the same review:
  [[idea-2026-08-09-tasks-table-grid-role-selection]],
  [[idea-2026-08-09-table-accessible-name-consistency]]
