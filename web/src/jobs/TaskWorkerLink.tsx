import type { MouseEvent } from 'react'
import { Link } from 'react-router-dom'
import type { TaskDetail } from './api'

// A task's worker as a link to its detail page. Click propagation is stopped so
// that inside a selectable row, following the link does not also select the row.
// The link is an inline block with a negative outline offset so its focus ring is drawn
// inside the box and survives the clipping of a truncating cell.
export function TaskWorkerLink({ task }: { task: Pick<TaskDetail, 'worker_id' | 'worker_name'> }) {
  if (!task.worker_id) return <span>-</span>
  return (
    <Link
      to={`/workers/${task.worker_id}`}
      onClick={(e: MouseEvent) => e.stopPropagation()}
      className="inline-block max-w-full truncate align-bottom hover:text-accent focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-accent"
    >
      {task.worker_name || task.worker_id.slice(0, 6)}
    </Link>
  )
}
