---
title: At narrow viewports the header nav is clipped with no affordance, so Schedules and Admin look like they do not exist
type: bug
status: closed
closed: 2026-09-02
resolution: fixed
created: 2026-08-24
priority: medium
source: first visual confirmation of the SPA at 320/375, from the browser harness's first CI run (2026-08-24 web-e2e-harness slice)
---

# At narrow viewports the header nav is clipped with no affordance

## Summary

At 320px and 375px the app header renders `RELAY | Jobs | Workers | S…` and then the user chip. The
remaining nav items are cut off at the viewport edge. The header is a horizontal scroll container, so
they are technically reachable - but nothing on screen indicates that. A user on a phone sees an app
with two pages.

This is **not** a document-overflow bug. `documentElement.scrollWidth <= clientWidth` holds, which is
what the 2026-08-13 narrow-viewport slice asserted and shipped. The assertion was correct. What it
could not express is what the result looks like.

## Repro / Symptoms

1. Run the browser harness: `make test-e2e` (see `web/e2e/README.md`).
2. Open any `*-320.png` or `*-375.png` under `web/test-results/`, or download the
   `playwright-<run id>` artifact from a `web-ci` run.
3. Observe the header on any authenticated surface. At 375: `RELAY  Jobs  Workers  S…`. At 320:
   `RELAY  Jobs  Worker` - "Workers" itself is cut mid-word.

Confirmed on Chromium at both widths across all authenticated surfaces. `/auth` is unaffected - it
renders no app shell, which is why it is the harness's control surface.

## Context

The 2026-08-13 narrow-viewport-overflow slice fixed app-wide horizontal overflow and shipped **two
design decisions with no hi-fi reference**, because `design_handoff_relay_holo` is silent on narrow
viewports. `docs/backlog/idea-2026-06-03-web-e2e-harness.md` recorded that neither decision had ever
been seen rendered by anyone, and that the header nav's horizontal scroll was "proven only as
numbers".

This item is the answer to that. The browser harness's **first CI run** produced the screenshots, and
the header is the thing they show. Three prior sessions of `getBoundingClientRect` and `scrollWidth`
measurement could not have found it, because by every number the page is correct.

## Proposal

The design question is genuinely open and should not be answered here by default. The options, in
rough order of cost:

- **A scroll affordance** - a gradient mask or a chevron at the clipped edge. Cheapest; keeps the
  current structure; still leaves the items hard to reach on a touch device.
- **A collapsed menu** below some breakpoint - the conventional answer, and the one a user expects.
  Largest change, and the hi-fi has no reference for it.
- **Prioritised truncation** - keep the current page plus an overflow control, so the visible item is
  always the one in use.

Whichever is chosen, the acceptance criterion should be visual as well as numeric, now that it can be:
a screenshot at 320 and 375 in which every primary destination is either visible or reachable through
a control that is visible.

## Acceptance / Done When

- At 320 and 375, every top-level destination is reachable through something a user can see.
- `layout.spec.ts` keeps its existing no-overflow assertions and gains one that fails if a primary nav
  destination is neither visible nor behind a visible control.
- The screenshots for the affected surfaces are reviewed by a human as part of the change, not just
  the numbers.

## Related

- `web/src/app/` - the header/nav shell
- `web/e2e/layout.spec.ts`, `web/e2e/surfaces.ts` - where the screenshots and the assertions live
- [[idea-2026-06-03-web-e2e-harness]] - the item whose whole argument was that this class of defect was
  invisible; this is the first instance it surfaced
- [[bug-2026-08-12-web-narrow-viewport-horizontal-overflow]] - the slice that fixed the overflow and
  shipped the two design decisions nobody had seen rendered (closed)

## Resolution
Collapsed the four destinations into a disclosure below the md breakpoint: one DOM copy of the links always mounted inside the Main navigation landmark, a Menu toggle with aria-expanded and aria-controls in both states, a header-anchored full-bleed panel, and UserMenu's handler set for Escape, outside mousedown, focusout and modifier clicks. The Playwright reachability predicate uses toBeInViewport (isVisible cannot see clipping inside a scroller and was green against this very bug) plus a no-scroll assertion on the panel at 768 and 1280; measured RED on all thirteen shell surfaces against the original shell at 320 and 375, green after. An emitted-CSS A/B control attributes all sixteen breakpoint utilities to HoloShell.tsx alone. Touch dismissal rides on mousedown with no touch lane; filed separately.
