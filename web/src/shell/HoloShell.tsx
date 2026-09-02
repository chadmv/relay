import { useId, useRef, useState, type ReactNode } from 'react'
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

  const [navOpen, setNavOpen] = useState(false)
  const navRef = useRef<HTMLElement>(null)
  const navToggleRef = useRef<HTMLButtonElement>(null)
  const navPanelId = useId()

  async function onLogout() {
    await logout()
    navigate('/auth')
  }

  // ONE copy of the links, always mounted, switched between an inline row and a
  // dropdown by CSS alone. Two copies would put two links named "Jobs" in the
  // accessibility tree and would break every getByRole('link', { name }) query in
  // this file's tests, which throw on multiple matches. jsdom applies none of this
  // app's CSS, so no unit test can tell the collapsed state from a broken one -
  // the browser lane owns that claim.
  //
  // `hidden` with `md:flex` is the Tailwind idiom: the variant rule is emitted
  // after the base utility, so md:flex wins at and above the breakpoint whatever
  // the open state is.
  const navPanelClass = `min-w-0 gap-0.5 md:flex md:overflow-x-auto ${navOpen ? 'flex' : 'hidden'}`

  return (
    <div className="min-h-screen bg-bg text-fg">
      {/* `relative z-10` here is what keeps UserMenu's dropdown visible. The
          dropdown hangs out of this header and over <main>, which comes LATER in
          the document and whose panels create stacking contexts of their own
          (every GlassPanel carries backdrop-blur), so at z-auto the page content
          paints over the open menu. The z-index has to be declared out HERE, on
          the header, not on the dropdown: this header's own backdrop-blur makes
          it a stacking context, which confines any z-index inside it. Measured
          in Chrome over 275 hit-test points across the open dropdown - with the
          dropdown's own z-50 and nothing else, 220 of them still returned a page
          panel; with `z-10` here and no z-index on the dropdown at all, 0 did.

          `relative z-0` on <main> does NOT fix today's bug (z-10 alone measured
          0/275 with <main> untouched). It is a guard on the next one: it wraps
          every descendant z-index in one stacking context, so a page-level
          z-index can never climb over the header. Measured the same way with a
          `relative z-20` added to a page panel - 99/275 occluded without this
          class, 0 with it.

          Dialogs are unaffected either way: they portal to a layer appended to
          <body>, outside both siblings, and keep their z-50 above them. */}
      {/* Narrow-viewport rule for this header (measured 2026-08-13): the nav
          panel is the ONLY element allowed to become a scroll container. The
          dropdown that UserMenu hangs below this header would be clipped by an
          overflow declared here, which is the same stacking behaviour the comment
          above measures. See
          docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md. */}
      <header className="relative z-10 flex items-center justify-between gap-3 border-b border-border bg-white/[0.025] px-[22px] py-3 backdrop-blur-[10px]">
        <div className="flex min-w-0 items-center gap-6">
          <Eyebrow className="text-accent">RELAY</Eyebrow>
          {/* The landmark wraps the toggle as well as the links, so it exists at
              every width even while the panel is collapsed, and aria-label names
              it now that it contains a control. It must NOT become positioned:
              the panel anchors to the <header>, which is already `relative`. */}
          <nav ref={navRef} aria-label="Main" className="min-w-0">
            <button
              ref={navToggleRef}
              type="button"
              onClick={() => setNavOpen((v) => !v)}
              aria-expanded={navOpen}
              // Present in BOTH states, unlike UserMenu's, because this panel is
              // always mounted so the IDREF always resolves.
              aria-controls={navPanelId}
              className={`md:hidden rounded-full border px-2.5 py-1 font-mono text-[10px] uppercase tracking-[0.12em] transition-colors ${navOpen ? 'border-accent/45 bg-accent/[0.14]' : 'border-border bg-accent/[0.08]'}`}
            >
              Menu
            </button>
            {/* min-w-0 lets this shrink below its content (a flex item's automatic
                minimum is its content width, which is what made the header a 523px
                floor); md:overflow-x-auto then makes every route reachable by
                scrolling at and above the breakpoint. Inert at any width where the
                links fit: a scroll container with no overflow renders no scrollbar
                and no visual difference. */}
            <div id={navPanelId} data-testid="header-nav-panel" className={navPanelClass}>
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
            </div>
          </nav>
        </div>
        <UserMenu email={user?.email ?? ''} onLogout={onLogout} />
      </header>
      <main className="relative z-0 p-5">{children}</main>
    </div>
  )
}
