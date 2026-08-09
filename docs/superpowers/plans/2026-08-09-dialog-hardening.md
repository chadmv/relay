# Dialog Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract one `DialogShell` plus a module-level `dialogStack` that the app's three hand-rolled dialogs compose, so every dialog gets a focus trap, an `inert`/`aria-hidden` background, a scroll lock, a portal, and an Escape that dismisses exactly one dialog - the topmost.

**Architecture:** Two new modules under `web/src/components/dialog/`. `dialogStack.ts` is a module-level LIFO of open dialog instances plus every global side effect derived from it (the shared portal layer, the body scroll lock, the background marking). `DialogShell.tsx` is the React component the three dialogs wrap: it renders a scrim and a `role="dialog"` panel exactly two elements deep, portals them into the shared layer, registers itself in the stack, and handles Escape and Tab on the panel's own `onKeyDown` - no `document`-level listeners anywhere. The three dialogs keep their own content and their own public props, so the five `ConfirmDialog` call sites are untouched.

**Tech Stack:** React 18.3 (`createPortal`, `useSyncExternalStore`, `useLayoutEffect`), TypeScript 5.7, Tailwind v4, Vitest 2.1 + jsdom 29 + `@testing-library/react` 16 + `@testing-library/user-event` 14, MSW 2. **No new dependency.**

**Spec:** `docs/superpowers/specs/2026-08-09-dialog-hardening.md`. The spec is authoritative on all design calls. Section 8 below lists the four places this plan deliberately deviates or extends, each with its reason.

**Worktree:** every path in this plan is relative to `D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb`. Run `git` from that directory, never from `D:/dev/relay`. Run `npx vitest` from `D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web`.

---

## Slice independence declaration

**FE/BE split: 100% frontend, 0% backend.** Nothing outside `web/src/` changes. Zero Go, zero SQL, zero `.proto`, zero migration, no `make generate`, no `web/dist` (run `git checkout -- web/dist/` before assembling the change set - a frontend build dirties it and it is stale from the scaffold).

**Task independence: SEQUENTIAL. Do not parallelise.** There is no frontend/backend split to fan out, and the four slices below are strictly ordered:

- Slice 1 creates the module that slices 2-4 import.
- Slice 2 migrates `ConfirmDialog` **and** `ResetPasswordDialog` together, because the RED-proven tests T1-T4 mount both at once through the real `UsersTab` path and cannot go green while either is un-migrated (see section 8, deviation 1).
- Slice 3 migrates `TokenRevealDialog` and depends on the shell existing.
- Slice 4 is the final acceptance sweep over the settled tree.

Each of slices 1, 2 and 3 leaves the tree fully green and is independently mergeable, so a halt after any of them is safe.

---

## The byte-identical existing-tests gate

This change is behavior-preserving for the three dialogs' *contracts* and observably new for their modal *behavior*. Following the Table iteration
(`docs/retros/2026-08-09-shared-accessible-table-primitive.md`, Problem #1), the two halves run under two different testing regimes, chosen per file **before** a line is written.

### Regime 1 - the preservation gate (protected files)

For every file in this list, `git diff --numstat` against the branch merge base must show **0 deletions**. Lines may be appended; no existing line may be deleted or modified.

1. `web/src/components/ConfirmDialog.test.tsx`
2. `web/src/admin/users/ResetPasswordDialog.test.tsx`
3. `web/src/workers/WorkerActions.test.tsx`
4. `web/src/workers/WorkspacesPanel.test.tsx` (append-only; the portal assertion lands here)
5. `web/src/jobs/JobActions.test.tsx`
6. `web/src/admin/users/UsersTab.test.tsx` (append-only; T1-T4 land here)
7. `web/src/admin/enrollments/EnrollmentsTab.test.tsx`
8. `web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx` (append-only; the layer-teardown test lands here)
9. `web/src/jobs/JobDetailPage.test.tsx`

**If an assertion in any of these needs adjusting, that IS the finding and it is a STOP - not something to fix in the test.** It means behavior changed and the migration is wrong. Report it, do not edit the file. A softer rule ("adjust the tests if needed") turns a real signal into a quiet test edit; the Table iteration's Problem #6 is the recorded case where a reviewer's blast-radius estimate was wrong by 3x and only the inconvenient constraint caught it.

**Appending is allowed** in files 4, 6 and 8, where this plan adds new tests. Appending means adding lines at the end of the file. Do **not** modify the existing import block; every new test in this plan is written to use only identifiers those files already import (see Task 10 for the one place that costs a `querySelector` instead of a `within`).

### Regime 2 - RED-proven new behavior

New behavior gets a test that was **seen failing first, with the failure output recorded in the task's commit message or the run log**. Claiming RED is not recording RED. Applies to: T1-T4 and T3b (Tasks 6-10), the `dialogStack` and `DialogShell` unit tests (Tasks 1-5), the `ReservationsTab` vacuity demonstration (Task 11), the `WorkspacesPanel` portal assertion (Task 12) and the enrollment layer-teardown test (Task 14).

### The two sanctioned edits outside the protected list

Exactly two test files outside the list above may be modified, and nothing else:

- `web/src/admin/TokenRevealDialog.test.tsx` - one test changes (Task 13), because the spec makes a product decision about Escape there.
- `web/src/admin/reservations/ReservationsTab.test.tsx` - one line and one positive control change (Task 11), because the portal makes an existing assertion vacuous.

**Plan-supplied test bodies and plan-supplied code are untrusted drafts.** Every test body below is a guess written from reading the tree, not from running it. Run each one and read its actual failure before believing it measures what its name claims. Where a test is supposed to be RED, if it is green on the first run, stop and work out why - a green test that was supposed to be red is the highest-value signal in this plan.

---

## File structure

**Create:**

| File | Responsibility |
| --- | --- |
| `web/src/components/dialog/dialogStack.ts` | Module-level LIFO of open dialogs; the shared portal layer; scroll lock; background `inert`/`aria-hidden`. No React. |
| `web/src/components/dialog/dialogStack.test.ts` | T5. Identity-checked removal, idempotent register, tolerant unregister, save-overflow-once, background marking round trip. |
| `web/src/components/dialog/DialogShell.tsx` | The React component: portal, two-element structure, registration lifecycle, focus acquisition/restore, panel-scoped Escape and Tab trap. |
| `web/src/components/dialog/DialogShell.test.tsx` | T6. Structure depth and exact class strings, Escape both ways, Tab/Shift+Tab wrap, `initialFocusRef` precedence. |

No barrel file. Two modules do not need one, and the codebase's only barrel (`components/holo/index.ts`) exists for a seven-consumer primitive set.

**Modify:**

| File | Change |
| --- | --- |
| `web/src/components/ConfirmDialog.tsx:1-70` | Whole file. Delete the `useEffect`, delete the two outer `div`s, wrap the content in `DialogShell`. Public props unchanged. |
| `web/src/admin/users/ResetPasswordDialog.tsx:1,18-36,60-68,112-114` | Delete the Escape `useEffect`; move the `<form>` inside the shell's panel and strip its `role`/`aria-modal`/`aria-labelledby`; update the header comment. |
| `web/src/admin/TokenRevealDialog.tsx:1,34-52,92-101,124-132,182-184` | Delete the Escape `useEffect`; wrap in `DialogShell` with `dismissOnEscape={false}` and `panelClassName="max-w-lg"`; rewrite header invariants 4 and 5. |
| `web/src/admin/users/UsersTab.test.tsx` (append after line 400) | T1, T2, T4, T3, T3b + the `backgroundNodes` helper. |
| `web/src/workers/WorkspacesPanel.test.tsx` (append after line 107) | The portal/containing-block assertion for spec finding (b). |
| `web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx` (append after line 241) | The layer-teardown-with-the-credential test (spec 7.4). |
| `web/src/admin/reservations/ReservationsTab.test.tsx:117,130` | Sanctioned edit: sweep scope and positive control. |
| `web/src/admin/TokenRevealDialog.test.tsx:80-93` | Sanctioned edit: Escape no longer dismisses; the positive control moves to Done. |

**Not touched:** the five `ConfirmDialog` call sites (`workers/WorkerActions.tsx:108`, `workers/WorkspacesPanel.tsx:71`, `jobs/JobActions.tsx:71`, `admin/users/UsersTab.tsx:279`, `admin/reservations/ReservationsTab.tsx:262`) - the public props are unchanged by design. `web/src/shell/UserMenu.tsx` is an explicit non-goal (it is a popup, not a modal).

---

## Facts verified against the tree that the tasks depend on

Read these before starting. Each was checked in this worktree, not assumed.

1. **All three scrims carry the identical class string** `fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4` (`ConfirmDialog.tsx:36`, `ResetPasswordDialog.tsx:61`, `TokenRevealDialog.tsx:126`). The panel strings differ in exactly one token: `max-w-sm` for the first two, `max-w-lg` for the third.
2. **`aria-hidden="true"` on the background makes `getByRole` blind to it.** `@testing-library/dom`'s `isSubtreeInaccessible` (`web/node_modules/@testing-library/dom/dist/role-helpers.js:33-45`) returns true for `aria-hidden="true"`, and `queryAllByRole` filters on it unless `hidden: true` is passed. `inert` appears **nowhere** in `@testing-library/dom`. So: post-fix, a background control is unreachable by `getByRole` while a dialog is open, and reachable with `{ hidden: true }`. This is why T1 captures its background button handle **before** opening the dialog and T2/T3/T3b pass `hidden: true`. `getByText`, `getByLabelText` and `getByTestId` apply no such filter and are unaffected.
3. **No existing protected test queries a background control by role while a dialog is open.** Every `getByRole` issued with a dialog open in `ConfirmDialog.test.tsx`, `ResetPasswordDialog.test.tsx`, `WorkerActions.test.tsx`, `WorkspacesPanel.test.tsx`, `JobActions.test.tsx`, `UsersTab.test.tsx`, `EnrollmentsTab.test.tsx`, `enrollmentTokenSecrecy.test.tsx` and `ReservationsTab.test.tsx` targets an element inside the dialog. This is the analysis that says the gate should hold; it is **not** a substitute for running it.
4. **`user-event` honours `preventDefault()` on the Tab keydown.** `dispatchEvent.js:27-43` runs the tab behavior only `if (!defaultPrevented)`. `getTabDestination.js:8-11` computes destinations from a document-wide `querySelectorAll` and has no `inert` and no `aria-hidden` awareness, so a keydown-intercepting trap is the only trap this suite can measure.
5. **`useLayoutEffect`, not `useEffect`, for the shell's lifecycle.** React 18 runs Layout destroy functions in the mutation phase, **before** the host node is detached; passive (`useEffect`) destroys for a deleted subtree run afterwards, when the focused node is already gone and `document.activeElement` has fallen back to `<body>`. Focus restoration (T4) is unimplementable in a passive cleanup.
6. **React subscribes an external store in a PASSIVE effect.** `useSyncExternalStore`'s subscription is set up after layout effects, and React catches the missed notification via `checkIfSnapshotChanged` on subscribe. So the mount sequence is: render (topmost=`false`) -> layout effect registers -> passive effect subscribes, sees the changed snapshot, forces a re-render -> topmost=`true` -> the focus effect fires. RTL's `render()` wraps this in `act`, which flushes the whole chain synchronously, so `ConfirmDialog.test.tsx:34-41` ("Escape invokes onCancel", which does `render()` then immediately `await userEvent.keyboard('{Escape}')` and needs focus already inside the panel) is the **canary** for this. Run that file first after Task 9.
7. **Tests do not use `StrictMode`** (only `web/src/main.tsx` does), but production does. The layer element is therefore detached-not-nulled when the stack empties, so a StrictMode dev double-invoke cannot leave a portal rendering into a node that never comes back.
8. **`web/src/admin/reservations/ReservationsTab.test.tsx`'s `row()` fixture** has no `starts_at`/`ends_at` and one `worker_ids` entry, so `deriveStatus` returns `ACTIVE` and `confirmDeleteBody` takes the third branch (`ReservationsTab.tsx:45`), whose sentence `Tasks already running on them are unaffected.` occurs exactly once in that source file.
9. **`aria-hidden` occurs in exactly one place in `web/src` today** (`admin/server/HealthPill.tsx:50`, on a `<span>` deep inside a component). No direct child of `document.body` owns an `inert` or `aria-hidden` attribute, so the stack's marking scheme never has an app-owned attribute to preserve.

---

# Slice 1: the shared module

## Task 1: `dialogStack` unit tests (T5)

**Files:**
- Create: `web/src/components/dialog/dialogStack.test.ts`

- [ ] **Step 1: Write the failing test file**

Create `web/src/components/dialog/dialogStack.test.ts`:

```ts
import { afterEach, expect, test } from 'vitest'
import {
  __resetForTest,
  getLayer,
  isEmpty,
  isTopmost,
  registerDialog,
  unregisterDialog,
} from './dialogStack'

// dialogStack is module state shared across the tests in this file. React
// Testing Library's auto-cleanup empties it for component tests, but these drive
// the module directly with no React in the picture, so reset explicitly.
afterEach(() => {
  __resetForTest()
  document.body.style.overflow = ''
})

function panel(): HTMLElement {
  const el = document.createElement('div')
  el.tabIndex = -1
  return el
}

test('unregister removes its OWN id by identity, not the top of the stack', () => {
  registerDialog('a', panel())
  registerDialog('b', panel())
  expect(isTopmost('b')).toBe(true)

  unregisterDialog('a')

  // A stack.pop() teardown would have removed b here, leaving a's dead id
  // topmost: Escape would stop working and the scroll lock would release while
  // b is still on screen. This is the "identity-checked teardown" invariant, in
  // a React effect cleanup.
  expect(isTopmost('b')).toBe(true)
  expect(isTopmost('a')).toBe(false)
  expect(isEmpty()).toBe(false)
})

test('registering the same id twice is idempotent', () => {
  registerDialog('a', panel())
  registerDialog('b', panel())
  // StrictMode's dev double-invoke and hot reload both produce this.
  registerDialog('a', panel())

  expect(isTopmost('b')).toBe(true)
  unregisterDialog('a')
  unregisterDialog('b')
  expect(isEmpty()).toBe(true)
})

test('unregistering an id that is not on the stack is a no-op', () => {
  registerDialog('a', panel())
  unregisterDialog('ghost')
  expect(isTopmost('a')).toBe(true)

  unregisterDialog('a')
  unregisterDialog('a')
  expect(isEmpty()).toBe(true)
})

test('body overflow is saved exactly once across two registrations and restored exactly', () => {
  document.body.style.overflow = 'auto'

  registerDialog('a', panel())
  expect(document.body.style.overflow).toBe('hidden')
  registerDialog('b', panel())
  expect(document.body.style.overflow).toBe('hidden')

  unregisterDialog('b')
  // A dialog is still open, so the lock must hold.
  expect(document.body.style.overflow).toBe('hidden')

  unregisterDialog('a')
  // A per-dialog save/restore pair would have had b save the 'hidden' that a
  // wrote, so this would read 'hidden' forever - a page that can never scroll
  // again.
  expect(document.body.style.overflow).toBe('auto')
})

test('the background is marked and unmarked, and the layer comes and goes', () => {
  const background = document.createElement('div')
  document.body.appendChild(background)

  registerDialog('a', panel())
  expect(background).toHaveAttribute('inert')
  expect(background).toHaveAttribute('aria-hidden', 'true')
  expect(document.querySelector('[data-dialog-layer]')).not.toBeNull()
  // The layer itself must never be marked: it holds the dialog, and aria-hidden
  // on an ancestor of the focused element is an outright AT violation.
  expect(getLayer().hasAttribute('inert')).toBe(false)
  expect(getLayer().hasAttribute('aria-hidden')).toBe(false)

  unregisterDialog('a')
  expect(background).not.toHaveAttribute('inert')
  expect(background).not.toHaveAttribute('aria-hidden')
  expect(document.querySelector('[data-dialog-layer]')).toBeNull()

  background.remove()
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/components/dialog/dialogStack.test.ts
```

Expected: FAIL, all five, with `Failed to resolve import "./dialogStack"`. Record the output.

## Task 2: the deliberately naive `dialogStack`

The spec (7.2, T3) requires the wrong implementation to exist and be **measured** before the right one lands, because the three teardown rules are exactly the kind that look like arbitrary style until you see what breaks without them.

**Files:**
- Create: `web/src/components/dialog/dialogStack.ts`

- [ ] **Step 1: Write the naive implementation**

Create `web/src/components/dialog/dialogStack.ts` with the two known-wrong behaviors marked. Do not commit this state.

```ts
type Entry = { id: string; panel: HTMLElement }

const stack: Entry[] = []
const subscribers = new Set<() => void>()

let layer: HTMLElement | null = null
let previousBodyOverflow: string | null = null

const MARK = 'data-dialog-inert'

export function getLayer(): HTMLElement {
  if (layer === null) {
    layer = document.createElement('div')
    layer.setAttribute('data-dialog-layer', '')
  }
  return layer
}

export function registerDialog(id: string, panel: HTMLElement): void {
  if (stack.some((e) => e.id === id)) return
  stack.push({ id, panel })
  apply()
  notify()
}

export function unregisterDialog(_id: string): void {
  // NAIVE #1 (to be replaced in Task 3): removes the top of the stack rather
  // than this caller's own entry.
  stack.pop()
  apply()
  notify()
}

export function isTopmost(id: string): boolean {
  return stack.length > 0 && stack[stack.length - 1].id === id
}

export function getTopmostPanel(): HTMLElement | null {
  return stack.length > 0 ? stack[stack.length - 1].panel : null
}

export function isEmpty(): boolean {
  return stack.length === 0
}

export function subscribe(fn: () => void): () => void {
  subscribers.add(fn)
  return () => {
    subscribers.delete(fn)
  }
}

function notify(): void {
  for (const fn of Array.from(subscribers)) fn()
}

function unmarkBackground(): void {
  for (const el of Array.from(document.body.querySelectorAll(`[${MARK}]`))) {
    el.removeAttribute('inert')
    el.removeAttribute('aria-hidden')
    el.removeAttribute(MARK)
  }
}

function apply(): void {
  // NAIVE #2 (to be replaced in Task 3): restores the globals on every
  // unregister rather than deriving them from the post-removal stack, and saves
  // the overflow on every registration rather than on the empty -> non-empty
  // transition.
  if (stack.length === 0) {
    unmarkBackground()
    if (previousBodyOverflow !== null) {
      document.body.style.overflow = previousBodyOverflow
      previousBodyOverflow = null
    }
    layer?.remove()
    return
  }

  const l = getLayer()
  if (l.parentNode !== document.body) document.body.appendChild(l)
  previousBodyOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  for (const el of Array.from(document.body.children)) {
    if (el === l) continue
    if (el.hasAttribute(MARK)) continue
    el.setAttribute(MARK, '')
    el.setAttribute('inert', '')
    el.setAttribute('aria-hidden', 'true')
  }
}

export function __resetForTest(): void {
  stack.length = 0
  apply()
  layer = null
}
```

- [ ] **Step 2: Run the unit tests and RECORD which fail**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/components/dialog/dialogStack.test.ts
```

Expected: 3 pass, **2 fail**:
- `unregister removes its OWN id by identity, not the top of the stack` - fails at `expect(isTopmost('b')).toBe(true)` (received `false`), because `pop()` removed `b`.
- `body overflow is saved exactly once across two registrations and restored exactly` - fails at the final `expect(document.body.style.overflow).toBe('auto')` (received `'hidden'`), because registering `b` saved the `'hidden'` that registering `a` had written.

**Paste both failure messages into the run log.** This is the recorded RED for spec rules 4.1.1 and the save-once rule, and it is the evidence Task 10's integration-level version reuses.

## Task 3: the correct `dialogStack`

**Files:**
- Modify: `web/src/components/dialog/dialogStack.ts` (replace `unregisterDialog` and `apply`, add the header comment)

- [ ] **Step 1: Replace the file with the correct implementation**

```ts
// A module-level LIFO of the dialogs that are currently open, plus every global
// side effect derived from it: the shared portal layer, the body scroll lock,
// and the inert/aria-hidden marking of the background.
//
// WHY A MODULE-LEVEL STACK RATHER THAN A CONTEXT. Two instances of the SAME
// component can be mounted at once with no shared parent state: WorkerDetailPage
// renders WorkerActions and WorkspacesPanel, and each owns its own ConfirmDialog
// behind independent state. "Which dialog is topmost" therefore needs per-
// INSTANCE identity, which no per-component convention, prop or provider keyed
// by component type can supply.
//
// WHY NOT native <dialog> + showModal(). jsdom 29's
// living/nodes/HTMLDialogElement-impl.js is, in its entirety,
// `class HTMLDialogElementImpl extends HTMLElementImpl {}` - no showModal, no
// close, no open reflection - so every dialog test would throw TypeError, and the
// only workaround (a hand-rolled polyfill in test setup) means the tests exercise
// the polyfill rather than the platform, leaving the trap that is the whole point
// of the route as the one thing never verified. Revisit when jsdom implements
// HTMLDialogElement, or when the repo gains a real-browser harness
// (docs/backlog/idea-2026-06-03-web-e2e-harness.md).
//
// TEARDOWN ORDER IS LOAD-BEARING. This is "end the generation before releasing
// the resource" - the project invariant - in its frontend form. Three rules,
// each with the failure mode a reviewer should look for:
//
//  1. unregisterDialog removes ITS OWN id by identity. NEVER stack.pop(). With A
//     under B, a pop() teardown for A removes B, leaving A's dead id topmost:
//     Escape stops working and the scroll lock releases while B is on screen.
//  2. Remove from the stack FIRST, then apply(). apply() reads only the
//     post-removal stack, so a dying dialog's cleanup can never describe a world
//     in which it is still open. Never restore-then-remove.
//  3. previousBodyOverflow is written only on the empty -> non-empty transition.
//     A per-dialog save/restore pair has the second dialog save the 'hidden' the
//     first one wrote, and the last close then restores 'hidden' permanently - a
//     page that can never scroll again.
//
// Callers must read isTopmost() at EVENT time, never from a value captured at
// effect setup. A captured value is a stale generation by another name.

type Entry = { id: string; panel: HTMLElement }

const stack: Entry[] = []
const subscribers = new Set<() => void>()

let layer: HTMLElement | null = null
let previousBodyOverflow: string | null = null

// Private marker so teardown only unmarks nodes THIS module marked, never an
// aria-hidden the app owns. Identity-checked teardown, applied to attributes.
const MARK = 'data-dialog-inert'

// The layer is detached when the stack empties but the reference is deliberately
// NOT nulled: React holds this exact node as the portal container for the
// lifetime of a mounted DialogShell, and StrictMode's dev double-invoke of
// effects unregisters and re-registers, which with a fresh element would leave
// the portal rendering into a detached node that never comes back.
export function getLayer(): HTMLElement {
  if (layer === null) {
    layer = document.createElement('div')
    layer.setAttribute('data-dialog-layer', '')
  }
  return layer
}

export function registerDialog(id: string, panel: HTMLElement): void {
  // Idempotent per id: a dialog that re-registers without its cleanup having run
  // (StrictMode, hot reload) must not appear twice.
  if (stack.some((e) => e.id === id)) return
  stack.push({ id, panel })
  apply()
  notify()
}

export function unregisterDialog(id: string): void {
  const i = stack.findIndex((e) => e.id === id)
  // Tolerate an absent id: a cleanup may run twice, or run for an id that was
  // never registered.
  if (i === -1) return
  stack.splice(i, 1) // rule 1
  apply() // rule 2
  notify()
}

export function isTopmost(id: string): boolean {
  return stack.length > 0 && stack[stack.length - 1].id === id
}

export function getTopmostPanel(): HTMLElement | null {
  return stack.length > 0 ? stack[stack.length - 1].panel : null
}

export function isEmpty(): boolean {
  return stack.length === 0
}

export function subscribe(fn: () => void): () => void {
  subscribers.add(fn)
  return () => {
    subscribers.delete(fn)
  }
}

function notify(): void {
  for (const fn of Array.from(subscribers)) fn()
}

// Derives ALL global state from the current stack. It never "restores" anything
// directly; there is exactly one place that decides what the world looks like,
// and it only ever reads the stack.
function apply(): void {
  if (stack.length === 0) {
    for (const el of Array.from(document.body.querySelectorAll(`[${MARK}]`))) {
      el.removeAttribute('inert')
      el.removeAttribute('aria-hidden')
      el.removeAttribute(MARK)
    }
    if (previousBodyOverflow !== null) {
      document.body.style.overflow = previousBodyOverflow
      previousBodyOverflow = null
    }
    layer?.remove()
    return
  }

  const l = getLayer()
  if (l.parentNode !== document.body) document.body.appendChild(l)

  // rule 3: the empty -> non-empty transition only.
  if (previousBodyOverflow === null) {
    previousBodyOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
  }

  // The background is "every direct child of document.body except the layer",
  // defined structurally rather than as #root: in production that marks #root,
  // and under React Testing Library it marks the RTL container div, which has no
  // id="root". Defining it structurally is what makes the behavior identical in
  // both, and what stops the inert assertions from being accidentally green.
  //
  // No node in web/src owns an inert or aria-hidden attribute of its own on a
  // direct child of <body> (verified 2026-08-09), so removing both on teardown
  // cannot clobber app state. MARK is what makes that check enforceable if it
  // ever stops being true.
  for (const el of Array.from(document.body.children)) {
    if (el === l) continue
    if (el.hasAttribute(MARK)) continue
    el.setAttribute(MARK, '')
    el.setAttribute('inert', '')
    el.setAttribute('aria-hidden', 'true')
  }
}

// Belt-and-braces for the two unit-test files that drive this module directly
// without React. Component tests do not need it: RTL's auto-cleanup unmounts and
// therefore runs the shells' cleanups, so the stack self-empties.
export function __resetForTest(): void {
  stack.length = 0
  apply()
  layer = null
}
```

- [ ] **Step 2: Run the unit tests to verify they pass**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/components/dialog/dialogStack.test.ts
```

Expected: PASS, 5 tests.

- [ ] **Step 3: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git add web/src/components/dialog/dialogStack.ts web/src/components/dialog/dialogStack.test.ts
git commit -m "$(cat <<'EOF'
feat(web): add dialogStack, a module-level LIFO of open dialogs

Owns the shared portal layer, the body scroll lock, and the inert/aria-hidden
marking of the background, all derived from the stack rather than restored
directly.

Three teardown rules, each RED-proven against a deliberately naive intermediate
implementation before landing:
- unregisterDialog removes its own id by identity, never stack.pop()
- remove from the stack first, then apply(); apply reads only the post-removal stack
- body overflow is saved on the empty -> non-empty transition only
EOF
)"
```

## Task 4: `DialogShell` unit tests (T6)

**Files:**
- Create: `web/src/components/dialog/DialogShell.test.tsx`

- [ ] **Step 1: Write the failing test file**

Create `web/src/components/dialog/DialogShell.test.tsx`:

```tsx
import { useRef } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'
import { DialogShell } from './DialogShell'
import { __resetForTest } from './dialogStack'

afterEach(() => __resetForTest())

function Harness({
  dismissOnEscape,
  onDismiss,
}: {
  dismissOnEscape?: boolean
  onDismiss: () => void
}) {
  return (
    <DialogShell
      titleId="harness-title"
      onDismiss={onDismiss}
      dismissOnEscape={dismissOnEscape}
      panelClassName="max-w-sm"
    >
      <h2 id="harness-title">Shell harness</h2>
      <button type="button">first</button>
      <button type="button">middle</button>
      <button type="button">last</button>
    </DialogShell>
  )
}

test('renders exactly two elements deep - the panel inside the scrim, nothing between', () => {
  render(<Harness onDismiss={vi.fn()} />)
  const dialog = screen.getByRole('dialog')
  const scrim = dialog.parentElement as HTMLElement

  // The depth is a hard constraint, not an implementation detail:
  // TokenRevealDialog.test.tsx:82 obtains the backdrop as
  // getByRole('dialog').parentElement and clicks it to prove a stray click cannot
  // destroy a credential. An extra wrapper would silently retarget that click and
  // the test would keep passing while proving nothing - a self-vacuuming security
  // assertion.
  expect(scrim.className).toBe(
    'fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4',
  )
  expect(dialog.className).toBe(
    'w-full rounded-card border border-border bg-bg p-5 shadow-xl max-w-sm',
  )
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(dialog).toHaveAttribute('aria-labelledby', 'harness-title')
  expect(dialog).toHaveAccessibleName('Shell harness')
  // tabIndex -1 gives the shell a focus target if a caller ever has no focusable
  // content; it does not enter the tab ring (user-event filters tabindex < 0 at
  // getTabDestination.js:11, and so do browsers).
  expect(dialog.getAttribute('tabindex')).toBe('-1')

  // Portaled to the single shared layer under <body>, not into the RTL container.
  expect(scrim.parentElement).toBe(document.querySelector('[data-dialog-layer]'))
  expect(document.querySelector('[data-dialog-layer]')?.parentElement).toBe(document.body)
})

test('the scrim has no click handler - a backdrop click never dismisses', async () => {
  const onDismiss = vi.fn()
  render(<Harness onDismiss={onDismiss} />)
  const scrim = screen.getByRole('dialog').parentElement as HTMLElement

  await userEvent.click(scrim)

  // None of the three dialogs has an overlay onClick today, and
  // TokenRevealDialog's invariant 4 records that as deliberate. The shell must
  // not add one.
  expect(onDismiss).not.toHaveBeenCalled()
})

test('Escape dismisses by default (the live-instrument control for the flag below)', async () => {
  const onDismiss = vi.fn()
  render(<Harness onDismiss={onDismiss} />)
  expect(screen.getByRole('button', { name: 'first' })).toHaveFocus()

  await userEvent.keyboard('{Escape}')

  expect(onDismiss).toHaveBeenCalledTimes(1)
})

test('dismissOnEscape={false} suppresses Escape while the keydown still reaches the panel', async () => {
  const onDismiss = vi.fn()
  render(<Harness dismissOnEscape={false} onDismiss={onDismiss} />)
  // Focus is inside the panel, so the keydown genuinely reaches the panel's
  // onKeyDown. Without this assertion the suppression could be an artefact of a
  // keystroke that landed on <body> - the test above is the paired positive
  // control on the same instrument.
  expect(screen.getByRole('button', { name: 'first' })).toHaveFocus()

  await userEvent.keyboard('{Escape}')

  expect(onDismiss).not.toHaveBeenCalled()
})

test('Tab from the last focusable wraps to the first, Shift+Tab from the first wraps to the last', async () => {
  render(<Harness onDismiss={vi.fn()} />)
  const first = screen.getByRole('button', { name: 'first' })
  const middle = screen.getByRole('button', { name: 'middle' })
  const last = screen.getByRole('button', { name: 'last' })
  expect(first).toHaveFocus()

  await userEvent.tab({ shift: true })
  expect(last).toHaveFocus()

  await userEvent.tab()
  expect(first).toHaveFocus()

  // The interior steps are NOT intercepted - the default tab behavior is left
  // alone so ordinary navigation inside the panel is the platform's, not ours.
  await userEvent.tab()
  expect(middle).toHaveFocus()
})

test('initialFocusRef wins over the first focusable', () => {
  function WithRef() {
    const ref = useRef<HTMLButtonElement>(null)
    return (
      <DialogShell titleId="ref-title" onDismiss={vi.fn()} initialFocusRef={ref}>
        <h2 id="ref-title">Ref harness</h2>
        <button type="button">first</button>
        <button type="button" ref={ref}>
          chosen
        </button>
      </DialogShell>
    )
  }
  render(<WithRef />)
  expect(screen.getByRole('button', { name: 'chosen' })).toHaveFocus()
})

test('with no panelClassName the panel carries the base string alone', () => {
  function Bare() {
    return (
      <DialogShell titleId="bare-title" onDismiss={vi.fn()}>
        <h2 id="bare-title">Bare</h2>
        <button type="button">only</button>
      </DialogShell>
    )
  }
  render(<Bare />)
  // The width utility must not sit in the base with a caller override: two
  // competing Tailwind utilities on one element resolve by stylesheet order, not
  // class-attribute order, so an override is not reliable.
  expect(screen.getByRole('dialog').className).toBe(
    'w-full rounded-card border border-border bg-bg p-5 shadow-xl',
  )
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/components/dialog/DialogShell.test.tsx
```

Expected: FAIL, all seven, with `Failed to resolve import "./DialogShell"`. Record the output.

## Task 5: `DialogShell`

**Files:**
- Create: `web/src/components/dialog/DialogShell.tsx`

- [ ] **Step 1: Write the component**

```tsx
import {
  useLayoutEffect,
  useId,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
  type RefObject,
} from 'react'
import { createPortal } from 'react-dom'
import {
  getLayer,
  getTopmostPanel,
  isEmpty,
  isTopmost,
  registerDialog,
  subscribe,
  unregisterDialog,
} from './dialogStack'

// The one modal shell. Every dialog in the app composes this; nothing else
// carries role="dialog", aria-modal, or the fixed inset-0 scrim.
//
// WHAT IT OWNS: the scrim, the panel box, and the modal BEHAVIOR - portal, focus
// acquisition and restore, the Tab trap, the scoped Escape, and registration in
// dialogStack (which owns inert, aria-hidden and the scroll lock). It owns no
// title, no body and no buttons; content stays with the caller, because
// ResetPasswordDialog and TokenRevealDialog are siblings of ConfirmDialog and not
// variants of it.
//
// THE RENDERED STRUCTURE IS EXACTLY TWO ELEMENTS DEEP AND THAT IS NON-NEGOTIABLE.
// TokenRevealDialog.test.tsx:82 obtains the backdrop as
// getByRole('dialog').parentElement and clicks it to prove a stray click cannot
// destroy a credential. An extra wrapper would silently retarget that click and
// the test would keep passing while proving nothing.
//
// NO onClick ON THE SCRIM, EVER. None of the three dialogs has one today and
// TokenRevealDialog's invariant 4 records that as deliberate: a stray click must
// never destroy the only copy of a credential.
//
// THE CLASS STRINGS ARE BYTE-IDENTICAL EXTRACTIONS. All three dialogs carried the
// same scrim string, so the whole string is SCRIM here. Their panel strings
// differed in exactly one token, so the width lives in panelClassName rather than
// in PANEL_BASE with a caller override: two competing Tailwind utilities on one
// element resolve by stylesheet order, not class-attribute order.
//
// WHY THE TRAP IS A KEYDOWN INTERCEPT AND NOT inert / a focusin sentinel.
// @testing-library/user-event@14 computes its Tab destination from a
// document-wide querySelectorAll (utils/focus/getTabDestination.js:8-11) and the
// string "inert" appears nowhere in the shipped package, so under this suite
// userEvent.tab() walks straight past an inert background. The one mechanism it
// does honour is preventDefault() on the keydown (event/dispatchEvent.js:27-43).
// So a trap built by intercepting Tab is both correct in a browser and the only
// one this repo can actually prove. inert and aria-hidden still ship, as the
// browser- and AT-facing mechanism and as defence in depth - but note that the
// tests assert them as ATTRIBUTES only. Nothing here proves inert blocks anything.
//
// A focusin sentinel on document was rejected (two naive focus-pulling traps
// mounted at once livelock, for no gain over the keydown path), and so were
// zero-size focusable sentinel divs at the panel edges (they add DOM nodes inside
// a panel whose innerHTML is swept by both the reservations honesty test and the
// enrollment secrecy suite).
//
// KNOWN LIMITATION, ACCEPTED: the focusable selector below does not evaluate
// display/visibility and does not cross shadow roots. No current consumer has
// hidden focusables or a shadow root.

const SCRIM = 'fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4'
const PANEL_BASE = 'w-full rounded-card border border-border bg-bg p-5 shadow-xl'

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([type=hidden]):not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex^="-"])',
].join(', ')

function focusables(panel: HTMLElement): HTMLElement[] {
  return Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE))
}

interface DialogShellProps {
  // The caller owns useId and renders its own <h2 id={titleId}>.
  titleId: string
  onDismiss: () => void
  dismissOnEscape?: boolean
  // Optional. Default: the first focusable in the panel, then the panel itself.
  initialFocusRef?: RefObject<HTMLElement | null>
  // Per-caller sizing only.
  panelClassName?: string
  children: ReactNode
}

export function DialogShell({
  titleId,
  onDismiss,
  dismissOnEscape = true,
  initialFocusRef,
  panelClassName,
  children,
}: DialogShellProps) {
  const id = useId()
  const panelRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLElement | null>(null)
  const wasTopmostRef = useRef(false)
  const onDismissRef = useRef(onDismiss)
  // Captured once: React holds this exact node as the portal container for this
  // instance's lifetime, so it must not change identity across renders.
  const [layer] = useState(getLayer)

  const topmost = useSyncExternalStore(subscribe, () => isTopmost(id))

  // onDismiss is read through a ref by the key handler and is NEVER a dependency
  // of the focus effect below. TokenRevealDialog.test.tsx:129-148 exists because
  // an earlier version keyed a focus effect on a callback identity that changes
  // on every parent re-render, yanking focus off the Done button every 60
  // seconds. That test is now the regression gate on this file.
  useLayoutEffect(() => {
    onDismissRef.current = onDismiss
  })

  // Registration lifecycle. useLayoutEffect, NOT useEffect: React 18 runs Layout
  // destroy functions in the mutation phase, before the host node is detached,
  // while passive destroys for a deleted subtree run afterwards - by which point
  // document.activeElement has already fallen back to <body> and the focus
  // restore below has nothing to check and nothing to restore.
  useLayoutEffect(() => {
    const panel = panelRef.current as HTMLElement
    // Captured BEFORE anything moves focus.
    triggerRef.current = document.activeElement as HTMLElement | null
    registerDialog(id, panel)

    return () => {
      // Guard on where focus actually is BEFORE releasing anything, so a dialog
      // that closes while the user has clicked elsewhere does not yank it back.
      const focusWasInside = panel.contains(document.activeElement)
      // Rule 2 of dialogStack: end the generation (leave the stack) before
      // deciding anything about the world.
      unregisterDialog(id)
      if (!focusWasInside) return
      if (isEmpty()) {
        const trigger = triggerRef.current
        if (trigger && trigger.isConnected) trigger.focus()
        return
      }
      // Another dialog is still open. Park focus on the topmost panel rather
      // than on the trigger (which sits behind the scrim) or on <body> (which
      // would put focus outside every open modal). If this close also promoted a
      // new topmost, that dialog's transition effect refines this to its own
      // initial target a moment later.
      getTopmostPanel()?.focus()
    }
  }, [id])

  // Focus acquisition, keyed on the false -> true transition of topmost and on
  // nothing else. One rule covers both "I just mounted" and "the dialog above me
  // just closed, I am topmost again".
  useLayoutEffect(() => {
    if (topmost && !wasTopmostRef.current) {
      const panel = panelRef.current
      if (panel) {
        const target = initialFocusRef?.current ?? focusables(panel)[0] ?? panel
        target.focus()
      }
    }
    wasTopmostRef.current = topmost
    // initialFocusRef is deliberately omitted: it is a ref object read at
    // transition time. Listing it would re-run this effect whenever a caller
    // produces a fresh ref identity, which is the focus-theft defect
    // TokenRevealDialog.test.tsx:129-148 pins.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [topmost])

  // Keyboard handling lives on the panel, not on document. With focus inside the
  // topmost dialog by construction, a panel-scoped onKeyDown is received by
  // exactly one dialog - which is what makes the Escape scoping mechanical rather
  // than conventional. isTopmost is read here, at EVENT time, never captured.
  function onKeyDown(e: ReactKeyboardEvent<HTMLDivElement>) {
    const panel = panelRef.current
    if (!panel) return

    if (e.key === 'Escape') {
      if (!dismissOnEscape) return
      // Defence in depth for the case where focus somehow sits in a lower dialog.
      if (!isTopmost(id)) return
      onDismissRef.current()
      return
    }

    if (e.key !== 'Tab') return

    if (!isTopmost(id)) {
      // A non-topmost dialog must not be a route out.
      e.preventDefault()
      getTopmostPanel()?.focus()
      return
    }

    const items = focusables(panel)
    if (items.length === 0) {
      e.preventDefault()
      panel.focus()
      return
    }
    const active = document.activeElement
    const first = items[0]
    const last = items[items.length - 1]
    if (e.shiftKey) {
      if (active === first || active === panel || !panel.contains(active)) {
        e.preventDefault()
        last.focus()
      }
      return
    }
    if (active === last || active === panel || !panel.contains(active)) {
      e.preventDefault()
      first.focus()
    }
  }

  return createPortal(
    // No onClick here. See the header.
    <div className={SCRIM}>
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        className={panelClassName ? `${PANEL_BASE} ${panelClassName}` : PANEL_BASE}
        onKeyDown={onKeyDown}
      >
        {children}
      </div>
    </div>,
    layer,
  )
}
```

- [ ] **Step 2: Run the unit tests to verify they pass**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/components/dialog/DialogShell.test.tsx
```

Expected: PASS, 7 tests.

If `Escape dismisses by default` fails because focus is on `<body>` rather than the `first` button, the `useSyncExternalStore` round-trip described in "Facts verified" #6 did not settle inside `render()`'s `act`. That is a STOP: report it rather than working around it, because the same round-trip is what `ConfirmDialog.test.tsx:34-41` depends on.

- [ ] **Step 3: Run the whole web suite to confirm nothing regressed**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npm test
```

Expected: PASS, with a net increase of 12 over the pre-change count (nothing consumes the shell yet).

- [ ] **Step 4: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git add web/src/components/dialog/DialogShell.tsx web/src/components/dialog/DialogShell.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): add DialogShell, the one modal shell

Portals into dialogStack's shared layer, renders exactly two elements deep (scrim
then role=dialog panel), traps Tab by intercepting the keydown, scopes Escape to
the topmost dialog via a panel-level onKeyDown, and restores focus to the trigger
on close. No document-level listeners.

Nothing consumes it yet.
EOF
)"
```

---

# Slice 2: `ConfirmDialog` and `ResetPasswordDialog` compose the shell

The four RED-proven behaviors all run through the real `UsersTab` reset-plus-archive path, so Tasks 6-8 write and RED-record the tests **before** either dialog is migrated, and Task 9 migrates both at once.

## Task 6: T1 - the trap, RED-recorded

**Files:**
- Modify (append only): `web/src/admin/users/UsersTab.test.tsx` (after line 400)

- [ ] **Step 1: Append the test**

Append to the end of `web/src/admin/users/UsersTab.test.tsx`. It uses only identifiers the file already imports (`server`, `screen`, `userEvent`, `expect`, `test`, `listHandler`, `user`, `renderTab`), so the import block is untouched.

```tsx
// ---------------------------------------------------------------------------
// Dialog hardening (docs/superpowers/specs/2026-08-09-dialog-hardening.md).
// This tab is the only page in the app that can mount two dialogs at once
// through a purely ordinary path - onResetPassword sets `resetting` and
// onArchive sets `confirm`, and neither clears the other - so it is where the
// modal guarantees are measured.
// ---------------------------------------------------------------------------

test('focus is trapped in the reset dialog: ten tabs each way never reach a background control', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('ada@studio.dev')
  // Captured BEFORE the dialog opens. Once it is open the background carries
  // aria-hidden="true", and @testing-library/dom's role query filters on exactly
  // that (role-helpers.js:33-45), so getByRole could no longer find this button.
  const archive = screen.getByRole('button', { name: 'Archive ada@studio.dev' })

  await userEvent.click(
    screen.getByRole('button', { name: 'Reset password for ada@studio.dev' }),
  )
  const dialog = await screen.findByRole('dialog')
  expect(dialog.contains(document.activeElement)).toBe(true)

  // Ten is more than the dialog's four focusables, so this proves the ring WRAPS
  // rather than merely not having escaped yet.
  for (let i = 0; i < 10; i++) {
    await userEvent.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
    expect(archive).not.toHaveFocus()
  }
  for (let i = 0; i < 10; i++) {
    await userEvent.tab({ shift: true })
    expect(dialog.contains(document.activeElement)).toBe(true)
    expect(archive).not.toHaveFocus()
  }
})
```

- [ ] **Step 2: Run it and RECORD the failure**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/users/UsersTab.test.tsx -t 'focus is trapped in the reset dialog'
```

Expected: FAIL on the fourth forward tab, at `expect(dialog.contains(document.activeElement)).toBe(true)` receiving `false`. Today the reset dialog's four focusables are last in document order, so `password -> confirm -> Cancel -> Reset password -> <body>`. Paste the failure into the run log. Do not commit yet.

## Task 7: T2 - one Escape closes exactly one. The centerpiece.

**Files:**
- Modify (append only): `web/src/admin/users/UsersTab.test.tsx`

- [ ] **Step 1: Append the test**

```tsx
test('with two dialogs open, one Escape closes exactly one - the topmost', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('ada@studio.dev')

  // Dialog 1, through the item's real path.
  await userEvent.click(
    screen.getByRole('button', { name: 'Reset password for ada@studio.dev' }),
  )
  await screen.findByRole('dialog')

  // Dialog 2, through the BACKGROUND trigger, with a click and not a tab
  // sequence. Once the trap lands the tab route to this button is unreachable BY
  // DESIGN, so a test that reached it by tabbing could only ever be red. The
  // click route is legitimate rather than synthetic: jsdom performs no
  // hit-testing so the scrim occludes nothing here, and on WorkerDetailPage the
  // scrim genuinely fails to cover the page (its ConfirmDialog sits inside a
  // backdrop-filter ancestor, which becomes the containing block for its
  // position:fixed descendants). It also models every non-tab route to a second
  // dialog: a click landing before paint, an AT activation, a dialog opened by an
  // async result.
  //
  // hidden: true because the open dialog marks the background aria-hidden, which
  // getByRole's accessibility filter honours. The flag makes this query behave
  // identically before and after the fix, so this test measures Escape scoping
  // and nothing else.
  await userEvent.click(
    screen.getByRole('button', { name: 'Archive ada@studio.dev', hidden: true }),
  )
  expect(screen.getAllByRole('dialog')).toHaveLength(2)

  await userEvent.keyboard('{Escape}')

  const survivors = screen.getAllByRole('dialog')
  expect(survivors).toHaveLength(1)
  // By accessible name, NEVER by array index: the DOM order of the two dialogs is
  // ConfirmDialog then ResetPasswordDialog (the JSX order at UsersTab.tsx:278 and
  // :297) regardless of which opened first, so an index assertion tests the wrong
  // property and would pass for the wrong reason.
  expect(survivors[0]).toHaveAccessibleName('Reset password for ada@studio.dev?')
  expect(screen.getByLabelText('New password')).toBeInTheDocument()
})
```

Note deliberately absent: this test does not touch Tab. T1 owns the trap, T2 owns Escape scoping. If T2 could only be constructed through the trap, a trap regression would take the Escape guarantee down with it and neither would be independently measurable.

- [ ] **Step 2: Run it and RECORD the failure**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/users/UsersTab.test.tsx -t 'with two dialogs open'
```

Expected: FAIL after the Escape, at `screen.getAllByRole('dialog')` throwing `Unable to find role="dialog"` - both `document`-level keydown listeners fire, so one Escape closes both dialogs and there are zero matches. Paste the failure into the run log.

## Task 8: T4 - focus restoration, RED-recorded

**Files:**
- Modify (append only): `web/src/admin/users/UsersTab.test.tsx`

- [ ] **Step 1: Append the test**

```tsx
test('closing a dialog returns focus to the control that opened it', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('ada@studio.dev')

  const archive = screen.getByRole('button', { name: 'Archive ada@studio.dev' })
  await userEvent.click(archive)
  await screen.findByRole('dialog')
  // The dialog took focus, so the assertion below is about restoration and not
  // about focus that simply never moved.
  expect(archive).not.toHaveFocus()

  await userEvent.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  expect(archive).toHaveFocus()
})
```

- [ ] **Step 2: Run it and RECORD the failure**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/users/UsersTab.test.tsx -t 'closing a dialog returns focus'
```

Expected: FAIL at `expect(archive).toHaveFocus()`. Today focus is left on the removed dialog's Cancel button, so `document.activeElement` falls back to `<body>`. Paste the failure into the run log.

## Task 9: migrate `ConfirmDialog` and `ResetPasswordDialog`

**Files:**
- Modify: `web/src/components/ConfirmDialog.tsx` (whole file)
- Modify: `web/src/admin/users/ResetPasswordDialog.tsx` (imports, header comment, the effect, the outer markup)

- [ ] **Step 1: Rewrite `web/src/components/ConfirmDialog.tsx`**

```tsx
import { useId, useRef } from 'react'
import { DialogShell } from './dialog/DialogShell'

interface ConfirmDialogProps {
  title: string
  body: string
  confirmLabel: string
  destructive?: boolean
  onConfirm: () => void
  onCancel: () => void
}

// Minimal shared confirm primitive, used at five call sites. The modal behavior -
// portal, focus trap, inert background, scroll lock, scoped Escape, focus restore
// - lives in DialogShell; this file owns only the copy and the two buttons. The
// public props are unchanged, so no call site moved.
export function ConfirmDialog({
  title,
  body,
  confirmLabel,
  destructive,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const titleId = useId()
  const cancelRef = useRef<HTMLButtonElement>(null)

  return (
    <DialogShell
      titleId={titleId}
      onDismiss={onCancel}
      initialFocusRef={cancelRef}
      panelClassName="max-w-sm"
    >
      <h2 id={titleId} className="text-[15px] font-medium text-fg">
        {title}
      </h2>
      <p className="mt-2 text-[13px] text-fg-mute">{body}</p>
      <div className="mt-5 flex justify-end gap-2">
        <button
          type="button"
          ref={cancelRef}
          onClick={onCancel}
          className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg-mute"
        >
          Cancel
        </button>
        <button
          type="button"
          onClick={onConfirm}
          className={
            'rounded-md px-3 py-1.5 text-[12px] font-medium ' +
            (destructive ? 'bg-err/20 text-err border border-err/50' : 'bg-accent text-bg')
          }
        >
          {confirmLabel}
        </button>
      </div>
    </DialogShell>
  )
}
```

- [ ] **Step 2: Rewrite the outer shape of `web/src/admin/users/ResetPasswordDialog.tsx`**

Four edits. Everything between `submit()` and the return - the validation logic, the `Field`/`Input` markup, the error node, the buttons - is unchanged.

1. Line 1: `import { useEffect, useId, useState, type FormEvent } from 'react'` becomes `import { useId, useState, type FormEvent } from 'react'`.
2. Add after the existing imports: `import { DialogShell } from '../../components/dialog/DialogShell'`.
3. Replace the header comment block (lines 18-23) with:

```tsx
// A sibling of ConfirmDialog, not a variant of it: ConfirmDialog takes a
// text-only `body` and cannot host form fields. Both compose the same
// DialogShell, which owns role="dialog", aria-modal, the portal, the focus trap,
// the inert background, the scroll lock and the scoped Escape. The <form> lives
// INSIDE the shell's panel and carries no dialog semantics of its own; a
// type="submit" button still submits its nearest form regardless. First field
// focused via autoFocus, which is also what the shell's firstFocusable picks, so
// the shared Input primitive still does not need to forward a ref.
```

4. Delete the entire `useEffect` at lines 30-36, and replace the outer scrim `div` and the `<form>`'s dialog attributes (lines 60-68 and the closing tags at 112-114) so the return reads:

```tsx
  return (
    <DialogShell titleId={titleId} onDismiss={onCancel} panelClassName="max-w-sm">
      <form onSubmit={submit}>
        <h2 id={titleId} className="text-[15px] font-medium text-fg">
          Reset password for {email}?
        </h2>
        {/* ... everything from the existing <p className="mb-4 mt-2 ..."> through
            the closing </div> of the button row is unchanged ... */}
      </form>
    </DialogShell>
  )
```

The `<form>` loses `role="dialog"`, `aria-modal="true"`, `aria-labelledby={titleId}` and its whole `className` - the shell's panel carries all four. Do **not** put a `className` back on the form.

- [ ] **Step 3: Run the canary first**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/components/ConfirmDialog.test.tsx
```

Expected: PASS, 5 tests. This file is the canary described in "Facts verified" #6: its `Escape invokes onCancel` test calls `render()` and then immediately sends Escape, so it only passes if the shell's initial focus lands inside the panel before `render()` returns. A failure here is a STOP, not a test edit.

- [ ] **Step 4: Run the four RED tests**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/users/UsersTab.test.tsx
```

Expected: PASS, all tests in the file, including the three appended in Tasks 6-8.

- [ ] **Step 5: Run every protected consumer test file**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run \
  src/components/ConfirmDialog.test.tsx \
  src/admin/users/ResetPasswordDialog.test.tsx \
  src/workers/WorkerActions.test.tsx \
  src/workers/WorkspacesPanel.test.tsx \
  src/jobs/JobActions.test.tsx \
  src/jobs/JobDetailPage.test.tsx \
  src/admin/enrollments/EnrollmentsTab.test.tsx \
  src/admin/enrollments/enrollmentTokenSecrecy.test.tsx \
  src/admin/reservations/ReservationsTab.test.tsx
```

Expected: PASS everywhere except `ReservationsTab.test.tsx`, which may still pass - see Task 11, where its vacuity is demonstrated rather than assumed.

- [ ] **Step 6: Run the byte-identical gate**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
BASE=$(git merge-base HEAD origin/main)
git diff --numstat "$BASE" -- \
  web/src/components/ConfirmDialog.test.tsx \
  web/src/admin/users/ResetPasswordDialog.test.tsx \
  web/src/workers/WorkerActions.test.tsx \
  web/src/workers/WorkspacesPanel.test.tsx \
  web/src/jobs/JobActions.test.tsx \
  web/src/admin/users/UsersTab.test.tsx \
  web/src/admin/enrollments/EnrollmentsTab.test.tsx \
  web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx \
  web/src/jobs/JobDetailPage.test.tsx
```

Expected: the second column (deletions) is `0` on **every** row. `UsersTab.test.tsx` shows additions only. Any non-zero deletion count is a STOP and a finding: report which file and which assertion, and do not edit the file.

- [ ] **Step 7: Run the full suite**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npm test
```

Expected: PASS, net +3 over the Slice 1 count.

- [ ] **Step 8: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git add web/src/components/ConfirmDialog.tsx web/src/admin/users/ResetPasswordDialog.tsx web/src/admin/users/UsersTab.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): ConfirmDialog and ResetPasswordDialog compose DialogShell

Both lose their document-level keydown listener and their hand-rolled scrim. The
five ConfirmDialog call sites are untouched - the public props did not change.

Three behaviors appended to UsersTab.test.tsx, each proven RED first against the
unmigrated components:
- Tab and Shift+Tab cannot leave the open dialog (was: escaped on the 4th tab)
- with two dialogs open, one Escape closes exactly one (was: closed both)
- closing a dialog returns focus to its trigger (was: fell back to <body>)

Zero-deletion gate held on all nine protected test files.
EOF
)"
```

## Task 10: T3 and T3b - teardown ordering, with the deliberate-reversion RED protocol

T3 is not RED against the pre-change code in any meaningful sense, because neither `inert` nor the scroll lock existed. It is RED against the **plausible wrong implementation**, so it is measured that way: land the assertions, then reintroduce each wrong behavior one at a time and record the failure it produces. A green T3 written after the correct implementation proves nothing.

**Files:**
- Modify (append only): `web/src/admin/users/UsersTab.test.tsx`

- [ ] **Step 1: Append the helper and the two tests**

```tsx
// Every direct child of <body> that is not the dialog layer. Defined structurally
// rather than as #root: in production the app is mounted in #root, but under
// React Testing Library it lives in an unnamed container div, so an assertion
// keyed on #root would be vacuously green here while proving nothing.
function backgroundNodes(): HTMLElement[] {
  return Array.from(document.body.children).filter(
    (el) => !el.hasAttribute('data-dialog-layer'),
  ) as HTMLElement[]
}

test('the background stays inert and the page stays locked until the LAST dialog closes', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('ada@studio.dev')

  await userEvent.click(
    screen.getByRole('button', { name: 'Reset password for ada@studio.dev' }),
  )
  await screen.findByRole('dialog')
  await userEvent.click(
    screen.getByRole('button', { name: 'Archive ada@studio.dev', hidden: true }),
  )
  expect(screen.getAllByRole('dialog')).toHaveLength(2)

  // Control on the instrument itself: the loops below assert over a list, and an
  // empty list satisfies every one of them.
  expect(backgroundNodes().length).toBeGreaterThan(0)
  for (const el of backgroundNodes()) {
    expect(el).toHaveAttribute('inert')
    expect(el).toHaveAttribute('aria-hidden', 'true')
  }
  expect(document.body.style.overflow).toBe('hidden')

  await userEvent.keyboard('{Escape}')
  expect(screen.getAllByRole('dialog')).toHaveLength(1)

  // One dialog is still open, so NOTHING global may be released yet. A cleanup
  // that restores unconditionally instead of deriving from the post-removal stack
  // unlocks the page and un-inerts the background while a modal is on screen.
  expect(backgroundNodes().length).toBeGreaterThan(0)
  for (const el of backgroundNodes()) {
    expect(el).toHaveAttribute('inert')
    expect(el).toHaveAttribute('aria-hidden', 'true')
  }
  expect(document.body.style.overflow).toBe('hidden')

  await userEvent.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  for (const el of backgroundNodes()) {
    expect(el).not.toHaveAttribute('inert')
    expect(el).not.toHaveAttribute('aria-hidden')
  }
  expect(document.body.style.overflow).toBe('')
  // The layer leaves <body> too, so the child list returns to exactly what it was
  // and nothing leaks between tests.
  expect(document.querySelector('[data-dialog-layer]')).toBeNull()
})

test('closing the LOWER dialog leaves the upper one open, focused and still dismissible', async () => {
  const seen: URLSearchParams[] = []
  server.use(listHandler(seen, () => ({ items: [user()], next_cursor: '', total: 1 })))
  renderTab()
  await screen.findByText('ada@studio.dev')

  await userEvent.click(
    screen.getByRole('button', { name: 'Reset password for ada@studio.dev' }),
  )
  const reset = await screen.findByRole('dialog')
  await userEvent.click(
    screen.getByRole('button', { name: 'Archive ada@studio.dev', hidden: true }),
  )
  expect(screen.getAllByRole('dialog')).toHaveLength(2)

  // Dismiss the one UNDERNEATH. This is the exact shape a stack.pop() teardown
  // gets wrong: it would remove the ARCHIVE dialog's entry instead, leaving the
  // reset dialog's dead id topmost, after which Escape reaches nothing.
  // Scoped with querySelector rather than `within` so this file's import block
  // stays byte-identical.
  const cancelInReset = Array.from(reset.querySelectorAll('button')).find(
    (b) => b.textContent === 'Cancel',
  ) as HTMLButtonElement
  expect(cancelInReset).toBeInTheDocument()
  await userEvent.click(cancelInReset)

  const survivors = screen.getAllByRole('dialog')
  expect(survivors).toHaveLength(1)
  expect(survivors[0]).toHaveAccessibleName('Archive ada@studio.dev?')
  // Focus did not escape to <body> behind a still-open modal.
  expect(survivors[0].contains(document.activeElement)).toBe(true)

  // And the survivor is genuinely still topmost, so Escape still reaches it.
  await userEvent.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})
```

- [ ] **Step 2: Run them and confirm green**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/users/UsersTab.test.tsx -t 'the background stays inert'
npx vitest run src/admin/users/UsersTab.test.tsx -t 'closing the LOWER dialog'
```

Expected: PASS, both.

- [ ] **Step 3: RED proof A - reintroduce the unconditional restore**

In `web/src/components/dialog/dialogStack.ts`, temporarily change the first line of `apply()` from `if (stack.length === 0) {` to `if (true) {`. Then:

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/users/UsersTab.test.tsx -t 'the background stays inert'
```

Expected: FAIL at the middle block - after the first Escape, `expect(el).toHaveAttribute('inert')` finds no `inert`, and `document.body.style.overflow` is `''` while a dialog is still on screen. Record the output, then revert the one-character edit.

- [ ] **Step 4: RED proof B - reintroduce the `stack.pop()` teardown**

In `web/src/components/dialog/dialogStack.ts`, temporarily replace the body of `unregisterDialog` with `stack.pop(); apply(); notify()`. Then:

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/users/UsersTab.test.tsx -t 'closing the LOWER dialog'
npx vitest run src/components/dialog/dialogStack.test.ts -t 'by identity'
```

Expected: both FAIL. The component test fails at the final `waitFor` - the archive dialog is never dismissed by Escape, because `pop()` removed its entry and it is no longer topmost. Record both, then revert.

- [ ] **Step 5: Re-run both green and commit**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/components/dialog src/admin/users/UsersTab.test.tsx
```

Expected: PASS.

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git diff --numstat $(git merge-base HEAD origin/main) -- web/src/components/dialog/dialogStack.ts
git add web/src/admin/users/UsersTab.test.tsx
git commit -m "$(cat <<'EOF'
test(web): pin dialogStack teardown ordering through the real UsersTab path

Two appended tests, each RED-proven against a deliberately reverted dialogStack
rather than against the pre-change tree, since neither inert nor the scroll lock
existed before:
- the background stays inert and the page stays locked until the LAST dialog
  closes (red under an unconditional restore)
- closing the LOWER of two dialogs leaves the upper one topmost, focused and
  still dismissible (red under a stack.pop() teardown)

Both reversions were run, recorded and undone; dialogStack.ts is unchanged.
EOF
)"
```

Confirm the `git diff --numstat` on `dialogStack.ts` printed nothing before committing - if it printed a row, a reversion was not undone.

## Task 11: the one sanctioned edit to `ReservationsTab.test.tsx`

`ReservationsTab.test.tsx:110-131` opens the delete confirm and sweeps `container.innerHTML` for affinity claims the product must never make. Portaling the dialog out of `container` makes every negative assertion vacuous - and the paired positive control `/general dispatch pool/i` would **still pass**, because the same phrase appears in the tab's own footnote at `ReservationsTab.tsx:253`. So the control cannot detect the scope error it exists to guard.

Demonstrate the vacuity; do not assert it.

**Files:**
- Modify: `web/src/admin/reservations/ReservationsTab.test.tsx:117,130` and the comment above line 117

- [ ] **Step 1: Demonstrate the vacuity and RECORD it**

In `web/src/admin/reservations/ReservationsTab.tsx:264`, temporarily change `body={confirmDeleteBody(confirm, now)}` to `body=""`. Then:

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/reservations/ReservationsTab.test.tsx -t 'the confirm dialog also carries no affinity claim when open'
```

Expected: **PASS**. The dialog has no copy at all and the test is still green. That is the RED proof that the assertion had gone vacuous, and it is the evidence that authorises the edit. Record the output. **Leave the `body=""` edit in place for Step 3.**

- [ ] **Step 2: Make the sanctioned edit**

In `web/src/admin/reservations/ReservationsTab.test.tsx`, replace line 117 and line 130 (and only those, plus the comment inserted above 117):

Line 117, `const html = container.innerHTML`, becomes:

```tsx
  // document.body, not container: the dialog is portaled to a layer under <body>
  // (web/src/components/dialog/dialogStack.ts), so a container-scoped sweep no
  // longer sees it and every negative assertion below would be vacuous. The
  // test's stated intent has always been "the confirm dialog carries no affinity
  // claim"; `container` was only ever a proxy for "what the user sees", and the
  // assertion's scope was narrower than its intent. Line 84's sweep, in the test
  // where no dialog is open, is unaffected and deliberately untouched.
  const html = document.body.innerHTML
```

Line 130, `expect(html).toMatch(/general dispatch pool/i)`, becomes:

```tsx
  // Positive control on the same instrument, on a phrase that exists ONLY in the
  // dialog body (ReservationsTab.tsx:45). The previous control was
  // /general dispatch pool/i, which also matches the tab's own footnote at
  // ReservationsTab.tsx:253 - so it stayed green under exactly the scope error it
  // existed to catch.
  expect(html).toMatch(/tasks already running on them are unaffected/i)
```

- [ ] **Step 3: Prove the strengthened control now catches the vacuity**

With `body=""` still in place from Step 1:

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/reservations/ReservationsTab.test.tsx -t 'the confirm dialog also carries no affinity claim when open'
```

Expected: **FAIL** at the new positive control. Record it. This is what makes the edit a fix rather than a rescope. Now revert `ReservationsTab.tsx:264` back to `body={confirmDeleteBody(confirm, now)}`.

- [ ] **Step 4: Confirm the phrase is unique in the source**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
rg -c "Tasks already running on them are unaffected" web/src/admin/reservations/ReservationsTab.tsx
```

Expected: `1`.

- [ ] **Step 5: Run the whole file and check the diff shape**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/reservations/ReservationsTab.test.tsx
```

Expected: PASS.

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git diff --numstat $(git merge-base HEAD origin/main) -- web/src/admin/reservations/ReservationsTab.tsx
git diff $(git merge-base HEAD origin/main) -- web/src/admin/reservations/ReservationsTab.test.tsx
```

Expected: `ReservationsTab.tsx` prints nothing (the source reversion is complete). The test diff shows exactly two changed lines plus the two added comments, all inside the test at lines 110-131.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git add web/src/admin/reservations/ReservationsTab.test.tsx
git commit -m "$(cat <<'EOF'
test(web): widen the reservations confirm-dialog sweep and fix its control

The dialog is now portaled out of the RTL container, so container.innerHTML no
longer contains it and all six negative assertions had gone vacuous. Demonstrated
by deleting the dialog's body copy entirely and watching the test still pass, then
by watching the strengthened control catch it.

The paired positive control moves from /general dispatch pool/i - which also
matches the tab's own footnote, so it could never detect this scope error - to a
phrase that occurs exactly once, in the dialog body.
EOF
)"
```

## Task 12: pin the `WorkspacesPanel` containing-block fix

`WorkspacesPanel` is rendered inside `<Panel title="Source workspaces">` (`WorkerDetailPage.tsx:126-128`), and `Panel` composes `GlassPanel`, whose base class string carries `backdrop-blur-[8px]` (`components/holo/GlassPanel.tsx:8-10`). An element with a `backdrop-filter` other than `none` becomes the containing block for its `position: fixed` descendants, so before this change that `ConfirmDialog`'s `fixed inset-0` scrim was clipped to the panel box rather than covering the viewport - a live visual defect. The portal fixes it as a side effect. Assert it, so the fix cannot be silently undone.

**Files:**
- Modify (append only): `web/src/workers/WorkspacesPanel.test.tsx` (after line 107)

- [ ] **Step 1: Append the test**

Uses only identifiers the file already imports (`screen`, `userEvent`, `http`, `HttpResponse`, `expect`, `test`, `server`, `renderWithQuery`).

```tsx
test('the evict confirm scrim is portaled to <body>, not trapped inside the glass panel', async () => {
  server.use(
    http.get('/v1/workers/w1/workspaces', () =>
      HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-a4f2', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
      ]),
    ),
  )
  const { container } = renderWithQuery(<WorkspacesPanel workerId="w1" />)
  await screen.findByText('ws-a4f2')
  await userEvent.click(screen.getByRole('button', { name: /evict/i }))

  const scrim = screen.getByRole('dialog').parentElement as HTMLElement
  // This panel renders inside <Panel title="Source workspaces">, which composes
  // GlassPanel and carries backdrop-blur-[8px]. An element with a backdrop-filter
  // other than `none` becomes the containing block for its position:fixed
  // descendants, so before the portal this `fixed inset-0` scrim was clipped to
  // the panel box instead of covering the viewport - and the mouse path to a
  // second dialog on WorkerDetailPage was wide open. The portal removes every
  // ancestor stacking context by construction, for this caller and every future
  // one.
  expect(container.contains(scrim)).toBe(false)
  expect(scrim.parentElement).toBe(document.querySelector('[data-dialog-layer]'))
  expect(document.querySelector('[data-dialog-layer]')?.parentElement).toBe(document.body)
})
```

- [ ] **Step 2: Run the file and the gate**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/workers/WorkspacesPanel.test.tsx
```

Expected: PASS, 7 tests.

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git diff --numstat $(git merge-base HEAD origin/main) -- web/src/workers/WorkspacesPanel.test.tsx
```

Expected: deletions column is `0`.

- [ ] **Step 3: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git add web/src/workers/WorkspacesPanel.test.tsx
git commit -m "$(cat <<'EOF'
test(web): pin that the workspaces evict scrim escapes GlassPanel's backdrop-filter

Its ConfirmDialog sits inside a backdrop-blur ancestor, which becomes the
containing block for position:fixed descendants, so the fixed inset-0 scrim was
clipped to the panel box and never covered the viewport. The portal fixes it; this
assertion stops it being silently undone.
EOF
)"
```

---

# Slice 3: `TokenRevealDialog`

## Task 13: `TokenRevealDialog` composes the shell with `dismissOnEscape={false}`

**Decision being implemented (spec section 5): Escape does not dismiss `TokenRevealDialog`. The "Done - I have copied it" button becomes the only dismissal.** The component's own invariant 4 already refuses backdrop-click dismissal because "a stray click must never destroy the only copy of the credential"; Escape is the same class of input - single, low-intent, no target, frequently reflexive - and only an appeal to consistency with the other two dialogs separated them. This change removes that appeal, by making consistency a property of the shell rather than of the dismissal policy. Escape here is not "cancel", it is "done": `onDone` is what calls `create.reset()`, so there is no state to revert to. The a11y objection is answered rather than ignored - the user is not trapped, because Done is inside the trap and reachable by one Tab.

**Files:**
- Modify: `web/src/admin/TokenRevealDialog.tsx`
- Modify: `web/src/admin/TokenRevealDialog.test.tsx:80-93` (sanctioned edit #1)

- [ ] **Step 1: Edit `web/src/admin/TokenRevealDialog.tsx`**

Four edits:

1. Line 1: `import { useEffect, useId, useRef, useState } from 'react'` is unchanged - `useEffect` is still used by the mount focus/select effect and the "Copied" timer.
2. Add after the existing imports: `import { DialogShell } from '../components/dialog/DialogShell'`.
3. Replace header invariants 4 and 5 (lines 42-52) with:

```tsx
//  4. NEITHER a backdrop click NOR Escape dismisses. There is deliberately no
//     onClick on the overlay (the hi-fi's AdminTokenModal has one at
//     design_handoff_relay_holo/hifi3-holo-pages.jsx:2345, which is fine for a
//     form and catastrophic for a secret), and DialogShell is passed
//     dismissOnEscape={false}. Escape is the same class of input as a stray
//     click - single, low-intent, no target, frequently reflexive - and here
//     dismissal IS the destructive act: onDone is what calls create.reset(), so
//     there is nothing to revert to and no cancel affordance to preserve. WAI-
//     ARIA APG requires Escape so a keyboard user is never trapped; they are not
//     trapped, because the Done button is inside the focus trap and one Tab away,
//     and it is the only exit BY DESIGN. This is the documented irreversible-
//     dismissal exception, not an oversight. A confirm-before-discarding dialog
//     was considered and rejected: it stacks a second modal on the credential
//     modal to guard against a keystroke the user can simply not press, and ends
//     in a dialog asking whether you meant to close the dialog that says do not
//     close me.
//  5. a11y and modal behavior come from web/src/components/dialog/DialogShell.tsx,
//     which every dialog in the app composes: the labelled modal role, the portal,
//     the focus trap, the inert + aria-hidden background, the scroll lock, and the
//     scoped Escape. The credential can no longer be tabbed past.
```

4. Delete the Escape `useEffect` at lines 92-101 (comment included), and replace the outer scrim `div` at lines 124-132 and its closing tags at 182-184 so the return reads:

```tsx
  return (
    <DialogShell
      titleId={titleId}
      onDismiss={onDone}
      dismissOnEscape={false}
      panelClassName="max-w-lg"
    >
      <div className="font-mono text-[10px] tracking-[0.18em] text-fg-mute">{endpoint}</div>
      {/* ... everything from the existing <h2 id={titleId}> through the closing
          </div> of the Done button row is unchanged ... */}
    </DialogShell>
  )
```

The mount-only focus+select effect at lines 85-89 **stays**: the shell would focus the same input, but the `select()` is this caller's own requirement and `TokenRevealDialog.test.tsx:76-77` asserts `selectionStart`/`selectionEnd`.

- [ ] **Step 2: Run the test file and record what breaks**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/TokenRevealDialog.test.tsx
```

Expected: exactly one failure, `a backdrop click does NOT dismiss it, but Escape does (paired positive control)`, at `expect(props.onDone).toHaveBeenCalledTimes(1)` receiving 0. Record it. **If any other test in this file fails, that is a STOP and a finding** - this file's exemption covers one test, not the file.

- [ ] **Step 3: Make the sanctioned edit**

Replace `web/src/admin/TokenRevealDialog.test.tsx:80-93` in its entirety with:

```tsx
test('neither a backdrop click nor Escape dismisses it - Done is the only exit (paired positive control)', async () => {
  const { props } = renderDialog()

  // Escape FIRST, while the mount focus is still on the token input, so the
  // keydown genuinely reaches the dialog panel. Done the other way round it would
  // land on <body> after the backdrop click blurs the input, and this assertion
  // would pass for the wrong reason. The live-instrument control - that a
  // DialogShell with the default dismissOnEscape DOES close on Escape - lives in
  // web/src/components/dialog/DialogShell.test.tsx.
  expect(screen.getByLabelText('Token')).toHaveFocus()
  await userEvent.keyboard('{Escape}')
  expect(props.onDone).not.toHaveBeenCalled()

  const backdrop = screen.getByRole('dialog').parentElement as HTMLElement
  await userEvent.click(backdrop)
  // A stray click must never destroy the only copy of the credential, and neither
  // must a reflexive Escape: dismissal here IS the destructive act.
  expect(props.onDone).not.toHaveBeenCalled()
  expect(screen.getByLabelText('Token')).toHaveValue(TOKEN)

  // Positive control on the same instrument: something CAN close it, so the two
  // negatives above are about the backdrop and Escape and not about a dialog that
  // is impossible to dismiss.
  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  expect(props.onDone).toHaveBeenCalledTimes(1)
})
```

- [ ] **Step 4: Run the file to verify it passes**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/TokenRevealDialog.test.tsx
```

Expected: PASS, 12 tests. In particular `a re-render with a fresh onDone identity does not steal focus back to the token input` must still pass unmodified - it is now the regression gate on `DialogShell`'s focus effect dependency array.

- [ ] **Step 5: Check the diff shape**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git diff $(git merge-base HEAD origin/main) -- web/src/admin/TokenRevealDialog.test.tsx
```

Expected: exactly one test body replaced. No other hunk.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git add web/src/admin/TokenRevealDialog.tsx web/src/admin/TokenRevealDialog.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): TokenRevealDialog composes DialogShell; Escape no longer dismisses it

dismissOnEscape={false}. The component's own invariant 4 already refused backdrop
dismissal because a stray click must never destroy the only copy of a credential;
Escape is the same class of input, and here dismissal IS the destructive act -
onDone is what calls create.reset(), so there is nothing to revert to. Done is
inside the focus trap and one Tab away, so no keyboard user is trapped.

One test changes, the file's single sanctioned edit: its paired positive control
moves from Escape to the Done button and it gains an assertion that Escape does
not call onDone, taken with focus still inside the panel so the negative is not
vacuous.
EOF
)"
```

## Task 14: security re-verification and the layer-teardown test

`domContainsSecret` scans `document.body.innerHTML` plus every `input`/`textarea` value document-wide (`web/src/test/secretLeaks.ts:71-77`), not a container, so the portal is safe in theory. The requirement here is that it is **checked**: if that positive control ever fails silently, all six "the token is gone" assertions in that file go vacuous at once.

**Files:**
- Modify (append only): `web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx` (after line 241)

- [ ] **Step 1: Re-run the secrecy suite and confirm the positive control individually**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/enrollments/enrollmentTokenSecrecy.test.tsx --reporter=verbose
```

Expected: PASS, 10 tests. Confirm specifically that `the token is revealed once, then leaves the DOM, the caches, storage, URLs, and the console` passed - `:182`'s `expect(domContainsSecret(TOKEN)).toBe(true)`, taken while the dialog is open, is the control that makes the five negatives afterwards meaningful. Record the verbose output for that test.

- [ ] **Step 2: Append the layer-teardown test**

Uses only identifiers the file already imports.

```tsx
test('the dialog layer leaves the DOM with the credential, retaining no detached subtree', async () => {
  const spies = spyOnConsole()
  server.use(
    http.get('/v1/agent-enrollments', () =>
      HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 }),
    ),
    http.post('/v1/agent-enrollments', () =>
      HttpResponse.json({ id: 'e9', token: TOKEN, expires_at: ROW.expires_at }, { status: 201 }),
    ),
  )
  renderTab(newClient())
  await screen.findByText('farm-west-13')
  await userEvent.click(screen.getByRole('button', { name: '+ Enroll agent' }))
  await userEvent.click(screen.getByRole('button', { name: 'Enroll' }))
  await screen.findByRole('dialog')

  // The dialog is portaled into a single shared layer under <body>
  // (web/src/components/dialog/dialogStack.ts). Hold a reference to it so the
  // DETACHED node can be inspected after teardown - a container that is removed
  // from the document but still holds the credential in a subtree is exactly the
  // leak a portal could introduce and document.body-scoped sweeps could not see.
  const layer = document.querySelector('[data-dialog-layer]') as HTMLElement
  expect(layer).not.toBeNull()
  // Positive control on THIS instrument: it can see the token when it is present.
  expect(layer.innerHTML).toContain(TOKEN)

  await userEvent.click(screen.getByRole('button', { name: /I have copied it/ }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  expect(document.querySelector('[data-dialog-layer]')).toBeNull()
  expect(layer.innerHTML).not.toContain(TOKEN)
  expect(layer.parentNode).toBeNull()
  expect(domContainsSecret(TOKEN)).toBe(false)
  assertNoConsoleLeak(spies, TOKEN)

  spies.forEach((s) => s.mockRestore())
})
```

- [ ] **Step 3: Run it, then the gate**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx vitest run src/admin/enrollments/enrollmentTokenSecrecy.test.tsx
```

Expected: PASS, 11 tests.

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git diff --numstat $(git merge-base HEAD origin/main) -- web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx
```

Expected: deletions column is `0`.

- [ ] **Step 4: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git add web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx
git commit -m "$(cat <<'EOF'
test(web): prove the dialog layer leaves the DOM with the credential

The reveal dialog now renders through a portal, so a detached layer node holding
the token is a leak that document.body-scoped sweeps cannot see. Holds a reference
to the layer, confirms it contains the token while open (positive control on that
instrument), and asserts it is detached and emptied after Done.

The existing suite's own positive control at :182 was re-run and individually
confirmed passing.
EOF
)"
```

---

# Slice 4: acceptance

## Task 15: acceptance sweeps, build, and the final gate

**Files:** none modified. This task only measures.

- [ ] **Step 1: Full web suite on the settled tree**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npm test
```

Expected: PASS, with a net increase of **+19** over the pre-change count (5 `dialogStack` + 7 `DialogShell` + 5 `UsersTab` + 1 `WorkspacesPanel` + 1 `enrollmentTokenSecrecy`; `TokenRevealDialog`'s count is unchanged, one test replaced). If the delta is different, work out why before proceeding - an unexplained delta means a test was silently skipped or duplicated.

- [ ] **Step 2: Acceptance sweep - the shell is shared, not copied**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
rg 'role="dialog"' web/src
rg 'aria-modal' web/src
rg 'fixed inset-0 z-50' web/src
rg "addEventListener\('keydown'" web/src
```

Expected:
- `role="dialog"` - `web/src/components/dialog/DialogShell.tsx` only. (Test-file assertions use `getByRole('dialog')`, which does not match this pattern.)
- `aria-modal` - `DialogShell.tsx` plus the four existing `toHaveAttribute('aria-modal', 'true')` assertions in test files. No `.tsx` component outside the shell.
- `fixed inset-0 z-50` - `DialogShell.tsx` only.
- `addEventListener('keydown'` - `web/src/shell/UserMenu.tsx` only. It is a popup, not a modal, and is an explicit non-goal; a follow-up item is proposed in section 9.

Any other match is a STOP.

- [ ] **Step 3: The full byte-identical gate against the merge base**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
BASE=$(git merge-base HEAD origin/main)
git diff --numstat "$BASE" -- \
  web/src/components/ConfirmDialog.test.tsx \
  web/src/admin/users/ResetPasswordDialog.test.tsx \
  web/src/workers/WorkerActions.test.tsx \
  web/src/workers/WorkspacesPanel.test.tsx \
  web/src/jobs/JobActions.test.tsx \
  web/src/admin/users/UsersTab.test.tsx \
  web/src/admin/enrollments/EnrollmentsTab.test.tsx \
  web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx \
  web/src/jobs/JobDetailPage.test.tsx
git diff --stat "$BASE" -- 'web/src/**/*.test.ts' 'web/src/**/*.test.tsx'
```

Expected: the first command shows `0` deletions on every row. The second shows exactly two modified test files outside the protected list - `TokenRevealDialog.test.tsx` and `ReservationsTab.test.tsx` - plus the two new files `dialogStack.test.ts` and `DialogShell.test.tsx` as pure additions. Checked against the **merge base**, not just `HEAD`, so no earlier commit on this branch can have touched them.

- [ ] **Step 4: Production build**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb/web
npx tsc -b && npx vite build
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git checkout -- web/dist/
```

Expected: both green. `web/dist` is tracked but stale from the scaffold, so a build dirties it and it must be reverted before the change set is assembled.

- [ ] **Step 5: Confirm the change set is entirely frontend**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
git diff --name-only $(git merge-base HEAD origin/main)
```

Expected: every path starts with `web/src/` or `docs/`. No `.go`, no `.sql`, no `.proto`, no migration, no `web/dist`.

- [ ] **Step 6: Look at `WorkerDetailPage` in a real browser once**

The portal is a **deliberate, named visual change** on that page: it is what fixes the clipped scrim from `GlassPanel`'s `backdrop-blur`. Everything else in this change set is argued pixel-neutral by the byte-identical class-string rule, not screenshotted - the repo has no visual regression harness (`idea-2026-06-03-web-e2e-harness`, open). Start `relay-server`, open a worker detail page as an admin, click Evict on a workspace row, and confirm the scrim now covers the whole viewport rather than the panel box. Record the observation. If a browser is unavailable in this run, record that it was not done and flag it for the reviewer rather than claiming it.

- [ ] **Step 7: Close the backlog item**

```bash
cd D:/dev/relay/.claude/worktrees/pr-merging-session-3f03bb
```

Then run `/backlog close confirmdialog-focus-trap-hardening`. **Never hand-edit the item's `status` field.** The command runs the full close sequence: it `git mv`s the file from `docs/backlog/` into `docs/backlog/closed/`, stamps the `status`/`closed`/`resolution` frontmatter, appends the `## Resolution` note, and commits. Flipping `status` alone leaves the file in the open directory, which `/backlog list` then reports as a malformed open item.

---

# Section 8: deviations from the spec, with reasons

The spec is authoritative. These four points are places where following it literally would not work, or would leave a hole. Each is called out so a reviewer can rule on it rather than discover it.

1. **Slices 2 and 3 are merged.** The spec (section 8) puts `ResetPasswordDialog` in its own slice after `ConfirmDialog`, but T1 tabs inside the **reset** dialog and T2/T3/T3b mount reset-plus-archive together. None of them can go green while `ResetPasswordDialog` still owns its `document` keydown listener and renders outside the layer. Migrating both in Slice 2 is the only ordering under which the spec's own "slices 1-3 are each independently green and mergeable" holds.

2. **`registerDialog` takes the panel element, and the module exports `getTopmostPanel` and `isEmpty`.** The spec's API sketch is `registerDialog(id: string): void` with entries of `{ id }`. Two of the spec's own rules are unimplementable that way: "a non-topmost dialog's Tab moves focus to the topmost dialog's panel" (4.3) and "if the stack is empty, restore the trigger" (4.3.4) both need the stack to answer questions about the topmost entry. Deriving the topmost panel from DOM order in the layer instead would couple two mechanisms that are only incidentally in agreement.

3. **A closing NON-topmost dialog parks focus on the topmost panel.** Spec 4.3.4 says only "if the stack is now non-empty, do nothing and let the newly-topmost dialog's transition effect take focus". That covers the topmost-closes case but not the lower-closes case, where the topmost does not change, no transition fires, and focus - which was inside the dialog that just went away - lands on `<body>`, outside every open modal. T3b asserts against that (`expect(survivors[0].contains(document.activeElement)).toBe(true)`) and would fail under the spec-literal implementation. Parking on the panel rather than on its first focusable means this never competes with the transition effect when both do fire.

4. **The `TokenRevealDialog` Escape assertion is taken with focus inside the panel, before the backdrop click.** The spec's sketch keeps the existing order (backdrop click, then Escape). Post-fix that is vacuous: the backdrop click blurs the token input, so the Escape lands on `<body>` and never reaches the panel's `onKeyDown` - the assertion would pass whatever `dismissOnEscape` was set to. Reordering, plus the explicit `expect(...).toHaveFocus()` and the paired live-instrument control in `DialogShell.test.tsx`, is what makes it a real pin.

Two further facts the spec does not state, which the tasks depend on and which a reviewer should check: the `aria-hidden` / `getByRole` interaction (see "Facts verified" #2, and every `hidden: true` in the appended tests), and `useLayoutEffect` rather than `useEffect` for the shell's lifecycle (#5).

---

# Section 9: what this does and does not prove

State this plainly in the PR description so nobody over-reads a green suite.

- **The Tab trap is proven behaviorally.** `userEvent.tab()` honours `preventDefault()`, so T1 measures the real mechanism.
- **`inert` and `aria-hidden` are proven as attributes, plus one behavioral corollary.** `user-event` has no `inert` support at all, so no test in this repo can show `inert` blocks anything. They ship because they are what a real browser and a real screen reader act on. The one behavioral consequence the suite does measure is that `@testing-library/dom`'s accessibility-tree query stops seeing the background, which is why the appended tests need `hidden: true` - that is a genuine ARIA-semantics check, and it is the whole of it. Any future claim that "the tests prove the background is unreachable" is overclaiming.
- **The scrim's pointer occlusion is proven by neither.** jsdom does no hit-testing. That is precisely why T2 clicks straight through it, and why doing so is honest rather than a cheat.
- **Pixel neutrality is argued, not screenshotted.** The defences are the byte-identical class-string rule and a reviewer diffing the class strings before and after. One known class-attribute-order change: the width utility now trails the base instead of sitting inside it. The class set is identical and Tailwind resolves by stylesheet order regardless - but that is an argument, not a screenshot.
- **Two mounted dialogs remain possible and are now merely safe.** Sibling dialogs inside the layer are not marked `inert` relative to each other, deliberately: sequencing an inert-mark against a focus move introduces a race for a state that is rare and about to end.

# Section 10: follow-ups to propose (do not file mid-plan)

- `UserMenu`'s `document`-level Escape and mousedown-outside listeners are the last hand-rolled dismissal in the app. Not a modal, so out of scope here; worth one item to give it the same treatment or to state deliberately that a popup does not need it.
- Native `<dialog>` reconsideration, gated on jsdom implementing `HTMLDialogElement` or on the e2e harness landing. The evidence is in `dialogStack.ts`'s header; an item should carry the trigger condition so it is re-evaluated rather than re-litigated.
- A sweep test that fails if `role="dialog"` appears outside `DialogShell.tsx`. The Table iteration's lesson is that a rule living only in a comment is a rule the next caller does not read, and the type-level equivalent is weaker here than it was for `TableRow`.
