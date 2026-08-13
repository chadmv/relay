---
title: A 401 from an old session clears a brand-new one, bouncing the user back to sign-in
type: bug
status: open
created: 2026-08-13
priority: medium
source: Phase 6 triage of the 2026-08-12-profile-pages slice (pre-existing gap, newly reachable through sign-out-everywhere)
---

# A 401 from an old session clears a brand-new one, bouncing the user back to sign-in

## Summary

`apiFetch` fires every registered `onUnauthorized` listener on **any** 401
(`web/src/lib/api.ts:44-46`), and the one listener that matters - `AuthProvider`'s - checks only
the current `status` before tearing the session down (`web/src/auth/AuthProvider.tsx:77-83`).
Neither side knows **which session** the 401 belongs to. A 401 caused by a token that is already
dead will therefore clear whatever token is in `localStorage` at the moment it lands, including one
issued seconds earlier by a fresh, successful login.

The listener's only guard is `if (statusRef.current === 'anonymous') return`, which exists to stop a
failed login on the sign-in screen from churning state. After a successful re-login the status is
`authenticated` again, so the guard is open and the stale 401 goes straight through to
`clearToken()`.

## Repro / Symptoms

1. Sign in. Any polled list page is mounted, so requests are in flight periodically
   (`web/src/jobs/useJobs.ts` and friends poll on an interval).
2. Trigger `DELETE /v1/auth/tokens` from `/profile/sessions`. The server deletes **every** token for
   the user (`DeleteTokensForUser`, `internal/store/query/tokens.sql:25-26`) and the SPA tears its
   own session down and navigates to `/auth`.
3. Any request that was already in flight at step 2 completes against the destroyed credential and
   returns 401.
4. Sign in again quickly. If a 401 from step 3 resolves after the new token is stored, the listener
   runs, `clearToken()` removes the new token, `setStatus('anonymous')` fires, and `ProtectedRoute`
   sends the user back to `/auth`. The user sees a successful login immediately undone with no
   error message.

The window is the tail of an in-flight request, so this is a race rather than a deterministic
failure - but sign-out-everywhere is precisely the flow that *guarantees* a burst of 401s and then
drops the user on a sign-in form, which is what turns a latent race into a reachable one.

## Context

**Pre-existing, not introduced by the profile slice.** The listener has been session-blind since it
was written, and the two paths that could produce a stale 401 before now (an admin archiving a user,
an admin password reset) both target *other* people's sessions, so the victim was never the one
about to log back in. Sign-out-everywhere is the first flow where the same person destroys their own
tokens and is then immediately invited to re-authenticate in the same tab.

Two aggravating details:

- **`apiFetch` passes no `AbortSignal`** (`web/src/lib/api.ts:29-42` builds `fetch` options from
  `opts` and never supplies one), so nothing cancels in-flight requests at teardown. `queryClient.clear()`
  evicts cached data; it does not abort a request already on the wire, and it does not stop a
  still-mounted observer from scheduling more. Verified during the profile slice's Phase 4 by
  measurement, and the corrected mechanism is now written out at
  `web/src/auth/AuthProvider.tsx:40-56`.
- **`apiStream` fires the same listeners** on a 401 (`web/src/lib/api.ts:127-129`), and an SSE
  connection is by construction long-lived, so it is the most likely source of a late 401 of all.

What is **not** the bug: the teardown ordering inside `clearSession` itself. Three Phase 4 lanes
independently confirmed that `clearSession` is synchronous, that `clearToken()` runs first so an
escaped request carries no `Authorization` header, and that `onUnauthorized` converges on identical
state when it does fire during that teardown. The defect is one generation later - the 401 that
arrives after a *new* session exists.

## Proposal

Give the 401 a session identity, and check it before acting. Two shapes, both small:

- **A. Stamp the token on the request and compare at teardown time.** `apiFetch` already reads the
  token at `web/src/lib/api.ts:32`; pass that value to the listeners
  (`unauthorizedListeners.forEach((fn) => fn(token))`) and have `AuthProvider`'s listener return
  early unless `getToken() === thatToken`. Do the same at `web/src/lib/api.ts:127-129` for
  `apiStream`, which reads its token at `:114`. This is the narrowest fix, it needs no new state, and
  it is exact: a 401 can only tear down the session whose credential produced it.
- **B. A session generation counter.** A ref in `AuthProvider` bumped by `login`, `register`,
  `clearSession` and the listener itself; requests capture it at issue time and the listener ignores
  a 401 stamped with a stale generation. More machinery, but it also covers a 401 arriving for a
  token that has since been *replaced* rather than removed.

A is the recommendation. B is only better if a second consumer of the generation appears.

**Explicitly not proposed:** aborting in-flight requests at teardown. That looks like the tidier fix
and is a trap - it is CLAUDE.md Invariant 1 in its frontend form, where aborting without first ending
the generation lets the dying request's own rejection handler run while it still looks current. If an
abort is added later it must come *after* the generation is ended, not instead of it. See
`web/src/jobs/useTaskLogStream.ts` for the instance the invariant was written from.

## Acceptance / Done When

- A test proves the discriminating case: sign in as token A, start a request that will 401, tear the
  session down, sign in as token B, then let the 401 resolve. `getToken()` must still be B and the
  auth status must still be `authenticated`. Prove it RED against today's code first - a test that
  only asserts "a 401 during a live session signs you out" passes either way and is vacuous here.
- The paired positive control survives: a 401 belonging to the **current** token still tears the
  session down. `web/src/profile/PasswordTab.auth.test.tsx:83-99` is the existing instance of that
  assertion and must stay green.
- `apiStream`'s 401 path (`web/src/lib/api.ts:127-129`) gets the same treatment, not just
  `apiFetch`'s.
- Whatever mechanism is chosen is documented at `AuthProvider`'s listener, which today explains only
  the `anonymous` guard.

## Related

- Source: `web/src/lib/api.ts:44-46` (the fire site), `:127-129` (the stream fire site), `:29-42`
  (no `AbortSignal`), `web/src/auth/AuthProvider.tsx:75-85` (the listener and its only guard),
  `:127-132` (`clearSession`), `web/src/profile/SessionsTab.tsx:63-66` (the caller that makes this
  reachable)
- The flow that surfaced it: `docs/superpowers/specs/2026-08-12-profile-pages.md` (decision 7) and
  `docs/retros/2026-08-12-profile-pages.md`
- Same token-revocation blind spot on the streaming side, and the other consumer of the 401 fire
  site: [[idea-2026-08-09-sse-revoked-token-keeps-streaming]]
- Touches the same login path, and would let `applyAuth` set the user without a second round trip
  while it is being edited: [[idea-2026-06-03-login-return-user-object]]

## Notes

Filed as a bug rather than an idea because the failing behaviour is unambiguous - a successful login
is silently undone - even though the trigger is a race. Medium rather than high because the window is
short, the user's remedy is to sign in a second time, and no data is at risk: the failure mode is
losing a session, never gaining one.

The generalizable shape is the project's own: **a status check establishes currency, never
identity.** The backend learned this on `tasks.status` writes, where a matching `assignment_epoch`
proves the caller's generation is current and proves nothing about who the caller is, so every such
write also fences on `worker_id`. This listener has the epoch half and not the identity half.
