# Worker Detail Page - Design

Date: 2026-06-05
Status: Approved (pending implementation plan)

## Overview

The worker detail page (`/workers/:id`) is the slice deferred from the Workers
list page (see `docs/retros/2026-06-03-web-workers.md`). It gives a single worker
a dedicated view: identity, hardware, live utilization telemetry, labels, and -
for admins - its source workspaces.

This slice is **read-only**. It introduces no mutations; the frontend's first
write actions are deferred to a later slice
(`docs/backlog/feature-2026-06-05-worker-detail-admin-mutations.md`).

The headline feature is **live telemetry charts** (CPU / memory / GPU over a
rolling ~30-minute window), which the list page explicitly deferred as a perf
footgun on an unbounded list but which belongs on a single-worker page.

## Goals

- A dedicated, auto-refreshing detail view reachable by clicking a worker in the
  list (grid card or table row).
- Render the worker's identity, hardware spec, and current status.
- Visualize utilization telemetry from `GET /v1/workers/{id}/metrics` as
  hand-rolled SVG area charts (no new charting dependency).
- Show the worker's labels (read-only).
- Show the worker's source workspaces (read-only), admin-only.

## Non-Goals (deferred, backlog filed)

- All admin mutations: rename, edit labels, set `max_slots`, drain (disable) /
  enable, revoke token, evict workspace -
  `feature-2026-06-05-worker-detail-admin-mutations.md`.
- "Current tasks" panel and "jobs today" activity stats (no per-worker task
  endpoint exists) - `feature-2026-06-05-worker-detail-activity-panel.md`.
- Per-worker reservations panel (reservations are not queryable by worker) -
  `feature-2026-06-05-worker-detail-reservations-panel.md`.

## Backend - already available, no changes

| Endpoint | Auth | Use |
|----------|------|-----|
| `GET /v1/workers/{id}` | any authed | identity, hardware, status, `last_seen_at`, `last_sample_at`, `disabled_at` |
| `GET /v1/workers/{id}/metrics` | any authed | telemetry samples + `sample_interval_seconds` |
| `GET /v1/workers/{id}/workspaces` | **admin** | source workspaces (read-only this slice) |

No backend changes are required for this slice.

### Telemetry data shape

The server holds a per-worker ring buffer sized to `RELAY_TELEMETRY_WINDOW`
(default 30 min) at `DefaultSampleInterval` (10 s) - roughly 180 samples. The
response (`workerMetricsResponse` / `metricSampleResponse`) is:

```
{
  worker_id: string,
  sample_interval_seconds: number,   // 10
  samples: [
    {
      t: string,            // ISO timestamp
      cpu_pct: number,
      mem_used: number,     // bytes
      mem_total: number,    // bytes
      gpu: boolean,
      gpu_util_pct: number,
      gpu_mem_used: number, // bytes
      gpu_mem_total: number // bytes
    }
  ]
}
```

`samples` is always a non-nil array; an offline / never-sampled worker yields an
empty array.

## Architecture (Approach A)

Extend the existing `web/src/workers/` feature module rather than create a new
one - list and detail are one feature and share `api.ts` types. New route
`/workers/:id` under `ProtectedRoute`.

### Data fetching - three focused hooks

Each hook polls at a cadence matched to its source:

- `useWorker(id)` - `refetchInterval: 3000`, `placeholderData: keepPreviousData`.
  Keeps status / `last_seen` live, matching the list page.
- `useWorkerMetrics(id)` - `refetchInterval: 10000`, aligned to the 10 s server
  sample cadence (polling faster only re-fetches identical data).
- `useWorkerWorkspaces(id)` - `enabled: !!user?.is_admin`, `refetchInterval: 15000`.
  Never issues the request for non-admins.

### Files

New, under `web/src/workers/`:

- `WorkerDetailPage.tsx` - composition: header, stat cards, telemetry, labels,
  workspaces.
- `MetricChart.tsx` - reusable SVG area+line chart component.
- `chart.ts` - **pure** path-geometry helpers (mirrors `liveness.ts`).
- `useWorker.ts`, `useWorkerMetrics.ts`, `useWorkerWorkspaces.ts` - query hooks.
- `WorkspacesPanel.tsx` - admin-only read-only workspaces table.

Modified:

- `api.ts` - add `getWorker(id)`, `getWorkerMetrics(id)`, `listWorkerWorkspaces(id)`
  and types `WorkerMetrics`, `MetricSample`, `Workspace`.
- `app/router.tsx` - add the `/workers/:id` route.
- `WorkersTable.tsx`, `WorkersGrid.tsx` - rows / cards link to the detail page.

## Page Layout

Filtered from the `v3Detail` wireframe in
`design_handoff_relay_holo/reference/screens/workers.js` to what the backend
supports. Uses the existing Holo design tokens and components (`StatusDot`,
`livenessView`, `specLine`, `labelChips`, `formatRelativeTime`).

1. **Header** - `← Workers` back link; worker name + status pill (reuse
   `StatusDot`/`livenessView`); identity line: short id, hostname, OS,
   `last_seen` relative, `last_sample` relative.
2. **Stat cards** (static capacity - no live running-task count is available):
   - CPU / RAM, e.g. `32c · 128GB`.
   - GPU, e.g. `2 × RTX 4090`, or `No GPU` when `gpu_count === 0`.
   - Max slots - capacity only (not "running / max").
3. **Telemetry** - area charts over the rolling window:
   - CPU % (`max: 100`).
   - Memory used / total (`max: mem_total`), with current GB.
   - GPU util % and GPU mem used / total - rendered **only when the worker has a
     GPU** (`gpu_count > 0` and samples report `gpu: true`).
   - Each chart shows title, current value, and a caption (`last 30 min · 10s
     samples`). Empty state ("No telemetry yet") when `samples` is empty.
4. **Labels** - read-only chips (reuse `labelChips`).
5. **Workspaces** (admin-only) - read-only table: short_id, type, source_key,
   baseline, last_used. No Evict. Not rendered for non-admins.

## Telemetry Chart Component

`MetricChart` props: `{ title, values: number[], max: number | 'auto', unit,
current, color }`. Renders a normalized SVG area+line in a responsive `viewBox`
at a fixed height.

The path geometry is a pure function in `chart.ts`
(`chartPath(values, w, h, max)`), unit-testable in isolation. Handles:

- empty series (no path / empty state handled by caller),
- single point,
- value clamping to `max`.

CPU uses `max: 100`; memory uses `max: mem_total`; GPU mirrors CPU/memory.

## States

- **Loading** - skeleton (consistent with the list page).
- **Not found (404)** - "Worker not found" with a back link.
- **Error** - message + Retry button (matches the list page error card).
- **Disabled / offline** - rendered with the same dimming from `livenessView`;
  telemetry shows its empty state when there are no samples.
- **Non-admin** - workspaces fetch is skipped (hook `enabled` guard) and the
  panel is not rendered.

## Testing

- `api.test.ts` - new clients via MSW.
- Hook tests - polling behavior; `useWorkerWorkspaces` skips the fetch when the
  user is not admin.
- `chart.ts` - pure-helper unit tests (path generation, empty / single-point,
  clamping).
- `WorkerDetailPage` tests - identity render; charts present; GPU charts only
  when the worker has a GPU; admin sees workspaces / non-admin does not;
  loading / error / not-found / empty-telemetry states.
- Navigation test - clicking a worker row routes to `/workers/:id`.

### Contract verification

Per the recurring lesson from prior slices, the new TS types are verified
field-for-field against the Go structs:

- `Worker` vs `workerResponse` (already done for the list; re-confirm the
  single-worker fields incl. `last_sample_at`).
- `WorkerMetrics` / `MetricSample` vs `workerMetricsResponse` /
  `metricSampleResponse`.
- `Workspace` vs `workspaceJSON`.

## Success Criteria

- Clicking a worker in the list opens `/workers/:id`.
- The page renders identity, hardware, and live status, auto-refreshing.
- CPU / memory (and GPU, when present) telemetry charts render from real sample
  data and update on the 10 s poll.
- Admins see the read-only workspaces table; non-admins never trigger that
  request and never see the panel.
- All states (loading, error, 404, empty telemetry) are handled.
- Full test suite and production build are green.
