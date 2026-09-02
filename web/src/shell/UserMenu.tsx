import {
  useEffect,
  useId,
  useRef,
  useState,
  type FocusEvent,
  type MouseEvent as ReactMouseEvent,
} from 'react'
import { Link } from 'react-router-dom'
import { GlassPanel } from '../components/holo'

interface UserMenuProps {
  email: string
  onLogout: () => void
}

// The header account dropdown. It is an ARIA DISCLOSURE, not a menu, and that is a
// decision rather than an omission - see
// docs/superpowers/specs/2026-08-13-usermenu-menu-roles.md.
//
// shell/HoloShell.tsx's collapsed nav is the sibling disclosure and shares this
// file's handler set, so a change to the behaviour here almost certainly belongs
// there too.
//
// WHY NO role="menu" / role="menuitem" / ARROW KEYS. Three of the four entries are
// site navigation links, which is the case the menu role's own specification
// excludes, and role="menuitem" on an <a href> REPLACES the link role: the item
// stops being announced as a link, drops out of a screen reader's links list, and
// drops out of browse-mode "next link" navigation. This is an AT-exposed-semantics
// cost only - the anchor is still a real <a href>, so the browser's own context
// menu and Ctrl/Cmd+click still open it in a new tab exactly as before; nothing
// about the ARIA role changes what the browser itself does with the element. A
// conforming menu also uses a roving tabindex, which would make those three links
// unreachable by Tab. So the toggle carries aria-expanded plus aria-controls and
// nothing else; the items stay three ordinary links and one ordinary button, and Tab
// reaches them natively because the panel follows the toggle in DOM order. This file
// previously advertised aria-haspopup="menu" against a plain <div> of links
// (backlog: feature-2026-06-05-usermenu-panel-menu-roles, closed by INVERTING its
// Proposal); that attribute was the only thing that ever claimed "menu". If this
// dropdown ever stops containing navigation links and becomes actions only, the
// calculus flips and role="menu" becomes correct - that is the trigger to revisit,
// and nothing short of it is.
//
// WHY ESCAPE IS A DOCUMENT LISTENER AND NOT AN onKeyDown ON THE PANEL. A React
// onKeyDown only fires when the event target is a descendant of the panel, and focus
// leaves the panel through more routes than this component can close - notably
// Safari, which does not focus a <button> on click, so the menu can be open with
// activeElement === <body> and a panel-scoped handler would never fire at all. This
// is DialogShell's one high-severity review finding
// (components/dialog/DialogShell.tsx:59-75) and it binds harder here. Do not "tidy"
// it onto the panel. DialogShell's own document Escape listener calls
// stopImmediatePropagation (DialogShell.tsx:355-372), but that only suppresses a
// SIBLING listener registered AFTER it on the same dispatch
// (DialogShell.test.tsx:425-431 pins exactly that narrower claim) - it cannot
// un-ring a bell a listener registered earlier already rang. The two DO overlap
// in practice: open this dropdown by mouse (so focus never lands inside it - the
// Safari case above), then keyboard-focus a page control and open a dialog; this
// listener was registered first, so on Escape it runs first, is not suppressed,
// and closes the dropdown, and DialogShell's own handler then runs right after
// and closes the dialog too (measured directly). One Escape dismissing both is
// acceptable and is not changed by that fact - what must not be assumed is that
// the overlap is structurally impossible, only that dismissing both is fine.
// Registration order, not "cannot open while a dialog is open", is what decides
// which listener wins.
//
// WHY THE PANEL IS NOT PORTALLED and does not register with dialogStack. Two
// reasons: the disclosure pattern needs the panel to FOLLOW the toggle in DOM order
// so Tab reaches it, and the dropdown's paint order is already solved by
// `relative z-10` on the header (HoloShell.tsx:29-49, measured over 275 hit-test
// points), which moving the panel to <body> would invalidate. Nothing here is modal:
// no scrim, no scroll lock, no inert, no aria-hidden on the background, and no Tab
// trap - for a disclosure, Tab out is a dismiss route.
export function UserMenu({ email, onLogout }: UserMenuProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const toggleRef = useRef<HTMLButtonElement>(null)
  const panelId = useId()

  // Close AND return focus to the toggle, but ONLY if focus was inside the
  // container. Used by Escape and by all four items.
  //
  // The containment check is read BEFORE setOpen, and that ordering is kept
  // defensively even though it costs nothing to keep - but NOT because setOpen(false)
  // synchronously unmounts the panel and detaches focus the way a cleanup running
  // after a real unmount does. It doesn't: React 18.3.1 batches this update, so the
  // panel is still mounted and document.activeElement is unchanged for the rest of
  // this handler, and reading the check AFTER setOpen would observe the identical
  // value (measured directly). DialogShell.tsx:227-240 is the real instance of that
  // detachment shape - its read runs in an effect CLEANUP, which React defers past
  // the point the node is actually gone, so ordering there is load-bearing, not
  // merely tidy.
  //
  // What the guard actually buys here is preventing focus theft: a mouse user in
  // Safari (which does not focus a <button> on click, so the menu can legitimately
  // be open with activeElement === <body>) must not have focus yanked onto a toggle
  // it was never on. Same REASONING as DialogShell.tsx:234-239,276-282 - none of its
  // modal machinery applies to a dropdown, and unlike that file's use of the
  // pattern, nothing here depends on beating a synchronous teardown.
  function closeAndRestoreFocus() {
    const focusWasInside = !!ref.current && ref.current.contains(document.activeElement)
    setOpen(false)
    if (focusWasInside) toggleRef.current?.focus()
  }

  // Close WITHOUT touching focus. Used by the two paths where the browser is
  // already moving focus itself, so a restore here would fight the user.
  function close() {
    setOpen(false)
  }

  // Guard for the three nav Links' onClick, in one place rather than three
  // copies. React Router's Link calls the caller's onClick BEFORE it decides
  // whether to navigate, so an unconditional close here ran even for a
  // Ctrl/Cmd/Shift/Alt+click or a non-primary button - collapsing the dropdown
  // and stealing focus back to the toggle in the tab the user is still looking
  // at, while the click opens a new tab or window. Same predicate react-router
  // itself uses to decide whether IT will handle the click
  // (shouldProcessLinkClick, react-router chunk-QUQL4437.mjs:7288-7292): only a
  // plain left click closes the menu.
  function onNavItemClick(e: ReactMouseEvent<HTMLAnchorElement>) {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return
    closeAndRestoreFocus()
  }

  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      // close(), NOT closeAndRestoreFocus(): mousedown fires BEFORE the browser
      // moves focus to whatever was pressed, so at this instant activeElement is
      // still inside the panel and a restore would steal focus away from the
      // control the user just clicked. Identical rule to the Escape path below,
      // opposite answer, purely because the event ordering differs. This is why the
      // DialogShell reasoning had to be re-derived here rather than copied.
      if (ref.current && !ref.current.contains(e.target as Node)) close()
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') closeAndRestoreFocus()
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
    // Both helpers are captured from the render that ran this effect and are
    // deliberately not dependencies: they touch only refs and setOpen, all of which
    // are stable for the component's life, so a stale closure cannot observe stale
    // state. Listing them would re-subscribe both document listeners on every
    // render for no behaviour gain.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  // React maps onBlur to the native, BUBBLING focusout, so this fires for focus
  // leaving any descendant of the container.
  //
  // Tab out is a DISMISS route for a disclosure, not something to intercept. Do not
  // copy DialogShell's Tab trap here: a menu is not modal, and the page behind it
  // stays fully interactive.
  function onContainerBlur(e: FocusEvent<HTMLDivElement>) {
    // A NULL relatedTarget means "blurred to nothing" - jsdom fires exactly that for
    // a bare blur() (jsdom/living/nodes/HTMLOrSVGElement-impl.js:82-83), and in a
    // real browser it is what pressing the mouse on this panel's own non-focusable
    // email header produces. Closing on it would make the dropdown vanish under the
    // user's cursor. The document mousedown handler already owns the "pressed
    // somewhere else" case, so this branch has a correct owner and does not need a
    // second one.
    if (!e.relatedTarget) return
    // Shift+Tab from the first item lands on the toggle, which is INSIDE this
    // container, so the containment check is what keeps the menu open there.
    //
    // close(), not closeAndRestoreFocus(): by construction focus is already outside,
    // so the restore would be a theft from the destination the user just Tabbed to.
    if (ref.current && !ref.current.contains(e.relatedTarget)) close()
  }

  return (
    <div ref={ref} className="relative min-w-0" onBlur={onContainerBlur}>
      <button
        ref={toggleRef}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        // Only while the panel is actually rendered: it is conditionally mounted
        // below, and an IDREF pointing at a node that does not exist is an
        // authoring error. aria-expanded, by contrast, is present in BOTH states.
        aria-controls={open ? panelId : undefined}
        className={`flex w-full min-w-0 items-center gap-2 rounded-full border px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.12em] transition-colors ${open ? 'border-accent/45 bg-accent/[0.14]' : 'border-border bg-accent/[0.08]'}`}
      >
        <span className="truncate text-fg normal-case tracking-normal">{email}</span>
      </button>
      {/* bg-popover is the load-bearing half here. GlassPanel sets NO
          background-color at all - only a 6%->2% white gradient, which is a
          background-IMAGE (measured: computed backgroundColor is rgba(0,0,0,0)).
          That is right for a panel sitting ON the page floor and wrong for one
          floating over live content, which would read straight through it. The
          two are different properties, so bg-popover fills in underneath and the
          gradient stays on top as a sheen.

          z-50 is NOT what stops the page bleeding through - it cannot be, since
          the header's backdrop-blur confines it (see HoloShell, which owns the
          fix and the measurements). It is local ordering only: it keeps this
          dropdown above anything later added to the header. */}
      {open && (
        <GlassPanel
          id={panelId}
          data-testid="user-menu-panel"
          className="absolute right-0 z-50 mt-2 w-56 bg-popover p-1.5 text-[12px]"
        >
          <div className="mb-1.5 flex items-center gap-2.5 border-b border-border px-2.5 pb-2.5 pt-2">
            <span className="truncate text-[12.5px] text-fg">{email}</span>
          </div>
          <Link
            to="/profile"
            onClick={onNavItemClick}
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Profile
          </Link>
          <Link
            to="/profile/password"
            onClick={onNavItemClick}
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Password
          </Link>
          <Link
            to="/profile/sessions"
            onClick={onNavItemClick}
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Sessions
          </Link>
          <div className="my-1.5 h-px bg-border" />
          <button
            onClick={() => {
              // Close first, then hand off. onLogout tears the session down and
              // unmounts this whole shell; doing it in this order means the
              // containment check above runs while the panel is unambiguously
              // still mounted.
              closeAndRestoreFocus()
              onLogout()
            }}
            className="block w-full rounded-md px-2.5 py-2 text-left text-err hover:bg-white/5"
          >
            Log out
          </button>
        </GlassPanel>
      )}
    </div>
  )
}
