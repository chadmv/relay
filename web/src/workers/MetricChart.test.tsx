import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { MetricChart } from './MetricChart'

test('renders the title, current value, and a chart path', () => {
  const { container } = render(
    <MetricChart title="CPU" values={[0, 50, 100]} max={100} current="50%" colorClass="text-accent" />,
  )
  expect(screen.getByText('CPU')).toBeInTheDocument()
  expect(screen.getByText('50%')).toBeInTheDocument()
  expect(screen.getByRole('img', { name: 'CPU' })).toBeInTheDocument()
  expect(container.querySelectorAll('path').length).toBe(2)
})

test('renders no path for an empty series', () => {
  const { container } = render(
    <MetricChart title="CPU" values={[]} max={100} current="-" colorClass="text-accent" />,
  )
  expect(container.querySelectorAll('path').length).toBe(0)
})
