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
