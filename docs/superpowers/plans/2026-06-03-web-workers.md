# Web Front End: Workers Slice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Workers list page (grid + table, server-side sort, ~3s polling) and, with it, establish the repo's first TanStack Query data-fetching foundation.

**Architecture:** A new `web/src/workers/` feature module (thin typed `api.ts`, a polled `useWorkers` query hook, pure presentation helpers in `liveness.ts`, and presentational components). TanStack Query v5 is added at the React root via `QueryClientProvider`; the existing `AuthProvider` and `apiFetch` client are untouched. The page polls the first page (50 workers) of `GET /v1/workers` every 3s, derives status presentation client-side from the server-authoritative `status` field, and renders either a card grid or a sortable table. No backend changes.

**Tech Stack:** React 18, TypeScript, Vite, Tailwind v4 (CSS-first tokens), `@tanstack/react-query` v5, Vitest + React Testing Library + MSW.

**Source spec:** [docs/superpowers/specs/2026-06-03-web-workers-design.md](../specs/2026-06-03-web-workers-design.md)

---

## File Structure

**Created:**
- `web/src/lib/queryClient.ts` - shared `QueryClient` instance + defaults
- `web/src/test/renderWithQuery.tsx` - test helper wrapping a component in a fresh `QueryClientProvider`
- `web/src/workers/api.ts` - `Worker`/`WorkersPage`/`WorkerSort`/`WorkerStatus` types + `listWorkers`
- `web/src/workers/api.test.ts`
- `web/src/workers/liveness.ts` - pure helpers: `livenessView`, `formatRelativeTime`, `specLine`, `labelChips`
- `web/src/workers/liveness.test.ts`
- `web/src/workers/useWorkers.ts` - polled `useQuery` hook
- `web/src/workers/useWorkers.test.tsx`
- `web/src/workers/StatusDot.tsx` - dot + status label
- `web/src/workers/WorkersGrid.tsx` - card grid (presentational)
- `web/src/workers/WorkersGrid.test.tsx`
- `web/src/workers/WorkersTable.tsx` - dense sortable table (presentational)
- `web/src/workers/WorkersTable.test.tsx`
- `web/src/workers/WorkersPage.tsx` - composes hook + toggle + summary + states
- `web/src/workers/WorkersPage.test.tsx`

**Modified:**
- `web/package.json` - add `@tanstack/react-query`
- `web/src/App.tsx` - wrap tree in `QueryClientProvider`
- `web/src/app/router.tsx` - point `/workers` at `WorkersPage`

**Conventions to follow (already in the codebase):**
- Glass panel: `rounded-card border border-border bg-white/5 backdrop-blur`.
- Theme color utilities are literal Tailwind classes mapping to CSS tokens: `text-ok`, `text-warn`, `text-err`, `text-fg-mute`, `text-fg-dim`, `text-accent`, `bg-accent`, `bg-bg`, `border-border`. **Never build class names dynamically** (e.g. `` `bg-${x}` ``) - Tailwind v4 only includes literals it can see in source.
- Tests use the shared MSW `server` from `./test/setup-helpers` and add per-test handlers with `server.use(...)`. `onUnhandledRequest` is `'error'`, so every request a test triggers must have a handler.
- `apiFetch` already prefixes `/v1`, attaches the bearer token, and throws `ApiError` on the `{error}` envelope.

---

### Task 1: Add TanStack Query, the QueryClient, the provider, and the test helper

**Files:**
- Modify: `web/package.json`
- Create: `web/src/lib/queryClient.ts`
- Modify: `web/src/App.tsx`
- Create: `web/src/test/renderWithQuery.tsx`

- [ ] **Step 1: Install the dependency**

Run:
```bash
cd web && npm install @tanstack/react-query@^5.62.0
```
Expected: `package.json` gains `"@tanstack/react-query": "^5.62.0"` under `dependencies` and `package-lock.json` updates with no errors.

- [ ] **Step 2: Create the shared QueryClient**

Create `web/src/lib/queryClient.ts`:
```ts
import { QueryClient } from '@tanstack/react-query'

// Shared client for the app. Polling (refetchInterval) and keepPreviousData are
// set per-hook (see workers/useWorkers.ts), not globally, so non-polled queries
// added later are unaffected. The existing 401 interceptor in lib/api.ts handles
// auth redirects; the client only needs sane retry/staleness defaults.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 0,
      retry: 1,
    },
  },
})
```

- [ ] **Step 3: Wrap the app in QueryClientProvider**

Modify `web/src/App.tsx` - add the import and wrap the existing tree:
```tsx
import { useEffect } from 'react'
import { BrowserRouter, useNavigate } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { AuthProvider } from './auth/AuthProvider'
import { onUnauthorized } from './lib/api'
import { queryClient } from './lib/queryClient'
import { AppRoutes } from './app/router'

function UnauthorizedRedirect() {
  const navigate = useNavigate()
  useEffect(() => onUnauthorized(() => navigate('/auth')), [navigate])
  return null
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <AuthProvider>
          <UnauthorizedRedirect />
          <AppRoutes />
        </AuthProvider>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
```

- [ ] **Step 4: Create the test render helper**

Create `web/src/test/renderWithQuery.tsx`:
```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import type { ReactElement } from 'react'

// Renders a component inside a fresh QueryClient with retries disabled, so error
// cases surface immediately and state never leaks between tests.
export function renderWithQuery(ui: ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}
```

- [ ] **Step 5: Verify the existing suite still passes**

Run:
```bash
cd web && npm test
```
Expected: all existing tests PASS (the provider addition is non-breaking). If TypeScript complains about an unused import, re-check Step 3.

- [ ] **Step 6: Commit**

```bash
git add web/package.json web/package-lock.json web/src/lib/queryClient.ts web/src/App.tsx web/src/test/renderWithQuery.tsx
git commit -m "feat(web): add TanStack Query client and provider"
```

---

### Task 2: Pure presentation helpers (`liveness.ts`)

**Files:**
- Create: `web/src/workers/liveness.ts`
- Test: `web/src/workers/liveness.test.ts`

These are pure functions with no React or network dependency. `livenessView` maps the server-authoritative status to presentation; the client does **not** recompute staleness.

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/liveness.test.ts`:
```ts
import { describe, expect, test } from 'vitest'
import { livenessView, formatRelativeTime, specLine, labelChips } from './liveness'
import type { Worker } from './api'

describe('livenessView', () => {
  test('online is green, not dimmed', () => {
    expect(livenessView('online')).toEqual({
      label: 'ONLINE', dotClass: 'bg-ok', textClass: 'text-ok', dimClass: '',
    })
  })
  test('stale is amber, not dimmed', () => {
    expect(livenessView('stale')).toEqual({
      label: 'STALE', dotClass: 'bg-warn', textClass: 'text-warn', dimClass: '',
    })
  })
  test('disabled is grey and dimmed', () => {
    expect(livenessView('disabled')).toEqual({
      label: 'DISABLED', dotClass: 'bg-fg-mute', textClass: 'text-fg-mute', dimClass: 'opacity-70',
    })
  })
  test('offline is red and most dimmed', () => {
    expect(livenessView('offline')).toEqual({
      label: 'OFFLINE', dotClass: 'bg-err', textClass: 'text-err', dimClass: 'opacity-[0.55]',
    })
  })
})

describe('formatRelativeTime', () => {
  const now = new Date('2026-06-03T12:00:00Z')
  test('seconds', () => {
    expect(formatRelativeTime('2026-06-03T11:59:48Z', now)).toBe('12s ago')
  })
  test('minutes', () => {
    expect(formatRelativeTime('2026-06-03T11:55:00Z', now)).toBe('5m ago')
  })
  test('hours', () => {
    expect(formatRelativeTime('2026-06-03T09:00:00Z', now)).toBe('3h ago')
  })
  test('days', () => {
    expect(formatRelativeTime('2026-06-01T12:00:00Z', now)).toBe('2d ago')
  })
  test('future clamps to 0s', () => {
    expect(formatRelativeTime('2026-06-03T12:00:30Z', now)).toBe('0s ago')
  })
})

function worker(over: Partial<Worker>): Worker {
  return {
    id: 'w', name: 'n', hostname: 'h', cpu_cores: 8, ram_gb: 64,
    gpu_count: 0, gpu_model: '', os: 'linux', max_slots: 4,
    labels: null, status: 'online', ...over,
  }
}

describe('specLine', () => {
  test('shows the GPU model when the worker has a GPU', () => {
    expect(specLine(worker({ gpu_count: 1, gpu_model: 'RTX 4090' }))).toBe('RTX 4090')
  })
  test('falls back to cpu/ram when there is no GPU', () => {
    expect(specLine(worker({ gpu_count: 0, cpu_cores: 16, ram_gb: 128 }))).toBe('16c · 128GB')
  })
})

describe('labelChips', () => {
  test('null labels yield no chips', () => {
    expect(labelChips(null)).toEqual([])
  })
  test('key=value pairs, bare key when value empty', () => {
    expect(labelChips({ pool: 'render', gpu: '' })).toEqual(['pool=render', 'gpu'])
  })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
cd web && npx vitest run src/workers/liveness.test.ts
```
Expected: FAIL - `Cannot find module './liveness'` (and `./api`, created in Task 3; this is acceptable - the next step creates `liveness.ts`, and the `Worker` type import resolves once Task 3 lands. To unblock now, see Step 3's note).

- [ ] **Step 3: Write the implementation**

Create `web/src/workers/liveness.ts`:
```ts
import type { Worker, WorkerStatus } from './api'

export interface LivenessView {
  label: string
  dotClass: string
  textClass: string
  dimClass: string
}

// Maps the server-authoritative status to presentation. Class strings are
// literals so Tailwind v4 includes them.
export function livenessView(status: WorkerStatus): LivenessView {
  switch (status) {
    case 'online':
      return { label: 'ONLINE', dotClass: 'bg-ok', textClass: 'text-ok', dimClass: '' }
    case 'stale':
      return { label: 'STALE', dotClass: 'bg-warn', textClass: 'text-warn', dimClass: '' }
    case 'disabled':
      return { label: 'DISABLED', dotClass: 'bg-fg-mute', textClass: 'text-fg-mute', dimClass: 'opacity-70' }
    case 'offline':
      return { label: 'OFFLINE', dotClass: 'bg-err', textClass: 'text-err', dimClass: 'opacity-[0.55]' }
  }
}

export function formatRelativeTime(iso: string, now: Date = new Date()): string {
  const secs = Math.max(0, Math.round((now.getTime() - new Date(iso).getTime()) / 1000))
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

export function specLine(w: Worker): string {
  return w.gpu_count > 0 && w.gpu_model ? w.gpu_model : `${w.cpu_cores}c · ${w.ram_gb}GB`
}

export function labelChips(labels: Record<string, string> | null): string[] {
  if (!labels) return []
  return Object.entries(labels).map(([k, v]) => (v ? `${k}=${v}` : k))
}
```

> **Note:** This test depends on the `Worker`/`WorkerStatus` types from Task 3's `api.ts`. If executing strictly in order, create `web/src/workers/api.ts` (Task 3, Step 3) first, then return here. The two files are co-dependent only at the type level.

- [ ] **Step 4: Run the test to verify it passes**

Run:
```bash
cd web && npx vitest run src/workers/liveness.test.ts
```
Expected: PASS (requires `api.ts` to exist - see note above).

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/liveness.ts web/src/workers/liveness.test.ts
git commit -m "feat(web): add worker liveness and formatting helpers"
```

---

### Task 3: API types and `listWorkers`

**Files:**
- Create: `web/src/workers/api.ts`
- Test: `web/src/workers/api.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/api.test.ts`:
```ts
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import { listWorkers, type WorkersPage } from './api'

const emptyPage: WorkersPage = { items: [], next_cursor: '', total: 0 }

test('requests the first page with the given sort and limit=50', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/workers', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listWorkers('name')
  expect(captured?.get('sort')).toBe('name')
  expect(captured?.get('limit')).toBe('50')
  expect(captured?.get('cursor')).toBeNull()
})

test('parses the page payload', async () => {
  server.use(
    http.get('/v1/workers', () =>
      HttpResponse.json({
        items: [{ id: 'w1', name: 'render-01', status: 'online' }],
        next_cursor: 'abc',
        total: 1,
      }),
    ),
  )
  const page = await listWorkers('-created_at')
  expect(page.total).toBe(1)
  expect(page.items[0].name).toBe('render-01')
})

test('throws ApiError on the error envelope', async () => {
  server.use(
    http.get('/v1/workers', () =>
      HttpResponse.json({ error: 'boom' }, { status: 500 }),
    ),
  )
  await expect(listWorkers('-created_at')).rejects.toBeInstanceOf(ApiError)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
cd web && npx vitest run src/workers/api.test.ts
```
Expected: FAIL - `Cannot find module './api'`.

- [ ] **Step 3: Write the implementation**

Create `web/src/workers/api.ts`:
```ts
import { apiFetch } from '../lib/api'

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
  | '-created_at'
  | 'created_at'
  | 'name'
  | '-name'
  | 'status'
  | '-status'
  | 'last_seen_at'
  | '-last_seen_at'

// First page only. limit=50 is the server default, passed explicitly so the
// client's page size is self-documenting and decoupled from server changes.
export function listWorkers(sort: WorkerSort): Promise<WorkersPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  return apiFetch<WorkersPage>(`/workers?${q}`)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:
```bash
cd web && npx vitest run src/workers/api.test.ts src/workers/liveness.test.ts
```
Expected: both files PASS (liveness now resolves its `Worker` import).

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/api.ts web/src/workers/api.test.ts
git commit -m "feat(web): add workers API types and listWorkers"
```

---

### Task 4: The polled `useWorkers` hook

**Files:**
- Create: `web/src/workers/useWorkers.ts`
- Test: `web/src/workers/useWorkers.test.tsx`

The hook polls on an interval. To keep the polling test fast and deterministic, `intervalMs` is a parameter defaulting to 3000 - production callers omit it; the test passes a small value and uses real timers.

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/useWorkers.test.tsx`:
```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkers } from './useWorkers'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches workers and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/workers', () => {
      count++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )

  renderHook(() => useWorkers('-created_at', 20), { wrapper })

  await waitFor(() => expect(count).toBe(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
cd web && npx vitest run src/workers/useWorkers.test.tsx
```
Expected: FAIL - `Cannot find module './useWorkers'`.

- [ ] **Step 3: Write the implementation**

Create `web/src/workers/useWorkers.ts`:
```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listWorkers, type WorkerSort } from './api'

// Polls the first page of workers. keepPreviousData keeps the old rows visible
// while a new sort loads and between polls, so the page never flashes empty.
// intervalMs defaults to 3000; tests inject a small value.
export function useWorkers(sort: WorkerSort, intervalMs = 3000) {
  return useQuery({
    queryKey: ['workers', sort],
    queryFn: () => listWorkers(sort),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:
```bash
cd web && npx vitest run src/workers/useWorkers.test.tsx
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/useWorkers.ts web/src/workers/useWorkers.test.tsx
git commit -m "feat(web): add polled useWorkers query hook"
```

---

### Task 5: `StatusDot` and `WorkersGrid`

**Files:**
- Create: `web/src/workers/StatusDot.tsx`
- Create: `web/src/workers/WorkersGrid.tsx`
- Test: `web/src/workers/WorkersGrid.test.tsx`

Both are presentational - they take a `Worker[]` prop and render. No network or Query.

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/WorkersGrid.test.tsx`:
```tsx
import { render, screen } from '@testing-library/react'
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

test('renders a card with name, status, spec, slots, and label chip', () => {
  render(<WorkersGrid workers={[worker({})]} />)
  expect(screen.getByText('render-01')).toBeInTheDocument()
  expect(screen.getByText('ONLINE')).toBeInTheDocument()
  expect(screen.getByText('RTX 4090')).toBeInTheDocument()
  expect(screen.getByText('4 slots')).toBeInTheDocument()
  expect(screen.getByText('pool=render')).toBeInTheDocument()
})

test('dims offline workers', () => {
  const { container } = render(<WorkersGrid workers={[worker({ id: 'o', name: 'off-01', status: 'offline' })]} />)
  expect(container.querySelector('.opacity-\\[0\\.55\\]')).not.toBeNull()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
cd web && npx vitest run src/workers/WorkersGrid.test.tsx
```
Expected: FAIL - `Cannot find module './WorkersGrid'`.

- [ ] **Step 3: Write `StatusDot`**

Create `web/src/workers/StatusDot.tsx`:
```tsx
import { livenessView } from './liveness'
import type { WorkerStatus } from './api'

export function StatusDot({ status }: { status: WorkerStatus }) {
  const v = livenessView(status)
  return (
    <span className={`inline-flex items-center gap-1.5 font-mono text-[10px] tracking-wider ${v.textClass}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${v.dotClass}`} />
      {v.label}
    </span>
  )
}
```

- [ ] **Step 4: Write `WorkersGrid`**

Create `web/src/workers/WorkersGrid.tsx`:
```tsx
import { StatusDot } from './StatusDot'
import { formatRelativeTime, labelChips, livenessView, specLine } from './liveness'
import type { Worker } from './api'

export function WorkersGrid({ workers }: { workers: Worker[] }) {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
      {workers.map((w) => (
        <div
          key={w.id}
          className={`rounded-card border border-border bg-white/5 p-4 backdrop-blur ${livenessView(w.status).dimClass}`}
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
            <span>{w.last_seen_at ? formatRelativeTime(w.last_seen_at) : '—'}</span>
          </div>
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run:
```bash
cd web && npx vitest run src/workers/WorkersGrid.test.tsx
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/workers/StatusDot.tsx web/src/workers/WorkersGrid.tsx web/src/workers/WorkersGrid.test.tsx
git commit -m "feat(web): add StatusDot and WorkersGrid"
```

---

### Task 6: `WorkersTable` with sortable headers

**Files:**
- Create: `web/src/workers/WorkersTable.tsx`
- Test: `web/src/workers/WorkersTable.test.tsx`

Presentational: takes `workers`, the current `sort`, and an `onSort(field)` callback. Clicking a sortable header invokes `onSort`; the active column shows a caret.

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/WorkersTable.test.tsx`:
```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { WorkersTable } from './WorkersTable'
import type { Worker } from './api'

function worker(over: Partial<Worker>): Worker {
  return {
    id: 'w1', name: 'render-01', hostname: 'h', cpu_cores: 16, ram_gb: 128,
    gpu_count: 0, gpu_model: '', os: 'linux', max_slots: 4,
    labels: null, status: 'online', last_seen_at: '2026-06-03T12:00:00Z', ...over,
  }
}

test('renders a row and calls onSort when a sortable header is clicked', async () => {
  const onSort = vi.fn()
  render(<WorkersTable workers={[worker({})]} sort="-created_at" onSort={onSort} />)
  expect(screen.getByText('render-01')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /name/i }))
  expect(onSort).toHaveBeenCalledWith('name')
})

test('shows a descending caret on the active sort column', () => {
  render(<WorkersTable workers={[worker({})]} sort="-name" onSort={() => {}} />)
  expect(screen.getByRole('button', { name: /name ▼/i })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
cd web && npx vitest run src/workers/WorkersTable.test.tsx
```
Expected: FAIL - `Cannot find module './WorkersTable'`.

- [ ] **Step 3: Write the implementation**

Create `web/src/workers/WorkersTable.tsx`:
```tsx
import { StatusDot } from './StatusDot'
import { formatRelativeTime, labelChips, livenessView, specLine } from './liveness'
import type { Worker, WorkerSort } from './api'

export type SortField = 'name' | 'status' | 'last_seen_at'

const COLS = 'grid grid-cols-[1fr_120px_70px_140px_1.2fr_120px]'

function caret(field: SortField, sort: WorkerSort): string {
  if (sort.replace('-', '') !== field) return ''
  return sort.startsWith('-') ? ' ▼' : ' ▲'
}

export function WorkersTable({
  workers,
  sort,
  onSort,
}: {
  workers: Worker[]
  sort: WorkerSort
  onSort: (field: SortField) => void
}) {
  return (
    <div className="rounded-card border border-border bg-white/5 backdrop-blur">
      <div className={`${COLS} border-b border-border px-4 py-3 font-mono text-[10px] tracking-wider text-fg-mute`}>
        <button type="button" className="text-left" onClick={() => onSort('name')}>
          NAME{caret('name', sort)}
        </button>
        <button type="button" className="text-left" onClick={() => onSort('status')}>
          STATUS{caret('status', sort)}
        </button>
        <span>SLOTS</span>
        <span>SPEC</span>
        <span>LABELS</span>
        <button type="button" className="text-left" onClick={() => onSort('last_seen_at')}>
          LAST SEEN{caret('last_seen_at', sort)}
        </button>
      </div>
      {workers.map((w) => (
        <div
          key={w.id}
          className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${livenessView(w.status).dimClass}`}
        >
          <span className="text-fg">{w.name}</span>
          <span><StatusDot status={w.status} /></span>
          <span className="text-fg-mute">{w.max_slots}</span>
          <span className="text-[10.5px] text-fg-mute">{specLine(w)}</span>
          <span className="flex flex-wrap gap-1">
            {labelChips(w.labels).map((c) => (
              <span
                key={c}
                className="rounded-full border border-accent/40 bg-accent/10 px-1.5 py-0.5 text-[9.5px] text-accent"
              >
                {c}
              </span>
            ))}
          </span>
          <span className="text-fg-mute">
            {w.last_seen_at ? formatRelativeTime(w.last_seen_at) : '—'}
          </span>
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:
```bash
cd web && npx vitest run src/workers/WorkersTable.test.tsx
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/WorkersTable.tsx web/src/workers/WorkersTable.test.tsx
git commit -m "feat(web): add sortable WorkersTable"
```

---

### Task 7: `WorkersPage` (compose hook + toggle + summary + states)

**Files:**
- Create: `web/src/workers/WorkersPage.tsx`
- Test: `web/src/workers/WorkersPage.test.tsx`

Composes everything: the polled hook, grid/table toggle (persisted to `localStorage`), the page-scoped summary strip, the live indicator, and the loading/error/empty states.

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/WorkersPage.test.tsx`:
```tsx
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { WorkersPage } from './WorkersPage'

afterEach(() => localStorage.clear())

const page = {
  items: [
    { id: 'w1', name: 'render-01', hostname: 'h', cpu_cores: 16, ram_gb: 128, gpu_count: 1, gpu_model: 'RTX 4090', os: 'linux', max_slots: 4, labels: null, status: 'online', last_seen_at: '2026-06-03T12:00:00Z' },
    { id: 'w2', name: 'render-02', hostname: 'h', cpu_cores: 8, ram_gb: 64, gpu_count: 0, gpu_model: '', os: 'linux', max_slots: 2, labels: null, status: 'offline' },
  ],
  next_cursor: '',
  total: 2,
}

test('renders workers and the page-scoped summary', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json(page)))
  renderWithQuery(<WorkersPage />)
  expect(await screen.findByText('render-01')).toBeInTheDocument()
  expect(screen.getByText('render-02')).toBeInTheDocument()
  expect(screen.getByText('2 workers')).toBeInTheDocument()
})

test('view toggle switches to the table and persists to localStorage', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json(page)))
  renderWithQuery(<WorkersPage />)
  await screen.findByText('render-01')
  await userEvent.click(screen.getByRole('button', { name: 'Table' }))
  expect(screen.getByRole('button', { name: /name/i })).toBeInTheDocument()
  expect(localStorage.getItem('relay.workers.view')).toBe('table')
})

test('clicking a sort header re-requests with the new sort', async () => {
  const sorts: (string | null)[] = []
  server.use(
    http.get('/v1/workers', ({ request }) => {
      sorts.push(new URL(request.url).searchParams.get('sort'))
      return HttpResponse.json(page)
    }),
  )
  renderWithQuery(<WorkersPage />)
  await screen.findByText('render-01')
  await userEvent.click(screen.getByRole('button', { name: 'Table' }))
  await userEvent.click(screen.getByRole('button', { name: /name/i }))
  await waitFor(() => expect(sorts).toContain('name'))
})

test('shows an error banner with retry, then recovers', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderWithQuery(<WorkersPage />)
  expect(await screen.findByRole('button', { name: /retry/i })).toBeInTheDocument()

  server.use(http.get('/v1/workers', () => HttpResponse.json(page)))
  await userEvent.click(screen.getByRole('button', { name: /retry/i }))
  expect(await screen.findByText('render-01')).toBeInTheDocument()
})

test('shows the empty state when there are no workers', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })))
  renderWithQuery(<WorkersPage />)
  expect(await screen.findByText(/no workers enrolled yet/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
cd web && npx vitest run src/workers/WorkersPage.test.tsx
```
Expected: FAIL - `Cannot find module './WorkersPage'`.

- [ ] **Step 3: Write the implementation**

Create `web/src/workers/WorkersPage.tsx`:
```tsx
import { useState } from 'react'
import { Button } from '../components/Button'
import { useWorkers } from './useWorkers'
import { WorkersGrid } from './WorkersGrid'
import { WorkersTable, type SortField } from './WorkersTable'
import type { Worker, WorkerSort, WorkerStatus } from './api'

type View = 'grid' | 'table'

const VIEW_KEY = 'relay.workers.view'

function loadView(): View {
  return localStorage.getItem(VIEW_KEY) === 'table' ? 'table' : 'grid'
}

function toggleSort(field: SortField, current: WorkerSort): WorkerSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as WorkerSort
  }
  return field
}

function countByStatus(workers: Worker[]): Record<WorkerStatus, number> {
  const counts: Record<WorkerStatus, number> = { online: 0, stale: 0, offline: 0, disabled: 0 }
  for (const w of workers) counts[w.status]++
  return counts
}

export function WorkersPage() {
  const [sort, setSort] = useState<WorkerSort>('-created_at')
  const [view, setView] = useState<View>(loadView)
  const { data, error, isLoading, isFetching, refetch } = useWorkers(sort)

  function chooseView(v: View) {
    setView(v)
    localStorage.setItem(VIEW_KEY, v)
  }

  if (isLoading && !data) {
    return (
      <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="h-28 rounded-card border border-border bg-white/5" />
        ))}
      </div>
    )
  }

  if (error && !data) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
        <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
        <Button className="w-auto px-4" onClick={() => refetch()}>
          Retry
        </Button>
      </div>
    )
  }

  const workers = data?.items ?? []
  if (workers.length === 0) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
        No workers enrolled yet.
      </div>
    )
  }

  const counts = countByStatus(workers)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">FLEET</div>
          <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
        </div>
        <div
          className="flex gap-4 font-mono text-[11px] text-fg-mute"
          title="Counts for the loaded page"
        >
          <span><b className="text-ok">{counts.online}</b> ONLINE</span>
          <span><b className="text-warn">{counts.stale}</b> STALE</span>
          <span><b className="text-fg-mute">{counts.disabled}</b> DISABLED</span>
          <span><b className="text-err">{counts.offline}</b> OFFLINE</span>
          <span className="text-fg-dim">· {data?.total ?? workers.length} workers</span>
        </div>
        <div className="ml-auto flex items-center gap-3">
          <span className="font-mono text-[10px] text-fg-mute">
            <span className={isFetching ? 'text-ok' : 'text-fg-dim'}>●</span> live · auto-refreshing
          </span>
          <div className="flex rounded-full border border-border p-0.5">
            {(['grid', 'table'] as View[]).map((v) => (
              <button
                key={v}
                type="button"
                onClick={() => chooseView(v)}
                className={`rounded-full px-3 py-1 text-[12px] ${view === v ? 'bg-accent text-bg' : 'text-fg-mute'}`}
              >
                {v === 'grid' ? 'Grid' : 'Table'}
              </button>
            ))}
          </div>
        </div>
      </div>

      {view === 'grid' ? (
        <WorkersGrid workers={workers} />
      ) : (
        <WorkersTable workers={workers} sort={sort} onSort={(f) => setSort((cur) => toggleSort(f, cur))} />
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:
```bash
cd web && npx vitest run src/workers/WorkersPage.test.tsx
```
Expected: PASS (all five tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/WorkersPage.tsx web/src/workers/WorkersPage.test.tsx
git commit -m "feat(web): add WorkersPage with grid/table toggle and polling states"
```

---

### Task 8: Wire the route and verify the whole slice

**Files:**
- Modify: `web/src/app/router.tsx`

- [ ] **Step 1: Point `/workers` at `WorkersPage`**

Modify `web/src/app/router.tsx` - add the import and replace the `/workers` element:
```tsx
import { Navigate, Route, Routes } from 'react-router-dom'
import { LoginScreen } from '../auth/LoginScreen'
import { RegisterScreen } from '../auth/RegisterScreen'
import { JobsPlaceholder } from './JobsPlaceholder'
import { WorkersPage } from '../workers/WorkersPage'
import { ProtectedRoute } from './ProtectedRoute'
import { PublicOnlyRoute } from './PublicOnlyRoute'

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<PublicOnlyRoute />}>
        <Route path="/auth" element={<LoginScreen />} />
        <Route path="/register" element={<RegisterScreen />} />
      </Route>
      <Route element={<ProtectedRoute />}>
        <Route path="/jobs" element={<JobsPlaceholder />} />
        <Route path="/workers" element={<WorkersPage />} />
        <Route path="/schedules" element={<JobsPlaceholder />} />
        <Route path="/admin" element={<JobsPlaceholder />} />
        <Route path="/profile/*" element={<JobsPlaceholder />} />
      </Route>
      <Route path="*" element={<Navigate to="/jobs" replace />} />
    </Routes>
  )
}
```

- [ ] **Step 2: Run the full front-end suite**

Run:
```bash
cd web && npm test
```
Expected: ALL tests PASS (existing auth/shell tests plus the new workers tests).

- [ ] **Step 3: Type-check and production build**

Run:
```bash
cd web && npm run build
```
Expected: `tsc -b` reports no type errors and `vite build` writes `web/dist/` with no errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/app/router.tsx
git commit -m "feat(web): mount Workers page at /workers"
```

---

## Self-Review

**Spec coverage:**
- List page only, detail deferred → Tasks 5-7; rows/cards are non-interactive (no click handlers). ✓
- List-endpoint data only, no sparklines → grid/table render only list fields. ✓
- Grid + table with toggle → Tasks 5, 6, 7. ✓
- First page (limit=50), polled ~3s → Task 3 (`limit: '50'`), Task 4 (`refetchInterval`). ✓
- Server-side sort via clickable headers → Task 6 + Task 7 `toggleSort`. ✓
- Feature module layout → matches the spec's `workers/` tree. ✓
- TanStack Query introduced; auth untouched → Task 1 wraps the tree, `AuthProvider` unchanged. ✓
- Status taxonomy online/stale/offline/disabled → `WorkerStatus` type + `livenessView`. ✓
- `keepPreviousData` no-flash → Task 4. ✓
- Page-scoped summary + total → Task 7 `countByStatus` + `data.total` with the "loaded page" title. ✓
- In-page live indicator → Task 7 `isFetching` dot. ✓
- View persisted to `relay.workers.view` → Task 7. ✓
- Error banner + Retry, empty state, skeleton → Task 7. ✓
- Tests: liveness unit, api MSW, polling, page integration (sort/toggle/error/empty) → Tasks 2-7. ✓
- No backend changes → none in plan. ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code. The only forward reference is the documented Task 2 ↔ Task 3 type co-dependency, with explicit instructions to create `api.ts` first if executing strictly in order. ✓

**Type consistency:** `WorkerStatus`, `Worker`, `WorkersPage`, `WorkerSort` defined in Task 3 and imported consistently. `SortField` defined in Task 6 (`WorkersTable.tsx`) and imported by Task 7. `livenessView`/`formatRelativeTime`/`specLine`/`labelChips` signatures match across producer (Task 2) and consumers (Tasks 5-6). `useWorkers(sort, intervalMs?)` matches its call site (Task 7 omits `intervalMs`; test passes it). `VIEW_KEY` value `'relay.workers.view'` matches the test assertion. ✓
