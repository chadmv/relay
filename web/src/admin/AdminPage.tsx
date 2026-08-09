import { Navigate, useParams } from 'react-router-dom'
import { Eyebrow } from '../components/holo'
import { AdminTabs } from './AdminTabs'
import { DEFAULT_ADMIN_TAB, findAdminTab } from './tabs'

// The admin shell. The hi-fi's right-aligned VERSION / BUILD / DB / UPTIME strip is
// omitted, and that is a decision rather than a deferral: no endpoint returns build
// or uptime facts (GET /v1/health returns {"status":"ok"} and does not check the
// database; GET /v1/config returns only {allow_self_register}). The Server tab
// (admin/server/ServerTab.tsx) ships without it for the same reason. Reviving the
// strip requires a new admin-gated endpoint returning a hand-written ALLOWLIST of
// non-secret config keys - see
// docs/backlog/feature-2026-08-09-server-info-allowlist-endpoint.md - never a
// redacted dump of os.Environ().
export function AdminPage() {
  const { tab } = useParams()
  // No :tab segment (e.g. an exact "/admin/users" route with no dynamic param)
  // means "use the default" - render it directly rather than redirecting to the
  // same path, which would be a no-op Navigate that renders nothing forever.
  const active = tab === undefined ? findAdminTab(DEFAULT_ADMIN_TAB) : findAdminTab(tab)
  // Unknown, or a tab that is not built yet: redirect rather than render an empty
  // shell, so the console never shows a dead tab.
  if (!active) return <Navigate to={`/admin/${DEFAULT_ADMIN_TAB}`} replace />
  const Panel = active.Panel

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <Eyebrow>SETTINGS · ADMIN ONLY</Eyebrow>
          <h1 className="text-[32px] font-normal tracking-tight">Admin</h1>
        </div>
      </div>
      <AdminTabs />
      <div className="flex min-h-0 flex-1 flex-col gap-3">
        <Panel />
      </div>
    </div>
  )
}
