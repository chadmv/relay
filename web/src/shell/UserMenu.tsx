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
// WHY NO role="menu" / role="menuitem" / ARROW KEYS. Three of the four entries are
// site navigation links, which is the case the menu role's own specification
// excludes, and role="menuitem" on an <a href> REPLACES the link role: the item
// stops being announced as a link and drops out of a screen reader's links list. A
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
// stopImmediatePropagation specifically to suppress this one (DialogShell.tsx:355-370),
// and the two are structurally prevented from overlapping anyway, so do not change
// this listener's registration site or its open-only lifetime without re-deriving
// that argument.
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
  // The containment check is read BEFORE setOpen, which is CLAUDE.md's "end the
  // generation before releasing the resource" in its smallest form: setOpen(false)
  // unmounts the panel and detaches whatever was focused, after which
  // document.activeElement is <body> and the check can no longer tell "focus was
  // on an item" from "focus was never in here at all".
  //
  // The guard is what separates a restore from focus theft: a mouse user in Safari
  // (which does not focus a <button> on click, so the menu can legitimately be open
  // with activeElement === <body>) must not have focus yanked onto a toggle it was
  // never on. Same reasoning as DialogShell.tsx:234-239,276-282, reused as REASONING
  // ONLY - none of its modal machinery applies to a dropdown.
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
    <div ref={ref} className="relative" onBlur={onContainerBlur}>
      <button
        ref={toggleRef}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        // Only while the panel is actually rendered: it is conditionally mounted
        // below, and an IDREF pointing at a node that does not exist is an
        // authoring error. aria-expanded, by contrast, is present in BOTH states.
        aria-controls={open ? panelId : undefined}
        className={`flex items-center gap-2 rounded-full border px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.12em] transition-colors ${open ? 'border-accent/45 bg-accent/[0.14]' : 'border-border bg-accent/[0.08]'}`}
      >
        <span className="text-fg normal-case tracking-normal">{email}</span>
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
