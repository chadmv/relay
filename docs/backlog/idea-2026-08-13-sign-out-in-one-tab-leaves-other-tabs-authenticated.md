---
title: Signing out in one tab leaves other tabs rendering an authenticated shell
type: idea
status: open
created: 2026-08-13
priority: low
source: Phase 6 triage of the 2026-08-13-cross-generation-401 slice (behaviour of the new identity fence across tabs, reasoned not measured)
---

# Signing out in one tab leaves other tabs rendering an authenticated shell

## Summary

The SPA has never propagated a sign-out across tabs, and after the cross-generation-401 fix the one
accidental mechanism that sometimes did it no longer fires.

`localStorage` is shared across every tab on the origin, so when tab 1 signs out - `clearToken()` on
logout, on sign-out-everywhere, or on any 401 teardown - tab 2's `getToken()` becomes `null` immediately.
Tab 2's React state is untouched: `status` is still `authenticated`, `user` is still populated,
`ProtectedRoute` keeps rendering the app shell and whatever data is already in its query cache.

Tab 2 recovers on its **next request**, which goes out with no `Authorization` header, 401s, and then
passes both fences in `AuthProvider`'s listener (`requestToken` is `null`, `getToken()` is `null`, so
identity passes by equality; `statusRef.current` is `authenticated`, so currency passes) and tears the
session down. A page with a polling hook recovers within one interval. A page with no polling and no user
interaction can sit indefinitely showing an authenticated shell over stale cached data.

## Repro / Symptoms

1. Sign in. Open the app in two tabs.
2. In tab 2, sit on a page that issues no periodic requests.
3. In tab 1, sign out (or use **Sign out everywhere** from `/profile/sessions`).
4. Tab 2 keeps rendering the header, the nav and the last-loaded data as though signed in. No banner, no
   redirect.
5. Interact with tab 2 so that it issues any request. It 401s, and only then does tab 2 go to `/auth`.

Nothing is actually reachable in step 4: tab 2 holds no credential, so every request it makes is
anonymous and fails. The defect is that the UI states a session exists when none does.

## Context

**The identity fence is behaving correctly here and must not be relaxed.** When tab 2 has a request in
flight at the moment tab 1 signs out, that request 401s carrying token A while `getToken()` is already
`null`, so the fence rejects it - correctly, because **that 401 genuinely says nothing about tab 2's
authority**. It is a report about a credential that no longer exists anywhere. Acting on it would be the
same category error the fence was built to stop.

Read this as a **pre-existing gap the fence made slightly more visible**, not as a regression in the fix:

- Before the fence, tab 2 bounced to `/auth` **only if it happened to have a request in flight** at the
  instant of the sign-out. That is an accident of timing, not a mechanism.
- A tab with nothing in flight sat exactly as stale before the fence as it does now.
- So the behaviour change is narrow and real: the in-flight case lost its accidental recovery, and the
  common case is unchanged.

Naming it that way matters, because the wrong reading ("the fix broke multi-tab sign-out") leads
straight to weakening the fence, which reintroduces the bug the fence closed.

**Not measured.** No test in the suite covers two tabs and no browser run has reproduced this. It is
reasoned from `localStorage` semantics and the shipped listener. Confirming it is the first task, not an
assumption to build on.

## Proposal

Give `AuthProvider` a `storage` event listener - the standard, purpose-built cross-tab mechanism, which
the app has never used.

- The `storage` event fires in **other** tabs of the same origin when a key changes, and does not fire in
  the tab that made the change, which is exactly the fan-out shape wanted here.
- On an event for the token key where the new value is `null` or differs from what this tab holds, run
  the same teardown `clearSession()` already performs. Do not invent a second teardown path.
- **Ordering matters and is the trap.** CLAUDE.md Invariant 1 in its frontend form: end the generation
  before releasing the resource. Work out what the listener's own continuations do on the next tick
  before wiring it, and re-read `web/src/jobs/useTaskLogStream.ts` for the instance the invariant was
  written from.
- Consider the **token replaced** case as well as removed: tab 1 signing in as a different user should
  not leave tab 2 rendering the first user's identity while sending the second user's credential.
  Comparing values rather than presence handles both, the same way the 401 identity fence does.

**Considered and rejected: relax the identity fence so a cross-generation 401 tears down anyway.** That
is a one-character change and it is wrong. It reinstates the exact defect
`bug-2026-08-13-cross-generation-401-clears-a-new-session` closed, because within a single tab the same
input means the opposite thing.

**Considered and rejected: poll `getToken()` on an interval.** A timer to observe a store that already
emits events, with a latency floor for free.

## Acceptance / Done When

- The behaviour is **reproduced first**, in a real browser with two tabs, before anything is built. If it
  does not reproduce, close the item and record why.
- After a sign-out in tab 1, tab 2 reaches the sign-in screen without needing a user action or a request.
- The identity fence in `AuthProvider`'s `onUnauthorized` listener is **unchanged**, and
  `web/src/auth/AuthProvider.crossgen.test.tsx` stays green untouched. If any assertion there needs
  adjusting, that is the finding, not the fix.
- A test covers the token-replaced case, not only token-removed.
- The single-tab path is unaffected: the tab that performed the sign-out must not run the teardown twice.

## Related

- Source: `web/src/auth/AuthProvider.tsx:113-115` (the two fences), `:164-169` (`clearSession`, the
  teardown to reuse), `web/src/lib/token.ts` (the `localStorage` accessors that are the cross-tab channel)
- The slice that produced this behaviour and the reasoning behind the fence:
  [[bug-2026-08-13-cross-generation-401-clears-a-new-session]] (closed) and
  `docs/retros/2026-08-13-cross-generation-401.md`
- The same "a credential died and the client has not noticed" question on the streaming side:
  [[idea-2026-08-09-sse-revoked-token-keeps-streaming]] - worth reading together, since a `storage`
  listener that tears the session down would also need to stop a live stream
- The harness that would make a two-tab test possible in CI at all:
  [[idea-2026-06-03-web-e2e-harness]]

## Notes

Filed as an idea rather than a bug because the failing behaviour is a stale view rather than a wrong
action: tab 2 cannot do anything with the session it appears to have, and it self-heals on contact. Low
priority for the same reason.

Titled as the observable rather than the remedy on purpose. "AuthProvider lacks a storage listener" would
bake one answer into the title, and this project has been burned by that before - see
`docs/backlog/closed/bug-2026-06-03-usermenu-aria-attributes.md`, whose fix-shaped title cost two months
and a follow-up item that had to be inverted.
