---
title: The collapsed header nav dismisses on mousedown and no lane can see a touch
type: idea
status: open
created: 2026-09-02
priority: low
source: Phase 4 invariants lens on the 2026-09-01 header-nav slice (lane A of the web SPA batch)
---

# The collapsed header nav dismisses on mousedown and no lane can see a touch

## Summary
The collapsed nav's outside-dismiss route registers a document mousedown listener, copied from
UserMenu. The panel exists specifically for phones, and no lane in the repo emulates touch:
playwright.config.ts has no touch-enabled project and header-nav.spec.ts drives everything with
mouse clicks and keys. If mobile Safari's compatibility mouse-event synthesis does not fire for a
tap on non-interactive content, tap-outside dismissal is gone and the panel stays open over the page;
Escape and Tab are not realistic substitutes on a phone.

## Context
Not reproduced: there is no touch lane to reproduce it in, which is the finding. Switching both
disclosures to pointerdown (a strictly wider net covering mouse, touch and pen) is the likely fix,
but changing it without a lane that can see the difference is an unverifiable fix.

## Proposal
- Add a touch-emulating Playwright project (hasTouch, a mobile device descriptor) with one test that
  opens the collapsed nav and taps dead space.
- Then decide pointerdown for both UserMenu and the nav together, so the two handler sets stay one set.

## Related
- web/src/shell/HoloShell.tsx, web/src/shell/UserMenu.tsx, web/playwright.config.ts
- [[bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports]] (closed; the slice this came from)
