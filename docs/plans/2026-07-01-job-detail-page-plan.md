# Job Detail Page and Row-Click Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the `/jobs/:id` job detail page (header, progress, task DAG, tasks table, Spec/Log tabs) and make `JobsTable` rows link to it, consuming only existing GET endpoints.

**Architecture:** A React route component (`JobDetailPage`) mirrors `WorkerDetailPage`: a polling `useJob(['job', id])` hook feeds the header/progress/DAG/tasks table; a selected-task state drives a tabbed right pane whose Log tab lazily fetches `GET /v1/tasks/:id/logs` only when active. Progress is derived client-side from the `tasks[]` array (the detail endpoint omits `total_tasks`/`done_tasks`/`started_at`/`finished_at`). Task status uses a separate `TaskStatus` vocabulary, never widening `JobStatus`. The DAG is computed by a pure `dagLayout` helper (Kahn-style longest-path layering) and rendered as accessible SVG.

**Tech Stack:** React 18, TypeScript, `@tanstack/react-query` v5, `react-router-dom` v7, Tailwind (existing token classes), Vitest + Testing Library + MSW. All work is under `web/`.

---

## Slice independence declaration

**This is a frontend-only slice. It is INDEPENDENT of any backend slice and requires no backend, store, SQL, proto, or migration change.** Every endpoint it consumes already ships and was verified against current code:

- `GET /v1/jobs/{id}` -> `internal/api/jobs.go` `handleGetJob` -> `toJobResponse`. Returns `id, name, priority, status, submitted_by, submitted_by_email?, labels, tasks[], created_at, updated_at`. It does **NOT** populate `total_tasks`/`done_tasks`/`started_at`/`finished_at`/`scheduled_job_id`/`scheduled_job_name` (those are list-only enrichment in `applyJobEnrichment`, never called by `handleGetJob`).
- Each `tasks[]` entry (`toTaskResponse`): `id, name, status, commands, env, requires, timeout_seconds, retries, retry_count, depends_on?, worker_id?`. `depends_on` is an array of task **names** (resolved server-side from dependency UUIDs via `uuidToName`), present only when non-empty (`omitempty`).
- `GET /v1/tasks/{id}/logs` -> `internal/api/tasks.go` `handleGetTaskLogs`. Returns `{ items: [{seq, stream, content, created_at}], next_seq, total }`. Static GET, seq-paginated, no SSE. 404 if task missing.

Because there is no backend dependency, this whole plan is a single slice. There is no Phase 3 parallelism to declare across a frontend/backend boundary; if the conductor splits work, all tasks below belong to the frontend engineer.

## Correctness points the engineer MUST NOT miss

Each is a spec requirement. Getting any of these wrong is a defect even if tests are green:

1. **Do not read progress fields off the detail response.** `GET /v1/jobs/{id}` does not return `total_tasks`/`done_tasks`/`started_at`/`finished_at`. Derive progress from `tasks[]`: `done = tasks.filter(t => t.status === 'done').length`, `total = tasks.length`.
2. **Task status vocabulary differs from job status.** Tasks are `pending | dispatched | running | done | failed | timed_out`; jobs are `pending | running | done | failed | cancelled`. Add a **separate** `TaskStatus` type and `taskStatusColor`. Do **NOT** widen `JobStatus` or reuse `statusColor` from `status.ts`.
3. **`depends_on` is task NAMES, not IDs, and the DAG needs no extra fetch.** Build nodes from `tasks[].name` and edges `dep -> task` for every `dep` in `task.depends_on`. No `GET /v1/tasks/:id` calls for the DAG.
4. **The Log tab is static fetch-once.** No `EventSource`, no SSE, no `refetchInterval`, no follow toggle, no auto-scroll-to-tail. It must NOT fetch while the Spec tab is active: the log query is `enabled` only when `selectedTaskId !== '' && activeTab === 'log'`.
5. **Leave the header actions slot empty.** Render a right-aligned reserved `<div>` region but no Cancel/Retry/Submit button and no `DELETE /v1/jobs/:id` wiring. Those are deferred to a separate slice.
6. **Default tab is Spec** (always has content), not Log.

## File structure

New files (all under `web/src/jobs/`):

| File | Responsibility |
| --- | --- |
| `taskStatus.ts` | `TaskStatus` union + `taskStatusColor(status): {text, dot}` covering all six task statuses. |
| `dagLayout.ts` | Pure `dagLayout(tasks)` -> `{ nodes, edges }` via Kahn longest-path layering. |
| `useJob.ts` | `useQuery(['job', id])` polling hook for the detail response. |
| `useTaskLogs.ts` | `useQuery(['task-logs', taskId])` for the log tab, `enabled` gated, no interval. |
| `TaskDag.tsx` | SVG DAG strip from `dagLayout`, `role="img"` + `aria-label`. |
| `SpecTab.tsx` | Renders selected task's commands / env / requires. |
| `LogTab.tsx` | Renders the static log for the selected task. |
| `TasksTable.tsx` | Tasks table; rows **select** a task (not navigation), `aria-selected`. |
| `JobDetailPage.tsx` | Route shell: loading/404/error/back-link, header, 55/45 split, tabs, selected-task state. |

New test files (under `web/src/jobs/`):

- `taskStatus.test.ts`, `dagLayout.test.ts`, `useTaskLogs.test.tsx`, `TaskDag.test.tsx`, `TasksTable.test.tsx`, `SpecTab.test.tsx`, `LogTab.test.tsx`, `JobDetailPage.test.tsx`.

Modified files:

- `web/src/jobs/api.ts` - add `TaskStatus` re-export is not needed; add `JobDetail`, `TaskDetail`, `LogEntry`, `TaskLogPage` types plus `getJob` and `getTaskLogs` fetchers.
- `web/src/app/router.tsx` - add `<Route path="/jobs/:id" element={<JobDetailPage />} />`.
- `web/src/jobs/JobsTable.tsx` - wrap the job name in a `<Link to={`/jobs/${j.id}`}>`.

## Sequencing note (avoid shared-file conflicts)

Three tasks touch already-existing files: Task 1 edits `api.ts`, Task 9 edits `router.tsx`, Task 10 edits `JobsTable.tsx`. They edit different files, so ordering between them is only about dependency, not conflict. All new-file tasks (2-8) are independent of each other except that Task 7/8 import the types/helpers from Tasks 1-6. Execute in the numbered order below.

## Conventions to follow (verified in-repo)

- Fetchers: `apiFetch<T>('/path')` from `web/src/lib/api.ts` prefixes `/v1` and throws `ApiError(status, code, msg)`. See `web/src/jobs/api.ts` `listJobs`.
- Detail hook: `useQuery` with `refetchInterval` defaulting to 3000, `placeholderData: keepPreviousData`, `intervalMs` param for test injection. See `web/src/workers/useWorker.ts`.
- Detail-page shell: `useParams()`, `isLoading && !data` skeleton, `error && !data` with `error instanceof ApiError && error.status === 404` split, back-`Link`, `!data` -> null. See `web/src/workers/WorkerDetailPage.tsx`.
- Row-click nav: name cell wrapped in `<Link to={...} className="text-fg hover:text-accent">`. See `web/src/workers/WorkersTable.tsx:57-61`.
- Tailwind tokens already in use: `border-border`, `bg-white/5`, `rounded-card`, `text-fg`, `text-fg-mute`, `text-fg-dim`, `font-mono`, `text-ok`, `text-warn`, `text-err`, `text-accent`, `bg-ok`, `bg-warn`, `bg-err`, `bg-accent`, `bg-fg-mute`.
- Tests: MSW `server` from `../test/setup-helpers`; `setToken('test-token')` + `AuthProvider` when a component reads auth; `new QueryClient({ defaultOptions: { queries: { retry: false } } })`. See `web/src/workers/WorkerDetailPage.test.tsx`.
- Run the web test suite from `web/`: `cd web && npx vitest run <path>`.

---

### Task 1: Detail API types and fetchers

**Files:**
- Modify: `web/src/jobs/api.ts` (append after `getJobStats`, currently ends at line 61)
- Test: `web/src/jobs/detailApi.test.ts` (Create)

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/detailApi.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import { getJob, getTaskLogs } from './api'

test('getJob fetches /jobs/:id and returns the detail with tasks', async () => {
  server.use(
    http.get('/v1/jobs/j1', () =>
      HttpResponse.json({
        id: 'j1',
        name: 'render',
        priority: 'high',
        status: 'running',
        submitted_by: 'u1',
        submitted_by_email: 'mira@studio.dev',
        labels: { team: 'fx' },
        created_at: '2026-07-01T00:00:00Z',
        updated_at: '2026-07-01T00:01:00Z',
        tasks: [
          {
            id: 't1',
            name: 'frame-001',
            status: 'done',
            commands: [['blender', '-b']],
            env: {},
            requires: {},
            timeout_seconds: 3600,
            retries: 2,
            retry_count: 0,
          },
        ],
      }),
    ),
  )
  const job = await getJob('j1')
  expect(job.name).toBe('render')
  expect(job.tasks[0].name).toBe('frame-001')
  expect(job.tasks[0].status).toBe('done')
})

test('getTaskLogs fetches /tasks/:id/logs with no since_seq by default', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_seq: 0, total: 0 })
    }),
  )
  await getTaskLogs('t1')
  expect(captured?.get('since_seq')).toBeNull()
})

test('getTaskLogs passes since_seq when provided', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_seq: 0, total: 0 })
    }),
  )
  await getTaskLogs('t1', 42)
  expect(captured?.get('since_seq')).toBe('42')
})

test('getJob throws ApiError on the error envelope', async () => {
  server.use(http.get('/v1/jobs/nope', () => HttpResponse.json({ error: 'job not found' }, { status: 404 })))
  await expect(getJob('nope')).rejects.toBeInstanceOf(ApiError)
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/jobs/detailApi.test.ts`
Expected: FAIL - `getJob` / `getTaskLogs` are not exported from `./api`.

- [ ] **Step 3: Write minimal implementation**

Append to `web/src/jobs/api.ts` (after line 61, keeping existing exports intact):

```ts
// Task-status vocabulary (migration 000019). Distinct from JobStatus: tasks add
// `dispatched` and `timed_out` and never use `cancelled` (a cancelled job's
// tasks are marked `failed` server-side).
export type TaskStatus = 'pending' | 'dispatched' | 'running' | 'done' | 'failed' | 'timed_out'

// One task as returned inside GET /v1/jobs/:id. `depends_on` is task NAMES, not
// IDs, resolved server-side; omitted when the task has no dependencies.
export interface TaskDetail {
  id: string
  name: string
  status: TaskStatus
  commands: string[][]
  env: Record<string, string>
  requires: Record<string, string>
  timeout_seconds: number | null
  retries: number
  retry_count: number
  depends_on?: string[]
  worker_id?: string
}

// GET /v1/jobs/:id. NOTE: the detail endpoint does NOT return total_tasks,
// done_tasks, started_at, or finished_at (those are list-only). Derive progress
// from `tasks`.
export interface JobDetail {
  id: string
  name: string
  priority: string
  status: JobStatus
  submitted_by: string
  submitted_by_email?: string
  labels: Record<string, string> | null
  tasks: TaskDetail[]
  created_at: string
  updated_at: string
}

export interface LogEntry {
  seq: number
  stream: 'stdout' | 'stderr'
  content: string
  created_at: string
}

export interface TaskLogPage {
  items: LogEntry[]
  next_seq: number
  total: number
}

// Fetches one job with its full task list. Throws ApiError(404) if absent.
export function getJob(id: string): Promise<JobDetail> {
  return apiFetch<JobDetail>(`/jobs/${id}`)
}

// Static historical task log (GET, seq-paginated). Fetch-once; no tailing.
export function getTaskLogs(taskId: string, sinceSeq?: number): Promise<TaskLogPage> {
  const q = new URLSearchParams()
  if (sinceSeq !== undefined) q.set('since_seq', String(sinceSeq))
  const qs = q.toString()
  return apiFetch<TaskLogPage>(`/tasks/${taskId}/logs${qs ? `?${qs}` : ''}`)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/jobs/detailApi.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/api.ts web/src/jobs/detailApi.test.ts
git commit -m "feat(web): job-detail API types and getJob/getTaskLogs fetchers"
```

---

### Task 2: Task status color map

**Files:**
- Create: `web/src/jobs/taskStatus.ts`
- Test: `web/src/jobs/taskStatus.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/taskStatus.test.ts`:

```ts
import { expect, test } from 'vitest'
import { taskStatusColor } from './taskStatus'

test('maps each of the six task statuses to a dot class', () => {
  expect(taskStatusColor('done').dot).toBe('bg-ok')
  expect(taskStatusColor('running').dot).toBe('bg-accent')
  expect(taskStatusColor('dispatched').dot).toBe('bg-accent')
  expect(taskStatusColor('pending').dot).toBe('bg-warn')
  expect(taskStatusColor('failed').dot).toBe('bg-err')
  expect(taskStatusColor('timed_out').dot).toBe('bg-err')
})

test('covers dispatched and timed_out (the statuses status.ts lacks)', () => {
  expect(taskStatusColor('dispatched').text).toBe('text-accent')
  expect(taskStatusColor('timed_out').text).toBe('text-err')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/jobs/taskStatus.test.ts`
Expected: FAIL - cannot resolve `./taskStatus`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/jobs/taskStatus.ts`:

```ts
import type { TaskStatus } from './api'

interface StatusView {
  text: string
  dot: string
}

// Color mapping for the TASK status vocabulary (distinct from status.ts, which
// only knows the JOB set). done=ok, running/dispatched=accent, pending=warn,
// failed/timed_out=err.
export function taskStatusColor(status: TaskStatus): StatusView {
  switch (status) {
    case 'done':
      return { text: 'text-ok', dot: 'bg-ok' }
    case 'running':
    case 'dispatched':
      return { text: 'text-accent', dot: 'bg-accent' }
    case 'pending':
      return { text: 'text-warn', dot: 'bg-warn' }
    case 'failed':
    case 'timed_out':
      return { text: 'text-err', dot: 'bg-err' }
    default:
      return { text: 'text-fg-mute', dot: 'bg-fg-mute' }
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/jobs/taskStatus.test.ts`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/taskStatus.ts web/src/jobs/taskStatus.test.ts
git commit -m "feat(web): taskStatusColor covering dispatched and timed_out"
```

---

### Task 3: DAG layout helper

**Files:**
- Create: `web/src/jobs/dagLayout.ts`
- Test: `web/src/jobs/dagLayout.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/dagLayout.test.ts`:

```ts
import { expect, test } from 'vitest'
import { dagLayout } from './dagLayout'
import type { TaskDetail } from './api'

function task(name: string, deps: string[] = [], status: TaskDetail['status'] = 'pending'): TaskDetail {
  return {
    id: name,
    name,
    status,
    commands: [],
    env: {},
    requires: {},
    timeout_seconds: null,
    retries: 0,
    retry_count: 0,
    depends_on: deps.length ? deps : undefined,
  }
}

function layerOf(nodes: ReturnType<typeof dagLayout>['nodes'], name: string): number {
  return nodes.find((n) => n.name === name)!.layer
}

test('roots with no deps land in layer 0', () => {
  const { nodes } = dagLayout([task('a'), task('b')])
  expect(layerOf(nodes, 'a')).toBe(0)
  expect(layerOf(nodes, 'b')).toBe(0)
})

test('a chain a -> b -> c yields layers 0, 1, 2', () => {
  const { nodes } = dagLayout([task('a'), task('b', ['a']), task('c', ['b'])])
  expect(layerOf(nodes, 'a')).toBe(0)
  expect(layerOf(nodes, 'b')).toBe(1)
  expect(layerOf(nodes, 'c')).toBe(2)
})

test('a fan-in node lands one layer past its deepest predecessor', () => {
  const tasks = [
    task('frame-001'),
    task('frame-002'),
    task('setup'),
    task('frame-003', ['setup']),
    task('denoise-all', ['frame-001', 'frame-002', 'frame-003']),
  ]
  const { nodes } = dagLayout(tasks)
  // frame-003 depends on setup(0) so is layer 1; denoise-all's deepest dep is
  // frame-003(1), so denoise-all is layer 2.
  expect(layerOf(nodes, 'frame-003')).toBe(1)
  expect(layerOf(nodes, 'denoise-all')).toBe(2)
})

test('edges are dep -> task for every depends_on entry', () => {
  const { edges } = dagLayout([task('a'), task('b', ['a'])])
  expect(edges).toEqual([{ from: 'a', to: 'b' }])
})

test('ignores a depends_on name that is not a known task', () => {
  const { nodes, edges } = dagLayout([task('b', ['ghost'])])
  expect(layerOf(nodes, 'b')).toBe(0)
  expect(edges).toEqual([])
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/jobs/dagLayout.test.ts`
Expected: FAIL - cannot resolve `./dagLayout`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/jobs/dagLayout.ts`:

```ts
import type { TaskDetail, TaskStatus } from './api'

export interface DagNode {
  name: string
  status: TaskStatus
  layer: number
}

export interface DagEdge {
  from: string
  to: string
}

export interface DagLayout {
  nodes: DagNode[]
  edges: DagEdge[]
}

// Builds a small directed graph from tasks[].name + depends_on. Nodes get a
// longest-path layer index (roots at 0, a node = 1 + max(layer of its known
// deps)); edges point dep -> task. Unknown dep names are ignored so a partial or
// malformed dependency never crashes rendering. Cycles are not expected (the API
// forbids them); a defensive visited-guard bounds the recursion regardless.
export function dagLayout(tasks: TaskDetail[]): DagLayout {
  const byName = new Map<string, TaskStatus>()
  for (const t of tasks) byName.set(t.name, t.status)

  const depsOf = new Map<string, string[]>()
  for (const t of tasks) {
    const known = (t.depends_on ?? []).filter((d) => byName.has(d))
    depsOf.set(t.name, known)
  }

  const layerCache = new Map<string, number>()
  function layer(name: string, stack: Set<string>): number {
    const cached = layerCache.get(name)
    if (cached !== undefined) return cached
    if (stack.has(name)) return 0 // defensive cycle break
    stack.add(name)
    const deps = depsOf.get(name) ?? []
    const l = deps.length === 0 ? 0 : 1 + Math.max(...deps.map((d) => layer(d, stack)))
    stack.delete(name)
    layerCache.set(name, l)
    return l
  }

  const nodes: DagNode[] = tasks.map((t) => ({
    name: t.name,
    status: t.status,
    layer: layer(t.name, new Set()),
  }))

  const edges: DagEdge[] = []
  for (const t of tasks) {
    for (const dep of depsOf.get(t.name) ?? []) {
      edges.push({ from: dep, to: t.name })
    }
  }

  return { nodes, edges }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/jobs/dagLayout.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/dagLayout.ts web/src/jobs/dagLayout.test.ts
git commit -m "feat(web): dagLayout helper (longest-path layers, dep->task edges)"
```

---

### Task 4: useJob polling hook

**Files:**
- Create: `web/src/jobs/useJob.ts`
- Test: `web/src/jobs/useJob.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/useJob.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJob } from './useJob'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches the job and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/jobs/j1', () => {
      count++
      return HttpResponse.json({
        id: 'j1', name: 'render', priority: 'high', status: 'running',
        submitted_by: 'u1', labels: null, tasks: [],
        created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
      })
    }),
  )
  const { result } = renderHook(() => useJob('j1', 20), { wrapper })
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
  await waitFor(() => expect(result.current.data?.name).toBe('render'))
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/jobs/useJob.test.tsx`
Expected: FAIL - cannot resolve `./useJob`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/jobs/useJob.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getJob } from './api'

// Polls a single job's detail (identity, status, and its task list). Polling
// keeps task status/progress live without SSE. Default 3000 matches the list and
// worker-detail pages. Tests inject a small value.
export function useJob(id: string, intervalMs = 3000) {
  return useQuery({
    queryKey: ['job', id],
    queryFn: () => getJob(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/jobs/useJob.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/useJob.ts web/src/jobs/useJob.test.tsx
git commit -m "feat(web): useJob polling hook for the detail response"
```

---

### Task 5: useTaskLogs gated hook

**Files:**
- Create: `web/src/jobs/useTaskLogs.ts`
- Test: `web/src/jobs/useTaskLogs.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/useTaskLogs.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useTaskLogs } from './useTaskLogs'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('does not fetch when disabled', async () => {
  let count = 0
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      count++
      return HttpResponse.json({ items: [], next_seq: 0, total: 0 })
    }),
  )
  renderHook(() => useTaskLogs('t1', false), { wrapper })
  await new Promise((r) => setTimeout(r, 50))
  expect(count).toBe(0)
})

test('fetches once when enabled and does not poll', async () => {
  let count = 0
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      count++
      return HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: 'hi', created_at: '2026-07-01T00:00:00Z' }],
        next_seq: 0,
        total: 1,
      })
    }),
  )
  const { result } = renderHook(() => useTaskLogs('t1', true), { wrapper })
  await waitFor(() => expect(result.current.data?.total).toBe(1))
  await new Promise((r) => setTimeout(r, 60))
  expect(count).toBe(1)
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/jobs/useTaskLogs.test.tsx`
Expected: FAIL - cannot resolve `./useTaskLogs`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/jobs/useTaskLogs.ts`:

```ts
import { useQuery } from '@tanstack/react-query'
import { getTaskLogs } from './api'

// Static historical log for a single task. NO refetchInterval: the log is
// fetch-once (live tailing/SSE is a separate deferred slice). `enabled` is
// controlled by the caller so we never fetch logs for a task the user has not
// opened, and never fetch while the Spec tab (not the Log tab) is showing.
// The key is deliberately NOT under the ['job', ...] prefix, so a job poll
// invalidation never disturbs the log query.
export function useTaskLogs(taskId: string, enabled: boolean) {
  return useQuery({
    queryKey: ['task-logs', taskId],
    queryFn: () => getTaskLogs(taskId),
    enabled,
  })
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/jobs/useTaskLogs.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/useTaskLogs.ts web/src/jobs/useTaskLogs.test.tsx
git commit -m "feat(web): useTaskLogs gated fetch-once hook (no polling)"
```

---

### Task 6: TaskDag SVG component

**Files:**
- Create: `web/src/jobs/TaskDag.tsx`
- Test: `web/src/jobs/TaskDag.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/TaskDag.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { TaskDag } from './TaskDag'
import type { TaskDetail } from './api'

function task(name: string, deps: string[] = [], status: TaskDetail['status'] = 'pending'): TaskDetail {
  return {
    id: name, name, status, commands: [], env: {}, requires: {},
    timeout_seconds: null, retries: 0, retry_count: 0,
    depends_on: deps.length ? deps : undefined,
  }
}

test('renders an accessible image labelled with node and edge counts', () => {
  render(<TaskDag tasks={[task('a'), task('b', ['a']), task('c', ['a'])]} />)
  const img = screen.getByRole('img', { name: /task dependency graph/i })
  expect(img).toBeInTheDocument()
  expect(img.getAttribute('aria-label')).toMatch(/3 tasks/)
  expect(img.getAttribute('aria-label')).toMatch(/2 dependency edges/)
})

test('renders each task name as a node label', () => {
  render(<TaskDag tasks={[task('frame-001'), task('denoise', ['frame-001'])]} />)
  expect(screen.getByText('frame-001')).toBeInTheDocument()
  expect(screen.getByText('denoise')).toBeInTheDocument()
})

test('renders an empty-state note when there are no tasks', () => {
  render(<TaskDag tasks={[]} />)
  expect(screen.getByText(/no tasks/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/jobs/TaskDag.test.tsx`
Expected: FAIL - cannot resolve `./TaskDag`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/jobs/TaskDag.tsx`:

```tsx
import type { TaskDetail } from './api'
import { taskStatusColor } from './taskStatus'
import { dagLayout, type DagNode } from './dagLayout'

const COL_W = 150
const ROW_H = 44
const NODE_W = 120
const NODE_H = 26
const PAD = 16

// Maps a node status to an SVG stroke/fill class. Reuses taskStatusColor's
// buckets; the text-* class doubles as an SVG `fill`/`stroke` via Tailwind's
// currentColor tokens on the wrapping <g>.
function nodeClass(node: DagNode): string {
  return taskStatusColor(node.status).text
}

// Visual-only dependency strip. The authoritative, screen-reader-navigable
// representation of dependencies is the tasks table's deps column; this SVG is
// an aid, so it is a single role="img" with a summarizing aria-label rather than
// individually focusable nodes.
export function TaskDag({ tasks }: { tasks: TaskDetail[] }) {
  if (tasks.length === 0) {
    return (
      <div className="rounded-card border border-border bg-white/5 p-4 text-[12px] text-fg-mute">
        No tasks to graph.
      </div>
    )
  }

  const { nodes, edges } = dagLayout(tasks)

  // Position: x by layer, y by order within a layer.
  const perLayer = new Map<number, number>()
  const pos = new Map<string, { x: number; y: number }>()
  for (const n of nodes) {
    const row = perLayer.get(n.layer) ?? 0
    perLayer.set(n.layer, row + 1)
    pos.set(n.name, { x: PAD + n.layer * COL_W, y: PAD + row * ROW_H })
  }

  const maxLayer = Math.max(...nodes.map((n) => n.layer))
  const maxRow = Math.max(...Array.from(perLayer.values()))
  const width = PAD * 2 + (maxLayer + 1) * COL_W
  const height = PAD * 2 + maxRow * ROW_H

  const label = `Task dependency graph: ${nodes.length} tasks, ${edges.length} dependency edges`

  return (
    <div className="overflow-x-auto rounded-card border border-border bg-white/5 p-2">
      <svg role="img" aria-label={label} width={width} height={height} className="text-fg-mute">
        {edges.map((e, i) => {
          const from = pos.get(e.from)!
          const to = pos.get(e.to)!
          const done = nodes.find((n) => n.name === e.from)?.status === 'done'
          return (
            <line
              key={i}
              x1={from.x + NODE_W}
              y1={from.y + NODE_H / 2}
              x2={to.x}
              y2={to.y + NODE_H / 2}
              stroke="currentColor"
              strokeWidth={1}
              strokeDasharray={done ? undefined : '4 3'}
              opacity={0.6}
            />
          )
        })}
        {nodes.map((n) => {
          const p = pos.get(n.name)!
          return (
            <g key={n.name} className={nodeClass(n)}>
              <rect
                x={p.x}
                y={p.y}
                width={NODE_W}
                height={NODE_H}
                rx={5}
                fill="currentColor"
                fillOpacity={0.12}
                stroke="currentColor"
                strokeOpacity={0.6}
              />
              <text
                x={p.x + NODE_W / 2}
                y={p.y + NODE_H / 2 + 3}
                textAnchor="middle"
                className="fill-fg font-mono text-[10px]"
              >
                {n.name.length > 16 ? `${n.name.slice(0, 15)}…` : n.name}
              </text>
            </g>
          )
        })}
      </svg>
    </div>
  )
}
```

Note: the `<text>` truncates to 16 chars so long names stay readable. The test names (`frame-001`, `denoise`, `a`, `b`, `c`) are all <= 16 chars, so `getByText` finds them verbatim.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/jobs/TaskDag.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/TaskDag.tsx web/src/jobs/TaskDag.test.tsx
git commit -m "feat(web): TaskDag SVG dependency strip (role=img + aria-label)"
```

---

### Task 7: TasksTable (selection, not navigation)

**Files:**
- Create: `web/src/jobs/TasksTable.tsx`
- Test: `web/src/jobs/TasksTable.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/TasksTable.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { TasksTable } from './TasksTable'
import type { TaskDetail } from './api'

function task(over: Partial<TaskDetail>): TaskDetail {
  return {
    id: 't1', name: 'frame-001', status: 'done', commands: [], env: {}, requires: {},
    timeout_seconds: null, retries: 2, retry_count: 0, ...over,
  }
}

const tasks: TaskDetail[] = [
  task({ id: 't1', name: 'frame-001', status: 'done' }),
  task({ id: 't2', name: 'denoise', status: 'running', depends_on: ['frame-001'], worker_id: 'w9abc123' }),
]

test('renders each task name and status', () => {
  render(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={() => {}} />)
  expect(screen.getByText('frame-001')).toBeInTheDocument()
  expect(screen.getByText('denoise')).toBeInTheDocument()
  expect(screen.getByText('running')).toBeInTheDocument()
})

test('marks the selected row with aria-selected', () => {
  render(<TasksTable tasks={tasks} selectedTaskId="t2" onSelect={() => {}} />)
  const rows = screen.getAllByRole('row')
  const selected = rows.filter((r) => r.getAttribute('aria-selected') === 'true')
  expect(selected).toHaveLength(1)
  expect(selected[0]).toHaveTextContent('denoise')
})

test('clicking a row calls onSelect with its id (selection, not navigation)', async () => {
  const onSelect = vi.fn()
  render(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={onSelect} />)
  await userEvent.click(screen.getByText('denoise'))
  expect(onSelect).toHaveBeenCalledWith('t2')
  // Rows are buttons/selectable, never anchors.
  expect(screen.queryByRole('link')).not.toBeInTheDocument()
})

test('shows an empty state when there are no tasks', () => {
  render(<TasksTable tasks={[]} selectedTaskId="" onSelect={() => {}} />)
  expect(screen.getByText(/no tasks/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/jobs/TasksTable.test.tsx`
Expected: FAIL - cannot resolve `./TasksTable`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/jobs/TasksTable.tsx`:

```tsx
import type { TaskDetail } from './api'
import { taskStatusColor } from './taskStatus'

const COLS = 'grid grid-cols-[1fr_110px_80px_120px_1fr]'

// Tasks table. Rows are SELECTION controls, not navigation: clicking a row sets
// the selected task that drives the Spec/Log panes. Uses aria-selected on each
// row (role=row inside role=table). No per-task duration/percent column: the API
// returns neither per-task timing nor a percent.
export function TasksTable({
  tasks,
  selectedTaskId,
  onSelect,
}: {
  tasks: TaskDetail[]
  selectedTaskId: string
  onSelect: (id: string) => void
}) {
  if (tasks.length === 0) {
    return (
      <div className="rounded-card border border-border bg-white/5 p-4 text-[12px] text-fg-mute">
        No tasks.
      </div>
    )
  }
  return (
    <div role="table" aria-label="Tasks" className="rounded-card border border-border bg-white/5">
      <div
        role="row"
        className={`${COLS} border-b border-border px-4 py-2 font-mono text-[10px] tracking-wider text-fg-mute`}
      >
        <span role="columnheader">NAME</span>
        <span role="columnheader">STATUS</span>
        <span role="columnheader">RETRY</span>
        <span role="columnheader">WORKER</span>
        <span role="columnheader">DEPS</span>
      </div>
      {tasks.map((t) => {
        const c = taskStatusColor(t.status)
        const selected = t.id === selectedTaskId
        return (
          <button
            key={t.id}
            type="button"
            role="row"
            aria-selected={selected}
            onClick={() => onSelect(t.id)}
            className={`${COLS} w-full items-center border-b border-border/40 px-4 py-2 text-left font-mono text-[11.5px] ${
              selected ? 'bg-accent/10' : ''
            }`}
          >
            <span role="cell" className="truncate font-sans text-[13px] text-fg">{t.name}</span>
            <span role="cell" className={`flex items-center gap-2 ${c.text}`}>
              <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
              {t.status}
            </span>
            <span role="cell" className="text-fg-mute">{t.retry_count}/{t.retries}</span>
            <span role="cell" className="truncate text-fg-mute">
              {t.worker_id ? t.worker_id.slice(0, 6) : '-'}
            </span>
            <span role="cell" className="truncate text-fg-mute">
              {t.depends_on && t.depends_on.length > 0 ? t.depends_on.join(', ') : '-'}
            </span>
          </button>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/jobs/TasksTable.test.tsx`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/TasksTable.tsx web/src/jobs/TasksTable.test.tsx
git commit -m "feat(web): TasksTable with selectable rows (aria-selected)"
```

---

### Task 8: SpecTab and LogTab

**Files:**
- Create: `web/src/jobs/SpecTab.tsx`, `web/src/jobs/LogTab.tsx`
- Test: `web/src/jobs/SpecTab.test.tsx`, `web/src/jobs/LogTab.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/jobs/SpecTab.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { SpecTab } from './SpecTab'
import type { TaskDetail } from './api'

const task: TaskDetail = {
  id: 't1', name: 'frame-001', status: 'done',
  commands: [['blender', '-b', 'scene.blend'], ['echo', 'done']],
  env: { CUDA: '1' },
  requires: { gpu: 'true' },
  timeout_seconds: 3600, retries: 2, retry_count: 0,
}

test('renders each command line', () => {
  render(<SpecTab task={task} />)
  expect(screen.getByText(/blender -b scene\.blend/)).toBeInTheDocument()
  expect(screen.getByText(/echo done/)).toBeInTheDocument()
})

test('renders env and requires entries', () => {
  render(<SpecTab task={task} />)
  expect(screen.getByText(/CUDA/)).toBeInTheDocument()
  expect(screen.getByText(/gpu/)).toBeInTheDocument()
})

test('renders a placeholder when no task is selected', () => {
  render(<SpecTab task={undefined} />)
  expect(screen.getByText(/select a task/i)).toBeInTheDocument()
})
```

Create `web/src/jobs/LogTab.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { LogTab } from './LogTab'
import type { LogEntry } from './api'

const items: LogEntry[] = [
  { seq: 1, stream: 'stdout', content: 'building', created_at: '2026-07-01T00:00:00Z' },
  { seq: 2, stream: 'stderr', content: 'warning: x', created_at: '2026-07-01T00:00:01Z' },
]

test('renders log lines with a stdout/stderr distinction', () => {
  render(<LogTab items={items} isLoading={false} isError={false} onRetry={() => {}} />)
  expect(screen.getByText('building')).toBeInTheDocument()
  const stderrLine = screen.getByText('warning: x')
  expect(stderrLine.className).toMatch(/text-err/)
})

test('shows the empty state when there is no output', () => {
  render(<LogTab items={[]} isLoading={false} isError={false} onRetry={() => {}} />)
  expect(screen.getByText(/no log output/i)).toBeInTheDocument()
})

test('shows a retry control on error', () => {
  render(<LogTab items={[]} isLoading={false} isError={true} onRetry={() => {}} />)
  expect(screen.getByText(/failed to load logs/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/jobs/SpecTab.test.tsx src/jobs/LogTab.test.tsx`
Expected: FAIL - cannot resolve `./SpecTab` / `./LogTab`.

- [ ] **Step 3: Write minimal implementations**

Create `web/src/jobs/SpecTab.tsx`:

```tsx
import type { TaskDetail } from './api'

// Renders the selected task's spec: commands, env, requires. No per-task source
// block (handleGetJob does not echo `source`); that is out of scope.
export function SpecTab({ task }: { task: TaskDetail | undefined }) {
  if (!task) {
    return <div className="p-4 text-[12px] text-fg-mute">Select a task to view its spec.</div>
  }
  const env = Object.entries(task.env)
  const requires = Object.entries(task.requires)
  return (
    <div className="flex flex-col gap-4 p-4">
      <section>
        <div className="mb-1 font-mono text-[10px] tracking-widest text-fg-mute">COMMANDS</div>
        <div className="flex flex-col gap-1 rounded-card border border-border bg-black/20 p-3 font-mono text-[11.5px] text-fg">
          {task.commands.length === 0 ? (
            <span className="text-fg-mute">(none)</span>
          ) : (
            task.commands.map((cmd, i) => <div key={i}>$ {cmd.join(' ')}</div>)
          )}
        </div>
      </section>
      <section>
        <div className="mb-1 font-mono text-[10px] tracking-widest text-fg-mute">ENV</div>
        <div className="rounded-card border border-border bg-white/5 p-3 font-mono text-[11.5px] text-fg-mute">
          {env.length === 0 ? '(none)' : env.map(([k, v]) => <div key={k}>{k}={v}</div>)}
        </div>
      </section>
      <section>
        <div className="mb-1 font-mono text-[10px] tracking-widest text-fg-mute">REQUIRES</div>
        <div className="rounded-card border border-border bg-white/5 p-3 font-mono text-[11.5px] text-fg-mute">
          {requires.length === 0 ? '(none)' : requires.map(([k, v]) => <div key={k}>{k}={v}</div>)}
        </div>
      </section>
    </div>
  )
}
```

Create `web/src/jobs/LogTab.tsx`:

```tsx
import { Button } from '../components/Button'
import type { LogEntry } from './api'

// Static historical log renderer. Fetch-once semantics live in the hook; this is
// a pure view over the resolved items plus loading/error/empty states. No SSE,
// no follow toggle, no auto-scroll-to-tail (deferred slice).
export function LogTab({
  items,
  isLoading,
  isError,
  onRetry,
}: {
  items: LogEntry[]
  isLoading: boolean
  isError: boolean
  onRetry: () => void
}) {
  if (isLoading) {
    return <div className="p-4 text-[12px] text-fg-mute">Loading logs...</div>
  }
  if (isError) {
    return (
      <div className="flex flex-col items-start gap-2 p-4">
        <div className="text-[12px] text-err">Failed to load logs.</div>
        <Button className="w-auto px-4" onClick={onRetry}>Retry</Button>
      </div>
    )
  }
  if (items.length === 0) {
    return <div className="p-4 text-[12px] text-fg-mute">No log output.</div>
  }
  return (
    <div className="flex flex-col gap-0.5 p-3 font-mono text-[11px]">
      {items.map((l) => (
        <div key={l.seq} className={l.stream === 'stderr' ? 'text-err' : 'text-fg'}>
          {l.content}
        </div>
      ))}
    </div>
  )
}
```

Note: confirm `web/src/components/Button.tsx` exists (it does; used by `WorkerDetailPage`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/jobs/SpecTab.test.tsx src/jobs/LogTab.test.tsx`
Expected: PASS (3 + 3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/SpecTab.tsx web/src/jobs/SpecTab.test.tsx web/src/jobs/LogTab.tsx web/src/jobs/LogTab.test.tsx
git commit -m "feat(web): SpecTab and LogTab detail-pane views"
```

---

### Task 9: JobDetailPage route shell

**Files:**
- Create: `web/src/jobs/JobDetailPage.tsx`
- Test: `web/src/jobs/JobDetailPage.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/JobDetailPage.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { JobDetailPage } from './JobDetailPage'

const ID = 'j1'

const JOB = {
  id: ID,
  name: 'shot-042 render',
  priority: 'high',
  status: 'running',
  submitted_by: 'u1',
  submitted_by_email: 'mira@studio.dev',
  labels: { team: 'fx' },
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:01:00Z',
  tasks: [
    {
      id: 't1', name: 'frame-001', status: 'done',
      commands: [['blender', '-b']], env: {}, requires: {},
      timeout_seconds: 3600, retries: 2, retry_count: 0,
    },
    {
      id: 't2', name: 'denoise', status: 'running',
      commands: [['denoise', '--all']], env: { CUDA: '1' }, requires: { gpu: 'true' },
      timeout_seconds: null, retries: 1, retry_count: 0, depends_on: ['frame-001'],
    },
  ],
}

function renderDetail() {
  setToken('test-token')
  server.use(http.get('/v1/users/me', () => HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: false })))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/jobs/${ID}`]}>
        <AuthProvider>
          <Routes>
            <Route path="/jobs/:id" element={<JobDetailPage />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('renders job identity and its tasks', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail()
  expect(await screen.findByText('shot-042 render')).toBeInTheDocument()
  expect(screen.getByText('mira@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('frame-001')).toBeInTheDocument()
  expect(screen.getByText('denoise')).toBeInTheDocument()
})

test('shows not-found for a 404 job with a back link', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json({ error: 'job not found' }, { status: 404 })))
  renderDetail()
  expect(await screen.findByText('Job not found.')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /jobs/i })).toBeInTheDocument()
})

test('shows a generic error with a Retry button on a non-404 failure', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderDetail()
  expect(await screen.findByRole('button', { name: /retry/i })).toBeInTheDocument()
})

test('defaults to the Spec tab and shows the selected task spec', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail()
  // Default selection is the first running task (denoise), Spec tab active.
  expect(await screen.findByText(/denoise --all/)).toBeInTheDocument()
  expect(screen.getByText(/CUDA/)).toBeInTheDocument()
})

test('does NOT hit the log endpoint while the Spec tab is active', async () => {
  let logCount = 0
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  server.use(
    http.get('/v1/tasks/:tid/logs', () => {
      logCount++
      return HttpResponse.json({ items: [], next_seq: 0, total: 0 })
    }),
  )
  renderDetail()
  await screen.findByText('shot-042 render')
  await new Promise((r) => setTimeout(r, 60))
  expect(logCount).toBe(0)
})

test('switching to the Log tab fetches once and renders lines', async () => {
  let logCount = 0
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  server.use(
    http.get('/v1/tasks/t2/logs', () => {
      logCount++
      return HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: 'rendering', created_at: '2026-07-01T00:00:00Z' }],
        next_seq: 0,
        total: 1,
      })
    }),
  )
  renderDetail()
  await screen.findByText('shot-042 render')
  await userEvent.click(screen.getByRole('tab', { name: /log/i }))
  expect(await screen.findByText('rendering')).toBeInTheDocument()
  expect(logCount).toBe(1)
})

test('selecting a task updates aria-selected and drives the spec pane', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail()
  await screen.findByText('shot-042 render')
  await userEvent.click(screen.getByText('frame-001'))
  const rows = screen.getAllByRole('row')
  const selected = rows.filter((r) => r.getAttribute('aria-selected') === 'true')
  expect(selected[0]).toHaveTextContent('frame-001')
  expect(screen.getByText(/blender -b/)).toBeInTheDocument()
})

test('reserved actions slot renders but contains no action buttons (deferred)', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail()
  await screen.findByText('shot-042 render')
  expect(screen.queryByRole('button', { name: /cancel/i })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /retry job/i })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /submit/i })).not.toBeInTheDocument()
})

test('derives progress from the tasks array (1 of 2 done)', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail()
  expect(await screen.findByText(/1\s*\/\s*2 tasks done/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/jobs/JobDetailPage.test.tsx`
Expected: FAIL - cannot resolve `./JobDetailPage`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/jobs/JobDetailPage.tsx`:

```tsx
import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { Button } from '../components/Button'
import { statusColor, progressPct } from './status'
import { TasksTable } from './TasksTable'
import { TaskDag } from './TaskDag'
import { SpecTab } from './SpecTab'
import { LogTab } from './LogTab'
import { useJob } from './useJob'
import { useTaskLogs } from './useTaskLogs'
import type { TaskDetail } from './api'

type Tab = 'spec' | 'log'

// Picks the most useful default task: the first running/failed one if present,
// else the first task. Returns '' for an empty job.
function defaultTaskId(tasks: TaskDetail[]): string {
  const active = tasks.find((t) => t.status === 'running' || t.status === 'failed' || t.status === 'timed_out')
  return active?.id ?? tasks[0]?.id ?? ''
}

export function JobDetailPage() {
  const { id = '' } = useParams()
  const { data: job, error, isLoading, refetch } = useJob(id)
  const [tab, setTab] = useState<Tab>('spec')
  const [pickedTaskId, setPickedTaskId] = useState<string>('')

  const tasks = job?.tasks ?? []

  // Effective selection: an explicit pick if it still matches a task, else the
  // default. This falls back automatically when a poll changes the task list.
  const selectedTaskId = useMemo(() => {
    if (pickedTaskId && tasks.some((t) => t.id === pickedTaskId)) return pickedTaskId
    return defaultTaskId(tasks)
  }, [pickedTaskId, tasks])

  const selectedTask = tasks.find((t) => t.id === selectedTaskId)

  const logs = useTaskLogs(selectedTaskId, selectedTaskId !== '' && tab === 'log')

  if (isLoading && !job) {
    return <div className="h-40 rounded-card border border-border bg-white/5" />
  }

  if (error && !job) {
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
        {notFound ? (
          <div className="text-[13px] text-fg-mute">Job not found.</div>
        ) : (
          <>
            <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => refetch()}>Retry</Button>
          </>
        )}
        <div className="mt-4">
          <Link to="/jobs" className="font-mono text-[11px] text-accent">&larr; Jobs</Link>
        </div>
      </div>
    )
  }

  if (!job) return null

  const c = statusColor(job.status)
  const done = tasks.filter((t) => t.status === 'done').length
  const total = tasks.length
  const active = tasks.filter((t) => t.status === 'running' || t.status === 'dispatched').length
  const pct = progressPct(done, total)
  const chips = Object.entries(job.labels ?? {}).map(([k, v]) => (v ? `${k}=${v}` : k))

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        <Link to="/jobs" className="font-mono text-[11px] text-fg-mute hover:text-fg">&larr; Jobs</Link>
        <div className="flex items-center gap-3">
          <h1 className="text-[28px] font-normal tracking-tight">{job.name}</h1>
          <span className={`flex items-center gap-2 font-mono text-[12px] ${c.text}`}>
            <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
            {job.status}
          </span>
          {/* Reserved actions slot: cancel/retry deferred to a later slice. Intentionally empty. */}
          <div data-testid="job-actions" className="ml-auto flex items-center gap-2" />
        </div>
        <div className="font-mono text-[11px] text-fg-mute">
          id {job.id.slice(0, 8)} · submitted by {job.submitted_by_email ?? '-'} · priority {job.priority}
        </div>
        {chips.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-1">
            {chips.map((ch) => (
              <span key={ch} className="rounded-full border border-accent/40 bg-accent/10 px-2 py-0.5 font-mono text-[10px] text-accent">
                {ch}
              </span>
            ))}
          </div>
        )}
      </div>

      <div className="flex flex-col gap-5 lg:flex-row">
        <div className="flex flex-col gap-4 lg:w-[55%]">
          <div className="flex flex-col gap-2">
            <div className="flex items-baseline justify-between font-mono text-[11px] text-fg-mute">
              <span>{done} / {total} tasks done</span>
              <span>{active} active</span>
            </div>
            <span className="relative h-1.5 overflow-hidden rounded bg-white/10">
              <span
                className={`absolute inset-y-0 left-0 rounded ${
                  job.status === 'done' ? 'bg-ok' : job.status === 'failed' ? 'bg-err' : 'bg-accent'
                }`}
                style={{ width: `${pct}%` }}
              />
            </span>
          </div>

          <TaskDag tasks={tasks} />
          <TasksTable tasks={tasks} selectedTaskId={selectedTaskId} onSelect={setPickedTaskId} />
        </div>

        <div className="flex flex-col lg:w-[45%]">
          <div role="tablist" aria-label="Task detail" className="flex gap-1 border-b border-border">
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'spec'}
              onClick={() => setTab('spec')}
              className={`px-3 py-2 text-[12px] ${tab === 'spec' ? 'border-b-2 border-accent text-fg' : 'text-fg-mute'}`}
            >
              Spec
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'log'}
              onClick={() => setTab('log')}
              className={`px-3 py-2 text-[12px] ${tab === 'log' ? 'border-b-2 border-accent text-fg' : 'text-fg-mute'}`}
            >
              Log
            </button>
          </div>
          <div className="rounded-b-card border border-t-0 border-border bg-white/5">
            {tab === 'spec' ? (
              <SpecTab task={selectedTask} />
            ) : (
              <LogTab
                items={logs.data?.items ?? []}
                isLoading={logs.isLoading}
                isError={logs.isError}
                onRetry={() => logs.refetch()}
              />
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/jobs/JobDetailPage.test.tsx`
Expected: PASS (9 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/JobDetailPage.tsx web/src/jobs/JobDetailPage.test.tsx
git commit -m "feat(web): JobDetailPage route shell with Spec/Log tabs"
```

---

### Task 10: Wire the route

**Files:**
- Modify: `web/src/app/router.tsx` (add import + one `<Route>` inside `<ProtectedRoute>`, currently lines 12-30)
- Test: covered by `JobDetailPage.test.tsx` (route) and Task 11 (link target). No new test file.

- [ ] **Step 1: Add the import**

In `web/src/app/router.tsx`, after the `JobsPage` import (line 5), add:

```tsx
import { JobDetailPage } from '../jobs/JobDetailPage'
```

- [ ] **Step 2: Add the route**

Inside the `<Route element={<ProtectedRoute />}>` block, immediately after the `/jobs` route (line 20), add:

```tsx
        <Route path="/jobs/:id" element={<JobDetailPage />} />
```

- [ ] **Step 3: Type-check**

Run: `cd web && npx tsc -b`
Expected: no errors.

- [ ] **Step 4: Run the full jobs test suite to confirm nothing broke**

Run: `cd web && npx vitest run src/jobs`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/app/router.tsx
git commit -m "feat(web): register /jobs/:id route"
```

---

### Task 11: JobsTable name-cell link

**Files:**
- Modify: `web/src/jobs/JobsTable.tsx` (add `Link` import; wrap the name span at lines 34-41)
- Test: `web/src/jobs/JobsTable.test.tsx` (add a link-href test; wrap the render in a router)

- [ ] **Step 1: Write the failing test**

Edit `web/src/jobs/JobsTable.test.tsx`. Replace the existing imports and each `render(<JobsTable .../>)` call so the table renders inside a `MemoryRouter` (a bare `<Link>` throws outside a router), then add the href assertion. The updated file:

```tsx
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { JobsTable } from './JobsTable'
import type { Job } from './api'

const jobs: Job[] = [
  {
    id: '9F4E1C', name: 'film-x / shot-042 render', priority: 'high', status: 'running',
    submitted_by_email: 'mira@studio.dev', labels: null,
    created_at: '2026-06-05T14:22:00Z', updated_at: '2026-06-05T14:30:00Z',
    total_tasks: 64, done_tasks: 48, started_at: '2026-06-05T14:22:00Z',
    scheduled_job_name: 'nightly-etl',
  },
  {
    id: 'C41A02', name: 'ci build', priority: 'low', status: 'done',
    submitted_by_email: 'ci@studio.dev', labels: null,
    created_at: '2026-06-05T14:30:00Z', updated_at: '2026-06-05T14:34:00Z',
    total_tasks: 12, done_tasks: 12,
  },
]

function renderTable(rows: Job[]) {
  return render(
    <MemoryRouter>
      <JobsTable jobs={rows} />
    </MemoryRouter>,
  )
}

test('renders job rows with name, owner, and progress percent', () => {
  renderTable(jobs)
  expect(screen.getByText('film-x / shot-042 render')).toBeInTheDocument()
  expect(screen.getByText('mira@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('75%')).toBeInTheDocument()
  expect(screen.getByText('100%')).toBeInTheDocument()
})

test('renders the schedule chip only when scheduled_job_name is present', () => {
  renderTable(jobs)
  expect(screen.getByText(/nightly-etl/)).toBeInTheDocument()
})

test('renders the empty state when there are no jobs', () => {
  renderTable([])
  expect(screen.getByText(/no jobs/i)).toBeInTheDocument()
})

test('the job name links to the job detail page', () => {
  renderTable(jobs)
  const link = screen.getByRole('link', { name: 'film-x / shot-042 render' })
  expect(link).toHaveAttribute('href', '/jobs/9F4E1C')
})
```

- [ ] **Step 2: Run test to verify the new test fails**

Run: `cd web && npx vitest run src/jobs/JobsTable.test.tsx`
Expected: FAIL - `getByRole('link', ...)` finds no link (name is a plain `<span>`).

- [ ] **Step 3: Write minimal implementation**

Edit `web/src/jobs/JobsTable.tsx`. Add the import at the top:

```tsx
import { Link } from 'react-router-dom'
```

Replace the name span (current lines 34-35) so the name is a Link, keeping the schedule chip sibling intact:

```tsx
            <span className="flex min-w-0 items-center gap-2">
              <Link to={`/jobs/${j.id}`} className="truncate font-sans text-[13px] text-fg hover:text-accent">
                {j.name}
              </Link>
              {j.scheduled_job_name && (
```

(The `{j.scheduled_job_name && (` line and everything after it in that span stays unchanged.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/jobs/JobsTable.test.tsx`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/JobsTable.tsx web/src/jobs/JobsTable.test.tsx
git commit -m "feat(web): link job-name cell to /jobs/:id"
```

---

### Task 12: Full suite + type check

**Files:** none (verification only)

- [ ] **Step 1: Type-check the whole web project**

Run: `cd web && npx tsc -b`
Expected: no errors.

- [ ] **Step 2: Run the full web test suite**

Run: `cd web && npx vitest run`
Expected: all tests PASS, including the pre-existing suites.

- [ ] **Step 3: Confirm web/dist is not dirtied**

The tracked `web/dist` is a stale scaffold artifact. This slice runs no `vite build`, so it should be untouched. If any build ran, revert it:

Run: `git checkout -- web/dist/`

- [ ] **Step 4: Commit (only if anything remains to record)**

No new files are expected here. If Step 1/2 surfaced a fix, commit it with a descriptive message; otherwise this task is a no-op gate.

---

## Self-review

**Spec coverage:**
- `/jobs/:id` page with split layout -> Task 9 + Task 10.
- Row-click navigation from JobsTable -> Task 11.
- Tasks table (status, retry, worker, deps; selection not navigation) -> Task 7.
- Task-DAG strip from `tasks[].name` + `depends_on` -> Tasks 3 + 6.
- Spec tab (commands/env/requires) -> Task 8 (SpecTab).
- Log tab (static GET, fetch-once, gated) -> Tasks 1 (fetcher), 5 (hook), 8 (LogTab), 9 (wiring + gating test).
- Loading/404/error/back-link states -> Task 9.
- Separate `TaskStatus` + color map (dispatched, timed_out) -> Tasks 1 + 2.
- Progress derived from tasks array -> Task 9 (`done`/`total`/`pct`, "1 / 2 tasks done" test).
- Deferral guards (no action buttons; log not hit on Spec tab) -> Task 9 tests.
- JobsTable name-cell link href -> Task 11 test.

**Correctness points cross-check:** each of the six "MUST NOT miss" items maps to a task and a test: (1) progress-derivation test in Task 9; (2) `TaskStatus`/`taskStatusColor` in Tasks 1-2, never touching `JobStatus`/`statusColor`; (3) `dagLayout` builds edges from names with no fetch, Task 3 + Task 6 (no `/tasks/:id` mock needed); (4) `useTaskLogs` `enabled` gate + the "does NOT hit the log endpoint while Spec tab active" test in Task 9; (5) reserved-slot empty `<div data-testid="job-actions">` + the "no action buttons" test in Task 9; (6) `useState<Tab>('spec')` default + the "defaults to the Spec tab" test in Task 9.

**Type consistency:** `JobDetail`/`TaskDetail`/`LogEntry`/`TaskLogPage`/`TaskStatus` are defined once in Task 1 and imported everywhere. `dagLayout` returns `{ nodes: DagNode[], edges: DagEdge[] }` with `DagNode.layer`; the tests (`layerOf`) and `TaskDag` both read `.layer`, `.name`, `.status`, and edges `.from`/`.to` consistently. `taskStatusColor` returns `{text, dot}` (matching `statusColor`'s shape), consumed by `TaskDag` (`.text`) and `TasksTable` (`.text`, `.dot`). Hook signatures: `useJob(id, intervalMs?)`, `useTaskLogs(taskId, enabled)`; `JobDetailPage` calls `useTaskLogs(selectedTaskId, selectedTaskId !== '' && tab === 'log')`, matching Task 5.

**Placeholder scan:** no TBD/TODO/"add error handling"/"similar to Task N" left; every code step shows real code.

**Note for the reviewer:** confirm `web/src/components/Button.tsx` exports `Button` with an `onClick` and accepts a `className` prop - both `WorkerDetailPage` and `JobsPage` use exactly `<Button className="w-auto px-4" onClick={...}>`, so this is an established contract, not a new one.
