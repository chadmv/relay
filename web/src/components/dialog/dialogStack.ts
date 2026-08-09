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
