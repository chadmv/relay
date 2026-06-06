import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'

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
        className="rounded-full border border-border bg-white/5 px-3 py-1 text-[12px] text-fg"
      >
        {email}
      </button>
      {open && (
        <div
          className="absolute right-0 mt-2 w-44 rounded-card border border-border p-1 text-[12px] shadow-xl"
          style={{ background: 'rgba(14,12,30,0.96)' }}
        >
          <Link to="/profile" className="block rounded px-3 py-2 hover:bg-white/5">
            Profile
          </Link>
          <Link to="/profile/password" className="block rounded px-3 py-2 hover:bg-white/5">
            Password
          </Link>
          <Link to="/profile/sessions" className="block rounded px-3 py-2 hover:bg-white/5">
            Sessions
          </Link>
          <button
            onClick={onLogout}
            className="block w-full rounded px-3 py-2 text-left text-err hover:bg-white/5"
          >
            Log out
          </button>
        </div>
      )}
    </div>
  )
}
