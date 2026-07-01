---
title: "Job detail page: accessible resizable tasks/detail split"
type: idea
status: open
created: 2026-07-01
priority: low
source: job-detail-page slice (2026-07-01 job-detail-page)
---

# Job detail page: accessible resizable tasks/detail split

## Summary
The Job detail page (`/jobs/:id`, shipped in the 2026-07-01 job-detail-page slice) uses a fixed
55/45 flex split between the tasks column and the detail column. The Holo design calls for a
user-resizable split. Add an accessible drag-resizer between the two columns with a persisted
width.

## Context
Surfaced while building the 2026-07-01 job-detail-page slice: the fixed 55/45 split was shipped
to keep the first detail page focused, with the resizable behavior deferred. The Holo reference
screen (`design_handoff_relay_holo/reference/screens/job-detail.js`) specifies a user-resizable
split, so the fixed ratio is an intentional interim state, not the target.

## Proposal
Add an accessible drag-resizer between the tasks and detail columns:

- Render a separator element with `role="separator"` and `aria-valuenow` / `aria-valuemin` /
  `aria-valuemax` reflecting the current split percentage.
- Support arrow-key resize (Left/Right or Up/Down adjust the split in fixed increments) when the
  separator is focused.
- Support pointer drag to resize.
- Persist the chosen width to `localStorage` so the split survives reloads and navigation.

## Acceptance / Done When
- The split between the tasks and detail columns is resizable by both pointer drag and keyboard
  arrow keys.
- The separator exposes `role="separator"` with accurate `aria-valuenow`/`aria-valuemin`/
  `aria-valuemax`, and is keyboard-focusable.
- The chosen width persists across reloads via `localStorage`.
- Existing Job detail page tests continue to pass.

## Related
- Source slice: 2026-07-01 job-detail-page
  (`docs/superpowers/specs/2026-07-01-job-detail-page-design.md`).
- Design: `design_handoff_relay_holo/reference/screens/job-detail.js` (resizable split).
- Pairs with [[idea-2026-06-26-shared-holo-design-primitives]] - the resizer is a candidate
  shared primitive, best extracted alongside the other Holo primitives so the split control is
  reusable across future detail pages.

## Notes
Frontend-only, small. The resizer is presentational and reusable; consider building it as part of
the shared-holo-design-primitives extraction so schedule detail and other future split layouts
get it for free.
