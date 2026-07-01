import { render } from '@testing-library/react'
import { expect, test } from 'vitest'
import { ProgressBar } from './ProgressBar'

function fill(container: HTMLElement): HTMLElement {
  const el = container.querySelector('[data-testid="progress-fill"]')
  if (!(el instanceof HTMLElement)) throw new Error('fill not found')
  return el
}

test('sets the fill width from value/max as a percentage', () => {
  const { container } = render(<ProgressBar value={2} max={4} />)
  expect(fill(container).style.width).toBe('50%')
})

test('defaults max to 100 when omitted', () => {
  const { container } = render(<ProgressBar value={30} />)
  expect(fill(container).style.width).toBe('30%')
})

test('clamps out-of-range values to 0..100', () => {
  const { container: hi } = render(<ProgressBar value={9} max={4} />)
  expect(fill(hi).style.width).toBe('100%')
  const { container: lo } = render(<ProgressBar value={-1} max={4} />)
  expect(fill(lo).style.width).toBe('0%')
})

test('renders the accent gradient fill by default and a muted fill via tone', () => {
  const { container } = render(<ProgressBar value={1} max={4} tone="muted" />)
  expect(fill(container)).toHaveClass('bg-white/20')
})
