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
// WHY ESCAPE IS A DOCUMENT-LEVEL LISTENER, NOT PART OF THE PANEL'S onKeyDown
// (code review, 2026-08-09). The Tab trap above is panel-scoped and that is
// correct, because Tab only needs to be intercepted while focus IS inside the
// panel. Escape is different: it must still dismiss even when focus has left
// the panel, and focus leaves the panel through more routes than the shell can
// close. A React keydown handler only fires when the event's target is a
// descendant of the panel; once document.activeElement falls back to <body> -
// a scrim mousedown's default action, a disabled={pending} submit button
// blurring mid-request (ResetPasswordDialog), a trigger row removed from under
// a still-focused element - a keydown targeted at <body> never reaches it. On
// main, every dialog used a document-level listener and so was immune to this;
// making the trap panel-only was a regression this shell must not repeat. The
// listener is gated on `dismissOnEscape && isTopmost(id)`, which is the old
// behavior plus the new stack scoping, so it never fires for a non-topmost or
// Escape-suppressed dialog. onMouseDown on the scrim (below) additionally stops
// the ONE most common route (a backdrop press) from blurring the panel at all,
// but it cannot cover every route, which is why both exist.
//
// A focusin sentinel on document was rejected (two naive focus-pulling traps
// mounted at once livelock, for no gain over the keydown path), and so were
// zero-size focusable sentinel divs at the panel edges (they add DOM nodes inside
// a panel whose innerHTML is swept by both the reservations honesty test and the
// enrollment secrecy suite).
//
const SCRIM = 'fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4'
const PANEL_BASE = 'w-full rounded-card border border-border bg-bg p-5 shadow-xl'

// KNOWN LIMITATION, ACCEPTED (unchanged by the additions below): this selector
// does not evaluate display/visibility and does not cross shadow roots. No
// current consumer has hidden focusables or a shadow root.
const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([type=hidden]):not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex^="-"])',
  '[contenteditable]:not([contenteditable="false"])',
  'iframe',
  'object',
  'embed',
  'area[href]',
  'audio[controls]',
  'video[controls]',
  'summary',
].join(', ')

function focusables(panel: HTMLElement): HTMLElement[] {
  const all = Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE))
  // A browser puts only ONE radio per `name` group in the tab order: the
  // checked one, or the first if none is checked. Leaving every radio in meant
  // `last` could resolve to an untabbable node, so the wrap-around never fired
  // once the loop reached it - measured with two unchecked radios after a
  // button, focus escaped the panel entirely.
  const radiosByName = new Map<string, HTMLInputElement[]>()
  for (const el of all) {
    if (el instanceof HTMLInputElement && el.type === 'radio' && el.name) {
      const group = radiosByName.get(el.name) ?? []
      group.push(el)
      radiosByName.set(el.name, group)
    }
  }
  const radioWinners = new Set<HTMLInputElement>()
  for (const group of radiosByName.values()) {
    radioWinners.add(group.find((r) => r.checked) ?? group[0])
  }
  return all.filter((el) => {
    if (!(el instanceof HTMLInputElement) || el.type !== 'radio' || !el.name) return true
    return radioWinners.has(el)
  })
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
      // Read panelRef BEFORE unregisterDialog, deliberately: unregisterDialog's
      // apply() may detach the layer and move activeElement, so this must read
      // the world exactly as this dialog leaves it, not after. The read is
      // null-safe: nothing between effect entry and unregisterDialog may throw,
      // because unregisterDialog is what ends this dialog's generation, and if a
      // throw skipped it, the stack would keep a dead entry forever - the
      // background stays inert and body overflow stays hidden, permanently,
      // since nothing would ever call unregisterDialog for this id again.
      const panel = panelRef.current
      const focusWasInside = !!panel && panel.contains(document.activeElement)
      // Rule 2 of dialogStack: end the generation (leave the stack) before
      // deciding anything about the world.
      unregisterDialog(id)

      if (!focusWasInside) {
        // Focus was already on a background control when this dialog closed
        // (it was never yanked here to begin with). If another dialog is still
        // open, that control is now behind ITS scrim and about to go inert, so
        // park focus on the survivor rather than leave it stranded there. If no
        // dialog remains, do nothing: the user's own focus target is left alone.
        if (!isEmpty()) getTopmostPanel()?.focus()
        return
      }
      if (isEmpty()) {
        const trigger = triggerRef.current
        if (trigger && trigger.isConnected) {
          trigger.focus()
          return
        }
        // trigger.isConnected is false: the trigger was ALREADY gone by the
        // time this cleanup ran. Falling through to <body> is a silent a11y
        // regression - a keyboard user's next Tab starts from the top of the
        // document instead of near where they were working - so land on `main`
        // instead, a landmark every page renders (HoloShell.tsx); give it a
        // programmatic focus stop if it does not already have one, rather than
        // requiring every page to add one.
        //
        // KNOWN GAP, investigated and accepted rather than silently claimed
        // fixed: all five ConfirmDialog call sites (delete reservation, evict
        // workspace, archive user, job action, unarchive) close SYNCHRONOUSLY -
        // setConfirm(null) runs in the same handler as .mutate(), before the
        // network resolves - so trigger.isConnected is normally still TRUE
        // right here, and the branch above runs instead. The row only
        // disappears LATER, once the mutation's invalidate-on-success refetch
        // completes and React removes it in an UNRELATED commit this cleanup
        // has already returned from by then. That later removal is not
        // observable from here: jsdom fires no event when a focused node is
        // silently detached (confirmed against
        // jsdom/lib/jsdom/living/nodes/Node-impl.js's _detach(), which nulls
        // the document's focus record directly with no blur/focusout
        // dispatch), and the two mechanisms that WOULD observe it in a real
        // browser - a MutationObserver, or a document-level focusout sentinel -
        // are the same shape of always-on background watcher the focusin
        // sentinel above was rejected for, and out of scope here. This branch
        // still covers every case it CAN see: a trigger already gone at close
        // time (e.g. a caller with a synchronous/optimistic removal).
        const landmark = document.querySelector('main')
        if (landmark instanceof HTMLElement) {
          if (!landmark.hasAttribute('tabindex')) landmark.tabIndex = -1
          landmark.focus()
        }
        return
      }
      // Another dialog is still open. Park focus on the topmost panel rather
      // than on the trigger (which sits behind the scrim) or on <body> (which
      // would put focus outside every open modal). If THIS close also promoted
      // a new topmost - i.e. this dialog itself was the one that was topmost -
      // that dialog's transition effect refines this to its own initial target
      // a moment later. If a LOWER dialog closed instead, no promotion happens
      // and this focus() call is the only one that runs.
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

  // Escape is a DOCUMENT-level listener - see the header comment for why the Tab
  // trap is correctly panel-scoped but Escape cannot be. Gated on
  // `dismissOnEscape && isTopmost(id)`, read at EVENT time so a captured value
  // can never go stale, this is the old pre-shell behavior (every dialog used a
  // document listener) plus the new stack scoping on top of it.
  useLayoutEffect(() => {
    function onDocumentKeyDown(e: KeyboardEvent) {
      if (e.key !== 'Escape') return
      if (!dismissOnEscape) return
      // Defence in depth for the case where focus somehow sits in a lower
      // dialog, and the actual gate for "am I the one that should act" when
      // focus is nowhere in particular (outside every panel).
      if (!isTopmost(id)) return
      // stopImmediatePropagation, NOT stopPropagation: both listeners are on
      // `document`, and plain stopPropagation only stops an event travelling
      // to the NEXT node in the path - it does nothing to another listener
      // registered on the SAME node (verified directly: two keydown listeners
      // on document, the first calling stopPropagation, and the second still
      // ran). Only stopImmediatePropagation blocks a sibling listener on this
      // node from firing. In THIS app that sibling is UserMenu's own Escape
      // handler, and the two are structurally prevented from overlapping
      // anyway - UserMenu's listener only exists while its dropdown is open,
      // and its toggle button is itself a background control that goes inert
      // while any dialog is open, so the dropdown cannot be opened in the
      // first place while this fires. This is defence in depth on top of that
      // structural guarantee, not the only thing preventing an overlap, and it
      // only ever blocks a listener registered AFTER this one for the same
      // dispatch - it cannot un-ring a bell a listener registered EARLIER
      // already rang.
      e.stopImmediatePropagation()
      onDismissRef.current()
    }
    document.addEventListener('keydown', onDocumentKeyDown)
    return () => document.removeEventListener('keydown', onDocumentKeyDown)
  }, [id, dismissOnEscape])

  // Tab handling stays on the panel, not on document: with focus inside the
  // topmost dialog by construction, a panel-scoped onKeyDown for Tab is
  // received by exactly one dialog while focus is where the trap put it.
  // isTopmost is read here, at EVENT time, never captured.
  function onKeyDown(e: ReactKeyboardEvent<HTMLDivElement>) {
    const panel = panelRef.current
    if (!panel) return

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
    // No onClick here. See the header. onMouseDown DOES intercept, but only to
    // preventDefault the browser's default focus-change behavior for a press
    // directly on the scrim (e.target === e.currentTarget excludes bubbling
    // from a child, i.e. from inside the panel) - it never dismisses. Without
    // this, a press on the non-focusable scrim blurs whatever was focused
    // (jsdom and browsers both do this), which is the scenario the
    // document-level Escape listener above must independently survive, but
    // which also breaks the Tab trap and all other keyboard interaction until
    // focus is somehow restored. This is the second half of that fix, not a
    // dismiss route.
    <div
      className={SCRIM}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) e.preventDefault()
      }}
    >
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
