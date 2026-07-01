---
title: Extract shared Holo design primitives (GlassPanel / Eyebrow / Chip / KPI / StatusDot)
type: idea
status: closed
created: 2026-06-26
priority: low
source: ROADMAP web-frontend deep review against design_handoff_relay_holo (2026-06-26)
closed: 2026-07-01
resolution: fixed
---

# Extract shared Holo design primitives (GlassPanel / Eyebrow / Chip / KPI / StatusDot)

## Summary
The theme tokens already match the Holo handoff, but the glass-panel / eyebrow-label / chip /
KPI-stat styling is re-inlined per page (e.g. `bg-white/5 backdrop-blur border-border`). Before
the surge of new pages (Admin, Profile, job detail, schedule detail), extract a small shared set
of presentational primitives so the new surfaces stay visually consistent.

## Context
Surfaced by the 2026-06-26 `/roadmap web-frontend deep` review against `design_handoff_relay_holo/`.
The handoff defines `glassPanel(C)` and a consistent eyebrow/chip/status vocabulary (README
"Design tokens" / "Glass panel" sections); the current SPA applies these inline rather than via
shared components.

## Proposal
Extract a small primitives module: `GlassPanel`, `Eyebrow` (mono uppercase label), `Chip`,
`KPIStat`, and lift the existing `web/src/workers/StatusDot.tsx` to a shared location. Keep it
purely presentational; do not introduce density-mode switching (the handoff says picking one
default is fine).

## Acceptance / Done When
- A shared primitives module exists and the already-shipped pages adopt it without visual regressions.
- New pages (Admin/Profile/detail pages) build on the primitives instead of re-inlining glass styling.

## Related
- Design: `design_handoff_relay_holo/README.md` (Design tokens / Glass panel), `hifi2-holo.jsx`, `hifi2-shared.jsx`
- Pairs with [[idea-2026-06-05-shared-accessible-table-primitive]] (the table-structure counterpart)
- Source: `web/src/theme/tokens.css`, `web/src/workers/StatusDot.tsx`

## Notes
Frontend-only, small. Best done just before or alongside the first new page so the primitives are
validated against real usage.

## Resolution
Extracted the shared primitive set under web/src/components/holo/ (GlassPanel, Panel, Eyebrow,
KpiStat, Chip, PillButton, ProgressBar, and StatusDot lifted out of web/src/workers/), mapped onto
the app's existing cyan token theme, and adopted them in the already-shipped WorkersPage/WorkersGrid
with no visual regression (Slice 1). The worker detail page was then relaid out to the hi-fi
HoloWorkerDetail using them (Slice 2), which validated the primitives against real usage. Spark was
deferred (no consumer yet). Shipped as the worker-detail Holo redesign (2026-07-01
holo-primitives-worker-detail; spec docs/superpowers/specs/2026-07-01-holo-primitives-worker-detail-design.md).
The remaining pages (jobs, schedules, auth, shell) adopt the primitives via a follow-on per-page
hi-fi relayout program, now underway.
