---
date: 2026-08-09
topic: dialog-hardening
branch: claude/pr-merging-session-3f03bb
range: the dialog-hardening commits on this branch (four slices, the review fixes, the re-verify fixes, and the backlog close)
---

# Session Retro: 2026-08-09 - dialog-hardening

**TL;DR:** The app's three hand-rolled dialogs now compose one modal shell.
`web/src/components/dialog/dialogStack.ts` is a module-level LIFO of open dialogs that owns a
shared body-level portal layer, the scroll lock and the `inert`/`aria-hidden` marking of the
background, all *derived* from the post-removal stack rather than restored directly;
`web/src/components/dialog/DialogShell.tsx` is the React component that portals, traps Tab by
intercepting the keydown, scopes Escape to the topmost dialog, and acquires and restores focus in
`useLayoutEffect`. `ConfirmDialog`, `ResetPasswordDialog` and `TokenRevealDialog` all wrap it, the
five `ConfirmDialog` call sites are untouched, and `TokenRevealDialog` ships
`dismissOnEscape={false}` because Escape there destroys an unrecoverable credential. Frontend-only,
zero Go; web suite **808+ from a 780 baseline**. Review returned **one high**, and that finding is
the most instructive thing in the iteration: it had already been correctly diagnosed inside the
diff and worked around as a test-ordering constraint instead of fixed. What and why are recorded in
the closed item's Resolution
(`docs/backlog/closed/idea-2026-07-01-confirmdialog-focus-trap-hardening.md`); this retro records
what is worth carrying forward, which this time is mostly about **evidence**: what the spec phase
measured before choosing, what the plan phase found by reading rather than reasoning, and what a
test-authoring workaround was quietly telling us.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-09-dialog-hardening.md`, **plan**
  `docs/superpowers/plans/2026-08-09-dialog-hardening.md` (15 tasks in four sequential slices, one
  frontend engineer; explicitly not parallelizable, since slice 1 creates the module slices 2-4
  import).
- `web/src/components/dialog/dialogStack.ts` + `dialogStack.test.ts` - the LIFO, the shared portal
  layer, the scroll lock, the background marking, and the three teardown rules, each RED-proven
  against a deliberately naive intermediate implementation before the correct one landed.
- `web/src/components/dialog/DialogShell.tsx` + `DialogShell.test.tsx` - the scrim, the
  `role="dialog"` panel exactly two elements deep, the portal, the Tab trap, the Escape scoping, and
  focus acquire/restore.
- Three migrated consumers: `web/src/components/ConfirmDialog.tsx`,
  `web/src/admin/users/ResetPasswordDialog.tsx`, `web/src/admin/TokenRevealDialog.tsx`. Public props
  unchanged, so the five `ConfirmDialog` call sites (`WorkerActions`, `WorkspacesPanel`,
  `JobActions`, `UsersTab`, `ReservationsTab`) were not edited at all.
- Appended tests in three shipped files: `UsersTab.test.tsx` (the trap, the scoped Escape, teardown
  ordering, focus restoration, and the lower-dialog-closes case), `WorkspacesPanel.test.tsx` (the
  portal/containing-block assertion), `enrollmentTokenSecrecy.test.tsx` (the layer leaves the DOM
  with the credential and retains no detached subtree).
- Exactly two sanctioned test edits, both named in the spec before a line was written:
  `TokenRevealDialog.test.tsx` (one test, the Escape product decision) and `ReservationsTab.test.tsx`
  (sweep scope plus its positive control, see Problem #7).
- Nothing outside `web/src/` and `docs/`. No Go, no SQL, no proto, no migration, **no new
  dependency**.

## Key Decisions

- **Route A - extract a shell - chosen on test-environment evidence, not on cost.** See Problem #1.
  The deciding property is that `@testing-library/user-event@14` honors `preventDefault()` on the Tab
  keydown, so a trap built by intercepting Tab is the only trap this repo can actually prove.
- **The stack keys on per-instance identity (`useId`), not on component type.** Forced by spec
  finding (a): `WorkerDetailPage` mounts two `ConfirmDialog`s from two different components with no
  shared parent state, so "which dialog is topmost" cannot be answered by a prop, a provider keyed by
  type, or any per-component convention.
- **`apply()` derives all global state from the current stack and never restores anything directly.**
  One function decides what the world looks like and it only ever reads the stack. The teardown rules
  fall out of that: remove your own id by identity (never `pop()`), remove first then `apply()`, and
  save `body.style.overflow` only on the empty to non-empty transition. That last one is the
  difference between a working page and one that can never scroll again.
- **The background is "every direct child of `document.body` except the layer", defined
  structurally.** In production that marks `#root`; under RTL it marks the container div, which has
  no `id="root"`. Defining it structurally is what keeps the `inert` assertions from being
  accidentally green in the only environment that runs them.
- **Exactly two elements deep, and no `onClick` on the scrim, ever.**
  `TokenRevealDialog.test.tsx` obtains the backdrop as `getByRole('dialog').parentElement`; an extra
  wrapper would silently retarget that click and the security assertion would keep passing while
  proving nothing.
- **`TokenRevealDialog` does not dismiss on Escape.** The component's own header already refused
  backdrop dismissal because "a stray click must never destroy the only copy of the credential";
  Escape is the same class of input, and here dismissal *is* the destructive act - `onDone` is what
  calls `create.reset()`, so there is nothing to revert to. Done sits inside the trap one Tab away,
  so no keyboard user is trapped. Recorded in the component header as the documented
  irreversible-dismissal exception rather than an oversight.
- **Byte-identical class strings, with the width in `panelClassName` rather than in the base with a
  caller override.** Same rule the Table primitive shipped under: two competing Tailwind utilities on
  one element resolve by stylesheet order, not class-attribute order, so an override is not reliable.
- **Tab is panel-scoped; Escape is document-scoped and gated on `isTopmost`.** The asymmetry is the
  design, and it was arrived at the hard way (Problems #3 and #4). Tab only needs interception while
  focus is inside the panel. Escape must still work when focus is not.

## Problems Encountered

1. **The spec phase rejected two obvious routes on measured evidence, and the measurements are the
   whole reason the choice is defensible.** Native `<dialog>` + `showModal()` is the textbook answer:
   the platform hands you the trap, the inert background, top-layer stacking and a scoped Escape. It
   was rejected because `web/node_modules/jsdom/lib/jsdom/living/nodes/HTMLDialogElement-impl.js` is,
   in its entirety, `class HTMLDialogElementImpl extends HTMLElementImpl { }` - no `showModal`, no
   `close`, no `open` reflection anywhere in the package - so every one of the ~20 existing dialog
   tests would throw, and the only workaround (a polyfill in test setup) means **the tests exercise
   the polyfill and the trap is the one thing never verified**. A headless focus-trap library was
   rejected by the same reasoning inverted: those libraries enforce through `inert`, `focusin`
   sentinels or the top layer, and `user-event@14` computes its Tab destination from a document-wide
   `querySelectorAll` (`utils/focus/getTabDestination.js:8-11`) with the string `inert` appearing
   **nowhere** in the shipped package. So the property we are required to prove is exactly the one a
   library makes unprovable in this harness. What user-event *does* honor is `preventDefault()` on
   the keydown (`event/dispatchEvent.js:27-43`), which is why the Tab-intercepting trap is both
   correct in a browser and testable here. The transferable rule: **check the test environment's
   actual capabilities before choosing an implementation**, by reading the dependency's source rather
   than by assuming a modern-ish toolchain implements a modern-ish platform feature. Both rejections
   were done by opening files in `node_modules`, and both would have been wrong if argued from
   reputation.
2. **The plan corrected the spec four times, each time from reading code rather than reasoning.**
   This is the pipeline's plan phase paying for itself, and the specific corrections are worth
   naming because none of them is the kind of thing a careful re-read of the spec would have caught:
   - **The spec's slicing made its own tests unpassable.** It put `ResetPasswordDialog` in a slice
     after `ConfirmDialog`, but the trap test tabs *inside* the reset dialog and the Escape tests
     mount reset-plus-archive together. Neither can go green while `ResetPasswordDialog` still owns a
     `document` listener and renders outside the layer. Merging the slices is the only ordering under
     which the spec's own "each slice is independently green and mergeable" claim holds.
   - **The spec's `dialogStack` API could not implement two of the spec's own rules.** Entries of
     `{ id }` cannot answer "move focus to the topmost dialog's panel" or "is the stack now empty",
     both of which the spec's own section 4.3 requires. `registerDialog` takes the panel element and
     the module exports `getTopmostPanel` and `isEmpty`.
   - **The spec left focus orphaned on `<body>` when a non-topmost dialog closes.** It specified only
     the topmost-closes case, where the promotion effect takes focus. When a *lower* dialog closes
     there is no promotion and no transition, so focus lands outside every open modal.
   - **The spec's `TokenRevealDialog` test was vacuous as sketched.** Keeping the existing order
     (backdrop click, then Escape) means the click blurs the token input first, so the Escape lands
     on `<body>` and the assertion passes regardless of what `dismissOnEscape` is set to. Hold that
     thought; it comes back as Problem #3.

   Plus two facts the spec did not state and every appended test depends on: `aria-hidden="true"`
   makes `getByRole` blind to the background (`@testing-library/dom`'s `isSubtreeInaccessible`), so
   background queries need `{ hidden: true }` or a handle captured before the dialog opens; and the
   shell's lifecycle must be `useLayoutEffect`, not `useEffect`, because React 18 runs passive
   destroys after the host node is detached, at which point focus restoration has nothing left to
   restore.
3. **The one high: a defect that had already been correctly diagnosed inside the diff and worked
   around instead of fixed.** Moving Escape from `document` onto the panel's `onKeyDown` looks like
   the tidy version of "scope Escape to the topmost dialog", and it is what the spec specified. It
   also breaks Escape entirely whenever focus is not inside the panel - and a scrim press, the single
   most common dismissal gesture, puts focus exactly there. It was a regression against `main`, on
   all three dialogs, since every one of them previously used a document-level listener and was
   immune by construction. All three review lenses found it independently.

   The part worth carrying forward is the evidence that it had *already been seen*. The sanctioned
   edit to `TokenRevealDialog.test.tsx` carries a comment explaining that Escape must be asserted
   **first**, because done the other way round "it would land on `<body>` after the backdrop click
   blurs the input, and this assertion would pass for the wrong reason". That is a correct, precise
   diagnosis of the defect. It was applied as a constraint on test ordering. **When you find yourself
   constraining a test's order, or its setup, to avoid a state, stop and ask whether that state is a
   bug rather than a test-authoring detail.** The tell here was sharp: the reason given was not "this
   ordering is clearer" but "in the other order the keystroke does not reach the dialog", which is a
   statement about production behavior wearing a test's clothes. The same sentence, read as a
   product claim, is the finding.
4. **The fix restored the mechanism the change had removed, plus the new gating.** Escape is a
   `document` listener again, gated on `dismissOnEscape && isTopmost(id)` read at event time. That is
   precisely the pre-shell behavior with the stack scoping layered on. Worth naming as its own
   lesson: **the old design was not wrong, it was under-specified.** The document listener was doing
   two jobs - dismiss on Escape, and dismiss regardless of where focus is - and the item only
   complained about the first one's lack of scoping. A rewrite that fixes the named complaint by
   replacing the mechanism silently drops the unnamed job. Before replacing a mechanism, enumerate
   what it currently guarantees, not just what it currently gets wrong. The shell also gained an
   `onMouseDown` on the scrim that `preventDefault()`s a press landing directly on it, which closes
   the single most common blur route at the source; the two coexist deliberately because one covers
   the common case and the other covers every case.
5. **Re-verify found a branch that does not do what its comment says, and it was unpinned by a
   test.** The cleanup's "focus was already on a background control" branch parks focus on the
   surviving topmost panel. React DOM's `restoreSelection` re-focuses the pre-commit `activeElement`
   after the mutation phase, which overwrites a `focus()` call made from a layout effect. Its two
   sibling branches work only because their focus target is *detached* by then, so React skips the
   restoration entirely. Those two are pinned by tests; this one was not, and it was confidently
   wrong with a confident comment. Two things follow. **A branch that no test pins can be
   confidently wrong**, and the comment density around it is not evidence of anything. And the
   iteration's claim that "every change is proven RED" was made **more broadly than it held** - it
   held for the behaviors the acceptance criteria named and not for a branch added during
   implementation to cover a case the acceptance criteria did not enumerate. That is the honest
   scope of the claim and it should be stated that way next time.
6. **Two spec-phase discoveries about the app itself, neither of which the backlog item knew.**
   `WorkerDetailPage` is a second two-dialog reproduction, and a worse one than the item's: it mounts
   two instances of the **same** component from two sibling components with no shared state, which is
   what forced per-instance identity into the stack's design. And `WorkspacesPanel`'s `ConfirmDialog`
   scrim never covered the viewport, because it renders inside `GlassPanel`, whose
   `backdrop-blur-[8px]` makes it the containing block for `position: fixed` descendants. That is a
   live visual defect that also falsifies the item's own premise that "a mouse cannot reach the
   second trigger, because the scrim covers the rows" - on that page the mouse path to a second
   dialog is open right now. Portaling fixed it as a side effect, which is a nice outcome and a
   slightly uncomfortable one: **the strongest independent justification for the central design
   decision was found by reading a CSS class string on an unrelated component.** The inventory pass
   is not a formality; this is the second consecutive iteration where reading every consumer before
   shaping the API changed the API.
7. **Portaling silently vacuumed a shipped test, and that test's positive control was already
   broken.** `ReservationsTab.test.tsx` opens the delete confirm and sweeps `container.innerHTML` for
   affinity claims the product must never make. Portaled out of `container`, every negative assertion
   goes vacuous - and the paired positive control, `/general dispatch pool/i`, would have **kept
   passing**, because that phrase also appears in the tab's own footnote, which is still inside the
   container. So the control could not detect the scope error it exists to guard against. Both were
   fixed: the sweep now reads `document.body.innerHTML`, and the control is a phrase unique to the
   dialog body, verified by `rg` to occur exactly once in the source. The contrast in the same change
   set is the instructive half: the enrollment-token secrecy checker was **not** blinded by the
   portal, because `domContainsSecret` sweeps `document.body` plus every `input`/`textarea` in the
   document rather than a render container. **A probe scoped to a render container measures a
   rendering decision; a probe scoped to the document measures the property.** When the assertion is
   "the user never sees X", the container was only ever a proxy, and the day a portal lands the proxy
   stops tracking the thing.
8. **The byte-identical existing-tests gate carried over from the Table iteration and held again.**
   Nine protected test files at zero deletions against the branch merge base, with three of them
   appended to. Exactly two sanctioned exceptions, both named in the spec before implementation
   started rather than negotiated during it. The rule stated up front was again "if an assertion
   needs adjusting, that *is* the finding and not the fix". Two iterations is enough to call it a
   standing method rather than a one-off, and its cheapness is worth noting: it costs one
   `git diff --numstat` and it converts "we think this refactor preserved behavior" into a
   mechanical fact for the files it covers.

## Findings Triage

- **One high**, ending a five-iteration streak of zero. It was the Escape regression (Problem #3),
  found independently by all three review lenses. Independent convergence was promoted to a lesson
  last iteration and it held again: three briefs landing on one behavior is near-certainty, not a
  hypothesis to re-litigate.
- **The high was in the one place the change was genuinely novel** - the mechanism swap from a
  document listener to a panel handler. The pattern that findings track novelty rather than diff size
  continues to hold; what changed this time is that the novelty was in *removing* something rather
  than adding something, and removal is where the "what did this used to guarantee" question is
  easiest to skip.
- **Re-verify found a second real defect after the high was fixed** (Problem #5), which is the
  argument for re-verifying rather than accepting a fix on the strength of the fix's own test.
- The remaining findings were fixed in the same iteration or triaged into Known Limitations below.
  One was filed as `idea-2026-08-09-body-level-portal-inert-marking` before this retro.

## Known Limitations

- **`inert` and `aria-hidden` are proven as attributes only.** `user-event` has no `inert` support
  whatsoever, so no test in this repo can show that `inert` blocks anything. They ship because they
  are what a real browser and a real screen reader act on. The one behavioral corollary the suite
  does measure is that `@testing-library/dom`'s role query stops seeing the background, which is a
  genuine ARIA-semantics check and is the whole of it. **Any future claim that "the tests prove the
  background is unreachable" is overclaiming**; the tests prove the keyboard path is trapped and that
  the attributes are applied and removed correctly.
- **The scrim's pointer occlusion is proven by neither mechanism.** jsdom does no hit-testing. That
  is exactly why the two-dialog test clicks straight through the scrim to reach the background
  trigger, and why doing so is honest rather than a cheat - Problem #6 shows the scrim genuinely
  fails to occlude on one real page.
- **Pixel neutrality is argued, not screenshotted.** The repo still has no visual regression harness
  (`idea-2026-06-03-web-e2e-harness`, open). The defenses are the byte-identical class-string rule
  and a reviewer diffing the strings. One known class-attribute-order change: the width utility now
  trails the base instead of sitting inside it; the class set is identical and Tailwind resolves by
  stylesheet order regardless, but that is an argument. `WorkerDetailPage` is a **deliberate, named**
  visual change and should be looked at in a browser once.
- **Two mounted dialogs remain possible and are now merely safe.** The design does not forbid
  stacking, deliberately. Sibling dialogs inside the layer are not marked `inert` relative to each
  other, because sequencing an inert-mark against a focus move introduces a race for a state that is
  rare and about to end. If stacked dialogs ever become a real workflow rather than an accident,
  revisit.
- **A body-level node appended *after* a dialog opens is never marked.** `apply()` iterates
  `document.body.children` on register and unregister only. Not a live defect - nothing in the app
  produces a body-level node outside `dialogStack.ts` itself - and already filed as
  `idea-2026-08-09-body-level-portal-inert-marking`.
- **Focus restoration cannot see a trigger that disappears *after* close.** All five `ConfirmDialog`
  call sites close synchronously, so the trigger is normally still connected when the cleanup runs;
  the row disappears later, in an unrelated commit, once the invalidate-on-success refetch completes.
  jsdom fires no event when a focused node is silently detached, and the two mechanisms that would
  observe it in a real browser (a `MutationObserver` or a document-level `focusout` sentinel) are the
  same shape of always-on background watcher the `focusin` sentinel was rejected for. Investigated
  and accepted rather than silently claimed fixed, which is the right disposition and is recorded in
  the source.
- **The focusable selector does not evaluate `display`/`visibility` and does not cross shadow
  roots.** No current consumer has hidden focusables or a shadow root. Stated in the header comment
  so it is not rediscovered.
- **`UserMenu` keeps its `document`-level Escape and mousedown-outside listeners.** Explicit
  non-goal: it is a popup, not a modal. It is now the last hand-rolled dismissal in the app.

## Backlog Triage

- **Already filed earlier in the iteration, from a deferred review finding:**
  `idea-2026-08-09-body-level-portal-inert-marking`.
- **Filed with this retro** (both were proposed in the spec's follow-ups section and left unfiled,
  both are specific and mechanically checkable):
  - `idea-2026-08-09-native-dialog-element-reconsideration` - revisit `<dialog>` + `showModal()`,
    **carrying its trigger condition** so it is re-evaluated rather than re-litigated: jsdom
    implementing `HTMLDialogElement`, or the e2e harness landing. The evidence for today's rejection
    is in `dialogStack.ts`'s header; the item's job is to make sure that evidence gets re-checked
    rather than treated as permanent.
  - `idea-2026-08-09-dialog-shell-sweep-test` - a test that fails if `role="dialog"`, `aria-modal` or
    the scrim class string appears outside `DialogShell.tsx`. Acceptance criterion 5 of the spec was
    a manual `rg` sweep run once at the end; the Table iteration's lesson is that a rule living only
    in a comment is a rule the next caller does not read, and the type-level trick that worked for
    `TableRow` (`Omit<..., 'role'>`) has no equivalent here.
- **Proposed and deliberately not filed:** giving `UserMenu` the same treatment. It overlaps
  substantially with the open `feature-2026-06-05-usermenu-panel-menu-roles`, whose acceptance
  already covers Escape and focus return; filing a second item would split one piece of work across
  two. Worth folding into that item when someone picks it up.
- **Explicitly not filed, per triage:** the double-Escape-before-flush property (matches `main`, and
  all three dialogs' handlers are idempotent), and the `getTopmostPanel` interior-pointer question
  (the no-interior-pointers-across-locks invariant is about mutable shared state under a lock; a DOM
  node handle returned from a synchronous module-level registry is not that shape).

## Improvement Goals

Carried forward from the recent iterations:

- **Treat the plan as an untrusted source of test design and plan-supplied code** - **honored, and
  this time inverted.** The plan was the artifact doing the correcting (Problem #2), four times over,
  each correction from reading the tree. The standing lesson still held in the other direction: the
  spec's own sketched test was vacuous, and the plan caught it. Read the goal as "every artifact in
  the chain is a draft, including the one immediately upstream of you".
- **Verify a backlog item's technical claims against the code during spec** - **honored, and it
  paid twice.** Every factual claim in the item was confirmed, and the spec then found two things the
  item did not know (Problem #6), one of which - the second two-dialog reproduction - changed the
  design.
- **A green test can be vacuous; prove RED against the real exposure** - **honored, with one honest
  exception.** The teardown rules were RED-proven against a deliberately naive intermediate
  implementation that was written specifically to be measured and then thrown away, which is the
  strongest form of this available. The exception is Problem #5: a branch added during
  implementation had no test and was wrong, so the blanket claim overreached.
- **Pair every absence assertion with a positive control, in the representation the real failure
  would take** - **honored, and it caught a control that was itself broken** (Problem #7). The
  reservations control matched the page footnote as well as the dialog body, so it could not detect
  the scope error it guarded. New corollary: **a positive control must be unique to the thing the
  negative is about.**
- **Rewriting a shared test file is coverage-losing** - **honored, as the iteration's primary gate**
  (Problem #8). Nine protected files at zero deletions against the merge base, three appended to, two
  sanctioned exceptions named in advance.
- **Teardown ends the generation before releasing the resource** - **honored, and this is the
  iteration where the frontend form of it became concrete rather than analogical.**
  `unregisterDialog` removes its own id by identity and `apply()` reads only the post-removal stack,
  so a dying dialog's cleanup can never describe a world in which it is still open. The naive
  `stack.pop()` version was written, measured failing, and replaced.
- **Identity-checked teardown** - **honored twice**, once in the stack (`unregisterDialog` by id) and
  once at the attribute level (the private `data-dialog-inert` marker, so teardown only unmarks nodes
  this module marked).
- **An overlay owns its own error surface** - **n/a as written, but adjacent.** The shell
  deliberately owns no error surface at all; content stays with the caller.
- **A wrong contract in docs is a defect / a recovery bound must be time-based / diagnose a red gate
  / calling the clear function is not evidence** - **n/a.** No client-facing contract changed, no
  recovery loop, no red gate. The secret-handling analogue *was* exercised: the credential's absence
  is asserted against `document.body` and against the detached layer node, not against a render
  container.
- **Phase 4 runs the documented pipeline** - **honored, third time.** Conductor `/code-review` fed to
  three parallel `relay-code-reviewer` lenses. The integration lane was skipped on a zero-Go diff.

New from this iteration:

- **Check the test environment's actual capabilities before choosing an implementation.** Two
  plausible routes were eliminated by opening files in `node_modules` (an empty jsdom
  `HTMLDialogElementImpl`; a document-wide, `inert`-blind tab-destination computation in
  `user-event`). Both would have been chosen if argued from reputation, and both would have produced
  a change whose central guarantee no test could measure. **Candidate for durable memory** - it
  generalizes to any decision where "the platform/library handles it" is the appealing answer.
- **A test-ordering workaround is a bug report in disguise.** When a test must be sequenced a
  particular way to avoid a state, ask whether that state is a defect. Here the workaround's own
  comment contained a correct diagnosis of a high-severity regression and it read as a test-authoring
  note (Problem #3). The tell: the justification is a claim about production behavior ("the keystroke
  would not reach the dialog"), not about test clarity. **Candidate for durable memory.**
- **Before replacing a mechanism, enumerate what it currently guarantees, not just what it currently
  gets wrong.** The document listener's unnamed second job - dismiss regardless of where focus is -
  was dropped by a rewrite aimed at its named first-job flaw (Problem #4). The old design was
  under-specified, not wrong. **Candidate for durable memory**, and it pairs with the existing
  "a wrong contract in docs is a defect" note.
- **A branch no test pins can be confidently wrong, and comment density is not evidence.** The
  focus-parking branch had a paragraph of correct-sounding reasoning and did not work, because React
  DOM restores the pre-commit `activeElement` after the mutation phase (Problem #5). Its siblings
  worked only by accident of their targets being detached. **Candidate for durable memory**, with the
  narrower React fact worth a line in the frontend notes: a layout-effect `focus()` on a node that
  stays connected can be overwritten by React's own focus restoration.
- **Scope a probe to the property, not to the render container.** A `container.innerHTML` sweep
  measures a rendering decision; the same sweep over `document.body` measures the claim. The portal
  blinded one and left the other untouched, in the same change set (Problem #7). Belongs as a
  sentence in the existing absence-assertion note.
- **State the scope of a verification claim as narrowly as it actually held.** "Every change is
  proven RED" was true of the acceptance-criteria behaviors and not of a branch added mid-flight.
  Belongs in the existing verification note rather than as a new one.

## Files Most Touched

- `web/src/components/dialog/dialogStack.ts` - the whole of the new global surface. Its header
  carries the four decisions most likely to be undone by a well-meaning later edit: why a module-level
  stack rather than a context (per-instance identity), why not native `<dialog>` with the jsdom
  evidence and the revisit trigger, the three teardown rules each with the failure mode a reviewer
  should look for, and the "read `isTopmost()` at event time, never captured" rule.
- `web/src/components/dialog/DialogShell.tsx` - where the high landed and where the fix lives. The
  header now explains the Tab/Escape asymmetry explicitly, because it looks arbitrary and is not: Tab
  only needs interception while focus is inside the panel, Escape must survive focus leaving it, and
  the panel-only version was a regression against `main` that this shell must not repeat.
- `web/src/components/dialog/DialogShell.test.tsx` - the structural pins. The two-element-depth test
  and the exact class-string assertions are what stop a future wrapper from silently retargeting the
  backdrop click in the credential dialog's security test.
- `web/src/admin/users/UsersTab.test.tsx` - the item's own reproduction, and where the four
  acceptance behaviors are measured against the real component: the trap, one Escape closing exactly
  one dialog, teardown ordering with two mounted, and focus restoration. Appended only.
- `web/src/admin/TokenRevealDialog.tsx` + `.test.tsx` - the product decision and its pin. Invariant 4
  in the header now refuses Escape for the same recorded reason it already refused a backdrop click,
  with the a11y exception argued rather than asserted. The one test that changed is the file's single
  sanctioned edit.
- `web/src/admin/reservations/ReservationsTab.test.tsx` - the self-vacuuming sweep and its broken
  positive control (Problem #7), both fixed, with a comment recording why the document-wide scope is
  the correct one.
- `web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx` - appended: the layer leaves the DOM
  with the credential and retains no detached subtree, with a positive control on that specific
  instrument. The suite's existing control was individually re-confirmed rather than assumed, because
  if it ever goes vacuous every negative in the file goes with it.
- `web/src/workers/WorkspacesPanel.test.tsx` - appended: the portal assertion for the containing-block
  defect from Problem #6.

## Verification

- Full web suite green: **808+ tests, up from a 780 baseline**, across the implementation, the review
  fixes and the re-verify fixes. Production build green (`tsc -b && vite build`), with
  `git checkout -- web/dist/` before the change set was assembled.
- Both re-run by the conductor on the settled tree rather than trusted from the implementer's report.
- **The byte-identical gate**: `git diff --numstat` against the branch merge base shows zero
  deletions for all nine protected test files, and exactly two modified test files outside that list
  (`TokenRevealDialog.test.tsx`, `ReservationsTab.test.tsx`), both sanctioned in the spec before
  implementation began. The two new test files are pure additions.
- **RED proofs recorded, not claimed**, for the teardown rules (against a deliberately naive
  intermediate `dialogStack`), for the trap, the scoped Escape, teardown ordering and focus
  restoration, and for the reservations sweep's vacuity, which was *demonstrated* by deleting the
  dialog body copy and showing the test still passed rather than argued.
- **Acceptance sweeps**: `role="dialog"`, `aria-modal` and the scrim class string resolve to
  `DialogShell.tsx` only among components; `addEventListener('keydown'` in `web/src` returns
  `UserMenu.tsx` (an explicit non-goal) and the shell's own gated listener.
- Change set confirmed to be entirely under `web/src/` and `docs/`: no Go file, no `.sql`, no
  `.proto`, no migration, no `web/dist`.
- Code review: conductor `/code-review` fed to three parallel `relay-code-reviewer` lenses
  (invariants, correctness, security), then a re-verify pass after the fixes. One high, fixed and
  re-verified. The integration lane was skipped on a zero-Go diff.
- No Go changed, so none of the six Invariants was in play directly. Two have exact frontend
  analogues here and both were honored deliberately rather than incidentally: **end the generation
  before releasing the resource** (remove from the stack, then derive the world from what remains)
  and **identity-checked teardown** (unregister your own id; unmark only the nodes you marked).
