# New Job submit form ("+ New job") Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a frontend "+ New job" flow to the web SPA - an entry-point button on the jobs list opening a `/jobs/new` route that hosts a JSON job-spec editor, which POSTs to the existing `POST /v1/jobs`, surfaces backend errors inline, and navigates to the created job on success.

**Architecture:** Pure frontend slice. A new `createJob` api-client function, a minimal `specTemplate.ts` (starter template string + a small `validateSpecText` helper), a router-free `useCreateJob` mutation hook (invalidates `['jobs']` and `['job-stats']`), a `NewJobPage` component wiring them together with local parse/shape checks and a single inline error banner, plus a `/jobs/new` route and a "+ New job" link on `JobsPage`.

**Tech Stack:** React 18, react-router-dom v7, TanStack Query v5, TypeScript, Vitest + Testing Library + MSW. All commands run from the `web/` directory.

---

## Slice independence

**This is a FRONTEND-ONLY slice.** The `POST /v1/jobs` endpoint already exists and is unchanged. There is no backend slice; there is nothing for a backend engineer to do in parallel. All tasks below are frontend and are sequential within the frontend engineer's queue (each task builds on the previous file). No `make generate` step is needed - no `.sql`, `.proto`, or Go files are touched.

## Verified backend contract (do not change any backend file)

Confirmed against `internal/api/server.go` and `internal/api/jobs.go`:

- Route (server.go:115): `mux.Handle("POST /v1/jobs", auth(http.HandlerFunc(s.handleCreateJob)))` - `auth`-only, NOT `admin`-gated. **Do NOT admin-gate the button or the route.** Every authenticated user can create a job.
- Body: JSON, read via `readJSON` (1 MiB cap). Decodes into `createJobRequest` (`name`, `priority`, `labels`, `tasks[]`).
- Success: `201 Created`, body is a `jobResponse` carrying `id` (jobs.go:225). Navigate to `/jobs/${id}`.
- Errors (all `{"error": "<msg>"}`):
  - `400` malformed JSON -> `{"error":"invalid request body"}` (readJSON).
  - `400` spec validation -> `{"error":"<validation message>"}` e.g. `"duplicate task name: build"` (jobs.go:197).
  - `413` -> `{"error":"request body too large"}`.
  - `401` if token missing/invalid; `500` on DB failure.

## CRITICAL correctness points the engineer MUST NOT miss

1. **Do NOT admin-gate.** `POST /v1/jobs` is auth-only. The "+ New job" link and the `/jobs/new` route sit under the existing `ProtectedRoute` and are shown to every logged-in user. No `is_admin` check anywhere in this slice.
2. **Keep client validation MINIMAL.** Only: valid JSON, top-level `name` is a non-empty string, `tasks` is a non-empty array. Do NOT reimplement `jobspec.Validate` in TypeScript (unique names, `command` xor `commands`, dependency cycles, priority enum, source spec). That would create a parallel validation path that drifts from the single job-spec pipeline. Defer everything else to the server and surface the server's `{"error": msg}` inline.
3. **Invalidate BOTH `['jobs']` AND `['job-stats']`.** The stats key is `['job-stats']`, decoupled from the `['jobs']` prefix (proven by `web/src/jobs/queryKeyDecoupling.test.tsx`). A one-key invalidation leaves the KPI strip's queued count stale. Assert the `['job-stats']` refetch with a REAL active observer (mount `useJobStats` via `renderHook` on the shared client, then count MSW hits) - NOT a bare `fetchQuery` seed. A `fetchQuery` seed leaves no observer, so the refetch assertion would be vacuous.
4. **Route collision guard.** `/jobs/new` must render the form, NOT the `JobDetailPage` fetching a job with id `"new"`. React Router v7 ranks static segments above dynamic, so `/jobs/new` wins over `/jobs/:id`. Still, include a test asserting NO `GET /v1/jobs/new` is made and the form renders.
5. **`ApiError.message` format.** `apiFetch` throws `new ApiError(res.status, code, \`${res.status} ${code}\`)` where `code` is the server's `error` string (see `web/src/lib/api.ts:52`). So `create.error.message` for a 400 is `"400 duplicate task name: build"` - the status is prefixed. The banner renders `create.error.message` (matching `JobActions.tsx`). Tests that check the server message MUST assert a substring (`toHaveTextContent(/duplicate task name: build/)`), NOT exact string equality against the bare server message.
6. **`201` carries `id`** -> `navigate('/jobs/' + id)`.
7. **Text preserved on error.** On any client or server error the editor `<textarea>` keeps the user's text. Only navigate (which unmounts the page) on success.
8. **`create.reset()` before re-validating** on every submit, so a stale server error banner clears before the retry.

---

## File Structure

New files:
- `web/src/jobs/specTemplate.ts` - starter template string + `validateSpecText` helper. One responsibility: client-side text -> parsed-or-error.
- `web/src/jobs/specTemplate.test.ts` - unit tests for the helper.
- `web/src/jobs/useCreateJob.ts` - the router-free mutation hook.
- `web/src/jobs/useCreateJob.test.tsx` - hook test (POST body, invalidations, active-observer stats refetch).
- `web/src/jobs/NewJobPage.tsx` - the page component.
- `web/src/jobs/NewJobPage.test.tsx` - page tests (entry link lives in JobsPage.test.tsx; the rest here).

Modified files:
- `web/src/jobs/api.ts` - add `createJob`.
- `web/src/jobs/api.test.ts` (create if absent) - api-client test for `createJob`. (Confirm at implementation time whether an api test file already exists for jobs; if not, create `web/src/jobs/api.test.ts`.)
- `web/src/app/router.tsx` - register `/jobs/new`.
- `web/src/jobs/JobsPage.tsx` - add the "+ New job" link.
- `web/src/jobs/JobsPage.test.tsx` - add the entry-point test.

---

## Task 1: `createJob` api-client function

**Files:**
- Modify: `web/src/jobs/api.ts` (add after `cancelJob`, around line 133)
- Test: `web/src/jobs/api.test.ts` (create; if a jobs api test file already exists, append there)

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/api.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { createJob } from './api'

test('createJob POSTs the spec to /v1/jobs and returns the created job', async () => {
  let body: unknown = null
  let method = ''
  server.use(
    http.post('/v1/jobs', async ({ request }) => {
      method = request.method
      body = await request.json()
      return HttpResponse.json(
        { id: 'job-123', name: 'my-job', status: 'pending' },
        { status: 201 },
      )
    }),
  )

  const spec = { name: 'my-job', tasks: [{ name: 'hello', command: ['echo', 'hi'] }] }
  const job = await createJob(spec)

  expect(method).toBe('POST')
  expect(body).toEqual(spec)
  expect(job.id).toBe('job-123')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `web/`): `npx vitest run src/jobs/api.test.ts`
Expected: FAIL - `createJob` is not exported from `./api`.

- [ ] **Step 3: Write minimal implementation**

Append to `web/src/jobs/api.ts` (after `cancelJob`, keep the existing `apiFetch` import):

```ts
// Creates a job from a raw parsed job-spec object. The client keeps the spec
// type permissive (unknown) and posts it verbatim; the server (ValidateJobSpec)
// is the validator of record, so new TaskSpec fields need no client change. The
// 201 body is a jobResponse; JobDetail is the closest existing type and carries
// the `id` the caller navigates to.
export function createJob(spec: unknown): Promise<JobDetail> {
  return apiFetch<JobDetail>('/jobs', { method: 'POST', json: spec })
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/jobs/api.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/api.ts web/src/jobs/api.test.ts
git commit -m "feat(web): add createJob api-client function"
```

---

## Task 2: `specTemplate.ts` - starter template + `validateSpecText`

**Files:**
- Create: `web/src/jobs/specTemplate.ts`
- Test: `web/src/jobs/specTemplate.test.ts`

The helper returns a discriminated result: `{ ok: true, value }` or `{ ok: false, error }`. Checks, in order: `JSON.parse` (parse error), then `name` is a non-empty string, then `tasks` is a non-empty array. Nothing deeper - the server validates the rest.

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/specTemplate.test.ts`:

```ts
import { expect, test } from 'vitest'
import { STARTER_TEMPLATE, validateSpecText } from './specTemplate'

test('the starter template is valid JSON with a name and a non-empty tasks array', () => {
  const r = validateSpecText(STARTER_TEMPLATE)
  expect(r.ok).toBe(true)
  if (r.ok) {
    expect(r.value).toMatchObject({ name: 'my-job' })
    expect(Array.isArray((r.value as { tasks: unknown[] }).tasks)).toBe(true)
    expect((r.value as { tasks: unknown[] }).tasks.length).toBeGreaterThan(0)
  }
})

test('a valid minimal spec passes', () => {
  const r = validateSpecText('{"name":"x","tasks":[{"name":"t","command":["echo"]}]}')
  expect(r.ok).toBe(true)
})

test('malformed JSON fails with an Invalid JSON message', () => {
  const r = validateSpecText('{ not json }')
  expect(r.ok).toBe(false)
  if (!r.ok) expect(r.error).toMatch(/Invalid JSON/)
})

test('missing name fails with a targeted message', () => {
  const r = validateSpecText('{"tasks":[{"name":"t","command":["echo"]}]}')
  expect(r.ok).toBe(false)
  if (!r.ok) expect(r.error).toMatch(/name/i)
})

test('empty name fails', () => {
  const r = validateSpecText('{"name":"","tasks":[{"name":"t"}]}')
  expect(r.ok).toBe(false)
  if (!r.ok) expect(r.error).toMatch(/name/i)
})

test('empty tasks array fails with a targeted message', () => {
  const r = validateSpecText('{"name":"x","tasks":[]}')
  expect(r.ok).toBe(false)
  if (!r.ok) expect(r.error).toMatch(/task/i)
})

test('missing tasks fails', () => {
  const r = validateSpecText('{"name":"x"}')
  expect(r.ok).toBe(false)
  if (!r.ok) expect(r.error).toMatch(/task/i)
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/jobs/specTemplate.test.ts`
Expected: FAIL - module `./specTemplate` not found.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/jobs/specTemplate.ts`:

```ts
// Prefilled starter spec: a minimal, valid, single-task job the user edits. An
// unedited submit succeeds and demonstrates the POST /v1/jobs shape. Uses the
// single `command` form and omits optional fields on purpose.
export const STARTER_TEMPLATE = `{
  "name": "my-job",
  "priority": "normal",
  "tasks": [
    {
      "name": "hello",
      "command": ["echo", "hello world"]
    }
  ]
}
`

export type SpecCheck =
  | { ok: true; value: unknown }
  | { ok: false; error: string }

// Minimal client-side pre-check. Deliberately shallow: valid JSON, a non-empty
// string `name`, and a non-empty `tasks` array. Deeper rules (unique task names,
// command xor commands, dependency cycles, priority enum, source) are left to
// the server (jobspec.Validate) so the two paths cannot drift.
export function validateSpecText(text: string): SpecCheck {
  let value: unknown
  try {
    value = JSON.parse(text)
  } catch (e) {
    return { ok: false, error: `Invalid JSON: ${(e as Error).message}` }
  }

  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return { ok: false, error: 'Spec must be a JSON object.' }
  }
  const obj = value as Record<string, unknown>

  if (typeof obj.name !== 'string' || obj.name.trim() === '') {
    return { ok: false, error: 'Spec is missing a non-empty "name".' }
  }
  if (!Array.isArray(obj.tasks) || obj.tasks.length === 0) {
    return { ok: false, error: 'Spec must have a non-empty "tasks" array.' }
  }

  return { ok: true, value }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/jobs/specTemplate.test.ts`
Expected: PASS (all 7 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/specTemplate.ts web/src/jobs/specTemplate.test.ts
git commit -m "feat(web): add job-spec starter template and minimal validateSpecText"
```

---

## Task 3: `useCreateJob` mutation hook

**Files:**
- Create: `web/src/jobs/useCreateJob.ts`
- Test: `web/src/jobs/useCreateJob.test.tsx`

The hook is router-free: it takes no `navigate`. It invalidates `['jobs']` and `['job-stats']` on success and returns the mutation. The page owns navigation in its own `onSuccess`.

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/useCreateJob.test.tsx`. Note the stats-refetch test mounts `useJobStats` as a REAL active observer (renderHook) on the shared client, then counts MSW hits after the create - a `fetchQuery` seed would leave no observer and make the assertion vacuous.

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useCreateJob } from './useCreateJob'
import { useJobStats } from './useJobStats'

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('createJob POSTs the spec and returns the created job', async () => {
  const client = newClient()
  let body: unknown = null
  server.use(
    http.post('/v1/jobs', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'job-1', name: 'my-job', status: 'pending' }, { status: 201 })
    }),
  )
  const { result } = renderHook(() => useCreateJob(), { wrapper: makeWrapper(client) })
  const spec = { name: 'my-job', tasks: [{ name: 't', command: ['echo'] }] }
  const job = await result.current.mutateAsync(spec)

  expect(body).toEqual(spec)
  expect(job.id).toBe('job-1')
})

test('onSuccess invalidates BOTH ["jobs"] and ["job-stats"]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/jobs', () =>
      HttpResponse.json({ id: 'job-1', name: 'my-job', status: 'pending' }, { status: 201 }),
    ),
  )
  const { result } = renderHook(() => useCreateJob(), { wrapper: makeWrapper(client) })
  await result.current.mutateAsync({ name: 'my-job', tasks: [{ name: 't' }] })

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['jobs'] }))
  // The decoupled stats key MUST be invalidated explicitly; ['jobs'] alone does
  // not reach ['job-stats'] (see queryKeyDecoupling.test.tsx).
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['job-stats'] }))
})

test('the create refetches an ACTIVE ["job-stats"] observer', async () => {
  const client = newClient()
  let statsCalls = 0
  const bigInterval = 100_000 // never auto-refetch during the test
  server.use(
    http.get('/v1/jobs/stats', () => {
      statsCalls++
      return HttpResponse.json({ running: 0, queued: 0, done_24h: 0, failed_24h: 0 })
    }),
    http.post('/v1/jobs', () =>
      HttpResponse.json({ id: 'job-1', name: 'my-job', status: 'pending' }, { status: 201 }),
    ),
  )
  const wrapper = makeWrapper(client)

  // Mount a REAL stats observer so an invalidation triggers a refetch. A bare
  // fetchQuery seed would leave no observer and make the refetch un-observable.
  const stats = renderHook(() => useJobStats(bigInterval), { wrapper })
  await waitFor(() => expect(stats.result.current.status).toBe('success'))
  expect(statsCalls).toBe(1)

  const create = renderHook(() => useCreateJob(), { wrapper })
  await create.result.current.mutateAsync({ name: 'my-job', tasks: [{ name: 't' }] })

  // The active observer must refetch on invalidation: at least 2 total hits.
  await waitFor(() => expect(statsCalls).toBeGreaterThanOrEqual(2))
})

test('a failed create rejects and does not invalidate', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/jobs', () =>
      HttpResponse.json({ error: 'duplicate task name: build' }, { status: 400 }),
    ),
  )
  const { result } = renderHook(() => useCreateJob(), { wrapper: makeWrapper(client) })

  await expect(
    result.current.mutateAsync({ name: 'x', tasks: [{ name: 't' }] }),
  ).rejects.toBeTruthy()
  expect(spy).not.toHaveBeenCalledWith({ queryKey: ['jobs'] })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/jobs/useCreateJob.test.tsx`
Expected: FAIL - module `./useCreateJob` not found.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/jobs/useCreateJob.ts`:

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createJob } from './api'

// Create-job mutation. Router-free by design: the page owns navigation in its
// own onSuccess. On success it invalidates TWO keys:
//   - ['jobs']       (bare prefix) so every list view ['jobs', sort, status,
//     cursor] refetches and the new job appears.
//   - ['job-stats']  MUST be explicit; it is decoupled from ['jobs'] (see
//     queryKeyDecoupling.test.tsx), so ['jobs'] alone leaves the KPI strip stale.
// There is NO ['job', id] to invalidate: the job is brand new and not yet cached.
// No optimistic update.
export function useCreateJob() {
  const qc = useQueryClient()

  return useMutation({
    mutationFn: (spec: unknown) => createJob(spec),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['jobs'] })
      qc.invalidateQueries({ queryKey: ['job-stats'] })
    },
  })
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/jobs/useCreateJob.test.tsx`
Expected: PASS (all 4 tests, including the active-observer stats refetch).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/useCreateJob.ts web/src/jobs/useCreateJob.test.tsx
git commit -m "feat(web): add useCreateJob hook invalidating jobs and job-stats"
```

---

## Task 4: `NewJobPage` component

**Files:**
- Create: `web/src/jobs/NewJobPage.tsx`
- Test: `web/src/jobs/NewJobPage.test.tsx`

The page: prefilled `<textarea>`, a submit button, a single inline error banner. On submit: `create.reset()`, then `validateSpecText(text)`; on `{ok:false}` show the error and make NO request; on `{ok:true}` call `create.mutate(value)`. The page passes its own `onSuccess` to `mutate` to navigate to `/jobs/${job.id}`. Button disabled while `create.isPending`. Banner shows the client error OR `create.error.message`. Text is preserved on any error (only success navigates away).

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/NewJobPage.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, delay } from 'msw'
import { expect, test } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { NewJobPage } from './NewJobPage'

// Renders NewJobPage at /jobs/new. A stub /jobs/:id route lets us assert
// navigation lands on the detail page for a real id (and prove /jobs/new does
// NOT match :id).
function renderNew() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/jobs/new']}>
        <Routes>
          <Route path="/jobs/new" element={<NewJobPage />} />
          <Route path="/jobs/:id" element={<div>detail for {useIdEcho()}</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// Tiny helper component body inlined via a wrapper so :id renders visibly.
import { useParams } from 'react-router-dom'
function useIdEcho() {
  const { id } = useParams()
  return id ?? ''
}

function editor() {
  return screen.getByRole('textbox') as HTMLTextAreaElement
}

test('renders the editor prefilled with the starter template', () => {
  renderNew()
  expect(editor().value).toMatch(/"name": "my-job"/)
  expect(editor().value).toMatch(/"hello world"/)
})

test('submitting the unedited template POSTs that body', async () => {
  let body: unknown = null
  server.use(
    http.post('/v1/jobs', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'job-1' }, { status: 201 })
    }),
  )
  renderNew()
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))
  await waitFor(() => expect(body).toMatchObject({ name: 'my-job' }))
  expect((body as { tasks: unknown[] }).tasks.length).toBe(1)
})

test('happy path: POST body, 201, navigation to /jobs/:id', async () => {
  let body: unknown = null
  server.use(
    http.post('/v1/jobs', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'job-123' }, { status: 201 })
    }),
  )
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{"name":"nj","tasks":[{{"name":"t","command":["echo"]}]}')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))

  expect(await screen.findByText('detail for job-123')).toBeInTheDocument()
  expect(body).toEqual({ name: 'nj', tasks: [{ name: 't', command: ['echo'] }] })
})

test('local parse error shows a banner and makes NO POST', async () => {
  let posted = false
  server.use(http.post('/v1/jobs', () => { posted = true; return HttpResponse.json({ id: 'x' }, { status: 201 }) }))
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{ not json }')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))

  expect(await screen.findByText(/Invalid JSON/)).toBeInTheDocument()
  expect(posted).toBe(false)
})

test('local shape error - missing name - banner and NO POST', async () => {
  let posted = false
  server.use(http.post('/v1/jobs', () => { posted = true; return HttpResponse.json({ id: 'x' }, { status: 201 }) }))
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{"tasks":[{{"name":"t"}]}')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))

  expect(await screen.findByText(/name/i)).toBeInTheDocument()
  expect(posted).toBe(false)
})

test('local shape error - empty tasks - banner and NO POST', async () => {
  let posted = false
  server.use(http.post('/v1/jobs', () => { posted = true; return HttpResponse.json({ id: 'x' }, { status: 201 }) }))
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{"name":"x","tasks":[]}')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))

  expect(await screen.findByText(/task/i)).toBeInTheDocument()
  expect(posted).toBe(false)
})

test('server 400 surfaces inline, no navigation, text preserved', async () => {
  server.use(
    http.post('/v1/jobs', () =>
      HttpResponse.json({ error: 'duplicate task name: build' }, { status: 400 }),
    ),
  )
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{"name":"nj","tasks":[{{"name":"t","command":["echo"]}]}')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))

  // ApiError.message is "400 duplicate task name: build" - assert the substring.
  expect(await screen.findByText(/duplicate task name: build/)).toBeInTheDocument()
  expect(screen.queryByText(/^detail for/)).not.toBeInTheDocument()
  expect(editor().value).toContain('"name":"nj"')
})

test('413 oversize surfaces inline (same banner path)', async () => {
  server.use(
    http.post('/v1/jobs', () =>
      HttpResponse.json({ error: 'request body too large' }, { status: 413 }),
    ),
  )
  renderNew()
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))
  expect(await screen.findByText(/request body too large/)).toBeInTheDocument()
})

test('submit button is disabled while the create is pending', async () => {
  server.use(
    http.post('/v1/jobs', async () => {
      await delay(50)
      return HttpResponse.json({ id: 'job-1' }, { status: 201 })
    }),
  )
  renderNew()
  const btn = screen.getByRole('button', { name: /create job/i })
  await userEvent.click(btn)
  await waitFor(() => expect(btn).toBeDisabled())
})

test('a stale server error clears on the next submit', async () => {
  let call = 0
  server.use(
    http.post('/v1/jobs', () => {
      call++
      if (call === 1) return HttpResponse.json({ error: 'duplicate task name: build' }, { status: 400 })
      return HttpResponse.json({ id: 'job-9' }, { status: 201 })
    }),
  )
  renderNew()
  const ta = editor()
  await userEvent.clear(ta)
  await userEvent.type(ta, '{{"name":"nj","tasks":[{{"name":"t","command":["echo"]}]}')
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))
  expect(await screen.findByText(/duplicate task name: build/)).toBeInTheDocument()

  // Resubmit (same valid text); the second POST 201s.
  await userEvent.click(screen.getByRole('button', { name: /create job/i }))
  expect(await screen.findByText('detail for job-9')).toBeInTheDocument()
  expect(screen.queryByText(/duplicate task name: build/)).not.toBeInTheDocument()
})
```

Note on the collision guard: it is exercised structurally here (the `Routes` maps `/jobs/new` to `NewJobPage`) and asserted head-on in Task 5's router test (no `GET /v1/jobs/new`). MSW runs with `onUnhandledRequest: 'error'`, so any accidental `GET /v1/jobs/new` fails the suite regardless.

Note on `userEvent.type` and braces: `userEvent.type` treats `{` and `[` specially, so `{{` types a literal `{`. The test strings above double the opening braces to emit literal JSON; keep that when authoring.

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/jobs/NewJobPage.test.tsx`
Expected: FAIL - module `./NewJobPage` not found.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/jobs/NewJobPage.tsx`:

```tsx
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Button } from '../components/Button'
import { useCreateJob } from './useCreateJob'
import { STARTER_TEMPLATE, validateSpecText } from './specTemplate'

// Dedicated /jobs/new page: a JSON job-spec editor that POSTs to /v1/jobs and,
// on 201, navigates to the created job's detail page. Creation is auth-only, so
// this is available to every logged-in user (no admin gate).
export function NewJobPage() {
  const navigate = useNavigate()
  const create = useCreateJob()
  const [text, setText] = useState(STARTER_TEMPLATE)
  // Client-side parse/shape error. Server errors come from create.error.
  const [clientError, setClientError] = useState<string | null>(null)

  function onSubmit() {
    // Clear a stale server error before re-validating (matches JobActions).
    create.reset()
    setClientError(null)

    const check = validateSpecText(text)
    if (!check.ok) {
      setClientError(check.error)
      return
    }
    create.mutate(check.value, {
      onSuccess: (job) => navigate(`/jobs/${job.id}`),
    })
  }

  // One banner slot for both sources; client error takes precedence since it is
  // set on the current submit and a stale server error was just reset.
  const bannerMessage = clientError ?? (create.error as Error | null)?.message ?? null

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <Link to="/jobs" className="font-mono text-[11px] text-fg-mute hover:text-fg">
          &larr; Jobs
        </Link>
        <h1 className="text-[28px] font-normal tracking-tight">New job</h1>
        <p className="font-mono text-[11px] text-fg-mute">
          Author a job-spec as JSON (the same shape <code>relay submit</code> accepts).
          Fields: name, priority, labels, tasks[] (name + command/commands, env,
          requires, timeout_seconds, retries, depends_on, source).
        </p>
      </div>

      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        spellCheck={false}
        aria-label="Job spec JSON"
        className="min-h-[360px] w-full rounded-card border border-border bg-white/5 p-3 font-mono text-[12px] text-fg"
      />

      {bannerMessage ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {bannerMessage}
        </div>
      ) : null}

      <div>
        <Button className="w-auto px-4" onClick={onSubmit} disabled={create.isPending}>
          Create job
        </Button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/jobs/NewJobPage.test.tsx`
Expected: PASS (all 11 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/NewJobPage.tsx web/src/jobs/NewJobPage.test.tsx
git commit -m "feat(web): add NewJobPage JSON job-spec editor with inline errors"
```

---

## Task 5: Register `/jobs/new` route + collision guard test

**Files:**
- Modify: `web/src/app/router.tsx:6` (import) and `:21-22` (add route adjacent to the other `/jobs` routes)
- Test: `web/src/jobs/NewJobPage.test.tsx` (append the router/collision test - it needs `AppRoutes`)

The route is declared adjacent to `/jobs` and `/jobs/:id` under `ProtectedRoute`. React Router v7 ranks static over dynamic, so `/jobs/new` wins regardless of order; declaring it next to the others keeps the file readable. The collision test asserts NO `GET /v1/jobs/new` fires.

- [ ] **Step 1: Write the failing test**

Append to `web/src/jobs/NewJobPage.test.tsx` (add `AppRoutes`, `AuthProvider`, and token helpers to the imports at the top of the file):

```tsx
// --- appended imports (merge into the existing import block) ---
// import { AppRoutes } from '../app/router'
// import { AuthProvider } from '../auth/AuthProvider'
// import { setToken, clearToken } from '../lib/token'
// import { afterEach } from 'vitest'

test('the /jobs/new route renders the form and makes NO GET /v1/jobs/new', async () => {
  setToken('test-token')
  let detailFetched = false
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: false }),
    ),
    // If JobDetailPage wrongly matched, it would GET /v1/jobs/new. Record it.
    http.get('/v1/jobs/new', () => {
      detailFetched = true
      return HttpResponse.json({ id: 'new' })
    }),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/jobs/new']}>
        <AuthProvider>
          <AppRoutes />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )

  // The editor renders (proves the form matched, not the detail page).
  expect(await screen.findByRole('textbox')).toBeInTheDocument()
  expect(detailFetched).toBe(false)
  clearToken()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/jobs/NewJobPage.test.tsx -t "makes NO GET"`
Expected: FAIL - `/jobs/new` is not registered, so `AppRoutes` falls through to `/jobs/:id` (`JobDetailPage`), which fires `GET /v1/jobs/new`, flipping `detailFetched` (and no `textbox` renders). The test fails.

- [ ] **Step 3: Write minimal implementation**

Modify `web/src/app/router.tsx`. Add the import next to the other jobs imports:

```tsx
import { NewJobPage } from '../jobs/NewJobPage'
```

Add the route inside the `ProtectedRoute` block, adjacent to the other `/jobs` routes (place it between `/jobs` and `/jobs/:id`):

```tsx
        <Route path="/jobs" element={<JobsPage />} />
        <Route path="/jobs/new" element={<NewJobPage />} />
        <Route path="/jobs/:id" element={<JobDetailPage />} />
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/jobs/NewJobPage.test.tsx`
Expected: PASS (all tests including the collision guard).

- [ ] **Step 5: Commit**

```bash
git add web/src/app/router.tsx web/src/jobs/NewJobPage.test.tsx
git commit -m "feat(web): register /jobs/new route with a collision-guard test"
```

---

## Task 6: "+ New job" entry-point link on `JobsPage`

**Files:**
- Modify: `web/src/jobs/JobsPage.tsx` - import `Link`, add the link in the `ml-auto` header cluster (JobsPage.tsx:125-129)
- Test: `web/src/jobs/JobsPage.test.tsx` - add the entry-point test

The link is a `react-router` `<Link to="/jobs/new">` styled as the accent button, placed in the top header row's `ml-auto` cluster (which currently holds only the "live / auto-refreshing" indicator). It is shown to every authenticated user - no admin gate.

- [ ] **Step 1: Write the failing test**

Append to `web/src/jobs/JobsPage.test.tsx` (imports `screen`, `MemoryRouter`, `renderPage`, `page`, `stats` already exist in that file; the `beforeEach` already stubs `/v1/jobs/stats`):

```tsx
test('shows a "+ New job" link to /jobs/new for any authenticated user', async () => {
  server.use(http.get('/v1/jobs', () => HttpResponse.json(page)))
  renderPage()
  await screen.findByText('film-x render')
  const link = screen.getByRole('link', { name: /new job/i })
  expect(link).toHaveAttribute('href', '/jobs/new')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/jobs/JobsPage.test.tsx -t "New job"`
Expected: FAIL - no link with an accessible name matching `/new job/i` exists.

- [ ] **Step 3: Write minimal implementation**

In `web/src/jobs/JobsPage.tsx`, add `Link` to the react-router import (add this import line near the top; there is currently no react-router import in the file):

```tsx
import { Link } from 'react-router-dom'
```

Replace the `ml-auto` header cluster (currently JobsPage.tsx:125-129) with one that also holds the link:

```tsx
        <div className="ml-auto flex items-center gap-3">
          <span className="font-mono text-[10px] text-fg-mute">
            <span className={isFetching ? 'text-ok' : 'text-fg-dim'}>●</span> live · auto-refreshing
          </span>
          <Link
            to="/jobs/new"
            className="rounded-[8px] bg-accent px-3 py-2 text-[13px] font-medium text-bg transition hover:bg-accent-b"
          >
            + New job
          </Link>
        </div>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/jobs/JobsPage.test.tsx`
Expected: PASS (the new test plus all existing JobsPage tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/JobsPage.tsx web/src/jobs/JobsPage.test.tsx
git commit -m "feat(web): add + New job entry-point link to the jobs list header"
```

---

## Task 7: Full suite, typecheck, and lint

**Files:** none (verification only).

- [ ] **Step 1: Run the full web test suite**

Run (from `web/`): `npx vitest run`
Expected: PASS - all tests, no regressions in existing jobs/router tests.

- [ ] **Step 2: Typecheck and lint**

Run (from `web/`): `npm run build` (or the repo's typecheck script, e.g. `npx tsc --noEmit`) and `npm run lint` if defined in `web/package.json`.
Expected: no type errors, no lint errors. If `web/dist` is dirtied by a build, discard it with `git checkout -- web/dist/` before committing (web/dist is not maintained per-PR).

- [ ] **Step 3: Commit (only if any lint/type fixes were needed)**

```bash
git add web/src
git commit -m "chore(web): satisfy typecheck and lint for the new-job flow"
```

---

## Self-review against the spec

- Spec section "Entry point" -> Task 6 (link, non-admin, `to="/jobs/new"`).
- Spec "API client" -> Task 1 (`createJob(spec: unknown): Promise<JobDetail>`, permissive type, verbatim POST).
- Spec "Mutation hook" -> Task 3 (router-free, invalidates `['jobs']` + `['job-stats']`, no `['job', id]`, no optimistic update).
- Spec "Editor and client-side validation" -> Task 2 (`STARTER_TEMPLATE`, `validateSpecText`: parse -> name -> tasks; nothing deeper) + Task 4 (page runs it before mutate).
- Spec "Submit and error handling" -> Task 4 (single banner, `create.reset()` before re-validate, pending disables button, `create.error.message` shown).
- Spec "Success navigation" -> Task 4 (`navigate('/jobs/' + job.id)` in the page's `onSuccess`).
- Spec "Route (not modal)" and collision caveat -> Task 5 (route registered, adjacency comment, no-GET assertion).
- Spec "Permission gating: none beyond auth" -> Tasks 5 and 6 (ProtectedRoute only, no admin gate).
- Spec test plan (10 cases): entry link (T6), collision guard (T5), prefilled+unedited submit (T4), happy path with both invalidations + active-observer stats (T3 + T4), local parse error no-POST (T4), local shape errors missing-name/empty-tasks no-POST (T4), server 400 inline + no-nav + text preserved (T4), 413 inline (T4), pending disables button (T4), error reset on resubmit (T4). All covered.
- Invariants: single job-spec pipeline preserved (permissive client type, server is validator of record; Task 2 explicitly refuses to port `jobspec.Validate`); no backend files touched; no `make generate` needed.

Placeholder scan: none - every code step contains complete code. Type consistency: `createJob(spec: unknown): Promise<JobDetail>`, `validateSpecText -> SpecCheck { ok, value|error }`, `STARTER_TEMPLATE`, and `useCreateJob()` names are identical across all tasks that reference them.
