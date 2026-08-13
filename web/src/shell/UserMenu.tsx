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
  const panelId = useId()

  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div ref={ref} className="relative">
      <button
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
          <Link to="/profile" className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5">
            Profile
          </Link>
          <Link
            to="/profile/password"
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Password
          </Link>
          <Link
            to="/profile/sessions"
            className="block rounded-md px-2.5 py-2 text-fg hover:bg-white/5"
          >
            Sessions
          </Link>
          <div className="my-1.5 h-px bg-border" />
          <button
            onClick={onLogout}
            className="block w-full rounded-md px-2.5 py-2 text-left text-err hover:bg-white/5"
          >
            Log out
          </button>
        </GlassPanel>
      )}
    </div>
  )
}
