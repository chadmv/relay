import { Navigate, Route, Routes } from 'react-router-dom'
import { LoginScreen } from '../auth/LoginScreen'
import { RegisterScreen } from '../auth/RegisterScreen'
import { JobsPlaceholder } from './JobsPlaceholder'
import { JobsPage } from '../jobs/JobsPage'
import { JobDetailPage } from '../jobs/JobDetailPage'
import { TaskLogPage } from '../jobs/TaskLogPage'
import { NewJobPage } from '../jobs/NewJobPage'
import { WorkersPage } from '../workers/WorkersPage'
import { WorkerDetailPage } from '../workers/WorkerDetailPage'
import { SchedulesPage } from '../schedules/SchedulesPage'
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
        <Route element={<AdminRoute />}>
          <Route path="/admin" element={<Navigate to="/admin/users" replace />} />
          <Route path="/admin/:tab" element={<AdminPage />} />
        </Route>
        <Route path="/profile/*" element={<JobsPlaceholder />} />
      </Route>
      <Route path="*" element={<Navigate to="/jobs" replace />} />
    </Routes>
  )
}
