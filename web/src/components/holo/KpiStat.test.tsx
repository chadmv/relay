import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { KpiStat } from './KpiStat'

test('renders the label, value, and optional sub', () => {
  render(<KpiStat label="CPU · RAM" value="16c · 128G" sub="os: linux" />)
  expect(screen.getByText('CPU · RAM')).toBeInTheDocument()
  expect(screen.getByText('16c · 128G')).toHaveClass('font-mono', 'text-[22px]', 'text-fg')
  expect(screen.getByText('os: linux')).toHaveClass('font-mono', 'text-[10px]', 'text-fg-mute')
})

test('omits the sub line when not provided', () => {
  render(<KpiStat label="GPU" value="No GPU" />)
  expect(screen.getByText('GPU')).toBeInTheDocument()
  expect(screen.getByText('No GPU')).toBeInTheDocument()
})

test('renders a progress bar when progress is provided', () => {
  const { container } = render(<KpiStat label="Slots" value="2/4" progress={{ used: 2, max: 4 }} />)
  const fill = container.querySelector('[data-testid="progress-fill"]')
  expect(fill).not.toBeNull()
  expect((fill as HTMLElement).style.width).toBe('50%')
})
