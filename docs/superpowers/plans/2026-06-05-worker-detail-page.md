# Worker Detail Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a read-only worker detail page at `/workers/:id` showing identity, hardware, live utilization telemetry (CPU/memory/GPU), labels, and an admin-only source-workspaces table.

**Architecture:** Extend the existing `web/src/workers/` feature module. Three focused TanStack Query hooks poll at cadences matched to their data (`useWorker` 3s, `useWorkerMetrics` 10s, `useWorkerWorkspaces` 15s). Telemetry charts are hand-rolled SVG built from a pure geometry helper - no new charting dependency. The workspaces panel is mounted only for admins, so non-admins never trigger that admin-only request.

**Tech Stack:** React 18, TypeScript, TanStack Query v5, react-router-dom v7, Tailwind v4 (Holo design tokens), Vitest + Testing Library + MSW.

**Spec:** `docs/superpowers/specs/2026-06-05-worker-detail-page-design.md`

**Conventions for every task:**
- All frontend commands run from the `web/` directory.
- Single test file: `npx vitest run src/workers/<file>.test.tsx`. Full suite: `npm test`.
- TDD: write the failing test, see it fail, implement, see it pass, commit.
- House rule: never use em dashes or en dashes. Use hyphens.

---

## File Structure

New files (all under `web/src/workers/` unless noted):

- `chart.ts` - pure SVG path geometry (`chartPath`). No React/DOM.
- `chart.test.ts`
- `MetricChart.tsx` - reusable SVG area+line chart component.
- `MetricChart.test.tsx`
- `useWorker.ts`, `useWorkerMetrics.ts`, `useWorkerWorkspaces.ts` - query hooks.
- `useWorker.test.tsx`, `useWorkerMetrics.test.tsx`, `useWorkerWorkspaces.test.tsx`
- `WorkspacesPanel.tsx` - admin-only read-only workspaces table.
- `WorkspacesPanel.test.tsx`
- `WorkerDetailPage.tsx` - page composition.
- `WorkerDetailPage.test.tsx`

Modified files:

- `web/src/workers/api.ts` - add `getWorker`, `getWorkerMetrics`, `listWorkerWorkspaces` + types.
- `web/src/workers/api.test.ts` - tests for the new clients.
- `web/src/workers/liveness.ts` - add `formatGB`.
- `web/src/workers/liveness.test.ts` - test for `formatGB`.
- `web/src/app/router.tsx` - add the `/workers/:id` route.
- `web/src/workers/WorkersGrid.tsx` + `WorkersTable.tsx` - cards/rows link to the detail page.
- `web/src/workers/WorkersGrid.test.tsx` + `WorkersTable.test.tsx` + `WorkersPage.test.tsx` - wrap renders in a router (Links require router context) and assert link targets.

---

## Task 1: API client and types

**Files:**
- Modify: `web/src/workers/api.ts`
- Test: `web/src/workers/api.test.ts`

- [ ] **Step 1: Add the failing tests**

Append to `web/src/workers/api.test.ts`. Also update the import on line 5 to include the new functions and the `MetricSample`/`WorkerMetrics`/`Workspace` types:

```ts
import {
  listWorkers,
  getWorkerStats,
  getWorker,
  getWorkerMetrics,
  listWorkerWorkspaces,
  type WorkersPage,
} from './api'
```

Append these tests at the end of the file:

```ts
test('getWorker fetches /workers/{id}', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/workers/w1', ({ request }) => {
      path = new URL(request.url).pathname
      return HttpResponse.json({ id: 'w1', name: 'render-01', status: 'online' })
    }),
  )
  const w = await getWorker('w1')
  expect(path).toBe('/v1/workers/w1')
  expect(w.name).toBe('render-01')
})

test('getWorkerMetrics fetches /workers/{id}/metrics', async () => {
  server.use(
    http.get('/v1/workers/w1/metrics', () =>
      HttpResponse.json({
        worker_id: 'w1',
        sample_interval_seconds: 10,
        samples: [
          {
            t: '2026-06-05T00:00:00Z',
            cpu_pct: 12.5,
            mem_used: 1,
            mem_total: 2,
            gpu: false,
            gpu_util_pct: 0,
            gpu_mem_used: 0,
            gpu_mem_total: 0,
          },
        ],
      }),
    ),
  )
  const m = await getWorkerMetrics('w1')
  expect(m.sample_interval_seconds).toBe(10)
  expect(m.samples[0].cpu_pct).toBe(12.5)
})

test('listWorkerWorkspaces fetches /workers/{id}/workspaces', async () => {
  server.use(
    http.get('/v1/workers/w1/workspaces', () =>
      HttpResponse.json([
        {
          source_type: 'perforce',
          source_key: '//depot/x',
          short_id: 'ws-1',
          baseline_hash: '@1',
          last_used_at: '2026-06-05T00:00:00Z',
        },
      ]),
    ),
  )
  const ws = await listWorkerWorkspaces('w1')
  expect(ws).toHaveLength(1)
  expect(ws[0].short_id).toBe('ws-1')
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/workers/api.test.ts`
Expected: FAIL - `getWorker`, `getWorkerMetrics`, `listWorkerWorkspaces` are not exported.

- [ ] **Step 3: Implement the clients and types**

Append to `web/src/workers/api.ts`:

```ts
export interface MetricSample {
  t: string
  cpu_pct: number
  mem_used: number
  mem_total: number
  gpu: boolean
  gpu_util_pct: number
  gpu_mem_used: number
  gpu_mem_total: number
}

export interface WorkerMetrics {
  worker_id: string
  sample_interval_seconds: number
  samples: MetricSample[]
}

export interface Workspace {
  source_type: string
  source_key: string
  short_id: string
  baseline_hash: string
  last_used_at: string
}

export function getWorker(id: string): Promise<Worker> {
  return apiFetch<Worker>(`/workers/${id}`)
}

// Short-term utilization history. samples is always present (empty for an
// offline / never-sampled worker).
export function getWorkerMetrics(id: string): Promise<WorkerMetrics> {
  return apiFetch<WorkerMetrics>(`/workers/${id}/metrics`)
}

// Admin-only. Source workspaces resident on the worker.
export function listWorkerWorkspaces(id: string): Promise<Workspace[]> {
  return apiFetch<Workspace[]>(`/workers/${id}/workspaces`)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/workers/api.test.ts`
Expected: PASS (all tests in the file).

- [ ] **Step 5: Verify the TS types match the Go structs (contract check)**

Confirm each TS field name matches the JSON tag on the Go struct. No code change expected; fix the TS types if any mismatch is found.

- `Worker` vs `workerResponse` in `internal/api/workers.go:17` - `id, name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, max_slots, labels, status, last_seen_at, last_sample_at, disabled_at`. (Already defined for the list; `last_sample_at` is present.)
- `MetricSample` vs `metricSampleResponse` in `internal/api/worker_metrics.go:13` - `t, cpu_pct, mem_used, mem_total, gpu, gpu_util_pct, gpu_mem_used, gpu_mem_total`.
- `WorkerMetrics` vs `workerMetricsResponse` in `internal/api/worker_metrics.go:24` - `worker_id, sample_interval_seconds, samples`.
- `Workspace` vs `workspaceJSON` in `internal/api/workspaces.go:11` - `source_type, source_key, short_id, baseline_hash, last_used_at`.

- [ ] **Step 6: Commit**

```bash
git add web/src/workers/api.ts web/src/workers/api.test.ts
git commit -m "feat(web): worker detail API clients and types"
```

---

## Task 2: formatGB helper

**Files:**
- Modify: `web/src/workers/liveness.ts`
- Test: `web/src/workers/liveness.test.ts`

- [ ] **Step 1: Add the failing test**

Update the import on line 2 of `web/src/workers/liveness.test.ts` to include `formatGB`:

```ts
import { livenessView, formatRelativeTime, specLine, labelChips, formatGB } from './liveness'
```

Append this block to `web/src/workers/liveness.test.ts`:

```ts
describe('formatGB', () => {
  test('formats bytes as GB with one decimal', () => {
    expect(formatGB(2 * 1024 ** 3)).toBe('2.0 GB')
  })
  test('zero is 0.0 GB', () => {
    expect(formatGB(0)).toBe('0.0 GB')
  })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/workers/liveness.test.ts`
Expected: FAIL - `formatGB` is not exported.

- [ ] **Step 3: Implement formatGB**

Append to `web/src/workers/liveness.ts`:

```ts
// Formats a byte count as gibibytes with one decimal, labeled "GB" to match the
// rest of the UI (e.g. worker.ram_gb). Used by the telemetry memory charts.
export function formatGB(bytes: number): string {
  return `${(bytes / 1024 ** 3).toFixed(1)} GB`
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/workers/liveness.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/liveness.ts web/src/workers/liveness.test.ts
git commit -m "feat(web): formatGB byte formatter"
```

---

## Task 3: Chart geometry (pure)

**Files:**
- Create: `web/src/workers/chart.ts`
- Test: `web/src/workers/chart.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/chart.test.ts`:

```ts
import { describe, expect, test } from 'vitest'
import { chartPath } from './chart'

describe('chartPath', () => {
  test('empty series yields empty paths', () => {
    expect(chartPath([], 100, 50, 100)).toEqual({ line: '', area: '' })
  })

  test('single point draws a flat line across the full width', () => {
    const { line } = chartPath([50], 100, 50, 100)
    expect(line).toBe('M0,25 L100,25')
  })

  test('maps min to the baseline and max to the top', () => {
    const { line } = chartPath([0, 100], 100, 50, 100)
    expect(line).toBe('M0,50 L100,0')
  })

  test('clamps values above max to the top', () => {
    const { line } = chartPath([150], 100, 50, 100)
    expect(line).toBe('M0,0 L100,0')
  })

  test('non-positive max maps everything to the baseline (no NaN)', () => {
    const { line } = chartPath([5, 9], 100, 50, 0)
    expect(line).toBe('M0,50 L100,50')
  })

  test('area closes along the baseline', () => {
    const { area } = chartPath([0, 100], 100, 50, 100)
    expect(area).toBe('M0,50 L100,0 L100,50 L0,50 Z')
  })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/workers/chart.test.ts`
Expected: FAIL - cannot find module `./chart`.

- [ ] **Step 3: Implement chartPath**

Create `web/src/workers/chart.ts`:

```ts
// Pure SVG path geometry for the telemetry charts. No React, no DOM - unit
// tested in isolation (mirrors liveness.ts).

export interface ChartPaths {
  line: string
  area: string
}

// Maps a series of values to an SVG line path and a filled area path inside a
// width x height box. y is inverted (0 at the top, height at the bottom). Values
// are clamped to [0, max]; a non-positive max maps everything to the baseline so
// the path never contains NaN. A single point is drawn as a flat line across the
// full width. An empty series yields empty strings so the caller can render an
// empty state instead.
export function chartPath(values: number[], width: number, height: number, max: number): ChartPaths {
  if (values.length === 0) return { line: '', area: '' }
  const pts = values.length === 1 ? [values[0], values[0]] : values
  const n = pts.length
  const dx = width / (n - 1)
  const y = (v: number): number => {
    if (max <= 0) return round(height)
    const clamped = Math.min(Math.max(v, 0), max)
    return round(height - (clamped / max) * height)
  }
  const coords = pts.map((v, i) => [round(i * dx), y(v)] as const)
  const line = coords.map(([x, yy], i) => `${i === 0 ? 'M' : 'L'}${x},${yy}`).join(' ')
  const first = coords[0][0]
  const lastX = coords[n - 1][0]
  const area = `${line} L${lastX},${round(height)} L${first},${round(height)} Z`
  return { line, area }
}

function round(n: number): number {
  return Math.round(n * 100) / 100
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/workers/chart.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/chart.ts web/src/workers/chart.test.ts
git commit -m "feat(web): pure SVG chart geometry helper"
```

---

## Task 4: MetricChart component

**Files:**
- Create: `web/src/workers/MetricChart.tsx`
- Test: `web/src/workers/MetricChart.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/MetricChart.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { MetricChart } from './MetricChart'

test('renders the title, current value, and a chart path', () => {
  const { container } = render(
    <MetricChart title="CPU" values={[0, 50, 100]} max={100} current="50%" colorClass="text-accent" />,
  )
  expect(screen.getByText('CPU')).toBeInTheDocument()
  expect(screen.getByText('50%')).toBeInTheDocument()
  expect(screen.getByRole('img', { name: 'CPU' })).toBeInTheDocument()
  expect(container.querySelectorAll('path').length).toBe(2)
})

test('renders no path for an empty series', () => {
  const { container } = render(
    <MetricChart title="CPU" values={[]} max={100} current="-" colorClass="text-accent" />,
  )
  expect(container.querySelectorAll('path').length).toBe(0)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/workers/MetricChart.test.tsx`
Expected: FAIL - cannot find module `./MetricChart`.

- [ ] **Step 3: Implement MetricChart**

Create `web/src/workers/MetricChart.tsx`:

```tsx
import { chartPath } from './chart'

const W = 300
const H = 60

// A small hand-rolled area+line chart. colorClass sets the line/fill color via
// currentColor so it stays on the Holo palette (e.g. "text-accent", "text-ok").
// An empty series renders just the frame; the caller decides whether to show a
// chart at all.
export function MetricChart({
  title,
  values,
  max,
  current,
  colorClass,
}: {
  title: string
  values: number[]
  max: number
  current: string
  colorClass: string
}) {
  const { line, area } = chartPath(values, W, H, max)
  return (
    <div className="rounded-card border border-border bg-white/5 p-3">
      <div className="flex items-baseline justify-between">
        <span className="font-mono text-[10px] tracking-wider text-fg-mute">{title}</span>
        <span className="font-mono text-[12px] text-fg">{current}</span>
      </div>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        className={`mt-2 h-16 w-full ${colorClass}`}
        role="img"
        aria-label={title}
      >
        {area && <path d={area} fill="currentColor" fillOpacity={0.15} />}
        {line && <path d={line} fill="none" stroke="currentColor" strokeWidth={1.5} />}
      </svg>
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/workers/MetricChart.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/MetricChart.tsx web/src/workers/MetricChart.test.tsx
git commit -m "feat(web): MetricChart SVG component"
```

---

## Task 5: useWorker hook

**Files:**
- Create: `web/src/workers/useWorker.ts`
- Test: `web/src/workers/useWorker.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/useWorker.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorker } from './useWorker'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches the worker and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/workers/w1', () => {
      count++
      return HttpResponse.json({ id: 'w1', name: 'render-01', status: 'online' })
    }),
  )
  const { result } = renderHook(() => useWorker('w1', 20), { wrapper })
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
  await waitFor(() => expect(result.current.data?.name).toBe('render-01'))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/workers/useWorker.test.tsx`
Expected: FAIL - cannot find module `./useWorker`.

- [ ] **Step 3: Implement useWorker**

Create `web/src/workers/useWorker.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getWorker } from './api'

// Polls a single worker's identity/status. Default 3000 matches the list page,
// keeping status/last_seen live. Tests inject a small value.
export function useWorker(id: string, intervalMs = 3000) {
  return useQuery({
    queryKey: ['worker', id],
    queryFn: () => getWorker(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/workers/useWorker.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/useWorker.ts web/src/workers/useWorker.test.tsx
git commit -m "feat(web): useWorker polling hook"
```

---

## Task 6: useWorkerMetrics hook

**Files:**
- Create: `web/src/workers/useWorkerMetrics.ts`
- Test: `web/src/workers/useWorkerMetrics.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/useWorkerMetrics.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkerMetrics } from './useWorkerMetrics'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches metrics and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/workers/w1/metrics', () => {
      count++
      return HttpResponse.json({ worker_id: 'w1', sample_interval_seconds: 10, samples: [] })
    }),
  )
  const { result } = renderHook(() => useWorkerMetrics('w1', 20), { wrapper })
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
  await waitFor(() => expect(result.current.data?.sample_interval_seconds).toBe(10))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/workers/useWorkerMetrics.test.tsx`
Expected: FAIL - cannot find module `./useWorkerMetrics`.

- [ ] **Step 3: Implement useWorkerMetrics**

Create `web/src/workers/useWorkerMetrics.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getWorkerMetrics } from './api'

// Polls a worker's telemetry. Default 10000 matches the 10s server sample
// cadence; polling faster only re-fetches identical data. Tests inject a small value.
export function useWorkerMetrics(id: string, intervalMs = 10000) {
  return useQuery({
    queryKey: ['worker', id, 'metrics'],
    queryFn: () => getWorkerMetrics(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/workers/useWorkerMetrics.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/useWorkerMetrics.ts web/src/workers/useWorkerMetrics.test.tsx
git commit -m "feat(web): useWorkerMetrics polling hook"
```

---

## Task 7: useWorkerWorkspaces hook

**Files:**
- Create: `web/src/workers/useWorkerWorkspaces.ts`
- Test: `web/src/workers/useWorkerWorkspaces.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/useWorkerWorkspaces.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkerWorkspaces } from './useWorkerWorkspaces'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches workspaces and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/workers/w1/workspaces', () => {
      count++
      return HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-1', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
      ])
    }),
  )
  const { result } = renderHook(() => useWorkerWorkspaces('w1', 20), { wrapper })
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
  await waitFor(() => expect(result.current.data?.[0].short_id).toBe('ws-1'))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/workers/useWorkerWorkspaces.test.tsx`
Expected: FAIL - cannot find module `./useWorkerWorkspaces`.

- [ ] **Step 3: Implement useWorkerWorkspaces**

Create `web/src/workers/useWorkerWorkspaces.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listWorkerWorkspaces } from './api'

// Polls a worker's source workspaces. Admin-only data; this hook is only mounted
// for admins (WorkerDetailPage gates rendering of the panel), so no enabled flag
// is needed - a non-admin page never mounts the panel and never fires this
// request. Slow cadence since workspaces change rarely. Tests inject a small value.
export function useWorkerWorkspaces(id: string, intervalMs = 15000) {
  return useQuery({
    queryKey: ['worker', id, 'workspaces'],
    queryFn: () => listWorkerWorkspaces(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/workers/useWorkerWorkspaces.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/useWorkerWorkspaces.ts web/src/workers/useWorkerWorkspaces.test.tsx
git commit -m "feat(web): useWorkerWorkspaces polling hook"
```

---

## Task 8: WorkspacesPanel component

**Files:**
- Create: `web/src/workers/WorkspacesPanel.tsx`
- Test: `web/src/workers/WorkspacesPanel.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/WorkspacesPanel.test.tsx`:

```tsx
import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { WorkspacesPanel } from './WorkspacesPanel'

test('renders workspace rows', async () => {
  server.use(
    http.get('/v1/workers/w1/workspaces', () =>
      HttpResponse.json([
        {
          source_type: 'perforce',
          source_key: '//depot/x/main',
          short_id: 'ws-a4f2',
          baseline_hash: '@CL 81234',
          last_used_at: '2026-06-05T00:00:00Z',
        },
      ]),
    ),
  )
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  expect(await screen.findByText('ws-a4f2')).toBeInTheDocument()
  expect(screen.getByText('//depot/x/main')).toBeInTheDocument()
})

test('shows the empty state when there are no workspaces', async () => {
  server.use(http.get('/v1/workers/w1/workspaces', () => HttpResponse.json([])))
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  expect(await screen.findByText('No workspaces.')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/workers/WorkspacesPanel.test.tsx`
Expected: FAIL - cannot find module `./WorkspacesPanel`.

- [ ] **Step 3: Implement WorkspacesPanel**

Create `web/src/workers/WorkspacesPanel.tsx`:

```tsx
import { formatRelativeTime } from './liveness'
import { useWorkerWorkspaces } from './useWorkerWorkspaces'

const COLS = 'grid grid-cols-[120px_90px_1fr_120px_90px]'

// Admin-only, read-only source workspaces table. Mounted by WorkerDetailPage
// only when the current user is an admin.
export function WorkspacesPanel({ workerId }: { workerId: string }) {
  const { data, isLoading } = useWorkerWorkspaces(workerId)
  const rows = data ?? []
  return (
    <div className="flex flex-col gap-2">
      <div className="font-mono text-[11px] tracking-widest text-fg-mute">SOURCE WORKSPACES</div>
      <div className="rounded-card border border-border bg-white/5">
        <div className={`${COLS} border-b border-border px-4 py-2 font-mono text-[10px] tracking-wider text-fg-mute`}>
          <span>SHORT ID</span>
          <span>TYPE</span>
          <span>SOURCE KEY</span>
          <span>BASELINE</span>
          <span>LAST USED</span>
        </div>
        {!isLoading && rows.length === 0 && (
          <div className="px-4 py-3 text-[12px] text-fg-mute">No workspaces.</div>
        )}
        {rows.map((ws) => (
          <div
            key={ws.short_id}
            className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11px]`}
          >
            <span className="text-fg">{ws.short_id}</span>
            <span className="text-fg-mute">{ws.source_type}</span>
            <span className="truncate text-fg-mute">{ws.source_key}</span>
            <span className="text-fg-mute">{ws.baseline_hash}</span>
            <span className="text-fg-mute">{formatRelativeTime(ws.last_used_at)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/workers/WorkspacesPanel.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/WorkspacesPanel.tsx web/src/workers/WorkspacesPanel.test.tsx
git commit -m "feat(web): admin-only WorkspacesPanel"
```

---

## Task 9: WorkerDetailPage

**Files:**
- Create: `web/src/workers/WorkerDetailPage.tsx`
- Test: `web/src/workers/WorkerDetailPage.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/WorkerDetailPage.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { WorkerDetailPage } from './WorkerDetailPage'

const ID = 'w1abc234'
const GB = 1024 ** 3

const WORKER = {
  id: ID,
  name: 'render-rig-A',
  hostname: 'render-a.studio.dev',
  cpu_cores: 32,
  ram_gb: 128,
  gpu_count: 2,
  gpu_model: 'RTX 4090',
  os: 'linux',
  max_slots: 4,
  labels: { rack: 'A' },
  status: 'online',
  last_seen_at: '2026-06-05T00:00:00Z',
  last_sample_at: '2026-06-05T00:00:00Z',
}

function metrics(over: Record<string, unknown> = {}) {
  return {
    worker_id: ID,
    sample_interval_seconds: 10,
    samples: [
      { t: '2026-06-05T00:00:00Z', cpu_pct: 40, mem_used: 64 * GB, mem_total: 128 * GB, gpu: true, gpu_util_pct: 55, gpu_mem_used: 8 * GB, gpu_mem_total: 24 * GB },
      { t: '2026-06-05T00:00:10Z', cpu_pct: 60, mem_used: 70 * GB, mem_total: 128 * GB, gpu: true, gpu_util_pct: 70, gpu_mem_used: 9 * GB, gpu_mem_total: 24 * GB },
    ],
    ...over,
  }
}

function renderDetail(isAdmin: boolean) {
  setToken('test-token')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: isAdmin }),
    ),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/workers/${ID}`]}>
        <AuthProvider>
          <Routes>
            <Route path="/workers/:id" element={<WorkerDetailPage />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('renders identity, hardware, and CPU/memory telemetry charts', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByText('render-rig-A')).toBeInTheDocument()
  expect(screen.getByText(/render-a\.studio\.dev/)).toBeInTheDocument()
  expect(screen.getByText('32c · 128GB')).toBeInTheDocument()
  expect(await screen.findByRole('img', { name: 'CPU' })).toBeInTheDocument()
  expect(screen.getByRole('img', { name: 'MEMORY' })).toBeInTheDocument()
})

test('shows GPU charts when the worker has a GPU', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByRole('img', { name: 'GPU' })).toBeInTheDocument()
  expect(screen.getByRole('img', { name: 'GPU MEMORY' })).toBeInTheDocument()
})

test('hides GPU charts when the worker has no GPU', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json({ ...WORKER, gpu_count: 0, gpu_model: '' })))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByRole('img', { name: 'CPU' })).toBeInTheDocument()
  expect(screen.queryByRole('img', { name: 'GPU' })).not.toBeInTheDocument()
})

test('shows an empty telemetry state when there are no samples', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics({ samples: [] }))))
  renderDetail(false)
  expect(await screen.findByText('No telemetry yet.')).toBeInTheDocument()
})

test('shows not-found for a 404 worker', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json({ error: 'worker not found' }, { status: 404 })))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics({ samples: [] }))))
  renderDetail(false)
  expect(await screen.findByText('Worker not found.')).toBeInTheDocument()
})

test('admins see the workspaces panel', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(
    http.get(`/v1/workers/${ID}/workspaces`, () =>
      HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-a4f2', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
      ]),
    ),
  )
  renderDetail(true)
  expect(await screen.findByText('ws-a4f2')).toBeInTheDocument()
})

test('non-admins never see or fetch workspaces', async () => {
  let wsCount = 0
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(
    http.get(`/v1/workers/${ID}/workspaces`, () => {
      wsCount++
      return HttpResponse.json([])
    }),
  )
  renderDetail(false)
  await screen.findByText('render-rig-A')
  await screen.findByRole('img', { name: 'CPU' })
  await new Promise((r) => setTimeout(r, 50))
  expect(wsCount).toBe(0)
  expect(screen.queryByText('SOURCE WORKSPACES')).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/workers/WorkerDetailPage.test.tsx`
Expected: FAIL - cannot find module `./WorkerDetailPage`.

- [ ] **Step 3: Implement WorkerDetailPage**

Create `web/src/workers/WorkerDetailPage.tsx`:

```tsx
import { Link, useParams } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { useAuth } from '../auth/AuthProvider'
import { Button } from '../components/Button'
import { MetricChart } from './MetricChart'
import { StatusDot } from './StatusDot'
import { WorkspacesPanel } from './WorkspacesPanel'
import { formatGB, formatRelativeTime, labelChips, livenessView } from './liveness'
import { useWorker } from './useWorker'
import { useWorkerMetrics } from './useWorkerMetrics'
import type { MetricSample } from './api'

function pct(n: number): string {
  return `${Math.round(n)}%`
}

function last<T>(arr: T[]): T | undefined {
  return arr[arr.length - 1]
}

export function WorkerDetailPage() {
  const { id = '' } = useParams()
  const { user } = useAuth()
  const { data: worker, error, isLoading, refetch } = useWorker(id)
  const { data: metrics } = useWorkerMetrics(id)

  if (isLoading && !worker) {
    return <div className="h-40 rounded-card border border-border bg-white/5" />
  }

  if (error && !worker) {
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
        {notFound ? (
          <div className="text-[13px] text-fg-mute">Worker not found.</div>
        ) : (
          <>
            <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => refetch()}>
              Retry
            </Button>
          </>
        )}
        <div className="mt-4">
          <Link to="/workers" className="font-mono text-[11px] text-accent">
            ← Workers
          </Link>
        </div>
      </div>
    )
  }

  if (!worker) return null

  const samples: MetricSample[] = metrics?.samples ?? []
  const hasGpu = worker.gpu_count > 0
  const latest = last(samples)
  const memTotal = latest?.mem_total ?? 0
  const gpuMemTotal = latest?.gpu_mem_total ?? 0

  return (
    <div className={`flex flex-col gap-5 ${livenessView(worker.status).dimClass}`}>
      <div className="flex flex-col gap-1">
        <Link to="/workers" className="font-mono text-[11px] text-fg-mute hover:text-fg">
          ← Workers
        </Link>
        <div className="flex items-center gap-3">
          <h1 className="text-[28px] font-normal tracking-tight">{worker.name}</h1>
          <StatusDot status={worker.status} />
        </div>
        <div className="font-mono text-[11px] text-fg-mute">
          id {worker.id.slice(0, 8)} · {worker.hostname} · {worker.os} ·{' '}
          {worker.last_seen_at ? `last seen ${formatRelativeTime(worker.last_seen_at)}` : 'never seen'}
          {worker.last_sample_at ? ` · sampled ${formatRelativeTime(worker.last_sample_at)}` : ''}
        </div>
      </div>

      <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-3">
        <div className="rounded-card border border-border bg-white/5 p-4">
          <div className="font-mono text-[10px] tracking-wider text-fg-mute">CPU · RAM</div>
          <div className="mt-1 text-[20px]">{worker.cpu_cores}c · {worker.ram_gb}GB</div>
          <div className="font-mono text-[10px] text-fg-mute">os: {worker.os}</div>
        </div>
        <div className="rounded-card border border-border bg-white/5 p-4">
          <div className="font-mono text-[10px] tracking-wider text-fg-mute">GPU</div>
          <div className="mt-1 text-[20px]">
            {hasGpu ? `${worker.gpu_count} × ${worker.gpu_model}` : 'No GPU'}
          </div>
        </div>
        <div className="rounded-card border border-border bg-white/5 p-4">
          <div className="font-mono text-[10px] tracking-wider text-fg-mute">MAX SLOTS</div>
          <div className="mt-1 text-[20px]">{worker.max_slots}</div>
          <div className="font-mono text-[10px] text-fg-mute">capacity</div>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <div className="flex items-baseline justify-between">
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">TELEMETRY</div>
          <div className="font-mono text-[10px] text-fg-dim">last 30 min · 10s samples</div>
        </div>
        {samples.length === 0 ? (
          <div className="rounded-card border border-border bg-white/5 p-6 text-center text-[12px] text-fg-mute">
            No telemetry yet.
          </div>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
            <MetricChart
              title="CPU"
              values={samples.map((s) => s.cpu_pct)}
              max={100}
              current={latest ? pct(latest.cpu_pct) : '-'}
              colorClass="text-accent"
            />
            <MetricChart
              title="MEMORY"
              values={samples.map((s) => s.mem_used)}
              max={memTotal}
              current={latest ? `${formatGB(latest.mem_used)} / ${formatGB(latest.mem_total)}` : '-'}
              colorClass="text-ok"
            />
            {hasGpu && (
              <>
                <MetricChart
                  title="GPU"
                  values={samples.map((s) => s.gpu_util_pct)}
                  max={100}
                  current={latest ? pct(latest.gpu_util_pct) : '-'}
                  colorClass="text-warn"
                />
                <MetricChart
                  title="GPU MEMORY"
                  values={samples.map((s) => s.gpu_mem_used)}
                  max={gpuMemTotal}
                  current={latest ? `${formatGB(latest.gpu_mem_used)} / ${formatGB(latest.gpu_mem_total)}` : '-'}
                  colorClass="text-warn"
                />
              </>
            )}
          </div>
        )}
      </div>

      {labelChips(worker.labels).length > 0 && (
        <div className="flex flex-col gap-2">
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">LABELS</div>
          <div className="flex flex-wrap gap-1">
            {labelChips(worker.labels).map((c) => (
              <span
                key={c}
                className="rounded-full border border-accent/40 bg-accent/10 px-2 py-0.5 font-mono text-[10px] text-accent"
              >
                {c}
              </span>
            ))}
          </div>
        </div>
      )}

      {user?.is_admin && <WorkspacesPanel workerId={id} />}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/workers/WorkerDetailPage.test.tsx`
Expected: PASS (all 7 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/WorkerDetailPage.tsx web/src/workers/WorkerDetailPage.test.tsx
git commit -m "feat(web): worker detail page composition"
```

---

## Task 10: Routing and list navigation

**Files:**
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/workers/WorkersGrid.tsx`, `web/src/workers/WorkersTable.tsx`
- Modify: `web/src/workers/WorkersGrid.test.tsx`, `web/src/workers/WorkersTable.test.tsx`, `web/src/workers/WorkersPage.test.tsx`

- [ ] **Step 1: Add the failing navigation tests and wrap existing renders in a router**

Adding `Link`s to the grid/table requires a router in their tests. Rewrite `web/src/workers/WorkersGrid.test.tsx` to:

```tsx
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { WorkersGrid } from './WorkersGrid'
import type { Worker } from './api'

function worker(over: Partial<Worker>): Worker {
  return {
    id: 'w1', name: 'render-01', hostname: 'h', cpu_cores: 16, ram_gb: 128,
    gpu_count: 1, gpu_model: 'RTX 4090', os: 'linux', max_slots: 4,
    labels: { pool: 'render' }, status: 'online',
    last_seen_at: '2026-06-03T12:00:00Z', ...over,
  }
}

function renderGrid(workers: Worker[]) {
  return render(
    <MemoryRouter>
      <WorkersGrid workers={workers} />
    </MemoryRouter>,
  )
}

test('renders a card with name, status, spec, slots, and label chip', () => {
  renderGrid([worker({})])
  expect(screen.getByText('render-01')).toBeInTheDocument()
  expect(screen.getByText('ONLINE')).toBeInTheDocument()
  expect(screen.getByText('16c · 128GB · RTX 4090')).toBeInTheDocument()
  expect(screen.getByText('4 slots')).toBeInTheDocument()
  expect(screen.getByText('pool=render')).toBeInTheDocument()
})

test('dims offline workers', () => {
  const { container } = renderGrid([worker({ id: 'o', name: 'off-01', status: 'offline' })])
  expect(container.querySelector('.opacity-\\[0\\.55\\]')).not.toBeNull()
})

test('each card links to the worker detail page', () => {
  renderGrid([worker({ id: 'w9', name: 'render-09' })])
  expect(screen.getByRole('link', { name: /render-09/ })).toHaveAttribute('href', '/workers/w9')
})
```

Rewrite `web/src/workers/WorkersTable.test.tsx` to wrap renders in a router and add a link test. Replace the file with:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { expect, test, vi } from 'vitest'
import { WorkersTable, type SortField } from './WorkersTable'
import type { Worker, WorkerSort } from './api'

function worker(over: Partial<Worker>): Worker {
  return {
    id: 'w1', name: 'render-01', hostname: 'h', cpu_cores: 16, ram_gb: 128,
    gpu_count: 0, gpu_model: '', os: 'linux', max_slots: 4,
    labels: null, status: 'online', last_seen_at: '2026-06-03T12:00:00Z', ...over,
  }
}

function renderTable(
  workers: Worker[],
  sort: WorkerSort = '-created_at',
  onSort: (f: SortField) => void = () => {},
) {
  return render(
    <MemoryRouter>
      <WorkersTable workers={workers} sort={sort} onSort={onSort} />
    </MemoryRouter>,
  )
}

test('renders a row and calls onSort when a sortable header is clicked', async () => {
  const onSort = vi.fn()
  renderTable([worker({})], '-created_at', onSort)
  expect(screen.getByText('render-01')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /name/i }))
  expect(onSort).toHaveBeenCalledWith('name')
})

test('shows a descending caret on the active sort column', () => {
  renderTable([worker({})], '-name')
  expect(screen.getByRole('button', { name: /name ▼/i })).toBeInTheDocument()
})

test('exposes aria-sort on the active sortable header and "none" on the rest', () => {
  renderTable([worker({})], '-last_seen_at')
  expect(screen.getByRole('button', { name: /last seen/i })).toHaveAttribute('aria-sort', 'descending')
  expect(screen.getByRole('button', { name: /name/i })).toHaveAttribute('aria-sort', 'none')
  expect(screen.getByRole('button', { name: /status/i })).toHaveAttribute('aria-sort', 'none')
})

test('reports ascending aria-sort when the active sort is ascending', () => {
  renderTable([worker({})], 'name')
  expect(screen.getByRole('button', { name: /name/i })).toHaveAttribute('aria-sort', 'ascending')
})

test('each row links to the worker detail page', () => {
  renderTable([worker({ id: 'w9', name: 'render-09' })])
  expect(screen.getByRole('link', { name: /render-09/ })).toHaveAttribute('href', '/workers/w9')
})
```

In `web/src/workers/WorkersPage.test.tsx`, add the router import and a small helper, then replace every `renderWithQuery(<WorkersPage />)` call with `renderPage()`:

Add after the existing imports:

```tsx
import { MemoryRouter } from 'react-router-dom'
```

Add after the `stats` constant (before the first `test(`):

```tsx
function renderPage() {
  return renderWithQuery(
    <MemoryRouter>
      <WorkersPage />
    </MemoryRouter>,
  )
}
```

Then replace each occurrence of `renderWithQuery(<WorkersPage />)` in the file with `renderPage()` (6 occurrences).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/workers/WorkersGrid.test.tsx src/workers/WorkersTable.test.tsx`
Expected: FAIL - the new link tests fail because cards/rows are not yet `Link`s (no element with role `link`).

- [ ] **Step 3: Add the route**

In `web/src/app/router.tsx`, add the import and the dynamic route. Add to the imports:

```tsx
import { WorkerDetailPage } from '../workers/WorkerDetailPage'
```

Add the route immediately after the existing `/workers` route line:

```tsx
        <Route path="/workers" element={<WorkersPage />} />
        <Route path="/workers/:id" element={<WorkerDetailPage />} />
```

- [ ] **Step 4: Make grid cards link to the detail page**

Replace the body of `web/src/workers/WorkersGrid.tsx` with:

```tsx
import { Link } from 'react-router-dom'
import { StatusDot } from './StatusDot'
import { formatRelativeTime, labelChips, livenessView, specLine } from './liveness'
import type { Worker } from './api'

export function WorkersGrid({ workers }: { workers: Worker[] }) {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
      {workers.map((w) => (
        <Link
          key={w.id}
          to={`/workers/${w.id}`}
          className={`block rounded-card border border-border bg-white/5 p-4 backdrop-blur transition hover:border-accent/50 ${livenessView(w.status).dimClass}`}
        >
          <div className="mb-2 flex items-baseline justify-between">
            <span className="font-mono text-[13px] text-fg">{w.name}</span>
            <StatusDot status={w.status} />
          </div>
          <div className="mb-2 font-mono text-[11px] text-fg-mute">{w.max_slots} slots</div>
          {labelChips(w.labels).length > 0 && (
            <div className="mb-2 flex flex-wrap gap-1">
              {labelChips(w.labels).map((c) => (
                <span
                  key={c}
                  className="rounded-full border border-accent/40 bg-accent/10 px-2 py-0.5 font-mono text-[9.5px] text-accent"
                >
                  {c}
                </span>
              ))}
            </div>
          )}
          <div className="mt-2 flex justify-between border-t border-border pt-2 font-mono text-[10px] text-fg-mute">
            <span>{specLine(w)}</span>
            <span>{w.last_seen_at ? formatRelativeTime(w.last_seen_at) : '-'}</span>
          </div>
        </Link>
      ))}
    </div>
  )
}
```

- [ ] **Step 5: Make table rows link to the detail page**

In `web/src/workers/WorkersTable.tsx`, add the import at the top:

```tsx
import { Link } from 'react-router-dom'
```

Then replace the row `<div>` in the `workers.map(...)` block (the element with `key={w.id}`) with a `Link`. Replace this:

```tsx
        <div
          key={w.id}
          className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${livenessView(w.status).dimClass}`}
        >
```

with:

```tsx
        <Link
          key={w.id}
          to={`/workers/${w.id}`}
          className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px] transition hover:bg-white/5 ${livenessView(w.status).dimClass}`}
        >
```

and change the matching closing `</div>` for that row (the one immediately before `))}`) to `</Link>`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `npx vitest run src/workers/WorkersGrid.test.tsx src/workers/WorkersTable.test.tsx src/workers/WorkersPage.test.tsx`
Expected: PASS (including the new link-target tests).

- [ ] **Step 7: Commit**

```bash
git add web/src/app/router.tsx web/src/workers/WorkersGrid.tsx web/src/workers/WorkersTable.tsx web/src/workers/WorkersGrid.test.tsx web/src/workers/WorkersTable.test.tsx web/src/workers/WorkersPage.test.tsx
git commit -m "feat(web): link worker list to detail page and add route"
```

---

## Task 11: Full verification

**Files:** none (verification only).

- [ ] **Step 1: Run the full test suite**

Run (from `web/`): `npm test`
Expected: PASS, all test files green (existing + new).

- [ ] **Step 2: Run the production build**

Run (from `web/`): `npm run build`
Expected: `tsc -b` reports no type errors and `vite build` completes. No unused-import or type errors from the new files.

- [ ] **Step 3: Fix any failures**

If the type-check flags an unused import (e.g. a helper imported but not used), remove it. If a test fails, fix the implementation or test per the failure - do not mark complete until both commands are clean.

- [ ] **Step 4: Confirm no build artifacts are staged**

Run: `git status`
Expected: clean. Do not commit `web/dist/`. If it appears, it is already gitignored; leave it unstaged.

---

## Self-Review (completed by plan author)

**Spec coverage:**
- Routing + nav (clickable list -> `/workers/:id`): Task 10.
- Header / identity, stat cards: Task 9.
- Telemetry charts (CPU/mem, GPU when present), hand-rolled SVG, empty state: Tasks 3, 4, 9.
- Labels (read-only): Task 9.
- Workspaces (admin-only, read-only, hidden for non-admins, no fetch for non-admins): Tasks 7, 8, 9.
- Data hooks at matched cadences (3s/10s/15s): Tasks 5, 6, 7.
- API clients + contract verification: Task 1.
- States (loading, error/retry, 404, empty telemetry): Task 9.
- Tests for every unit incl. navigation: every task.

**Placeholder scan:** No TBD/TODO/"handle edge cases"; every code step shows complete code.

**Type consistency:** `chartPath(values, width, height, max) -> {line, area}` used identically in `MetricChart`. `formatGB(bytes)` used in `WorkerDetailPage`. Hook signatures `useWorker(id, intervalMs?)`, `useWorkerMetrics(id, intervalMs?)`, `useWorkerWorkspaces(id, intervalMs?)` consistent across hook files, tests, and page. API client names `getWorker` / `getWorkerMetrics` / `listWorkerWorkspaces` consistent across `api.ts`, tests, and hooks. TS field names verified against Go JSON tags in Task 1 Step 5.
