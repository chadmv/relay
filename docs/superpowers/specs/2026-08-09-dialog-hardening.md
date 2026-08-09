# Dialog hardening: one modal shell, a focus trap, a scoped Escape

- Date: 2026-08-09
- Backlog item: `docs/backlog/idea-2026-07-01-confirmdialog-focus-trap-hardening.md`
- Surface: frontend only (`web/src/`). Zero Go, zero SQL, zero proto, no new dependency.
- Status: design approved for planning. This spec was authored in an unattended run, so the
  interactive section-by-section approval gate of the brainstorming flow was not available; every
  decision below is recorded with its rationale and its evidence so the gate can be applied to the
  written artifact instead.

## 1. Problem

Three hand-rolled dialogs, five `ConfirmDialog` call sites, and no focus trap, no background
`inert`/`aria-hidden`, no scroll lock, and three separate `document`-level `keydown` listeners. The
two gaps compose: the missing trap is the mechanism that makes the document-global Escape reachable,
and one of the three dialogs discards an unrecoverable credential on Escape.

### 1.1 The item's claims, re-verified against the tree

Every factual claim in the backlog item holds. Verified rather than trusted, since this item has
already corrected itself once and a sibling item in the same batch shipped with a stale count.

| Claim | Verdict |
| --- | --- |
| Three implementations | Confirmed: `web/src/components/ConfirmDialog.tsx`, `web/src/admin/users/ResetPasswordDialog.tsx`, `web/src/admin/TokenRevealDialog.tsx` |
| Each has `role="dialog"` + `aria-modal` + `aria-labelledby` + a first-element focus | Confirmed, all three |
| Each has zero trap, zero `inert`, zero scroll lock, one `document` keydown listener | Confirmed, all three (`ConfirmDialog.tsx:26-33`, `ResetPasswordDialog.tsx:30-36`, `TokenRevealDialog.tsx:95-101`) |
| Five `ConfirmDialog` call sites | Confirmed, exactly five: `workers/WorkerActions.tsx:108`, `workers/WorkspacesPanel.tsx:71`, `jobs/JobActions.tsx:71`, `admin/users/UsersTab.tsx:279`, `admin/reservations/ReservationsTab.tsx:262` |
| `UsersTab` can mount two dialogs at once | Confirmed: `setResetting(u)` at `:178`, `setConfirm({...})` at `:180-181`, neither clears the other; independent render blocks at `:278` and `:297` |
| The project carries no focus-trap dependency | Confirmed: `web/package.json` has six runtime deps, none related |

### 1.2 Four things the item does not know

**(a) `UsersTab` is not the only two-dialog page, and the second one is worse.**
`WorkerDetailPage` renders `WorkerActions` (`:96`) and `WorkspacesPanel` (`:127`). Each owns its own
`ConfirmDialog` behind independent state (`WorkerActions.tsx:17` `confirm`, `WorkspacesPanel.tsx:28`
`confirmId`) in *different components with no shared parent state at all*. So two instances of the
**same** component can be mounted simultaneously. Consequence for the design: "which dialog is
topmost" cannot be decided by component type, by a prop, or by any per-component convention. It needs
per-*instance* identity. The item's "three implementations" framing undercounts the problem.

**(b) `WorkspacesPanel`'s scrim does not cover the viewport today.**
`WorkspacesPanel` is rendered inside `<Panel title="Source workspaces">`
(`WorkerDetailPage.tsx:126-128`), and `Panel` composes `GlassPanel`, whose base class string carries
`backdrop-blur-[8px]` (`web/src/components/holo/GlassPanel.tsx:8-10`). An element with a
`backdrop-filter` other than `none` becomes the containing block for `position: fixed` descendants.
So that `ConfirmDialog`'s `fixed inset-0` scrim is clipped to the panel box, not the viewport. Two
consequences: it is a live visual defect, and the item's premise that "a mouse cannot reach the second
trigger, because the open dialog's `fixed inset-0` scrim covers the rows" is **false on that page** -
the mouse path to a second dialog is open there right now. This is the independent justification for
portaling (section 4.2).

**(c) One existing test goes silently vacuous if the dialog is portaled, and its own positive control
does not catch it.**
`web/src/admin/reservations/ReservationsTab.test.tsx:110-131` opens the delete confirm and sweeps
`container.innerHTML` for affinity claims the product must never make. Portaling the dialog out of
`container` would make every negative assertion vacuous, and the paired positive control
`expect(html).toMatch(/general dispatch pool/i)` would **still pass**, because the same phrase appears
in the tab's own footnote (`ReservationsTab.tsx:253-254`). This is the Table iteration's "a gate can
be silently satisfied by the bug it should catch", one iteration later, in the file the byte-identical
gate is supposed to protect. Handling is specified in section 7.3.

**(d) The two routes the item proposes are not the only two, and the third one is dead here.**
Native `<dialog>` + `showModal()` would hand us the trap, the inert background, top-layer stacking and
a scoped Escape from the platform. It is rejected on hard evidence, not taste; see section 3.

## 2. Goals and non-goals

**Goals**

1. Focus is trapped in the open dialog: Tab and Shift+Tab cycle within it and cannot reach background
   controls.
2. The background is `inert` and `aria-hidden`, and page scroll is locked, while any dialog is open,
   and all three are restored exactly when the last dialog closes.
3. Escape dismisses exactly one dialog: the topmost.
4. Focus returns to the element that opened the dialog when it closes.
5. All three implementations share the hardened behavior; none is fixed in isolation.
6. A recorded decision for `TokenRevealDialog`'s Escape.

**Non-goals**

- Backdrop-click dismissal. **None** of the three dialogs has an `onClick` on its overlay today, and
  `TokenRevealDialog`'s header comment (invariant 4) records that as deliberate. The shell must not
  add one; adding it would change behavior at five call sites and reintroduce a defect the code was
  written to avoid.
- Animation, transitions, `::backdrop` styling, or any visual change. This change must be
  pixel-neutral.
- A `DialogShell` that owns titles, bodies, or buttons. It owns the scrim, the panel box, and the
  modal *behavior*. Content stays with the caller. `ResetPasswordDialog` and `TokenRevealDialog` are
  deliberately not `ConfirmDialog` variants; the extraction must not force them through
  `ConfirmDialog`'s text-only API.
- `UserMenu`'s `document`-level Escape (`web/src/shell/UserMenu.tsx:19-27`). It is a popup, not a
  modal, and it is out of scope. Filed as a follow-up in section 11.
- Making `UsersTab`'s two dialog states mutually exclusive. Explicitly rejected: it treats the symptom
  on one page and hides the general defect that finding (a) proves exists elsewhere.
- Scrollbar-width compensation (`padding-right`) when the scroll lock engages. The app has no
  always-visible scrollbar assumption and no test can observe it; adding it is unverifiable churn.

## 3. Route selection

### Route C - native `<dialog>` + `showModal()`: rejected, on test-environment evidence

Browser support is not the problem (Baseline since March 2022, and this app is an internal console).
The blocker is that the acceptance criteria are test-shaped and this route makes them unwritable:

- **jsdom 29.1.1 does not implement it.** `web/node_modules/jsdom/lib/jsdom/living/nodes/HTMLDialogElement-impl.js`
  is, in its entirety, `class HTMLDialogElementImpl extends HTMLElementImpl { }`. There is no
  `showModal`, no `close`, no `open` reflection anywhere in the package (`rg showModal web/node_modules/jsdom`
  returns nothing). A component calling `showModal()` throws `TypeError` in every one of the ~20
  existing dialog tests.
- The workaround is a hand-rolled polyfill in test setup, at which point **the tests exercise the
  polyfill, not the platform** - the trap that is the whole point of the route would be the one thing
  never verified.
- It also forces a scrim rewrite (`::backdrop` instead of the current `bg-black/60` overlay div),
  which breaks the pixel-neutrality goal and `TokenRevealDialog.test.tsx:82`'s
  `getByRole('dialog').parentElement` backdrop handle.

Revisit when jsdom implements `HTMLDialogElement`, or when the project gains a real-browser harness
(`idea-2026-06-03-web-e2e-harness`, still open). Record this in the shell's header comment so the next
person does not re-derive it.

### Route B - adopt a headless dialog library: rejected

Seriously weighed. The honest case for it is that a maintained focus trap has handled edge cases we
have not thought of.

Against it:

- **A new runtime dependency is a real cost here.** `web/package.json` carries six runtime deps, every
  one load-bearing. A Radix/Headless-UI dialog brings a transitive tree for one component.
- **It breaks the byte-identical-tests gate outright.** These libraries own the markup. Every existing
  `getByRole('dialog')`, `toHaveAccessibleName`, `.parentElement` backdrop handle and
  `within(dialog)` query would need re-verification, which destroys the proof that behavior was
  preserved.
- **The decisive one: the thing we must prove is exactly the thing a library makes unprovable in this
  harness.** These libraries enforce the trap through `inert`, `aria-hidden`, `focusin` sentinels, or
  the top layer. `@testing-library/user-event@14` computes its Tab destination from a
  *document-wide* `document.querySelectorAll(FOCUSABLE_SELECTOR)`
  (`web/node_modules/@testing-library/user-event/dist/cjs/utils/focus/getTabDestination.js:8-11`) and
  the string `inert` appears **nowhere** in the shipped package. It has no `aria-hidden` awareness and
  no top-layer awareness. So under this suite, `userEvent.tab()` walks straight past an `inert`
  background into a "trapped" dialog's page, and the acceptance criterion "focus cannot Tab to
  background elements" cannot be asserted.

### Route A - extract a shared `DialogShell` the three compose: **chosen**

The one mechanism user-event *does* honor is `preventDefault()` on the `keydown`:
`dispatchEvent.js:27-43` runs the Tab behavior only `if (!defaultPrevented)`. So a trap built by
intercepting the Tab keydown and moving focus itself is both correct in a browser and fully
exercisable in this suite. That is the deciding property, and it is why Route A is not merely the
cheap option.

`inert` and `aria-hidden` still ship, as the browser- and AT-facing mechanism and as defense in depth.
The spec is explicit about what each test proves (section 7.5): the trap is proven behaviorally, the
`inert`/`aria-hidden` attributes are asserted as attributes only.

## 4. Design

Two new modules, no barrel (two files do not need one):

- `web/src/components/dialog/dialogStack.ts` - a module-level LIFO of open dialogs plus the global
  side effects derived from it.
- `web/src/components/dialog/DialogShell.tsx` - the React component the three dialogs wrap.

### 4.1 `dialogStack.ts`

State: an ordered array of entries `{ id: string }`, module-level, plus a `Set` of subscribers, plus
one saved `previousBodyOverflow` string and one `layer: HTMLElement | null`.

API:

```ts
export function registerDialog(id: string): void       // push, then apply()
export function unregisterDialog(id: string): void     // remove THIS id, then apply()
export function isTopmost(id: string): boolean
export function subscribe(fn: () => void): () => void  // for useSyncExternalStore
export function getLayer(): HTMLElement                 // portal target, created on demand
export function __resetForTest(): void                  // test-only escape hatch, see 7.6
```

`apply()` derives *all* global state from the current stack. It never "restores" anything directly:

- Stack empty -> remove `inert` and `aria-hidden` from every marked background node, set
  `document.body.style.overflow` back to the saved value, remove the layer element from
  `document.body`, and clear the saved value.
- Stack non-empty -> ensure the layer exists and is appended to `document.body`; on the empty ->
  non-empty transition only, save `document.body.style.overflow` and set it to `hidden`; mark every
  direct child of `document.body` *except the layer* with `inert=""` and `aria-hidden="true"`.

**Background is defined as "every direct child of `document.body` except the dialog layer"**, not as
`document.getElementById('root')`. In production that marks `#root`; under React Testing Library it
marks the RTL container div, which has no `id="root"`. Defining it structurally is what makes the
behavior the same in both, and what makes section 7's inert assertions meaningful rather than
accidentally green.

**Invariant application - "end the generation before releasing the resource."** This is the frontend
instance of the project invariant, and with two dialogs mounted it is concrete rather than theoretical.
Three rules, each with a named failure mode a reviewer should look for:

1. **`unregisterDialog` removes its own id by identity, never `stack.pop()`.** Failure mode: dialog A
   closes while B is open; a `pop()` teardown removes **B**, leaving A's dead id topmost. Escape then
   does nothing and the scroll lock releases while B is still on screen. This is the
   "identity-checked teardown" invariant verbatim, in a React effect cleanup.
2. **Remove from the stack first, then `apply()`.** Never restore-then-remove. `apply()` reads only the
   post-removal stack, so a dying dialog's cleanup cannot describe a world where it is still open.
3. **Every consumer of "am I topmost" reads it at event time**, never from a value captured at effect
   setup. A captured value is a stale generation by another name.

The saved `previousBodyOverflow` is written **only** on the empty -> non-empty transition. A per-dialog
save/restore pair would have the second dialog save the value `hidden` that the first one wrote, and
the last close would then restore `hidden` permanently - a page that can never scroll again.

### 4.2 Portal

`DialogShell` renders through `createPortal(..., getLayer())`. The layer is a single
`<div data-dialog-layer>` appended to `document.body`, shared by all dialogs, removed when the stack
empties (so nothing leaks between tests, and so `document.body`'s child list returns to exactly what
it was).

Justification, in order of weight:

1. It fixes the live containment defect from finding (b). A `fixed` scrim inside a `backdrop-filter`
   ancestor is not viewport-sized; portaling to `document.body` removes every ancestor stacking
   context by construction, for this case and for every future caller.
2. `inert` on the background is otherwise inexpressible: if the dialog lives inside `#root`, marking
   `#root` inert marks the dialog too, and `aria-hidden` on an ancestor of a focused element is an
   outright AT violation.
3. Sibling dialogs land in one container in mount order, so paint order agrees with the Escape order
   the stack defines without any dynamic `z-index`.

Portal safety was swept, not assumed: every `container.`-scoped assertion in the web suite was
reviewed, and exactly one is affected - `ReservationsTab.test.tsx`, finding (c), handled in 7.3. The
enrollment-token secrecy suite is unaffected because `domContainsSecret` scans `document.body` and all
`input`/`textarea` elements document-wide (`web/src/test/secretLeaks.ts:71-77`), not a container; its
positive control must nonetheless be re-run and confirmed (7.4), because if that control ever goes
vacuous the entire secrecy suite does with it.

### 4.3 `DialogShell.tsx`

```ts
interface DialogShellProps {
  titleId: string                                 // caller owns useId and renders its own <h2 id>
  onDismiss: () => void
  dismissOnEscape?: boolean                       // default true
  initialFocusRef?: RefObject<HTMLElement | null> // optional; default = first focusable in the panel
  panelClassName?: string                         // per-caller sizing only
  children: ReactNode
}
```

**Rendered structure - exactly two elements deep, non-negotiable:**

```
<div className={SCRIM}>                                     <- no onClick, ever
  <div role="dialog" aria-modal="true" aria-labelledby={titleId} tabIndex={-1}
       className={`${PANEL_BASE} ${panelClassName ?? ''}`}
       onKeyDown={...}>
    {children}
  </div>
</div>
```

The depth is a hard constraint, not an implementation detail: `TokenRevealDialog.test.tsx:82` obtains
the backdrop as `screen.getByRole('dialog').parentElement` and clicks it to prove a stray click cannot
destroy a credential. An extra wrapper would silently retarget that click and the test would keep
passing while proving nothing - a self-vacuuming security assertion.

**Class strings, byte-identical only.** All three dialogs today carry the *identical* scrim string
`fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4`
(`ConfirmDialog.tsx:36`, `ResetPasswordDialog.tsx:61`, `TokenRevealDialog.tsx:126`), so the whole
string becomes `SCRIM` in the shell. The panel strings differ in exactly one token:
`w-full max-w-sm rounded-card border border-border bg-bg p-5 shadow-xl` for the first two and
`max-w-lg` for `TokenRevealDialog`. So `PANEL_BASE` is
`w-full rounded-card border border-border bg-bg p-5 shadow-xl` and `panelClassName` carries
`max-w-sm` / `max-w-lg`. The width must not sit in the base with a caller override: two competing
Tailwind utilities on one element resolve by stylesheet order, not class-attribute order, so an
override is not reliable. Same rule the Table primitive was built under.

**`tabIndex={-1}` on the panel** exists so the shell has a focus target if a caller ever has no
focusable content. It does not enter the tab ring: user-event filters out elements whose `tabindex`
parses below zero (`getTabDestination.js:11`), and browsers do the same.

**Effects, in order:**

1. `useId`-derived stable instance id. `registerDialog(id)` on mount, `unregisterDialog(id)` in the
   cleanup. Note the id is per-instance, which is what finding (a) requires.
2. Subscribe to the stack via `useSyncExternalStore` so `isTopmost` is reactive.
3. **Focus acquisition, keyed on the `false -> true` transition of `isTopmost` and on nothing else.**
   Target: `initialFocusRef?.current ?? firstFocusable(panel) ?? panel`. Keying on the transition
   covers both "I just mounted" and "the dialog above me just closed, I am topmost again" with one
   rule.
   The dependency array is load-bearing and there is a shipped test that proves why:
   `TokenRevealDialog.test.tsx:129-148` ("a re-render with a fresh onDone identity does not steal
   focus back to the token input") exists because an earlier version keyed a focus effect on a
   callback identity that changes on every parent re-render, yanking focus off the Done button every
   60 seconds. `onDismiss` must therefore be read through a ref inside the handlers, never listed as a
   dependency of the focus effect. **That existing test is now the regression gate on the shell**, and
   must stay green unmodified.
4. **Trigger capture and restore.** Capture `document.activeElement` once at mount, before moving
   focus. On unmount: `unregisterDialog` runs first (rule 2 above); then, if the stack is now
   non-empty, do nothing and let the newly-topmost dialog's transition effect take focus; if the stack
   is empty and the captured element is still connected to the document, focus it. Guard the restore
   on focus currently being inside this panel, so a dialog that closes while the user has clicked
   elsewhere does not yank focus back.

**Keyboard handling lives on the panel, not on `document`.** This is the substantive change and it is
what makes the Escape scoping mechanical rather than conventional: with focus inside the topmost
dialog by construction, a panel-scoped `onKeyDown` is received by exactly one dialog. The three
`document.addEventListener('keydown', ...)` registrations are deleted.

- `Escape`: if `dismissOnEscape` is false, ignore. If `isTopmost(id)` is false, ignore (defense in
  depth for the case where focus somehow sits in a lower dialog). Otherwise call `onDismissRef.current()`.
- `Tab` / `Shift+Tab`:
  - If `isTopmost(id)` is false: `preventDefault()` and move focus to the topmost dialog's panel. A
    non-topmost dialog must not be a route out.
  - Otherwise compute the panel's focusables in DOM order. If `Shift+Tab` on the first (or focus is
    the panel itself), `preventDefault()` and focus the last. If `Tab` on the last, `preventDefault()`
    and focus the first. Otherwise do nothing and let the default proceed.

Focusable selector, stated explicitly so it can be kept in agreement with the tests:
`a[href], button:not([disabled]), input:not([type=hidden]):not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex^="-"])`.
Known limitation, accepted: it does not evaluate `display`/`visibility` and does not cross shadow
roots. No current consumer has hidden focusables or a shadow root. State this in the header comment
rather than leaving it to be rediscovered.

**Rejected trap mechanisms**, recorded so they are not re-proposed:

- *`focusin` sentinel on `document` that pulls focus back into the topmost panel.* Rejected: two naive
  focus-pulling traps mounted simultaneously livelock, ping-ponging focus. Our stack guard would
  prevent that, but the mechanism buys nothing the Tab interception plus `inert` does not already
  cover, in exchange for a whole class of infinite-loop bug.
- *Zero-size focusable sentinel `<div>`s at the panel's edges.* Rejected: they add DOM nodes inside a
  panel whose `innerHTML` is swept by both the reservations honesty test and the enrollment secrecy
  suite, for no gain over the keydown path.

### 4.4 Consumer changes

`ConfirmDialog.tsx` - delete the effect and the outer two divs; wrap children in `DialogShell` with
`panelClassName="max-w-sm"`, pass the existing `cancelRef` as `initialFocusRef`, `onDismiss={onCancel}`.
Public props unchanged, so the five call sites are untouched.

`ResetPasswordDialog.tsx` - delete the effect and the outer div; the `<form>` moves *inside* the
shell's panel and loses its `role`/`aria-modal`/`aria-labelledby` (the shell's panel carries them).
Keeps `autoFocus` on the password `Input`, which is also what `firstFocusable` selects, so
`Input` still does not need `forwardRef`. The existing assertions
(`getByRole('dialog')` has `aria-modal`, has the accessible name, and `toContainElement` the error
node) all hold with the form nested, and `<button type="submit">` still submits its nearest form.

`TokenRevealDialog.tsx` - delete the Escape effect; keep the mount-only focus+select effect (the shell
would focus the same input, but the `select()` is the caller's own requirement and its test asserts
`selectionStart`/`selectionEnd`). Pass `panelClassName="max-w-lg"` and `dismissOnEscape={false}` (see
section 5). Header comment: invariant 4 gains the Escape decision alongside the backdrop-click one,
and invariant 5's "NO focus trap, same as ConfirmDialog and ResetPasswordDialog" is replaced by a
pointer to the shell.

## 5. `TokenRevealDialog`'s Escape: decision

**Decision: Escape does not dismiss `TokenRevealDialog`. `dismissOnEscape={false}`. The "Done - I have
copied it" button becomes the only dismissal.**

Rationale:

- **The component's own recorded reasoning already decides this.** Invariant 4 in its header refuses
  backdrop-click dismissal because "a stray click must never destroy the only copy of the credential",
  while accepting Escape "preserving the baseline of the two shipped dialogs". Escape is the same
  class of input as a stray click: single, low-intent, no target, frequently reflexive. The stated
  reason for refusing one applies verbatim to the other; only the appeal to consistency separated
  them, and this change removes that appeal by making consistency a property of the shell rather than
  of the dismissal policy.
- **Escape here is not "cancel", it is "done".** The dialog's dismissal *is* the destructive act -
  `onDone` is what calls `create.reset()`. There is no state to revert to. A dialog whose Escape is
  indistinguishable from its primary action has no cancel affordance to preserve.
- **The known near-miss makes it concrete.** The 2026-08-09 review of that tab fixed a defect where the
  dialog stole focus back every 60 seconds; the recorded consequence was a keyboard admin pressing
  Enter on Done, getting nothing, and reaching for Escape next. The trap and the focus fix remove the
  trigger; removing Escape removes the loaded weapon.
- **Confirm-before-discarding was considered and rejected.** WAI-ARIA APG's guidance for a destructive
  cancel is a confirmation, so this is the orthodox choice. It is wrong here: it stacks a second modal
  on top of the credential modal - the exact configuration this whole change exists to make safe - to
  guard against a keystroke the user can simply not press, and it ends in a confirm dialog asking
  whether you meant to close the dialog that says do not close me.
- **The a11y objection is answered.** APG requires Escape so a keyboard user is never trapped. They
  are not trapped: the primary Done button is inside the trap, is reachable by one Tab, and is the
  only exit by design. This is the documented exception case (an irreversible dismissal), not an
  oversight, and it must be written into the component header as such.

Consequence for tests: `web/src/admin/TokenRevealDialog.test.tsx` is the **one file explicitly exempt
from the byte-identical gate**, because the item's own acceptance asks for a product decision here.
Exactly one test changes: `:80` "a backdrop click does NOT dismiss it, but Escape does (paired
positive control)". Its positive control moves from Escape to the Done button, and it gains an
assertion that Escape does *not* call `onDone`. The paired-control structure - a negative assertion is
only meaningful next to a positive one on the same instrument - must survive the edit.

## 6. Data flow and failure modes

| Event | Sequence |
| --- | --- |
| First dialog opens | `registerDialog(id)` -> stack `[]` -> `[A]` -> `apply()` creates the layer, saves `body.style.overflow`, sets `hidden`, marks the RTL/`#root` background `inert` + `aria-hidden` -> shell's `isTopmost` transition effect focuses the initial target -> trigger captured before that |
| Second dialog opens over it | `registerDialog(B)` -> `[A, B]` -> `apply()` is idempotent for the globals -> B's transition effect takes focus. A's `isTopmost` flips to `false`; A does not move focus and stops handling keys |
| Escape | Delivered to `document.activeElement`, which is inside B; bubbles to B's panel; B is topmost -> `onDismiss` |
| B closes | `unregisterDialog(B)` removes **B's id by identity** -> `[A]` -> `apply()` sees a non-empty stack, so scroll stays locked and the background stays inert -> A's `isTopmost` goes `false -> true` -> A refocuses its initial target. B does **not** restore the trigger, because the stack is non-empty |
| A closes | `unregisterDialog(A)` -> `[]` -> `apply()` unmarks the background, restores the saved overflow, removes the layer -> A restores focus to its captured trigger if it is still connected |

Failure modes deliberately designed against:

- `stack.pop()` in a cleanup - removes the wrong dialog. Rule 4.1.1.
- Per-dialog save/restore of `body.style.overflow` - permanently unscrollable page. Section 4.1.
- Focus effect keyed on a callback identity - focus theft on every parent re-render. Section 4.3.3.
- Restoring the trigger while another dialog is open - focus lands behind the scrim. Section 4.3.4.
- Two traps fighting over focus - avoided by construction (only the topmost acts, and no `focusin`
  sentinel exists). Section 4.3.
- A dialog closing without its cleanup running (React strict-mode double-invoke, hot reload) -
  `registerDialog` must be idempotent per id and `unregisterDialog` must tolerate an absent id.

## 7. Testing

The Table iteration's central method applies directly, and this spec adopts it deliberately: **decide
which testing regime each file is in before writing a line.** A behavior-preserving change's gate is
*the tests did not change*; an observable change's gate is *the test was seen failing first*. This
change is in both regimes at once, split by file.

### 7.1 Regime 1 - the preservation gate

For these files, `git diff` must show **zero deleted or modified lines**; additions may be appended.
Checked against the branch merge base, not just `HEAD`, so no earlier commit can have touched them.

- `web/src/components/ConfirmDialog.test.tsx`
- `web/src/admin/users/ResetPasswordDialog.test.tsx`
- `web/src/workers/WorkerActions.test.tsx`
- `web/src/workers/WorkspacesPanel.test.tsx`
- `web/src/jobs/JobActions.test.tsx`
- `web/src/admin/users/UsersTab.test.tsx` (append-only; T1-T4 land here)
- `web/src/admin/enrollments/EnrollmentsTab.test.tsx`
- `web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx`
- `web/src/jobs/JobDetailPage.test.tsx`

Stated up front, as it was for the Table: **if an assertion needs adjusting, that *is* the finding and
not the fix.** Two test files are named exceptions and no others: `TokenRevealDialog.test.tsx`
(section 5) and `ReservationsTab.test.tsx` (section 7.3). The gate is on test files; source files
change freely.

### 7.2 Regime 2 - RED-proven new behavior

All four go in `web/src/admin/users/UsersTab.test.tsx` against the real component, appended. Each RED
run must be **recorded as output**, not claimed.

**T1 - the trap, via the item's real path.** Render the tab, click
`Reset password for ada@studio.dev` to open the reset dialog, then `await userEvent.tab()` in a loop
of at least 10 (more than the dialog's focusable count, so the ring is proven to wrap rather than
merely not-yet-escape). After every step assert `screen.getByRole('dialog').contains(document.activeElement)`,
and separately assert that `screen.getByRole('button', { name: 'Archive ada@studio.dev' })` never
holds focus. RED today at the step where focus leaves the panel. This is the test the whole
`inert`-versus-keydown analysis in section 3 exists to make possible.

**T2 - one Escape closes exactly one. The centerpiece.**

```
render -> click "Reset password for ada@studio.dev"      // dialog 1, real path
       -> click "Archive ada@studio.dev"                 // dialog 2, background trigger
       -> expect(screen.getAllByRole('dialog')).toHaveLength(2)
       -> await userEvent.keyboard('{Escape}')
       -> expect(screen.getAllByRole('dialog')).toHaveLength(1)
       -> expect(the survivor).toHaveAccessibleName('Reset password for ada@studio.dev?')
```

Three construction notes the plan must carry verbatim, because each is a place a careless
implementation makes the test lie:

- **Use `userEvent.click` on the background Archive button, not a Tab-to-it sequence.** After the fix
  the trap makes the Tab route unreachable *by design*, so a test that reaches the second trigger by
  tabbing can only ever be RED. The click route is legitimate rather than synthetic: jsdom performs no
  hit-testing so the scrim occludes nothing there, and in a real browser finding (b) shows the scrim
  genuinely fails to cover the page on `WorkerDetailPage`. It also models every non-Tab route to a
  second dialog - a click landing before paint, an AT activation, a dialog opened by an async result.
- **Assert the survivor by accessible name, never by array index.** The DOM order of the two dialogs is
  `ConfirmDialog` then `ResetPasswordDialog` (JSX order at `UsersTab.tsx:278` and `:297`) regardless of
  which opened first, so an index assertion tests the wrong property and would pass for the wrong
  reason.
- **This test must not depend on the trap.** T1 owns the trap. T2 owns Escape scoping. If T2 could only
  be constructed through the trap, then a trap regression would take the Escape guarantee down with
  it and neither would be independently measurable.

RED today: both `document` listeners fire, `getAllByRole('dialog')` throws (zero matches). Record that
exact failure.

**T3 - teardown ordering with two dialogs mounted.** From T2's two-dialog state: after the first
Escape, assert the background element still carries `inert` and `document.body.style.overflow` is
still `hidden`; after the second Escape, assert both are cleared and `data-dialog-layer` is gone from
`document.body`. Honesty requirement: T3 is **not** RED against today's code in the `inert`/overflow
sense, because neither exists yet. It is RED against the plausible wrong implementation, so the plan
must sequence it that way - land `dialogStack` with an unconditional restore and a `stack.pop()`
teardown first, record T3 failing, then apply rules 4.1.1 and 4.1.2. A green T3 written after the
correct implementation proves nothing.

**T4 - focus restoration.** Click `Archive ada@studio.dev` (which leaves focus on that button), press
Escape, assert `screen.getByRole('button', { name: 'Archive ada@studio.dev' })` has focus. RED today
(focus is left on the removed dialog's node, so `document.activeElement` falls back to `body`).

**T5 - `dialogStack` unit tests** in `web/src/components/dialog/dialogStack.test.ts`: identity-checked
removal (`register a; register b; unregister a` leaves `b` topmost), idempotent double-register,
tolerant unregister of an unknown id, and the overflow save happening exactly once across two
registrations.

**T6 - `DialogShell` unit tests** in `web/src/components/dialog/DialogShell.test.tsx`: the two-element
structure (`getByRole('dialog').parentElement` is the scrim and carries the exact scrim class string),
`dismissOnEscape={false}` suppresses Escape, and Shift+Tab from the first focusable lands on the last.

### 7.3 The one sanctioned edit outside the exempt file

`web/src/admin/reservations/ReservationsTab.test.tsx:110-131`, per finding (c). Protocol, following
the `WorkerDetailPage.test.tsx` precedent from the Table iteration:

1. Land the portal with this file **unmodified** and demonstrate the vacuity rather than assert it:
   temporarily delete the confirm dialog's body copy entirely and show the test still passes. Record
   that output. That is the RED proof that the assertion had gone vacuous, and it is the evidence that
   authorizes the edit.
2. Change `const html = container.innerHTML` to `const html = document.body.innerHTML` in **that test
   only** (line 117). Line 84's sweep, in the test where no dialog is open, is unaffected and must not
   be touched.
3. Add a comment recording why the wider scope is the correct one: the test's stated intent is "the
   confirm dialog carries no affinity claim", and `container` was only ever a proxy for "what the user
   sees". The assertion's scope was narrower than its intent - the same shape of defect as the
   over-wide page-global assertion the Table iteration narrowed, read in the opposite direction.
4. Strengthen the positive control while there. `/general dispatch pool/i` also matches the tab's own
   footnote, so it cannot detect the scope error it exists to guard. Replace it with a phrase unique to
   the dialog body (`confirmDeleteBody`), and confirm by `rg` that the chosen phrase appears exactly
   once in `ReservationsTab.tsx`.

### 7.4 Security re-verification

`web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx` must be re-run and its **positive
control** at `:182` (`expect(domContainsSecret(TOKEN)).toBe(true)` while the dialog is open)
individually confirmed passing. `domContainsSecret` scans `document.body.innerHTML` plus every
`input`/`textarea` value document-wide, so the portal is safe in theory; the requirement here is that
it is checked, because if that control ever fails silently, all six "the token is gone" assertions in
that file become vacuous at once. Also assert that the dialog layer element is **removed** from
`document.body` when the stack empties, so no detached subtree retains the credential.

### 7.5 What each mechanism's test actually proves

Stated plainly so no one over-reads a green suite:

- **The Tab trap is proven behaviorally.** `userEvent.tab()` honors `preventDefault()`, so T1 measures
  the real mechanism.
- **`inert` and `aria-hidden` are proven only as attributes.** user-event has no `inert` support
  whatsoever, so no test in this repo can show that `inert` blocks anything. They ship because they are
  what a real browser and a real screen reader act on; the suite asserts presence and correct teardown,
  nothing more. Any future claim that "the tests prove the background is unreachable" is
  overclaiming - the tests prove the *keyboard* path is trapped and that the attributes are correctly
  applied and removed.
- **The scrim's pointer occlusion is proven by neither.** jsdom does no hit-testing. This is precisely
  why T2 clicks straight through it and why that is honest rather than a cheat.

### 7.6 Test hygiene

`dialogStack` is module state shared across tests in a file. RTL's auto-cleanup unmounts and therefore
runs the cleanups, so the stack self-empties; `__resetForTest()` exists as a belt-and-braces
`afterEach` for the two unit-test files that drive the module directly without React.

## 8. Sequencing

Four slices, sequential (slice 1 creates the module slices 2-4 import), one frontend engineer. Slices
1-3 are each independently green and mergeable.

1. `dialogStack.ts` + `DialogShell.tsx` + their unit tests (T5, T6). Nothing consumes them yet.
2. `ConfirmDialog` composes the shell. Regime-1 gate on `ConfirmDialog.test.tsx` and the five call
   sites' test files. Then T1-T4 appended to `UsersTab.test.tsx`, each RED-proven. Includes the
   `ReservationsTab.test.tsx` protocol (7.3), since that is where the portal first reaches that page.
3. `ResetPasswordDialog` composes the shell.
4. `TokenRevealDialog` composes the shell with `dismissOnEscape={false}`; the single sanctioned edit to
   `TokenRevealDialog.test.tsx`; the 7.4 secrecy re-verification.

## 9. Acceptance criteria

Carried from the item's "Acceptance / Done When", sharpened, each mechanically checkable.

1. **Trap.** T1 green, RED output recorded. Focus cannot Tab or Shift+Tab out of an open dialog.
2. **Inert + scroll lock.** While any dialog is open, every direct child of `document.body` except the
   dialog layer carries `inert` and `aria-hidden="true"`, and `document.body.style.overflow` is
   `hidden`. All three are exactly restored when the last dialog closes, and the layer element is
   removed. T3 green, with its RED recorded against the deliberately-wrong intermediate
   implementation.
3. **Scoped Escape.** T2 green, RED output recorded: two dialogs mounted through the real `UsersTab`
   reset-plus-archive path, one Escape, exactly one closes, and the survivor is identified by
   accessible name.
4. **Focus restoration.** T4 green: focus returns to the trigger element.
5. **Shared, not fixed in isolation.** `rg 'role="dialog"' web/src` and `rg 'aria-modal' web/src` each
   return matches in `DialogShell.tsx` only. `rg "fixed inset-0 z-50" web/src` returns
   `DialogShell.tsx` only. `rg "addEventListener\('keydown'" web/src` returns no match in any of the
   three dialog files (`UserMenu.tsx` remains, out of scope).
6. **Preservation.** `git diff --numstat` shows zero deletions for every test file in the 7.1 list,
   checked against the branch merge base. Exactly two test files outside that list are modified -
   `TokenRevealDialog.test.tsx` (section 5) and `ReservationsTab.test.tsx` (section 7.3). New test
   files (`dialogStack.test.ts`, `DialogShell.test.tsx`) are additions, not modifications.
7. **TokenReveal decision recorded** in the component header alongside its existing invariants, with
   the a11y justification, and pinned by a test asserting Escape does not call `onDone`.
8. **Neutral elsewhere.** Full web suite green with a net increase; `tsc -b && vite build` green;
   `git checkout -- web/dist/` before the change set is assembled; nothing outside `web/src/` changes.

## 10. Risks and open items

- **Pixel neutrality is argued, not screenshotted.** The repo has no visual regression harness
  (`idea-2026-06-03-web-e2e-harness`, open). The defenses are the byte-identical class-string rule and
  a reviewer diffing the class strings before and after. The portal *does* change rendering on
  `WorkerDetailPage` - that is the point, it fixes finding (b) - so that page is a deliberate,
  named visual change and should be looked at in a browser once.
- **`inert` browser support** is Chrome 102+, Safari 15.5+, Firefox 112+. Acceptable for an internal
  console. `aria-hidden` is the fallback on anything older, and the Tab trap is independent of both.
- **Two mounted dialogs remain possible and are now merely *safe*.** The design does not forbid
  stacking, deliberately (section 2). Sibling dialogs inside the layer are not marked `inert` relative
  to each other, because sequencing an inert-mark against a focus move introduces a race for a state
  that is rare and about to end. If stacked dialogs ever become a real workflow rather than an
  accident, revisit.

## 11. Follow-ups to file (proposed, not filed)

- `UserMenu`'s `document`-level Escape and mousedown-outside listeners are the last hand-rolled
  dismissal in the app. Not a modal, so not in this scope; worth one item to give it the same
  treatment or to state deliberately that a popup does not need it.
- Native `<dialog>` reconsideration, gated on jsdom implementing `HTMLDialogElement` or on the e2e
  harness landing. Section 3 has the evidence; an item should carry the trigger condition so it is
  re-evaluated rather than re-litigated.
- A lint rule or a test that fails if `role="dialog"` appears outside `DialogShell.tsx`. The Table
  iteration's lesson was that a rule living only in a comment is a rule the next caller does not read;
  the type-level equivalent here is weaker than it was for `TableRow`, so a sweep test may be the
  right shape.
