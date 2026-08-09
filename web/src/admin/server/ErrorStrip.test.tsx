import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ErrorStrip } from './ErrorStrip'

test('renders the error message and a Retry button', () => {
  render(<ErrorStrip message="500 boom" onRetry={() => {}} />)
  expect(screen.getByText('500 boom')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
})

test('Retry calls back exactly once per click', async () => {
  const onRetry = vi.fn()
  render(<ErrorStrip message="500 boom" onRetry={onRetry} />)
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(onRetry).toHaveBeenCalledTimes(1)
})
