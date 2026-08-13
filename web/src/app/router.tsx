import { Navigate, Route, Routes } from 'react-router-dom'
import { LoginScreen } from '../auth/LoginScreen'
import { RegisterScreen } from '../auth/RegisterScreen'
import { ProfilePage } from '../profile/ProfilePage'
import { JobsPage } from '../jobs/JobsPage'
import { JobDetailPage } from '../jobs/JobDetailPage'
import { TaskLogPage } from '../jobs/TaskLogPage'
import { NewJobPage } from '../jobs/NewJobPage'
import { WorkersPage } from '../workers/WorkersPage'
import { WorkerDetailPage } from '../workers/WorkerDetailPage'
import { SchedulesPage } from '../schedules/SchedulesPage'
import { ScheduleDetailPage } from '../schedules/ScheduleDetailPage'
import { AdminPage } from '../admin/AdminPage'
import { AdminRoute } from './AdminRoute'
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
        <Route path="/jobs/new" element={<NewJobPage />} />
        <Route path="/jobs/:id" element={<JobDetailPage />} />
        <Route path="/jobs/:id/tasks/:taskId" element={<TaskLogPage />} />
        <Route path="/workers" element={<WorkersPage />} />
        <Route path="/workers/:id" element={<WorkerDetailPage />} />
        <Route path="/schedules" element={<SchedulesPage />} />
        {/* No AdminRoute: every /v1/scheduled-jobs/{id} route is auth(...) and
            owner-or-admin, 404-on-deny (internal/api/server.go:163-168,
            internal/api/scheduled_jobs.go:147-169). */}
        <Route path="/schedules/:id" element={<ScheduleDetailPage />} />
        <Route element={<AdminRoute />}>
          <Route path="/admin" element={<Navigate to="/admin/users" replace />} />
          <Route path="/admin/:tab" element={<AdminPage />} />
        </Route>
        {/* No AdminRoute: every endpoint behind this page is auth(...) and acts
            on the identity in the bearer token, never on an id from a path or a
            body (internal/api/server.go:97-100, :153). Gating it on admin would
            lock out exactly the users who need it. Same two-route shape as
            /admin above, so UserMenu's /profile link resolves without change. */}
        <Route path="/profile" element={<Navigate to="/profile/identity" replace />} />
        <Route path="/profile/:tab" element={<ProfilePage />} />
      </Route>
      <Route path="*" element={<Navigate to="/jobs" replace />} />
    </Routes>
  )
}
