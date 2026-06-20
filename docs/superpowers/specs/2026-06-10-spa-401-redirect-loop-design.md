# SPA 401 redirect-loop fix - design

- Date: 2026-06-10
- Author: relay TPM (autonomous SPEC phase)
- Backlog: `docs/backlog/bug-2026-06-10-spa-401-redirect-loop.md`
- Status: ready for implementation

## Problem

When a session's bearer token expires (the 30-day lifetime that every session
eventually hits), the SPA freezes in a redirect loop that only a hard reload
escapes.

Root cause, traced through the live code:

- `web/src/lib/api.ts` fires every `onUnauthorized` listener on any `401`.
- The only subscriber is `UnauthorizedRedirect` in `web/src/App.tsx`, which calls
  `navigate('/auth')` and nothing else.
- `AuthProvider` (`web/src/auth/AuthProvider.tsx`) sets `status` only at mount, so
  after the navigate it is still `authenticated`, `user` is still set, and the
  token is still in `localStorage`.
- `PublicOnlyRoute` (`web/src/app/PublicOnlyRoute.tsx:9`) sees
  `status === 'authenticated'` and immediately `<Navigate to="/jobs">`.
- `/jobs` is polled; the next poll 401s, fires `onUnauthorized` again, navigates
  back to `/auth`, bounces to `/jobs`. The URL flickers and stale data stays on
  screen.

The fix is to reset auth state when a 401 arrives, so the existing route guards
land the user on the sign-in screen with no bounce-back.

## Correction to the backlog proposal

The proposal's snippet calls `clearToken()`, `setUser(null)`, `setStatus('anonymous')`
and `queryClient.clear()`. Validated against the code, those setters
(`setUser`, `setStatus`) are local `useState` setters private to `AuthProvider`;
they are not exposed on the auth context, and `App.tsx`'s `UnauthorizedRedirect`
has no access to them. Therefore the subscription must move **into** `AuthProvider`,
where the setters live, rather than staying in `App.tsx`. `queryClient` is reached
inside `AuthProvider` via `useQueryClient()` (the provider is nested inside
`QueryClientProvider` in `App.tsx`), not via the module-level `queryClient` import.
This matches the proposal's intent; only the placement differs.

## Fix

Two files change. No new files.

### `web/src/auth/AuthProvider.tsx`

- Import `useQueryClient` from `@tanstack/react-query` and `onUnauthorized` from
  `../lib/api`.
- Inside `AuthProvider`, obtain `const queryClient = useQueryClient()`.
- Add one `useEffect` that subscribes once to `onUnauthorized` and resets state:

  - `clearToken()`
  - `setUser(null)`
  - `setStatus('anonymous')`
  - `queryClient.clear()`

  The effect returns the unsubscribe function that `onUnauthorized` already gives
  back, and runs with an empty dependency array so it subscribes exactly once for
  the lifetime of the provider. This satisfies the "wired once, no double
  subscribe" requirement: `onUnauthorized` stores listeners in a `Set`, and the
  effect's cleanup removes the listener on unmount.

  Guard the reset so it is a no-op when already anonymous (avoid redundant state
  churn and a needless `queryClient.clear()` when an unauthenticated request 401s,
  e.g. a bad login still on the sign-in screen). Implementation may early-return
  when `status === 'anonymous'`; the implementer chooses the exact mechanism (ref
  or functional state read) so the effect can stay mounted once with a stable
  closure.

### `web/src/App.tsx`

- Delete the `UnauthorizedRedirect` component and its `<UnauthorizedRedirect />`
  usage.
- Remove the now-unused imports: `useEffect`, `useNavigate`, `onUnauthorized`,
  and the module-level `queryClient` import if nothing else in the file uses it.
  (`QueryClientProvider` still needs `queryClient`, so that import stays.)

No change to `api.ts`, `queryClient.ts`, `token.ts`, `PublicOnlyRoute.tsx`,
`ProtectedRoute.tsx`, or the router. The route guards already do the right thing
once `status` flips to `anonymous`:

- `ProtectedRoute.tsx:8` -> `status === 'anonymous'` renders `<Navigate to="/auth">`.
- `PublicOnlyRoute.tsx:10` -> non-authenticated renders the sign-in `<Outlet>`.

## Success criteria

1. An authenticated session whose next poll returns 401 lands on the sign-in
   screen.
2. No redirect loop: the URL settles on `/auth` and does not flicker back to
   `/jobs`.
3. Token and auth state are cleared: `localStorage` token is gone, `user` is
   `null`, `status` is `anonymous`.
4. The query cache is cleared so stale rows do not linger.

## Verification

### Vitest component/integration test

Add a test (extend `web/src/App.test.tsx`, which already drives the full `<App />`
through msw, or a focused `AuthProvider` test mirroring the existing
`AuthProvider.test.tsx` patterns). The integration-level `App.test.tsx` is
preferred because it exercises the real route guards and proves the absence of a
loop.

Test shape:

- Seed `setToken('tok')`; msw returns a user for `/v1/users/me` and empty data for
  the `/jobs` queries so the app reaches the jobs page (`OVERVIEW` is the existing
  authenticated-landing assertion in `App.test.tsx`).
- Flip the relevant msw handler (e.g. `/v1/jobs` or `/v1/users/me` on the next
  poll) to return `401`.
- Assert the sign-in screen renders (`/sign in to the coordinator/i`, the existing
  marker).
- Assert `getToken()` is `null` after the 401 is handled.
- Assert no loop: the sign-in screen stays rendered (a `waitFor` that the
  authenticated `OVERVIEW` marker is gone and does not reappear). Keep the test
  deterministic - avoid relying on real polling timers; trigger the 401 via an
  explicit refetch or a short fake-timer advance consistent with the existing
  hooks' `refetchInterval`.

The whole `web` suite (`npm test` / vitest) must stay green, including the
existing `App.test.tsx`, `AuthProvider.test.tsx`, and `ProtectedRoute.test.tsx`.

### Browser-preview check

Run the dev server, sign in, then force a 401 (revoke or expire the token
server-side, or temporarily make an endpoint return 401) and confirm the UI lands
on the sign-in form with a stable URL and no flicker, and that a fresh login works
without a reload.

## Risk and overlap notes

- **Overlap with `bug-2026-06-10-spa-query-cache-logout`.** Clearing the cache in
  the 401 handler is in scope here because a stale cache is part of what makes the
  frozen page show old data. It does **not** fully subsume the logout item: that
  bug is about the manual `logout()` path, which is a separate code path and still
  needs its own `queryClient.clear()`. Flag for the human: after this fix lands,
  the query-cache-logout item is reduced to "add `queryClient.clear()` to
  `logout()`" and should stay open. Do not close it as part of this work.
- **`expires_at` unused.** The backlog notes `LoginResponse.expires_at` is received
  but never used, leaving 401 as the only expiry path. Out of scope for this fix;
  proactive expiry is a separate enhancement and should not be folded in here.
- **No double-clear churn.** The anonymous guard above prevents a 401 on an
  already-anonymous session (failed login) from clearing an empty cache and
  re-setting state on every keystroke-driven request.
- **Listener lifetime.** Moving the subscription into `AuthProvider` keeps it tied
  to the provider's lifetime; the `Set`-based unsubscribe in `onUnauthorized`
  ensures no leak across remounts in tests.
