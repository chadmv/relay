---
title: The layout overflow gate cannot distinguish "fits" from "clipped behind an inner scroller"
type: idea
status: open
created: 2026-08-24
priority: medium
source: demonstrated by mutation during Phase 4 of the 2026-08-24 web-e2e-harness slice
---

# The layout overflow gate cannot distinguish "fits" from "clipped behind an inner scroller"

## Summary

`web/e2e/layout.spec.ts` asserts that the document, `<header>` and `<main>` do not exceed the viewport
width. That is a real check and it caught a real class of bug. But an element that **clips its own
content behind an inner scroll container** satisfies it perfectly, because the overflow moves inside
the element instead of past the viewport edge.

Demonstrated during review, not hypothesised. Replacing the `flex-wrap` on
`web/src/admin/AdminTabs.tsx` with `flex max-w-full ... overflow-x-auto` leaves **all tests passing**
while five admin tabs are clipped behind a scroller that has no `tabIndex`, no `role`, no `aria-label`
and no visual affordance.

## Repro / Symptoms

1. On `web/src/admin/AdminTabs.tsx`, replace `flex-wrap` with `max-w-full` plus `overflow-x-auto`.
2. `make test-e2e` - everything passes.
3. Open the 320px screenshots: the last tabs are gone, with nothing indicating they exist.

One refinement found while measuring, which the fix must respect: `overflow-x-auto` **alone** does not
hide the signal, because `self-start` sizes the element to its content and the overflow still reaches
the document. Only the width-constrained form absorbs it. So the hazard is specific rather than
general to every scroller, and a naive "ban `overflow-x-auto`" rule would be both wrong and noisy.

## Context

This is a property of the gate, and is deliberately **not** a duplicate of
[[bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports]]. That item is one shipped instance with a
nav-specific acceptance criterion. This item is the reason a second instance could ship green - and the
shipped instance is itself evidence that the pattern is one the codebase reaches for: `HoloShell.tsx`
already applies exactly this remedy to the nav.

The project's tables use an inner scroll wrapper **deliberately** - a wide data table on a phone has to
scroll somewhere, and `Table.tsx` gives that wrapper a `role="group"`, an `aria-label` and a tab stop
precisely so the clipped columns stay reachable. So the gate cannot simply forbid inner scrollers. The
distinction it needs to make is between a scroller that is announced and reachable and one that is
neither.

## Proposal

Add an assertion that finds elements whose `scrollWidth` exceeds their `clientWidth` and requires each
to be reachable: a tab stop plus an accessible name, which is the contract `Table.tsx` already
satisfies. Candidate shape:

```
for each element with scrollWidth > clientWidth + tolerance:
  expect it to have a tabindex (or be natively focusable) AND an accessible name
```

That passes today's tables unchanged, fails the mutated `AdminTabs`, and fails the shipped header nav -
which is the correct outcome, since the header nav is a filed bug.

Decide the tolerance deliberately: sub-pixel rounding produces `scrollWidth` one greater than
`clientWidth` on elements that visually fit.

## Acceptance / Done When

- The `AdminTabs` mutation above turns the suite RED.
- Every existing table surface still passes with no change to `web/src`.
- The tolerance is justified in a comment against a measured value, not guessed.

## Related

- `web/e2e/layout.spec.ts` - the gate
- `web/src/components/holo/Table.tsx` - the wrapper that already satisfies the proposed contract
- `web/src/shell/HoloShell.tsx` - the former shipped instance, fixed by the collapsed nav
- [[bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports]] - that instance, closed 2026-09-02
- `web/e2e/nav.ts` - the header-only reachability predicate built on `toBeInViewport()`
- [[bug-2026-09-02-taskdag-scroller-has-no-tab-stop-or-name]] - the first instance outside the header

## Notes

2026-09-02: the known instance is fixed (the header nav collapses into a disclosure below `md`,
PR #171), and the instrument this item asks for now partly exists. `web/e2e/nav.ts` carries a
reachability predicate that uses Playwright's `toBeInViewport()`, which does clip against
intermediate scroll containers, plus a `scrollWidth <= clientWidth` assertion on the nav panel at
768 and 1280. It was measured RED against the pre-fix shell on all thirteen shell surfaces at 320 and
375. Two limits keep this item open: the predicate runs only over the header's four destinations, not
over every scroll container the page renders, and the plan-supplied first version used `isVisible()`,
which cannot see clipping at all and was green against the bug (recorded in `web/CLAUDE.md`). The
general predicate is still owed, and the TaskDag scroller on `/jobs/:id` is its first instance outside
the header.
