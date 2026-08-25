---
title: "Reconsider native <dialog> + showModal() for DialogShell, once jsdom or an e2e harness allows it"
type: idea
status: open
created: 2026-08-09
priority: low
source: spec follow-up from the dialog-hardening work (2026-08-09)
---

# Reconsider native `<dialog>` + `showModal()` for DialogShell, once jsdom or an e2e harness allows it

## Summary
`web/src/components/dialog/DialogShell.tsx` hand-rolls the focus trap, the `inert`/`aria-hidden`
background, the stacking order and the scoped Escape. The platform supplies all four for free via
`<dialog>` + `showModal()` and the top layer. That route was rejected during the 2026-08-09
dialog-hardening work **on test-environment evidence, not on design grounds**. This item exists so
the rejection is re-evaluated when the evidence changes, rather than re-litigated from scratch or
treated as permanent.

## Context
The evidence for today's rejection is recorded in `web/src/components/dialog/dialogStack.ts`'s
header comment:

- `web/node_modules/jsdom/lib/jsdom/living/nodes/HTMLDialogElement-impl.js` is, in its entirety,
  `class HTMLDialogElementImpl extends HTMLElementImpl { }`. No `showModal`, no `close`, no `open`
  reflection anywhere in the package. A component calling `showModal()` throws `TypeError` in every
  existing dialog test.
- The only workaround is a hand-rolled polyfill in test setup, at which point the tests exercise the
  polyfill rather than the platform - so the trap, which is the whole point of the route, becomes the
  one thing never verified.
- It also forces a scrim rewrite (`::backdrop` rather than the current `bg-black/60` overlay div),
  which breaks the pixel-neutrality the migration was held to and invalidates
  `TokenRevealDialog.test.tsx`'s `getByRole('dialog').parentElement` backdrop handle.

Browser support was never the blocker (Baseline since March 2022, and this is an internal console).

## Trigger condition
Re-evaluate when **either** holds:

1. jsdom implements `HTMLDialogElement` (`showModal`, `close`, `open` reflection, and ideally top-layer
   focus semantics). Check by re-reading the impl file above and running
   `rg showModal web/node_modules/jsdom`.
2. The repo gains a real-browser test harness ([[idea-2026-06-03-web-e2e-harness]]), which would let
   the trap be verified where the platform actually implements it.

## Proposal
On trigger, re-run the comparison rather than assuming the platform wins:

- Would the migration keep the two-element rendered depth that `TokenRevealDialog.test.tsx` depends
  on, or does `::backdrop` change the backdrop handle?
- Does `showModal()`'s top layer make `dialogStack`'s manual `inert`/`aria-hidden` marking and its
  `z-index`-free stacking redundant, or only partly?
- Does the platform's Escape (`cancel` event) support `dismissOnEscape={false}` for
  `TokenRevealDialog` cleanly, or does suppressing it require the same interception we have now?
- Is the scroll lock still ours to own?

A partial adoption (native element for the trap and stacking, existing stack for the scroll lock) is a
legitimate outcome.

## Acceptance / Done When
- Either the migration lands with the existing dialog test suite intact, or this item is closed with
  a recorded reason why the platform route is still not preferable, updated with whatever the new
  evidence actually showed.

## Related
- `web/src/components/dialog/DialogShell.tsx`, `web/src/components/dialog/dialogStack.ts` (the header
  comment carrying the current evidence)
- `docs/superpowers/specs/2026-08-09-dialog-hardening.md` section 3 (route selection)
- [[idea-2026-06-03-web-e2e-harness]] - one of the two trigger conditions
- Shipped the shell this would replace: [[idea-2026-07-01-confirmdialog-focus-trap-hardening]]

## 2026-08-24: THIS ITEM'S WRITTEN TRIGGER HAS FIRED - and here is exactly what that does and does not unlock

The item defers reconsidering `<dialog>` until the project has a real browser lane, on the grounds that
jsdom cannot evaluate the top layer, `::backdrop`, or the element's focus behaviour. **That lane now
exists**: `web/e2e/` runs Chromium and WebKit against the production-embedded SPA in CI, as of the
2026-08-24 web-e2e-harness slice.

Recording precisely what is now available, so the fired trigger does not become noise:

- **Available**: a real browser, two engines, real `Tab` and arrow key presses, real layout, full-page
  screenshots as artifacts.
- **NOT available, and one of these is load-bearing here**: the harness has **no visual assertions** -
  screenshots are artifacts a human reads, not baselines a test compares - so a `::backdrop` regression
  would not fail a build. And **WebKit is not Safari**; the config says so explicitly. If this item's
  reconsideration turns on Safari-specific dialog behaviour rather than WebKit's, the trigger has
  fired only partly.

So the honest status is: the blocker on *evaluating* `<dialog>` is gone, and the blocker on
*regression-proofing* it is not. Anyone picking this up should decide which of the two they need before
scoping, and should read `web/e2e/README.md` rather than this paragraph, since that file is maintained
with the harness.
