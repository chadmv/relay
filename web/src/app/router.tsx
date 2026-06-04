import { Navigate, Route, Routes } from 'react-router-dom'
import { LoginScreen } from '../auth/LoginScreen'
import { RegisterScreen } from '../auth/RegisterScreen'
import { JobsPlaceholder } from './JobsPlaceholder'
import { ProtectedRoute } from './ProtectedRoute'

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/auth" element={<LoginScreen />} />
      <Route path="/register" element={<RegisterScreen />} />
      <Route element={<ProtectedRoute />}>
        <Route path="/jobs" element={<JobsPlaceholder />} />
        <Route path="/workers" element={<JobsPlaceholder />} />
        <Route path="/schedules" element={<JobsPlaceholder />} />
        <Route path="/admin" element={<JobsPlaceholder />} />
        <Route path="/profile/*" element={<JobsPlaceholder />} />
      </Route>
      <Route path="*" element={<Navigate to="/jobs" replace />} />
    </Routes>
  )
}
