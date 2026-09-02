---
title: The Jobs Lanes row always scrolls, even at 1280, where the hi-fi uses fluid columns
type: idea
status: open
created: 2026-09-02
priority: low
source: Phase 4 correctness lens on the 2026-09-01 jobs-lanes slice (lane F)
---

# The Jobs Lanes row always scrolls, even at 1280

## Summary
Five 280px lanes plus gaps are about 1450px, wider than the content box at 1280, so the Cancelled lane
is clipped mid-word at every width the harness measures and the row scrolls by construction. The
hi-fi's HoloLanes does not scroll at desktop: it is a four-column fluid grid with each lane body
scrolling vertically. The slice's stated objection to a breakpoint (stacking nests a vertical scroller
in a vertical scroller) does not reach a fluid grid.

## Proposal
A five-column fluid grid above the lg breakpoint keeping the 280px scroller below it, with the surface
screenshot at 1280 reviewed by a human.

## Related
- web/src/jobs/JobsLanes.tsx, design_handoff_relay_holo/hifi3-holo-pages.jsx (HoloLanes)
- [[idea-2026-06-05-jobs-lanes-swimlanes-view]] (closed)
