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
