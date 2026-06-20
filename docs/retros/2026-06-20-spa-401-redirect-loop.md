---
date: 2026-06-20
topic: spa-401-redirect-loop
branch: claude/suspicious-beaver-5f66ef
pr: "2026-06-20 / spa-401-redirect-loop"
merge: "2026-06-20 / spa-401-redirect-loop"
---

# Session Retro: 2026-06-20 - SPA 401 redirect loop fix

**TL;DR:** Closed `bug-2026-06-10-spa-401-redirect-loop`, the infinite
sign-in bounce-back every session hit at token expiry. The single
`onUnauthorized` subscription now lives in `AuthProvider`: on a 401 it clears
the token, user, and status, and calls `queryClient.clear()`, guarded by a
`statusRef` so it no-ops when already anonymous. The dead `UnauthorizedRedirect`
(which navigated to `/auth` without resetting state) was deleted from `App.tsx`,
so the route guards now land the user on sign-in with no re-401 loop.

## What Was Built

- `web/src/auth/AuthProvider.tsx`: a mount-once `useEffect` subscribes to
  `onUnauthorized`; the handler reaches the shared cache via `useQueryClient()`
  and resets auth state. A `statusRef` mirrors `status` so the subscription reads
  the latest value without re-subscribing, and short-circuits when already
  anonymous (no churn-clear on a failed login while still on the sign-in screen).
- `web/src/App.tsx`: removed the dead `UnauthorizedRedirect` component that was
  the root of the bounce-back (it navigated without clearing state, so the guards
  immediately sent the user back).
- `web/src/App.test.tsx`: red-then-green regression test driving the
  authenticated -> 401 -> sign-in-renders path; plus the mechanically-required
  `QueryClientProvider` wrappers in sibling tests. Full web suite green.

## Key Decisions

- **Handler lives in `AuthProvider`, not `App.tsx`.** The backlog proposal placed
  the handler in `App.tsx`, but `setUser`/`setStatus` are private `useState`
  setters not exposed on the auth context, and the cache needs `useQueryClient()`.
  The only place with access to all three is `AuthProvider`, so the subscription
  moved there.
- **Drive the query, not the timers, in the regression test.** The plan called for
  `vi.useFakeTimers()` plus advancing the clock to fire the poll, but React Query
  timers registered with real timers before `useFakeTimers()` are not in the fake
  queue, making the trigger unreliable. The test instead calls
  `queryClient.refetchQueries()`, which drives the identical
  authenticated -> 401 -> guard path deterministically.
- **Scope held to the loop.** `queryClient.clear()` on the manual `logout()` path
  and `expires_at`-driven proactive expiry were left out; the cache-on-logout gap
  stays tracked as its own open item.

## Problems Encountered

- **The backlog proposal's code placement was wrong.** It assumed the 401 handler
  could sit in `App.tsx` and call the auth setters directly; those setters are not
  on the context. The spec caught this and relocated the handler to `AuthProvider`
  before planning. **Lesson:** validate a backlog proposal's code placement against
  the actual module boundaries (what is exported vs. private) before planning, not
  after.
- **Fake-timer poll advancement is fragile with React Query.** `vi.useFakeTimers()`
  did not capture timers React Query had already registered, so the poll would not
  fire on demand. **Lesson:** to exercise a refetch-driven path, call
  `refetchQueries()`/`invalidateQueries()` directly rather than advancing fake
  timers.
- **Browser-preview gate was only partial.** No live `relay-server` backend was
  running, so the polling-401 path could not be exercised end to end in a browser.
  The engineer confirmed the auth-reset mechanism via `preview_eval` (a mount-path
  401 clears the token and lands on `/auth`) and relied on the vitest regression
  test for the polling path, and was explicit about the limitation. This is
  recurring friction: the auth slice has shipped bugs past unit tests before, and
  it is exactly the gap the open `idea-2026-06-03-web-e2e-harness` item targets.

## Improvement Goals

- Prefer driving React Query refetches directly in tests over fake-timer advancement.
- The partial browser-preview gate reinforces the existing E2E-harness idea; no new
  item filed (see Backlog Triage below).

## Backlog Triage

- **No new backlog items filed.** The one candidate gap this iteration surfaced -
  inability to exercise the polling-401 path in a browser without a live backend -
  is squarely covered by the open `idea-2026-06-03-web-e2e-harness.md`, whose stated
  purpose is exactly "a browser-driven E2E harness exercising the SPA against a real
  `relay-server`" to catch the auth-class bugs that slip past unit tests. Filing a
  new item would duplicate it.
- **`bug-2026-06-10-spa-query-cache-logout` stays open and is NOT re-filed.** The
  401 fix added `queryClient.clear()` only to the `onUnauthorized` handler; the
  manual `logout()` path in `AuthProvider` still does not clear the cache, so the
  previous-user-data-flash bug remains. `queryClient` is now in scope there, so the
  remaining fix is a one-liner, but it is the existing tracked item, not new work.

## Files Most Touched

- `web/src/auth/AuthProvider.tsx`
- `web/src/App.tsx`
- `web/src/App.test.tsx`
