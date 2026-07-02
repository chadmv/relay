import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { GlassPanel } from '../components/holo'

interface UserMenuProps {
  email: string
  onLogout: () => void
}

export function UserMenu({ email, onLogout }: UserMenuProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

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
        aria-haspopup="menu"
        aria-expanded={open}
        className={`flex items-center gap-2 rounded-full border px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.12em] transition-colors ${open ? 'border-accent/45 bg-accent/[0.14]' : 'border-border bg-accent/[0.08]'}`}
      >
        <span className="text-fg normal-case tracking-normal">{email}</span>
      </button>
      {open && (
        <GlassPanel className="absolute right-0 mt-2 w-56 p-1.5 text-[12px]">
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
