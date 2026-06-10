---
title: SPA gets stuck in an infinite redirect loop when the session expires
type: bug
status: open
created: 2026-06-10
priority: high
source: full-codebase review (2026-06-10)
---

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
