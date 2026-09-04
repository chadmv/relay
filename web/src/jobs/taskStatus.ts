import type { TaskStatus } from './api'

interface StatusView {
  text: string
  dot: string
}

// Color mapping for the TASK status vocabulary (distinct from status.ts, which
// only knows the JOB set). done=ok, running/dispatched/preparing=accent,
// pending=warn, failed/timed_out=err.
export function taskStatusColor(status: TaskStatus): StatusView {
  switch (status) {
    case 'done':
      return { text: 'text-ok', dot: 'bg-ok' }
    case 'running':
    case 'dispatched':
    case 'preparing':
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

// The terminal task statuses. A ?task_id= subscription has no terminal signal of
// its own (README.md:1310-1313), so this is what useJob's 3 s poll turns into
// "stop tailing" - which is what makes one log-only connection sufficient.
const TERMINAL: TaskStatus[] = ['done', 'failed', 'timed_out']

export function isTerminalTask(status: TaskStatus | undefined): boolean {
  return status !== undefined && TERMINAL.includes(status)
}
