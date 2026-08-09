import { Navigate, Outlet } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'

// UX-only guard. The security boundary is server-side: every /v1/users admin route
// is registered auth(admin(...)) (internal/api/server.go:150-156), so a non-admin
// who forges client state just collects 403s. This renders INSIDE ProtectedRoute,
// so an authenticated user is already guaranteed and only is_admin is checked.
export function AdminRoute() {
  const { user } = useAuth()
  if (!user?.is_admin) return <Navigate to="/jobs" replace />
  return <Outlet />
}
