import { Navigate, Route, Routes } from 'react-router-dom'
import { LoginScreen } from '../auth/LoginScreen'
import { RegisterScreen } from '../auth/RegisterScreen'
import { JobsPlaceholder } from './JobsPlaceholder'
import { JobsPage } from '../jobs/JobsPage'
import { WorkersPage } from '../workers/WorkersPage'
import { ProtectedRoute } from './ProtectedRoute'
import { PublicOnlyRoute } from './PublicOnlyRoute'

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<PublicOnlyRoute />}>
        <Route path="/auth" element={<LoginScreen />} />
        <Route path="/register" element={<RegisterScreen />} />
      </Route>
      <Route element={<ProtectedRoute />}>
        <Route path="/jobs" element={<JobsPage />} />
        <Route path="/workers" element={<WorkersPage />} />
        <Route path="/schedules" element={<JobsPlaceholder />} />
        <Route path="/admin" element={<JobsPlaceholder />} />
        <Route path="/profile/*" element={<JobsPlaceholder />} />
      </Route>
      <Route path="*" element={<Navigate to="/jobs" replace />} />
    </Routes>
  )
}
