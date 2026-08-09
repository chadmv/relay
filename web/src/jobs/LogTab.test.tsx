import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { LogTab } from './LogTab'
import type { LogRow } from './logBuffer'
import type { TaskLogStreamResult } from './useTaskLogStream'

function row(key: number, text: string, over: Partial<LogRow> = {}): LogRow {
  return { key, kind: 'line', stream: 'stdout', text, time: '2026-07-01T00:00:00Z', ...over }
}

function streamOf(over: Partial<TaskLogStreamResult> = {}): TaskLogStreamResult {
  return {
    rows: [row(1, 'building'), row(2, 'warning: x', { stream: 'stderr' })],
    status: 'live',
    attempt: 0,
    dropped: false,
    evicted: false,
    historyTruncated: false,
    total: 2,
    errorMessage: '',
    reconnect: () => {},
    ...over,
  }
}

function renderTab(stream = streamOf(), taskId = 't2') {
  return render(
    <MemoryRouter>
      <LogTab jobId="j1" taskId={taskId} stream={stream} />
    </MemoryRouter>,
  )
}

test('renders log lines with a stdout/stderr distinction', () => {
  renderTab()
  expect(screen.getByText('building')).toBeInTheDocument()
  expect(screen.getByText('warning: x').className).toMatch(/text-err/)
})

// Replaces the old 'shows a STATIC history marker ... never a LIVE badge' case:
// live tailing has shipped, so the honest signal is now the inverse.
test('shows a LIVE badge while the stream is open and no STATIC marker', () => {
  renderTab()
  expect(screen.getByText('LIVE')).toBeInTheDocument()
  expect(screen.queryByText(/static/i)).toBeNull()
  expect(screen.queryByText(/live tailing pending/i)).toBeNull()
})

test('does not show LIVE for a terminal task', () => {
  renderTab(streamOf({ status: 'history' }))
  expect(screen.queryByText('LIVE')).toBeNull()
  expect(screen.getByText('HISTORY')).toBeInTheDocument()
})

test('shows the empty state when there is no output', () => {
  renderTab(streamOf({ rows: [], status: 'history' }))
  expect(screen.getByText(/no log output/i)).toBeInTheDocument()
})

test('shows a retry control on error', () => {
  renderTab(streamOf({ rows: [], status: 'error', errorMessage: 'boom' }))
  expect(screen.getByText(/failed to load logs/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
})

test('links to the full-screen view for the selected task', () => {
  renderTab()
  expect(screen.getByRole('link', { name: /full screen/i })).toHaveAttribute(
    'href',
    '/jobs/j1/tasks/t2',
  )
})

test('omits the full-screen link when no task is selected', () => {
  renderTab(streamOf({ rows: [], status: 'idle' }), '')
  expect(screen.queryByRole('link', { name: /full screen/i })).toBeNull()
})
