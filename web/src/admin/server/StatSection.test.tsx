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
  render(
    <StatSection
      caption="FLEET · GET /v1/workers/stats"
      cells={CELLS}
      error={null}
      label="fleet stats"
      dataUpdatedAt={0}
      onRetry={() => {}}
    />,
  )
  expect(screen.getByText('FLEET · GET /v1/workers/stats')).toBeInTheDocument()
  expect(screen.getByText('4')).toBeInTheDocument()
  expect(screen.getByText('ONLINE')).toBeInTheDocument()
  expect(screen.getByText('revoked workers excluded')).toBeInTheDocument()
})

test('formats values as en-US, thousands-separated - not a locale-dependent toLocaleString', () => {
  // A literal, not (1234).toLocaleString(): on a CI runner whose ICU locale has no
  // thousands separator, toLocaleString(1234) === '1234' and this assertion would
  // become tautological against the same call the component makes.
  render(
    <StatSection
      caption="FLEET"
      cells={CELLS}
      error={null}
      label="fleet stats"
      dataUpdatedAt={0}
      onRetry={() => {}}
    />,
  )
  expect(screen.getByText('1,234')).toBeInTheDocument()
})

test('a null value renders an em dash so the grid does not reflow when data lands', () => {
  render(
    <StatSection
      caption="FLEET"
      cells={LOADING}
      error={null}
      label="fleet stats"
      dataUpdatedAt={0}
      onRetry={() => {}}
    />,
  )
  // Both cells and the caption are present during first paint.
  expect(screen.getByText('FLEET')).toBeInTheDocument()
  expect(screen.getByText('ONLINE')).toBeInTheDocument()
  expect(screen.getAllByText('—')).toHaveLength(2)
})

test('error with NO data replaces the grid in place, keeping the caption', async () => {
  const onRetry = vi.fn()
  render(
    <StatSection
      caption="FLEET"
      cells={LOADING}
      error={new Error('500 boom')}
      label="fleet stats"
      dataUpdatedAt={0}
      onRetry={onRetry}
    />,
  )
  expect(screen.getByText('FLEET')).toBeInTheDocument()
  expect(screen.getByText('500 boom')).toBeInTheDocument()
  // No fabricated numbers and no placeholder cells behind the strip.
  expect(screen.queryByText('ONLINE')).not.toBeInTheDocument()
  expect(screen.queryByText('—')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry fleet stats' }))
  expect(onRetry).toHaveBeenCalledTimes(1)
})

test('error WITH data keeps the numbers and adds the staleness line with an age', () => {
  const fiveMinutesAgo = Date.now() - 5 * 60 * 1000
  render(
    <StatSection
      caption="FLEET"
      cells={CELLS}
      error={new Error('500 boom')}
      label="fleet stats"
      dataUpdatedAt={fiveMinutesAgo}
      onRetry={() => {}}
    />,
  )
  // Blanking good numbers on a dropped poll is worse than marking them stale.
  expect(screen.getByText('4')).toBeInTheDocument()
  expect(screen.getByText('1,234')).toBeInTheDocument()
  const stale = screen.getByText(/stale · last update failed/)
  expect(stale.textContent).toContain('5m ago')
  expect(screen.queryByRole('button', { name: /Retry/ })).not.toBeInTheDocument()
})

test('the staleness line is announced to assistive tech', () => {
  render(
    <StatSection
      caption="FLEET"
      cells={CELLS}
      error={new Error('500 boom')}
      label="fleet stats"
      dataUpdatedAt={Date.now()}
      onRetry={() => {}}
    />,
  )
  const status = screen.getByRole('status')
  expect(status).toHaveAttribute('aria-live', 'polite')
  expect(status).toHaveTextContent('stale · last update failed')
})

test('no staleness line when there is no error', () => {
  render(
    <StatSection
      caption="FLEET"
      cells={CELLS}
      error={null}
      label="fleet stats"
      dataUpdatedAt={0}
      onRetry={() => {}}
    />,
  )
  expect(screen.queryByText(/stale · last update failed/)).not.toBeInTheDocument()
})
