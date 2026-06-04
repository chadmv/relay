import { Navigate, Outlet } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'

// PublicOnlyRoute gates the auth screens. Once authenticated (including the
// moment login/register succeeds and flips auth state), it redirects to the
// default landing page so the user doesn't stay stranded on the sign-in form.
export function PublicOnlyRoute() {
  const { status } = useAuth()
  if (status === 'authenticated') return <Navigate to="/jobs" replace />
  return <Outlet />
}
