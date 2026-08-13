---
date: 2026-08-12
topic: profile-pages
branch: claude/pr-merge-session-f5796e
range: origin/main..HEAD (green, not yet merged)
---

# Session Retro: 2026-08-12 - Profile Pages (Identity / Password / Sessions)

**TL;DR:** Shipped `/profile/:tab` behind the three dead `UserMenu` links - Identity (name-only
rename), Password (three client guards), Sessions (one action, no list) - closing
`feature-2026-06-26-profile-identity-password-sessions`. 100% frontend; the Go diff is empty for
the second iteration running. Three things distinguish this one. First, **the spec overturned two
claims it was handed** - the conductor's hypothesis that the password change logs you out, and the
hi-fi's "Sign out everywhere **else**" label - which makes this the fifth consecutive iteration
where the backlog item's own Proposal was wrong about the thing it proposed. Second, **the
conductor's `/code-review` was wrong about the code and right about the prose**: it flagged
`clearSession`'s ordering, all three lenses refuted the code claim by three independent mechanisms,
and the invariants lane then measured the accompanying comment and found *it* false. Third, **the
implementing engineer's own evidence survived third-party re-execution intact** - the correctness
lane re-ran all five mutation proofs and every one reddened with the claimed message. That is the
first time in this arc the implementer's evidence has held up under re-execution rather than being
partially refuted.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-12-profile-pages.md`, **plan**
  `docs/superpowers/plans/2026-08-12-profile-pages.md` (11 sequential tasks, one frontend engineer,
  no parallelism: Tasks 3-7 import Tasks 1-2, Task 8's registry imports the three panels, and
  Tasks 1 and 10 both write into shipped shared modules).
- Two new routes in `web/src/app/router.tsx:46-47`, inside `ProtectedRoute` and deliberately **not**
  `AdminRoute`: `/profile` renders `<Navigate to="/profile/identity" replace/>` and `/profile/:tab`
  renders `ProfilePage`. Every endpoint behind the page is `auth(...)`, never `AdminOnly`, so gating
  the page on admin would lock out exactly the users who need it.
- Seven new source files under `web/src/profile/`: `ProfilePage.tsx`, `ProfileTabs.tsx`, `tabs.ts`,
  `IdentityTab.tsx`, `PasswordTab.tsx`, `SessionsTab.tsx`, `api.ts`, plus nine test files including
  two split out on purpose so they are findable (`PasswordTab.auth.test.tsx`,
  `SessionsTab.teardown.test.tsx`).
- `web/src/auth/AuthProvider.tsx` gains `applyUser` (`:134-136`) and `clearSession` (`:127-132`);
  `logout()` is re-expressed through `clearSession` (`:138-141`) with a byte-identical exported
  signature. The 401 listener at `:75-85` was deliberately **not** routed through `clearSession` -
  it carries a `statusRef` no-op guard that `clearSession` must not have.
- `web/src/lib/types.ts` - `User` gains a required `created_at: string`.
- `web/src/components/Field.tsx` - the error text gains `role="alert"` and an `id` pushed onto the
  control's `aria-describedby` via `cloneElement` (`:19-27,38-42`). This was a Phase 4 fix, not a
  planned change.
- `web/src/app/JobsPlaceholder.tsx` deleted. Its only importer was the `/profile/*` splat this slice
  replaced; the one surviving mention in the tree is `ProfileRoutes.test.tsx:64`, which asserts its
  text is unreachable.
- Tests 890 -> 959 (reported by the lanes; this pass had no shell, so the count is not
  independently re-run here - see Verification).

## Key Decisions

- **Sessions ships the action without the list, and the reasoning is not the precedent it looks
  like.** Two shipped precedents omitted controls whose endpoints did not exist: the enrollments
  tab's revoke (`EnrollmentsTab.tsx:197-205`) and the server tab's VERSION/BUILD strip
  (`AdminPage.tsx:6-14`). Neither faced this case. Here the *action* works -
  `DELETE /v1/auth/tokens` is a live, auth-gated, idempotent 204 (`internal/api/auth.go:350-357`) -
  and only the *list* is unsupported. The house rule is "omit what the backend cannot supply, and
  file the enabler"; applied at the granularity of the **control** rather than the **tab**, it drops
  the list and keeps the action. Dropping a working security control because an unrelated read
  endpoint is missing would be over-applying it.

  Three supporting reasons, recorded because the outcome alone does not carry them. (1) The list is
  much further away than the enabler item claims: `api_tokens` has exactly five columns - `id`,
  `user_id`, `token_hash`, `created_at`, `expires_at` (`migrations/000001_initial.up.sql:13-19`) - so
  `last_used_at`, agent, IP and location are a migration, not a query. (2) Omitting the tab entirely
  would have made `UserMenu`'s third link *resolve* to a page it does not name, which is a link that
  lies rather than a link that is dead. (3) The no-list shape is what makes the teardown safe: with
  no query on the tab there is no active observer to refire against a destroyed token. A Sessions
  *list* would have had to solve that ordering problem; a Sessions *action* does not have it.
  The whole argument is written at `SessionsTab.tsx:9-25` so the next person does not re-derive it.

- **The control is labelled `Sign out everywhere`, never the hi-fi's "everywhere else".**
  `DeleteTokensForUser` is `DELETE FROM api_tokens WHERE user_id = $1` with no `id <> $2`
  (`internal/store/query/tokens.sql:25-26`), so it takes the caller's own token too. The hi-fi says
  "else" twice and reads as authoritative. A control that understates its own blast radius is a
  defect, not a copy nit, and the security lane verified the shipped copy against the SQL rather
  than against the spec.

- **The password change does NOT log you out, and the brief said it might.**
  `PUT /v1/users/me/password` calls `DeleteOtherTokensForUser` with the caller's `TokenID`
  (`auth.go:325-328` -> `tokens.sql:28-29`, `AND id <> $2`). The two endpoints on this surface are
  exact opposites, and `PasswordTab.auth.test.tsx:101-116` and `SessionsTab.teardown.test.tsx` are
  written as each other's controls for precisely that reason.

- **On a 204 from sign-out-everywhere the SPA tears its own session down rather than waiting to be
  401ed.** A 204 fires no listener (`lib/api.ts:44-46` is 401-only), so without this the shell keeps
  rendering as authenticated against a credential the server has already destroyed. `logout()` was
  explicitly rejected for this: it would first fire `DELETE /v1/auth/token` against a token that no
  longer exists, a guaranteed 401 racing the teardown already in flight. A dedicated test asserts the
  singular DELETE is never issued.

- **No `invalidateQueries` anywhere in `web/src/profile/`, and the omission is stated at the site.**
  The house pattern for a mutation is `onSuccess: () => qc.invalidateQueries(...)`. Here that would
  refetch every mounted query against a destroyed credential, before anything unmounts, because a
  hook-level `onSuccess` resolves ahead of the success dispatch. The plan's scope guard banned it
  outright and `SessionsTab.tsx:42-53` explains why. This is the previous retro's "an invalidation is
  a continuation" lesson reaching the point of authorship rather than the point of review.

- **Zero polling, zero timers, zero background requests.** Identity data is already resident in
  `AuthProvider`; there is no list to keep fresh. Confirmed empirically by the browser lane over 30s.

- **`applyUser` pushes the PATCH 200 body into `AuthProvider` instead of adding a second `['me']`
  query.** The response is the same `userResponse` struct `GET /v1/users/me` returns (`users.go:429`
  and `:410` both call `toUserResponse`), so it is authoritative and needs no confirming round trip.
  One owner of identity, not two caches that can disagree.

- **The tab shell is a local copy, not an extraction.** `web/src/profile/tabs.ts` is the **second**
  consumer of the registry-plus-switch shape (`admin/tabs.ts` is the first) and the house rule is
  extract before the third. Recorded at `tabs.ts:12-24`. The min-8 password guard reached its
  **fourth** site and was still copied rather than extracted: two lines with no decision inside them
  become indirection when hidden behind a helper.

- **The detail-page state triad countdown was checked and deliberately not advanced.** This page
  fetches no resource by id, has no 404 state, renders no loading panel and no retryable error card,
  so `idea-2026-08-12-detail-page-state-triad-primitive` gains no consumer here. Checked explicitly
  so the item's third-consumer deviation does not silently become a fourth.

## Problems Encountered

1. **The settled save clobbered a newer edit, and the comment asserted the opposite.**
   `IdentityTab`'s `onSuccess` cleared the draft unconditionally, so a keystroke landing while a slow
   PATCH was in flight was overwritten when it settled. The correctness lane probed it: type "Alpha",
   Save, type "Beta" mid-flight, and the field reverts to "Alpha" when the response lands. The
   original comment claimed the draft was "NEVER re-derived from `user`", which was true of the
   render path and false of the settled continuation. Fixed by making the release conditional on the
   draft still equalling what *this* save submitted (`IdentityTab.tsx:44`).

   This is the same shape as the epoch fence: the settled response establishes **content**, the
   current draft establishes **intent**, and a continuation must prove its generation is still
   current before writing. The previous retro's version of this was "read the route param and the
   rendered row as one question". Same rule, no route param in sight.

2. **`save.reset()` sat after the no-op early return, stranding a stale banner in both directions.**
   Retyping the original name after a 400 left the error banner up while issuing no request, and
   clicking Save again after a success left the success banner up. Fixed by hoisting `reset()` above
   the `trimmed === user.name` return (`IdentityTab.tsx:55-66`). This is the same defect the schedule
   slice fixed in `ScheduleTriggerForm` one iteration ago, in a different file, written by the same
   agent role. The lesson did not transfer, which says the ordering rule needs to live somewhere an
   author reads, not only in a retro.

3. **The client guard failures were never announced, while the server error in the same form was.**
   `PasswordTab` routed all three guards through one shared error string, which put the min-8 and
   72-byte messages under *Confirm new password* - the wrong control - and `Field` gave the error
   text no `role="alert"`, so nothing announced it. The server's 403 in the same form *was*
   announced, because it rendered through the component's own `role="alert"` div. So the two error
   surfaces in one form had opposite accessibility behaviour. Fixed with two separate error slots
   (`PasswordTab.tsx:20-26`) and by giving `Field` `role="alert"` plus `aria-describedby`
   (`Field.tsx:19-42`).

4. **The `clearSession` comment was false, and the finding that led there was wrong about the code.**
   The conductor's `/code-review` flagged `clearSession`'s ordering as a possible Invariant 1
   violation, marked PLAUSIBLE rather than confirmed. All three lenses refuted it, independently and
   by three different mechanisms: a probe showing `requests AFTER the DELETE = []`; the observation
   that `onUnauthorized` converges on identical state and is no-op-guarded on `statusRef`
   (`AuthProvider.tsx:78`); and that `clearSession` is fully synchronous, so no callback can
   interleave. But the invariants lane then measured the comment's stated *mechanism* and found it
   false: `queryClient.clear()` does not stop refetch intervals (`calls at clear() = 7`, `calls 300ms
   later with the observer still mounted = 28`). What actually guards the teardown is `clearToken()`
   running first, so an escaped request carries no Authorization header, plus the `ProtectedRoute`
   unmount that `setStatus('anonymous')` eventually causes. The corrected mechanism is now written
   out at `AuthProvider.tsx:40-56`.

   The generalizable part: a finding can be wrong about the code and right about the prose, and the
   only way to tell is to measure the mechanism rather than reason about it. Three refutations of the
   code claim did not close the question, because none of them was a claim about what
   `queryClient.clear()` does.

5. **The plaintext password survived in the settled mutation's closure, not its variables - and the
   existing test structurally could not catch it.** The engineer had correctly defended
   `state.variables` by passing no variables to `mutate()`, and the test asserted
   `JSON.stringify(m.state)` did not contain the secret. But `@tanstack/query-core`'s
   `mutationObserver.js` builds the cached mutation from `this.options`, and post-success re-renders
   do not replace it, so the `mutationFn` closure over `current`/`next` kept the plaintext reachable
   from `queryClient.getMutationCache().getAll()` for the default 5-minute `gcTime`. Stringifying
   `state` can never see a closure. Fixed with `gcTime: 0` plus a synchronous `change.reset()` in
   `onSuccess`, and the test now asserts the **cache itself goes empty**
   (`PasswordTab.auth.test.tsx:160-166`) with the pending-window positive control moved *earlier* so
   it is not measuring an already-evicted entry (`:142-155`).

   This is the standing "calling the clear function is not evidence" lesson one level deeper: the
   correct instrument was not the inputs, and it was not `state` either. It was the store's
   occupancy.

6. **The fix for finding 5 caused a real regression, and only a pre-existing assertion caught it.**
   A synchronous `change.reset()` inside `onSuccess` detaches the observer *before* query-core
   dispatches `"success"`, so `change.isSuccess` never became true and the success banner silently
   stopped rendering. The retention fix broke a user-visible confirmation with no error and no
   warning. Fixed by sourcing the banner from a plain local flag (`succeeded`) rather than the
   mutation's state machine, with the ordering written out at `PasswordTab.tsx:54-63`.

   Worth its own entry because of what it says about reaching into a library's lifecycle: a fix that
   manipulates a state machine's registration will silently take that state machine's **outputs**
   with it, and the outputs are usually what the user sees. The only thing between this and shipping
   was an assertion nobody wrote for this purpose.

7. **Four smaller confirmed findings.** An unreachable branch; a no-op `.filter(Boolean)` whose
   comment claimed it was load-bearing (now removed, with the real reason written at
   `ProfilePage.tsx:7-12`: `part[0]` on an empty string is `undefined` and `join` stringifies that as
   `''`); an over-claiming test title; and a dead ternary. None changes behaviour, all four were
   prose or reachability claims that were wrong.

8. **The item's Proposal was wrong for the fifth iteration running.** Its Acceptance said "Sessions
   tab lists active sessions ... and supports sign-out-everywhere", and its Notes called the gap "one
   small endpoint". Neither survived contact with the schema. Five for five is no longer an anecdote
   about individual items; it is a property of the Proposal field.

## Findings Triage

- **6 confirmed and fixed, 0 HIGH.** Two behavioural (the clobbered edit, the stranded banner), one
  accessibility (the unannounced guards, which reached a shared primitive), one secret-retention (the
  closure), one prose (the false `clearSession` comment), one bundle of four small items. Plus the
  regression the retention fix itself caused, caught in the same phase.
- **The conductor's `/code-review` raised 2 findings and one was wrong.** It was refuted on the code
  and vindicated on the comment (Problem 4). The calibration carried forward from the last two
  retros - "the broad review's output is an input to the lenses, whatever its count" - held again,
  with one addition: a refuted finding is not a closed question until somebody has measured the thing
  the finding was pointing at.
- **All five of the engineer's mutation proofs were independently re-run and every one reddened with
  the claimed message**, and the secret-retention positive control was verified genuine rather than
  passing against an empty array. Say it plainly: this is the first iteration in the recent arc where
  the implementer's own evidence survived third-party re-execution intact. The three preceding
  retros each recorded an implementer claim that a lens refuted.
- **Phase 4 ran four lanes** - invariants, correctness, security, and a real browser lane taking the
  integration slot on a zero-Go diff. Second time; it should now be treated as the standing shape for
  a frontend-only slice rather than as a substitution decided per iteration.
- **The browser lane found no defects attributable to this PR.** All three tabs render against a mock
  backend on the real Vite dev server; `/profile/nonsense` redirects to identity; zero polling over
  30s; a 24-emoji passphrase (48 characters, 96 bytes) trips the 72-byte guard, which is the
  discriminating input a 73-ASCII-character test would not have been; and a 7-point hit test proved
  the ConfirmDialog stacks above the page, with no repeat of the profile-dropdown stacking bug.
- **The browser lane measured 135px of horizontal overflow at 375px and correctly refused to claim
  it.** It isolated the cause to the shared header shell and attributed it to the already-filed
  app-wide item rather than filing a duplicate. That is the "a finding's stated scope is a starting
  point" rule running in the honest direction.
- **`Field` is a shared primitive and its change moved none of its consumers' tests.** The brief said
  ~12 consumers and 95 tests; reading the tree I count **10 source consumers**, 8 of them
  pre-existing (`ScheduleTriggerForm`, `WorkerEditForm`, `RegisterScreen`, `LoginScreen`,
  `ResetPasswordDialog`, `CreateUserForm`, `CreateReservationForm`, `CreateEnrollmentForm`) plus the
  two new profile tabs. The discrepancy does not change the point: **not one of those tests moved a
  line**, because not one of them had ever asserted that an error was announced. A primitive can ship
  without an accessibility behaviour and stay green across eight consumers indefinitely.

## Deferred Findings

Filed this pass:

1. `bug-2026-08-13-cross-generation-401-clears-a-new-session` (**bug/medium**) -
   `web/src/lib/api.ts:44-46` fires `onUnauthorized` on **any** 401, and the listener
   (`AuthProvider.tsx:77-83`) checks only `status`, never which session the 401 belongs to.
   `apiFetch` passes no `AbortSignal`, so an in-flight request completes and fires it regardless of
   what happened in between. Newly reachable through sign-out-everywhere, which guarantees a burst of
   401s and then drops the user on the sign-in form: any one of them landing after a successful
   re-login deletes the brand-new token and bounces the user out again. **Pre-existing gap, not
   introduced here** - the same listener has always been session-blind, and `apiStream` at
   `lib/api.ts:127-129` fires it too. Proposal: stamp the token in use on the request and clear only
   if `getToken()` still matches, or carry a generation counter. Same end-the-generation-first shape,
   applied where it actually bites.
2. `idea-2026-08-13-field-error-wiring-audit` (**idea/low**) - `Field` now announces, but **eleven**
   error surfaces across ten files that do **not** go through `Field` still do not, enumerated in the
   item by `file:line` rather than estimated, against six comparable surfaces that do. Includes a
   proposal for a sweep test in the shape of the already-filed
   `idea-2026-08-09-dialog-shell-sweep-test`, and a separate open question about the page-level
   fetch-error cards, which may want a focus move rather than a live region.

Proposed by the spec's Scoped-out table, **not filed this pass** (the conductor's Phase 6 brief
scoped filing to the two above; these are recorded here so they are not lost):

3. **Amend `feature-2026-06-26-web-enabler-backend-endpoints`.** Its Proposal at `:25-27` asks for
   `GET /v1/auth/tokens` returning "created_at, last_used_at, current-session flag" as if all three
   were queryable. `api_tokens` has no `last_used_at` column, so that field is a migration. The
   minimum honest list is `id`, `created_at`, `expires_at` and a current-session flag. This is a
   wrong contract in a docs artifact that a future implementer will build against, which this project
   already treats as a defect. **Recommend a human accept this amendment**; it is an edit to an
   existing open item rather than a new file, so it was not made unilaterally.
4. **`idea-2026-08-13-sign-out-others-endpoint`** (low) - a `DELETE /v1/auth/tokens?keep_current=true`
   arm, or a distinct route, reusing the `DeleteOtherTokensForUser` query the password path already
   calls (`tokens.sql:28-29`). The query exists; only a route does not. This is what would make the
   hi-fi's "everywhere else" label true instead of wrong.
5. **`idea-2026-08-13-user-last-login-tracking`** (low) - a `users.last_login_at` touched by
   `handleLogin`, which is what the omitted `LAST LOGIN` meta entry and the Activity card need. Note
   it overlaps `feature-2026-06-26-audit-log-admin-console-actions`; whoever picks it up should check
   for duplication first.

Considered and **not** filed, with reasons:

- **`applyUser` accepting a whole `User` by reference, including `is_admin`.** Rejected. `AdminRoute`
  is documented in its own source as a UX-only guard (`web/src/app/AdminRoute.tsx:4-7`) and the
  security boundary is server-side, so a wrong `is_admin` in client state buys a non-admin a set of
  403s and nothing else. The only caller passes the parsed 200 body of `PATCH /v1/users/me`, which is
  the same `toUserResponse` struct the session was seeded from - the server is the source of both.
  Narrowing the parameter to `Pick<User, 'name'>` would also falsify the design: the whole point of
  decision 3 is that the response **is** the authoritative row. This is a type-narrowing nit with no
  reachable consequence, and filing it would spend a reviewer's attention on the one item in the pile
  that cannot bite.
- **A password strength meter.** The server has exactly one rule, `len(new) >= 8`
  (`auth.go:284-287`). A complexity policy is a backend product decision, not a UI gap - the same
  treatment `queue` overlap got in the schedule spec.
- **The "Forgot your password?" card.** Accurate but aimed at a locked-out user who by definition
  cannot reach a page behind the login wall. Belongs in the README.
- **Extracting the tab shell** (second consumer) or **the min-8 guard** (two lines, no decision
  inside). Both recorded in the spec and in `tabs.ts` rather than filed.

## Known Limitations

- **There is no session list, and there cannot be one without a migration.** The tab says so on the
  page and names the enabler. A user asking "what is logged in as me?" gets an honest "the server
  does not track that" plus an all-or-nothing control.
- **Sign-out-everywhere is genuinely all-or-nothing.** No per-session revoke, and no
  all-but-current variant, because neither route exists.
- **The cross-generation 401 window is open**, filed and unfixed. A 401 that lands after a re-login
  will sign the user out again. Sign-out-everywhere is the surface that makes it easy to hit.
- **Concurrent renames are last-writer-wins.** `UpdateUserName` is a bare `WHERE id = $1` with no
  version column and no 409. In practice the only writer is the user's own form, so the blast radius
  is one user with two tabs open.
- **Email is not editable anywhere, by construction**, and the input says so rather than hiding.
- **`MEMBER SINCE` is the only account fact on the page.** No last login, no login count, no session
  count - no columns for any of them.
- **The narrow-viewport overflow reproduces here** (135px at 375px, measured), attributed to the
  shared header shell and tracked in the app-wide item. Unfixed.
- **Enter-to-submit was not confirmed in a real browser.** The browser lane could not deliver
  synthetic key events in a pane-not-displayed environment and **said so rather than asserting it**.
  Both forms use `<form onSubmit>` with a `type="submit"` primary, and the jsdom tests click the
  button rather than pressing Enter, so implicit submission is untested end to end in either
  environment. Recorded as a real gap, not as a formality.
- **No end-to-end coverage in CI.** The browser lane ran once, by hand, against a mock backend.
  `idea-2026-06-03-web-e2e-harness` remains open and this iteration adds a second slice's worth of
  point-in-time-only measurements to the argument for it.
- **The page was never exercised against a real `relay-server`.** Every fixture is hand-written, so
  contract drift between the fixtures and the Go handlers would not be caught by anything here.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code during spec** - honored, fifth
  iteration running, and it produced the two most consequential corrections in the slice (Key
  Decisions 2 and 3). Both were claims the *brief* asserted, not just the item, which is the new
  part: the conductor's own hypothesis was wrong and the spec said so with SQL.
- **A backlog proposal is not a contract** - honored, and now with a track record: five for five.
  Treat the Proposal field as a hypothesis with a poor prior rather than as a specification.
- **Stage the work so RED is behavioral** - honored; every task stated its expected failure text, and
  Task 1's plan explicitly predicted that its fourth test would pass immediately as an
  untouched-behaviour control rather than pretending it was RED.
- **An invalidation is a continuation** - **honored at authorship**, which is the first time a lesson
  from the previous retro reached the plan's scope guard rather than being rediscovered in review.
  `invalidateQueries` was banned from `web/src/profile/` in the plan and the omission is argued at
  `SessionsTab.tsx:42-53`.
- **When the Go diff is empty, spend the integration lane on a real browser** - honored, second time,
  and it again produced findings no jsdom test could produce (the emoji byte guard, the dialog hit
  test, the overflow measurement). Promote from "the right trade" to "the default".
- **A zero-finding broad review does not close the question** - amended again, see Findings Triage.
- **A finding's stated scope is a starting point, not a census** - applied while filing, twice: the
  `Field` consumer count was re-derived from the tree rather than taken from the brief, and the
  unannounced-error-surface count in the new audit item was enumerated (eleven sites across ten
  files, each named with a line number) rather than estimated.
- **A cadence test must assert the wiring** - not applicable this slice, deliberately: there is no
  cadence anywhere on this surface. Recorded so its absence is not mistaken for a lapse.
- **A new test file is a load change** - **not measured this pass.** Eleven new test files landed and
  nothing was reported destabilized, but with no shell here that is a report rather than a
  measurement. If an unrelated real-timer test starts flaking on main, this branch is the first
  suspect.
- **Backlog housekeeping is required scope** - the close ran during implementation via
  `/backlog close`, and this pass improved the item's `## Resolution` note rather than assuming it
  was accurate.

New from this iteration:

- **A fix that reaches into a library's state machine takes that machine's outputs with it.**
  Detaching an observer to evict a secret also silences everything keyed on that observer's state,
  including the success banner the user reads. When a fix manipulates registration, enumerate what
  reads the registered thing before shipping - and note that here the only thing that caught it was
  an assertion written for another purpose entirely.
- **A false comment can hide behind correct code, and three refutations do not find it.** The lenses
  refuted the code claim three different ways and none of them was a measurement of what
  `queryClient.clear()` actually does. Measure the mechanism a comment names, not the conclusion the
  comment reaches. **Candidate for durable memory**, and a direct extension of the existing "a wrong
  contract in docs is a defect" note.
- **A shared primitive can be missing a behaviour that none of its consumers' tests can see.**
  `Field` shipped without `role="alert"` and eight consumers passed `error` for months, green,
  because absence was never asserted anywhere. When adding a behaviour to a primitive, check whether
  any consumer test would have failed without it - if none would, the behaviour needs its own test at
  the primitive, and probably a sweep for the surfaces that bypass the primitive entirely.
- **"Omit what the backend cannot supply" applies at the granularity of the control, not the
  surface.** Two precedents both had a broken control and a missing read; this one had a working
  control and a missing read, and applying the rule by analogy would have deleted a working security
  action. When a precedent is invoked, state which of its facts is doing the work.
- **Re-running the implementer's own proofs is cheap and should stay standard.** It cost one lane a
  few minutes and, for the first time in this arc, returned "all five reproduce with the claimed
  message". A verification step that only ever fires when something is wrong still needs to be run
  when nothing is, or its silence means nothing.
- **The same ordering defect shipped twice in two consecutive slices** (reset-after-early-return,
  Problem 2). A rule recorded only in a retro does not reach the next author. Either it goes in
  CLAUDE.md, in a lint, or in the plan's per-task text - or it will arrive a third time.

## Files Most Touched

- `web/src/profile/PasswordTab.tsx` - the three guards with two error slots (Problem 3), the
  variable-free mutation with `gcTime: 0` and the closure-retention argument (Problem 5), and the
  `succeeded` local flag with the query-core ordering explained at the site (Problem 6). The one file
  in the slice where three separate findings landed.
- `web/src/profile/IdentityTab.tsx` - the conditional draft release (Problem 1) and the hoisted
  `reset()` (Problem 2), both with the reasoning written at the call site.
- `web/src/profile/SessionsTab.tsx` - the action-without-a-list argument, the verified blast-radius
  copy, the deliberate absence of `invalidateQueries`, and the close-the-dialog-before-firing
  ordering so a mutation error does not render behind its own scrim.
- `web/src/auth/AuthProvider.tsx` - `applyUser`, `clearSession`, and the corrected teardown mechanism
  at `:40-56` (Problem 4). The 401 listener at `:75-85` is deliberately untouched.
- `web/src/components/Field.tsx` - the `role="alert"` plus `aria-describedby` wiring, added in
  Phase 4, affecting ten consumers and moving none of their tests.
- `web/src/profile/api.ts` - three literal paths and every backend hazard in the slice written down
  once, including the singular-versus-plural DELETE distinction.
- `web/src/profile/PasswordTab.auth.test.tsx` - the 403-versus-401 pair with a live-instrument
  control, and the secret-retention test with its positive control moved ahead of the eviction.
- `web/src/app/router.tsx` - the two new routes and the comment explaining why neither is behind
  `AdminRoute`.
- `web/src/profile/ProfilePage.tsx` - the meta strip, the omission rationale, and the corrected
  `initialsOf` comment.

## Verification

- **Web suite reported green at 959 tests** (890 before), by the implementing engineer and the four
  lanes. **This pass had no shell**, so neither the count nor the green state is independently re-run
  in this document - it is reported, not verified here. The exact-file-set check and the final gate
  run are the conductor's, per the standing rule that subagent claims are verified against the tree
  rather than trusted.
- Every factual claim in this retro that could be checked by reading was checked: the two new routes
  and their absence from `AdminRoute`; the conditional draft release at `IdentityTab.tsx:44` and the
  hoisted `reset()` at `:60`; the two error slots and the `succeeded` flag in `PasswordTab.tsx`;
  `gcTime: 0` plus `change.reset()` and the cache-goes-empty assertion at
  `PasswordTab.auth.test.tsx:160-166`; `applyUser`/`clearSession` and the corrected comment at
  `AuthProvider.tsx:40-56,127-141`; the untouched 401 listener at `:75-85`; `Field`'s `role="alert"`
  and `aria-describedby`; the ten `Field` consumers and the eleven error surfaces that bypass it;
  `lib/api.ts:44-46` and `:127-129` firing `onUnauthorized` with no session identity; the
  `SessionsTab` copy against `tokens.sql:25-26` versus `:28-29`; the deleted `JobsPlaceholder` (one
  surviving mention, an assertion that it is unreachable); and `AdminRoute.tsx:4-7` documenting
  itself as UX-only.
- **Not verified:** the production build, the browser measurements (single manual run, no artifact in
  the repo), the test count, whether the new test files changed suite load, and anything requiring
  execution.
- Phase 4 was four lanes dispatched in one message - invariants, correctness, security, and a
  real-browser lane in the integration slot - over a conductor-run `/code-review` that supplied 2
  findings as prior input. Each lane confirmed or refuted those independently before adding its own.
- `web/dist` is tracked but stale from the scaffold; a frontend build dirties it, and it must be
  reverted before the change set is assembled.
