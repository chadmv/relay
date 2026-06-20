# SPA 401 Redirect-Loop Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop the SPA redirect loop on session expiry by resetting auth state inside `AuthProvider` when any request returns 401, so the existing route guards land the user on the sign-in screen with no bounce-back.

**Architecture:** Move the single `onUnauthorized` subscription out of `App.tsx`'s `UnauthorizedRedirect` and into `AuthProvider`, where the `setUser`/`setStatus` setters and `clearToken()` live. The handler clears the token, nulls the user, flips `status` to `anonymous`, and clears the React Query cache via `useQueryClient()`, guarded to no-op when already anonymous. With `status === 'anonymous'`, `ProtectedRoute` redirects to `/auth` and `PublicOnlyRoute` shows the sign-in `<Outlet>` - no flicker back to `/jobs`.

**Tech Stack:** React 18, react-router-dom v7, @tanstack/react-query v5, vitest v2 + @testing-library/react + msw v2.

---

## Slice declaration

**FRONTEND-ONLY. Single `relay-frontend-engineer` slice. Phase 3 is NOT parallel.**

No backend, SQL, proto, or Go change. There is no new server endpoint and no
dependency on one. All changes live under `web/`. The conductor must run this as
one sequential slice.

## Scope and boundaries

Files that change (surgical - per CLAUDE.md, touch only these):

- `web/src/auth/AuthProvider.tsx` - add the `onUnauthorized` subscription + reset.
- `web/src/App.tsx` - delete `UnauthorizedRedirect` and its now-unused imports.
- `web/src/App.test.tsx` - add the failing-then-passing regression test (the
  spec names `App.test.tsx` as the preferred home because it drives the real
  route guards through `<App />`, proving the absence of a loop).

No change to `web/src/lib/api.ts` (the `onUnauthorized` publisher already exposes
a `Set`-backed subscribe/unsubscribe that is exactly what we need),
`web/src/lib/queryClient.ts`, `web/src/lib/token.ts`,
`web/src/app/PublicOnlyRoute.tsx`, or `web/src/app/ProtectedRoute.tsx`. The guards
already do the right thing once `status` flips to `anonymous`
(`ProtectedRoute.tsx:8`, `PublicOnlyRoute.tsx:9`). No new test helper is required:
`web/src/test/setup-helpers.ts` already re-exports the shared msw `server`, and
`setToken`/`getToken`/`clearToken` already exist in `web/src/lib/token.ts`.

If, mid-implementation, you find you genuinely must change `api.ts` or a test
helper, stop and justify it - the spec asserts none is needed.

## Out of scope (do not touch)

- **`bug-2026-06-10-spa-query-cache-logout`** overlaps (it wants
  `queryClient.clear()` in the manual `logout()` path). That is a separate code
  path. Do NOT modify `logout()` here and do NOT close or edit that backlog item.
  After this fix lands it is reduced to "add `queryClient.clear()` to `logout()`"
  and stays open.
- **`LoginResponse.expires_at` proactive expiry.** Received but unused; out of
  scope. 401 remains the only expiry path for this fix.

## Verify commands (frontend)

Discovered from `web/package.json` (`"test": "vitest run"`, `"build": "tsc -b && vite build"`):

- Single test (red/green loop): `npm --prefix web run test -- App.test.tsx`
- Full suite: `npm --prefix web run test`
- Typecheck + build (catches removed-import / unused-symbol errors that `tsc -b`
  flags): `npm --prefix web run build`

**Browser-preview gate (required).** The auth slice has shipped bugs past unit
tests before, so a green suite is necessary but not sufficient. Before declaring
done, the implementer MUST run `npm --prefix web run dev`, sign in against a live
`relay-server`, force a 401 (revoke/expire the token server-side, or temporarily
make an endpoint return 401), and visually confirm: the UI lands on the sign-in
form, the URL settles on `/auth` with no flicker, and a fresh login works without
a hard reload.

---

## Task 1: Failing regression test - 401 on a polling session lands on sign-in with no loop

**Files:**

- Test: `web/src/App.test.tsx` (append a new test; do not edit the two existing tests)

This task writes the test FIRST and confirms it goes red against the current
(broken) `UnauthorizedRedirect`-only code, reproducing the loop.

- [ ] **Step 1: Write the failing test**

Append this test to `web/src/App.test.tsx`. It seeds an authenticated session,
lets the app reach the jobs page (`OVERVIEW`), flips every relevant handler to
401, advances fake timers past the 3000ms jobs poll to fire the next request, and
asserts the sign-in screen renders, the token is cleared, and the authenticated
marker does not reappear (no loop).

```tsx
import { vi } from 'vitest'
import { setToken, getToken } from './lib/token'

test('a 401 on the next poll lands an authenticated session on sign-in with no loop', async () => {
  const ME = { id: '1', email: 'admin@example.com', name: 'Admin', is_admin: true }
  // Phase 1: authenticated. Hydrate the user and serve empty jobs so we reach OVERVIEW.
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.get('/v1/jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ running: 0, queued: 0, done_24h: 0, failed_24h: 0 }),
    ),
  )
  setToken('tok')
  render(<App />)
  expect(await screen.findByText('OVERVIEW')).toBeInTheDocument()

  // Phase 2: the session expires. Every endpoint now 401s.
  const unauthorized = () =>
    HttpResponse.json({ error: 'unauthorized' }, { status: 401 })
  server.use(
    http.get('/v1/users/me', unauthorized),
    http.get('/v1/jobs', unauthorized),
    http.get('/v1/jobs/stats', unauthorized),
  )

  // Fire the next jobs poll (useJobs default refetchInterval is 3000ms).
  vi.useFakeTimers()
  await vi.advanceTimersByTimeAsync(3100)
  vi.useRealTimers()

  // The 401 handler must reset auth state: sign-in renders, token gone.
  await waitFor(() =>
    expect(screen.getByText(/sign in to the coordinator/i)).toBeInTheDocument(),
  )
  expect(getToken()).toBeNull()

  // No loop: the authenticated marker must not bounce back.
  expect(screen.queryByText('OVERVIEW')).not.toBeInTheDocument()
  await new Promise((r) => setTimeout(r, 50))
  expect(screen.queryByText('OVERVIEW')).not.toBeInTheDocument()
})
```

Note on imports: `render`, `screen`, `waitFor`, `http`, `HttpResponse`, `server`,
`test`, `expect`, and `clearToken` are already imported at the top of
`App.test.tsx`. Add `import { vi } from 'vitest'` and extend the existing
`./lib/token` import to also bring in `setToken` and `getToken` (the file already
imports `clearToken` from `./lib/token`).

- [ ] **Step 2: Run the test to verify it FAILS (reproduces the loop)**

Run: `npm --prefix web run test -- App.test.tsx`

Expected: FAIL. With the current `UnauthorizedRedirect`-only code, the 401 fires
`navigate('/auth')` but `status` stays `authenticated`, so `PublicOnlyRoute`
immediately redirects back to `/jobs`. The assertion `getByText(/sign in to the
coordinator/i)` fails (or `OVERVIEW` is still present / reappears), demonstrating
the redirect loop in red.

- [ ] **Step 3: Commit the red test**

```bash
git add web/src/App.test.tsx
git commit -m "test(web): reproduce the SPA 401 redirect loop (red)"
```

---

## Task 2: Move the 401 reset into AuthProvider

**Files:**

- Modify: `web/src/auth/AuthProvider.tsx:1-44`

- [ ] **Step 1: Add the imports**

Change the top imports of `web/src/auth/AuthProvider.tsx`.

Replace line 1-3:

```tsx
import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { apiFetch } from '../lib/api'
import { clearToken, getToken, setToken } from '../lib/token'
```

with (add `useRef`, `useQueryClient`, and `onUnauthorized`):

```tsx
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { apiFetch, onUnauthorized } from '../lib/api'
import { clearToken, getToken, setToken } from '../lib/token'
```

- [ ] **Step 2: Add the query client, a status ref, and the 401 subscription**

Inside `AuthProvider`, just below the two `useState` lines
(`web/src/auth/AuthProvider.tsx:26-27`):

```tsx
  const [status, setStatus] = useState<Status>('loading')
  const [user, setUser] = useState<User | null>(null)
  const queryClient = useQueryClient()

  // Mirror status in a ref so the 401 subscription can read the latest value
  // without re-subscribing. The effect below mounts once for the provider's life.
  const statusRef = useRef(status)
  statusRef.current = status

  // Reset auth state on any 401 so the route guards send the user to sign-in.
  // Guarded to no-op when already anonymous: a failed login still on the sign-in
  // screen must not churn state or clear an empty cache on every request.
  useEffect(
    () =>
      onUnauthorized(() => {
        if (statusRef.current === 'anonymous') return
        clearToken()
        setUser(null)
        setStatus('anonymous')
        queryClient.clear()
      }),
    [queryClient],
  )
```

Rationale for the ref (per the spec's "ref or functional state read" choice): the
effect must subscribe exactly once for the provider's lifetime so the `Set`-based
unsubscribe in `onUnauthorized` runs cleanly on unmount and there is no double
subscribe. A `statusRef` lets the stable closure read the current `status`
without listing it in the dependency array. `queryClient` is stable (created once
in `App.tsx`), so listing it does not re-subscribe in practice.

- [ ] **Step 3: Run the new test to verify it PASSES**

Run: `npm --prefix web run test -- App.test.tsx`

Expected: PASS. The 401 now clears the token, sets `status` to `anonymous`,
`ProtectedRoute` redirects to `/auth`, `PublicOnlyRoute` renders the sign-in
`<Outlet>`, and `OVERVIEW` does not reappear.

- [ ] **Step 4: Commit**

```bash
git add web/src/auth/AuthProvider.tsx
git commit -m "fix(web): reset auth state in AuthProvider on 401 (green)"
```

---

## Task 3: Delete UnauthorizedRedirect from App.tsx

**Files:**

- Modify: `web/src/App.tsx:1-26`

The subscription now lives in `AuthProvider`, so `UnauthorizedRedirect` is dead
and its imports are unused. `tsc -b` (via `npm run build`) treats unused imports
as errors, so they must be removed.

- [ ] **Step 1: Rewrite `App.tsx` without `UnauthorizedRedirect`**

Replace the entire contents of `web/src/App.tsx` with:

```tsx
import { BrowserRouter } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { AuthProvider } from './auth/AuthProvider'
import { queryClient } from './lib/queryClient'
import { AppRoutes } from './app/router'

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <AuthProvider>
          <AppRoutes />
        </AuthProvider>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
```

Removed vs. the original: the `UnauthorizedRedirect` component, its
`<UnauthorizedRedirect />` usage, and the now-unused imports `useEffect` (from
`react`), `useNavigate` (from `react-router-dom`), and `onUnauthorized` (from
`./lib/api`). Kept: `queryClient` (still passed to `QueryClientProvider`) and
`BrowserRouter`.

- [ ] **Step 2: Run the full suite to verify nothing regressed**

Run: `npm --prefix web run test`

Expected: PASS for the whole `web` suite, including the new test, the two
existing `App.test.tsx` tests, `AuthProvider.test.tsx`, and
`ProtectedRoute.test.tsx`.

- [ ] **Step 3: Typecheck and build to confirm no unused-import errors**

Run: `npm --prefix web run build`

Expected: clean `tsc -b` + `vite build` with no "declared but never read"
errors for `useEffect`, `useNavigate`, or `onUnauthorized`.

- [ ] **Step 4: Commit**

```bash
git add web/src/App.tsx
git commit -m "refactor(web): drop dead UnauthorizedRedirect from App"
```

---

## Task 4: Browser-preview verification (manual gate)

**Files:** none (verification only).

- [ ] **Step 1: Run the dev server and reproduce expiry**

Run: `npm --prefix web run dev` (with a live `relay-server` reachable on
`http://localhost:8080`; the vite proxy forwards `/v1`).

Sign in, then force a 401 (revoke or expire the token server-side, or temporarily
make an endpoint return 401).

- [ ] **Step 2: Confirm the four success criteria from the spec**

Verify by eye:

1. The UI lands on the sign-in screen.
2. The URL settles on `/auth` and does NOT flicker back to `/jobs`.
3. The token is gone from `localStorage` (`relay.token`), `user` is `null`,
   `status` is `anonymous`.
4. Stale job rows do not linger (cache cleared); a fresh login works without a
   hard reload.

There is no commit for this task - it is a release gate. If any criterion fails,
return to Task 2.

---

## Self-review

- **Spec coverage:**
  - Move subscription into `AuthProvider` with `clearToken`/`setUser(null)`/
    `setStatus('anonymous')`/`queryClient.clear()` via `useQueryClient()` -> Task 2.
  - Anonymous no-op guard -> Task 2 (`statusRef.current === 'anonymous'` early return).
  - Subscribe-once with `Set`-based unsubscribe cleanup -> Task 2 (effect returns
    `onUnauthorized`'s unsubscribe; ref avoids re-subscribe).
  - Delete `UnauthorizedRedirect` and unused imports from `App.tsx` -> Task 3.
  - No change to `api.ts`/`queryClient.ts`/`token.ts`/guards/router -> honored
    (only `AuthProvider.tsx`, `App.tsx`, `App.test.tsx` change).
  - Vitest integration test driving real guards, asserting sign-in renders +
    token cleared + no loop -> Task 1.
  - Full suite stays green -> Task 3 Step 2.
  - Browser-preview check -> Task 4.
  - Overlap note (`spa-query-cache-logout` stays open, not modified) and
    `expires_at` out of scope -> "Out of scope" section.
- **Placeholder scan:** every code step shows the real code; no TODO/TBD.
- **Type consistency:** `status`/`setStatus`/`setUser`/`statusRef`/`queryClient`/
  `onUnauthorized`/`clearToken`/`getToken`/`setToken` names match across tasks
  and the actual source files read during planning.
