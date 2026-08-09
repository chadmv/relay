# Admin Console - Server / Overview Tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the fourth admin console tab, `/admin/server`, a read-only operational overview rendering nine fleet-wide counts, the self-registration policy flag and an HTTP reachability pill from four endpoints that already exist.

**Architecture:** A new `web/src/admin/server/` module following the sibling-tab shape (api client + hooks + presentational components + a `*Tab.tsx` composition), plus exactly one entry appended to `ADMIN_TABS`. Job and worker counts reuse the shipped `useJobStats` / `useWorkerStats` hooks across module boundaries (no second client for either endpoint); health and config get thin new clients and hooks. The distinguishing requirement is per-region degradation: four independent queries, and no query's failure may unmount another's data.

**Tech Stack:** React 18, TypeScript, TanStack Query v5, react-router-dom v7, Tailwind v4 (Holo tokens), Vitest + MSW + Testing Library.

---

## Slice independence declaration

- **This is a 100% frontend slice.** Owner: `relay-frontend-engineer`.
- **There is NO backend slice.** No Go file, no `.sql` file, no `.proto` file, no migration is touched. All four endpoints (`GET /v1/jobs/stats`, `GET /v1/workers/stats`, `GET /v1/config`, `GET /v1/health`) exist and ship today, unchanged. No `make generate` step exists anywhere in this plan.
- **Tasks 1-9 are SEQUENTIAL, not parallel.** Each task's test imports a module the previous task created (`api.ts` -> hooks -> components -> `ServerTab` -> registry). There is nothing here to fan out; a Phase 3 parallel dispatch would just produce merge conflicts in `ServerTab.tsx`.
- Because there is only one slice, Phase 3 runs a single engineer end to end.

## Spec

`docs/superpowers/specs/2026-08-09-admin-server-overview-tab.md` is authoritative. Read it before Task 1; the "Degraded rendering" and "Cell contents" sections are the acceptance criteria this plan encodes.

## Critical files to read first

| File | Why |
|---|---|
| `web/src/admin/tabs.ts` | The registry you append one entry to (Task 8). |
| `web/src/admin/reservations/ReservationsTab.tsx` | The sibling-tab shape: endpoint caption, error strip with Retry, mono footnote. |
| `web/src/admin/reservations/api.test.ts` | The `server.use()` + MSW client-test convention you mirror in Task 1. |
| `web/src/admin/reservations/useWorkerOptions.test.tsx` | The `renderHook` + `QueryClientProvider` harness, including the "positive control on the same counter" habit. |
| `web/src/jobs/useJobStats.ts`, `web/src/workers/useWorkerStats.ts` | The two reused hooks and, importantly, their REAL query keys (see the correction below). |
| `web/src/components/holo/{KpiStat,Panel,Chip,Eyebrow,GlassPanel}.tsx` | The primitives you compose. `KpiStat` already wraps `GlassPanel`, which is why it is never nested inside `Panel`. |
| `web/src/lib/api.ts` | `apiFetch` prefixes `/v1`, attaches the bearer, throws `ApiError(status, code, "<status> <code>")`. |
| `web/src/lib/types.ts:13-15` | `ConfigResponse` already exists. Import it; do not redeclare it. |

### Correction to one spec detail - read this before Task 6

The spec's "Polling and load" table names the worker stats cache key `['worker-stats']`. **That is wrong.** The shipped hook caches under `['workers', 'stats']`:

```ts
// web/src/workers/useWorkerStats.ts:6-13
export function useWorkerStats(intervalMs = 3000) {
  return useQuery({
    queryKey: ['workers', 'stats'],
    queryFn: getWorkerStats,
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

The spec's substantive point is unaffected and still correct - this tab creates an *observer* on the existing entry rather than a new entry - because it calls the same hook. Do not "fix" the key, and do not assert `['worker-stats']` anywhere. `useJobStats`'s key really is `['job-stats']`.

Two consequences of `keepPreviousData` on `useWorkerStats` that Task 7 depends on: after a successful load, a failing poll leaves `data` defined and sets `error`, which is exactly the stale-with-error state. `useJobStats` behaves the same way for a failing *refetch* (TanStack v5 keeps the last successful `data` on the cache entry) even without `placeholderData`.

## File structure

### Create

| File | Responsibility |
|---|---|
| `web/src/admin/server/api.ts` | `getHealth()`, `getServerConfig()`, `HealthResponse`. |
| `web/src/admin/server/api.test.ts` | Client contract tests. |
| `web/src/admin/server/useServerHealth.ts` | `['server-health']`, 30s poll. |
| `web/src/admin/server/useServerHealth.test.tsx` | Key + polling behaviour. |
| `web/src/admin/server/useServerConfig.ts` | `['server-config']`, `staleTime: Infinity`, no poll. |
| `web/src/admin/server/useServerConfig.test.tsx` | Key + no-refetch-on-remount. |
| `web/src/admin/server/ErrorStrip.tsx` | The shared degraded body: message + Retry. Two consumers (stat sections, access panel). |
| `web/src/admin/server/ErrorStrip.test.tsx` | Message renders, Retry calls back once. |
| `web/src/admin/server/HealthPill.tsx` | `deriveHealthPill()` (pure) + `HealthPill`. |
| `web/src/admin/server/HealthPill.test.tsx` | All four pill states. |
| `web/src/admin/server/StatSection.tsx` | Caption + KPI grid + loading / stale / degraded bodies. |
| `web/src/admin/server/StatSection.test.tsx` | The three body states in isolation. |
| `web/src/admin/server/ServerTab.tsx` | Composition: header row, two stat sections, access panel, footnote. |
| `web/src/admin/server/ServerTab.test.tsx` | Happy path (Task 6) and the degraded matrix (Task 7). |

`ErrorStrip` is one file beyond the spec's list. It is justified by the spec's own reasoning for `StatSection` ("the degraded behaviour is the part that must be identical in both") - here there are three consumers of the same strip, and duplicating it three times is how the copy drifts.

### Modify

| File | Change |
|---|---|
| `web/src/admin/tabs.ts:1-24` | Import `ServerTab`, append one registry entry, extend the header comment's backlog reference. |
| `web/src/admin/AdminPage.tsx:6-9` | Comment-only correction of the stale "future Server/overview tab" forward reference. |
| `web/src/admin/AdminPage.test.tsx` | Two new routing tests (Task 8). |

Nothing outside `web/src/admin/` changes. `web/src/jobs/`, `web/src/workers/`, `web/src/lib/` and `web/src/components/` are read-only for this slice.

## Conventions this plan assumes

- Run the web suite from `web/`: `cd web && npm test`. A single file: `npm test -- src/admin/server/api.test.ts`. A single test: `npm test -- src/admin/server/ServerTab.test.tsx -t 'name fragment'`.
- Commits happen from the repo root (`D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb`), and every `git add` in this plan lists explicit paths - never `git add -A`, because `web/dist` is tracked and stale.
- **Plan-supplied test bodies are guesses.** Every test in this plan must be run and observed RED for the right reason before its implementation step. If a test passes before the implementation exists, it is vacuous - fix the test, do not proceed.

---

### Task 1: The health and config clients

**Files:**
- Create: `web/src/admin/server/api.ts`
- Test: `web/src/admin/server/api.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/server/api.test.ts`:

```tsx
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { ApiError } from '../../lib/api'
import { getHealth, getServerConfig } from './api'

test('getHealth requests /v1/health and returns the status string', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/health', ({ request }) => {
      path = new URL(request.url).pathname
      return HttpResponse.json({ status: 'ok' })
    }),
  )
  const res = await getHealth()
  expect(path).toBe('/v1/health')
  expect(res.status).toBe('ok')
})

test('getHealth passes through a non-ok status rather than normalizing it', async () => {
  // handleHealth writes map[string]string, so the value is NOT a closed union.
  // A future "degraded" must reach the pill instead of being coerced to ok.
  server.use(http.get('/v1/health', () => HttpResponse.json({ status: 'degraded' })))
  expect((await getHealth()).status).toBe('degraded')
})

test('getHealth surfaces a 500 as an ApiError', async () => {
  server.use(http.get('/v1/health', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  const err = await getHealth().catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err).toMatchObject({ status: 500, code: 'boom' })
})

test('getServerConfig requests /v1/config and returns allow_self_register', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/config', ({ request }) => {
      path = new URL(request.url).pathname
      return HttpResponse.json({ allow_self_register: true })
    }),
  )
  const res = await getServerConfig()
  expect(path).toBe('/v1/config')
  expect(res.allow_self_register).toBe(true)
})

test('getServerConfig carries false through, not undefined', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ allow_self_register: false })))
  const res = await getServerConfig()
  expect(res.allow_self_register).toBe(false)
})

test('getServerConfig surfaces a 500 as an ApiError', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  await expect(getServerConfig()).rejects.toBeInstanceOf(ApiError)
})
```

- [ ] **Step 2: Run the test and watch it fail**

```
cd web && npm test -- src/admin/server/api.test.ts
```

Expected: FAIL - `Failed to resolve import "./api"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/admin/server/api.ts`:

```ts
import { apiFetch } from '../../lib/api'
import type { ConfigResponse } from '../../lib/types'

// Mirrors handleHealth (internal/api/health.go:5-7), which writes
// map[string]string{"status": "ok"}. `status` is deliberately `string` and not a
// closed union: the Go handler's type admits any value, and the pill reports what
// the server said rather than asserting health from a 200.
//
// HEALTHY here means "the HTTP listener answered". handleHealth performs NO
// database check, so this must never be presented as a database probe.
export interface HealthResponse {
  status: string
}

export function getHealth(): Promise<HealthResponse> {
  return apiFetch<HealthResponse>('/health')
}

// ConfigResponse already exists in lib/types.ts:13-15 (RegisterScreen consumes it).
// Re-export the type so this module's consumers have one import site, but do NOT
// redeclare the shape.
export type { ConfigResponse }

// Public endpoint (internal/api/config.go:5-11): no bearer required. apiFetch
// attaches one anyway when a token is present, which the server ignores.
export function getServerConfig(): Promise<ConfigResponse> {
  return apiFetch<ConfigResponse>('/config')
}
```

- [ ] **Step 4: Run the test and watch it pass**

```
cd web && npm test -- src/admin/server/api.test.ts
```

Expected: PASS, 6 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/server/api.ts web/src/admin/server/api.test.ts
git commit -m "feat(web): health and config clients for the admin server tab"
```

---

### Task 2: useServerHealth

**Files:**
- Create: `web/src/admin/server/useServerHealth.ts`
- Test: `web/src/admin/server/useServerHealth.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/server/useServerHealth.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { HEALTH_POLL_MS, useServerHealth } from './useServerHealth'
import type { HealthResponse } from './api'

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('caches under ["server-health"]', async () => {
  server.use(http.get('/v1/health', () => HttpResponse.json({ status: 'ok' })))
  const client = newClient()
  const { result } = renderHook(() => useServerHealth(), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(client.getQueryData<HealthResponse>(['server-health'])?.status).toBe('ok')
})

test('polls at the 30s reachability cadence, not faster', async () => {
  let calls = 0
  server.use(
    http.get('/v1/health', () => {
      calls++
      return HttpResponse.json({ status: 'ok' })
    }),
  )
  const { result } = renderHook(() => useServerHealth(), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)
  expect(HEALTH_POLL_MS).toBe(30_000)

  // A real-time wait can only rule out an interval SHORTER than the wait, so this
  // 250ms window catches a copy-pasted 3000/10_000 only via the constant assertion
  // above - which is the load-bearing check. The wait guards against an accidental
  // sub-250ms interval, which no copy-paste source in this repo has.
  await new Promise((r) => setTimeout(r, 250))
  expect(calls).toBe(1)

  // Positive control on the SAME counter, so the equality above is about polling
  // and not a dead instrument.
  await result.current.refetch()
  await waitFor(() => expect(calls).toBe(2))
})

test('an error leaves data undefined so the pill can distinguish unreachable from checking', async () => {
  server.use(http.get('/v1/health', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  const { result } = renderHook(() => useServerHealth(), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('error'))
  expect(result.current.data).toBeUndefined()
})
```

- [ ] **Step 2: Run the test and watch it fail**

```
cd web && npm test -- src/admin/server/useServerHealth.test.tsx
```

Expected: FAIL - `Failed to resolve import "./useServerHealth"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/admin/server/useServerHealth.ts`:

```ts
import { useQuery } from '@tanstack/react-query'
import { getHealth } from './api'

// A reachability probe, not a dashboard: 30s is enough to notice the listener
// going away, and this endpoint is polled by every open admin tab.
export const HEALTH_POLL_MS = 30_000

export function useServerHealth() {
  return useQuery({
    queryKey: ['server-health'],
    queryFn: getHealth,
    refetchInterval: HEALTH_POLL_MS,
  })
}
```

- [ ] **Step 4: Run the test and watch it pass**

```
cd web && npm test -- src/admin/server/useServerHealth.test.tsx
```

Expected: PASS, 3 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/server/useServerHealth.ts web/src/admin/server/useServerHealth.test.tsx
git commit -m "feat(web): useServerHealth polls /v1/health at 30s"
```

---

### Task 3: useServerConfig

**Files:**
- Create: `web/src/admin/server/useServerConfig.ts`
- Test: `web/src/admin/server/useServerConfig.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/server/useServerConfig.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useServerConfig } from './useServerConfig'
import type { ConfigResponse } from '../../lib/types'

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('caches under ["server-config"]', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ allow_self_register: false })))
  const client = newClient()
  const { result } = renderHook(() => useServerConfig(), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(client.getQueryData<ConfigResponse>(['server-config'])?.allow_self_register).toBe(false)
})

test('does not poll and does not refetch on a remount within the same client', async () => {
  let calls = 0
  server.use(
    http.get('/v1/config', () => {
      calls++
      return HttpResponse.json({ allow_self_register: true })
    }),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)
  const first = renderHook(() => useServerConfig(), { wrapper })
  await waitFor(() => expect(first.result.current.status).toBe('success'))
  expect(calls).toBe(1)

  // The value is read from process env at startup, so it cannot change without a
  // server restart - which also restarts the SPA's own server. Unmount and remount
  // is the real scenario (tab switch), and staleTime: Infinity must make it free.
  first.unmount()
  const second = renderHook(() => useServerConfig(), { wrapper })
  await waitFor(() => expect(second.result.current.status).toBe('success'))

  // Wait past a duration that would actually catch a refetch. useJobStats polls at
  // 3000ms and useWorkerStats at 3000ms; any interval copied from this codebase, or
  // any staleTime default (0) driving a mount refetch, has fired well before 3.2s.
  await new Promise((r) => setTimeout(r, 3_200))
  expect(calls).toBe(1)

  // Positive control on the SAME counter.
  await second.result.current.refetch()
  await waitFor(() => expect(calls).toBe(2))
}, 15_000)
```

- [ ] **Step 2: Run the test and watch it fail**

```
cd web && npm test -- src/admin/server/useServerConfig.test.tsx
```

Expected: FAIL - `Failed to resolve import "./useServerConfig"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/admin/server/useServerConfig.ts`:

```ts
import { useQuery } from '@tanstack/react-query'
import { getServerConfig } from './api'

// AllowSelfRegister is read from process env at startup (internal/api/config.go:9
// reads a Server field set at wiring time), so it cannot change without a server
// restart - and that restart also restarts the process serving this SPA. There is
// nothing to poll for, so this is fetched once per page load and never again.
export function useServerConfig() {
  return useQuery({
    queryKey: ['server-config'],
    queryFn: getServerConfig,
    staleTime: Infinity,
  })
}
```

- [ ] **Step 4: Run the test and watch it pass**

```
cd web && npm test -- src/admin/server/useServerConfig.test.tsx
```

Expected: PASS, 2 tests. The second takes ~3.5s by design.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/server/useServerConfig.ts web/src/admin/server/useServerConfig.test.tsx
git commit -m "feat(web): useServerConfig caches the startup-only self-register flag"
```

---

### Task 4: ErrorStrip

**Files:**
- Create: `web/src/admin/server/ErrorStrip.tsx`
- Test: `web/src/admin/server/ErrorStrip.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/server/ErrorStrip.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ErrorStrip } from './ErrorStrip'

test('renders the error message and a Retry button', () => {
  render(<ErrorStrip message="500 boom" onRetry={() => {}} />)
  expect(screen.getByText('500 boom')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
})

test('Retry calls back exactly once per click', async () => {
  const onRetry = vi.fn()
  render(<ErrorStrip message="500 boom" onRetry={onRetry} />)
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(onRetry).toHaveBeenCalledTimes(1)
})
```

- [ ] **Step 2: Run the test and watch it fail**

```
cd web && npm test -- src/admin/server/ErrorStrip.test.tsx
```

Expected: FAIL - `Failed to resolve import "./ErrorStrip"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/admin/server/ErrorStrip.tsx`:

```tsx
import { Button } from '../../components/Button'
import { GlassPanel } from '../../components/holo'

// The in-place degraded body, shared by the two stat sections and the access panel.
// One component because the copy and the affordance must be identical in all three:
// a region that fails shows what failed and offers a way to try again, and NEVER a
// fabricated value. Same shape as the sibling tabs' error state
// (ReservationsTab.tsx:133-141), scoped to a region rather than the page.
export function ErrorStrip({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <GlassPanel className="flex flex-col items-start gap-2 p-4">
      <div className="text-[12px] text-err">{message}</div>
      <Button className="w-auto px-3 py-1 text-[12px]" onClick={onRetry}>
        Retry
      </Button>
    </GlassPanel>
  )
}
```

- [ ] **Step 4: Run the test and watch it pass**

```
cd web && npm test -- src/admin/server/ErrorStrip.test.tsx
```

Expected: PASS, 2 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/server/ErrorStrip.tsx web/src/admin/server/ErrorStrip.test.tsx
git commit -m "feat(web): shared degraded ErrorStrip for the server tab regions"
```

---

### Task 5: HealthPill

**Files:**
- Create: `web/src/admin/server/HealthPill.tsx`
- Test: `web/src/admin/server/HealthPill.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/server/HealthPill.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { deriveHealthPill, HealthPill } from './HealthPill'

test('loading with no data is CHECKING in the muted tone', () => {
  expect(deriveHealthPill(undefined, null)).toEqual({ text: 'CHECKING', tone: 'text-fg-mute' })
})

test('status ok is HEALTHY in the ok tone', () => {
  expect(deriveHealthPill({ status: 'ok' }, null)).toEqual({ text: 'HEALTHY', tone: 'text-ok' })
})

test('a non-ok status is reported verbatim in the warn tone, never coerced to HEALTHY', () => {
  // The pill must report what the server SAID, not assert health from a 200.
  expect(deriveHealthPill({ status: 'degraded' }, null)).toEqual({
    text: 'DEGRADED',
    tone: 'text-warn',
  })
})

test('an error is UNREACHABLE in the err tone even if stale data exists', () => {
  const err = new Error('500 boom')
  expect(deriveHealthPill(undefined, err)).toEqual({ text: 'UNREACHABLE', tone: 'text-err' })
  expect(deriveHealthPill({ status: 'ok' }, err)).toEqual({
    text: 'UNREACHABLE',
    tone: 'text-err',
  })
})

test('the dot is a separate node so the label is assertable on its own', () => {
  render(<HealthPill data={{ status: 'ok' }} error={null} />)
  expect(screen.getByText('HEALTHY')).toBeInTheDocument()
})

test('renders UNREACHABLE from an error', () => {
  render(<HealthPill data={undefined} error={new Error('500 boom')} />)
  expect(screen.getByText('UNREACHABLE')).toBeInTheDocument()
  expect(screen.queryByText('HEALTHY')).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test and watch it fail**

```
cd web && npm test -- src/admin/server/HealthPill.test.tsx
```

Expected: FAIL - `Failed to resolve import "./HealthPill"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/admin/server/HealthPill.tsx`:

```tsx
import type { HealthResponse } from './api'

// HEALTHY means "the HTTP listener answered", nothing more: handleHealth
// (internal/api/health.go:5-7) performs NO database check. Do not derive this pill
// from the stat queries to make it "smarter" - a green pill next to two degraded
// stat sections is the CORRECT rendering of "server up, Postgres down", and
// ServerTab.test.tsx asserts exactly that.
//
// The error branch wins over stale data: a pill is a liveness claim, and a claim
// backed by a response from 30s ago that has since started failing is not one.
export function deriveHealthPill(
  data: HealthResponse | undefined,
  error: Error | null,
): { text: string; tone: string } {
  if (error) return { text: 'UNREACHABLE', tone: 'text-err' }
  if (!data) return { text: 'CHECKING', tone: 'text-fg-mute' }
  if (data.status === 'ok') return { text: 'HEALTHY', tone: 'text-ok' }
  // Unreachable in production today - handleHealth only ever writes "ok". It is one
  // ternary, and it means a future non-ok status shows up instead of being silently
  // rendered as HEALTHY.
  return { text: String(data.status).toUpperCase(), tone: 'text-warn' }
}

export function HealthPill({
  data,
  error,
}: {
  data: HealthResponse | undefined
  error: Error | null
}) {
  const { text, tone } = deriveHealthPill(data, error)
  return (
    <span className={`flex items-center gap-1.5 font-mono text-[10px] tracking-[0.14em] ${tone}`}>
      {/* The dot is its own node so the label is assertable as an exact string. */}
      <span aria-hidden="true">●</span>
      <span>{text}</span>
    </span>
  )
}
```

- [ ] **Step 4: Run the test and watch it pass**

```
cd web && npm test -- src/admin/server/HealthPill.test.tsx
```

Expected: PASS, 6 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/server/HealthPill.tsx web/src/admin/server/HealthPill.test.tsx
git commit -m "feat(web): health pill reports the server's status verbatim"
```

---

### Task 6: StatSection

**Files:**
- Create: `web/src/admin/server/StatSection.tsx`
- Test: `web/src/admin/server/StatSection.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/server/StatSection.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { StatSection, type StatCell } from './StatSection'

const CELLS: StatCell[] = [
  { label: 'ONLINE', value: 4 },
  { label: 'TOTAL', value: 1234, sub: 'revoked workers excluded', wide: true },
]

const LOADING: StatCell[] = [
  { label: 'ONLINE', value: null },
  { label: 'TOTAL', value: null, sub: 'revoked workers excluded', wide: true },
]

test('renders the caption, the values and the sub-lines', () => {
  render(<StatSection caption="FLEET · GET /v1/workers/stats" cells={CELLS} error={null} onRetry={() => {}} />)
  expect(screen.getByText('FLEET · GET /v1/workers/stats')).toBeInTheDocument()
  expect(screen.getByText('4')).toBeInTheDocument()
  expect(screen.getByText('ONLINE')).toBeInTheDocument()
  expect(screen.getByText('revoked workers excluded')).toBeInTheDocument()
})

test('formats values with toLocaleString, matching the pagination footers', () => {
  render(<StatSection caption="FLEET" cells={CELLS} error={null} onRetry={() => {}} />)
  expect(screen.getByText((1234).toLocaleString())).toBeInTheDocument()
})

test('a null value renders an em dash so the grid does not reflow when data lands', () => {
  render(<StatSection caption="FLEET" cells={LOADING} error={null} onRetry={() => {}} />)
  // Both cells and the caption are present during first paint.
  expect(screen.getByText('FLEET')).toBeInTheDocument()
  expect(screen.getByText('ONLINE')).toBeInTheDocument()
  expect(screen.getAllByText('—')).toHaveLength(2)
})

test('error with NO data replaces the grid in place, keeping the caption', async () => {
  const onRetry = vi.fn()
  render(
    <StatSection caption="FLEET" cells={LOADING} error={new Error('500 boom')} onRetry={onRetry} />,
  )
  expect(screen.getByText('FLEET')).toBeInTheDocument()
  expect(screen.getByText('500 boom')).toBeInTheDocument()
  // No fabricated numbers and no placeholder cells behind the strip.
  expect(screen.queryByText('ONLINE')).not.toBeInTheDocument()
  expect(screen.queryByText('—')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(onRetry).toHaveBeenCalledTimes(1)
})

test('error WITH data keeps the numbers and adds the staleness line', () => {
  render(<StatSection caption="FLEET" cells={CELLS} error={new Error('500 boom')} onRetry={() => {}} />)
  // Blanking good numbers on a dropped poll is worse than marking them stale.
  expect(screen.getByText('4')).toBeInTheDocument()
  expect(screen.getByText((1234).toLocaleString())).toBeInTheDocument()
  expect(screen.getByText('stale · last update failed')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
})

test('no staleness line when there is no error', () => {
  render(<StatSection caption="FLEET" cells={CELLS} error={null} onRetry={() => {}} />)
  expect(screen.queryByText('stale · last update failed')).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test and watch it fail**

```
cd web && npm test -- src/admin/server/StatSection.test.tsx
```

Expected: FAIL - `Failed to resolve import "./StatSection"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/admin/server/StatSection.tsx`:

```tsx
import { Eyebrow, KpiStat } from '../../components/holo'
import { ErrorStrip } from './ErrorStrip'

// value === null means "no data yet" (first paint) - the cell renders an em dash so
// the fixed grid keeps its final size and does not reflow when data lands.
export interface StatCell {
  label: string
  value: number | null
  sub?: string
  wide?: boolean
}

interface StatSectionProps {
  caption: string
  cells: StatCell[]
  error: Error | null
  onRetry: () => void
}

// A bare Eyebrow caption above a grid of KpiStats - NOT a Panel wrapping KpiStats.
// Both Panel and KpiStat wrap GlassPanel, and glass inside glass reads as a
// rendering bug in this palette. This is the WorkerDetailPage treatment
// (WorkerDetailPage.tsx:98-111), so the console inherits a look the app ships.
export function StatSection({ caption, cells, error, onRetry }: StatSectionProps) {
  const hasData = cells.some((c) => c.value !== null)
  return (
    <div className="flex flex-col gap-2">
      <Eyebrow className="border-b border-border pb-1.5 text-[10px] tracking-[0.18em]">
        {caption}
      </Eyebrow>
      {error && !hasData ? (
        <ErrorStrip message={error.message} onRetry={onRetry} />
      ) : (
        <>
          {error && (
            // A poll failed after a good load. Keep the numbers, mark them stale -
            // with a 10s poll a single dropped request is the common case, and
            // blanking correct numbers for it is the worse failure.
            <div className="font-mono text-[10px] tracking-[0.04em] text-warn">
              stale · last update failed
            </div>
          )}
          <div className="grid grid-cols-2 gap-3">
            {cells.map((c) => (
              <div key={c.label} className={c.wide ? 'col-span-2' : undefined}>
                <KpiStat
                  label={c.label}
                  value={c.value === null ? '—' : c.value.toLocaleString()}
                  sub={c.sub}
                />
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run the test and watch it pass**

```
cd web && npm test -- src/admin/server/StatSection.test.tsx
```

Expected: PASS, 6 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/server/StatSection.tsx web/src/admin/server/StatSection.test.tsx
git commit -m "feat(web): StatSection with loading, stale and degraded bodies"
```

---

### Task 7: ServerTab - happy path

**Files:**
- Create: `web/src/admin/server/ServerTab.tsx`
- Test: `web/src/admin/server/ServerTab.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/server/ServerTab.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { ServerTab } from './ServerTab'

// Distinct values per field, so a swapped-field regression (e.g. `stale` rendered
// under OFFLINE) fails instead of passing on a coincidence.
const JOB_STATS = { running: 11, queued: 22, done_24h: 33, failed_24h: 44 }
const WORKER_STATS = { online: 55, stale: 66, offline: 77, disabled: 88, total: 99 }

function handlers({ allowSelfRegister = false }: { allowSelfRegister?: boolean } = {}) {
  return [
    http.get('/v1/jobs/stats', () => HttpResponse.json(JOB_STATS)),
    http.get('/v1/workers/stats', () => HttpResponse.json(WORKER_STATS)),
    http.get('/v1/config', () => HttpResponse.json({ allow_self_register: allowSelfRegister })),
    http.get('/v1/health', () => HttpResponse.json({ status: 'ok' })),
  ]
}

// ServerTab uses no auth context and renders no router Link, so neither
// AuthProvider nor MemoryRouter is needed - only the query client.
function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <ServerTab />
      </QueryClientProvider>,
    ),
  }
}

test('renders all nine counts against their exact fields', async () => {
  server.use(...handlers())
  renderTab()
  expect(await screen.findByText('11')).toBeInTheDocument()
  for (const [label, value] of [
    ['RUNNING', '11'],
    ['QUEUED', '22'],
    ['DONE · 24H', '33'],
    ['FAILED OR CANCELLED · 24H', '44'],
    ['ONLINE', '55'],
    ['STALE', '66'],
    ['OFFLINE', '77'],
    ['DISABLED', '88'],
    ['TOTAL', '99'],
  ] as const) {
    expect(screen.getByText(label)).toBeInTheDocument()
    expect(screen.getByText(value)).toBeInTheDocument()
  }
})

test('the failed bucket is labelled failed OR cancelled', async () => {
  // JobStatusCounts filters status IN ('failed','cancelled')
  // (internal/store/query/jobs.sql:282-292), so "Failed" alone would be wrong.
  server.use(...handlers())
  renderTab()
  expect(await screen.findByText('FAILED OR CANCELLED · 24H')).toBeInTheDocument()
  expect(screen.queryByText('FAILED · 24H')).not.toBeInTheDocument()
})

test('TOTAL carries the revoked-exclusion sub-line and QUEUED says status = pending', async () => {
  server.use(...handlers())
  renderTab()
  expect(await screen.findByText('revoked workers excluded')).toBeInTheDocument()
  expect(screen.getByText('status = pending')).toBeInTheDocument()
})

test('the pill reads HEALTHY when /v1/health says ok', async () => {
  server.use(...handlers())
  renderTab()
  expect(await screen.findByText('HEALTHY')).toBeInTheDocument()
})

test('the self-registration chip reads DISABLED for allow_self_register: false', async () => {
  server.use(...handlers({ allowSelfRegister: false }))
  renderTab()
  expect(await screen.findByText('DISABLED')).toBeInTheDocument()
  expect(screen.getByText('Self-registration')).toBeInTheDocument()
  // The prose must match the flag: handleRegister returns 400 "invite_token is
  // required" when AllowSelfRegister is false (internal/api/auth.go:80-84).
  expect(
    screen.getByText('POST /v1/auth/register requires an invite_token.'),
  ).toBeInTheDocument()
})

test('the self-registration chip reads ENABLED for allow_self_register: true', async () => {
  server.use(...handlers({ allowSelfRegister: true }))
  renderTab()
  expect(await screen.findByText('ENABLED')).toBeInTheDocument()
  expect(
    screen.getByText('POST /v1/auth/register creates a non-admin account without an invite.'),
  ).toBeInTheDocument()
})

test('renders no version, build, uptime, database or environment content', async () => {
  server.use(...handlers())
  const { container } = renderTab()
  await screen.findByText('11')
  // innerHTML, not textContent: an env row rendered into a title or aria-label
  // would be invisible to textContent.
  const html = container.innerHTML
  for (const forbidden of [
    'VERSION',
    'BUILD',
    'UPTIME',
    'RELAY_',
    'postgres://',
    'go1.',
  ]) {
    expect(html).not.toContain(forbidden)
  }
})

test('the footnote states the 24h proxy and that the pill is not a database probe', async () => {
  server.use(...handlers())
  renderTab()
  await screen.findByText('11')
  const footnote = screen.getByTestId('server-footnote').textContent ?? ''
  expect(footnote).toContain('updated_at')
  expect(footnote).toContain('does not check the database')
})
```

- [ ] **Step 2: Run the test and watch it fail**

```
cd web && npm test -- src/admin/server/ServerTab.test.tsx
```

Expected: FAIL - `Failed to resolve import "./ServerTab"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/admin/server/ServerTab.tsx`:

```tsx
import { Chip, Panel } from '../../components/holo'
import { useJobStats } from '../../jobs/useJobStats'
import { useWorkerStats } from '../../workers/useWorkerStats'
import { ErrorStrip } from './ErrorStrip'
import { HealthPill } from './HealthPill'
import { StatSection, type StatCell } from './StatSection'
import { useServerConfig } from './useServerConfig'
import { useServerHealth } from './useServerHealth'

// Passed EXPLICITLY, never defaulted: useJobStats and useWorkerStats default to
// 3000ms for the jobs and workers dashboards, and a change to that default must not
// silently change this tab. 10s is strictly less load than the shipped dashboards,
// so this tab introduces no new worst case for either stats endpoint.
const POLL_MS = 10_000

export function ServerTab() {
  // Reused across module boundaries on purpose: these hooks already own
  // ['job-stats'] and ['workers','stats'], so mounting this tab creates an OBSERVER
  // on the existing cache entries rather than a second client for the same endpoint.
  const jobs = useJobStats(POLL_MS)
  const fleet = useWorkerStats(POLL_MS)
  const health = useServerHealth()
  const config = useServerConfig()

  const jobCells: StatCell[] = [
    { label: 'RUNNING', value: jobs.data?.running ?? null },
    // jobs.status = 'pending' - a JOB count, not "tasks waiting for a slot".
    { label: 'QUEUED', value: jobs.data?.queued ?? null, sub: 'status = pending' },
    { label: 'DONE · 24H', value: jobs.data?.done_24h ?? null },
    { label: 'FAILED OR CANCELLED · 24H', value: jobs.data?.failed_24h ?? null },
  ]

  const fleetCells: StatCell[] = [
    { label: 'ONLINE', value: fleet.data?.online ?? null },
    { label: 'STALE', value: fleet.data?.stale ?? null },
    { label: 'OFFLINE', value: fleet.data?.offline ?? null },
    { label: 'DISABLED', value: fleet.data?.disabled ?? null },
    {
      label: 'TOTAL',
      value: fleet.data?.total ?? null,
      // Every bucket excludes revoked workers, matching GET /v1/workers. Stated on
      // the cell so an admin never reconciles it against the decommissioned list.
      sub: 'revoked workers excluded',
      wide: true,
    },
  ]

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex flex-col">
          <span className="text-[15px] text-fg">Server overview</span>
          <span className="font-mono text-[10.5px] tracking-[0.06em] text-fg-mute">
            Read-only · live aggregates
          </span>
        </div>
        <div className="ml-auto">
          <HealthPill data={health.data} error={health.error as Error | null} />
        </div>
      </div>

      {/* Four independent queries. No query's failure may unmount another's data. */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <StatSection
          caption="JOBS · GET /v1/jobs/stats"
          cells={jobCells}
          error={jobs.error as Error | null}
          onRetry={() => jobs.refetch()}
        />
        <StatSection
          caption="FLEET · GET /v1/workers/stats"
          cells={fleetCells}
          error={fleet.error as Error | null}
          onRetry={() => fleet.refetch()}
        />
      </div>

      <Panel title="Access" meta="GET /v1/config" bodyClassName="flex flex-col gap-2 px-4 py-3">
        {config.error && !config.data ? (
          // NEVER render a default here. A fabricated "DISABLED" would misreport the
          // registration policy, which is a security-relevant claim; a page that
          // reports nothing is strictly better than one that reports a guess.
          <ErrorStrip
            message={(config.error as Error).message}
            onRetry={() => config.refetch()}
          />
        ) : (
          <>
            <div className="flex items-center gap-3 text-[13px] text-fg">
              <span>Self-registration</span>
              {config.data ? (
                <Chip tone={config.data.allow_self_register ? 'accent' : 'muted'}>
                  {config.data.allow_self_register ? 'ENABLED' : 'DISABLED'}
                </Chip>
              ) : (
                <span className="font-mono text-[11px] text-fg-mute">—</span>
              )}
            </div>
            {config.data && (
              <div className="font-mono text-[10px] tracking-[0.04em] text-fg-mute">
                {config.data.allow_self_register
                  ? 'POST /v1/auth/register creates a non-admin account without an invite.'
                  : 'POST /v1/auth/register requires an invite_token.'}
              </div>
            )}
          </>
        )}
      </Panel>

      {/* Plain text, no nested spans, so the wording is assertable as one string. */}
      <div
        data-testid="server-footnote"
        className="font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim"
      >
        ▸ Every value here comes from a named response field of GET /v1/jobs/stats, GET
        /v1/workers/stats or GET /v1/config. Version, build, uptime, database and environment
        values are deliberately absent: no endpoint returns them, and an endpoint that did would
        need a hand-written allowlist of non-secret keys rather than a redacted env dump. The 24h
        buckets are windowed on jobs.updated_at, a finish-time proxy rather than a real finish
        timestamp, so treat them as indicative and not as an audit source. FAILED OR CANCELLED
        counts both statuses. All worker buckets, including TOTAL, exclude revoked workers. The
        health pill reflects HTTP reachability only - GET /v1/health does not check the database,
        so HEALTHY means the listener answered and nothing more.
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run the test and watch it pass**

```
cd web && npm test -- src/admin/server/ServerTab.test.tsx
```

Expected: PASS, 8 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/server/ServerTab.tsx web/src/admin/server/ServerTab.test.tsx
git commit -m "feat(web): ServerTab renders the four live sources"
```

---

### Task 8: ServerTab - the degraded matrix

This is the acceptance criterion of the whole slice: "the request fails" is the behaviour nobody writes down, so it is written down here.

**Files:**
- Modify: `web/src/admin/server/ServerTab.test.tsx` (append; keep the existing helpers)
- Modify (only if a test legitimately fails): `web/src/admin/server/ServerTab.tsx`

- [ ] **Step 1: Write the failing tests**

Append to `web/src/admin/server/ServerTab.test.tsx`. Note the extra imports needed at the top of the file - add `userEvent` and `waitFor`:

```tsx
// Add to the existing imports at the top of the file:
//   import { render, screen, waitFor } from '@testing-library/react'
//   import userEvent from '@testing-library/user-event'

const fail = (path: string) =>
  http.get(path, () => HttpResponse.json({ error: 'boom' }, { status: 500 }))

test('a jobs/stats 500 degrades ONLY the jobs section', async () => {
  server.use(...handlers(), fail('/v1/jobs/stats'))
  renderTab()
  // The fleet numbers, the chip and the pill all survive.
  expect(await screen.findByText('55')).toBeInTheDocument()
  expect(screen.getByText('99')).toBeInTheDocument()
  expect(screen.getByText('DISABLED')).toBeInTheDocument()
  expect(screen.getByText('HEALTHY')).toBeInTheDocument()
  // The jobs section shows the strip, and no jobs number is on screen.
  expect(screen.getByText('500 boom')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  expect(screen.queryByText('RUNNING')).not.toBeInTheDocument()
  expect(screen.queryByText('11')).not.toBeInTheDocument()
})

test('Retry issues exactly one more jobs/stats request and restores the grid', async () => {
  let calls = 0
  server.use(
    ...handlers(),
    http.get('/v1/jobs/stats', () => {
      calls++
      return calls === 1
        ? HttpResponse.json({ error: 'boom' }, { status: 500 })
        : HttpResponse.json(JOB_STATS)
    }),
  )
  renderTab()
  await screen.findByText('500 boom')
  expect(calls).toBe(1)
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('11')).toBeInTheDocument()
  expect(screen.getByText('RUNNING')).toBeInTheDocument()
  expect(screen.queryByText('500 boom')).not.toBeInTheDocument()
  expect(calls).toBe(2)
})

test('a workers/stats 500 degrades ONLY the fleet section', async () => {
  server.use(...handlers(), fail('/v1/workers/stats'))
  renderTab()
  expect(await screen.findByText('11')).toBeInTheDocument()
  expect(screen.getByText('44')).toBeInTheDocument()
  expect(screen.getByText('DISABLED')).toBeInTheDocument()
  expect(screen.getByText('HEALTHY')).toBeInTheDocument()
  expect(screen.getByText('500 boom')).toBeInTheDocument()
  expect(screen.queryByText('ONLINE')).not.toBeInTheDocument()
  expect(screen.queryByText('99')).not.toBeInTheDocument()
})

test('a config 500 renders NO self-registration chip in either state', async () => {
  server.use(...handlers(), fail('/v1/config'))
  renderTab()
  expect(await screen.findByText('11')).toBeInTheDocument()
  expect(screen.getByText('55')).toBeInTheDocument()
  // Absence of BOTH, so a fabricated default cannot pass this test.
  expect(screen.queryByText('ENABLED')).not.toBeInTheDocument()
  expect(screen.queryByText('DISABLED')).not.toBeInTheDocument()
  expect(screen.getByText('Access')).toBeInTheDocument()
  expect(screen.getByText('500 boom')).toBeInTheDocument()
})

test('a health 500 shows UNREACHABLE and nothing else changes', async () => {
  server.use(...handlers(), fail('/v1/health'))
  renderTab()
  expect(await screen.findByText('UNREACHABLE')).toBeInTheDocument()
  for (const v of ['11', '22', '33', '44', '55', '66', '77', '88', '99']) {
    expect(screen.getByText(v)).toBeInTheDocument()
  }
  expect(screen.getByText('DISABLED')).toBeInTheDocument()
})

test('the realistic outage: health ok while BOTH stats endpoints 500', async () => {
  // Postgres down, server up. The pill is a listener probe, not a database probe,
  // so HEALTHY here is CORRECT - and this test fails loudly if anyone later derives
  // the pill from the stat queries to make it look smarter.
  server.use(...handlers(), fail('/v1/jobs/stats'), fail('/v1/workers/stats'))
  renderTab()
  expect(await screen.findByText('HEALTHY')).toBeInTheDocument()
  expect(screen.getAllByText('500 boom')).toHaveLength(2)
  expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(2)
  expect(screen.queryByText('RUNNING')).not.toBeInTheDocument()
  expect(screen.queryByText('ONLINE')).not.toBeInTheDocument()
})

test('all four failing still renders the header, both captions, the panel and the footnote', async () => {
  server.use(
    fail('/v1/jobs/stats'),
    fail('/v1/workers/stats'),
    fail('/v1/config'),
    fail('/v1/health'),
  )
  renderTab()
  expect(await screen.findByText('UNREACHABLE')).toBeInTheDocument()
  expect(screen.getByText('Server overview')).toBeInTheDocument()
  expect(screen.getByText('JOBS · GET /v1/jobs/stats')).toBeInTheDocument()
  expect(screen.getByText('FLEET · GET /v1/workers/stats')).toBeInTheDocument()
  expect(screen.getByText('Access')).toBeInTheDocument()
  expect(screen.getByTestId('server-footnote')).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(3)
})

test('a poll that fails AFTER a good load keeps the numbers and marks them stale', async () => {
  let calls = 0
  server.use(
    ...handlers(),
    http.get('/v1/jobs/stats', () => {
      calls++
      return calls === 1
        ? HttpResponse.json(JOB_STATS)
        : HttpResponse.json({ error: 'boom' }, { status: 500 })
    }),
  )
  const { client } = renderTab()
  expect(await screen.findByText('11')).toBeInTheDocument()

  // Drive the second fetch explicitly rather than waiting out the 10s interval.
  await client.refetchQueries({ queryKey: ['job-stats'] })

  await waitFor(() => expect(screen.getByText('stale · last update failed')).toBeInTheDocument())
  // The numbers are STILL on screen - a dropped poll must not blank good data.
  expect(screen.getByText('11')).toBeInTheDocument()
  expect(screen.getByText('44')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the tests and watch them fail (or confirm which already pass)**

```
cd web && npm test -- src/admin/server/ServerTab.test.tsx
```

Expected: most of these should PASS immediately, because Task 7's implementation already encodes per-region degradation. **That is not a free pass.** For every test that passes on the first run, prove it is non-vacuous by temporarily breaking the behaviour it claims to protect, watching it go RED, then reverting. The three checks that matter most:

1. In `ServerTab.tsx`, temporarily replace the two `StatSection` elements with a single page-level `{jobs.error || fleet.error ? <ErrorStrip .../> : ...}` and confirm the "degrades ONLY the jobs section" and "realistic outage" tests fail. Revert.
2. Temporarily change `HealthPill`'s call site to `error={(health.error ?? jobs.error) as Error | null}` and confirm "the realistic outage" fails. Revert.
3. Temporarily change the access panel's chip branch to `{config.data?.allow_self_register ? <Chip>ENABLED</Chip> : <Chip>DISABLED</Chip>}` and confirm "a config 500 renders NO self-registration chip" fails. Revert.

Record the observed RED output for each in the task notes. Any test that cannot be made to fail this way is measuring nothing - rewrite it.

- [ ] **Step 3: Fix any genuine failure**

If a test fails for a real reason, the likely causes and their fixes:

- `expect(screen.queryByText('500 boom')).not.toBeInTheDocument()` after Retry fails: TanStack keeps `error` set until the refetch resolves successfully. It clears on success, so if this fails, the assertion ran before the refetch settled - wrap it in `await waitFor(...)`.
- `getAllByText('500 boom')` finds three instead of two in the "realistic outage" test: `handlers()` is spread first and `fail(...)` after, so the later handler wins for the two stats paths only. Confirm `/v1/config` is still succeeding.

Make the minimal change and re-run. Do not change `ServerTab.tsx`'s degradation structure to satisfy a test - the structure is the requirement.

- [ ] **Step 4: Run the whole module and watch it pass**

```
cd web && npm test -- src/admin/server/
```

Expected: PASS, all files in the module.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/server/ServerTab.test.tsx web/src/admin/server/ServerTab.tsx
git commit -m "test(web): per-region degradation matrix for the server tab"
```

---

### Task 9: Register the tab and correct the stale AdminPage comment

**Files:**
- Modify: `web/src/admin/tabs.ts:1-24`
- Modify: `web/src/admin/AdminPage.tsx:6-9`
- Test: `web/src/admin/AdminPage.test.tsx` (append two tests, extend `renderAt`'s handlers)

- [ ] **Step 1: Write the failing tests**

In `web/src/admin/AdminPage.test.tsx`, add the four server-tab handlers inside the existing `server.use(...)` call in `renderAt` (after the `/v1/reservations` handler, before the closing paren):

```tsx
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ running: 11, queued: 22, done_24h: 33, failed_24h: 44 }),
    ),
    http.get('/v1/workers/stats', () =>
      HttpResponse.json({ online: 55, stale: 66, offline: 77, disabled: 88, total: 99 }),
    ),
    http.get('/v1/config', () => HttpResponse.json({ allow_self_register: false })),
    http.get('/v1/health', () => HttpResponse.json({ status: 'ok' })),
```

Then append these two tests at the end of the file:

```tsx
test('/admin/server renders the server panel inside the same shell and marks the pill active', async () => {
  renderAt('/admin/server')
  expect(screen.getByText('SETTINGS · ADMIN ONLY')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
  expect(await screen.findByText('Server overview')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Server' })).toHaveAttribute('aria-current', 'page')
})

test('/admin/users still renders its own panel, not the server overview', async () => {
  renderAt('/admin/users')
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.queryByText('Server overview')).not.toBeInTheDocument()
})
```

Note: the existing `renders no unbacked server-facts strip` test renders `/admin/users` and asserts `VERSION` / `BUILD` / `DB` / `UPTIME` are absent. It stays exactly as it is and must still pass - the strip is omitted on the shell and on the new tab alike.

- [ ] **Step 2: Run the tests and watch them fail**

```
cd web && npm test -- src/admin/AdminPage.test.tsx
```

Expected: FAIL. `/admin/server` is not in `ADMIN_TABS`, so `findAdminTab` returns undefined and the page redirects to `/admin/users`; the failure is `Unable to find an element with the text: Server overview`, and `getByRole('link', { name: 'Server' })` also fails.

- [ ] **Step 3: Write the minimal implementation**

Edit `web/src/admin/tabs.ts` - add the import and the entry, and drop the now-satisfied backlog reference from the comment:

```ts
import type { ComponentType } from 'react'
import { EnrollmentsTab } from './enrollments/EnrollmentsTab'
import { ReservationsTab } from './reservations/ReservationsTab'
import { ServerTab } from './server/ServerTab'
import { UsersTab } from './users/UsersTab'

export interface AdminTab {
  slug: string
  label: string
  Panel: ComponentType
}

// The admin console is a registry plus a switch. Tabs that are not built yet are
// ABSENT on purpose: an unknown /admin/:tab segment redirects to /admin/users
// instead of rendering an empty panel, so this slice cannot ship dead tabs.
// Adding a tab later is one entry here - see
// docs/backlog/feature-2026-08-08-admin-invites-tab.md, which stays blocked on a
// GET /v1/invites that does not exist.
// Order matches the hi-fi's tab order (Invites, still absent, sits between Users and
// Agent enrolls).
export const ADMIN_TABS: AdminTab[] = [
  { slug: 'users', label: 'Users', Panel: UsersTab },
  { slug: 'enrollments', label: 'Agent enrolls', Panel: EnrollmentsTab },
  { slug: 'reservations', label: 'Reservations', Panel: ReservationsTab },
  { slug: 'server', label: 'Server', Panel: ServerTab },
]
```

`DEFAULT_ADMIN_TAB` and `findAdminTab` are unchanged.

Edit `web/src/admin/AdminPage.tsx` lines 6-9 - comment only, no behaviour change:

```tsx
// The admin shell. The hi-fi's right-aligned VERSION / BUILD / DB / UPTIME strip is
// omitted, and that is a decision rather than a deferral: no endpoint returns build
// or uptime facts (GET /v1/health returns {"status":"ok"} and does not check the
// database; GET /v1/config returns only {allow_self_register}). The Server tab
// (admin/server/ServerTab.tsx) ships without it for the same reason. Reviving the
// strip requires a new admin-gated endpoint returning a hand-written ALLOWLIST of
// non-secret config keys - see
// docs/backlog/feature-2026-08-09-server-info-allowlist-endpoint.md - never a
// redacted dump of os.Environ().
```

- [ ] **Step 4: Run the tests and watch them pass**

```
cd web && npm test -- src/admin/AdminPage.test.tsx src/admin/AdminTabs.test.tsx
```

Expected: PASS. If `AdminTabs.test.tsx` asserts an exact tab count or an exact label list, update that assertion to include `Server` in the same run - that is the registry test doing its job, not a regression.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/tabs.ts web/src/admin/AdminPage.tsx web/src/admin/AdminPage.test.tsx web/src/admin/AdminTabs.test.tsx
git commit -m "feat(web): register the admin Server tab and correct the stale shell comment"
```

---

### Task 10: Full suite, typecheck, build, and dist hygiene

**Files:** none created or modified by this task, except possibly a test assertion that the full-suite run reveals.

- [ ] **Step 1: Run the whole web suite**

```
cd web && npm test
```

Expected: PASS, with the module's new tests added to the existing total. A failure outside `src/admin/` means this slice broke a shared assumption - most plausibly a test that asserts an exact `ADMIN_TABS` length or an exact nav label set. Fix the assertion to include `Server`; do not delete the test.

- [ ] **Step 2: Typecheck and build**

```
cd web && npm run build
```

Expected: `tsc -b` clean, then a successful `vite build`. Two type errors are foreseeable and both are real:
- `jobs.error` / `fleet.error` are `Error | null` already in TanStack v5 with the default error type, so the `as Error | null` casts are belt-and-braces; if `tsc` flags them as unnecessary, remove the cast rather than the prop.
- `KpiStat`'s `sub` is `ReactNode | undefined`, so passing `c.sub` (possibly `undefined`) is fine.

- [ ] **Step 3: Revert the build output**

`web/dist` is tracked but stale from the scaffold, and the build in Step 2 dirties it.

```bash
git checkout -- web/dist/
git status --porcelain
```

Expected: `git status --porcelain` prints nothing. If it prints anything at all, that file was not meant to change - investigate before proceeding.

- [ ] **Step 4: Verify the change set is exactly what the spec allows**

```bash
git diff --stat origin/main...HEAD
```

Expected: every path is under `web/src/admin/`. No Go file, no `.sql`, no `.proto`, no migration, no `web/dist`, nothing under `web/src/jobs/`, `web/src/workers/`, `web/src/lib/` or `web/src/components/`.

- [ ] **Step 5: Commit (only if Steps 1-2 required a fix)**

```bash
git add web/src/admin/
git commit -m "test(web): align the admin registry assertions with the Server tab"
```

If nothing changed, there is nothing to commit and the slice is complete.

---

## Acceptance criteria traceability

| Spec criterion | Task |
|---|---|
| 1. `/admin/server` renders under the existing gate, one `ADMIN_TABS` entry, no router/shell change | Task 9 |
| 2. Four job counts, five worker counts, the self-register flag, the health pill | Task 7 |
| 3. Every value traces to a named field; no version/build/uptime/db/env content | Task 7 (`renders no version, build, uptime, database or environment content`) |
| 4. `failed_24h` labelled failed **or cancelled**; TOTAL states revoked exclusion; footnote states the `updated_at` proxy and that the pill is not a database probe | Task 7 |
| 5. Any one query failing degrades only its own region, each with a working Retry | Task 8 |
| 6. A stat query failing after a good load keeps its numbers and marks them stale | Tasks 6 and 8 |
| 7. `/v1/config` failing renders no chip in either state | Task 8 |
| 8. `AdminPage.tsx`'s stale comment corrected | Task 9 |
| 9. `npm test` and the production build green; nothing outside `web/src/admin/`; no backend change; `web/dist` reverted | Task 10 |

## Notes for the reviewer

- The pill deliberately reports `HEALTHY` while both stat sections are degraded. That combination has a dedicated test and is the design intent, not an oversight: `handleHealth` (`internal/api/health.go:5-7`) does not touch the database.
- `web/src/auth/RegisterScreen.tsx:21-25` still calls `apiFetch<ConfigResponse>('/config')` inline. It is intentionally NOT refactored onto `useServerConfig`: the two call sites want different semantics (a one-shot with a fail-closed `false` fallback on the sign-up path, versus a cached query with a visible error state here), and touching the sign-up path is unrelated churn.
- The frontend generation-ordering invariant is not exercised anywhere in this slice: there is no `AbortController`, no SSE stream and no hand-rolled async lifecycle. Every request is a TanStack query whose cancellation TanStack owns. Any future "live" upgrade of this tab that hand-rolls a stream would need that invariant applied.
