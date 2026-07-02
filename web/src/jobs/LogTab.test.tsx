import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { LogTab } from './LogTab'
import type { LogEntry } from './api'

const items: LogEntry[] = [
  { seq: 1, stream: 'stdout', content: 'building', created_at: '2026-07-01T00:00:00Z' },
  { seq: 2, stream: 'stderr', content: 'warning: x', created_at: '2026-07-01T00:00:01Z' },
]

test('renders log lines with a stdout/stderr distinction', () => {
  render(<LogTab items={items} isLoading={false} isError={false} onRetry={() => {}} />)
  expect(screen.getByText('building')).toBeInTheDocument()
  const stderrLine = screen.getByText('warning: x')
  expect(stderrLine.className).toMatch(/text-err/)
})

test('shows the empty state when there is no output', () => {
  render(<LogTab items={[]} isLoading={false} isError={false} onRetry={() => {}} />)
  expect(screen.getByText(/no log output/i)).toBeInTheDocument()
})

test('shows a retry control on error', () => {
  render(<LogTab items={[]} isLoading={false} isError={true} onRetry={() => {}} />)
  expect(screen.getByText(/failed to load logs/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
})

test('shows a STATIC history marker and a live-pending note, never a LIVE badge', () => {
  render(<LogTab items={items} isLoading={false} isError={false} onRetry={() => {}} />)
  // Honest signalling: the log is fetch-once history, not a live stream. SSE
  // tailing is backend-blocked (feature-2026-06-26-sse-task-log-publishing).
  expect(screen.getByText(/static|history/i)).toBeInTheDocument()
  expect(screen.getByText(/live tailing pending/i)).toBeInTheDocument()
  // A green LIVE badge would imply a stream we cannot deliver.
  expect(screen.queryByText(/^live$/i)).toBeNull()
})
