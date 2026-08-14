# Cross-generation 401: give the 401 a session identity - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop a 401 produced by a dead credential from tearing down a *different*, live session - stamp every request with the token it actually attached, pass that token to the `onUnauthorized` listeners, and have `AuthProvider`'s listener act only when the 401 belongs to the token it is holding right now.

**Architecture:** Two production files. `web/src/lib/api.ts` widens the listener type from `() => void` to `(token: string | null) => void` and passes the token it read at request time at both fire sites (`apiFetch` and `apiStream`). `web/src/auth/AuthProvider.tsx` adds one line to its listener - `if (requestToken !== getToken()) return` - ahead of the existing `anonymous` guard. No new state, no ref, no generation counter, no `AbortSignal`, no change to `clearSession`, no change to any component.

**Tech Stack:** TypeScript 5.7, React 18, Vitest 2.1 + Testing Library 16 + user-event 14 + jsdom 29, MSW 2.7. No new dependency. No Go.

**Backlog item closed by this slice:** `docs/backlog/bug-2026-08-13-cross-generation-401-clears-a-new-session.md`. Close it with `/backlog close bug-2026-08-13-cross-generation-401-clears-a-new-session`, which `git mv`s the file into `docs/backlog/closed/`; never hand-edit `status:`.

---

## READ THIS FIRST: discrepancies between the backlog item and the tree

The item is unusually accurate - **every claim about the SPA's behaviour is correct and every SPA line citation still resolves.** But three of its supporting citations have drifted, and one of its two design arguments is wrong. Nothing here changes the shape of the work; the fix is still small and still lives in the same two files.

| Item's claim | Verdict | Evidence |
|---|---|---|
| `apiFetch` fires every listener on any 401 (`api.ts:44-46`) | **Confirmed, exact** | `web/src/lib/api.ts:44-46` is literally `if (res.status === 401) { unauthorizedListeners.forEach((fn) => fn()) }` |
| The listener's only guard is the `anonymous` check (`AuthProvider.tsx:77-83`) | **Confirmed, exact** | `web/src/auth/AuthProvider.tsx:77-83`; the sole guard is `if (statusRef.current === 'anonymous') return` |
| `apiFetch` passes no `AbortSignal` (`api.ts:29-42`) | **Confirmed, exact** | the `fetch` call at `:38-42` spreads `...rest` and sets only `headers` and `body`; no `signal` anywhere in `apiFetch` |
| `apiStream` fires the same listeners (`api.ts:127-129`) | **Confirmed, exact** | `web/src/lib/api.ts:127-129`, same `unauthorizedListeners.forEach` |
| `apiStream` reads its token at `:114` | **Confirmed, exact** | `const token = getToken()` at `web/src/lib/api.ts:114`; `apiFetch` reads its own at `:32` |
| `clearSession` at `AuthProvider.tsx:127-132` | **Confirmed, exact** | four synchronous statements, `clearToken()` first |
| `SessionsTab.tsx:63-66` is the caller that makes this reachable | **Confirmed, exact** | `onSuccess: () => { clearSession(); navigate('/auth') }` |
| `PasswordTab.auth.test.tsx:83-99` is the existing positive control | **Confirmed, exact** | `test('a 401 from the same endpoint DOES tear the session down (control: the probe is live)')` spans exactly `:83-99` |
| `DELETE /v1/auth/tokens` destroys every token including the caller's | **Confirmed** | `internal/api/server.go:100` -> `handleLogoutAll` (`internal/api/auth.go:353-360`) -> `DeleteTokensForUser` = `DELETE FROM api_tokens WHERE user_id = $1`, no `id <> $2` |
| Polled list pages keep requests in flight | **Confirmed** | `web/src/jobs/useJobs.ts:11`, `useJob.ts:11`, `useJobStats.ts:8` all set `refetchInterval` |
| `DeleteTokensForUser` is at `internal/store/query/tokens.sql:25-26` | **WRONG - drifted** | it is now at **`tokens.sql:40-41`**. Lines 25-26 are inside the `DeleteToken` comment block added by PR #125. `DeleteOtherTokensForUser` likewise moved from `:28-29` to **`:43-44`**. See "Citation drift" below - the same stale pair is repeated in five shipped files. |
| Option B "also covers a 401 arriving for a token that has since been **replaced** rather than removed" - i.e. B strictly dominates A | **WRONG - and it is the item's only argument for B** | Option A compares token **values**, not presence. Replacement is `getToken() === 'tok_B' !== 'tok_A'`, which A rejects exactly as it rejects removal. A would only fail if the *same token string* were re-issued to a later session, which cannot happen: a token is 32 random bytes hex-encoded (CLAUDE.md, "Token format"). **A has no gap relative to B.** |
| "Not the bug: the teardown ordering inside `clearSession`" | **Confirmed, and it stays true under this change** | see "A 401 arriving during teardown" below |

### Citation drift found while verifying (not fixed by every task here - read the scope note)

Two clusters of stale prose, both pre-existing, both the "wrong prose about correct code" defect class:

1. **`tokens.sql:25-26` / `:28-29`** now point at a comment, not at the statements they name. Live sites: `web/src/auth/AuthProvider.tsx:34-35`, `web/src/profile/api.ts:52`, `:67`, `:70`, `web/src/profile/SessionsTab.tsx:36`, `:81`, `web/src/profile/PasswordTab.auth.test.tsx:106`.
2. **`AuthProvider.tsx:39-49`** is cited three times as "the `onUnauthorized` subscriber". The subscription is at `:75-85`; `:39-49` is the middle of the `clearSession` doc comment. Live sites: `web/src/lib/api.ts:94`, `web/src/lib/api.stream.test.ts:46`, `web/src/jobs/useTaskLogStream.ts:337`.

**Task 4 fixes only the four instances that live in files this slice already edits** (`api.ts:94`, `api.stream.test.ts:46`, `AuthProvider.tsx:34-35`). The rest are reported to the conductor as a separate finding, so that `PasswordTab.auth.test.tsx` in particular stays **byte-identical** and its green run remains clean evidence rather than something this slice touched.

### A third, unrelated finding, reported not fixed

`web/src/profile/SessionsTab.tsx:11-12` and `:113-121` state "There is no `GET /v1/auth/tokens` anywhere in `internal/api/server.go`" and point the reader at a backlog file as the tracking item. **That endpoint now exists** (`internal/api/server.go:103`, `handleListTokens`, shipped in PR #125). The tab renders a user-visible paragraph asserting a capability gap that has since been filled. Out of scope here - it is a UI slice, not a race fix - but it should be filed.

---

## Design: option A, confirmed

**Ship option A. Do not build option B.**

The item recommends A and gives B one advantage - covering a token that was *replaced* rather than removed. That advantage is not real. A compares the token **value** the request carried against the token **value** in `localStorage` at the moment the 401 lands, so `A -> (cleared) -> B` and `A -> B` are the same comparison and both are rejected. The only input A cannot distinguish is a second session issued the *identical* token string, which the token format (32 random bytes, hex, never reused) rules out. B, by contrast, adds a mutable ref that four call sites must remember to bump, and CLAUDE.md's first Invariant is precisely about the ordering hazards a generation counter introduces (bump before release, or the dying resource's own callbacks pass the staleness guard). A introduces **no new state at all**, so it has no ordering hazard to get wrong. A is smaller, exact, and strictly sufficient; B is more machinery for a case A already covers.

### What is passed, and what it is compared against

- `apiFetch` and `apiStream` each already read `getToken()` **once**, before the request goes out, into a local `token`. That exact value - `string | null` - is what is handed to the listeners.
- `AuthProvider`'s listener compares it against **`getToken()` evaluated at listener-run time**, not against a ref and not against React state. This is deliberate and load-bearing:
  - `localStorage` is the single source of truth for the credential, and `setToken`/`clearToken` write it **synchronously**.
  - A ref mirroring `status` (or a ref mirroring the token, updated in a commit) would **lag**. `applyAuth` (`AuthProvider.tsx:104-109`) calls `setToken(res.token)` and *then* awaits `/users/me`; the status flip happens after that round trip. A 401 landing inside that window would be compared against a stale mirror and would wrongly tear down the brand-new session - reintroducing the same class of bug through the fix.
  - Reading `getToken()` fresh means the comparison is always against the credential the *next* request would actually send, which is the only thing that matters.

### The two fences, and why both stay

The item's own closing note names the shape: **a status check establishes currency, never identity.** The listener keeps both:

```
identity: requestToken !== getToken()          -> "this 401 is not about the credential we hold"
currency: statusRef.current === 'anonymous'    -> "we are not in a session to tear down"
```

The `anonymous` guard **must not be deleted**. A failed login on the sign-in screen sends a request with `token === null` while `getToken()` is also `null`, so it **passes the identity fence by equality**; the `anonymous` guard is the only thing that still stops it from churning state and clearing an empty cache on every attempt. Removing it would regress `LoginScreen.test.tsx` and the sign-in flow. Order the identity fence first (it is the cheaper, more specific question), but either order yields the same result.

### The 401 that carries no token

An unauthenticated request that 401s carries `null`. Three cases, all correct under the change:

| `getToken()` when the 401 lands | Result | Comment |
|---|---|---|
| `null`, status `anonymous` (sign-in screen) | identity passes, currency returns early | unchanged from today; `PasswordTab.auth.test.tsx` and `LoginScreen.test.tsx` stay green |
| `null`, status still `authenticated` (mid-teardown, request escaped after `clearToken()`) | identity passes, teardown runs again, idempotently | unchanged from today, and it converges: `clearSession` already did the same four things |
| `'tok_B'` (a new session exists) | identity **rejects** | new behaviour, and correct: a token-less request's 401 says nothing about session B |

### A 401 arriving during teardown

The item says three Phase 4 lanes confirmed `clearSession` converges. That claim **still holds**, but the mechanism changes and the plan must say so:

- **Today:** the listener runs during teardown and repeats `clearToken` / `setUser(null)` / `setStatus('anonymous')` / `queryClient.clear()`. Convergence comes from those four operations being idempotent.
- **After this change:** if the escaped request carried token A and `clearSession` has already run, `getToken()` is `null`, the identity fence rejects, and the listener does **nothing**. Convergence now comes from `clearSession` having already done all four things itself - which it does, synchronously, with `clearToken()` first (`AuthProvider.tsx:127-132`).

**Nothing needs to change for this to hold**, but it is not self-evidently safe, so Task 3 pins it with a test. The property to protect is: *no path clears the token without also clearing user, status and cache*. `clearToken()` is called at exactly three sites (`AuthProvider.tsx:79` the listener, `:98` the bootstrap catch, `:128` `clearSession`) and all three do the full teardown, so there is no site relying on the listener to finish a half-teardown.

### Every `onUnauthorized` registration site, enumerated before the signature changes

A widened callback signature is a silent-breakage risk, so this was checked exhaustively (`rg onUnauthorized web/`):

| Site | Kind | Effect of `type Listener = (token: string | null) => void` |
|---|---|---|
| `web/src/auth/AuthProvider.tsx:77` | **the only production consumer** | edited by Task 2 to take the parameter |
| `web/src/lib/api.test.ts:41` | test, `vi.fn()` | none - `vi.fn()` accepts any arguments |
| `web/src/lib/api.stream.test.ts:37` | test, `vi.fn()` | none - same |

There is **no third production subscriber**, and TypeScript accepts an existing `() => void` where `(token: string | null) => void` is expected (fewer parameters is always assignable), so the widening cannot break a caller even if one were missed. The remaining `onUnauthorized` hits in the tree are comments only (`profile/api.ts:44`, `:74`, `SessionsTab.tsx:37,47,57`, `PasswordTab.auth.test.tsx:61`, `useTaskLogStream.ts:336`, `SessionsTab.teardown.test.tsx:119,136`).

### `apiStream`: the same comparison is valid for a long-lived connection

`apiStream` reads the token at `:114`, attaches it at `:115`, and the 401 check at `:127-129` runs on the **initial response**, immediately after `await doFetch(...)` resolves and before any body is read. There is no second 401 later: once the stream is open, a mid-stream failure ends the body or errors the read loop, and recovery is the hook's job - `useTaskLogStream` calls `apiStream` **again**, which re-reads `getToken()` fresh (`useTaskLogStream.ts:332` calls `openStream`, and `:340` treats 401 as terminal, no retry). So the captured token is always exactly the credential that produced the 401, no matter how long the connection then lives. The comparison is valid, and it matters *more* here than for `apiFetch`: an SSE connection is by construction the most likely source of a late 401.

---

## Slice independence declaration

- **Backend slice: NONE. This is 100% `web/`. Zero Go files, zero `.sql` files, zero `.proto` files, zero migrations.** Therefore: no `make generate`, no `*.sql.go`, no `models.go`, no Go integration test. None of the backend Invariants is in play (the *frontend* form of Invariant 1 is, as reasoning - see the Design section).
- **Frontend slice: ONE `relay-frontend-engineer`, SEQUENTIAL.** Tasks 2 and 3 both depend on the token stamping introduced in Task 1, and Task 3's tests exercise the guard added in Task 2. Do not split across two engineers; do not run any task in parallel with another.
- **Phase 3 parallelism available to the conductor: none within this plan.**
- **Phase 4:** on a zero-Go diff the integration lane does not apply. The real-browser lane **does** have something to contribute here and it is unusually concrete: the repro in the backlog item is a browser flow (sign in, open a polled list page, sign out everywhere, sign in again immediately, watch whether the second login sticks). Ask for that, not for a jsdom re-run.
- **`web/dist`:** tracked but stale from the scaffold. **No task in this plan runs `npm run build`**, so nothing here should dirty it. If an engineer runs `npm run build` or `vite build` for any reason, `web/dist` will be rewritten - the conductor must `git checkout -- web/dist/` before assembling the PR. Verify with `git status --short web/dist` before committing.

---

## File Structure

**Modified - four files. Created - one file. Nothing else in `web/src`.**

| File | Change |
|---|---|
| `web/src/lib/api.ts` | `Listener` type widened (`:15`); `onUnauthorized` doc updated (`:18`); `fn(token)` at `:45` and `:128`; drifted citation corrected at `:94` |
| `web/src/auth/AuthProvider.tsx` | listener takes `requestToken` and fences on it (`:77-83`); the guard comment above it rewritten (`:72-74`); drifted `tokens.sql` citation corrected (`:34-35`) |
| `web/src/lib/api.test.ts` | one appended test |
| `web/src/lib/api.stream.test.ts` | one appended test; drifted citation corrected at `:46` |
| **`web/src/auth/AuthProvider.crossgen.test.tsx`** (new) | five tests: the discriminating `apiFetch` case, its positive control, the teardown-convergence guard, the discriminating `apiStream` case, its positive control |

**Why a new test file rather than appending to `AuthProvider.session.test.tsx`:** every test in the new file needs an in-flight request held open by an explicit gate plus a live `AuthProvider`, which is a different harness from the existing session file's click-a-button-and-assert shape. Keeping the discriminating test and its positive control adjacent in one file is the point - the pair is the evidence, and a reader must be able to see both at once.

### Files that must stay byte-identical

- `web/src/profile/PasswordTab.auth.test.tsx` - the item names `:83-99` as the existing positive control. It must stay green **and untouched**, including its stale comment at `:106`. Gate: `git diff --numstat -- web/src/profile/PasswordTab.auth.test.tsx` must print nothing at the end of the slice.
- `web/src/profile/SessionsTab.tsx`, `web/src/profile/SessionsTab.teardown.test.tsx`, `web/src/auth/AuthProvider.session.test.tsx`, `web/src/auth/AuthProvider.test.tsx`, `web/src/auth/LoginScreen.test.tsx`, `web/src/jobs/useTaskLogStream.ts` - **no edits.** If an assertion in any of them needs adjusting, **that is the finding, not the fix**: stop and report it.

---

## Scope guard - do NOT build

- **No `AbortSignal` and no abort-at-teardown.** The item explicitly rejects it and explains why: aborting without first ending the generation is CLAUDE.md Invariant 1 in its frontend form, the exact trap `web/src/jobs/useTaskLogStream.ts` was written from. Not in this slice, in any form.
- **No generation counter, no new ref, no new state in `AuthProvider`.** Option B is refuted above.
- **No change to `clearSession`, `logout`, `applyAuth`, `login`, `register` or the bootstrap effect.**
- **No removal of the `anonymous` guard.** It is the sign-in-screen fence and it is still load-bearing; see Design.
- **No change to `SessionsTab`, to any query hook, or to any polling interval.**
- **No new export from `lib/token.ts`.**
- **No `apiStream` retry-policy change.** 401 stays terminal in `useTaskLogStream`.
- **No fix for the `SessionsTab` "there is no GET /v1/auth/tokens" prose** - reported, not repaired here.

---

## Conventions for every task

- All `npm`/`npx` commands run from the `web/` directory of the worktree: `D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web`.
- Single file: `npx vitest run src/lib/api.test.ts`. Full suite: `npm test`.
- Commit from the worktree root: `D:/dev/relay/.claude/worktrees/happy-mendel-18687f`. Do **not** `cd D:/dev/relay` - that is a different checkout on `main`.
- TDD per step: write the failing test, run it and watch it fail **with the stated message**, implement, run it and watch it pass, commit.
- **House rule: never an em dash or en dash**, in code, comments, copy or this document. Plain ASCII hyphens only.
- **Plan-supplied test bodies are guesses until run.** Where a task says "expected RED", a green run before the implementation exists means the test is wrong - fix the test, do not proceed. Where a task says "guard, proven by mutation", run the named mutation and **record both outputs in the task report**.
- Never run `npm run build`. See the `web/dist` note above.

---

## Task 0: Baseline

- [ ] **Step 1: Record the pre-change suite size**

Run from `web/`: `npm test`

Expected: all green. **Write the exact "Tests N passed" number into the task report.** Every later "expected PASS, N + k" in this plan is relative to this number, and a slice that cannot state its own baseline cannot claim it added tests rather than replaced them.

- [ ] **Step 2: Confirm the tree is clean**

Run from the worktree root: `git status --short`

Expected: no output. In particular `web/dist` must not appear. If it does, `git checkout -- web/dist/` before starting.

---

## Task 1: Stamp the token on the 401 notification

`api.ts` learns to say *which* credential produced the 401. Nothing acts on it yet - `AuthProvider`'s listener still ignores the argument - so this task is **behaviour-neutral** and the whole existing suite must stay green.

**Files:**
- Modify: `web/src/lib/api.ts:15` (the type), `:18` (the doc), `:45` (the `apiFetch` fire site), `:128` (the `apiStream` fire site)
- Modify: `web/src/lib/api.test.ts` (append one test)
- Modify: `web/src/lib/api.stream.test.ts` (append one test)

- [ ] **Step 1: Write the two failing tests**

Append to `web/src/lib/api.test.ts`:

```ts
// The 401 notification must say WHICH credential produced it, or the subscriber
// cannot tell a 401 belonging to the session it holds from one belonging to a
// session that died two logins ago. See
// docs/superpowers/plans/2026-08-13-cross-generation-401.md.
test('the 401 listener receives the exact token the request carried', async () => {
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ error: 'invalid_credentials' }, { status: 401 }),
    ),
  )
  setToken('tok_stamped')
  const spy = vi.fn()
  const off = onUnauthorized(spy)
  await apiFetch('/users/me').catch(() => {})
  // toHaveBeenCalledWith, not just toHaveBeenCalled: fn() with no argument is the
  // pre-fix behaviour and passes any arity-blind assertion.
  expect(spy).toHaveBeenCalledWith('tok_stamped')
  off()
})

test('the 401 listener receives null when the request carried no token', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ error: 'invalid_credentials' }, { status: 401 }),
    ),
  )
  // No setToken: this is the failed-login-on-the-sign-in-screen shape, and the
  // subscriber must be able to see that the request was anonymous rather than
  // guess. null, explicitly - NOT undefined, which is what an unstamped fn()
  // would deliver and which would compare unequal to getToken()'s null.
  const spy = vi.fn()
  const off = onUnauthorized(spy)
  await apiFetch('/auth/login', { method: 'POST', json: {} }).catch(() => {})
  expect(spy).toHaveBeenCalledWith(null)
  off()
})
```

Append to `web/src/lib/api.stream.test.ts`:

```ts
// The streaming half of the same contract. An SSE connection is long-lived by
// construction, so it is the likeliest source of a 401 that outlives its own
// session - which makes stamping matter MORE here than on apiFetch.
test('the 401 listener receives the token the stream carried', async () => {
  const fake = fakeSseServer()
  fake.status = 401
  fake.errorBody = { error: 'unauthorized' }
  setToken('tok-stream')
  const seen = vi.fn()
  const off = onUnauthorized(seen)
  await expect(
    apiStream('/events?task_id=t1', {
      signal: new AbortController().signal,
      onEvent: () => {},
      fetchImpl: fake.fetchImpl,
    }),
  ).rejects.toBeInstanceOf(ApiError)
  expect(seen).toHaveBeenCalledWith('tok-stream')
  off()
})
```

**How these tests discriminate.** All three assert the **argument**, which is the entire delta. The existing `invokes the unauthorized handler on 401` (`api.test.ts:34-45`) and `a 401 fires the onUnauthorized listeners and does not retry` (`api.stream.test.ts:32-51`) already assert that the listener fires and are arity-blind; they stay green throughout and are the controls proving the fire site itself still works. The `null` test is not decoration: it pins that an anonymous request reports `null` and not `undefined`, and `undefined !== null` would make the identity fence in Task 2 reject a case it must accept.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/lib/api.test.ts src/lib/api.stream.test.ts`

Expected: **3 failing tests**, each at its `toHaveBeenCalledWith`, reporting the received call as `[]` (called with 0 arguments) against an expected `[ "tok_stamped" ]`, `[ null ]` and `[ "tok-stream" ]` respectively. Every other test in both files passes.

- [ ] **Step 3: Implement**

In `web/src/lib/api.ts`, replace line 15:

```ts
type Listener = () => void
```

with:

```ts
// The token the request actually attached, or null if it went out unauthenticated.
// It is the 401's IDENTITY: a subscriber must be able to tell a 401 belonging to
// the credential it holds now from one belonging to a session that is already
// dead. Widened from `() => void`; a zero-parameter callback is still assignable,
// so existing subscribers that do not care are unaffected.
type Listener = (token: string | null) => void
```

Replace the doc comment on line 18:

```ts
/** Register a callback fired whenever a request returns 401. Returns an unsubscribe fn. */
```

with:

```ts
/**
 * Register a callback fired whenever a request returns 401, with the token that
 * request carried. Returns an unsubscribe fn.
 *
 * The token argument is not optional context - it is what makes the notification
 * actionable. Firing bare would tell every subscriber only THAT a 401 happened,
 * so a 401 from a revoked credential would look identical to one from the live
 * session and could tear down a session issued seconds after it. Subscribers that
 * act on a 401 must compare this against the credential they hold; see
 * AuthProvider's onUnauthorized subscription.
 */
```

Replace lines 44-46 (the `apiFetch` fire site):

```ts
  if (res.status === 401) {
    unauthorizedListeners.forEach((fn) => fn(token))
  }
```

Replace lines 127-129 (the `apiStream` fire site):

```ts
  if (res.status === 401) {
    unauthorizedListeners.forEach((fn) => fn(token))
  }
```

Both sites pass the **local `token`** already read at `:32` and `:114` respectively. Do not re-read `getToken()` at either fire site: the point is the value the request went out with, and by the time a 401 lands the stored token may already be a different one - which is the entire bug.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/lib/api.test.ts src/lib/api.stream.test.ts`

Expected: PASS, all tests in both files.

Then run the whole suite: `npm test`

Expected: PASS, baseline + 3. This task is behaviour-neutral - `AuthProvider`'s listener declares no parameter, so it ignores the new argument. **If anything outside these two files goes red here, stop**: it means a subscriber exists that this plan did not enumerate.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/api.ts web/src/lib/api.test.ts web/src/lib/api.stream.test.ts
git commit -m "feat(web): the 401 notification carries the token the request used"
```

---

## Task 2: The identity fence in `AuthProvider`

The fix. One `if`, plus the comment that explains it.

**Files:**
- Modify: `web/src/auth/AuthProvider.tsx:72-85`
- Create: `web/src/auth/AuthProvider.crossgen.test.tsx`

- [ ] **Step 1: Write the failing test and its positive control**

Create `web/src/auth/AuthProvider.crossgen.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { apiFetch } from '../lib/api'
import { clearToken, getToken, setToken } from '../lib/token'
import { AuthProvider, useAuth } from './AuthProvider'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

// The in-flight request under test. It is held in a module-level binding rather
// than in component state ON PURPOSE: the test awaits this exact promise, so the
// interleaving is controlled by an explicit gate and a real await, never by a
// timer or a sleep. A timing-based version of this test would be flaky and this
// project has rejected those before.
let inflight: Promise<unknown> | null = null

function Probe() {
  const { status, user, login, clearSession } = useAuth()
  const [fired, setFired] = useState(0)
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="who">{user?.email ?? 'none'}</span>
      <span data-testid="fired">{fired}</span>
      <button
        onClick={() => {
          // apiFetch reads the token at call time, so this request is stamped with
          // whatever is stored RIGHT NOW - which is the whole point.
          inflight = apiFetch('/jobs/stats').catch(() => {})
          setFired((n) => n + 1)
        }}
      >
        fire
      </button>
      <button onClick={() => clearSession()}>clear</button>
      <button onClick={() => void login('mira@studio.dev', 'pw').catch(() => {})}>login</button>
    </div>
  )
}

function renderProbe() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <AuthProvider>
        <Probe />
      </AuthProvider>
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

/** A 401 for /jobs/stats that does not resolve until the returned release() is called. */
function gated401() {
  let release!: () => void
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  const tokensSeen: (string | null)[] = []
  server.use(
    http.get('/v1/jobs/stats', async ({ request }) => {
      tokensSeen.push(request.headers.get('Authorization'))
      await gate
      return HttpResponse.json({ error: 'invalid token' }, { status: 401 })
    }),
  )
  return { release: () => release(), tokensSeen }
}

test('a 401 from a DEAD session does not clear the session that replaced it', async () => {
  // THE discriminating case, and the reason this file exists. Proven RED against
  // a listener whose only guard is the anonymous check: there, the late 401 calls
  // clearToken() on token B and the user watches a successful login undo itself
  // with no error message.
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.post('/v1/auth/login', () => HttpResponse.json({ token: 'tok_B', expires_at: '' })),
  )
  setToken('tok_A')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  const { release, tokensSeen } = gated401()
  await userEvent.click(screen.getByText('fire'))
  // Positive control on the SETUP: the held request really went out, and it really
  // carried token A. Without this the test could pass because no request was ever
  // made, which is the vacuous version of it.
  await waitFor(() => expect(tokensSeen).toEqual(['Bearer tok_A']))

  // Generation A ends, generation B begins - the sign-out-everywhere shape.
  await userEvent.click(screen.getByText('clear'))
  await waitFor(() => expect(getToken()).toBeNull())
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(getToken()).toBe('tok_B'))
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('authenticated'))

  // Only NOW does the dead session's 401 land.
  release()
  await act(async () => {
    await inflight
  })

  // Session B survives, in both stores. getToken() is the load-bearing one: the
  // listener's clearToken() is synchronous, so this assertion needs no React
  // commit and cannot pass by racing one.
  expect(getToken()).toBe('tok_B')
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
  expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev')
})

test('POSITIVE CONTROL: a 401 for the CURRENT token still tears the session down', async () => {
  // The other half of the pair, in the same file and the same harness as the test
  // above, so a reader sees both at once. Without it, "the 401 was ignored" would
  // also be satisfied by a listener that ignores every 401 - which is the single
  // most likely way to get this fix wrong.
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  setToken('tok_A')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  const { release, tokensSeen } = gated401()
  await userEvent.click(screen.getByText('fire'))
  await waitFor(() => expect(tokensSeen).toEqual(['Bearer tok_A']))

  // No teardown, no re-login: token A is still the session when the 401 lands.
  release()
  await act(async () => {
    await inflight
  })

  expect(getToken()).toBeNull()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  expect(screen.getByTestId('who')).toHaveTextContent('none')
})

test('a 401 landing DURING teardown still leaves everything torn down', async () => {
  // The convergence property the backlog item said three Phase 4 lanes confirmed.
  // It still holds, but for a DIFFERENT reason after this change: the listener no
  // longer runs at all here (the token is already gone, so the identity fence
  // rejects), and convergence now rests entirely on clearSession having done all
  // four things itself, synchronously, with clearToken() first
  // (AuthProvider.tsx:127-132). Pin it, because that is a load-bearing dependency
  // the fix silently acquired.
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  setToken('tok_A')
  const { client } = renderProbe()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))
  client.setQueryData(['workers'], [{ id: 'w1' }])

  const { release, tokensSeen } = gated401()
  await userEvent.click(screen.getByText('fire'))
  await waitFor(() => expect(tokensSeen).toEqual(['Bearer tok_A']))

  await userEvent.click(screen.getByText('clear'))
  release()
  await act(async () => {
    await inflight
  })

  expect(getToken()).toBeNull()
  expect(screen.getByTestId('status')).toHaveTextContent('anonymous')
  expect(screen.getByTestId('who')).toHaveTextContent('none')
  expect(client.getQueryCache().getAll()).toHaveLength(0)
})
```

**How these tests discriminate - one named mutation each, and which SINGLE test each reddens:**

| Mutation | Reddens | Stays green |
|---|---|---|
| Delete the identity fence line (today's code) | **`a 401 from a DEAD session does not clear the session that replaced it`**, and only it in this file | the positive control and the convergence test - both end torn down either way |
| Make the fence unconditional (`return` before the four statements) | **`POSITIVE CONTROL: a 401 for the CURRENT token still tears the session down`**, and only it in this file | the discriminating test - it wants the listener to do nothing |
| Revert `api.ts:45` to bare `fn()` (so the listener receives `undefined`) | **the positive control** (`undefined !== 'tok_A'`, so nothing tears down) plus `api.test.ts`'s two argument tests | the discriminating test - `undefined !== 'tok_B'` also rejects, for the wrong reason |
| Compare against a `useRef` mirror of the token instead of `getToken()` | none of these three | - **and that is the honest limit of this file.** The ref-lag hazard lives in the window between `setToken` and the status commit inside `applyAuth`; it is argued in the Design section and is not covered by a test. Do not claim it is. |

The positive control is deliberately **redundant** with `PasswordTab.auth.test.tsx:83-99`, which asserts the same property through a different component and a different harness. That is a cross-file regression guard, not shared-setup coupling: neither test can mask a failure of the other, since they share no fixture, no render helper and no handler.

- [ ] **Step 2: Run the tests to verify the first one fails**

Run: `npx vitest run src/auth/AuthProvider.crossgen.test.tsx`

Expected: **1 failing test.** `a 401 from a DEAD session does not clear the session that replaced it` fails at `expect(getToken()).toBe('tok_B')` with received `null`. The other two pass - the positive control and the convergence test both describe behaviour today's code already has, which is exactly why the first test is the one that carries the slice.

**Record this output in the task report.** It is the record that the defect was real and that the fix is what closed it.

- [ ] **Step 3: Implement**

In `web/src/auth/AuthProvider.tsx`, replace lines 72-85 (the comment and the `useEffect`) with:

```tsx
  // Reset auth state on any 401 so the route guards send the user to sign-in.
  //
  // TWO fences, answering two different questions. This is CLAUDE.md's rule in its
  // frontend form: a status check establishes CURRENCY, never IDENTITY. The backend
  // learned it on tasks.status writes, where a matching assignment_epoch proves the
  // caller's generation is current and proves nothing about who the caller is, so
  // every such write also fences on worker_id. Until 2026-08-13 this listener had
  // the currency half and not the identity half.
  //
  // IDENTITY - requestToken !== getToken(). apiFetch and apiStream stamp each 401
  // with the token that request actually attached (lib/api.ts). Without this fence
  // a 401 produced by an ALREADY DEAD credential clears whatever token happens to
  // be in localStorage when it lands, including one issued seconds earlier by a
  // fresh login: sign out everywhere, sign back in, and a straggler 401 from a
  // still-in-flight poll silently undoes the new session with no error message.
  // Nothing cancels in-flight requests at teardown - apiFetch passes no
  // AbortSignal, and queryClient.clear() evicts cached data without aborting a
  // request already on the wire - so that straggler is guaranteed, not theoretical.
  //
  // The comparison reads getToken() FRESH rather than a ref, deliberately.
  // localStorage is the credential's single source of truth and setToken/clearToken
  // write it synchronously, whereas any React-committed mirror lags: applyAuth
  // stores the new token and only THEN awaits /users/me, so a mirror would still
  // say "old" through that whole window and would reject a 401 belonging to the
  // brand-new session - reintroducing this same bug through its own fix.
  //
  // Comparing by VALUE covers replacement as well as removal, so no session
  // generation counter is needed: a token is 32 random bytes (CLAUDE.md, "Token
  // format"), so a later session never reuses an earlier one's string.
  //
  // A 401 arriving DURING clearSession() fails this fence - the token is already
  // gone - and correctly does nothing: clearSession already did all four of the
  // statements below, synchronously, with clearToken() first (:127-132).
  //
  // CURRENCY - statusRef.current === 'anonymous'. Still load-bearing, and it is
  // NOT made redundant by the fence above: a failed login on the sign-in screen
  // sends a request with no token while getToken() is also null, so it passes the
  // identity fence BY EQUALITY. This guard is the only thing that stops it churning
  // state and clearing an empty cache on every attempt.
  useEffect(
    () =>
      onUnauthorized((requestToken) => {
        if (requestToken !== getToken()) return
        if (statusRef.current === 'anonymous') return
        clearToken()
        setUser(null)
        setStatus('anonymous')
        queryClient.clear()
      }),
    [queryClient],
  )
```

No other change to the file in this task. The import on `:4` already brings in `getToken`.

- [ ] **Step 4: Run the tests to verify they pass, then run both mutations**

Run: `npx vitest run src/auth/AuthProvider.crossgen.test.tsx`

Expected: PASS, 3 tests.

Run the two auth-adjacent suites that must not regress:

Run: `npx vitest run src/auth src/profile`

Expected: PASS. In particular `PasswordTab.auth.test.tsx`'s `a 401 from the same endpoint DOES tear the session down` and `LoginScreen.test.tsx` must be green **untouched**. If `LoginScreen` goes red, the `anonymous` guard was dropped; if `PasswordTab`'s 401 control goes red, the fence is comparing the wrong things.

Now prove the pair discriminates in both directions. **Run each mutation, record the exact failure output, then revert it.**

**Mutation A - remove the identity fence.** Delete the `if (requestToken !== getToken()) return` line.

Run: `npx vitest run src/auth/AuthProvider.crossgen.test.tsx`
Expected: FAIL in `a 401 from a DEAD session does not clear the session that replaced it`, at `expect(getToken()).toBe('tok_B')`, received `null`. The other two pass. **Revert.**

**Mutation B - fence everything out.** Replace the fence with a bare `return` as the first statement of the listener body.

Run: `npx vitest run src/auth/AuthProvider.crossgen.test.tsx`
Expected: FAIL in `POSITIVE CONTROL: a 401 for the CURRENT token still tears the session down`, at `expect(getToken()).toBeNull()`, received `'tok_A'`. The discriminating test still passes - say so in the report, it is why the pair exists. **Revert.**

Re-run after reverting and confirm 3 passing.

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/AuthProvider.tsx web/src/auth/AuthProvider.crossgen.test.tsx
git commit -m "fix(web): a 401 only tears down the session whose token produced it"
```

---

## Task 3: The `apiStream` half, end to end

Task 1 stamped the stream's 401 and Task 2 fenced the listener, so the stream path is **already fixed** - these two tests pass on arrival. They are guards, not RED-driven work, and they earn their place through the mutation in Step 3. They exist because the acceptance criteria name `apiStream` explicitly and because an SSE connection is the likeliest real-world source of a late 401: the `apiFetch` tests would still pass if someone later reverted only `api.ts:128`.

**Files:**
- Modify: `web/src/auth/AuthProvider.crossgen.test.tsx` (append two tests and one import)

- [ ] **Step 1: Write the guard tests**

Change the `apiFetch` import at the top of `web/src/auth/AuthProvider.crossgen.test.tsx` to:

```tsx
import { ApiError, apiFetch, apiStream } from '../lib/api'
```

Append to the same file:

```tsx
/**
 * A 401 SSE response that does not resolve until release() is called. Written
 * inline rather than through fakeSseServer() because that helper answers
 * immediately and this test's whole subject is WHEN the 401 lands. The signal is
 * never consulted: apiStream throws on the 401 before it reaches the read loop.
 */
function gatedStream401() {
  let release!: () => void
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  const tokensSeen: (string | null)[] = []
  const fetchImpl = (async (_url: string, init?: RequestInit) => {
    tokensSeen.push(new Headers(init?.headers).get('Authorization'))
    await gate
    return new Response(JSON.stringify({ error: 'unauthorized' }), {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as unknown as typeof fetch
  return { release: () => release(), tokensSeen, fetchImpl }
}

test('a stream 401 from a DEAD session does not clear the session that replaced it', async () => {
  // The streaming half of the discriminating case. An SSE connection is long-lived
  // by construction, so it outlives its own session more readily than any polled
  // request does - it is the single most likely source of a cross-generation 401.
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.post('/v1/auth/login', () => HttpResponse.json({ token: 'tok_B', expires_at: '' })),
  )
  setToken('tok_A')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  const { release, tokensSeen, fetchImpl } = gatedStream401()
  const streamed = apiStream('/events?task_id=t1', {
    signal: new AbortController().signal,
    onEvent: () => {},
    fetchImpl,
  }).catch((e) => e)
  // Positive control on the setup: the stream really opened, carrying token A.
  await waitFor(() => expect(tokensSeen).toEqual(['Bearer tok_A']))

  await userEvent.click(screen.getByText('clear'))
  await waitFor(() => expect(getToken()).toBeNull())
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(getToken()).toBe('tok_B'))

  release()
  await act(async () => {
    expect(await streamed).toBeInstanceOf(ApiError)
  })

  expect(getToken()).toBe('tok_B')
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
})

test('POSITIVE CONTROL: a stream 401 for the CURRENT token still tears the session down', async () => {
  // Without this, the test above is also satisfied by an apiStream that stopped
  // notifying anybody at all - which is exactly what reverting api.ts:128 to a bare
  // fn() produces, since the listener would then compare undefined against a live
  // token and reject every stream 401 forever.
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  setToken('tok_A')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  const { release, tokensSeen, fetchImpl } = gatedStream401()
  const streamed = apiStream('/events?task_id=t1', {
    signal: new AbortController().signal,
    onEvent: () => {},
    fetchImpl,
  }).catch((e) => e)
  await waitFor(() => expect(tokensSeen).toEqual(['Bearer tok_A']))

  release()
  await act(async () => {
    expect(await streamed).toBeInstanceOf(ApiError)
  })

  expect(getToken()).toBeNull()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
})
```

**How these tests discriminate.** Both pass on arrival - **a green run here is expected and is not evidence of anything.** Step 3's mutation is the evidence:

| Mutation | Reddens | Note |
|---|---|---|
| Revert `api.ts:128` to bare `fn()` | **`POSITIVE CONTROL: a stream 401 for the CURRENT token...`** in this file, plus `the 401 listener receives the token the stream carried` in `api.stream.test.ts` | Two tests, in two files, through two different instruments (an end-to-end session teardown, and a spy argument). Not shared-setup coupling: they share no fixture and no harness, and the pair is what distinguishes "the stream stopped stamping" from "the stream stopped notifying". |
| Delete the identity fence | the stream discriminating test **and** the `apiFetch` discriminating test | Correct and expected - it is one fence serving both transports, not two mechanisms. |

- [ ] **Step 2: Run the tests**

Run: `npx vitest run src/auth/AuthProvider.crossgen.test.tsx`

Expected: PASS, 5 tests. If either new test fails, the plan-supplied body is wrong - fix the test, and check first that `tokensSeen` really recorded `Bearer tok_A` (if it recorded `null`, the render helper ran before `setToken`).

- [ ] **Step 3: Run the mutation and record the output**

**Mutation C - unstamp the stream.** In `web/src/lib/api.ts:128`, change `fn(token)` back to `fn()`.

Run: `npx vitest run src/auth/AuthProvider.crossgen.test.tsx src/lib/api.stream.test.ts`
Expected: **2 failing tests** - `POSITIVE CONTROL: a stream 401 for the CURRENT token still tears the session down` at `expect(getToken()).toBeNull()` with received `'tok_A'`, and `the 401 listener receives the token the stream carried` at its `toHaveBeenCalledWith`. Note that `apiFetch`'s tests all stay green under this mutation, which is the point of having a stream-specific pair. **Revert.**

Re-run after reverting and confirm 5 passing.

- [ ] **Step 4: Commit**

```bash
git add web/src/auth/AuthProvider.crossgen.test.tsx
git commit -m "test(web): the stream 401 path honours session identity too"
```

---

## Task 4: Correct the drifted citations in the files this slice touched

No behaviour changes. Three comment corrections found while verifying the backlog item, confined to files this slice already edits. A wrong contract stated in prose is a defect on this project: consumers implement against the prose and no test covers it.

**Files:**
- Modify: `web/src/auth/AuthProvider.tsx:34-35`
- Modify: `web/src/lib/api.ts:94`
- Modify: `web/src/lib/api.stream.test.ts:46`

- [ ] **Step 1: Fix the `tokens.sql` line reference in `AuthProvider.tsx`**

At `web/src/auth/AuthProvider.tsx:34-35`, the comment reads:

```
  // is DeleteTokensForUser, `DELETE FROM api_tokens WHERE user_id = $1`
  // (internal/store/query/tokens.sql:25-26), with no `id <> $2`. Any request made
```

`DeleteTokensForUser` moved to `tokens.sql:40-41` when PR #125 added the `DeleteToken` doc block and the list statements. Change `:25-26` to `:40-41`. Change nothing else on those lines - the statement text and the `id <> $2` claim are both still correct.

- [ ] **Step 2: Fix the two `AuthProvider.tsx:39-49` references**

The `onUnauthorized` subscription is at `AuthProvider.tsx:75-85` (and grows in Task 2), not at `:39-49`, which is the middle of the `clearSession` doc comment. Both replacements drop the line numbers rather than updating them: this citation has now drifted twice, and the surrounding comment is about to change size again, so name the symbol instead.

At `web/src/lib/api.ts:93-94`, change:

```
 * attached (token.ts:3-5) is attached in exactly one place, and a streaming 401 fires the same
 * onUnauthorized notifier AuthProvider subscribes to (AuthProvider.tsx:39-49).
```

so the second line reads:

```
 * onUnauthorized notifier AuthProvider subscribes to (its onUnauthorized effect).
```

At `web/src/lib/api.stream.test.ts:46`, change:

```
  // redirect to sign-in (AuthProvider.tsx:39-49 is the subscriber).
```

to:

```
  // redirect to sign-in (AuthProvider's onUnauthorized effect is the subscriber).
```

- [ ] **Step 3: Verify nothing else moved**

Run: `npx vitest run src/lib src/auth`

Expected: PASS. Comments only - if anything changed status here, the edit strayed outside a comment.

Run from the worktree root: `git diff --numstat -- web/src/profile/`

Expected: **no output.** The `profile/` directory is untouched by this slice, and `PasswordTab.auth.test.tsx` in particular must be byte-identical so that its green run is clean evidence.

- [ ] **Step 4: Commit**

```bash
git add web/src/auth/AuthProvider.tsx web/src/lib/api.ts web/src/lib/api.stream.test.ts
git commit -m "docs(web): correct drifted file:line citations in the 401 path"
```

---

## Task 5: Full gate and close the backlog item

- [ ] **Step 1: Full web suite**

Run from `web/`: `npm test`

Expected: PASS, **baseline + 8** (2 in `api.test.ts`, 1 in `api.stream.test.ts`, 5 in `AuthProvider.crossgen.test.tsx` - which is 8; state the arithmetic explicitly in the report and reconcile it against the number Task 0 recorded. If the total does not match baseline + 8, a test was replaced rather than added: find out which).

- [ ] **Step 2: Go gate, as a no-regression check only**

Run from the worktree root:

```bash
go build ./...
go test ./...
```

Expected: PASS. **Zero Go files change in this slice**; this runs only to prove that. If `make` is not on PATH in the shell, use these commands directly rather than `make build` / `make test`.

- [ ] **Step 3: Confirm the diff is exactly the intended file set**

Run from the worktree root: `git status --short && git diff --stat origin/main`

Expected: exactly five paths - `web/src/lib/api.ts`, `web/src/lib/api.test.ts`, `web/src/lib/api.stream.test.ts`, `web/src/auth/AuthProvider.tsx`, `web/src/auth/AuthProvider.crossgen.test.tsx` - plus, after the next step, the backlog file's move. **`web/dist` must not appear.** If it does, `git checkout -- web/dist/`.

- [ ] **Step 4: Close the backlog item**

Run: `/backlog close bug-2026-08-13-cross-generation-401-clears-a-new-session`

The command `git mv`s the file into `docs/backlog/closed/`, stamps the frontmatter and appends a Resolution note, then commits. Never hand-edit `status:`.

**The Resolution must record two things**, or the next reader inherits a wrong belief:

1. Option **A** shipped, and the item's stated advantage for option B - that only B covers a token that was *replaced* rather than removed - is **false**. A compares token values, so replacement and removal are the same rejection. No session generation counter exists, and one should not be added on that argument.
2. The item's citation of `DeleteTokensForUser` at `internal/store/query/tokens.sql:25-26` had drifted to `:40-41` (and `DeleteOtherTokensForUser` from `:28-29` to `:43-44`). The behaviour claim was correct; only the coordinates were stale.

---

## Findings for the conductor - not fixed by this plan

1. **Stale `tokens.sql` coordinates in five more shipped files.** `web/src/profile/api.ts:52`, `:67`, `:70`, `web/src/profile/SessionsTab.tsx:36`, `:81`, `web/src/profile/PasswordTab.auth.test.tsx:106` all cite `tokens.sql:25-26` / `:28-29`; the statements are now at `:40-41` and `:43-44`. Deliberately excluded here so `profile/` stays byte-identical and `PasswordTab.auth.test.tsx`'s green run is clean evidence.
2. **Stale listener coordinates in `web/src/jobs/useTaskLogStream.ts:337`** (`AuthProvider.tsx:39-49`). Same drift as the two this slice fixes; excluded because the file is otherwise untouched.
3. **`SessionsTab` asserts a capability gap that no longer exists.** `web/src/profile/SessionsTab.tsx:11-12` and the user-visible paragraph at `:113-121` both state "there is no `GET /v1/auth/tokens`". `internal/api/server.go:103` registers exactly that endpoint (`handleListTokens`, PR #125). This is a user-facing wrong statement plus a now-satisfiable UI omission, and it deserves its own item.
4. **The ref-lag hazard is argued, not tested.** The Design section explains why the fence must read `getToken()` rather than a React-committed mirror; no test in this slice would go red against a ref-based implementation whose mirror happened to be updated synchronously. Named as untestable-here rather than glossed.

---

## Self-review

**Acceptance coverage:**

| Acceptance criterion | Task |
|---|---|
| Discriminating test, proven RED first | Task 2 Step 2 (`apiFetch`), Task 3 Step 3 mutation (`apiStream`) |
| Positive control survives: a 401 for the current token still tears down; `PasswordTab.auth.test.tsx:83-99` stays green | Task 2 Step 1 (new control) and Step 4 (`npx vitest run src/auth src/profile`); byte-identity gate in Task 4 Step 3 |
| `apiStream`'s 401 path gets the same treatment | Task 1 (`api.ts:128`), Task 3 (both stream tests) |
| The mechanism is documented at `AuthProvider`'s listener | Task 2 Step 3 |

**Type consistency:** `Listener` is `(token: string | null) => void` in Task 1 and the listener's parameter is named `requestToken` in Task 2 - a rename at the call site, not a type mismatch. `gated401()` returns `{ release, tokensSeen }` and `gatedStream401()` returns `{ release, tokensSeen, fetchImpl }`; both are destructured exactly as returned. `inflight` is declared once at module scope in Task 2 and reused in Task 3 only through `apiFetch`-based tests (the stream tests hold their own `streamed` binding).
