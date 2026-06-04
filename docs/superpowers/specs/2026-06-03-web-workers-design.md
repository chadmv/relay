# Web Front End: Workers Slice

**Date:** 2026-06-03
**Status:** Approved (design)
**Scope:** The Workers list page for the Relay web UI, plus the repo's first
TanStack Query integration with ~3s polling.

## Summary

Build the **Workers list page** and, with it, establish the data-fetching
foundation - TanStack Query with ~3s polling - that every later page group
(Jobs, Schedules, Admin) will reuse. The page recreates the "Holo" Workers view
from `design_handoff_relay_holo/`, grounded in what the real `GET /v1/workers`
endpoint actually returns rather than the prototype's aspirational fields.

This slice follows the [Foundation + Auth
slice](2026-06-03-web-frontend-foundation-auth-design.md) and reuses its
`apiFetch` client, token store, theme tokens, shell, and routing.

## Decisions (locked during brainstorming)

| Decision | Choice |
| --- | --- |
| Slice scope | Workers **list page only**; worker detail page deferred to its own slice |
| Telemetry depth | **List-endpoint data only** - no per-worker `/metrics` fanout, no sparklines |
| Layouts | **Both** grid and table, with a segmented toggle |
| Pagination | **First page only** (50 workers), polled every ~3s; no prev/next UI |
| Sorting | **Server-side**, driven by clickable table column headers |
| Data layer shape | **Feature module** (`web/src/workers/`) - the template later pages copy |
| Query library | `@tanstack/react-query` v5 |
| Auth flow | **Untouched** - stays plain context; Query is introduced only for worker data |
| Backend changes | **None** - every field the page needs already exists |

## Background: what the backend actually provides

Grounded against `internal/api/workers.go` and `internal/metrics/sweep.go`
(verified, per the prior slice's retro lesson about inventing API contracts):

- **`GET /v1/workers`** (auth required, any user) returns a cursor-paginated
  page `{ items, next_cursor, total }`. Each item (`workerResponse`) has:
  `id`, `name`, `hostname`, `cpu_cores`, `ram_gb`, `gpu_count`, `gpu_model`,
  `os`, `max_slots`, `labels` (JSON object), `status`, `last_seen_at`,
  `last_sample_at`, `disabled_at`.
- **Sort** is server-side via `?sort=`, supporting `created_at` (default
  `-created_at`), `name`, `status`, `last_seen_at` (each asc/desc).
- **Page size** defaults to 50 (`?limit=`, range [1, 200]) per
  `internal/api/pagination.go`.
- **Status taxonomy is `online` / `stale` / `offline`**, with **`disabled`**
  overlaid by the API whenever `disabled_at` is set. There is **no `busy`/`idle`**
  - the prototype's six-state model assumed running-task counts the list
  endpoint does not provide.
  - `online`: connected, telemetry samples arriving on schedule.
  - `stale`: still connected, but no telemetry sample within
    `RELAY_TELEMETRY_STALE_AFTER` (default 30s). The metrics `Sweeper` owns this
    transition. Stale workers remain dispatch-eligible.
  - `offline`: the gRPC stream dropped; the worker is gone.
  - `disabled`: admin-paused (`POST /v1/workers/{id}/disable`), surfaced as
    `status: "disabled"` regardless of underlying liveness.

**Not available from the list endpoint** (and therefore not shown this slice):
live CPU/GPU/MEM utilization or time series (only from
`GET /v1/workers/{id}/metrics`, per worker), used/running slot counts,
running-job, and uptime. These belong to the deferred detail-page slice.

## Architecture

### Dependency and providers

Add `@tanstack/react-query` (v5) to `web/package.json`. Mount a
`QueryClientProvider` near the React root, wrapping the existing providers; the
`AuthProvider` and router are otherwise untouched.

`web/src/lib/queryClient.ts` exports a single shared `QueryClient`. Defaults:

- `staleTime: 0` - worker data is live.
- `retry: 1`.
- Polling (`refetchInterval: 3000`) and `placeholderData: keepPreviousData` are
  set **per-hook** on the workers query, not globally, so future non-polled
  queries are unaffected.
- `refetchIntervalInBackground` is left at its default (`false`): polling pauses
  when the browser tab is hidden, so background tabs do not hammer the API.
- Errors propagate as the existing typed `ApiError`. The global `401`
  interceptor in `lib/api.ts` already clears the token and redirects to
  `/auth`; the QueryClient does not duplicate that handling.

### Feature module

```
web/src/workers/
  api.ts            # listWorkers(sort) -> WorkersPage; Worker + WorkerSort types
  useWorkers.ts     # useQuery wrapper: key ['workers', sort], 3s poll
  liveness.ts       # pure: status -> { label, colorToken, dimmed }; relative-time fmt
  WorkersPage.tsx   # header, summary strip, view toggle, loading/error/empty states
  WorkersGrid.tsx   # card grid
  WorkersTable.tsx  # dense sortable table
  StatusDot.tsx     # small status indicator (dot + color token)
```

This split is the convention later page groups copy: a feature folder with a
thin typed `api.ts`, a query hook, pure presentation helpers, and components.

### Routing

Replace the placeholder `/workers` route with `WorkersPage`, behind the
existing `ProtectedRoute`. The `HoloShell` nav link already points at
`/workers`. No admin gate - `GET /v1/workers` is open to any authenticated
user. Rows and cards are **non-interactive** this slice (the detail page is
deferred); they do not link to a dead route.

## Data layer

### `api.ts`

```ts
export type WorkerStatus = 'online' | 'stale' | 'offline' | 'disabled'

export interface Worker {
  id: string
  name: string
  hostname: string
  cpu_cores: number
  ram_gb: number
  gpu_count: number
  gpu_model: string
  os: string
  max_slots: number
  labels: Record<string, string> | null
  status: WorkerStatus
  last_seen_at?: string
  last_sample_at?: string
  disabled_at?: string
}

export interface WorkersPage {
  items: Worker[]
  next_cursor: string
  total: number
}

export type WorkerSort =
  | '-created_at' | 'created_at'
  | 'name' | '-name'
  | 'status' | '-status'
  | 'last_seen_at' | '-last_seen_at'

export function listWorkers(sort: WorkerSort): Promise<WorkersPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  return apiFetch<WorkersPage>(`/workers?${q}`)
}
```

Types are hand-written to match `workerResponse` exactly (no codegen; verified
against `internal/api/workers.go`). First page only - no `cursor` param. The
page size is passed **explicitly** as `limit=50` (the server default) rather
than relying on the server default, so the client's page size is
self-documenting and decoupled from any future server-side change.

### `useWorkers.ts`

```ts
useQuery({
  queryKey: ['workers', sort],
  queryFn: () => listWorkers(sort),
  refetchInterval: 3000,
  placeholderData: keepPreviousData,
})
```

`keepPreviousData` is the central polling-UX detail: re-sorting (a new query
key) keeps the old rows visible while the new page loads, and each 3s poll swaps
data in with no loading flash.

### `liveness.ts`

A pure, unit-testable mapper. The server is the source of truth for status (the
`Sweeper` already computes `stale`; the API already overlays `disabled`), so the
client does **not** recompute staleness. It only maps status to presentation:

| status | dot color token | label | card treatment |
| --- | --- | --- | --- |
| `online` | `--ok` (green) | ONLINE | normal |
| `stale` | `--warn` (amber) | STALE | normal |
| `disabled` | `--fg-mute` (grey) | DISABLED | dimmed (opacity 0.7) |
| `offline` | `--err` (red) | OFFLINE | dimmed (opacity 0.55) |

A small relative-time formatter renders `last_seen_at` as "last seen 12s ago"
with no date library.

## UI

### Page header and summary strip

Title "Workers" with the "FLEET" mono eyebrow. The summary counts strip is
**page-scoped and labeled as such**: counts are derived from the loaded page
(e.g. "12 ONLINE · 2 STALE · 1 DISABLED · 1 OFFLINE"), with the endpoint's
`total` shown separately ("20 workers"). This avoids implying fleet-wide tallies
when only one page is held. A true fleet-wide status breakdown would require a
backend aggregate endpoint - **out of scope, noted as a future gap.**

An in-page **live indicator** ("● live · auto-refreshing") near the header
subtly reflects the query's `isFetching`. The global shell sync indicator is
intentionally left untouched (less coupling).

### View toggle

The prototype's segmented Grid / Table control, top-right. Selection persists to
`localStorage` under a single key (`relay.workers.view`), matching the existing
token-store choke-point pattern, so the choice survives reloads.

### Grid view (`WorkersGrid`)

One glass-panel card per worker: name + status dot/label, hardware spec line
(cpu/ram, with `gpu_model` appended when present), `max_slots` as capacity, label chips, and a "last
seen Xs ago" footer. Disabled/offline cards are dimmed per the liveness map. No
sparklines, no used/running cells. Non-interactive.

### Table view (`WorkersTable`)

Dense rows. Columns: **Name, Status, Slots (max), Spec, Labels, Last seen**.
Sortable headers - **Name, Status, Last seen** (plus the default `created_at`) -
render a clickable label with an asc/desc caret; clicking re-keys the query and
toggles direction. Non-sortable columns (Spec, Labels, Slots) render plain
headers. Sort state is shared with the grid (the grid simply uses whichever sort
is active; default `-created_at`).

### States

- **Initial load:** skeleton rows/cards (not a bare spinner) so the layout does
  not jump.
- **Error:** a glass-panel banner with the `ApiError` message and a "Retry"
  button (`refetch()`); polling continues underneath.
- **Empty:** a centered "No workers enrolled yet" panel.

## Error handling

| Condition | Treatment |
| --- | --- |
| `401` mid-session | existing global `onUnauthorized` interceptor clears token + redirects to `/auth`; no page-level handling |
| Other `ApiError` (500, network) on first load | glass-panel error banner with message + Retry |
| Poll failure **after** data already loaded | keep showing the last good rows (`keepPreviousData`) and surface a subtle error state; do **not** blank the page on a transient blip |
| Empty `items` | "No workers enrolled yet" panel |

The polling-resilience rule: a single failed 3s refetch must not wipe a working
screen.

## Testing

### Front end (Vitest + React Testing Library + MSW)

Extends the existing harness from the Auth slice.

- **`liveness.ts`** - pure unit tests: each status maps to the correct
  label/color/dimming; relative-time formatter output.
- **`api.ts`** - MSW-mocked `GET /v1/workers`: correct `sort` query string,
  `WorkersPage` parsing, `ApiError` surfaced on the `{error}` envelope.
- **Page integration** - render `WorkersPage` with MSW:
  - rows/cards render from a mocked page;
  - clicking a sort header re-requests with the new `sort` and reorders;
  - the view toggle switches grid/table and persists to `localStorage`;
  - an error envelope shows the banner + Retry;
  - an empty response shows the empty state.
- **Polling** - one focused test with fake timers: advancing ~3s triggers a
  second fetch (proving the loop is wired, not exhaustive timer choreography).

### Backend (Go)

**None.** No backend changes this slice; `GET /v1/workers` and its
sort/pagination already have coverage.

### Not in scope

End-to-end browser tests (Playwright/Cypress, still deferred and tracked in the
backlog), per-worker `/metrics` polling and sparklines, the worker detail page,
and all worker mutations (edit / disable / enable).

## Future gaps recorded

- **Fleet-wide status counts:** the summary strip is page-scoped because no
  aggregate endpoint exists. A `GET /v1/workers/stats` (counts by status) would
  let it show true fleet totals.
- **Worker detail page:** telemetry charts (`/metrics`), running tasks,
  workspaces, and admin actions (edit / disable / enable) - its own later slice.
- **Pagination UI:** if real fleets outgrow one page, add cursor prev/next or
  load-more, composing the cursor into the query key alongside sort.
