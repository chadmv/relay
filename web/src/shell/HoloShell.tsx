import type { ReactNode } from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { UserMenu } from './UserMenu'

const NAV = [
  { to: '/jobs', label: 'Jobs' },
  { to: '/workers', label: 'Workers' },
  { to: '/schedules', label: 'Schedules' },
  { to: '/admin', label: 'Admin' },
]

export function HoloShell({ children }: { children: ReactNode }) {
  const { user, logout } = useAuth()
  const navigate = useNavigate()

  async function onLogout() {
    await logout()
    navigate('/auth')
  }

  return (
    <div className="min-h-screen bg-bg text-fg">
      <header className="flex items-center justify-between border-b border-border px-5 py-3">
        <div className="flex items-center gap-6">
          <span className="font-sans text-[18px] font-bold">
            relay<span className="text-accent">.</span>
          </span>
          <nav className="flex gap-4 text-[12px]">
            {NAV.map((n) => (
              <NavLink
                key={n.to}
                to={n.to}
                className={({ isActive }) =>
                  isActive ? 'text-accent' : 'text-fg-mute hover:text-fg'
                }
              >
                {n.label}
              </NavLink>
            ))}
          </nav>
        </div>
        <UserMenu email={user?.email ?? ''} onLogout={onLogout} />
      </header>
      <main className="p-5">{children}</main>
    </div>
  )
}
