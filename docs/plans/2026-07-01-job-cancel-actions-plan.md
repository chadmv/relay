# Job Cancel Actions (Graceful + Force) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire graceful cancel and force-cancel of a job into the job-detail page header, backed by a mutation hook that invalidates the three affected query keys.

**Architecture:** Frontend-only slice. The backend `DELETE /v1/jobs/{id}[?force=true]` endpoint already exists, is owner-or-admin gated server-side, and is unchanged by this work. We add a `cancelJob` API client, a `useJobActions` mutation hook that mirrors `useWorkerActions`, and a `JobActions` header bar (two buttons behind the shared `ConfirmDialog`), rendered into the reserved actions slot on `JobDetailPage` only for the owner or an admin.

**Tech Stack:** React 18, TypeScript, TanStack Query v5, react-router-dom, Vitest + MSW + Testing Library. Styling via existing Tailwind v4 utility classes and design tokens (`text-err`, `border-border`, `bg-accent`, `bg-white/5`, `text-fg`, `text-fg-mute`).

---

## Slice independence

**This is a FRONTEND-ONLY slice. There is no backend work and no cross-slice dependency.** The backend endpoint was verified during planning against `internal/api/jobs.go` (`handleCancelJob`, lines 676-780) and matches the spec exactly:

- Route: `DELETE /v1/jobs/{id}`. Any authenticated user may call it; the handler applies its own owner-or-admin check (`jobs.go:707-715`), returning `404` (existence hidden) to a non-owner non-admin.
- Force param: `?force=true`, parsed with `strconv.ParseBool(r.URL.Query().Get("force"))` (`jobs.go:683`). Anything not parseable as true is graceful. The ONLY observable server effect of `force` is the `force` bool carried in the best-effort agent `cancelSignal` (`jobs.go:763-771`); the DB effect (cancel all non-terminal tasks, set job `cancelled`) is identical in both modes.
- Terminal guard: `if job.Status == "cancelled" || job.Status == "done"` returns `409 Conflict` "job is already in a terminal state" (`jobs.go:717-720`). Note `failed` is NOT terminal here, so cancelling a failed job succeeds (200) and re-marks it `cancelled`.
- Success: `200 OK` with a `jobResponse` (`status: "cancelled"`, no task array, no `submitted_by_email`; `toJobResponse(job, "", nil, nil)` at `jobs.go:779`).
- Post-cancel visibility: a cancelled job is still returned by `GET /v1/jobs/{id}`. The detail page stays valid and viewable.

Because there is no backend work and the whole change lives in `web/src/`, **the entire plan is for `relay-frontend-engineer`.** There is no Phase 3 parallelism to declare across engineers. The tasks are **sequential within one engineer** because Task 3 imports the hook from Task 2, Task 2 imports the client from Task 1, and Task 4 imports the component from Task 3. Do them in order.

## Non-obvious correctness points (do NOT miss these)

These are the exact failure modes an engineer with no context will hit. Every one is spelled out in the spec.

1. **Invalidate THREE keys, including `['job-stats']` explicitly.** `onSuccess` must invalidate `['job', id]`, `['jobs']`, AND `['job-stats']`. The stats query is keyed `['job-stats']` (see `web/src/jobs/useJobStats.ts:6`), NOT nested under `['jobs']`. This decoupling is deliberate and load-bearing: the existing regression test `web/src/jobs/queryKeyDecoupling.test.tsx` asserts that `invalidateQueries({ queryKey: ['jobs'] })` does NOT refetch the stats query. So a two-key invalidation (`['job', id]` + `['jobs']`) will silently leave the KPI strip's running/queued counts stale after a cancel. The third `['job-stats']` invalidation is required. Task 2 Step 1 writes the test that catches this; the explicit `['job-stats']` refetch assertion in Task 3 is the second guard.

2. **Invalidate `['job', id]` and STAY on the page.** A cancelled job is still viewable via `GET /v1/jobs/{id}`. Invalidate `['job', id]` so the status pill refetches and flips to `cancelled`; the user does NOT navigate away. This is the OPPOSITE of worker revoke (`useWorkerActions.ts:77-84`), which deliberately skips `['worker', id]` and navigates to `/workers` because that query 404s post-revoke. Do not copy the revoke navigation.

3. **Force is a call-site argument, not two mutations.** There is ONE `cancel` mutation whose variable is `force: boolean` (`cancel.mutate(false)` vs `cancel.mutate(true)`). Both share identical cache handling. Do not define two mutations. The only observable difference between the two buttons is the `?force=true` query param on the request URL.

4. **Buttons hidden ONLY for `done`/`cancelled`; `failed` stays cancellable.** Terminal detection uses `job.status === 'done' || job.status === 'cancelled'`. A `failed` job keeps its buttons because the server's 409 guard treats only `cancelled` and `done` as terminal. Do not add `failed` to the hide list; the client must agree with the server.

5. **Owner-or-admin UI gate.** Render `<JobActions>` only when `user.is_admin || job.submitted_by === user.id`. `user` comes from `useAuth()` (`web/src/auth/AuthProvider.tsx:106`), which exposes `{ id, is_admin }` (see `web/src/lib/types.ts:1-6`). `job.submitted_by` is on `JobDetail` (`web/src/jobs/api.ts:92`). This gate is a usability affordance only; the server 404s unauthorized callers regardless.

6. **`cancel.reset()` on dialog re-open.** When re-opening a dialog after a prior failure, call `cancel.reset()` to clear the stale error, matching the worker enable path (`WorkerActions.tsx:71`).

## File structure

New files (all under `web/src/jobs/`):

- `web/src/jobs/useJobActions.ts` - the mutation hook. One `useMutation` (`cancel`) taking `force: boolean`; `onSuccess` invalidates the three keys.
- `web/src/jobs/useJobActions.test.tsx` - hook-level tests: graceful URL, force URL, three-key invalidation, no-navigation, error propagation.
- `web/src/jobs/JobActions.tsx` - the header action bar: two buttons (Cancel / Force cancel), the `ConfirmDialog`, the inline error banner, terminal-state hiding.
- `web/src/jobs/JobActions.test.tsx` - component tests: graceful/force flows, dialog dismiss, gating (owner/admin/neither), terminal-state hiding, 409 error banner + no navigation.

Modified files:

- `web/src/jobs/api.ts` - add the `cancelJob(id, force)` client.
- `web/src/jobs/api.test.ts` - add two tests: graceful (no `force`) and force (`force=true`) request URLs.
- `web/src/jobs/JobDetailPage.tsx` - render `<JobActions job={job} />` into the reserved slot behind the owner-or-admin gate; import `useAuth`.
- `web/src/jobs/JobDetailPage.test.tsx` - update the existing "reserved actions slot" test, which will otherwise break once buttons render for the owner fixture (see Task 4 Step 1).

## Conventions to follow

- Run all commands from the `web/` directory. From the worktree root `D:\dev\relay\.claude\worktrees\stoic-cannon-15b269`, `cd web` first. Test command: `npm test` (runs `vitest run`, whole suite). Single file: `npx vitest run src/jobs/<file>`. Single test: `npx vitest run src/jobs/<file> -t "<test name>"`. Build/typecheck: `npm run build` (`tsc -b && vite build`).
- Match existing style: functional components, named exports, Tailwind utility strings, design tokens. Error banner styling reuses the `WorkerActions` pattern: `rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err`. Force button styling reuses the worker Revoke pattern: `border-err/50 bg-err/10 text-err`.
- Never use em dashes or en dashes; use regular hyphens.
- Do NOT edit `web/dist/`. If a build dirties it, `git checkout -- web/dist/` before committing (it is stale from the scaffold and not maintained per-PR).
- Commit after each task. Use the exact commit messages given; `git add` only the files that task touched.

---

## Task 1: `cancelJob` API client

**Files:**
- Modify: `web/src/jobs/api.ts` (append after `getTaskLogs`, currently ending at line 124)
- Test: `web/src/jobs/api.test.ts` (append)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/jobs/api.test.ts`. Update the import on line 5 to include `cancelJob`:

```ts
import { listJobs, getJobStats, cancelJob, type JobsPage } from './api'
```

Then append these two tests at the end of the file:

```ts
test('cancelJob DELETEs /jobs/{id} with no force param when force=false', async () => {
  let method: string | undefined
  let search: string | undefined
  server.use(
    http.delete('/v1/jobs/j1', ({ request }) => {
      method = request.method
      search = new URL(request.url).search
      return HttpResponse.json({ id: 'j1', status: 'cancelled' })
    }),
  )
  await cancelJob('j1', false)
  expect(method).toBe('DELETE')
  expect(search).toBe('')
})

test('cancelJob DELETEs /jobs/{id}?force=true when force=true', async () => {
  let search: string | undefined
  server.use(
    http.delete('/v1/jobs/j1', ({ request }) => {
      search = new URL(request.url).searchParams.get('force')
      return HttpResponse.json({ id: 'j1', status: 'cancelled' })
    }),
  )
  await cancelJob('j1', true)
  expect(search).toBe('true')
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web; npx vitest run src/jobs/api.test.ts`
Expected: FAIL - `cancelJob` is not exported (`"cancelJob" is not exported by "src/jobs/api.ts"`), TypeScript/import error.

- [ ] **Step 3: Write the minimal implementation**

Append to `web/src/jobs/api.ts` after the `getTaskLogs` function (after line 124):

```ts
// Cancels a job. force=true asks agents to force-kill running tasks; the DB
// effect (mark all non-terminal tasks and the job cancelled) is identical either
// way. Server 409s a job already `cancelled`/`done`, 404s a non-owner non-admin.
// The server returns the updated job body, but the caller invalidates rather than
// writing it into the cache, so the typed result is unused.
export function cancelJob(id: string, force: boolean): Promise<JobDetail> {
  return apiFetch<JobDetail>(`/jobs/${id}${force ? '?force=true' : ''}`, { method: 'DELETE' })
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web; npx vitest run src/jobs/api.test.ts`
Expected: PASS - all tests including the two new ones.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/api.ts web/src/jobs/api.test.ts
git commit -m "feat(web): add cancelJob API client for job cancel actions"
```

---

## Task 2: `useJobActions` mutation hook

**Files:**
- Create: `web/src/jobs/useJobActions.ts`
- Test: `web/src/jobs/useJobActions.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/jobs/useJobActions.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJobActions } from './useJobActions'

const ID = 'j1'

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('graceful cancel DELETEs /jobs/{id} with no force query string', async () => {
  const client = newClient()
  let search = ''
  server.use(
    http.delete(`/v1/jobs/${ID}`, ({ request }) => {
      search = new URL(request.url).search
      return HttpResponse.json({ id: ID, status: 'cancelled' })
    }),
  )
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })
  await result.current.cancel.mutateAsync(false)

  expect(search).toBe('')
})

test('force cancel DELETEs /jobs/{id}?force=true', async () => {
  const client = newClient()
  let force: string | null = null
  server.use(
    http.delete(`/v1/jobs/${ID}`, ({ request }) => {
      force = new URL(request.url).searchParams.get('force')
      return HttpResponse.json({ id: ID, status: 'cancelled' })
    }),
  )
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })
  await result.current.cancel.mutateAsync(true)

  expect(force).toBe('true')
})

test('onSuccess invalidates all THREE keys: [job,id], [jobs], and [job-stats]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(http.delete(`/v1/jobs/${ID}`, () => HttpResponse.json({ id: ID, status: 'cancelled' })))
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })
  await result.current.cancel.mutateAsync(false)

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['job', ID] }))
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['jobs'] }))
  // The decoupled stats key MUST be invalidated explicitly; ['jobs'] alone does
  // not reach ['job-stats'] (see queryKeyDecoupling.test.tsx). Missing this call
  // is the two-key regression.
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['job-stats'] }))
})

test('a failed cancel rejects and does not invalidate', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.delete(`/v1/jobs/${ID}`, () =>
      HttpResponse.json({ error: 'job is already in a terminal state' }, { status: 409 }),
    ),
  )
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })

  await expect(result.current.cancel.mutateAsync(false)).rejects.toBeTruthy()
  expect(spy).not.toHaveBeenCalledWith({ queryKey: ['job', ID] })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web; npx vitest run src/jobs/useJobActions.test.tsx`
Expected: FAIL - cannot resolve `./useJobActions` (module does not exist).

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/jobs/useJobActions.ts`:

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { cancelJob } from './api'

// Cancel mutation for the job-detail actions bar. Follows the invalidate-on-
// success strategy of useWorkerActions. Key invariants:
//  - ONE mutation; force is its variable (cancel.mutate(false|true)). The only
//    observable difference is the ?force=true query param.
//  - onSuccess invalidates THREE keys: ['job', id], ['jobs'], and ['job-stats'].
//    ['job-stats'] is decoupled from ['jobs'] (see queryKeyDecoupling.test.tsx),
//    so the bare ['jobs'] invalidation alone would leave the KPI strip stale.
//  - ['job', id] IS invalidated (a cancelled job is still viewable); the caller
//    stays on the detail page. This is the opposite of worker revoke.
//  - No optimistic update; useJob polls ['job', id] every 3s and the invalidate
//    triggers an immediate refetch.
export function useJobActions(id: string) {
  const qc = useQueryClient()

  const cancel = useMutation({
    mutationFn: (force: boolean) => cancelJob(id, force),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['job', id] })
      qc.invalidateQueries({ queryKey: ['jobs'] })
      qc.invalidateQueries({ queryKey: ['job-stats'] })
    },
  })

  return { cancel }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web; npx vitest run src/jobs/useJobActions.test.tsx`
Expected: PASS - all four tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/useJobActions.ts web/src/jobs/useJobActions.test.tsx
git commit -m "feat(web): add useJobActions cancel mutation with three-key invalidation"
```

---

## Task 3: `JobActions` header bar component

**Files:**
- Create: `web/src/jobs/JobActions.tsx`
- Test: `web/src/jobs/JobActions.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/jobs/JobActions.test.tsx`:

```tsx
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { JobActions } from './JobActions'
import type { JobDetail } from './api'

const ID = 'j1'

const JOB: JobDetail = {
  id: ID,
  name: 'shot-042 render',
  priority: 'high',
  status: 'running',
  submitted_by: 'u1',
  labels: null,
  tasks: [],
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:01:00Z',
}

function renderActions(job: JobDetail) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <JobActions job={job} />
    </QueryClientProvider>,
  )
}

test('a running job shows Cancel and Force cancel buttons', () => {
  renderActions(JOB)
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Force cancel' })).toBeInTheDocument()
})

test('graceful cancel confirms and DELETEs without ?force=true', async () => {
  let search = ''
  server.use(
    http.delete(`/v1/jobs/${ID}`, ({ request }) => {
      search = new URL(request.url).search
      return HttpResponse.json({ ...JOB, status: 'cancelled' })
    }),
  )
  renderActions(JOB)
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  const dialog = screen.getByRole('dialog')
  // Primary action label is "Cancel job" (not "Cancel") to disambiguate from the
  // dialog's own "Cancel" dismiss button.
  await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel job' }))
  await waitFor(() => expect(search).toBe(''))
})

test('force cancel confirms and DELETEs with ?force=true', async () => {
  let force: string | null = null
  server.use(
    http.delete(`/v1/jobs/${ID}`, ({ request }) => {
      force = new URL(request.url).searchParams.get('force')
      return HttpResponse.json({ ...JOB, status: 'cancelled' })
    }),
  )
  renderActions(JOB)
  await userEvent.click(screen.getByRole('button', { name: 'Force cancel' }))
  const dialog = screen.getByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Force cancel' }))
  await waitFor(() => expect(force).toBe('true'))
})

test('dismissing the confirm dialog fires no request', async () => {
  let hits = 0
  server.use(
    http.delete(`/v1/jobs/${ID}`, () => {
      hits++
      return HttpResponse.json({ ...JOB, status: 'cancelled' })
    }),
  )
  renderActions(JOB)
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }))
  await new Promise((r) => setTimeout(r, 20))
  expect(hits).toBe(0)
})

test('Escape dismisses the dialog and fires no request', async () => {
  let hits = 0
  server.use(
    http.delete(`/v1/jobs/${ID}`, () => {
      hits++
      return HttpResponse.json({ ...JOB, status: 'cancelled' })
    }),
  )
  renderActions(JOB)
  await userEvent.click(screen.getByRole('button', { name: 'Force cancel' }))
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(hits).toBe(0)
})

test('a done job hides both buttons', () => {
  renderActions({ ...JOB, status: 'done' })
  expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Force cancel' })).not.toBeInTheDocument()
})

test('a cancelled job hides both buttons', () => {
  renderActions({ ...JOB, status: 'cancelled' })
  expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Force cancel' })).not.toBeInTheDocument()
})

test('a failed job STILL shows both buttons (server allows cancel of failed)', () => {
  renderActions({ ...JOB, status: 'failed' })
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Force cancel' })).toBeInTheDocument()
})

test('the stats query refetches after a successful cancel (three-key invalidation)', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  let statsCalls = 0
  server.use(
    http.get('/v1/jobs/stats', () => {
      statsCalls++
      return HttpResponse.json({ running: 0, queued: 0, done_24h: 0, failed_24h: 0 })
    }),
    http.delete(`/v1/jobs/${ID}`, () => HttpResponse.json({ ...JOB, status: 'cancelled' })),
  )
  // Seed the stats query into the cache so an invalidation triggers a refetch.
  await client.fetchQuery({ queryKey: ['job-stats'], queryFn: () =>
    fetch('/v1/jobs/stats').then((r) => r.json()) })
  expect(statsCalls).toBe(1)

  render(
    <QueryClientProvider client={client}>
      <JobActions job={JOB} />
    </QueryClientProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel job' }))

  // ['job-stats'] refetches -> statsCalls goes to 2. A two-key invalidation
  // (missing ['job-stats']) leaves it at 1 and fails this assertion.
  await waitFor(() => expect(statsCalls).toBe(2))
})

test('a 409 surfaces an inline error banner and does not navigate', async () => {
  server.use(
    http.delete(`/v1/jobs/${ID}`, () =>
      HttpResponse.json({ error: 'job is already in a terminal state' }, { status: 409 }),
    ),
  )
  renderActions(JOB)
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel job' }))
  // The banner shows the server message (or the "409 <code>" fallback).
  expect(await screen.findByText(/terminal state|409/)).toBeInTheDocument()
  // The buttons remain mounted (no navigation, still on the detail page).
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web; npx vitest run src/jobs/JobActions.test.tsx`
Expected: FAIL - cannot resolve `./JobActions` (module does not exist).

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/jobs/JobActions.tsx`:

```tsx
import { useState } from 'react'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useJobActions } from './useJobActions'
import type { JobDetail } from './api'

type Pending = null | 'cancel' | 'force'

// Job-detail header action bar. Owns the two cancel buttons, the confirm dialog,
// and the inline error. A cancelled job stays viewable, so on success we do NOT
// navigate; the ['job', id] invalidation flips the status pill on refetch.
export function JobActions({ job }: { job: JobDetail }) {
  const { cancel } = useJobActions(job.id)
  const [confirm, setConfirm] = useState<Pending>(null)

  // Hide the buttons only for states the server treats as terminal for cancel
  // (cancelled/done). `failed` is NOT terminal server-side, so it stays
  // cancellable and keeps its buttons.
  const terminal = job.status === 'cancelled' || job.status === 'done'

  const actionError = cancel.error as Error | null

  function openConfirm(which: Exclude<Pending, null>) {
    cancel.reset()
    setConfirm(which)
  }

  function runConfirmed() {
    if (confirm === 'cancel') cancel.mutate(false)
    else if (confirm === 'force') cancel.mutate(true)
    setConfirm(null)
  }

  const confirmCopy: Record<Exclude<Pending, null>, { title: string; body: string; label: string; destructive?: boolean }> = {
    cancel: {
      title: `Cancel ${job.name}?`,
      body: 'Running tasks are asked to stop and the job is marked cancelled. Tasks that have not started are dropped.',
      // "Cancel job" (not "Cancel") avoids ambiguity with the dialog's own
      // "Cancel" dismiss button.
      label: 'Cancel job',
      destructive: true,
    },
    force: {
      title: `Force cancel ${job.name}?`,
      body: 'Running tasks are force-killed immediately and the job is marked cancelled. Use this when a graceful cancel is not stopping the work.',
      label: 'Force cancel',
      destructive: true,
    },
  }

  return (
    <div className="flex flex-col gap-2">
      {!terminal && (
        <div className="flex items-center gap-2">
          <button
            type="button"
            disabled={cancel.isPending}
            onClick={() => openConfirm('cancel')}
            className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg-mute disabled:opacity-40"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={cancel.isPending}
            onClick={() => openConfirm('force')}
            className="rounded-md border border-err/50 bg-err/10 px-3 py-1.5 text-[12px] text-err disabled:opacity-40"
          >
            Force cancel
          </button>
        </div>
      )}

      {actionError ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      {confirm && (
        <ConfirmDialog
          title={confirmCopy[confirm].title}
          body={confirmCopy[confirm].body}
          confirmLabel={confirmCopy[confirm].label}
          destructive={confirmCopy[confirm].destructive}
          onConfirm={runConfirmed}
          onCancel={() => setConfirm(null)}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web; npx vitest run src/jobs/JobActions.test.tsx`
Expected: PASS - all eleven tests.

Note on the error-banner test: `apiFetch` throws `ApiError(status, code, "<status> <code>")` where `code` is the value of the `{error}` envelope's `error` field (`web/src/lib/api.ts:47-53`). So `actionError.message` is `"409 job is already in a terminal state"`, which matches the `/terminal state|409/` regex.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/JobActions.tsx web/src/jobs/JobActions.test.tsx
git commit -m "feat(web): add JobActions header bar with graceful and force cancel"
```

---

## Task 4: Render `JobActions` into the reserved slot with the owner-or-admin gate

**Files:**
- Modify: `web/src/jobs/JobDetailPage.tsx` (imports at lines 1-13; the reserved slot at lines 84-85)
- Modify: `web/src/jobs/JobDetailPage.test.tsx` (the "reserved actions slot" test at lines 142-149; add gating tests)

- [ ] **Step 1: Update the existing "reserved actions slot" test (RED-preserving)**

The current test at `JobDetailPage.test.tsx:142-149` asserts NO cancel button renders. The default fixture user is the owner (`me` returns `id: 'u1'`, `JOB.submitted_by === 'u1'`), so once Task 4 wires in `JobActions`, the Cancel button WILL render and this test would break. Replace that test so it reflects the new behavior, and add gating coverage.

Replace lines 142-149 (the whole `test('reserved actions slot renders but contains no action buttons (deferred)', ...)`) with the following. Note `renderDetail()` currently hard-codes `is_admin: false` and `id: 'u1'` in its `/v1/users/me` handler (line 40); to vary the user per test, override that handler with `server.use(...)` BEFORE calling `renderDetail()` (MSW uses the most recently registered matching handler):

```tsx
test('owner (non-admin) sees the cancel actions in the reserved slot', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  // Default me handler is id:'u1', is_admin:false; JOB.submitted_by is 'u1'.
  renderDetail()
  await screen.findByText('shot-042 render')
  const slot = screen.getByTestId('job-actions')
  expect(within(slot).getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  expect(within(slot).getByRole('button', { name: 'Force cancel' })).toBeInTheDocument()
})

test('admin non-owner sees the cancel actions', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  server.use(http.get('/v1/users/me', () => HttpResponse.json({ id: 'other', email: 'a@b.co', name: 'A', is_admin: true })))
  renderDetail()
  await screen.findByText('shot-042 render')
  const slot = screen.getByTestId('job-actions')
  expect(within(slot).getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  expect(within(slot).getByRole('button', { name: 'Force cancel' })).toBeInTheDocument()
})

test('non-owner non-admin does NOT see the cancel actions', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  server.use(http.get('/v1/users/me', () => HttpResponse.json({ id: 'other', email: 'a@b.co', name: 'A', is_admin: false })))
  renderDetail()
  await screen.findByText('shot-042 render')
  const slot = screen.getByTestId('job-actions')
  expect(within(slot).queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
  expect(within(slot).queryByRole('button', { name: 'Force cancel' })).not.toBeInTheDocument()
})
```

Add `within` to the Testing Library import on line 1:

```tsx
import { render, screen, within } from '@testing-library/react'
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web; npx vitest run src/jobs/JobDetailPage.test.tsx`
Expected: FAIL - the three new tests fail because `JobDetailPage` still renders an empty slot (no buttons). The owner and admin tests fail on the missing Cancel button; the non-owner test passes (empty slot already has no buttons) but is asserting the correct final behavior.

- [ ] **Step 3: Wire `JobActions` into `JobDetailPage` behind the gate**

In `web/src/jobs/JobDetailPage.tsx`, add two imports. After the existing `import { Button } from '../components/Button'` (line 4), add the auth hook import, and after `import type { TaskDetail } from './api'` (line 12) add the component import:

```tsx
import { useAuth } from '../auth/AuthProvider'
```

```tsx
import { JobActions } from './JobActions'
```

Inside the component, read the current user. Add after `const { id = '' } = useParams()` (line 24):

```tsx
  const { user } = useAuth()
```

Then compute the gate after `if (!job) return null` (line 65), before `const c = statusColor(job.status)`:

```tsx
  const canManage = Boolean(user && (user.is_admin || job.submitted_by === user.id))
```

Finally replace the reserved-slot element (lines 84-85):

```tsx
          {/* Reserved actions slot: cancel/retry deferred to a later slice. Intentionally empty. */}
          <div data-testid="job-actions" className="ml-auto flex items-center gap-2" />
```

with the gated render, keeping the same `data-testid` and classes on the wrapper:

```tsx
          <div data-testid="job-actions" className="ml-auto flex items-center gap-2">
            {canManage && <JobActions job={job} />}
          </div>
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web; npx vitest run src/jobs/JobDetailPage.test.tsx`
Expected: PASS - all tests including the three gating tests.

- [ ] **Step 5: Run the full web suite and typecheck**

Run: `cd web; npm test`
Expected: PASS - whole suite green, including `queryKeyDecoupling.test.tsx` (unchanged, still passing).

Run: `cd web; npm run build`
Expected: `tsc -b` clean, `vite build` succeeds.

If the build dirtied `web/dist/`, revert it: `git checkout -- web/dist/`

- [ ] **Step 6: Commit**

```bash
git add web/src/jobs/JobDetailPage.tsx web/src/jobs/JobDetailPage.test.tsx
git commit -m "feat(web): render JobActions in job detail header for owner or admin"
```

---

## Self-review against the spec

Spec coverage check (each requirement maps to a task):

- Cancel + force-cancel actions wired to `DELETE /v1/jobs/{id}[?force=true]` -> Task 1 (client), Task 3 (buttons).
- Both gated behind `ConfirmDialog` with distinct copy; primary label "Cancel job" to avoid "Cancel/Cancel" ambiguity; force uses err-accent styling -> Task 3 (`confirmCopy`, button classes).
- `useJobActions(id)` invalidate-on-success hook mirroring `useWorkerActions` -> Task 2.
- Three-key invalidation including explicit `['job-stats']` -> Task 2 Step 1 (hook test), Task 3 (component-level `statsCalls` refetch test). Correctness point 1.
- Invalidate `['job', id]` and stay on the page (no navigation) -> Task 2 (`onSuccess` includes `['job', id]`, no navigate), Task 3 (409 test asserts buttons remain mounted). Correctness point 2.
- Force is a call-site arg, one mutation -> Task 2 (`mutationFn: (force) => ...`), Task 3 (`runConfirmed` calls `cancel.mutate(false|true)`). Correctness point 3.
- Buttons hidden only for done/cancelled; failed stays cancellable -> Task 3 (`terminal` computation + tests). Correctness point 4.
- Owner-or-admin UI gate -> Task 4 (`canManage`) + gating tests. Correctness point 5.
- `cancel.reset()` on dialog re-open -> Task 3 (`openConfirm`). Correctness point 6.
- Inline error banner on failure -> Task 3 (`actionError` banner + 409 test).
- Keep `data-testid="job-actions"` wrapper + classes -> Task 4 Step 3 (wrapper preserved, `JobActions` rendered inside).

Test-plan coverage (spec section "Test plan", items 1-9):

1. Graceful cancel (no force param) -> Task 3 "graceful cancel confirms and DELETEs without ?force=true"; Task 1/Task 2 URL tests.
2. Force cancel (force=true) -> Task 3 "force cancel confirms and DELETEs with ?force=true"; Task 1/Task 2 URL tests.
3. Confirm-then-dismiss (Cancel button and Escape) -> Task 3 "dismissing the confirm dialog fires no request" + "Escape dismisses the dialog and fires no request".
4. Gating non-owner non-admin -> Task 4 "non-owner non-admin does NOT see the cancel actions".
5. Gating admin non-owner -> Task 4 "admin non-owner sees the cancel actions".
6. Gating owner non-admin -> Task 4 "owner (non-admin) sees the cancel actions".
7. Terminal hides (done/cancelled) + failed shows -> Task 3 three status tests.
8. Three-key invalidation with explicit `['job-stats']` refetch, no navigation -> Task 2 invalidation test + Task 3 `statsCalls` refetch test.
9. 409 error banner + no navigation -> Task 3 "a 409 surfaces an inline error banner and does not navigate".

Type consistency check: `cancelJob(id: string, force: boolean): Promise<JobDetail>` (Task 1) is imported and called by `useJobActions` (Task 2). `useJobActions(id)` returns `{ cancel }`; `JobActions` (Task 3) destructures `{ cancel }` and uses `cancel.mutate`, `cancel.isPending`, `cancel.error`, `cancel.reset`. `JobActions` takes `{ job: JobDetail }`; `JobDetailPage` (Task 4) passes `job={job}` where `job` is the `JobDetail` from `useJob`. `JobDetail.submitted_by` (string) and `JobDetail.status` are used consistently. `useAuth().user` is `User | null` with `{ id, is_admin }`. No signature drift.

No placeholders: every code step contains the full file or the exact edit. No "TBD", no "add validation", no "similar to Task N".
