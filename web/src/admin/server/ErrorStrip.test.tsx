import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ErrorStrip } from './ErrorStrip'

test('renders the error message and a Retry button', () => {
  render(<ErrorStrip message="500 boom" label="jobs stats" onRetry={() => {}} />)
  expect(screen.getByText('500 boom')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry jobs stats' })).toBeInTheDocument()
})

test('Retry calls back exactly once per click', async () => {
  const onRetry = vi.fn()
  render(<ErrorStrip message="500 boom" label="jobs stats" onRetry={onRetry} />)
  await userEvent.click(screen.getByRole('button', { name: 'Retry jobs stats' }))
  expect(onRetry).toHaveBeenCalledTimes(1)
})

test('the strip is an alert region and the Retry label is distinguished by the label prop', () => {
  render(<ErrorStrip message="500 boom" label="fleet stats" onRetry={() => {}} />)
  expect(screen.getByRole('alert')).toHaveTextContent('500 boom')
  expect(screen.getByRole('button', { name: 'Retry fleet stats' })).toBeInTheDocument()
})
