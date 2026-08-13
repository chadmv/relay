import type { ComponentType } from 'react'
import { IdentityTab } from './IdentityTab'
import { PasswordTab } from './PasswordTab'
import { SessionsTab } from './SessionsTab'

export interface ProfileTab {
  slug: string
  label: string
  Panel: ComponentType
}

// A registry plus a switch, mirroring web/src/admin/tabs.ts:21-32. This is the
// SECOND consumer of that shape and the house rule is extract before the THIRD,
// so no shared tab primitive is extracted here - recorded so the next tabbed
// surface triggers the extraction. The two are not parameterizable today without
// inventing options neither needs: admin sits behind AdminRoute and profile does
// not (every endpoint here is auth(...), never AdminOnly -
// internal/api/server.go:97-100, :153), and the hi-fi wants a count badge on
// Sessions that AdminTabs deliberately does not render and that we could not
// supply anyway, since GET /v1/auth/tokens does not exist.
//
// Slug note: the hi-fi's first slug is 'profile' (hifi3-holo-pages.jsx:2817),
// which would make the URL /profile/profile. 'identity' matches the panel's own
// name in the design (ProfileIdentity, :2909) and the backlog item's title.
export const PROFILE_TABS: ProfileTab[] = [
  { slug: 'identity', label: 'Identity', Panel: IdentityTab },
  { slug: 'password', label: 'Password', Panel: PasswordTab },
  { slug: 'sessions', label: 'Sessions', Panel: SessionsTab },
]

export const DEFAULT_PROFILE_TAB = 'identity'

export function findProfileTab(slug: string | undefined): ProfileTab | undefined {
  return PROFILE_TABS.find((t) => t.slug === slug)
}
