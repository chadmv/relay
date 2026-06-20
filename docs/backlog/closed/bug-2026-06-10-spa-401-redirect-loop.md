---
title: SPA gets stuck in an infinite redirect loop when the session expires
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-20
priority: high
source: full-codebase review (2026-06-10)
---

## Resolution
Fixed 2026-06-20 (spa-401-redirect-loop). Moved the single `onUnauthorized`
subscription into `AuthProvider`: on a 401 it now calls `clearToken()`,
`setUser(null)`, `setStatus('anonymous')`, and `queryClient.clear()`, guarded by a
`statusRef` so it no-ops when already anonymous (no churn-clear on a failed login)
and subscribes exactly once via a `Set`-backed unsubscribe. The dead
`UnauthorizedRedirect` (which navigated to `/auth` without resetting state, the
cause of the bounce-back) was deleted from `App.tsx`. With `status` now
`anonymous`, `ProtectedRoute` renders `<Navigate to="/auth">` and `PublicOnlyRoute`
shows the sign-in screen instead of bouncing to `/jobs`, so the poll no longer
re-401s. A red regression test in `App.test.tsx` reproduces the loop
(authenticated -> next request 401 -> sign-in renders, token null, overview gone)
and is green with the fix; full web suite 139/139, `tsc -b && vite build` clean.
Confined to `web/src/auth/AuthProvider.tsx` and `web/src/App.tsx` (plus the test
and mechanically-required `QueryClientProvider` wrappers in sibling tests). The
sibling item [spa-query-cache-logout](bug-2026-06-10-spa-query-cache-logout.md)
(clear the cache on the manual `logout()` path) is NOT subsumed and stays open;
`expires_at`-driven proactive expiry remains out of scope.

# SPA gets stuck in an infinite redirect loop when the session expires

## Summary
The only `onUnauthorized` subscriber (`App.tsx:11`) navigates to `/auth` but never clears the token or auth state. `AuthProvider` only sets status at mount, so status stays `authenticated`; `PublicOnlyRoute` bounces back to `/jobs`, whose queries 401 again every poll cycle. The user sees a frozen page with stale data and a flickering URL; only a hard reload escapes. This is the path every session hits at the 30-day token expiry. Related gap: `LoginResponse.expires_at` is received but never used, so 401 handling is the only expiry path.

## Proposal
Make the 401 handler reset auth state in `AuthProvider`; the route guards then redirect naturally:

```tsx
useEffect(() => onUnauthorized(() => {
  clearToken()
  setUser(null)
  setStatus('anonymous')
  queryClient.clear()
}), [])
```

Delete `UnauthorizedRedirect` from App.tsx. Add a test: authenticated session, next poll returns 401, expect the sign-in screen.

## Related
- `web/src/App.tsx:9-13`
- `web/src/auth/AuthProvider.tsx:29-51`
- `web/src/app/PublicOnlyRoute.tsx:9`
- bug-2026-06-10-spa-query-cache-logout
