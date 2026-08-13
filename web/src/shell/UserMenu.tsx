import { useEffect, useId, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { GlassPanel } from '../components/holo'

interface UserMenuProps {
  email: string
  onLogout: () => void
}

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

  return (
    <div ref={ref} className="relative">
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
            onClick={closeAndRestoreFocus}
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Profile
          </Link>
          <Link
            to="/profile/password"
            onClick={closeAndRestoreFocus}
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Password
          </Link>
          <Link
            to="/profile/sessions"
            onClick={closeAndRestoreFocus}
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
