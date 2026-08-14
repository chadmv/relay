---
title: A login the user was told failed can silently succeed on the next page load
type: bug
status: open
created: 2026-08-13
priority: low
source: Phase 6 triage of the 2026-08-13-cross-generation-401 slice (pre-existing, found while reading applyAuth for the identity fence)
---

# A login the user was told failed can silently succeed on the next page load

## Summary

`applyAuth` stores the token before it has confirmed anything, and never unstores it if the
confirmation fails:

```
async function applyAuth(res: LoginResponse) {
  setToken(res.token)                              // web/src/auth/AuthProvider.tsx:142
  const me = await apiFetch<User>('/users/me')     // :143 - no catch anywhere on this path
  setUser(me)
  setStatus('authenticated')
}
```

If `GET /v1/users/me` fails, `applyAuth` throws, `login()` rejects, and `LoginScreen` catches it and
renders an error message (`web/src/auth/LoginScreen.tsx:20-24`). Nothing clears the token. The user is
told the login failed while a live credential sits in `localStorage`.

On the next full page load the bootstrap effect (`AuthProvider.tsx:124-139`) finds that token, calls
`/users/me`, and - if the failure was transient - **authenticates the user who was just told their
login did not work.**

## Repro / Symptoms

1. Submit correct credentials. `POST /v1/auth/login` returns 200 and the token is stored.
2. Make the immediately following `GET /v1/users/me` fail for any reason other than the credential
   itself: a 500, a proxy hiccup, a dropped connection, a timeout.
3. The sign-in screen shows "Sign-in failed" (or the 429 copy). The user believes they are signed out.
4. Reload the page. The bootstrap effect uses the stored token, `/users/me` now succeeds, and the app
   renders as authenticated with no sign-in.

The 401 sub-case behaves differently and is **not** the interesting one: a 401 on that request fires
`onUnauthorized`, and `AuthProvider`'s listener declines to act because `statusRef.current` is still
`anonymous` on the sign-in screen. That guard is correct and must not be removed - it is what stops a
mistyped password churning auth state on every attempt. The token is simply left behind, and the
bootstrap effect's own `.catch` (`:134-138`) clears it on the next mount. So for the 401 path the
symptom is a dead credential lingering in `localStorage` until the next page load; for the non-401
paths it is the silent sign-in above.

## Context

**Entirely pre-existing.** Identical on `origin/main`. The 2026-08-13 cross-generation-401 slice did not
touch `applyAuth`, and its identity fence changes nothing here in either direction: the 401 arriving on
this path carries the token that was just stored, so it passes the identity fence by equality and is
stopped by the `anonymous` guard exactly as it was before.

Why the ordering is the way it is, so nobody "fixes" it by moving the `setToken`: `apiFetch` reads the
credential from `localStorage` at call time (`web/src/lib/api.ts:67`), so the token **must** be stored
before the `/users/me` request goes out. Storing later is not an option; the missing piece is the
failure path, not the order.

Impact is bounded and that is why this is low. Nothing escalates privilege, no other user is affected,
and the stored credential is the one the server legitimately issued to this person seconds earlier. The
defect is that the UI and `localStorage` disagree about whether a session exists, and the user is the
one holding the wrong belief.

## Proposal

Wrap the confirmation and undo the store on failure:

```
async function applyAuth(res: LoginResponse) {
  setToken(res.token)
  try {
    const me = await apiFetch<User>('/users/me')
    setUser(me)
    setStatus('authenticated')
  } catch (err) {
    clearToken()   // the session was never established; do not leave the credential behind
    throw err      // LoginScreen still renders its error - the caller's contract is unchanged
  }
}
```

`clearToken` is already imported (`AuthProvider.tsx:4`). `setUser`/`setStatus` do not need resetting on
this path: on the sign-in screen `user` is already `null` and `status` is already `anonymous`, and
`register()` reaches `applyAuth` from the same screen. If the same helper is ever reached from an
authenticated state, the full `clearSession()` is the correct call instead - state that decision when
implementing rather than inheriting this note.

**Considered and rejected: leave the token and rely on the bootstrap effect.** It does eventually
converge, but only on the next mount, and convergence is not the problem - the problem is that between
now and that mount the user has been told one thing and the store believes another. It also converges
in the *wrong direction* for the transient case, by signing the user in.

## Acceptance / Done When

- A test proves the discriminating case: `POST /auth/login` returns 200, the subsequent `GET /users/me`
  returns a non-401 error, and after the rejection `getToken()` is `null`. Prove it RED against today's
  code first - a test that only asserts "a failed login shows an error" passes either way and is vacuous
  here.
- The paired positive control survives: a **successful** login still leaves the token stored and the
  status `authenticated`.
- The 401 sub-case is covered too, and the `anonymous` guard in the `onUnauthorized` listener stays
  untouched: `web/src/auth/LoginScreen.test.tsx` must stay green without edits.
- `register()` gets the same treatment, since it shares `applyAuth`.

## Related

- Source: `web/src/auth/AuthProvider.tsx:141-146` (`applyAuth`), `:124-139` (the bootstrap effect that
  makes the silent sign-in possible), `:113-115` (the two fences, neither of which fires here),
  `web/src/auth/LoginScreen.tsx:20-24` (the caller that reports failure and clears nothing),
  `web/src/lib/api.ts:67` (why the token must be stored before the request)
- **Would remove this window entirely**: [[idea-2026-06-03-login-return-user-object]] - if login returned
  the user object, `applyAuth` would have no second round trip to fail. If that item is picked up first,
  this one may close with it; if this one is picked up first, keep the `catch` anyway, because `register`
  and any future confirmation step reintroduce the shape.
- The slice that surfaced it, and the fence that does **not** cover it:
  [[bug-2026-08-13-cross-generation-401-clears-a-new-session]] (closed) and
  `docs/retros/2026-08-13-cross-generation-401.md`

## Notes

Filed as a bug rather than an idea because the observable is unambiguous and user-visible: the app tells
you the login failed and then signs you in. Low rather than medium because it needs a non-401 failure in
a specific one-request window, the blast radius is one user's own credential, and the workaround
(sign in again, or sign out) is immediate.

The generalizable shape, in the vocabulary this project already uses: **a two-phase operation that
writes durable state in phase one must undo that write when phase two fails.** `applyAuth` writes to
`localStorage` before it has earned the right to, which is the same family as the epoch-fence rule that
a side effect must be gated on the check having actually passed.
