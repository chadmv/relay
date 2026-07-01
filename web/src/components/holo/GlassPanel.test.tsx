import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { GlassPanel } from './GlassPanel'

test('renders children inside a div by default with the glass base classes', () => {
  render(<GlassPanel>hello</GlassPanel>)
  const el = screen.getByText('hello')
  expect(el.tagName).toBe('DIV')
  expect(el).toHaveClass('rounded-card', 'border', 'border-border', 'backdrop-blur-[8px]')
})

test('merges a caller className after the base classes', () => {
  render(<GlassPanel className="bg-black/25">x</GlassPanel>)
  expect(screen.getByText('x')).toHaveClass('bg-black/25')
})

test('renders as the element named by `as`', () => {
  render(
    <GlassPanel as="section" className="p-4">
      s
    </GlassPanel>,
  )
  expect(screen.getByText('s').tagName).toBe('SECTION')
})
