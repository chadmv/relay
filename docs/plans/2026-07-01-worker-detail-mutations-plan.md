# Worker Detail Page - Admin Mutation Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the admin write actions (rename, edit labels, set max_slots, disable, drain, enable, revoke token, evict workspace) to the worker detail page, establishing the reusable frontend mutation + cache-invalidation pattern.

**Architecture:** Frontend-only slice. Every endpoint already exists and is already admin-gated server-side (`auth(admin(...))` in `internal/api/server.go`). We extend the existing `web/src/workers/` feature module with new API clients, a mutation hook (`useWorkerActions`), a shared `ConfirmDialog` primitive, an inline `WorkerEditForm`, and an admin-only `WorkerActions` bar wired into `WorkerDetailPage`, plus a per-row Evict button in `WorkspacesPanel`.

**Tech Stack:** React 18, TypeScript, TanStack Query v5, react-router-dom, Vitest + MSW + Testing Library. Styling via existing Tailwind v4 utility classes and design tokens (`text-err`, `border-border`, `bg-accent`, etc.).

---

## Slice independence

**This is a FRONTEND-ONLY slice.** There is no backend work: all six endpoints exist, are registered under `auth(admin(...))`, and were verified during planning:

- `PATCH /v1/workers/{id}` -> 200 `workerResponse` (`handleUpdateWorker`, `internal/api/workers.go:361`). Body struct is `{ name *string, labels map[string]string, max_slots *int32 }`. Fields omitted keep current values; `labels` when present is a **full replace** (`json.Marshal(body.Labels)`), not a per-key merge.
- `POST /v1/workers/{id}/disable[?requeue=true]` -> 200 `disableWorkerResponse` (`handleDisableWorker`, `workers.go:424`). `disableWorkerResponse` = `workerResponse` plus `requeued_tasks int` (`workers.go:37-40`).
- `POST /v1/workers/{id}/enable` -> 200 `workerResponse` (`handleEnableWorker`, `workers.go:526`).
- `DELETE /v1/workers/{id}/token` -> **204 No Content** (`handleDeleteWorkerToken`, `agent_enrollments.go:227`, `w.WriteHeader(http.StatusNoContent)` at line 242). Clears the token AND sets status to `revoked`.
- `POST /v1/workers/{id}/workspaces/{short_id}/evict` -> **202 Accepted**, no body (`handleEvictWorkerWorkspace`, `workspaces.go:43`, `w.WriteHeader(http.StatusAccepted)` at line 80).

Because there is no backend work, **the entire plan is for `relay-frontend-engineer`.** The tasks are **largely sequential within one engineer** because they edit shared files (`api.ts`, `WorkerDetailPage.tsx`). Do them in the order below. There is no Phase 3 parallelism to declare across engineers; the only backend-shaped step is the pre-existing route surface, already merged.

## Non-obvious correctness points (do not miss these)

1. **`apiFetch` only special-cases 204, not 202.** `web/src/lib/api.ts:55` returns `undefined` for status 204 but then falls through to `res.json()` for every other status. The evict endpoint returns **202 with an empty body**, so `res.json()` would throw a parse error. Task 1 must broaden that guard to cover no-body success responses (see Task 1 Step 5). Do not skip this; without it the evict client rejects on success.
2. **Revoke is terminal - navigate, do NOT invalidate `['worker', id]`.** `DELETE /token` sets status to `revoked`; revoked workers are excluded from `GET /v1/workers/{id}`, so that query would 404 and render the not-found card. On revoke success: invalidate only `['workers']` and `navigate('/workers')`. Never call `invalidateQueries(['worker', id])` in the revoke path.
3. **Labels PATCH is a full replace, not a per-key merge.** The labels editor loads the current label map and submits the **complete edited map** (adds, removes, and renames all reflected). Sending a partial map deletes the omitted keys.
4. **`['workers', 'revoked', ...]` is a prefix under `['workers']`.** `useRevokedWorkers` keys on `['workers', 'revoked', cursor]`; `useWorkers` keys on `['workers', sort]`. A single prefix invalidation `invalidateQueries({ queryKey: ['workers'] })` covers both the active list and the decommissioned list. Always invalidate the bare `['workers']` prefix, never a sort-specific key.
5. **204/202 responses are no-body.** The revoke and evict clients return `void`. Do not attempt to read a JSON body from them, and do not type them as returning `Worker`.
6. **Server coalesces disabled status.** `toWorkerResponse` (`workers.go:42`) sets `status = 'disabled'` and populates `disabled_at` when the worker is disabled. The Disable/Enable button label is driven by `disabled_at` presence (`disabled_at` present => show Enable; else show Disable + Drain). The optimistic toggle must mirror this: disabling sets `status: 'disabled'` and a truthy `disabled_at`; enabling clears `disabled_at` (and picks a live status - use `'online'` for the optimistic value; the ~3s poll reconciles the real liveness).

## File structure

New files (all under `web/src/`):

- `web/src/components/ConfirmDialog.tsx` - minimal shared confirm primitive (`role="dialog"`, labelled, Escape/Cancel dismiss, focus the cancel button on open, destructive variant). Lives in `components/` because Admin and Profile will reuse it.
- `web/src/components/ConfirmDialog.test.tsx`
- `web/src/workers/useWorkerActions.ts` - one `useMutation` per action, each with the correct `onSuccess`/`onMutate` cache handling.
- `web/src/workers/useWorkerActions.test.tsx`
- `web/src/workers/WorkerEditForm.tsx` - inline edit form (name, labels, max_slots); submits only changed fields.
- `web/src/workers/WorkerEditForm.test.tsx`
- `web/src/workers/WorkerActions.tsx` - admin-only action bar (Edit entry point, Disable/Enable toggle, Drain, Revoke), owning the ConfirmDialog + pending + inline-error state.
- `web/src/workers/WorkerActions.test.tsx`

Modified files:

- `web/src/workers/api.ts` - add `WorkerPatch`, `DisableWorkerResponse` types and `updateWorker`, `disableWorker`, `enableWorker`, `revokeWorkerToken`, `evictWorkspace` clients.
- `web/src/lib/api.ts` - broaden the no-body guard (202 as well as 204).
- `web/src/workers/WorkerDetailPage.tsx` - render `<WorkerActions>` under the header when `user?.is_admin`.
- `web/src/workers/WorkspacesPanel.tsx` - add an admin-only per-row Evict button wired through `useWorkerActions`, guarded by a ConfirmDialog.
- `web/src/workers/WorkspacesPanel.test.tsx` - extend for the evict flow.
- `web/src/workers/WorkerDetailPage.test.tsx` - extend for admin gating of the actions bar.

## Conventions to follow

- Run all commands from the `web/` directory: `cd web` first (relative to the worktree root `D:\dev\relay\.claude\worktrees\stoic-cannon-15b269`). Test command: `npm test` (`vitest run`). Build/typecheck: `npm run build` (`tsc -b && vite build`).
- Match existing style: functional components, named exports, Tailwind utility strings, design tokens (`text-fg`, `text-fg-mute`, `text-err`, `border-border`, `bg-white/5`, `bg-accent`). Error styling reuses the `SchedulesPage` pattern: `rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err`.
- Never use em dashes or en dashes; use regular hyphens.
- Do not edit `web/dist/`. If a build dirties it, `git checkout -- web/dist/` before committing.
- Use bash heredocs for commit messages (the Bash tool runs Git Bash).

---

## Task 1: API clients and types (`api.ts`) + `apiFetch` no-body fix

**Files:**
- Modify: `web/src/lib/api.ts:55`
- Modify: `web/src/workers/api.ts` (append after line 107)
- Test: `web/src/workers/useWorkerActions.test.tsx` (created here; the client-level assertions live inside the hook tests in Task 3, so this task's dedicated test targets the 202 fix and the client call shapes via a tiny direct test)

- [ ] **Step 1: Write the failing test for the 202/204 no-body handling**

Create `web/src/lib/api.test.ts`:

```ts
import { afterAll, afterEach, beforeAll, expect, test } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { apiFetch } from './api'

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

test('returns undefined for a 204 No Content response', async () => {
  server.use(http.delete('/v1/workers/w1/token', () => new HttpResponse(null, { status: 204 })))
  const out = await apiFetch<void>('/workers/w1/token', { method: 'DELETE' })
  expect(out).toBeUndefined()
})

test('returns undefined for a 202 Accepted empty-body response', async () => {
  server.use(http.post('/v1/workers/w1/workspaces/ws-a/evict', () => new HttpResponse(null, { status: 202 })))
  const out = await apiFetch<void>('/workers/w1/workspaces/ws-a/evict', { method: 'POST' })
  expect(out).toBeUndefined()
})
```

- [ ] **Step 2: Run the test to verify the 202 case fails**

Run: `cd web; npm test -- src/lib/api.test.ts`
Expected: the 204 test passes; the **202 test FAILS** because `apiFetch` falls through to `res.json()` on an empty 202 body and throws a JSON parse error.

- [ ] **Step 3: Broaden the no-body guard in `apiFetch`**

In `web/src/lib/api.ts`, replace line 55:

```ts
  if (res.status === 204) return undefined as T
```

with:

```ts
  // 204 (revoke) and 202 (evict, best-effort async) return no body.
  if (res.status === 204 || res.status === 202) return undefined as T
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web; npm test -- src/lib/api.test.ts`
Expected: both tests PASS.

- [ ] **Step 5: Add the worker mutation clients and types**

Append to `web/src/workers/api.ts` (after line 107):

```ts
export interface DisableWorkerResponse extends Worker {
  requeued_tasks: number
}

export interface WorkerPatch {
  name?: string
  labels?: Record<string, string>
  max_slots?: number
}

// Admin-only. Rename / edit labels / set max_slots. Fields omitted keep their
// current value; `labels`, when present, is a full replace of the label map (the
// server marshals the whole map), not a per-key merge.
export function updateWorker(id: string, patch: WorkerPatch): Promise<Worker> {
  return apiFetch<Worker>(`/workers/${id}`, { method: 'PATCH', json: patch })
}

// Admin-only. Disable (pause) the worker. requeue=true is the "drain" concept:
// in-flight tasks are requeued to other workers and cancelled here.
export function disableWorker(id: string, requeue: boolean): Promise<DisableWorkerResponse> {
  const q = requeue ? '?requeue=true' : ''
  return apiFetch<DisableWorkerResponse>(`/workers/${id}/disable${q}`, { method: 'POST' })
}

// Admin-only. Re-enable a disabled worker.
export function enableWorker(id: string): Promise<Worker> {
  return apiFetch<Worker>(`/workers/${id}/enable`, { method: 'POST' })
}

// Admin-only. Revoke the agent token. TERMINAL: also sets the worker to
// `revoked`, which excludes it from every list/get endpoint. Returns 204 (no
// body). After success the caller must navigate away, not re-fetch the worker.
export function revokeWorkerToken(id: string): Promise<void> {
  return apiFetch<void>(`/workers/${id}/token`, { method: 'DELETE' })
}

// Admin-only. Request eviction of a source workspace. Best-effort/async: returns
// 202 (no body); the agent evicts on its stream and confirms later via an
// inventory update. A held workspace is refused by the agent, not this endpoint.
export function evictWorkspace(id: string, shortId: string): Promise<void> {
  return apiFetch<void>(`/workers/${id}/workspaces/${shortId}/evict`, { method: 'POST' })
}
```

- [ ] **Step 6: Verify typecheck and the existing suite stay green**

Run: `cd web; npm run build`
Expected: PASS (no TS errors).
Run: `cd web; npm test -- src/lib/api.test.ts`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd web && git checkout -- dist/ 2>/dev/null; cd ..
git add web/src/lib/api.ts web/src/lib/api.test.ts web/src/workers/api.ts
git commit -m "feat(web): worker mutation API clients + 202 no-body handling"
```

---

## Task 2: `ConfirmDialog` shared primitive

**Files:**
- Create: `web/src/components/ConfirmDialog.tsx`
- Test: `web/src/components/ConfirmDialog.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/ConfirmDialog.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ConfirmDialog } from './ConfirmDialog'

test('renders title, body, confirm and cancel; is a labelled dialog', () => {
  render(
    <ConfirmDialog
      title="Disable render-rig-A?"
      body="It will stop receiving new tasks."
      confirmLabel="Disable"
      onConfirm={() => {}}
      onCancel={() => {}}
    />,
  )
  const dialog = screen.getByRole('dialog')
  expect(dialog).toHaveAccessibleName('Disable render-rig-A?')
  expect(screen.getByText('It will stop receiving new tasks.')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
})

test('Cancel invokes onCancel and not onConfirm', async () => {
  const onConfirm = vi.fn()
  const onCancel = vi.fn()
  render(
    <ConfirmDialog title="t" body="b" confirmLabel="Go" onConfirm={onConfirm} onCancel={onCancel} />,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(onCancel).toHaveBeenCalledOnce()
  expect(onConfirm).not.toHaveBeenCalled()
})

test('Escape invokes onCancel', async () => {
  const onCancel = vi.fn()
  render(
    <ConfirmDialog title="t" body="b" confirmLabel="Go" onConfirm={() => {}} onCancel={onCancel} />,
  )
  await userEvent.keyboard('{Escape}')
  expect(onCancel).toHaveBeenCalledOnce()
})

test('Confirm invokes onConfirm', async () => {
  const onConfirm = vi.fn()
  render(
    <ConfirmDialog title="t" body="b" confirmLabel="Go" onConfirm={onConfirm} onCancel={() => {}} />,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Go' }))
  expect(onConfirm).toHaveBeenCalledOnce()
})

test('destructive variant still renders the confirm button', () => {
  render(
    <ConfirmDialog
      title="t"
      body="b"
      confirmLabel="Revoke"
      destructive
      onConfirm={() => {}}
      onCancel={() => {}}
    />,
  )
  expect(screen.getByRole('button', { name: 'Revoke' })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web; npm test -- src/components/ConfirmDialog.test.tsx`
Expected: FAIL with "Failed to resolve import './ConfirmDialog'".

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/components/ConfirmDialog.tsx`:

```tsx
import { useEffect, useId, useRef } from 'react'

interface ConfirmDialogProps {
  title: string
  body: string
  confirmLabel: string
  destructive?: boolean
  onConfirm: () => void
  onCancel: () => void
}

// Minimal shared confirm primitive. No portal library, no focus-trap dependency:
// role="dialog" labelled by its title, Escape and Cancel both dismiss, and the
// cancel button is focused on open. Reused by Admin/Profile later.
export function ConfirmDialog({
  title,
  body,
  confirmLabel,
  destructive,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const titleId = useId()
  const cancelRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    cancelRef.current?.focus()
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onCancel()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onCancel])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="w-full max-w-sm rounded-card border border-border bg-bg p-5 shadow-xl"
      >
        <h2 id={titleId} className="text-[15px] font-medium text-fg">
          {title}
        </h2>
        <p className="mt-2 text-[13px] text-fg-mute">{body}</p>
        <div className="mt-5 flex justify-end gap-2">
          <button
            type="button"
            ref={cancelRef}
            onClick={onCancel}
            className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg-mute"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            className={
              'rounded-md px-3 py-1.5 text-[12px] font-medium ' +
              (destructive ? 'bg-err/20 text-err border border-err/50' : 'bg-accent text-bg')
            }
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web; npm test -- src/components/ConfirmDialog.test.tsx`
Expected: PASS (all five).

- [ ] **Step 5: Commit**

```bash
cd web && git checkout -- dist/ 2>/dev/null; cd ..
git add web/src/components/ConfirmDialog.tsx web/src/components/ConfirmDialog.test.tsx
git commit -m "feat(web): ConfirmDialog shared primitive"
```

---

## Task 3: `useWorkerActions` mutation hook

**Files:**
- Create: `web/src/workers/useWorkerActions.ts`
- Test: `web/src/workers/useWorkerActions.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/workers/useWorkerActions.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkerActions } from './useWorkerActions'
import type { Worker } from './api'

const ID = 'w1'

const WORKER: Worker = {
  id: ID,
  name: 'rig',
  hostname: 'h',
  cpu_cores: 8,
  ram_gb: 32,
  gpu_count: 0,
  gpu_model: '',
  os: 'linux',
  max_slots: 2,
  labels: null,
  status: 'online',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('updateWorker PATCHes, writes the response into the cache, and invalidates worker + workers', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.patch(`/v1/workers/${ID}`, () => HttpResponse.json({ ...WORKER, name: 'renamed' })),
  )
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  await result.current.update.mutateAsync({ name: 'renamed' })

  expect((client.getQueryData(['worker', ID]) as Worker).name).toBe('renamed')
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['worker', ID] }))
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['workers'] }))
})

test('disable (requeue=false) POSTs /disable with no query string and invalidates', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let seenUrl = ''
  server.use(
    http.post(`/v1/workers/${ID}/disable`, ({ request }) => {
      seenUrl = new URL(request.url).search
      return HttpResponse.json({ ...WORKER, status: 'disabled', disabled_at: 'now', requeued_tasks: 0 })
    }),
  )
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  await result.current.disable.mutateAsync(false)

  expect(seenUrl).toBe('')
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['workers'] }))
})

test('disable (requeue=true) POSTs /disable?requeue=true and returns requeued_tasks', async () => {
  const client = newClient()
  let seenUrl = ''
  server.use(
    http.post(`/v1/workers/${ID}/disable`, ({ request }) => {
      seenUrl = new URL(request.url).search
      return HttpResponse.json({ ...WORKER, status: 'disabled', disabled_at: 'now', requeued_tasks: 3 })
    }),
  )
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  const res = await result.current.disable.mutateAsync(true)

  expect(seenUrl).toBe('?requeue=true')
  expect(res.requeued_tasks).toBe(3)
})

test('enable POSTs /enable and invalidates', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(http.post(`/v1/workers/${ID}/enable`, () => HttpResponse.json(WORKER)))
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  await result.current.enable.mutateAsync()

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['workers'] }))
})

test('revoke DELETEs /token, does NOT invalidate [worker,id], invalidates [workers]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(http.delete(`/v1/workers/${ID}/token`, () => new HttpResponse(null, { status: 204 })))
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  await result.current.revoke.mutateAsync()

  expect(spy).not.toHaveBeenCalledWith({ queryKey: ['worker', ID] })
  expect(spy).toHaveBeenCalledWith({ queryKey: ['workers'] })
})

test('evict POSTs the evict path and invalidates the workspaces query', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let seen = false
  server.use(
    http.post(`/v1/workers/${ID}/workspaces/ws-a/evict`, () => {
      seen = true
      return new HttpResponse(null, { status: 202 })
    }),
  )
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  await result.current.evict.mutateAsync('ws-a')

  expect(seen).toBe(true)
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['worker', ID, 'workspaces'] }))
})

test('disable optimistically flips cached status and rolls back on error', async () => {
  const client = newClient()
  client.setQueryData(['worker', ID], WORKER)
  server.use(
    http.post(`/v1/workers/${ID}/disable`, () => HttpResponse.json({ error: 'boom' }, { status: 500 })),
  )
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })

  await expect(result.current.disable.mutateAsync(false)).rejects.toBeTruthy()
  // After rollback the cached worker is back to its pre-mutation state.
  await waitFor(() => expect((client.getQueryData(['worker', ID]) as Worker).status).toBe('online'))
  expect((client.getQueryData(['worker', ID]) as Worker).disabled_at).toBeUndefined()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web; npm test -- src/workers/useWorkerActions.test.tsx`
Expected: FAIL with "Failed to resolve import './useWorkerActions'".

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/workers/useWorkerActions.ts`:

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { Worker, WorkerPatch } from './api'
import {
  disableWorker,
  enableWorker,
  evictWorkspace,
  revokeWorkerToken,
  updateWorker,
} from './api'

// Mutations for the admin worker-detail actions. Default strategy is
// invalidate-on-success (mirrors useScheduleActions). Key invariants:
//  - Invalidate the bare ['workers'] prefix so both the active list (['workers',
//    sort]) and the revoked list (['workers','revoked',cursor]) refresh.
//  - Revoke does NOT invalidate ['worker', id] (that query 404s post-revoke); the
//    caller navigates to /workers instead.
//  - Only the disable/enable toggle is optimistic; all others plain-invalidate.
export function useWorkerActions(id: string) {
  const qc = useQueryClient()

  const update = useMutation({
    mutationFn: (patch: WorkerPatch) => updateWorker(id, patch),
    onSuccess: (updated) => {
      qc.setQueryData(['worker', id], updated)
      qc.invalidateQueries({ queryKey: ['worker', id] })
      qc.invalidateQueries({ queryKey: ['workers'] })
    },
  })

  const disable = useMutation({
    mutationFn: (requeue: boolean) => disableWorker(id, requeue),
    // Optimistic: flip the cached status so the pill does not lag the ~3s poll.
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: ['worker', id] })
      const previous = qc.getQueryData<Worker>(['worker', id])
      if (previous) {
        qc.setQueryData<Worker>(['worker', id], {
          ...previous,
          status: 'disabled',
          disabled_at: new Date().toISOString(),
        })
      }
      return { previous }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.previous) qc.setQueryData(['worker', id], ctx.previous)
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['worker', id] })
      qc.invalidateQueries({ queryKey: ['workers'] })
    },
  })

  const enable = useMutation({
    mutationFn: () => enableWorker(id),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: ['worker', id] })
      const previous = qc.getQueryData<Worker>(['worker', id])
      if (previous) {
        qc.setQueryData<Worker>(['worker', id], {
          ...previous,
          status: 'online',
          disabled_at: undefined,
        })
      }
      return { previous }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.previous) qc.setQueryData(['worker', id], ctx.previous)
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['worker', id] })
      qc.invalidateQueries({ queryKey: ['workers'] })
    },
  })

  const revoke = useMutation({
    mutationFn: () => revokeWorkerToken(id),
    // Revoke is terminal: the worker becomes `revoked` and GET /workers/{id}
    // 404s. Do NOT invalidate ['worker', id]; the caller navigates away.
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['workers'] })
    },
  })

  const evict = useMutation({
    mutationFn: (shortId: string) => evictWorkspace(id, shortId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['worker', id, 'workspaces'] })
    },
  })

  return { update, disable, enable, revoke, evict }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web; npm test -- src/workers/useWorkerActions.test.tsx`
Expected: PASS (all eight).

- [ ] **Step 5: Commit**

```bash
cd web && git checkout -- dist/ 2>/dev/null; cd ..
git add web/src/workers/useWorkerActions.ts web/src/workers/useWorkerActions.test.tsx
git commit -m "feat(web): useWorkerActions mutation hook"
```

---

## Task 4: `WorkerEditForm` inline edit form

**Files:**
- Create: `web/src/workers/WorkerEditForm.tsx`
- Test: `web/src/workers/WorkerEditForm.test.tsx`

The form owns local state for name, max_slots, and a list of `{ key, value }` label rows (an array, so keys can be renamed/removed). On submit it builds a `WorkerPatch` with **only changed fields**: `name` only if changed; `max_slots` only if changed; `labels` only if the rebuilt map differs from the current map. `labels` is always sent as the **full** rebuilt map (full replace).

- [ ] **Step 1: Write the failing tests**

Create `web/src/workers/WorkerEditForm.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { WorkerEditForm } from './WorkerEditForm'
import type { Worker } from './api'

const WORKER: Worker = {
  id: 'w1',
  name: 'rig-A',
  hostname: 'h',
  cpu_cores: 8,
  ram_gb: 32,
  gpu_count: 0,
  gpu_model: '',
  os: 'linux',
  max_slots: 2,
  labels: { rack: 'A', tier: 'gold' },
  status: 'online',
}

test('pre-fills current name, max_slots, and labels', () => {
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={() => {}} onCancel={() => {}} />)
  expect(screen.getByLabelText(/name/i)).toHaveValue('rig-A')
  expect(screen.getByLabelText(/max slots/i)).toHaveValue(2)
  expect(screen.getByDisplayValue('rack')).toBeInTheDocument()
  expect(screen.getByDisplayValue('gold')).toBeInTheDocument()
})

test('submits only the changed name field', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  const name = screen.getByLabelText(/name/i)
  await userEvent.clear(name)
  await userEvent.type(name, 'rig-B')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).toHaveBeenCalledWith({ name: 'rig-B' })
})

test('submits only the changed max_slots field', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  const slots = screen.getByLabelText(/max slots/i)
  await userEvent.clear(slots)
  await userEvent.type(slots, '5')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).toHaveBeenCalledWith({ max_slots: 5 })
})

test('editing labels submits the full edited map (add + remove a key)', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  // Remove the "tier" row.
  await userEvent.click(screen.getByRole('button', { name: 'Remove tier' }))
  // Add a new "zone=east" row.
  await userEvent.click(screen.getByRole('button', { name: /add label/i }))
  const keyInputs = screen.getAllByPlaceholderText('key')
  const valInputs = screen.getAllByPlaceholderText('value')
  await userEvent.type(keyInputs[keyInputs.length - 1], 'zone')
  await userEvent.type(valInputs[valInputs.length - 1], 'east')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).toHaveBeenCalledWith({ labels: { rack: 'A', zone: 'east' } })
})

test('submitting with no changes sends an empty patch', async () => {
  const onSubmit = vi.fn()
  render(<WorkerEditForm worker={WORKER} pending={false} onSubmit={onSubmit} onCancel={() => {}} />)
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).toHaveBeenCalledWith({})
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web; npm test -- src/workers/WorkerEditForm.test.tsx`
Expected: FAIL with "Failed to resolve import './WorkerEditForm'".

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/workers/WorkerEditForm.tsx`:

```tsx
import { useState } from 'react'
import { Field } from '../components/Field'
import { Input } from '../components/Input'
import type { Worker, WorkerPatch } from './api'

interface LabelRow {
  key: string
  value: string
}

function toRows(labels: Record<string, string> | null): LabelRow[] {
  if (!labels) return []
  return Object.entries(labels).map(([key, value]) => ({ key, value }))
}

function rowsToMap(rows: LabelRow[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const r of rows) {
    const key = r.key.trim()
    if (key) out[key] = r.value
  }
  return out
}

function sameMap(a: Record<string, string>, b: Record<string, string>): boolean {
  const ak = Object.keys(a)
  const bk = Object.keys(b)
  if (ak.length !== bk.length) return false
  return ak.every((k) => a[k] === b[k])
}

interface WorkerEditFormProps {
  worker: Worker
  pending: boolean
  onSubmit: (patch: WorkerPatch) => void
  onCancel: () => void
}

// Inline edit form for name / labels / max_slots. Builds a WorkerPatch with only
// changed fields; labels, when changed, is submitted as the full rebuilt map
// (the server does a full replace of the label map, not a per-key merge).
export function WorkerEditForm({ worker, pending, onSubmit, onCancel }: WorkerEditFormProps) {
  const [name, setName] = useState(worker.name)
  const [maxSlots, setMaxSlots] = useState(String(worker.max_slots))
  const [rows, setRows] = useState<LabelRow[]>(toRows(worker.labels))

  function submit(e: React.FormEvent) {
    e.preventDefault()
    const patch: WorkerPatch = {}
    if (name !== worker.name) patch.name = name
    const nextSlots = Number(maxSlots)
    if (!Number.isNaN(nextSlots) && nextSlots !== worker.max_slots) patch.max_slots = nextSlots
    const nextLabels = rowsToMap(rows)
    if (!sameMap(nextLabels, worker.labels ?? {})) patch.labels = nextLabels
    onSubmit(patch)
  }

  return (
    <form onSubmit={submit} className="rounded-card border border-border bg-white/5 p-4">
      <Field label="Name" htmlFor="worker-name">
        <Input id="worker-name" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Max slots" htmlFor="worker-slots">
        <Input
          id="worker-slots"
          type="number"
          value={maxSlots}
          onChange={(e) => setMaxSlots(e.target.value)}
        />
      </Field>
      <div className="mb-3">
        <div className="mb-1 font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">Labels</div>
        <div className="flex flex-col gap-1.5">
          {rows.map((row, i) => (
            <div key={i} className="flex items-center gap-1.5">
              <Input
                placeholder="key"
                value={row.key}
                onChange={(e) =>
                  setRows(rows.map((r, j) => (j === i ? { ...r, key: e.target.value } : r)))
                }
              />
              <Input
                placeholder="value"
                value={row.value}
                onChange={(e) =>
                  setRows(rows.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)))
                }
              />
              <button
                type="button"
                aria-label={`Remove ${row.key}`}
                onClick={() => setRows(rows.filter((_, j) => j !== i))}
                className="shrink-0 rounded-md border border-border px-2 py-1 text-[11px] text-fg-mute"
              >
                x
              </button>
            </div>
          ))}
        </div>
        <button
          type="button"
          onClick={() => setRows([...rows, { key: '', value: '' }])}
          className="mt-1.5 rounded-md border border-border px-2 py-1 text-[11px] text-fg-mute"
        >
          Add label
        </button>
      </div>
      <div className="flex justify-end gap-2">
        <button
          type="button"
          onClick={onCancel}
          className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg-mute"
        >
          Cancel
        </button>
        <button
          type="submit"
          disabled={pending}
          className="rounded-md bg-accent px-3 py-1.5 text-[12px] font-medium text-bg disabled:opacity-50"
        >
          Save
        </button>
      </div>
    </form>
  )
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web; npm test -- src/workers/WorkerEditForm.test.tsx`
Expected: PASS (all five).

- [ ] **Step 5: Commit**

```bash
cd web && git checkout -- dist/ 2>/dev/null; cd ..
git add web/src/workers/WorkerEditForm.tsx web/src/workers/WorkerEditForm.test.tsx
git commit -m "feat(web): WorkerEditForm inline edit form"
```

---

## Task 5: `WorkerActions` bar + `WorkerDetailPage` wiring + admin gating

**Files:**
- Create: `web/src/workers/WorkerActions.tsx`
- Test: `web/src/workers/WorkerActions.test.tsx`
- Modify: `web/src/workers/WorkerDetailPage.tsx:165` (add `<WorkerActions>` under the header, admin-gated)
- Test: `web/src/workers/WorkerDetailPage.test.tsx` (extend for the actions bar)

`WorkerActions` owns: the edit-form toggle, the pending state (disable its own buttons while any of its mutations run), the ConfirmDialog for Disable/Drain/Revoke, and the inline error message. It receives the `worker` and uses `useWorkerActions(worker.id)` internally plus `useNavigate()` for the revoke redirect.

- [ ] **Step 1: Write the failing tests for `WorkerActions`**

Create `web/src/workers/WorkerActions.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, test, vi } from 'vitest'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { WorkerActions } from './WorkerActions'
import type { Worker } from './api'

const ID = 'w1'

const WORKER: Worker = {
  id: ID,
  name: 'rig-A',
  hostname: 'h',
  cpu_cores: 8,
  ram_gb: 32,
  gpu_count: 0,
  gpu_model: '',
  os: 'linux',
  max_slots: 2,
  labels: null,
  status: 'online',
}

function LocationProbe() {
  return <div data-testid="loc">{useLocation().pathname}</div>
}

function renderActions(worker: Worker) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/workers/${ID}`]}>
        <Routes>
          <Route path="/workers/:id" element={<><WorkerActions worker={worker} /><LocationProbe /></>} />
          <Route path="/workers" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

test('online worker shows Disable and Drain (not Enable)', () => {
  renderActions(WORKER)
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Drain' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Enable' })).not.toBeInTheDocument()
})

test('disabled worker shows Enable (not Disable/Drain)', () => {
  renderActions({ ...WORKER, status: 'disabled', disabled_at: '2026-07-01T00:00:00Z' })
  expect(screen.getByRole('button', { name: 'Enable' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Disable' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Drain' })).not.toBeInTheDocument()
})

test('clicking Disable opens a confirm dialog; cancel fires no request', async () => {
  let hits = 0
  server.use(http.post(`/v1/workers/${ID}/disable`, () => { hits++; return HttpResponse.json({ ...WORKER, requeued_tasks: 0 }) }))
  renderActions(WORKER)
  await userEvent.click(screen.getByRole('button', { name: 'Disable' }))
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await new Promise((r) => setTimeout(r, 20))
  expect(hits).toBe(0)
})

test('confirming Disable fires exactly one request to /disable', async () => {
  let hits = 0
  server.use(http.post(`/v1/workers/${ID}/disable`, () => { hits++; return HttpResponse.json({ ...WORKER, status: 'disabled', disabled_at: 'now', requeued_tasks: 0 }) }))
  renderActions(WORKER)
  await userEvent.click(screen.getByRole('button', { name: 'Disable' }))
  // The confirm button inside the dialog is also labelled "Disable"; scope to the dialog.
  const dialog = screen.getByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Disable' }))
  await waitFor(() => expect(hits).toBe(1))
})

test('revoke success navigates to /workers', async () => {
  server.use(http.delete(`/v1/workers/${ID}/token`, () => new HttpResponse(null, { status: 204 })))
  renderActions(WORKER)
  await userEvent.click(screen.getByRole('button', { name: 'Revoke' }))
  const dialog = screen.getByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }))
  await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent('/workers'))
})

test('a mutation error renders an inline message and leaves the actions mounted', async () => {
  server.use(http.post(`/v1/workers/${ID}/enable`, () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderActions({ ...WORKER, status: 'disabled', disabled_at: 'now' })
  await userEvent.click(screen.getByRole('button', { name: 'Enable' }))
  expect(await screen.findByText(/boom|500/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Enable' })).toBeInTheDocument()
})
```

Add these imports at the top of the test file (they are used above): `import { waitFor, within } from '@testing-library/react'` (merge into the existing `@testing-library/react` import line).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web; npm test -- src/workers/WorkerActions.test.tsx`
Expected: FAIL with "Failed to resolve import './WorkerActions'".

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/workers/WorkerActions.tsx`:

```tsx
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { WorkerEditForm } from './WorkerEditForm'
import { useWorkerActions } from './useWorkerActions'
import type { Worker, WorkerPatch } from './api'

type Pending = null | 'disable' | 'drain' | 'revoke'

// Admin-only action bar for the worker detail page. Owns the edit-form toggle,
// the confirm dialog for destructive/disruptive actions, and the inline error.
export function WorkerActions({ worker }: { worker: Worker }) {
  const navigate = useNavigate()
  const { update, disable, enable, revoke } = useWorkerActions(worker.id)
  const [editing, setEditing] = useState(false)
  const [confirm, setConfirm] = useState<Pending>(null)

  const busy =
    update.isPending || disable.isPending || enable.isPending || revoke.isPending
  const isDisabled = Boolean(worker.disabled_at)
  const actionError = (update.error ?? disable.error ?? enable.error ?? revoke.error) as
    | Error
    | null

  function onSave(patch: WorkerPatch) {
    update.mutate(patch, { onSuccess: () => setEditing(false) })
  }

  function runConfirmed() {
    if (confirm === 'disable') disable.mutate(false)
    else if (confirm === 'drain') disable.mutate(true)
    else if (confirm === 'revoke') revoke.mutate(undefined, { onSuccess: () => navigate('/workers') })
    setConfirm(null)
  }

  const confirmCopy: Record<Exclude<Pending, null>, { title: string; body: string; label: string; destructive?: boolean }> = {
    disable: {
      title: `Disable ${worker.name}?`,
      body: 'It will stop receiving new tasks. In-flight tasks keep running.',
      label: 'Disable',
    },
    drain: {
      title: `Drain ${worker.name}?`,
      body: 'It stops receiving new tasks and its in-flight tasks are requeued to other workers and cancelled here.',
      label: 'Drain',
    },
    revoke: {
      title: `Revoke ${worker.name}'s agent token?`,
      body: 'This decommissions the worker. It disappears from the fleet and must re-enroll to return.',
      label: 'Revoke',
      destructive: true,
    },
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={() => setEditing((v) => !v)}
          className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg"
        >
          Edit
        </button>
        {isDisabled ? (
          <button
            type="button"
            disabled={busy}
            onClick={() => enable.mutate()}
            className="rounded-md border border-accent/50 bg-accent/15 px-3 py-1.5 text-[12px] text-fg disabled:opacity-40"
          >
            Enable
          </button>
        ) : (
          <>
            <button
              type="button"
              disabled={busy}
              onClick={() => setConfirm('disable')}
              className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg-mute disabled:opacity-40"
            >
              Disable
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={() => setConfirm('drain')}
              className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg-mute disabled:opacity-40"
            >
              Drain
            </button>
          </>
        )}
        <button
          type="button"
          disabled={busy}
          onClick={() => setConfirm('revoke')}
          className="rounded-md border border-err/50 bg-err/10 px-3 py-1.5 text-[12px] text-err disabled:opacity-40"
        >
          Revoke
        </button>
      </div>

      {editing && (
        <WorkerEditForm
          worker={worker}
          pending={update.isPending}
          onSubmit={onSave}
          onCancel={() => setEditing(false)}
        />
      )}

      {actionError ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      {disable.data && disable.data.requeued_tasks > 0 ? (
        <div className="rounded-card border border-accent/40 bg-accent/10 px-4 py-2 text-[12px] text-accent">
          Requeued {disable.data.requeued_tasks} task(s).
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

- [ ] **Step 4: Run the `WorkerActions` tests to verify they pass**

Run: `cd web; npm test -- src/workers/WorkerActions.test.tsx`
Expected: PASS (all six).

- [ ] **Step 5: Wire `WorkerActions` into `WorkerDetailPage`**

In `web/src/workers/WorkerDetailPage.tsx`, add the import after line 7:

```tsx
import { WorkerActions } from './WorkerActions'
```

Then insert the actions bar under the header block. Replace the closing `</div>` of the header block (currently line 81) so the actions render directly after it. Concretely, after the header `<div className="flex flex-col gap-1"> ... </div>` block (ends line 81), add:

```tsx
      {user?.is_admin && <WorkerActions worker={worker} />}
```

The final structure of the top of the returned tree becomes:

```tsx
  return (
    <div className={`flex flex-col gap-5 ${livenessView(worker.status).dimClass}`}>
      <div className="flex flex-col gap-1">
        {/* ...existing header content... */}
      </div>

      {user?.is_admin && <WorkerActions worker={worker} />}

      <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-3">
        {/* ...existing hardware cards... */}
```

- [ ] **Step 6: Write the failing admin-gating tests in `WorkerDetailPage.test.tsx`**

Add to `web/src/workers/WorkerDetailPage.test.tsx`:

```tsx
test('admins see the worker action bar', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(http.get(`/v1/workers/${ID}/workspaces`, () => HttpResponse.json([])))
  renderDetail(true)
  expect(await screen.findByRole('button', { name: 'Edit' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Revoke' })).toBeInTheDocument()
})

test('non-admins see none of the action controls', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  await screen.findByText('render-rig-A')
  expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Disable' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Revoke' })).not.toBeInTheDocument()
})
```

- [ ] **Step 7: Run the detail-page tests to verify they pass**

Run: `cd web; npm test -- src/workers/WorkerDetailPage.test.tsx`
Expected: PASS (existing tests plus the two new ones).

- [ ] **Step 8: Run the full suite and build**

Run: `cd web; npm test`
Expected: all green.
Run: `cd web; npm run build`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
cd web && git checkout -- dist/ 2>/dev/null; cd ..
git add web/src/workers/WorkerActions.tsx web/src/workers/WorkerActions.test.tsx web/src/workers/WorkerDetailPage.tsx web/src/workers/WorkerDetailPage.test.tsx
git commit -m "feat(web): WorkerActions bar wired into detail page with admin gating"
```

---

## Task 6: `WorkspacesPanel` per-row Evict button

**Files:**
- Modify: `web/src/workers/WorkspacesPanel.tsx`
- Test: `web/src/workers/WorkspacesPanel.test.tsx` (extend)

The panel is already mounted only for admins, so no extra `is_admin` check is needed inside it. Add a per-row Evict button that opens a ConfirmDialog and, on confirm, calls `evict.mutate(shortId)`. Add an ACTIONS column to the grid.

- [ ] **Step 1: Write the failing tests**

Add to `web/src/workers/WorkspacesPanel.test.tsx`:

```tsx
import userEvent from '@testing-library/user-event'
import { waitFor, within } from '@testing-library/react'

test('clicking Evict opens a confirm dialog; confirm POSTs the evict path', async () => {
  let hits = 0
  server.use(
    http.get('/v1/workers/w1/workspaces', () =>
      HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-a4f2', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
      ]),
    ),
  )
  server.use(
    http.post('/v1/workers/w1/workspaces/ws-a4f2/evict', () => {
      hits++
      return new HttpResponse(null, { status: 202 })
    }),
  )
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  await screen.findByText('ws-a4f2')
  await userEvent.click(screen.getByRole('button', { name: /evict/i }))
  const dialog = screen.getByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Evict' }))
  await waitFor(() => expect(hits).toBe(1))
})

test('cancelling the evict confirm fires no request', async () => {
  let hits = 0
  server.use(
    http.get('/v1/workers/w1/workspaces', () =>
      HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-a4f2', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
      ]),
    ),
  )
  server.use(http.post('/v1/workers/w1/workspaces/ws-a4f2/evict', () => { hits++; return new HttpResponse(null, { status: 202 }) }))
  renderWithQuery(<WorkspacesPanel workerId="w1" />)
  await screen.findByText('ws-a4f2')
  await userEvent.click(screen.getByRole('button', { name: /evict/i }))
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await new Promise((r) => setTimeout(r, 20))
  expect(hits).toBe(0)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web; npm test -- src/workers/WorkspacesPanel.test.tsx`
Expected: FAIL - no Evict button found.

- [ ] **Step 3: Write the minimal implementation**

Replace the full contents of `web/src/workers/WorkspacesPanel.tsx`:

```tsx
import { useState } from 'react'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { formatRelativeTime } from './liveness'
import { useWorkerActions } from './useWorkerActions'
import { useWorkerWorkspaces } from './useWorkerWorkspaces'

const COLS = 'grid grid-cols-[120px_90px_1fr_120px_90px_90px]'

// Admin-only source workspaces table with per-row evict. Mounted by
// WorkerDetailPage only when the current user is an admin, so no inner is_admin
// check is needed. Eviction is best-effort/async (202): the row does not vanish
// immediately; the 15s workspace poll reconciles once the agent confirms.
export function WorkspacesPanel({ workerId }: { workerId: string }) {
  const { data, isLoading } = useWorkerWorkspaces(workerId)
  const { evict } = useWorkerActions(workerId)
  const [confirmId, setConfirmId] = useState<string | null>(null)
  const rows = data ?? []

  function runEvict() {
    if (confirmId) evict.mutate(confirmId)
    setConfirmId(null)
  }

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
          <span className="text-right">ACTIONS</span>
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
            <span className="flex justify-end">
              <button
                type="button"
                disabled={evict.isPending}
                onClick={() => setConfirmId(ws.short_id)}
                className="rounded-md border border-err/50 bg-err/10 px-2 py-0.5 text-[10px] text-err disabled:opacity-40"
              >
                Evict
              </button>
            </span>
          </div>
        ))}
      </div>

      {evict.error ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {(evict.error as Error).message}
        </div>
      ) : null}

      {confirmId && (
        <ConfirmDialog
          title={`Evict workspace ${confirmId}?`}
          body="The agent removes it on next opportunity. A held workspace is refused."
          confirmLabel="Evict"
          onConfirm={runEvict}
          onCancel={() => setConfirmId(null)}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web; npm test -- src/workers/WorkspacesPanel.test.tsx`
Expected: PASS (existing two plus the two new ones).

- [ ] **Step 5: Add a non-admin absence assertion in `WorkerDetailPage.test.tsx`**

Extend the existing `non-admins never see or fetch workspaces` test (or add a focused one) to assert the Evict button is absent for non-admins. Since the whole panel is unmounted for non-admins, the existing `expect(screen.queryByText('SOURCE WORKSPACES')).not.toBeInTheDocument()` already covers this transitively; add an explicit button assertion for clarity:

```tsx
  expect(screen.queryByRole('button', { name: /evict/i })).not.toBeInTheDocument()
```

Place it alongside the existing assertions in that test.

- [ ] **Step 6: Run the full suite and build**

Run: `cd web; npm test`
Expected: all green.
Run: `cd web; npm run build`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd web && git checkout -- dist/ 2>/dev/null; cd ..
git add web/src/workers/WorkspacesPanel.tsx web/src/workers/WorkspacesPanel.test.tsx web/src/workers/WorkerDetailPage.test.tsx
git commit -m "feat(web): per-row Evict button in workspaces panel"
```

---

## Final verification

- [ ] **Full test suite**

Run: `cd web; npm test`
Expected: all suites green, including the new files.

- [ ] **Production build / typecheck**

Run: `cd web; npm run build`
Expected: PASS, no TS errors.

- [ ] **Confirm `web/dist/` is not staged**

Run: `git status --short web/dist`
Expected: no output (dist untouched). If dirty: `git checkout -- web/dist/`.

## Contract verification (do this before final commit)

Re-confirm TS types field-for-field against Go (already checked during planning, re-verify after edits):

- `DisableWorkerResponse extends Worker` + `requeued_tasks: number` matches `disableWorkerResponse` (embeds `workerResponse`, adds `RequeuedTasks int json:"requeued_tasks"`, `internal/api/workers.go:37-40`).
- `WorkerPatch` fields (`name?`, `labels?`, `max_slots?`) match `handleUpdateWorker`'s body struct (`name *string`, `labels map[string]string`, `max_slots *int32`, `workers.go:381-385`).
- 204 (revoke) and 202 (evict) are handled as no-body by `apiFetch` (Task 1 broadened the guard).

## Spec coverage self-check

- Rename / labels / slots -> Task 4 (form) + Task 3 (`update` mutation) + Task 5 (Edit entry point). Full-replace labels behavior enforced in Task 4.
- Disable / Drain / Enable -> Task 3 (mutations, optimistic toggle) + Task 5 (buttons + confirm). Drain surfaces `requeued_tasks` (Task 5 success note).
- Revoke -> Task 3 (`revoke` mutation, no detail invalidation) + Task 5 (destructive confirm + navigate).
- Evict -> Task 3 (`evict` mutation) + Task 6 (per-row button + confirm).
- Admin gating -> Task 5 (detail page gate) + Task 6 (panel already admin-mounted).
- ConfirmDialog primitive -> Task 2.
- Mutation strategy / query-key invalidation -> Task 3, matching the spec's per-action table.
- `apiFetch` 202 handling -> Task 1 (the one non-obvious gap not in the spec's file list but required for evict to work).
