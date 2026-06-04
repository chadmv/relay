import { Navigate, Outlet } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { HoloShell } from '../shell/HoloShell'

export function ProtectedRoute() {
  const { status } = useAuth()
  if (status === 'loading') return <div className="min-h-screen bg-bg" />
  if (status === 'anonymous') return <Navigate to="/auth" replace />
  return (
    <HoloShell>
      <Outlet />
    </HoloShell>
  )
}
