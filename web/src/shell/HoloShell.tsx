import type { ReactNode } from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { Eyebrow } from '../components/holo'
import { UserMenu } from './UserMenu'

const NAV = [
  { to: '/jobs', label: 'Jobs' },
  { to: '/workers', label: 'Workers' },
  { to: '/schedules', label: 'Schedules' },
  // Cosmetic gate only - AdminRoute redirects and the server's AdminOnly
  // middleware is the real boundary. Hiding it keeps non-admins out of a route
  // that would only 403 for them.
  { to: '/admin', label: 'Admin', adminOnly: true },
]

export function HoloShell({ children }: { children: ReactNode }) {
  const { user, logout } = useAuth()
  const navigate = useNavigate()
  const nav = NAV.filter((n) => !n.adminOnly || user?.is_admin)

  async function onLogout() {
    await logout()
    navigate('/auth')
  }

  return (
    <div className="min-h-screen bg-bg text-fg">
      <header className="flex items-center justify-between border-b border-border bg-white/[0.025] px-[22px] py-3 backdrop-blur-[10px]">
        <div className="flex items-center gap-6">
          <Eyebrow className="text-accent">RELAY</Eyebrow>
          <nav className="flex gap-0.5">
            {nav.map((n) => (
              <NavLink
                key={n.to}
                to={n.to}
                className={({ isActive }) =>
                  `border-b-2 px-[14px] py-[7px] text-[13px] tracking-[0.02em] transition-colors ${
                    isActive
                      ? 'border-accent text-fg'
                      : 'border-transparent text-fg-mute hover:text-fg'
                  }`
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
