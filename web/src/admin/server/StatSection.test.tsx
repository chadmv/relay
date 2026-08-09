import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { StatSection, type StatCell } from './StatSection'

const CELLS: StatCell[] = [
  { label: 'ONLINE', value: 4 },
  { label: 'TOTAL', value: 1234, sub: 'revoked workers excluded', wide: true },
]

const LOADING: StatCell[] = [
  { label: 'ONLINE', value: null },
  { label: 'TOTAL', value: null, sub: 'revoked workers excluded', wide: true },
]

test('renders the caption, the values and the sub-lines', () => {
  render(<StatSection caption="FLEET · GET /v1/workers/stats" cells={CELLS} error={null} onRetry={() => {}} />)
  expect(screen.getByText('FLEET · GET /v1/workers/stats')).toBeInTheDocument()
  expect(screen.getByText('4')).toBeInTheDocument()
  expect(screen.getByText('ONLINE')).toBeInTheDocument()
  expect(screen.getByText('revoked workers excluded')).toBeInTheDocument()
})

test('formats values with toLocaleString, matching the pagination footers', () => {
  render(<StatSection caption="FLEET" cells={CELLS} error={null} onRetry={() => {}} />)
  expect(screen.getByText((1234).toLocaleString())).toBeInTheDocument()
})

test('a null value renders an em dash so the grid does not reflow when data lands', () => {
  render(<StatSection caption="FLEET" cells={LOADING} error={null} onRetry={() => {}} />)
  // Both cells and the caption are present during first paint.
  expect(screen.getByText('FLEET')).toBeInTheDocument()
  expect(screen.getByText('ONLINE')).toBeInTheDocument()
  expect(screen.getAllByText('—')).toHaveLength(2)
})

test('error with NO data replaces the grid in place, keeping the caption', async () => {
  const onRetry = vi.fn()
  render(
    <StatSection caption="FLEET" cells={LOADING} error={new Error('500 boom')} onRetry={onRetry} />,
  )
  expect(screen.getByText('FLEET')).toBeInTheDocument()
  expect(screen.getByText('500 boom')).toBeInTheDocument()
  // No fabricated numbers and no placeholder cells behind the strip.
  expect(screen.queryByText('ONLINE')).not.toBeInTheDocument()
  expect(screen.queryByText('—')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(onRetry).toHaveBeenCalledTimes(1)
})

test('error WITH data keeps the numbers and adds the staleness line', () => {
  render(<StatSection caption="FLEET" cells={CELLS} error={new Error('500 boom')} onRetry={() => {}} />)
  // Blanking good numbers on a dropped poll is worse than marking them stale.
  expect(screen.getByText('4')).toBeInTheDocument()
  expect(screen.getByText((1234).toLocaleString())).toBeInTheDocument()
  expect(screen.getByText('stale · last update failed')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
})

test('no staleness line when there is no error', () => {
  render(<StatSection caption="FLEET" cells={CELLS} error={null} onRetry={() => {}} />)
  expect(screen.queryByText('stale · last update failed')).not.toBeInTheDocument()
})
