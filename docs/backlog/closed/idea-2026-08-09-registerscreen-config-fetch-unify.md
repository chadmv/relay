---
title: "Move RegisterScreen onto useServerConfig and give its config fetch a cancellation guard"
type: idea
status: closed
closed: 2026-09-03
resolution: fixed
created: 2026-08-09
priority: low
source: deferred review finding from the admin Server/overview tab (2026-08-09)
---

# Move RegisterScreen onto useServerConfig and give its config fetch a cancellation guard

## Summary
`GET /v1/config` now has two client call sites. `web/src/admin/server/useServerConfig.ts` (shipped with
the admin Server tab) is a cached TanStack query keyed `['server-config']` with
`staleTime: Infinity`; `web/src/auth/RegisterScreen.tsx:21-25` still calls
`apiFetch<ConfigResponse>('/config')` inline in a raw `useEffect`. Unify on the hook, and while doing
so close the effect's missing cancellation guard.

## Context
Both facts were review findings on the Server tab slice, deferred by the conductor rather than fixed,
because touching the sign-up path was out of that slice's scope (its spec explicitly ring-fenced
everything outside `web/src/admin/`).

The current effect:

```tsx
useEffect(() => {
  apiFetch<ConfigResponse>('/config')
    .then((c) => setSelfRegister(c.allow_self_register))
    .catch(() => setSelfRegister(false))
}, [])
```

Two issues:

1. **Duplication.** Two clients for one endpoint is the drift the single-pipeline habit exists to
   prevent. Today the shapes agree because both import `ConfigResponse` from `lib/types.ts`, so this is
   a maintainability item rather than a live inconsistency.
2. **No cancellation guard.** Nothing bumps or checks a generation, so whatever resolves calls
   `setSelfRegister` unconditionally. This is the shape of the project's generation-ordering invariant
   ("end the generation before releasing the resource"), applied to an async effect that has no
   generation at all. It is benign in practice today: the screen is unlikely to remount mid-flight, and
   the consequence of a stale flag is a wrong client-side hint about whether an invite token is
   required, not a security decision - `handleRegister` enforces the invite requirement server-side
   regardless (`internal/api/auth.go`). Worth fixing as hygiene on a path that is otherwise easy to
   copy from.

## Proposal
- Have `RegisterScreen` consume `useServerConfig()`, or a small shared wrapper over it, instead of the
  inline fetch. Confirm the two call sites' semantics can genuinely be reconciled before doing this -
  the sign-up path wants a **fail-closed `false`** on error (render the invite field), while the admin
  tab wants a **visible error state and no fabricated value**. If they cannot be reconciled cleanly,
  the honest outcome is to keep the two call sites and instead just add the guard, plus a comment at
  each site naming the other.
- Either way, remove the unguarded `setState`-after-await: an `AbortController` plus an ignore flag,
  or the query hook, which cancels for you.
- `RegisterScreen` is unauthenticated, so if the hook route is taken, verify it works with whatever
  `QueryClientProvider` is in scope at that point in the tree.

## Acceptance / Done When
- `/v1/config` has one client, or two with an explicit comment at each site saying why the semantics
  differ.
- A test asserts a `RegisterScreen` config response that resolves after unmount produces no state
  update (or the query hook's cancellation makes the case unreachable and the test says so).
- The existing `RegisterScreen` tests, including the fail-closed-on-error behaviour, still pass
  unchanged.

## Related
- `web/src/auth/RegisterScreen.tsx:21-25`, `web/src/admin/server/useServerConfig.ts`,
  `web/src/lib/types.ts`
- Retro: `docs/retros/2026-08-09-admin-server-overview-tab.md` (Deferred Findings #1)
- Spec that deliberately deferred it:
  `docs/superpowers/specs/2026-08-09-admin-server-overview-tab.md`

## Resolution
Shipped in lane MF of the 2026-09-02 web-frontend batch: RegisterScreen reads /v1/config through useServerConfig; the inline effect and its unguarded post-await state write are gone. The two consumers keep their own policies (fail-closed false at sign-up, a visible error in the admin tab) and the fail-closed arm got the test the acceptance criterion assumed already existed; a pending config fetch renders neither the form nor a premature guess.
