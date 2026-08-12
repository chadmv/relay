---
title: The app has no documented z-index layering scale
type: idea
status: open
created: 2026-08-12
priority: low
source: filed from the 2026-08-12 profile-dropdown stacking fix, which hit the trap this item describes before finding the real cause
---

# The app has no documented z-index layering scale

## Summary
Three overlay mechanisms now coexist in `web/src` and each picked its z-index
independently at its own call site, with nothing recording the scale or the rules that
govern it. The next overlay - a toast, a popover, a command palette, a tooltip - has no
document to consult and will pick a number by looking at whichever neighbour it happens
to read first.

## Context
The values in the tree today:

| surface | value | where |
|---|---|---|
| dialog scrim | `z-50` | `web/src/components/dialog/DialogShell.tsx` (`SCRIM`), on a layer portalled to `<body>` |
| app header | `relative z-10` | `web/src/shell/HoloShell.tsx` |
| page content | `relative z-0` | `web/src/shell/HoloShell.tsx` (`<main>`) |
| profile dropdown | `z-50` | `web/src/shell/UserMenu.tsx`, scoped **inside** the header |

Two `z-50`s that mean entirely different things, because one is in the root stacking
context and the other is confined to the header's.

The 2026-08-12 fix is the evidence this is worth writing down: the obvious move - put a
z-index on the dropdown - **does not work**, because the header's own `backdrop-blur-[10px]`
makes it a stacking context, which confines any z-index declared inside it. Measured in
Chrome by hit-testing 275 points across the open dropdown: 220/275 still occluded with the
dropdown's `z-50` alone, 0/275 once the order was declared between `<header>` and `<main>`.
That is a non-obvious rule about *this* app's specific DOM, discovered by measurement, and
it currently survives only as a comment in one file that a person writing a toast would
have no reason to open.

## Proposal
Small and documentation-shaped, not a refactor:

- Record the scale in one place - a `layers.ts` constant set, or a comment block in
  `web/src/theme/tokens.css` next to `--color-popover` (added by the same fix for the same
  reason: overlays need an opaque surface because `GlassPanel` sets no `background-color`
  at all).
- State the two rules the values depend on, both of which are properties of this app's
  DOM rather than general CSS folklore:
  1. An ancestor with `backdrop-filter` (every `GlassPanel`, and the header) is a
     stacking context, so a z-index inside it cannot compete with anything outside it.
  2. `<main>` carries `relative z-0` specifically so page-level z-indices are contained
     and can never climb over the header. A new page-level overlay should stay inside
     that context rather than raising itself past it.
- Cross-link from `GlassPanel.tsx` and `DialogShell.tsx`, the two places a new overlay
  author is most likely to start reading.

Deliberately NOT proposed: renumbering the existing values, or introducing a z-index
utility layer. Nothing is broken right now; this is about the next author, not this code.

## Acceptance / Done When
- One documented location lists every z-index the app uses and what each is for.
- Both rules above are stated, with the measured evidence (or a pointer to
  `HoloShell.tsx`, which carries the numbers).
- `GlassPanel.tsx` and `DialogShell.tsx` point at it.
- Adding a new overlay requires reading exactly one file to choose a value correctly.

## Related
- `web/src/shell/HoloShell.tsx`, `web/src/shell/UserMenu.tsx`,
  `web/src/components/dialog/DialogShell.tsx`, `web/src/components/holo/GlassPanel.tsx`
- [[idea-2026-08-09-body-level-portal-inert-marking]] - the same "a new overlay appears"
  problem from the inert/aria-hidden side rather than the paint-order side. A new overlay
  has to get both right, and neither is currently written down anywhere it would be found.
  Worth doing together.
- [[idea-2026-08-09-native-dialog-element-reconsideration]] - **check this before starting.**
  If native `<dialog>`/`showModal()` is ever adopted, dialogs move to the browser's top
  layer and leave the z-index scale entirely, which changes what this document should say
  about the `z-50` scrim. That item is currently blocked on jsdom support or the Playwright
  harness, so the scale is real today either way - but do not write it as though `z-50` for
  dialogs is permanent.

## Notes
- The dropdown at `z-50` is not a bug and does not conflict with the dialog scrim's `z-50`:
  it is confined to the header's stacking context, and dialogs portal to `<body>` outside
  it. That the two identical numbers are unrelated is exactly the sort of thing a reader
  cannot infer from the values alone, and is the strongest argument for writing it down.
