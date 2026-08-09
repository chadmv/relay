# Admin Console - Route Shell + Users Tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `/admin` stub with an admin-gated, tabbed `/admin/:tab` shell whose only registered tab - Users - is fully wired to the six existing admin user endpoints (list with sort / cursor pagination / include-archived / exact-email filter, create, rename, archive, unarchive, password reset).

**Architecture:** A new feature module `web/src/admin/` mirroring `web/src/workers/`. The shell is a registry (`ADMIN_TABS`) plus a switch: `AdminPage` looks up `useParams().tab` and renders that entry's `Panel`, redirecting unknown segments to `/admin/users`. Tabs that are not built yet are absent from the registry, so no dead tabs ship. The Users tab is a standard relay list page: one non-polling TanStack query keyed `['users', sort, includeArchived, cursor, email]`, five mutations that each invalidate the bare `['users']` prefix, `computePageRange` pagination, and `ConfirmDialog` for destructive actions.

**Tech Stack:** React 18, TypeScript, TanStack Query v5, react-router-dom v7, Tailwind v4 (Holo design tokens), Vitest + Testing Library + MSW.

**Spec:** `docs/superpowers/specs/2026-08-08-admin-console-shell-users-tab.md`

---

## Slice independence declaration

- **Backend slice: none.** This plan changes **zero Go files**. All six endpoints already exist and are already `auth(admin(...))`-gated (`internal/api/server.go:150-156`). No `.sql` edits, so no `make generate`, and no Invariant (epoch fence, job-spec pipeline, bounded sender, identity-checked teardown, interior pointers, single JSON entry point) is in play.
- **Frontend slice: one, and it is SEQUENTIAL.** Do **not** split these tasks across two frontend engineers running in parallel. Reasons:
  - Tasks 8, 9, and 10 all write into the same new `web/src/admin/` tree, and Task 10 edits `web/src/app/router.tsx` and `web/src/shell/HoloShell.tsx` - files that any second writer would collide on.
  - Task 9 imports the component Task 8 creates (`tabs.ts` -> `UsersTab`), and Task 8 imports every component from Tasks 1-7. The dependency chain is nearly linear; there is no honest parallel cut.
  - The project has already been burned by concurrent writers on shared frontend files. One engineer, tasks in order.
- **Parallelism the conductor can still use:** none within this plan. If the batch has other independent work, that work can run alongside this whole plan, but this plan is a single serial unit.

---

## Conventions for every task

- All frontend commands run from the `web/` directory.
- Single test file: `npx vitest run src/<path>.test.tsx`. Full suite: `npm test`.
- TDD: write the failing test, run it and watch it fail, implement, run it and watch it pass, commit.
- MSW is configured with `onUnhandledRequest: 'error'` (`web/src/test/setup.ts:5`). Every endpoint a test's component touches must have a handler, including `/v1/users/me` whenever `AuthProvider` is mounted.
- House rule: never use em dashes or en dashes. Use hyphens. (Ellipsis `…` is fine and is used in one placeholder, copied from the hi-fi.)
- Never reformat or "tidy" code you are not asked to change.

---

## Scope guard: do NOT build the other four tabs

The spec carves Invites, Agent enrollments, Reservations, and Server into their own backlog items. This plan registers **exactly one** tab. If you find yourself writing an `InvitesTab`, a placeholder panel, a "coming soon" card, or a fifth entry in `ADMIN_TABS`, stop - that is out of scope and ships four dead tabs. The registry-only shape is the guard: adding a tab later is one entry in `web/src/admin/tabs.ts`.

Also omitted on purpose (unbacked data - do not fake): the `SESSIONS` column, the `LAST LOGIN` column, the `service` role value, any role-change control, and the header `VERSION` / `BUILD` / `DB` / `UPTIME` strip. There is no endpoint behind any of them.

---

## File Structure

**New files**

| File | Responsibility |
|---|---|
| `web/src/lib/useDebouncedValue.ts` | Generic debounce hook (lives in `lib/` because it is not admin-specific). |
| `web/src/lib/useDebouncedValue.test.ts` | Fake-timer unit tests. |
| `web/src/admin/users/api.ts` | `AdminUser` / `AdminUsersPage` / `UserSort` types + the six typed clients. |
| `web/src/admin/users/api.test.ts` | Method + path + body + query-param assertions. |
| `web/src/admin/users/useAdminUsers.ts` | The list query. No `refetchInterval`. |
| `web/src/admin/users/useAdminUsers.test.tsx` | Query key, request params, no-polling assertion. |
| `web/src/admin/users/useAdminUserActions.ts` | Five mutations, each invalidating bare `['users']`. |
| `web/src/admin/users/useAdminUserActions.test.tsx` | Exact call shapes + the non-vacuous invalidation test. |
| `web/src/admin/users/UsersTable.tsx` | Presentational table + per-row actions + inline rename. |
| `web/src/admin/users/UsersTable.test.tsx` | Columns, role pill, archived rows, own-row guard, sort headers, rename. |
| `web/src/admin/users/CreateUserForm.tsx` | Inline create panel. |
| `web/src/admin/users/CreateUserForm.test.tsx` | Client validation, payload, 409 mapping. |
| `web/src/admin/users/ResetPasswordDialog.tsx` | Form dialog with the session-revocation warning. |
| `web/src/admin/users/ResetPasswordDialog.test.tsx` | Validation, warning copy, a11y baseline. |
| `web/src/admin/users/UsersTab.tsx` | Composition: control row, states, table, footer, dialogs. |
| `web/src/admin/users/UsersTab.test.tsx` | Filter / toggle / sort / pagination / states / action wiring. |
| `web/src/admin/tabs.ts` | The `ADMIN_TABS` registry (one entry). |
| `web/src/admin/AdminTabs.tsx` | Pill-group tab bar built from the registry. |
| `web/src/admin/AdminTabs.test.tsx` | One tab rendered, active state, unbuilt tabs absent. |
| `web/src/admin/AdminPage.tsx` | Shell: header + tab bar + panel switch + unknown-tab redirect. |
| `web/src/admin/AdminPage.test.tsx` | Header, panel mount, unknown-tab redirect. |
| `web/src/app/AdminRoute.tsx` | `is_admin` route guard (UX only). |
| `web/src/app/AdminRoute.test.tsx` | Gate behaviour + real-router wiring through `AppRoutes`. |
| `web/src/shell/HoloShell.test.tsx` | Admin nav entry shown to admins, hidden from non-admins. |

**Modified files**

| File | Change |
|---|---|
| `web/src/app/router.tsx:28` | Replace the `/admin` -> `JobsPlaceholder` stub with the `AdminRoute`-nested `/admin` + `/admin/:tab` pair. |
| `web/src/shell/HoloShell.tsx:7-12,29` | Mark the Admin nav entry `adminOnly` and filter `NAV` on `user?.is_admin`. |

**Reused, not rebuilt** (read these before writing anything):

- `web/src/components/holo/GlassPanel.tsx`, `Eyebrow.tsx`, `PillButton.tsx` (variants `primary`/`ghost`/`muted`/`danger`), `Chip.tsx` (tones `accent`/`muted`/`warn`), barrel at `web/src/components/holo/index.ts`.
- `web/src/components/ConfirmDialog.tsx` - text-only `body`; that is why password reset needs its own dialog.
- `web/src/components/Field.tsx`, `web/src/components/Input.tsx`, `web/src/components/Button.tsx`.
- `web/src/lib/api.ts:28` `apiFetch` (single fetch entry point; already returns `undefined` for 204 at line 56) and `ApiError` at line 3.
- `web/src/lib/pageRange.ts:6` `computePageRange`.
- Patterns to copy: cursor/stack/offsets pagination `web/src/jobs/JobsPage.tsx:28-83`; footer markup `web/src/jobs/JobsPage.tsx:176-200`; `toggleSort` `web/src/workers/WorkersPage.tsx:22-27`; sortable header + `aria-sort` + role-based table markup `web/src/workers/WorkersTable.tsx:6-82`; mutation hook `web/src/workers/useWorkerActions.ts`; action bar with `busy` + shared inline error + confirm dialog `web/src/workers/WorkerActions.tsx:19-116`; inline form `web/src/workers/WorkerEditForm.tsx`; loading/error/empty triad `web/src/workers/WorkersPage.tsx:156-193`; route guard `web/src/app/ProtectedRoute.tsx`; guard test harness `web/src/app/ProtectedRoute.test.tsx:13-28`.

**Backend contract** (read-only reference, do not edit): `internal/api/users.go:22-29` (`userResponse`), `:49-51` (`updateUserRequest`, name-only), `:111-132` (the `archived_at`-is-always-null-in-active-mode behaviour), `:569-575` (`createUserRequest`), `internal/api/auth.go:359-420` (`handleAdminPasswordReset`, `{email, new_password}`, 204, deletes all of the target's tokens).

---

## Task 1: Users API clients and types

**Files:**
- Create: `web/src/admin/users/api.ts`
- Test: `web/src/admin/users/api.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/users/api.test.ts`:

```ts
import { expect, test } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/setup-helpers'
import {
  archiveUser,
  createUser,
  listUsers,
  renameUser,
  resetUserPassword,
  unarchiveUser,
} from './api'

const USER = {
  id: 'u1',
  email: 'a@b.co',
  name: 'A',
  is_admin: false,
  created_at: '2026-08-01T00:00:00Z',
  archived_at: null,
}

test('listUsers sends sort and limit=50 and returns the envelope', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/users', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [USER], next_cursor: 'c2', total: 7 })
    }),
  )
  const page = await listUsers({ sort: '-created_at', includeArchived: false, cursor: '', email: '' })
  expect(params?.get('sort')).toBe('-created_at')
  expect(params?.get('limit')).toBe('50')
  expect(params?.has('include_archived')).toBe(false)
  expect(params?.has('cursor')).toBe(false)
  expect(params?.has('email')).toBe(false)
  expect(page.items[0].email).toBe('a@b.co')
  expect(page.next_cursor).toBe('c2')
  expect(page.total).toBe(7)
})

test('listUsers sends include_archived, cursor, and email when provided', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/users', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  await listUsers({ sort: 'email', includeArchived: true, cursor: 'cur1', email: 'a@b.co' })
  expect(params?.get('include_archived')).toBe('true')
  expect(params?.get('cursor')).toBe('cur1')
  expect(params?.get('email')).toBe('a@b.co')
  expect(params?.get('sort')).toBe('email')
})

test('createUser POSTs /users with the full body and reads the 201', async () => {
  let body: unknown
  server.use(
    http.post('/v1/users', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...USER, is_admin: true }, { status: 201 })
    }),
  )
  const created = await createUser({ email: 'new@b.co', name: 'New', password: 'password1', is_admin: true })
  expect(body).toEqual({ email: 'new@b.co', name: 'New', password: 'password1', is_admin: true })
  expect(created.is_admin).toBe(true)
})

test('renameUser PATCHes /users/{id} with only a name', async () => {
  let body: unknown
  server.use(
    http.patch('/v1/users/u1', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...USER, name: 'Renamed' })
    }),
  )
  const updated = await renameUser('u1', 'Renamed')
  expect(body).toEqual({ name: 'Renamed' })
  expect(updated.name).toBe('Renamed')
})

test('archiveUser POSTs /users/{id}/archive and returns the archived row', async () => {
  server.use(
    http.post('/v1/users/u1/archive', () =>
      HttpResponse.json({ ...USER, archived_at: '2026-08-02T00:00:00Z' }),
    ),
  )
  const archived = await archiveUser('u1')
  expect(archived.archived_at).toBe('2026-08-02T00:00:00Z')
})

test('unarchiveUser POSTs /users/{id}/unarchive', async () => {
  let hit = false
  server.use(
    http.post('/v1/users/u1/unarchive', () => {
      hit = true
      return HttpResponse.json(USER)
    }),
  )
  const restored = await unarchiveUser('u1')
  expect(hit).toBe(true)
  expect(restored.archived_at).toBeNull()
})

test('resetUserPassword POSTs email + new_password and tolerates a 204 with no body', async () => {
  let body: unknown
  server.use(
    http.post('/v1/users/password-reset', async ({ request }) => {
      body = await request.json()
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await expect(resetUserPassword('a@b.co', 'password1')).resolves.toBeUndefined()
  expect(body).toEqual({ email: 'a@b.co', new_password: 'password1' })
})

test('a 409 from createUser surfaces as an ApiError with status 409', async () => {
  server.use(
    http.post('/v1/users', () =>
      HttpResponse.json({ error: 'email already registered' }, { status: 409 }),
    ),
  )
  await expect(
    createUser({ email: 'dupe@b.co', name: '', password: 'password1', is_admin: false }),
  ).rejects.toMatchObject({ status: 409, code: 'email already registered' })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/users/api.test.ts`
Expected: FAIL - `Failed to resolve import "./api"` (the module does not exist).

- [ ] **Step 3: Implement the clients and types**

Create `web/src/admin/users/api.ts`:

```ts
import { apiFetch } from '../../lib/api'

// Mirrors internal/api/users.go:22-29 (userResponse). archived_at is nullable AND
// is only meaningful when include_archived=true: usersListRowToResponse passes a
// zero timestamp for the active-only query family (internal/api/users.go:111-132),
// so never infer "archived" from archived_at unless the toggle is on.
export interface AdminUser {
  id: string
  email: string
  name: string
  is_admin: boolean
  created_at: string
  archived_at: string | null
}

export interface AdminUsersPage {
  items: AdminUser[]
  next_cursor: string
  total: number
}

// The three sortable keys accepted by UsersSortSpec (internal/api/users.go:69-76).
export type UserSortField = 'created_at' | 'name' | 'email'

export type UserSort =
  | '-created_at'
  | 'created_at'
  | 'name'
  | '-name'
  | 'email'
  | '-email'

export interface ListUsersParams {
  sort: UserSort
  includeArchived: boolean
  cursor: string
  email: string
}

// limit=50 is the server default, passed explicitly so the client's page size is
// self-documenting (same as listWorkers). The server short-circuits the ?email=
// branch before pagination, returning the same envelope with 0 or 1 items.
export function listUsers({ sort, includeArchived, cursor, email }: ListUsersParams): Promise<AdminUsersPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  if (includeArchived) q.set('include_archived', 'true')
  if (cursor) q.set('cursor', cursor)
  if (email) q.set('email', email)
  return apiFetch<AdminUsersPage>(`/users?${q}`)
}

// Mirrors createUserRequest (internal/api/users.go:569-575). This is the ONLY
// place is_admin can be set; no endpoint mutates it afterwards. A blank name
// defaults to the email server-side. 409 on a duplicate email.
export interface CreateUserBody {
  email: string
  name: string
  password: string
  is_admin: boolean
}

export function createUser(body: CreateUserBody): Promise<AdminUser> {
  return apiFetch<AdminUser>('/users', { method: 'POST', json: body })
}

// {id} is the user UUID, not the email. The body accepts ONLY name
// (updateUserRequest, internal/api/users.go:49-51).
export function renameUser(id: string, name: string): Promise<AdminUser> {
  return apiFetch<AdminUser>(`/users/${id}`, { method: 'PATCH', json: { name } })
}

// Transactional server-side: archives, deletes the target's API tokens, disables
// their scheduled jobs. 400 on self or last-active-admin, 409 if already archived.
export function archiveUser(id: string): Promise<AdminUser> {
  return apiFetch<AdminUser>(`/users/${id}/archive`, { method: 'POST' })
}

export function unarchiveUser(id: string): Promise<AdminUser> {
  return apiFetch<AdminUser>(`/users/${id}/unarchive`, { method: 'POST' })
}

// Keyed by email in the BODY, not by a path id. Returns 204 (no body) and deletes
// every one of the target's tokens - including yours if you target yourself.
export function resetUserPassword(email: string, newPassword: string): Promise<void> {
  return apiFetch<void>('/users/password-reset', {
    method: 'POST',
    json: { email, new_password: newPassword },
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/users/api.test.ts`
Expected: PASS (8 tests).

- [ ] **Step 5: Verify the TS types match the Go structs (contract check)**

Open each Go site and confirm the JSON tags field-for-field. No code change expected; fix the TS if a mismatch is found.

- `AdminUser` vs `userResponse` (`internal/api/users.go:22-29`): `id, email, name, is_admin, created_at, archived_at` - `archived_at` is `*time.Time`, hence `string | null`.
- `AdminUsersPage` vs `page[userResponse]` (see `writeJSON` calls at `internal/api/users.go:169-173`): `items, next_cursor, total`.
- `CreateUserBody` vs `createUserRequest` (`internal/api/users.go:569-575`): `email, name, password, is_admin`.
- Rename body vs `updateUserRequest` (`internal/api/users.go:49-51`): `name` only - there is no `is_admin` field, so no role change is possible.
- Reset body vs the anonymous struct in `handleAdminPasswordReset` (`internal/api/auth.go:359-420`): `email`, `new_password`.

- [ ] **Step 6: Commit**

```bash
git add web/src/admin/users/api.ts web/src/admin/users/api.test.ts
git commit -m "feat(web): admin users API clients and types"
```

---

## Task 2: useDebouncedValue hook

**Files:**
- Create: `web/src/lib/useDebouncedValue.ts`
- Test: `web/src/lib/useDebouncedValue.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/lib/useDebouncedValue.test.ts`:

```ts
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { useDebouncedValue } from './useDebouncedValue'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

test('returns the initial value immediately', () => {
  const { result } = renderHook(() => useDebouncedValue('a', 300))
  expect(result.current).toBe('a')
})

test('holds the old value until the delay elapses', () => {
  const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, 300), {
    initialProps: { v: 'a' },
  })
  rerender({ v: 'ab' })
  act(() => {
    vi.advanceTimersByTime(299)
  })
  expect(result.current).toBe('a')
  act(() => {
    vi.advanceTimersByTime(1)
  })
  expect(result.current).toBe('ab')
})

test('a burst of changes emits only the last value', () => {
  const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, 300), {
    initialProps: { v: '' },
  })
  rerender({ v: 'a' })
  act(() => {
    vi.advanceTimersByTime(100)
  })
  rerender({ v: 'ab' })
  act(() => {
    vi.advanceTimersByTime(100)
  })
  rerender({ v: 'abc' })
  // Only 200ms of the 300ms window has elapsed since 'a', and the timer restarted
  // twice, so nothing has been emitted yet.
  expect(result.current).toBe('')
  act(() => {
    vi.advanceTimersByTime(300)
  })
  expect(result.current).toBe('abc')
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/lib/useDebouncedValue.test.ts`
Expected: FAIL - `Failed to resolve import "./useDebouncedValue"`.

- [ ] **Step 3: Implement the hook**

Create `web/src/lib/useDebouncedValue.ts`:

```ts
import { useEffect, useState } from 'react'

// Returns `value` delayed by delayMs, restarting the timer on every change, so a
// burst of keystrokes produces exactly one downstream update. Used by the admin
// Users tab's exact-email filter: the query key only changes on the debounced
// value, so typing does not fan out one request per keystroke.
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(t)
  }, [value, delayMs])
  return debounced
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/lib/useDebouncedValue.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/useDebouncedValue.ts web/src/lib/useDebouncedValue.test.ts
git commit -m "feat(web): useDebouncedValue hook"
```

---

## Task 3: useAdminUsers list query

**Files:**
- Create: `web/src/admin/users/useAdminUsers.ts`
- Test: `web/src/admin/users/useAdminUsers.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/users/useAdminUsers.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useAdminUsers } from './useAdminUsers'
import type { AdminUsersPage } from './api'

const USER = {
  id: 'u1',
  email: 'a@b.co',
  name: 'A',
  is_admin: false,
  created_at: '2026-08-01T00:00:00Z',
  archived_at: null,
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('caches under ["users", sort, includeArchived, cursor, email] and passes the params through', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/users', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [USER], next_cursor: '', total: 1 })
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useAdminUsers('name', true, 'cur1', 'a@b.co'), {
    wrapper: makeWrapper(client),
  })

  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(params?.get('sort')).toBe('name')
  expect(params?.get('include_archived')).toBe('true')
  expect(params?.get('cursor')).toBe('cur1')
  expect(params?.get('email')).toBe('a@b.co')

  const cached = client.getQueryData<AdminUsersPage>(['users', 'name', true, 'cur1', 'a@b.co'])
  expect(cached?.items[0].id).toBe('u1')
})

test('does not poll - the users table is not live data', async () => {
  let calls = 0
  server.use(
    http.get('/v1/users', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { result } = renderHook(() => useAdminUsers('-created_at', false, '', ''), {
    wrapper: makeWrapper(newClient()),
  })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)
  // Long enough that any accidental refetchInterval (the shipped list hooks use
  // 3000ms, and a copy-paste of useWorkers would inherit it) would have fired.
  await new Promise((r) => setTimeout(r, 120))
  expect(calls).toBe(1)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/users/useAdminUsers.test.tsx`
Expected: FAIL - `Failed to resolve import "./useAdminUsers"`.

- [ ] **Step 3: Implement the hook**

Create `web/src/admin/users/useAdminUsers.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listUsers, type AdminUsersPage, type UserSort } from './api'

// The list query for the admin Users tab. Deliberately NO refetchInterval: unlike
// workers/jobs this is not live data, so polling it every 3s is pointless load.
// Refresh comes from useAdminUserActions invalidating the bare ['users'] prefix.
// keepPreviousData keeps rows visible while a new sort / page / filter loads, which
// is also what makes isPlaceholderData usable to disable the pager mid-fetch.
export function useAdminUsers(
  sort: UserSort,
  includeArchived: boolean,
  cursor: string,
  email: string,
) {
  return useQuery<AdminUsersPage>({
    queryKey: ['users', sort, includeArchived, cursor, email],
    queryFn: () => listUsers({ sort, includeArchived, cursor, email }),
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/users/useAdminUsers.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/users/useAdminUsers.ts web/src/admin/users/useAdminUsers.test.tsx
git commit -m "feat(web): useAdminUsers list query hook"
```

---

## Task 4: useAdminUserActions mutations

**Files:**
- Create: `web/src/admin/users/useAdminUserActions.ts`
- Test: `web/src/admin/users/useAdminUserActions.test.tsx`

This is the task where a green test is most likely to be vacuous, so Step 5 is a mandatory non-vacuity check.

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/users/useAdminUserActions.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useAdminUserActions } from './useAdminUserActions'
import { useAdminUsers } from './useAdminUsers'

const ID = 'u1'

const USER = {
  id: ID,
  email: 'a@b.co',
  name: 'A',
  is_admin: false,
  created_at: '2026-08-01T00:00:00Z',
  archived_at: null,
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('create POSTs /users with the body and invalidates the bare ["users"] prefix', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.post('/v1/users', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json(USER, { status: 201 })
    }),
  )
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await result.current.create.mutateAsync({
    email: 'a@b.co',
    name: 'A',
    password: 'password1',
    is_admin: false,
  })

  expect(body).toEqual({ email: 'a@b.co', name: 'A', password: 'password1', is_admin: false })
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
})

test('rename PATCHes /users/{id} with only a name and invalidates ["users"]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.patch(`/v1/users/${ID}`, async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...USER, name: 'Renamed' })
    }),
  )
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await result.current.rename.mutateAsync({ id: ID, name: 'Renamed' })

  expect(body).toEqual({ name: 'Renamed' })
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
})

test('archive POSTs /users/{id}/archive and invalidates ["users"]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post(`/v1/users/${ID}/archive`, () =>
      HttpResponse.json({ ...USER, archived_at: '2026-08-02T00:00:00Z' }),
    ),
  )
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await result.current.archive.mutateAsync(ID)

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
})

test('unarchive POSTs /users/{id}/unarchive and invalidates ["users"]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(http.post(`/v1/users/${ID}/unarchive`, () => HttpResponse.json(USER)))
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await result.current.unarchive.mutateAsync(ID)

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
})

test('resetPassword POSTs email + new_password, resolves on a 204 with no body, and invalidates ["users"]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.post('/v1/users/password-reset', async ({ request }) => {
      body = await request.json()
      return new HttpResponse(null, { status: 204 })
    }),
  )
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await expect(
    result.current.resetPassword.mutateAsync({ email: 'a@b.co', newPassword: 'password1' }),
  ).resolves.toBeUndefined()

  expect(body).toEqual({ email: 'a@b.co', new_password: 'password1' })
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
})

test('no mutation invalidates a fully-qualified list key', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(http.post(`/v1/users/${ID}/archive`, () => HttpResponse.json(USER)))
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await result.current.archive.mutateAsync(ID)

  // The decoupling lesson from web/src/jobs/queryKeyDecoupling.test.tsx: a
  // fully-qualified key only refetches the page/sort/filter combination that
  // happens to be mounted. Every call must use the bare prefix.
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
  for (const call of spy.mock.calls) {
    const key = (call[0] as { queryKey: unknown[] }).queryKey
    expect(key).toEqual(['users'])
  }
})

test('archiving refetches a MOUNTED users list (active observer, not a cache seed)', async () => {
  let listCalls = 0
  server.use(
    http.get('/v1/users', () => {
      listCalls++
      return HttpResponse.json({ items: [USER], next_cursor: '', total: 1 })
    }),
    http.post(`/v1/users/${ID}/archive`, () =>
      HttpResponse.json({ ...USER, archived_at: '2026-08-02T00:00:00Z' }),
    ),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)

  // The list query MUST be mounted via renderHook so it has an active observer.
  // A client.fetchQuery / setQueryData seed leaves no observer, invalidateQueries'
  // default refetchType:'active' never fires, and this assertion would pass
  // vacuously no matter what key the mutation invalidates.
  const { result: list } = renderHook(() => useAdminUsers('-created_at', false, '', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useAdminUserActions(), { wrapper })
  await actions.current.archive.mutateAsync(ID)

  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/users/useAdminUserActions.test.tsx`
Expected: FAIL - `Failed to resolve import "./useAdminUserActions"`.

- [ ] **Step 3: Implement the hook**

Create `web/src/admin/users/useAdminUserActions.ts`:

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  archiveUser,
  createUser,
  renameUser,
  resetUserPassword,
  unarchiveUser,
  type CreateUserBody,
} from './api'

// Mutations for the admin Users tab. Direct port of useWorkerActions' shape with
// two deliberate differences:
//  - Every mutation invalidates the BARE ['users'] prefix, never a fully-qualified
//    key, so any mounted ['users', sort, includeArchived, cursor, email]
//    combination refetches (see web/src/jobs/queryKeyDecoupling.test.tsx).
//  - No optimistic updates. useWorkerActions' optimistic disable/enable exists
//    because a 3s poll made the pill lag; useAdminUsers does not poll and every
//    call here returns the updated row (or 204), so invalidate-on-success is both
//    simplest and correct.
export function useAdminUserActions() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: ['users'] })

  const create = useMutation({
    mutationFn: (body: CreateUserBody) => createUser(body),
    onSuccess: invalidate,
  })

  const rename = useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) => renameUser(id, name),
    onSuccess: invalidate,
  })

  const archive = useMutation({
    mutationFn: (id: string) => archiveUser(id),
    onSuccess: invalidate,
  })

  const unarchive = useMutation({
    mutationFn: (id: string) => unarchiveUser(id),
    onSuccess: invalidate,
  })

  const resetPassword = useMutation({
    mutationFn: ({ email, newPassword }: { email: string; newPassword: string }) =>
      resetUserPassword(email, newPassword),
    onSuccess: invalidate,
  })

  return { create, rename, archive, unarchive, resetPassword }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/users/useAdminUserActions.test.tsx`
Expected: PASS (7 tests).

- [ ] **Step 5: Prove the invalidation test is not vacuous**

Temporarily break the invalidation key in `web/src/admin/users/useAdminUserActions.ts`:

```ts
  const invalidate = () => qc.invalidateQueries({ queryKey: ['users-broken'] })
```

Run: `npx vitest run src/admin/users/useAdminUserActions.test.tsx`
Expected: FAIL - the "archiving refetches a MOUNTED users list" test fails on `listCalls` still being 1, and the five `toHaveBeenCalledWith({ queryKey: ['users'] })` assertions fail. If that test still passes, the observer is not active and the test is worthless - fix the test before continuing.

Then revert the line back to `['users']` and re-run:

Run: `npx vitest run src/admin/users/useAdminUserActions.test.tsx`
Expected: PASS (7 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/admin/users/useAdminUserActions.ts web/src/admin/users/useAdminUserActions.test.tsx
git commit -m "feat(web): useAdminUserActions mutations with ['users'] invalidation"
```

---

## Task 5: UsersTable

Presentational only - no queries, no mutations. All behaviour arrives as props, which is what makes it testable with plain `render` and `vi.fn()`.

**Files:**
- Create: `web/src/admin/users/UsersTable.tsx`
- Test: `web/src/admin/users/UsersTable.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/users/UsersTable.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { UsersTable } from './UsersTable'
import type { AdminUser, UserSort } from './api'

function user(over: Partial<AdminUser> = {}): AdminUser {
  return {
    id: 'u1',
    email: 'ada@studio.dev',
    name: 'Ada',
    is_admin: false,
    created_at: '2026-08-01T12:00:00Z',
    archived_at: null,
    ...over,
  }
}

function renderTable(over: Partial<Parameters<typeof UsersTable>[0]> = {}) {
  const props = {
    users: [user()],
    sort: '-created_at' as UserSort,
    onSort: vi.fn(),
    showArchived: false,
    currentUserId: 'me',
    busy: false,
    onRename: vi.fn(),
    onResetPassword: vi.fn(),
    onArchive: vi.fn(),
    onUnarchive: vi.fn(),
    ...over,
  }
  return { props, ...render(<UsersTable {...props} />) }
}

test('renders email, name, created date, and the avatar initial', () => {
  renderTable()
  expect(screen.getByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('Ada')).toBeInTheDocument()
  expect(screen.getByText('2026-08-01')).toBeInTheDocument()
  expect(screen.getByText('A')).toBeInTheDocument()
})

test('the role pill reads ADMIN or USER and nothing else', () => {
  renderTable({ users: [user({ is_admin: true }), user({ id: 'u2', email: 'b@c.dev', is_admin: false })] })
  expect(screen.getByText('ADMIN')).toBeInTheDocument()
  expect(screen.getByText('USER')).toBeInTheDocument()
  expect(screen.queryByText(/service/i)).not.toBeInTheDocument()
})

test('renders no SESSIONS or LAST LOGIN column (no backend for either)', () => {
  renderTable()
  expect(screen.queryByText('SESSIONS')).not.toBeInTheDocument()
  expect(screen.queryByText('LAST LOGIN')).not.toBeInTheDocument()
  expect(screen.queryByText(/active$/)).not.toBeInTheDocument()
})

test('active rows offer Reset pw, Rename, and Archive', () => {
  renderTable()
  expect(screen.getByRole('button', { name: 'Reset pw' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Rename' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Archive' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Unarchive' })).not.toBeInTheDocument()
})

test('the Archive button is absent on the acting admin own row', () => {
  renderTable({ currentUserId: 'u1' })
  expect(screen.queryByRole('button', { name: 'Archive' })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Rename' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Reset pw' })).toBeInTheDocument()
})

test('with showArchived on, an archived row is dimmed and offers only Unarchive', () => {
  const { container } = renderTable({
    showArchived: true,
    users: [user({ archived_at: '2026-08-02T00:00:00Z' })],
  })
  expect(screen.getByRole('button', { name: 'Unarchive' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Archive' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Rename' })).not.toBeInTheDocument()
  expect(container.querySelector('.opacity-\\[0\\.55\\]')).not.toBeNull()
})

test('with showArchived off, a stray archived_at does not archive the row', () => {
  // internal/api/users.go:111-132 sends a null archived_at in active-only mode, so
  // "archived" must be gated on the toggle, never inferred from the field alone.
  renderTable({ showArchived: false, users: [user({ archived_at: '2026-08-02T00:00:00Z' })] })
  expect(screen.getByRole('button', { name: 'Archive' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Unarchive' })).not.toBeInTheDocument()
})

test('clicking a sortable header calls onSort with that field', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: /^EMAIL/ }))
  expect(props.onSort).toHaveBeenCalledWith('email')
  await userEvent.click(screen.getByRole('button', { name: /^NAME/ }))
  expect(props.onSort).toHaveBeenCalledWith('name')
  await userEvent.click(screen.getByRole('button', { name: /^CREATED/ }))
  expect(props.onSort).toHaveBeenCalledWith('created_at')
})

test('exposes aria-sort on the active column and "none" on the others', () => {
  renderTable({ sort: 'email' })
  expect(screen.getByRole('columnheader', { name: /EMAIL/ })).toHaveAttribute('aria-sort', 'ascending')
  expect(screen.getByRole('columnheader', { name: /NAME/ })).toHaveAttribute('aria-sort', 'none')
})

test('descending sort shows a descending caret', () => {
  renderTable({ sort: '-name' })
  expect(screen.getByRole('button', { name: 'NAME ▼' })).toBeInTheDocument()
})

test('Rename turns the name cell into an input and submits the trimmed value', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: 'Rename' }))
  const input = screen.getByLabelText('Name for ada@studio.dev')
  await userEvent.clear(input)
  await userEvent.type(input, '  Ada L  ')
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(props.onRename).toHaveBeenCalledWith('u1', 'Ada L')
  expect(screen.queryByLabelText('Name for ada@studio.dev')).not.toBeInTheDocument()
})

test('Cancel leaves the rename without calling onRename', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: 'Rename' }))
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(props.onRename).not.toHaveBeenCalled()
  expect(screen.getByText('Ada')).toBeInTheDocument()
})

test('an empty rename is not submitted', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: 'Rename' }))
  await userEvent.clear(screen.getByLabelText('Name for ada@studio.dev'))
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(props.onRename).not.toHaveBeenCalled()
})

test('row actions fire their callbacks with the row user', async () => {
  const { props } = renderTable()
  await userEvent.click(screen.getByRole('button', { name: 'Reset pw' }))
  expect(props.onResetPassword).toHaveBeenCalledWith(props.users[0])
  await userEvent.click(screen.getByRole('button', { name: 'Archive' }))
  expect(props.onArchive).toHaveBeenCalledWith(props.users[0])
})

test('busy disables every row action', () => {
  renderTable({ busy: true })
  expect(screen.getByRole('button', { name: 'Reset pw' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Rename' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Archive' })).toBeDisabled()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/users/UsersTable.test.tsx`
Expected: FAIL - `Failed to resolve import "./UsersTable"`.

- [ ] **Step 3: Implement UsersTable**

Create `web/src/admin/users/UsersTable.tsx`:

```tsx
import { useState } from 'react'
import { Chip } from '../../components/holo'
import { Input } from '../../components/Input'
import type { AdminUser, UserSort, UserSortField } from './api'

// EMAIL | NAME | ROLE | CREATED | ACTIONS. The hi-fi's SESSIONS and LAST LOGIN
// columns are omitted: no endpoint exposes a per-user token count and `users` has
// no last_login_at column. Faking either would read as real data.
const COLS = 'grid grid-cols-[1.6fr_1fr_110px_120px_270px]'

// Row mini-actions use literal classes rather than PillButton overrides: two
// competing padding utilities on one element resolve by stylesheet order, not by
// class-attribute order, so an override is not reliable at this size.
const MINI = 'rounded-full border px-2.5 py-1 font-mono text-[10.5px] tracking-[0.04em] disabled:opacity-40'
const MINI_GHOST = `${MINI} border-border bg-white/5 text-fg-mute`
const MINI_ACCENT = `${MINI} border-accent/50 bg-accent/10 text-accent`
const MINI_DANGER = `${MINI} border-err/40 bg-err/10 text-err`

function caret(field: UserSortField, sort: UserSort): string {
  if (sort.replace('-', '') !== field) return ''
  return sort.startsWith('-') ? ' ▼' : ' ▲'
}

function ariaSort(field: UserSortField, sort: UserSort): 'ascending' | 'descending' | 'none' {
  if (sort.replace('-', '') !== field) return 'none'
  return sort.startsWith('-') ? 'descending' : 'ascending'
}

interface UsersTableProps {
  users: AdminUser[]
  sort: UserSort
  onSort: (field: UserSortField) => void
  // Only when the include-archived toggle is ON is archived_at meaningful: the
  // active-only query family sends a zero timestamp (internal/api/users.go:111-132).
  showArchived: boolean
  currentUserId: string
  busy: boolean
  onRename: (id: string, name: string) => void
  onResetPassword: (user: AdminUser) => void
  onArchive: (user: AdminUser) => void
  onUnarchive: (user: AdminUser) => void
}

export function UsersTable({
  users,
  sort,
  onSort,
  showArchived,
  currentUserId,
  busy,
  onRename,
  onResetPassword,
  onArchive,
  onUnarchive,
}: UsersTableProps) {
  const [editingId, setEditingId] = useState<string | null>(null)
  const [draft, setDraft] = useState('')

  function startRename(u: AdminUser) {
    setEditingId(u.id)
    setDraft(u.name)
  }

  function submitRename(id: string) {
    const name = draft.trim()
    if (!name) return
    onRename(id, name)
    setEditingId(null)
  }

  return (
    <div
      role="table"
      aria-label="Users"
      className="rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px]"
    >
      <div
        role="row"
        className={`${COLS} border-b border-border px-[18px] py-3 font-mono text-[10px] tracking-[0.16em] text-fg-mute`}
      >
        <div role="columnheader" aria-sort={ariaSort('email', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('email')}>
            EMAIL{caret('email', sort)}
          </button>
        </div>
        <div role="columnheader" aria-sort={ariaSort('name', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('name')}>
            NAME{caret('name', sort)}
          </button>
        </div>
        <span role="columnheader">ROLE</span>
        <div role="columnheader" aria-sort={ariaSort('created_at', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('created_at')}>
            CREATED{caret('created_at', sort)}
          </button>
        </div>
        <span role="columnheader" className="text-right">
          ACTIONS
        </span>
      </div>

      {users.map((u) => {
        const archived = showArchived && Boolean(u.archived_at)
        const isSelf = u.id === currentUserId
        return (
          <div
            key={u.id}
            role="row"
            className={`${COLS} items-center border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
              archived ? 'opacity-[0.55]' : ''
            }`}
          >
            <span role="cell" className="flex min-w-0 items-center gap-2.5">
              <span className="grid h-6 w-6 flex-none place-items-center rounded-md bg-gradient-to-br from-accent/45 to-accent-b/30 text-[11px] font-semibold text-white">
                {u.email.charAt(0).toUpperCase()}
              </span>
              <span className="truncate font-sans text-[12.5px] text-fg">{u.email}</span>
            </span>

            <span role="cell" className="min-w-0 pr-2">
              {editingId === u.id ? (
                <span className="flex items-center gap-1.5">
                  <Input
                    aria-label={`Name for ${u.email}`}
                    value={draft}
                    onChange={(e) => setDraft(e.target.value)}
                    className="py-1 text-[12px]"
                  />
                  <button type="button" className={MINI_ACCENT} onClick={() => submitRename(u.id)}>
                    Save
                  </button>
                  <button type="button" className={MINI_GHOST} onClick={() => setEditingId(null)}>
                    Cancel
                  </button>
                </span>
              ) : (
                <span className="truncate font-sans text-[12px] text-fg-mute">{u.name}</span>
              )}
            </span>

            <span role="cell">
              {/* Two values only. Relay's model is a single is_admin boolean; the
                  hi-fi's `service` role is mock fiction. */}
              <Chip tone={u.is_admin ? 'accent' : 'muted'}>{u.is_admin ? 'ADMIN' : 'USER'}</Chip>
            </span>

            <span role="cell" className="text-[10.5px] text-fg-mute">
              {u.created_at.slice(0, 10)}
            </span>

            <span role="cell" className="flex justify-end gap-1.5">
              {archived ? (
                <button
                  type="button"
                  className={MINI_ACCENT}
                  disabled={busy}
                  onClick={() => onUnarchive(u)}
                >
                  Unarchive
                </button>
              ) : (
                <>
                  <button
                    type="button"
                    className={MINI_GHOST}
                    disabled={busy}
                    onClick={() => onResetPassword(u)}
                  >
                    Reset pw
                  </button>
                  <button
                    type="button"
                    className={MINI_GHOST}
                    disabled={busy}
                    onClick={() => startRename(u)}
                  >
                    Rename
                  </button>
                  {/* No Archive on your own row: the server 400s "cannot archive
                      yourself", so the button would be a guaranteed-failing control. */}
                  {!isSelf && (
                    <button
                      type="button"
                      className={MINI_DANGER}
                      disabled={busy}
                      onClick={() => onArchive(u)}
                    >
                      Archive
                    </button>
                  )}
                </>
              )}
            </span>
          </div>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/users/UsersTable.test.tsx`
Expected: PASS (15 tests).

If the `getByText('A')` avatar assertion is ambiguous because a name also renders as `A`, keep the fixture name `Ada` as written - the fixture is chosen so the initial is unique.

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/users/UsersTable.tsx web/src/admin/users/UsersTable.test.tsx
git commit -m "feat(web): admin UsersTable with sort, role pill, and row actions"
```

---

## Task 6: CreateUserForm

**Files:**
- Create: `web/src/admin/users/CreateUserForm.tsx`
- Test: `web/src/admin/users/CreateUserForm.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/users/CreateUserForm.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ApiError } from '../../lib/api'
import { CreateUserForm } from './CreateUserForm'

function renderForm(over: Partial<Parameters<typeof CreateUserForm>[0]> = {}) {
  const props = {
    pending: false,
    error: null as Error | null,
    onSubmit: vi.fn(),
    onCancel: vi.fn(),
    ...over,
  }
  return { props, ...render(<CreateUserForm {...props} />) }
}

test('submits email, trimmed name, password, and is_admin', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Email'), 'new@studio.dev')
  await userEvent.type(screen.getByLabelText('Name'), '  New Person  ')
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByLabelText(/^Admin/))
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))
  expect(props.onSubmit).toHaveBeenCalledWith({
    email: 'new@studio.dev',
    name: 'New Person',
    password: 'password1',
    is_admin: true,
  })
})

test('is_admin defaults to false', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Email'), 'new@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))
  expect(props.onSubmit).toHaveBeenCalledWith({
    email: 'new@studio.dev',
    name: '',
    password: 'password1',
    is_admin: false,
  })
})

test('rejects a blank email without submitting', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))
  expect(screen.getByText('Email is required.')).toBeInTheDocument()
  expect(props.onSubmit).not.toHaveBeenCalled()
})

test('rejects a password under 8 characters without submitting', async () => {
  const { props } = renderForm()
  await userEvent.type(screen.getByLabelText('Email'), 'new@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'short')
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))
  expect(screen.getByText('Password must be at least 8 characters.')).toBeInTheDocument()
  expect(props.onSubmit).not.toHaveBeenCalled()
})

test('a 409 renders as a duplicate-email field error and preserves form state', async () => {
  renderForm({ error: new ApiError(409, 'email already registered', '409 email already registered') })
  await userEvent.type(screen.getByLabelText('Email'), 'dupe@studio.dev')
  expect(screen.getByText('That email is already registered.')).toBeInTheDocument()
  expect(screen.getByLabelText('Email')).toHaveValue('dupe@studio.dev')
})

test('a non-409 error renders as a form-level message', () => {
  renderForm({ error: new ApiError(400, 'invalid email address', '400 invalid email address') })
  expect(screen.getByText('400 invalid email address')).toBeInTheDocument()
  expect(screen.queryByText('That email is already registered.')).not.toBeInTheDocument()
})

test('pending disables the Create button', () => {
  renderForm({ pending: true })
  expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
})

test('Cancel calls onCancel', async () => {
  const { props } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(props.onCancel).toHaveBeenCalled()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/users/CreateUserForm.test.tsx`
Expected: FAIL - `Failed to resolve import "./CreateUserForm"`.

- [ ] **Step 3: Implement CreateUserForm**

Create `web/src/admin/users/CreateUserForm.tsx`:

```tsx
import { useState, type FormEvent } from 'react'
import { Field } from '../../components/Field'
import { GlassPanel, PillButton } from '../../components/holo'
import { Input } from '../../components/Input'
import { ApiError } from '../../lib/api'
import type { CreateUserBody } from './api'

interface CreateUserFormProps {
  pending: boolean
  error: Error | null
  onSubmit: (body: CreateUserBody) => void
  onCancel: () => void
}

// Inline create panel (mirrors WorkerEditForm's inline-toggle rather than a modal,
// since it is a multi-field form). This is the only surface that can set is_admin:
// no endpoint mutates it after creation.
export function CreateUserForm({ pending, error, onSubmit, onCancel }: CreateUserFormProps) {
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [isAdmin, setIsAdmin] = useState(false)
  const [emailError, setEmailError] = useState<string | undefined>()
  const [passwordError, setPasswordError] = useState<string | undefined>()

  // 409 is the duplicate-email case: show it on the email field and keep the form
  // state so the admin can edit and retry. Anything else is a form-level message.
  const duplicate = error instanceof ApiError && error.status === 409
  const formError = error && !duplicate ? error.message : undefined

  function submit(e: FormEvent) {
    e.preventDefault()
    const trimmedEmail = email.trim()
    if (!trimmedEmail) {
      setEmailError('Email is required.')
      return
    }
    setEmailError(undefined)
    if (password.length < 8) {
      setPasswordError('Password must be at least 8 characters.')
      return
    }
    setPasswordError(undefined)
    onSubmit({ email: trimmedEmail, name: name.trim(), password, is_admin: isAdmin })
  }

  return (
    <GlassPanel as="form" onSubmit={submit} className="p-4">
      <Field
        label="Email"
        htmlFor="new-user-email"
        error={emailError ?? (duplicate ? 'That email is already registered.' : undefined)}
      >
        <Input id="new-user-email" value={email} onChange={(e) => setEmail(e.target.value)} />
      </Field>
      <Field label="Name" htmlFor="new-user-name" hint="Defaults to the email when blank.">
        <Input id="new-user-name" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Password" htmlFor="new-user-password" error={passwordError}>
        <Input
          id="new-user-password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </Field>
      <label
        htmlFor="new-user-admin"
        className="mb-3 flex items-center gap-2 text-[12px] text-fg-mute"
      >
        <input
          id="new-user-admin"
          type="checkbox"
          checked={isAdmin}
          onChange={(e) => setIsAdmin(e.target.checked)}
          className="accent-accent"
        />
        Admin
        <span className="font-mono text-[11px] text-fg-dim">is_admin - set once, at creation</span>
      </label>
      {formError && <div className="mb-3 text-[11px] text-err">{formError}</div>}
      <div className="flex justify-end gap-2">
        <PillButton onClick={onCancel}>Cancel</PillButton>
        <PillButton type="submit" variant="primary" disabled={pending}>
          Create
        </PillButton>
      </div>
    </GlassPanel>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/users/CreateUserForm.test.tsx`
Expected: PASS (8 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/users/CreateUserForm.tsx web/src/admin/users/CreateUserForm.test.tsx
git commit -m "feat(web): admin CreateUserForm with 409 handling"
```

---

## Task 7: ResetPasswordDialog

**Files:**
- Create: `web/src/admin/users/ResetPasswordDialog.tsx`
- Test: `web/src/admin/users/ResetPasswordDialog.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/users/ResetPasswordDialog.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ResetPasswordDialog } from './ResetPasswordDialog'

function renderDialog(over: Partial<Parameters<typeof ResetPasswordDialog>[0]> = {}) {
  const props = {
    email: 'ada@studio.dev',
    pending: false,
    onSubmit: vi.fn(),
    onCancel: vi.fn(),
    ...over,
  }
  return { props, ...render(<ResetPasswordDialog {...props} />) }
}

test('is a labelled modal dialog naming the target and focuses the first field', () => {
  renderDialog()
  const dialog = screen.getByRole('dialog')
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(screen.getByText('Reset password for ada@studio.dev?')).toBeInTheDocument()
  expect(dialog).toHaveAccessibleName('Reset password for ada@studio.dev?')
  expect(screen.getByLabelText('New password')).toHaveFocus()
})

test('warns that every session of the target is revoked and that self-reset signs you out', () => {
  renderDialog()
  expect(
    screen.getByText(/revokes every session belonging to that user/i),
  ).toBeInTheDocument()
  expect(screen.getByText(/if that user is you, you will be signed out immediately/i)).toBeInTheDocument()
})

test('rejects a password under 8 characters without submitting', async () => {
  const { props } = renderDialog()
  await userEvent.type(screen.getByLabelText('New password'), 'short')
  await userEvent.type(screen.getByLabelText('Confirm password'), 'short')
  await userEvent.click(screen.getByRole('button', { name: 'Reset password' }))
  expect(screen.getByText('Password must be at least 8 characters.')).toBeInTheDocument()
  expect(props.onSubmit).not.toHaveBeenCalled()
})

test('rejects a mismatched confirmation without submitting', async () => {
  const { props } = renderDialog()
  await userEvent.type(screen.getByLabelText('New password'), 'password1')
  await userEvent.type(screen.getByLabelText('Confirm password'), 'password2')
  await userEvent.click(screen.getByRole('button', { name: 'Reset password' }))
  expect(screen.getByText('The two passwords do not match.')).toBeInTheDocument()
  expect(props.onSubmit).not.toHaveBeenCalled()
})

test('submits the new password when both fields agree', async () => {
  const { props } = renderDialog()
  await userEvent.type(screen.getByLabelText('New password'), 'password1')
  await userEvent.type(screen.getByLabelText('Confirm password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Reset password' }))
  expect(props.onSubmit).toHaveBeenCalledWith('password1')
})

test('Escape and Cancel both dismiss', async () => {
  const { props } = renderDialog()
  await userEvent.keyboard('{Escape}')
  expect(props.onCancel).toHaveBeenCalledTimes(1)
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(props.onCancel).toHaveBeenCalledTimes(2)
})

test('pending disables the submit button', () => {
  renderDialog({ pending: true })
  expect(screen.getByRole('button', { name: 'Reset password' })).toBeDisabled()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/users/ResetPasswordDialog.test.tsx`
Expected: FAIL - `Failed to resolve import "./ResetPasswordDialog"`.

- [ ] **Step 3: Implement ResetPasswordDialog**

Create `web/src/admin/users/ResetPasswordDialog.tsx`:

```tsx
import { useEffect, useId, useState, type FormEvent } from 'react'
import { Field } from '../../components/Field'
import { Input } from '../../components/Input'

interface ResetPasswordDialogProps {
  email: string
  pending: boolean
  onSubmit: (newPassword: string) => void
  onCancel: () => void
}

// A sibling of ConfirmDialog, not a variant of it: ConfirmDialog takes a text-only
// `body` and cannot host form fields. Matches ConfirmDialog's a11y baseline -
// role="dialog", aria-modal, labelled by its title, Escape dismisses, first field
// focused on open (via autoFocus, so the shared Input primitive does not need to
// forward a ref). No focus trap, same as ConfirmDialog; that debt is tracked by
// docs/backlog/idea-2026-07-01-confirmdialog-focus-trap-hardening.md.
export function ResetPasswordDialog({ email, pending, onSubmit, onCancel }: ResetPasswordDialogProps) {
  const titleId = useId()
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState<string | undefined>()

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onCancel()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onCancel])

  function submit(e: FormEvent) {
    e.preventDefault()
    if (password.length < 8) {
      setError('Password must be at least 8 characters.')
      return
    }
    if (password !== confirm) {
      setError('The two passwords do not match.')
      return
    }
    setError(undefined)
    onSubmit(password)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <form
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onSubmit={submit}
        className="w-full max-w-sm rounded-card border border-border bg-bg p-5 shadow-xl"
      >
        <h2 id={titleId} className="text-[15px] font-medium text-fg">
          Reset password for {email}?
        </h2>
        <p className="mb-4 mt-2 text-[13px] text-fg-mute">
          This revokes every session belonging to that user, so they must sign in again. If that
          user is you, you will be signed out immediately.
        </p>
        <Field label="New password" htmlFor="reset-pw">
          <Input
            id="reset-pw"
            type="password"
            autoFocus
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        <Field label="Confirm password" htmlFor="reset-pw-confirm" error={error}>
          <Input
            id="reset-pw-confirm"
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </Field>
        <div className="mt-5 flex justify-end gap-2">
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
            className="rounded-md border border-err/50 bg-err/20 px-3 py-1.5 text-[12px] font-medium text-err disabled:opacity-40"
          >
            Reset password
          </button>
        </div>
      </form>
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/users/ResetPasswordDialog.test.tsx`
Expected: PASS (7 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/admin/users/ResetPasswordDialog.tsx web/src/admin/users/ResetPasswordDialog.test.tsx
git commit -m "feat(web): ResetPasswordDialog with session-revocation warning"
```

---

## Task 8: UsersTab composition

The only stateful piece. `debounceMs` is a prop with a 300ms default so tests can pass `10` and stay on **real** timers - do not use `vi.useFakeTimers()` here, it deadlocks with `userEvent` and TanStack Query.

**Files:**
- Create: `web/src/admin/users/UsersTab.tsx`
- Test: `web/src/admin/users/UsersTab.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/admin/users/UsersTab.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { AuthProvider } from '../../auth/AuthProvider'
import { clearToken, setToken } from '../../lib/token'
import { UsersTab } from './UsersTab'
import type { AdminUser } from './api'

const ME = { id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: true }

function user(over: Partial<AdminUser> = {}): AdminUser {
  return {
    id: 'u1',
    email: 'ada@studio.dev',
    name: 'Ada',
    is_admin: false,
    created_at: '2026-08-01T12:00:00Z',
    archived_at: null,
    ...over,
  }
}

// Records every GET /v1/users request so tests can assert on query params, and
// serves `pages` in order (each entry is one response envelope).
function listHandler(
  seen: URLSearchParams[],
  envelope: (params: URLSearchParams) => { items: AdminUser[]; next_cursor: string; total: number },
) {
  return http.get('/v1/users', ({ request }) => {
    const params = new URL(request.url).searchParams
    seen.push(params)
    return HttpResponse.json(envelope(params))
  })
}

function renderTab() {
  setToken('tok')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <AuthProvider>
        <UsersTab debounceMs={10} />
      </AuthProvider>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('renders rows from the envelope and the endpoint hint', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('GET /v1/users')).toBeInTheDocument()
  expect(seen[0].get('sort')).toBe('-created_at')
  expect(seen[0].has('include_archived')).toBe(false)
})

test('shows the loading skeleton, then the rows', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  const { container } = renderTab()
  expect(container.querySelectorAll('.h-9').length).toBeGreaterThan(0)
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})

test('shows an error card whose Retry refetches', async () => {
  let calls = 0
  server.use(
    http.get('/v1/users', () => {
      calls++
      if (calls === 1) return HttpResponse.json({ error: 'boom' }, { status: 500 })
      return HttpResponse.json({ items: [user()], next_cursor: '', total: 1 })
    }),
  )
  renderTab()
  expect(await screen.findByText('500 boom')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})

test('toggling include archived sets include_archived=true and resets the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (params) => ({
      items: [user({ archived_at: params.get('include_archived') ? '2026-08-02T00:00:00Z' : null })],
      next_cursor: 'c2',
      total: 3,
    })),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('c2'))

  await userEvent.click(screen.getByLabelText(/include archived/i))
  await waitFor(() => expect(seen.at(-1)?.get('include_archived')).toBe('true'))
  expect(seen.at(-1)?.has('cursor')).toBe(false)
  expect(await screen.findByRole('button', { name: 'Unarchive' })).toBeInTheDocument()
})

test('typing in the email filter issues exactly one ?email= request and hides the pager', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: 'c2', total: 9 })))
  renderTab()
  await screen.findByText('ada@studio.dev')
  expect(screen.getByRole('button', { name: /next 50/ })).toBeInTheDocument()

  await userEvent.type(screen.getByLabelText('Filter by email'), 'ada@studio.dev')
  await waitFor(() => expect(seen.filter((p) => p.has('email')).length).toBe(1))
  expect(seen.filter((p) => p.has('email'))[0].get('email')).toBe('ada@studio.dev')

  // The server returns before parsePage on the ?email= branch, so the pager would
  // claim a page that does not exist.
  await waitFor(() => expect(screen.queryByRole('button', { name: /next 50/ })).not.toBeInTheDocument())
})

test('a filter with no match shows the filtered empty card', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (params) =>
      params.has('email')
        ? { items: [], next_cursor: '', total: 0 }
        : { items: [user()], next_cursor: '', total: 1 },
    ),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.type(screen.getByLabelText('Filter by email'), 'nobody@studio.dev')
  expect(await screen.findByText('No users match that email.')).toBeInTheDocument()
})

test('a header click issues the expected sort and resets the cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: 'c2', total: 3 })))
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  await waitFor(() => expect(seen.at(-1)?.get('cursor')).toBe('c2'))

  await userEvent.click(screen.getByRole('button', { name: /^EMAIL/ }))
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('email'))
  expect(seen.at(-1)?.has('cursor')).toBe(false)

  await userEvent.click(screen.getByRole('button', { name: /^EMAIL/ }))
  await waitFor(() => expect(seen.at(-1)?.get('sort')).toBe('-email'))
})

test('pagination walks the cursor stack and reports the computed range', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, (params) =>
      params.get('cursor') === 'c2'
        ? { items: [user({ id: 'u2', email: 'bob@studio.dev' })], next_cursor: '', total: 2 }
        : { items: [user()], next_cursor: 'c2', total: 2 },
    ),
  )
  renderTab()
  expect(await screen.findByText('1-1 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /prev/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /next 50/ }))
  expect(await screen.findByText('bob@studio.dev')).toBeInTheDocument()
  expect(await screen.findByText('2-2 of 2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /next 50/ })).toBeDisabled()

  await userEvent.click(screen.getByRole('button', { name: /prev/ }))
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(await screen.findByText('1-1 of 2')).toBeInTheDocument()
})

test('creating a user POSTs, closes the form, and refreshes the table', async () => {
  const seen: URLSearchParams[] = []
  let created = false
  let body: unknown
  server.use(
    listHandler(seen, () =>
      created
        ? { items: [user(), user({ id: 'u2', email: 'new@studio.dev', name: 'New' })], next_cursor: '', total: 2 }
        : { items: [user()], next_cursor: '', total: 1 },
    ),
    http.post('/v1/users', async ({ request }) => {
      body = await request.json()
      created = true
      return HttpResponse.json(user({ id: 'u2', email: 'new@studio.dev' }), { status: 201 })
    }),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: '+ Create user' }))
  await userEvent.type(screen.getByLabelText('Email'), 'new@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))

  await waitFor(() =>
    expect(body).toEqual({ email: 'new@studio.dev', name: '', password: 'password1', is_admin: false }),
  )
  expect(await screen.findByText('new@studio.dev')).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByLabelText('Password')).not.toBeInTheDocument())
})

test('renaming a row PATCHes and refreshes without a confirm dialog', async () => {
  const seen: URLSearchParams[] = []
  let renamed = false
  server.use(
    listHandler(seen, () => ({ items: [user({ name: renamed ? 'Ada L' : 'Ada' })], next_cursor: '', total: 1 })),
    http.patch('/v1/users/u1', () => {
      renamed = true
      return HttpResponse.json(user({ name: 'Ada L' }))
    }),
  )
  renderTab()
  await screen.findByText('Ada')
  await userEvent.click(screen.getByRole('button', { name: 'Rename' }))
  const input = screen.getByLabelText('Name for ada@studio.dev')
  await userEvent.clear(input)
  await userEvent.type(input, 'Ada L')
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByText('Ada L')).toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('Archive is behind a confirm dialog: Cancel fires no request, Confirm fires one', async () => {
  const seen: URLSearchParams[] = []
  let archiveCalls = 0
  server.use(
    listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })),
    http.post('/v1/users/u1/archive', () => {
      archiveCalls++
      return HttpResponse.json(user({ archived_at: '2026-08-02T00:00:00Z' }))
    }),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')

  await userEvent.click(screen.getByRole('button', { name: 'Archive' }))
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  expect(screen.getByText(/revokes all of their API tokens/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(archiveCalls).toBe(0)

  await userEvent.click(screen.getByRole('button', { name: 'Archive' }))
  await userEvent.click(screen.getAllByRole('button', { name: 'Archive' }).at(-1)!)
  await waitFor(() => expect(archiveCalls).toBe(1))
})

test('Unarchive is behind a confirm dialog', async () => {
  const seen: URLSearchParams[] = []
  let unarchiveCalls = 0
  server.use(
    listHandler(seen, () => ({
      items: [user({ archived_at: '2026-08-02T00:00:00Z' })],
      next_cursor: '',
      total: 1,
    })),
    http.post('/v1/users/u1/unarchive', () => {
      unarchiveCalls++
      return HttpResponse.json(user())
    }),
  )
  renderTab()
  await userEvent.click(await screen.findByLabelText(/include archived/i))
  await userEvent.click(await screen.findByRole('button', { name: 'Unarchive' }))
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  await userEvent.click(screen.getAllByRole('button', { name: 'Unarchive' }).at(-1)!)
  await waitFor(() => expect(unarchiveCalls).toBe(1))
})

test('Reset pw opens the dialog and POSTs email + new_password', async () => {
  const seen: URLSearchParams[] = []
  let body: unknown
  server.use(
    listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })),
    http.post('/v1/users/password-reset', async ({ request }) => {
      body = await request.json()
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: 'Reset pw' }))
  await userEvent.type(screen.getByLabelText('New password'), 'password1')
  await userEvent.type(screen.getByLabelText('Confirm password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: 'Reset password' }))

  await waitFor(() =>
    expect(body).toEqual({ email: 'ada@studio.dev', new_password: 'password1' }),
  )
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

test('a server-guard rejection renders inline and leaves the table mounted', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })),
    http.post('/v1/users/u1/archive', () =>
      HttpResponse.json({ error: 'cannot archive the last active admin' }, { status: 400 }),
    ),
  )
  renderTab()
  await screen.findByText('ada@studio.dev')
  await userEvent.click(screen.getByRole('button', { name: 'Archive' }))
  await userEvent.click(screen.getAllByRole('button', { name: 'Archive' }).at(-1)!)

  expect(await screen.findByText('400 cannot archive the last active admin')).toBeInTheDocument()
  expect(screen.getByText('ada@studio.dev')).toBeInTheDocument()
})

test('the acting admin own row has no Archive control', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    listHandler(seen, () => ({
      items: [user({ id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: true })],
      next_cursor: '',
      total: 1,
    })),
  )
  renderTab()
  expect(await screen.findByText('me@studio.dev')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Archive' })).not.toBeInTheDocument()
})

test('the footnote explains the archive and reset side effects', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('ada@studio.dev')
  expect(screen.getByText(/Server guards prevent archiving yourself or the last active admin/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/admin/users/UsersTab.test.tsx`
Expected: FAIL - `Failed to resolve import "./UsersTab"`.

- [ ] **Step 3: Implement UsersTab**

Create `web/src/admin/users/UsersTab.tsx`:

```tsx
import { useState } from 'react'
import { useAuth } from '../../auth/AuthProvider'
import { Button } from '../../components/Button'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { GlassPanel, PillButton } from '../../components/holo'
import { computePageRange } from '../../lib/pageRange'
import { useDebouncedValue } from '../../lib/useDebouncedValue'
import { CreateUserForm } from './CreateUserForm'
import { ResetPasswordDialog } from './ResetPasswordDialog'
import { UsersTable } from './UsersTable'
import { useAdminUserActions } from './useAdminUserActions'
import { useAdminUsers } from './useAdminUsers'
import type { AdminUser, CreateUserBody, UserSort, UserSortField } from './api'

// Same shape as WorkersPage's toggleSort: clicking the active column flips its
// direction, clicking another column selects it ascending.
function toggleSort(field: UserSortField, current: UserSort): UserSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as UserSort
  }
  return field
}

type Confirm = { kind: 'archive' | 'unarchive'; user: AdminUser } | null

// debounceMs is a prop only so tests can shrink it and stay on real timers;
// production always uses the 300ms default.
export function UsersTab({ debounceMs = 300 }: { debounceMs?: number }) {
  const { user: me } = useAuth()
  const [sort, setSort] = useState<UserSort>('-created_at')
  const [includeArchived, setIncludeArchived] = useState(false)
  const [emailInput, setEmailInput] = useState('')
  const email = useDebouncedValue(emailInput, debounceMs).trim()
  // Cursor of the current page (''=first). The stack holds the cursors we paged
  // forward from; offsets tracks the real row offset so partial pages stay
  // correct. Same pattern as JobsPage.
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
  const [creating, setCreating] = useState(false)
  const [confirm, setConfirm] = useState<Confirm>(null)
  const [resetting, setResetting] = useState<AdminUser | null>(null)

  const { data, error, isLoading, isPlaceholderData, refetch } = useAdminUsers(
    sort,
    includeArchived,
    cursor,
    email,
  )
  const { create, rename, archive, unarchive, resetPassword } = useAdminUserActions()

  // create.error is routed into CreateUserForm (it owns the 409 copy), so it is
  // deliberately not part of the shared inline error box.
  const actionError = (rename.error ?? archive.error ?? unarchive.error ?? resetPassword.error) as
    | Error
    | null
  const busy =
    rename.isPending || archive.isPending || unarchive.isPending || resetPassword.isPending
  const filtering = email !== ''

  function resetPaging() {
    setCursor('')
    setStack([])
    setStartOffset(0)
    setOffsets([])
  }

  function pickSort(field: UserSortField) {
    setSort(toggleSort(field, sort))
    // The server rejects a cursor whose sort key does not match (internal/api/pagination.go).
    resetPaging()
  }

  function pickIncludeArchived(v: boolean) {
    setIncludeArchived(v)
    // Different row set and total, so the old cursor is meaningless.
    resetPaging()
  }

  function pickEmail(v: string) {
    setEmailInput(v)
    resetPaging()
  }

  // Plain setters, not functional updaters: cursor, stack, and offsets are all read
  // from the current render and React batches them in one event (same reasoning as
  // JobsPage.next/prev).
  function next() {
    if (!data?.next_cursor) return
    const currentPageSize = data.items.length
    setStack([...stack, cursor])
    setCursor(data.next_cursor)
    setOffsets([...offsets, startOffset])
    setStartOffset(startOffset + currentPageSize)
  }

  function prev() {
    if (stack.length === 0) return
    const copy = [...stack]
    const back = copy.pop() ?? ''
    setStack(copy)
    setCursor(back)
    const offsetsCopy = [...offsets]
    const prevOffset = offsetsCopy.pop() ?? 0
    setOffsets(offsetsCopy)
    setStartOffset(prevOffset)
  }

  function onCreate(body: CreateUserBody) {
    create.mutate(body, { onSuccess: () => setCreating(false) })
  }

  function runConfirmed() {
    if (!confirm) return
    if (confirm.kind === 'archive') archive.mutate(confirm.user.id)
    else unarchive.mutate(confirm.user.id)
    setConfirm(null)
  }

  function onResetSubmit(newPassword: string) {
    if (!resetting) return
    resetPassword.mutate(
      { email: resetting.email, newPassword },
      { onSuccess: () => setResetting(null) },
    )
  }

  const users = data?.items ?? []
  const total = data?.total ?? 0
  const { x, y } = computePageRange(startOffset, users.length)
  const rangeText =
    users.length === 0
      ? `0 of ${total.toLocaleString()}`
      : `${x.toLocaleString()}-${y.toLocaleString()} of ${total.toLocaleString()}`

  let body
  if (isLoading && !data) {
    body = (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 8 }).map((_, i) => (
          <GlassPanel key={i} className="h-9" />
        ))}
      </div>
    )
  } else if (error && !data) {
    body = (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
        <Button className="w-auto px-4" onClick={() => refetch()}>
          Retry
        </Button>
      </GlassPanel>
    )
  } else if (users.length === 0) {
    body = (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
        {filtering ? 'No users match that email.' : 'No users yet.'}
      </GlassPanel>
    )
  } else {
    body = (
      <>
        <UsersTable
          users={users}
          sort={sort}
          onSort={pickSort}
          showArchived={includeArchived}
          currentUserId={me?.id ?? ''}
          busy={busy}
          onRename={(id, name) => rename.mutate({ id, name })}
          onResetPassword={(u) => setResetting(u)}
          onArchive={(u) => setConfirm({ kind: 'archive', user: u })}
          onUnarchive={(u) => setConfirm({ kind: 'unarchive', user: u })}
        />
        <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
          <span>
            SHOWING <span className="text-fg">{rangeText}</span>
            {' · '}/v1/users{filtering ? ' · EXACT EMAIL MATCH' : ' · CURSOR PAGINATED'}
          </span>
          {/* The server returns before parsePage on the ?email= branch, so while a
              filter is active there is no page to walk. */}
          {!filtering && (
            <div className="flex gap-2">
              <button
                type="button"
                onClick={prev}
                disabled={stack.length === 0 || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                ← prev
              </button>
              <button
                type="button"
                onClick={next}
                disabled={!data?.next_cursor || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                next 50 →
              </button>
            </div>
          )}
        </div>
      </>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-mono text-[11px] tracking-[0.06em] text-fg-mute">GET /v1/users</span>
        <label className="flex cursor-pointer items-center gap-2 text-[12px] text-fg-mute">
          <input
            type="checkbox"
            checked={includeArchived}
            onChange={(e) => pickIncludeArchived(e.target.checked)}
            className="accent-accent"
          />
          include archived
          <span className="font-mono text-[11px] text-fg-dim">?include_archived=true</span>
        </label>
        <input
          aria-label="Filter by email"
          placeholder="?email=… exact match"
          value={emailInput}
          onChange={(e) => pickEmail(e.target.value)}
          className="ml-auto min-w-[240px] rounded-full border border-border bg-black/25 px-3.5 py-1.5 text-[12px] text-fg outline-none placeholder:text-fg-dim focus:border-accent"
        />
        <PillButton variant="primary" onClick={() => setCreating((v) => !v)}>
          + Create user
        </PillButton>
      </div>

      {creating && (
        <CreateUserForm
          pending={create.isPending}
          error={create.error as Error | null}
          onSubmit={onCreate}
          onCancel={() => setCreating(false)}
        />
      )}

      {actionError ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      {body}

      <div className="font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim">
        ▸ <span className="text-fg-mute">Archive</span> revokes all of their API tokens, forces
        re-login, and disables their scheduled jobs. Server guards prevent archiving yourself or the
        last active admin. Password reset revokes the target's sessions too.
      </div>

      {confirm && (
        <ConfirmDialog
          title={
            confirm.kind === 'archive'
              ? `Archive ${confirm.user.email}?`
              : `Unarchive ${confirm.user.email}?`
          }
          body={
            confirm.kind === 'archive'
              ? 'This revokes all of their API tokens, forces re-login, and disables their scheduled jobs.'
              : 'This restores their access. Their API tokens are not restored, so they must sign in again.'
          }
          confirmLabel={confirm.kind === 'archive' ? 'Archive' : 'Unarchive'}
          destructive={confirm.kind === 'archive'}
          onConfirm={runConfirmed}
          onCancel={() => setConfirm(null)}
        />
      )}

      {resetting && (
        <ResetPasswordDialog
          email={resetting.email}
          pending={resetPassword.isPending}
          onSubmit={onResetSubmit}
          onCancel={() => setResetting(null)}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/admin/users/UsersTab.test.tsx`
Expected: PASS (16 tests).

If the "typing in the email filter" test sees two `?email=` requests, the debounce is not wired to the query key - check that `useAdminUsers` receives the debounced `email`, not `emailInput`.

- [ ] **Step 5: Prove the debounce test is not vacuous**

Temporarily pass the raw input straight through in `web/src/admin/users/UsersTab.tsx`:

```ts
  const email = emailInput.trim()
```

Run: `npx vitest run src/admin/users/UsersTab.test.tsx -t 'email filter'`
Expected: FAIL - one request per keystroke, so `seen.filter((p) => p.has('email')).length` is 14, not 1. If it still passes, the assertion is not measuring the debounce.

Restore `const email = useDebouncedValue(emailInput, debounceMs).trim()` and re-run:

Run: `npx vitest run src/admin/users/UsersTab.test.tsx`
Expected: PASS (16 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/admin/users/UsersTab.tsx web/src/admin/users/UsersTab.test.tsx
git commit -m "feat(web): admin Users tab with filters, pagination, and actions"
```

---

## Task 9: Tab registry, AdminTabs, AdminPage

**Files:**
- Create: `web/src/admin/tabs.ts`
- Create: `web/src/admin/AdminTabs.tsx`
- Test: `web/src/admin/AdminTabs.test.tsx`
- Create: `web/src/admin/AdminPage.tsx`
- Test: `web/src/admin/AdminPage.test.tsx`

- [ ] **Step 1: Write the failing AdminTabs test**

Create `web/src/admin/AdminTabs.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { ADMIN_TABS, DEFAULT_ADMIN_TAB, findAdminTab } from './tabs'
import { AdminTabs } from './AdminTabs'

function renderTabs(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <AdminTabs />
    </MemoryRouter>,
  )
}

test('the registry holds exactly the built tabs', () => {
  expect(ADMIN_TABS.map((t) => t.slug)).toEqual(['users'])
  expect(DEFAULT_ADMIN_TAB).toBe('users')
})

test('findAdminTab resolves a known slug and rejects everything else', () => {
  expect(findAdminTab('users')?.label).toBe('Users')
  expect(findAdminTab('invites')).toBeUndefined()
  expect(findAdminTab('bogus')).toBeUndefined()
  expect(findAdminTab(undefined)).toBeUndefined()
})

test('renders one link per registry entry, pointing at /admin/<slug>', () => {
  renderTabs('/admin/users')
  expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('href', '/admin/users')
  expect(screen.getAllByRole('link')).toHaveLength(1)
})

test('the current tab is marked as the current page', () => {
  renderTabs('/admin/users')
  expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('aria-current', 'page')
})

test('tabs that are not built yet are absent', () => {
  renderTabs('/admin/users')
  for (const label of ['Invites', 'Agent enrolls', 'Reservations', 'Server']) {
    expect(screen.queryByText(label)).not.toBeInTheDocument()
  }
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `npx vitest run src/admin/AdminTabs.test.tsx`
Expected: FAIL - `Failed to resolve import "./tabs"`.

- [ ] **Step 3: Implement the registry and the tab bar**

Create `web/src/admin/tabs.ts`:

```ts
import type { ComponentType } from 'react'
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
// docs/backlog/feature-2026-08-08-admin-invites-tab.md,
// docs/backlog/feature-2026-08-08-admin-enrollments-tab.md,
// docs/backlog/feature-2026-08-08-admin-reservations-tab.md,
// docs/backlog/feature-2026-08-08-admin-server-overview-tab.md.
export const ADMIN_TABS: AdminTab[] = [{ slug: 'users', label: 'Users', Panel: UsersTab }]

export const DEFAULT_ADMIN_TAB = 'users'

export function findAdminTab(slug: string | undefined): AdminTab | undefined {
  return ADMIN_TABS.find((t) => t.slug === slug)
}
```

Create `web/src/admin/AdminTabs.tsx`:

```tsx
import { NavLink } from 'react-router-dom'
import { ADMIN_TABS } from './tabs'

// The hi-fi's pill-group tab bar: rounded-full, dark translucent, active tab
// filled with the accent gradient. NavLink supplies the active state (and
// aria-current="page"), matching the HoloShell nav pattern. Count badges are not
// rendered in this slice - a count would have to be lifted out of each panel's
// own query, and the Users footer already shows the total.
export function AdminTabs() {
  return (
    <div className="flex gap-1.5 self-start rounded-full border border-border bg-black/30 p-[3px] backdrop-blur-[8px]">
      {ADMIN_TABS.map((t) => (
        <NavLink
          key={t.slug}
          to={`/admin/${t.slug}`}
          className={({ isActive }) =>
            `rounded-full px-3.5 py-1.5 text-[12px] tracking-[0.02em] transition-colors ${
              isActive
                ? 'bg-gradient-to-r from-accent to-accent-b font-semibold text-bg'
                : 'text-fg-mute hover:text-fg'
            }`
          }
        >
          {t.label}
        </NavLink>
      ))}
    </div>
  )
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `npx vitest run src/admin/AdminTabs.test.tsx`
Expected: PASS (5 tests).

- [ ] **Step 5: Write the failing AdminPage test**

Create `web/src/admin/AdminPage.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes, useParams } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { AdminPage } from './AdminPage'

const ME = { id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: true }

function Landing() {
  const { tab } = useParams()
  return <div>landed on {tab}</div>
}

function renderAt(path: string) {
  setToken('tok')
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.get('/v1/users', () =>
      HttpResponse.json({
        items: [
          {
            id: 'u1',
            email: 'ada@studio.dev',
            name: 'Ada',
            is_admin: false,
            created_at: '2026-08-01T12:00:00Z',
            archived_at: null,
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <Routes>
            <Route path="/admin/users" element={<AdminPage />} />
            <Route path="/admin/:tab" element={<AdminPage />} />
            <Route path="/landed/:tab" element={<Landing />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('renders the hi-fi header, the tab bar, and the Users panel', async () => {
  renderAt('/admin/users')
  expect(screen.getByText('SETTINGS · ADMIN ONLY')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Users' })).toBeInTheDocument()
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})

test('renders no unbacked server-facts strip', () => {
  renderAt('/admin/users')
  for (const label of ['VERSION', 'BUILD', 'DB', 'UPTIME']) {
    expect(screen.queryByText(label)).not.toBeInTheDocument()
  }
})

test('an unknown tab segment redirects to the users tab', async () => {
  renderAt('/admin/bogus')
  // The redirect lands on the /admin/users route, which renders the shell + panel.
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
})

test('a not-yet-built tab segment redirects rather than rendering an empty shell', async () => {
  renderAt('/admin/invites')
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})
```

- [ ] **Step 6: Run it to verify it fails**

Run: `npx vitest run src/admin/AdminPage.test.tsx`
Expected: FAIL - `Failed to resolve import "./AdminPage"`.

- [ ] **Step 7: Implement AdminPage**

Create `web/src/admin/AdminPage.tsx`:

```tsx
import { Navigate, useParams } from 'react-router-dom'
import { Eyebrow } from '../components/holo'
import { AdminTabs } from './AdminTabs'
import { DEFAULT_ADMIN_TAB, findAdminTab } from './tabs'

// The admin shell. The hi-fi's right-aligned VERSION / BUILD / DB / UPTIME strip
// is omitted: no endpoint returns build or uptime facts (GET /v1/health returns
// {"status":"ok"}, GET /v1/config returns only {allow_self_register}). It belongs
// to the future Server/overview tab.
export function AdminPage() {
  const { tab } = useParams()
  const active = findAdminTab(tab)
  // Unknown, or a tab that is not built yet: redirect rather than render an empty
  // shell, so the console never shows a dead tab.
  if (!active) return <Navigate to={`/admin/${DEFAULT_ADMIN_TAB}`} replace />
  const Panel = active.Panel

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <Eyebrow>SETTINGS · ADMIN ONLY</Eyebrow>
          <h1 className="text-[32px] font-normal tracking-tight">Admin</h1>
        </div>
      </div>
      <AdminTabs />
      <div className="flex min-h-0 flex-1 flex-col gap-3">
        <Panel />
      </div>
    </div>
  )
}
```

- [ ] **Step 8: Run it to verify it passes**

Run: `npx vitest run src/admin/AdminPage.test.tsx src/admin/AdminTabs.test.tsx`
Expected: PASS (9 tests).

- [ ] **Step 9: Commit**

```bash
git add web/src/admin/tabs.ts web/src/admin/AdminTabs.tsx web/src/admin/AdminTabs.test.tsx web/src/admin/AdminPage.tsx web/src/admin/AdminPage.test.tsx
git commit -m "feat(web): admin shell with tab registry and unknown-tab redirect"
```

---

## Task 10: AdminRoute guard, router wiring, and nav gating

**Files:**
- Create: `web/src/app/AdminRoute.tsx`
- Test: `web/src/app/AdminRoute.test.tsx`
- Modify: `web/src/app/router.tsx:1-34`
- Modify: `web/src/shell/HoloShell.tsx:7-12,28-44`
- Test: `web/src/shell/HoloShell.test.tsx`

- [ ] **Step 1: Write the failing guard + routing test**

Create `web/src/app/AdminRoute.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { AppRoutes } from './router'

const USERS_PAGE = {
  items: [
    {
      id: 'u1',
      email: 'ada@studio.dev',
      name: 'Ada',
      is_admin: false,
      created_at: '2026-08-01T12:00:00Z',
      archived_at: null,
    },
  ],
  next_cursor: '',
  total: 1,
}

// Exercises the REAL router tree, so a guard that works in isolation but is wired
// wrong in router.tsx still fails here.
function renderApp(path: string, isAdmin: boolean) {
  setToken('tok')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: isAdmin }),
    ),
    http.get('/v1/users', () => HttpResponse.json(USERS_PAGE)),
    // Only hit on the non-admin redirect to /jobs.
    http.get('/v1/jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ running: 0, queued: 0, done_24h: 0, failed_24h: 0 }),
    ),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <AppRoutes />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('/admin redirects an admin to the users tab', async () => {
  renderApp('/admin', true)
  expect(await screen.findByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})

test('/admin/bogus redirects an admin to the users tab', async () => {
  renderApp('/admin/bogus', true)
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})

test('a non-admin at /admin/users is redirected to /jobs', async () => {
  renderApp('/admin/users', false)
  expect(await screen.findByRole('heading', { level: 1, name: 'Jobs' })).toBeInTheDocument()
  expect(screen.queryByRole('heading', { level: 1, name: 'Admin' })).not.toBeInTheDocument()
})

test('a non-admin at /admin is redirected to /jobs', async () => {
  renderApp('/admin', false)
  expect(await screen.findByRole('heading', { level: 1, name: 'Jobs' })).toBeInTheDocument()
})

test('a non-admin sees no Admin nav entry and an admin does', async () => {
  const nonAdmin = renderApp('/jobs', false)
  await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Jobs' })).toBeInTheDocument())
  expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument()
  nonAdmin.unmount()
  clearToken()

  renderApp('/jobs', true)
  expect(await screen.findByRole('link', { name: 'Admin' })).toBeInTheDocument()
})
```

Create `web/src/shell/HoloShell.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { HoloShell } from './HoloShell'

function renderShell(isAdmin: boolean) {
  setToken('tok')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: isAdmin }),
    ),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/jobs']}>
        <AuthProvider>
          <HoloShell>
            <div>page body</div>
          </HoloShell>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('always shows the non-admin nav entries', async () => {
  renderShell(false)
  await waitFor(() => expect(screen.getByText('page body')).toBeInTheDocument())
  for (const label of ['Jobs', 'Workers', 'Schedules']) {
    expect(screen.getByRole('link', { name: label })).toBeInTheDocument()
  }
})

test('hides the Admin nav entry from non-admins', async () => {
  renderShell(false)
  await waitFor(() => expect(screen.getByRole('link', { name: 'Jobs' })).toBeInTheDocument())
  expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument()
})

test('shows the Admin nav entry to admins', async () => {
  renderShell(true)
  expect(await screen.findByRole('link', { name: 'Admin' })).toHaveAttribute('href', '/admin')
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/app/AdminRoute.test.tsx src/shell/HoloShell.test.tsx`
Expected: FAIL - `AdminRoute.test.tsx` fails to resolve `./AdminRoute`; `HoloShell.test.tsx` fails on "hides the Admin nav entry from non-admins" because `NAV` currently lists Admin unconditionally (`web/src/shell/HoloShell.tsx:11`).

- [ ] **Step 3: Implement AdminRoute**

Create `web/src/app/AdminRoute.tsx`:

```tsx
import { Navigate, Outlet } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'

// UX-only guard. The security boundary is server-side: every /v1/users admin route
// is registered auth(admin(...)) (internal/api/server.go:150-156), so a non-admin
// who forges client state just collects 403s. This renders INSIDE ProtectedRoute,
// so an authenticated user is already guaranteed and only is_admin is checked.
export function AdminRoute() {
  const { user } = useAuth()
  if (!user?.is_admin) return <Navigate to="/jobs" replace />
  return <Outlet />
}
```

- [ ] **Step 4: Wire the routes**

In `web/src/app/router.tsx`, add two imports after line 10:

```tsx
import { AdminPage } from '../admin/AdminPage'
import { AdminRoute } from './AdminRoute'
```

Then replace the single stub line 28:

```tsx
        <Route path="/admin" element={<JobsPlaceholder />} />
```

with the guarded pair:

```tsx
        <Route element={<AdminRoute />}>
          <Route path="/admin" element={<Navigate to="/admin/users" replace />} />
          <Route path="/admin/:tab" element={<AdminPage />} />
        </Route>
```

`Navigate` is already imported on line 1. Keep the `JobsPlaceholder` import - line 29 (`/profile/*`) still uses it.

- [ ] **Step 5: Gate the Admin nav entry**

In `web/src/shell/HoloShell.tsx`, replace the `NAV` constant (lines 7-12):

```tsx
const NAV = [
  { to: '/jobs', label: 'Jobs' },
  { to: '/workers', label: 'Workers' },
  { to: '/schedules', label: 'Schedules' },
  // Cosmetic gate only - AdminRoute redirects and the server's AdminOnly
  // middleware is the real boundary. Hiding it keeps non-admins out of a route
  // that would only 403 for them.
  { to: '/admin', label: 'Admin', adminOnly: true },
]
```

Then, inside `HoloShell`, add the filtered list right after the `navigate` declaration (line 16):

```tsx
  const nav = NAV.filter((n) => !n.adminOnly || user?.is_admin)
```

and change the map on line 29 from `NAV.map((n) => (` to `nav.map((n) => (`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `npx vitest run src/app/AdminRoute.test.tsx src/shell/HoloShell.test.tsx`
Expected: PASS (8 tests).

- [ ] **Step 7: Commit**

```bash
git add web/src/app/AdminRoute.tsx web/src/app/AdminRoute.test.tsx web/src/app/router.tsx web/src/shell/HoloShell.tsx web/src/shell/HoloShell.test.tsx
git commit -m "feat(web): admin-gated /admin/:tab routes and nav entry"
```

---

## Task 11: Full verification

**Files:** none (verification only).

- [ ] **Step 1: Run the full test suite**

Run (from `web/`): `npm test`
Expected: PASS, every test file green - the existing suite plus the 12 new files. If a pre-existing test broke, the likely cause is the `HoloShell` nav change; fix the test only if it asserted the old unconditional Admin link, otherwise fix the implementation.

- [ ] **Step 2: Run the production build (type-check included)**

Run (from `web/`): `npm run build`
Expected: `tsc -b` reports no type errors and `vite build` completes. Common failures to fix: an unused import, `Parameters<typeof Component>[0]` in a test needing the props interface exported, or a `UserSort` cast.

- [ ] **Step 3: Revert the build artifacts**

`web/dist` is tracked but stale from the original scaffold, so `npm run build` dirties it. It is NOT maintained per-change.

Run: `git checkout -- web/dist/`
Then run: `git status`
Expected: clean, or only the files this plan intentionally created/modified. `web/dist/` must not appear in the change set.

- [ ] **Step 4: Browser check against the hi-fi Holo**

Start the server and the dev SPA, sign in as an admin, and visit `/admin/users`. Compare against `design_handoff_relay_holo/hifi3-holo-pages.jsx` `HoloAdmin` (line 1930) and `AdminUsers` (line 2003) - the hi-fi is authoritative; the orange/cursive `reference/screens/admin.js` sketch is structure-only.

Confirm by eye:
- Eyebrow `SETTINGS · ADMIN ONLY` above a 32px/400 `Admin` title, matching the Workers page treatment.
- The tab bar is a single rounded-full pill group, `self-start`, dark translucent, with Users filled by the accent gradient. No other tabs.
- The control row reads `GET /v1/users`, the `include archived` checkbox with its `?include_archived=true` annotation, the right-aligned email pill input, and a gradient `+ Create user` button.
- The table is a glass panel with a mono 10px/0.16em header row; rows show the 24px gradient avatar square, the email in sans, the muted name, and an ADMIN/USER pill (accent for admin). Row action buttons should not make rows visibly taller than the hi-fi's - if they do, tighten the `MINI` padding in `UsersTable.tsx`.
- Toggle `include archived` and confirm archived rows render dimmed with only `Unarchive`.
- Your own row has no `Archive` button.
- The footer reads `SHOWING x-y of total · /v1/users · CURSOR PAGINATED`, and the pager disappears while an email filter is active.

- [ ] **Step 5: Confirm the scope boundary**

Run: `git diff --stat main...HEAD`
Expected: changes only under `web/src/admin/`, `web/src/app/`, `web/src/shell/`, and `web/src/lib/useDebouncedValue*`. Zero Go files, zero `.sql` files, zero `web/dist`. If anything else appears, remove it.

---

## Self-Review (completed by plan author)

**1. Spec coverage**

| Spec requirement | Task |
|---|---|
| Six typed API clients + exact paths/bodies | 1 |
| Contract check vs `userResponse` / `createUserRequest` / `updateUserRequest` / reset body | 1 Step 5 |
| Debounced (300ms) email filter | 2, 8 |
| List query key `['users', sort, includeArchived, cursor, email]`, `keepPreviousData`, no polling | 3 |
| Five mutations, bare `['users']` invalidation, 204 tolerated, active-observer test | 4 |
| Table columns, ADMIN/USER pill only, avatar, archived dimming + Unarchive-only, own-row Archive hidden, sort headers + `aria-sort`, inline rename | 5 |
| Create panel with `is_admin` at creation, client validation, 409 inline | 6 |
| Password-reset dialog, >= 8 chars, mismatch, revocation warning incl. self-signout, a11y baseline | 7 |
| `include_archived` toggle resets cursor; filter hides pager; sort resets cursor; cursor pagination + `computePageRange` footer; `isPlaceholderData` pager disable; loading/error/empty triad; ConfirmDialog for archive/unarchive; inline server-guard errors; busy disables triggers | 8 |
| Registry + pill tab bar; unknown/unbuilt tab redirects to `/admin/users`; hi-fi header; no server-facts strip | 9 |
| `AdminRoute` gate, `/admin` -> `/admin/users`, `/admin/:tab`, no `?tab=`, nav entry filtered on `is_admin` | 10 |
| `npm test` + build green, browser check, `web/dist` reverted, scope boundary | 11 |
| Omissions honoured (no SESSIONS, no LAST LOGIN, no `service` role, no role-change control, no server-facts strip, no count badges, no page-size button) | 5, 9, plus explicit assertions in 5 and 9 tests |

Two spec points were **underspecified and decided here**:
- The spec's `AdminTabs` sketch allows an optional `count?` badge. This plan renders **no** count badge: the only real count lives inside the Users panel's own query, so lifting it into the shell would either duplicate the request or fabricate a number. Registry entries are `{ slug, label, Panel }`. Adding a badge later is additive.
- The spec does not say where the debounce lives. This plan puts a generic `useDebouncedValue` in `web/src/lib/` (not `web/src/admin/`) since it is not admin-specific, and makes `UsersTab`'s delay a prop (`debounceMs = 300`) so tests can shrink it and avoid the known `vi.useFakeTimers()` + `userEvent` deadlock.

**2. Placeholder scan:** No TBD/TODO/"handle edge cases"/"similar to Task N". Every code step contains complete, runnable code. Every test step names the exact command and the expected failure text.

**3. Type consistency:** `AdminUser`, `AdminUsersPage`, `UserSort`, `UserSortField`, `CreateUserBody`, and `ListUsersParams` are defined once in Task 1 and used with the same names in Tasks 3, 4, 5, 6, and 8. `listUsers` takes a single object argument in Task 1 and is called that way in Task 3. `rename` takes `{ id, name }` and `resetPassword` takes `{ email, newPassword }` in Task 4, and Task 8 calls both with exactly those shapes. `UsersTable`'s prop names (`showArchived`, `currentUserId`, `busy`, `onRename`, `onResetPassword`, `onArchive`, `onUnarchive`) match Task 8's call site. `findAdminTab` / `ADMIN_TABS` / `DEFAULT_ADMIN_TAB` are defined in Task 9 and used in the same task's `AdminPage` and `AdminTabs`. `useAdminUsers(sort, includeArchived, cursor, email)` has one signature across Tasks 3, 4, and 8.
