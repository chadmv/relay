import { Navigate, useParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { Eyebrow } from '../components/holo'
import { ProfileTabs } from './ProfileTabs'
import { DEFAULT_PROFILE_TAB, findProfileTab } from './tabs'

// Up to two initials from the display name. Splits on runs of whitespace,
// which can yield leading/trailing empty parts for a name with surrounding
// whitespace (the server only trims the ends, internal/api/users.go:61, but
// an interior double space survives). No .filter(Boolean) is needed for
// that: part[0] on an empty string is undefined, and Array.prototype.join
// stringifies undefined as '', so an empty part simply contributes nothing.
function initialsOf(name: string): string {
  return name
    .split(/\s+/)
    .map((part) => part[0])
    .join('')
    .slice(0, 2)
    .toUpperCase()
}

// The profile shell. Mirrors AdminPage.tsx:15-40: registry, switch, redirect on
// anything unknown.
//
// The hi-fi's meta strip has a fourth entry, LAST LOGIN (hifi3-holo-pages.jsx:2846),
// and a whole Activity side card (:2946-2965). Both are omitted, and that is a
// decision rather than a deferral: the users table has no last-login column, there
// is no login counter, and a session count would need GET /v1/auth/tokens, which
// does not exist. MEMBER SINCE - the one real value - moves into this strip, where
// it stands on its own. Rendering "-" for facts the backend cannot supply is the
// VERSION/BUILD/DB/UPTIME mistake the admin console already avoided (AdminPage.tsx:6-14).
//
// The hi-fi's "<- BACK" link (:2829) is also omitted: it is a prototype router
// artifact, and the app's real back affordance is the persistent shell nav.
export function ProfilePage() {
  const { tab } = useParams()
  const { user } = useAuth()

  // tab is never undefined here: this component is only ever mounted at
  // /profile/:tab (router.tsx:47), and /profile alone is a separate route that
  // redirects to /profile/identity (router.tsx:46) before ProfilePage ever
  // renders. Anything unrecognized redirects, so the surface can never show a
  // dead tab.
  const active = findProfileTab(tab)
  if (!active) return <Navigate to={`/profile/${DEFAULT_PROFILE_TAB}`} replace />
  // ProtectedRoute blanks the page while status is 'loading', so in the app this
  // is never null by the time the route renders. The guard keeps the component
  // correct on its own.
  if (!user) return null

  const Panel = active.Panel

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-end gap-6">
        {/* min-w-0 here and on the H1: a bootstrap admin with no separate display
            name renders its email as this 32px heading, which sets a floor at
            320px the same way UserMenu.tsx's toggle did before its own min-w-0 +
            truncate fix. */}
        <div className="min-w-0">
          <Eyebrow>YOUR ACCOUNT</Eyebrow>
          <h1 className="flex min-w-0 items-center gap-3.5 text-[32px] font-normal tracking-tight">
            <span
              data-testid="profile-initials"
              aria-hidden="true"
              className="grid h-10 w-10 place-items-center rounded-[10px] bg-gradient-to-br from-accent to-accent-b text-[15px] font-bold tracking-[0.04em] text-bg"
            >
              {initialsOf(user.name)}
            </span>
            <span className="truncate">{user.name}</span>
          </h1>
        </div>
        <div className="ml-auto flex gap-3.5 font-mono text-[11px] tracking-[0.06em] text-fg-mute">
          <div className="flex flex-col items-end gap-px">
            <span className="text-[9px] tracking-[0.16em]">EMAIL</span>
            <span data-testid="meta-email" className="text-[12px] text-fg">
              {user.email}
            </span>
          </div>
          <div className="flex flex-col items-end gap-px">
            <span className="text-[9px] tracking-[0.16em]">ROLE</span>
            <span data-testid="meta-role" className="text-[12px] text-fg">
              {user.is_admin ? 'ADMIN' : 'USER'}
            </span>
          </div>
          <div className="flex flex-col items-end gap-px">
            <span className="text-[9px] tracking-[0.16em]">MEMBER SINCE</span>
            {/* The house rendering for an absolute date: a string slice, matching
                UsersTable.tsx:123. No Date parsing, so there is no timezone
                behaviour to get wrong, and a fixture missing the field throws
                loudly instead of rendering "Invalid Date". */}
            <span data-testid="meta-member-since" className="text-[12px] text-fg">
              {user.created_at.slice(0, 10)}
            </span>
          </div>
        </div>
      </div>
      <ProfileTabs />
      <div className="flex min-h-0 flex-1 flex-col gap-3">
        <Panel />
      </div>
    </div>
  )
}
