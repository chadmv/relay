import { Link } from 'react-router-dom'
import { LogView } from './LogView'
import type { TaskLogStreamResult } from './useTaskLogStream'

// The job-detail Log pane: LogView plus a link to the full-screen view. All log
// state lives in useTaskLogStream, which JobDetailPage mounts - this component is
// purely presentational, and that is what keeps the "exactly one SSE connection
// per page" guarantee structural rather than a convention (spec Decision 8).
export function LogTab({
  jobId,
  taskId,
  stream,
}: {
  jobId: string
  taskId: string
  stream: TaskLogStreamResult
}) {
  return (
    <LogView
      stream={stream}
      headerExtra={
        taskId ? (
          <Link
            to={`/jobs/${jobId}/tasks/${taskId}`}
            className="tracking-[0.14em] text-accent hover:underline"
          >
            FULL SCREEN
          </Link>
        ) : null
      }
    />
  )
}
