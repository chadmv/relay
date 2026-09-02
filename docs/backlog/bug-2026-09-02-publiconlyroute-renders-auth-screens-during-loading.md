---
title: PublicOnlyRoute renders the auth screens while auth status is still loading, so an authenticated direct visit flashes and focuses a form that unmounts
type: bug
status: open
created: 2026-09-02
priority: low
source: Phase 4 invariants lens on the 2026-09-01 usermenu-focus chain (lane C of the web SPA batch)
---

# PublicOnlyRoute renders the auth screens during loading

## Summary
PublicOnlyRoute redirects only when status is authenticated; during loading it renders the outlet. A
returning user with a live token who opens /auth or /register sees the form mount, autoFocus moves
focus into it (which can pop a password-manager dropdown or the soft keyboard), and then /users/me
resolves and the redirect unmounts it, dropping focus to body on /jobs. The transient focus grab is
new with the arrival-focus change; the flash and the body landing predate it.

## Proposal
Render nothing (or the app's loading treatment) from PublicOnlyRoute while status is loading, the
mirror of what ProtectedRoute already does, and pin it with a test that mounts /auth with a live token
and asserts the email field never takes focus.

## Related
- web/src/app/PublicOnlyRoute.tsx, web/src/auth/LoginScreen.tsx, web/src/auth/RegisterScreen.tsx
- [[idea-2026-08-13-post-logout-focus-lands-on-body]] (closed)
