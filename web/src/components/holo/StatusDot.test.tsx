import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { StatusDot } from './StatusDot'

test('renders the mono status label for a worker status', () => {
  render(<StatusDot status="online" />)
  expect(screen.getByText('ONLINE')).toHaveClass('font-mono', 'text-ok')
})

test('renders the offline label', () => {
  render(<StatusDot status="offline" />)
  expect(screen.getByText('OFFLINE')).toHaveClass('text-err')
})
