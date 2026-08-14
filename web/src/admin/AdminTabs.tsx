import { NavLink } from 'react-router-dom'
import { ADMIN_TABS } from './tabs'

// The hi-fi's pill-group tab bar: rounded-full, dark translucent, active tab
// filled with the accent gradient. NavLink supplies the active state (and
// aria-current="page"), matching the HoloShell nav pattern. Count badges are not
// rendered in this slice - a count would have to be lifted out of each panel's
// own query, and the Users footer already shows the total.
export function AdminTabs() {
  return (
    // flex-wrap: found by Task 7's real-browser pass of
    // docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md - the header
    // floor (Cause 0) was masking this row at 523px until it was fixed, the same
    // way it masked the breadcrumb rows Task 5 fixed. Five pills do not fit a
    // 375px viewport; wrapping is the same remedy as Task 5's rows rather than a
    // new idiom.
    <div className="flex flex-wrap gap-1.5 self-start rounded-full border border-border bg-black/30 p-[3px] backdrop-blur-[8px]">
      {ADMIN_TABS.map((t) => (
        <NavLink
          key={t.slug}
          to={`/admin/${t.slug}`}
          className={({ isActive }) =>
            `rounded-full px-3.5 py-1.5 text-[12px] tracking-[0.02em] transition-colors ${
              isActive
                ? 'bg-gradient-to-r from-accent to-accent-b font-semibold text-bg'
                : 'text-fg-mute hover:text-fg'
            }`
          }
        >
          {t.label}
        </NavLink>
      ))}
    </div>
  )
}
