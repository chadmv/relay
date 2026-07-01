---
date: 2026-07-01
topic: worker-detail-holo-redesign
branch: claude/stoic-cannon-15b269
pr: "#95 (worker-detail Holo redesign; primitives + relayout + label UX)"
---

# Session Retro: 2026-07-01 - worker-detail Holo redesign

**TL;DR:** User feedback ("the worker detail page looks nothing like the design handoff")
led to a two-slice redesign: extract a shared Holo primitive set (Slice 1) and relayout the
worker detail page to the hi-fi `HoloWorkerDetail` (Slice 2), stacked into PR #95. A
dev-server review surfaced three things: a job-detail page crash (fixed standalone in PR
#96), a too-heavy label-editing UX (reworked to inline tag entry), and a duplicate-dialog
wart (resolved by the same rework). Green throughout (344 web tests).

## What Was Built

- **Slice 1 - shared Holo primitives** (`web/src/components/holo/`): GlassPanel, Panel,
  Eyebrow, KpiStat, Chip, PillButton, ProgressBar, and StatusDot (lifted out of
  `web/src/workers/`), mapped onto the app's existing cyan `tokens.css`. Adopted in the
  shipped WorkersPage/WorkersGrid with no visual regression. Spark deferred (no consumer).
  Closes `idea-2026-06-26-shared-holo-design-primitives`.
- **Slice 2 - worker detail relayout**: `WorkerDetailPage` rewritten to `HoloWorkerDetail`
  (breadcrumb + identity header with inline action pills, 4-up KPI row, two-column glass
  body). Backend-blocked panels (current tasks, reservations, jobs-today KPI, live slot
  count) render graceful placeholders with **no fabricated data**, each naming its enabler
  backlog item. Telemetry (MetricChart) kept. Behavior preserved (mutations, confirm
  dialogs, revoke-navigates, full-replace labels, admin gating).
- **Label UX rework**: `+ add label` became an inline "type a tag, press Enter" input
  (parses `key=value`, bare word = tag), each chip gets an x to remove, and label editing
  left the Edit dialog (now name + max_slots only). New `WorkerLabels` component.

## Notable / What We Learned

- **Two design layers - I compared against the wrong one first.** The handoff has a lo-fi
  sketch (`reference/screens/*` + `styles.css`: cursive fonts, orange accent, rotated
  "hand-drawn" look) AND the hi-fi Holo (`hifi3-holo-pages.jsx`: cyan/dark/Space Grotesk,
  the picked direction). My first gap analysis compared the app to the lo-fi sketch and
  wrongly concluded telemetry was "extra." Corrected to the hi-fi `HoloWorkerDetail`
  (which does include telemetry). Lesson: confirm which fidelity layer is authoritative
  before analyzing a design gap.
- **Root cause of the app "looking nothing like" the design was a never-built primitive
  system** - every page hand-rolled `bg-white/5 border-border`. Fixing that (Slice 1) is
  the durable fix; the worker page was just the most visible symptom.
- **The job-detail blank-page bug was an integration bug past thorough unit tests.** The API
  returns `"env": null` for tasks that omit it (`json.Marshal(nil map)` is `null`), and
  SpecTab did `Object.entries(null)` -> threw -> blank page. MSW mocks always sent `{}`,
  matching the wrong non-null TS type, so unit tests never caught it. Fixed at both layers
  (backend normalizes to `{}`; frontend guards + honest nullable types) in PR #96, with a
  RED-proven regression test. Reinforces the open web-e2e-harness need.
- **Conductor independent re-verification kept quality honest.** Every code subagent's
  "green" was re-checked (exact file set, no stray artifacts, re-run tests + build) before
  commit; reviewers mutation-tested load-bearing assertions to prove them non-vacuous.

## Deferred / Follow-ups

- **Whole-app hi-fi relayout program** (user-requested): jobs list/detail/new-job, schedules,
  auth, shell each get their own per-page relayout PR onto the primitives. Underway (jobs
  list first).
- `Spark` primitive (build when a page needs it); the holo `StatusDot` is worker-specific
  (jobs keep their own `status.ts` dot) - a shared status primitive may be worth extracting
  during the jobs relayout.
- Existing open items unchanged: ConfirmDialog focus-trap hardening, job-detail drag-resizer,
  the worker activity/reservations backend enablers (which light up the placeholder panels).
