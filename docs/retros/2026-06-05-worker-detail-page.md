# Session Retro: 2026-06-05 - Worker Detail Page

## What Was Built

The read-only worker detail page (`/workers/:id`), the slice deferred from the Workers list page. It shows a single worker's identity, hardware, live utilization telemetry, labels, and (for admins) its source workspaces, reachable by clicking any worker in the list grid or table.

The headline feature is live telemetry: hand-rolled SVG area charts for CPU, memory, and (when the worker has a GPU) GPU utilization and GPU memory, polled on the 10s server sample cadence. This is the charting capability the list page explicitly deferred as a perf footgun on an unbounded list.

Delivered through the full superpowers flow: brainstorming -> spec -> writing-plans -> subagent-driven development (11 tasks, fresh implementer + review per task) -> final whole-branch review (SHIP). 39 new front-end tests (89 total, all green) and a clean production build (`tsc -b && vite build`).

## Key Decisions

- **Read-only slice; mutations deferred.** Every web slice so far has been read-only, and this page is where the wireframe introduced the first writes. We kept it read-only and filed the admin mutations (rename, labels, max_slots, drain/enable, revoke token, evict) as a follow-up backlog item.
- **Hand-rolled SVG charts, no charting library.** A pure `chartPath` geometry helper (empty/single-point/clamp/non-positive-max handled, unit-tested in isolation) feeds a thin `MetricChart` component that colors via `currentColor` + a Tailwind text class. Fits the project's minimalist ethos; zero new dependencies for one page.
- **Three focused hooks at matched cadences.** `useWorker` (3s, like the list), `useWorkerMetrics` (10s, aligned to the server sample interval), `useWorkerWorkspaces` (15s). Polling faster than the source changes only re-fetches identical data.
- **Admin-gating by conditional mount, not an `enabled` flag.** The workspaces panel (whose endpoint is admin-only) is rendered only when `user.is_admin`, so a non-admin page never mounts the panel and never fires the admin-only request. Tested both ways (`wsCount === 0` for non-admins).
- **Stat cards show capacity only.** No per-worker running-task count exists over HTTP, so "max slots" is capacity, not "running/max"; the wireframe's current-tasks and jobs-today panels were dropped (no backing endpoint) and filed to the backlog.
- **GPU charts gate on hardware-stable `gpu_count`, not the per-sample `gpu` flag.** The final review flagged this as a deviation from the spec's literal wording; the chosen behavior is better (avoids charts flickering away on a transient `nvidia-smi` miss), so we kept it and annotated both the code and the spec.

## Problems Encountered

- **`npm run build` overwrote a committed placeholder.** `web/dist/index.html` is a deliberately-tracked placeholder ("run make web-build"); the gitignore force-includes only that one file while ignoring the rest of `dist/`. The production build rewrote it to reference hashed assets. Restored the placeholder with `git checkout` so no build artifact was committed and the tree stayed clean.
- **Retro range over-captured.** The prior retro (`2026-06-04-workers-stats-endpoint`) ended at a SHA that several other sessions' work (auto-enroll, UserMenu aria, dev.ps1) landed after without chaining their own retros to it. Scoped this retro to the actual session-start commit (`264350b`) rather than the prior retro's ending SHA, so it covers only the worker-detail work.

## Known Limitations

- GPU charts are shown whenever `gpu_count > 0`; the per-sample `gpu` boolean is not consulted (intentional, to avoid flicker), so a worker whose GPU sampling is failing still shows empty GPU charts rather than hiding them.
- `useWorkerMetrics` still fires once on a 404 worker (the result is unused on the error path). Harmless dead-end request; left as-is to avoid changing the hook signature.

## Open Questions

The deferred wireframe panels are filed as backlog features (created earlier this session):

- See [`feature-2026-06-05-worker-detail-admin-mutations`](../backlog/feature-2026-06-05-worker-detail-admin-mutations.md) - Worker detail page admin mutation actions
- See [`feature-2026-06-05-worker-detail-activity-panel`](../backlog/feature-2026-06-05-worker-detail-activity-panel.md) - Running-tasks and activity stats panel (needs a per-worker tasks endpoint)
- See [`feature-2026-06-05-worker-detail-reservations-panel`](../backlog/feature-2026-06-05-worker-detail-reservations-panel.md) - Reservations panel (needs a per-worker reservation lookup)

## Improvement Goals

- The contract-verification habit held again: TS types were checked field-for-field against the Go structs (`workerResponse`, `metricSampleResponse`/`workerMetricsResponse`, `workspaceJSON`) in Task 1, and no contract bug shipped. Keep doing this per slice.
- When a spec lists a literal gating condition (here, "and samples report `gpu: true`"), decide at design time whether the stable or the transient signal is wanted, so the implementation does not have to deviate and annotate after review.

## Files Most Touched

- `web/src/workers/WorkerDetailPage.tsx` (+168) - page composition: header, stat cards, telemetry, labels, admin-gated workspaces, all states.
- `web/src/workers/WorkerDetailPage.test.tsx` (+143) - identity/charts/gpu-gating/empty/404/error/admin-gating coverage.
- `web/src/workers/MetricChart.tsx` (+42) / `chart.ts` (+35) - SVG chart component and its pure geometry helper.
- `web/src/workers/WorkspacesPanel.tsx` (+40) - admin-only read-only workspaces table.
- `web/src/workers/api.ts` (+41) - `getWorker`/`getWorkerMetrics`/`listWorkerWorkspaces` + `MetricSample`/`WorkerMetrics`/`Workspace` types, verified against the Go structs.
- `web/src/workers/useWorker.ts` / `useWorkerMetrics.ts` / `useWorkerWorkspaces.ts` - the three polling hooks.
- `web/src/workers/WorkersGrid.tsx` / `WorkersTable.tsx` - cards/rows are now `Link`s to the detail page.
- `web/src/app/router.tsx` (+2) - mounted the `/workers/:id` route.
- `web/src/workers/liveness.ts` (+6) - added `formatGB` byte formatter.
- `docs/superpowers/specs/2026-06-05-worker-detail-page-design.md` / `plans/2026-06-05-worker-detail-page.md` - the spec and the 11-task implementation plan executed this session.

## Commit Range

264350bd562270ae9d825f9ac521182b16d37e64..e779f86
