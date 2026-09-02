---
title: useCursorPager hides the stacks but takes an unvalidatable pageSize, which is the exact value both closed footer-range bugs were about
type: idea
status: closed
closed: 2026-09-02
resolution: fixed
priority: medium
created: 2026-08-14
source: review finding at Phase 4 of the 2026-08-14-cursor-pager-hook slice; deliberately not fixed there because it is an API change, not a refactor
---

# useCursorPager hides the stacks but takes an unvalidatable pageSize, which is the exact value both closed footer-range bugs were about

## Summary

`web/src/lib/useCursorPager.ts` shipped with deliberately asymmetric encapsulation:

- **Tight on the stacks.** It returns `canPrev: boolean`, never `stack` or `offsets`. The doc comment
  states why: a consumer holding the array could `pop()` it and desync `offsets` from `cursor` behind
  the hook's back. Value out, mutation only through the hook's own methods. That is CLAUDE.md's "no
  interior pointers across locks" in its frontend form, and it is right.
- **Wide open on the page size.** `next(nextCursor: string | undefined, pageSize: number)` takes a
  bare `number` that the hook cannot check against anything. Every call site passes
  `rows.length` off its own query result, and nothing - not the type system, not a test, not the hook -
  can tell a correct `rows.length` from a stale one, from `50`, or from `999`.

**`pageSize` is the value both of this project's shipped pagination bugs were about**
(`docs/backlog/closed/bug-2026-06-05-jobs-pagination-footer-absolute-range.md` and
`docs/backlog/closed/bug-2026-06-21-schedules-pagination-footer-absolute-range.md`, both fixed by
accumulating the **actual** page size rather than a fixed limit). The hook's own comment says so:
"`pageSize` is the ACTUAL number of rows on the page being left, never the request limit."

And it is measurably unconstrained. The Phase 4 mutation sweep passed a bogus `pageSize` of `999` at
two call sites and the suites stayed green: `WorkersPage` 13/13, `ReservationsTab` 17/17. Those two
now have sibling tests; the shape that makes the mistake possible does not.

## Context

**Why the current shape was chosen, so it does not have to be re-argued.** Passing the query result
was considered at spec time and rejected as the higher-risk starting point for a change whose entire
premise was that nothing changes: the hook would have to name a response shape it does not own,
across seven different response types, and dragging `next_cursor` and `isPlaceholderData` semantics
into a primitive whose job is arithmetic on two stacks was out of scope. That reasoning was correct
**for that slice** and is not a reason to keep the shape forever.

**Why this is worth revisiting now rather than never.** The hook is the single owner of the page walk
for seven surfaces and will be the owner for the eighth. The asymmetry is the interesting part: the
hook already decided that a consumer cannot be trusted with state it could desync, and then accepted
the one scalar whose desynchronization is the documented, shipped-twice defect.

## Proposal

Change `next` to take the page rather than two scalars:

```ts
interface Page {
  next_cursor?: string
  items: unknown[]
}
next: (page: Page | undefined) => void
```

The hook then derives both values it needs - `page?.next_cursor` for the guard and the advance, and
`page.items.length` for the offset accumulation - and a consumer cannot supply one without the other
or supply a size that disagrees with the rows it just rendered. `next(undefined)` stays a no-op, which
also removes the `data?.` chain at each call site.

Points to settle at spec time:

- **What structural type to name.** Every list response in the SPA already has `{items, next_cursor,
  total}`; a minimal structural interface declared by the hook (not imported from a feature module)
  keeps the "imports only `react`" property. Whether `items: unknown[]` is enough, or whether a
  generic is worth it, is the design question.
- **Whether the placeholder-data case changes anything.** `isPlaceholderData` gates the button's
  `disabled`, not the call, so it should stay at the call sites - but confirm that a click cannot
  arrive with a stale `data` under `keepPreviousData`, since that is exactly the case where a
  derived-from-the-page size differs from a hand-passed one.
- **Whether `startOffset` should be derivable at all**, or whether the hook should keep accumulating.
  Accumulation is what makes a partial final page correct; do not lose that property.

**Cost, stated honestly:** seven call sites change, and the gate this time is the seven surfaces'
test files **plus** the five `*.pager.test.tsx` siblings the last slice added. That is a larger frozen
set than the last one, and the same zero-diff rule should apply. The hook's own eight tests will
change, which is correct - the hook's API is the thing being changed.

## Acceptance / Done When

- `next` takes one argument and derives both the cursor and the row count from it.
- No call site passes a row count; a grep for `.length)` at a `pager.next(` call site returns nothing.
- The two closed footer-range bugs' property still holds: a partial final page produces a correct
  absolute range. The three-page three-size hook test survives, adapted to the new signature.
- The seven surface test files and the five `*.pager.test.tsx` files have a zero-line diff. An
  assertion needing adjustment is the finding.
- The hook still imports only `react`.

## Related

- Source: `web/src/lib/useCursorPager.ts`, and the seven call sites
- The bugs this defends against: `docs/backlog/closed/bug-2026-06-05-jobs-pagination-footer-absolute-range.md`,
  `docs/backlog/closed/bug-2026-06-21-schedules-pagination-footer-absolute-range.md`
- Design record: `docs/superpowers/specs/2026-08-14-cursor-pager-hook.md` (Decision 1 - the tradeoff
  as it was taken), `docs/retros/2026-08-14-cursor-pager-hook.md`
- Same file, different question: [[idea-2026-08-14-toggle-sort-generic]]

## Resolution
useCursorPager.next takes a CursorPage (75a8670). next_cursor is REQUIRED, refuting the item's optional sketch: optional fails open (a renamed field becomes a permanent silent no-op), required fails closed at all seven call sites. items is readonly and read only for its length; startOffset keeps accumulating. Zero-line diff to the twelve frozen test files; the wrong-page substitution at WorkersPage is pinned by a new sibling test.
