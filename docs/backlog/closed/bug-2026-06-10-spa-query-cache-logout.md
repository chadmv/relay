---
title: Query cache is not cleared on logout; previous user's data flashes for the next user
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-20
resolution: fixed
priority: medium
source: full-codebase review (2026-06-10)
---

# Query cache is not cleared on logout; previous user's data flashes for the next user

## Summary
`logout()` clears the token and user but never touches the shared `queryClient`. All jobs/workers/schedules query data stays cached in memory, so a second user logging in on the same browser session sees the previous user's cached rows render instantly (placeholder until the refetch lands), including `submitted_by_email` and `owner_email`. No `queryClient.clear()`/`removeQueries` call exists anywhere in `web/src`.

## Proposal
`AuthProvider` is mounted inside `QueryClientProvider`, so:

```tsx
const qc = useQueryClient()
async function logout() {
  await apiFetch('/auth/token', { method: 'DELETE' }).catch(() => {})
  clearToken(); setUser(null); setStatus('anonymous')
  qc.clear()
}
```

Also clear from the 401 handler (see the redirect-loop item).

## Related
- `web/src/auth/AuthProvider.tsx:69-74`
- `web/src/lib/queryClient.ts`
- bug-2026-06-10-spa-401-redirect-loop

## Resolution
fixed - `logout()` in `web/src/auth/AuthProvider.tsx` now calls `queryClient.clear()`
after resetting auth state, mirroring the already-fixed 401-handler path. The previous
user's cached jobs/workers/schedules rows (including emails) no longer flash for the next
user logging in on the same browser. Covered by a new AuthProvider test that seeds the
cache, logs out, and asserts the query cache is empty.
