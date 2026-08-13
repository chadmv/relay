import type { ComponentType } from 'react'
import { EnrollmentsTab } from './enrollments/EnrollmentsTab'
import { InvitesTab } from './invites/InvitesTab'
import { ReservationsTab } from './reservations/ReservationsTab'
import { ServerTab } from './server/ServerTab'
import { UsersTab } from './users/UsersTab'

export interface AdminTab {
  slug: string
  label: string
  Panel: ComponentType
}

// The admin console is a registry plus a switch. Tabs that are not built yet are
// ABSENT on purpose: an unknown /admin/:tab segment redirects to /admin/users
// instead of rendering an empty panel, so this file cannot ship dead tabs. Adding
// a tab is one entry here - nothing in routing or gating changes.
// Order matches the hi-fi's tab order: Invites sits between Users and Agent
// enrolls (design_handoff_relay_holo/hifi3-holo-pages.jsx:2083).
export const ADMIN_TABS: AdminTab[] = [
  { slug: 'users', label: 'Users', Panel: UsersTab },
  { slug: 'invites', label: 'Invites', Panel: InvitesTab },
  { slug: 'enrollments', label: 'Agent enrolls', Panel: EnrollmentsTab },
  { slug: 'reservations', label: 'Reservations', Panel: ReservationsTab },
  { slug: 'server', label: 'Server', Panel: ServerTab },
]

export const DEFAULT_ADMIN_TAB = 'users'

export function findAdminTab(slug: string | undefined): AdminTab | undefined {
  return ADMIN_TABS.find((t) => t.slug === slug)
}
