---
date: 2026-07-01
topic: whole-app-holo-relayout
branch: claude/stoic-cannon-15b269
pr: "#95, #97-#102 (whole-app hi-fi Holo relayout program)"
---

# Session Retro: 2026-07-01 - whole-app hi-fi Holo relayout

**TL;DR:** User feedback ("the worker detail page looks nothing like the design handoff")
grew into a full program: extract the shared Holo primitive system, then relayout every page
onto it to match the picked hi-fi design. Shipped across seven PRs (plus a separate crash
fix). Every surface in the app is now on the Holo framework, consistent end to end. Approach
throughout: pragmatic restyle that preserves all behavior and omits unbacked mock data (filing
backend enrichers) rather than faking it.

## What shipped

- **#95** - shared Holo primitives (`web/src/components/holo/`: GlassPanel, Panel, Eyebrow,
  KpiStat, Chip, PillButton, ProgressBar, StatusDot) + worker-detail relayout + inline label UX.
- **#96** - job-detail null-env crash fix (separate bug found during dev-server review).
- **#97** - jobs list relayout.
- **#98** - schedules list relayout.
- **#99** - new-job page relayout.
- **#100** - auth screens (login + register) relayout.
- **#101** - app shell (nav/layout + UserMenu) relayout.
- **#102** - job detail page relayout (the judgment page; user chose fixed split + pragmatic omit).

## What Went Well

- **Primitives-first paid off.** Extracting the vocabulary once (#95) meant every later page was
  a thin, low-risk restyle onto the same components; the two list pages and the auth/shell/new-job
  pages each touched only 1-4 files with no behavior change.
- **"Don't force the primitive" discipline.** Each page used only the primitives that fit and
  kept inline what didn't (jobs/schedules keep their own status dots since the shared StatusDot is
  worker-vocabulary; native `<select>` kept for a11y; per-status progress fills kept). This avoided
  a premature over-general API.
- **Behavior preservation held under review.** Every restyle was reviewed against a hard-preserve
  list; the pagination state machines, mutation wiring, and the SpecTab null-safety all came through
  byte-for-byte, and reviewers mutation-tested the load-bearing assertions.
- **Honest about backend gaps.** The hi-fi mocks show data the API doesn't provide (elapsed/ETA,
  per-task timing, live log, container image/runtime/cluster). Rather than fake it or ship empty
  shells, the relayouts omit it and the real enrichers were filed as backlog; mock-fiction fields
  (image/runtime/cluster) were identified as not part of relay's model and not filed.

## Notable / Lessons

- **Two design layers - I compared to the wrong one first.** The handoff has a lo-fi sketch
  (`reference/`, cursive + orange) and the authoritative hi-fi Holo (`hifi3-holo-pages.jsx`,
  cyan/dark). My first gap analysis used the sketch and mis-concluded telemetry was "extra."
  Captured as memory [[reference_holo_handoff_two_layers]].
- **Integration bug past unit tests.** The job-detail blank-page crash (Object.entries on a `null`
  env the API really returns but MSW mocks never sent) reinforces the open web-e2e-harness item.
- **Cadence worked.** After the user validated the first list restyle, auto-merging the
  straightforward restyles and pausing only on the job-detail judgment page kept velocity without
  losing control of the one page with real design forks.

## Filed follow-ups / remaining

- Backend enrichers to fill the job-detail density: [[feature-2026-07-01-job-detail-timing-enrichment]],
  [[feature-2026-07-01-per-task-timing]], and the already-tracked SSE log publishing.
- UI polish already filed: [[idea-2026-07-01-job-detail-resizable-split]] (accessible drag-resizer),
  [[idea-2026-07-01-confirmdialog-focus-trap-hardening]], and a generic tone-based status-dot
  primitive (worth filing when a third consumer is actually built).
- Not relayouts but still placeholders: the **Admin** and **Profile** pages are unbuilt features
  (routed to a placeholder), not styling gaps - they get built fresh on the primitives.
