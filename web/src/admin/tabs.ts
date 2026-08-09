import type { ComponentType } from 'react'
import { EnrollmentsTab } from './enrollments/EnrollmentsTab'
import { ReservationsTab } from './reservations/ReservationsTab'
import { UsersTab } from './users/UsersTab'

export interface AdminTab {
  slug: string
  label: string
  Panel: ComponentType
}

// The admin console is a registry plus a switch. Tabs that are not built yet are
// ABSENT on purpose: an unknown /admin/:tab segment redirects to /admin/users
// instead of rendering an empty panel, so this slice cannot ship dead tabs.
// Adding a tab later is one entry here - see
// docs/backlog/feature-2026-08-08-admin-invites-tab.md,
// docs/backlog/feature-2026-08-08-admin-server-overview-tab.md.
// Order matches the hi-fi's tab order (Invites, still absent, sits between Users and
// Agent enrolls).
export const ADMIN_TABS: AdminTab[] = [
  { slug: 'users', label: 'Users', Panel: UsersTab },
  { slug: 'enrollments', label: 'Agent enrolls', Panel: EnrollmentsTab },
  { slug: 'reservations', label: 'Reservations', Panel: ReservationsTab },
]

export const DEFAULT_ADMIN_TAB = 'users'

export function findAdminTab(slug: string | undefined): AdminTab | undefined {
  return ADMIN_TABS.find((t) => t.slug === slug)
}
