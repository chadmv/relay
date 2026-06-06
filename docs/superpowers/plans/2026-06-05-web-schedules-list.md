# Web Schedules List Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `/schedules` list page of the Relay web front end against the real `GET /v1/scheduled-jobs` endpoint, with run-now and enable/disable row actions, plus a small backend addition exposing `owner_email`.

**Architecture:** A new `web/src/schedules/` React feature module mirroring the shipped `web/src/workers/` module (TanStack Query polling, `keepPreviousData`, skeleton/error/empty states). One backend change adds `owner_email` to the scheduled-job list response via a batch user lookup (existing list queries untouched). Read + two mutations (run-now, enable/disable); no detail page, no filters/search.

**Tech Stack:** Go (sqlc, pgx, net/http), Postgres; React 18 + Vite + TypeScript + Tailwind v4 + TanStack Query; Vitest + Testing Library + MSW. Backend integration tests use testcontainers (`//go:build integration`).

**Reference spec:** `docs/superpowers/specs/2026-06-05-web-schedules-list-design.md`

---

## Task 1: Backend - `GetUserEmailsByIDs` query

**Files:**
- Modify: `internal/store/query/users.sql` (append a query)
- Generated (do not hand-edit): `internal/store/users.sql.go`, `internal/store/querier.go`

- [ ] **Step 1: Add the sqlc query**

Append to `internal/store/query/users.sql`:

```sql
-- name: GetUserEmailsByIDs :many
SELECT id, email FROM users WHERE id = ANY($1::uuid[]);
```

- [ ] **Step 2: Regenerate the store layer**

Run: `make generate`
Expected: `internal/store/users.sql.go` gains a `GetUserEmailsByIDs` method returning a row type with `ID pgtype.UUID` and `Email string`; `querier.go` gains the interface method. No other intended files change.

Note (Windows): `sqlc generate` may rewrite unrelated `internal/store/*.sql.go` files with line-ending-only diffs. Before committing, restore those: `git checkout -- internal/store/` for everything except `users.sql.go`, `querier.go`, and `query/users.sql`. Verify with `git diff --stat`.

- [ ] **Step 3: Confirm it compiles**

Run: `go build ./...`
Expected: builds clean.

- [ ] **Step 4: Commit**

```bash
git add internal/store/query/users.sql internal/store/users.sql.go internal/store/querier.go
git commit -m "store: add GetUserEmailsByIDs query"
```

---

## Task 2: Backend - `owner_email` on the list response

**Files:**
- Modify: `internal/api/scheduled_jobs.go` (response struct + both list dispatch paths)
- Test: `internal/api/scheduled_jobs_owner_email_integration_test.go` (create)

The single-resource handlers (`handleGetScheduledJob`, etc.) intentionally do **not** populate `owner_email` in this slice - only the list endpoint needs it. `toScheduledJobResponse` leaves `OwnerEmail` as the zero value; the list handler fills it in after building items.

- [ ] **Step 1: Write the failing integration test**

Create `internal/api/scheduled_jobs_owner_email_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Admin list view: each row carries the correct owner's email.
func TestListScheduledJobs_OwnerEmail_AdminMultiOwner(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjmail-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	owner1 := createTestUser(t, q, "Owner1", "sjmail-owner1@test.com", false)
	owner2 := createTestUser(t, q, "Owner2", "sjmail-owner2@test.com", false)

	seedScheduledJob(t, pool, "mail-sched-a", uuidString(owner1.ID),
		time.Now().Add(1*time.Hour), time.Now())
	seedScheduledJob(t, pool, "mail-sched-b", uuidString(owner2.ID),
		time.Now().Add(2*time.Hour), time.Now())

	code, p := getScheduledJobsPage(t, srv, adminToken, "sort=name&limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 2)

	byName := map[string]string{}
	for _, it := range p.Items {
		byName[it["name"].(string)] = it["owner_email"].(string)
	}
	require.Equal(t, "sjmail-owner1@test.com", byName["mail-sched-a"])
	require.Equal(t, "sjmail-owner2@test.com", byName["mail-sched-b"])
}

// Owner-scoped view: the caller's email on their own rows.
func TestListScheduledJobs_OwnerEmail_OwnerScoped(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	alice := createTestUser(t, q, "Alice", "sjmail-alice@test.com", false)
	aliceToken := createTestToken(t, q, alice.ID)

	seedScheduledJob(t, pool, "alice-sched", uuidString(alice.ID),
		time.Now().Add(1*time.Hour), time.Now())

	code, p := getScheduledJobsPage(t, srv, aliceToken, "sort=name&limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	require.Equal(t, "sjmail-alice@test.com", p.Items[0]["owner_email"].(string))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListScheduledJobs_OwnerEmail -v -timeout 180s`
Expected: FAIL - `owner_email` is missing (nil type assertion panic / empty string), because the field does not exist yet.

- [ ] **Step 3: Add the response field**

In `internal/api/scheduled_jobs.go`, add to `scheduledJobResponse` (after `OwnerID`):

```go
	OwnerEmail    string          `json:"owner_email"`
```

- [ ] **Step 4: Add a helper to populate emails, and call it from both list paths**

In `internal/api/scheduled_jobs.go`, add this helper:

```go
// fillOwnerEmails resolves owner_email for a page of items. For the owner-scoped
// path every item belongs to the caller, so pass selfEmail to skip the lookup.
// For the admin path pass selfEmail == "" to batch-resolve from the store.
func (s *Server) fillOwnerEmails(r *http.Request, items []scheduledJobResponse, selfEmail string) {
	if selfEmail != "" {
		for i := range items {
			items[i].OwnerEmail = selfEmail
		}
		return
	}
	ids := make([]pgtype.UUID, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		if !seen[it.OwnerID] {
			seen[it.OwnerID] = true
			id, err := parseUUID(it.OwnerID)
			if err == nil {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := s.q.GetUserEmailsByIDs(r.Context(), ids)
	if err != nil {
		return // best-effort: leave owner_email empty on lookup failure
	}
	emailByID := make(map[string]string, len(rows))
	for _, row := range rows {
		emailByID[uuidStr(row.ID)] = row.Email
	}
	for i := range items {
		items[i].OwnerEmail = emailByID[items[i].OwnerID]
	}
}
```

In `handleListScheduledJobs`, in the **admin** branch, immediately before `writeJSON(w, http.StatusOK, page[...]{...})` (after `total` is computed):

```go
	s.fillOwnerEmails(r, items, "")
```

In the **non-admin (owner-scoped)** branch, immediately before its `writeJSON`:

```go
	s.fillOwnerEmails(r, items, u.Email)
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListScheduledJobs_OwnerEmail -v -timeout 180s`
Expected: PASS (both subtests).

- [ ] **Step 6: Run the full scheduled-jobs integration suite for no regressions**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListScheduledJobs -v -timeout 240s`
Expected: PASS (sort, pagination, owner-email tests all green).

- [ ] **Step 7: Commit**

```bash
git add internal/api/scheduled_jobs.go internal/api/scheduled_jobs_owner_email_integration_test.go
git commit -m "api: add owner_email to scheduled-job list response"
```

---

## Task 3: Frontend - API client (`schedules/api.ts`)

**Files:**
- Create: `web/src/schedules/api.ts`
- Test: `web/src/schedules/api.test.ts`

All `web` commands run from the `web/` directory. Run tests with `npm test -- --run <path>`.

- [ ] **Step 1: Write the failing test**

Create `web/src/schedules/api.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import { listSchedules, runScheduleNow, setScheduleEnabled, type SchedulesPage } from './api'

const emptyPage: SchedulesPage = { items: [], next_cursor: '', total: 0 }

test('listSchedules requests the first page with sort and limit=50, no cursor', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listSchedules('name')
  expect(captured?.get('sort')).toBe('name')
  expect(captured?.get('limit')).toBe('50')
  expect(captured?.get('cursor')).toBeNull()
})

test('listSchedules includes the cursor when provided', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listSchedules('-created_at', 'CUR123')
  expect(captured?.get('cursor')).toBe('CUR123')
})

test('listSchedules parses the page payload', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () =>
      HttpResponse.json({
        items: [{ id: 's1', name: 'nightly', owner_email: 'a@b.com', enabled: true }],
        next_cursor: 'abc',
        total: 1,
      }),
    ),
  )
  const page = await listSchedules('-created_at')
  expect(page.total).toBe(1)
  expect(page.items[0].name).toBe('nightly')
})

test('listSchedules throws ApiError on the error envelope', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () => HttpResponse.json({ error: 'boom' }, { status: 500 })),
  )
  await expect(listSchedules('-created_at')).rejects.toBeInstanceOf(ApiError)
})

test('runScheduleNow POSTs to the run-now path', async () => {
  let method: string | undefined
  let path: string | undefined
  server.use(
    http.post('/v1/scheduled-jobs/s1/run-now', ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      return HttpResponse.json({ id: 'job1' }, { status: 201 })
    }),
  )
  await runScheduleNow('s1')
  expect(method).toBe('POST')
  expect(path).toBe('/v1/scheduled-jobs/s1/run-now')
})

test('setScheduleEnabled PATCHes the enabled flag', async () => {
  let body: unknown
  server.use(
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 's1', enabled: false })
    }),
  )
  await setScheduleEnabled('s1', false)
  expect(body).toEqual({ enabled: false })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run (from `web/`): `npm test -- --run src/schedules/api.test.ts`
Expected: FAIL - cannot resolve `./api` (module not created).

- [ ] **Step 3: Implement the API client**

Create `web/src/schedules/api.ts`:

```ts
import { apiFetch } from '../lib/api'

// Matches the Go scheduledJobResponse field-for-field. job_spec is raw JSON and
// is not rendered in the list, so it stays `unknown`.
export interface Schedule {
  id: string
  name: string
  owner_id: string
  owner_email: string
  cron_expr: string
  timezone: string
  job_spec: unknown
  overlap_policy: string
  enabled: boolean
  next_run_at: string
  last_run_at?: string
  last_job_id?: string
  created_at: string
  updated_at: string
}

export interface SchedulesPage {
  items: Schedule[]
  next_cursor: string
  total: number
}

export type ScheduleSort =
  | '-created_at'
  | 'created_at'
  | 'name'
  | '-name'
  | 'next_run_at'
  | '-next_run_at'
  | 'updated_at'
  | '-updated_at'

// One page (limit=50). cursor advances to the next page when present.
export function listSchedules(sort: ScheduleSort, cursor?: string): Promise<SchedulesPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  if (cursor) q.set('cursor', cursor)
  return apiFetch<SchedulesPage>(`/scheduled-jobs?${q}`)
}

// Submits a fresh job from the stored job_spec. Allowed for the owner or an admin.
export function runScheduleNow(id: string): Promise<unknown> {
  return apiFetch(`/scheduled-jobs/${id}/run-now`, { method: 'POST' })
}

// Toggles the enabled flag via PATCH.
export function setScheduleEnabled(id: string, enabled: boolean): Promise<Schedule> {
  return apiFetch<Schedule>(`/scheduled-jobs/${id}`, { method: 'PATCH', json: { enabled } })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run (from `web/`): `npm test -- --run src/schedules/api.test.ts`
Expected: PASS (all 6 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/api.ts web/src/schedules/api.test.ts
git commit -m "web: schedules API client"
```

---

## Task 4: Frontend - format helpers (`schedules/format.ts`)

**Files:**
- Create: `web/src/schedules/format.ts`
- Test: `web/src/schedules/format.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/schedules/format.test.ts`:

```ts
import { expect, test } from 'vitest'
import { formatRelativeTime, nextRunDisplay, shortId } from './format'

const now = new Date('2026-06-05T12:00:00Z')

test('formatRelativeTime renders past times as "ago"', () => {
  expect(formatRelativeTime('2026-06-05T11:55:00Z', now)).toBe('5m ago')
  expect(formatRelativeTime('2026-06-05T11:59:30Z', now)).toBe('30s ago')
  expect(formatRelativeTime('2026-06-05T10:00:00Z', now)).toBe('2h ago')
})

test('nextRunDisplay renders future times as "in"', () => {
  expect(nextRunDisplay('2026-06-05T12:07:00Z', now)).toBe('in 7m')
  expect(nextRunDisplay('2026-06-05T12:00:30Z', now)).toBe('in 30s')
  expect(nextRunDisplay('2026-06-05T14:00:00Z', now)).toBe('in 2h')
})

test('nextRunDisplay renders past/now as "due"', () => {
  expect(nextRunDisplay('2026-06-05T11:59:00Z', now)).toBe('due')
})

test('shortId takes the first 8 chars', () => {
  expect(shortId('abcdef12-3456-7890-abcd-ef1234567890')).toBe('abcdef12')
  expect(shortId('')).toBe('-')
  expect(shortId(undefined)).toBe('-')
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run (from `web/`): `npm test -- --run src/schedules/format.test.ts`
Expected: FAIL - cannot resolve `./format`.

- [ ] **Step 3: Implement the helpers**

Create `web/src/schedules/format.ts`:

```ts
// Relative "Xs/m/h/d ago" for a past timestamp. Mirrors the Workers helper.
export function formatRelativeTime(iso: string, now: Date = new Date()): string {
  const secs = Math.max(0, Math.round((now.getTime() - new Date(iso).getTime()) / 1000))
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

// Forward-looking "in Xs/m/h/d" for next_run_at; "due" once the time has passed.
export function nextRunDisplay(iso: string, now: Date = new Date()): string {
  const secs = Math.round((new Date(iso).getTime() - now.getTime()) / 1000)
  if (secs <= 0) return 'due'
  if (secs < 60) return `in ${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `in ${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `in ${hours}h`
  return `in ${Math.floor(hours / 24)}d`
}

// First 8 chars of a UUID, or "-" when absent.
export function shortId(id: string | undefined): string {
  if (!id) return '-'
  return id.slice(0, 8)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run (from `web/`): `npm test -- --run src/schedules/format.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/format.ts web/src/schedules/format.test.ts
git commit -m "web: schedules format helpers"
```

---

## Task 5: Frontend - data hook (`schedules/useSchedules.ts`)

**Files:**
- Create: `web/src/schedules/useSchedules.ts`
- Test: `web/src/schedules/useSchedules.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/schedules/useSchedules.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useSchedules } from './useSchedules'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches schedules and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/scheduled-jobs', () => {
      count++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )

  renderHook(() => useSchedules('-created_at', undefined, 20), { wrapper })

  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
})

test('passes the cursor through to the request', async () => {
  let cursor: string | null = null
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      cursor = new URL(request.url).searchParams.get('cursor')
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )

  renderHook(() => useSchedules('-created_at', 'PAGE2', 20), { wrapper })
  await waitFor(() => expect(cursor).toBe('PAGE2'))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run (from `web/`): `npm test -- --run src/schedules/useSchedules.test.tsx`
Expected: FAIL - cannot resolve `./useSchedules`.

- [ ] **Step 3: Implement the hook**

Create `web/src/schedules/useSchedules.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listSchedules, type ScheduleSort } from './api'

// Polls one page of schedules. keepPreviousData avoids flashing empty on
// re-sort/paging and between polls. Schedules are low-churn, so the default
// interval is 10s (tests inject a small value). The relative "next run"
// countdown is ticked client-side by the page, not by this poll.
export function useSchedules(sort: ScheduleSort, cursor?: string, intervalMs = 10000) {
  return useQuery({
    queryKey: ['schedules', sort, cursor ?? ''],
    queryFn: () => listSchedules(sort, cursor),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run (from `web/`): `npm test -- --run src/schedules/useSchedules.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/useSchedules.ts web/src/schedules/useSchedules.test.tsx
git commit -m "web: schedules polling hook"
```

---

## Task 6: Frontend - action mutations (`schedules/useScheduleActions.ts`)

**Files:**
- Create: `web/src/schedules/useScheduleActions.ts`
- Test: `web/src/schedules/useScheduleActions.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/schedules/useScheduleActions.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useScheduleActions } from './useScheduleActions'

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

test('runNow POSTs run-now and invalidates the schedules query', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/scheduled-jobs/s1/run-now', () => HttpResponse.json({ id: 'job1' }, { status: 201 })),
  )

  const { result } = renderHook(() => useScheduleActions(), { wrapper: makeWrapper(client) })
  await result.current.runNow.mutateAsync('s1')

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['schedules'] }))
})

test('setEnabled PATCHes and invalidates the schedules query', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.patch('/v1/scheduled-jobs/s1', () => HttpResponse.json({ id: 's1', enabled: false })),
  )

  const { result } = renderHook(() => useScheduleActions(), { wrapper: makeWrapper(client) })
  await result.current.setEnabled.mutateAsync({ id: 's1', enabled: false })

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['schedules'] }))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run (from `web/`): `npm test -- --run src/schedules/useScheduleActions.test.tsx`
Expected: FAIL - cannot resolve `./useScheduleActions`.

- [ ] **Step 3: Implement the mutations**

Create `web/src/schedules/useScheduleActions.ts`:

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { runScheduleNow, setScheduleEnabled } from './api'

// Mutations for the row actions. Both invalidate the schedules list on success so
// the table reflects the new state on the next render without waiting for a poll.
export function useScheduleActions() {
  const qc = useQueryClient()

  const runNow = useMutation({
    mutationFn: (id: string) => runScheduleNow(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  const setEnabled = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => setScheduleEnabled(id, enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  return { runNow, setEnabled }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run (from `web/`): `npm test -- --run src/schedules/useScheduleActions.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/useScheduleActions.ts web/src/schedules/useScheduleActions.test.tsx
git commit -m "web: schedules action mutations"
```

---

## Task 7: Frontend - table (`schedules/SchedulesTable.tsx`)

**Files:**
- Create: `web/src/schedules/SchedulesTable.tsx`
- Test: `web/src/schedules/SchedulesTable.test.tsx`

The table is presentational: it receives rows, the pending-action id, and action callbacks. "Run now" is always shown; the second button is "Disable" when enabled or "Enable" when disabled. Buttons disable while a mutation targeting that row is pending.

- [ ] **Step 1: Write the failing test**

Create `web/src/schedules/SchedulesTable.test.tsx`:

```tsx
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { render } from '@testing-library/react'
import { expect, test, vi } from 'vitest'
import { SchedulesTable } from './SchedulesTable'
import type { Schedule } from './api'

function sched(over: Partial<Schedule> = {}): Schedule {
  return {
    id: 's1',
    name: 'nightly-build',
    owner_id: 'o1',
    owner_email: 'dev@studio.com',
    cron_expr: '0 2 * * *',
    timezone: 'UTC',
    job_spec: {},
    overlap_policy: 'skip',
    enabled: true,
    next_run_at: '2099-01-01T00:00:00Z',
    last_run_at: '2026-06-05T11:00:00Z',
    last_job_id: 'abcdef12-3456-7890-abcd-ef1234567890',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-05T11:00:00Z',
    ...over,
  }
}

test('renders core columns', () => {
  render(<SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByText('nightly-build')).toBeInTheDocument()
  expect(screen.getByText('0 2 * * *')).toBeInTheDocument()
  expect(screen.getByText('dev@studio.com')).toBeInTheDocument()
  expect(screen.getByText('abcdef12')).toBeInTheDocument() // short last_job_id
})

test('enabled row shows Run now + Disable', () => {
  render(<SchedulesTable schedules={[sched({ enabled: true })]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByRole('button', { name: 'Run now' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
})

test('disabled row shows Run now + Enable', () => {
  render(<SchedulesTable schedules={[sched({ enabled: false })]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByRole('button', { name: 'Run now' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Enable' })).toBeInTheDocument()
})

test('clicking Run now and Disable fires callbacks with the id and next-enabled', async () => {
  const onRunNow = vi.fn()
  const onToggleEnabled = vi.fn()
  render(<SchedulesTable schedules={[sched({ enabled: true })]} pendingId={null} onRunNow={onRunNow} onToggleEnabled={onToggleEnabled} />)
  await userEvent.click(screen.getByRole('button', { name: 'Run now' }))
  await userEvent.click(screen.getByRole('button', { name: 'Disable' }))
  expect(onRunNow).toHaveBeenCalledWith('s1')
  expect(onToggleEnabled).toHaveBeenCalledWith('s1', false)
})

test('pending row disables its action buttons', () => {
  render(<SchedulesTable schedules={[sched()]} pendingId={'s1'} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  expect(screen.getByRole('button', { name: 'Run now' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeDisabled()
})

test('missing last_job_id renders a dash', () => {
  render(<SchedulesTable schedules={[sched({ last_job_id: undefined })]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  // last run cell and last job cell both could be '-'; assert the LAST JOB short id is absent
  expect(screen.queryByText('abcdef12')).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run (from `web/`): `npm test -- --run src/schedules/SchedulesTable.test.tsx`
Expected: FAIL - cannot resolve `./SchedulesTable`.

- [ ] **Step 3: Implement the table**

Create `web/src/schedules/SchedulesTable.tsx`:

```tsx
import type { Schedule } from './api'
import { formatRelativeTime, nextRunDisplay, shortId } from './format'

const COLS = 'grid grid-cols-[1.4fr_120px_110px_90px_1fr_1fr_110px_1.3fr_150px]'

export function SchedulesTable({
  schedules,
  pendingId,
  onRunNow,
  onToggleEnabled,
}: {
  schedules: Schedule[]
  pendingId: string | null
  onRunNow: (id: string) => void
  onToggleEnabled: (id: string, nextEnabled: boolean) => void
}) {
  return (
    <div className="rounded-card border border-border bg-white/5 backdrop-blur">
      <div className={`${COLS} border-b border-border px-4 py-3 font-mono text-[10px] tracking-wider text-fg-mute`}>
        <span>NAME</span>
        <span>CRON</span>
        <span>TZ</span>
        <span>OVERLAP</span>
        <span>NEXT RUN</span>
        <span>LAST RUN</span>
        <span>LAST JOB</span>
        <span>OWNER</span>
        <span className="text-right">ACTIONS</span>
      </div>
      {schedules.map((s) => {
        const pending = pendingId === s.id
        return (
          <div
            key={s.id}
            className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${s.enabled ? '' : 'opacity-[0.55]'}`}
          >
            <span className="flex min-w-0 items-center gap-2">
              <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${s.enabled ? 'bg-ok' : 'bg-fg-dim'}`} />
              <span className="truncate font-sans text-[13px] text-fg">{s.name}</span>
            </span>
            <span className="text-fg">{s.cron_expr}</span>
            <span className="truncate text-[10.5px] text-fg-mute">{s.timezone}</span>
            <span>
              <span
                className={`rounded-full border border-border px-1.5 py-0.5 text-[9.5px] uppercase tracking-wider ${s.overlap_policy === 'allow' ? 'text-accent' : 'text-fg-mute'}`}
              >
                {s.overlap_policy}
              </span>
            </span>
            <span className={s.enabled ? 'text-fg' : 'text-fg-dim'}>
              {s.enabled ? <span className="text-accent">▸</span> : null} {nextRunDisplay(s.next_run_at)}
            </span>
            <span className="text-fg-mute">{s.last_run_at ? formatRelativeTime(s.last_run_at) : '-'}</span>
            <span className="text-[10.5px] text-fg-mute">{shortId(s.last_job_id)}</span>
            <span className="truncate text-[10.5px] text-fg-mute">{s.owner_email}</span>
            <span className="flex justify-end gap-1.5">
              <button
                type="button"
                disabled={pending}
                onClick={() => onRunNow(s.id)}
                className="rounded-md border border-accent/50 bg-accent/15 px-2.5 py-1 text-[11px] text-fg disabled:opacity-40"
              >
                Run now
              </button>
              <button
                type="button"
                disabled={pending}
                onClick={() => onToggleEnabled(s.id, !s.enabled)}
                className="rounded-md border border-border bg-white/5 px-2.5 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                {s.enabled ? 'Disable' : 'Enable'}
              </button>
            </span>
          </div>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run (from `web/`): `npm test -- --run src/schedules/SchedulesTable.test.tsx`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/SchedulesTable.tsx web/src/schedules/SchedulesTable.test.tsx
git commit -m "web: schedules table component"
```

---

## Task 8: Frontend - page composition (`schedules/SchedulesPage.tsx`)

**Files:**
- Create: `web/src/schedules/SchedulesPage.tsx`
- Test: `web/src/schedules/SchedulesPage.test.tsx`

The page owns: sort state, a cursor stack (for prev/next), the pending-action id, loading/error/empty states, the summary strip, the sort `<select>`, the table, and the footer/pagination. A 1s local ticker re-renders so relative times stay fresh between 10s polls.

- [ ] **Step 1: Write the failing test**

Create `web/src/schedules/SchedulesPage.test.tsx`:

```tsx
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { SchedulesPage } from './SchedulesPage'

const page = {
  items: [
    {
      id: 's1', name: 'nightly-build', owner_id: 'o1', owner_email: 'dev@studio.com',
      cron_expr: '0 2 * * *', timezone: 'UTC', job_spec: {}, overlap_policy: 'skip',
      enabled: true, next_run_at: '2099-01-01T00:00:00Z', last_run_at: '2026-06-05T11:00:00Z',
      last_job_id: 'abcdef12-3456', created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-05T11:00:00Z',
    },
    {
      id: 's2', name: 'weekly-clean', owner_id: 'o1', owner_email: 'dev@studio.com',
      cron_expr: '0 0 * * 0', timezone: 'UTC', job_spec: {}, overlap_policy: 'allow',
      enabled: false, next_run_at: '2099-01-02T00:00:00Z', created_at: '2026-06-01T00:00:00Z',
      updated_at: '2026-06-05T10:00:00Z',
    },
  ],
  next_cursor: '',
  total: 2,
}

test('renders schedules and the page-scoped summary', async () => {
  server.use(http.get('/v1/scheduled-jobs', () => HttpResponse.json(page)))
  renderWithQuery(<SchedulesPage />)
  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.getByText('weekly-clean')).toBeInTheDocument()
  expect(screen.getByText('2 schedules')).toBeInTheDocument()
})

test('shows the empty state when there are no schedules', async () => {
  server.use(http.get('/v1/scheduled-jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })))
  renderWithQuery(<SchedulesPage />)
  expect(await screen.findByText('No schedules yet.')).toBeInTheDocument()
})

test('shows the error state with a Retry button', async () => {
  server.use(http.get('/v1/scheduled-jobs', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderWithQuery(<SchedulesPage />)
  expect(await screen.findByText('Retry')).toBeInTheDocument()
})

test('changing the sort re-requests with the new sort key', async () => {
  const sorts: (string | null)[] = []
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      sorts.push(new URL(request.url).searchParams.get('sort'))
      return HttpResponse.json(page)
    }),
  )
  renderWithQuery(<SchedulesPage />)
  await screen.findByText('nightly-build')
  await userEvent.selectOptions(screen.getByLabelText('Sort'), 'name')
  await waitFor(() => expect(sorts).toContain('name'))
})

test('next/prev pagination walks the cursor', async () => {
  const cursors: (string | null)[] = []
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      const c = new URL(request.url).searchParams.get('cursor')
      cursors.push(c)
      // First page returns a next_cursor; second page returns none.
      return HttpResponse.json({ ...page, next_cursor: c ? '' : 'CUR2' })
    }),
  )
  renderWithQuery(<SchedulesPage />)
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  await waitFor(() => expect(cursors).toContain('CUR2'))
  await userEvent.click(screen.getByRole('button', { name: /prev/i }))
  await waitFor(() => expect(cursors.filter((c) => c === null).length).toBeGreaterThanOrEqual(2))
})

test('clicking Disable PATCHes the schedule', async () => {
  let patched: unknown
  server.use(
    http.get('/v1/scheduled-jobs', () => HttpResponse.json(page)),
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      patched = await request.json()
      return HttpResponse.json({ ...page.items[0], enabled: false })
    }),
  )
  renderWithQuery(<SchedulesPage />)
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getAllByRole('button', { name: 'Disable' })[0])
  await waitFor(() => expect(patched).toEqual({ enabled: false }))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run (from `web/`): `npm test -- --run src/schedules/SchedulesPage.test.tsx`
Expected: FAIL - cannot resolve `./SchedulesPage`.

- [ ] **Step 3: Implement the page**

Create `web/src/schedules/SchedulesPage.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { Button } from '../components/Button'
import type { Schedule, ScheduleSort } from './api'
import { useSchedules } from './useSchedules'
import { useScheduleActions } from './useScheduleActions'
import { SchedulesTable } from './SchedulesTable'

const SORT_OPTIONS: { value: ScheduleSort; label: string }[] = [
  { value: '-created_at', label: 'Newest' },
  { value: 'created_at', label: 'Oldest' },
  { value: 'name', label: 'Name A→Z' },
  { value: '-name', label: 'Name Z→A' },
  { value: 'next_run_at', label: 'Next run soonest' },
  { value: '-next_run_at', label: 'Next run latest' },
  { value: '-updated_at', label: 'Recently run' },
  { value: 'updated_at', label: 'Least recently run' },
]

function countEnabled(schedules: Schedule[]): { enabled: number; paused: number } {
  let enabled = 0
  for (const s of schedules) if (s.enabled) enabled++
  return { enabled, paused: schedules.length - enabled }
}

export function SchedulesPage() {
  const [sort, setSort] = useState<ScheduleSort>('-created_at')
  // Cursor stack: [] is the first page; each entry is the cursor for a deeper page.
  const [cursorStack, setCursorStack] = useState<string[]>([])
  const cursor = cursorStack[cursorStack.length - 1]
  const [pendingId, setPendingId] = useState<string | null>(null)

  const { data, error, isLoading, refetch } = useSchedules(sort, cursor)
  const { runNow, setEnabled } = useScheduleActions()

  // Tick once a second so relative "next run"/"last run" strings stay fresh
  // between 10s polls.
  const [, setTick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 1000)
    return () => clearInterval(t)
  }, [])

  function chooseSort(next: ScheduleSort) {
    setSort(next)
    setCursorStack([]) // restart paging when the sort changes
  }

  async function onRunNow(id: string) {
    setPendingId(id)
    try {
      await runNow.mutateAsync(id)
    } finally {
      setPendingId(null)
    }
  }

  async function onToggleEnabled(id: string, nextEnabled: boolean) {
    setPendingId(id)
    try {
      await setEnabled.mutateAsync({ id, enabled: nextEnabled })
    } finally {
      setPendingId(null)
    }
  }

  if (isLoading && !data) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="h-10 rounded-card border border-border bg-white/5" />
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

  const schedules = data?.items ?? []
  if (schedules.length === 0) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
        No schedules yet.
      </div>
    )
  }

  const counts = countEnabled(schedules)
  const total = data?.total ?? schedules.length
  const actionError = (runNow.error ?? setEnabled.error) as Error | null

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">RECURRING</div>
          <h1 className="text-[32px] font-normal tracking-tight">Schedules</h1>
        </div>
        <div className="flex gap-4 font-mono text-[11px] text-fg-mute">
          <span><b className="text-ok">{counts.enabled}</b> ENABLED</span>
          <span><b className="text-fg">{counts.paused}</b> PAUSED</span>
          <span className="text-fg-dim">· <span>{`${total} schedules`}</span></span>
        </div>
        <label className="ml-auto flex items-center gap-2 font-mono text-[10px] text-fg-mute">
          <span>Sort</span>
          <select
            aria-label="Sort"
            value={sort}
            onChange={(e) => chooseSort(e.target.value as ScheduleSort)}
            className="rounded-md border border-border bg-black/25 px-2 py-1 text-[11px] text-fg"
          >
            {SORT_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
      </div>

      {actionError ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      <SchedulesTable
        schedules={schedules}
        pendingId={pendingId}
        onRunNow={onRunNow}
        onToggleEnabled={onToggleEnabled}
      />

      <div className="flex items-center justify-between font-mono text-[10.5px] tracking-wide text-fg-mute">
        <span>
          SHOWING <span className="text-fg">{schedules.length}</span> OF{' '}
          <span className="text-fg">{total}</span> · OWNED + ADMINISTRATIVE
        </span>
        <div className="flex gap-1.5">
          <button
            type="button"
            disabled={cursorStack.length === 0}
            onClick={() => setCursorStack((s) => s.slice(0, -1))}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            ← prev
          </button>
          <button
            type="button"
            disabled={!data?.next_cursor}
            onClick={() => data?.next_cursor && setCursorStack((s) => [...s, data.next_cursor])}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            next 50 →
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run (from `web/`): `npm test -- --run src/schedules/SchedulesPage.test.tsx`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/SchedulesPage.tsx web/src/schedules/SchedulesPage.test.tsx
git commit -m "web: schedules page composition"
```

---

## Task 9: Frontend - mount the route

**Files:**
- Modify: `web/src/app/router.tsx`

- [ ] **Step 1: Replace the `/schedules` placeholder with the real page**

In `web/src/app/router.tsx`, add the import near the other page imports:

```tsx
import { SchedulesPage } from '../schedules/SchedulesPage'
```

Change the schedules route from:

```tsx
        <Route path="/schedules" element={<JobsPlaceholder />} />
```

to:

```tsx
        <Route path="/schedules" element={<SchedulesPage />} />
```

Leave the other `JobsPlaceholder` routes (`/jobs`, `/admin`, `/profile/*`) unchanged - `JobsPlaceholder` is still imported and used by them.

- [ ] **Step 2: Run the full front-end test suite**

Run (from `web/`): `npm test -- --run`
Expected: PASS - all existing tests plus the new schedules tests green.

- [ ] **Step 3: Type-check and production build**

Run (from `web/`): `npm run build`
Expected: `tsc` passes and Vite builds with no errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/app/router.tsx
git commit -m "web: mount Schedules list page at /schedules"
```

---

## Task 10: Full verification & contract check

**Files:** none (verification only)

- [ ] **Step 1: Contract check - TS `Schedule` vs Go `scheduledJobResponse`**

Open `web/src/schedules/api.ts` and `internal/api/scheduled_jobs.go` side by side. Confirm every JSON field on `scheduledJobResponse` has a matching property on the TS `Schedule` interface with a compatible type, including the new `owner_email`. Note in the commit/PR that the contract was verified field-for-field.

Fields to reconcile: `id`, `name`, `owner_id`, `owner_email`, `cron_expr`, `timezone`, `job_spec`, `overlap_policy`, `enabled`, `next_run_at`, `last_run_at?`, `last_job_id?`, `created_at`, `updated_at`.

- [ ] **Step 2: Backend unit + relevant integration tests**

Run: `make test`
Expected: PASS.

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListScheduledJobs -v -timeout 240s`
Expected: PASS (sort, pagination, owner-email).

- [ ] **Step 3: Front-end suite + build (final)**

Run (from `web/`): `npm test -- --run`
Expected: PASS.

Run (from `web/`): `npm run build`
Expected: clean build.

- [ ] **Step 4: No-em-dash house-rule scan**

Confirm none of the new files introduced an em dash (—) or en dash (–) in comments or copy. The `→` and `←` arrows and the `▸` glyph are intentional UI characters and are fine; only em/en dashes are forbidden.

- [ ] **Step 5: Final review against the spec**

Re-read `docs/superpowers/specs/2026-06-05-web-schedules-list-design.md` and confirm each in-scope item is implemented and each deferred item is genuinely absent (no detail page, no Edit button, no filter chips/search, no FAILED-24h stat).

---

## Follow-up backlog (not implemented here)

These were explicitly deferred in the spec; file as backlog items during the finishing-a-development-branch step:

- Schedule detail page + the "Edit" action.
- Last-job link + status dot (needs a Jobs detail page + last-job status data).
- "FAILED 24h" summary stat (needs a failed-runs aggregate endpoint).
- Server-side `enabled` filter + name search (enables the design's filter chips + search box).
