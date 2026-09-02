---
date: 2026-09-01
topic: usermenu-focus-chain
lane: web batch lane C
items:
  - idea-2026-08-13-usermenu-outside-mousedown-drops-focus
  - idea-2026-08-13-post-logout-focus-lands-on-body
---

# Design: the UserMenu focus chain - the accepted dead-space gap, and focus on arrival at the auth screens

## Overview

Two backlog items in one family: where keyboard focus goes when the account dropdown closes,
and where it goes when the application replaces the whole shell underneath it. They ship as one
branch and one PR with one commit per item, in the order below, because the second one's
correctness argument depends on the first one's ordering rule staying exactly as it is.

1. **Item 1** - closing the dropdown by pressing non-focusable content drops focus to `<body>`.
   Frontend only, `web/src/shell/UserMenu.tsx` plus its test file. **No behaviour change.**
2. **Item 2** - signing out lands on the sign-in page with focus nowhere. Frontend only,
   `web/src/auth/LoginScreen.tsx`, `web/src/auth/RegisterScreen.tsx`, their tests, one new test
   file, and one added assertion in an existing Playwright test.

No backend change, no new endpoint, no new dependency, no new runtime mechanism. Written in
autonomous gate mode: every question that the brainstorming flow would have asked is decided here
and recorded in the Decisions section with its options and its reason.

### System-design, scalability and security lens

Stated once for the whole chain because there is very little to say, and saying so is the point.

- **Load.** Item 1 adds zero runtime code. Item 2 adds one `focus()` call per mount of an auth
  screen, applied by React's own `autoFocus` handling. There is no new listener, no new timer, no
  new scheduling primitive, no new network call and no new state. Nothing here scales with the
  number of jobs, tasks, workers or users.
- **Failure mode.** If the first control of either auth screen is renamed, removed or reordered,
  `autoFocus` silently does nothing and the page reverts to today's behaviour. That silence is the
  reason the acceptance criteria below require the property to be pinned on four paths rather than
  argued.
- **Interaction with the dialog layer.** An auth screen can only mount after `ProtectedRoute` has
  unmounted the shell, which unmounts any open `DialogShell` with it, so the new `autoFocus` can
  never race a modal's focus acquisition. There is no path on which both are mounted.
- **Threat model.** Empty, and deliberately checked rather than assumed. Focus crosses no trust
  boundary, nothing input-derived is rendered, no value is read, and no credential is touched.
  Autofocusing the email field lets a password manager offer to fill on arrival, which discloses
  nothing the page did not already ask for.
- **Invariants.** The relevant one is "end the generation before releasing the resource", in the
  frontend form CLAUDE.md gives it. `UserMenu`'s logout handler calls `closeAndRestoreFocus()`
  before `onLogout()` precisely so the containment check runs while the panel is unambiguously
  mounted; that ordering is a regression guard for this chain, not a detail. Item 1's rejected
  route is the same invariant read in the other direction - a continuation scheduled from a
  listener, running after the thing it reasons about has been released - and is the reason it is
  rejected rather than merely deferred.

## Verification pass at HEAD

Both items were treated as proposals. Every path, line reference and claim was re-read at HEAD in
`D:/dev/relay/.claude/worktrees/web-c-usermenu-focus`.

| Claim | Verdict | Evidence at HEAD |
| --- | --- | --- |
| `UserMenu.tsx:123-131` is the outside-mousedown handler, calling `close()` not `closeAndRestoreFocus()` | Confirmed | `onDown` in the `useEffect` keyed on `open` |
| The rationale comment sits at `:124-129` | Confirmed | the `close(), NOT closeAndRestoreFocus()` block |
| `close()` at `:103-105`, `closeAndRestoreFocus()` at `:95-99` | Confirmed | both helpers |
| `UserMenu.test.tsx:223-252` is the spy-based test that pins the no-steal behaviour | Confirmed | `an outside mousedown closes the menu and never touches the toggle focus` |
| Logout handler at `UserMenu.tsx:229-241`, close-then-hand-off | Confirmed | the `Log out` button's `onClick` |
| `HoloShell.onLogout` at `:22-25` awaits `logout()` then navigates to `/auth` | Confirmed | `HoloShell` |
| `LoginScreen.tsx:37` is the `h1`, `:41-47` the email `Input`, with no `autoFocus` | Confirmed | `LoginScreen` |
| Nothing else on the destination claims focus | Confirmed | no `focus()` and no `autoFocus` anywhere in `web/src/auth` |
| The app has no route-change focus or announcement policy | Confirmed | every `.focus()` in `web/src` belongs to `DialogShell`, `TokenRevealDialog` or `UserMenu`; the only two `autoFocus` uses are `ResetPasswordDialog` and `WorkerLabels`, both inside dialogs |
| The 401 teardown reaches `/auth` by a different path | Confirmed | `AuthProvider`'s `onUnauthorized` subscription sets `anonymous`; `ProtectedRoute` renders `<Navigate to="/auth" replace />` |
| A direct unauthenticated visit renders `LoginScreen` | Confirmed | `router.tsx` puts `/auth` under `PublicOnlyRoute`, which returns `<Outlet />` unless authenticated |
| The register screen has the same gap | Confirmed, and it has a second wrinkle the items do not mention | `RegisterScreen` renders an empty div until `/config` resolves (`selfRegister === null`), so its form appears on a LATER commit than the component's first |
| `Input` forwards arbitrary input attributes | Confirmed | `Input` spreads `props` onto the `<input>` |
| The unit lane errors on unhandled requests | Confirmed | `web/src/test/setup.ts` uses `onUnhandledRequest: 'error'`, so the new route-level tests must register every handler they touch |
| A real-browser logout flow already exists | Confirmed | `web/e2e/auth.spec.ts`, `logout returns to /auth and clears relay.token`, run under chromium and webkit |

### Refuted or corrected

- **Item 1 calls `queueMicrotask` and `requestAnimationFrame` interchangeable** ("Schedule the
  restore with `queueMicrotask`/`requestAnimationFrame`"). They are not, and the difference is the
  whole of route 2's correctness. A microtask queued inside a `mousedown` listener runs at the
  microtask checkpoint after that listener returns, which is **before** the browser performs
  `mousedown`'s own focusing default action; a frame or a task runs after it. On the microtask
  variant the `document.activeElement === document.body` gate therefore reads `body` in **both**
  branches in a real browser, and the focusable case is saved only by the browser overwriting the
  steal a moment later - a transient focus, a spurious focus/blur pair on the toggle and, for a
  screen reader, a spurious announcement. This is a mechanism argument from the HTML event model,
  **not measured here**, and it is recorded as a hazard to measure rather than as a fact.
- **Item 1's route 2 omits a containment capture.** As written ("restore to the toggle only if
  `document.activeElement` is `document.body`") it steals focus in the case the component already
  guards: a mouse user in Safari, where the menu is legitimately open with `activeElement ===
  document.body` and focus was never inside the panel. Any deferred restore must ALSO capture
  `ref.current.contains(document.activeElement)` synchronously in the mousedown handler and require
  it. `UserMenu.test.tsx`'s `Escape does not steal focus when focus was outside the container`
  is the existing test of that property, and route 2 as written has no equivalent for this path.
- **Item 1's stated cost for route 2, "a second scheduling primitive in a component that currently
  has none", is true of the file and misleading about the codebase.** `DialogShell.tsx:264-273`
  already defers a focus decision with `queueMicrotask` and documents why. The cost of route 2 is
  not novelty; it is that the gate is evaluated at a moment that differs between jsdom and a
  browser.
- **Item 2's route 1 offers `autoFocus` "or a `useEffect` + `ref` focus if `autoFocus` proves
  awkward" as equivalent fallbacks.** On `RegisterScreen` they are not equivalent: a
  `useEffect(..., [])` runs after the first commit, where the early return has rendered an empty
  div and the ref is null, and never runs again. The attribute is correct there and the effect is
  silently a no-op. The choice is load-bearing, not stylistic.
- **Could not verify:** `web/node_modules` is not installed in this worktree, so
  `@testing-library/user-event`'s `focusElement` and `jsdom`'s focus handling were **not** read at
  source. Versions are pinned in `web/package-lock.json`: user-event 14.6.1, jsdom 29.1.1,
  @testing-library/react 16.3.2, react 18.3.1, vitest 2.1.9. Every user-event internal cited below
  is quoted from the existing test comments in `UserMenu.test.tsx`, which cite file and line, and
  must be re-checked by the implementer against the installed copy before any of it is written
  into a comment.

## Item 1 - the accepted dead-space gap

### Decision: route 1, plus a test that makes the decision falsifiable

Route 3 stays rejected on record and is not re-proposed. Between routes 1 and 2:

**Route 1 wins, and the item's own recommendation is upheld** - but with one addition the item does
not ask for. Recording an accepted gap in a comment is a claim that nothing pins, and a comment is
exactly the artifact this project has repeatedly found drifting away from the code. So the accepted
behaviour gets a **test** as well as a sentence: pressing dead space closes the menu and leaves
focus on `<body>`. That converts "considered and left open" from prose into a red light. A future
author who implements the restore does not have to find the comment; the suite tells them the
behaviour they changed was chosen.

Why not route 2, weighed honestly:

- The population is real but narrow: a mixed-input user who tabbed into the panel and then reached
  for the mouse. A keyboard user dismisses with Escape, which already restores. A mouse user never
  had focus in the panel. The cost today is one Tab from the top of the document, and it is not a
  regression.
- Route 2's gate answers "did anything else claim focus" at a moment whose position relative to the
  browser's own focusing step is the entire question, and the default lane cannot observe that
  position at all. jsdom's ordering comes from user-event's emulation, not from the browser's event
  loop, so a jsdom test proving "the focusable branch does not steal" would be proving a property
  of the harness. The only instrument that could prove it is the Playwright lane.
- Getting it wrong re-creates focus theft in the case that currently works, which is the defect the
  whole 2026-08-13 slice existed to prevent. Trading a certain small gap for an uncertain regression
  in a larger population is the wrong direction at low priority.
- If route 2 is ever taken, it needs three things route 2 as written does not have: a
  post-default-action primitive, a synchronous containment capture, and a real-browser proof.
  That is a different, larger slice, and it is proposed as a follow-up item below rather than
  smuggled in here.

### What changes, by symbol

- `web/src/shell/UserMenu.tsx`, the comment inside `onDown` (the document `mousedown` listener
  registered by the `useEffect` keyed on `open`). Six lines appended to the existing rationale
  block. **No executable line changes.**
- `web/src/shell/UserMenu.test.tsx`: one new render helper and one new test.

Exact comment text to append inside `onDown`, immediately after the existing
`close(), NOT closeAndRestoreFocus()` paragraph and before the `if (ref.current ...)` line:

```
      // A press on NON-FOCUSABLE content is the uncovered case, accepted rather
      // than overlooked: nothing takes focus, so it falls to <body> when the panel
      // unmounts ('pressing non-focusable dead space...' in UserMenu.test.tsx pins
      // that). A fix must fire after the browser has moved focus for this press
      // and must still require focus to have been inside the panel, or it steals
      // focus in the mouse-open case closeAndRestoreFocus guards against.
```

This is hazard and constraint only. It carries no date, no history, no measurement provenance, no
count and no narrative, and it cites the test that pins its claim, per CLAUDE.md's comment policy.
The mechanism argument behind the first constraint - why a microtask is the wrong primitive - lives
in this spec and in the commit message, which are records of a moment and cannot drift.

### Keyboard and focus model (unchanged, restated so the commit is checkable)

| Route | Focus outcome | Owner |
| --- | --- | --- |
| Escape, focus inside the container | Toggle | `closeAndRestoreFocus` |
| Escape, focus outside (mouse-open) | Left alone | `closeAndRestoreFocus`'s containment check |
| Item click (link or logout) | Toggle | `onNavItemClick` / the logout handler |
| Modified item click | Untouched, menu stays open | `onNavItemClick`'s predicate |
| Tab out of the container | Destination keeps focus | `onContainerBlur` |
| Shift+Tab from the first item | Toggle, menu stays OPEN | containment check |
| Outside mousedown on a FOCUSABLE control | That control | `close()`, no focus call |
| Outside mousedown on NON-FOCUSABLE content | `<body>` | `close()`, no focus call - **the accepted gap** |

### Acceptance criteria, each mapped to a named test

| Criterion | Test |
| --- | --- |
| Pressing genuinely non-focusable content closes the menu and leaves focus on `<body>`, and does not call `toggle.focus` | NEW, `web/src/shell/UserMenu.test.tsx`: `pressing non-focusable dead space closes the menu and leaves focus on <body>` |
| A press on a focusable control still leaves focus on that control with `toggle.focus` never called | EXISTING, must stay byte-identical and green: `an outside mousedown closes the menu and never touches the toggle focus` |
| The decision is recorded where it is made, in hazard form | Review of the diff; the comment text above is the contract |
| Nothing else in the component changed | The diff of `UserMenu.tsx` contains comment lines only |

New test design, in detail, because two details decide whether it is worth anything:

- **The press target must be a bare `<div>`, not `document.body`.** The item's reason is that
  user-event blurs the active element when the click target has no focusable ancestor, so the two
  are not the same probe. Add a helper alongside `renderMenuWithSibling` that renders a
  non-focusable `<div data-testid="dead-space">` after the menu. Leave `renderMenu` and
  `renderMenuWithSibling` byte-identical. Before writing any comment about user-event's behaviour,
  read the installed `@testing-library/user-event` 14.6.1 source, per the "could not verify" note
  above.
- **The assertions must run after pending work has flushed**, or the test would stay green against
  a deferred restore and its claim to pin the decision would be false. Await one macrotask turn
  (`await new Promise((r) => setTimeout(r, 0))`) after the click, before asserting. No `act` wrapper
  is needed: no React state update happens in that window. Residual gap, stated rather than hidden:
  this does not flush a `requestAnimationFrame`-deferred restore, which jsdom schedules on a
  roughly 16 ms timer. The test pins the synchronous and task-deferred shapes.
- Positive control: Tab into the panel and assert focus landed on the `Profile` link before
  pressing, so the test cannot pass against a menu that never opened.
- Spy on `toggle.focus` with the same instrument the neighbouring test uses, so the two cannot
  disagree by measuring different things.

**Required mutation proof, and it must leave the test behind.** Append
`toggleRef.current?.focus()` to the end of `onDown` and confirm the new test goes RED **and** the
existing `an outside mousedown closes the menu and never touches the toggle focus` goes RED. Verify
the mutation actually applied by diffing against a saved copy of the file, and restore from that
copy - never `git checkout --`, which would discard the uncommitted test under proof.

## Item 2 - focus on arrival at the auth screens

### Decision: route 1, at the destination, on both auth screens

**Route 1: focus the first form control of the auth screen it lands on, using the `autoFocus`
attribute.** Route 2 (a `tabIndex={-1}` `h1` focused on mount) is not taken now, for one reason
that is about the app rather than about the technique: the application has no route-change
announcement policy, and every `.focus()` in `web/src` today belongs to a dialog or to the account
menu. Adopting the heading treatment on the auth screens alone would announce a transition there and
stay silent on every other route change in the app, which reads as an inconsistency rather than as a
policy. Route 2 is the right move the day the app grows that policy, and it is proposed as a
follow-up item below so the decision is conditioned on something findable rather than on an
intention.

The honest cost of choosing route 1: a screen-reader user arriving after a 401 teardown - the one
transition they did not ask for - is dropped into a labelled text field rather than being told where
they are. That is better than `<body>`, and it is worse than an announcement. It is recorded here so
the follow-up item is not read as a nicety.

**The fix lives at the destination, not at any departure point.** `UserMenu`'s close-then-hand-off
ordering is unchanged and is a regression guard for this commit. A destination-side fix covers all
three arrival paths - the logout navigation, the 401 teardown, and a direct unauthenticated visit -
by construction, because all three mount the same component. Three of them are pinned by tests
anyway, because "covered by construction" is an argument and the criterion asks for evidence.

**The register screen gets the same treatment, in the same commit.** It has the identical gap by the
identical cause (`RegisterScreen`'s first control, the display name input, claims no focus), it is
reached from `/auth` by a link the user tabs to, and leaving it out would make the omission look
deliberate to the next reader. One attribute and one test.

**The attribute, not a mount effect.** `RegisterScreen` returns an empty div until `/config`
resolves, so its form mounts on a later commit than the component's first. React applies `autoFocus`
when *that element* mounts; a `useEffect(..., [])` fires once, on the commit where the ref is still
null, and never again. The attribute is also applied in React's layout phase, after
`resetAfterCommit` runs `restoreSelection`, so on the logout path - where React's pre-commit focus
target is the toggle being removed in that same commit - nothing can overwrite it. Both auth screens
use the attribute for one rule, not two.

### What changes, by symbol

- `web/src/auth/LoginScreen.tsx`: `autoFocus` on the email `Input` (the `id="email"` control, the
  form's first).
- `web/src/auth/RegisterScreen.tsx`: `autoFocus` on the display name `Input` (the `id="name"`
  control, the form's first once the form renders at all).
- `web/src/auth/LoginScreen.test.tsx`: one test.
- `web/src/auth/RegisterScreen.test.tsx`: one test.
- `web/src/auth/authArrivalFocus.test.tsx`: NEW file, three route-level tests, following the render
  shape of `web/src/app/ProtectedRoute.test.tsx` (a `QueryClientProvider`, a `MemoryRouter` with
  `initialEntries`, an `AuthProvider`, and a `Routes` tree with `/auth` and one protected route).
- `web/e2e/auth.spec.ts`: one added assertion inside the existing
  `logout returns to /auth and clears relay.token` test. No new e2e test.
- `web/src/shell/UserMenu.tsx`: **unchanged.** Stated as scope, not as an omission.

### Keyboard and focus model

- On mount, `/auth` places focus on the email field and `/register` places focus on the display name
  field. Both are ordinary tab stops; no `tabindex` is added anywhere and the tab order is unchanged.
- No other route claims focus on mount. That is the current policy and this commit does not change
  it.
- No departure point moves focus toward a destination. `UserMenu` continues to restore focus to its
  own toggle and then hand off, and `HoloShell.onLogout` continues to await `logout()` and navigate.
- The transient state on the logout path is unchanged and is correct: the toggle holds focus for the
  duration of the `DELETE /v1/auth/token` round trip, then the shell unmounts and the destination
  claims focus in the same commit.

### Acceptance criteria, each mapped to a named test

| Criterion | Test |
| --- | --- |
| The sign-in screen puts focus on a named element on mount, and the page really rendered | NEW, `LoginScreen.test.tsx`: `the email field takes focus when the sign-in screen mounts` - asserts `document.activeElement` is `getByLabelText('Email')`, with the level-1 `Sign in` heading asserted present as the positive control |
| A direct unauthenticated visit to `/auth` lands with focus on the email field | NEW, `authArrivalFocus.test.tsx`: `arriving at /auth unauthenticated puts focus on the email field` |
| Signing out from the dropdown lands on the sign-in page with focus on the email field, not `<body>` | NEW, `authArrivalFocus.test.tsx`: `signing out from the account menu leaves focus on the email field, not on <body>` |
| A 401 teardown lands the same way | NEW, `authArrivalFocus.test.tsx`: `a 401 teardown lands on the sign-in page with focus on the email field` |
| The register screen behaves the same once its form renders | NEW, `RegisterScreen.test.tsx`: `the display name field takes focus once the register form renders` |
| The property holds in a real browser on the logout path | `web/e2e/auth.spec.ts`, inside `logout returns to /auth and clears relay.token`: poll the focused element's id and expect the email field |
| `UserMenu` keeps its close-then-hand-off ordering | EXISTING, must stay byte-identical and green: `Log out closes the menu, returns focus to the toggle, and still calls onLogout once` |

Test design notes that decide whether these are worth anything:

- **Every one needs a positive control that the sign-in page rendered.** An `activeElement`
  assertion against an unmounted tree is trivially satisfiable. Use the level-1 `Sign in` heading,
  found with `findByRole('heading', { name: 'Sign in', level: 1 })` on the route-level tests, which
  also gives the await that keeps React updates inside `act`.
- **The lane errors on unhandled requests.** The logout test must register `GET /v1/users/me` and
  `DELETE /v1/auth/token`; the 401 test must register `GET /v1/users/me` plus the endpoint it uses
  to provoke the 401. `authTokenSecrecy.test.tsx` is the working precedent for driving a real 401
  through a mounted `AuthProvider`.
- **The logout test drives the real component**, not a synthetic button: render the protected route
  so `HoloShell` renders `UserMenu`, click the toggle by its email accessible name, then click
  `Log out`. Anything less does not exercise the path the item is about.
- **The register test is the discriminator for the attribute-versus-effect decision.** It must
  `await` the form appearing (the `/config` handler resolves first) before asserting focus.

**Required mutation proofs.**

1. Remove `autoFocus` from `LoginScreen`: the four sign-in tests go RED. This is the headline
   discriminator and it must be run before the tests are believed.
2. Remove `autoFocus` from `RegisterScreen`: the register test goes RED.
3. Replace `RegisterScreen`'s attribute with a `ref` plus `useEffect(() => ref.current?.focus(),
   [])`: the register test must STAY RED, which is the evidence for the attribute-over-effect
   decision. If it comes back green, the decision's stated reason is wrong and the spec's rationale
   must be corrected in the retro rather than quietly kept.

Verify each mutation applied by diffing against a saved copy, and restore from the copy.

## Test lanes and gates

- `cd web && npm test` - the whole unit suite, not just the touched files.
- `cd web && npx tsc -b` and `npm run build` - both are CI gates in `web-ci.yml`.
- `make test-e2e` - required for the one added assertion. **If the lane cannot be run locally
  (it needs Docker, a Postgres container, and the chromium and webkit browsers), say so plainly in
  the report and let CI run it. Do not substitute a jsdom assertion and describe it as browser
  coverage.**
- No Go lane is touched by either commit. Do not run or claim one.
- `web/dist` is tracked and stale by convention; if anything writes to it, restore it before the
  branch is assembled.

## Commit plan

Two commits, one branch, one PR, in this order.

1. `docs`-adjacent but code-touching: the `UserMenu` comment and its pin test. The commit message
   carries the route-1-versus-route-2 argument, including the microtask-ordering hazard, since that
   argument must not live in the comment. Closes item 1.
2. The two `autoFocus` attributes, the four unit tests and the e2e assertion. The commit message
   carries the route-1-versus-route-2 argument for the destination and the
   attribute-versus-mount-effect evidence. Closes item 2.

The backlog close for both items is a `git mv` into `docs/backlog/closed/` via `/backlog close`,
which is required scope for this PR, not optional cleanup.

## Decisions

Autonomous mode: each of these is a question the flow would have asked, decided here.

1. **Item 1, route 1 or route 2?** Options: (1) record the accepted gap in the comment, no behaviour
   change; (2) a deferred restore gated on `activeElement === document.body`. **Chose route 1, plus
   a test pinning the accepted behaviour.** The gate route 2 depends on is evaluated at a moment
   whose position relative to the browser's own focusing step is unobservable in the default lane,
   so the test that would prove both branches would be proving a property of the harness; the
   population is narrow; and getting it wrong re-creates focus theft in the case that currently
   works. The added test is the part the item did not ask for: a comment is not a check.
2. **Does route 1 mean "comment only"?** Options: comment only, as the item proposes; or comment
   plus a test. **Chose comment plus test.** An accepted gap recorded only in prose is exactly the
   artifact this project has repeatedly found drifting; the test makes reversing the decision a red
   light rather than a silent flip.
3. **What primitive would route 2 need, if it is ever taken?** Options: `queueMicrotask`,
   `requestAnimationFrame`, `setTimeout`. **Recorded as: not a microtask.** A microtask runs at the
   checkpoint after the listener and before the browser's `mousedown` focusing default action, which
   makes the gate vacuous in a browser while it reads decisive in jsdom. Written into the follow-up
   item, and into the comment only as the constraint "must fire after the browser has moved focus",
   with no unverified mechanism asserted in the comment.
4. **Where does the mechanism argument live?** Options: the comment; the spec and commit message.
   **Chose spec and commit message**, per CLAUDE.md's comment policy: the comment carries the
   hazard and the constraint, the argument goes where it cannot drift.
5. **Item 2, route 1 or route 2?** Options: (1) focus the first form control; (2) `tabIndex={-1}` on
   the `h1` plus a mount focus, for the screen-reader announcement. **Chose route 1.** There is no
   route-announcement policy in the app, so route 2 on the auth screens alone is an inconsistency,
   not a policy; the destination is a two-field form whose purpose is immediate input; route 1 also
   serves the mouse user. The cost - no announcement on the 401 path - is stated above and drives a
   follow-up item.
6. **Attribute or mount effect?** Options: `autoFocus`; `useRef` plus `useEffect`. **Chose the
   attribute.** On `RegisterScreen` the form mounts on a later commit than the component, so a
   `[]`-deps effect fires with a null ref and never re-runs. The attribute also lands in the layout
   phase, after React's `restoreSelection`, which is what keeps the logout path from clobbering it.
   Mutation proof 3 is the evidence.
7. **Which control on each screen?** Options: first control; the most likely field. **Chose the
   first control on each** - email on sign-in, display name on register - so the rule is one
   sentence and does not need per-screen judgement.
8. **Does the register screen get the same treatment?** Options: in scope; out of scope as not named
   by the item. **Chose in scope, same commit.** Same gap, same cause, one attribute and one test,
   and an omission here would read to the next author as a decision.
9. **Which arrival paths get tests?** Options: the two the task requires; all three. **Chose three
   route-level tests plus one component-level test.** The 401 path is the one the user did not
   initiate and the one the item warns is easy to lose, so it is not the one to drop.
10. **Does the e2e lane get an assertion?** Options: unit tests only; add one line to the existing
    logout e2e test. **Chose to add it.** A real navigation in a real browser is the honest
    instrument for "focus lands on a named element", the flow already exists in
    `web/e2e/auth.spec.ts`, and it costs one assertion and no new test.
11. **One PR or two?** Options: split; chain. **Chose the chain the lane specifies**, two commits in
    the given order. They share one focus model and one set of regression guards, and item 2's
    argument depends on item 1's ordering rule being restated rather than changed.
12. **Does anything move into `UserMenu` for item 2?** Options: fix at the departure point; fix at
    the destination. **Destination**, per the item, and `UserMenu` is untouched by commit 2. By the
    time the shell unmounts, `UserMenu` no longer exists to move focus anywhere.

## Backlog items this closes

- `docs/backlog/idea-2026-08-13-usermenu-outside-mousedown-drops-focus.md` - closed by commit 1,
  route 1, with the accepted gap recorded in a hazard comment and pinned by a test. Its Acceptance
  section explicitly allows this outcome ("or it is recorded as accepted ... so the next reader does
  not mistake silence for coverage").
- `docs/backlog/idea-2026-08-13-post-logout-focus-lands-on-body.md` - closed by commit 2, route 1,
  applied at the destination and covering all three arrival paths.

Both close with `/backlog close`, which does the `git mv` into `docs/backlog/closed/`.

## Proposed follow-up backlog items

Proposals only. None of these is filed by this spec; the human accepts or declines.

1. **A deferred outside-mousedown focus restore for `UserMenu`, proven in a real browser.** The
   route 2 this spec declines, done properly: a post-default-action primitive, a synchronous
   containment capture, and a Playwright test on both branches - dead space restores to the toggle,
   a focusable control keeps focus with no `toggle.focus` call. Include the measurement that settles
   whether a microtask really does run before the focusing default action, since this spec asserts
   it as reasoning rather than as a measurement.
2. **A route-change focus and announcement policy for the SPA.** Item 2's route 2, generalized: one
   rule for where focus goes on every route change, not just the auth screens. This is the trigger
   that would make the `h1` treatment right rather than inconsistent.
3. **`PublicOnlyRoute` renders the auth screens while auth status is still `loading`.**
   `ProtectedRoute` renders a blank page during `loading`; `PublicOnlyRoute` renders `<Outlet />`,
   so a user with a valid stored token who opens `/auth` directly sees the sign-in form flash, and
   after this change it also takes focus, before the redirect to `/jobs`. Not a regression and not
   in scope here.
4. **Focus falls to `<body>` on arrival at `/jobs` after a successful sign-in.** The mirror of item
   2 in the other direction, and the same shape: the destination claims nothing. Cheap to state,
   and it belongs to proposal 2's policy rather than to a second one-off.
5. **`/register` is absent from the e2e surface list.** `web/e2e/README.md` records it as one of the
   five pages `surfaces.ts` does not carry, so commit 2's register-screen change has unit coverage
   only. Not created by this work; worth an item because this chain adds behaviour there.
