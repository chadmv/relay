---
date: 2026-06-20
topic: spa-query-cache-logout
branch: claude/blissful-brown-c7780a
pr: "2026-06-20 / spa-query-cache-logout"
merge: "2026-06-20 / spa-query-cache-logout"
---

# Session Retro: 2026-06-20 - Clear query cache on logout

**TL;DR:** Closed `bug-2026-06-10-spa-query-cache-logout` with a one-line fix:
the manual `logout()` path in `web/src/auth/AuthProvider.tsx` now calls
`queryClient.clear()` after resetting auth state. Previously the token and user
were cleared but the shared TanStack Query cache was not, so a second user
logging in on the same browser saw the previous user's cached jobs/workers/
schedules rows (including emails) flash before the refetch landed.

## What Was Built

- `web/src/auth/AuthProvider.tsx:96`: add `queryClient.clear()` to `logout()`,
  mirroring the already-fixed 401-handler path (line 46). `queryClient` was
  already in scope via `useQueryClient()`.
- `web/src/auth/AuthProvider.test.tsx`: new regression test that seeds the cache
  with another user's data, logs out, and asserts the query cache is empty.
  Confirmed to fail without the fix.

## Key Decisions

- Trivial one-liner, so calibrated the playbook to a TDD implementation plus a
  single combined review instead of the full spec -> plan -> two-stage-review
  pipeline. The 401-handler path was already correct, so scope was just the
  manual logout path.

## Backlog Triage

- None. One-line fix; nothing surfaced. No new items filed.
