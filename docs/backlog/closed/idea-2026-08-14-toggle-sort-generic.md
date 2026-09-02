---
title: toggleSort is duplicated in five list surfaces, each typed over its own sort union
type: idea
status: closed
closed: 2026-09-02
resolution: fixed
priority: low
created: 2026-08-14
source: deliberately excluded from the 2026-08-14-cursor-pager-hook slice (spec Decision 3); the surviving half of the extraction debt that slice discharged
---

# toggleSort is duplicated in five list surfaces, each typed over its own sort union

## Summary

Five surfaces carry a byte-identical five-line pure function that implements one rule - clicking the
active column flips its direction, clicking another column selects it ascending:

- `web/src/workers/WorkersPage.tsx:22`
- `web/src/admin/users/UsersTab.tsx:17`
- `web/src/admin/enrollments/EnrollmentsTab.tsx:16`
- `web/src/admin/reservations/ReservationsTab.tsx:21`
- `web/src/admin/invites/InvitesTab.tsx:26`

```tsx
function toggleSort(field: InviteSortField, current: InviteSort): InviteSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as InviteSort
  }
  return field
}
```

The house rule is extract before the third consumer. This is the fifth.

## Context

**This is the surviving half of a debt that was half discharged on 2026-08-14.** The cursor-pager
extraction (`idea-2026-08-13-cursor-pager-hook`, closed) took the stateful half - seven copies of the
cursor-stack page walk - into `web/src/lib/useCursorPager.ts`, and deliberately left `toggleSort`
behind. The reason is recorded in that slice's spec as Decision 3 and is still the honest statement of
the difficulty:

> Its five copies are typed over five per-module unions (`WorkerSort`, `UserSort`, `EnrollmentSort`,
> `ReservationSort`, `InviteSort`) and a shared version needs a generic plus a cast at every call
> site. Doing it there would put a type-level design question inside the change whose gate is that
> nothing changes.

`web/src/admin/invites/InvitesTab.tsx:14-25` currently carries the accounting - it is the FIFTH copy,
and the comment says so after this slice corrected an inherited off-by-one that had it as FOURTH.
**A comment is not a queue**, which is why this exists as an item.

**The pure-function half is the easy half in every respect except one.** No state, no hooks, no
render behaviour, and its callers are all one-liners inside a `pickSort` handler. The only real
question is the type.

## Proposal

One helper in `web/src/lib/` (beside `useCursorPager.ts`, `pageRange.ts`, `useNow.ts`), generic over
the module's own sort union:

```ts
export function toggleSort<S extends string>(field: string, current: S): S
```

Points to settle at spec time:

- **Where the cast lives.** The current bodies each carry one `as InviteSort`-style cast because
  template-literal arithmetic on a union is not inferable. A generic version either keeps one cast
  inside the helper (five casts become one, at the cost of the helper asserting for its callers) or
  pushes a cast to each of the five call sites (no better than today). The first is probably right and
  should be argued rather than assumed.
- **Whether the field parameter should be typed.** Each module has a `*SortField` union that pairs
  with its `*Sort`; a two-parameter generic can relate them, at some cost in signature legibility.
- **Whether five is the real count.** `SchedulesPage` and `JobsPage` use a `<select>` rather than
  sortable headers and have no copy. Verify at spec time - the count has moved once already.

## Acceptance / Done When

- One `toggleSort` exists; the five copies are gone and a grep for `function toggleSort` in `web/src`
  returns exactly one hit, in `web/src/lib/`.
- The helper has direct tests: flip an active ascending column to descending, flip descending back to
  ascending, and select a different column ascending from either direction.
- **Zero-line diff to the five surfaces' existing test files** and to the `*.pager.test.tsx` siblings.
  This is a behaviour-preserving refactor; an assertion needing adjustment is the finding.
- `web/src/admin/invites/InvitesTab.tsx:14-25` loses its accounting comment entirely, since both halves
  of the debt it tracks are then discharged.
- `tsc -b` clean with no `@ts-expect-error` and no `any` at any call site.

## Related

- Source: the five files above
- The half that was done, and the precedent for the gate: [[idea-2026-08-14-cursor-pager-next-takes-the-page]]
  points at the same hook; the closed parent is `idea-2026-08-13-cursor-pager-hook`
- Design record: `docs/superpowers/specs/2026-08-14-cursor-pager-hook.md` (Decision 3),
  `docs/retros/2026-08-14-cursor-pager-hook.md`
- Same rule, different primitive: [[idea-2026-08-12-detail-page-state-triad-primitive]]

## Resolution
One toggleSort in web/src/lib, generic over the module's sort union (a3518ba), with field constrained to the union's base names via SortFieldOf<S> so a typo'd column is a compile error again (5d89c8e, pinned by a ts-expect-error line the tsc lane compiles). A template-literal generic relating field and sort was refuted: WorkersPage's field union is SortField in WorkersTable.tsx and is narrower than WorkerSort. Five copies and their comment blocks deleted; zero-line diff to the frozen files.
