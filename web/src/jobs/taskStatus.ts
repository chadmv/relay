import type { TaskStatus } from './api'

interface StatusView {
  text: string
  dot: string
}

// Color mapping for the TASK status vocabulary (distinct from status.ts, which
// only knows the JOB set). done=ok, running/dispatched=accent, pending=warn,
// failed/timed_out=err.
export function taskStatusColor(status: TaskStatus): StatusView {
  switch (status) {
    case 'done':
      return { text: 'text-ok', dot: 'bg-ok' }
    case 'running':
    case 'dispatched':
      return { text: 'text-accent', dot: 'bg-accent' }
    case 'pending':
      return { text: 'text-warn', dot: 'bg-warn' }
    case 'failed':
    case 'timed_out':
      return { text: 'text-err', dot: 'bg-err' }
    default:
      return { text: 'text-fg-mute', dot: 'bg-fg-mute' }
  }
}
