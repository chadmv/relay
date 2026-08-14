---
date: 2026-08-13
topic: cross-generation-401
branch: claude/pr-merging-session-6aede7
range: origin/main..HEAD (green, not yet merged)
---

# Session Retro: 2026-08-13 - The 401 gets a session identity, and the prose around it got everything else

**TL;DR:** Closed `bug-2026-08-13-cross-generation-401-clears-a-new-session` with option A, exactly
as the item recommended: `apiFetch` and `apiStream` stamp each `onUnauthorized` notification with the
token the request actually attached, and `AuthProvider`'s listener returns early unless that token
still equals `getToken()` when the 401 lands. A 401 belonging to a session that died two logins ago
can no longer tear down the session that replaced it. Two production files, one new test file, two
appended tests elsewhere, one new secrecy test. Suite 1049 -> 1059.

**The shipped behaviour change is one `if`.** Everything else in this document is about the sentences
around it. The item's recommendation was right and its stated *reason* for the alternative was false.
The fix is the frontend instance of the project's own epoch-fence rule, which nobody had noticed until
the item's own closing note said so. A comment this diff added was invalidated by the same diff before
it was committed. A comment in a file this diff never opened became false. A comment in the diff's own
**new test file** was made false by a **later commit on the same branch**. And the round that fixed the
stale citations missed one, which was found - for the third time on this project - by grepping the
claim's literal wording rather than by inspecting where it ought to live.

Ten findings landed. Eight of them were prose. **Tenth consecutive iteration in which wrong prose about
correct code was the dominant defect class**, and the first in which the wrong prose was *produced by
the change being reviewed* rather than inherited from the tree.

## The pipeline was compressed, and that was a judgment call worth recording

Phase 1 was folded into Phase 2. The conductor wrote no spec; the planner absorbed the spec role under
a hard verification mandate. That is a deviation from the standing lifecycle and it should be defended
or corrected in the open rather than left as a silent shortcut.

**Why it was defensible here.** The backlog item already contained everything a spec produces:

- the mechanism, cited to the line at both fire sites and at the listener;
- **two candidate designs with a recommendation** (stamp the token; a session generation counter);
- an **explicitly rejected trap option** (abort in-flight requests at teardown) with the reason - it is
  CLAUDE.md Invariant 1 in its frontend form - and a pointer to `useTaskLogStream.ts`, the instance the
  invariant was written from;
- acceptance criteria naming the discriminating test, the positive control that must survive, the
  `apiStream` half, and the documentation requirement;
- and its own generalization, "a status check establishes currency, never identity", which is the
  design.

A spec against that item would have restated it. The item was the spec, written by a Phase 6 pass that
had just spent a slice measuring this exact surface.

**What the compression cost, and what covered it.** A spec's real product is not the design; it is the
adversarial read of the item. Folding it away removes the party whose job is to disagree with the item.
The mitigation was to give the planner that mandate explicitly, and it worked: the plan opens with a
thirteen-row table of the item's claims against the tree, confirms ten of them exact, and refutes two
(one citation, one design argument). See below. **The compression is defensible when the item carries a
design; it is not defensible without the verification mandate moved somewhere explicit**, because
otherwise the only artifact that ever checks the item disappears and nothing announces its absence.

Recorded as a decision rather than as a lapse, and the condition is worth reusing: **fold Phase 1 into
Phase 2 only when the item already contains alternatives and a recommendation, and only by naming the
planner as the party that must refute it.**

## The item recommended the right option, and its argument for the other one was false

The item offered A (compare token values) and B (a session generation counter), recommended A, and gave
B exactly one advantage: B "also covers a 401 arriving for a token that has since been **replaced**
rather than removed". That sentence is the item's whole case for B, and it is wrong.

**A compares values, not presence.** `getToken() === 'tok_B'` is not equal to `'tok_A'`, so `A -> B` is
rejected by precisely the same comparison that rejects `A -> null -> B`. Replacement and removal are the
same rejection. The only input A cannot distinguish is a *second session issued the identical token
string*, which the project's own token format rules out: 32 random bytes, hex-encoded, never reused
(CLAUDE.md, "Token format"). **A has no gap relative to B.**

Two further points, both of which make A better rather than merely equal:

1. **A adds no state, so it has no ordering hazard.** B is a mutable generation that four call sites must
   remember to bump, in the correct order relative to the resource it guards. The item itself warns, two
   paragraphs earlier, that the tidier-looking abort fix is Invariant 1's frontend trap - and a
   generation counter is the *other* half of that same invariant, the half that has to be bumped first.
   **The item recommended, as its fallback, a mechanism whose failure mode it had already described as a
   trap in the same document.** Nobody caught that until the plan reasoned about A's statelessness.
2. **The fence must read `getToken()` fresh, not a mirror**, and this is where B would actually have been
   *worse*. `applyAuth` calls `setToken(res.token)` and only then awaits `/users/me`; the status flip
   happens after that round trip. Any React-committed mirror lags through that whole window, so a 401
   landing inside it would be compared against a stale value and would tear down the brand-new session -
   **reintroducing this exact bug through its own fix**. `localStorage` is the credential's single source
   of truth and `setToken`/`clearToken` write it synchronously, so reading it fresh is the only
   comparison that is always against the credential the next request would send.

Both corrections are written into the closed item's Resolution, because a closed item titled around a
session-identity fix is otherwise circumstantial evidence that a generation counter exists.

**Ninth consecutive iteration in which verifying the item's technical claims changed something.** This
one is a new variety: the item's conclusion was right, its factual claims about the SPA were *all*
correct and every SPA line citation still resolved, and the defect was in its comparative argument. An
item can be accurate about the world and wrong about its own options.

## The fix is the frontend instance of the project's own backend rule

The item's closing note names it, and the shipped comment now carries it:

> a status check establishes **currency**, never **identity**.

The backend learned this on `tasks.status` writes. A matching `assignment_epoch` proves the caller's
generation is current and proves nothing about *who* the caller is, so every such write also fences on
`worker_id`. `AuthProvider`'s listener had the currency half (`statusRef.current === 'anonymous'`) and
not the identity half, and had since it was written.

**Both fences are load-bearing, and the reason the currency guard survives is the interesting half.**
The obvious reading after adding an identity fence is that the `anonymous` guard is now redundant - a
more specific check has arrived, delete the vaguer one. It is not redundant, and the counter-example is
the most ordinary flow in the app: **a failed login on the sign-in screen sends a request with
`token === null` while `getToken()` is also `null`, so it passes the identity fence BY EQUALITY.** The
`anonymous` guard is the only thing that stops a mistyped password churning auth state and clearing an
empty cache on every attempt. Deleting it regresses `LoginScreen.test.tsx`.

That is worth generalizing beyond this file. On the backend the two predicates guard different columns
and nobody would confuse them. Here they are two `if`s three characters apart, and the identity fence
**passes** in exactly the case the currency guard exists for. The pairing survives because the plan
wrote out the null case as a three-row table before anyone implemented it, not because it is obvious in
the code. The shipped comment (`web/src/auth/AuthProvider.tsx:72-110`) states both questions, names
which one the null case passes, and says why. That comment is longer than the function; it should be.

## Citations, three sweeps deep

Four separate citation defects, each a different mechanism, in a slice whose behavioural change is one
line. Read them as a set, because individually each looks like tidying and together they are a pattern
about what a `file:line` citation actually costs.

### 1. The diff's own new comment invalidated itself on arrival

A comment this change added to `AuthProvider.tsx` cited `clearSession` at `:127-132`. That was correct
on `origin/main`. It was false in the commit that wrote it, because **the 37 comment lines the same diff
inserted above it pushed `clearSession` down to `:164-169`**.

The citation was never true in any tree that ever existed. It described the file the author was reading
in their head - the pre-edit one - and no gate on this project can catch that, because a line citation
is a comment and comments do not compile.

**The rule that falls out is mechanical and cheap: a `file:line` citation into a file you are actively
editing is a citation that will rot before you commit it.** Cite the symbol. The corrected passage now
reads "clearSession() already did all four of its own statements, synchronously, with clearToken()
first" and names no line at all. `web/src/lib/api.ts:32` and `api.stream.test.ts:46` got the same
treatment for the same reason ("AuthProvider's onUnauthorized subscription" rather than a range that had
already drifted twice and was about to change size again).

### 2. The diff stranded five citations to its own fire site

Moving the `apiFetch` 401 handler stranded five comments citing `lib/api.ts:44-46` as the fire site.
Those five were found and repaired - a tree-wide grep of `web/src` now returns zero `api.ts:44-46`
citations - and they were repaired **in the same commit series that deliberately de-line-numbered two
other citations** because they had drifted twice. The slice was simultaneously learning the lesson and
paying for not having learned it earlier.

### 3. The stale citation that had become security prose, and the fourth site found by grep

The item's own citation of `DeleteTokensForUser` at `internal/store/query/tokens.sql:25-26` had drifted:
the doc-comment block added to `DeleteToken` in the previous slice moved the statement to `:40-41`. The
behaviour claim ("`DELETE FROM api_tokens WHERE user_id = $1`, no `id <> $2`, every token for the user")
was correct throughout; only the coordinates were stale.

**Why this one is not cosmetic.** `tokens.sql:25-26` today lands *inside `DeleteToken`'s comment* - a
per-session, **owner-scoped** delete - while the prose at each citing site is asserting the unscoped
blast radius that makes sign-out-everywhere a session-destroying operation. A reader following the
citation to check the claim finds a statement that contradicts it, and the claim they were checking is
the one the entire teardown design rests on. That is the wrong-prose class at its most expensive: not a
false sentence, but a true sentence pointed at its own counter-example.

The fix round corrected it at `AuthProvider.tsx:35`, `SessionsTab.tsx:39`, `:84` and
`SessionsTab.test.tsx:72` - and **missed a fourth site**, `web/src/profile/api.ts:68`. The conductor
found it by **grepping the claim's literal wording**, not by reasoning about where such a citation would
live. That is the **third time on this project that method has beaten inspection**, after the
`DialogShell` overlap claim and the `timed_out` writer. The rule has now earned promotion from a habit
to a step: when a citation or a claim is corrected anywhere, grep its literal text before closing the
finding, every time, because the person chasing it has been wrong about the site count three times
running.

### 4. A later commit on the same branch falsified the diff's own new test comment

This is the sharpest instance and it survives in the shipped tree. `AuthProvider.crossgen.test.tsx:264`
says "reverting `api.ts:128` to a bare `fn()`". That was correct when the test was written: the
`apiStream` fire site was at `api.ts:128`. The **later** robustness commit on this same branch extracted
both fire sites into `notifyUnauthorized`, moving the stream fire site to `:163` - and `api.ts:128` now
points at a line in the `apiStream` doc comment.

Finding 1 is a citation invalidated by its own diff. **This one is a citation invalidated by a diff two
commits later on the same branch**, which is strictly harder to catch: at the moment it was written it
was true, it was reviewed as true, and nothing in the later commit's own review had reason to look at a
test comment in another file. It is left as is and recorded here rather than filed, because it is a
one-line repair for whoever next opens the file and an item would cost more to triage than to fix. But
the generalization is real and it is the reason the four findings belong in one section: **the number of
citations a branch invalidates is a function of how much the branch moves, not of how much it changes.**
This branch's behavioural delta is one `if` and it invalidated at least seven citations.

### 5. The change made a comment in an untouched file false

`web/src/jobs/useTaskLogStream.ts` stated that a stream 401 fires the notifier and "AuthProvider
redirects". After the identity fence, it deliberately may not: a stream that outlives a
sign-out-everywhere fires the notifier and `AuthProvider` correctly does nothing.

**The hook's behaviour was fine; only its stated mechanism was wrong.** 401 stays terminal there, no
retry, and the hook never depended on the redirect actually happening. Corrected in branch
(`useTaskLogStream.ts:336-341`) to say that `AuthProvider` redirects only when the 401's token matches
the current session, and that a cross-generation 401 fires the notifier with nothing downstream.

The class is worth naming because it inverts the usual scope question. Every other finding in this
section is about a citation the diff moved. This one is about a **claim the diff falsified in a file it
never opened**: the hook asserted a downstream consequence of a component this change altered. The
countermeasure is not "grep for line numbers", it is **grep for assertions about the behaviour you are
changing, tree-wide, including files with zero diff**.

## What Was Built

- **No spec.** Phase 1 folded into Phase 2; see the compression section. **Plan**
  `docs/superpowers/plans/2026-08-13-cross-generation-401.md`, five tasks plus a baseline task, one
  `relay-frontend-engineer`, strictly sequential (Tasks 2 and 3 both depend on the stamping from Task 1,
  and Task 3's tests exercise the guard from Task 2). Notable for its thirteen-row "discrepancies between
  the backlog item and the tree" table, which is the format that produced the option-B refutation, and
  for enumerating **every** `onUnauthorized` registration site before widening the callback signature.
- **`web/src/lib/api.ts`.** `type Listener = (token: string | null) => void`; the `onUnauthorized` doc
  comment now says the token argument is what makes the notification actionable, not optional context;
  both fire sites (`:79-81` in `apiFetch`, `:162-164` in `apiStream`) pass the **local** `token` already
  read before the request went out, never a re-read of `getToken()` - re-reading at the fire site would
  reproduce the bug inside the fix. Plus `notifyUnauthorized` (`:39-57`), the try/catch fan-out; see Key
  Decisions.
- **`web/src/auth/AuthProvider.tsx`.** One line of behaviour - `if (requestToken !== getToken()) return`
  ahead of the existing `anonymous` guard (`:113-115`) - under a 39-line comment block (`:72-110`) that
  states both fences, the epoch-fence analogy, why the comparison reads `getToken()` fresh rather than a
  ref, why no generation counter exists, and what a 401 arriving mid-teardown now does.
- **`web/src/auth/AuthProvider.crossgen.test.tsx`** (new, 5 tests): the discriminating `apiFetch` case,
  its positive control, the teardown-convergence guard, the discriminating `apiStream` case, its positive
  control. Both discriminating tests hold a real request open behind an explicit gate promise and release
  it after the new session exists - **no timer, no sleep**, and each carries a positive control on its own
  setup (`tokensSeen` must equal `['Bearer tok_A']`) so the test cannot pass because no request was ever
  made.
- **`web/src/lib/api.test.ts`** +3: the token is stamped, `null` is stamped for an anonymous request (not
  `undefined`, which would compare unequal to `getToken()`'s `null` and break the sign-in-screen case),
  and the throwing-listener test.
- **`web/src/lib/api.stream.test.ts`** +1: the stream half of the stamping contract.
- **`web/src/auth/authTokenSecrecy.test.tsx`** (new, 1 test): the session token never reaches console
  through a 401, asserted through the existing `web/src/test/secretLeaks.ts` harness with a positive
  control on the setup.
- **Citation corrections** at `AuthProvider.tsx:35`, `api.ts:32`, `api.stream.test.ts:46`,
  `SessionsTab.tsx:39`, `:84`, `SessionsTab.test.tsx:72`, `profile/api.ts:68`, and
  `useTaskLogStream.ts:336-341`.
- **One out-of-plan copy fix.** `SessionsTab.tsx` told users, in a visible paragraph, that
  `GET /v1/auth/tokens` does not exist. It shipped in the previous slice. The copy and its doc comment
  now say the real gap - this tab does not yet render a list from it. **No list UI was built.**
- **Suite 1049 -> 1059** (3 + 1 + 5 + 1 = 10). **Zero Go, zero SQL, zero proto, zero migration.**

## Key Decisions

- **Option A, and no session generation counter.** Refuted above. The stronger form of the decision is
  what it declines: A introduces no new state, so there is no bump-before-release ordering to get wrong,
  which is the precise hazard CLAUDE.md Invariant 1 exists for and the precise hazard the item warned
  about two paragraphs before recommending the mechanism that has it.
- **The comparison reads `getToken()` fresh at listener-run time.** Not a ref, not React state. A mirror
  lags through `applyAuth`'s `/users/me` round trip and would reject a 401 belonging to the session it is
  supposed to protect. **This is argued and not tested** - see Known Limitations, where it is the honest
  hole in the file.
- **The `anonymous` guard stays, and the order is identity-then-currency.** Either order yields the same
  answer; identity runs first because it is the cheaper and more specific question. The guard is not
  redundant: the failed-login case passes the identity fence by equality.
- **The fire sites pass the local `token`, never a re-read.** By the time a 401 lands, the stored token
  may already be a different one. That is the bug, stated as an implementation constraint.
- **The listener fan-out is try/catch-wrapped.** `unauthorizedListeners` is a `Set` iterated with
  `forEach`, which does not catch a callback's throw. An uncaught throw would (a) stop iteration, so
  every subscriber registered after the throwing one is silently skipped, and (b) propagate out of
  `apiFetch`/`apiStream`, **replacing the caller's real result** - the `ApiError` about to be thrown for
  this 401 - with an unrelated error the calling code was never written to expect. Proven by mutation:
  without the wrapper the second listener is called **0** times and the awaited error is
  `Error('boom')` rather than `ApiError`. The catch logs the subscriber's error only, never the token.
- **The token gets a secrecy test on the new channel.** Stamping the raw bearer credential onto a
  notification is a **new place the token travels** that did not exist before this slice. Nothing forces
  a subscriber to log it and nothing prevents it. The test extends `web/src/test/secretLeaks.ts` - the
  same instrument already pinning the enrollment and invite token flows - rather than inventing a
  pattern, and it pins the real flow (a live `AuthProvider`, a real 401) with a positive control proving
  the teardown actually ran. Proven non-vacuous by mutation.
- **`web/src/profile/PasswordTab.auth.test.tsx` stays byte-identical.** The item names `:83-99` as the
  existing positive control; it must stay green *and* untouched so its green run is clean evidence rather
  than something this slice adjusted. It cost one thing - see Findings Triage.
- **No `AbortSignal`, no abort-at-teardown, in any form.** The item rejected it explicitly and the plan
  carried the rejection into a scope guard. It remains the tidier-looking fix and remains the trap.

## Problems Encountered

1. **The reported flake was measured rather than waved away, and the honest answer is partial.** Record
   this one precisely, because it has two halves that point in opposite directions and summarizing it as
   either "flaky, fixed" or "flaky, unexplained" would be false.

   **What was observed.** Two lanes independently saw failures: a positive control failing **3 times**
   early in the slice, and a cold run reddening **4 tests across 3 files, including a file this slice does
   not modify.**

   **What the investigation found.** The engineer could not reproduce the specific reported failure in
   roughly **90 runs at 4x to 20x contention**. What it found instead was **suite-wide, pre-existing
   `waitFor` flakiness under high vitest concurrency, hitting unrelated unmodified files** - which
   explains the unmodified file in the cold run and explains nothing about the specific failure.

   **What it did fix, and it is a real test defect.** Both login-based tests in the new file polled
   `getToken()` with `waitFor` and stopped there. `login()` calls `setToken()` synchronously but only sets
   status to `authenticated` after an internal, unawaited `GET /users/me` settles. **Polling `getToken()`
   proves the synchronous half happened and nothing about the second half**, so the subsequent
   `authenticated` assertion - a bare `expect` with no wait behind it in the stream test - was racing the
   login flow it was supposed to be waiting for. Fixed **additively**, by capturing `login()`'s own promise
   from the Probe button and awaiting it (`AuthProvider.crossgen.test.tsx:30-39`, `:128`, `:251`). **No
   assertion was loosened, no timeout was raised, and no `waitFor` was added to paper over it** - a real
   `await` replaced a poll. The mechanism is documented at the binding, alongside the existing
   `inflight` comment it deliberately mirrors.

   **The honest statement is both of these at once: a real test defect was found and fixed, AND the
   originally reported failure remains unreproduced.** The fixed defect is a plausible cause of the
   positive control's 3 early failures and is not proven to be the cause. The standing rule from the
   2026-08-12 arc - measure a suspected pre-existing failure both ways and get a number for each - was
   satisfied in spirit (90 runs at varying contention) and not in letter (no branch-versus-`origin/main`
   pair). This is the second consecutive iteration in which a flake was adjudicated without that pair.

2. **The real-browser lane replaced the integration lane on a zero-Go diff, and earned the slot.** The
   standing rule promoted this to the default two iterations ago; the previous retro could not confirm it
   ran. This time it ran and it is the strongest instance yet, because the repro in the backlog item is a
   browser flow and nothing else in the pipeline can execute it.

   It stood up Postgres, `relay-server` and Vite; signed in as token A; **held a request open**; ran
   sign-out-everywhere; signed back in as token B; then released the held request. Result: `401` for
   token A, the session stayed on token B, and no bounce to `/auth`. That is the item's repro, executed,
   against a real server, in a real browser - **the first end-to-end evidence for any fix in this arc**,
   in a project whose entire frontend gate is jsdom.

   **It also reported a methodology bug of its own**: a fetch-patch that lost the native reference across
   a reload, which contaminated a run. It re-ran clean and reported the clean run **as a re-run**, rather
   than reporting the contaminated one or quietly discarding it. That is the integration lane catching
   its own fixture error, which the 2026-08-13 list-endpoints retro named as the best form of a finding,
   now seen in the browser lane.

3. **The slice acquired two robustness additions that were not in the item, the plan, or the acceptance
   criteria.** Both came out of review asking what the change makes newly reachable, and both are
   defensible on the merits; recorded here because "the plan said five tasks and six things shipped" is
   the shape that usually indicates scope creep and here does not.
   - **The try/catch fan-out** is not speculative hardening. Before this slice, a subscriber received no
     arguments and had nothing to do; after it, subscribers are *expected* to run a comparison and act,
     which is the first time a subscriber has had a reason to contain logic that can throw. The change
     that makes the throw plausible is this one.
   - **The secrecy test** is the same question in the other direction: this slice created a new channel
     that carries the raw bearer credential, and the project already has an instrument for exactly that
     question with two existing users. Extending it was cheaper than arguing that nobody would log it.

   Both are the "ask what the diff makes it newly reasonable to build next" rule from the previous
   iteration, applied to the diff's own surface rather than to a future slice.

4. **`web/dist` did not appear, and the rule was declared inapplicable up front.** The plan stated that no
   task runs `npm run build`, so nothing should dirty it, and named the remedy if anything did. Third
   consecutive slice in which the plan declared the applicable and inapplicable rule sets before the first
   task. This has now stopped producing incidents, which is the outcome the practice was written for.

## Findings Triage

- **0 high, 0 medium against the shipped behaviour. 10 findings, 8 of them prose.** The two that were not:
  the test-timing defect (Problem 1) and the unguarded listener fan-out (Key Decisions), both introduced
  or made reachable by this slice and both fixed before merge.
- **The behavioural fix survived review untouched.** Nothing in Phase 4 changed `AuthProvider.tsx:113-115`
  or either fire site's argument. Every subsequent commit on the branch was a comment, a test, or the
  fan-out wrapper. That is unusual enough to say out loud: the previous two iterations both had a review
  finding against the shipped behaviour.
- **The byte-identity gate on `PasswordTab.auth.test.tsx` held, and it froze a stale citation.** The file
  is untouched, so its green run is clean evidence for the positive control the item names. It also still
  carries `tokens.sql:25-26` at `:106`, the exact drift this slice corrected in four other files. **The
  gate did its job and preserved a known-false coordinate as a side effect.** This is a new and real
  tension: a byte-identity gate freezes wrong prose along with right behaviour. Both calls were correct
  here - clean evidence is worth more than one line number - but the tradeoff should be *named* when the
  gate is declared, not discovered afterwards.
- **`DeleteOtherTokensForUser`'s citation was not swept.** The plan's finding named both drifted pairs;
  the slice fixed `:25-26 -> :40-41` (the security-prose one) and left `:28-29 -> :43-44` stale at five
  sites: `web/src/profile/api.ts:53`, `:71`, `PasswordTab.tsx:152`, `PasswordTab.test.tsx:205`,
  `PasswordTab.auth.test.tsx:106`. Verified by reading. **Not filed** - it is a mechanical
  search-and-replace whose claim (`AND id <> $2`) is still correct and whose new location is four lines
  away, and an item would cost more to triage than the edit. Recorded so the next person in `profile/`
  does it in passing.
- **`AuthProvider.crossgen.test.tsx:264` cites `api.ts:128`, which is stale.** Falsified by a later commit
  on the same branch. Recorded above, not filed, same reasoning.
- **Every mutation in the slice was named in advance and both outputs recorded.** Mutation A (delete the
  identity fence) reddens the discriminating test and only it; mutation B (fence everything out) reddens
  the positive control and only it; mutation C (unstamp the stream) reddens the stream control plus the
  spy test in a different file through a different instrument. The fan-out and secrecy tests each got
  their own. **Every absence-or-inaction assertion in this slice has a paired positive control**, which is
  the standing requirement for a fix whose observable is "nothing happened".
- **The conductor's `/code-review` output was supplied to the lanes as prior findings**, per the standing
  shape, and each lane confirmed or refuted before adding its own passes.

## Deferred Findings

**Filed this pass (three items, each proposed for human review rather than treated as accepted):**

1. `bug-2026-08-13-failed-login-leaves-a-live-token-in-localstorage.md` (**bug/low**) - `applyAuth`
   (`AuthProvider.tsx:141-146`) calls `setToken(res.token)` and then awaits `GET /users/me` with no
   `catch`. If that second request fails, `login()` rejects and `LoginScreen` shows an error
   (`LoginScreen.tsx:20-24`, which never clears the token), but the token stays in `localStorage`. The
   401 case is *silent by design* - the listener's `anonymous` guard correctly declines to act on the
   sign-in screen. The sharp observable is the non-401 case: **a login the user was told failed can
   silently succeed on the next page load**, because the bootstrap effect finds the stored token and
   authenticates with it. **Entirely pre-existing** - identical on `origin/main`, unaffected by the
   identity fence in either direction. Cross-linked to `idea-2026-06-03-login-return-user-object`, which
   would remove the window entirely by deleting the second round trip.
2. `idea-2026-08-13-sign-out-in-one-tab-leaves-other-tabs-authenticated.md` (**idea/low**) - deliberately
   titled as the observable rather than the remedy. `localStorage` is shared across tabs, so tab 1's
   `clearToken()` empties it for tab 2 as well; tab 2's in-flight 401 carries token A, the identity fence
   correctly rejects it, and tab 2 keeps rendering an authenticated shell. **The fence is behaving
   correctly** - that 401 genuinely says nothing about tab 2's authority - and the real gap is that
   nothing propagates a cross-tab sign-out at all. It self-heals on tab 2's next request (which goes out
   unauthenticated, 401s, and then passes both fences), so a polled page recovers within one interval.
   The item states honestly that this is a **narrow behaviour change** introduced by this slice: before
   the fence, a tab that happened to have a request in flight would have bounced immediately; a tab that
   did not would have sat stale exactly as it does now. Remedy sketched as a `storage` event listener,
   which is the standard mechanism and which the app has never had.
3. `idea-2026-08-13-web-suite-waitfor-flakiness-under-concurrency.md` (**idea/low**) - Problem 1's
   unexplained half, filed as a **measurement to preserve rather than a fix to apply**. Two lanes saw
   failures; roughly 90 runs at 4x to 20x contention did not reproduce the specific one; what was found
   instead was suite-wide `waitFor` flakiness under high concurrency reaching unmodified files. The item's
   proposed first step is a **diagnosis** (pin `maxThreads` and measure with and against, per the standing
   both-ways rule), not a remedy, and it names its own risk: without a reproducible offender list this is
   the kind of item that can absorb triage without converging. Filed anyway because a suite that flakes
   under load undermines every green gate this project's process depends on, and the next person to see a
   red run deserves the prior.

**Amendment applied to an existing item (measurement added, framing adjusted):**

- `docs/backlog/bug-2026-08-12-web-narrow-viewport-horizontal-overflow.md`, the next roadmap item. The
  browser lane measured `/profile/sessions` at a 375px viewport: `scrollWidth` **523px** against a 375px
  client width, 148px of overflow, and **the widest offending element was the top header nav bar at
  523px while `MAIN` was only 391px**. That is a third independent surface, and it **challenges the
  item's stated remedy**: the item's Cause 2 points at the shared `Table` primitive, and this surface
  renders no table at all (`web/src/profile/` contains no `grid-cols-[...]` template and no `Table`
  consumer). The amendment records the measurement, records that the same 523px total appeared on the
  Invites tab on 2026-08-13, and adds a clearly-labelled hypothesis - that the header sets a ~523px floor
  app-wide and tables add on top of it, consistent with the 653px seen on schedule detail - with an
  instruction to **measure the header first** rather than starting from the `Table` primitive. Stated at
  exactly the strength of the evidence: one surface measured the header as the widest element.

**Considered and NOT filed, with reasons:**

- **The `Listener` type is structurally satisfiable by a callback that ignores the fence.**
  `(token: string | null) => void` still accepts a bare `() => {}`, because TypeScript makes a
  fewer-parameter function assignable - which is not incidental, it is the property the plan relied on to
  prove the widening could not break an unenumerated subscriber. Two remedies were considered and **both
  fail on the merits**:
  - *A required object parameter* (`({ token }) => ...`) forces a future subscriber to *name* the token
    and does nothing to force it to *compare* the token. It converts a silent omission into a slightly
    louder one. In this project's own vocabulary, that is **a speed bump, not a guard**.
  - *A sweep test asserting every subscriber fences* asserts a rule that is **not universally correct**.
    A subscriber that logs, closes a socket, or shows a toast has no business comparing tokens; only a
    subscriber that tears down session state does. A blanket sweep would be wrong for four of the five
    plausible future subscribers, and a sweep that only checks a parameter is declared is the speed bump
    again.

  There is exactly **one** production subscriber today, and the control that actually addresses the risk
  already shipped: the `onUnauthorized` doc comment (`web/src/lib/api.ts:23-33`) states that the token is
  what makes the notification actionable and instructs subscribers that act on a 401 to compare it against
  the credential they hold. **Recorded here so the next reader who spots the structural gap finds the
  answer instead of the idea**, and with the trigger named: if a second *state-tearing* subscriber ever
  registers, the question is worth re-asking, and the answer then is probably a named helper both
  subscribers call, not a type change.
- **Neither residual citation drift** (`tokens.sql:28-29` at five sites; `api.ts:128` in the new test
  file). See Findings Triage. One-line repairs, correct claims, wrong coordinates; triage would cost more
  than the edit.
- **`SessionsTab` still renders no session list**, now that the copy correctly says the endpoint exists.
  **Not filed as a new item** - it belongs to the already-open profile/sessions work and the previous
  retro explicitly ruled that a per-session revoke and list UI is a slice, not a backlog chore. Filing it
  now would create a second file for one question.

## Known Limitations

- **The ref-lag hazard is argued, not tested.** The Design section and the shipped comment both explain
  why the fence must read `getToken()` fresh rather than a React-committed mirror. **No test in this slice
  goes red against a ref-based implementation**, because the window it fails in is inside `applyAuth`'s
  `/users/me` round trip and the harness cannot hold that request open and drive the listener at the same
  time. The plan named this as untestable-here rather than glossing it. It is the single most likely way
  a future refactor reintroduces this bug, and the only thing standing in front of it is a comment.
- **The multi-tab behaviour change was reasoned, never executed.** No test and no browser run covers two
  tabs. See filed item 2.
- **The browser lane proved the fix, not the absence of the bug elsewhere.** One flow, one browser, one
  session pair. It is real end-to-end evidence and it is a single path.
- **The specific reported flake was never reproduced.** Roughly 90 runs at 4x to 20x contention. The test
  defect that was fixed is a *plausible* cause of the 3 early positive-control failures and is not proven
  to be the cause, and no branch-versus-`origin/main` pair was measured. See Problem 1.
- **jsdom does no layout, so nothing here says anything about rendering.** Stated because it is now the
  fourth frontend slice in a row where it is true, and because the overflow item this retro amends exists
  precisely because of it. `idea-2026-06-03-web-e2e-harness` remains open.
- **`AuthProvider` still has no cross-tab session propagation**, no `AbortSignal` at teardown, and no
  generation counter. All three are deliberate; the first is now filed, the second is an explicit scope
  guard and a documented trap, the third is refuted.
- **Two stale citations ship in this branch**, both verified by reading and both recorded above.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code during spec** - honored in a compressed
  pipeline where the *planner* held the mandate, ninth iteration running. New variety: the item's factual
  claims were all correct and its comparative argument was wrong.
- **A backlog proposal is not a contract** - nine for nine, and the mildest instance in the arc. The
  recommendation was adopted unchanged; only its supporting argument for the road not taken was refuted.
- **Plan-supplied tests are untrusted** - honored and it paid. The plan's test bodies polled `getToken()`
  where they needed to await `login()`, and one asserted status with no wait at all. The engineer fixed
  the tests rather than loosening the assertions, which is the behaviour the rule asks for.
- **Re-running the implementer's own proofs is cheap and should stay standard** - honored; every mutation
  was named in advance and both outputs recorded.
- **A mutation proof must leave a test behind** - honored: the fan-out and secrecy mutations each left a
  permanent discriminating test, not a reverted experiment.
- **When a claim is refuted, grep the tree for its wording** - honored and it **paid for the third time**,
  finding the fourth stale `tokens.sql` site the fix round missed. Promote from habit to step.
- **Wrong prose about correct code is the dominant defect class** - **tenth consecutive iteration**, and
  the first in which the wrong prose was produced by the change under review rather than inherited.
- **When the Go diff is empty, spend the integration lane on a real browser** - **honored, executed, and
  confirmed** after one iteration where it could not be verified. It produced the only end-to-end evidence
  in the slice.
- **Ask what the diff makes it newly reasonable to build next** - honored, and turned inward: both
  unplanned additions came from asking what this change makes newly reachable on its own surface.
- **A byte-identity gate proves preservation only in the dimensions the existing tests measure** - held,
  with a new wrinkle; see the new goal below.
- **Backlog housekeeping is required scope** - the item is already closed and `git mv`d on this branch,
  with the Resolution recording the option-A/option-B correction and the citation drift. Done rather than
  pending, for the first time in three iterations.

New from this iteration:

- **A `file:line` citation into a file you are actively editing is already stale.** The comment that cited
  `clearSession` at `:127-132` was pushed to `:164-169` by the same diff's own 37 inserted comment lines,
  so it was never true in any committed tree. Cite the symbol. **Candidate for durable memory**, and the
  cheapest lesson in the document.
- **A branch invalidates citations in proportion to how much it MOVES, not how much it changes.** This
  branch's behavioural delta is one `if` and it invalidated at least seven citations, one of them in its
  own brand-new test file, falsified by a commit two steps later on the same branch. Before merging, grep
  the branch's own diff for citations into files the branch touched, including the ones the branch
  created.
- **Grep for assertions about the behaviour you changed, in files with zero diff.** `useTaskLogStream.ts`
  asserted "AuthProvider redirects" and this change made that conditionally false without opening the
  file. The line-number sweep would not have found it; only a search for claims about the changed
  behaviour would.
- **An item can be right about the world and wrong about its own options.** Every factual claim in this
  item was correct and every SPA citation resolved; the defect was in the comparative argument for the
  alternative it did not recommend. Verify a recommendation's *reasons*, not only its conclusion - a
  correct recommendation resting on a false premise will be inherited by the next person who has to
  choose differently.
- **A byte-identity gate freezes wrong prose along with right behaviour.** `PasswordTab.auth.test.tsx`
  stayed untouched, which is what made its green run clean evidence, and it still carries the exact stale
  citation this slice corrected four times elsewhere. Both calls were right. **Name the tradeoff when the
  gate is declared**, so the frozen defect is a recorded cost rather than a later discovery.
- **A poll is not an await.** The plan's tests polled `getToken()` and stopped, which proves only the
  synchronous half of a two-phase async operation happened. When an operation sets state in two stages,
  waiting for the first stage's observable and asserting on the second is a race dressed as a wait. Await
  the operation's own promise. **Candidate for durable memory**, as a companion to "a cadence test must
  assert the wiring".
- **A partially-explained flake must be reported as partially explained.** A real test defect was found and
  fixed AND the originally reported failure was never reproduced. Reporting only the fix implies the
  question is closed; reporting only the non-reproduction discards a genuine defect. Say both, in that
  order, and say which one the evidence actually supports.
- **Fold Phase 1 into Phase 2 only when the item carries alternatives and a recommendation, and only by
  naming the planner as the party that must refute it.** The compression was right here and it removes the
  only artifact whose job is to disagree with the item. Move the mandate explicitly or do not compress.

## Files Most Touched

- `web/src/auth/AuthProvider.tsx` - the fix. `:113-115` is the whole behavioural delta (the identity fence,
  then the surviving currency guard); `:72-110` is the 39-line comment block that is the epoch-fence
  analogy, the fresh-`getToken()` argument, the no-generation-counter argument, and the null-token case.
  `:35` is the corrected `tokens.sql` coordinate. `:141-146` is `applyAuth`, **not touched**, and now a
  filed item.
- `web/src/lib/api.ts` - `Listener` and the widened doc (`:15-33`), `notifyUnauthorized` (`:39-57`) and
  both fire sites (`:79-81`, `:162-164`). Note that neither fire site re-reads `getToken()`; that is the
  constraint the whole fix rests on.
- `web/src/auth/AuthProvider.crossgen.test.tsx` - new, 5 tests, the evidence. Notable for holding a real
  request open behind an explicit gate promise rather than a timer, for the `tokensSeen` setup controls,
  for the `loginResult` binding and its comment (`:30-39`) which is Problem 1's fix written at the site,
  and for `:264`, which is the stale citation Findings Triage records.
- `web/src/auth/authTokenSecrecy.test.tsx` - new, 1 test, the new channel pinned through the existing
  `secretLeaks.ts` harness with a positive control on its own setup.
- `web/src/lib/api.test.ts` - the three appended tests, of which the throwing-listener one is the only
  place the fan-out wrapper is proven.
- `web/src/test/secretLeaks.ts` - **not touched, and that is the point.** It was extended by a new consumer
  rather than modified, which is what a shared instrument is for.
- `web/src/jobs/useTaskLogStream.ts:336-341` - the corrected claim in a file with no other change. The one
  finding in this slice that a line-number sweep could not have found.
- `web/src/profile/SessionsTab.tsx` - the corrected `tokens.sql` coordinates and the corrected
  user-visible copy about `GET /v1/auth/tokens`. Still renders no session list.
- `web/src/profile/PasswordTab.auth.test.tsx` - **byte-identical, deliberately**, and still carrying
  `tokens.sql:25-26` at `:106`.
- `docs/superpowers/plans/2026-08-13-cross-generation-401.md` - notable for its thirteen-row claim-check
  table and for enumerating every `onUnauthorized` registration site before widening the signature. Both
  formats are worth copying; the first is what refuted option B.

## Verification

- **This pass had no shell.** Nothing was executed. No `git log`, no `git diff`, no test run. Every claim
  below that could be checked by reading was checked against the worktree.
- **Verified by reading:** the widened `Listener` and its doc (`web/src/lib/api.ts:15-33`);
  `notifyUnauthorized` and its comment (`:39-57`); both fire sites passing the local `token` (`:79-81`,
  `:162-164`) and both `getToken()` reads preceding their requests (`:67`, `:149`); the identity fence and
  the surviving `anonymous` guard in that order (`AuthProvider.tsx:113-115`) and the comment block above
  them (`:72-110`); that the comment names `clearSession()` by symbol and no longer by line, and that
  `clearSession` is in fact at `:164-169`; `applyAuth`'s missing `catch` (`:141-146`) and
  `LoginScreen.tsx:20-24` not clearing the token, which are filed item 1; the five tests in
  `AuthProvider.crossgen.test.tsx` including the `loginResult` binding (`:30-39`), both `await loginResult`
  sites (`:128`, `:251`), both `tokensSeen` setup controls, and the stale `api.ts:128` citation at `:264`;
  the three appended tests in `api.test.ts` including the throwing-listener test (`:113-136`); the
  appended stream test and the corrected citation in `api.stream.test.ts:46`; the single test in
  `authTokenSecrecy.test.tsx` and its positive control (`:61-64`); `secretLeaks.ts` unmodified with its
  three existing consumers; the corrected `useTaskLogStream.ts:336-341`; the corrected
  `tokens.sql` coordinates at `AuthProvider.tsx:35`, `SessionsTab.tsx:39`, `:84`,
  `SessionsTab.test.tsx:72`, `profile/api.ts:68`; the **surviving** stale `:28-29` at `profile/api.ts:53`,
  `:71`, `PasswordTab.tsx:152`, `PasswordTab.test.tsx:205`, `PasswordTab.auth.test.tsx:106`; the
  **absence** of any remaining `api.ts:44-46` citation in `web/src`; `tokens.sql:22-44` confirming
  `DeleteToken`'s comment block occupies `:23-37` with `DeleteTokensForUser` at `:40-41` and
  `DeleteOtherTokensForUser` at `:43-44`; that `web/src/profile/` contains no `grid-cols-[...]` template
  and no `Table` consumer, which is the amendment's load-bearing fact; `HoloShell.tsx:49-71`, the header's
  non-wrapping flex row, which is consistent with the browser lane's header measurement; the full text of
  the closed backlog item and its Resolution; and the full text of the plan.
- **Reported by the implementing and verifying lanes, not re-run here:** the suite count 1049 -> 1059
  (arithmetic consistent with the ten tests counted by reading: 3 + 1 + 5 + 1); every mutation output,
  including the `0` call count and the `Error('boom')` result for the fan-out; the roughly 90 reruns at 4x
  to 20x contention; the 3 early positive-control failures and the 4-test cold run; every browser-lane
  measurement, including the `401`-for-token-A end-to-end run and the `/profile/sessions` 523px / 391px
  numbers used in the amendment; and the green Go gate on a zero-Go diff.
- **Not verified:** all test results, all mutation outputs, the exact commit count and diff stat of the
  branch, the file set of the diff (inferred from reading the tree, not from `git`), the browser
  measurements, and anything requiring execution. Each is attributed above.
- **The backlog item was already closed and `git mv`d to `docs/backlog/closed/` before this pass**, with
  its Resolution recording the option-A/option-B correction and the citation drift. That is the first time
  in this arc the close was not left pending for the conductor. The three items filed by this pass are in
  `docs/backlog/` as **proposals**; the human gives final accept. The exact-file-set check and the final
  gate run remain the conductor's, per the standing rule that subagent claims are verified against the
  tree rather than trusted.
