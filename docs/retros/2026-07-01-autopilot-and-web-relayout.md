---
date: 2026-07-01
topic: autopilot-and-web-relayout
branch: claude/stoic-cannon-15b269
range: f50b34e..c40877a
---

# Session Retro: 2026-07-01 - autopilot batch + whole-app Holo relayout

**TL;DR:** One long session with two arcs: an unattended `/autopilot 4` batch that shipped
four web features (#91-#94), then user feedback ("the worker detail page looks nothing like the
design") that grew into a full whole-app hi-fi Holo relayout (#95, #97-#102) plus a live crash
fix (#96). Twelve PRs merged to main; every SPA surface is now on the shared primitive
framework. Per-topic detail lives in the six topic retros written along the way; this is the
session-level synthesis and the process lessons.

## What Was Built

Twelve PRs (see per-topic retros for detail):

- **Autopilot batch (#91-#94)** - one `/autopilot 4` run, full spec->plan->implement->verify->
  merge per item: worker-detail admin mutations (#91), job detail page + row-click nav (#92),
  job cancel/force-cancel (#93), New Job submit form (#94). Retros:
  `2026-07-01-worker-detail-mutations`, `-job-detail-page`, `-job-cancel-actions`,
  `-job-submit-form`.
- **Job-detail crash fix (#96)** - found during dev-server review: the job-detail page blanked
  because the API returns `"env": null` for tasks that omit it and `SpecTab` did
  `Object.entries(null)`. Fixed both layers (backend normalizes to `{}`; frontend guards +
  honest nullable types), RED-proven.
- **Whole-app Holo relayout (#95, #97-#102)** - extract the shared primitive set + relayout the
  worker detail page (#95), then jobs list (#97), schedules (#98), new-job (#99), auth (#100),
  shell (#101), and job detail (#102). Retros: `2026-07-01-worker-detail-holo-redesign`,
  `2026-07-01-whole-app-holo-relayout`.

## Key Decisions

- **Primitives-first, then per-page.** Extracting the vocabulary once made every later page a
  thin restyle. Confirmed the right order for a design-system migration.
- **Pragmatic restyle, not fidelity theater.** Where the hi-fi mocks showed data the backend
  lacks, the relayouts omit it and file the real enrichers, rather than faking values or
  shipping empty shells. Mock-fiction fields (container image/runtime/cluster) were identified
  as not part of relay's model and not filed.
- **"Don't force the primitive."** Each page used only the primitives that fit; jobs/schedules
  kept their own status dots, native `<select>` stayed for a11y, per-status progress fills
  stayed inline. Avoided a premature over-general API.
- **Cadence for the migration.** After the user validated the first list restyle, auto-merge the
  straightforward restyles and pause only on the one judgment page (job detail). Kept velocity
  without ceding the page with real design forks.
- **One PR per page, off main, reused worktree branch.** Each page shipped as its own reviewable
  PR; the conductor reused the single worktree branch, resetting to main after each squash merge.

## Problems Encountered

- **Subagent role-confusion + concurrent writers (autopilot iteration 1).** A frontend-engineer
  subagent got confused about its role and a second implementer raced it on the same files
  (mid-run reverts, stray temp files). The conductor's independent re-verification of the tree +
  re-running the green gate caught it. Promoted to [[feedback_verify_tree_not_subagent_claims]].
- **Compared against the wrong design layer first.** The handoff has a lo-fi sketch and the hi-fi
  Holo; my first gap analysis used the sketch and mis-concluded telemetry was "extra." Promoted
  to [[reference_holo_handoff_two_layers]].
- **Integration bug past thorough unit tests.** The null-env crash never showed in MSW-mocked
  unit tests (mocks sent `{}`, matching the wrong type). Reinforces the open web-e2e-harness item.
- **Vacuous test caught in review.** A planned TanStack-invalidation test seeded the query with
  `fetchQuery` (no active observer), so it would have passed regardless. Promoted to
  [[reference_tanstack_invalidation_test_needs_active_observer]].

## Known Limitations

- The relaid-out **job detail page is intentionally less dense than the mock** - elapsed/ETA,
  per-task timing, and live-log tailing are omitted pending backend work, already filed:
  [[feature-2026-07-01-job-detail-timing-enrichment]], [[feature-2026-07-01-per-task-timing]],
  and the tracked SSE log publisher.

## Improvement Goals

The prior retro (`2026-06-25-sendinventory-wedge-escape`) had no Improvement Goals section, so
nothing carried in. New goals this session, most already promoted to durable memory:

- **Independently re-verify the working tree and re-run the green gate after every code
  subagent** - never merge on a subagent's "all green" claim, especially under concurrency.
  Already promoted: [[feedback_verify_tree_not_subagent_claims]].
- **Confirm which design-fidelity layer is authoritative before analyzing a gap.** Already
  promoted: [[reference_holo_handoff_two_layers]].
- **Test invalidation/refetch with a real active observer, not a `fetchQuery` seed.** Already
  promoted: [[reference_tanstack_invalidation_test_needs_active_observer]].
- **For a large UI/design-system migration: primitives-first, one PR per page, "don't force the
  primitive," omit-unbacked-not-fake, and auto-merge-restyles-while-pausing-on-judgment-pages.**
  New this session; not yet promoted. Candidate for a durable home if a second migration recurs.

## Files Most Touched

- `web/src/components/holo/*` - the new shared primitive set (GlassPanel, Panel, Eyebrow, KpiStat,
  Chip, PillButton, ProgressBar, StatusDot); the foundation every page now consumes.
- `web/src/workers/WorkerDetailPage.tsx` - relaid out to hi-fi HoloWorkerDetail (the initiating page).
- `web/src/jobs/JobDetailPage.tsx` - built in the autopilot batch (#92), then relaid out (#102).
- `web/src/workers/WorkerActions.tsx` / `useWorkerActions.ts` / `WorkerLabels.tsx` - worker
  mutations (#91) + the inline label UX rework.
- `web/src/jobs/JobActions.tsx` / `useJobActions.ts` - job cancel/force-cancel (#93).
- `web/src/jobs/NewJobPage.tsx` - New Job submit form (#94), later restyled (#99).
- `web/src/jobs/{JobsPage,JobsTable}.tsx`, `web/src/schedules/{SchedulesPage,SchedulesTable}.tsx` -
  list-page relayouts (#97, #98).
- `web/src/auth/{LoginScreen,RegisterScreen}.tsx`, `web/src/shell/{HoloShell,UserMenu}.tsx` -
  auth + shell relayouts (#100, #101).
- `web/src/jobs/SpecTab.tsx` + `internal/api/{server,jobs}.go` - the null-env crash fix (#96).
- `web/src/jobs/{TaskDag,dagLayout,TasksTable,LogTab}.tsx` - job-detail DAG/tasks/log (#92, #102).
